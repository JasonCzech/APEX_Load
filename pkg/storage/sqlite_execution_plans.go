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
	_, err := s.db.Exec(`DELETE FROM execution_plans WHERE id = ?`, id)
	return err
}
