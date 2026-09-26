package storage

import (
	"database/sql"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
)

// postgresIntegerWideningLock serializes the one-time int4 -> int8 widening
// across HA replicas that boot against the same database.
const postgresIntegerWideningLock = `SELECT pg_advisory_xact_lock(7241801551050296673)`

// postgresOwnedTableNames lists every table the schema creates. The widening
// migration is scoped to these so a database shared with other applications
// never has its tables altered.
func postgresOwnedTableNames() []string {
	statements := append([]string{schemaMigrationLedgerDDL}, schemaCreateTableStatements()...)
	seen := make(map[string]struct{}, len(statements))
	names := make([]string, 0, len(statements))
	for _, statement := range statements {
		for _, match := range createTableNamePattern.FindAllStringSubmatch(statement, -1) {
			name := strings.ToLower(match[1])
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

type postgresIntegerColumn struct {
	table  string
	column string
}

const postgresInt4ColumnsQuery = `SELECT c.relname, a.attname
	FROM pg_attribute a
	JOIN pg_class c ON c.oid = a.attrelid
	JOIN pg_namespace n ON n.oid = c.relnamespace
	WHERE n.nspname = current_schema()
		AND c.relkind IN ('r', 'p')
		AND a.attnum > 0
		AND NOT a.attisdropped
		AND a.atttypid = 'int4'::regtype
		AND c.relname = ANY(?)
	ORDER BY c.relname, a.attnum`

// postgresCatalogQuerier is satisfied by both *storeDB and *storeTx.
type postgresCatalogQuerier interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

func queryPostgresInt4Columns(querier postgresCatalogQuerier, tables []string) ([]postgresIntegerColumn, error) {
	rows, err := querier.Query(postgresInt4ColumnsQuery, tables)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var columns []postgresIntegerColumn
	for rows.Next() {
		var column postgresIntegerColumn
		if err := rows.Scan(&column.table, &column.column); err != nil {
			return nil, err
		}
		columns = append(columns, column)
	}
	return columns, rows.Err()
}

// ensurePostgresIntegerColumnsBigint widens every int4 column in an owned table
// to BIGINT. Databases created before rewriteSQL mapped DDL INTEGER to BIGINT
// declared those columns int4, so int64 values such as tests.duration_ns failed
// to encode (a 150s test could not be saved). INTEGER -> BIGINT is lossless and
// keeps defaults, CHECKs and indexes. The catalog probe runs first without a
// lock, so an already-migrated database takes no ACCESS EXCLUSIVE lock on
// restart; each table is rewritten once with all its columns in one ALTER.
func (s *SQLiteStore) ensurePostgresIntegerColumnsBigint() error {
	if s == nil || s.db == nil || s.db.dialect != storeDialectPostgres {
		return nil
	}
	tables := postgresOwnedTableNames()
	pending, err := queryPostgresInt4Columns(s.db, tables)
	if err != nil {
		return fmt.Errorf("inspect PostgreSQL int4 columns: %w", err)
	}
	if len(pending) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin PostgreSQL BIGINT widening: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(postgresIntegerWideningLock); err != nil {
		return fmt.Errorf("lock PostgreSQL BIGINT widening: %w", err)
	}
	// Re-read under the lock: a concurrently booting replica may have finished.
	pending, err = queryPostgresInt4Columns(tx, tables)
	if err != nil {
		return fmt.Errorf("inspect PostgreSQL int4 columns: %w", err)
	}
	byTable := make(map[string][]string)
	order := make([]string, 0)
	for _, column := range pending {
		if _, ok := byTable[column.table]; !ok {
			order = append(order, column.table)
		}
		byTable[column.table] = append(byTable[column.table], column.column)
	}
	for _, table := range order {
		clauses := make([]string, 0, len(byTable[table]))
		for _, column := range byTable[table] {
			clauses = append(clauses, "ALTER COLUMN "+quotePostgresIdentifier(column)+" TYPE BIGINT")
		}
		started := time.Now()
		if _, err := tx.Exec("ALTER TABLE " + quotePostgresIdentifier(table) + " " + strings.Join(clauses, ", ")); err != nil {
			return fmt.Errorf("widen %s to BIGINT: %w", table, err)
		}
		slog.Info("widened PostgreSQL int4 columns to BIGINT", "table", table, "columns", byTable[table], "elapsed", time.Since(started))
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit PostgreSQL BIGINT widening: %w", err)
	}
	return nil
}
