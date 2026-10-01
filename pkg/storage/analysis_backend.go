package storage

import (
	"encoding/hex"
	"errors"
	"time"
)

// Analytical evidence and the cleanup outbox share one durable storage
// namespace. Replicas must agree on its identity before accessing artifacts.
// This setting is immutable during ordinary operation; a storage migration
// must move evidence and update its identity together while writers are stopped.
func (s *SQLiteStore) EnsureAnalysisStorageIdentity(identity string) error {
	decoded, err := hex.DecodeString(identity)
	if err != nil || len(decoded) != 32 {
		return errors.New("invalid analytical storage identity")
	}
	const key = "analysis_storage_identity_v1"
	if _, err = s.db.Exec(`INSERT INTO app_settings(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO NOTHING`, key, identity, time.Now().UTC()); err != nil {
		return err
	}
	stored, err := s.GetSetting(key)
	if err != nil {
		return err
	}
	if stored != identity {
		return errors.New("analytical storage configuration changed; restore its recorded backend or complete an offline evidence migration")
	}
	return nil
}
