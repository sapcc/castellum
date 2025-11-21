// SPDX-FileCopyrightText: 2019 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/sapcc/go-api-declarations/castellum"
	"github.com/sapcc/go-bits/assert"

	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/castellum/internal/test"
)

func TestGetPendingOperationsForResource(baseT *testing.T) {
	t := test.T{T: baseT}
	s := test.NewSetup(t.T,
		commonSetupOptionsForAPITest(),
	)
	hh := s.Handler

	testCommonEndpointBehavior(t, hh, s,
		"/v1/projects/%s/resources/%s/operations/pending")

	// start-data.sql contains no pending operations
	s.Validator.Enforcer.Forbid("project:edit:foo") // this should not be an issue
	response := []assert.JSONObject{}
	req := assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/projects/project1/resources/foo/operations/pending",
		ExpectStatus: http.StatusOK,
		ExpectBody:   assert.JSONObject{"pending_operations": response},
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
		"project_id": "project1",
		"asset_type": "foo",
		"asset_id":   "fooasset1",
		"state":      "created",
		"reason":     "high",
		"old_size":   1024,
		"new_size":   2048,
		"created": assert.JSONObject{
			"at":            21,
			"usage_percent": 75,
		},
	}
	req.ExpectBody = assert.JSONObject{
		"pending_operations": []assert.JSONObject{pendingOpJSON},
	}
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

	// check querying by domain
	req.Path = "/v1/operations/pending?domain=domain1"
	req.Check(t.T, hh)
	req.Path = "/v1/operations/pending?domain=domain1&asset-type=foo"
	req.Check(t.T, hh)

	// check queries with URL arguments where nothing matches
	req.ExpectStatus = http.StatusNotFound
	req.ExpectBody = nil
	req.Path = "/v1/operations/pending?domain=unknown"
	req.Check(t.T, hh)
	req.Path = "/v1/operations/pending?domain=domain1&project=unknown"
	req.Check(t.T, hh)
	req.Path = "/v1/operations/pending?domain=domain1&asset-type=unknown"
	req.Check(t.T, hh)
}

func withEitherFailedOrErroredOperation(action func(castellum.OperationOutcome)) {
	// start-data.sql has a FinishedOperation with outcome "errored". This helper
	// function enables us to re-run tests concerning this "errored" operation with
	// its outcome changed to "failed", to check that "failed" behaves like
	// "errored" for the operations report endpoints.
	action(castellum.OperationOutcomeErrored)
	action(castellum.OperationOutcomeFailed)
}

func TestGetRecentlyFailedOperationsForResource(baseT *testing.T) {
	t := test.T{T: baseT}
	withEitherFailedOrErroredOperation(func(failedOperationOutcome castellum.OperationOutcome) {
		s := test.NewSetup(t.T,
			commonSetupOptionsForAPITest(),
		)
		hh := s.Handler

		testCommonEndpointBehavior(t, hh, s,
			"/v1/projects/%s/resources/%s/operations/recently-failed")

		t.MustExec(s.DB, `UPDATE finished_operations SET outcome = $1 WHERE outcome = $2`,
			failedOperationOutcome, castellum.OperationOutcomeErrored,
		)

		// start-data.sql has a recently failed critical operation for fooasset1, but
		// it will not be shown because fooasset1 does not have critical usage levels
		// anymore
		expectedOps := []assert.JSONObject{}
		s.Validator.Enforcer.Forbid("project:edit:foo") // this should not be an issue
		assert.HTTPRequest{
			Method:       "GET",
			Path:         "/v1/projects/project1/resources/foo/operations/recently-failed",
			ExpectStatus: http.StatusOK,
			ExpectBody:   assert.JSONObject{"recently_failed_operations": expectedOps},
		}.Check(t.T, hh)

		// to make the recently-failed operation appear, move fooasset1 back to
		// critical usage levels
		t.MustExec(s.DB, `UPDATE resources SET critical_threshold_percent = 95 WHERE id = 1`)
		t.MustExec(s.DB, `UPDATE assets SET usage = $1 WHERE id = $2`,
			castellum.UsageValues{castellum.SingularUsageMetric: 0.97 * 1024},
			1,
		)
		expectedOps = []assert.JSONObject{{
			"project_id": "project1",
			"asset_type": "foo",
			"asset_id":   "fooasset1",
			"reason":     "critical",
			"state":      string(failedOperationOutcome),
			"old_size":   1024,
			"new_size":   1025,
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
		}}
		assert.HTTPRequest{
			Method:       "GET",
			Path:         "/v1/projects/project1/resources/foo/operations/recently-failed",
			ExpectStatus: http.StatusOK,
			ExpectBody:   assert.JSONObject{"recently_failed_operations": expectedOps},
		}.Check(t.T, hh)

		// operation should NOT disappear when there is a pending operation that has
		// not yet finished
		t.Must(s.DB.Insert(&db.PendingOperation{
			AssetID:   1,
			Reason:    castellum.OperationReasonHigh,
			OldSize:   1024,
			NewSize:   2048,
			Usage:     castellum.UsageValues{castellum.SingularUsageMetric: 768},
			CreatedAt: time.Unix(61, 0).UTC(),
		}))
		assert.HTTPRequest{
			Method:       "GET",
			Path:         "/v1/projects/project1/resources/foo/operations/recently-failed",
			ExpectStatus: http.StatusOK,
			ExpectBody:   assert.JSONObject{"recently_failed_operations": expectedOps},
		}.Check(t.T, hh)

		// operation should disappear when there is a non-failed operation that
		// finished after the failed one
		t.Must(s.DB.Insert(&db.FinishedOperation{
			AssetID:     1,
			Reason:      castellum.OperationReasonHigh,
			Outcome:     castellum.OperationOutcomeSucceeded,
			OldSize:     1024,
			NewSize:     2048,
			Usage:       castellum.UsageValues{castellum.SingularUsageMetric: 768},
			CreatedAt:   time.Unix(61, 0).UTC(),
			ConfirmedAt: p2time(time.Unix(61, 0).UTC()),
			GreenlitAt:  p2time(time.Unix(61, 0).UTC()),
			FinishedAt:  time.Unix(61, 0).UTC(),
		}))
		expectedOps = []assert.JSONObject{}
		req := assert.HTTPRequest{
			Method:       "GET",
			Path:         "/v1/projects/project1/resources/foo/operations/recently-failed",
			ExpectStatus: http.StatusOK,
			ExpectBody:   assert.JSONObject{"recently_failed_operations": expectedOps},
		}
		req.Check(t.T, hh)

		// check querying by domain
		req.Path = "/v1/operations/recently-failed?domain=domain1"
		req.Check(t.T, hh)
		req.Path = "/v1/operations/recently-failed?domain=domain1&asset-type=foo"
		req.Check(t.T, hh)

		// check queries with URL arguments where nothing matches
		req.ExpectStatus = http.StatusNotFound
		req.ExpectBody = nil
		req.Path = "/v1/operations/recently-failed?domain=unknown"
		req.Check(t.T, hh)
		req.Path = "/v1/operations/recently-failed?domain=domain1&project=unknown"
		req.Check(t.T, hh)
		req.Path = "/v1/operations/recently-failed?domain=domain1&asset-type=unknown"
		req.Check(t.T, hh)
	})
}

