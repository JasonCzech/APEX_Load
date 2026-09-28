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
	if record == nil || len(manifest) == 0 || len(manifest) > MaxArchivePreparedManifestBytes {
		return false, errors.New("bounded archive manifest preparation is required")
	}
	var identity struct {
		TestID string `json:"test_id"`
	}
	if err := json.Unmarshal([]byte(manifest), &identity); err != nil || identity.TestID != strings.TrimSpace(testID) {
		return false, errors.New("prepared archive manifest identity is invalid")
	}
	sum := sha256.Sum256([]byte(manifest))
	if !strings.EqualFold(hex.EncodeToString(sum[:]), strings.TrimSpace(record.ManifestSHA256)) {
		return false, errors.New("prepared archive manifest digest is invalid")
	}
	return s.updateArchiveFinalizationRecordIfToken(testID, intentToken, record, &manifest)
}
