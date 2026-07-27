package importsession

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestLegacyDescriptorDefaultsToBrunoProfileWithoutChangingWireShape(t *testing.T) {
	const legacy = `{"id":"session-1","project_id":"project-1","actor":"actor-1","backend_key":"objects/session","sha256":"abc","size_bytes":3,"expires_at":"2030-01-01T00:00:00Z"}`

	var descriptor SessionDescriptor
	if err := json.Unmarshal([]byte(legacy), &descriptor); err != nil {
		t.Fatalf("decode legacy descriptor: %v", err)
	}
	if descriptor.Profile != "" {
		t.Fatalf("stored profile = %q, want empty legacy representation", descriptor.Profile)
	}
	if got := descriptor.EffectiveProfile(); got != LegacyBrunoProfile {
		t.Fatalf("effective profile = %q, want %q", got, LegacyBrunoProfile)
	}

	encoded, err := json.Marshal(descriptor)
	if err != nil {
		t.Fatalf("encode legacy descriptor: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatalf("decode encoded descriptor: %v", err)
	}
	if _, exists := wire["profile"]; exists {
		t.Fatalf("legacy descriptor unexpectedly gained profile on the wire: %s", encoded)
	}
}

func TestDescriptorPreservesExplicitImporterProfile(t *testing.T) {
	descriptor := SessionDescriptor{Profile: "postman_native"}
	if got := descriptor.EffectiveProfile(); got != "postman_native" {
		t.Fatalf("effective profile = %q, want postman_native", got)
	}
}

func TestManagerOpenRejectsEveryReturnedDescriptorScopeMismatch(t *testing.T) {
	requested := Scope{
		ID:        "session-1",
		Profile:   "postman_native",
		ProjectID: "project-1",
		Actor:     "actor-1",
	}
	base := SessionDescriptor{
		ID:        requested.ID,
		Profile:   requested.Profile,
		ProjectID: requested.ProjectID,
		Actor:     requested.Actor,
	}
	tests := map[string]func(*SessionDescriptor){
		"id":      func(descriptor *SessionDescriptor) { descriptor.ID = "session-2" },
		"profile": func(descriptor *SessionDescriptor) { descriptor.Profile = "openapi_native" },
		"project": func(descriptor *SessionDescriptor) { descriptor.ProjectID = "project-2" },
		"actor":   func(descriptor *SessionDescriptor) { descriptor.Actor = "actor-2" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			descriptor := base
			mutate(&descriptor)
			reader := &trackingReadCloser{Reader: bytes.NewReader([]byte("protected"))}
			backend := &scopeCheckingStore{
				openDescriptor: descriptor,
				openReader:     reader,
			}
			manager, err := NewManager(backend)
			if err != nil {
				t.Fatalf("NewManager: %v", err)
			}

			payload, returned, err := manager.Open(context.Background(), requested, 64)
			if !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("Open error = %v, want os.ErrNotExist", err)
			}
			if payload != nil || !reflect.DeepEqual(returned, SessionDescriptor{}) {
				t.Fatalf("Open returned mismatched protected state: payload=%q descriptor=%+v", payload, returned)
			}
			if !reader.closed {
				t.Fatal("Open did not close the mismatched protected payload")
			}
		})
	}
}

func TestManagerOpenRejectsExpiredDescriptorWithoutReadingPayload(t *testing.T) {
	requested := Scope{
		ID:        "session-1",
		Profile:   "postman_native",
		ProjectID: "project-1",
		Actor:     "actor-1",
	}
	reader := &trackingReadCloser{Reader: bytes.NewReader([]byte("protected"))}
	backend := &scopeCheckingStore{
		openDescriptor: SessionDescriptor{
			ID:        requested.ID,
			Profile:   requested.Profile,
			ProjectID: requested.ProjectID,
			Actor:     requested.Actor,
			ExpiresAt: time.Now().Add(-time.Minute),
		},
		openReader: reader,
	}
	manager, err := NewManager(backend)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	payload, descriptor, err := manager.Open(context.Background(), requested, 64)
	if !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("Open error = %v, want ErrSessionExpired", err)
	}
	if payload != nil || !reflect.DeepEqual(descriptor, SessionDescriptor{}) {
		t.Fatalf("Open returned expired protected state: payload=%q descriptor=%+v", payload, descriptor)
	}
	if !reader.closed {
		t.Fatal("Open did not close the expired protected payload")
	}
	if reader.reads != 0 {
		t.Fatalf("Open read expired protected payload %d times", reader.reads)
	}
}

