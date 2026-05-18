package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/stormrunner/pkg/convert/loadrunner"
	"github.com/jasonczech/stormrunner/pkg/dsl"
	browserprotocol "github.com/jasonczech/stormrunner/pkg/protocol/browser"
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
	s.recordAuditEvent(r, "test.create", "success", "test", test.ID, test.ProjectID, map[string]any{"name": test.Name, "status": test.Status})

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
	test, ok := s.runner.GetTest(id)
	if !ok {
		writeError(w, http.StatusNotFound, "test not found")
		return
	}
	if test.Status == "PENDING" {
		s.recordAuditEvent(r, "test.start", "pending", "test", id, test.ProjectID, map[string]any{"status": test.Status})
		writeJSON(w, http.StatusOK, map[string]any{
			"status":  "pending",
			"test_id": id,
			"message": "test queued until project capacity is available",
		})
		return
	}
	s.recordAuditEvent(r, "test.start", "success", "test", id, test.ProjectID, map[string]any{"status": test.Status})
	writeJSON(w, http.StatusOK, map[string]string{"status": "started", "test_id": id})
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

	if issues := validateScript(script); len(issues) > 0 {
		writeError(w, http.StatusBadRequest, strings.Join(issues, "; "))
		return
	}

	stored := s.runner.StoreScript(script)
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

	writeJSON(w, http.StatusOK, buildScriptValidationResponse("", script))
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
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
	projectID := ""
	name := ""
	if test, ok := s.runner.GetTest(id); ok {
		projectID = test.ProjectID
		name = test.Name
	}
	s.recordAuditEvent(r, "test.vusers.scale", "success", "test", id, projectID, map[string]any{"name": name, "group": req.Group, "target_vusers": req.TargetVUsers})

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
	body.WriteString("# HELP stormrunner_up Whether the Stormrunner controller is healthy.\n")
	body.WriteString("# TYPE stormrunner_up gauge\n")
	body.WriteString("stormrunner_up 1\n")
	body.WriteString("# HELP stormrunner_websocket_clients Number of connected websocket clients.\n")
	body.WriteString("# TYPE stormrunner_websocket_clients gauge\n")
	fmt.Fprintf(&body, "stormrunner_websocket_clients %d\n", s.hub.ClientCount())
	body.WriteString("# HELP stormrunner_test_count Number of tracked tests in memory.\n")
	body.WriteString("# TYPE stormrunner_test_count gauge\n")
	fmt.Fprintf(&body, "stormrunner_test_count %d\n", len(tests))
	body.WriteString("# HELP stormrunner_tests_by_status Number of tracked tests by status.\n")
	body.WriteString("# TYPE stormrunner_tests_by_status gauge\n")
	for status, count := range statusCounts {
		fmt.Fprintf(&body, "stormrunner_tests_by_status{status=%q} %d\n", status, count)
	}
	body.WriteString("# HELP stormrunner_scaling_vusers VUsers by autoscaling demand state.\n")
	body.WriteString("# TYPE stormrunner_scaling_vusers gauge\n")
	fmt.Fprintf(&body, "stormrunner_scaling_vusers{state=%q} %d\n", "queued", scalingStats.QueuedVUsers)
	fmt.Fprintf(&body, "stormrunner_scaling_vusers{state=%q} %d\n", "active", scalingStats.ActiveVUsers)
	body.WriteString("# HELP stormrunner_scaling_tests Tests by autoscaling demand state.\n")
	body.WriteString("# TYPE stormrunner_scaling_tests gauge\n")
	fmt.Fprintf(&body, "stormrunner_scaling_tests{state=%q} %d\n", "queued", scalingStats.QueuedTests)
	fmt.Fprintf(&body, "stormrunner_scaling_tests{state=%q} %d\n", "active", scalingStats.ActiveTests)
	body.WriteString("# HELP stormrunner_scaling_protocol_vusers VUsers by autoscaling demand state and protocol.\n")
	body.WriteString("# TYPE stormrunner_scaling_protocol_vusers gauge\n")
	writeScalingProtocolVUsers(&body, "queued", scalingStats.QueuedProtocolVUsers)
	writeScalingProtocolVUsers(&body, "active", scalingStats.ActiveProtocolVUsers)
	body.WriteString("# HELP stormrunner_capacity_available_generators Available generators from capacity planning.\n")
	body.WriteString("# TYPE stormrunner_capacity_available_generators gauge\n")
	fmt.Fprintf(&body, "stormrunner_capacity_available_generators{scope=%q} %d\n", "configured", availability.ConfiguredAvailableGenerators)
	fmt.Fprintf(&body, "stormrunner_capacity_available_generators{scope=%q} %d\n", "effective", availability.EffectiveAvailableGenerators)
	fmt.Fprintf(&body, "stormrunner_capacity_available_generators{scope=%q} %d\n", "reserved", availability.ReservedGenerators)
	fmt.Fprintf(&body, "stormrunner_capacity_available_generators{scope=%q} %d\n", "shared", availability.SharedAvailableGenerators)
	if availability.LiveAvailableGenerators != nil {
		fmt.Fprintf(&body, "stormrunner_capacity_available_generators{scope=%q} %d\n", "live", *availability.LiveAvailableGenerators)
	}
	if len(regionStats) > 0 {
		body.WriteString("# HELP stormrunner_generator_region_count Number of configured generator inventory entries by region.\n")
		body.WriteString("# TYPE stormrunner_generator_region_count gauge\n")
		body.WriteString("# HELP stormrunner_generator_region_available_capacity Available VUser capacity reported by generators by region.\n")
		body.WriteString("# TYPE stormrunner_generator_region_available_capacity gauge\n")
		body.WriteString("# HELP stormrunner_generator_region_active_vusers Active VUsers reported by generators by region.\n")
		body.WriteString("# TYPE stormrunner_generator_region_active_vusers gauge\n")
		body.WriteString("# HELP stormrunner_generator_capacity_state_count Number of generators by region and capacity state.\n")
		body.WriteString("# TYPE stormrunner_generator_capacity_state_count gauge\n")
		body.WriteString("# HELP stormrunner_generator_drain_state_count Number of generators by region and drain state.\n")
		body.WriteString("# TYPE stormrunner_generator_drain_state_count gauge\n")
		body.WriteString("# HELP stormrunner_generator_compatibility_state_count Number of generators by region and compatibility state.\n")
		body.WriteString("# TYPE stormrunner_generator_compatibility_state_count gauge\n")
		regions := make([]string, 0, len(regionStats))
		for region := range regionStats {
			regions = append(regions, region)
		}
		sort.Strings(regions)
		for _, region := range regions {
			stats := regionStats[region]
			fmt.Fprintf(&body, "stormrunner_generator_region_count{region=%q} %d\n", region, stats.Count)
			fmt.Fprintf(&body, "stormrunner_generator_region_available_capacity{region=%q} %d\n", region, stats.AvailableCapacity)
			fmt.Fprintf(&body, "stormrunner_generator_region_active_vusers{region=%q} %d\n", region, stats.ActiveVUsers)
			writeLabeledCounts(&body, "stormrunner_generator_capacity_state_count", "region", region, "state", stats.CapacityStates)
			writeLabeledCounts(&body, "stormrunner_generator_drain_state_count", "region", region, "state", stats.DrainStates)
			writeLabeledCounts(&body, "stormrunner_generator_compatibility_state_count", "region", region, "state", stats.CompatibilityStates)
		}
	}
	body.WriteString("# HELP stormrunner_max_concurrent_tests Configured maximum concurrent tests.\n")
	body.WriteString("# TYPE stormrunner_max_concurrent_tests gauge\n")
	fmt.Fprintf(&body, "stormrunner_max_concurrent_tests %d\n", settings.General.MaxConcurrentTests)
	body.WriteString("# HELP stormrunner_metrics_interval_seconds Configured metrics publish interval in seconds.\n")
	body.WriteString("# TYPE stormrunner_metrics_interval_seconds gauge\n")
	fmt.Fprintf(&body, "stormrunner_metrics_interval_seconds %d\n", settings.General.MetricsIntervalSeconds)
	_, _ = w.Write([]byte(body.String()))
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
		case "QUEUED", "PENDING":
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
		fmt.Fprintf(body, "stormrunner_scaling_protocol_vusers{state=%q,protocol=%q} %d\n", state, protocol, protocolVUsers[protocol])
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
	ScriptID                    string                                       `json:"script_id"`
	Valid                       bool                                         `json:"valid"`
	Issues                      []string                                     `json:"issues"`
	Warnings                    []string                                     `json:"warnings,omitempty"`
	RuntimeSettingsParity       []loadrunner.RuntimeSettingParityEntry       `json:"runtime_settings_parity,omitempty"`
	RuntimeSettingsStatusCounts map[string]int                               `json:"runtime_settings_status_counts,omitempty"`
	MigrationReadiness          *loadrunner.RuntimeSettingMigrationReadiness `json:"migration_readiness,omitempty"`
	AuthoringHealth             AuthoringHealth                              `json:"authoring_health"`
}

