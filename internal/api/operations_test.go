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
	"github.com/sapcc/go-bits/must"

	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/castellum/internal/test"
)

func TestGetPendingOperationsForResource(t *testing.T) {
	s := test.NewSetup(t,
		commonSetupOptionsForAPITest(),
	)
	ctx := t.Context()

	testCommonEndpointBehavior(t, s,
		"/v1/projects/%s/resources/%s/operations/pending")

	// start-data.sql contains no pending operations
	s.Validator.Enforcer.Forbid("project:edit:foo") // this should not be an issue
	s.Handler.RespondTo(ctx, "GET /v1/projects/project1/resources/foo/operations/pending").
		ExpectJSON(t, http.StatusOK, jsonmatch.Object{"pending_operations": jsonmatch.Array{}})

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
		"project_id": "project1",
		"asset_type": "foo",
		"asset_id":   "fooasset1",
		"state":      "created",
		"reason":     "high",
		"old_size":   1024,
		"new_size":   2048,
		"created": jsonmatch.Object{
			"at":            21,
			"usage_percent": 75,
		},
	}
	response := jsonmatch.Object{
		"pending_operations": jsonmatch.Array{pendingOpJSON},
	}
	s.Handler.RespondTo(ctx, "GET /v1/projects/project1/resources/foo/operations/pending").
		ExpectJSON(t, http.StatusOK, response)

	// check rendering of a pending operation in state "confirmed"
	pendingOp.ConfirmedAt = Some(time.Unix(22, 0).UTC())
	must.SucceedT(t, s.DBUpdate(&pendingOp))
	pendingOpJSON["state"] = "confirmed"
	pendingOpJSON["confirmed"] = jsonmatch.Object{"at": 22}
	s.Handler.RespondTo(ctx, "GET /v1/projects/project1/resources/foo/operations/pending").
		ExpectJSON(t, http.StatusOK, response)

	// check rendering of a pending operation in state "greenlit"
	pendingOp.GreenlitAt = Some(time.Unix(23, 0).UTC())
	must.SucceedT(t, s.DBUpdate(&pendingOp))
	pendingOpJSON["state"] = "greenlit"
	pendingOpJSON["greenlit"] = jsonmatch.Object{"at": 23}
	s.Handler.RespondTo(ctx, "GET /v1/projects/project1/resources/foo/operations/pending").
		ExpectJSON(t, http.StatusOK, response)

	pendingOp.GreenlitByUserUUID = Some("user1")
	must.SucceedT(t, s.DBUpdate(&pendingOp))
	pendingOpJSON["greenlit"] = jsonmatch.Object{"at": 23, "by_user": "user1"}
	s.Handler.RespondTo(ctx, "GET /v1/projects/project1/resources/foo/operations/pending").
		ExpectJSON(t, http.StatusOK, response)

	// check querying by domain
	s.Handler.RespondTo(ctx, "GET /v1/operations/pending?domain=domain1").
		ExpectJSON(t, http.StatusOK, response)
	s.Handler.RespondTo(ctx, "GET /v1/operations/pending?domain=domain1&asset-type=foo").
		ExpectJSON(t, http.StatusOK, response)

	// check queries with URL arguments where nothing matches
	s.Handler.RespondTo(ctx, "GET /v1/operations/pending?domain=unknown").
		ExpectStatus(t, http.StatusNotFound)
	s.Handler.RespondTo(ctx, "GET /v1/operations/pending?domain=domain1&project=unknown").
		ExpectStatus(t, http.StatusNotFound)
	s.Handler.RespondTo(ctx, "GET /v1/operations/pending?domain=domain1&asset-type=unknown").
		ExpectStatus(t, http.StatusNotFound)
}

func withEitherFailedOrErroredOperation(action func(castellum.OperationOutcome)) {
	// start-data.sql has a FinishedOperation with outcome "errored". This helper
	// function enables us to re-run tests concerning this "errored" operation with
	// its outcome changed to "failed", to check that "failed" behaves like
	// "errored" for the operations report endpoints.
	action(castellum.OperationOutcomeErrored)
	action(castellum.OperationOutcomeFailed)
}

