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
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/majewsky/sqlproxy"
	"github.com/sapcc/go-bits/easypg"
	"github.com/sapcc/go-bits/logg"
	"gopkg.in/gorp.v2"
)

//Resource describes the autoscaling behavior for a single resource in a
//single project or domain. Note that we reuse Limes terminology here: A
//project resource is the totality of all assets (see type Asset) of a single
//type within a project. For example, a single NFS share is not a resource,
//it's an asset. But it *belongs* to the resource "NFS shares", and more
//specifically, to the project resource "NFS shares for project X".
type Resource struct {
	//The pair of (.ScopeUUID, .AssetType) uniquely identifies a Resource on
	//the API level. Internally, other tables reference Resource by the numeric
	//.ID field.
	ID         int64     `db:"id"`
	ScopeUUID  string    `db:"scope_uuid"`  //either project UUID or domain UUID
	DomainUUID string    `db:"domain_uuid"` //for domain resources: equal to .ScopeUUID
	AssetType  AssetType `db:"asset_type"`

	//When we last checked this Resource for new or deleted assets.
	ScrapedAt *time.Time `db:"scraped_at"`

	//Assets will resize when they have crossed a certain threshold for a certain
	//time. Those thresholds (in percent of usage) and delays (in seconds) are
	//defined here. The "critical" threshold will cause immediate upscaling, so
	//it does not have a configurable delay.
	LowThresholdPercent      float64 `db:"low_threshold_percent"`
	LowDelaySeconds          uint32  `db:"low_delay_seconds"`
	HighThresholdPercent     float64 `db:"high_threshold_percent"`
	HighDelaySeconds         uint32  `db:"high_delay_seconds"`
	CriticalThresholdPercent float64 `db:"critical_threshold_percent"`

	//This defines how much the the asset's size changes per
	//downscaling/upscaling operation (in % of previous size). This can be NULL
	//when the asset type defines size steps differently. For example, for the
	//asset type "instance", we will have a list of allowed flavors somewhere else.
	SizeStepPercent float64 `db:"size_step_percent"`
	//When true, ignore SizeStepPercent and always resize by the smallest step
	//that will move usage back into normal areas.
	SingleStep bool `db:"single_step"`

	//This defines absolute boundaries for the asset size. If configured, resize
	//operations will never move to a size outside this range.
	MinimumSize *uint64 `db:"min_size"`
	MaximumSize *uint64 `db:"max_size"`
	//If configured, downsize operations will be inhibited when `newSize -
	//absoluteUsage` would be smaller than this.
	MinimumFreeSize *uint64 `db:"min_free_size"`
}

//AssetType is the type of Resource.AssetType. It extends type string with some
//convenience methods.
type AssetType string

//PolicyRuleForRead returns the name of the policy rule that allows read access
//to this resource.
func (a AssetType) PolicyRuleForRead() string {
	//only consider the asset type up to the first colon, e.g.
	//  assetType = "quota:compute:instances"
	//  -> result = "project:show:quota"
	assetTypeFields := strings.SplitN(string(a), ":", 2)
	return "project:show:" + assetTypeFields[0]
}

//PolicyRuleForWrite returns the name of the policy rule that allows write
//access to this resource.
func (a AssetType) PolicyRuleForWrite() string {
	assetTypeFields := strings.SplitN(string(a), ":", 2)
	return "project:edit:" + assetTypeFields[0]
}

