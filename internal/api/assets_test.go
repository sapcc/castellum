// SPDX-FileCopyrightText: 2019 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/majewsky/gg/jsonmatch"
	. "github.com/majewsky/gg/option"
	"github.com/sapcc/go-api-declarations/castellum"
	"github.com/sapcc/go-bits/easypg"
	"github.com/sapcc/go-bits/must"

	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/castellum/internal/test"
)

func TestGetAssets(t *testing.T) {
	s := test.NewSetup(t,
		commonSetupOptionsForAPITest(),
	)
	ctx := t.Context()

	testCommonEndpointBehavior(t, s,
		"/v1/projects/%s/assets/%s")

	expectedAssets := []jsonmatch.Object{
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
			"checked": jsonmatch.Object{
				"error": "unexpected uptime",
			},
			"stale": false,
		},
	}

	// happy path
	s.Validator.Enforcer.Forbid("project:edit:foo") // this should not be an issue
	s.Handler.RespondTo(ctx, "GET /v1/projects/project1/assets/foo").
		ExpectJSON(t, http.StatusOK, jsonmatch.Object{"assets": expectedAssets})
}

func TestGetAsset(t *testing.T) {
	s := test.NewSetup(t,
		commonSetupOptionsForAPITest(),
	)
	ctx := t.Context()

	testCommonEndpointBehavior(t, s,
		"/v1/projects/%s/assets/%s/fooasset1")

	// expect error for unknown asset
	s.Handler.RespondTo(ctx, "GET /v1/projects/project1/assets/foo/doesnotexist").
		ExpectStatus(t, http.StatusNotFound)

	// happy path: just an asset without any operations
	s.Validator.Enforcer.Forbid("project:edit:foo") // this should not be an issue
	response := jsonmatch.Object{
		"id":            "fooasset1",
		"size":          1024,
		"usage_percent": 50,
		"stale":         true,
	}
	s.Handler.RespondTo(ctx, "GET /v1/projects/project1/assets/foo/fooasset1").
		ExpectJSON(t, http.StatusOK, response)

	// check rendering of a pending operation in state "created"
	pendingOp := db.PendingOperation{
		AssetID:   1,
		Reason:    castellum.OperationReasonHigh,
		OldSize:   1024,
		NewSize:   2048,
		Usage:     castellum.UsageValues{castellum.SingularUsageMetric: 768},
		CreatedAt: time.Unix(21, 0).UTC(),
	}
	must.SucceedT(t, s.DB.Insert(&pendingOp))
	pendingOpJSON := jsonmatch.Object{
		"state":    "created",
		"reason":   "high",
		"old_size": 1024,
		"new_size": 2048,
		"created": jsonmatch.Object{
			"at":            21,
			"usage_percent": 75,
		},
	}
	response["pending_operation"] = pendingOpJSON
	s.Handler.RespondTo(ctx, "GET /v1/projects/project1/assets/foo/fooasset1").
		ExpectJSON(t, http.StatusOK, response)

	// check rendering of a pending operation in state "confirmed"
	pendingOp.ConfirmedAt = Some(time.Unix(22, 0).UTC())
	must.SucceedT(t, s.DBUpdate(&pendingOp))
	pendingOpJSON["state"] = "confirmed"
	pendingOpJSON["confirmed"] = jsonmatch.Object{"at": 22}
	s.Handler.RespondTo(ctx, "GET /v1/projects/project1/assets/foo/fooasset1").
		ExpectJSON(t, http.StatusOK, response)

	// check rendering of a pending operation in state "greenlit"
	pendingOp.GreenlitAt = Some(time.Unix(23, 0).UTC())
	must.SucceedT(t, s.DBUpdate(&pendingOp))
	pendingOpJSON["state"] = "greenlit"
	pendingOpJSON["greenlit"] = jsonmatch.Object{"at": 23}
	s.Handler.RespondTo(ctx, "GET /v1/projects/project1/assets/foo/fooasset1").
		ExpectJSON(t, http.StatusOK, response)

	pendingOp.GreenlitByUserUUID = Some("user1")
	must.SucceedT(t, s.DBUpdate(&pendingOp))
	pendingOpJSON["greenlit"] = jsonmatch.Object{"at": 23, "by_user": "user1"}
	s.Handler.RespondTo(ctx, "GET /v1/projects/project1/assets/foo/fooasset1").
		ExpectJSON(t, http.StatusOK, response)

	// check rendering of a scraping error
	must.SucceedT(t, s.DBExec(`UPDATE assets SET scrape_error_message = $1 WHERE id = 1`, "filer is on fire"))
	response["checked"] = jsonmatch.Object{"error": "filer is on fire"}
	s.Handler.RespondTo(ctx, "GET /v1/projects/project1/assets/foo/fooasset1").
		ExpectJSON(t, http.StatusOK, response)

	// check rendering of finished operations in all possible states
	response["finished_operations"] = []jsonmatch.Object{
		{
			"reason":   "low",
			"state":    "cancelled",
			"old_size": 1000,
			"new_size": 900,
			"created": jsonmatch.Object{
				"at":            31,
				"usage_percent": 20,
			},
			"finished": jsonmatch.Object{
				"at": 32,
			},
		},
		{
			"reason":   "high",
			"state":    "succeeded",
			"old_size": 1023,
			"new_size": 1024,
			"created": jsonmatch.Object{
				"at":            41,
				"usage_percent": 80,
			},
			"confirmed": jsonmatch.Object{
				"at": 42,
			},
			"greenlit": jsonmatch.Object{
				"at":      43,
				"by_user": "user2",
			},
			"finished": jsonmatch.Object{
				"at": 44,
			},
		},
		{
			"reason":   "critical",
			"state":    "errored",
			"old_size": 1024,
			"new_size": 1025,
			"created": jsonmatch.Object{
				"at":            51,
				"usage_percent": 96,
			},
			"confirmed": jsonmatch.Object{
				"at": 52,
			},
			"greenlit": jsonmatch.Object{
				"at": 52,
			},
			"finished": jsonmatch.Object{
				"at":    53,
				"error": "datacenter is on fire",
			},
		},
	}
	s.Handler.RespondTo(ctx, "GET /v1/projects/project1/assets/foo/fooasset1?history").
		ExpectJSON(t, http.StatusOK, response)

	// check rendering of an asset that has never had a successful scrape
	must.SucceedT(t, s.DB.Insert(&db.Asset{
		ResourceID:         1,
		UUID:               "fooasset3",
		ScrapeErrorMessage: "filer has stranger anxiety",
	}))
	s.Handler.RespondTo(ctx, "GET /v1/projects/project1/assets/foo/fooasset3").
		ExpectJSON(t, http.StatusOK, jsonmatch.Object{
			"id": "fooasset3",
			"checked": jsonmatch.Object{
				"error": "filer has stranger anxiety",
			},
			"stale":         false,
			"usage_percent": 0,
		})
}

