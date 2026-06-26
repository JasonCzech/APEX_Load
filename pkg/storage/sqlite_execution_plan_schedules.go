package storage

// SQLite store — execution-plan schedules + their occurrences. Structurally a
// sibling of sqlite_schedules.go (scenario schedules); the cron/timezone/missed-
// run logic is shared at the api layer, the persistence is parallel.

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

const executionPlanScheduleColumns = `id, plan_id, COALESCE(project_id, ''), COALESCE(revision, 1), name, cron_expr,
	COALESCE(timezone, ''), COALESCE(run_name_template, ''), COALESCE(enabled, 1), COALESCE(missed_run_policy, ''),
	COALESCE(conflict_summary, ''), COALESCE(next_required_action, ''), COALESCE(last_run_run_id, ''),
	last_run_at, next_run_at, disabled_from, disabled_until, COALESCE(last_error, ''), created_at, updated_at
	FROM execution_plan_schedules`

func (s *SQLiteStore) SaveExecutionPlanSchedule(schedule *ExecutionPlanSchedule) error {
	if schedule == nil {
		return errors.New("execution plan schedule is required")
	}
	if strings.TrimSpace(schedule.ID) == "" {
		return errors.New("execution plan schedule id is required")
	}
	if strings.TrimSpace(schedule.PlanID) == "" {
		return errors.New("execution plan schedule plan_id is required")
	}
	if strings.TrimSpace(schedule.Name) == "" {
		return errors.New("execution plan schedule name is required")
	}
	if strings.TrimSpace(schedule.CronExpr) == "" {
		return errors.New("execution plan schedule cron_expr is required")
	}
	if schedule.CreatedAt.IsZero() {
		schedule.CreatedAt = time.Now()
	}
	if schedule.UpdatedAt.IsZero() {
		schedule.UpdatedAt = time.Now()
	}
	schedule.ProjectID = normalizeProjectID(schedule.ProjectID)
	if schedule.Revision < 1 {
		schedule.Revision = 1
	}
	_, err := s.db.Exec(`INSERT INTO execution_plan_schedules (id, plan_id, project_id, revision, name, cron_expr, timezone, run_name_template, enabled, missed_run_policy, conflict_summary, next_required_action, last_run_run_id, last_run_at, next_run_at, disabled_from, disabled_until, last_error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET plan_id=excluded.plan_id, project_id=excluded.project_id, revision=excluded.revision, name=excluded.name,
		cron_expr=excluded.cron_expr, timezone=excluded.timezone, run_name_template=excluded.run_name_template, enabled=excluded.enabled,
		missed_run_policy=excluded.missed_run_policy, conflict_summary=excluded.conflict_summary, next_required_action=excluded.next_required_action,
		last_run_run_id=excluded.last_run_run_id, last_run_at=excluded.last_run_at, next_run_at=excluded.next_run_at,
		disabled_from=excluded.disabled_from, disabled_until=excluded.disabled_until, last_error=excluded.last_error, updated_at=excluded.updated_at`,
		schedule.ID, schedule.PlanID, schedule.ProjectID, schedule.Revision, schedule.Name, schedule.CronExpr, schedule.Timezone, schedule.RunNameTemplate,
		schedule.Enabled, schedule.MissedRunPolicy, schedule.ConflictSummary, schedule.NextRequiredAction, schedule.LastRunRunID,
		schedule.LastRunAt, schedule.NextRunAt, schedule.DisabledFrom, schedule.DisabledUntil, schedule.LastError, schedule.CreatedAt, schedule.UpdatedAt)
	return err
}

func (s *SQLiteStore) GetExecutionPlanSchedule(id string) (*ExecutionPlanSchedule, error) {
	row := s.db.QueryRow(`SELECT `+executionPlanScheduleColumns+` WHERE id = ?`, id)
	return scanExecutionPlanSchedule(row)
}

