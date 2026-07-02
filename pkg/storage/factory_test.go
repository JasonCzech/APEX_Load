package storage

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestNewConfiguredStoreDefaultsToSQLite(t *testing.T) {
	store, err := NewConfiguredStore(StoreConfig{SQLitePath: filepath.Join(t.TempDir(), "apexload.db")})
	if err != nil {
		t.Fatalf("NewConfiguredStore returned error: %v", err)
	}
	defer store.Close()

	info, ok := store.(BackendInfoProvider)
	if !ok {
		t.Fatal("configured store does not expose backend info")
	}
	if info.BackendInfo().Type != StoreBackendSQLite {
		t.Fatalf("backend type = %q, want sqlite", info.BackendInfo().Type)
	}
}

func TestNewConfiguredStoreRejectsPostgresWithoutDatabaseURL(t *testing.T) {
	_, err := NewConfiguredStore(StoreConfig{Backend: StoreBackendPostgres})
	if err == nil {
		t.Fatal("expected missing postgres database url error")
	}
	if !strings.Contains(err.Error(), "postgres database url") {
		t.Fatalf("error = %q, want database url guidance", err.Error())
	}
}

func TestRewriteSQLForPostgres(t *testing.T) {
	query := `INSERT OR IGNORE INTO domains (id, name, created_at, updated_at) VALUES (?, ?, ?, ?)`
	got := rewriteSQL(query, storeDialectPostgres)
	want := `INSERT INTO domains (id, name, created_at, updated_at) VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`
	if got != want {
		t.Fatalf("rewriteSQL insert = %q, want %q", got, want)
	}

	ddl := `CREATE TABLE IF NOT EXISTS tests (
		id TEXT PRIMARY KEY,
		shakeout_enabled INTEGER DEFAULT 0,
		baseline_pinned INTEGER DEFAULT 0,
		start_time DATETIME,
		project_id TEXT REFERENCES projects(id),
		FOREIGN KEY(project_id) REFERENCES projects(id)
	)`
	got = rewriteSQL(ddl, storeDialectPostgres)
	for _, forbidden := range []string{"DATETIME", "REFERENCES", "FOREIGN KEY", "INTEGER DEFAULT 0"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("rewritten ddl still contains %q: %s", forbidden, got)
		}
	}
	if !strings.Contains(got, "TIMESTAMPTZ") || !strings.Contains(got, "BOOLEAN DEFAULT FALSE") {
		t.Fatalf("rewritten ddl did not normalize timestamp/bool types: %s", got)
	}

	// Read-side COALESCE defaults on boolean columns must be rewritten to FALSE,
	// or Postgres rejects COALESCE(boolean, 0) with a type mismatch. Each boolean
	// column whose DDL is rewritten to BOOLEAN must have a matching COALESCE entry;
	// the object-repository read (auto_promote_on_heal) regressed without it.
	for _, col := range []string{"shakeout_enabled", "resource_baseline_enabled", "auto_promote_on_heal", "baseline_pinned"} {
		sel := "SELECT COALESCE(" + col + ", 0) FROM t"
		gotSel := rewriteSQL(sel, storeDialectPostgres)
		if strings.Contains(gotSel, "COALESCE("+col+", 0)") {
			t.Fatalf("COALESCE(%s, 0) was not rewritten for Postgres: %s", col, gotSel)
		}
		if !strings.Contains(gotSel, "COALESCE("+col+", FALSE)") {
			t.Fatalf("COALESCE(%s, ...) not rewritten to FALSE: %s", col, gotSel)
		}
	}
}

func TestPostgresAuthQueriesUseDialectSafePredicates(t *testing.T) {
	got := rewriteSQL(`SELECT COUNT(DISTINCT u.id) FROM users u
		JOIN user_role_bindings b ON b.user_id = u.id
		WHERE u.active = ? AND b.scope_type = 'global' AND b.role = ?`, storeDialectPostgres)
	if strings.Contains(got, "active = 1") {
		t.Fatalf("rewritten auth count query still uses SQLite boolean literal: %s", got)
	}
	if !strings.Contains(got, "u.active = $1") || !strings.Contains(got, "b.role = $2") {
		t.Fatalf("rewritten auth count query placeholders = %s, want parameterized bool and role", got)
	}

	got = rewriteSQL(`SELECT id FROM users WHERE 1 = 1 AND (LOWER(subject) LIKE ? OR LOWER(email) LIKE ? OR LOWER(display_name) LIKE ?)`, storeDialectPostgres)
	if !strings.Contains(got, "LOWER(subject) LIKE $1") || !strings.Contains(got, "LOWER(email) LIKE $2") || !strings.Contains(got, "LOWER(display_name) LIKE $3") {
		t.Fatalf("rewritten user search query = %s, want case-normalized LIKE predicates", got)
	}
}