func TestGetRecentlyFailedOperationsForResource(t *testing.T) {
	withEitherFailedOrErroredOperation(func(failedOperationOutcome castellum.OperationOutcome) {
		s := test.NewSetup(t,
			commonSetupOptionsForAPITest(),
		)
		ctx := t.Context()

		testCommonEndpointBehavior(t, s,
			"/v1/projects/%s/resources/%s/operations/recently-failed")

		must.SucceedT(t, s.DBExec(`UPDATE finished_operations SET outcome = $1 WHERE outcome = $2`,
			failedOperationOutcome, castellum.OperationOutcomeErrored,
		))

		// start-data.sql has a recently failed critical operation for fooasset1, but
		// it will not be shown because fooasset1 does not have critical usage levels
		// anymore
		expectedOps := []jsonmatch.Object{}
		s.Validator.Enforcer.Forbid("project:edit:foo") // this should not be an issue
		s.Handler.RespondTo(ctx, "GET /v1/projects/project1/resources/foo/operations/recently-failed").
			ExpectJSON(t, http.StatusOK, jsonmatch.Object{"recently_failed_operations": expectedOps})

		// to make the recently-failed operation appear, move fooasset1 back to
		// critical usage levels
		must.SucceedT(t, s.DBExec(`UPDATE resources SET critical_threshold_percent = 95 WHERE id = 1`))
		must.SucceedT(t, s.DBExec(`UPDATE assets SET usage = $1 WHERE id = $2`,
			castellum.UsageValues{castellum.SingularUsageMetric: 0.97 * 1024},
			1,
		))
		expectedOps = []jsonmatch.Object{{
			"project_id": "project1",
			"asset_type": "foo",
			"asset_id":   "fooasset1",
			"reason":     "critical",
			"state":      string(failedOperationOutcome),
			"old_size":   1024,
			"new_size":   1025,
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
		}}
		s.Handler.RespondTo(ctx, "GET /v1/projects/project1/resources/foo/operations/recently-failed").
			ExpectJSON(t, http.StatusOK, jsonmatch.Object{"recently_failed_operations": expectedOps})

		// operation should NOT disappear when there is a pending operation that has
		// not yet finished
		must.SucceedT(t, s.DB.Insert(&db.PendingOperation{
			AssetID:   1,
			Reason:    castellum.OperationReasonHigh,
			OldSize:   1024,
			NewSize:   2048,
			Usage:     castellum.UsageValues{castellum.SingularUsageMetric: 768},
			CreatedAt: time.Unix(61, 0).UTC(),
		}))
		s.Handler.RespondTo(ctx, "GET /v1/projects/project1/resources/foo/operations/recently-failed").
			ExpectJSON(t, http.StatusOK, jsonmatch.Object{"recently_failed_operations": expectedOps})

		// operation should disappear when there is a non-failed operation that
		// finished after the failed one
		must.SucceedT(t, s.DB.Insert(&db.FinishedOperation{
			AssetID:     1,
			Reason:      castellum.OperationReasonHigh,
			Outcome:     castellum.OperationOutcomeSucceeded,
			OldSize:     1024,
			NewSize:     2048,
			Usage:       castellum.UsageValues{castellum.SingularUsageMetric: 768},
			CreatedAt:   time.Unix(61, 0).UTC(),
			ConfirmedAt: Some(time.Unix(61, 0).UTC()),
			GreenlitAt:  Some(time.Unix(61, 0).UTC()),
			FinishedAt:  time.Unix(61, 0).UTC(),
		}))
		emptyResponse := jsonmatch.Object{"recently_failed_operations": jsonmatch.Array{}}
		s.Handler.RespondTo(ctx, "GET /v1/projects/project1/resources/foo/operations/recently-failed").
			ExpectJSON(t, http.StatusOK, emptyResponse)

		// check querying by domain
		s.Handler.RespondTo(ctx, "GET /v1/operations/recently-failed?domain=domain1").
			ExpectJSON(t, http.StatusOK, emptyResponse)
		s.Handler.RespondTo(ctx, "GET /v1/operations/recently-failed?domain=domain1&asset-type=foo").
			ExpectJSON(t, http.StatusOK, emptyResponse)

		// check queries with URL arguments where nothing matches
		s.Handler.RespondTo(ctx, "GET /v1/operations/recently-failed?domain=unknown").
			ExpectStatus(t, http.StatusNotFound)
		s.Handler.RespondTo(ctx, "GET /v1/operations/recently-failed?domain=domain1&project=unknown").
			ExpectStatus(t, http.StatusNotFound)
		s.Handler.RespondTo(ctx, "GET /v1/operations/recently-failed?domain=domain1&asset-type=unknown").
			ExpectStatus(t, http.StatusNotFound)
	})
}

