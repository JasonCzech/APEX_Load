package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/jasonczech/apexload/pkg/protocol"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

// sqlStore owns the shared relational persistence implementation. Exported
// SQLiteStore remains a compatibility alias; backend construction selects a
// dialect explicitly rather than implying SQLite semantics for PostgreSQL.
type sqlStore struct {
	db          *storeDB
	backend     StoreBackendInfo
	timescaleDB bool
}

type SQLiteStore = sqlStore

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

// versionTimestamp stamps a row's updated_at version at microseconds, the
// precision PostgreSQL keeps, so the copy a writer holds after saving
// compares equal to the stored value on every backend. Optimistic writes
// compare that version exactly.
func versionTimestamp() time.Time {
	return time.Now().UTC().Truncate(time.Microsecond)
}

type storeDB struct {
	db      *sql.DB
	dialect storeDialect
	// Used only before a PostgreSQL store is published to callers. Every
	// initialization query/transaction stays on the advisory-lock session.
	initializationConn *sql.Conn
}

type storeTx struct {
	tx      *sql.Tx
	dialect storeDialect
}

type optionalSchemaMigration struct {
	name          string
	query         string
	postgresQuery string
}

const (
	intervalSummarySchemaVersionV1 = "apexload.interval-summary.v1"
	intervalSummarySchemaVersion   = "apexload.interval-summary.v8"
	archiveRecordSchemaVersion     = "apexload.archive.v1"
	scriptRecordingSchemaVersion   = "apexload.recording.v2"
)

