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
	testCommonEndpointBehavior(t, hh, validator,
		"/v1/projects/%s/resources/%s/operations/pending")

	//start-data.sql contains no pending operations
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

func TestGetRecentlyFailedOperationsForResource(baseT *testing.T) {
	t := test.T{T: baseT}
	h, hh, validator, _, _ := setupTest(t)
	testCommonEndpointBehavior(t, hh, validator,
		"/v1/projects/%s/resources/%s/operations/recently-failed")

	//start-data.sql has a recently failed critical operation for fooasset1, but
	//it will not be shown because fooasset1 does not have critical usage levels
	//anymore
	expectedOps := []assert.JSONObject{}
	validator.Forbid("project:edit:foo") //this should not be an issue
	assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/projects/project1/resources/foo/operations/recently-failed",
		ExpectStatus: http.StatusOK,
		ExpectBody:   assert.JSONObject{"recently_failed_operations": expectedOps},
	}.Check(t.T, hh)

	//to make the recently-failed operation appear, move fooasset1 back to
	//critical usage levels
	t.MustExec(h.DB, `UPDATE resources SET critical_threshold_percent = 95 WHERE id = 1`)
	t.MustExec(h.DB, `UPDATE assets SET usage_percent = 97 WHERE id = 1`)
	expectedOps = []assert.JSONObject{{
		"asset_id": "fooasset1",
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
	}}
	assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/projects/project1/resources/foo/operations/recently-failed",
		ExpectStatus: http.StatusOK,
		ExpectBody:   assert.JSONObject{"recently_failed_operations": expectedOps},
	}.Check(t.T, hh)

	//operation should NOT disappear when there is a pending operation that has
	//not yet finished
	t.Must(h.DB.Insert(&db.PendingOperation{
		AssetID:      1,
		Reason:       db.OperationReasonHigh,
		OldSize:      1024,
		NewSize:      2048,
		UsagePercent: 60,
		CreatedAt:    time.Unix(61, 0).UTC(),
	}))
	assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/projects/project1/resources/foo/operations/recently-failed",
		ExpectStatus: http.StatusOK,
		ExpectBody:   assert.JSONObject{"recently_failed_operations": expectedOps},
	}.Check(t.T, hh)

	//operation should disappear when there is a non-failed operation that
	//finished after the failed one
	t.Must(h.DB.Insert(&db.FinishedOperation{
		AssetID:      1,
		Reason:       db.OperationReasonHigh,
		Outcome:      db.OperationOutcomeSucceeded,
		OldSize:      1024,
		NewSize:      2048,
		UsagePercent: 60,
		CreatedAt:    time.Unix(61, 0).UTC(),
		ConfirmedAt:  p2time(time.Unix(61, 0).UTC()),
		GreenlitAt:   p2time(time.Unix(61, 0).UTC()),
		FinishedAt:   time.Unix(61, 0).UTC(),
	}))
	expectedOps = []assert.JSONObject{}
	assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/projects/project1/resources/foo/operations/recently-failed",
		ExpectStatus: http.StatusOK,
		ExpectBody:   assert.JSONObject{"recently_failed_operations": expectedOps},
	}.Check(t.T, hh)
}