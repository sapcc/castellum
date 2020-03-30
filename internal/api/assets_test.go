/******************************************************************************
*
*  Copyright 2019 SAP SE
*
*  Licensed under the Apache License, Version 2.0 (the "License");
*  you may not use this file except in compliance with the License.
*  You may obtain a copy of the License at
*
*      http://www.apache.org/licenses/LICENSE-2.0
*
*  Unless required by applicable law or agreed to in writing, software
*  distributed under the License is distributed on an "AS IS" BASIS,
*  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
*  See the License for the specific language governing permissions and
*  limitations under the License.
*
******************************************************************************/

package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/castellum/internal/test"
	"github.com/sapcc/go-bits/assert"
)

func TestGetAssets(baseT *testing.T) {
	t := test.T{T: baseT}
	withHandler(t, nil, func(h *handler, hh http.Handler, mv *MockValidator, _ []db.Resource, _ []db.Asset) {
		testCommonEndpointBehavior(t, hh, mv,
			"/v1/projects/%s/assets/%s")

		expectedAssets := []assert.JSONObject{
			{
				"id":            "fooasset1",
				"size":          1024,
				"usage_percent": 50,
				"scraped_at":    11,
				"stale":         true,
			},
			{
				"id":            "fooasset2",
				"size":          512,
				"usage_percent": 80,
				"scraped_at":    12,
				"checked": assert.JSONObject{
					"at":    15,
					"error": "unexpected uptime",
				},
				"stale": false,
			},
		}

		//happy path
		mv.Forbid("project:edit:foo") //this should not be an issue
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
	withHandler(t, nil, func(h *handler, hh http.Handler, mv *MockValidator, _ []db.Resource, _ []db.Asset) {
		testCommonEndpointBehavior(t, hh, mv,
			"/v1/projects/%s/assets/%s/fooasset1")

		//expect error for unknown asset
		assert.HTTPRequest{
			Method:       "GET",
			Path:         "/v1/projects/project1/assets/foo/doesnotexist",
			ExpectStatus: http.StatusNotFound,
		}.Check(t.T, hh)

		//happy path: just an asset without any operations
		mv.Forbid("project:edit:foo") //this should not be an issue
		response := assert.JSONObject{
			"id":            "fooasset1",
			"size":          1024,
			"usage_percent": 50,
			"scraped_at":    11,
			"stale":         true,
		}
		req := assert.HTTPRequest{
			Method:       "GET",
			Path:         "/v1/projects/project1/assets/foo/fooasset1",
			ExpectStatus: http.StatusOK,
			ExpectBody:   response,
		}
		req.Check(t.T, hh)

		//check rendering of a pending operation in state "created"
		pendingOp := db.PendingOperation{
			AssetID:      1,
			Reason:       db.OperationReasonHigh,
			OldSize:      1024,
			NewSize:      2048,
			UsagePercent: 60,
			CreatedAt:    time.Unix(21, 0).UTC(),
		}
		t.Must(h.DB.Insert(&pendingOp))
		pendingOpJSON := assert.JSONObject{
			"state":    "created",
			"reason":   "high",
			"old_size": 1024,
			"new_size": 2048,
			"created": assert.JSONObject{
				"at":            21,
				"usage_percent": 60,
			},
		}
		response["pending_operation"] = pendingOpJSON
		req.Check(t.T, hh)

		//check rendering of a pending operation in state "confirmed"
		pendingOp.ConfirmedAt = p2time(time.Unix(22, 0).UTC())
		t.MustUpdate(h.DB, &pendingOp)
		pendingOpJSON["state"] = "confirmed"
		pendingOpJSON["confirmed"] = assert.JSONObject{"at": 22}
		req.Check(t.T, hh)

		//check rendering of a pending operation in state "greenlit"
		pendingOp.GreenlitAt = p2time(time.Unix(23, 0).UTC())
		t.MustUpdate(h.DB, &pendingOp)
		pendingOpJSON["state"] = "greenlit"
		pendingOpJSON["greenlit"] = assert.JSONObject{"at": 23}
		req.Check(t.T, hh)

		pendingOp.GreenlitByUserUUID = p2string("user1")
		t.MustUpdate(h.DB, &pendingOp)
		pendingOpJSON["greenlit"] = assert.JSONObject{"at": 23, "by_user": "user1"}
		req.Check(t.T, hh)

		//check rendering of a scraping error
		t.MustExec(h.DB, `UPDATE assets SET checked_at = UNIX(12), scrape_error_message = $1 WHERE id = 1`, "filer is on fire")
		response["checked"] = assert.JSONObject{
			"at":    12,
			"error": "filer is on fire",
		}
		req.Check(t.T, hh)

		//check rendering of finished operations in all possible states
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
					"usage_percent": 97,
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

		//check rendering of an asset that has never had a successful scrape
		t.Must(h.DB.Insert(&db.Asset{
			ResourceID:         1,
			UUID:               "fooasset3",
			CheckedAt:          time.Unix(42, 0).UTC(),
			ScrapeErrorMessage: "filer has stranger anxiety",
		}))
		assert.HTTPRequest{
			Method:       "GET",
			Path:         "/v1/projects/project1/assets/foo/fooasset3",
			ExpectStatus: http.StatusOK,
			ExpectBody: assert.JSONObject{
				"id": "fooasset3",
				"checked": assert.JSONObject{
					"at":    42,
					"error": "filer has stranger anxiety",
				},
				"stale": false,
			},
		}.Check(t.T, hh)
	})
}

func p2string(val string) *string {
	return &val
}
func p2time(val time.Time) *time.Time {
	return &val
}
