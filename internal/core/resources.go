// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package core

import (
	"context"
	"encoding/json"
	"sort"

	. "github.com/majewsky/gg/option"
	"github.com/sapcc/go-api-declarations/castellum"
	"github.com/sapcc/go-bits/errext"

	"github.com/sapcc/castellum/internal/db"
)

// ApplyResourceSpecInto validates the configuration in the given resource
// specification (which can either come from the API or from a seed file) and
// applies it in-place into the given db.Resource record.
//
// The `existingResources` argument shall contain all resources currently
// existing in the DB with the same scope UUID (including `res` itself, if it
// refers to a pre-existing resource).
//
// For new resources, a fresh `res` shall be given that shall only be filled
// with an AssetType and ScopeUUID.
func ApplyResourceSpecInto(ctx context.Context, res *db.Resource, spec castellum.Resource, existingResources map[db.AssetType]struct{}, cfg Config, team AssetManagerTeam) (errs errext.ErrorSet) {
	manager, info := team.ForAssetType(res.AssetType)
	if manager == nil {
		errs.Addf("unsupported asset type")
		return
	}

	res.ConfigJSON = string(spec.ConfigJSON.UnwrapOr(json.RawMessage("")))
	errs.Add(manager.CheckResourceAllowed(ctx, res.AssetType, res.ScopeUUID, res.ConfigJSON, existingResources))

	if spec.Checked.IsSome() {
		errs.Addf("resource.checked cannot be set via the API")
	}
	if spec.AssetCount != 0 {
		errs.Addf("resource.asset_count cannot be set via the API")
	}

	errs.Append(applyThresholdSpecsInto(res, spec, info))
	errs.Append(checkIntraThresholdConsistency(res, spec, info))
	errs.Append(applySteppingSpecInto(res, spec))
	errs.Append(applySizeConstraintsSpecInto(res, spec, cfg.MaxAssetSizeFor(res.AssetType, res.ScopeUUID)))
	return
}

func applyThresholdSpecsInto(res *db.Resource, spec castellum.Resource, info AssetTypeInfo) (errs errext.ErrorSet) {
	if threshold, ok := spec.LowThreshold.Unpack(); ok {
		res.LowThresholdPercent = threshold.UsagePercent
		errs.Append(checkThresholdCommon(info, "low", res.LowThresholdPercent))
		res.LowDelaySeconds = threshold.DelaySeconds
		if res.LowDelaySeconds == 0 {
			errs.Addf("delay for low threshold is missing")
		}
	} else {
		res.LowThresholdPercent = info.MakeZeroUsageValues()
		res.LowDelaySeconds = 0
	}

	if threshold, ok := spec.HighThreshold.Unpack(); ok {
		res.HighThresholdPercent = threshold.UsagePercent
		errs.Append(checkThresholdCommon(info, "high", res.HighThresholdPercent))
		res.HighDelaySeconds = threshold.DelaySeconds
		if res.HighDelaySeconds == 0 {
			errs.Addf("delay for high threshold is missing")
		}
	} else {
		res.HighThresholdPercent = info.MakeZeroUsageValues()
		res.HighDelaySeconds = 0
	}

	if threshold, ok := spec.CriticalThreshold.Unpack(); ok {
		res.CriticalThresholdPercent = threshold.UsagePercent
		errs.Append(checkThresholdCommon(info, "critical", res.CriticalThresholdPercent))
		if threshold.DelaySeconds != 0 {
			errs.Addf("critical threshold may not have a delay")
		}
	} else {
		res.CriticalThresholdPercent = info.MakeZeroUsageValues()
	}

	return
}

