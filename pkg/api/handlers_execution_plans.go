package api

// Execution plans: ordered cross-run orchestration of scenarios — the
// LoadRunner Enterprise test-set/test-instance analog. A plan materializes one
// test run per enabled entry (in Order), either in parallel or sequentially.
// Sequential runs are created eagerly with honest per-entry partial-failure
// reporting; true dependency-gated sequential *execution* (wait for run N before
// starting N+1) is a follow-up tracked under the execution_planning dimension.

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	defaultExecutionPlanListLimit        = 100
	maxExecutionPlanEntries              = 256
	maxExecutionPlanDataPoolsPerEntry    = 64
	maxExecutionPlanRuntimeOverrideBytes = 256 << 10
)

func validExecutionRunMode(mode string) bool {
	return mode == "parallel" || mode == "sequential"
}

func (s *Server) handleCreateExecutionPlan(w http.ResponseWriter, r *http.Request) {
	service := s.executionPlanDomainService()
	if !service.StorageConfigured() {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	fence, err := service.CaptureMutationFence()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "execution leader is unavailable")
		return
	}
	var plan storage.ExecutionPlan
	if !decodeJSONBody(w, r, &plan) {
		return
	}
	if status, msg := service.Validate(&plan); status != 0 {
		writeError(w, status, msg)
		return
	}
	plan.ID = "exec-plan-" + uuid.New().String()
	plan.ProjectID = normalizeProjectID(plan.ProjectID)
	if plan.RunMode == "" {
		plan.RunMode = "parallel"
	}
	plan.Status = "draft"
	if err := service.Create(&plan, fence); err != nil {
		if errors.Is(err, errNotExecutionLeader) {
			writeError(w, http.StatusServiceUnavailable, "execution leader is unavailable")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "execution_plan.create", "success", "execution_plan", plan.ID, plan.ProjectID, map[string]any{
		"name": plan.Name, "entries": len(plan.Entries), "run_mode": plan.RunMode,
	})
	writeJSON(w, http.StatusCreated, plan)
}

