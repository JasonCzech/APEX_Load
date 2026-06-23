package storage

// SQLite store — scenarios. Split from sqlite.go (same package, behavior-preserving).

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
)

func (s *SQLiteStore) SaveScenario(scenario *Scenario, scripts []ScenarioScript) error {
	if scenario.ID == "" {
		return errors.New("scenario id is required")
	}
	if strings.TrimSpace(scenario.Name) == "" {
		return errors.New("scenario name is required")
	}
	if strings.TrimSpace(scenario.Duration) == "" {
		return errors.New("scenario duration is required")
	}
	if scenario.CreatedAt.IsZero() {
		scenario.CreatedAt = time.Now()
	}
	scenario.UpdatedAt = time.Now()
	assignmentJSON, _ := json.Marshal(scenario.Assignment)
	goalJSON, _ := json.Marshal(scenario.GoalConfig)
	runtimeJSON, _ := json.Marshal(scenario.RuntimeSettings)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO scenarios (id, ref_key, name, duration, start_delay, scenario_mode, shakeout_enabled, resource_baseline_enabled, assignment_json, goal_config_json, runtime_settings_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET ref_key=excluded.ref_key, name=excluded.name, duration=excluded.duration, start_delay=excluded.start_delay, scenario_mode=excluded.scenario_mode,
		shakeout_enabled=excluded.shakeout_enabled, resource_baseline_enabled=excluded.resource_baseline_enabled,
		assignment_json=excluded.assignment_json, goal_config_json=excluded.goal_config_json, runtime_settings_json=excluded.runtime_settings_json, updated_at=excluded.updated_at`,
		scenario.ID, scenario.RefKey, scenario.Name, scenario.Duration, scenario.StartDelay, scenario.ScenarioMode, scenario.ScriptShakeout, scenario.ResourceBaseline, string(assignmentJSON), string(goalJSON), string(runtimeJSON), scenario.CreatedAt, scenario.UpdatedAt); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM scenario_scripts WHERE scenario_id = ?`, scenario.ID); err != nil {
		return err
	}
	for i, script := range scripts {
		if script.Order == 0 && i > 0 {
			script.Order = i
		}
		if script.ScenarioID == "" {
			script.ScenarioID = scenario.ID
		}
		stagesJSON, _ := json.Marshal(script.Stages)
		runtimeJSON, _ := json.Marshal(script.RuntimeSettings)
		assignmentJSON, _ := json.Marshal(script.Assignment)
		if _, err := tx.Exec(`INSERT INTO scenario_scripts (scenario_id, sort_order, name, script_id, vusers, ramp_up, ramp_down, stages_json, initialization, role, runtime_settings_json, assignment_json)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, script.ScenarioID, script.Order, script.Name, script.ScriptID, script.VUsers, script.RampUp, script.RampDown, string(stagesJSON), script.Initialization, script.Role, string(runtimeJSON), string(assignmentJSON)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLiteStore) GetScenario(id string) (*Scenario, []ScenarioScript, error) {
	var scenario Scenario
	var assignmentJSON, goalJSON, runtimeJSON string
	err := s.db.QueryRow(`SELECT id, COALESCE(ref_key, ''), name, duration, COALESCE(start_delay, ''), COALESCE(scenario_mode, ''), COALESCE(shakeout_enabled, 0), COALESCE(resource_baseline_enabled, 0), COALESCE(assignment_json, ''), COALESCE(goal_config_json, ''), COALESCE(runtime_settings_json, ''), created_at, updated_at FROM scenarios WHERE id = ?`, id).Scan(
		&scenario.ID, &scenario.RefKey, &scenario.Name, &scenario.Duration, &scenario.StartDelay, &scenario.ScenarioMode, &scenario.ScriptShakeout, &scenario.ResourceBaseline, &assignmentJSON, &goalJSON, &runtimeJSON, &scenario.CreatedAt, &scenario.UpdatedAt)
	if err != nil {
		return nil, nil, err
	}
	if assignmentJSON != "" && assignmentJSON != "null" {
		json.Unmarshal([]byte(assignmentJSON), &scenario.Assignment)
	}
	if goalJSON != "" && goalJSON != "null" {
		json.Unmarshal([]byte(goalJSON), &scenario.GoalConfig)
	}
	if runtimeJSON != "" && runtimeJSON != "null" {
		json.Unmarshal([]byte(runtimeJSON), &scenario.RuntimeSettings)
	}
	scripts, err := s.listScenarioScripts(id)
	if err != nil {
		return nil, nil, err
	}
	return &scenario, scripts, nil
}

func (s *SQLiteStore) ListScenarios() ([]*Scenario, error) {
	rows, err := s.db.Query(`SELECT id, COALESCE(ref_key, ''), name, duration, COALESCE(start_delay, ''), COALESCE(scenario_mode, ''), COALESCE(shakeout_enabled, 0), COALESCE(resource_baseline_enabled, 0), COALESCE(assignment_json, ''), COALESCE(goal_config_json, ''), COALESCE(runtime_settings_json, ''), created_at, updated_at FROM scenarios ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var scenarios []*Scenario
	for rows.Next() {
		var scenario Scenario
		var assignmentJSON, goalJSON, runtimeJSON string
		if err := rows.Scan(&scenario.ID, &scenario.RefKey, &scenario.Name, &scenario.Duration, &scenario.StartDelay, &scenario.ScenarioMode, &scenario.ScriptShakeout, &scenario.ResourceBaseline, &assignmentJSON, &goalJSON, &runtimeJSON, &scenario.CreatedAt, &scenario.UpdatedAt); err != nil {
			return nil, err
		}
		if assignmentJSON != "" && assignmentJSON != "null" {
			json.Unmarshal([]byte(assignmentJSON), &scenario.Assignment)
		}
		if goalJSON != "" && goalJSON != "null" {
			json.Unmarshal([]byte(goalJSON), &scenario.GoalConfig)
		}
		if runtimeJSON != "" && runtimeJSON != "null" {
			json.Unmarshal([]byte(runtimeJSON), &scenario.RuntimeSettings)
		}
		scenarios = append(scenarios, &scenario)
	}
	return scenarios, rows.Err()
}

func (s *SQLiteStore) listScenarioScripts(scenarioID string) ([]ScenarioScript, error) {
	rows, err := s.db.Query(`SELECT scenario_id, sort_order, COALESCE(name, ''), script_id, COALESCE(vusers, 0), COALESCE(ramp_up, ''), COALESCE(ramp_down, ''), COALESCE(stages_json, ''), COALESCE(initialization, ''), COALESCE(role, ''), COALESCE(runtime_settings_json, ''), COALESCE(assignment_json, '') FROM scenario_scripts WHERE scenario_id = ? ORDER BY sort_order ASC`, scenarioID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var scripts []ScenarioScript
	for rows.Next() {
		var script ScenarioScript
		var stagesJSON, runtimeSettingsJSON, assignmentJSON string
		if err := rows.Scan(&script.ScenarioID, &script.Order, &script.Name, &script.ScriptID, &script.VUsers, &script.RampUp, &script.RampDown, &stagesJSON, &script.Initialization, &script.Role, &runtimeSettingsJSON, &assignmentJSON); err != nil {
			return nil, err
		}
		if strings.TrimSpace(stagesJSON) != "" {
			json.Unmarshal([]byte(stagesJSON), &script.Stages)
		}
		if strings.TrimSpace(runtimeSettingsJSON) != "" && runtimeSettingsJSON != "null" {
			json.Unmarshal([]byte(runtimeSettingsJSON), &script.RuntimeSettings)
		}
		if strings.TrimSpace(assignmentJSON) != "" && assignmentJSON != "null" {
			json.Unmarshal([]byte(assignmentJSON), &script.Assignment)
		}
		scripts = append(scripts, script)
	}
	return scripts, rows.Err()
}

func (s *SQLiteStore) DeleteScenario(id string) error {
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM tests WHERE scenario_id = ?`, id).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return errors.New("scenario has test results")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM scenario_scripts WHERE scenario_id = ?`, id); err != nil {
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

func (s *SQLiteStore) SaveScenarioDataPoolBindings(scenarioID string, bindings []ScenarioDataPoolBinding) error {
	if scenarioID == "" {
		return errors.New("scenario id is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
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
	return tx.Commit()
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
	_, err := s.db.Exec(`DELETE FROM scenario_data_pool_bindings WHERE id = ?`, id)
	return err
}
