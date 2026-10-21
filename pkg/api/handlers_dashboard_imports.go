package api

// Imported Grafana dashboards: two-phase convert → review → commit, mirroring the
// Bruno import doctrine. Phase 1 parses the uploaded dashboard JSON and returns a
// reviewable panel set plus diagnostics and variable bindings, holding the source in a
// short-lived durable session. Phase 2 re-derives the trusted panel/binding set from
// that stored source (never trusting the client's echoed values) and persists it.
//
// A dashboard is an observability artifact, not a load script, so this deliberately
// does NOT reuse the /api/v1/scripts/import/* machinery or the script-import response
// shape.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/grafana"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	grafanaImportSessionTTL   = 15 * time.Minute
	grafanaImportSessionLimit = 128
	grafanaImportSessionBytes = 64 << 20 // 64 MiB across all active sessions
)

type grafanaDashboardImportRequest struct {
	ProjectID string `json:"project_id"`
	Name      string `json:"name,omitempty"`
	Content   string `json:"content"`
}

type grafanaDashboardImportResponse struct {
	ImportSessionID  string                    `json:"import_session_id"`
	ExpiresAt        time.Time                 `json:"expires_at"`
	Dashboard        *grafana.Dashboard        `json:"dashboard"`
	Stats            grafana.ConvertStats      `json:"stats"`
	Warnings         []string                  `json:"warnings,omitempty"`
	Assumptions      []string                  `json:"assumptions,omitempty"`
	ReviewSections   []diag.ReviewSection      `json:"review_sections,omitempty"`
	Bindings         []grafana.VariableBinding `json:"bindings,omitempty"`
	RequiredBindings []string                  `json:"required_bindings,omitempty"`
	HasBlockers      bool                      `json:"has_blockers"`
}

type grafanaDashboardCommitRequest struct {
	ProjectID       string            `json:"project_id"`
	ImportSessionID string            `json:"import_session_id"`
	Name            string            `json:"name,omitempty"`
	Bindings        map[string]string `json:"bindings,omitempty"`
}

type grafanaDashboardPreviewRequest struct {
	ProjectID       string            `json:"project_id"`
	ImportSessionID string            `json:"import_session_id"`
	Bindings        map[string]string `json:"bindings,omitempty"`
	TestID          string            `json:"test_id,omitempty"`
	Start           time.Time         `json:"start,omitempty"`
	End             time.Time         `json:"end,omitempty"`
	StepSeconds     int               `json:"step_seconds,omitempty"`
}

type grafanaDashboardLiveImportRequest struct {
	ProjectID string `json:"project_id"`
	UID       string `json:"uid"`
	Name      string `json:"name,omitempty"`
}

type grafanaLabelValuesRequest struct {
	ProjectID       string            `json:"project_id"`
	ImportSessionID string            `json:"import_session_id"`
	Variable        string            `json:"variable"`
	Bindings        map[string]string `json:"bindings,omitempty"`
	TestID          string            `json:"test_id,omitempty"`
}

var grafanaLabelValuesPattern = regexp.MustCompile(`(?is)^\s*label_values\s*\(\s*(.+)\s*,\s*([A-Za-z_][A-Za-z0-9_]*)\s*\)\s*$`)

