// SPDX-FileCopyrightText: 2019 SAP SE
// SPDX-License-Identifier: Apache-2.0

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