// helper function to check the internal consistency of {Low,High,Critical}ThresholdPercent
func checkThresholdCommon(info AssetTypeInfo, tType string, vals castellum.UsageValues) (errs errext.ErrorSet) {
	isMetric := make(map[castellum.UsageMetric]bool)
	for _, metric := range info.UsageMetrics {
		isMetric[metric] = true
		val, exists := vals[metric]
		if !exists {
			errs.Addf("missing %s threshold%s", tType, Identifier(metric, " for %s"))
			continue
		}
		if val <= 0 || val > 100 {
			errs.Addf("%s threshold%s must be above 0%% and below or at 100%% of usage", tType, Identifier(metric, " for %s"))
		}
	}

	providedMetrics := make([]string, 0, len(vals))
	for metric := range vals {
		providedMetrics = append(providedMetrics, string(metric))
	}
	sort.Strings(providedMetrics) // for deterministic order of error messages in unit test
	for _, metric := range providedMetrics {
		if !isMetric[castellum.UsageMetric(metric)] {
			errs.Addf("%s threshold specified for metric %q which is not valid for this asset type", tType, metric)
		}
	}

	return
}

//nolint:gocognit // This function is just above the limit at cognit = 33, but factoring out the repetitive part is unreasonably complicated.
func checkIntraThresholdConsistency(res *db.Resource, spec castellum.Resource, info AssetTypeInfo) (errs errext.ErrorSet) {
	if spec.LowThreshold.IsSome() && spec.HighThreshold.IsSome() {
		for _, metric := range info.UsageMetrics {
			if res.LowThresholdPercent[metric] > res.HighThresholdPercent[metric] {
				errs.Addf("low threshold%s must be below high threshold", Identifier(metric, " for %s"))
			}
		}
	}
	if spec.LowThreshold.IsSome() && spec.CriticalThreshold.IsSome() {
		for _, metric := range info.UsageMetrics {
			if res.LowThresholdPercent[metric] > res.CriticalThresholdPercent[metric] {
				errs.Addf("low threshold%s must be below critical threshold", Identifier(metric, " for %s"))
			}
		}
	}
	if spec.HighThreshold.IsSome() && spec.CriticalThreshold.IsSome() {
		for _, metric := range info.UsageMetrics {
			if res.HighThresholdPercent[metric] > res.CriticalThresholdPercent[metric] {
				errs.Addf("high threshold%s must be below critical threshold", Identifier(metric, " for %s"))
			}
		}
	}

	if spec.LowThreshold.IsNone() && spec.HighThreshold.IsNone() && spec.CriticalThreshold.IsNone() {
		errs.Addf("at least one threshold must be configured")
	}

	return
}

func applySteppingSpecInto(res *db.Resource, spec castellum.Resource) (errs errext.ErrorSet) {
	res.SizeStepPercent = spec.SizeSteps.Percent
	res.SingleStep = spec.SizeSteps.Single
	if res.SingleStep {
		if res.SizeStepPercent != 0 {
			errs.Addf("percentage-based step may not be configured when single-step resizing is used")
		}
	} else {
		if res.SizeStepPercent == 0 {
			errs.Addf("size step must be greater than 0%%")
		}
	}

	return
}

func applySizeConstraintsSpecInto(res *db.Resource, spec castellum.Resource, maxAssetSize Option[uint64]) (errs errext.ErrorSet) {
	sc := spec.SizeConstraints.UnwrapOr(castellum.SizeConstraints{})

	res.MinimumSize = sc.Minimum
	if res.MinimumSize == Some[uint64](0) {
		res.MinimumSize = None[uint64]()
	}

	res.MaximumSize = sc.Maximum
	if maxSize, ok := res.MaximumSize.Unpack(); ok {
		if maxSize <= res.MinimumSize.UnwrapOr(0) {
			errs.Addf("maximum size must be greater than minimum size")
		}
		if limit, ok := maxAssetSize.Unpack(); ok && maxSize > limit {
			errs.Addf("maximum size must be %d or less", limit)
		}
	} else if maxAssetSize.IsSome() {
		errs.Addf("maximum size must be configured for %s", res.AssetType)
	}

	res.MinimumFreeSize = sc.MinimumFree
	if res.MinimumFreeSize == Some[uint64](0) {
		res.MinimumFreeSize = None[uint64]()
	}

	res.MinimumFreeIsCritical = sc.MinimumFreeIsCritical
	if res.MinimumFreeIsCritical && res.MinimumFreeSize.IsNone() {
		errs.Addf("threshold for minimum free space must be configured")
	}

	return
}
