package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/grafana"
	"github.com/jasonczech/apexload/pkg/storage"
)

// snapshotSpyStore embeds the full spy and tracks whether a snapshot was written.
type snapshotSpyStore struct {
	*metricsStoreSpy
	dashboards []*storage.GrafanaDashboard
	snapshot   *storage.GrafanaDashboardSnapshot
	saveCalled bool
}

func (s *snapshotSpyStore) GetGrafanaDashboardSnapshot(string, string) (*storage.GrafanaDashboardSnapshot, error) {
	if s.snapshot != nil {
		return s.snapshot, nil
	}
	return nil, sql.ErrNoRows
}

func TestApplyImportedDashboardThresholdVerdictPromotesRunSLA(t *testing.T) {
	payload, err := json.Marshal(ImportedDashboardGraphsResponse{Dashboards: []DashboardPanelsResult{{
		DashboardTitle: "Infra", Panels: []DashboardPanel{{Title: "CPU", ThresholdEvaluated: true, ThresholdBreached: true, ThresholdDetail: "entered critical threshold range at 95"}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	store := &snapshotSpyStore{metricsStoreSpy: &metricsStoreSpy{}, snapshot: &storage.GrafanaDashboardSnapshot{TestID: "run-1", ProjectID: "proj-a", Payload: payload}}
	runner := NewTestRunner(1, NewHub(), store)
	test := &ManagedTest{ID: "run-1", ProjectID: "proj-a"}
	runner.mu.Lock()
	runner.tests[test.ID] = test
	runner.mu.Unlock()
	if err := runner.applyImportedDashboardThresholdVerdict(test); err != nil {
		t.Fatal(err)
	}
	if !test.ciSLAEvaluated || !test.ciSLABreached || len(test.ciSLADetails) != 1 || test.ciSLADetails[0] != "Infra / CPU: entered critical threshold range at 95" {
		t.Fatalf("SLA state = evaluated:%v breached:%v details:%v", test.ciSLAEvaluated, test.ciSLABreached, test.ciSLADetails)
	}
}

func (s *snapshotSpyStore) ListGrafanaDashboards(string, int, int) ([]*storage.GrafanaDashboard, error) {
	return s.dashboards, nil
}

func (s *snapshotSpyStore) SaveGrafanaDashboardSnapshot(snapshot *storage.GrafanaDashboardSnapshot) error {
	s.saveCalled = true
	copy := *snapshot
	copy.Payload = append([]byte(nil), snapshot.Payload...)
	s.snapshot = &copy
	return nil
}

// TestCaptureImportedDashboardSnapshotSkipsEmpty verifies a run with no imported
// dashboards to capture does not write an (empty) snapshot row.
func TestCaptureImportedDashboardSnapshotSkipsEmpty(t *testing.T) {
	store := &snapshotSpyStore{metricsStoreSpy: &metricsStoreSpy{}}
	runner := NewTestRunner(1, NewHub(), store)
	// Prometheus configured, but the project has zero enabled dashboards.
	settings := runner.GetSystemSettings()
	settings.Prometheus = PrometheusSettings{Enabled: true, BaseURL: "http://prometheus:9090", TimeoutSeconds: 5}
	if _, err := runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings: %v", err)
	}

	test := &ManagedTest{ID: "run-1", ProjectID: "proj-a", CreatedAt: time.Now().Add(-time.Minute)}
	if err := runner.captureImportedDashboardSnapshot(context.Background(), test, time.Now()); err != nil {
		t.Fatalf("capture returned error: %v", err)
	}
	if store.saveCalled {
		t.Fatalf("expected no snapshot save when the run has no imported dashboards")
	}
}

func TestImportedDashboardSnapshotErrorsTriggerRetry(t *testing.T) {
	results := []DashboardPanelsResult{{Panels: []DashboardPanel{{PanelID: 1, Error: "prometheus unavailable"}}}}
	if !importedDashboardSnapshotHasErrors(results) {
		t.Fatal("expected failed panel to request a retry")
	}
	results[0].Panels[0].Error = ""
	if importedDashboardSnapshotHasErrors(results) {
		t.Fatal("successful panels should not request a retry")
	}
}

func TestCaptureImportedDashboardSnapshotRetriesUnconfiguredDatasourceThenPersistsEvidence(t *testing.T) {
	store := &snapshotSpyStore{metricsStoreSpy: &metricsStoreSpy{}, dashboards: []*storage.GrafanaDashboard{{
		ID: "dashboard-1", ProjectID: "proj-a", Name: "Infra", Enabled: true,
		Panels: []grafana.Panel{{PanelID: 1, Title: "CPU", Kind: grafana.PanelKindLine}},
	}}}
	runner := NewTestRunner(1, NewHub(), store)
	test := &ManagedTest{ID: "run-1", ProjectID: "proj-a", CreatedAt: time.Now().Add(-time.Minute)}
	if err := runner.captureImportedDashboardSnapshotAttempt(context.Background(), test, time.Now(), false); err == nil {
		t.Fatal("expected an unconfigured datasource to request snapshot backfill")
	}
	if store.saveCalled {
		t.Fatal("transient datasource failure should not be persisted before retry exhaustion")
	}
	if err := runner.captureImportedDashboardSnapshotAttempt(context.Background(), test, time.Now(), true); err != nil {
		t.Fatal(err)
	}
	if !store.saveCalled || store.snapshot == nil {
		t.Fatal("expected fail-soft snapshot evidence after retry exhaustion")
	}
	var response ImportedDashboardGraphsResponse
	if err := json.Unmarshal(store.snapshot.Payload, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Dashboards) != 1 || len(response.Dashboards[0].Panels) != 1 || response.Dashboards[0].Panels[0].Error == "" {
		t.Fatalf("snapshot response = %#v", response)
	}
}
