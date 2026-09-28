package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
)

const MaxRunArchiveCollectionCacheBytes int64 = 32 << 30

// A receipt binds the cached manifest and every payload hash to durable storage.
// Paths are derived locally from the collection token and target, never stored.
type RunArchiveCollectionSource struct {
	ReceiptSHA256 string `json:"receipt_sha256"`
	Bytes         int64  `json:"bytes"`
}

func validateCollectionCacheMetadata(job *RunArchiveCollectionIntent) error {
	if job == nil || job.CacheLimitBytes < 0 || job.CacheLimitBytes > MaxRunArchiveCollectionCacheBytes || len(job.CachedSources) > len(job.Sources) {
		return errors.New("invalid archive collection cache metadata")
	}
	var total int64
	for target, source := range job.CachedSources {
		if job.Sources[target] == "" || source.Bytes <= 0 || source.Bytes > job.CacheLimitBytes-total {
			return errors.New("archive collection cache exceeds its frozen limit or source set")
		}
		if err := validateArchiveCollectionSources(map[string]string{target: source.ReceiptSHA256}); err != nil {
			return err
		}
		total += source.Bytes
	}
	return nil
}

func (s *SQLiteStore) lockCollectionCacheIntent(tx *storeTx, testID, token, lease, owner string) (*RunArchiveCollectionIntent, error) {
	if testID == "" || token == "" || (lease == "") != (owner == "") {
		return nil, errors.New("invalid archive collection cache identity")
	}
	if lease != "" {
		held, err := schedulerLeaseHeldInTransaction(tx, s.db.dialect, lease, owner)
		if err != nil {
			return nil, err
		}
		if !held {
			return nil, sql.ErrNoRows
		}
	}
	query := `SELECT ` + archiveCollectionColumns + currentArchiveCollection + ` AND c.test_id = ? AND c.intent_token = ? AND c.resolved = 0 AND c.exhausted = 0
 AND NOT EXISTS(SELECT 1 FROM run_archives a WHERE a.test_id = c.test_id)
 AND NOT EXISTS(SELECT 1 FROM archive_finalization_jobs j WHERE j.test_id = c.test_id)
 AND NOT EXISTS(SELECT 1 FROM archive_cleanup_jobs d WHERE d.test_id = c.test_id)`
	if s.db.dialect == storeDialectPostgres {
		query += ` FOR UPDATE OF c`
	}
	return scanRunArchiveCollection(tx.QueryRow(query, testID, token))
}

// ConfigureRunArchiveCollectionCache freezes a per-run byte ceiling before any
// cache writes. Restart or configuration changes cannot enlarge a run's ceiling.
// The cache implementation must separately serialize and bound its entire root.
func (s *SQLiteStore) ConfigureRunArchiveCollectionCache(testID, token string, limit int64, lease, owner string) (*RunArchiveCollectionIntent, bool, error) {
	if limit <= 0 || limit > MaxRunArchiveCollectionCacheBytes {
		return nil, false, errors.New("invalid archive collection cache limit")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	job, err := s.lockCollectionCacheIntent(tx, testID, token, lease, owner)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if job.CacheLimitBytes == 0 {
		if _, err = tx.Exec(`UPDATE run_archive_collection_intents SET cache_limit_bytes = ? WHERE test_id = ? AND intent_token = ?`, limit, testID, token); err != nil {
			return nil, false, err
		}
		job.CacheLimitBytes = limit
	}
	if err = tx.Commit(); err != nil {
		return nil, false, err
	}
	return job, true, nil
}

// SaveRunArchiveCollectionSource commits the trust anchor after synced cache
// files exist. An acknowledged source is immutable, including across retries.
func (s *SQLiteStore) SaveRunArchiveCollectionSource(testID, token, target string, source RunArchiveCollectionSource, lease, owner string) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	job, err := s.lockCollectionCacheIntent(tx, testID, token, lease, owner)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if prior, ok := job.CachedSources[target]; ok && prior != source {
		return false, errors.New("archive collection source receipt cannot change")
	}
	if job.CachedSources == nil {
		job.CachedSources = make(map[string]RunArchiveCollectionSource)
	}
	job.CachedSources[target] = source
	if err := validateCollectionCacheMetadata(job); err != nil {
		return false, err
	}
	data, err := json.Marshal(job.CachedSources)
	if err != nil {
		return false, err
	}
	if len(data) > 512<<10 {
		return false, errors.New("archive collection cache metadata exceeds limit")
	}
	if _, err = tx.Exec(`UPDATE run_archive_collection_intents SET cached_sources_json = ? WHERE test_id = ? AND intent_token = ?`, string(data), testID, token); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
