package storage

// SQLite store — execution-plan run approvals (governance gate). Mirrors the
// scenario-schedule approval persistence.

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

const executionPlanApprovalColumns = `id, plan_id, COALESCE(project_id, ''), policy, status, COALESCE(requested_by, ''),
	requested_at, COALESCE(resolved_by, ''), resolved_at, COALESCE(decision_reason, ''), COALESCE(required_role, ''),
	COALESCE(run_id, ''), created_at, updated_at FROM execution_plan_approvals`

func (s *SQLiteStore) SaveExecutionPlanApproval(approval *ExecutionPlanApproval) error {
	if approval == nil {
		return errors.New("execution plan approval is required")
	}
	if strings.TrimSpace(approval.ID) == "" {
		return errors.New("execution plan approval id is required")
	}
	if strings.TrimSpace(approval.PlanID) == "" {
		return errors.New("execution plan approval plan_id is required")
	}
	if strings.TrimSpace(approval.Policy) == "" {
		return errors.New("execution plan approval policy is required")
	}
	if strings.TrimSpace(approval.Status) == "" {
		return errors.New("execution plan approval status is required")
	}
	if approval.RequestedAt.IsZero() {
		approval.RequestedAt = time.Now()
	}
	if approval.CreatedAt.IsZero() {
		approval.CreatedAt = time.Now()
	}
	if approval.UpdatedAt.IsZero() {
		approval.UpdatedAt = time.Now()
	}
	approval.ProjectID = normalizeProjectID(approval.ProjectID)
	_, err := s.db.Exec(`INSERT INTO execution_plan_approvals (id, plan_id, project_id, policy, status, requested_by, requested_at, resolved_by, resolved_at, decision_reason, required_role, run_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET plan_id=excluded.plan_id, project_id=excluded.project_id, policy=excluded.policy, status=excluded.status,
		requested_by=excluded.requested_by, requested_at=excluded.requested_at, resolved_by=excluded.resolved_by,
		resolved_at=excluded.resolved_at, decision_reason=excluded.decision_reason, required_role=excluded.required_role,
		run_id=excluded.run_id, updated_at=excluded.updated_at`,
		approval.ID, approval.PlanID, approval.ProjectID, approval.Policy, approval.Status, approval.RequestedBy, approval.RequestedAt, approval.ResolvedBy, approval.ResolvedAt, approval.DecisionReason, approval.RequiredRole, approval.RunID, approval.CreatedAt, approval.UpdatedAt)
	return err
}

func (s *SQLiteStore) GetExecutionPlanApproval(id string) (*ExecutionPlanApproval, error) {
	row := s.db.QueryRow(`SELECT `+executionPlanApprovalColumns+` WHERE id = ?`, id)
	approval, err := scanExecutionPlanApproval(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return approval, err
}

func (s *SQLiteStore) ListExecutionPlanApprovals(planID string) ([]*ExecutionPlanApproval, error) {
	rows, err := s.db.Query(`SELECT `+executionPlanApprovalColumns+` WHERE plan_id = ? ORDER BY requested_at DESC`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	approvals := make([]*ExecutionPlanApproval, 0)
	for rows.Next() {
		approval, err := scanExecutionPlanApproval(rows)
		if err != nil {
			return nil, err
		}
		approvals = append(approvals, approval)
	}
	return approvals, rows.Err()
}

func scanExecutionPlanApproval(row scanner) (*ExecutionPlanApproval, error) {
	var approval ExecutionPlanApproval
	err := row.Scan(
		&approval.ID, &approval.PlanID, &approval.ProjectID, &approval.Policy, &approval.Status, &approval.RequestedBy,
		&approval.RequestedAt, &approval.ResolvedBy, &approval.ResolvedAt, &approval.DecisionReason, &approval.RequiredRole,
		&approval.RunID, &approval.CreatedAt, &approval.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	approval.ProjectID = normalizeProjectID(approval.ProjectID)
	return &approval, nil
}
