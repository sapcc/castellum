// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package tasks

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sapcc/go-bits/jobloop"
	"github.com/sapcc/go-bits/logg"

	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
)

// ResourceSeedingJob applies the resource seed from the Config every few minutes.
//
// Since the seed is static for the duration of the program's runtime, it looks
// like it should only be necessary once to do at startup. But project seeds
// only apply if the project in question exists. Hence, we check back every few
// minutes to see if a project which we are interested in and which was missing
// before has now shown up.
func (c *Context) ResourceSeedingJob(registerer prometheus.Registerer) jobloop.Job {
	return (&jobloop.CronJob{
		Metadata: jobloop.JobMetadata{
			ReadableName: "resource seeding",
			CounterOpts: prometheus.CounterOpts{
				Name: "castellum_resource_seeding_runs",
				Help: "Counter for resource seeding runs.",
			},
		},
		Interval: 5 * time.Minute,
		Task: func(ctx context.Context, _ prometheus.Labels) error {
			return c.applyResourceSeeds(ctx)
		},
	}).Setup(registerer)
}

func (c *Context) applyResourceSeeds(ctx context.Context) error {
	var missingProjects []string
	for _, seed := range c.Config.ProjectSeeds {
		projectUUID, err := c.ProviderClient.FindProjectID(ctx, seed.ProjectName, seed.DomainName)
		if err != nil {
			return fmt.Errorf(`cannot find project "%s/%s": %w`, seed.DomainName, seed.ProjectName, err)
		}
		if projectUUID == "" {
			// project does not exist in Keystone -> skip this project seed this time
			missingProjects = append(missingProjects, fmt.Sprintf(`"%s/%s"`, seed.DomainName, seed.ProjectName))
			continue
		}

		err = c.applyProjectSeed(ctx, projectUUID, seed)
		if err != nil {
			return fmt.Errorf(`while applying seed for project "%s/%s" (%s): %w`, seed.DomainName, seed.ProjectName, projectUUID, err)
		}
	}

	if len(missingProjects) > 0 {
		sort.Strings(missingProjects)
		logg.Info("while applying the resource seed: %d projects were skipped because they do not exist in Keystone: %s",
			len(missingProjects), strings.Join(missingProjects, ", "))
	}

	return nil
}

func (c *Context) applyProjectSeed(ctx context.Context, projectUUID string, seed core.ProjectSeed) error {
	// list existing resources
	var dbResources []db.Resource
	_, err := c.DB.Select(&dbResources,
		`SELECT * FROM resources WHERE scope_uuid = $1`, projectUUID)
	if err != nil {
		return err
	}
	isExistingResource := make(map[db.AssetType]struct{})
	for _, dbResource := range dbResources {
		isExistingResource[dbResource.AssetType] = struct{}{}
	}

	// check existing resources (positive seeds take preference over negative seeds)
	for _, dbResource := range dbResources {
		resource, exists := seed.Resources[dbResource.AssetType]
		if exists {
			// apply positive seed
			dbResourceCopy := dbResource
			errs := core.ApplyResourceSpecInto(ctx, &dbResourceCopy, resource, isExistingResource, c.Config, c.Team)
			if !errs.IsEmpty() {
				return fmt.Errorf("cannot apply %s seed: %s", dbResource.AssetType, errs.Join(", "))
			}
			if !reflect.DeepEqual(dbResource, dbResourceCopy) {
				logg.Info("applying %s seed for project %s/%s...", dbResource.AssetType, seed.DomainName, seed.ProjectName)
				_, err := c.DB.Update(&dbResourceCopy)
				if err != nil {
					return err
				}
			}
		} else if seed.ForbidsResource(dbResource.AssetType) {
			// enforce negative seed
			logg.Info("enforcing negative %s seed for project %s/%s...", dbResource.AssetType, seed.DomainName, seed.ProjectName)
			_, err := c.DB.Delete(&dbResource)
			if err != nil {
				return err
			}
			delete(isExistingResource, dbResource.AssetType)
		}
	}

	// create missing resources from positive seeds
	for assetType, resource := range seed.Resources {
		_, exists := isExistingResource[assetType]
		if exists {
			continue
		}

		proj, err := c.ProviderClient.GetProject(ctx, projectUUID)
		if err != nil {
			return err
		}
		dbResource := db.Resource{
			ScopeUUID:    projectUUID,
			DomainUUID:   proj.DomainID,
			AssetType:    assetType,
			NextScrapeAt: time.Unix(0, 0).UTC(), // give new resources a very early next_scrape_at to prioritize them in the scrape queue
		}
		errs := core.ApplyResourceSpecInto(ctx, &dbResource, resource, isExistingResource, c.Config, c.Team)
		if !errs.IsEmpty() {
			return fmt.Errorf("cannot apply %s seed: %s", dbResource.AssetType, errs.Join(", "))
		}
		logg.Info("applying %s seed for project %s/%s...", dbResource.AssetType, seed.DomainName, seed.ProjectName)
		err = c.DB.Insert(&dbResource)
		if err != nil {
			return err
		}
		isExistingResource[assetType] = struct{}{}
	}

	return nil
}
