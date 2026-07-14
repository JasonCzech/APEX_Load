package storage

// SQLite store — execution plans. Entries are persisted inline as JSON. The same
// SQL serves the PostgreSQL backend via rewriteSQL.

import (
	"encoding/json"
	"fmt"
	"time"
)

func (s *SQLiteStore) SaveExecutionPlan(plan *ExecutionPlan) error {
	if plan == nil {
		return fmt.Errorf("execution plan is required")
	}
	plan.ProjectID = normalizeProjectID(plan.ProjectID)
	if plan.RunMode == "" {
		plan.RunMode = "parallel"
	}
	if plan.Status == "" {
		plan.Status = "draft"
	}
	now := time.Now()
	if plan.CreatedAt.IsZero() {
		plan.CreatedAt = now
	}
	plan.UpdatedAt = now
	entriesJSON, err := json.Marshal(plan.Entries)
	if err != nil {
		return fmt.Errorf("marshal execution plan entries: %w", err)
	}
	_, err = s.db.Exec(`INSERT INTO execution_plans (
			id, project_id, name, description, entries_json, run_mode, timeslot_id, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			project_id=excluded.project_id, name=excluded.name, description=excluded.description,
			entries_json=excluded.entries_json, run_mode=excluded.run_mode, timeslot_id=excluded.timeslot_id,
			status=excluded.status, updated_at=excluded.updated_at`,
		plan.ID, plan.ProjectID, plan.Name, plan.Description, string(entriesJSON), plan.RunMode,
		plan.TimeslotID, plan.Status, plan.CreatedAt, plan.UpdatedAt)
	return err
}

const executionPlanSelect = `SELECT id, COALESCE(project_id, ''), name, COALESCE(description, ''),
	COALESCE(entries_json, ''), COALESCE(run_mode, 'parallel'), timeslot_id, COALESCE(status, 'draft'),
	created_at, updated_at FROM execution_plans`

func scanExecutionPlan(row capacityRowScanner) (*ExecutionPlan, error) {
	var plan ExecutionPlan
	var entriesJSON string
	if err := row.Scan(&plan.ID, &plan.ProjectID, &plan.Name, &plan.Description, &entriesJSON,
		&plan.RunMode, &plan.TimeslotID, &plan.Status, &plan.CreatedAt, &plan.UpdatedAt); err != nil {
		return nil, err
	}
	if entriesJSON != "" && entriesJSON != "null" {
		if err := json.Unmarshal([]byte(entriesJSON), &plan.Entries); err != nil {
			return nil, fmt.Errorf("unmarshal execution plan entries: %w", err)
		}
	}
	if plan.Entries == nil {
		plan.Entries = []ExecutionPlanEntry{}
	}
	return &plan, nil
}

func (s *SQLiteStore) GetExecutionPlan(id string) (*ExecutionPlan, error) {
	return scanExecutionPlan(s.db.QueryRow(executionPlanSelect+` WHERE id = ?`, id))
}

func (s *SQLiteStore) ListExecutionPlans(projectID string, limit int, offset int) ([]*ExecutionPlan, error) {
	query := executionPlanSelect
	args := []any{}
	if projectID != "" {
		query += ` WHERE project_id = ?`
		args = append(args, normalizeProjectID(projectID))
	}
	query += ` ORDER BY created_at DESC`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
		if offset > 0 {
			query += ` OFFSET ?`
			args = append(args, offset)
		}
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	plans := make([]*ExecutionPlan, 0)
	for rows.Next() {
		plan, err := scanExecutionPlan(rows)
		if err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}
	return plans, rows.Err()
}

func (s *SQLiteStore) DeleteExecutionPlan(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM execution_plan_schedule_occurrences WHERE schedule_id IN (SELECT id FROM execution_plan_schedules WHERE plan_id = ?)`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM execution_plan_schedules WHERE plan_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM execution_plan_approvals WHERE plan_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM execution_plans WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) SaveExecutionPlanRun(run *ExecutionPlanRun) error {
	entriesJSON, err := prepareExecutionPlanRunForWrite(run)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO execution_plan_runs (
			id, plan_id, project_id, run_mode, status, entries_json, revision, created_at, updated_at, completed_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
			plan_id=excluded.plan_id, project_id=excluded.project_id, run_mode=excluded.run_mode,
			status=excluded.status, entries_json=excluded.entries_json, updated_at=excluded.updated_at,
			completed_at=excluded.completed_at, revision=execution_plan_runs.revision+1`,
		run.ID, run.PlanID, run.ProjectID, run.RunMode, run.Status, string(entriesJSON),
		run.Revision, run.CreatedAt, run.UpdatedAt, run.CompletedAt)
	return err
}

