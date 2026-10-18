package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

var (
	ErrScenarioAuthoringConflict             = errors.New("scenario authored content changed; reload before applying changes")
	ErrScenarioAuthoringNotFound             = errors.New("scenario authored content not found")
	ErrScenarioAuthoringUnavailable          = errors.New("scenario authored content revision unavailable")
	ErrScenarioAuthoringPreconditionRequired = errors.New("scenario authored content precondition required")
	ErrScenarioAuthoringInvalidRevision      = errors.New("invalid scenario authored content revision")
	// ErrScenarioAuthoringOutcomeUnknown marks a failed commit acknowledgement:
	// the mutation may or may not be durable, so callers must reload before
	// retrying. Errors before the commit are definite rollbacks.
	ErrScenarioAuthoringOutcomeUnknown = errors.New("scenario authored content commit outcome unknown")
	// ErrScenarioHasResults refuses deleting a scenario that runs reference.
	ErrScenarioHasResults = errors.New("scenario has test results")
)

// ScenarioAuthoringSnapshot contains saved overrides and reference identities,
// never effective script defaults or mutable joined display names. Every field
// and the content nonce are read in one repeatable database snapshot.
type ScenarioAuthoringSnapshot struct {
	Scenario          *Scenario
	Scripts           []ScenarioScript
	DataPools         []ScenarioDataPoolBinding
	LibraryAssignment *LibraryAssignment
	MonitorBindings   *MonitorBindings
	ProjectID         string
	ContentRevision   string
}
type ScenarioAuthoringPrecondition struct {
	ProjectID          string
	ContentRevision    string
	CreatedAt          time.Time
	LegacyOrdinarySave bool `json:"-"`
}
type ScenarioAuthoringMutationResult struct {
	Snapshot *ScenarioAuthoringSnapshot
	Applied  bool
}
type ScenarioAuthoringStore interface {
	GetScenarioAuthoringSnapshot(string) (*ScenarioAuthoringSnapshot, error)
	CreateScenarioAuthoringIfAbsent(*Scenario, []ScenarioScript, []ScenarioDataPoolBinding) (*ScenarioAuthoringMutationResult, error)
	UpdateScenarioAuthoringIfCurrent(*Scenario, []ScenarioScript, []ScenarioDataPoolBinding, ScenarioAuthoringPrecondition) (*ScenarioAuthoringMutationResult, error)
}
type ScenarioAuthoringReferenceStore interface {
	GetScenarioAuthoringSnapshotByRefKey(string) (*ScenarioAuthoringSnapshot, error)
}

var _ ScenarioAuthoringStore = (*SQLiteStore)(nil)
var _ ScenarioAuthoringReferenceStore = (*SQLiteStore)(nil)

