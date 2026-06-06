package storage

import (
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jasonczech/apexload/pkg/dsl"
	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	db          *storeDB
	backend     StoreBackendInfo
	timescaleDB bool
}

type PostgresStore struct {
	*SQLiteStore
}

type StoreBackendInfo struct {
	Type               string `json:"type"`
	Driver             string `json:"driver"`
	MigrationVersion   string `json:"migration_version"`
	LeaseCapability    bool   `json:"lease_capability"`
	TimescaleDBEnabled bool   `json:"timescaledb_enabled"`
	DSN                string `json:"-"`
}

type storeDialect string

const (
	storeDialectSQLite   storeDialect = "sqlite"
	storeDialectPostgres storeDialect = "postgres"
)

type storeDB struct {
	db      *sql.DB
	dialect storeDialect
}

type storeTx struct {
	tx      *sql.Tx
	dialect storeDialect
}

type optionalSchemaMigration struct {
	name  string
	query string
}

const (
	intervalSummarySchemaVersionV1 = "apexload.interval-summary.v1"
	intervalSummarySchemaVersion   = "apexload.interval-summary.v4"
	archiveRecordSchemaVersion     = "apexload.archive.v1"
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

type NetworkLatencySummaryRecord struct {
	Targets []NetworkLatencyTargetRecord `json:"targets"`
}

type NetworkLatencyTargetRecord struct {
	Name           string    `json:"name"`
	Endpoint       string    `json:"endpoint"`
	LatencyMs      float64   `json:"latency_ms"`
	ProbeTotalMs   float64   `json:"probe_total_ms,omitempty"`
	DNSMs          float64   `json:"dns_ms,omitempty"`
	ConnectMs      float64   `json:"connect_ms,omitempty"`
	TLSMs          float64   `json:"tls_ms,omitempty"`
	TTFBMs         float64   `json:"ttfb_ms,omitempty"`
	HTTPStatus     int       `json:"http_status,omitempty"`
	ProbeMethod    string    `json:"probe_method,omitempty"`
	IP             string    `json:"ip,omitempty"`
	Hops           int       `json:"hops,omitempty"`
	LastSuccess    bool      `json:"last_success"`
	JitterMs       float64   `json:"jitter_ms,omitempty"`
	SuccessRatePct float64   `json:"success_rate_pct,omitempty"`
	SampleCount    int       `json:"sample_count,omitempty"`
	RouteStatus    string    `json:"route_status,omitempty"`
	Error          string    `json:"error,omitempty"`
	Timestamp      time.Time `json:"timestamp"`
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
	Latency                  *NetworkLatencySummaryRecord           `json:"latency,omitempty"`
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

	s := newSQLStore(db, storeDialectSQLite, StoreBackendInfo{
		Type:             "sqlite",
		Driver:           "modernc.org/sqlite",
		MigrationVersion: archiveRecordSchemaVersion,
		LeaseCapability:  true,
		DSN:              path,
	})
	if err := s.init(); err != nil {
		_ = db.Close()
		return nil, err
	}

	return s, nil
}

func NewPostgresStore(databaseURL string, options StoreConnectionOptions, timescaleDB bool) (*PostgresStore, error) {
	databaseURL = strings.TrimSpace(databaseURL)
	if databaseURL == "" {
		return nil, errors.New("postgres database url is required")
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to open postgres db: %w", err)
	}
	applyConnectionOptions(db, options)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to ping postgres db: %w", err)
	}
	s := newSQLStore(db, storeDialectPostgres, StoreBackendInfo{
		Type:               "postgres",
		Driver:             "github.com/jackc/pgx/v5/stdlib",
		MigrationVersion:   archiveRecordSchemaVersion,
		LeaseCapability:    true,
		TimescaleDBEnabled: timescaleDB,
		DSN:                databaseURL,
	})
	if err := s.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &PostgresStore{SQLiteStore: s}, nil
}

