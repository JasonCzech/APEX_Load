package api

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
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

	projectID := normalizeProjectID(req.ProjectID)
	notify := func(evt recorder.SessionEvent) {
		var safeRequest *recorder.RecordedRequest
		if evt.Request != nil {
			request := sanitizeRecordedRequestForResponse(*evt.Request)
			safeRequest = &request
		}
		s.hub.BroadcastScopedJSON("", projectID, map[string]any{
			"type":       evt.Type,
			"project_id": projectID,
			"session_id": evt.SessionID,
			"request":    safeRequest,
			"session":    sanitizeRecorderSessionForResponse(evt.Session),
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

	session, err := s.recorder.StartSessionForProject(id, req.Name, projectID, filters, notify)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	resp := map[string]any{
		"session":      sanitizeRecorderSessionForResponse(session),
		"proxy_addr":   s.recorder.ProxyAddr(),
		"ca_cert_path": s.recorder.CACertPath(),
	}
	if s.desktopMode {
		// Desktop proxy credentials are a separate per-launch capability. This
		// response is itself behind the desktop API capability middleware; hosted
		// deployments must never expose environment-backed proxy credentials.
		resp["proxy_auth_token"] = s.recorder.ProxyAuthToken()
		w.Header().Set("Cache-Control", "no-store")
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
	writeJSON(w, http.StatusOK, sanitizeRecorderSessionForResponse(session))
}

func (s *Server) handleResumeRecording(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	session, err := s.recorder.ResumeSession(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sanitizeRecorderSessionForResponse(session))
}

func (s *Server) handleStopRecording(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	session, err := s.recorder.StopSession(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sanitizeRecorderSessionForResponse(session))
}

func (s *Server) handleGetRecording(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	includeRequests := queryIncludes(r.URL.Query(), "requests") || strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("include_requests")), "true")
	sinceSeq := 0
	if rawSince := strings.TrimSpace(r.URL.Query().Get("since")); rawSince != "" {
		parsed, err := strconv.Atoi(rawSince)
		if err != nil || parsed < 0 {
			writeError(w, http.StatusBadRequest, "since must be a non-negative request sequence")
			return
		}
		sinceSeq = parsed
		includeRequests = true
	}
	session, ok := s.recorder.GetSessionWithOptions(id, includeRequests, sinceSeq)
	if !ok {
		writeError(w, http.StatusNotFound, "recording session not found")
		return
	}
	raw := rawRecordingExportRequested(r)
	if raw && !s.canExportRawRecording(r, session.ProjectID) {
		s.recordAuditEvent(r, "recording.session_raw", "denied", "recording", id, session.ProjectID, map[string]any{"reason": "insufficient_role"})
		writeError(w, http.StatusForbidden, "raw recording session access requires editor, project_admin, or system_admin access")
		return
	}
	if raw {
		w.Header().Set("X-APEXLoad-Sensitivity", scrub.SensitivityRaw)
		writeJSON(w, http.StatusOK, session)
		return
	}
	w.Header().Set("X-APEXLoad-Sensitivity", scrub.SensitivityScrubbed)
	writeJSON(w, http.StatusOK, sanitizeRecorderSessionForResponse(session))
}

func (s *Server) handleListRecordings(w http.ResponseWriter, r *http.Request) {
	projectID := r.URL.Query().Get("project_id")
	includeRequests := queryIncludes(r.URL.Query(), "requests") || strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("include_requests")), "true")
	raw := rawRecordingExportRequested(r)
	if raw && !s.canExportRawRecording(r, projectID) {
		s.recordAuditEvent(r, "recording.session_list_raw", "denied", "recording", "", projectID, map[string]any{"reason": "insufficient_role"})
		writeError(w, http.StatusForbidden, "raw recording session access requires editor, project_admin, or system_admin access")
		return
	}
	sessions := s.recorder.ListSessionSummaries()
	if includeRequests {
		sessions = s.recorder.ListSessions()
	}
	if projectID != "" {
		filtered := sessions[:0]
		for _, session := range sessions {
			if session.ProjectID == projectID {
				filtered = append(filtered, session)
			}
		}
		sessions = filtered
	}
	active := s.recorder.ActiveSessionSummary()
	if raw {
		w.Header().Set("X-APEXLoad-Sensitivity", scrub.SensitivityRaw)
	} else {
		w.Header().Set("X-APEXLoad-Sensitivity", scrub.SensitivityScrubbed)
		for index := range sessions {
			sessions[index] = sanitizeRecorderSessionForResponse(sessions[index])
		}
		active = sanitizeRecorderSessionForResponse(active)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sessions": sessions,
		"count":    len(sessions),
		"active":   active,
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
	writeJSON(w, http.StatusOK, sanitizeRecordedRequestForResponse(*req))
}

func sanitizeRecorderSessionForResponse(session *recorder.Session) *recorder.Session {
	if session == nil {
		return nil
	}
	clone := &recorder.Session{
		ID:           session.ID,
		Name:         session.Name,
		ProjectID:    session.ProjectID,
		Status:       session.Status,
		StartedAt:    session.StartedAt,
		BaseURL:      scrub.ValueURL(session.BaseURL, scrub.Policy{}),
		RequestCount: session.RequestCount,
		LastSeq:      session.LastSeq,
		Filters: recorder.SessionFilters{
			ExcludeStatic:         session.Filters.ExcludeStatic,
			ExcludeStaticSet:      session.Filters.ExcludeStaticSet,
			ExcludeHosts:          append([]string(nil), session.Filters.ExcludeHosts...),
			IncludeHosts:          append([]string(nil), session.Filters.IncludeHosts...),
			BodyCaptureLimitBytes: session.Filters.BodyCaptureLimitBytes,
			RedactHeaders:         append([]string(nil), session.Filters.RedactHeaders...),
			CaptureCookies:        session.Filters.CaptureCookies,
		},
	}
	if session.StoppedAt != nil {
		stoppedAt := *session.StoppedAt
		clone.StoppedAt = &stoppedAt
	}
	if len(session.Requests) > 0 {
		clone.Requests = make([]recorder.RecordedRequest, len(session.Requests))
		for index, request := range session.Requests {
			clone.Requests[index] = sanitizeRecordedRequestForResponse(request)
		}
	}
	return clone
}

