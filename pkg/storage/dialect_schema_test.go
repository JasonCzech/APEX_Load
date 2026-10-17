package storage

import (
	"strings"
	"testing"
)

func TestExplicitBooleanSchemaPreservesExistingDialectDDL(t *testing.T) {
	legacy := schemaCreateTableStatements()
	native := schemaCreateTableStatementsFor(storeDialectPostgres)
	if len(legacy) != len(native) {
		t.Fatal("schema statement count changed")
	}
	for i, query := range native {
		if got, want := rewriteSQL(query, storeDialectPostgres), rewriteSQL(legacy[i], storeDialectPostgres); got != want {
			t.Fatalf("schema %d changed: %s\nwant: %s", i, got, want)
		}
	}
	for _, migration := range schemaColumnMigrations() {
		if got, want := rewriteSQL(migration.statement(storeDialectPostgres), storeDialectPostgres), rewriteSQL(migration.query, storeDialectPostgres); got != want {
			t.Fatalf("migration %s changed: %s != %s", migration.name, got, want)
		}
	}
}

func TestBooleanMigrationIsIndependentOfColumnSpelling(t *testing.T) {
	for _, notNull := range []bool{false, true} {
		for _, defaultValue := range []bool{false, true} {
			migration := booleanColumnMigration("new_table", "future_flag_with_new_name", notNull, defaultValue)
			pg := migration.statement(storeDialectPostgres)
			if !strings.Contains(pg, "future_flag_with_new_name BOOLEAN") || strings.Contains(pg, "INTEGER") {
				t.Fatalf("explicit Postgres type = %s", pg)
			}
			if got := rewriteSQL(pg, storeDialectPostgres); got != pg {
				t.Fatalf("explicit boolean required rewriting: %s", got)
			}
			if !strings.Contains(migration.statement(storeDialectSQLite), "future_flag_with_new_name INTEGER") {
				t.Fatal("SQLite affinity changed")
			}
		}
	}
}

func exerciseExplicitDialectUpgrade(t *testing.T, store *SQLiteStore) {
	t.Helper()
	const table = "dialect_refactor_fixture"
	if _, err := store.db.Exec(`CREATE TABLE dialect_refactor_fixture (id TEXT PRIMARY KEY, wide_value INTEGER NOT NULL, literal_text TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	const literal = "active INTEGER DEFAULT 1 ? enabled COALESCE(enabled, 1)"
	const wide = int64(1 << 40)
	if _, err := store.db.Exec(`INSERT INTO dialect_refactor_fixture (id,wide_value,literal_text) VALUES (?, ?, 'active INTEGER DEFAULT 1 ? enabled COALESCE(enabled, 1)')`, "old", wide); err != nil {
		t.Fatal(err)
	}
	migrations := []optionalSchemaMigration{booleanColumnMigration(table, "future_flag_with_new_name", true, true)}
	if err := store.applyOptionalSchemaMigrations(migrations); err != nil {
		t.Fatal(err)
	}
	if err := store.applyOptionalSchemaMigrations(migrations); err != nil {
		t.Fatalf("idempotent upgrade: %v", err)
	}
	var flag bool
	var count int64
	var text string
	if err := store.db.QueryRow(`SELECT future_flag_with_new_name,wide_value,literal_text FROM dialect_refactor_fixture WHERE id = ?`, "old").Scan(&flag, &count, &text); err != nil {
		t.Fatal(err)
	}
	if !flag || count != wide || text != literal {
		t.Fatalf("upgraded record = %v %d %q", flag, count, text)
	}
	if _, err := store.db.Exec(`UPDATE dialect_refactor_fixture SET future_flag_with_new_name = ? WHERE id = ?`, store.boolValue(false), "old"); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT future_flag_with_new_name FROM dialect_refactor_fixture WHERE id = ?`, "old").Scan(&flag); err != nil || flag {
		t.Fatalf("boolean round trip: %v %v", flag, err)
	}
	if store.db.dialect == storeDialectPostgres {
		for column, want := range map[string]string{"future_flag_with_new_name": "boolean", "wide_value": "bigint"} {
			var got string
			if err := store.db.QueryRow(`SELECT data_type FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = ? AND column_name = ?`, table, column).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("%s type=%s want %s", column, got, want)
			}
		}
	}
}

func TestSQLiteExplicitDialectUpgrade(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir() + "/dialect.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	exerciseExplicitDialectUpgrade(t, store)
}
