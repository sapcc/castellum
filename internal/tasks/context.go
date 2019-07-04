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

	"github.com/getsentry/sentry-go"
	"github.com/sapcc/castellum/internal/core"
	"gopkg.in/gorp.v2"
)

//Context holds things used by the various task implementations in this
//package.
type Context struct {
	DB   *gorp.DbMap
	Team core.AssetManagerTeam

	//dependency injection slots (usually filled by ApplyDefaults(), but filled
	//with doubles in tests)
	TimeNow func() time.Time

	//When Blocker is not nil, tasks that support concurrent operation will
	//withhold operations until this channel is closed.
	Blocker <-chan struct{}

	//A local Sentry Hub isolated from the global Sentry namespace.
	SentryHub *sentry.Hub
}

//ApplyDefaults injects the regular runtime dependencies into this Context.
func (c *Context) ApplyDefaults() {
	c.TimeNow = time.Now
}

//CloneForNewGoroutine clones the given Context and injects the relevant runtime
//dependencies that must differ for each goroutine.
func (c Context) CloneForNewGoroutine() Context {
	cloned := c
	if sendEventsToSentry {
		// clone the current Sentry Hub from the global namespace and assign it
		//to the cloned Context. This is needed to avoid overwriting Sentry's Scope.
		cloned.SentryHub = sentry.CurrentHub().Clone()
	}
	return cloned
}
