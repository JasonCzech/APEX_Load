package storage

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// RuntimeSecretProvisioningAttempt is a value-free recovery journal written
// before a test create begins provisioning governed runtime credentials. The
// logical names are safe metadata; secret material is never stored here.
type RuntimeSecretProvisioningAttempt struct {
	ID                 string
	ProjectID          string
	ResourceType       string
	TestID             string
	MaterializationKey string
	RefKey             string
	CommitFingerprint  string
	// Actor is the unique provider-ownership token used only for create rollback.
	Actor                 string
	AuditActorType        string
	AuditActorSubject     string
	AuditActorDisplayName string
	SecretNames           []string
	LeaseExpiresAt        time.Time
	CreatedAt             time.Time
}

// RuntimeSecretProvisioningAttemptStore is optional so Store test doubles do
// not acquire a new requirement. Production SQLite and PostgreSQL stores
// implement it.
type RuntimeSecretProvisioningAttemptStore interface {
	CreateRuntimeSecretProvisioningAttempt(*RuntimeSecretProvisioningAttempt) (bool, error)
	ListRuntimeSecretProvisioningAttempts() ([]*RuntimeSecretProvisioningAttempt, error)
	DeleteRuntimeSecretProvisioningAttempt(id, actor string) (bool, error)
}

func (s *SQLiteStore) CreateRuntimeSecretProvisioningAttempt(attempt *RuntimeSecretProvisioningAttempt) (bool, error) {
	if err := validateRuntimeSecretProvisioningAttempt(s, attempt); err != nil {
		return false, err
	}
	namesJSON, err := json.Marshal(attempt.SecretNames)
	if err != nil {
		return false, err
	}
	result, err := s.db.Exec(`
		INSERT INTO runtime_secret_provisioning_attempts (
			id, project_id, resource_type, test_id, materialization_key, ref_key, commit_fingerprint, actor_subject,
			audit_actor_type, audit_actor_subject, audit_actor_display_name, secret_names_json,
			lease_expires_at, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT DO NOTHING
	`, attempt.ID, attempt.ProjectID, attempt.ResourceType, attempt.TestID, attempt.MaterializationKey, attempt.RefKey, attempt.CommitFingerprint, attempt.Actor,
		attempt.AuditActorType, attempt.AuditActorSubject, attempt.AuditActorDisplayName, string(namesJSON),
		attempt.LeaseExpiresAt, attempt.CreatedAt)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

func (s *SQLiteStore) ListRuntimeSecretProvisioningAttempts() ([]*RuntimeSecretProvisioningAttempt, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("runtime secret provisioning attempt storage is not configured")
	}
	rows, err := s.db.Query(`
		SELECT id, project_id, resource_type, test_id, materialization_key, ref_key, commit_fingerprint, actor_subject,
		       audit_actor_type, audit_actor_subject, audit_actor_display_name, secret_names_json,
		       lease_expires_at, created_at
		FROM runtime_secret_provisioning_attempts
		ORDER BY created_at, id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	attempts := make([]*RuntimeSecretProvisioningAttempt, 0)
	for rows.Next() {
		var attempt RuntimeSecretProvisioningAttempt
		var namesJSON string
		if err := rows.Scan(&attempt.ID, &attempt.ProjectID, &attempt.ResourceType, &attempt.TestID,
			&attempt.MaterializationKey, &attempt.RefKey, &attempt.CommitFingerprint, &attempt.Actor,
			&attempt.AuditActorType, &attempt.AuditActorSubject, &attempt.AuditActorDisplayName,
			&namesJSON, &attempt.LeaseExpiresAt, &attempt.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(namesJSON), &attempt.SecretNames); err != nil {
			return nil, err
		}
		attempts = append(attempts, &attempt)
	}
	return attempts, rows.Err()
}

func (s *SQLiteStore) DeleteRuntimeSecretProvisioningAttempt(id, actor string) (bool, error) {
	if s == nil || s.db == nil {
		return false, errors.New("runtime secret provisioning attempt storage is not configured")
	}
	id, actor = strings.TrimSpace(id), strings.TrimSpace(actor)
	if id == "" || actor == "" {
		return false, errors.New("runtime secret provisioning attempt id and actor are required")
	}
	result, err := s.db.Exec(`
		DELETE FROM runtime_secret_provisioning_attempts
		WHERE id = ? AND actor_subject = ?
	`, id, actor)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

func validateRuntimeSecretProvisioningAttempt(store *SQLiteStore, attempt *RuntimeSecretProvisioningAttempt) error {
	if store == nil || store.db == nil || attempt == nil {
		return errors.New("runtime secret provisioning attempt storage is not configured")
	}
	attempt.ID = strings.TrimSpace(attempt.ID)
	attempt.ProjectID = normalizeProjectID(attempt.ProjectID)
	attempt.ResourceType = strings.ToLower(strings.TrimSpace(attempt.ResourceType))
	if attempt.ResourceType == "" {
		attempt.ResourceType = "test"
	}
	attempt.TestID = strings.TrimSpace(attempt.TestID)
	attempt.MaterializationKey = strings.TrimSpace(attempt.MaterializationKey)
	attempt.RefKey = strings.TrimSpace(attempt.RefKey)
	attempt.CommitFingerprint = strings.TrimSpace(attempt.CommitFingerprint)
	attempt.Actor = strings.TrimSpace(attempt.Actor)
	attempt.AuditActorType = strings.ToLower(strings.TrimSpace(attempt.AuditActorType))
	attempt.AuditActorSubject = strings.TrimSpace(attempt.AuditActorSubject)
	attempt.AuditActorDisplayName = strings.TrimSpace(attempt.AuditActorDisplayName)
	if attempt.AuditActorType == "" {
		attempt.AuditActorType = "system"
	}
	if attempt.AuditActorSubject == "" {
		attempt.AuditActorSubject = "controller:test-creation"
	}
	if attempt.ID == "" || attempt.ProjectID == "" || attempt.TestID == "" || attempt.Actor == "" ||
		attempt.LeaseExpiresAt.IsZero() || len(attempt.SecretNames) == 0 {
		return errors.New("runtime secret provisioning attempt fields are required")
	}
	switch attempt.ResourceType {
	case "test":
	case "scenario":
		if attempt.CommitFingerprint == "" {
			return errors.New("scenario runtime secret provisioning attempts require a commit fingerprint")
		}
	default:
		return errors.New("runtime secret provisioning attempt resource type is invalid")
	}
	seen := make(map[string]struct{}, len(attempt.SecretNames))
	for index, name := range attempt.SecretNames {
		name = strings.TrimSpace(name)
		if name == "" {
			return errors.New("runtime secret provisioning attempt names are required")
		}
		if _, duplicate := seen[name]; duplicate {
			return errors.New("runtime secret provisioning attempt names must be unique")
		}
		seen[name] = struct{}{}
		attempt.SecretNames[index] = name
	}
	if attempt.CreatedAt.IsZero() {
		attempt.CreatedAt = time.Now().UTC()
	}
	attempt.LeaseExpiresAt = attempt.LeaseExpiresAt.UTC().Truncate(time.Microsecond)
	attempt.CreatedAt = attempt.CreatedAt.UTC().Truncate(time.Microsecond)
	return nil
}

var _ RuntimeSecretProvisioningAttemptStore = (*SQLiteStore)(nil)
var _ RuntimeSecretProvisioningAttemptStore = (*PostgresStore)(nil)
