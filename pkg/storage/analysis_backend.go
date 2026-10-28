package storage

import (
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"
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

const analysisVolumeIdentitySettingKey = "analysis_volume_identity_v1"

func analysisValidVolumeIdentity(identity string) bool {
	if analysisCanonicalHash(identity) {
		return true
	}
	parsed, err := uuid.Parse(identity)
	return err == nil && parsed.String() == identity
}

// GetAnalysisVolumeIdentity returns the bound filesystem volume identity from
// app_settings, or "" when no analytical filesystem volume has been bound yet.
func (s *SQLiteStore) GetAnalysisVolumeIdentity() (string, error) {
	return s.GetSetting(analysisVolumeIdentitySettingKey)
}

// EnsureAnalysisVolumeIdentity binds a filesystem volume identity at writer
// mint time using compare-and-set (insert-if-absent) and verifies that the
// stored volume identity matches.
func (s *SQLiteStore) EnsureAnalysisVolumeIdentity(identity string) (string, error) {
	if !analysisValidVolumeIdentity(identity) {
		return "", errors.New("invalid analytical volume identity")
	}
	if _, err := s.db.Exec(`INSERT INTO app_settings(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO NOTHING`, analysisVolumeIdentitySettingKey, identity, time.Now().UTC()); err != nil {
		return "", err
	}
	stored, err := s.GetSetting(analysisVolumeIdentitySettingKey)
	if err != nil {
		return "", err
	}
	if stored != identity {
		return stored, errors.New("analytical archive volume identity changed; restore the mounted volume or rebind after offline migration")
	}
	return stored, nil
}

// RebindAnalysisVolumeIdentity updates the bound filesystem volume identity
// during an explicit administrator intervention after a volume migration.
func (s *SQLiteStore) RebindAnalysisVolumeIdentity(identity string) error {
	if !analysisValidVolumeIdentity(identity) {
		return errors.New("invalid analytical volume identity")
	}
	_, err := s.db.Exec(`INSERT INTO app_settings(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`, analysisVolumeIdentitySettingKey, identity, time.Now().UTC())
	return err
}
