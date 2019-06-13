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

	policy "github.com/databus23/goslo.policy"
	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/castellum/internal/plugins"
	"github.com/sapcc/castellum/internal/test"
	"github.com/sapcc/go-bits/gopherpolicy"
)

func setupTest(t test.T) (*handler, http.Handler, *MockValidator, []db.Resource, []db.Asset) {
	baseline := "fixtures/start-data.sql"
	dbi := t.PrepareDB(&baseline)
	team := core.AssetManagerTeam{
		&plugins.AssetManagerStatic{AssetType: "foo"},
		&plugins.AssetManagerStatic{AssetType: "bar"},
	}
	mv := &MockValidator{}

	var resources []db.Resource
	_, err := dbi.Select(&resources, `SELECT * FROM resources ORDER BY ID`)
	t.Must(err)

	var assets []db.Asset
	_, err = dbi.Select(&assets, `SELECT * FROM assets ORDER BY ID`)
	t.Must(err)

	h := &handler{DB: dbi, Team: team, Validator: mv}
	return h, h.BuildRouter(), mv, resources, assets
}

//MockValidator implements the gopherpolicy.Enforcer and gopherpolicy.Validator
//interfaces.
type MockValidator struct {
	ForbiddenRules map[string]bool
}

func (mv *MockValidator) Allow(rule string) {
	if mv.ForbiddenRules == nil {
		mv.ForbiddenRules = make(map[string]bool)
	}
	mv.ForbiddenRules[rule] = false
}

func (mv *MockValidator) Forbid(rule string) {
	if mv.ForbiddenRules == nil {
		mv.ForbiddenRules = make(map[string]bool)
	}
	mv.ForbiddenRules[rule] = true
}

func (mv *MockValidator) CheckToken(r *http.Request) *gopherpolicy.Token {
	return &gopherpolicy.Token{
		Enforcer: mv,
		Context:  policy.Context{},
	}
}

func (mv *MockValidator) Enforce(rule string, ctx policy.Context) bool {
	return !mv.ForbiddenRules[rule]
}

func p2uint64(x uint64) *uint64 {
	return &x
}
