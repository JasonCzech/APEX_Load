package storage

// SQLite store — tests. Split from sqlite.go (same package, behavior-preserving).

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/scrub"
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

const (
	testProjectRevisionSelect     = `SELECT updated_at FROM projects WHERE id = ?`
	testApplicationRevisionSelect = `SELECT COALESCE(project_id, ''), updated_at FROM applications WHERE id = ?`
	testReleaseRevisionSelect     = `SELECT application_id, COALESCE(project_id, ''), updated_at, archived_at FROM releases WHERE id = ?`
)

func (s *SQLiteStore) saveTestWithExecutor(executor testSaveExecutor, test *TestRecord) error {
	args, err := prepareTestSaveArgs(test)
	if err != nil {
		return err
	}
	if err := s.validateTestSavePlacement(executor, test); err != nil {
		return err
	}
	return s.writePreparedTestWithExecutor(executor, test, args)
}

// validateTestSavePlacement acquires every tenant/content row needed by the
// full-row test upsert. Keeping this phase separate lets the start transaction
// acquire these rows before PostgreSQL's deferred-trigger singleton revision
// rows, matching the content-row -> revision-row order used by concurrent
// admission writers.
func (s *SQLiteStore) validateTestSavePlacement(executor testSaveExecutor, test *TestRecord) error {
	exists, err := validateTestPlacement(executor, test, true, false)
	if err != nil {
		return err
	}
	if !exists {
		if err := lockActiveReleaseForNewTest(executor, test); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) validateExistingTestStartPlacement(executor testSaveExecutor, test *TestRecord) error {
	if _, err := validateTestPlacement(executor, test, true, true); err != nil {
		return err
	}
	tx, ok := executor.(*storeTx)
	if !ok {
		return errors.New("test start identity validation requires a transaction")
	}
	return validateExistingTestStartIdentity(tx, s.db.dialect, test)
}

// writePreparedTestWithExecutor writes an already-normalized test after the
// caller has acquired and validated its placement rows. Callers must not use it
// without the matching create/update or existing-start placement validator in
// the same transaction.
func (s *SQLiteStore) writePreparedTestWithExecutor(executor testSaveExecutor, test *TestRecord, args []any) error {
	allowMaterializedPrelaunchAbort := false
	if strings.EqualFold(strings.TrimSpace(test.Status), "ABORTED") {
		var previousStatus string
		err := executor.QueryRow(`SELECT COALESCE(status, '') FROM tests WHERE id = ?`, test.ID).Scan(&previousStatus)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("load prior test status for aborted occurrence reconciliation: %w", err)
		}
		allowMaterializedPrelaunchAbort = strings.EqualFold(strings.TrimSpace(previousStatus), "INITIALIZING")
	}
	query := `INSERT INTO tests (id, ref_key, materialization_key, name, status, application_id, release_id, scenario_id, project_id, folder_id, scenario_mode, termination_mode, shakeout_enabled, resource_baseline_enabled, start_delay, assignment_json, total_vusers, duration_ns, start_time, scheduled_start_at, started_at, pending_since, paused_at, cumulative_pause_ns, capacity_window_start_at, capacity_window_end_at, baseline_pinned, baseline_pinned_at, end_time, error, created_by_subject, created_by_display_name, started_by_subject, started_by_display_name, stopped_by_subject, stopped_by_display_name, groups_json, data_pools_json, runtime_settings_json, goal_config_json, traffic_profile_json, elk_capture_json, dynatrace_capture_json, start_authorization_json, execution_plan_approval_json, initial_provenance_json, timeslot_id)
			  VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			  ON CONFLICT(id) DO UPDATE SET
			  ref_key=excluded.ref_key,
			  materialization_key=excluded.materialization_key,
			  name=excluded.name, status=excluded.status, application_id=excluded.application_id, release_id=excluded.release_id, scenario_id=excluded.scenario_id,
			  project_id=excluded.project_id, folder_id=excluded.folder_id,
			  scenario_mode=excluded.scenario_mode, termination_mode=excluded.termination_mode, shakeout_enabled=excluded.shakeout_enabled,
			  resource_baseline_enabled=excluded.resource_baseline_enabled, start_delay=excluded.start_delay, assignment_json=excluded.assignment_json, total_vusers=excluded.total_vusers,
			  duration_ns=excluded.duration_ns, start_time=excluded.start_time, scheduled_start_at=excluded.scheduled_start_at, started_at=excluded.started_at, pending_since=excluded.pending_since,
			  paused_at=excluded.paused_at, cumulative_pause_ns=excluded.cumulative_pause_ns,
			  capacity_window_start_at=excluded.capacity_window_start_at, capacity_window_end_at=excluded.capacity_window_end_at,
			  baseline_pinned=excluded.baseline_pinned, baseline_pinned_at=excluded.baseline_pinned_at, end_time=excluded.end_time,
			  error=excluded.error, created_by_subject=excluded.created_by_subject, created_by_display_name=excluded.created_by_display_name,
			  started_by_subject=excluded.started_by_subject, started_by_display_name=excluded.started_by_display_name,
			  stopped_by_subject=excluded.stopped_by_subject, stopped_by_display_name=excluded.stopped_by_display_name, groups_json=excluded.groups_json, data_pools_json=excluded.data_pools_json,
			  runtime_settings_json=excluded.runtime_settings_json, goal_config_json=excluded.goal_config_json, traffic_profile_json=excluded.traffic_profile_json, elk_capture_json=excluded.elk_capture_json, dynatrace_capture_json=excluded.dynatrace_capture_json, start_authorization_json=excluded.start_authorization_json, execution_plan_approval_json=excluded.execution_plan_approval_json,
			  initial_provenance_json=CASE
			    WHEN tests.initial_provenance_json IS NULL OR TRIM(tests.initial_provenance_json) IN ('', 'null') THEN excluded.initial_provenance_json
			    ELSE tests.initial_provenance_json
			  END,
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
	if strings.EqualFold(strings.TrimSpace(test.Status), "ABORTED") {
		tx, ok := executor.(*storeTx)
		if !ok {
			return errors.New("aborted test persistence requires a transaction")
		}
		abortedAt := time.Time{}
		if test.EndTime != nil {
			abortedAt = *test.EndTime
		}
		if err := reconcileQueuedScenarioScheduleOccurrenceForTest(tx, test.ID, scenarioScheduleOccurrenceAborted, abortedAt, test.Error, allowMaterializedPrelaunchAbort); err != nil {
			return fmt.Errorf("reconcile queued scenario occurrence at test cancellation: %w", err)
		}
	}
	return nil
}

func (s *SQLiteStore) createTestIfAbsentWithExecutor(executor testSaveExecutor, test *TestRecord) (bool, error) {
	args, err := prepareTestSaveArgs(test)
	if err != nil {
		return false, err
	}
	exists, err := validateTestPlacement(executor, test, false, false)
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
	result, err := executor.Exec(`INSERT INTO tests (id, ref_key, materialization_key, name, status, application_id, release_id, scenario_id, project_id, folder_id, scenario_mode, termination_mode, shakeout_enabled, resource_baseline_enabled, start_delay, assignment_json, total_vusers, duration_ns, start_time, scheduled_start_at, started_at, pending_since, paused_at, cumulative_pause_ns, capacity_window_start_at, capacity_window_end_at, baseline_pinned, baseline_pinned_at, end_time, error, created_by_subject, created_by_display_name, started_by_subject, started_by_display_name, stopped_by_subject, stopped_by_display_name, groups_json, data_pools_json, runtime_settings_json, goal_config_json, traffic_profile_json, elk_capture_json, dynatrace_capture_json, start_authorization_json, execution_plan_approval_json, initial_provenance_json, timeslot_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
	dataPoolsJSON, err := marshalTestField("data_pools_json", test.DataPools)
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
	trafficProfileJSON, err := marshalTestField("traffic_profile_json", test.TrafficProfile)
	if err != nil {
		return nil, err
	}
	elkCaptureJSON, err := marshalTestField("elk_capture_json", test.ELKCapture)
	if err != nil {
		return nil, err
	}
	dynatraceCaptureJSON, err := marshalTestField("dynatrace_capture_json", test.DynatraceCapture)
	if err != nil {
		return nil, err
	}
	startAuthorizationJSON, err := marshalTestField("start_authorization_json", test.StartAuthorization)
	if err != nil {
		return nil, err
	}
	executionPlanApprovalJSON, err := marshalTestField("execution_plan_approval_json", test.ExecutionPlanApproval)
	if err != nil {
		return nil, err
	}
	initialProvenanceJSON, err := marshalTestField("initial_provenance_json", test.InitialProvenance)
	if err != nil {
		return nil, err
	}
	var initialProvenanceValue any
	if test.InitialProvenance != nil {
		initialProvenanceValue = initialProvenanceJSON
	}
	return []any{test.ID, test.RefKey, test.MaterializationKey, test.Name, test.Status, test.ApplicationID, test.ReleaseID, nullableString(test.ScenarioID), test.ProjectID, nullableStringPointer(test.FolderID), test.ScenarioMode, test.TerminationMode, test.ScriptShakeout, test.ResourceBaseline, test.StartDelay, assignmentJSON, test.TotalVUsers, test.Duration, test.StartTime, test.ScheduledStartAt, test.StartedAt, test.PendingSince, test.PausedAt, test.CumulativePause, test.CapacityWindowStartAt, test.CapacityWindowEndAt, test.BaselinePinned, test.BaselinePinnedAt, test.EndTime, test.Error, test.CreatedBySubject, test.CreatedByDisplayName, test.StartedBySubject, test.StartedByDisplayName, test.StoppedBySubject, test.StoppedByDisplayName, groupsJSON, dataPoolsJSON, runtimeSettingsJSON, goalConfigJSON, trafficProfileJSON, elkCaptureJSON, dynatraceCaptureJSON, startAuthorizationJSON, executionPlanApprovalJSON, initialProvenanceValue, nullableStringPointer(test.TimeslotID)}, nil
}

// validateTestPlacement locks and verifies every durable tenant-bearing parent
// before a test row is inserted or updated. The project lock matches project
// deletion's lock order, while the conditional upsert in saveTestWithExecutor
// remains the final fence for two concurrent first writers of the same test ID.
// It returns whether the test already exists.
func validateTestPlacement(executor testSaveExecutor, test *TestRecord, rejectRelocation, requireExistingStartIdentity bool) (bool, error) {
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
	if !exists && requireExistingStartIdentity {
		return false, fmt.Errorf("%w: test %q no longer exists", ErrTestStartIdentityChanged, test.ID)
	}
	if exists && rejectRelocation && normalizeProjectID(existingProjectID) != test.ProjectID {
		if requireExistingStartIdentity {
			return false, fmt.Errorf("%w: test %q moved from project %q to %q", ErrTestStartIdentityChanged, test.ID, test.ProjectID, normalizeProjectID(existingProjectID))
		}
		return false, fmt.Errorf("test %q belongs to project %q, not %q", test.ID, normalizeProjectID(existingProjectID), test.ProjectID)
	}
	if exists && !rejectRelocation {
		return true, nil
	}
	if !exists && !test.ExpectedProjectUpdatedAt.IsZero() {
		var projectUpdatedAt time.Time
		if err := executor.QueryRow(testProjectRevisionSelect, test.ProjectID).Scan(&projectUpdatedAt); err != nil {
			return false, err
		}
		if !projectUpdatedAt.Equal(test.ExpectedProjectUpdatedAt) {
			return false, fmt.Errorf("test project %q changed while the run was being prepared", test.ProjectID)
		}
	}

	if err := lockRequiredTestParent(executor,
		`UPDATE applications SET updated_at = updated_at WHERE id = ?`, test.ApplicationID, "application"); err != nil {
		return false, err
	}
	var applicationProjectID string
	var applicationUpdatedAt time.Time
	if err := executor.QueryRow(testApplicationRevisionSelect, test.ApplicationID).Scan(&applicationProjectID, &applicationUpdatedAt); err != nil {
		return false, err
	}
	if normalizeProjectID(applicationProjectID) != test.ProjectID {
		return false, fmt.Errorf("test application %q belongs to project %q, not %q", test.ApplicationID, normalizeProjectID(applicationProjectID), test.ProjectID)
	}
	if !exists && !test.ExpectedApplicationUpdatedAt.IsZero() && !applicationUpdatedAt.Equal(test.ExpectedApplicationUpdatedAt) {
		return false, fmt.Errorf("test application %q changed while the run was being prepared", test.ApplicationID)
	}

	if err := lockRequiredTestParent(executor,
		`UPDATE releases SET updated_at = updated_at WHERE id = ?`, test.ReleaseID, "release"); err != nil {
		return false, err
	}
	var releaseApplicationID, releaseProjectID string
	var releaseUpdatedAt time.Time
	var releaseArchivedAt sql.NullTime
	if err := executor.QueryRow(testReleaseRevisionSelect, test.ReleaseID).Scan(&releaseApplicationID, &releaseProjectID, &releaseUpdatedAt, &releaseArchivedAt); err != nil {
		return false, err
	}
	if releaseApplicationID != test.ApplicationID {
		return false, fmt.Errorf("test release %q belongs to application %q, not %q", test.ReleaseID, releaseApplicationID, test.ApplicationID)
	}
	if normalizeProjectID(releaseProjectID) != test.ProjectID {
		return false, fmt.Errorf("test release %q belongs to project %q, not %q", test.ReleaseID, normalizeProjectID(releaseProjectID), test.ProjectID)
	}
	if !exists && releaseArchivedAt.Valid {
		return false, fmt.Errorf("release is archived: %s", test.ReleaseID)
	}
	if !exists && !test.ExpectedReleaseUpdatedAt.IsZero() && !releaseUpdatedAt.Equal(test.ExpectedReleaseUpdatedAt) {
		return false, fmt.Errorf("test release %q changed while the run was being prepared", test.ReleaseID)
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

// SaveTestInitialProvenance writes only the immutable start-of-run evidence.
// It deliberately avoids a full-row UPSERT so a startup callback cannot replay
// stale lifecycle/configuration fields over concurrent control-plane updates.
func (s *SQLiteStore) SaveTestInitialProvenance(id, projectID string, provenance any) (bool, error) {
	return saveTestInitialProvenanceWithExecutor(s.db, id, projectID, provenance)
}

// SaveTestInitialProvenanceIfLeaseHeld combines the targeted write and HA
// execution-lease check in one transaction.
func (s *SQLiteStore) SaveTestInitialProvenanceIfLeaseHeld(id, projectID string, provenance any, leaseName, leaseOwnerID string) (bool, error) {
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
	saved, err := saveTestInitialProvenanceWithExecutor(tx, id, projectID, provenance)
	if err != nil || !saved {
		return saved, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func saveTestInitialProvenanceWithExecutor(executor testSaveExecutor, id, projectID string, provenance any) (bool, error) {
	id = strings.TrimSpace(id)
	if id == "" || provenance == nil {
		return false, errors.New("test id and initial provenance are required")
	}
	encoded, err := marshalTestField("initial_provenance_json", provenance)
	if err != nil {
		return false, err
	}
	result, err := executor.Exec(`UPDATE tests SET initial_provenance_json = CASE
		WHEN initial_provenance_json IS NULL OR TRIM(initial_provenance_json) IN ('', 'null') THEN ?
		ELSE initial_provenance_json END
		WHERE id = ? AND project_id = ?`, encoded, id, normalizeProjectID(projectID))
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected == 1, nil
}

// RepairTestRuntimeSnapshot rewrites only the two runtime-bearing JSON columns
// of one immutable test identity. It is used to scrub legacy snapshots without
// replaying a stale full TestRecord over concurrently changing lifecycle state.
func (s *SQLiteStore) RepairTestRuntimeSnapshot(id, projectID, materializationKey string, startTime time.Time, groups, runtimeSettings any) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	repaired, err := repairTestRuntimeSnapshotWithExecutor(tx, id, projectID, materializationKey, startTime, groups, runtimeSettings)
	if err != nil || !repaired {
		return repaired, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// RepairTestRuntimeSnapshotIfLeaseHeld combines the narrow runtime repair with
// the execution-epoch fence. A standby or stale leader therefore cannot scrub a
// replacement row or mutate the active leader's durable snapshot.
func (s *SQLiteStore) RepairTestRuntimeSnapshotIfLeaseHeld(id, projectID, materializationKey string, startTime time.Time, groups, runtimeSettings any, leaseName, leaseOwnerID string) (bool, error) {
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
	repaired, err := repairTestRuntimeSnapshotWithExecutor(tx, id, projectID, materializationKey, startTime, groups, runtimeSettings)
	if err != nil || !repaired {
		return repaired, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func repairTestRuntimeSnapshotWithExecutor(executor testSaveExecutor, id, projectID, materializationKey string, startTime time.Time, groups, runtimeSettings any) (bool, error) {
	id = strings.TrimSpace(id)
	projectID = normalizeProjectID(projectID)
	materializationKey = strings.TrimSpace(materializationKey)
	if id == "" || startTime.IsZero() {
		return false, errors.New("test identity is required for runtime snapshot repair")
	}
	groupsJSON, err := marshalTestField("groups_json", groups)
	if err != nil {
		return false, err
	}
	runtimeSettingsJSON, err := marshalTestField("runtime_settings_json", runtimeSettings)
	if err != nil {
		return false, err
	}
	result, err := executor.Exec(`
		UPDATE tests
		SET groups_json = ?, runtime_settings_json = ?
		WHERE id = ?
		  AND COALESCE(NULLIF(TRIM(project_id), ''), ?) = ?
		  AND COALESCE(materialization_key, '') = ?
		  AND start_time = ?
	`, groupsJSON, runtimeSettingsJSON, id, DefaultProjectID, projectID, materializationKey, startTime)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

// UpdateTestVUserScale persists only the workload columns a VUser scale
// reservation owns. A gradual ramp reserves up to one step per ramp interval,
// and routing each of those through the full-row upsert re-validated tenant
// placement, locked the parent release row, and re-serialized every unrelated
// JSON column of the run. Returns false when no row matched so the caller can
// fall back to the full-row write rather than silently dropping the reservation.
func (s *SQLiteStore) UpdateTestVUserScale(update TestVUserScaleUpdate) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	updated, err := updateTestVUserScaleWithExecutor(tx, update)
	if err != nil || !updated {
		return updated, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// UpdateTestVUserScaleIfLeaseHeld is the execution-lease fenced form of
// UpdateTestVUserScale. A controller that has lost the execution epoch can no
// longer publish workload changes for a run it used to own.
func (s *SQLiteStore) UpdateTestVUserScaleIfLeaseHeld(update TestVUserScaleUpdate, leaseName, leaseOwnerID string) (bool, error) {
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
	updated, err := updateTestVUserScaleWithExecutor(tx, update)
	if err != nil || !updated {
		return updated, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func updateTestVUserScaleWithExecutor(executor testSaveExecutor, update TestVUserScaleUpdate) (bool, error) {
	id := strings.TrimSpace(update.ID)
	if id == "" {
		return false, errors.New("test id is required for a VUser scale update")
	}
	if update.TotalVUsers < 0 {
		return false, errors.New("total vusers cannot be negative")
	}
	groupsJSON, err := marshalTestField("groups_json", update.Groups)
	if err != nil {
		return false, err
	}
	result, err := executor.Exec(`
		UPDATE tests
		SET total_vusers = ?, groups_json = ?
		WHERE id = ?
	`, update.TotalVUsers, groupsJSON, id)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
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

const testCapacityBindingReadBatchSize = 500

// ListTestCapacityBindings returns only the durable identity and reciprocal
// timeslot fields needed by HA list readers. Queries are chunked below SQLite's
// parameter ceiling (and remain efficient on PostgreSQL), so a dashboard list
// refresh never degrades into one GetTest call per row.
func (s *SQLiteStore) ListTestCapacityBindings(testIDs []string) ([]TestCapacityBindingState, error) {
	unique := make([]string, 0, len(testIDs))
	seen := make(map[string]struct{}, len(testIDs))
	for _, id := range testIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	states := make([]TestCapacityBindingState, 0, len(unique))
	for start := 0; start < len(unique); start += testCapacityBindingReadBatchSize {
		end := min(start+testCapacityBindingReadBatchSize, len(unique))
		batch := unique[start:end]
		args := make([]any, len(batch))
		for index, id := range batch {
			args[index] = id
		}
		rows, err := s.db.Query(`SELECT id, COALESCE(project_id, ''), start_time,
			timeslot_id, capacity_window_start_at, capacity_window_end_at
			FROM tests WHERE id IN (`+capacityBindingPlaceholders(len(batch))+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var state TestCapacityBindingState
			if err := rows.Scan(&state.ID, &state.ProjectID, &state.StartTime, &state.TimeslotID, &state.CapacityWindowStartAt, &state.CapacityWindowEndAt); err != nil {
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
	}
	return states, nil
}

func testSelectColumns() string {
	return `SELECT id, COALESCE(ref_key, ''), COALESCE(materialization_key, ''), name, status, COALESCE(application_id, ''), COALESCE(release_id, ''), COALESCE(scenario_id, ''), COALESCE(project_id, ''), folder_id, COALESCE(scenario_mode, ''), COALESCE(termination_mode, 'duration'), COALESCE(shakeout_enabled, 0), COALESCE(resource_baseline_enabled, 0), COALESCE(start_delay, ''), COALESCE(assignment_json, ''), total_vusers, duration_ns, start_time, scheduled_start_at, started_at, pending_since, paused_at, COALESCE(cumulative_pause_ns, 0), capacity_window_start_at, capacity_window_end_at, COALESCE(baseline_pinned, 0), baseline_pinned_at, end_time, error, COALESCE(created_by_subject, ''), COALESCE(created_by_display_name, ''), COALESCE(started_by_subject, ''), COALESCE(started_by_display_name, ''), COALESCE(stopped_by_subject, ''), COALESCE(stopped_by_display_name, ''), groups_json, COALESCE(data_pools_json, ''), COALESCE(runtime_settings_json, ''), COALESCE(goal_config_json, ''), COALESCE(traffic_profile_json, ''), COALESCE(elk_capture_json, ''), COALESCE(dynatrace_capture_json, ''), COALESCE(start_authorization_json, ''), COALESCE(execution_plan_approval_json, ''), COALESCE(initial_provenance_json, ''), timeslot_id, COALESCE(continuity_checkpoint_json, '')`
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
	var groupsJSON, dataPoolsJSON, assignmentJSON, runtimeSettingsJSON, goalConfigJSON, trafficProfileJSON, elkCaptureJSON, dynatraceCaptureJSON, startAuthorizationJSON, executionPlanApprovalJSON, initialProvenanceJSON string
	if err := scanner.Scan(&test.ID, &test.RefKey, &test.MaterializationKey, &test.Name, &test.Status, &test.ApplicationID, &test.ReleaseID, &test.ScenarioID, &test.ProjectID, &test.FolderID, &test.ScenarioMode, &test.TerminationMode, &test.ScriptShakeout, &test.ResourceBaseline, &test.StartDelay, &assignmentJSON, &test.TotalVUsers, &test.Duration, &test.StartTime, &test.ScheduledStartAt, &test.StartedAt, &test.PendingSince, &test.PausedAt, &test.CumulativePause, &test.CapacityWindowStartAt, &test.CapacityWindowEndAt, &test.BaselinePinned, &test.BaselinePinnedAt, &test.EndTime, &test.Error, &test.CreatedBySubject, &test.CreatedByDisplayName, &test.StartedBySubject, &test.StartedByDisplayName, &test.StoppedBySubject, &test.StoppedByDisplayName, &groupsJSON, &dataPoolsJSON, &runtimeSettingsJSON, &goalConfigJSON, &trafficProfileJSON, &elkCaptureJSON, &dynatraceCaptureJSON, &startAuthorizationJSON, &executionPlanApprovalJSON, &initialProvenanceJSON, &test.TimeslotID, &test.ContinuityCheckpoint); err != nil {
		return test, err
	}
	if err := json.Unmarshal([]byte(groupsJSON), &test.Groups); err != nil {
		return test, fmt.Errorf("decode test %s groups_json: %w", test.ID, err)
	}
	if dataPoolsJSON != "" && dataPoolsJSON != "null" {
		if err := json.Unmarshal([]byte(dataPoolsJSON), &test.DataPools); err != nil {
			return test, fmt.Errorf("decode test %s data_pools_json: %w", test.ID, err)
		}
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
	if trafficProfileJSON != "" && trafficProfileJSON != "null" {
		if err := json.Unmarshal([]byte(trafficProfileJSON), &test.TrafficProfile); err != nil {
			return test, fmt.Errorf("decode test %s traffic_profile_json: %w", test.ID, err)
		}
	}
	if elkCaptureJSON != "" && elkCaptureJSON != "null" {
		if err := json.Unmarshal([]byte(elkCaptureJSON), &test.ELKCapture); err != nil {
			return test, fmt.Errorf("decode test %s elk_capture_json: %w", test.ID, err)
		}
	}
	if dynatraceCaptureJSON != "" && dynatraceCaptureJSON != "null" {
		if err := json.Unmarshal([]byte(dynatraceCaptureJSON), &test.DynatraceCapture); err != nil {
			return test, fmt.Errorf("decode test %s dynatrace_capture_json: %w", test.ID, err)
		}
	}
	if startAuthorizationJSON != "" && startAuthorizationJSON != "null" {
		if err := json.Unmarshal([]byte(startAuthorizationJSON), &test.StartAuthorization); err != nil {
			return test, fmt.Errorf("decode test %s start_authorization_json: %w", test.ID, err)
		}
	}
	if executionPlanApprovalJSON != "" && executionPlanApprovalJSON != "null" {
		if err := json.Unmarshal([]byte(executionPlanApprovalJSON), &test.ExecutionPlanApproval); err != nil {
			return test, fmt.Errorf("decode test %s execution_plan_approval_json: %w", test.ID, err)
		}
	}
	if initialProvenanceJSON != "" && initialProvenanceJSON != "null" {
		if err := json.Unmarshal([]byte(initialProvenanceJSON), &test.InitialProvenance); err != nil {
			return test, fmt.Errorf("decode test %s initial_provenance_json: %w", test.ID, err)
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
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE tests SET status = ?, end_time = ?, error = ? WHERE id = ?`, status, endTime, errStr, id); err != nil {
		return err
	}
	if strings.EqualFold(strings.TrimSpace(status), "FAILED") {
		failedAt := time.Time{}
		if endTime != nil {
			failedAt = *endTime
		}
		if err := reconcileQueuedScenarioScheduleOccurrenceForTest(tx, id, scenarioScheduleOccurrenceFailed, failedAt, errStr, false); err != nil {
			return fmt.Errorf("reconcile queued scenario occurrence at test failure: %w", err)
		}
	}
	if err := recordUnconfirmedTerminalArchive(tx, id, status); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) UpdateTestStatusIfActive(id string, status string, endTime *time.Time, errStr string) (TestStatusTransitionResult, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
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
		if strings.EqualFold(strings.TrimSpace(status), "FAILED") {
			failedAt := time.Time{}
			if endTime != nil {
				failedAt = *endTime
			}
			if err := reconcileQueuedScenarioScheduleOccurrenceForTest(tx, id, scenarioScheduleOccurrenceFailed, failedAt, errStr, false); err != nil {
				return "", fmt.Errorf("reconcile queued scenario occurrence at test failure: %w", err)
			}
		}
		if err := recordUnconfirmedTerminalArchive(tx, id, status); err != nil {
			return "", err
		}
		if err := tx.Commit(); err != nil {
			return "", err
		}
		return TestStatusTransitionUpdated, nil
	}
	var current string
	if err := tx.QueryRow(`SELECT status FROM tests WHERE id = ?`, id).Scan(&current); err != nil {
		if err == sql.ErrNoRows {
			return TestStatusTransitionMissing, nil
		}
		return "", err
	}
	if current == status {
		if strings.EqualFold(strings.TrimSpace(status), "FAILED") {
			failedAt := time.Time{}
			if endTime != nil {
				failedAt = *endTime
			}
			if err := reconcileQueuedScenarioScheduleOccurrenceForTest(tx, id, scenarioScheduleOccurrenceFailed, failedAt, errStr, false); err != nil {
				return "", fmt.Errorf("reconcile queued scenario occurrence at unchanged test failure: %w", err)
			}
		}
		if err := recordUnconfirmedTerminalArchive(tx, id, status); err != nil {
			return "", err
		}
		if err := tx.Commit(); err != nil {
			return "", err
		}
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
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
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
		if err := recordUnconfirmedTerminalArchive(tx, id, status); err != nil {
			return "", err
		}
		if err := tx.Commit(); err != nil {
			return "", err
		}
		return TestStatusTransitionUpdated, nil
	}
	if err := tx.Rollback(); err != nil {
		return "", err
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
		if err := recordUnconfirmedTerminalArchive(tx, id, status); err != nil {
			return "", err
		}
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
		if strings.EqualFold(strings.TrimSpace(status), "FAILED") {
			failedAt := time.Time{}
			if endTime != nil {
				failedAt = *endTime
			}
			if err := reconcileQueuedScenarioScheduleOccurrenceForTest(tx, id, scenarioScheduleOccurrenceFailed, failedAt, errStr, false); err != nil {
				return "", fmt.Errorf("reconcile queued scenario occurrence at lease-fenced test failure: %w", err)
			}
		}
		if err := recordUnconfirmedTerminalArchive(tx, id, status); err != nil {
			return "", err
		}
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
		if strings.EqualFold(strings.TrimSpace(status), "FAILED") {
			failedAt := time.Time{}
			if endTime != nil {
				failedAt = *endTime
			}
			if err := reconcileQueuedScenarioScheduleOccurrenceForTest(tx, id, scenarioScheduleOccurrenceFailed, failedAt, errStr, false); err != nil {
				return "", fmt.Errorf("reconcile queued scenario occurrence at unchanged lease-fenced test failure: %w", err)
			}
		}
		if err := recordUnconfirmedTerminalArchive(tx, id, status); err != nil {
			return "", err
		}
		if err := tx.Commit(); err != nil {
			return "", err
		}
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

// UpdateTestContinuityCheckpointIfLeaseHeld writes the HA run-continuity
// adoption checkpoint for one run, but only while leaseOwnerID holds the
// execution lease. A non-empty checkpoint is written only to an active run so a
// stale leader cannot arm adoption for a run a successor already finished; an
// empty checkpoint clears the column in any status.
func (s *SQLiteStore) UpdateTestContinuityCheckpointIfLeaseHeld(id, checkpoint, leaseName, leaseOwnerID string) (TestStatusTransitionResult, error) {
	return s.updateTestContinuityCheckpointIfLeaseHeld(id, nil, checkpoint, leaseName, leaseOwnerID)
}

// CompareAndSwapTestContinuityCheckpointIfLeaseHeld additionally binds the write
// to the exact durable snapshot read before planning an adoption token rotation.
func (s *SQLiteStore) CompareAndSwapTestContinuityCheckpointIfLeaseHeld(id, expected, checkpoint, leaseName, leaseOwnerID string) (TestStatusTransitionResult, error) {
	return s.updateTestContinuityCheckpointIfLeaseHeld(id, &expected, checkpoint, leaseName, leaseOwnerID)
}

func (s *SQLiteStore) updateTestContinuityCheckpointIfLeaseHeld(id string, expected *string, checkpoint, leaseName, leaseOwnerID string) (TestStatusTransitionResult, error) {
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
	var value any
	query := `UPDATE tests SET continuity_checkpoint_json = ? WHERE id = ?`
	if strings.TrimSpace(checkpoint) != "" {
		value = checkpoint
		query += ` AND status IN ('INITIALIZING', 'STARTING', 'RUNNING', 'PAUSED', 'STOPPING')`
	}
	args := []any{value, id}
	if expected != nil {
		query += ` AND COALESCE(continuity_checkpoint_json, '') = ?`
		args = append(args, *expected)
	}
	result, err := tx.Exec(query, args...)
	if err != nil {
		return "", err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return "", err
	}
	if affected == 0 {
		var exists int
		if err := tx.QueryRow(`SELECT 1 FROM tests WHERE id = ?`, id).Scan(&exists); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return TestStatusTransitionMissing, nil
			}
			return "", err
		}
		// The run is no longer active: nothing to arm.
		return TestStatusTransitionUnchanged, nil
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return TestStatusTransitionUpdated, nil
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
// fields, rather than from a replica-local map. Pinning changes only the target; several historical runs may remain pinned. A nil
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

	predicate, predicateArgs := `id = ?`, []any{id}
	if pinnedAt == nil {
		_, err = tx.Exec(`UPDATE tests SET baseline_pinned = ?, baseline_pinned_at = NULL WHERE id = ?`, false, id)
	} else {
		_, err = tx.Exec(`UPDATE tests SET baseline_pinned = ?, baseline_pinned_at = ? WHERE id = ?`, true, pinnedAt.UTC(), id)
	}
	if err != nil {
		return nil, err
	}

	rows, err := tx.Query(`SELECT id, COALESCE(baseline_pinned, 0), baseline_pinned_at FROM tests WHERE `+predicate+` ORDER BY id`, predicateArgs...)
	if err != nil {
		return nil, err
	}
	// Idempotent belt-and-braces: the explicit Close below is kept because it
	// surfaces the Close error, which a bare defer discards. The defer covers
	// the paths that never reach it — notably a panic in a scan helper, which
	// the controller recovers, pinning this connection for the process's life.
	defer rows.Close()
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
	if err := queueAnalysisCleanupTx(tx, "monitor", projectID, id); err != nil {
		return err
	}
	if err := reconcileQueuedScenarioScheduleOccurrenceForTestDeletion(tx, id); err != nil {
		return err
	}
	var incompleteControllerChecks int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM github_check_links
		WHERE test_id = ? AND kind = 'controller'
			AND (LOWER(COALESCE(status, '')) <> 'completed' OR COALESCE(conclusion, '') = '')`, id).
		Scan(&incompleteControllerChecks); err != nil {
		return err
	}
	if incompleteControllerChecks > 0 {
		return fmt.Errorf("test has an incomplete controller-owned GitHub Check")
	}
	archive, err := getRunArchiveTx(tx, id)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	job, jobErr := getArchiveFinalizationJobTx(tx, id)
	if jobErr != nil && jobErr != sql.ErrNoRows {
		return jobErr
	}
	if archive != nil {
		if err := enqueueArchiveCleanupTx(tx, projectID, archive); err != nil {
			return err
		}
		// A refresh/re-finalization in flight has already advanced the pending
		// intent to a NEW object (UpdateArchiveFinalizationRecordIfToken) while
		// run_archives still points at the previous one. The DELETE below drops
		// that intent row, so its object has to be queued as well or the bytes
		// stay in object storage with nothing left that references them.
		if err := enqueuePendingArchiveObjectCleanupTx(tx, projectID, archive, job); err != nil {
			return err
		}
	} else if job != nil {
		if err := enqueueArchiveCleanupTx(tx, projectID, &job.ArchiveRecord); err != nil {
			return err
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
	if _, err := tx.Exec(`DELETE FROM grafana_dashboard_snapshots WHERE test_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM github_check_links WHERE test_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM github_check_intents WHERE test_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM powerbi_publishes WHERE test_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM runtime_secret_provisioning_attempts WHERE test_id = ?`, id); err != nil {
		return err
	}
	annotationPrefix := "report:annotations:" + id + ":"
	if _, err := tx.Exec(`DELETE FROM app_settings
		WHERE SUBSTR(key, 1, LENGTH(?)) = ?`, annotationPrefix, annotationPrefix); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE scenario_schedules SET last_run_test_id = NULL WHERE last_run_test_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE scenario_schedule_occurrences SET test_id = NULL WHERE test_id = ?`, id); err != nil {
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
		transactionsJSON, err := marshalNullableJSON(record.Transactions)
		if err != nil {
			return err
		}
		byRegionJSON, err := marshalNullableJSON(record.ByRegion)
		if err != nil {
			return err
		}
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
			transactions_json, by_region_json, browser_json, browser_by_device_json, latency_json,
			dropped_results, websocket_clients, websocket_dropped_messages, websocket_dropped_events,
			generator_published_at, controller_received_at, collected_at, published_at,
			generator_processing_lag_ms, transport_lag_ms, controller_fanout_lag_ms, sample_age_ms
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
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
			transactionsJSON,
			byRegionJSON,
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
		transactions_json,
		by_region_json,
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
		var transactionsJSON sql.NullString
		var byRegionJSON sql.NullString
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
			&transactionsJSON,
			&byRegionJSON,
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
		if transactionsJSON.Valid && strings.TrimSpace(transactionsJSON.String) != "" {
			if err := json.Unmarshal([]byte(transactionsJSON.String), &record.Transactions); err != nil {
				return nil, err
			}
		}
		if byRegionJSON.Valid && strings.TrimSpace(byRegionJSON.String) != "" {
			if err := json.Unmarshal([]byte(byRegionJSON.String), &record.ByRegion); err != nil {
				return nil, err
			}
		}
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
		CompletedIterations int64                                 `json:"completed_iterations,omitempty"`
		Timestamp           time.Time                             `json:"timestamp"`
		ActiveVUsers        int64                                 `json:"active_vusers"`
		TPS                 float64                               `json:"tps"`
		AvgResponseMs       float64                               `json:"avg_response_ms"`
		ErrorPct            float64                               `json:"error_pct"`
		TotalTx             int64                                 `json:"total_transactions"`
		TotalErrors         int64                                 `json:"total_errors"`
		P50Ms               float64                               `json:"p50_ms"`
		P90Ms               float64                               `json:"p90_ms"`
		P95Ms               float64                               `json:"p95_ms"`
		P99Ms               float64                               `json:"p99_ms"`
		Transactions        map[string]*IntervalTransactionRecord `json:"transactions,omitempty"`
		ByRegion            map[string]*IntervalTransactionRecord `json:"by_region,omitempty"`
		Browser             *browserEnvelope                      `json:"browser,omitempty"`
		BrowserByDevice     map[string]*browserDeviceEnvelope     `json:"browser_by_device,omitempty"`
		Latency             *NetworkLatencySummaryRecord          `json:"latency,omitempty"`
		Telemetry           *telemetryEnvelope                    `json:"telemetry,omitempty"`
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
		CompletedIterations: summary.CompletedIterations,
		SchemaVersion:       intervalSummarySchemaVersion,
		Timestamp:           summary.Timestamp,
		ActiveVUsers:        summary.ActiveVUsers,
		TPS:                 summary.TPS,
		AvgResponseMs:       summary.AvgResponseMs,
		ErrorPct:            summary.ErrorPct,
		TotalTx:             summary.TotalTx,
		TotalErrors:         summary.TotalErrors,
		P50Ms:               summary.P50Ms,
		P90Ms:               summary.P90Ms,
		P95Ms:               summary.P95Ms,
		P99Ms:               summary.P99Ms,
		Transactions:        compactIntervalSummaryTransactions(summary.Transactions),
		ByRegion:            compactIntervalSummaryTransactions(summary.ByRegion),
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

const (
	maxPersistedIntervalSummaryTransactions = 512
	intervalSummaryTransactionOverflow      = "__other__"
)

func compactIntervalSummaryTransactions(metrics map[string]*IntervalTransactionRecord) map[string]*IntervalTransactionRecord {
	if len(metrics) == 0 {
		return nil
	}

	merged := make(map[string]*IntervalTransactionRecord, len(metrics))
	for fallbackName, metric := range metrics {
		if metric == nil {
			continue
		}
		name := strings.TrimSpace(metric.Name)
		if name == "" {
			name = fallbackName
		}
		name = persistedIntervalTransactionName(name)
		mergeIntervalSummaryTransaction(merged, name, metric)
	}
	if len(merged) == 0 {
		return nil
	}

	keys := make([]string, 0, len(merged))
	for name := range merged {
		if name != intervalSummaryTransactionOverflow {
			keys = append(keys, name)
		}
	}
	sort.Strings(keys)
	overflow := merged[intervalSummaryTransactionOverflow]
	entryCount := len(keys)
	if overflow != nil {
		entryCount++
	}
	if entryCount <= maxPersistedIntervalSummaryTransactions {
		return merged
	}

	compacted := make(map[string]*IntervalTransactionRecord, maxPersistedIntervalSummaryTransactions)
	keep := maxPersistedIntervalSummaryTransactions - 1
	for index, name := range keys {
		if index < keep {
			mergeIntervalSummaryTransaction(compacted, name, merged[name])
			continue
		}
		mergeIntervalSummaryTransaction(compacted, intervalSummaryTransactionOverflow, merged[name])
	}
	if overflow != nil {
		mergeIntervalSummaryTransaction(compacted, intervalSummaryTransactionOverflow, overflow)
	}
	return compacted
}

func mergeIntervalSummaryTransaction(destination map[string]*IntervalTransactionRecord, name string, incoming *IntervalTransactionRecord) {
	if destination == nil || incoming == nil {
		return
	}
	current := destination[name]
	if current == nil {
		copy := *incoming
		copy.Name = name
		destination[name] = &copy
		return
	}
	combinedCount := current.Count + incoming.Count
	if combinedCount > 0 {
		current.AvgMs = (current.AvgMs*float64(current.Count) + incoming.AvgMs*float64(incoming.Count)) / float64(combinedCount)
	}
	current.TPS += incoming.TPS
	current.Count = combinedCount
	current.Errors += incoming.Errors
}

func persistedIntervalTransactionName(name string) string {
	if name = strings.TrimSpace(scrub.ValueLabel(name, scrub.Policy{})); name != "" {
		return name
	}
	return "unnamed"
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
