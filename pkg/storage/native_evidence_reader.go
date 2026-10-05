package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// NativeIntervalEvidenceReader streams a run's retained native interval
// evidence, oldest first, without loading the rest of each history row.
type NativeIntervalEvidenceReader interface {
	ForEachNativeIntervalEvidence(ctx context.Context, testID string, fn func(timestamp time.Time, evidence *NativeIntervalEvidence) error) error
}

// nativeEvidencePageSize keeps at most this many decoded rows in memory; one
// row can hold several hundred t-digests.
const nativeEvidencePageSize = 32

// ForEachNativeIntervalEvidence pages through the run's rows that carry native
// evidence. It is meant for completed runs, whose rows no longer change.
func (s *SQLiteStore) ForEachNativeIntervalEvidence(ctx context.Context, testID string, fn func(time.Time, *NativeIntervalEvidence) error) error {
	type row struct {
		timestamp time.Time
		evidence  *NativeIntervalEvidence
	}
	for offset := 0; ; offset += nativeEvidencePageSize {
		rows, err := s.db.QueryContext(ctx, `SELECT timestamp, native_evidence_json FROM test_interval_summaries
			WHERE test_id = ? AND native_evidence_json IS NOT NULL AND native_evidence_json <> ''
			ORDER BY timestamp ASC LIMIT ? OFFSET ?`, testID, nativeEvidencePageSize, offset)
		if err != nil {
			return err
		}
		page := make([]row, 0, nativeEvidencePageSize)
		for rows.Next() {
			var timestamp time.Time
			var encoded sql.NullString
			if err := rows.Scan(&timestamp, &encoded); err != nil {
				rows.Close()
				return err
			}
			var evidence NativeIntervalEvidence
			if err := json.Unmarshal([]byte(encoded.String), &evidence); err != nil {
				rows.Close()
				return fmt.Errorf("decode native interval evidence at %s: %w", timestamp.UTC().Format(time.RFC3339Nano), err)
			}
			page = append(page, row{timestamp, &evidence})
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		// The page is fully read before callbacks run, so a callback never holds
		// a database cursor open.
		for _, item := range page {
			if err := fn(item.timestamp, item.evidence); err != nil {
				return err
			}
		}
		if len(page) < nativeEvidencePageSize {
			return nil
		}
	}
}
