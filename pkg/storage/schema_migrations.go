package storage

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"time"
)

const schemaMigrationLedgerDDL = `CREATE TABLE IF NOT EXISTS schema_migrations (
	version INTEGER PRIMARY KEY,
	name TEXT NOT NULL UNIQUE,
	fingerprint TEXT NOT NULL,
	applied_at DATETIME NOT NULL
)`

type schemaRelease struct {
	Version     int
	Name        string
	Fingerprint string
}

// schemaReleases is append-only. A schema DDL, optional migration, index, or
// named convergence phase change must add a release; changing a released
// fingerprint makes existing databases fail closed instead of claiming that a
// different schema is the same version.
var schemaReleases = []schemaRelease{
	{Version: 1, Name: "idempotent-schema-baseline", Fingerprint: "60c5d967d6f0543361fa8842fac8a0e157b118bcef49d2c6310a7cee19a15832"},
	{Version: 2, Name: "cloudtest-outcome-parity-foundations", Fingerprint: "9a70267c0b98361c2563e0339c17b3eaa1d5595ef6fea06b9f94eb62c696fb28"},
	{Version: 3, Name: "shared-dashboard-definitions", Fingerprint: "59f38c09909ddb93e6e1ad362595a5436a9942fe8befd861ba9ed4636759d3ad"},
	{Version: 4, Name: "domain-feature-access-controls", Fingerprint: "86c06ad55fe5e62f1e8e9956738a8b42080e5a63988c9b2903e1f03b9eb0a13a"},
	{Version: 5, Name: "production-traffic-profiles", Fingerprint: "6d3bff068cfb95b5ac82283cc3794af6941f3d3f9df9c4c329d3fdc9a2133ada"},
	{Version: 6, Name: "production-traffic-run-snapshots", Fingerprint: "b77be774c8f129230d348c391e3cf34ab356f7900446bf9244f09e3d852ff462"},
	{Version: 7, Name: "scenario-group-transaction-response-time-sla", Fingerprint: "2b27fdaf2af24996243a8aa5d67e97b01cbe81515ce391653da7f744df80ad2e"},
	{Version: 8, Name: "peak-pod-capacity-reservations", Fingerprint: "3c72a2e09f9362c4167a2f804a8c3d1b8a275bf96ce98822705f67d1ef4b11a1"},
}

type SchemaMigrationRecord struct {
	Version     int       `json:"version"`
	Name        string    `json:"name"`
	Fingerprint string    `json:"fingerprint"`
	AppliedAt   time.Time `json:"applied_at"`
}

type SchemaStatus struct {
	CurrentVersion int                     `json:"current_version"`
	AppliedVersion int                     `json:"applied_version"`
	Applied        []SchemaMigrationRecord `json:"applied"`
	Pending        []SchemaMigrationRecord `json:"pending"`
}

func currentSchemaRelease() schemaRelease {
	return schemaReleases[len(schemaReleases)-1]
}

func currentSchemaVersionLabel() string {
	return fmt.Sprintf("apexload.schema.v%d", currentSchemaRelease().Version)
}

