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

	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/castellum/internal/test"
	"github.com/sapcc/go-bits/assert"
)

func TestGetResourceScrapeErrors(baseT *testing.T) {
	t := test.T{T: baseT}
	withHandler(t, nil, func(h *handler, hh http.Handler, mv *MockValidator, _ []db.Resource, _ []db.Asset) {

		//endpoint requires a token with cluster access
		mv.Forbid("cluster:access")
		assert.HTTPRequest{
			Method:       "GET",
			Path:         "/v1/admin/resource-scrape-errors",
			ExpectStatus: http.StatusForbidden,
		}.Check(t.T, hh)
		mv.Allow("cluster:access")

		//happy path
		assert.HTTPRequest{
			Method:       "GET",
			Path:         "/v1/admin/resource-scrape-errors",
			ExpectStatus: http.StatusOK,
			ExpectBody: assert.JSONObject{
				"resource_scrape_errors": []assert.JSONObject{
					assert.JSONObject{
						"asset_type": "bar",
						"checked": assert.JSONObject{
							"at":    3,
							"error": "datacenter is on fire",
						},
						"domain_id":  "domain1",
						"project_id": "project1",
					},
					assert.JSONObject{
						"asset_type": "foo",
						"checked": assert.JSONObject{
							"at":    6,
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