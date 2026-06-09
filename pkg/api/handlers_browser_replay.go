package api

import (
	"encoding/json"
	"net/http"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
)

type BrowserReplayStartRequest struct {
	GroupName       string                 `json:"group_name,omitempty"`
	ReplaySessionID string                 `json:"replay_session_id,omitempty"`
	Variables       map[string]string      `json:"variables,omitempty"`
	RedactionRules  []engine.RedactionRule `json:"redaction_rules,omitempty"`
	StartPaused     bool                   `json:"start_paused,omitempty"`
	Breakpoints     []int                  `json:"breakpoints,omitempty"`
	Browser         *dsl.BrowserConfig     `json:"browser,omitempty"`
	IgnoreThinkTime *bool                  `json:"ignore_think_time,omitempty"`
	Script          *dsl.Script            `json:"script,omitempty"`
	Preflight       *ReplayPreflight       `json:"-"`
}

func (s *Server) handleStartBrowserReplayDraftScript(w http.ResponseWriter, r *http.Request) {
	var req BrowserReplayStartRequest
	if r.Body == nil || r.ContentLength == 0 {
		writeError(w, http.StatusBadRequest, "request body is required")
		return
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.Script == nil {
		writeError(w, http.StatusBadRequest, "script is required")
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

	var req BrowserReplayStartRequest
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
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
	}

	s.handleStartBrowserReplayRequest(w, r, id, script, req)
}

func (s *Server) handleStartBrowserReplayRequest(w http.ResponseWriter, r *http.Request, id string, script *dsl.Script, req BrowserReplayStartRequest) {
	script = applyBrowserReplayOverrides(script, req)
	issues := validateScriptForReplay(script)
	warnings := validationWarnings(script)
	preflight := buildReplayPreflight(script, req.Variables, issues, warnings)
	if len(issues) > 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"script_id": id,
			"valid":     false,
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
	session := s.browserReplay.Start(id, script, req)
	s.recordAuditEvent(r, "browser_replay.session.start", "success", "browser_replay_session", session.ID, session.ProjectID, map[string]any{"script_id": session.ScriptID, "script_name": script.Name, "status": session.Status, "start_paused": req.StartPaused})
	writeJSON(w, http.StatusOK, map[string]any{
		"script_id":         id,
		"replay_session_id": session.ID,
		"session":           session,
		"preflight":         preflight,
		"warnings":          warnings,
	})
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
	session, ok := s.browserReplay.Get(sessionID)
	if !ok {
		writeError(w, http.StatusNotFound, "browser replay session not found")
		return
	}
	s.recordAuditEvent(r, "browser_replay.session.stop", "success", "browser_replay_session", session.ID, session.ProjectID, map[string]any{"script_id": session.ScriptID, "status": session.Status})
	writeJSON(w, http.StatusOK, map[string]any{"session": session})
}

func (s *Server) handleStopBrowserReplaySession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	session, ok := s.browserReplay.Stop(sessionID)
	if !ok {
		writeError(w, http.StatusNotFound, "browser replay session not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": session})
}

func (s *Server) handlePauseBrowserReplaySession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	session, ok, changed := s.browserReplay.Pause(sessionID)
	if !ok {
		writeError(w, http.StatusNotFound, "browser replay session not found")
		return
	}
	if !changed {
		writeError(w, http.StatusConflict, "browser replay session is not running")
		return
	}
	s.recordAuditEvent(r, "browser_replay.session.pause", "success", "browser_replay_session", session.ID, session.ProjectID, map[string]any{"script_id": session.ScriptID, "status": session.Status})
	writeJSON(w, http.StatusOK, map[string]any{"session": session})
}

func (s *Server) handleResumeBrowserReplaySession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	session, ok, changed := s.browserReplay.Resume(sessionID)
	if !ok {
		writeError(w, http.StatusNotFound, "browser replay session not found")
		return
	}
	if !changed {
		writeError(w, http.StatusConflict, "browser replay session is not paused")
		return
	}
	s.recordAuditEvent(r, "browser_replay.session.resume", "success", "browser_replay_session", session.ID, session.ProjectID, map[string]any{"script_id": session.ScriptID, "status": session.Status})
	writeJSON(w, http.StatusOK, map[string]any{"session": session})
}
