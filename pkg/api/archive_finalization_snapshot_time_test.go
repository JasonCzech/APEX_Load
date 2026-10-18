package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/grafana"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestRecoveredArchiveDashboardSnapshotUsesExecutorEnd(t *testing.T) {
	for _, refresh := range []bool{false, true} {
		t.Run(map[bool]string{false: "prepared orphan", true: "catalog backfill"}[refresh], func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
			t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
			store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			const testID = "recovered-dashboard-window"
			_, executorEnd := saveFailedOverOrphanFixture(t, store, root, testID)
			row, err := store.GetTest(testID)
			if err != nil {
				t.Fatal(err)
			}
			if row.EndTime == nil || !row.EndTime.After(executorEnd) {
				t.Fatal("fixture must separate execution and recovery completion")
			}
			if err := store.SaveGrafanaDashboard(&storage.GrafanaDashboard{
				ID: "dashboard", ProjectID: storage.DefaultProjectID, Name: "Infra", SourceTitle: "Infra",
				SourceSHA256: "abc", SchemaVersion: 39, Enabled: true,
				Panels: []grafana.Panel{{PanelID: 1, Title: "CPU", Kind: grafana.PanelKindLine,
					GridPos: grafana.GridPos{W: 12, H: 8},
					Targets: []grafana.Target{{RefID: "A", Expr: `up{test_id="$test_id"}`}}}},
			}); err != nil {
				t.Fatal(err)
			}
			var unavailable atomic.Bool
			unavailable.Store(refresh)
			var queries atomic.Int32
			prometheus := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				queries.Add(1)
				if got := r.URL.Query().Get("end"); got != executorEnd.Format(time.RFC3339) {
					t.Errorf("snapshot query end = %q, want executor end %s", got, executorEnd.Format(time.RFC3339))
				}
				if unavailable.Load() {
					http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[]}}`))
			}))
			t.Cleanup(prometheus.Close)
			runner := NewTestRunner(1, nil, store)
			settings := runner.GetSystemSettings()
			settings.Prometheus = PrometheusSettings{Enabled: true, BaseURL: prometheus.URL, TimeoutSeconds: 1}
			if _, err := runner.UpdateSystemSettings(settings); err != nil {
				t.Fatal(err)
			}
			if err := runner.recoverOrphanedLocalRunArchives(context.Background()); err != nil {
				t.Fatal(err)
			}
			job, err := store.GetArchiveFinalizationJob(testID)
			if err != nil || job.PreparedManifestJSON == "" {
				t.Fatalf("missing prepared recovery manifest: %+v %v", job, err)
			}
			if err := runner.processArchiveFinalizationJob(context.Background(), job); err != nil {
				t.Fatal(err)
			}
			if refresh {
				if _, err := store.GetGrafanaDashboardSnapshot(testID, storage.DefaultProjectID); !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf("failed initial capture persisted a snapshot: %v", err)
				}
				backfill, err := store.GetArchiveRefreshJob(testID)
				if err != nil {
					t.Fatal(err)
				}
				unavailable.Store(false)
				if err := runner.processArchiveRefreshJob(context.Background(), backfill); err != nil {
					t.Fatal(err)
				}
			}
			snapshot, err := store.GetGrafanaDashboardSnapshot(testID, storage.DefaultProjectID)
			if err != nil {
				t.Fatal(err)
			}
			var response ImportedDashboardGraphsResponse
			if err := json.Unmarshal(snapshot.Payload, &response); err != nil {
				t.Fatal(err)
			}
			if queries.Load() == 0 || !response.End.Equal(executorEnd) || len(response.Dashboards) != 1 || importedDashboardSnapshotHasErrors(response.Dashboards) {
				t.Fatalf("snapshot lost the verified executor window: queries=%d response=%+v", queries.Load(), response)
			}
			catalog, err := store.GetRunArchive(testID)
			if err != nil || !catalog.CompletedAt.Equal(*row.EndTime) {
				t.Fatalf("snapshot capture changed canonical catalog completion: %+v %v", catalog, err)
			}
			current, err := store.GetTest(testID)
			if err != nil || current.EndTime == nil || !current.EndTime.Equal(*row.EndTime) {
				t.Fatalf("snapshot capture changed durable completion: %+v %v", current, err)
			}
		})
	}
}
