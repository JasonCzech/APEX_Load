package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
)

type BrowserReplayStartRequest struct {
	GroupName       string                 `json:"group_name,omitempty"`
	ProjectID       string                 `json:"project_id,omitempty"`
	ReplaySessionID string                 `json:"replay_session_id,omitempty"`
	Variables       map[string]string      `json:"variables,omitempty"`
	RedactionRules  []engine.RedactionRule `json:"redaction_rules,omitempty"`
	StartPaused     bool                   `json:"start_paused,omitempty"`
	Breakpoints     []int                  `json:"breakpoints,omitempty"`
	Browser         *dsl.BrowserConfig     `json:"browser,omitempty"`
	IgnoreThinkTime *bool                  `json:"ignore_think_time,omitempty"`
	Script          *dsl.Script            `json:"script,omitempty"`
	Preflight       *ReplayPreflight       `json:"-"`
	SecretResolver  engine.SecretResolver  `json:"-"`
	// trustedPreservedImport is server-owned provenance set only for the
	// unchanged durable script loaded by the canonical stored-script route.
	// Draft JSON cannot set it.
	trustedPreservedImport  bool
	executionContext        context.Context
	releaseExecutionContext context.CancelFunc
}

type BrowserReplayStartResponse struct {
	ScriptID        string                `json:"script_id"`
	ReplaySessionID string                `json:"replay_session_id"`
	Session         *BrowserReplaySession `json:"session"`
	Preflight       *ReplayPreflight      `json:"preflight,omitempty"`
	Warnings        []string              `json:"warnings,omitempty"`
}

type BrowserReplaySessionResponse struct {
	Session *BrowserReplaySession `json:"session"`
}

