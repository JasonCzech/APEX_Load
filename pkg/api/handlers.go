package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/buildinfo"
	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
	"github.com/jasonczech/apexload/pkg/dsl"
	browserprotocol "github.com/jasonczech/apexload/pkg/protocol/browser"
	"github.com/jasonczech/apexload/pkg/scrub"
)

// requireExecutionLeader writes a 503 and returns false when this replica does
// not hold the HA execution lease, so execution-mutating endpoints become
// read-only on non-leader replicas. It is always a pass when election is off.
func (s *Server) requireExecutionLeader(w http.ResponseWriter) bool {
	if s.isExecutionLeader() {
		return true
	}
	writeError(w, http.StatusServiceUnavailable, "this controller replica is not the active execution leader; retry against the active controller")
	return false
}

func (s *Server) handleCreateTest(w http.ResponseWriter, r *http.Request) {
	if !s.requireExecutionLeader(w) {
		return
	}
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

	writeJSON(w, http.StatusCreated, maskManagedTest(test))
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
	summary, activeOnly, limit, offset, ok := parseTestListOptions(w, r)
	if !ok {
		return
	}
	limitExplicit := strings.TrimSpace(r.URL.Query().Get("limit")) != ""
	applicationID := r.URL.Query().Get("application_id")
	releaseID := r.URL.Query().Get("release_id")
	scenarioID := r.URL.Query().Get("scenario_id")
	projectID := r.URL.Query().Get("project_id")
	var folderID *string
	if r.URL.Query().Has("folder_id") {
		value := r.URL.Query().Get("folder_id")
		folderID = &value
	}
	projectID = s.projectIDForFolderFilter(r, projectID, folderID)
	// List responses have never exposed full interval history: maskManagedTests
	// removes it after the runner snapshot is built. Always use the summary page
	// path so a list request does not deep-clone thousands of rich history points
	// per retained run only to discard them immediately. The bounded default also
	// prevents an omitted limit from materializing every retained run at once.
	tests, total := s.runner.listTestSummaryPage(testSummaryListQuery{
		projectID:        projectID,
		folderID:         folderID,
		applicationID:    applicationID,
		releaseID:        releaseID,
		scenarioID:       scenarioID,
		activeOnly:       activeOnly,
		limit:            limit,
		offset:           offset,
		prioritizeActive: limitExplicit,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"tests":   maskManagedTests(tests),
		"count":   len(tests),
		"total":   total,
		"limit":   limit,
		"offset":  offset,
		"summary": summary,
	})
}

const (
	defaultTestListLimit = 200
	maxTestListLimit     = 2000
)

func parseTestListOptions(w http.ResponseWriter, r *http.Request) (summary bool, activeOnly bool, limit int, offset int, ok bool) {
	limit = defaultTestListLimit
	query := r.URL.Query()
	for name, target := range map[string]*bool{"summary": &summary, "active_only": &activeOnly} {
		if raw := query.Get(name); raw != "" {
			parsed, err := strconv.ParseBool(raw)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid "+name)
				return false, false, 0, 0, false
			}
			*target = parsed
		}
	}
	if raw := query.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > maxTestListLimit {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return false, false, 0, 0, false
		}
		limit = parsed
	}
	if raw := query.Get("offset"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			writeError(w, http.StatusBadRequest, "invalid offset")
			return false, false, 0, 0, false
		}
		offset = parsed
	}
	return summary, activeOnly, limit, offset, true
}

func isActiveTestListStatus(status string) bool {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "QUEUED", "PENDING", "INITIALIZING", "SHAKEOUT", "SHAKEOUT_REVIEW", "RUNNING", "PAUSED", "STOPPING":
		return true
	default:
		return false
	}
}

func sortTestList(tests []*ManagedTest, activeFirst bool) {
	sort.SliceStable(tests, func(i, j int) bool {
		if tests[i] == nil {
			return false
		}
		if tests[j] == nil {
			return true
		}
		if activeFirst {
			leftActive := isActiveTestListStatus(tests[i].Status)
			rightActive := isActiveTestListStatus(tests[j].Status)
			if leftActive != rightActive {
				return leftActive
			}
		}
		return tests[i].CreatedAt.After(tests[j].CreatedAt)
	})
}

