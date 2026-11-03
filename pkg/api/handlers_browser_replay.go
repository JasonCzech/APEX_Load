package api

import "github.com/jasonczech/apexload/pkg/recordbridge"

import (
	"context"
	"errors"
	"net/http"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type BrowserReplayStartRequest struct {
	browserAuthoredDefinition protocol.BrowserAuthoredDefinition
	LiveTableSeeds            []engine.LiveTableSandboxSeed `json:"live_table_seeds,omitempty"`
	GroupName                 string                        `json:"group_name,omitempty"`
	ProjectID                 string                        `json:"project_id,omitempty"`
	ReplaySessionID           string                        `json:"replay_session_id,omitempty"`
	Variables                 map[string]string             `json:"variables,omitempty"`
	RedactionRules            []engine.RedactionRule        `json:"redaction_rules,omitempty"`
	StartPaused               bool                          `json:"start_paused,omitempty"`
	Breakpoints               []int                         `json:"breakpoints,omitempty"`
	Browser                   *dsl.BrowserConfig            `json:"browser,omitempty"`
	IgnoreThinkTime           *bool                         `json:"ignore_think_time,omitempty"`
	RunID                     string                        `json:"run_id,omitempty"`
	ContinueOnError           *bool                         `json:"continue_on_error,omitempty"`
	// RecordHAR records a native HAR of the replay. It is opt-in: a
	// recording runs under the HAR size, memory and owner limits, which close
	// the browser context, so an unrequested one could fail a replay that
	// would otherwise pass. Without it the replay serves the native HAR its
	// script's own capture policy recorded, if any, or else the synthetic HAR
	// built from the report's network entries. Where native HAR capture is
	// not supported (browser.NativeHARSupported) it is refused with 422.
	RecordHAR      *bool                 `json:"record_har,omitempty"`
	Script         *dsl.Script           `json:"script,omitempty"`
	Preflight      *ReplayPreflight      `json:"-"`
	SecretResolver engine.SecretResolver `json:"-"`
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
	if !requireNonEmptyReplayBody(w, r) {
		return
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if !bindDraftReplayRequestScript(w, req.Script, req.ProjectID) {
		return
	}
	s.handleStartBrowserReplayRequest(w, r, "", req.Script, req)
}

func (s *Server) handleStartBrowserReplayScript(w http.ResponseWriter, r *http.Request) {
	stored, ok := s.loadAuthorizedReplayStoredScript(w, r)
	if !ok {
		return
	}

	var req BrowserReplayStartRequest
	if !decodeOptionalJSONBody(w, r, &req) {
		return
	}

	script, trustedPreservedImport, ok := s.bindStoredReplayRequestScript(w, stored, req.Script, req.ProjectID)
	if !ok {
		return
	}
	req.trustedPreservedImport = trustedPreservedImport

	s.handleStartBrowserReplayRequest(w, r, stored.ID, script, req)
}

// OpenAPI-Responses: 409, 422, 500, 503
func (s *Server) handleStartBrowserReplayRequest(w http.ResponseWriter, r *http.Request, id string, script *dsl.Script, req BrowserReplayStartRequest) {
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
	if req.RecordHAR != nil && *req.RecordHAR && !browserNativeHARSupported() {
		writeError(w, http.StatusUnprocessableEntity, errReplayHARPlatformUnsupported.Error())
		return
	}
	req.browserAuthoredDefinition = engine.BrowserOriginalAuthoredDefinition(script)
	script = applyBrowserReplayOverrides(script, req)
	preparedExec, ok := s.prepareReplayOrDebugExecution(r.Context(), w, replayExecutionPrepOptions{
		ScriptID:               id,
		Script:                 script,
		ReplaySessionID:        req.ReplaySessionID,
		RunID:                  req.RunID,
		ValidateRunID:          true,
		TrustedPreservedImport: req.trustedPreservedImport,
		LiveTableSeeds:         req.LiveTableSeeds,
		Variables:              req.Variables,
		RequireBrowserProtocol: true,
		CheckArchivedScript:    true,
	})
	if !ok {
		return
	}
	script = preparedExec.Script
	req.Preflight = preparedExec.Preflight
	req.SecretResolver = preparedExec.SecretResolver
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
		Preflight:       preparedExec.Preflight,
		Warnings:        preparedExec.Warnings,
	}
	writeJSON(w, http.StatusOK, response)
}

func writeBrowserReplayStartError(w http.ResponseWriter, err error) {
	if errors.Is(err, errBrowserReplaySessionCapacity) || errors.Is(err, errBrowserReplayManagerClosed) || errors.Is(err, errReplaySessionCapacity) || errors.Is(err, errReplaySessionProjectCapacity) || errors.Is(err, errReplaySessionLeaseUnavailable) {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	if writeScriptReplayBlocked(w, err) {
		return
	}
	if errors.Is(err, errBrowserReplaySessionIDConflict) || errors.Is(err, errReplaySessionIDConflict) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	// The cause is logged with the response's ref, never sent.
	writeError(w, http.StatusInternalServerError, "failed to start browser replay: "+err.Error())
}

func applyBrowserReplayOverrides(script *dsl.Script, req BrowserReplayStartRequest) *dsl.Script {
	return recordbridge.ApplyBrowserReplayOverrides(script, recordbridge.BrowserReplayStartRequest{Browser: req.Browser})
}

// OpenAPI-Responses: 404, 503
func (s *Server) handleGetBrowserReplaySession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	authorizedProjectID, _ := authorizedProjectFromRequest(r)
	service := s.recordingHTTPService()
	session, err := service.ReplaySessionFor(r.Context(), sessionID, authorizedProjectID)
	w.Header().Set("Cache-Control", "no-store")
	if err != nil {
		service.WriteRetainedReplayError(w, err)
		return
	}
	if durability := session.ArtifactDurability(); durability != "" {
		w.Header().Set("X-APEXLoad-Replay-Durability", durability)
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
