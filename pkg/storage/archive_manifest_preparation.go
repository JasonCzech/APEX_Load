package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

// MaxArchivePreparedManifestBytes bounds each private durable recovery marker.
const MaxArchivePreparedManifestBytes = 8 << 20

// PrepareArchiveFinalizationManifestIfToken commits the next manifest's bytes
// and trust anchor together, before its atomic local replacement. Readers of the
// archive catalog cannot see this private recovery marker.
func (s *SQLiteStore) PrepareArchiveFinalizationManifestIfToken(testID, intentToken string, record *RunArchiveRecord, manifest string) (bool, error) {
	if err := validatePreparedArchiveManifest(testID, record, manifest); err != nil {
		return false, err
	}
	return s.updateArchiveFinalizationRecordIfToken(testID, intentToken, record, &manifest)
}

// validatePreparedArchiveManifest checks that prepared bytes are bounded, name
// the run and are the exact bytes the record's trust anchor describes.
func validatePreparedArchiveManifest(testID string, record *RunArchiveRecord, manifest string) error {
	if record == nil || len(manifest) == 0 || len(manifest) > MaxArchivePreparedManifestBytes {
		return errors.New("bounded archive manifest preparation is required")
	}
	var identity struct {
		TestID string `json:"test_id"`
	}
	if err := json.Unmarshal([]byte(manifest), &identity); err != nil || identity.TestID != strings.TrimSpace(testID) {
		return errors.New("prepared archive manifest identity is invalid")
	}
	sum := sha256.Sum256([]byte(manifest))
	if !strings.EqualFold(hex.EncodeToString(sum[:]), strings.TrimSpace(record.ManifestSHA256)) {
		return errors.New("prepared archive manifest digest is invalid")
	}
	return nil
}