type BrowserMetricsSummaryRecord struct {
	Samples              int64   `json:"samples"`
	DocumentLoads        int64   `json:"document_loads,omitempty"`
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
	DocumentLoads        int64   `json:"document_loads,omitempty"`
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
	MessagingProgress        *protocol.MessagingProgressSnapshot    `json:"messaging_progress,omitempty"`
	Messaging                map[string]*protocol.MessagingSummary  `json:"messaging,omitempty"`
	NativeEvidence           *NativeIntervalEvidence                `json:"native_evidence,omitempty"`
	CompletedIterations      int64                                  `json:"completed_iterations,omitempty"`
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
	Transactions             map[string]*IntervalTransactionRecord  `json:"transactions,omitempty"`
	ByRegion                 map[string]*IntervalTransactionRecord  `json:"by_region,omitempty"`
	Browser                  *BrowserMetricsSummaryRecord           `json:"browser,omitempty"`
	BrowserByDevice          map[string]*BrowserDeviceMetricsRecord `json:"browser_by_device,omitempty"`
	Latency                  *NetworkLatencySummaryRecord           `json:"latency,omitempty"`
	DroppedResultsAvailable  bool                                   `json:"dropped_results_available,omitempty"`
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

// IntervalTransactionRecord is the bounded, chart-focused transaction
// breakdown stored with a live interval summary. Count is the number of
// attempts, Errors is a subset of Count, and TPS is the successful transaction
// rate for the interval.
type IntervalTransactionRecord struct {
	Name   string  `json:"name"`
	TPS    float64 `json:"tps"`
	Count  int64   `json:"count"`
	Errors int64   `json:"errors"`
	AvgMs  float64 `json:"avg_ms"`
}

// sqliteDSN adds pragmas for integrity and the controller's high-read,
// single-writer shape. Foreign-key enforcement makes the declared ownership
// graph real, busy_timeout waits out brief read/write overlap, WAL lets readers
// continue while a writer commits, and synchronous=NORMAL avoids fsyncing every
// page while keeping WAL recovery semantics. Existing query parameters on the
// path are preserved and caller-provided pragmas win.
func sqliteDSN(path string) string {
	if strings.TrimSpace(path) == "" {
		return path
	}
	for _, pragma := range []struct {
		key   string
		value string
	}{
		{key: "_pragma=foreign_keys", value: "_pragma=foreign_keys(ON)"},
		{key: "_pragma=busy_timeout", value: "_pragma=busy_timeout(5000)"},
		{key: "_pragma=journal_mode", value: "_pragma=journal_mode(WAL)"},
		{key: "_pragma=synchronous", value: "_pragma=synchronous(NORMAL)"},
	} {
		if strings.Contains(path, pragma.key) {
			continue
		}
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		path += sep + pragma.value
	}
	return path
}

func nullableString(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}

func nullableStringPointer(value *string) any {
	if value == nil {
		return nil
	}
	return nullableString(*value)
}

func NewSQLiteStore(path string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite db: %w", err)
	}
	// Single connection: modernc.org/sqlite + WAL is serialized on one writer,
	// and a single pooled connection avoids "database is locked" contention.
	//
	// INVARIANT: with one connection, an open *sql.Rows pins it, so no store
	// method may run a SECOND query before it has closed the rows from the first
	// (the pool wait has no deadline — the goroutine would block forever waiting
	// for the connection it itself holds). Always drain/Close rows before issuing
	// a nested query: collect the row values into a slice first, then run the
	// per-row follow-up queries (see ListUsers, which reads all users before
	// calling ListUserRoleBindings for each).
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	s := newSQLStore(db, storeDialectSQLite, StoreBackendInfo{
		Type:             "sqlite",
		Driver:           "modernc.org/sqlite",
		MigrationVersion: currentSchemaVersionLabel(),
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
		MigrationVersion:   currentSchemaVersionLabel(),
		LeaseCapability:    true,
		TimescaleDBEnabled: timescaleDB,
		DSN:                databaseURL,
	})
	if err := s.initializePostgres(); err != nil {
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

func (db *storeDB) initializationConnection(ctx context.Context) (*sql.Conn, error) {
	return db.db.Conn(ctx)
}

func (db *storeDB) Exec(query string, args ...any) (sql.Result, error) {
	return db.ExecContext(context.Background(), query, args...)
}

func (db *storeDB) Query(query string, args ...any) (*sql.Rows, error) {
	return db.QueryContext(context.Background(), query, args...)
}

func (db *storeDB) QueryRow(query string, args ...any) *sql.Row {
	return db.QueryRowContext(context.Background(), query, args...)
}

// ExecContext/QueryContext/QueryRowContext are the context-carrying half of the
// storeDB surface. Without them a caller that already holds a context has to
// reach through to db.db and hand-write rewriteSQL at the call site, and one
// forgotten rewrite silently ships SQLite-only SQL (`?` placeholders, DATETIME,
// integer booleans) to PostgreSQL. TestStorageQueriesGoThroughTheDialectWrapper
// keeps that bypass from reappearing.
func (db *storeDB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return db.executor().ExecContext(ctx, rewriteSQL(query, db.dialect), args...)
}

func (db *storeDB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return db.executor().QueryContext(ctx, rewriteSQL(query, db.dialect), args...)
}

func (db *storeDB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return db.executor().QueryRowContext(ctx, rewriteSQL(query, db.dialect), args...)
}

func (db *storeDB) Begin() (*storeTx, error) {
	return db.BeginTx(context.Background(), nil)
}

func (db *storeDB) BeginTx(ctx context.Context, opts *sql.TxOptions) (*storeTx, error) {
	tx, err := db.executor().BeginTx(ctx, opts)
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

func (tx *storeTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return tx.tx.ExecContext(ctx, rewriteSQL(query, tx.dialect), args...)
}

func (tx *storeTx) Query(query string, args ...any) (*sql.Rows, error) {
	return tx.tx.Query(rewriteSQL(query, tx.dialect), args...)
}

func (tx *storeTx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return tx.tx.QueryContext(ctx, rewriteSQL(query, tx.dialect), args...)
}

func (tx *storeTx) QueryRow(query string, args ...any) *sql.Row {
	return tx.tx.QueryRow(rewriteSQL(query, tx.dialect), args...)
}

func (tx *storeTx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return tx.tx.QueryRowContext(ctx, rewriteSQL(query, tx.dialect), args...)
}

func (tx *storeTx) Commit() error {
	return tx.tx.Commit()
}

func (tx *storeTx) Rollback() error {
	return tx.tx.Rollback()
}

var (
	insertOrIgnorePrefix = regexp.MustCompile(`(?is)^(\s*)INSERT\s+OR\s+IGNORE\s+INTO\s+`)
	inlineReferences     = regexp.MustCompile(`(?i)\s+REFERENCES\s+[a-zA-Z_][a-zA-Z0-9_]*\s*\([^)]*\)(?:\s+ON\s+DELETE\s+(?:CASCADE|SET\s+NULL|RESTRICT|NO\s+ACTION))?`)
	trailingCreateComma  = regexp.MustCompile(`,\s*\)`)
	// postgresSQLRewrites translates SQLite SQL into Postgres SQL by SUBSTRING
	// match: every entry is pinned to one column name written one exact way.
	// New boolean DDL uses explicit dialect constructors. This compatibility
	// path still supports older callers and queries: a boolean column that
	// deviates from a pattern here (extra NOT NULL, a different default, a new
	// name) silently stays INTEGER on Postgres while Go binds a real bool — a
	// runtime-only break no SQLite test can see. Three guards keep the list
	// self-policing against the live schema and the live SQL; extend the matching
	// one whenever you add an entry:
	//   - TestPostgresSchemaBooleanColumnsRewriteToBoolean (factory_test.go):
	//     DDL column types + COALESCE(bool_col, 0|1) reads.
	//   - TestPostgresBooleanColumnPredicatesAreDialectSafe
	//     (postgres_boolean_predicate_test.go): `bool_col = 0|1` DML predicates,
	//     including the bare `AND bool_col = 1` fragments this package appends to
	//     base queries. It does NOT cover positional integer literals inside
	//     INSERT ... VALUES (...), which are equally fatal on Postgres — bind
	//     those with s.boolValue rather than writing 0/1 into the literal.
	//   - TestPostgresRewrittenSchemaIsWellFormed
	//     (postgres_schema_wellformed_test.go): the rewritten DDL still parses.
	postgresSQLRewrites = []struct {
		pattern     *regexp.Regexp
		replacement string
	}{
		{regexp.MustCompile(`(?i)\bDATETIME\b`), "TIMESTAMPTZ"},
		{regexp.MustCompile(`(?i)\bshakeout_enabled\s+INTEGER\s+DEFAULT\s+0\b`), "shakeout_enabled BOOLEAN DEFAULT FALSE"},
		{regexp.MustCompile(`(?i)\bauto_promote_on_heal\s+INTEGER\s+DEFAULT\s+0\b`), "auto_promote_on_heal BOOLEAN DEFAULT FALSE"},
		{regexp.MustCompile(`(?i)\bresource_baseline_enabled\s+INTEGER\s+DEFAULT\s+0\b`), "resource_baseline_enabled BOOLEAN DEFAULT FALSE"},
		{regexp.MustCompile(`(?i)\benabled\s+INTEGER\s+DEFAULT\s+1\b`), "enabled BOOLEAN DEFAULT TRUE"},
		{regexp.MustCompile(`(?i)\bbaseline_pinned\s+INTEGER\s+DEFAULT\s+0\b`), "baseline_pinned BOOLEAN DEFAULT FALSE"},
		{regexp.MustCompile(`(?i)\bbase_missing\s+INTEGER\s+NOT\s+NULL\s+DEFAULT\s+0\b`), "base_missing BOOLEAN NOT NULL DEFAULT FALSE"},
		{regexp.MustCompile(`(?i)\bactive\s+INTEGER\s+NOT\s+NULL\s+DEFAULT\s+1\b`), "active BOOLEAN NOT NULL DEFAULT TRUE"},
		{regexp.MustCompile(`(?i)\bCOALESCE\s*\(\s*((?:[a-z_][a-z0-9_]*\s*\.\s*)?)shakeout_enabled\s*,\s*0\s*\)`), "COALESCE(${1}shakeout_enabled, FALSE)"},
		{regexp.MustCompile(`(?i)\bCOALESCE\s*\(\s*((?:[a-z_][a-z0-9_]*\s*\.\s*)?)resource_baseline_enabled\s*,\s*0\s*\)`), "COALESCE(${1}resource_baseline_enabled, FALSE)"},
		{regexp.MustCompile(`(?i)\bCOALESCE\s*\(\s*((?:[a-z_][a-z0-9_]*\s*\.\s*)?)auto_promote_on_heal\s*,\s*0\s*\)`), "COALESCE(${1}auto_promote_on_heal, FALSE)"},
		{regexp.MustCompile(`(?i)\bCOALESCE\s*\(\s*((?:[a-z_][a-z0-9_]*\s*\.\s*)?)enabled\s*,\s*1\s*\)`), "COALESCE(${1}enabled, TRUE)"},
		{regexp.MustCompile(`(?i)\bCOALESCE\s*\(\s*((?:[a-z_][a-z0-9_]*\s*\.\s*)?)baseline_pinned\s*,\s*0\s*\)`), "COALESCE(${1}baseline_pinned, FALSE)"},
		{regexp.MustCompile(`(?i)\bCOALESCE\s*\(\s*((?:[a-z_][a-z0-9_]*\s*\.\s*)?)base_missing\s*,\s*0\s*\)`), "COALESCE(${1}base_missing, FALSE)"},
		{regexp.MustCompile(`(?i)\bactive\s*=\s*1\b`), "active = TRUE"},
	}
	// postgresDDLIntegerType widens every remaining DDL INTEGER to BIGINT. SQLite
	// INTEGER is a 64-bit type, so the schema freely stores int64 values in it
	// (duration_ns, cumulative_pause_ns, byte sizes, unix-ms stamps, lease
	// generations); a literal Postgres INTEGER is int4 and rejects anything past
	// 2^31-1, which made every test longer than ~2.1s unsavable. It runs after
	// postgresSQLRewrites so the BOOLEAN entries above claim their columns first,
	// and only on DDL: DML casts such as CAST(value AS INTEGER) are unchanged.
	// Existing databases are widened by ensurePostgresIntegerColumnsBigint.
	postgresDDLIntegerType = regexp.MustCompile(`(?i)\bINTEGER\b`)
)

func rewriteSQL(query string, dialect storeDialect) string {
	if dialect != storeDialectPostgres {
		return query
	}
	rewritten := query
	insertOrIgnore := insertOrIgnorePrefix.MatchString(rewritten)
	if insertOrIgnore {
		rewritten = insertOrIgnorePrefix.ReplaceAllString(rewritten, "${1}INSERT INTO ")
	}
	ddl := false
	rewriteSQLCodeSegments(rewritten, func(code string) string {
		upper := strings.ToUpper(code)
		ddl = ddl || strings.Contains(upper, "CREATE TABLE") || strings.Contains(upper, "ALTER TABLE")
		return code
	})
	rewritten = rewriteSQLCodeSegments(rewritten, func(code string) string {
		if ddl {
			code = stripPostgresForeignKeyReferences(code)
		}
		for _, rewrite := range postgresSQLRewrites {
			code = rewrite.pattern.ReplaceAllString(code, rewrite.replacement)
		}
		if ddl {
			code = postgresDDLIntegerType.ReplaceAllString(code, "BIGINT")
		}
		return code
	})
	if insertOrIgnore {
		rewritten = appendPostgresOnConflict(rewritten)
	}
	return rewriteQuestionPlaceholders(rewritten)
}

func appendPostgresOnConflict(query string) string {
	trimmed := strings.TrimRightFunc(query, unicode.IsSpace)
	trailing := query[len(trimmed):]
	if strings.HasSuffix(trimmed, ";") {
		return strings.TrimSuffix(trimmed, ";") + " ON CONFLICT DO NOTHING;" + trailing
	}
	return trimmed + " ON CONFLICT DO NOTHING" + trailing
}

// rewriteSQLCodeSegments applies transform only to executable SQL. Quoted
// values/identifiers, line and block comments, and PostgreSQL dollar-quoted
// bodies are copied byte-for-byte so dialect translation cannot corrupt user
// values or procedural bodies that happen to contain schema-like text.
func rewriteSQLCodeSegments(query string, transform func(string) string) string {
	var output strings.Builder
	output.Grow(len(query) + 16)
	codeStart := 0
	flushCode := func(end int) {
		if end > codeStart {
			output.WriteString(transform(query[codeStart:end]))
		}
	}
	for index := 0; index < len(query); {
		start := index
		var end int
		switch {
		case query[index] == '\'' || query[index] == '"':
			quote := query[index]
			index++
			for index < len(query) {
				if query[index] != quote {
					index++
					continue
				}
				index++
				if index < len(query) && query[index] == quote {
					index++
					continue
				}
				break
			}
			end = index
		case index+1 < len(query) && query[index:index+2] == "--":
			if newline := strings.IndexByte(query[index:], '\n'); newline >= 0 {
				end = index + newline + 1
			} else {
				end = len(query)
			}
			index = end
		case index+1 < len(query) && query[index:index+2] == "/*":
			index += 2
			depth := 1
			for index < len(query) && depth > 0 {
				switch {
				case index+1 < len(query) && query[index:index+2] == "/*":
					depth++
					index += 2
				case index+1 < len(query) && query[index:index+2] == "*/":
					depth--
					index += 2
				default:
					index++
				}
			}
			end = index
		case query[index] == '$':
			tagEnd := index + 1
			for tagEnd < len(query) && ((query[tagEnd] >= 'a' && query[tagEnd] <= 'z') || (query[tagEnd] >= 'A' && query[tagEnd] <= 'Z') || (query[tagEnd] >= '0' && query[tagEnd] <= '9') || query[tagEnd] == '_') {
				tagEnd++
			}
			if tagEnd >= len(query) || query[tagEnd] != '$' {
				index++
				continue
			}
			tag := query[index : tagEnd+1]
			bodyEnd := strings.Index(query[tagEnd+1:], tag)
			if bodyEnd < 0 {
				index++
				continue
			}
			end = tagEnd + 1 + bodyEnd + len(tag)
			index = end
		default:
			index++
			continue
		}
		flushCode(start)
		output.WriteString(query[start:end])
		codeStart = end
	}
	flushCode(len(query))
	return output.String()
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
	placeholder := 1
	return rewriteSQLCodeSegments(query, func(code string) string {
		var builder strings.Builder
		builder.Grow(len(code) + 8)
		for index := 0; index < len(code); index++ {
			if code[index] != '?' {
				builder.WriteByte(code[index])
				continue
			}
			builder.WriteByte('$')
			builder.WriteString(strconv.Itoa(placeholder))
			placeholder++
		}
		return builder.String()
	})
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
// as documented, gated extensions (see docs/ENTERPRISE_PLATFORM_RUNBOOK.md).
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
