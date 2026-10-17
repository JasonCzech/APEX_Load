package recordbridge

import (
	"errors"
	"fmt"
	"github.com/jasonczech/apexload/pkg/recorder"
	"github.com/jasonczech/apexload/pkg/recorder/marker"
	"github.com/jasonczech/apexload/pkg/recordingprofile"
	"github.com/jasonczech/apexload/pkg/scrub"
	"net/http"
	"strconv"
	"strings"
)

func (s *Service) HandleStartRecordingTransaction(w http.ResponseWriter, r *http.Request) {
	s.HandleRecordingMarker(w, r, marker.KindTransactionStart)
}

func (s *Service) HandleEndRecordingTransaction(w http.ResponseWriter, r *http.Request) {
	s.HandleRecordingMarker(w, r, marker.KindTransactionEnd)
}

func (s *Service) HandleAddRecordingNote(w http.ResponseWriter, r *http.Request) {
	s.HandleRecordingMarker(w, r, marker.KindNote)
}

func (s *Service) HandleRecordingMarker(w http.ResponseWriter, r *http.Request, kind string) {
	var req struct {
		Name string `json:"name,omitempty"`
		Text string `json:"text,omitempty"`
	}
	if !s.decodeOptionalJSONBody(w, r, &req) {
		return
	}
	session, added, err := s.Recorder.AddMarker(r.PathValue("id"), marker.Marker{Kind: kind, Name: req.Name, Text: req.Text})
	switch {
	case errors.Is(err, recorder.ErrSessionNotFound):
		s.writeError(w, http.StatusNotFound, "recording session not found")
		return
	case errors.Is(err, recorder.ErrSessionNotLive):
		s.writeError(w, http.StatusConflict, err.Error())
		return
	case err != nil:
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": sanitizeRecorderSessionForResponse(session), "marker": added})
}

func (s *Service) HandleGetRecording(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	includeRequests := queryIncludes(r.URL.Query(), "requests") || strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("include_requests")), "true")
	sinceSeq := 0
	tail := 0
	if rawSince := strings.TrimSpace(r.URL.Query().Get("since")); rawSince != "" {
		parsed, err := strconv.Atoi(rawSince)
		if err != nil || parsed < 0 {
			s.writeError(w, http.StatusBadRequest, "since must be a non-negative request sequence")
			return
		}
		sinceSeq = parsed
		includeRequests = true
	}
	if rawTail := strings.TrimSpace(r.URL.Query().Get("tail")); rawTail != "" {
		parsed, err := strconv.Atoi(rawTail)
		if err != nil || parsed < 1 || parsed > 100 {
			s.writeError(w, http.StatusBadRequest, "tail must be between 1 and 100")
			return
		}
		tail = parsed
		includeRequests = true
	}
	session, ok := s.Recorder.GetSessionWindow(id, includeRequests, sinceSeq, tail)
	if !ok {
		s.writeError(w, http.StatusNotFound, "recording session not found")
		return
	}
	raw := rawRecordingExportRequested(r)
	if raw && !s.canExportRaw(r, session.ProjectID) {
		s.audit(r, "recording.session_raw", "denied", "recording", id, session.ProjectID, map[string]any{"reason": "insufficient_role"})
		s.writeError(w, http.StatusForbidden, "raw recording session access requires editor, project_admin, or system_admin access")
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

func (s *Service) HandleUpdateRecordedRequest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	seq, err := strconv.Atoi(r.PathValue("seq"))
	if err != nil || seq <= 0 {
		s.writeError(w, http.StatusBadRequest, "invalid request sequence")
		return
	}

	var update recorder.RequestUpdate
	if !s.decodeJSONBody(w, r, &update) {
		return
	}

	req, err := s.Recorder.UpdateRequest(id, seq, update)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sanitizeRecordedRequestForResponse(*req))
}

