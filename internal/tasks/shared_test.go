// SPDX-FileCopyrightText: 2019 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package tasks_test

import (
	"testing"
	"time"

	"github.com/sapcc/go-bits/easypg"

	"github.com/sapcc/castellum/internal/plugins"
	"github.com/sapcc/castellum/internal/test"
)

func TestMain(m *testing.M) {
	easypg.WithTestDB(m, func() int { return m.Run() })
}

func commonSetupOptionsForWorkerTest() test.SetupOption {
	return test.WithAssetManagers(
		&plugins.AssetManagerStatic{AssetType: "foo"},
	)
}

// Take pointer to time.Time expression.
func p2time(t time.Time) *time.Time {
	return &t
}
