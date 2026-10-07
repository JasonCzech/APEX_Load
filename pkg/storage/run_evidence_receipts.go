package storage

import (
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Run evidence receipts (D5). The controller records a receipt once it
// durably holds an optional evidence artifact a generator produced (a native
// HAR), publishes the run's receipts when the archive publication commits,
// and only then acknowledges them so the generator deletes its copies.
const (
	EvidenceReceiptAccepted   = "accepted"
	EvidenceReceiptPublished  = "published"
	EvidenceReceiptTombstoned = "tombstoned"

	EvidenceKindHARNative    = "har_native"
	EvidenceKindHARSynthetic = "har_synthetic"
	EvidenceKindInteraction  = "interaction_evidence"
)

// RunEvidenceReceipt identifies one evidence artifact of a run by its source
// (the generator's archive source identity and the artifact's own source
// generation) and records what the controller holds.
type RunEvidenceReceipt struct {
	TestID              string    `json:"test_id"`
	ExecutionGeneration uint64    `json:"execution_generation"`
	SourceIdentity      string    `json:"source_identity"`
	Target              string    `json:"target"`
	SourceGeneration    string    `json:"source_generation"`
	Sequence            int64     `json:"sequence"`
	Kind                string    `json:"kind"`
	Name                string    `json:"name"`
	SHA256              string    `json:"sha256"`
	Bytes               int64     `json:"bytes"`
	State               string    `json:"state"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// RunEvidenceReceiptStore is the receipt contract the archive pipeline uses.
type RunEvidenceReceiptStore interface {
	AcceptRunEvidenceReceipt(receipt RunEvidenceReceipt) (string, error)
	PublishRunEvidenceReceipts(testID string) (int64, error)
	ListRunEvidenceReceipts(testID string) ([]RunEvidenceReceipt, error)
	ResetAcceptedRunEvidenceReceipts(testID string, executionGeneration uint64) (int64, error)
}

var _ RunEvidenceReceiptStore = (*SQLiteStore)(nil)

// ErrEvidenceReceiptConflict reports a source artifact identity already
// received with different content.
var ErrEvidenceReceiptConflict = errors.New("evidence receipt conflicts with a recorded artifact")

func validateRunEvidenceReceipt(r RunEvidenceReceipt) error {
	sum, err := hex.DecodeString(r.SHA256)
	switch {
	case strings.TrimSpace(r.TestID) == "" || len(r.TestID) > 256:
		return errors.New("evidence receipt needs a test id")
	case strings.TrimSpace(r.SourceIdentity) == "" || len(r.SourceIdentity) > 512 || len(r.Target) > 512:
		return errors.New("evidence receipt needs a bounded source identity")
	case strings.TrimSpace(r.SourceGeneration) == "" || len(r.SourceGeneration) > 128 || strings.TrimSpace(r.Name) == "" || len(r.Name) > 256:
		return errors.New("evidence receipt needs a bounded source generation and name")
	case r.Kind != EvidenceKindHARNative && r.Kind != EvidenceKindHARSynthetic && r.Kind != EvidenceKindInteraction:
		return fmt.Errorf("unsupported evidence kind %q", r.Kind)
	case err != nil || len(sum) != 32 || strings.ToLower(r.SHA256) != r.SHA256:
		return errors.New("evidence receipt needs a lowercase SHA-256")
	case r.Bytes < 0 || r.Sequence < 0 || r.ExecutionGeneration > 1<<62:
		return errors.New("evidence receipt has invalid counts")
	}
	return nil
}

// AcceptRunEvidenceReceipt records that the controller durably holds an
// artifact and returns the receipt's state. Repeating a receipt with the
// same content is idempotent; different content for the same source
// artifact is ErrEvidenceReceiptConflict. An artifact received after the
// run's archive was published, after the run was deleted, or under an
// execution generation older than one already recorded is tombstoned: the
// caller deletes its copy.
func (s *SQLiteStore) AcceptRunEvidenceReceipt(r RunEvidenceReceipt) (string, error) {
	if err := validateRunEvidenceReceipt(r); err != nil {
		return "", err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var existing RunEvidenceReceipt
	var generation int64
	err = tx.QueryRow(`SELECT kind, sha256, bytes, state, execution_generation FROM run_evidence_receipts
		WHERE test_id = ? AND source_identity = ? AND source_generation = ?`, r.TestID, r.SourceIdentity, r.SourceGeneration).
		Scan(&existing.Kind, &existing.SHA256, &existing.Bytes, &existing.State, &generation)
	found := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if found && (existing.Kind != r.Kind || existing.SHA256 != r.SHA256 || existing.Bytes != r.Bytes) {
		return "", ErrEvidenceReceiptConflict
	}
	state := EvidenceReceiptAccepted
	var newest sql.NullInt64
	if err := tx.QueryRow(`SELECT MAX(execution_generation) FROM run_evidence_receipts WHERE test_id = ?`, r.TestID).Scan(&newest); err != nil {
		return "", err
	}
	var archived, tests int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM run_archives WHERE test_id = ?`, r.TestID).Scan(&archived); err != nil {
		return "", err
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM tests WHERE id = ?`, r.TestID).Scan(&tests); err != nil {
		return "", err
	}
	switch {
	case found && existing.State != EvidenceReceiptAccepted:
		// A published or tombstoned receipt is final.
		return existing.State, nil
	case archived > 0 || tests == 0:
		state = EvidenceReceiptTombstoned
	case r.ExecutionGeneration > 0 && newest.Valid && uint64(newest.Int64) > r.ExecutionGeneration:
		// A staging attempt of an older execution generation (a deposed
		// controller still merging) is refused and changes nothing a newer
		// generation recorded.
		return EvidenceReceiptTombstoned, nil
	case r.ExecutionGeneration == 0 && newest.Valid && newest.Int64 > 0:
		// A collection retry runs without the execution fence (generation
		// 0): it inherits the newest generation rather than tombstoning the
		// evidence the live attempt left for it.
		r.ExecutionGeneration = uint64(newest.Int64)
	}
	now := time.Now().UTC()
	if found {
		if _, err := tx.Exec(`UPDATE run_evidence_receipts SET state = ?, execution_generation = ?, target = ?, updated_at = ?
			WHERE test_id = ? AND source_identity = ? AND source_generation = ?`,
			state, int64(max(r.ExecutionGeneration, uint64(generation))), r.Target, now, r.TestID, r.SourceIdentity, r.SourceGeneration); err != nil {
			return "", err
		}
	} else if _, err := tx.Exec(`INSERT INTO run_evidence_receipts (test_id, execution_generation, source_identity, target, source_generation, sequence, kind, name, sha256, bytes, state, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.TestID, int64(r.ExecutionGeneration), r.SourceIdentity, r.Target, r.SourceGeneration, r.Sequence, r.Kind, r.Name, r.SHA256, r.Bytes, state, now, now); err != nil {
		return "", err
	}
	return state, tx.Commit()
}

// PublishRunEvidenceReceipts marks a run's accepted receipts published once
// its archive publication committed, and returns how many it published.
func (s *SQLiteStore) PublishRunEvidenceReceipts(testID string) (int64, error) {
	if strings.TrimSpace(testID) == "" {
		return 0, errors.New("test id is required")
	}
	result, err := s.db.Exec(`UPDATE run_evidence_receipts SET state = ?, updated_at = ? WHERE test_id = ? AND state = ?`,
		EvidenceReceiptPublished, time.Now().UTC(), testID, EvidenceReceiptAccepted)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// ResetAcceptedRunEvidenceReceipts forgets a run's accepted receipts at the
// start of a staging attempt: an accepted receipt is only a staging
// attempt's claim (never acknowledged to a generator), so a failed attempt's
// claims must not be published with an archive that does not hold them. An
// attempt of an execution generation forgets only claims of that generation
// or older, never a newer controller's; a collection retry (generation 0)
// forgets them all. Published and tombstoned receipts stay final.
func (s *SQLiteStore) ResetAcceptedRunEvidenceReceipts(testID string, executionGeneration uint64) (int64, error) {
	if strings.TrimSpace(testID) == "" {
		return 0, errors.New("test id is required")
	}
	query, args := `DELETE FROM run_evidence_receipts WHERE test_id = ? AND state = ?`, []any{testID, EvidenceReceiptAccepted}
	if executionGeneration > 0 {
		query += ` AND execution_generation <= ?`
		args = append(args, int64(executionGeneration))
	}
	result, err := s.db.Exec(query, args...)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// ListRunEvidenceReceipts returns a run's receipts in source and sequence
// order.
func (s *SQLiteStore) ListRunEvidenceReceipts(testID string) ([]RunEvidenceReceipt, error) {
	rows, err := s.db.Query(`SELECT test_id, execution_generation, source_identity, target, source_generation, sequence, kind, name, sha256, bytes, state, created_at, updated_at
		FROM run_evidence_receipts WHERE test_id = ? ORDER BY source_identity, sequence, source_generation`, testID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RunEvidenceReceipt
	for rows.Next() {
		var r RunEvidenceReceipt
		var generation int64
		if err := rows.Scan(&r.TestID, &generation, &r.SourceIdentity, &r.Target, &r.SourceGeneration, &r.Sequence, &r.Kind, &r.Name, &r.SHA256, &r.Bytes, &r.State, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		r.ExecutionGeneration = uint64(generation)
		out = append(out, r)
	}
	return out, rows.Err()
}
