package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jasonczech/stormrunner/pkg/recorder"
)

func (s *Server) handleStartRecording(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name                  string   `json:"name"`
		ProjectID             string   `json:"project_id,omitempty"`
		ExcludeStatic         bool     `json:"exclude_static"`
		ExcludeHosts          []string `json:"exclude_hosts,omitempty"`
		IncludeHosts          []string `json:"include_hosts,omitempty"`
		BodyCaptureLimitBytes int      `json:"body_capture_limit_bytes,omitempty"`
		RedactHeaders         []string `json:"redact_headers,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.Name == "" {
		req.Name = "Recording Session"
	}

	id := uuid.New().String()

	notify := func(evt recorder.SessionEvent) {
		projectID := ""
		if evt.Session != nil {
			projectID = evt.Session.ProjectID
		}
		s.hub.BroadcastJSON(map[string]any{
			"type":       evt.Type,
			"project_id": projectID,
			"session_id": evt.SessionID,
			"request":    evt.Request,
			"session":    evt.Session,
		})
	}

	session, err := s.recorder.StartSessionForProject(id, req.Name, normalizeProjectID(req.ProjectID), recorder.SessionFilters{
		ExcludeStatic:         req.ExcludeStatic,
		ExcludeHosts:          req.ExcludeHosts,
		IncludeHosts:          req.IncludeHosts,
		BodyCaptureLimitBytes: req.BodyCaptureLimitBytes,
		RedactHeaders:         req.RedactHeaders,
	}, notify)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	s.recordAuditEvent(r, "recorder.session.start", "success", "recording_session", session.ID, session.ProjectID, map[string]any{"name": session.Name, "status": session.Status})

	writeJSON(w, http.StatusCreated, map[string]any{
		"session":      session,
		"proxy_addr":   s.recorder.ProxyAddr(),
		"ca_cert_path": s.recorder.CACertPath(),
	})
}

func (s *Server) handlePauseRecording(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	session, err := s.recorder.PauseSession(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "recorder.session.pause", "success", "recording_session", session.ID, session.ProjectID, map[string]any{"name": session.Name, "status": session.Status})
	writeJSON(w, http.StatusOK, session)
}

func (s *Server) handleResumeRecording(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	session, err := s.recorder.ResumeSession(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "recorder.session.resume", "success", "recording_session", session.ID, session.ProjectID, map[string]any{"name": session.Name, "status": session.Status})
	writeJSON(w, http.StatusOK, session)
}

func (s *Server) handleStopRecording(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	session, err := s.recorder.StopSession(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "recorder.session.stop", "success", "recording_session", session.ID, session.ProjectID, map[string]any{"name": session.Name, "status": session.Status})
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
	session, ok := s.recorder.GetSession(id)
	if !ok {
		writeError(w, http.StatusNotFound, "recording session not found")
		return
	}
	if !s.recorder.DeleteSession(id) {
		writeError(w, http.StatusNotFound, "recording session not found")
		return
	}
	s.recordAuditEvent(r, "recorder.session.delete", "success", "recording_session", id, session.ProjectID, map[string]any{"name": session.Name})
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
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
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
		Name string `json:"name"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	result := recorder.ConvertSession(session, req.Name)
	if result.Script != nil {
		result.Script.ProjectID = normalizeProjectID(session.ProjectID)
	}

	stored := s.runner.StoreScript(result.Script)
	stats := newRecorderImportStats(len(result.Script.Actions), len(result.Correlations))
	s.recordAuditEvent(r, "recorder.session.convert", "success", "recording_session", session.ID, session.ProjectID, map[string]any{"script_id": stored.ID, "script_name": stored.Name, "converted_actions": stats["converted_actions"], "correlations_detected": stats["correlations_detected"]})

	writeJSON(w, http.StatusCreated, newScriptImportResponse(scriptImportResponseOptions{
		Script:       stored,
		Warnings:     result.Warnings,
		Stats:        stats,
		Correlations: result.Correlations,
	}))
}

func (s *Server) handlePreviewRecordingConversion(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	session, ok := s.recorder.GetSession(id)
	if !ok {
		writeError(w, http.StatusNotFound, "recording session not found")
		return
	}

	var req struct {
		Name string `json:"name"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	result := recorder.ConvertSession(session, req.Name)
	if result.Script != nil {
		result.Script.ProjectID = normalizeProjectID(session.ProjectID)
	}
	stats := newRecorderImportStats(len(result.Script.Actions), len(result.Correlations))
	writeJSON(w, http.StatusOK, newScriptImportResponse(scriptImportResponseOptions{
		Result:       result,
		Script:       result.Script,
		Warnings:     result.Warnings,
		Stats:        stats,
		Correlations: result.Correlations,
	}))
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

	filename := sanitizeDownloadName(session.Name)
	if filename == "" {
		filename = "recording"
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.har"`, filename))
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

func (s *Server) handleRecorderCACert(w http.ResponseWriter, r *http.Request) {
	certPEM := s.recorder.GetCACertPEM()
	if certPEM == nil {
		// Initialize the CA lazily so the cert is available for download.
		s.recorder.HandleCACert(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("Content-Disposition", `attachment; filename="stormrunner-ca.pem"`)
	w.Write(certPEM)
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
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	active := s.recorder.ActiveSession()
	var activeInfo any
	if active != nil && projectID != "" && normalizeProjectID(active.ProjectID) != normalizeProjectID(projectID) {
		active = nil
	}
	if active != nil {
		activeInfo = map[string]any{
			"id":            active.ID,
			"name":          active.Name,
			"status":        active.Status,
			"request_count": len(active.Requests),
			"started_at":    active.StartedAt,
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"proxy_addr":   s.recorder.ProxyAddr(),
		"ca_cert_path": s.recorder.CACertPath(),
		"active":       activeInfo,
	})
}
