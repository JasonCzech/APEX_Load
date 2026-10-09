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

// NativeIntervalEvidenceRetention reports whether a run's persisted interval
// rows reached the store's retention cap, so its oldest rows may have been
// trimmed.
type NativeIntervalEvidenceRetention interface {
	NativeIntervalEvidenceTrimmed(ctx context.Context, testID string) (bool, error)
}

// NativeIntervalEvidenceTrimmed reports whether the run has as many persisted
// interval rows as the store keeps (maxPersistedTestHistoryPoints): the
// oldest may have been trimmed. Exactly that many without a trim also counts.
func (s *SQLiteStore) NativeIntervalEvidenceTrimmed(ctx context.Context, testID string) (bool, error) {
	var rows int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM test_interval_summaries WHERE test_id = ?`, testID).Scan(&rows); err != nil {
		return false, err
	}
	return rows >= maxPersistedTestHistoryPoints, nil
}

// nativeEvidencePageSize keeps at most this many decoded rows in memory; one
// row can hold several hundred t-digests.
const nativeEvidencePageSize = 32

// nativeEvidencePageBytes also ends a page once its encoded rows reach this
// size: a distributed run's row carries the joint evidence of every source
// batch accepted since the previous point, so rows vary widely in size. A
// variable so tests can page by bytes.
var nativeEvidencePageBytes = 32 << 20

// ForEachNativeIntervalEvidence pages through the run's rows that carry native
// evidence. It is meant for completed runs, whose rows no longer change. Pages
// resume from the last timestamp read, so each row is read once; rows that
// share that timestamp are skipped by count, which keeps duplicates exact.
func (s *SQLiteStore) ForEachNativeIntervalEvidence(ctx context.Context, testID string, fn func(time.Time, *NativeIntervalEvidence) error) error {
	type row struct {
		timestamp time.Time
		evidence  *NativeIntervalEvidence
	}
	var after time.Time
	resumed, ties := false, 0
	for {
		query := `SELECT timestamp, native_evidence_json FROM test_interval_summaries
			WHERE test_id = ? AND native_evidence_json IS NOT NULL AND native_evidence_json <> ''`
		args := []any{testID}
		if resumed {
			query += ` AND timestamp >= ?`
			args = append(args, after)
		}
		query += ` ORDER BY timestamp ASC LIMIT ? OFFSET ?`
		args = append(args, nativeEvidencePageSize, ties)
		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		page := make([]row, 0, nativeEvidencePageSize)
		pageBytes, more := 0, false
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
			if pageBytes += len(encoded.String); pageBytes >= nativeEvidencePageBytes {
				more = true // the page ends early; the next one resumes after it
				break
			}
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
		if !more && len(page) < nativeEvidencePageSize {
			return nil
		}
		last := page[len(page)-1].timestamp
		atLast := 0
		for _, item := range page {
			if item.timestamp.Equal(last) {
				atLast++
			}
		}
		if resumed && last.Equal(after) {
			ties += atLast
		} else {
			after, ties, resumed = last, atLast, true
		}
	}
}
