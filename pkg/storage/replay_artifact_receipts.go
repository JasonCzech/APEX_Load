package storage

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

// ReplayArtifactSpec identifies immutable, already scrubbed terminal artifacts.
// Generation must be fresh cryptographic randomness per attempt, never reused.
// Object names are derived only from Generation, never from a session ID or URI.
// SourceSHA256 identifies the configured backend/namespace; it contains no credentials.
type ReplayArtifactSpec struct {
	Version      int    `json:"version"`
	SessionID    string `json:"session_id"`
	ProjectID    string `json:"project_id"`
	Generation   string `json:"generation"`
	SourceSHA256 string `json:"source_sha256"`
	HARSHA256    string `json:"har_sha256"`
	HARBytes     int64  `json:"har_bytes"`
	ReportSHA256 string `json:"report_sha256"`
	ReportBytes  int64  `json:"report_bytes"`
}

type ReplayArtifactReceipt struct {
	Spec         ReplayArtifactSpec
	State        string
	WriterJoined bool
	ExpiresAt    time.Time
}

// Limits include staging, published and cleanup-pending objects. Expiry alone
// does not release a reservation: physical cleanup must first be confirmed.
type ReplayArtifactLimits struct {
	GlobalReceipts  int
	ProjectReceipts int
	GlobalBytes     int64
	ProjectBytes    int64
}

var ErrReplayArtifactFence = errors.New("replay artifact ownership unavailable")
var ErrReplayArtifactCapacity = errors.New("replay artifact retention capacity unavailable")

func replayArtifactDigest(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
func (spec ReplayArtifactSpec) valid() bool {
	return spec.Version == 1 &&
		utf8.ValidString(spec.SessionID) && spec.SessionID != "" && len(spec.SessionID) <= 128 && strings.TrimSpace(spec.SessionID) == spec.SessionID &&
		utf8.ValidString(spec.ProjectID) && spec.ProjectID != "" && len(spec.ProjectID) <= 128 && normalizeProjectID(spec.ProjectID) == spec.ProjectID && strings.TrimSpace(spec.ProjectID) == spec.ProjectID &&
		replayArtifactDigest(spec.Generation) && replayArtifactDigest(spec.SourceSHA256) && replayArtifactDigest(spec.HARSHA256) && replayArtifactDigest(spec.ReportSHA256) &&
		spec.HARBytes > 0 && spec.HARBytes <= 256<<20 && spec.ReportBytes > 0 && spec.ReportBytes <= 16<<20
}
func (limits ReplayArtifactLimits) valid() bool {
	return limits.GlobalReceipts > 0 && limits.GlobalReceipts <= 4096 && limits.ProjectReceipts > 0 && limits.ProjectReceipts <= limits.GlobalReceipts && limits.GlobalBytes > 0 && limits.GlobalBytes <= 64<<30 && limits.ProjectBytes > 0 && limits.ProjectBytes <= limits.GlobalBytes
}

// BeginReplayArtifactReceipt reserves cleanup identity and bytes before any
// upload. Only the exact unexpired terminal lease can reserve. Repeated begins
// conflict, including the same generation: an unknown prior writer must not be
// restarted just because its controller restarted.
func (s *SQLiteStore) BeginReplayArtifactReceipt(ctx context.Context, spec ReplayArtifactSpec, owner string, ttl time.Duration, limits ReplayArtifactLimits) error {
	if !spec.valid() || !utf8.ValidString(owner) || owner == "" || len(owner) > 128 || strings.TrimSpace(owner) != owner || ttl <= 0 || ttl > 24*time.Hour || !limits.valid() {
		return errors.New("invalid replay artifact reservation")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Admission and session-ID acquisition use the same allocator lock.
	result, err := tx.ExecContext(ctx, `UPDATE scheduler_leases SET updated_at=updated_at WHERE name=?`, replaySessionAllocatorName)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return ErrReplayArtifactFence
	}
	if _, err = lockProjectForOwnedMutationContext(ctx, tx, spec.ProjectID); err != nil {
		return err
	}
	now, err := preciseDatabaseTimeContext(ctx, tx, s.db.dialect)
	if err != nil {
		return err
	}
	var leaseExpiry time.Time
	err = tx.QueryRowContext(ctx, `SELECT expires_at FROM replay_session_leases WHERE session_id=? AND project_id=? AND owner_token=? AND state=? AND expires_at>?`, spec.SessionID, spec.ProjectID, owner, replaySessionStateRetained, now).Scan(&leaseExpiry)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrReplayArtifactFence
	}
	if err != nil {
		return err
	}
	var exists int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM replay_artifact_receipts WHERE session_id=? OR generation=?`, spec.SessionID, spec.Generation).Scan(&exists); err != nil {
		return err
	}
	if exists != 0 {
		return ErrReplayArtifactFence
	}
	var globalCount, projectCount int
	var globalBytes, projectBytes int64
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(reserved_bytes),0) FROM replay_artifact_receipts`).Scan(&globalCount, &globalBytes); err != nil {
		return err
	}
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(reserved_bytes),0) FROM replay_artifact_receipts WHERE project_id=?`, spec.ProjectID).Scan(&projectCount, &projectBytes); err != nil {
		return err
	}
	size := spec.HARBytes + spec.ReportBytes
	if globalCount >= limits.GlobalReceipts || projectCount >= limits.ProjectReceipts || size > limits.GlobalBytes-globalBytes || size > limits.ProjectBytes-projectBytes {
		return ErrReplayArtifactCapacity
	}
	expiry := now.Add(ttl)
	if leaseExpiry.Before(expiry) {
		expiry = leaseExpiry
	}
	data, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO replay_artifact_receipts(session_id,project_id,generation,owner_token,state,spec_json,reserved_bytes,writer_joined,expires_at) VALUES(?,?,?,?,?,?,?,?,?)`, spec.SessionID, spec.ProjectID, spec.Generation, owner, "staging", string(data), size, 0, expiry)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// JoinReplayArtifactWriter is physical-writer completion proof supplied by the
