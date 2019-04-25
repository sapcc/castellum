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
	"fmt"
	"os"
	"strings"

	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"
	"github.com/gophercloud/utils/openstack/clientconfig"
	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/castellum/internal/observer"
	"github.com/sapcc/go-bits/logg"

	//load asset managers
	_ "github.com/sapcc/castellum/internal/plugins"
)

func main() {
	dbi, err := db.Init(mustGetenv("CASTELLUM_DB_URI"))
	if err != nil {
		logg.Fatal(err.Error())
	}
	providerClient := initGophercloud()

	team, err := core.CreateAssetManagers(
		strings.Split(mustGetenv("CASTELLUM_ASSET_MANAGERS"), ","),
		providerClient,
	)
	if err != nil {
		logg.Fatal(err.Error())
	}

	o := observer.Observer{DB: dbi, Team: team}
	o.ApplyDefaults()
	fmt.Println("Hello Castellum")
}

func initGophercloud() *gophercloud.ProviderClient {
	ao, err := clientconfig.AuthOptions(nil)
	if err != nil {
		logg.Fatal("cannot connect to OpenStack: " + err.Error())
	}
	ao.AllowReauth = true
	providerClient, err := openstack.AuthenticatedClient(*ao)
	if err != nil {
		logg.Fatal("cannot connect to OpenStack: " + err.Error())
	}
	return providerClient
}

func mustGetenv(key string) string {
	val := os.Getenv(key)
	if val == "" {
		logg.Fatal("missing required environment variable: " + key)
	}
	return val
}
