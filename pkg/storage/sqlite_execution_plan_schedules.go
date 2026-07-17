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
	last_run_at, next_run_at, disabled_from, disabled_until, COALESCE(last_error, ''),
	COALESCE(actor_type, 'legacy'), COALESCE(actor_subject, 'controller:execution-plan-schedule-legacy'),
	COALESCE(actor_display_name, ''), created_at, updated_at
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
	normalizeExecutionPlanScheduleActor(schedule)
	if schedule.Revision < 1 {
		schedule.Revision = 1
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := bindExecutionPlanScheduleParent(tx, schedule); err != nil {
		return err
	}
	result, err := tx.Exec(`INSERT INTO execution_plan_schedules (id, plan_id, project_id, revision, name, cron_expr, timezone, run_name_template, enabled, missed_run_policy, conflict_summary, next_required_action, last_run_run_id, last_run_at, next_run_at, disabled_from, disabled_until, last_error, actor_type, actor_subject, actor_display_name, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET revision=excluded.revision, name=excluded.name,
		cron_expr=excluded.cron_expr, timezone=excluded.timezone, run_name_template=excluded.run_name_template, enabled=excluded.enabled,
		missed_run_policy=excluded.missed_run_policy, conflict_summary=excluded.conflict_summary, next_required_action=excluded.next_required_action,
		last_run_run_id=excluded.last_run_run_id, last_run_at=excluded.last_run_at, next_run_at=excluded.next_run_at,
		disabled_from=excluded.disabled_from, disabled_until=excluded.disabled_until, last_error=excluded.last_error,
		actor_type=excluded.actor_type, actor_subject=excluded.actor_subject, actor_display_name=excluded.actor_display_name,
		updated_at=excluded.updated_at
		WHERE execution_plan_schedules.plan_id=excluded.plan_id AND execution_plan_schedules.project_id=excluded.project_id`,
		schedule.ID, schedule.PlanID, schedule.ProjectID, schedule.Revision, schedule.Name, schedule.CronExpr, schedule.Timezone, schedule.RunNameTemplate,
		schedule.Enabled, schedule.MissedRunPolicy, schedule.ConflictSummary, schedule.NextRequiredAction, schedule.LastRunRunID,
		schedule.LastRunAt, schedule.NextRunAt, schedule.DisabledFrom, schedule.DisabledUntil, schedule.LastError,
		schedule.ActorType, schedule.ActorSubject, schedule.ActorDisplayName, schedule.CreatedAt, schedule.UpdatedAt)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return errors.New("execution plan schedule belongs to another plan or project")
	}
	return tx.Commit()
}

// CreateExecutionPlanScheduleIfAbsent creates a schedule without ever replacing
// an existing row. Operator create paths use this instead of the legacy UPSERT
// so a concurrent writer cannot be silently overwritten.
func (s *SQLiteStore) CreateExecutionPlanScheduleIfAbsent(schedule *ExecutionPlanSchedule) (bool, error) {
	return createExecutionPlanScheduleIfAbsentWithExecutor(s.db, schedule)
}

func createExecutionPlanScheduleIfAbsentWithExecutor(executor testSaveExecutor, schedule *ExecutionPlanSchedule) (bool, error) {
	if err := prepareExecutionPlanSchedule(schedule); err != nil {
		return false, err
	}
	if err := bindExecutionPlanScheduleParent(executor, schedule); err != nil {
		return false, err
	}
	result, err := executor.Exec(`INSERT INTO execution_plan_schedules (id, plan_id, project_id, revision, name, cron_expr, timezone, run_name_template, enabled, missed_run_policy, conflict_summary, next_required_action, last_run_run_id, last_run_at, next_run_at, disabled_from, disabled_until, last_error, actor_type, actor_subject, actor_display_name, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING`,
		schedule.ID, schedule.PlanID, schedule.ProjectID, schedule.Revision, schedule.Name, schedule.CronExpr, schedule.Timezone, schedule.RunNameTemplate,
		schedule.Enabled, schedule.MissedRunPolicy, schedule.ConflictSummary, schedule.NextRequiredAction, schedule.LastRunRunID,
		schedule.LastRunAt, schedule.NextRunAt, schedule.DisabledFrom, schedule.DisabledUntil, schedule.LastError,
		schedule.ActorType, schedule.ActorSubject, schedule.ActorDisplayName, schedule.CreatedAt, schedule.UpdatedAt)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected == 1, nil
}

