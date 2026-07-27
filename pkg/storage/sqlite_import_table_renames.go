package storage

import (
	"fmt"
)

// The protected-import artifact, revision, and receipt tables were introduced
// for the Bruno importer and carried Bruno-specific names. They now hold rows
// for every format routed through the shared import commit service, so the
// names were changed to format-neutral ones alongside the pkg/importartifact
// rename.
//
// Renaming a table that already holds encrypted source artifacts and commit
// receipts is the one part of that change that can lose data, so it is done
// here rather than as a CREATE-time concern. Two properties matter:
//
//  1. This runs BEFORE schemaCreateTableStatements(). Those statements are
//     CREATE TABLE IF NOT EXISTS against the *new* names. If they ran first on
//     an upgraded database they would create empty new tables, the rename below
//     would then be skipped as "already migrated", and every existing artifact
//     and receipt would be stranded in the old tables with nothing reading them.
//     The ordering is the correctness guarantee, not an optimization.
//
//  2. Each rename is skipped unless the old table exists and the new one does
//     not. That makes the migration idempotent and makes a fresh database a
//     no-op, so the same init() path serves both new and upgraded installs.
//
// Both SQLite (3.25+) and PostgreSQL update foreign-key references to a renamed
// table automatically, so the artifacts -> revisions references survive without
// being rewritten by hand. The renames are ordered parent-before-child anyway so
// the intent is legible in a schema dump.

type importTableRename struct {
	from string
	to   string
}

// legacyImportTableRenames lists the Bruno-era table names and their neutral
// replacements. Entries must never be removed: a database that skipped several
// releases still needs the rename on first start under a newer binary.
func legacyImportTableRenames() []importTableRename {
	return []importTableRename{
		{from: "bruno_source_artifacts", to: "import_source_artifacts"},
		{from: "bruno_source_revisions", to: "import_source_revisions"},
		{from: "bruno_native_import_batches", to: "import_batches"},
	}
}

// legacyImportIndexNames lists indexes created under the old naming scheme.
//
// A renamed table keeps its existing indexes under their original names, and
// the schemaIndexes list then creates identically-shaped indexes under the new
// names. Without this drop an upgraded database would carry two copies of every
// index on these tables: correct, but paying double the write cost forever.
func legacyImportIndexNames() []string {
	return []string{
		"idx_bruno_artifacts_project_hash",
		"idx_bruno_revisions_script_created",
		"idx_bruno_revisions_project_active",
		"idx_bruno_revisions_one_active",
		"idx_bruno_native_batches_project_created",
	}
}

// tableExists reports whether a table is present in the current schema.
//
// The catalog is one of the few places the two dialects cannot be bridged by
// rewriteSQL, because the catalog relation itself differs rather than just the
// syntax around it.
func (s *SQLiteStore) tableExists(name string) (bool, error) {
	query := `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`
	if s.db.dialect == storeDialectPostgres {
		query = `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = CURRENT_SCHEMA() AND table_name = ?`
	}
	var count int
	if err := s.db.QueryRow(query, name).Scan(&count); err != nil {
		return false, fmt.Errorf("failed to check whether table %s exists: %w", name, err)
	}
	return count > 0, nil
}

// backfillSanitizedSourceAvailability marks pre-existing revisions as having a
// real sanitized document.
//
// Every revision written before the sanitized_available column existed came from
// the Bruno importer, which always produces two distinct artifacts -- a raw
// bundle and a separately sanitized one -- so its two artifact columns never
// hold the same id. Formats that retain source raw-only satisfy the NOT NULL
// constraint by pointing both columns at the same raw artifact.
//
// That difference is the discriminator, which makes this safe to run on every
// start rather than once: a raw-only revision can never match, so repeated runs
// cannot promote one into an editor-visible sanitized download. A flag-based
// "run once" migration would have to be trusted not to re-run; this one does not
// need to be trusted at all.
func (s *SQLiteStore) backfillSanitizedSourceAvailability() error {
	if _, err := s.db.Exec(`UPDATE import_source_revisions SET sanitized_available = 1
		WHERE sanitized_available = 0 AND sanitized_artifact_id <> raw_artifact_id`); err != nil {
		return fmt.Errorf("failed to backfill import_source_revisions.sanitized_available: %w", err)
	}
	return nil
}

// renameLegacyImportTables migrates a pre-rename database in place. It is a
// no-op on a fresh database and safe to run on every start.
func (s *SQLiteStore) renameLegacyImportTables() error {
	for _, rename := range legacyImportTableRenames() {
		oldExists, err := s.tableExists(rename.from)
		if err != nil {
			return err
		}
		if !oldExists {
			continue
		}
		newExists, err := s.tableExists(rename.to)
		if err != nil {
			return err
		}
		if newExists {
			// Both names present. This is not a state the migration can
			// resolve on its own: picking either table silently discards the
			// rows in the other. Fail closed and let an operator decide.
			return fmt.Errorf(
				"cannot rename %s to %s: both tables exist, which suggests a partially applied migration; "+
					"inspect both and drop or merge one before restarting",
				rename.from, rename.to,
			)
		}
		if _, err := s.db.Exec(fmt.Sprintf(`ALTER TABLE %s RENAME TO %s`, rename.from, rename.to)); err != nil {
			return fmt.Errorf("failed to rename table %s to %s: %w", rename.from, rename.to, err)
		}
	}
	for _, index := range legacyImportIndexNames() {
		if _, err := s.db.Exec(fmt.Sprintf(`DROP INDEX IF EXISTS %s`, index)); err != nil {
			return fmt.Errorf("failed to drop legacy index %s: %w", index, err)
		}
	}
	return nil
}
