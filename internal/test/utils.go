// SPDX-FileCopyrightText: 2019 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package test

import (
	"regexp"
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

// removeCommentsFromJSON removes C-style comments from JSON literals.
// It is intended only for use with JSON literals that appear in test code.
// Its implementation is very simple and not intended for use with untrusted inputs.
func removeCommentsFromJSON(jsonStr string) string {
	singleLineCommentRegex := regexp.MustCompile(`//[^\n]*`)
	multiLineCommentRegex := regexp.MustCompile(`(?s)/\*.*?\*/`)
	emptyLineRegex := regexp.MustCompile(`\n\s*\n`)

	result := singleLineCommentRegex.ReplaceAllString(jsonStr, "")
	result = multiLineCommentRegex.ReplaceAllString(result, "")
	result = emptyLineRegex.ReplaceAllString(result, "\n")
	return result
}
