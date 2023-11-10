/*******************************************************************************
*
* Copyright 2023 SAP SE
*
* Licensed under the Apache License, Version 2.0 (the "License");
* you may not use this file except in compliance with the License.
* You should have received a copy of the License along with this
* program. If not, you may obtain a copy of the License at
*
*     http://www.apache.org/licenses/LICENSE-2.0
*
* Unless required by applicable law or agreed to in writing, software
* distributed under the License is distributed on an "AS IS" BASIS,
* WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
* See the License for the specific language governing permissions and
* limitations under the License.
*
*******************************************************************************/

package core

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/sapcc/go-api-declarations/castellum"
	"github.com/sapcc/go-bits/assert"
)

func TestGetEligibleOperations(t *testing.T) {
	//define some shorthands for use in this test
	check := func(resLogicStr string, assetStatusStr string, expectedWithPercentageStep string, expectedWithSingleStep string) {
		t.Helper()
		resLogic := mustParseResourceLogic(t, resLogicStr)
		assetStatus := mustParseAssetStatus(t, assetStatusStr)
		assert.DeepEqual(t, "eligible operations with percentage-step resizing",
			eligibleOperationsToString(GetEligibleOperations(resLogic, assetStatus)),
			expectedWithPercentageStep,
		)

		resLogic.SizeStepPercent = 0
		resLogic.SingleStep = true
		assert.DeepEqual(t, "eligible operations with single-step resizing",
			eligibleOperationsToString(GetEligibleOperations(resLogic, assetStatus)),
			expectedWithSingleStep,
		)
	}

	//if no threshold is crossed, do not do anything
	check(
		"low=20%, high=80%, crit=95%, step=20%",
		"size=1000, usage=500",
		"", //no operations are generated for percentage-step resizing
		"", //no operations are generated for single-step resizing
	)

	//if thresholds are crossed, resizes will be suggested
	check(
		"low=20%, high=80%, crit=95%, step=20%",
		"size=1000, usage=200", //exactly at low
		"low->800",
		"low->999",
	)
	check(
		"low=20%, high=80%, crit=95%, step=20%",
		"size=1000, usage=160", //clearly below low
		"low->800",
		"low->799",
	)
	check(
		"low=20%, high=80%, crit=95%, step=20%",
		"size=1000, usage=800", //exactly at high
		"high->1200",
		"high->1001",
	)
	check(
		"low=20%, high=80%, crit=95%, step=20%",
		"size=1000, usage=840", //clearly above high
		"high->1200",
		"high->1051",
	)
	check(
		"low=20%, high=80%, crit=95%, step=20%",
		"size=1000, usage=950", //exactly at critical
		"critical->1200, high->1200",
		//In single-step resizing, both have the same target value! If we do a critical resize, we will also have it move out of the high threshold.
		"critical->1188, high->1188",
	)
	check(
		"low=20%, high=80%, crit=95%, step=20%",
		"size=1000, usage=990", //clearly above critical
		"critical->1200, high->1200",
		"critical->1238, high->1238",
	)

	//TODO: move testcases here from internal/tasks/asset_scrape_test.go, starting from line 488 downwards
}

// Builds a ResourceLogic from a compact string representation like "low=20%, high=80%, step=single, min=200".
func mustParseResourceLogic(t *testing.T, input string) (result ResourceLogic) {
	t.Helper()
	result.UsageMetrics = []castellum.UsageMetric{castellum.SingularUsageMetric}
	for _, assignment := range strings.Split(input, ",") {
		assignment = strings.TrimSpace(assignment)
		parts := strings.SplitN(assignment, "=", 2)
		switch parts[0] {
		case "low":
			result.LowThresholdPercent = one(mustParseFloatPercent(t, parts[1]))
		case "high":
			result.HighThresholdPercent = one(mustParseFloatPercent(t, parts[1]))
		case "crit":
			result.CriticalThresholdPercent = one(mustParseFloatPercent(t, parts[1]))
		case "step":
			result.SizeStepPercent = mustParseFloatPercent(t, parts[1])
			result.SingleStep = false
		case "min":
			result.MinimumSize = mustParsePointerToUint64(t, parts[1])
		case "max":
			result.MaximumSize = mustParsePointerToUint64(t, parts[1])
		case "min_free":
			result.MinimumFreeSize = mustParsePointerToUint64(t, parts[1])
		default:
			panic("unknown field in ResourceLogic string: " + parts[0])
		}
	}
	return result
}

// Builds an AssetStatus from a compact string representation like "size=1000, usage=500, min=1100".
func mustParseAssetStatus(t *testing.T, input string) (result AssetStatus) {
	t.Helper()
	for _, assignment := range strings.Split(input, ",") {
		assignment = strings.TrimSpace(assignment)
		parts := strings.SplitN(assignment, "=", 2)
		switch parts[0] {
		case "size":
			result.Size = mustParseUint64(t, parts[1])
		case "usage":
			result.Usage = one(mustParseFloat(t, parts[1]))
		case "min":
			result.MinimumSize = mustParsePointerToUint64(t, parts[1])
		case "max":
			result.MaximumSize = mustParsePointerToUint64(t, parts[1])
		}
	}
	return result
}

// Renders the result type of GetEligibleOperations into a compact string representation like "low->1500, high->2000".
func eligibleOperationsToString(m map[castellum.OperationReason]uint64) string {
	var keys []castellum.OperationReason
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var fields []string
	for _, k := range keys {
		fields = append(fields, fmt.Sprintf("%s->%d", k, m[k]))
	}
	return strings.Join(fields, ", ")
}

func mustParseFloat(t *testing.T, input string) float64 {
	t.Helper()
	val, err := strconv.ParseFloat(input, 64)
	if err != nil {
		t.Fatal(err)
	}
	return val
}

func mustParseFloatPercent(t *testing.T, input string) float64 {
	t.Helper()
	input, ok := strings.CutSuffix(input, "%")
	if !ok {
		t.Fatalf("no percent sign at end of float percent value: %q", input)
	}
	return mustParseFloat(t, input)
}

func mustParseUint64(t *testing.T, input string) uint64 {
	t.Helper()
	val, err := strconv.ParseUint(input, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return val
}

func mustParsePointerToUint64(t *testing.T, input string) *uint64 {
	t.Helper()
	val := mustParseUint64(t, input)
	return &val
}

func one(x float64) castellum.UsageValues {
	return castellum.UsageValues{castellum.SingularUsageMetric: x}
}
