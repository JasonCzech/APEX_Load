package storage

import "fmt"

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
func (s *SQLiteStore) tableExists(name string) (bool, error) {
	query := `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`
	if s.db.dialect == storeDialectPostgres {
		query = `SELECT COUNT(*) FROM information_schema.tables
			WHERE table_schema = CURRENT_SCHEMA() AND table_type = 'BASE TABLE' AND table_name = ?`
	}
	var count int
	if err := s.db.QueryRow(query, name).Scan(&count); err != nil {
		return false, fmt.Errorf("failed to check whether table %s exists: %w", name, err)
	}
	return count > 0, nil
}

// validateImportPhysicalTableLayout runs before CREATE TABLE statements. A
// short-lived schema version renamed these tables to format-neutral names. Do
// not silently create empty legacy tables alongside such a database: that
// would strand encrypted artifacts and idempotency receipts. Migration of that
// already-renamed layout requires an explicit maintenance operation so no live
// controller loses its table while serving traffic.
func (s *SQLiteStore) validateImportPhysicalTableLayout() error {
	for _, table := range importPhysicalTables() {
		renamedExists, err := s.tableExists(table.renamed)
		if err != nil {
			return err
		}
		if !renamedExists {
			continue
		}
		legacyExists, err := s.tableExists(table.legacy)
		if err != nil {
			return err
		}
		if legacyExists {
			return fmt.Errorf(
				"import storage has both %s and %s; refusing to choose between two physical tables",
				table.legacy,
				table.renamed,
			)
		}
		return fmt.Errorf(
			"import storage uses unsupported renamed table %s; restore the compatible physical name %s during maintenance before starting this controller",
			table.renamed,
			table.legacy,
		)
	}
	return nil
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
