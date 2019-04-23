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
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"
	"github.com/gophercloud/utils/openstack/clientconfig"
	"github.com/sapcc/castellum/internal/core"
	db "github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/go-bits/logg"
	"github.com/sapcc/go-bits/postlite"
)

func main() {
	dbConn := initDB()
	providerClient := initGophercloud()

	team, err := core.CreateAssetManagers(
		strings.Split(mustGetenv("CASTELLUM_ASSET_MANAGERS"), ","),
		providerClient,
	)
	if err != nil {
		logg.Fatal(err.Error())
	}

	_ = dbConn
	_ = team
	fmt.Println("Hello Castellum")
}

func initDB() *sql.DB {
	dbURL, err := url.Parse(mustGetenv("CASTELLUM_DB_URI"))
	if err != nil {
		logg.Fatal("malformed CASTELLUM_DB_URI: " + err.Error())
	}
	//allow SQLite for testing purposes (TODO really?)
	if dbURL.String() == "sqlite://" {
		dbURL = nil
	}
	dbConn, err := postlite.Connect(postlite.Configuration{
		PostgresURL: dbURL,
		Migrations:  db.SQLMigrations,
	})
	if err != nil {
		logg.Fatal("cannot connect to database: " + err.Error())
	}
	return dbConn
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
