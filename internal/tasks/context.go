// SPDX-FileCopyrightText: 2019 SAP SE
// SPDX-License-Identifier: Apache-2.0

package tasks

import (
	"math/rand"
	"time"

	"github.com/go-gorp/gorp/v3"

	"github.com/sapcc/castellum/internal/core"
)

// Context holds things used by the various task implementations in this
// package.
type Context struct {
	Config         core.Config
	DB             *gorp.DbMap
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
	c.AddJitter = addJitter
}

// addJitter returns a random duration within +/- 10% of the requested value.
// This can be used to even out the load on a scheduled job over time, by
// spreading jobs that would normally be scheduled right next to each other out
// over time without corrupting the individual schedules too much.
func addJitter(duration time.Duration) time.Duration {
	//nolint:gosec // This is not crypto-relevant, so math/rand is okay.
	r := rand.Float64() //NOTE: 0 <= r < 1
	return time.Duration(float64(duration) * (0.9 + 0.2*r))
}

// JobPoller is a function, usually a member function of type Context, that can
// be called repeatedly to obtain Job instances.
//
// If there are no jobs to work on right now, sql.ErrNoRows shall be returned
// to signal to the caller to slow down the polling.
type JobPoller func() (Job, error)

// Job is a job that can be transferred to a worker goroutine to be executed
// there.
type Job interface {
	Execute() error
}

// ExecuteOne is used by unit tests to find and execute exactly one instance of
// the given type of Job. sql.ErrNoRows is returned when there are no jobs of
// that type waiting.
func ExecuteOne(p JobPoller) error {
	j, err := p()
	if err != nil {
		return err
	}
	return j.Execute()
}

const (
	// AssetScrapeInterval is the interval for scrapes of an individual asset.
	AssetScrapeInterval time.Duration = 5 * time.Minute
	// ResourceScrapeInterval is the interval for scrapes of an individual resource.
	ResourceScrapeInterval time.Duration = 30 * time.Minute
)
