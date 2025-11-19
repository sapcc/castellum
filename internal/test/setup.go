// SPDX-FileCopyrightText: 2025 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package test

import (
	"testing"

	"github.com/go-gorp/gorp/v3"
	"github.com/sapcc/go-bits/audittools"
	"github.com/sapcc/go-bits/easypg"
	"github.com/sapcc/go-bits/logg"
	"github.com/sapcc/go-bits/mock"
	"github.com/sapcc/go-bits/osext"

	"github.com/sapcc/castellum/internal/core"
	"github.com/sapcc/castellum/internal/db"
)

type setupParams struct {
	DBFixtureFile string
}

// SetupOption is an option that can be given to NewSetup().
type SetupOption func(*setupParams)

// WithDBFixtureFile is a SetupOption that initializes the DB by executing the given SQL file.
func WithDBFixtureFile(path string) SetupOption {
	return func(params *setupParams) {
		params.DBFixtureFile = path
	}
}

// Setup contains all the pieces that are needed for most tests.
type Setup struct {
	// for all types of integration tests
	DB             *gorp.DbMap
	ProviderClient MockProviderClient

	// for API tests only
	Auditor   *audittools.MockAuditor
	Validator *mock.Validator[*mock.Enforcer]
}

// NewSetup prepares most or all pieces of Keppel for a test.
func NewSetup(t *testing.T, opts ...SetupOption) Setup {
	t.Helper()
	logg.ShowDebug = osext.GetenvBool("CASTELLUM_DEBUG")
	var params setupParams
	for _, option := range opts {
		option(&params)
	}

	// initialize all parts of Setup that can be written as a single expression
	s := Setup{
		DB: nil, // see below
		ProviderClient: MockProviderClient{
			Domains: map[string]core.CachedDomain{
				"domain1": {Name: "First Domain"},
			},
			Projects: map[string]core.CachedProject{
				"project1": {Name: "First Project", DomainID: "domain1"},
				"project2": {Name: "Second Project", DomainID: "domain1"},
				"project3": {Name: "Third Project", DomainID: "domain1"},
			},
		},
		Auditor:   audittools.NewMockAuditor(),
		Validator: mock.NewValidator(mock.NewEnforcer(), nil),
	}

	// initialize DB
	dbOpts := []easypg.TestSetupOption{
		easypg.ClearTables("resources", "assets", "pending_operations", "finished_operations"),
		easypg.ResetPrimaryKeys("resources", "assets", "pending_operations"),
	}
	if params.DBFixtureFile != "" {
		dbOpts = append(dbOpts, easypg.LoadSQLFile(params.DBFixtureFile))
	}
	dbConn := easypg.ConnectForTest(t, db.Configuration(), dbOpts...)
	s.DB = db.InitORM(dbConn)
	t.Cleanup(func() {
		_ = dbConn.Close()
	})

	return s
}
