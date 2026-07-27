package storage

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// seedLegacyBrunoSchema builds the pre-rename shape of the three protected
// import tables and puts one row in each, so an upgrade can be observed rather
// than assumed. The DDL is deliberately spelled out here instead of being
// derived from schemaCreateTableStatements: this is a fixture of what shipped,
// and it must not drift when the live schema changes.
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
		 VALUES ('artifact-1', 'project-1', 'raw', 'key/one', 'abc123', 42, '{"alg":"test"}', 'active', ?)`, now); err != nil {
		t.Fatalf("seed artifact: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO bruno_source_revisions
			(id, script_id, project_id, active_flag, raw_artifact_id, sanitized_artifact_id, source_format,
			 compatibility_version, identity_map_json, import_options_json, security_profile_json,
			 change_summary_json, secret_bindings_json, canonical_script_json, created_at)
		 VALUES ('revision-1', 'script-1', 'project-1', 1, 'artifact-1', 'artifact-1', 'bruno_native',
			 'v1', '{}', '{}', '{}', '{}', '{}', '{}', ?)`, now); err != nil {
		t.Fatalf("seed revision: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO bruno_native_import_batches (id, project_id, idempotency_key, created_at)
		 VALUES ('batch-1', 'project-1', 'idem-1', ?)`, now); err != nil {
		t.Fatalf("seed batch: %v", err)
	}
}

// TestUpgradeFromLegacyBrunoTablesPreservesRows is the test that justifies the
// rename shipping at all. The failure it guards against is silent: if
// schemaCreateTableStatements ran before the rename, init() would succeed, the
// new tables would exist, every query would work, and the encrypted artifacts
// and commit receipts would simply be gone from the application's view.
//
// So this asserts on row contents, not just on table existence.
func TestUpgradeFromLegacyBrunoTablesPreservesRows(t *testing.T) {
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
		t.Fatalf("upgrade store: %v", err)
	}
	defer store.Close()

	var (
		artifactSHA  string
		artifactSize int64
	)
	if err := store.db.QueryRow(
		`SELECT sha256, size_bytes FROM import_source_artifacts WHERE id = ?`, "artifact-1",
	).Scan(&artifactSHA, &artifactSize); err != nil {
		t.Fatalf("artifact did not survive the rename: %v", err)
	}
	if artifactSHA != "abc123" || artifactSize != 42 {
		t.Fatalf("artifact row changed: sha=%q size=%d", artifactSHA, artifactSize)
	}

	var rawArtifactID, sourceFormat string
	if err := store.db.QueryRow(
		`SELECT raw_artifact_id, source_format FROM import_source_revisions WHERE id = ?`, "revision-1",
	).Scan(&rawArtifactID, &sourceFormat); err != nil {
		t.Fatalf("revision did not survive the rename: %v", err)
	}
	if rawArtifactID != "artifact-1" || sourceFormat != "bruno_native" {
		t.Fatalf("revision row changed: raw=%q format=%q", rawArtifactID, sourceFormat)
	}

	var idempotencyKey string
	if err := store.db.QueryRow(
		`SELECT idempotency_key FROM import_batches WHERE id = ?`, "batch-1",
	).Scan(&idempotencyKey); err != nil {
		t.Fatalf("receipt did not survive the rename: %v", err)
	}
	if idempotencyKey != "idem-1" {
		t.Fatalf("receipt row changed: key=%q", idempotencyKey)
	}

	for _, table := range []string{"bruno_source_artifacts", "bruno_source_revisions", "bruno_native_import_batches"} {
		exists, err := store.tableExists(table)
		if err != nil {
			t.Fatalf("check %s: %v", table, err)
		}
		if exists {
			t.Fatalf("legacy table %s still exists after the rename", table)
		}
	}

	// The legacy indexes must be gone rather than sitting alongside the new
	// ones, or every write to these tables pays for two copies of each index.
	for _, index := range legacyImportIndexNames() {
		var count int
		if err := store.db.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, index,
		).Scan(&count); err != nil {
			t.Fatalf("check index %s: %v", index, err)
		}
		if count != 0 {
			t.Fatalf("legacy index %s survived the rename", index)
		}
	}
}

// TestUpgradeFromLegacyBrunoTablesIsIdempotent covers the ordinary restart: the
// same binary starting twice against an already-migrated database must not fail
// or touch anything.
func TestUpgradeFromLegacyBrunoTablesIsIdempotent(t *testing.T) {
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
		t.Fatalf("second open must be a no-op, got: %v", err)
	}
	defer second.Close()

	var artifacts int
	if err := second.db.QueryRow(`SELECT COUNT(*) FROM import_source_artifacts`).Scan(&artifacts); err != nil {
		t.Fatalf("count artifacts: %v", err)
	}
	if artifacts != 1 {
		t.Fatalf("artifact count = %d after second open, want 1", artifacts)
	}
}

// TestFreshDatabaseSkipsTheRename pins that a new install never executes the
// migration, so the rename cannot become a startup cost or a failure mode for
// databases that never held the old names.
func TestFreshDatabaseSkipsTheRename(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "fresh.db"))
	if err != nil {
		t.Fatalf("fresh store: %v", err)
	}
	defer store.Close()

	for _, table := range []string{"import_source_artifacts", "import_source_revisions", "import_batches"} {
		exists, err := store.tableExists(table)
		if err != nil {
			t.Fatalf("check %s: %v", table, err)
		}
		if !exists {
			t.Fatalf("fresh database is missing %s", table)
		}
	}
	for _, rename := range legacyImportTableRenames() {
		exists, err := store.tableExists(rename.from)
		if err != nil {
			t.Fatalf("check %s: %v", rename.from, err)
		}
		if exists {
			t.Fatalf("fresh database unexpectedly has legacy table %s", rename.from)
		}
	}
}

// TestRenameFailsClosedWhenBothTablesExist pins the ambiguous case. Choosing
// either table would silently discard the rows in the other, so the migration
// refuses and asks for an operator instead.
func TestRenameFailsClosedWhenBothTablesExist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conflict.db")

	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	seedLegacyBrunoSchema(t, raw)
	if _, err := raw.Exec(`CREATE TABLE import_source_artifacts (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("seed conflicting table: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw db: %v", err)
	}

	store, err := NewSQLiteStore(path)
	if err == nil {
		store.Close()
		t.Fatal("expected the rename to fail closed when both tables exist")
	}
	if got := err.Error(); !strings.Contains(got, "both tables exist") {
		t.Fatalf("error should explain the conflict, got: %v", got)
	}
}
