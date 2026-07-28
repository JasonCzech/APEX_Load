package storage

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
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

func TestBrokenReverseMigrationForeignKeysAreRepairedForScriptDelete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broken-reverse-migration.db")
	initial, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("create initial store: %v", err)
	}
	if err := initial.Close(); err != nil {
		t.Fatalf("close initial store: %v", err)
	}

	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	if _, err := raw.Exec(`PRAGMA legacy_alter_table = OFF`); err != nil {
		t.Fatalf("disable legacy alter table: %v", err)
	}
	for _, table := range importPhysicalTables() {
		if _, err := raw.Exec(`ALTER TABLE ` + table.legacy + ` RENAME TO ` + table.renamed); err != nil {
			t.Fatalf("rename %s to %s: %v", table.legacy, table.renamed, err)
		}
	}
	if _, err := raw.Exec(`PRAGMA legacy_alter_table = ON`); err != nil {
		t.Fatalf("enable legacy alter table: %v", err)
	}
	for _, table := range importPhysicalTables() {
		if _, err := raw.Exec(`ALTER TABLE ` + table.renamed + ` RENAME TO ` + table.legacy); err != nil {
			t.Fatalf("reverse rename %s to %s: %v", table.renamed, table.legacy, err)
		}
	}
	now := time.Now().UTC()
	if _, err := raw.Exec(`INSERT INTO bruno_source_artifacts (
		id, project_id, artifact_kind, backend_key, sha256, size_bytes,
		encryption_json, lifecycle_state, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"repair-artifact", "project-1", "raw", "repair/key", "repair-sha", 12,
		`{"alg":"test"}`, "active", now,
	); err != nil {
		t.Fatalf("seed artifact in broken schema: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO bruno_source_revisions (
		id, script_id, project_id, active_flag, raw_artifact_id,
		sanitized_artifact_id, sanitized_available, source_format,
		compatibility_version, identity_map_json, import_options_json,
		security_profile_json, change_summary_json, secret_bindings_json,
		canonical_script_json, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"repair-revision", "source-script", "project-1", 1, "repair-artifact",
		"repair-artifact", 0, "bruno_native", "v1", `{}`, `{}`, `{}`, `{}`, `{}`, `{}`, now,
	); err != nil {
		t.Fatalf("seed revision in broken schema: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close broken raw db: %v", err)
	}

	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("open repaired store: %v", err)
	}
	defer store.Close()
	var foreignKeysEnabled int
	if err := store.db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeysEnabled); err != nil {
		t.Fatalf("read foreign_keys after repair: %v", err)
	}
	if foreignKeysEnabled != 1 {
		t.Fatalf("foreign_keys after repair = %d, want 1", foreignKeysEnabled)
	}

	for _, table := range []string{"bruno_source_revisions", "bruno_native_import_batches"} {
		broken, err := store.importTableHasRenamedForeignKeyTarget(
			table,
			"import_source_artifacts",
			"import_source_revisions",
		)
		if err != nil {
			t.Fatalf("inspect repaired %s: %v", table, err)
		}
		if broken {
			t.Fatalf("%s still references removed generic import tables", table)
		}
	}
	var repairedArtifactID string
	if err := store.db.QueryRow(
		`SELECT raw_artifact_id FROM bruno_source_revisions WHERE id = ?`,
		"repair-revision",
	).Scan(&repairedArtifactID); err != nil {
		t.Fatalf("read revision preserved by repair: %v", err)
	}
	if repairedArtifactID != "repair-artifact" {
		t.Fatalf("preserved revision artifact = %q, want repair-artifact", repairedArtifactID)
	}

	script := &dsl.Script{ID: "delete-after-fk-repair", Name: "Delete after FK repair", Protocol: "http"}
	if err := store.SaveScript(script); err != nil {
		t.Fatalf("save script after repair: %v", err)
	}
	stored, err := store.GetScript(script.ID)
	if err != nil {
		t.Fatalf("get script after repair: %v", err)
	}
	deleted, err := store.DeleteScriptIfUnchanged(stored.ID, stored.UpdatedAt)
	if err != nil {
		t.Fatalf("delete script after repair: %v", err)
	}
	if !deleted {
		t.Fatal("delete script after repair reported no deletion")
	}
}

func seedRenamedGenericImportSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	seedLegacyBrunoSchema(t, db)
	for _, table := range importPhysicalTables() {
		if _, err := db.Exec(`ALTER TABLE ` + table.legacy + ` RENAME TO ` + table.renamed); err != nil {
			t.Fatalf("rename %s to %s: %v", table.legacy, table.renamed, err)
		}
	}
	for _, index := range []string{
		"idx_bruno_artifacts_project_hash",
		"idx_bruno_revisions_script_created",
		"idx_bruno_revisions_project_active",
		"idx_bruno_revisions_one_active",
		"idx_bruno_native_batches_project_created",
	} {
		if _, err := db.Exec(`DROP INDEX IF EXISTS ` + index); err != nil {
			t.Fatalf("drop Bruno index %s: %v", index, err)
		}
	}
	statements := []string{
		`CREATE INDEX idx_import_artifacts_project_hash ON import_source_artifacts(project_id, artifact_kind, sha256)`,
		`CREATE INDEX idx_import_revisions_script_created ON import_source_revisions(script_id, created_at)`,
		`CREATE INDEX idx_import_revisions_project_active ON import_source_revisions(project_id, active_flag)`,
		`CREATE UNIQUE INDEX idx_import_revisions_one_active ON import_source_revisions(script_id, project_id) WHERE active_flag = 1`,
		`CREATE INDEX idx_import_batches_project_created ON import_batches(project_id, created_at)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("seed renamed index: %v\n%s", err, statement)
		}
	}
}

