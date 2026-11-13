package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/protocol/jsonextract"
	"github.com/jasonczech/apexload/pkg/scrub"
	_ "modernc.org/sqlite"
)

const (
	defaultDatabaseMaxRows         = 100
	hardDatabaseMaxRows            = 1_000
	hardDatabaseMaxColumns         = 256
	hardDatabaseColumnNameBytes    = 1 << 10
	hardDatabaseValueBytes         = 256 << 10
	hardDatabaseRetainedValueBytes = 1 << 20
	hardDatabaseResponseBytes      = 8 << 20
	maxDatabaseEvidenceColumns     = 16
	maxDatabaseEvidenceValueBytes  = 4 << 10
	hardDatabaseDSNBytes           = 16 << 10
	hardDatabaseSQLBytes           = 1 << 20
	hardDatabaseBindCount          = 1_024
	hardDatabaseBindValueBytes     = 1 << 20
	hardDatabaseBindBytes          = 8 << 20
)

func init() {
	protocol.Register("database", func() protocol.Protocol {
		return &Database{}
	})
}

type Database struct {
	db                       *sql.DB
	tx                       *sql.Tx
	driver                   string
	dsn                      string
	queryTimeout             time.Duration
	maxRows                  int
	readOnly                 bool
	rollbackOnTeardown       bool
	unredactedDebugSnapshots bool
	// retainAuthoredEvidence is the inverse of the script's config.log.redact_run_evidence
	// (protocol.RedactsAuthoredEvidence). Off, the authored SQL, request
	// headers, params and request body keep their values minus exact
	// platform-managed ones. Query RESULT cells are server content and keep the
	// column-name masking either way.
	retainAuthoredEvidence bool
	redactionMu            sync.RWMutex
	redactor               protocol.TextRedactor
	authoredRedactor       protocol.TextRedactor
}

var (
	_ protocol.RuntimeTextRedactorSetter         = (*Database)(nil)
	_ protocol.RuntimeAuthoredTextRedactorSetter = (*Database)(nil)
)

func (d *Database) SetRuntimeAuthoredTextRedactor(redactor protocol.TextRedactor) {
	d.redactionMu.Lock()
	d.authoredRedactor = redactor
	d.redactionMu.Unlock()
}

// redactAuthoredText scrubs script-authored REQUEST content: redactText with
// config.log.redact_run_evidence on; otherwise only the engine's exact runtime
// matcher and project rules.
func (d *Database) redactAuthoredText(value string) string {
	if !d.retainAuthoredEvidence {
		return d.redactText(value)
	}
	if value == "" {
		return ""
	}
	d.redactionMu.RLock()
	redactor := d.authoredRedactor
	d.redactionMu.RUnlock()
	if redactor != nil {
		return redactor(value)
	}
	return value
}

// redactAuthoredNamedValue masks an authored value under a sensitive NAME: always
// with the setting on, otherwise only when it carries an exact platform value.
func (d *Database) redactAuthoredNamedValue(value string) string {
	if !d.retainAuthoredEvidence {
		return scrub.Redacted
	}
	if scrubbed := d.redactAuthoredText(value); scrubbed != value {
		return scrub.Redacted
	}
	return value
}

func (d *Database) SetRuntimeTextRedactor(redactor protocol.TextRedactor) {
	d.redactionMu.Lock()
	d.redactor = redactor
	d.redactionMu.Unlock()
}

func (d *Database) currentRedactor() protocol.TextRedactor {
	d.redactionMu.RLock()
	redactor := d.redactor
	d.redactionMu.RUnlock()
	return redactor
}

func (d *Database) redactText(value string) string {
	if value == "" {
		return ""
	}
	// The configured DSN is not masked here by value: its credential parts reach
	// the engine runtime redactor as platform-managed values when the DSN is
	// secret-backed, and as script-sourced values only when the script sets
	// config.log.redact_run_evidence to true.
	out := value
	if redactor := d.currentRedactor(); redactor != nil {
		out = redactor(out)
	}
	return scrub.ValueLabel(out, scrub.Policy{})
}

func (d *Database) redactError(err error) error {
	if err == nil {
		return nil
	}
	msg := d.redactText(err.Error())
	msg = scrub.Value(msg, scrub.Policy{})
	return errors.New(msg)
}

func (d *Database) Name() string { return "database" }

func (d *Database) Init(ctx context.Context, config map[string]any) error {
	d.unredactedDebugSnapshots, _ = config[protocol.ReplayUnredactedDebugConfigKey].(bool)
	d.retainAuthoredEvidence = !protocol.RedactsAuthoredEvidence(config)
	configuredDriver := strings.ToLower(strings.TrimSpace(protocol.ConfigString(config, "database_driver")))
	if err := validateDatabaseDriver(configuredDriver); err != nil {
		return err
	}
	d.driver = normalizeDriver(configuredDriver)
	d.dsn = strings.TrimSpace(protocol.ConfigString(config, "database_dsn"))
	if d.dsn == "" {
		// buildProtoConfigWithSecretResolver resolves controller-managed secret
		// templates before protocol initialization and carries the result in the
		// dedicated secret field. Apply the target policy to that final DSN too.
		d.dsn = strings.TrimSpace(protocol.ConfigString(config, "database_dsn_secret"))
	}
	if d.driver == "" {
		return errors.New("database driver is required")
	}
	if d.dsn == "" {
		return errors.New("database dsn is required")
	}
	if len(d.dsn) > hardDatabaseDSNBytes {
		return fmt.Errorf("database dsn exceeds %d byte limit", hardDatabaseDSNBytes)
	}
	if d.driver == "sqlite" {
		if err := validateSQLiteMemoryDSN(d.dsn); err != nil {
			return err
		}
	}
	if err := protocol.ValidateTargetDSN(d.driver, d.dsn, protocol.TargetPolicyHosts(config)); err != nil {
		return err
	}
	queryTimeout, err := protocol.BoundedMilliseconds(config["database_query_timeout_ms"], 30*time.Second, dsl.MaxConfiguredProtocolTimeout)
	if err != nil {
		return fmt.Errorf("database query_timeout_ms %w", err)
	}
	db, err := sql.Open(d.driver, d.dsn)
	if err != nil {
		return d.redactError(err)
	}
	if maxOpen := protocol.ConfigInt(config, "database_max_open_conns"); maxOpen > 0 {
		db.SetMaxOpenConns(maxOpen)
	}
	if maxIdle := protocol.ConfigInt(config, "database_max_idle_conns"); maxIdle > 0 {
		db.SetMaxIdleConns(maxIdle)
	}
	if lifetimeMS := protocol.ConfigInt(config, "database_conn_max_lifetime_ms"); lifetimeMS > 0 {
		db.SetConnMaxLifetime(time.Duration(lifetimeMS) * time.Millisecond)
	}
	d.queryTimeout = queryTimeout
	d.maxRows = defaultDatabaseMaxRows
	if maxRows := protocol.ConfigInt(config, "database_max_rows"); maxRows > 0 {
		d.maxRows = min(maxRows, hardDatabaseMaxRows)
	}
	d.readOnly, _ = config["database_read_only"].(bool)
	d.rollbackOnTeardown, _ = config["database_rollback_on_teardown"].(bool)
	d.db = db
	if err := d.db.PingContext(ctx); err != nil {
		_ = d.db.Close()
		d.db = nil
		return d.redactError(err)
	}
	return nil
}

