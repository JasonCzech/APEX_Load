package storage

import (
	"errors"
	"path/filepath"
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
