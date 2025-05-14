// SPDX-FileCopyrightText: 2019 SAP SE
// SPDX-License-Identifier: Apache-2.0

package tasks

import (
	"context"
	"testing"
	"time"

	"github.com/go-gorp/gorp/v3"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sapcc/go-bits/easypg"
	"github.com/sapcc/go-bits/mock"

	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/plugins"
	"github.com/sapcc/castellum/internal/test"
)

func TestMain(m *testing.M) {
	easypg.WithTestDB(m, func() int { return m.Run() })
}

func withContext(t test.T, cfg core.Config, action func(context.Context, *Context, *plugins.AssetManagerStatic, *mock.Clock, *prometheus.Registry)) {
	t.WithDB(nil, func(dbi *gorp.DbMap) {
		amStatic := &plugins.AssetManagerStatic{AssetType: "foo"}
		// clock starts at an easily recognizable value
		clock := mock.NewClock()
		clock.StepBy(99990 * time.Second)
		registry := prometheus.NewPedanticRegistry()

		mpc := test.MockProviderClient{
			Domains: map[string]core.CachedDomain{
				"domain1": {Name: "First Domain"},
			},
			Projects: map[string]core.CachedProject{
				"project1": {Name: "First Project", DomainID: "domain1"},
				"project2": {Name: "Second Project", DomainID: "domain1"},
				"project3": {Name: "Third Project", DomainID: "domain1"},
			},
		}

		action(context.Background(), &Context{
			Config:         cfg,
			DB:             dbi,
			Team:           core.AssetManagerTeam{amStatic},
			ProviderClient: mpc,
			TimeNow:        clock.Now,
			AddJitter:      noJitter,
		}, amStatic, clock, registry)
	})
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
