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

// ScriptDebugStartRequest starts a debug or replay session. Iterations is the
// number of action-phase iterations to run, 1 to maxScriptDebugIterations;
// 0 or omitted runs one.
type ScriptDebugStartRequest struct {
	LiveTableSeeds   []engine.LiveTableSandboxSeed `json:"live_table_seeds,omitempty"`
	GroupName        string                        `json:"group_name,omitempty"`
	ProjectID        string                        `json:"project_id,omitempty"`
	ReplaySessionID  string                        `json:"replay_session_id,omitempty"`
	ReplayProfile    string                        `json:"replay_profile,omitempty"`
	RuntimeProfile   *engine.ReplayRuntimeProfile  `json:"runtime_profile,omitempty"`
	Variables        map[string]string             `json:"variables,omitempty"`
	RedactionRules   []engine.RedactionRule        `json:"redaction_rules,omitempty"`
	StartPaused      bool                          `json:"start_paused,omitempty"`
	Iterations       int                           `json:"iterations,omitempty"`
	Breakpoints      []ScriptDebugBreakpoint       `json:"breakpoints,omitempty"`
	BreakOnFailure   bool                          `json:"break_on_failure,omitempty"`
	RunID            string                        `json:"run_id,omitempty"`
	ContinueOnError  *bool                         `json:"continue_on_error,omitempty"`
	LogLevel         string                        `json:"log_level,omitempty"`
	Browser          *dsl.BrowserConfig            `json:"browser,omitempty"`
	IgnoreThinkTime  *bool                         `json:"ignore_think_time,omitempty"`
	RuntimeOverrides *RuntimeSettingsOverride      `json:"runtime_overrides,omitempty"`
	Script           *dsl.Script                   `json:"script,omitempty"`
	// ExpectedContentRevision, on a stored-script start without a script body,
	// is the stored revision the client shows. A different stored revision
	// answers 409 rather than running content the client never saw; the run
	// then uses the durable script at exactly that revision.
	ExpectedContentRevision string                `json:"expected_content_revision,omitempty"`
	Mode                    string                `json:"-"`
	Preflight               *ReplayPreflight      `json:"-"`
	SecretResolver          engine.SecretResolver `json:"-"`
	ActionGraphDigest       string                `json:"-"`
	// trustedPreservedImport is set only after loading the unchanged durable
	// import snapshot. A request body cannot establish this trust.
	trustedPreservedImport  bool
	storedBaseScript        *dsl.Script
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

// maxScriptDebugIterations bounds the action-phase iterations one debug or
// replay session may run.
const maxScriptDebugIterations = 100

var scriptDebugIterationsError = fmt.Sprintf("iterations must be between 1 and %d (0 or omitted runs one iteration)", maxScriptDebugIterations)

type ScriptDebugSessionResponse struct {
	Session *ScriptDebugSession `json:"session"`
}

func (s *Server) handleStartScriptDebugDraftScript(w http.ResponseWriter, r *http.Request) {
	var req ScriptDebugStartRequest
	if !requireNonEmptyReplayBody(w, r) {
		return
	}
	if !decodeOptionalJSONBodyAllowingUnknownFields(w, r, &req) {
		return
	}
	if !bindDraftReplayRequestScript(w, req.Script, req.ProjectID) {
		return
	}
	s.handleStartScriptDebugRequest(w, r, "", req.Script, req)
}

func (s *Server) handleStartScriptDebugScript(w http.ResponseWriter, r *http.Request) {
	stored, ok := s.loadAuthorizedReplayStoredScript(w, r)
	if !ok {
		return
	}

	var req ScriptDebugStartRequest
	if !decodeOptionalJSONBodyAllowingUnknownFields(w, r, &req) {
		return
	}
	if stored, ok = s.pinStoredDebugRevision(w, stored, req); !ok {
		return
	}

	script, trustedPreservedImport, ok := s.bindStoredReplayRequestScript(w, stored, req.Script, req.ProjectID)
	if !ok {
		return
	}
	req.trustedPreservedImport = trustedPreservedImport
	req.storedBaseScript = stored.Script

	s.handleStartScriptDebugRequest(w, r, stored.ID, script, req)
}

func (s *Server) handleStartReplaySessionDraftScript(w http.ResponseWriter, r *http.Request) {
	var req ScriptDebugStartRequest
	if !requireNonEmptyReplayBody(w, r) {
		return
	}
	if !decodeOptionalJSONBodyAllowingUnknownFields(w, r, &req) {
		return
	}
	if !bindDraftReplayRequestScript(w, req.Script, req.ProjectID) {
		return
	}
	req.Mode = "replay"
	s.handleStartScriptDebugRequest(w, r, "", req.Script, req)
}

func (s *Server) handleStartReplaySessionScript(w http.ResponseWriter, r *http.Request) {
	stored, ok := s.loadAuthorizedReplayStoredScript(w, r)
	if !ok {
		return
	}

	var req ScriptDebugStartRequest
	if !decodeOptionalJSONBodyAllowingUnknownFields(w, r, &req) {
		return
	}
	if stored, ok = s.pinStoredDebugRevision(w, stored, req); !ok {
		return
	}

	script, trustedPreservedImport, ok := s.bindStoredReplayRequestScript(w, stored, req.Script, req.ProjectID)
	if !ok {
		return
	}
	req.trustedPreservedImport = trustedPreservedImport
	req.storedBaseScript = stored.Script
	req.Mode = "replay"
	s.handleStartScriptDebugRequest(w, r, stored.ID, script, req)
}

// scriptDebugRevisionChangedCode marks a stored-script start refused because
// the stored script is no longer the revision the client expected.
const scriptDebugRevisionChangedCode = "debug_script_revision_changed"

// pinStoredDebugRevision binds a body-less stored-script start to the revision
// the client expected. Without an expected revision, or with a script body (the
// body is what runs), the stored script is returned unchanged. Otherwise the
// durable snapshot is read: a different revision answers 409, and a match
// returns the durable script so the run is exactly the checked revision, not a
// possibly stale cached copy.
func (s *Server) pinStoredDebugRevision(w http.ResponseWriter, stored *StoredScript, req ScriptDebugStartRequest) (*StoredScript, bool) {
	expected := strings.TrimSpace(req.ExpectedContentRevision)
	if expected == "" || req.Script != nil {
		return stored, true
	}
	durable, found, err := s.runner.GetScriptAuthoringDurable(stored.ID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "the stored script revision could not be confirmed")
		return nil, false
	}
	if !found || durable == nil || durable.Script == nil || normalizeProjectID(durable.ProjectID) != normalizeProjectID(stored.ProjectID) {
		writeError(w, http.StatusNotFound, "script not found")
		return nil, false
	}
	if durable.ContentRevision != expected {
		writeJSON(w, http.StatusConflict, AuthoringIdempotencyError{Error: "the stored script changed since it was opened; reload it before debugging", Code: scriptDebugRevisionChangedCode})
		return nil, false
	}
	if err := storedScriptReplayGateError(durable.Script); err != nil {
		s.writeStoredScriptReplayGate(w, durable.Script, err)
		return nil, false
	}
	pinned := *stored
	pinned.Script = durable.Script
	pinned.ContentRevision = durable.ContentRevision
	return &pinned, true
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
	if req.Iterations < 0 || req.Iterations > maxScriptDebugIterations {
		writeError(w, http.StatusBadRequest, scriptDebugIterationsError)
		return
	}
	if req.RuntimeProfile != nil && req.RuntimeProfile.HAROptions != nil {
		normalized, err := req.RuntimeProfile.HAROptions.Normalize()
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		profile := *req.RuntimeProfile
		profile.HAROptions = &normalized
		req.RuntimeProfile = &profile
	}
	storedBase := req.storedBaseScript
	if storedBase == nil && id != "" {
		if stored, ok := s.runner.GetScript(id); ok && stored != nil {
			storedBase = stored.Script
		}
	}
	var runtimeSecretValues map[string]string
	if req.RuntimeOverrides != nil {
		var ok bool
		script, runtimeSecretValues, req.RedactionRules, ok = s.applyReplayOrDebugRuntimeOverrides(
			w,
			id,
			storedBase,
			script,
			req.RuntimeOverrides,
			req.RedactionRules,
		)
		if !ok {
			return
		}
	}
	script = applyScriptDebugOverrides(script, &req)
	if req.Iterations < 0 || req.Iterations > maxScriptDebugIterations {
		writeError(w, http.StatusBadRequest, scriptDebugIterationsError)
		return
	}
	preparedExec, ok := s.prepareReplayOrDebugExecution(r.Context(), w, replayExecutionPrepOptions{
		ScriptID:               id,
		Script:                 script,
		ReplaySessionID:        req.ReplaySessionID,
		RunID:                  req.RunID,
		ValidateRunID:          true,
		TrustedPreservedImport: req.trustedPreservedImport,
		LiveTableSeeds:         req.LiveTableSeeds,
		Variables:              req.Variables,
		RuntimeSecretValues:    runtimeSecretValues,
		ComputeActionDigest:    true,
		ExtraWarnings:          scriptDebugParityWarnings,
		CheckArchivedScript:    true,
	})
	if !ok {
		return
	}
	script = preparedExec.Script
	req.ActionGraphDigest = preparedExec.ActionGraphDigest
	req.Preflight = preparedExec.Preflight
	req.SecretResolver = preparedExec.SecretResolver
	warnings := preparedExec.Warnings
	preflight := preparedExec.Preflight
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

func applyScriptDebugOverrides(script *dsl.Script, req *ScriptDebugStartRequest) *dsl.Script {
	if script == nil || req == nil {
		return script
	}
	evidence, hasValidJavaApproval := readJavaVuserAuthoringApproval(script)
	if hasValidJavaApproval && !libraryScriptHashMatches(script, evidence.ContentHash) {
		hasValidJavaApproval = false
	}
	origScript := script
	script = applyBrowserReplayOverrides(script, BrowserReplayStartRequest{Browser: req.Browser})
	needsClone := req.ContinueOnError != nil || strings.TrimSpace(req.LogLevel) != "" || req.Iterations > 0
	if needsClone {
		cloned := cloneScriptDefinition(script)
		if cloned != nil {
			script = cloned
		}
	}
	if req.ContinueOnError != nil {
		script.Config.ErrorHandling.ContinueOnError = dsl.ContinueOnErrorValue(*req.ContinueOnError)
	}
	if strings.TrimSpace(req.LogLevel) != "" {
		script.Config.Log.Level = normalizeDebugLogLevel(req.LogLevel)
	} else if runtimeOverridesSpecifiesLogLevel(req.RuntimeOverrides) && strings.TrimSpace(script.Config.Log.Level) != "" {
		req.LogLevel = normalizeDebugLogLevel(script.Config.Log.Level)
	}
	if req.Iterations > 0 {
		script.Config.RunLogic.Iterations = req.Iterations
	} else if runtimeOverridesSpecifiesIterations(req.RuntimeOverrides) && script.Config.RunLogic.Iterations > 0 {
		req.Iterations = script.Config.RunLogic.Iterations
	}
	if req.IgnoreThinkTime == nil && runtimeOverridesSpecifiesThinkTime(req.RuntimeOverrides) {
		ignore := strings.EqualFold(strings.TrimSpace(script.Config.ThinkTime.Type), "none")
		req.IgnoreThinkTime = &ignore
	}
	if hasValidJavaApproval && script != origScript {
		stampJavaVuserAuthoringApproval(script, evidence.SessionID)
	}
	return script
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
