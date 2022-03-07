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

	"github.com/sapcc/castellum/internal/core"
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
}

//ApplyDefaults injects the regular runtime dependencies into this Context.
func (c *Context) ApplyDefaults() {
	c.TimeNow = time.Now
}

//JobPoller is a function, usually a member function of type Context, that can
//be called repeatedly to obtain Job instances.
//
//If there are no jobs to work on right now, sql.ErrNoRows shall be returned
//to signal to the caller to slow down the polling.
type JobPoller func() (Job, error)

//Job is a job that can be transferred to a worker goroutine to be executed
//there.
type Job interface {
	Execute() error
}

//ExecuteOne is used by unit tests to find and execute exactly one instance of
//the given type of Job. sql.ErrNoRows is returned when there are no jobs of
//that type waiting.
func ExecuteOne(p JobPoller) error {
	j, err := p()
	if err != nil {
		return err
	}
	return j.Execute()
}
