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

package core

import (
	"fmt"
	"math"
	"sort"

	"github.com/sapcc/go-api-declarations/castellum"

	"github.com/sapcc/castellum/internal/db"
)

// GetUsagePercent calculates `100 * usage / size`, but has additional logic for
// some corner cases like size = 0.
func GetUsagePercent(size uint64, usage float64) float64 {
	if size == 0 {
		if usage == 0 {
			return 0
		}
		//This value is a somewhat arbitrary choice, but it should be above 100%
		//because `usage > size`.
		return 200
	}

	return 100 * usage / float64(size)
}

// GetMultiUsagePercent is like GetUsagePercent, but converts multiple usage values at once.
func GetMultiUsagePercent(size uint64, usage castellum.UsageValues) castellum.UsageValues {
	result := make(castellum.UsageValues, len(usage))
	for k, v := range usage {
		result[k] = GetUsagePercent(size, v)
	}
	return result
}

// ResourceLogic contains all the attributes from `db.Resource` that pertain to
// the calculation of resize actions for assets within a given resource.
//
// It does not include anything that is not needed for that calculation, in
// order to avoid accidental dependencies on things like timing information,
// UUIDs or error message strings.
type ResourceLogic struct {
	UsageMetrics             []castellum.UsageMetric
	LowThresholdPercent      castellum.UsageValues
	HighThresholdPercent     castellum.UsageValues
	CriticalThresholdPercent castellum.UsageValues

	SizeStepPercent float64
	SingleStep      bool

	MinimumSize     *uint64
	MaximumSize     *uint64
	MinimumFreeSize *uint64
}

// LogicOfResource converts a Resource into just its ResourceLogic.
func LogicOfResource(res db.Resource, info AssetTypeInfo) ResourceLogic {
	if res.AssetType != info.AssetType {
		panic(fmt.Sprintf(
			"LogicOfResource called with mismatching arguments: res.AssetType = %q, but info.AssetType = %q",
			res.AssetType, info.AssetType,
		))
	}
	return ResourceLogic{
		UsageMetrics:             info.UsageMetrics,
		LowThresholdPercent:      res.LowThresholdPercent,
		HighThresholdPercent:     res.HighThresholdPercent,
		CriticalThresholdPercent: res.CriticalThresholdPercent,
		SizeStepPercent:          res.SizeStepPercent,
		SingleStep:               res.SingleStep,
		MinimumSize:              res.MinimumSize,
		MaximumSize:              res.MaximumSize,
		MinimumFreeSize:          res.MinimumFreeSize,
	}
}

// GetEligibleOperations calculates which resizing operations the given asset
// (within the given resource) is eligible for. In the result, each key-value
// pair means that the asset has crossed the threshold `key` and thus should be
// resized to `value`.
func GetEligibleOperations(res ResourceLogic, asset AssetStatus) map[castellum.OperationReason]uint64 {
	//never touch a zero-sized asset unless it has non-zero usage
	if asset.Size == 0 && !asset.Usage.IsNonZero() {
		//UNLESS we need to force it larger because of configuration
		if (res.MinimumSize == nil || *res.MinimumSize == 0) && (res.MinimumFreeSize == nil || *res.MinimumFreeSize == 0) {
			return nil
		}
	}

	result := make(map[castellum.OperationReason]uint64)
	if val := checkReason(res, asset, castellum.OperationReasonLow); val != nil {
		result[castellum.OperationReasonLow] = *val
	}
	if val := checkReason(res, asset, castellum.OperationReasonCritical); val != nil {
		result[castellum.OperationReasonCritical] = *val
	} else if val := checkReason(res, asset, castellum.OperationReasonHigh); val != nil {
		result[castellum.OperationReasonHigh] = *val
	}
	return result
}