func (s *Server) handleImportGrafanaDashboard(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	var req grafanaDashboardImportRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		writeError(w, http.StatusBadRequest, "dashboard content is required")
		return
	}
	if len(strings.TrimSpace(req.Name)) > storage.MaxGrafanaDashboardNameBytes {
		writeError(w, http.StatusBadRequest, "dashboard name exceeds the size limit")
		return
	}
	rawProjectID := strings.TrimSpace(req.ProjectID)
	if rawProjectID == "" {
		writeError(w, http.StatusBadRequest, "project_id is required")
		return
	}
	projectID := normalizeProjectID(rawProjectID)
	result, err := grafana.Convert(grafana.ImportRequest{Name: req.Name, ProjectID: projectID, Content: req.Content})
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to parse grafana dashboard: "+err.Error())
		return
	}
	canonicalContent, err := grafana.CanonicalizeImportSource(req.Content)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to normalize grafana dashboard: "+err.Error())
		return
	}
	// Hash the exact uploaded text, including insignificant surrounding whitespace,
	// so provenance identifies the user's source rather than its canonical form.
	sourceSum := sha256.Sum256([]byte(req.Content))
	now := time.Now().UTC()
	session := &storage.GrafanaImportSession{
		ID: "gdash-import-" + uuid.New().String(), ProjectID: projectID,
		Name: strings.TrimSpace(req.Name), Content: canonicalContent, SourceSHA256: hex.EncodeToString(sourceSum[:]),
		CreatedAt: now, ExpiresAt: now.Add(grafanaImportSessionTTL),
	}
	if err := s.store.SaveGrafanaImportSession(session, grafanaImportSessionLimit, grafanaImportSessionBytes); err != nil {
		if errors.Is(err, storage.ErrGrafanaImportSessionLimit) {
			writeError(w, http.StatusTooManyRequests, err.Error())
		} else {
			writeError(w, http.StatusInternalServerError, "failed to create Grafana import session")
		}
		return
	}
	writeJSON(w, http.StatusOK, grafanaDashboardImportResponse{
		ImportSessionID:  session.ID,
		ExpiresAt:        session.ExpiresAt,
		Dashboard:        result.Dashboard,
		Stats:            result.Stats,
		Warnings:         result.Warnings,
		Assumptions:      result.Assumptions,
		ReviewSections:   result.Unsupported.ReviewSections(),
		Bindings:         result.Bindings,
		RequiredBindings: result.RequiredBindingNames(),
		HasBlockers:      result.HasBlockers(),
	})
}

func (s *Server) handleCommitGrafanaDashboardImport(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	var req grafanaDashboardCommitRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	rawProjectID := strings.TrimSpace(req.ProjectID)
	if rawProjectID == "" {
		writeError(w, http.StatusBadRequest, "project_id is required")
		return
	}
	projectID := normalizeProjectID(rawProjectID)
	if len(strings.TrimSpace(req.Name)) > storage.MaxGrafanaDashboardNameBytes {
		writeError(w, http.StatusBadRequest, "dashboard name exceeds the size limit")
		return
	}
	sessionID := strings.TrimSpace(req.ImportSessionID)
	session, err := s.store.GetGrafanaImportSession(sessionID, projectID, time.Now().UTC())
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusConflict, "import session expired or not found; re-upload the dashboard")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to load Grafana import session")
		}
		return
	}

	// Re-derive the trusted panel/binding set from the stored source. The client's
	// echoed dashboard/bindings are never trusted as data.
	result, err := grafana.Convert(grafana.ImportRequest{Name: session.Name, ProjectID: projectID, Content: session.Content})
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to parse grafana dashboard: "+err.Error())
		return
	}
	if result.HasBlockers() {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"code":            "grafana_import_blocked",
			"message":         "the dashboard has blocking diagnostics that must be resolved before import",
			"review_sections": result.Unsupported.ReviewSections(),
		})
		return
	}

	bindings, missing, bindingErr := resolveGrafanaImportBindings(result, req.Bindings)
	if bindingErr != nil {
		writeError(w, http.StatusBadRequest, bindingErr.Error())
		return
	}
	if len(missing) > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{
			"code": "grafana_import_unbound_variables", "message": "bind every required dashboard variable before importing", "variables": missing,
		})
		return
	}

	// Claim the durable session only after validation. The compare-and-set claim works
	// across controller replicas, and a failed save releases it for a safe retry.
	claimToken := uuid.New().String()
	session, claimed, err := s.store.ClaimGrafanaImportSession(session.ID, projectID, claimToken, time.Now().UTC(), time.Minute)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to claim Grafana import session")
		return
	}
	if !claimed {
		writeError(w, http.StatusConflict, "import session already committed; re-upload to import again")
		return
	}

	dashboard := &storage.GrafanaDashboard{
		ID:               "gdash-" + uuid.New().String(),
		ProjectID:        projectID,
		Name:             firstNonEmptyString(strings.TrimSpace(req.Name), session.Name, result.Dashboard.SourceTitle, "Imported Dashboard"),
		SourceTitle:      result.Dashboard.SourceTitle,
		SourceUID:        result.Dashboard.SourceUID,
		SourceSHA256:     session.SourceSHA256,
		SchemaVersion:    result.Dashboard.SchemaVersion,
		Enabled:          true,
		Panels:           result.Dashboard.Panels,
		Bindings:         bindings,
		Diagnostics:      result.Unsupported,
		ImportSessionID:  session.ID,
		ImportClaimToken: claimToken,
	}
	_, dashboard.CreatedBy, _ = s.resolveAuditActor(r)
	if err := s.store.SaveGrafanaDashboard(dashboard); err != nil {
		_ = s.store.ReleaseGrafanaImportSession(session.ID, projectID, claimToken)
		if errors.Is(err, storage.ErrGrafanaImportClaimLost) {
			writeError(w, http.StatusConflict, "import session claim changed; retry the commit")
		} else if errors.Is(err, storage.ErrGrafanaDashboardLimit) {
			writeError(w, http.StatusConflict, err.Error())
		} else {
			writeError(w, http.StatusInternalServerError, "failed to save imported Grafana dashboard")
		}
		return
	}
	s.recordAuditEvent(r, "monitoring.dashboard.import", "success", "grafana_dashboard", dashboard.ID, dashboard.ProjectID, map[string]any{
		"name":   dashboard.Name,
		"panels": len(dashboard.Panels),
	})
	writeJSON(w, http.StatusCreated, dashboard)
}

