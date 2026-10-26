package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
)

type ScriptDebugStartRequest struct {
	LiveTableSeeds    []engine.LiveTableSandboxSeed `json:"live_table_seeds,omitempty"`
	GroupName         string                        `json:"group_name,omitempty"`
	ProjectID         string                        `json:"project_id,omitempty"`
	ReplaySessionID   string                        `json:"replay_session_id,omitempty"`
	ReplayProfile     string                        `json:"replay_profile,omitempty"`
	RuntimeProfile    *engine.ReplayRuntimeProfile  `json:"runtime_profile,omitempty"`
	Variables         map[string]string             `json:"variables,omitempty"`
	RedactionRules    []engine.RedactionRule        `json:"redaction_rules,omitempty"`
	StartPaused       bool                          `json:"start_paused,omitempty"`
	Breakpoints       []ScriptDebugBreakpoint       `json:"breakpoints,omitempty"`
	BreakOnFailure    bool                          `json:"break_on_failure,omitempty"`
	RunID             string                        `json:"run_id,omitempty"`
	ContinueOnError   *bool                         `json:"continue_on_error,omitempty"`
	LogLevel          string                        `json:"log_level,omitempty"`
	Browser           *dsl.BrowserConfig            `json:"browser,omitempty"`
	IgnoreThinkTime   *bool                         `json:"ignore_think_time,omitempty"`
	Script            *dsl.Script                   `json:"script,omitempty"`
	Mode              string                        `json:"-"`
	Preflight         *ReplayPreflight              `json:"-"`
	SecretResolver    engine.SecretResolver         `json:"-"`
	ActionGraphDigest string                        `json:"-"`
	// trustedPreservedImport is set only after loading the unchanged durable
	// import snapshot. A request body cannot establish this trust.
	trustedPreservedImport  bool
	executionContext        context.Context
	releaseExecutionContext context.CancelFunc
}

type ScriptDebugStepRequest struct {
	Mode string `json:"mode,omitempty"`
}

type ScriptDebugBreakpointsRequest struct {
	Breakpoints []ScriptDebugBreakpoint `json:"breakpoints"`
}

type ScriptDebugOptionsRequest struct {
	BreakOnFailure *bool `json:"break_on_failure"`
}

type ScriptDebugStartResponse struct {
	ScriptID        string              `json:"script_id"`
	ReplaySessionID string              `json:"replay_session_id"`
	DebugSessionID  string              `json:"debug_session_id"`
	Session         *ScriptDebugSession `json:"session"`
	Preflight       *ReplayPreflight    `json:"preflight,omitempty"`
	Warnings        []string            `json:"warnings,omitempty"`
}

type ScriptDebugSessionResponse struct {
	Session *ScriptDebugSession `json:"session"`
}