//Asset describes a single thing that can be resized dynamically based on its
//utilization. Assets are grouped into resources, see type Resource. Each
//individual resizing is an operation, see type Operation.
type Asset struct {
	//The pair of (.ResourceID, .UUID) uniquely identifies an asset on the
	//API level. Internally, other tables reference Resource by the
	//numeric .ID field.
	//
	//Note that .UUID may be a project/domain UUID for assets that exist exactly
	//once per project/domain, e.g. quota. In that case, .UUID does not uniquely
	//identify an asset unless .ResourceID is also considered.
	ID         int64  `db:"id"`
	ResourceID int64  `db:"resource_id"`
	UUID       string `db:"uuid"`

	//The asset's current size as reported by OpenStack. The meaning of this
	//value is defined by the plugin that implements this asset type.
	Size uint64 `db:"size"`
	//The asset's current utilization as a percentage of its size. This must
	//always be between 0 and 100.
	UsagePercent float64 `db:"usage_percent"`
	//The asset's current utilization, in the same unit as .Size. This is only
	//set when the asset manager reports absolute usages.
	AbsoluteUsage *uint64 `db:"absolute_usage"`

	//When we last tried to obtain the current .Size and .UsagePercent values.
	CheckedAt time.Time `db:"checked_at"`
	//When the current .Size and .UsagePercent values were obtained.
	ScrapedAt *time.Time `db:"scraped_at"`
	//This flag is set by a Castellum worker after a resize operation to indicate
	//that the .Size attribute is outdated. The value is the new_size of the
	//resize operation. We should expect this size to show in the next
	//GetAssetStatus(), but sometimes it doesn't because of information flow
	//delays. That's why we have both .Size and .ExpectedSize: to accurately
	//detect when the resize operation has reflected in the datastore that we're
	//polling for GetAssetStatus().
	ExpectedSize *uint64 `db:"expected_size"`

	//If the last scrape failed, contains the error message returned by
	//GetAssetStatus(). Contains the empty string otherwise.
	ScrapeErrorMessage string `db:"scrape_error_message"`
}

