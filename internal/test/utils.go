// SPDX-FileCopyrightText: 2019 SAP SE
// SPDX-License-Identifier: Apache-2.0

package test

import (
	"testing"

	"github.com/go-gorp/gorp/v3"
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
func (t T) MustExec(dbi *gorp.DbMap, query string, args ...any) {
	t.Helper()
	_, err := dbi.Exec(query, args...)
	t.Must(err)
}
