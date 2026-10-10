package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/importsession"
)

const (
	importCandidateSessionTTL      = 30 * time.Minute
	importCandidateSessionMaxBytes = importsession.MaxPayloadBytes
	importCandidateSessionVersion  = 3
	importCandidateClaimRenewEvery = 10 * time.Second
)

// The protected store requires a durable timestamp for capacity and claim
// bookkeeping. This sentinel represents an operator-selected non-expiring
// draft while keeping legacy descriptor validation unchanged.
var importCandidateNoExpiration = time.Date(9999, time.December, 31, 23, 59, 59, 0, time.UTC)

type importCandidateEnvelope struct {
	Version                int                                    `json:"version"`
	Profile                string                                 `json:"profile"`
	ProjectID              string                                 `json:"project_id"`
	Script                 dsl.Script                             `json:"script"`
	CredentialRequirements []importCandidateCredentialRequirement `json:"credential_requirements,omitempty"`
	// RawSource is the unmodified uploaded document, retained so a committed
	// script can be traced back to what was actually imported.
	//
	// It is carried here rather than written as an artifact at inspect time
	// because most inspections are never committed: writing then would leave an
	// orphaned encrypted object for every abandoned import. The session object is
	// already encrypted at rest and is removed by its configured draft lifecycle,
	// so deferring the artifact write to commit avoids an extra durable copy.
	//
	// This is unredacted source and must be treated as credential material: it is
	// cleared after use, never logged, and never returned in a response. A v1
	// payload has no such field and decodes with RawSource empty, which disables
	// retention for that session -- the safe direction during an upgrade.
	RawSource          []byte    `json:"raw_source,omitempty"`
	RawSourceFormat    string    `json:"raw_source_format,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
	ExpiresAt          time.Time `json:"expires_at"`
	ExpirationDisabled bool      `json:"expiration_disabled,omitempty"`
}

// importCandidateSource is the uploaded document a caller wants retained.
// A zero value disables retention for that import.
type importCandidateSource struct {
	Bytes  []byte
	Format string
}

// importCandidateCredentialRequirement is intentionally scoped to the
// encrypted candidate payload. credentialpolicy.Requirement.Value has json:"-";
// this envelope is the one explicit codec allowed to serialize that value.
type importCandidateCredentialRequirement struct {
	Requirement credentialpolicy.Requirement `json:"requirement"`
	Value       string                       `json:"value"`
}

type protectedImportCandidate struct {
	ExpiresAt              time.Time
	ExpirationDisabled     bool
	Script                 *dsl.Script
	CredentialRequirements []credentialpolicy.Requirement
	// RawSource is unredacted uploaded source. Treat it like a credential:
	// clearProtectedImportCandidate zeroes it, and it must never reach a
	// response body or a log line.
	RawSource       []byte
	RawSourceFormat string
}

func (s *Server) protectedImportSessionManager() (*importsession.Manager, error) {
	if s == nil {
		return nil, errors.New("protected import session service is unavailable")
	}
	objects, err := s.brunoArtifactStore()
	if err != nil {
		return nil, err
	}
	s.brunoArtifactMu.Lock()
	defer s.brunoArtifactMu.Unlock()
	if s.importSessions != nil {
		return s.importSessions, nil
	}
	manager, err := importsession.NewManager(objects)
	if err != nil {
		return nil, err
	}
	s.importSessions = manager
	return manager, nil
}

func (s *Server) registerImportCandidateSession(r *http.Request, profile string, script *dsl.Script) (string, time.Time, error) {
	return s.registerImportCandidateSessionWithRequirements(r, profile, script, nil, importCandidateSource{})
}

func (s *Server) registerImportCandidateSessionWithExpirationPolicy(r *http.Request, profile string, script *dsl.Script, disableExpiration bool) (string, time.Time, error) {
	return s.registerImportCandidateSessionInternal(r, profile, script, nil, importCandidateSource{}, disableExpiration)
}

func (s *Server) registerImportCandidateSessionWithRequirements(
	r *http.Request,
	profile string,
	script *dsl.Script,
	requirements []credentialpolicy.Requirement,
	source importCandidateSource,
) (string, time.Time, error) {
	return s.registerImportCandidateSessionInternal(r, profile, script, requirements, source, false)
}

func (s *Server) registerImportCandidateSessionInternal(
	r *http.Request,
	profile string,
	script *dsl.Script,
	requirements []credentialpolicy.Requirement,
	source importCandidateSource,
	disableExpiration bool,
) (string, time.Time, error) {
	if r == nil || script == nil {
		return "", time.Time{}, errors.New("protected import candidate is required")
	}
	profile = strings.ToLower(strings.TrimSpace(profile))
	if profile == "" {
		return "", time.Time{}, errors.New("protected import candidate profile is required")
	}
	present, err := s.brunoArtifactStorePresent()
	if err != nil {
		return "", time.Time{}, markProtectedImportStoreUnavailable(err)
	}
	if !present {
		// Protected sessions are additive. Deployments without an envelope-key
		// configuration retain the established client-submitted script flow.
		return "", time.Time{}, nil
	}
	projectID := normalizeProjectID(script.ProjectID)
	if authorized, scoped := authorizedProjectFromRequest(r); scoped && authorized != projectID {
		return "", time.Time{}, os.ErrNotExist
	}
	_, actor, _ := s.resolveAuditActor(r)
	now := time.Now().UTC()
	publicExpiresAt := now.Add(importCandidateSessionTTL)
	if expiry, ok := r.Context().Value(importBatchExpiryContextKey{}).(time.Time); ok {
		if disableExpiration || !expiry.After(now) || expiry.After(publicExpiresAt) {
			return "", time.Time{}, errors.New("invalid protected batch expiry")
		}
		publicExpiresAt = expiry
	}
	storedExpiresAt := publicExpiresAt
	if disableExpiration {
		publicExpiresAt = time.Time{}
		storedExpiresAt = importCandidateNoExpiration
	}
	envelopeVersion := 2
	if disableExpiration {
		envelopeVersion = importCandidateSessionVersion
	}
	envelope := importCandidateEnvelope{
		Version:            envelopeVersion,
		Profile:            profile,
		ProjectID:          projectID,
		Script:             *script,
		CreatedAt:          now,
		ExpiresAt:          storedExpiresAt,
		ExpirationDisabled: disableExpiration,
	}
	if len(source.Bytes) > 0 {
		envelope.RawSource = source.Bytes
		envelope.RawSourceFormat = strings.ToLower(strings.TrimSpace(source.Format))
	}
	payload, err := marshalImportCandidateEnvelope(envelope, requirements)
	if err != nil {
		return "", time.Time{}, err
	}
	defer clear(payload)
	if int64(len(payload)) > importCandidateSessionMaxBytes {
		return "", time.Time{}, fmt.Errorf("protected import candidate exceeds %d-byte limit", importCandidateSessionMaxBytes)
	}
	manager, err := s.protectedImportSessionManager()
	if err != nil {
		return "", time.Time{}, markProtectedImportStoreUnavailable(err)
	}
	id := uuid.NewString()
	err = manager.Save(r.Context(), importsession.SessionDescriptor{
		ID:                 id,
		Profile:            profile,
		ProjectID:          projectID,
		Actor:              actor,
		ExpiresAt:          storedExpiresAt,
		ExpirationDisabled: disableExpiration,
	}, payload, importCandidateSessionMaxBytes)
	if err != nil {
		return "", time.Time{}, markProtectedImportStoreUnavailable(err)
	}
	return id, publicExpiresAt, nil
}

func (s *Server) claimImportCandidateSession(
	r *http.Request,
	profile string,
	sessionID string,
	projectID string,
) (*dsl.Script, *importsession.Claim, error) {
	candidate, claim, err := s.claimProtectedImportCandidateSession(r, profile, sessionID, projectID)
	if err != nil {
		return nil, nil, err
	}
	script := candidate.Script
	clearProtectedImportCandidate(candidate)
	return script, claim, nil
}

func (s *Server) claimProtectedImportCandidateSession(
	r *http.Request,
	profile string,
	sessionID string,
	projectID string,
) (*protectedImportCandidate, *importsession.Claim, error) {
	manager, scope, err := s.protectedImportCandidateScope(r, profile, sessionID, projectID)
	if err != nil {
		return nil, nil, err
	}
	claimed, err := manager.Claim(r.Context(), scope, importCandidateSessionMaxBytes)
	if err != nil {
		return nil, nil, err
	}
	defer clear(claimed.Payload)

	candidate, err := decodeProtectedImportCandidate(claimed.Payload, scope.Profile, scope.ProjectID, true)
	if err != nil {
		if releaseErr := manager.Release(claimed.Claim); releaseErr != nil {
			// Report the cleanup failure without wrapping it: its sentinel (for
			// example ErrSessionClaimed) describes the release, not this
			// candidate, and must not turn an invalid draft into a busy one.
			return nil, nil, errors.Join(err, fmt.Errorf("release protected import candidate claim: %v", releaseErr))
		}
		return nil, nil, err
	}
	return candidate, &claimed.Claim, nil
}

// openProtectedImportCandidateSession reads a protected candidate without
// leasing it. Read-only inspection must not take the one-shot consume lease:
// the dashboard inspects every rotated candidate while the operator saves it,
// and an inspection holding that lease made the concurrent save fail as an
// invalid draft. A candidate a consumer has already claimed reads as absent.
func (s *Server) openProtectedImportCandidateSession(
	r *http.Request,
	profile string,
	sessionID string,
	projectID string,
) (*protectedImportCandidate, error) {
	manager, scope, err := s.protectedImportCandidateScope(r, profile, sessionID, projectID)
	if err != nil {
		return nil, err
	}
	payload, _, err := manager.Open(r.Context(), scope, importCandidateSessionMaxBytes)
	if err != nil {
		return nil, err
	}
	defer clear(payload)
	return decodeProtectedImportCandidate(payload, scope.Profile, scope.ProjectID, false)
}

// protectedImportCandidateScope resolves the caller's session scope and
// rejects an enterprise candidate that a guided repair already replaced.
func (s *Server) protectedImportCandidateScope(
	r *http.Request,
	profile string,
	sessionID string,
	projectID string,
) (*importsession.Manager, importsession.Scope, error) {
	if r == nil {
		return nil, importsession.Scope{}, errors.New("request is required")
	}
	profile = strings.ToLower(strings.TrimSpace(profile))
	sessionID = strings.TrimSpace(sessionID)
	projectID = normalizeProjectID(projectID)
	_, actor, _ := s.resolveAuditActor(r)
	manager, err := s.protectedImportSessionManager()
	if err != nil {
		return nil, importsession.Scope{}, err
	}
	if profile == enterpriseArchiveSaveProfile {
		if receipt, _, receiptErr := manager.Open(r.Context(), importsession.Scope{ID: repairReceiptID(sessionID), Profile: repairReceiptProfile, ProjectID: projectID, Actor: actor}, importCandidateSessionMaxBytes); receiptErr == nil {
			clear(receipt)
			return nil, importsession.Scope{}, errors.New("candidate was superseded by repair")
		} else if !errors.Is(receiptErr, os.ErrNotExist) {
			return nil, importsession.Scope{}, receiptErr
		}
	}
	return manager, importsession.Scope{
		ID:        sessionID,
		Profile:   profile,
		ProjectID: projectID,
		Actor:     actor,
	}, nil
}

// decodeProtectedImportCandidate validates a decrypted candidate envelope
// against the requested scope. The caller still owns and clears payload.
// Read-only callers pass retainRawSource=false so they never copy the
// unredacted upload they do not use.
func decodeProtectedImportCandidate(
	payload []byte,
	profile string,
	projectID string,
	retainRawSource bool,
) (*protectedImportCandidate, error) {
	envelope, requirements, err := unmarshalImportCandidateEnvelope(payload)
	if err != nil {
		return nil, errors.New("protected import candidate is invalid")
	}
	// Accept every version this build understands, not just the current one. A
	// session is claimed by whichever binary is running when the user finishes
	// reviewing, so pinning to the newest version would fail every import that
	// was still open across a deploy. v1 simply carries no retained source.
	if !supportedImportCandidateSessionVersion(envelope.Version) ||
		!strings.EqualFold(strings.TrimSpace(envelope.Profile), profile) ||
		normalizeProjectID(envelope.ProjectID) != projectID {
		clearCredentialRequirementValues(requirements)
		return nil, errors.New("protected import candidate scope is invalid")
	}
	if !envelope.ExpirationDisabled && !envelope.ExpiresAt.After(time.Now().UTC()) {
		clearCredentialRequirementValues(requirements)
		return nil, importsession.ErrSessionExpired
	}
	script := envelope.Script
	// The payload is cleared by the caller, and RawSource aliases into it after
	// json.Unmarshal, so the bytes must be copied out before that happens or
	// the caller would see a zeroed buffer.
	var rawSource []byte
	if retainRawSource {
		rawSource = append([]byte(nil), envelope.RawSource...)
	}
	clear(envelope.RawSource)
	return &protectedImportCandidate{
		Script:                 &script,
		CredentialRequirements: requirements,
		RawSource:              rawSource,
		RawSourceFormat:        envelope.RawSourceFormat,
		ExpiresAt:              envelope.ExpiresAt,
		ExpirationDisabled:     envelope.ExpirationDisabled,
	}, nil
}

// supportedImportCandidateSessionVersion reports whether this build can decode a
// stored candidate envelope. Versions are additive: v2 added retained raw
// source and v3 added the explicit non-expiring-draft policy. Older payloads
// remain valid and use their original time-limited behavior.
func supportedImportCandidateSessionVersion(version int) bool {
	return version >= 1 && version <= importCandidateSessionVersion
}

func marshalImportCandidateEnvelope(
	envelope importCandidateEnvelope,
	requirements []credentialpolicy.Requirement,
) ([]byte, error) {
	protected := make([]importCandidateCredentialRequirement, 0, len(requirements))
	defer clearImportCandidateEnvelopeCredentials(protected)
	cloned := credentialpolicy.CloneRequirements(requirements)
	defer clearCredentialRequirementValues(cloned)
	seen := make(map[string]struct{}, len(requirements))
	for index := range cloned {
		requirement := cloned[index]
		value, recoverable := credentialpolicy.RecoverableSourceCredentialValue(requirement.Value)
		cloned[index].Value = ""
		requirement.Value = ""
		if !recoverable || requirement.ExistingReference {
			continue
		}
		id := strings.TrimSpace(requirement.ID)
		if id == "" {
			return nil, errors.New("protected credential requirement id is required")
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, errors.New("protected credential requirement ids must be unique")
		}
		seen[id] = struct{}{}
		protected = append(protected, importCandidateCredentialRequirement{
			Requirement: requirement,
			Value:       value,
		})
	}
	envelope.CredentialRequirements = protected
	return json.Marshal(envelope)
}

func unmarshalImportCandidateEnvelope(payload []byte) (importCandidateEnvelope, []credentialpolicy.Requirement, error) {
	var envelope importCandidateEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return importCandidateEnvelope{}, nil, err
	}
	defer clearImportCandidateEnvelopeCredentials(envelope.CredentialRequirements)
	requirements := make([]credentialpolicy.Requirement, 0, len(envelope.CredentialRequirements))
	seen := make(map[string]struct{}, len(envelope.CredentialRequirements))
	for index := range envelope.CredentialRequirements {
		protected := &envelope.CredentialRequirements[index]
		id := strings.TrimSpace(protected.Requirement.ID)
		value, recoverable := credentialpolicy.RecoverableSourceCredentialValue(protected.Value)
		if id == "" || !recoverable || protected.Requirement.ExistingReference {
			clearCredentialRequirementValues(requirements)
			return importCandidateEnvelope{}, nil, errors.New("protected credential requirement is invalid")
		}
		if _, duplicate := seen[id]; duplicate {
			clearCredentialRequirementValues(requirements)
			return importCandidateEnvelope{}, nil, errors.New("protected credential requirement ids must be unique")
		}
		seen[id] = struct{}{}
		requirement := protected.Requirement
		requirement.Value = value
		requirement.SourceValueAvailable = true
		requirement.Targets = append([]credentialpolicy.TargetRef(nil), requirement.Targets...)
		requirements = append(requirements, requirement)
	}
	return envelope, requirements, nil
}

func clearImportCandidateEnvelopeCredentials(requirements []importCandidateCredentialRequirement) {
	for index := range requirements {
		requirements[index].Value = ""
	}
}

func clearCredentialRequirementValues(requirements []credentialpolicy.Requirement) {
	for index := range requirements {
		requirements[index].Value = ""
	}
}

func clearProtectedImportCandidate(candidate *protectedImportCandidate) {
	if candidate == nil {
		return
	}
	clearCredentialRequirementValues(candidate.CredentialRequirements)
	candidate.CredentialRequirements = nil
	clear(candidate.RawSource)
	candidate.RawSource = nil
}

func (s *Server) releaseImportCandidateClaim(claim *importsession.Claim) {
	if s == nil || claim == nil || s.importSessions == nil {
		return
	}
	_ = s.importSessions.Release(*claim)
}

func (s *Server) completeImportCandidateClaim(ctx context.Context, claim *importsession.Claim) error {
	if s == nil || claim == nil {
		return nil
	}
	manager, err := s.protectedImportSessionManager()
	if err != nil {
		return err
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	// Completion must remain fenced to this exact lease epoch. The store marks
	// the claim consumed before deleting anything, so if cleanup then fails the
	// committed session is never recovered into a claimable draft; the store's
	// janitor finishes deleting it. Only if that first write fails does the
	// claim stay unmarked, and then stale-claim recovery can at worst replay the
	// generic flow's deterministic create-only script.
	return manager.Complete(cleanupCtx, *claim)
}

type importCandidateClaimLease struct {
	manager  *importsession.Manager
	claim    importsession.Claim
	done     chan struct{}
	finished chan struct{}
	stopOnce sync.Once
	errMu    sync.Mutex
	lostErr  error
}

func (s *Server) startImportCandidateClaimLease(claim *importsession.Claim) *importCandidateClaimLease {
	lease := &importCandidateClaimLease{
		done:     make(chan struct{}),
		finished: make(chan struct{}),
	}
	if claim != nil {
		lease.claim = *claim
	}
	manager, err := s.protectedImportSessionManager()
	if err != nil || claim == nil {
		if err == nil {
			err = errors.New("protected import session claim is required")
		}
		lease.lostErr = err
		close(lease.finished)
		return lease
	}
	lease.manager = manager
	go func() {
		defer close(lease.finished)
		ticker := time.NewTicker(importCandidateClaimRenewEvery)
		defer ticker.Stop()
		for {
			select {
			case <-lease.done:
				return
			case now := <-ticker.C:
				if err := lease.touch(now); err != nil {
					return
				}
			}
		}
	}()
	return lease
}

func (l *importCandidateClaimLease) touch(now time.Time) error {
	if l == nil || l.manager == nil {
		return errors.New("protected import session claim lease is unavailable")
	}
	l.errMu.Lock()
	if l.lostErr != nil {
		err := l.lostErr
		l.errMu.Unlock()
		return err
	}
	l.errMu.Unlock()
	if err := l.manager.Renew(l.claim, now.UTC()); err != nil {
		l.errMu.Lock()
		if l.lostErr == nil {
			l.lostErr = fmt.Errorf("protected import session claim lease lost: %w", err)
		}
		err = l.lostErr
		l.errMu.Unlock()
		return err
	}
	return nil
}

func (l *importCandidateClaimLease) assertOwned() error {
	return l.touch(time.Now().UTC())
}

func (l *importCandidateClaimLease) stop() {
	if l == nil {
		return
	}
	l.stopOnce.Do(func() { close(l.done) })
	<-l.finished
}

func addImportCandidateSession(
	response map[string]any,
	sessionID string,
	expiresAt time.Time,
	err error,
) {
	if response == nil {
		return
	}
	if err == nil && strings.TrimSpace(sessionID) != "" {
		response["import_session_id"] = sessionID
		response["expires_at"] = expiresAt
		return
	}
	if err == nil {
		return
	}
	warnings, _ := response["warnings"].([]string)
	response["warnings"] = append(warnings, "A protected server-side import candidate could not be created; the legacy script commit flow remains available.")
}