func buildScriptValidationResponse(scriptID string, script *dsl.Script) scriptValidationResponse {
	issues := validateScript(script)
	warnings := validationWarnings(script)
	parity := buildValidationRuntimeSettingsParity(script)
	counts := loadrunner.RuntimeSettingStatusCounts(parity)
	readiness := loadrunner.BuildRTSMigrationReadiness(parity, validationRuntimeSettingMigrationPolicy(script))

	return scriptValidationResponse{
		ScriptID:                    scriptID,
		Valid:                       len(issues) == 0,
		Issues:                      issues,
		Warnings:                    warnings,
		RuntimeSettingsParity:       parity,
		RuntimeSettingsStatusCounts: counts,
		MigrationReadiness:          readiness,
		AuthoringHealth:             buildAuthoringHealth(issues, warnings, counts, scriptHasAuthoringProvenance(script)),
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

		if engine == "webkit" {
			warnings = append(warnings, browserprotocol.WebKitInstallWarning())
		}
	}

	if hasStreamingCompatibilityOnlySettings(script) {
		warnings = append(warnings, streamingCompatibilityWarning(script))
	}

	if needsRecordedResourceMetadataWarning(script) {
		warnings = append(warnings, recordedResourceMetadataWarning())
	}

	return warnings
}

func hasStreamingCompatibilityOnlySettings(script *dsl.Script) bool {
	if script == nil {
		return false
	}
	streaming := script.Config.WebRuntime.Streaming
	return strings.TrimSpace(streaming.Mode) != "" || streaming.BufferSizeSeconds > 0 || streaming.FixedBitrateKbps > 0 || strings.TrimSpace(streaming.BandwidthEstimation) != ""
}

