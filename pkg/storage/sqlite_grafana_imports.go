package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *SQLiteStore) SaveGrafanaImportSession(session *GrafanaImportSession, maxActive int, maxActiveBytes int64) error {
	if session == nil || strings.TrimSpace(session.ID) == "" || strings.TrimSpace(session.Content) == "" || strings.TrimSpace(session.SourceSHA256) == "" {
		return errors.New("grafana import session id and content are required")
	}
	session.ProjectID = normalizeProjectID(session.ProjectID)
	if session.CreatedAt.IsZero() {
		session.CreatedAt = time.Now().UTC()
	}
	if !session.ExpiresAt.After(session.CreatedAt) {
		return errors.New("grafana import session expiry must be after creation")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Serialize the global count-and-insert decision across controller replicas.
	// The default project is undeletable and gives both SQLite and PostgreSQL a
	// durable lock row without introducing a database-specific advisory lock.
	if _, err := lockProjectForOwnedMutation(tx, DefaultProjectID); err != nil {
		return err
	}
	// Also fence the owning project against concurrent deletion. All session
	// creators take the global row first, so the two-lock order is consistent.
	if session.ProjectID != DefaultProjectID {
		if _, err := lockProjectForOwnedMutation(tx, session.ProjectID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM grafana_import_sessions WHERE expires_at <= ?`, session.CreatedAt); err != nil {
		return err
	}
	if maxActive > 0 {
		var count int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM grafana_import_sessions`).Scan(&count); err != nil {
			return err
		}
		if count >= maxActive {
			return fmt.Errorf("%w: maximum %d active sessions", ErrGrafanaImportSessionLimit, maxActive)
		}
	}
	if maxActiveBytes > 0 {
		var activeBytes int64
		byteLengthExpression := `LENGTH(CAST(content AS BLOB))`
		if s.db.dialect == storeDialectPostgres {
			byteLengthExpression = `OCTET_LENGTH(content)`
		}
		if err := tx.QueryRow(`SELECT COALESCE(SUM(` + byteLengthExpression + `), 0) FROM grafana_import_sessions`).Scan(&activeBytes); err != nil {
			return err
		}
		contentBytes := int64(len(session.Content))
		if contentBytes > maxActiveBytes || activeBytes > maxActiveBytes-contentBytes {
			return fmt.Errorf("%w: active session content exceeds %d bytes", ErrGrafanaImportSessionLimit, maxActiveBytes)
		}
	}
	if _, err := tx.Exec(`INSERT INTO grafana_import_sessions
		(id, project_id, name, content, source_sha256, expires_at, claim_token, claimed_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, NULL, NULL, ?)`, session.ID, session.ProjectID, session.Name, session.Content, session.SourceSHA256, session.ExpiresAt, session.CreatedAt); err != nil {
		return err
	}
	return tx.Commit()
}

const grafanaImportSessionSelect = `SELECT id, project_id, COALESCE(name, ''), content,
	source_sha256, expires_at, COALESCE(claim_token, ''), claimed_at, created_at FROM grafana_import_sessions`

func scanGrafanaImportSession(row capacityRowScanner) (*GrafanaImportSession, error) {
	var session GrafanaImportSession
	var claimedAt sql.NullTime
	if err := row.Scan(&session.ID, &session.ProjectID, &session.Name, &session.Content, &session.SourceSHA256,
		&session.ExpiresAt, &session.ClaimToken, &claimedAt, &session.CreatedAt); err != nil {
		return nil, err
	}
	if claimedAt.Valid {
		session.ClaimedAt = claimedAt.Time
	}
	return &session, nil
}

func (s *SQLiteStore) GetGrafanaImportSession(id, projectID string, now time.Time) (*GrafanaImportSession, error) {
	return scanGrafanaImportSession(s.db.QueryRow(grafanaImportSessionSelect+
		` WHERE id = ? AND project_id = ? AND expires_at > ?`, strings.TrimSpace(id), normalizeProjectID(projectID), now))
}

func (s *SQLiteStore) ClaimGrafanaImportSession(id, projectID, token string, now time.Time, staleAfter time.Duration) (*GrafanaImportSession, bool, error) {
	if strings.TrimSpace(token) == "" {
		return nil, false, errors.New("grafana import claim token is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	projectID = normalizeProjectID(projectID)
	if _, err := lockProjectForOwnedMutation(tx, projectID); err != nil {
		return nil, false, err
	}
	claimCondition := `(claim_token IS NULL OR claim_token = '')`
	args := []any{token, now, strings.TrimSpace(id), projectID, now}
	if staleAfter > 0 {
		claimCondition = `(` + claimCondition + ` OR claimed_at IS NULL OR claimed_at <= ?)`
		args = append(args, now.Add(-staleAfter))
	}
	result, err := tx.Exec(`UPDATE grafana_import_sessions SET claim_token = ?, claimed_at = ?
		WHERE id = ? AND project_id = ? AND expires_at > ?
		AND `+claimCondition, args...)
	if err != nil {
		return nil, false, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return nil, false, err
	}
	session, err := scanGrafanaImportSession(tx.QueryRow(grafanaImportSessionSelect+
		` WHERE id = ? AND project_id = ? AND claim_token = ?`, strings.TrimSpace(id), projectID, token))
	if err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return session, true, nil
}

func (s *SQLiteStore) ReleaseGrafanaImportSession(id, projectID, token string) error {
	_, err := s.db.Exec(`UPDATE grafana_import_sessions SET claim_token = NULL, claimed_at = NULL
		WHERE id = ? AND project_id = ? AND claim_token = ?`, strings.TrimSpace(id), normalizeProjectID(projectID), token)
	return err
}

func (s *SQLiteStore) DeleteClaimedGrafanaImportSession(id, projectID, token string) (bool, error) {
	result, err := s.db.Exec(`DELETE FROM grafana_import_sessions WHERE id = ? AND project_id = ? AND claim_token = ?`,
		strings.TrimSpace(id), normalizeProjectID(projectID), token)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

func (s *SQLiteStore) DeleteExpiredGrafanaImportSessions(now time.Time) (int64, error) {
	result, err := s.db.Exec(`DELETE FROM grafana_import_sessions WHERE expires_at <= ?`, now)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *SQLiteStore) SaveGrafanaDashboardSnapshot(snapshot *GrafanaDashboardSnapshot) error {
	if snapshot == nil || strings.TrimSpace(snapshot.TestID) == "" || len(snapshot.Payload) == 0 {
		return errors.New("grafana dashboard snapshot test id and payload are required")
	}
	if err := validateGrafanaDashboardSnapshotSize(snapshot.Payload, MaxGrafanaDashboardSnapshotBytes); err != nil {
		return err
	}
	snapshot.ProjectID = normalizeProjectID(snapshot.ProjectID)
	if snapshot.CapturedAt.IsZero() {
		snapshot.CapturedAt = time.Now().UTC()
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	query := `SELECT COALESCE(project_id, '') FROM tests WHERE id = ?`
	if s.db.dialect == storeDialectPostgres {
		query += ` FOR UPDATE`
	}
	var testProjectID string
	if err := tx.QueryRow(query, snapshot.TestID).Scan(&testProjectID); err != nil {
		return err
	}
	if normalizeProjectID(testProjectID) != snapshot.ProjectID {
		return fmt.Errorf("test %q belongs to another project", snapshot.TestID)
	}
	// A terminal-run snapshot is evidence, so the first successful capture wins.
	// Retries and concurrent finalizers must never rewrite an existing payload.
	if _, err := tx.Exec(`INSERT INTO grafana_dashboard_snapshots (test_id, project_id, payload_json, captured_at)
		VALUES (?, ?, ?, ?) ON CONFLICT(test_id) DO NOTHING`,
		snapshot.TestID, snapshot.ProjectID, string(snapshot.Payload), snapshot.CapturedAt); err != nil {
		return err
	}
	return tx.Commit()
}

func validateGrafanaDashboardSnapshotSize(payload []byte, maxBytes int) error {
	if maxBytes > 0 && len(payload) > maxBytes {
		return fmt.Errorf("grafana dashboard snapshot exceeds %d bytes", maxBytes)
	}
	return nil
}

func (s *SQLiteStore) GetGrafanaDashboardSnapshot(testID, projectID string) (*GrafanaDashboardSnapshot, error) {
	var snapshot GrafanaDashboardSnapshot
	var payload string
	err := s.db.QueryRow(`SELECT test_id, project_id, payload_json, captured_at
		FROM grafana_dashboard_snapshots WHERE test_id = ? AND project_id = ?`, strings.TrimSpace(testID), normalizeProjectID(projectID)).
		Scan(&snapshot.TestID, &snapshot.ProjectID, &payload, &snapshot.CapturedAt)
	if err != nil {
		return nil, err
	}
	snapshot.Payload = []byte(payload)
	return &snapshot, nil
}
