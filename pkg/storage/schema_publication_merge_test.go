package storage

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// Published releases retain their identities when independently developed
// features are integrated; an existing main database must remain reopenable.
func TestSchemaPublicationMergePreservesMainLedgerAndAddsFeatureReleases(t *testing.T) {
	published := []schemaRelease{
		{Version: 27, Name: "object-activity-and-archives", Fingerprint: "10e70e6ffc7829b2d9e594c854903b1db57d4807b1c2878cea751e0eeb37d1d2"},
		{Version: 28, Name: "archive-restoration-schedule-boundary", Fingerprint: "ba1ac64df70d019c92b5aaae2e4d6ed14f927d8fa4b30d9083a1f533e71981ed"},
		{Version: 29, Name: "appdynamics-durable-run-monitoring", Fingerprint: "ba1618a00f60ea7b120e07b375d67d8fd7c6368d1a9e8a18ef25c09f9b4c0186"},
		{Version: 30, Name: "prometheus-governed-run-monitoring", Fingerprint: "c1393b724be106974e1c3e9765668d426dbd6720dc6df89096b7a32ce0d0124f"},
		{Version: 31, Name: "durable-messaging-progress-history", Fingerprint: "7fbd77d1e116e4349e96ca211376035d847db90d8137c640e7d8f82cb2912f32"},
		{Version: 32, Name: "durable-messaging-management-polls", Fingerprint: "8302b2875bbd64d91edac96ab4c39b6adf66bbfb288f60fbef5428718a1d4911"},
		{Version: 33, Name: "isolated-native-analysis-attempts", Fingerprint: "348e5bff6128c23431046d91f301e65f219eaa542faead33fc42d1508c4587df"},
		{Version: 34, Name: "durable-analysis-upload-physical-writers", Fingerprint: "52971cd3c20e72aefeb2c719f02e125a3689f107a50981c4da14d01a8e34783b"},
		{Version: 35, Name: "dynatrace-saas-durable-run-monitoring", Fingerprint: "de996ade9928b95fa97e6ede0c811d2ee20ee73e0f44d1e26945547814066d7a"},
		{Version: 36, Name: "durable-script-goal-config", Fingerprint: "2d937708f297ade2cdcdc224678c7f25689d3f5f923124549963014c38e0b29e"},
		{Version: 37, Name: "durable-script-content-revision", Fingerprint: "d741b8384d281d84172d60b888a72228892173bbbc96d4f04a0e480cc1e9b745"},
		{Version: 38, Name: "durable-scenario-content-revision", Fingerprint: "185b01e0a6e99c3a5d43a668d2113d7258b5cd89aa0f30e45f094bb2f9c7f5d4"},
		{Version: 39, Name: "run-trace-context-policy", Fingerprint: "26cd04b1928418d383f28268ae70c20e2b943e08217c7fdb3855e8a092a6d8cc"},
		{Version: 40, Name: "rca-analysis-revisions", Fingerprint: "0aa6b5258f7162db82e7647c622672b145dd1db69356907c774882a4071a54e3"},
		{Version: 41, Name: "business-key-project-keys", Fingerprint: "385585ff3818a833a350cd25838901726297b40a458670abf302d52bbb667c52"},
		{Version: 42, Name: "rca-critiques", Fingerprint: "b17ff1432f5b81961297d33644433a760017d6bf8317877509633fab06ac70bb"},
	}
	path := filepath.Join(t.TempDir(), "published-main.db")
	raw, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = raw.Exec(schemaMigrationLedgerDDL); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	appliedAt := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	for _, release := range published {
		if _, err = raw.Exec(`INSERT INTO schema_migrations(version,name,fingerprint,applied_at) VALUES(?,?,?,?)`, release.Version, release.Name, release.Fingerprint, appliedAt); err != nil {
			raw.Close()
			t.Fatal(err)
		}
	}
	if err = raw.Close(); err != nil {
		t.Fatal(err)
	}
	for reopen := 0; reopen < 2; reopen++ {
		store, err := NewSQLiteStore(path)
		if err != nil {
			t.Fatalf("open %d from published main ledger: %v", reopen, err)
		}
		status, err := store.SchemaStatus()
		if err != nil || status.AppliedVersion != currentSchemaRelease().Version || len(status.Pending) != 0 || len(status.Applied) != len(schemaReleases) {
			store.Close()
			t.Fatalf("merged schema status: %#v, %v", status, err)
		}
		for _, release := range published {
			record := status.Applied[release.Version-1]
			if record.Version != release.Version || record.Name != release.Name || record.Fingerprint != release.Fingerprint || !record.AppliedAt.Equal(appliedAt) {
				store.Close()
				t.Fatalf("published ledger identity changed: %#v", record)
			}
		}
		for _, table := range []string{"monitor_collection_leases", "monitor_management_polls", "analysis_attempt_journal", "analysis_upload_writers", "dynatrace_operations", "dynatrace_scan_reservations", "elastic_inspections"} {
			var count int
			if err = store.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil || count != 1 {
				store.Close()
				t.Fatalf("merged feature table %s absent: count=%d err=%v", table, count, err)
			}
		}
		if err = store.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPublicationMergeManagementSourceRefusesProviderConfiguration(t *testing.T) {
	_, source, _ := managementFixture(t)
	for name, mutate := range map[string]func(*MonitorSource){
		"provider connection": func(m *MonitorSource) { m.ConnectionID = "provider-connection" },
		"provider executor":   func(m *MonitorSource) { m.ExecutorMode = "direct" },
	} {
		t.Run(name, func(t *testing.T) {
			mixed := source
			mutate(&mixed)
			if err := mixed.Validate(); err == nil {
				t.Fatal("management approval admitted unrelated provider execution configuration")
			}
		})
	}
	if err := source.Validate(); err != nil {
		t.Fatal("ordinary management approval lost compatibility", err)
	}
}