//PendingOperation describes an ongoing resize operation for an asset.
type PendingOperation struct {
	ID      int64           `db:"id"`
	AssetID int64           `db:"asset_id"`
	Reason  OperationReason `db:"reason"`

	//.OldSize and .UsagePercent mirror the state of the asset when the operation
	//was created, and .NewSize defines the target size.
	OldSize      uint64  `db:"old_size"`
	NewSize      uint64  `db:"new_size"`
	UsagePercent float64 `db:"usage_percent"`

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

//IntoFinishedOperation creates the FinishedOperation for this PendingOperation.
func (o PendingOperation) IntoFinishedOperation(outcome OperationOutcome, finishedAt time.Time) FinishedOperation {
	return FinishedOperation{
		AssetID:            o.AssetID,
		Reason:             o.Reason,
		Outcome:            outcome,
		OldSize:            o.OldSize,
		NewSize:            o.NewSize,
		UsagePercent:       o.UsagePercent,
		CreatedAt:          o.CreatedAt,
		ConfirmedAt:        o.ConfirmedAt,
		GreenlitAt:         o.GreenlitAt,
		FinishedAt:         finishedAt,
		GreenlitByUserUUID: o.GreenlitByUserUUID,
	}
}

//FinishedOperation describes a finished resize operation for an asset.
type FinishedOperation struct {
	//All fields are identical in semantics to those in type PendingOperation, except
	//where noted.
	AssetID int64            `db:"asset_id"`
	Reason  OperationReason  `db:"reason"`
	Outcome OperationOutcome `db:"outcome"`

	OldSize      uint64  `db:"old_size"`
	NewSize      uint64  `db:"new_size"`
	UsagePercent float64 `db:"usage_percent"`

	CreatedAt   time.Time  `db:"created_at"`
	ConfirmedAt *time.Time `db:"confirmed_at"`
	GreenlitAt  *time.Time `db:"greenlit_at"`
	//When the resize operation succeeded, failed, errored, or was cancelled.
	FinishedAt time.Time `db:"finished_at"`

	GreenlitByUserUUID *string `db:"greenlit_by_user_uuid"`
	ErrorMessage       string  `db:"error_message"`
}

//OperationReason is an enumeration type for possible reasons for a resize operation.
type OperationReason string

const (
	//OperationReasonCritical indicates that the resize operation was triggered
	//because the asset's usage exceeded the critical threshold.
	OperationReasonCritical OperationReason = "critical"
	//OperationReasonHigh indicates that the resize operation was triggered
	//because the asset's usage exceeded the high threshold.
	OperationReasonHigh OperationReason = "high"
	//OperationReasonLow indicates that the resize operation was triggered
	//because the asset's usage deceeded the low threshold.
	OperationReasonLow OperationReason = "low"
)

//OperationOutcome is an enumeration type for possible outcomes for a resize operation.
type OperationOutcome string

const (
	//OperationOutcomeSucceeded indicates that a resize operation was completed
	//successfully.
	OperationOutcomeSucceeded OperationOutcome = "succeeded"
	//OperationOutcomeFailed indicates that a resize operation failed because of a problem on the side of the user (e.g. insufficient quota).
	OperationOutcomeFailed OperationOutcome = "failed"
	//OperationOutcomeErrored indicates that a resize operation errored because of a problem in OpenStack.
	OperationOutcomeErrored OperationOutcome = "errored"
	//OperationOutcomeCancelled indicates that a resize operation was cancelled. This happens when usage falls back into normal
	OperationOutcomeCancelled OperationOutcome = "cancelled"
)

//OperationState is an enumeration type for all possible states of an operation.
type OperationState string

const (
	//OperationStateDidNotExist is a bogus state for transitions where there is no
	//previous state.
	OperationStateDidNotExist OperationState = "none"
	//OperationStateCreated is a PendingOperation with ConfirmedAt == nil.
	OperationStateCreated OperationState = "created"
	//OperationStateConfirmed is a PendingOperation with ConfirmedAt != nil && GreenlitAt == nil.
	OperationStateConfirmed OperationState = "confirmed"
	//OperationStateGreenlit is a PendingOperation with ConfirmedAt != nil && GreenlitAt != nil.
	OperationStateGreenlit OperationState = "greenlit"
	//OperationStateCancelled is a FinishedOperation with OperationOutcomeCancelled.
	OperationStateCancelled = OperationState(OperationOutcomeCancelled)
	//OperationStateSucceeded is a FinishedOperation with OperationOutcomeSucceeded.
	OperationStateSucceeded = OperationState(OperationOutcomeSucceeded)
	//OperationStateFailed is a FinishedOperation with OperationOutcomeFailed.
	OperationStateFailed = OperationState(OperationOutcomeFailed)
	//OperationStateErrored is a FinishedOperation with OperationOutcomeErrored.
	OperationStateErrored = OperationState(OperationOutcomeErrored)
)

//State returns the operation's state as a word.
func (o PendingOperation) State() OperationState {
	switch {
	case o.ConfirmedAt == nil:
		return OperationStateCreated
	case o.GreenlitAt == nil:
		return OperationStateConfirmed
	default:
		return OperationStateGreenlit
	}
}

//State returns the operation's state as a word.
func (o FinishedOperation) State() OperationState {
	return OperationState(o.Outcome)
}

func init() {
	logger := func(msg string) {
		logg.Debug(msg)
	}
	sql.Register("postgres-with-logging", &sqlproxy.Driver{
		ProxiedDriverName: "postgres",
		BeforeQueryHook:   sqlproxy.TraceQuery(logger),
	})
}

//Init connects to the database and initializes the schema and model types.
func Init(urlStr string) (*gorp.DbMap, error) {
	dbURL, err := url.Parse(urlStr)
	if err != nil {
		return nil, fmt.Errorf("malformed CASTELLUM_DB_URI: " + err.Error())
	}

	cfg := easypg.Configuration{
		PostgresURL: dbURL,
		Migrations:  SQLMigrations,
	}
	if logStatements, _ := strconv.ParseBool(os.Getenv("CASTELLUM_DEBUG_SQL")); logStatements {
		cfg.OverrideDriverName = "postgres-with-logging"
	}

	dbConn, err := easypg.Connect(cfg)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to database: " + err.Error())
	}

	gorpDB := &gorp.DbMap{Db: dbConn, Dialect: gorp.PostgresDialect{}}
	gorpDB.AddTableWithName(Resource{}, "resources").SetKeys(true, "id")
	gorpDB.AddTableWithName(Asset{}, "assets").SetKeys(true, "id")
	gorpDB.AddTableWithName(PendingOperation{}, "pending_operations").SetKeys(true, "id")
	gorpDB.AddTableWithName(FinishedOperation{}, "finished_operations")
	return gorpDB, nil
}
