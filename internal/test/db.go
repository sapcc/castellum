// SPDX-FileCopyrightText: 2019 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package test

import (
	"github.com/go-gorp/gorp/v3"
)

// MustUpdate aborts the test if dbi.Update(row) throws an error.
func (t T) MustUpdate(dbi *gorp.DbMap, row any) {
	_, err := dbi.Update(row)
	t.Must(err)
}
