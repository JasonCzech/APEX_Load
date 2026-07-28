package storage

import (
	"database/sql"
	"fmt"
	"strings"
)

// Import persistence keeps the Bruno-era physical table names even though the
// Go contracts are importer-neutral. Older controller binaries issue SQL
// directly against these names, so changing them in place breaks rolling
// upgrades and makes a binary rollback unable to read source artifacts and
// commit receipts.
type importPhysicalTable struct {
	legacy  string
	renamed string
}

func importPhysicalTables() []importPhysicalTable {
	return []importPhysicalTable{
		{legacy: "bruno_source_artifacts", renamed: "import_source_artifacts"},
		{legacy: "bruno_source_revisions", renamed: "import_source_revisions"},
		{legacy: "bruno_native_import_batches", renamed: "import_batches"},
	}
}

// tableExists reports whether a physical table is present in the current
// schema. Views intentionally do not count: the storage contract is about the
// durable table identity used by older binaries and foreign keys.
type importTableCatalogQueryer interface {
	QueryRow(query string, args ...any) *sql.Row
}

func importTableExists(queryer importTableCatalogQueryer, dialect storeDialect, name string) (bool, error) {
	query := `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`
	if dialect == storeDialectPostgres {
		query = `SELECT COUNT(*) FROM information_schema.tables
			WHERE table_schema = CURRENT_SCHEMA() AND table_type = 'BASE TABLE' AND table_name = ?`
	}
	var count int
	if err := queryer.QueryRow(query, name).Scan(&count); err != nil {
		return false, fmt.Errorf("failed to check whether table %s exists: %w", name, err)
	}
	return count > 0, nil
}

func (s *SQLiteStore) tableExists(name string) (bool, error) {
	return importTableExists(s.db, s.db.dialect, name)
}

type importPhysicalTableFamily struct {
	existing map[string]bool
	rows     int64
}

func inspectImportPhysicalTableFamily(
	queryer importTableCatalogQueryer,
	dialect storeDialect,
	legacyNames bool,
) (importPhysicalTableFamily, error) {
	family := importPhysicalTableFamily{existing: make(map[string]bool, len(importPhysicalTables()))}
	for _, pair := range importPhysicalTables() {
		name := pair.renamed
		if legacyNames {
			name = pair.legacy
		}
		exists, err := importTableExists(queryer, dialect, name)
		if err != nil {
			return importPhysicalTableFamily{}, err
		}
		family.existing[name] = exists
		if !exists {
			continue
		}
		var count int64
		if err := queryer.QueryRow(fmt.Sprintf(`SELECT COUNT(*) FROM %s`, name)).Scan(&count); err != nil {
			return importPhysicalTableFamily{}, fmt.Errorf("count import table %s: %w", name, err)
		}
		family.rows += count
	}
	return family, nil
}

func (family importPhysicalTableFamily) anyExists() bool {
	for _, exists := range family.existing {
		if exists {
			return true
		}
	}
	return false
}

// dropImportPhysicalTableFamily removes an empty compatibility family in
// foreign-key-safe child-to-parent order.
func dropImportPhysicalTableFamily(tx *storeTx, legacyNames bool) error {
	pairs := importPhysicalTables()
	for _, index := range []int{1, 2, 0} {
		name := pairs[index].renamed
		if legacyNames {
			name = pairs[index].legacy
		}
		if _, err := tx.Exec(fmt.Sprintf(`DROP TABLE IF EXISTS %s`, name)); err != nil {
			return fmt.Errorf("drop empty import compatibility table %s: %w", name, err)
		}
	}
	return nil
}

var renamedImportIndexNames = []string{
	"idx_import_artifacts_project_hash",
	"idx_import_revisions_script_created",
	"idx_import_revisions_project_active",
	"idx_import_revisions_one_active",
	"idx_import_batches_project_created",
}

