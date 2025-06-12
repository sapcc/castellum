// SPDX-FileCopyrightText: 2019 SAP SE
// SPDX-License-Identifier: Apache-2.0

package core

import (
	"context"
	"errors"
	"fmt"

	"github.com/sapcc/go-api-declarations/castellum"
	"github.com/sapcc/go-bits/pluggable"

	"github.com/sapcc/castellum/internal/db"
)

// AssetStatus shows the current state of an asset.
//
// It is returned by AssetManager.GetAssetStatus(). The semantics of all fields
// match their equivalently-named counterparts in the db.Asset type.
type AssetStatus struct {
	Size              uint64
	Usage             castellum.UsageValues
	StrictMinimumSize *uint64
	StrictMaximumSize *uint64
}

// StatusOfAsset converts an Asset into just its AssetStatus.
//
// This is used when passing an Asset from a high-level workflow function into
// a low-level logic function. Low-level functions explicitly take only the
// AssetStatus to avoid accidental dependencies on non-logic attributes
// like timestamps, UUIDs or error message strings.
func StatusOfAsset(asset db.Asset, cfg Config, res db.Resource) AssetStatus {
	strictMaximumSize := asset.StrictMaximumSize
	if val := cfg.MaxAssetSizeFor(res.AssetType, res.ScopeUUID); val != nil && (strictMaximumSize == nil || *val < *strictMaximumSize) {
		strictMaximumSize = val
	}
	return AssetStatus{
		Size:              asset.Size,
		Usage:             asset.Usage,
		StrictMinimumSize: asset.StrictMinimumSize,
		StrictMaximumSize: strictMaximumSize,
	}
}

// AssetTypeInfo describes an AssetType supported by an AssetManager.
type AssetTypeInfo struct {
	AssetType    db.AssetType
	UsageMetrics []castellum.UsageMetric
}

// Identifier inserts the metric name into the given format string, but returns
// the empty string for SingularUsageMetric. It is used when building log messages.
func Identifier(metric castellum.UsageMetric, format string) string {
	if metric == castellum.SingularUsageMetric {
		return ""
	}
	return fmt.Sprintf(format, metric)
}

// MakeZeroUsageValues is a convenience function to instantiate an all-zero UsageValues for this AssetType.
func (info AssetTypeInfo) MakeZeroUsageValues() castellum.UsageValues {
	vals := make(castellum.UsageValues, len(info.UsageMetrics))
	for _, metric := range info.UsageMetrics {
		vals[metric] = 0
	}
	return vals
}

// AssetNotFoundError is returned by AssetManager.GetAssetStatus() if the
// concerning asset can not be found in the respective backend.
type AssetNotFoundError struct {
	InnerError error
}

func (e AssetNotFoundError) Error() string {
	return e.InnerError.Error()
}

var (
	// ErrNoConfigurationAllowed is returned by AssetManager.CheckResourceAllowed()
	// when the user has given configuration, but the asset type in question does
	// not accept any configuration.
	ErrNoConfigurationAllowed = errors.New("no configuration allowed for this asset type")
	// ErrNoConfigurationProvided is returned by
	// AssetManager.CheckResourceAllowed() when the user has not given
	// configuration, but the asset type in question requires configuration.
	ErrNoConfigurationProvided = errors.New("type-specific configuration must be provided for this asset type")
)

// AssetManager is the main modularization interface in Castellum. It
// provides a separation boundary between the plugins that implement the
// concrete behavior for specific asset types, and the core logic of Castellum.
// It is created by CreateAssetManagers() using AssetManagerRegistry.
type AssetManager interface {
	pluggable.Plugin

	// Init is called before all other interface methods, and can be used by the
	// AssetManager to receive a reference to the ProviderClient, as well as
	// perform any first-time initialization.
	//
	// The supplied ProviderClient should be stored inside the AssetManager
	// instance for later usage and/or used to query OpenStack capabilities.
	Init(ctx context.Context, provider ProviderClient) error

	// If this asset type is supported by this asset manager, return information
	// about it. Otherwise return nil.
	InfoForAssetType(assetType db.AssetType) *AssetTypeInfo

	// A non-nil return value makes the API deny any attempts to create a resource
	// with that scope and asset type with that error.
	//
	// This can perform multiple types of validations:
	//- allowing resources for some scopes, but not others (e.g. only projects
	//  with a specific marker)
	//- validating plugin-specific configuration in `configJSON`
	//- allowing resources depending on which other resources exist in the same
	// scope, by checking `existingResources`
	//
	// Simple implementations should return nil for empty `configJSON` and
	// `core.ErrNoConfigurationAllowed` otherwise.
	CheckResourceAllowed(ctx context.Context, assetType db.AssetType, scopeUUID string, configJSON string, existingResources map[db.AssetType]struct{}) error

	ListAssets(ctx context.Context, res db.Resource) ([]string, error)
	// The returned Outcome should be either Succeeded, Failed or Errored, but not Cancelled.
	// The returned error should be nil if and only if the outcome is Succeeded.
	SetAssetSize(ctx context.Context, res db.Resource, assetUUID string, oldSize, newSize uint64) (castellum.OperationOutcome, error)
	// previousStatus will be nil when this function is called for the first time
	// for the given asset.
	GetAssetStatus(ctx context.Context, res db.Resource, assetUUID string, previousStatus *AssetStatus) (AssetStatus, error)
}

// AssetManagerRegistry is a pluggable.Registry for AssetManager implementations.
var AssetManagerRegistry pluggable.Registry[AssetManager]

// AssetManagerTeam is the set of AssetManager instances that Castellum is using.
type AssetManagerTeam []AssetManager

// CreateAssetManagers prepares a set of AssetManager instances for a single run
// of Castellum. The first argument is the list of IDs of all factories that
// shall be used to create asset managers.
func CreateAssetManagers(ctx context.Context, pluginTypeIDs []string, provider ProviderClient) (AssetManagerTeam, error) {
	team := make(AssetManagerTeam, len(pluginTypeIDs))
	for idx, pluginTypeID := range pluginTypeIDs {
		manager := AssetManagerRegistry.Instantiate(pluginTypeID)
		if manager == nil {
			return nil, fmt.Errorf("unknown asset manager: %q", pluginTypeID)
		}
		err := manager.Init(ctx, provider)
		if err != nil {
			return nil, fmt.Errorf("cannot initialize asset manager %q: %s", pluginTypeID, err.Error())
		}
		team[idx] = manager
	}
	return team, nil
}

// ForAssetType returns the asset manager for the given asset type, or nil if
// the asset type is not supported.
func (team AssetManagerTeam) ForAssetType(assetType db.AssetType) (AssetManager, AssetTypeInfo) {
	for _, manager := range team {
		info := manager.InfoForAssetType(assetType)
		if info != nil {
			return manager, *info
		}
	}
	return nil, AssetTypeInfo{
		// provide a reasonable fallback for AssetTypeInfo
		AssetType:    assetType,
		UsageMetrics: []castellum.UsageMetric{castellum.SingularUsageMetric},
	}
}