func resolveGrafanaImportBindings(result *grafana.ConvertResult, supplied map[string]string) (map[string]string, []string, error) {
	bindings := map[string]string{}
	seenNames := make(map[string]struct{}, len(supplied))
	required := make(map[string]struct{}, len(result.RequiredBindingNames()))
	for _, name := range result.RequiredBindingNames() {
		required[name] = struct{}{}
	}
	// User-supplied binding values must be safe to splice into PromQL.
	for name, value := range supplied {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, duplicate := seenNames[name]; duplicate {
			return nil, nil, fmt.Errorf("binding %s was supplied more than once", name)
		}
		seenNames[name] = struct{}{}
		if !grafana.ValidateBindingName(name) || grafana.IsReservedBindingName(name) {
			return nil, nil, fmt.Errorf("binding name %s is invalid or reserved", name)
		}
		if _, allowed := required[name]; !allowed {
			return nil, nil, fmt.Errorf("binding %s is not required by this dashboard", name)
		}
		if !grafana.ValidateBindingValue(value) {
			return nil, nil, fmt.Errorf("binding value for %s is not a valid PromQL value", name)
		}
		bindings[name] = value
	}
	// Fold in import-resolved auto variables whose value is fixed at import time —
	// constants and the selected interval — so query time can resolve references to
	// them (e.g. rate(x[$win])). Datasource-type autos are intentionally excluded:
	// they resolve to a label, never a PromQL value, and are not used inside exprs.
	for _, b := range result.Bindings {
		if b.Kind != grafana.BindingAuto || b.ResolvedTo == "" || len(b.UsedBy) == 0 {
			continue
		}
		switch b.GrafanaType {
		case "constant":
			// A constant is a fixed dashboard value; an unsafe one would inject when
			// spliced, so the dashboard genuinely cannot be imported safely.
			if !grafana.ValidateBindingValue(b.ResolvedTo) {
				return nil, nil, fmt.Errorf("constant variable %s has a value that cannot be safely used in PromQL", b.Name)
			}
			bindings[b.Name] = b.ResolvedTo
		case "interval":
			// A plain duration ("5m") is folded so rate(x[$win]) resolves. A dynamic
			// "auto" selection (e.g. $__auto_interval_x) is not a literal value — skip
			// it rather than blocking the whole import; a panel that references it then
			// surfaces a per-panel error at query time.
			if grafana.ValidateBindingValue(b.ResolvedTo) {
				bindings[b.Name] = b.ResolvedTo
			}
		}
	}
	// Every required (and used) variable must be bound.
	var missing []string
	for _, name := range result.RequiredBindingNames() {
		if _, ok := bindings[name]; !ok {
			missing = append(missing, name)
		}
	}
	return bindings, missing, nil
}

// resolveGrafanaOptionBindings validates bindings used while evaluating a query
// variable. Unlike commit resolution, it permits any declared required variable (not
// only variables referenced by an imported panel) and does not require unrelated
// variables to be filled before one option list can be loaded.
func resolveGrafanaOptionBindings(result *grafana.ConvertResult, supplied map[string]string) (map[string]string, error) {
	bindings := map[string]string{}
	allowed := map[string]struct{}{}
	seen := map[string]struct{}{}
	for _, binding := range result.Bindings {
		if binding.Kind == grafana.BindingRequired {
			allowed[binding.Name] = struct{}{}
		}
		if binding.Kind != grafana.BindingAuto || binding.ResolvedTo == "" {
			continue
		}
		if binding.GrafanaType == "constant" || binding.GrafanaType == "interval" {
			if !grafana.ValidateBindingValue(binding.ResolvedTo) {
				return nil, fmt.Errorf("automatic variable %s has a value that cannot be safely used in PromQL", binding.Name)
			}
			bindings[binding.Name] = binding.ResolvedTo
		}
	}
	for rawName, value := range supplied {
		name := strings.TrimSpace(rawName)
		if _, duplicate := seen[name]; duplicate {
			return nil, fmt.Errorf("binding %s was supplied more than once", name)
		}
		seen[name] = struct{}{}
		if _, ok := allowed[name]; !ok || !grafana.ValidateBindingName(name) || grafana.IsReservedBindingName(name) {
			return nil, fmt.Errorf("binding %s is not a declared query input", name)
		}
		if !grafana.ValidateBindingValue(value) {
			return nil, fmt.Errorf("binding value for %s is not a valid PromQL value", name)
		}
		bindings[name] = value
	}
	return bindings, nil
}

