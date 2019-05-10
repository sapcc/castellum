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