func TestManagerClaimRejectsEveryReturnedDescriptorScopeMismatch(t *testing.T) {
	requested := Scope{
		ID:        "session-1",
		Profile:   "postman_native",
		ProjectID: "project-1",
		Actor:     "actor-1",
	}
	base := SessionDescriptor{
		ID:         requested.ID,
		Profile:    requested.Profile,
		ProjectID:  requested.ProjectID,
		Actor:      requested.Actor,
		ClaimToken: "claim-1",
		ExpiresAt:  time.Now().Add(time.Hour),
	}
	tests := map[string]func(*SessionDescriptor){
		"id":      func(descriptor *SessionDescriptor) { descriptor.ID = "session-2" },
		"profile": func(descriptor *SessionDescriptor) { descriptor.Profile = "openapi_native" },
		"project": func(descriptor *SessionDescriptor) { descriptor.ProjectID = "project-2" },
		"actor":   func(descriptor *SessionDescriptor) { descriptor.Actor = "actor-2" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			descriptor := base
			mutate(&descriptor)
			protected := []byte("protected")
			backend := &scopeCheckingStore{
				claimDescriptor: descriptor,
				claimPayload:    protected,
				releaseErr:      errors.New("backend cleanup failed"),
			}
			manager, err := NewManager(backend)
			if err != nil {
				t.Fatalf("NewManager: %v", err)
			}

			claimed, err := manager.Claim(context.Background(), requested, 64)
			if !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("Claim error = %v, want os.ErrNotExist", err)
			}
			if claimed != nil {
				t.Fatalf("Claim returned mismatched protected state: %+v", claimed)
			}
			if !bytes.Equal(protected, make([]byte, len(protected))) {
				t.Fatalf("Claim did not clear mismatched protected payload: %q", protected)
			}
			if backend.released != (Claim{
				Scope: Scope{
					ID:        requested.ID,
					Profile:   "",
					ProjectID: requested.ProjectID,
					Actor:     requested.Actor,
				},
				Token: descriptor.ClaimToken,
			}) {
				t.Fatalf("released claim = %+v, want requested session scope and returned token", backend.released)
			}
		})
	}
}

func TestManagerClaimRejectsInvalidBoundAndEmptyClaimToken(t *testing.T) {
	requested := Scope{
		ID:        "session-1",
		Profile:   "postman_native",
		ProjectID: "project-1",
		Actor:     "actor-1",
	}
	backend := &scopeCheckingStore{
		claimDescriptor: SessionDescriptor{
			ID:        requested.ID,
			Profile:   requested.Profile,
			ProjectID: requested.ProjectID,
			Actor:     requested.Actor,
			ExpiresAt: time.Now().Add(time.Hour),
		},
		claimPayload: []byte("protected"),
	}
	manager, err := NewManager(backend)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	if _, err := manager.Claim(context.Background(), requested, 0); err == nil {
		t.Fatal("Claim with a zero maximum unexpectedly succeeded")
	}
	if backend.claimCalls != 0 {
		t.Fatalf("Claim called the backend %d times for an invalid maximum", backend.claimCalls)
	}
	if _, err := manager.Claim(context.Background(), requested, MaxPayloadBytes+1); err == nil {
		t.Fatal("Claim above the shared maximum unexpectedly succeeded")
	}
	if backend.claimCalls != 0 {
		t.Fatalf("Claim called the backend %d times for an excessive maximum", backend.claimCalls)
	}

	claimed, err := manager.Claim(context.Background(), requested, 64)
	if err == nil || claimed != nil {
		t.Fatalf("Claim with an empty token = (%+v, %v), want an error", claimed, err)
	}
	if !bytes.Equal(backend.claimPayload, make([]byte, len(backend.claimPayload))) {
		t.Fatalf("Claim did not clear payload after empty token: %q", backend.claimPayload)
	}
	if backend.released.ID != requested.ID ||
		backend.released.ProjectID != requested.ProjectID ||
		backend.released.Actor != requested.Actor ||
		backend.released.Token != "" {
		t.Fatalf("empty-token claim release = %+v, want requested scope with empty token", backend.released)
	}
}