func schemaConvergenceFingerprint() string {
	hash := sha256.New()
	write := func(kind, name, query string) {
		_, _ = fmt.Fprintf(hash, "%s\x00%s\x00%s\x00", kind, name, strings.TrimSpace(query))
	}
	write("ledger", "schema_migrations", schemaMigrationLedgerDDL)
	tableIndex := 0
	for _, query := range schemaCreateTableStatements() {
		if query == schemaMigrationLedgerDDL {
			continue
		}
		write("table", fmt.Sprintf("%06d", tableIndex), query)
		tableIndex++
	}
	for _, migration := range schemaColumnMigrations() {
		write("column", migration.name, migration.query)
	}
	for _, index := range schemaIndexes {
		write("index", index.name, index.query)
	}
	// These named phases mutate or constrain schema state outside the three SQL
	// registries above. Names are intentionally fingerprinted so adding/removing
	// a phase requires a schema release even when its SQL is built dynamically.
	for _, phase := range []string{
		"compatible-import-physical-layout",
		"postgres-artifact-size-bigint",
		"kubernetes-execution-target-backfill",
		"execution-target-enrollment-expiry-backfill",
		"postgres-scheduler-generation-bigint",
		"release-group-backfill",
		"scheduler-content-revision",
		"start-admission-revision",
		"data-pool-paging-state",
		"legacy-import-foreign-key-repair",
		"sanitized-source-availability",
		"github-check-link-kind",
		"blank-script-id",
		"legacy-script-recording-repair",
		"correlation-ruleset-repair",
		"archive-finalization-intent-token",
		"github-remote-blob-sha",
		"github-sync-target-owner",
		"fleet-pool-membership-consolidation",
		"interval-and-archive-schema-version",
		"default-domain-project-application",
		"postgres-foreign-keys",
		"timescaledb",
	} {
		write("phase", phase, "")
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func (s *SQLiteStore) ensureSchemaMigrationLedger() error {
	if s == nil || s.db == nil {
		return fmt.Errorf("storage is not initialized")
	}
	if _, err := s.db.Exec(schemaMigrationLedgerDDL); err != nil {
		return fmt.Errorf("create schema migration ledger: %w", err)
	}
	status, err := s.SchemaStatus()
	if err != nil {
		return err
	}
	current := currentSchemaRelease()
	if status.AppliedVersion > current.Version {
		return fmt.Errorf("database schema version %d is newer than supported version %d", status.AppliedVersion, current.Version)
	}
	for index, release := range schemaReleases {
		if release.Version != index+1 || strings.TrimSpace(release.Name) == "" || strings.TrimSpace(release.Fingerprint) == "" {
			return fmt.Errorf("schema release registry must be contiguous and complete: index %d has version %d", index, release.Version)
		}
	}
	for _, applied := range status.Applied {
		if applied.Version <= 0 || applied.Version > len(schemaReleases) {
			return fmt.Errorf("schema migration ledger contains unknown version %d", applied.Version)
		}
		expected := schemaReleases[applied.Version-1]
		if applied.Name != expected.Name || applied.Fingerprint != expected.Fingerprint {
			return fmt.Errorf("schema migration %d identity mismatch: found %q/%s, want %q/%s", applied.Version, applied.Name, applied.Fingerprint, expected.Name, expected.Fingerprint)
		}
	}
	return nil
}

func (s *SQLiteStore) recordSchemaReleases() error {
	for _, release := range schemaReleases {
		if _, err := s.db.Exec(`INSERT INTO schema_migrations (version, name, fingerprint, applied_at)
			VALUES (?, ?, ?, ?) ON CONFLICT(version) DO NOTHING`, release.Version, release.Name, release.Fingerprint, time.Now().UTC()); err != nil {
			return fmt.Errorf("record schema migration %d: %w", release.Version, err)
		}
	}
	status, err := s.SchemaStatus()
	if err != nil {
		return err
	}
	if len(status.Pending) != 0 {
		return fmt.Errorf("schema convergence left %d migration releases pending", len(status.Pending))
	}
	for _, applied := range status.Applied {
		expected := schemaReleases[applied.Version-1]
		if applied.Name != expected.Name || applied.Fingerprint != expected.Fingerprint {
			return fmt.Errorf("schema migration %d changed after it was applied", applied.Version)
		}
	}
	return nil
}

// SchemaStatus inspects the ordered migration ledger without mutating it.
func (s *SQLiteStore) SchemaStatus() (SchemaStatus, error) {
	status := SchemaStatus{CurrentVersion: currentSchemaRelease().Version}
	if s == nil || s.db == nil {
		return status, fmt.Errorf("storage is not initialized")
	}
	rows, err := s.db.Query(`SELECT version, name, fingerprint, applied_at FROM schema_migrations ORDER BY version`)
	if err != nil {
		return status, fmt.Errorf("read schema migration ledger: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var record SchemaMigrationRecord
		if err := rows.Scan(&record.Version, &record.Name, &record.Fingerprint, &record.AppliedAt); err != nil {
			return status, fmt.Errorf("scan schema migration ledger: %w", err)
		}
		status.Applied = append(status.Applied, record)
		if record.Version > status.AppliedVersion {
			status.AppliedVersion = record.Version
		}
	}
	if err := rows.Err(); err != nil {
		return status, fmt.Errorf("read schema migration ledger: %w", err)
	}
	appliedVersions := make(map[int]bool, len(status.Applied))
	for _, applied := range status.Applied {
		appliedVersions[applied.Version] = true
	}
	for _, release := range schemaReleases {
		if !appliedVersions[release.Version] {
			status.Pending = append(status.Pending, SchemaMigrationRecord{
				Version: release.Version, Name: release.Name, Fingerprint: release.Fingerprint,
			})
		}
	}
	return status, nil
}

func (s *PostgresStore) SchemaStatus() (SchemaStatus, error) {
	if s == nil || s.SQLiteStore == nil {
		return SchemaStatus{}, fmt.Errorf("postgres store is not initialized")
	}
	return s.SQLiteStore.SchemaStatus()
}
