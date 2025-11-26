// SPDX-FileCopyrightText: 2019 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"context"
	"errors"
	"fmt"
	"sort"

	. "github.com/majewsky/gg/option"
	"github.com/sapcc/go-api-declarations/castellum"

	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
)

// StaticAsset represents an asset managed by AssetManagerStatic. It is only
// used in tests as a double for an actual asset.
type StaticAsset struct {
	Size              uint64
	Usage             uint64
	StrictMinimumSize Option[uint64]
	StrictMaximumSize Option[uint64]

	// When non-zero, these fields model a resize operation that will only be
	// reflected after GetAssetStatus() has been called for as many times as
	// indicated in the .RemainingDelay field.
	NewSize        uint64
	RemainingDelay uint

	// When true, return a bogus error from GetAssetStatus().
	CannotGetAssetStatus bool

	// When true, return a core.AssetNotFoundError from GetAssetStatus().
	CannotFindAsset bool
}

// AssetManagerStatic is a core.AssetManager for testing purposes. It just
// contains a static list of assets for a single asset type. No requests against
// OpenStack are ever made by it.
//
// Attempts to resize assets will succeed if and only if `newSize > usage`.
type AssetManagerStatic struct {
	AssetType                 db.AssetType
	Assets                    map[string]map[string]StaticAsset
	UsageMetrics              []castellum.UsageMetric
	CheckResourceAllowedFails bool
	SetAssetSizeFails         bool
	ExpectsConfiguration      bool
	ConflictsWithAssetType    db.AssetType
}

// PluginTypeID implements the core.AssetManager interface.
func (m AssetManagerStatic) PluginTypeID() string { return "static" }

// Init implements the core.AssetManager interface.
func (m AssetManagerStatic) Init(ctx context.Context, provider core.ProviderClient) (err error) {
	return nil // unused
}

// InfoForAssetType implements the core.AssetManager interface.
func (m AssetManagerStatic) InfoForAssetType(assetType db.AssetType) Option[core.AssetTypeInfo] {
	if assetType == m.AssetType {
		usageMetrics := m.UsageMetrics
		if len(usageMetrics) == 0 {
			usageMetrics = []castellum.UsageMetric{castellum.SingularUsageMetric}
		}
		return Some(core.AssetTypeInfo{
			AssetType:    m.AssetType,
			UsageMetrics: usageMetrics,
		})
	}
	return None[core.AssetTypeInfo]()
}

// CheckResourceAllowed implements the core.AssetManager interface.
func (m AssetManagerStatic) CheckResourceAllowed(_ context.Context, assetType db.AssetType, scopeUUID, configJSON string, existingResources map[db.AssetType]struct{}) error {
	if m.ExpectsConfiguration {
		if configJSON == "" {
			return core.ErrNoConfigurationProvided
		}
		if configJSON != `{"foo":"bar"}` {
			return errors.New("wrong configuration was supplied")
		}
	} else if configJSON != "" {
		return core.ErrNoConfigurationAllowed
	}

	if m.CheckResourceAllowedFails {
		return errSimulatedRejection
	}

	if m.ConflictsWithAssetType != "" {
		for otherAssetType := range existingResources {
			if otherAssetType == m.ConflictsWithAssetType {
				return fmt.Errorf("cannot create %s resource because there is a %s resource", string(assetType), string(otherAssetType))
			}
		}
	}

	return nil
}

var (
	errWrongAssetType      = errors.New("wrong asset type for this asset manager")
	errUnknownProject      = errors.New("no such project")
	errUnknownAsset        = errors.New("no such asset")
	errOldSizeMismatch     = errors.New("asset has different size than expected")
	errTooSmall            = errors.New("cannot set size smaller than current usage")
	errSimulatedRejection  = errors.New("CheckResourceAllowed failing as requested")
	errSimulatedGetFailure = errors.New("GetAssetStatus failing as requested")
	errSimulatedNotFound   = errors.New("GetAssetStatus asset not found in backend")
	errSimulatedSetFailure = errors.New("SetAssetSize failing as requested")
)

// ListAssets implements the core.AssetManager interface.
func (m AssetManagerStatic) ListAssets(_ context.Context, res db.Resource) ([]string, error) {
	if res.AssetType != m.AssetType {
		return nil, errWrongAssetType
	}
	assets, exists := m.Assets[res.ScopeUUID]
	if !exists {
		return nil, errUnknownProject
	}
	uuids := make([]string, 0, len(assets))
	for uuid := range assets {
		uuids = append(uuids, uuid)
	}
	sort.Strings(uuids) // for deterministic test behavior
	return uuids, nil
}

// GetAssetStatus implements the core.AssetManager interface.
func (m AssetManagerStatic) GetAssetStatus(_ context.Context, res db.Resource, assetUUID string, previousStatus Option[core.AssetStatus]) (core.AssetStatus, error) {
	if res.AssetType != m.AssetType {
		return core.AssetStatus{}, errWrongAssetType
	}
	assets, exists := m.Assets[res.ScopeUUID]
	if !exists {
		return core.AssetStatus{}, errUnknownProject
	}
	asset, exists := assets[assetUUID]
	if !exists {
		return core.AssetStatus{}, errUnknownAsset
	}

	if asset.CannotGetAssetStatus {
		return core.AssetStatus{}, errSimulatedGetFailure
	}
	if asset.CannotFindAsset {
		return core.AssetStatus{}, core.AssetNotFoundError{InnerError: errSimulatedNotFound}
	}

	if asset.NewSize != 0 {
		asset.RemainingDelay--
		if asset.RemainingDelay == 0 {
			asset = StaticAsset{Size: asset.NewSize, Usage: asset.Usage}
		}
		assets[assetUUID] = asset
	}

	return core.AssetStatus{
		Size:              asset.Size,
		Usage:             castellum.UsageValues{castellum.SingularUsageMetric: float64(asset.Usage)},
		StrictMinimumSize: asset.StrictMinimumSize,
		StrictMaximumSize: asset.StrictMaximumSize,
	}, nil
}

// SetAssetSize implements the core.AssetManager interface.
func (m AssetManagerStatic) SetAssetSize(_ context.Context, res db.Resource, assetUUID string, oldSize, newSize uint64) (castellum.OperationOutcome, error) {
	if res.AssetType != m.AssetType {
		return castellum.OperationOutcomeErrored, errWrongAssetType
	}
	assets, exists := m.Assets[res.ScopeUUID]
	if !exists {
		return castellum.OperationOutcomeErrored, errUnknownProject
	}
	asset, exists := assets[assetUUID]
	if !exists {
		return castellum.OperationOutcomeErrored, errUnknownAsset
	}
	if asset.Size != oldSize {
		return castellum.OperationOutcomeErrored, errOldSizeMismatch
	}
	if asset.Usage > newSize {
		return castellum.OperationOutcomeErrored, errTooSmall
	}
	if m.SetAssetSizeFails {
		return castellum.OperationOutcomeFailed, errSimulatedSetFailure
	}
	assets[assetUUID] = StaticAsset{Size: newSize, Usage: asset.Usage}
	return castellum.OperationOutcomeSucceeded, nil
}
