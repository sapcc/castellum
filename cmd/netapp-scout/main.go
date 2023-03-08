/*******************************************************************************
*
* Copyright 2023 SAP SE
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
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sapcc/go-api-declarations/bininfo"
	"github.com/sapcc/go-bits/httpapi"
	"github.com/sapcc/go-bits/httpext"
	"github.com/sapcc/go-bits/logg"
	"github.com/sapcc/go-bits/must"
	"github.com/sapcc/go-bits/osext"
	"github.com/sapcc/go-bits/promquery"
	"gopkg.in/yaml.v2"
)

func main() {
	logg.ShowDebug = osext.GetenvBool("CASTELLUM_DEBUG")
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage:", os.Args[0], "<config-file>")
	}

	wrap := httpext.WrapTransport(&http.DefaultTransport)
	wrap.SetInsecureSkipVerify(osext.GetenvBool("CASTELLUM_INSECURE")) //for debugging with mitmproxy etc. (DO NOT SET IN PRODUCTION)
	wrap.SetOverrideUserAgent(bininfo.Component(), bininfo.VersionOr("rolling"))

	promClient, httpListenAddr := readConfiguration(os.Args[1])

	//in a separate goroutine, query Prometheus for metric data in regular intervals
	engine := &Engine{PromClient: promClient}
	go engine.CollectLoop()

	//in the main goroutine, run the HTTP API
	handler := httpapi.Compose(
		engine,
		httpapi.HealthCheckAPI{Check: engine.CheckDataAvailability, SkipRequestLog: true},
	)
	http.Handle("/", handler)
	http.Handle("/metrics", promhttp.Handler())

	ctx := httpext.ContextWithSIGINT(context.Background(), 10*time.Second)
	must.Succeed(httpext.ListenAndServeContext(ctx, httpListenAddr, nil))
}

func readConfiguration(filePath string) (promClient promquery.Client, httpListenAddr string) {
	//NOTE: This configuration file is documented in `docs/asset-managers/nfs-shares.md`.
	buf := must.Return(os.ReadFile(filePath))
	var cfg struct {
		HTTP struct {
			ListenAddress string `yaml:"listen_address"`
		} `yaml:"http"`
		Prometheus promquery.Config `yaml:"prometheus"`
	}
	must.Succeed(yaml.UnmarshalStrict(buf, &cfg))
	if cfg.HTTP.ListenAddress == "" {
		cfg.HTTP.ListenAddress = ":8080"
	}

	return must.Return(cfg.Prometheus.Connect()), cfg.HTTP.ListenAddress
}
