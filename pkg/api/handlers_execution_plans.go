package api

// Execution plans: ordered cross-run orchestration of scenarios — the
// LoadRunner Enterprise test-set/test-instance analog. A plan materializes one
// test run per enabled entry (in Order), either in parallel or sequentially.
// Sequential runs are created eagerly with honest per-entry partial-failure
// reporting; true dependency-gated sequential *execution* (wait for run N before
// starting N+1) is a follow-up tracked under the execution_planning dimension.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

const defaultExecutionPlanListLimit = 100

func validExecutionRunMode(mode string) bool {
	return mode == "parallel" || mode == "sequential"
}

func (s *Server) validateExecutionPlan(plan *storage.ExecutionPlan) (int, string) {
	if strings.TrimSpace(plan.Name) == "" {
		return http.StatusBadRequest, "execution plan name is required"
	}
	if plan.RunMode != "" && !validExecutionRunMode(plan.RunMode) {
		return http.StatusBadRequest, "run_mode must be parallel or sequential"
	}
	planProject := normalizeProjectID(plan.ProjectID)
	for _, entry := range plan.Entries {
		if strings.TrimSpace(entry.ScenarioID) == "" {
			return http.StatusBadRequest, "every execution plan entry requires a scenario_id"
		}
		if entry.FailurePolicy != "" && entry.FailurePolicy != "halt" && entry.FailurePolicy != "skip" {
			return http.StatusBadRequest, "entry failure_policy must be halt or skip"
		}
		if len(entry.RuntimeOverrides) > 0 {
			var runtimeOverrides *RuntimeSettingsOverride
			if err := json.Unmarshal(entry.RuntimeOverrides, &runtimeOverrides); err != nil {
				return http.StatusBadRequest, "entry runtime_overrides must be a valid runtime settings object"
			}
		}
		for _, binding := range entry.DataPools {
			if strings.TrimSpace(binding.PoolID) == "" {
				return http.StatusBadRequest, "every execution plan entry data pool requires a pool_id"
			}
			if strings.TrimSpace(binding.Alias) == "" {
				return http.StatusBadRequest, "every execution plan entry data pool requires an alias"
			}
		}
		// Cross-tenant guard: an entry that references an existing scenario must
		// reference one in the plan's own project. Unknown scenarios are allowed
		// (they fail per-entry at materialize) so plans can be drafted early.
		if s.runner != nil {
			if scenario, ok := s.runner.GetScenario(entry.ScenarioID); ok && normalizeProjectID(scenario.ProjectID) != planProject {
				return http.StatusBadRequest, "an execution plan entry references a scenario in a different project"
			}
		}
	}
	return 0, ""
}

func (s *Server) handleCreateExecutionPlan(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	var plan storage.ExecutionPlan
	if !decodeJSONBody(w, r, &plan) {
		return
	}
	if status, msg := s.validateExecutionPlan(&plan); status != 0 {
		writeError(w, status, msg)
		return
	}
	plan.ID = "exec-plan-" + uuid.New().String()
	plan.ProjectID = normalizeProjectID(plan.ProjectID)
	if plan.RunMode == "" {
		plan.RunMode = "parallel"
	}
	plan.Status = "draft"
	if err := s.store.SaveExecutionPlan(&plan); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "execution_plan.create", "success", "execution_plan", plan.ID, plan.ProjectID, map[string]any{
		"name": plan.Name, "entries": len(plan.Entries), "run_mode": plan.RunMode,
	})
	writeJSON(w, http.StatusCreated, plan)
}

