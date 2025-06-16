// SPDX-FileCopyrightText: 2023 SAP SE
// SPDX-License-Identifier: Apache-2.0

package core

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/sapcc/go-api-declarations/castellum"
	"github.com/sapcc/go-bits/assert"
	"github.com/sapcc/go-bits/logg"
)

func TestGetEligibleOperations(t *testing.T) {
	// define some shorthands for use in this test
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

	// if no threshold is crossed, do not do anything
	check(
		"low=20%, high=80%, crit=95%, step=20%",
		"size=1000, usage=500",
		"", "", // no operations are generated
	)

	// if thresholds are crossed, resizes will be suggested
	check(
		"low=20%, high=80%, crit=95%, step=20%",
		"size=1000, usage=200", // exactly at low
		"low->800", "low->999",
	)
	check(
		"low=20%, high=80%, crit=95%, step=20%",
		"size=1000, usage=160", // clearly below low
		"low->800", "low->799",
	)
	check(
		"low=20%, high=80%, crit=95%, step=20%",
		"size=1000, usage=800", // exactly at high
		"high->1200", "high->1001",
	)
	check(
		"low=20%, high=80%, crit=95%, step=20%",
		"size=1000, usage=840", // clearly above high
		"high->1200", "high->1051",
	)
	check(
		"low=20%, high=80%, crit=95%, step=20%",
		"size=1000, usage=950", // exactly at critical
		"critical->1200",
		// In single-step resizing, critical resize also moves out of the high threshold.
		"critical->1188",
	)
	check(
		"low=20%, high=80%, crit=95%, step=20%",
		"size=1000, usage=990", // clearly above critical
		"critical->1200", "critical->1238",
	)

	// critical resize can take multiple steps at once
	check(
		"crit=95%, step=1%",
		"size=1380, usage=1350",
		// Percentage-step resizing will take four steps at once here (1380 -> 1393 -> 1406 -> 1420 -> 1434).
		//
		// This example is manufactured specifically such that the step size changes
		// between steps, to validate that a new step size is calculated each time,
		// same as if multiple steps had been taken in successive operations.
		//
		//NOTE: This testcase used to require a target size of 1420, but that was wrong.
		// A size of 1420 would lead to 95% usage (or rather, 95.07%) which is still
		// above the critical threshold.
		"critical->1434",
		// Single-step resizing will move just beyond the critical threshold.
		"critical->1422",
	)

	// resize in one direction should not go into a threshold on the opposite side
	check(
		"low=75%, high=80%, crit=95%, step=20%",
		"size=1000, usage=700",
		// Single-step resizing targets just above the low threshold and thus does
		// not come near the high threshold, but percentage-step resizing would
		// (if it ignored the high threshold) go down to size 800 which is too far.
		"low->876", "low->933",
	)
	check(
		"low=90%, crit=95%, step=20%",
		"size=1000, usage=800",
		// Same as above, but this time we're bounded by the critical threshold.
		"low->843", "low->888",
	)
	check(
		"low=20%, high=22%, crit=95%, step=20%",
		"size=1000, usage=230",
		// Same as above, but in the other direction (upsizing instead of downsizing).
		"high->1149", "high->1046",
	)

	// test priority order of thresholds
	//
	// For quota autoscaling, we recommend setting thresholds very close to each
	// other like in these tests. This is usually not a problem for large asset
	// sizes because there is always a size value that satisfies both constraints.
	//
	// However, for really small asset sizes, there can be usage values like
	// below such that there is no size value in the acceptable range of 98%-99%.
	check(
		"low=98%, high=99%, crit=100%, step=1%",
		"size=15, usage=14",
		// Right below the low threshold, no downsize should be generated because
		// that would put us above the high and critical thresholds.
		"", "",
	)
	check(
		"low=98%, high=99%, crit=100%, step=1%",
		"size=14, usage=14",
		// Right above the high and critical threshold, the low threshold must be
		// disregarded. It's better to be slightly too large than slightly too small.
		"critical->15", "critical->15",
	)

	// MinimumSize constraint
	check(
		"low=20%, high=80%, crit=95%, step=20%, min=200",
		"size=1000, usage=100",
		"low->800", "low->499", // not restricted by MinimumSize
	)
	check(
		"low=20%, high=80%, crit=95%, step=20%",
		"size=1000, usage=100, smin=200",
		"low->800", "low->499", // not restricted by StrictMinimumSize
	)
	check(
		"low=20%, high=80%, crit=95%, step=20%, min=1000",
		"size=1000, usage=100",
		"", "", // overridden by MinimumSize
	)
	check(
		"low=20%, high=80%, crit=95%, step=20%",
		"size=1000, usage=100, smin=1000",
		"", "", // overridden by StrictMinimumSize
	)
	check(
		"low=20%, high=80%, crit=95%, step=20%, min=900",
		"size=1000, usage=100",
		"low->900", "low->900", // restricted by MinimumSize
	)
	check(
		"low=20%, high=80%, crit=95%, step=20%",
		"size=1000, usage=100, smin=900",
		"low->900", "low->900", // restricted by StrictMinimumSize
	)
	check(
		"low=20%, high=80%, crit=95%, step=20%, min=1100",
		"size=1000, usage=500",
		"", "", // MinimumSize does not force upsizes (only StrictMinimumSize does)
	)
	check(
		"low=20%, high=80%, crit=95%, step=20%",
		"size=1000, usage=500, smin=1100",
		"high->1200", "high->1100", // forced by StrictMinimumSize
	)

	// MaximumSize constraint
	check(
		"low=20%, high=80%, crit=95%, step=20%, max=2000",
		"size=1000, usage=990",
		"critical->1200", "critical->1238", // not restricted by MaximumSize
	)
	check(
		"low=20%, high=80%, crit=95%, step=20%",
		"size=1000, usage=990, smax=2000",
		"critical->1200", "critical->1238", // not restricted by StrictMaximumSize
	)
	check(
		"low=20%, high=80%, crit=95%, step=20%, max=1000",
		"size=1000, usage=990",
		"", "", // overridden by MaximumSize
	)
	check(
		"low=20%, high=80%, crit=95%, step=20%",
		"size=1000, usage=990, smax=1000",
		"", "", // overridden by StrictMaximumSize
	)
	check(
		"low=20%, high=80%, crit=95%, step=20%, max=1100",
		"size=1000, usage=990",
		"critical->1100", "critical->1100", // restricted by MaximumSize
	)
	check(
		"low=20%, high=80%, crit=95%, step=20%",
		"size=1000, usage=990, smax=1100",
		"critical->1100",
		"critical->1100", // restricted by StrictMaximumSize
	)
	check(
		"low=20%, high=80%, crit=95%, step=20%, max=900",
		"size=1000, usage=500",
		"", "", // MaximumSize does not force downsizes (only StrictMaximumSize does)
	)
	check(
		"low=20%, high=80%, crit=95%, step=20%",
		"size=1000, usage=500, smax=900",
		"low->800", "low->900", // forced by StrictMaximumSize
	)

	// MinimumFreeSize constraint
	check(
		"low=20%, high=80%, crit=95%, step=20%, min_free=600",
		"size=1000, usage=100",
		"low->800", "low->700", // not restricted by MinimumFreeSize
	)
	check(
		"low=20%, high=80%, crit=95%, step=20%, min_free=800",
		"size=1000, usage=100",
		"low->900", "low->900", // restricted by MinimumFreeSize
	)
	check(
		"low=20%, high=80%, crit=95%, step=20%, min_free=800",
		"size=1000, usage=200",
		"", "", // overridden by MinimumFreeSize
	)
	check(
		"low=20%, high=80%, crit=95%, step=20%, min_free=600",
		"size=1000, usage=500",
		"high->1200", "high->1100", // forced by MinimumFreeSize
	)

	// Critical MinimumFreeSize constraint
	// If MinimumFreeSize is marked as critical, forced upsizes should be critical actions.
	// Other behaviour should not be affected regardless whether the flag is set or not.
	check(
		"low=20%, high=80%, crit=95%, step=20%, min_free=300, min_free_is_critical=true",
		"size=1000, usage=100",
		"low->800", "low->499", // not restricted by critical MinimumFreeSize
	)
	check(
		"low=20%, high=80%, crit=95%, step=20%, min_free=300, min_free_is_critical=false",
		"size=1000, usage=100",
		"low->800", "low->499", // not restricted by non-critical MinimumFreeSize
	)
	check(
		"low=20%, high=80%, crit=95%, step=20%, min_free=800, min_free_is_critical=false",
		"size=1000, usage=100",
		"low->900", "low->900", // restricted by non-critical MinimumFreeSize
	)
	check(
		"low=20%, high=80%, crit=95%, step=20%, min_free=800, min_free_is_critical=true",
		"size=1000, usage=100",
		"low->900", "low->900", // restricted by critical MinimumFreeSize
	)
	check(
		"low=20%, high=80%, crit=95%, step=20%, min_free=800, min_free_is_critical=false",
		"size=1000, usage=200",
		"", "", // overridden by non-critical MinimumFreeSize
	)
	check(
		"low=20%, high=80%, crit=95%, step=20%, min_free=800, min_free_is_critical=true",
		"size=1000, usage=200",
		"", "", // overridden by critical MinimumFreeSize
	)
	check(
		"low=20%, high=80%, crit=95%, step=20%, min_free=600, min_free_is_critical=false",
		"size=1000, usage=500",
		"high->1200", "high->1100", // forced by non-critical MinimumFreeSize
	)
	check(
		"low=20%, high=80%, crit=95%, step=20%, min_free=600, min_free_is_critical=true",
		"size=1000, usage=500",
		"critical->1200", "critical->1100", // forced by critical MinimumFreeSize
	)

	// test behavior around zero size and/or zero usage without constraints
	check(
		"low=20%, high=80%, crit=95%, step=20%",
		"size=1, usage=1",
		// This tests that the step is never rounded down to zero.
		"critical->2", "critical->2",
	)
	check(
		"low=20%, high=80%, crit=95%, step=20%",
		"size=1, usage=0",
		// This tests that downsizing to size = 0 is forbidden.
		"", "",
	)
	check(
		"low=20%, high=80%, crit=95%, step=20%",
		// Zero size and usage occurs e.g. in the project-quota asset manager, when the project
		// in question has no quota at all. We expect Castellum to:
		//
		//- leave assets with 0 size and 0 usage alone (and not crash on divide-by-zero while doing so)
		//- never resize assets with non-zero size and 0 usage to zero size
		"size=0, usage=0",
		"", "",
	)
	check(
		"low=20%, high=80%, crit=95%, step=20%",
		"size=0, usage=5",
		// Single-step resizing will end up one higher than percentage-step
		// resizing because it also wants to leave the high threshold.
		"critical->6", "critical->7",
	)

	// test behavior around zero size and/or zero usage with MinimumFreeSize constraint
	check(
		"low=89.9%, crit=90%, min_free=2",
		// This testcase is based on a bug discovered in the wild: Single-step
		// resizing did not generate a pending operation in this case because of the
		// special-cased handling around `usage = 0`.
		"size=5, usage=0",
		"low->4", "low->2",
	)
	check(
		"low=99.9%, crit=100%, min_free=10",
		// Another bug discovered in the wild, this time for `size = 0`.
		"size=0, usage=0",
		"critical->10", "critical->10",
	)

	// test priority order between thresholds and constraints
	check(
		"low=20%, high=80%, crit=95%, step=200%, min_free=2500",
		"size=1000, usage=500",
		// MinimumFreeSize takes precedence over the low threshold: We should upsize
		// the asset to guarantee the MinimumFreeSize, even if this puts us below
		// the low threshold. (For percentage-step resizing, we chose a comically
		// large step size above to ensure that we can see the low threshold being
		// passed.)
		"high->3000", "high->3000",
	)
	check(
		"low=20%, high=80%, crit=95%, step=20%",
		"size=1000, usage=500, smin=3000",
		// StrictMinimumSize takes precedence over the low threshold
		"high->3000", "high->3000",
	)
	check(
		"low=20%, high=80%, crit=95%, step=20%",
		"size=1000, usage=500, smax=500",
		// StrictMaximumSize takes precedence over the high and critical thresholds
		"low->500", "low->500",
	)

	// test conflicts between constraints
	//
	// Entirely conflicting constraints of equal priority shall paralyze Castellum and
	// suppress all actions. Otherwise, the stronger constraint should be enforced.
	// Priority 0: StrictMinimumSize, StrictMaximumSize
	// Priority 1: MinimumFreeSize, MinimumSize, MaximumSize

	check(
		"low=20%, high=80%, crit=95%, step=20%, max=900",
		"size=1000, usage=500, smin=1100",
		"high->1200", "high->1100", // StrictMinimumSize takes precedence over maximum constraint
	)
	check(
		"low=10%, high=80%, crit=95%, step=20%, min_free=550",
		"size=1000, usage=500, smin=1100",
		"high->1200", "high->1100", // StrictMinimumSize takes precedence over minimum free constraint
	)
	check(
		"low=20%, high=80%, crit=95%, step=20%, min_free=200",
		"size=1000, usage=900, smax=1050",
		"high->1050", "high->1050", // StrictMaximumSize takes precedence over minimum free constraint
	)
	check(
		"low=20%, high=80%, crit=95%, step=20%, min_free=200",
		"size=1000, usage=800, smax=900",
		"low->900", "low->900", // StrictMaximumSize enforces downsizing over minimum free constraint
	)
	check(
		"low=20%, high=80%, crit=95%, step=20%, min=1100",
		"size=1000, usage=800, smax=900",
		"low->900", "low->900", // StrictMaximumSize takes precedence over minimum constraint
	)
	check(
		"low=20%, high=80%, crit=95%, step=20%, max=1050, min_free=600",
		"size=1000, usage=500",
		"high->1050", "high->1050", // Upsizing due to MinimumFreeSize should not exceed maximum constraint
	)
	check(
		"low=20%, high=80%, crit=95%, step=20%",
		"size=1000, usage=500, smin=1100, smax=900",
		"", "", // Conflict of strict constrains should result in no action
	)
	check(
		"low=20%, high=80%, crit=95%, step=20%, min=1100, max=900",
		"size=1000, usage=990",
		"", "", // Conflict of non-strict constrains should prevent upsizing
	)
	check(
		"low=20%, high=80%, crit=95%, step=20%, min=1100, max=900",
		"size=1000, usage=100",
		"", "", // Conflict of non-strict constrains should prevent downsizing
	)

	// a specific test that used to fail in prod: one metric's low threshold
	// should not cause inaction when another metric goes into critical
	logg.ShowDebug = true
	resLogic := ResourceLogic{
		UsageMetrics:             []castellum.UsageMetric{"cpu", "ram"},
		LowThresholdPercent:      castellum.UsageValues{"cpu": 25, "ram": 60},
		HighThresholdPercent:     castellum.UsageValues{"cpu": 0, "ram": 0}, // disabled
		CriticalThresholdPercent: castellum.UsageValues{"cpu": 60.5, "ram": 90},
		SizeStepPercent:          20.0,
		SingleStep:               false,
	}
	assetStatus := AssetStatus{
		Size:  4,
		Usage: castellum.UsageValues{"cpu": 1.05, "ram": 3.95},
	}
	assert.DeepEqual(t, "eligible operations with percentage-step resizing",
		eligibleOperationsToString(GetEligibleOperations(resLogic, assetStatus)),
		"critical->5",
	)
}