func checkReason(res ResourceLogic, asset AssetStatus, reason castellum.OperationReason) *uint64 {
	//phase 1: generate global constraints
	//
	//NOTE: We only add MinimumSize as a constraint for downsizing. For upsizing,
	//it's okay if the target is below MinimumSize. It just means we're inching
	//closer *towards* the happy area. (And vice versa for MaximumSize.)
	c := emptyConstraints()
	if reason == castellum.OperationReasonLow && res.MinimumSize != nil {
		c.forbidBelow(*res.MinimumSize)
	}
	if reason != castellum.OperationReasonLow && res.MaximumSize != nil {
		c.forbidAbove(*res.MaximumSize)
	}

	//we also support asset-local MinimumSize and MaximumSize values for
	//technical constraints that are difficult to express in terms of raw usage
	//numbers
	if reason == castellum.OperationReasonLow && asset.MinimumSize != nil {
		c.forbidBelow(*asset.MinimumSize)
	}
	if reason != castellum.OperationReasonLow && asset.MaximumSize != nil {
		c.forbidAbove(*asset.MaximumSize)
	}

	//do not allow downsize operations to cross above the high/critical thresholds
	if reason == castellum.OperationReasonLow {
		for _, metric := range res.UsageMetrics {
			if res.HighThresholdPercent[metric] != 0 {
				c.forbidBelow(uint64(math.Floor(100*asset.Usage[metric]/res.HighThresholdPercent[metric])) + 1)
			}
			if res.CriticalThresholdPercent[metric] != 0 {
				c.forbidBelow(uint64(math.Floor(100*asset.Usage[metric]/res.CriticalThresholdPercent[metric])) + 1)
			}
		}
	}

	//do not allow upsize operations to cross below the low threshold
	if reason != castellum.OperationReasonLow {
		for _, metric := range res.UsageMetrics {
			if res.LowThresholdPercent[metric] != 0 {
				lowSize := uint64(math.Floor(100*asset.Usage[metric]/res.LowThresholdPercent[metric])) - 1

				//BUT ensure that this threshold does not prevent us from taking action
				//at all (if in doubt, the high or critical threshold is more important
				//than the low threshold; it's better to have an asset slightly too large
				//than slightly too small)
				highThresholdPerc := res.HighThresholdPercent[metric]
				if highThresholdPerc == 0 {
					highThresholdPerc = res.CriticalThresholdPercent[metric]
				}
				if highThresholdPerc != 0 {
					for lowSize > 0 && (100*asset.Usage[metric]/float64(lowSize)) >= highThresholdPerc {
						lowSize++
					}
				}

				//ALSO the MinimumFreeSize takes precedence over the low threshold: we're
				//allowed to go into low usage if it helps us satisfy the MinimumFreeSize
				if res.MinimumFreeSize != nil {
					minSize := *res.MinimumFreeSize + uint64(math.Ceil(asset.Usage[metric]))
					if lowSize < minSize {
						lowSize = minSize
					}
				}

				if lowSize > 0 {
					c.forbidAbove(lowSize)
				}
			}
		}
	}

	//MinimumFreeSize is a constraint, but can also cause action, so it
	//technically falls in both phase 1 and phase 2
	var a actions
	takeActionBecauseMinimumFreeSize := false
	if res.MinimumFreeSize != nil {
		for _, metric := range res.UsageMetrics {
			minSize := *res.MinimumFreeSize + uint64(math.Ceil(asset.Usage[metric]))
			//This handling for MinimumSize and MinimumFreeSize is preferably done on
			//the "high" threshold, but if no "high" threshold is configured, it will
			//be done on the "critical" threshold instead.
			ensureMinSize := func() {
				if asset.Size < minSize {
					a.AddAction(action{Min: minSize, Desired: minSize}, *c)
					//We also let the rest of this method behave as if the `high` threshold
					//was crossed. The percentage-step resizing may generate a larger
					//target size than this action right now did, in which case it will
					//override this action.
					takeActionBecauseMinimumFreeSize = true
				}
			}

			switch reason {
			case castellum.OperationReasonLow:
				c.forbidBelow(minSize)
			case castellum.OperationReasonCritical:
				if res.HighThresholdPercent[metric] == 0 {
					ensureMinSize()
				}
			case castellum.OperationReasonHigh:
				ensureMinSize()
			}
		}
	}

	//phase 2: generate an action when the corresponding threshold is passed
	takeActionBecauseThreshold := false
	for _, metric := range res.UsageMetrics {
		usagePercent := GetUsagePercent(asset.Size, asset.Usage[metric])
		switch reason {
		case castellum.OperationReasonLow:
			if res.LowThresholdPercent[metric] > 0 && usagePercent <= res.LowThresholdPercent[metric] {
				takeActionBecauseThreshold = true
			}
		case castellum.OperationReasonHigh:
			if res.HighThresholdPercent[metric] > 0 && usagePercent >= res.HighThresholdPercent[metric] {
				takeActionBecauseThreshold = true
			}
		case castellum.OperationReasonCritical:
			if res.CriticalThresholdPercent[metric] > 0 && usagePercent >= res.CriticalThresholdPercent[metric] {
				takeActionBecauseThreshold = true
			}
		}
	}
	if takeActionBecauseThreshold || takeActionBecauseMinimumFreeSize {
		if res.SingleStep {
			for _, metric := range res.UsageMetrics {
				a.AddAction(getActionSingleStep(res, asset, metric, reason), *c)
			}
		} else {
			a.AddAction(getActionPercentageStep(res, asset, reason), *c)
		}
	}

	//phase 3: take the boldest action that satisfies the constraints
	var target *uint64
	if reason == castellum.OperationReasonLow {
		target = a.Min()
	} else {
		target = a.Max()
	}

	//...but only if it's actually a resize
	if target != nil && *target == asset.Size {
		return nil
	}
	return target
}

