// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package castellum

import . "github.com/majewsky/gg/option"

// Asset is the API representation of an asset.
type Asset struct {
	UUID               string                      `json:"id"`
	Size               uint64                      `json:"size,omitempty"`
	UsagePercent       UsageValues                 `json:"usage_percent"`
	MinimumSize        Option[uint64]              `json:"min_size,omitzero"`
	MaximumSize        Option[uint64]              `json:"max_size,omitzero"`
	Checked            Option[Checked]             `json:"checked,omitzero"`
	Stale              bool                        `json:"stale"`
	PendingOperation   Option[StandaloneOperation] `json:"pending_operation,omitzero"`
	FinishedOperations []StandaloneOperation       `json:"finished_operations,omitempty"`
}

// Checked appears in type Asset and Resource.
type Checked struct {
	ErrorMessage string `json:"error,omitempty"`
}

// StandaloneOperation is the API representation for a pending or finished
// resize operation when the operation stands on its one or within a larger
// list of operations.
type StandaloneOperation struct {
	Operation
	ProjectUUID string `json:"project_id,omitempty"`
	AssetType   string `json:"asset_type,omitempty"`
	AssetID     string `json:"asset_id,omitempty"`
}

// StandaloneOperation is the API representation for a pending or finished
// resize operation when the operation appears within its respective asset.
type Operation struct {
	State     OperationState                `json:"state"`
	Reason    OperationReason               `json:"reason"`
	OldSize   uint64                        `json:"old_size"`
	NewSize   uint64                        `json:"new_size"`
	Created   OperationCreation             `json:"created"`
	Confirmed Option[OperationConfirmation] `json:"confirmed,omitzero"`
	Greenlit  Option[OperationGreenlight]   `json:"greenlit,omitzero"`
	Finished  Option[OperationFinish]       `json:"finished,omitzero"`
}

// OperationCreation appears in type Operation.
type OperationCreation struct {
	AtUnix       int64       `json:"at"`
	UsagePercent UsageValues `json:"usage_percent"`
}

// OperationConfirmation appears in type Operation.
type OperationConfirmation struct {
	AtUnix int64 `json:"at"`
}

// OperationGreenlight appears in type Operation.
type OperationGreenlight struct {
	AtUnix     int64          `json:"at"`
	ByUserUUID Option[string] `json:"by_user,omitzero"`
}

// OperationFinish appears in type Operation.
type OperationFinish struct {
	AtUnix       int64  `json:"at"`
	ErrorMessage string `json:"error,omitempty"`
}
