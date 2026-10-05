package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
	"time"
)

type MonitorObjectCleanupReference struct {
	RunID       string `json:"run_id"`
	ArtifactKey string `json:"artifact_key"`
}

func validStoredMonitorArtifactKey(key string) bool {
	parts := strings.Split(key, "/")
	return len(parts) == 4 && parts[0] == "monitor-evidence" && parts[1] == "v1" && validManagementDigest(parts[2]) && strings.HasSuffix(parts[3], ".json") && validManagementDigest(strings.TrimSuffix(parts[3], ".json"))
}
func monitorObjectOwnedBy(project, run, key string) bool {
	if !validStoredMonitorArtifactKey(key) || !validManagementText(project, 256) || !validManagementText(run, 256) {
		return false
	}
	h := sha256.Sum256([]byte(project + "\x00" + run))
	return strings.Split(key, "/")[2] == hex.EncodeToString(h[:])
}
func (s *SQLiteStore) QueueMonitorObjectCleanup(project, run, key string) error {
	if !monitorObjectOwnedBy(project, run, key) {
		return errors.New("invalid monitor object cleanup ownership")
	}
	b, _ := json.Marshal(MonitorObjectCleanupReference{run, key})
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = queueAnalysisCleanupTx(tx, "monitor-object", project, string(b)); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *SQLiteStore) IsMonitorArtifactReferenced(project, key string) (bool, error) {
	if !validStoredMonitorArtifactKey(key) {
		return false, errors.New("invalid monitor artifact reference query")
	}
	var n int
	e := s.db.QueryRow(`SELECT COUNT(*) FROM run_monitor_batches b JOIN run_monitor_jobs j ON j.id=b.job_id WHERE j.project_id=? AND b.artifact_key=?`, project, key).Scan(&n)
	return n > 0, e
}

// ManagementPollRetentionStore supports bounded scheduled journal retention.
// Durable receipts and unresolved physical authority always retain their polls.
type ManagementPollRetentionStore interface {
	SweepManagementPollRetention(context.Context, time.Time, int) (scanned, pruned int64, err error)
}

const managementPollRetentionCursorKey = "internal:monitor-management:retention-cursor:v1"

// The cursor is private durable metadata, shared by all controller replicas.
// Keeping it in the same transaction as deletions avoids lost progress during
// failover and advances across protected journals instead of revisiting them
// indefinitely. A zero position starts the next bounded pass.
type managementPollRetentionCursor struct {
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	ID        string    `json:"id"`
}

type managementPollRetentionCandidate struct {
	ID        string
	ProjectID string
	CreatedAt time.Time
}