func (s *Server) handlePreviewGrafanaDashboardImport(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	var req grafanaDashboardPreviewRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	rawProjectID := strings.TrimSpace(req.ProjectID)
	if rawProjectID == "" {
		writeError(w, http.StatusBadRequest, "project_id is required")
		return
	}
	projectID := normalizeProjectID(rawProjectID)
	session, err := s.store.GetGrafanaImportSession(strings.TrimSpace(req.ImportSessionID), projectID, time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusConflict, "import session expired or not found; re-upload the dashboard")
		return
	}
	result, err := grafana.Convert(grafana.ImportRequest{Name: session.Name, ProjectID: projectID, Content: session.Content})
	if err != nil || result.HasBlockers() {
		writeError(w, http.StatusUnprocessableEntity, "dashboard has blocking import diagnostics")
		return
	}
	bindings, missing, err := resolveGrafanaImportBindings(result, req.Bindings)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(missing) > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{"code": "grafana_import_unbound_variables", "variables": missing})
		return
	}
	if req.End.IsZero() {
		req.End = time.Now().UTC()
	}
	if req.Start.IsZero() {
		req.Start = req.End.Add(-15 * time.Minute)
	}
	if req.StepSeconds == 0 {
		req.StepSeconds = defaultPrometheusGraphStepSeconds
	}
	if !req.End.After(req.Start) || req.End.Sub(req.Start) > maxPrometheusGraphRange || req.StepSeconds < minPrometheusGraphStepSeconds || req.StepSeconds > maxPrometheusGraphStepSeconds {
		writeError(w, http.StatusBadRequest, "invalid preview query window")
		return
	}
	if points := int(req.End.Sub(req.Start).Seconds())/req.StepSeconds + 1; points > maxPrometheusGraphPoints {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("preview range/step would produce %d points per series, exceeding the maximum of %d", points, maxPrometheusGraphPoints))
		return
	}
	if !s.requireDashboardTestScope(w, strings.TrimSpace(req.TestID), projectID) {
		return
	}
	ds := s.graphDataSource()
	enabled, configured := ds.Available()
	resp := ImportedDashboardGraphsResponse{Enabled: enabled, Configured: configured, DatasourceID: ds.ID(), GeneratedAt: time.Now().UTC(), Start: req.Start, End: req.End, StepSeconds: req.StepSeconds, Dashboards: []DashboardPanelsResult{}}
	if enabled && configured {
		panels := resolveDashboardPanels(r.Context(), ds, dashboardResolvePlan{dashboardID: session.ID, panels: result.Dashboard.Panels, resolution: grafana.QueryResolution{TestID: req.TestID, ProjectID: projectID, Bindings: bindings}, start: req.Start, end: req.End, stepSeconds: req.StepSeconds, perQueryTimeoutSeconds: graphDataSourceTimeoutSeconds(s.runner.GetSystemSettings())})
		resp.Dashboards = append(resp.Dashboards, DashboardPanelsResult{DashboardID: session.ID, DashboardTitle: result.Dashboard.SourceTitle, DatasourceID: ds.ID(), Panels: panels})
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleGrafanaImportLabelValues(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	var req grafanaLabelValuesRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	rawProjectID := strings.TrimSpace(req.ProjectID)
	if rawProjectID == "" || !grafana.ValidateBindingName(req.Variable) {
		writeError(w, http.StatusBadRequest, "valid project_id and variable are required")
		return
	}
	projectID := normalizeProjectID(rawProjectID)
	settings := s.runner.GetSystemSettings()
	if !settings.Prometheus.AllowLabelValues {
		writeError(w, http.StatusForbidden, "label_values resolution is disabled by system policy")
		return
	}
	session, err := s.store.GetGrafanaImportSession(strings.TrimSpace(req.ImportSessionID), projectID, time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusConflict, "import session expired or not found; re-upload the dashboard")
		return
	}
	result, err := grafana.Convert(grafana.ImportRequest{Name: session.Name, ProjectID: projectID, Content: session.Content})
	if err != nil || result.HasBlockers() {
		writeError(w, http.StatusUnprocessableEntity, "dashboard has blocking import diagnostics")
		return
	}
	var binding *grafana.VariableBinding
	for i := range result.Bindings {
		if result.Bindings[i].Name == strings.TrimSpace(req.Variable) && result.Bindings[i].Kind == grafana.BindingRequired {
			binding = &result.Bindings[i]
			break
		}
	}
	if binding == nil || binding.GrafanaType != "query" {
		writeError(w, http.StatusBadRequest, "variable is not an importable query variable")
		return
	}
	parts := grafanaLabelValuesPattern.FindStringSubmatch(binding.Query)
	if len(parts) != 3 || !grafana.IsRunScopedExpression(parts[1]) {
		writeError(w, http.StatusUnprocessableEntity, "label_values query must contain one bounded, run-scoped PromQL expression")
		return
	}
	bindings, err := resolveGrafanaOptionBindings(result, req.Bindings)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.requireDashboardTestScope(w, strings.TrimSpace(req.TestID), projectID) {
		return
	}
	selector, err := grafana.ResolvePanelExpr(parts[1], grafana.QueryResolution{ProjectID: projectID, TestID: strings.TrimSpace(req.TestID), Bindings: bindings, StepSeconds: 15, RangeSeconds: 300})
	if err != nil {
		writeError(w, http.StatusConflict, "label_values query has unresolved variables: "+err.Error())
		return
	}
	if !grafana.IsRunScopedResolvedExpression(selector, strings.TrimSpace(req.TestID), projectID) {
		writeError(w, http.StatusUnprocessableEntity, "resolved label_values query is not run-scoped")
		return
	}
	ds := s.graphDataSource()
	enabled, configured := ds.Available()
	if !enabled || !configured {
		writeError(w, http.StatusServiceUnavailable, "dashboard datasource is not configured")
		return
	}
	label := parts[2]
	end := time.Now().UTC()
	series, err := ds.QueryRange(r.Context(), graphRangeQuery{Expr: fmt.Sprintf("count by (%s) (%s)", label, selector), DatasourceUID: binding.DatasourceUID, Start: end.Add(-5 * time.Minute), End: end, StepSeconds: 30, MaxSeries: grafana.MaxOptionsPerVar, MaxBytes: maxImportedDashboardQueryResponseBytes, SeriesKeyPrefix: "label_values"})
	if err != nil {
		writeError(w, http.StatusBadGateway, dashboardPanelError(err))
		return
	}
	seen := map[string]struct{}{}
	options := make([]string, 0, len(series))
	for _, item := range series {
		value := strings.TrimSpace(item.Labels[label])
		if value == "" || !grafana.ValidateBindingValue(value) {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		options = append(options, value)
	}
	sort.Strings(options)
	writeJSON(w, http.StatusOK, map[string]any{"variable": binding.Name, "options": options})
}

func (s *Server) handleImportGrafanaDashboardByUID(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	var req grafanaDashboardLiveImportRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	rawProjectID, uid := strings.TrimSpace(req.ProjectID), strings.TrimSpace(req.UID)
	if rawProjectID == "" || uid == "" || len(uid) > grafana.MaxSourceUIDBytes || !grafana.ValidateBindingValue(uid) {
		writeError(w, http.StatusBadRequest, "valid project_id and Grafana uid are required")
		return
	}
	projectID := normalizeProjectID(rawProjectID)
	settings := normalizeGrafanaSettings(s.runner.GetSystemSettings().Grafana)
	enabled, configured := newGrafanaGraphDataSource(settings).Available()
	if !enabled || !configured {
		writeError(w, http.StatusServiceUnavailable, "Grafana connection is not configured")
		return
	}
	endpoint := strings.TrimRight(settings.BaseURL, "/") + "/api/dashboards/uid/" + url.PathEscape(uid)
	allow := map[string]struct{}{}
	for _, host := range settings.allowlistHosts() {
		allow[strings.ToLower(host)] = struct{}{}
	}
	if err := validateOutboundURLAllowingHosts(endpoint, allow); err != nil {
		writeError(w, http.StatusBadGateway, "Grafana URL is not allowed")
		return
	}
	upstreamReq, err := http.NewRequestWithContext(r.Context(), http.MethodGet, endpoint, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create Grafana request")
		return
	}
	upstreamReq.Header.Set("Authorization", "Bearer "+settings.token())
	client := newHostAllowlistedHTTPClient(settings.allowlistHosts(), time.Duration(settings.TimeoutSeconds)*time.Second)
	resp, err := client.Do(upstreamReq)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to fetch Grafana dashboard")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("Grafana returned status %d", resp.StatusCode))
		return
	}
	content, err := io.ReadAll(io.LimitReader(resp.Body, grafanaDashboardImportMaxBodyBytes+1))
	if err != nil || int64(len(content)) > grafanaDashboardImportMaxBodyBytes {
		writeError(w, http.StatusBadGateway, "Grafana dashboard response is invalid or too large")
		return
	}
	result, err := grafana.Convert(grafana.ImportRequest{Name: req.Name, ProjectID: projectID, Content: string(content)})
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to parse Grafana dashboard: "+err.Error())
		return
	}
	canonical, err := grafana.CanonicalizeImportSource(string(content))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to normalize Grafana dashboard")
		return
	}
	sum := sha256.Sum256(content)
	now := time.Now().UTC()
	session := &storage.GrafanaImportSession{ID: "gdash-import-" + uuid.New().String(), ProjectID: projectID, Name: strings.TrimSpace(req.Name), Content: canonical, SourceSHA256: hex.EncodeToString(sum[:]), CreatedAt: now, ExpiresAt: now.Add(grafanaImportSessionTTL)}
	if err := s.store.SaveGrafanaImportSession(session, grafanaImportSessionLimit, grafanaImportSessionBytes); err != nil {
		if errors.Is(err, storage.ErrGrafanaImportSessionLimit) {
			writeError(w, http.StatusTooManyRequests, err.Error())
		} else {
			writeError(w, http.StatusInternalServerError, "failed to create Grafana import session")
		}
		return
	}
	writeJSON(w, http.StatusOK, grafanaDashboardImportResponse{ImportSessionID: session.ID, ExpiresAt: session.ExpiresAt, Dashboard: result.Dashboard, Stats: result.Stats, Warnings: result.Warnings, Assumptions: result.Assumptions, ReviewSections: result.Unsupported.ReviewSections(), Bindings: result.Bindings, RequiredBindings: result.RequiredBindingNames(), HasBlockers: result.HasBlockers()})
}

