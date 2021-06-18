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
	"testing"

	"github.com/sapcc/go-bits/assert"
)

func TestUsageValuesEncodingDecoding(t *testing.T) {
	testCases := []struct {
		UsageValues  UsageValues
		SQLEncoding  string
		JSONEncoding string
	}{
		{
			UsageValues:  UsageValues{SingularUsageMetric: 42.1},
			SQLEncoding:  `{"singular":42.1}`,
			JSONEncoding: `42.1`,
		},
		{
			UsageValues:  UsageValues{"foo": 0},
			SQLEncoding:  `{"foo":0}`,
			JSONEncoding: `{"foo":0}`,
		},
		{
			UsageValues:  UsageValues{"foo": 0, "bar": 23},
			SQLEncoding:  `{"bar":23,"foo":0}`,
			JSONEncoding: `{"bar":23,"foo":0}`,
		},
		{
			UsageValues:  UsageValues{"foo": 0, SingularUsageMetric: 42.1},
			SQLEncoding:  `{"foo":0,"singular":42.1}`,
			JSONEncoding: `{"foo":0,"singular":42.1}`,
		},
	}

	for idx, tc := range testCases {
		indexed := func(task string) string {
			return fmt.Sprintf("%s no. %d/%d", task, idx+1, len(testCases))
		}

		//check encoding into SQL
		actualSQLEncoding, err := tc.UsageValues.Value()
		if err == nil {
			assert.DeepEqual(t, indexed("SQLEncoding"), actualSQLEncoding, driver.Value(tc.SQLEncoding))
		} else {
			t.Errorf("SQL encoding of %#v failed: %v", tc.UsageValues, err.Error())
		}

		//check decoding from SQL
		var actualDecoded UsageValues
		err = actualDecoded.Scan(tc.SQLEncoding)
		if err == nil {
			assert.DeepEqual(t, indexed("SQLDecoded"), actualDecoded, tc.UsageValues)
		} else {
			t.Errorf("SQL decoding of %q failed: %v", tc.SQLEncoding, err.Error())
		}

		//check encoding into JSON
		actualJSONEncoding, err := json.Marshal(tc.UsageValues)
		if err == nil {
			assert.DeepEqual(t, indexed("JSONEncoding"), string(actualJSONEncoding), tc.JSONEncoding)
		} else {
			t.Errorf("JSON encoding of %#v failed: %v", tc.UsageValues, err.Error())
		}

		//check decoding from JSON
		actualDecoded = UsageValues{}
		err = json.Unmarshal([]byte(tc.JSONEncoding), &actualDecoded)
		if err == nil {
			assert.DeepEqual(t, indexed("JSONDecoded"), actualDecoded, tc.UsageValues)
		} else {
			t.Errorf("JSON decoding of %q failed: %v", tc.JSONEncoding, err.Error())
		}
	}
}
