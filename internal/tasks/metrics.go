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
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/go-bits/logg"
)

var (
	projectResourceExistsGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "castellum_has_project_resource",
			Help: "Constant value of 1 for each existing project resource.",
		},
		[]string{"project_id", "asset"},
	)
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
	prometheus.MustRegister(assetResizeCounter)
	prometheus.MustRegister(assetResizeErroredCounter)
}

//EnsureScrapingCounters adds 0 to all scraping counters, to ensure that
//all relevant timeseries exist.
func (c Context) EnsureScrapingCounters() error {
	err := c.foreachAssetType(func(assetType db.AssetType) {
		labels := prometheus.Labels{"asset": string(assetType)}
		resourceScrapeSuccessCounter.With(labels).Add(0)
		resourceScrapeFailedCounter.With(labels).Add(0)
		assetScrapeSuccessCounter.With(labels).Add(0)
		assetScrapeFailedCounter.With(labels).Add(0)
	})
	if err != nil {
		return fmt.Errorf("during EnsureScrapingCounters: %w", err)
	}
	return nil
}

//EnsureResizingCounters adds 0 to all resizing counters, to ensure that
//all relevant timeseries exist.
func (c Context) EnsureResizingCounters() error {
	err := c.foreachAssetType(func(assetType db.AssetType) {
		labels := prometheus.Labels{"asset": string(assetType)}
		assetResizeCounter.With(labels).Add(0)
		assetResizeErroredCounter.With(labels).Add(0)
	})
	if err != nil {
		return fmt.Errorf("during EnsureResizingCounters: %w", err)
	}
	return nil
}

func (c Context) foreachAssetType(action func(db.AssetType)) (err error) {
	rows, err := c.DB.Query(`SELECT DISTINCT asset_type FROM resources`)
	if err != nil {
		return err
	}

	defer func() {
		if err != nil {
			err = rows.Close()
		} else {
			rows.Close()
		}
	}()

	for rows.Next() {
		var assetType db.AssetType
		err := rows.Scan(&assetType)
		if err != nil {
			return err
		}
		action(assetType)
	}
	err = rows.Err()
	if err != nil {
		rows.Close()
		return err
	}
	return rows.Close()
}

////////////////////////////////////////////////////////////////////////////////
// Some metrics are generated with a prometheus.Collector implementation, so
// that we don't have to track when resources are deleted and need to be
// removed from the respective GaugeVecs.

//StateMetricsCollector is a prometheus.Collector that submits gauges
//describing database entries.
type StateMetricsCollector struct {
	Context Context
}

//Describe implements the prometheus.Collector interface.
func (c StateMetricsCollector) Describe(ch chan<- *prometheus.Desc) {
	projectResourceExistsGauge.Describe(ch)
}

var resourceStateQuery = `SELECT scope_uuid, asset_type FROM resources`

//Collect implements the prometheus.Collector interface.
func (c StateMetricsCollector) Collect(ch chan<- prometheus.Metric) {
	err := c.doCollect(ch)
	if err != nil {
		logg.Error("collect state metrics failed: " + err.Error())
	}
}

func (c StateMetricsCollector) doCollect(ch chan<- prometheus.Metric) error {
	//NOTE: I use NewConstMetric() instead of storing the values in the GaugeVec
	//instances,
	//
	//1. because it is faster.
	//2. because this automatically handles deleted resources correctly.
	//   (Their metrics just disappear when Prometheus scrapes next time.)

	//fetch Descs for all metrics
	descCh := make(chan *prometheus.Desc, 1)
	projectResourceExistsGauge.Describe(descCh)
	projectResourceExistsDesc := <-descCh

	//fetch values
	rows, err := c.Context.DB.Query(resourceStateQuery)
	if err != nil {
		return err
	}
	for rows.Next() {
		var (
			scopeUUID string
			assetType db.AssetType
		)
		err := rows.Scan(&scopeUUID, &assetType)
		if err != nil {
			return err
		}
		ch <- prometheus.MustNewConstMetric(
			projectResourceExistsDesc,
			prometheus.GaugeValue, 1,
			scopeUUID, string(assetType),
		)
	}
	err = rows.Err()
	if err == nil {
		err = rows.Close()
	}
	if err != nil {
		return err
	}

	return nil
}
