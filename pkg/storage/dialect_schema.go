package storage

import "fmt"

// booleanColumnDDL renders the declared semantic type directly. A new boolean
// column no longer needs a matching column-name expression in rewriteSQL.
// SQLite retains its historical INTEGER affinity and numeric default; Postgres
// receives BOOLEAN and a boolean literal before the compatibility wrapper runs.
func (dialect storeDialect) booleanColumnDDL(notNull, defaultValue bool) string {
	typ, value := "INTEGER", "0"
	if defaultValue {
		value = "1"
	}
	if dialect == storeDialectPostgres {
		typ, value = "BOOLEAN", "FALSE"
		if defaultValue {
			value = "TRUE"
		}
	}
	if notNull {
		typ += " NOT NULL"
	}
	return typ + " DEFAULT " + value
}

func booleanColumnMigration(table, column string, notNull, defaultValue bool) optionalSchemaMigration {
	prefix := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s ", table, column)
	return optionalSchemaMigration{
		name:          table + "." + column,
		query:         prefix + storeDialectSQLite.booleanColumnDDL(notNull, defaultValue),
		postgresQuery: prefix + storeDialectPostgres.booleanColumnDDL(notNull, defaultValue),
	}
}

// statement keeps untouched migrations on the legacy dialect adapter, while
// explicit backend DDL can evolve without depending on schema-name rewriting.
func (migration optionalSchemaMigration) statement(dialect storeDialect) string {
	if dialect == storeDialectPostgres && migration.postgresQuery != "" {
		return migration.postgresQuery
	}
	return migration.query
}
