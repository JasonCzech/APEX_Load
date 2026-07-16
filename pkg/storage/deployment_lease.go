package storage

import (
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const DefaultDeploymentLeaseName = "controller-deployment"

var (
	ErrDeploymentLeaseActiveTests = errors.New("active tests prevent deployment lease acquisition")
	ErrDeploymentLeaseOwned       = errors.New("deployment lease is held by another owner")
	ErrDeploymentLeaseNotHeld     = errors.New("deployment lease is not held by this owner")
	ErrDeploymentLeaseExpired     = errors.New("deployment lease ownership has expired")
	ErrDeploymentLeaseActive      = errors.New("test start blocked by an active deployment lease")
)

// DeploymentLeaseContext is deliberately bounded to non-secret deployment
// provenance. Credentials and the lease token are never accepted as context.
type DeploymentLeaseContext struct {
	Release     string `json:"release,omitempty"`
	Environment string `json:"environment,omitempty"`
	Actor       string `json:"actor,omitempty"`
	Repository  string `json:"repository,omitempty"`
	ServerURL   string `json:"server_url,omitempty"`
	RunID       string `json:"run_id,omitempty"`
	RunAttempt  string `json:"run_attempt,omitempty"`
	ApprovalID  string `json:"approval_id,omitempty"`
}

type DeploymentLease struct {
	Name       string                 `json:"name"`
	OwnerID    string                 `json:"owner_id,omitempty"`
	TokenHash  string                 `json:"-"`
	AcquiredAt time.Time              `json:"acquired_at,omitempty"`
	ExpiresAt  time.Time              `json:"expires_at,omitempty"`
	UpdatedAt  time.Time              `json:"updated_at,omitempty"`
	Active     bool                   `json:"active"`
	Context    DeploymentLeaseContext `json:"context,omitempty"`
}

// DeploymentLeaseStore is an optional storage capability used by deployment
// controllers and by the test-start fence. Production stores must implement it;
// callers fail closed if a configured store does not.
type DeploymentLeaseStore interface {
	AcquireDeploymentLease(name, ownerID, tokenHash string, ttl time.Duration, context DeploymentLeaseContext, allowActiveTests bool) (*DeploymentLease, int, error)
	RenewDeploymentLease(name, ownerID, tokenHash string, ttl time.Duration) (*DeploymentLease, error)
	ReleaseDeploymentLease(name, ownerID, tokenHash string) (*DeploymentLease, error)
	GetDeploymentLease(name string) (*DeploymentLease, error)
	SaveTestStartIfNoDeploymentLease(test *TestRecord, deploymentLeaseName, authorizedDeploymentOwnerID, authorizedDeploymentTokenHash string) error
	SaveTestStartIfExecutionLeaseHeldAndNoDeploymentLease(test *TestRecord, executionLeaseName, executionLeaseOwnerID, deploymentLeaseName, authorizedDeploymentOwnerID, authorizedDeploymentTokenHash string) (bool, error)
}

func (s *SQLiteStore) AcquireDeploymentLease(name, ownerID, tokenHash string, ttl time.Duration, context DeploymentLeaseContext, allowActiveTests bool) (*DeploymentLease, int, error) {
	if err := validateDeploymentLeaseIdentity(name, ownerID, tokenHash, ttl); err != nil {
		return nil, 0, err
	}
	contextJSON, err := json.Marshal(context)
	if err != nil {
		return nil, 0, fmt.Errorf("encode deployment lease context: %w", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	lease, now, err := lockDeploymentLeaseInTransaction(tx, s.db.dialect, name)
	if err != nil {
		return nil, 0, err
	}
	if lease.Active {
		if lease.OwnerID != ownerID || !deploymentLeaseTokenHashesEqual(lease.TokenHash, tokenHash) {
			return lease, 0, ErrDeploymentLeaseOwned
		}
		lease.ExpiresAt = now.Add(ttl)
		lease.UpdatedAt = now
		lease.Context = context
		if _, err := tx.Exec(`UPDATE deployment_leases SET expires_at=?, updated_at=?, context_json=? WHERE name=? AND owner_id=? AND token_hash=?`, lease.ExpiresAt, now, string(contextJSON), name, ownerID, tokenHash); err != nil {
			return nil, 0, err
		}
		if err := tx.Commit(); err != nil {
			return nil, 0, err
		}
		return lease, 0, nil
	}

	activeCount, err := countExecutingTestsInTransaction(tx)
	if err != nil {
		return nil, 0, fmt.Errorf("count active tests: %w", err)
	}
	if activeCount > 0 && !allowActiveTests {
		return lease, activeCount, ErrDeploymentLeaseActiveTests
	}
	expiresAt := now.Add(ttl)
	if _, err := tx.Exec(`UPDATE deployment_leases SET owner_id=?, token_hash=?, acquired_at=?, expires_at=?, updated_at=?, context_json=? WHERE name=?`, ownerID, tokenHash, now, expiresAt, now, string(contextJSON), name); err != nil {
		return nil, 0, err
	}
	lease = &DeploymentLease{Name: name, OwnerID: ownerID, TokenHash: tokenHash, AcquiredAt: now, ExpiresAt: expiresAt, UpdatedAt: now, Active: true, Context: context}
	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}
	return lease, activeCount, nil
}

func (s *SQLiteStore) RenewDeploymentLease(name, ownerID, tokenHash string, ttl time.Duration) (*DeploymentLease, error) {
	if err := validateDeploymentLeaseIdentity(name, ownerID, tokenHash, ttl); err != nil {
		return nil, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	lease, now, err := lockDeploymentLeaseInTransaction(tx, s.db.dialect, name)
	if err != nil {
		return nil, err
	}
	if lease.OwnerID == ownerID && deploymentLeaseTokenHashesEqual(lease.TokenHash, tokenHash) && !lease.Active {
		return lease, ErrDeploymentLeaseExpired
	}
	if !lease.Active || lease.OwnerID != ownerID || !deploymentLeaseTokenHashesEqual(lease.TokenHash, tokenHash) {
		return lease, ErrDeploymentLeaseNotHeld
	}
	lease.ExpiresAt = now.Add(ttl)
	lease.UpdatedAt = now
	if _, err := tx.Exec(`UPDATE deployment_leases SET expires_at=?, updated_at=? WHERE name=? AND owner_id=? AND token_hash=?`, lease.ExpiresAt, now, name, ownerID, tokenHash); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return lease, nil
}

func (s *SQLiteStore) ReleaseDeploymentLease(name, ownerID, tokenHash string) (*DeploymentLease, error) {
	if err := validateDeploymentLeaseIdentity(name, ownerID, tokenHash, time.Second); err != nil {
		return nil, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	lease, now, err := lockDeploymentLeaseInTransaction(tx, s.db.dialect, name)
	if err != nil {
		return nil, err
	}
	if lease.OwnerID == ownerID && deploymentLeaseTokenHashesEqual(lease.TokenHash, tokenHash) && !lease.Active {
		// Preserve the ownership proof and make an explicit release identifiable
		// so a transport-level retry can receive the same successful outcome.
		// A naturally expired lease has expires_at later than updated_at and
		// remains distinguishable from this release marker.
		if lease.ExpiresAt.Equal(lease.UpdatedAt) {
			return lease, nil
		}
		return lease, ErrDeploymentLeaseExpired
	}
	if !lease.Active || lease.OwnerID != ownerID || !deploymentLeaseTokenHashesEqual(lease.TokenHash, tokenHash) {
		return lease, ErrDeploymentLeaseNotHeld
	}
	if _, err := tx.Exec(`UPDATE deployment_leases SET expires_at=?, updated_at=? WHERE name=? AND owner_id=? AND token_hash=?`, now, now, name, ownerID, tokenHash); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	lease.Active = false
	lease.ExpiresAt = now
	lease.UpdatedAt = now
	return lease, nil
}

func (s *SQLiteStore) GetDeploymentLease(name string) (*DeploymentLease, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("deployment lease name is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	lease, _, err := lockDeploymentLeaseInTransaction(tx, s.db.dialect, name)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return lease, nil
}

func (s *SQLiteStore) SaveTestStartIfNoDeploymentLease(test *TestRecord, deploymentLeaseName, authorizedDeploymentOwnerID, authorizedDeploymentTokenHash string) error {
	return s.saveTestStartIfNoDeploymentLease(test, "", "", deploymentLeaseName, authorizedDeploymentOwnerID, authorizedDeploymentTokenHash, false)
}

func (s *SQLiteStore) SaveTestStartIfExecutionLeaseHeldAndNoDeploymentLease(test *TestRecord, executionLeaseName, executionLeaseOwnerID, deploymentLeaseName, authorizedDeploymentOwnerID, authorizedDeploymentTokenHash string) (bool, error) {
	err := s.saveTestStartIfNoDeploymentLease(test, executionLeaseName, executionLeaseOwnerID, deploymentLeaseName, authorizedDeploymentOwnerID, authorizedDeploymentTokenHash, true)
	if errors.Is(err, errExecutionLeaseNotHeld) {
		return false, nil
	}
	return err == nil, err
}

var errExecutionLeaseNotHeld = errors.New("execution lease is not held")

func (s *SQLiteStore) saveTestStartIfNoDeploymentLease(test *TestRecord, executionLeaseName, executionLeaseOwnerID, deploymentLeaseName, authorizedDeploymentOwnerID, authorizedDeploymentTokenHash string, requireExecutionLease bool) error {
	if test == nil {
		return errors.New("test is required")
	}
	switch strings.ToUpper(strings.TrimSpace(test.Status)) {
	case "SHAKEOUT", "INITIALIZING":
	default:
		return fmt.Errorf("deployment-fenced test save requires SHAKEOUT or INITIALIZING status, got %q", test.Status)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	lease, _, err := lockDeploymentLeaseInTransaction(tx, s.db.dialect, deploymentLeaseName)
	if err != nil {
		return err
	}
	authorized := strings.TrimSpace(authorizedDeploymentOwnerID) != "" &&
		strings.TrimSpace(authorizedDeploymentTokenHash) != "" &&
		lease.OwnerID == strings.TrimSpace(authorizedDeploymentOwnerID) &&
		deploymentLeaseTokenHashesEqual(lease.TokenHash, strings.TrimSpace(authorizedDeploymentTokenHash))
	if lease.Active && !authorized {
		return ErrDeploymentLeaseActive
	}
	if requireExecutionLease {
		held, err := schedulerLeaseHeldInTransaction(tx, s.db.dialect, strings.TrimSpace(executionLeaseName), strings.TrimSpace(executionLeaseOwnerID))
		if err != nil {
			return err
		}
		if !held {
			return errExecutionLeaseNotHeld
		}
	}
	if err := s.saveTestWithExecutor(tx, test); err != nil {
		return err
	}
	return tx.Commit()
}

func validateDeploymentLeaseIdentity(name, ownerID, tokenHash string, ttl time.Duration) error {
	if strings.TrimSpace(name) == "" || strings.TrimSpace(ownerID) == "" || strings.TrimSpace(tokenHash) == "" {
		return errors.New("deployment lease name, owner_id, and token hash are required")
	}
	if ttl <= 0 {
		return errors.New("deployment lease ttl must be positive")
	}
	return nil
}

func deploymentLeaseTokenHashesEqual(left, right string) bool {
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func lockDeploymentLeaseInTransaction(tx *storeTx, dialect storeDialect, name string) (*DeploymentLease, time.Time, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, time.Time{}, errors.New("deployment lease name is required")
	}
	epoch := time.Unix(0, 0).UTC()
	if _, err := tx.Exec(`INSERT INTO deployment_leases (name, owner_id, token_hash, acquired_at, expires_at, updated_at, context_json) VALUES (?, '', '', ?, ?, ?, '{}') ON CONFLICT(name) DO NOTHING`, name, epoch, epoch, epoch); err != nil {
		return nil, time.Time{}, err
	}
	// The no-op write takes SQLite's database writer lock. PostgreSQL uses an
	// explicit row lock below. The insert above also serializes first use.
	if dialect == storeDialectSQLite {
		if _, err := tx.Exec(`UPDATE deployment_leases SET updated_at=updated_at WHERE name=?`, name); err != nil {
			return nil, time.Time{}, err
		}
	}
	query := `SELECT name, owner_id, token_hash, acquired_at, expires_at, updated_at, context_json FROM deployment_leases WHERE name=?`
	if dialect == storeDialectPostgres {
		query += ` FOR UPDATE`
	}
	var lease DeploymentLease
	var contextJSON string
	if err := tx.QueryRow(query, name).Scan(&lease.Name, &lease.OwnerID, &lease.TokenHash, &lease.AcquiredAt, &lease.ExpiresAt, &lease.UpdatedAt, &contextJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, time.Time{}, errors.New("deployment lease guard row is missing")
		}
		return nil, time.Time{}, err
	}
	if strings.TrimSpace(contextJSON) != "" {
		if err := json.Unmarshal([]byte(contextJSON), &lease.Context); err != nil {
			return nil, time.Time{}, fmt.Errorf("decode deployment lease context: %w", err)
		}
	}
	now, err := preciseDatabaseTime(tx, dialect)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("read deployment-lease database clock: %w", err)
	}
	lease.Active = lease.OwnerID != "" && lease.TokenHash != "" && lease.ExpiresAt.After(now)
	return &lease, now, nil
}

func countExecutingTestsInTransaction(tx *storeTx) (int, error) {
	var count int
	err := tx.QueryRow(`SELECT COUNT(*) FROM tests WHERE UPPER(TRIM(COALESCE(status, ''))) IN ('INITIALIZING', 'STARTING', 'SHAKEOUT', 'RUNNING', 'PAUSED', 'STOPPING')`).Scan(&count)
	return count, err
}
