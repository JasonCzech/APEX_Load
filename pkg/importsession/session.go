// Package importsession defines the importer-neutral contract for protected,
// short-lived inspection sessions.
//
// Session payloads are deliberately opaque to this package. Import adapters
// own their payload codecs and are responsible for clearing decrypted bytes
// and any restored source credential values after use.
package importsession

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

const (
	// LegacyBrunoProfile is used when reading a descriptor written before
	// importer profiles were recorded. It is part of the persisted
	// compatibility contract and must not be changed.
	LegacyBrunoProfile = "bruno"

	// MaxPayloadBytes is the process-wide upper bound for a protected import
	// session payload. Importers may request a smaller per-profile bound, but
	// must not turn the shared encrypted store into an unbounded read surface.
	MaxPayloadBytes int64 = 64 << 20
)

var (
	// These errors intentionally retain their established Bruno wording while
	// the legacy artifact implementation is migrated behind this generic
	// contract. Existing callers and operators may surface the text, and all
	// new code should match them with errors.Is.
	ErrProjectDeleted   = errors.New("Bruno artifact project is deleted")
	ErrSessionCapacity  = errors.New("Bruno import session capacity reached")
	ErrSessionClaimed   = errors.New("Bruno import session is already being consumed")
	ErrSessionClaimLost = errors.New("Bruno import session claim was lost")
	ErrSessionExpired   = errors.New("protected import session expired")
	ErrSessionScope     = errors.New("protected import session scope is invalid")
)

// ObjectRef identifies one immutable encrypted object. BackendKey is opaque to
// callers and is meaningful only to the configured protected object store.
type ObjectRef struct {
	BackendKey string
	ProjectID  string
	Kind       string
	SHA256     string
	SizeBytes  int64
}

// SessionDescriptor is value-free lifecycle metadata for one encrypted
// inspection payload. Profile was added after Bruno sessions were deployed;
// an empty value therefore means LegacyBrunoProfile.
type SessionDescriptor struct {
	ID        string `json:"id"`
	Profile   string `json:"profile,omitempty"`
	ProjectID string `json:"project_id"`
	Actor     string `json:"actor"`
	// ClaimToken fences one lease epoch from a later stale-claim recovery.
	// Normal descriptors omit it; each successful claim replaces it with a
	// fresh token before publishing the claimed state.
	ClaimToken     string      `json:"claim_token,omitempty"`
	BackendKey     string      `json:"backend_key"`
	SHA256         string      `json:"sha256"`
	SizeBytes      int64       `json:"size_bytes"`
	ExpiresAt      time.Time   `json:"expires_at"`
	RelatedObjects []ObjectRef `json:"related_objects,omitempty"`
	CapacitySlot   *int        `json:"capacity_slot,omitempty"`
}

// EffectiveProfile returns the stored profile or the legacy Bruno profile for
// descriptors created before profiles were persisted.
func (d SessionDescriptor) EffectiveProfile() string {
	if d.Profile == "" {
		return LegacyBrunoProfile
	}
	return d.Profile
}

// PublicationDescriptor is a write-ahead record for content-addressed source
// objects. It is removed only after a session descriptor or durable database
// receipt owns every listed object.
type PublicationDescriptor struct {
	ID        string      `json:"id"`
	Profile   string      `json:"profile,omitempty"`
	ProjectID string      `json:"project_id"`
	ExpiresAt time.Time   `json:"expires_at"`
	Objects   []ObjectRef `json:"objects"`
}

// ProtectedStore is the importer-neutral lifecycle boundary implemented by
// the encrypted artifact store. Claims retain the encrypted payload until the
// caller either releases the claim after failure or completes it after a
// durable commit.
//
// Claim tokens must be compared on every renew, release, and completion so a
// stale controller cannot mutate a later lease epoch.
type ProtectedStore interface {
	SaveSession(context.Context, SessionDescriptor, io.Reader, int64) error
	SaveSessionBytes(context.Context, SessionDescriptor, []byte, int64) error
	OpenSession(context.Context, string, string, string) (io.ReadCloser, SessionDescriptor, error)
	ClaimSession(context.Context, string, string, string, int64) ([]byte, SessionDescriptor, error)
	TouchSessionClaim(string, string, string, string, time.Time) error
	ReleaseSessionClaim(string, string, string, string) error
	CompleteSessionClaim(context.Context, string, string, string, string) error
	FinalizeCommittedSession(context.Context, string, string, string) error
	DeleteSession(context.Context, string, string) error
	DeleteProject(context.Context, string) error
	DeleteExpiredSessions(context.Context, time.Time) ([]SessionDescriptor, error)
	IsSessionRelatedObjectReferenced(string, string) (bool, error)
}