func (s *Server) handleStartScriptDebugDraftScript(w http.ResponseWriter, r *http.Request) {
	var req ScriptDebugStartRequest
	if r.Body == nil || r.ContentLength == 0 {
		writeError(w, http.StatusBadRequest, "request body is required")
		return
	}
	if !decodeOptionalJSONBodyAllowingUnknownFields(w, r, &req) {
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
	s.handleStartScriptDebugRequest(w, r, "", req.Script, req)
}

func (s *Server) handleStartScriptDebugScript(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	stored, ok := s.runner.GetScript(id)
	if !ok {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	if !requireAuthorizedProjectMatch(w, r, stored.ProjectID) {
		return
	}
	if err := s.runner.checkArchivedExecution("", []string{stored.ID}); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err := storedScriptReplayGateError(stored.Script); err != nil {
		s.writeStoredScriptReplayGate(w, stored.Script, err)
		return
	}

	var req ScriptDebugStartRequest
	if !decodeOptionalJSONBodyAllowingUnknownFields(w, r, &req) {
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
		req.trustedPreservedImport = isCredentialPreservingImport(stored.Script)
	}
	if err := bindStoredScriptProject(script, stored.Script.ProjectID, req.ProjectID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	s.handleStartScriptDebugRequest(w, r, stored.ID, script, req)
}

func (s *Server) handleStartReplaySessionDraftScript(w http.ResponseWriter, r *http.Request) {
	var req ScriptDebugStartRequest
	if r.Body == nil || r.ContentLength == 0 {
		writeError(w, http.StatusBadRequest, "request body is required")
		return
	}
	if !decodeOptionalJSONBodyAllowingUnknownFields(w, r, &req) {
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
	if !requireAuthorizedProjectMatch(w, r, stored.ProjectID) {
		return
	}
	if err := s.runner.checkArchivedExecution("", []string{stored.ID}); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err := storedScriptReplayGateError(stored.Script); err != nil {
		s.writeStoredScriptReplayGate(w, stored.Script, err)
		return
	}

	var req ScriptDebugStartRequest
	if !decodeOptionalJSONBodyAllowingUnknownFields(w, r, &req) {
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
		req.trustedPreservedImport = isCredentialPreservingImport(stored.Script)
	}
	if err := bindStoredScriptProject(script, stored.Script.ProjectID, req.ProjectID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Mode = "replay"
	s.handleStartScriptDebugRequest(w, r, stored.ID, script, req)
}

// resolveTrustedStoredReplayEcho recognizes an unchanged disclosure-safe GET
// projection of a credential-preserving import. The client never receives the
// retained credential bytes, so all saved-script replay/debug entry points must
// substitute the server-owned durable snapshot before executable preparation.
//
// An edited projection is handled by resolveStoredReplayImportScript, which
// reconciles the edit against the same durable prior instead of executing the
// disclosure sentinels.
func resolveTrustedStoredReplayEcho(candidate, stored *dsl.Script) (*dsl.Script, bool) {
	if candidate != nil && isCredentialPreservingImport(stored) && matchesStoredScriptDisclosureProjection(candidate, stored) {
		return stored, true
	}
	return candidate, false
}

func (s *Server) handleStartScriptDebugRequest(w http.ResponseWriter, r *http.Request, id string, script *dsl.Script, req ScriptDebugStartRequest) {
	if !s.requireExecutionLeader(w) {
		return
	}
	if err := validateExternalReplaySessionID(req.ReplaySessionID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateDebugRunIdentity(req.RunID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	script = applyScriptDebugOverrides(script, req)
	submitted := script
	var prepared bool
	script, prepared = prepareTransientExecutableScript(
		w,
		script,
		req.trustedPreservedImport,
	)
	if !prepared {
		return
	}
	actionGraphDigest, digestErr := scriptCorrelationActionGraphDigest(script)
	if digestErr != nil {
		writeError(w, http.StatusInternalServerError, "failed to identify replay action graph")
		return
	}
	req.ActionGraphDigest = actionGraphDigest
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
		s.transientStaticIssues(submitted, script, req.trustedPreservedImport),
		refIssues...,
	)
	warnings := append(validationWarnings(script), refWarnings...)
	warnings = append(warnings, scriptDebugParityWarnings(script)...)
	if !s.prepareReplayLiveTables(r.Context(), w, script, req.LiveTableSeeds) {
		return
	}
	secretResolver := s.runner.secretResolverForScript(script)
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

	if id != "" {
		if err := s.runner.checkArchivedExecution("", []string{id}); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
	}
	req.Preflight = preflight
	req.SecretResolver = secretResolver
	req.executionContext, req.releaseExecutionContext = s.runner.executionOperationContext(context.Background())
	session, err := s.scriptDebug.StartChecked(id, script, req)
	if err != nil {
		req.releaseExecutionContext()
		if errors.Is(err, errScriptDebugSessionCapacity) || errors.Is(err, errScriptDebugManagerClosed) || errors.Is(err, errReplaySessionCapacity) || errors.Is(err, errReplaySessionProjectCapacity) || errors.Is(err, errReplaySessionLeaseUnavailable) {
			writeError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		if writeScriptReplayBlocked(w, err) {
			return
		}
		if errors.Is(err, errScriptDebugSessionIDConflict) || errors.Is(err, errReplaySessionIDConflict) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		// The cause is logged with the response's ref, never sent.
		writeError(w, http.StatusInternalServerError, "failed to start script debug session: "+err.Error())
		return
	}
	s.recordAuditEvent(r, "script_debug.session.start", "success", "script_debug_session", session.ID, session.ProjectID, map[string]any{"script_id": session.ScriptID, "script_name": script.Name, "status": session.Status, "start_paused": req.StartPaused, "log_level": session.LogLevel})
	writeJSON(w, http.StatusOK, ScriptDebugStartResponse{
		ScriptID:        id,
		ReplaySessionID: session.ID,
		DebugSessionID:  session.ID,
		Session:         session,
		Preflight:       preflight,
		Warnings:        warnings,
	})
}

// scriptDebugParityWarnings makes the single-VUser deterministic replay boundary
// visible before its result is used to judge a multi-VUser scenario.
func scriptDebugParityWarnings(script *dsl.Script) []string {
	if script == nil {
		return nil
	}
	for _, parameter := range script.Parameters {
		if parameter.Type != "file" {
			continue
		}
		if parameter.Selection == "random" || parameter.BlockSize > 0 || parameter.ValueForEachVUser {
			return []string{"Debugger replay validates one deterministic VUser path; randomized or VUser-partitioned parameter data still requires a scenario shakeout before load execution."}
		}
	}
	return nil
}

func applyScriptDebugOverrides(script *dsl.Script, req ScriptDebugStartRequest) *dsl.Script {
	if script == nil {
		return script
	}
	return applyBrowserReplayOverrides(script, BrowserReplayStartRequest{Browser: req.Browser})
}

func (s *Server) handleGetScriptDebugSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	authorizedProjectID, _ := authorizedProjectFromRequest(r)
	session, ok := s.scriptDebug.GetForProject(sessionID, authorizedProjectID)
	if !ok {
		writeError(w, http.StatusNotFound, "script debug session not found")
		return
	}
	writeJSON(w, http.StatusOK, ScriptDebugSessionResponse{Session: session})
}

func (s *Server) handlePauseScriptDebugSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	authorizedProjectID, _ := authorizedProjectFromRequest(r)
	session, ok, changed := s.scriptDebug.PauseForProject(sessionID, authorizedProjectID)
	if !ok {
		writeError(w, http.StatusNotFound, "script debug session not found")
		return
	}
	if !changed {
		writeError(w, http.StatusConflict, "script debug session is not running")
		return
	}
	s.recordAuditEvent(r, "script_debug.session.pause", "success", "script_debug_session", session.ID, session.ProjectID, map[string]any{"script_id": session.ScriptID, "status": session.Status})
	writeJSON(w, http.StatusOK, ScriptDebugSessionResponse{Session: session})
}

func (s *Server) handleResumeScriptDebugSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	authorizedProjectID, _ := authorizedProjectFromRequest(r)
	session, ok, changed := s.scriptDebug.ResumeForProject(sessionID, authorizedProjectID)
	if !ok {
		writeError(w, http.StatusNotFound, "script debug session not found")
		return
	}
	if !changed {
		writeError(w, http.StatusConflict, "script debug session is not paused")
		return
	}
	s.recordAuditEvent(r, "script_debug.session.resume", "success", "script_debug_session", session.ID, session.ProjectID, map[string]any{"script_id": session.ScriptID, "status": session.Status})
	writeJSON(w, http.StatusOK, ScriptDebugSessionResponse{Session: session})
}

func (s *Server) handleStepScriptDebugSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	// The step body is optional: legacy clients send none (step-into). A
	// {"mode":"into"|"over"|"loop_end"} body selects the step mode.
	mode := ""
	if r.Body != nil {
		var req ScriptDebugStepRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
		mode = req.Mode
	}
	authorizedProjectID, _ := authorizedProjectFromRequest(r)
	session, ok, changed := s.scriptDebug.StepModeForProject(sessionID, authorizedProjectID, mode)
	if !ok {
		writeError(w, http.StatusNotFound, "script debug session not found")
		return
	}
	if !changed {
		writeError(w, http.StatusConflict, "script debug session is not paused")
		return
	}
	s.recordAuditEvent(r, "script_debug.session.step", "success", "script_debug_session", session.ID, session.ProjectID, map[string]any{"script_id": session.ScriptID, "status": session.Status, "mode": mode})
	writeJSON(w, http.StatusOK, ScriptDebugSessionResponse{Session: session})
}

func (s *Server) handleStopScriptDebugSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	authorizedProjectID, _ := authorizedProjectFromRequest(r)
	session, ok := s.scriptDebug.StopForProject(sessionID, authorizedProjectID)
	if !ok {
		writeError(w, http.StatusNotFound, "script debug session not found")
		return
	}
	s.recordAuditEvent(r, "script_debug.session.stop", "success", "script_debug_session", session.ID, session.ProjectID, map[string]any{"script_id": session.ScriptID, "status": session.Status})
	writeJSON(w, http.StatusOK, ScriptDebugSessionResponse{Session: session})
}

func (s *Server) handleUpdateScriptDebugBreakpoints(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	var req ScriptDebugBreakpointsRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	authorizedProjectID, _ := authorizedProjectFromRequest(r)
	session, ok := s.scriptDebug.UpdateBreakpointsForProject(sessionID, authorizedProjectID, req.Breakpoints)
	if !ok {
		writeError(w, http.StatusNotFound, "script debug session not found")
		return
	}
	s.recordAuditEvent(r, "script_debug.breakpoints.update", "success", "script_debug_session", session.ID, session.ProjectID, map[string]any{"script_id": session.ScriptID, "breakpoints": len(session.Breakpoints)})
	writeJSON(w, http.StatusOK, ScriptDebugSessionResponse{Session: session})
}

func (s *Server) handleUpdateScriptDebugOptions(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	var req ScriptDebugOptionsRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.BreakOnFailure == nil {
		writeError(w, http.StatusBadRequest, "break_on_failure is required")
		return
	}
	authorizedProjectID, _ := authorizedProjectFromRequest(r)
	session, ok := s.scriptDebug.SetBreakOnFailureForProject(sessionID, authorizedProjectID, *req.BreakOnFailure)
	if !ok {
		writeError(w, http.StatusNotFound, "script debug session not found")
		return
	}
	s.recordAuditEvent(r, "script_debug.options.update", "success", "script_debug_session", session.ID, session.ProjectID, map[string]any{"script_id": session.ScriptID, "break_on_failure": session.BreakOnFailure})
	writeJSON(w, http.StatusOK, ScriptDebugSessionResponse{Session: session})
}

func (s *Server) handleExportScriptDebugSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	format := strings.TrimSpace(r.URL.Query().Get("format"))
	authorizedProjectID, _ := authorizedProjectFromRequest(r)
	if strings.EqualFold(format, "har") {
		payload, availability, ok := s.scriptDebug.ExportHARForProject(sessionID, authorizedProjectID)
		if !ok {
			writeError(w, http.StatusNotFound, "script debug session not found")
			return
		}
		if !availability.Available {
			writeError(w, http.StatusConflict, availability.Reason)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="script-debug-%s.har"`, sessionID))
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
		return
	}
	payload, contentType, ok := s.scriptDebug.ExportForProject(sessionID, authorizedProjectID, format)
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
