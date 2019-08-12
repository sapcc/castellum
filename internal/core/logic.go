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
	"math"
	"sort"

	"github.com/sapcc/castellum/internal/db"
)

//GetEligibleOperations calculates which resizing operations the given asset
//(within the given resource) is eligible for. In the result, each key-value
//pair means that the asset has crossed the threshold `key` and thus should be
//resized to `value`.
func GetEligibleOperations(res db.Resource, asset db.Asset) map[db.OperationReason]uint64 {
	result := make(map[db.OperationReason]uint64)
	if val := checkReason(res, asset, db.OperationReasonLow); val != nil {
		result[db.OperationReasonLow] = *val
	}
	if val := checkReason(res, asset, db.OperationReasonHigh); val != nil {
		result[db.OperationReasonHigh] = *val
	}
	if val := checkReason(res, asset, db.OperationReasonCritical); val != nil {
		result[db.OperationReasonCritical] = *val
	}
	return result
}

func checkReason(res db.Resource, asset db.Asset, reason db.OperationReason) *uint64 {
	//phase 1: generate global constraints
	//
	//NOTE: We only add MinimumSize as a constraint for downsizing. For upsizing,
	//it's okay if the target is below MinimumSize. It just means we're inching
	//closer *towards* the happy area. (And vice versa for MaximumSize.)
	c := emptyConstraints()
	if reason == db.OperationReasonLow && res.MinimumSize != nil {
		c.forbidBelow(*res.MinimumSize)
	}
	if reason != db.OperationReasonLow && res.MaximumSize != nil {
		c.forbidAbove(*res.MaximumSize)
	}

	//MinimumFreeSize is a constraint, but can also cause action, so it
	//technically falls in both phase 1 and phase 2
	var a actions
	takeActionBecauseMinimumFreeSize := false
	if res.MinimumFreeSize != nil && asset.AbsoluteUsage != nil {
		minSize := *res.MinimumFreeSize + *asset.AbsoluteUsage
		switch reason {
		case db.OperationReasonLow:
			c.forbidBelow(minSize)
		case db.OperationReasonHigh:
			if asset.Size < minSize {
				a.AddAction(action{Min: minSize, Desired: minSize}, *c)
				//We also let the rest of this method behave as if the `high` threshold
				//was crossed. The percentage-step resizing may generate a larger
				//target size than this action right now did, in which case it will
				//override this action.
				takeActionBecauseMinimumFreeSize = true
			}
		}
	}

	//phase 2: generate an action when the corresponding threshold is passed
	takeActionBecauseThreshold := false
	switch reason {
	case db.OperationReasonLow:
		takeActionBecauseThreshold = res.LowThresholdPercent > 0 && asset.UsagePercent <= res.LowThresholdPercent
	case db.OperationReasonHigh:
		takeActionBecauseThreshold = res.HighThresholdPercent > 0 && asset.UsagePercent >= res.HighThresholdPercent
	case db.OperationReasonCritical:
		takeActionBecauseThreshold = res.CriticalThresholdPercent > 0 && asset.UsagePercent >= res.CriticalThresholdPercent
	}
	if takeActionBecauseThreshold || takeActionBecauseMinimumFreeSize {
		if res.SingleStep {
			a.AddAction(getActionSingleStep(res, asset, reason), *c)
		} else {
			a.AddAction(getActionPercentageStep(res, asset, reason), *c)
		}
	}

	//phase 3: take the boldest action that satifies the constraints
	if reason == db.OperationReasonLow {
		return a.Min()
	}
	return a.Max()
}

func getActionPercentageStep(res db.Resource, asset db.Asset, reason db.OperationReason) action {
	newSize := getNewSizePercentageStep(res, asset, reason, asset.Size)
	if reason == db.OperationReasonLow {
		return action{Min: newSize, Desired: newSize, Max: asset.Size}
	}
	return action{Min: asset.Size, Desired: newSize, Max: newSize}
}

func getNewSizePercentageStep(res db.Resource, asset db.Asset, reason db.OperationReason, assetSize uint64) uint64 {
	step := uint64(math.Floor((float64(assetSize) * res.SizeStepPercent) / 100))
	//a small fraction of a small value (e.g. 10% of size = 6) may round down to zero
	if step == 0 {
		step = 1
	}

	switch reason {
	case db.OperationReasonCritical:
		newSize := assetSize + step
		//for assets reporting absolute usage, we can estimate the new usage-%
		//immediately and take multiple steps if usage would still be crossing the
		//critical threshold otherwise
		if asset.AbsoluteUsage != nil {
			newUsagePercent := 100 * float64(*asset.AbsoluteUsage) / float64(newSize)
			if newUsagePercent >= res.CriticalThresholdPercent {
				//restart call with newSize as old size to calculate the next step
				return getNewSizePercentageStep(res, asset, reason, newSize)
			}
		}
		return newSize
	case db.OperationReasonHigh:
		return assetSize + step
	case db.OperationReasonLow:
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

func getActionSingleStep(res db.Resource, asset db.Asset, reason db.OperationReason) action {
	//NOTE: Single-step resizing is only allowed for resources that report
	//absolute usage, so we are going to assum that asset.AbsoluteUsage != nil.

	var (
		thresholdPerc float64
		delta         float64
	)
	switch reason {
	case db.OperationReasonCritical:
		//A "critical" resize should also leave the "high" threshold if there is
		//one. Otherwise we would have to do a "high" resize directly afterwards
		//which contradicts the whole "single-step" business.
		thresholdPerc = res.CriticalThresholdPercent
		if res.HighThresholdPercent > 0 {
			thresholdPerc = res.HighThresholdPercent
		}
		delta = -0.0001
	case db.OperationReasonHigh:
		thresholdPerc = res.HighThresholdPercent
		delta = -0.0001
	case db.OperationReasonLow:
		thresholdPerc = res.LowThresholdPercent
		delta = +0.0001
	default:
		panic("unreachable")
	}

	//the new size should be close to the threshold, but with a small delta to
	//avoid hitting the threshold exactly
	newSizeFloat := 100 * float64(*asset.AbsoluteUsage) / (thresholdPerc + delta)
	if reason == db.OperationReasonLow {
		//for "low", round size down to ensure usage-% comes out above the threshold
		newSizeRounded := math.Floor(newSizeFloat)
		//make sure that we don't resize to or below 0
		if newSizeRounded < 1.5 {
			return action{Max: 1, Desired: asset.Size}
		}
		newSize := uint64(newSizeRounded)
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
