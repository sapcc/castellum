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

package tasks

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	resourceScrapeSuccessCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "castellum_successful_resource_scrapes",
			Help: "Counter for successful resource scrape operations.",
		},
		[]string{"asset"},
	)
	resourceScrapeFailedCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "castellum_failed_resource_scrapes",
			Help: "Counter for failed resource scrape operations.",
		},
		[]string{"asset"},
	)
	assetScrapeSuccessCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "castellum_successful_asset_scrapes",
			Help: "Counter for successful asset scrape operations.",
		},
		[]string{"asset"},
	)
	assetScrapeFailedCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "castellum_failed_asset_scrapes",
			Help: "Counter for failed asset scrape operations.",
		},
		[]string{"asset"},
	)
	assetResizeCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "castellum_asset_resizes",
			Help: `Counter for asset resize operations that ran to completion, yielding a FinishedOperation in either "succeeded" or "failed" state.`,
		},
		[]string{"asset"},
	)
	assetResizeErroredCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "castellum_errored_asset_resizes",
			Help: "Counter for asset resize operations that encountered an unexpected error and could not produce a FinishedOperation.",
		},
		[]string{"asset"},
	)
)

func init() {
	prometheus.MustRegister(resourceScrapeSuccessCounter)
	prometheus.MustRegister(resourceScrapeFailedCounter)
	prometheus.MustRegister(assetScrapeSuccessCounter)
	prometheus.MustRegister(assetScrapeFailedCounter)
}

//InitializeScrapingCounters adds 0 to all scraping counters, to ensure that
//all relevant timeseries exist.
func (c Context) InitializeScrapingCounters() {
	for _, manager := range c.Team {
		for _, assetType := range manager.AssetTypes() {
			labels := prometheus.Labels{"asset": string(assetType)}
			resourceScrapeSuccessCounter.With(labels).Add(0)
			resourceScrapeFailedCounter.With(labels).Add(0)
			assetScrapeSuccessCounter.With(labels).Add(0)
			assetScrapeFailedCounter.With(labels).Add(0)
		}
	}
}

//InitializeResizingCounters adds 0 to all resizing counters, to ensure that
//all relevant timeseries exist.
func (c Context) InitializeResizingCounters() {
	for _, manager := range c.Team {
		for _, assetType := range manager.AssetTypes() {
			labels := prometheus.Labels{"asset": string(assetType)}
			assetResizeCounter.With(labels).Add(0)
			assetResizeErroredCounter.With(labels).Add(0)
		}
	}
}
