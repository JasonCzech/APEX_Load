package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/grafana"
	"github.com/jasonczech/apexload/pkg/storage"
)

const importedDashboardSnapshotTimeout = 30 * time.Second

type importedDashboardSnapshotStore interface {
	ListGrafanaDashboards(projectID string, limit int, offset int) ([]*storage.GrafanaDashboard, error)
	SaveGrafanaDashboardSnapshot(snapshot *storage.GrafanaDashboardSnapshot) error
	GetGrafanaDashboardSnapshot(testID, projectID string) (*storage.GrafanaDashboardSnapshot, error)
}

// captureImportedDashboardSnapshot resolves the bounded dashboard set once at run
// completion and stores an immutable-by-test payload for subsequent result views.
// Dashboard definitions may later be edited or deleted without rewriting this run's
// evidence.
func (tr *TestRunner) captureImportedDashboardSnapshot(ctx context.Context, test *ManagedTest, completedAt time.Time) error {
	if tr == nil || test == nil || test.ID == "" {
		return nil
	}
	store, ok := tr.testStore.(importedDashboardSnapshotStore)
	if !ok {
		return nil
	}
	projectID := normalizeProjectID(test.ProjectID)
	if _, err := store.GetGrafanaDashboardSnapshot(test.ID, projectID); err == nil {
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	settings := normalizePrometheusSettings(tr.GetSystemSettings().Prometheus)
	ds := newPromGraphDataSource(settings)
	enabled, configured := ds.Available()
	start := test.CreatedAt
	if test.StartedAt != nil && !test.StartedAt.IsZero() {
		start = *test.StartedAt
	}
	end := completedAt
	if end.IsZero() {
		end = time.Now().UTC()
	}
	if !start.Before(end) {
		start = end.Add(-time.Minute)
	}
	stepSeconds := snapshotStepSeconds(start, end)
	resp := ImportedDashboardGraphsResponse{
		Enabled: enabled, Configured: configured, DatasourceID: ds.ID(), GeneratedAt: time.Now().UTC(),
		Start: start, End: end, StepSeconds: stepSeconds, Dashboards: []DashboardPanelsResult{},
	}
	if enabled && configured {
		dashboards, err := store.ListGrafanaDashboards(projectID, storage.MaxGrafanaDashboardsPerProject+1, 0)
		if err != nil {
			return err
		}
		if len(dashboards) > storage.MaxGrafanaDashboardsPerProject {
			return fmt.Errorf("%w: project exceeds the maximum of %d imported Grafana dashboards", storage.ErrGrafanaDashboardLimit, storage.MaxGrafanaDashboardsPerProject)
		}
		active := make([]*storage.GrafanaDashboard, 0, len(dashboards))
		for _, dashboard := range dashboards {
			if dashboard.Enabled && len(dashboard.Panels) > 0 {
				active = append(active, dashboard)
			}
		}
		results := make([]DashboardPanelsResult, len(active))
		var aggregatePoints atomic.Int64
		var aggregateBytes atomic.Int64
		querySemaphore := make(chan struct{}, maxDashboardQueryConcurrency)
		jobs := make(chan int, len(active))
		var wg sync.WaitGroup
		workers := min(maxConcurrentDashboardResolves, len(active))
		for worker := 0; worker < workers; worker++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range jobs {
					dashboard := active[i]
					panels := resolveDashboardPanels(ctx, ds, dashboardResolvePlan{
						dashboardID: dashboard.ID, panels: dashboard.Panels,
						resolution: grafanaQueryResolutionForSnapshot(test.ID, dashboard),
						start:      start, end: end, stepSeconds: stepSeconds,
						perQueryTimeoutSeconds: settings.TimeoutSeconds,
						aggregatePointsUsed:    &aggregatePoints,
						aggregatePointsMax:     maxImportedDashboardsPointsTotal,
						aggregateBytesUsed:     &aggregateBytes,
						aggregateBytesMax:      maxImportedDashboardsBytesTotal,
						querySemaphore:         querySemaphore,
					})
					results[i] = DashboardPanelsResult{DashboardID: dashboard.ID, DashboardTitle: dashboard.Name, DatasourceID: ds.ID(), Panels: panels}
				}
			}()
		}
		for i := range active {
			jobs <- i
		}
		close(jobs)
		wg.Wait()
		resp.Dashboards = results
	}
	// Don't persist a snapshot for runs with no imported dashboards to capture: it
	// would write one empty row per run forever, and the read path already falls
	// through correctly on a missing snapshot.
	if len(resp.Dashboards) == 0 {
		return nil
	}
	payload, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	return store.SaveGrafanaDashboardSnapshot(&storage.GrafanaDashboardSnapshot{
		TestID: test.ID, ProjectID: projectID, Payload: payload, CapturedAt: resp.GeneratedAt,
	})
}

func grafanaQueryResolutionForSnapshot(testID string, dashboard *storage.GrafanaDashboard) grafana.QueryResolution {
	return grafana.QueryResolution{TestID: testID, ProjectID: dashboard.ProjectID, Bindings: dashboard.Bindings}
}

func snapshotStepSeconds(start, end time.Time) int {
	seconds := int(end.Sub(start).Seconds())
	step := (seconds + 419) / 420
	if step < 5 {
		return 5
	}
	return step
}
