package storage

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestNewConfiguredStoreDefaultsToSQLite(t *testing.T) {
	store, err := NewConfiguredStore(StoreConfig{SQLitePath: filepath.Join(t.TempDir(), "stormrunner.db")})
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