// ensureCompatibleImportPhysicalTableLayout runs before CREATE TABLE
// statements. A previously shipped controller renamed these tables to
// importer-neutral names. The public storage contract subsequently returned to
// the original Bruno-era physical names so rolling downgrades could continue to
// read receipts and encrypted artifacts.
//
// A database that was opened by the short-lived renamed release must remain a
// normal online upgrade, not a controller-startup outage. Rename every
// generic-only table back in one transaction. A rollback through an older
// binary can create a second, empty Bruno family beside a populated generic
// family (and the inverse is possible after another upgrade). An entirely
// empty family is safe to drop before retaining or renaming the populated
// family. If both families contain rows, fail before changing anything because
// choosing either side could discard durable data. PostgreSQL takes a
// transaction-scoped advisory lock so concurrent HA replicas cannot race the
// catalog transition.
func (s *SQLiteStore) ensureCompatibleImportPhysicalTableLayout() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if s.db.dialect == storeDialectPostgres {
		// Stable application-local key; held only for this bounded schema check.
		if _, err := tx.Exec(`SELECT pg_advisory_xact_lock(73194721018461)`); err != nil {
			return fmt.Errorf("lock import physical-table compatibility migration: %w", err)
		}
	}

	legacyFamily, err := inspectImportPhysicalTableFamily(tx, s.db.dialect, true)
	if err != nil {
		return err
	}
	renamedFamily, err := inspectImportPhysicalTableFamily(tx, s.db.dialect, false)
	if err != nil {
		return err
	}
	if legacyFamily.rows > 0 && renamedFamily.rows > 0 {
		return fmt.Errorf(
			"import storage has both populated Bruno and generic physical table families; refusing to choose between durable rows",
		)
	}

	switch {
	case renamedFamily.rows > 0:
		if legacyFamily.anyExists() {
			if err := dropImportPhysicalTableFamily(tx, true); err != nil {
				return err
			}
		}
	case legacyFamily.rows > 0:
		if renamedFamily.anyExists() {
			if err := dropImportPhysicalTableFamily(tx, false); err != nil {
				return err
			}
		}
	default:
		// Prefer the stable Bruno physical contract when both families are
		// empty, including partial layouts left by interrupted old startups.
		if renamedFamily.anyExists() {
			if err := dropImportPhysicalTableFamily(tx, false); err != nil {
				return err
			}
		}
	}

	if renamedFamily.rows > 0 {
		for _, table := range importPhysicalTables() {
			renamedExists := renamedFamily.existing[table.renamed]
			if !renamedExists {
				return fmt.Errorf(
					"populated generic import storage is missing required table %s",
					table.renamed,
				)
			}
			if _, err := tx.Exec(fmt.Sprintf(
				`ALTER TABLE %s RENAME TO %s`,
				table.renamed,
				table.legacy,
			)); err != nil {
				return fmt.Errorf(
					"restore compatible import table name %s from %s: %w",
					table.legacy,
					table.renamed,
					err,
				)
			}
		}
	}
	for _, index := range renamedImportIndexNames {
		if _, err := tx.Exec(fmt.Sprintf(`DROP INDEX IF EXISTS %s`, index)); err != nil {
			return fmt.Errorf("remove superseded import index %s: %w", index, err)
		}
	}
	return tx.Commit()
}

