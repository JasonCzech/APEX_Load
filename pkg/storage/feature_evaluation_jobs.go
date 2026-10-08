package storage

import (
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Evaluation job states. A job is queued, then running under a leased owner,
// and ends ready, failed, cancelled or expired. Results are bounded and
// published by one fenced write, so there is no separate publishing state.
const (
	EvaluationJobQueued    = "queued"
	EvaluationJobRunning   = "running"
	EvaluationJobReady     = "ready"
	EvaluationJobFailed    = "failed"
	EvaluationJobCancelled = "cancelled"
	EvaluationJobExpired   = "expired"

	MaxEvaluationJobRequestBytes = 64 << 10
	MaxEvaluationJobResultBytes  = 1 << 20
)

var (
	ErrEvaluationJobKeyConflict     = errors.New("evaluation idempotency key was already used with a different request")
	ErrEvaluationJobQuota           = errors.New("project evaluation queue is full")
	ErrEvaluationJobNotFound        = errors.New("evaluation job not found")
	ErrEvaluationJobInvalid         = errors.New("invalid evaluation job")
	ErrEvaluationJobProjectNotFound = errors.New("evaluation project not found")
)

// Evaluation failure codes say why a job ended without a result.
const (
	EvaluationFailureRunError          = "run_error"
	EvaluationFailureDeadline          = "deadline_exceeded"
	EvaluationFailureResultTooLarge    = "result_too_large"
	EvaluationFailureResultEncoding    = "result_encoding"
	EvaluationFailurePanic             = "panic"
	EvaluationFailureAttemptsExhausted = "attempts_exhausted"
)

// EvaluationJob is one bounded analytical evaluation (browser intelligence,
// telemetry acceptance, derived analysis, export) with its frozen inputs.
type EvaluationJob struct {
	ID                 string `json:"id"`
	ProjectID          string `json:"project_id"`
	Actor              string `json:"actor"`
	Kind               string `json:"kind"`
	IdempotencyKeyHash string `json:"-"`
	// RequestDigest identifies the exact request an idempotency key was used
	// with; InputFingerprint only records what the result was computed from.
	RequestDigest    string `json:"-"`
	Request          string `json:"request"`
	InputFingerprint string `json:"input_fingerprint"`
	// SubjectID, when set by the job's kind, is what the evaluation is about
	// (a browser evaluation's canonical run ID), so its jobs are found by an
	// index rather than by reading requests.
	SubjectID      string     `json:"subject_id,omitempty"`
	State          string     `json:"state"`
	Revision       int64      `json:"revision"`
	Owner          string     `json:"-"`
	LeaseExpiresAt *time.Time `json:"-"`
	Progress       string     `json:"progress,omitempty"`
	Result         string     `json:"result,omitempty"`
	ResultSHA256   string     `json:"result_sha256,omitempty"`
	Failure        string     `json:"failure,omitempty"`
	FailureCode    string     `json:"failure_code,omitempty"`
	// Attempts counts claims; a job that keeps losing its runner expires.
	Attempts int `json:"attempts"`
	// ResultBytes is the stored result size; listings carry it instead of
	// the result itself.
	ResultBytes int64      `json:"result_bytes,omitempty"`
	HiddenAt    *time.Time `json:"hidden_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// Unfinished reports whether the job can still run.
func (j *EvaluationJob) Unfinished() bool {
	return j != nil && (j.State == EvaluationJobQueued || j.State == EvaluationJobRunning)
}

type EvaluationJobStore interface {
	CreateEvaluationJob(job *EvaluationJob, maxActivePerProject int) (*EvaluationJob, bool, error)
	GetEvaluationJob(projectID, id string) (*EvaluationJob, error)
	ListEvaluationJobs(projectID, kind string, limit, offset int) ([]*EvaluationJob, error)
	ListEvaluationJobsForSubject(projectID, kind, subjectID string, limit int) ([]*EvaluationJob, error)
	ClaimEvaluationJob(owner string, now time.Time, lease time.Duration, kinds []string, maxAttempts int) (*EvaluationJob, error)
	RenewEvaluationJob(id, owner string, now time.Time, lease time.Duration) (bool, error)
	CompleteEvaluationJobFenced(id, owner, state, result, resultSHA256, failureCode, failure string, now time.Time, leaseName, leaseOwner string, generation uint64) (bool, error)
	CancelEvaluationJob(projectID, id string, now time.Time) (bool, error)
	HideEvaluationJob(projectID, id string, now time.Time) (bool, error)
}

var _ EvaluationJobStore = (*SQLiteStore)(nil)

const evaluationJobColumns = `id, project_id, actor, kind, idempotency_key_hash, request_digest, request, input_fingerprint, state, revision, owner,
	lease_expires_at, progress, result, result_sha256, failure, failure_code, attempts, hidden_at, created_at, updated_at, completed_at, result_bytes, subject_id`

// evaluationJobSummaryColumns leave the request and result bodies out; a
// listing returns their size and hash only.
const evaluationJobSummaryColumns = `id, project_id, actor, kind, idempotency_key_hash, request_digest, '' AS request, input_fingerprint, state, revision, owner,
	lease_expires_at, progress, '' AS result, result_sha256, failure, failure_code, attempts, hidden_at, created_at, updated_at, completed_at, result_bytes, subject_id`

func scanEvaluationJob(row interface{ Scan(...any) error }) (*EvaluationJob, error) {
	var job EvaluationJob
	var lease, hidden, completed sql.NullTime
	var resultBytes sql.NullInt64
	if err := row.Scan(&job.ID, &job.ProjectID, &job.Actor, &job.Kind, &job.IdempotencyKeyHash, &job.RequestDigest, &job.Request, &job.InputFingerprint,
		&job.State, &job.Revision, &job.Owner, &lease, &job.Progress, &job.Result, &job.ResultSHA256, &job.Failure, &job.FailureCode, &job.Attempts, &hidden,
		&job.CreatedAt, &job.UpdatedAt, &completed, &resultBytes, &job.SubjectID); err != nil {
		return nil, err
	}
	job.ResultBytes = resultBytes.Int64
	for _, pair := range []struct {
		value sql.NullTime
		into  **time.Time
	}{{lease, &job.LeaseExpiresAt}, {hidden, &job.HiddenAt}, {completed, &job.CompletedAt}} {
		if pair.value.Valid {
			value := pair.value.Time
			*pair.into = &value
		}
	}
	return &job, nil
}

// CreateEvaluationJob inserts a queued job. With an idempotency key hash the
// same key returns the existing job when its request digest matches and
// ErrEvaluationJobKeyConflict otherwise; inputs that changed since (a run
// still ingesting) do not make an identical retry a different request. At
// most maxActivePerProject jobs may be queued or running per project.
func (s *SQLiteStore) CreateEvaluationJob(job *EvaluationJob, maxActivePerProject int) (*EvaluationJob, bool, error) {
	if job == nil || strings.TrimSpace(job.ID) == "" || strings.TrimSpace(job.Kind) == "" || strings.TrimSpace(job.Actor) == "" ||
		strings.TrimSpace(job.InputFingerprint) == "" || len(job.Request) > MaxEvaluationJobRequestBytes || maxActivePerProject < 1 ||
		(job.IdempotencyKeyHash != "" && strings.TrimSpace(job.RequestDigest) == "") {
		return nil, false, ErrEvaluationJobInvalid
	}
	project := normalizeProjectID(job.ProjectID)
	tx, err := s.db.Begin()
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	if _, err := lockProjectForOwnedMutation(tx, project); err != nil {
		if errors.Is(err, ErrOwnedProjectNotFound) {
			return nil, false, ErrEvaluationJobProjectNotFound
		}
		return nil, false, err
	}
	if job.IdempotencyKeyHash != "" {
		existing, err := scanEvaluationJob(tx.QueryRow(`SELECT `+evaluationJobColumns+` FROM feature_evaluation_jobs
			WHERE project_id = ? AND actor = ? AND kind = ? AND idempotency_key_hash = ?`, project, job.Actor, job.Kind, job.IdempotencyKeyHash))
		if err == nil {
			if existing.RequestDigest != job.RequestDigest {
				return nil, false, ErrEvaluationJobKeyConflict
			}
			return existing, false, tx.Commit()
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, false, err
		}
	}
	var active int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM feature_evaluation_jobs WHERE project_id = ? AND state IN (?, ?)`, project, EvaluationJobQueued, EvaluationJobRunning).Scan(&active); err != nil {
		return nil, false, err
	}
	if active >= maxActivePerProject {
		return nil, false, ErrEvaluationJobQuota
	}
	now := time.Now().UTC()
	if _, err := tx.Exec(`INSERT INTO feature_evaluation_jobs (id, project_id, actor, kind, idempotency_key_hash, request_digest, request, input_fingerprint,
		state, revision, owner, lease_expires_at, progress, result, result_sha256, failure, failure_code, attempts, hidden_at, created_at, updated_at, completed_at, subject_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1, '', NULL, '', '', '', '', '', 0, NULL, ?, ?, NULL, ?)`,
		job.ID, project, job.Actor, job.Kind, job.IdempotencyKeyHash, job.RequestDigest, job.Request, job.InputFingerprint, EvaluationJobQueued, now, now, job.SubjectID); err != nil {
		return nil, false, err
	}
	created, err := scanEvaluationJob(tx.QueryRow(`SELECT `+evaluationJobColumns+` FROM feature_evaluation_jobs WHERE id = ?`, job.ID))
	if err != nil {
		return nil, false, err
	}
	return created, true, tx.Commit()
}

