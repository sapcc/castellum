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

	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/plugins"
	"github.com/sapcc/castellum/internal/test"
)

func setupContext(t test.T) (*Context, *plugins.AssetManagerStatic, *test.FakeClock) {
	dbi := t.PrepareDB()
	amStatic := &plugins.AssetManagerStatic{
		AssetType: "foo",
	}
	//clock starts at an easily recognizable value
	clockVar := test.FakeClock(99990)
	clock := &clockVar

	return &Context{
		DB:      dbi,
		Team:    core.AssetManagerTeam{amStatic},
		TimeNow: clock.Now,
	}, amStatic, clock
}

//Take pointer to time.Time expression.
func p2time(t time.Time) *time.Time {
	return &t
}

//Take pointer to uint64 expression.
func p2uint64(x uint64) *uint64 {
	return &x
}
