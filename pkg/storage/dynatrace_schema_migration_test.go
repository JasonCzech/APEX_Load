package storage

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestDynatraceSchemaUpgradesPublishedAppDynamicsWithoutRewritingEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "release-29.db")
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	seedMonitorFixture(t, s)
	c := &DynatraceConnection{ID: "legacy-dt", ProjectID: "monitor-p", Name: "Legacy", Enabled: true, EnvironmentURL: "https://fixture.live.dynatrace.com", MetricsEnabled: true, EnvironmentTokenSecret: "frozen-ref", MetricQueries: []DynatraceMetricQuery{{Name: "cpu", MetricSelector: "builtin:host.cpu.usage", Aggregation: "avg"}}}
	if err = s.CreateDynatraceConnection(c); err != nil {
		t.Fatal(err)
	}
	oldCapture := map[string]any{"connection_id": c.ID, "environment_token_secret": "frozen-ref", "metric_queries": c.MetricQueries}
	run, _ := s.GetTest("monitor-run")
	run.DynatraceCapture = oldCapture
	run.MonitorBindings = nil
	if err = s.SaveTest(run); err != nil {
		t.Fatal(err)
	}
	// Freeze the persisted JSON shape. In-memory typed query structs and the
	// generic archived map have different key ordering, which is not a mutation.
	run, _ = s.GetTest("monitor-run")
	before, _ := json.Marshal(run.DynatraceCapture)
	for _, q := range []string{`DROP TABLE dynatrace_operations`, `DROP TABLE dynatrace_scan_reservations`, `ALTER TABLE dynatrace_connections DROP COLUMN revision`, `ALTER TABLE dynatrace_connections DROP COLUMN verified_revision`, `ALTER TABLE dynatrace_connections DROP COLUMN native_config_json`, `DELETE FROM schema_migrations WHERE version=30`} {
		if _, err = s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	status, err := s.SchemaStatus()
	if err != nil || status.AppliedVersion != 30 || status.Applied[28].Fingerprint != "ba1618a00f60ea7b120e07b375d67d8fd7c6368d1a9e8a18ef25c09f9b4c0186" {
		t.Fatalf("release identity changed %+v %v", status, err)
	}
	run, err = s.GetTest("monitor-run")
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(run.DynatraceCapture)
	if string(before) != string(after) || run.MonitorBindings != nil {
		t.Fatalf("legacy evidence/selection changed before=%s after=%s", before, after)
	}
	c, err = s.GetDynatraceConnection(c.ID)
	if err != nil || c.Revision != 1 || c.VerifiedRevision != 0 || c.EnvironmentTokenSecret != "frozen-ref" || c.CollectionMode != "direct" {
		t.Fatalf("legacy profile %+v %v", c, err)
	}
	if sources, err := s.ListMonitorSources(c.ProjectID); err != nil || len(sources) != 0 {
		t.Fatalf("upgrade automatically selected Dynatrace %+v %v", sources, err)
	}
	source, err := s.EnsureLegacyDynatraceMonitorSource(c.ProjectID, c.ID)
	if err != nil || source.ExecutorMode != "direct" || source.Dynatrace == nil {
		t.Fatalf("explicit legacy selection %+v %v", source, err)
	}
	bindings, err := s.GetMonitorBindings(c.ProjectID, "monitor-scenario")
	if err != nil || len(bindings.Sources) != 0 {
		t.Fatalf("legacy adapter selected unrelated scenario %+v %v", bindings, err)
	}
	if _, err = s.EnsureLegacyDynatraceMonitorSource("monitor-other", c.ID); err == nil {
		t.Fatal("cross-project legacy selection accepted")
	}
	c.Name = "Updated legacy profile"
	c.MetricQueries[0].MetricSelector = "builtin:host.mem.usage"
	if ok, err := s.UpdateDynatraceConnectionIfRevision(c, c.Revision); err != nil || !ok {
		t.Fatal(ok, err)
	}
	refreshed, err := s.EnsureLegacyDynatraceMonitorSource(c.ProjectID, c.ID)
	if err != nil || refreshed.ID != source.ID || refreshed.Revision != source.Revision+1 || refreshed.Name != c.Name || refreshed.Dynatrace.Metrics[0].MetricSelector != c.MetricQueries[0].MetricSelector {
		t.Fatal(refreshed, err)
	}
}