func (d *Database) Execute(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
	start := time.Now()
	result := &protocol.Result{
		ActionName:       protocol.ActionName(action),
		StartTime:        start,
		DependencyType:   "database",
		DependencyTarget: d.driver,
		Extractions:      make(map[string]string),
		Success:          true,
	}
	if action == nil {
		result.Success = false
		result.ErrorMessage = "database action is required"
		result.Duration = time.Since(start)
		return result, nil
	}
	if d.db == nil {
		result.Success = false
		result.ErrorMessage = "database protocol is not initialized"
		result.Duration = time.Since(start)
		return result, nil
	}

	actionType := strings.ToLower(strings.TrimSpace(action.Type))
	actionCtx := ctx
	cancel := func() {}
	if d.queryTimeout > 0 && actionType != "db_begin" && actionType != "database_begin" && actionType != "begin" && actionType != "db_commit" && actionType != "database_commit" && actionType != "commit" && actionType != "db_rollback" && actionType != "database_rollback" && actionType != "rollback" {
		actionCtx, cancel = context.WithTimeout(ctx, d.queryTimeout)
	}
	defer cancel()

	var (
		err             error
		evidencePayload map[string]any
		evidenceOmitted int
	)
	switch actionType {
	case "db_begin", "database_begin", "begin":
		err = d.begin(actionCtx)
		result.ResponseBody = `{"transaction":"begun"}`
		evidencePayload = map[string]any{"transaction": "begun"}
	case "db_commit", "database_commit", "commit":
		err = d.commit()
		result.ResponseBody = `{"transaction":"committed"}`
		evidencePayload = map[string]any{"transaction": "committed"}
	case "db_rollback", "database_rollback", "rollback":
		err = d.rollback()
		result.ResponseBody = `{"transaction":"rolled_back"}`
		evidencePayload = map[string]any{"transaction": "rolled_back"}
	case "db_health", "database_health", "ping":
		err = d.db.PingContext(actionCtx)
		result.ResponseBody = `{"status":"ok"}`
		evidencePayload = map[string]any{"status": "ok"}
	case "db_query", "database_query", "query", "select":
		result.ResponseBody, evidencePayload, evidenceOmitted, err = d.query(actionCtx, action)
	case "db_exec", "database_exec", "exec", "execute", "insert", "update", "delete", "db_procedure", "stored_procedure":
		result.ResponseBody, evidencePayload, err = d.exec(actionCtx, action)
	default:
		err = fmt.Errorf("unsupported database action type: %s", action.Type)
	}
	result.Duration = time.Since(start)
	result.BytesSent = int64(len(sqlText(action)))
	result.BytesReceived = int64(len(result.ResponseBody))
	result.StatusCode = 200
	if err != nil {
		result.Success = false
		result.StatusCode = 500
		result.ErrorMessage = d.redactError(err).Error()
	}
	if result.Success {
		if extractorErr := runDatabaseExtractors(action.Extractors, result); extractorErr != "" {
			result.Success = false
			result.StatusCode = 500
			result.ErrorMessage = d.redactText(extractorErr)
		}
	}
	if result.Success {
		if assertionErr := d.evaluateDatabaseAssertions(action.Assertions, result, result.ResponseBody); assertionErr != "" {
			result.Success = false
			result.StatusCode = 500
			result.ErrorMessage = d.redactText(assertionErr)
		}
	}
	if !d.unredactedDebugSnapshots {
		d.scrubQueryOutputs(result, evidencePayload)
	}
	if !result.Success {
		result.Snapshot = d.databaseSnapshot(action, result)
	}
	result.ServerTelemetry = &protocol.ServerTelemetry{
		ServerProcessingMs: float64(result.Duration.Microseconds()) / 1000,
		RequestPayloadKB:   float64(result.BytesSent) / 1024,
		ResponsePayloadKB:  float64(result.BytesReceived) / 1024,
	}
	evidenceResult := *result
	if len(evidencePayload) > 0 {
		if compact, marshalErr := json.Marshal(evidencePayload); marshalErr == nil {
			evidenceResult.ResponseBody = string(compact)
		} else {
			evidenceResult.ResponseBody = ""
		}
	} else {
		evidenceResult.ResponseBody = ""
	}
	evidenceAction := action
	if !d.unredactedDebugSnapshots {
		copied := *action
		rawTarget := strings.TrimSpace(action.RedactedTarget)
		if rawTarget == "" {
			rawTarget = sqlText(action)
		}
		copied.RedactedTarget = d.redactAuthoredText(rawTarget)
		copied.RedactedHeaders = d.redactHeaders(action)
		copied.RedactedParams = d.redactParamsMap(action.RedactedParams, action.Params)
		evidenceAction = &copied
	}
	result.ProtocolEvidence = protocol.BuildProtocolEvidence(evidenceAction, &evidenceResult)
	result.ProtocolEvidenceOmittedCount = evidenceResult.ProtocolEvidenceOmittedCount + evidenceOmitted
	return result, nil
}

