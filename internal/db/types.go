/*******************************************************************************
*
* Copyright 2021 SAP SE
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

package db

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
)

//UsageMetric identifies a particular usage value for an asset.
type UsageMetric string

//SingularUsageMetric is the UsageMetric value for assets that have only one
//usage metric. For example, project-quota assets only have a single usage
//value reported by Limes, so the only key in type UsageValues will be
//SingularUsageMetric. By contrast, server group assets have two usage values
//(for CPU and RAM usage, respectively), so SingularUsageMetric is not used.
const SingularUsageMetric UsageMetric = "singular"

//UsageValues contains all usage values for an asset at a particular point in time.
type UsageValues map[UsageMetric]float64

//Scan implements the sql.Scanner interface.
func (u *UsageValues) Scan(src interface{}) error {
	var srcBytes []byte
	switch src := src.(type) {
	case string:
		srcBytes = []byte(src)
	case []byte:
		srcBytes = src
	case nil:
		srcBytes = nil
	default:
		return fmt.Errorf("cannot scan value of type %T into type db.UsageValues", src)
	}

	*u = make(UsageValues)
	err := json.Unmarshal(srcBytes, u)
	if err != nil {
		return fmt.Errorf("while parsing UsageValues %q: %w", string(srcBytes), err)
	}
	return nil
}

//Value implements the sql/driver.Valuer interface.
func (u UsageValues) Value() (driver.Value, error) {
	//cast into underlying type to avoid custom MarshalJSON implementation below
	buf, err := json.Marshal(map[UsageMetric]float64(u))
	if err != nil {
		return driver.Value(""), fmt.Errorf("while serializing %#v: %w", u, err)
	}
	return driver.Value(string(buf)), nil
}

//MarshalJSON implements the json.Marshaler interface.
//
//This marshalling is only used in API responses. Serialization into the
//database bypasses it and always marshals a map, even for singular values.
func (u UsageValues) MarshalJSON() ([]byte, error) {
	//for backwards-compatibility, encode `{"singular":x}` as just `x`
	if len(u) == 1 {
		singularVal, exists := u[SingularUsageMetric]
		if exists {
			return json.Marshal(singularVal)
		}
	}

	//otherwise encode like a regular map
	return json.Marshal(map[UsageMetric]float64(u))
}
