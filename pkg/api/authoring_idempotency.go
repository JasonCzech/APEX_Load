package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/keywrap"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	authoringReceiptDigestKeySetting = "security.authoring_receipt_digest_hmac_key.v1"
	authoringReceiptKeyDomain        = "apexload:authoring-receipt:key:v1"
	authoringReceiptRequestDomain    = "apexload:authoring-receipt:request:v1"
	// Credential values a create carries are fingerprinted under their own
	// key, stored only wrapped by the deployment's key wrapper (the one the
	// project secret store needs to accept those credentials at all), so a
	// database reader cannot test guesses of them offline.
	authoringReceiptCredentialKeySetting = "security.authoring_receipt_credential_hmac_key.v1"
	authoringReceiptCredentialKeyAAD     = "apexload:authoring-receipt:credential-key:v1"
	authoringReceiptCredentialDomain     = "apexload:authoring-receipt:credentials:v1"
	authoringReceiptCredentialKeyBytes   = 32
	// authoringReceiptRetention is how long a key is guaranteed to replay.
	authoringReceiptRetention = 24 * time.Hour
	// authoringReceiptLease bounds how long a crashed request holds its key
	// before a retry may resume the create.
	authoringReceiptLease = 2 * time.Minute
	// authoringReceiptVersionHeader negotiates the replay contract of a keyed
	// create. Absent or "1" is the legacy contract (a replay returns the
	// resource as it is now); "2" completes the receipt in the create's own
	// transaction and a replay returns the original outcome. Responses to a
	// keyed create echo the contract that applied.
	authoringReceiptVersionHeader = "X-APEX-Authoring-Receipt-Version"
	// authoringReceiptOutcomeHeadroom keeps the outcome envelope's own fields
	// inside the stored-outcome bound when a projection is near it.
	authoringReceiptOutcomeHeadroom = 64 << 10
)

// errAuthoringReceiptCreateUnsupported: the store cannot create a resource and
// complete an original-outcome receipt in one transaction.
var errAuthoringReceiptCreateUnsupported = errors.New("original-outcome authoring receipts are unavailable on this store")

// errAuthoringReceiptCredentialKeyUnavailable: no key wrapper is configured
// to protect credential fingerprints.
var errAuthoringReceiptCredentialKeyUnavailable = errors.New("credential fingerprints require a configured key wrapper")

// errAuthoringReceiptCredentialsUnprotected: a keyed run create carries
// credential values and no key wrapper is configured to protect their
// fingerprint.
var errAuthoringReceiptCredentialsUnprotected = errors.New("a keyed run create carrying credential values requires a configured key wrapper")

// authoringReceiptFingerprint is the request identity an Idempotency-Key
// binds. request is the caller's exact input before server-side
// normalization, with credential values replaced by references; credentials
// holds those values. legacy, when set, is the input as the digest covered
// it before credential values were fingerprinted separately.
type authoringReceiptFingerprint struct {
	request     any
	credentials map[string]string
	legacy      any
}

// legacyOrRequest is the input the request digest covered before credential
// values were fingerprinted separately.
func (fingerprint authoringReceiptFingerprint) legacyOrRequest() any {
	if fingerprint.legacy != nil {
		return fingerprint.legacy
	}
	return fingerprint.request
}

// AuthoringReceiptOutcome is the original outcome of a create made under the
// version 2 receipt contract, as committed in the create's own transaction. A
// replay returns it unchanged; it is not the resource's current state, which
// a client reads separately before editing.
type AuthoringReceiptOutcome struct {
	ReceiptVersion  int       `json:"receipt_version"`
	ResourceType    string    `json:"resource_type"`
	ResourceID      string    `json:"resource_id"`
	ProjectID       string    `json:"project_id"`
	ContentRevision string    `json:"content_revision"`
	CreatedAt       time.Time `json:"created_at"`
	// AuthoringProjection is the disclosure-safe authoring projection the
	// create committed, omitted (with ProjectionOmitted naming why) when it
	// exceeds the stored-outcome bound.
	AuthoringProjection json.RawMessage `json:"authoring_projection,omitempty"`
	ProjectionOmitted   string          `json:"authoring_projection_omitted,omitempty"`
}

