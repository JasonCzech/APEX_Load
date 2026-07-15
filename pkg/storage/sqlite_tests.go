package storage

// SQLite store — tests. Split from sqlite.go (same package, behavior-preserving).

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *SQLiteStore) SaveTest(test *TestRecord) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.saveTestWithExecutor(tx, test); err != nil {
		return err
	}
	return tx.Commit()
}

// CreateTestIfAbsent persists a newly materialized test without allowing a
// deterministic/requested ID to replace an existing lifecycle row.
func (s *SQLiteStore) CreateTestIfAbsent(test *TestRecord) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	created, err := s.createTestIfAbsentWithExecutor(tx, test)
	if err != nil || !created {
		return created, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// CreateTestIfAbsentIfLeaseHeld combines the create-only identity fence with
// the controller execution lease. A stale controller can neither insert a new
// row nor turn an ID collision into an update of a successor-owned run.
func (s *SQLiteStore) CreateTestIfAbsentIfLeaseHeld(test *TestRecord, leaseName, leaseOwnerID string) (bool, error) {
	leaseName = strings.TrimSpace(leaseName)
	leaseOwnerID = strings.TrimSpace(leaseOwnerID)
	if test == nil {
		return false, fmt.Errorf("test is required")
	}
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
	created, err := s.createTestIfAbsentWithExecutor(tx, test)
	if err != nil || !created {
		return created, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *SQLiteStore) CreateTestIfAbsentIfLeaseTermHeld(test *TestRecord, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error) {
	if test == nil {
		return false, fmt.Errorf("test is required")
	}
	tx, err := s.beginExecutionLeaseTermTransaction(leaseName, leaseOwnerID, leaseGeneration)
	if err != nil || tx == nil {
		return false, err
	}
	defer tx.Rollback()
	created, err := s.createTestIfAbsentWithExecutor(tx, test)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return created, nil
}

type testSaveExecutor interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
}

func (s *SQLiteStore) saveTestWithExecutor(executor testSaveExecutor, test *TestRecord) error {
	args, err := prepareTestSaveArgs(test)
	if err != nil {
		return err
	}
	exists, err := validateTestPlacement(executor, test, true)
	if err != nil {
		return err
	}
	if !exists {
		if err := lockActiveReleaseForNewTest(executor, test); err != nil {
			return err
		}
	}

	query := `INSERT INTO tests (id, ref_key, materialization_key, name, status, application_id, release_id, scenario_id, project_id, folder_id, scenario_mode, shakeout_enabled, resource_baseline_enabled, start_delay, assignment_json, total_vusers, duration_ns, start_time, scheduled_start_at, pending_since, paused_at, cumulative_pause_ns, capacity_window_start_at, capacity_window_end_at, baseline_pinned, baseline_pinned_at, end_time, error, created_by_subject, created_by_display_name, started_by_subject, started_by_display_name, stopped_by_subject, stopped_by_display_name, groups_json, runtime_settings_json, goal_config_json, execution_plan_approval_json, timeslot_id)
			  VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			  ON CONFLICT(id) DO UPDATE SET
			  ref_key=excluded.ref_key,
			  materialization_key=excluded.materialization_key,
			  name=excluded.name, status=excluded.status, application_id=excluded.application_id, release_id=excluded.release_id, scenario_id=excluded.scenario_id,
			  project_id=excluded.project_id, folder_id=excluded.folder_id,
			  scenario_mode=excluded.scenario_mode, shakeout_enabled=excluded.shakeout_enabled,
			  resource_baseline_enabled=excluded.resource_baseline_enabled, start_delay=excluded.start_delay, assignment_json=excluded.assignment_json, total_vusers=excluded.total_vusers,
			  duration_ns=excluded.duration_ns, start_time=excluded.start_time, scheduled_start_at=excluded.scheduled_start_at, pending_since=excluded.pending_since,
			  paused_at=excluded.paused_at, cumulative_pause_ns=excluded.cumulative_pause_ns,
			  capacity_window_start_at=excluded.capacity_window_start_at, capacity_window_end_at=excluded.capacity_window_end_at,
			  baseline_pinned=excluded.baseline_pinned, baseline_pinned_at=excluded.baseline_pinned_at, end_time=excluded.end_time,
			  error=excluded.error, created_by_subject=excluded.created_by_subject, created_by_display_name=excluded.created_by_display_name,
			  started_by_subject=excluded.started_by_subject, started_by_display_name=excluded.started_by_display_name,
			  stopped_by_subject=excluded.stopped_by_subject, stopped_by_display_name=excluded.stopped_by_display_name, groups_json=excluded.groups_json,
			  runtime_settings_json=excluded.runtime_settings_json, goal_config_json=excluded.goal_config_json, execution_plan_approval_json=excluded.execution_plan_approval_json,
			  timeslot_id=excluded.timeslot_id
			  WHERE tests.project_id=excluded.project_id`

	result, err := executor.Exec(query, args...)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("test %q project ownership changed concurrently", test.ID)
	}
	return nil
}

