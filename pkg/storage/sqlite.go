package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jasonczech/stormrunner/pkg/dsl"
	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	db *sql.DB
}

func NewSQLiteStore(path string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite db: %w", err)
	}

	s := &SQLiteStore{db: db}
	if err := s.init(); err != nil {
		return nil, err
	}

	return s, nil
}

func (s *SQLiteStore) init() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS scripts (
			id TEXT PRIMARY KEY,
			name TEXT,
			protocol TEXT,
			version TEXT,
			generated_by TEXT,
			generated_from TEXT,
			config TEXT,
			data_sources TEXT,
			init TEXT,
			actions TEXT,
			teardown TEXT,
			created_at DATETIME,
			updated_at DATETIME
		)`,
		`CREATE TABLE IF NOT EXISTS tests (
			id TEXT PRIMARY KEY,
			name TEXT,
			status TEXT,
			scenario_mode TEXT,
			shakeout_enabled INTEGER DEFAULT 0,
			resource_baseline_enabled INTEGER DEFAULT 0,
			total_vusers INTEGER,
			duration_ns INTEGER,
			start_time DATETIME,
			end_time DATETIME,
			error TEXT,
			groups_json TEXT,
			goal_config_json TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS test_history (
			test_id TEXT,
			timestamp DATETIME,
			metrics_json TEXT,
			FOREIGN KEY(test_id) REFERENCES tests(id)
		)`,
		`CREATE TABLE IF NOT EXISTS run_archives (
			test_id TEXT PRIMARY KEY,
			schema_version TEXT,
			archive_path TEXT,
			manifest_path TEXT,
			transaction_count INTEGER,
			resource_sample_count INTEGER,
			interval_summary_count INTEGER,
			created_at DATETIME,
			completed_at DATETIME,
			FOREIGN KEY(test_id) REFERENCES tests(id)
		)`,
	}

	for _, q := range queries {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("failed to run migration query: %w", err)
		}
	}

	if _, err := s.db.Exec(`ALTER TABLE tests ADD COLUMN runtime_settings_json TEXT`); err != nil {
		if err.Error() != "SQL logic error: duplicate column name: runtime_settings_json (1)" {
			return fmt.Errorf("failed to add runtime_settings_json column: %w", err)
		}
	}

	if _, err := s.db.Exec(`ALTER TABLE tests ADD COLUMN scenario_mode TEXT`); err != nil {
		if err.Error() != "SQL logic error: duplicate column name: scenario_mode (1)" {
			return fmt.Errorf("failed to add scenario_mode column: %w", err)
		}
	}

	if _, err := s.db.Exec(`ALTER TABLE tests ADD COLUMN goal_config_json TEXT`); err != nil {
		if err.Error() != "SQL logic error: duplicate column name: goal_config_json (1)" {
			return fmt.Errorf("failed to add goal_config_json column: %w", err)
		}
	}

	if _, err := s.db.Exec(`ALTER TABLE tests ADD COLUMN shakeout_enabled INTEGER DEFAULT 0`); err != nil {
		if err.Error() != "SQL logic error: duplicate column name: shakeout_enabled (1)" {
			return fmt.Errorf("failed to add shakeout_enabled column: %w", err)
		}
	}

	if _, err := s.db.Exec(`ALTER TABLE tests ADD COLUMN resource_baseline_enabled INTEGER DEFAULT 0`); err != nil {
		if err.Error() != "SQL logic error: duplicate column name: resource_baseline_enabled (1)" {
			return fmt.Errorf("failed to add resource_baseline_enabled column: %w", err)
		}
	}

	return nil
}

func (s *SQLiteStore) SaveScript(script *dsl.Script) error {
	configJSON, _ := json.Marshal(script.Config)
	dataSourcesJSON, _ := json.Marshal(script.DataSources)
	initJSON, _ := json.Marshal(script.Init)
	actionsJSON, _ := json.Marshal(script.Actions)
	teardownJSON, _ := json.Marshal(script.Teardown)

	if script.CreatedAt.IsZero() {
		script.CreatedAt = time.Now()
	}
	script.UpdatedAt = time.Now()

	query := `INSERT INTO scripts (id, name, protocol, version, generated_by, generated_from, config, data_sources, init, actions, teardown, created_at, updated_at)
			  VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			  ON CONFLICT(id) DO UPDATE SET
			  name=excluded.name, protocol=excluded.protocol, version=excluded.version, 
			  generated_by=excluded.generated_by, generated_from=excluded.generated_from,
			  config=excluded.config, data_sources=excluded.data_sources, 
			  init=excluded.init, actions=excluded.actions, teardown=excluded.teardown, 
			  updated_at=excluded.updated_at`

	_, err := s.db.Exec(query,
		script.ID, script.Name, script.Protocol, script.Version,
		script.GeneratedBy, script.GeneratedFrom,
		string(configJSON), string(dataSourcesJSON), string(initJSON), string(actionsJSON), string(teardownJSON),
		script.CreatedAt, script.UpdatedAt)
	return err
}

func (s *SQLiteStore) GetScript(id string) (*dsl.Script, error) {
	var script dsl.Script
	var configJSON, dataSourcesJSON, initJSON, actionsJSON, teardownJSON string

	query := `SELECT id, name, protocol, version, generated_by, generated_from, config, data_sources, init, actions, teardown, created_at, updated_at 
			  FROM scripts WHERE id = ?`
	err := s.db.QueryRow(query, id).Scan(
		&script.ID, &script.Name, &script.Protocol, &script.Version,
		&script.GeneratedBy, &script.GeneratedFrom,
		&configJSON, &dataSourcesJSON, &initJSON, &actionsJSON, &teardownJSON,
		&script.CreatedAt, &script.UpdatedAt)
	if err != nil {
		return nil, err
	}

	json.Unmarshal([]byte(configJSON), &script.Config)
	json.Unmarshal([]byte(dataSourcesJSON), &script.DataSources)
	json.Unmarshal([]byte(initJSON), &script.Init)
	json.Unmarshal([]byte(actionsJSON), &script.Actions)
	json.Unmarshal([]byte(teardownJSON), &script.Teardown)

	return &script, nil
}

func (s *SQLiteStore) ListScripts() ([]*dsl.Script, error) {
	query := `SELECT id, name, protocol, version, generated_by, generated_from, config, data_sources, init, actions, teardown, created_at, updated_at 
			  FROM scripts ORDER BY created_at DESC`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var scripts []*dsl.Script
	for rows.Next() {
		var script dsl.Script
		var configJSON, dataSourcesJSON, initJSON, actionsJSON, teardownJSON string
		err := rows.Scan(
			&script.ID, &script.Name, &script.Protocol, &script.Version,
			&script.GeneratedBy, &script.GeneratedFrom,
			&configJSON, &dataSourcesJSON, &initJSON, &actionsJSON, &teardownJSON,
			&script.CreatedAt, &script.UpdatedAt)
		if err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(configJSON), &script.Config)
		json.Unmarshal([]byte(dataSourcesJSON), &script.DataSources)
		json.Unmarshal([]byte(initJSON), &script.Init)
		json.Unmarshal([]byte(actionsJSON), &script.Actions)
		json.Unmarshal([]byte(teardownJSON), &script.Teardown)
		scripts = append(scripts, &script)
	}
	return scripts, nil
}

func (s *SQLiteStore) DeleteScript(id string) error {
	_, err := s.db.Exec(`DELETE FROM scripts WHERE id = ?`, id)
	return err
}

func (s *SQLiteStore) SaveTest(test *TestRecord) error {
	groupsJSON, _ := json.Marshal(test.Groups)
	runtimeSettingsJSON, _ := json.Marshal(test.RuntimeSettings)
	goalConfigJSON, _ := json.Marshal(test.GoalConfig)

	query := `INSERT INTO tests (id, name, status, scenario_mode, shakeout_enabled, resource_baseline_enabled, total_vusers, duration_ns, start_time, end_time, error, groups_json, runtime_settings_json, goal_config_json)
			  VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err := s.db.Exec(query, test.ID, test.Name, test.Status, test.ScenarioMode, test.ScriptShakeout, test.ResourceBaseline, test.TotalVUsers, test.Duration, test.StartTime, test.EndTime, test.Error, string(groupsJSON), string(runtimeSettingsJSON), string(goalConfigJSON))
	return err
}

