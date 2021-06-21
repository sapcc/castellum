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
	"context"
	"database/sql"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/dlmiddlecote/sqlstats"
	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"
	"github.com/gophercloud/utils/openstack/clientconfig"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/cors"
	"github.com/sapcc/castellum/internal/api"
	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/castellum/internal/tasks"
	"github.com/sapcc/go-bits/gopherpolicy"
	"github.com/sapcc/go-bits/httpee"
	"github.com/sapcc/go-bits/logg"
	"github.com/streadway/amqp"
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
	logg.ShowDebug, _ = strconv.ParseBool(os.Getenv("CASTELLUM_DEBUG"))

	//initialize DB connection
	dbUsername := envOrDefault("CASTELLUM_DB_USERNAME", "postgres")
	dbPass := os.Getenv("CASTELLUM_DB_PASSWORD")
	dbHost := envOrDefault("CASTELLUM_DB_HOSTNAME", "localhost")
	dbPort := envOrDefault("CASTELLUM_DB_PORT", "5432")
	dbName := envOrDefault("CASTELLUM_DB_NAME", "castellum")
	dbConnOpts := os.Getenv("CASTELLUM_DB_CONNECTION_OPTIONS")

	dbURL := &url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(dbUsername, dbPass),
		Host:     net.JoinHostPort(dbHost, dbPort),
		Path:     dbName,
		RawQuery: dbConnOpts,
	}
	dbi, err := db.Init(dbURL)
	if err != nil {
		logg.Fatal(err.Error())
	}
	prometheus.MustRegister(sqlstats.NewStatsCollector("castellum", dbi.Db))

	//initialize OpenStack connection
	ao, err := clientconfig.AuthOptions(nil)
	if err != nil {
		logg.Fatal("cannot find OpenStack credentials: " + err.Error())
	}
	ao.AllowReauth = true
	baseProvider, err := openstack.AuthenticatedClient(*ao)
	if err != nil {
		logg.Fatal("cannot connect to OpenStack: " + err.Error())
	}
	baseProvider.UserAgent.Prepend("castellum")
	eo := gophercloud.EndpointOpts{
		//note that empty values are acceptable in both fields
		Region:       os.Getenv("OS_REGION_NAME"),
		Availability: gophercloud.Availability(os.Getenv("OS_INTERFACE")),
	}
	providerClient, err := core.WrapProviderClient(baseProvider, eo)
	if err != nil {
		logg.Fatal("cannot find Keystone V3 API: " + err.Error())
	}

	//get HTTP listen address
	httpListenAddr := os.Getenv("CASTELLUM_HTTP_LISTEN_ADDRESS")
	if httpListenAddr == "" {
		httpListenAddr = ":8080"
	}

	//initialize asset managers
	team, err := core.CreateAssetManagers(
		strings.Split(mustGetenv("CASTELLUM_ASSET_MANAGERS"), ","),
		providerClient, eo,
	)
	if err != nil {
		logg.Fatal(err.Error())
	}

	//get max asset sizes
	cfg := core.Config{
		MaxAssetSize: make(map[db.AssetType]*uint64),
	}
	maxAssetSizes := strings.Split(mustGetenv("CASTELLUM_MAX_ASSET_SIZES"), ",")
	for _, v := range maxAssetSizes {
		sL := strings.Split(v, "=")
		if len(sL) != 2 {
			logg.Fatal("expected a max asset size configuration value in the format: '<asset-type>=<max-asset-size>', got: %s", v)
		}
		assetType := sL[0]
		maxSize, err := strconv.ParseUint(sL[1], 10, 64)
		if err != nil {
			logg.Fatal(err.Error())
		}

		found := false
		for _, assetManager := range team {
			info := assetManager.InfoForAssetType(db.AssetType(assetType))
			if info != nil {
				found = true
				cfg.MaxAssetSize[info.AssetType] = &maxSize
			}
		}
		if !found {
			logg.Fatal("unknown asset type: %s", assetType)
		}
	}

	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "api":
		if len(os.Args) != 2 {
			usage()
		}
		runAPI(&cfg, dbi, team, providerClient, eo, httpListenAddr)
	case "observer":
		if len(os.Args) != 2 {
			usage()
		}
		runObserver(dbi, team, httpListenAddr)
	case "worker":
		if len(os.Args) != 2 {
			usage()
		}
		runWorker(dbi, team, httpListenAddr)
	case "test-asset-type":
		if len(os.Args) != 3 {
			usage()
		}
		runAssetTypeTestShell(dbi, team, db.AssetType(os.Args[2]))
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

