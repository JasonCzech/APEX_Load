package storage

// SQLite store — scenarios. Split from sqlite.go (same package, behavior-preserving).

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

func (s *SQLiteStore) SaveScenario(scenario *Scenario, scripts []ScenarioScript) error {
	assignmentJSON, goalJSON, runtimeJSON, trafficJSON, err := prepareScenarioForSave(scenario)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := saveScenarioTx(tx, scenario, scripts, assignmentJSON, goalJSON, runtimeJSON, trafficJSON); err != nil {
		return err
	}
	return tx.Commit()
}

func prepareScenarioForSave(scenario *Scenario) ([]byte, []byte, []byte, []byte, error) {
	if scenario == nil {
		return nil, nil, nil, nil, errors.New("scenario is required")
	}
	if scenario.ID == "" {
		return nil, nil, nil, nil, errors.New("scenario id is required")
	}
	if strings.TrimSpace(scenario.Name) == "" {
		return nil, nil, nil, nil, errors.New("scenario name is required")
	}
	if scenario.TerminationMode != "iterations" && strings.TrimSpace(scenario.Duration) == "" {
		return nil, nil, nil, nil, errors.New("scenario duration is required")
	}
	if scenario.CreatedAt.IsZero() {
		scenario.CreatedAt = time.Now().UTC()
	}
	scenario.UpdatedAt = time.Now().UTC()
	assignmentJSON, err := json.Marshal(scenario.Assignment)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	goalJSON, err := json.Marshal(scenario.GoalConfig)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	runtimeJSON, err := json.Marshal(scenario.RuntimeSettings)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	trafficJSON, err := json.Marshal(scenario.TrafficProfile)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return assignmentJSON, goalJSON, runtimeJSON, trafficJSON, nil
}

// projectIDForScenarioScriptsTx derives the scenario tenant from its scripts.
// Scenarios have no project_id column, so every durable writer must preserve
// this invariant rather than trusting an API-layer cache.
func projectIDForScenarioScriptsTx(tx *storeTx, scripts []ScenarioScript) (string, error) {
	projectID := ""
	ordered := append([]ScenarioScript(nil), scripts...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ScriptID < ordered[j].ScriptID })
	for _, scenarioScript := range ordered {
		scriptID := strings.TrimSpace(scenarioScript.ScriptID)
		if scriptID == "" {
			return "", errors.New("scenario script id is required")
		}
		if err := ensureObjectActiveTx(tx, "script", scriptID); err != nil {
			return "", err
		}
		var candidate string
		if err := tx.QueryRow(`SELECT COALESCE(project_id, '') FROM scripts WHERE id = ?`, scriptID).Scan(&candidate); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return "", fmt.Errorf("scenario script %q does not exist", scriptID)
			}
			return "", err
		}
		candidate = normalizeProjectID(candidate)
		if projectID == "" {
			projectID = candidate
			continue
		}
		if candidate != projectID {
			return "", errors.New("scenario scripts span multiple projects")
		}
	}
	return normalizeProjectID(projectID), nil
}