// owning adapter after all writes join. It is intentionally permitted after
// lease expiry or project deletion so only that generation can become cleanable.
// A lost heartbeat, elapsed TTL or a canceled context is never this proof.
func (s *SQLiteStore) JoinReplayArtifactWriter(ctx context.Context, session, project, generation, owner string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE replay_artifact_receipts SET writer_joined=1 WHERE session_id=? AND project_id=? AND generation=? AND owner_token=?`, session, project, generation, owner)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

// PublishReplayArtifactReceipt never upgrades an expired, deleted or replaced
// owner. The report/HAR hashes describe bytes verified by the owning adapter;
// publication does not itself perform backend I/O.
func (s *SQLiteStore) PublishReplayArtifactReceipt(ctx context.Context, session, project, generation, owner string) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err = lockProjectForOwnedMutationContext(ctx, tx, project); err != nil {
		return false, err
	}
	now, err := preciseDatabaseTimeContext(ctx, tx, s.db.dialect)
	if err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE replay_artifact_receipts SET state='ready' WHERE session_id=? AND project_id=? AND generation=? AND owner_token=? AND state='staging' AND writer_joined=1 AND expires_at>? AND EXISTS(SELECT 1 FROM replay_session_leases l WHERE l.session_id=replay_artifact_receipts.session_id AND l.project_id=replay_artifact_receipts.project_id AND l.owner_token=replay_artifact_receipts.owner_token AND l.state=? AND l.expires_at>?)`, session, project, generation, owner, now, replaySessionStateRetained, now)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return n == 1, nil
}

func scanReplayArtifactReceipt(row interface{ Scan(...any) error }) (*ReplayArtifactReceipt, error) {
	var receipt ReplayArtifactReceipt
	var data, session, project, generation string
	var bytes int64
	var joined int
	if err := row.Scan(&session, &project, &generation, &data, &bytes, &receipt.State, &joined, &receipt.ExpiresAt); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(data), &receipt.Spec); err != nil {
		return nil, err
	}
	if !receipt.Spec.valid() || receipt.Spec.SessionID != session || receipt.Spec.ProjectID != project || receipt.Spec.Generation != generation || bytes != receipt.Spec.HARBytes+receipt.Spec.ReportBytes || (joined != 0 && joined != 1) {
		return nil, errors.New("invalid retained replay artifact receipt")
	}
	receipt.WriterJoined = joined == 1
	return &receipt, nil
}

