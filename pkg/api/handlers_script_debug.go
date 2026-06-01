package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
)

type ScriptDebugStartRequest struct {
	GroupName       string                       `json:"group_name,omitempty"`
	ReplaySessionID string                       `json:"replay_session_id,omitempty"`
	ReplayProfile   string                       `json:"replay_profile,omitempty"`
	RuntimeProfile  *engine.ReplayRuntimeProfile `json:"runtime_profile,omitempty"`
	Variables       map[string]string            `json:"variables,omitempty"`
	RedactionRules  []engine.RedactionRule       `json:"redaction_rules,omitempty"`
	StartPaused     bool                         `json:"start_paused,omitempty"`
	Breakpoints     []ScriptDebugBreakpoint      `json:"breakpoints,omitempty"`
	LogLevel        string                       `json:"log_level,omitempty"`
	Browser         *dsl.BrowserConfig           `json:"browser,omitempty"`
	IgnoreThinkTime *bool                        `json:"ignore_think_time,omitempty"`
	Script          *dsl.Script                  `json:"script,omitempty"`
	Mode            string                       `json:"-"`
	Preflight       *ReplayPreflight             `json:"-"`
}

func (s *Server) handleStartScriptDebugDraftScript(w http.ResponseWriter, r *http.Request) {
	var req ScriptDebugStartRequest
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
	s.handleStartScriptDebugRequest(w, r, "", req.Script, req)
}

func (s *Server) handleStartScriptDebugScript(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	stored, ok := s.runner.GetScript(id)
	if !ok {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}

	var req ScriptDebugStartRequest
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

	s.handleStartScriptDebugRequest(w, r, id, script, req)
}

func (s *Server) handleStartReplaySessionDraftScript(w http.ResponseWriter, r *http.Request) {
	var req ScriptDebugStartRequest
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
	req.Mode = "replay"
	s.handleStartScriptDebugRequest(w, r, "", req.Script, req)
}

func (s *Server) handleStartReplaySessionScript(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	stored, ok := s.runner.GetScript(id)
	if !ok {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}

	var req ScriptDebugStartRequest
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
	req.Mode = "replay"
	s.handleStartScriptDebugRequest(w, r, id, script, req)
}

func (s *Server) handleStartScriptDebugRequest(w http.ResponseWriter, r *http.Request, id string, script *dsl.Script, req ScriptDebugStartRequest) {
	script = applyScriptDebugOverrides(script, req)
	issues := validateScript(script)
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

	req.Preflight = preflight
	session := s.scriptDebug.Start(id, script, req)
	s.recordAuditEvent(r, "script_debug.session.start", "success", "script_debug_session", session.ID, session.ProjectID, map[string]any{"script_id": session.ScriptID, "script_name": script.Name, "status": session.Status, "start_paused": req.StartPaused, "log_level": session.LogLevel})
	writeJSON(w, http.StatusOK, map[string]any{
		"script_id":         id,
		"replay_session_id": session.ID,
		"debug_session_id":  session.ID,
		"session":           session,
		"preflight":         preflight,
		"warnings":          warnings,
	})
}

func applyScriptDebugOverrides(script *dsl.Script, req ScriptDebugStartRequest) *dsl.Script {
	if script == nil {
		return script
	}
	return applyBrowserReplayOverrides(script, BrowserReplayStartRequest{Browser: req.Browser})
}

func (s *Server) handleGetScriptDebugSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	session, ok := s.scriptDebug.Get(sessionID)
	if !ok {
		writeError(w, http.StatusNotFound, "script debug session not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": session})
}

func (s *Server) handlePauseScriptDebugSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	session, ok, changed := s.scriptDebug.Pause(sessionID)
	if !ok {
		writeError(w, http.StatusNotFound, "script debug session not found")
		return
	}
	if !changed {
		writeError(w, http.StatusConflict, "script debug session is not running")
		return
	}
	s.recordAuditEvent(r, "script_debug.session.pause", "success", "script_debug_session", session.ID, session.ProjectID, map[string]any{"script_id": session.ScriptID, "status": session.Status})
	writeJSON(w, http.StatusOK, map[string]any{"session": session})
}

func (s *Server) handleResumeScriptDebugSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	session, ok, changed := s.scriptDebug.Resume(sessionID)
	if !ok {
		writeError(w, http.StatusNotFound, "script debug session not found")
		return
	}
	if !changed {
		writeError(w, http.StatusConflict, "script debug session is not paused")
		return
	}
	s.recordAuditEvent(r, "script_debug.session.resume", "success", "script_debug_session", session.ID, session.ProjectID, map[string]any{"script_id": session.ScriptID, "status": session.Status})
	writeJSON(w, http.StatusOK, map[string]any{"session": session})
}

func (s *Server) handleStepScriptDebugSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	session, ok, changed := s.scriptDebug.Step(sessionID)
	if !ok {
		writeError(w, http.StatusNotFound, "script debug session not found")
		return
	}
	if !changed {
		writeError(w, http.StatusConflict, "script debug session is not paused")
		return
	}
	s.recordAuditEvent(r, "script_debug.session.step", "success", "script_debug_session", session.ID, session.ProjectID, map[string]any{"script_id": session.ScriptID, "status": session.Status})
	writeJSON(w, http.StatusOK, map[string]any{"session": session})
}

func (s *Server) handleStopScriptDebugSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	session, ok := s.scriptDebug.Stop(sessionID)
	if !ok {
		writeError(w, http.StatusNotFound, "script debug session not found")
		return
	}
	s.recordAuditEvent(r, "script_debug.session.stop", "success", "script_debug_session", session.ID, session.ProjectID, map[string]any{"script_id": session.ScriptID, "status": session.Status})
	writeJSON(w, http.StatusOK, map[string]any{"session": session})
}

func (s *Server) handleUpdateScriptDebugBreakpoints(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	var req struct {
		Breakpoints []ScriptDebugBreakpoint `json:"breakpoints"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	session, ok := s.scriptDebug.UpdateBreakpoints(sessionID, req.Breakpoints)
	if !ok {
		writeError(w, http.StatusNotFound, "script debug session not found")
		return
	}
	s.recordAuditEvent(r, "script_debug.breakpoints.update", "success", "script_debug_session", session.ID, session.ProjectID, map[string]any{"script_id": session.ScriptID, "breakpoints": len(session.Breakpoints)})
	writeJSON(w, http.StatusOK, map[string]any{"session": session})
}

func (s *Server) handleExportScriptDebugSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	format := strings.TrimSpace(r.URL.Query().Get("format"))
	payload, contentType, ok := s.scriptDebug.Export(sessionID, format)
	if !ok {
		writeError(w, http.StatusNotFound, "script debug session not found")
		return
	}
	extension := "json"
	if strings.HasPrefix(contentType, "text/plain") {
		extension = "log"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="script-debug-%s.%s"`, sessionID, extension))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
}