func (s *Server) handleListExecutionPlans(w http.ResponseWriter, r *http.Request) {
	limit, offset := parseLimitOffsetQuery(r, defaultExecutionPlanListLimit)
	if s.store == nil {
		writeJSON(w, http.StatusOK, map[string]any{"execution_plans": []*storage.ExecutionPlan{}, "count": 0, "limit": limit, "offset": offset})
		return
	}
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	plans, err := s.store.ListExecutionPlans(projectID, limit, offset)
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
	if s.store == nil {
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
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
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
	req.ID = existing.ID
	req.ProjectID = existing.ProjectID
	req.CreatedAt = existing.CreatedAt
	if status, msg := s.validateExecutionPlan(&req); status != 0 {
		writeError(w, status, msg)
		return
	}
	if req.RunMode == "" {
		req.RunMode = existing.RunMode
	}
	if req.Status == "" {
		req.Status = existing.Status
	}
	if err := s.store.SaveExecutionPlan(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "execution_plan.update", "success", "execution_plan", req.ID, req.ProjectID, map[string]any{"entries": len(req.Entries)})
	writeJSON(w, http.StatusOK, req)
}

func (s *Server) handleDeleteExecutionPlan(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	id := r.PathValue("id")
	projectID := ""
	if plan, err := s.store.GetExecutionPlan(id); err == nil && plan != nil {
		projectID = plan.ProjectID
	}
	if err := s.store.DeleteExecutionPlan(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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
func (tr *TestRunner) MaterializeAndRunExecutionPlan(plan *storage.ExecutionPlan, attribSubject, attribDisplayName string) (*ExecutionPlanMaterializeResult, error) {
	return tr.materializeAndRunExecutionPlan(plan, attribSubject, attribDisplayName, "")
}

func (tr *TestRunner) materializeAndRunExecutionPlan(plan *storage.ExecutionPlan, attribSubject, attribDisplayName, runNameOverride string) (*ExecutionPlanMaterializeResult, error) {
	return tr.materializeAndRunExecutionPlanWithRunID(plan, attribSubject, attribDisplayName, runNameOverride, "")
}

// materializeAndRunExecutionPlanWithRunID is the idempotent scheduler-facing
// variant. A non-empty runIDOverride is anchored before test side effects and is
// reused on occurrence replay after a controller crash.
func (tr *TestRunner) materializeAndRunExecutionPlanWithRunID(plan *storage.ExecutionPlan, attribSubject, attribDisplayName, runNameOverride, runIDOverride string) (*ExecutionPlanMaterializeResult, error) {
	if plan == nil {
		return nil, errors.New("execution plan is required")
	}
	runIDOverride = strings.TrimSpace(runIDOverride)
	if runIDOverride != "" && tr.execPlanRunStore != nil {
		existing, err := tr.execPlanRunStore.GetExecutionPlanRun(runIDOverride)
		switch {
		case err == nil && existing != nil:
			if existing.PlanID != plan.ID || normalizeProjectID(existing.ProjectID) != normalizeProjectID(plan.ProjectID) || existing.RunMode != plan.RunMode {
				return nil, fmt.Errorf("execution-plan run id %q is already used by another materialization", runIDOverride)
			}
			return executionPlanMaterializeResultFromRun(existing), nil
		case err != nil && !errors.Is(err, sql.ErrNoRows):
			return nil, executionPlanPersistenceError("check preassigned execution-plan run id", err)
		}
	}
	runName := strings.TrimSpace(runNameOverride)
	if runName == "" {
		runName = strings.TrimSpace(plan.Name)
	}
	// Governance gate: a plan whose aggregate peak exceeds the project VUser quota
	// requires a project-admin approval before it can run.
	gateApproval, blocked, err := tr.executionPlanRunGate(plan, attribSubject, runIDOverride)
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
		if claimRunID == "" {
			claimRunID = "claim-" + uuid.New().String()
		}
		claimed := claimRunID != "" && gateApproval.RunID == claimRunID
		if !claimed {
			var err error
			claimed, err = tr.execPlanScheduleStore.ClaimExecutionPlanApproval(gateApproval.ID, claimRunID, time.Now().UTC())
			if err != nil {
				return nil, err
			}
			if !claimed && runIDOverride != "" {
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
		run, err := tr.startExecutionPlanRunWithID(plan, attribSubject, attribDisplayName, runName, runIDOverride, gateApproval)
		if err != nil {
			return nil, err
		}
		results := make([]executionPlanRunResult, 0, len(run.Entries))
		for _, e := range run.Entries {
			results = append(results, executionPlanRunResult{Order: e.Order, ScenarioID: e.ScenarioID, TestID: e.TestID, Status: e.Status, Detail: e.Detail})
		}
		result = &ExecutionPlanMaterializeResult{RunID: run.ID, Status: run.Status, RunMode: "sequential", Results: results}
		tr.consumeExecutionPlanApproval(gateApproval, result.RunID)
		return result, nil
	}

	entries := make([]storage.ExecutionPlanEntry, len(plan.Entries))
	copy(entries, plan.Entries)
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Order < entries[j].Order })

	now := time.Now().UTC()
	runID := runIDOverride
	if runID == "" {
		runID = "exec-plan-run-" + uuid.New().String()
	}
	initialEntries := make([]storage.ExecutionPlanRunEntry, 0, len(entries))
	for _, entry := range entries {
		status := "pending"
		detail := ""
		if !entry.IsEnabled() {
			status, detail = "skipped", "entry disabled"
		}
		initialEntries = append(initialEntries, storage.ExecutionPlanRunEntry{
			Order: entry.Order, ScenarioID: entry.ScenarioID, ScenarioName: entry.ScenarioName,
			Status: status, Detail: detail,
		})
	}
	// Anchor the parallel materialization before creating any tests. If storage is
	// unavailable the handler fails without emitting test-created side effects.
	anchor := &storage.ExecutionPlanRun{
		ID: runID, PlanID: plan.ID, ProjectID: normalizeProjectID(plan.ProjectID),
		RunMode: "parallel", Status: "running", Entries: initialEntries,
		CreatedAt: now, UpdatedAt: now,
	}
	createdAnchor, err := tr.createExecutionPlanRunDurable(anchor)
	if err != nil {
		return nil, executionPlanPersistenceError("persist parallel execution-plan run before materialization", err)
	}
	if !createdAnchor {
		existing, err := tr.loadExistingExecutionPlanRun(runID, plan.ID, plan.ProjectID, "parallel")
		if err != nil {
			return nil, err
		}
		return executionPlanMaterializeResultFromRun(existing), nil
	}
	runRevision := anchor.Revision
	results := make([]executionPlanRunResult, 0, len(entries))
	runEntries := make([]storage.ExecutionPlanRunEntry, 0, len(entries))
	createdTests := make([]*ManagedTest, 0, len(entries))
	created, failed := 0, 0
	planTimeslotUsed := false
	for _, entry := range entries {
		res := executionPlanRunResult{Order: entry.Order, ScenarioID: entry.ScenarioID}
		runEntry := storage.ExecutionPlanRunEntry{Order: entry.Order, ScenarioID: entry.ScenarioID, ScenarioName: entry.ScenarioName}
		if !entry.IsEnabled() {
			res.Status, res.Detail = "skipped", "entry disabled"
			runEntry.Status, runEntry.Detail = "skipped", "entry disabled"
			results = append(results, res)
			runEntries = append(runEntries, runEntry)
			continue
		}
		var planTimeslot *string
		if trimPtr(entry.TimeslotID) == "" && !planTimeslotUsed && trimPtr(plan.TimeslotID) != "" {
			planTimeslot = plan.TimeslotID
			planTimeslotUsed = true
		}
		createRequest, err := buildPlanEntryCreateRequest(runName, plan.ProjectID, planTimeslot, entry)
		var test *ManagedTest
		if err == nil {
			test, err = tr.createTest(createRequest, false)
		}
		if err != nil {
			failed++
			res.Status, res.Detail = "failed", err.Error()
			runEntry.Status, runEntry.Detail = "failed", err.Error()
		} else {
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
	if err := tr.persistExecutionPlanRunDurable(finalRun); err != nil {
		persistErr := executionPlanPersistenceError("persist completed parallel execution-plan run", err)
		if errors.Is(err, errExecutionPlanRunCommitUnknown) {
			// A lost commit response is not proof that the final transition failed.
			// Keep the silently staged tests intact and reconcile the exact snapshot;
			// deleting them could corrupt a run the database already accepted.
			tr.enqueueExecutionPlanRunRecordRetryAfter(finalRun, func() {
				for _, test := range createdTests {
					tr.broadcastEvent("test_created", test)
				}
				tr.consumeExecutionPlanApproval(gateApproval, finalRun.ID)
			})
			return nil, persistErr
		}
		for _, test := range createdTests {
			if _, cleanupErr := tr.DeleteTestWithError(test.ID); cleanupErr != nil {
				persistErr = errors.Join(persistErr, fmt.Errorf("clean up uncommitted plan test %s: %w", test.ID, cleanupErr))
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
		if terminalErr := tr.persistExecutionPlanRunDurable(failedRun); terminalErr != nil {
			tr.enqueueExecutionPlanRunRecordRetry(failedRun)
			persistErr = errors.Join(persistErr, executionPlanPersistenceError("terminalize rolled-back parallel execution-plan run", terminalErr))
		}
		return nil, persistErr
	}
	for _, test := range createdTests {
		tr.broadcastEvent("test_created", test)
	}
	result = &ExecutionPlanMaterializeResult{RunID: runID, Status: runStatus, RunMode: "parallel", Created: created, Failed: failed, Results: results}
	tr.consumeExecutionPlanApproval(gateApproval, result.RunID)
	return result, nil
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

// consumeExecutionPlanApproval records the real run id on an approval that was
// already atomically claimed before the run (see ClaimExecutionPlanApproval),
// replacing the claim token for audit accuracy.
func (tr *TestRunner) consumeExecutionPlanApproval(approval *storage.ExecutionPlanApproval, runID string) {
	if approval == nil || runID == "" || tr.execPlanScheduleStore == nil {
		return
	}
	approval.RunID = runID
	approval.UpdatedAt = time.Now().UTC()
	if err := tr.execPlanScheduleStore.SaveExecutionPlanApproval(approval); err != nil {
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
	if s.store == nil {
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

	result, err := s.runner.MaterializeAndRunExecutionPlan(plan, attribSubject, actorDisplayName)
	if err != nil {
		if errors.Is(err, errExecutionPlanRunPersistence) {
			writeError(w, http.StatusServiceUnavailable, "execution-plan run persistence is temporarily unavailable")
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
	limit, offset := parseLimitOffsetQuery(r, defaultExecutionPlanListLimit)
	if s.store == nil {
		writeJSON(w, http.StatusOK, map[string]any{"runs": []*storage.ExecutionPlanRun{}, "count": 0, "limit": limit, "offset": offset})
		return
	}
	runs, err := s.store.ListExecutionPlanRuns(r.PathValue("id"), limit, offset)
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
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	run, err := s.store.GetExecutionPlanRun(r.PathValue("runId"))
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
	writeJSON(w, http.StatusOK, run)
}

func executionEntryLabel(entry storage.ExecutionPlanEntry) string {
	if strings.TrimSpace(entry.ScenarioName) != "" {
		return entry.ScenarioName
	}
	return entry.ScenarioID
}

func (s *Server) loadExecutionPlan(w http.ResponseWriter, r *http.Request) (*storage.ExecutionPlan, bool) {
	plan, err := s.store.GetExecutionPlan(r.PathValue("id"))
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
