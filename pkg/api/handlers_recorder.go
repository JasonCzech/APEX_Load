package api

import "github.com/jasonczech/apexload/pkg/internal/importreview"

import (
	"github.com/jasonczech/apexload/pkg/recordbridge"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/recorder"
	"github.com/jasonczech/apexload/pkg/recordingprofile"
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
		// CaptureCookies, when set, wins over the recording profile's.
		CaptureCookies *bool `json:"capture_cookies,omitempty"`
		// RecordingProfileID names a project recording profile: its host
		// filters, capture limit and cookie capture fill the options left
		// unset here, and conversions of the session apply it.
		RecordingProfileID string `json:"recording_profile_id,omitempty"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.Name == "" {
		req.Name = "Recording Session"
	}

	id := uuid.New().String()

	projectID := normalizeProjectID(req.ProjectID)
	profile, err := s.projectRecordingProfile(projectID, req.RecordingProfileID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// A session the caller's browser cannot reach records nothing: refuse it,
	// with the reason, while the proxy is not listening or listens only on a
	// loopback address this caller is not on.
	if proxy := recorderProxyAvailability(s.recorder.ProxyState(), s.recorderClientIsLocal(r)); proxy["available"] != true {
		reason, _ := proxy["reason"].(string)
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":       "HTTP proxy recording is not available to this browser: " + reason,
			"code":        "proxy_unavailable",
			"reason_code": proxy["reason_code"],
		})
		return
	}
	if profile != nil {
		// A profile's host filters add to the ones the request names: typing
		// one more host must not silently drop the profile's list.
		req.IncludeHosts = mergeRecorderHostFilters(profile.IncludeHosts, req.IncludeHosts)
		req.ExcludeHosts = mergeRecorderHostFilters(profile.ExcludeHosts, req.ExcludeHosts)
		if req.BodyCaptureLimitBytes == 0 {
			req.BodyCaptureLimitBytes = profile.BodyCaptureLimitBytes
		}
	}
	notify := func(evt recorder.SessionEvent) {
		s.broadcastRecorderSessionEvent(projectID, evt)
	}

	filters := recorder.SessionFilters{
		ExcludeHosts:          req.ExcludeHosts,
		IncludeHosts:          req.IncludeHosts,
		BodyCaptureLimitBytes: req.BodyCaptureLimitBytes,
		RedactHeaders:         req.RedactHeaders,
		CaptureCookies:        recorderCaptureCookies(req.CaptureCookies, profile),
		RecordingProfileID:    req.RecordingProfileID,
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
		"session":    sanitizeRecorderSessionForResponse(session),
		"proxy_addr": s.recorder.ProxyAddr(),
	}
	if s.recorderClientIsLocal(r) {
		resp["ca_cert_path"] = s.recorder.CACertPath()
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

func (s *Server) broadcastRecorderSessionEvent(projectID string, evt recorder.SessionEvent) {
	var safeRequest *recorder.RecordedRequest
	if evt.Request != nil {
		request := sanitizeRecordedRequestForResponse(*evt.Request)
		safeRequest = &request
	}
	payload := map[string]any{
		"type":       evt.Type,
		"project_id": projectID,
		"session_id": evt.SessionID,
		"request":    safeRequest,
		"session":    sanitizeRecorderSessionForResponse(evt.Session),
	}
	if evt.Warning != "" {
		payload["warning"] = scrub.Value(evt.Warning, scrub.Policy{})
	}
	s.hub.BroadcastScopedJSON("", projectID, payload)
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

func (s *Server) handleStartRecordingTransaction(w http.ResponseWriter, r *http.Request) {
	s.recordingHTTPService().HandleStartRecordingTransaction(w, r)
}

func (s *Server) handleEndRecordingTransaction(w http.ResponseWriter, r *http.Request) {
	s.recordingHTTPService().HandleEndRecordingTransaction(w, r)
}

func (s *Server) handleAddRecordingNote(w http.ResponseWriter, r *http.Request) {
	s.recordingHTTPService().HandleAddRecordingNote(w, r)
}

// handleRecordingMarker marks a transaction boundary or a note on a proxy
// recording, so an operator capturing a device can name each step as they
// perform it: 404 unknown session, 409 not recording or paused, 400 invalid.
func (s *Server) handleRecordingMarker(w http.ResponseWriter, r *http.Request, kind string) {
	s.recordingHTTPService().HandleRecordingMarker(w, r, kind)
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
	s.recordingHTTPService().HandleGetRecording(w, r)
}

func (s *Server) handleListRecordings(w http.ResponseWriter, r *http.Request) {
	projectID := authorizedProjectOr(r, r.URL.Query().Get("project_id"))
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
	active := s.visibleRecorderSessionForRequest(r, s.recorder.ActiveSessionSummary(), projectID)
	if raw {
		w.Header().Set("X-APEXLoad-Sensitivity", scrub.SensitivityRaw)
	} else {
		w.Header().Set("X-APEXLoad-Sensitivity", scrub.SensitivityScrubbed)
		for index := range sessions {
			sessions[index] = sanitizeRecorderSessionForResponse(sessions[index])
		}
		active = sanitizeRecorderSessionForResponse(active)
	}
	// A listing keeps each session's open transactions; its markers come
	// with the session itself (GET /api/v1/recorder/{id}).
	for _, session := range sessions {
		if session != nil {
			session.Markers = nil
		}
	}
	if active != nil {
		active.Markers = nil
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
	s.recordingHTTPService().HandleUpdateRecordedRequest(w, r)
}

func sanitizeRecorderSessionForResponse(session *recorder.Session) *recorder.Session {
	return recordbridge.SanitizeRecorderSessionForResponse(session)
}

func sanitizeRecorderWarningsForResponse(warnings []string) []string {
	return recordbridge.SanitizeRecorderWarningsForResponse(warnings)
}

func sanitizeRecordedRequestForResponse(request recorder.RecordedRequest) recorder.RecordedRequest {
	return recordbridge.SanitizeRecordedRequestForResponse(request)
}

func sanitizeRecordedHeaderValues(headers map[string][]string, policy scrub.Policy) map[string][]string {
	return recordbridge.SanitizeRecordedHeaderValues(headers, policy)
}

func (s *Server) handleConvertRecording(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	session, ok := s.recorder.GetSession(id)
	if !ok {
		writeError(w, http.StatusNotFound, "recording session not found")
		return
	}

	var req recordbridge.RecordingConversionRequest
	if !decodeOptionalJSONBody(w, r, &req) {
		return
	}
	if !validHTTPVersionMode(w, req.HTTPVersionMode) {
		return
	}
	profileID := strings.TrimSpace(req.RecordingProfileID)
	if profileID == "" {
		profileID = strings.TrimSpace(req.ProfileID)
	}
	profile, err := s.sessionRecordingProfile(session, profileID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	composed, convErr := recordbridge.ComposeRecordingSessionConversion(session, profile, req)
	if convErr != nil {
		writeError(w, convErr.Status, convErr.Message)
		return
	}
	defer clearCredentialRequirementValues(composed.ProtectedRequirements)

	// This conversion is produced directly from the server-owned recording
	// snapshot. The operator already supplied every captured request credential,
	// so retain those exact executable values in the private durable copy rather
	// than replacing them with unconfigured {{secret.*}} references.
	importedScript, err := preparePersistentImportedLibraryScript(
		composed.Script,
		nil,
		"recorder",
	)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "failed to prepare recorded credentials: "+err.Error())
		return
	}
	if err := validateConvertedScriptForCommit(importedScript); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "recording did not produce an executable script: "+err.Error())
		return
	}
	stored, _, err := s.runner.CreateImportedScriptDurableReportingReplay(
		importedScript,
		"recorder",
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save recorded script: "+err.Error())
		return
	}
	if composed.Script != nil && stored.ID != "" {
		// The conversion's own baseline lines up with the options it used
		// (resource mode, profile filters, async shaping).
		if err := s.persistRecordedBaseline(stored.ID, "proxy_recording", stored.Script, composed.Baseline); err != nil {
			slog.Warn("failed to persist proxy recording baseline", "script_id", stored.ID, "error", err)
		}
	}

	response := s.newScriptImportResponseWithSourceSecrets(r, scriptImportResponseOptions{
		Script:                   composed.Script,
		Warnings:                 composed.Warnings,
		Diagnostics:              composed.Diagnostics,
		UnsupportedElements:      composed.Diagnostics.Strings(),
		Correlations:             composed.Correlations,
		ReviewSections:           composed.Sections,
		Parameterization:         buildImportParameterization(composed.Script, composed.PreApplied),
		Preflight:                composed.Preflight,
		EffectiveSettings:        composed.EffectiveSettings,
		ProtectedRequirements:    composed.ProtectedRequirements,
		PreparedCredentialReview: composed.PreparedCredentialReview,
		CredentialReviewPrepared: composed.CredentialReviewPrepared,
		SkipDraftAttestation:     true,
	})
	response["script"] = maskStoredScript(stored)
	response["scripts"] = []any{maskStoredScript(stored)}
	writeScriptImportResponse(w, http.StatusCreated, response)
}

var validateConvertedScriptForCommit = importreview.ValidateConvertedScriptForCommit

func (s *Server) handlePreviewRecordingConversion(w http.ResponseWriter, r *http.Request) {
	s.recordingHTTPService().HandlePreviewRecordingConversion(w, r)
}

// recordingConversionStats counts the session's recorded requests and the
// ones marked excluded, which a conversion leaves out, for the review.
var recordingConversionStats = recordbridge.RecordingConversionStats

func (s *Server) handleExportRecording(w http.ResponseWriter, r *http.Request) {
	s.recordingHTTPService().HandleExportRecording(w, r)
}

func rawRecordingExportRequested(r *http.Request) bool {
	return recordbridge.RawRecordingExportRequested(r)
}

func (s *Server) canExportRawRecording(r *http.Request, projectID string) bool {
	if user, ok := AuthenticatedUserFromContext(r.Context()); ok && user != nil {
		return user.hasGlobalRole(RoleEditor, RoleProjectAdmin, RoleSystemAdmin) ||
			userHasProjectOrParentRole(s.store, user, projectID, RoleEditor, RoleProjectAdmin)
	}
	settings := s.authSettingsForRequest()
	return isAuthBypassMode(settings.Mode)
}

func (s *Server) handleRecorderCACert(w http.ResponseWriter, r *http.Request) {
	s.recorder.HandleCACert(w, r)
}

func sanitizeDownloadName(name string) string { return recordbridge.SanitizeDownloadName(name) }

func (s *Server) handleRecorderStatus(w http.ResponseWriter, r *http.Request) {
	projectID := authorizedProjectOr(r, strings.TrimSpace(r.URL.Query().Get("project_id")))
	active := s.visibleRecorderSessionForRequest(r, s.recorder.ActiveSessionSummary(), projectID)
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
	browserActive := s.browserRecording.Active()
	if browserActive != nil && projectID != "" && normalizeProjectID(browserActive.ProjectID) != normalizeProjectID(projectID) {
		browserActive = nil
	}
	localClient := s.recorderClientIsLocal(r)
	resp := map[string]any{
		"proxy_addr":   s.recorder.ProxyAddr(),
		"proxy":        recorderProxyAvailability(s.recorder.ProxyState(), localClient),
		"local_client": localClient,
		"active":       activeInfo,
		"browser": map[string]any{
			"availability": s.browserRecordingAvailability(),
			"active":       browserActive,
			"install":      s.browserInstall.snapshot(),
		},
	}
	// The CA file's path is only useful, and only shown, to a browser on the
	// controller's host; anyone else downloads the certificate.
	if localClient {
		resp["ca_cert_path"] = s.recorder.CACertPath()
	}
	if s.desktopMode {
		resp["proxy_auth_token"] = s.recorder.ProxyAuthToken()
		w.Header().Set("Cache-Control", "no-store")
	}
	writeJSON(w, http.StatusOK, resp)
}

// mergeRecorderHostFilters is the profile's hosts followed by the request's,
// trimmed, once each (hosts compare case-insensitively).
func mergeRecorderHostFilters(profileHosts, requestHosts []string) []string {
	return recordbridge.MergeRecorderHostFilters(profileHosts, requestHosts)
}

// recorderProxyLoopbackOnly is the reason code for a proxy listening only on
// the controller host's loopback interface, out of a remote browser's reach.
const recorderProxyLoopbackOnly = "loopback_only"

// recorderProxyAvailability is whether the viewing browser can record through
// the proxy: it must be listening, and a loopback-only listener serves only a
// browser on the controller's host. reason is operator-facing; the dashboard
// words reason_code for authors.
func recorderProxyAvailability(proxy recorder.ProxyState, localClient bool) map[string]any {
	out := map[string]any{
		"available":               proxy.Running,
		"running":                 proxy.Running,
		"private_targets_allowed": proxy.PrivateTargetsAllowed,
	}
	switch {
	case !proxy.Running:
		out["reason_code"], out["reason"] = proxy.ReasonCode, proxy.Reason
	case proxy.Loopback && !localClient:
		out["available"] = false
		out["reason_code"] = recorderProxyLoopbackOnly
		out["reason"] = "the recording proxy listens on " + proxy.Addr + " on the controller host, which this browser cannot reach; set APEXLOAD_RECORDER_PROXY_ADDR to a reachable address (with APEXLOAD_RECORDER_PROXY_TOKEN) to record through it"
	}
	return out
}

// recorderClientIsLocal reports whether the browser viewing the dashboard runs
// on the controller's host: the desktop app, or a request from a loopback peer
// to a loopback name. Any forwarding header naming another host or client (a
// reverse proxy or auth sidecar in front, even one that rewrites Host to the
// loopback upstream) makes it remote. Only a local browser can use a
// loopback-bound proxy or open the CA file at its path. The headers can only
// make a request look less local, so they need no trust.
func (s *Server) recorderClientIsLocal(r *http.Request) bool {
	if s.desktopMode {
		return true
	}
	if !recorderHostIsLoopback(r.RemoteAddr) || !recorderHostIsLoopback(r.Host) {
		return false
	}
	for _, name := range []string{"X-Forwarded-Host", "X-Forwarded-For", "X-Real-IP"} {
		for _, value := range r.Header.Values(name) {
			for _, host := range strings.Split(value, ",") {
				if !recorderHostIsLoopback(host) {
					return false
				}
			}
		}
	}
	for _, value := range r.Header.Values("Forwarded") {
		for _, element := range strings.Split(value, ",") {
			for _, pair := range strings.Split(element, ";") {
				key, val, ok := strings.Cut(strings.TrimSpace(pair), "=")
				if !ok {
					continue
				}
				switch strings.ToLower(strings.TrimSpace(key)) {
				case "for", "host":
					if !recorderHostIsLoopback(strings.Trim(strings.TrimSpace(val), `"`)) {
						return false
					}
				}
			}
		}
	}
	return true
}

