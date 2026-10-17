package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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
	return tr.captureImportedDashboardSnapshotAttempt(ctx, test, completedAt, false)
}

func (tr *TestRunner) captureImportedDashboardSnapshotAttempt(ctx context.Context, test *ManagedTest, completedAt time.Time, persistPanelErrors bool) error {
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
	settings := tr.GetSystemSettings()
	ds := configuredGraphDataSource(settings)
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
	if len(active) == 0 {
		return nil
	}
	if enabled && configured {
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
						perQueryTimeoutSeconds: graphDataSourceTimeoutSeconds(settings),
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
	} else {
		message := "dashboard datasource is disabled or not fully configured"
		resp.Dashboards = make([]DashboardPanelsResult, len(active))
		for i, dashboard := range active {
			panels := make([]DashboardPanel, len(dashboard.Panels))
			for panelIndex, panel := range dashboard.Panels {
				panels[panelIndex] = failedPanel(panel, message)
			}
			resp.Dashboards[i] = DashboardPanelsResult{DashboardID: dashboard.ID, DashboardTitle: dashboard.Name, DatasourceID: ds.ID(), Panels: panels}
		}
	}
	// Don't persist a snapshot for runs with no imported dashboards to capture: it
	// would write one empty row per run forever, and the read path already falls
	// through correctly on a missing snapshot.
	if len(resp.Dashboards) == 0 {
		return nil
	}
	if !persistPanelErrors && importedDashboardSnapshotHasErrors(resp.Dashboards) {
		return fmt.Errorf("one or more imported dashboard panels could not be resolved")
	}
	payload, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	return store.SaveGrafanaDashboardSnapshot(&storage.GrafanaDashboardSnapshot{
		TestID: test.ID, ProjectID: projectID, Payload: payload, CapturedAt: resp.GeneratedAt,
	})
}

func importedDashboardSnapshotHasErrors(results []DashboardPanelsResult) bool {
	for _, dashboard := range results {
		for _, panel := range dashboard.Panels {
			if panel.Error != "" {
				return true
			}
		}
	}
	return false
}

func (tr *TestRunner) applyImportedDashboardThresholdVerdict(test *ManagedTest) error {
	if tr == nil || test == nil {
		return nil
	}
	store, ok := tr.testStore.(importedDashboardSnapshotStore)
	if !ok {
		return nil
	}
	snapshot, err := store.GetGrafanaDashboardSnapshot(test.ID, normalizeProjectID(test.ProjectID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var response ImportedDashboardGraphsResponse
	if err := json.Unmarshal(snapshot.Payload, &response); err != nil {
		return err
	}
	evaluated := false
	breached := false
	details := append([]string(nil), test.ciSLADetails...)
	for _, dashboard := range response.Dashboards {
		for _, panel := range dashboard.Panels {
			if panel.ThresholdEvaluated {
				evaluated = true
			}
			if !panel.ThresholdBreached {
				continue
			}
			breached = true
			details = compactNonEmptyStrings(append(details, importedPanelBreachDetail(dashboard.DashboardTitle, panel.Title, panel.ThresholdDetail))...)
		}
	}
	tr.mu.Lock()
	test.ciSLAEvaluated = test.ciSLAEvaluated || evaluated
	test.ciSLABreached = test.ciSLABreached || breached
	test.ciSLADetails = details
	if current := tr.tests[test.ID]; current != nil && normalizeProjectID(current.ProjectID) == normalizeProjectID(test.ProjectID) {
		current.ciSLAEvaluated = current.ciSLAEvaluated || evaluated
		current.ciSLABreached = current.ciSLABreached || breached
		current.ciSLADetails = compactNonEmptyStrings(append(current.ciSLADetails, details...)...)
	}
	tr.mu.Unlock()
	return nil
}

// importedPanelBreachDetail describes an imported panel's threshold breach,
// prefixed by whichever of the dashboard and panel titles are set. Titles are
// kept as written, including any slashes they contain.
func importedPanelBreachDetail(dashboardTitle, panelTitle, thresholdDetail string) string {
	titles := make([]string, 0, 2)
	for _, title := range []string{dashboardTitle, panelTitle} {
		if title = strings.TrimSpace(title); title != "" {
			titles = append(titles, title)
		}
	}
	prefix := strings.Join(titles, " / ")
	detail := strings.TrimSpace(thresholdDetail)
	if detail == "" {
		return prefix + " breached its imported threshold"
	}
	if prefix != "" {
		return prefix + ": " + detail
	}
	return detail
}

func graphDataSourceTimeoutSeconds(settings SystemSettings) int {
	if settings.Grafana.Enabled {
		return normalizeGrafanaSettings(settings.Grafana).TimeoutSeconds
	}
	return normalizePrometheusSettings(settings.Prometheus).TimeoutSeconds
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
