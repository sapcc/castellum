// SPDX-FileCopyrightText: 2019 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package test

import "regexp"

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
