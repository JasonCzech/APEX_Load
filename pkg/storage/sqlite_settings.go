package storage

// SQLite store — settings. Split from sqlite.go (same package, behavior-preserving).

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"time"
)

const ensureSettingSequenceAtLeastQuery = `INSERT INTO app_settings (key, value, updated_at) VALUES (?, ?, ?)
	ON CONFLICT(key) DO UPDATE SET
	value=CASE
		WHEN CAST(app_settings.value AS INTEGER) < CAST(excluded.value AS INTEGER) THEN excluded.value
		ELSE app_settings.value
	END,
	updated_at=CASE
		WHEN CAST(app_settings.value AS INTEGER) < CAST(excluded.value AS INTEGER) THEN excluded.updated_at
		ELSE app_settings.updated_at
	END
	RETURNING value`

func (s *SQLiteStore) SaveSetting(key string, value string) error {
	_, err := s.db.Exec(`INSERT INTO app_settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`, key, value, time.Now())
	return err
}

func (s *SQLiteStore) GetSetting(key string) (string, error) {
	return s.GetSettingContext(context.Background(), key)
}

// GetSettingContext is the cancellable settings read used by bounded health
// probes. GetSetting remains available for compatibility with existing callers.
func (s *SQLiteStore) GetSettingContext(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = ?`, key).Scan(&value)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	return value, nil
}

// DeleteSetting removes a durable setting so consumers can converge to their
// configured defaults. It is intentionally a concrete-store operation rather
// than part of the broad Store interface; reset workflows that need it can opt
// in without forcing unrelated compatibility stores to implement deletion.
func (s *SQLiteStore) DeleteSetting(key string) error {
	_, err := s.db.Exec(`DELETE FROM app_settings WHERE key = ?`, key)
	return err
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

// EnsureSettingSequenceAtLeast atomically raises an integer setting without
// ever lowering a value advanced by another controller. Startup ref-key
// observation uses this before allocating from pre-existing durable runs.
func (s *SQLiteStore) EnsureSettingSequenceAtLeast(key string, minimum int) (int, error) {
	if minimum <= 0 {
		return 0, fmt.Errorf("setting sequence minimum must be positive")
	}
	var raw string
	err := s.db.QueryRow(ensureSettingSequenceAtLeastQuery, key, strconv.Itoa(minimum), time.Now()).Scan(&raw)
	if err != nil {
		return 0, err
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum {
		return 0, fmt.Errorf("invalid setting sequence floor %q", raw)
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
	// Idempotent belt-and-braces: the explicit Close below is kept because it
	// surfaces the Close error, which a bare defer discards. The defer covers
	// the paths that never reach it — notably a panic in a scan helper, which
	// the controller recovers, pinning this connection for the process's life.
	defer rows.Close()
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