func TestGetRecentlySucceededOperationsForResource(baseT *testing.T) {
	t := test.T{T: baseT}

	withEitherFailedOrErroredOperation(func(failedOperationOutcome castellum.OperationOutcome) {
		s := test.NewSetup(t.T,
			commonSetupOptionsForAPITest(),
		)
		hh := s.Handler

		testCommonEndpointBehavior(t, hh, s,
			"/v1/projects/%s/resources/%s/operations/recently-succeeded")

		t.MustExec(s.DB, `UPDATE finished_operations SET outcome = $1 WHERE outcome = $2`,
			failedOperationOutcome, castellum.OperationOutcomeErrored,
		)

		// start-data.sql has a succeeded operation, but also a failed/errored one on the same
		// asset after that, so we should not see anything yet
		expectedOps := []assert.JSONObject{}
		s.Validator.Enforcer.Forbid("project:edit:foo") // this should not be an issue
		assert.HTTPRequest{
			Method:       "GET",
			Path:         "/v1/projects/project1/resources/foo/operations/recently-succeeded",
			ExpectStatus: http.StatusOK,
			ExpectBody:   assert.JSONObject{"recently_succeeded_operations": expectedOps},
		}.Check(t.T, hh)

		// make the failed operation a cancelled one to surface the succeeded operation
		t.MustExec(s.DB, `UPDATE finished_operations SET outcome = $1 WHERE outcome = $2`,
			castellum.OperationOutcomeCancelled, failedOperationOutcome,
		)
		expectedOps = []assert.JSONObject{{
			"project_id": "project1",
			"asset_type": "foo",
			"asset_id":   "fooasset1",
			"reason":     "high",
			"state":      "succeeded",
			"old_size":   1023,
			"new_size":   1024,
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
		}}
		assert.HTTPRequest{
			Method:       "GET",
			Path:         "/v1/projects/project1/resources/foo/operations/recently-succeeded",
			ExpectStatus: http.StatusOK,
			ExpectBody:   assert.JSONObject{"recently_succeeded_operations": expectedOps},
		}.Check(t.T, hh)

		// operation should NOT disappear when there is a pending operation that has
		// not yet finished
		t.Must(s.DB.Insert(&db.PendingOperation{
			AssetID:   1,
			Reason:    castellum.OperationReasonHigh,
			OldSize:   1024,
			NewSize:   2048,
			Usage:     castellum.UsageValues{castellum.SingularUsageMetric: 768},
			CreatedAt: time.Unix(61, 0).UTC(),
		}))
		req := assert.HTTPRequest{
			Method:       "GET",
			Path:         "/v1/projects/project1/resources/foo/operations/recently-succeeded",
			ExpectStatus: http.StatusOK,
			ExpectBody:   assert.JSONObject{"recently_succeeded_operations": expectedOps},
		}
		req.Check(t.T, hh)

		// check querying by domain
		req.Path = "/v1/operations/recently-succeeded?domain=domain1"
		req.Check(t.T, hh)
		req.Path = "/v1/operations/recently-succeeded?domain=domain1&asset-type=foo"
		req.Check(t.T, hh)

		// check queries with URL arguments where nothing matches
		req.ExpectStatus = http.StatusNotFound
		req.ExpectBody = nil
		req.Path = "/v1/operations/recently-succeeded?domain=unknown"
		req.Check(t.T, hh)
		req.Path = "/v1/operations/recently-succeeded?domain=domain1&project=unknown"
		req.Check(t.T, hh)
		req.Path = "/v1/operations/recently-succeeded?domain=domain1&asset-type=unknown"
		req.Check(t.T, hh)

		// check with shortened max-age
		assert.HTTPRequest{
			Method:       "GET",
			Path:         "/v1/projects/project1/resources/foo/operations/recently-succeeded?max-age=10m",
			ExpectStatus: http.StatusOK,
			ExpectBody:   assert.JSONObject{"recently_succeeded_operations": []assert.JSONObject{}},
		}.Check(t.T, hh)
	})
}
