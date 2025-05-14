// SPDX-FileCopyrightText: 2021 SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"github.com/sapcc/go-api-declarations/castellum"
	"github.com/sapcc/go-bits/errext"
)

// UserError is an error wrapper that allows to mark errors as "failed" instead
// of "errored" without having to carry a separate OperationOutcome value.
//
// TODO upstream this into internal/core and remove OperationOutcome return values from the fallible methods on AssetManager
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
// appropriate castellum.OperationOutcome to the result.
func Classify(err error) (castellum.OperationOutcome, error) {
	if err == nil {
		return castellum.OperationOutcomeSucceeded, nil
	}
	if uerr, ok := errext.As[UserError](err); ok {
		return castellum.OperationOutcomeFailed, uerr.Inner
	} else {
		return castellum.OperationOutcomeErrored, err
	}
}
