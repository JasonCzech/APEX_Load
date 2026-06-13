package storage

// SQLite store — settings. Split from sqlite.go (same package, behavior-preserving).

import (
	"database/sql"
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