// ScopedProtectedStore is the profile-aware destructive lifecycle extension.
// Legacy Bruno callers retain the original ProtectedStore methods, while
// importer-neutral handlers use these methods so possession of another
// importer's session id cannot finalize or delete its protected payload.
type ScopedProtectedStore interface {
	FinalizeCommittedSessionScoped(context.Context, Scope) error
	DeleteSessionScoped(context.Context, Scope) error
}

// ScopedAccessProtectedStore lets a backend validate importer profile before
// it opens protected bytes or reports the state of an active claim. Without
// this extension, a wrong-profile caller with an exact session id could learn
// that the session is currently claimed even though Manager would reject the
// payload after a normal open.
type ScopedAccessProtectedStore interface {
	OpenSessionScoped(context.Context, Scope) (io.ReadCloser, SessionDescriptor, error)
	ClaimSessionScoped(context.Context, Scope, int64) ([]byte, SessionDescriptor, error)
}

// Scope is the value-free identity used for every importer-neutral session
// operation. Session IDs are globally unique; Profile additionally prevents a
// handler from decoding another importer's protected payload.
type Scope struct {
	ID        string
	Profile   string
	ProjectID string
	Actor     string
}

type Claim struct {
	Scope
	Token string
}

type ClaimedSession struct {
	Descriptor SessionDescriptor
	Claim      Claim
	Payload    []byte
}

// Lifecycle is the profile-aware interface intended for import handlers.
// ProtectedStore remains the compatibility boundary for the deployed
// encrypted artifact implementation.
type Lifecycle interface {
	Save(context.Context, SessionDescriptor, []byte, int64) error
	Open(context.Context, Scope, int64) ([]byte, SessionDescriptor, error)
	Claim(context.Context, Scope, int64) (*ClaimedSession, error)
	Renew(Claim, time.Time) error
	Release(Claim) error
	Complete(context.Context, Claim) error
	FinalizeCommitted(context.Context, Scope) error
	Delete(context.Context, Scope) error
	Sweep(context.Context, time.Time) ([]SessionDescriptor, error)
}

// Manager adds importer-profile isolation and bounded payload reads to an
// existing protected store without changing its on-disk representation.
type Manager struct {
	backend ProtectedStore
}

func NewManager(backend ProtectedStore) (*Manager, error) {
	if backend == nil {
		return nil, errors.New("protected import session store is required")
	}
	return &Manager{backend: backend}, nil
}

func (m *Manager) Save(
	ctx context.Context,
	descriptor SessionDescriptor,
	payload []byte,
	maxBytes int64,
) error {
	if m == nil || m.backend == nil {
		return errors.New("protected import session store is required")
	}
	if descriptor.Profile != "" {
		descriptor.Profile = strings.TrimSpace(descriptor.Profile)
		if descriptor.Profile == "" {
			return fmt.Errorf("%w: profile is invalid", ErrSessionScope)
		}
	}
	if err := validateScope(scopeFromDescriptor(descriptor)); err != nil {
		return err
	}
	if err := validatePayloadBound(maxBytes); err != nil {
		return err
	}
	if int64(len(payload)) > maxBytes {
		return errors.New("protected import session exceeds its maximum size")
	}
	if !descriptor.ExpiresAt.After(time.Now().UTC()) {
		return ErrSessionExpired
	}
	return m.backend.SaveSessionBytes(ctx, descriptor, payload, maxBytes)
}

