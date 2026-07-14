package storage

// SQLite store — scenarios. Split from sqlite.go (same package, behavior-preserving).

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *SQLiteStore) SaveScenario(scenario *Scenario, scripts []ScenarioScript) error {
	assignmentJSON, goalJSON, runtimeJSON, err := prepareScenarioForSave(scenario)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := saveScenarioTx(tx, scenario, scripts, assignmentJSON, goalJSON, runtimeJSON); err != nil {
		return err
	}
	return tx.Commit()
}

func prepareScenarioForSave(scenario *Scenario) ([]byte, []byte, []byte, error) {
	if scenario == nil {
		return nil, nil, nil, errors.New("scenario is required")
	}
	if scenario.ID == "" {
		return nil, nil, nil, errors.New("scenario id is required")
	}
	if strings.TrimSpace(scenario.Name) == "" {
		return nil, nil, nil, errors.New("scenario name is required")
	}
	if strings.TrimSpace(scenario.Duration) == "" {
		return nil, nil, nil, errors.New("scenario duration is required")
	}
	if scenario.CreatedAt.IsZero() {
		scenario.CreatedAt = time.Now()
	}
	scenario.UpdatedAt = time.Now()
	assignmentJSON, err := json.Marshal(scenario.Assignment)
	if err != nil {
		return nil, nil, nil, err
	}
	goalJSON, err := json.Marshal(scenario.GoalConfig)
	if err != nil {
		return nil, nil, nil, err
	}
	runtimeJSON, err := json.Marshal(scenario.RuntimeSettings)
	if err != nil {
		return nil, nil, nil, err
	}
	return assignmentJSON, goalJSON, runtimeJSON, nil
}

func saveScenarioTx(tx *storeTx, scenario *Scenario, scripts []ScenarioScript, assignmentJSON, goalJSON, runtimeJSON []byte) error {
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
		stagesJSON, err := json.Marshal(script.Stages)
		if err != nil {
			return fmt.Errorf("marshal scenario %s script %d stages_json: %w", scenario.ID, i, err)
		}
		runtimeJSON, err := json.Marshal(script.RuntimeSettings)
		if err != nil {
			return fmt.Errorf("marshal scenario %s script %d runtime_settings_json: %w", scenario.ID, i, err)
		}
		assignmentJSON, err := json.Marshal(script.Assignment)
		if err != nil {
			return fmt.Errorf("marshal scenario %s script %d assignment_json: %w", scenario.ID, i, err)
		}
		if _, err := tx.Exec(`INSERT INTO scenario_scripts (scenario_id, sort_order, name, script_id, vusers, ramp_up, ramp_down, stages_json, initialization, role, runtime_settings_json, assignment_json)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, script.ScenarioID, script.Order, script.Name, script.ScriptID, script.VUsers, script.RampUp, script.RampDown, string(stagesJSON), script.Initialization, script.Role, string(runtimeJSON), string(assignmentJSON)); err != nil {
			return err
		}
	}
	return nil
}

// SaveScenarioWithDataPoolBindings persists the scenario, script ordering, and
// data-pool bindings in one transaction. This prevents a binding failure from
// leaving a partially updated scenario visible after an error response.
func (s *SQLiteStore) SaveScenarioWithDataPoolBindings(scenario *Scenario, scripts []ScenarioScript, bindings []ScenarioDataPoolBinding) error {
	assignmentJSON, goalJSON, runtimeJSON, err := prepareScenarioForSave(scenario)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := saveScenarioTx(tx, scenario, scripts, assignmentJSON, goalJSON, runtimeJSON); err != nil {
		return err
	}
	if err := saveScenarioDataPoolBindingsTx(tx, scenario.ID, bindings); err != nil {
		return err
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
	if err := decodeScenarioJSON(&scenario, assignmentJSON, goalJSON, runtimeJSON); err != nil {
		return nil, nil, err
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
		if err := decodeScenarioJSON(&scenario, assignmentJSON, goalJSON, runtimeJSON); err != nil {
			return nil, err
		}
		scenarios = append(scenarios, &scenario)
	}
	return scenarios, rows.Err()
}

func decodeScenarioJSON(scenario *Scenario, assignmentJSON, goalJSON, runtimeJSON string) error {
	fields := []struct {
		name string
		raw  string
		dst  any
	}{
		{name: "assignment_json", raw: assignmentJSON, dst: &scenario.Assignment},
		{name: "goal_config_json", raw: goalJSON, dst: &scenario.GoalConfig},
		{name: "runtime_settings_json", raw: runtimeJSON, dst: &scenario.RuntimeSettings},
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

func (s *SQLiteStore) SaveScenarioDataPoolBindings(scenarioID string, bindings []ScenarioDataPoolBinding) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := saveScenarioDataPoolBindingsTx(tx, scenarioID, bindings); err != nil {
		return err
	}
	return tx.Commit()
}

func saveScenarioDataPoolBindingsTx(tx *storeTx, scenarioID string, bindings []ScenarioDataPoolBinding) error {
	if scenarioID == "" {
		return errors.New("scenario id is required")
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
	_, err := s.db.Exec(`DELETE FROM scenario_data_pool_bindings WHERE id = ?`, id)
	return err
}
