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
	now := time.Now().UTC()
	if note.CreatedAt.IsZero() {
		note.CreatedAt = now
	}
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

func (s *SQLiteStore) ListTestNotes(testID string) ([]*TestNote, error) {
	rows, err := s.db.Query(`SELECT id, test_id, COALESCE(body, ''), created_at, updated_at, COALESCE(created_by, ''), COALESCE(updated_by, '')
		FROM test_notes WHERE test_id = ? ORDER BY updated_at DESC`, strings.TrimSpace(testID))
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

func (s *SQLiteStore) DeleteTestNotesByTestID(testID string) error {
	_, err := s.db.Exec(`DELETE FROM test_notes WHERE test_id = ?`, strings.TrimSpace(testID))
	return err
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
