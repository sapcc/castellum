// SPDX-FileCopyrightText: 2019 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package tasks

import (
	"database/sql"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sapcc/go-bits/logg"
	"github.com/sapcc/go-bits/sqlext"

	"github.com/sapcc/castellum/internal/db"
)

var projectResourceExistsGauge = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "castellum_has_project_resource",
		Help: "Constant value of 1 for each existing project resource.",
	},
	[]string{"project_id", "asset"},
)

////////////////////////////////////////////////////////////////////////////////
// Some metrics are generated with a prometheus.Collector implementation, so
// that we don't have to track when resources are deleted and need to be
// removed from the respective GaugeVecs.

// StateMetricsCollector is a prometheus.Collector that submits gauges
// describing database entries.
type StateMetricsCollector struct {
	Context Context
}

// Describe implements the prometheus.Collector interface.
func (c StateMetricsCollector) Describe(ch chan<- *prometheus.Desc) {
	projectResourceExistsGauge.Describe(ch)
}

var resourceStateQuery = `SELECT scope_uuid, asset_type FROM resources`

// Collect implements the prometheus.Collector interface.
func (c StateMetricsCollector) Collect(ch chan<- prometheus.Metric) {
	err := c.doCollect(ch)
	if err != nil {
		logg.Error("collect state metrics failed: " + err.Error())
	}
}

func (c StateMetricsCollector) doCollect(ch chan<- prometheus.Metric) error {
	//NOTE: I use NewConstMetric() instead of storing the values in the GaugeVec
	// instances,
	//
	// 1. because it is faster.
	// 2. because this automatically handles deleted resources correctly.
	//   (Their metrics just disappear when Prometheus scrapes next time.)

	// fetch Descs for all metrics
	descCh := make(chan *prometheus.Desc, 1)
	projectResourceExistsGauge.Describe(descCh)
	projectResourceExistsDesc := <-descCh

	// fetch values
	err := sqlext.ForeachRow(c.Context.DB, resourceStateQuery, nil, func(rows *sql.Rows) error {
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
		return nil
	})
	return err
}
