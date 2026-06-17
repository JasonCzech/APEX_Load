package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/buildinfo"
	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
	"github.com/jasonczech/apexload/pkg/dsl"
	browserprotocol "github.com/jasonczech/apexload/pkg/protocol/browser"
)

func (s *Server) handleCreateTest(w http.ResponseWriter, r *http.Request) {
	var req CreateTestRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}

	test, err := s.runner.CreateTest(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	actorType, actorSubject, actorDisplayName := s.resolveAuditActor(r)
	if actorType == "user" || actorType == "service_account" {
		// AttributeTestCreated sets CreatedBySubject/CreatedByDisplayName under
		// tr.mu; writing them here directly would race the run/read paths.
		s.runner.AttributeTestCreated(test.ID, actorSubject, actorDisplayName)
	}
	s.recordAuditEvent(r, "test.create", "success", "test", test.ID, test.ProjectID, map[string]any{"name": test.Name, "status": test.Status})

	writeJSON(w, http.StatusCreated, test)
}

func (s *Server) handleEstimateTest(w http.ResponseWriter, r *http.Request) {
	var req CreateTestRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}

	estimate, err := s.runner.EstimateDraftCapacity(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, estimate)
}

func (s *Server) handlePreflightTest(w http.ResponseWriter, r *http.Request) {
	var req LaunchPreflightRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}

	preflight, err := s.runner.PreflightLaunch(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, preflight)
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

func (s *Server) handleGetTestManifest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	manifest, err := s.runner.GetTestManifest(id)
	if err != nil {
		if strings.Contains(err.Error(), "test not found") {
			writeError(w, http.StatusNotFound, "test not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, manifest)
}

func (s *Server) handleStartTest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req StartTestOptions
	if r.Body != nil && r.ContentLength != 0 {
		if !decodeJSONBody(w, r, &req) {
			return
		}
	}
	if err := s.runner.StartTestWithOptions(id, req); err != nil {
		var capacityErr *CapacityShortfallError
		if errors.As(err, &capacityErr) {
			if test, ok := s.runner.GetTest(id); ok {
				s.recordAuditEvent(r, "capacity.placement.reject", "blocked", "test", id, test.ProjectID, capacityShortfallAuditDetails(capacityErr.Advisory, req.OverrideCapacityShortfall))
			}
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":              capacityErr.Error(),
				"code":               "capacity_shortfall",
				"test_id":            id,
				"capacity_advisory":  capacityErr.Advisory,
				"override_available": true,
			})
			return
		}
		var browserDensityErr *BrowserDensityGateError
		if errors.As(err, &browserDensityErr) {
			if test, ok := s.runner.GetTest(id); ok {
				s.recordAuditEvent(r, "capacity.browser_density.reject", "blocked", "test", id, test.ProjectID, browserDensityGateAuditDetails(browserDensityErr.Advisory))
			}
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":              browserDensityErr.Error(),
				"code":               "browser_density_release_gate",
				"test_id":            id,
				"capacity_advisory":  browserDensityErr.Advisory,
				"override_available": false,
			})
			return
		}
		var shakeoutErr *ScriptShakeoutGateError
		if errors.As(err, &shakeoutErr) {
			if test, ok := s.runner.GetTest(id); ok {
				s.recordAuditEvent(r, "test.script_shakeout.review", "blocked", "test", id, test.ProjectID, map[string]any{"status": test.Status, "error": test.Error, "report": shakeoutErr.Report})
			}
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":              shakeoutErr.Error(),
				"code":               "script_shakeout_failed",
				"test_id":            id,
				"status":             "shakeout_review",
				"script_shakeout":    shakeoutErr.Report,
				"override_available": true,
				"cancel_available":   true,
			})
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	actorType, actorSubject, actorDisplayName := s.resolveAuditActor(r)
	if actorType == "user" || actorType == "service_account" {
		s.runner.AttributeTestStarted(id, actorSubject, actorDisplayName)
	}
	test, ok := s.waitForStartResponseState(id)
	if !ok {
		writeError(w, http.StatusNotFound, "test not found")
		return
	}
	if test.Status == "PENDING" {
		details := map[string]any{"status": test.Status, "pending_reason": test.PendingReason, "queue_position": test.QueuePosition}
		s.recordAuditEvent(r, "test.start", "pending", "test", id, test.ProjectID, details)
		s.recordAuditEvent(r, "capacity.fair_share.defer", "blocked", "test", id, test.ProjectID, details)
		writeJSON(w, http.StatusOK, map[string]any{
			"status":  "pending",
			"test_id": id,
			"message": "test queued until project capacity is available",
		})
		return
	}
	s.recordAuditEvent(r, "test.start", "success", "test", id, test.ProjectID, map[string]any{"status": test.Status})
	writeJSON(w, http.StatusOK, map[string]any{"status": "started", "test_id": id, "test": test})
}

const startResponseStateTimeout = 750 * time.Millisecond

