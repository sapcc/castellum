/******************************************************************************
*
*  Copyright 2024 SAP SE
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
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/sapcc/go-bits/logg"
	"github.com/sapcc/go-bits/must"
)

var clientLaunchScript = `#!/bin/sh
set -euo pipefail

stop_postgres() {
	EXIT_CODE=$?
	pg_ctl stop --wait --silent -D .testdb/datadir
}
trap stop_postgres EXIT INT TERM

rm -f -- .testdb/run/postgresql.log
pg_ctl start --wait --silent -D .testdb/datadir -l .testdb/run/postgresql.log
%s -U postgres -h 127.0.0.1 -p 54321 "$@"
`

var insideWithTestDatabase = false

// WithTestDatabase spawns a PostgreSQL database for the duration of a `go test` run.
// Its data directory, configuration and logs are stored in the ".testdb" directory below the repository root.
//
// How to interact with the test database:
//   - To inspect it manually, use one of the helper scripts in the ".testdb" directory, e.g. ".testdb/psql.sh".
//   - It is currently not supported to run tests for multiple packages concurrently, so make sure to run "go test" with "-p 1".
//   - Make sure to add "/.testdb" to your repository's .gitignore rules.
//
// This function takes a testing.M because it is supposed to be called from TestMain().
// This is required to ensure that its cleanup phase shuts down the database server after all tests have been executed.
// Add a TestMain() like this to each package that needs to interact with the test database:
//
//	func TestMain(m *testing.M) {
//		test.WithTestDatabase(m, func() int { return m.Run() })
//	}
func WithTestDatabase(m *testing.M, action func() int) int {
	rootPath := must.Return(findRepositoryRootDir())

	// create DB on first use
	hasPostgresDB := must.Return(checkPathExists(filepath.Join(rootPath, ".testdb/datadir/PG_VERSION")))
	if !hasPostgresDB {
		for _, dirName := range []string{".testdb/datadir", ".testdb/run"} {
			must.Succeed(os.MkdirAll(filepath.Join(rootPath, dirName), 0777)) // subject to umask
		}
		cmd := exec.Command("initdb", "-A", "trust", "-U", "postgres", //nolint:gosec // rule G204 is overly broad
			"-D", filepath.Join(rootPath, ".testdb/datadir"),
			"-c", "external_pid_file="+filepath.Join(rootPath, ".testdb/run/pid"),
			"-c", "unix_socket_directories="+filepath.Join(rootPath, ".testdb/run"),
			"-c", "port=54321",
		)
		cmd.Stdin = nil
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		err := cmd.Run()
		if err != nil {
			logg.Fatal("could not run initdb: %s", err.Error())
		}
	}

	// drop helper scripts that can be used to attach to the test DB for manual debugging and inspection
	for _, clientTool := range []string{"psql", "pgcli", "pg_dump"} {
		path := filepath.Join(rootPath, ".testdb", clientTool+".sh")
		contents := fmt.Sprintf(clientLaunchScript, clientTool)
		must.Succeed(os.WriteFile(path, []byte(contents), 0777)) // subject to umask, intentionally executable
	}

	// start database process
	cmd := exec.Command("pg_ctl", "start", "--wait", "--silent", //nolint:gosec // rule G204 is overly broad
		"-D", filepath.Join(rootPath, ".testdb/datadir"),
		"-l", filepath.Join(rootPath, ".testdb/run/postgresql.log"),
	)
	cmd.Stdin = nil
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		logg.Fatal("could not run pg_ctl start: %s", err.Error())
	}

	// run tests
	insideWithTestDatabase = true
	exitCode := action()
	insideWithTestDatabase = false

	// stop database process (regardless of whether tests succeeded or failed!)
	cmd = exec.Command("pg_ctl", "stop", "--wait", "--silent", //nolint:gosec // rule G204 is overly broad
		"-D", filepath.Join(rootPath, ".testdb/datadir"),
	)
	cmd.Stdin = nil
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	if err != nil {
		logg.Fatal("could not run pg_ctl stop: %s", err.Error())
	}

	return exitCode
}

func findRepositoryRootDir() (string, error) {
	// NOTE: `go test` runs each test within the directory containing its source code.
	dirPath, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		isRepoRoot, err := checkPathExists(filepath.Join(dirPath, "go.mod"))
		switch {
		case err != nil:
			return "", err
		case isRepoRoot:
			return dirPath, nil
		default:
			// this is not the repo root, keep searching
			parentPath := filepath.Dir(dirPath)
			if parentPath == dirPath {
				return "", errors.New("could not find repository root (neither $PWD nor any parents contain a go.mod file)")
			}
			dirPath = parentPath
		}
	}
}

func checkPathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	switch {
	case err == nil:
		return true, nil
	case os.IsNotExist(err):
		return false, nil
	default:
		return false, err
	}
}

type setupOpts struct {
	tableNamesForClear   []string
	sqlFileToLoad        string
	tableNamesForPKReset []string
}

type SetupOption func(*setupOpts)

func ClearTables(tableNames ...string) SetupOption {
	return func(opts *setupOpts) {
		opts.tableNamesForClear = append(opts.tableNamesForClear, tableNames...)
	}
}

func ExecSQLFile(path string) SetupOption {
	return func(opts *setupOpts) {
		opts.sqlFileToLoad = path
	}
}

func ResetPrimaryKeys(tableNames ...string) SetupOption {
	return func(opts *setupOpts) {
		opts.tableNamesForPKReset = append(opts.tableNamesForPKReset, tableNames...)
	}
}

func ConnectToTestDatabase(t *testing.T, opts ...SetupOption) *sql.DB {
	TODO("how does this mesh into the non-test case? we need an easypg.Configuration here if we want to use easypg.Connect")
}
