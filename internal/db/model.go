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

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/go-gorp/gorp/v3"
	"github.com/sapcc/go-api-declarations/castellum"
	"github.com/sapcc/go-bits/easypg"
)

// Resource describes the autoscaling behavior for a single resource in a
// single project or domain. Note that we reuse Limes terminology here: A
// project resource is the totality of all assets (see type Asset) of a single
// type within a project. For example, a single NFS share is not a resource,
// it's an asset. But it *belongs* to the resource "NFS shares", and more
// specifically, to the project resource "NFS shares for project X".
type Resource struct {
	// The pair of (.ScopeUUID, .AssetType) uniquely identifies a Resource on
	// the API level. Internally, other tables reference Resource by the numeric
	// .ID field.
	ID         int64     `db:"id"`
	ScopeUUID  string    `db:"scope_uuid"`  // either project UUID or domain UUID
	DomainUUID string    `db:"domain_uuid"` // for domain resources: equal to .ScopeUUID
	AssetType  AssetType `db:"asset_type"`
	ConfigJSON string    `db:"config_json"` // (optional) config specifically for this asset type

	// Assets will resize when they have crossed a certain threshold for a certain
	// time. Those thresholds (in percent of usage) and delays (in seconds) are
	// defined here. The "critical" threshold will cause immediate upscaling, so
	// it does not have a configurable delay.
	LowThresholdPercent      castellum.UsageValues `db:"low_threshold_percent"`
	LowDelaySeconds          uint32                `db:"low_delay_seconds"`
	HighThresholdPercent     castellum.UsageValues `db:"high_threshold_percent"`
	HighDelaySeconds         uint32                `db:"high_delay_seconds"`
	CriticalThresholdPercent castellum.UsageValues `db:"critical_threshold_percent"`

	// This defines how much the asset's size changes per
	// downscaling/upscaling operation (in % of previous size).
	SizeStepPercent float64 `db:"size_step_percent"`
	// When true, ignore SizeStepPercent and always resize by the smallest step
	// that will move usage back into normal areas.
	SingleStep bool `db:"single_step"`

	// This defines absolute boundaries for the asset size. If configured, resize
	// operations will never move to a size outside this range.
	MinimumSize *uint64 `db:"min_size"`
	MaximumSize *uint64 `db:"max_size"`
	// If configured, downsize operations will be inhibited when
	// `newSize - absoluteUsage` would be smaller than this, and upsize operations
	// will be forced when `currentSize - absoluteUsage` is smaller than this.
	MinimumFreeSize *uint64 `db:"min_free_size"`
	// When true, upsize operations forced by MinimumFreeSize will be critical actions.
	MinimumFreeIsCritical bool `db:"min_free_is_critical"`

	// Contains the error message if the last scrape failed, otherwise an empty string.
	ScrapeErrorMessage string `db:"scrape_error_message"`
	// The next time when this Resource should be checked for new or deleted assets.
	NextScrapeAt time.Time `db:"next_scrape_at"`
	// Contains the duration of the last scrape, or 0 if the resource was never scraped successfully.
	ScrapeDurationSecs float64 `db:"scrape_duration_secs"`
}

// AssetType is the type of Resource.AssetType. It extends type string with some
// convenience methods.
type AssetType string

// PolicyRuleForRead returns the name of the policy rule that allows read access
// to this resource.
func (a AssetType) PolicyRuleForRead() string {
	// only consider the asset type up to the first colon, e.g.
	//  assetType = "quota:compute:instances"
	//  -> result = "project:show:quota"
	assetTypeFields := strings.SplitN(string(a), ":", 2)
	return "project:show:" + assetTypeFields[0]
}

// PolicyRuleForWrite returns the name of the policy rule that allows write
// access to this resource.
func (a AssetType) PolicyRuleForWrite() string {
	assetTypeFields := strings.SplitN(string(a), ":", 2)
	return "project:edit:" + assetTypeFields[0]
}

