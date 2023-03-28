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

import "encoding/json"

// Resource is the API representation of a resource.
type Resource struct {
	//fields that only appear in GET responses
	Checked    *Checked `json:"checked,omitempty"`
	AssetCount int64    `json:"asset_count"`

	//fields that are also allowed in PUT requests
	ConfigJSON        *json.RawMessage `json:"config,omitempty"`
	LowThreshold      *Threshold       `json:"low_threshold,omitempty"`
	HighThreshold     *Threshold       `json:"high_threshold,omitempty"`
	CriticalThreshold *Threshold       `json:"critical_threshold,omitempty"`
	SizeConstraints   *SizeConstraints `json:"size_constraints,omitempty"`
	SizeSteps         SizeSteps        `json:"size_steps"`
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
	Minimum     *uint64 `json:"minimum,omitempty"`
	Maximum     *uint64 `json:"maximum,omitempty"`
	MinimumFree *uint64 `json:"minimum_free,omitempty"`
}