func (s *Server) handleGetTest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	opts, ok := managedTestMaskOptionsFromQuery(w, r.URL.Query())
	if !ok {
		return
	}
	var test *ManagedTest
	if opts.IncludeHistory {
		test, ok = s.runner.GetTest(id)
	} else {
		test, ok = s.runner.GetTestSummary(id)
	}
	if !ok {
		writeError(w, http.StatusNotFound, "test not found")
		return
	}
	writeJSON(w, http.StatusOK, maskManagedTestWithOptions(test, opts))
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
	writeJSON(w, http.StatusOK, maskTestRunManifest(manifest))
}

func (s *Server) handleStartTest(w http.ResponseWriter, r *http.Request) {
	if !s.requireExecutionLeader(w) {
		return
	}
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
	writeJSON(w, http.StatusOK, map[string]any{"status": "started", "test_id": id, "test": maskManagedTest(test)})
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
	writeJSON(w, http.StatusOK, maskManagedTest(test))
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
	writeJSON(w, http.StatusOK, maskManagedTest(test))
}

func (s *Server) handleStopTest(w http.ResponseWriter, r *http.Request) {
	if !s.requireExecutionLeader(w) {
		return
	}
	id := r.PathValue("id")
	if err := s.runner.StopTest(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	actorType, actorSubject, actorDisplayName := s.resolveAuditActor(r)
	if actorType == "user" || actorType == "service_account" {
		s.runner.AttributeTestStopped(id, actorSubject, actorDisplayName)
	}
	projectID := ""
	if test, ok := s.runner.GetTest(id); ok {
		projectID = test.ProjectID
	}
	s.recordAuditEvent(r, "test.stop", "success", "test", id, projectID, nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopping", "test_id": id})
}

func (s *Server) handleAbortTest(w http.ResponseWriter, r *http.Request) {
	if !s.requireExecutionLeader(w) {
		return
	}
	id := r.PathValue("id")
	if err := s.runner.AbortTest(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	actorType, actorSubject, actorDisplayName := s.resolveAuditActor(r)
	if actorType == "user" || actorType == "service_account" {
		s.runner.AttributeTestStopped(id, actorSubject, actorDisplayName)
	}
	projectID := ""
	if test, ok := s.runner.GetTest(id); ok {
		projectID = test.ProjectID
	}
	s.recordAuditEvent(r, "test.abort", "success", "test", id, projectID, nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "aborted", "test_id": id})
}

func (s *Server) handlePauseTest(w http.ResponseWriter, r *http.Request) {
	if !s.requireExecutionLeader(w) {
		return
	}
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
	if !s.requireExecutionLeader(w) {
		return
	}
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
	if !s.requireExecutionLeader(w) {
		return
	}
	id := r.PathValue("id")
	canonicalID := s.runner.resolveTestID(id)
	projectID := ""
	name := ""
	status := ""
	if test, ok := s.runner.GetTest(id); ok {
		projectID = test.ProjectID
		name = test.Name
		status = test.Status
	}
	deleted, err := s.runner.DeleteTestWithError(canonicalID)
	if err != nil {
		if deleted {
			writeError(w, http.StatusInternalServerError, "test metadata was deleted but physical archive cleanup failed: "+err.Error())
			return
		}
		if errors.Is(err, errNotExecutionLeader) {
			writeError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to delete test: "+err.Error())
		return
	}
	if !deleted {
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
	if groupFilter.Invalid {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("groups must contain at most %d names of at most %d bytes", maxGroupFilterNames, maxGroupFilterNameBytes))
		return
	}
	historySince, historyLimit, ok := liveMetricsHistoryWindowFromQuery(w, r.URL.Query())
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"test_id": id,
		"status":  test.Status,
		"metrics": filterLiveMetricsForGroups(test.LiveMetrics, groupFilter),
		// Key the group-metrics history cache by the canonical test.ID, not the
		// request path value which may be a RefKey alias. DeleteTest cleans up the
		// cache by canonical id, so an alias-keyed entry would otherwise never be
		// evicted and leak for the process lifetime.
		"history": filterLiveMetricsHistoryForGroupsCached(test.ID, selectLiveMetricsHistory(test.History, historySince, historyLimit), groupFilter),
	})
}

func managedTestMaskOptionsFromQuery(w http.ResponseWriter, query url.Values) (managedTestMaskOptions, bool) {
	since, limit, ok := liveMetricsHistoryWindowFromQuery(w, query)
	if !ok {
		return managedTestMaskOptions{}, false
	}
	return managedTestMaskOptions{
		IncludeHistory:   queryIncludes(query, "history"),
		HistorySince:     since,
		HistoryLimit:     limit,
		IncludeSparkline: true,
	}, true
}

func liveMetricsHistoryWindowFromQuery(w http.ResponseWriter, query url.Values) (*time.Time, int, bool) {
	var since *time.Time
	if raw := strings.TrimSpace(query.Get("since")); raw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "since must be an RFC3339 timestamp")
			return nil, 0, false
		}
		since = &parsed
	}
	limit := 0
	if raw := strings.TrimSpace(query.Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer")
			return nil, 0, false
		}
		limit = parsed
	}
	return since, limit, true
}

