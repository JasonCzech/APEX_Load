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

const (
	intervalSummarySchemaVersionV1 = "stormrunner.interval-summary.v1"
	intervalSummarySchemaVersion   = "stormrunner.interval-summary.v3"
	archiveRecordSchemaVersion     = "stormrunner.archive.v1"
)

type BrowserMetricsSummaryRecord struct {
	Samples              int64   `json:"samples"`
	AvgFCPMs             float64 `json:"avg_fcp_ms,omitempty"`
	AvgLCPMs             float64 `json:"avg_lcp_ms,omitempty"`
	AvgCLSScore          float64 `json:"avg_cls_score,omitempty"`
	AvgINPMs             float64 `json:"avg_inp_ms,omitempty"`
	AvgBlockingResources float64 `json:"avg_blocking_resources,omitempty"`
	AvgLongTaskCount     float64 `json:"avg_long_task_count,omitempty"`
	AvgJSHeapUsedMB      float64 `json:"avg_js_heap_used_mb,omitempty"`
	AvgBrowserProcessCPU float64 `json:"avg_browser_process_cpu_pct,omitempty"`
	AvgBrowserProcessMem float64 `json:"avg_browser_process_mem_mb,omitempty"`
}

type BrowserDeviceMetricsRecord struct {
	DeviceProfile        string  `json:"device_profile,omitempty"`
	DevicePlatform       string  `json:"device_platform,omitempty"`
	DeviceTier           string  `json:"device_tier,omitempty"`
	BrowserEngine        string  `json:"browser_engine,omitempty"`
	Samples              int64   `json:"samples"`
	AvgFCPMs             float64 `json:"avg_fcp_ms,omitempty"`
	AvgLCPMs             float64 `json:"avg_lcp_ms,omitempty"`
	AvgCLSScore          float64 `json:"avg_cls_score,omitempty"`
	AvgINPMs             float64 `json:"avg_inp_ms,omitempty"`
	AvgBlockingResources float64 `json:"avg_blocking_resources,omitempty"`
	AvgLongTaskCount     float64 `json:"avg_long_task_count,omitempty"`
	AvgJSHeapUsedMB      float64 `json:"avg_js_heap_used_mb,omitempty"`
	AvgBrowserProcessCPU float64 `json:"avg_browser_process_cpu_pct,omitempty"`
	AvgBrowserProcessMem float64 `json:"avg_browser_process_mem_mb,omitempty"`
}

