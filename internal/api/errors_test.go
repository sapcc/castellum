// SPDX-FileCopyrightText: 2020 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"net/http"
	"testing"
	"time"

	. "github.com/majewsky/gg/option"
	"github.com/sapcc/go-api-declarations/castellum"
	"github.com/sapcc/go-bits/assert"
	"github.com/sapcc/go-bits/must"

	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/castellum/internal/test"
)

func TestGetResourceScrapeErrors(t *testing.T) {
	s := test.NewSetup(t,
		commonSetupOptionsForAPITest(),
	)
	hh := s.Handler

	// endpoint requires a token with cluster access
	s.Validator.Enforcer.Forbid("cluster:access")
	assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/admin/resource-scrape-errors",
		ExpectStatus: http.StatusForbidden,
	}.Check(t, hh)
	s.Validator.Enforcer.Allow("cluster:access")

	// happy path
	assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/admin/resource-scrape-errors",
		ExpectStatus: http.StatusOK,
		ExpectBody: assert.JSONObject{
			"resource_scrape_errors": []assert.JSONObject{
				{
					"asset_type": "bar",
					"checked": assert.JSONObject{
						"error": "datacenter is on fire",
					},
					"domain_id":  "domain1",
					"project_id": "project1",
				},
				{
					"asset_type": "foo",
					"checked": assert.JSONObject{
						"error": "datacenter is on fire",
					},
					"domain_id":  "domain1",
					"project_id": "something-else",
				},
			},
		},
	}.Check(t, hh)
}

func TestGetAssetScrapeErrors(t *testing.T) {
	s := test.NewSetup(t,
		commonSetupOptionsForAPITest(),
	)
	hh := s.Handler

	// endpoint requires a token with cluster access
	s.Validator.Enforcer.Forbid("cluster:access")
	assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/admin/asset-scrape-errors",
		ExpectStatus: http.StatusForbidden,
	}.Check(t, hh)
	s.Validator.Enforcer.Allow("cluster:access")

	assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/admin/asset-scrape-errors",
		ExpectStatus: http.StatusOK,
		ExpectBody: assert.JSONObject{
			"asset_scrape_errors": []assert.JSONObject{
				{
					"asset_id":   "fooasset2",
					"asset_type": "foo",
					"checked": assert.JSONObject{
						"error": "unexpected uptime",
					},
					"domain_id":  "domain1",
					"project_id": "project1",
				},
			},
		},
	}.Check(t, hh)
}

func TestGetAssetResizeErrors(t *testing.T) {
	s := test.NewSetup(t,
		commonSetupOptionsForAPITest(),
	)
	hh := s.Handler

	// endpoint requires a token with cluster access
	s.Validator.Enforcer.Forbid("cluster:access")
	assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/admin/asset-resize-errors",
		ExpectStatus: http.StatusForbidden,
	}.Check(t, hh)
	s.Validator.Enforcer.Allow("cluster:access")

	// check that the "errored" resize operation is rendered properly
	req := assert.HTTPRequest{
		Method:       "GET",
		Path:         "/v1/admin/asset-resize-errors",
		ExpectStatus: http.StatusOK,
		ExpectBody: assert.JSONObject{
			"asset_resize_errors": []assert.JSONObject{
				{
					"asset_id":   "fooasset1",
					"asset_type": "foo",
					"domain_id":  "domain1",
					"finished": assert.JSONObject{
						"at":    53,
						"error": "datacenter is on fire",
					},
					"new_size":   1025,
					"old_size":   1024,
					"project_id": "project1",
				},
			},
		},
	}
	req.Check(t, hh)

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
	req.ExpectBody = assert.JSONObject{
		"asset_resize_errors": []assert.JSONObject{},
	}
	req.Check(t, hh)
}
