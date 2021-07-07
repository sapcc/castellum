/******************************************************************************
*
*  Copyright 2021 SAP SE
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
	"strings"

	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
)

type assetManagerServerGroups struct{}

func init() {
	core.RegisterAssetManagerFactory("server-groups", func(provider core.ProviderClient) (core.AssetManager, error) {
		//TODO parse CASTELLUM_SERVERGROUPS_PROMETHEUS_URL
		//TODO parse CASTELLUM_SERVERGROUPS_LOCAL_ROLES
		return &assetManagerServerGroups{}, nil
	})
}

//InfoForAssetType implements the core.AssetManager interface.
func (m *assetManagerServerGroups) InfoForAssetType(assetType db.AssetType) *core.AssetTypeInfo {
	if strings.HasPrefix(string(assetType), "server-group:") {
		return &core.AssetTypeInfo{
			AssetType:    assetType,
			UsageMetrics: []db.UsageMetric{"cpu", "ram"},
		}
	}
	return nil
}

//CheckResourceAllowed implements the core.AssetManager interface.
func (m *assetManagerServerGroups) CheckResourceAllowed(assetType db.AssetType, scopeUUID string, configJSON string) error {
	//TODO
	return errors.New("unimplemented")
}

//ListAssets implements the core.AssetManager interface.
func (m *assetManagerServerGroups) ListAssets(res db.Resource) ([]string, error) {
	groupUUID := strings.TrimPrefix(string(res.AssetType), "server-group:")
	return []string{groupUUID}, nil
}

//GetAssetStatus implements the core.AssetManager interface.
func (m *assetManagerServerGroups) GetAssetStatus(res db.Resource, assetUUID string, previousStatus *core.AssetStatus) (core.AssetStatus, error) {
	//TODO
	return core.AssetStatus{}, errors.New("unimplemented")
}

//SetAssetSize implements the core.AssetManager interface.
func (m *assetManagerServerGroups) SetAssetSize(res db.Resource, assetUUID string, oldSize, newSize uint64) (db.OperationOutcome, error) {
	//TODO
	return db.OperationOutcomeErrored, errors.New("unimplemented")
}
