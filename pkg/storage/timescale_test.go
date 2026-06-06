package storage

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreConfigFromEnvParsesTimescaleDB(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"", false},
		{"false", false},
		{"0", false},
		{"true", true},
		{"1", true},
		{"YES", true},
		{"On", true},
	}
	for _, tc := range cases {
		t.Run("APEXLOAD_TIMESCALEDB="+tc.value, func(t *testing.T) {
			if tc.value == "" {
				t.Setenv("APEXLOAD_TIMESCALEDB", "")
			} else {
				t.Setenv("APEXLOAD_TIMESCALEDB", tc.value)
			}
			if got := StoreConfigFromEnv().TimescaleDB; got != tc.want {
				t.Fatalf("TimescaleDB = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTimescaleSetupStatements(t *testing.T) {
	statements := timescaleSetupStatements(timescaleHypertables)
	if len(statements) == 0 {
		t.Fatal("expected at least one TimescaleDB setup statement")
	}
	if statements[0] != `CREATE EXTENSION IF NOT EXISTS timescaledb` {
		t.Fatalf("first statement = %q, want CREATE EXTENSION", statements[0])
	}
	joined := strings.Join(statements, "\n")
	if !strings.Contains(joined, `create_hypertable('test_interval_summaries', 'timestamp', if_not_exists => TRUE, migrate_data => TRUE)`) {
		t.Fatalf("missing hypertable conversion for test_interval_summaries:\n%s", joined)
	}
}

func TestTimescaleDefaultsDisabledOnSQLite(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	if store.BackendInfo().TimescaleDBEnabled {
		t.Fatal("SQLite store should report TimescaleDBEnabled=false")
	}
	if store.timescaleDB {
		t.Fatal("SQLite store should not enable TimescaleDB")
	}
}

func TestApplyTimescaleDBNoOpWhenNotPostgres(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	// Even if the flag were somehow set, the SQLite dialect must short-circuit:
	// create_hypertable / the timescaledb extension only exist on PostgreSQL.
	store.timescaleDB = true
	if err := store.applyTimescaleDB(); err != nil {
		t.Fatalf("applyTimescaleDB on sqlite should be a no-op, got: %v", err)
	}
}

func TestConfiguredSQLiteIgnoresTimescaleFlag(t *testing.T) {
	store, err := NewConfiguredStore(StoreConfig{
		SQLitePath:  filepath.Join(t.TempDir(), "apexload.db"),
		TimescaleDB: true,
	})
	if err != nil {
		t.Fatalf("NewConfiguredStore returned error: %v", err)
	}
	defer store.Close()

	info := store.(BackendInfoProvider).BackendInfo()
	if info.TimescaleDBEnabled {
		t.Fatal("SQLite backend must ignore TimescaleDB flag (postgres-only feature)")
	}
}
