// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package castellum

import (
	"encoding/json"

	. "github.com/majewsky/gg/option"
)

// Resource is the API representation of a resource.
type Resource struct {
	// fields that only appear in GET responses
	Checked    Option[Checked] `json:"checked,omitzero"`
	AssetCount int64           `json:"asset_count"`

	// fields that are also allowed in PUT requests
	ConfigJSON        Option[json.RawMessage] `json:"config,omitzero"`
	LowThreshold      Option[Threshold]       `json:"low_threshold,omitzero"`
	HighThreshold     Option[Threshold]       `json:"high_threshold,omitzero"`
	CriticalThreshold Option[Threshold]       `json:"critical_threshold,omitzero"`
	SizeConstraints   Option[SizeConstraints] `json:"size_constraints,omitzero"`
	SizeSteps         SizeSteps               `json:"size_steps"`
}

// Threshold appears in type Resource.
type Threshold struct {
	UsagePercent UsageValues `json:"usage_percent"`
	DelaySeconds uint32      `json:"delay_seconds,omitempty"`
}

// SizeSteps appears in type Resource.
type SizeSteps struct {
	Percent float64 `json:"percent,omitempty"`
	Single  bool    `json:"single,omitempty"`
}

// SizeConstraints appears in type Resource.
type SizeConstraints struct {
	Minimum               Option[uint64] `json:"minimum,omitzero"`
	Maximum               Option[uint64] `json:"maximum,omitzero"`
	MinimumFree           Option[uint64] `json:"minimum_free,omitzero"`
	MinimumFreeIsCritical bool           `json:"minimum_free_is_critical,omitempty"`
}