func seedEmptyImportCompatibilityFamily(t *testing.T, db *sql.DB, sourceLegacy bool) {
	t.Helper()
	for _, table := range importPhysicalTables() {
		source := table.renamed
		target := table.legacy
		if sourceLegacy {
			source, target = target, source
		}
		if _, err := db.Exec(`CREATE TABLE ` + target + ` AS SELECT * FROM ` + source + ` WHERE 0`); err != nil {
			t.Fatalf("seed empty compatibility table %s from %s: %v", target, source, err)
		}
	}
}

func TestRenamedImportTableLayoutIsReverseMigratedWithoutDataLoss(t *testing.T) {
	path := filepath.Join(t.TempDir(), "renamed.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	seedRenamedGenericImportSchema(t, raw)
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw db: %v", err)
	}

	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("open renamed import layout: %v", err)
	}
	defer store.Close()

	var artifactSHA string
	if err := store.db.QueryRow(
		`SELECT sha256 FROM bruno_source_artifacts WHERE id = ?`,
		"artifact-1",
	).Scan(&artifactSHA); err != nil {
		t.Fatalf("read migrated artifact: %v", err)
	}
	if artifactSHA != "abc123" {
		t.Fatalf("artifact sha = %q, want abc123", artifactSHA)
	}
	var revisionArtifactID string
	if err := store.db.QueryRow(
		`SELECT raw_artifact_id FROM bruno_source_revisions WHERE id = ?`,
		"revision-1",
	).Scan(&revisionArtifactID); err != nil {
		t.Fatalf("read migrated revision: %v", err)
	}
	if revisionArtifactID != "artifact-1" {
		t.Fatalf("revision artifact id = %q, want artifact-1", revisionArtifactID)
	}
	var receiptKey string
	if err := store.db.QueryRow(
		`SELECT idempotency_key FROM bruno_native_import_batches WHERE id = ?`,
		"batch-1",
	).Scan(&receiptKey); err != nil {
		t.Fatalf("read migrated receipt: %v", err)
	}
	if receiptKey != "idem-1" {
		t.Fatalf("receipt idempotency key = %q, want idem-1", receiptKey)
	}

	for _, table := range importPhysicalTables() {
		legacyExists, err := store.tableExists(table.legacy)
		if err != nil {
			t.Fatalf("check %s: %v", table.legacy, err)
		}
		renamedExists, err := store.tableExists(table.renamed)
		if err != nil {
			t.Fatalf("check %s: %v", table.renamed, err)
		}
		if !legacyExists || renamedExists {
			t.Fatalf("physical table pair %s/%s after migration: legacy=%t renamed=%t",
				table.legacy, table.renamed, legacyExists, renamedExists)
		}
	}
	for _, index := range renamedImportIndexNames {
		var count int
		if err := store.db.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`,
			index,
		).Scan(&count); err != nil {
			t.Fatalf("check renamed index %s: %v", index, err)
		}
		if count != 0 {
			t.Fatalf("renamed index %s survived reverse migration", index)
		}
	}
	var foreignKeyFailures int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&foreignKeyFailures); err != nil {
		t.Fatalf("check migrated foreign keys: %v", err)
	}
	if foreignKeyFailures != 0 {
		t.Fatalf("reverse migration left %d foreign-key failures", foreignKeyFailures)
	}
}

func TestPopulatedGenericImportFamilyReplacesEmptyBrunoFamily(t *testing.T) {
	path := filepath.Join(t.TempDir(), "generic-populated.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	seedRenamedGenericImportSchema(t, raw)
	seedEmptyImportCompatibilityFamily(t, raw, false)
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw db: %v", err)
	}

	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("open dual layout: %v", err)
	}
	defer store.Close()
	var sha string
	if err := store.db.QueryRow(`SELECT sha256 FROM bruno_source_artifacts WHERE id = 'artifact-1'`).Scan(&sha); err != nil {
		t.Fatalf("load retained generic artifact after rename: %v", err)
	}
	if sha != "abc123" {
		t.Fatalf("retained artifact sha = %q", sha)
	}
	for _, pair := range importPhysicalTables() {
		if exists, err := store.tableExists(pair.renamed); err != nil || exists {
			t.Fatalf("generic table %s survived: exists=%t err=%v", pair.renamed, exists, err)
		}
	}
}

func TestPopulatedBrunoImportFamilyDropsEmptyGenericFamily(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bruno-populated.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	seedLegacyBrunoSchema(t, raw)
	seedEmptyImportCompatibilityFamily(t, raw, true)
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw db: %v", err)
	}

	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("open dual layout: %v", err)
	}
	defer store.Close()
	var sha string
	if err := store.db.QueryRow(`SELECT sha256 FROM bruno_source_artifacts WHERE id = 'artifact-1'`).Scan(&sha); err != nil {
		t.Fatalf("load retained Bruno artifact: %v", err)
	}
	if sha != "abc123" {
		t.Fatalf("retained artifact sha = %q", sha)
	}
	for _, pair := range importPhysicalTables() {
		if exists, err := store.tableExists(pair.renamed); err != nil || exists {
			t.Fatalf("empty generic table %s survived: exists=%t err=%v", pair.renamed, exists, err)
		}
	}
}

func TestBothPopulatedImportTableFamiliesFailWithoutPartialMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ambiguous.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	seedRenamedGenericImportSchema(t, raw)
	seedEmptyImportCompatibilityFamily(t, raw, false)
	if _, err := raw.Exec(`INSERT INTO bruno_native_import_batches
		(id, project_id, idempotency_key, created_at)
		VALUES ('legacy-batch', 'project-1', 'legacy-key', ?)`, time.Now().UTC()); err != nil {
		t.Fatalf("seed populated conflicting table: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw db: %v", err)
	}

	store, err := NewSQLiteStore(path)
	if err == nil {
		store.Close()
		t.Fatal("expected ambiguous import layout to fail")
	}
	if got := err.Error(); !strings.Contains(got, "both") ||
		!strings.Contains(strings.ToLower(got), "populated") {
		t.Fatalf("error does not identify ambiguous tables: %v", err)
	}

	check, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("reopen raw db: %v", err)
	}
	defer check.Close()
	for _, name := range []string{"import_source_artifacts", "import_source_revisions", "import_batches"} {
		var count int
		if err := check.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`,
			name,
		).Scan(&count); err != nil {
			t.Fatalf("check %s: %v", name, err)
		}
		if count != 1 {
			t.Fatalf("failed migration partially renamed %s", name)
		}
	}
	for _, name := range []string{"bruno_source_artifacts", "bruno_source_revisions", "bruno_native_import_batches"} {
		var count int
		if err := check.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`,
			name,
		).Scan(&count); err != nil {
			t.Fatalf("check %s: %v", name, err)
		}
		if count != 1 {
			t.Fatalf("failed migration partially dropped %s", name)
		}
	}
}