func (d *Database) Teardown(ctx context.Context) error {
	if d.tx != nil {
		if d.rollbackOnTeardown {
			_ = d.tx.Rollback()
		}
		d.tx = nil
	}
	if d.db != nil {
		err := d.db.Close()
		d.db = nil
		return err
	}
	return nil
}

func (d *Database) HealthCheck(ctx context.Context) error {
	if d.db == nil {
		return nil
	}
	return d.redactError(d.db.PingContext(ctx))
}

const maxDatabaseCellScrubDepth = 8

func (d *Database) scrubCellValue(column string, value any) (any, bool) {
	return d.scrubCellValueDepth(column, value, 0)
}

func (d *Database) scrubCellValueDepth(column string, value any, depth int) (any, bool) {
	if scrub.SensitiveName(column) {
		if value == nil {
			return nil, false
		}
		if text, ok := value.(string); ok && text == scrub.Redacted {
			return value, false
		}
		return scrub.Redacted, true
	}
	switch typed := value.(type) {
	case nil, bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return value, false
	case json.Number:
		raw := string(typed)
		redacted := d.redactText(raw)
		if redacted != raw {
			return redacted, true
		}
		return value, false
	case string:
		redacted := d.redactText(typed)
		return redacted, redacted != typed
	case []byte:
		raw := string(typed)
		redacted := d.redactText(raw)
		if redacted != raw {
			return []byte(redacted), true
		}
		return value, false
	case []string:
		if depth >= maxDatabaseCellScrubDepth {
			return scrub.Redacted, true
		}
		var out []string
		changed := false
		for i, elem := range typed {
			redacted := d.redactText(elem)
			if redacted != elem {
				if !changed {
					out = append([]string(nil), typed...)
					changed = true
				}
				out[i] = redacted
			}
		}
		if changed {
			return out, true
		}
		return value, false
	case []any:
		if depth >= maxDatabaseCellScrubDepth {
			return scrub.Redacted, true
		}
		var out []any
		changed := false
		for i, elem := range typed {
			next, elemChanged := d.scrubCellValueDepth("", elem, depth+1)
			if elemChanged {
				if !changed {
					out = append([]any(nil), typed...)
					changed = true
				}
				out[i] = next
			}
		}
		if changed {
			return out, true
		}
		return value, false
	case map[string]string:
		if depth >= maxDatabaseCellScrubDepth {
			return scrub.Redacted, true
		}
		var out map[string]string
		changed := false
		for k, elem := range typed {
			next, elemChanged := d.scrubCellValueDepth(k, elem, depth+1)
			if elemChanged {
				if !changed {
					out = make(map[string]string, len(typed))
					for origK, origV := range typed {
						out[origK] = origV
					}
					changed = true
				}
				if str, ok := next.(string); ok {
					out[k] = str
				} else {
					out[k] = scrub.Redacted
				}
			}
		}
		if changed {
			return out, true
		}
		return value, false
	case map[string]any:
		if depth >= maxDatabaseCellScrubDepth {
			return scrub.Redacted, true
		}
		var out map[string]any
		changed := false
		for k, elem := range typed {
			next, elemChanged := d.scrubCellValueDepth(k, elem, depth+1)
			if elemChanged {
				if !changed {
					out = make(map[string]any, len(typed))
					for origK, origV := range typed {
						out[origK] = origV
					}
					changed = true
				}
				out[k] = next
			}
		}
		if changed {
			return out, true
		}
		return value, false
	default:
		if depth >= maxDatabaseCellScrubDepth {
			return scrub.Redacted, true
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return scrub.Redacted, true
		}
		var generic any
		if err := jsonextract.DecodeExact(encoded, &generic); err != nil {
			return scrub.Redacted, true
		}
		next, genericChanged := d.scrubCellValueDepth(column, generic, depth+1)
		if genericChanged {
			return next, true
		}
		return value, false
	}
}

func firstDatabasePayloadColumn(payload map[string]any) string {
	switch cols := payload["columns"].(type) {
	case []string:
		if len(cols) > 0 {
			return cols[0]
		}
	case []any:
		if len(cols) > 0 {
			col, _ := cols[0].(string)
			return col
		}
	}
	return ""
}

func (d *Database) scrubQueryPayloadMap(payload map[string]any) bool {
	if len(payload) == 0 {
		return false
	}
	firstCol := firstDatabasePayloadColumn(payload)
	changed := false
	scrubRow := func(row map[string]any) {
		for col, val := range row {
			if next, cellChanged := d.scrubCellValue(col, val); cellChanged {
				row[col] = next
				changed = true
			}
		}
	}
	switch rows := payload["rows"].(type) {
	case []map[string]any:
		for _, row := range rows {
			scrubRow(row)
		}
	case []any:
		for _, entry := range rows {
			if row, ok := entry.(map[string]any); ok {
				scrubRow(row)
			}
		}
	}
	if firstRow, ok := payload["first_row"].(map[string]any); ok {
		scrubRow(firstRow)
	}
	if scalar, exists := payload["scalar"]; exists {
		if next, scalarChanged := d.scrubCellValue(firstCol, scalar); scalarChanged {
			payload["scalar"] = next
			changed = true
		}
	}
	return changed
}

func (d *Database) scrubQueryOutputs(result *protocol.Result, evidencePayload map[string]any) {
	d.scrubQueryPayloadMap(evidencePayload)
	if result == nil || result.ResponseBody == "" {
		return
	}
	payload, err := decodeDatabasePayload(result.ResponseBody)
	if err != nil {
		result.ResponseBody = d.redactText(result.ResponseBody)
		return
	}
	if d.scrubQueryPayloadMap(payload) {
		if encoded, encodeErr := encodeDatabasePayload(payload); encodeErr == nil {
			result.ResponseBody = encoded
		} else {
			result.ResponseBody = scrub.Redacted
		}
	}
}

