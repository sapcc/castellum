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

import (
	"testing"

	"gopkg.in/gorp.v2"
)

// T extends testing.T with custom helper methods.
type T struct {
	*testing.T
}

// Must fails the test if the given error is non-nil.
func (t T) Must(err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err.Error())
	}
}

// MustExec fails the test if dbi.Exec(query) returns an error.
func (t T) MustExec(dbi *gorp.DbMap, query string, args ...interface{}) {
	t.Helper()
	_, err := dbi.Exec(query, args...)
	t.Must(err)
}