func (s *SQLiteStore) GetTest(id string) (*TestRecord, error) {
	var test TestRecord
	var groupsJSON, runtimeSettingsJSON, goalConfigJSON string

	query := `SELECT id, name, status, COALESCE(scenario_mode, ''), COALESCE(shakeout_enabled, 0), COALESCE(resource_baseline_enabled, 0), total_vusers, duration_ns, start_time, end_time, error, groups_json, COALESCE(runtime_settings_json, ''), COALESCE(goal_config_json, '') FROM tests WHERE id = ?`
	err := s.db.QueryRow(query, id).Scan(&test.ID, &test.Name, &test.Status, &test.ScenarioMode, &test.ScriptShakeout, &test.ResourceBaseline, &test.TotalVUsers, &test.Duration, &test.StartTime, &test.EndTime, &test.Error, &groupsJSON, &runtimeSettingsJSON, &goalConfigJSON)
	if err != nil {
		return nil, err
	}

	json.Unmarshal([]byte(groupsJSON), &test.Groups)
	if runtimeSettingsJSON != "" && runtimeSettingsJSON != "null" {
		json.Unmarshal([]byte(runtimeSettingsJSON), &test.RuntimeSettings)
	}
	if goalConfigJSON != "" && goalConfigJSON != "null" {
		json.Unmarshal([]byte(goalConfigJSON), &test.GoalConfig)
	}
	return &test, nil
}