func envOrDefault(key, defaultVal string) string {
	val := os.Getenv(key)
	if val == "" {
		val = defaultVal
	}
	return val
}

////////////////////////////////////////////////////////////////////////////////
// task: API

func runAPI(cfg *core.Config, dbi *gorp.DbMap, team core.AssetManagerTeam, providerClient *core.ProviderClient, eo gophercloud.EndpointOpts, httpListenAddr string) {
	tv := gopherpolicy.TokenValidator{
		IdentityV3: providerClient.KeystoneV3,
		Cacher:     gopherpolicy.InMemoryCacher(),
	}
	err := tv.LoadPolicyFile(mustGetenv("CASTELLUM_OSLO_POLICY_PATH"))
	if err != nil {
		logg.Fatal("cannot load oslo.policy: " + err.Error())
	}

	//wrap the main API handler in several layers of middleware (CORS is
	//deliberately the outermost middleware, to exclude preflight checks from
	//logging)
	handler := api.NewHandler(cfg, dbi, team, &tv, providerClient)
	handler = logg.Middleware{}.Wrap(handler)
	handler = cors.New(cors.Options{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"HEAD", "GET", "POST", "PUT", "DELETE"},
		AllowedHeaders: []string{"Content-Type", "User-Agent", "X-Auth-Token"},
	}).Handler(handler)

	//Start audit logging.
	rabbitQueueName := os.Getenv("CASTELLUM_RABBITMQ_QUEUE_NAME")
	if rabbitQueueName != "" {
		username := envOrDefault("CASTELLUM_RABBITMQ_USERNAME", "guest")
		pass := envOrDefault("CASTELLUM_RABBITMQ_PASSWORD", "guest")
		hostname := envOrDefault("CASTELLUM_RABBITMQ_HOSTNAME", "localhost")
		port, err := strconv.Atoi(envOrDefault("CASTELLUM_RABBITMQ_PORT", "5672"))
		if err != nil {
			logg.Fatal("invalid value for CASTELLUM_RABBITMQ_PORT: " + err.Error())
		}
		rabbitURI := amqp.URI{
			Scheme:   "amqp",
			Host:     hostname,
			Port:     port,
			Username: username,
			Password: pass,
			Vhost:    "/",
		}
		api.StartAuditLogging(rabbitQueueName, rabbitURI)
	}

	//metrics and healthcheck are deliberately not covered by any of the
	//middlewares - we do not want to log those requests
	http.Handle("/", handler)
	http.Handle("/metrics", promhttp.Handler())
	http.Handle("/healthcheck", http.HandlerFunc(healthCheckHandler))

	logg.Info("listening on " + httpListenAddr)
	err = httpee.ListenAndServeContext(httpee.ContextWithSIGINT(context.Background(), 10*time.Second), httpListenAddr, nil)
	if err != nil {
		logg.Error(err.Error())
	}
}

func healthCheckHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if r.URL.Path == "/healthcheck" && r.Method == "GET" {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	} else {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("not found"))
	}
}

////////////////////////////////////////////////////////////////////////////////
// task: observer

func runObserver(dbi *gorp.DbMap, team core.AssetManagerTeam, httpListenAddr string) {
	c := tasks.Context{DB: dbi, Team: team}
	c.ApplyDefaults()
	prometheus.MustRegister(tasks.StateMetricsCollector{Context: c})

	//The observer process has a budget of 16 DB connections. Since there are
	//much more assets than resources, we give most of these (12 of 16) to asset
	//scraping. The rest is split between resource scrape and garbage collection.
	for idx := 0; idx < 12; idx++ {
		go queuedJobLoop(func() error {
			return c.ScrapeNextAsset(time.Now().Add(-5 * time.Minute))
		})
	}
	for idx := 0; idx < 3; idx++ {
		go queuedJobLoop(func() error {
			return c.ScrapeNextResource(time.Now().Add(-30 * time.Minute))
		})
	}
	go cronJobLoop(3*time.Minute, c.EnsureScrapingCounters)
	go cronJobLoop(1*time.Hour, func() error {
		return tasks.CollectGarbage(dbi, time.Now().Add(-14*24*time.Hour)) //14 days
	})

	//use main goroutine to emit Prometheus metrics
	http.Handle("/metrics", promhttp.Handler())
	http.Handle("/healthcheck", http.HandlerFunc(healthCheckHandler))
	logg.Info("listening on " + httpListenAddr)
	err := httpee.ListenAndServeContext(httpee.ContextWithSIGINT(context.Background(), 10*time.Second), httpListenAddr, nil)
	if err != nil {
		logg.Error(err.Error())
	}
}

