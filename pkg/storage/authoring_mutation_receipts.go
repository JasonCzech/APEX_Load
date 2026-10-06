package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	AuthoringReceiptPending  = "pending"
	AuthoringReceiptComplete = "complete"

	AuthoringPurposeScriptCreate   = "script_create"
	AuthoringPurposeScenarioCreate = "scenario_create"
)

var (
	ErrAuthoringReceiptConflict   = errors.New("idempotency key was already used with a different request")
	ErrAuthoringReceiptBusy       = errors.New("idempotent request is already in progress")
	ErrAuthoringReceiptExpired    = errors.New("idempotency key has expired")
	ErrAuthoringReceiptTombstoned = errors.New("the resource created with this idempotency key was deleted")
)

// AuthoringMutationReceipt fences retries of an ordinary authoring create.
// KeyHash and RequestDigest are keyed digests computed by the caller; raw keys,
// request bodies and credentials are never stored. ResourceID is fixed when
// the receipt is first claimed, so a retry creates (or finds) the same object.
type AuthoringMutationReceipt struct {
	ID               string
	ProjectID        string
	Actor            string
	Purpose          string
	KeyHash          string
	RequestDigest    string
	ResourceType     string
	ResourceID       string
	ResourceRevision string
	State            string
	OwnerToken       string
	LeaseExpiresAt   *time.Time
	ExpiresAt        time.Time
	TombstonedAt     *time.Time
	TombstoneReason  string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type AuthoringMutationReceiptClaimInput struct {
	ProjectID     string
	Actor         string
	Purpose       string
	KeyHash       string
	RequestDigest string
	ResourceType  string
	ResourceID    string
	OwnerToken    string
	// Retention is how long the key is guaranteed to replay; after it a retry
	// is refused rather than treated as a new request.
	Retention time.Duration
}

// AuthoringMutationReceiptClaim reports how a claim resolved. Created is a
// fresh reservation; Replay is a completed receipt whose resource should be
// returned; neither means a stale pending receipt was taken over and the
// create must be resumed (the fixed ResourceID makes resumption safe).
type AuthoringMutationReceiptClaim struct {
	Receipt *AuthoringMutationReceipt
	Created bool
	Replay  bool
}

// AuthoringMutationReceiptStore is optional; lightweight stores do not emulate
// durable retry fencing and callers refuse an Idempotency-Key without it.
type AuthoringMutationReceiptStore interface {
	ClaimAuthoringMutationReceipt(in AuthoringMutationReceiptClaimInput, now time.Time, lease time.Duration) (*AuthoringMutationReceiptClaim, error)
	CompleteAuthoringMutationReceipt(id, ownerToken, resourceRevision string, now time.Time) (bool, error)
	AbandonAuthoringMutationReceipt(id, ownerToken string) (bool, error)
	ReleaseAuthoringMutationReceiptLease(id, ownerToken string) (bool, error)
	TombstoneAuthoringMutationReceipts(resourceType, resourceID, reason string, now time.Time) error
	RestoreAuthoringMutationReceiptTombstones(resourceType, resourceID, reason string, at time.Time) error
}

var _ AuthoringMutationReceiptStore = (*SQLiteStore)(nil)

const authoringReceiptColumns = `id, project_id, actor, purpose, key_hash, request_digest, resource_type, resource_id,
	resource_revision, state, owner_token, lease_expires_at, expires_at, tombstoned_at, tombstone_reason, created_at, updated_at`

func (s *SQLiteStore) ClaimAuthoringMutationReceipt(in AuthoringMutationReceiptClaimInput, now time.Time, lease time.Duration) (_ *AuthoringMutationReceiptClaim, resultErr error) {
	in.ProjectID = normalizeProjectID(strings.TrimSpace(in.ProjectID))
	for _, field := range []*string{&in.Actor, &in.Purpose, &in.KeyHash, &in.RequestDigest, &in.ResourceType, &in.ResourceID, &in.OwnerToken} {
		*field = strings.TrimSpace(*field)
	}
	if in.Actor == "" || in.Purpose == "" || in.KeyHash == "" || in.RequestDigest == "" || in.ResourceType == "" || in.ResourceID == "" || in.OwnerToken == "" {
		return nil, errors.New("authoring receipt requires actor, purpose, key hash, request digest, resource and owner token")
	}
	if lease <= 0 || in.Retention <= 0 {
		return nil, errors.New("authoring receipt lease and retention must be positive")
	}
	now = now.UTC()
	leaseExpiresAt := now.Add(lease)
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin authoring receipt claim: %w", err)
	}
	defer func() {
		if resultErr != nil {
			_ = tx.Rollback()
		}
	}()
	insert, err := tx.Exec(`INSERT OR IGNORE INTO authoring_mutation_receipts (
		id, project_id, actor, purpose, key_hash, request_digest, resource_type, resource_id,
		resource_revision, state, owner_token, lease_expires_at, expires_at, tombstoned_at, tombstone_reason, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, '', ?, ?, ?, ?, NULL, '', ?, ?)`,
		uuid.NewString(), in.ProjectID, in.Actor, in.Purpose, in.KeyHash, in.RequestDigest, in.ResourceType, in.ResourceID,
		AuthoringReceiptPending, in.OwnerToken, leaseExpiresAt, now.Add(in.Retention), now, now)
	if err != nil {
		return nil, fmt.Errorf("reserve authoring receipt: %w", err)
	}
	inserted, err := insert.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("inspect authoring receipt reservation: %w", err)
	}
	receipt, err := scanAuthoringMutationReceipt(tx.QueryRow(`SELECT `+authoringReceiptColumns+`
		FROM authoring_mutation_receipts WHERE project_id = ? AND actor = ? AND purpose = ? AND key_hash = ?`,
		in.ProjectID, in.Actor, in.Purpose, in.KeyHash))
	if err != nil {
		return nil, fmt.Errorf("load authoring receipt: %w", err)
	}
	commit := func(claim *AuthoringMutationReceiptClaim) (*AuthoringMutationReceiptClaim, error) {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit authoring receipt claim: %w", err)
		}
		return claim, nil
	}
	if inserted == 1 {
		return commit(&AuthoringMutationReceiptClaim{Receipt: receipt, Created: true})
	}
	// Refusals are ordered so a deleted resource is reported even after the
	// key expires, and a changed request never learns about the original.
	if receipt.RequestDigest != in.RequestDigest || receipt.ResourceType != in.ResourceType {
		return nil, ErrAuthoringReceiptConflict
	}
	if receipt.TombstonedAt != nil {
		return nil, ErrAuthoringReceiptTombstoned
	}
	if !now.Before(receipt.ExpiresAt) {
		return nil, ErrAuthoringReceiptExpired
	}
	if receipt.State == AuthoringReceiptComplete {
		return commit(&AuthoringMutationReceiptClaim{Receipt: receipt, Replay: true})
	}
	if receipt.LeaseExpiresAt != nil && receipt.LeaseExpiresAt.After(now) {
		return nil, ErrAuthoringReceiptBusy
	}
	update, err := tx.Exec(`UPDATE authoring_mutation_receipts SET owner_token = ?, lease_expires_at = ?, updated_at = ?
		WHERE id = ? AND state = ? AND tombstoned_at IS NULL AND (lease_expires_at IS NULL OR lease_expires_at <= ?)`,
		in.OwnerToken, leaseExpiresAt, now, receipt.ID, AuthoringReceiptPending, now)
	if err != nil {
		return nil, fmt.Errorf("take over stale authoring receipt: %w", err)
	}
	if updated, err := update.RowsAffected(); err != nil || updated != 1 {
		if err != nil {
			return nil, fmt.Errorf("inspect stale authoring receipt takeover: %w", err)
		}
		return nil, ErrAuthoringReceiptBusy
	}
	receipt.OwnerToken, receipt.LeaseExpiresAt, receipt.UpdatedAt = in.OwnerToken, &leaseExpiresAt, now
	return commit(&AuthoringMutationReceiptClaim{Receipt: receipt})
}

