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

package core

import (
	"fmt"

	"github.com/gophercloud/gophercloud"
	"github.com/sapcc/castellum/internal/db"
)

//AssetStatus shows the current state of an asset. It is returned by AssetManager.GetProjectAssetStatus().
type AssetStatus struct {
	Size         uint64
	UsagePercent uint32
}

//AssetManager is the main modularization interface in Castellum. It
//provides a separation boundary between the plugins that implement the
//concrete behavior for specific asset types, and the core logic of Castellum.
//It is created by CreateAssetManagers() using AssetManagerFactory.
type AssetManager interface {
	//Returns the list of all asset types supported by this asset manager.
	AssetTypes() []string

	ListAssets(res db.Resource) ([]string, error)
	SetAssetSize(res db.Resource, assetUUID string, size uint64) error
	//previousStatus will be nil when this function is called for the first time
	//for the given asset.
	GetAssetStatus(res db.Resource, assetUUID string, previousStatus *AssetStatus) (AssetStatus, error)
}

//AssetManagerFactory is something that creates AssetManager instances. This
//intermediate step is useful because Castellum should not always support all
//types of assets. By having plugins register a factory instead of an
//AssetManager instance, we can ensure that only the selected asset managers
//get instantiated.
//
//The supplied ProviderClient should be stored inside the AssetManager instance
//for later usage. It can also be used to query OpenStack capabilities.
type AssetManagerFactory func(*gophercloud.ProviderClient) (AssetManager, error)

var assetManagerFactories = make(map[string]AssetManagerFactory)

//RegisterAssetManagerFactory registers an AssetManagerFactory with this package.
//The given ID must be unique among all factories. It appears in the
//CASTELLUM_ASSET_MANAGERS environment variable that controls which factories
//are used.
func RegisterAssetManagerFactory(id string, factory AssetManagerFactory) {
	if id == "" {
		panic("RegisterAssetManagerFactory called with empty ID!")
	}
	if _, exists := assetManagerFactories[id]; exists {
		panic(fmt.Sprintf("RegisterAssetManagerFactory called multiple times for ID = %q", id))
	}
	if factory == nil {
		panic(fmt.Sprintf("RegisterAssetManagerFactory called with factory = nil! (ID = %q)", id))
	}
	assetManagerFactories[id] = factory
}

//AssetManagerTeam is the set of AssetManager instances that Castellum is using.
type AssetManagerTeam []AssetManager

//CreateAssetManagers prepares a set of AssetManager instances for a single run
//of Castellum. The first argument is the list of IDs of all factories that
//shall be used to create asset managers.
func CreateAssetManagers(factoryIDs []string, provider *gophercloud.ProviderClient) (AssetManagerTeam, error) {
	team := make(AssetManagerTeam, len(factoryIDs))
	for idx, factoryID := range factoryIDs {
		factory, exists := assetManagerFactories[factoryID]
		if !exists {
			return nil, fmt.Errorf("unknown asset manager: %q", factoryID)
		}
		var err error
		team[idx], err = factory(provider)
		if err != nil {
			return nil, fmt.Errorf("cannot initialize asset manager %q: %s", factoryID, err.Error())
		}
	}
	return team, nil
}

//ForAssetType returns the asset manager for the given asset type, or nil if
//the asset type is not supported.
func (team AssetManagerTeam) ForAssetType(assetType string) AssetManager {
	for _, manager := range team {
		types := manager.AssetTypes()
		for _, t := range types {
			if assetType == t {
				return manager
			}
		}
	}
	return nil
}
