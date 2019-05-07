/******************************************************************************
*
*  Copyright 2019 SAP SE
*
*  Licensed under the Apache License, Version 2.0 (the "License");
*  you may not use this file except in compliance with the License.
*  You may obtain a copy of the License at
*
*      http://www.apache.org/licenses/LICENSE-2.0
*
*  Unless required by applicable law or agreed to in writing, software
*  distributed under the License is distributed on an "AS IS" BASIS,
*  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
*  See the License for the specific language governing permissions and
*  limitations under the License.
*
******************************************************************************/

package plugins

import (
	"errors"
	"sort"

	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
)

//StaticAsset represents an asset managed by AssetManagerStatic. It is only
//used in tests as a double for an actual asset.
type StaticAsset struct {
	Size  uint64
	Usage uint64
}

//AssetManagerStatic is a core.AssetManager for testing purposes. It just
//contains a static list of assets for a single asset type. No requests against
//OpenStack are ever made by it.
//
//Attempts to resize assets will succeed if and only if `newSize > usage`.
type AssetManagerStatic struct {
	AssetType db.AssetType
	Assets    map[string]map[string]StaticAsset
}

//AssetTypes implements the core.AssetManager interface.
func (m AssetManagerStatic) AssetTypes() []db.AssetType {
	return []db.AssetType{m.AssetType}
}

var (
	errWrongAssetType = errors.New("wrong asset type for this asset manager")
	errUnknownProject = errors.New("no such project")
	errUnknownAsset   = errors.New("no such asset")
	errTooSmall       = errors.New("cannot set size smaller than current usage")
)

//ListAssets implements the core.AssetManager interface.
func (m AssetManagerStatic) ListAssets(res db.Resource) ([]string, error) {
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
	sort.Strings(uuids) //for deterministic test behavior
	return uuids, nil
}

//GetAssetStatus implements the core.AssetManager interface.
func (m AssetManagerStatic) GetAssetStatus(res db.Resource, assetUUID string, previousStatus *core.AssetStatus) (core.AssetStatus, error) {
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
	return core.AssetStatus{
		Size:         asset.Size,
		UsagePercent: uint32(asset.Usage * 100 / asset.Size),
	}, nil
}

//SetAssetSize implements the core.AssetManager interface.
func (m AssetManagerStatic) SetAssetSize(res db.Resource, assetUUID string, size uint64) error {
	if res.AssetType != m.AssetType {
		return errWrongAssetType
	}
	assets, exists := m.Assets[res.ScopeUUID]
	if !exists {
		return errUnknownProject
	}
	asset, exists := assets[assetUUID]
	if !exists {
		return errUnknownAsset
	}
	if asset.Usage > size {
		return errTooSmall
	}
	assets[assetUUID] = StaticAsset{size, asset.Usage}
	return nil
}
