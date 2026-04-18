// SPDX-FileCopyrightText: 2019 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package tasks

import (
	"database/sql"
	"time"

	"github.com/sapcc/go-bits/jobloop"

	"github.com/sapcc/castellum/internal/core"
)

// Context holds things used by the various task implementations in this
// package.
type Context struct {
	Config         core.Config
	DB             *sql.DB
	Team           core.AssetManagerTeam
	ProviderClient core.ProviderClient

	// dependency injection slots (usually filled by ApplyDefaults(), but filled
	// with doubles in tests)
	TimeNow   func() time.Time
	AddJitter func(time.Duration) time.Duration
}

// ApplyDefaults injects the regular runtime dependencies into this Context.
func (c *Context) ApplyDefaults() {
	c.TimeNow = time.Now
	c.AddJitter = jobloop.DefaultJitter
}

const (
	// AssetScrapeInterval is the interval for scrapes of an individual asset.
	AssetScrapeInterval time.Duration = 5 * time.Minute
	// ResourceScrapeInterval is the interval for scrapes of an individual resource.
	ResourceScrapeInterval time.Duration = 30 * time.Minute
)
