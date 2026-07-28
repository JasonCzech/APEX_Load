package storage

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// seedLegacyBrunoSchema is a fixed fixture for the physical table identities
// shipped before importer-neutral Go APIs were introduced.
func seedLegacyBrunoSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	statements := []string{
		`CREATE TABLE bruno_source_artifacts (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			artifact_kind TEXT NOT NULL,
			backend_key TEXT NOT NULL,
			sha256 TEXT NOT NULL,
			size_bytes INTEGER NOT NULL,
			manifest_sha256 TEXT,
			source_format TEXT,
			encryption_json TEXT NOT NULL,
			lifecycle_state TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			UNIQUE(project_id, artifact_kind, sha256)
		)`,
		`CREATE TABLE bruno_source_revisions (
			id TEXT PRIMARY KEY,
			script_id TEXT NOT NULL,
			project_id TEXT NOT NULL,
			parent_revision_id TEXT REFERENCES bruno_source_revisions(id),
			active_flag INTEGER NOT NULL DEFAULT 0,
			raw_artifact_id TEXT NOT NULL REFERENCES bruno_source_artifacts(id),
			sanitized_artifact_id TEXT NOT NULL REFERENCES bruno_source_artifacts(id),
			source_format TEXT NOT NULL,
			compatibility_version TEXT NOT NULL,
			identity_map_json TEXT NOT NULL,
			import_options_json TEXT NOT NULL,
			dependency_snapshot_ref TEXT,
			security_profile_json TEXT NOT NULL,
			change_summary_json TEXT NOT NULL,
			secret_bindings_json TEXT NOT NULL,
			canonical_script_json TEXT NOT NULL,
			created_by TEXT,
			created_at DATETIME NOT NULL
		)`,
		`CREATE TABLE bruno_native_import_batches (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			idempotency_key TEXT NOT NULL,
			created_at DATETIME NOT NULL
		)`,
		`CREATE INDEX idx_bruno_artifacts_project_hash ON bruno_source_artifacts(project_id, artifact_kind, sha256)`,
		`CREATE INDEX idx_bruno_revisions_script_created ON bruno_source_revisions(script_id, created_at)`,
		`CREATE INDEX idx_bruno_revisions_project_active ON bruno_source_revisions(project_id, active_flag)`,
		`CREATE UNIQUE INDEX idx_bruno_revisions_one_active ON bruno_source_revisions(script_id, project_id) WHERE active_flag = 1`,
		`CREATE INDEX idx_bruno_native_batches_project_created ON bruno_native_import_batches(project_id, created_at)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("seed legacy schema: %v\n%s", err, statement)
		}
	}
	if _, err := db.Exec(
		`INSERT INTO bruno_source_artifacts
			(id, project_id, artifact_kind, backend_key, sha256, size_bytes, encryption_json, lifecycle_state, created_at)
		 VALUES ('artifact-1', 'project-1', 'raw', 'key/one', 'abc123', 42, '{"alg":"test"}', 'active', ?)`,
		now,
	); err != nil {
		t.Fatalf("seed artifact: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO bruno_source_revisions
			(id, script_id, project_id, active_flag, raw_artifact_id, sanitized_artifact_id, source_format,
			 compatibility_version, identity_map_json, import_options_json, security_profile_json,
			 change_summary_json, secret_bindings_json, canonical_script_json, created_at)
		 VALUES ('revision-1', 'script-1', 'project-1', 1, 'artifact-1', 'artifact-1', 'bruno_native',
			 'v1', '{}', '{}', '{}', '{}', '{}', '{}', ?)`,
		now,
	); err != nil {
		t.Fatalf("seed revision: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO bruno_native_import_batches (id, project_id, idempotency_key, created_at)
		 VALUES ('batch-1', 'project-1', 'idem-1', ?)`,
		now,
	); err != nil {
		t.Fatalf("seed batch: %v", err)
	}
}

func TestLegacyBrunoPhysicalTablesRemainInPlaceAndRowsReadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	seedLegacyBrunoSchema(t, raw)
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw db: %v", err)
	}

	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("open compatible store: %v", err)
	}
	defer store.Close()

	var artifactSHA string
	var artifactSize int64
	if err := store.db.QueryRow(
		`SELECT sha256, size_bytes FROM bruno_source_artifacts WHERE id = ?`,
		"artifact-1",
	).Scan(&artifactSHA, &artifactSize); err != nil {
		t.Fatalf("read legacy artifact row: %v", err)
	}
	if artifactSHA != "abc123" || artifactSize != 42 {
		t.Fatalf("artifact row changed: sha=%q size=%d", artifactSHA, artifactSize)
	}

	var rawArtifactID, sourceFormat string
	if err := store.db.QueryRow(
		`SELECT raw_artifact_id, source_format FROM bruno_source_revisions WHERE id = ?`,
		"revision-1",
	).Scan(&rawArtifactID, &sourceFormat); err != nil {
		t.Fatalf("read legacy revision row: %v", err)
	}
	if rawArtifactID != "artifact-1" || sourceFormat != "bruno_native" {
		t.Fatalf("revision row changed: raw=%q format=%q", rawArtifactID, sourceFormat)
	}

	var idempotencyKey string
	if err := store.db.QueryRow(
		`SELECT idempotency_key FROM bruno_native_import_batches WHERE id = ?`,
		"batch-1",
	).Scan(&idempotencyKey); err != nil {
		t.Fatalf("read legacy receipt row: %v", err)
	}
	if idempotencyKey != "idem-1" {
		t.Fatalf("receipt row changed: key=%q", idempotencyKey)
	}

	for _, table := range importPhysicalTables() {
		legacyExists, err := store.tableExists(table.legacy)
		if err != nil {
			t.Fatalf("check %s: %v", table.legacy, err)
		}
		if !legacyExists {
			t.Fatalf("compatible physical table %s was renamed or removed", table.legacy)
		}
		renamedExists, err := store.tableExists(table.renamed)
		if err != nil {
			t.Fatalf("check %s: %v", table.renamed, err)
		}
		if renamedExists {
			t.Fatalf("unexpected format-neutral physical table %s", table.renamed)
		}
	}
}

func TestLegacyBrunoPhysicalTableStartupIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	seedLegacyBrunoSchema(t, raw)
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw db: %v", err)
	}

	first, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first: %v", err)
	}
	second, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer second.Close()

	var artifacts int
	if err := second.db.QueryRow(`SELECT COUNT(*) FROM bruno_source_artifacts`).Scan(&artifacts); err != nil {
		t.Fatalf("count artifacts: %v", err)
	}
	if artifacts != 1 {
		t.Fatalf("artifact count = %d after second open, want 1", artifacts)
	}
}

func TestFreshDatabaseUsesLegacyBrunoPhysicalImportTables(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "fresh.db"))
	if err != nil {
		t.Fatalf("fresh store: %v", err)
	}
	defer store.Close()

	for _, table := range importPhysicalTables() {
		legacyExists, err := store.tableExists(table.legacy)
		if err != nil {
			t.Fatalf("check %s: %v", table.legacy, err)
		}
		if !legacyExists {
			t.Fatalf("fresh database is missing compatible table %s", table.legacy)
		}
		renamedExists, err := store.tableExists(table.renamed)
		if err != nil {
			t.Fatalf("check %s: %v", table.renamed, err)
		}
		if renamedExists {
			t.Fatalf("fresh database unexpectedly created %s", table.renamed)
		}
	}
}

func TestRenamedImportTableLayoutFailsClosedBeforeCreatingLegacyTables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "renamed.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	if _, err := raw.Exec(`CREATE TABLE import_source_artifacts (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("seed renamed table: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw db: %v", err)
	}

	store, err := NewSQLiteStore(path)
	if err == nil {
		store.Close()
		t.Fatal("expected renamed import layout to fail closed")
	}
	if got := err.Error(); !strings.Contains(got, "import_source_artifacts") ||
		!strings.Contains(got, "bruno_source_artifacts") {
		t.Fatalf("error does not identify the maintenance repair: %v", err)
	}

	check, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("reopen raw db: %v", err)
	}
	defer check.Close()
	var legacyTables int
	if err := check.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'bruno_source_artifacts'`,
	).Scan(&legacyTables); err != nil {
		t.Fatalf("check legacy table: %v", err)
	}
	if legacyTables != 0 {
		t.Fatal("failed startup created an empty legacy table beside renamed data")
	}
}
