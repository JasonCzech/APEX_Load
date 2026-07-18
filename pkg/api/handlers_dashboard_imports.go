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
	"log/slog"
	"net/http"
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

	bindings := map[string]string{}
	required := make(map[string]struct{}, len(result.RequiredBindingNames()))
	for _, name := range result.RequiredBindingNames() {
		required[name] = struct{}{}
	}
	// User-supplied binding values must be safe to splice into PromQL.
	for name, value := range req.Bindings {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if !grafana.ValidateBindingName(name) || grafana.IsReservedBindingName(name) {
			writeError(w, http.StatusBadRequest, "binding name "+name+" is invalid or reserved")
			return
		}
		if _, allowed := required[name]; !allowed {
			writeError(w, http.StatusBadRequest, "binding "+name+" is not required by this dashboard")
			return
		}
		if !grafana.ValidateBindingValue(value) {
			writeError(w, http.StatusBadRequest, "binding value for "+name+" is not a valid PromQL value")
			return
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
				writeError(w, http.StatusBadRequest, "constant variable "+b.Name+" has a value that cannot be safely used in PromQL")
				return
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
	if len(missing) > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{
			"code":      "grafana_import_unbound_variables",
			"message":   "bind every required dashboard variable before importing",
			"variables": missing,
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

func (s *Server) handleGrafanaDashboardSupportMatrix(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"entries": grafana.GetSupportMatrix(),
		"notes":   grafana.GetSupportMatrixNotes(),
	})
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
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