func (d *Database) begin(ctx context.Context) error {
	if d.tx != nil {
		return errors.New("database transaction is already active")
	}
	tx, err := d.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: d.readOnly})
	if err != nil {
		return err
	}
	d.tx = tx
	return nil
}

func (d *Database) commit() error {
	if d.tx == nil {
		return errors.New("database transaction is not active")
	}
	err := d.tx.Commit()
	d.tx = nil
	return err
}

func (d *Database) rollback() error {
	if d.tx == nil {
		return errors.New("database transaction is not active")
	}
	err := d.tx.Rollback()
	d.tx = nil
	return err
}

func (d *Database) query(ctx context.Context, action *protocol.Action) (string, map[string]any, int, error) {
	query := sqlText(action)
	if query == "" {
		return "", nil, 0, errors.New("database query SQL is required")
	}
	if err := d.validateStatement(query); err != nil {
		return "", nil, 0, err
	}
	if d.readOnly {
		if err := validateReadOnlySQL(query); err != nil {
			return "", nil, 0, err
		}
	}
	standaloneTx, err := d.beginStandaloneReadOnly(ctx)
	if err != nil {
		return "", nil, 0, fmt.Errorf("begin driver read-only transaction: %w", err)
	}
	if standaloneTx != nil {
		defer standaloneTx.Rollback()
	}
	binds, err := boundedActionBinds(action)
	if err != nil {
		return "", nil, 0, err
	}
	var rows *sql.Rows
	if standaloneTx != nil {
		rows, err = standaloneTx.QueryContext(ctx, query, binds...)
	} else {
		rows, err = d.queryContext(ctx, query, binds...)
	}
	if err != nil {
		return "", nil, 0, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return "", nil, 0, err
	}
	if len(columns) > hardDatabaseMaxColumns {
		return "", nil, 0, fmt.Errorf("database result has %d columns, hard limit is %d", len(columns), hardDatabaseMaxColumns)
	}
	var retainedBytes int64
	for _, column := range columns {
		if len(column) > hardDatabaseColumnNameBytes {
			return "", nil, 0, fmt.Errorf("database column name exceeds %d bytes", hardDatabaseColumnNameBytes)
		}
		retainedBytes += int64(len(column))
	}
	maxRows := d.maxRows
	if override := actionParamInt(action.Params, "max_rows"); override > 0 {
		if maxRows <= 0 || override < maxRows {
			maxRows = override
		}
	}
	if maxRows <= 0 || maxRows > hardDatabaseMaxRows {
		maxRows = hardDatabaseMaxRows
	}
	values := make([]any, len(columns))
	scanTargets := make([]any, len(columns))
	for i := range values {
		scanTargets[i] = &values[i]
	}
	outRows := make([]map[string]any, 0)
	for rows.Next() {
		if maxRows > 0 && len(outRows) >= maxRows {
			break
		}
		if err := rows.Scan(scanTargets...); err != nil {
			return "", nil, 0, err
		}
		item := make(map[string]any, len(columns))
		for i, column := range columns {
			normalized, retained, err := normalizeSQLValueBounded(values[i])
			if err != nil {
				return "", nil, 0, fmt.Errorf("database column %q: %w", column, err)
			}
			if retainedBytes > hardDatabaseRetainedValueBytes-retained {
				return "", nil, 0, fmt.Errorf("database result retained values exceed %d bytes", hardDatabaseRetainedValueBytes)
			}
			retainedBytes += retained
			item[column] = normalized
		}
		outRows = append(outRows, item)
	}
	if err := rows.Err(); err != nil {
		return "", nil, 0, err
	}
	payload := map[string]any{
		"columns":   columns,
		"row_count": len(outRows),
		"rows":      outRows,
	}
	if len(outRows) > 0 {
		payload["first_row"] = outRows[0]
		if len(columns) > 0 {
			payload["scalar"] = outRows[0][columns[0]]
		}
	}
	body, err := encodeDatabasePayload(payload)
	if err != nil {
		return "", nil, 0, err
	}
	if err := rows.Close(); err != nil {
		return "", nil, 0, err
	}
	if standaloneTx != nil {
		if err := standaloneTx.Commit(); err != nil {
			return "", nil, 0, fmt.Errorf("commit driver read-only transaction: %w", err)
		}
	}
	evidence, omitted := compactDatabaseQueryEvidence(columns, outRows)
	return body, evidence, omitted, nil
}

func (d *Database) exec(ctx context.Context, action *protocol.Action) (string, map[string]any, error) {
	statement := sqlText(action)
	if statement == "" {
		return "", nil, errors.New("database exec SQL is required")
	}
	if err := d.validateStatement(statement); err != nil {
		return "", nil, err
	}
	if d.readOnly {
		if err := validateReadOnlySQL(statement); err != nil {
			return "", nil, err
		}
	}
	standaloneTx, err := d.beginStandaloneReadOnly(ctx)
	if err != nil {
		return "", nil, fmt.Errorf("begin driver read-only transaction: %w", err)
	}
	if standaloneTx != nil {
		defer standaloneTx.Rollback()
	}
	binds, err := boundedActionBinds(action)
	if err != nil {
		return "", nil, err
	}
	var res sql.Result
	if standaloneTx != nil {
		res, err = standaloneTx.ExecContext(ctx, statement, binds...)
	} else {
		res, err = d.execContext(ctx, statement, binds...)
	}
	if err != nil {
		return "", nil, err
	}
	rowsAffected, _ := res.RowsAffected()
	lastInsertID, _ := res.LastInsertId()
	payload := map[string]any{
		"rows_affected":  rowsAffected,
		"last_insert_id": lastInsertID,
	}
	body, err := encodeDatabasePayload(payload)
	if err != nil {
		return "", nil, err
	}
	if standaloneTx != nil {
		if err := standaloneTx.Commit(); err != nil {
			return "", nil, fmt.Errorf("commit driver read-only transaction: %w", err)
		}
	}
	return body, payload, nil
}

func (d *Database) beginStandaloneReadOnly(ctx context.Context) (*sql.Tx, error) {
	if !d.readOnly || d.tx != nil {
		return nil, nil
	}
	return d.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
}

