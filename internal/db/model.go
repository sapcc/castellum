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

package db

import "time"

//ProjectResource describes the autoscaling behavior for a single resource in a
//single project. Note that we reuse Limes terminology here: A project resource
//is the totality of all assets (see type Asset) of a single type within a
//project. For example, a single NFS share is not a resource, it's an asset.
//But it *belongs* to the resource "NFS shares", and more specifically, to the
//project resource "NFS shares for project X".
type ProjectResource struct {
	//The pair of (.ProjectUUID, .AssetType) uniquely identifies a
	//ProjectResource on the API level. Internally, other tables reference
	//ProjectResource by the numeric .ID field.
	ID          int64  `db:"id"`
	ProjectUUID string `db:"project_uuid"`
	AssetType   string `db:"asset_type"`

	//Assets will resize when they have crossed a certain threshold for a certain
	//time. Those thresholds (in percent of usage) and delays (in seconds) are
	//defined here. The "critical" threshold will cause immediate upscaling, so
	//it does not have a configurable delay.
	LowThresholdPercent      uint32 `db:"low_threshold_percent"`
	LowDelaySeconds          uint32 `db:"low_delay_seconds"`
	HighThresholdPercent     uint32 `db:"high_threshold_percent"`
	HighDelaySeconds         uint32 `db:"hight_delay_seconds"`
	CriticalThresholdPercent uint32 `db:"critical_threshold_percent"`

	//This defines how much the the asset's size changes per
	//downscaling/upscaling operation (in % of previous size). This can be NULL
	//when the asset type defines size steps differently. For example, for the
	//asset type "instance", we will have a list of allowed flavors somewhere else.
	SizeStepPercent uint32 `db:"size_step_percent"`
}

//Asset describes a single thing that can be resized dynamically based on its
//utilization. Assets are grouped into project resources, see type
//ProjectResource. Each individual resizing is an operation, see type
//Operation.
type Asset struct {
	//The pair of (.ProjectResourceID, .UUID) uniquely identifies an asset on the
	//API level. Internally, other tables reference ProjectResource by the
	//numeric .ID field.
	//
	//Note that .UUID may be equal to the project's UUID for assets that exist
	//only once per project, e.g. quota. In that case, .UUID does not uniquely
	//identify an asset unless .ProjectResourceID is also considered.
	ID                int64  `db:"id"`
	ProjectResourceID int64  `db:"project_resource_id"`
	UUID              string `db:"uuid"`

	//The asset's current size as reported by OpenStack. The meaning of this
	//value is defined by the plugin that implements this asset type.
	Size uint64 `db:"size"`
	//The asset's current utilization as a percentage of its size. This must
	//always be between 0 and 100.
	UsagePercent uint32 `db:"usage_percent"`
	//When the current .UsagePercent value was obtained.
	ScrapedAt time.Time `db:"scraped_at"`
	//This flag is set by a Castellum worker after a resize operation to indicate
	//that the .Size attribute is outdated.
	Stale bool `db:"stale"`
}

//PendingOperation describes an ongoing resize operation for an asset.
type PendingOperation struct {
	ID      int64           `db:"id"`
	AssetID int64           `db:"asset_id"`
	Reason  OperationReason `db:"reason"`

	//.OldSize and .UsagePercent mirror the state of the asset when the operation
	//was created, and .NewSize defines the target size.
	OldSize      uint64 `db:"old_size"`
	NewSize      uint64 `db:"old_size"`
	UsagePercent uint32 `db:"usage_percent"`

	//This sequence of timestamps represent the various states that an operation enters in its lifecycle.

	//When we first saw usage crossing the threshold.
	CreatedAt time.Time `db:"created_at"`
	//When we confirmed that usage had crossed the threshold for the required time. (For .Reason == OperationReasonCritical, this is equal to CreatedAt.)
	ConfirmedAt *time.Time `db:"confirmed_at"`
	//When a user permitted this operation to go ahead. (For operations not
	//subject to operator approval, this is equal to ConfirmedAt.) The value may
	//be in the future when the operator wants to delay the operation until the
	//next maintenance window. The resize will only be executed once .GreenlitAt
	//is non-null and refers to a point in time that is in the past.
	GreenlitAt *time.Time `db:"greenlit_at"`

	//The UUID of the user that greenlit this operation, if any. If GreenlitAt is
	//not null, but this field is null, it means that the operation did not
	//require operator approval.
	GreenlitByUserUUID *string `db:"greenlit_by_user_uuid"`
}

//FinishedOperation describes a finished resize operation for an asset.
type FinishedOperation struct {
	//All fields are identical in semantics to those in type PendingOperation, except
	//where noted.
	AssetID int64            `db:"asset_id"`
	Reason  OperationReason  `db:"reason"`
	Outcome OperationOutcome `db:"outcome"`

	OldSize      uint64 `db:"old_size"`
	NewSize      uint64 `db:"old_size"`
	UsagePercent uint32 `db:"usage_percent"`

	CreatedAt   time.Time `db:"created_at"`
	ConfirmedAt time.Time `db:"confirmed_at"`
	GreenlitAt  time.Time `db:"greenlit_at"`
	//When the resize operation succeeded, failed, or was cancelled.
	FinishedAt time.Time `db:"finished_at"`

	GreenlitByUserUUID *string `db:"greenlit_by_user_uuid"`
}

//OperationReason is an enumeration type for possible reasons for a resize operation.
type OperationReason string

const (
	//OperationReasonCritical indicates that the resize operation was triggered
	//because the asset's usage exceeded the critical threshold.
	OperationReasonCritical OperationReason = "01-critical"
	//OperationReasonHigh indicates that the resize operation was triggered
	//because the asset's usage exceeded the high threshold.
	OperationReasonHigh = "02-high"
	//OperationReasonLow indicates that the resize operation was triggered
	//because the asset's usage deceeded the low threshold.
	OperationReasonLow = "03-low"
)

//OperationOutcome is an enumeration type for possible outcomes for a resize operation.
type OperationOutcome string

const (
	//OperationOutcomeSucceeded indicates that a resize operation was completed
	//successfully.
	OperationOutcomeSucceeded OperationOutcome = "succeeded"
	//OperationOutcomeFailed indicates that a resize operation failed because of an error in OpenStack.
	OperationOutcomeFailed = "failed"
	//OperationOutcomeCancelled indicates that a resize operation was cancelled. This happens when usage falls back into normal
	OperationOutcomeCancelled = "cancelled"
)