func storedScenarioProjectIDTx(tx *storeTx, scenarioID string) (string, bool, error) {
	var exists int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM scenarios WHERE id = ?`, scenarioID).Scan(&exists); err != nil {
		return "", false, err
	}
	if exists == 0 {
		return "", false, nil
	}
	rows, err := tx.Query(`SELECT COALESCE(s.project_id, '')
		FROM scenario_scripts ss
		JOIN scripts s ON s.id = ss.script_id
		WHERE ss.scenario_id = ?`, scenarioID)
	if err != nil {
		return "", false, err
	}
	defer rows.Close()
	projectID := ""
	for rows.Next() {
		var candidate string
		if err := rows.Scan(&candidate); err != nil {
			return "", false, err
		}
		candidate = normalizeProjectID(candidate)
		if projectID == "" {
			projectID = candidate
			continue
		}
		if projectID != candidate {
			return "", false, errors.New("stored scenario scripts span multiple projects")
		}
	}
	if err := rows.Err(); err != nil {
		return "", false, err
	}
	return normalizeProjectID(projectID), true, nil
}

func validateScenarioProjectRewriteTx(tx *storeTx, scenarioID string, scripts []ScenarioScript) error {
	requestedProjectID, err := projectIDForScenarioScriptsTx(tx, scripts)
	if err != nil {
		return err
	}
	currentProjectID, exists, err := storedScenarioProjectIDTx(tx, scenarioID)
	if err != nil {
		return err
	}
	if exists && currentProjectID != requestedProjectID {
		return fmt.Errorf("scenario project ownership cannot change from %q to %q", currentProjectID, requestedProjectID)
	}
	return nil
}

func validateScenarioScriptJSON(scenarioID string, scripts []ScenarioScript) error {
	for i, script := range scripts {
		if _, err := json.Marshal(script.Stages); err != nil {
			return fmt.Errorf("marshal scenario %s script %d stages_json: %w", scenarioID, i, err)
		}
		if _, err := json.Marshal(script.RuntimeSettings); err != nil {
			return fmt.Errorf("marshal scenario %s script %d runtime_settings_json: %w", scenarioID, i, err)
		}
		if _, err := json.Marshal(script.Assignment); err != nil {
			return fmt.Errorf("marshal scenario %s script %d assignment_json: %w", scenarioID, i, err)
		}
	}
	return nil
}

func saveScenarioTx(tx *storeTx, scenario *Scenario, scripts []ScenarioScript, assignmentJSON, goalJSON, runtimeJSON, trafficJSON []byte) error {
	if _, err := lockScenarioMutationProjectTx(tx, scenario.ID, scripts); err != nil {
		return err
	}
	if err := ensureObjectActiveTx(tx, "scenario", scenario.ID); err != nil {
		return err
	}
	if err := validateScenarioScriptJSON(scenario.ID, scripts); err != nil {
		return err
	}
	if err := validateScenarioProjectRewriteTx(tx, scenario.ID, scripts); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO scenarios (id, ref_key, name, workload_shape_id, duration, start_delay, scenario_mode, termination_mode, shakeout_enabled, resource_baseline_enabled, assignment_json, goal_config_json, runtime_settings_json, traffic_profile_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET ref_key=CASE WHEN TRIM(COALESCE(excluded.ref_key, '')) = '' THEN scenarios.ref_key ELSE excluded.ref_key END, name=excluded.name, duration=excluded.duration, start_delay=excluded.start_delay, scenario_mode=excluded.scenario_mode, termination_mode=excluded.termination_mode,
		shakeout_enabled=excluded.shakeout_enabled, resource_baseline_enabled=excluded.resource_baseline_enabled,
		workload_shape_id=excluded.workload_shape_id, assignment_json=excluded.assignment_json, goal_config_json=excluded.goal_config_json, runtime_settings_json=excluded.runtime_settings_json, traffic_profile_json=excluded.traffic_profile_json, updated_at=excluded.updated_at`,
		scenario.ID, scenario.RefKey, scenario.Name, scenario.WorkloadShapeID, scenario.Duration, scenario.StartDelay, scenario.ScenarioMode, scenario.TerminationMode, scenario.ScriptShakeout, scenario.ResourceBaseline, string(assignmentJSON), string(goalJSON), string(runtimeJSON), string(trafficJSON), scenario.CreatedAt, scenario.UpdatedAt); err != nil {
		return err
	}
	return replaceScenarioScriptsTx(tx, scenario.ID, scripts)
}

