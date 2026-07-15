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
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := bindScenarioScheduleParent(tx, schedule); err != nil {
		return err
	}
	result, err := tx.Exec(`INSERT INTO scenario_schedules (id, scenario_id, project_id, revision, name, cron_expr, timezone, release_id, timeslot_id, run_name_template, enabled, approval_policy, missed_run_policy, conflict_summary, next_required_action, pending_approval_id, owner_subject, owner_display_name, governance_json, blackout_windows_json, maintenance_windows_json, availability_windows_json, last_run_test_id, last_run_at, next_run_at, disabled_from, disabled_until, last_error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET revision=excluded.revision, name=excluded.name, cron_expr=excluded.cron_expr,
		timezone=excluded.timezone, release_id=excluded.release_id, timeslot_id=excluded.timeslot_id, run_name_template=excluded.run_name_template,
		enabled=excluded.enabled, approval_policy=excluded.approval_policy, missed_run_policy=excluded.missed_run_policy,
		conflict_summary=excluded.conflict_summary, next_required_action=excluded.next_required_action, pending_approval_id=excluded.pending_approval_id,
		owner_subject=excluded.owner_subject, owner_display_name=excluded.owner_display_name,
		governance_json=excluded.governance_json, blackout_windows_json=excluded.blackout_windows_json,
		maintenance_windows_json=excluded.maintenance_windows_json, availability_windows_json=excluded.availability_windows_json, last_run_test_id=excluded.last_run_test_id, last_run_at=excluded.last_run_at,
		next_run_at=excluded.next_run_at, disabled_from=excluded.disabled_from, disabled_until=excluded.disabled_until, last_error=excluded.last_error, updated_at=excluded.updated_at
		WHERE scenario_schedules.scenario_id=excluded.scenario_id AND scenario_schedules.project_id=excluded.project_id`,
		schedule.ID, schedule.ScenarioID, schedule.ProjectID, schedule.Revision, schedule.Name, schedule.CronExpr, schedule.Timezone, nullableString(schedule.ReleaseID), schedule.TimeslotID, schedule.RunNameTemplate, schedule.Enabled, schedule.ApprovalPolicy, schedule.MissedRunPolicy, schedule.ConflictSummary, schedule.NextRequiredAction, schedule.PendingApprovalID, schedule.OwnerSubject, schedule.OwnerDisplayName, string(governanceJSON), string(blackoutWindowsJSON), string(maintenanceWindowsJSON), string(availabilityWindowsJSON),
		nullableString(schedule.LastRunTestID), schedule.LastRunAt, schedule.NextRunAt, schedule.DisabledFrom, schedule.DisabledUntil, schedule.LastError, schedule.CreatedAt, schedule.UpdatedAt)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("scenario schedule %q belongs to another scenario or project", schedule.ID)
	}
	return tx.Commit()
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
	if err := bindScenarioScheduleParent(tx, schedule); err != nil {
		return false, err
	}
	if strings.TrimSpace(approval.ScenarioID) != "" && approval.ScenarioID != schedule.ScenarioID {
		return false, errors.New("schedule and approval scenario identifiers do not match")
	}
	approval.ScenarioID = schedule.ScenarioID
	approval.ProjectID = schedule.ProjectID
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
		result, err = tx.Exec(`UPDATE scenario_schedules SET revision=?, name=?, cron_expr=?, timezone=?, release_id=?, timeslot_id=?, run_name_template=?, enabled=?, approval_policy=?, missed_run_policy=?, conflict_summary=?, next_required_action=?, pending_approval_id=?, owner_subject=?, owner_display_name=?, governance_json=?, blackout_windows_json=?, maintenance_windows_json=?, availability_windows_json=?, last_run_test_id=?, last_run_at=?, next_run_at=?, disabled_from=?, disabled_until=?, last_error=?, updated_at=? WHERE id=? AND scenario_id=? AND project_id=? AND COALESCE(revision, 1)=?`,
			schedule.Revision, schedule.Name, schedule.CronExpr, schedule.Timezone, nullableString(schedule.ReleaseID), schedule.TimeslotID, schedule.RunNameTemplate, schedule.Enabled, schedule.ApprovalPolicy, schedule.MissedRunPolicy, schedule.ConflictSummary, schedule.NextRequiredAction, schedule.PendingApprovalID, schedule.OwnerSubject, schedule.OwnerDisplayName, governanceJSON, blackoutJSON, maintenanceJSON, availabilityJSON, nullableString(schedule.LastRunTestID), schedule.LastRunAt, schedule.NextRunAt, schedule.DisabledFrom, schedule.DisabledUntil, schedule.LastError, schedule.UpdatedAt, schedule.ID, schedule.ScenarioID, schedule.ProjectID, expectedRevision)
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
	approvalResult, err := tx.Exec(`INSERT INTO scenario_schedule_approvals (id, schedule_id, scenario_id, project_id, schedule_revision, policy, status, requested_by, requested_at, resolved_by, resolved_at, decision_reason, required_role, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET schedule_revision=excluded.schedule_revision, policy=excluded.policy, status=excluded.status,
		requested_by=excluded.requested_by, requested_at=excluded.requested_at, resolved_by=excluded.resolved_by,
		resolved_at=excluded.resolved_at, decision_reason=excluded.decision_reason, required_role=excluded.required_role,
		updated_at=excluded.updated_at
		WHERE scenario_schedule_approvals.schedule_id=excluded.schedule_id
			AND scenario_schedule_approvals.scenario_id=excluded.scenario_id
			AND scenario_schedule_approvals.project_id=excluded.project_id`,
		approval.ID, approval.ScheduleID, approval.ScenarioID, approval.ProjectID, approval.ScheduleRevision, approval.Policy, approval.Status, approval.RequestedBy, approval.RequestedAt, approval.ResolvedBy, approval.ResolvedAt, approval.DecisionReason, approval.RequiredRole, approval.CreatedAt, approval.UpdatedAt)
	if err != nil {
		return false, err
	}
	approvalAffected, err := approvalResult.RowsAffected()
	if err != nil {
		return false, err
	}
	if approvalAffected != 1 {
		return false, errors.New("schedule approval belongs to another schedule")
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// CreateScenarioScheduleIfAbsent creates a schedule without replacing an
// existing row. This is the create half of the schedule CAS contract.
func (s *SQLiteStore) CreateScenarioScheduleIfAbsent(schedule *ScenarioSchedule) (bool, error) {
	return createScenarioScheduleIfAbsentWithExecutor(s.db, schedule)
}

func createScenarioScheduleIfAbsentWithExecutor(executor testSaveExecutor, schedule *ScenarioSchedule) (bool, error) {
	governanceJSON, blackoutJSON, maintenanceJSON, availabilityJSON, err := prepareScenarioSchedule(schedule)
	if err != nil {
		return false, err
	}
	if err := bindScenarioScheduleParent(executor, schedule); err != nil {
		return false, err
	}
	result, err := executor.Exec(`INSERT INTO scenario_schedules (id, scenario_id, project_id, revision, name, cron_expr, timezone, release_id, timeslot_id, run_name_template, enabled, approval_policy, missed_run_policy, conflict_summary, next_required_action, pending_approval_id, owner_subject, owner_display_name, governance_json, blackout_windows_json, maintenance_windows_json, availability_windows_json, last_run_test_id, last_run_at, next_run_at, disabled_from, disabled_until, last_error, created_at, updated_at)
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
	return updateScenarioScheduleIfRevisionWithExecutor(s.db, schedule, expectedRevision)
}

func updateScenarioScheduleIfRevisionWithExecutor(executor testSaveExecutor, schedule *ScenarioSchedule, expectedRevision int) (bool, error) {
	governanceJSON, blackoutJSON, maintenanceJSON, availabilityJSON, err := prepareScenarioSchedule(schedule)
	if err != nil {
		return false, err
	}
	if expectedRevision < 1 {
		return false, errors.New("expected scenario schedule revision must be positive")
	}
	if err := bindScenarioScheduleParent(executor, schedule); err != nil {
		return false, err
	}
	nextRevision := expectedRevision + 1
	result, err := executor.Exec(`UPDATE scenario_schedules SET revision=?, name=?, cron_expr=?, timezone=?, release_id=?, timeslot_id=?, run_name_template=?, enabled=?, approval_policy=?, missed_run_policy=?, conflict_summary=?, next_required_action=?, pending_approval_id=?, owner_subject=?, owner_display_name=?, governance_json=?, blackout_windows_json=?, maintenance_windows_json=?, availability_windows_json=?, last_run_test_id=?, last_run_at=?, next_run_at=?, disabled_from=?, disabled_until=?, last_error=?, updated_at=? WHERE id=? AND scenario_id=? AND project_id=? AND COALESCE(revision, 1)=?`,
		nextRevision, schedule.Name, schedule.CronExpr, schedule.Timezone, nullableString(schedule.ReleaseID), schedule.TimeslotID, schedule.RunNameTemplate, schedule.Enabled, schedule.ApprovalPolicy, schedule.MissedRunPolicy, schedule.ConflictSummary, schedule.NextRequiredAction, schedule.PendingApprovalID, schedule.OwnerSubject, schedule.OwnerDisplayName, governanceJSON, blackoutJSON, maintenanceJSON, availabilityJSON,
		nullableString(schedule.LastRunTestID), schedule.LastRunAt, schedule.NextRunAt, schedule.DisabledFrom, schedule.DisabledUntil, schedule.LastError, schedule.UpdatedAt,
		schedule.ID, schedule.ScenarioID, schedule.ProjectID, expectedRevision)
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

// CreateScenarioScheduleIfAbsentAndLeaseHeld and
// UpdateScenarioScheduleIfRevisionAndLeaseHeld combine the execution-epoch
// fence with the schedule CAS in one transaction. A sweep that loses execution
// leadership while blocked in storage therefore cannot create or advance
// schedule state after a successor takes over.
func (s *SQLiteStore) CreateScenarioScheduleIfAbsentAndLeaseHeld(schedule *ScenarioSchedule, leaseName, leaseOwnerID string) (bool, error) {
	return s.mutateScenarioScheduleIfLeaseHeld(leaseName, leaseOwnerID, func(tx *storeTx) (bool, error) {
		return createScenarioScheduleIfAbsentWithExecutor(tx, schedule)
	})
}

func (s *SQLiteStore) UpdateScenarioScheduleIfRevisionAndLeaseHeld(schedule *ScenarioSchedule, expectedRevision int, leaseName, leaseOwnerID string) (bool, error) {
	originalRevision := schedule.Revision
	changed, err := s.mutateScenarioScheduleIfLeaseHeld(leaseName, leaseOwnerID, func(tx *storeTx) (bool, error) {
		return updateScenarioScheduleIfRevisionWithExecutor(tx, schedule, expectedRevision)
	})
	if err != nil || !changed {
		schedule.Revision = originalRevision
	}
	return changed, err
}

func (s *SQLiteStore) CreateScenarioScheduleIfAbsentAndLeaseTermHeld(schedule *ScenarioSchedule, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error) {
	tx, err := s.beginExecutionLeaseTermTransaction(leaseName, leaseOwnerID, leaseGeneration)
	if err != nil || tx == nil {
		return false, err
	}
	defer tx.Rollback()
	created, err := createScenarioScheduleIfAbsentWithExecutor(tx, schedule)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return created, nil
}

func (s *SQLiteStore) UpdateScenarioScheduleIfRevisionAndLeaseTermHeld(schedule *ScenarioSchedule, expectedRevision int, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error) {
	originalRevision := schedule.Revision
	tx, err := s.beginExecutionLeaseTermTransaction(leaseName, leaseOwnerID, leaseGeneration)
	if err != nil || tx == nil {
		return false, err
	}
	defer tx.Rollback()
	updated, err := updateScenarioScheduleIfRevisionWithExecutor(tx, schedule, expectedRevision)
	if err != nil || !updated {
		schedule.Revision = originalRevision
		return updated, err
	}
	if err := tx.Commit(); err != nil {
		schedule.Revision = originalRevision
		return false, err
	}
	return true, nil
}

func (s *SQLiteStore) mutateScenarioScheduleIfLeaseHeld(leaseName, leaseOwnerID string, mutate func(*storeTx) (bool, error)) (bool, error) {
	leaseName = strings.TrimSpace(leaseName)
	leaseOwnerID = strings.TrimSpace(leaseOwnerID)
	if leaseName == "" || leaseOwnerID == "" {
		return false, errors.New("lease name and owner token are required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	held, err := schedulerLeaseHeldInTransaction(tx, s.db.dialect, leaseName, leaseOwnerID)
	if err != nil || !held {
		return false, err
	}
	changed, err := mutate(tx)
	if err != nil || !changed {
		return changed, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
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

// bindScenarioScheduleParent derives project ownership from the durable
// scenario graph and rejects attempts to reuse an existing schedule ID under a
// different scenario. Scenario rows do not carry project_id themselves, so the
// script membership remains the source of truth.
func bindScenarioScheduleParent(executor testSaveExecutor, schedule *ScenarioSchedule) error {
	var scenarioCount, distinctProjects int
	var minProjectID, maxProjectID string
	if err := executor.QueryRow(`SELECT COUNT(DISTINCT sc.id),
		COUNT(DISTINCT COALESCE(NULLIF(s.project_id, ''), ?)),
		COALESCE(MIN(COALESCE(NULLIF(s.project_id, ''), ?)), ?),
		COALESCE(MAX(COALESCE(NULLIF(s.project_id, ''), ?)), ?)
		FROM scenarios sc
		LEFT JOIN scenario_scripts ss ON ss.scenario_id = sc.id
		LEFT JOIN scripts s ON s.id = ss.script_id
		WHERE sc.id = ?`, DefaultProjectID, DefaultProjectID, DefaultProjectID, DefaultProjectID, DefaultProjectID, schedule.ScenarioID).
		Scan(&scenarioCount, &distinctProjects, &minProjectID, &maxProjectID); err != nil {
		return err
	}
	if scenarioCount != 1 {
		return fmt.Errorf("scenario %q does not exist", schedule.ScenarioID)
	}
	if distinctProjects != 1 || normalizeProjectID(minProjectID) != normalizeProjectID(maxProjectID) {
		return fmt.Errorf("scenario %q scripts span multiple projects", schedule.ScenarioID)
	}
	schedule.ProjectID = normalizeProjectID(minProjectID)
	var existingScenarioID string
	if err := executor.QueryRow(`SELECT scenario_id FROM scenario_schedules WHERE id = ?`, schedule.ID).Scan(&existingScenarioID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	if existingScenarioID != schedule.ScenarioID {
		return fmt.Errorf("scenario schedule %q belongs to scenario %q", schedule.ID, existingScenarioID)
	}
	return nil
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
	result, err := s.DeleteScenarioScheduleGuarded(id)
	if err != nil {
		return err
	}
	if result == ScheduleDeleteBlockedByActiveOccurrence {
		return fmt.Errorf("scenario schedule has a nonterminal occurrence")
	}
	return nil
}

type scenarioScheduleDeleteExecutor interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
}

func deleteScenarioScheduleGuardedWithExecutor(executor scenarioScheduleDeleteExecutor, id string) (ScheduleDeleteResult, error) {
	// The no-op write serializes this decision with occurrence inserts on SQLite
	// and establishes parent-row ownership before the status check on PostgreSQL.
	locked, err := executor.Exec(`UPDATE scenario_schedules SET updated_at = updated_at WHERE id = ?`, id)
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
	if err := executor.QueryRow(`SELECT COUNT(*) FROM scenario_schedule_occurrences
		WHERE schedule_id = ? AND LOWER(TRIM(COALESCE(status, ''))) NOT IN ('materialized', 'failed', 'blocked', 'skipped')`, id).Scan(&active); err != nil {
		return "", err
	}
	if active > 0 {
		return ScheduleDeleteBlockedByActiveOccurrence, nil
	}
	if _, err := executor.Exec(`DELETE FROM scenario_schedule_approvals WHERE schedule_id = ?`, id); err != nil {
		return "", err
	}
	if _, err := executor.Exec(`DELETE FROM scenario_schedule_occurrences WHERE schedule_id = ?`, id); err != nil {
		return "", err
	}
	result, err := executor.Exec(`DELETE FROM scenario_schedules WHERE id = ?`, id)
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

func (s *SQLiteStore) DeleteScenarioScheduleGuarded(id string) (ScheduleDeleteResult, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", errors.New("scenario schedule id is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	result, err := deleteScenarioScheduleGuardedWithExecutor(tx, id)
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

func (s *SQLiteStore) DeleteScenarioScheduleGuardedIfLeaseTermHeld(id, leaseName, leaseOwnerID string, leaseGeneration uint64) (ScheduleDeleteResult, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", errors.New("scenario schedule id is required")
	}
	tx, err := s.beginExecutionLeaseTermTransaction(leaseName, leaseOwnerID, leaseGeneration)
	if err != nil || tx == nil {
		return "", err
	}
	defer tx.Rollback()
	result, err := deleteScenarioScheduleGuardedWithExecutor(tx, id)
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

const scenarioScheduleOccurrenceColumns = `id, schedule_id, scenario_id, COALESCE(project_id, ''), COALESCE(schedule_revision, 1),
	COALESCE(revision, 1), COALESCE(transition_token, ''), planned_at, materialized_at, status,
	COALESCE(reason_code, ''), COALESCE(test_id, ''), COALESCE(approval_id, ''), COALESCE(conflict_summary, ''), created_at, updated_at
	FROM scenario_schedule_occurrences`

func (s *SQLiteStore) SaveScenarioScheduleOccurrence(occurrence *ScenarioScheduleOccurrence) error {
	expectedRevision := int64(1)
	if occurrence != nil && occurrence.Revision > 0 {
		expectedRevision = occurrence.Revision
	}
	created, err := s.CreateScenarioScheduleOccurrenceIfAbsent(occurrence)
	if err != nil || created {
		return err
	}
	occurrence.Revision = expectedRevision
	updated, err := s.CompareAndSwapScenarioScheduleOccurrence(occurrence, expectedRevision)
	if err != nil {
		return err
	}
	if !updated {
		return errors.New("scenario schedule occurrence changed or was deleted concurrently")
	}
	return nil
}

func prepareScenarioScheduleOccurrence(occurrence *ScenarioScheduleOccurrence) error {
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
	if occurrence.Revision < 1 {
		occurrence.Revision = 1
	}
	return nil
}

func (s *SQLiteStore) CreateScenarioScheduleOccurrenceIfAbsent(occurrence *ScenarioScheduleOccurrence) (bool, error) {
	return createScenarioScheduleOccurrenceIfAbsentWithExecutor(s.db, occurrence)
}

func createScenarioScheduleOccurrenceIfAbsentWithExecutor(executor testSaveExecutor, occurrence *ScenarioScheduleOccurrence) (bool, error) {
	if err := bindScenarioScheduleOccurrenceParent(executor, occurrence); err != nil {
		return false, err
	}
	if err := prepareScenarioScheduleOccurrence(occurrence); err != nil {
		return false, err
	}
	occurrence.Revision = 1
	result, err := executor.Exec(`INSERT INTO scenario_schedule_occurrences (id, schedule_id, scenario_id, project_id, schedule_revision, revision, transition_token, planned_at, materialized_at, status, reason_code, test_id, approval_id, conflict_summary, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(schedule_id, planned_at) DO NOTHING`,
		occurrence.ID, occurrence.ScheduleID, occurrence.ScenarioID, occurrence.ProjectID, occurrence.ScheduleRevision,
		occurrence.Revision, occurrence.TransitionToken, occurrence.PlannedAt, occurrence.MaterializedAt, occurrence.Status,
		occurrence.ReasonCode, nullableString(occurrence.TestID), occurrence.ApprovalID, occurrence.ConflictSummary, occurrence.CreatedAt, occurrence.UpdatedAt)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}

// ClaimScenarioScheduleOccurrenceIfCurrent is the scheduler-only create path.
// The occurrence and the parent schedule predicate are evaluated by one SQL
// statement, so a stale sweep cannot claim work after a concurrent disable,
// reschedule, or revision change has already committed.
func (s *SQLiteStore) ClaimScenarioScheduleOccurrenceIfCurrent(occurrence *ScenarioScheduleOccurrence) (bool, error) {
	return claimScenarioScheduleOccurrenceIfCurrentWithExecutor(s.db, occurrence)
}

func claimScenarioScheduleOccurrenceIfCurrentWithExecutor(executor testSaveExecutor, occurrence *ScenarioScheduleOccurrence) (bool, error) {
	if err := bindScenarioScheduleOccurrenceParent(executor, occurrence); err != nil {
		return false, err
	}
	if err := prepareScenarioScheduleOccurrence(occurrence); err != nil {
		return false, err
	}
	occurrence.Revision = 1
	result, err := executor.Exec(`INSERT INTO scenario_schedule_occurrences (id, schedule_id, scenario_id, project_id, schedule_revision, revision, transition_token, planned_at, materialized_at, status, reason_code, test_id, approval_id, conflict_summary, created_at, updated_at)
		SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
		FROM scenario_schedules
		WHERE id = ?
			AND COALESCE(revision, 1) = ?
			AND COALESCE(enabled, 1) = ?
			AND next_run_at = ?
		ON CONFLICT(schedule_id, planned_at) DO NOTHING`,
		occurrence.ID, occurrence.ScheduleID, occurrence.ScenarioID, occurrence.ProjectID, occurrence.ScheduleRevision,
		occurrence.Revision, occurrence.TransitionToken, occurrence.PlannedAt, occurrence.MaterializedAt, occurrence.Status,
		occurrence.ReasonCode, nullableString(occurrence.TestID), occurrence.ApprovalID, occurrence.ConflictSummary, occurrence.CreatedAt, occurrence.UpdatedAt,
		occurrence.ScheduleID, occurrence.ScheduleRevision, true, occurrence.PlannedAt)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}

func (s *SQLiteStore) CreateScenarioScheduleOccurrenceIfAbsentAndLeaseTermHeld(occurrence *ScenarioScheduleOccurrence, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error) {
	tx, err := s.beginExecutionLeaseTermTransaction(leaseName, leaseOwnerID, leaseGeneration)
	if err != nil || tx == nil {
		return false, err
	}
	defer tx.Rollback()
	created, err := createScenarioScheduleOccurrenceIfAbsentWithExecutor(tx, occurrence)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return created, nil
}

func (s *SQLiteStore) ClaimScenarioScheduleOccurrenceIfCurrentAndLeaseTermHeld(occurrence *ScenarioScheduleOccurrence, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error) {
	tx, err := s.beginExecutionLeaseTermTransaction(leaseName, leaseOwnerID, leaseGeneration)
	if err != nil || tx == nil {
		return false, err
	}
	defer tx.Rollback()
	created, err := claimScenarioScheduleOccurrenceIfCurrentWithExecutor(tx, occurrence)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return created, nil
}

func (s *SQLiteStore) CompareAndSwapScenarioScheduleOccurrence(occurrence *ScenarioScheduleOccurrence, expectedRevision int64) (bool, error) {
	return compareAndSwapScenarioScheduleOccurrenceWithExecutor(s.db, occurrence, expectedRevision)
}

func compareAndSwapScenarioScheduleOccurrenceWithExecutor(executor testSaveExecutor, occurrence *ScenarioScheduleOccurrence, expectedRevision int64) (bool, error) {
	if expectedRevision < 1 {
		return false, errors.New("scenario schedule occurrence expected revision must be positive")
	}
	if err := bindScenarioScheduleOccurrenceParent(executor, occurrence); err != nil {
		return false, err
	}
	if err := prepareScenarioScheduleOccurrence(occurrence); err != nil {
		return false, err
	}
	result, err := executor.Exec(`UPDATE scenario_schedule_occurrences SET
		materialized_at=?, status=?, reason_code=?, test_id=?, approval_id=?, conflict_summary=?, transition_token=?, revision=revision+1, updated_at=?
		WHERE id=? AND schedule_id=? AND planned_at=? AND COALESCE(revision, 1)=?`,
		occurrence.MaterializedAt, occurrence.Status, occurrence.ReasonCode, nullableString(occurrence.TestID), occurrence.ApprovalID,
		occurrence.ConflictSummary, occurrence.TransitionToken, occurrence.UpdatedAt, occurrence.ID, occurrence.ScheduleID, occurrence.PlannedAt, expectedRevision)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return false, err
	}
	occurrence.Revision = expectedRevision + 1
	return true, nil
}

func (s *SQLiteStore) CompareAndSwapScenarioScheduleOccurrenceIfLeaseTermHeld(occurrence *ScenarioScheduleOccurrence, expectedRevision int64, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error) {
	tx, err := s.beginExecutionLeaseTermTransaction(leaseName, leaseOwnerID, leaseGeneration)
	if err != nil || tx == nil {
		return false, err
	}
	defer tx.Rollback()
	updated, err := compareAndSwapScenarioScheduleOccurrenceWithExecutor(tx, occurrence, expectedRevision)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return updated, nil
}

func (s *SQLiteStore) SaveScenarioScheduleOccurrenceIfLeaseHeld(occurrence *ScenarioScheduleOccurrence, leaseName, leaseOwnerID string) (bool, error) {
	leaseName = strings.TrimSpace(leaseName)
	leaseOwnerID = strings.TrimSpace(leaseOwnerID)
	if leaseName == "" || leaseOwnerID == "" {
		return false, errors.New("lease name and owner token are required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	held, err := schedulerLeaseHeldInTransaction(tx, s.db.dialect, leaseName, leaseOwnerID)
	if err != nil || !held {
		return false, err
	}
	expectedRevision := occurrence.Revision
	if expectedRevision < 1 {
		expectedRevision = 1
	}
	created, err := createScenarioScheduleOccurrenceIfAbsentWithExecutor(tx, occurrence)
	if err != nil {
		return false, err
	}
	if !created {
		occurrence.Revision = expectedRevision
		updated, updateErr := compareAndSwapScenarioScheduleOccurrenceWithExecutor(tx, occurrence, expectedRevision)
		if updateErr != nil || !updated {
			return false, updateErr
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *SQLiteStore) GetScenarioScheduleOccurrence(id string) (*ScenarioScheduleOccurrence, error) {
	row := s.db.QueryRow(`SELECT `+scenarioScheduleOccurrenceColumns+` WHERE id = ?`, id)
	return scanScenarioScheduleOccurrence(row)
}

func (s *SQLiteStore) GetScenarioScheduleOccurrenceByScheduleAndPlannedAt(scheduleID string, plannedAt time.Time) (*ScenarioScheduleOccurrence, error) {
	row := s.db.QueryRow(`SELECT `+scenarioScheduleOccurrenceColumns+` WHERE schedule_id = ? AND planned_at = ?`, scheduleID, plannedAt)
	occurrence, err := scanScenarioScheduleOccurrence(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return occurrence, err
}

func (s *SQLiteStore) ListScenarioScheduleOccurrences(scheduleID string) ([]*ScenarioScheduleOccurrence, error) {
	return s.listScenarioScheduleOccurrencesPage(scheduleID, 0, 0)
}

// ListScenarioScheduleOccurrencesPage bounds schedule history reads exposed by
// the controller API. The unpaged method remains available for internal
// recovery paths that intentionally need the complete history.
func (s *SQLiteStore) ListScenarioScheduleOccurrencesPage(scheduleID string, limit, offset int) ([]*ScenarioScheduleOccurrence, error) {
	if limit <= 0 {
		return []*ScenarioScheduleOccurrence{}, nil
	}
	if offset < 0 {
		offset = 0
	}
	return s.listScenarioScheduleOccurrencesPage(scheduleID, limit, offset)
}

func (s *SQLiteStore) listScenarioScheduleOccurrencesPage(scheduleID string, limit, offset int) ([]*ScenarioScheduleOccurrence, error) {
	query := `SELECT ` + scenarioScheduleOccurrenceColumns + ` WHERE schedule_id = ? ORDER BY planned_at DESC`
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
	return collectScenarioScheduleOccurrences(rows)
}

func (s *SQLiteStore) LatestScenarioScheduleOccurrence(scheduleID string) (*ScenarioScheduleOccurrence, error) {
	row := s.db.QueryRow(`SELECT `+scenarioScheduleOccurrenceColumns+` WHERE schedule_id = ? ORDER BY planned_at DESC LIMIT 1`, scheduleID)
	occurrence, err := scanScenarioScheduleOccurrence(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return occurrence, err
}

func (s *SQLiteStore) ListAllScenarioScheduleOccurrences() ([]*ScenarioScheduleOccurrence, error) {
	rows, err := s.db.Query(`SELECT ` + scenarioScheduleOccurrenceColumns + ` ORDER BY planned_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectScenarioScheduleOccurrences(rows)
}

func (s *SQLiteStore) ListScenarioScheduleOccurrencesByTimeRange(projectID string, from time.Time, to time.Time) ([]*ScenarioScheduleOccurrence, error) {
	projectID = normalizeProjectID(projectID)
	query := `SELECT ` + scenarioScheduleOccurrenceColumns + ` WHERE planned_at >= ? AND planned_at < ?`
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
	result, err := s.DeleteScenarioScheduleOccurrenceGuarded(id)
	if err != nil {
		return err
	}
	if result == ScheduleOccurrenceDeleteBlockedByStatus {
		return fmt.Errorf("scenario schedule occurrence is not terminal")
	}
	return nil
}

func (s *SQLiteStore) DeleteScenarioScheduleOccurrenceGuarded(id string) (ScheduleOccurrenceDeleteResult, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", errors.New("scenario schedule occurrence id is required")
	}
	result, err := s.db.Exec(`DELETE FROM scenario_schedule_occurrences
		WHERE id = ?
		  AND LOWER(TRIM(COALESCE(status, ''))) IN ('materialized', 'failed', 'blocked', 'skipped')
		  AND EXISTS (
			SELECT 1 FROM scenario_schedules s
			WHERE s.id = scenario_schedule_occurrences.schedule_id
			  AND (s.next_run_at IS NULL OR s.next_run_at > scenario_schedule_occurrences.planned_at)
		  )`, id)
	if err != nil {
		return "", err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return "", err
	}
	if affected == 1 {
		return ScheduleOccurrenceDeleteDeleted, nil
	}
	var exists int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM scenario_schedule_occurrences WHERE id = ?`, id).Scan(&exists); err != nil {
		return "", err
	}
	if exists == 0 {
		return ScheduleOccurrenceDeleteMissing, nil
	}
	return ScheduleOccurrenceDeleteBlockedByStatus, nil
}

func (s *SQLiteStore) SaveScenarioScheduleApproval(approval *ScenarioScheduleApproval) error {
	if err := bindScenarioScheduleApprovalParent(s.db, approval); err != nil {
		return err
	}
	if err := prepareScenarioScheduleApproval(approval); err != nil {
		return err
	}
	result, err := s.db.Exec(`INSERT INTO scenario_schedule_approvals (id, schedule_id, scenario_id, project_id, schedule_revision, policy, status, requested_by, requested_at, resolved_by, resolved_at, decision_reason, required_role, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET schedule_revision=excluded.schedule_revision, policy=excluded.policy, status=excluded.status,
		requested_by=excluded.requested_by, requested_at=excluded.requested_at, resolved_by=excluded.resolved_by,
		resolved_at=excluded.resolved_at, decision_reason=excluded.decision_reason, required_role=excluded.required_role,
		updated_at=excluded.updated_at
		WHERE scenario_schedule_approvals.schedule_id=excluded.schedule_id
			AND scenario_schedule_approvals.scenario_id=excluded.scenario_id
			AND scenario_schedule_approvals.project_id=excluded.project_id`,
		approval.ID, approval.ScheduleID, approval.ScenarioID, approval.ProjectID, approval.ScheduleRevision, approval.Policy, approval.Status, approval.RequestedBy, approval.RequestedAt, approval.ResolvedBy, approval.ResolvedAt, approval.DecisionReason, approval.RequiredRole, approval.CreatedAt, approval.UpdatedAt)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return errors.New("schedule approval belongs to another schedule")
	}
	return nil
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

func bindScenarioScheduleOccurrenceParent(executor testSaveExecutor, occurrence *ScenarioScheduleOccurrence) error {
	if occurrence == nil {
		return errors.New("schedule occurrence is required")
	}
	var scenarioID, projectID string
	var scheduleRevision int
	if err := executor.QueryRow(`SELECT scenario_id, COALESCE(project_id, ''), COALESCE(revision, 1) FROM scenario_schedules WHERE id = ?`, occurrence.ScheduleID).
		Scan(&scenarioID, &projectID, &scheduleRevision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("scenario schedule %q does not exist", occurrence.ScheduleID)
		}
		return err
	}
	occurrence.ScenarioID = scenarioID
	occurrence.ProjectID = normalizeProjectID(projectID)
	if occurrence.ScheduleRevision < 1 {
		occurrence.ScheduleRevision = scheduleRevision
	}
	return nil
}

func bindScenarioScheduleApprovalParent(executor testSaveExecutor, approval *ScenarioScheduleApproval) error {
	if approval == nil {
		return errors.New("schedule approval is required")
	}
	var scenarioID, projectID string
	var scheduleRevision int
	if err := executor.QueryRow(`SELECT scenario_id, COALESCE(project_id, ''), COALESCE(revision, 1) FROM scenario_schedules WHERE id = ?`, approval.ScheduleID).
		Scan(&scenarioID, &projectID, &scheduleRevision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("scenario schedule %q does not exist", approval.ScheduleID)
		}
		return err
	}
	approval.ScenarioID = scenarioID
	approval.ProjectID = normalizeProjectID(projectID)
	if approval.ScheduleRevision < 1 {
		approval.ScheduleRevision = scheduleRevision
	}
	return nil
}

func (s *SQLiteStore) GetScenarioScheduleApproval(id string) (*ScenarioScheduleApproval, error) {
	row := s.db.QueryRow(`SELECT id, schedule_id, scenario_id, COALESCE(project_id, ''), COALESCE(schedule_revision, 1), policy, status, COALESCE(requested_by, ''), requested_at, COALESCE(resolved_by, ''), resolved_at, COALESCE(decision_reason, ''), COALESCE(required_role, ''), created_at, updated_at FROM scenario_schedule_approvals WHERE id = ?`, id)
	return scanScenarioScheduleApproval(row)
}

func (s *SQLiteStore) ListScenarioScheduleApprovals(scheduleID string) ([]*ScenarioScheduleApproval, error) {
	return s.listScenarioScheduleApprovalsPage(scheduleID, 0, 0)
}

// ListScenarioScheduleApprovalsPage bounds the durable approval history read.
func (s *SQLiteStore) ListScenarioScheduleApprovalsPage(scheduleID string, limit, offset int) ([]*ScenarioScheduleApproval, error) {
	if limit <= 0 {
		return []*ScenarioScheduleApproval{}, nil
	}
	if offset < 0 {
		offset = 0
	}
	return s.listScenarioScheduleApprovalsPage(scheduleID, limit, offset)
}

func (s *SQLiteStore) listScenarioScheduleApprovalsPage(scheduleID string, limit, offset int) ([]*ScenarioScheduleApproval, error) {
	query := `SELECT id, schedule_id, scenario_id, COALESCE(project_id, ''), COALESCE(schedule_revision, 1), policy, status, COALESCE(requested_by, ''), requested_at, COALESCE(resolved_by, ''), resolved_at, COALESCE(decision_reason, ''), COALESCE(required_role, ''), created_at, updated_at FROM scenario_schedule_approvals WHERE schedule_id = ? ORDER BY requested_at DESC`
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
	acquired, _, err := s.TryAcquireSchedulerLeaseTerm(name, ownerID, time.Time{}, ttl)
	return acquired, err
}

// TryAcquireSchedulerLeaseWithGeneration acquires or renews a scheduler lease
// and returns its durable fencing generation. The generation advances only
// when ownership moves to a different owner token; ordinary renewals by the
// same owner preserve both the generation and original acquisition time.
//
// ReleaseSchedulerLease deliberately expires rather than deletes the row so a
// later owner cannot restart the fencing sequence at one.
func (s *SQLiteStore) TryAcquireSchedulerLeaseWithGeneration(name string, ownerID string, _ time.Time, ttl time.Duration) (bool, uint64, error) {
	acquired, lease, err := s.TryAcquireSchedulerLeaseTerm(name, ownerID, time.Time{}, ttl)
	if err != nil || !acquired || lease == nil {
		return acquired, 0, err
	}
	return true, lease.Generation, nil
}

// TryAcquireSchedulerLeaseTerm returns the complete lease term read in the same
// transaction as acquisition. The owner and generation form an atomic fencing
// term; timestamps remain useful database metadata, while callers maintain a
// conservative request-anchored deadline in their own monotonic clock domain.
func (s *SQLiteStore) TryAcquireSchedulerLeaseTerm(name string, ownerID string, _ time.Time, ttl time.Duration) (bool, *SchedulerLease, error) {
	name = strings.TrimSpace(name)
	ownerID = strings.TrimSpace(ownerID)
	if name == "" {
		return false, nil, errors.New("scheduler lease name is required")
	}
	if ownerID == "" {
		return false, nil, errors.New("scheduler lease owner_id is required")
	}
	if ttl <= 0 {
		return false, nil, errors.New("scheduler lease ttl must be positive")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, nil, err
	}
	defer tx.Rollback()
	now, err := preciseDatabaseTime(tx, s.db.dialect)
	if err != nil {
		return false, nil, fmt.Errorf("read scheduler database clock: %w", err)
	}
	expiresAt := now.Add(ttl)
	const maxLeaseGeneration = int64(^uint64(0) >> 1)
	result, err := tx.Exec(`INSERT INTO scheduler_leases (name, owner_id, generation, acquired_at, expires_at, updated_at)
		VALUES (?, ?, 1, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
		owner_id=excluded.owner_id,
		generation=CASE WHEN scheduler_leases.owner_id = excluded.owner_id THEN scheduler_leases.generation ELSE scheduler_leases.generation + 1 END,
		acquired_at=CASE WHEN scheduler_leases.owner_id = excluded.owner_id THEN scheduler_leases.acquired_at ELSE excluded.acquired_at END,
		expires_at=excluded.expires_at,
		updated_at=excluded.updated_at
		WHERE (scheduler_leases.expires_at <= ? OR scheduler_leases.owner_id = ?)
		AND scheduler_leases.generation > 0
		AND (scheduler_leases.owner_id = ? OR scheduler_leases.generation < ?)`,
		name, ownerID, now, expiresAt, now, now, ownerID, ownerID, maxLeaseGeneration)
	if err != nil {
		return false, nil, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, nil, err
	}
	if affected == 0 {
		var (
			currentOwner      string
			currentGeneration int64
			currentExpiresAt  time.Time
		)
		queryErr := tx.QueryRow(`SELECT owner_id, generation, expires_at FROM scheduler_leases WHERE name = ?`, name).
			Scan(&currentOwner, &currentGeneration, &currentExpiresAt)
		if queryErr != nil && !errors.Is(queryErr, sql.ErrNoRows) {
			return false, nil, queryErr
		}
		if queryErr == nil && currentGeneration <= 0 {
			return false, nil, fmt.Errorf("scheduler lease %q has invalid generation %d", name, currentGeneration)
		}
		if queryErr == nil && currentOwner != ownerID && !currentExpiresAt.After(now) && currentGeneration >= maxLeaseGeneration {
			return false, nil, fmt.Errorf("scheduler lease %q generation exhausted at %d", name, currentGeneration)
		}
		return false, nil, nil
	}
	var lease SchedulerLease
	var storedGeneration int64
	if err := tx.QueryRow(`SELECT name, owner_id, generation, acquired_at, expires_at, updated_at FROM scheduler_leases WHERE name = ? AND owner_id = ?`, name, ownerID).
		Scan(&lease.Name, &lease.OwnerID, &storedGeneration, &lease.AcquiredAt, &lease.ExpiresAt, &lease.UpdatedAt); err != nil {
		return false, nil, err
	}
	if storedGeneration <= 0 {
		return false, nil, fmt.Errorf("scheduler lease %q has invalid generation %d", name, storedGeneration)
	}
	lease.Generation = uint64(storedGeneration)
	if err := tx.Commit(); err != nil {
		return false, nil, err
	}
	return true, &lease, nil
}

// ReleaseSchedulerLease relinquishes only a lease still owned by ownerID. The
// ownership predicate prevents a delayed shutdown from expiring a successor's
// lease after failover.
func (s *SQLiteStore) ReleaseSchedulerLease(name string, ownerID string) error {
	name = strings.TrimSpace(name)
	ownerID = strings.TrimSpace(ownerID)
	if name == "" || ownerID == "" {
		return errors.New("scheduler lease name and owner_id are required")
	}
	// Keep the row (and therefore its monotonic generation) while making it
	// immediately eligible for takeover. A delayed release remains fenced by the
	// exact owner predicate and cannot expire a successor's lease.
	_, err := s.db.Exec(`UPDATE scheduler_leases SET expires_at = updated_at WHERE name = ? AND owner_id = ?`, name, ownerID)
	return err
}

func (s *SQLiteStore) GetSchedulerLease(name string) (*SchedulerLease, error) {
	var lease SchedulerLease
	err := s.db.QueryRow(`SELECT name, owner_id, generation, acquired_at, expires_at, updated_at FROM scheduler_leases WHERE name = ?`, name).Scan(&lease.Name, &lease.OwnerID, &lease.Generation, &lease.AcquiredAt, &lease.ExpiresAt, &lease.UpdatedAt)
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
		&occurrence.Revision,
		&occurrence.TransitionToken,
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
	if occurrence.Revision < 1 {
		occurrence.Revision = 1
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
