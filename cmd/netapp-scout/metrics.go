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

import "github.com/prometheus/client_golang/prometheus"

var (
	collectedAtGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "castellum_netapp_scout_data_collected_at",
		Help: "Timestamp of the last successful data collection in castellum-netapp-scout.",
	})
	collectionDurationSecsGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "castellum_netapp_scout_data_collection_duration_secs",
		Help: "Duration in seconds of the last successful data collection in castellum-netapp-scout.",
	})
	successfulCollectionsCounter = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "castellum_netapp_scout_successful_collections",
		Help: "Counter for successful data collections in castellum-netapp-scout.",
	})
	failedCollectionsCounter = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "castellum_netapp_scout_failed_collections",
		Help: "Counter for failed data collections in castellum-netapp-scout.",
	})
)

func init() {
	prometheus.MustRegister(collectedAtGauge)
	prometheus.MustRegister(collectionDurationSecsGauge)
	prometheus.MustRegister(successfulCollectionsCounter)
	successfulCollectionsCounter.Add(0)
	prometheus.MustRegister(failedCollectionsCounter)
	failedCollectionsCounter.Add(0)
}