// renderAuthoringReceiptOutcome renders one original outcome. The projection
// is the same disclosure-safe authoring view a reader is served, so the
// receipt never stores credentials the projection masks. A nil projection is
// one already known to exceed the bound.
func renderAuthoringReceiptOutcome(resourceType, id, projectID, revision string, createdAt time.Time, projection any) (string, error) {
	outcome := AuthoringReceiptOutcome{
		ReceiptVersion: storage.AuthoringReceiptOutcomeOriginal, ResourceType: resourceType, ResourceID: id,
		ProjectID: normalizeProjectID(projectID), ContentRevision: revision, CreatedAt: createdAt.UTC(),
	}
	outcome.ProjectionOmitted = "exceeds_receipt_outcome_bound"
	if projection != nil {
		body, err := json.Marshal(projection)
		if err != nil {
			return "", err
		}
		if len(body) <= authoringReceiptProjectionBound {
			outcome.AuthoringProjection, outcome.ProjectionOmitted = body, ""
		}
	}
	encoded, err := json.Marshal(outcome)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// authoringReceiptProjectionBound is the largest projection an outcome keeps.
const authoringReceiptProjectionBound = storage.MaxAuthoringReceiptOutcomeBytes - authoringReceiptOutcomeHeadroom

func renderScriptReceiptOutcome(snapshot *storage.ScriptAuthoringSnapshot) (string, error) {
	// The outcome renders inside the create's transaction. A script whose
	// stored content alone exceeds the bound has no projection within it, so
	// it is omitted without building and marshalling it there.
	if snapshot != nil && snapshot.Script != nil && snapshot.ContentBytes > authoringReceiptProjectionBound {
		if !storage.ValidScriptContentRevision(snapshot.ContentRevision) {
			return "", storage.ErrScriptAuthoringUnavailable
		}
		return renderAuthoringReceiptOutcome("script", snapshot.Script.ID, snapshot.Script.ProjectID, snapshot.ContentRevision, snapshot.Script.CreatedAt, nil)
	}
	stored, err := storedScriptFromAuthoringSnapshot(snapshot)
	if err != nil {
		return "", err
	}
	return renderAuthoringReceiptOutcome("script", stored.ID, stored.ProjectID, stored.ContentRevision, stored.CreatedAt, stored.AuthoringProjection)
}

func renderScenarioReceiptOutcome(snapshot *storage.ScenarioAuthoringSnapshot) (string, error) {
	projection, err := buildScenarioAuthoringProjection(snapshot)
	if err != nil {
		return "", err
	}
	return renderAuthoringReceiptOutcome("scenario", snapshot.Scenario.ID, snapshot.ProjectID, snapshot.ContentRevision, snapshot.Scenario.CreatedAt, projection)
}

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
	// completedInTransaction: the create completed an original-outcome
	// receipt in its own transaction, so there is nothing left to complete.
	completedInTransaction bool
	// outcomeUnknown keeps the receipt whatever a read shows: a commit whose
	// acknowledgement was lost may still become visible.
	outcomeUnknown bool
	// absent reads the store for the receipt's resource. It reports true only
	// when the resource definitely does not exist.
	absent func(id string) bool
	// cannotHaveCreated: the receipt's caller-chosen ID already held another
	// resource before this fresh attempt, and creates never overwrite, so
	// this request created nothing whatever is there now.
	cannotHaveCreated bool
	// requestedVersion is the receipt contract the request asked for; the
	// receipt's own OutcomeVersion (fixed by its first claim) decides replay.
	requestedVersion int
}

// originalOutcome reports whether the receipt completes inside the create's
// own transaction and replays its stored original outcome.
func (attempt *authoringCreateAttempt) originalOutcome() bool {
	return attempt != nil && attempt.receipt != nil && attempt.receipt.OutcomeVersion == storage.AuthoringReceiptOutcomeOriginal
}

// answersOriginal reports whether this request runs under the version 2
// contract: the receipt was claimed under it and the request asks for it.
func (attempt *authoringCreateAttempt) answersOriginal() bool {
	return attempt.originalOutcome() && attempt.requestedVersion == storage.AuthoringReceiptOutcomeOriginal
}

