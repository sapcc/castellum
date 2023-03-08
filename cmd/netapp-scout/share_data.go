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

package main

import (
	"fmt"
	"math"
)

// ShareData contains all metric values belonging to a single share.
type ShareData map[string]float64

var allMetricNames = []string{
	"netapp_volume_is_space_reporting_logical",
	"netapp_volume_logical_used_bytes",
	"netapp_volume_percentage_snapshot_reserve",
	"netapp_volume_snapshot_reserved_bytes",
	"netapp_volume_snapshot_used_bytes",
	"netapp_volume_total_bytes",
	"netapp_volume_used_bytes",
}

func (d ShareData) pick(metricName string) (float64, error) {
	val, ok := d[metricName]
	if !ok {
		return 0, fmt.Errorf("no data for metric: %s", metricName)
	}
	return val, nil
}

//NOTE on the size/usage calculation
//
//Option 1: For old shares, we have 5% snapshot reserve that gets allocated
//*AS PART OF* the target share size, so we need to count the snapshot
//reserve into the size and the snapshot usage into the usage, i.e.
//
//    cond  = netapp_volume_is_space_reporting_logical == 0 && netapp_volume_percentage_snapshot_reserve == 5
//    size  = netapp_volume_total_bytes + netapp_volume_snapshot_reserved_bytes
//    usage = netapp_volume_used_bytes  + max(netapp_volume_snapshot_reserved_bytes, netapp_volume_snapshot_used_bytes)
//
//Option 2: For newer shares, we have a much larger snapshot reserve (usually
//50%) that gets allocated *IN ADDITION TO* the target share size, and
//therefore snapshot usage usually does not eat into the main share size, i.e.
//
//    cond  = netapp_volume_is_space_reporting_logical == 0 && netapp_volume_percentage_snapshot_reserve > 5
//    size  = netapp_volume_total_bytes
//    usage = netapp_volume_used_bytes
//
//Option 3: Same as option 2, but if logical space reporting is used, we need
//to look at a different metric.
//
//    cond  = netapp_volume_is_space_reporting_logical == 1
//    size  = netapp_volume_total_bytes
//    usage = netapp_volume_logical_used_bytes
//
//TODO Remove option 1 once all shares have migrated to the new layout.

// GetSizeBytes computes the size of this share in bytes. If some required
// metrics are missing, an error is returned.
func (d ShareData) GetSizeBytes() (float64, error) {
	//option 3
	isSpaceReportingLogical, err := d.pick("netapp_volume_is_space_reporting_logical")
	if err != nil {
		return 0, err
	}
	if isSpaceReportingLogical == 1.0 {
		return d.pick("netapp_volume_total_bytes")
	}

	//option 2
	snapshotReservePercent, err := d.pick("netapp_volume_percentage_snapshot_reserve")
	if err != nil {
		return 0, err
	}
	if snapshotReservePercent != 5.0 {
		return d.pick("netapp_volume_total_bytes")
	}

	//option 1
	bytesTotal, err := d.pick("netapp_volume_total_bytes")
	if err != nil {
		return 0, err
	}
	bytesReservedBySnapshots, err := d.pick("netapp_volume_snapshot_reserved_bytes")
	if err != nil {
		return 0, err
	}
	return bytesTotal + bytesReservedBySnapshots, nil
}

// GetUsageBytes computes the usage of this share in bytes. If some required
// metrics are missing, an error is returned.
func (d ShareData) GetUsageBytes() (float64, error) {
	//option 3
	isSpaceReportingLogical, err := d.pick("netapp_volume_is_space_reporting_logical")
	if err != nil {
		return 0, err
	}
	if isSpaceReportingLogical == 1.0 {
		return d.pick("netapp_volume_logical_used_bytes")
	}

	//option 2
	snapshotReservePercent, err := d.pick("netapp_volume_percentage_snapshot_reserve")
	if err != nil {
		return 0, err
	}
	if snapshotReservePercent != 5.0 {
		return d.pick("netapp_volume_used_bytes")
	}

	//option 1
	bytesUsed, err := d.pick("netapp_volume_used_bytes")
	if err != nil {
		return 0, err
	}
	bytesReservedBySnapshots, err := d.pick("netapp_volume_snapshot_reserved_bytes")
	if err != nil {
		return 0, err
	}
	bytesUsedBySnapshots, err := d.pick("netapp_volume_snapshot_used_bytes")
	if err != nil {
		return 0, err
	}
	return bytesUsed + math.Max(bytesUsedBySnapshots, bytesReservedBySnapshots), nil
}
