package importartifact

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/importsession"
)

func TestEncryptedStoreImplementsGenericProtectedSessionContract(t *testing.T) {
	store := newEncryptedArtifactTestStore(t)
	var protected importsession.ProtectedStore = store
	manager, err := importsession.NewManager(protected)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	descriptor := importsession.SessionDescriptor{
		ID:        "generic-session",
		Profile:   "postman_native",
		ProjectID: "project-generic",
		Actor:     "actor-generic",
		ExpiresAt: time.Now().UTC().Add(time.Minute),
	}
	if err := manager.Save(
		context.Background(), descriptor, []byte(`{"protected":true}`), 1024,
	); err != nil {
		t.Fatalf("Save: %v", err)
	}

	payload, opened, err := manager.Open(
		context.Background(),
		importsession.Scope{
			ID: descriptor.ID, Profile: descriptor.Profile,
			ProjectID: descriptor.ProjectID, Actor: descriptor.Actor,
		},
		1024,
	)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if string(payload) != `{"protected":true}` {
		t.Fatalf("payload = %q", payload)
	}
	if opened.Profile != descriptor.Profile {
		t.Fatalf("profile = %q, want %q", opened.Profile, descriptor.Profile)
	}
	objectPath, err := store.pathForBackendKey(opened.BackendKey)
	if err != nil {
		t.Fatalf("resolve protected payload path: %v", err)
	}
	onDisk, err := os.ReadFile(objectPath)
	if err != nil {
		t.Fatalf("read protected payload: %v", err)
	}
	if !strings.HasPrefix(string(onDisk), artifactMagic) {
		t.Fatalf("protected payload no longer uses legacy artifact magic %q", artifactMagic)
	}
	if _, _, err := manager.Open(
		context.Background(),
		importsession.Scope{
			ID: descriptor.ID, Profile: "openapi_native",
			ProjectID: descriptor.ProjectID, Actor: descriptor.Actor,
		},
		1024,
	); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cross-profile Open error = %v, want os.ErrNotExist", err)
	}
}

func TestGenericProtectedSessionClaimIsSingleWinner(t *testing.T) {
	store := newEncryptedArtifactTestStore(t)
	var protected importsession.ProtectedStore = store
	manager, err := importsession.NewManager(protected)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	descriptor := importsession.SessionDescriptor{
		ID:        "generic-concurrent-session",
		Profile:   "openapi_native",
		ProjectID: "project-generic",
		Actor:     "actor-generic",
		ExpiresAt: time.Now().UTC().Add(time.Minute),
	}
	if err := manager.Save(
		context.Background(), descriptor, []byte("payload"), 1024,
	); err != nil {
		t.Fatalf("Save: %v", err)
	}

	type outcome struct {
		session *importsession.ClaimedSession
		err     error
	}
	start := make(chan struct{})
	results := make(chan outcome, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			claimed, err := manager.Claim(
				context.Background(),
				importsession.Scope{
					ID: descriptor.ID, Profile: descriptor.Profile,
					ProjectID: descriptor.ProjectID, Actor: descriptor.Actor,
				},
				1024,
			)
			if claimed != nil {
				clear(claimed.Payload)
			}
			results <- outcome{session: claimed, err: err}
		}()
	}
	close(start)
	workers.Wait()
	close(results)

	var winner *importsession.ClaimedSession
	claimedErrors := 0
	for result := range results {
		switch {
		case result.err == nil:
			if winner != nil {
				t.Fatal("multiple concurrent claims succeeded")
			}
			winner = result.session
		case errors.Is(result.err, importsession.ErrSessionClaimed):
			claimedErrors++
		default:
			t.Fatalf("unexpected concurrent claim error: %v", result.err)
		}
	}
	if winner == nil || claimedErrors != 1 {
		t.Fatalf("winner=%+v claimed errors=%d, want one of each", winner, claimedErrors)
	}
	if err := manager.Release(winner.Claim); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

func TestGenericManagerReleasesCrossProfileClaim(t *testing.T) {
	store := newEncryptedArtifactTestStore(t)
	manager, err := importsession.NewManager(store)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	descriptor := importsession.SessionDescriptor{
		ID:        "generic-profile-claim",
		Profile:   "postman_native",
		ProjectID: "project-generic",
		Actor:     "actor-generic",
		ExpiresAt: time.Now().UTC().Add(time.Minute),
	}
	if err := manager.Save(context.Background(), descriptor, []byte("payload"), 1024); err != nil {
		t.Fatalf("Save: %v", err)
	}
	wrong := importsession.Scope{
		ID: descriptor.ID, Profile: "openapi_native",
		ProjectID: descriptor.ProjectID, Actor: descriptor.Actor,
	}
	if _, err := manager.Claim(context.Background(), wrong, 1024); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cross-profile Claim error = %v, want os.ErrNotExist", err)
	}
	correct := wrong
	correct.Profile = descriptor.Profile
	claimed, err := manager.Claim(context.Background(), correct, 1024)
	if err != nil {
		t.Fatalf("Claim after cross-profile release: %v", err)
	}
	clear(claimed.Payload)
	if _, err := manager.Claim(context.Background(), wrong, 1024); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cross-profile Claim during active lease error = %v, want os.ErrNotExist", err)
	}
	if _, _, err := manager.Open(context.Background(), wrong, 1024); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cross-profile Open during active lease error = %v, want os.ErrNotExist", err)
	}
	if err := manager.Release(claimed.Claim); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

