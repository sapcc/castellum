// SPDX-FileCopyrightText: 2020 SAP SE or an SAP affiliate company
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

func TestGetResourceScrapeErrors(t *testing.T) {
	s := test.NewSetup(t,
		commonSetupOptionsForAPITest(),
	)
	ctx := t.Context()

	// endpoint requires a token with cluster access
	s.Validator.Enforcer.Forbid("cluster:access")
	s.Handler.RespondTo(ctx, "GET /v1/admin/resource-scrape-errors").
		ExpectStatus(t, http.StatusForbidden)
	s.Validator.Enforcer.Allow("cluster:access")

	// happy path
	s.Handler.RespondTo(ctx, "GET /v1/admin/resource-scrape-errors").
		ExpectJSON(t, http.StatusOK, jsonmatch.Object{
			"resource_scrape_errors": []jsonmatch.Object{
				{
					"asset_type": "bar",
					"checked": jsonmatch.Object{
						"error": "datacenter is on fire",
					},
					"domain_id":  "domain1",
					"project_id": "project1",
				},
				{
					"asset_type": "foo",
					"checked": jsonmatch.Object{
						"error": "datacenter is on fire",
					},
					"domain_id":  "domain1",
					"project_id": "something-else",
				},
			},
		})
}

func TestGetAssetScrapeErrors(t *testing.T) {
	s := test.NewSetup(t,
		commonSetupOptionsForAPITest(),
	)
	ctx := t.Context()

	// endpoint requires a token with cluster access
	s.Validator.Enforcer.Forbid("cluster:access")
	s.Handler.RespondTo(ctx, "GET /v1/admin/asset-scrape-errors").
		ExpectStatus(t, http.StatusForbidden)
	s.Validator.Enforcer.Allow("cluster:access")

	s.Handler.RespondTo(ctx, "GET /v1/admin/asset-scrape-errors").
		ExpectJSON(t, http.StatusOK, jsonmatch.Object{
			"asset_scrape_errors": []jsonmatch.Object{
				{
					"asset_id":   "fooasset2",
					"asset_type": "foo",
					"checked": jsonmatch.Object{
						"error": "unexpected uptime",
					},
					"domain_id":  "domain1",
					"project_id": "project1",
				},
			},
		})
}

func TestGetAssetResizeErrors(t *testing.T) {
	s := test.NewSetup(t,
		commonSetupOptionsForAPITest(),
	)
	ctx := t.Context()

	// endpoint requires a token with cluster access
	s.Validator.Enforcer.Forbid("cluster:access")
	s.Handler.RespondTo(ctx, "GET /v1/admin/asset-resize-errors").
		ExpectStatus(t, http.StatusForbidden)
	s.Validator.Enforcer.Allow("cluster:access")

	// check that the "errored" resize operation is rendered properly
	s.Handler.RespondTo(ctx, "GET /v1/admin/asset-resize-errors").
		ExpectJSON(t, http.StatusOK, jsonmatch.Object{
			"asset_resize_errors": []jsonmatch.Object{
				{
					"asset_id":   "fooasset1",
					"asset_type": "foo",
					"domain_id":  "domain1",
					"finished": jsonmatch.Object{
						"at":    53,
						"error": "datacenter is on fire",
					},
					"new_size":   1025,
					"old_size":   1024,
					"project_id": "project1",
				},
			},
		})

	// add a new operation on the same asset that results with outcome
	// "succeeded" and check that we get an empty list
	must.SucceedT(t, s.DB.Insert(&db.FinishedOperation{
		AssetID:     1,
		Reason:      castellum.OperationReasonCritical,
		Outcome:     castellum.OperationOutcomeSucceeded,
		OldSize:     1024,
		NewSize:     1025,
		Usage:       castellum.UsageValues{castellum.SingularUsageMetric: 983.04},
		CreatedAt:   time.Unix(70, 0).UTC(),
		ConfirmedAt: Some(time.Unix(71, 0).UTC()),
		GreenlitAt:  Some(time.Unix(71, 0).UTC()),
		FinishedAt:  time.Unix(73, 0).UTC(),
	}))
	s.Handler.RespondTo(ctx, "GET /v1/admin/asset-resize-errors").
		ExpectJSON(t, http.StatusOK, jsonmatch.Object{
			"asset_resize_errors": jsonmatch.Array{},
		})
}