func streamingCompatibilityWarning(script *dsl.Script) string {
	message := "LoadRunner-style streaming and media runtime settings are preserved for migration parity, but Stormrunner does not execute media playback semantics yet."
	if script == nil {
		return message
	}
	if details := streamingCompatibilityDetails(script.Config.WebRuntime.Streaming); details != "" {
		return message + " Preserved settings: " + details + "."
	}
	return message
}

func streamingCompatibilityDetails(streaming dsl.StreamingConfig) string {
	details := make([]string, 0, 4)
	if mode := strings.TrimSpace(streaming.Mode); mode != "" {
		details = append(details, "mode="+mode)
	}
	if streaming.BufferSizeSeconds > 0 {
		details = append(details, fmt.Sprintf("buffer=%ds", streaming.BufferSizeSeconds))
	}
	if streaming.FixedBitrateKbps > 0 {
		details = append(details, fmt.Sprintf("fixed_bitrate=%dkbps", streaming.FixedBitrateKbps))
	}
	if bandwidth := strings.TrimSpace(streaming.BandwidthEstimation); bandwidth != "" {
		details = append(details, "bandwidth_estimation="+bandwidth)
	}
	return strings.Join(details, ", ")
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

func buildValidationRuntimeSettingsParity(script *dsl.Script) []loadrunner.RuntimeSettingParityEntry {
	if script == nil {
		return nil
	}

	preserved := runtimeSettingsParityValue(validationImportPreservedValue(script, "runtime_settings_parity"))
	derived := loadrunner.BuildRTSParityReportFromScript(script)
	if len(derived) == 0 {
		return preserved
	}
	if len(preserved) == 0 {
		return derived
	}

	preservedByID := make(map[string]loadrunner.RuntimeSettingParityEntry, len(preserved))
	for _, entry := range preserved {
		preservedByID[entry.ID] = entry
	}

	merged := make([]loadrunner.RuntimeSettingParityEntry, 0, len(derived))
	for _, entry := range derived {
		if preservedEntry, ok := preservedByID[entry.ID]; ok && len(entry.RuntimeFiles) == 0 {
			entry.RuntimeFiles = append([]string(nil), preservedEntry.RuntimeFiles...)
		}
		merged = append(merged, entry)
	}

	return merged
}

func validationRuntimeSettingMigrationPolicy(script *dsl.Script) loadrunner.RuntimeSettingMigrationPolicy {
	readiness := loadRunnerMigrationReadinessValue(validationImportPreservedValue(script, "runtime_settings_migration_readiness"))
	if readiness != nil && len(readiness.Policy.BlockStatuses) > 0 {
		return readiness.Policy
	}
	return loadrunner.DefaultRuntimeSettingMigrationPolicy()
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

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
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
