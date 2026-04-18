// SPDX-FileCopyrightText: 2026 Stefan Majewsky <majewsky@gmx.net>
// SPDX-License-Identifier: Apache-2.0

package oblast

import (
	"strconv"
	"strings"
)

// Dialect accounts for differences between different SQL dialects
// that are relevant to query generation within Oblast.
//
// # Compatibility notice
//
// This interface may be extended, even within minor versions, when doing so is
// required to add support for new DB dialects that differ from previously
// supported dialects in unexpected ways.
type Dialect interface {
	// Placeholder returns the placeholder for the i-th query argument.
	// Most dialects use "?", but e.g. PostgreSQL uses "$1", "$2" and so on.
	// The argument numbers from 0 like a slice index.
	Placeholder(i int) string

	// QuoteIdentifier wraps the name of a column or table in quotes,
	// in order to avoid the name from being interpreted as a keyword.
	QuoteIdentifier(name string) string

	// UsesLastInsertID returns whether values for auto-generated columns are
	// collected from LastInsertID(). If false, the INSERT query must instead
	// yield a result row containing the values.
	UsesLastInsertID() bool

	// InsertSuffixForAutoColumns is appended to `INSERT (...) VALUES (...)`
	// statements to collect values for auto-filled columns.
	//
	// If UsesLastInsertID is true, this is usually not needed and the empty
	// string can be returned.
	InsertSuffixForAutoColumns(columns []string) string
}

// MysqlDialect is the dialect of MySQL and MariaDB databases.
func MysqlDialect() Dialect {
	return mysqlDialect{}
}

type mysqlDialect struct{}

func (mysqlDialect) Placeholder(_ int) string                           { return "?" }
func (mysqlDialect) QuoteIdentifier(name string) string                 { return "`" + name + "`" }
func (mysqlDialect) UsesLastInsertID() bool                             { return true }
func (mysqlDialect) InsertSuffixForAutoColumns(columns []string) string { return "" }

// PostgresDialect is the dialect of PostgreSQL databases.
func PostgresDialect() Dialect {
	return postgresDialect{}
}

type postgresDialect struct{}

func (postgresDialect) Placeholder(i int) string           { return "$" + strconv.Itoa(i+1) }
func (postgresDialect) QuoteIdentifier(name string) string { return `"` + name + `"` }
func (postgresDialect) UsesLastInsertID() bool             { return false }

func (p postgresDialect) InsertSuffixForAutoColumns(columns []string) string {
	quotedColumns := make([]string, len(columns))
	for idx, name := range columns {
		quotedColumns[idx] = p.QuoteIdentifier(name)
	}
	return ` RETURNING ` + strings.Join(quotedColumns, ", ")
}

// SqliteDialect is the dialect of SQLite databases.
func SqliteDialect() Dialect {
	return sqliteDialect{}
}

type sqliteDialect struct{}

func (sqliteDialect) Placeholder(_ int) string                           { return "?" }
func (sqliteDialect) QuoteIdentifier(name string) string                 { return `"` + name + `"` }
func (sqliteDialect) UsesLastInsertID() bool                             { return true }
func (sqliteDialect) InsertSuffixForAutoColumns(columns []string) string { return "" }