func decodeManagementPollRetentionCursor(raw string) (managementPollRetentionCursor, error) {
	var cursor managementPollRetentionCursor
	if len(raw) == 0 || len(raw) > 4096 {
		return cursor, errors.New("invalid management retention cursor")
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil {
		return cursor, errors.New("invalid management retention cursor")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF || cursor.Version != 1 ||
		(cursor.ID == "") != cursor.CreatedAt.IsZero() ||
		(cursor.ID != "" && !validManagementText(cursor.ID, 256)) {
		return cursor, errors.New("invalid management retention cursor")
	}
	cursor.CreatedAt = cursor.CreatedAt.UTC()
	return cursor, nil
}

// SweepManagementPollRetention reads at most scanLimit terminal index entries
// and takes at most 50 project fences. The raw page is deliberately independent
// of eligibility: receipt-protected and live fairness cursors also move the
// durable scan position. Deletion eligibility is rechecked under each project
// fence, so concurrent publication cannot lose accepted evidence.
//
// scanned counts index entries read, including an unprocessed suffix when the
// project bound is reached. Only the processed prefix advances the cursor. An
// empty page resets the cursor and returns; the next call begins another pass.
func (s *SQLiteStore) SweepManagementPollRetention(ctx context.Context, before time.Time, scanLimit int) (scanned, pruned int64, err error) {
	if ctx == nil || before.IsZero() || scanLimit < 1 || scanLimit > 512 {
		return 0, 0, errors.New("invalid management journal retention sweep")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	initial, _ := json.Marshal(managementPollRetentionCursor{Version: 1})
	var raw string
	// ON CONFLICT performs an update even when unchanged. This serializes the
	// private cursor on PostgreSQL as well as SQLite before any project locks.
	if err := tx.QueryRowContext(ctx, `INSERT INTO app_settings(key,value,updated_at) VALUES(?,?,?)
 ON CONFLICT(key) DO UPDATE SET value=app_settings.value RETURNING value`, managementPollRetentionCursorKey, string(initial), now).Scan(&raw); err != nil {
		return 0, 0, err
	}
	cursor, err := decodeManagementPollRetentionCursor(raw)
	if err != nil {
		return 0, 0, err
	}
	query := `SELECT p.id,p.project_id,p.created_at FROM monitor_management_polls p
 WHERE p.state IN ('accepted','abandoned') AND p.created_at<?`
	args := []any{before.UTC()}
	if cursor.ID != "" {
		query += ` AND (p.created_at,p.id)>(?,?)`
		args = append(args, cursor.CreatedAt, cursor.ID)
	}
	query += ` ORDER BY p.created_at,p.id LIMIT ?`
	args = append(args, scanLimit)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, 0, err
	}
	candidates := make([]managementPollRetentionCandidate, 0, scanLimit)
	for rows.Next() {
		var candidate managementPollRetentionCandidate
		if err := rows.Scan(&candidate.ID, &candidate.ProjectID, &candidate.CreatedAt); err != nil {
			rows.Close()
			return 0, 0, err
		}
		if !validManagementText(candidate.ID, 256) || !validManagementText(candidate.ProjectID, 256) || candidate.CreatedAt.IsZero() {
			rows.Close()
			return 0, 0, errors.New("invalid management retention candidate")
		}
		candidate.CreatedAt = candidate.CreatedAt.UTC()
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, 0, err
	}
	scanned = int64(len(candidates))
	projects := make(map[string]struct{})
	prefix := len(candidates)
	for i, candidate := range candidates {
		if _, exists := projects[candidate.ProjectID]; !exists {
			if len(projects) == 50 {
				prefix = i
				break
			}
			projects[candidate.ProjectID] = struct{}{}
		}
	}
	projectIDs := make([]string, 0, len(projects))
	for project := range projects {
		projectIDs = append(projectIDs, project)
	}
	sort.Strings(projectIDs)
	for _, project := range projectIDs {
		// Match the existing project mutation fence, carrying cancellation into
		// lock waits. A concurrently deleted project needs no further cleanup.
		if _, err := tx.ExecContext(ctx, `UPDATE projects SET updated_at=updated_at WHERE id=?`, project); err != nil {
			return scanned, 0, err
		}
	}
	for _, candidate := range candidates[:prefix] {
		result, err := tx.ExecContext(ctx, `DELETE FROM monitor_management_polls WHERE id IN (
 SELECT p.id FROM monitor_management_polls p WHERE p.id=? AND p.project_id=? AND `+managementPollRetentionEligible+`)`, candidate.ID, candidate.ProjectID, before.UTC(), now)
		if err != nil {
			return scanned, 0, err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return scanned, 0, err
		}
		pruned += n
	}
	cursor = managementPollRetentionCursor{Version: 1}
	if prefix > 0 {
		last := candidates[prefix-1]
		cursor.CreatedAt, cursor.ID = last.CreatedAt, last.ID
	}
	encoded, err := json.Marshal(cursor)
	if err != nil {
		return scanned, 0, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE app_settings SET value=?,updated_at=? WHERE key=?`, string(encoded), now, managementPollRetentionCursorKey); err != nil {
		return scanned, 0, err
	}
	if err := tx.Commit(); err != nil {
		return scanned, 0, err
	}
	return scanned, pruned, nil
}

// Retain the newest fairness cursor only for configurations with live jobs.
// Superseded cursors and obsolete revisions do not persist forever merely
// because another configuration on that source is still collecting.
const managementPollRetentionEligible = `p.created_at<? AND p.state IN ('accepted','abandoned')
 AND NOT EXISTS(SELECT 1 FROM run_monitor_batches b WHERE b.management_poll_id=p.id)
 AND NOT EXISTS(SELECT 1 FROM monitor_collection_leases l WHERE l.project_id=p.project_id AND l.source_id=p.source_id AND l.poll_id=p.id AND l.state!='released')
 AND (NOT EXISTS(SELECT 1 FROM run_monitor_jobs j JOIN tests t ON t.id=j.test_id
     WHERE j.project_id=p.project_id AND j.source_id=p.source_id AND j.state='collecting'
     AND j.expires_at>? AND t.status IN ('RUNNING','PAUSED')
     AND j.management_config_key=p.collector_id || '/' || CAST(p.source_revision AS TEXT) || '/' || p.scope_hash || '/' || p.identity_hash)
   OR EXISTS(SELECT 1 FROM monitor_management_polls newer WHERE newer.project_id=p.project_id
     AND newer.source_id=p.source_id AND newer.collector_id=p.collector_id AND newer.source_revision=p.source_revision
     AND newer.scope_hash=p.scope_hash AND newer.identity_hash=p.identity_hash AND newer.poll_sequence>p.poll_sequence))`

func (s *SQLiteStore) PruneManagementPolls(project string, before time.Time, limit int) (int64, error) {
	if before.IsZero() || limit < 1 || limit > 1000 {
		return 0, errors.New("invalid management journal retention")
	}
	tx, e := s.db.Begin()
	if e != nil {
		return 0, e
	}
	defer tx.Rollback()
	if _, e = lockProjectForOwnedMutation(tx, project); e != nil {
		return 0, e
	}
	r, e := tx.Exec(`DELETE FROM monitor_management_polls WHERE id IN (SELECT p.id FROM monitor_management_polls p WHERE p.project_id=? AND `+managementPollRetentionEligible+` ORDER BY p.created_at,p.id LIMIT ?)`, project, before.UTC(), time.Now().UTC(), limit)
	if e != nil {
		return 0, e
	}
	n, e := r.RowsAffected()
	if e != nil {
		return 0, e
	}
	return n, tx.Commit()
}
