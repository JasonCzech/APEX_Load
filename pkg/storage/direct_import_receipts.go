package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	DirectImportReceiptPending  = "pending"
	DirectImportReceiptComplete = "complete"
)

var (
	ErrDirectImportReceiptConflict = errors.New("direct import operation conflicts with an existing request")
	ErrDirectImportReceiptBusy     = errors.New("direct import operation is already in progress")
)

// DirectImportReceipt fences retries of direct multi-asset imports. Operation
// keys and request digests are disclosure-safe hashes; raw source credentials
// must never be placed in this record.
type DirectImportReceipt struct {
	ID               string
	ProjectID        string
	SourceProfile    string
	OperationKeyHash string
	RequestSHA256    string
	State            string
	OwnerToken       string
	LeaseExpiresAt   *time.Time
	ScriptIDs        []string
	ScenarioID       string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type DirectImportReceiptClaim struct {
	Receipt *DirectImportReceipt
	Created bool
	Replay  bool
}

// DirectImportReceiptStore is optional so lightweight Store implementations do
// not have to emulate durable retry fencing. Production SQLite/PostgreSQL
// stores implement it.
type DirectImportReceiptStore interface {
	ClaimDirectImportReceipt(
		projectID, sourceProfile, operationKeyHash, requestSHA256, ownerToken string,
		now time.Time, lease time.Duration,
	) (*DirectImportReceiptClaim, error)
	RenewDirectImportReceipt(id, ownerToken string, now time.Time, lease time.Duration) (bool, error)
	CompleteDirectImportReceipt(id, ownerToken string, scriptIDs []string, scenarioID string, now time.Time) (bool, error)
	AbandonDirectImportReceipt(id, ownerToken string) (bool, error)
}

// DirectImportReceiptReleaseStore is an optional extension that permits an
// owner to relinquish a pending receipt without deleting its stable identity.
// Keeping this separate preserves compatibility with receipt stores that can
// safely retain ownership until their ordinary lease expires.
type DirectImportReceiptReleaseStore interface {
	ReleaseDirectImportReceipt(id, ownerToken string, now time.Time) (bool, error)
}

func (s *SQLiteStore) ClaimDirectImportReceipt(
	projectID, sourceProfile, operationKeyHash, requestSHA256, ownerToken string,
	now time.Time, lease time.Duration,
) (_ *DirectImportReceiptClaim, resultErr error) {
	projectID = normalizeProjectID(strings.TrimSpace(projectID))
	sourceProfile = strings.TrimSpace(sourceProfile)
	operationKeyHash = strings.TrimSpace(operationKeyHash)
	requestSHA256 = strings.TrimSpace(requestSHA256)
	ownerToken = strings.TrimSpace(ownerToken)
	if sourceProfile == "" || operationKeyHash == "" || requestSHA256 == "" || ownerToken == "" {
		return nil, errors.New("direct import receipt requires profile, operation key hash, request digest, and owner token")
	}
	if lease <= 0 {
		return nil, errors.New("direct import receipt lease must be positive")
	}
	now = now.UTC()
	expiresAt := now.Add(lease)
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin direct import receipt claim: %w", err)
	}
	defer func() {
		if resultErr != nil {
			_ = tx.Rollback()
		}
	}()

	id := uuid.NewString()
	insertResult, err := tx.Exec(`INSERT OR IGNORE INTO direct_import_receipts (
		id, project_id, source_profile, operation_key_hash, request_sha256,
		state, owner_token, lease_expires_at, script_ids_json, scenario_id,
		created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, '[]', NULL, ?, ?)`,
		id, projectID, sourceProfile, operationKeyHash, requestSHA256,
		DirectImportReceiptPending, ownerToken, expiresAt, now, now)
	if err != nil {
		return nil, fmt.Errorf("reserve direct import receipt: %w", err)
	}
	inserted, err := insertResult.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("inspect direct import receipt reservation: %w", err)
	}

	receipt, err := scanDirectImportReceipt(tx.QueryRow(`SELECT
		id, project_id, source_profile, operation_key_hash, request_sha256,
		state, owner_token, lease_expires_at, script_ids_json, scenario_id,
		created_at, updated_at
		FROM direct_import_receipts
		WHERE project_id = ? AND source_profile = ? AND operation_key_hash = ?`,
		projectID, sourceProfile, operationKeyHash))
	if err != nil {
		return nil, fmt.Errorf("load direct import receipt: %w", err)
	}
	if receipt.RequestSHA256 != requestSHA256 {
		return nil, ErrDirectImportReceiptConflict
	}
	if receipt.State == DirectImportReceiptComplete {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit direct import receipt replay: %w", err)
		}
		return &DirectImportReceiptClaim{Receipt: receipt, Replay: true}, nil
	}
	if inserted == 1 {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit direct import receipt reservation: %w", err)
		}
		return &DirectImportReceiptClaim{Receipt: receipt, Created: true}, nil
	}
	if receipt.LeaseExpiresAt != nil && receipt.LeaseExpiresAt.After(now) {
		return nil, ErrDirectImportReceiptBusy
	}

	updateResult, err := tx.Exec(`UPDATE direct_import_receipts
		SET owner_token = ?, lease_expires_at = ?, updated_at = ?
		WHERE id = ? AND state = ? AND
			(lease_expires_at IS NULL OR lease_expires_at <= ?)`,
		ownerToken, expiresAt, now, receipt.ID, DirectImportReceiptPending, now)
	if err != nil {
		return nil, fmt.Errorf("claim stale direct import receipt: %w", err)
	}
	updated, err := updateResult.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("inspect stale direct import receipt claim: %w", err)
	}
	if updated != 1 {
		return nil, ErrDirectImportReceiptBusy
	}
	receipt.OwnerToken = ownerToken
	receipt.LeaseExpiresAt = &expiresAt
	receipt.UpdatedAt = now
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit stale direct import receipt claim: %w", err)
	}
	return &DirectImportReceiptClaim{Receipt: receipt}, nil
}

