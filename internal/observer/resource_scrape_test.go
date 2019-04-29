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
	"database/sql"
	"testing"

	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/castellum/internal/plugins"
	"github.com/sapcc/go-bits/postlite"
)

func TestResourceScraping(t *testing.T) {
	o, amStatic, clock := setupObserver(t)

	//ScrapeNextResource() without any resources just does nothing
	err := o.ScrapeNextResource("foo", o.TimeNow())
	if err != sql.ErrNoRows {
		t.Errorf("expected sql.ErrNoRows, got %s instead", err.Error())
	}
	postlite.AssertDBContent(t, o.DB.Db, "fixtures/resource-scrape-0.sql")

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
	must(t, o.ScrapeNextResource("foo", o.TimeNow()))
	postlite.AssertDBContent(t, o.DB.Db, "fixtures/resource-scrape-1.sql")

	//first ScrapeNextResource() should scrape project3/foo
	//(NOT project2 because its resource has a different asset type)
	clock.Step()
	must(t, o.ScrapeNextResource("foo", o.TimeNow()))
	postlite.AssertDBContent(t, o.DB.Db, "fixtures/resource-scrape-2.sql")

	//next ScrapeNextResource() should scrape project1/foo again because its
	//scraped_at timestamp is the smallest; there should be no changes except for
	//resources.scraped_at
	clock.Step()
	must(t, o.ScrapeNextResource("foo", o.TimeNow()))
	postlite.AssertDBContent(t, o.DB.Db, "fixtures/resource-scrape-3.sql")

	//simulate deletion of an asset
	delete(amStatic.Assets["project3"], "asset6")
	clock.Step()
	must(t, o.ScrapeNextResource("foo", o.TimeNow()))
	postlite.AssertDBContent(t, o.DB.Db, "fixtures/resource-scrape-4.sql")

	//simulate addition of a new asset
	amStatic.Assets["project1"]["asset7"] = plugins.StaticAsset{Size: 10, Usage: 3}
	clock.Step()
	must(t, o.ScrapeNextResource("foo", o.TimeNow()))
	postlite.AssertDBContent(t, o.DB.Db, "fixtures/resource-scrape-5.sql")

	//check behavior on a resource without assets
	must(t, o.DB.Insert(&db.Resource{
		ScopeUUID: "project4",
		AssetType: "foo",
	}))
	amStatic.Assets["project4"] = nil
	clock.Step()
	must(t, o.ScrapeNextResource("foo", o.TimeNow()))
	postlite.AssertDBContent(t, o.DB.Db, "fixtures/resource-scrape-6.sql")
}