func replaceScenarioScriptsTx(tx *storeTx, scenarioID string, scripts []ScenarioScript) error {
	if _, err := tx.Exec(`DELETE FROM scenario_scripts WHERE scenario_id = ?`, scenarioID); err != nil {
		return err
	}
	for i, script := range scripts {
		if script.ScenarioID != "" && script.ScenarioID != scenarioID {
			return fmt.Errorf("scenario script belongs to %q, not %q", script.ScenarioID, scenarioID)
		}
		if script.Order == 0 && i > 0 {
			script.Order = i
		}
		script.ScenarioID = scenarioID
		stagesJSON, err := json.Marshal(script.Stages)
		if err != nil {
			return fmt.Errorf("marshal scenario %s script %d stages_json: %w", scenarioID, i, err)
		}
		runtimeJSON, err := json.Marshal(script.RuntimeSettings)
		if err != nil {
			return fmt.Errorf("marshal scenario %s script %d runtime_settings_json: %w", scenarioID, i, err)
		}
		assignmentJSON, err := json.Marshal(script.Assignment)
		if err != nil {
			return fmt.Errorf("marshal scenario %s script %d assignment_json: %w", scenarioID, i, err)
		}
		if _, err := tx.Exec(`INSERT INTO scenario_scripts (scenario_id, sort_order, name, script_id, vusers, ramp_up, ramp_down, start_offset, stages_json, iterations, initialization, role, runtime_settings_json, assignment_json, transaction_response_time_sla_ms)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, script.ScenarioID, script.Order, script.Name, script.ScriptID, script.VUsers, script.RampUp, script.RampDown, script.StartOffset, string(stagesJSON), script.Iterations, script.Initialization, script.Role, string(runtimeJSON), string(assignmentJSON), script.TransactionResponseTimeSLAMs); err != nil {
			return err
		}
	}
	return nil
}

// CreateScenarioWithDataPoolBindingsIfAbsent atomically creates every durable
// component of a scenario and never overwrites an existing deterministic ID.
func (s *SQLiteStore) CreateScenarioWithDataPoolBindingsIfAbsent(scenario *Scenario, scripts []ScenarioScript, bindings []ScenarioDataPoolBinding) (bool, error) {
	assignmentJSON, goalJSON, runtimeJSON, trafficJSON, err := prepareScenarioForSave(scenario)
	if err != nil {
		return false, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err := lockScenarioCreateProjectTx(tx, scenario.ID, scripts); err != nil {
		return false, err
	}
	applied, err := createScenarioWithBindingsTx(tx, scenario, scripts, bindings, assignmentJSON, goalJSON, runtimeJSON, trafficJSON)
	if err != nil || !applied {
		return applied, err
	}
	return true, tx.Commit()
}

func createScenarioWithBindingsTx(tx *storeTx, scenario *Scenario, scripts []ScenarioScript, bindings []ScenarioDataPoolBinding, assignmentJSON, goalJSON, runtimeJSON, trafficJSON []byte) (bool, error) {
	if err := validateScenarioScriptJSON(scenario.ID, scripts); err != nil {
		return false, err
	}
	if _, err := projectIDForScenarioScriptsTx(tx, scripts); err != nil {
		return false, err
	}
	result, err := tx.Exec(`INSERT INTO scenarios (id, ref_key, name, workload_shape_id, duration, start_delay, scenario_mode, termination_mode, shakeout_enabled, resource_baseline_enabled, assignment_json, goal_config_json, runtime_settings_json, traffic_profile_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING`,
		scenario.ID, scenario.RefKey, scenario.Name, scenario.WorkloadShapeID, scenario.Duration, scenario.StartDelay, scenario.ScenarioMode, scenario.TerminationMode, scenario.ScriptShakeout, scenario.ResourceBaseline, string(assignmentJSON), string(goalJSON), string(runtimeJSON), string(trafficJSON), scenario.CreatedAt, scenario.UpdatedAt)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return false, err
	}
	if err := replaceScenarioScriptsTx(tx, scenario.ID, scripts); err != nil {
		return false, err
	}
	if err := saveScenarioDataPoolBindingsTx(tx, scenario.ID, bindings); err != nil {
		return false, err
	}
	return true, nil
}

// UpdateScenarioWithDataPoolBindingsIfUnchanged replaces the scenario and all
// child rows only when updated_at still matches the caller's durable snapshot.
func (s *SQLiteStore) UpdateScenarioWithDataPoolBindingsIfUnchanged(scenario *Scenario, scripts []ScenarioScript, bindings []ScenarioDataPoolBinding, expectedUpdatedAt time.Time) (bool, error) {
	if expectedUpdatedAt.IsZero() {
		return false, errors.New("expected scenario updated_at is required")
	}
	assignmentJSON, goalJSON, runtimeJSON, trafficJSON, err := prepareScenarioForSave(scenario)
	if err != nil {
		return false, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err := lockScenarioMutationProjectTx(tx, scenario.ID, scripts); err != nil {
		return false, err
	}
	if err := validateScenarioScriptJSON(scenario.ID, scripts); err != nil {
		return false, err
	}
	if err := validateScenarioProjectRewriteTx(tx, scenario.ID, scripts); err != nil {
		return false, err
	}
	if err := ensureObjectActiveTx(tx, "scenario", scenario.ID); err != nil {
		return false, err
	}
	var currentUpdatedAt time.Time
	var currentUpdatedRaw string
	if err := tx.QueryRow(`SELECT updated_at, CAST(updated_at AS TEXT) FROM scenarios WHERE id = ?`, scenario.ID).Scan(&currentUpdatedAt, &currentUpdatedRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if !currentUpdatedAt.Equal(expectedUpdatedAt) {
		return false, nil
	}
	result, err := tx.Exec(`UPDATE scenarios SET ref_key = ?, name = ?, workload_shape_id = ?, duration = ?, start_delay = ?, scenario_mode = ?, termination_mode = ?, shakeout_enabled = ?, resource_baseline_enabled = ?, assignment_json = ?, goal_config_json = ?, runtime_settings_json = ?, traffic_profile_json = ?, updated_at = ?
		WHERE id = ? AND CAST(updated_at AS TEXT) = ?`,
		scenario.RefKey, scenario.Name, scenario.WorkloadShapeID, scenario.Duration, scenario.StartDelay, scenario.ScenarioMode, scenario.TerminationMode, scenario.ScriptShakeout, scenario.ResourceBaseline, string(assignmentJSON), string(goalJSON), string(runtimeJSON), string(trafficJSON), scenario.UpdatedAt, scenario.ID, currentUpdatedRaw)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return false, err
	}
	if err := replaceScenarioScriptsTx(tx, scenario.ID, scripts); err != nil {
		return false, err
	}
	if err := saveScenarioDataPoolBindingsTx(tx, scenario.ID, bindings); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// SaveScenarioWithDataPoolBindings persists the scenario, script ordering, and
// data-pool bindings in one transaction. This prevents a binding failure from
// leaving a partially updated scenario visible after an error response.
func (s *SQLiteStore) SaveScenarioWithDataPoolBindings(scenario *Scenario, scripts []ScenarioScript, bindings []ScenarioDataPoolBinding) error {
	assignmentJSON, goalJSON, runtimeJSON, trafficJSON, err := prepareScenarioForSave(scenario)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := saveScenarioTx(tx, scenario, scripts, assignmentJSON, goalJSON, runtimeJSON, trafficJSON); err != nil {
		return err
	}
	if err := saveScenarioDataPoolBindingsTx(tx, scenario.ID, bindings); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) GetScenario(id string) (*Scenario, []ScenarioScript, error) {
	var scenario Scenario
	var assignmentJSON, goalJSON, runtimeJSON, trafficJSON string
	err := s.db.QueryRow(`SELECT id, COALESCE(ref_key, ''), name, COALESCE(workload_shape_id, ''), duration, COALESCE(start_delay, ''), COALESCE(scenario_mode, ''), COALESCE(termination_mode, 'duration'), COALESCE(shakeout_enabled, 0), COALESCE(resource_baseline_enabled, 0), COALESCE(assignment_json, ''), COALESCE(goal_config_json, ''), COALESCE(runtime_settings_json, ''), COALESCE(traffic_profile_json, ''), created_at, updated_at, (SELECT archived_at FROM object_archive_states a WHERE a.resource_type='scenario' AND a.resource_id=scenarios.id AND `+archiveIncarnationMatch(s.db.dialect, "a.incarnation", "scenarios.created_at")+`) FROM scenarios WHERE id = ?`, id).Scan(
		&scenario.ID, &scenario.RefKey, &scenario.Name, &scenario.WorkloadShapeID, &scenario.Duration, &scenario.StartDelay, &scenario.ScenarioMode, &scenario.TerminationMode, &scenario.ScriptShakeout, &scenario.ResourceBaseline, &assignmentJSON, &goalJSON, &runtimeJSON, &trafficJSON, &scenario.CreatedAt, &scenario.UpdatedAt, &scenario.ArchivedAt)
	if err != nil {
		return nil, nil, err
	}
	if err := decodeScenarioJSON(&scenario, assignmentJSON, goalJSON, runtimeJSON, trafficJSON); err != nil {
		return nil, nil, err
	}
	scripts, err := s.listScenarioScripts(id)
	if err != nil {
		return nil, nil, err
	}
	return &scenario, scripts, nil
}

func (s *SQLiteStore) ListScenarios() ([]*Scenario, error) {
	rows, err := s.db.Query(`SELECT id, COALESCE(ref_key, ''), name, COALESCE(workload_shape_id, ''), duration, COALESCE(start_delay, ''), COALESCE(scenario_mode, ''), COALESCE(termination_mode, 'duration'), COALESCE(shakeout_enabled, 0), COALESCE(resource_baseline_enabled, 0), COALESCE(assignment_json, ''), COALESCE(goal_config_json, ''), COALESCE(runtime_settings_json, ''), COALESCE(traffic_profile_json, ''), created_at, updated_at, (SELECT archived_at FROM object_archive_states a WHERE a.resource_type='scenario' AND a.resource_id=scenarios.id AND ` + archiveIncarnationMatch(s.db.dialect, "a.incarnation", "scenarios.created_at") + `) FROM scenarios ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var scenarios []*Scenario
	for rows.Next() {
		var scenario Scenario
		var assignmentJSON, goalJSON, runtimeJSON, trafficJSON string
		if err := rows.Scan(&scenario.ID, &scenario.RefKey, &scenario.Name, &scenario.WorkloadShapeID, &scenario.Duration, &scenario.StartDelay, &scenario.ScenarioMode, &scenario.TerminationMode, &scenario.ScriptShakeout, &scenario.ResourceBaseline, &assignmentJSON, &goalJSON, &runtimeJSON, &trafficJSON, &scenario.CreatedAt, &scenario.UpdatedAt, &scenario.ArchivedAt); err != nil {
			return nil, err
		}
		if err := decodeScenarioJSON(&scenario, assignmentJSON, goalJSON, runtimeJSON, trafficJSON); err != nil {
			return nil, err
		}
		scenarios = append(scenarios, &scenario)
	}
	return scenarios, rows.Err()
}

func decodeScenarioJSON(scenario *Scenario, assignmentJSON, goalJSON, runtimeJSON, trafficJSON string) error {
	fields := []struct {
		name string
		raw  string
		dst  any
	}{
		{name: "assignment_json", raw: assignmentJSON, dst: &scenario.Assignment},
		{name: "goal_config_json", raw: goalJSON, dst: &scenario.GoalConfig},
		{name: "runtime_settings_json", raw: runtimeJSON, dst: &scenario.RuntimeSettings},
		{name: "traffic_profile_json", raw: trafficJSON, dst: &scenario.TrafficProfile},
	}
	for _, field := range fields {
		if strings.TrimSpace(field.raw) == "" || field.raw == "null" {
			continue
		}
		if err := json.Unmarshal([]byte(field.raw), field.dst); err != nil {
			return fmt.Errorf("decode scenario %s %s: %w", scenario.ID, field.name, err)
		}
	}
	return nil
}

func (s *SQLiteStore) listScenarioScripts(scenarioID string) ([]ScenarioScript, error) {
	return listScenarioScriptsReader(s.db, scenarioID)
}

func listScenarioScriptsReader(reader libraryAssignmentReader, scenarioID string) ([]ScenarioScript, error) {
	rows, err := reader.Query(`SELECT scenario_id, sort_order, COALESCE(name, ''), script_id, COALESCE(vusers, 0), COALESCE(ramp_up, ''), COALESCE(ramp_down, ''), COALESCE(start_offset, ''), COALESCE(stages_json, ''), iterations, COALESCE(initialization, ''), COALESCE(role, ''), COALESCE(runtime_settings_json, ''), COALESCE(assignment_json, ''), transaction_response_time_sla_ms FROM scenario_scripts WHERE scenario_id = ? ORDER BY sort_order ASC`, scenarioID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var scripts []ScenarioScript
	for rows.Next() {
		var script ScenarioScript
		var iterations sql.NullInt64
		var stagesJSON, runtimeSettingsJSON, assignmentJSON string
		if err := rows.Scan(&script.ScenarioID, &script.Order, &script.Name, &script.ScriptID, &script.VUsers, &script.RampUp, &script.RampDown, &script.StartOffset, &stagesJSON, &iterations, &script.Initialization, &script.Role, &runtimeSettingsJSON, &assignmentJSON, &script.TransactionResponseTimeSLAMs); err != nil {
			return nil, err
		}
		if iterations.Valid {
			value := int(iterations.Int64)
			script.Iterations = &value
		}
		if strings.TrimSpace(stagesJSON) != "" {
			if err := json.Unmarshal([]byte(stagesJSON), &script.Stages); err != nil {
				return nil, fmt.Errorf("decode scenario %s script %d stages_json: %w", scenarioID, script.Order, err)
			}
		}
		if strings.TrimSpace(runtimeSettingsJSON) != "" && runtimeSettingsJSON != "null" {
			if err := json.Unmarshal([]byte(runtimeSettingsJSON), &script.RuntimeSettings); err != nil {
				return nil, fmt.Errorf("decode scenario %s script %d runtime_settings_json: %w", scenarioID, script.Order, err)
			}
		}
		if strings.TrimSpace(assignmentJSON) != "" && assignmentJSON != "null" {
			if err := json.Unmarshal([]byte(assignmentJSON), &script.Assignment); err != nil {
				return nil, fmt.Errorf("decode scenario %s script %d assignment_json: %w", scenarioID, script.Order, err)
			}
		}
		scripts = append(scripts, script)
	}
	return scripts, rows.Err()
}

func (s *SQLiteStore) DeleteScenario(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockScenarioExistingProjectTx(tx, id); err != nil {
		return err
	}
	locked, err := tx.Exec(`UPDATE scenarios SET updated_at = updated_at WHERE id = ?`, id)
	if err != nil {
		return err
	}
	affected, err := locked.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return nil
	}
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM tests WHERE scenario_id = ?`, id).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return errors.New("scenario has test results")
	}
	if _, err := tx.Exec(`DELETE FROM scenario_scripts WHERE scenario_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM scenario_schedule_approvals WHERE schedule_id IN (SELECT id FROM scenario_schedules WHERE scenario_id = ?)`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM scenario_schedule_occurrences WHERE schedule_id IN (SELECT id FROM scenario_schedules WHERE scenario_id = ?)`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM scenario_schedules WHERE scenario_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM scenario_data_pool_bindings WHERE scenario_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM scenarios WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteScenarioIfUnchanged removes a newly imported scenario only if no later
// edit or dependent schedule/test has claimed it. It is used by bundle rollback
// and deliberately refuses to cascade independently-created state.
func (s *SQLiteStore) DeleteScenarioIfUnchanged(id string, expectedUpdatedAt time.Time) (bool, error) {
	if strings.TrimSpace(id) == "" || expectedUpdatedAt.IsZero() {
		return false, errors.New("scenario id and expected updated_at are required")
	}
	var currentUpdatedAt time.Time
	var currentUpdatedRaw string
	if err := s.db.QueryRow(`SELECT updated_at, CAST(updated_at AS TEXT) FROM scenarios WHERE id = ?`, id).Scan(&currentUpdatedAt, &currentUpdatedRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if !currentUpdatedAt.Equal(expectedUpdatedAt) {
		return false, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if err := lockScenarioExistingProjectTx(tx, id); err != nil {
		return false, err
	}
	locked, err := tx.Exec(`UPDATE scenarios SET updated_at = updated_at WHERE id = ? AND CAST(updated_at AS TEXT) = ?`, id, currentUpdatedRaw)
	if err != nil {
		return false, err
	}
	rows, err := locked.RowsAffected()
	if err != nil || rows != 1 {
		return false, err
	}
	for _, query := range []string{
		`SELECT COUNT(*) FROM tests WHERE scenario_id = ?`,
		`SELECT COUNT(*) FROM scenario_schedules WHERE scenario_id = ?`,
	} {
		var count int
		if err := tx.QueryRow(query, id).Scan(&count); err != nil {
			return false, err
		}
		if count != 0 {
			return false, nil
		}
	}
	if _, err := tx.Exec(`DELETE FROM scenario_scripts WHERE scenario_id = ?`, id); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`DELETE FROM scenario_data_pool_bindings WHERE scenario_id = ?`, id); err != nil {
		return false, err
	}
	deleted, err := tx.Exec(`DELETE FROM scenarios WHERE id = ? AND CAST(updated_at AS TEXT) = ?`, id, currentUpdatedRaw)
	if err != nil {
		return false, err
	}
	rows, err = deleted.RowsAffected()
	if err != nil || rows != 1 {
		return false, err
	}
	return true, tx.Commit()
}

func (s *SQLiteStore) SaveScenarioDataPoolBindings(scenarioID string, bindings []ScenarioDataPoolBinding) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockScenarioExistingProjectTx(tx, scenarioID); err != nil {
		return err
	}
	if err := ensureObjectActiveTx(tx, "scenario", scenarioID); err != nil {
		return err
	}
	if err := saveScenarioDataPoolBindingsTx(tx, scenarioID, bindings); err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateScenarioDataPoolBindingsIfUnchanged treats the bindings as part of the
// scenario aggregate. Updating them advances scenarios.updated_at and only wins
// when the caller still owns the durable snapshot it loaded.
func (s *SQLiteStore) UpdateScenarioDataPoolBindingsIfUnchanged(scenarioID string, bindings []ScenarioDataPoolBinding, expectedUpdatedAt time.Time) (time.Time, bool, error) {
	scenarioID = strings.TrimSpace(scenarioID)
	if scenarioID == "" {
		return time.Time{}, false, errors.New("scenario id is required")
	}
	if expectedUpdatedAt.IsZero() {
		return time.Time{}, false, errors.New("expected scenario updated_at is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return time.Time{}, false, err
	}
	defer tx.Rollback()
	if err := lockScenarioExistingProjectTx(tx, scenarioID); err != nil {
		return time.Time{}, false, err
	}
	var currentUpdatedAt time.Time
	var currentUpdatedRaw string
	if err := tx.QueryRow(`SELECT updated_at, CAST(updated_at AS TEXT) FROM scenarios WHERE id = ?`, scenarioID).Scan(&currentUpdatedAt, &currentUpdatedRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return time.Time{}, false, nil
		}
		return time.Time{}, false, err
	}
	if !currentUpdatedAt.Equal(expectedUpdatedAt) {
		return time.Time{}, false, nil
	}
	updatedAt, err := preciseDatabaseTime(tx, s.db.dialect)
	if err != nil {
		return time.Time{}, false, err
	}
	if !updatedAt.After(currentUpdatedAt) {
		updatedAt = currentUpdatedAt.Add(time.Microsecond)
	}
	if err := ensureObjectActiveTx(tx, "scenario", scenarioID); err != nil {
		return time.Time{}, false, err
	}
	result, err := tx.Exec(`UPDATE scenarios SET updated_at = ? WHERE id = ? AND CAST(updated_at AS TEXT) = ?`, updatedAt, scenarioID, currentUpdatedRaw)
	if err != nil {
		return time.Time{}, false, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return time.Time{}, false, err
	}
	if err := saveScenarioDataPoolBindingsTx(tx, scenarioID, bindings); err != nil {
		return time.Time{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return time.Time{}, false, err
	}
	return updatedAt, true, nil
}

func validateScenarioDataPoolProjectsTx(tx *storeTx, scenarioID string, bindings []ScenarioDataPoolBinding) error {
	scenarioProjectID, exists, err := storedScenarioProjectIDTx(tx, scenarioID)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("scenario %q does not exist", scenarioID)
	}
	for _, binding := range bindings {
		poolID := strings.TrimSpace(binding.PoolID)
		if poolID == "" {
			return errors.New("data pool id is required")
		}
		var poolProjectID string
		if err := tx.QueryRow(`SELECT COALESCE(project_id, '') FROM shared_data_pools WHERE id = ?`, poolID).Scan(&poolProjectID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("data pool %q does not exist", poolID)
			}
			return err
		}
		if normalizeProjectID(poolProjectID) != scenarioProjectID {
			return fmt.Errorf("data pool %q belongs to a different project than scenario %q", poolID, scenarioID)
		}
	}
	return nil
}

func saveScenarioDataPoolBindingsTx(tx *storeTx, scenarioID string, bindings []ScenarioDataPoolBinding) error {
	if scenarioID == "" {
		return errors.New("scenario id is required")
	}
	if err := validateScenarioDataPoolProjectsTx(tx, scenarioID, bindings); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM scenario_data_pool_bindings WHERE scenario_id = ?`, scenarioID); err != nil {
		return err
	}
	now := time.Now()
	for _, binding := range bindings {
		if binding.ID == "" {
			return errors.New("scenario data pool binding id is required")
		}
		if binding.PoolID == "" {
			return errors.New("data pool id is required")
		}
		if strings.TrimSpace(binding.Alias) == "" {
			return errors.New("data pool binding alias is required")
		}
		if binding.AccessMode == "" {
			binding.AccessMode = "read_only"
		}
		if binding.SelectionStrategy == "" {
			binding.SelectionStrategy = "sequential"
		}
		if binding.CreatedAt.IsZero() {
			binding.CreatedAt = now
		}
		binding.UpdatedAt = now
		if _, err := tx.Exec(`INSERT INTO scenario_data_pool_bindings (id, scenario_id, pool_id, alias, access_mode, selection_strategy, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, binding.ID, scenarioID, binding.PoolID, binding.Alias, binding.AccessMode, binding.SelectionStrategy, binding.CreatedAt, binding.UpdatedAt); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) ListScenarioDataPoolBindings(scenarioID string) ([]ScenarioDataPoolBinding, error) {
	rows, err := s.db.Query(`SELECT b.id, b.scenario_id, b.pool_id, COALESCE(p.name, ''), b.alias, b.access_mode, b.selection_strategy, b.created_at, b.updated_at
		FROM scenario_data_pool_bindings b
		LEFT JOIN shared_data_pools p ON p.id = b.pool_id
		WHERE b.scenario_id = ?
		ORDER BY b.alias ASC`, scenarioID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ScenarioDataPoolBinding
	for rows.Next() {
		var binding ScenarioDataPoolBinding
		if err := rows.Scan(&binding.ID, &binding.ScenarioID, &binding.PoolID, &binding.PoolName, &binding.Alias, &binding.AccessMode, &binding.SelectionStrategy, &binding.CreatedAt, &binding.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, binding)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) DeleteScenarioDataPoolBinding(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockScenarioSQLiteWriterTx(tx, ""); err != nil {
		return err
	}
	var scenarioID string
	if err := tx.QueryRow(`SELECT scenario_id FROM scenario_data_pool_bindings WHERE id=?`, id).Scan(&scenarioID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	if err := lockScenarioExistingProjectTx(tx, scenarioID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE scenarios SET updated_at=updated_at WHERE id=?`, scenarioID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM scenario_data_pool_bindings WHERE id=? AND scenario_id=?`, id, scenarioID); err != nil {
		return err
	}
	return tx.Commit()
}