func (d *Database) queryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if d.tx != nil {
		return d.tx.QueryContext(ctx, query, args...)
	}
	return d.db.QueryContext(ctx, query, args...)
}

func (d *Database) execContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if d.tx != nil {
		return d.tx.ExecContext(ctx, query, args...)
	}
	return d.db.ExecContext(ctx, query, args...)
}

func normalizeDriver(driver string) string {
	driver = strings.ToLower(strings.TrimSpace(driver))
	switch driver {
	case "sqlite3":
		return "sqlite"
	case "postgres":
		// pgx is the bundled PostgreSQL database/sql implementation. Keep the
		// public DSL spelling "postgres" stable while selecting the registered
		// runtime driver internally.
		return "pgx"
	default:
		return driver
	}
}

func validateDatabaseDriver(driver string) error {
	switch driver {
	case "sqlite", "sqlite3", "postgres", "mysql", "odbc", "oracle":
		return nil
	default:
		return fmt.Errorf("unsupported database driver: %s", driver)
	}
}

func validateSQLiteMemoryDSN(dsn string) error {
	if dsn == ":memory:" {
		return nil
	}
	parsed, err := url.Parse(dsn)
	if err != nil || !strings.EqualFold(parsed.Scheme, "file") || parsed.Host != "" || parsed.User != nil || parsed.Fragment != "" {
		return errors.New("SQLite database_dsn must be :memory: or a named in-memory file URI")
	}
	name := parsed.Opaque
	if name == "" {
		name = parsed.Path
	}
	name, err = url.PathUnescape(name)
	if err != nil || name == "" || len(name) > 1<<10 || strings.ContainsAny(name, "/\\\x00") {
		return errors.New("SQLite named in-memory database identifier is invalid")
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return fmt.Errorf("parse SQLite database_dsn query: %w", err)
	}
	for key, values := range query {
		if key != "mode" && key != "cache" {
			return fmt.Errorf("SQLite named in-memory database_dsn option %q is not allowed", key)
		}
		if len(values) != 1 {
			return fmt.Errorf("SQLite named in-memory database_dsn option %q must appear once", key)
		}
	}
	if name != ":memory:" {
		if values := query["mode"]; len(values) != 1 || values[0] != "memory" {
			return errors.New("SQLite file URIs must set mode=memory")
		}
	} else if values := query["mode"]; len(values) == 1 && values[0] != "memory" {
		return errors.New("SQLite file URIs may only use mode=memory")
	}
	if values := query["cache"]; len(values) == 1 && values[0] != "shared" && values[0] != "private" {
		return errors.New("SQLite in-memory cache must be shared or private")
	}
	return nil
}

func (d *Database) validateStatement(statement string) error {
	if len(statement) > hardDatabaseSQLBytes {
		return fmt.Errorf("database SQL exceeds %d byte limit", hardDatabaseSQLBytes)
	}
	if d.driver == "sqlite" {
		return validateSQLiteSQLSafety(statement)
	}
	return nil
}

func validateSQLiteSQLSafety(statement string) error {
	tokens, multiple, err := sqlTokens(statement)
	if err != nil {
		return fmt.Errorf("SQLite safety policy rejected SQL: %w", err)
	}
	if multiple || len(tokens) == 0 {
		return errors.New("SQLite safety policy requires exactly one SQL statement")
	}
	quoted, err := sqlQuotedIdentifierTokens(statement)
	if err != nil {
		return fmt.Errorf("SQLite safety policy rejected SQL: %w", err)
	}
	tokens = append(tokens, quoted...)
	blocked := map[string]struct{}{
		"attach": {}, "detach": {}, "vacuum": {}, "pragma": {},
		"load_extension": {}, "readfile": {}, "writefile": {}, "fsdir": {},
		"fileio": {}, "zipfile": {}, "sqlite_dbpage": {}, "sqlite_dbdata": {},
		"sqlite_dbptr": {}, "virtual": {},
	}
	for _, token := range tokens {
		if _, denied := blocked[token]; denied || strings.HasPrefix(token, "pragma_") {
			return fmt.Errorf("SQLite safety policy blocks filesystem-capable SQL token %q", token)
		}
	}
	return nil
}

func sqlText(action *protocol.Action) string {
	if action == nil {
		return ""
	}
	if target := strings.TrimSpace(action.Target); target != "" {
		return target
	}
	return strings.TrimSpace(string(action.Body))
}

func boundedActionBinds(action *protocol.Action) ([]any, error) {
	if action == nil || action.Params == nil {
		return nil, nil
	}
	value, ok := action.Params["binds"]
	if !ok {
		return nil, nil
	}
	var binds []any
	switch typed := value.(type) {
	case []any:
		binds = typed
	case []string:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = item
		}
		binds = out
	case map[string]any:
		if len(typed) > hardDatabaseBindCount {
			return nil, fmt.Errorf("database bind count exceeds %d", hardDatabaseBindCount)
		}
		out := make([]any, 0, len(typed))
		keys := make([]int, 0, len(typed))
		indexed := make(map[int]any)
		for key, item := range typed {
			var index int
			if _, err := fmt.Sscanf(key, "%d", &index); err == nil {
				keys = append(keys, index)
				indexed[index] = item
			}
		}
		sortInts(keys)
		for _, key := range keys {
			out = append(out, indexed[key])
		}
		binds = out
	default:
		binds = []any{typed}
	}
	if len(binds) > hardDatabaseBindCount {
		return nil, fmt.Errorf("database bind count exceeds %d", hardDatabaseBindCount)
	}
	retained := int64(0)
	for i, bind := range binds {
		size := int64(16)
		switch typed := bind.(type) {
		case string:
			size = int64(len(typed))
		case []byte:
			size = int64(len(typed))
		}
		if size > hardDatabaseBindValueBytes {
			return nil, fmt.Errorf("database bind %d exceeds %d byte limit", i, hardDatabaseBindValueBytes)
		}
		if size > hardDatabaseBindBytes-retained {
			return nil, fmt.Errorf("database binds exceed %d byte aggregate limit", hardDatabaseBindBytes)
		}
		retained += size
	}
	return binds, nil
}

