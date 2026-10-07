package storage

import (
	"errors"
	"regexp"
	"strings"
	"time"
)

var appDynamicsQuotaKey = regexp.MustCompile(`^[a-f0-9]{64}$`)

func (s *SQLiteStore) EnsureAppDynamicsQuotaSchema() error {
	for _, query := range []string{
		`CREATE TABLE IF NOT EXISTS appdynamics_quota_pools (resource_key TEXT PRIMARY KEY,updated_at TIMESTAMP NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS appdynamics_quota_leases (resource_key TEXT NOT NULL REFERENCES appdynamics_quota_pools(resource_key) ON DELETE CASCADE,executor_id TEXT NOT NULL,expires_at TIMESTAMP NOT NULL,PRIMARY KEY(resource_key,executor_id))`,
		`CREATE INDEX IF NOT EXISTS idx_appdynamics_quota_expiry ON appdynamics_quota_leases(resource_key,expires_at)`,
	} {
		if _, err := s.db.Exec(query); err != nil {
			return err
		}
	}
	return nil
}

// AllocateAppDynamicsQuota admits at most two distinct executor identities to a
// resource for sixty seconds. Each executor must independently enforce half of
// the configured account request budget, shared across its concurrent jobs.
func (s *SQLiteStore) AllocateAppDynamicsQuota(resourceKey, executorID string, now time.Time) (bool, error) {
	if !appDynamicsQuotaKey.MatchString(resourceKey) || executorID == "" || len(executorID) > 256 || strings.ContainsAny(executorID, "\r\n\x00") || now.IsZero() {
		return false, errors.New("invalid AppDynamics quota identity")
	}
	if err := s.EnsureAppDynamicsQuotaSchema(); err != nil {
		return false, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	now = now.UTC()
	if _, err = tx.Exec(`INSERT INTO appdynamics_quota_pools(resource_key,updated_at) VALUES(?,?) ON CONFLICT(resource_key) DO NOTHING`, resourceKey, now); err != nil {
		return false, err
	}
	// PostgreSQL locks the shared resource row before inspecting its leases;
	// SQLite's write transaction takes its corresponding database write lock.
	if tx.dialect == storeDialectPostgres {
		var locked string
		if err = tx.QueryRow(`SELECT resource_key FROM appdynamics_quota_pools WHERE resource_key=? FOR UPDATE`, resourceKey).Scan(&locked); err != nil {
			return false, err
		}
	} else if _, err = tx.Exec(`UPDATE appdynamics_quota_pools SET updated_at=updated_at WHERE resource_key=?`, resourceKey); err != nil {
		return false, err
	}
	if _, err = tx.Exec(`DELETE FROM appdynamics_quota_leases WHERE resource_key=? AND expires_at<=?`, resourceKey, now); err != nil {
		return false, err
	}
	var current, count int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM appdynamics_quota_leases WHERE resource_key=? AND executor_id=?`, resourceKey, executorID).Scan(&current); err != nil {
		return false, err
	}
	if current == 0 {
		if err = tx.QueryRow(`SELECT COUNT(*) FROM appdynamics_quota_leases WHERE resource_key=?`, resourceKey).Scan(&count); err != nil {
			return false, err
		}
		if count >= 2 {
			return false, tx.Commit()
		}
	}
	if _, err = tx.Exec(`INSERT INTO appdynamics_quota_leases(resource_key,executor_id,expires_at) VALUES(?,?,?) ON CONFLICT(resource_key,executor_id) DO UPDATE SET expires_at=excluded.expires_at`, resourceKey, executorID, now.Add(time.Minute)); err != nil {
		return false, err
	}
	if _, err = tx.Exec(`UPDATE appdynamics_quota_pools SET updated_at=? WHERE resource_key=?`, now, resourceKey); err != nil {
		return false, err
	}
	return true, tx.Commit()
}