func ValidScenarioContentRevision(value string) bool {
	if len(value) != len("scn-v1-")+64 || !strings.HasPrefix(value, "scn-v1-") {
		return false
	}
	for _, c := range value[len("scn-v1-"):] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func validateScenarioAuthoringPrecondition(expected ScenarioAuthoringPrecondition) error {
	if !ValidScenarioContentRevision(expected.ContentRevision) {
		return ErrScenarioAuthoringInvalidRevision
	}
	if strings.TrimSpace(expected.ProjectID) == "" || expected.CreatedAt.IsZero() {
		return errors.New("scenario project and created_at are required for conditional mutation")
	}
	return nil
}
func scenarioSelectColumns(dialect storeDialect) string {
	return `id, COALESCE(ref_key, ''), name, COALESCE(workload_shape_id, ''), duration, COALESCE(start_delay, ''), COALESCE(scenario_mode, ''), COALESCE(termination_mode, 'duration'), COALESCE(shakeout_enabled, 0), COALESCE(resource_baseline_enabled, 0), COALESCE(assignment_json, ''), COALESCE(goal_config_json, ''), COALESCE(runtime_settings_json, ''), COALESCE(traffic_profile_json, ''), created_at, updated_at, (SELECT archived_at FROM object_archive_states a WHERE a.resource_type='scenario' AND a.resource_id=scenarios.id AND ` + archiveIncarnationMatch(dialect, "a.incarnation", "scenarios.created_at") + `)`
}
func scanScenarioRow(row scriptRowScanner) (*Scenario, error) {
	var scenario Scenario
	var assignment, goals, runtime, traffic string
	if err := row.Scan(&scenario.ID, &scenario.RefKey, &scenario.Name, &scenario.WorkloadShapeID, &scenario.Duration, &scenario.StartDelay, &scenario.ScenarioMode, &scenario.TerminationMode, &scenario.ScriptShakeout, &scenario.ResourceBaseline, &assignment, &goals, &runtime, &traffic, &scenario.CreatedAt, &scenario.UpdatedAt, &scenario.ArchivedAt); err != nil {
		return nil, err
	}
	if err := decodeScenarioJSON(&scenario, assignment, goals, runtime, traffic); err != nil {
		return nil, err
	}
	return &scenario, nil
}

// Missing script owners cannot silently turn a corrupt scenario into a default
// project scenario. The existing derivation is reused after completeness checks.
func scenarioAuthoringProjectIDTx(tx *storeTx, id string) (string, error) {
	var total, matched int
	if err := tx.QueryRow(`SELECT COUNT(*), COUNT(s.id) FROM scenario_scripts ss LEFT JOIN scripts s ON s.id=ss.script_id WHERE ss.scenario_id=?`, id).Scan(&total, &matched); err != nil {
		return "", err
	}
	if total != matched {
		return "", ErrScenarioAuthoringUnavailable
	}
	project, exists, err := storedScenarioProjectIDTx(tx, id)
	if err != nil {
		return "", fmt.Errorf("%w: invalid durable scenario owner", ErrScenarioAuthoringUnavailable)
	}
	if !exists {
		return "", ErrScenarioAuthoringNotFound
	}
	return project, nil
}
func readScenarioAuthoringSnapshotTx(tx *storeTx, id string) (*ScenarioAuthoringSnapshot, error) {
	scenario, err := scanScenarioRow(tx.QueryRow(`SELECT `+scenarioSelectColumns(tx.dialect)+` FROM scenarios WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrScenarioAuthoringNotFound
	}
	if err != nil {
		return nil, err
	}
	var revision string
	if err := tx.QueryRow(`SELECT COALESCE(content_revision,'') FROM scenarios WHERE id=?`, id).Scan(&revision); err != nil {
		return nil, err
	}
	if !ValidScenarioContentRevision(revision) {
		return nil, ErrScenarioAuthoringUnavailable
	}
	project, err := scenarioAuthoringProjectIDTx(tx, id)
	if err != nil {
		return nil, err
	}
	scripts, err := listScenarioScriptsReader(tx, id)
	if err != nil {
		return nil, err
	}
	if scripts == nil {
		scripts = []ScenarioScript{}
	}
	pools, err := readScenarioAuthoringPoolsTx(tx, id, project)
	if err != nil {
		return nil, err
	}
	placement, err := readScenarioAuthoringPlacementTx(tx, id, project)
	if err != nil {
		return nil, err
	}
	monitors, err := readScenarioAuthoringMonitorsTx(tx, id, project)
	if err != nil {
		return nil, err
	}
	return &ScenarioAuthoringSnapshot{Scenario: scenario, Scripts: scripts, DataPools: pools, LibraryAssignment: placement, MonitorBindings: monitors, ProjectID: project, ContentRevision: revision}, nil
}
func readScenarioAuthoringPoolsTx(tx *storeTx, id, project string) ([]ScenarioDataPoolBinding, error) {
	rows, err := tx.Query(`SELECT b.id,b.scenario_id,COALESCE(b.pool_id,''),b.alias,b.access_mode,b.selection_strategy,b.created_at,b.updated_at,p.id,COALESCE(p.project_id,''),COALESCE(b.live_options_json,'null'),COALESCE(b.scope,''),COALESCE(b.project_table_id,''),pt.project_id FROM scenario_data_pool_bindings b LEFT JOIN shared_data_pools p ON p.id=b.pool_id LEFT JOIN project_live_tables pt ON pt.id=b.project_table_id WHERE b.scenario_id=? ORDER BY b.alias,b.id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ScenarioDataPoolBinding{}
	for rows.Next() {
		var b ScenarioDataPoolBinding
		var poolID, projectOwner sql.NullString
		var owner string
		var liveOptions string
		if err := rows.Scan(&b.ID, &b.ScenarioID, &b.PoolID, &b.Alias, &b.AccessMode, &b.SelectionStrategy, &b.CreatedAt, &b.UpdatedAt, &poolID, &owner, &liveOptions, &b.Scope, &b.ProjectTableID, &projectOwner); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(liveOptions), &b.LiveOptions); err != nil {
			return nil, ErrScenarioAuthoringUnavailable
		}
		if b.Scope == "project" && (!projectOwner.Valid || projectOwner.String != project || b.AccessMode != "live" || b.PoolID != "") {
			return nil, ErrScenarioAuthoringUnavailable
		}
		if (!poolID.Valid || normalizeProjectID(owner) != project) && !liveBindingHasNoSeedPool(b) {
			return nil, ErrScenarioAuthoringUnavailable
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
func readScenarioAuthoringPlacementTx(tx *storeTx, id, project string) (*LibraryAssignment, error) {
	assignment := emptyLibraryAssignment()
	var app sql.NullString
	err := tx.QueryRow(`SELECT application_id,revision FROM scenario_library_assignments WHERE item_id=?`, id).Scan(&app, &assignment.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return assignment, nil
	}
	if err != nil {
		return nil, err
	}
	if assignment.Revision < 0 {
		return nil, ErrScenarioAuthoringUnavailable
	}
	if app.Valid {
		var owner string
		if app.String == "" {
			return nil, ErrScenarioAuthoringUnavailable
		}
		if err := tx.QueryRow(`SELECT COALESCE(project_id,'') FROM applications WHERE id=?`, app.String).Scan(&owner); err != nil {
			return nil, ErrScenarioAuthoringUnavailable
		}
		if normalizeProjectID(owner) != project {
			return nil, ErrScenarioAuthoringUnavailable
		}
		assignment.Application = &LibraryAssignmentApplication{ID: app.String}
	}
	rows, err := tx.Query(`SELECT ar.release_id,r.id,r.application_id,COALESCE(r.project_id,'') FROM scenario_library_assignments_releases ar LEFT JOIN releases r ON r.id=ar.release_id WHERE ar.item_id=? ORDER BY ar.release_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var releaseID, owner string
		var actualID, releaseApp sql.NullString
		if err := rows.Scan(&releaseID, &actualID, &releaseApp, &owner); err != nil {
			return nil, err
		}
		if !app.Valid || !actualID.Valid || !releaseApp.Valid || releaseApp.String != app.String || normalizeProjectID(owner) != project {
			return nil, ErrScenarioAuthoringUnavailable
		}
		assignment.Releases = append(assignment.Releases, LibraryAssignmentRelease{ID: releaseID, ApplicationID: app.String})
	}
	return assignment, rows.Err()
}
func readScenarioAuthoringMonitorsTx(tx *storeTx, id, project string) (*MonitorBindings, error) {
	out := &MonitorBindings{ScenarioID: id, ProjectID: project, Sources: []MonitorBinding{}}
	var owner, body string
	var revision int64
	err := tx.QueryRow(`SELECT project_id,revision,payload FROM monitor_bindings WHERE scenario_id=?`, id).Scan(&owner, &revision, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(body), out); err != nil {
		return nil, ErrScenarioAuthoringUnavailable
	}
	if owner != project || out.ProjectID != project || out.ScenarioID != id || out.Revision != revision || revision <= 0 || len(out.Sources) > MaxRunMonitorSources {
		return nil, ErrScenarioAuthoringUnavailable
	}
	seen := map[string]bool{}
	for _, binding := range out.Sources {
		if strings.TrimSpace(binding.SourceID) == "" || seen[binding.SourceID] {
			return nil, ErrScenarioAuthoringUnavailable
		}
		seen[binding.SourceID] = true
	}
	if out.Sources == nil {
		out.Sources = []MonitorBinding{}
	}
	// Source order is a set of exact identities, independent of provider context.
	sort.Slice(out.Sources, func(i, j int) bool { return out.Sources[i].SourceID < out.Sources[j].SourceID })
	return out, nil
}
func (s *SQLiteStore) GetScenarioAuthoringSnapshot(id string) (*ScenarioAuthoringSnapshot, error) {
	tx, err := s.beginScriptAuthoringRead()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	snapshot, err := readScenarioAuthoringSnapshotTx(tx, id)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return snapshot, nil
}
func (s *SQLiteStore) GetScenarioAuthoringSnapshotByRefKey(refKey string) (*ScenarioAuthoringSnapshot, error) {
	if strings.TrimSpace(refKey) == "" {
		return nil, ErrScenarioAuthoringNotFound
	}
	tx, err := s.beginScriptAuthoringRead()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT id FROM scenarios WHERE ref_key=? ORDER BY id LIMIT 2`, refKey)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	closeErr := rows.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(ids) == 0 {
		return nil, ErrScenarioAuthoringNotFound
	}
	if len(ids) != 1 {
		return nil, ErrScenarioAuthoringUnavailable
	}
	snapshot, err := readScenarioAuthoringSnapshotTx(tx, ids[0])
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return snapshot, nil
}

// Lock project before scenario and then referenced scripts, matching placement,
// monitor and hierarchy mutation order. A read does not acquire source locks.
func scenarioRequestedProjectIDTx(tx *storeTx, scripts []ScenarioScript) (string, error) {
	var project string
	ordered := append([]ScenarioScript(nil), scripts...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ScriptID < ordered[j].ScriptID })
	for _, group := range ordered {
		if strings.TrimSpace(group.ScriptID) == "" {
			return "", errors.New("scenario script id is required")
		}
		var owner string
		if err := tx.QueryRow(`SELECT COALESCE(project_id,'') FROM scripts WHERE id=?`, group.ScriptID).Scan(&owner); err != nil {
			return "", err
		}
		owner = normalizeProjectID(owner)
		if project != "" && owner != project {
			return "", errors.New("scenario scripts span multiple projects")
		}
		project = owner
	}
	return normalizeProjectID(project), nil
}

// SQLite write transactions must reserve the database writer before any read.
// Otherwise two replicas can both read a deferred snapshot and the loser cannot
// upgrade it after the winner commits (SQLITE_BUSY ignores busy_timeout then).
// This no-op acquires SQLite's database write lock without changing evidence.
func lockScenarioSQLiteWriterTx(tx *storeTx, id string) error {
	if tx.dialect != storeDialectSQLite {
		return nil
	}
	_, err := tx.Exec(`UPDATE scenarios SET updated_at=updated_at WHERE id=?`, id)
	return err
}

// Create-only collisions retain the legacy false/no-error replay result; they
// never authorize a rewrite of an existing object's tenant or content.
func lockScenarioCreateProjectTx(tx *storeTx, id string, scripts []ScenarioScript) (string, error) {
	if err := validateScenarioScriptJSON(id, scripts); err != nil {
		return "", err
	}
	if err := lockScenarioSQLiteWriterTx(tx, id); err != nil {
		return "", err
	}
	requested, err := scenarioRequestedProjectIDTx(tx, scripts)
	if err != nil {
		return "", err
	}
	return lockProjectForOwnedMutation(tx, requested)
}
func lockScenarioMutationProjectTx(tx *storeTx, id string, scripts []ScenarioScript) (string, error) {
	if err := validateScenarioScriptJSON(id, scripts); err != nil {
		return "", err
	}
	if err := lockScenarioSQLiteWriterTx(tx, id); err != nil {
		return "", err
	}
	requested, err := scenarioRequestedProjectIDTx(tx, scripts)
	if err != nil {
		return "", err
	}
	current, exists, err := storedScenarioProjectIDTx(tx, id)
	if err != nil {
		return "", err
	}
	if exists && current != requested {
		return "", errors.New("scenario project ownership cannot change")
	}
	if _, err := lockProjectForOwnedMutation(tx, requested); err != nil {
		return "", err
	}
	// Revalidate after the hierarchy lock; another writer may have won earlier.
	current, exists, err = storedScenarioProjectIDTx(tx, id)
	if err != nil {
		return "", err
	}
	if exists && current != requested {
		return "", errors.New("scenario project ownership cannot change")
	}
	return requested, nil
}
func lockScenarioExistingProjectTx(tx *storeTx, id string) error {
	if err := lockScenarioSQLiteWriterTx(tx, id); err != nil {
		return err
	}
	project, exists, err := storedScenarioProjectIDTx(tx, id)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	_, err = lockProjectForOwnedMutation(tx, project)
	return err
}
func cloneScenarioAuthoringInput(scenario *Scenario, scripts []ScenarioScript, pools []ScenarioDataPoolBinding) (*Scenario, []ScenarioScript, []ScenarioDataPoolBinding, error) {
	if scenario == nil {
		return nil, nil, nil, errors.New("scenario is required")
	}
	input := struct {
		Scenario *Scenario
		Scripts  []ScenarioScript
		Pools    []ScenarioDataPoolBinding
	}{scenario, scripts, pools}
	encoded, err := json.Marshal(input)
	if err != nil {
		return nil, nil, nil, err
	}
	var cloned struct {
		Scenario *Scenario
		Scripts  []ScenarioScript
		Pools    []ScenarioDataPoolBinding
	}
	if err := json.Unmarshal(encoded, &cloned); err != nil {
		return nil, nil, nil, err
	}
	// ScenarioScript's display JSON omits empty stages, but persisted authoring
	// distinguishes an explicit empty schedule from absent/inherited stages.
	for i, group := range scripts {
		if group.Stages != nil && len(group.Stages) == 0 {
			cloned.Scripts[i].Stages = []LoadStage{}
		}
	}
	return cloned.Scenario, cloned.Scripts, cloned.Pools, nil
}
func (s *SQLiteStore) CreateScenarioAuthoringIfAbsent(input *Scenario, groups []ScenarioScript, pools []ScenarioDataPoolBinding) (*ScenarioAuthoringMutationResult, error) {
	return s.createScenarioAuthoringIfAbsent(input, groups, pools, nil)
}
func (s *SQLiteStore) createScenarioAuthoringIfAbsent(input *Scenario, groups []ScenarioScript, pools []ScenarioDataPoolBinding, admission *ScenarioSourceAdmission) (*ScenarioAuthoringMutationResult, error) {
	scenario, groups, pools, err := cloneScenarioAuthoringInput(input, groups, pools)
	if err != nil {
		return nil, err
	}
	scenario.CreatedAt = newRowTimestamp(scenario.CreatedAt)
	assignment, goals, runtime, traffic, err := prepareScenarioForSave(scenario)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var project string
	if admission != nil {
		if err := acquireLibraryImportPublicationFenceTx(tx); err != nil {
			return nil, err
		}
		project, err = lockProjectForOwnedMutation(tx, admission.ProjectID)
		if err != nil {
			return nil, err
		}
		if err := lockPrivateScenarioCreateTx(tx, scenario.ID); err != nil {
			return nil, err
		}
		if err := admitScenarioSourcesTx(tx, admission, pools); err != nil {
			return nil, err
		}
	} else {
		project, err = lockScenarioCreateProjectTx(tx, scenario.ID, groups)
		if err != nil {
			return nil, err
		}
	}
	if err := validateScenarioAuthoringPoolsActiveTx(tx, pools); err != nil {
		return nil, err
	}
	applied, err := createScenarioWithBindingsTx(tx, scenario, groups, pools, assignment, goals, runtime, traffic)
	if err != nil {
		return nil, err
	}
	if admission != nil && !applied {
		return nil, ErrScenarioSourceAdmissionConflict
	}
	snapshot, err := readScenarioAuthoringSnapshotTx(tx, scenario.ID)
	if err != nil {
		return nil, err
	}
	if snapshot.ProjectID != project {
		return nil, ErrScenarioAuthoringNotFound
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrScenarioAuthoringOutcomeUnknown, err)
	}
	return &ScenarioAuthoringMutationResult{Snapshot: snapshot, Applied: applied}, nil
}
func (s *SQLiteStore) UpdateScenarioAuthoringIfCurrent(input *Scenario, groups []ScenarioScript, pools []ScenarioDataPoolBinding, expected ScenarioAuthoringPrecondition) (*ScenarioAuthoringMutationResult, error) {
	return s.updateScenarioAuthoringIfCurrent(input, groups, pools, expected, nil)
}
func (s *SQLiteStore) updateScenarioAuthoringIfCurrent(input *Scenario, groups []ScenarioScript, pools []ScenarioDataPoolBinding, expected ScenarioAuthoringPrecondition, admission *ScenarioSourceAdmission) (*ScenarioAuthoringMutationResult, error) {
	if err := validateScenarioAuthoringPrecondition(expected); err != nil {
		return nil, err
	}
	scenario, groups, pools, err := cloneScenarioAuthoringInput(input, groups, pools)
	if err != nil {
		return nil, err
	}
	scenario.CreatedAt = expected.CreatedAt
	scenario.ArchivedAt = nil
	assignment, goals, runtime, traffic, err := prepareScenarioForSave(scenario)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if admission != nil {
		if err := acquireLibraryImportPublicationFenceTx(tx); err != nil {
			return nil, err
		}
		if _, err := lockProjectForOwnedMutation(tx, admission.ProjectID); err != nil {
			return nil, err
		}
	}
	if err := lockScenarioSQLiteWriterTx(tx, scenario.ID); err != nil {
		return nil, err
	}
	owner, exists, err := storedScenarioProjectIDTx(tx, scenario.ID)
	if err != nil {
		return nil, err
	}
	if !exists || owner != normalizeProjectID(expected.ProjectID) {
		return nil, ErrScenarioAuthoringNotFound
	}
	if _, err := lockProjectForOwnedMutation(tx, owner); err != nil {
		return nil, err
	}
	query := `SELECT created_at,COALESCE(content_revision,'') FROM scenarios WHERE id=?`
	if tx.dialect == storeDialectPostgres {
		query += ` FOR UPDATE`
	}
	var created time.Time
	var revision string
	if err := tx.QueryRow(query, scenario.ID).Scan(&created, &revision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrScenarioAuthoringNotFound
		}
		return nil, err
	}
	currentOwner, err := scenarioAuthoringProjectIDTx(tx, scenario.ID)
	if err != nil {
		return nil, err
	}
	if currentOwner != owner || !created.Equal(expected.CreatedAt) {
		return nil, ErrScenarioAuthoringNotFound
	}
	if !ValidScenarioContentRevision(revision) {
		return nil, ErrScenarioAuthoringUnavailable
	}
	if expected.LegacyOrdinarySave {
		strict, err := scenarioStrictSavePolicyTx(tx, owner)
		if err != nil {
			return nil, err
		}
		if strict {
			return nil, ErrScenarioAuthoringPreconditionRequired
		}
	}
	if revision != expected.ContentRevision {
		return nil, ErrScenarioAuthoringConflict
	}
	if err := ensureObjectActiveTx(tx, "scenario", scenario.ID); err != nil {
		return nil, err
	}
	if err := validateScenarioScriptJSON(scenario.ID, groups); err != nil {
		return nil, err
	}
	if err := validateScenarioProjectRewriteTx(tx, scenario.ID, groups); err != nil {
		return nil, err
	}
	if admission != nil {
		if err := admitScenarioSourcesTx(tx, admission, pools); err != nil {
			return nil, err
		}
	}
	if err := updateScenarioAuthoredRowTx(tx, scenario, assignment, goals, runtime, traffic); err != nil {
		return nil, err
	}
	if err := replaceScenarioScriptsTx(tx, scenario.ID, groups); err != nil {
		return nil, err
	}
	if err := validateScenarioAuthoringPoolsActiveTx(tx, pools); err != nil {
		return nil, err
	}
	if err := saveScenarioDataPoolBindingsTx(tx, scenario.ID, pools); err != nil {
		return nil, err
	}
	snapshot, err := readScenarioAuthoringSnapshotTx(tx, scenario.ID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrScenarioAuthoringOutcomeUnknown, err)
	}
	return &ScenarioAuthoringMutationResult{Snapshot: snapshot, Applied: true}, nil
}
func updateScenarioAuthoredRowTx(tx *storeTx, scenario *Scenario, assignment, goals, runtime, traffic []byte) error {
	_, err := tx.Exec(`UPDATE scenarios SET ref_key=?,name=?,workload_shape_id=?,duration=?,start_delay=?,scenario_mode=?,termination_mode=?,shakeout_enabled=?,resource_baseline_enabled=?,assignment_json=?,goal_config_json=?,runtime_settings_json=?,traffic_profile_json=?,updated_at=? WHERE id=?`, scenario.RefKey, scenario.Name, scenario.WorkloadShapeID, scenario.Duration, scenario.StartDelay, scenario.ScenarioMode, scenario.TerminationMode, scenario.ScriptShakeout, scenario.ResourceBaseline, string(assignment), string(goals), string(runtime), string(traffic), scenario.UpdatedAt, scenario.ID)
	return err
}

// Pool lifecycle is admitted under the same project-first transaction. Sorted
// tuple locks prevent a pool archive from racing a successful authoring save.
func validateScenarioAuthoringPoolsActiveTx(tx *storeTx, pools []ScenarioDataPoolBinding) error {
	ids := map[string]bool{}
	projectIDs := map[string]bool{}
	for _, binding := range pools {
		if binding.Scope == "project" {
			if !liveBindingHasNoSeedPool(binding) {
				return errors.New("invalid project live table binding")
			}
			projectIDs[binding.ProjectTableID] = true
			continue
		}
		if binding.PoolID == "" && liveBindingHasNoSeedPool(binding) {
			continue
		}
		ids[binding.PoolID] = true
	}
	projectOrder := make([]string, 0, len(projectIDs))
	for id := range projectIDs {
		projectOrder = append(projectOrder, id)
	}
	sort.Strings(projectOrder)
	for _, id := range projectOrder {
		result, err := tx.Exec(`UPDATE project_live_tables SET updated_at=updated_at WHERE id=? AND state='active'`, id)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return errors.New("scenario project live table is unavailable or not active")
		}
	}
	ordered := make([]string, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	for _, id := range ordered {
		result, err := tx.Exec(`UPDATE shared_data_pools SET updated_at=updated_at WHERE id=? AND archived_at IS NULL`, id)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return errors.New("scenario data pool is unavailable or archived")
		}
	}
	return nil
}
