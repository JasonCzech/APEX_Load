package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jasonczech/stormrunner/pkg/dsl"
)

func (s *Server) handleCreateTest(w http.ResponseWriter, r *http.Request) {
	var req CreateTestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	test, err := s.runner.CreateTest(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, test)
}

func (s *Server) handleEstimateTest(w http.ResponseWriter, r *http.Request) {
	var req CreateTestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	estimate, err := s.runner.EstimateDraftCapacity(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, estimate)
}

func (s *Server) handleListTests(w http.ResponseWriter, r *http.Request) {
	applicationID := r.URL.Query().Get("application_id")
	releaseID := r.URL.Query().Get("release_id")
	scenarioID := r.URL.Query().Get("scenario_id")
	if applicationID != "" || releaseID != "" || scenarioID != "" {
		tests := s.runner.ListTestsByContext(applicationID, releaseID, scenarioID)
		writeJSON(w, http.StatusOK, map[string]any{
			"tests": tests,
			"count": len(tests),
		})
		return
	}

	projectID := r.URL.Query().Get("project_id")
	var folderID *string
	if r.URL.Query().Has("folder_id") {
		value := r.URL.Query().Get("folder_id")
		folderID = &value
	}
	if projectID == "" && folderID == nil {
		tests := s.runner.ListTests()
		writeJSON(w, http.StatusOK, map[string]any{
			"tests": tests,
			"count": len(tests),
		})
		return
	}
	tests := s.runner.ListTestsFiltered(projectID, folderID)
	writeJSON(w, http.StatusOK, map[string]any{
		"tests": tests,
		"count": len(tests),
	})
}

func (s *Server) handleGetTest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	test, ok := s.runner.GetTest(id)
	if !ok {
		writeError(w, http.StatusNotFound, "test not found")
		return
	}
	writeJSON(w, http.StatusOK, test)
}

func (s *Server) handleStartTest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req StartTestOptions
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
	}
	if err := s.runner.StartTestWithOptions(id, req); err != nil {
		var capacityErr *CapacityShortfallError
		if errors.As(err, &capacityErr) {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":              capacityErr.Error(),
				"code":               "capacity_shortfall",
				"test_id":            id,
				"capacity_advisory":  capacityErr.Advisory,
				"override_available": true,
			})
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "started", "test_id": id})
}

func (s *Server) handleStopTest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.runner.StopTest(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopping", "test_id": id})
}

func (s *Server) handleAbortTest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.runner.AbortTest(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "aborted", "test_id": id})
}

func (s *Server) handlePauseTest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.runner.PauseTest(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "paused", "test_id": id})
}

func (s *Server) handleResumeTest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.runner.ResumeTest(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "resumed", "test_id": id})
}

func (s *Server) handleDeleteTest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.runner.DeleteTest(id) {
		writeError(w, http.StatusNotFound, "test not found or currently running")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

func (s *Server) handleGetMetrics(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	test, ok := s.runner.GetTest(id)
	if !ok {
		writeError(w, http.StatusNotFound, "test not found")
		return
	}
	if test.LiveMetrics == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"test_id": id,
			"status":  test.Status,
			"message": "no metrics available yet",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"test_id": id,
		"status":  test.Status,
		"metrics": test.LiveMetrics,
		"history": test.History,
	})
}

func (s *Server) handleCreateScript(w http.ResponseWriter, r *http.Request) {
	var wrapper struct {
		ProjectID string     `json:"project_id,omitempty"`
		FolderID  *string    `json:"folder_id,omitempty"`
		Script    dsl.Script `json:"script"`
	}
	if err := json.NewDecoder(r.Body).Decode(&wrapper); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	script := &wrapper.Script
	if wrapper.ProjectID != "" {
		script.ProjectID = wrapper.ProjectID
	}
	if wrapper.FolderID != nil {
		script.FolderID = wrapper.FolderID
	}

	if script.Name == "" {
		writeError(w, http.StatusBadRequest, "script name is required")
		return
	}
	if script.Protocol == "" {
		writeError(w, http.StatusBadRequest, "script protocol is required")
		return
	}
	if len(script.Actions) == 0 {
		writeError(w, http.StatusBadRequest, "script must have at least one action")
		return
	}

	stored := s.runner.StoreScript(script)
	writeJSON(w, http.StatusCreated, stored)
}

func (s *Server) handleListScripts(w http.ResponseWriter, r *http.Request) {
	projectID := r.URL.Query().Get("project_id")
	var folderID *string
	if r.URL.Query().Has("folder_id") {
		value := r.URL.Query().Get("folder_id")
		folderID = &value
	}
	if projectID == "" && folderID == nil {
		scripts := s.runner.ListScriptsWithUsage()
		writeJSON(w, http.StatusOK, map[string]any{
			"scripts": scripts,
			"count":   len(scripts),
		})
		return
	}
	scripts := s.runner.ListScriptsFiltered(projectID, folderID)
	writeJSON(w, http.StatusOK, map[string]any{
		"scripts": scripts,
		"count":   len(scripts),
	})
}