func (s *Server) handleGrafanaDashboardSupportMatrix(w http.ResponseWriter, r *http.Request) {
	s.writeImporterSupportMatrix(w, r, "grafana")
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// startAuthoringReceiptOutcomeJanitor scrubs request digests and stored
// original outcomes once their keys can no longer replay (24 hours after the
// first claim).
func (s *Server) startAuthoringReceiptOutcomeJanitor() {
	if s == nil || s.store == nil || s.receiptOutcomeCleanupCancel != nil {
		return
	}
	store, supported := s.store.(storage.AuthoringReceiptCreateStore)
	if !supported {
		return
	}
	purge := func(now time.Time) {
		if _, err := store.ScrubExpiredAuthoringReceipts(now.UTC()); err != nil {
			slog.Warn("failed to scrub expired authoring receipts", "error", err)
		}
	}
	purge(time.Now())
	ctx, cancel := context.WithCancel(context.Background())
	s.receiptOutcomeCleanupCancel = cancel
	if !s.launchBackground(func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				purge(now)
			}
		}
	}) {
		cancel()
		s.receiptOutcomeCleanupCancel = nil
	}
}

func (s *Server) startGrafanaImportSessionJanitor() {
	if s == nil || s.store == nil || s.grafanaSessionCleanupCancel != nil {
		return
	}
	if _, err := s.store.DeleteExpiredGrafanaImportSessions(time.Now().UTC()); err != nil {
		slog.Warn("failed to prune expired Grafana import sessions", "error", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.grafanaSessionCleanupCancel = cancel
	if !s.launchBackground(func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				if _, err := s.store.DeleteExpiredGrafanaImportSessions(now.UTC()); err != nil {
					slog.Warn("failed to prune expired Grafana import sessions", "error", err)
				}
			}
		}
	}) {
		cancel()
		s.grafanaSessionCleanupCancel = nil
	}
}
