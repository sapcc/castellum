/*******************************************************************************
*
* Copyright 2022 SAP SE
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

package core

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"time"

	prom_api "github.com/prometheus/client_golang/api"
	prom_v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	"github.com/sapcc/go-bits/logg"
	"github.com/sapcc/go-bits/osext"
)

//TODO: upstream this into go-bits (and rename PrometheusEmptyResultError into ErrNoRows at that point)

// PrometheusClient provides API access to a Prometheus server.
type PrometheusClient struct {
	api prom_v1.API
}

// PrometheusClientFromEnv sets up a Prometheus client from the environment variables:
//
//	${envPrefix}_URL    - required
//	${envPrefix}_CACERT - optional
//	${envPrefix}_CERT   - optional
//	${envPrefix}_KEY    - optional
func PrometheusClientFromEnv(envPrefix string) (PrometheusClient, error) {
	serverURL := osext.MustGetenv(envPrefix + "_URL")
	tlsConfig := &tls.Config{} //nolint:gosec // used for a client which defaults to TLS version 1.2

	certPath := os.Getenv(envPrefix + "_CERT")
	if certPath != "" {
		//if a client certificate is given, we also need the private key
		keyPath := osext.MustGetenv(envPrefix + "_KEY")
		clientCert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return PrometheusClient{}, err
		}
		tlsConfig.Certificates = []tls.Certificate{clientCert}
	}

	cacertPath := os.Getenv(envPrefix + "_CACERT")
	if cacertPath != "" {
		serverCACert, err := os.ReadFile(cacertPath)
		if err != nil {
			return PrometheusClient{}, fmt.Errorf("cannot load CA certificate from %s: %w", cacertPath, err)
		}
		certPool := x509.NewCertPool()
		certPool.AppendCertsFromPEM(serverCACert)
		tlsConfig.RootCAs = certPool
	}

	roundTripper := prom_api.DefaultRoundTripper
	if transport, ok := roundTripper.(*http.Transport); ok {
		transport.TLSClientConfig = tlsConfig
	} else {
		return PrometheusClient{}, fmt.Errorf("expected roundTripper of type \"*http.Transport\", got %T", roundTripper)
	}

	client, err := prom_api.NewClient(prom_api.Config{Address: serverURL, RoundTripper: roundTripper})
	if err != nil {
		return PrometheusClient{}, fmt.Errorf("cannot connect to Prometheus at %s: %w", serverURL, err)
	}
	return PrometheusClient{prom_v1.NewAPI(client)}, nil
}

// GetVector executes a Prometheus query and returns a vector of results.
func (c PrometheusClient) GetVector(queryStr string) (model.Vector, error) {
	value, warnings, err := c.api.Query(context.Background(), queryStr, time.Now())
	for _, warning := range warnings {
		logg.Info("Prometheus query produced warning: %s", warning)
	}
	if err != nil {
		return nil, fmt.Errorf("could not execute Prometheus query: %s: %s", queryStr, err.Error())
	}
	resultVector, ok := value.(model.Vector)
	if !ok {
		return nil, fmt.Errorf("could not execute Prometheus query: %s: unexpected type %T", queryStr, value)
	}
	return resultVector, nil
}

// GetSingleValue executes a Prometheus query and returns the first result. If
// the query produces multiple values, only the first value will be returned.
// If the query produces no values, the returned error will be of type
// PrometheusEmptyResultError.
func (c PrometheusClient) GetSingleValue(queryStr string) (float64, error) {
	resultVector, err := c.GetVector(queryStr)
	if err != nil {
		return 0, err
	}

	switch resultVector.Len() {
	case 0:
		return 0, PrometheusEmptyResultError{Query: queryStr}
	case 1:
		return float64(resultVector[0].Value), nil
	default:
		//suppress the log message when all values are the same (this can happen
		//when an adventurous Prometheus configuration causes the NetApp exporter
		//to be scraped twice)
		firstValue := resultVector[0].Value
		allTheSame := true
		for _, entry := range resultVector {
			if firstValue != entry.Value {
				allTheSame = false
				break
			}
		}
		if !allTheSame {
			logg.Info("Prometheus query returned more than one result: %s (only the first value will be used)", queryStr)
		}
		return float64(resultVector[0].Value), nil
	}
}

// PrometheusEmptyResultError is returned by PrometheusClient.GetSingleValue()
// if there were no result values at all.
type PrometheusEmptyResultError struct {
	Query string
}

// Error implements the builtin/error interface.
func (e PrometheusEmptyResultError) Error() string {
	return fmt.Sprintf("Prometheus query returned empty result: %s", e.Query)
}
