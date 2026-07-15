package storage

// SQLite store — schedules. Split from sqlite.go (same package, behavior-preserving).

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *SQLiteStore) SaveScenarioSchedule(schedule *ScenarioSchedule) error {
	if schedule == nil {
		return errors.New("schedule is required")
	}
	if strings.TrimSpace(schedule.ID) == "" {
		return errors.New("schedule id is required")
	}
	if strings.TrimSpace(schedule.ScenarioID) == "" {
		return errors.New("schedule scenario_id is required")
	}
	if strings.TrimSpace(schedule.Name) == "" {
		return errors.New("schedule name is required")
	}
	if strings.TrimSpace(schedule.CronExpr) == "" {
		return errors.New("schedule cron_expr is required")
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
	governanceJSON, err := json.Marshal(schedule.Governance)
	if err != nil {
		return fmt.Errorf("marshal scenario schedule governance: %w", err)
	}
	blackoutWindowsJSON, err := json.Marshal(schedule.BlackoutWindows)
	if err != nil {
		return fmt.Errorf("marshal scenario schedule blackout windows: %w", err)
	}
	maintenanceWindowsJSON, err := json.Marshal(schedule.MaintenanceWindows)
	if err != nil {
		return fmt.Errorf("marshal scenario schedule maintenance windows: %w", err)
	}
	availabilityWindowsJSON, err := json.Marshal(schedule.AvailabilityWindows)
	if err != nil {
		return fmt.Errorf("marshal scenario schedule availability windows: %w", err)
	}
	_, err = s.db.Exec(`INSERT INTO scenario_schedules (id, scenario_id, project_id, revision, name, cron_expr, timezone, release_id, timeslot_id, run_name_template, enabled, approval_policy, missed_run_policy, conflict_summary, next_required_action, pending_approval_id, owner_subject, owner_display_name, governance_json, blackout_windows_json, maintenance_windows_json, availability_windows_json, last_run_test_id, last_run_at, next_run_at, disabled_from, disabled_until, last_error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET scenario_id=excluded.scenario_id, project_id=excluded.project_id, revision=excluded.revision, name=excluded.name, cron_expr=excluded.cron_expr,
		timezone=excluded.timezone, release_id=excluded.release_id, timeslot_id=excluded.timeslot_id, run_name_template=excluded.run_name_template,
		enabled=excluded.enabled, approval_policy=excluded.approval_policy, missed_run_policy=excluded.missed_run_policy,
		conflict_summary=excluded.conflict_summary, next_required_action=excluded.next_required_action, pending_approval_id=excluded.pending_approval_id,
		owner_subject=excluded.owner_subject, owner_display_name=excluded.owner_display_name,
		governance_json=excluded.governance_json, blackout_windows_json=excluded.blackout_windows_json,
		maintenance_windows_json=excluded.maintenance_windows_json, availability_windows_json=excluded.availability_windows_json, last_run_test_id=excluded.last_run_test_id, last_run_at=excluded.last_run_at,
		next_run_at=excluded.next_run_at, disabled_from=excluded.disabled_from, disabled_until=excluded.disabled_until, last_error=excluded.last_error, updated_at=excluded.updated_at`,
		schedule.ID, schedule.ScenarioID, schedule.ProjectID, schedule.Revision, schedule.Name, schedule.CronExpr, schedule.Timezone, nullableString(schedule.ReleaseID), schedule.TimeslotID, schedule.RunNameTemplate, schedule.Enabled, schedule.ApprovalPolicy, schedule.MissedRunPolicy, schedule.ConflictSummary, schedule.NextRequiredAction, schedule.PendingApprovalID, schedule.OwnerSubject, schedule.OwnerDisplayName, string(governanceJSON), string(blackoutWindowsJSON), string(maintenanceWindowsJSON), string(availabilityWindowsJSON),
		nullableString(schedule.LastRunTestID), schedule.LastRunAt, schedule.NextRunAt, schedule.DisabledFrom, schedule.DisabledUntil, schedule.LastError, schedule.CreatedAt, schedule.UpdatedAt)
	return err
}

// SaveScenarioScheduleWithApproval commits the schedule revision and its
// pending approval as one durable unit. This prevents a crash or partial write
// from leaving pending_approval_id dangling.
func (s *SQLiteStore) SaveScenarioScheduleWithApproval(schedule *ScenarioSchedule, approval *ScenarioScheduleApproval, expectedRevision int) (bool, error) {
	governanceJSON, blackoutJSON, maintenanceJSON, availabilityJSON, err := prepareScenarioSchedule(schedule)
	if err != nil {
		return false, err
	}
	if err := prepareScenarioScheduleApproval(approval); err != nil {
		return false, err
	}
	if approval.ScheduleID != schedule.ID || schedule.PendingApprovalID != approval.ID {
		return false, errors.New("schedule and approval identifiers do not match")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var result sql.Result
	if expectedRevision == 0 {
		result, err = tx.Exec(`INSERT INTO scenario_schedules (id, scenario_id, project_id, revision, name, cron_expr, timezone, release_id, timeslot_id, run_name_template, enabled, approval_policy, missed_run_policy, conflict_summary, next_required_action, pending_approval_id, owner_subject, owner_display_name, governance_json, blackout_windows_json, maintenance_windows_json, availability_windows_json, last_run_test_id, last_run_at, next_run_at, disabled_from, disabled_until, last_error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING`,
			schedule.ID, schedule.ScenarioID, schedule.ProjectID, schedule.Revision, schedule.Name, schedule.CronExpr, schedule.Timezone, nullableString(schedule.ReleaseID), schedule.TimeslotID, schedule.RunNameTemplate, schedule.Enabled, schedule.ApprovalPolicy, schedule.MissedRunPolicy, schedule.ConflictSummary, schedule.NextRequiredAction, schedule.PendingApprovalID, schedule.OwnerSubject, schedule.OwnerDisplayName, governanceJSON, blackoutJSON, maintenanceJSON, availabilityJSON,
			nullableString(schedule.LastRunTestID), schedule.LastRunAt, schedule.NextRunAt, schedule.DisabledFrom, schedule.DisabledUntil, schedule.LastError, schedule.CreatedAt, schedule.UpdatedAt)
	} else {
		schedule.Revision = expectedRevision + 1
		approval.ScheduleRevision = schedule.Revision
		result, err = tx.Exec(`UPDATE scenario_schedules SET scenario_id=?, project_id=?, revision=?, name=?, cron_expr=?, timezone=?, release_id=?, timeslot_id=?, run_name_template=?, enabled=?, approval_policy=?, missed_run_policy=?, conflict_summary=?, next_required_action=?, pending_approval_id=?, owner_subject=?, owner_display_name=?, governance_json=?, blackout_windows_json=?, maintenance_windows_json=?, availability_windows_json=?, last_run_test_id=?, last_run_at=?, next_run_at=?, disabled_from=?, disabled_until=?, last_error=?, updated_at=? WHERE id=? AND COALESCE(revision, 1)=?`,
			schedule.ScenarioID, schedule.ProjectID, schedule.Revision, schedule.Name, schedule.CronExpr, schedule.Timezone, nullableString(schedule.ReleaseID), schedule.TimeslotID, schedule.RunNameTemplate, schedule.Enabled, schedule.ApprovalPolicy, schedule.MissedRunPolicy, schedule.ConflictSummary, schedule.NextRequiredAction, schedule.PendingApprovalID, schedule.OwnerSubject, schedule.OwnerDisplayName, governanceJSON, blackoutJSON, maintenanceJSON, availabilityJSON, nullableString(schedule.LastRunTestID), schedule.LastRunAt, schedule.NextRunAt, schedule.DisabledFrom, schedule.DisabledUntil, schedule.LastError, schedule.UpdatedAt, schedule.ID, expectedRevision)
	}
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return false, err
	}
	// A schedule can reference only one actionable approval. Close any older
	// pending request in the same transaction so history remains truthful and an
	// obsolete approval cannot remain permanently unresolvable.
	if _, err = tx.Exec(`UPDATE scenario_schedule_approvals
		SET status='superseded', resolved_at=?, decision_reason=?, updated_at=?
		WHERE schedule_id=? AND status='pending' AND id<>?`,
		approval.UpdatedAt, "superseded by a newer schedule revision", approval.UpdatedAt, schedule.ID, approval.ID); err != nil {
		return false, err
	}
	_, err = tx.Exec(`INSERT INTO scenario_schedule_approvals (id, schedule_id, scenario_id, project_id, schedule_revision, policy, status, requested_by, requested_at, resolved_by, resolved_at, decision_reason, required_role, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET scenario_id=excluded.scenario_id, project_id=excluded.project_id,
		schedule_revision=excluded.schedule_revision, policy=excluded.policy, status=excluded.status,
		requested_by=excluded.requested_by, requested_at=excluded.requested_at, resolved_by=excluded.resolved_by,
		resolved_at=excluded.resolved_at, decision_reason=excluded.decision_reason, required_role=excluded.required_role,
		updated_at=excluded.updated_at`,
		approval.ID, approval.ScheduleID, approval.ScenarioID, approval.ProjectID, approval.ScheduleRevision, approval.Policy, approval.Status, approval.RequestedBy, approval.RequestedAt, approval.ResolvedBy, approval.ResolvedAt, approval.DecisionReason, approval.RequiredRole, approval.CreatedAt, approval.UpdatedAt)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// CreateScenarioScheduleIfAbsent creates a schedule without replacing an
// existing row. This is the create half of the schedule CAS contract.
func (s *SQLiteStore) CreateScenarioScheduleIfAbsent(schedule *ScenarioSchedule) (bool, error) {
	governanceJSON, blackoutJSON, maintenanceJSON, availabilityJSON, err := prepareScenarioSchedule(schedule)
	if err != nil {
		return false, err
	}
	result, err := s.db.Exec(`INSERT INTO scenario_schedules (id, scenario_id, project_id, revision, name, cron_expr, timezone, release_id, timeslot_id, run_name_template, enabled, approval_policy, missed_run_policy, conflict_summary, next_required_action, pending_approval_id, owner_subject, owner_display_name, governance_json, blackout_windows_json, maintenance_windows_json, availability_windows_json, last_run_test_id, last_run_at, next_run_at, disabled_from, disabled_until, last_error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING`,
		schedule.ID, schedule.ScenarioID, schedule.ProjectID, schedule.Revision, schedule.Name, schedule.CronExpr, schedule.Timezone, nullableString(schedule.ReleaseID), schedule.TimeslotID, schedule.RunNameTemplate, schedule.Enabled, schedule.ApprovalPolicy, schedule.MissedRunPolicy, schedule.ConflictSummary, schedule.NextRequiredAction, schedule.PendingApprovalID, schedule.OwnerSubject, schedule.OwnerDisplayName, governanceJSON, blackoutJSON, maintenanceJSON, availabilityJSON,
		nullableString(schedule.LastRunTestID), schedule.LastRunAt, schedule.NextRunAt, schedule.DisabledFrom, schedule.DisabledUntil, schedule.LastError, schedule.CreatedAt, schedule.UpdatedAt)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected == 1, nil
}

// UpdateScenarioScheduleIfRevision updates only an existing row whose revision
// still matches the caller's snapshot. It cannot resurrect a deleted schedule.
func (s *SQLiteStore) UpdateScenarioScheduleIfRevision(schedule *ScenarioSchedule, expectedRevision int) (bool, error) {
	governanceJSON, blackoutJSON, maintenanceJSON, availabilityJSON, err := prepareScenarioSchedule(schedule)
	if err != nil {
		return false, err
	}
	if expectedRevision < 1 {
		return false, errors.New("expected scenario schedule revision must be positive")
	}
	nextRevision := expectedRevision + 1
	result, err := s.db.Exec(`UPDATE scenario_schedules SET scenario_id=?, project_id=?, revision=?, name=?, cron_expr=?, timezone=?, release_id=?, timeslot_id=?, run_name_template=?, enabled=?, approval_policy=?, missed_run_policy=?, conflict_summary=?, next_required_action=?, pending_approval_id=?, owner_subject=?, owner_display_name=?, governance_json=?, blackout_windows_json=?, maintenance_windows_json=?, availability_windows_json=?, last_run_test_id=?, last_run_at=?, next_run_at=?, disabled_from=?, disabled_until=?, last_error=?, updated_at=? WHERE id=? AND COALESCE(revision, 1)=?`,
		schedule.ScenarioID, schedule.ProjectID, nextRevision, schedule.Name, schedule.CronExpr, schedule.Timezone, nullableString(schedule.ReleaseID), schedule.TimeslotID, schedule.RunNameTemplate, schedule.Enabled, schedule.ApprovalPolicy, schedule.MissedRunPolicy, schedule.ConflictSummary, schedule.NextRequiredAction, schedule.PendingApprovalID, schedule.OwnerSubject, schedule.OwnerDisplayName, governanceJSON, blackoutJSON, maintenanceJSON, availabilityJSON,
		nullableString(schedule.LastRunTestID), schedule.LastRunAt, schedule.NextRunAt, schedule.DisabledFrom, schedule.DisabledUntil, schedule.LastError, schedule.UpdatedAt,
		schedule.ID, expectedRevision)
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

func prepareScenarioSchedule(schedule *ScenarioSchedule) (string, string, string, string, error) {
	if schedule == nil {
		return "", "", "", "", errors.New("schedule is required")
	}
	if strings.TrimSpace(schedule.ID) == "" || strings.TrimSpace(schedule.ScenarioID) == "" {
		return "", "", "", "", errors.New("schedule id and scenario_id are required")
	}
	if strings.TrimSpace(schedule.Name) == "" || strings.TrimSpace(schedule.CronExpr) == "" {
		return "", "", "", "", errors.New("schedule name and cron_expr are required")
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
	governanceJSON, err := json.Marshal(schedule.Governance)
	if err != nil {
		return "", "", "", "", fmt.Errorf("marshal scenario schedule governance: %w", err)
	}
	blackoutJSON, err := json.Marshal(schedule.BlackoutWindows)
	if err != nil {
		return "", "", "", "", fmt.Errorf("marshal scenario schedule blackout windows: %w", err)
	}
	maintenanceJSON, err := json.Marshal(schedule.MaintenanceWindows)
	if err != nil {
		return "", "", "", "", fmt.Errorf("marshal scenario schedule maintenance windows: %w", err)
	}
	availabilityJSON, err := json.Marshal(schedule.AvailabilityWindows)
	if err != nil {
		return "", "", "", "", fmt.Errorf("marshal scenario schedule availability windows: %w", err)
	}
	return string(governanceJSON), string(blackoutJSON), string(maintenanceJSON), string(availabilityJSON), nil
}

func (s *SQLiteStore) GetScenarioSchedule(id string) (*ScenarioSchedule, error) {
	row := s.db.QueryRow(`SELECT id, scenario_id, COALESCE(project_id, ''), COALESCE(revision, 1), name, cron_expr, COALESCE(timezone, ''), COALESCE(release_id, ''), COALESCE(timeslot_id, ''), COALESCE(run_name_template, ''), COALESCE(enabled, 1), COALESCE(approval_policy, ''), COALESCE(missed_run_policy, ''), COALESCE(conflict_summary, ''), COALESCE(next_required_action, ''), COALESCE(pending_approval_id, ''), COALESCE(owner_subject, ''), COALESCE(owner_display_name, ''), COALESCE(governance_json, ''), COALESCE(blackout_windows_json, ''), COALESCE(maintenance_windows_json, ''), COALESCE(availability_windows_json, ''), COALESCE(last_run_test_id, ''), last_run_at, next_run_at, disabled_from, disabled_until, COALESCE(last_error, ''), created_at, updated_at FROM scenario_schedules WHERE id = ?`, id)
	return scanScenarioSchedule(row)
}

func (s *SQLiteStore) ListScenarioSchedules(scenarioID string) ([]*ScenarioSchedule, error) {
	rows, err := s.db.Query(`SELECT id, scenario_id, COALESCE(project_id, ''), COALESCE(revision, 1), name, cron_expr, COALESCE(timezone, ''), COALESCE(release_id, ''), COALESCE(timeslot_id, ''), COALESCE(run_name_template, ''), COALESCE(enabled, 1), COALESCE(approval_policy, ''), COALESCE(missed_run_policy, ''), COALESCE(conflict_summary, ''), COALESCE(next_required_action, ''), COALESCE(pending_approval_id, ''), COALESCE(owner_subject, ''), COALESCE(owner_display_name, ''), COALESCE(governance_json, ''), COALESCE(blackout_windows_json, ''), COALESCE(maintenance_windows_json, ''), COALESCE(availability_windows_json, ''), COALESCE(last_run_test_id, ''), last_run_at, next_run_at, disabled_from, disabled_until, COALESCE(last_error, ''), created_at, updated_at FROM scenario_schedules WHERE scenario_id = ? ORDER BY created_at ASC`, scenarioID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectScenarioSchedules(rows)
}

func (s *SQLiteStore) ListAllScenarioSchedules() ([]*ScenarioSchedule, error) {
	rows, err := s.db.Query(`SELECT id, scenario_id, COALESCE(project_id, ''), COALESCE(revision, 1), name, cron_expr, COALESCE(timezone, ''), COALESCE(release_id, ''), COALESCE(timeslot_id, ''), COALESCE(run_name_template, ''), COALESCE(enabled, 1), COALESCE(approval_policy, ''), COALESCE(missed_run_policy, ''), COALESCE(conflict_summary, ''), COALESCE(next_required_action, ''), COALESCE(pending_approval_id, ''), COALESCE(owner_subject, ''), COALESCE(owner_display_name, ''), COALESCE(governance_json, ''), COALESCE(blackout_windows_json, ''), COALESCE(maintenance_windows_json, ''), COALESCE(availability_windows_json, ''), COALESCE(last_run_test_id, ''), last_run_at, next_run_at, disabled_from, disabled_until, COALESCE(last_error, ''), created_at, updated_at FROM scenario_schedules ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectScenarioSchedules(rows)
}

func (s *SQLiteStore) DeleteScenarioSchedule(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM scenario_schedule_approvals WHERE schedule_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM scenario_schedule_occurrences WHERE schedule_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM scenario_schedules WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) SaveScenarioScheduleOccurrence(occurrence *ScenarioScheduleOccurrence) error {
	if occurrence == nil {
		return errors.New("schedule occurrence is required")
	}
	if strings.TrimSpace(occurrence.ID) == "" {
		return errors.New("schedule occurrence id is required")
	}
	if strings.TrimSpace(occurrence.ScheduleID) == "" {
		return errors.New("schedule occurrence schedule_id is required")
	}
	if occurrence.PlannedAt.IsZero() {
		return errors.New("schedule occurrence planned_at is required")
	}
	if strings.TrimSpace(occurrence.Status) == "" {
		return errors.New("schedule occurrence status is required")
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
	_, err := s.db.Exec(`INSERT INTO scenario_schedule_occurrences (id, schedule_id, scenario_id, project_id, schedule_revision, planned_at, materialized_at, status, reason_code, test_id, approval_id, conflict_summary, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(schedule_id, planned_at) DO UPDATE SET scenario_id=excluded.scenario_id, project_id=excluded.project_id,
		schedule_revision=excluded.schedule_revision, materialized_at=excluded.materialized_at, status=excluded.status,
		reason_code=excluded.reason_code, test_id=excluded.test_id, approval_id=excluded.approval_id,
		conflict_summary=excluded.conflict_summary, updated_at=excluded.updated_at`,
		occurrence.ID, occurrence.ScheduleID, occurrence.ScenarioID, occurrence.ProjectID, occurrence.ScheduleRevision, occurrence.PlannedAt, occurrence.MaterializedAt, occurrence.Status, occurrence.ReasonCode, nullableString(occurrence.TestID), occurrence.ApprovalID, occurrence.ConflictSummary, occurrence.CreatedAt, occurrence.UpdatedAt)
	return err
}

func (s *SQLiteStore) GetScenarioScheduleOccurrence(id string) (*ScenarioScheduleOccurrence, error) {
	row := s.db.QueryRow(`SELECT id, schedule_id, scenario_id, COALESCE(project_id, ''), COALESCE(schedule_revision, 1), planned_at, materialized_at, status, COALESCE(reason_code, ''), COALESCE(test_id, ''), COALESCE(approval_id, ''), COALESCE(conflict_summary, ''), created_at, updated_at FROM scenario_schedule_occurrences WHERE id = ?`, id)
	return scanScenarioScheduleOccurrence(row)
}

func (s *SQLiteStore) GetScenarioScheduleOccurrenceByScheduleAndPlannedAt(scheduleID string, plannedAt time.Time) (*ScenarioScheduleOccurrence, error) {
	row := s.db.QueryRow(`SELECT id, schedule_id, scenario_id, COALESCE(project_id, ''), COALESCE(schedule_revision, 1), planned_at, materialized_at, status, COALESCE(reason_code, ''), COALESCE(test_id, ''), COALESCE(approval_id, ''), COALESCE(conflict_summary, ''), created_at, updated_at FROM scenario_schedule_occurrences WHERE schedule_id = ? AND planned_at = ?`, scheduleID, plannedAt)
	occurrence, err := scanScenarioScheduleOccurrence(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return occurrence, err
}

func (s *SQLiteStore) ListScenarioScheduleOccurrences(scheduleID string) ([]*ScenarioScheduleOccurrence, error) {
	rows, err := s.db.Query(`SELECT id, schedule_id, scenario_id, COALESCE(project_id, ''), COALESCE(schedule_revision, 1), planned_at, materialized_at, status, COALESCE(reason_code, ''), COALESCE(test_id, ''), COALESCE(approval_id, ''), COALESCE(conflict_summary, ''), created_at, updated_at FROM scenario_schedule_occurrences WHERE schedule_id = ? ORDER BY planned_at DESC`, scheduleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectScenarioScheduleOccurrences(rows)
}

func (s *SQLiteStore) LatestScenarioScheduleOccurrence(scheduleID string) (*ScenarioScheduleOccurrence, error) {
	row := s.db.QueryRow(`SELECT id, schedule_id, scenario_id, COALESCE(project_id, ''), COALESCE(schedule_revision, 1), planned_at, materialized_at, status, COALESCE(reason_code, ''), COALESCE(test_id, ''), COALESCE(approval_id, ''), COALESCE(conflict_summary, ''), created_at, updated_at FROM scenario_schedule_occurrences WHERE schedule_id = ? ORDER BY planned_at DESC LIMIT 1`, scheduleID)
	occurrence, err := scanScenarioScheduleOccurrence(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return occurrence, err
}

func (s *SQLiteStore) ListAllScenarioScheduleOccurrences() ([]*ScenarioScheduleOccurrence, error) {
	rows, err := s.db.Query(`SELECT id, schedule_id, scenario_id, COALESCE(project_id, ''), COALESCE(schedule_revision, 1), planned_at, materialized_at, status, COALESCE(reason_code, ''), COALESCE(test_id, ''), COALESCE(approval_id, ''), COALESCE(conflict_summary, ''), created_at, updated_at FROM scenario_schedule_occurrences ORDER BY planned_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectScenarioScheduleOccurrences(rows)
}

func (s *SQLiteStore) ListScenarioScheduleOccurrencesByTimeRange(projectID string, from time.Time, to time.Time) ([]*ScenarioScheduleOccurrence, error) {
	projectID = normalizeProjectID(projectID)
	query := `SELECT id, schedule_id, scenario_id, COALESCE(project_id, ''), COALESCE(schedule_revision, 1), planned_at, materialized_at, status, COALESCE(reason_code, ''), COALESCE(test_id, ''), COALESCE(approval_id, ''), COALESCE(conflict_summary, ''), created_at, updated_at FROM scenario_schedule_occurrences WHERE planned_at >= ? AND planned_at < ?`
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
	return collectScenarioScheduleOccurrences(rows)
}

func (s *SQLiteStore) DeleteScenarioScheduleOccurrence(id string) error {
	_, err := s.db.Exec(`DELETE FROM scenario_schedule_occurrences WHERE id = ?`, id)
	return err
}

func (s *SQLiteStore) SaveScenarioScheduleApproval(approval *ScenarioScheduleApproval) error {
	if err := prepareScenarioScheduleApproval(approval); err != nil {
		return err
	}
	_, err := s.db.Exec(`INSERT INTO scenario_schedule_approvals (id, schedule_id, scenario_id, project_id, schedule_revision, policy, status, requested_by, requested_at, resolved_by, resolved_at, decision_reason, required_role, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET scenario_id=excluded.scenario_id, project_id=excluded.project_id,
		schedule_revision=excluded.schedule_revision, policy=excluded.policy, status=excluded.status,
		requested_by=excluded.requested_by, requested_at=excluded.requested_at, resolved_by=excluded.resolved_by,
		resolved_at=excluded.resolved_at, decision_reason=excluded.decision_reason, required_role=excluded.required_role,
		updated_at=excluded.updated_at`,
		approval.ID, approval.ScheduleID, approval.ScenarioID, approval.ProjectID, approval.ScheduleRevision, approval.Policy, approval.Status, approval.RequestedBy, approval.RequestedAt, approval.ResolvedBy, approval.ResolvedAt, approval.DecisionReason, approval.RequiredRole, approval.CreatedAt, approval.UpdatedAt)
	return err
}

func prepareScenarioScheduleApproval(approval *ScenarioScheduleApproval) error {
	if approval == nil {
		return errors.New("schedule approval is required")
	}
	if strings.TrimSpace(approval.ID) == "" {
		return errors.New("schedule approval id is required")
	}
	if strings.TrimSpace(approval.ScheduleID) == "" {
		return errors.New("schedule approval schedule_id is required")
	}
	if strings.TrimSpace(approval.Policy) == "" {
		return errors.New("schedule approval policy is required")
	}
	if strings.TrimSpace(approval.Status) == "" {
		return errors.New("schedule approval status is required")
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
	if approval.ScheduleRevision < 1 {
		approval.ScheduleRevision = 1
	}
	return nil
}

func (s *SQLiteStore) GetScenarioScheduleApproval(id string) (*ScenarioScheduleApproval, error) {
	row := s.db.QueryRow(`SELECT id, schedule_id, scenario_id, COALESCE(project_id, ''), COALESCE(schedule_revision, 1), policy, status, COALESCE(requested_by, ''), requested_at, COALESCE(resolved_by, ''), resolved_at, COALESCE(decision_reason, ''), COALESCE(required_role, ''), created_at, updated_at FROM scenario_schedule_approvals WHERE id = ?`, id)
	return scanScenarioScheduleApproval(row)
}

func (s *SQLiteStore) ListScenarioScheduleApprovals(scheduleID string) ([]*ScenarioScheduleApproval, error) {
	rows, err := s.db.Query(`SELECT id, schedule_id, scenario_id, COALESCE(project_id, ''), COALESCE(schedule_revision, 1), policy, status, COALESCE(requested_by, ''), requested_at, COALESCE(resolved_by, ''), resolved_at, COALESCE(decision_reason, ''), COALESCE(required_role, ''), created_at, updated_at FROM scenario_schedule_approvals WHERE schedule_id = ? ORDER BY requested_at DESC`, scheduleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectScenarioScheduleApprovals(rows)
}

// ResolveScenarioScheduleApproval atomically resolves a still-pending approval
// and applies its corresponding schedule state. Competing approve/reject calls
// cannot both succeed, and neither row can be committed without the other.
func (s *SQLiteStore) ResolveScenarioScheduleApproval(approval *ScenarioScheduleApproval, schedule *ScenarioSchedule, expectedScheduleRevision int) (bool, error) {
	if approval == nil || schedule == nil {
		return false, errors.New("approval and schedule are required")
	}
	if approval.ID == "" || approval.ScheduleID == "" || schedule.ID != approval.ScheduleID {
		return false, errors.New("approval and schedule identifiers do not match")
	}
	if approval.Status != "approved" && approval.Status != "rejected" {
		return false, errors.New("approval status must be approved or rejected")
	}
	if expectedScheduleRevision < 1 {
		return false, errors.New("expected scenario schedule revision must be positive")
	}
	nextScheduleRevision := expectedScheduleRevision + 1
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`UPDATE scenario_schedule_approvals SET status=?, resolved_by=?, resolved_at=?, decision_reason=?, updated_at=? WHERE id=? AND schedule_id=? AND status='pending'`,
		approval.Status, approval.ResolvedBy, approval.ResolvedAt, approval.DecisionReason, approval.UpdatedAt, approval.ID, approval.ScheduleID)
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
	result, err = tx.Exec(`UPDATE scenario_schedules SET revision=?, pending_approval_id=?, next_required_action=?, last_error=?, updated_at=? WHERE id=? AND COALESCE(revision, 1)=? AND pending_approval_id=?`,
		nextScheduleRevision, schedule.PendingApprovalID, schedule.NextRequiredAction, schedule.LastError, schedule.UpdatedAt, schedule.ID, expectedScheduleRevision, approval.ID)
	if err != nil {
		return false, err
	}
	affected, err = result.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected != 1 {
		return false, nil
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	schedule.Revision = nextScheduleRevision
	return true, nil
}

func (s *SQLiteStore) TryAcquireSchedulerLease(name string, ownerID string, _ time.Time, ttl time.Duration) (bool, error) {
	name = strings.TrimSpace(name)
	ownerID = strings.TrimSpace(ownerID)
	if name == "" {
		return false, errors.New("scheduler lease name is required")
	}
	if ownerID == "" {
		return false, errors.New("scheduler lease owner_id is required")
	}
	if ttl <= 0 {
		return false, errors.New("scheduler lease ttl must be positive")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var epochSeconds int64
	clockQuery := `SELECT unixepoch()`
	if s.db.dialect == storeDialectPostgres {
		clockQuery = `SELECT CAST(EXTRACT(EPOCH FROM clock_timestamp()) AS BIGINT)`
	}
	if err := tx.QueryRow(clockQuery).Scan(&epochSeconds); err != nil {
		return false, fmt.Errorf("read scheduler database clock: %w", err)
	}
	now := time.Unix(epochSeconds, 0).UTC()
	expiresAt := now.Add(ttl)
	result, err := tx.Exec(`INSERT INTO scheduler_leases (name, owner_id, acquired_at, expires_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET owner_id=excluded.owner_id, acquired_at=excluded.acquired_at,
		expires_at=excluded.expires_at, updated_at=excluded.updated_at
		WHERE scheduler_leases.expires_at <= ? OR scheduler_leases.owner_id = ?`,
		name, ownerID, now, expiresAt, now, now, ownerID)
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
	return affected > 0, nil
}

func (s *SQLiteStore) GetSchedulerLease(name string) (*SchedulerLease, error) {
	var lease SchedulerLease
	err := s.db.QueryRow(`SELECT name, owner_id, acquired_at, expires_at, updated_at FROM scheduler_leases WHERE name = ?`, name).Scan(&lease.Name, &lease.OwnerID, &lease.AcquiredAt, &lease.ExpiresAt, &lease.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &lease, nil
}

func scanScenarioSchedule(row scanner) (*ScenarioSchedule, error) {
	var schedule ScenarioSchedule
	var enabled any
	var governanceJSON string
	var blackoutWindowsJSON string
	var maintenanceWindowsJSON string
	var availabilityWindowsJSON string
	err := row.Scan(
		&schedule.ID,
		&schedule.ScenarioID,
		&schedule.ProjectID,
		&schedule.Revision,
		&schedule.Name,
		&schedule.CronExpr,
		&schedule.Timezone,
		&schedule.ReleaseID,
		&schedule.TimeslotID,
		&schedule.RunNameTemplate,
		&enabled,
		&schedule.ApprovalPolicy,
		&schedule.MissedRunPolicy,
		&schedule.ConflictSummary,
		&schedule.NextRequiredAction,
		&schedule.PendingApprovalID,
		&schedule.OwnerSubject,
		&schedule.OwnerDisplayName,
		&governanceJSON,
		&blackoutWindowsJSON,
		&maintenanceWindowsJSON,
		&availabilityWindowsJSON,
		&schedule.LastRunTestID,
		&schedule.LastRunAt,
		&schedule.NextRunAt,
		&schedule.DisabledFrom,
		&schedule.DisabledUntil,
		&schedule.LastError,
		&schedule.CreatedAt,
		&schedule.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	schedule.Enabled = sqlBool(enabled)
	schedule.ProjectID = normalizeProjectID(schedule.ProjectID)
	if schedule.Revision < 1 {
		schedule.Revision = 1
	}
	if governanceJSON != "" && governanceJSON != "null" {
		var governance ScenarioScheduleGovernance
		if err := json.Unmarshal([]byte(governanceJSON), &governance); err != nil {
			return nil, fmt.Errorf("decode scenario schedule %s governance_json: %w", schedule.ID, err)
		}
		schedule.Governance = &governance
	}
	if blackoutWindowsJSON != "" && blackoutWindowsJSON != "null" {
		if err := json.Unmarshal([]byte(blackoutWindowsJSON), &schedule.BlackoutWindows); err != nil {
			return nil, fmt.Errorf("decode scenario schedule %s blackout_windows_json: %w", schedule.ID, err)
		}
	}
	if maintenanceWindowsJSON != "" && maintenanceWindowsJSON != "null" {
		if err := json.Unmarshal([]byte(maintenanceWindowsJSON), &schedule.MaintenanceWindows); err != nil {
			return nil, fmt.Errorf("decode scenario schedule %s maintenance_windows_json: %w", schedule.ID, err)
		}
	}
	if availabilityWindowsJSON != "" && availabilityWindowsJSON != "null" {
		if err := json.Unmarshal([]byte(availabilityWindowsJSON), &schedule.AvailabilityWindows); err != nil {
			return nil, fmt.Errorf("decode scenario schedule %s availability_windows_json: %w", schedule.ID, err)
		}
	}
	return &schedule, nil
}

func collectScenarioSchedules(rows *sql.Rows) ([]*ScenarioSchedule, error) {
	var schedules []*ScenarioSchedule
	for rows.Next() {
		schedule, err := scanScenarioSchedule(rows)
		if err != nil {
			return nil, err
		}
		schedules = append(schedules, schedule)
	}
	return schedules, rows.Err()
}

func scanScenarioScheduleOccurrence(row scanner) (*ScenarioScheduleOccurrence, error) {
	var occurrence ScenarioScheduleOccurrence
	err := row.Scan(
		&occurrence.ID,
		&occurrence.ScheduleID,
		&occurrence.ScenarioID,
		&occurrence.ProjectID,
		&occurrence.ScheduleRevision,
		&occurrence.PlannedAt,
		&occurrence.MaterializedAt,
		&occurrence.Status,
		&occurrence.ReasonCode,
		&occurrence.TestID,
		&occurrence.ApprovalID,
		&occurrence.ConflictSummary,
		&occurrence.CreatedAt,
		&occurrence.UpdatedAt,
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

func collectScenarioScheduleOccurrences(rows *sql.Rows) ([]*ScenarioScheduleOccurrence, error) {
	var occurrences []*ScenarioScheduleOccurrence
	for rows.Next() {
		occurrence, err := scanScenarioScheduleOccurrence(rows)
		if err != nil {
			return nil, err
		}
		occurrences = append(occurrences, occurrence)
	}
	return occurrences, rows.Err()
}

func scanScenarioScheduleApproval(row scanner) (*ScenarioScheduleApproval, error) {
	var approval ScenarioScheduleApproval
	err := row.Scan(
		&approval.ID,
		&approval.ScheduleID,
		&approval.ScenarioID,
		&approval.ProjectID,
		&approval.ScheduleRevision,
		&approval.Policy,
		&approval.Status,
		&approval.RequestedBy,
		&approval.RequestedAt,
		&approval.ResolvedBy,
		&approval.ResolvedAt,
		&approval.DecisionReason,
		&approval.RequiredRole,
		&approval.CreatedAt,
		&approval.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	approval.ProjectID = normalizeProjectID(approval.ProjectID)
	if approval.ScheduleRevision < 1 {
		approval.ScheduleRevision = 1
	}
	return &approval, nil
}

func collectScenarioScheduleApprovals(rows *sql.Rows) ([]*ScenarioScheduleApproval, error) {
	var approvals []*ScenarioScheduleApproval
	for rows.Next() {
		approval, err := scanScenarioScheduleApproval(rows)
		if err != nil {
			return nil, err
		}
		approvals = append(approvals, approval)
	}
	return approvals, rows.Err()
}
