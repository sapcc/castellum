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
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/dlmiddlecote/sqlstats"
	"github.com/go-gorp/gorp/v3"
	"github.com/gophercloud/gophercloud/v2/openstack"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/cors"
	"github.com/sapcc/go-api-declarations/bininfo"
	"github.com/sapcc/go-api-declarations/castellum"
	"github.com/sapcc/go-bits/audittools"
	"github.com/sapcc/go-bits/easypg"
	"github.com/sapcc/go-bits/gopherpolicy"
	"github.com/sapcc/go-bits/httpapi"
	"github.com/sapcc/go-bits/httpapi/pprofapi"
	"github.com/sapcc/go-bits/httpext"
	"github.com/sapcc/go-bits/jobloop"
	"github.com/sapcc/go-bits/logg"
	"github.com/sapcc/go-bits/must"
	"github.com/sapcc/go-bits/osext"
	"go.uber.org/automaxprocs/maxprocs"

	"github.com/sapcc/castellum/internal/api"
	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/castellum/internal/tasks"

	// load asset managers
	_ "github.com/sapcc/castellum/internal/plugins"
)

func usage() {
	fmt.Fprintf(os.Stderr,
		"usage:\n\t%s [api|observer|worker] <config-file>\n\t%s test-asset-type <config-file> <type> [<resource-config-json>]\n",
		os.Args[0], os.Args[0],
	)
	os.Exit(1)
}

func main() {
	bininfo.HandleVersionArgument()
	if len(os.Args) < 3 {
		usage()
	}
	taskName, configPath := os.Args[1], os.Args[2]
	bininfo.SetTaskName(taskName)

	logg.ShowDebug = osext.GetenvBool("CASTELLUM_DEBUG")
	undoMaxprocs := must.Return(maxprocs.Set(maxprocs.Logger(logg.Debug)))
	defer undoMaxprocs()

	wrap := httpext.WrapTransport(&http.DefaultTransport)
	wrap.SetInsecureSkipVerify(osext.GetenvBool("CASTELLUM_INSECURE")) // for debugging with mitmproxy etc. (DO NOT SET IN PRODUCTION)
	wrap.SetOverrideUserAgent(bininfo.Component(), bininfo.VersionOr("rolling"))

	// initialize DB connection
	dbURL := must.Return(easypg.URLFrom(easypg.URLParts{
		HostName:          osext.GetenvOrDefault("CASTELLUM_DB_HOSTNAME", "localhost"),
		Port:              osext.GetenvOrDefault("CASTELLUM_DB_PORT", "5432"),
		UserName:          osext.GetenvOrDefault("CASTELLUM_DB_USERNAME", "postgres"),
		Password:          os.Getenv("CASTELLUM_DB_PASSWORD"),
		ConnectionOptions: os.Getenv("CASTELLUM_DB_CONNECTION_OPTIONS"),
		DatabaseName:      osext.GetenvOrDefault("CASTELLUM_DB_NAME", "castellum"),
	}))
	dbi := must.Return(db.Init(dbURL))
	prometheus.MustRegister(sqlstats.NewStatsCollector("castellum", dbi.Db))

	// initialize OpenStack connection
	ctx := httpext.ContextWithSIGINT(context.Background(), 10*time.Second)
	providerClient := must.Return(core.NewProviderClient(ctx))

	// get max asset sizes
	cfg := must.Return(core.LoadConfig(configPath))

	// initialize asset managers
	team := must.Return(core.CreateAssetManagers(
		strings.Split(osext.MustGetenv("CASTELLUM_ASSET_MANAGERS"), ","),
		providerClient,
	))

	httpListenAddr := osext.GetenvOrDefault("CASTELLUM_HTTP_LISTEN_ADDRESS", ":8080")
	switch taskName {
	case "api":
		if len(os.Args) != 3 {
			usage()
		}
		runAPI(ctx, cfg, dbi, team, providerClient, httpListenAddr)
	case "observer":
		if len(os.Args) != 3 {
			usage()
		}
		runObserver(ctx, cfg, dbi, team, providerClient, httpListenAddr)
	case "worker":
		if len(os.Args) != 3 {
			usage()
		}
		runWorker(ctx, dbi, team, httpListenAddr)
	case "test-asset-type":
		if len(os.Args) != 4 && len(os.Args) != 5 {
			usage()
		}
		configJSON := ""
		if len(os.Args) == 5 {
			configJSON = os.Args[4]
		}
		runAssetTypeTestShell(ctx, team, db.AssetType(os.Args[3]), configJSON)
	default:
		usage()
	}
}

////////////////////////////////////////////////////////////////////////////////
// task: API