// replaysOriginal reports whether a replay returns the stored original
// outcome. A legacy client retrying a version 2 key keeps its legacy replay,
// and so does a version 2 receipt completed without an outcome (by a
// controller that predates the contract, during a rolling upgrade).
func (attempt *authoringCreateAttempt) replaysOriginal() bool {
	return attempt.answersOriginal() && attempt.receipt.State == storage.AuthoringReceiptComplete && attempt.receipt.OriginalOutcome != ""
}

// completion binds the create's transaction to this request's reservation.
func (attempt *authoringCreateAttempt) completion() storage.AuthoringReceiptCompletion {
	return storage.AuthoringReceiptCompletion{ReceiptID: attempt.receipt.ID, OwnerToken: attempt.ownerToken, Now: time.Now().UTC()}
}

// markAuthoringReceiptVersion echoes the receipt contract that applied to the
// keyed create this request performed: 2 when its transaction recorded the
// original outcome for a request that asked for it.
func markAuthoringReceiptVersion(w http.ResponseWriter, attempt *authoringCreateAttempt) {
	if attempt == nil {
		return
	}
	setAuthoringReceiptVersion(w, attempt.answersOriginal())
}

func setAuthoringReceiptVersion(w http.ResponseWriter, original bool) {
	if original {
		w.Header().Set(authoringReceiptVersionHeader, "2")
		return
	}
	w.Header().Set(authoringReceiptVersionHeader, "1")
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
	if attempt.completedInTransaction {
		return
	}
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
	if (attempt.attempted || attempt.resumed) && !attempt.cannotHaveCreated {
		if attempt.absent == nil || !attempt.absent(attempt.receipt.ResourceID) {
			return
		}
	}
	if _, err := attempt.store.AbandonAuthoringMutationReceipt(attempt.receipt.ID, attempt.ownerToken); err != nil {
		slog.Warn("authoring create receipt not released", "purpose", attempt.receipt.Purpose, "error", err)
	}
}

// adoptsExisting reports whether an object already stored under the
// receipt's ID must be this request's own earlier, unconfirmed create. The ID
// is derived from the keyed digest, so nothing else can be there. A pending
// original-outcome receipt names such an object only when a controller that
// predates the contract created it during a rolling upgrade; adopting it
// completes the receipt without an outcome, and replays stay legacy.
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