func (s *SQLiteStore) ListExecutionPlanSchedules(planID string) ([]*ExecutionPlanSchedule, error) {
	rows, err := s.db.Query(`SELECT `+executionPlanScheduleColumns+` WHERE plan_id = ? ORDER BY created_at ASC`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectExecutionPlanSchedules(rows)
}

func (s *SQLiteStore) ListAllExecutionPlanSchedules() ([]*ExecutionPlanSchedule, error) {
	rows, err := s.db.Query(`SELECT ` + executionPlanScheduleColumns + ` ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectExecutionPlanSchedules(rows)
}

func (s *SQLiteStore) DeleteExecutionPlanSchedule(id string) error {
	_, err := s.db.Exec(`DELETE FROM execution_plan_schedules WHERE id = ?`, id)
	return err
}

func scanExecutionPlanSchedule(row scanner) (*ExecutionPlanSchedule, error) {
	var schedule ExecutionPlanSchedule
	var enabled any
	err := row.Scan(
		&schedule.ID, &schedule.PlanID, &schedule.ProjectID, &schedule.Revision, &schedule.Name, &schedule.CronExpr,
		&schedule.Timezone, &schedule.RunNameTemplate, &enabled, &schedule.MissedRunPolicy,
		&schedule.ConflictSummary, &schedule.NextRequiredAction, &schedule.LastRunRunID,
		&schedule.LastRunAt, &schedule.NextRunAt, &schedule.DisabledFrom, &schedule.DisabledUntil, &schedule.LastError, &schedule.CreatedAt, &schedule.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	schedule.Enabled = sqlBool(enabled)
	schedule.ProjectID = normalizeProjectID(schedule.ProjectID)
	if schedule.Revision < 1 {
		schedule.Revision = 1
	}
	return &schedule, nil
}

func collectExecutionPlanSchedules(rows *sql.Rows) ([]*ExecutionPlanSchedule, error) {
	schedules := make([]*ExecutionPlanSchedule, 0)
	for rows.Next() {
		schedule, err := scanExecutionPlanSchedule(rows)
		if err != nil {
			return nil, err
		}
		schedules = append(schedules, schedule)
	}
	return schedules, rows.Err()
}

const executionPlanScheduleOccurrenceColumns = `id, schedule_id, plan_id, COALESCE(project_id, ''), COALESCE(schedule_revision, 1),
	planned_at, materialized_at, status, COALESCE(reason_code, ''), COALESCE(run_id, ''), COALESCE(conflict_summary, ''), created_at, updated_at
	FROM execution_plan_schedule_occurrences`

func (s *SQLiteStore) SaveExecutionPlanScheduleOccurrence(occurrence *ExecutionPlanScheduleOccurrence) error {
	if occurrence == nil {
		return errors.New("execution plan schedule occurrence is required")
	}
	if strings.TrimSpace(occurrence.ID) == "" {
		return errors.New("execution plan schedule occurrence id is required")
	}
	if strings.TrimSpace(occurrence.ScheduleID) == "" {
		return errors.New("execution plan schedule occurrence schedule_id is required")
	}
	if occurrence.PlannedAt.IsZero() {
		return errors.New("execution plan schedule occurrence planned_at is required")
	}
	if strings.TrimSpace(occurrence.Status) == "" {
		return errors.New("execution plan schedule occurrence status is required")
	}
	if occurrence.CreatedAt.IsZero() {
		occurrence.CreatedAt = time.Now()
	}
	if occurrence.UpdatedAt.IsZero() {
		occurrence.UpdatedAt = time.Now()
	}
	occurrence.ProjectID = normalizeProjectID(occurrence.ProjectID)
	if occurrence.ScheduleRevision < 1 {
		occurrence.ScheduleRevision = 1
	}
	_, err := s.db.Exec(`INSERT INTO execution_plan_schedule_occurrences (id, schedule_id, plan_id, project_id, schedule_revision, planned_at, materialized_at, status, reason_code, run_id, conflict_summary, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(schedule_id, planned_at) DO UPDATE SET plan_id=excluded.plan_id, project_id=excluded.project_id,
		schedule_revision=excluded.schedule_revision, materialized_at=excluded.materialized_at, status=excluded.status,
		reason_code=excluded.reason_code, run_id=excluded.run_id, conflict_summary=excluded.conflict_summary, updated_at=excluded.updated_at`,
		occurrence.ID, occurrence.ScheduleID, occurrence.PlanID, occurrence.ProjectID, occurrence.ScheduleRevision, occurrence.PlannedAt, occurrence.MaterializedAt, occurrence.Status, occurrence.ReasonCode, occurrence.RunID, occurrence.ConflictSummary, occurrence.CreatedAt, occurrence.UpdatedAt)
	return err
}

func (s *SQLiteStore) GetExecutionPlanScheduleOccurrenceByScheduleAndPlannedAt(scheduleID string, plannedAt time.Time) (*ExecutionPlanScheduleOccurrence, error) {
	row := s.db.QueryRow(`SELECT `+executionPlanScheduleOccurrenceColumns+` WHERE schedule_id = ? AND planned_at = ?`, scheduleID, plannedAt)
	occurrence, err := scanExecutionPlanScheduleOccurrence(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return occurrence, err
}

func (s *SQLiteStore) ListExecutionPlanScheduleOccurrences(scheduleID string) ([]*ExecutionPlanScheduleOccurrence, error) {
	rows, err := s.db.Query(`SELECT `+executionPlanScheduleOccurrenceColumns+` WHERE schedule_id = ? ORDER BY planned_at DESC`, scheduleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectExecutionPlanScheduleOccurrences(rows)
}

func (s *SQLiteStore) ListAllExecutionPlanScheduleOccurrences() ([]*ExecutionPlanScheduleOccurrence, error) {
	rows, err := s.db.Query(`SELECT ` + executionPlanScheduleOccurrenceColumns + ` ORDER BY planned_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectExecutionPlanScheduleOccurrences(rows)
}

func scanExecutionPlanScheduleOccurrence(row scanner) (*ExecutionPlanScheduleOccurrence, error) {
	var occurrence ExecutionPlanScheduleOccurrence
	err := row.Scan(
		&occurrence.ID, &occurrence.ScheduleID, &occurrence.PlanID, &occurrence.ProjectID, &occurrence.ScheduleRevision,
		&occurrence.PlannedAt, &occurrence.MaterializedAt, &occurrence.Status, &occurrence.ReasonCode, &occurrence.RunID, &occurrence.ConflictSummary, &occurrence.CreatedAt, &occurrence.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	occurrence.ProjectID = normalizeProjectID(occurrence.ProjectID)
	if occurrence.ScheduleRevision < 1 {
		occurrence.ScheduleRevision = 1
	}
	return &occurrence, nil
}

func collectExecutionPlanScheduleOccurrences(rows *sql.Rows) ([]*ExecutionPlanScheduleOccurrence, error) {
	occurrences := make([]*ExecutionPlanScheduleOccurrence, 0)
	for rows.Next() {
		occurrence, err := scanExecutionPlanScheduleOccurrence(rows)
		if err != nil {
			return nil, err
		}
		occurrences = append(occurrences, occurrence)
	}
	return occurrences, rows.Err()
}
