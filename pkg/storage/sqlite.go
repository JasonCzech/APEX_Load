package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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
		`CREATE TABLE IF NOT EXISTS projects (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS folders (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			parent_id TEXT,
			name TEXT NOT NULL,
			category TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY(project_id) REFERENCES projects(id),
			FOREIGN KEY(parent_id) REFERENCES folders(id)
		)`,
		`CREATE TABLE IF NOT EXISTS scripts (
			id TEXT PRIMARY KEY,
			name TEXT,
			protocol TEXT,
			version TEXT,
			project_id TEXT,
			folder_id TEXT,
			generated_by TEXT,
			generated_from TEXT,
			metadata_json TEXT,
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
			project_id TEXT,
			folder_id TEXT,
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
		`CREATE TABLE IF NOT EXISTS app_settings (
			key TEXT PRIMARY KEY,
			value TEXT,
			updated_at DATETIME
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

	if _, err := s.db.Exec(`ALTER TABLE scripts ADD COLUMN metadata_json TEXT`); err != nil {
		if err.Error() != "SQL logic error: duplicate column name: metadata_json (1)" {
			return fmt.Errorf("failed to add metadata_json column: %w", err)
		}
	}

	for _, migration := range []struct {
		query string
		name  string
	}{
		{`ALTER TABLE scripts ADD COLUMN project_id TEXT REFERENCES projects(id)`, "scripts.project_id"},
		{`ALTER TABLE scripts ADD COLUMN folder_id TEXT REFERENCES folders(id)`, "scripts.folder_id"},
		{`ALTER TABLE tests ADD COLUMN project_id TEXT REFERENCES projects(id)`, "tests.project_id"},
		{`ALTER TABLE tests ADD COLUMN folder_id TEXT REFERENCES folders(id)`, "tests.folder_id"},
	} {
		if _, err := s.db.Exec(migration.query); err != nil && !isDuplicateColumnErr(err) {
			return fmt.Errorf("failed to add %s column: %w", migration.name, err)
		}
	}

	return s.ensureDefaultProject()
}

func isDuplicateColumnErr(err error) bool {
	return strings.Contains(err.Error(), "duplicate column name")
}

