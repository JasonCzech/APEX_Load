package api

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// snapshotSpyStore embeds the full spy and tracks whether a snapshot was written.
type snapshotSpyStore struct {
	*metricsStoreSpy
	dashboards []*storage.GrafanaDashboard
	saveCalled bool
}

func (s *snapshotSpyStore) GetGrafanaDashboardSnapshot(string, string) (*storage.GrafanaDashboardSnapshot, error) {
	return nil, sql.ErrNoRows
}

func (s *snapshotSpyStore) ListGrafanaDashboards(string, int, int) ([]*storage.GrafanaDashboard, error) {
	return s.dashboards, nil
}

func (s *snapshotSpyStore) SaveGrafanaDashboardSnapshot(*storage.GrafanaDashboardSnapshot) error {
	s.saveCalled = true
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