func (s *SQLiteStore) CompleteAuthoringMutationReceipt(id, ownerToken, resourceRevision string, now time.Time) (bool, error) {
	id, ownerToken = strings.TrimSpace(id), strings.TrimSpace(ownerToken)
	if id == "" || ownerToken == "" {
		return false, errors.New("authoring receipt completion requires id and owner token")
	}
	result, err := s.db.Exec(`UPDATE authoring_mutation_receipts
		SET state = ?, owner_token = '', lease_expires_at = NULL, resource_revision = ?, updated_at = ?
		WHERE id = ? AND state = ? AND owner_token = ? AND tombstoned_at IS NULL`,
		AuthoringReceiptComplete, strings.TrimSpace(resourceRevision), now.UTC(), id, AuthoringReceiptPending, ownerToken)
	if err != nil {
		return false, fmt.Errorf("complete authoring receipt: %w", err)
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

// ReleaseAuthoringMutationReceiptLease keeps a pending reservation but drops
// its owner and lease, so the next retry resumes it at once and its durable
// read decides between adopting a committed resource and creating it.
func (s *SQLiteStore) ReleaseAuthoringMutationReceiptLease(id, ownerToken string) (bool, error) {
	id, ownerToken = strings.TrimSpace(id), strings.TrimSpace(ownerToken)
	if id == "" || ownerToken == "" {
		return false, errors.New("authoring receipt lease release requires id and owner token")
	}
	result, err := s.db.Exec(`UPDATE authoring_mutation_receipts SET owner_token = '', lease_expires_at = NULL, updated_at = ? WHERE id = ? AND state = ? AND owner_token = ?`,
		time.Now().UTC(), id, AuthoringReceiptPending, ownerToken)
	if err != nil {
		return false, fmt.Errorf("release authoring receipt lease: %w", err)
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

// AbandonAuthoringMutationReceipt removes a pending reservation whose create
// definitely did not happen (for example a validation failure), so the same
// key can be retried with a corrected request.
func (s *SQLiteStore) AbandonAuthoringMutationReceipt(id, ownerToken string) (bool, error) {
	id, ownerToken = strings.TrimSpace(id), strings.TrimSpace(ownerToken)
	if id == "" || ownerToken == "" {
		return false, errors.New("authoring receipt abandonment requires id and owner token")
	}
	result, err := s.db.Exec(`DELETE FROM authoring_mutation_receipts WHERE id = ? AND state = ? AND owner_token = ?`,
		id, AuthoringReceiptPending, ownerToken)
	if err != nil {
		return false, fmt.Errorf("abandon authoring receipt: %w", err)
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

// TombstoneAuthoringMutationReceipts records that a resource was deleted, so
// a later retry of the request that created it can never recreate it. Callers
// tombstone before deleting and withdraw the tombstone when the delete is
// definitely refused; a delete whose outcome is unknown keeps it, which
// refuses retries but never duplicates or resurrects a resource.
func (s *SQLiteStore) TombstoneAuthoringMutationReceipts(resourceType, resourceID, reason string, now time.Time) error {
	resourceType, resourceID = strings.TrimSpace(resourceType), strings.TrimSpace(resourceID)
	if resourceType == "" || resourceID == "" {
		return errors.New("authoring receipt tombstone requires resource type and id")
	}
	_, err := s.db.Exec(`UPDATE authoring_mutation_receipts SET tombstoned_at = ?, tombstone_reason = ?, owner_token = '', lease_expires_at = NULL, updated_at = ?
		WHERE resource_type = ? AND resource_id = ? AND tombstoned_at IS NULL`,
		now.UTC(), strings.TrimSpace(reason), now.UTC(), resourceType, resourceID)
	if err != nil {
		return fmt.Errorf("tombstone authoring receipts: %w", err)
	}
	return nil
}

// RestoreAuthoringMutationReceiptTombstones withdraws the tombstones written
// at exactly at for this resource, after its delete was definitely refused,
// and only while the resource still exists: a refused delete that raced a
// successful one must not withdraw the tombstone the deleted resource needs.
// Earlier tombstones are untouched. A withdrawn pending receipt has no owner
// or lease, so the next retry resumes it and adopts the live resource.
func (s *SQLiteStore) RestoreAuthoringMutationReceiptTombstones(resourceType, resourceID, reason string, at time.Time) error {
	resourceType, resourceID = strings.TrimSpace(resourceType), strings.TrimSpace(resourceID)
	if resourceType == "" || resourceID == "" {
		return errors.New("authoring receipt tombstone restore requires resource type and id")
	}
	table := ""
	switch resourceType {
	case "scenario":
		table = "scenarios"
	case "script":
		table = "scripts"
	default:
		return fmt.Errorf("authoring receipt tombstone restore: unsupported resource type %q", resourceType)
	}
	_, err := s.db.Exec(`UPDATE authoring_mutation_receipts SET tombstoned_at = NULL, tombstone_reason = '', updated_at = ?
		WHERE resource_type = ? AND resource_id = ? AND tombstoned_at = ? AND tombstone_reason = ?
		AND EXISTS (SELECT 1 FROM `+table+` WHERE id = ?)`,
		time.Now().UTC(), resourceType, resourceID, at.UTC(), strings.TrimSpace(reason), resourceID)
	if err != nil {
		return fmt.Errorf("restore authoring receipt tombstones: %w", err)
	}
	return nil
}

func scanAuthoringMutationReceipt(row scanner) (*AuthoringMutationReceipt, error) {
	var receipt AuthoringMutationReceipt
	var leaseExpiresAt, tombstonedAt sql.NullTime
	if err := row.Scan(&receipt.ID, &receipt.ProjectID, &receipt.Actor, &receipt.Purpose, &receipt.KeyHash, &receipt.RequestDigest,
		&receipt.ResourceType, &receipt.ResourceID, &receipt.ResourceRevision, &receipt.State, &receipt.OwnerToken,
		&leaseExpiresAt, &receipt.ExpiresAt, &tombstonedAt, &receipt.TombstoneReason, &receipt.CreatedAt, &receipt.UpdatedAt); err != nil {
		return nil, err
	}
	if leaseExpiresAt.Valid {
		value := leaseExpiresAt.Time
		receipt.LeaseExpiresAt = &value
	}
	if tombstonedAt.Valid {
		value := tombstonedAt.Time
		receipt.TombstonedAt = &value
	}
	return &receipt, nil
}
