package api

// Imported Grafana dashboards: list/get/patch/delete plus the panel-resolution
// endpoints that turn stored panel PromQL into series over a run window.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/grafana"
	"github.com/jasonczech/apexload/pkg/storage"
)

const defaultGrafanaDashboardListLimit = 100

// ImportedDashboardGraphsResponse is the live/aggregate shape: every enabled dashboard
// in a project resolved over the requested window. It mirrors PrometheusGraphsResponse
// so the dashboard consumes it with the same machinery.
type ImportedDashboardGraphsResponse struct {
	Enabled      bool                    `json:"enabled"`
	Configured   bool                    `json:"configured"`
	DatasourceID string                  `json:"datasource_id,omitempty"`
	GeneratedAt  time.Time               `json:"generated_at"`
	Start        time.Time               `json:"start,omitempty"`
	End          time.Time               `json:"end,omitempty"`
	StepSeconds  int                     `json:"step_seconds,omitempty"`
	Dashboards   []DashboardPanelsResult `json:"dashboards"`
}

func (s *Server) handleListGrafanaDashboards(w http.ResponseWriter, r *http.Request) {
	limit, offset := parseLimitOffsetQuery(r, defaultGrafanaDashboardListLimit)
	if s.store == nil {
		writeJSON(w, http.StatusOK, map[string]any{"dashboards": []*storage.GrafanaDashboard{}, "count": 0, "limit": limit, "offset": offset})
		return
	}
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	dashboards, err := s.store.ListGrafanaDashboards(projectID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list Grafana dashboards")
		return
	}
	if dashboards == nil {
		dashboards = []*storage.GrafanaDashboard{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"dashboards": dashboards, "count": len(dashboards), "limit": limit, "offset": offset})
}

func (s *Server) handleGetGrafanaDashboard(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	dashboard, ok := s.loadGrafanaDashboard(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, dashboard)
}

func (s *Server) handlePatchGrafanaDashboard(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	dashboard, ok := s.loadGrafanaDashboard(w, r)
	if !ok {
		return
	}
	expectedUpdatedAt := dashboard.UpdatedAt
	var patch struct {
		Name        *string            `json:"name"`
		Description *string            `json:"description"`
		Enabled     *bool              `json:"enabled"`
		Bindings    *map[string]string `json:"bindings"`
	}
	if !decodeJSONBody(w, r, &patch) {
		return
	}
	if patch.Name != nil {
		name := strings.TrimSpace(*patch.Name)
		if name == "" {
			writeError(w, http.StatusBadRequest, "dashboard name cannot be empty")
			return
		}
		if len(name) > storage.MaxGrafanaDashboardNameBytes {
			writeError(w, http.StatusBadRequest, "dashboard name exceeds the size limit")
			return
		}
		dashboard.Name = name
	}
	if patch.Description != nil {
		description := strings.TrimSpace(*patch.Description)
		if len(description) > storage.MaxGrafanaDashboardDescriptionBytes {
			writeError(w, http.StatusBadRequest, "dashboard description exceeds the size limit")
			return
		}
		dashboard.Description = description
	}
	if patch.Enabled != nil {
		dashboard.Enabled = *patch.Enabled
	}
	if patch.Bindings != nil {
		next := make(map[string]string, len(*patch.Bindings))
		seen := make(map[string]struct{}, len(*patch.Bindings))
		for name, value := range *patch.Bindings {
			name = strings.TrimSpace(name)
			if _, duplicate := seen[name]; duplicate {
				writeError(w, http.StatusBadRequest, "binding "+name+" was supplied more than once")
				return
			}
			seen[name] = struct{}{}
			if _, declared := dashboard.Bindings[name]; !declared {
				writeError(w, http.StatusBadRequest, "binding "+name+" is not declared by this dashboard")
				return
			}
			if !grafana.ValidateBindingName(name) || grafana.IsReservedBindingName(name) || !grafana.ValidateBindingValue(value) {
				writeError(w, http.StatusBadRequest, "binding "+name+" is invalid")
				return
			}
			next[name] = value
		}
		if len(next) != len(dashboard.Bindings) {
			writeError(w, http.StatusBadRequest, "every existing dashboard binding must be supplied")
			return
		}
		dashboard.Bindings = next
	}
	updated, err := s.store.UpdateGrafanaDashboardIfUnchanged(dashboard, expectedUpdatedAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update Grafana dashboard")
		return
	}
	if !updated {
		writeError(w, http.StatusConflict, "Grafana dashboard changed concurrently; reload and retry")
		return
	}
	s.recordAuditEvent(r, "monitoring.dashboard.update", "success", "grafana_dashboard", dashboard.ID, dashboard.ProjectID, nil)
	writeJSON(w, http.StatusOK, dashboard)
}