func (s *Server) waitForStartResponseState(id string) (*ManagedTest, bool) {
	deadline := time.Now().Add(startResponseStateTimeout)
	for {
		test, ok := s.runner.GetTest(id)
		if !ok {
			return nil, false
		}
		if test.Status != "INITIALIZING" || time.Now().After(deadline) {
			return test, true
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func capacityShortfallAuditDetails(advisory *CapacityAdvisory, overrideRequested bool) map[string]any {
	details := map[string]any{
		"reason":             "capacity_shortfall",
		"override_requested": overrideRequested,
	}
	if advisory == nil {
		return details
	}
	details["total_vusers"] = advisory.TotalVUsers
	details["recommended_generators"] = advisory.RecommendedGenerators
	details["available_generators"] = advisory.AvailableGenerators
	details["shortfall_generators"] = advisory.ShortfallGenerators
	details["requires_override"] = advisory.RequiresOverride
	details["availability_source"] = advisory.AvailabilitySource
	if len(advisory.Warnings) > 0 {
		details["warnings"] = append([]string(nil), advisory.Warnings...)
	}
	return details
}

func browserDensityGateAuditDetails(advisory *CapacityAdvisory) map[string]any {
	details := map[string]any{
		"reason": "browser_density_release_gate",
	}
	if advisory == nil {
		return details
	}
	details["browser_vusers"] = advisory.BrowserVUsers
	details["max_browser_canary_vusers_per_generator"] = advisory.MaxBrowserCanaryVUsersPerGenerator
	details["browser_density_tier"] = advisory.BrowserDensityTier
	details["browser_density_release_gate"] = advisory.BrowserDensityReleaseGate
	details["browser_benchmark_evidence_attached"] = advisory.BrowserBenchmarkEvidenceAttached
	details["browser_density_override_requested"] = advisory.BrowserDensityOverrideRequested
	if len(advisory.Warnings) > 0 {
		details["warnings"] = append([]string(nil), advisory.Warnings...)
	}
	return details
}

func (s *Server) handlePinComparisonBaseline(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	test, err := s.runner.PinComparisonBaseline(id)
	if err != nil {
		if strings.Contains(err.Error(), "test not found") {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "test.comparison_baseline.pin", "success", "test", id, test.ProjectID, map[string]any{"name": test.Name, "scenario_id": test.ScenarioID, "baseline_pinned": test.BaselinePinned})
	writeJSON(w, http.StatusOK, test)
}

func (s *Server) handleUnpinComparisonBaseline(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	test, err := s.runner.UnpinComparisonBaseline(id)
	if err != nil {
		if strings.Contains(err.Error(), "test not found") {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "test.comparison_baseline.unpin", "success", "test", id, test.ProjectID, map[string]any{"name": test.Name, "scenario_id": test.ScenarioID, "baseline_pinned": test.BaselinePinned})
	writeJSON(w, http.StatusOK, test)
}

func (s *Server) handleStopTest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.runner.StopTest(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	projectID := ""
	if test, ok := s.runner.GetTest(id); ok {
		projectID = test.ProjectID
	}
	s.recordAuditEvent(r, "test.stop", "success", "test", id, projectID, nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopping", "test_id": id})
}

func (s *Server) handleAbortTest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.runner.AbortTest(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	projectID := ""
	if test, ok := s.runner.GetTest(id); ok {
		projectID = test.ProjectID
	}
	s.recordAuditEvent(r, "test.abort", "success", "test", id, projectID, nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "aborted", "test_id": id})
}

func (s *Server) handlePauseTest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.runner.PauseTest(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	projectID := ""
	if test, ok := s.runner.GetTest(id); ok {
		projectID = test.ProjectID
	}
	s.recordAuditEvent(r, "test.pause", "success", "test", id, projectID, nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "paused", "test_id": id})
}

func (s *Server) handleResumeTest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.runner.ResumeTest(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	projectID := ""
	if test, ok := s.runner.GetTest(id); ok {
		projectID = test.ProjectID
	}
	s.recordAuditEvent(r, "test.resume", "success", "test", id, projectID, nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "resumed", "test_id": id})
}

func (s *Server) handleDeleteTest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	projectID := ""
	name := ""
	status := ""
	if test, ok := s.runner.GetTest(id); ok {
		projectID = test.ProjectID
		name = test.Name
		status = test.Status
	}
	if !s.runner.DeleteTest(id) {
		writeError(w, http.StatusNotFound, "test not found or currently active")
		return
	}
	s.recordAuditEvent(r, "test.delete", "success", "test", id, projectID, map[string]any{"name": name, "status": status})
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
	groupFilter := parseGroupFilterQuery(r.URL.Query())
	writeJSON(w, http.StatusOK, map[string]any{
		"test_id": id,
		"status":  test.Status,
		"metrics": filterLiveMetricsForGroups(test.LiveMetrics, groupFilter),
		"history": filterLiveMetricsHistoryForGroups(test.History, groupFilter),
	})
}

func (s *Server) handleGetLiveErrorEvidence(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	signature := strings.TrimSpace(r.URL.Query().Get("signature"))
	if signature == "" {
		writeError(w, http.StatusBadRequest, "signature is required")
		return
	}
	action := strings.TrimSpace(r.URL.Query().Get("action"))
	limit := defaultLiveErrorEvidenceLimit
	if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		limit = parsed
	}
	evidence, ok := s.runner.ListLiveErrorEvidence(id, signature, action, limit)
	if !ok {
		writeError(w, http.StatusNotFound, "test not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"test_id":   id,
		"signature": signature,
		"evidence":  evidence,
	})
}

func (s *Server) handleCreateScript(w http.ResponseWriter, r *http.Request) {
	var wrapper struct {
		ProjectID string     `json:"project_id,omitempty"`
		FolderID  *string    `json:"folder_id,omitempty"`
		Script    dsl.Script `json:"script"`
		// SourceRecordingSessionID, when set, captures the recorded baseline from
		// the still-live recording session this script was generated from, for
		// later record-vs-replay correlation diagnosis.
		SourceRecordingSessionID string `json:"source_recording_session_id,omitempty"`
	}
	if !decodeJSONBody(w, r, &wrapper) {
		return
	}
	script := &wrapper.Script
	if wrapper.ProjectID != "" {
		script.ProjectID = wrapper.ProjectID
	}
	if wrapper.FolderID != nil {
		script.FolderID = wrapper.FolderID
	}

	if issues := validateScript(script); len(issues) > 0 {
		writeError(w, http.StatusBadRequest, strings.Join(issues, "; "))
		return
	}

	stored := s.runner.StoreScript(script)
	s.captureRecordingBaseline(stored.ID, wrapper.SourceRecordingSessionID)
	s.recordAuditEvent(r, "script.create", "success", "script", stored.ID, stored.ProjectID, map[string]any{"name": stored.Name, "folder_id": stored.FolderID})
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

	writeJSON(w, http.StatusOK, buildScriptValidationResponse(id, script.Script))
}

func (s *Server) handleValidateDraftScript(w http.ResponseWriter, r *http.Request) {
	var wrapper struct {
		ProjectID string            `json:"project_id,omitempty"`
		FolderID  *string           `json:"folder_id,omitempty"`
		Script    dsl.Script        `json:"script"`
		Variables map[string]string `json:"variables,omitempty"`
	}
	if !decodeJSONBody(w, r, &wrapper) {
		return
	}

	script := &wrapper.Script
	if wrapper.ProjectID != "" {
		script.ProjectID = wrapper.ProjectID
	}
	if wrapper.FolderID != nil {
		script.FolderID = wrapper.FolderID
	}

	writeJSON(w, http.StatusOK, buildScriptValidationResponseWithVariables("", script, wrapper.Variables))
}

func (s *Server) handleMoveScript(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	stored, ok := s.runner.GetScript(id)
	if !ok {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	var req struct {
		FolderID *string `json:"folder_id"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if !s.runner.MoveScript(id, req.FolderID) {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	s.recordAuditEvent(r, "script.move", "success", "script", id, stored.ProjectID, map[string]any{"name": stored.Name, "folder_id": req.FolderID})
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "folder_id": req.FolderID})
}

func (s *Server) handleMoveTest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	test, ok := s.runner.GetTest(id)
	if !ok {
		writeError(w, http.StatusNotFound, "test not found")
		return
	}
	var req struct {
		FolderID *string `json:"folder_id"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if !s.runner.MoveTest(id, req.FolderID) {
		writeError(w, http.StatusNotFound, "test not found")
		return
	}
	s.recordAuditEvent(r, "test.move", "success", "test", id, test.ProjectID, map[string]any{"name": test.Name, "folder_id": req.FolderID})
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "folder_id": req.FolderID})
}

func (s *Server) handleScaleVUsers(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Group        string `json:"group"`
		Action       string `json:"action,omitempty"`
		TargetVUsers int    `json:"target_vusers"`
		RampDuration string `json:"ramp_duration,omitempty"`
	}
	if !decodeJSONBody(w, r, &req) {
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
	action := strings.TrimSpace(req.Action)
	if action == "" {
		action = "run"
	}
	if action != "run" && action != "pause" && action != "stop" {
		writeError(w, http.StatusBadRequest, "action must be one of run, pause, or stop")
		return
	}
	targetVUsers := req.TargetVUsers
	if action == "pause" || action == "stop" {
		targetVUsers = 0
	}
	var rampDuration time.Duration
	if strings.TrimSpace(req.RampDuration) != "" {
		var err error
		rampDuration, err = time.ParseDuration(req.RampDuration)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid ramp_duration %q: %v", req.RampDuration, err))
			return
		}
		if rampDuration <= 0 {
			writeError(w, http.StatusBadRequest, "ramp_duration must be positive")
			return
		}
	}

	if err := s.runner.ScaleVUsersWithOptions(id, req.Group, targetVUsers, ScaleVUsersOptions{RampDuration: rampDuration}); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	projectID := ""
	name := ""
	if test, ok := s.runner.GetTest(id); ok {
		projectID = test.ProjectID
		name = test.Name
	}
	auditDetails := map[string]any{"name": name, "group": req.Group, "action": action, "target_vusers": targetVUsers}
	if rampDuration > 0 {
		auditDetails["ramp_duration"] = req.RampDuration
	}
	s.recordAuditEvent(r, "test.vusers.scale", "success", "test", id, projectID, auditDetails)

	payload := map[string]any{
		"test_id":       id,
		"group":         req.Group,
		"action":        action,
		"target_vusers": targetVUsers,
		"status":        "scaled",
	}
	if action == "pause" {
		payload["status"] = "paused"
	} else if action == "stop" {
		payload["status"] = "stopped"
	}
	if rampDuration > 0 {
		payload["status"] = "ramping"
		payload["ramp_duration"] = req.RampDuration
	}
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) handleUpdateGoalTarget(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Group     string  `json:"group"`
		TargetTPS float64 `json:"target_tps"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Group) == "" {
		writeError(w, http.StatusBadRequest, "group name is required")
		return
	}
	if req.TargetTPS <= 0 || math.IsNaN(req.TargetTPS) || math.IsInf(req.TargetTPS, 0) {
		writeError(w, http.StatusBadRequest, "target_tps must be greater than zero")
		return
	}

	update, err := s.runner.UpdateGoalTargetTPS(id, req.Group, req.TargetTPS)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	projectID := ""
	name := ""
	if test, ok := s.runner.GetTest(id); ok {
		projectID = test.ProjectID
		name = test.Name
	}
	s.recordAuditEvent(r, "test.goal_target.adjust", "success", "test", id, projectID, map[string]any{
		"name":               name,
		"group":              req.Group,
		"target_tps":         update.TargetTPS,
		"target_average_tps": update.TargetAverageTPS,
	})

	writeJSON(w, http.StatusOK, update)
}

func (s *Server) handleAdjustDuration(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Duration string `json:"duration"`
	}
	if !decodeJSONBody(w, r, &req) {
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
	projectID := ""
	name := ""
	if test, ok := s.runner.GetTest(id); ok {
		projectID = test.ProjectID
		name = test.Name
	}
	s.recordAuditEvent(r, "test.duration.adjust", "success", "test", id, projectID, map[string]any{"name": name, "duration": req.Duration})

	writeJSON(w, http.StatusOK, map[string]any{
		"test_id":      id,
		"new_duration": req.Duration,
		"status":       "adjusted",
	})
}

func (s *Server) handleSystemVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.versionResponse())
}

func (s *Server) handleHealthCheck(w http.ResponseWriter, r *http.Request) {
	settings := s.runner.GetSystemSettings()
	availability := resolveCapacityAvailability(settings)
	build := buildinfo.Get()
	writeJSON(w, http.StatusOK, map[string]any{
		"status":          "healthy",
		"version":         build.Version,
		"display_version": build.DisplayVersion,
		"commit":          build.Commit,
		"revision":        build.Revision,
		"environment":     build.Environment,
		"build_time":      build.BuildTime,
		"ws_clients":      s.hub.ClientCount(),
		"test_count":      s.runner.TestCount(),
		"capacity_availability": map[string]any{
			"source":                          availability.Source,
			"configured_available_generators": availability.ConfiguredAvailableGenerators,
			"effective_available_generators":  availability.EffectiveAvailableGenerators,
			"live_available_generators":       availability.LiveAvailableGenerators,
			"reserved_generators":             availability.ReservedGenerators,
			"shared_available_generators":     availability.SharedAvailableGenerators,
			"warnings":                        availability.Warnings,
		},
	})
}

func (s *Server) handlePrometheusMetrics(w http.ResponseWriter, r *http.Request) {
	settings := s.runner.GetSystemSettings()
	availability := resolveCapacityAvailability(settings)
	tests := s.runner.ListTests()
	regionStats := s.generatorRegionStats(r.Context())
	scalingStats := scalingSignalStats(tests)
	statusCounts := make(map[string]int)
	for _, test := range tests {
		if test == nil || strings.TrimSpace(test.Status) == "" {
			continue
		}
		statusCounts[test.Status]++
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	var body strings.Builder
	body.WriteString("# HELP apexload_up Whether the APEX Load controller is healthy.\n")
	body.WriteString("# TYPE apexload_up gauge\n")
	body.WriteString("apexload_up 1\n")
	body.WriteString("# HELP apexload_websocket_clients Number of connected websocket clients.\n")
	body.WriteString("# TYPE apexload_websocket_clients gauge\n")
	fmt.Fprintf(&body, "apexload_websocket_clients %d\n", s.hub.ClientCount())
	body.WriteString("# HELP apexload_test_count Number of tracked tests in memory.\n")
	body.WriteString("# TYPE apexload_test_count gauge\n")
	fmt.Fprintf(&body, "apexload_test_count %d\n", len(tests))
	body.WriteString("# HELP apexload_tests_by_status Number of tracked tests by status.\n")
	body.WriteString("# TYPE apexload_tests_by_status gauge\n")
	for status, count := range statusCounts {
		fmt.Fprintf(&body, "apexload_tests_by_status{status=%q} %d\n", status, count)
	}
	body.WriteString("# HELP apexload_scaling_vusers VUsers by autoscaling demand state.\n")
	body.WriteString("# TYPE apexload_scaling_vusers gauge\n")
	fmt.Fprintf(&body, "apexload_scaling_vusers{state=%q} %d\n", "queued", scalingStats.QueuedVUsers)
	fmt.Fprintf(&body, "apexload_scaling_vusers{state=%q} %d\n", "active", scalingStats.ActiveVUsers)
	body.WriteString("# HELP apexload_scaling_tests Tests by autoscaling demand state.\n")
	body.WriteString("# TYPE apexload_scaling_tests gauge\n")
	fmt.Fprintf(&body, "apexload_scaling_tests{state=%q} %d\n", "queued", scalingStats.QueuedTests)
	fmt.Fprintf(&body, "apexload_scaling_tests{state=%q} %d\n", "active", scalingStats.ActiveTests)
	body.WriteString("# HELP apexload_scaling_protocol_vusers VUsers by autoscaling demand state and protocol.\n")
	body.WriteString("# TYPE apexload_scaling_protocol_vusers gauge\n")
	writeScalingProtocolVUsers(&body, "queued", scalingStats.QueuedProtocolVUsers)
	writeScalingProtocolVUsers(&body, "active", scalingStats.ActiveProtocolVUsers)
	body.WriteString("# HELP apexload_capacity_available_generators Available generators from capacity planning.\n")
	body.WriteString("# TYPE apexload_capacity_available_generators gauge\n")
	fmt.Fprintf(&body, "apexload_capacity_available_generators{scope=%q} %d\n", "configured", availability.ConfiguredAvailableGenerators)
	fmt.Fprintf(&body, "apexload_capacity_available_generators{scope=%q} %d\n", "effective", availability.EffectiveAvailableGenerators)
	fmt.Fprintf(&body, "apexload_capacity_available_generators{scope=%q} %d\n", "reserved", availability.ReservedGenerators)
	fmt.Fprintf(&body, "apexload_capacity_available_generators{scope=%q} %d\n", "shared", availability.SharedAvailableGenerators)
	if availability.LiveAvailableGenerators != nil {
		fmt.Fprintf(&body, "apexload_capacity_available_generators{scope=%q} %d\n", "live", *availability.LiveAvailableGenerators)
	}
	if len(regionStats) > 0 {
		body.WriteString("# HELP apexload_generator_region_count Number of configured generator inventory entries by region.\n")
		body.WriteString("# TYPE apexload_generator_region_count gauge\n")
		body.WriteString("# HELP apexload_generator_region_available_capacity Available VUser capacity reported by generators by region.\n")
		body.WriteString("# TYPE apexload_generator_region_available_capacity gauge\n")
		body.WriteString("# HELP apexload_generator_region_active_vusers Active VUsers reported by generators by region.\n")
		body.WriteString("# TYPE apexload_generator_region_active_vusers gauge\n")
		body.WriteString("# HELP apexload_generator_capacity_state_count Number of generators by region and capacity state.\n")
		body.WriteString("# TYPE apexload_generator_capacity_state_count gauge\n")
		body.WriteString("# HELP apexload_generator_drain_state_count Number of generators by region and drain state.\n")
		body.WriteString("# TYPE apexload_generator_drain_state_count gauge\n")
		body.WriteString("# HELP apexload_generator_compatibility_state_count Number of generators by region and compatibility state.\n")
		body.WriteString("# TYPE apexload_generator_compatibility_state_count gauge\n")
		regions := make([]string, 0, len(regionStats))
		for region := range regionStats {
			regions = append(regions, region)
		}
		sort.Strings(regions)
		for _, region := range regions {
			stats := regionStats[region]
			fmt.Fprintf(&body, "apexload_generator_region_count{region=%q} %d\n", region, stats.Count)
			fmt.Fprintf(&body, "apexload_generator_region_available_capacity{region=%q} %d\n", region, stats.AvailableCapacity)
			fmt.Fprintf(&body, "apexload_generator_region_active_vusers{region=%q} %d\n", region, stats.ActiveVUsers)
			writeLabeledCounts(&body, "apexload_generator_capacity_state_count", "region", region, "state", stats.CapacityStates)
			writeLabeledCounts(&body, "apexload_generator_drain_state_count", "region", region, "state", stats.DrainStates)
			writeLabeledCounts(&body, "apexload_generator_compatibility_state_count", "region", region, "state", stats.CompatibilityStates)
		}
	}
	body.WriteString("# HELP apexload_max_concurrent_tests Configured maximum concurrent tests.\n")
	body.WriteString("# TYPE apexload_max_concurrent_tests gauge\n")
	fmt.Fprintf(&body, "apexload_max_concurrent_tests %d\n", settings.General.MaxConcurrentTests)
	body.WriteString("# HELP apexload_metrics_interval_seconds Configured metrics publish interval in seconds.\n")
	body.WriteString("# TYPE apexload_metrics_interval_seconds gauge\n")
	fmt.Fprintf(&body, "apexload_metrics_interval_seconds %d\n", settings.General.MetricsIntervalSeconds)
	githubSettings := s.currentGitHubSettings()
	body.WriteString("# HELP apexload_github_integration_enabled Whether GitHub Enterprise integration is enabled.\n")
	body.WriteString("# TYPE apexload_github_integration_enabled gauge\n")
	fmt.Fprintf(&body, "apexload_github_integration_enabled %d\n", boolMetric(githubSettings.Enabled))
	body.WriteString("# HELP apexload_github_auth_configured GitHub auth methods configured by type.\n")
	body.WriteString("# TYPE apexload_github_auth_configured gauge\n")
	fmt.Fprintf(&body, "apexload_github_auth_configured{mode=%q} %d\n", "oauth", boolMetric(githubSettings.oauthConfigured()))
	fmt.Fprintf(&body, "apexload_github_auth_configured{mode=%q} %d\n", "app", boolMetric(githubSettings.appConfigured()))
	fmt.Fprintf(&body, "apexload_github_auth_configured{mode=%q} %d\n", "pat", boolMetric(githubSettings.patConfigured()))
	if store, ok := s.githubEnterpriseStore(); ok {
		if jobs, err := store.ListGitHubSyncJobs("", 100); err == nil {
			counts := map[string]int{}
			for _, job := range jobs {
				counts[job.Status]++
			}
			body.WriteString("# HELP apexload_github_sync_jobs Recent GitHub sync jobs by status.\n")
			body.WriteString("# TYPE apexload_github_sync_jobs gauge\n")
			for status, count := range counts {
				fmt.Fprintf(&body, "apexload_github_sync_jobs{status=%q} %d\n", status, count)
			}
		}
	}
	_, _ = w.Write([]byte(body.String()))
}

func boolMetric(value bool) int {
	if value {
		return 1
	}
	return 0
}

type generatorRegionStats struct {
	Count               int
	AvailableCapacity   int64
	ActiveVUsers        int64
	CapacityStates      map[string]int
	DrainStates         map[string]int
	CompatibilityStates map[string]int
}

type scalingSignals struct {
	QueuedTests          int
	ActiveTests          int
	QueuedVUsers         int
	ActiveVUsers         int
	QueuedProtocolVUsers map[string]int
	ActiveProtocolVUsers map[string]int
}

func scalingSignalStats(tests []*ManagedTest) scalingSignals {
	stats := scalingSignals{
		QueuedProtocolVUsers: make(map[string]int),
		ActiveProtocolVUsers: make(map[string]int),
	}
	for _, test := range tests {
		if test == nil {
			continue
		}
		switch test.Status {
		case "QUEUED", "PENDING", "RESERVED":
			stats.QueuedTests++
			stats.QueuedVUsers += test.TotalVUsers
			addProtocolVUsers(stats.QueuedProtocolVUsers, test.Groups)
		case "INITIALIZING", "RUNNING", "PAUSED":
			stats.ActiveTests++
			stats.ActiveVUsers += test.TotalVUsers
			addProtocolVUsers(stats.ActiveProtocolVUsers, test.Groups)
		}
	}
	return stats
}

func addProtocolVUsers(out map[string]int, groups []TestGroup) {
	for _, group := range groups {
		out[usageKey(group.Protocol, "unknown")] += group.VUsers
	}
}

func writeScalingProtocolVUsers(body *strings.Builder, state string, protocolVUsers map[string]int) {
	protocols := make([]string, 0, len(protocolVUsers))
	for protocol := range protocolVUsers {
		protocols = append(protocols, protocol)
	}
	sort.Strings(protocols)
	for _, protocol := range protocols {
		fmt.Fprintf(body, "apexload_scaling_protocol_vusers{state=%q,protocol=%q} %d\n", state, protocol, protocolVUsers[protocol])
	}
}

func (s *Server) generatorRegionStats(ctx context.Context) map[string]generatorRegionStats {
	inventory := s.systemInventory(ctx)
	if len(inventory.Generators) == 0 {
		return nil
	}
	stats := make(map[string]generatorRegionStats)
	for _, generator := range inventory.Generators {
		region := strings.TrimSpace(generator.Region)
		if region == "" {
			region = "unassigned"
		}
		current := stats[region]
		if current.CapacityStates == nil {
			current.CapacityStates = make(map[string]int)
		}
		if current.DrainStates == nil {
			current.DrainStates = make(map[string]int)
		}
		if current.CompatibilityStates == nil {
			current.CompatibilityStates = make(map[string]int)
		}
		current.Count++
		current.AvailableCapacity += int64(generator.AvailableCapacity)
		current.ActiveVUsers += int64(generator.ActiveVUsers)
		current.CapacityStates[usageKey(generator.CapacityState, "unknown")]++
		current.DrainStates[usageKey(generator.DrainState, "active")]++
		current.CompatibilityStates[usageKey(generator.CompatibilityState, "compatible")]++
		stats[region] = current
	}
	return stats
}

func writeLabeledCounts(body *strings.Builder, metricName string, labelA string, valueA string, labelB string, counts map[string]int) {
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintf(body, "%s{%s=%q,%s=%q} %d\n", metricName, labelA, valueA, labelB, key, counts[key])
	}
}

func (s *Server) handleGetSystemSettings(w http.ResponseWriter, r *http.Request) {
	settings := s.runner.GetSystemSettings()
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) handleUpdateSystemSettings(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	var settings SystemSettings
	if err := json.Unmarshal(body, &settings); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	// A PUT that omits the prometheus section must not wipe it — inherit the
	// currently-stored value. When the section IS present, settings.Prometheus
	// carries the admin's explicit choice (including an explicit disable), which
	// the faithful normalizer in UpdateSystemSettings now preserves.
	if !rawJSONHasKey(string(body), "prometheus") {
		settings.Prometheus = s.runner.GetSystemSettings().Prometheus
	}
	if !rawJSONHasKey(string(body), "client_app") {
		settings.ClientApp = s.runner.GetSystemSettings().ClientApp
	}
	if !rawJSONHasKey(string(body), "client_downloads") {
		settings.ClientDownloads = s.runner.GetSystemSettings().ClientDownloads
	}

	updated, err := s.runner.UpdateSystemSettings(settings)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "system.settings.update", "success", "system_settings", "global", "", map[string]any{"auth_mode": updated.Auth.Mode})

	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	s.hub.serveWS(w, r, "", strings.TrimSpace(r.URL.Query().Get("project_id")))
}

func (s *Server) handleTestStream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	_, ok := s.runner.GetTest(id)
	if !ok {
		writeError(w, http.StatusNotFound, "test not found")
		return
	}
	s.hub.serveWS(w, r, id, "")
}

func validateScript(script *dsl.Script) []string {
	return dsl.ValidateScriptIssues(script)
}

type scriptValidationResponse struct {
	ScriptID                    string                                              `json:"script_id"`
	Valid                       bool                                                `json:"valid"`
	Issues                      []string                                            `json:"issues"`
	Warnings                    []string                                            `json:"warnings,omitempty"`
	Preflight                   *ReplayPreflight                                    `json:"preflight,omitempty"`
	RuntimeSettingsParity       []enterprisearchive.RuntimeSettingParityEntry       `json:"runtime_settings_parity,omitempty"`
	RuntimeSettingsStatusCounts map[string]int                                      `json:"runtime_settings_status_counts,omitempty"`
	MigrationReadiness          *enterprisearchive.RuntimeSettingMigrationReadiness `json:"migration_readiness,omitempty"`
	DataSourceFit               *DataSourceFitReport                                `json:"data_source_fit,omitempty"`
	AIGuardrails                *AIGuardrailReport                                  `json:"ai_guardrails,omitempty"`
	AuthoringHealth             AuthoringHealth                                     `json:"authoring_health"`
}

func buildScriptValidationResponse(scriptID string, script *dsl.Script) scriptValidationResponse {
	return buildScriptValidationResponseWithVariables(scriptID, script, nil)
}

func buildScriptValidationResponseWithVariables(scriptID string, script *dsl.Script, variables map[string]string) scriptValidationResponse {
	issues := validateScript(script)
	warnings := validationWarnings(script)
	preflight := buildReplayPreflight(script, variables, issues, warnings)
	parity := buildValidationRuntimeSettingsParity(script)
	counts := enterprisearchive.RuntimeSettingStatusCounts(parity)
	readiness := enterprisearchive.BuildRTSMigrationReadiness(parity, validationRuntimeSettingMigrationPolicy(script))
	dataSourceFit := buildDataSourceFitReport(script)
	aiGuardrails := buildAIGuardrailReport(script, len(issues) == 0)

	return scriptValidationResponse{
		ScriptID:                    scriptID,
		Valid:                       len(issues) == 0,
		Issues:                      issues,
		Warnings:                    warnings,
		Preflight:                   preflight,
		RuntimeSettingsParity:       parity,
		RuntimeSettingsStatusCounts: counts,
		MigrationReadiness:          readiness,
		DataSourceFit:               dataSourceFit,
		AIGuardrails:                aiGuardrails,
		AuthoringHealth:             buildAuthoringHealth(issues, warnings, counts, dataSourceFit, aiGuardrails, scriptHasAuthoringProvenance(script)),
	}
}

func scriptHasAuthoringProvenance(script *dsl.Script) bool {
	if script == nil {
		return false
	}
	if strings.TrimSpace(script.GeneratedBy) != "" || strings.TrimSpace(script.GeneratedFrom) != "" {
		return true
	}
	return script.Metadata != nil && script.Metadata.Import != nil
}

func validationWarnings(script *dsl.Script) []string {
	if script == nil {
		return nil
	}

	warnings := make([]string, 0, 2)
	if script.Protocol == "browser" {
		engine := strings.TrimSpace(script.Config.Browser.Engine)
		if deviceProfile := strings.TrimSpace(script.Config.Browser.DeviceProfile); deviceProfile != "" {
			if profile, ok := browserprotocol.LookupDevice(deviceProfile); ok && profile.BrowserFamily != "" && engine == "" {
				engine = profile.BrowserFamily
			}
		}
		if warning := browserprotocol.RuntimeInstallWarning(engine); warning != "" {
			warnings = append(warnings, warning)
		}
	}

	if needsRecordedResourceMetadataWarning(script) {
		warnings = append(warnings, recordedResourceMetadataWarning())
	}

	return warnings
}

func needsRecordedResourceMetadataWarning(script *dsl.Script) bool {
	if script == nil || script.Protocol != "browser" {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(script.Config.Browser.DownloadResources), "recorded") {
		return false
	}
	return !hasRecordedResourceMetadata(script)
}

func hasRecordedResourceMetadata(script *dsl.Script) bool {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil || script.Metadata.Import.Preserved == nil {
		return false
	}
	rawAvailable, ok := script.Metadata.Import.Preserved["recorded_resource_urls_available"]
	if !ok {
		return false
	}
	available, ok := rawAvailable.(bool)
	return ok && available
}

func recordedResourceMetadataWarning() string {
	return "Browser replay with download_resources=recorded requires preserved recorded-resource metadata. Manually authored scripts or imports without captured resource URLs cannot enforce recorded-only asset loading yet."
}

func buildValidationRuntimeSettingsParity(script *dsl.Script) []enterprisearchive.RuntimeSettingParityEntry {
	if script == nil {
		return nil
	}

	preserved := runtimeSettingsParityValue(validationImportPreservedValue(script, "runtime_settings_parity"))
	derived := enterprisearchive.BuildRTSParityReportFromScript(script)
	if len(derived) == 0 {
		return preserved
	}
	if len(preserved) == 0 {
		return derived
	}

	preservedByID := make(map[string]enterprisearchive.RuntimeSettingParityEntry, len(preserved))
	for _, entry := range preserved {
		preservedByID[entry.ID] = entry
	}

	merged := make([]enterprisearchive.RuntimeSettingParityEntry, 0, len(derived))
	for _, entry := range derived {
		if preservedEntry, ok := preservedByID[entry.ID]; ok && len(entry.RuntimeFiles) == 0 {
			entry.RuntimeFiles = append([]string(nil), preservedEntry.RuntimeFiles...)
		}
		merged = append(merged, entry)
	}

	return merged
}

func validationRuntimeSettingMigrationPolicy(script *dsl.Script) enterprisearchive.RuntimeSettingMigrationPolicy {
	readiness := enterpriseArchiveMigrationReadinessValue(validationImportPreservedValue(script, "runtime_settings_migration_readiness"))
	if readiness != nil && len(readiness.Policy.BlockStatuses) > 0 {
		return readiness.Policy
	}
	return enterprisearchive.DefaultRuntimeSettingMigrationPolicy()
}

func validationImportPreservedValue(script *dsl.Script, key string) any {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil || script.Metadata.Import.Preserved == nil {
		return nil
	}
	return script.Metadata.Import.Preserved[key]
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// decodeJSONBody decodes the JSON request body into dst. On failure it writes a
// 400 "invalid request body" response and returns false, signalling the caller
// to stop (the response has already been written). On success it returns true.
// It does not buffer the body — use decodeRequestBody when the body must be read
// again after decoding (e.g. for project-scope resolution).
func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return false
	}
	return true
}

// queryFormat returns the normalized (trimmed, lower-cased) "format" query
// parameter — the conventional selector for response encodings (e.g. "csv",
// "json"). It mirrors the strings.ToLower(strings.TrimSpace(...)) idiom that was
// repeated across the export handlers.
func queryFormat(r *http.Request) string {
	return strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
}

// wantsCSV reports whether the request asked for a CSV-encoded response via
// ?format=csv (case-insensitive, whitespace-tolerant).
func wantsCSV(r *http.Request) bool {
	return queryFormat(r) == "csv"
}

func writeError(w http.ResponseWriter, status int, msg string) {
	// Never leak internal error detail (DB errors, file paths, stack context) to
	// clients on 5xx responses. Log the real message server-side under a
	// correlation ID and return a generic message referencing that ID.
	if status >= http.StatusInternalServerError {
		ref := newErrorReference()
		slog.Error("internal server error", "ref", ref, "status", status, "detail", msg)
		writeJSON(w, status, map[string]string{
			"error": "internal server error",
			"ref":   ref,
		})
		return
	}
	writeJSON(w, status, map[string]string{"error": msg})
}

// newErrorReference returns a short opaque ID correlating a client-visible 5xx
// response with the detailed server-side log entry.
func newErrorReference() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(buf[:])
}

type SLABreachReport struct {
	TestID      string   `json:"test_id"`
	Status      string   `json:"status"`
	SLABreached bool     `json:"sla_breached"`
	Details     []string `json:"details,omitempty"`
}

func (s *Server) handleGetTestSLAStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// First check memory
	test, ok := s.runner.GetTest(id)
	if ok {
		s.writeSLAReport(w, id, test.Status, test.GoalConfig, test.LiveMetrics)
		return
	}

	// Then check archive
	_, ok = s.runner.GetRunArchive(id)
	if !ok {
		writeError(w, http.StatusNotFound, "test not found")
		return
	}

	// Determine SLA status from the archive record if available
	manifest, err := s.runner.GetRunArchiveManifest(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to load archive manifest for SLA: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, SLABreachReport{
		TestID:      id,
		Status:      "COMPLETE",
		SLABreached: manifest.SLABreached,
		Details:     manifest.SLADetails,
	})
}

func (s *Server) writeSLAReport(w http.ResponseWriter, id, status string, cfg *GoalConfig, live *LiveMetrics) {
	if live == nil || live.GoalStatus == nil {
		writeJSON(w, http.StatusOK, SLABreachReport{
			TestID:      id,
			Status:      status,
			SLABreached: false,
		})
		return
	}

	breach := live.GoalStatus.BreachActive
	var details []string
	if live.GoalStatus.LatencyBreached {
		details = append(details, "P95 latency breached threshold")
	}
	if live.GoalStatus.ErrorBreached {
		details = append(details, "Error percentage breached threshold")
	}
	if live.GoalStatus.ErrorCountBreached {
		details = append(details, "Total error count breached threshold")
	}
	if live.GoalStatus.ServerProcessingBreached {
		details = append(details, "Server processing time breached threshold")
	}
	if live.GoalStatus.TransferBreached {
		details = append(details, "Transfer time breached threshold")
	}

	writeJSON(w, http.StatusOK, SLABreachReport{
		TestID:      id,
		Status:      status,
		SLABreached: breach,
		Details:     details,
	})
}