//Execute a task repeatedly, but slow down when sql.ErrNoRows is returned by it.
//(Tasks use this error value to indicate that nothing needs scraping, so we
//can back off a bit to avoid useless database load.)
func queuedJobLoop(task func() error) {
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

//Execute a task repeatedly, in set intervals. Unlike queuedJobLoop(), this
//does not change pace when errors are returned.
func cronJobLoop(interval time.Duration, task func() error) {
	for {
		err := task()
		if err != nil {
			logg.Error(err.Error())
		}
		time.Sleep(interval)
	}
}

////////////////////////////////////////////////////////////////////////////////
// task: worker

func runWorker(dbi *gorp.DbMap, team core.AssetManagerTeam, httpListenAddr string) {
	c := tasks.Context{DB: dbi, Team: team}
	c.ApplyDefaults()

	go queuedJobLoop(func() error {
		_, err := c.ExecuteNextResize()
		return err
	})
	go cronJobLoop(3*time.Minute, c.EnsureResizingCounters)

	//use main goroutine to emit Prometheus metrics
	http.Handle("/metrics", promhttp.Handler())
	http.Handle("/healthcheck", http.HandlerFunc(healthCheckHandler))
	logg.Info("listening on " + httpListenAddr)
	err := httpee.ListenAndServeContext(httpee.ContextWithSIGINT(context.Background(), 10*time.Second), httpListenAddr, nil)
	if err != nil {
		logg.Error(err.Error())
	}
}

////////////////////////////////////////////////////////////////////////////////
// task: test-asset-type

func runAssetTypeTestShell(dbi *gorp.DbMap, team core.AssetManagerTeam, assetType db.AssetType) {
	manager, _ := team.ForAssetType(assetType)
	if manager == nil {
		logg.Fatal("no manager configured for asset type: %q", assetType)
	}

	fmt.Println("")
	fmt.Println("supported commands:")
	fmt.Println("\tlist   <project-id>                                     - calls manager.ListAssets()")
	fmt.Println("\tshow   <project-id> <asset-id>                          - calls manager.GetAssetStatus() with previousStatus == nil")
	fmt.Println("\tshow   <project-id> <asset-id> <size> <metric=usage>... - calls manager.GetAssetStatus() with previousStatus != nil")
	fmt.Println("\tresize <project-id> <asset-id> <old-size> <new-size>    - calls manager.SetAssetSize()")
	fmt.Println("")

	stdin := bufio.NewReader(os.Stdin)
	eof := false
PROMPT:
	for !eof {
		//show prompt
		os.Stdout.Write([]byte("> "))
		input, err := stdin.ReadString('\n')
		eof = err == io.EOF
		if !eof && err != nil {
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
			switch {
			case len(fields) == 3:
				previousStatus = nil
			case len(fields) >= 5:
				size, err := strconv.ParseUint(fields[3], 10, 64)
				if err != nil {
					logg.Error(err.Error())
					continue
				}
				usageValues := make(db.UsageValues)
				for _, field := range fields[4:] {
					subfields := strings.SplitN(field, "=", 2)
					if len(subfields) != 2 {
						logg.Error(`field %q is not of the form "metric=value"`, field)
						continue PROMPT
					}
					usage, err := strconv.ParseFloat(subfields[1], 64)
					if err != nil {
						logg.Error(err.Error())
						continue PROMPT
					}
					usageValues[db.UsageMetric(subfields[0])] = usage
				}
				previousStatus = &core.AssetStatus{Size: size, Usage: usageValues}
			default:
				logg.Error("wrong number of arguments")
				continue
			}
			result, err := manager.GetAssetStatus(res, fields[2], previousStatus)
			if err != nil {
				logg.Error(err.Error())
				continue
			}
			logg.Info("size = %d, usage = %g", result.Size, result.Usage)

		case "resize":
			if len(fields) != 5 {
				logg.Error("wrong number of arguments")
				continue
			}
			oldSize, err := strconv.ParseUint(fields[3], 10, 64)
			if err != nil {
				logg.Error(err.Error())
				continue
			}
			newSize, err := strconv.ParseUint(fields[4], 10, 64)
			if err != nil {
				logg.Error(err.Error())
				continue
			}
			outcome, err := manager.SetAssetSize(res, fields[2], oldSize, newSize)
			logg.Info("outcome: %s", outcome)
			if err != nil {
				logg.Error(err.Error())
				continue
			}

		default:
			logg.Error("unknown command: %q", fields[0])
		}
	}

	os.Stdout.Write([]byte("\n"))
}