func (s *Server) handleGetScript(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	script, ok := s.runner.GetScript(id)
	if !ok {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	writeJSON(w, http.StatusOK, script)
}

func (s *Server) handleValidateScript(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	script, ok := s.runner.GetScript(id)
	if !ok {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}

	issues := validateScript(script.Script)
	valid := len(issues) == 0

	writeJSON(w, http.StatusOK, map[string]any{
		"script_id": id,
		"valid":     valid,
		"issues":    issues,
	})
}

func (s *Server) handleMoveScript(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		FolderID *string `json:"folder_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if !s.runner.MoveScript(id, req.FolderID) {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "folder_id": req.FolderID})
}

func (s *Server) handleMoveTest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		FolderID *string `json:"folder_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if !s.runner.MoveTest(id, req.FolderID) {
		writeError(w, http.StatusNotFound, "test not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "folder_id": req.FolderID})
}

func (s *Server) handleScaleVUsers(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Group        string `json:"group"`
		TargetVUsers int    `json:"target_vusers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.Group == "" {
		writeError(w, http.StatusBadRequest, "group name is required")
		return
	}
	if req.TargetVUsers < 0 {
		writeError(w, http.StatusBadRequest, "target_vusers must be non-negative")
		return
	}

	if err := s.runner.ScaleVUsers(id, req.Group, req.TargetVUsers); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"test_id":       id,
		"group":         req.Group,
		"target_vusers": req.TargetVUsers,
		"status":        "scaled",
	})
}

func (s *Server) handleAdjustDuration(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Duration string `json:"duration"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	dur, err := time.ParseDuration(req.Duration)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid duration %q: %v", req.Duration, err))
		return
	}
	if dur <= 0 {
		writeError(w, http.StatusBadRequest, "duration must be positive")
		return
	}

	if err := s.runner.AdjustDuration(id, dur); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"test_id":      id,
		"new_duration": req.Duration,
		"status":       "adjusted",
	})
}

func (s *Server) handleHealthCheck(w http.ResponseWriter, r *http.Request) {
	settings := s.runner.GetSystemSettings()
	availability := resolveCapacityAvailability(settings)
	writeJSON(w, http.StatusOK, map[string]any{
		"status":     "healthy",
		"version":    "0.1.0",
		"ws_clients": s.hub.ClientCount(),
		"test_count": len(s.runner.tests),
		"capacity_availability": map[string]any{
			"source":                          availability.Source,
			"configured_available_generators": availability.ConfiguredAvailableGenerators,
			"effective_available_generators":  availability.EffectiveAvailableGenerators,
			"live_available_generators":       availability.LiveAvailableGenerators,
			"warnings":                        availability.Warnings,
		},
	})
}

func (s *Server) handleGetSystemSettings(w http.ResponseWriter, r *http.Request) {
	settings := s.runner.GetSystemSettings()
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) handleUpdateSystemSettings(w http.ResponseWriter, r *http.Request) {
	var settings SystemSettings
	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	updated, err := s.runner.UpdateSystemSettings(settings)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	s.hub.serveWS(w, r, "")
}

func (s *Server) handleTestStream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	_, ok := s.runner.GetTest(id)
	if !ok {
		writeError(w, http.StatusNotFound, "test not found")
		return
	}
	s.hub.serveWS(w, r, id)
}

func validateScript(script *dsl.Script) []string {
	var issues []string
	if script.Protocol == "jms-solace" {
		return validateJMSScript(script)
	}
	if script.Protocol != "browser" && script.Config.BaseURL == "" {
		issues = append(issues, "config.base_url is empty")
	}
	for i, a := range script.Actions {
		if a.Name == "" {
			issues = append(issues, fmt.Sprintf("actions[%d]: missing name", i))
		}
		if script.Protocol == "browser" {
			switch a.Type {
			case "browser_screenshot":
				continue
			case "browser_wait":
				if a.WaitFor == nil && a.Selector == "" && a.Target == "" {
					issues = append(issues, fmt.Sprintf("actions[%d] %q: browser_wait requires wait_for, selector, or target", i, a.Name))
				}
			default:
				if a.Target == "" && a.Selector == "" {
					issues = append(issues, fmt.Sprintf("actions[%d] %q: missing target or selector", i, a.Name))
				}
			}
			continue
		}
		if a.Target == "" {
			issues = append(issues, fmt.Sprintf("actions[%d] %q: missing target", i, a.Name))
		}
	}
	return issues
}

func validateJMSScript(script *dsl.Script) []string {
	var issues []string
	jms := script.Config.JMS
	if !jms.Mock {
		if jms.BrokerURL == "" {
			issues = append(issues, "config.jms.broker_url is required when mock mode is disabled")
		}
		if jms.MessageVPN == "" {
			issues = append(issues, "config.jms.message_vpn is recommended when mock mode is disabled")
		}
		issues = append(issues, "real Solace JMS adapter is not available in this build; enable config.jms.mock for replay")
	}
	if jms.Destination == "" {
		usesActionDestinations := true
		for _, a := range script.Actions {
			switch a.Type {
			case "jms_publish", "jms_consume", "jms_request_reply":
				if a.Target == "" {
					usesActionDestinations = false
				}
			}
		}
		if !usesActionDestinations {
			issues = append(issues, "config.jms.destination or per-action target is required")
		}
	}
	for i, a := range script.Actions {
		if a.Name == "" {
			issues = append(issues, fmt.Sprintf("actions[%d]: missing name", i))
		}
		switch a.Type {
		case "jms_publish", "jms_consume", "jms_request_reply", "jms_ack", "jms_commit", "jms_rollback":
		default:
			if a.Type == "" {
				issues = append(issues, fmt.Sprintf("actions[%d]: missing type", i))
			} else {
				issues = append(issues, fmt.Sprintf("actions[%d] %q: unsupported JMS action type %q", i, a.Name, a.Type))
			}
		}
		if (a.Type == "jms_publish" || a.Type == "jms_consume" || a.Type == "jms_request_reply") && a.Target == "" && jms.Destination == "" {
			issues = append(issues, fmt.Sprintf("actions[%d] %q: missing destination", i, a.Name))
		}
		if a.Type == "jms_request_reply" && jms.ReplyTo == "" {
			replyTo, _ := a.Params["reply_to"].(string)
			if strings.TrimSpace(replyTo) == "" {
				issues = append(issues, fmt.Sprintf("actions[%d] %q: missing reply_to destination", i, a.Name))
			}
		}
	}
	return issues
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