// looksBooleanColumn reports whether a column name follows one of the project's
// boolean naming conventions, i.e. it is bound/scanned as a Go bool and therefore
// must be a Postgres BOOLEAN (not INTEGER). Counters/sizes/versions/flags stored as
// 0/1 ints (sort_order, revision, desired_count, cordoned, …) intentionally do NOT
// match — they stay INTEGER in both dialects. Extend this list if a new bool naming
// convention is introduced.
func looksBooleanColumn(name string) bool {
	switch {
	case strings.HasSuffix(name, "_enabled"),
		strings.HasSuffix(name, "_disabled"),
		strings.HasSuffix(name, "_pinned"),
		strings.HasPrefix(name, "is_"),
		strings.HasPrefix(name, "has_"),
		strings.HasPrefix(name, "auto_promote"),
		name == "active",
		name == "enabled":
		return true
	default:
		return false
	}
}

// TestPostgresSchemaBooleanColumnsRewriteToBoolean drives the REAL schema — every
// CREATE TABLE plus every ALTER ... ADD COLUMN — through the Postgres dialect rewriter
// and fails if any conventionally-boolean column survives as INTEGER, or if any
// COALESCE(bool_col, 0|1) read in the storage package's SQL is not rewritten to a
// boolean default. There is no separate Postgres schema file: rewriteSQL translates
// the SQLite DDL/DML on the fly, and its boolean handling is a hardcoded list. This
// guard makes that list self-policing against the schema so a newly added boolean
// column can't silently break Postgres (regressed once on auto_promote_on_heal).
func TestPostgresSchemaBooleanColumnsRewriteToBoolean(t *testing.T) {
	stmts := append([]string{}, schemaCreateTableStatements()...)
	for _, m := range schemaColumnMigrations() {
		stmts = append(stmts, m.query)
	}

	intColumn := regexp.MustCompile(`(?i)\b([a-z_][a-z0-9_]*)\s+INTEGER\b`)

	// Collect the boolean columns declared anywhere in the live schema.
	boolCols := map[string]bool{}
	for _, stmt := range stmts {
		for _, m := range intColumn.FindAllStringSubmatch(stmt, -1) {
			if name := strings.ToLower(m[1]); looksBooleanColumn(name) {
				boolCols[name] = true
			}
		}
	}
	if len(boolCols) == 0 {
		t.Fatal("no boolean columns detected in live schema — the schema accessors or naming heuristic are broken")
	}

	// DDL side: after rewriting, no boolean column may remain INTEGER.
	for _, stmt := range stmts {
		rewritten := rewriteSQL(stmt, storeDialectPostgres)
		for _, m := range intColumn.FindAllStringSubmatch(rewritten, -1) {
			if name := strings.ToLower(m[1]); looksBooleanColumn(name) {
				t.Errorf("boolean column %q is still INTEGER after the Postgres rewrite — add a BOOLEAN entry to rewriteSQL.\nstatement: %s", name, strings.TrimSpace(stmt))
			}
		}
	}

	// Read side: every COALESCE(bool_col, 0|1) that actually appears in the storage
	// package's SQL must be rewritten so the integer default becomes a boolean, or
	// Postgres rejects COALESCE(boolean, 0) with a type mismatch.
	coalesce := regexp.MustCompile(`COALESCE\(\s*([a-z_][a-z0-9_]*)\s*,\s*([01])\s*\)`)
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	seen := 0
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, m := range coalesce.FindAllStringSubmatch(string(src), -1) {
			col := strings.ToLower(m[1])
			if !boolCols[col] {
				continue
			}
			seen++
			raw := m[0]
			got := rewriteSQL(raw, storeDialectPostgres)
			if !strings.Contains(got, "COALESCE("+col+", FALSE)") && !strings.Contains(got, "COALESCE("+col+", TRUE)") {
				t.Errorf("%s: %q is not rewritten to a boolean default for Postgres — add a COALESCE entry to rewriteSQL. got: %s", name, raw, got)
			}
		}
	}
	if seen == 0 {
		t.Fatal("found no COALESCE(bool_col, 0|1) usages to verify — the source scan likely regressed")
	}
}

func TestSQLiteInitIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "apexload.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("first NewSQLiteStore returned error: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("first Close returned error: %v", err)
	}

	store, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("second NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
}

func TestIsDuplicateColumnErrRecognizesSQLiteAndPostgresMessages(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "sqlite duplicate column",
			err:  errors.New("SQL logic error: duplicate column name: runtime_settings_json (1)"),
			want: true,
		},
		{
			name: "postgres duplicate column",
			err:  errors.New(`ERROR: column "runtime_settings_json" of relation "tests" already exists (SQLSTATE 42701)`),
			want: true,
		},
		{
			name: "different ddl error",
			err:  errors.New("ERROR: relation tests does not exist"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isDuplicateColumnErr(tt.err); got != tt.want {
				t.Fatalf("isDuplicateColumnErr() = %v, want %v", got, tt.want)
			}
		})
	}
}