func (s *SQLiteStore) ListTests() ([]*TestRecord, error) {
	rows, err := s.db.Query(`SELECT id, name, status, COALESCE(scenario_mode, ''), COALESCE(shakeout_enabled, 0), COALESCE(resource_baseline_enabled, 0), total_vusers, duration_ns, start_time, end_time, error, groups_json, COALESCE(runtime_settings_json, ''), COALESCE(goal_config_json, '') FROM tests ORDER BY start_time DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tests []*TestRecord
	for rows.Next() {
		var test TestRecord
		var groupsJSON, runtimeSettingsJSON, goalConfigJSON string
		if err := rows.Scan(&test.ID, &test.Name, &test.Status, &test.ScenarioMode, &test.ScriptShakeout, &test.ResourceBaseline, &test.TotalVUsers, &test.Duration, &test.StartTime, &test.EndTime, &test.Error, &groupsJSON, &runtimeSettingsJSON, &goalConfigJSON); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(groupsJSON), &test.Groups)
		if runtimeSettingsJSON != "" && runtimeSettingsJSON != "null" {
			json.Unmarshal([]byte(runtimeSettingsJSON), &test.RuntimeSettings)
		}
		if goalConfigJSON != "" && goalConfigJSON != "null" {
			json.Unmarshal([]byte(goalConfigJSON), &test.GoalConfig)
		}
		tests = append(tests, &test)
	}
	return tests, nil
}

func (s *SQLiteStore) UpdateTestStatus(id string, status string, endTime *time.Time, errStr string) error {
	_, err := s.db.Exec(`UPDATE tests SET status = ?, end_time = ?, error = ? WHERE id = ?`, status, endTime, errStr, id)
	return err
}

func (s *SQLiteStore) DeleteTest(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM test_history WHERE test_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM run_archives WHERE test_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM tests WHERE id = ?`, id); err != nil {
		return err
	}

	return tx.Commit()
}

func (s *SQLiteStore) SaveHistory(testID string, timestamp time.Time, metrics any) error {
	metricsJSON, _ := json.Marshal(metrics)
	_, err := s.db.Exec(`INSERT INTO test_history (test_id, timestamp, metrics_json) VALUES (?, ?, ?)`, testID, timestamp, string(metricsJSON))
	return err
}

func (s *SQLiteStore) GetHistory(testID string) ([]any, error) {
	rows, err := s.db.Query(`SELECT metrics_json FROM test_history WHERE test_id = ? ORDER BY timestamp ASC`, testID)
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
		json.Unmarshal([]byte(metricsJSON), &metrics)
		history = append(history, metrics)
	}
	return history, nil
}

func (s *SQLiteStore) SaveRunArchive(record *RunArchiveRecord) error {
	_, err := s.db.Exec(`INSERT INTO run_archives (test_id, schema_version, archive_path, manifest_path, transaction_count, resource_sample_count, interval_summary_count, created_at, completed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(test_id) DO UPDATE SET
		schema_version=excluded.schema_version,
		archive_path=excluded.archive_path,
		manifest_path=excluded.manifest_path,
		transaction_count=excluded.transaction_count,
		resource_sample_count=excluded.resource_sample_count,
		interval_summary_count=excluded.interval_summary_count,
		created_at=excluded.created_at,
		completed_at=excluded.completed_at`,
		record.TestID,
		record.SchemaVersion,
		record.ArchivePath,
		record.ManifestPath,
		record.TransactionCount,
		record.ResourceSampleCount,
		record.IntervalSummaryCount,
		record.CreatedAt,
		record.CompletedAt,
	)
	return err
}

func (s *SQLiteStore) GetRunArchive(testID string) (*RunArchiveRecord, error) {
	var record RunArchiveRecord
	err := s.db.QueryRow(`SELECT test_id, schema_version, archive_path, manifest_path, transaction_count, resource_sample_count, interval_summary_count, created_at, completed_at FROM run_archives WHERE test_id = ?`, testID).Scan(
		&record.TestID,
		&record.SchemaVersion,
		&record.ArchivePath,
		&record.ManifestPath,
		&record.TransactionCount,
		&record.ResourceSampleCount,
		&record.IntervalSummaryCount,
		&record.CreatedAt,
		&record.CompletedAt,
	)
	if err != nil {
		return nil, err
	}
	return &record, nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}
