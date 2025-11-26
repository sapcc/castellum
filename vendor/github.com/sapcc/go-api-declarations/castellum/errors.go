// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

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
	AssetUUID   string  `json:"asset_id"`
	ProjectUUID string  `json:"project_id,omitempty"`
	DomainUUID  string  `json:"domain_id"`
	AssetType   string  `json:"asset_type"`
	Checked     Checked `json:"checked"`
}

// AssetResizeError is the API representation for an asset resize error.
type AssetResizeError struct {
	AssetUUID   string          `json:"asset_id"`
	ProjectUUID string          `json:"project_id,omitempty"`
	DomainUUID  string          `json:"domain_id"`
	AssetType   string          `json:"asset_type"`
	OldSize     uint64          `json:"old_size,omitempty"`
	NewSize     uint64          `json:"new_size,omitempty"`
	Finished    OperationFinish `json:"finished"`
}