func (s *Server) handleDeleteGrafanaDashboard(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	dashboard, ok := s.loadGrafanaDashboard(w, r)
	if !ok {
		return
	}
	projectID := authorizedProjectOr(r, dashboard.ProjectID)
	deleted, err := s.store.DeleteGrafanaDashboardIfUnchanged(dashboard.ID, projectID, dashboard.UpdatedAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete grafana dashboard")
		return
	}
	if !deleted {
		writeError(w, http.StatusConflict, "grafana dashboard changed concurrently")
		return
	}
	s.recordAuditEvent(r, "monitoring.dashboard.delete", "success", "grafana_dashboard", dashboard.ID, dashboard.ProjectID, nil)
	writeJSON(w, http.StatusOK, map[string]string{"deleted": dashboard.ID})
}

// handleQueryGrafanaDashboardPanels resolves one dashboard's panels over the requested
// window — used to preview a single dashboard.
func (s *Server) handleQueryGrafanaDashboardPanels(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	dashboard, ok := s.loadGrafanaDashboard(w, r)
	if !ok {
		return
	}
	if !s.requireDashboardTestScope(w, strings.TrimSpace(r.URL.Query().Get("test_id")), dashboard.ProjectID) {
		return
	}
	ds := s.graphDataSource()
	enabled, configured := ds.Available()
	resp := ImportedDashboardGraphsResponse{
		Enabled:      enabled,
		Configured:   configured,
		DatasourceID: ds.ID(),
		GeneratedAt:  time.Now().UTC(),
		Dashboards:   []DashboardPanelsResult{},
	}
	if !enabled || !configured {
		writeJSON(w, http.StatusOK, resp)
		return
	}
	start, end, stepSeconds, err := parsePrometheusGraphRange(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Panel resolution is fail-soft even when every panel fails. Returning the
	// per-panel diagnostics is the point of the explicit "Test now" workflow; a
	// generic 502 would hide the broken query or authentication detail.
	result, _ := s.resolveDashboardResult(r, ds, dashboard, start, end, stepSeconds)
	resp.Start, resp.End = start, end
	// Filtering does not resample immutable snapshot points. Preserve the capture's
	// actual step; use the requested step only for legacy snapshots that omitted it.
	if resp.StepSeconds <= 0 {
		resp.StepSeconds = stepSeconds
	}
	resp.Dashboards = append(resp.Dashboards, result)
	writeJSON(w, http.StatusOK, resp)
}

// handleImportedDashboardGraphs resolves every enabled dashboard in a project over the
// requested window — the live Command Center / snapshot aggregate.
func (s *Server) handleImportedDashboardGraphs(w http.ResponseWriter, r *http.Request) {
	ds := s.graphDataSource()
	enabled, configured := ds.Available()
	resp := ImportedDashboardGraphsResponse{
		Enabled:      enabled,
		Configured:   configured,
		DatasourceID: ds.ID(),
		GeneratedAt:  time.Now().UTC(),
		Dashboards:   []DashboardPanelsResult{},
	}
	if s.store == nil {
		writeJSON(w, http.StatusOK, resp)
		return
	}
	projectID := normalizeProjectID(strings.TrimSpace(r.URL.Query().Get("project_id")))
	testID := strings.TrimSpace(r.URL.Query().Get("test_id"))
	if !s.requireDashboardTestScope(w, testID, projectID) {
		return
	}
	if testID != "" {
		if test, found := s.runner.GetTestSummary(testID); found && normalizeProjectID(test.ProjectID) == projectID && isTerminalTestStatus(test.Status) {
			snapshot, snapshotErr := s.store.GetGrafanaDashboardSnapshot(testID, projectID)
			if snapshotErr == nil {
				if err := json.Unmarshal(snapshot.Payload, &resp); err != nil {
					writeError(w, http.StatusInternalServerError, "stored dashboard snapshot is corrupt")
					return
				}
				start, end, stepSeconds, err := parsePrometheusGraphRange(r)
				if err != nil {
					writeError(w, http.StatusBadRequest, err.Error())
					return
				}
				filterImportedDashboardSnapshot(&resp, start, end, stepSeconds)
				writeJSON(w, http.StatusOK, resp)
				return
			}
			if !errors.Is(snapshotErr, sql.ErrNoRows) {
				writeError(w, http.StatusInternalServerError, "failed to load stored dashboard snapshot")
				return
			}
		}
	}
	if !enabled || !configured {
		writeJSON(w, http.StatusOK, resp)
		return
	}
	start, end, stepSeconds, err := parsePrometheusGraphRange(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	resp, err = s.resolveImportedDashboardGraphs(r.Context(), ds, projectID, testID, start, end, stepSeconds)
	if err != nil {
		if errors.Is(err, storage.ErrGrafanaDashboardLimit) {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
		} else {
			writeError(w, http.StatusInternalServerError, "failed to load imported Grafana dashboards")
		}
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func filterImportedDashboardSnapshot(resp *ImportedDashboardGraphsResponse, start, end time.Time, stepSeconds int) {
	if resp == nil {
		return
	}
	resp.Start, resp.End = start, end
	// Filtering to the requested window does not resample the immutable snapshot
	// points, so keep the capture's actual step; only fall back to the requested step
	// for legacy snapshots that stored no step.
	if resp.StepSeconds <= 0 {
		resp.StepSeconds = stepSeconds
	}
	startMillis, endMillis := start.UnixMilli(), end.UnixMilli()
	for dashboardIndex := range resp.Dashboards {
		for panelIndex := range resp.Dashboards[dashboardIndex].Panels {
			panel := &resp.Dashboards[dashboardIndex].Panels[panelIndex]
			for seriesIndex := range panel.Series {
				values := panel.Series[seriesIndex].Values[:0]
				for _, point := range panel.Series[seriesIndex].Values {
					if point.TimestampMs >= startMillis && point.TimestampMs <= endMillis {
						values = append(values, point)
					}
				}
				panel.Series[seriesIndex].Values = values
			}
		}
	}
}

func (s *Server) requireDashboardTestScope(w http.ResponseWriter, testID, projectID string) bool {
	if testID == "" {
		return true
	}
	test, found := s.runner.GetTestSummary(testID)
	if !found || normalizeProjectID(test.ProjectID) != normalizeProjectID(projectID) {
		writeError(w, http.StatusNotFound, "test not found in the requested project")
		return false
	}
	return true
}

func (s *Server) resolveImportedDashboardGraphs(ctx context.Context, ds graphDataSource, projectID, testID string, start, end time.Time, stepSeconds int) (ImportedDashboardGraphsResponse, error) {
	// A project can contain several dashboard waves. Without a shared deadline each
	// worker would start a fresh per-dashboard timeout, multiplying worst-case request
	// latency by the dashboard count.
	ctx, cancel := context.WithTimeout(ctx, maxDashboardTotalTimeout)
	defer cancel()
	resp := ImportedDashboardGraphsResponse{
		Enabled: true, Configured: true, DatasourceID: ds.ID(), GeneratedAt: time.Now().UTC(),
		Start: start, End: end, StepSeconds: stepSeconds, Dashboards: []DashboardPanelsResult{},
	}
	dashboards, err := s.store.ListGrafanaDashboards(projectID, storage.MaxGrafanaDashboardsPerProject+1, 0)
	if err != nil {
		return resp, err
	}
	if len(dashboards) > storage.MaxGrafanaDashboardsPerProject {
		return resp, fmt.Errorf("%w: project exceeds the maximum of %d imported Grafana dashboards", storage.ErrGrafanaDashboardLimit, storage.MaxGrafanaDashboardsPerProject)
	}
	active := make([]*storage.GrafanaDashboard, 0, len(dashboards))
	for _, d := range dashboards {
		if d.Enabled && len(d.Panels) > 0 {
			active = append(active, d)
		}
	}
	// Dashboards are independent, so resolve them concurrently (bounded) rather than
	// serially — otherwise a project's live latency is the sum of the per-dashboard
	// budgets instead of the max. Each dashboard still bounds its own panel fan-out.
	results := make([]DashboardPanelsResult, len(active))
	var aggregatePoints atomic.Int64
	var aggregateBytes atomic.Int64
	querySemaphore := make(chan struct{}, maxDashboardQueryConcurrency)
	jobs := make(chan int)
	var wg sync.WaitGroup
	workers := min(maxConcurrentDashboardResolves, len(active))
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				result, _ := s.resolveStoredDashboardResultWithBudget(ctx, ds, active[i], testID, start, end, stepSeconds, &aggregatePoints, &aggregateBytes, querySemaphore)
				results[i] = result
			}
		}()
	}
	for i := range active {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	resp.Dashboards = results
	return resp, nil
}

// resolveDashboardResult runs one dashboard's panels and returns the result plus
// whether the whole dashboard failed at the datasource (all panels errored).
func (s *Server) resolveDashboardResult(r *http.Request, ds graphDataSource, d *storage.GrafanaDashboard, start, end time.Time, stepSeconds int) (DashboardPanelsResult, bool) {
	return s.resolveStoredDashboardResult(r.Context(), ds, d, strings.TrimSpace(r.URL.Query().Get("test_id")), start, end, stepSeconds)
}

func (s *Server) resolveStoredDashboardResult(ctx context.Context, ds graphDataSource, d *storage.GrafanaDashboard, testID string, start, end time.Time, stepSeconds int) (DashboardPanelsResult, bool) {
	return s.resolveStoredDashboardResultWithBudget(ctx, ds, d, testID, start, end, stepSeconds, nil, nil, nil)
}

func (s *Server) resolveStoredDashboardResultWithBudget(ctx context.Context, ds graphDataSource, d *storage.GrafanaDashboard, testID string, start, end time.Time, stepSeconds int, aggregatePoints, aggregateBytes *atomic.Int64, querySemaphore chan struct{}) (DashboardPanelsResult, bool) {
	settings := s.runner.GetSystemSettings()
	panels := resolveDashboardPanels(ctx, ds, dashboardResolvePlan{
		dashboardID: d.ID,
		panels:      d.Panels,
		resolution: grafana.QueryResolution{
			TestID:    strings.TrimSpace(testID),
			ProjectID: d.ProjectID,
			Bindings:  d.Bindings,
		},
		start:                  start,
		end:                    end,
		stepSeconds:            stepSeconds,
		perQueryTimeoutSeconds: graphDataSourceTimeoutSeconds(settings),
		aggregatePointsUsed:    aggregatePoints,
		aggregatePointsMax:     maxImportedDashboardsPointsTotal,
		aggregateBytesUsed:     aggregateBytes,
		aggregateBytesMax:      maxImportedDashboardsBytesTotal,
		querySemaphore:         querySemaphore,
	})
	result := DashboardPanelsResult{
		DashboardID:    d.ID,
		DashboardTitle: d.Name,
		DatasourceID:   ds.ID(),
		Panels:         panels,
	}
	return result, result.AllPanelsFailed()
}

func (s *Server) loadGrafanaDashboard(w http.ResponseWriter, r *http.Request) (*storage.GrafanaDashboard, bool) {
	dashboard, err := s.store.GetGrafanaDashboard(r.PathValue("id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "grafana dashboard not found")
			return nil, false
		}
		writeError(w, http.StatusInternalServerError, "failed to load grafana dashboard")
		return nil, false
	}
	if !requireAuthorizedProjectMatch(w, r, dashboard.ProjectID) {
		return nil, false
	}
	return dashboard, true
}
