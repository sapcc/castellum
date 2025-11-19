// SPDX-FileCopyrightText: 2019 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package tasks_test

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sapcc/go-bits/easypg"

	"github.com/sapcc/castellum/internal/plugins"
	"github.com/sapcc/castellum/internal/tasks"
	"github.com/sapcc/castellum/internal/test"
)

func TestMain(m *testing.M) {
	easypg.WithTestDB(m, func() int { return m.Run() })
}

func commonSetupOptionsForWorkerTest() test.SetupOption {
	return test.WithAssetManagers(
		&plugins.AssetManagerStatic{AssetType: "foo"},
	)
}

func withContext(s test.Setup, action func(context.Context, *tasks.Context, *prometheus.Registry)) {
	registry := prometheus.NewPedanticRegistry()

	action(context.Background(), &tasks.Context{
		Config:         s.Config,
		DB:             s.DB,
		Team:           s.Team,
		ProviderClient: s.ProviderClient,
		TimeNow:        s.Clock.Now,
		AddJitter:      noJitter,
	}, registry)
}

func noJitter(d time.Duration) time.Duration {
	// Tests should be deterministic, so we do not add random jitter here.
	return d
}

// Take pointer to time.Time expression.
func p2time(t time.Time) *time.Time {
	return &t
}

// Take pointer to uint64 expression.
func p2uint64(x uint64) *uint64 {
	return &x
}
