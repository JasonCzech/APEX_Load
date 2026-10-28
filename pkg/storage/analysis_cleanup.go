package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/internal/procidentity"
)

type AnalysisCleanupJob struct {
	Kind       string
	ProjectID  string
	ResourceID string
	NotBefore  time.Time
}
type AnalysisCleanupStore interface {
	ListAnalysisCleanupJobs(time.Time, int) ([]AnalysisCleanupJob, error)
	CompleteAnalysisCleanupJob(AnalysisCleanupJob) error
}

// Scheduling delay only reduces churn. It is never evidence that an owned
// native worker or durable writer has stopped; cleanup separately requires proof.
func queueAnalysisCleanupTx(tx *storeTx, kind, project, id string) error {
	_, err := tx.Exec(`INSERT INTO analysis_cleanup_jobs(kind,project_id,resource_id,not_before) VALUES(?,?,?,?) ON CONFLICT(kind,resource_id) DO UPDATE SET not_before=excluded.not_before`, kind, project, id, time.Now().UTC().Add(20*time.Minute))
	return err
}

func queueProjectAnalysisCleanupTx(tx *storeTx, project string) error {
	rows, err := tx.Query(`SELECT id FROM analysis_imports WHERE project_id=? UNION SELECT import_id FROM analysis_attempt_journal WHERE project_id=? UNION SELECT import_id FROM analysis_upload_writers WHERE project_id=?`, project, project, project)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
		if len(ids) > 1000 {
			rows.Close()
			return ErrAnalysisAuthority
		}
	}
	scanErr, closeErr := rows.Err(), rows.Close()
	if scanErr != nil {
		return scanErr
	}
	if closeErr != nil {
		return closeErr
	}
	for _, id := range ids {
		if err = queueAnalysisImportAttemptsCleanupTx(tx, project, id); err != nil {
			return err
		}
	}

	now := time.Now().UTC().Add(20 * time.Minute)
	for _, query := range []string{
		`INSERT INTO analysis_cleanup_jobs(kind,project_id,resource_id,not_before) SELECT 'dataset',project_id,id,? FROM analysis_imports WHERE project_id=? ON CONFLICT(kind,resource_id) DO UPDATE SET not_before=excluded.not_before`,
		`INSERT INTO analysis_cleanup_jobs(kind,project_id,resource_id,not_before) SELECT 'monitor',project_id,test_id,? FROM run_monitor_sessions WHERE project_id=? ON CONFLICT(kind,resource_id) DO UPDATE SET not_before=excluded.not_before`,
	} {
		if _, err := tx.Exec(query, now, project); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) ListAnalysisCleanupJobs(now time.Time, limit int) ([]AnalysisCleanupJob, error) {
	if limit < 1 || limit > 100 {
		return nil, errors.New("invalid analysis cleanup page")
	}
	rows, err := s.db.Query(`SELECT j.kind,j.project_id,j.resource_id,j.not_before FROM analysis_cleanup_jobs j
        LEFT JOIN analysis_imports d ON j.kind='dataset' AND d.id=j.resource_id
        LEFT JOIN tests t ON j.kind='monitor' AND t.id=j.resource_id
        WHERE j.not_before<=? AND ((j.kind='dataset' AND (d.id IS NULL OR d.state IN ('cancelled','deleted','deleting'))) OR (j.kind='monitor' AND t.id IS NULL) OR j.kind IN ('monitor-object','attempt','upload-writer'))
        ORDER BY j.not_before,j.resource_id LIMIT ?`, now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []AnalysisCleanupJob{}
	for rows.Next() {
		var j AnalysisCleanupJob
		if err := rows.Scan(&j.Kind, &j.ProjectID, &j.ResourceID, &j.NotBefore); err != nil {
			return nil, err
		}
		result = append(result, j)
	}
	return result, rows.Err()
}
func (s *SQLiteStore) CompleteAnalysisCleanupJob(j AnalysisCleanupJob) error {
	_, err := s.db.Exec(`DELETE FROM analysis_cleanup_jobs WHERE kind=? AND project_id=? AND resource_id=? AND not_before=?`, j.Kind, j.ProjectID, j.ResourceID, j.NotBefore)
	return err
}

// Caller already holds project ownership, then import ownership where present.
// Journals outlive row/project deletion, so late physical writer proof is not lost.
func queueAnalysisImportAttemptsCleanupTx(tx *storeTx, project, id string) error {
	if err := analysisUploadQueueImportTx(tx, project, id); err != nil {
		return err
	}
	rows, err := tx.Query(`SELECT payload FROM analysis_attempt_journal WHERE project_id=? AND import_id=? ORDER BY id`, project, id)
	if err != nil {
		return err
	}
	defer rows.Close()
	attempts := []AnalysisAttempt{}
	for rows.Next() {
		var body string
		if err = rows.Scan(&body); err != nil {
			return err
		}
		var a AnalysisAttempt
		if json.Unmarshal([]byte(body), &a) != nil || a.ProjectID != project || a.ImportID != id {
			return ErrAnalysisAuthority
		}
		attempts = append(attempts, a)
		if len(attempts) > 4096 {
			return ErrAnalysisAuthority
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	// Revocation/deletion must first preserve a legacy normalizing writer even
	// when it has not produced a single output key. Missing journal, empty output,
	// expired SQL lease and absent Owner never prove physical quiescence.
	var legacyBody, state, owner string
	legacyErr := tx.QueryRow(`SELECT payload,state,owner FROM analysis_imports WHERE id=? AND project_id=?`, id, project).Scan(&legacyBody, &state, &owner)
	if legacyErr != nil && !errors.Is(legacyErr, sql.ErrNoRows) {
		return legacyErr
	}
	if legacyErr == nil {
		var in AnalysisImport
		if json.Unmarshal([]byte(legacyBody), &in) != nil || in.ProjectID != project || in.ID != id {
			return ErrAnalysisAuthority
		}
		legacyUpload := in.UploadWriterVersion != AnalysisUploadWriterVersion && state == "uploading"
		for _, file := range in.Files {
			if in.UploadWriterVersion != AnalysisUploadWriterVersion && len(file.Chunks) > 0 {
				legacyUpload = true
			}
		}
		// Legacy uploads have no exact per-write journal or physical seal. Retain
		// their unknown writer fence; only versioned journalled uploads can prove
		// quiescence through their own durable physical-close receipts.
		if in.WriterAttestation == nil && (state == "normalizing" || legacyUpload) {
			tracked, unknown := false, false
			for _, a := range attempts {
				if owner != "" && a.ID == owner {
					tracked = true
				}
				if a.LegacyWriterUnknown {
					unknown = true
				}
			}
			if (!tracked || legacyUpload) && !unknown {
				var in AnalysisImport
				if json.Unmarshal([]byte(legacyBody), &in) != nil || in.ProjectID != project || in.ID != id {
					return ErrAnalysisAuthority
				}
				now := time.Now().UTC()
				sentinel := AnalysisAttempt{ID: uuid.NewString(), ProjectID: project, ImportID: id, Revision: in.Revision, CreatedAt: now, AbsoluteExpiry: now, Originals: append([]string{}, in.Originals...), Normalized: append([]string{}, in.Normalized...), Rejections: append([]string{}, in.RejectionArtifacts...), Obsolete: append([]string{}, in.ObsoleteObjects...), LegacyWriterUnknown: true, CleanupQueued: true}
				if err = analysisAttemptJournalInsert(context.Background(), tx, &sentinel); err != nil {
					return err
				}
				attempts = append(attempts, sentinel)
			}
		}
	}
	for i := range attempts {
		a := &attempts[i]
		a.CleanupQueued = true
		if err = analysisAttemptJournalWrite(context.Background(), tx, a); err != nil {
			return err
		}
		if err = queueAnalysisCleanupTx(tx, "attempt", project, a.ID); err != nil {
			return err
		}
	}
	return nil
}

// AnalysisDatasetCleanupStatus explains why a revoked dataset's namespace is
// still retained. Each unproven generation is counted once, by its reason:
//   - LegacyWriters: a legacy writer with no journalled plan (a pre-journal
//     upload, an unknown legacy owner, an output no journal plans). Only an
//     explicit intervention can prove its removal safe.
//   - UnrecordedWriters: a journalled generation without a recorded writer
//     process (minted before writer incarnations, by a controller with no
//     process identity, or rewritten by an older controller). Only its own
//     process can still close it.
//   - RemoteCommitsUnproven: its controller provably exited, but a remote
//     object store may still apply a commit it had sent. Only an explicit
//     intervention can prove its removal safe.
//   - UnprovenWriters: its writer was recorded where the asking host can
//     judge it (procidentity.Judgeable), so it may settle: it closes, or that
//     host proves its exit at a later sweep, unless its PID has been reused
//     by a process the host cannot inspect (another user's under hidepid).
//   - UnjudgeableWriters: its writer was recorded where the asking host cannot
//     judge it: another machine, or another boot or PID namespace of this one.
//     It resolves only if it is still running and closes, or a controller
//     that can judge it proves its exit; a restarted container or a replaced
//     pod with a new host name never can.
type AnalysisDatasetCleanupStatus struct {
	Ready                 bool
	Live                  bool
	LegacyWriters         int
	UnrecordedWriters     int
	RemoteCommitsUnproven int
	UnprovenWriters       int
	UnjudgeableWriters    int
}

// Resolvable reports whether a pending deletion may still complete without
// an intervention: some writer may yet close or be proven exited.
func (s AnalysisDatasetCleanupStatus) Resolvable() bool {
	return s.UnprovenWriters > 0 || s.UnjudgeableWriters > 0 || s.UnrecordedWriters > 0
}

// ResolvesHere reports whether the asking host itself will settle a writer:
// it closes, or this host proves its exit.
func (s AnalysisDatasetCleanupStatus) ResolvesHere() bool {
	return s.UnprovenWriters > 0
}

// countUnproven classifies a recorded, unproven writer by whether here can
// judge it; a nil here counts every one as judgeable.
func (s *AnalysisDatasetCleanupStatus) countUnproven(here func(procidentity.Identity) bool, incarnation procidentity.Identity) {
	if here != nil && !here(incarnation) {
		s.UnjudgeableWriters++
		return
	}
	s.UnprovenWriters++
}

// A whole namespace can be removed only after every retained attempt has its
// parent-issued physical seal. Unknown legacy failed plans remain retained.
func (s *SQLiteStore) AnalysisDatasetCleanupReady(ctx context.Context, project, id string) (bool, error) {
	status, err := s.AnalysisDatasetCleanupStatus(ctx, project, id, nil)
	return status.Ready, err
}

// AnalysisDatasetCleanupStatus reads a revoked dataset's retention reasons.
// here reports whether the asking host can judge a recorded writer.
func (s *SQLiteStore) AnalysisDatasetCleanupStatus(ctx context.Context, project, id string, here func(procidentity.Identity) bool) (AnalysisDatasetCleanupStatus, error) {
	var status AnalysisDatasetCleanupStatus
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return status, err
	}
	defer tx.Rollback()
	if _, err = lockProjectForOwnedMutation(tx, project); err != nil {
		var present int
		if e := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE id=?`, project).Scan(&present); e != nil || present != 0 {
			return status, ErrAnalysisPublicationUnknown
		}
	}
	in, err := analysisAttemptLoad(ctx, tx, id, project)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return status, err
	}
	if in != nil && in.State != "cancelled" && in.State != "deleting" && in.State != "deleted" {
		status.Live = true
		return status, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT payload FROM analysis_attempt_journal WHERE project_id=? AND import_id=?`, project, id)
	if err != nil {
		return status, err
	}
	defer rows.Close()
	count := 0
	known := map[string]bool{}
	for rows.Next() {
		count++
		if count > 4096 {
			return AnalysisDatasetCleanupStatus{}, ErrAnalysisAuthority
		}
		var body string
		if err = rows.Scan(&body); err != nil {
			return AnalysisDatasetCleanupStatus{}, err
		}
		var a AnalysisAttempt
		if json.Unmarshal([]byte(body), &a) != nil || a.ProjectID != project || a.ImportID != id {
			return AnalysisDatasetCleanupStatus{}, ErrAnalysisAuthority
		}
		if !a.WritersQuiesced {
			switch {
			case a.LegacyWriterUnknown:
				status.LegacyWriters++
			case a.Incarnation == nil:
				status.UnrecordedWriters++
			case a.Termination != nil && a.Termination.RemoteCommitUnproven:
				status.RemoteCommitsUnproven++
			default:
				status.countUnproven(here, *a.Incarnation)
			}
		}
		for _, key := range append(append(append(append([]string{}, a.Originals...), a.Normalized...), a.Rejections...), a.Obsolete...) {
			if !analysisAttemptOwnedOutputKey(project, id, key) {
				return AnalysisDatasetCleanupStatus{}, ErrAnalysisAuthority
			}
			known[key] = true
		}
	}
	if err = rows.Err(); err != nil {
		return AnalysisDatasetCleanupStatus{}, err
	}
	if err = rows.Close(); err != nil {
		return AnalysisDatasetCleanupStatus{}, err
	}
	if in != nil && in.WriterAttestation == nil {
		for _, key := range append(append(append(append([]string{}, in.Originals...), in.Normalized...), in.RejectionArtifacts...), in.ObsoleteObjects...) {
			if !known[key] {
				// An output no journal plans has no recorded writer at all.
				status.LegacyWriters++
				break
			}
		}
	}
	if err = analysisUploadUnprovenTx(ctx, tx, project, id, here, &status); err != nil {
		return AnalysisDatasetCleanupStatus{}, err
	}
	if tx.Commit() != nil {
		return AnalysisDatasetCleanupStatus{}, ErrAnalysisPublicationUnknown
	}
	status.Ready = status.LegacyWriters == 0 && status.UnrecordedWriters == 0 && status.RemoteCommitsUnproven == 0 && status.UnprovenWriters == 0 && status.UnjudgeableWriters == 0
	return status, nil
}

// A blocked physical owner remains pending, but is moved out of the first page
// with a bounded retry delay. The exact not_before CAS cannot overwrite a newer
// cancellation/cleanup queue entry created by a concurrent transaction.
func (s *SQLiteStore) DeferAnalysisCleanupJob(j AnalysisCleanupJob, until time.Time) error {
	if until.Before(time.Now().UTC()) || until.After(time.Now().UTC().Add(time.Hour)) {
		return ErrAnalysisAuthority
	}
	_, err := s.db.Exec(`UPDATE analysis_cleanup_jobs SET not_before=? WHERE kind=? AND project_id=? AND resource_id=? AND not_before=?`, until.UTC(), j.Kind, j.ProjectID, j.ResourceID, j.NotBefore)
	return err
}
