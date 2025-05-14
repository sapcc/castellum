// SPDX-FileCopyrightText: 2020 SAP SE
// SPDX-License-Identifier: Apache-2.0

package core

import (
	"fmt"
	"os"

	"github.com/sapcc/go-api-declarations/castellum"
	"github.com/sapcc/go-bits/regexpext"
	"gopkg.in/yaml.v2"

	"github.com/sapcc/castellum/internal/db"
)

// Config contains everything that we found in the configuration file.
type Config struct {
	MaxAssetSizeRules []MaxAssetSizeRule `yaml:"max_asset_sizes"`
	ProjectSeeds      []ProjectSeed      `yaml:"project_seeds"`
}

// LoadConfig loads the configuration file from the given path.
func LoadConfig(configPath string) (Config, error) {
	buf, err := os.ReadFile(configPath)
	if err != nil {
		return Config{}, err
	}

	var cfg Config
	err = yaml.UnmarshalStrict(buf, &cfg)
	if err != nil {
		return Config{}, fmt.Errorf("could not parse %s: %w", configPath, err)
	}

	return cfg, nil
}

// MaxAssetSizeRule appears in type Config.
type MaxAssetSizeRule struct {
	AssetTypeRx regexpext.BoundedRegexp `yaml:"asset_type"`
	ScopeUUID   string                  `yaml:"scope_uuid"` // leave empty to have the rule apply to all scopes
	Value       uint64                  `yaml:"value"`
}

// MaxAssetSizeFor computes the highest permissible max_size value for this
// asset type. If no constraints apply, nil is returned.
func (c Config) MaxAssetSizeFor(assetType db.AssetType, scopeUUID string) (result *uint64) {
	for _, rule := range c.MaxAssetSizeRules {
		if rule.AssetTypeRx.MatchString(string(assetType)) && (rule.ScopeUUID == "" || rule.ScopeUUID == scopeUUID) {
			val := rule.Value
			result = &val
		}
	}

	return
}

// ProjectSeed appears in type Seed.
type ProjectSeed struct {
	ProjectName             string                              `yaml:"project_name"`
	DomainName              string                              `yaml:"domain_name"`
	Resources               map[db.AssetType]castellum.Resource `yaml:"resources"`
	DisabledResourceRegexps []regexpext.BoundedRegexp           `yaml:"disabled_resources"`
}

// IsSeededResource returns true if the config contains a seed for this
// resource. This is used by the API to reject PUT/DELETE requests to seeded
// resources.
func (c Config) IsSeededResource(project CachedProject, domain CachedDomain, assetType db.AssetType) bool {
	for _, s := range c.ProjectSeeds {
		if project.Name == s.ProjectName && domain.Name == s.DomainName {
			return s.isSeededResource(assetType)
		}
	}
	return false
}

func (s ProjectSeed) isSeededResource(assetType db.AssetType) bool {
	_, exists := s.Resources[assetType]
	return exists || s.ForbidsResource(assetType)
}

func (s ProjectSeed) ForbidsResource(assetType db.AssetType) bool {
	for _, rx := range s.DisabledResourceRegexps {
		if rx.MatchString(string(assetType)) {
			return true
		}
	}

	return false
}