func (s *SQLiteStore) CreateExecutionPlanScheduleIfAbsentAndLeaseTermHeld(schedule *ExecutionPlanSchedule, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error) {
	tx, err := s.beginExecutionLeaseTermTransaction(leaseName, leaseOwnerID, leaseGeneration)
	if err != nil || tx == nil {
		return false, err
	}
	defer tx.Rollback()
	created, err := createExecutionPlanScheduleIfAbsentWithExecutor(tx, schedule)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return created, nil
}

// UpdateExecutionPlanScheduleIfRevision updates an existing schedule only when
// its durable revision still matches the caller's snapshot. It never inserts,
// which also prevents a stale scheduler from resurrecting a deleted row.
func (s *SQLiteStore) UpdateExecutionPlanScheduleIfRevision(schedule *ExecutionPlanSchedule, expectedRevision int) (bool, error) {
	return updateExecutionPlanScheduleIfRevisionWithExecutor(s.db, schedule, expectedRevision)
}

func updateExecutionPlanScheduleIfRevisionWithExecutor(executor testSaveExecutor, schedule *ExecutionPlanSchedule, expectedRevision int) (bool, error) {
	if err := prepareExecutionPlanSchedule(schedule); err != nil {
		return false, err
	}
	if expectedRevision < 1 {
		return false, errors.New("expected execution plan schedule revision must be positive")
	}
	if err := bindExecutionPlanScheduleParent(executor, schedule); err != nil {
		return false, err
	}
	nextRevision := expectedRevision + 1
	result, err := executor.Exec(`UPDATE execution_plan_schedules SET revision=?, name=?, cron_expr=?, timezone=?, run_name_template=?, enabled=?, missed_run_policy=?, conflict_summary=?, next_required_action=?, last_run_run_id=?, last_run_at=?, next_run_at=?, disabled_from=?, disabled_until=?, last_error=?, actor_type=?, actor_subject=?, actor_display_name=?, updated_at=? WHERE id=? AND plan_id=? AND project_id=? AND COALESCE(revision, 1)=?`,
		nextRevision, schedule.Name, schedule.CronExpr, schedule.Timezone, schedule.RunNameTemplate,
		schedule.Enabled, schedule.MissedRunPolicy, schedule.ConflictSummary, schedule.NextRequiredAction, schedule.LastRunRunID,
		schedule.LastRunAt, schedule.NextRunAt, schedule.DisabledFrom, schedule.DisabledUntil, schedule.LastError,
		schedule.ActorType, schedule.ActorSubject, schedule.ActorDisplayName, schedule.UpdatedAt,
		schedule.ID, schedule.PlanID, schedule.ProjectID, expectedRevision)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected != 1 {
		return false, nil
	}
	schedule.Revision = nextRevision
	return true, nil
}

func (s *SQLiteStore) UpdateExecutionPlanScheduleIfRevisionAndLeaseTermHeld(schedule *ExecutionPlanSchedule, expectedRevision int, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error) {
	tx, err := s.beginExecutionLeaseTermTransaction(leaseName, leaseOwnerID, leaseGeneration)
	if err != nil || tx == nil {
		return false, err
	}
	defer tx.Rollback()
	updated, err := updateExecutionPlanScheduleIfRevisionWithExecutor(tx, schedule, expectedRevision)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return updated, nil
}