func (m *Manager) Open(
	ctx context.Context,
	scope Scope,
	maxBytes int64,
) ([]byte, SessionDescriptor, error) {
	if m == nil || m.backend == nil {
		return nil, SessionDescriptor{}, errors.New("protected import session store is required")
	}
	if err := validateScope(scope); err != nil {
		return nil, SessionDescriptor{}, err
	}
	if err := validatePayloadBound(maxBytes); err != nil {
		return nil, SessionDescriptor{}, err
	}
	var reader io.ReadCloser
	var descriptor SessionDescriptor
	var err error
	if scoped, ok := m.backend.(ScopedAccessProtectedStore); ok {
		reader, descriptor, err = scoped.OpenSessionScoped(ctx, scope)
	} else {
		reader, descriptor, err = m.backend.OpenSession(
			ctx, scope.ID, scope.ProjectID, scope.Actor,
		)
	}
	if err != nil {
		return nil, descriptor, err
	}
	if !descriptorMatchesScope(descriptor, scope) {
		_ = reader.Close()
		return nil, SessionDescriptor{}, os.ErrNotExist
	}
	if !descriptor.ExpiresAt.After(time.Now().UTC()) {
		closeErr := reader.Close()
		return nil, SessionDescriptor{}, errors.Join(ErrSessionExpired, closeErr)
	}
	payload, readErr := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil {
		clear(payload)
		return nil, SessionDescriptor{}, errors.Join(readErr, closeErr)
	}
	if int64(len(payload)) > maxBytes {
		clear(payload)
		return nil, SessionDescriptor{}, errors.New("protected import session exceeds its maximum size")
	}
	return payload, descriptor, nil
}

func (m *Manager) Claim(
	ctx context.Context,
	scope Scope,
	maxBytes int64,
) (*ClaimedSession, error) {
	if m == nil || m.backend == nil {
		return nil, errors.New("protected import session store is required")
	}
	if err := validateScope(scope); err != nil {
		return nil, err
	}
	if err := validatePayloadBound(maxBytes); err != nil {
		return nil, err
	}
	var payload []byte
	var descriptor SessionDescriptor
	var err error
	if scoped, ok := m.backend.(ScopedAccessProtectedStore); ok {
		payload, descriptor, err = scoped.ClaimSessionScoped(ctx, scope, maxBytes)
	} else {
		payload, descriptor, err = m.backend.ClaimSession(
			ctx, scope.ID, scope.ProjectID, scope.Actor, maxBytes,
		)
	}
	if err != nil {
		return nil, err
	}
	claim := Claim{
		Scope: Scope{
			ID:        scope.ID,
			Profile:   effectiveScopeProfile(scope),
			ProjectID: scope.ProjectID,
			Actor:     scope.Actor,
		},
		Token: descriptor.ClaimToken,
	}
	if !descriptorMatchesScope(descriptor, scope) {
		clear(payload)
		// A non-profile-aware compatibility backend may only discover a
		// mismatched descriptor after it has moved the requested session into
		// claimed state. Best-effort cleanup must not turn that internal
		// mismatch into an existence oracle for the caller.
		_ = m.backend.ReleaseSessionClaim(
			claim.ID, claim.ProjectID, claim.Actor, claim.Token,
		)
		return nil, os.ErrNotExist
	}
	if strings.TrimSpace(claim.Token) == "" {
		clear(payload)
		tokenErr := errors.New("protected import session store returned an empty claim token")
		releaseErr := m.backend.ReleaseSessionClaim(
			claim.ID, claim.ProjectID, claim.Actor, claim.Token,
		)
		return nil, errors.Join(tokenErr, releaseErr)
	}
	if int64(len(payload)) > maxBytes {
		clear(payload)
		releaseErr := m.backend.ReleaseSessionClaim(
			claim.ID, claim.ProjectID, claim.Actor, claim.Token,
		)
		if releaseErr != nil {
			return nil, fmt.Errorf("release oversized protected import session claim: %w", releaseErr)
		}
		return nil, errors.New("protected import session exceeds its maximum size")
	}
	if !descriptor.ExpiresAt.After(time.Now().UTC()) {
		clear(payload)
		releaseErr := m.backend.ReleaseSessionClaim(
			claim.ID, claim.ProjectID, claim.Actor, claim.Token,
		)
		if releaseErr != nil {
			return nil, fmt.Errorf("release expired protected import session claim: %w", releaseErr)
		}
		return nil, ErrSessionExpired
	}
	return &ClaimedSession{
		Descriptor: descriptor,
		Claim:      claim,
		Payload:    payload,
	}, nil
}

func validatePayloadBound(maxBytes int64) error {
	if maxBytes <= 0 {
		return errors.New("protected import session maximum size must be positive")
	}
	if maxBytes > MaxPayloadBytes {
		return fmt.Errorf(
			"protected import session maximum size exceeds %d bytes",
			MaxPayloadBytes,
		)
	}
	return nil
}