func (s *SQLiteStore) ensureDefaultProject() error {
	now := time.Now()
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO projects (id, name, created_at, updated_at) VALUES (?, ?, ?, ?)`, DefaultProjectID, "Unassigned", now, now); err != nil {
		return fmt.Errorf("failed to create default project: %w", err)
	}
	if _, err := s.db.Exec(`UPDATE scripts SET project_id = ? WHERE project_id IS NULL OR project_id = ''`, DefaultProjectID); err != nil {
		return fmt.Errorf("failed to backfill script project ids: %w", err)
	}
	if _, err := s.db.Exec(`UPDATE tests SET project_id = ? WHERE project_id IS NULL OR project_id = ''`, DefaultProjectID); err != nil {
		return fmt.Errorf("failed to backfill test project ids: %w", err)
	}
	return nil
}

func normalizeProjectID(projectID string) string {
	if projectID == "" {
		return DefaultProjectID
	}
	return projectID
}

func validateFolderCategory(category string) error {
	switch category {
	case "scripts", "scenarios", "results", "recordings":
		return nil
	default:
		return fmt.Errorf("invalid folder category: %s", category)
	}
}

func (s *SQLiteStore) SaveProject(project *Project) error {
	if project.ID == "" {
		return errors.New("project id is required")
	}
	if strings.TrimSpace(project.Name) == "" {
		return errors.New("project name is required")
	}
	if project.CreatedAt.IsZero() {
		project.CreatedAt = time.Now()
	}
	project.UpdatedAt = time.Now()
	_, err := s.db.Exec(`INSERT INTO projects (id, name, created_at, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, updated_at=excluded.updated_at`,
		project.ID, project.Name, project.CreatedAt, project.UpdatedAt)
	return err
}

func (s *SQLiteStore) GetProject(id string) (*Project, error) {
	var project Project
	err := s.db.QueryRow(`SELECT id, name, created_at, updated_at FROM projects WHERE id = ?`, id).Scan(
		&project.ID, &project.Name, &project.CreatedAt, &project.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &project, nil
}

func (s *SQLiteStore) ListProjects() ([]*Project, error) {
	rows, err := s.db.Query(`SELECT id, name, created_at, updated_at FROM projects ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var projects []*Project
	for rows.Next() {
		var project Project
		if err := rows.Scan(&project.ID, &project.Name, &project.CreatedAt, &project.UpdatedAt); err != nil {
			return nil, err
		}
		projects = append(projects, &project)
	}
	return projects, rows.Err()
}

func (s *SQLiteStore) DeleteProject(id string) error {
	if id == DefaultProjectID {
		return errors.New("default project cannot be deleted")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT id FROM tests WHERE project_id = ?`, id)
	if err != nil {
		return err
	}
	var testIDs []string
	for rows.Next() {
		var testID string
		if err := rows.Scan(&testID); err != nil {
			rows.Close()
			return err
		}
		testIDs = append(testIDs, testID)
	}
	rows.Close()
	for _, testID := range testIDs {
		if _, err := tx.Exec(`DELETE FROM test_history WHERE test_id = ?`, testID); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM run_archives WHERE test_id = ?`, testID); err != nil {
			return err
		}
	}
	for _, q := range []string{
		`DELETE FROM tests WHERE project_id = ?`,
		`DELETE FROM scripts WHERE project_id = ?`,
		`DELETE FROM folders WHERE project_id = ?`,
		`DELETE FROM projects WHERE id = ?`,
	} {
		if _, err := tx.Exec(q, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLiteStore) SaveFolder(folder *Folder) error {
	if folder.ID == "" {
		return errors.New("folder id is required")
	}
	if strings.TrimSpace(folder.Name) == "" {
		return errors.New("folder name is required")
	}
	if err := validateFolderCategory(folder.Category); err != nil {
		return err
	}
	folder.ProjectID = normalizeProjectID(folder.ProjectID)
	if folder.CreatedAt.IsZero() {
		folder.CreatedAt = time.Now()
	}
	folder.UpdatedAt = time.Now()
	_, err := s.db.Exec(`INSERT INTO folders (id, project_id, parent_id, name, category, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET project_id=excluded.project_id, parent_id=excluded.parent_id, name=excluded.name, category=excluded.category, updated_at=excluded.updated_at`,
		folder.ID, folder.ProjectID, folder.ParentID, folder.Name, folder.Category, folder.CreatedAt, folder.UpdatedAt)
	return err
}

func (s *SQLiteStore) GetFolder(id string) (*Folder, error) {
	var folder Folder
	err := s.db.QueryRow(`SELECT id, project_id, parent_id, name, category, created_at, updated_at FROM folders WHERE id = ?`, id).Scan(
		&folder.ID, &folder.ProjectID, &folder.ParentID, &folder.Name, &folder.Category, &folder.CreatedAt, &folder.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &folder, nil
}

func (s *SQLiteStore) ListFolders(projectID string, category string) ([]*Folder, error) {
	projectID = normalizeProjectID(projectID)
	args := []any{projectID}
	query := `SELECT id, project_id, parent_id, name, category, created_at, updated_at FROM folders WHERE project_id = ?`
	if category != "" {
		if err := validateFolderCategory(category); err != nil {
			return nil, err
		}
		query += ` AND category = ?`
		args = append(args, category)
	}
	query += ` ORDER BY name ASC`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var folders []*Folder
	for rows.Next() {
		var folder Folder
		if err := rows.Scan(&folder.ID, &folder.ProjectID, &folder.ParentID, &folder.Name, &folder.Category, &folder.CreatedAt, &folder.UpdatedAt); err != nil {
			return nil, err
		}
		folders = append(folders, &folder)
	}
	return folders, rows.Err()
}

func (s *SQLiteStore) MoveFolder(id string, newParentID *string) error {
	if newParentID != nil {
		if *newParentID == id {
			return errors.New("folder cannot be moved into itself")
		}
		isDescendant, err := s.folderHasAncestor(*newParentID, id)
		if err != nil {
			return err
		}
		if isDescendant {
			return errors.New("folder cannot be moved into its own descendant")
		}
	}
	_, err := s.db.Exec(`UPDATE folders SET parent_id = ?, updated_at = ? WHERE id = ?`, newParentID, time.Now(), id)
	return err
}

func (s *SQLiteStore) folderHasAncestor(folderID string, ancestorID string) (bool, error) {
	current := folderID
	for current != "" {
		var parent sql.NullString
		if err := s.db.QueryRow(`SELECT parent_id FROM folders WHERE id = ?`, current).Scan(&parent); err != nil {
			return false, err
		}
		if !parent.Valid {
			return false, nil
		}
		if parent.String == ancestorID {
			return true, nil
		}
		current = parent.String
	}
	return false, nil
}

func (s *SQLiteStore) DeleteFolder(id string) error {
	var childCount, scriptCount, testCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM folders WHERE parent_id = ?`, id).Scan(&childCount); err != nil {
		return err
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM scripts WHERE folder_id = ?`, id).Scan(&scriptCount); err != nil {
		return err
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM tests WHERE folder_id = ?`, id).Scan(&testCount); err != nil {
		return err
	}
	if childCount+scriptCount+testCount > 0 {
		return errors.New("folder is not empty")
	}
	_, err := s.db.Exec(`DELETE FROM folders WHERE id = ?`, id)
	return err
}

func (s *SQLiteStore) SaveScript(script *dsl.Script) error {
	script.ProjectID = normalizeProjectID(script.ProjectID)
	metadataJSON, _ := json.Marshal(script.Metadata)
	configJSON, _ := json.Marshal(script.Config)
	dataSourcesJSON, _ := json.Marshal(script.DataSources)
	initJSON, _ := json.Marshal(script.Init)
	actionsJSON, _ := json.Marshal(script.Actions)
	teardownJSON, _ := json.Marshal(script.Teardown)

	if script.CreatedAt.IsZero() {
		script.CreatedAt = time.Now()
	}
	script.UpdatedAt = time.Now()

	query := `INSERT INTO scripts (id, name, protocol, version, project_id, folder_id, generated_by, generated_from, metadata_json, config, data_sources, init, actions, teardown, created_at, updated_at)
			  VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			  ON CONFLICT(id) DO UPDATE SET
			  name=excluded.name, protocol=excluded.protocol, version=excluded.version, 
			  project_id=excluded.project_id, folder_id=excluded.folder_id,
			  generated_by=excluded.generated_by, generated_from=excluded.generated_from,
			  metadata_json=excluded.metadata_json,
			  config=excluded.config, data_sources=excluded.data_sources, 
			  init=excluded.init, actions=excluded.actions, teardown=excluded.teardown, 
			  updated_at=excluded.updated_at`

	_, err := s.db.Exec(query,
		script.ID, script.Name, script.Protocol, script.Version, script.ProjectID, script.FolderID,
		script.GeneratedBy, script.GeneratedFrom,
		string(metadataJSON), string(configJSON), string(dataSourcesJSON), string(initJSON), string(actionsJSON), string(teardownJSON),
		script.CreatedAt, script.UpdatedAt)
	return err
}

func (s *SQLiteStore) GetScript(id string) (*dsl.Script, error) {
	var script dsl.Script
	var metadataJSON, configJSON, dataSourcesJSON, initJSON, actionsJSON, teardownJSON string

	query := `SELECT id, name, protocol, version, COALESCE(project_id, ''), folder_id, generated_by, generated_from, COALESCE(metadata_json, ''), config, data_sources, init, actions, teardown, created_at, updated_at
			  FROM scripts WHERE id = ?`
	err := s.db.QueryRow(query, id).Scan(
		&script.ID, &script.Name, &script.Protocol, &script.Version,
		&script.ProjectID, &script.FolderID,
		&script.GeneratedBy, &script.GeneratedFrom,
		&metadataJSON, &configJSON, &dataSourcesJSON, &initJSON, &actionsJSON, &teardownJSON,
		&script.CreatedAt, &script.UpdatedAt)
	if err != nil {
		return nil, err
	}

	if metadataJSON != "" && metadataJSON != "null" {
		json.Unmarshal([]byte(metadataJSON), &script.Metadata)
	}
	json.Unmarshal([]byte(configJSON), &script.Config)
	json.Unmarshal([]byte(dataSourcesJSON), &script.DataSources)
	json.Unmarshal([]byte(initJSON), &script.Init)
	json.Unmarshal([]byte(actionsJSON), &script.Actions)
	json.Unmarshal([]byte(teardownJSON), &script.Teardown)

	return &script, nil
}

func (s *SQLiteStore) ListScripts(projectID string, folderID *string) ([]*dsl.Script, error) {
	args := []any{}
	query := `SELECT id, name, protocol, version, COALESCE(project_id, ''), folder_id, generated_by, generated_from, COALESCE(metadata_json, ''), config, data_sources, init, actions, teardown, created_at, updated_at FROM scripts`
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
	query += ` ORDER BY created_at DESC`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var scripts []*dsl.Script
	for rows.Next() {
		var script dsl.Script
		var metadataJSON, configJSON, dataSourcesJSON, initJSON, actionsJSON, teardownJSON string
		err := rows.Scan(
			&script.ID, &script.Name, &script.Protocol, &script.Version,
			&script.ProjectID, &script.FolderID,
			&script.GeneratedBy, &script.GeneratedFrom,
			&metadataJSON, &configJSON, &dataSourcesJSON, &initJSON, &actionsJSON, &teardownJSON,
			&script.CreatedAt, &script.UpdatedAt)
		if err != nil {
			return nil, err
		}
		if metadataJSON != "" && metadataJSON != "null" {
			json.Unmarshal([]byte(metadataJSON), &script.Metadata)
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

func (s *SQLiteStore) MoveScript(id string, folderID *string) error {
	_, err := s.db.Exec(`UPDATE scripts SET folder_id = ?, updated_at = ? WHERE id = ?`, folderID, time.Now(), id)
	return err
}

func (s *SQLiteStore) SaveTest(test *TestRecord) error {
	test.ProjectID = normalizeProjectID(test.ProjectID)
	groupsJSON, _ := json.Marshal(test.Groups)
	runtimeSettingsJSON, _ := json.Marshal(test.RuntimeSettings)
	goalConfigJSON, _ := json.Marshal(test.GoalConfig)

	query := `INSERT INTO tests (id, name, status, project_id, folder_id, scenario_mode, shakeout_enabled, resource_baseline_enabled, total_vusers, duration_ns, start_time, end_time, error, groups_json, runtime_settings_json, goal_config_json)
			  VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err := s.db.Exec(query, test.ID, test.Name, test.Status, test.ProjectID, test.FolderID, test.ScenarioMode, test.ScriptShakeout, test.ResourceBaseline, test.TotalVUsers, test.Duration, test.StartTime, test.EndTime, test.Error, string(groupsJSON), string(runtimeSettingsJSON), string(goalConfigJSON))
	return err
}

func (s *SQLiteStore) GetTest(id string) (*TestRecord, error) {
	var test TestRecord
	var groupsJSON, runtimeSettingsJSON, goalConfigJSON string

	query := `SELECT id, name, status, COALESCE(project_id, ''), folder_id, COALESCE(scenario_mode, ''), COALESCE(shakeout_enabled, 0), COALESCE(resource_baseline_enabled, 0), total_vusers, duration_ns, start_time, end_time, error, groups_json, COALESCE(runtime_settings_json, ''), COALESCE(goal_config_json, '') FROM tests WHERE id = ?`
	err := s.db.QueryRow(query, id).Scan(&test.ID, &test.Name, &test.Status, &test.ProjectID, &test.FolderID, &test.ScenarioMode, &test.ScriptShakeout, &test.ResourceBaseline, &test.TotalVUsers, &test.Duration, &test.StartTime, &test.EndTime, &test.Error, &groupsJSON, &runtimeSettingsJSON, &goalConfigJSON)
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

func (s *SQLiteStore) ListTests(projectID string, folderID *string) ([]*TestRecord, error) {
	args := []any{}
	query := `SELECT id, name, status, COALESCE(project_id, ''), folder_id, COALESCE(scenario_mode, ''), COALESCE(shakeout_enabled, 0), COALESCE(resource_baseline_enabled, 0), total_vusers, duration_ns, start_time, end_time, error, groups_json, COALESCE(runtime_settings_json, ''), COALESCE(goal_config_json, '') FROM tests`
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
		var test TestRecord
		var groupsJSON, runtimeSettingsJSON, goalConfigJSON string
		if err := rows.Scan(&test.ID, &test.Name, &test.Status, &test.ProjectID, &test.FolderID, &test.ScenarioMode, &test.ScriptShakeout, &test.ResourceBaseline, &test.TotalVUsers, &test.Duration, &test.StartTime, &test.EndTime, &test.Error, &groupsJSON, &runtimeSettingsJSON, &goalConfigJSON); err != nil {
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

func (s *SQLiteStore) MoveTest(id string, folderID *string) error {
	_, err := s.db.Exec(`UPDATE tests SET folder_id = ? WHERE id = ?`, folderID, id)
	return err
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

func (s *SQLiteStore) SaveSetting(key string, value string) error {
	_, err := s.db.Exec(`INSERT INTO app_settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`, key, value, time.Now())
	return err
}

func (s *SQLiteStore) GetSetting(key string) (string, error) {
	var value string
	err := s.db.QueryRow(`SELECT value FROM app_settings WHERE key = ?`, key).Scan(&value)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	return value, nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}