func (s *SQLiteStore) RenewDirectImportReceipt(
	id, ownerToken string,
	now time.Time,
	lease time.Duration,
) (bool, error) {
	id = strings.TrimSpace(id)
	ownerToken = strings.TrimSpace(ownerToken)
	if id == "" || ownerToken == "" || lease <= 0 {
		return false, errors.New("direct import receipt renewal requires id, owner token, and positive lease")
	}
	now = now.UTC()
	result, err := s.db.Exec(`UPDATE direct_import_receipts
		SET lease_expires_at = ?, updated_at = ?
		WHERE id = ? AND state = ? AND owner_token = ?`,
		now.Add(lease), now, id, DirectImportReceiptPending, ownerToken)
	if err != nil {
		return false, fmt.Errorf("renew direct import receipt: %w", err)
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func (s *SQLiteStore) CompleteDirectImportReceipt(
	id, ownerToken string,
	scriptIDs []string,
	scenarioID string,
	now time.Time,
) (bool, error) {
	id = strings.TrimSpace(id)
	ownerToken = strings.TrimSpace(ownerToken)
	if id == "" || ownerToken == "" || len(scriptIDs) == 0 {
		return false, errors.New("direct import receipt completion requires id, owner token, and script ids")
	}
	copiedIDs := make([]string, len(scriptIDs))
	seen := make(map[string]struct{}, len(scriptIDs))
	for index, scriptID := range scriptIDs {
		scriptID = strings.TrimSpace(scriptID)
		if scriptID == "" {
			return false, errors.New("direct import receipt contains an empty script id")
		}
		if _, duplicate := seen[scriptID]; duplicate {
			return false, errors.New("direct import receipt contains duplicate script ids")
		}
		seen[scriptID] = struct{}{}
		copiedIDs[index] = scriptID
	}
	idsJSON, err := json.Marshal(copiedIDs)
	if err != nil {
		return false, fmt.Errorf("marshal direct import script ids: %w", err)
	}
	now = now.UTC()
	var scenario any
	if trimmed := strings.TrimSpace(scenarioID); trimmed != "" {
		scenario = trimmed
	}
	result, err := s.db.Exec(`UPDATE direct_import_receipts
		SET state = ?, owner_token = '', lease_expires_at = NULL,
			script_ids_json = ?, scenario_id = ?, updated_at = ?
		WHERE id = ? AND state = ? AND owner_token = ?`,
		DirectImportReceiptComplete, string(idsJSON), scenario, now,
		id, DirectImportReceiptPending, ownerToken)
	if err != nil {
		return false, fmt.Errorf("complete direct import receipt: %w", err)
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

// ReleaseDirectImportReceipt relinquishes a pending receipt without deleting
// its operation identity. A later retry can immediately reclaim the same row
// and therefore derives the same deterministic asset IDs.
func (s *SQLiteStore) ReleaseDirectImportReceipt(
	id, ownerToken string,
	now time.Time,
) (bool, error) {
	id = strings.TrimSpace(id)
	ownerToken = strings.TrimSpace(ownerToken)
	if id == "" || ownerToken == "" {
		return false, errors.New("direct import receipt release requires id and owner token")
	}
	now = now.UTC()
	result, err := s.db.Exec(`UPDATE direct_import_receipts
		SET owner_token = '', lease_expires_at = NULL, updated_at = ?
		WHERE id = ? AND state = ? AND owner_token = ?`,
		now, id, DirectImportReceiptPending, ownerToken)
	if err != nil {
		return false, fmt.Errorf("release direct import receipt: %w", err)
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func (s *SQLiteStore) AbandonDirectImportReceipt(id, ownerToken string) (bool, error) {
	id = strings.TrimSpace(id)
	ownerToken = strings.TrimSpace(ownerToken)
	if id == "" || ownerToken == "" {
		return false, errors.New("direct import receipt abandonment requires id and owner token")
	}
	result, err := s.db.Exec(`DELETE FROM direct_import_receipts
		WHERE id = ? AND state = ? AND owner_token = ?`,
		id, DirectImportReceiptPending, ownerToken)
	if err != nil {
		return false, fmt.Errorf("abandon direct import receipt: %w", err)
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func scanDirectImportReceipt(row scanner) (*DirectImportReceipt, error) {
	var receipt DirectImportReceipt
	var leaseExpiresAt sql.NullTime
	var scriptIDsJSON string
	var scenarioID sql.NullString
	if err := row.Scan(
		&receipt.ID, &receipt.ProjectID, &receipt.SourceProfile,
		&receipt.OperationKeyHash, &receipt.RequestSHA256, &receipt.State,
		&receipt.OwnerToken, &leaseExpiresAt, &scriptIDsJSON, &scenarioID,
		&receipt.CreatedAt, &receipt.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if leaseExpiresAt.Valid {
		value := leaseExpiresAt.Time
		receipt.LeaseExpiresAt = &value
	}
	if err := json.Unmarshal([]byte(scriptIDsJSON), &receipt.ScriptIDs); err != nil {
		return nil, fmt.Errorf("decode direct import receipt script ids: %w", err)
	}
	receipt.ScenarioID = scenarioID.String
	return &receipt, nil
}
