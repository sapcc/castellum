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
	"sort"

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
func ApplyResourceSpecInto(res *db.Resource, spec castellum.Resource, existingResources []db.AssetType, cfg *Config, team AssetManagerTeam) (errs errext.ErrorSet) {
	manager, info := team.ForAssetType(res.AssetType)
	if manager == nil {
		errs.Addf("unsupported asset type")
		return
	}

	if spec.ConfigJSON == nil {
		res.ConfigJSON = ""
	} else {
		res.ConfigJSON = string(*spec.ConfigJSON)
	}
	errs.Add(manager.CheckResourceAllowed(res.AssetType, res.ScopeUUID, res.ConfigJSON, existingResources))

	if spec.Checked != nil {
		errs.Addf("resource.checked cannot be set via the API")
	}
	if spec.AssetCount != 0 {
		errs.Addf("resource.asset_count cannot be set via the API")
	}

	errs.Append(applyThresholdSpecsInto(res, spec, info))
	errs.Append(checkIntraThresholdConsistency(res, spec, info))
	errs.Append(applySteppingSpecInto(res, spec))
	errs.Append(applySizeConstraintsSpecInto(res, spec, cfg.MaxAssetSizeFor(res.AssetType)))
	return
}

func applyThresholdSpecsInto(res *db.Resource, spec castellum.Resource, info AssetTypeInfo) (errs errext.ErrorSet) {
	if spec.LowThreshold == nil {
		res.LowThresholdPercent = info.MakeZeroUsageValues()
		res.LowDelaySeconds = 0
	} else {
		res.LowThresholdPercent = spec.LowThreshold.UsagePercent
		errs.Append(checkThresholdCommon(info, "low", res.LowThresholdPercent))
		res.LowDelaySeconds = spec.LowThreshold.DelaySeconds
		if res.LowDelaySeconds == 0 {
			errs.Addf("delay for low threshold is missing")
		}
	}

	if spec.HighThreshold == nil {
		res.HighThresholdPercent = info.MakeZeroUsageValues()
		res.HighDelaySeconds = 0
	} else {
		res.HighThresholdPercent = spec.HighThreshold.UsagePercent
		errs.Append(checkThresholdCommon(info, "high", res.HighThresholdPercent))
		res.HighDelaySeconds = spec.HighThreshold.DelaySeconds
		if res.HighDelaySeconds == 0 {
			errs.Addf("delay for high threshold is missing")
		}
	}

	if spec.CriticalThreshold == nil {
		res.CriticalThresholdPercent = info.MakeZeroUsageValues()
	} else {
		res.CriticalThresholdPercent = spec.CriticalThreshold.UsagePercent
		errs.Append(checkThresholdCommon(info, "critical", res.CriticalThresholdPercent))
		if spec.CriticalThreshold.DelaySeconds != 0 {
			errs.Addf("critical threshold may not have a delay")
		}
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
	sort.Strings(providedMetrics) //for deterministic order of error messages in unit test
	for _, metric := range providedMetrics {
		if !isMetric[castellum.UsageMetric(metric)] {
			errs.Addf("%s threshold specified for metric %q which is not valid for this asset type", tType, metric)
		}
	}

	return
}

//nolint:gocognit // This function is just above the limit at cognit = 33, but factoring out the repetitive part is unreasonably complicated.
func checkIntraThresholdConsistency(res *db.Resource, spec castellum.Resource, info AssetTypeInfo) (errs errext.ErrorSet) {
	if spec.LowThreshold != nil && spec.HighThreshold != nil {
		for _, metric := range info.UsageMetrics {
			if res.LowThresholdPercent[metric] > res.HighThresholdPercent[metric] {
				errs.Addf("low threshold%s must be below high threshold", Identifier(metric, " for %s"))
			}
		}
	}
	if spec.LowThreshold != nil && spec.CriticalThreshold != nil {
		for _, metric := range info.UsageMetrics {
			if res.LowThresholdPercent[metric] > res.CriticalThresholdPercent[metric] {
				errs.Addf("low threshold%s must be below critical threshold", Identifier(metric, " for %s"))
			}
		}
	}
	if spec.HighThreshold != nil && spec.CriticalThreshold != nil {
		for _, metric := range info.UsageMetrics {
			if res.HighThresholdPercent[metric] > res.CriticalThresholdPercent[metric] {
				errs.Addf("high threshold%s must be below critical threshold", Identifier(metric, " for %s"))
			}
		}
	}

	if spec.LowThreshold == nil && spec.HighThreshold == nil && spec.CriticalThreshold == nil {
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

func applySizeConstraintsSpecInto(res *db.Resource, spec castellum.Resource, maxAssetSize *uint64) (errs errext.ErrorSet) {
	if spec.SizeConstraints == nil {
		if maxAssetSize != nil {
			errs.Addf("maximum size must be configured for %s", res.AssetType)
		}
		res.MinimumSize = nil
		res.MaximumSize = nil
		res.MinimumFreeSize = nil
	} else {
		res.MinimumSize = spec.SizeConstraints.Minimum
		if res.MinimumSize != nil && *res.MinimumSize == 0 {
			res.MinimumSize = nil
		}

		res.MaximumSize = spec.SizeConstraints.Maximum
		if res.MaximumSize == nil {
			if maxAssetSize != nil {
				errs.Addf("maximum size must be configured for %s", res.AssetType)
			}
		} else {
			min := uint64(0)
			if res.MinimumSize != nil {
				min = *res.MinimumSize
			}
			if *res.MaximumSize <= min {
				errs.Addf("maximum size must be greater than minimum size")
			}
			if maxAssetSize != nil && *res.MaximumSize > *maxAssetSize {
				errs.Addf("maximum size must be %d or less", *maxAssetSize)
			}
		}

		res.MinimumFreeSize = spec.SizeConstraints.MinimumFree
		if res.MinimumFreeSize != nil && *res.MinimumFreeSize == 0 {
			res.MinimumFreeSize = nil
		}
	}

	return
}
