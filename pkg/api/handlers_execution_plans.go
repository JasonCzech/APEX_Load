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

func (s *Server) handleMaterializeAndRunExecutionPlan(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	plan, ok := s.loadExecutionPlan(w, r)
	if !ok {
		return
	}
	entries := make([]storage.ExecutionPlanEntry, len(plan.Entries))
	copy(entries, plan.Entries)
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Order < entries[j].Order })

	sequential := plan.RunMode == "sequential"
	actorType, actorSubject, actorDisplayName := s.resolveAuditActor(r)
	attributable := actorType == "user" || actorType == "service_account"

	results := make([]executionPlanRunResult, 0, len(entries))
	created, failed := 0, 0
	halted := false
	for _, entry := range entries {
		res := executionPlanRunResult{Order: entry.Order, ScenarioID: entry.ScenarioID}
		switch {
		case !entry.IsEnabled():
			res.Status = "skipped"
			res.Detail = "entry disabled"
		case halted:
			res.Status = "skipped"
			res.Detail = "halted after a prior failure"
		default:
			var overrides *RuntimeSettingsOverride
			if len(entry.RuntimeOverrides) > 0 {
				var parsed RuntimeSettingsOverride
				if err := json.Unmarshal(entry.RuntimeOverrides, &parsed); err == nil {
					overrides = &parsed
				}
			}
			test, err := s.runner.CreateTest(CreateTestRequest{
				Name:            strings.TrimSpace(plan.Name + " / " + executionEntryLabel(entry)),
				ScenarioID:      entry.ScenarioID,
				ProjectID:       plan.ProjectID,
				RuntimeSettings: overrides,
			})
			if err != nil {
				failed++
				res.Status = "failed"
				res.Detail = err.Error()
				// Default failure policy is halt; sequential plans stop, parallel continue.
				if sequential && entry.FailurePolicy != "skip" {
					halted = true
				}
			} else {
				created++
				res.Status = "created"
				res.TestID = test.ID
				if attributable {
					s.runner.AttributeTestCreated(test.ID, actorSubject, actorDisplayName)
				}
			}
		}
		results = append(results, res)
	}

	s.recordAuditEvent(r, "execution_plan.run", "success", "execution_plan", plan.ID, plan.ProjectID, map[string]any{
		"run_mode": plan.RunMode, "created": created, "failed": failed, "actor": actorDisplayName,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"execution_plan_id": plan.ID,
		"run_mode":          plan.RunMode,
		"created_count":     created,
		"failed_count":      failed,
		"results":           results,
		"materialized_at":   time.Now().UTC(),
	})
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
