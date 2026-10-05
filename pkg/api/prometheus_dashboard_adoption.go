package api

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/grafana"
	"github.com/jasonczech/apexload/pkg/prometheus"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Adoption resolves the existing import contract into a reviewable draft. It
// neither queries a datasource nor copies credentials or dashboard thresholds.
type PrometheusDashboardAdoptionRequest struct {
	DashboardID  string `json:"dashboard_id"`
	StepSeconds  int    `json:"step_seconds,omitempty"`
	RangeSeconds int    `json:"range_seconds,omitempty"`
}

type PrometheusDashboardAdoptionResponse struct {
	Selection          prometheus.Selection `json:"selection"`
	DashboardID        string               `json:"dashboard_id"`
	DashboardSHA256    string               `json:"dashboard_sha256"`
	ConnectionID       string               `json:"connection_id"`
	ConnectionRevision int64                `json:"connection_revision"`
	ResolvedBindings   map[string]string    `json:"resolved_bindings,omitempty"`
	DatasourceUIDs     []string             `json:"datasource_uids"`
	RangeSeconds       int                  `json:"range_seconds"`
	Warnings           []string             `json:"warnings"`
	Truncated          bool                 `json:"truncated"`
}

func (s *Server) handlePrometheusAdoptDashboard(w http.ResponseWriter, r *http.Request) {
	connection, _ := s.loadPrometheusConnection(w, r)
	if connection == nil {
		return
	}
	if !connection.Enabled || connection.VerifiedRevision != connection.Revision || connection.LastCheckStatus != "connected" {
		writeError(w, http.StatusConflict, "select an enabled, verified Prometheus connection")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 96<<10)
	var req PrometheusDashboardAdoptionRequest
	if !decodePrometheusJSON(w, r, &req) {
		return
	}
	if req.DashboardID == "" || len(req.DashboardID) > 256 {
		writeError(w, http.StatusBadRequest, "dashboard_id is required")
		return
	}
	if req.StepSeconds == 0 {
		req.StepSeconds = 15
	}
	if req.RangeSeconds == 0 {
		req.RangeSeconds = 300
	}
	if req.StepSeconds < 1 || req.StepSeconds > 3600 || req.RangeSeconds < 1 || req.RangeSeconds > 600 {
		writeError(w, http.StatusBadRequest, "adoption step or macro range exceeds supported bounds")
		return
	}
	dashboard, err := s.store.GetGrafanaDashboard(req.DashboardID)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (dashboard == nil || dashboard.ProjectID != connection.ProjectID) {
		writeError(w, http.StatusNotFound, "Grafana dashboard not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Grafana dashboard unavailable")
		return
	}
	draft := prometheusDashboardDraft(dashboard, connection.ID, connection.Revision, req.StepSeconds, req.RangeSeconds)
	writeJSON(w, http.StatusOK, draft)
}

func prometheusDashboardDraft(dashboard *storage.GrafanaDashboard, connectionID string, revision int64, step, window int) PrometheusDashboardAdoptionResponse {
	result := PrometheusDashboardAdoptionResponse{
		Selection:   prometheus.Selection{ProjectID: dashboard.ProjectID, StepSeconds: step, SettleSeconds: 120, Queries: []prometheus.Query{}},
		DashboardID: dashboard.ID, DashboardSHA256: dashboard.SourceSHA256, ConnectionID: connectionID, ConnectionRevision: revision,
		ResolvedBindings: dashboard.Bindings, RangeSeconds: window, DatasourceUIDs: []string{},
		Warnings: []string{"Review each query's target scope, unit, semantics and retained labels before enabling the source.", "All adopted queries will use the selected project connection; original Grafana datasource credentials and thresholds are not adopted.", "Time macros are resolved to a fixed query lookback, not the future test duration."},
	}
	datasources := map[string]bool{}
	resolution := grafana.QueryResolution{StepSeconds: step, RangeSeconds: window, ProjectID: dashboard.ProjectID, Bindings: dashboard.Bindings}
	legacyTransformed, unidentifiedTransform := legacyPrometheusDashboardTransformations(dashboard)
	for panelIndex, panel := range dashboard.Panels {
		transformed := panel.UnsupportedTransformations != nil && *panel.UnsupportedTransformations
		legacyUnverifiable := panel.UnsupportedTransformations == nil && unidentifiedTransform
		if transformed || panel.UnsupportedTransformations == nil && legacyTransformed[panelIndex] || legacyUnverifiable {
			message := fmt.Sprintf("panel_%d: unsupported Grafana transformations; define raw target queries explicitly.", panel.PanelID)
			if legacyUnverifiable {
				message = fmt.Sprintf("panel_%d: legacy transformation provenance cannot identify the affected panel; reimport the dashboard before adopting its queries.", panel.PanelID)
			}
			result.Warnings = append(result.Warnings, message)
			continue
		}
		for index, target := range panel.Targets {
			if strings.TrimSpace(target.Expr) == "" {
				continue
			}
			id := fmt.Sprintf("panel_%d_%d", panel.PanelID, index+1)
			expression, err := grafana.ResolvePanelExpr(target.Expr, resolution)
			if err != nil {
				result.Warnings = append(result.Warnings, id+": unresolved or unsafe variables; run-specific test_id templates require a new reusable target-scope expression.")
				continue
			}
			query := prometheus.Query{ID: id, Name: id, Expression: expression, Unit: panel.Unit, Semantics: "gauge", AllowedLabels: append([]string{}, target.LabelFields...), MinSeries: 1}
			if len(result.Selection.Queries) >= 20 {
				result.Truncated = true
				continue
			}
			candidate := result.Selection
			candidate.Queries = append(append([]prometheus.Query{}, result.Selection.Queries...), query)
			if candidate.Validate() != nil {
				result.Warnings = append(result.Warnings, id+": unsupported query or retained labels; edit the definition explicitly.")
				continue
			}
			result.Selection.Queries = append(result.Selection.Queries, query)
			uid := target.DatasourceUID
			if uid == "" {
				uid = panel.DatasourceUID
			}
			if uid != "" {
				datasources[uid] = true
			}
		}
	}
	for uid := range datasources {
		result.DatasourceUIDs = append(result.DatasourceUIDs, uid)
	}
	sort.Strings(result.DatasourceUIDs)
	if result.Truncated {
		result.Warnings = append(result.Warnings, "The draft reached the 20-query source limit; remaining targets were skipped.")
	}
	return result
}

// Older imports retained transformation diagnostics but omitted their original
// panel indices during row flattening and exclusion. Match quoted titles rather
// than treating the original panels[i] as an index into the surviving panels.
// An anonymous or otherwise unidentifiable diagnostic leaves legacy panels
// unverifiable; new imports carry an explicit per-panel true/false marker.
func legacyPrometheusDashboardTransformations(dashboard *storage.GrafanaDashboard) (map[int]bool, bool) {
	transformed := map[int]bool{}
	unidentified := false
	for _, diagnostic := range dashboard.Diagnostics {
		if diagnostic.Element != "transformations" {
			continue
		}
		matched := false
		for index, panel := range dashboard.Panels {
			if panel.Title != "" && strings.HasSuffix(diagnostic.Location, " "+strconv.Quote(panel.Title)) {
				transformed[index] = true
				matched = true
			}
		}
		unidentified = unidentified || !matched
	}
	return transformed, unidentified
}
