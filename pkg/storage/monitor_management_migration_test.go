package storage

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

var managementAddedColumns = map[string][]string{"run_monitor_collector_readiness": {"instance_id", "capabilities_json", "management_scopes_json"}, "run_monitor_jobs": {"source_id", "management_config_key"}, "run_monitor_batches": {"evidence_version", "management_snapshot_count", "management_row_count", "collection_generation", "management_poll_id"}}

func removeManagementFoundationSchema(t *testing.T, db *storeDB) {
	t.Helper()
	for _, q := range []string{`DROP INDEX idx_monitor_jobs_source`, `DROP INDEX idx_monitor_jobs_management`, `DROP INDEX idx_monitor_polls_source`, `DROP INDEX idx_monitor_polls_retention`, `DROP INDEX idx_monitor_polls_configuration`, `DROP INDEX idx_monitor_batches_poll`, `DROP TABLE monitor_management_polls`, `DROP TABLE monitor_collection_leases`} {
		if _, e := db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	for table, columns := range managementAddedColumns {
		for _, column := range columns {
			if _, e := db.Exec(`ALTER TABLE ` + table + ` DROP COLUMN ` + column); e != nil {
				t.Fatal(e)
			}
		}
	}
	if _, e := db.Exec(`DELETE FROM schema_migrations WHERE version=28`); e != nil {
		t.Fatal(e)
	}
}
func seedLegacyMonitorReceipt(t *testing.T, s *SQLiteStore) MonitorSource {
	t.Helper()
	_, m, now := seedMonitorFixture(t, s)
	if _, e := s.SaveMonitorSource(m, 0); e != nil {
		t.Fatal(e)
	}
	if _, e := s.SaveMonitorBindings(MonitorBindings{ProjectID: m.ProjectID, ScenarioID: "monitor-scenario", Sources: []MonitorBinding{{SourceID: m.ID}}}, 0); e != nil {
		t.Fatal(e)
	}
	if _, e := s.StartRunMonitors(m.ProjectID, "monitor-run", "monitor-scenario", now, now.Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	if e := s.AcceptMonitorBatch(m.ProjectID, m.CollectorID, MonitorBatchReceipt{JobID: "monitor-run:" + m.ID, Sequence: 0, Digest: managementCore, ArtifactKey: "legacy-point-artifact", SampleCount: 1, ReceivedAt: now}); e != nil {
		t.Fatal(e)
	}
	return m
}
func assertManagementLegacyUpgrade(t *testing.T, s *SQLiteStore, m MonitorSource) {
	t.Helper()
	status, e := s.SchemaStatus()
	if e != nil || status.AppliedVersion != 28 {
		t.Fatalf("release28 migration: %#v %v", status, e)
	}
	r, e := s.ListMonitorBatchReceipts(m.ProjectID, "monitor-run", "", -1, 10)
	if e != nil || len(r) != 1 {
		t.Fatalf("legacy receipts: %#v %v", r, e)
	}
	b := r[0]
	if b.EvidenceVersion != nil || b.ManagementSnapshotCount != nil || b.ManagementRowCount != nil || b.CollectionGeneration != nil || b.ManagementPollID != nil || b.SampleCount != 1 {
		t.Fatalf("legacy unknown metadata changed: %#v", b)
	}
	var instance sql.NullString
	if e = s.db.QueryRow(`SELECT instance_id FROM run_monitor_collector_readiness WHERE collector_id=?`, m.CollectorID).Scan(&instance); e != nil || instance.Valid {
		t.Fatalf("legacy readiness acquired authority: %#v %v", instance, e)
	}
}
func TestSQLiteManagementRelease28UpgradesPopulatedRelease27(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy27.db")
	s, e := NewSQLiteStore(path)
	if e != nil {
		t.Fatal(e)
	}
	m := seedLegacyMonitorReceipt(t, s)
	removeManagementFoundationSchema(t, s.db)
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		s, e = NewSQLiteStore(path)
		if e != nil {
			t.Fatal(e)
		}
		assertManagementLegacyUpgrade(t, s, m)
		if e = s.Close(); e != nil {
			t.Fatal(e)
		}
	}
}