// claimAuthoringCreate claims the request's Idempotency-Key, if any. The
// fingerprint must describe the caller's exact input before server-side
// normalization, with any caller-chosen resource ID included; requestedID is
// that ID or "".
func (s *Server) claimAuthoringCreate(w http.ResponseWriter, r *http.Request, purpose, resourceType, projectID, requestedID string, request authoringReceiptFingerprint) (*authoringCreateAttempt, authoringCreateOutcome) {
	raw := r.Header.Get("Idempotency-Key")
	if strings.TrimSpace(raw) == "" && len(r.Header.Values("Idempotency-Key")) == 0 {
		return nil, authoringCreateProceed
	}
	requestedVersion, versionOK := authoringReceiptVersionRequested(r)
	if !versionOK {
		writeAuthoringIdempotencyError(w, http.StatusBadRequest, "idempotency_receipt_version_unsupported", "X-APEX-Authoring-Receipt-Version must be 1 or 2")
		return nil, authoringCreateHandled
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
	if _, atomic := s.store.(storage.AuthoringReceiptCreateStore); requestedVersion == storage.AuthoringReceiptOutcomeOriginal && !atomic {
		writeAuthoringIdempotencyError(w, http.StatusServiceUnavailable, "idempotency_receipt_version_unavailable", "original-outcome receipts are unavailable on this controller; retry with receipt version 1 for a replay of the current resource")
		return nil, authoringCreateHandled
	}
	identity, err := s.authoringReceiptIdentity(r, purpose, projectID, key, requestedID, request)
	if errors.Is(err, errAuthoringReceiptCredentialsUnprotected) {
		// A definitive refusal before the key is claimed: nothing was created.
		writeAuthoringIdempotencyError(w, http.StatusUnprocessableEntity, "idempotency_credentials_unprotected", "a keyed run create cannot carry credential values on a controller without key wrapping; use secret references for them, or configure key wrapping")
		return nil, authoringCreateHandled
	}
	if err != nil {
		writeAuthoringIdempotencyError(w, http.StatusServiceUnavailable, "idempotency_unavailable", "idempotency receipts are unavailable")
		return nil, authoringCreateHandled
	}
	owner := uuid.NewString()
	identity.ResourceType, identity.OwnerToken, identity.OutcomeVersion = resourceType, owner, requestedVersion
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
	attempt := &authoringCreateAttempt{store: store, receipt: claim.Receipt, ownerToken: owner, derivedID: identity.derivedID, resumed: !claim.Created && !claim.Replay, requestedVersion: requestedVersion}
	if claim.Replay {
		attempt.completed = true
		return attempt, authoringCreateReplay
	}
	return attempt, authoringCreateProceed
}

// authoringReceiptVersionRequested reads the requested receipt contract:
// legacy when absent. Anything but exactly one "1" or "2" is refused, so an
// unknown later contract is never served as an older one.
func authoringReceiptVersionRequested(r *http.Request) (int, bool) {
	values := r.Header.Values(authoringReceiptVersionHeader)
	if len(values) == 0 {
		return storage.AuthoringReceiptOutcomeLegacy, true
	}
	if len(values) != 1 {
		return 0, false
	}
	switch strings.TrimSpace(values[0]) {
	case "1":
		return storage.AuthoringReceiptOutcomeLegacy, true
	case "2":
		return storage.AuthoringReceiptOutcomeOriginal, true
	default:
		return 0, false
	}
}

type authoringReceiptIdentity struct {
	storage.AuthoringMutationReceiptClaimInput
	derivedID bool
}

// authoringReceiptIdentity computes the keyed digests and the fixed resource
// ID for one key. Digests are HMACs under a per-deployment key kept in the
// settings table, so a receipt row alone cannot be used to test guesses of a
// key or request body. That key is not wrapped, so where a key wrapper is
// configured credential values never enter its digest: they are fingerprinted
// under a second key stored only wrapped (authoringReceiptCredentialKey), and
// the request digest covers that fingerprint and the request with references
// in their place. Without a wrapper (a project secret store that does not use
// one) a scenario create's digest covers the raw request as it did before
// this scheme, and a reader of the whole database could test guesses of those
// values offline. Run creates were never digested before it, so a keyed run
// create that carries credential values is refused instead.
func (s *Server) authoringReceiptIdentity(r *http.Request, purpose, projectID, key, requestedID string, request authoringReceiptFingerprint) (authoringReceiptIdentity, error) {
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
	var requestDigest, legacyDigest string
	if len(request.credentials) == 0 {
		requestDigest, err = directImportHMAC(digestKey, authoringReceiptRequestDomain, request.request)
	} else if credentialKey, keyErr := s.authoringReceiptCredentialKey(r.Context()); errors.Is(keyErr, errAuthoringReceiptCredentialKeyUnavailable) {
		if purpose == storage.AuthoringPurposeTestCreate {
			return authoringReceiptIdentity{}, errAuthoringReceiptCredentialsUnprotected
		}
		requestDigest, err = directImportHMAC(digestKey, authoringReceiptRequestDomain, request.legacyOrRequest())
	} else if keyErr != nil {
		return authoringReceiptIdentity{}, keyErr
	} else {
		credentials, digestErr := directImportHMAC(credentialKey, authoringReceiptCredentialDomain, request.credentials)
		if digestErr != nil {
			return authoringReceiptIdentity{}, digestErr
		}
		requestDigest, err = directImportHMAC(digestKey, authoringReceiptRequestDomain, struct {
			Request     any    `json:"request"`
			Credentials string `json:"credentials"`
		}{request.request, credentials})
		if err == nil {
			// Receipts claimed before credential values were fingerprinted
			// separately (or while no wrapper was configured) still
			// recognize their own retries.
			legacyDigest, err = directImportHMAC(digestKey, authoringReceiptRequestDomain, request.legacyOrRequest())
		}
	}
	if err != nil {
		return authoringReceiptIdentity{}, err
	}
	identity := authoringReceiptIdentity{AuthoringMutationReceiptClaimInput: storage.AuthoringMutationReceiptClaimInput{
		ProjectID: projectID, Actor: actor, Purpose: purpose, KeyHash: keyHash, RequestDigest: requestDigest,
		LegacyRequestDigest: legacyDigest, ResourceID: strings.TrimSpace(requestedID), Retention: authoringReceiptRetention,
	}}
	if identity.ResourceID == "" {
		identity.derivedID = true
		identity.ResourceID = uuid.NewSHA1(uuid.NameSpaceURL, []byte("apexload:authoring-create:"+keyHash)).String()
	}
	return identity, nil
}

// authoringReceiptCredentialKey returns the key credential values are
// fingerprinted under. It is generated once per deployment and kept in the
// settings table only wrapped by the configured key wrapper. Without one it
// reports errAuthoringReceiptCredentialKeyUnavailable (see
// authoringReceiptIdentity); a wrapper whose key the store cannot initialize
// atomically is an error, never a reason to digest the values unprotected.
func (s *Server) authoringReceiptCredentialKey(ctx context.Context) ([]byte, error) {
	s.receiptCredentialKeyMu.Lock()
	defer s.receiptCredentialKeyMu.Unlock()
	if len(s.receiptCredentialKey) == authoringReceiptCredentialKeyBytes {
		return s.receiptCredentialKey, nil
	}
	wrapper := s.clusterSecretWrapper
	if wrapper == nil || s.store == nil {
		return nil, errAuthoringReceiptCredentialKeyUnavailable
	}
	cas, supported := s.store.(settingsCompareAndSwapper)
	if !supported {
		return nil, errors.New("authoring receipt credential key cannot be initialized atomically on this store")
	}
	aad := []byte(authoringReceiptCredentialKeyAAD)
	raw, err := s.store.GetSetting(authoringReceiptCredentialKeySetting)
	if err != nil {
		return nil, fmt.Errorf("load authoring receipt credential key: %w", err)
	}
	if strings.TrimSpace(raw) == "" {
		generated := make([]byte, authoringReceiptCredentialKeyBytes)
		if _, err := rand.Read(generated); err != nil {
			return nil, fmt.Errorf("generate authoring receipt credential key: %w", err)
		}
		wrapped, err := wrapper.WrapKey(ctx, generated, aad)
		clear(generated)
		if err != nil {
			return nil, fmt.Errorf("wrap authoring receipt credential key: %w", err)
		}
		encoded, err := json.Marshal(wrapped)
		if err != nil {
			return nil, err
		}
		// Whichever replica wins the swap defines the key; every replica,
		// this one included, then unwraps the stored value.
		if _, err := cas.CompareAndSwapSetting(authoringReceiptCredentialKeySetting, "", string(encoded)); err != nil {
			return nil, fmt.Errorf("initialize authoring receipt credential key: %w", err)
		}
		if raw, err = s.store.GetSetting(authoringReceiptCredentialKeySetting); err != nil {
			return nil, fmt.Errorf("reload authoring receipt credential key: %w", err)
		}
	}
	var wrapped keywrap.WrappedKey
	if err := json.Unmarshal([]byte(raw), &wrapped); err != nil {
		return nil, errors.New("authoring receipt credential key is invalid")
	}
	key, err := wrapper.UnwrapKey(ctx, wrapped, aad)
	if err != nil {
		return nil, fmt.Errorf("unwrap authoring receipt credential key: %w", err)
	}
	if len(key) != authoringReceiptCredentialKeyBytes {
		return nil, errors.New("authoring receipt credential key is invalid")
	}
	s.receiptCredentialKey = key
	return key, nil
}

// runtimeCredentialReceiptFingerprint is the request identity of a create
// whose root and group runtime settings may carry credential values. They
// are fingerprinted under the wrapped credential key, never under the request
// digest's key; a request without any keeps the digest it always had. rebuild
// returns a copy of the request carrying the protected settings.
func runtimeCredentialReceiptFingerprint(request any, root *RuntimeSettingsOverride, groups []CreateGroupEntry, rebuild func(*RuntimeSettingsOverride, []CreateGroupEntry) any) (authoringReceiptFingerprint, error) {
	const scope = "authoring-receipt"
	credentials := map[string]string{}
	collect := func(settings *RuntimeSettingsOverride, group int) (*RuntimeSettingsOverride, error) {
		projected, values, _, err := protectLegacyRuntimeOverrideSecrets(scope, group, nil, settings)
		if err != nil {
			return nil, err
		}
		for name, value := range values {
			credentials[name] = value
		}
		return projected, nil
	}
	protectedRoot, err := collect(root, -1)
	if err != nil {
		return authoringReceiptFingerprint{}, err
	}
	protectedGroups := append([]CreateGroupEntry(nil), groups...)
	for index := range protectedGroups {
		if protectedGroups[index].RuntimeSettings, err = collect(groups[index].RuntimeSettings, index); err != nil {
			return authoringReceiptFingerprint{}, err
		}
	}
	if len(credentials) == 0 {
		return authoringReceiptFingerprint{request: request}, nil
	}
	return authoringReceiptFingerprint{request: rebuild(protectedRoot, protectedGroups), credentials: credentials, legacy: request}, nil
}

// scenarioCreateReceiptFingerprint is a scenario create's request identity.
func scenarioCreateReceiptFingerprint(req CreateScenarioRequest) (authoringReceiptFingerprint, error) {
	return runtimeCredentialReceiptFingerprint(req, req.RuntimeSettings, req.Groups, func(root *RuntimeSettingsOverride, groups []CreateGroupEntry) any {
		protected := req
		protected.RuntimeSettings, protected.Groups = root, groups
		return protected
	})
}

// testCreateReceiptFingerprint is a run create's request identity.
func testCreateReceiptFingerprint(req CreateTestRequest) (authoringReceiptFingerprint, error) {
	return runtimeCredentialReceiptFingerprint(req, req.RuntimeSettings, req.Groups, func(root *RuntimeSettingsOverride, groups []CreateGroupEntry) any {
		protected := req
		protected.RuntimeSettings, protected.Groups = root, groups
		return protected
	})
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
	if attempt != nil {
		setAuthoringReceiptVersion(w, attempt.replaysOriginal())
	}
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
		// same statement, and only its own tombstone time; the delete that
		// wins renews its tombstone at a fresh time once it commits, so a
		// refused delete racing it cannot leave the resource untombstoned.
		if err := store.RestoreAuthoringMutationReceiptTombstones(resourceType, resourceID, "deleted", at); err != nil {
			slog.Warn("authoring receipt tombstone not withdrawn after a refused delete", "resource_type", resourceType, "resource_id", resourceID, "error", err)
		}
	}, nil
}

