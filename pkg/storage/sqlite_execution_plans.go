package storage

// SQLite store — execution plans. Entries are persisted inline as JSON. The same
// SQL serves the PostgreSQL backend via rewriteSQL.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *SQLiteStore) SaveExecutionPlan(plan *ExecutionPlan) error {
	return saveExecutionPlanWithExecutor(s.db, plan)
}

func saveExecutionPlanWithExecutor(executor testSaveExecutor, plan *ExecutionPlan) error {
	entriesJSON, err := prepareExecutionPlanForWrite(plan)
	if err != nil {
		return err
	}
	if plan.Revision <= 0 {
		plan.Revision = 1
	}
	result, err := executor.Exec(`INSERT INTO execution_plans (
			id, project_id, revision, name, description, entries_json, run_mode, timeslot_id, status, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
			name=excluded.name, description=excluded.description,
			entries_json=excluded.entries_json, run_mode=excluded.run_mode, timeslot_id=excluded.timeslot_id,
			status=excluded.status, updated_at=excluded.updated_at, revision=execution_plans.revision+1
			WHERE COALESCE(NULLIF(execution_plans.project_id, ''), ?) = excluded.project_id`,
		plan.ID, plan.ProjectID, plan.Revision, plan.Name, plan.Description, string(entriesJSON), plan.RunMode,
		plan.TimeslotID, plan.Status, plan.CreatedAt, plan.UpdatedAt, DefaultProjectID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("execution plan %q belongs to a different project", plan.ID)
	}
	return nil
}

func prepareExecutionPlanForWrite(plan *ExecutionPlan) ([]byte, error) {
	if plan == nil {
		return nil, fmt.Errorf("execution plan is required")
	}
	if strings.TrimSpace(plan.ID) == "" {
		return nil, fmt.Errorf("execution plan id is required")
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
		return nil, fmt.Errorf("marshal execution plan entries: %w", err)
	}
	return entriesJSON, nil
}

func (s *SQLiteStore) CreateExecutionPlanIfAbsent(plan *ExecutionPlan) (bool, error) {
	return createExecutionPlanIfAbsentWithExecutor(s.db, plan)
}

