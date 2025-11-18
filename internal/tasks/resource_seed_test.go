// SPDX-FileCopyrightText: 2023 SAP SE
// SPDX-License-Identifier: Apache-2.0

package tasks

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"

	"github.com/majewsky/gg/jsonmatch"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sapcc/go-api-declarations/castellum"
	"github.com/sapcc/go-bits/assert"
	"github.com/sapcc/go-bits/easypg"
	"github.com/sapcc/go-bits/mock"

	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
	"github.com/sapcc/castellum/internal/plugins"
	"github.com/sapcc/castellum/internal/test"
)

func configFromJSON(t test.T, buf string) (cfg core.Config) {
	t.Must(json.Unmarshal([]byte(removeCommentsFromJSON(buf)), &cfg))
	return
}

const resourceSeedingConfigGood = `{
	"project_seeds": [
		// This project exists, so this positive seed will be applied.
		{
			"project_name": "First Project",
			"domain_name": "First Domain",
			"resources": {
				"foo": {
					"critical_threshold": {
						"usage_percent": {
							"singular": 95
						}
					},
					"size_steps": {
						"percent": 20
					}
				}
			}
		},
		// This project exists, so this negative seed will be applied.
		{
			"project_name": "Second Project",
			"domain_name": "First Domain",
			"disabled_resources": ["fo*"]
		},
		// This project does not exist, so this seed will be skipped.
		{
			"project_name": "Third Project",
			"domain_name": "Unknown Domain",
			"resources": {
				"foo": {
					"critical_threshold": {
						"usage_percent": {
							"singular": 95
						}
					},
					"size_steps": {
						"percent": 20
					}
				}
			}
		}
	]
}`

func TestResourceSeedingSuccess(baseT *testing.T) {
	t := test.T{T: baseT}
	cfg := configFromJSON(t, resourceSeedingConfigGood)
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
const resourceSeedingConfigBadResource = `{
	"project_seeds": [
		{
			"project_name": "First Project",
			"domain_name": "First Domain",
			"resources": {
				"foo": {
					"high_threshold": {
						"usage_percent": {
							"singular": 90
						}
					},
					"size_steps": {
						"percent": 20
					}
				}
			}
		}
	]
}`

func TestResourceSeedingBadResource(baseT *testing.T) {
	t := test.T{T: baseT}
	cfg := configFromJSON(t, resourceSeedingConfigBadResource)
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

// removeCommentsFromJSON removes C-style comments from JSON literals.
// It is intended only for use with JSON literals that appear in test code.
// Its implementation is very simple and not intended for use with untrusted inputs.
func removeCommentsFromJSON(jsonStr string) string {
	singleLineCommentRegex := regexp.MustCompile(`//[^\n]*`)
	multiLineCommentRegex := regexp.MustCompile(`(?s)/\*.*?\*/`)
	emptyLineRegex := regexp.MustCompile(`\n\s*\n`)

	result := singleLineCommentRegex.ReplaceAllString(jsonStr, "")
	result = multiLineCommentRegex.ReplaceAllString(result, "")
	result = emptyLineRegex.ReplaceAllString(result, "\n")
	return result
}

func TestRemoveCommentsFromJSON(t *testing.T) {
	jsonStr := `{
    "name": "test", // This is an inline comment
    // This is a single line comment
    "value": 42, // Another inline comment
    /* This is a multiline
      comment that spans
      multiple lines */
    "enabled": true, // Final inline comment
    // Another single line comment
    "config": {
      "debug": false /* inline multiline comment */
    }
  }`

	expected := jsonmatch.Object{
		"name":    "test",
		"value":   42,
		"enabled": true,
		"config": jsonmatch.Object{
			"debug": false,
		},
	}

	result := removeCommentsFromJSON(jsonStr)

	for _, diff := range expected.DiffAgainst([]byte(result)) {
		if diff.Pointer == "" {
			t.Errorf("%s: expected %s, but got %s", diff.Kind, diff.ExpectedJSON, diff.ActualJSON)
		} else {
			t.Errorf("%s at %s: expected %s, but got %s", diff.Kind, diff.Pointer, diff.ExpectedJSON, diff.ActualJSON)
		}
	}
}