// writeAuthoringReceiptCreateError answers the two failures of a create that
// completes its receipt in its own transaction. Either way nothing committed.
func writeAuthoringReceiptCreateError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, storage.ErrAuthoringReceiptLost):
		// Another attempt resumed the reservation after this one's lease
		// expired, or the key's replay lifetime ended first; a retry sees
		// that attempt's outcome or the expired key.
		w.Header().Set("Retry-After", "2")
		writeAuthoringIdempotencyError(w, http.StatusConflict, "idempotency_request_in_progress", "this create no longer holds its Idempotency-Key (another request took it over, or it expired); retry to see the key's outcome")
		return true
	case errors.Is(err, errAuthoringReceiptCreateUnsupported):
		writeAuthoringIdempotencyError(w, http.StatusServiceUnavailable, "idempotency_receipt_version_unavailable", "original-outcome receipts are unavailable on this controller; retry with receipt version 1 for a replay of the current resource")
		return true
	}
	return false
}

// replayOriginalAuthoringOutcome answers a retried version 2 create with the
// outcome its own transaction recorded. The resource must still exist and
// remain in the caller's project: a deleted original is never returned, and
// the outcome is never refreshed from the current resource.
func (s *Server) replayOriginalAuthoringOutcome(w http.ResponseWriter, r *http.Request, attempt *authoringCreateAttempt, exists func(string) (bool, error)) {
	var outcome AuthoringReceiptOutcome
	if err := json.Unmarshal([]byte(attempt.receipt.OriginalOutcome), &outcome); err != nil || outcome.ReceiptVersion != storage.AuthoringReceiptOutcomeOriginal ||
		outcome.ResourceID != attempt.resourceID() || outcome.ResourceType != attempt.receipt.ResourceType {
		writeAuthoringIdempotencyError(w, http.StatusServiceUnavailable, "idempotency_unavailable", "the original outcome could not be read; retry later")
		return
	}
	if expected, scoped := authorizedProjectFromRequest(r); (scoped && normalizeProjectID(outcome.ProjectID) != expected) ||
		normalizeProjectID(outcome.ProjectID) != normalizeProjectID(attempt.receipt.ProjectID) {
		writeError(w, http.StatusNotFound, outcome.ResourceType+" not found")
		return
	}
	found, err := exists(outcome.ResourceID)
	if err != nil {
		writeAuthoringIdempotencyError(w, http.StatusServiceUnavailable, "idempotency_unavailable", "the original "+outcome.ResourceType+" could not be read; retry later")
		return
	}
	if !found {
		writeAuthoringIdempotencyError(w, http.StatusGone, "idempotency_resource_deleted", "the resource created with this Idempotency-Key was deleted and will not be recreated")
		return
	}
	markAuthoringReplay(w, attempt)
	writeJSON(w, http.StatusCreated, outcome)
}

