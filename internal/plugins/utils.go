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

package plugins

import "github.com/sapcc/castellum/internal/db"

// UserError is an error wrapper that simplifies
//
// TODO upstream this into internal/core and make
type UserError struct {
	Inner error
}

// Error implements the builtin/error interface.
func (e UserError) Error() string {
	return e.Inner.Error()
}

// Cause implements the causer interface implied by errors.Cause().
func (e UserError) Cause() error {
	return e.Inner
}

// Classify inspects the given error, unwraps UserError if possible, and adds an
// appropriate db.OperationOutcome to the result.
func Classify(err error) (db.OperationOutcome, error) {
	switch err := err.(type) {
	case nil:
		return db.OperationOutcomeSucceeded, nil
	case UserError:
		return db.OperationOutcomeFailed, err.Inner
	default:
		return db.OperationOutcomeErrored, err
	}
}
