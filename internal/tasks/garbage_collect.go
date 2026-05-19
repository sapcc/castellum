// SPDX-FileCopyrightText: 2019 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package tasks

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sapcc/go-bits/jobloop"
	"go.xyrillian.de/oblast"
)

// GarbageCollectionJob removes old entries from the finished_operations table.
func (c *Context) GarbageCollectionJob(registerer prometheus.Registerer) jobloop.Job {
	return (&jobloop.CronJob{
		Metadata: jobloop.JobMetadata{
			ReadableName: "garbage collection of finished operations",
			CounterOpts: prometheus.CounterOpts{
				Name: "castellum_finished_operation_garbage_collection_runs",
				Help: "Counter for garbage collection runs of the finished_operations table.",
			},
		},
		Interval: 1 * time.Hour,
		Task: func(ctx context.Context, _ prometheus.Labels) error {
			return CollectGarbage(c.DB, time.Now().Add(-14*24*time.Hour)) // 14 days
		},
	}).Setup(registerer)
}

// CollectGarbage removes old entries from the finished_operations table.
func CollectGarbage(dbi *oblast.DB, maxLastUpdatedAt time.Time) error {
	_, err := dbi.Exec(`DELETE FROM finished_operations WHERE finished_at < $1`, maxLastUpdatedAt)
	return err
}
