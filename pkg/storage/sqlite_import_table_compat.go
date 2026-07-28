package storage

import (
	"database/sql"
	"fmt"
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