// beginExecutionLeaseTermTransaction validates and locks the durable execution
// lease. A nil transaction with a nil error means the supplied term is fenced.
func (s *SQLiteStore) beginExecutionLeaseTermTransaction(leaseName, leaseOwnerID string, leaseGeneration uint64) (*storeTx, error) {
	leaseName = strings.TrimSpace(leaseName)
	leaseOwnerID = strings.TrimSpace(leaseOwnerID)
	if leaseName == "" || leaseOwnerID == "" || leaseGeneration == 0 {
		return nil, errors.New("lease name, owner token, and positive generation are required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	held, err := schedulerLeaseTermHeldInTransaction(tx, s.db.dialect, leaseName, leaseOwnerID, leaseGeneration)
	if err != nil || !held {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func prepareExecutionPlanSchedule(schedule *ExecutionPlanSchedule) error {
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
	normalizeExecutionPlanScheduleActor(schedule)
	if schedule.Revision < 1 {
		schedule.Revision = 1
	}
	return nil
}

func normalizeExecutionPlanScheduleActor(schedule *ExecutionPlanSchedule) {
	if schedule == nil {
		return
	}
	schedule.ActorType = strings.ToLower(strings.TrimSpace(schedule.ActorType))
	if schedule.ActorType == "" {
		schedule.ActorType = "system"
	}
	schedule.ActorSubject = strings.TrimSpace(schedule.ActorSubject)
	if schedule.ActorSubject == "" {
		schedule.ActorSubject = "controller:execution-plan-schedule"
	}
	schedule.ActorDisplayName = strings.TrimSpace(schedule.ActorDisplayName)
}

func bindExecutionPlanScheduleParent(executor testSaveExecutor, schedule *ExecutionPlanSchedule) error {
	var projectID string
	if err := executor.QueryRow(`SELECT COALESCE(project_id, '') FROM execution_plans WHERE id = ?`, schedule.PlanID).Scan(&projectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("execution plan not found: " + schedule.PlanID)
		}
		return err
	}
	schedule.ProjectID = normalizeProjectID(projectID)
	var existingPlanID string
	if err := executor.QueryRow(`SELECT plan_id FROM execution_plan_schedules WHERE id = ?`, schedule.ID).Scan(&existingPlanID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	if existingPlanID != schedule.PlanID {
		return errors.New("execution plan schedule belongs to another plan")
	}
	return nil
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
	result, err := s.DeleteExecutionPlanScheduleGuarded(id)
	if err != nil {
		return err
	}
	if result == ScheduleDeleteBlockedByActiveOccurrence {
		return errors.New("execution plan schedule has a nonterminal occurrence")
	}
	return nil
}

type executionPlanScheduleDeleteExecutor interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
}

func deleteExecutionPlanScheduleGuardedWithExecutor(executor executionPlanScheduleDeleteExecutor, id string) (ScheduleDeleteResult, error) {
	locked, err := executor.Exec(`UPDATE execution_plan_schedules SET updated_at = updated_at WHERE id = ?`, id)
	if err != nil {
		return "", err
	}
	affected, err := locked.RowsAffected()
	if err != nil {
		return "", err
	}
	if affected == 0 {
		return ScheduleDeleteMissing, nil
	}
	var active int
	if err := executor.QueryRow(`SELECT COUNT(*) FROM execution_plan_schedule_occurrences
		WHERE schedule_id = ? AND LOWER(TRIM(COALESCE(status, ''))) NOT IN ('materialized', 'failed', 'blocked', 'skipped')`, id).Scan(&active); err != nil {
		return "", err
	}
	if active > 0 {
		return ScheduleDeleteBlockedByActiveOccurrence, nil
	}
	if _, err := executor.Exec(`DELETE FROM execution_plan_schedule_occurrences WHERE schedule_id = ?`, id); err != nil {
		return "", err
	}
	result, err := executor.Exec(`DELETE FROM execution_plan_schedules WHERE id = ?`, id)
	if err != nil {
		return "", err
	}
	affected, err = result.RowsAffected()
	if err != nil {
		return "", err
	}
	if affected != 1 {
		return ScheduleDeleteMissing, nil
	}
	return ScheduleDeleteDeleted, nil
}

func (s *SQLiteStore) DeleteExecutionPlanScheduleGuarded(id string) (ScheduleDeleteResult, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", errors.New("execution plan schedule id is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	result, err := deleteExecutionPlanScheduleGuardedWithExecutor(tx, id)
	if err != nil {
		return "", err
	}
	if result != ScheduleDeleteDeleted {
		return result, nil
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return result, nil
}

func (s *SQLiteStore) DeleteExecutionPlanScheduleIfLeaseTermHeld(id, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error) {
	result, err := s.DeleteExecutionPlanScheduleGuardedIfLeaseTermHeld(id, leaseName, leaseOwnerID, leaseGeneration)
	return result == ScheduleDeleteDeleted, err
}

func (s *SQLiteStore) DeleteExecutionPlanScheduleGuardedIfLeaseTermHeld(id, leaseName, leaseOwnerID string, leaseGeneration uint64) (ScheduleDeleteResult, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", errors.New("execution plan schedule id is required")
	}
	tx, err := s.beginExecutionLeaseTermTransaction(leaseName, leaseOwnerID, leaseGeneration)
	if err != nil || tx == nil {
		return "", err
	}
	defer tx.Rollback()
	result, err := deleteExecutionPlanScheduleGuardedWithExecutor(tx, id)
	if err != nil {
		return "", err
	}
	if result != ScheduleDeleteDeleted {
		return result, nil
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return result, nil
}

func scanExecutionPlanSchedule(row scanner) (*ExecutionPlanSchedule, error) {
	var schedule ExecutionPlanSchedule
	var enabled any
	err := row.Scan(
		&schedule.ID, &schedule.PlanID, &schedule.ProjectID, &schedule.Revision, &schedule.Name, &schedule.CronExpr,
		&schedule.Timezone, &schedule.RunNameTemplate, &enabled, &schedule.MissedRunPolicy,
		&schedule.ConflictSummary, &schedule.NextRequiredAction, &schedule.LastRunRunID,
		&schedule.LastRunAt, &schedule.NextRunAt, &schedule.DisabledFrom, &schedule.DisabledUntil, &schedule.LastError,
		&schedule.ActorType, &schedule.ActorSubject, &schedule.ActorDisplayName, &schedule.CreatedAt, &schedule.UpdatedAt,
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
	COALESCE(revision, 1), COALESCE(transition_token, ''), planned_at, materialized_at, status,
	COALESCE(reason_code, ''), COALESCE(run_id, ''), COALESCE(conflict_summary, ''),
	COALESCE(actor_type, 'legacy'), COALESCE(actor_subject, 'controller:execution-plan-schedule-occurrence-legacy'),
	COALESCE(actor_display_name, ''), created_at, updated_at
	FROM execution_plan_schedule_occurrences`

func (s *SQLiteStore) SaveExecutionPlanScheduleOccurrence(occurrence *ExecutionPlanScheduleOccurrence) error {
	expectedRevision := int64(1)
	if occurrence != nil && occurrence.Revision > 0 {
		expectedRevision = occurrence.Revision
	}
	created, err := s.CreateExecutionPlanScheduleOccurrenceIfAbsent(occurrence)
	if err != nil || created {
		return err
	}
	occurrence.Revision = expectedRevision
	updated, err := s.CompareAndSwapExecutionPlanScheduleOccurrence(occurrence, expectedRevision)
	if err != nil {
		return err
	}
	if !updated {
		return errors.New("execution plan schedule occurrence changed or was deleted concurrently")
	}
	return nil
}

func prepareExecutionPlanScheduleOccurrence(occurrence *ExecutionPlanScheduleOccurrence) error {
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
	normalizeExecutionPlanScheduleOccurrenceActor(occurrence)
	if occurrence.ScheduleRevision < 1 {
		occurrence.ScheduleRevision = 1
	}
	if occurrence.Revision < 1 {
		occurrence.Revision = 1
	}
	return nil
}

func normalizeExecutionPlanScheduleOccurrenceActor(occurrence *ExecutionPlanScheduleOccurrence) {
	if occurrence == nil {
		return
	}
	occurrence.ActorType = strings.ToLower(strings.TrimSpace(occurrence.ActorType))
	if occurrence.ActorType == "" {
		occurrence.ActorType = "legacy"
	}
	occurrence.ActorSubject = strings.TrimSpace(occurrence.ActorSubject)
	if occurrence.ActorSubject == "" {
		occurrence.ActorSubject = "controller:execution-plan-schedule-occurrence-legacy"
	}
	occurrence.ActorDisplayName = strings.TrimSpace(occurrence.ActorDisplayName)
}

func (s *SQLiteStore) CreateExecutionPlanScheduleOccurrenceIfAbsent(occurrence *ExecutionPlanScheduleOccurrence) (bool, error) {
	return createExecutionPlanScheduleOccurrenceIfAbsentWithExecutor(s.db, occurrence)
}

func createExecutionPlanScheduleOccurrenceIfAbsentWithExecutor(executor testSaveExecutor, occurrence *ExecutionPlanScheduleOccurrence) (bool, error) {
	if err := bindExecutionPlanScheduleOccurrenceParent(executor, occurrence); err != nil {
		return false, err
	}
	if err := prepareExecutionPlanScheduleOccurrence(occurrence); err != nil {
		return false, err
	}
	occurrence.Revision = 1
	result, err := executor.Exec(`INSERT INTO execution_plan_schedule_occurrences (id, schedule_id, plan_id, project_id, schedule_revision, revision, transition_token, planned_at, materialized_at, status, reason_code, run_id, conflict_summary, actor_type, actor_subject, actor_display_name, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(schedule_id, planned_at) DO NOTHING`,
		occurrence.ID, occurrence.ScheduleID, occurrence.PlanID, occurrence.ProjectID, occurrence.ScheduleRevision, occurrence.Revision, occurrence.TransitionToken,
		occurrence.PlannedAt, occurrence.MaterializedAt, occurrence.Status, occurrence.ReasonCode, occurrence.RunID, occurrence.ConflictSummary,
		occurrence.ActorType, occurrence.ActorSubject, occurrence.ActorDisplayName, occurrence.CreatedAt, occurrence.UpdatedAt)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

// ClaimExecutionPlanScheduleOccurrenceIfCurrent is the scheduler-only create
// path. It atomically verifies that the parent schedule still names this exact
// due occurrence before inserting the duplicate-prevention anchor.
func (s *SQLiteStore) ClaimExecutionPlanScheduleOccurrenceIfCurrent(occurrence *ExecutionPlanScheduleOccurrence) (bool, error) {
	return claimExecutionPlanScheduleOccurrenceIfCurrentWithExecutor(s.db, occurrence)
}

func claimExecutionPlanScheduleOccurrenceIfCurrentWithExecutor(executor testSaveExecutor, occurrence *ExecutionPlanScheduleOccurrence) (bool, error) {
	if err := bindExecutionPlanScheduleOccurrenceParent(executor, occurrence); err != nil {
		return false, err
	}
	if err := prepareExecutionPlanScheduleOccurrence(occurrence); err != nil {
		return false, err
	}
	occurrence.Revision = 1
	result, err := executor.Exec(`INSERT INTO execution_plan_schedule_occurrences (id, schedule_id, plan_id, project_id, schedule_revision, revision, transition_token, planned_at, materialized_at, status, reason_code, run_id, conflict_summary, actor_type, actor_subject, actor_display_name, created_at, updated_at)
		SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
		FROM execution_plan_schedules
		WHERE id = ?
			AND COALESCE(revision, 1) = ?
			AND COALESCE(enabled, 1) = ?
			AND next_run_at = ?
		ON CONFLICT(schedule_id, planned_at) DO NOTHING`,
		occurrence.ID, occurrence.ScheduleID, occurrence.PlanID, occurrence.ProjectID, occurrence.ScheduleRevision, occurrence.Revision, occurrence.TransitionToken,
		occurrence.PlannedAt, occurrence.MaterializedAt, occurrence.Status, occurrence.ReasonCode, occurrence.RunID, occurrence.ConflictSummary,
		occurrence.ActorType, occurrence.ActorSubject, occurrence.ActorDisplayName, occurrence.CreatedAt, occurrence.UpdatedAt,
		occurrence.ScheduleID, occurrence.ScheduleRevision, true, occurrence.PlannedAt)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func (s *SQLiteStore) CreateExecutionPlanScheduleOccurrenceIfAbsentAndLeaseTermHeld(occurrence *ExecutionPlanScheduleOccurrence, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error) {
	tx, err := s.beginExecutionLeaseTermTransaction(leaseName, leaseOwnerID, leaseGeneration)
	if err != nil || tx == nil {
		return false, err
	}
	defer tx.Rollback()
	created, err := createExecutionPlanScheduleOccurrenceIfAbsentWithExecutor(tx, occurrence)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return created, nil
}

func (s *SQLiteStore) ClaimExecutionPlanScheduleOccurrenceIfCurrentAndLeaseTermHeld(occurrence *ExecutionPlanScheduleOccurrence, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error) {
	tx, err := s.beginExecutionLeaseTermTransaction(leaseName, leaseOwnerID, leaseGeneration)
	if err != nil || tx == nil {
		return false, err
	}
	defer tx.Rollback()
	created, err := claimExecutionPlanScheduleOccurrenceIfCurrentWithExecutor(tx, occurrence)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return created, nil
}

func (s *SQLiteStore) CompareAndSwapExecutionPlanScheduleOccurrence(occurrence *ExecutionPlanScheduleOccurrence, expectedRevision int64) (bool, error) {
	return compareAndSwapExecutionPlanScheduleOccurrenceWithExecutor(s.db, occurrence, expectedRevision)
}

func compareAndSwapExecutionPlanScheduleOccurrenceWithExecutor(executor testSaveExecutor, occurrence *ExecutionPlanScheduleOccurrence, expectedRevision int64) (bool, error) {
	if expectedRevision < 1 {
		return false, errors.New("execution plan schedule occurrence expected revision must be positive")
	}
	if err := bindExecutionPlanScheduleOccurrenceParent(executor, occurrence); err != nil {
		return false, err
	}
	if err := prepareExecutionPlanScheduleOccurrence(occurrence); err != nil {
		return false, err
	}
	result, err := executor.Exec(`UPDATE execution_plan_schedule_occurrences SET
		materialized_at=?, status=?, reason_code=?, run_id=?, conflict_summary=?, transition_token=?, revision=revision+1, updated_at=?
		WHERE id=? AND schedule_id=? AND planned_at=? AND COALESCE(revision, 1)=?
			AND COALESCE(actor_type, 'legacy')=? AND COALESCE(actor_subject, 'controller:execution-plan-schedule-occurrence-legacy')=?
			AND COALESCE(actor_display_name, '')=?`,
		occurrence.MaterializedAt, occurrence.Status, occurrence.ReasonCode, occurrence.RunID, occurrence.ConflictSummary,
		occurrence.TransitionToken, occurrence.UpdatedAt, occurrence.ID, occurrence.ScheduleID, occurrence.PlannedAt, expectedRevision,
		occurrence.ActorType, occurrence.ActorSubject, occurrence.ActorDisplayName)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected != 1 {
		return false, nil
	}
	occurrence.Revision = expectedRevision + 1
	return true, nil
}

func (s *SQLiteStore) CompareAndSwapExecutionPlanScheduleOccurrenceIfLeaseTermHeld(occurrence *ExecutionPlanScheduleOccurrence, expectedRevision int64, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error) {
	tx, err := s.beginExecutionLeaseTermTransaction(leaseName, leaseOwnerID, leaseGeneration)
	if err != nil || tx == nil {
		return false, err
	}
	defer tx.Rollback()
	updated, err := compareAndSwapExecutionPlanScheduleOccurrenceWithExecutor(tx, occurrence, expectedRevision)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return updated, nil
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
	return s.listExecutionPlanScheduleOccurrencesPage(scheduleID, 0, 0)
}

func bindExecutionPlanScheduleOccurrenceParent(executor testSaveExecutor, occurrence *ExecutionPlanScheduleOccurrence) error {
	if occurrence == nil {
		return errors.New("execution plan schedule occurrence is required")
	}
	var planID, projectID, actorType, actorSubject, actorDisplayName string
	var scheduleRevision int
	if err := executor.QueryRow(`SELECT plan_id, COALESCE(project_id, ''), COALESCE(revision, 1),
		COALESCE(actor_type, 'legacy'), COALESCE(actor_subject, 'controller:execution-plan-schedule-legacy'), COALESCE(actor_display_name, '')
		FROM execution_plan_schedules WHERE id = ?`, occurrence.ScheduleID).
		Scan(&planID, &projectID, &scheduleRevision, &actorType, &actorSubject, &actorDisplayName); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("execution plan schedule not found: " + occurrence.ScheduleID)
		}
		return err
	}
	occurrence.PlanID = planID
	occurrence.ProjectID = normalizeProjectID(projectID)
	if occurrence.ScheduleRevision < 1 {
		occurrence.ScheduleRevision = scheduleRevision
	}
	if strings.TrimSpace(occurrence.ActorType) == "" && strings.TrimSpace(occurrence.ActorSubject) == "" {
		occurrence.ActorType = actorType
		occurrence.ActorSubject = actorSubject
		occurrence.ActorDisplayName = actorDisplayName
	}
	return nil
}

// ListExecutionPlanScheduleOccurrencesPage bounds controller-facing schedule
// history reads without changing the complete-history recovery primitive.
func (s *SQLiteStore) ListExecutionPlanScheduleOccurrencesPage(scheduleID string, limit, offset int) ([]*ExecutionPlanScheduleOccurrence, error) {
	if limit <= 0 {
		return []*ExecutionPlanScheduleOccurrence{}, nil
	}
	if offset < 0 {
		offset = 0
	}
	return s.listExecutionPlanScheduleOccurrencesPage(scheduleID, limit, offset)
}

func (s *SQLiteStore) listExecutionPlanScheduleOccurrencesPage(scheduleID string, limit, offset int) ([]*ExecutionPlanScheduleOccurrence, error) {
	query := `SELECT ` + executionPlanScheduleOccurrenceColumns + ` WHERE schedule_id = ? ORDER BY planned_at DESC`
	args := []any{scheduleID}
	if limit > 0 {
		query += ` LIMIT ? OFFSET ?`
		args = append(args, limit, offset)
	}
	rows, err := s.db.Query(query, args...)
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

func (s *SQLiteStore) ListExecutionPlanScheduleOccurrencesByTimeRange(projectID string, from time.Time, to time.Time) ([]*ExecutionPlanScheduleOccurrence, error) {
	projectID = normalizeProjectID(projectID)
	query := `SELECT ` + executionPlanScheduleOccurrenceColumns + ` WHERE planned_at >= ? AND planned_at < ?`
	args := []any{from.UTC(), to.UTC()}
	if projectID != "" {
		query += ` AND COALESCE(project_id, '') = ?`
		args = append(args, projectID)
	}
	query += ` ORDER BY planned_at DESC`
	rows, err := s.db.Query(query, args...)
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
		&occurrence.Revision, &occurrence.TransitionToken, &occurrence.PlannedAt, &occurrence.MaterializedAt,
		&occurrence.Status, &occurrence.ReasonCode, &occurrence.RunID, &occurrence.ConflictSummary,
		&occurrence.ActorType, &occurrence.ActorSubject, &occurrence.ActorDisplayName,
		&occurrence.CreatedAt, &occurrence.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	occurrence.ProjectID = normalizeProjectID(occurrence.ProjectID)
	if occurrence.ScheduleRevision < 1 {
		occurrence.ScheduleRevision = 1
	}
	if occurrence.Revision < 1 {
		occurrence.Revision = 1
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
