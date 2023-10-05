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
)

// ShareData contains all metric values belonging to a single share.
type ShareData map[string]float64

var allMetricNames = []string{
	"manila_share_minimal_size_bytes_for_castellum",
	"manila_share_size_bytes_for_castellum",
	"manila_share_used_bytes_for_castellum",
}

func (d ShareData) pick(metricName string) (float64, error) {
	val, ok := d[metricName]
	if !ok {
		return 0, fmt.Errorf("no data for metric: %s", metricName)
	}
	return val, nil
}

// GetSizeBytes computes the size of this share in bytes.
// If some required metrics are missing, an error is returned.
func (d ShareData) GetSizeBytes() (float64, error) {
	return d.pick("manila_share_size_bytes_for_castellum")
}

// GetMinimumSizeBytes computes the minimum size of this share in bytes.
// If some required metrics are missing, an error is returned.
func (d ShareData) GetMinimumSizeBytes() (float64, error) {
	return d.pick("manila_share_minimal_size_bytes_for_castellum")
}

// GetUsageBytes computes the usage of this share in bytes.
// If some required metrics are missing, an error is returned.
func (d ShareData) GetUsageBytes() (float64, error) {
	return d.pick("manila_share_used_bytes_for_castellum")
}
