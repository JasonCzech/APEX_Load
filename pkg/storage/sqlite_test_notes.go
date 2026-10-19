package storage

// SQLite store - test notes. Split from sqlite.go (same package, behavior-preserving).

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

func (s *SQLiteStore) CreateTestNote(note *TestNote) error {
	if note == nil {
		return errors.New("test note is nil")
	}
	// The row is new, so the caller's stamps take the stored precision: a
	// later edit sends this updated_at back as the version it expects.
	note.CreatedAt = newRowTimestamp(note.CreatedAt)
	note.UpdatedAt = note.UpdatedAt.Truncate(time.Microsecond)
	if note.UpdatedAt.IsZero() {
		note.UpdatedAt = note.CreatedAt
	}
	_, err := s.db.Exec(`INSERT INTO test_notes (id, test_id, body, created_at, updated_at, created_by, updated_by)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		strings.TrimSpace(note.ID),
		strings.TrimSpace(note.TestID),
		note.Body,
		note.CreatedAt,
		note.UpdatedAt,
		strings.TrimSpace(note.CreatedBy),
		strings.TrimSpace(note.UpdatedBy),
	)
	return err
}

func (s *SQLiteStore) CreateTestNoteAndArchiveRefresh(note *TestNote) error {
	if note == nil {
		return errors.New("test note is nil")
	}
	// The row is new, so the caller's stamps take the stored precision: a
	// later edit sends this updated_at back as the version it expects.
	note.CreatedAt = newRowTimestamp(note.CreatedAt)
	note.UpdatedAt = note.UpdatedAt.Truncate(time.Microsecond)
	if note.UpdatedAt.IsZero() {
		note.UpdatedAt = note.CreatedAt
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := bumpArchiveRefreshJobTx(tx, s.db.dialect, note.TestID); err != nil && !errors.Is(err, ErrArchiveCleanupPending) {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO test_notes (id, test_id, body, created_at, updated_at, created_by, updated_by)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, strings.TrimSpace(note.ID), strings.TrimSpace(note.TestID), note.Body,
		note.CreatedAt, note.UpdatedAt, strings.TrimSpace(note.CreatedBy), strings.TrimSpace(note.UpdatedBy)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) ListTestNotes(testID string) ([]*TestNote, error) {
	return s.listTestNotes(testID, 0, 0)
}

func (s *SQLiteStore) ListTestNotesPage(testID string, limit, offset int) ([]*TestNote, error) {
	if limit <= 0 {
		return []*TestNote{}, nil
	}
	if offset < 0 {
		offset = 0
	}
	return s.listTestNotes(testID, limit, offset)
}

func (s *SQLiteStore) listTestNotes(testID string, limit, offset int) ([]*TestNote, error) {
	query := `SELECT id, test_id, COALESCE(body, ''), created_at, updated_at, COALESCE(created_by, ''), COALESCE(updated_by, '')
		FROM test_notes WHERE test_id = ? ORDER BY updated_at DESC`
	args := []any{strings.TrimSpace(testID)}
	if limit > 0 {
		query += ` LIMIT ? OFFSET ?`
		args = append(args, limit, offset)
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	notes := []*TestNote{}
	for rows.Next() {
		note, err := scanTestNote(rows)
		if err != nil {
			return nil, err
		}
		notes = append(notes, note)
	}
	return notes, rows.Err()
}

func (s *SQLiteStore) GetTestNote(id string) (*TestNote, error) {
	return scanTestNote(s.db.QueryRow(`SELECT id, test_id, COALESCE(body, ''), created_at, updated_at, COALESCE(created_by, ''), COALESCE(updated_by, '')
		FROM test_notes WHERE id = ?`, strings.TrimSpace(id)))
}

func (s *SQLiteStore) UpdateTestNote(note *TestNote) error {
	if note == nil {
		return errors.New("test note is nil")
	}
	note.UpdatedAt = time.Now().UTC()
	result, err := s.db.Exec(`UPDATE test_notes SET body = ?, updated_at = ?, updated_by = ? WHERE id = ?`,
		note.Body,
		note.UpdatedAt,
		strings.TrimSpace(note.UpdatedBy),
		strings.TrimSpace(note.ID),
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *SQLiteStore) UpdateTestNoteAndArchiveRefresh(note *TestNote) error {
	if note == nil {
		return errors.New("test note is nil")
	}
	note.UpdatedAt = time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := bumpArchiveRefreshJobTx(tx, s.db.dialect, note.TestID); err != nil && !errors.Is(err, ErrArchiveCleanupPending) {
		return err
	}
	result, err := tx.Exec(`UPDATE test_notes SET body = ?, updated_at = ?, updated_by = ? WHERE id = ? AND test_id = ?`,
		note.Body, note.UpdatedAt, strings.TrimSpace(note.UpdatedBy), strings.TrimSpace(note.ID), strings.TrimSpace(note.TestID))
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

// UpdateTestNoteAndArchiveRefreshIfUnchanged atomically updates a note and its
// archive refresh marker only when the caller's note snapshot is still current.
func (s *SQLiteStore) UpdateTestNoteAndArchiveRefreshIfUnchanged(note *TestNote, expectedUpdatedAt time.Time) (bool, error) {
	if note == nil {
		return false, errors.New("test note is nil")
	}
	if strings.TrimSpace(note.ID) == "" || strings.TrimSpace(note.TestID) == "" {
		return false, errors.New("test note id and test id are required")
	}
	if expectedUpdatedAt.IsZero() {
		return false, errors.New("expected test note updated_at is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var currentTestID string
	var currentUpdatedAt time.Time
	var currentUpdatedRaw string
	if err := tx.QueryRow(`SELECT test_id, updated_at, CAST(updated_at AS TEXT) FROM test_notes WHERE id = ?`, strings.TrimSpace(note.ID)).Scan(&currentTestID, &currentUpdatedAt, &currentUpdatedRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if strings.TrimSpace(currentTestID) != strings.TrimSpace(note.TestID) || !currentUpdatedAt.Equal(expectedUpdatedAt) {
		return false, nil
	}
	if _, err := bumpArchiveRefreshJobTx(tx, s.db.dialect, note.TestID); err != nil && !errors.Is(err, ErrArchiveCleanupPending) {
		return false, err
	}
	note.UpdatedAt, err = preciseDatabaseTime(tx, s.db.dialect)
	if err != nil {
		return false, err
	}
	if !note.UpdatedAt.After(currentUpdatedAt) {
		note.UpdatedAt = currentUpdatedAt.Add(time.Microsecond)
	}
	result, err := tx.Exec(`UPDATE test_notes SET body = ?, updated_at = ?, updated_by = ?
		WHERE id = ? AND test_id = ? AND CAST(updated_at AS TEXT) = ?`, note.Body, note.UpdatedAt,
		strings.TrimSpace(note.UpdatedBy), strings.TrimSpace(note.ID), strings.TrimSpace(note.TestID), currentUpdatedRaw)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *SQLiteStore) DeleteTestNote(id string) error {
	result, err := s.db.Exec(`DELETE FROM test_notes WHERE id = ?`, strings.TrimSpace(id))
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *SQLiteStore) DeleteTestNoteAndArchiveRefresh(id string, testID string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := bumpArchiveRefreshJobTx(tx, s.db.dialect, testID); err != nil && !errors.Is(err, ErrArchiveCleanupPending) {
		return err
	}
	result, err := tx.Exec(`DELETE FROM test_notes WHERE id = ? AND test_id = ?`, strings.TrimSpace(id), strings.TrimSpace(testID))
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

func scanTestNote(row rowScanner) (*TestNote, error) {
	var note TestNote
	if err := row.Scan(
		&note.ID,
		&note.TestID,
		&note.Body,
		&note.CreatedAt,
		&note.UpdatedAt,
		&note.CreatedBy,
		&note.UpdatedBy,
	); err != nil {
		return nil, err
	}
	return &note, nil
}
