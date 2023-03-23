// Copyright 2023 SAP SE
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package castellum

// ResourceScrapeError is the API representation for a resource scrape error.
type ResourceScrapeError struct {
	ProjectUUID string  `json:"project_id,omitempty"`
	DomainUUID  string  `json:"domain_id"`
	AssetType   string  `json:"asset_type"`
	Checked     Checked `json:"checked"`
}

// AssetScrapeError is the API representation for an asset scrape error.
type AssetScrapeError struct {
	AssetUUID   string   `json:"asset_id"`
	ProjectUUID string   `json:"project_id,omitempty"`
	DomainUUID  string   `json:"domain_id"`
	AssetType   string   `json:"asset_type"`
	Checked     *Checked `json:"checked,omitempty"`
}

// AssetResizeError is the API representation for an asset resize error.
type AssetResizeError struct {
	AssetUUID   string           `json:"asset_id"`
	ProjectUUID string           `json:"project_id,omitempty"`
	DomainUUID  string           `json:"domain_id"`
	AssetType   string           `json:"asset_type"`
	OldSize     uint64           `json:"old_size,omitempty"`
	NewSize     uint64           `json:"new_size,omitempty"`
	Finished    *OperationFinish `json:"finished,omitempty"`
}