func (s *SQLiteStore) GetEvaluationJob(projectID, id string) (*EvaluationJob, error) {
	job, err := scanEvaluationJob(s.db.QueryRow(`SELECT `+evaluationJobColumns+` FROM feature_evaluation_jobs WHERE project_id = ? AND id = ?`, normalizeProjectID(projectID), id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrEvaluationJobNotFound
	}
	return job, err
}

// ListEvaluationJobsForSubject returns the visible job summaries of one kind
// about one subject, newest first, by index. Request and result bodies are
// left out.
func (s *SQLiteStore) ListEvaluationJobsForSubject(projectID, kind, subjectID string, limit int) ([]*EvaluationJob, error) {
	if limit < 1 || limit > 1000 || strings.TrimSpace(kind) == "" || strings.TrimSpace(subjectID) == "" {
		return nil, errors.New("invalid evaluation subject query")
	}
	rows, err := s.db.Query(`SELECT `+evaluationJobSummaryColumns+` FROM feature_evaluation_jobs
		WHERE project_id = ? AND kind = ? AND subject_id = ? AND hidden_at IS NULL ORDER BY created_at DESC, id LIMIT ?`, normalizeProjectID(projectID), kind, subjectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []*EvaluationJob{}
	for rows.Next() {
		job, err := scanEvaluationJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

// ListEvaluationJobs returns visible job summaries, newest first. Request and
// result bodies are left out (see evaluationJobSummaryColumns).
func (s *SQLiteStore) ListEvaluationJobs(projectID, kind string, limit, offset int) ([]*EvaluationJob, error) {
	if limit < 1 || limit > 100 || offset < 0 {
		return nil, errors.New("invalid evaluation job page")
	}
	query := `SELECT ` + evaluationJobSummaryColumns + ` FROM feature_evaluation_jobs WHERE project_id = ? AND hidden_at IS NULL`
	args := []any{normalizeProjectID(projectID)}
	if kind != "" {
		query += ` AND kind = ?`
		args = append(args, kind)
	}
	query += ` ORDER BY created_at DESC, id LIMIT ? OFFSET ?`
	rows, err := s.db.Query(query, append(args, limit, offset)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []*EvaluationJob{}
	for rows.Next() {
		job, err := scanEvaluationJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

// ClaimEvaluationJob takes the oldest queued job of one of kinds, or such a
// running job whose lease expired, for owner. Only kinds this controller runs
// are claimed, so a peer on another build never fails a job it cannot run. A
// job already claimed maxAttempts times without finishing expires instead,
// so a job that keeps killing its runner cannot block the queue.
func (s *SQLiteStore) ClaimEvaluationJob(owner string, now time.Time, lease time.Duration, kinds []string, maxAttempts int) (*EvaluationJob, error) {
	if strings.TrimSpace(owner) == "" || lease <= 0 || maxAttempts < 1 {
		return nil, errors.New("invalid evaluation job claim")
	}
	if len(kinds) == 0 {
		return nil, nil
	}
	now = now.UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(kinds)), ",")
	selectArgs := []any{EvaluationJobQueued, EvaluationJobRunning, now}
	for _, kind := range kinds {
		selectArgs = append(selectArgs, kind)
	}
	expired := false
	for range 64 {
		var id string
		var attempts int
		err = tx.QueryRow(`SELECT id, attempts FROM feature_evaluation_jobs WHERE (state = ? OR (state = ? AND lease_expires_at <= ?)) AND kind IN (`+placeholders+`) ORDER BY created_at, id LIMIT 1`,
			selectArgs...).Scan(&id, &attempts)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return nil, err
		}
		if attempts >= maxAttempts {
			if _, err := tx.Exec(`UPDATE feature_evaluation_jobs SET state = ?, owner = '', lease_expires_at = NULL, failure_code = ?, failure = ?,
				completed_at = ?, updated_at = ?, revision = revision + 1 WHERE id = ? AND (state = ? OR (state = ? AND lease_expires_at <= ?))`,
				EvaluationJobExpired, EvaluationFailureAttemptsExhausted, "the evaluation was claimed "+strconv.Itoa(attempts)+" times without finishing",
				now, now, id, EvaluationJobQueued, EvaluationJobRunning, now); err != nil {
				return nil, err
			}
			expired = true
			continue
		}
		result, err := tx.Exec(`UPDATE feature_evaluation_jobs SET state = ?, owner = ?, lease_expires_at = ?, attempts = attempts + 1, revision = revision + 1, updated_at = ?
			WHERE id = ? AND (state = ? OR (state = ? AND lease_expires_at <= ?))`,
			EvaluationJobRunning, owner, now.Add(lease), now, id, EvaluationJobQueued, EvaluationJobRunning, now)
		if err != nil {
			return nil, err
		}
		if n, err := result.RowsAffected(); err != nil || n != 1 {
			return nil, err
		}
		claimed, err := scanEvaluationJob(tx.QueryRow(`SELECT `+evaluationJobColumns+` FROM feature_evaluation_jobs WHERE id = ?`, id))
		if err != nil {
			return nil, err
		}
		return claimed, tx.Commit()
	}
	if expired {
		return nil, tx.Commit()
	}
	return nil, nil
}

func (s *SQLiteStore) RenewEvaluationJob(id, owner string, now time.Time, lease time.Duration) (bool, error) {
	result, err := s.db.Exec(`UPDATE feature_evaluation_jobs SET lease_expires_at = ?, updated_at = ? WHERE id = ? AND state = ? AND owner = ?`,
		now.UTC().Add(lease), now.UTC(), id, EvaluationJobRunning, owner)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

// CompleteEvaluationJobFenced publishes a terminal outcome only while owner
// still holds the running job and, when leaseName is set, while the execution
// lease term still matches. A cancelled or overtaken job records nothing.
func (s *SQLiteStore) CompleteEvaluationJobFenced(id, owner, state, result, resultSHA256, failureCode, failure string, now time.Time, leaseName, leaseOwner string, generation uint64) (bool, error) {
	if owner == "" || (state != EvaluationJobReady && state != EvaluationJobFailed) || len(result) > MaxEvaluationJobResultBytes || len(failure) > 4096 || len(failureCode) > 64 ||
		(state == EvaluationJobFailed) != (failureCode != "") {
		return false, errors.New("invalid evaluation job completion")
	}
	var tx *storeTx
	var err error
	if leaseName != "" {
		tx, err = s.beginExecutionLeaseTermTransaction(leaseName, leaseOwner, generation)
		if err == nil && tx == nil {
			return false, nil
		}
	} else {
		tx, err = s.db.Begin()
	}
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	// result_bytes is stored rather than computed so a listing never reads
	// the result body, and it counts bytes where LENGTH counts characters.
	update, err := tx.Exec(`UPDATE feature_evaluation_jobs SET state = ?, result = ?, result_sha256 = ?, result_bytes = ?, failure = ?, failure_code = ?, completed_at = ?, updated_at = ?,
		lease_expires_at = NULL, revision = revision + 1 WHERE id = ? AND state = ? AND owner = ?`,
		state, result, resultSHA256, len(result), failure, failureCode, now.UTC(), now.UTC(), id, EvaluationJobRunning, owner)
	if err != nil {
		return false, err
	}
	n, err := update.RowsAffected()
	if err != nil || n != 1 {
		return false, err
	}
	return true, tx.Commit()
}

// CancelEvaluationJob ends an unfinished job. A running owner's later
// completion then matches no row.
func (s *SQLiteStore) CancelEvaluationJob(projectID, id string, now time.Time) (bool, error) {
	result, err := s.db.Exec(`UPDATE feature_evaluation_jobs SET state = ?, owner = '', lease_expires_at = NULL, completed_at = ?, updated_at = ?, revision = revision + 1
		WHERE project_id = ? AND id = ? AND state IN (?, ?)`, EvaluationJobCancelled, now.UTC(), now.UTC(), normalizeProjectID(projectID), id, EvaluationJobQueued, EvaluationJobRunning)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

// HideEvaluationJob hides a finished job's results from listings while the
// row stays as the audit record.
func (s *SQLiteStore) HideEvaluationJob(projectID, id string, now time.Time) (bool, error) {
	result, err := s.db.Exec(`UPDATE feature_evaluation_jobs SET hidden_at = ?, updated_at = ?, revision = revision + 1
		WHERE project_id = ? AND id = ? AND hidden_at IS NULL AND state NOT IN (?, ?)`, now.UTC(), now.UTC(), normalizeProjectID(projectID), id, EvaluationJobQueued, EvaluationJobRunning)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}
