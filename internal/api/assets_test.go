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
	_, hh, validator, _, _ := setupTest(t)

	//endpoint requires a token with project access
	validator.Forbid("project:access")
	assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/projects/project1/assets/foo",
		ExpectStatus: http.StatusForbidden,
	}.Check(t.T, hh)
	validator.Allow("project:access")

	//expect error for unknown project or resource
	assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/projects/project2/assets/foo",
		ExpectStatus: http.StatusNotFound,
	}.Check(t.T, hh)
	assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/projects/project1/assets/doesnotexist",
		ExpectStatus: http.StatusNotFound,
	}.Check(t.T, hh)

	//the "unknown" resource exists, but it should be 404 regardless because we
	//don't have an asset manager for it
	assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/projects/project1/assets/unknown",
		ExpectStatus: http.StatusNotFound,
	}.Check(t.T, hh)

	//expect error for inaccessible resource
	validator.Forbid("project:show:foo")
	assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/projects/project1/assets/foo",
		ExpectStatus: http.StatusForbidden,
	}.Check(t.T, hh)
	validator.Allow("project:show:foo")

	//happy path
	validator.Forbid("project:edit:foo") //this should not be an issue
	assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/projects/project1/assets/foo",
		ExpectStatus: http.StatusOK,
		ExpectBody: assert.JSONObject{
			"assets": []assert.JSONObject{
				{"id": "fooasset1"},
				{"id": "fooasset2"},
			},
		},
	}.Check(t.T, hh)
}

func TestGetAsset(baseT *testing.T) {
	t := test.T{T: baseT}
	h, hh, validator, _, _ := setupTest(t)

	//endpoint requires a token with project access
	validator.Forbid("project:access")
	assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/projects/project1/assets/foo/fooasset1",
		ExpectStatus: http.StatusForbidden,
	}.Check(t.T, hh)
	validator.Allow("project:access")

	//expect error for unknown project, resource or asset
	assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/projects/project2/assets/foo/fooasset1",
		ExpectStatus: http.StatusNotFound,
	}.Check(t.T, hh)
	assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/projects/project1/assets/doesnotexist/fooasset1",
		ExpectStatus: http.StatusNotFound,
	}.Check(t.T, hh)
	assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/projects/project1/assets/foo/doesnotexist",
		ExpectStatus: http.StatusNotFound,
	}.Check(t.T, hh)

	//the "unknown" resource exists, but it should be 404 regardless because we
	//don't have an asset manager for it
	assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/projects/project1/assets/unknown/bogusasset",
		ExpectStatus: http.StatusNotFound,
	}.Check(t.T, hh)

	//expect error for inaccessible resource
	validator.Forbid("project:show:foo")
	assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/projects/project1/assets/foo/fooasset1",
		ExpectStatus: http.StatusForbidden,
	}.Check(t.T, hh)
	validator.Allow("project:show:foo")

	//happy path: just an asset without any operations
	validator.Forbid("project:edit:foo") //this should not be an issue
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
			"state":    "failed",
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
}

func p2string(val string) *string {
	return &val
}
func p2time(val time.Time) *time.Time {
	return &val
}