func getActionPercentageStep(res ResourceLogic, asset AssetStatus, reason castellum.OperationReason) action {
	newSize := getNewSizePercentageStep(res, asset, reason, asset.Size)
	if reason == castellum.OperationReasonLow {
		return action{Min: newSize, Desired: newSize, Max: asset.Size}
	}
	return action{Min: asset.Size, Desired: newSize, Max: newSize}
}

func getNewSizePercentageStep(res ResourceLogic, asset AssetStatus, reason castellum.OperationReason, assetSize uint64) uint64 {
	step := uint64(math.Floor((float64(assetSize) * res.SizeStepPercent) / 100))
	//a small fraction of a small value (e.g. 10% of size = 6) may round down to zero
	if step == 0 {
		step = 1
	}

	switch reason {
	case castellum.OperationReasonCritical:
		newSize := assetSize + step
		//take multiple steps if usage continues to cross the critical threshold
		for _, metric := range res.UsageMetrics {
			newUsagePercent := GetUsagePercent(newSize, asset.Usage[metric])
			if newUsagePercent >= res.CriticalThresholdPercent[metric] {
				//restart call with newSize as old size to calculate the next step
				return getNewSizePercentageStep(res, asset, reason, newSize)
			}
		}
		return newSize
	case castellum.OperationReasonHigh:
		return assetSize + step
	case castellum.OperationReasonLow:
		//when going down, we have to take care not to end up with zero
		if assetSize < 1+step {
			//^ This condition is equal to `assetSize - step < 1`, but cannot overflow below 0.
			return 1
		}
		return assetSize - step
	default:
		panic("unexpected reason: " + string(reason))
	}
}

func getActionSingleStep(res ResourceLogic, asset AssetStatus, metric castellum.UsageMetric, reason castellum.OperationReason) action {
	var (
		thresholdPerc float64
		delta         float64
	)
	switch reason {
	case castellum.OperationReasonCritical:
		//A "critical" resize should also leave the "high" threshold if there is
		//one. Otherwise we would have to do a "high" resize directly afterwards
		//which contradicts the whole "single-step" business.
		thresholdPerc = res.CriticalThresholdPercent[metric]
		if res.HighThresholdPercent[metric] > 0 {
			thresholdPerc = res.HighThresholdPercent[metric]
		}
		delta = -0.0001
	case castellum.OperationReasonHigh:
		thresholdPerc = res.HighThresholdPercent[metric]
		delta = -0.0001
	case castellum.OperationReasonLow:
		thresholdPerc = res.LowThresholdPercent[metric]
		delta = +0.0001
	default:
		panic("unreachable")
	}

	//the new size should be close to the threshold, but with a small delta to
	//avoid hitting the threshold exactly
	newSizeFloat := 100 * asset.Usage[metric] / (thresholdPerc + delta)
	if reason == castellum.OperationReasonLow {
		//for "low", round size down to ensure usage-% comes out above the threshold
		newSize := uint64(math.Floor(newSizeFloat))
		return action{Desired: newSize, Max: asset.Size}
	}
	//for "high"/"critical", round size up to ensure usage-% comes out below the threshold
	newSize := uint64(math.Ceil(newSizeFloat))
	return action{Desired: newSize, Min: asset.Size}
}

////////////////////////////////////////////////////////////////////////////////
// type constraints

type constraints struct {
	Min uint64
	Max uint64
}

func emptyConstraints() *constraints {
	//Min starts at 1 because we never want to resize to 0
	return &constraints{1, math.MaxUint64}
}

func (c *constraints) forbidBelow(val uint64) {
	if c.Min < val {
		c.Min = val
	}
}

func (c *constraints) forbidAbove(val uint64) {
	if c.Max > val {
		c.Max = val
	}
}

func (c *constraints) isSatisfiable() bool {
	return c.Min <= c.Max
}

////////////////////////////////////////////////////////////////////////////////
// type action(s)

type action struct {
	Min     uint64
	Max     uint64 //can be 0 to signify absence
	Desired uint64
}

type actions []uint64

func (as *actions) AddAction(a action, c constraints) {
	if a.Min != 0 {
		c.forbidBelow(a.Min)
	}
	if a.Max != 0 {
		c.forbidAbove(a.Max)
	}
	if !c.isSatisfiable() {
		return
	}

	val := a.Desired
	if val < c.Min {
		val = c.Min
	}
	if val > c.Max {
		val = c.Max
	}
	*as = append(*as, val)
}

func (as actions) Min() *uint64 {
	if len(as) == 0 {
		return nil
	}
	sort.Slice(as, func(i, j int) bool { return as[i] < as[j] })
	val := as[0]
	return &val
}

func (as actions) Max() *uint64 {
	if len(as) == 0 {
		return nil
	}
	sort.Slice(as, func(i, j int) bool { return as[i] < as[j] })
	val := as[len(as)-1]
	return &val
}
