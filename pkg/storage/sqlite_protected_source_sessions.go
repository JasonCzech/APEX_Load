package storage

import (
	"errors"
	"strings"
	"time"
)

const protectedSourceSecretSessionCapacity = 128

func (s *SQLiteStore) CreateProtectedSourceSecretSession(session *ProtectedSourceSecretSessionCipher) (bool, error) {
	if err := validateProtectedSourceSecretSession(s, session); err != nil {
		return false, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM protected_source_secret_sessions WHERE expires_at <= ?`, time.Now().UTC()); err != nil {
		return false, err
	}
	for slot := 0; slot < protectedSourceSecretSessionCapacity; slot++ {
		result, err := tx.Exec(`
			INSERT INTO protected_source_secret_sessions (
				id, project_id, actor_subject, capacity_slot, ciphertext,
				wrapped_key_json, expires_at, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT DO NOTHING
		`, session.ID, session.ProjectID, session.Actor, slot, session.Ciphertext,
			session.WrappedKeyJSON, session.ExpiresAt, session.CreatedAt)
		if err != nil {
			return false, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return false, err
		}
		if affected != 1 {
			continue
		}
		if err := tx.Commit(); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, ErrProtectedSourceSecretSessionCapacity
}

func (s *SQLiteStore) ClaimProtectedSourceSecretSession(id, projectID, actor, claimToken string, now, claimExpiresAt time.Time) (*ProtectedSourceSecretSessionCipher, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("protected source secret session storage is not configured")
	}
	id = strings.TrimSpace(id)
	projectID = strings.TrimSpace(projectID)
	actor = strings.TrimSpace(actor)
	claimToken = strings.TrimSpace(claimToken)
	now = now.UTC()
	claimExpiresAt = claimExpiresAt.UTC()
	if id == "" || projectID == "" || actor == "" || claimToken == "" || !claimExpiresAt.After(now) {
		return nil, errors.New("protected source secret session claim scope is required")
	}
	var session ProtectedSourceSecretSessionCipher
	err := s.db.QueryRow(`
		UPDATE protected_source_secret_sessions
		SET claim_token = ?, claim_expires_at = ?
		WHERE id = ? AND project_id = ? AND actor_subject = ?
		  AND (claim_token IS NULL OR TRIM(claim_token) = '' OR claim_expires_at IS NULL OR claim_expires_at <= ?)
		RETURNING id, project_id, actor_subject, ciphertext, wrapped_key_json, expires_at, created_at
	`, claimToken, claimExpiresAt, id, projectID, actor, now).Scan(
		&session.ID, &session.ProjectID, &session.Actor, &session.Ciphertext,
		&session.WrappedKeyJSON, &session.ExpiresAt, &session.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &session, nil
}

func (s *SQLiteStore) FinalizeProtectedSourceSecretSession(id, projectID, actor, claimToken string) (bool, error) {
	return s.mutateProtectedSourceSecretSessionClaim(true, id, projectID, actor, claimToken)
}

func (s *SQLiteStore) ReleaseProtectedSourceSecretSessionClaim(id, projectID, actor, claimToken string) (bool, error) {
	return s.mutateProtectedSourceSecretSessionClaim(false, id, projectID, actor, claimToken)
}

func (s *SQLiteStore) mutateProtectedSourceSecretSessionClaim(finalize bool, id, projectID, actor, claimToken string) (bool, error) {
	if s == nil || s.db == nil {
		return false, errors.New("protected source secret session storage is not configured")
	}
	id = strings.TrimSpace(id)
	projectID = strings.TrimSpace(projectID)
	actor = strings.TrimSpace(actor)
	claimToken = strings.TrimSpace(claimToken)
	if id == "" || projectID == "" || actor == "" || claimToken == "" {
		return false, errors.New("protected source secret session claim scope is required")
	}
	query := `UPDATE protected_source_secret_sessions
		SET claim_token = NULL, claim_expires_at = NULL
		WHERE id = ? AND project_id = ? AND actor_subject = ? AND claim_token = ?`
	if finalize {
		query = `DELETE FROM protected_source_secret_sessions
			WHERE id = ? AND project_id = ? AND actor_subject = ? AND claim_token = ?`
	}
	result, err := s.db.Exec(query, id, projectID, actor, claimToken)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

func (s *SQLiteStore) DeleteExpiredProtectedSourceSecretSessions(now time.Time) (int64, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("protected source secret session storage is not configured")
	}
	result, err := s.db.Exec(`DELETE FROM protected_source_secret_sessions WHERE expires_at <= ?`, now.UTC())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func validateProtectedSourceSecretSession(store *SQLiteStore, session *ProtectedSourceSecretSessionCipher) error {
	if store == nil || store.db == nil || session == nil {
		return errors.New("protected source secret session storage is not configured")
	}
	session.ID = strings.TrimSpace(session.ID)
	session.ProjectID = strings.TrimSpace(session.ProjectID)
	session.Actor = strings.TrimSpace(session.Actor)
	session.Ciphertext = strings.TrimSpace(session.Ciphertext)
	session.WrappedKeyJSON = strings.TrimSpace(session.WrappedKeyJSON)
	if session.ID == "" || session.ProjectID == "" || session.Actor == "" || session.Ciphertext == "" ||
		session.WrappedKeyJSON == "" || session.ExpiresAt.IsZero() {
		return errors.New("protected source secret session encrypted fields are required")
	}
	if session.CreatedAt.IsZero() {
		session.CreatedAt = time.Now().UTC()
	}
	session.ExpiresAt = session.ExpiresAt.UTC().Truncate(time.Microsecond)
	session.CreatedAt = session.CreatedAt.UTC().Truncate(time.Microsecond)
	return nil
}

var _ ProtectedSourceSecretSessionStore = (*SQLiteStore)(nil)
var _ ProtectedSourceSecretSessionStore = (*PostgresStore)(nil)
