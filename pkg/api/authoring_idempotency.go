package api

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	authoringReceiptDigestKeySetting = "security.authoring_receipt_digest_hmac_key.v1"
	authoringReceiptKeyDomain        = "apexload:authoring-receipt:key:v1"
	authoringReceiptRequestDomain    = "apexload:authoring-receipt:request:v1"
	// authoringReceiptRetention is how long a key is guaranteed to replay.
	authoringReceiptRetention = 24 * time.Hour
	// authoringReceiptLease bounds how long a crashed request holds its key
	// before a retry may resume the create.
	authoringReceiptLease = 2 * time.Minute
)

// authoringCreateOutcome says what the handler should do after claiming.
type authoringCreateOutcome int

const (
	// authoringCreateProceed: no key was presented, or the receipt was
	// reserved (or resumed) and the create should run with ResourceID.
	authoringCreateProceed authoringCreateOutcome = iota
	// authoringCreateReplay: the create already completed; return the
	// resource named by the receipt instead of creating anything.
	authoringCreateReplay
	// authoringCreateHandled: a response was already written.
	authoringCreateHandled
)

// authoringCreateAttempt is one claimed Idempotency-Key for an ordinary
// script or scenario create. The resource ID is fixed by the receipt, so a
// retry after a lost response creates (or finds) exactly the same object.
type authoringCreateAttempt struct {
	store      storage.AuthoringMutationReceiptStore
	receipt    *storage.AuthoringMutationReceipt
	ownerToken string
	// derivedID means the resource ID came from the key, not from the caller,
	// so an existing object with that ID can only be this request's result.
	derivedID bool
	// resumed means a stale pending receipt was taken over: an earlier
	// attempt may have created the object without completing the receipt.
	resumed bool
	// attempted is set once the durable create is called. From then on a
	// failure may have committed, so the receipt is abandoned only after a
	// durable read proves the resource absent.
	attempted bool
	completed bool
	// outcomeUnknown keeps the receipt whatever a read shows: a commit whose
	// acknowledgement was lost may still become visible.
	outcomeUnknown bool
	// absent reads the store for the receipt's resource. It reports true only
	// when the resource definitely does not exist.
	absent func(id string) bool
}

func (attempt *authoringCreateAttempt) resourceID() string {
	if attempt == nil || attempt.receipt == nil {
		return ""
	}
	return attempt.receipt.ResourceID
}

// markAttempted records that the durable create is about to run.
func (attempt *authoringCreateAttempt) markAttempted() {
	if attempt != nil {
		attempt.attempted = true
	}
}

// complete records the created resource's revision. A failed completion is
// logged, not surfaced: the object exists, and a retry resumes the pending
// receipt and adopts it by its fixed ID.
func (attempt *authoringCreateAttempt) complete(revision string) {
	if attempt == nil || attempt.completed {
		return
	}
	attempt.completed = true
	done, err := attempt.store.CompleteAuthoringMutationReceipt(attempt.receipt.ID, attempt.ownerToken, revision, time.Now().UTC())
	if err != nil || !done {
		slog.Warn("authoring create receipt not completed; a retry will adopt the created resource", "purpose", attempt.receipt.Purpose, "resource_id", attempt.receipt.ResourceID, "error", err)
	}
}

// fail records the create's error; an unknown commit outcome pins the
// receipt so a retry adopts whatever committed.
func (attempt *authoringCreateAttempt) fail(err error) {
	if attempt != nil && errors.Is(err, storage.ErrScenarioAuthoringOutcomeUnknown) {
		attempt.outcomeUnknown = true
	}
}

// release abandons a receipt whose create definitely did not happen, so the
// same key can be retried with a corrected request, and frees the lease of
// one whose outcome is unknown. Deferred by handlers.
// Before the durable create is called this request cannot have created
// anything; afterwards, and for a receipt resumed from an earlier attempt
// that may have committed, only a durable read proving the resource absent
// allows the key to be reused.
func (attempt *authoringCreateAttempt) release() {
	if attempt == nil || attempt.completed {
		return
	}
	if attempt.outcomeUnknown {
		// The create may have committed: keep the receipt, but let the next
		// retry resume it now rather than wait out this request's lease.
		if _, err := attempt.store.ReleaseAuthoringMutationReceiptLease(attempt.receipt.ID, attempt.ownerToken); err != nil {
			slog.Warn("authoring create receipt lease not released", "purpose", attempt.receipt.Purpose, "error", err)
		}
		return
	}
	if attempt.attempted || attempt.resumed {
		if attempt.absent == nil || !attempt.absent(attempt.receipt.ResourceID) {
			return
		}
	}
	if _, err := attempt.store.AbandonAuthoringMutationReceipt(attempt.receipt.ID, attempt.ownerToken); err != nil {
		slog.Warn("authoring create receipt not released", "purpose", attempt.receipt.Purpose, "error", err)
	}
}