func (s *SQLiteStore) createTestIfAbsentWithExecutor(executor testSaveExecutor, test *TestRecord) (bool, error) {
	args, err := prepareTestSaveArgs(test)
	if err != nil {
		return false, err
	}
	exists, err := validateTestPlacement(executor, test, false)
	if err != nil {
		return false, err
	}
	// Preserve create-only collision semantics: an existing deterministic ID is
	// never rewritten, and callers receive (false, nil) regardless of the stale
	// candidate's other fields. A concurrent first insert is still fenced by the
	// INSERT ... ON CONFLICT DO NOTHING below.
	if exists {
		return false, nil
	}
	if err := lockActiveReleaseForNewTest(executor, test); err != nil {
		return false, err
	}
	result, err := executor.Exec(`INSERT INTO tests (id, ref_key, materialization_key, name, status, application_id, release_id, scenario_id, project_id, folder_id, scenario_mode, shakeout_enabled, resource_baseline_enabled, start_delay, assignment_json, total_vusers, duration_ns, start_time, scheduled_start_at, pending_since, paused_at, cumulative_pause_ns, capacity_window_start_at, capacity_window_end_at, baseline_pinned, baseline_pinned_at, end_time, error, created_by_subject, created_by_display_name, started_by_subject, started_by_display_name, stopped_by_subject, stopped_by_display_name, groups_json, runtime_settings_json, goal_config_json, execution_plan_approval_json, timeslot_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING`, args...)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

// lockActiveReleaseForNewTest serializes explicit-release test creation with
// release archival. Both paths take the same harmless row write before checking
// their invariant, so either the test commits first and blocks archival or the
// archive commits first and makes the test creation fail.
func lockActiveReleaseForNewTest(executor testSaveExecutor, test *TestRecord) error {
	if test == nil {
		return errors.New("test is required")
	}
	releaseID := normalizeReleaseID(test.ReleaseID)
	if releaseID == LegacyReleaseID {
		return nil
	}
	result, err := executor.Exec(`UPDATE releases SET updated_at = updated_at WHERE id = ?`, releaseID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("release not found: %s", releaseID)
	}
	var archivedAt sql.NullTime
	if err := executor.QueryRow(`SELECT archived_at FROM releases WHERE id = ?`, releaseID).Scan(&archivedAt); err != nil {
		return err
	}
	if archivedAt.Valid {
		return fmt.Errorf("release is archived: %s", releaseID)
	}
	return nil
}

func prepareTestSaveArgs(test *TestRecord) ([]any, error) {
	if test == nil {
		return nil, fmt.Errorf("test is required")
	}
	test.ID = strings.TrimSpace(test.ID)
	if test.ID == "" {
		return nil, errors.New("test id is required")
	}
	test.ProjectID = normalizeProjectID(strings.TrimSpace(test.ProjectID))
	test.ApplicationID = strings.TrimSpace(test.ApplicationID)
	if test.ApplicationID == "" || (test.ApplicationID == LegacyApplicationID && test.ProjectID != DefaultProjectID) {
		test.ApplicationID = legacyApplicationID(test.ProjectID)
	}
	test.ReleaseID = strings.TrimSpace(test.ReleaseID)
	if test.ReleaseID == "" || (test.ReleaseID == LegacyReleaseID && test.ProjectID != DefaultProjectID) {
		test.ReleaseID = legacyReleaseID(test.ProjectID)
	}
	test.ScenarioID = strings.TrimSpace(test.ScenarioID)
	if test.FolderID != nil {
		folderID := strings.TrimSpace(*test.FolderID)
		test.FolderID = &folderID
	}
	groupsJSON, err := marshalTestField("groups_json", test.Groups)
	if err != nil {
		return nil, err
	}
	assignmentJSON, err := marshalTestField("assignment_json", test.Assignment)
	if err != nil {
		return nil, err
	}
	runtimeSettingsJSON, err := marshalTestField("runtime_settings_json", test.RuntimeSettings)
	if err != nil {
		return nil, err
	}
	goalConfigJSON, err := marshalTestField("goal_config_json", test.GoalConfig)
	if err != nil {
		return nil, err
	}
	executionPlanApprovalJSON, err := marshalTestField("execution_plan_approval_json", test.ExecutionPlanApproval)
	if err != nil {
		return nil, err
	}
	return []any{test.ID, test.RefKey, test.MaterializationKey, test.Name, test.Status, test.ApplicationID, test.ReleaseID, nullableString(test.ScenarioID), test.ProjectID, nullableStringPointer(test.FolderID), test.ScenarioMode, test.ScriptShakeout, test.ResourceBaseline, test.StartDelay, assignmentJSON, test.TotalVUsers, test.Duration, test.StartTime, test.ScheduledStartAt, test.PendingSince, test.PausedAt, test.CumulativePause, test.CapacityWindowStartAt, test.CapacityWindowEndAt, test.BaselinePinned, test.BaselinePinnedAt, test.EndTime, test.Error, test.CreatedBySubject, test.CreatedByDisplayName, test.StartedBySubject, test.StartedByDisplayName, test.StoppedBySubject, test.StoppedByDisplayName, groupsJSON, runtimeSettingsJSON, goalConfigJSON, executionPlanApprovalJSON, nullableStringPointer(test.TimeslotID)}, nil
}

// validateTestPlacement locks and verifies every durable tenant-bearing parent
// before a test row is inserted or updated. The project lock matches project
// deletion's lock order, while the conditional upsert in saveTestWithExecutor
// remains the final fence for two concurrent first writers of the same test ID.
// It returns whether the test already exists.
func validateTestPlacement(executor testSaveExecutor, test *TestRecord, rejectRelocation bool) (bool, error) {
	if test == nil {
		return false, errors.New("test is required")
	}
	if err := lockRequiredTestParent(executor,
		`UPDATE projects SET updated_at = updated_at WHERE id = ?`, test.ProjectID, "project"); err != nil {
		return false, err
	}
	if err := ensureLegacyTestHierarchy(executor, test.ProjectID); err != nil {
		return false, err
	}

	exists, existingProjectID, err := lockExistingTestPlacement(executor, test.ID)
	if err != nil {
		return false, err
	}
	if exists && rejectRelocation && normalizeProjectID(existingProjectID) != test.ProjectID {
		return false, fmt.Errorf("test %q belongs to project %q, not %q", test.ID, normalizeProjectID(existingProjectID), test.ProjectID)
	}
	if exists && !rejectRelocation {
		return true, nil
	}

	if err := lockRequiredTestParent(executor,
		`UPDATE applications SET updated_at = updated_at WHERE id = ?`, test.ApplicationID, "application"); err != nil {
		return false, err
	}
	var applicationProjectID string
	if err := executor.QueryRow(`SELECT COALESCE(project_id, '') FROM applications WHERE id = ?`, test.ApplicationID).Scan(&applicationProjectID); err != nil {
		return false, err
	}
	if normalizeProjectID(applicationProjectID) != test.ProjectID {
		return false, fmt.Errorf("test application %q belongs to project %q, not %q", test.ApplicationID, normalizeProjectID(applicationProjectID), test.ProjectID)
	}

	if err := lockRequiredTestParent(executor,
		`UPDATE releases SET updated_at = updated_at WHERE id = ?`, test.ReleaseID, "release"); err != nil {
		return false, err
	}
	var releaseApplicationID, releaseProjectID string
	if err := executor.QueryRow(`SELECT application_id, COALESCE(project_id, '') FROM releases WHERE id = ?`, test.ReleaseID).Scan(&releaseApplicationID, &releaseProjectID); err != nil {
		return false, err
	}
	if releaseApplicationID != test.ApplicationID {
		return false, fmt.Errorf("test release %q belongs to application %q, not %q", test.ReleaseID, releaseApplicationID, test.ApplicationID)
	}
	if normalizeProjectID(releaseProjectID) != test.ProjectID {
		return false, fmt.Errorf("test release %q belongs to project %q, not %q", test.ReleaseID, normalizeProjectID(releaseProjectID), test.ProjectID)
	}

	if test.ScenarioID != "" {
		if err := validateScenarioProjectPlacement(executor, test.ScenarioID, test.ProjectID, "test"); err != nil {
			return false, err
		}
	}
	if test.FolderID != nil && strings.TrimSpace(*test.FolderID) != "" {
		folderID := strings.TrimSpace(*test.FolderID)
		if err := lockRequiredTestParent(executor,
			`UPDATE folders SET updated_at = updated_at WHERE id = ?`, folderID, "folder"); err != nil {
			return false, err
		}
		var folderProjectID string
		if err := executor.QueryRow(`SELECT project_id FROM folders WHERE id = ?`, folderID).Scan(&folderProjectID); err != nil {
			return false, err
		}
		if normalizeProjectID(folderProjectID) != test.ProjectID {
			return false, fmt.Errorf("test folder %q belongs to project %q, not %q", folderID, normalizeProjectID(folderProjectID), test.ProjectID)
		}
	}
	return exists, nil
}

func lockRequiredTestParent(executor testSaveExecutor, query, id, kind string) error {
	return lockRequiredPlacementParent(executor, query, id, "test", kind)
}

func lockRequiredPlacementParent(executor testSaveExecutor, query, id, owner, kind string) error {
	result, err := executor.Exec(query, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("%s %s not found: %s", owner, kind, id)
	}
	return nil
}

func lockExistingTestPlacement(executor testSaveExecutor, testID string) (bool, string, error) {
	result, err := executor.Exec(`UPDATE tests SET project_id = project_id WHERE id = ?`, testID)
	if err != nil {
		return false, "", err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, "", err
	}
	if affected == 0 {
		return false, "", nil
	}
	var projectID string
	if err := executor.QueryRow(`SELECT COALESCE(project_id, '') FROM tests WHERE id = ?`, testID).Scan(&projectID); err != nil {
		return false, "", err
	}
	return true, projectID, nil
}

func ensureLegacyTestHierarchy(executor testSaveExecutor, projectID string) error {
	applicationID := legacyApplicationID(projectID)
	releaseID := legacyReleaseID(projectID)
	now := time.Now().UTC()
	if _, err := executor.Exec(`INSERT OR IGNORE INTO applications (id, project_id, name, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		applicationID, projectID, "Legacy", now, now); err != nil {
		return fmt.Errorf("ensure legacy application for project %q: %w", projectID, err)
	}
	if _, err := executor.Exec(`INSERT OR IGNORE INTO releases (id, application_id, project_id, name, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		releaseID, applicationID, projectID, "Imported Legacy", now, now); err != nil {
		return fmt.Errorf("ensure legacy release for project %q: %w", projectID, err)
	}
	return nil
}

func validateScenarioProjectPlacement(executor testSaveExecutor, scenarioID, projectID, owner string) error {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		owner = "resource"
	}
	if err := lockRequiredPlacementParent(executor,
		`UPDATE scenarios SET updated_at = updated_at WHERE id = ?`, scenarioID, owner, "scenario"); err != nil {
		return err
	}
	var scriptCount, projectCount int
	var scenarioProjectID string
	if err := executor.QueryRow(`SELECT COUNT(*), COUNT(DISTINCT COALESCE(NULLIF(s.project_id, ''), ?)), COALESCE(MIN(COALESCE(NULLIF(s.project_id, ''), ?)), ?)
		FROM scenario_scripts ss
		JOIN scripts s ON s.id = ss.script_id
		WHERE ss.scenario_id = ?`, DefaultProjectID, DefaultProjectID, DefaultProjectID, scenarioID).
		Scan(&scriptCount, &projectCount, &scenarioProjectID); err != nil {
		return err
	}
	if projectCount > 1 {
		return fmt.Errorf("%s scenario %q spans multiple projects", owner, scenarioID)
	}
	if scriptCount == 0 {
		scenarioProjectID = DefaultProjectID
	}
	if normalizeProjectID(scenarioProjectID) != projectID {
		return fmt.Errorf("%s scenario %q belongs to project %q, not %q", owner, scenarioID, normalizeProjectID(scenarioProjectID), projectID)
	}
	return nil
}

// SaveTestIfLeaseHeld performs the lease check and the test upsert in one
// database transaction. leaseOwnerID is an acquisition-epoch token (not merely
// a process name), so a late callback cannot write after another epoch takes
// over or after the same process reacquires leadership.
func (s *SQLiteStore) SaveTestIfLeaseHeld(test *TestRecord, leaseName, leaseOwnerID string) (bool, error) {
	leaseName = strings.TrimSpace(leaseName)
	leaseOwnerID = strings.TrimSpace(leaseOwnerID)
	if test == nil {
		return false, fmt.Errorf("test is required")
	}
	if leaseName == "" || leaseOwnerID == "" {
		return false, errors.New("lease name and owner token are required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	held, err := schedulerLeaseHeldInTransaction(tx, s.db.dialect, leaseName, leaseOwnerID)
	if err != nil {
		return false, err
	}
	if !held {
		return false, nil
	}
	if err := s.saveTestWithExecutor(tx, test); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// EnsureTestRefKey fills only an empty ref_key column and returns the durable
// winner. It deliberately avoids a full-row upsert: reference-key backfill runs
// during controller boot, before an HA replica has acquired an execution epoch,
// and must not overwrite lifecycle fields owned by the active replica.
func (s *SQLiteStore) EnsureTestRefKey(id, proposedRefKey string) (string, error) {
	id = strings.TrimSpace(id)
	proposedRefKey = strings.TrimSpace(proposedRefKey)
	if id == "" || proposedRefKey == "" {
		return "", errors.New("test id and proposed reference key are required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE tests SET ref_key = ? WHERE id = ? AND (ref_key IS NULL OR TRIM(ref_key) = '')`, proposedRefKey, id); err != nil {
		return "", err
	}
	var durableRefKey string
	if err := tx.QueryRow(`SELECT COALESCE(ref_key, '') FROM tests WHERE id = ?`, id).Scan(&durableRefKey); err != nil {
		return "", err
	}
	durableRefKey = strings.TrimSpace(durableRefKey)
	if durableRefKey == "" {
		return "", fmt.Errorf("test %q still has an empty reference key", id)
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return durableRefKey, nil
}

func marshalTestField(name string, value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("marshal test %s: %w", name, err)
	}
	return string(payload), nil
}

func (s *SQLiteStore) GetTest(id string) (*TestRecord, error) {
	query := testSelectColumns() + ` FROM tests WHERE id = ? OR ref_key = ?`
	test, err := scanTest(s.db.QueryRow(query, id, id))
	if err != nil {
		return nil, err
	}
	return &test, nil
}

func testSelectColumns() string {
	return `SELECT id, COALESCE(ref_key, ''), COALESCE(materialization_key, ''), name, status, COALESCE(application_id, ''), COALESCE(release_id, ''), COALESCE(scenario_id, ''), COALESCE(project_id, ''), folder_id, COALESCE(scenario_mode, ''), COALESCE(shakeout_enabled, 0), COALESCE(resource_baseline_enabled, 0), COALESCE(start_delay, ''), COALESCE(assignment_json, ''), total_vusers, duration_ns, start_time, scheduled_start_at, pending_since, paused_at, COALESCE(cumulative_pause_ns, 0), capacity_window_start_at, capacity_window_end_at, COALESCE(baseline_pinned, 0), baseline_pinned_at, end_time, error, COALESCE(created_by_subject, ''), COALESCE(created_by_display_name, ''), COALESCE(started_by_subject, ''), COALESCE(started_by_display_name, ''), COALESCE(stopped_by_subject, ''), COALESCE(stopped_by_display_name, ''), groups_json, COALESCE(runtime_settings_json, ''), COALESCE(goal_config_json, ''), COALESCE(execution_plan_approval_json, ''), timeslot_id`
}

// rowScanner abstracts *sql.Row and *sql.Rows so scan helpers work with both
// QueryRow and Query results.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanTest scans a full test row (matching testSelectColumns) and decodes its
// JSON-encoded fields. Shared by GetTest, ListTests, and ListTestsByContext.
func scanTest(scanner rowScanner) (TestRecord, error) {
	var test TestRecord
	var groupsJSON, assignmentJSON, runtimeSettingsJSON, goalConfigJSON, executionPlanApprovalJSON string
	if err := scanner.Scan(&test.ID, &test.RefKey, &test.MaterializationKey, &test.Name, &test.Status, &test.ApplicationID, &test.ReleaseID, &test.ScenarioID, &test.ProjectID, &test.FolderID, &test.ScenarioMode, &test.ScriptShakeout, &test.ResourceBaseline, &test.StartDelay, &assignmentJSON, &test.TotalVUsers, &test.Duration, &test.StartTime, &test.ScheduledStartAt, &test.PendingSince, &test.PausedAt, &test.CumulativePause, &test.CapacityWindowStartAt, &test.CapacityWindowEndAt, &test.BaselinePinned, &test.BaselinePinnedAt, &test.EndTime, &test.Error, &test.CreatedBySubject, &test.CreatedByDisplayName, &test.StartedBySubject, &test.StartedByDisplayName, &test.StoppedBySubject, &test.StoppedByDisplayName, &groupsJSON, &runtimeSettingsJSON, &goalConfigJSON, &executionPlanApprovalJSON, &test.TimeslotID); err != nil {
		return test, err
	}
	if err := json.Unmarshal([]byte(groupsJSON), &test.Groups); err != nil {
		return test, fmt.Errorf("decode test %s groups_json: %w", test.ID, err)
	}
	if assignmentJSON != "" && assignmentJSON != "null" {
		if err := json.Unmarshal([]byte(assignmentJSON), &test.Assignment); err != nil {
			return test, fmt.Errorf("decode test %s assignment_json: %w", test.ID, err)
		}
	}
	if runtimeSettingsJSON != "" && runtimeSettingsJSON != "null" {
		if err := json.Unmarshal([]byte(runtimeSettingsJSON), &test.RuntimeSettings); err != nil {
			return test, fmt.Errorf("decode test %s runtime_settings_json: %w", test.ID, err)
		}
	}
	if goalConfigJSON != "" && goalConfigJSON != "null" {
		if err := json.Unmarshal([]byte(goalConfigJSON), &test.GoalConfig); err != nil {
			return test, fmt.Errorf("decode test %s goal_config_json: %w", test.ID, err)
		}
	}
	if executionPlanApprovalJSON != "" && executionPlanApprovalJSON != "null" {
		if err := json.Unmarshal([]byte(executionPlanApprovalJSON), &test.ExecutionPlanApproval); err != nil {
			return test, fmt.Errorf("decode test %s execution_plan_approval_json: %w", test.ID, err)
		}
	}
	return test, nil
}

func (s *SQLiteStore) ListTests(projectID string, folderID *string) ([]*TestRecord, error) {
	args := []any{}
	query := testSelectColumns() + ` FROM tests`
	var filters []string
	if projectID != "" {
		filters = append(filters, `project_id = ?`)
		args = append(args, projectID)
	}
	if folderID != nil {
		if *folderID == "" {
			filters = append(filters, `folder_id IS NULL`)
		} else {
			filters = append(filters, `folder_id = ?`)
			args = append(args, *folderID)
		}
	}
	if len(filters) > 0 {
		query += ` WHERE ` + strings.Join(filters, ` AND `)
	}
	query += ` ORDER BY start_time DESC`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tests []*TestRecord
	for rows.Next() {
		test, err := scanTest(rows)
		if err != nil {
			return nil, err
		}
		tests = append(tests, &test)
	}
	return tests, rows.Err()
}

func (s *SQLiteStore) ListTestsByContext(applicationID string, releaseID string, scenarioID string) ([]*TestRecord, error) {
	args := []any{}
	query := testSelectColumns() + ` FROM tests`
	var filters []string
	if applicationID != "" {
		filters = append(filters, `application_id = ?`)
		args = append(args, applicationID)
	}
	if releaseID != "" {
		filters = append(filters, `release_id = ?`)
		args = append(args, releaseID)
	}
	if scenarioID != "" {
		filters = append(filters, `scenario_id = ?`)
		args = append(args, scenarioID)
	}
	if len(filters) > 0 {
		query += ` WHERE ` + strings.Join(filters, ` AND `)
	}
	query += ` ORDER BY start_time DESC`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tests []*TestRecord
	for rows.Next() {
		test, err := scanTest(rows)
		if err != nil {
			return nil, err
		}
		tests = append(tests, &test)
	}
	return tests, rows.Err()
}

func (s *SQLiteStore) UpdateTestStatus(id string, status string, endTime *time.Time, errStr string) error {
	_, err := s.db.Exec(`UPDATE tests SET status = ?, end_time = ?, error = ? WHERE id = ?`, status, endTime, errStr, id)
	return err
}

func (s *SQLiteStore) UpdateTestStatusIfActive(id string, status string, endTime *time.Time, errStr string) (TestStatusTransitionResult, error) {
	result, err := s.db.Exec(`UPDATE tests SET status = ?, end_time = ?, error = ?
		WHERE id = ? AND (status = ? OR status IN ('INITIALIZING', 'STARTING', 'RUNNING', 'PAUSED', 'STOPPING', 'RESERVED', 'PENDING', 'SCHEDULED', 'SHAKEOUT', 'SHAKEOUT_REVIEW', 'QUEUED'))`, status, endTime, errStr, id, status)
	if err != nil {
		return "", err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return "", err
	}
	if affected == 1 {
		return TestStatusTransitionUpdated, nil
	}
	var current string
	if err := s.db.QueryRow(`SELECT status FROM tests WHERE id = ?`, id).Scan(&current); err != nil {
		if err == sql.ErrNoRows {
			return TestStatusTransitionMissing, nil
		}
		return "", err
	}
	if current == status {
		return TestStatusTransitionUnchanged, nil
	}
	return TestStatusTransitionFenced, nil
}

// UpdateTestStatusIfActiveAndMaterializationKey terminalizes only the
// deterministic execution-plan materialization that the caller owns. The key
// predicate is part of the UPDATE, so an ID deleted/recreated between a prior
// read and this write cannot be changed by stale recovery work.
func (s *SQLiteStore) UpdateTestStatusIfActiveAndMaterializationKey(id, materializationKey, status string, endTime *time.Time, errStr string) (TestStatusTransitionResult, error) {
	materializationKey = strings.TrimSpace(materializationKey)
	if materializationKey == "" {
		return TestStatusTransitionFenced, nil
	}
	result, err := s.db.Exec(`UPDATE tests SET status = ?, end_time = ?, error = ?
		WHERE id = ? AND TRIM(COALESCE(materialization_key, '')) = ?
		  AND (status = ? OR status IN ('INITIALIZING', 'STARTING', 'RUNNING', 'PAUSED', 'STOPPING', 'RESERVED', 'PENDING', 'SCHEDULED', 'SHAKEOUT', 'SHAKEOUT_REVIEW', 'QUEUED'))`,
		status, endTime, errStr, id, materializationKey, status)
	if err != nil {
		return "", err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return "", err
	}
	if affected == 1 {
		return TestStatusTransitionUpdated, nil
	}
	return s.classifyMaterializationStatusTransition(id, materializationKey, status)
}

// UpdateTestStatusIfActiveAndMaterializationKeyAndLeaseTermHeld combines the
// materialization ownership predicate with the complete HA lease term in one
// transaction. Neither an ID reuse nor a superseded controller epoch can land
// a stale execution-plan cleanup on a successor's test row.
func (s *SQLiteStore) UpdateTestStatusIfActiveAndMaterializationKeyAndLeaseTermHeld(id, materializationKey, status string, endTime *time.Time, errStr, leaseName, leaseOwnerID string, leaseGeneration uint64) (TestStatusTransitionResult, error) {
	materializationKey = strings.TrimSpace(materializationKey)
	leaseName = strings.TrimSpace(leaseName)
	leaseOwnerID = strings.TrimSpace(leaseOwnerID)
	if materializationKey == "" || leaseName == "" || leaseOwnerID == "" || leaseGeneration == 0 {
		return TestStatusTransitionFenced, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	held, err := schedulerLeaseTermHeldInTransaction(tx, s.db.dialect, leaseName, leaseOwnerID, leaseGeneration)
	if err != nil {
		return "", err
	}
	if !held {
		return TestStatusTransitionFenced, nil
	}
	result, err := tx.Exec(`UPDATE tests SET status = ?, end_time = ?, error = ?
		WHERE id = ? AND TRIM(COALESCE(materialization_key, '')) = ?
		  AND (status = ? OR status IN ('INITIALIZING', 'STARTING', 'RUNNING', 'PAUSED', 'STOPPING', 'RESERVED', 'PENDING', 'SCHEDULED', 'SHAKEOUT', 'SHAKEOUT_REVIEW', 'QUEUED'))`,
		status, endTime, errStr, id, materializationKey, status)
	if err != nil {
		return "", err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return "", err
	}
	if affected == 1 {
		if err := tx.Commit(); err != nil {
			return "", err
		}
		return TestStatusTransitionUpdated, nil
	}
	var currentStatus, currentKey string
	if err := tx.QueryRow(`SELECT status, COALESCE(materialization_key, '') FROM tests WHERE id = ?`, id).Scan(&currentStatus, &currentKey); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TestStatusTransitionMissing, nil
		}
		return "", err
	}
	if strings.TrimSpace(currentKey) != materializationKey {
		return TestStatusTransitionFenced, nil
	}
	if currentStatus == status {
		return TestStatusTransitionUnchanged, nil
	}
	return TestStatusTransitionFenced, nil
}

func (s *SQLiteStore) classifyMaterializationStatusTransition(id, materializationKey, status string) (TestStatusTransitionResult, error) {
	var currentStatus, currentKey string
	if err := s.db.QueryRow(`SELECT status, COALESCE(materialization_key, '') FROM tests WHERE id = ?`, id).Scan(&currentStatus, &currentKey); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TestStatusTransitionMissing, nil
		}
		return "", err
	}
	if strings.TrimSpace(currentKey) != strings.TrimSpace(materializationKey) {
		return TestStatusTransitionFenced, nil
	}
	if currentStatus == status {
		return TestStatusTransitionUnchanged, nil
	}
	return TestStatusTransitionFenced, nil
}

// UpdateTestStatusIfActiveAndLeaseHeld changes a lifecycle row only while the
// supplied acquisition-epoch token still owns a live scheduler lease. The
// lease row and test row are checked and changed in one transaction so a
// callback from an older controller epoch cannot terminalize a successor's
// active run.
func (s *SQLiteStore) UpdateTestStatusIfActiveAndLeaseHeld(id string, status string, endTime *time.Time, errStr, leaseName, leaseOwnerID string) (TestStatusTransitionResult, error) {
	leaseName = strings.TrimSpace(leaseName)
	leaseOwnerID = strings.TrimSpace(leaseOwnerID)
	if leaseName == "" || leaseOwnerID == "" {
		return TestStatusTransitionFenced, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	held, err := schedulerLeaseHeldInTransaction(tx, s.db.dialect, leaseName, leaseOwnerID)
	if err != nil {
		return "", err
	}
	if !held {
		return TestStatusTransitionFenced, nil
	}

	result, err := tx.Exec(`UPDATE tests SET status = ?, end_time = ?, error = ?
		WHERE id = ? AND (status = ? OR status IN ('INITIALIZING', 'STARTING', 'RUNNING', 'PAUSED', 'STOPPING', 'RESERVED', 'PENDING', 'SCHEDULED', 'SHAKEOUT', 'SHAKEOUT_REVIEW', 'QUEUED'))`, status, endTime, errStr, id, status)
	if err != nil {
		return "", err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return "", err
	}
	if affected == 1 {
		if err := tx.Commit(); err != nil {
			return "", err
		}
		return TestStatusTransitionUpdated, nil
	}
	var current string
	if err := tx.QueryRow(`SELECT status FROM tests WHERE id = ?`, id).Scan(&current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TestStatusTransitionMissing, nil
		}
		return "", err
	}
	if current == status {
		return TestStatusTransitionUnchanged, nil
	}
	return TestStatusTransitionFenced, nil
}

// preciseDatabaseTime keeps lease expiry checks on the database clock without
// truncating to whole seconds. PostgreSQL returns its native timestamp; SQLite
// strftime provides millisecond precision, which matches the timestamp
// precision used by its persisted lease rows.
func preciseDatabaseTime(tx *storeTx, dialect storeDialect) (time.Time, error) {
	if dialect == storeDialectPostgres {
		var now time.Time
		if err := tx.QueryRow(`SELECT clock_timestamp()`).Scan(&now); err != nil {
			return time.Time{}, err
		}
		return now.UTC(), nil
	}
	var raw string
	if err := tx.QueryRow(`SELECT strftime('%Y-%m-%dT%H:%M:%fZ', 'now')`).Scan(&raw); err != nil {
		return time.Time{}, err
	}
	now, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse SQLite clock %q: %w", raw, err)
	}
	return now.UTC(), nil
}

func schedulerLeaseHeldInTransaction(tx *storeTx, dialect storeDialect, leaseName, leaseOwnerID string) (bool, error) {
	leaseQuery := `SELECT owner_id, expires_at FROM scheduler_leases WHERE name = ?`
	if dialect == storeDialectPostgres {
		leaseQuery += ` FOR UPDATE`
	}
	var owner string
	var expiresAt time.Time
	if err := tx.QueryRow(leaseQuery, leaseName).Scan(&owner, &expiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	databaseNow, err := preciseDatabaseTime(tx, dialect)
	if err != nil {
		return false, fmt.Errorf("read scheduler-lease database clock: %w", err)
	}
	return owner == leaseOwnerID && expiresAt.After(databaseNow), nil
}

// schedulerLeaseTermHeldInTransaction validates the complete durable fencing
// term while holding the lease row in the same transaction as the protected
// mutation. Owner tokens prevent callbacks from an older acquisition attempt
// from committing; the monotonic generation also fences a controller identity
// that is accidentally reused after a later owner has taken over.
func schedulerLeaseTermHeldInTransaction(tx *storeTx, dialect storeDialect, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error) {
	if leaseGeneration == 0 {
		return false, fmt.Errorf("scheduler lease generation must be positive")
	}
	leaseQuery := `SELECT owner_id, generation, expires_at FROM scheduler_leases WHERE name = ?`
	if dialect == storeDialectPostgres {
		leaseQuery += ` FOR UPDATE`
	}
	var (
		owner      string
		generation int64
		expiresAt  time.Time
	)
	if err := tx.QueryRow(leaseQuery, leaseName).Scan(&owner, &generation, &expiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if generation <= 0 {
		return false, fmt.Errorf("scheduler lease %q has invalid generation %d", leaseName, generation)
	}
	databaseNow, err := preciseDatabaseTime(tx, dialect)
	if err != nil {
		return false, fmt.Errorf("read scheduler-lease database clock: %w", err)
	}
	return owner == leaseOwnerID && uint64(generation) == leaseGeneration && expiresAt.After(databaseNow), nil
}

// UpdateTestBaselines atomically updates only baseline-owned columns. A nil
// timestamp unpins the row; lifecycle/status fields are never rewritten from a
// potentially stale in-memory test snapshot.
func (s *SQLiteStore) UpdateTestBaselines(changes map[string]*time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for id, pinnedAt := range changes {
		pinned := pinnedAt != nil
		if _, err := tx.Exec(`UPDATE tests SET baseline_pinned = ?, baseline_pinned_at = ? WHERE id = ?`, pinned, pinnedAt, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetTestComparisonBaseline applies baseline selection from durable scope
// fields, rather than from a replica-local map. Pinning unpins every other row
// in the same comparison scope and pins the target in one transaction. A nil
// pinnedAt unpins only the target.
func (s *SQLiteStore) SetTestComparisonBaseline(id string, pinnedAt *time.Time) ([]TestBaselineState, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("test id is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	targetQuery := `SELECT COALESCE(project_id, ''), COALESCE(scenario_id, ''), COALESCE(release_id, ''), COALESCE(application_id, ''), name, status FROM tests WHERE id = ?`
	if s.db.dialect == storeDialectPostgres {
		targetQuery += ` FOR UPDATE`
	}
	var projectID, scenarioID, releaseID, applicationID, name, status string
	if err := tx.QueryRow(targetQuery, id).Scan(&projectID, &scenarioID, &releaseID, &applicationID, &name, &status); err != nil {
		return nil, err
	}
	if pinnedAt != nil {
		switch strings.ToUpper(strings.TrimSpace(status)) {
		case "COMPLETE", "FAILED", "ABORTED":
		default:
			return nil, fmt.Errorf("comparison baseline pinning requires a completed, failed, or aborted test")
		}
	}

	predicate, predicateArgs := comparisonBaselineScopePredicate(normalizeProjectID(projectID), scenarioID, releaseID, applicationID, name)
	if pinnedAt == nil {
		if _, err := tx.Exec(`UPDATE tests SET baseline_pinned = ?, baseline_pinned_at = NULL WHERE id = ?`, false, id); err != nil {
			return nil, err
		}
		predicate = `id = ?`
		predicateArgs = []any{id}
	} else {
		pinTime := pinnedAt.UTC()
		args := []any{id, true, false, id, pinTime}
		args = append(args, predicateArgs...)
		if _, err := tx.Exec(`UPDATE tests
			SET baseline_pinned = CASE WHEN id = ? THEN ? ELSE ? END,
				baseline_pinned_at = CASE WHEN id = ? THEN ? ELSE NULL END
			WHERE `+predicate, args...); err != nil {
			return nil, err
		}
	}

	rows, err := tx.Query(`SELECT id, COALESCE(baseline_pinned, 0), baseline_pinned_at FROM tests WHERE `+predicate+` ORDER BY id`, predicateArgs...)
	if err != nil {
		return nil, err
	}
	states := make([]TestBaselineState, 0)
	for rows.Next() {
		var state TestBaselineState
		if err := rows.Scan(&state.ID, &state.Pinned, &state.PinnedAt); err != nil {
			rows.Close()
			return nil, err
		}
		states = append(states, state)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return states, nil
}

func comparisonBaselineScopePredicate(projectID, scenarioID, releaseID, applicationID, name string) (string, []any) {
	projectPredicate := `COALESCE(NULLIF(project_id, ''), ?) = ? AND `
	projectArgs := []any{DefaultProjectID, normalizeProjectID(projectID)}
	if scenarioID != "" {
		return projectPredicate + `COALESCE(scenario_id, '') = ?`, append(projectArgs, scenarioID)
	}
	if releaseID != "" {
		return projectPredicate + `COALESCE(scenario_id, '') = '' AND COALESCE(release_id, '') = ? AND name = ?`, append(projectArgs, releaseID, name)
	}
	if applicationID != "" {
		return projectPredicate + `COALESCE(scenario_id, '') = '' AND COALESCE(release_id, '') = '' AND COALESCE(application_id, '') = ? AND name = ?`, append(projectArgs, applicationID, name)
	}
	return projectPredicate + `COALESCE(scenario_id, '') = '' AND COALESCE(release_id, '') = '' AND COALESCE(application_id, '') = '' AND name = ?`, append(projectArgs, name)
}

func (s *SQLiteStore) DeleteTest(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if s.db.dialect == storeDialectPostgres {
		var timeslotID *string
		if err := tx.QueryRow(`SELECT timeslot_id FROM tests WHERE id = ?`, id).Scan(&timeslotID); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err := lockCapacityReservationsForTestMutation(tx, id, timeslotID); err != nil {
			return err
		}
	}

	var projectID string
	testQuery := `SELECT COALESCE(project_id, '') FROM tests WHERE id = ?`
	if s.db.dialect == storeDialectPostgres {
		testQuery += ` FOR UPDATE`
	}
	if err := tx.QueryRow(testQuery, id).Scan(&projectID); err != nil && err != sql.ErrNoRows {
		return err
	}
	if err := deleteTestGraphTx(tx, id, projectID); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteTestIfLeaseHeld deletes a non-active test and all owned metadata only
// while leaseOwnerID still owns an unexpired execution lease. The lease row and
// test row are checked under the same transaction as the delete, closing the
// gap between a replica-local leader/status check and destructive persistence.
func (s *SQLiteStore) DeleteTestIfLeaseHeld(id, leaseName, leaseOwnerID string) (LeaseFencedDeleteResult, error) {
	leaseName, leaseOwnerID, err := validateLeaseFencedDelete(leaseName, leaseOwnerID)
	if err != nil {
		return LeaseFencedDeleteFenced, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return LeaseFencedDeleteFenced, err
	}
	defer tx.Rollback()

	held, err := schedulerLeaseHeldInTransaction(tx, s.db.dialect, leaseName, leaseOwnerID)
	if err != nil {
		return LeaseFencedDeleteFenced, err
	}
	if !held {
		return LeaseFencedDeleteFenced, nil
	}
	if s.db.dialect == storeDialectPostgres {
		var timeslotID *string
		if err := tx.QueryRow(`SELECT timeslot_id FROM tests WHERE id = ?`, id).Scan(&timeslotID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return LeaseFencedDeleteMissing, nil
			}
			return LeaseFencedDeleteFenced, err
		}
		if err := lockCapacityReservationsForTestMutation(tx, id, timeslotID); err != nil {
			return LeaseFencedDeleteFenced, err
		}
	}

	query := `SELECT COALESCE(project_id, ''), COALESCE(status, '') FROM tests WHERE id = ?`
	if s.db.dialect == storeDialectPostgres {
		query += ` FOR UPDATE`
	}
	var projectID, testStatus string
	if err := tx.QueryRow(query, id).Scan(&projectID, &testStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return LeaseFencedDeleteMissing, nil
		}
		return LeaseFencedDeleteFenced, err
	}
	if activeTestStatusForDeletion(testStatus) {
		return LeaseFencedDeleteActive, nil
	}
	if err := deleteTestGraphTx(tx, id, projectID); err != nil {
		return LeaseFencedDeleteFenced, err
	}
	if err := tx.Commit(); err != nil {
		return LeaseFencedDeleteFenced, err
	}
	return LeaseFencedDeleteDeleted, nil
}

// lockCapacityReservationsForTestMutation preserves the global PostgreSQL row
// lock order (lease -> reservation -> test) shared with bind/release. SQLite
// serializes writers at the database level and does not support FOR UPDATE, so
// callers invoke this helper only for PostgreSQL transactions.
func lockCapacityReservationsForTestMutation(tx *storeTx, testID string, timeslotID *string) error {
	query := `SELECT id FROM capacity_reservations WHERE bound_test_id = ?`
	args := []any{testID}
	if timeslotID != nil && strings.TrimSpace(*timeslotID) != "" {
		query += ` OR id = ?`
		args = append(args, strings.TrimSpace(*timeslotID))
	}
	query += ` ORDER BY id FOR UPDATE`
	rows, err := tx.Query(query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var ignored string
		if err := rows.Scan(&ignored); err != nil {
			return err
		}
	}
	return rows.Err()
}

func deleteTestGraphTx(tx *storeTx, id, projectID string) error {
	archive, err := getRunArchiveTx(tx, id)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if archive != nil {
		if err := enqueueArchiveCleanupTx(tx, projectID, archive); err != nil {
			return err
		}
	} else {
		job, jobErr := getArchiveFinalizationJobTx(tx, id)
		if jobErr != nil && jobErr != sql.ErrNoRows {
			return jobErr
		}
		if job != nil {
			if err := enqueueArchiveCleanupTx(tx, projectID, &job.ArchiveRecord); err != nil {
				return err
			}
		}
	}

	// A reservation is an independently durable capacity object. Clear both
	// binding columns in this transaction so deleting a test cannot strand a
	// timeslot (and a later failure rolls the release back with the test delete).
	if _, err := tx.Exec(`UPDATE capacity_reservations
		SET bound_test_id = NULL, bound_test_status = NULL, updated_at = ?
		WHERE bound_test_id = ?`, time.Now().UTC(), id); err != nil {
		return err
	}

	if _, err := tx.Exec(`DELETE FROM test_history WHERE test_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM test_notes WHERE test_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM test_interval_summaries WHERE test_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM github_check_links WHERE test_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM run_archives WHERE test_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM archive_finalization_jobs WHERE test_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM archive_refresh_jobs WHERE test_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM tests WHERE id = ?`, id); err != nil {
		return err
	}
	return nil
}

func (s *SQLiteStore) MoveTest(id string, folderID *string) error {
	_, err := s.db.Exec(`UPDATE tests SET folder_id = ? WHERE id = ?`, folderID, id)
	return err
}

func (s *SQLiteStore) SaveHistory(testID string, timestamp time.Time, metrics any) error {
	record, err := intervalSummaryRecordFromMetrics(timestamp, metrics)
	if err == nil {
		browserJSON, err := marshalNullableJSON(record.Browser)
		if err != nil {
			return err
		}
		browserByDeviceJSON, err := marshalNullableJSON(record.BrowserByDevice)
		if err != nil {
			return err
		}
		latencyJSON, err := marshalNullableJSON(record.Latency)
		if err != nil {
			return err
		}
		var collectedAt any
		if !record.CollectedAt.IsZero() {
			collectedAt = record.CollectedAt
		}
		var publishedAt any
		if !record.PublishedAt.IsZero() {
			publishedAt = record.PublishedAt
		}
		var generatorPublishedAt any
		if !record.GeneratorPublishedAt.IsZero() {
			generatorPublishedAt = record.GeneratorPublishedAt
		}
		var controllerReceivedAt any
		if !record.ControllerReceivedAt.IsZero() {
			controllerReceivedAt = record.ControllerReceivedAt
		}
		_, err = s.db.Exec(`INSERT INTO test_interval_summaries (
			test_id, schema_version, timestamp, active_vusers, tps, avg_response_ms, error_pct,
			total_transactions, total_errors, p50_ms, p90_ms, p95_ms, p99_ms,
			browser_json, browser_by_device_json, latency_json,
			dropped_results, websocket_clients, websocket_dropped_messages, websocket_dropped_events,
			generator_published_at, controller_received_at, collected_at, published_at,
			generator_processing_lag_ms, transport_lag_ms, controller_fanout_lag_ms, sample_age_ms
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			testID,
			record.SchemaVersion,
			record.Timestamp,
			record.ActiveVUsers,
			record.TPS,
			record.AvgResponseMs,
			record.ErrorPct,
			record.TotalTx,
			record.TotalErrors,
			record.P50Ms,
			record.P90Ms,
			record.P95Ms,
			record.P99Ms,
			browserJSON,
			browserByDeviceJSON,
			latencyJSON,
			record.DroppedResults,
			record.WebsocketClients,
			record.WebsocketDroppedMessages,
			record.WebsocketDroppedEvents,
			generatorPublishedAt,
			controllerReceivedAt,
			collectedAt,
			publishedAt,
			record.GeneratorProcessingLagMs,
			record.TransportLagMs,
			record.ControllerFanoutLagMs,
			record.SampleAgeMs,
		)
		if err != nil {
			return err
		}
		return s.trimPersistedTestHistory("test_interval_summaries", testID, maxPersistedTestHistoryPoints)
	}

	metricsJSON, err := json.Marshal(metrics)
	if err != nil {
		return fmt.Errorf("marshal test history metrics for %s: %w", testID, err)
	}
	_, err = s.db.Exec(`INSERT INTO test_history (test_id, timestamp, metrics_json) VALUES (?, ?, ?)`, testID, timestamp, string(metricsJSON))
	if err != nil {
		return err
	}
	return s.trimPersistedTestHistory("test_history", testID, maxPersistedTestHistoryPoints)
}

const maxPersistedTestHistoryPoints = 3600

// trimPersistedTestHistory keeps the controller's restart/live-history window
// bounded. Complete interval history is already streamed into the run archive;
// retaining every one-second controller snapshot in the database as well grows
// without limit on long-running tests.
func (s *SQLiteStore) trimPersistedTestHistory(table string, testID string, limit int) error {
	if limit <= 0 {
		return nil
	}
	var sqliteQuery string
	var postgresQuery string
	switch table {
	case "test_interval_summaries":
		sqliteQuery = `DELETE FROM test_interval_summaries WHERE rowid IN (
			SELECT rowid FROM test_interval_summaries WHERE test_id = ?
			ORDER BY timestamp DESC, rowid DESC LIMIT -1 OFFSET ?
		)`
		postgresQuery = `DELETE FROM test_interval_summaries WHERE ctid IN (
			SELECT ctid FROM test_interval_summaries WHERE test_id = ?
			ORDER BY timestamp DESC, ctid DESC OFFSET ?
		)`
	case "test_history":
		sqliteQuery = `DELETE FROM test_history WHERE rowid IN (
			SELECT rowid FROM test_history WHERE test_id = ?
			ORDER BY timestamp DESC, rowid DESC LIMIT -1 OFFSET ?
		)`
		postgresQuery = `DELETE FROM test_history WHERE ctid IN (
			SELECT ctid FROM test_history WHERE test_id = ?
			ORDER BY timestamp DESC, ctid DESC OFFSET ?
		)`
	default:
		return fmt.Errorf("unsupported test history table %q", table)
	}
	query := sqliteQuery
	if s.db.dialect == storeDialectPostgres {
		query = postgresQuery
	}
	_, err := s.db.Exec(query, testID, limit)
	return err
}

func (s *SQLiteStore) GetHistory(testID string) ([]any, error) {
	return s.GetHistoryLimit(testID, 0)
}

func (s *SQLiteStore) GetHistoryLimit(testID string, limit int) ([]any, error) {
	typedHistory, err := s.getTypedHistoryLimit(testID, limit)
	if err != nil {
		return nil, err
	}
	if len(typedHistory) > 0 {
		return typedHistory, nil
	}

	query := `SELECT metrics_json FROM test_history WHERE test_id = ? ORDER BY timestamp ASC`
	args := []any{testID}
	if limit > 0 {
		// PostgreSQL requires an alias on a subquery in FROM; SQLite does not.
		// rewriteSQL() does not synthesize one, so it must be written here or the
		// Postgres dialect rejects the query.
		query = `SELECT metrics_json FROM (
			SELECT timestamp, metrics_json FROM test_history
			WHERE test_id = ?
			ORDER BY timestamp DESC
			LIMIT ?
		) AS recent ORDER BY timestamp ASC`
		args = append(args, limit)
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var history []any
	for rows.Next() {
		var metricsJSON string
		if err := rows.Scan(&metricsJSON); err != nil {
			return nil, err
		}
		var metrics any
		if err := json.Unmarshal([]byte(metricsJSON), &metrics); err != nil {
			return nil, fmt.Errorf("decode test %s history metrics: %w", testID, err)
		}
		history = append(history, metrics)
	}
	return history, rows.Err()
}

func (s *SQLiteStore) getTypedHistory(testID string) ([]any, error) {
	return s.getTypedHistoryLimit(testID, 0)
}

func (s *SQLiteStore) getTypedHistoryLimit(testID string, limit int) ([]any, error) {
	query := `SELECT
		schema_version,
		timestamp,
		active_vusers,
		tps,
		avg_response_ms,
		error_pct,
		total_transactions,
		total_errors,
		p50_ms,
		p90_ms,
		p95_ms,
		p99_ms,
		browser_json,
		browser_by_device_json,
		latency_json,
		dropped_results,
		websocket_clients,
		websocket_dropped_messages,
		websocket_dropped_events,
		generator_published_at,
		controller_received_at,
		collected_at,
		published_at,
		generator_processing_lag_ms,
		transport_lag_ms,
		controller_fanout_lag_ms,
		sample_age_ms
	FROM test_interval_summaries WHERE test_id = ?`
	args := []any{testID}
	if limit > 0 {
		// Alias the derived table so the Postgres dialect accepts it (SQLite is
		// lenient; rewriteSQL() does not add one). See GetHistoryLimit above.
		query = `SELECT * FROM (` + query + ` ORDER BY timestamp DESC LIMIT ?) AS recent ORDER BY timestamp ASC`
		args = append(args, limit)
	} else {
		query += ` ORDER BY timestamp ASC`
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	history := make([]any, 0)
	for rows.Next() {
		var record IntervalSummaryRecord
		var browserJSON sql.NullString
		var browserByDeviceJSON sql.NullString
		var latencyJSON sql.NullString
		var droppedResults sql.NullInt64
		var websocketClients sql.NullInt64
		var websocketDroppedMessages sql.NullInt64
		var websocketDroppedEvents sql.NullInt64
		var generatorPublishedAt sql.NullTime
		var controllerReceivedAt sql.NullTime
		var collectedAt sql.NullTime
		var publishedAt sql.NullTime
		var generatorProcessingLagMs sql.NullFloat64
		var transportLagMs sql.NullFloat64
		var controllerFanoutLagMs sql.NullFloat64
		var sampleAgeMs sql.NullFloat64
		var schemaVersion sql.NullString
		if err := rows.Scan(
			&schemaVersion,
			&record.Timestamp,
			&record.ActiveVUsers,
			&record.TPS,
			&record.AvgResponseMs,
			&record.ErrorPct,
			&record.TotalTx,
			&record.TotalErrors,
			&record.P50Ms,
			&record.P90Ms,
			&record.P95Ms,
			&record.P99Ms,
			&browserJSON,
			&browserByDeviceJSON,
			&latencyJSON,
			&droppedResults,
			&websocketClients,
			&websocketDroppedMessages,
			&websocketDroppedEvents,
			&generatorPublishedAt,
			&controllerReceivedAt,
			&collectedAt,
			&publishedAt,
			&generatorProcessingLagMs,
			&transportLagMs,
			&controllerFanoutLagMs,
			&sampleAgeMs,
		); err != nil {
			return nil, err
		}
		record.SchemaVersion = normalizeTelemetrySchemaVersion(schemaVersion.String, intervalSummarySchemaVersionV1)
		if browserJSON.Valid && strings.TrimSpace(browserJSON.String) != "" {
			if err := json.Unmarshal([]byte(browserJSON.String), &record.Browser); err != nil {
				return nil, err
			}
		}
		if browserByDeviceJSON.Valid && strings.TrimSpace(browserByDeviceJSON.String) != "" {
			if err := json.Unmarshal([]byte(browserByDeviceJSON.String), &record.BrowserByDevice); err != nil {
				return nil, err
			}
		}
		if latencyJSON.Valid && strings.TrimSpace(latencyJSON.String) != "" {
			if err := json.Unmarshal([]byte(latencyJSON.String), &record.Latency); err != nil {
				return nil, err
			}
		}
		if droppedResults.Valid {
			record.DroppedResults = droppedResults.Int64
		}
		if websocketClients.Valid {
			record.WebsocketClients = int(websocketClients.Int64)
		}
		if websocketDroppedMessages.Valid {
			record.WebsocketDroppedMessages = websocketDroppedMessages.Int64
		}
		if websocketDroppedEvents.Valid {
			record.WebsocketDroppedEvents = websocketDroppedEvents.Int64
		}
		if generatorPublishedAt.Valid {
			record.GeneratorPublishedAt = generatorPublishedAt.Time
		}
		if controllerReceivedAt.Valid {
			record.ControllerReceivedAt = controllerReceivedAt.Time
		}
		if collectedAt.Valid {
			record.CollectedAt = collectedAt.Time
		}
		if publishedAt.Valid {
			record.PublishedAt = publishedAt.Time
		}
		if generatorProcessingLagMs.Valid {
			record.GeneratorProcessingLagMs = generatorProcessingLagMs.Float64
		}
		if transportLagMs.Valid {
			record.TransportLagMs = transportLagMs.Float64
		}
		if controllerFanoutLagMs.Valid {
			record.ControllerFanoutLagMs = controllerFanoutLagMs.Float64
		}
		if sampleAgeMs.Valid {
			record.SampleAgeMs = sampleAgeMs.Float64
		}
		history = append(history, record)
	}
	return history, rows.Err()
}

func intervalSummaryRecordFromMetrics(timestamp time.Time, metrics any) (IntervalSummaryRecord, error) {
	type browserEnvelope struct {
		Samples              int64   `json:"samples"`
		AvgFCPMs             float64 `json:"avg_fcp_ms,omitempty"`
		AvgLCPMs             float64 `json:"avg_lcp_ms,omitempty"`
		AvgCLSScore          float64 `json:"avg_cls_score,omitempty"`
		AvgINPMs             float64 `json:"avg_inp_ms,omitempty"`
		AvgTTFBMs            float64 `json:"avg_ttfb_ms,omitempty"`
		AvgBlockingResources float64 `json:"avg_blocking_resources,omitempty"`
		AvgLongTaskCount     float64 `json:"avg_long_task_count,omitempty"`
		AvgJSHeapUsedMB      float64 `json:"avg_js_heap_used_mb,omitempty"`
		AvgBrowserProcessCPU float64 `json:"avg_browser_process_cpu_pct,omitempty"`
		AvgBrowserProcessMem float64 `json:"avg_browser_process_mem_mb,omitempty"`
	}
	type browserDeviceEnvelope struct {
		DeviceProfile        string  `json:"device_profile,omitempty"`
		DevicePlatform       string  `json:"device_platform,omitempty"`
		DeviceTier           string  `json:"device_tier,omitempty"`
		BrowserEngine        string  `json:"browser_engine,omitempty"`
		Samples              int64   `json:"samples"`
		AvgFCPMs             float64 `json:"avg_fcp_ms,omitempty"`
		AvgLCPMs             float64 `json:"avg_lcp_ms,omitempty"`
		AvgCLSScore          float64 `json:"avg_cls_score,omitempty"`
		AvgINPMs             float64 `json:"avg_inp_ms,omitempty"`
		AvgTTFBMs            float64 `json:"avg_ttfb_ms,omitempty"`
		AvgBlockingResources float64 `json:"avg_blocking_resources,omitempty"`
		AvgLongTaskCount     float64 `json:"avg_long_task_count,omitempty"`
		AvgJSHeapUsedMB      float64 `json:"avg_js_heap_used_mb,omitempty"`
		AvgBrowserProcessCPU float64 `json:"avg_browser_process_cpu_pct,omitempty"`
		AvgBrowserProcessMem float64 `json:"avg_browser_process_mem_mb,omitempty"`
	}
	type telemetryEnvelope struct {
		DroppedResults           int64     `json:"dropped_results,omitempty"`
		WebsocketClients         int       `json:"websocket_clients,omitempty"`
		WebsocketDroppedMessages int64     `json:"websocket_dropped_messages,omitempty"`
		WebsocketDroppedEvents   int64     `json:"websocket_dropped_events,omitempty"`
		GeneratorPublishedAt     time.Time `json:"generator_published_at,omitempty"`
		ControllerReceivedAt     time.Time `json:"controller_received_at,omitempty"`
		CollectedAt              time.Time `json:"collected_at,omitempty"`
		PublishedAt              time.Time `json:"published_at,omitempty"`
		GeneratorProcessingLagMs float64   `json:"generator_processing_lag_ms,omitempty"`
		TransportLagMs           float64   `json:"transport_lag_ms,omitempty"`
		ControllerFanoutLagMs    float64   `json:"controller_fanout_lag_ms,omitempty"`
		SampleAgeMs              float64   `json:"sample_age_ms,omitempty"`
	}
	type summaryEnvelope struct {
		Timestamp       time.Time                         `json:"timestamp"`
		ActiveVUsers    int64                             `json:"active_vusers"`
		TPS             float64                           `json:"tps"`
		AvgResponseMs   float64                           `json:"avg_response_ms"`
		ErrorPct        float64                           `json:"error_pct"`
		TotalTx         int64                             `json:"total_transactions"`
		TotalErrors     int64                             `json:"total_errors"`
		P50Ms           float64                           `json:"p50_ms"`
		P90Ms           float64                           `json:"p90_ms"`
		P95Ms           float64                           `json:"p95_ms"`
		P99Ms           float64                           `json:"p99_ms"`
		Browser         *browserEnvelope                  `json:"browser,omitempty"`
		BrowserByDevice map[string]*browserDeviceEnvelope `json:"browser_by_device,omitempty"`
		Latency         *NetworkLatencySummaryRecord      `json:"latency,omitempty"`
		Telemetry       *telemetryEnvelope                `json:"telemetry,omitempty"`
	}

	payload, err := json.Marshal(metrics)
	if err != nil {
		return IntervalSummaryRecord{}, err
	}
	var summary summaryEnvelope
	if err := json.Unmarshal(payload, &summary); err != nil {
		return IntervalSummaryRecord{}, err
	}
	record := IntervalSummaryRecord{
		SchemaVersion: intervalSummarySchemaVersion,
		Timestamp:     summary.Timestamp,
		ActiveVUsers:  summary.ActiveVUsers,
		TPS:           summary.TPS,
		AvgResponseMs: summary.AvgResponseMs,
		ErrorPct:      summary.ErrorPct,
		TotalTx:       summary.TotalTx,
		TotalErrors:   summary.TotalErrors,
		P50Ms:         summary.P50Ms,
		P90Ms:         summary.P90Ms,
		P95Ms:         summary.P95Ms,
		P99Ms:         summary.P99Ms,
	}
	if summary.Browser != nil {
		record.Browser = &BrowserMetricsSummaryRecord{
			Samples:              summary.Browser.Samples,
			AvgFCPMs:             summary.Browser.AvgFCPMs,
			AvgLCPMs:             summary.Browser.AvgLCPMs,
			AvgCLSScore:          summary.Browser.AvgCLSScore,
			AvgINPMs:             summary.Browser.AvgINPMs,
			AvgTTFBMs:            summary.Browser.AvgTTFBMs,
			AvgBlockingResources: summary.Browser.AvgBlockingResources,
			AvgLongTaskCount:     summary.Browser.AvgLongTaskCount,
			AvgJSHeapUsedMB:      summary.Browser.AvgJSHeapUsedMB,
			AvgBrowserProcessCPU: summary.Browser.AvgBrowserProcessCPU,
			AvgBrowserProcessMem: summary.Browser.AvgBrowserProcessMem,
		}
	}
	if len(summary.BrowserByDevice) > 0 {
		record.BrowserByDevice = make(map[string]*BrowserDeviceMetricsRecord, len(summary.BrowserByDevice))
		for name, metrics := range summary.BrowserByDevice {
			if metrics == nil {
				continue
			}
			record.BrowserByDevice[name] = &BrowserDeviceMetricsRecord{
				DeviceProfile:        metrics.DeviceProfile,
				DevicePlatform:       metrics.DevicePlatform,
				DeviceTier:           metrics.DeviceTier,
				BrowserEngine:        metrics.BrowserEngine,
				Samples:              metrics.Samples,
				AvgFCPMs:             metrics.AvgFCPMs,
				AvgLCPMs:             metrics.AvgLCPMs,
				AvgCLSScore:          metrics.AvgCLSScore,
				AvgINPMs:             metrics.AvgINPMs,
				AvgTTFBMs:            metrics.AvgTTFBMs,
				AvgBlockingResources: metrics.AvgBlockingResources,
				AvgLongTaskCount:     metrics.AvgLongTaskCount,
				AvgJSHeapUsedMB:      metrics.AvgJSHeapUsedMB,
				AvgBrowserProcessCPU: metrics.AvgBrowserProcessCPU,
				AvgBrowserProcessMem: metrics.AvgBrowserProcessMem,
			}
		}
	}
	if summary.Latency != nil {
		record.Latency = &NetworkLatencySummaryRecord{
			Targets: append([]NetworkLatencyTargetRecord(nil), summary.Latency.Targets...),
		}
	}
	if record.Timestamp.IsZero() {
		record.Timestamp = timestamp
	}
	if summary.Telemetry != nil {
		record.DroppedResults = summary.Telemetry.DroppedResults
		record.WebsocketClients = summary.Telemetry.WebsocketClients
		record.WebsocketDroppedMessages = summary.Telemetry.WebsocketDroppedMessages
		record.WebsocketDroppedEvents = summary.Telemetry.WebsocketDroppedEvents
		record.GeneratorPublishedAt = summary.Telemetry.GeneratorPublishedAt
		record.ControllerReceivedAt = summary.Telemetry.ControllerReceivedAt
		record.CollectedAt = summary.Telemetry.CollectedAt
		record.PublishedAt = summary.Telemetry.PublishedAt
		record.GeneratorProcessingLagMs = summary.Telemetry.GeneratorProcessingLagMs
		record.TransportLagMs = summary.Telemetry.TransportLagMs
		record.ControllerFanoutLagMs = summary.Telemetry.ControllerFanoutLagMs
		record.SampleAgeMs = summary.Telemetry.SampleAgeMs
	}
	return record, nil
}

func marshalNullableJSON(value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if string(encoded) == "null" || string(encoded) == "{}" {
		return nil, nil
	}
	return string(encoded), nil
}
