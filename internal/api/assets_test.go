// SPDX-FileCopyrightText: 2019 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/sapcc/go-api-declarations/castellum"
	"github.com/sapcc/go-bits/assert"
	"github.com/sapcc/go-bits/audittools"
	"github.com/sapcc/go-bits/easypg"
	"github.com/sapcc/go-bits/mock"

	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/castellum/internal/test"
)

func TestGetAssets(baseT *testing.T) {
	t := test.T{T: baseT}
	withHandler(t, core.Config{}, nil, func(_ test.Setup, hh http.Handler, _ core.AssetManagerTeam, mv *mock.Validator[*mock.Enforcer], _ *audittools.MockAuditor, _ []db.Resource, _ []db.Asset) {
		testCommonEndpointBehavior(t, hh, mv,
			"/v1/projects/%s/assets/%s")

		expectedAssets := []assert.JSONObject{
			{
				"id":            "fooasset1",
				"size":          1024,
				"usage_percent": 50,
				"stale":         true,
			},
			{
				"id":            "fooasset2",
				"size":          512,
				"usage_percent": 80,
				"min_size":      256,
				"max_size":      1024,
				"checked": assert.JSONObject{
					"error": "unexpected uptime",
				},
				"stale": false,
			},
		}

		// happy path
		mv.Enforcer.Forbid("project:edit:foo") // this should not be an issue
		assert.HTTPRequest{
			Method:       "GET",
			Path:         "/v1/projects/project1/assets/foo",
			ExpectStatus: http.StatusOK,
			ExpectBody:   assert.JSONObject{"assets": expectedAssets},
		}.Check(t.T, hh)
	})
}

func TestGetAsset(baseT *testing.T) {
	t := test.T{T: baseT}
	withHandler(t, core.Config{}, nil, func(s test.Setup, hh http.Handler, _ core.AssetManagerTeam, mv *mock.Validator[*mock.Enforcer], _ *audittools.MockAuditor, _ []db.Resource, _ []db.Asset) {
		testCommonEndpointBehavior(t, hh, mv,
			"/v1/projects/%s/assets/%s/fooasset1")

		// expect error for unknown asset
		assert.HTTPRequest{
			Method:       "GET",
			Path:         "/v1/projects/project1/assets/foo/doesnotexist",
			ExpectStatus: http.StatusNotFound,
		}.Check(t.T, hh)

		// happy path: just an asset without any operations
		mv.Enforcer.Forbid("project:edit:foo") // this should not be an issue
		response := assert.JSONObject{
			"id":            "fooasset1",
			"size":          1024,
			"usage_percent": 50,
			"stale":         true,
		}
		req := assert.HTTPRequest{
			Method:       "GET",
			Path:         "/v1/projects/project1/assets/foo/fooasset1",
			ExpectStatus: http.StatusOK,
			ExpectBody:   response,
		}
		req.Check(t.T, hh)

		// check rendering of a pending operation in state "created"
		pendingOp := db.PendingOperation{
			AssetID:   1,
			Reason:    castellum.OperationReasonHigh,
			OldSize:   1024,
			NewSize:   2048,
			Usage:     castellum.UsageValues{castellum.SingularUsageMetric: 768},
			CreatedAt: time.Unix(21, 0).UTC(),
		}
		t.Must(s.DB.Insert(&pendingOp))
		pendingOpJSON := assert.JSONObject{
			"state":    "created",
			"reason":   "high",
			"old_size": 1024,
			"new_size": 2048,
			"created": assert.JSONObject{
				"at":            21,
				"usage_percent": 75,
			},
		}
		response["pending_operation"] = pendingOpJSON
		req.Check(t.T, hh)

		// check rendering of a pending operation in state "confirmed"
		pendingOp.ConfirmedAt = p2time(time.Unix(22, 0).UTC())
		t.MustUpdate(s.DB, &pendingOp)
		pendingOpJSON["state"] = "confirmed"
		pendingOpJSON["confirmed"] = assert.JSONObject{"at": 22}
		req.Check(t.T, hh)

		// check rendering of a pending operation in state "greenlit"
		pendingOp.GreenlitAt = p2time(time.Unix(23, 0).UTC())
		t.MustUpdate(s.DB, &pendingOp)
		pendingOpJSON["state"] = "greenlit"
		pendingOpJSON["greenlit"] = assert.JSONObject{"at": 23}
		req.Check(t.T, hh)

		pendingOp.GreenlitByUserUUID = p2string("user1")
		t.MustUpdate(s.DB, &pendingOp)
		pendingOpJSON["greenlit"] = assert.JSONObject{"at": 23, "by_user": "user1"}
		req.Check(t.T, hh)

		// check rendering of a scraping error
		t.MustExec(s.DB, `UPDATE assets SET scrape_error_message = $1 WHERE id = 1`, "filer is on fire")
		response["checked"] = assert.JSONObject{
			"error": "filer is on fire",
		}
		req.Check(t.T, hh)

		// check rendering of finished operations in all possible states
		response["finished_operations"] = []assert.JSONObject{
			{
				"reason":   "low",
				"state":    "cancelled",
				"old_size": 1000,
				"new_size": 900,
				"created": assert.JSONObject{
					"at":            31,
					"usage_percent": 20,
				},
				"finished": assert.JSONObject{
					"at": 32,
				},
			},
			{
				"reason":   "high",
				"state":    "succeeded",
				"old_size": 1023,
				"new_size": 1024,
				"created": assert.JSONObject{
					"at":            41,
					"usage_percent": 80,
				},
				"confirmed": assert.JSONObject{
					"at": 42,
				},
				"greenlit": assert.JSONObject{
					"at":      43,
					"by_user": "user2",
				},
				"finished": assert.JSONObject{
					"at": 44,
				},
			},
			{
				"reason":   "critical",
				"state":    "errored",
				"old_size": 1024,
				"new_size": 1025,
				"created": assert.JSONObject{
					"at":            51,
					"usage_percent": 96,
				},
				"confirmed": assert.JSONObject{
					"at": 52,
				},
				"greenlit": assert.JSONObject{
					"at": 52,
				},
				"finished": assert.JSONObject{
					"at":    53,
					"error": "datacenter is on fire",
				},
			},
		}
		req.Path += "?history"
		req.Check(t.T, hh)

		// check rendering of an asset that has never had a successful scrape
		t.Must(s.DB.Insert(&db.Asset{
			ResourceID:         1,
			UUID:               "fooasset3",
			ScrapeErrorMessage: "filer has stranger anxiety",
		}))
		assert.HTTPRequest{
			Method:       "GET",
			Path:         "/v1/projects/project1/assets/foo/fooasset3",
			ExpectStatus: http.StatusOK,
			ExpectBody: assert.JSONObject{
				"id": "fooasset3",
				"checked": assert.JSONObject{
					"error": "filer has stranger anxiety",
				},
				"stale":         false,
				"usage_percent": 0,
			},
		}.Check(t.T, hh)
	})
}

