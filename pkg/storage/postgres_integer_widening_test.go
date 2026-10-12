package storage

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestRewriteSQLWidensDDLIntegerToBigint(t *testing.T) {
	ddl := `CREATE TABLE IF NOT EXISTS tests (
		id TEXT PRIMARY KEY,
		shakeout_enabled INTEGER DEFAULT 0,
		duration_ns INTEGER,
		cumulative_pause_ns INTEGER NOT NULL DEFAULT 0,
		note TEXT DEFAULT 'INTEGER'
	)`
	got := rewriteSQL(ddl, storeDialectPostgres)
	for _, want := range []string{
		"shakeout_enabled BOOLEAN DEFAULT FALSE",
		"duration_ns BIGINT",
		"cumulative_pause_ns BIGINT NOT NULL DEFAULT 0",
		"DEFAULT 'INTEGER'",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rewritten DDL missing %q:\n%s", want, got)
		}
	}

	alter := `ALTER TABLE tests ADD COLUMN paused_ns INTEGER NOT NULL DEFAULT 0`
	if got := rewriteSQL(alter, storeDialectPostgres); !strings.Contains(got, "paused_ns BIGINT NOT NULL DEFAULT 0") {
		t.Fatalf("rewritten ALTER = %q, want BIGINT column", got)
	}

	// DML casts are small counters and must keep their original semantics.
	dml := `UPDATE app_settings SET value = CAST(value AS INTEGER) + 1 WHERE key = ?`
	if got := rewriteSQL(dml, storeDialectPostgres); !strings.Contains(got, "CAST(value AS INTEGER)") {
		t.Fatalf("DML cast was rewritten: %q", got)
	}

	if got := rewriteSQL(ddl, storeDialectSQLite); got != ddl {
		t.Fatalf("SQLite DDL must be unchanged, got %q", got)
	}
}

// TestPostgresSchemaDeclaresNoInt4Columns drives the real schema through the
// Postgres rewriter: no column may come out as int4, which cannot hold the
// int64 values the SQLite-first schema stores in INTEGER columns.
func TestPostgresSchemaDeclaresNoInt4Columns(t *testing.T) {
	stmts := append([]string{schemaMigrationLedgerDDL}, schemaCreateTableStatements()...)
	for _, migration := range schemaColumnMigrations() {
		stmts = append(stmts, migration.query)
	}
	int4 := regexp.MustCompile(`(?i)\b(?:INTEGER|INT|INT4|SERIAL)\b`)
	for _, stmt := range stmts {
		rewritten := rewriteSQL(stmt, storeDialectPostgres)
		var code strings.Builder
		rewriteSQLCodeSegments(rewritten, func(segment string) string {
			code.WriteString(segment)
			code.WriteString(" ")
			return segment
		})
		if match := int4.FindString(code.String()); match != "" {
			t.Errorf("rewritten Postgres DDL still declares %q:\n%s", match, strings.TrimSpace(rewritten))
		}
	}
}

func TestPostgresOwnedTableNamesCoverSchema(t *testing.T) {
	names := postgresOwnedTableNames()
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if seen[name] {
			t.Fatalf("duplicate owned table %q", name)
		}
		seen[name] = true
	}
	for _, required := range []string{"tests", "test_history", "scheduler_leases", "schema_migrations", "live_tables", "live_table_journal", "live_table_finalizations", "project_live_tables", "project_live_table_instances", "project_live_table_journal", "project_live_table_attachments", "project_live_table_operator_receipts", "run_rendezvous"} {
		if !seen[required] {
			t.Fatalf("owned tables missing %q: %v", required, names)
		}
	}
	// The schema registry also carries indexes. Compare the owned-name parser
	// against the independently materialized schema, not the statement count.
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "owned-tables.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	rows, err := store.db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var actual []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		actual = append(actual, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(names, actual) {
		t.Fatalf("PostgreSQL owned tables %v do not match materialized schema %v", names, actual)
	}
}
