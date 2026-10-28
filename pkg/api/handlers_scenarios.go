package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	defaultScheduleHistoryListLimit = 100
	maxScheduleHistoryListLimit     = 250
)

func (s *Server) handleCreateScenario(w http.ResponseWriter, r *http.Request) {
	var req CreateScenarioRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	actorType, actorSubject, actorDisplayName := s.resolveAuditActor(r)
	req.creationActor = testCreationActor{Type: actorType, Subject: actorSubject, DisplayName: actorDisplayName}
	projectID, projectErr := s.scenarioRequestProjectID(req)
	if projectErr != nil {
		writeError(w, http.StatusBadRequest, projectErr.Error())
		return
	}
	fingerprint, fingerprintErr := scenarioCreateReceiptFingerprint(req)
	if fingerprintErr != nil {
		writeError(w, http.StatusBadRequest, fingerprintErr.Error())
		return
	}
	receipt, outcome := s.claimAuthoringCreate(w, r, storage.AuthoringPurposeScenarioCreate, "scenario", projectID, "", fingerprint)
	switch outcome {
	case authoringCreateHandled:
		return
	case authoringCreateReplay:
		s.replayScenarioCreate(w, r, receipt)
		return
	}
	if receipt != nil {
		receipt.absent = s.durableScenarioAbsent
	}
	defer receipt.release()
	domainID, featureID, shapeErr := s.validateScenarioWorkloadShape(req, projectID, "")
	if errors.Is(shapeErr, errUnknownWorkloadShape) {
		writeError(w, http.StatusBadRequest, "unknown workload_shape_id")
		return
	}
	if shapeErr != nil {
		writeError(w, http.StatusServiceUnavailable, "domain feature policy is unavailable")
		return
	}
	if featureID != "" {
		writeDomainFeatureDisabled(w, domainID, featureID)
		return
	}
	var scenario *StoredScenario
	var committed *storage.ScenarioAuthoringSnapshot
	var err error
	if receipt != nil && receipt.adoptsExisting() {
		// A resumed attempt whose scenario committed replays it before the
		// create runs again: a second create would provision the request's
		// runtime credentials again, which the first attempt's pending
		// provisioning journal refuses.
		exists, readErr := s.durableScenarioExists(receipt.resourceID())
		if readErr != nil {
			writeAuthoringIdempotencyError(w, http.StatusServiceUnavailable, "idempotency_unavailable", "the original scenario could not be read; retry later")
			return
		}
		if exists {
			s.replayScenarioCreate(w, r, receipt)
			return
		}
	}
	if receipt != nil {
		// The receipt fixes the scenario ID, so a retry after a lost response
		// finds the scenario this request created instead of making another.
		receipt.markAttempted()
		if receipt.originalOutcome() {
			scenario, committed, err = s.runner.CreateScenarioAuthoringDurableCompletingReceipt(receipt.resourceID(), req, receipt.completion())
			receipt.completedInTransaction = err == nil
		} else {
			scenario, committed, err = s.runner.CreateScenarioAuthoringDurable(receipt.resourceID(), req)
		}
		receipt.fail(err)
		// errScenarioConcurrentChange covers both "the ID already exists" and a
		// concurrent content change during compilation. Only a durable read can
		// tell an earlier attempt's scenario from a create that never happened.
		if errors.Is(err, errScenarioConcurrentChange) && receipt.adoptsExisting() {
			exists, readErr := s.durableScenarioExists(receipt.resourceID())
			if readErr != nil {
				writeAuthoringIdempotencyError(w, http.StatusServiceUnavailable, "idempotency_unavailable", "the original scenario could not be read; retry later")
				return
			}
			if exists {
				s.replayScenarioCreate(w, r, receipt)
				return
			}
		}
	} else {
		scenario, committed, err = s.runner.CreateScenarioAuthoringDurable("", req)
	}
	if err != nil {
		if writeAuthoringReceiptCreateError(w, err) {
			return
		}
		if errors.Is(err, errServiceAccountRuntimeSecretWrite) || errors.Is(err, errUnattributedRuntimeSecretWrite) {
			writeError(w, http.StatusForbidden, err.Error())
			return
		}
		if errors.Is(err, errScenarioConcurrentChange) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		if errors.Is(err, storage.ErrScenarioAuthoringOutcomeUnknown) {
			// The create may have committed; reload so this replica lists it
			// if it did.
			s.runner.refreshScenariosAfterDurableMismatch()
			writeScenarioAuthoringSaveOutcomeUnavailable(w, true)
			return
		}
		if status, ok := runPolicyErrorStatus(err); ok {
			writeError(w, status, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if receipt != nil {
		revision := ""
		if committed != nil {
			revision = committed.ContentRevision
		}
		receipt.complete(revision)
	}
	s.recordAuditEvent(r, "scenario.create", "success", "scenario", scenario.ID, scenario.ProjectID, map[string]any{"name": scenario.Name, "duration": scenario.Duration})
	markAuthoringReceiptVersion(w, receipt)
	response := maskStoredScenarioForEditor(scenario)
	// The created baseline and its strong tag let an editor save next without
	// a separate authoring read. Both come from the snapshot read inside the
	// create transaction, so a later writer cannot pair this body with its tag.
	attachScenarioAuthoringValidator(w, response, scenario, committed)
	writeJSON(w, http.StatusCreated, response)
}

func (s *Server) handleListScenarios(w http.ResponseWriter, r *http.Request) {
	archiveState, valid := archiveStateFilter(w, r)
	if !valid {
		return
	}
	projectID := r.URL.Query().Get("project_id")
	if projectID != "" {
		scenarios := filterArchivedScenarios(s.runner.ListScenariosFiltered(projectID), archiveState)
		if !s.scenarioLibraryAssignments(w, scenarios) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"scenarios": maskStoredScenarios(scenarios), "count": len(scenarios)})
		return
	}
	scenarios := filterArchivedScenarios(s.runner.ListScenarios(), archiveState)
	if !s.scenarioLibraryAssignments(w, scenarios) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"scenarios": maskStoredScenarios(scenarios), "count": len(scenarios)})
}

