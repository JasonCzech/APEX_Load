package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/recorder"
	"github.com/jasonczech/apexload/pkg/scrub"
)

func (s *Server) handleStartRecording(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name                  string   `json:"name"`
		ProjectID             string   `json:"project_id,omitempty"`
		ExcludeStatic         *bool    `json:"exclude_static"`
		ExcludeHosts          []string `json:"exclude_hosts,omitempty"`
		IncludeHosts          []string `json:"include_hosts,omitempty"`
		BodyCaptureLimitBytes int      `json:"body_capture_limit_bytes,omitempty"`
		RedactHeaders         []string `json:"redact_headers,omitempty"`
		CaptureCookies        bool     `json:"capture_cookies,omitempty"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.Name == "" {
		req.Name = "Recording Session"
	}

	id := uuid.New().String()

	notify := func(evt recorder.SessionEvent) {
		s.hub.BroadcastJSON(map[string]any{
			"type":       evt.Type,
			"session_id": evt.SessionID,
			"request":    evt.Request,
			"session":    evt.Session,
		})
	}

	filters := recorder.SessionFilters{
		ExcludeHosts:          req.ExcludeHosts,
		IncludeHosts:          req.IncludeHosts,
		BodyCaptureLimitBytes: req.BodyCaptureLimitBytes,
		RedactHeaders:         req.RedactHeaders,
		CaptureCookies:        req.CaptureCookies,
	}
	if req.ExcludeStatic != nil {
		filters.ExcludeStatic = *req.ExcludeStatic
		filters.ExcludeStaticSet = true
	}

	session, err := s.recorder.StartSessionForProject(id, req.Name, normalizeProjectID(req.ProjectID), filters, notify)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	resp := map[string]any{
		"session":      session,
		"proxy_addr":   s.recorder.ProxyAddr(),
		"ca_cert_path": s.recorder.CACertPath(),
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) handlePauseRecording(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	session, err := s.recorder.PauseSession(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (s *Server) handleResumeRecording(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	session, err := s.recorder.ResumeSession(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (s *Server) handleStopRecording(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	session, err := s.recorder.StopSession(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (s *Server) handleGetRecording(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	session, ok := s.recorder.GetSession(id)
	if !ok {
		writeError(w, http.StatusNotFound, "recording session not found")
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (s *Server) handleListRecordings(w http.ResponseWriter, r *http.Request) {
	projectID := r.URL.Query().Get("project_id")
	sessions := s.recorder.ListSessions()
	if projectID != "" {
		filtered := sessions[:0]
		for _, session := range sessions {
			if session.ProjectID == projectID {
				filtered = append(filtered, session)
			}
		}
		sessions = filtered
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sessions": sessions,
		"count":    len(sessions),
		"active":   s.recorder.ActiveSession(),
	})
}

func (s *Server) handleDeleteRecording(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.recorder.DeleteSession(id) {
		writeError(w, http.StatusNotFound, "recording session not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

func (s *Server) handleUpdateRecordedRequest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	seq, err := strconv.Atoi(r.PathValue("seq"))
	if err != nil || seq <= 0 {
		writeError(w, http.StatusBadRequest, "invalid request sequence")
		return
	}

	var update recorder.RequestUpdate
	if !decodeJSONBody(w, r, &update) {
		return
	}

	req, err := s.recorder.UpdateRequest(id, seq, update)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, req)
}

func (s *Server) handleConvertRecording(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	session, ok := s.recorder.GetSession(id)
	if !ok {
		writeError(w, http.StatusNotFound, "recording session not found")
		return
	}

	var req struct {
		Name         string `json:"name"`
		Parameterize bool   `json:"parameterize"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	result := recorder.ConvertSessionWithOptions(session, req.Name, recorder.ConvertOptions{Parameterize: req.Parameterize})
	if result.Script != nil {
		result.Script.ProjectID = normalizeProjectID(session.ProjectID)
	}

	stored := s.runner.StoreScript(result.Script)
	if result.Script != nil && stored.ID != "" {
		if err := s.persistRecordedBaseline(stored.ID, "proxy_recording", recorder.SessionBaseline(session)); err != nil {
			slog.Warn("failed to persist proxy recording baseline", "script_id", stored.ID, "error", err)
		}
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"script":       stored,
		"correlations": result.Correlations,
		"warnings":     result.Warnings,
	})
}

