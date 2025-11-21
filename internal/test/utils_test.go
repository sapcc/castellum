// SPDX-FileCopyrightText: 2025 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package test

import (
	"testing"

	"github.com/majewsky/gg/jsonmatch"
)

func TestRemoveCommentsFromJSON(t *testing.T) {
	jsonStr := `{
		"name": "test", // This is an inline comment
		// This is a single line comment
		"value": 42, // Another inline comment
		/* This is a multiline
			comment that spans
			multiple lines */
		"enabled": true, // Final inline comment
		// Another single line comment
		"config": {
			"debug": false /* inline multiline comment */
		}
	}`

	expected := jsonmatch.Object{
		"name":    "test",
		"value":   42,
		"enabled": true,
		"config": jsonmatch.Object{
			"debug": false,
		},
	}

	result := removeCommentsFromJSON(jsonStr)
	for _, diff := range expected.DiffAgainst([]byte(result)) {
		t.Error(diff.String())
	}
}