func TestManagerSaveAndOpenRejectBoundsAboveSharedMaximumBeforeBackend(t *testing.T) {
	scope := Scope{
		ID:        "session-1",
		Profile:   "postman_native",
		ProjectID: "project-1",
		Actor:     "actor-1",
	}
	backend := &scopeCheckingStore{}
	manager, err := NewManager(backend)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	descriptor := SessionDescriptor{
		ID:        scope.ID,
		Profile:   scope.Profile,
		ProjectID: scope.ProjectID,
		Actor:     scope.Actor,
	}

	if err := manager.Save(
		context.Background(),
		descriptor,
		[]byte("protected"),
		MaxPayloadBytes+1,
	); err == nil {
		t.Fatal("Save above the shared maximum unexpectedly succeeded")
	}
	if backend.saveCalls != 0 {
		t.Fatalf("Save called the backend %d times for an excessive maximum", backend.saveCalls)
	}

	if _, _, err := manager.Open(
		context.Background(),
		scope,
		MaxPayloadBytes+1,
	); err == nil {
		t.Fatal("Open above the shared maximum unexpectedly succeeded")
	}
	if backend.openCalls != 0 {
		t.Fatalf("Open called the backend %d times for an excessive maximum", backend.openCalls)
	}
}

func TestManagerSaveNormalizesProfileAndRoundTripsCanonicalScope(t *testing.T) {
	backend := &scopeCheckingStore{}
	manager, err := NewManager(backend)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	descriptor := SessionDescriptor{
		ID:        "session-1",
		Profile:   "  postman_native  ",
		ProjectID: "project-1",
		Actor:     "actor-1",
		ExpiresAt: time.Now().Add(time.Hour),
	}
	protected := []byte("protected")
	if err := manager.Save(
		context.Background(),
		descriptor,
		protected,
		64,
	); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if backend.savedDescriptor.Profile != "postman_native" {
		t.Fatalf("saved profile = %q, want canonical postman_native", backend.savedDescriptor.Profile)
	}

	backend.openDescriptor = backend.savedDescriptor
	backend.openReader = &trackingReadCloser{Reader: bytes.NewReader(backend.savedPayload)}
	payload, opened, err := manager.Open(context.Background(), Scope{
		ID:        descriptor.ID,
		Profile:   "postman_native",
		ProjectID: descriptor.ProjectID,
		Actor:     descriptor.Actor,
	}, 64)
	if err != nil {
		t.Fatalf("Open canonical profile: %v", err)
	}
	defer clear(payload)
	if !bytes.Equal(payload, protected) || opened.Profile != "postman_native" {
		t.Fatalf("round trip = payload %q descriptor %+v", payload, opened)
	}
}

func TestManagerSaveRejectsWhitespaceProfileAndExpiredDescriptorBeforeBackend(t *testing.T) {
	backend := &scopeCheckingStore{}
	manager, err := NewManager(backend)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	base := SessionDescriptor{
		ID:        "session-1",
		ProjectID: "project-1",
		Actor:     "actor-1",
		ExpiresAt: time.Now().Add(time.Hour),
	}

	invalidProfile := base
	invalidProfile.Profile = "   "
	if err := manager.Save(
		context.Background(),
		invalidProfile,
		[]byte("protected"),
		64,
	); !errors.Is(err, ErrSessionScope) {
		t.Fatalf("Save whitespace profile error = %v, want ErrSessionScope", err)
	}

	expired := base
	expired.Profile = "postman_native"
	expired.ExpiresAt = time.Now().Add(-time.Minute)
	if err := manager.Save(
		context.Background(),
		expired,
		[]byte("protected"),
		64,
	); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("Save expired descriptor error = %v, want ErrSessionExpired", err)
	}
	if backend.saveCalls != 0 {
		t.Fatalf("Save called backend %d times for invalid descriptors", backend.saveCalls)
	}
}