type IntervalSummaryRecord struct {
	SchemaVersion            string                                 `json:"schema_version,omitempty"`
	Timestamp                time.Time                              `json:"timestamp"`
	ActiveVUsers             int64                                  `json:"active_vusers"`
	TPS                      float64                                `json:"tps"`
	AvgResponseMs            float64                                `json:"avg_response_ms"`
	ErrorPct                 float64                                `json:"error_pct"`
	TotalTx                  int64                                  `json:"total_transactions"`
	TotalErrors              int64                                  `json:"total_errors"`
	P50Ms                    float64                                `json:"p50_ms"`
	P90Ms                    float64                                `json:"p90_ms"`
	P95Ms                    float64                                `json:"p95_ms"`
	P99Ms                    float64                                `json:"p99_ms"`
	Browser                  *BrowserMetricsSummaryRecord           `json:"browser,omitempty"`
	BrowserByDevice          map[string]*BrowserDeviceMetricsRecord `json:"browser_by_device,omitempty"`
	DroppedResults           int64                                  `json:"dropped_results,omitempty"`
	WebsocketClients         int                                    `json:"websocket_clients,omitempty"`
	WebsocketDroppedMessages int64                                  `json:"websocket_dropped_messages,omitempty"`
	WebsocketDroppedEvents   int64                                  `json:"websocket_dropped_events,omitempty"`
	GeneratorPublishedAt     time.Time                              `json:"generator_published_at,omitempty"`
	ControllerReceivedAt     time.Time                              `json:"controller_received_at,omitempty"`
	CollectedAt              time.Time                              `json:"collected_at,omitempty"`
	PublishedAt              time.Time                              `json:"published_at,omitempty"`
	GeneratorProcessingLagMs float64                                `json:"generator_processing_lag_ms,omitempty"`
	TransportLagMs           float64                                `json:"transport_lag_ms,omitempty"`
	ControllerFanoutLagMs    float64                                `json:"controller_fanout_lag_ms,omitempty"`
	SampleAgeMs              float64                                `json:"sample_age_ms,omitempty"`
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
			project_id TEXT REFERENCES projects(id),
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
			archived_at DATETIME,
			FOREIGN KEY(application_id) REFERENCES applications(id)
		)`,
		`CREATE TABLE IF NOT EXISTS scenarios (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			duration TEXT NOT NULL,
			start_delay TEXT,
			scenario_mode TEXT,
			shakeout_enabled INTEGER DEFAULT 0,
			resource_baseline_enabled INTEGER DEFAULT 0,
			assignment_json TEXT,
			goal_config_json TEXT,
			runtime_settings_json TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS scenario_schedules (
			id TEXT PRIMARY KEY,
			scenario_id TEXT NOT NULL,
			project_id TEXT,
			revision INTEGER DEFAULT 1,
			name TEXT NOT NULL,
			cron_expr TEXT NOT NULL,
			timezone TEXT,
			release_id TEXT,
			run_name_template TEXT,
			enabled INTEGER DEFAULT 1,
			approval_policy TEXT,
			missed_run_policy TEXT,
			conflict_summary TEXT,
			next_required_action TEXT,
			pending_approval_id TEXT,
			owner_subject TEXT,
			owner_display_name TEXT,
			governance_json TEXT,
			blackout_windows_json TEXT,
			availability_windows_json TEXT,
			last_run_test_id TEXT,
			last_run_at DATETIME,
			next_run_at DATETIME,
			last_error TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY(scenario_id) REFERENCES scenarios(id),
			FOREIGN KEY(release_id) REFERENCES releases(id),
			FOREIGN KEY(last_run_test_id) REFERENCES tests(id)
		)`,
		`CREATE TABLE IF NOT EXISTS scenario_schedule_occurrences (
			id TEXT PRIMARY KEY,
			schedule_id TEXT NOT NULL,
			scenario_id TEXT NOT NULL,
			project_id TEXT,
			schedule_revision INTEGER DEFAULT 1,
			planned_at DATETIME NOT NULL,
			materialized_at DATETIME,
			status TEXT NOT NULL,
			reason_code TEXT,
			test_id TEXT,
			approval_id TEXT,
			conflict_summary TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			UNIQUE(schedule_id, planned_at),
			FOREIGN KEY(schedule_id) REFERENCES scenario_schedules(id),
			FOREIGN KEY(test_id) REFERENCES tests(id)
		)`,
		`CREATE TABLE IF NOT EXISTS scenario_schedule_approvals (
			id TEXT PRIMARY KEY,
			schedule_id TEXT NOT NULL,
			scenario_id TEXT NOT NULL,
			project_id TEXT,
			schedule_revision INTEGER DEFAULT 1,
			policy TEXT NOT NULL,
			status TEXT NOT NULL,
			requested_by TEXT,
			requested_at DATETIME NOT NULL,
			resolved_by TEXT,
			resolved_at DATETIME,
			decision_reason TEXT,
			required_role TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY(schedule_id) REFERENCES scenario_schedules(id)
		)`,
		`CREATE TABLE IF NOT EXISTS scheduler_leases (
			name TEXT PRIMARY KEY,
			owner_id TEXT NOT NULL,
			acquired_at DATETIME NOT NULL,
			expires_at DATETIME NOT NULL,
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
			stages_json TEXT,
			initialization TEXT,
			PRIMARY KEY (scenario_id, sort_order),
			FOREIGN KEY(scenario_id) REFERENCES scenarios(id),
			FOREIGN KEY(script_id) REFERENCES scripts(id)
		)`,
		`CREATE TABLE IF NOT EXISTS shared_data_pools (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT,
			schema_json TEXT,
			project_id TEXT REFERENCES projects(id),
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			archived_at DATETIME
		)`,
		`CREATE TABLE IF NOT EXISTS shared_data_pool_rows (
			id TEXT PRIMARY KEY,
			pool_id TEXT NOT NULL,
			data_json TEXT NOT NULL,
			status TEXT,
			version INTEGER DEFAULT 1,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY(pool_id) REFERENCES shared_data_pools(id)
		)`,
		`CREATE TABLE IF NOT EXISTS scenario_data_pool_bindings (
			id TEXT PRIMARY KEY,
			scenario_id TEXT NOT NULL,
			pool_id TEXT NOT NULL,
			alias TEXT NOT NULL,
			access_mode TEXT NOT NULL,
			selection_strategy TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			UNIQUE(scenario_id, alias),
			FOREIGN KEY(scenario_id) REFERENCES scenarios(id),
			FOREIGN KEY(pool_id) REFERENCES shared_data_pools(id)
		)`,
		`CREATE TABLE IF NOT EXISTS projects (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			settings_json TEXT,
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
			assignment_json TEXT,
			total_vusers INTEGER,
			duration_ns INTEGER,
			start_time DATETIME,
			pending_since DATETIME,
			baseline_pinned INTEGER DEFAULT 0,
			baseline_pinned_at DATETIME,
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
		`CREATE TABLE IF NOT EXISTS test_interval_summaries (
			test_id TEXT,
			schema_version TEXT,
			timestamp DATETIME,
			active_vusers INTEGER,
			tps REAL,
			avg_response_ms REAL,
			error_pct REAL,
			total_transactions INTEGER,
			total_errors INTEGER,
			p50_ms REAL,
			p90_ms REAL,
			p95_ms REAL,
			p99_ms REAL,
			browser_json TEXT,
			browser_by_device_json TEXT,
			dropped_results INTEGER,
			websocket_clients INTEGER,
			websocket_dropped_messages INTEGER,
			websocket_dropped_events INTEGER,
			generator_published_at DATETIME,
			controller_received_at DATETIME,
			collected_at DATETIME,
			published_at DATETIME,
			generator_processing_lag_ms REAL,
			transport_lag_ms REAL,
			controller_fanout_lag_ms REAL,
			sample_age_ms REAL,
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
		`CREATE TABLE IF NOT EXISTS service_accounts (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT,
			key_hash TEXT NOT NULL UNIQUE,
			active INTEGER NOT NULL DEFAULT 1,
			global_roles_json TEXT,
			project_roles_json TEXT,
			scopes_json TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			last_used_at DATETIME,
			expires_at DATETIME,
			key_rotated_at DATETIME,
			rotation_interval_days INTEGER
		)`,
		`CREATE TABLE IF NOT EXISTS audit_events (
			id TEXT PRIMARY KEY,
			action TEXT NOT NULL,
			actor_type TEXT NOT NULL,
			actor_subject TEXT NOT NULL,
			actor_display_name TEXT,
			project_id TEXT,
			resource_type TEXT,
			resource_id TEXT,
			status TEXT NOT NULL,
			details_json TEXT,
			created_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS usage_accounting_snapshots (
			id TEXT PRIMARY KEY,
			project_id TEXT,
			generated_at DATETIME NOT NULL,
			payload_json TEXT NOT NULL,
			created_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS authoring_sessions (
			id TEXT PRIMARY KEY,
			project_id TEXT,
			source TEXT NOT NULL,
			name TEXT NOT NULL,
			status TEXT NOT NULL,
			draft_json TEXT,
			review_state_json TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			last_accessed_at DATETIME NOT NULL
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

	if _, err := s.db.Exec(`ALTER TABLE scenarios ADD COLUMN assignment_json TEXT`); err != nil && !isDuplicateColumnErr(err) {
		return fmt.Errorf("failed to add scenarios.assignment_json column: %w", err)
	}

	if _, err := s.db.Exec(`ALTER TABLE scenarios ADD COLUMN start_delay TEXT`); err != nil && !isDuplicateColumnErr(err) {
		return fmt.Errorf("failed to add scenarios.start_delay column: %w", err)
	}

	if _, err := s.db.Exec(`ALTER TABLE tests ADD COLUMN assignment_json TEXT`); err != nil && !isDuplicateColumnErr(err) {
		return fmt.Errorf("failed to add tests.assignment_json column: %w", err)
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
		{`ALTER TABLE projects ADD COLUMN settings_json TEXT`, "projects.settings_json"},
		{`ALTER TABLE applications ADD COLUMN project_id TEXT REFERENCES projects(id)`, "applications.project_id"},
		{`ALTER TABLE scripts ADD COLUMN project_id TEXT REFERENCES projects(id)`, "scripts.project_id"},
		{`ALTER TABLE scripts ADD COLUMN folder_id TEXT REFERENCES folders(id)`, "scripts.folder_id"},
		{`ALTER TABLE tests ADD COLUMN project_id TEXT REFERENCES projects(id)`, "tests.project_id"},
		{`ALTER TABLE tests ADD COLUMN folder_id TEXT REFERENCES folders(id)`, "tests.folder_id"},
		{`ALTER TABLE tests ADD COLUMN application_id TEXT REFERENCES applications(id)`, "tests.application_id"},
		{`ALTER TABLE tests ADD COLUMN release_id TEXT REFERENCES releases(id)`, "tests.release_id"},
		{`ALTER TABLE tests ADD COLUMN scenario_id TEXT REFERENCES scenarios(id)`, "tests.scenario_id"},
		{`ALTER TABLE tests ADD COLUMN pending_since DATETIME`, "tests.pending_since"},
		{`ALTER TABLE tests ADD COLUMN baseline_pinned INTEGER DEFAULT 0`, "tests.baseline_pinned"},
		{`ALTER TABLE tests ADD COLUMN baseline_pinned_at DATETIME`, "tests.baseline_pinned_at"},
		{`ALTER TABLE shared_data_pools ADD COLUMN project_id TEXT REFERENCES projects(id)`, "shared_data_pools.project_id"},
		{`ALTER TABLE releases ADD COLUMN archived_at DATETIME`, "releases.archived_at"},
		{`ALTER TABLE test_interval_summaries ADD COLUMN schema_version TEXT`, "test_interval_summaries.schema_version"},
		{`ALTER TABLE run_archives ADD COLUMN schema_version TEXT`, "run_archives.schema_version"},
		{`ALTER TABLE test_interval_summaries ADD COLUMN generator_published_at DATETIME`, "test_interval_summaries.generator_published_at"},
		{`ALTER TABLE test_interval_summaries ADD COLUMN controller_received_at DATETIME`, "test_interval_summaries.controller_received_at"},
		{`ALTER TABLE test_interval_summaries ADD COLUMN generator_processing_lag_ms REAL`, "test_interval_summaries.generator_processing_lag_ms"},
		{`ALTER TABLE test_interval_summaries ADD COLUMN transport_lag_ms REAL`, "test_interval_summaries.transport_lag_ms"},
		{`ALTER TABLE test_interval_summaries ADD COLUMN controller_fanout_lag_ms REAL`, "test_interval_summaries.controller_fanout_lag_ms"},
		{`ALTER TABLE test_interval_summaries ADD COLUMN browser_json TEXT`, "test_interval_summaries.browser_json"},
		{`ALTER TABLE test_interval_summaries ADD COLUMN browser_by_device_json TEXT`, "test_interval_summaries.browser_by_device_json"},
		{`ALTER TABLE service_accounts ADD COLUMN active INTEGER NOT NULL DEFAULT 1`, "service_accounts.active"},
		{`ALTER TABLE service_accounts ADD COLUMN expires_at DATETIME`, "service_accounts.expires_at"},
		{`ALTER TABLE service_accounts ADD COLUMN key_rotated_at DATETIME`, "service_accounts.key_rotated_at"},
		{`ALTER TABLE service_accounts ADD COLUMN rotation_interval_days INTEGER`, "service_accounts.rotation_interval_days"},
		{`ALTER TABLE service_accounts ADD COLUMN scopes_json TEXT`, "service_accounts.scopes_json"},
		{`ALTER TABLE scenario_scripts ADD COLUMN stages_json TEXT`, "scenario_scripts.stages_json"},
		{`ALTER TABLE scenario_schedules ADD COLUMN project_id TEXT`, "scenario_schedules.project_id"},
		{`ALTER TABLE scenario_schedules ADD COLUMN owner_subject TEXT`, "scenario_schedules.owner_subject"},
		{`ALTER TABLE scenario_schedules ADD COLUMN owner_display_name TEXT`, "scenario_schedules.owner_display_name"},
		{`ALTER TABLE scenario_schedules ADD COLUMN governance_json TEXT`, "scenario_schedules.governance_json"},
		{`ALTER TABLE scenario_schedules ADD COLUMN blackout_windows_json TEXT`, "scenario_schedules.blackout_windows_json"},
		{`ALTER TABLE scenario_schedules ADD COLUMN availability_windows_json TEXT`, "scenario_schedules.availability_windows_json"},
		{`ALTER TABLE scenario_schedules ADD COLUMN revision INTEGER DEFAULT 1`, "scenario_schedules.revision"},
		{`ALTER TABLE scenario_schedules ADD COLUMN approval_policy TEXT`, "scenario_schedules.approval_policy"},
		{`ALTER TABLE scenario_schedules ADD COLUMN missed_run_policy TEXT`, "scenario_schedules.missed_run_policy"},
		{`ALTER TABLE scenario_schedules ADD COLUMN conflict_summary TEXT`, "scenario_schedules.conflict_summary"},
		{`ALTER TABLE scenario_schedules ADD COLUMN next_required_action TEXT`, "scenario_schedules.next_required_action"},
		{`ALTER TABLE scenario_schedules ADD COLUMN pending_approval_id TEXT`, "scenario_schedules.pending_approval_id"},
	} {
		if _, err := s.db.Exec(migration.query); err != nil && !isDuplicateColumnErr(err) {
			return fmt.Errorf("failed to add %s column: %w", migration.name, err)
		}
	}

	if _, err := s.db.Exec(`UPDATE test_interval_summaries SET schema_version = ? WHERE schema_version IS NULL OR TRIM(schema_version) = ''`, intervalSummarySchemaVersionV1); err != nil {
		return fmt.Errorf("failed to backfill test_interval_summaries.schema_version: %w", err)
	}
	if _, err := s.db.Exec(`UPDATE run_archives SET schema_version = ? WHERE schema_version IS NULL OR TRIM(schema_version) = ''`, archiveRecordSchemaVersion); err != nil {
		return fmt.Errorf("failed to backfill run_archives.schema_version: %w", err)
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
	if _, err := s.db.Exec(`UPDATE applications SET project_id = ? WHERE project_id IS NULL OR project_id = ''`, DefaultProjectID); err != nil {
		return fmt.Errorf("failed to backfill application project ids: %w", err)
	}
	if _, err := s.db.Exec(`UPDATE tests SET project_id = ? WHERE project_id IS NULL OR project_id = ''`, DefaultProjectID); err != nil {
		return fmt.Errorf("failed to backfill test project ids: %w", err)
	}
	if _, err := s.db.Exec(`UPDATE shared_data_pools SET project_id = ? WHERE project_id IS NULL OR project_id = ''`, DefaultProjectID); err != nil {
		return fmt.Errorf("failed to backfill data pool project ids: %w", err)
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
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO applications (id, project_id, name, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, LegacyApplicationID, DefaultProjectID, "Legacy", now, now); err != nil {
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
		Name           string      `json:"name"`
		ScriptID       string      `json:"script_id"`
		VUsers         int         `json:"vusers"`
		RampUp         string      `json:"ramp_up"`
		RampDown       string      `json:"ramp_down"`
		Stages         []LoadStage `json:"stages"`
		Initialization string      `json:"initialization"`
	}
	if err := json.Unmarshal([]byte(groupsJSON), &groups); err != nil {
		return nil
	}
	for i, group := range groups {
		if group.ScriptID == "" {
			continue
		}
		stagesJSON, _ := json.Marshal(group.Stages)
		if _, err := s.db.Exec(`INSERT OR IGNORE INTO scenario_scripts (scenario_id, sort_order, name, script_id, vusers, ramp_up, ramp_down, stages_json, initialization)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, scenarioID, i, group.Name, group.ScriptID, group.VUsers, group.RampUp, group.RampDown, string(stagesJSON), group.Initialization); err != nil {
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
	application.ProjectID = normalizeProjectID(application.ProjectID)
	if strings.TrimSpace(application.Name) == "" {
		return errors.New("application name is required")
	}
	if application.CreatedAt.IsZero() {
		application.CreatedAt = time.Now()
	}
	application.UpdatedAt = time.Now()
	_, err := s.db.Exec(`INSERT INTO applications (id, project_id, name, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET project_id=excluded.project_id, name=excluded.name, updated_at=excluded.updated_at`,
		application.ID, application.ProjectID, application.Name, application.CreatedAt, application.UpdatedAt)
	return err
}

func (s *SQLiteStore) GetApplication(id string) (*Application, error) {
	var application Application
	err := s.db.QueryRow(`SELECT id, COALESCE(project_id, ''), name, created_at, updated_at FROM applications WHERE id = ?`, id).Scan(
		&application.ID, &application.ProjectID, &application.Name, &application.CreatedAt, &application.UpdatedAt)
	if err != nil {
		return nil, err
	}
	application.ProjectID = normalizeProjectID(application.ProjectID)
	return &application, nil
}

func (s *SQLiteStore) ListApplications() ([]*Application, error) {
	rows, err := s.db.Query(`SELECT id, COALESCE(project_id, ''), name, created_at, updated_at FROM applications ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var applications []*Application
	for rows.Next() {
		var application Application
		if err := rows.Scan(&application.ID, &application.ProjectID, &application.Name, &application.CreatedAt, &application.UpdatedAt); err != nil {
			return nil, err
		}
		application.ProjectID = normalizeProjectID(application.ProjectID)
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
	_, err := s.db.Exec(`INSERT INTO releases (id, application_id, name, created_at, updated_at, archived_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET application_id=excluded.application_id, name=excluded.name, updated_at=excluded.updated_at, archived_at=excluded.archived_at`,
		release.ID, release.ApplicationID, release.Name, release.CreatedAt, release.UpdatedAt, release.ArchivedAt)
	return err
}

func (s *SQLiteStore) GetRelease(id string) (*Release, error) {
	var release Release
	err := s.db.QueryRow(`SELECT id, application_id, name, created_at, updated_at, archived_at FROM releases WHERE id = ?`, id).Scan(
		&release.ID, &release.ApplicationID, &release.Name, &release.CreatedAt, &release.UpdatedAt, &release.ArchivedAt)
	if err != nil {
		return nil, err
	}
	return &release, nil
}

func (s *SQLiteStore) ListReleases(applicationID string, includeArchived bool) ([]*Release, error) {
	args := []any{}
	query := `SELECT id, application_id, name, created_at, updated_at, archived_at FROM releases`
	filters := []string{}
	if applicationID != "" {
		filters = append(filters, `application_id = ?`)
		args = append(args, applicationID)
	}
	if !includeArchived {
		filters = append(filters, `archived_at IS NULL`)
	}
	if len(filters) > 0 {
		query += ` WHERE ` + strings.Join(filters, ` AND `)
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
		if err := rows.Scan(&release.ID, &release.ApplicationID, &release.Name, &release.CreatedAt, &release.UpdatedAt, &release.ArchivedAt); err != nil {
			return nil, err
		}
		releases = append(releases, &release)
	}
	return releases, rows.Err()
}

func (s *SQLiteStore) ArchiveRelease(id string) error {
	if id == LegacyReleaseID {
		return errors.New("legacy release cannot be archived")
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM tests WHERE release_id = ?`, id).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return errors.New("release has associated results; archive or delete those results first")
	}
	now := time.Now()
	_, err := s.db.Exec(`UPDATE releases SET archived_at = ?, updated_at = ? WHERE id = ?`, now, now, id)
	return err
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
		return errors.New("release has associated results; archive or delete those results first")
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
	assignmentJSON, _ := json.Marshal(scenario.Assignment)
	goalJSON, _ := json.Marshal(scenario.GoalConfig)
	runtimeJSON, _ := json.Marshal(scenario.RuntimeSettings)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO scenarios (id, name, duration, start_delay, scenario_mode, shakeout_enabled, resource_baseline_enabled, assignment_json, goal_config_json, runtime_settings_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, duration=excluded.duration, start_delay=excluded.start_delay, scenario_mode=excluded.scenario_mode,
		shakeout_enabled=excluded.shakeout_enabled, resource_baseline_enabled=excluded.resource_baseline_enabled,
		assignment_json=excluded.assignment_json, goal_config_json=excluded.goal_config_json, runtime_settings_json=excluded.runtime_settings_json, updated_at=excluded.updated_at`,
		scenario.ID, scenario.Name, scenario.Duration, scenario.StartDelay, scenario.ScenarioMode, scenario.ScriptShakeout, scenario.ResourceBaseline, string(assignmentJSON), string(goalJSON), string(runtimeJSON), scenario.CreatedAt, scenario.UpdatedAt); err != nil {
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
		if _, err := tx.Exec(`INSERT INTO scenario_scripts (scenario_id, sort_order, name, script_id, vusers, ramp_up, ramp_down, stages_json, initialization)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, script.ScenarioID, script.Order, script.Name, script.ScriptID, script.VUsers, script.RampUp, script.RampDown, string(stagesJSON), script.Initialization); err != nil {
			return err
		}
	}
	return tx.Commit()
}

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
	availabilityWindowsJSON, err := json.Marshal(schedule.AvailabilityWindows)
	if err != nil {
		return fmt.Errorf("marshal scenario schedule availability windows: %w", err)
	}
	_, err = s.db.Exec(`INSERT INTO scenario_schedules (id, scenario_id, project_id, revision, name, cron_expr, timezone, release_id, run_name_template, enabled, approval_policy, missed_run_policy, conflict_summary, next_required_action, pending_approval_id, owner_subject, owner_display_name, governance_json, blackout_windows_json, availability_windows_json, last_run_test_id, last_run_at, next_run_at, last_error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET scenario_id=excluded.scenario_id, project_id=excluded.project_id, revision=excluded.revision, name=excluded.name, cron_expr=excluded.cron_expr,
		timezone=excluded.timezone, release_id=excluded.release_id, run_name_template=excluded.run_name_template,
		enabled=excluded.enabled, approval_policy=excluded.approval_policy, missed_run_policy=excluded.missed_run_policy,
		conflict_summary=excluded.conflict_summary, next_required_action=excluded.next_required_action, pending_approval_id=excluded.pending_approval_id,
		owner_subject=excluded.owner_subject, owner_display_name=excluded.owner_display_name,
		governance_json=excluded.governance_json, blackout_windows_json=excluded.blackout_windows_json,
		availability_windows_json=excluded.availability_windows_json, last_run_test_id=excluded.last_run_test_id, last_run_at=excluded.last_run_at,
		next_run_at=excluded.next_run_at, last_error=excluded.last_error, updated_at=excluded.updated_at`,
		schedule.ID, schedule.ScenarioID, schedule.ProjectID, schedule.Revision, schedule.Name, schedule.CronExpr, schedule.Timezone, schedule.ReleaseID, schedule.RunNameTemplate, schedule.Enabled, schedule.ApprovalPolicy, schedule.MissedRunPolicy, schedule.ConflictSummary, schedule.NextRequiredAction, schedule.PendingApprovalID, schedule.OwnerSubject, schedule.OwnerDisplayName, string(governanceJSON), string(blackoutWindowsJSON), string(availabilityWindowsJSON),
		schedule.LastRunTestID, schedule.LastRunAt, schedule.NextRunAt, schedule.LastError, schedule.CreatedAt, schedule.UpdatedAt)
	return err
}

func (s *SQLiteStore) GetScenarioSchedule(id string) (*ScenarioSchedule, error) {
	row := s.db.QueryRow(`SELECT id, scenario_id, COALESCE(project_id, ''), COALESCE(revision, 1), name, cron_expr, COALESCE(timezone, ''), COALESCE(release_id, ''), COALESCE(run_name_template, ''), COALESCE(enabled, 1), COALESCE(approval_policy, ''), COALESCE(missed_run_policy, ''), COALESCE(conflict_summary, ''), COALESCE(next_required_action, ''), COALESCE(pending_approval_id, ''), COALESCE(owner_subject, ''), COALESCE(owner_display_name, ''), COALESCE(governance_json, ''), COALESCE(blackout_windows_json, ''), COALESCE(availability_windows_json, ''), COALESCE(last_run_test_id, ''), last_run_at, next_run_at, COALESCE(last_error, ''), created_at, updated_at FROM scenario_schedules WHERE id = ?`, id)
	return scanScenarioSchedule(row)
}

func (s *SQLiteStore) ListScenarioSchedules(scenarioID string) ([]*ScenarioSchedule, error) {
	rows, err := s.db.Query(`SELECT id, scenario_id, COALESCE(project_id, ''), COALESCE(revision, 1), name, cron_expr, COALESCE(timezone, ''), COALESCE(release_id, ''), COALESCE(run_name_template, ''), COALESCE(enabled, 1), COALESCE(approval_policy, ''), COALESCE(missed_run_policy, ''), COALESCE(conflict_summary, ''), COALESCE(next_required_action, ''), COALESCE(pending_approval_id, ''), COALESCE(owner_subject, ''), COALESCE(owner_display_name, ''), COALESCE(governance_json, ''), COALESCE(blackout_windows_json, ''), COALESCE(availability_windows_json, ''), COALESCE(last_run_test_id, ''), last_run_at, next_run_at, COALESCE(last_error, ''), created_at, updated_at FROM scenario_schedules WHERE scenario_id = ? ORDER BY created_at ASC`, scenarioID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectScenarioSchedules(rows)
}

func (s *SQLiteStore) ListAllScenarioSchedules() ([]*ScenarioSchedule, error) {
	rows, err := s.db.Query(`SELECT id, scenario_id, COALESCE(project_id, ''), COALESCE(revision, 1), name, cron_expr, COALESCE(timezone, ''), COALESCE(release_id, ''), COALESCE(run_name_template, ''), COALESCE(enabled, 1), COALESCE(approval_policy, ''), COALESCE(missed_run_policy, ''), COALESCE(conflict_summary, ''), COALESCE(next_required_action, ''), COALESCE(pending_approval_id, ''), COALESCE(owner_subject, ''), COALESCE(owner_display_name, ''), COALESCE(governance_json, ''), COALESCE(blackout_windows_json, ''), COALESCE(availability_windows_json, ''), COALESCE(last_run_test_id, ''), last_run_at, next_run_at, COALESCE(last_error, ''), created_at, updated_at FROM scenario_schedules ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectScenarioSchedules(rows)
}

func (s *SQLiteStore) DeleteScenarioSchedule(id string) error {
	_, err := s.db.Exec(`DELETE FROM scenario_schedules WHERE id = ?`, id)
	return err
}

func (s *SQLiteStore) SaveScenarioScheduleOccurrence(occurrence *ScenarioScheduleOccurrence) error {
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
	_, err := s.db.Exec(`INSERT INTO scenario_schedule_occurrences (id, schedule_id, scenario_id, project_id, schedule_revision, planned_at, materialized_at, status, reason_code, test_id, approval_id, conflict_summary, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(schedule_id, planned_at) DO UPDATE SET scenario_id=excluded.scenario_id, project_id=excluded.project_id,
		schedule_revision=excluded.schedule_revision, materialized_at=excluded.materialized_at, status=excluded.status,
		reason_code=excluded.reason_code, test_id=excluded.test_id, approval_id=excluded.approval_id,
		conflict_summary=excluded.conflict_summary, updated_at=excluded.updated_at`,
		occurrence.ID, occurrence.ScheduleID, occurrence.ScenarioID, occurrence.ProjectID, occurrence.ScheduleRevision, occurrence.PlannedAt, occurrence.MaterializedAt, occurrence.Status, occurrence.ReasonCode, occurrence.TestID, occurrence.ApprovalID, occurrence.ConflictSummary, occurrence.CreatedAt, occurrence.UpdatedAt)
	return err
}

func (s *SQLiteStore) GetScenarioScheduleOccurrence(id string) (*ScenarioScheduleOccurrence, error) {
	row := s.db.QueryRow(`SELECT id, schedule_id, scenario_id, COALESCE(project_id, ''), COALESCE(schedule_revision, 1), planned_at, materialized_at, status, COALESCE(reason_code, ''), COALESCE(test_id, ''), COALESCE(approval_id, ''), COALESCE(conflict_summary, ''), created_at, updated_at FROM scenario_schedule_occurrences WHERE id = ?`, id)
	return scanScenarioScheduleOccurrence(row)
}

func (s *SQLiteStore) GetScenarioScheduleOccurrenceByScheduleAndPlannedAt(scheduleID string, plannedAt time.Time) (*ScenarioScheduleOccurrence, error) {
	row := s.db.QueryRow(`SELECT id, schedule_id, scenario_id, COALESCE(project_id, ''), COALESCE(schedule_revision, 1), planned_at, materialized_at, status, COALESCE(reason_code, ''), COALESCE(test_id, ''), COALESCE(approval_id, ''), COALESCE(conflict_summary, ''), created_at, updated_at FROM scenario_schedule_occurrences WHERE schedule_id = ? AND planned_at = ?`, scheduleID, plannedAt)
	occurrence, err := scanScenarioScheduleOccurrence(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return occurrence, err
}

func (s *SQLiteStore) ListScenarioScheduleOccurrences(scheduleID string) ([]*ScenarioScheduleOccurrence, error) {
	rows, err := s.db.Query(`SELECT id, schedule_id, scenario_id, COALESCE(project_id, ''), COALESCE(schedule_revision, 1), planned_at, materialized_at, status, COALESCE(reason_code, ''), COALESCE(test_id, ''), COALESCE(approval_id, ''), COALESCE(conflict_summary, ''), created_at, updated_at FROM scenario_schedule_occurrences WHERE schedule_id = ? ORDER BY planned_at DESC`, scheduleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectScenarioScheduleOccurrences(rows)
}

func (s *SQLiteStore) ListAllScenarioScheduleOccurrences() ([]*ScenarioScheduleOccurrence, error) {
	rows, err := s.db.Query(`SELECT id, schedule_id, scenario_id, COALESCE(project_id, ''), COALESCE(schedule_revision, 1), planned_at, materialized_at, status, COALESCE(reason_code, ''), COALESCE(test_id, ''), COALESCE(approval_id, ''), COALESCE(conflict_summary, ''), created_at, updated_at FROM scenario_schedule_occurrences ORDER BY planned_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectScenarioScheduleOccurrences(rows)
}

func (s *SQLiteStore) SaveScenarioScheduleApproval(approval *ScenarioScheduleApproval) error {
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
	_, err := s.db.Exec(`INSERT INTO scenario_schedule_approvals (id, schedule_id, scenario_id, project_id, schedule_revision, policy, status, requested_by, requested_at, resolved_by, resolved_at, decision_reason, required_role, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET scenario_id=excluded.scenario_id, project_id=excluded.project_id,
		schedule_revision=excluded.schedule_revision, policy=excluded.policy, status=excluded.status,
		requested_by=excluded.requested_by, requested_at=excluded.requested_at, resolved_by=excluded.resolved_by,
		resolved_at=excluded.resolved_at, decision_reason=excluded.decision_reason, required_role=excluded.required_role,
		updated_at=excluded.updated_at`,
		approval.ID, approval.ScheduleID, approval.ScenarioID, approval.ProjectID, approval.ScheduleRevision, approval.Policy, approval.Status, approval.RequestedBy, approval.RequestedAt, approval.ResolvedBy, approval.ResolvedAt, approval.DecisionReason, approval.RequiredRole, approval.CreatedAt, approval.UpdatedAt)
	return err
}

func (s *SQLiteStore) GetScenarioScheduleApproval(id string) (*ScenarioScheduleApproval, error) {
	row := s.db.QueryRow(`SELECT id, schedule_id, scenario_id, COALESCE(project_id, ''), COALESCE(schedule_revision, 1), policy, status, COALESCE(requested_by, ''), requested_at, COALESCE(resolved_by, ''), resolved_at, COALESCE(decision_reason, ''), COALESCE(required_role, ''), created_at, updated_at FROM scenario_schedule_approvals WHERE id = ?`, id)
	return scanScenarioScheduleApproval(row)
}

func (s *SQLiteStore) ListScenarioScheduleApprovals(scheduleID string) ([]*ScenarioScheduleApproval, error) {
	rows, err := s.db.Query(`SELECT id, schedule_id, scenario_id, COALESCE(project_id, ''), COALESCE(schedule_revision, 1), policy, status, COALESCE(requested_by, ''), requested_at, COALESCE(resolved_by, ''), resolved_at, COALESCE(decision_reason, ''), COALESCE(required_role, ''), created_at, updated_at FROM scenario_schedule_approvals WHERE schedule_id = ? ORDER BY requested_at DESC`, scheduleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectScenarioScheduleApprovals(rows)
}

func (s *SQLiteStore) TryAcquireSchedulerLease(name string, ownerID string, now time.Time, ttl time.Duration) (bool, error) {
	name = strings.TrimSpace(name)
	ownerID = strings.TrimSpace(ownerID)
	if name == "" {
		return false, errors.New("scheduler lease name is required")
	}
	if ownerID == "" {
		return false, errors.New("scheduler lease owner_id is required")
	}
	if ttl <= 0 {
		return false, errors.New("scheduler lease ttl must be positive")
	}
	now = now.UTC()
	expiresAt := now.Add(ttl)
	result, err := s.db.Exec(`INSERT INTO scheduler_leases (name, owner_id, acquired_at, expires_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET owner_id=excluded.owner_id, acquired_at=excluded.acquired_at,
		expires_at=excluded.expires_at, updated_at=excluded.updated_at
		WHERE scheduler_leases.expires_at <= ? OR scheduler_leases.owner_id = ?`,
		name, ownerID, now, expiresAt, now, now, ownerID)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func (s *SQLiteStore) GetSchedulerLease(name string) (*SchedulerLease, error) {
	var lease SchedulerLease
	err := s.db.QueryRow(`SELECT name, owner_id, acquired_at, expires_at, updated_at FROM scheduler_leases WHERE name = ?`, name).Scan(&lease.Name, &lease.OwnerID, &lease.AcquiredAt, &lease.ExpiresAt, &lease.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &lease, nil
}

func (s *SQLiteStore) GetScenario(id string) (*Scenario, []ScenarioScript, error) {
	var scenario Scenario
	var assignmentJSON, goalJSON, runtimeJSON string
	err := s.db.QueryRow(`SELECT id, name, duration, COALESCE(start_delay, ''), COALESCE(scenario_mode, ''), COALESCE(shakeout_enabled, 0), COALESCE(resource_baseline_enabled, 0), COALESCE(assignment_json, ''), COALESCE(goal_config_json, ''), COALESCE(runtime_settings_json, ''), created_at, updated_at FROM scenarios WHERE id = ?`, id).Scan(
		&scenario.ID, &scenario.Name, &scenario.Duration, &scenario.StartDelay, &scenario.ScenarioMode, &scenario.ScriptShakeout, &scenario.ResourceBaseline, &assignmentJSON, &goalJSON, &runtimeJSON, &scenario.CreatedAt, &scenario.UpdatedAt)
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
	rows, err := s.db.Query(`SELECT id, name, duration, COALESCE(start_delay, ''), COALESCE(scenario_mode, ''), COALESCE(shakeout_enabled, 0), COALESCE(resource_baseline_enabled, 0), COALESCE(assignment_json, ''), COALESCE(goal_config_json, ''), COALESCE(runtime_settings_json, ''), created_at, updated_at FROM scenarios ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var scenarios []*Scenario
	for rows.Next() {
		var scenario Scenario
		var assignmentJSON, goalJSON, runtimeJSON string
		if err := rows.Scan(&scenario.ID, &scenario.Name, &scenario.Duration, &scenario.StartDelay, &scenario.ScenarioMode, &scenario.ScriptShakeout, &scenario.ResourceBaseline, &assignmentJSON, &goalJSON, &runtimeJSON, &scenario.CreatedAt, &scenario.UpdatedAt); err != nil {
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
	rows, err := s.db.Query(`SELECT scenario_id, sort_order, COALESCE(name, ''), script_id, COALESCE(vusers, 0), COALESCE(ramp_up, ''), COALESCE(ramp_down, ''), COALESCE(stages_json, ''), COALESCE(initialization, '') FROM scenario_scripts WHERE scenario_id = ? ORDER BY sort_order ASC`, scenarioID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var scripts []ScenarioScript
	for rows.Next() {
		var script ScenarioScript
		var stagesJSON string
		if err := rows.Scan(&script.ScenarioID, &script.Order, &script.Name, &script.ScriptID, &script.VUsers, &script.RampUp, &script.RampDown, &stagesJSON, &script.Initialization); err != nil {
			return nil, err
		}
		if strings.TrimSpace(stagesJSON) != "" {
			json.Unmarshal([]byte(stagesJSON), &script.Stages)
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

type scanner interface {
	Scan(dest ...any) error
}

func scanScenarioSchedule(row scanner) (*ScenarioSchedule, error) {
	var schedule ScenarioSchedule
	var enabled int
	var governanceJSON string
	var blackoutWindowsJSON string
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
		&availabilityWindowsJSON,
		&schedule.LastRunTestID,
		&schedule.LastRunAt,
		&schedule.NextRunAt,
		&schedule.LastError,
		&schedule.CreatedAt,
		&schedule.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	schedule.Enabled = enabled != 0
	schedule.ProjectID = normalizeProjectID(schedule.ProjectID)
	if schedule.Revision < 1 {
		schedule.Revision = 1
	}
	if governanceJSON != "" && governanceJSON != "null" {
		var governance ScenarioScheduleGovernance
		if err := json.Unmarshal([]byte(governanceJSON), &governance); err == nil {
			schedule.Governance = &governance
		}
	}
	if blackoutWindowsJSON != "" && blackoutWindowsJSON != "null" {
		_ = json.Unmarshal([]byte(blackoutWindowsJSON), &schedule.BlackoutWindows)
	}
	if availabilityWindowsJSON != "" && availabilityWindowsJSON != "null" {
		_ = json.Unmarshal([]byte(availabilityWindowsJSON), &schedule.AvailabilityWindows)
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

func (s *SQLiteStore) SaveDataPool(pool *SharedDataPool) error {
	if pool.ID == "" {
		return errors.New("data pool id is required")
	}
	if strings.TrimSpace(pool.Name) == "" {
		return errors.New("data pool name is required")
	}
	if pool.CreatedAt.IsZero() {
		pool.CreatedAt = time.Now()
	}
	pool.ProjectID = normalizeProjectID(pool.ProjectID)
	pool.UpdatedAt = time.Now()
	schemaJSON, err := json.Marshal(pool.Schema)
	if err != nil {
		return fmt.Errorf("marshal data pool schema: %w", err)
	}
	_, err = s.db.Exec(`INSERT INTO shared_data_pools (id, name, description, schema_json, project_id, created_at, updated_at, archived_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, description=excluded.description, schema_json=excluded.schema_json, project_id=excluded.project_id, updated_at=excluded.updated_at, archived_at=excluded.archived_at`,
		pool.ID, pool.Name, pool.Description, string(schemaJSON), pool.ProjectID, pool.CreatedAt, pool.UpdatedAt, pool.ArchivedAt)
	return err
}

func (s *SQLiteStore) GetDataPool(id string) (*SharedDataPool, error) {
	var pool SharedDataPool
	var schemaJSON string
	err := s.db.QueryRow(`SELECT id, name, COALESCE(description, ''), COALESCE(schema_json, ''), COALESCE(project_id, ''), created_at, updated_at, archived_at FROM shared_data_pools WHERE id = ?`, id).Scan(
		&pool.ID, &pool.Name, &pool.Description, &schemaJSON, &pool.ProjectID, &pool.CreatedAt, &pool.UpdatedAt, &pool.ArchivedAt)
	if err != nil {
		return nil, err
	}
	pool.ProjectID = normalizeProjectID(pool.ProjectID)
	if schemaJSON != "" && schemaJSON != "null" {
		_ = json.Unmarshal([]byte(schemaJSON), &pool.Schema)
	}
	return &pool, nil
}

func (s *SQLiteStore) ListDataPools(includeArchived bool) ([]*SharedDataPool, error) {
	query := `SELECT id, name, COALESCE(description, ''), COALESCE(schema_json, ''), COALESCE(project_id, ''), created_at, updated_at, archived_at FROM shared_data_pools`
	if !includeArchived {
		query += ` WHERE archived_at IS NULL`
	}
	query += ` ORDER BY updated_at DESC`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var pools []*SharedDataPool
	for rows.Next() {
		var pool SharedDataPool
		var schemaJSON string
		if err := rows.Scan(&pool.ID, &pool.Name, &pool.Description, &schemaJSON, &pool.ProjectID, &pool.CreatedAt, &pool.UpdatedAt, &pool.ArchivedAt); err != nil {
			return nil, err
		}
		pool.ProjectID = normalizeProjectID(pool.ProjectID)
		if schemaJSON != "" && schemaJSON != "null" {
			_ = json.Unmarshal([]byte(schemaJSON), &pool.Schema)
		}
		pools = append(pools, &pool)
	}
	return pools, rows.Err()
}

func (s *SQLiteStore) DeleteDataPool(id string) error {
	var bindingCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM scenario_data_pool_bindings WHERE pool_id = ?`, id).Scan(&bindingCount); err != nil {
		return err
	}
	if bindingCount > 0 {
		return errors.New("data pool is bound to one or more scenarios")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM shared_data_pool_rows WHERE pool_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM shared_data_pools WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) SaveDataPoolRow(row *SharedDataPoolRow) error {
	if row.ID == "" {
		return errors.New("data pool row id is required")
	}
	if row.PoolID == "" {
		return errors.New("data pool id is required")
	}
	if row.Data == nil {
		row.Data = map[string]string{}
	}
	if row.Status == "" {
		row.Status = "active"
	}
	if row.Version <= 0 {
		row.Version = 1
	}
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now()
	}
	row.UpdatedAt = time.Now()
	dataJSON, err := json.Marshal(row.Data)
	if err != nil {
		return fmt.Errorf("marshal data pool row: %w", err)
	}
	_, err = s.db.Exec(`INSERT INTO shared_data_pool_rows (id, pool_id, data_json, status, version, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET data_json=excluded.data_json, status=excluded.status, version=shared_data_pool_rows.version + 1, updated_at=excluded.updated_at`,
		row.ID, row.PoolID, string(dataJSON), row.Status, row.Version, row.CreatedAt, row.UpdatedAt)
	return err
}

func (s *SQLiteStore) ListDataPoolRows(poolID string) ([]*SharedDataPoolRow, error) {
	rows, err := s.db.Query(`SELECT id, pool_id, data_json, COALESCE(status, ''), COALESCE(version, 1), created_at, updated_at FROM shared_data_pool_rows WHERE pool_id = ? ORDER BY created_at ASC`, poolID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*SharedDataPoolRow
	for rows.Next() {
		var row SharedDataPoolRow
		var dataJSON string
		if err := rows.Scan(&row.ID, &row.PoolID, &dataJSON, &row.Status, &row.Version, &row.CreatedAt, &row.UpdatedAt); err != nil {
			return nil, err
		}
		if dataJSON != "" && dataJSON != "null" {
			_ = json.Unmarshal([]byte(dataJSON), &row.Data)
		}
		if row.Data == nil {
			row.Data = map[string]string{}
		}
		out = append(out, &row)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) DeleteDataPoolRow(id string) error {
	_, err := s.db.Exec(`DELETE FROM shared_data_pool_rows WHERE id = ?`, id)
	return err
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
	settingsJSON, err := json.Marshal(project.Settings)
	if err != nil {
		return fmt.Errorf("marshal project settings: %w", err)
	}
	_, err = s.db.Exec(`INSERT INTO projects (id, name, settings_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, settings_json=excluded.settings_json, updated_at=excluded.updated_at`,
		project.ID, project.Name, string(settingsJSON), project.CreatedAt, project.UpdatedAt)
	return err
}

func (s *SQLiteStore) GetProject(id string) (*Project, error) {
	var project Project
	var settingsJSON string
	err := s.db.QueryRow(`SELECT id, name, COALESCE(settings_json, ''), created_at, updated_at FROM projects WHERE id = ?`, id).Scan(
		&project.ID, &project.Name, &settingsJSON, &project.CreatedAt, &project.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if settingsJSON != "" && settingsJSON != "null" {
		if err := json.Unmarshal([]byte(settingsJSON), &project.Settings); err != nil {
			return nil, fmt.Errorf("unmarshal project settings: %w", err)
		}
	}
	return &project, nil
}

func (s *SQLiteStore) ListProjects() ([]*Project, error) {
	rows, err := s.db.Query(`SELECT id, name, COALESCE(settings_json, ''), created_at, updated_at FROM projects ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var projects []*Project
	for rows.Next() {
		var project Project
		var settingsJSON string
		if err := rows.Scan(&project.ID, &project.Name, &settingsJSON, &project.CreatedAt, &project.UpdatedAt); err != nil {
			return nil, err
		}
		if settingsJSON != "" && settingsJSON != "null" {
			if err := json.Unmarshal([]byte(settingsJSON), &project.Settings); err != nil {
				return nil, fmt.Errorf("unmarshal project settings: %w", err)
			}
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
		if _, err := tx.Exec(`DELETE FROM test_interval_summaries WHERE test_id = ?`, testID); err != nil {
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

func (s *SQLiteStore) SaveAuthoringSession(session *AuthoringSession) error {
	if session == nil {
		return errors.New("authoring session is required")
	}
	session.ProjectID = normalizeProjectID(session.ProjectID)
	now := time.Now()
	if session.CreatedAt.IsZero() {
		session.CreatedAt = now
	}
	session.UpdatedAt = now
	if session.LastAccessedAt.IsZero() {
		session.LastAccessedAt = now
	}
	draftJSON, err := json.Marshal(session.Draft)
	if err != nil {
		return fmt.Errorf("marshal authoring draft: %w", err)
	}
	reviewStateJSON, err := json.Marshal(session.ReviewState)
	if err != nil {
		return fmt.Errorf("marshal authoring review state: %w", err)
	}
	_, err = s.db.Exec(`INSERT INTO authoring_sessions (id, project_id, source, name, status, draft_json, review_state_json, created_at, updated_at, last_accessed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			project_id=excluded.project_id,
			source=excluded.source,
			name=excluded.name,
			status=excluded.status,
			draft_json=excluded.draft_json,
			review_state_json=excluded.review_state_json,
			updated_at=excluded.updated_at,
			last_accessed_at=excluded.last_accessed_at`,
		session.ID, session.ProjectID, session.Source, session.Name, session.Status, string(draftJSON), string(reviewStateJSON), session.CreatedAt, session.UpdatedAt, session.LastAccessedAt)
	return err
}

func (s *SQLiteStore) GetAuthoringSession(id string) (*AuthoringSession, error) {
	var session AuthoringSession
	var draftJSON, reviewStateJSON string
	err := s.db.QueryRow(`SELECT id, COALESCE(project_id, ''), source, name, status, COALESCE(draft_json, ''), COALESCE(review_state_json, ''), created_at, updated_at, last_accessed_at FROM authoring_sessions WHERE id = ?`, id).Scan(
		&session.ID, &session.ProjectID, &session.Source, &session.Name, &session.Status, &draftJSON, &reviewStateJSON, &session.CreatedAt, &session.UpdatedAt, &session.LastAccessedAt)
	if err != nil {
		return nil, err
	}
	decodeAuthoringSessionJSON(&session, draftJSON, reviewStateJSON)
	now := time.Now()
	if _, err := s.db.Exec(`UPDATE authoring_sessions SET last_accessed_at = ? WHERE id = ?`, now, id); err == nil {
		session.LastAccessedAt = now
	}
	return &session, nil
}

func (s *SQLiteStore) ListAuthoringSessions(projectID string) ([]*AuthoringSession, error) {
	args := []any{}
	query := `SELECT id, COALESCE(project_id, ''), source, name, status, COALESCE(draft_json, ''), COALESCE(review_state_json, ''), created_at, updated_at, last_accessed_at FROM authoring_sessions`
	if projectID != "" {
		query += ` WHERE project_id = ?`
		args = append(args, normalizeProjectID(projectID))
	}
	query += ` ORDER BY updated_at DESC`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sessions := make([]*AuthoringSession, 0)
	for rows.Next() {
		var session AuthoringSession
		var draftJSON, reviewStateJSON string
		if err := rows.Scan(&session.ID, &session.ProjectID, &session.Source, &session.Name, &session.Status, &draftJSON, &reviewStateJSON, &session.CreatedAt, &session.UpdatedAt, &session.LastAccessedAt); err != nil {
			return nil, err
		}
		decodeAuthoringSessionJSON(&session, draftJSON, reviewStateJSON)
		sessions = append(sessions, &session)
	}
	return sessions, nil
}

func (s *SQLiteStore) DeleteAuthoringSession(id string) error {
	_, err := s.db.Exec(`DELETE FROM authoring_sessions WHERE id = ?`, id)
	return err
}

func decodeAuthoringSessionJSON(session *AuthoringSession, draftJSON string, reviewStateJSON string) {
	if draftJSON != "" && draftJSON != "null" {
		var script dsl.Script
		if err := json.Unmarshal([]byte(draftJSON), &script); err == nil {
			session.Draft = &script
		}
	}
	if reviewStateJSON != "" && reviewStateJSON != "null" {
		_ = json.Unmarshal([]byte(reviewStateJSON), &session.ReviewState)
	}
}

func (s *SQLiteStore) SaveTest(test *TestRecord) error {
	test.ApplicationID = normalizeApplicationID(test.ApplicationID)
	test.ReleaseID = normalizeReleaseID(test.ReleaseID)
	test.ProjectID = normalizeProjectID(test.ProjectID)
	groupsJSON, _ := json.Marshal(test.Groups)
	assignmentJSON, _ := json.Marshal(test.Assignment)
	runtimeSettingsJSON, _ := json.Marshal(test.RuntimeSettings)
	goalConfigJSON, _ := json.Marshal(test.GoalConfig)

	query := `INSERT INTO tests (id, name, status, application_id, release_id, scenario_id, project_id, folder_id, scenario_mode, shakeout_enabled, resource_baseline_enabled, assignment_json, total_vusers, duration_ns, start_time, pending_since, baseline_pinned, baseline_pinned_at, end_time, error, groups_json, runtime_settings_json, goal_config_json)
			  VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			  ON CONFLICT(id) DO UPDATE SET
			  name=excluded.name, status=excluded.status, application_id=excluded.application_id, release_id=excluded.release_id, scenario_id=excluded.scenario_id,
			  project_id=excluded.project_id, folder_id=excluded.folder_id,
			  scenario_mode=excluded.scenario_mode, shakeout_enabled=excluded.shakeout_enabled,
			  resource_baseline_enabled=excluded.resource_baseline_enabled, assignment_json=excluded.assignment_json, total_vusers=excluded.total_vusers,
			  duration_ns=excluded.duration_ns, start_time=excluded.start_time, pending_since=excluded.pending_since,
			  baseline_pinned=excluded.baseline_pinned, baseline_pinned_at=excluded.baseline_pinned_at, end_time=excluded.end_time,
			  error=excluded.error, groups_json=excluded.groups_json,
			  runtime_settings_json=excluded.runtime_settings_json, goal_config_json=excluded.goal_config_json`

	_, err := s.db.Exec(query, test.ID, test.Name, test.Status, test.ApplicationID, test.ReleaseID, test.ScenarioID, test.ProjectID, test.FolderID, test.ScenarioMode, test.ScriptShakeout, test.ResourceBaseline, string(assignmentJSON), test.TotalVUsers, test.Duration, test.StartTime, test.PendingSince, test.BaselinePinned, test.BaselinePinnedAt, test.EndTime, test.Error, string(groupsJSON), string(runtimeSettingsJSON), string(goalConfigJSON))
	return err
}

func (s *SQLiteStore) GetTest(id string) (*TestRecord, error) {
	var test TestRecord
	var groupsJSON, assignmentJSON, runtimeSettingsJSON, goalConfigJSON string

	query := `SELECT id, name, status, COALESCE(application_id, ''), COALESCE(release_id, ''), COALESCE(scenario_id, ''), COALESCE(project_id, ''), folder_id, COALESCE(scenario_mode, ''), COALESCE(shakeout_enabled, 0), COALESCE(resource_baseline_enabled, 0), COALESCE(assignment_json, ''), total_vusers, duration_ns, start_time, pending_since, COALESCE(baseline_pinned, 0), baseline_pinned_at, end_time, error, groups_json, COALESCE(runtime_settings_json, ''), COALESCE(goal_config_json, '') FROM tests WHERE id = ?`
	err := s.db.QueryRow(query, id).Scan(&test.ID, &test.Name, &test.Status, &test.ApplicationID, &test.ReleaseID, &test.ScenarioID, &test.ProjectID, &test.FolderID, &test.ScenarioMode, &test.ScriptShakeout, &test.ResourceBaseline, &assignmentJSON, &test.TotalVUsers, &test.Duration, &test.StartTime, &test.PendingSince, &test.BaselinePinned, &test.BaselinePinnedAt, &test.EndTime, &test.Error, &groupsJSON, &runtimeSettingsJSON, &goalConfigJSON)
	if err != nil {
		return nil, err
	}

	json.Unmarshal([]byte(groupsJSON), &test.Groups)
	if assignmentJSON != "" && assignmentJSON != "null" {
		json.Unmarshal([]byte(assignmentJSON), &test.Assignment)
	}
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
	query := `SELECT id, name, status, COALESCE(application_id, ''), COALESCE(release_id, ''), COALESCE(scenario_id, ''), COALESCE(project_id, ''), folder_id, COALESCE(scenario_mode, ''), COALESCE(shakeout_enabled, 0), COALESCE(resource_baseline_enabled, 0), COALESCE(assignment_json, ''), total_vusers, duration_ns, start_time, pending_since, COALESCE(baseline_pinned, 0), baseline_pinned_at, end_time, error, groups_json, COALESCE(runtime_settings_json, ''), COALESCE(goal_config_json, '') FROM tests`
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
		var groupsJSON, assignmentJSON, runtimeSettingsJSON, goalConfigJSON string
		if err := rows.Scan(&test.ID, &test.Name, &test.Status, &test.ApplicationID, &test.ReleaseID, &test.ScenarioID, &test.ProjectID, &test.FolderID, &test.ScenarioMode, &test.ScriptShakeout, &test.ResourceBaseline, &assignmentJSON, &test.TotalVUsers, &test.Duration, &test.StartTime, &test.PendingSince, &test.BaselinePinned, &test.BaselinePinnedAt, &test.EndTime, &test.Error, &groupsJSON, &runtimeSettingsJSON, &goalConfigJSON); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(groupsJSON), &test.Groups)
		if assignmentJSON != "" && assignmentJSON != "null" {
			json.Unmarshal([]byte(assignmentJSON), &test.Assignment)
		}
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
	query := `SELECT id, name, status, COALESCE(application_id, ''), COALESCE(release_id, ''), COALESCE(scenario_id, ''), COALESCE(project_id, ''), folder_id, COALESCE(scenario_mode, ''), COALESCE(shakeout_enabled, 0), COALESCE(resource_baseline_enabled, 0), COALESCE(assignment_json, ''), total_vusers, duration_ns, start_time, pending_since, COALESCE(baseline_pinned, 0), baseline_pinned_at, end_time, error, groups_json, COALESCE(runtime_settings_json, ''), COALESCE(goal_config_json, '') FROM tests`
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
		var groupsJSON, assignmentJSON, runtimeSettingsJSON, goalConfigJSON string
		if err := rows.Scan(&test.ID, &test.Name, &test.Status, &test.ApplicationID, &test.ReleaseID, &test.ScenarioID, &test.ProjectID, &test.FolderID, &test.ScenarioMode, &test.ScriptShakeout, &test.ResourceBaseline, &assignmentJSON, &test.TotalVUsers, &test.Duration, &test.StartTime, &test.PendingSince, &test.BaselinePinned, &test.BaselinePinnedAt, &test.EndTime, &test.Error, &groupsJSON, &runtimeSettingsJSON, &goalConfigJSON); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(groupsJSON), &test.Groups)
		if assignmentJSON != "" && assignmentJSON != "null" {
			json.Unmarshal([]byte(assignmentJSON), &test.Assignment)
		}
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
	if _, err := tx.Exec(`DELETE FROM test_interval_summaries WHERE test_id = ?`, id); err != nil {
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
			browser_json, browser_by_device_json,
			dropped_results, websocket_clients, websocket_dropped_messages, websocket_dropped_events,
			generator_published_at, controller_received_at, collected_at, published_at,
			generator_processing_lag_ms, transport_lag_ms, controller_fanout_lag_ms, sample_age_ms
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
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
		return err
	}

	metricsJSON, _ := json.Marshal(metrics)
	_, err = s.db.Exec(`INSERT INTO test_history (test_id, timestamp, metrics_json) VALUES (?, ?, ?)`, testID, timestamp, string(metricsJSON))
	return err
}

func (s *SQLiteStore) GetHistory(testID string) ([]any, error) {
	typedHistory, err := s.getTypedHistory(testID)
	if err != nil {
		return nil, err
	}
	if len(typedHistory) > 0 {
		return typedHistory, nil
	}

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

func (s *SQLiteStore) getTypedHistory(testID string) ([]any, error) {
	rows, err := s.db.Query(`SELECT
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
	FROM test_interval_summaries WHERE test_id = ? ORDER BY timestamp ASC`, testID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	history := make([]any, 0)
	for rows.Next() {
		var record IntervalSummaryRecord
		var browserJSON sql.NullString
		var browserByDeviceJSON sql.NullString
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
				AvgBlockingResources: metrics.AvgBlockingResources,
				AvgLongTaskCount:     metrics.AvgLongTaskCount,
				AvgJSHeapUsedMB:      metrics.AvgJSHeapUsedMB,
				AvgBrowserProcessCPU: metrics.AvgBrowserProcessCPU,
				AvgBrowserProcessMem: metrics.AvgBrowserProcessMem,
			}
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

func (s *SQLiteStore) SaveRunArchive(record *RunArchiveRecord) error {
	if record == nil {
		return errors.New("run archive record is nil")
	}
	record.SchemaVersion = normalizeTelemetrySchemaVersion(record.SchemaVersion, archiveRecordSchemaVersion)
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
	record.SchemaVersion = normalizeTelemetrySchemaVersion(record.SchemaVersion, archiveRecordSchemaVersion)
	return &record, nil
}

func normalizeTelemetrySchemaVersion(version string, fallback string) string {
	if strings.TrimSpace(version) == "" {
		return fallback
	}
	return version
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

func (s *SQLiteStore) SaveServiceAccount(account *ServiceAccount) error {
	if account == nil {
		return fmt.Errorf("service account is required")
	}
	globalRolesJSON, err := json.Marshal(account.GlobalRoles)
	if err != nil {
		return err
	}
	projectRolesJSON, err := json.Marshal(account.ProjectRoles)
	if err != nil {
		return err
	}
	scopesJSON, err := json.Marshal(account.Scopes)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO service_accounts (id, name, description, key_hash, active, global_roles_json, project_roles_json, scopes_json, created_at, updated_at, last_used_at, expires_at, key_rotated_at, rotation_interval_days)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name=excluded.name,
			description=excluded.description,
			key_hash=excluded.key_hash,
			active=excluded.active,
			global_roles_json=excluded.global_roles_json,
			project_roles_json=excluded.project_roles_json,
			scopes_json=excluded.scopes_json,
			updated_at=excluded.updated_at,
			last_used_at=excluded.last_used_at,
			expires_at=excluded.expires_at,
			key_rotated_at=excluded.key_rotated_at,
			rotation_interval_days=excluded.rotation_interval_days`,
		account.ID,
		account.Name,
		account.Description,
		account.KeyHash,
		boolToSQLite(account.Active),
		string(globalRolesJSON),
		string(projectRolesJSON),
		string(scopesJSON),
		account.CreatedAt,
		account.UpdatedAt,
		account.LastUsedAt,
		account.ExpiresAt,
		account.KeyRotatedAt,
		account.RotationIntervalDays,
	)
	return err
}

func (s *SQLiteStore) GetServiceAccount(id string) (*ServiceAccount, error) {
	return s.getServiceAccountByQuery(serviceAccountSelectColumns()+` FROM service_accounts WHERE id = ?`, id)
}

func (s *SQLiteStore) GetServiceAccountByKeyHash(keyHash string) (*ServiceAccount, error) {
	return s.getServiceAccountByQuery(serviceAccountSelectColumns()+` FROM service_accounts WHERE key_hash = ? AND active = 1 AND (expires_at IS NULL OR expires_at > ?)`, keyHash, time.Now().UTC())
}

func serviceAccountSelectColumns() string {
	return `SELECT id, name, description, key_hash, active, global_roles_json, project_roles_json, COALESCE(scopes_json, ''), created_at, updated_at, last_used_at, expires_at, key_rotated_at, rotation_interval_days`
}

func (s *SQLiteStore) getServiceAccountByQuery(query string, args ...any) (*ServiceAccount, error) {
	var account ServiceAccount
	var active int
	var globalRolesJSON string
	var projectRolesJSON string
	var scopesJSON string
	var lastUsedAt sql.NullTime
	var expiresAt sql.NullTime
	var keyRotatedAt sql.NullTime
	var rotationIntervalDays sql.NullInt64
	err := s.db.QueryRow(query, args...).Scan(
		&account.ID,
		&account.Name,
		&account.Description,
		&account.KeyHash,
		&active,
		&globalRolesJSON,
		&projectRolesJSON,
		&scopesJSON,
		&account.CreatedAt,
		&account.UpdatedAt,
		&lastUsedAt,
		&expiresAt,
		&keyRotatedAt,
		&rotationIntervalDays,
	)
	if err != nil {
		return nil, err
	}
	account.Active = active != 0
	if lastUsedAt.Valid {
		account.LastUsedAt = &lastUsedAt.Time
	}
	if expiresAt.Valid {
		account.ExpiresAt = &expiresAt.Time
	}
	if keyRotatedAt.Valid {
		account.KeyRotatedAt = &keyRotatedAt.Time
	}
	if rotationIntervalDays.Valid {
		account.RotationIntervalDays = int(rotationIntervalDays.Int64)
	}
	if err := json.Unmarshal([]byte(globalRolesJSON), &account.GlobalRoles); err != nil && strings.TrimSpace(globalRolesJSON) != "" {
		return nil, err
	}
	if err := json.Unmarshal([]byte(projectRolesJSON), &account.ProjectRoles); err != nil && strings.TrimSpace(projectRolesJSON) != "" {
		return nil, err
	}
	if err := json.Unmarshal([]byte(scopesJSON), &account.Scopes); err != nil && strings.TrimSpace(scopesJSON) != "" {
		return nil, err
	}
	if account.ProjectRoles == nil {
		account.ProjectRoles = map[string][]string{}
	}
	populateServiceAccountDerivedFields(&account)
	return &account, nil
}

func (s *SQLiteStore) ListServiceAccounts() ([]*ServiceAccount, error) {
	rows, err := s.db.Query(serviceAccountSelectColumns() + ` FROM service_accounts ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	accounts := []*ServiceAccount{}
	for rows.Next() {
		var account ServiceAccount
		var active int
		var globalRolesJSON string
		var projectRolesJSON string
		var scopesJSON string
		var lastUsedAt sql.NullTime
		var expiresAt sql.NullTime
		var keyRotatedAt sql.NullTime
		var rotationIntervalDays sql.NullInt64
		if err := rows.Scan(&account.ID, &account.Name, &account.Description, &account.KeyHash, &active, &globalRolesJSON, &projectRolesJSON, &scopesJSON, &account.CreatedAt, &account.UpdatedAt, &lastUsedAt, &expiresAt, &keyRotatedAt, &rotationIntervalDays); err != nil {
			return nil, err
		}
		account.Active = active != 0
		if lastUsedAt.Valid {
			account.LastUsedAt = &lastUsedAt.Time
		}
		if expiresAt.Valid {
			account.ExpiresAt = &expiresAt.Time
		}
		if keyRotatedAt.Valid {
			account.KeyRotatedAt = &keyRotatedAt.Time
		}
		if rotationIntervalDays.Valid {
			account.RotationIntervalDays = int(rotationIntervalDays.Int64)
		}
		if err := json.Unmarshal([]byte(globalRolesJSON), &account.GlobalRoles); err != nil && strings.TrimSpace(globalRolesJSON) != "" {
			return nil, err
		}
		if err := json.Unmarshal([]byte(projectRolesJSON), &account.ProjectRoles); err != nil && strings.TrimSpace(projectRolesJSON) != "" {
			return nil, err
		}
		if err := json.Unmarshal([]byte(scopesJSON), &account.Scopes); err != nil && strings.TrimSpace(scopesJSON) != "" {
			return nil, err
		}
		if account.ProjectRoles == nil {
			account.ProjectRoles = map[string][]string{}
		}
		populateServiceAccountDerivedFields(&account)
		accounts = append(accounts, &account)
	}
	return accounts, rows.Err()
}

func (s *SQLiteStore) RotateServiceAccountKey(id string, keyHash string, updatedAt time.Time) error {
	_, err := s.db.Exec(`UPDATE service_accounts SET key_hash = ?, updated_at = ?, key_rotated_at = ? WHERE id = ?`, keyHash, updatedAt, updatedAt, id)
	return err
}

func populateServiceAccountDerivedFields(account *ServiceAccount) {
	if account == nil || account.RotationIntervalDays <= 0 {
		return
	}
	anchor := account.CreatedAt
	if account.KeyRotatedAt != nil {
		anchor = *account.KeyRotatedAt
	}
	due := anchor.AddDate(0, 0, account.RotationIntervalDays)
	account.RotationDueAt = &due
}

func (s *SQLiteStore) UpdateServiceAccountLastUsed(id string, lastUsedAt time.Time) error {
	_, err := s.db.Exec(`UPDATE service_accounts SET last_used_at = ?, updated_at = ? WHERE id = ?`, lastUsedAt, lastUsedAt, id)
	return err
}

func (s *SQLiteStore) UpdateServiceAccountActive(id string, active bool, updatedAt time.Time) error {
	_, err := s.db.Exec(`UPDATE service_accounts SET active = ?, updated_at = ? WHERE id = ?`, boolToSQLite(active), updatedAt, id)
	return err
}

func (s *SQLiteStore) DeleteServiceAccount(id string) error {
	_, err := s.db.Exec(`DELETE FROM service_accounts WHERE id = ?`, id)
	return err
}

func (s *SQLiteStore) SaveAuditEvent(event *AuditEvent) error {
	if event == nil {
		return fmt.Errorf("audit event is required")
	}
	detailsJSON, err := json.Marshal(event.Details)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO audit_events (id, action, actor_type, actor_subject, actor_display_name, project_id, resource_type, resource_id, status, details_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID,
		event.Action,
		event.ActorType,
		event.ActorSubject,
		event.ActorDisplayName,
		event.ProjectID,
		event.ResourceType,
		event.ResourceID,
		event.Status,
		string(detailsJSON),
		event.CreatedAt,
	)
	return err
}

func (s *SQLiteStore) ListAuditEvents(filter AuditEventFilter) ([]*AuditEvent, error) {
	if filter.Limit <= 0 {
		filter.Limit = 100
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	query := strings.Builder{}
	query.WriteString(`SELECT id, action, actor_type, actor_subject, actor_display_name, project_id, resource_type, resource_id, status, details_json, created_at FROM audit_events WHERE 1 = 1`)
	args := make([]any, 0, 8)
	appendFilter := func(column string, value string) {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return
		}
		query.WriteString(` AND ` + column + ` = ?`)
		args = append(args, trimmed)
	}
	appendFilter("action", filter.Action)
	appendFilter("actor_type", filter.ActorType)
	appendFilter("project_id", filter.ProjectID)
	appendFilter("resource_type", filter.ResourceType)
	appendFilter("resource_id", filter.ResourceID)
	appendFilter("status", filter.Status)
	query.WriteString(` ORDER BY created_at DESC LIMIT ? OFFSET ?`)
	args = append(args, filter.Limit, filter.Offset)
	rows, err := s.db.Query(query.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []*AuditEvent{}
	for rows.Next() {
		var event AuditEvent
		var detailsJSON string
		if err := rows.Scan(&event.ID, &event.Action, &event.ActorType, &event.ActorSubject, &event.ActorDisplayName, &event.ProjectID, &event.ResourceType, &event.ResourceID, &event.Status, &detailsJSON, &event.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(detailsJSON), &event.Details); err != nil && strings.TrimSpace(detailsJSON) != "" {
			return nil, err
		}
		events = append(events, &event)
	}
	return events, rows.Err()
}

func (s *SQLiteStore) SaveUsageAccountingSnapshot(snapshot *UsageAccountingSnapshot) error {
	if snapshot == nil {
		return fmt.Errorf("usage accounting snapshot is required")
	}
	if strings.TrimSpace(snapshot.ID) == "" {
		return fmt.Errorf("usage accounting snapshot id is required")
	}
	if strings.TrimSpace(snapshot.PayloadJSON) == "" {
		return fmt.Errorf("usage accounting snapshot payload is required")
	}
	snapshot.ProjectID = strings.TrimSpace(snapshot.ProjectID)
	if snapshot.GeneratedAt.IsZero() {
		snapshot.GeneratedAt = time.Now().UTC()
	}
	if snapshot.CreatedAt.IsZero() {
		snapshot.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.Exec(`INSERT INTO usage_accounting_snapshots (id, project_id, generated_at, payload_json, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET project_id=excluded.project_id, generated_at=excluded.generated_at, payload_json=excluded.payload_json, created_at=excluded.created_at`,
		snapshot.ID,
		snapshot.ProjectID,
		snapshot.GeneratedAt,
		snapshot.PayloadJSON,
		snapshot.CreatedAt,
	)
	return err
}

func (s *SQLiteStore) ListUsageAccountingSnapshots(projectID string, limit int, offset int) ([]*UsageAccountingSnapshot, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}
	query := `SELECT id, project_id, generated_at, payload_json, created_at FROM usage_accounting_snapshots`
	args := []any{}
	if strings.TrimSpace(projectID) != "" {
		query += ` WHERE project_id = ?`
		args = append(args, normalizeProjectID(projectID))
	}
	query += ` ORDER BY generated_at DESC, created_at DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	snapshots := []*UsageAccountingSnapshot{}
	for rows.Next() {
		var snapshot UsageAccountingSnapshot
		if err := rows.Scan(&snapshot.ID, &snapshot.ProjectID, &snapshot.GeneratedAt, &snapshot.PayloadJSON, &snapshot.CreatedAt); err != nil {
			return nil, err
		}
		snapshots = append(snapshots, &snapshot)
	}
	return snapshots, rows.Err()
}

func boolToSQLite(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}
