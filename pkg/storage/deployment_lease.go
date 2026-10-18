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
	lease, err := readDeploymentLeaseInTransaction(tx, s.db.dialect, name)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return lease, nil
}

// SaveTestStartIfNoDeploymentLease atomically commits the caller-owned Start
// record. On successful INITIALIZING commit, InitialProvenance is replaced with
// an owned decoded copy of the write-once winner read in that transaction. No
// InitialProvenance changes on failure. Callers must not share the mutable record.
func (s *SQLiteStore) SaveTestStartIfNoDeploymentLease(test *TestRecord, deploymentLeaseName, authorizedDeploymentOwnerID, authorizedDeploymentTokenHash string) error {
	return s.saveTestStartIfNoDeploymentLease(test, "", "", deploymentLeaseName, authorizedDeploymentOwnerID, authorizedDeploymentTokenHash, false)
}

// SaveTestStartIfExecutionLeaseHeldAndNoDeploymentLease has the same owned
// InitialProvenance result contract, additionally fenced by execution leadership.
func (s *SQLiteStore) SaveTestStartIfExecutionLeaseHeldAndNoDeploymentLease(test *TestRecord, executionLeaseName, executionLeaseOwnerID, deploymentLeaseName, authorizedDeploymentOwnerID, authorizedDeploymentTokenHash string) (bool, error) {
	err := s.saveTestStartIfNoDeploymentLease(test, executionLeaseName, executionLeaseOwnerID, deploymentLeaseName, authorizedDeploymentOwnerID, authorizedDeploymentTokenHash, true)
	if errors.Is(err, errExecutionLeaseNotHeld) {
		return false, nil
	}
	return err == nil, err
}

var errExecutionLeaseNotHeld = errors.New("execution lease is not held")

// ErrTestProjectRevisionChanged marks a start transition whose project policy
// or quota revision changed after controller admission was evaluated. Start
// callers may reload their evidence and retry; the SHAKEOUT/INITIALIZING row was
// not written because this check runs in the same transaction as the save.
var ErrTestProjectRevisionChanged = errors.New("test project changed before start commit")

// ErrTestStartAdmissionRevisionChanged marks a start transition whose durable
// launch inputs changed after the controller assembled its evidence snapshot.
// The caller may reload evidence and retry; no SHAKEOUT/INITIALIZING row was
// committed.
var ErrTestStartAdmissionRevisionChanged = errors.New("test start admission state changed before start commit")