func createExecutionPlanIfAbsentWithExecutor(executor testSaveExecutor, plan *ExecutionPlan) (bool, error) {
	entriesJSON, err := prepareExecutionPlanForWrite(plan)
	if err != nil {
		return false, err
	}
	plan.Revision = 1
	result, err := executor.Exec(`INSERT INTO execution_plans (
			id, project_id, revision, name, description, entries_json, run_mode, timeslot_id, status, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO NOTHING`,
		plan.ID, plan.ProjectID, plan.Revision, plan.Name, plan.Description, string(entriesJSON), plan.RunMode,
		plan.TimeslotID, plan.Status, plan.CreatedAt, plan.UpdatedAt)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

func (s *SQLiteStore) CompareAndSwapExecutionPlan(plan *ExecutionPlan, expectedRevision int64) (bool, error) {
	return compareAndSwapExecutionPlanWithExecutor(s.db, plan, expectedRevision)
}

func compareAndSwapExecutionPlanWithExecutor(executor testSaveExecutor, plan *ExecutionPlan, expectedRevision int64) (bool, error) {
	if expectedRevision <= 0 {
		return false, fmt.Errorf("expected execution-plan revision must be positive")
	}
	entriesJSON, err := prepareExecutionPlanForWrite(plan)
	if err != nil {
		return false, err
	}
	result, err := executor.Exec(`UPDATE execution_plans SET
			name = ?, description = ?, entries_json = ?, run_mode = ?,
			timeslot_id = ?, status = ?, updated_at = ?, revision = revision + 1
			WHERE id = ? AND revision = ? AND COALESCE(NULLIF(project_id, ''), ?) = ?`,
		plan.Name, plan.Description, string(entriesJSON), plan.RunMode,
		plan.TimeslotID, plan.Status, plan.UpdatedAt, plan.ID, expectedRevision, DefaultProjectID, plan.ProjectID)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected == 1 {
		plan.Revision = expectedRevision + 1
	}
	return affected == 1, nil
}

func (s *SQLiteStore) SaveExecutionPlanIfLeaseTermHeld(plan *ExecutionPlan, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error) {
	tx, err := s.beginExecutionLeaseTermTransaction(leaseName, leaseOwnerID, leaseGeneration)
	if err != nil || tx == nil {
		return false, err
	}
	defer tx.Rollback()
	if err := saveExecutionPlanWithExecutor(tx, plan); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *SQLiteStore) CreateExecutionPlanIfAbsentAndLeaseTermHeld(plan *ExecutionPlan, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error) {
	tx, err := s.beginExecutionLeaseTermTransaction(leaseName, leaseOwnerID, leaseGeneration)
	if err != nil || tx == nil {
		return false, err
	}
	defer tx.Rollback()
	created, err := createExecutionPlanIfAbsentWithExecutor(tx, plan)
	if err != nil || !created {
		return created, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *SQLiteStore) CompareAndSwapExecutionPlanIfLeaseTermHeld(plan *ExecutionPlan, expectedRevision int64, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error) {
	tx, err := s.beginExecutionLeaseTermTransaction(leaseName, leaseOwnerID, leaseGeneration)
	if err != nil || tx == nil {
		return false, err
	}
	defer tx.Rollback()
	updated, err := compareAndSwapExecutionPlanWithExecutor(tx, plan, expectedRevision)
	if err != nil || !updated {
		return updated, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

const executionPlanSelect = `SELECT id, COALESCE(project_id, ''), COALESCE(revision, 1), name, COALESCE(description, ''),
	COALESCE(entries_json, ''), COALESCE(run_mode, 'parallel'), timeslot_id, COALESCE(status, 'draft'),
	created_at, updated_at FROM execution_plans`

func scanExecutionPlan(row capacityRowScanner) (*ExecutionPlan, error) {
	var plan ExecutionPlan
	var entriesJSON string
	if err := row.Scan(&plan.ID, &plan.ProjectID, &plan.Revision, &plan.Name, &plan.Description, &entriesJSON,
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

type executionPlanDeleteExecutor interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
}

func deleteExecutionPlanGuardedWithExecutor(executor executionPlanDeleteExecutor, id string) (ExecutionPlanDeleteResult, error) {
	var exists int
	if err := executor.QueryRow(`SELECT COUNT(*) FROM execution_plans WHERE id = ?`, id).Scan(&exists); err != nil {
		return "", err
	}
	if exists == 0 {
		return ExecutionPlanDeleteMissing, nil
	}
	var activeRuns int
	if err := executor.QueryRow(`SELECT COUNT(*) FROM execution_plan_runs
		WHERE plan_id = ? AND LOWER(COALESCE(status, '')) NOT IN ('completed', 'halted', 'failed')`, id).Scan(&activeRuns); err != nil {
		return "", err
	}
	if activeRuns > 0 {
		return ExecutionPlanDeleteBlockedByActiveRun, nil
	}
	var materializing int
	if err := executor.QueryRow(`SELECT COUNT(*) FROM execution_plan_schedule_occurrences
		WHERE plan_id = ? AND LOWER(COALESCE(status, '')) = 'materializing'`, id).Scan(&materializing); err != nil {
		return "", err
	}
	if materializing > 0 {
		return ExecutionPlanDeleteBlockedByMaterializingOccurrence, nil
	}
	if _, err := executor.Exec(`DELETE FROM execution_plan_schedule_occurrences WHERE schedule_id IN (SELECT id FROM execution_plan_schedules WHERE plan_id = ?)`, id); err != nil {
		return "", err
	}
	if _, err := executor.Exec(`DELETE FROM execution_plan_schedules WHERE plan_id = ?`, id); err != nil {
		return "", err
	}
	if _, err := executor.Exec(`DELETE FROM execution_plan_approvals WHERE plan_id = ?`, id); err != nil {
		return "", err
	}
	result, err := executor.Exec(`DELETE FROM execution_plans WHERE id = ?`, id)
	if err != nil {
		return "", err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return "", err
	}
	if affected != 1 {
		return ExecutionPlanDeleteMissing, nil
	}
	return ExecutionPlanDeleteDeleted, nil
}

func (s *SQLiteStore) DeleteExecutionPlanGuarded(id string) (ExecutionPlanDeleteResult, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("execution plan id is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	result, err := deleteExecutionPlanGuardedWithExecutor(tx, id)
	if err != nil {
		return "", err
	}
	if result != ExecutionPlanDeleteDeleted {
		return result, nil
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return result, nil
}

func (s *SQLiteStore) DeleteExecutionPlanGuardedIfLeaseTermHeld(id, leaseName, leaseOwnerID string, leaseGeneration uint64) (ExecutionPlanDeleteResult, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("execution plan id is required")
	}
	tx, err := s.beginExecutionLeaseTermTransaction(leaseName, leaseOwnerID, leaseGeneration)
	if err != nil || tx == nil {
		return "", err
	}
	defer tx.Rollback()
	result, err := deleteExecutionPlanGuardedWithExecutor(tx, id)
	if err != nil {
		return "", err
	}
	if result != ExecutionPlanDeleteDeleted {
		return result, nil
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return result, nil
}

func (s *SQLiteStore) DeleteExecutionPlanIfLeaseTermHeld(id, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return false, fmt.Errorf("execution plan id is required")
	}
	tx, err := s.beginExecutionLeaseTermTransaction(leaseName, leaseOwnerID, leaseGeneration)
	if err != nil || tx == nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM execution_plan_schedule_occurrences WHERE schedule_id IN (SELECT id FROM execution_plan_schedules WHERE plan_id = ?)`, id); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`DELETE FROM execution_plan_schedules WHERE plan_id = ?`, id); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`DELETE FROM execution_plan_approvals WHERE plan_id = ?`, id); err != nil {
		return false, err
	}
	result, err := tx.Exec(`DELETE FROM execution_plans WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return affected == 1, nil
}

func (s *SQLiteStore) SaveExecutionPlanRun(run *ExecutionPlanRun) error {
	entriesJSON, err := prepareExecutionPlanRunForWrite(run)
	if err != nil {
		return err
	}
	if err := bindExecutionPlanRunParent(s.db, run); err != nil {
		return err
	}
	result, err := s.db.Exec(`INSERT INTO execution_plan_runs (
			id, plan_id, project_id, run_mode, status, entries_json, transition_token, revision, created_at, updated_at, completed_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
			run_mode=excluded.run_mode,
			status=excluded.status, entries_json=excluded.entries_json, updated_at=excluded.updated_at,
			completed_at=excluded.completed_at, transition_token=excluded.transition_token,
			revision=execution_plan_runs.revision+1
			WHERE execution_plan_runs.plan_id=excluded.plan_id AND execution_plan_runs.project_id=excluded.project_id`,
		run.ID, run.PlanID, run.ProjectID, run.RunMode, run.Status, string(entriesJSON),
		run.TransitionToken, run.Revision, run.CreatedAt, run.UpdatedAt, run.CompletedAt)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("execution plan run %q belongs to another plan", run.ID)
	}
	return nil
}

// CreateExecutionPlanRunIfAbsent atomically establishes the durable side-effect
// boundary for one materialization. Only the caller that inserts the RunID may
// proceed to create tests; a conflicting caller must reload the existing row.
func (s *SQLiteStore) CreateExecutionPlanRunIfAbsent(run *ExecutionPlanRun) (bool, error) {
	return createExecutionPlanRunIfAbsentWithExecutor(s.db, run)
}

func createExecutionPlanRunIfAbsentWithExecutor(executor testSaveExecutor, run *ExecutionPlanRun) (bool, error) {
	entriesJSON, err := prepareExecutionPlanRunForWrite(run)
	if err != nil {
		return false, err
	}
	if err := bindExecutionPlanRunParent(executor, run); err != nil {
		return false, err
	}
	run.Revision = 1
	result, err := executor.Exec(`INSERT INTO execution_plan_runs (
			id, plan_id, project_id, run_mode, status, entries_json, transition_token, revision, created_at, updated_at, completed_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO NOTHING`,
		run.ID, run.PlanID, run.ProjectID, run.RunMode, run.Status, string(entriesJSON),
		run.TransitionToken, run.Revision, run.CreatedAt, run.UpdatedAt, run.CompletedAt)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows == 1, nil
}

// CreateExecutionPlanRunIfAbsentAndLeaseTermHeld creates the run anchor only
// while the supplied execution-leadership term is still current. The lease row
// is locked and checked in the same transaction as the insert so a controller
// from term N cannot cross the side-effect boundary after term N+1 takes over.
func (s *SQLiteStore) CreateExecutionPlanRunIfAbsentAndLeaseTermHeld(run *ExecutionPlanRun, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error) {
	leaseName = strings.TrimSpace(leaseName)
	leaseOwnerID = strings.TrimSpace(leaseOwnerID)
	if leaseName == "" || leaseOwnerID == "" || leaseGeneration == 0 {
		return false, fmt.Errorf("lease name, owner token, and positive generation are required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	held, err := schedulerLeaseTermHeldInTransaction(tx, s.db.dialect, leaseName, leaseOwnerID, leaseGeneration)
	if err != nil || !held {
		return false, err
	}
	created, err := createExecutionPlanRunIfAbsentWithExecutor(tx, run)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return created, nil
}

// CompareAndSwapExecutionPlanRun persists one progress transition without
// allowing an older controller snapshot to overwrite a newer row. Identity and
// creation time are immutable after the create-only anchor is established.
func (s *SQLiteStore) CompareAndSwapExecutionPlanRun(run *ExecutionPlanRun, expectedRevision int64) (bool, error) {
	return compareAndSwapExecutionPlanRunWithExecutor(s.db, run, expectedRevision)
}

func compareAndSwapExecutionPlanRunWithExecutor(executor testSaveExecutor, run *ExecutionPlanRun, expectedRevision int64) (bool, error) {
	if expectedRevision < 1 {
		return false, fmt.Errorf("execution plan run expected revision must be positive")
	}
	entriesJSON, err := prepareExecutionPlanRunForWrite(run)
	if err != nil {
		return false, err
	}
	if err := bindExecutionPlanRunParent(executor, run); err != nil {
		return false, err
	}
	result, err := executor.Exec(`UPDATE execution_plan_runs SET
			status = ?, entries_json = ?, updated_at = ?, completed_at = ?, transition_token = ?, revision = revision + 1
			WHERE id = ? AND plan_id = ? AND project_id = ? AND revision = ?`,
		run.Status, string(entriesJSON), run.UpdatedAt, run.CompletedAt, run.TransitionToken, run.ID, run.PlanID, run.ProjectID, expectedRevision)
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

// CompareAndSwapExecutionPlanRunIfLeaseTermHeld combines the run revision CAS
// with the durable execution-lease term check in one transaction.
func (s *SQLiteStore) CompareAndSwapExecutionPlanRunIfLeaseTermHeld(run *ExecutionPlanRun, expectedRevision int64, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error) {
	leaseName = strings.TrimSpace(leaseName)
	leaseOwnerID = strings.TrimSpace(leaseOwnerID)
	if leaseName == "" || leaseOwnerID == "" || leaseGeneration == 0 {
		return false, fmt.Errorf("lease name, owner token, and positive generation are required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	held, err := schedulerLeaseTermHeldInTransaction(tx, s.db.dialect, leaseName, leaseOwnerID, leaseGeneration)
	if err != nil || !held {
		return false, err
	}
	updated, err := compareAndSwapExecutionPlanRunWithExecutor(tx, run, expectedRevision)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return updated, nil
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

func bindExecutionPlanRunParent(executor testSaveExecutor, run *ExecutionPlanRun) error {
	if run == nil {
		return fmt.Errorf("execution plan run is required")
	}
	run.PlanID = strings.TrimSpace(run.PlanID)
	if run.PlanID == "" {
		return fmt.Errorf("execution plan run plan_id is required")
	}

	// Materialization normally follows a durable plan, in which case that plan
	// is the authority for the tenant boundary. Some embedders intentionally
	// run transient, in-memory plans, so absence alone cannot reject a new run.
	var planProjectID string
	planFound := true
	if err := executor.QueryRow(`SELECT COALESCE(project_id, '') FROM execution_plans WHERE id = ?`, run.PlanID).Scan(&planProjectID); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		planFound = false
	} else {
		planProjectID = normalizeProjectID(planProjectID)
	}

	// Once a run ID exists, its plan and project are immutable even when the
	// parent plan was transient and has no durable row. This prevents an upsert
	// or CAS from relocating an existing run across aggregate/tenant boundaries.
	var existingPlanID, existingProjectID string
	err := executor.QueryRow(`SELECT COALESCE(plan_id, ''), COALESCE(project_id, '')
		FROM execution_plan_runs WHERE id = ?`, run.ID).Scan(&existingPlanID, &existingProjectID)
	switch {
	case err == nil:
		existingPlanID = strings.TrimSpace(existingPlanID)
		existingProjectID = normalizeProjectID(existingProjectID)
		if existingPlanID != run.PlanID {
			return fmt.Errorf("execution plan run %q belongs to another plan", run.ID)
		}
		if planFound && existingProjectID != planProjectID {
			return fmt.Errorf("execution plan run %q project does not match its plan", run.ID)
		}
		run.ProjectID = existingProjectID
	case errors.Is(err, sql.ErrNoRows):
		if planFound {
			run.ProjectID = planProjectID
		} else {
			run.ProjectID = normalizeProjectID(run.ProjectID)
		}
	default:
		return err
	}
	return nil
}

const executionPlanRunSelect = `SELECT id, COALESCE(plan_id, ''), COALESCE(project_id, ''),
	COALESCE(run_mode, 'sequential'), COALESCE(status, 'running'), COALESCE(entries_json, ''),
	COALESCE(transition_token, ''), COALESCE(revision, 1), created_at, updated_at, completed_at FROM execution_plan_runs`

func scanExecutionPlanRun(row capacityRowScanner) (*ExecutionPlanRun, error) {
	var run ExecutionPlanRun
	var entriesJSON string
	if err := row.Scan(&run.ID, &run.PlanID, &run.ProjectID, &run.RunMode, &run.Status,
		&entriesJSON, &run.TransitionToken, &run.Revision, &run.CreatedAt, &run.UpdatedAt, &run.CompletedAt); err != nil {
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

// ListRunningExecutionPlanRuns returns non-terminal plan runs of every mode.
// Recovery uses this narrow query at boot/failover so its cost is proportional
// to live orchestration rather than the complete retained run history; callers
// then apply mode-specific sequential-resume or parallel-cleanup behavior.
func (s *SQLiteStore) ListRunningExecutionPlanRuns() ([]*ExecutionPlanRun, error) {
	rows, err := s.db.Query(executionPlanRunSelect+` WHERE status = ? ORDER BY created_at ASC`, "running")
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
