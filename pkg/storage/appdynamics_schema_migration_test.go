package storage

import (
	"path/filepath"
	"testing"
)

func TestAppDynamicsSchemaUpgradesPublishedMainWithoutLosingEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main-v28.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	_, _, now := seedMonitorFixture(t, store)
	const native = `{"schema_version":"native.v1","series":[]}`
	const annotation = `{"annotations":[{"id":"operator-note"}]}`
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO test_interval_summaries(test_id,timestamp,native_evidence_json,dropped_results_available,dropped_results) VALUES(?,?,?,?,?)`, []any{"monitor-run", now, native, true, 7}},
		{`INSERT INTO run_annotations(test_id,project_id,revision,payload) VALUES(?,?,?,?)`, []any{"monitor-run", "monitor-p", 1, annotation}},
		{`DROP TABLE run_monitor_observation_heads`, nil},
		{`DROP TABLE appdynamics_inspections`, nil},
		{`DROP TABLE appdynamics_connections`, nil},
		{`DROP TABLE appdynamics_quota_leases`, nil},
		{`DROP TABLE appdynamics_quota_pools`, nil},
		{`ALTER TABLE tests DROP COLUMN monitor_bindings_json`, nil},
		{`DELETE FROM schema_migrations WHERE version>=29`, nil},
	} {
		if _, err := store.db.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	status, err := store.SchemaStatus()
	if err != nil || status.AppliedVersion != currentSchemaRelease().Version || len(status.Applied) != currentSchemaRelease().Version {
		t.Fatalf("upgraded schema: %+v %v", status, err)
	}
	if status.Applied[27].Name != "archive-restoration-schedule-boundary" || status.Applied[27].Fingerprint != "ba1ac64df70d019c92b5aaae2e4d6ed14f927d8fa4b30d9083a1f533e71981ed" {
		t.Fatalf("published release identity changed: %+v", status.Applied[27])
	}
	var nativeAfter, annotationAfter string
	var available bool
	var drops int
	if err := store.db.QueryRow(`SELECT native_evidence_json,dropped_results_available,dropped_results FROM test_interval_summaries WHERE test_id=?`, "monitor-run").Scan(&nativeAfter, &available, &drops); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT payload FROM run_annotations WHERE test_id=?`, "monitor-run").Scan(&annotationAfter); err != nil {
		t.Fatal(err)
	}
	if nativeAfter != native || annotationAfter != annotation || !available || drops != 7 {
		t.Fatalf("prior monitoring evidence changed: native=%q annotations=%q available=%v drops=%d", nativeAfter, annotationAfter, available, drops)
	}
	for _, table := range []string{"appdynamics_quota_pools", "appdynamics_quota_leases", "appdynamics_connections", "appdynamics_inspections", "run_monitor_observation_heads"} {
		var count int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("new table %s: %d %v", table, count, err)
		}
	}
	if _, err := store.GetTest("monitor-run"); err != nil {
		t.Fatalf("test selection after upgrade: %v", err)
	}
}
