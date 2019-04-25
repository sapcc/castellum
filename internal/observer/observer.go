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

package observer

import (
	"time"

	"github.com/sapcc/castellum/internal/core"
	"gopkg.in/gorp.v2"
)

//Observer holds things used by the various worker implementations in this
//package.
type Observer struct {
	DB   *gorp.DbMap
	Team core.AssetManagerTeam

	//dependency injection slots (usually filled by ApplyDefaults(), but filled
	//with doubles in tests)
	TimeNow func() time.Time
}

//ApplyDefaults injects the regular runtime dependencies into this Observer.
func (o *Observer) ApplyDefaults() {
	o.TimeNow = time.Now
}
