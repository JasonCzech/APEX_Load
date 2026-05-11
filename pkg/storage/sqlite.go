package storage

import (
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
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
		`CREATE TABLE IF NOT EXISTS applications (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS releases (
			id TEXT PRIMARY KEY,
			application_id TEXT NOT NULL,
			name TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY(application_id) REFERENCES applications(id)
		)`,
		`CREATE TABLE IF NOT EXISTS scenarios (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			duration TEXT NOT NULL,
			scenario_mode TEXT,
			shakeout_enabled INTEGER DEFAULT 0,
			resource_baseline_enabled INTEGER DEFAULT 0,
			goal_config_json TEXT,
			runtime_settings_json TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS scenario_scripts (
			scenario_id TEXT NOT NULL,
			sort_order INTEGER NOT NULL,
			name TEXT,
			script_id TEXT NOT NULL,
			vusers INTEGER,
			ramp_up TEXT,
			ramp_down TEXT,
			initialization TEXT,
			PRIMARY KEY (scenario_id, sort_order),
			FOREIGN KEY(scenario_id) REFERENCES scenarios(id),
			FOREIGN KEY(script_id) REFERENCES scripts(id)
		)`,
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
			application_id TEXT,
			release_id TEXT,
			scenario_id TEXT,
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
		{`ALTER TABLE tests ADD COLUMN application_id TEXT REFERENCES applications(id)`, "tests.application_id"},
		{`ALTER TABLE tests ADD COLUMN release_id TEXT REFERENCES releases(id)`, "tests.release_id"},
		{`ALTER TABLE tests ADD COLUMN scenario_id TEXT REFERENCES scenarios(id)`, "tests.scenario_id"},
	} {
		if _, err := s.db.Exec(migration.query); err != nil && !isDuplicateColumnErr(err) {
			return fmt.Errorf("failed to add %s column: %w", migration.name, err)
		}
	}

	if err := s.ensureDefaultProject(); err != nil {
		return err
	}
	return s.ensureLegacyApplication()
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

func normalizeApplicationID(applicationID string) string {
	if applicationID == "" {
		return LegacyApplicationID
	}
	return applicationID
}

func normalizeReleaseID(releaseID string) string {
	if releaseID == "" {
		return LegacyReleaseID
	}
	return releaseID
}

func legacyScenarioID(name string) string {
	if strings.TrimSpace(name) == "" {
		name = "Unnamed Scenario"
	}
	sum := sha1.Sum([]byte(name))
	return "legacy-" + hex.EncodeToString(sum[:])[:12]
}