// replayScriptCreate answers a retried or resumed script create with the
// script the original request created.
func (s *Server) replayScriptCreate(w http.ResponseWriter, r *http.Request, attempt *authoringCreateAttempt) {
	if attempt.replaysOriginal() {
		s.replayOriginalAuthoringOutcome(w, r, attempt, func(id string) (bool, error) {
			stored, found, err := s.runner.GetScriptAuthoringDurable(id)
			return found && stored != nil, err
		})
		return
	}
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
	if attempt.replaysOriginal() {
		s.replayOriginalAuthoringOutcome(w, r, attempt, s.durableScenarioExists)
		return
	}
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
	response := maskStoredScenarioForEditor(scenario)
	// The replay carries the current validator only when this replica's copy
	// is the durable state; otherwise the client reads view=authoring first.
	if reader, supported := s.store.(storage.ScenarioAuthoringStore); s.store != nil && supported {
		if snapshot, readErr := reader.GetScenarioAuthoringSnapshot(scenario.ID); readErr == nil {
			attachScenarioAuthoringValidator(w, response, scenario, snapshot)
		}
	}
	writeJSON(w, http.StatusCreated, response)
}

// replayTestCreate answers a retried run create with the run the original
// request created, as it is now (receipt contract 1).
func (s *Server) replayTestCreate(w http.ResponseWriter, r *http.Request, attempt *authoringCreateAttempt) {
	test, found := s.runner.GetTest(attempt.resourceID())
	if !found || test == nil {
		absent := s.durableTestAbsent(attempt.resourceID())
		if absent {
			writeAuthoringIdempotencyError(w, http.StatusGone, "idempotency_resource_deleted", "the resource created with this Idempotency-Key was deleted and will not be recreated")
			return
		}
		writeAuthoringIdempotencyError(w, http.StatusServiceUnavailable, "idempotency_unavailable", "the original run is not yet visible on this controller; retry later")
		return
	}
	if expected, scoped := authorizedProjectFromRequest(r); scoped && normalizeProjectID(test.ProjectID) != expected {
		writeError(w, http.StatusNotFound, "test not found")
		return
	}
	attempt.complete("")
	markAuthoringReplay(w, attempt)
	writeJSON(w, http.StatusCreated, maskManagedTest(test))
}

