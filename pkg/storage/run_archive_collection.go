package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

const MaxRunArchiveCollectionSources = 256

// RunArchiveCollectionIntent binds collection to one durable test incarnation
// and to the original supervised assignment on each expected generator. These
// digests grant no execution authority and remain private storage metadata.
type RunArchiveCollectionIntent struct {
	TestID          string
	ProjectID       string
	StartTime       time.Time
	IntentToken     string
	Sources         map[string]string
	CacheLimitBytes int64
	CachedSources   map[string]RunArchiveCollectionSource
	Attempts        int
	Exhausted       bool
	Resolved        bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func validateArchiveCollectionSources(sources map[string]string) error {
	if len(sources) == 0 || len(sources) > MaxRunArchiveCollectionSources {
		return errors.New("invalid archive collection source count")
	}
	for target, identity := range sources {
		if strings.TrimSpace(target) != target || target == "" || len(target) > 1024 || strings.ContainsAny(target, "\x00\r\n") {
			return errors.New("invalid archive collection target")
		}
		if len(identity) != 64 {
			return errors.New("invalid archive collection identity")
		}
		for _, c := range identity {
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
				return errors.New("invalid archive collection identity")
			}
		}
	}
	return nil
}

// PrepareRunArchiveCollection merges expected sources before dispatch. Existing
// target identities are immutable; token rotation must not rebind earlier files.
// A false result means the incarnation, status or execution lease is stale.
func (s *SQLiteStore) PrepareRunArchiveCollection(testID, projectID string, started time.Time, sources map[string]string, leaseName, owner string) (*RunArchiveCollectionIntent, bool, error) {
	if testID == "" || started.IsZero() {
		return nil, false, errors.New("archive collection run identity is required")
	}
	if err := validateArchiveCollectionSources(sources); err != nil {
		return nil, false, err
	}
	if (leaseName == "") != (owner == "") {
		return nil, false, errors.New("archive collection lease name and owner must be supplied together")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	if leaseName != "" {
		held, err := schedulerLeaseHeldInTransaction(tx, s.db.dialect, leaseName, owner)
		if err != nil || !held {
			return nil, false, err
		}
	}
	query := `SELECT COALESCE(project_id, ''), start_time, status FROM tests WHERE id = ?`
	if s.db.dialect == storeDialectPostgres {
		query += ` FOR UPDATE`
	}
	var storedProject, state string
	var storedStart time.Time
	if err := tx.QueryRow(query, testID).Scan(&storedProject, &storedStart, &state); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, err
	}
	canonical := started
	if s.db.dialect == storeDialectPostgres {
		canonical = canonical.Truncate(time.Microsecond)
	}
	if normalizeProjectID(storedProject) != normalizeProjectID(projectID) || !storedStart.Equal(canonical) || (state != "INITIALIZING" && state != "STARTING" && state != "RUNNING" && state != "PAUSED" && state != "STOPPING") {
		return nil, false, nil
	}
	now := time.Now().UTC()
	job := &RunArchiveCollectionIntent{TestID: testID, ProjectID: normalizeProjectID(storedProject), StartTime: storedStart, IntentToken: uuid.NewString(), Sources: make(map[string]string), CreatedAt: now, UpdatedAt: now}
	prior, err := scanRunArchiveCollection(tx.QueryRow(`SELECT `+archiveCollectionColumns+` FROM run_archive_collection_intents c WHERE c.test_id = ? AND c.project_id = ? AND c.test_start_time = ?`, testID, job.ProjectID, storedStart))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	if err == nil {
		if prior.Resolved || prior.Exhausted {
			return nil, false, nil
		}
		job = prior
		job.UpdatedAt = now
	}
	for target, identity := range sources {
		if prior, ok := job.Sources[target]; ok && prior != identity {
			return nil, false, errors.New("archive collection target identity cannot change")
		}
		job.Sources[target] = identity
	}
	if err := validateArchiveCollectionSources(job.Sources); err != nil {
		return nil, false, err
	}
	data, err := json.Marshal(job.Sources)
	if err != nil {
		return nil, false, err
	}
	if len(data) > 512<<10 {
		return nil, false, errors.New("archive collection metadata exceeds limit")
	}
	_, err = tx.Exec(`INSERT INTO run_archive_collection_intents(test_id, project_id, test_start_time, intent_token, sources_json, attempts, exhausted, created_at, updated_at)
 VALUES(?, ?, ?, ?, ?, 0, 0, ?, ?) ON CONFLICT(test_id) DO UPDATE SET sources_json = excluded.sources_json, updated_at = excluded.updated_at
 WHERE run_archive_collection_intents.project_id = excluded.project_id AND run_archive_collection_intents.test_start_time = excluded.test_start_time`, testID, job.ProjectID, storedStart, job.IntentToken, string(data), job.CreatedAt, now)
	if err != nil {
		return nil, false, err
	}
	// An existing different incarnation is never silently reset. Normal deletion
	// cascades the intent, so seeing it here indicates an unsupported ID reuse.
	var token string
	if err := tx.QueryRow(`SELECT intent_token FROM run_archive_collection_intents WHERE test_id = ?`, testID).Scan(&token); err != nil {
		return nil, false, err
	}
	if token != job.IntentToken {
		return nil, false, errors.New("archive collection belongs to another incarnation")
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return job, true, nil
}

const archiveCollectionColumns = `c.test_id, c.project_id, c.test_start_time, c.intent_token, c.sources_json, c.cache_limit_bytes, c.cached_sources_json, c.attempts, c.exhausted, c.resolved, c.created_at, c.updated_at`
const currentArchiveCollection = ` FROM run_archive_collection_intents c JOIN tests t ON t.id = c.test_id
 WHERE c.project_id = COALESCE(NULLIF(t.project_id, ''), 'unassigned') AND c.test_start_time = t.start_time`

type collectionRowScanner interface{ Scan(...any) error }

func scanRunArchiveCollection(row collectionRowScanner) (*RunArchiveCollectionIntent, error) {
	var job RunArchiveCollectionIntent
	var raw, cached string
	var exhausted, resolved int
	if err := row.Scan(&job.TestID, &job.ProjectID, &job.StartTime, &job.IntentToken, &raw, &job.CacheLimitBytes, &cached, &job.Attempts, &exhausted, &resolved, &job.CreatedAt, &job.UpdatedAt); err != nil {
		return nil, err
	}
	if len(raw) > 512<<10 {
		return nil, errors.New("archive collection metadata exceeds limit")
	}
	if err := json.Unmarshal([]byte(raw), &job.Sources); err != nil {
		return nil, err
	}
	if err := validateArchiveCollectionSources(job.Sources); err != nil {
		return nil, err
	}
	if parsed, err := uuid.Parse(job.IntentToken); err != nil || parsed.String() != job.IntentToken {
		return nil, errors.New("invalid archive collection token")
	}
	if job.Attempts < 0 || job.Attempts > 100 || exhausted < 0 || exhausted > 1 || resolved < 0 || resolved > 1 || job.StartTime.IsZero() || job.CreatedAt.IsZero() || job.UpdatedAt.IsZero() {
		return nil, errors.New("invalid archive collection progress")
	}
	if len(cached) > 512<<10 {
		return nil, errors.New("archive collection cache metadata exceeds limit")
	}
	if err := json.Unmarshal([]byte(cached), &job.CachedSources); err != nil {
		return nil, err
	}
	if err := validateCollectionCacheMetadata(&job); err != nil {
		return nil, err
	}
	job.Exhausted = exhausted != 0
	job.Resolved = resolved != 0
	return &job, nil
}
func (s *SQLiteStore) GetRunArchiveCollection(testID string) (*RunArchiveCollectionIntent, error) {
	return scanRunArchiveCollection(s.db.QueryRow(`SELECT `+archiveCollectionColumns+currentArchiveCollection+` AND c.test_id = ?`, testID))
}

func (s *SQLiteStore) ListRunArchiveCollections(limit int, dueBefore time.Time) ([]*RunArchiveCollectionIntent, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := s.db.Query(`SELECT `+archiveCollectionColumns+currentArchiveCollection+` AND t.status IN ('COMPLETE','FAILED','ABORTED') AND t.end_time IS NOT NULL
 AND c.resolved = 0 AND c.exhausted = 0 AND (c.attempts = 0 OR c.updated_at <= ?)
 AND NOT EXISTS(SELECT 1 FROM run_archives a WHERE a.test_id = c.test_id)
 AND NOT EXISTS(SELECT 1 FROM archive_finalization_jobs j WHERE j.test_id = c.test_id)
 ORDER BY c.updated_at, c.test_id LIMIT ?`, dueBefore, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []*RunArchiveCollectionIntent
	for rows.Next() {
		job, err := scanRunArchiveCollection(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

// ClaimRunArchiveCollectionAttempt counts attempts before any network I/O, so a
// crashing worker cannot reset the retry budget. The frozen token and attempt
// counter reject stale concurrent workers. Callers must additionally hold the
// shared source fence for the full I/O attempt.
func (s *SQLiteStore) ClaimRunArchiveCollectionAttempt(testID, token string, attempts int, leaseName, owner string) (bool, error) {
	if testID == "" || token == "" || attempts < 0 || attempts >= 100 || (leaseName == "") != (owner == "") {
		return false, errors.New("invalid collection attempt")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if leaseName != "" {
		held, err := schedulerLeaseHeldInTransaction(tx, s.db.dialect, leaseName, owner)
		if err != nil || !held {
			return false, err
		}
	}
	result, err := tx.Exec(`UPDATE run_archive_collection_intents SET attempts = attempts + 1, updated_at = ?
 WHERE test_id = ? AND intent_token = ? AND attempts = ? AND exhausted = 0 AND resolved = 0
 AND EXISTS(SELECT 1 FROM tests t WHERE t.id = run_archive_collection_intents.test_id AND t.start_time = run_archive_collection_intents.test_start_time
 AND COALESCE(NULLIF(t.project_id,''),'unassigned') = run_archive_collection_intents.project_id AND t.status IN ('COMPLETE','FAILED','ABORTED') AND t.end_time IS NOT NULL)
 AND NOT EXISTS(SELECT 1 FROM run_archives a WHERE a.test_id = run_archive_collection_intents.test_id)
 AND NOT EXISTS(SELECT 1 FROM archive_finalization_jobs j WHERE j.test_id = run_archive_collection_intents.test_id)`, time.Now().UTC(), testID, token, attempts)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return count == 1, nil
}

func (s *SQLiteStore) ExhaustRunArchiveCollection(testID, token string, attempts int, leaseName, owner string) (bool, error) {
	if testID == "" || token == "" || attempts <= 0 || attempts > 100 || (leaseName == "") != (owner == "") {
		return false, errors.New("invalid collection exhaustion")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if leaseName != "" {
		held, err := schedulerLeaseHeldInTransaction(tx, s.db.dialect, leaseName, owner)
		if err != nil || !held {
			return false, err
		}
	}
	result, err := tx.Exec(`UPDATE run_archive_collection_intents SET exhausted = 1, updated_at = ? WHERE test_id = ? AND intent_token = ? AND attempts = ? AND exhausted = 0 AND resolved = 0 AND EXISTS(SELECT 1 FROM tests t WHERE t.id = run_archive_collection_intents.test_id AND t.start_time = run_archive_collection_intents.test_start_time
 AND COALESCE(NULLIF(t.project_id,''),'unassigned') = run_archive_collection_intents.project_id AND t.status IN ('COMPLETE','FAILED','ABORTED') AND t.end_time IS NOT NULL)
 AND NOT EXISTS(SELECT 1 FROM run_archives a WHERE a.test_id = run_archive_collection_intents.test_id)
 AND NOT EXISTS(SELECT 1 FROM archive_finalization_jobs j WHERE j.test_id = run_archive_collection_intents.test_id)`, time.Now().UTC(), testID, token, attempts)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if count == 1 {
		// Terminality already installed an unconfirmed archive marker in the
		// lifecycle transaction. Only after all bounded collection attempts are
		// exhausted can that marker become a definitive collection failure.
		if _, err := tx.Exec(`UPDATE run_archive_failures SET reason = ?, recorded_at = ?
			WHERE test_id = ? AND reason = ?`, RunArchiveFailureRemoteCollection,
			time.Now().UTC(), testID, RunArchiveFailureUnconfirmed); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return count == 1, nil
}

// ReconcileExhaustedRunArchiveFailures upgrades terminal rows exhausted by an
// older controller that left only the atomic unconfirmed marker. The bounded
// repair is lease-fenced and never changes a published or pending archive.
func (s *SQLiteStore) ReconcileExhaustedRunArchiveFailures(leaseName, owner string, limit int) (int64, error) {
	if limit <= 0 || limit > 1000 || (leaseName == "") != (owner == "") {
		return 0, errors.New("invalid archive failure repair request")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if leaseName != "" {
		held, err := schedulerLeaseHeldInTransaction(tx, s.db.dialect, leaseName, owner)
		if err != nil || !held {
			return 0, err
		}
	}
	result, err := tx.Exec(`UPDATE run_archive_failures SET reason = ?, recorded_at = ?
		WHERE reason = ? AND test_id IN (
			SELECT f.test_id FROM run_archive_failures f
			JOIN run_archive_collection_intents c ON c.test_id = f.test_id
			JOIN tests t ON t.id = f.test_id
			WHERE f.reason = ? AND c.exhausted = 1 AND c.resolved = 0
			AND c.project_id = f.project_id AND c.test_start_time = f.test_start_time
			AND COALESCE(NULLIF(t.project_id, ''), 'unassigned') = f.project_id
			AND t.start_time = f.test_start_time
			AND t.end_time = f.test_end_time AND t.status = f.test_status
			AND NOT EXISTS (SELECT 1 FROM run_archives a WHERE a.test_id = f.test_id)
			AND NOT EXISTS (SELECT 1 FROM archive_finalization_jobs j WHERE j.test_id = f.test_id)
			ORDER BY c.updated_at, f.test_id LIMIT ?
		)`, RunArchiveFailureRemoteCollection, time.Now().UTC(), RunArchiveFailureUnconfirmed,
		RunArchiveFailureUnconfirmed, limit)
	if err != nil {
		return 0, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return count, nil
}
