/******************************************************************************
*
*  Copyright 2020 SAP SE
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

	"github.com/sapcc/go-api-declarations/castellum"
	"github.com/sapcc/go-bits/assert"
	"github.com/sapcc/go-bits/mock"

	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/castellum/internal/test"
)

func TestGetResourceScrapeErrors(baseT *testing.T) {
	t := test.T{T: baseT}
	withHandler(t, core.Config{}, nil, func(_ *handler, hh http.Handler, mv *mock.Validator[*mock.Enforcer], _ []db.Resource, _ []db.Asset) {
		//endpoint requires a token with cluster access
		mv.Enforcer.Forbid("cluster:access")
		assert.HTTPRequest{
			Method:       "GET",
			Path:         "/v1/admin/resource-scrape-errors",
			ExpectStatus: http.StatusForbidden,
		}.Check(t.T, hh)
		mv.Enforcer.Allow("cluster:access")

		//happy path
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
		}.Check(t.T, hh)
	})
}

func TestGetAssetScrapeErrors(baseT *testing.T) {
	t := test.T{T: baseT}
	withHandler(t, core.Config{}, nil, func(_ *handler, hh http.Handler, mv *mock.Validator[*mock.Enforcer], _ []db.Resource, _ []db.Asset) {
		//endpoint requires a token with cluster access
		mv.Enforcer.Forbid("cluster:access")
		assert.HTTPRequest{
			Method:       "GET",
			Path:         "/v1/admin/asset-scrape-errors",
			ExpectStatus: http.StatusForbidden,
		}.Check(t.T, hh)
		mv.Enforcer.Allow("cluster:access")

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
		}.Check(t.T, hh)
	})
}

func TestGetAssetResizeErrors(baseT *testing.T) {
	t := test.T{T: baseT}
	withHandler(t, core.Config{}, nil, func(h *handler, hh http.Handler, mv *mock.Validator[*mock.Enforcer], _ []db.Resource, _ []db.Asset) {
		//endpoint requires a token with cluster access
		mv.Enforcer.Forbid("cluster:access")
		assert.HTTPRequest{
			Method:       "GET",
			Path:         "/v1/admin/asset-resize-errors",
			ExpectStatus: http.StatusForbidden,
		}.Check(t.T, hh)
		mv.Enforcer.Allow("cluster:access")

		//check that the "errored" resize operation is rendered properly
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
		req.Check(t.T, hh)

		//add a new operation on the same asset that results with outcome
		//"succeeded" and check that we get an empty list
		t.Must(h.DB.Insert(&db.FinishedOperation{
			AssetID:     1,
			Reason:      castellum.OperationReasonCritical,
			Outcome:     castellum.OperationOutcomeSucceeded,
			OldSize:     1024,
			NewSize:     1025,
			Usage:       castellum.UsageValues{castellum.SingularUsageMetric: 983.04},
			CreatedAt:   time.Unix(70, 0).UTC(),
			ConfirmedAt: p2time(time.Unix(71, 0).UTC()),
			GreenlitAt:  p2time(time.Unix(71, 0).UTC()),
			FinishedAt:  time.Unix(73, 0).UTC(),
		}))
		req.ExpectBody = assert.JSONObject{
			"asset_resize_errors": []assert.JSONObject{},
		}
		req.Check(t.T, hh)
	})
}