func (s *Server) handleGetScenario(w http.ResponseWriter, r *http.Request) {
	if views, present := r.URL.Query()["view"]; present {
		s.writeScenarioAuthoringView(w, r, views)
		return
	}
	scenario, ok := s.runner.GetScenario(r.PathValue("id"))
	if !ok {
		// A copy this replica holds that another controller has since saved
		// fails read revalidation; refresh once rather than report it
		// missing. Unknown ids never trigger a refresh.
		if cached, known := s.runner.getScenarioForMutation(r.PathValue("id")); known && cached != nil {
			s.runner.refreshScenariosAfterDurableMismatch()
			if refreshed, found := s.runner.GetScenario(cached.ID); found && normalizeProjectID(refreshed.ProjectID) == normalizeProjectID(cached.ProjectID) {
				scenario, ok = refreshed, true
			}
		}
	}
	if !ok {
		writeError(w, http.StatusNotFound, "scenario not found")
		return
	}
	// An editor that builds its draft from this body can save it conditioned:
	// the validator is attached only when this replica's copy is the durable
	// state, so the tag always describes the body. A stale copy (another
	// controller saved it) is refreshed once, as a conditioned save would.
	var snapshot *storage.ScenarioAuthoringSnapshot
	if reader, supported := s.store.(storage.ScenarioAuthoringStore); s.store != nil && supported {
		if read, readErr := reader.GetScenarioAuthoringSnapshot(scenario.ID); readErr == nil {
			snapshot = read
			if read.Scenario != nil && !read.Scenario.UpdatedAt.Equal(scenario.UpdatedAt) {
				s.runner.refreshScenariosAfterDurableMismatch()
				if refreshed, found := s.runner.GetScenario(scenario.ID); found && normalizeProjectID(refreshed.ProjectID) == normalizeProjectID(scenario.ProjectID) {
					scenario = refreshed
				}
			}
		}
	}
	if !s.scenarioLibraryAssignments(w, []*StoredScenario{scenario}) {
		return
	}
	response := maskStoredScenarioForEditor(scenario)
	if snapshot != nil {
		attachScenarioAuthoringValidator(w, response, scenario, snapshot)
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleUpdateScenario(w http.ResponseWriter, r *http.Request) {
	var req CreateScenarioRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	actorType, actorSubject, actorDisplayName := s.resolveAuditActor(r)
	req.creationActor = testCreationActor{Type: actorType, Subject: actorSubject, DisplayName: actorDisplayName}
	existing, ok := s.runner.getScenarioForMutation(r.PathValue("id"))
	if !ok || existing == nil {
		writeError(w, http.StatusNotFound, "scenario not found")
		return
	}
	admission, admitted := s.admitScenarioSavePrecondition(w, r, existing)
	if !admitted {
		return
	}
	expected, mode := admission.expected, admission.mode
	if expected != nil && !existing.UpdatedAt.Equal(admission.durableUpdatedAt) {
		// The save builder reconciles masked credentials against the cached
		// scenario, so it must be the state the durable snapshot describes. A
		// conditioned save proved the client saw the durable revision, so the
		// cache is refreshed once; an unconditioned save on a stale replica
		// view is refused, as the timestamp-gated save always refused it.
		if mode == scenarioSaveModeConditioned {
			s.runner.refreshScenariosAfterDurableMismatch()
			existing, ok = s.runner.getScenarioForMutation(existing.ID)
		}
		if !ok || existing == nil || !existing.UpdatedAt.Equal(admission.durableUpdatedAt) {
			writeError(w, http.StatusConflict, "scenario changed concurrently; reload before applying changes")
			return
		}
	}
	requestProjectID, err := s.scenarioRequestProjectID(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if requestProjectID != "" && normalizeProjectID(requestProjectID) != normalizeProjectID(existing.ProjectID) {
		writeError(w, http.StatusBadRequest, "scenario scripts must remain in the scenario project")
		return
	}
	domainID, featureID, shapeErr := s.validateScenarioWorkloadShape(req, existing.ProjectID, existing.WorkloadShapeID)
	if errors.Is(shapeErr, errUnknownWorkloadShape) {
		writeError(w, http.StatusBadRequest, "unknown workload_shape_id")
		return
	}
	if shapeErr != nil {
		writeError(w, http.StatusServiceUnavailable, "domain feature policy is unavailable")
		return
	}
	if featureID != "" {
		writeDomainFeatureDisabled(w, domainID, featureID)
		return
	}
	details := map[string]any{"name": req.Name, "duration": req.Duration, "save_mode": mode}
	// An unconditioned save cannot protect a draft the client loaded earlier,
	// so a store-backed compatibility save is audited before it is attempted.
	operationID := ""
	if mode == scenarioSaveModeLegacy && s.store != nil {
		operationID, err = s.beginRequiredAuditOperation(r, "scenario.update", "scenario", existing.ID, existing.ProjectID, details)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "scenario compatibility audit unavailable; no save was attempted")
			return
		}
	}
	var scenario *StoredScenario
	var committed *storage.ScenarioAuthoringSnapshot
	if expected != nil {
		scenario, committed, ok, err = s.runner.UpdateScenarioAuthoringDurable(existing.ID, req, *expected, admission.durableUpdatedAt)
	} else {
		scenario, ok, err = s.runner.UpdateScenario(existing.ID, req)
	}
	unknown := errors.Is(err, storage.ErrScenarioAuthoringOutcomeUnknown)
	if unknown {
		// The cache still describes the pre-save state; reload it so the next
		// save is judged against whatever actually committed, whatever the
		// audit completion below does.
		s.runner.refreshScenariosAfterDurableMismatch()
	}
	if operationID != "" {
		status, outcome := "failure", "not_committed"
		if err == nil && ok {
			status, outcome = "success", "committed"
		} else if unknown {
			outcome = "unknown"
		}
		if auditErr := s.completeRequiredAuditOperation(r, operationID, "scenario.update", status, outcome, "scenario", existing.ID, existing.ProjectID, details); auditErr != nil {
			writeScenarioAuthoringSaveOutcomeUnavailable(w, false)
			return
		}
	}
	if unknown {
		writeScenarioAuthoringSaveOutcomeUnavailable(w, true)
		return
	}
	if err != nil {
		if mode == scenarioSaveModeLegacy && errors.Is(err, storage.ErrScenarioAuthoringConflict) {
			// The client presented no validator, so a revision race is reported as
			// the concurrent change it always was, not as a failed precondition.
			writeError(w, http.StatusConflict, "scenario changed concurrently; reload before applying changes")
			return
		}
		if writeScenarioAuthoringMutationError(w, err) {
			return
		}
		status := http.StatusBadRequest
		if errors.Is(err, errScenarioConcurrentChange) {
			status = http.StatusConflict
		} else if errors.Is(err, errServiceAccountRuntimeSecretWrite) || errors.Is(err, errUnattributedRuntimeSecretWrite) {
			status = http.StatusForbidden
		} else if policyStatus, ok := runPolicyErrorStatus(err); ok {
			status = policyStatus
		}
		writeError(w, status, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "scenario not found")
		return
	}
	if operationID == "" {
		s.recordAuditEvent(r, "scenario.update", "success", "scenario", scenario.ID, scenario.ProjectID, details)
	}
	if !s.scenarioLibraryAssignments(w, []*StoredScenario{scenario}) {
		return
	}
	response := maskStoredScenarioForEditor(scenario)
	if committed != nil {
		// The committed snapshot was read in the write transaction, so the new
		// baseline and its tag describe exactly what this save persisted.
		projection, projectionErr := buildScenarioAuthoringProjection(committed)
		tag, tagErr := scenarioAuthoringETag(committed.ContentRevision)
		if projectionErr == nil && tagErr == nil {
			response.ContentRevision = committed.ContentRevision
			response.AuthoringProjection = projection
			w.Header().Set("ETag", tag)
		}
	}
	w.Header().Set("X-APEX-Scenario-Save-Mode", mode)
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) scenarioRequestProjectID(req CreateScenarioRequest) (string, error) {
	projectID := strings.TrimSpace(req.ProjectID)
	hasExplicitProject := projectID != ""
	if projectID != "" {
		projectID = normalizeProjectID(projectID)
	}
	for _, group := range req.Groups {
		script, ok := s.runner.GetScript(strings.TrimSpace(group.ScriptID))
		if !ok || script == nil {
			return "", fmt.Errorf("script not found: %s", group.ScriptID)
		}
		candidate := normalizeProjectID(script.ProjectID)
		if projectID != "" && candidate != projectID {
			if !hasExplicitProject {
				return "", fmt.Errorf("scenario scripts span multiple projects")
			}
			return "", fmt.Errorf("scenario scripts do not belong to project_id")
		}
		projectID = candidate
	}
	return projectID, nil
}

func (s *Server) handleValidateScenarioLoadModel(w http.ResponseWriter, r *http.Request) {
	var req CreateScenarioRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	projectID, projectErr := s.scenarioRequestProjectID(req)
	if projectErr != nil {
		writeError(w, http.StatusBadRequest, projectErr.Error())
		return
	}
	domainID, featureID, shapeErr := s.validateScenarioWorkloadShape(req, projectID, "")
	if errors.Is(shapeErr, errUnknownWorkloadShape) {
		writeError(w, http.StatusBadRequest, "unknown workload_shape_id")
		return
	}
	if shapeErr != nil {
		writeError(w, http.StatusServiceUnavailable, "domain feature policy is unavailable")
		return
	}
	if featureID != "" {
		writeDomainFeatureDisabled(w, domainID, featureID)
		return
	}
	report, err := s.runner.ValidateScenarioLoadModel(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) handleSizeScenarioFromGoal(w http.ResponseWriter, r *http.Request) {
	var req GoalSizingRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	var report *RunArchiveReport
	baselineID := strings.TrimSpace(req.BaselineTestID)
	if baselineID == "" && goalSizingNeedsBaseline(req) {
		writeError(w, http.StatusBadRequest, "baseline_test_id is required unless every group supplies manual ART")
		return
	}
	if baselineID != "" {
		if _, ok := s.runner.GetTest(baselineID); !ok {
			writeError(w, http.StatusNotFound, "baseline test not found")
			return
		}
		record, ok := s.runner.GetRunArchiveContext(r.Context(), baselineID)
		if !ok {
			writeError(w, http.StatusNotFound, "baseline run archive not found")
			return
		}
		lease, err := acquireArchiveMaterialized(r.Context(), record)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to materialize baseline archive: "+err.Error())
			return
		}
		defer lease.Release()
		report, err = s.archiveReadCaches().cachedRunArchiveReport(record)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to build baseline report: "+err.Error())
			return
		}
	}
	sizing, err := buildGoalSizingReport(report, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Attach load-model validation best-effort: a validation error (e.g. a
	// baseline script no longer in the library) should not hide the sizing math.
	if validation, verr := s.runner.ValidateScenarioLoadModel(sizing.ScenarioDraft); verr == nil {
		sizing.LoadModelValidation = validation
	}
	writeJSON(w, http.StatusOK, sizing)
}

func goalSizingNeedsBaseline(req GoalSizingRequest) bool {
	for _, group := range req.Groups {
		basis := strings.ToLower(strings.TrimSpace(group.ARTBasis))
		if basis != "manual" && !(basis == "" && group.ARTMs != nil) {
			return true
		}
	}
	return false
}

func (s *Server) handleDeleteScenario(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	scenario, ok := s.runner.GetScenario(id)
	if !ok {
		// Ownership is resolved before any condition is parsed, as on PUT.
		writeError(w, http.StatusNotFound, "scenario not found")
		return
	}
	projectID := scenario.ProjectID
	id = scenario.ID
	admission, admitted := s.admitScenarioWriter(w, r, id, projectID)
	if !admitted {
		return
	}
	var guard *storage.ScenarioWriterGuard
	if admission.guarded() {
		guard = &admission.guard
	}
	// Tombstone first: a delete with an unknown outcome then refuses retries of
	// the create that made this scenario, but can never let one recreate it. A
	// definitely refused delete withdraws the tombstone.
	restoreReceipts, err := s.tombstoneAuthoringReceipts("scenario", id)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "scenario delete unavailable; no deletion was attempted")
		return
	}
	deleted, err := s.runner.DeleteScenarioDurable(id, guard)
	if err != nil && !errors.Is(err, storage.ErrScenarioAuthoringOutcomeUnknown) {
		// Only a failed commit leaves the outcome unknown; any other error is
		// a definite refusal and withdraws the tombstone.
		restoreReceipts()
		if errors.Is(err, storage.ErrScenarioHasResults) {
			writeError(w, http.StatusBadRequest, "scenario not found or has results")
			return
		}
		if !writeScenarioWriterError(w, err) {
			writeError(w, http.StatusInternalServerError, "failed to delete scenario: "+err.Error())
		}
		return
	}
	if err != nil {
		// The delete may have committed: keep its tombstone current.
		s.retombstoneAuthoringReceipts("scenario", id)
		if !writeScenarioWriterError(w, err) {
			writeError(w, http.StatusInternalServerError, "failed to delete scenario: "+err.Error())
		}
		return
	}
	if !deleted {
		restoreReceipts()
		writeError(w, http.StatusBadRequest, "scenario not found or has results")
		return
	}
	s.retombstoneAuthoringReceipts("scenario", id)
	s.recordAuditEvent(r, "scenario.delete", "success", "scenario", id, projectID, nil)
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

func (s *Server) handleScenarioResults(w http.ResponseWriter, r *http.Request) {
	scenarioID := r.PathValue("id")
	_, _, limit, offset, ok := parseTestListOptions(w, r)
	if !ok {
		return
	}
	tests, total := s.runner.listTestSummaryPage(testSummaryListQuery{
		applicationID: r.URL.Query().Get("application_id"),
		releaseID:     r.URL.Query().Get("release_id"),
		scenarioID:    scenarioID,
		limit:         limit,
		offset:        offset,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"tests":  maskManagedTests(tests),
		"count":  len(tests),
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

func (s *Server) handleScenarioSchedulePreview(w http.ResponseWriter, r *http.Request) {
	schedules := s.schedulingDomainService()
	var req CreateScenarioScheduleRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	count := 5
	if raw := strings.TrimSpace(r.URL.Query().Get("count")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			count = n
		}
	}
	// Keep the transport result type explicit so generated OpenAPI schemas remain
	// independent of the concrete scheduling-service adapter implementation.
	var preview *ScenarioSchedulePreview
	preview, err := schedules.PreviewScenarioScheduleRuns(r.PathValue("id"), req, count)
	if err != nil {
		status := http.StatusBadRequest
		msg := err.Error()
		if policyStatus, ok := runPolicyErrorStatus(err); ok {
			status = policyStatus
			if errors.Is(err, errRunPolicyStoreUnavailable) {
				msg = "analysis policy store unavailable"
			}
		} else if err.Error() == "scenario not found: "+r.PathValue("id") {
			status = http.StatusNotFound
		}
		writeError(w, status, msg)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (s *Server) handleCreateScenarioSchedule(w http.ResponseWriter, r *http.Request) {
	schedules := s.schedulingDomainService()
	var req CreateScenarioScheduleRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	_, actorSubject, actorDisplayName := s.resolveAuditActor(r)
	req.OwnerSubject = actorSubject
	req.OwnerDisplayName = actorDisplayName
	req.RBACReviewSource = "api"
	var schedule *storage.ScenarioSchedule
	schedule, err := schedules.CreateScenarioSchedule(r.PathValue("id"), req)
	if err != nil {
		status := http.StatusBadRequest
		msg := err.Error()
		if policyStatus, ok := runPolicyErrorStatus(err); ok {
			status = policyStatus
			if errors.Is(err, errRunPolicyStoreUnavailable) {
				msg = "analysis policy store unavailable"
			}
		} else if err.Error() == "scenario not found: "+r.PathValue("id") {
			status = http.StatusNotFound
		}
		writeError(w, status, msg)
		return
	}
	projectID := schedules.ScenarioProjectID(r.PathValue("id"))
	s.recordAuditEvent(r, "scenario.schedule.create", "success", "scenario_schedule", schedule.ID, projectID, scenarioScheduleAuditDetails(schedule))
	writeJSON(w, http.StatusCreated, schedule)
}

func (s *Server) handleListScenarioSchedules(w http.ResponseWriter, r *http.Request) {
	schedules, ok := s.schedulingDomainService().ListScenarioSchedules(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "scenario not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"schedules": schedules, "count": len(schedules), "clock_changes": scenarioScheduleClockChanges(schedules, time.Now().UTC())})
}

func (s *Server) handleUpdateScenarioSchedule(w http.ResponseWriter, r *http.Request) {
	schedules := s.schedulingDomainService()
	var req CreateScenarioScheduleRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	expectedRevision, err := scheduleExpectedRevision(r, req.ExpectedRevision)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errSchedulePreconditionRequired) {
			status = http.StatusPreconditionRequired
		}
		writeError(w, status, err.Error())
		return
	}
	req.ExpectedRevision = expectedRevision
	_, actorSubject, actorDisplayName := s.resolveAuditActor(r)
	req.OwnerSubject = actorSubject
	req.OwnerDisplayName = actorDisplayName
	req.RBACReviewSource = "api"
	var schedule *storage.ScenarioSchedule
	schedule, ok, err := schedules.UpdateScenarioSchedule(r.PathValue("id"), r.PathValue("schedule_id"), req)
	if err != nil {
		status := http.StatusBadRequest
		msg := err.Error()
		if policyStatus, ok := runPolicyErrorStatus(err); ok {
			status = policyStatus
			if errors.Is(err, errRunPolicyStoreUnavailable) {
				msg = "analysis policy store unavailable"
			}
		} else if errors.Is(err, errScheduleConcurrentChange) {
			status = http.StatusConflict
		}
		writeError(w, status, msg)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "scenario schedule not found")
		return
	}
	projectID := schedules.ScenarioProjectID(r.PathValue("id"))
	s.recordAuditEvent(r, "scenario.schedule.update", "success", "scenario_schedule", schedule.ID, projectID, scenarioScheduleAuditDetails(schedule))
	writeJSON(w, http.StatusOK, schedule)
}

func (s *Server) handleDeleteScenarioSchedule(w http.ResponseWriter, r *http.Request) {
	schedules := s.schedulingDomainService()
	projectID := schedules.ScenarioProjectID(r.PathValue("id"))
	deleted, err := schedules.DeleteScenarioSchedule(r.PathValue("id"), r.PathValue("schedule_id"))
	if err != nil {
		if errors.Is(err, errScheduleHasActiveOccurrence) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		if errors.Is(err, errNotExecutionLeader) {
			writeError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to delete scenario schedule")
		return
	}
	if !deleted {
		writeError(w, http.StatusNotFound, "scenario schedule not found")
		return
	}
	s.recordAuditEvent(r, "scenario.schedule.delete", "success", "scenario_schedule", r.PathValue("schedule_id"), projectID, map[string]any{"scenario_id": r.PathValue("id")})
	writeJSON(w, http.StatusOK, map[string]string{"deleted": r.PathValue("schedule_id")})
}

func (s *Server) handleListScenarioScheduleOccurrences(w http.ResponseWriter, r *http.Request) {
	limit, offset := parseLimitOffsetQuery(r, defaultScheduleHistoryListLimit)
	if limit > maxScheduleHistoryListLimit {
		limit = maxScheduleHistoryListLimit
	}
	occurrences, ok, err := s.schedulingDomainService().ListScenarioScheduleOccurrencesPage(r.PathValue("id"), r.PathValue("schedule_id"), limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "scenario schedule not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"occurrences": occurrences, "count": len(occurrences), "limit": limit, "offset": offset})
}

func (s *Server) handleListScenarioScheduleApprovals(w http.ResponseWriter, r *http.Request) {
	limit, offset := parseLimitOffsetQuery(r, defaultScheduleHistoryListLimit)
	if limit > maxScheduleHistoryListLimit {
		limit = maxScheduleHistoryListLimit
	}
	approvals, ok, err := s.schedulingDomainService().ListScenarioScheduleApprovalsPage(r.PathValue("id"), r.PathValue("schedule_id"), limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "scenario schedule not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"approvals": approvals, "count": len(approvals), "limit": limit, "offset": offset})
}

func (s *Server) handleDeleteScenarioScheduleOccurrence(w http.ResponseWriter, r *http.Request) {
	schedules := s.schedulingDomainService()
	projectID := schedules.ScenarioProjectID(r.PathValue("id"))
	deleted, err := schedules.DeleteScenarioScheduleOccurrence(r.PathValue("id"), r.PathValue("schedule_id"), r.PathValue("occurrence_id"))
	if err != nil {
		if errors.Is(err, errScheduleHasActiveOccurrence) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to delete scenario schedule occurrence")
		return
	}
	if !deleted {
		writeError(w, http.StatusNotFound, "scenario schedule occurrence not found")
		return
	}
	s.recordAuditEvent(r, "scenario.schedule.occurrence.delete", "success", "scenario_schedule_occurrence", r.PathValue("occurrence_id"), projectID, map[string]any{"scenario_id": r.PathValue("id"), "schedule_id": r.PathValue("schedule_id")})
	writeJSON(w, http.StatusOK, map[string]string{"deleted": r.PathValue("occurrence_id")})
}

func (s *Server) handleScenarioScheduleHealth(w http.ResponseWriter, r *http.Request) {
	health := s.schedulingDomainService().Health(time.Now().UTC())
	writeJSON(w, http.StatusOK, health)
}

func (s *Server) handleApproveScenarioScheduleApproval(w http.ResponseWriter, r *http.Request) {
	s.handleResolveScenarioScheduleApproval(w, r, true)
}

func (s *Server) handleRejectScenarioScheduleApproval(w http.ResponseWriter, r *http.Request) {
	s.handleResolveScenarioScheduleApproval(w, r, false)
}

func (s *Server) handleResolveScenarioScheduleApproval(w http.ResponseWriter, r *http.Request, approved bool) {
	schedules := s.schedulingDomainService()
	var req struct {
		Reason string `json:"reason,omitempty"`
	}
	if !decodeOptionalJSONBody(w, r, &req) {
		return
	}
	_, actorSubject, _ := s.resolveAuditActor(r)
	approval, schedule, ok, err := schedules.ResolveScenarioScheduleApproval(r.PathValue("id"), r.PathValue("schedule_id"), r.PathValue("approval_id"), approved, actorSubject, req.Reason)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "scenario schedule approval not found")
		return
	}
	status := "rejected"
	action := "scenario.schedule.approval.reject"
	if approved {
		status = "approved"
		action = "scenario.schedule.approval.approve"
	}
	projectID := ""
	if schedule != nil {
		projectID = schedule.ProjectID
	}
	s.recordAuditEvent(r, action, "success", "scenario_schedule_approval", approval.ID, projectID, map[string]any{"scenario_id": r.PathValue("id"), "schedule_id": r.PathValue("schedule_id"), "decision": status, "reason": req.Reason})
	writeJSON(w, http.StatusOK, map[string]any{"approval": approval, "schedule": schedule})
}

func scenarioScheduleAuditDetails(schedule *storage.ScenarioSchedule) map[string]any {
	if schedule == nil {
		return nil
	}
	details := map[string]any{
		"scenario_id": schedule.ScenarioID,
		"name":        schedule.Name,
		"cron_expr":   schedule.CronExpr,
		"enabled":     schedule.Enabled,
		"owner": map[string]string{
			"subject":      schedule.OwnerSubject,
			"display_name": schedule.OwnerDisplayName,
		},
	}
	if schedule.Governance != nil {
		details["governance"] = schedule.Governance
	}
	if schedule.AnalysisPolicySelections != nil {
		details["analysis_policy_selections"] = schedule.AnalysisPolicySelections
	}
	if len(schedule.BlackoutWindows) > 0 {
		details["blackout_windows"] = schedule.BlackoutWindows
	}
	if len(schedule.MaintenanceWindows) > 0 {
		details["maintenance_windows"] = schedule.MaintenanceWindows
	}
	if len(schedule.AvailabilityWindows) > 0 {
		details["availability_windows"] = schedule.AvailabilityWindows
	}
	return details
}