func (m *Manager) Renew(claim Claim, now time.Time) error {
	if m == nil || m.backend == nil {
		return errors.New("protected import session store is required")
	}
	if err := validateClaim(claim); err != nil {
		return err
	}
	return m.backend.TouchSessionClaim(
		claim.ID, claim.ProjectID, claim.Actor, claim.Token, now,
	)
}

func (m *Manager) Release(claim Claim) error {
	if m == nil || m.backend == nil {
		return errors.New("protected import session store is required")
	}
	if err := validateClaim(claim); err != nil {
		return err
	}
	return m.backend.ReleaseSessionClaim(
		claim.ID, claim.ProjectID, claim.Actor, claim.Token,
	)
}

func (m *Manager) Complete(ctx context.Context, claim Claim) error {
	if m == nil || m.backend == nil {
		return errors.New("protected import session store is required")
	}
	if err := validateClaim(claim); err != nil {
		return err
	}
	return m.backend.CompleteSessionClaim(
		ctx, claim.ID, claim.ProjectID, claim.Actor, claim.Token,
	)
}

func (m *Manager) FinalizeCommitted(ctx context.Context, scope Scope) error {
	if m == nil || m.backend == nil {
		return errors.New("protected import session store is required")
	}
	if err := validateScope(scope); err != nil {
		return err
	}
	if scoped, ok := m.backend.(ScopedProtectedStore); ok {
		return scoped.FinalizeCommittedSessionScoped(ctx, scope)
	}
	if effectiveScopeProfile(scope) != LegacyBrunoProfile {
		return errors.New("protected import session store cannot profile-fence finalization")
	}
	return m.backend.FinalizeCommittedSession(
		ctx, scope.ID, scope.ProjectID, scope.Actor,
	)
}

func (m *Manager) Delete(ctx context.Context, scope Scope) error {
	if m == nil || m.backend == nil {
		return errors.New("protected import session store is required")
	}
	if err := validateScope(scope); err != nil {
		return err
	}
	if scoped, ok := m.backend.(ScopedProtectedStore); ok {
		return scoped.DeleteSessionScoped(ctx, scope)
	}
	if effectiveScopeProfile(scope) != LegacyBrunoProfile {
		return errors.New("protected import session store cannot profile-fence deletion")
	}
	return m.backend.DeleteSession(ctx, scope.ID, scope.ProjectID)
}

func (m *Manager) Sweep(ctx context.Context, now time.Time) ([]SessionDescriptor, error) {
	if m == nil || m.backend == nil {
		return nil, errors.New("protected import session store is required")
	}
	return m.backend.DeleteExpiredSessions(ctx, now.UTC())
}

func validateScope(scope Scope) error {
	if strings.TrimSpace(scope.ID) == "" ||
		strings.TrimSpace(scope.ProjectID) == "" ||
		strings.TrimSpace(scope.Actor) == "" {
		return fmt.Errorf("%w: id, project, and actor are required", ErrSessionScope)
	}
	if !validSessionID(scope.ID) {
		return fmt.Errorf("%w: id is invalid", ErrSessionScope)
	}
	return nil
}

func validateClaim(claim Claim) error {
	if err := validateScope(claim.Scope); err != nil {
		return err
	}
	if strings.TrimSpace(claim.Token) == "" {
		return errors.New("protected import session claim token is required")
	}
	return nil
}

func scopeFromDescriptor(descriptor SessionDescriptor) Scope {
	return Scope{
		ID:        descriptor.ID,
		Profile:   descriptor.EffectiveProfile(),
		ProjectID: descriptor.ProjectID,
		Actor:     descriptor.Actor,
	}
}

func profileMatches(descriptor SessionDescriptor, requested string) bool {
	return descriptor.EffectiveProfile() == effectiveProfile(requested)
}

func descriptorMatchesScope(descriptor SessionDescriptor, requested Scope) bool {
	return descriptor.ID == requested.ID &&
		descriptor.ProjectID == requested.ProjectID &&
		descriptor.Actor == requested.Actor &&
		profileMatches(descriptor, requested.Profile)
}

func effectiveScopeProfile(scope Scope) string {
	return effectiveProfile(scope.Profile)
}

func effectiveProfile(profile string) string {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return LegacyBrunoProfile
	}
	return profile
}

func validSessionID(id string) bool {
	id = strings.TrimSpace(id)
	return id != "" && !strings.ContainsAny(id, `/\`) && !strings.Contains(id, "..")
}

var _ Lifecycle = (*Manager)(nil)