func (s *SQLiteStore) ensureLegacyApplication() error {
	now := time.Now()
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO applications (id, name, created_at, updated_at) VALUES (?, ?, ?, ?)`, LegacyApplicationID, "Legacy", now, now); err != nil {
		return fmt.Errorf("failed to create legacy application: %w", err)
	}
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO releases (id, application_id, name, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, LegacyReleaseID, LegacyApplicationID, "Imported Legacy", now, now); err != nil {
		return fmt.Errorf("failed to create legacy release: %w", err)
	}
	if _, err := s.db.Exec(`UPDATE tests SET application_id = ? WHERE application_id IS NULL OR application_id = ''`, LegacyApplicationID); err != nil {
		return fmt.Errorf("failed to backfill test application ids: %w", err)
	}
	if _, err := s.db.Exec(`UPDATE tests SET release_id = ? WHERE release_id IS NULL OR release_id = ''`, LegacyReleaseID); err != nil {
		return fmt.Errorf("failed to backfill test release ids: %w", err)
	}
	return s.ensureLegacyScenarios()
}

func (s *SQLiteStore) ensureLegacyScenarios() error {
	rows, err := s.db.Query(`SELECT id, COALESCE(name, ''), COALESCE(scenario_mode, ''), COALESCE(shakeout_enabled, 0), COALESCE(resource_baseline_enabled, 0), duration_ns, COALESCE(groups_json, '[]'), COALESCE(runtime_settings_json, ''), COALESCE(goal_config_json, ''), start_time
		FROM tests
		WHERE scenario_id IS NULL OR scenario_id = ''
		ORDER BY CASE WHEN status = 'QUEUED' THEN 0 ELSE 1 END, start_time DESC`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type legacyCandidate struct {
		testID           string
		name             string
		scenarioMode     string
		scriptShakeout   bool
		resourceBaseline bool
		duration         int64
		groupsJSON       string
		runtimeJSON      string
		goalJSON         string
		startTime        time.Time
	}
	seen := map[string]bool{}
	var candidates []legacyCandidate
	for rows.Next() {
		var candidate legacyCandidate
		if err := rows.Scan(&candidate.testID, &candidate.name, &candidate.scenarioMode, &candidate.scriptShakeout, &candidate.resourceBaseline, &candidate.duration, &candidate.groupsJSON, &candidate.runtimeJSON, &candidate.goalJSON, &candidate.startTime); err != nil {
			return err
		}
		if candidate.name == "" {
			candidate.name = "Unnamed Scenario"
		}
		scenarioID := legacyScenarioID(candidate.name)
		if seen[scenarioID] {
			continue
		}
		seen[scenarioID] = true
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, candidate := range candidates {
		scenarioID := legacyScenarioID(candidate.name)
		now := candidate.startTime
		if now.IsZero() {
			now = time.Now()
		}
		duration := time.Duration(candidate.duration).String()
		if candidate.duration <= 0 {
			duration = "1m"
		}
		if _, err := s.db.Exec(`INSERT OR IGNORE INTO scenarios (id, name, duration, scenario_mode, shakeout_enabled, resource_baseline_enabled, goal_config_json, runtime_settings_json, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, scenarioID, candidate.name, duration, candidate.scenarioMode, candidate.scriptShakeout, candidate.resourceBaseline, candidate.goalJSON, candidate.runtimeJSON, now, now); err != nil {
			return err
		}
		if err := s.insertLegacyScenarioScripts(scenarioID, candidate.groupsJSON); err != nil {
			return err
		}
		if _, err := s.db.Exec(`UPDATE tests SET scenario_id = ? WHERE name = ? AND (scenario_id IS NULL OR scenario_id = '')`, scenarioID, candidate.name); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) insertLegacyScenarioScripts(scenarioID string, groupsJSON string) error {
	var groups []struct {
		Name           string `json:"name"`
		ScriptID       string `json:"script_id"`
		VUsers         int    `json:"vusers"`
		RampUp         string `json:"ramp_up"`
		RampDown       string `json:"ramp_down"`
		Initialization string `json:"initialization"`
	}
	if err := json.Unmarshal([]byte(groupsJSON), &groups); err != nil {
		return nil
	}
	for i, group := range groups {
		if group.ScriptID == "" {
			continue
		}
		if _, err := s.db.Exec(`INSERT OR IGNORE INTO scenario_scripts (scenario_id, sort_order, name, script_id, vusers, ramp_up, ramp_down, initialization)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, scenarioID, i, group.Name, group.ScriptID, group.VUsers, group.RampUp, group.RampDown, group.Initialization); err != nil {
			return err
		}
	}
	return nil
}

func validateFolderCategory(category string) error {
	switch category {
	case "scripts", "scenarios", "results", "recordings", "general":
		return nil
	default:
		return fmt.Errorf("invalid folder category: %s", category)
	}
}

func (s *SQLiteStore) SaveApplication(application *Application) error {
	if application.ID == "" {
		return errors.New("application id is required")
	}
	if strings.TrimSpace(application.Name) == "" {
		return errors.New("application name is required")
	}
	if application.CreatedAt.IsZero() {
		application.CreatedAt = time.Now()
	}
	application.UpdatedAt = time.Now()
	_, err := s.db.Exec(`INSERT INTO applications (id, name, created_at, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, updated_at=excluded.updated_at`,
		application.ID, application.Name, application.CreatedAt, application.UpdatedAt)
	return err
}

func (s *SQLiteStore) GetApplication(id string) (*Application, error) {
	var application Application
	err := s.db.QueryRow(`SELECT id, name, created_at, updated_at FROM applications WHERE id = ?`, id).Scan(
		&application.ID, &application.Name, &application.CreatedAt, &application.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &application, nil
}

func (s *SQLiteStore) ListApplications() ([]*Application, error) {
	rows, err := s.db.Query(`SELECT id, name, created_at, updated_at FROM applications ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var applications []*Application
	for rows.Next() {
		var application Application
		if err := rows.Scan(&application.ID, &application.Name, &application.CreatedAt, &application.UpdatedAt); err != nil {
			return nil, err
		}
		applications = append(applications, &application)
	}
	return applications, rows.Err()
}

func (s *SQLiteStore) DeleteApplication(id string) error {
	if id == LegacyApplicationID {
		return errors.New("legacy application cannot be deleted")
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM tests WHERE application_id = ?`, id).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return errors.New("application has test results")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM releases WHERE application_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM applications WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) SaveRelease(release *Release) error {
	if release.ID == "" {
		return errors.New("release id is required")
	}
	release.ApplicationID = normalizeApplicationID(release.ApplicationID)
	if strings.TrimSpace(release.Name) == "" {
		return errors.New("release name is required")
	}
	if release.CreatedAt.IsZero() {
		release.CreatedAt = time.Now()
	}
	release.UpdatedAt = time.Now()
	_, err := s.db.Exec(`INSERT INTO releases (id, application_id, name, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET application_id=excluded.application_id, name=excluded.name, updated_at=excluded.updated_at`,
		release.ID, release.ApplicationID, release.Name, release.CreatedAt, release.UpdatedAt)
	return err
}

func (s *SQLiteStore) GetRelease(id string) (*Release, error) {
	var release Release
	err := s.db.QueryRow(`SELECT id, application_id, name, created_at, updated_at FROM releases WHERE id = ?`, id).Scan(
		&release.ID, &release.ApplicationID, &release.Name, &release.CreatedAt, &release.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &release, nil
}

func (s *SQLiteStore) ListReleases(applicationID string) ([]*Release, error) {
	args := []any{}
	query := `SELECT id, application_id, name, created_at, updated_at FROM releases`
	if applicationID != "" {
		query += ` WHERE application_id = ?`
		args = append(args, applicationID)
	}
	query += ` ORDER BY created_at DESC, name ASC`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var releases []*Release
	for rows.Next() {
		var release Release
		if err := rows.Scan(&release.ID, &release.ApplicationID, &release.Name, &release.CreatedAt, &release.UpdatedAt); err != nil {
			return nil, err
		}
		releases = append(releases, &release)
	}
	return releases, rows.Err()
}

func (s *SQLiteStore) DeleteRelease(id string) error {
	if id == LegacyReleaseID {
		return errors.New("legacy release cannot be deleted")
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM tests WHERE release_id = ?`, id).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return errors.New("release has test results")
	}
	_, err := s.db.Exec(`DELETE FROM releases WHERE id = ?`, id)
	return err
}

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
	goalJSON, _ := json.Marshal(scenario.GoalConfig)
	runtimeJSON, _ := json.Marshal(scenario.RuntimeSettings)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO scenarios (id, name, duration, scenario_mode, shakeout_enabled, resource_baseline_enabled, goal_config_json, runtime_settings_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, duration=excluded.duration, scenario_mode=excluded.scenario_mode,
		shakeout_enabled=excluded.shakeout_enabled, resource_baseline_enabled=excluded.resource_baseline_enabled,
		goal_config_json=excluded.goal_config_json, runtime_settings_json=excluded.runtime_settings_json, updated_at=excluded.updated_at`,
		scenario.ID, scenario.Name, scenario.Duration, scenario.ScenarioMode, scenario.ScriptShakeout, scenario.ResourceBaseline, string(goalJSON), string(runtimeJSON), scenario.CreatedAt, scenario.UpdatedAt); err != nil {
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
		if _, err := tx.Exec(`INSERT INTO scenario_scripts (scenario_id, sort_order, name, script_id, vusers, ramp_up, ramp_down, initialization)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, script.ScenarioID, script.Order, script.Name, script.ScriptID, script.VUsers, script.RampUp, script.RampDown, script.Initialization); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLiteStore) GetScenario(id string) (*Scenario, []ScenarioScript, error) {
	var scenario Scenario
	var goalJSON, runtimeJSON string
	err := s.db.QueryRow(`SELECT id, name, duration, COALESCE(scenario_mode, ''), COALESCE(shakeout_enabled, 0), COALESCE(resource_baseline_enabled, 0), COALESCE(goal_config_json, ''), COALESCE(runtime_settings_json, ''), created_at, updated_at FROM scenarios WHERE id = ?`, id).Scan(
		&scenario.ID, &scenario.Name, &scenario.Duration, &scenario.ScenarioMode, &scenario.ScriptShakeout, &scenario.ResourceBaseline, &goalJSON, &runtimeJSON, &scenario.CreatedAt, &scenario.UpdatedAt)
	if err != nil {
		return nil, nil, err
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
	rows, err := s.db.Query(`SELECT id, name, duration, COALESCE(scenario_mode, ''), COALESCE(shakeout_enabled, 0), COALESCE(resource_baseline_enabled, 0), COALESCE(goal_config_json, ''), COALESCE(runtime_settings_json, ''), created_at, updated_at FROM scenarios ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var scenarios []*Scenario
	for rows.Next() {
		var scenario Scenario
		var goalJSON, runtimeJSON string
		if err := rows.Scan(&scenario.ID, &scenario.Name, &scenario.Duration, &scenario.ScenarioMode, &scenario.ScriptShakeout, &scenario.ResourceBaseline, &goalJSON, &runtimeJSON, &scenario.CreatedAt, &scenario.UpdatedAt); err != nil {
			return nil, err
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
	rows, err := s.db.Query(`SELECT scenario_id, sort_order, COALESCE(name, ''), script_id, COALESCE(vusers, 0), COALESCE(ramp_up, ''), COALESCE(ramp_down, ''), COALESCE(initialization, '') FROM scenario_scripts WHERE scenario_id = ? ORDER BY sort_order ASC`, scenarioID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var scripts []ScenarioScript
	for rows.Next() {
		var script ScenarioScript
		if err := rows.Scan(&script.ScenarioID, &script.Order, &script.Name, &script.ScriptID, &script.VUsers, &script.RampUp, &script.RampDown, &script.Initialization); err != nil {
			return nil, err
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
	if _, err := tx.Exec(`DELETE FROM scenarios WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
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
	if category != "" && category != "general" {
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
	test.ApplicationID = normalizeApplicationID(test.ApplicationID)
	test.ReleaseID = normalizeReleaseID(test.ReleaseID)
	test.ProjectID = normalizeProjectID(test.ProjectID)
	groupsJSON, _ := json.Marshal(test.Groups)
	runtimeSettingsJSON, _ := json.Marshal(test.RuntimeSettings)
	goalConfigJSON, _ := json.Marshal(test.GoalConfig)

	query := `INSERT INTO tests (id, name, status, application_id, release_id, scenario_id, project_id, folder_id, scenario_mode, shakeout_enabled, resource_baseline_enabled, total_vusers, duration_ns, start_time, end_time, error, groups_json, runtime_settings_json, goal_config_json)
			  VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			  ON CONFLICT(id) DO UPDATE SET
			  name=excluded.name, status=excluded.status, application_id=excluded.application_id, release_id=excluded.release_id, scenario_id=excluded.scenario_id,
			  project_id=excluded.project_id, folder_id=excluded.folder_id,
			  scenario_mode=excluded.scenario_mode, shakeout_enabled=excluded.shakeout_enabled,
			  resource_baseline_enabled=excluded.resource_baseline_enabled, total_vusers=excluded.total_vusers,
			  duration_ns=excluded.duration_ns, start_time=excluded.start_time, end_time=excluded.end_time,
			  error=excluded.error, groups_json=excluded.groups_json,
			  runtime_settings_json=excluded.runtime_settings_json, goal_config_json=excluded.goal_config_json`

	_, err := s.db.Exec(query, test.ID, test.Name, test.Status, test.ApplicationID, test.ReleaseID, test.ScenarioID, test.ProjectID, test.FolderID, test.ScenarioMode, test.ScriptShakeout, test.ResourceBaseline, test.TotalVUsers, test.Duration, test.StartTime, test.EndTime, test.Error, string(groupsJSON), string(runtimeSettingsJSON), string(goalConfigJSON))
	return err
}

func (s *SQLiteStore) GetTest(id string) (*TestRecord, error) {
	var test TestRecord
	var groupsJSON, runtimeSettingsJSON, goalConfigJSON string

	query := `SELECT id, name, status, COALESCE(application_id, ''), COALESCE(release_id, ''), COALESCE(scenario_id, ''), COALESCE(project_id, ''), folder_id, COALESCE(scenario_mode, ''), COALESCE(shakeout_enabled, 0), COALESCE(resource_baseline_enabled, 0), total_vusers, duration_ns, start_time, end_time, error, groups_json, COALESCE(runtime_settings_json, ''), COALESCE(goal_config_json, '') FROM tests WHERE id = ?`
	err := s.db.QueryRow(query, id).Scan(&test.ID, &test.Name, &test.Status, &test.ApplicationID, &test.ReleaseID, &test.ScenarioID, &test.ProjectID, &test.FolderID, &test.ScenarioMode, &test.ScriptShakeout, &test.ResourceBaseline, &test.TotalVUsers, &test.Duration, &test.StartTime, &test.EndTime, &test.Error, &groupsJSON, &runtimeSettingsJSON, &goalConfigJSON)
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
	query := `SELECT id, name, status, COALESCE(application_id, ''), COALESCE(release_id, ''), COALESCE(scenario_id, ''), COALESCE(project_id, ''), folder_id, COALESCE(scenario_mode, ''), COALESCE(shakeout_enabled, 0), COALESCE(resource_baseline_enabled, 0), total_vusers, duration_ns, start_time, end_time, error, groups_json, COALESCE(runtime_settings_json, ''), COALESCE(goal_config_json, '') FROM tests`
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
		if err := rows.Scan(&test.ID, &test.Name, &test.Status, &test.ApplicationID, &test.ReleaseID, &test.ScenarioID, &test.ProjectID, &test.FolderID, &test.ScenarioMode, &test.ScriptShakeout, &test.ResourceBaseline, &test.TotalVUsers, &test.Duration, &test.StartTime, &test.EndTime, &test.Error, &groupsJSON, &runtimeSettingsJSON, &goalConfigJSON); err != nil {
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

func (s *SQLiteStore) ListTestsByContext(applicationID string, releaseID string, scenarioID string) ([]*TestRecord, error) {
	args := []any{}
	query := `SELECT id, name, status, COALESCE(application_id, ''), COALESCE(release_id, ''), COALESCE(scenario_id, ''), COALESCE(project_id, ''), folder_id, COALESCE(scenario_mode, ''), COALESCE(shakeout_enabled, 0), COALESCE(resource_baseline_enabled, 0), total_vusers, duration_ns, start_time, end_time, error, groups_json, COALESCE(runtime_settings_json, ''), COALESCE(goal_config_json, '') FROM tests`
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
		var test TestRecord
		var groupsJSON, runtimeSettingsJSON, goalConfigJSON string
		if err := rows.Scan(&test.ID, &test.Name, &test.Status, &test.ApplicationID, &test.ReleaseID, &test.ScenarioID, &test.ProjectID, &test.FolderID, &test.ScenarioMode, &test.ScriptShakeout, &test.ResourceBaseline, &test.TotalVUsers, &test.Duration, &test.StartTime, &test.EndTime, &test.Error, &groupsJSON, &runtimeSettingsJSON, &goalConfigJSON); err != nil {
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
	return tests, rows.Err()
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