// repairLegacyImportForeignKeyTargets fixes SQLite schemas produced by the
// short-lived generic import-table migration when it was reversed with
// legacy_alter_table enabled. In that state the physical tables have their
// compatible Bruno names again, but the child-table declarations still point
// at import_source_artifacts/import_source_revisions. SQLite resolves those
// missing parents even for a zero-row DELETE, so every script deletion fails
// and rolls back.
func (s *SQLiteStore) repairLegacyImportForeignKeyTargets() error {
	if s.db.dialect != storeDialectSQLite {
		return nil
	}
	revisionsBroken, err := s.importTableHasRenamedForeignKeyTarget(
		"bruno_source_revisions",
		"import_source_artifacts",
		"import_source_revisions",
	)
	if err != nil {
		return err
	}
	batchesBroken, err := s.importTableHasRenamedForeignKeyTarget(
		"bruno_native_import_batches",
		"import_source_artifacts",
	)
	if err != nil {
		return err
	}
	if !revisionsBroken && !batchesBroken {
		return nil
	}

	if _, err := s.db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		return fmt.Errorf("disable foreign keys for import schema repair: %w", err)
	}
	defer func() {
		_, _ = s.db.Exec(`PRAGMA foreign_keys = ON`)
	}()
	if _, err := s.db.Exec(`PRAGMA legacy_alter_table = ON`); err != nil {
		return fmt.Errorf("enable legacy alter-table behavior for import schema repair: %w", err)
	}
	defer func() {
		_, _ = s.db.Exec(`PRAGMA legacy_alter_table = OFF`)
	}()

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin import foreign-key repair: %w", err)
	}
	defer tx.Rollback()

	if revisionsBroken {
		for _, statement := range []string{
			`CREATE TABLE bruno_source_revisions_fk_repair (
				id TEXT PRIMARY KEY,
				script_id TEXT NOT NULL REFERENCES scripts(id),
				project_id TEXT NOT NULL REFERENCES projects(id),
				parent_revision_id TEXT REFERENCES bruno_source_revisions(id),
				active_flag INTEGER NOT NULL DEFAULT 0,
				raw_artifact_id TEXT NOT NULL REFERENCES bruno_source_artifacts(id),
				sanitized_artifact_id TEXT NOT NULL REFERENCES bruno_source_artifacts(id),
				sanitized_available INTEGER NOT NULL DEFAULT 0,
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
			`INSERT INTO bruno_source_revisions_fk_repair (
				id, script_id, project_id, parent_revision_id, active_flag,
				raw_artifact_id, sanitized_artifact_id, sanitized_available,
				source_format, compatibility_version, identity_map_json,
				import_options_json, dependency_snapshot_ref, security_profile_json,
				change_summary_json, secret_bindings_json, canonical_script_json,
				created_by, created_at
			) SELECT
				id, script_id, project_id, parent_revision_id, active_flag,
				raw_artifact_id, sanitized_artifact_id, sanitized_available,
				source_format, compatibility_version, identity_map_json,
				import_options_json, dependency_snapshot_ref, security_profile_json,
				change_summary_json, secret_bindings_json, canonical_script_json,
				created_by, created_at
			FROM bruno_source_revisions`,
			`DROP TABLE bruno_source_revisions`,
			`ALTER TABLE bruno_source_revisions_fk_repair RENAME TO bruno_source_revisions`,
			`CREATE INDEX idx_bruno_revisions_script_created ON bruno_source_revisions(script_id, created_at)`,
			`CREATE INDEX idx_bruno_revisions_project_active ON bruno_source_revisions(project_id, active_flag)`,
			`CREATE UNIQUE INDEX idx_bruno_revisions_one_active ON bruno_source_revisions(script_id, project_id) WHERE active_flag = 1`,
		} {
			if _, err := tx.Exec(statement); err != nil {
				return fmt.Errorf("repair bruno_source_revisions foreign keys: %w", err)
			}
		}
	}
	if batchesBroken {
		for _, statement := range []string{
			`CREATE TABLE bruno_native_import_batches_fk_repair (
				id TEXT PRIMARY KEY,
				project_id TEXT NOT NULL REFERENCES projects(id),
				source_profile TEXT NOT NULL DEFAULT 'bruno_native',
				receipt_version INTEGER NOT NULL DEFAULT 1,
				idempotency_key TEXT NOT NULL,
				request_digest_profile TEXT NOT NULL DEFAULT 'bruno-native-commit-v1',
				request_sha256 TEXT NOT NULL,
				raw_artifact_id TEXT NOT NULL REFERENCES bruno_source_artifacts(id),
				sanitized_artifact_id TEXT NOT NULL REFERENCES bruno_source_artifacts(id),
				candidate_ids_json TEXT NOT NULL,
				script_ids_json TEXT NOT NULL,
				scripts_json TEXT NOT NULL DEFAULT '[]',
				created_by TEXT,
				created_at DATETIME NOT NULL,
				UNIQUE(project_id, idempotency_key)
			)`,
			`INSERT INTO bruno_native_import_batches_fk_repair (
				id, project_id, source_profile, receipt_version, idempotency_key,
				request_digest_profile, request_sha256, raw_artifact_id,
				sanitized_artifact_id, candidate_ids_json, script_ids_json,
				scripts_json, created_by, created_at
			) SELECT
				id, project_id, source_profile, receipt_version, idempotency_key,
				request_digest_profile, request_sha256, raw_artifact_id,
				sanitized_artifact_id, candidate_ids_json, script_ids_json,
				scripts_json, created_by, created_at
			FROM bruno_native_import_batches`,
			`DROP TABLE bruno_native_import_batches`,
			`ALTER TABLE bruno_native_import_batches_fk_repair RENAME TO bruno_native_import_batches`,
			`CREATE INDEX idx_bruno_native_batches_project_created ON bruno_native_import_batches(project_id, created_at)`,
		} {
			if _, err := tx.Exec(statement); err != nil {
				return fmt.Errorf("repair bruno_native_import_batches foreign keys: %w", err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit import foreign-key repair: %w", err)
	}
	if _, err := s.db.Exec(`PRAGMA legacy_alter_table = OFF`); err != nil {
		return fmt.Errorf("restore alter-table behavior after import schema repair: %w", err)
	}
	if _, err := s.db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		return fmt.Errorf("restore foreign keys after import schema repair: %w", err)
	}
	var foreignKeysEnabled int
	if err := s.db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeysEnabled); err != nil {
		return fmt.Errorf("verify foreign keys after import schema repair: %w", err)
	}
	if foreignKeysEnabled != 1 {
		return fmt.Errorf("foreign keys remained disabled after import schema repair")
	}
	return nil
}

func (s *SQLiteStore) importTableHasRenamedForeignKeyTarget(table string, targets ...string) (bool, error) {
	rows, err := s.db.Query(`PRAGMA foreign_key_list(` + table + `)`)
	if err != nil {
		return false, fmt.Errorf("inspect %s foreign keys: %w", table, err)
	}
	defer rows.Close()
	renamed := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		renamed[strings.ToLower(strings.TrimSpace(target))] = struct{}{}
	}
	for rows.Next() {
		var id, sequence int
		var target, from, to, onUpdate, onDelete, match string
		if err := rows.Scan(&id, &sequence, &target, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			return false, fmt.Errorf("scan %s foreign key: %w", table, err)
		}
		if _, broken := renamed[strings.ToLower(strings.TrimSpace(target))]; broken {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("inspect %s foreign keys: %w", table, err)
	}
	return false, nil
}

// backfillSanitizedSourceAvailability marks pre-existing revisions as having a
// real sanitized document.
//
// Every revision written before the sanitized_available column existed came
// from the Bruno importer, which always produced distinct raw and sanitized
// artifacts. Formats that retain source raw-only point both columns at the same
// raw artifact and therefore never match this bounded, idempotent backfill.
func (s *SQLiteStore) backfillSanitizedSourceAvailability() error {
	if _, err := s.db.Exec(`UPDATE bruno_source_revisions SET sanitized_available = 1
		WHERE sanitized_available = 0 AND sanitized_artifact_id <> raw_artifact_id`); err != nil {
		return fmt.Errorf("failed to backfill bruno_source_revisions.sanitized_available: %w", err)
	}
	return nil
}