func normalizeSQLValue(value any) any {
	switch typed := value.(type) {
	case []byte:
		return string(typed)
	case time.Time:
		return typed.Format(time.RFC3339Nano)
	default:
		return typed
	}
}

func normalizeSQLValueBounded(value any) (any, int64, error) {
	switch typed := value.(type) {
	case []byte:
		if len(typed) > hardDatabaseValueBytes {
			return nil, 0, fmt.Errorf("value exceeds %d bytes", hardDatabaseValueBytes)
		}
		return string(typed), int64(len(typed)), nil
	case string:
		if len(typed) > hardDatabaseValueBytes {
			return nil, 0, fmt.Errorf("value exceeds %d bytes", hardDatabaseValueBytes)
		}
		return typed, int64(len(typed)), nil
	case time.Time:
		formatted := typed.Format(time.RFC3339Nano)
		return formatted, int64(len(formatted)), nil
	case nil:
		return nil, 0, nil
	default:
		return normalizeSQLValue(value), 16, nil
	}
}

func encodeDatabasePayload(value map[string]any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	if len(data) > hardDatabaseResponseBytes {
		return "", fmt.Errorf("database response exceeds %d bytes", hardDatabaseResponseBytes)
	}
	return string(data), nil
}

func compactDatabaseQueryEvidence(columns []string, rows []map[string]any) (map[string]any, int) {
	evidence := map[string]any{"row_count": len(rows)}
	columnLimit := min(len(columns), maxDatabaseEvidenceColumns)
	omitted := len(columns) - columnLimit + max(0, len(rows)-1)
	evidenceColumns := append([]string(nil), columns[:columnLimit]...)
	evidence["columns"] = evidenceColumns
	if len(rows) == 0 || columnLimit == 0 {
		return evidence, omitted
	}
	first := make(map[string]any, columnLimit)
	for _, column := range evidenceColumns {
		value := rows[0][column]
		if text, ok := value.(string); ok && len(text) > maxDatabaseEvidenceValueBytes {
			omitted++
		}
		first[column] = compactDatabaseEvidenceValue(value)
	}
	evidence["first_row"] = first
	scalar := rows[0][columns[0]]
	if text, ok := scalar.(string); ok && len(text) > maxDatabaseEvidenceValueBytes {
		omitted++
	}
	evidence["scalar"] = compactDatabaseEvidenceValue(scalar)
	return evidence, omitted
}

func compactDatabaseEvidenceValue(value any) any {
	text, ok := value.(string)
	if !ok || len(text) <= maxDatabaseEvidenceValueBytes {
		return value
	}
	return text[:maxDatabaseEvidenceValueBytes]
}

func runDatabaseExtractors(extractors []protocol.Extractor, result *protocol.Result) string {
	if len(extractors) == 0 {
		return ""
	}
	payload, err := decodeDatabasePayload(result.ResponseBody)
	if err != nil {
		return err.Error()
	}
	for _, extractor := range extractors {
		value, err := extractDatabaseValue(extractor, payload, []byte(result.ResponseBody))
		if err != nil {
			return err.Error()
		}
		if extractor.Name != "" {
			if err := protocol.StoreExtraction(result, extractor.Name, value); err != nil {
				result.Extractions = nil
				return fmt.Sprintf("database extractor %q output rejected: %v", extractor.Name, err)
			}
		}
	}
	return ""
}

func extractDatabaseValue(extractor protocol.Extractor, payload map[string]any, body []byte) (string, error) {
	source := strings.ToLower(strings.TrimSpace(extractor.Source))
	switch source {
	case "", "body", "result", "rows":
		return extractDatabaseBodyValue(extractor, body)
	case "scalar":
		return jsonextract.Stringify(payload["scalar"]), nil
	case "row_count", "rows_affected", "last_insert_id":
		return jsonextract.Stringify(payload[source]), nil
	default:
		return "", fmt.Errorf("unsupported database extraction source: %s", extractor.Source)
	}
}

func extractDatabaseBodyValue(extractor protocol.Extractor, body []byte) (string, error) {
	switch strings.ToLower(strings.TrimSpace(extractor.Type)) {
	case "", "json", "jsonpath":
		return jsonextract.JSONPath(extractor.Expression, body)
	case "jmespath":
		return jsonextract.JMESPath(extractor.Expression, body)
	case "regex", "regexp":
		return jsonextract.Regex(extractor.Expression, body)
	case "boundary":
		return jsonextract.Boundary(extractor.Expression, body)
	default:
		return "", fmt.Errorf("unsupported database extraction type: %s", extractor.Type)
	}
}

func (d *Database) evaluateDatabaseAssertions(assertions []protocol.Assertion, result *protocol.Result, body string) string {
	if len(assertions) == 0 {
		return ""
	}
	var payload map[string]any
	_ = jsonextract.DecodeExact([]byte(body), &payload)
	for _, assertion := range assertions {
		switch strings.ToLower(strings.TrimSpace(assertion.Type)) {
		case "row_count":
			actual := numericPayloadValue(payload, "row_count")
			expected, ok := numericAssertionValue(assertion.Expected)
			if ok && actual != expected {
				return fmt.Sprintf("row_count assertion failed: got %d, want %d", actual, expected)
			}
			if assertion.Max > 0 && actual > assertion.Max {
				return fmt.Sprintf("row_count assertion failed: got %d, max %d", actual, assertion.Max)
			}
		case "rows_affected":
			actual := numericPayloadValue(payload, "rows_affected")
			expected, ok := numericAssertionValue(assertion.Expected)
			if ok && actual != expected {
				return fmt.Sprintf("rows_affected assertion failed: got %d, want %d", actual, expected)
			}
			if assertion.Max > 0 && actual > assertion.Max {
				return fmt.Sprintf("rows_affected assertion failed: got %d, max %d", actual, assertion.Max)
			}
		case "scalar_equals":
			actual := jsonextract.Stringify(payload["scalar"])
			expected := strings.TrimSpace(fmt.Sprint(assertion.Expected))
			if actual != expected {
				reportedActual := actual
				if d != nil && !d.unredactedDebugSnapshots {
					scrubbedScalar, _ := d.scrubCellValue(firstDatabasePayloadColumn(payload), payload["scalar"])
					reportedActual = jsonextract.Stringify(scrubbedScalar)
				}
				return fmt.Sprintf("scalar_equals assertion failed: got %q, want %q", reportedActual, expected)
			}
		case "body_contains":
			if !strings.Contains(body, assertion.Value) {
				return fmt.Sprintf("body_contains assertion failed: %q not found", assertion.Value)
			}
		case "body_not_contains":
			if strings.Contains(body, assertion.Value) {
				return fmt.Sprintf("body_not_contains assertion failed: %q found", assertion.Value)
			}
		case "response_time_ms":
			if assertion.Max > 0 && result != nil && result.Duration > time.Duration(assertion.Max)*time.Millisecond {
				return fmt.Sprintf("response_time_ms assertion failed: got %.1f ms, max %d", float64(result.Duration.Microseconds())/1000, assertion.Max)
			}
		}
	}
	return ""
}

