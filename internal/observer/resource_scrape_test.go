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

package observer

import (
	"testing"
	"time"

	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/castellum/internal/plugins"
	"github.com/sapcc/go-bits/postlite"
	"gopkg.in/gorp.v2"
)

//FakeClock is a clock that only changes when we tell it to.
type FakeClock int64

//Now is a double for time.Now().
func (f *FakeClock) Now() time.Time {
	return time.Unix(int64(*f), 0).UTC()
}

//Step advances the clock by one second.
func (f *FakeClock) Step() {
	*f++
}

func setupObserver(t *testing.T) (*Observer, *plugins.AssetManagerStatic, *FakeClock) {
	dbi, err := db.Init("postgres://postgres@localhost:54321/castellum?sslmode=disable")
	if err != nil {
		t.Error(err)
	}

	//wipe the DB clean if there are any leftovers from the previous test run
	mustExec(t, dbi, "DELETE FROM resources")
	mustExec(t, dbi, "DELETE FROM assets")
	mustExec(t, dbi, "DELETE FROM pending_operations")
	mustExec(t, dbi, "DELETE FROM finished_operations")
	//reset all primary key sequences for reproducible row IDs
	mustExec(t, dbi, "ALTER SEQUENCE resources_id_seq RESTART WITH 1")
	mustExec(t, dbi, "ALTER SEQUENCE assets_id_seq RESTART WITH 1")
	mustExec(t, dbi, "ALTER SEQUENCE pending_operations_id_seq RESTART WITH 1")

	amStatic := &plugins.AssetManagerStatic{
		AssetType: "foo",
	}
	//clock starts at an easily recognizable value
	clockVar := FakeClock(99990)
	clock := &clockVar

	return &Observer{
		DB: dbi,
		Team: core.AssetManagerTeam{
			amStatic,
		},
		TimeNow: clock.Now,
	}, amStatic, clock
}

func TestResourceScraping(t *testing.T) {
	o, amStatic, clock := setupObserver(t)

	//create some project resources for testing
	must(t, o.DB.Insert(&db.Resource{
		ScopeUUID: "project1",
		AssetType: "foo",
	}))
	must(t, o.DB.Insert(&db.Resource{
		ScopeUUID: "project2",
		AssetType: "bar", //note: different asset type
	}))
	must(t, o.DB.Insert(&db.Resource{
		ScopeUUID: "project3",
		AssetType: "foo",
	}))

	//create some mock assets that ScrapeNextResource() can find
	amStatic.Assets = map[string]map[string]plugins.StaticAsset{
		"project1": {
			"asset1": {Size: 1000, Usage: 400},
			"asset2": {Size: 2000, Usage: 1000},
		},
		"project2": {
			"asset3": {Size: 3000, Usage: 500},
			"asset4": {Size: 4000, Usage: 800},
		},
		"project3": {
			"asset5": {Size: 5000, Usage: 2500},
			"asset6": {Size: 6000, Usage: 2520},
		},
	}

	//first ScrapeNextResource() should scrape project1/foo
	clock.Step()
	must(t, o.ScrapeNextResource("foo"))
	postlite.AssertDBContent(t, o.DB.Db, "fixtures/resource-scrape-1.sql")

	//first ScrapeNextResource() should scrape project3/foo
	//(NOT project2 because its resource has a different asset type)
	clock.Step()
	must(t, o.ScrapeNextResource("foo"))
	postlite.AssertDBContent(t, o.DB.Db, "fixtures/resource-scrape-2.sql")
}

func must(t *testing.T, err error) {
	if err != nil {
		t.Fatal(err.Error())
	}
}

func mustExec(t *testing.T, dbi *gorp.DbMap, query string) {
	_, err := dbi.Exec(query)
	must(t, err)
}