// GetReplayArtifactReceipt is tenant scoped and never falls back to a new local
// session. Backend identity, exact lengths and hashes still require verification.
func (s *SQLiteStore) GetReplayArtifactReceipt(ctx context.Context, session, project string) (*ReplayArtifactReceipt, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now, err := preciseDatabaseTimeContext(ctx, tx, s.db.dialect)
	if err != nil {
		return nil, err
	}
	receipt, err := scanReplayArtifactReceipt(tx.QueryRowContext(ctx, `SELECT session_id,project_id,generation,spec_json,reserved_bytes,state,writer_joined,expires_at FROM replay_artifact_receipts WHERE session_id=? AND project_id=? AND state='ready' AND writer_joined=1 AND expires_at>? AND EXISTS(SELECT 1 FROM projects p WHERE p.id=replay_artifact_receipts.project_id)`, session, project, now))
	if err != nil {
		return nil, err
	}
	if receipt.Spec.SessionID != session || receipt.Spec.ProjectID != project {
		return nil, errors.New("replay artifact identity mismatch")
	}
	return receipt, nil
}

// ClaimReplayArtifactCleanup converts joined expired/lost/deleted reservations
// to cleanup tombstones. It never treats an unjoined writer as safe to delete.
// The tombstone retains namespace and quota until Finish confirms physical removal.
func (s *SQLiteStore) ClaimReplayArtifactCleanup(ctx context.Context, limit int) ([]ReplayArtifactReceipt, error) {
	return s.claimReplayArtifactCleanup(ctx, limit, nil)
}

// ClaimReplayArtifactCleanupAfter uses a bounded session-ID keyset so a cleanup
// worker can advance past receipts belonging to another configured backend.
// The cursor is scheduling only, never proof of writer completion. Callers
// restart from an empty cursor after reaching the end to retry retained failures.
func (s *SQLiteStore) ClaimReplayArtifactCleanupAfter(ctx context.Context, after string, limit int) ([]ReplayArtifactReceipt, error) {
	if len(after) > 128 || !utf8.ValidString(after) || strings.TrimSpace(after) != after {
		return nil, errors.New("invalid replay cleanup cursor")
	}
	return s.claimReplayArtifactCleanup(ctx, limit, &after)
}

func (s *SQLiteStore) claimReplayArtifactCleanup(ctx context.Context, limit int, after *string) ([]ReplayArtifactReceipt, error) {
	if limit < 1 || limit > 256 {
		return nil, errors.New("invalid replay cleanup limit")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now, err := preciseDatabaseTimeContext(ctx, tx, s.db.dialect)
	if err != nil {
		return nil, err
	}
	query := `SELECT session_id,project_id,generation,spec_json,reserved_bytes,state,writer_joined,expires_at FROM replay_artifact_receipts r WHERE writer_joined=1 AND (state='cleanup' OR expires_at<=? OR (state='staging' AND NOT EXISTS(SELECT 1 FROM replay_session_leases l WHERE l.session_id=r.session_id AND l.project_id=r.project_id AND l.owner_token=r.owner_token AND l.state=? AND l.expires_at>?)))`
	args := []any{now, replaySessionStateRetained, now}
	if after != nil {
		query += ` AND session_id>? ORDER BY session_id LIMIT ?`
		args = append(args, *after, limit)
	} else {
		query += ` ORDER BY expires_at,session_id LIMIT ?`
		args = append(args, limit)
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	out := []ReplayArtifactReceipt{}
	for rows.Next() {
		receipt, e := scanReplayArtifactReceipt(rows)
		if e != nil {
			rows.Close()
			return nil, e
		}
		out = append(out, *receipt)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range out {
		r := &out[i]
		result, e := tx.ExecContext(ctx, `UPDATE replay_artifact_receipts SET state='cleanup' WHERE session_id=? AND generation=? AND writer_joined=1 AND (state='cleanup' OR expires_at<=? OR (state='staging' AND NOT EXISTS(SELECT 1 FROM replay_session_leases l WHERE l.session_id=replay_artifact_receipts.session_id AND l.project_id=replay_artifact_receipts.project_id AND l.owner_token=replay_artifact_receipts.owner_token AND l.state=? AND l.expires_at>?)))`, r.Spec.SessionID, r.Spec.Generation, now, replaySessionStateRetained, now)
		if e != nil {
			return nil, e
		}
		n, e := result.RowsAffected()
		if e != nil {
			return nil, e
		}
		if n != 1 {
			return nil, ErrReplayArtifactFence
		}
		r.State = "cleanup"
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

// FinishReplayArtifactCleanup is called only after exact-generation physical
// removal is confirmed. Repeated/late cleanup cannot remove a successor receipt.
func (s *SQLiteStore) FinishReplayArtifactCleanup(ctx context.Context, session, project, generation string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM replay_artifact_receipts WHERE session_id=? AND project_id=? AND generation=? AND state='cleanup' AND writer_joined=1`, session, project, generation)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}
