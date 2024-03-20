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

package tasks

import (
	"context"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sapcc/go-api-declarations/castellum"
	"github.com/sapcc/go-bits/assert"
	"github.com/sapcc/go-bits/easypg"
	"github.com/sapcc/go-bits/mock"
	"gopkg.in/yaml.v2"

	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/castellum/internal/plugins"
	"github.com/sapcc/castellum/internal/test"
)

func configFromYAML(t test.T, buf string) (cfg core.Config) {
	// must have spaces as indentation in YAML, but my editor gets confused when
	// the file has both space indentation and tab indentation -> string literal
	// has tabs, but we replace them with spaces before yaml.UnmarshalStrict()
	buf = strings.ReplaceAll(buf, "\t", "  ")
	t.Must(yaml.UnmarshalStrict([]byte(buf), &cfg))
	return
}

const resourceSeedingConfigGood = `
project_seeds:
	# This project exists, so this positive seed will be applied.
	- project_name: 'First Project'
		domain_name: 'First Domain'
		resources: &positive-seed
			foo:
				critical_threshold: { usage_percent: { singular: 95 } }
				size_steps: { percent: 20 }
	# This project exists, so this negative seed will be applied.
	- project_name: 'Second Project'
		domain_name: 'First Domain'
		disabled_resources: [ 'fo*' ]
	# This project does not exist, so this seed will be skipped.
	- project_name: 'Third Project'
		domain_name: 'Unknown Domain'
		resources: *positive-seed
`

func TestResourceSeedingSuccess(baseT *testing.T) {
	t := test.T{T: baseT}
	cfg := configFromYAML(t, resourceSeedingConfigGood)
	withContext(t, cfg, func(ctx context.Context, c *Context, _ *plugins.AssetManagerStatic, _ *mock.Clock, registry *prometheus.Registry) {
		job := c.ResourceSeedingJob(registry)

		// create a resource in a project that is not seeded - this will be ignored by the seeding job
		t.Must(c.DB.Insert(&db.Resource{
			ScopeUUID:           "project3",
			DomainUUID:          "domain1",
			AssetType:           "foo",
			LowThresholdPercent: castellum.UsageValues{castellum.SingularUsageMetric: 60},
			LowDelaySeconds:     3600,
			SingleStep:          true,
		}))

		// create a resource that has a negative seed - the seeding job will delete it
		t.Must(c.DB.Insert(&db.Resource{
			ScopeUUID:           "project2",
			DomainUUID:          "domain1",
			AssetType:           "foo",
			LowThresholdPercent: castellum.UsageValues{castellum.SingularUsageMetric: 60},
			LowDelaySeconds:     3600,
			SingleStep:          true,
		}))

		tr, tr0 := easypg.NewTracker(t.T, c.DB.Db)
		tr0.Ignore()

		// test that seeding job applies the seeds (except for the one project that the MockProviderClient reports as nonexistent)
		t.Must(job.ProcessOne(ctx))
		tr.DBChanges().AssertEqualf(`
			DELETE FROM resources WHERE id = 2 AND scope_uuid = 'project2' AND asset_type = 'foo';
			INSERT INTO resources (id, scope_uuid, asset_type, low_threshold_percent, low_delay_seconds, high_threshold_percent, high_delay_seconds, critical_threshold_percent, size_step_percent, domain_uuid, next_scrape_at) VALUES (3, 'project1', 'foo', '{"singular":0}', 0, '{"singular":0}', 0, '{"singular":95}', 20, 'domain1', 0);
		`)

		// test that the next seeding run does not change anything
		t.Must(job.ProcessOne(ctx))
		tr.DBChanges().AssertEmpty()

		// perturb one of the seeded resources
		t.MustExec(c.DB, `UPDATE resources SET high_threshold_percent = $1, high_delay_seconds = $2 WHERE scope_uuid = $3`,
			castellum.UsageValues{castellum.SingularUsageMetric: 80}, 7200, "project1")

		// test that the next seeding run resets these changes
		t.Must(job.ProcessOne(ctx))
		tr.DBChanges().AssertEmpty()
	})
}

// The resource definition in this seed is missing the high_threshold.delay_secs field.
const resourceSeedingConfigBadResource = `
project_seeds:
	- project_name: 'First Project'
		domain_name: 'First Domain'
		resources: &positive-seed
			foo:
				high_threshold: { usage_percent: { singular: 90 } }
				size_steps: { percent: 20 }
`

func TestResourceSeedingBadResource(baseT *testing.T) {
	t := test.T{T: baseT}
	cfg := configFromYAML(t, resourceSeedingConfigBadResource)
	withContext(t, cfg, func(ctx context.Context, c *Context, _ *plugins.AssetManagerStatic, _ *mock.Clock, registry *prometheus.Registry) {
		job := c.ResourceSeedingJob(registry)

		err := job.ProcessOne(ctx)
		if err == nil {
			t.Error("expected ResourceSeedingJob to fail, but succeeded!")
		} else {
			assert.DeepEqual(t.T, "error message from ResourceSeedingJob", err.Error(),
				`while applying seed for project "First Domain/First Project" (project1): cannot apply foo seed: delay for high threshold is missing`)
		}
	})
}
