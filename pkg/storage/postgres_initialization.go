package storage

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"time"
)

// PostgreSQL CREATE IF NOT EXISTS and independent unique indexes do not make
// an entire concurrent initialization atomic. Serialize APEX initialization in
// this database before even creating its ledger. Existing per-migration
// transaction locks remain usable within this session lock.
const postgresInitializationLock int64 = 7241801551050296674

func (s *SQLiteStore) initializePostgres() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return s.withPostgresInitializationLock(ctx, s.init)
}

func (s *SQLiteStore) withPostgresInitializationLock(ctx context.Context, initialize func() error) error {
	conn, err := s.db.initializationConnection(ctx)
	if err != nil {
		return fmt.Errorf("acquire PostgreSQL initialization connection: %w", err)
	}
	// Never return a possibly locked session to the pool, including cancellation,
	// a failed migration or an uncertain unlock. Discard releases session locks.
	defer func() {
		s.db.initializationConn = nil
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		_ = conn.Close()
	}()
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, postgresInitializationLock); err != nil {
		return fmt.Errorf("lock PostgreSQL initialization: %w", err)
	}
	s.db.initializationConn = conn
	return initialize()
}

// Both the normal pool and a pinned initialization session implement this
// surface. Pinning avoids consuming an extra pool slot (including max-open=1)
// and prevents reconnecting on a different session after losing the lock.
type storeExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
	BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
}

func (db *storeDB) executor() storeExecutor {
	if db.initializationConn != nil {
		return db.initializationConn
	}
	return db.db
}