func sanitizeRecorderSessionForResponse(session *recorder.Session) *recorder.Session {
	if session == nil {
		return nil
	}
	clone := &recorder.Session{
		ID:                       session.ID,
		Name:                     session.Name,
		ProjectID:                session.ProjectID,
		Status:                   session.Status,
		StartedAt:                session.StartedAt,
		BaseURL:                  scrub.ValueURL(session.BaseURL, scrub.Policy{}),
		RequestCount:             session.RequestCount,
		LastSeq:                  session.LastSeq,
		DroppedRequests:          session.DroppedRequests,
		DroppedWebSocketMessages: session.DroppedWebSocketMessages,
		RejectedRequests:         session.RejectedRequests,
		LastRejectedHost:         session.LastRejectedHost,
		FilteredRequests:         session.FilteredRequests,
		LastFilteredHost:         session.LastFilteredHost,
		Truncated:                session.Truncated,
		Warnings:                 sanitizeRecorderWarningsForResponse(session.Warnings),
		Filters: recorder.SessionFilters{
			ExcludeStatic:         session.Filters.ExcludeStatic,
			ExcludeStaticSet:      session.Filters.ExcludeStaticSet,
			ExcludeHosts:          append([]string(nil), session.Filters.ExcludeHosts...),
			IncludeHosts:          append([]string(nil), session.Filters.IncludeHosts...),
			BodyCaptureLimitBytes: session.Filters.BodyCaptureLimitBytes,
			RedactHeaders:         append([]string(nil), session.Filters.RedactHeaders...),
			CaptureCookies:        session.Filters.CaptureCookies,
			RecordingProfileID:    session.Filters.RecordingProfileID,
		},
		// Operator-authored transaction names and notes, not recorded traffic.
		Markers:          append([]marker.Marker(nil), session.Markers...),
		OpenTransactions: append([]string(nil), session.OpenTransactions...),
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

func sanitizeRecorderWarningsForResponse(warnings []string) []string {
	if warnings == nil {
		return nil
	}
	safeWarnings := make([]string, len(warnings))
	for index, warning := range warnings {
		safeWarnings[index] = scrub.Value(warning, scrub.Policy{})
	}
	return safeWarnings
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

func (s *Service) HandleExportRecording(w http.ResponseWriter, r *http.Request) {
	s.extendWriteDeadline(w)
	id := r.PathValue("id")
	session, ok := s.Recorder.GetSession(id)
	if !ok {
		s.writeError(w, http.StatusNotFound, "recording session not found")
		return
	}

	format := strings.ToLower(r.URL.Query().Get("format"))
	if format == "" {
		format = "har"
	}
	if format != "har" {
		s.writeError(w, http.StatusBadRequest, "unsupported recording export format: "+format)
		return
	}

	includeExcluded := r.URL.Query().Get("include_excluded") == "true"
	data, err := recorder.ExportHAR(session, includeExcluded)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "failed to export HAR: "+err.Error())
		return
	}

	raw := rawRecordingExportRequested(r)
	if raw && !s.canExportRaw(r, session.ProjectID) {
		s.audit(r, "recording.export_har_raw", "denied", "recording", id, session.ProjectID, map[string]any{"reason": "insufficient_role"})
		s.writeError(w, http.StatusForbidden, "raw recording export requires editor, project_admin, or system_admin access")
		return
	}

	sensitivity := scrub.SensitivityRaw
	if !raw {
		scrubbed, err := scrub.HAR(data, scrub.Policy{})
		if err != nil {
			s.writeError(w, http.StatusInternalServerError, "failed to scrub HAR: "+err.Error())
			return
		}
		data = scrubbed
		sensitivity = scrub.SensitivityScrubbed
	}
	s.audit(r, "recording.export_har", "success", "recording", id, session.ProjectID, map[string]any{"sensitivity": sensitivity, "raw": raw})

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

func mergeRecorderHostFilters(profileHosts, requestHosts []string) []string {
	merged := make([]string, 0, len(profileHosts)+len(requestHosts))
	seen := make(map[string]bool, len(profileHosts)+len(requestHosts))
	for _, hosts := range [][]string{profileHosts, requestHosts} {
		for _, host := range hosts {
			host = strings.TrimSpace(host)
			key := strings.ToLower(host)
			if host == "" || seen[key] {
				continue
			}
			seen[key] = true
			merged = append(merged, host)
		}
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

func recorderCaptureCookies(requested *bool, profile *recordingprofile.Profile) bool {
	if requested != nil {
		return *requested
	}
	return profile != nil && profile.CaptureCookies != nil && *profile.CaptureCookies
}
