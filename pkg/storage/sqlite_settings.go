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