func recorderHostIsLoopback(hostport string) bool {
	host := strings.TrimSpace(hostport)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(strings.Trim(host, "[]"))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// sessionRecordingProfile resolves the recording profile a conversion of a
// proxy session applies: the one named, or the one it was started with.
func (s *Server) sessionRecordingProfile(session *recorder.Session, profileID string) (*recordingprofile.Profile, error) {
	if profileID == "" {
		profileID = session.Filters.RecordingProfileID
	}
	var profile *recordingprofile.Profile
	var err error
	profile, err = s.projectRecordingProfile(normalizeProjectID(session.ProjectID), profileID)
	if err != nil || profile == nil || !strings.EqualFold(profile.ID, session.Filters.RecordingProfileID) {
		return profile, err
	}
	// The session recorded the profile's hosts merged with the ones typed at
	// start; converting with that profile merges them the same way, so a typed
	// include host is not dropped again by the profile's own list (and a host
	// the profile now excludes still is).
	scoped := *profile
	scoped.IncludeHosts = mergeRecorderHostFilters(profile.IncludeHosts, session.Filters.IncludeHosts)
	scoped.ExcludeHosts = mergeRecorderHostFilters(profile.ExcludeHosts, session.Filters.ExcludeHosts)
	return &scoped, nil
}

// recorderCaptureCookies is a proxy session's cookie capture: the request's
// explicit choice, else the recording profile's, else off.
func recorderCaptureCookies(requested *bool, profile *recordingprofile.Profile) bool {
	return recordbridge.RecorderCaptureCookies(requested, profile)
}