// CreateExecutionPlanRunIfAbsent atomically establishes the durable side-effect
// boundary for one materialization. Only the caller that inserts the RunID may
// proceed to create tests; a conflicting caller must reload the existing row.
func (s *SQLiteStore) CreateExecutionPlanRunIfAbsent(run *ExecutionPlanRun) (bool, error) {
	entriesJSON, err := prepareExecutionPlanRunForWrite(run)
	if err != nil {
		return false, err
	}
	run.Revision = 1
	result, err := s.db.Exec(`INSERT INTO execution_plan_runs (
			id, plan_id, project_id, run_mode, status, entries_json, revision, created_at, updated_at, completed_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO NOTHING`,
		run.ID, run.PlanID, run.ProjectID, run.RunMode, run.Status, string(entriesJSON),
		run.Revision, run.CreatedAt, run.UpdatedAt, run.CompletedAt)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows == 1, nil
}

// CompareAndSwapExecutionPlanRun persists one progress transition without
// allowing an older controller snapshot to overwrite a newer row. Identity and
// creation time are immutable after the create-only anchor is established.
func (s *SQLiteStore) CompareAndSwapExecutionPlanRun(run *ExecutionPlanRun, expectedRevision int64) (bool, error) {
	if expectedRevision < 1 {
		return false, fmt.Errorf("execution plan run expected revision must be positive")
	}
	entriesJSON, err := prepareExecutionPlanRunForWrite(run)
	if err != nil {
		return false, err
	}
	result, err := s.db.Exec(`UPDATE execution_plan_runs SET
			status = ?, entries_json = ?, updated_at = ?, completed_at = ?, revision = revision + 1
			WHERE id = ? AND revision = ?`,
		run.Status, string(entriesJSON), run.UpdatedAt, run.CompletedAt, run.ID, expectedRevision)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if rows == 1 {
		run.Revision = expectedRevision + 1
		return true, nil
	}
	return false, nil
}

func prepareExecutionPlanRunForWrite(run *ExecutionPlanRun) ([]byte, error) {
	if run == nil {
		return nil, fmt.Errorf("execution plan run is required")
	}
	if run.ID == "" {
		return nil, fmt.Errorf("execution plan run id is required")
	}
	run.ProjectID = normalizeProjectID(run.ProjectID)
	if run.RunMode == "" {
		run.RunMode = "sequential"
	}
	if run.Status == "" {
		run.Status = "running"
	}
	now := time.Now()
	if run.CreatedAt.IsZero() {
		run.CreatedAt = now
	}
	if run.Revision < 1 {
		run.Revision = 1
	}
	run.UpdatedAt = now
	entriesJSON, err := json.Marshal(run.Entries)
	if err != nil {
		return nil, fmt.Errorf("marshal execution plan run entries: %w", err)
	}
	return entriesJSON, nil
}

const executionPlanRunSelect = `SELECT id, COALESCE(plan_id, ''), COALESCE(project_id, ''),
	COALESCE(run_mode, 'sequential'), COALESCE(status, 'running'), COALESCE(entries_json, ''),
	COALESCE(revision, 1), created_at, updated_at, completed_at FROM execution_plan_runs`

func scanExecutionPlanRun(row capacityRowScanner) (*ExecutionPlanRun, error) {
	var run ExecutionPlanRun
	var entriesJSON string
	if err := row.Scan(&run.ID, &run.PlanID, &run.ProjectID, &run.RunMode, &run.Status,
		&entriesJSON, &run.Revision, &run.CreatedAt, &run.UpdatedAt, &run.CompletedAt); err != nil {
		return nil, err
	}
	if entriesJSON != "" && entriesJSON != "null" {
		if err := json.Unmarshal([]byte(entriesJSON), &run.Entries); err != nil {
			return nil, fmt.Errorf("unmarshal execution plan run entries: %w", err)
		}
	}
	if run.Entries == nil {
		run.Entries = []ExecutionPlanRunEntry{}
	}
	return &run, nil
}

func (s *SQLiteStore) GetExecutionPlanRun(id string) (*ExecutionPlanRun, error) {
	return scanExecutionPlanRun(s.db.QueryRow(executionPlanRunSelect+` WHERE id = ?`, id))
}

func (s *SQLiteStore) ListExecutionPlanRuns(planID string, limit int, offset int) ([]*ExecutionPlanRun, error) {
	query := executionPlanRunSelect
	args := []any{}
	if planID != "" {
		query += ` WHERE plan_id = ?`
		args = append(args, planID)
	}
	query += ` ORDER BY created_at DESC`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
		if offset > 0 {
			query += ` OFFSET ?`
			args = append(args, offset)
		}
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := make([]*ExecutionPlanRun, 0)
	for rows.Next() {
		run, err := scanExecutionPlanRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

// ListRunningExecutionPlanRuns returns only non-terminal sequential plan runs.
// Recovery uses this narrow query at boot/failover so its cost is proportional
// to live orchestration rather than the complete retained run history.
func (s *SQLiteStore) ListRunningExecutionPlanRuns() ([]*ExecutionPlanRun, error) {
	rows, err := s.db.Query(executionPlanRunSelect+` WHERE run_mode = ? AND status = ? ORDER BY created_at ASC`, "sequential", "running")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := make([]*ExecutionPlanRun, 0)
	for rows.Next() {
		run, err := scanExecutionPlanRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}
