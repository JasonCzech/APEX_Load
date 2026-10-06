package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	evaluationJobLease = 2 * time.Minute
	// One heavy evaluation runs per controller at a time.
	evaluationJobSlots = 1
	// At most this many evaluations may be queued or running per project.
	evaluationJobsActivePerProject = 20
	defaultEvaluationJobDeadline   = 10 * time.Minute
	// A job claimed this many times without finishing (each runner died or
	// lost its lease) expires instead of blocking the queue.
	evaluationJobMaxAttempts = 3

	evaluationReceiptDigestKeySetting = "security.evaluation_request_digest_hmac_key.v1"
	evaluationRequestDigestDomain     = "apexload:evaluation-job:request:v1"
)

// evaluationKind is one kind of bounded analytical evaluation. Kinds register
// themselves (browser intelligence, telemetry acceptance, native derived
// analysis, bounded export) as their work packages land; a request for an
// unregistered kind is refused.
type evaluationKind struct {
	Name string
	// Prepare validates the request and returns its input fingerprint, which
	// must cover every input the result depends on (runs, artifacts, policy
	// and catalog versions) so an idempotent retry can be told apart from a
	// changed request.
	Prepare func(s *Server, projectID string, request json.RawMessage) (string, error)
	// Run computes the bounded result. ctx ends on cancellation, lease loss,
	// shutdown or the kind's deadline.
	Run      func(ctx context.Context, s *Server, job *storage.EvaluationJob) (any, error)
	Deadline time.Duration
}

var evaluationKinds = struct {
	sync.RWMutex
	byName map[string]evaluationKind
}{byName: map[string]evaluationKind{}}

func registerEvaluationKind(kind evaluationKind) {
	evaluationKinds.Lock()
	defer evaluationKinds.Unlock()
	evaluationKinds.byName[kind.Name] = kind
}

func lookupEvaluationKind(name string) (evaluationKind, bool) {
	evaluationKinds.RLock()
	defer evaluationKinds.RUnlock()
	kind, ok := evaluationKinds.byName[name]
	return kind, ok
}