func TestManagerClaimEnforcesBoundReturnedByBackend(t *testing.T) {
	requested := Scope{
		ID:        "session-1",
		Profile:   "postman_native",
		ProjectID: "project-1",
		Actor:     "actor-1",
	}
	payload := []byte("oversized")
	backend := &scopeCheckingStore{
		claimDescriptor: SessionDescriptor{
			ID:         requested.ID,
			Profile:    requested.Profile,
			ProjectID:  requested.ProjectID,
			Actor:      requested.Actor,
			ClaimToken: "claim-1",
			ExpiresAt:  time.Now().Add(time.Hour),
		},
		claimPayload: payload,
	}
	manager, err := NewManager(backend)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	claimed, err := manager.Claim(context.Background(), requested, 4)
	if err == nil || claimed != nil {
		t.Fatalf("Claim with oversized backend payload = (%+v, %v), want an error", claimed, err)
	}
	if !bytes.Equal(payload, make([]byte, len(payload))) {
		t.Fatalf("Claim did not clear oversized payload: %q", payload)
	}
	if backend.released.Token != "claim-1" ||
		backend.released.ID != requested.ID ||
		backend.released.ProjectID != requested.ProjectID ||
		backend.released.Actor != requested.Actor {
		t.Fatalf("released claim = %+v, want the oversized claim lease", backend.released)
	}
}

type trackingReadCloser struct {
	io.Reader
	closed bool
	reads  int
}

func (r *trackingReadCloser) Read(payload []byte) (int, error) {
	r.reads++
	return r.Reader.Read(payload)
}

func (r *trackingReadCloser) Close() error {
	r.closed = true
	return nil
}

type scopeCheckingStore struct {
	openDescriptor  SessionDescriptor
	openReader      io.ReadCloser
	openCalls       int
	claimDescriptor SessionDescriptor
	claimPayload    []byte
	claimCalls      int
	saveCalls       int
	savedDescriptor SessionDescriptor
	savedPayload    []byte
	released        Claim
	releaseErr      error
}

func (s *scopeCheckingStore) SaveSession(context.Context, SessionDescriptor, io.Reader, int64) error {
	return nil
}

func (s *scopeCheckingStore) SaveSessionBytes(
	_ context.Context,
	descriptor SessionDescriptor,
	payload []byte,
	_ int64,
) error {
	s.saveCalls++
	s.savedDescriptor = descriptor
	s.savedPayload = append(s.savedPayload[:0], payload...)
	return nil
}

func (s *scopeCheckingStore) OpenSession(
	context.Context,
	string,
	string,
	string,
) (io.ReadCloser, SessionDescriptor, error) {
	s.openCalls++
	return s.openReader, s.openDescriptor, nil
}

func (s *scopeCheckingStore) ClaimSession(
	context.Context,
	string,
	string,
	string,
	int64,
) ([]byte, SessionDescriptor, error) {
	s.claimCalls++
	return s.claimPayload, s.claimDescriptor, nil
}

func (s *scopeCheckingStore) TouchSessionClaim(string, string, string, string, time.Time) error {
	return nil
}

func (s *scopeCheckingStore) ReleaseSessionClaim(id, projectID, actor, token string) error {
	s.released = Claim{
		Scope: Scope{
			ID:        id,
			ProjectID: projectID,
			Actor:     actor,
		},
		Token: token,
	}
	return s.releaseErr
}

func (s *scopeCheckingStore) CompleteSessionClaim(
	context.Context,
	string,
	string,
	string,
	string,
) error {
	return nil
}

func (s *scopeCheckingStore) FinalizeCommittedSession(context.Context, string, string, string) error {
	return nil
}

func (s *scopeCheckingStore) DeleteSession(context.Context, string, string) error {
	return nil
}

func (s *scopeCheckingStore) DeleteProject(context.Context, string) error {
	return nil
}

func (s *scopeCheckingStore) DeleteExpiredSessions(
	context.Context,
	time.Time,
) ([]SessionDescriptor, error) {
	return nil, nil
}

func (s *scopeCheckingStore) IsSessionRelatedObjectReferenced(string, string) (bool, error) {
	return false, nil
}
