package api

// Execution-plan run governance. A plan run whose aggregate peak VUsers would
// exceed the project's VUser quota is gated: it requires a project-admin approval
// before it can materialize. Parallel plans count concurrent demand (sum of
// enabled entries); sequential plans count the single largest entry. The gate is
// shared by the manual run handler and the recurring plan scheduler so both
// honor the same quota.

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

// executionPlanAggregatePeak estimates the plan's peak concurrent VUsers from its
// enabled entries' scenarios (parallel = sum, sequential = max single entry).
func (tr *TestRunner) executionPlanAggregatePeak(plan *storage.ExecutionPlan) int {
	peak := 0
	for _, entry := range plan.Entries {
		if !entry.IsEnabled() {
			continue
		}
		scenario, ok := tr.GetScenario(entry.ScenarioID)
		if !ok {
			continue
		}
		entryVUsers := 0
		for _, script := range scenario.Scripts {
			entryVUsers += script.VUsers
		}
		if plan.RunMode == "sequential" {
			if entryVUsers > peak {
				peak = entryVUsers
			}
		} else {
			peak += entryVUsers
		}
	}
	return peak
}

// executionPlanRunGate decides whether a plan run may proceed. It returns:
//   - (approval, false, nil): proceed; if approval != nil it is an approved,
//     unconsumed approval the caller must stamp with the new run id.
//   - (approval, true, nil): blocked; approval is the pending approval to surface.
//   - (nil, false, nil): no governance required (under quota / no quota set).
func (tr *TestRunner) executionPlanRunGate(plan *storage.ExecutionPlan, actorSubject string) (*storage.ExecutionPlanApproval, bool, error) {
	if tr.execPlanScheduleStore == nil {
		return nil, false, nil
	}
	projectID := normalizeProjectID(plan.ProjectID)
	maxVUsers := tr.projectMaxVUsers(projectID)
	if maxVUsers <= 0 {
		return nil, false, nil
	}
	peak := tr.executionPlanAggregatePeak(plan)
	if peak <= maxVUsers {
		return nil, false, nil
	}
	approvals, err := tr.execPlanScheduleStore.ListExecutionPlanApprovals(plan.ID)
	if err != nil {
		return nil, false, err
	}
	var pending *storage.ExecutionPlanApproval
	for _, approval := range approvals {
		if approval.Status == "approved" && approval.RunID == "" {
			return approval, false, nil // consume this authorization
		}
		if approval.Status == "pending" && pending == nil {
			pending = approval
		}
	}
	if pending != nil {
		return pending, true, nil
	}
	now := time.Now().UTC()
	approval := &storage.ExecutionPlanApproval{
		ID:             uuid.New().String(),
		PlanID:         plan.ID,
		ProjectID:      projectID,
		Policy:         "quota_burst",
		Status:         "pending",
		RequestedBy:    actorSubject,
		RequestedAt:    now,
		RequiredRole:   RoleProjectAdmin,
		DecisionReason: fmt.Sprintf("Aggregate peak %d VUsers exceeds project limit %d; project-admin approval required.", peak, maxVUsers),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := tr.execPlanScheduleStore.SaveExecutionPlanApproval(approval); err != nil {
		return nil, false, err
	}
	return approval, true, nil
}

// ResolveExecutionPlanApproval approves or rejects a pending plan approval. It
// only flips a still-pending row (a lightweight guard against double-resolve).
func (tr *TestRunner) ResolveExecutionPlanApproval(planID, approvalID string, approve bool, resolvedBy, reason string) (*storage.ExecutionPlanApproval, bool, error) {
	if tr.execPlanScheduleStore == nil {
		return nil, false, nil
	}
	approval, err := tr.execPlanScheduleStore.GetExecutionPlanApproval(approvalID)
	if err != nil {
		return nil, false, err
	}
	if approval == nil || approval.PlanID != planID {
		return nil, false, nil
	}
	if approval.Status != "pending" {
		return approval, true, fmt.Errorf("approval is already %s", approval.Status)
	}
	now := time.Now().UTC()
	if approve {
		approval.Status = "approved"
	} else {
		approval.Status = "rejected"
	}
	approval.ResolvedBy = resolvedBy
	approval.ResolvedAt = &now
	approval.DecisionReason = reason
	approval.UpdatedAt = now
	if err := tr.execPlanScheduleStore.SaveExecutionPlanApproval(approval); err != nil {
		return nil, true, err
	}
	return approval, true, nil
}

func (tr *TestRunner) ListExecutionPlanApprovals(planID string) ([]*storage.ExecutionPlanApproval, error) {
	if tr.execPlanScheduleStore == nil {
		return nil, nil
	}
	return tr.execPlanScheduleStore.ListExecutionPlanApprovals(planID)
}
