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
	importCandidateSessionVersion  = 1
	importCandidateClaimRenewEvery = 10 * time.Second
)

type importCandidateEnvelope struct {
	Version                int                                    `json:"version"`
	Profile                string                                 `json:"profile"`
	ProjectID              string                                 `json:"project_id"`
	Script                 dsl.Script                             `json:"script"`
	CredentialRequirements []importCandidateCredentialRequirement `json:"credential_requirements,omitempty"`
	CreatedAt              time.Time                              `json:"created_at"`
	ExpiresAt              time.Time                              `json:"expires_at"`
}

// importCandidateCredentialRequirement is intentionally scoped to the
// encrypted candidate payload. credentialpolicy.Requirement.Value has json:"-";
// this envelope is the one explicit codec allowed to serialize that value.
type importCandidateCredentialRequirement struct {
	Requirement credentialpolicy.Requirement `json:"requirement"`
	Value       string                       `json:"value"`
}

type protectedImportCandidate struct {
	Script                 *dsl.Script
	CredentialRequirements []credentialpolicy.Requirement
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
	return s.registerImportCandidateSessionWithRequirements(r, profile, script, nil)
}

func (s *Server) registerImportCandidateSessionWithRequirements(
	r *http.Request,
	profile string,
	script *dsl.Script,
	requirements []credentialpolicy.Requirement,
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
		return "", time.Time{}, err
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
	expiresAt := now.Add(importCandidateSessionTTL)
	envelope := importCandidateEnvelope{
		Version:   importCandidateSessionVersion,
		Profile:   profile,
		ProjectID: projectID,
		Script:    *script,
		CreatedAt: now,
		ExpiresAt: expiresAt,
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
		return "", time.Time{}, err
	}
	id := uuid.NewString()
	err = manager.Save(r.Context(), importsession.SessionDescriptor{
		ID:        id,
		Profile:   profile,
		ProjectID: projectID,
		Actor:     actor,
		ExpiresAt: expiresAt,
	}, payload, importCandidateSessionMaxBytes)
	if err != nil {
		return "", time.Time{}, err
	}
	return id, expiresAt, nil
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
	if r == nil {
		return nil, nil, errors.New("request is required")
	}
	profile = strings.ToLower(strings.TrimSpace(profile))
	sessionID = strings.TrimSpace(sessionID)
	projectID = normalizeProjectID(projectID)
	_, actor, _ := s.resolveAuditActor(r)
	manager, err := s.protectedImportSessionManager()
	if err != nil {
		return nil, nil, err
	}
	claimed, err := manager.Claim(r.Context(), importsession.Scope{
		ID:        sessionID,
		Profile:   profile,
		ProjectID: projectID,
		Actor:     actor,
	}, importCandidateSessionMaxBytes)
	if err != nil {
		return nil, nil, err
	}
	defer clear(claimed.Payload)

	envelope, requirements, err := unmarshalImportCandidateEnvelope(claimed.Payload)
	if err != nil {
		_ = manager.Release(claimed.Claim)
		return nil, nil, errors.New("protected import candidate is invalid")
	}
	if envelope.Version != importCandidateSessionVersion ||
		!strings.EqualFold(strings.TrimSpace(envelope.Profile), profile) ||
		normalizeProjectID(envelope.ProjectID) != projectID {
		clearCredentialRequirementValues(requirements)
		releaseErr := manager.Release(claimed.Claim)
		return nil, nil, errors.Join(errors.New("protected import candidate scope is invalid"), releaseErr)
	}
	if !envelope.ExpiresAt.After(time.Now().UTC()) {
		clearCredentialRequirementValues(requirements)
		if releaseErr := manager.Release(claimed.Claim); releaseErr != nil {
			return nil, nil, fmt.Errorf("release expired protected import candidate claim: %w", releaseErr)
		}
		return nil, nil, importsession.ErrSessionExpired
	}
	script := envelope.Script
	return &protectedImportCandidate{
		Script:                 &script,
		CredentialRequirements: requirements,
	}, &claimed.Claim, nil
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
		value := requirement.Value
		cloned[index].Value = ""
		requirement.Value = ""
		if value == "" || requirement.ExistingReference {
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
		if id == "" || protected.Value == "" || protected.Requirement.ExistingReference {
			clearCredentialRequirementValues(requirements)
			return importCandidateEnvelope{}, nil, errors.New("protected credential requirement is invalid")
		}
		if _, duplicate := seen[id]; duplicate {
			clearCredentialRequirementValues(requirements)
			return importCandidateEnvelope{}, nil, errors.New("protected credential requirement ids must be unique")
		}
		seen[id] = struct{}{}
		requirement := protected.Requirement
		requirement.Value = protected.Value
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
	// Completion must remain fenced to this exact lease epoch. The generic
	// OpenAPI/Postman flow has no durable receipt authorizing an epochless
	// finalization; if cleanup fails, retain the claim so a stale-claim recovery
	// can reconcile the deterministic create-only script safely.
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