func decodeDatabasePayload(body string) (map[string]any, error) {
	var payload map[string]any
	if err := jsonextract.DecodeExact([]byte(body), &payload); err != nil {
		return nil, fmt.Errorf("decode database response: %w", err)
	}
	return payload, nil
}

func (d *Database) redactHeaders(action *protocol.Action) map[string]string {
	if action == nil || (len(action.Headers) == 0 && len(action.RedactedHeaders) == 0) {
		return nil
	}
	out := make(map[string]string, len(action.Headers)+len(action.RedactedHeaders))
	policy := scrub.Policy{}
	for key, value := range action.Headers {
		if policy.SensitiveHeader(key) {
			out[key] = d.redactAuthoredNamedValue(value)
		} else {
			out[key] = d.redactAuthoredText(value)
		}
	}
	for key, value := range action.RedactedHeaders {
		if policy.SensitiveHeader(key) {
			out[key] = d.redactAuthoredNamedValue(value)
		} else {
			out[key] = d.redactAuthoredText(value)
		}
	}
	return out
}

func (d *Database) redactParamsMap(redacted, raw map[string]any) map[string]any {
	src := raw
	if len(redacted) > 0 {
		src = redacted
	}
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]any, len(src))
	for k, v := range src {
		if scrub.SensitiveName(k) {
			if text, ok := v.(string); ok {
				out[k] = d.redactAuthoredNamedValue(text)
				continue
			}
			if !d.retainAuthoredEvidence {
				out[k] = scrub.Redacted
				continue
			}
		}
		out[k] = d.redactParamValue(v, 0)
	}
	return out
}

func (d *Database) redactParamValue(v any, depth int) any {
	if v == nil {
		return nil
	}
	if depth > 8 {
		return scrub.Redacted
	}
	switch typed := v.(type) {
	case string:
		return d.redactAuthoredText(typed)
	case []string:
		cp := make([]string, len(typed))
		for i, item := range typed {
			cp[i] = d.redactAuthoredText(item)
		}
		return cp
	case []any:
		cp := make([]any, len(typed))
		for i, item := range typed {
			cp[i] = d.redactParamValue(item, depth+1)
		}
		return cp
	case map[string]string:
		cp := make(map[string]string, len(typed))
		for k, item := range typed {
			if scrub.SensitiveName(k) {
				cp[k] = d.redactAuthoredNamedValue(item)
			} else {
				cp[k] = d.redactAuthoredText(item)
			}
		}
		return cp
	case map[string]any:
		cp := make(map[string]any, len(typed))
		for k, item := range typed {
			if text, ok := item.(string); ok && scrub.SensitiveName(k) {
				cp[k] = d.redactAuthoredNamedValue(text)
			} else if scrub.SensitiveName(k) && !d.retainAuthoredEvidence {
				cp[k] = scrub.Redacted
			} else {
				cp[k] = d.redactParamValue(item, depth+1)
			}
		}
		return cp
	default:
		return v
	}
}

func (d *Database) databaseSnapshot(action *protocol.Action, result *protocol.Result) *protocol.ErrorSnapshot {
	if action == nil || result == nil {
		return nil
	}
	if d.unredactedDebugSnapshots {
		headers := make(map[string]string, len(action.Headers))
		for k, v := range action.Headers {
			headers[k] = v
		}
		if len(headers) == 0 {
			headers = nil
		}
		return &protocol.ErrorSnapshot{
			RequestMethod:  action.Type,
			RequestURL:     sqlText(action),
			RequestHeaders: headers,
			RequestBody:    string(action.Body),
			ResponseStatus: result.StatusCode,
			ResponseBody:   result.ResponseBody,
		}
	}
	requestURL := strings.TrimSpace(action.RedactedTarget)
	if requestURL == "" {
		requestURL = sqlText(action)
	}
	requestBody := string(action.Body)
	if len(action.RedactedBody) > 0 {
		requestBody = string(action.RedactedBody)
	}
	return &protocol.ErrorSnapshot{
		RequestMethod:  action.Type,
		RequestURL:     d.redactAuthoredText(requestURL),
		RequestHeaders: d.redactHeaders(action),
		RequestBody:    d.redactAuthoredText(requestBody),
		ResponseStatus: result.StatusCode,
		ResponseBody:   d.redactText(result.ResponseBody),
	}
}

func numericPayloadValue(payload map[string]any, key string) int {
	if payload == nil {
		return 0
	}
	value, _ := numericAssertionValue(payload[key])
	return value
}

func numericAssertionValue(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	case json.Number:
		value, err := typed.Int64()
		return int(value), err == nil
	default:
		return 0, false
	}
}