// Asset describes a single thing that can be resized dynamically based on its
// utilization. Assets are grouped into resources, see type Resource. Each
// individual resizing is an operation, see type Operation.
type Asset struct {
	// The pair of (.ResourceID, .UUID) uniquely identifies an asset on the
	// API level. Internally, other tables reference Resource by the
	// numeric .ID field.
	//
	// Note that .UUID may be a project/domain UUID for assets that exist exactly
	// once per project/domain, e.g. quota. In that case, .UUID does not uniquely
	// identify an asset unless .ResourceID is also considered.
	ID         int64  `db:"id"`
	ResourceID int64  `db:"resource_id"`
	UUID       string `db:"uuid"`

	// The asset's current size as reported by OpenStack. The meaning of this
	// value is defined by the plugin that implements this asset type.
	Size uint64 `db:"size"`
	// The asset's current utilization, in the same unit as .Size.
	Usage castellum.UsageValues `db:"usage"`

	// The asset's minimum size and maximum size as reported by OpenStack. This
	// should only be filled if the size is limited by technical constraints that
	// are difficult to express in terms of absolute usage (or where merging those
	// hidden constraints into the usage value would cause unnecessary confusion
	// to the end user).
	//
	// This differs from the MinimumSize and MaximumSize fields on type Resource
	// in the level of enforcement: The constraints on type Resource just say that
	// we will not actively move beyond these size boundaries. These limits here,
	// by contrast, are actively enforced: Sizes beyond these boundaries will
	// result in a resize operation to move back into the boundary (hence the
	// qualifier "strict").
	StrictMinimumSize *uint64 `db:"min_size"`
	StrictMaximumSize *uint64 `db:"max_size"`

	// This flag is set by a Castellum worker after a resize operation to indicate
	// that the .Size attribute is outdated. The value is the new_size of the
	// resize operation. We should expect this size to show in the next
	// GetAssetStatus(), but sometimes it doesn't because of information flow
	// delays. That's why we have both .Size and .ExpectedSize: to accurately
	// detect when the resize operation has reflected in the datastore that we're
	// polling for GetAssetStatus().
	ExpectedSize *uint64 `db:"expected_size"`
	// If ExpectedSize is not nil, this is the timestamp when ExpectedSize is filled.
	ResizedAt *time.Time `db:"resized_at"`

	// If the last scrape failed, contains the error message returned by
	// GetAssetStatus(). Contains the empty string otherwise.
	ScrapeErrorMessage string `db:"scrape_error_message"`
	// The next time when new .Size and .Usage values shall be obtained.
	NextScrapeAt time.Time `db:"next_scrape_at"`
	// Contains the duration of the last scrape, or 0 if the asset was never scraped successfully.
	ScrapeDurationSecs float64 `db:"scrape_duration_secs"`
	// Whether we ever scraped this asset successfully. If false, .Size and .Usage
	// will be 0 and those values should not be trusted.
	NeverScraped bool `db:"never_scraped"`

	// A comma-separated list of all UsageMetrics for which this asset has
	// critical usage levels. This field is only generated and never consumed by
	// Castellum. Its intention is to allow operators to inspect the DB and alert
	// on assets that remain on critical usage levels for too long.
	CriticalUsages string `db:"critical_usages"`
}

// PendingOperation describes an ongoing resize operation for an asset.
type PendingOperation struct {
	ID      int64                     `db:"id"`
	AssetID int64                     `db:"asset_id"`
	Reason  castellum.OperationReason `db:"reason"`

	// .OldSize and .Usage mirror the state of the asset when the operation
	// was created, and .NewSize defines the target size.
	OldSize uint64                `db:"old_size"`
	NewSize uint64                `db:"new_size"`
	Usage   castellum.UsageValues `db:"usage"`

	// This sequence of timestamps represent the various states that an operation enters in its lifecycle.

	// When we first saw usage crossing the threshold.
	CreatedAt time.Time `db:"created_at"`
	// When we confirmed that usage had crossed the threshold for the required time. (For .Reason == OperationReasonCritical, this is equal to CreatedAt.)
	ConfirmedAt *time.Time `db:"confirmed_at"`
	// When a user permitted this operation to go ahead. (For operations not
	// subject to operator approval, this is equal to ConfirmedAt.) The value may
	// be in the future when the operator wants to delay the operation until the
	// next maintenance window. The resize will only be executed once .GreenlitAt
	// is non-null and refers to a point in time that is in the past.
	GreenlitAt *time.Time `db:"greenlit_at"`

	// The UUID of the user that greenlit this operation, if any. If GreenlitAt is
	// not null, but this field is null, it means that the operation did not
	// require operator approval.
	GreenlitByUserUUID *string `db:"greenlit_by_user_uuid"`

	// When the resize results in the outcome "errored", we have the option of
	// retrying at a later point in time. This field tracks how many times the
	// operation was put back in the queue after an errored resize.
	ErroredAttempts uint32 `db:"errored_attempts"`
	// When we will attempt the next resize. This field is only filled after an
	// errored resize, i.e. when `op.ErroredAttempts > 0`.
	RetryAt *time.Time `db:"retry_at"`
}