// durableTestAbsent reports true only when the store proves the run does not
// exist.
func (s *Server) durableTestAbsent(id string) bool {
	if _, found := s.runner.GetTest(id); found {
		return false
	}
	store := s.runner.testStore
	if store == nil {
		return true
	}
	_, err := store.GetTest(id)
	return errors.Is(err, sql.ErrNoRows)
}

// durableTestPresent reports true only when a run is known to exist at id.
func (s *Server) durableTestPresent(id string) bool {
	if _, found := s.runner.GetTest(id); found {
		return true
	}
	if s.runner.testStore == nil {
		return false
	}
	record, err := s.runner.testStore.GetTest(id)
	return err == nil && record != nil
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

// retombstoneAuthoringReceipts tombstones a deleted resource's receipts again,
// at a fresh time, after its delete committed or when its outcome is unknown.
// A concurrent refused delete may have withdrawn the tombstone this delete
// relied on, or may still be withdrawing it: its restore matches only its own
// tombstone time, so the fresh one survives it.
func (s *Server) retombstoneAuthoringReceipts(resourceType, resourceID string) {
	store, supported := s.store.(storage.AuthoringMutationReceiptStore)
	if s.store == nil || !supported {
		return
	}
	if err := store.RenewAuthoringMutationReceiptTombstones(resourceType, resourceID, "deleted", time.Now().UTC().Truncate(time.Microsecond)); err != nil {
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
