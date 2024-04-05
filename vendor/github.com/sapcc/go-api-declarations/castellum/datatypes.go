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

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
)

// OperationReason is an enumeration type for possible reasons for a resize operation.
type OperationReason string

const (
	// OperationReasonCritical indicates that the resize operation was triggered
	// because the asset's usage exceeded the critical threshold.
	OperationReasonCritical OperationReason = "critical"
	// OperationReasonHigh indicates that the resize operation was triggered
	// because the asset's usage exceeded the high threshold.
	OperationReasonHigh OperationReason = "high"
	// OperationReasonLow indicates that the resize operation was triggered
	// because the asset's usage deceeded the low threshold.
	OperationReasonLow OperationReason = "low"
)

// OperationOutcome is an enumeration type for possible outcomes for a resize operation.
type OperationOutcome string

const (
	// OperationOutcomeSucceeded indicates that a resize operation was completed
	// successfully.
	OperationOutcomeSucceeded OperationOutcome = "succeeded"
	// OperationOutcomeFailed indicates that a resize operation failed because of a problem on the side of the user (e.g. insufficient quota).
	OperationOutcomeFailed OperationOutcome = "failed"
	// OperationOutcomeErrored indicates that a resize operation errored because of a problem in OpenStack.
	OperationOutcomeErrored OperationOutcome = "errored"
	// OperationOutcomeCancelled indicates that a resize operation was cancelled. This happens when usage falls back into normal
	OperationOutcomeCancelled OperationOutcome = "cancelled"
)

// OperationState is an enumeration type for all possible states of an operation.
type OperationState string

const (
	// OperationStateDidNotExist is a bogus state for transitions where there is no
	// previous state.
	OperationStateDidNotExist OperationState = "none"
	// OperationStateCreated is a PendingOperation with ConfirmedAt == nil.
	OperationStateCreated OperationState = "created"
	// OperationStateConfirmed is a PendingOperation with ConfirmedAt != nil && GreenlitAt == nil.
	OperationStateConfirmed OperationState = "confirmed"
	// OperationStateGreenlit is a PendingOperation with ConfirmedAt != nil && GreenlitAt != nil.
	OperationStateGreenlit OperationState = "greenlit"
	// OperationStateCancelled is a FinishedOperation with OperationOutcomeCancelled.
	OperationStateCancelled = OperationState(OperationOutcomeCancelled)
	// OperationStateSucceeded is a FinishedOperation with OperationOutcomeSucceeded.
	OperationStateSucceeded = OperationState(OperationOutcomeSucceeded)
	// OperationStateFailed is a FinishedOperation with OperationOutcomeFailed.
	OperationStateFailed = OperationState(OperationOutcomeFailed)
	// OperationStateErrored is a FinishedOperation with OperationOutcomeErrored.
	OperationStateErrored = OperationState(OperationOutcomeErrored)
)

// UsageMetric identifies a particular usage value for an asset.
type UsageMetric string

// SingularUsageMetric is the UsageMetric value for assets that have only one
// usage metric. For example, project-quota assets only have a single usage
// value reported by Limes, so the only key in type UsageValues will be
// SingularUsageMetric. By contrast, server group assets have two usage values
// (for CPU and RAM usage, respectively), so SingularUsageMetric is not used.
const SingularUsageMetric UsageMetric = "singular"

// UsageValues contains all usage values for an asset at a particular point in time.
type UsageValues map[UsageMetric]float64

// Scan implements the sql.Scanner interface.
func (u *UsageValues) Scan(src any) error {
	var srcBytes []byte
	switch src := src.(type) {
	case string:
		srcBytes = []byte(src)
	case []byte:
		srcBytes = src
	case nil:
		srcBytes = nil
	default:
		return fmt.Errorf("cannot scan value of type %T into type db.UsageValues", src)
	}

	*u = make(UsageValues)
	err := json.Unmarshal(srcBytes, u)
	if err != nil {
		return fmt.Errorf("while parsing UsageValues %q: %w", string(srcBytes), err)
	}
	return nil
}

// Value implements the sql/driver.Valuer interface.
func (u UsageValues) Value() (driver.Value, error) {
	// cast into underlying type to avoid custom MarshalJSON implementation below
	buf, err := json.Marshal(map[UsageMetric]float64(u))
	if err != nil {
		return driver.Value(""), fmt.Errorf("while serializing %#v: %w", u, err)
	}
	return driver.Value(string(buf)), nil
}

// MarshalJSON implements the json.Marshaler interface.
//
// This marshalling is only used in API responses. Serialization into the
// database bypasses it and always marshals a map, even for singular values.
func (u UsageValues) MarshalJSON() ([]byte, error) {
	// for backwards-compatibility, encode `{"singular":x}` as just `x`
	if len(u) == 1 {
		singularVal, exists := u[SingularUsageMetric]
		if exists {
			return json.Marshal(singularVal)
		}
	}

	// otherwise encode like a regular map
	return json.Marshal(map[UsageMetric]float64(u))
}

// UnmarshalJSON implements the json.Unmarshaler interface.
//
// This unmarshalling is only used in API requests. Deserialization from the
// database bypasses it and always requires a map-shaped input, even for singular values.
func (u *UsageValues) UnmarshalJSON(buf []byte) error {
	// for backwards-compatibility, interpret `x` as `{"singular":x}`
	var x float64
	err := json.Unmarshal(buf, &x)
	if err == nil {
		*u = UsageValues{SingularUsageMetric: x}
		return nil
	}

	var m map[UsageMetric]float64
	err = json.Unmarshal(buf, &m)
	if err == nil {
		*u = m
	}
	return nil
}

// IsNonZero returns true if any usage value in this set is not zero.
func (u UsageValues) IsNonZero() bool {
	for _, v := range u {
		if v != 0 {
			return true
		}
	}
	return false
}
