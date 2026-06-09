package api

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
)

type ReplayScriptRequest struct {
	GroupName       string                 `json:"group_name,omitempty"`
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.Script == nil {
		writeError(w, http.StatusBadRequest, "script is required")
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

	var req ReplayScriptRequest
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
	s.handleReplayRequest(w, id, script, req)
}

func (s *Server) handleReplayRequest(w http.ResponseWriter, id string, script *dsl.Script, req ReplayScriptRequest) {
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
