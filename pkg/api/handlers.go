package api

import "github.com/jasonczech/apexload/pkg/internal/importreview"

import (
	"crypto/rand"
	"database/sql"
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
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/runcontract"
	"github.com/jasonczech/apexload/pkg/scrub"
	"github.com/jasonczech/apexload/pkg/storage"
	"github.com/jasonczech/apexload/pkg/telemetry"
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
	actorType, actorSubject, actorDisplayName := s.resolveAuditActor(r)
	req.creationActor = testCreationActor{Type: actorType, Subject: actorSubject, DisplayName: actorDisplayName}

	// An Idempotency-Key makes a run create (such as a queued run's revised
	// copy) retry-safe under receipt contract 1: a retry returns the run the
	// first attempt created, as it is now.
	receipt, ok := s.claimTestCreate(w, r, &req)
	if !ok {
		return
	}
	defer receipt.release()
	receipt.markAttempted()
	test, err := s.runner.CreateTest(req)
	if err != nil && errors.Is(err, errTestIdentityCollision) && receipt.adoptsExisting() {
		// An earlier attempt with this key created the run, but its response
		// (and receipt completion) was lost.
		s.replayTestCreate(w, r, receipt)
		return
	}
	if err != nil && receipt != nil {
		if errors.Is(err, errTestIdentityCollision) {
			// A caller-chosen run ID is never adopted: nothing proves the
			// run there is this key's.
			writeAuthoringIdempotencyError(w, http.StatusConflict, "test_identity_in_use", "a run with requested_test_id already exists; read it by that ID before creating another")
			return
		}
		if receipt.derivedID && !s.durableTestAbsent(receipt.resourceID()) {
			// The key fixed this run's ID, so a run there (or an unreadable
			// store) means the failure may have followed the commit: answer
			// as unknown so the client keeps the key, never as a refusal it
			// may rotate, and let its retry resume the receipt at once. A
			// caller-chosen ID proves nothing and keeps the original error.
			receipt.outcomeUnknown = true
			writeAuthoringIdempotencyError(w, http.StatusServiceUnavailable, "test_create_outcome_unknown", "the run create's outcome is unknown; retry with the same Idempotency-Key, which returns the run if it was created")
			return
		}
	}
	if err != nil {
		if errors.Is(err, errServiceAccountRuntimeSecretWrite) {
			writeError(w, http.StatusForbidden, err.Error())
			return
		}
		if errors.Is(err, errTestIDOwnedByAnotherProject) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	receipt.complete("")
	s.recordAuditEvent(r, "test.create", "success", "test", test.ID, test.ProjectID, map[string]any{"name": test.Name, "status": test.Status})
	markAuthoringReceiptVersion(w, receipt)
	writeJSON(w, http.StatusCreated, maskManagedTest(test))
}

// claimTestCreate claims a run create's Idempotency-Key, if any, and fixes
// the run ID to the receipt's. ok is false when a response was written: a
// refusal, or the replay of an earlier attempt's run.
func (s *Server) claimTestCreate(w http.ResponseWriter, r *http.Request, req *CreateTestRequest) (*authoringCreateAttempt, bool) {
	if len(r.Header.Values("Idempotency-Key")) > 0 {
		if version, valid := authoringReceiptVersionRequested(r); valid && version != storage.AuthoringReceiptOutcomeLegacy {
			writeAuthoringIdempotencyError(w, http.StatusBadRequest, "idempotency_receipt_version_unsupported", "run creates support receipt contract 1 only")
			return nil, false
		}
	}
	fingerprint, err := testCreateReceiptFingerprint(*req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return nil, false
	}
	projectID := normalizeProjectID(req.ProjectID)
	if authorized, scoped := authorizedProjectFromRequest(r); scoped {
		projectID = authorized
	}
	attempt, outcome := s.claimAuthoringCreate(w, r, storage.AuthoringPurposeTestCreate, "test", projectID, req.RequestedTestID, fingerprint)
	switch outcome {
	case authoringCreateHandled:
		return nil, false
	case authoringCreateReplay:
		s.replayTestCreate(w, r, attempt)
		return nil, false
	}
	if attempt == nil {
		return nil, true
	}
	attempt.absent = s.durableTestAbsent
	if strings.TrimSpace(req.RequestedTestID) == "" {
		req.RequestedTestID = attempt.resourceID()
	} else if !attempt.derivedID && !attempt.resumed && s.durableTestPresent(attempt.resourceID()) {
		// Run creates never overwrite: whatever this attempt does, the run
		// already at the caller's ID is not its creation.
		attempt.cannotHaveCreated = true
	}
	if attempt.adoptsExisting() {
		if _, found := s.runner.GetTest(attempt.resourceID()); found {
			s.replayTestCreate(w, r, attempt)
			attempt.release()
			return nil, false
		}
	}
	return attempt, true
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
		projectID:     projectID,
		folderID:      folderID,
		applicationID: applicationID,
		releaseID:     releaseID,
		scenarioID:    scenarioID,
		activeOnly:    activeOnly,
		limit:         limit,
		offset:        offset,
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
	case "RESERVED", "QUEUED", "PENDING", "INITIALIZING", "STARTING", "SHAKEOUT", "SHAKEOUT_REVIEW", "RUNNING", "PAUSED", "STOPPING":
		return true
	default:
		return false
	}
}