func TestPostAssetErrorResolved(baseT *testing.T) {
	t := test.T{T: baseT}
	clock := mock.NewClock()
	clock.StepBy(time.Hour)
	withHandler(t, core.Config{}, clock.Now, func(s test.Setup, hh http.Handler, _ core.AssetManagerTeam, mv *mock.Validator[*mock.Enforcer], _ *audittools.MockAuditor, _ []db.Resource, _ []db.Asset) {
		tr, tr0 := easypg.NewTracker(t.T, s.DB.Db)
		tr0.Ignore()

		// endpoint requires cluster access
		mv.Enforcer.Forbid("cluster:access")
		assert.HTTPRequest{
			Method:       "POST",
			Path:         "/v1/projects/project1/assets/foo/fooasset1/error-resolved",
			ExpectStatus: http.StatusForbidden,
		}.Check(t.T, hh)
		mv.Enforcer.Allow("cluster:access")

		// expect error for unknown project
		assert.HTTPRequest{
			Method:       "POST",
			Path:         "/v1/projects/project1/assets/projectdoesnotexist/fooasset1/error-resolved",
			ExpectStatus: http.StatusNotFound,
		}.Check(t.T, hh)

		// expect error for unknown asset
		assert.HTTPRequest{
			Method:       "POST",
			Path:         "/v1/projects/project1/assets/foo/assetdoesnotexist/error-resolved",
			ExpectStatus: http.StatusNotFound,
		}.Check(t.T, hh)

		tr.DBChanges().AssertEmpty()

		// happy path
		req := assert.HTTPRequest{
			Method:       "POST",
			Path:         "/v1/projects/project1/assets/foo/fooasset1/error-resolved",
			ExpectStatus: http.StatusOK,
		}
		req.Check(t.T, hh)

		tr.DBChanges().AssertEqualf(`
			INSERT INTO finished_operations (asset_id, reason, outcome, old_size, new_size, created_at, confirmed_at, greenlit_at, finished_at, greenlit_by_user_uuid, usage) VALUES (1, 'critical', 'error-resolved', 0, 0, %[1]d, %[1]d, %[1]d, %[1]d, '', 'null');
		`,
			clock.Now().Unix())

		// expect conflict for asset where the last operation is not "errored"
		assert.HTTPRequest{
			Method:       "POST",
			Path:         "/v1/projects/project1/assets/foo/fooasset1/error-resolved",
			ExpectStatus: http.StatusConflict,
		}.Check(t.T, hh)
	})
}

func p2string(val string) *string {
	return &val
}
func p2time(val time.Time) *time.Time {
	return &val
}
