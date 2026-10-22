package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// ReplaySessionLeaseAcquireResult identifies why a distributed replay-session
// reservation did or did not succeed. Active leases and retained-ID tombstones
// share a dedicated table and use the database clock for ownership fencing.
type ReplaySessionLeaseAcquireResult string

const (
	ReplaySessionLeaseAcquired        ReplaySessionLeaseAcquireResult = "acquired"
	ReplaySessionLeaseConflict        ReplaySessionLeaseAcquireResult = "conflict"
	ReplaySessionLeaseGlobalCapacity  ReplaySessionLeaseAcquireResult = "global_capacity"
	ReplaySessionLeaseProjectCapacity ReplaySessionLeaseAcquireResult = "project_capacity"
)

const (
	replaySessionAllocatorName  = "replay-session-allocator:v2"
	replaySessionAllocatorOwner = "replay-session-allocator"
	replaySessionCleanupBatch   = 256
	replaySessionStateActive    = "active"
	replaySessionStateRetained  = "retained"
)

// TryAcquireReplaySessionLease atomically enforces exact-ID ownership plus the
// global and per-project active-session caps. A durable allocator row
// serializes the count-and-claim transition across PostgreSQL replicas; the
// same first-write transition obtains SQLite's single-writer lock.
func (s *SQLiteStore) TryAcquireReplaySessionLease(
	ctx context.Context,
	sessionID, ownerToken, projectID string,
	ttl time.Duration,
	globalLimit, projectLimit int,
) (ReplaySessionLeaseAcquireResult, error) {
	sessionID, ownerToken, projectID, err := validateReplaySessionLeaseIdentity(sessionID, ownerToken, projectID, ttl)
	if err != nil {
		return "", err
	}
	if globalLimit <= 0 || projectLimit <= 0 || projectLimit > globalLimit {
		return "", errors.New("replay session lease limits must be positive and project limit must not exceed global limit")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	tx, err := s.beginReplaySessionLeaseTransaction(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	// This insert is intentionally the first database operation. On SQLite it
	// obtains the writer lock before any snapshot read; on PostgreSQL the update
	// below row-locks the shared allocator until commit.
	metadataNow := time.Now().UTC()
	if _, err := replayLeaseExecContext(ctx, tx, `INSERT INTO scheduler_leases (name, owner_id, generation, acquired_at, expires_at, updated_at)
		VALUES (?, ?, 1, ?, ?, ?)
		ON CONFLICT(name) DO NOTHING`, replaySessionAllocatorName, replaySessionAllocatorOwner, metadataNow, metadataNow, metadataNow); err != nil {
		return "", fmt.Errorf("initialize replay session allocator: %w", err)
	}
	if _, err := replayLeaseExecContext(ctx, tx, `UPDATE scheduler_leases SET updated_at = updated_at WHERE name = ?`, replaySessionAllocatorName); err != nil {
		return "", fmt.Errorf("lock replay session allocator: %w", err)
	}

	now, err := preciseDatabaseTimeContext(ctx, tx, s.db.dialect)
	if err != nil {
		return "", fmt.Errorf("read replay session lease database clock: %w", err)
	}

	// Remove a bounded batch on every admission so abandoned active rows and
	// expired retained-ID tombstones cannot accumulate without limit. The expiry
	// index keeps this independent of the historical table size.
	if _, err := replayLeaseExecContext(ctx, tx, `DELETE FROM replay_session_leases
		WHERE session_id IN (
			SELECT session_id FROM replay_session_leases
			WHERE expires_at <= ?
			ORDER BY expires_at, session_id
			LIMIT ?
		)`, now, replaySessionCleanupBatch); err != nil {
		return "", fmt.Errorf("purge expired replay session leases: %w", err)
	}

	// Durable artifacts keep the namespace even after process pruning or lease
	// expiry. Physical cleanup, not a clock alone, releases this tombstone.
	var artifactCount int
	if err := replayLeaseQueryRowContext(ctx, tx, `SELECT COUNT(*) FROM replay_artifact_receipts WHERE session_id=?`, sessionID).Scan(&artifactCount); err != nil {
		return "", err
	}
	if artifactCount != 0 {
		return ReplaySessionLeaseConflict, nil
	}

	var existingExpiry time.Time
	err = replayLeaseQueryRowContext(ctx, tx, `SELECT expires_at FROM replay_session_leases WHERE session_id = ?`, sessionID).Scan(&existingExpiry)
	if err == nil && existingExpiry.After(now) {
		return ReplaySessionLeaseConflict, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("read replay session lease: %w", err)
	}

	var globalCount int
	if err := replayLeaseQueryRowContext(ctx, tx, `SELECT COUNT(*) FROM replay_session_leases
		WHERE state = ? AND expires_at > ?`, replaySessionStateActive, now).Scan(&globalCount); err != nil {
		return "", fmt.Errorf("count active replay session leases: %w", err)
	}
	if globalCount >= globalLimit {
		return ReplaySessionLeaseGlobalCapacity, nil
	}

	var projectCount int
	if err := replayLeaseQueryRowContext(ctx, tx, `SELECT COUNT(*) FROM replay_session_leases
		WHERE project_id = ? AND state = ? AND expires_at > ?`,
		projectID, replaySessionStateActive, now).Scan(&projectCount); err != nil {
		return "", fmt.Errorf("count active project replay session leases: %w", err)
	}
	if projectCount >= projectLimit {
		return ReplaySessionLeaseProjectCapacity, nil
	}

	expiresAt := now.Add(ttl)
	result, err := replayLeaseExecContext(ctx, tx, `INSERT INTO replay_session_leases (session_id, owner_token, project_id, state, generation, expires_at, updated_at)
		VALUES (?, ?, ?, ?, 1, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
		owner_token=excluded.owner_token,
		project_id=excluded.project_id,
		state=excluded.state,
		generation=replay_session_leases.generation + 1,
		expires_at=excluded.expires_at,
		updated_at=excluded.updated_at
		WHERE replay_session_leases.expires_at <= ?
		AND replay_session_leases.generation > 0
		AND replay_session_leases.generation < ?`,
		sessionID, ownerToken, projectID, replaySessionStateActive, expiresAt, now, now, int64(math.MaxInt64))
	if err != nil {
		return "", fmt.Errorf("acquire replay session lease: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return "", fmt.Errorf("read replay session lease acquisition result: %w", err)
	}
	if affected != 1 {
		// The allocator lock should make an ordinary ownership race impossible.
		// Treat an exhausted/corrupt generation as an operational error instead
		// of weakening the fence.
		return "", fmt.Errorf("replay session lease %q could not advance its fencing generation", sessionID)
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit replay session lease: %w", err)
	}
	return ReplaySessionLeaseAcquired, nil
}

// RenewReplaySessionLease extends only an unexpired lease still owned by the
// exact project/token pair. Returning false means the caller has lost the
// fence and must stop the active replay.
func (s *SQLiteStore) RenewReplaySessionLease(ctx context.Context, sessionID, ownerToken, projectID string, ttl time.Duration) (bool, error) {
	sessionID, ownerToken, projectID, err := validateReplaySessionLeaseIdentity(sessionID, ownerToken, projectID, ttl)
	if err != nil {
		return false, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tx, err := s.beginReplaySessionLeaseTransaction(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	now, err := preciseDatabaseTimeContext(ctx, tx, s.db.dialect)
	if err != nil {
		return false, fmt.Errorf("read replay session lease database clock: %w", err)
	}
	result, err := replayLeaseExecContext(ctx, tx, `UPDATE replay_session_leases
		SET expires_at = ?, updated_at = ?
		WHERE session_id = ? AND owner_token = ? AND project_id = ?
		AND state = ? AND expires_at > ?`, now.Add(ttl), now, sessionID, ownerToken, projectID, replaySessionStateActive, now)
	if err != nil {
		return false, fmt.Errorf("renew replay session lease: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read replay session lease renewal result: %w", err)
	}
	if affected != 1 {
		return false, nil
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit replay session lease renewal: %w", err)
	}
	return true, nil
}

// RetainReplaySessionLease atomically removes an active lease from capacity
// while preserving the globally addressable session id as a tombstone for the
// manager's terminal-retention window.
func (s *SQLiteStore) RetainReplaySessionLease(ctx context.Context, sessionID, ownerToken, projectID string, ttl time.Duration) (bool, error) {
	sessionID, ownerToken, projectID, err := validateReplaySessionLeaseIdentity(sessionID, ownerToken, projectID, ttl)
	if err != nil {
		return false, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tx, err := s.beginReplaySessionLeaseTransaction(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	now, err := preciseDatabaseTimeContext(ctx, tx, s.db.dialect)
	if err != nil {
		return false, fmt.Errorf("read replay session lease database clock: %w", err)
	}
	result, err := replayLeaseExecContext(ctx, tx, `UPDATE replay_session_leases
		SET state = ?, expires_at = ?, updated_at = ?
		WHERE session_id = ? AND owner_token = ? AND project_id = ?
		AND state = ? AND expires_at > ?`, replaySessionStateRetained, now.Add(ttl), now,
		sessionID, ownerToken, projectID, replaySessionStateActive, now)
	if err != nil {
		return false, fmt.Errorf("retain replay session lease: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read replay session retention result: %w", err)
	}
	if affected != 1 {
		return false, nil
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit replay session retention: %w", err)
	}
	return true, nil
}

// ReleaseReplaySessionLease deletes only an active lease still owned by the
// exact project/token pair. Owner fencing makes deletion safe: a delayed release
// from an old owner cannot affect a successor.
func (s *SQLiteStore) ReleaseReplaySessionLease(ctx context.Context, sessionID, ownerToken, projectID string) (bool, error) {
	return s.deleteReplaySessionLease(ctx, sessionID, ownerToken, projectID, replaySessionStateActive)
}

// ReleaseRetainedReplaySessionLease removes an owner-fenced retained-ID
// tombstone when the corresponding terminal session is pruned.
func (s *SQLiteStore) ReleaseRetainedReplaySessionLease(ctx context.Context, sessionID, ownerToken, projectID string) (bool, error) {
	return s.deleteReplaySessionLease(ctx, sessionID, ownerToken, projectID, replaySessionStateRetained)
}

func (s *SQLiteStore) deleteReplaySessionLease(ctx context.Context, sessionID, ownerToken, projectID, state string) (bool, error) {
	sessionID, ownerToken, projectID, err := validateReplaySessionLeaseIdentity(sessionID, ownerToken, projectID, time.Second)
	if err != nil {
		return false, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM replay_session_leases
		WHERE session_id = ? AND owner_token = ? AND project_id = ? AND state = ?`,
		sessionID, ownerToken, projectID, state)
	if err != nil {
		return false, fmt.Errorf("release replay session lease: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read replay session lease release result: %w", err)
	}
	return affected == 1, nil
}

func validateReplaySessionLeaseIdentity(sessionID, ownerToken, projectID string, ttl time.Duration) (string, string, string, error) {
	sessionID = strings.TrimSpace(sessionID)
	ownerToken = strings.TrimSpace(ownerToken)
	projectID = normalizeProjectID(strings.TrimSpace(projectID))
	if sessionID == "" || ownerToken == "" {
		return "", "", "", errors.New("replay session id and owner token are required")
	}
	if ttl <= 0 {
		return "", "", "", errors.New("replay session lease ttl must be positive")
	}
	return sessionID, ownerToken, projectID, nil
}

func (s *SQLiteStore) beginReplaySessionLeaseTransaction(ctx context.Context) (*storeTx, error) {
	return s.db.BeginTx(ctx, nil)
}

func replayLeaseExecContext(ctx context.Context, tx *storeTx, query string, args ...any) (sql.Result, error) {
	return tx.ExecContext(ctx, query, args...)
}

func replayLeaseQueryRowContext(ctx context.Context, tx *storeTx, query string, args ...any) *sql.Row {
	return tx.QueryRowContext(ctx, query, args...)
}

func preciseDatabaseTimeContext(ctx context.Context, tx *storeTx, dialect storeDialect) (time.Time, error) {
	if dialect == storeDialectPostgres {
		var now time.Time
		if err := replayLeaseQueryRowContext(ctx, tx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
			return time.Time{}, err
		}
		return now.UTC(), nil
	}
	var raw string
	if err := replayLeaseQueryRowContext(ctx, tx, `SELECT strftime('%Y-%m-%dT%H:%M:%fZ', 'now')`).Scan(&raw); err != nil {
		return time.Time{}, err
	}
	now, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse SQLite clock %q: %w", raw, err)
	}
	return now.UTC(), nil
}
