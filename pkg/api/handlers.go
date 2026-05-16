package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

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
		writeJSON(w, http.StatusOK, map[string]any{
			"status":  "pending",
			"test_id": id,
			"message": "test queued until project capacity is available",
		})
		return
	}
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
	writeJSON(w, http.StatusOK, test)
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
		writeError(w, http.StatusNotFound, "test not found or currently active")
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

	if issues := validateScript(script); len(issues) > 0 {
		writeError(w, http.StatusBadRequest, strings.Join(issues, "; "))
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
	warnings := validationWarnings(script.Script)
	valid := len(issues) == 0

	writeJSON(w, http.StatusOK, map[string]any{
		"script_id": id,
		"valid":     valid,
		"issues":    issues,
		"warnings":  warnings,
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

func (s *Server) handlePrometheusMetrics(w http.ResponseWriter, r *http.Request) {
	settings := s.runner.GetSystemSettings()
	availability := resolveCapacityAvailability(settings)
	tests := s.runner.ListTests()
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
	body.WriteString("# HELP stormrunner_capacity_available_generators Available generators from capacity planning.\n")
	body.WriteString("# TYPE stormrunner_capacity_available_generators gauge\n")
	fmt.Fprintf(&body, "stormrunner_capacity_available_generators{scope=%q} %d\n", "configured", availability.ConfiguredAvailableGenerators)
	fmt.Fprintf(&body, "stormrunner_capacity_available_generators{scope=%q} %d\n", "effective", availability.EffectiveAvailableGenerators)
	if availability.LiveAvailableGenerators != nil {
		fmt.Fprintf(&body, "stormrunner_capacity_available_generators{scope=%q} %d\n", "live", *availability.LiveAvailableGenerators)
	}
	body.WriteString("# HELP stormrunner_max_concurrent_tests Configured maximum concurrent tests.\n")
	body.WriteString("# TYPE stormrunner_max_concurrent_tests gauge\n")
	fmt.Fprintf(&body, "stormrunner_max_concurrent_tests %d\n", settings.General.MaxConcurrentTests)
	body.WriteString("# HELP stormrunner_metrics_interval_seconds Configured metrics publish interval in seconds.\n")
	body.WriteString("# TYPE stormrunner_metrics_interval_seconds gauge\n")
	fmt.Fprintf(&body, "stormrunner_metrics_interval_seconds %d\n", settings.General.MetricsIntervalSeconds)
	_, _ = w.Write([]byte(body.String()))
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

func validationWarnings(script *dsl.Script) []string {
	if script == nil || script.Protocol != "browser" {
		return nil
	}

	engine := strings.TrimSpace(script.Config.Browser.Engine)
	if deviceProfile := strings.TrimSpace(script.Config.Browser.DeviceProfile); deviceProfile != "" {
		if profile, ok := browserprotocol.LookupDevice(deviceProfile); ok && profile.BrowserFamily != "" && engine == "" {
			engine = profile.BrowserFamily
		}
	}

	if engine != "webkit" {
		return nil
	}

	return []string{browserprotocol.WebKitInstallWarning()}
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
