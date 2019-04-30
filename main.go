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
	"bufio"
	"database/sql"
	"fmt"
	"os"
	"strconv"
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

func usage() {
	fmt.Fprintf(os.Stderr,
		"usage:\n\t%s [api|observer|worker]\n\t%s test-asset-type <type>\n",
		os.Args[0], os.Args[0],
	)
	os.Exit(1)
}

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

	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "api":
		if len(os.Args) != 2 {
			usage()
		}
		fmt.Println("TODO unimplemented")
	case "observer":
		if len(os.Args) != 2 {
			usage()
		}
		runObserver(dbi, team)
	case "worker":
		if len(os.Args) != 2 {
			usage()
		}
		fmt.Println("TODO unimplemented")
	case "test-asset-type":
		if len(os.Args) != 3 {
			usage()
		}
		runAssetTypeTestShell(dbi, team, os.Args[2])
	default:
		usage()
	}
}

func mustGetenv(key string) string {
	val := os.Getenv(key)
	if val == "" {
		logg.Fatal("missing required environment variable: " + key)
	}
	return val
}

////////////////////////////////////////////////////////////////////////////////
// task: observer

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

////////////////////////////////////////////////////////////////////////////////
// task: test-asset-type

func runAssetTypeTestShell(dbi *gorp.DbMap, team core.AssetManagerTeam, assetType string) {
	manager := team.ForAssetType(assetType)
	if manager == nil {
		logg.Fatal("no manager configured for asset type: %q", assetType)
	}

	fmt.Println("")
	fmt.Println("supported commands:")
	fmt.Println("\tlist   <project-id>                            - calls manager.ListAssets()")
	fmt.Println("\tshow   <project-id> <asset-id>                 - calls manager.GetAssetStatus() with previousStatus == nil")
	fmt.Println("\tshow   <project-id> <asset-id> <size> <usage%> - calls manager.GetAssetStatus() with previousStatus != nil")
	fmt.Println("\tresize <project-id> <asset-id> <size>          - calls manager.SetAssetSize()")
	fmt.Println("")

	stdin := bufio.NewReader(os.Stdin)
	for {
		//show prompt
		os.Stdout.Write([]byte("> "))
		input, err := stdin.ReadString('\n')
		if err != nil {
			logg.Fatal(err.Error())
		}

		fields := strings.Fields(strings.TrimSpace(input))
		if len(fields) == 0 {
			continue
		}
		var res db.Resource
		if len(fields) > 1 {
			res.AssetType = assetType
			res.ScopeUUID = fields[1]
		}

		switch fields[0] {
		case "list":
			if len(fields) != 2 {
				logg.Error("wrong number of arguments")
				continue
			}
			result, err := manager.ListAssets(res)
			if err != nil {
				logg.Error(err.Error())
				continue
			}
			for idx, assetUUID := range result {
				logg.Info("result[%d] = %q", idx, assetUUID)
			}

		case "show":
			var previousStatus *core.AssetStatus
			switch len(fields) {
			case 3:
				previousStatus = nil
			case 5:
				size, err := strconv.ParseUint(fields[3], 10, 64)
				if err != nil {
					logg.Error(err.Error())
					continue
				}
				usagePerc, err := strconv.ParseUint(fields[4], 10, 32)
				if err != nil {
					logg.Error(err.Error())
					continue
				}
				previousStatus = &core.AssetStatus{Size: size, UsagePercent: uint32(usagePerc)}
			default:
				logg.Error("wrong number of arguments")
				continue
			}
			result, err := manager.GetAssetStatus(res, fields[2], previousStatus)
			if err != nil {
				logg.Error(err.Error())
				continue
			}
			logg.Info("size = %d, usage = %d%%", result.Size, result.UsagePercent)

		case "resize":
			if len(fields) != 4 {
				logg.Error("wrong number of arguments")
				continue
			}
			newSize, err := strconv.ParseUint(fields[3], 10, 64)
			if err != nil {
				logg.Error(err.Error())
				continue
			}
			err = manager.SetAssetSize(res, fields[2], newSize)
			if err != nil {
				logg.Error(err.Error())
				continue
			}

		default:
			logg.Error(err.Error())
		}
	}
}
