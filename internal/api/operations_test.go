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

func TestGetPendingOperationsForResource(baseT *testing.T) {
	t := test.T{T: baseT}
	h, hh, validator, _, _ := setupTest(t)

	//endpoint requires a token with project access
	validator.Forbid("project:access")
	assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/projects/project1/resources/foo/operations/pending",
		ExpectStatus: http.StatusForbidden,
	}.Check(t.T, hh)
	validator.Allow("project:access")

	//expect error for unknown project or resource
	assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/projects/project2/resources/foo/operations/pending",
		ExpectStatus: http.StatusNotFound,
	}.Check(t.T, hh)
	assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/projects/project1/resources/doesnotexist/operations/pending",
		ExpectStatus: http.StatusNotFound,
	}.Check(t.T, hh)

	//the "unknown" resource exists, but it should be 404 regardless because we
	//don't have an asset manager for it
	assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/projects/project1/resources/unknown/operations/pending",
		ExpectStatus: http.StatusNotFound,
	}.Check(t.T, hh)

	//expect error for inaccessible resource
	validator.Forbid("project:show:foo")
	assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/projects/project1/resources/foo/operations/pending",
		ExpectStatus: http.StatusForbidden,
	}.Check(t.T, hh)
	validator.Allow("project:show:foo")

	//happy path: no pending operations
	validator.Forbid("project:edit:foo") //this should not be an issue
	response := []assert.JSONObject{}
	req := assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/projects/project1/resources/foo/operations/pending",
		ExpectStatus: http.StatusOK,
		ExpectBody:   assert.JSONObject{"pending_operations": response},
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
		"asset_id": "fooasset1",
		"state":    "created",
		"reason":   "high",
		"old_size": 1024,
		"new_size": 2048,
		"created": assert.JSONObject{
			"at":            21,
			"usage_percent": 60,
		},
	}
	req.ExpectBody = assert.JSONObject{
		"pending_operations": []assert.JSONObject{pendingOpJSON},
	}
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
}
