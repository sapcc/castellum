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

package core

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sapcc/go-api-declarations/castellum"
	"github.com/sapcc/go-bits/logg"

	"github.com/sapcc/castellum/internal/db"
)

var opStateTransitionCounter = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "castellum_operation_state_transitions",
		Help: "Counter for state transitions of operations.",
	},
	[]string{"project_id", "asset", "from_state", "to_state"},
)

func init() {
	prometheus.MustRegister(opStateTransitionCounter)
}

// CountStateTransition must be called whenever an operation changes to a
// different state.
func CountStateTransition(res db.Resource, assetUUID string, from, to castellum.OperationState) {
	labels := prometheus.Labels{
		"project_id": res.ScopeUUID,
		"asset":      string(res.AssetType),
		"from_state": string(from),
		"to_state":   string(to),
	}
	opStateTransitionCounter.With(labels).Inc()
	logg.Info("moving operation on %s %s from state %s to state %s", res.AssetType, assetUUID, from, to)
}
