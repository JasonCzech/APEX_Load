package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jasonczech/stormrunner/pkg/protocol"
	_ "modernc.org/sqlite"
)

func init() {
	protocol.Register("database", func() protocol.Protocol {
		return &Database{}
	})
}

type Database struct {
	db                 *sql.DB
	tx                 *sql.Tx
	driver             string
	dsn                string
	queryTimeout       time.Duration
	maxRows            int
	readOnly           bool
	rollbackOnTeardown bool
}

func (d *Database) Name() string { return "database" }

func (d *Database) Init(ctx context.Context, config map[string]any) error {
	d.driver = normalizeDriver(configString(config, "database_driver"))
	d.dsn = strings.TrimSpace(configString(config, "database_dsn"))
	if d.driver == "" {
		return errors.New("database driver is required")
	}
	if d.dsn == "" {
		return errors.New("database dsn is required")
	}
	db, err := sql.Open(d.driver, d.dsn)
	if err != nil {
		return err
	}
	if maxOpen := configInt(config, "database_max_open_conns"); maxOpen > 0 {
		db.SetMaxOpenConns(maxOpen)
	}
	if maxIdle := configInt(config, "database_max_idle_conns"); maxIdle > 0 {
		db.SetMaxIdleConns(maxIdle)
	}
	if lifetimeMS := configInt(config, "database_conn_max_lifetime_ms"); lifetimeMS > 0 {
		db.SetConnMaxLifetime(time.Duration(lifetimeMS) * time.Millisecond)
	}
	d.queryTimeout = 30 * time.Second
	if timeoutMS := configInt(config, "database_query_timeout_ms"); timeoutMS > 0 {
		d.queryTimeout = time.Duration(timeoutMS) * time.Millisecond
	}
	d.maxRows = 100
	if maxRows := configInt(config, "database_max_rows"); maxRows > 0 {
		d.maxRows = maxRows
	}
	d.readOnly, _ = config["database_read_only"].(bool)
	d.rollbackOnTeardown, _ = config["database_rollback_on_teardown"].(bool)
	d.db = db
	if err := d.db.PingContext(ctx); err != nil {
		_ = d.db.Close()
		d.db = nil
		return err
	}
	return nil
}

func (d *Database) Execute(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
	start := time.Now()
	result := &protocol.Result{
		ActionName:       actionName(action),
		StartTime:        start,
		DependencyType:   "database",
		DependencyTarget: d.driver,
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

	var err error
	switch actionType {
	case "db_begin", "database_begin", "begin":
		err = d.begin(actionCtx)
		result.ResponseBody = `{"transaction":"begun"}`
	case "db_commit", "database_commit", "commit":
		err = d.commit()
		result.ResponseBody = `{"transaction":"committed"}`
	case "db_rollback", "database_rollback", "rollback":
		err = d.rollback()
		result.ResponseBody = `{"transaction":"rolled_back"}`
	case "db_health", "database_health", "ping":
		err = d.db.PingContext(actionCtx)
		result.ResponseBody = `{"status":"ok"}`
	case "db_query", "database_query", "query", "select":
		result.ResponseBody, err = d.query(actionCtx, action)
	case "db_exec", "database_exec", "exec", "execute", "insert", "update", "delete", "db_procedure", "stored_procedure":
		result.ResponseBody, err = d.exec(actionCtx, action)
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
		result.ErrorMessage = err.Error()
	}
	if assertionErr := evaluateDatabaseAssertions(action.Assertions, result.ResponseBody); assertionErr != "" {
		result.Success = false
		result.StatusCode = 500
		result.ErrorMessage = assertionErr
	}
	result.ServerTelemetry = &protocol.ServerTelemetry{
		ServerProcessingMs: float64(result.Duration.Microseconds()) / 1000,
		RequestPayloadKB:   float64(result.BytesSent) / 1024,
		ResponsePayloadKB:  float64(result.BytesReceived) / 1024,
	}
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
	return d.db.PingContext(ctx)
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

func (d *Database) query(ctx context.Context, action *protocol.Action) (string, error) {
	query := sqlText(action)
	if query == "" {
		return "", errors.New("database query SQL is required")
	}
	rows, err := d.queryContext(ctx, query, actionBinds(action)...)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return "", err
	}
	maxRows := d.maxRows
	if override := actionParamInt(action.Params, "max_rows"); override > 0 {
		maxRows = override
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
			return "", err
		}
		item := make(map[string]any, len(columns))
		for i, column := range columns {
			item[column] = normalizeSQLValue(values[i])
		}
		outRows = append(outRows, item)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return encodeDatabasePayload(map[string]any{
		"columns":   columns,
		"row_count": len(outRows),
		"rows":      outRows,
	})
}

func (d *Database) exec(ctx context.Context, action *protocol.Action) (string, error) {
	statement := sqlText(action)
	if statement == "" {
		return "", errors.New("database exec SQL is required")
	}
	if d.readOnly && isMutatingSQL(statement) {
		return "", errors.New("database read_only mode blocks mutating SQL")
	}
	res, err := d.execContext(ctx, statement, actionBinds(action)...)
	if err != nil {
		return "", err
	}
	rowsAffected, _ := res.RowsAffected()
	lastInsertID, _ := res.LastInsertId()
	return encodeDatabasePayload(map[string]any{
		"rows_affected":  rowsAffected,
		"last_insert_id": lastInsertID,
	})
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
	default:
		return driver
	}
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

func actionBinds(action *protocol.Action) []any {
	if action == nil || action.Params == nil {
		return nil
	}
	value, ok := action.Params["binds"]
	if !ok {
		return nil
	}
	switch typed := value.(type) {
	case []any:
		return typed
	case []string:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = item
		}
		return out
	case map[string]any:
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
		return out
	default:
		return []any{typed}
	}
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

func encodeDatabasePayload(value map[string]any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func evaluateDatabaseAssertions(assertions []protocol.Assertion, body string) string {
	if len(assertions) == 0 {
		return ""
	}
	var payload map[string]any
	_ = json.Unmarshal([]byte(body), &payload)
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
		}
	}
	return ""
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

func isMutatingSQL(statement string) bool {
	first := strings.Fields(strings.ToLower(strings.TrimSpace(statement)))
	if len(first) == 0 {
		return false
	}
	switch first[0] {
	case "insert", "update", "delete", "merge", "create", "alter", "drop", "truncate", "replace":
		return true
	default:
		return false
	}
}

func actionName(action *protocol.Action) string {
	if action == nil {
		return ""
	}
	return action.Name
}

func configString(config map[string]any, key string) string {
	if value, ok := config[key].(string); ok {
		return value
	}
	return ""
}

func configInt(config map[string]any, key string) int {
	value, ok := config[key]
	if !ok {
		return 0
	}
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case float32:
		return int(typed)
	default:
		return 0
	}
}

func actionParamInt(params map[string]any, key string) int {
	if params == nil {
		return 0
	}
	return configInt(params, key)
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