func validateReadOnlySQL(statement string) error {
	tokens, multiple, err := sqlTokens(statement)
	if err != nil || multiple || len(tokens) == 0 {
		return errors.New("database read_only mode requires one read-only SELECT statement")
	}
	if tokens[0] != "select" && tokens[0] != "with" {
		return errors.New("database read_only mode blocks non-SELECT SQL")
	}
	blocked := map[string]struct{}{
		"insert": {}, "update": {}, "delete": {}, "merge": {}, "create": {},
		"into":  {},
		"alter": {}, "drop": {}, "truncate": {}, "replace": {}, "attach": {},
		"detach": {}, "vacuum": {}, "reindex": {}, "analyze": {}, "pragma": {},
		"call": {}, "do": {}, "copy": {}, "grant": {}, "revoke": {}, "set": {},
		"reset": {}, "begin": {}, "commit": {}, "rollback": {}, "savepoint": {},
		"release": {}, "lock": {}, "refresh": {},
	}
	foundSelect := false
	for _, token := range tokens {
		if token == "select" {
			foundSelect = true
		}
		if _, denied := blocked[token]; denied {
			return errors.New("database read_only mode blocks mutating or administrative SQL")
		}
	}
	if !foundSelect {
		return errors.New("database read_only mode requires a SELECT statement")
	}
	return nil
}

// sqlTokens extracts unquoted identifiers while rejecting more than one SQL
// statement. Comments and quoted strings are ignored so they cannot disguise a
// mutating leading token or a statement separator.
func sqlTokens(statement string) ([]string, bool, error) {
	var tokens []string
	var word strings.Builder
	flush := func() {
		if word.Len() > 0 {
			tokens = append(tokens, strings.ToLower(word.String()))
			word.Reset()
		}
	}
	terminated := false
	for i := 0; i < len(statement); {
		c := statement[i]
		if c == '-' && i+1 < len(statement) && statement[i+1] == '-' {
			flush()
			i += 2
			for i < len(statement) && statement[i] != '\n' {
				i++
			}
			continue
		}
		if c == '/' && i+1 < len(statement) && statement[i+1] == '*' {
			flush()
			// MySQL/MariaDB execute versioned comments (/*!...*/ and /*M!...*/)
			// as SQL, and several dialects interpret /*+...*/ optimizer comments.
			// Treat all of these as executable input rather than stripping them as
			// ordinary comments, otherwise mutating clauses can bypass read_only.
			if i+2 < len(statement) && (statement[i+2] == '!' || statement[i+2] == '+' ||
				i+3 < len(statement) && (statement[i+2] == 'M' || statement[i+2] == 'm') && statement[i+3] == '!') {
				return nil, false, errors.New("executable SQL comments are not allowed in database read_only mode")
			}
			end := strings.Index(statement[i+2:], "*/")
			if end < 0 {
				return nil, false, errors.New("unterminated SQL comment")
			}
			i += end + 4
			continue
		}
		if c == '\'' || c == '"' || c == '`' || c == '[' {
			flush()
			if terminated {
				return tokens, true, nil
			}
			closeChar := c
			if c == '[' {
				closeChar = ']'
			}
			i++
			closed := false
			for i < len(statement) {
				if statement[i] == closeChar {
					if closeChar != ']' && i+1 < len(statement) && statement[i+1] == closeChar {
						i += 2
						continue
					}
					i++
					closed = true
					break
				}
				i++
			}
			if !closed {
				return nil, false, errors.New("unterminated SQL quote")
			}
			continue
		}
		if c == ';' {
			flush()
			if terminated {
				return tokens, true, nil
			}
			terminated = true
			i++
			continue
		}
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' || (word.Len() > 0 && c >= '0' && c <= '9') {
			if terminated {
				return tokens, true, nil
			}
			word.WriteByte(c)
			i++
			continue
		}
		flush()
		if terminated && c != ' ' && c != '\t' && c != '\r' && c != '\n' {
			return tokens, true, nil
		}
		i++
	}
	flush()
	return tokens, false, nil
}

// sqlQuotedIdentifierTokens extracts quoted identifiers so dangerous SQLite
// functions cannot be hidden behind "...", `...`, or [...] quoting. String
// literals and comments remain ignored.
func sqlQuotedIdentifierTokens(statement string) ([]string, error) {
	var tokens []string
	appendWords := func(value string) {
		var word strings.Builder
		flush := func() {
			if word.Len() > 0 {
				tokens = append(tokens, strings.ToLower(word.String()))
				word.Reset()
			}
		}
		for i := 0; i < len(value); i++ {
			c := value[i]
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' || (word.Len() > 0 && c >= '0' && c <= '9') {
				word.WriteByte(c)
			} else {
				flush()
			}
		}
		flush()
	}
	for i := 0; i < len(statement); {
		switch statement[i] {
		case '-':
			if i+1 < len(statement) && statement[i+1] == '-' {
				i += 2
				for i < len(statement) && statement[i] != '\n' {
					i++
				}
				continue
			}
		case '/':
			if i+1 < len(statement) && statement[i+1] == '*' {
				end := strings.Index(statement[i+2:], "*/")
				if end < 0 {
					return nil, errors.New("unterminated SQL comment")
				}
				i += end + 4
				continue
			}
		case '\'':
			i++
			closed := false
			for i < len(statement) {
				if statement[i] == '\'' {
					if i+1 < len(statement) && statement[i+1] == '\'' {
						i += 2
						continue
					}
					i++
					closed = true
					break
				}
				i++
			}
			if !closed {
				return nil, errors.New("unterminated SQL string")
			}
			continue
		case '"', '`', '[':
			open := statement[i]
			closeChar := open
			if open == '[' {
				closeChar = ']'
			}
			i++
			start := i
			var content strings.Builder
			closed := false
			for i < len(statement) {
				if statement[i] == closeChar {
					if closeChar != ']' && i+1 < len(statement) && statement[i+1] == closeChar {
						content.WriteString(statement[start:i])
						content.WriteByte(closeChar)
						i += 2
						start = i
						continue
					}
					content.WriteString(statement[start:i])
					i++
					closed = true
					break
				}
				i++
			}
			if !closed {
				return nil, errors.New("unterminated SQL quoted identifier")
			}
			appendWords(content.String())
			continue
		}
		i++
	}
	return tokens, nil
}

func actionParamInt(params map[string]any, key string) int {
	if params == nil {
		return 0
	}
	return protocol.ConfigInt(params, key)
}

func sortInts(values []int) {
	for i := 1; i < len(values); i++ {
		current := values[i]
		j := i - 1
		for j >= 0 && values[j] > current {
			values[j+1] = values[j]
			j--
		}
		values[j+1] = current
	}
}
