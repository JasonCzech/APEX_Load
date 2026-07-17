package api

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
)

type ReplayScriptRequest struct {
	GroupName       string                 `json:"group_name,omitempty"`
	ProjectID       string                 `json:"project_id,omitempty"`
	ReplaySessionID string                 `json:"replay_session_id,omitempty"`
	Variables       map[string]string      `json:"variables,omitempty"`
	RedactionRules  []engine.RedactionRule `json:"redaction_rules,omitempty"`
	IgnoreThinkTime *bool                  `json:"ignore_think_time,omitempty"`
	Script          *dsl.Script            `json:"script,omitempty"`
}

func (s *Server) handleReplayDraftScript(w http.ResponseWriter, r *http.Request) {
	var req ReplayScriptRequest
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
	s.handleReplayRequest(w, "", req.Script, req)
}

func (s *Server) handleReplayScript(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	stored, ok := s.runner.GetScript(id)
	if !ok {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	if !requireAuthorizedProjectMatch(w, r, stored.ProjectID) {
		return
	}

	var req ReplayScriptRequest
	if r.Body != nil && r.ContentLength != 0 {
		if !decodeJSONBody(w, r, &req) {
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
	if err := bindStoredScriptProject(script, stored.Script.ProjectID, req.ProjectID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	s.handleReplayRequest(w, id, script, req)
}

func (s *Server) handleReplayRequest(w http.ResponseWriter, id string, script *dsl.Script, req ReplayScriptRequest) {
	var prepared bool
	script, prepared = prepareTransientExecutableScript(w, script)
	if !prepared {
		return
	}
	issues := validateScriptForReplay(script)
	warnings := validationWarnings(script)
	secretResolver := s.runner.secretResolverForProject(script.ProjectID)
	preflight := buildReplayPreflight(script, req.Variables, issues, warnings, secretResolver)
	if len(issues) > 0 || len(preflight.MissingSecrets) > 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"script_id": id,
			"valid":     preflight.Valid,
			"issues":    issues,
			"warnings":  warnings,
			"preflight": preflight,
		})
		return
	}

	ignoreThinkTime := true
	if req.IgnoreThinkTime != nil {
		ignoreThinkTime = *req.IgnoreThinkTime
	}
	replaySessionID := req.ReplaySessionID
	if replaySessionID == "" {
		replaySessionID = uuid.New().String()
	}

	report, err := s.runner.ReplayScriptDefinition(script, engine.ReplayOptions{
		ReplaySessionID: replaySessionID,
		GroupName:       req.GroupName,
		RuntimeProfile:  &engine.ReplayRuntimeProfile{Name: "classic replay", CaptureMode: "diagnostic", LogLevel: "info", IgnoreThinkTime: ignoreThinkTime},
		Variables:       req.Variables,
		RedactionRules:  req.RedactionRules,
		SecretResolver:  secretResolver,
		IgnoreThinkTime: ignoreThinkTime,
		OnProgress: func(event engine.ReplayProgressEvent) {
			s.broadcastReplayProgress(replaySessionID, id, normalizeProjectID(script.ProjectID), event)
		},
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"script_id":         id,
		"replay_session_id": replaySessionID,
		"preflight":         preflight,
		"report":            report,
		"warnings":          warnings,
	})
}

// prepareTransientExecutableScript applies the same disclosure-safe binding
// boundary used by durable script writes to replay, debugger, and browser
// drafts. Caller-supplied overrides are executable even though they are not
// persisted, so plaintext credentials and executable sentinels must be
// converted through the Credentials flow before any session or progress event
// can observe them.
func prepareTransientExecutableScript(w http.ResponseWriter, script *dsl.Script) (*dsl.Script, bool) {
	projected := maskScriptSecrets(script)
	if projected == nil {
		writeError(w, http.StatusBadRequest, "failed to project script for secret binding")
		return nil, false
	}
	if unresolved := discoverSecretBindingRequirements(projected, 0); len(unresolved) > 0 {
		writeSecretBindingsRequired(w, unresolved)
		return nil, false
	}
	return projected, true
}

func (s *Server) broadcastReplayProgress(replaySessionID string, scriptID string, projectID string, event engine.ReplayProgressEvent) {
	message := map[string]any{
		"type":              event.Type,
		"event_id":          event.EventID,
		"replay_session_id": replaySessionID,
		"script_id":         scriptID,
		"project_id":        projectID,
		"timestamp":         event.Timestamp,
	}
	if event.EventIndex >= 0 {
		message["event_index"] = event.EventIndex
	}
	if event.Event != nil {
		message["event"] = event.Event
	}
	if event.Action != nil {
		message["action"] = event.Action
	}
	if event.Report != nil {
		message["report"] = event.Report
	}
	s.hub.BroadcastJSON(message)
}
