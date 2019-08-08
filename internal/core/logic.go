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

	"github.com/sapcc/castellum/internal/db"
)

//GetMatchingReasons returns a map that indicates for which resizing operations
//the given asset (within the given resource) is eligible.
func GetMatchingReasons(res db.Resource, asset db.Asset) map[db.OperationReason]bool {
	result := make(map[db.OperationReason]bool)
	if res.LowThresholdPercent > 0 && asset.UsagePercent <= res.LowThresholdPercent {
		if canDownsize(res, asset) {
			result[db.OperationReasonLow] = true
		}
	}
	if res.HighThresholdPercent > 0 && asset.UsagePercent >= res.HighThresholdPercent {
		if canUpsize(res, asset, db.OperationReasonHigh) {
			result[db.OperationReasonHigh] = true
		}
	}
	if res.CriticalThresholdPercent > 0 && asset.UsagePercent >= res.CriticalThresholdPercent {
		if canUpsize(res, asset, db.OperationReasonCritical) {
			result[db.OperationReasonCritical] = true
		}
	}

	//even if the high threshold is not surpassed, we still want to upsize when it is necessary to ensure res.MinimumFreeSize
	if res.MinimumFreeSize != nil && asset.AbsoluteUsage != nil {
		freeSize := asset.Size - *asset.AbsoluteUsage
		if asset.Size < *asset.AbsoluteUsage {
			//avoid overflow below 0
			freeSize = 0
		}
		if freeSize < *res.MinimumFreeSize {
			result[db.OperationReasonHigh] = true
		}
	}

	return result
}

func canDownsize(res db.Resource, asset db.Asset) bool {
	if res.MinimumSize != nil {
		if GetNewSize(res, asset, db.OperationReasonLow) < *res.MinimumSize {
			return false
		}
	}
	if res.MinimumFreeSize != nil && asset.AbsoluteUsage != nil {
		newSize := GetNewSize(res, asset, db.OperationReasonLow)
		if newSize < *asset.AbsoluteUsage+*res.MinimumFreeSize {
			//^ This condition is equal to `newSize - absUsage < minFreeSize`, but
			//cannot overflow below 0.
			return false
		}
	}
	return true
}

func canUpsize(res db.Resource, asset db.Asset, reason db.OperationReason) bool {
	if res.MaximumSize == nil {
		return true
	}
	return GetNewSize(res, asset, reason) <= *res.MaximumSize
}

//GetNewSize returns the target size for this asset (within this resource)
//after resizing for the given reason.
func GetNewSize(res db.Resource, asset db.Asset, reason db.OperationReason) uint64 {
	if res.SingleStep {
		return getNewSizeSingleStep(res, asset, reason)
	}
	return getNewSize(res, asset, reason, asset.Size)
}

func getNewSize(res db.Resource, asset db.Asset, reason db.OperationReason, assetSize uint64) uint64 {
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
				return getNewSize(res, asset, reason, newSize)
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

func getNewSizeSingleStep(res db.Resource, asset db.Asset, reason db.OperationReason) uint64 {
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
			return 1
		}
		return uint64(newSizeRounded)
	}
	//for "high"/"critical", round size up to ensure usage-% comes out below the threshold
	return uint64(math.Ceil(newSizeFloat))
}