func registeredEvaluationKinds() []string {
	evaluationKinds.RLock()
	defer evaluationKinds.RUnlock()
	names := make([]string, 0, len(evaluationKinds.byName))
	for name := range evaluationKinds.byName {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// EvaluationJobRequest creates an evaluation. Request is the kind's typed
// input; it never carries SQL, code or helper commands.
type EvaluationJobRequest struct {
	Kind    string          `json:"kind"`
	Request json.RawMessage `json:"request"`
}

type EvaluationJobResponse struct {
	ID               string          `json:"id"`
	ProjectID        string          `json:"project_id"`
	Kind             string          `json:"kind"`
	Actor            string          `json:"actor"`
	State            string          `json:"state"`
	Revision         int64           `json:"revision"`
	Request          json.RawMessage `json:"request,omitempty"`
	InputFingerprint string          `json:"input_fingerprint"`
	Result           json.RawMessage `json:"result,omitempty"`
	ResultSHA256     string          `json:"result_sha256,omitempty"`
	ResultBytes      int64           `json:"result_bytes,omitempty"`
	Failure          string          `json:"failure,omitempty"`
	FailureCode      string          `json:"failure_code,omitempty"`
	Attempts         int             `json:"attempts"`
	Hidden           bool            `json:"hidden,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
	CompletedAt      *time.Time      `json:"completed_at,omitempty"`
}

// evaluationJobResponse is the full job, request and result included; a
// listing uses evaluationJobSummary.
func evaluationJobResponse(job *storage.EvaluationJob) EvaluationJobResponse {
	response := evaluationJobSummary(job)
	if job.Request != "" {
		response.Request = json.RawMessage(job.Request)
	}
	if job.Result != "" {
		response.Result = json.RawMessage(job.Result)
	}
	return response
}

// evaluationJobSummary leaves the request and result bodies out; their hash
// and size identify them, and GET {id} returns them.
func evaluationJobSummary(job *storage.EvaluationJob) EvaluationJobResponse {
	return EvaluationJobResponse{
		ID: job.ID, ProjectID: job.ProjectID, Kind: job.Kind, Actor: job.Actor, State: job.State, Revision: job.Revision,
		InputFingerprint: job.InputFingerprint, ResultSHA256: job.ResultSHA256, ResultBytes: job.ResultBytes,
		Failure: job.Failure, FailureCode: job.FailureCode, Attempts: job.Attempts, Hidden: job.HiddenAt != nil,
		CreatedAt: job.CreatedAt, UpdatedAt: job.UpdatedAt, CompletedAt: job.CompletedAt,
	}
}

func (s *Server) evaluationJobStore(w http.ResponseWriter) (storage.EvaluationJobStore, bool) {
	store, ok := s.store.(storage.EvaluationJobStore)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "evaluation jobs unavailable")
	}
	return store, ok
}

// callerManagesProjectEvaluations reports whether the caller may cancel or
// hide other actors' evaluations: editors and administrators may; viewers
// manage only their own.
func (s *Server) callerManagesProjectEvaluations(r *http.Request, projectID string) bool {
	user, ok := AuthenticatedUserFromContext(r.Context())
	if !ok || user == nil {
		if _, service := AuthenticatedServiceAccountFromContext(r.Context()); service {
			return false
		}
		// Authentication is disabled (development or bypass mode).
		return true
	}
	for _, role := range []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin} {
		if _, ok := user.GlobalRoles[role]; ok {
			return true
		}
	}
	_, granted := userProjectRoleGrantSource(s.store, user, projectID, RoleEditor, RoleProjectAdmin)
	return granted
}

func (s *Server) handleEvaluationJobs(w http.ResponseWriter, r *http.Request) {
	project := normalizeProjectID(r.URL.Query().Get("project_id"))
	store, ok := s.evaluationJobStore(w)
	if !ok {
		return
	}
	id := r.PathValue("id")
	switch {
	case r.Method == http.MethodGet && id == "":
		kind := r.URL.Query().Get("kind")
		limit, offset := parseLimitOffsetQuery(r, 50)
		jobs, err := store.ListEvaluationJobs(project, kind, min(limit, 100), offset)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "cannot list evaluation jobs")
			return
		}
		responses := make([]EvaluationJobResponse, 0, len(jobs))
		for _, job := range jobs {
			responses = append(responses, evaluationJobSummary(job))
		}
		writeJSON(w, http.StatusOK, map[string]any{"jobs": responses, "kinds": registeredEvaluationKinds()})
	case r.Method == http.MethodGet:
		job, err := store.GetEvaluationJob(project, id)
		if err != nil {
			writeError(w, http.StatusNotFound, "evaluation job not found")
			return
		}
		writeJSON(w, http.StatusOK, evaluationJobResponse(job))
	case r.Method == http.MethodDelete:
		s.deleteEvaluationJob(w, r, store, project, id)
	case r.Method == http.MethodPost:
		s.createEvaluationJob(w, r, store, project)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) createEvaluationJob(w http.ResponseWriter, r *http.Request, store storage.EvaluationJobStore, project string) {
	r.Body = http.MaxBytesReader(w, r.Body, storage.MaxEvaluationJobRequestBytes+1024)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "evaluation request exceeds its byte limit")
			return
		}
		writeError(w, http.StatusBadRequest, "cannot read evaluation request")
		return
	}
	var req EvaluationJobRequest
	if err := decodeAnalysisDefinitionJSON(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	kind, known := lookupEvaluationKind(req.Kind)
	if !known {
		available := registeredEvaluationKinds()
		message := "no evaluation kinds are available on this controller"
		if len(available) > 0 {
			message = "kind must be one of " + strings.Join(available, ", ")
		}
		writeError(w, http.StatusBadRequest, message)
		return
	}
	if len(req.Request) == 0 || len(req.Request) > storage.MaxEvaluationJobRequestBytes || !json.Valid(req.Request) {
		writeError(w, http.StatusBadRequest, "evaluation request must be bounded JSON")
		return
	}
	fingerprint, err := kind.Prepare(s, project, req.Request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	actor := authoringReceiptActor(r, s)
	keyHash, requestDigest := "", ""
	if values := r.Header.Values("Idempotency-Key"); len(values) > 0 {
		key, err := validateCIRunIdempotencyKey(strings.Join(values, ","))
		if err != nil || len(values) != 1 {
			writeAuthoringIdempotencyError(w, http.StatusBadRequest, "idempotency_key_invalid", "exactly one valid Idempotency-Key is required when present")
			return
		}
		digest := sha256.Sum256([]byte("apexload:evaluation-job:" + project + "\x00" + actor + "\x00" + req.Kind + "\x00" + key))
		keyHash = hex.EncodeToString(digest[:])
		// The key is judged against the request itself, not the input
		// fingerprint: inputs may advance between a lost response and its
		// identical retry (a run still ingesting telemetry).
		digestKey, keyErr := s.persistentDigestKey(evaluationReceiptDigestKeySetting, "evaluation request")
		if keyErr == nil {
			requestDigest, keyErr = directImportHMAC(digestKey, evaluationRequestDigestDomain, []any{req.Kind, req.Request})
		}
		if keyErr != nil {
			writeAuthoringIdempotencyError(w, http.StatusServiceUnavailable, "idempotency_unavailable", "evaluation idempotency is unavailable")
			return
		}
	}
	job, created, err := store.CreateEvaluationJob(&storage.EvaluationJob{
		ID: uuid.NewString(), ProjectID: project, Actor: actor, Kind: req.Kind, IdempotencyKeyHash: keyHash,
		RequestDigest: requestDigest, Request: string(req.Request), InputFingerprint: fingerprint,
	}, evaluationJobsActivePerProject)
	switch {
	case errors.Is(err, storage.ErrEvaluationJobKeyConflict):
		writeAuthoringIdempotencyError(w, http.StatusConflict, "idempotency_key_reused", "Idempotency-Key was already used with a different evaluation request")
		return
	case errors.Is(err, storage.ErrEvaluationJobQuota):
		writeError(w, http.StatusTooManyRequests, fmt.Sprintf("at most %d evaluations may be queued or running per project", evaluationJobsActivePerProject))
		return
	case errors.Is(err, storage.ErrEvaluationJobProjectNotFound):
		writeError(w, http.StatusNotFound, "project not found")
		return
	case errors.Is(err, storage.ErrEvaluationJobInvalid):
		writeError(w, http.StatusInternalServerError, "evaluation kind produced an invalid job")
		return
	case err != nil:
		writeError(w, http.StatusServiceUnavailable, "cannot queue evaluation")
		return
	}
	if created {
		s.recordAuditEvent(r, "evaluation.create", "success", "evaluation_job", job.ID, project, map[string]any{"kind": job.Kind})
		s.wakeEvaluationWorker()
	} else {
		w.Header().Set("Idempotent-Replayed", "true")
	}
	writeJSON(w, http.StatusAccepted, evaluationJobResponse(job))
}

// deleteEvaluationJob cancels an evaluation; with hide=true it hides a
// finished one from listings. Both are idempotent: a job already in the
// requested end state is returned unchanged, so a retried cancel never hides
// the job and a retried hide never fails. The row stays as the audit record;
// input runs and datasets are never deleted.
func (s *Server) deleteEvaluationJob(w http.ResponseWriter, r *http.Request, store storage.EvaluationJobStore, project, id string) {
	hide := false
	switch r.URL.Query().Get("hide") {
	case "", "false":
	case "true":
		hide = true
	default:
		writeError(w, http.StatusBadRequest, "hide must be true or false")
		return
	}
	job, err := store.GetEvaluationJob(project, id)
	if err != nil {
		writeError(w, http.StatusNotFound, "evaluation job not found")
		return
	}
	if job.Actor != authoringReceiptActor(r, s) && !s.callerManagesProjectEvaluations(r, project) {
		writeError(w, http.StatusForbidden, "only the creator or a project editor may cancel or hide this evaluation")
		return
	}
	now := time.Now()
	action := "evaluation.cancel"
	changed := false
	switch {
	case hide && job.Unfinished():
		writeError(w, http.StatusConflict, "cancel the evaluation before hiding it")
		return
	case hide && job.HiddenAt == nil:
		action = "evaluation.hide"
		changed, err = store.HideEvaluationJob(project, id, now)
	case !hide && job.Unfinished():
		changed, err = store.CancelEvaluationJob(project, id, now)
		if changed {
			if jobs := s.evaluationJobRunner(); jobs != nil {
				jobs.cancelRunning(id)
			}
		}
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "cannot update evaluation")
		return
	}
	if changed {
		s.recordAuditEvent(r, action, "success", "evaluation_job", id, project, map[string]any{"kind": job.Kind})
	}
	current, err := store.GetEvaluationJob(project, id)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "cannot read evaluation")
		return
	}
	// A job that finished between the read and the cancel has the end state
	// the caller asked for; any other unchanged outcome is a real conflict.
	if !hide && current.Unfinished() || hide && current.HiddenAt == nil {
		writeError(w, http.StatusConflict, "evaluation changed; reload it")
		return
	}
	writeJSON(w, http.StatusOK, evaluationJobResponse(current))
}

// evaluationJobRunner returns the shared leased-job runner for evaluations,
// or nil when the store keeps none.
func (s *Server) evaluationJobRunner() *leasedJobRunner[storage.EvaluationJob] {
	store, ok := s.store.(storage.EvaluationJobStore)
	if !ok {
		return nil
	}
	s.evaluationJobsOnce.Do(func() {
		s.evaluationJobs = &leasedJobRunner[storage.EvaluationJob]{
			name: "evaluation job", lease: evaluationJobLease, slots: evaluationJobSlots,
			idOf: func(job *storage.EvaluationJob) string { return job.ID },
			claim: func(owner string, now time.Time, lease time.Duration) (*storage.EvaluationJob, error) {
				// Only kinds this controller can run are claimed; a job of a kind
				// registered only on a newer peer waits for that peer.
				return store.ClaimEvaluationJob(owner, now, lease, registeredEvaluationKinds(), evaluationJobMaxAttempts)
			},
			renew: store.RenewEvaluationJob,
			run: func(ctx context.Context, job *storage.EvaluationJob, owner string, term executionLeaseTerm) error {
				return s.runEvaluationJob(ctx, store, job, owner, term)
			},
			fail: func(job *storage.EvaluationJob, owner string, term executionLeaseTerm, message string) error {
				return s.completeEvaluationJob(store, job, owner, term, storage.EvaluationJobFailed, "", storage.EvaluationFailurePanic, message)
			},
		}
	})
	return s.evaluationJobs
}

func (s *Server) wakeEvaluationWorker() {
	if jobs := s.evaluationJobRunner(); jobs != nil {
		jobs.wakeUp()
	}
}

func (s *Server) startEvaluationWorker() {
	jobs := s.evaluationJobRunner()
	if jobs == nil || s.runner == nil {
		return
	}
	s.evaluationCancel = jobs.start(s)
}

// runNextEvaluationJob claims and runs one queued evaluation.
func (s *Server) runNextEvaluationJob(ctx context.Context) (bool, error) {
	jobs := s.evaluationJobRunner()
	if jobs == nil {
		return false, nil
	}
	return jobs.runNext(s, ctx, nil)
}

func (s *Server) runEvaluationJob(ctx context.Context, store storage.EvaluationJobStore, job *storage.EvaluationJob, owner string, term executionLeaseTerm) error {
	kind, known := lookupEvaluationKind(job.Kind)
	if !known {
		// Claims are limited to registered kinds, so this is a kind that was
		// unregistered while the job was claimed. Leave the lease to expire so
		// a controller that runs the kind can take the job.
		return nil
	}
	deadline := kind.Deadline
	if deadline <= 0 {
		deadline = defaultEvaluationJobDeadline
	}
	runCtx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	result, runErr := kind.Run(runCtx, s, job)
	if runErr != nil && ctx.Err() != nil {
		// Shutdown, cancellation or a lost lease: record nothing.
		return nil
	}
	if runErr != nil {
		code := storage.EvaluationFailureRunError
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			code, runErr = storage.EvaluationFailureDeadline, fmt.Errorf("evaluation exceeded its %s deadline", deadline)
		}
		return s.completeEvaluationJob(store, job, owner, term, storage.EvaluationJobFailed, "", code, boundedFailure(runErr.Error()))
	}
	body, err := json.Marshal(result)
	if err != nil {
		return s.completeEvaluationJob(store, job, owner, term, storage.EvaluationJobFailed, "", storage.EvaluationFailureResultEncoding, boundedFailure("evaluation result could not be encoded: "+err.Error()))
	}
	if len(body) > storage.MaxEvaluationJobResultBytes {
		return s.completeEvaluationJob(store, job, owner, term, storage.EvaluationJobFailed, "", storage.EvaluationFailureResultTooLarge, "evaluation result exceeds the stored size limit")
	}
	return s.completeEvaluationJob(store, job, owner, term, storage.EvaluationJobReady, string(body), "", "")
}

func (s *Server) completeEvaluationJob(store storage.EvaluationJobStore, job *storage.EvaluationJob, owner string, term executionLeaseTerm, state, result, failureCode, failure string) error {
	digest := ""
	if result != "" {
		sum := sha256.Sum256([]byte(result))
		digest = hex.EncodeToString(sum[:])
	}
	leaseName, leaseOwner, generation := s.fencedLease(term)
	// A cancelled job, an overtaken owner or a lost term records nothing.
	_, err := store.CompleteEvaluationJobFenced(job.ID, owner, state, result, digest, failureCode, failure, time.Now(), leaseName, leaseOwner, generation)
	return err
}

func boundedFailure(message string) string {
	if len(message) <= 1024 {
		return message
	}
	cut := 1024
	for cut > 0 && !utf8.RuneStart(message[cut]) {
		cut--
	}
	return message[:cut] + "…"
}