func TestGenericManagerProfileFencesDestructiveLifecycle(t *testing.T) {
	store := newEncryptedArtifactTestStore(t)
	manager, err := importsession.NewManager(store)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	descriptor := importsession.SessionDescriptor{
		ID:        "profile-fenced-lifecycle",
		Profile:   "postman",
		ProjectID: "project-generic",
		Actor:     "actor-generic",
		ExpiresAt: time.Now().UTC().Add(time.Minute),
	}
	if err := manager.Save(context.Background(), descriptor, []byte("payload"), 1024); err != nil {
		t.Fatalf("Save: %v", err)
	}
	wrong := importsession.Scope{
		ID: descriptor.ID, Profile: "openapi",
		ProjectID: descriptor.ProjectID, Actor: descriptor.Actor,
	}
	if err := manager.FinalizeCommitted(context.Background(), wrong); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cross-profile FinalizeCommitted error = %v, want os.ErrNotExist", err)
	}
	if err := manager.Delete(context.Background(), wrong); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cross-profile Delete error = %v, want os.ErrNotExist", err)
	}
	correct := wrong
	correct.Profile = descriptor.Profile
	payload, _, err := manager.Open(context.Background(), correct, 1024)
	if err != nil {
		t.Fatalf("Open after cross-profile destructive attempts: %v", err)
	}
	clear(payload)
	if err := manager.Delete(context.Background(), correct); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, _, err := manager.Open(context.Background(), correct, 1024); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Open after Delete error = %v, want os.ErrNotExist", err)
	}
}

func TestProtectedSessionLifecycleRejectsTraversalIDs(t *testing.T) {
	store := newEncryptedArtifactTestStore(t)
	manager, err := importsession.NewManager(store)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	descriptor := importsession.SessionDescriptor{
		ID:        "../outside-session",
		Profile:   "postman",
		ProjectID: "project-generic",
		Actor:     "actor-generic",
		ExpiresAt: time.Now().UTC().Add(time.Minute),
	}
	if err := manager.Save(context.Background(), descriptor, []byte("payload"), 1024); err == nil {
		t.Fatal("Save accepted a traversal session id")
	}
	claim := importsession.Claim{
		Scope: importsession.Scope{
			ID: descriptor.ID, Profile: descriptor.Profile,
			ProjectID: descriptor.ProjectID, Actor: descriptor.Actor,
		},
		Token: "forged-token",
	}
	if err := manager.Renew(claim, time.Now().UTC()); err == nil {
		t.Fatal("Renew accepted a traversal session id")
	}
	if err := manager.Release(claim); err == nil {
		t.Fatal("Release accepted a traversal session id")
	}
	if err := manager.Complete(context.Background(), claim); err == nil {
		t.Fatal("Complete accepted a traversal session id")
	}
}

func TestGenericManagerDoesNotReturnExpiredClaimPayload(t *testing.T) {
	store := newEncryptedArtifactTestStore(t)
	manager, err := importsession.NewManager(store)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	descriptor := importsession.SessionDescriptor{
		ID:        "expired-generic-session",
		Profile:   "postman",
		ProjectID: "project-generic",
		Actor:     "actor-generic",
		ExpiresAt: time.Now().UTC().Add(-time.Second),
	}
	// Manager.Save deliberately refuses an already-expired descriptor, so the
	// session is staged through the store to reproduce the real on-disk state:
	// written while valid, expired by the time a claim arrives.
	if err := store.SaveSessionBytes(context.Background(), descriptor, []byte("credential-payload"), 1024); err != nil {
		t.Fatalf("SaveSessionBytes: %v", err)
	}
	claimed, err := manager.Claim(context.Background(), importsession.Scope{
		ID: descriptor.ID, Profile: descriptor.Profile,
		ProjectID: descriptor.ProjectID, Actor: descriptor.Actor,
	}, 1024)
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("Claim error = %v, want expired", err)
	}
	if claimed != nil {
		t.Fatalf("expired claim returned payload: %+v", claimed)
	}
	if _, err := os.Stat(filepath.Join(store.root, ".sessions", descriptor.ID+".claim.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired claim was not released: %v", err)
	}
}

func TestGenericManagerOpensLegacyUnprofiledSession(t *testing.T) {
	store := newEncryptedArtifactTestStore(t)
	manager, err := importsession.NewManager(store)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	descriptor := importsession.SessionDescriptor{
		ID:        "legacy-unprofiled-session",
		ProjectID: "project-legacy",
		Actor:     "actor-legacy",
		ExpiresAt: time.Now().UTC().Add(time.Minute),
	}
	if err := manager.Save(context.Background(), descriptor, []byte("legacy"), 1024); err != nil {
		t.Fatalf("Save: %v", err)
	}
	rawDescriptor, err := os.ReadFile(
		filepath.Join(store.root, ".sessions", descriptor.ID+".json"),
	)
	if err != nil {
		t.Fatalf("read legacy descriptor: %v", err)
	}
	if strings.Contains(string(rawDescriptor), `"profile"`) {
		t.Fatalf("legacy descriptor wire shape changed: %s", rawDescriptor)
	}
	payload, opened, err := manager.Open(
		context.Background(),
		importsession.Scope{
			ID: descriptor.ID, Profile: importsession.LegacyBrunoProfile,
			ProjectID: descriptor.ProjectID, Actor: descriptor.Actor,
		},
		1024,
	)
	if err != nil {
		t.Fatalf("Open legacy descriptor: %v", err)
	}
	if string(payload) != "legacy" || opened.EffectiveProfile() != importsession.LegacyBrunoProfile {
		t.Fatalf("legacy payload/descriptor = %q/%+v", payload, opened)
	}
}
