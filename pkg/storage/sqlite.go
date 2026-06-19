package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
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
	scriptRecordingSchemaVersion   = "apexload.recording.v1"
)

type BrowserMetricsSummaryRecord struct {
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
	AvgTTFBMs            float64 `json:"avg_ttfb_ms,omitempty"`
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
		{"auto_promote_on_heal INTEGER DEFAULT 0", "auto_promote_on_heal BOOLEAN DEFAULT FALSE"},
		{"resource_baseline_enabled INTEGER DEFAULT 0", "resource_baseline_enabled BOOLEAN DEFAULT FALSE"},
		{"enabled INTEGER DEFAULT 1", "enabled BOOLEAN DEFAULT TRUE"},
		{"baseline_pinned INTEGER DEFAULT 0", "baseline_pinned BOOLEAN DEFAULT FALSE"},
		{"active INTEGER NOT NULL DEFAULT 1", "active BOOLEAN NOT NULL DEFAULT TRUE"},
		{"ADD COLUMN active INTEGER NOT NULL DEFAULT 1", "ADD COLUMN active BOOLEAN NOT NULL DEFAULT TRUE"},
		{"COALESCE(shakeout_enabled, 0)", "COALESCE(shakeout_enabled, FALSE)"},
		{"COALESCE(resource_baseline_enabled, 0)", "COALESCE(resource_baseline_enabled, FALSE)"},
		{"COALESCE(auto_promote_on_heal, 0)", "COALESCE(auto_promote_on_heal, FALSE)"},
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

// applyTimescaleDB enables TimescaleDB hypertables when the operator has opted in
// (APEXLOAD_TIMESCALEDB=true) and the backend is PostgreSQL. It is a no-op for
// SQLite and whenever TimescaleDB is disabled, which is the default.

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

type scanner interface {
	Scan(dest ...any) error
}

func normalizeTelemetrySchemaVersion(version string, fallback string) string {
	if strings.TrimSpace(version) == "" {
		return fallback
	}
	return version
}

type userScanner interface {
	Scan(dest ...any) error
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
