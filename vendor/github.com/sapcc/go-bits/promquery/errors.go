/*******************************************************************************
*
* Copyright 2022 SAP SE
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

package promquery

import "fmt"

// NoRowsError is returned by PrometheusClient.GetSingleValue()
// if there were no result values at all.
type NoRowsError struct {
	Query string
}

// Error implements the builtin/error interface.
func (e NoRowsError) Error() string {
	return fmt.Sprintf("Prometheus query returned empty result: %s", e.Query)
}

// IsErrNoRows checks whether the given error is a NoRowsError.
func IsErrNoRows(err error) bool {
	if err == nil {
		return false
	}
	_, ok := err.(NoRowsError)
	return ok
}