func sanitizeRecordedRequestForResponse(request recorder.RecordedRequest) recorder.RecordedRequest {
	policy := scrub.Policy{}
	request.URL = scrub.ValueURL(request.URL, policy)
	request.QueryString = scrub.Value(request.QueryString, policy)
	request.Headers = scrub.Headers(request.Headers, policy)
	request.HeadersMulti = sanitizeRecordedHeaderValues(request.HeadersMulti, policy)
	request.RawHeaders = nil
	request.RawHeadersMulti = nil
	request.Body = scrub.Value(request.Body, policy)
	request.ResponseHeaders = scrub.Headers(request.ResponseHeaders, policy)
	request.ResponseHeadersMulti = sanitizeRecordedHeaderValues(request.ResponseHeadersMulti, policy)
	request.ResponseBody = scrub.Value(request.ResponseBody, policy)
	request.StepName = scrub.Value(request.StepName, policy)
	request.TransactionName = scrub.Value(request.TransactionName, policy)
	request.Note = scrub.Value(request.Note, policy)
	if len(request.WebSocketMessages) > 0 {
		messages := make([]recorder.RecordedWebSocketMessage, len(request.WebSocketMessages))
		copy(messages, request.WebSocketMessages)
		for index := range messages {
			messages[index].Payload = scrub.Value(messages[index].Payload, policy)
		}
		request.WebSocketMessages = messages
	}
	return request
}

func sanitizeRecordedHeaderValues(headers map[string][]string, policy scrub.Policy) map[string][]string {
	if len(headers) == 0 {
		return headers
	}
	result := make(map[string][]string, len(headers))
	for name, values := range headers {
		redacted := make([]string, len(values))
		for index, value := range values {
			if policy.SensitiveHeader(name) {
				redacted[index] = scrub.Redacted
			} else {
				redacted[index] = scrub.Value(value, policy)
			}
		}
		result[name] = redacted
	}
	return result
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
	if !decodeOptionalJSONBody(w, r, &req) {
		return
	}

	result := recorder.ConvertSessionWithOptions(session, req.Name, recorder.ConvertOptions{Parameterize: req.Parameterize})
	if result.Script != nil {
		result.Script.ProjectID = normalizeProjectID(session.ProjectID)
	}

	safeScript, _, err := applySuggestedSecretBindings(result.Script, 0)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "failed to prepare recorded credentials: "+err.Error())
		return
	}
	if err := validateConvertedScriptForCommit(safeScript); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "recording did not produce an executable script: "+err.Error())
		return
	}
	stored, err := s.runner.StoreScriptDurable(safeScript)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save recorded script: "+err.Error())
		return
	}
	if result.Script != nil && stored.ID != "" {
		if err := s.persistRecordedBaseline(stored.ID, "proxy_recording", recorder.SessionBaseline(session)); err != nil {
			slog.Warn("failed to persist proxy recording baseline", "script_id", stored.ID, "error", err)
		}
	}

	response := s.newScriptImportResponseWithSourceSecrets(r, scriptImportResponseOptions{
		Script:       result.Script,
		Warnings:     result.Warnings,
		Correlations: result.Correlations,
	})
	response["script"] = maskStoredScript(stored)
	response["scripts"] = []any{maskStoredScript(stored)}
	writeJSON(w, http.StatusCreated, response)
}

func validateConvertedScriptForCommit(script *dsl.Script) error {
	if script == nil {
		return fmt.Errorf("conversion produced no script")
	}
	if issues := validateScript(script); len(issues) > 0 {
		return fmt.Errorf("%s", strings.Join(issues, "; "))
	}
	return nil
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
	if !decodeOptionalJSONBody(w, r, &req) {
		return
	}

	result := recorder.ConvertSessionWithOptions(session, req.Name, recorder.ConvertOptions{Parameterize: req.Parameterize})
	if result.Script != nil {
		result.Script.ProjectID = normalizeProjectID(session.ProjectID)
	}
	writeJSON(w, http.StatusOK, s.newScriptImportResponseWithSourceSecrets(r, scriptImportResponseOptions{
		Script:       result.Script,
		Warnings:     result.Warnings,
		Correlations: result.Correlations,
	}))
}

func (s *Server) handleExportRecording(w http.ResponseWriter, r *http.Request) {
	extendArtifactWriteDeadline(w)
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
	active := s.recorder.ActiveSessionSummary()
	var activeInfo any
	if active != nil {
		activeInfo = map[string]any{
			"id":            active.ID,
			"name":          active.Name,
			"status":        active.Status,
			"request_count": active.RequestCount,
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
	if s.desktopMode {
		resp["proxy_auth_token"] = s.recorder.ProxyAuthToken()
		w.Header().Set("Cache-Control", "no-store")
	}
	writeJSON(w, http.StatusOK, resp)
}
