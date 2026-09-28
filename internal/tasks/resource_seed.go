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
	"go.xyrillian.de/gg/gsql"

	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
)

// deleteResourceAndMaybeGCGroup deletes the given resource and
// if that was the last resource in its group, also deletes the group.
func (c *Context) deleteResourceAndMaybeGCGroup(ctx context.Context, dbResource db.Resource) error {
	return c.DB.WithinTransaction(ctx, nil, func(tx *gsql.Tx) error {
		// Lock the group row first so no concurrent INSERT can slip in between
		// the resource DELETE and the group emptiness check.
		_, err := gsql.NoneIfNoRows(db.ResourceGroupStore.SelectOneWhere(ctx, tx, `id = $1 FOR UPDATE`, dbResource.ResourceGroupID))
		if err != nil {
			return err
		}
		if err := db.ResourceStore.Delete(ctx, tx, dbResource); err != nil {
			return err
		}
		return db.GCResourceGroup(ctx, tx, dbResource.ResourceGroupID)
	})
}

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
	// list existing resources for this scope (joined through resource_groups)
	dbResources, err := db.ResourceStore.SelectWhere(ctx, c.DB,
		`resource_group_id IN (SELECT id FROM resource_groups WHERE scope_uuid = $1)`,
		projectUUID,
	).Collect()
	if err != nil {
		return err
	}
	isExistingResource := make(map[db.AssetType]struct{})
	for _, dbResource := range dbResources {
		isExistingResource[dbResource.AssetType] = struct{}{}
	}

	// cache of already-loaded groups by (asset_manager) for this scope
	groupsByManager := make(map[string]db.ResourceGroup)
	loadGroup := func(assetType db.AssetType) (db.ResourceGroup, error) {
		manager := c.Team.PluginTypeIDForAssetType(assetType)
		if group, ok := groupsByManager[manager]; ok {
			return group, nil
		}
		group, err := gsql.NoneIfNoRows(db.ResourceGroupStore.SelectOneWhere(ctx, c.DB, `scope_uuid = $1 AND asset_manager = $2`, projectUUID, manager))
		if err != nil {
			return db.ResourceGroup{}, err
		}
		if g, ok := group.Unpack(); ok {
			groupsByManager[manager] = g
			return g, nil
		}
		return db.ResourceGroup{}, nil
	}

	// check existing resources (positive seeds take preference over negative seeds)
	for _, dbResource := range dbResources {
		resource, exists := seed.Resources[dbResource.AssetType]
		if exists {
			// apply positive seed
			group, err := loadGroup(dbResource.AssetType)
			if err != nil {
				return err
			}
			dbResourceCopy := dbResource
			errs := core.ApplyResourceSpecInto(ctx, group, &dbResourceCopy, resource, isExistingResource, c.Config, c.Team)
			if !errs.IsEmpty() {
				return fmt.Errorf("cannot apply %s seed: %s", dbResource.AssetType, errs.Join(", "))
			}
			if !reflect.DeepEqual(dbResource, dbResourceCopy) {
				logg.Info("applying %s seed for project %s/%s...", dbResource.AssetType, seed.DomainName, seed.ProjectName)
				err := db.ResourceStore.Update(ctx, c.DB, dbResourceCopy)
				if err != nil {
					return err
				}
			}
		} else if seed.ForbidsResource(dbResource.AssetType) {
			// enforce negative seed
			logg.Info("enforcing negative %s seed for project %s/%s...", dbResource.AssetType, seed.DomainName, seed.ProjectName)
			if err := c.deleteResourceAndMaybeGCGroup(ctx, dbResource); err != nil {
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
		assetManager := c.Team.PluginTypeIDForAssetType(assetType)

		// Build a temporary group for validation, so that an invalid seed does not leave behind a dangling resource group
		tempGroup := db.ResourceGroup{
			ScopeUUID:    projectUUID,
			DomainUUID:   proj.DomainID,
			AssetManager: assetManager,
		}
		// Reuse the persisted group if we already have one cached.
		if g, ok := groupsByManager[assetManager]; ok {
			tempGroup = g
		}

		dbResource := db.Resource{
			AssetType: assetType,
		}
		errs := core.ApplyResourceSpecInto(ctx, tempGroup, &dbResource, resource, isExistingResource, c.Config, c.Team)
		if !errs.IsEmpty() {
			return fmt.Errorf("cannot apply %s seed: %s", dbResource.AssetType, errs.Join(", "))
		}

		// Validation passed, now create resource group, if required, and resource atomically
		err = c.DB.WithinTransaction(ctx, nil, func(tx *gsql.Tx) error {
			group, err := db.EnsureResourceGroup(ctx, tx, projectUUID, proj.DomainID, assetManager)
			if err != nil {
				return err
			}
			groupsByManager[group.AssetManager] = group
			dbResource.ResourceGroupID = group.ID
			return db.ResourceStore.Insert(ctx, tx, &dbResource)
		})
		if err != nil {
			return err
		}
		logg.Info("applying %s seed for project %s/%s...", dbResource.AssetType, seed.DomainName, seed.ProjectName)
		isExistingResource[assetType] = struct{}{}
	}

	return nil
}
