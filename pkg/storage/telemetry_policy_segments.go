package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// TelemetryPolicySegment is one append-only telemetry budget policy change of
// a running test (TEL-B): from EffectiveAt, the run's live telemetry is
// classified by Receipt. Sequence 1 is the admission receipt, which the test's
// initial provenance holds; stored segments start at 2 and are never updated.
type TelemetryPolicySegment struct {
	TestID      string          `json:"test_id"`
	Sequence    int64           `json:"sequence"`
	EffectiveAt time.Time       `json:"effective_at"`
	Receipt     json.RawMessage `json:"receipt"`
	// Source is "system_settings" or "run_selection".
	Source   string `json:"source"`
	PolicyID string `json:"policy_id,omitempty"`
	// RequestID is the requester's identity for the change: a retry with the
	// same ID returns the segment it created instead of appending another.
	RequestID              string    `json:"request_id"`
	RequestedBySubject     string    `json:"requested_by_subject,omitempty"`
	RequestedByDisplayName string    `json:"requested_by_display_name,omitempty"`
	CreatedAt              time.Time `json:"created_at"`
}

// TelemetryPolicySegmentStore keeps a test's telemetry policy segments.
type TelemetryPolicySegmentStore interface {
	AppendTelemetryPolicySegmentIfLeaseHeld(segment TelemetryPolicySegment, expectedLatest int64, leaseName, leaseOwnerID string) (TelemetryPolicySegment, bool, error)
	ListTelemetryPolicySegments(testID string) ([]TelemetryPolicySegment, error)
}

var (
	// ErrTelemetryPolicySegmentConflict refuses an append whose expected
	// latest sequence is not the test's latest.
	ErrTelemetryPolicySegmentConflict = errors.New("the run's telemetry policy segments changed")
	// ErrTelemetryPolicySegmentLease refuses an append by a controller that
	// no longer holds the execution lease.
	ErrTelemetryPolicySegmentLease = errors.New("the execution lease is not held")
)

// AppendTelemetryPolicySegmentIfLeaseHeld appends segment as the test's next
// one (expectedLatest + 1; the admission receipt is sequence 1), in one
// transaction that also proves the execution lease is held when a lease is
// named (an HA controller); with no lease named it is not fenced. A request
// ID the test already used returns that segment with replayed true, whatever
// else the retry names: the caller compares it.
func (s *SQLiteStore) AppendTelemetryPolicySegmentIfLeaseHeld(segment TelemetryPolicySegment, expectedLatest int64, leaseName, leaseOwnerID string) (TelemetryPolicySegment, bool, error) {
	segment.TestID = strings.TrimSpace(segment.TestID)
	segment.RequestID = strings.TrimSpace(segment.RequestID)
	leaseName, leaseOwnerID = strings.TrimSpace(leaseName), strings.TrimSpace(leaseOwnerID)
	if segment.TestID == "" || segment.RequestID == "" || len(segment.Receipt) == 0 || segment.EffectiveAt.IsZero() {
		return TelemetryPolicySegment{}, false, errors.New("test ID, request ID, receipt and effective time are required")
	}
	if (leaseName == "") != (leaseOwnerID == "") {
		return TelemetryPolicySegment{}, false, errors.New("a lease needs both its name and owner token")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return TelemetryPolicySegment{}, false, err
	}
	defer tx.Rollback()
	if leaseName != "" {
		held, err := schedulerLeaseHeldInTransaction(tx, s.db.dialect, leaseName, leaseOwnerID)
		if err != nil {
			return TelemetryPolicySegment{}, false, err
		}
		if !held {
			return TelemetryPolicySegment{}, false, ErrTelemetryPolicySegmentLease
		}
	}
	existing, err := scanTelemetryPolicySegment(tx.QueryRow(telemetryPolicySegmentSelect+` WHERE test_id = ? AND request_id = ?`, segment.TestID, segment.RequestID))
	if err == nil {
		return existing, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return TelemetryPolicySegment{}, false, err
	}
	var exists int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM tests WHERE id = ?`, segment.TestID).Scan(&exists); err != nil {
		return TelemetryPolicySegment{}, false, err
	}
	if exists == 0 {
		return TelemetryPolicySegment{}, false, sql.ErrNoRows
	}
	var latest sql.NullInt64
	if err := tx.QueryRow(`SELECT MAX(sequence) FROM test_telemetry_policy_segments WHERE test_id = ?`, segment.TestID).Scan(&latest); err != nil {
		return TelemetryPolicySegment{}, false, err
	}
	current := int64(1)
	if latest.Valid && latest.Int64 > current {
		current = latest.Int64
	}
	if expectedLatest != current {
		return TelemetryPolicySegment{}, false, ErrTelemetryPolicySegmentConflict
	}
	segment.Sequence = current + 1
	// Microsecond precision (PostgreSQL's), so what is returned is what is
	// read back on every dialect.
	segment.EffectiveAt = segment.EffectiveAt.UTC().Truncate(time.Microsecond)
	if segment.CreatedAt.IsZero() {
		segment.CreatedAt = time.Now()
	}
	segment.CreatedAt = segment.CreatedAt.UTC().Truncate(time.Microsecond)
	if _, err := tx.Exec(`INSERT INTO test_telemetry_policy_segments (test_id, sequence, effective_at, receipt_json, source, policy_id, request_id, requested_by_subject, requested_by_display_name, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		segment.TestID, segment.Sequence, segment.EffectiveAt, string(segment.Receipt), segment.Source, nullableString(strings.TrimSpace(segment.PolicyID)),
		segment.RequestID, nullableString(segment.RequestedBySubject), nullableString(segment.RequestedByDisplayName), segment.CreatedAt.UTC()); err != nil {
		return TelemetryPolicySegment{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return TelemetryPolicySegment{}, false, err
	}
	return segment, false, nil
}

// ListTelemetryPolicySegments returns the test's stored segments in sequence
// order (not the admission receipt, sequence 1).
func (s *SQLiteStore) ListTelemetryPolicySegments(testID string) ([]TelemetryPolicySegment, error) {
	rows, err := s.db.Query(telemetryPolicySegmentSelect+` WHERE test_id = ? ORDER BY sequence`, strings.TrimSpace(testID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	segments := []TelemetryPolicySegment{}
	for rows.Next() {
		segment, err := scanTelemetryPolicySegment(rows)
		if err != nil {
			return nil, err
		}
		segments = append(segments, segment)
	}
	return segments, rows.Err()
}

const telemetryPolicySegmentSelect = `SELECT test_id, sequence, effective_at, receipt_json, source, COALESCE(policy_id, ''), request_id, COALESCE(requested_by_subject, ''), COALESCE(requested_by_display_name, ''), created_at FROM test_telemetry_policy_segments`

func scanTelemetryPolicySegment(scanner rowScanner) (TelemetryPolicySegment, error) {
	var segment TelemetryPolicySegment
	var receipt string
	if err := scanner.Scan(&segment.TestID, &segment.Sequence, &segment.EffectiveAt, &receipt, &segment.Source, &segment.PolicyID, &segment.RequestID, &segment.RequestedBySubject, &segment.RequestedByDisplayName, &segment.CreatedAt); err != nil {
		return TelemetryPolicySegment{}, err
	}
	segment.Receipt = json.RawMessage(receipt)
	segment.EffectiveAt = segment.EffectiveAt.UTC()
	segment.CreatedAt = segment.CreatedAt.UTC()
	return segment, nil
}
