package storage

import (
	"database/sql"
	"strings"
	"testing"
)

func TestSchemaReleaseFingerprintIsCurrent(t *testing.T) {
	want := currentSchemaRelease().Fingerprint
	if got := schemaConvergenceFingerprint(); got != want {
		t.Fatalf("schema convergence fingerprint = %s, want %s; append a schema release before accepting schema changes", got, want)
	}
}

func TestSQLiteSchemaStatusRecordsConvergenceIdempotently(t *testing.T) {
	path := t.TempDir() + "/schema-status.db"
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	status, err := store.SchemaStatus()
	if err != nil {
		t.Fatalf("schema status: %v", err)
	}
	if status.CurrentVersion != 1 || status.AppliedVersion != 1 || len(status.Applied) != 1 || len(status.Pending) != 0 {
		t.Fatalf("schema status after init = %#v", status)
	}
	firstAppliedAt := status.Applied[0].AppliedAt
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	reopened, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer reopened.Close()
	reopenedStatus, err := reopened.SchemaStatus()
	if err != nil {
		t.Fatalf("schema status after reopen: %v", err)
	}
	if len(reopenedStatus.Applied) != 1 || !reopenedStatus.Applied[0].AppliedAt.Equal(firstAppliedAt) {
		t.Fatalf("idempotent reopen changed ledger: first=%#v reopened=%#v", status, reopenedStatus)
	}
}

func TestConfiguredStoreExposesSchemaStatus(t *testing.T) {
	store, err := NewConfiguredStore(StoreConfig{Backend: StoreBackendSQLite, SQLitePath: t.TempDir() + "/configured.db"})
	if err != nil {
		t.Fatalf("configured store: %v", err)
	}
	defer store.Close()
	provider, ok := store.(SchemaStatusProvider)
	if !ok {
		t.Fatalf("configured store %T does not expose schema status", store)
	}
	status, err := provider.SchemaStatus()
	if err != nil || status.AppliedVersion != currentSchemaRelease().Version {
		t.Fatalf("configured schema status = %#v, %v", status, err)
	}
	info := store.(BackendInfoProvider).BackendInfo()
	if info.MigrationVersion != currentSchemaVersionLabel() {
		t.Fatalf("backend migration version = %q, want %q", info.MigrationVersion, currentSchemaVersionLabel())
	}
}

func TestSchemaLedgerRejectsNewerAndChangedReleases(t *testing.T) {
	tests := []struct {
		name        string
		version     int
		releaseName string
		fingerprint string
		wantError   string
	}{
		{name: "newer", version: 2, releaseName: "future", fingerprint: "future", wantError: "newer than supported"},
		{name: "changed", version: 1, releaseName: "changed", fingerprint: "changed", wantError: "identity mismatch"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := t.TempDir() + "/ledger.db"
			raw, err := sql.Open("sqlite", sqliteDSN(path))
			if err != nil {
				t.Fatalf("open raw db: %v", err)
			}
			if _, err := raw.Exec(schemaMigrationLedgerDDL); err != nil {
				t.Fatalf("create ledger: %v", err)
			}
			if _, err := raw.Exec(`INSERT INTO schema_migrations (version, name, fingerprint, applied_at) VALUES (?, ?, ?, CURRENT_TIMESTAMP)`, test.version, test.releaseName, test.fingerprint); err != nil {
				t.Fatalf("seed ledger: %v", err)
			}
			if err := raw.Close(); err != nil {
				t.Fatalf("close raw db: %v", err)
			}
			_, err = NewSQLiteStore(path)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("NewSQLiteStore error = %v, want %q", err, test.wantError)
			}
		})
	}
}