func (s *Server) handlePreviewRecordingConversion(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	session, ok := s.recorder.GetSession(id)
	if !ok {
		writeError(w, http.StatusNotFound, "recording session not found")
		return
	}

	var req struct {
		Name         string `json:"name"`
		Parameterize bool   `json:"parameterize"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	result := recorder.ConvertSessionWithOptions(session, req.Name, recorder.ConvertOptions{Parameterize: req.Parameterize})
	if result.Script != nil {
		result.Script.ProjectID = normalizeProjectID(session.ProjectID)
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleExportRecording(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	session, ok := s.recorder.GetSession(id)
	if !ok {
		writeError(w, http.StatusNotFound, "recording session not found")
		return
	}

	format := strings.ToLower(r.URL.Query().Get("format"))
	if format == "" {
		format = "har"
	}
	if format != "har" {
		writeError(w, http.StatusBadRequest, "unsupported recording export format: "+format)
		return
	}

	includeExcluded := r.URL.Query().Get("include_excluded") == "true"
	data, err := recorder.ExportHAR(session, includeExcluded)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to export HAR: "+err.Error())
		return
	}

	raw := rawRecordingExportRequested(r)
	if raw && !s.canExportRawRecording(r, session.ProjectID) {
		s.recordAuditEvent(r, "recording.export_har_raw", "denied", "recording", id, session.ProjectID, map[string]any{"reason": "insufficient_role"})
		writeError(w, http.StatusForbidden, "raw recording export requires editor, project_admin, or system_admin access")
		return
	}

	sensitivity := scrub.SensitivityRaw
	if !raw {
		scrubbed, err := scrub.HAR(data, scrub.Policy{})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to scrub HAR: "+err.Error())
			return
		}
		data = scrubbed
		sensitivity = scrub.SensitivityScrubbed
	}
	s.recordAuditEvent(r, "recording.export_har", "success", "recording", id, session.ProjectID, map[string]any{"sensitivity": sensitivity, "raw": raw})

	filename := sanitizeDownloadName(session.Name)
	if filename == "" {
		filename = "recording"
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.har"`, filename))
	w.Header().Set("X-APEXLoad-Sensitivity", sensitivity)
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

func rawRecordingExportRequested(r *http.Request) bool {
	query := r.URL.Query()
	return query.Get("raw") == "true" || query.Get("scrub") == "false"
}

func (s *Server) canExportRawRecording(r *http.Request, projectID string) bool {
	if user, ok := AuthenticatedUserFromContext(r.Context()); ok && user != nil {
		return user.hasGlobalRole(RoleEditor, RoleProjectAdmin, RoleSystemAdmin) ||
			user.hasProjectRole(projectID, RoleEditor, RoleProjectAdmin)
	}
	settings := normalizeAuthSettings(s.runner.GetSystemSettings().Auth)
	return isAuthBypassMode(settings.Mode)
}

func (s *Server) handleRecorderCACert(w http.ResponseWriter, r *http.Request) {
	s.recorder.HandleCACert(w, r)
}

func sanitizeDownloadName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return ""
	}
	var b strings.Builder
	lastDash := false
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func (s *Server) handleRecorderStatus(w http.ResponseWriter, r *http.Request) {
	active := s.recorder.ActiveSession()
	var activeInfo any
	if active != nil {
		activeInfo = map[string]any{
			"id":            active.ID,
			"name":          active.Name,
			"status":        active.Status,
			"request_count": len(active.Requests),
			"started_at":    active.StartedAt,
		}
	}
	resp := map[string]any{
		"proxy_addr":   s.recorder.ProxyAddr(),
		"ca_cert_path": s.recorder.CACertPath(),
		"active":       activeInfo,
		"browser": map[string]any{
			"availability": s.browserRecordingAvailability(),
			"active":       s.browserRecording.Active(),
			"install":      s.browserInstall.snapshot(),
		},
	}
	writeJSON(w, http.StatusOK, resp)
}
