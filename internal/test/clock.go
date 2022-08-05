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

package test

import "time"

// FakeClock is a clock that only changes when we tell it to.
type FakeClock int64

// Now is a double for time.Now().
func (f *FakeClock) Now() time.Time {
	return time.Unix(int64(*f), 0).UTC()
}

// Step advances the clock by one second.
func (f *FakeClock) Step() {
	*f++
}

// StepBy advances the clock by the given duration
func (f *FakeClock) StepBy(d time.Duration) {
	*f += FakeClock(d / time.Second)
}