type StoreConnectionOptions struct {
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

func applyConnectionOptions(db *sql.DB, options StoreConnectionOptions) {
	if db == nil {
		return
	}
	if options.MaxOpenConns > 0 {
		db.SetMaxOpenConns(options.MaxOpenConns)
	}
	if options.MaxIdleConns > 0 {
		db.SetMaxIdleConns(options.MaxIdleConns)
	}
	if options.ConnMaxLifetime > 0 {
		db.SetConnMaxLifetime(options.ConnMaxLifetime)
	}
}

func newSQLStore(db *sql.DB, dialect storeDialect, info StoreBackendInfo) *SQLiteStore {
	return &SQLiteStore{
		db: &storeDB{
			db:      db,
			dialect: dialect,
		},
		backend:     info,
		timescaleDB: info.TimescaleDBEnabled,
	}
}

func (s *SQLiteStore) BackendInfo() StoreBackendInfo {
	if s == nil {
		return StoreBackendInfo{}
	}
	return s.backend
}

func (s *PostgresStore) BackendInfo() StoreBackendInfo {
	if s == nil || s.SQLiteStore == nil {
		return StoreBackendInfo{}
	}
	return s.SQLiteStore.BackendInfo()
}

func (db *storeDB) Exec(query string, args ...any) (sql.Result, error) {
	return db.db.Exec(rewriteSQL(query, db.dialect), args...)
}

func (db *storeDB) Query(query string, args ...any) (*sql.Rows, error) {
	return db.db.Query(rewriteSQL(query, db.dialect), args...)
}

func (db *storeDB) QueryRow(query string, args ...any) *sql.Row {
	return db.db.QueryRow(rewriteSQL(query, db.dialect), args...)
}

func (db *storeDB) Begin() (*storeTx, error) {
	tx, err := db.db.Begin()
	if err != nil {
		return nil, err
	}
	return &storeTx{tx: tx, dialect: db.dialect}, nil
}

func (db *storeDB) Close() error {
	return db.db.Close()
}

func (db *storeDB) Ping() error {
	return db.db.Ping()
}

func (tx *storeTx) Exec(query string, args ...any) (sql.Result, error) {
	return tx.tx.Exec(rewriteSQL(query, tx.dialect), args...)
}

func (tx *storeTx) Query(query string, args ...any) (*sql.Rows, error) {
	return tx.tx.Query(rewriteSQL(query, tx.dialect), args...)
}

func (tx *storeTx) QueryRow(query string, args ...any) *sql.Row {
	return tx.tx.QueryRow(rewriteSQL(query, tx.dialect), args...)
}

func (tx *storeTx) Commit() error {
	return tx.tx.Commit()
}

func (tx *storeTx) Rollback() error {
	return tx.tx.Rollback()
}

var (
	insertOrIgnorePattern = regexp.MustCompile(`(?is)^\s*INSERT\s+OR\s+IGNORE\s+INTO\s+(.+)$`)
	inlineReferences      = regexp.MustCompile(`(?i)\s+REFERENCES\s+[a-zA-Z_][a-zA-Z0-9_]*\s*\([^)]*\)`)
	trailingCreateComma   = regexp.MustCompile(`,\s*\)`)
)

func rewriteSQL(query string, dialect storeDialect) string {
	if dialect != storeDialectPostgres {
		return query
	}
	rewritten := query
	rewritten = strings.ReplaceAll(rewritten, "DATETIME", "TIMESTAMPTZ")
	rewritten = strings.ReplaceAll(rewritten, "datetime", "TIMESTAMPTZ")
	if strings.Contains(strings.ToUpper(rewritten), "CREATE TABLE") || strings.Contains(strings.ToUpper(rewritten), "ALTER TABLE") {
		rewritten = stripPostgresForeignKeyReferences(rewritten)
	}
	for _, replacement := range []struct {
		old string
		new string
	}{
		{"shakeout_enabled INTEGER DEFAULT 0", "shakeout_enabled BOOLEAN DEFAULT FALSE"},
		{"resource_baseline_enabled INTEGER DEFAULT 0", "resource_baseline_enabled BOOLEAN DEFAULT FALSE"},
		{"enabled INTEGER DEFAULT 1", "enabled BOOLEAN DEFAULT TRUE"},
		{"baseline_pinned INTEGER DEFAULT 0", "baseline_pinned BOOLEAN DEFAULT FALSE"},
		{"active INTEGER NOT NULL DEFAULT 1", "active BOOLEAN NOT NULL DEFAULT TRUE"},
		{"ADD COLUMN active INTEGER NOT NULL DEFAULT 1", "ADD COLUMN active BOOLEAN NOT NULL DEFAULT TRUE"},
		{"COALESCE(shakeout_enabled, 0)", "COALESCE(shakeout_enabled, FALSE)"},
		{"COALESCE(resource_baseline_enabled, 0)", "COALESCE(resource_baseline_enabled, FALSE)"},
		{"COALESCE(enabled, 1)", "COALESCE(enabled, TRUE)"},
		{"COALESCE(baseline_pinned, 0)", "COALESCE(baseline_pinned, FALSE)"},
		{" active = 1", " active = TRUE"},
	} {
		rewritten = strings.ReplaceAll(rewritten, replacement.old, replacement.new)
	}
	if matches := insertOrIgnorePattern.FindStringSubmatch(rewritten); len(matches) == 2 {
		rewritten = "INSERT INTO " + matches[1] + " ON CONFLICT DO NOTHING"
	}
	return rewriteQuestionPlaceholders(rewritten)
}

func stripPostgresForeignKeyReferences(query string) string {
	lines := strings.Split(query, "\n")
	filtered := lines[:0]
	for _, line := range lines {
		if strings.Contains(strings.ToUpper(line), "FOREIGN KEY") {
			continue
		}
		filtered = append(filtered, inlineReferences.ReplaceAllString(line, ""))
	}
	query = strings.Join(filtered, "\n")
	for trailingCreateComma.MatchString(query) {
		query = trailingCreateComma.ReplaceAllString(query, ")")
	}
	return query
}

func rewriteQuestionPlaceholders(query string) string {
	var builder strings.Builder
	builder.Grow(len(query) + 8)
	placeholder := 1
	inSingleQuote := false
	for i := 0; i < len(query); i++ {
		ch := query[i]
		if ch == '\'' {
			builder.WriteByte(ch)
			if inSingleQuote && i+1 < len(query) && query[i+1] == '\'' {
				i++
				builder.WriteByte(query[i])
				continue
			}
			inSingleQuote = !inSingleQuote
			continue
		}
		if ch == '?' && !inSingleQuote {
			builder.WriteByte('$')
			builder.WriteString(strconv.Itoa(placeholder))
			placeholder++
			continue
		}
		builder.WriteByte(ch)
	}
	return builder.String()
}

func (s *SQLiteStore) init() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS domains (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS application_repositories (
			id TEXT PRIMARY KEY,
			domain_id TEXT NOT NULL,
			name TEXT NOT NULL,
			description TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			UNIQUE(domain_id),
			FOREIGN KEY(domain_id) REFERENCES domains(id)
		)`,
		`CREATE TABLE IF NOT EXISTS repository_categories (
			id TEXT PRIMARY KEY,
			domain_id TEXT NOT NULL,
			repository_id TEXT NOT NULL,
			name TEXT NOT NULL,
			description TEXT,
			sort_order INTEGER DEFAULT 0,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY(domain_id) REFERENCES domains(id),
			FOREIGN KEY(repository_id) REFERENCES application_repositories(id)
		)`,
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
			project_id TEXT REFERENCES projects(id),
			name TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			archived_at DATETIME,
			FOREIGN KEY(application_id) REFERENCES applications(id)
		)`,
		`CREATE TABLE IF NOT EXISTS release_sla_waivers (
			id TEXT PRIMARY KEY,
			release_id TEXT NOT NULL,
			project_id TEXT,
			scope TEXT NOT NULL,
			reason TEXT NOT NULL,
			approved_by TEXT,
			status TEXT NOT NULL,
			expires_at DATETIME,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY(release_id) REFERENCES releases(id)
		)`,
		`CREATE TABLE IF NOT EXISTS scenarios (
			id TEXT PRIMARY KEY,
			ref_key TEXT,
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
			maintenance_windows_json TEXT,
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
			domain_id TEXT REFERENCES domains(id),
			category_id TEXT REFERENCES repository_categories(id),
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
			ref_key TEXT,
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
			parameters TEXT,
			init TEXT,
			actions TEXT,
			teardown TEXT,
			created_at DATETIME,
			updated_at DATETIME
		)`,
		`CREATE TABLE IF NOT EXISTS tests (
			id TEXT PRIMARY KEY,
			ref_key TEXT,
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
			created_by_subject TEXT,
			created_by_display_name TEXT,
			started_by_subject TEXT,
			started_by_display_name TEXT,
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
			latency_json TEXT,
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
			ref_key TEXT,
			schema_version TEXT,
			archive_backend TEXT,
			archive_path TEXT,
			manifest_path TEXT,
			archive_uri TEXT,
			manifest_uri TEXT,
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
			domain_roles_json TEXT,
			repository_roles_json TEXT,
			scopes_json TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			last_used_at DATETIME,
			expires_at DATETIME,
			key_rotated_at DATETIME,
			rotation_interval_days INTEGER
		)`,
		`CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			subject TEXT NOT NULL UNIQUE,
			email TEXT,
			display_name TEXT,
			active INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			last_seen_at DATETIME,
			disabled_at DATETIME
		)`,
		`CREATE TABLE IF NOT EXISTS user_role_bindings (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			scope_type TEXT NOT NULL,
			scope_id TEXT,
			role TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			UNIQUE(user_id, scope_type, scope_id, role),
			FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS audit_events (
			id TEXT PRIMARY KEY,
			action TEXT NOT NULL,
			actor_type TEXT NOT NULL,
			actor_subject TEXT NOT NULL,
			actor_display_name TEXT,
			project_id TEXT,
			domain_id TEXT,
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
		`CREATE TABLE IF NOT EXISTS external_result_imports (
			id TEXT PRIMARY KEY,
			provider TEXT NOT NULL,
			name TEXT NOT NULL,
			project_id TEXT,
			release_id TEXT,
			scenario_id TEXT,
			source TEXT,
			summary_json TEXT,
			raw_json TEXT,
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

	if err := s.applyOptionalSchemaMigrations([]optionalSchemaMigration{
		{name: "tests.runtime_settings_json", query: `ALTER TABLE tests ADD COLUMN runtime_settings_json TEXT`},
		{name: "tests.scenario_mode", query: `ALTER TABLE tests ADD COLUMN scenario_mode TEXT`},
		{name: "tests.goal_config_json", query: `ALTER TABLE tests ADD COLUMN goal_config_json TEXT`},
		{name: "tests.shakeout_enabled", query: `ALTER TABLE tests ADD COLUMN shakeout_enabled INTEGER DEFAULT 0`},
		{name: "tests.resource_baseline_enabled", query: `ALTER TABLE tests ADD COLUMN resource_baseline_enabled INTEGER DEFAULT 0`},
		{name: "scenarios.assignment_json", query: `ALTER TABLE scenarios ADD COLUMN assignment_json TEXT`},
		{name: "scenarios.start_delay", query: `ALTER TABLE scenarios ADD COLUMN start_delay TEXT`},
		{name: "tests.assignment_json", query: `ALTER TABLE tests ADD COLUMN assignment_json TEXT`},
		{name: "scripts.metadata_json", query: `ALTER TABLE scripts ADD COLUMN metadata_json TEXT`},
		{name: "projects.settings_json", query: `ALTER TABLE projects ADD COLUMN settings_json TEXT`},
		{name: "projects.domain_id", query: `ALTER TABLE projects ADD COLUMN domain_id TEXT REFERENCES domains(id)`},
		{name: "projects.category_id", query: `ALTER TABLE projects ADD COLUMN category_id TEXT REFERENCES repository_categories(id)`},
		{name: "applications.project_id", query: `ALTER TABLE applications ADD COLUMN project_id TEXT REFERENCES projects(id)`},
		{name: "releases.project_id", query: `ALTER TABLE releases ADD COLUMN project_id TEXT REFERENCES projects(id)`},
		{name: "scripts.project_id", query: `ALTER TABLE scripts ADD COLUMN project_id TEXT REFERENCES projects(id)`},
		{name: "scripts.folder_id", query: `ALTER TABLE scripts ADD COLUMN folder_id TEXT REFERENCES folders(id)`},
		{name: "scripts.parameters", query: `ALTER TABLE scripts ADD COLUMN parameters TEXT`},
		{name: "tests.project_id", query: `ALTER TABLE tests ADD COLUMN project_id TEXT REFERENCES projects(id)`},
		{name: "tests.folder_id", query: `ALTER TABLE tests ADD COLUMN folder_id TEXT REFERENCES folders(id)`},
		{name: "tests.application_id", query: `ALTER TABLE tests ADD COLUMN application_id TEXT REFERENCES applications(id)`},
		{name: "tests.release_id", query: `ALTER TABLE tests ADD COLUMN release_id TEXT REFERENCES releases(id)`},
		{name: "tests.scenario_id", query: `ALTER TABLE tests ADD COLUMN scenario_id TEXT REFERENCES scenarios(id)`},
		{name: "tests.pending_since", query: `ALTER TABLE tests ADD COLUMN pending_since DATETIME`},
		{name: "tests.baseline_pinned", query: `ALTER TABLE tests ADD COLUMN baseline_pinned INTEGER DEFAULT 0`},
		{name: "tests.baseline_pinned_at", query: `ALTER TABLE tests ADD COLUMN baseline_pinned_at DATETIME`},
		{name: "shared_data_pools.project_id", query: `ALTER TABLE shared_data_pools ADD COLUMN project_id TEXT REFERENCES projects(id)`},
		{name: "releases.archived_at", query: `ALTER TABLE releases ADD COLUMN archived_at DATETIME`},
		{name: "test_interval_summaries.schema_version", query: `ALTER TABLE test_interval_summaries ADD COLUMN schema_version TEXT`},
		{name: "run_archives.schema_version", query: `ALTER TABLE run_archives ADD COLUMN schema_version TEXT`},
		{name: "run_archives.archive_backend", query: `ALTER TABLE run_archives ADD COLUMN archive_backend TEXT`},
		{name: "run_archives.archive_uri", query: `ALTER TABLE run_archives ADD COLUMN archive_uri TEXT`},
		{name: "run_archives.manifest_uri", query: `ALTER TABLE run_archives ADD COLUMN manifest_uri TEXT`},
		{name: "test_interval_summaries.generator_published_at", query: `ALTER TABLE test_interval_summaries ADD COLUMN generator_published_at DATETIME`},
		{name: "test_interval_summaries.controller_received_at", query: `ALTER TABLE test_interval_summaries ADD COLUMN controller_received_at DATETIME`},
		{name: "test_interval_summaries.generator_processing_lag_ms", query: `ALTER TABLE test_interval_summaries ADD COLUMN generator_processing_lag_ms REAL`},
		{name: "test_interval_summaries.transport_lag_ms", query: `ALTER TABLE test_interval_summaries ADD COLUMN transport_lag_ms REAL`},
		{name: "test_interval_summaries.controller_fanout_lag_ms", query: `ALTER TABLE test_interval_summaries ADD COLUMN controller_fanout_lag_ms REAL`},
		{name: "test_interval_summaries.browser_json", query: `ALTER TABLE test_interval_summaries ADD COLUMN browser_json TEXT`},
		{name: "test_interval_summaries.browser_by_device_json", query: `ALTER TABLE test_interval_summaries ADD COLUMN browser_by_device_json TEXT`},
		{name: "test_interval_summaries.latency_json", query: `ALTER TABLE test_interval_summaries ADD COLUMN latency_json TEXT`},
		{name: "service_accounts.active", query: `ALTER TABLE service_accounts ADD COLUMN active INTEGER NOT NULL DEFAULT 1`},
		{name: "service_accounts.expires_at", query: `ALTER TABLE service_accounts ADD COLUMN expires_at DATETIME`},
		{name: "service_accounts.key_rotated_at", query: `ALTER TABLE service_accounts ADD COLUMN key_rotated_at DATETIME`},
		{name: "service_accounts.rotation_interval_days", query: `ALTER TABLE service_accounts ADD COLUMN rotation_interval_days INTEGER`},
		{name: "service_accounts.scopes_json", query: `ALTER TABLE service_accounts ADD COLUMN scopes_json TEXT`},
		{name: "service_accounts.domain_roles_json", query: `ALTER TABLE service_accounts ADD COLUMN domain_roles_json TEXT`},
		{name: "service_accounts.repository_roles_json", query: `ALTER TABLE service_accounts ADD COLUMN repository_roles_json TEXT`},
		{name: "audit_events.domain_id", query: `ALTER TABLE audit_events ADD COLUMN domain_id TEXT`},
		{name: "tests.created_by_subject", query: `ALTER TABLE tests ADD COLUMN created_by_subject TEXT`},
		{name: "tests.created_by_display_name", query: `ALTER TABLE tests ADD COLUMN created_by_display_name TEXT`},
		{name: "tests.started_by_subject", query: `ALTER TABLE tests ADD COLUMN started_by_subject TEXT`},
		{name: "tests.started_by_display_name", query: `ALTER TABLE tests ADD COLUMN started_by_display_name TEXT`},
		{name: "scenario_scripts.stages_json", query: `ALTER TABLE scenario_scripts ADD COLUMN stages_json TEXT`},
		{name: "scenario_schedules.project_id", query: `ALTER TABLE scenario_schedules ADD COLUMN project_id TEXT`},
		{name: "scenario_schedules.owner_subject", query: `ALTER TABLE scenario_schedules ADD COLUMN owner_subject TEXT`},
		{name: "scenario_schedules.owner_display_name", query: `ALTER TABLE scenario_schedules ADD COLUMN owner_display_name TEXT`},
		{name: "scenario_schedules.governance_json", query: `ALTER TABLE scenario_schedules ADD COLUMN governance_json TEXT`},
		{name: "scenario_schedules.blackout_windows_json", query: `ALTER TABLE scenario_schedules ADD COLUMN blackout_windows_json TEXT`},
		{name: "scenario_schedules.maintenance_windows_json", query: `ALTER TABLE scenario_schedules ADD COLUMN maintenance_windows_json TEXT`},
		{name: "scenario_schedules.availability_windows_json", query: `ALTER TABLE scenario_schedules ADD COLUMN availability_windows_json TEXT`},
		{name: "scenario_schedules.revision", query: `ALTER TABLE scenario_schedules ADD COLUMN revision INTEGER DEFAULT 1`},
		{name: "scenario_schedules.approval_policy", query: `ALTER TABLE scenario_schedules ADD COLUMN approval_policy TEXT`},
		{name: "scenario_schedules.missed_run_policy", query: `ALTER TABLE scenario_schedules ADD COLUMN missed_run_policy TEXT`},
		{name: "scenario_schedules.conflict_summary", query: `ALTER TABLE scenario_schedules ADD COLUMN conflict_summary TEXT`},
		{name: "scenario_schedules.next_required_action", query: `ALTER TABLE scenario_schedules ADD COLUMN next_required_action TEXT`},
		{name: "scenario_schedules.pending_approval_id", query: `ALTER TABLE scenario_schedules ADD COLUMN pending_approval_id TEXT`},
		{name: "scripts.ref_key", query: `ALTER TABLE scripts ADD COLUMN ref_key TEXT`},
		{name: "scenarios.ref_key", query: `ALTER TABLE scenarios ADD COLUMN ref_key TEXT`},
		{name: "tests.ref_key", query: `ALTER TABLE tests ADD COLUMN ref_key TEXT`},
		{name: "run_archives.ref_key", query: `ALTER TABLE run_archives ADD COLUMN ref_key TEXT`},
	}); err != nil {
		return err
	}

	// Unique indexes guard against duplicate reference keys. Partial indexes
	// skip NULL/empty values so legacy rows awaiting backfill do not collide.
	for _, idx := range []struct {
		query string
		name  string
	}{
		{`CREATE UNIQUE INDEX IF NOT EXISTS idx_scripts_ref_key ON scripts(ref_key) WHERE ref_key IS NOT NULL AND ref_key != ''`, "idx_scripts_ref_key"},
		{`CREATE UNIQUE INDEX IF NOT EXISTS idx_scenarios_ref_key ON scenarios(ref_key) WHERE ref_key IS NOT NULL AND ref_key != ''`, "idx_scenarios_ref_key"},
		{`CREATE UNIQUE INDEX IF NOT EXISTS idx_tests_ref_key ON tests(ref_key) WHERE ref_key IS NOT NULL AND ref_key != ''`, "idx_tests_ref_key"},
		{`CREATE UNIQUE INDEX IF NOT EXISTS idx_run_archives_ref_key ON run_archives(ref_key) WHERE ref_key IS NOT NULL AND ref_key != ''`, "idx_run_archives_ref_key"},
	} {
		if _, err := s.db.Exec(idx.query); err != nil {
			return fmt.Errorf("failed to create %s: %w", idx.name, err)
		}
	}

	if _, err := s.db.Exec(`UPDATE test_interval_summaries SET schema_version = ? WHERE schema_version IS NULL OR TRIM(schema_version) = ''`, intervalSummarySchemaVersionV1); err != nil {
		return fmt.Errorf("failed to backfill test_interval_summaries.schema_version: %w", err)
	}
	if _, err := s.db.Exec(`UPDATE run_archives SET schema_version = ? WHERE schema_version IS NULL OR TRIM(schema_version) = ''`, archiveRecordSchemaVersion); err != nil {
		return fmt.Errorf("failed to backfill run_archives.schema_version: %w", err)
	}

	if err := s.ensureDefaultDomainHierarchy(); err != nil {
		return err
	}
	if err := s.ensureDefaultProject(); err != nil {
		return err
	}
	if err := s.ensureLegacyApplication(); err != nil {
		return err
	}
	return s.applyTimescaleDB()
}

// timescaleHypertable describes a table to convert into a TimescaleDB hypertable
// and the time column it is partitioned by.
type timescaleHypertable struct {
	table      string
	timeColumn string
}

// timescaleHypertables is the set of tables converted when TimescaleDB is enabled.
//
// Only test_interval_summaries is wired today: it has no unique/primary-key
// constraints, so it can be converted cleanly. usage_accounting_snapshots
// (PRIMARY KEY id) and archive-resident resource samples require a composite key
// that includes the partition column before they can be converted; they are left
// as documented, gated extensions (see docs/ENTERPRISE_DATA_PLATFORM_UPGRADE_PLAN.md).
var timescaleHypertables = []timescaleHypertable{
	{table: "test_interval_summaries", timeColumn: "timestamp"},
}

// timescaleSetupStatements returns the (Postgres-only) statements that enable the
// TimescaleDB extension and convert the configured tables into hypertables. It is
// pure and side-effect free so the SQL contract can be unit-tested without a live
// TimescaleDB instance.
func timescaleSetupStatements(tables []timescaleHypertable) []string {
	statements := []string{`CREATE EXTENSION IF NOT EXISTS timescaledb`}
	for _, ht := range tables {
		statements = append(statements, fmt.Sprintf(
			`SELECT create_hypertable('%s', '%s', if_not_exists => TRUE, migrate_data => TRUE)`,
			ht.table, ht.timeColumn,
		))
	}
	return statements
}

// applyTimescaleDB enables TimescaleDB hypertables when the operator has opted in
// (APEXLOAD_TIMESCALEDB=true) and the backend is PostgreSQL. It is a no-op for
// SQLite and whenever TimescaleDB is disabled, which is the default.
func (s *SQLiteStore) applyTimescaleDB() error {
	if !s.timescaleDB || s.db == nil || s.db.dialect != storeDialectPostgres {
		return nil
	}
	for _, stmt := range timescaleSetupStatements(timescaleHypertables) {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("failed to apply TimescaleDB setup (%q): %w", stmt, err)
		}
	}
	return nil
}

func (s *SQLiteStore) applyOptionalSchemaMigrations(migrations []optionalSchemaMigration) error {
	for _, migration := range migrations {
		if _, err := s.db.Exec(migration.query); err != nil && !isDuplicateColumnErr(err) {
			return fmt.Errorf("failed to apply schema migration %s: %w", migration.name, err)
		}
	}
	return nil
}

func isDuplicateColumnErr(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "duplicate column name") || strings.Contains(message, "already exists")
}

