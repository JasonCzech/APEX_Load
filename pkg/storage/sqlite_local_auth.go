package storage

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const localAuthFixturePostgresAdvisoryLock = `SELECT pg_advisory_xact_lock(7241801551050296672)`

func normalizeLocalUsername(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func localAuthFixtureSeedLockQuery(dialect storeDialect) string {
	if dialect == storeDialectPostgres {
		return localAuthFixturePostgresAdvisoryLock
	}
	return ""
}

func normalizeLocalAuthFixtures(fixtures []LocalAuthFixture) ([]LocalAuthFixture, error) {
	normalized := make([]LocalAuthFixture, len(fixtures))
	for i := range fixtures {
		fixture := fixtures[i]
		username := normalizeLocalUsername(fixture.Credential.Username)
		if username == "" {
			return nil, fmt.Errorf("local auth fixture username is required")
		}
		if _, err := bcrypt.Cost([]byte(fixture.Credential.PasswordHash)); err != nil {
			return nil, fmt.Errorf("local auth fixture %q password hash must be bcrypt: %w", username, err)
		}

		fixture.User.ID = strings.TrimSpace(fixture.User.ID)
		fixture.User.Subject = strings.TrimSpace(fixture.User.Subject)
		if fixture.User.ID == "" || fixture.User.Subject == "" {
			return nil, fmt.Errorf("local auth fixture user id and subject are required")
		}
		credentialUserID := strings.TrimSpace(fixture.Credential.UserID)
		if credentialUserID != "" && credentialUserID != fixture.User.ID {
			return nil, fmt.Errorf("local auth fixture %q credential user id %q does not match reserved user %q", username, credentialUserID, fixture.User.ID)
		}
		fixture.Credential.Username = username
		fixture.Credential.UserID = fixture.User.ID

		bindings := make([]*UserRoleBinding, len(fixture.Bindings))
		for j, candidate := range fixture.Bindings {
			if candidate == nil {
				return nil, fmt.Errorf("invalid local auth fixture binding for %q", username)
			}
			binding := *candidate
			if err := normalizeUserRoleBindingValues(&binding); err != nil {
				return nil, fmt.Errorf("invalid local auth fixture binding for %q: %w", username, err)
			}
			if binding.UserID != fixture.User.ID {
				return nil, fmt.Errorf("invalid local auth fixture binding for %q", username)
			}
			bindings[j] = &binding
		}
		fixture.Bindings = bindings
		normalized[i] = fixture
	}
	return normalized, nil
}

// EnsureLocalAuthFixtures creates the complete fixture set in one transaction
// and leaves an existing credential's user, active state, password hash, and
// bindings alone. PostgreSQL's transaction-scoped advisory lock serializes this
// startup operation across HA controller replicas; SQLite's single-writer
// transaction provides the equivalent serialization locally.
func (s *SQLiteStore) EnsureLocalAuthFixtures(fixtures []LocalAuthFixture) error {
	normalized, err := normalizeLocalAuthFixtures(fixtures)
	if err != nil {
		return err
	}
	if len(normalized) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if lockQuery := localAuthFixtureSeedLockQuery(s.db.dialect); lockQuery != "" {
		if _, err := tx.Exec(lockQuery); err != nil {
			return fmt.Errorf("lock local auth fixture seed: %w", err)
		}
	}

	for i := range normalized {
		if err := ensureLocalAuthFixtureTx(tx, s.boolValue, normalized[i]); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func ensureLocalAuthFixtureTx(tx *storeTx, boolValue func(bool) any, fixture LocalAuthFixture) error {
	username := fixture.Credential.Username
	var existingUserID, existingPasswordHash string
	err := tx.QueryRow(`SELECT user_id, password_hash FROM local_auth_credentials WHERE username = ?`, username).Scan(&existingUserID, &existingPasswordHash)
	if err == nil {
		if _, hashErr := bcrypt.Cost([]byte(existingPasswordHash)); hashErr != nil {
			return fmt.Errorf("local auth credential %q stored password hash must be bcrypt: %w", username, hashErr)
		}
		var present, subject string
		if lookupErr := tx.QueryRow(`SELECT id, subject FROM users WHERE id = ?`, existingUserID).Scan(&present, &subject); lookupErr != nil {
			if errors.Is(lookupErr, sql.ErrNoRows) {
				return fmt.Errorf("local auth credential %q references a missing user", username)
			}
			return lookupErr
		}
		if present != fixture.User.ID || subject != fixture.User.Subject {
			return fmt.Errorf("reserved local auth identity %q conflicts with existing credential user %q", username, present)
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	var collision string
	collisionErr := tx.QueryRow(`SELECT id FROM users WHERE id = ? OR subject = ? LIMIT 1`, fixture.User.ID, fixture.User.Subject).Scan(&collision)
	if collisionErr == nil {
		return fmt.Errorf("reserved local auth identity %q conflicts with existing user %q", username, collision)
	}
	if !errors.Is(collisionErr, sql.ErrNoRows) {
		return collisionErr
	}
	if _, err := tx.Exec(`INSERT INTO users (id, subject, email, display_name, active, created_at, updated_at, session_epoch)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, fixture.User.ID, fixture.User.Subject, fixture.User.Email,
		fixture.User.DisplayName, boolValue(fixture.User.Active), fixture.User.CreatedAt, fixture.User.UpdatedAt, fixture.User.SessionEpoch); err != nil {
		return err
	}
	for _, binding := range fixture.Bindings {
		if err := normalizeAndLockUserRoleBindingScope(tx, binding); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO user_role_bindings (id, user_id, scope_type, scope_id, role, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, binding.ID, binding.UserID, binding.ScopeType, binding.ScopeID,
			binding.Role, binding.CreatedAt, binding.UpdatedAt); err != nil {
			return err
		}
	}
	credential := fixture.Credential
	if _, err := tx.Exec(`INSERT INTO local_auth_credentials (username, user_id, password_hash, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)`, credential.Username, credential.UserID, credential.PasswordHash,
		credential.CreatedAt, credential.UpdatedAt); err != nil {
		return err
	}
	return nil
}

func (s *SQLiteStore) GetLocalAuthCredential(username string) (*LocalAuthCredential, error) {
	credential := &LocalAuthCredential{}
	err := s.db.QueryRow(`SELECT username, user_id, password_hash, created_at, updated_at
		FROM local_auth_credentials WHERE username = ?`, normalizeLocalUsername(username)).Scan(
		&credential.Username, &credential.UserID, &credential.PasswordHash, &credential.CreatedAt, &credential.UpdatedAt)
	return credential, err
}

func canonicalLocalSessionTokenHash(value string) (string, error) {
	if len(value) != sha256.Size*2 {
		return "", fmt.Errorf("local auth session token hash must be a canonical SHA-256 hex digest")
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || hex.EncodeToString(decoded) != value {
		return "", fmt.Errorf("local auth session token hash must be a canonical SHA-256 hex digest")
	}
	return value, nil
}

func (s *SQLiteStore) SaveLocalAuthSession(session *LocalAuthSession) error {
	if session == nil || strings.TrimSpace(session.UserID) == "" {
		return fmt.Errorf("local auth session token hash and user id are required")
	}
	tokenHash, err := canonicalLocalSessionTokenHash(session.TokenHash)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO local_auth_sessions (token_hash, user_id, session_epoch, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?)`, tokenHash, session.UserID, session.SessionEpoch, session.CreatedAt, session.ExpiresAt)
	return err
}

func (s *SQLiteStore) GetLocalAuthSessionByTokenHash(tokenHash string) (*LocalAuthSession, error) {
	tokenHash, err := canonicalLocalSessionTokenHash(tokenHash)
	if err != nil {
		return nil, err
	}
	session := &LocalAuthSession{}
	err = s.db.QueryRow(`SELECT token_hash, user_id, session_epoch, created_at, expires_at
		FROM local_auth_sessions WHERE token_hash = ?`, tokenHash).Scan(
		&session.TokenHash, &session.UserID, &session.SessionEpoch, &session.CreatedAt, &session.ExpiresAt)
	return session, err
}

func (s *SQLiteStore) DeleteLocalAuthSessionByTokenHash(tokenHash string) error {
	tokenHash, err := canonicalLocalSessionTokenHash(tokenHash)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`DELETE FROM local_auth_sessions WHERE token_hash = ?`, tokenHash)
	return err
}

func (s *SQLiteStore) DeleteExpiredLocalAuthSessions(cutoff time.Time) error {
	_, err := s.db.Exec(`DELETE FROM local_auth_sessions WHERE expires_at <= ?`, cutoff.UTC())
	return err
}