// Builds a ResourceLogic from a compact string representation like "low=20%, high=80%, step=single, min=200".
func mustParseResourceLogic(t *testing.T, input string) (result ResourceLogic) {
	t.Helper()
	result.UsageMetrics = []castellum.UsageMetric{castellum.SingularUsageMetric}
	for assignment := range strings.SplitSeq(input, ",") {
		assignment = strings.TrimSpace(assignment)
		parts := strings.SplitN(assignment, "=", 2)
		switch parts[0] {
		case "low":
			result.LowThresholdPercent = singular(mustParseFloatPercent(t, parts[1]))
		case "high":
			result.HighThresholdPercent = singular(mustParseFloatPercent(t, parts[1]))
		case "crit":
			result.CriticalThresholdPercent = singular(mustParseFloatPercent(t, parts[1]))
		case "step":
			result.SizeStepPercent = mustParseFloatPercent(t, parts[1])
			result.SingleStep = false
		case "min":
			result.MinimumSize = mustParsePointerToUint64(t, parts[1])
		case "max":
			result.MaximumSize = mustParsePointerToUint64(t, parts[1])
		case "min_free":
			result.MinimumFreeSize = mustParsePointerToUint64(t, parts[1])
		case "min_free_is_critical":
			result.MinimumFreeIsCritical = mustParseBool(t, parts[1])
		default:
			panic("unknown field in ResourceLogic string: " + parts[0])
		}
	}
	return result
}

// Builds an AssetStatus from a compact string representation like "size=1000, usage=500, smin=1100".
func mustParseAssetStatus(t *testing.T, input string) (result AssetStatus) {
	t.Helper()
	for assignment := range strings.SplitSeq(input, ",") {
		assignment = strings.TrimSpace(assignment)
		parts := strings.SplitN(assignment, "=", 2)
		switch parts[0] {
		case "size":
			result.Size = mustParseUint64(t, parts[1])
		case "usage":
			result.Usage = singular(mustParseFloat(t, parts[1]))
		case "smin":
			result.StrictMinimumSize = mustParsePointerToUint64(t, parts[1])
		case "smax":
			result.StrictMaximumSize = mustParsePointerToUint64(t, parts[1])
		default:
			panic("unknown field in AssetStatus string: " + parts[0])
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

func mustParseBool(t *testing.T, input string) bool {
	t.Helper()
	val, err := strconv.ParseBool(input)
	if err != nil {
		t.Fatal(err)
	}
	return val
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

func singular(x float64) castellum.UsageValues {
	return castellum.UsageValues{castellum.SingularUsageMetric: x}
}