func queryIncludes(query url.Values, token string) bool {
	token = strings.ToLower(strings.TrimSpace(token))
	for _, raw := range query["include"] {
		for _, part := range strings.Split(raw, ",") {
			if strings.ToLower(strings.TrimSpace(part)) == token {
				return true
			}
		}
	}
	return false
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
	if authorizedProjectID, scoped := authorizedProjectFromRequest(r); scoped {
		if err := bindDraftScriptProject(script, authorizedProjectID); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if err := validateScriptFolderProject(s.store, script); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if unresolved := discoverSecretBindingRequirements(script, 0); len(unresolved) > 0 {
		// Requirements and opaque IDs must describe the exact disclosure-safe
		// script representation the binding endpoint will consume. Project first
		// so a mixed raw/redacted scalar cannot be echoed back or produce stale IDs.
		projected := maskScriptSecrets(script)
		if projected == nil {
			writeError(w, http.StatusBadRequest, "failed to project script for secret binding")
			return
		}
		writeSecretBindingsRequired(w, discoverSecretBindingRequirements(projected, 0))
		return
	}
	if err := reconcileMaskedScriptSecrets(script, nil); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if issues := validateScript(script); len(issues) > 0 {
		writeError(w, http.StatusBadRequest, strings.Join(issues, "; "))
		return
	}

	stored, err := s.runner.StoreScriptDurable(script)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save script: "+err.Error())
		return
	}
	s.captureRecordingBaseline(stored.ID, wrapper.SourceRecordingSessionID)
	s.recordAuditEvent(r, "script.create", "success", "script", stored.ID, stored.ProjectID, map[string]any{"name": stored.Name, "folder_id": stored.FolderID})
	writeJSON(w, http.StatusCreated, maskStoredScript(stored))
}

func (s *Server) handleListScripts(w http.ResponseWriter, r *http.Request) {
	projectID := r.URL.Query().Get("project_id")
	var folderID *string
	if r.URL.Query().Has("folder_id") {
		value := r.URL.Query().Get("folder_id")
		folderID = &value
	}
	projectID = s.projectIDForFolderFilter(r, projectID, folderID)
	if projectID == "" && folderID == nil {
		scripts := s.runner.ListScriptsWithUsage()
		writeJSON(w, http.StatusOK, map[string]any{
			"scripts": maskStoredScripts(scripts),
			"count":   len(scripts),
		})
		return
	}
	scripts := s.runner.ListScriptsFiltered(projectID, folderID)
	writeJSON(w, http.StatusOK, map[string]any{
		"scripts": maskStoredScripts(scripts),
		"count":   len(scripts),
	})
}

func (s *Server) projectIDForFolderFilter(r *http.Request, projectID string, folderID *string) string {
	if strings.TrimSpace(projectID) != "" || folderID == nil || strings.TrimSpace(*folderID) == "" {
		return projectID
	}
	if authorizedProjectID, scoped := authorizedProjectFromRequest(r); scoped {
		return authorizedProjectID
	}
	if s.store != nil {
		if folder, err := s.store.GetFolder(strings.TrimSpace(*folderID)); err == nil && folder != nil {
			return normalizeProjectID(folder.ProjectID)
		}
	}
	return projectID
}