func runAPI(ctx context.Context, cfg core.Config, dbi *gorp.DbMap, team core.AssetManagerTeam, providerClient core.ProviderClient, httpListenAddr string) {
	identityV3, err := providerClient.CloudAdminClient(openstack.NewIdentityV3)
	if err != nil {
		logg.Fatal("cannot find Keystone V3 API: " + err.Error())
	}
	tv := gopherpolicy.TokenValidator{
		IdentityV3: identityV3,
		Cacher:     gopherpolicy.InMemoryCacher(),
	}
	must.Succeed(tv.LoadPolicyFile(osext.MustGetenv("CASTELLUM_OSLO_POLICY_PATH"), nil))

	// connect to Hermes RabbitMQ if requested
	auditor := audittools.NewNullAuditor()
	if os.Getenv("CASTELLUM_RABBITMQ_QUEUE_NAME") != "" {
		auditor = must.Return(audittools.NewAuditor(ctx, audittools.AuditorOpts{
			EnvPrefix: "CASTELLUM_RABBITMQ",
			Observer: audittools.Observer{
				TypeURI: "service/autoscaling",
				Name:    bininfo.Component(),
				ID:      audittools.GenerateUUID(),
			},
		}))
	}

	// wrap the main API handler in several layers of middleware
	corsMiddleware := cors.New(cors.Options{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"HEAD", "GET", "POST", "PUT", "DELETE"},
		AllowedHeaders: []string{"Content-Type", "User-Agent", "X-Auth-Token"},
	})
	handler := httpapi.Compose(
		api.NewHandler(cfg, dbi, team, &tv, providerClient, auditor),
		httpapi.HealthCheckAPI{SkipRequestLog: true},
		httpapi.WithGlobalMiddleware(corsMiddleware.Handler),
		pprofapi.API{IsAuthorized: pprofapi.IsRequestFromLocalhost},
	)
	mux := http.NewServeMux()
	mux.Handle("/", handler)
	mux.Handle("/metrics", promhttp.Handler())

	must.Succeed(httpext.ListenAndServeContext(ctx, httpListenAddr, mux))
}

////////////////////////////////////////////////////////////////////////////////
// task: observer

func runObserver(ctx context.Context, cfg core.Config, dbi *gorp.DbMap, team core.AssetManagerTeam, providerClient core.ProviderClient, httpListenAddr string) {
	c := tasks.Context{Config: cfg, DB: dbi, Team: team, ProviderClient: providerClient}
	c.ApplyDefaults()
	prometheus.MustRegister(tasks.StateMetricsCollector{Context: c})

	// The observer process has a budget of 16 DB connections. Since there are
	// much more assets than resources, we give most of these (12 of 16) to asset
	// scraping. The rest is split between resource scrape and garbage collection.
	go c.AssetScrapingJob(nil).Run(ctx, jobloop.NumGoroutines(12))
	go c.ResourceScrapingJob(nil).Run(ctx, jobloop.NumGoroutines(3))
	go c.ResourceSeedingJob(nil).Run(ctx)
	go c.GarbageCollectionJob(nil).Run(ctx)

	// use main goroutine to emit Prometheus metrics
	handler := httpapi.Compose(
		httpapi.HealthCheckAPI{SkipRequestLog: true},
		pprofapi.API{IsAuthorized: pprofapi.IsRequestFromLocalhost},
	)
	mux := http.NewServeMux()
	mux.Handle("/", handler)
	mux.Handle("/metrics", promhttp.Handler())
	must.Succeed(httpext.ListenAndServeContext(ctx, httpListenAddr, mux))
}

////////////////////////////////////////////////////////////////////////////////
// task: worker

func runWorker(ctx context.Context, dbi *gorp.DbMap, team core.AssetManagerTeam, httpListenAddr string) {
	c := tasks.Context{DB: dbi, Team: team}
	c.ApplyDefaults()

	// The worker process has a budget of 16 DB connections. We need one of that
	// for polling, the rest can go towards resizing workers. Therefore, 12 resize
	// workers is a safe number that even leaves some headroom for future tasks.
	go c.AssetResizingJob(nil).Run(ctx, jobloop.NumGoroutines(12))

	// use main goroutine to emit Prometheus metrics
	handler := httpapi.Compose(
		httpapi.HealthCheckAPI{SkipRequestLog: true},
		pprofapi.API{IsAuthorized: pprofapi.IsRequestFromLocalhost},
	)
	mux := http.NewServeMux()
	mux.Handle("/", handler)
	mux.Handle("/metrics", promhttp.Handler())
	must.Succeed(httpext.ListenAndServeContext(ctx, httpListenAddr, mux))
}

////////////////////////////////////////////////////////////////////////////////
// task: test-asset-type

func runAssetTypeTestShell(ctx context.Context, team core.AssetManagerTeam, assetType db.AssetType, configJSON string) {
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
		// show prompt
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
			res.ConfigJSON = configJSON
			err := manager.CheckResourceAllowed(ctx, res.AssetType, res.ScopeUUID, res.ConfigJSON, nil)
			if err != nil {
				logg.Error("CheckResourceAllowed failed: " + err.Error())
				continue
			}
		}

		switch fields[0] {
		case "list":
			if len(fields) != 2 {
				logg.Error("wrong number of arguments")
				continue
			}
			result, err := manager.ListAssets(ctx, res)
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
				usageValues := make(castellum.UsageValues)
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
					usageValues[castellum.UsageMetric(subfields[0])] = usage
				}
				previousStatus = &core.AssetStatus{Size: size, Usage: usageValues}
			default:
				logg.Error("wrong number of arguments")
				continue
			}
			result, err := manager.GetAssetStatus(ctx, res, fields[2], previousStatus)
			if err != nil {
				logg.Error(err.Error())
				continue
			}
			logg.Info("size = %d", result.Size)
			for metric, value := range result.Usage {
				logg.Info("usage[%s] = %g", metric, value)
			}
			if result.StrictMinimumSize != nil {
				logg.Info("minsize = %d", *result.StrictMinimumSize)
			}

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
			outcome, err := manager.SetAssetSize(ctx, res, fields[2], oldSize, newSize)
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