func (s *Server) handleListExecutionPlans(w http.ResponseWriter, r *http.Request) {
	service := s.executionPlanDomainService()
	limit, offset := parseLimitOffsetQuery(r, defaultExecutionPlanListLimit)
	if !service.StorageConfigured() {
		writeJSON(w, http.StatusOK, map[string]any{"execution_plans": []*storage.ExecutionPlan{}, "count": 0, "limit": limit, "offset": offset})
		return
	}
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	plans, err := service.List(projectID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if plans == nil {
		plans = []*storage.ExecutionPlan{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"execution_plans": plans, "count": len(plans), "limit": limit, "offset": offset})
}

func (s *Server) handleGetExecutionPlan(w http.ResponseWriter, r *http.Request) {
	if !s.executionPlanDomainService().StorageConfigured() {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	plan, ok := s.loadExecutionPlan(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func (s *Server) handleUpdateExecutionPlan(w http.ResponseWriter, r *http.Request) {
	service := s.executionPlanDomainService()
	if !service.StorageConfigured() {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	fence, err := service.CaptureMutationFence()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "execution leader is unavailable")
		return
	}
	existing, ok := s.loadExecutionPlan(w, r)
	if !ok {
		return
	}
	var req storage.ExecutionPlan
	if !decodeJSONBody(w, r, &req) {
		return
	}
	expectedRevision := req.Revision
	if expectedRevision <= 0 {
		writeError(w, http.StatusBadRequest, "execution plan revision is required and must be positive")
		return
	}
	req.ID = existing.ID
	req.ProjectID = existing.ProjectID
	req.CreatedAt = existing.CreatedAt
	if status, msg := service.Validate(&req); status != 0 {
		writeError(w, status, msg)
		return
	}
	if req.RunMode == "" {
		req.RunMode = existing.RunMode
	}
	if req.Status == "" {
		req.Status = existing.Status
	}
	if err := service.Update(&req, expectedRevision, fence); err != nil {
		if errors.Is(err, errNotExecutionLeader) {
			writeError(w, http.StatusServiceUnavailable, "execution leader is unavailable")
			return
		}
		if errors.Is(err, errExecutionPlanRevisionConflict) {
			writeError(w, http.StatusConflict, "execution plan was modified by another request; reload and retry")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "execution_plan.update", "success", "execution_plan", req.ID, req.ProjectID, map[string]any{"entries": len(req.Entries)})
	writeJSON(w, http.StatusOK, req)
}

func (s *Server) handleDeleteExecutionPlan(w http.ResponseWriter, r *http.Request) {
	service := s.executionPlanDomainService()
	if !service.StorageConfigured() {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	fence, err := service.CaptureMutationFence()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "execution leader is unavailable")
		return
	}
	id := r.PathValue("id")
	result, projectID, err := service.Delete(id, fence)
	if err != nil {
		if errors.Is(err, errNotExecutionLeader) {
			writeError(w, http.StatusServiceUnavailable, "execution leader is unavailable")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	switch result {
	case storage.ExecutionPlanDeleteMissing:
		writeError(w, http.StatusNotFound, "execution plan not found")
		return
	case storage.ExecutionPlanDeleteBlockedByActiveRun:
		writeError(w, http.StatusConflict, "execution plan has a non-terminal run and cannot be deleted")
		return
	case storage.ExecutionPlanDeleteBlockedByMaterializingOccurrence:
		writeError(w, http.StatusConflict, "execution plan has a materializing schedule occurrence and cannot be deleted")
		return
	case storage.ExecutionPlanDeleteDeleted:
	default:
		writeError(w, http.StatusInternalServerError, "execution plan delete returned an unknown result")
		return
	}
	s.recordAuditEvent(r, "execution_plan.delete", "success", "execution_plan", id, projectID, nil)
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

type executionPlanRunResult struct {
	Order      int    `json:"order"`
	ScenarioID string `json:"scenario_id"`
	TestID     string `json:"test_id,omitempty"`
	Status     string `json:"status"` // created | failed | skipped
	Detail     string `json:"detail,omitempty"`
}

// ExecutionPlanMaterializeResult is the outcome of materializing+running a plan,
// shared by the HTTP handler and the schedule sweep.
type ExecutionPlanMaterializeResult struct {
	RunID    string
	Status   string
	RunMode  string
	Created  int
	Failed   int
	Results  []executionPlanRunResult
	Blocked  bool
	Approval *storage.ExecutionPlanApproval
}

// MaterializeAndRunExecutionPlan runs a plan once: sequential plans kick off the
// dependency-gated orchestrator; parallel plans stage one queued test per enabled
// entry and persist an eager run snapshot. Attribution is applied when
// attribSubject is non-empty. This is the single entrypoint reused by the manual
// HTTP handler and the recurring execution-plan scheduler; callers are
// responsible for the execution-leader gate (the HTTP handler and the leader-only
// scheduler sweep both satisfy it).
func (tr *TestRunner) MaterializeAndRunExecutionPlan(plan *storage.ExecutionPlan, attribSubject, attribDisplayName string, actors ...testCreationActor) (*ExecutionPlanMaterializeResult, error) {
	return tr.materializeAndRunExecutionPlan(plan, attribSubject, attribDisplayName, "", actors...)
}

func (tr *TestRunner) materializeAndRunExecutionPlan(plan *storage.ExecutionPlan, attribSubject, attribDisplayName, runNameOverride string, actors ...testCreationActor) (*ExecutionPlanMaterializeResult, error) {
	return tr.materializeAndRunExecutionPlanWithRunID(plan, attribSubject, attribDisplayName, runNameOverride, "", actors...)
}

// materializeAndRunExecutionPlanWithRunID is the idempotent scheduler-facing
// variant. A non-empty runIDOverride is anchored before test side effects and is
// reused on occurrence replay after a controller crash.
func (tr *TestRunner) materializeAndRunExecutionPlanWithRunID(plan *storage.ExecutionPlan, attribSubject, attribDisplayName, runNameOverride, runIDOverride string, actors ...testCreationActor) (*ExecutionPlanMaterializeResult, error) {
	leaseTerm, err := tr.captureExecutionLeaseTerm()
	if err != nil {
		return nil, err
	}
	return tr.materializeAndRunExecutionPlanWithRunIDAndTerm(plan, attribSubject, attribDisplayName, runNameOverride, runIDOverride, leaseTerm, actors...)
}

func (tr *TestRunner) materializeAndRunExecutionPlanWithRunIDAndTerm(plan *storage.ExecutionPlan, attribSubject, attribDisplayName, runNameOverride, runIDOverride string, leaseTerm executionLeaseTerm, actors ...testCreationActor) (*ExecutionPlanMaterializeResult, error) {
	if plan == nil {
		return nil, errors.New("execution plan is required")
	}
	if !tr.executionLeaseTermCurrent(leaseTerm) {
		return nil, errNotExecutionLeader
	}
	runIDOverride = strings.TrimSpace(runIDOverride)
	if runIDOverride == "" {
		// If a former leader committed the approval claim but stopped before the
		// run anchor, resume that exact final run ID. Two concurrent rescuers are
		// still serialized by the create-only run anchor, so this closes the
		// claim/anchor crash window without allowing one approval to fund two runs.
		recoveryRunID, err := tr.executionPlanApprovalRunIDAwaitingAnchor(plan)
		if err != nil {
			return nil, executionPlanPersistenceError("recover execution-plan approval claim", err)
		}
		runIDOverride = recoveryRunID
		if runIDOverride == "" {
			runIDOverride = "exec-plan-run-" + uuid.New().String()
		}
	}
	if runIDOverride != "" && tr.execPlanRunStore != nil {
		existing, err := tr.execPlanRunStore.GetExecutionPlanRun(runIDOverride)
		switch {
		case err == nil && existing != nil:
			if existing.PlanID != plan.ID || normalizeProjectID(existing.ProjectID) != normalizeProjectID(plan.ProjectID) || existing.RunMode != plan.RunMode {
				return nil, fmt.Errorf("execution-plan run id %q is already used by another materialization", runIDOverride)
			}
			tr.handoffParallelExecutionPlanTestsWithTerm(existing, leaseTerm)
			return executionPlanMaterializeResultFromRun(existing), nil
		case err != nil && !errors.Is(err, sql.ErrNoRows):
			return nil, executionPlanPersistenceError("check preassigned execution-plan run id", err)
		}
	}
	runName := strings.TrimSpace(runNameOverride)
	if runName == "" {
		runName = strings.TrimSpace(plan.Name)
	}
	creationActor := executionPlanCreationActor(attribSubject, attribDisplayName, actors...)
	if err := tr.preflightExecutionPlanRuntimeSecretAuthority(plan, runName, runIDOverride, creationActor); err != nil {
		return nil, err
	}
	// Governance gate: a plan whose aggregate peak exceeds the project VUser quota
	// requires a project-admin approval before it can run.
	gateApproval, blocked, err := tr.executionPlanRunGateWithTerm(plan, attribSubject, leaseTerm, runIDOverride)
	if err != nil {
		return nil, err
	}
	if blocked {
		return &ExecutionPlanMaterializeResult{Status: "blocked", RunMode: plan.RunMode, Blocked: true, Approval: gateApproval}, nil
	}
	// Atomically CLAIM the authorizing approval BEFORE any side effects, so two
	// concurrent runs (e.g. a manual run racing the scheduler sweep) cannot both
	// consume one approval and a run can never proceed on an unclaimed approval.
	if gateApproval != nil {
		claimRunID := runIDOverride
		claimed := claimRunID != "" && gateApproval.RunID == claimRunID
		if !claimed {
			var err error
			claimed, err = tr.claimExecutionPlanApprovalWithTerm(gateApproval.ID, claimRunID, time.Now().UTC(), leaseTerm)
			if err != nil {
				return nil, err
			}
			if !claimed {
				// A takeover can race the final claim write from the former
				// leader. Treat an approval already stamped with this exact
				// idempotency key as ours; it remains unusable by every other run.
				fresh, loadErr := tr.execPlanScheduleStore.GetExecutionPlanApproval(gateApproval.ID)
				if loadErr != nil {
					return nil, loadErr
				}
				if fresh != nil && fresh.Status == "approved" && fresh.RunID == runIDOverride {
					gateApproval = fresh
					claimed = true
				}
			}
		}
		if !claimed {
			return &ExecutionPlanMaterializeResult{Status: "blocked", RunMode: plan.RunMode, Blocked: true, Approval: gateApproval}, nil
		}
	}

	var result *ExecutionPlanMaterializeResult
	if plan.RunMode == "sequential" {
		run, err := tr.startExecutionPlanRunWithIDAndTermActor(plan, attribSubject, attribDisplayName, runName, runIDOverride, leaseTerm, creationActor, gateApproval)
		if err != nil {
			return nil, err
		}
		results := make([]executionPlanRunResult, 0, len(run.Entries))
		for _, e := range run.Entries {
			results = append(results, executionPlanRunResult{Order: e.Order, ScenarioID: e.ScenarioID, TestID: e.TestID, Status: e.Status, Detail: e.Detail})
		}
		result = &ExecutionPlanMaterializeResult{RunID: run.ID, Status: run.Status, RunMode: "sequential", Results: results}
		tr.consumeExecutionPlanApprovalWithTerm(gateApproval, result.RunID, leaseTerm)
		return result, nil
	}

	entries := make([]storage.ExecutionPlanEntry, len(plan.Entries))
	copy(entries, plan.Entries)
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Order < entries[j].Order })

	now := time.Now().UTC()
	runID := runIDOverride
	initialEntries := make([]storage.ExecutionPlanRunEntry, 0, len(entries))
	anchorTimeslotUsed := false
	for entryIndex, entry := range entries {
		status := "pending"
		detail := ""
		testID := ""
		effectiveTimeslot := entry.TimeslotID
		if !entry.IsEnabled() {
			status, detail = "skipped", "entry disabled"
		} else {
			testID = executionPlanEntryTestID(runID, entryIndex, entry.Order)
			if trimPtr(effectiveTimeslot) == "" && !anchorTimeslotUsed && trimPtr(plan.TimeslotID) != "" {
				effectiveTimeslot = plan.TimeslotID
				anchorTimeslotUsed = true
			}
		}
		snapshot := executionPlanRunEntrySnapshot(entry, runName, attribSubject, attribDisplayName, effectiveTimeslot, gateApproval, creationActor)
		snapshot.TestID = testID
		snapshot.Status = status
		snapshot.Detail = detail
		snapshot.MaterializationKey = executionPlanEntryMaterializationKey(runID, entryIndex, entry.Order)
		initialEntries = append(initialEntries, snapshot)
	}
	// Anchor the parallel materialization before creating any tests. If storage is
	// unavailable the handler fails without emitting test-created side effects.
	anchor := &storage.ExecutionPlanRun{
		ID: runID, PlanID: plan.ID, ProjectID: normalizeProjectID(plan.ProjectID),
		RunMode: "parallel", Status: "running", Entries: initialEntries,
		CreatedAt: now, UpdatedAt: now,
	}
	createdAnchor, err := tr.createExecutionPlanRunDurableWithTerm(anchor, leaseTerm)
	if err != nil {
		return nil, executionPlanPersistenceError("persist parallel execution-plan run before materialization", err)
	}
	if !createdAnchor {
		existing, err := tr.loadExistingExecutionPlanRun(runID, plan.ID, plan.ProjectID, "parallel")
		if err != nil {
			return nil, err
		}
		tr.handoffParallelExecutionPlanTestsWithTerm(existing, leaseTerm)
		return executionPlanMaterializeResultFromRun(existing), nil
	}
	runRevision := anchor.Revision
	results := make([]executionPlanRunResult, 0, len(entries))
	runEntries := make([]storage.ExecutionPlanRunEntry, 0, len(entries))
	createdTests := make([]*ManagedTest, 0, len(entries))
	created, failed := 0, 0
	planTimeslotUsed := false
	for entryIndex, entry := range entries {
		res := executionPlanRunResult{Order: entry.Order, ScenarioID: entry.ScenarioID}
		testID := executionPlanEntryTestID(runID, entryIndex, entry.Order)
		runEntry := initialEntries[entryIndex]
		if !entry.IsEnabled() {
			res.Status, res.Detail = "skipped", "entry disabled"
			runEntry.TestID, runEntry.Status, runEntry.Detail = "", "skipped", "entry disabled"
			results = append(results, res)
			runEntries = append(runEntries, runEntry)
			continue
		}
		var planTimeslot *string
		if trimPtr(entry.TimeslotID) == "" && !planTimeslotUsed && trimPtr(plan.TimeslotID) != "" {
			planTimeslot = plan.TimeslotID
			planTimeslotUsed = true
		}
		err := tr.validateExecutionPlanApprovalMaterialization(plan.ID, runID, gateApproval)
		var createRequest CreateTestRequest
		if err == nil {
			createRequest, err = buildPlanEntryCreateRequest(runName, plan.ProjectID, planTimeslot, entry, creationActor)
			if err == nil {
				createRequest.MaterializationKey = runEntry.MaterializationKey
				createRequest.ExecutionPlanApproval = gateApproval
			}
		}
		var test *ManagedTest
		if err == nil {
			test, err = tr.createTestWithIDAndTerm(createRequest, false, testID, leaseTerm)
		}
		if err == nil {
			err = tr.validateExecutionPlanApprovalMaterialization(plan.ID, runID, gateApproval)
		}
		if err != nil {
			if errors.Is(err, errNotExecutionLeader) || !tr.executionLeaseTermCurrent(leaseTerm) {
				return nil, errNotExecutionLeader
			}
			if test != nil {
				if failErr := tr.failExecutionPlanStagedTestWithTerm(testID, err.Error(), leaseTerm, test.MaterializationKey); failErr != nil {
					return nil, errors.Join(err, failErr)
				}
			}
			failed++
			res.Status, res.Detail = "failed", err.Error()
			runEntry.Status, runEntry.Detail = "failed", err.Error()
		} else {
			if !tr.executionLeaseTermCurrent(leaseTerm) {
				return nil, errNotExecutionLeader
			}
			created++
			createdTests = append(createdTests, test)
			res.Status, res.TestID = "created", test.ID
			runEntry.Status, runEntry.TestID = "created", test.ID
			if strings.TrimSpace(attribSubject) != "" {
				tr.AttributeTestCreated(test.ID, attribSubject, attribDisplayName)
			}
			tr.AttributeTestExecutionPlanApproval(test.ID, gateApproval)
		}
		results = append(results, res)
		runEntries = append(runEntries, runEntry)
	}

	runStatus := "completed"
	if failed > 0 {
		runStatus = "failed"
	}
	finalRun := &storage.ExecutionPlanRun{
		ID:          runID,
		PlanID:      plan.ID,
		ProjectID:   normalizeProjectID(plan.ProjectID),
		RunMode:     "parallel",
		Status:      runStatus,
		Entries:     runEntries,
		Revision:    runRevision,
		CreatedAt:   now,
		UpdatedAt:   now,
		CompletedAt: &now,
	}
	if err := tr.persistExecutionPlanRunDurableWithTerm(finalRun, leaseTerm); err != nil {
		persistErr := executionPlanPersistenceError("persist completed parallel execution-plan run", err)
		if errors.Is(err, errNotExecutionLeader) || !tr.executionLeaseTermCurrent(leaseTerm) {
			return nil, persistErr
		}
		if errors.Is(err, errExecutionPlanRunCommitUnknown) {
			// A lost commit response is not proof that the final transition failed.
			// Keep the silently staged tests intact and reconcile the exact snapshot;
			// deleting them could corrupt a run the database already accepted.
			tr.enqueueExecutionPlanRunRecordRetryAfterWithTerm(finalRun, func() {
				for _, test := range createdTests {
					tr.broadcastEvent("test_created", test)
				}
				tr.consumeExecutionPlanApprovalWithTerm(gateApproval, finalRun.ID, leaseTerm)
				tr.handoffParallelExecutionPlanTestsWithTerm(finalRun, leaseTerm)
				tr.broadcastExecutionPlanEvent("execution_plan_run_finished", finalRun, nil)
			}, leaseTerm)
			return nil, persistErr
		}
		for _, test := range createdTests {
			if cleanupErr := tr.failExecutionPlanStagedTestWithTerm(test.ID, "parallel execution-plan final transition was not persisted", leaseTerm, test.MaterializationKey); cleanupErr != nil {
				persistErr = errors.Join(persistErr, fmt.Errorf("terminalize uncommitted plan test %s: %w", test.ID, cleanupErr))
			}
		}
		failedAt := time.Now().UTC()
		failedEntries := append([]storage.ExecutionPlanRunEntry(nil), runEntries...)
		for i := range failedEntries {
			if failedEntries[i].Status == "created" {
				failedEntries[i].Status = "failed"
				failedEntries[i].Detail = "materialization rolled back because the final execution-plan transition could not be persisted"
			}
		}
		failedRun := &storage.ExecutionPlanRun{
			ID: runID, PlanID: plan.ID, ProjectID: normalizeProjectID(plan.ProjectID), RunMode: "parallel", Status: "failed",
			Entries: failedEntries, Revision: runRevision, CreatedAt: now, UpdatedAt: failedAt, CompletedAt: &failedAt,
		}
		if terminalErr := tr.persistExecutionPlanRunDurableWithTerm(failedRun, leaseTerm); terminalErr != nil {
			tr.enqueueExecutionPlanRunRecordRetryAfterWithTerm(failedRun, nil, leaseTerm)
			persistErr = errors.Join(persistErr, executionPlanPersistenceError("terminalize rolled-back parallel execution-plan run", terminalErr))
		}
		return nil, persistErr
	}
	for _, test := range createdTests {
		tr.broadcastEvent("test_created", test)
	}
	tr.consumeExecutionPlanApprovalWithTerm(gateApproval, finalRun.ID, leaseTerm)
	tr.handoffParallelExecutionPlanTestsWithTerm(finalRun, leaseTerm)
	tr.broadcastExecutionPlanEvent("execution_plan_run_finished", finalRun, nil)
	result = executionPlanMaterializeResultFromRun(finalRun)
	return result, nil
}

// handoffParallelExecutionPlanTestsWithTerm moves a durably materialized
// parallel plan into the ordinary fair-share queue immediately. Before this
// handoff, an idle controller could leave every staged test queued until an
// unrelated completion or leadership event happened to trigger promotion.
func (tr *TestRunner) handoffParallelExecutionPlanTestsWithTerm(run *storage.ExecutionPlanRun, leaseTerm executionLeaseTerm) {
	if run == nil || run.RunMode != "parallel" {
		return
	}
	if err := tr.handoffParallelExecutionPlanTestsOnceWithTerm(run, leaseTerm); err != nil {
		if errors.Is(err, errNotExecutionLeader) {
			return
		}
		slog.Warn("parallel execution-plan queue handoff failed", "run_id", run.ID, "error", err)
		tr.enqueueParallelExecutionPlanPromotionRetryWithTerm(run.ID, leaseTerm)
	}
}

func (tr *TestRunner) handoffParallelExecutionPlanTestsOnceWithTerm(run *storage.ExecutionPlanRun, leaseTerm executionLeaseTerm) error {
	if run == nil || run.RunMode != "parallel" {
		return nil
	}
	if !tr.executionLeaseTermCurrent(leaseTerm) {
		return errNotExecutionLeader
	}
	type admissionCandidate struct {
		index  int
		entry  storage.ExecutionPlanRunEntry
		testID string
		status string
	}
	type admissionOutcome struct {
		admissionCandidate
		err error
	}
	candidates := make([]admissionCandidate, 0, len(run.Entries))
	for i, entry := range run.Entries {
		testID := strings.TrimSpace(entry.TestID)
		if entry.Status != "created" || testID == "" {
			continue
		}
		tr.mu.RLock()
		test := tr.tests[testID]
		status := ""
		if test != nil {
			status = test.Status
		}
		tr.mu.RUnlock()
		if test == nil {
			return fmt.Errorf("materialized parallel execution-plan test %s is not loaded", testID)
		}
		candidates = append(candidates, admissionCandidate{index: i, entry: entry, testID: testID, status: status})
	}

	// Start admission concurrently so synchronous script shakeout in one entry
	// does not serialize a nominally parallel plan. The worker cap bounds CPU,
	// browser, and persistence pressure for very large plans.
	const maxParallelPlanAdmissionWorkers = 8
	outcomes := make(chan admissionOutcome, len(candidates))
	semaphore := make(chan struct{}, maxParallelPlanAdmissionWorkers)
	var wait sync.WaitGroup
	for _, candidate := range candidates {
		if candidate.status != "QUEUED" {
			outcomes <- admissionOutcome{admissionCandidate: candidate}
			continue
		}
		candidate := candidate
		wait.Add(1)
		go func() {
			defer wait.Done()
			semaphore <- struct{}{}
			err := tr.startExecutionPlanTestWithTerm(candidate.testID, leaseTerm)
			<-semaphore
			tr.mu.RLock()
			if current := tr.tests[candidate.testID]; current != nil {
				candidate.status = current.Status
			}
			tr.mu.RUnlock()
			outcomes <- admissionOutcome{admissionCandidate: candidate, err: err}
		}()
	}
	wait.Wait()
	close(outcomes)

	hasPending := false
	reconciled := cloneExecutionPlanRunRecord(run)
	changed := false
	for outcome := range outcomes {
		if errors.Is(outcome.err, errNotExecutionLeader) {
			return outcome.err
		}
		if outcome.err != nil {
			// Admission errors are terminal for this materialization. Otherwise a
			// QUEUED/PENDING row could run later even though the plan snapshot was
			// already returned as completed.
			if !isTerminalTestStatus(outcome.status) && outcome.status != "SHAKEOUT_REVIEW" {
				if failErr := tr.failExecutionPlanStagedTestWithTerm(outcome.testID, outcome.err.Error(), leaseTerm, outcome.entry.MaterializationKey); failErr != nil {
					return errors.Join(fmt.Errorf("start materialized parallel execution-plan test %s: %w", outcome.testID, outcome.err), failErr)
				}
			}
			reconciled.Entries[outcome.index].Status = "failed"
			reconciled.Entries[outcome.index].Detail = outcome.err.Error()
			changed = true
			continue
		}
		switch outcome.status {
		case "PENDING":
			hasPending = true
		case "FAILED", "ABORTED":
			tr.mu.RLock()
			current := tr.tests[outcome.testID]
			detail := ""
			if current != nil {
				detail = strings.TrimSpace(current.Error)
			}
			tr.mu.RUnlock()
			reconciled.Entries[outcome.index].Status = "failed"
			reconciled.Entries[outcome.index].Detail = firstNonEmpty(detail, "materialized test failed before execution admission")
			changed = true
		}
	}
	if changed {
		now := time.Now().UTC()
		reconciled.Status = "failed"
		reconciled.UpdatedAt = now
		reconciled.CompletedAt = &now
		reconciled.TransitionToken = ""
		if err := tr.persistExecutionPlanRunDurableWithTerm(reconciled, leaseTerm); err != nil {
			return executionPlanPersistenceError("persist parallel execution-plan admission failure", err)
		}
		*run = *reconciled
	}
	if hasPending {
		return tr.promotePendingTestsWithTerm(leaseTerm)
	}
	return nil
}

func (tr *TestRunner) executionPlanApprovalRunIDAwaitingAnchor(plan *storage.ExecutionPlan) (string, error) {
	if tr == nil || plan == nil || tr.execPlanScheduleStore == nil || tr.execPlanRunStore == nil {
		return "", nil
	}
	contentHash := tr.executionPlanApprovalContentHash(plan)
	approvals, err := tr.execPlanScheduleStore.ListExecutionPlanApprovals(plan.ID)
	if err != nil {
		return "", err
	}
	for _, approval := range approvals {
		if approval == nil || approval.Status != "approved" || approval.PlanContentHash != contentHash {
			continue
		}
		runID := strings.TrimSpace(approval.RunID)
		// Legacy claim-* values were not final run identities and cannot be safely
		// resumed. New claims always carry the final exec-plan-run-* ID.
		if !strings.HasPrefix(runID, "exec-plan-run-") {
			continue
		}
		run, loadErr := tr.execPlanRunStore.GetExecutionPlanRun(runID)
		if loadErr != nil && !errors.Is(loadErr, sql.ErrNoRows) {
			return "", loadErr
		}
		if run == nil || errors.Is(loadErr, sql.ErrNoRows) {
			return runID, nil
		}
	}
	return "", nil
}

func executionPlanMaterializeResultFromRun(run *storage.ExecutionPlanRun) *ExecutionPlanMaterializeResult {
	if run == nil {
		return nil
	}
	result := &ExecutionPlanMaterializeResult{RunID: run.ID, Status: run.Status, RunMode: run.RunMode}
	result.Results = make([]executionPlanRunResult, 0, len(run.Entries))
	for _, entry := range run.Entries {
		result.Results = append(result.Results, executionPlanRunResult{
			Order: entry.Order, ScenarioID: entry.ScenarioID, TestID: entry.TestID,
			Status: entry.Status, Detail: entry.Detail,
		})
		if entry.TestID != "" {
			result.Created++
		}
		if entry.Status == "failed" {
			result.Failed++
		}
	}
	return result
}

func approvalID(a *storage.ExecutionPlanApproval) string {
	if a == nil {
		return ""
	}
	return a.ID
}

// consumeExecutionPlanApproval refreshes the approval audit record after a run
// whose final ID was already atomically claimed before its anchor was created.
func (tr *TestRunner) consumeExecutionPlanApproval(approval *storage.ExecutionPlanApproval, runID string) {
	leaseTerm, err := tr.captureExecutionLeaseTerm()
	if err != nil {
		return
	}
	tr.consumeExecutionPlanApprovalWithTerm(approval, runID, leaseTerm)
}

func (tr *TestRunner) consumeExecutionPlanApprovalWithTerm(approval *storage.ExecutionPlanApproval, runID string, leaseTerm executionLeaseTerm) {
	if approval == nil || runID == "" || tr.execPlanScheduleStore == nil {
		return
	}
	approval.RunID = runID
	approval.UpdatedAt = time.Now().UTC()
	if err := tr.saveExecutionPlanApprovalWithTerm(approval, leaseTerm); err != nil {
		slog.Error("failed to attach execution-plan run to approval", "approval_id", approval.ID, "run_id", runID, "error", err)
	}
}

func (s *Server) handleMaterializeAndRunExecutionPlan(w http.ResponseWriter, r *http.Request) {
	// Running an execution plan mutates execution state (persists plan-run
	// records, creates/starts tests, broadcasts lifecycle events). On an HA
	// follower it must return 503 like every other execution-mutating handler,
	// rather than persisting a junk run and emitting misleading WebSocket events.
	if !s.requireExecutionLeader(w) {
		return
	}
	service := s.executionPlanDomainService()
	if !service.StorageConfigured() {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	plan, ok := s.loadExecutionPlan(w, r)
	if !ok {
		return
	}
	actorType, actorSubject, actorDisplayName := s.resolveAuditActor(r)
	attribSubject := ""
	if actorType == "user" || actorType == "service_account" {
		attribSubject = actorSubject
	}

	result, err := service.MaterializeAndRun(plan, attribSubject, actorDisplayName, testCreationActor{
		Type: actorType, Subject: actorSubject, DisplayName: actorDisplayName,
	})
	if err != nil {
		if errors.Is(err, errNotExecutionLeader) {
			writeError(w, http.StatusServiceUnavailable, "execution leader is unavailable")
			return
		}
		if errors.Is(err, errExecutionPlanRunPersistence) {
			writeError(w, http.StatusServiceUnavailable, "execution-plan run persistence is temporarily unavailable")
			return
		}
		if errors.Is(err, errServiceAccountRuntimeSecretWrite) || errors.Is(err, errUnattributedRuntimeSecretWrite) {
			s.recordAuditEvent(r, "execution_plan.run", "failure", "execution_plan", plan.ID, plan.ProjectID, map[string]any{
				"reason": "secret_write_denied", "actor": actorDisplayName,
			})
			writeError(w, http.StatusForbidden, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if result.Blocked {
		s.recordAuditEvent(r, "execution_plan.run", "blocked", "execution_plan", plan.ID, plan.ProjectID, map[string]any{
			"reason": "approval_required", "approval_id": approvalID(result.Approval), "actor": actorDisplayName,
		})
		writeJSON(w, http.StatusConflict, map[string]any{
			"execution_plan_id": plan.ID,
			"status":            "blocked",
			"reason":            "approval_required",
			"approval":          result.Approval,
		})
		return
	}
	s.recordAuditEvent(r, "execution_plan.run", "success", "execution_plan", plan.ID, plan.ProjectID, map[string]any{
		"run_mode": result.RunMode, "run_id": result.RunID, "status": result.Status, "created": result.Created, "failed": result.Failed, "actor": actorDisplayName,
	})
	response := map[string]any{
		"execution_plan_id": plan.ID,
		"run_mode":          result.RunMode,
		"run_id":            result.RunID,
		"status":            result.Status,
		"results":           result.Results,
		"materialized_at":   time.Now().UTC(),
	}
	if result.RunMode == "parallel" {
		response["created_count"] = result.Created
		response["failed_count"] = result.Failed
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleListExecutionPlanRuns(w http.ResponseWriter, r *http.Request) {
	service := s.executionPlanDomainService()
	limit, offset := parseLimitOffsetQuery(r, defaultExecutionPlanListLimit)
	if !service.StorageConfigured() {
		writeJSON(w, http.StatusOK, map[string]any{"runs": []*storage.ExecutionPlanRun{}, "count": 0, "limit": limit, "offset": offset})
		return
	}
	runs, err := service.ListRuns(r.PathValue("id"), limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if runs == nil {
		runs = []*storage.ExecutionPlanRun{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs, "count": len(runs), "limit": limit, "offset": offset})
}

func (s *Server) handleGetExecutionPlanRun(w http.ResponseWriter, r *http.Request) {
	service := s.executionPlanDomainService()
	if !service.StorageConfigured() {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	run, err := service.GetRun(r.PathValue("runId"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "execution plan run not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load execution plan run")
		return
	}
	// Confine the run to the plan in the path so the project-scoped auth holds.
	if run.PlanID != r.PathValue("id") {
		writeError(w, http.StatusNotFound, "execution plan run not found")
		return
	}
	// Keep the historical operation-specific OpenAPI response placeholder for
	// this storage-owned transport while preserving the exact runtime JSON.
	writeJSON(w, http.StatusOK, any(run))
}

func executionEntryLabel(entry storage.ExecutionPlanEntry) string {
	if strings.TrimSpace(entry.ScenarioName) != "" {
		return entry.ScenarioName
	}
	return entry.ScenarioID
}

func (s *Server) loadExecutionPlan(w http.ResponseWriter, r *http.Request) (*storage.ExecutionPlan, bool) {
	plan, err := s.executionPlanDomainService().Get(r.PathValue("id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "execution plan not found")
			return nil, false
		}
		writeError(w, http.StatusInternalServerError, "failed to load execution plan")
		return nil, false
	}
	return plan, true
}