// adoptsExisting reports whether an object already stored under the
// receipt's ID must be this request's own earlier, unconfirmed create.
func (attempt *authoringCreateAttempt) adoptsExisting() bool {
	return attempt != nil && attempt.derivedID && attempt.resumed
}

func authoringReceiptActor(r *http.Request, s *Server) string {
	actorType, actorSubject, _ := s.resolveAuditActor(r)
	actorType, actorSubject = strings.TrimSpace(actorType), strings.TrimSpace(actorSubject)
	if actorType == "" && actorSubject == "" {
		return "anonymous"
	}
	return actorType + ":" + actorSubject
}

// claimAuthoringCreate claims the request's Idempotency-Key, if any. request
// must be the caller's exact input before server-side normalization, with any
// caller-chosen resource ID included; requestedID is that ID or "".
func (s *Server) claimAuthoringCreate(w http.ResponseWriter, r *http.Request, purpose, resourceType, projectID, requestedID string, request any) (*authoringCreateAttempt, authoringCreateOutcome) {
	raw := r.Header.Get("Idempotency-Key")
	if strings.TrimSpace(raw) == "" && len(r.Header.Values("Idempotency-Key")) == 0 {
		return nil, authoringCreateProceed
	}
	if len(r.Header.Values("Idempotency-Key")) != 1 {
		writeAuthoringIdempotencyError(w, http.StatusBadRequest, "idempotency_key_invalid", "exactly one Idempotency-Key header is required")
		return nil, authoringCreateHandled
	}
	key, err := validateCIRunIdempotencyKey(raw)
	if err != nil {
		writeAuthoringIdempotencyError(w, http.StatusBadRequest, "idempotency_key_invalid", err.Error())
		return nil, authoringCreateHandled
	}
	store, supported := s.store.(storage.AuthoringMutationReceiptStore)
	if s.store == nil || !supported {
		// Ignoring the key would let a retry silently create a duplicate.
		writeAuthoringIdempotencyError(w, http.StatusServiceUnavailable, "idempotency_unavailable", "durable idempotent creates are unavailable on this controller; retry without Idempotency-Key only if a duplicate is acceptable")
		return nil, authoringCreateHandled
	}
	identity, err := s.authoringReceiptIdentity(r, purpose, projectID, key, requestedID, request)
	if err != nil {
		writeAuthoringIdempotencyError(w, http.StatusServiceUnavailable, "idempotency_unavailable", "idempotency receipts are unavailable")
		return nil, authoringCreateHandled
	}
	owner := uuid.NewString()
	identity.ResourceType, identity.OwnerToken = resourceType, owner
	claim, err := store.ClaimAuthoringMutationReceipt(identity.AuthoringMutationReceiptClaimInput, time.Now().UTC(), authoringReceiptLease)
	switch {
	case errors.Is(err, storage.ErrAuthoringReceiptConflict):
		writeAuthoringIdempotencyError(w, http.StatusConflict, "idempotency_key_reused", "Idempotency-Key was already used with a different request")
		return nil, authoringCreateHandled
	case errors.Is(err, storage.ErrAuthoringReceiptExpired):
		writeAuthoringIdempotencyError(w, http.StatusConflict, "idempotency_key_expired", "Idempotency-Key has expired; reload before deciding whether to create again with a new key")
		return nil, authoringCreateHandled
	case errors.Is(err, storage.ErrAuthoringReceiptTombstoned):
		writeAuthoringIdempotencyError(w, http.StatusGone, "idempotency_resource_deleted", "the resource created with this Idempotency-Key was deleted and will not be recreated")
		return nil, authoringCreateHandled
	case errors.Is(err, storage.ErrAuthoringReceiptBusy):
		w.Header().Set("Retry-After", "2")
		writeAuthoringIdempotencyError(w, http.StatusConflict, "idempotency_request_in_progress", "a request with this Idempotency-Key is still in progress")
		return nil, authoringCreateHandled
	case err != nil || claim == nil || claim.Receipt == nil:
		writeAuthoringIdempotencyError(w, http.StatusServiceUnavailable, "idempotency_unavailable", "idempotency receipts are unavailable")
		return nil, authoringCreateHandled
	}
	attempt := &authoringCreateAttempt{store: store, receipt: claim.Receipt, ownerToken: owner, derivedID: identity.derivedID, resumed: !claim.Created && !claim.Replay}
	if claim.Replay {
		attempt.completed = true
		return attempt, authoringCreateReplay
	}
	return attempt, authoringCreateProceed
}