func TestGetRecentlySucceededOperationsForResource(t *testing.T) {
	withEitherFailedOrErroredOperation(func(failedOperationOutcome castellum.OperationOutcome) {
		s := test.NewSetup(t,
			commonSetupOptionsForAPITest(),
		)
		ctx := t.Context()

		testCommonEndpointBehavior(t, s,
			"/v1/projects/%s/resources/%s/operations/recently-succeeded")

		must.SucceedT(t, s.DBExec(`UPDATE finished_operations SET outcome = $1 WHERE outcome = $2`,
			failedOperationOutcome, castellum.OperationOutcomeErrored,
		))

		// start-data.sql has a succeeded operation, but also a failed/errored one on the same
		// asset after that, so we should not see anything yet
		expectedOps := []jsonmatch.Object{}
		s.Validator.Enforcer.Forbid("project:edit:foo") // this should not be an issue
		s.Handler.RespondTo(ctx, "GET /v1/projects/project1/resources/foo/operations/recently-succeeded").
			ExpectJSON(t, http.StatusOK, jsonmatch.Object{"recently_succeeded_operations": expectedOps})

		// make the failed operation a cancelled one to surface the succeeded operation
		must.SucceedT(t, s.DBExec(`UPDATE finished_operations SET outcome = $1 WHERE outcome = $2`,
			castellum.OperationOutcomeCancelled, failedOperationOutcome,
		))
		expectedOps = []jsonmatch.Object{{
			"project_id": "project1",
			"asset_type": "foo",
			"asset_id":   "fooasset1",
			"reason":     "high",
			"state":      "succeeded",
			"old_size":   1023,
			"new_size":   1024,
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
		}}
		s.Handler.RespondTo(ctx, "GET /v1/projects/project1/resources/foo/operations/recently-succeeded").
			ExpectJSON(t, http.StatusOK, jsonmatch.Object{"recently_succeeded_operations": expectedOps})

		// operation should NOT disappear when there is a pending operation that has
		// not yet finished
		must.SucceedT(t, s.DB.Insert(&db.PendingOperation{
			AssetID:   1,
			Reason:    castellum.OperationReasonHigh,
			OldSize:   1024,
			NewSize:   2048,
			Usage:     castellum.UsageValues{castellum.SingularUsageMetric: 768},
			CreatedAt: time.Unix(61, 0).UTC(),
		}))
		s.Handler.RespondTo(ctx, "GET /v1/projects/project1/resources/foo/operations/recently-succeeded").
			ExpectJSON(t, http.StatusOK, jsonmatch.Object{"recently_succeeded_operations": expectedOps})

		// check querying by domain
		s.Handler.RespondTo(ctx, "GET /v1/operations/recently-succeeded?domain=domain1").
			ExpectJSON(t, http.StatusOK, jsonmatch.Object{"recently_succeeded_operations": expectedOps})
		s.Handler.RespondTo(ctx, "GET /v1/operations/recently-succeeded?domain=domain1&asset-type=foo").
			ExpectJSON(t, http.StatusOK, jsonmatch.Object{"recently_succeeded_operations": expectedOps})

		// check queries with URL arguments where nothing matches
		s.Handler.RespondTo(ctx, "GET /v1/operations/recently-succeeded?domain=unknown").
			ExpectStatus(t, http.StatusNotFound)
		s.Handler.RespondTo(ctx, "GET /v1/operations/recently-succeeded?domain=domain1&project=unknown").
			ExpectStatus(t, http.StatusNotFound)
		s.Handler.RespondTo(ctx, "GET /v1/operations/recently-succeeded?domain=domain1&asset-type=unknown").
			ExpectStatus(t, http.StatusNotFound)

		// check with shortened max-age
		s.Handler.RespondTo(ctx, "GET /v1/projects/project1/resources/foo/operations/recently-succeeded?max-age=10m").
			ExpectJSON(t, http.StatusOK, jsonmatch.Object{"recently_succeeded_operations": jsonmatch.Array{}})
	})
}
