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
	COALESCE(plan_content_hash, ''), COALESCE(run_id, ''), created_at, updated_at FROM execution_plan_approvals`

func (s *SQLiteStore) SaveExecutionPlanApproval(approval *ExecutionPlanApproval) error {
	return saveExecutionPlanApprovalWithExecutor(s.db, approval)
}

func saveExecutionPlanApprovalWithExecutor(executor testSaveExecutor, approval *ExecutionPlanApproval) error {
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
	var projectID string
	if err := executor.QueryRow(`SELECT COALESCE(project_id, '') FROM execution_plans WHERE id = ?`, approval.PlanID).Scan(&projectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("execution plan not found: " + approval.PlanID)
		}
		return err
	}
	approval.ProjectID = normalizeProjectID(projectID)
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
	result, err := executor.Exec(`INSERT INTO execution_plan_approvals (id, plan_id, project_id, policy, status, requested_by, requested_at, resolved_by, resolved_at, decision_reason, required_role, plan_content_hash, run_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET policy=excluded.policy, status=excluded.status,
		requested_by=excluded.requested_by, requested_at=excluded.requested_at, resolved_by=excluded.resolved_by,
		resolved_at=excluded.resolved_at, decision_reason=excluded.decision_reason, required_role=excluded.required_role,
		plan_content_hash=excluded.plan_content_hash, run_id=excluded.run_id, updated_at=excluded.updated_at
		WHERE execution_plan_approvals.plan_id=excluded.plan_id AND execution_plan_approvals.project_id=excluded.project_id`,
		approval.ID, approval.PlanID, approval.ProjectID, approval.Policy, approval.Status, approval.RequestedBy, approval.RequestedAt, approval.ResolvedBy, approval.ResolvedAt, approval.DecisionReason, approval.RequiredRole, approval.PlanContentHash, approval.RunID, approval.CreatedAt, approval.UpdatedAt)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return errors.New("execution plan approval belongs to another plan")
	}
	return nil
}

func (s *SQLiteStore) SaveExecutionPlanApprovalIfLeaseTermHeld(approval *ExecutionPlanApproval, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error) {
	tx, err := s.beginExecutionLeaseTermTransaction(leaseName, leaseOwnerID, leaseGeneration)
	if err != nil || tx == nil {
		return false, err
	}
	defer tx.Rollback()
	if err := saveExecutionPlanApprovalWithExecutor(tx, approval); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
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
	return s.listExecutionPlanApprovalsPage(planID, 0, 0)
}

// ListExecutionPlanApprovalsPage bounds controller-facing governance history
// reads while retaining the unpaged primitive for internal policy evaluation.
func (s *SQLiteStore) ListExecutionPlanApprovalsPage(planID string, limit, offset int) ([]*ExecutionPlanApproval, error) {
	if limit <= 0 {
		return []*ExecutionPlanApproval{}, nil
	}
	if offset < 0 {
		offset = 0
	}
	return s.listExecutionPlanApprovalsPage(planID, limit, offset)
}

func (s *SQLiteStore) listExecutionPlanApprovalsPage(planID string, limit, offset int) ([]*ExecutionPlanApproval, error) {
	query := `SELECT ` + executionPlanApprovalColumns + ` WHERE plan_id = ? ORDER BY requested_at DESC`
	args := []any{planID}
	if limit > 0 {
		query += ` LIMIT ? OFFSET ?`
		args = append(args, limit, offset)
	}
	rows, err := s.db.Query(query, args...)
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

// ClaimExecutionPlanApproval atomically marks an approved, unconsumed approval as
// consumed by stamping run_id, but only if it is still approved AND unconsumed.
// Returns false when another run already claimed it — the single-use guarantee.
func (s *SQLiteStore) ClaimExecutionPlanApproval(id, runID string, updatedAt time.Time) (bool, error) {
	return claimExecutionPlanApprovalWithExecutor(s.db, id, runID, updatedAt)
}

func claimExecutionPlanApprovalWithExecutor(executor testSaveExecutor, id, runID string, updatedAt time.Time) (bool, error) {
	if strings.TrimSpace(runID) == "" {
		return false, errors.New("run id is required to claim an approval")
	}
	result, err := executor.Exec(`UPDATE execution_plan_approvals SET run_id = ?, updated_at = ? WHERE id = ? AND status = 'approved' AND (run_id IS NULL OR run_id = '')`,
		runID, updatedAt.UTC(), id)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func (s *SQLiteStore) ClaimExecutionPlanApprovalIfLeaseTermHeld(id, runID string, updatedAt time.Time, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error) {
	tx, err := s.beginExecutionLeaseTermTransaction(leaseName, leaseOwnerID, leaseGeneration)
	if err != nil || tx == nil {
		return false, err
	}
	defer tx.Rollback()
	claimed, err := claimExecutionPlanApprovalWithExecutor(tx, id, runID, updatedAt)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return claimed, nil
}

// UpdateExecutionPlanApprovalResolution atomically resolves a still-pending
// approval, closing the read-modify-write TOCTOU. Returns false if the row is no
// longer pending (already approved/rejected concurrently).
func (s *SQLiteStore) UpdateExecutionPlanApprovalResolution(id, toStatus, resolvedBy, reason string, resolvedAt time.Time) (bool, error) {
	return updateExecutionPlanApprovalResolutionWithExecutor(s.db, id, toStatus, resolvedBy, reason, resolvedAt)
}

func updateExecutionPlanApprovalResolutionWithExecutor(executor testSaveExecutor, id, toStatus, resolvedBy, reason string, resolvedAt time.Time) (bool, error) {
	result, err := executor.Exec(`UPDATE execution_plan_approvals SET status = ?, resolved_by = ?, resolved_at = ?, decision_reason = ?, updated_at = ? WHERE id = ? AND status = 'pending'`,
		toStatus, resolvedBy, resolvedAt.UTC(), reason, resolvedAt.UTC(), id)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func (s *SQLiteStore) UpdateExecutionPlanApprovalResolutionIfLeaseTermHeld(id, toStatus, resolvedBy, reason string, resolvedAt time.Time, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error) {
	tx, err := s.beginExecutionLeaseTermTransaction(leaseName, leaseOwnerID, leaseGeneration)
	if err != nil || tx == nil {
		return false, err
	}
	defer tx.Rollback()
	updated, err := updateExecutionPlanApprovalResolutionWithExecutor(tx, id, toStatus, resolvedBy, reason, resolvedAt)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return updated, nil
}

// UpdateExecutionPlanApprovalResolutionAndContentHash atomically binds a
// legacy pending approval to the reviewed plan content while resolving it. A
// competing resolver cannot observe or restore the intermediate pending row.
func (s *SQLiteStore) UpdateExecutionPlanApprovalResolutionAndContentHash(id, toStatus, resolvedBy, reason, contentHash string, resolvedAt time.Time) (bool, error) {
	return updateExecutionPlanApprovalResolutionAndContentHashWithExecutor(s.db, id, toStatus, resolvedBy, reason, contentHash, resolvedAt)
}

func updateExecutionPlanApprovalResolutionAndContentHashWithExecutor(executor testSaveExecutor, id, toStatus, resolvedBy, reason, contentHash string, resolvedAt time.Time) (bool, error) {
	contentHash = strings.TrimSpace(contentHash)
	if contentHash == "" {
		return false, errors.New("execution plan approval content hash is required")
	}
	result, err := executor.Exec(`UPDATE execution_plan_approvals SET
		status = ?, resolved_by = ?, resolved_at = ?, decision_reason = ?, plan_content_hash = ?, updated_at = ?
		WHERE id = ? AND status = 'pending' AND (COALESCE(plan_content_hash, '') = '' OR plan_content_hash = ?)`,
		toStatus, resolvedBy, resolvedAt.UTC(), reason, contentHash, resolvedAt.UTC(), id, contentHash)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func (s *SQLiteStore) UpdateExecutionPlanApprovalResolutionAndContentHashIfLeaseTermHeld(id, toStatus, resolvedBy, reason, contentHash string, resolvedAt time.Time, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error) {
	tx, err := s.beginExecutionLeaseTermTransaction(leaseName, leaseOwnerID, leaseGeneration)
	if err != nil || tx == nil {
		return false, err
	}
	defer tx.Rollback()
	updated, err := updateExecutionPlanApprovalResolutionAndContentHashWithExecutor(tx, id, toStatus, resolvedBy, reason, contentHash, resolvedAt)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return updated, nil
}

func scanExecutionPlanApproval(row scanner) (*ExecutionPlanApproval, error) {
	var approval ExecutionPlanApproval
	err := row.Scan(
		&approval.ID, &approval.PlanID, &approval.ProjectID, &approval.Policy, &approval.Status, &approval.RequestedBy,
		&approval.RequestedAt, &approval.ResolvedBy, &approval.ResolvedAt, &approval.DecisionReason, &approval.RequiredRole,
		&approval.PlanContentHash, &approval.RunID, &approval.CreatedAt, &approval.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	approval.ProjectID = normalizeProjectID(approval.ProjectID)
	return &approval, nil
}