type authoringReceiptIdentity struct {
	storage.AuthoringMutationReceiptClaimInput
	derivedID bool
}

// authoringReceiptIdentity computes the keyed digests and the fixed resource
// ID for one key. Digests are HMACs under a per-deployment key kept in the
// settings table, so a receipt row alone cannot be used to test guesses of a
// key or request body. The key is not wrapped: a reader of the whole database
// holds both, and could test guesses offline (including runtime credentials
// entered with a scenario create, which the digest covers).
func (s *Server) authoringReceiptIdentity(r *http.Request, purpose, projectID, key, requestedID string, request any) (authoringReceiptIdentity, error) {
	digestKey, err := s.persistentDigestKey(authoringReceiptDigestKeySetting, "authoring receipt")
	if err != nil {
		return authoringReceiptIdentity{}, err
	}
	projectID = normalizeProjectID(projectID)
	actor := authoringReceiptActor(r, s)
	keyHash, err := directImportHMAC(digestKey, authoringReceiptKeyDomain, []string{projectID, actor, purpose, key})
	if err != nil {
		return authoringReceiptIdentity{}, err
	}
	requestDigest, err := directImportHMAC(digestKey, authoringReceiptRequestDomain, request)
	if err != nil {
		return authoringReceiptIdentity{}, err
	}
	identity := authoringReceiptIdentity{AuthoringMutationReceiptClaimInput: storage.AuthoringMutationReceiptClaimInput{
		ProjectID: projectID, Actor: actor, Purpose: purpose, KeyHash: keyHash, RequestDigest: requestDigest,
		ResourceID: strings.TrimSpace(requestedID), Retention: authoringReceiptRetention,
	}}
	if identity.ResourceID == "" {
		identity.derivedID = true
		identity.ResourceID = uuid.NewSHA1(uuid.NameSpaceURL, []byte("apexload:authoring-create:"+keyHash)).String()
	}
	return identity, nil
}

// AuthoringIdempotencyError carries a fixed code for clients deciding whether
// to retry, reload or stop.
type AuthoringIdempotencyError struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

func writeAuthoringIdempotencyError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, AuthoringIdempotencyError{Error: message, Code: code})
}

// markAuthoringReplay labels a response that returns an earlier create's
// resource. The body is the resource as it is now; the receipt's revision is
// the revision the original create produced.
func markAuthoringReplay(w http.ResponseWriter, attempt *authoringCreateAttempt) {
	w.Header().Set("Idempotent-Replayed", "true")
	if attempt != nil && attempt.receipt != nil && attempt.receipt.ResourceRevision != "" {
		w.Header().Set("X-APEX-Original-Content-Revision", attempt.receipt.ResourceRevision)
	}
}

// tombstoneAuthoringReceipts must run before a script or scenario is deleted,
// so a retry of the request that created it cannot recreate it. The returned
// restore withdraws exactly these tombstones; handlers call it when the delete
// was definitely refused, so a live resource is not reported as deleted. The
// restore applies only while the resource still exists: a delete that lost to
// a concurrent delete is refused too, and that resource is gone. If the
// restore itself fails, the tombstone stays and the create key answers 410
// until it expires: refusing a retry is safe, recreating a resource is not.
func (s *Server) tombstoneAuthoringReceipts(resourceType, resourceID string) (func(), error) {
	store, supported := s.store.(storage.AuthoringMutationReceiptStore)
	if s.store == nil || !supported {
		return func() {}, nil
	}
	at := time.Now().UTC().Truncate(time.Microsecond)
	if err := store.TombstoneAuthoringMutationReceipts(resourceType, resourceID, "deleted", at); err != nil {
		return nil, err
	}
	return func() {
		// The store restores only while the resource still exists, in the
		// same statement, so a refused delete never strips the tombstone of
		// a resource a concurrent delete removed.
		if err := store.RestoreAuthoringMutationReceiptTombstones(resourceType, resourceID, "deleted", at); err != nil {
			slog.Warn("authoring receipt tombstone not withdrawn after a refused delete", "resource_type", resourceType, "resource_id", resourceID, "error", err)
		}
	}, nil
}