func (s *Server) handleStartBrowserReplayDraftScript(w http.ResponseWriter, r *http.Request) {
	var req BrowserReplayStartRequest
	if r.Body == nil || r.ContentLength == 0 {
		writeError(w, http.StatusBadRequest, "request body is required")
		return
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.Script == nil {
		writeError(w, http.StatusBadRequest, "script is required")
		return
	}
	if err := bindDraftScriptProject(req.Script, req.ProjectID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.handleStartBrowserReplayRequest(w, r, "", req.Script, req)
}

func (s *Server) handleStartBrowserReplayScript(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	stored, ok := s.runner.GetScript(id)
	if !ok {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	if !requireAuthorizedProjectMatch(w, r, stored.ProjectID) {
		return
	}
	if err := storedScriptReplayGateError(stored.Script); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var req BrowserReplayStartRequest
	if !decodeOptionalJSONBody(w, r, &req) {
		return
	}

	script := stored.Script
	if req.Script != nil {
		script = req.Script
		if script.ID == "" {
			script.ID = stored.ID
		}
		if script.Name == "" {
			script.Name = stored.Name
		}
		var resolveErr error
		script, req.trustedPreservedImport, resolveErr = resolveStoredReplayImportScript(script, stored.Script)
		if resolveErr != nil {
			writeError(w, http.StatusBadRequest, resolveErr.Error())
			return
		}
	} else {
		// The stored-script route may apply only the bounded browser overrides
		// in applyBrowserReplayOverrides; none can change the target, proxy, or
		// credential fields. Retain server-owned import trust for that exact
		// durable script so harmless viewport/device overrides do not replace
		// an imported password with its disclosure sentinel.
		req.trustedPreservedImport = isCredentialPreservingImport(stored.Script)
	}
	if err := bindStoredScriptProject(script, stored.Script.ProjectID, req.ProjectID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	s.handleStartBrowserReplayRequest(w, r, id, script, req)
}

// OpenAPI-Responses: 409, 500, 503
func (s *Server) handleStartBrowserReplayRequest(w http.ResponseWriter, r *http.Request, id string, script *dsl.Script, req BrowserReplayStartRequest) {
	if !s.requireExecutionLeader(w) {
		return
	}
	if err := validateExternalReplaySessionID(req.ReplaySessionID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	script = applyBrowserReplayOverrides(script, req)
	var prepared bool
	script, prepared = prepareTransientExecutableScript(
		w,
		script,
		req.trustedPreservedImport,
	)
	if !prepared {
		return
	}
	resolvedScript, refWarnings, refIssues := resolveObjectRefs(s.store, script)
	if resolvedScript != nil {
		script = resolvedScript
	}
	script, prepared = prepareTransientExecutableScript(
		w,
		script,
		req.trustedPreservedImport,
	)
	if !prepared {
		return
	}
	issues := append(
		validateScriptForReplayWithImportTrust(
			script,
			req.trustedPreservedImport,
		),
		refIssues...,
	)
	warnings := append(validationWarnings(script), refWarnings...)
	secretResolver := s.runner.secretResolverForProject(script.ProjectID)
	preflight := buildReplayPreflight(script, req.Variables, issues, warnings, secretResolver)
	if !preflight.Valid {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"script_id": id,
			"valid":     preflight.Valid,
			"issues":    issues,
			"warnings":  warnings,
			"preflight": preflight,
		})
		return
	}
	if script.Protocol != "browser" {
		writeError(w, http.StatusBadRequest, "browser replay requires script.protocol to be browser")
		return
	}

	req.Preflight = preflight
	req.SecretResolver = secretResolver
	req.executionContext, req.releaseExecutionContext = s.runner.executionOperationContext(context.Background())
	session, err := s.browserReplay.Start(id, script, req)
	if err != nil {
		req.releaseExecutionContext()
		writeBrowserReplayStartError(w, err)
		return
	}
	s.recordAuditEvent(r, "browser_replay.session.start", "success", "browser_replay_session", session.ID, session.ProjectID, map[string]any{"script_id": session.ScriptID, "script_name": script.Name, "status": session.Status, "start_paused": req.StartPaused})
	response := BrowserReplayStartResponse{
		ScriptID:        id,
		ReplaySessionID: session.ID,
		Session:         session,
		Preflight:       preflight,
		Warnings:        warnings,
	}
	writeJSON(w, http.StatusOK, response)
}

func writeBrowserReplayStartError(w http.ResponseWriter, err error) {
	if errors.Is(err, errBrowserReplaySessionCapacity) || errors.Is(err, errBrowserReplayManagerClosed) || errors.Is(err, errReplaySessionCapacity) || errors.Is(err, errReplaySessionProjectCapacity) || errors.Is(err, errReplaySessionLeaseUnavailable) {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	if errors.Is(err, errBrowserReplaySessionIDConflict) || errors.Is(err, errReplaySessionIDConflict) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeError(w, http.StatusInternalServerError, "failed to start browser replay")
}

func applyBrowserReplayOverrides(script *dsl.Script, req BrowserReplayStartRequest) *dsl.Script {
	if script == nil || req.Browser == nil {
		return script
	}
	clone := *script
	clone.Config = script.Config
	browserCfg := clone.Config.Browser
	if req.Browser.Engine != "" {
		browserCfg.Engine = req.Browser.Engine
	}
	if req.Browser.Headless != nil {
		browserCfg.Headless = req.Browser.Headless
	}
	if req.Browser.SlowMoMs > 0 {
		browserCfg.SlowMoMs = req.Browser.SlowMoMs
	}
	if req.Browser.UserAgent != "" {
		browserCfg.UserAgent = req.Browser.UserAgent
	}
	if req.Browser.DeviceProfile != "" {
		browserCfg.DeviceProfile = req.Browser.DeviceProfile
	}
	if req.Browser.TouchMode != nil {
		browserCfg.TouchMode = req.Browser.TouchMode
	}
	if req.Browser.Orientation != "" {
		browserCfg.Orientation = req.Browser.Orientation
	}
	if req.Browser.ClientMetrics != nil {
		browserCfg.ClientMetrics = req.Browser.ClientMetrics
	}
	if req.Browser.CaptureScreenshots != "" {
		browserCfg.CaptureScreenshots = req.Browser.CaptureScreenshots
	}
	if req.Browser.Viewport.Width > 0 {
		browserCfg.Viewport.Width = req.Browser.Viewport.Width
	}
	if req.Browser.Viewport.Height > 0 {
		browserCfg.Viewport.Height = req.Browser.Viewport.Height
	}
	if req.Browser.ReviewPolicy.AllowReviewedJavaScript || req.Browser.ReviewPolicy.AllowReviewRequired {
		browserCfg.ReviewPolicy = req.Browser.ReviewPolicy
	}
	clone.Config.Browser = browserCfg
	return &clone
}

func (s *Server) handleGetBrowserReplaySession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	authorizedProjectID, _ := authorizedProjectFromRequest(r)
	session, ok := s.browserReplay.GetForProject(sessionID, authorizedProjectID)
	if !ok {
		writeError(w, http.StatusNotFound, "browser replay session not found")
		return
	}
	writeJSON(w, http.StatusOK, BrowserReplaySessionResponse{Session: session})
}

func (s *Server) handleStopBrowserReplaySession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	authorizedProjectID, _ := authorizedProjectFromRequest(r)
	session, ok := s.browserReplay.StopForProject(sessionID, authorizedProjectID)
	if !ok {
		writeError(w, http.StatusNotFound, "browser replay session not found")
		return
	}
	writeJSON(w, http.StatusOK, BrowserReplaySessionResponse{Session: session})
}

func (s *Server) handlePauseBrowserReplaySession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	authorizedProjectID, _ := authorizedProjectFromRequest(r)
	session, ok, changed := s.browserReplay.PauseForProject(sessionID, authorizedProjectID)
	if !ok {
		writeError(w, http.StatusNotFound, "browser replay session not found")
		return
	}
	if !changed {
		writeError(w, http.StatusConflict, "browser replay session is not running")
		return
	}
	s.recordAuditEvent(r, "browser_replay.session.pause", "success", "browser_replay_session", session.ID, session.ProjectID, map[string]any{"script_id": session.ScriptID, "status": session.Status})
	writeJSON(w, http.StatusOK, BrowserReplaySessionResponse{Session: session})
}

func (s *Server) handleResumeBrowserReplaySession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	authorizedProjectID, _ := authorizedProjectFromRequest(r)
	session, ok, changed := s.browserReplay.ResumeForProject(sessionID, authorizedProjectID)
	if !ok {
		writeError(w, http.StatusNotFound, "browser replay session not found")
		return
	}
	if !changed {
		writeError(w, http.StatusConflict, "browser replay session is not paused")
		return
	}
	s.recordAuditEvent(r, "browser_replay.session.resume", "success", "browser_replay_session", session.ID, session.ProjectID, map[string]any{"script_id": session.ScriptID, "status": session.Status})
	writeJSON(w, http.StatusOK, BrowserReplaySessionResponse{Session: session})
}
