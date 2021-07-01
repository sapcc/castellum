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
	"errors"
	"fmt"

	"github.com/gophercloud/gophercloud"
	"github.com/sapcc/castellum/internal/db"
)

//AssetStatus shows the current state of an asset. It is returned by AssetManager.GetAssetStatus().
type AssetStatus struct {
	Size  uint64
	Usage db.UsageValues
}

//AssetTypeInfo describes an AssetType supported by an AssetManager.
type AssetTypeInfo struct {
	AssetType    db.AssetType
	UsageMetrics []db.UsageMetric
}

//MakeZeroUsageValues is a convenience function to instantiate an all-zero UsageValues for this AssetType.
func (info AssetTypeInfo) MakeZeroUsageValues() db.UsageValues {
	vals := make(db.UsageValues, len(info.UsageMetrics))
	for _, metric := range info.UsageMetrics {
		vals[metric] = 0
	}
	return vals
}

//AssetNotFoundErr is returned by AssetManager.GetAssetStatus() if the
//concerning asset can not be found in the respective backend.
type AssetNotFoundErr struct {
	InnerError error
}

func (e AssetNotFoundErr) Error() string {
	return e.InnerError.Error()
}

var (
	//ErrNoConfigurationAllowed is returned by AssetManager.CheckResourceAllowed()
	//when the user has given configuration, but the asset type in question does
	//not accept any configuration.
	ErrNoConfigurationAllowed = errors.New("no configuration allowed for this asset type")
	//ErrNoConfigurationProvided is returned by
	//AssetManager.CheckResourceAllowed() when the user has not given
	//configuration, but the asset type in question requires configuration.
	ErrNoConfigurationProvided = errors.New("type-specific configuration must be provided for this asset type")
)

//AssetManager is the main modularization interface in Castellum. It
//provides a separation boundary between the plugins that implement the
//concrete behavior for specific asset types, and the core logic of Castellum.
//It is created by CreateAssetManagers() using AssetManagerFactory.
type AssetManager interface {
	//If this asset type is supported by this asset manager, return information
	//about it. Otherwise return nil.
	InfoForAssetType(assetType db.AssetType) *AssetTypeInfo

	//A non-nil return value makes the API deny any attempts to create a resource
	//with that scope and asset type with that error.
	//
	//The initial intended purpose is to allow resources for some scopes, but
	//deny them for others. The second purpose is to validate plugin-specific
	//configuration passed in the `configJSON` parameter.
	//
	//Simple implementations should return nil for empty `configJSON` and
	//`core.ErrNoConfigurationAllowed` otherwise.
	CheckResourceAllowed(assetType db.AssetType, scopeUUID string, configJSON string) error

	ListAssets(res db.Resource) ([]string, error)
	//The returned Outcome should be either Succeeded, Failed or Errored, but not Cancelled.
	//The returned error should be nil if and only if the outcome is Succeeded.
	SetAssetSize(res db.Resource, assetUUID string, oldSize, newSize uint64) (db.OperationOutcome, error)
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
type AssetManagerFactory func(*ProviderClient, gophercloud.EndpointOpts) (AssetManager, error)

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
func CreateAssetManagers(factoryIDs []string, provider *ProviderClient, eo gophercloud.EndpointOpts) (AssetManagerTeam, error) {
	team := make(AssetManagerTeam, len(factoryIDs))
	for idx, factoryID := range factoryIDs {
		factory, exists := assetManagerFactories[factoryID]
		if !exists {
			return nil, fmt.Errorf("unknown asset manager: %q", factoryID)
		}
		var err error
		team[idx], err = factory(provider, eo)
		if err != nil {
			return nil, fmt.Errorf("cannot initialize asset manager %q: %s", factoryID, err.Error())
		}
	}
	return team, nil
}

//ForAssetType returns the asset manager for the given asset type, or nil if
//the asset type is not supported.
func (team AssetManagerTeam) ForAssetType(assetType db.AssetType) (AssetManager, AssetTypeInfo) {
	for _, manager := range team {
		info := manager.InfoForAssetType(assetType)
		if info != nil {
			return manager, *info
		}
	}
	return nil, AssetTypeInfo{
		//provide a reasonable fallback for AssetTypeInfo
		AssetType:    assetType,
		UsageMetrics: []db.UsageMetric{db.SingularUsageMetric},
	}
}
