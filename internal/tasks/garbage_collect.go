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

package tasks

import (
	"time"

	"gopkg.in/gorp.v2"
)

//CollectGarbage removes old entries from the finished_operations table.
func CollectGarbage(dbi *gorp.DbMap, maxLastUpdatedAt time.Time) error {
	_, err := dbi.Exec(`DELETE FROM finished_operations WHERE finished_at < $1`, maxLastUpdatedAt)
	return err
}
