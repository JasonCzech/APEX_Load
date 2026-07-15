package storage

// SQLite store — settings. Split from sqlite.go (same package, behavior-preserving).

import (
	"database/sql"
	"fmt"
	"strconv"
	"time"
)

func (s *SQLiteStore) SaveSetting(key string, value string) error {
	_, err := s.db.Exec(`INSERT INTO app_settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`, key, value, time.Now())
	return err
}

func (s *SQLiteStore) GetSetting(key string) (string, error) {
	var value string
	err := s.db.QueryRow(`SELECT value FROM app_settings WHERE key = ?`, key).Scan(&value)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	return value, nil
}

// NextSettingSequence atomically increments an integer setting and returns the
// committed value. The single upsert statement is safe across controller
// replicas for both SQLite and PostgreSQL.
func (s *SQLiteStore) NextSettingSequence(key string) (int, error) {
	var raw string
	err := s.db.QueryRow(`INSERT INTO app_settings (key, value, updated_at) VALUES (?, '1', ?)
		ON CONFLICT(key) DO UPDATE SET value=CAST(app_settings.value AS INTEGER)+1, updated_at=excluded.updated_at
		RETURNING value`, key, time.Now()).Scan(&raw)
	if err != nil {
		return 0, err
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("invalid setting sequence %q", raw)
	}
	return value, nil
}

// CompareAndSwapSetting replaces a setting only when its current value matches
// expected. An empty expected value creates the row only when it is absent.
func (s *SQLiteStore) CompareAndSwapSetting(key, expected, value string) (bool, error) {
	if expected == "" {
		result, err := s.db.Exec(`INSERT INTO app_settings (key, value, updated_at) VALUES (?, ?, ?)
			ON CONFLICT(key) DO NOTHING`, key, value, time.Now())
		if err != nil {
			return false, err
		}
		rows, err := result.RowsAffected()
		return rows == 1, err
	}
	result, err := s.db.Exec(`UPDATE app_settings SET value = ?, updated_at = ? WHERE key = ? AND value = ?`, value, time.Now(), key, expected)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

// CompareAndSwapSettingWithCapacityFence performs a setting CAS while holding
// the same durable fence used by capacity-reservation approval. The validator
// sees the current value and a transactionally consistent snapshot of approved
// timeslots, preventing settings and approval writes from each validating
// against the other's pre-commit state.
func (s *SQLiteStore) CompareAndSwapSettingWithCapacityFence(
	key, expected, value string,
	updatedAt time.Time,
	validate func(current string, approved []*CapacityReservation) error,
) (bool, error) {
	if validate == nil {
		return false, fmt.Errorf("capacity settings validator is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if err := acquireCapacitySettingsFence(tx, updatedAt); err != nil {
		return false, err
	}
	settingQuery := `SELECT value FROM app_settings WHERE key = ?`
	if s.db.dialect == storeDialectPostgres {
		settingQuery += ` FOR UPDATE`
	}
	var current string
	err = tx.QueryRow(settingQuery, key).Scan(&current)
	exists := err == nil
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}
	if expected == "" {
		if exists {
			return false, nil
		}
	} else if !exists || current != expected {
		return false, nil
	}

	rows, err := tx.Query(capacityReservationSelect + ` WHERE approval_state = 'approved'`)
	if err != nil {
		return false, err
	}
	approved := make([]*CapacityReservation, 0)
	for rows.Next() {
		reservation, scanErr := scanCapacityReservation(rows)
		if scanErr != nil {
			rows.Close()
			return false, scanErr
		}
		approved = append(approved, reservation)
	}
	if err := rows.Close(); err != nil {
		return false, err
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	if err := validate(current, approved); err != nil {
		return false, err
	}

	var result sql.Result
	if exists {
		result, err = tx.Exec(`UPDATE app_settings SET value = ?, updated_at = ? WHERE key = ? AND value = ?`, value, updatedAt.UTC(), key, expected)
	} else {
		result, err = tx.Exec(`INSERT INTO app_settings (key, value, updated_at) VALUES (?, ?, ?)
			ON CONFLICT(key) DO NOTHING`, key, value, updatedAt.UTC())
	}
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected != 1 {
		return false, nil
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