// replayScriptCreate answers a retried or resumed script create with the
// script the original request created.
func (s *Server) replayScriptCreate(w http.ResponseWriter, r *http.Request, attempt *authoringCreateAttempt) {
	stored, found, err := s.runner.GetScriptAuthoringDurable(attempt.resourceID())
	if err != nil {
		writeAuthoringIdempotencyError(w, http.StatusServiceUnavailable, "idempotency_unavailable", "the original script could not be read; retry later")
		return
	}
	if !found || stored == nil {
		writeAuthoringIdempotencyError(w, http.StatusGone, "idempotency_resource_deleted", "the resource created with this Idempotency-Key was deleted and will not be recreated")
		return
	}
	if !requireScriptAuthoringProjectMatch(w, r, stored.ProjectID) {
		return
	}
	// A completed receipt already holds the original revision. Adopting an
	// unconfirmed create cannot know it: the script may have been edited since.
	attempt.complete("")
	markAuthoringReplay(w, attempt)
	writeJSON(w, http.StatusCreated, viewStoredScript(stored))
}

// replayScenarioCreate answers a retried or resumed scenario create with the
// scenario the original request created. Existence is decided by a durable
// read, so a replica whose cache has not caught up answers 503, never 410.
func (s *Server) replayScenarioCreate(w http.ResponseWriter, r *http.Request, attempt *authoringCreateAttempt) {
	exists, err := s.durableScenarioExists(attempt.resourceID())
	if err != nil {
		writeAuthoringIdempotencyError(w, http.StatusServiceUnavailable, "idempotency_unavailable", "the original scenario could not be read; retry later")
		return
	}
	if !exists {
		writeAuthoringIdempotencyError(w, http.StatusGone, "idempotency_resource_deleted", "the resource created with this Idempotency-Key was deleted and will not be recreated")
		return
	}
	scenario, found := s.runner.GetScenario(attempt.resourceID())
	if !found || scenario == nil {
		s.runner.refreshScenariosAfterDurableMismatch()
		scenario, found = s.runner.GetScenario(attempt.resourceID())
	}
	if !found || scenario == nil {
		writeAuthoringIdempotencyError(w, http.StatusServiceUnavailable, "idempotency_unavailable", "the original scenario is not yet visible on this controller; retry later")
		return
	}
	if expected, scoped := authorizedProjectFromRequest(r); scoped && normalizeProjectID(scenario.ProjectID) != expected {
		writeError(w, http.StatusNotFound, "scenario not found")
		return
	}
	attempt.complete("")
	markAuthoringReplay(w, attempt)
	response := maskStoredScenario(scenario)
	// The replay carries the current validator only when this replica's copy
	// is the durable state; otherwise the client reads view=authoring first.
	if reader, supported := s.store.(storage.ScenarioAuthoringStore); s.store != nil && supported {
		if snapshot, readErr := reader.GetScenarioAuthoringSnapshot(scenario.ID); readErr == nil {
			attachScenarioAuthoringValidator(w, response, scenario, snapshot)
		}
	}
	writeJSON(w, http.StatusCreated, response)
}

// durableScenarioExists reads the store, not this replica's cache. Stores
// without durable authoring snapshots fall back to the cache.
func (s *Server) durableScenarioExists(id string) (bool, error) {
	reader, supported := s.store.(storage.ScenarioAuthoringStore)
	if s.store == nil || !supported {
		_, found := s.runner.GetScenario(id)
		return found, nil
	}
	_, err := reader.GetScenarioAuthoringSnapshot(id)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, storage.ErrScenarioAuthoringNotFound), errors.Is(err, sql.ErrNoRows):
		return false, nil
	default:
		return false, err
	}
}

// retombstoneAuthoringReceipts tombstones a deleted resource's receipts again
// after its delete committed. A concurrent refused delete may have withdrawn
// the tombstone this delete relied on before the delete committed.
func (s *Server) retombstoneAuthoringReceipts(resourceType, resourceID string) {
	store, supported := s.store.(storage.AuthoringMutationReceiptStore)
	if s.store == nil || !supported {
		return
	}
	if err := store.TombstoneAuthoringMutationReceipts(resourceType, resourceID, "deleted", time.Now().UTC().Truncate(time.Microsecond)); err != nil {
		slog.Warn("authoring receipt tombstone not renewed after a delete", "resource_type", resourceType, "resource_id", resourceID, "error", err)
	}
}

// durableScenarioAbsent reports true only when the store proves the scenario
// does not exist.
func (s *Server) durableScenarioAbsent(id string) bool {
	exists, err := s.durableScenarioExists(id)
	return err == nil && !exists
}

// durableScriptAbsent reports true only when the store proves the script does
// not exist.
func (s *Server) durableScriptAbsent(id string) bool {
	stored, found, err := s.runner.GetScriptAuthoringDurable(id)
	return err == nil && (!found || stored == nil)
}