func TestPostAssetErrorResolved(t *testing.T) {
	s := test.NewSetup(t,
		commonSetupOptionsForAPITest(),
	)
	ctx := t.Context()

	tr, tr0 := easypg.NewTracker(t, s.DB.Db)
	tr0.Ignore()

	// endpoint requires cluster access
	s.Validator.Enforcer.Forbid("cluster:access")
	s.Handler.RespondTo(ctx, "POST /v1/projects/project1/assets/foo/fooasset1/error-resolved").
		ExpectStatus(t, http.StatusForbidden)
	s.Validator.Enforcer.Allow("cluster:access")

	// expect error for unknown project
	s.Handler.RespondTo(ctx, "POST /v1/projects/project1/assets/projectdoesnotexist/fooasset1/error-resolved").
		ExpectStatus(t, http.StatusNotFound)

	// expect error for unknown asset
	s.Handler.RespondTo(ctx, "POST /v1/projects/project1/assets/foo/assetdoesnotexist/error-resolved").
		ExpectStatus(t, http.StatusNotFound)

	tr.DBChanges().AssertEmpty()

	// happy path
	s.Handler.RespondTo(ctx, "POST /v1/projects/project1/assets/foo/fooasset1/error-resolved").
		ExpectStatus(t, http.StatusOK)

	tr.DBChanges().AssertEqualf(`
		INSERT INTO finished_operations (asset_id, reason, outcome, old_size, new_size, created_at, confirmed_at, greenlit_at, finished_at, greenlit_by_user_uuid, usage) VALUES (1, 'critical', 'error-resolved', 0, 0, %[1]d, %[1]d, %[1]d, %[1]d, '', 'null');
	`,
		s.Clock.Now().Unix())

	// expect conflict for asset where the last operation is not "errored"
	s.Handler.RespondTo(ctx, "POST /v1/projects/project1/assets/foo/fooasset1/error-resolved").
		ExpectStatus(t, http.StatusConflict)
}
