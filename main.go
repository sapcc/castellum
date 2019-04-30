/*******************************************************************************
*
* Copyright 2019 SAP SE
*
* Licensed under the Apache License, Version 2.0 (the "License");
* you may not use this file except in compliance with the License.
* You should have received a copy of the License along with this
* program. If not, you may obtain a copy of the License at
*
*     http://www.apache.org/licenses/LICENSE-2.0
*
* Unless required by applicable law or agreed to in writing, software
* distributed under the License is distributed on an "AS IS" BASIS,
* WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
* See the License for the specific language governing permissions and
* limitations under the License.
*
*******************************************************************************/

package main

import (
	"database/sql"
	"os"
	"strings"
	"time"

	"github.com/gophercloud/gophercloud/openstack"
	"github.com/gophercloud/utils/openstack/clientconfig"
	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/castellum/internal/observer"
	"github.com/sapcc/go-bits/logg"
	"gopkg.in/gorp.v2"

	//load asset managers
	_ "github.com/sapcc/castellum/internal/plugins"
)

func main() {
	//initialize DB connection
	dbi, err := db.Init(mustGetenv("CASTELLUM_DB_URI"))
	if err != nil {
		logg.Fatal(err.Error())
	}

	//initialize OpenStack connection
	ao, err := clientconfig.AuthOptions(nil)
	if err != nil {
		logg.Fatal("cannot connect to OpenStack: " + err.Error())
	}
	ao.AllowReauth = true
	providerClient, err := openstack.AuthenticatedClient(*ao)
	if err != nil {
		logg.Fatal("cannot connect to OpenStack: " + err.Error())
	}

	//initialize asset managers
	team, err := core.CreateAssetManagers(
		strings.Split(mustGetenv("CASTELLUM_ASSET_MANAGERS"), ","),
		providerClient,
	)
	if err != nil {
		logg.Fatal(err.Error())
	}

	//TODO: implement the other subcommands
	runObserver(dbi, team)
}

func runObserver(dbi *gorp.DbMap, team core.AssetManagerTeam) {
	o := observer.Observer{DB: dbi, Team: team}
	o.ApplyDefaults()

	for _, manager := range team {
		for _, assetType := range manager.AssetTypes() {
			go observerJobLoop(func() error {
				return o.ScrapeNextResource(assetType, time.Now().Add(-30*time.Minute))
			})
			go observerJobLoop(func() error {
				return o.ScrapeNextAsset(assetType, time.Now().Add(-5*time.Minute))
			})
		}
	}
	go func() {
		for {
			err := observer.CollectGarbage(dbi, time.Now().Add(-14*24*time.Hour)) //14 days
			if err != nil {
				logg.Error(err.Error())
			}
			time.Sleep(time.Hour)
		}
	}()

	select {}
}

//Execute a task repeatedly, but slow down when sql.ErrNoRows is returned by it.
//(Observer.ScrapeNextX methods use this error value to indicate that nothing
//needs scraping, so we can back off a bit to avoid useless database load.)
func observerJobLoop(task func() error) {
	for {
		err := task()
		switch err {
		case nil:
			//nothing to do here
		case sql.ErrNoRows:
			//nothing to do right now - slow down a bit to avoid useless DB load
			time.Sleep(10 * time.Second)
		default:
			logg.Error(err.Error())
		}
	}
}

func mustGetenv(key string) string {
	val := os.Getenv(key)
	if val == "" {
		logg.Fatal("missing required environment variable: " + key)
	}
	return val
}