func (s *Server) handleGetScript(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	script, ok := s.runner.GetScript(id)
	if !ok {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	if !requireAuthorizedProjectMatch(w, r, script.ProjectID) {
		return
	}
	writeJSON(w, http.StatusOK, maskStoredScript(script))
}

func (s *Server) handleValidateScript(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	script, ok := s.runner.GetScript(id)
	if !ok {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	if !requireAuthorizedProjectMatch(w, r, script.ProjectID) {
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
	if !requireAuthorizedProjectMatch(w, r, stored.ProjectID) {
		return
	}
	var req struct {
		FolderID *string `json:"folder_id"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	moved, err := s.runner.MoveScriptDurableInProject(id, req.FolderID, authorizedProjectOr(r, stored.ProjectID))
	if err != nil {
		if errors.Is(err, errScriptConcurrentChange) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to move script: "+err.Error())
		return
	}
	if !moved {
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
	moved, err := s.runner.MoveTestDurable(id, req.FolderID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to move test: "+err.Error())
		return
	}
	if !moved {
		writeError(w, http.StatusNotFound, "test not found")
		return
	}
	s.recordAuditEvent(r, "test.move", "success", "test", id, test.ProjectID, map[string]any{"name": test.Name, "folder_id": req.FolderID})
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "folder_id": req.FolderID})
}

func (s *Server) handleScaleVUsers(w http.ResponseWriter, r *http.Request) {
	if !s.requireExecutionLeader(w) {
		return
	}
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
	if !s.requireExecutionLeader(w) {
		return
	}
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
	if !s.requireExecutionLeader(w) {
		return
	}
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
	availability := resolveCapacityAvailabilityWithLiveAt(settings, s.runner.liveAvailableGeneratorCount(r.Context()), time.Now())
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

func (s *Server) handleGetSystemSettings(w http.ResponseWriter, r *http.Request) {
	settings := s.runner.GetSystemSettings()
	writeJSON(w, http.StatusOK, redactedSystemSettingsResponse(settings))
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
	// A settings PUT is a read-modify-write operation because omitted sections
	// and write-only secrets are preserved. Refresh without throttling first so a
	// stale HA replica cannot overwrite a newer shared snapshot.
	s.runner.refreshSystemSettingsFromStore(true)
	currentSettings, expectedSettingsRevision := s.runner.systemSettingsSnapshot()
	// A PUT that omits the prometheus section must not wipe it — inherit the
	// currently-stored value. When the section IS present, settings.Prometheus
	// carries the admin's explicit choice (including an explicit disable), which
	// the faithful normalizer in UpdateSystemSettings now preserves.
	if !rawJSONHasKey(string(body), "prometheus") {
		settings.Prometheus = currentSettings.Prometheus
	}
	if !rawJSONHasKey(string(body), "client_app") {
		settings.ClientApp = currentSettings.ClientApp
	}
	if !rawJSONHasKey(string(body), "client_downloads") {
		settings.ClientDownloads = currentSettings.ClientDownloads
	}

	settings = preserveWriteOnlySystemSettingsSecrets(settings, currentSettings)
	updated, err := s.runner.updateSystemSettings(settings, expectedSettingsRevision, true)
	if err != nil {
		if errors.Is(err, errSystemSettingsConflict) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "system.settings.update", "success", "system_settings", "global", "", map[string]any{"auth_mode": updated.Auth.Mode})

	writeJSON(w, http.StatusOK, redactedSystemSettingsResponse(updated))
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	s.hub.serveWS(w, r, "", projectID, s.websocketAuthorizer(r, projectID))
}

func (s *Server) handleTestStream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	test, ok := s.runner.GetTest(id)
	if !ok {
		writeError(w, http.StatusNotFound, "test not found")
		return
	}
	projectID := normalizeProjectID(test.ProjectID)
	s.hub.serveWS(w, r, id, "", s.websocketAuthorizer(r, projectID))
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
	safeMessage := scrub.Value(msg, scrub.Policy{})
	// Never leak internal error detail (DB errors, file paths, stack context) to
	// clients on 5xx responses. Log a credential-scrubbed detail under a
	// correlation ID and return a generic message referencing that ID.
	if status >= http.StatusInternalServerError {
		ref := newErrorReference()
		slog.Error("internal server error", "ref", ref, "status", status, "detail", safeMessage)
		writeJSON(w, status, map[string]string{
			"error": "internal server error",
			"ref":   ref,
		})
		return
	}
	writeJSON(w, status, map[string]string{"error": safeMessage})
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