func sortTestList(tests []*ManagedTest) {
	sort.SliceStable(tests, func(i, j int) bool {
		if tests[i] == nil {
			return false
		}
		if tests[j] == nil {
			return true
		}
		activeI := isActiveTestListStatus(tests[i].Status)
		activeJ := isActiveTestListStatus(tests[j].Status)
		if activeI != activeJ {
			return activeI
		}
		if tests[i].CreatedAt.Equal(tests[j].CreatedAt) {
			return tests[i].ID < tests[j].ID
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
	if !decodeOptionalJSONBodyAllowingUnknownFields(w, r, &req) {
		return
	}
	actorType, actorSubject, actorDisplayName := s.resolveAuditActor(r)
	req.actor = testCreationActor{Type: actorType, Subject: actorSubject, DisplayName: actorDisplayName}
	req.traceContext = telemetry.Detach(r.Context())
	if err := s.runner.StartTestWithOptions(id, req); err != nil {
		if errors.Is(err, errRecoverySpareRequiresPodReservations) {
			if test, ok := s.runner.GetTest(id); ok {
				s.recordAuditEvent(r, "capacity.placement.reject", "blocked", "test", id, test.ProjectID, map[string]any{"reason": "recovery_spare_requires_pod_reservations"})
			}
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": err.Error(), "code": "recovery_spare_requires_pod_reservations",
				"test_id": id, "override_available": false,
			})
			return
		}
		var windowErr *LaunchWindowConflictError
		if errors.As(err, &windowErr) {
			if test, ok := s.runner.GetTest(id); ok {
				s.recordAuditEvent(r, "capacity.launch_window.reject", "blocked", "test", id, test.ProjectID, map[string]any{
					"reason": "launch_window_conflict", "blocking_conflicts": windowErr.Conflicts,
				})
			}
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":              windowErr.Error(),
				"code":               "launch_window_conflict",
				"test_id":            id,
				"blocking_conflicts": windowErr.Conflicts,
				"override_available": false,
			})
			return
		}
		var placementErr *StartPlacementConflictError
		if errors.As(err, &placementErr) {
			if test, ok := s.runner.GetTest(id); ok {
				s.recordAuditEvent(r, "capacity.placement.reject", "blocked", "test", id, test.ProjectID, map[string]any{
					"reason": "generator_placement_conflict", "blocking_conflicts": placementErr.Conflicts,
				})
			}
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":              placementErr.Error(),
				"code":               "generator_placement_conflict",
				"test_id":            id,
				"blocking_conflicts": placementErr.Conflicts,
				"override_available": false,
			})
			return
		}
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
		if errors.Is(err, errStartAdmissionEvidenceStale) || retryableStartSecretRequirement(err) {
			w.Header().Set("Retry-After", "1")
			// The client gets the retry guidance. Only a provider failure behind a
			// secret requirement is hidden from it; log that under the ref. An
			// admission race is a designed retry with nothing more to log.
			ref := errorReferenceFor(w)
			var requirementErr *startSecretRequirementError
			if errors.As(err, &requirementErr) && requirementErr.cause != nil {
				ref = logServerError(w, http.StatusServiceUnavailable, requirementErr.cause, "code", "start_admission_temporarily_unavailable", "test_id", id)
			}
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"error":   err.Error(),
				"code":    "start_admission_temporarily_unavailable",
				"test_id": id,
				"ref":     ref,
			})
			return
		}
		if errors.Is(err, errPreparationInProgress) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "code": "preparation_in_progress", "test_id": id})
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	test, ok := s.waitForStartResponseState(id)
	if !ok {
		writeError(w, http.StatusNotFound, "test not found")
		return
	}
	switch strings.ToUpper(strings.TrimSpace(test.Status)) {
	case "PENDING":
		details := map[string]any{"status": test.Status, "pending_reason": test.PendingReason, "queue_position": test.QueuePosition}
		s.recordAuditEvent(r, "test.start", "pending", "test", id, test.ProjectID, details)
		s.recordAuditEvent(r, "capacity.fair_share.defer", "blocked", "test", id, test.ProjectID, details)
		writeJSON(w, http.StatusOK, map[string]any{
			"status":  "pending",
			"test_id": id,
			"message": "test queued until project capacity is available",
		})
		return
	case "INITIALIZING":
		details := map[string]any{"status": test.Status}
		s.recordAuditEvent(r, "test.start", "accepted", "test", id, test.ProjectID, details)
		writeJSON(w, http.StatusAccepted, map[string]any{
			"status":  "initializing",
			"test_id": id,
			"test":    maskManagedTest(test),
		})
		return
	case "RUNNING", "PAUSED":
		s.recordAuditEvent(r, "test.start", "success", "test", id, test.ProjectID, map[string]any{"status": test.Status})
		writeJSON(w, http.StatusOK, map[string]any{"status": "started", "test_id": id, "test": maskManagedTest(test)})
		return
	case "COMPLETE":
		// Very short and iteration-bounded runs can legitimately finish before the
		// synchronous response window closes. Report the authoritative terminal
		// state instead of pretending the run is still starting.
		s.recordAuditEvent(r, "test.start", "success", "test", id, test.ProjectID, map[string]any{"status": test.Status})
		writeJSON(w, http.StatusOK, map[string]any{"status": "complete", "test_id": id, "test": maskManagedTest(test)})
		return
	case "FAILED":
		s.recordAuditEvent(r, "test.start", "failure", "test", id, test.ProjectID, map[string]any{"status": test.Status})
		writeError(w, http.StatusInternalServerError, firstNonEmpty(test.Error, "test failed during startup"))
		return
	case "ABORTED":
		s.recordAuditEvent(r, "test.start", "failure", "test", id, test.ProjectID, map[string]any{"status": test.Status})
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":   "test was aborted before execution started",
			"status":  "aborted",
			"test_id": id,
			"test":    maskManagedTest(test),
		})
		return
	default:
		s.recordAuditEvent(r, "test.start", "failure", "test", id, test.ProjectID, map[string]any{"status": test.Status})
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":   "test did not enter a runnable state",
			"status":  strings.ToLower(strings.TrimSpace(test.Status)),
			"test_id": id,
			"test":    maskManagedTest(test),
		})
		return
	}
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
	actorType, actorSubject, actorDisplayName := s.resolveAuditActor(r)
	if err := s.runner.StopTestAsActor(id, testCreationActor{Type: actorType, Subject: actorSubject, DisplayName: actorDisplayName}); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	projectID := ""
	status := "stopping"
	if test, ok := s.runner.GetTest(id); ok {
		projectID = test.ProjectID
		status = strings.ToLower(strings.TrimSpace(test.Status))
		if status == "" {
			status = "stopping"
		}
	}
	s.recordAuditEvent(r, "test.stop", "success", "test", id, projectID, nil)
	writeJSON(w, http.StatusOK, map[string]any{"status": status, "test_id": id, "stop_requested": true})
}