func (s *SQLiteStore) ensureDefaultDomainHierarchy() error {
	now := time.Now()
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO domains (id, name, description, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, DefaultDomainID, "Default Domain", "Default migrated APEX Load domain.", now, now); err != nil {
		return fmt.Errorf("failed to create default domain: %w", err)
	}
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO application_repositories (id, domain_id, name, description, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`, DefaultApplicationRepositoryID, DefaultDomainID, "Application Repository", "Default migrated application repository.", now, now); err != nil {
		return fmt.Errorf("failed to create default application repository: %w", err)
	}
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO repository_categories (id, domain_id, repository_id, name, description, sort_order, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, DefaultRepositoryCategoryID, DefaultDomainID, DefaultApplicationRepositoryID, "Uncategorized", "Default category for migrated projects.", 0, now, now); err != nil {
		return fmt.Errorf("failed to create default repository category: %w", err)
	}
	return nil
}

func (s *SQLiteStore) ensureDefaultProject() error {
	now := time.Now()
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO projects (id, domain_id, category_id, name, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`, DefaultProjectID, DefaultDomainID, DefaultRepositoryCategoryID, "Unassigned", now, now); err != nil {
		return fmt.Errorf("failed to create default project: %w", err)
	}
	if _, err := s.db.Exec(`UPDATE projects SET domain_id = ? WHERE domain_id IS NULL OR domain_id = ''`, DefaultDomainID); err != nil {
		return fmt.Errorf("failed to backfill project domain ids: %w", err)
	}
	if _, err := s.db.Exec(`UPDATE projects SET category_id = ? WHERE category_id IS NULL OR category_id = ''`, DefaultRepositoryCategoryID); err != nil {
		return fmt.Errorf("failed to backfill project category ids: %w", err)
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

func normalizeDomainID(domainID string) string {
	if domainID == "" {
		return DefaultDomainID
	}
	return domainID
}

func normalizeRepositoryID(repositoryID string) string {
	if repositoryID == "" {
		return DefaultApplicationRepositoryID
	}
	return repositoryID
}

func normalizeRepositoryCategoryID(categoryID string) string {
	if categoryID == "" {
		return DefaultRepositoryCategoryID
	}
	return categoryID
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
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO releases (id, application_id, project_id, name, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`, LegacyReleaseID, LegacyApplicationID, DefaultProjectID, "Imported Legacy", now, now); err != nil {
		return fmt.Errorf("failed to create legacy release: %w", err)
	}
	if _, err := s.db.Exec(`UPDATE releases SET project_id = COALESCE((SELECT project_id FROM applications WHERE applications.id = releases.application_id), ?) WHERE project_id IS NULL OR project_id = ''`, DefaultProjectID); err != nil {
		return fmt.Errorf("failed to backfill release project ids: %w", err)
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

func (s *SQLiteStore) SaveDomain(domain *Domain) error {
	if domain == nil {
		return errors.New("domain is required")
	}
	if strings.TrimSpace(domain.ID) == "" {
		return errors.New("domain id is required")
	}
	if strings.TrimSpace(domain.Name) == "" {
		return errors.New("domain name is required")
	}
	if domain.CreatedAt.IsZero() {
		domain.CreatedAt = time.Now()
	}
	domain.UpdatedAt = time.Now()
	_, err := s.db.Exec(`INSERT INTO domains (id, name, description, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, description=excluded.description, updated_at=excluded.updated_at`,
		domain.ID, strings.TrimSpace(domain.Name), strings.TrimSpace(domain.Description), domain.CreatedAt, domain.UpdatedAt)
	return err
}

func (s *SQLiteStore) GetDomain(id string) (*Domain, error) {
	var domain Domain
	err := s.db.QueryRow(`SELECT id, name, COALESCE(description, ''), created_at, updated_at FROM domains WHERE id = ?`, normalizeDomainID(id)).Scan(
		&domain.ID, &domain.Name, &domain.Description, &domain.CreatedAt, &domain.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &domain, nil
}

func (s *SQLiteStore) ListDomains() ([]*Domain, error) {
	rows, err := s.db.Query(`SELECT id, name, COALESCE(description, ''), created_at, updated_at FROM domains ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var domains []*Domain
	for rows.Next() {
		var domain Domain
		if err := rows.Scan(&domain.ID, &domain.Name, &domain.Description, &domain.CreatedAt, &domain.UpdatedAt); err != nil {
			return nil, err
		}
		domains = append(domains, &domain)
	}
	return domains, rows.Err()
}

func (s *SQLiteStore) DeleteDomain(id string) error {
	id = normalizeDomainID(id)
	if id == DefaultDomainID {
		return errors.New("default domain cannot be deleted")
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM projects WHERE domain_id = ?`, id).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return errors.New("domain has projects")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM repository_categories WHERE domain_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM application_repositories WHERE domain_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM domains WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) SaveApplicationRepository(repository *ApplicationRepository) error {
	if repository == nil {
		return errors.New("application repository is required")
	}
	if strings.TrimSpace(repository.ID) == "" {
		return errors.New("application repository id is required")
	}
	repository.DomainID = normalizeDomainID(repository.DomainID)
	if strings.TrimSpace(repository.Name) == "" {
		return errors.New("application repository name is required")
	}
	if repository.CreatedAt.IsZero() {
		repository.CreatedAt = time.Now()
	}
	repository.UpdatedAt = time.Now()
	_, err := s.db.Exec(`INSERT INTO application_repositories (id, domain_id, name, description, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET domain_id=excluded.domain_id, name=excluded.name, description=excluded.description, updated_at=excluded.updated_at`,
		repository.ID, repository.DomainID, strings.TrimSpace(repository.Name), strings.TrimSpace(repository.Description), repository.CreatedAt, repository.UpdatedAt)
	return err
}

func (s *SQLiteStore) GetApplicationRepository(domainID string) (*ApplicationRepository, error) {
	var repository ApplicationRepository
	err := s.db.QueryRow(`SELECT id, domain_id, name, COALESCE(description, ''), created_at, updated_at FROM application_repositories WHERE domain_id = ?`, normalizeDomainID(domainID)).Scan(
		&repository.ID, &repository.DomainID, &repository.Name, &repository.Description, &repository.CreatedAt, &repository.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &repository, nil
}

func (s *SQLiteStore) SaveRepositoryCategory(category *RepositoryCategory) error {
	if category == nil {
		return errors.New("repository category is required")
	}
	if strings.TrimSpace(category.ID) == "" {
		return errors.New("repository category id is required")
	}
	category.DomainID = normalizeDomainID(category.DomainID)
	category.RepositoryID = normalizeRepositoryID(category.RepositoryID)
	if strings.TrimSpace(category.Name) == "" {
		return errors.New("repository category name is required")
	}
	if category.CreatedAt.IsZero() {
		category.CreatedAt = time.Now()
	}
	category.UpdatedAt = time.Now()
	_, err := s.db.Exec(`INSERT INTO repository_categories (id, domain_id, repository_id, name, description, sort_order, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET domain_id=excluded.domain_id, repository_id=excluded.repository_id, name=excluded.name, description=excluded.description, sort_order=excluded.sort_order, updated_at=excluded.updated_at`,
		category.ID, category.DomainID, category.RepositoryID, strings.TrimSpace(category.Name), strings.TrimSpace(category.Description), category.SortOrder, category.CreatedAt, category.UpdatedAt)
	return err
}

func (s *SQLiteStore) GetRepositoryCategory(id string) (*RepositoryCategory, error) {
	var category RepositoryCategory
	err := s.db.QueryRow(`SELECT id, domain_id, repository_id, name, COALESCE(description, ''), COALESCE(sort_order, 0), created_at, updated_at FROM repository_categories WHERE id = ?`, normalizeRepositoryCategoryID(id)).Scan(
		&category.ID, &category.DomainID, &category.RepositoryID, &category.Name, &category.Description, &category.SortOrder, &category.CreatedAt, &category.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &category, nil
}

func (s *SQLiteStore) ListRepositoryCategories(domainID string) ([]*RepositoryCategory, error) {
	rows, err := s.db.Query(`SELECT id, domain_id, repository_id, name, COALESCE(description, ''), COALESCE(sort_order, 0), created_at, updated_at FROM repository_categories WHERE domain_id = ? ORDER BY sort_order ASC, name ASC`, normalizeDomainID(domainID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var categories []*RepositoryCategory
	for rows.Next() {
		var category RepositoryCategory
		if err := rows.Scan(&category.ID, &category.DomainID, &category.RepositoryID, &category.Name, &category.Description, &category.SortOrder, &category.CreatedAt, &category.UpdatedAt); err != nil {
			return nil, err
		}
		categories = append(categories, &category)
	}
	return categories, rows.Err()
}

func (s *SQLiteStore) DeleteRepositoryCategory(id string) error {
	id = normalizeRepositoryCategoryID(id)
	if id == DefaultRepositoryCategoryID {
		return errors.New("default repository category cannot be deleted")
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM projects WHERE category_id = ?`, id).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return errors.New("repository category has projects")
	}
	_, err := s.db.Exec(`DELETE FROM repository_categories WHERE id = ?`, id)
	return err
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
	if strings.TrimSpace(release.ProjectID) == "" {
		if application, err := s.GetApplication(release.ApplicationID); err == nil && application != nil {
			release.ProjectID = application.ProjectID
		}
	}
	release.ProjectID = normalizeProjectID(release.ProjectID)
	if strings.TrimSpace(release.Name) == "" {
		return errors.New("release name is required")
	}
	if release.CreatedAt.IsZero() {
		release.CreatedAt = time.Now()
	}
	release.UpdatedAt = time.Now()
	_, err := s.db.Exec(`INSERT INTO releases (id, application_id, project_id, name, created_at, updated_at, archived_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET application_id=excluded.application_id, project_id=excluded.project_id, name=excluded.name, updated_at=excluded.updated_at, archived_at=excluded.archived_at`,
		release.ID, release.ApplicationID, release.ProjectID, release.Name, release.CreatedAt, release.UpdatedAt, release.ArchivedAt)
	return err
}

func (s *SQLiteStore) GetRelease(id string) (*Release, error) {
	var release Release
	err := s.db.QueryRow(`SELECT id, application_id, COALESCE(project_id, ''), name, created_at, updated_at, archived_at FROM releases WHERE id = ?`, id).Scan(
		&release.ID, &release.ApplicationID, &release.ProjectID, &release.Name, &release.CreatedAt, &release.UpdatedAt, &release.ArchivedAt)
	if err != nil {
		return nil, err
	}
	release.ProjectID = normalizeProjectID(release.ProjectID)
	return &release, nil
}

func (s *SQLiteStore) ListReleases(applicationID string, includeArchived bool) ([]*Release, error) {
	args := []any{}
	query := `SELECT id, application_id, COALESCE(project_id, ''), name, created_at, updated_at, archived_at FROM releases`
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
		if err := rows.Scan(&release.ID, &release.ApplicationID, &release.ProjectID, &release.Name, &release.CreatedAt, &release.UpdatedAt, &release.ArchivedAt); err != nil {
			return nil, err
		}
		release.ProjectID = normalizeProjectID(release.ProjectID)
		releases = append(releases, &release)
	}
	return releases, rows.Err()
}

func (s *SQLiteStore) ListReleasesByProject(projectID string, includeArchived bool) ([]*Release, error) {
	args := []any{normalizeProjectID(projectID)}
	query := `SELECT id, application_id, COALESCE(project_id, ''), name, created_at, updated_at, archived_at FROM releases WHERE project_id = ?`
	if !includeArchived {
		query += ` AND archived_at IS NULL`
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
		if err := rows.Scan(&release.ID, &release.ApplicationID, &release.ProjectID, &release.Name, &release.CreatedAt, &release.UpdatedAt, &release.ArchivedAt); err != nil {
			return nil, err
		}
		release.ProjectID = normalizeProjectID(release.ProjectID)
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
	maintenanceWindowsJSON, err := json.Marshal(schedule.MaintenanceWindows)
	if err != nil {
		return fmt.Errorf("marshal scenario schedule maintenance windows: %w", err)
	}
	availabilityWindowsJSON, err := json.Marshal(schedule.AvailabilityWindows)
	if err != nil {
		return fmt.Errorf("marshal scenario schedule availability windows: %w", err)
	}
	_, err = s.db.Exec(`INSERT INTO scenario_schedules (id, scenario_id, project_id, revision, name, cron_expr, timezone, release_id, run_name_template, enabled, approval_policy, missed_run_policy, conflict_summary, next_required_action, pending_approval_id, owner_subject, owner_display_name, governance_json, blackout_windows_json, maintenance_windows_json, availability_windows_json, last_run_test_id, last_run_at, next_run_at, last_error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET scenario_id=excluded.scenario_id, project_id=excluded.project_id, revision=excluded.revision, name=excluded.name, cron_expr=excluded.cron_expr,
		timezone=excluded.timezone, release_id=excluded.release_id, run_name_template=excluded.run_name_template,
		enabled=excluded.enabled, approval_policy=excluded.approval_policy, missed_run_policy=excluded.missed_run_policy,
		conflict_summary=excluded.conflict_summary, next_required_action=excluded.next_required_action, pending_approval_id=excluded.pending_approval_id,
		owner_subject=excluded.owner_subject, owner_display_name=excluded.owner_display_name,
		governance_json=excluded.governance_json, blackout_windows_json=excluded.blackout_windows_json,
		maintenance_windows_json=excluded.maintenance_windows_json, availability_windows_json=excluded.availability_windows_json, last_run_test_id=excluded.last_run_test_id, last_run_at=excluded.last_run_at,
		next_run_at=excluded.next_run_at, last_error=excluded.last_error, updated_at=excluded.updated_at`,
		schedule.ID, schedule.ScenarioID, schedule.ProjectID, schedule.Revision, schedule.Name, schedule.CronExpr, schedule.Timezone, schedule.ReleaseID, schedule.RunNameTemplate, schedule.Enabled, schedule.ApprovalPolicy, schedule.MissedRunPolicy, schedule.ConflictSummary, schedule.NextRequiredAction, schedule.PendingApprovalID, schedule.OwnerSubject, schedule.OwnerDisplayName, string(governanceJSON), string(blackoutWindowsJSON), string(maintenanceWindowsJSON), string(availabilityWindowsJSON),
		schedule.LastRunTestID, schedule.LastRunAt, schedule.NextRunAt, schedule.LastError, schedule.CreatedAt, schedule.UpdatedAt)
	return err
}

func (s *SQLiteStore) GetScenarioSchedule(id string) (*ScenarioSchedule, error) {
	row := s.db.QueryRow(`SELECT id, scenario_id, COALESCE(project_id, ''), COALESCE(revision, 1), name, cron_expr, COALESCE(timezone, ''), COALESCE(release_id, ''), COALESCE(run_name_template, ''), COALESCE(enabled, 1), COALESCE(approval_policy, ''), COALESCE(missed_run_policy, ''), COALESCE(conflict_summary, ''), COALESCE(next_required_action, ''), COALESCE(pending_approval_id, ''), COALESCE(owner_subject, ''), COALESCE(owner_display_name, ''), COALESCE(governance_json, ''), COALESCE(blackout_windows_json, ''), COALESCE(maintenance_windows_json, ''), COALESCE(availability_windows_json, ''), COALESCE(last_run_test_id, ''), last_run_at, next_run_at, COALESCE(last_error, ''), created_at, updated_at FROM scenario_schedules WHERE id = ?`, id)
	return scanScenarioSchedule(row)
}

func (s *SQLiteStore) ListScenarioSchedules(scenarioID string) ([]*ScenarioSchedule, error) {
	rows, err := s.db.Query(`SELECT id, scenario_id, COALESCE(project_id, ''), COALESCE(revision, 1), name, cron_expr, COALESCE(timezone, ''), COALESCE(release_id, ''), COALESCE(run_name_template, ''), COALESCE(enabled, 1), COALESCE(approval_policy, ''), COALESCE(missed_run_policy, ''), COALESCE(conflict_summary, ''), COALESCE(next_required_action, ''), COALESCE(pending_approval_id, ''), COALESCE(owner_subject, ''), COALESCE(owner_display_name, ''), COALESCE(governance_json, ''), COALESCE(blackout_windows_json, ''), COALESCE(maintenance_windows_json, ''), COALESCE(availability_windows_json, ''), COALESCE(last_run_test_id, ''), last_run_at, next_run_at, COALESCE(last_error, ''), created_at, updated_at FROM scenario_schedules WHERE scenario_id = ? ORDER BY created_at ASC`, scenarioID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectScenarioSchedules(rows)
}

func (s *SQLiteStore) ListAllScenarioSchedules() ([]*ScenarioSchedule, error) {
	rows, err := s.db.Query(`SELECT id, scenario_id, COALESCE(project_id, ''), COALESCE(revision, 1), name, cron_expr, COALESCE(timezone, ''), COALESCE(release_id, ''), COALESCE(run_name_template, ''), COALESCE(enabled, 1), COALESCE(approval_policy, ''), COALESCE(missed_run_policy, ''), COALESCE(conflict_summary, ''), COALESCE(next_required_action, ''), COALESCE(pending_approval_id, ''), COALESCE(owner_subject, ''), COALESCE(owner_display_name, ''), COALESCE(governance_json, ''), COALESCE(blackout_windows_json, ''), COALESCE(maintenance_windows_json, ''), COALESCE(availability_windows_json, ''), COALESCE(last_run_test_id, ''), last_run_at, next_run_at, COALESCE(last_error, ''), created_at, updated_at FROM scenario_schedules ORDER BY created_at ASC`)
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
	var enabled any
	var governanceJSON string
	var blackoutWindowsJSON string
	var maintenanceWindowsJSON string
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
		&maintenanceWindowsJSON,
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
	schedule.Enabled = sqlBool(enabled)
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
	if maintenanceWindowsJSON != "" && maintenanceWindowsJSON != "null" {
		_ = json.Unmarshal([]byte(maintenanceWindowsJSON), &schedule.MaintenanceWindows)
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
	project.DomainID = normalizeDomainID(project.DomainID)
	project.CategoryID = normalizeRepositoryCategoryID(project.CategoryID)
	if project.CreatedAt.IsZero() {
		project.CreatedAt = time.Now()
	}
	project.UpdatedAt = time.Now()
	settingsJSON, err := json.Marshal(project.Settings)
	if err != nil {
		return fmt.Errorf("marshal project settings: %w", err)
	}
	_, err = s.db.Exec(`INSERT INTO projects (id, domain_id, category_id, name, settings_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET domain_id=excluded.domain_id, category_id=excluded.category_id, name=excluded.name, settings_json=excluded.settings_json, updated_at=excluded.updated_at`,
		project.ID, project.DomainID, project.CategoryID, project.Name, string(settingsJSON), project.CreatedAt, project.UpdatedAt)
	return err
}

func (s *SQLiteStore) GetProject(id string) (*Project, error) {
	var project Project
	var settingsJSON string
	err := s.db.QueryRow(`SELECT id, COALESCE(domain_id, ''), COALESCE(category_id, ''), name, COALESCE(settings_json, ''), created_at, updated_at FROM projects WHERE id = ?`, id).Scan(
		&project.ID, &project.DomainID, &project.CategoryID, &project.Name, &settingsJSON, &project.CreatedAt, &project.UpdatedAt)
	if err != nil {
		return nil, err
	}
	project.DomainID = normalizeDomainID(project.DomainID)
	project.CategoryID = normalizeRepositoryCategoryID(project.CategoryID)
	if settingsJSON != "" && settingsJSON != "null" {
		if err := json.Unmarshal([]byte(settingsJSON), &project.Settings); err != nil {
			return nil, fmt.Errorf("unmarshal project settings: %w", err)
		}
	}
	return &project, nil
}

func (s *SQLiteStore) ListProjects() ([]*Project, error) {
	rows, err := s.db.Query(`SELECT id, COALESCE(domain_id, ''), COALESCE(category_id, ''), name, COALESCE(settings_json, ''), created_at, updated_at FROM projects ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var projects []*Project
	for rows.Next() {
		var project Project
		var settingsJSON string
		if err := rows.Scan(&project.ID, &project.DomainID, &project.CategoryID, &project.Name, &settingsJSON, &project.CreatedAt, &project.UpdatedAt); err != nil {
			return nil, err
		}
		project.DomainID = normalizeDomainID(project.DomainID)
		project.CategoryID = normalizeRepositoryCategoryID(project.CategoryID)
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
	parametersJSON, _ := json.Marshal(script.Parameters)
	initJSON, _ := json.Marshal(script.Init)
	actionsJSON, _ := json.Marshal(script.Actions)
	teardownJSON, _ := json.Marshal(script.Teardown)

	if script.CreatedAt.IsZero() {
		script.CreatedAt = time.Now()
	}
	script.UpdatedAt = time.Now()

	query := `INSERT INTO scripts (id, ref_key, name, protocol, version, project_id, folder_id, generated_by, generated_from, metadata_json, config, data_sources, parameters, init, actions, teardown, created_at, updated_at)
			  VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			  ON CONFLICT(id) DO UPDATE SET
			  ref_key=excluded.ref_key,
			  name=excluded.name, protocol=excluded.protocol, version=excluded.version,
			  project_id=excluded.project_id, folder_id=excluded.folder_id,
			  generated_by=excluded.generated_by, generated_from=excluded.generated_from,
			  metadata_json=excluded.metadata_json,
			  config=excluded.config, data_sources=excluded.data_sources, parameters=excluded.parameters,
			  init=excluded.init, actions=excluded.actions, teardown=excluded.teardown,
			  updated_at=excluded.updated_at`

	_, err := s.db.Exec(query,
		script.ID, script.RefKey, script.Name, script.Protocol, script.Version, script.ProjectID, script.FolderID,
		script.GeneratedBy, script.GeneratedFrom,
		string(metadataJSON), string(configJSON), string(dataSourcesJSON), string(parametersJSON), string(initJSON), string(actionsJSON), string(teardownJSON),
		script.CreatedAt, script.UpdatedAt)
	return err
}

func (s *SQLiteStore) GetScript(id string) (*dsl.Script, error) {
	var script dsl.Script
	var metadataJSON, configJSON, dataSourcesJSON, parametersJSON, initJSON, actionsJSON, teardownJSON string

	query := `SELECT id, COALESCE(ref_key, ''), name, protocol, version, COALESCE(project_id, ''), folder_id, generated_by, generated_from, COALESCE(metadata_json, ''), config, data_sources, COALESCE(parameters, ''), init, actions, teardown, created_at, updated_at
			  FROM scripts WHERE id = ?`
	err := s.db.QueryRow(query, id).Scan(
		&script.ID, &script.RefKey, &script.Name, &script.Protocol, &script.Version,
		&script.ProjectID, &script.FolderID,
		&script.GeneratedBy, &script.GeneratedFrom,
		&metadataJSON, &configJSON, &dataSourcesJSON, &parametersJSON, &initJSON, &actionsJSON, &teardownJSON,
		&script.CreatedAt, &script.UpdatedAt)
	if err != nil {
		return nil, err
	}

	if metadataJSON != "" && metadataJSON != "null" {
		json.Unmarshal([]byte(metadataJSON), &script.Metadata)
	}
	json.Unmarshal([]byte(configJSON), &script.Config)
	json.Unmarshal([]byte(dataSourcesJSON), &script.DataSources)
	if parametersJSON != "" && parametersJSON != "null" {
		json.Unmarshal([]byte(parametersJSON), &script.Parameters)
	}
	json.Unmarshal([]byte(initJSON), &script.Init)
	json.Unmarshal([]byte(actionsJSON), &script.Actions)
	json.Unmarshal([]byte(teardownJSON), &script.Teardown)

	return &script, nil
}

func (s *SQLiteStore) ListScripts(projectID string, folderID *string) ([]*dsl.Script, error) {
	args := []any{}
	query := `SELECT id, COALESCE(ref_key, ''), name, protocol, version, COALESCE(project_id, ''), folder_id, generated_by, generated_from, COALESCE(metadata_json, ''), config, data_sources, COALESCE(parameters, ''), init, actions, teardown, created_at, updated_at FROM scripts`
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
		var metadataJSON, configJSON, dataSourcesJSON, parametersJSON, initJSON, actionsJSON, teardownJSON string
		err := rows.Scan(
			&script.ID, &script.RefKey, &script.Name, &script.Protocol, &script.Version,
			&script.ProjectID, &script.FolderID,
			&script.GeneratedBy, &script.GeneratedFrom,
			&metadataJSON, &configJSON, &dataSourcesJSON, &parametersJSON, &initJSON, &actionsJSON, &teardownJSON,
			&script.CreatedAt, &script.UpdatedAt)
		if err != nil {
			return nil, err
		}
		if metadataJSON != "" && metadataJSON != "null" {
			json.Unmarshal([]byte(metadataJSON), &script.Metadata)
		}
		json.Unmarshal([]byte(configJSON), &script.Config)
		json.Unmarshal([]byte(dataSourcesJSON), &script.DataSources)
		if parametersJSON != "" && parametersJSON != "null" {
			json.Unmarshal([]byte(parametersJSON), &script.Parameters)
		}
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

	query := `INSERT INTO tests (id, ref_key, name, status, application_id, release_id, scenario_id, project_id, folder_id, scenario_mode, shakeout_enabled, resource_baseline_enabled, assignment_json, total_vusers, duration_ns, start_time, pending_since, baseline_pinned, baseline_pinned_at, end_time, error, created_by_subject, created_by_display_name, started_by_subject, started_by_display_name, groups_json, runtime_settings_json, goal_config_json)
			  VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			  ON CONFLICT(id) DO UPDATE SET
			  ref_key=excluded.ref_key,
			  name=excluded.name, status=excluded.status, application_id=excluded.application_id, release_id=excluded.release_id, scenario_id=excluded.scenario_id,
			  project_id=excluded.project_id, folder_id=excluded.folder_id,
			  scenario_mode=excluded.scenario_mode, shakeout_enabled=excluded.shakeout_enabled,
			  resource_baseline_enabled=excluded.resource_baseline_enabled, assignment_json=excluded.assignment_json, total_vusers=excluded.total_vusers,
			  duration_ns=excluded.duration_ns, start_time=excluded.start_time, pending_since=excluded.pending_since,
			  baseline_pinned=excluded.baseline_pinned, baseline_pinned_at=excluded.baseline_pinned_at, end_time=excluded.end_time,
			  error=excluded.error, created_by_subject=excluded.created_by_subject, created_by_display_name=excluded.created_by_display_name,
			  started_by_subject=excluded.started_by_subject, started_by_display_name=excluded.started_by_display_name, groups_json=excluded.groups_json,
			  runtime_settings_json=excluded.runtime_settings_json, goal_config_json=excluded.goal_config_json`

	_, err := s.db.Exec(query, test.ID, test.RefKey, test.Name, test.Status, test.ApplicationID, test.ReleaseID, test.ScenarioID, test.ProjectID, test.FolderID, test.ScenarioMode, test.ScriptShakeout, test.ResourceBaseline, string(assignmentJSON), test.TotalVUsers, test.Duration, test.StartTime, test.PendingSince, test.BaselinePinned, test.BaselinePinnedAt, test.EndTime, test.Error, test.CreatedBySubject, test.CreatedByDisplayName, test.StartedBySubject, test.StartedByDisplayName, string(groupsJSON), string(runtimeSettingsJSON), string(goalConfigJSON))
	return err
}

func (s *SQLiteStore) GetTest(id string) (*TestRecord, error) {
	var test TestRecord
	var groupsJSON, assignmentJSON, runtimeSettingsJSON, goalConfigJSON string

	query := testSelectColumns() + ` FROM tests WHERE id = ?`
	err := s.db.QueryRow(query, id).Scan(&test.ID, &test.RefKey, &test.Name, &test.Status, &test.ApplicationID, &test.ReleaseID, &test.ScenarioID, &test.ProjectID, &test.FolderID, &test.ScenarioMode, &test.ScriptShakeout, &test.ResourceBaseline, &assignmentJSON, &test.TotalVUsers, &test.Duration, &test.StartTime, &test.PendingSince, &test.BaselinePinned, &test.BaselinePinnedAt, &test.EndTime, &test.Error, &test.CreatedBySubject, &test.CreatedByDisplayName, &test.StartedBySubject, &test.StartedByDisplayName, &groupsJSON, &runtimeSettingsJSON, &goalConfigJSON)
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

func testSelectColumns() string {
	return `SELECT id, COALESCE(ref_key, ''), name, status, COALESCE(application_id, ''), COALESCE(release_id, ''), COALESCE(scenario_id, ''), COALESCE(project_id, ''), folder_id, COALESCE(scenario_mode, ''), COALESCE(shakeout_enabled, 0), COALESCE(resource_baseline_enabled, 0), COALESCE(assignment_json, ''), total_vusers, duration_ns, start_time, pending_since, COALESCE(baseline_pinned, 0), baseline_pinned_at, end_time, error, COALESCE(created_by_subject, ''), COALESCE(created_by_display_name, ''), COALESCE(started_by_subject, ''), COALESCE(started_by_display_name, ''), groups_json, COALESCE(runtime_settings_json, ''), COALESCE(goal_config_json, '')`
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
		var test TestRecord
		var groupsJSON, assignmentJSON, runtimeSettingsJSON, goalConfigJSON string
		if err := rows.Scan(&test.ID, &test.RefKey, &test.Name, &test.Status, &test.ApplicationID, &test.ReleaseID, &test.ScenarioID, &test.ProjectID, &test.FolderID, &test.ScenarioMode, &test.ScriptShakeout, &test.ResourceBaseline, &assignmentJSON, &test.TotalVUsers, &test.Duration, &test.StartTime, &test.PendingSince, &test.BaselinePinned, &test.BaselinePinnedAt, &test.EndTime, &test.Error, &test.CreatedBySubject, &test.CreatedByDisplayName, &test.StartedBySubject, &test.StartedByDisplayName, &groupsJSON, &runtimeSettingsJSON, &goalConfigJSON); err != nil {
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
		var test TestRecord
		var groupsJSON, assignmentJSON, runtimeSettingsJSON, goalConfigJSON string
		if err := rows.Scan(&test.ID, &test.RefKey, &test.Name, &test.Status, &test.ApplicationID, &test.ReleaseID, &test.ScenarioID, &test.ProjectID, &test.FolderID, &test.ScenarioMode, &test.ScriptShakeout, &test.ResourceBaseline, &assignmentJSON, &test.TotalVUsers, &test.Duration, &test.StartTime, &test.PendingSince, &test.BaselinePinned, &test.BaselinePinnedAt, &test.EndTime, &test.Error, &test.CreatedBySubject, &test.CreatedByDisplayName, &test.StartedBySubject, &test.StartedByDisplayName, &groupsJSON, &runtimeSettingsJSON, &goalConfigJSON); err != nil {
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

func (s *SQLiteStore) SaveRunArchive(record *RunArchiveRecord) error {
	if record == nil {
		return errors.New("run archive record is nil")
	}
	record.SchemaVersion = normalizeTelemetrySchemaVersion(record.SchemaVersion, archiveRecordSchemaVersion)
	_, err := s.db.Exec(`INSERT INTO run_archives (test_id, ref_key, schema_version, archive_backend, archive_path, manifest_path, archive_uri, manifest_uri, transaction_count, resource_sample_count, interval_summary_count, created_at, completed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(test_id) DO UPDATE SET
		ref_key=excluded.ref_key,
		schema_version=excluded.schema_version,
		archive_backend=excluded.archive_backend,
		archive_path=excluded.archive_path,
		manifest_path=excluded.manifest_path,
		archive_uri=excluded.archive_uri,
		manifest_uri=excluded.manifest_uri,
		transaction_count=excluded.transaction_count,
		resource_sample_count=excluded.resource_sample_count,
		interval_summary_count=excluded.interval_summary_count,
		created_at=excluded.created_at,
		completed_at=excluded.completed_at`,
		record.TestID,
		record.RefKey,
		record.SchemaVersion,
		record.ArchiveBackend,
		record.ArchivePath,
		record.ManifestPath,
		record.ArchiveURI,
		record.ManifestURI,
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
	err := s.db.QueryRow(`SELECT test_id, COALESCE(ref_key, ''), schema_version, COALESCE(archive_backend, ''), archive_path, manifest_path, COALESCE(archive_uri, ''), COALESCE(manifest_uri, ''), transaction_count, resource_sample_count, interval_summary_count, created_at, completed_at FROM run_archives WHERE test_id = ?`, testID).Scan(
		&record.TestID,
		&record.RefKey,
		&record.SchemaVersion,
		&record.ArchiveBackend,
		&record.ArchivePath,
		&record.ManifestPath,
		&record.ArchiveURI,
		&record.ManifestURI,
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
	domainRolesJSON, err := json.Marshal(account.DomainRoles)
	if err != nil {
		return err
	}
	repositoryRolesJSON, err := json.Marshal(account.RepositoryRoles)
	if err != nil {
		return err
	}
	scopesJSON, err := json.Marshal(account.Scopes)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO service_accounts (id, name, description, key_hash, active, global_roles_json, project_roles_json, domain_roles_json, repository_roles_json, scopes_json, created_at, updated_at, last_used_at, expires_at, key_rotated_at, rotation_interval_days)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name=excluded.name,
			description=excluded.description,
			key_hash=excluded.key_hash,
			active=excluded.active,
			global_roles_json=excluded.global_roles_json,
			project_roles_json=excluded.project_roles_json,
			domain_roles_json=excluded.domain_roles_json,
			repository_roles_json=excluded.repository_roles_json,
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
		s.boolValue(account.Active),
		string(globalRolesJSON),
		string(projectRolesJSON),
		string(domainRolesJSON),
		string(repositoryRolesJSON),
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
	return `SELECT id, name, description, key_hash, active, global_roles_json, project_roles_json, COALESCE(domain_roles_json, ''), COALESCE(repository_roles_json, ''), COALESCE(scopes_json, ''), created_at, updated_at, last_used_at, expires_at, key_rotated_at, rotation_interval_days`
}

func (s *SQLiteStore) getServiceAccountByQuery(query string, args ...any) (*ServiceAccount, error) {
	var account ServiceAccount
	var active any
	var globalRolesJSON string
	var projectRolesJSON string
	var domainRolesJSON string
	var repositoryRolesJSON string
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
		&domainRolesJSON,
		&repositoryRolesJSON,
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
	account.Active = sqlBool(active)
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
	if err := json.Unmarshal([]byte(domainRolesJSON), &account.DomainRoles); err != nil && strings.TrimSpace(domainRolesJSON) != "" {
		return nil, err
	}
	if err := json.Unmarshal([]byte(repositoryRolesJSON), &account.RepositoryRoles); err != nil && strings.TrimSpace(repositoryRolesJSON) != "" {
		return nil, err
	}
	if err := json.Unmarshal([]byte(scopesJSON), &account.Scopes); err != nil && strings.TrimSpace(scopesJSON) != "" {
		return nil, err
	}
	if account.ProjectRoles == nil {
		account.ProjectRoles = map[string][]string{}
	}
	if account.DomainRoles == nil {
		account.DomainRoles = map[string][]string{}
	}
	if account.RepositoryRoles == nil {
		account.RepositoryRoles = map[string][]string{}
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
		var active any
		var globalRolesJSON string
		var projectRolesJSON string
		var domainRolesJSON string
		var repositoryRolesJSON string
		var scopesJSON string
		var lastUsedAt sql.NullTime
		var expiresAt sql.NullTime
		var keyRotatedAt sql.NullTime
		var rotationIntervalDays sql.NullInt64
		if err := rows.Scan(&account.ID, &account.Name, &account.Description, &account.KeyHash, &active, &globalRolesJSON, &projectRolesJSON, &domainRolesJSON, &repositoryRolesJSON, &scopesJSON, &account.CreatedAt, &account.UpdatedAt, &lastUsedAt, &expiresAt, &keyRotatedAt, &rotationIntervalDays); err != nil {
			return nil, err
		}
		account.Active = sqlBool(active)
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
		if err := json.Unmarshal([]byte(domainRolesJSON), &account.DomainRoles); err != nil && strings.TrimSpace(domainRolesJSON) != "" {
			return nil, err
		}
		if err := json.Unmarshal([]byte(repositoryRolesJSON), &account.RepositoryRoles); err != nil && strings.TrimSpace(repositoryRolesJSON) != "" {
			return nil, err
		}
		if err := json.Unmarshal([]byte(scopesJSON), &account.Scopes); err != nil && strings.TrimSpace(scopesJSON) != "" {
			return nil, err
		}
		if account.ProjectRoles == nil {
			account.ProjectRoles = map[string][]string{}
		}
		if account.DomainRoles == nil {
			account.DomainRoles = map[string][]string{}
		}
		if account.RepositoryRoles == nil {
			account.RepositoryRoles = map[string][]string{}
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
	_, err := s.db.Exec(`UPDATE service_accounts SET active = ?, updated_at = ? WHERE id = ?`, s.boolValue(active), updatedAt, id)
	return err
}

func (s *SQLiteStore) DeleteServiceAccount(id string) error {
	_, err := s.db.Exec(`DELETE FROM service_accounts WHERE id = ?`, id)
	return err
}

func (s *SQLiteStore) SaveUser(user *User) error {
	if user == nil {
		return fmt.Errorf("user is required")
	}
	user.Subject = strings.TrimSpace(user.Subject)
	if user.Subject == "" {
		return fmt.Errorf("user subject is required")
	}
	_, err := s.db.Exec(`INSERT INTO users (id, subject, email, display_name, active, created_at, updated_at, last_seen_at, disabled_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			subject=excluded.subject,
			email=excluded.email,
			display_name=excluded.display_name,
			active=excluded.active,
			updated_at=excluded.updated_at,
			last_seen_at=excluded.last_seen_at,
			disabled_at=excluded.disabled_at`,
		user.ID,
		user.Subject,
		user.Email,
		user.DisplayName,
		s.boolValue(user.Active),
		user.CreatedAt,
		user.UpdatedAt,
		user.LastSeenAt,
		user.DisabledAt,
	)
	return err
}

func (s *SQLiteStore) GetUser(id string) (*User, error) {
	return s.getUserByQuery(userSelectColumns()+` FROM users WHERE id = ?`, id)
}

func (s *SQLiteStore) GetUserBySubject(subject string) (*User, error) {
	return s.getUserByQuery(userSelectColumns()+` FROM users WHERE subject = ?`, strings.TrimSpace(subject))
}

func (s *SQLiteStore) ListUsers(filter UserFilter) ([]*User, error) {
	if filter.Limit <= 0 {
		filter.Limit = 100
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	query := strings.Builder{}
	query.WriteString(userSelectColumns() + ` FROM users WHERE 1 = 1`)
	args := []any{}
	if q := strings.TrimSpace(filter.Query); q != "" {
		query.WriteString(` AND (subject LIKE ? OR email LIKE ? OR display_name LIKE ?)`)
		like := "%" + q + "%"
		args = append(args, like, like, like)
	}
	if filter.Active != nil {
		query.WriteString(` AND active = ?`)
		args = append(args, s.boolValue(*filter.Active))
	}
	query.WriteString(` ORDER BY updated_at DESC LIMIT ? OFFSET ?`)
	args = append(args, filter.Limit, filter.Offset)
	rows, err := s.db.Query(query.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []*User{}
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		bindings, err := s.ListUserRoleBindings(user.ID)
		if err != nil {
			return nil, err
		}
		user.Bindings = bindings
		users = append(users, user)
	}
	return users, rows.Err()
}

func (s *SQLiteStore) UpdateUserActive(id string, active bool, updatedAt time.Time) error {
	var disabledAt any
	if !active {
		disabledAt = updatedAt
	}
	_, err := s.db.Exec(`UPDATE users SET active = ?, updated_at = ?, disabled_at = ? WHERE id = ?`, s.boolValue(active), updatedAt, disabledAt, id)
	return err
}

func (s *SQLiteStore) DeleteUser(id string) error {
	_, err := s.db.Exec(`DELETE FROM users WHERE id = ?`, id)
	return err
}

func (s *SQLiteStore) SaveUserRoleBinding(binding *UserRoleBinding) error {
	if binding == nil {
		return fmt.Errorf("user role binding is required")
	}
	_, err := s.db.Exec(`INSERT INTO user_role_bindings (id, user_id, scope_type, scope_id, role, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, scope_type, scope_id, role) DO UPDATE SET
			updated_at=excluded.updated_at`,
		binding.ID,
		binding.UserID,
		binding.ScopeType,
		binding.ScopeID,
		binding.Role,
		binding.CreatedAt,
		binding.UpdatedAt,
	)
	return err
}

func (s *SQLiteStore) DeleteUserRoleBinding(id string) error {
	_, err := s.db.Exec(`DELETE FROM user_role_bindings WHERE id = ?`, id)
	return err
}

func (s *SQLiteStore) ListUserRoleBindings(userID string) ([]*UserRoleBinding, error) {
	rows, err := s.db.Query(`SELECT id, user_id, scope_type, COALESCE(scope_id, ''), role, created_at, updated_at FROM user_role_bindings WHERE user_id = ? ORDER BY scope_type, scope_id, role`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	bindings := []*UserRoleBinding{}
	for rows.Next() {
		var binding UserRoleBinding
		if err := rows.Scan(&binding.ID, &binding.UserID, &binding.ScopeType, &binding.ScopeID, &binding.Role, &binding.CreatedAt, &binding.UpdatedAt); err != nil {
			return nil, err
		}
		bindings = append(bindings, &binding)
	}
	return bindings, rows.Err()
}

func userSelectColumns() string {
	return `SELECT id, subject, COALESCE(email, ''), COALESCE(display_name, ''), active, created_at, updated_at, last_seen_at, disabled_at`
}

type userScanner interface {
	Scan(dest ...any) error
}

func (s *SQLiteStore) getUserByQuery(query string, args ...any) (*User, error) {
	user, err := scanUser(s.db.QueryRow(query, args...))
	if err != nil {
		return nil, err
	}
	bindings, err := s.ListUserRoleBindings(user.ID)
	if err != nil {
		return nil, err
	}
	user.Bindings = bindings
	return user, nil
}

func scanUser(scanner userScanner) (*User, error) {
	var user User
	var active any
	var lastSeenAt sql.NullTime
	var disabledAt sql.NullTime
	if err := scanner.Scan(&user.ID, &user.Subject, &user.Email, &user.DisplayName, &active, &user.CreatedAt, &user.UpdatedAt, &lastSeenAt, &disabledAt); err != nil {
		return nil, err
	}
	user.Active = sqlBool(active)
	if lastSeenAt.Valid {
		user.LastSeenAt = &lastSeenAt.Time
	}
	if disabledAt.Valid {
		user.DisabledAt = &disabledAt.Time
	}
	return &user, nil
}

func (s *SQLiteStore) SaveAuditEvent(event *AuditEvent) error {
	if event == nil {
		return fmt.Errorf("audit event is required")
	}
	detailsJSON, err := json.Marshal(event.Details)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO audit_events (id, action, actor_type, actor_subject, actor_display_name, project_id, domain_id, resource_type, resource_id, status, details_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID,
		event.Action,
		event.ActorType,
		event.ActorSubject,
		event.ActorDisplayName,
		event.ProjectID,
		event.DomainID,
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
	query.WriteString(`SELECT id, action, actor_type, actor_subject, actor_display_name, COALESCE(project_id, ''), COALESCE(domain_id, ''), resource_type, resource_id, status, details_json, created_at FROM audit_events WHERE 1 = 1`)
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
	appendFilter("actor_subject", filter.ActorSubject)
	appendFilter("project_id", filter.ProjectID)
	appendFilter("domain_id", filter.DomainID)
	appendFilter("resource_type", filter.ResourceType)
	appendFilter("resource_id", filter.ResourceID)
	appendFilter("status", filter.Status)
	if filter.CreatedAfter != nil {
		query.WriteString(` AND created_at >= ?`)
		args = append(args, *filter.CreatedAfter)
	}
	if filter.CreatedBefore != nil {
		query.WriteString(` AND created_at <= ?`)
		args = append(args, *filter.CreatedBefore)
	}
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
		if err := rows.Scan(&event.ID, &event.Action, &event.ActorType, &event.ActorSubject, &event.ActorDisplayName, &event.ProjectID, &event.DomainID, &event.ResourceType, &event.ResourceID, &event.Status, &detailsJSON, &event.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(detailsJSON), &event.Details); err != nil && strings.TrimSpace(detailsJSON) != "" {
			return nil, err
		}
		events = append(events, &event)
	}
	return events, rows.Err()
}

func (s *SQLiteStore) SaveReleaseSLAWaiver(waiver *ReleaseSLAWaiver) error {
	if waiver == nil {
		return fmt.Errorf("release SLA waiver is required")
	}
	waiver.ID = strings.TrimSpace(waiver.ID)
	waiver.ReleaseID = strings.TrimSpace(waiver.ReleaseID)
	waiver.Scope = strings.TrimSpace(waiver.Scope)
	waiver.Reason = strings.TrimSpace(waiver.Reason)
	waiver.ApprovedBy = strings.TrimSpace(waiver.ApprovedBy)
	waiver.Status = strings.TrimSpace(waiver.Status)
	if waiver.ID == "" {
		return fmt.Errorf("release SLA waiver id is required")
	}
	if waiver.ReleaseID == "" {
		return fmt.Errorf("release id is required")
	}
	if waiver.Scope == "" {
		waiver.Scope = "release"
	}
	if waiver.Reason == "" {
		return fmt.Errorf("release SLA waiver reason is required")
	}
	if waiver.Status == "" {
		waiver.Status = "active"
	}
	waiver.ProjectID = normalizeProjectID(waiver.ProjectID)
	now := time.Now().UTC()
	if waiver.CreatedAt.IsZero() {
		waiver.CreatedAt = now
	}
	waiver.UpdatedAt = now
	_, err := s.db.Exec(`INSERT INTO release_sla_waivers (id, release_id, project_id, scope, reason, approved_by, status, expires_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			release_id=excluded.release_id,
			project_id=excluded.project_id,
			scope=excluded.scope,
			reason=excluded.reason,
			approved_by=excluded.approved_by,
			status=excluded.status,
			expires_at=excluded.expires_at,
			updated_at=excluded.updated_at`,
		waiver.ID,
		waiver.ReleaseID,
		waiver.ProjectID,
		waiver.Scope,
		waiver.Reason,
		waiver.ApprovedBy,
		waiver.Status,
		waiver.ExpiresAt,
		waiver.CreatedAt,
		waiver.UpdatedAt,
	)
	return err
}

func (s *SQLiteStore) ListReleaseSLAWaivers(releaseID string) ([]*ReleaseSLAWaiver, error) {
	releaseID = strings.TrimSpace(releaseID)
	if releaseID == "" {
		return nil, fmt.Errorf("release id is required")
	}
	rows, err := s.db.Query(`SELECT id, release_id, COALESCE(project_id, ''), scope, reason, COALESCE(approved_by, ''), status, expires_at, created_at, updated_at
		FROM release_sla_waivers WHERE release_id = ? ORDER BY created_at DESC`, releaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	waivers := []*ReleaseSLAWaiver{}
	for rows.Next() {
		waiver, err := scanReleaseSLAWaiver(rows)
		if err != nil {
			return nil, err
		}
		waivers = append(waivers, waiver)
	}
	return waivers, rows.Err()
}

func scanReleaseSLAWaiver(row scanner) (*ReleaseSLAWaiver, error) {
	var waiver ReleaseSLAWaiver
	var expiresAt sql.NullTime
	if err := row.Scan(
		&waiver.ID,
		&waiver.ReleaseID,
		&waiver.ProjectID,
		&waiver.Scope,
		&waiver.Reason,
		&waiver.ApprovedBy,
		&waiver.Status,
		&expiresAt,
		&waiver.CreatedAt,
		&waiver.UpdatedAt,
	); err != nil {
		return nil, err
	}
	waiver.ProjectID = normalizeProjectID(waiver.ProjectID)
	if expiresAt.Valid {
		waiver.ExpiresAt = &expiresAt.Time
	}
	if waiver.ExpiresAt != nil && waiver.ExpiresAt.Before(time.Now().UTC()) && waiver.Status == "active" {
		waiver.Status = "expired"
	}
	return &waiver, nil
}

func (s *SQLiteStore) SaveExternalResultImport(result *ExternalResultImport) error {
	if result == nil {
		return fmt.Errorf("external result import is required")
	}
	result.ID = strings.TrimSpace(result.ID)
	result.Provider = strings.ToLower(strings.TrimSpace(result.Provider))
	result.Name = strings.TrimSpace(result.Name)
	result.ReleaseID = strings.TrimSpace(result.ReleaseID)
	result.ScenarioID = strings.TrimSpace(result.ScenarioID)
	result.Source = strings.TrimSpace(result.Source)
	if result.ID == "" {
		return fmt.Errorf("external result import id is required")
	}
	if result.Provider == "" {
		return fmt.Errorf("external result import provider is required")
	}
	if result.Name == "" {
		result.Name = result.Provider + " import"
	}
	result.ProjectID = normalizeProjectID(result.ProjectID)
	if result.CreatedAt.IsZero() {
		result.CreatedAt = time.Now().UTC()
	}
	summaryJSON, err := json.Marshal(result.Summary)
	if err != nil {
		return fmt.Errorf("marshal external result summary: %w", err)
	}
	rawJSON, err := json.Marshal(result.RawResult)
	if err != nil {
		return fmt.Errorf("marshal external raw result: %w", err)
	}
	_, err = s.db.Exec(`INSERT INTO external_result_imports (id, provider, name, project_id, release_id, scenario_id, source, summary_json, raw_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			provider=excluded.provider,
			name=excluded.name,
			project_id=excluded.project_id,
			release_id=excluded.release_id,
			scenario_id=excluded.scenario_id,
			source=excluded.source,
			summary_json=excluded.summary_json,
			raw_json=excluded.raw_json`,
		result.ID,
		result.Provider,
		result.Name,
		result.ProjectID,
		result.ReleaseID,
		result.ScenarioID,
		result.Source,
		string(summaryJSON),
		string(rawJSON),
		result.CreatedAt,
	)
	return err
}

func (s *SQLiteStore) ListExternalResultImports(filter ExternalResultImportFilter) ([]*ExternalResultImport, error) {
	if filter.Limit <= 0 {
		filter.Limit = 100
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	query := strings.Builder{}
	query.WriteString(`SELECT id, provider, name, COALESCE(project_id, ''), COALESCE(release_id, ''), COALESCE(scenario_id, ''), COALESCE(source, ''), COALESCE(summary_json, ''), COALESCE(raw_json, ''), created_at FROM external_result_imports WHERE 1 = 1`)
	args := make([]any, 0, 6)
	if projectID := strings.TrimSpace(filter.ProjectID); projectID != "" {
		query.WriteString(` AND project_id = ?`)
		args = append(args, normalizeProjectID(projectID))
	}
	if releaseID := strings.TrimSpace(filter.ReleaseID); releaseID != "" {
		query.WriteString(` AND release_id = ?`)
		args = append(args, releaseID)
	}
	if provider := strings.ToLower(strings.TrimSpace(filter.Provider)); provider != "" {
		query.WriteString(` AND provider = ?`)
		args = append(args, provider)
	}
	query.WriteString(` ORDER BY created_at DESC LIMIT ? OFFSET ?`)
	args = append(args, filter.Limit, filter.Offset)
	rows, err := s.db.Query(query.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	imports := []*ExternalResultImport{}
	for rows.Next() {
		result, err := scanExternalResultImport(rows)
		if err != nil {
			return nil, err
		}
		imports = append(imports, result)
	}
	return imports, rows.Err()
}

func scanExternalResultImport(row scanner) (*ExternalResultImport, error) {
	var result ExternalResultImport
	var summaryJSON string
	var rawJSON string
	if err := row.Scan(
		&result.ID,
		&result.Provider,
		&result.Name,
		&result.ProjectID,
		&result.ReleaseID,
		&result.ScenarioID,
		&result.Source,
		&summaryJSON,
		&rawJSON,
		&result.CreatedAt,
	); err != nil {
		return nil, err
	}
	result.ProjectID = normalizeProjectID(result.ProjectID)
	if strings.TrimSpace(summaryJSON) != "" && strings.TrimSpace(summaryJSON) != "null" {
		if err := json.Unmarshal([]byte(summaryJSON), &result.Summary); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(rawJSON) != "" && strings.TrimSpace(rawJSON) != "null" {
		if err := json.Unmarshal([]byte(rawJSON), &result.RawResult); err != nil {
			return nil, err
		}
	}
	return &result, nil
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

func (s *SQLiteStore) boolValue(value bool) any {
	if s != nil && s.db != nil && s.db.dialect == storeDialectPostgres {
		return value
	}
	return boolToSQLite(value)
}

func sqlBool(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case int:
		return typed != 0
	case int64:
		return typed != 0
	case int32:
		return typed != 0
	case []byte:
		return sqlBool(string(typed))
	case string:
		normalized := strings.TrimSpace(strings.ToLower(typed))
		return normalized == "1" || normalized == "t" || normalized == "true" || normalized == "yes" || normalized == "on"
	default:
		return false
	}
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}
