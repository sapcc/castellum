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
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/sapcc/go-bits/logg"
	"github.com/sapcc/go-bits/promquery"
)

// Engine contains the state of the application. It implements both the data retrieval as well as the HTTP API.
type Engine struct {
	PromClient promquery.Client
	Data       map[ShareIdentity]ShareData
	DataMutex  sync.RWMutex
}

type ShareIdentity struct {
	ProjectID string
	ShareID   string
}

func (e *Engine) GetShareData(key ShareIdentity) ShareData {
	e.DataMutex.RLock()
	defer e.DataMutex.RUnlock()
	return e.Data[key]
}

func (e *Engine) CollectLoop() {
	for {
		err := e.collect()
		if err != nil {
			failedCollectionsCounter.Inc()
			logg.Error(err.Error())
		}
		time.Sleep(5 * time.Second)
	}
}

func (e *Engine) collect() error {
	//Our general strategy is to build an entirely new instance of `e.Data`. Once
	//we have collected all data, we swap out the data instance in the engine
	//(similar to double-buffering).

	startedAt := time.Now()
	result := make(map[ShareIdentity]ShareData)
	for _, metricName := range allMetricNames {
		//NOTE: The `max by (share_id)` is necessary for when a share is being
		//migrated to another shareserver and thus appears in the metrics twice.
		//The `volume_type!="dp"` is required to filter out metrics for snapmirrors.
		//The `volume_state!="offline"` is required to filter out metrics for decom leftovers
		//(where an online share is migrated away from an offline filer).
		query := fmt.Sprintf(`max by (project_id, share_id) (%s{volume_type!="dp",volume_state!="offline"})`, metricName)
		vector, err := e.PromClient.GetVector(query)
		if err != nil {
			return fmt.Errorf("cannot collect %s data: %w", metricName, err)
		}

		for _, sample := range vector {
			key := ShareIdentity{
				ProjectID: string(sample.Metric["project_id"]),
				ShareID:   string(sample.Metric["share_id"]),
			}
			data, ok := result[key]
			if !ok {
				data = make(ShareData)
				result[key] = data
			}
			data[metricName] = float64(sample.Value)
		}
	}

	//there should never be no data at all (if there are no shares, what do we need an autoscaler for?)
	if len(result) == 0 {
		return errors.New("collected no NetApp metrics at all (is the netapp-api-exporter scrape working correctly?)")
	}
	//check that we have complete data for all shares
	for shareIdentity, shareData := range result {
		for _, metricName := range allMetricNames {
			if _, exists := shareData[metricName]; !exists {
				return fmt.Errorf("collected incomplete data: share %s in project %s does not have %s (this can happen momentarily if shares just appeared or disappeared while we collected; if this error persists, it's a problem)",
					shareIdentity.ShareID, shareIdentity.ProjectID, metricName)
			}
		}
	}

	//track collection success and performance
	finishedAt := time.Now()
	successfulCollectionsCounter.Inc()
	collectedAtGauge.Set(float64(startedAt.Unix()))
	collectionDurationSecsGauge.Set(finishedAt.Sub(startedAt).Seconds())
	logg.Debug("collected data on %d shares", len(result))

	//make new data visible to the API
	e.DataMutex.Lock()
	defer e.DataMutex.Unlock()
	e.Data = result
	return nil
}