func (s *Server) handleAbortTest(w http.ResponseWriter, r *http.Request) {
	if !s.requireExecutionLeader(w) {
		return
	}
	id := r.PathValue("id")
	actorType, actorSubject, actorDisplayName := s.resolveAuditActor(r)
	if err := s.runner.AbortTestAsActor(id, testCreationActor{Type: actorType, Subject: actorSubject, DisplayName: actorDisplayName}); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	projectID := ""
	status := "stopping"
	if test, ok := s.runner.GetTest(id); ok {
		projectID = test.ProjectID
		status = strings.ToLower(strings.TrimSpace(test.Status))
		if status == "" {
			status = "stopping"
		}
	}
	s.recordAuditEvent(r, "test.abort", "success", "test", id, projectID, nil)
	writeJSON(w, http.StatusOK, map[string]any{"status": status, "test_id": id, "abort_requested": true})
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
		if metadata, metadataErr := decodeCIRunMetadata(test.MaterializationKey); metadataErr == nil && metadata.PublishCheck {
			response, responseErr := s.buildCIRunResponse(r.Context(), test)
			if responseErr != nil {
				writeError(w, http.StatusBadGateway, "failed to prepare the required GitHub Check before deletion: "+responseErr.Error())
				return
			}
			if _, syncErr := s.syncCIGitHubCheck(r.Context(), test, metadata, response); syncErr != nil {
				writeError(w, http.StatusBadGateway, "failed to conclude the required GitHub Check before deletion: "+syncErr.Error())
				return
			}
		}
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
	historySince, historyLimit, ok := liveMetricsHistoryWindowFromQuery(w, r.URL.Query())
	if !ok {
		return
	}
	test, ok := s.runner.GetTestMetricsWindow(id, historySince, historyLimit)
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
	writeJSON(w, http.StatusOK, map[string]any{
		"test_id": id,
		"status":  test.Status,
		"metrics": filterLiveMetricsForGroups(test.LiveMetrics, groupFilter),
		// Key the group-metrics history cache by the canonical test.ID, not the
		// request path value which may be a RefKey alias. DeleteTest cleans up the
		// cache by canonical id, so an alias-keyed entry would otherwise never be
		// evicted and leak for the process lifetime.
		"history": filterLiveMetricsHistoryForGroupsCached(test, test.History, groupFilter),
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

// scriptCreateFingerprint is the request identity an Idempotency-Key binds:
// the create request as the client sent it.
type scriptCreateFingerprint struct {
	ProjectID                string     `json:"project_id,omitempty"`
	FolderID                 *string    `json:"folder_id,omitempty"`
	Script                   dsl.Script `json:"script"`
	SourceRecordingSessionID string     `json:"source_recording_session_id,omitempty"`
}

func (s *Server) handleCreateScript(w http.ResponseWriter, r *http.Request) {
	var wrapper struct {
		ProjectID string     `json:"project_id,omitempty"`
		FolderID  *string    `json:"folder_id,omitempty"`
		Script    dsl.Script `json:"script"`
		// SourceRecordingSessionID requests correlation-baseline capture. The
		// server accepts it only when the submitted action graph is an exact
		// deterministic conversion of that recording snapshot.
		SourceRecordingSessionID string `json:"source_recording_session_id,omitempty"`
	}
	if !decodeJSONBody(w, r, &wrapper) {
		return
	}
	script := &wrapper.Script
	if _, batched := scriptBatch(script); batched {
		writeError(w, http.StatusConflict, "Save this JMeter import through the complete batch workflow")
		return
	}
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
	// An Idempotency-Key makes an ordinary create retry-safe. A credential-
	// preserving import that claims a single-use import attestation is fenced
	// by that attestation instead; one without an attestation to claim uses
	// the receipt like any other create.
	var receipt *authoringCreateAttempt
	if !credentialPreservingImportClaimsAttestation(script) {
		attempt, outcome := s.claimAuthoringCreate(w, r, storage.AuthoringPurposeScriptCreate, "script", script.ProjectID, script.ID, authoringReceiptFingerprint{request: scriptCreateFingerprint(wrapper)})
		switch outcome {
		case authoringCreateHandled:
			return
		case authoringCreateReplay:
			s.replayScriptCreate(w, r, attempt)
			return
		}
		receipt = attempt
		if receipt != nil {
			receipt.absent = s.durableScriptAbsent
		}
		defer receipt.release()
		if receipt != nil && strings.TrimSpace(script.ID) == "" {
			script.ID = receipt.resourceID()
		}
	}
	// Resolve duplicate IDs before credential/source work. The durable insert
	// repeats this check atomically after validation to close concurrent creates.
	if strings.TrimSpace(script.ID) != "" && !(isCredentialPreservingImport(script) && importDraftAttestationToken(script) != "") {
		existing, exists, readErr := s.runner.scriptPrimaryIDForCreate(script.ID)
		if readErr != nil && !errors.Is(readErr, sql.ErrNoRows) {
			writeError(w, http.StatusServiceUnavailable, "script create admission unavailable")
			return
		}
		if exists && existing != nil {
			if isCIScriptSnapshot(existing) || normalizeProjectID(existing.ProjectID) != normalizeProjectID(script.ProjectID) {
				writeError(w, http.StatusNotFound, "script not found")
				return
			}
			if receipt.adoptsExisting() && existing.ID == receipt.resourceID() {
				// An earlier attempt with this key created the script but its
				// response (and receipt completion) was lost.
				s.replayScriptCreate(w, r, receipt)
				return
			}
			writeError(w, http.StatusConflict, "script ID already exists; use a conditioned update")
			return
		}
	}
	if err := validateScriptFolderProject(s.store, script); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateJavaVMParameterSafety(script, isCredentialPreservingImport(script)); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var importAttestationClaim *importDraftAttestationClaim
	if isCredentialPreservingImport(script) {
		fingerprint, err := fingerprintImportDraftCredentials(script)
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to validate credential-preserving import draft")
			return
		}
		if fingerprint.HasRawCredentials ||
			importDraftAttestationToken(script) != "" {
			importAttestationClaim, err = s.claimImportDraftAttestation(r, script)
			if err != nil {
				if writeImportDraftBusy(w, err) {
					return
				}
				writeError(w, http.StatusConflict, "credential-preserving import draft is invalid or expired; run the import again")
				return
			}
			defer func() {
				if importAttestationClaim != nil {
					s.releaseImportDraftAttestation(importAttestationClaim)
				}
			}()
			if importAttestationClaim.Script == nil {
				writeError(w, http.StatusConflict, "credential-preserving import draft is invalid or expired; run the import again")
				return
			}
			script = importAttestationClaim.Script
			script, err = replaceUnavailableImportCredentialValues(script)
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			script, err = preparePersistentImportedLibraryScript(
				script,
				nil,
				importAttestationClaim.Profile,
			)
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			if strings.TrimSpace(script.ID) == "" {
				script.ID = protectedImportCreateScriptID(
					importAttestationClaim.Profile,
					importAttestationClaim.Token,
				)
			}
		} else {
			s.discardImportDraftAttestation(r, script)
			script, err = preparePersistentLibraryScript(script, nil)
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
	} else {
		// Always inspect the disclosure-safe projection. A draft that contains
		// only an inline credential (and no literal sentinel) must receive the
		// same guided binding response as a previously redacted import.
		projected := maskScriptSecrets(script)
		if projected == nil {
			writeError(w, http.StatusBadRequest, "failed to project script for secret binding")
			return
		}
		if unresolved := discoverSecretBindingRequirements(projected, 0); len(unresolved) > 0 {
			// Requirements and opaque IDs describe the exact safe representation
			// consumed by the binding endpoint; plaintext never appears in the error.
			writeSecretBindingsRequired(w, unresolved)
			return
		}
		// Save the caller's executable object, not the disclosure projection: a
		// stored script must always be runnable. Non-executable source-review
		// evidence is still scrubbed before the durable write by
		// sanitizePersistentImportReviewSurfaces, which every persistence path
		// applies independently of masking.
		if err := reconcileMaskedScriptSecrets(script, nil); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	if issues := validateStoredExecutionScript(script); len(issues) > 0 {
		writeError(w, http.StatusBadRequest, strings.Join(issues, "; "))
		return
	}
	if scriptRequiresJavaVuserAuthoringApproval(script) {
		writeError(w, http.StatusConflict, "Java Vuser scripts must be saved through authoring-session review and approval")
		return
	}
	baseline, err := s.attestRecordingBaselineCapture(
		script.ProjectID,
		script,
		wrapper.SourceRecordingSessionID,
		importAttestationClaim,
	)
	if err != nil {
		switch {
		case errors.Is(err, errRecordingCaptureSourceNotFound):
			writeError(w, http.StatusNotFound, err.Error())
		default:
			writeError(w, http.StatusConflict, err.Error())
		}
		return
	}

	var stored *StoredScript
	if importAttestationClaim != nil {
		if err := s.assertImportDraftAttestationOwned(importAttestationClaim); err != nil {
			writeError(w, http.StatusConflict, "credential-preserving import draft lease was lost; retry the save")
			return
		}
		stored, _, err = s.runner.CreateImportedScriptDurableReportingReplay(
			script,
			importAttestationClaim.Profile,
		)
	} else {
		receipt.markAttempted()
		if receipt.originalOutcome() {
			stored, err = s.runner.CreateScriptAuthoringDurableCompletingReceipt(script, receipt.completion())
			receipt.completedInTransaction = err == nil
		} else {
			stored, err = s.runner.CreateScriptAuthoringDurable(script)
		}
	}
	if err != nil {
		if writeAuthoringReceiptCreateError(w, err) {
			return
		}
		if errors.Is(err, errScriptAuthoringAlreadyExists) {
			if receipt.adoptsExisting() {
				s.replayScriptCreate(w, r, receipt)
				return
			}
			writeError(w, http.StatusConflict, "script ID already exists; use a conditioned update")
			return
		}
		if errors.Is(err, storage.ErrScriptAuthoringNotFound) {
			writeError(w, http.StatusNotFound, "script not found")
			return
		}
		if errors.Is(err, storage.ErrScriptRefKeyTaken) {
			writeError(w, http.StatusConflict, "script reference key is already in use; create the script without ref_key to get a new one")
			return
		}
		// An unclassified failure may have committed; keep the receipt so a
		// retry adopts the script rather than reusing the key.
		if receipt != nil {
			receipt.outcomeUnknown = true
		}
		writeError(w, http.StatusInternalServerError, "failed to save script: "+err.Error())
		return
	}
	if importAttestationClaim != nil {
		if completeErr := s.completeImportDraftAttestation(importAttestationClaim); completeErr != nil {
			slog.Warn(
				"failed to finalize credential-preserving import draft session",
				"script_id", stored.ID,
				"error", completeErr,
			)
		}
		importAttestationClaim = nil
	}
	if baseline != nil {
		if err := s.persistRecordedBaseline(stored.ID, baseline.source, stored.Script, baseline.observations); err != nil {
			slog.Warn("failed to persist attested recorded baseline", "script_id", stored.ID, "error", err)
		}
	}
	receipt.complete(stored.ContentRevision)
	s.recordAuditEvent(r, "script.create", "success", "script", stored.ID, stored.ProjectID, map[string]any{"name": disclosureSafeScriptName(stored.Name, stored.Script), "folder_id": stored.FolderID})
	markAuthoringReceiptVersion(w, receipt)
	writeJSON(w, http.StatusCreated, viewStoredScript(stored))
}

// credentialPreservingImportClaimsAttestation reports whether the create will
// claim a single-use import attestation, which then fences retries.
func credentialPreservingImportClaimsAttestation(script *dsl.Script) bool {
	if !isCredentialPreservingImport(script) {
		return false
	}
	if importDraftAttestationToken(script) != "" {
		return true
	}
	fingerprint, err := fingerprintImportDraftCredentials(script)
	return err == nil && fingerprint.HasRawCredentials
}

func (s *Server) handleListScripts(w http.ResponseWriter, r *http.Request) {
	archiveState, valid := archiveStateFilter(w, r)
	if !valid {
		return
	}
	projectID := r.URL.Query().Get("project_id")
	var folderID *string
	if r.URL.Query().Has("folder_id") {
		value := r.URL.Query().Get("folder_id")
		folderID = &value
	}
	projectID = s.projectIDForFolderFilter(r, projectID, folderID)
	var scripts []*StoredScript
	if projectID == "" && folderID == nil {
		scripts = s.runner.ListScriptsWithUsage()
	} else {
		scripts = s.runner.ListScriptsFiltered(projectID, folderID)
	}

	scripts = filterArchivedScripts(scripts, archiveState)

	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query != "" {
		needle := strings.ToLower(query)
		filtered := make([]*StoredScript, 0, len(scripts))
		for _, script := range scripts {
			if script == nil {
				continue
			}
			if strings.Contains(strings.ToLower(script.Name), needle) ||
				strings.Contains(strings.ToLower(script.ID), needle) ||
				strings.Contains(strings.ToLower(script.RefKey), needle) {
				filtered = append(filtered, script)
			}
		}
		scripts = filtered
	}
	// Pagination must never inherit Go map iteration order from the in-memory
	// runner. Use a complete, deterministic ordering so repeated replacement
	// searches cannot skip or duplicate records when timestamps are equal.
	sort.Slice(scripts, func(i, j int) bool {
		left, right := scripts[i], scripts[j]
		if left == nil || right == nil {
			return right == nil && left != nil
		}
		leftName, rightName := strings.ToLower(left.Name), strings.ToLower(right.Name)
		if leftName != rightName {
			return leftName < rightName
		}
		if left.Name != right.Name {
			return left.Name < right.Name
		}
		if left.RefKey != right.RefKey {
			return left.RefKey < right.RefKey
		}
		return left.ID < right.ID
	})
	total := len(scripts)
	response := map[string]any{"count": total}
	if r.URL.Query().Has("limit") || r.URL.Query().Has("offset") || query != "" {
		limit, offset := parseLimitOffsetQuery(r, 100)
		start := min(offset, total)
		end := min(start+limit, total)
		scripts = scripts[start:end]
		response["limit"] = limit
		response["offset"] = offset
	}
	if !s.scriptLibraryAssignments(w, scripts) {
		return
	}
	response["scripts"] = maskStoredScripts(scripts)
	writeJSON(w, http.StatusOK, response)
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
	stored, _, admitted := s.loadScriptAuthoringRequest(w, r, id)
	if !admitted {
		return
	}
	if views, present := r.URL.Query()["view"]; present {
		if len(views) != 1 || views[0] != "authoring" {
			writeError(w, http.StatusBadRequest, "unsupported script view")
			return
		}
		if stored.AuthoringProjection == nil {
			writeError(w, http.StatusServiceUnavailable, "durable script content revisions unavailable")
			return
		}
		tag, tagErr := scriptAuthoringETag(stored.ContentRevision)
		if tagErr != nil {
			writeError(w, http.StatusServiceUnavailable, "durable script content revision unavailable")
			return
		}
		w.Header().Set("ETag", tag)
		w.Header().Set("Cache-Control", "private, no-store")
		writeJSON(w, http.StatusOK, stored.AuthoringProjection)
		return
	}
	if !s.scriptLibraryAssignments(w, []*StoredScript{stored}) {
		return
	}
	// This enriched representation includes independently changing usage and
	// joined names, so its authored nonce is not a byte-strong ETag.
	writeJSON(w, http.StatusOK, viewStoredScript(stored))
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

	// Validate the durable revision when the store keeps one, and name it, so
	// the result binds to the exact stored document it describes.
	// Without one (no revision store, a failed read, a moved script) the
	// response names no revision and the editor validates its baseline as a
	// draft instead.
	durable, found, err := s.runner.GetScriptAuthoringDurable(id)
	if err != nil {
		slog.Warn("durable script read for validation failed", "script_id", id, "error", err)
	}
	if err == nil && found && durable != nil && durable.Script != nil &&
		durable.ID == script.ID && durable.ProjectID == script.ProjectID && durable.ContentRevision != "" {
		response := buildScriptValidationResponse(id, durable.Script, s.runner.secretResolverForScript(durable.Script))
		response.ValidatedContentRevision = durable.ContentRevision
		writeJSON(w, http.StatusOK, response)
		return
	}
	writeJSON(w, http.StatusOK, buildScriptValidationResponse(id, script.Script, s.runner.secretResolverForScript(script.Script)))
}

// maxValidationFingerprintBytes bounds the opaque draft fingerprint a client
// may ask the controller to echo.
const maxValidationFingerprintBytes = 128

func validValidationFingerprint(value string) bool {
	if len(value) > maxValidationFingerprintBytes {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < 0x21 || value[i] > 0x7e {
			return false
		}
	}
	return true
}

func (s *Server) handleValidateDraftScript(w http.ResponseWriter, r *http.Request) {
	var wrapper struct {
		ProjectID string            `json:"project_id,omitempty"`
		FolderID  *string           `json:"folder_id,omitempty"`
		Script    dsl.Script        `json:"script"`
		Variables map[string]string `json:"variables,omitempty"`
		// Fingerprint is an opaque client digest of the submitted draft,
		// echoed as validated_fingerprint (at most 128 visible ASCII
		// characters, 0x21-0x7E: no spaces).
		Fingerprint string `json:"fingerprint,omitempty"`
	}
	if !decodeJSONBody(w, r, &wrapper) {
		return
	}
	if !validValidationFingerprint(wrapper.Fingerprint) {
		writeError(w, http.StatusBadRequest, "fingerprint must be at most 128 visible ASCII characters (0x21-0x7E, no spaces)")
		return
	}

	script := &wrapper.Script
	if wrapper.ProjectID != "" {
		script.ProjectID = wrapper.ProjectID
	}
	if wrapper.FolderID != nil {
		script.FolderID = wrapper.FolderID
	}

	response := buildDraftScriptValidationResponseWithVariables(
		"",
		script,
		wrapper.Variables,
		s.runner.secretResolverForScript(script),
	)
	response.ValidatedFingerprint = wrapper.Fingerprint
	writeJSON(w, http.StatusOK, response)
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
	s.recordAuditEvent(r, "script.move", "success", "script", id, stored.ProjectID, map[string]any{"name": disclosureSafeScriptName(stored.Name, stored.Script), "folder_id": req.FolderID})
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "folder_id": req.FolderID})
}

func (s *Server) handleMoveTest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	test, ok := s.runner.GetTestSummary(id)
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
		status := http.StatusBadRequest
		if errors.Is(err, engine.ErrInitialRampInProgress) || errors.Is(err, errTestLoadNotReady) || errors.Is(err, errDistributedTestActivating) {
			status = http.StatusConflict
		}
		writeError(w, status, err.Error())
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

func (s *Server) handlePreviewRuntimeAdjustment(w http.ResponseWriter, r *http.Request) {
	if !s.requireExecutionLeader(w) {
		return
	}
	var req RuntimeAdjustmentRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	preview, err := s.runner.PreviewRuntimeAdjustment(r.PathValue("id"), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (s *Server) handleApplyRuntimeAdjustment(w http.ResponseWriter, r *http.Request) {
	if !s.requireExecutionLeader(w) {
		return
	}
	var req RuntimeAdjustmentRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	before, _ := s.runner.GetTest(r.PathValue("id"))
	preview, err := s.runner.ApplyRuntimeAdjustment(r.PathValue("id"), req)
	if err != nil {
		status := http.StatusBadRequest
		if preview != nil || strings.TrimSpace(req.ExpectedRevision) != "" {
			status = http.StatusConflict
		}
		writeJSON(w, status, map[string]any{"error": err.Error(), "preview": preview})
		return
	}
	test, _ := s.runner.GetTest(r.PathValue("id"))
	projectID, name := "", ""
	if test != nil {
		projectID, name = test.ProjectID, test.Name
	}
	changes := make([]map[string]any, 0, len(req.Groups))
	for _, requested := range req.Groups {
		change := map[string]any{"group": requested.Name, "target_duration": requested.TargetDuration}
		if requested.TargetVUsers != nil {
			change["target_vusers"] = *requested.TargetVUsers
		}
		if requested.TargetTPS != nil {
			change["target_tps"] = *requested.TargetTPS
		}
		if before != nil {
			for _, group := range before.Groups {
				if group.Name == requested.Name {
					change["previous_duration"] = effectiveGroupDuration(group, before.Duration).String()
					change["previous_vusers"] = groupCurrentVUsers(group)
					break
				}
			}
			if goal, ok := before.GoalTargets[requested.Name]; ok {
				change["previous_tps"] = goal.TargetTPS
			}
		}
		changes = append(changes, change)
	}
	s.recordAuditEvent(r, "test.runtime.adjust", "success", "test", r.PathValue("id"), projectID, map[string]any{
		"name": name, "scenario_duration": preview.Impact.ScenarioDuration, "peak_vusers": preview.Impact.PeakVUsers, "changes": changes,
	})
	writeJSON(w, http.StatusOK, preview)
}

func (s *Server) handleSystemVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.versionResponse())
}

// healthCheckSnapshot is the runner-derived part of the health payload, stamped
// with the moment it was built from live runner state. Every probe that reads
// live state republishes it, so under a healthy controller the copy is at most
// one probe interval old; during a durable-write stall it is not republished and
// ages with the stall, which is what healthCheckDurableWriteBypassBudget bounds.
type healthCheckSnapshot struct {
	availability CapacityAvailabilitySnapshot
	testCount    int
	publishedAt  time.Time
}

// healthCheckDurableWriteBypassBudget bounds how long the liveness probe may keep
// answering from a cached snapshot instead of live runner state.
//
// It is derived from the run-driver panic backstop's budget deliberately. The
// bypass exists so the kubelet cannot restart the replica before the backstop has
// finished riding out a stalled store, so it must last exactly as long as the
// backstop can wait and no longer. The backstop caps its own tolerance because
// the in-flight gauge is process-wide (runTestPanicRecoveryDurableWriteExtensions
// and the reasoning above it); an uncapped bypass has the mirror-image failure. A
// durable write that never returns - a black-holed connection with no statement
// timeout, which the runner's context-free writes cannot interrupt - pins the
// gauge above zero forever and never releases tr.mu, so every start, stop, list
// and scale is wedged while the probe answers 200 for the life of the process and
// nothing ever restarts it. Past this budget the handler goes back to the live
// read: it blocks, the probe fails, and the container is restarted onto a fresh
// connection, which is what happened before the bypass existed.
func healthCheckDurableWriteBypassBudget() time.Duration {
	return time.Duration(1+runTestPanicRecoveryDurableWriteExtensions) * runTestPanicRecoveryLockTimeout
}

// healthCheckSnapshotWithoutRunnerLock returns the last published snapshot when
// reading live runner state would block behind a durable write.
//
// /api/v1/system/health is the Kubernetes liveness probe in every shipped
// deployment (periodSeconds 20, timeoutSeconds 3, default failureThreshold 3),
// so the kubelet kills the container roughly 43s after this handler starts
// blocking. Both runner reads in the payload take tr.mu, and the runner
// deliberately holds tr.mu across its durable writes, which take no context and
// can stall for minutes during a managed-database failover. Blocking here would
// therefore restart the pod into the same unreachable store long before the
// run-driver panic backstop's (1+8)*30s extension could ride that failover out -
// the exact crash-loop the extension exists to prevent, and the reason its
// budget is written in minutes.
//
// The bypass is deliberately narrow. With no durable write in flight the
// handler still reads live state, so a mutex a panic genuinely orphaned still
// hangs the probe and still gets the replica restarted. Until the first probe
// has published a snapshot there is nothing to serve and the handler blocks as
// before; a controller reaches that point long before it can stall a write.
//
// It is also bounded. A stalled write recovers, but a black-holed one never
// returns: the gauge stays above zero for the life of the process, tr.mu is never
// released, and an unbounded bypass would answer 200 for a permanently wedged
// controller so the kubelet never restarts it. Once the served copy is older than
// healthCheckDurableWriteBypassBudget - the same budget the panic backstop bounds
// its own use of this gauge with - the handler goes back to the live read and the
// replica is restarted as it was before the bypass existed.
func (s *Server) healthCheckSnapshotWithoutRunnerLock() (healthCheckSnapshot, bool) {
	if s == nil || s.runner == nil {
		return healthCheckSnapshot{}, false
	}
	if s.runner.durableWritesInFlight.Load() <= 0 {
		return healthCheckSnapshot{}, false
	}
	cached := s.healthSnapshot.Load()
	if cached == nil {
		return healthCheckSnapshot{}, false
	}
	budget := healthCheckDurableWriteBypassBudget()
	if budget <= 0 || time.Since(cached.publishedAt) > budget {
		return healthCheckSnapshot{}, false
	}
	return *cached, true
}

func (s *Server) handleHealthCheck(w http.ResponseWriter, r *http.Request) {
	snapshot, cached := s.healthCheckSnapshotWithoutRunnerLock()
	if !cached {
		// Liveness must not refresh durable settings or probe remote generators.
		// Both can outlast the kubelet deadline during a dependency outage. The
		// normal settings/inventory cadences keep these local observations fresh.
		// Retain the runner lock (and its bounded durable-write bypass above) so
		// a genuinely wedged runner still fails liveness.
		s.runner.mu.RLock()
		snapshot = healthCheckSnapshot{
			availability: resolveCapacityAvailabilityWithLiveAt(s.runner.settings, s.runner.cachedLiveAvailableGeneratorCount(time.Now()), time.Now()),
			testCount:    len(s.runner.tests),
			// Stamped after the live reads returned, so the age the bypass bounds is
			// the age of the data being served.
			publishedAt: time.Now(),
		}
		s.runner.mu.RUnlock()
		s.healthSnapshot.Store(&snapshot)
	}
	availability := snapshot.availability
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
		"test_count":      snapshot.testCount,
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

// OpenAPI-Responses: 200
func (s *Server) handleGetSystemSettings(w http.ResponseWriter, r *http.Request) {
	settings := s.systemSettingsDomainService().Get()
	writeSystemSettingsResponse(w, http.StatusOK, settings)
}

// OpenAPI-Responses: 200, 409, 412, 428
func (s *Server) handleUpdateSystemSettings(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	decoded, err := decodeSystemSettingsUpdate(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	ifMatchRevision, err := parseSystemSettingsIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	bodyRevision := strings.TrimSpace(decoded.ExpectedRevision)
	if bodyRevision != "" && !validSystemSettingsRevision(bodyRevision) {
		writeError(w, http.StatusBadRequest, "expected_revision must be the revision returned by GET /api/v1/system/settings")
		return
	}
	if ifMatchRevision != "" && bodyRevision != "" && ifMatchRevision != bodyRevision {
		writeError(w, http.StatusBadRequest, "If-Match and expected_revision must identify the same system-settings revision")
		return
	}
	expectedRevision := ifMatchRevision
	if expectedRevision == "" {
		expectedRevision = bodyRevision
	}
	if expectedRevision == "" {
		writeError(w, http.StatusPreconditionRequired, "system settings update requires If-Match or expected_revision from GET /api/v1/system/settings")
		return
	}
	requiredAuditOperationID := ""
	result, err := s.systemSettingsDomainService().Update(decoded.Settings, body, expectedRevision, func(plan systemSettingsUpdatePlan) error {
		if !plan.PolicyChanged && !plan.NotificationCredentialsChanged {
			return nil
		}
		operationID, auditErr := s.beginRequiredAuditOperation(r, "system.settings.update", "system_settings", "global", "", map[string]any{
			"allow_source_secret_migration":          plan.Candidate.ScriptImports.AllowSourceSecretMigration,
			"source_secret_migration_policy_changed": plan.PolicyChanged,
			"notification_credentials_changed":       plan.NotificationCredentialsChanged,
			"notification_credential_count_before":   plan.PreviousCredentialCount,
			"notification_credential_count_after":    plan.CandidateCredentialCount,
		})
		if auditErr != nil {
			return auditErr
		}
		requiredAuditOperationID = operationID
		return nil
	})
	var preCommitErr *systemSettingsBeforeCommitError
	if errors.As(err, &preCommitErr) {
		writeError(w, http.StatusServiceUnavailable, "system settings audit is unavailable; governed settings were not changed")
		return
	}
	if err != nil {
		if requiredAuditOperationID != "" {
			outcome := "update_failed"
			auditDetails := map[string]any{
				"allow_source_secret_migration":          result.Candidate.ScriptImports.AllowSourceSecretMigration,
				"source_secret_migration_policy_changed": result.PolicyChanged,
				"notification_credentials_changed":       result.NotificationCredentialsChanged,
				"notification_credential_count_before":   result.PreviousCredentialCount,
				"notification_credential_count_after":    result.CandidateCredentialCount,
			}
			if errors.Is(err, errSystemSettingsConflict) {
				outcome = "conflict"
			} else if strings.Contains(err.Error(), "persist settings") {
				outcome = "persistence_error"
				auditDetails["commit_state"] = "ambiguous"
			}
			if auditErr := s.completeRequiredAuditOperation(r, requiredAuditOperationID, "system.settings.update", "failed", outcome, "system_settings", "global", "", auditDetails); auditErr != nil {
				writeError(w, http.StatusServiceUnavailable, "system settings audit outcome could not be persisted")
				return
			}
		}
		if errors.Is(err, errSystemSettingsConflict) {
			status := http.StatusConflict
			if ifMatchRevision != "" {
				status = http.StatusPreconditionFailed
			}
			writeError(w, status, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	auditDetails := map[string]any{
		"auth_mode":                              result.Updated.Auth.Mode,
		"allow_source_secret_migration":          result.Updated.ScriptImports.AllowSourceSecretMigration,
		"source_secret_migration_policy_changed": result.PolicyChanged,
		"notification_credentials_changed":       result.NotificationCredentialsChanged,
		"notification_credential_count_before":   result.PreviousCredentialCount,
		"notification_credential_count_after":    result.CandidateCredentialCount,
	}
	if requiredAuditOperationID != "" {
		if auditErr := s.completeRequiredAuditOperation(r, requiredAuditOperationID, "system.settings.update", "success", "updated", "system_settings", "global", "", auditDetails); auditErr != nil {
			writeError(w, http.StatusServiceUnavailable, "system settings were updated but the governed-change audit outcome could not be persisted; reload settings before retrying")
			return
		}
	} else {
		s.recordAuditEvent(r, "system.settings.update", "success", "system_settings", "global", "", auditDetails)
	}

	writeSystemSettingsResponse(w, http.StatusOK, result.Updated)
}

func writeSystemSettingsResponse(w http.ResponseWriter, status int, settings SystemSettings) {
	revision := systemSettingsRevision(settings)
	w.Header().Set("ETag", systemSettingsETag(revision))
	response := redactedSystemSettingsResponse(settings)
	response["revision"] = revision
	writeJSON(w, status, response)
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
	s.hub.serveTestWS(w, r, id, projectID, s.websocketAuthorizer(r, projectID))
}

var validateScript = importreview.ValidateScript

type scriptValidationResponse = importreview.ScriptValidationResponse

func buildScriptValidationResponse(scriptID string, script *dsl.Script, resolvers ...engine.SecretResolver) scriptValidationResponse {
	return importreview.BuildScriptValidationResponse(scriptID, script, resolvers...)
}

var buildScriptValidationResponseWithVariables = importreview.BuildScriptValidationResponseWithVariables

func buildDraftScriptValidationResponseWithVariables(scriptID string, script *dsl.Script, variables map[string]string, resolvers ...engine.SecretResolver) scriptValidationResponse {
	return importreview.BuildDraftScriptValidationResponseWithVariables(scriptID, script, variables, resolvers...)
}

var buildScriptValidationResponseFromIssues = importreview.BuildScriptValidationResponseFromIssues

var scriptHasAuthoringProvenance = importreview.ScriptHasAuthoringProvenance

var validationWarnings = importreview.ValidationWarnings

var needsRecordedResourceMetadataWarning = importreview.NeedsRecordedResourceMetadataWarning

var hasRecordedResourceMetadata = importreview.HasRecordedResourceMetadata

var recordedResourceMetadataWarning = importreview.RecordedResourceMetadataWarning

var buildValidationRuntimeSettingsParity = importreview.BuildValidationRuntimeSettingsParity

var validationMustPreserveImportedRuntimeFinding = importreview.ValidationMustPreserveImportedRuntimeFinding

var validationRuntimeSettingMigrationPolicy = importreview.ValidationRuntimeSettingMigrationPolicy

var validationImportPreservedValue = importreview.ValidationImportPreservedValue

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
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body exceeds the configured size limit")
			return false
		}
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
	// clients on 5xx responses. Log a credential-scrubbed detail under a
	// correlation ID and return a generic message referencing that ID. Inside
	// the access logger the ID is the request ID, so the reported ref also
	// finds the request's access line.
	if status >= http.StatusInternalServerError {
		writeJSON(w, status, map[string]string{
			"error": "internal server error",
			"ref":   logServerErrorDetail(w, status, msg),
		})
		return
	}
	writeJSON(w, status, map[string]string{"error": scrub.Value(msg, scrub.Policy{})})
}

// logServerError records the internal cause of a 5xx answered with a
// structured body rather than writeError's generic envelope, under the same
// message and ref writeError uses, and returns the ref for the body. The cause
// is credential-scrubbed and never sent to the client.
func logServerError(w http.ResponseWriter, status int, cause error, attrs ...any) string {
	detail := ""
	if cause != nil {
		detail = cause.Error()
	}
	return logServerErrorDetail(w, status, detail, attrs...)
}

// logServerErrorDetail is the one writer of "internal server error" lines,
// shared by writeError and logServerError so both stay searchable alike.
func logServerErrorDetail(w http.ResponseWriter, status int, detail string, attrs ...any) string {
	ref := errorReferenceFor(w)
	args := append([]any{"ref", ref, "status", status}, attrs...)
	if detail != "" {
		args = append(args, "detail", scrub.Value(detail, scrub.Policy{}))
	}
	slog.Error("internal server error", args...)
	return ref
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

type SLABreachReport = runcontract.SLABreachReport

func (s *Server) handleGetTestSLAStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// First check memory
	test, ok := s.runner.GetTest(id)
	if ok {
		if isTerminalTestStatus(test.Status) {
			manifest, err := s.runner.GetRunArchiveManifest(id)
			if err == nil {
				s.writeArchivedSLAReport(w, id, manifest)
				return
			}
			if (test.GoalConfig != nil && len(test.GoalConfig.TransactionRules) > 0) || managedTestHasTransactionResponseTimeSLA(test) {
				writeError(w, http.StatusServiceUnavailable, "terminal transaction SLA evidence is not yet available")
				return
			}
		}
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

	s.writeArchivedSLAReport(w, id, manifest)
}

func (s *Server) writeArchivedSLAReport(w http.ResponseWriter, id string, manifest *RunArchiveManifest) {
	status := "COMPLETE"
	if manifest != nil && strings.TrimSpace(manifest.Status) != "" {
		status = manifest.Status
	}
	writeJSON(w, http.StatusOK, SLABreachReport{
		TestID:                            id,
		Status:                            status,
		SLAEvaluated:                      manifest.SLAEvaluated,
		SLABreached:                       manifest.SLABreached,
		Details:                           manifest.SLADetails,
		TransactionRuleResults:            manifest.TransactionRuleResults,
		TransactionResponseTimeSLAResults: append([]TransactionResponseTimeSLAResult(nil), manifest.TransactionResponseTimeSLAResults...),
	})
}

func (s *Server) writeSLAReport(w http.ResponseWriter, id, status string, cfg *GoalConfig, live *LiveMetrics) {
	if live == nil {
		writeJSON(w, http.StatusOK, SLABreachReport{
			TestID:      id,
			Status:      status,
			SLABreached: false,
		})
		return
	}

	breach := live.GoalStatus != nil && live.GoalStatus.BreachActive
	var details []string
	if live.GoalStatus != nil && live.GoalStatus.LatencyBreached {
		details = append(details, "P95 latency breached threshold")
	}
	if live.GoalStatus != nil && live.GoalStatus.ErrorBreached {
		details = append(details, "Error percentage breached threshold")
	}
	if live.GoalStatus != nil && live.GoalStatus.ErrorCountBreached {
		details = append(details, "Total error count breached threshold")
	}
	if live.GoalStatus != nil && live.GoalStatus.ServerProcessingBreached {
		details = append(details, "Server processing time breached threshold")
	}
	if live.GoalStatus != nil && live.GoalStatus.TransferBreached {
		details = append(details, "Transfer time breached threshold")
	}

	evaluated := hasLiveGoalConfig(cfg)
	if goalConfigOnlyHasTransactionResponseTimeSLA(cfg) {
		evaluated = false
	}
	writeJSON(w, http.StatusOK, SLABreachReport{
		TestID:                            id,
		Status:                            status,
		SLAEvaluated:                      evaluated,
		SLABreached:                       breach,
		Details:                           details,
		TransactionResponseTimeSLAResults: append([]TransactionResponseTimeSLAResult(nil), live.TransactionResponseTimeSLA...),
	})
}

func managedTestHasTransactionResponseTimeSLA(test *ManagedTest) bool {
	if test == nil {
		return false
	}
	if test.GoalConfig != nil && test.GoalConfig.TransactionResponseTimeSLAMs != nil {
		return true
	}
	for _, group := range test.Groups {
		if group.TransactionResponseTimeSLAMs != nil {
			return true
		}
	}
	return false
}

func goalConfigOnlyHasTransactionResponseTimeSLA(config *GoalConfig) bool {
	if config == nil || config.TransactionResponseTimeSLAMs == nil {
		return false
	}
	copy := cloneGoalConfig(config)
	copy.TransactionResponseTimeSLAMs = nil
	return !hasLiveGoalConfig(copy)
}