// ErrTestStartIdentityChanged marks a start transition whose original durable
// row was deleted or replaced while the controller was preparing the commit.
// Start writes are updates of an existing incarnation; they must never recreate
// a deleted test through the full-row UPSERT used by ordinary metadata saves.
var ErrTestStartIdentityChanged = errors.New("test identity changed before start commit")

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
		var held bool
		if test.ExecutionGeneration > 0 {
			// The generation stamped below must be the lease's real term.
			held, err = schedulerLeaseTermHeldInTransaction(tx, s.db.dialect, strings.TrimSpace(executionLeaseName), strings.TrimSpace(executionLeaseOwnerID), test.ExecutionGeneration)
		} else {
			held, err = schedulerLeaseHeldInTransaction(tx, s.db.dialect, strings.TrimSpace(executionLeaseName), strings.TrimSpace(executionLeaseOwnerID))
		}
		if err != nil {
			return err
		}
		if !held {
			return errExecutionLeaseNotHeld
		}
	}
	// Normalize and marshal the immutable write projection before taking content
	// locks. The resulting args are written only after placement and admission
	// validation complete in this same transaction.
	preparedArgs, err := prepareTestSaveArgs(test)
	if err != nil {
		return err
	}
	if !test.ExpectedProjectUpdatedAt.IsZero() {
		if err := lockRequiredTestParent(tx,
			`UPDATE projects SET updated_at = updated_at WHERE id = ?`, test.ProjectID, "project"); err != nil {
			return err
		}
		var projectUpdatedAt time.Time
		if err := tx.QueryRow(testProjectRevisionSelect, normalizeProjectID(test.ProjectID)).Scan(&projectUpdatedAt); err != nil {
			return err
		}
		if !projectUpdatedAt.Equal(test.ExpectedProjectUpdatedAt) {
			return fmt.Errorf("%w: project %q", ErrTestProjectRevisionChanged, normalizeProjectID(test.ProjectID))
		}
	}
	// SQLite's deployment-lease no-op write above already owns the database
	// writer lock, so compare before this transaction's immediate table triggers
	// advance the revision. PostgreSQL uses deferred triggers and compares only
	// after acquiring every admission-content row lock below, preserving the
	// content-row -> revision-row order used by concurrent writers.
	if s.db.dialect == storeDialectSQLite {
		if err := validateExpectedStartAdmissionRevision(tx, s.db.dialect, test.ExpectedStartAdmissionRevision); err != nil {
			return err
		}
	}
	// A bound capacity reservation is part of launch authorization, not merely
	// display metadata. Lock and revalidate it in this transaction before the
	// full-row upsert can persist SHAKEOUT/INITIALIZING (and potentially restore a
	// stale timeslot_id cleared by a concurrent reject).
	slotID, err := validateCapacityReservationForTestStart(tx, s.db.dialect, test)
	if err != nil {
		return err
	}
	if slotID != "" {
		result, err := tx.Exec(`UPDATE capacity_reservations SET bound_test_status = ?
			WHERE id = ? AND approval_state = 'approved' AND bound_test_id = ?`, test.Status, slotID, test.ID)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return fmt.Errorf("%w: timeslot %q changed before test %q start commit", ErrCapacityReservationStartInvalid, slotID, test.ID)
		}
	}
	// saveTestWithExecutor normally combines placement validation with the
	// upsert. A PostgreSQL start must split those phases: concurrent content
	// writers take their test/scenario rows before deferred triggers take the
	// singleton revision rows, so doing the revision check first can deadlock.
	if err := s.validateExistingTestStartPlacement(tx, test); err != nil {
		return err
	}
	if strings.EqualFold(strings.TrimSpace(test.Status), "INITIALIZING") {
		if err := reconcileQueuedScenarioScheduleOccurrenceForTest(tx, test.ID, scenarioScheduleOccurrenceStarted, time.Time{}, "", false); err != nil {
			return fmt.Errorf("reconcile queued scenario occurrence at test start: %w", err)
		}
	}
	if s.db.dialect == storeDialectPostgres {
		if err := validateExpectedPostgresStartAdmissionRevision(tx, test.ExpectedStartAdmissionRevision); err != nil {
			return err
		}
	}
	if err := s.writePreparedTestWithExecutor(tx, test, preparedArgs); err != nil {
		return err
	}
	if requireExecutionLease && test.ExecutionGeneration > 0 {
		// The full-row write never touches this column; only Start and adoption
		// record which execution term runs the row, and only forward.
		if _, err := tx.Exec(`UPDATE tests SET execution_generation = ? WHERE id = ? AND COALESCE(execution_generation, 0) <= ?`,
			int64(test.ExecutionGeneration), test.ID, int64(test.ExecutionGeneration)); err != nil {
			return fmt.Errorf("record start execution generation: %w", err)
		}
	}
	// Read the exact CASE winner while the same transaction owns the test row.
	// A second read after commit could fail after execution-capable state is
	// durable, or observe a different incarnation. Decode before commit so a
	// corrupt prior receipt rolls back Start rather than exposing a candidate.
	var initial any
	initializing := strings.EqualFold(strings.TrimSpace(test.Status), "INITIALIZING")
	if initializing {
		var raw string
		if err := tx.QueryRow(`SELECT COALESCE(initial_provenance_json, '') FROM tests WHERE id = ? AND project_id = ?`, test.ID, test.ProjectID).Scan(&raw); err != nil {
			return fmt.Errorf("read committed Start provenance: %w", err)
		}
		if strings.TrimSpace(raw) != "" {
			if err := json.Unmarshal([]byte(raw), &initial); err != nil {
				return fmt.Errorf("decode committed Start provenance: %w", err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if initializing {
		test.InitialProvenance = initial
	}
	return nil
}

func validateExistingTestStartIdentity(tx *storeTx, dialect storeDialect, test *TestRecord) error {
	if tx == nil || test == nil {
		return ErrTestStartIdentityChanged
	}
	var refKey, projectID string
	var startTime time.Time
	if err := tx.QueryRow(`SELECT COALESCE(ref_key, ''), COALESCE(project_id, ''), start_time FROM tests WHERE id = ?`, test.ID).
		Scan(&refKey, &projectID, &startTime); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: test %q no longer exists", ErrTestStartIdentityChanged, test.ID)
		}
		return err
	}
	durableStart := startTime.UTC()
	requestedStart := test.StartTime.UTC()
	if dialect == storeDialectPostgres {
		durableStart = durableStart.Truncate(time.Microsecond)
		requestedStart = requestedStart.Truncate(time.Microsecond)
	}
	if strings.TrimSpace(refKey) != strings.TrimSpace(test.RefKey) ||
		normalizeProjectID(projectID) != normalizeProjectID(test.ProjectID) ||
		!durableStart.Equal(requestedStart) {
		return fmt.Errorf("%w: test %q was replaced", ErrTestStartIdentityChanged, test.ID)
	}
	return nil
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

// readDeploymentLeaseInTransaction returns an inactive lease when no durable
// row exists, without installing a guard row or taking a writer lock. Guard
// creation and row fencing belong only to acquisition/start transitions; the
// status endpoint must not mutate durable state on every poll.
func readDeploymentLeaseInTransaction(tx *storeTx, dialect storeDialect, name string) (*DeploymentLease, error) {
	query := `SELECT name, owner_id, token_hash, acquired_at, expires_at, updated_at, context_json FROM deployment_leases WHERE name=?`
	var lease DeploymentLease
	var contextJSON string
	if err := tx.QueryRow(query, name).Scan(&lease.Name, &lease.OwnerID, &lease.TokenHash, &lease.AcquiredAt, &lease.ExpiresAt, &lease.UpdatedAt, &contextJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &DeploymentLease{Name: name}, nil
		}
		return nil, err
	}
	if strings.TrimSpace(contextJSON) != "" {
		if err := json.Unmarshal([]byte(contextJSON), &lease.Context); err != nil {
			return nil, fmt.Errorf("decode deployment lease context: %w", err)
		}
	}
	now, err := preciseDatabaseTime(tx, dialect)
	if err != nil {
		return nil, fmt.Errorf("read deployment-lease database clock: %w", err)
	}
	lease.Active = lease.OwnerID != "" && lease.TokenHash != "" && lease.ExpiresAt.After(now)
	return &lease, nil
}

func countExecutingTestsInTransaction(tx *storeTx) (int, error) {
	var count int
	err := tx.QueryRow(`SELECT COUNT(*) FROM tests WHERE UPPER(TRIM(COALESCE(status, ''))) IN ('INITIALIZING', 'STARTING', 'SHAKEOUT', 'RUNNING', 'PAUSED', 'STOPPING')`).Scan(&count)
	return count, err
}