// IntoFinishedOperation creates the FinishedOperation for this PendingOperation.
func (o PendingOperation) IntoFinishedOperation(outcome castellum.OperationOutcome, finishedAt time.Time) FinishedOperation {
	return FinishedOperation{
		AssetID:            o.AssetID,
		Reason:             o.Reason,
		Outcome:            outcome,
		OldSize:            o.OldSize,
		NewSize:            o.NewSize,
		Usage:              o.Usage,
		CreatedAt:          o.CreatedAt,
		ConfirmedAt:        o.ConfirmedAt,
		GreenlitAt:         o.GreenlitAt,
		FinishedAt:         finishedAt,
		GreenlitByUserUUID: o.GreenlitByUserUUID,
		ErroredAttempts:    o.ErroredAttempts,
	}
}

// FinishedOperation describes a finished resize operation for an asset.
type FinishedOperation struct {
	// All fields are identical in semantics to those in type PendingOperation, except
	// where noted.
	AssetID int64                      `db:"asset_id"`
	Reason  castellum.OperationReason  `db:"reason"`
	Outcome castellum.OperationOutcome `db:"outcome"`

	OldSize uint64                `db:"old_size"`
	NewSize uint64                `db:"new_size"`
	Usage   castellum.UsageValues `db:"usage"`

	CreatedAt   time.Time  `db:"created_at"`
	ConfirmedAt *time.Time `db:"confirmed_at"`
	GreenlitAt  *time.Time `db:"greenlit_at"`
	// When the resize operation succeeded, failed, errored, or was cancelled.
	FinishedAt time.Time `db:"finished_at"`

	GreenlitByUserUUID *string `db:"greenlit_by_user_uuid"`
	ErrorMessage       string  `db:"error_message"`
	ErroredAttempts    uint32  `db:"errored_attempts"`
}

// State returns the operation's state as a word.
func (o PendingOperation) State() castellum.OperationState {
	switch {
	case o.ConfirmedAt == nil:
		return castellum.OperationStateCreated
	case o.GreenlitAt == nil:
		return castellum.OperationStateConfirmed
	default:
		return castellum.OperationStateGreenlit
	}
}

// State returns the operation's state as a word.
func (o FinishedOperation) State() castellum.OperationState {
	return castellum.OperationState(o.Outcome)
}

// Init connects to the database and initializes the schema and model types.
func Init(dbURL *url.URL) (*gorp.DbMap, error) {
	cfg := easypg.Configuration{
		PostgresURL: dbURL,
		Migrations:  SQLMigrations,
	}

	dbConn, err := easypg.Connect(cfg)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to database: %w", err)
	}

	// ensure that this process does not starve other Castellum processes for DB connections
	dbConn.SetMaxOpenConns(16)

	gorpDB := &gorp.DbMap{Db: dbConn, Dialect: gorp.PostgresDialect{}}
	gorpDB.AddTableWithName(Resource{}, "resources").SetKeys(true, "id")
	gorpDB.AddTableWithName(Asset{}, "assets").SetKeys(true, "id")
	gorpDB.AddTableWithName(PendingOperation{}, "pending_operations").SetKeys(true, "id")
	gorpDB.AddTableWithName(FinishedOperation{}, "finished_operations")
	return gorpDB, nil
}
