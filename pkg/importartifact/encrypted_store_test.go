package importartifact

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestEncryptedStoreRoundTripDedupAndProjectIsolation(t *testing.T) {
	store := newEncryptedArtifactTestStore(t)
	content := bytes.Repeat([]byte("bruno-source-\x00"), 15_000)
	first, err := store.Put(context.Background(), "project-a", "sanitized", bytes.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if first.SizeBytes != int64(len(content)) || first.SHA256 == "" || first.BackendKey == "" {
		t.Fatalf("stored object = %+v", first)
	}
	second, err := store.Put(context.Background(), "project-a", "sanitized", bytes.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatalf("deduplicated Put: %v", err)
	}
	if second.BackendKey != first.BackendKey || second.SHA256 != first.SHA256 {
		t.Fatalf("deduplicated object = %+v, first = %+v", second, first)
	}
	decoded, err := store.ReadAll(context.Background(), OpenRequest{
		BackendKey: first.BackendKey,
		ProjectID:  "project-a",
		Kind:       "sanitized",
		SHA256:     first.SHA256,
		SizeBytes:  first.SizeBytes,
	}, int64(len(content)))
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(decoded, content) {
		t.Fatal("decrypted content differs from source")
	}
	if _, _, err := store.Open(context.Background(), OpenRequest{
		BackendKey: first.BackendKey,
		ProjectID:  "project-b",
		Kind:       "sanitized",
		SHA256:     first.SHA256,
		SizeBytes:  first.SizeBytes,
	}); err == nil || !strings.Contains(err.Error(), "another project") {
		t.Fatalf("cross-project Open error = %v", err)
	}
}

func TestEncryptedStoreReadOnlyModeOpensExistingObjectsAndRejectsWrites(t *testing.T) {
	root := t.TempDir()
	wrapper, err := NewLocalAESKeyWrapper(LocalKeyOptions{Key: bytes.Repeat([]byte{0x7c}, 32), KeyID: "test"})
	if err != nil {
		t.Fatal(err)
	}
	writable, err := NewEncryptedStore(StoreOptions{RootDir: root, Wrapper: wrapper})
	if err != nil {
		t.Fatal(err)
	}
	object, err := writable.Put(context.Background(), "project-a", "sanitized", strings.NewReader("bundle"), 1024)
	if err != nil {
		t.Fatal(err)
	}
	if err := writable.SaveSession(context.Background(), SessionDescriptor{
		ID:        "read-only-session",
		ProjectID: "project-a",
		Actor:     "actor-a",
		ExpiresAt: time.Now().UTC().Add(time.Minute),
	}, strings.NewReader(`{"secret":"retained"}`), 1024); err != nil {
		t.Fatal(err)
	}
	readOnly, err := NewEncryptedStore(StoreOptions{RootDir: root, Wrapper: wrapper, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	data, err := readOnly.ReadAll(context.Background(), OpenRequest{
		BackendKey: object.BackendKey,
		ProjectID:  "project-a",
		Kind:       "sanitized",
		SHA256:     object.SHA256,
		SizeBytes:  object.SizeBytes,
	}, 1024)
	if err != nil || string(data) != "bundle" {
		t.Fatalf("read-only ReadAll = %q, %v", data, err)
	}
	if _, err := readOnly.Put(context.Background(), "project-a", "sanitized", strings.NewReader("new"), 1024); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("read-only Put error = %v", err)
	}
	if err := readOnly.DeleteSession(context.Background(), "read-only-session", "project-a"); err == nil ||
		!strings.Contains(err.Error(), "read-only") {
		t.Fatalf("read-only DeleteSession error = %v", err)
	}
	reader, _, err := readOnly.OpenSession(context.Background(), "read-only-session", "project-a", "actor-a")
	if err != nil {
		t.Fatalf("read-only session was mutated: %v", err)
	}
	_ = reader.Close()
}

func TestEncryptedStorePersistsAndDeletesImportSession(t *testing.T) {
	store := newEncryptedArtifactTestStore(t)
	expiry := time.Now().UTC().Add(time.Minute)
	if err := store.SaveSession(context.Background(), SessionDescriptor{
		ID:        "session-1",
		ProjectID: "project-a",
		Actor:     "actor-a",
		ExpiresAt: expiry,
	}, strings.NewReader(`{"secret":"value"}`), 1024); err != nil {
		t.Fatal(err)
	}
	reader, descriptor, err := store.OpenSession(context.Background(), "session-1", "project-a", "actor-a")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil || string(data) != `{"secret":"value"}` || descriptor.ExpiresAt != expiry {
		t.Fatalf("session data=%q descriptor=%#v err=%v", data, descriptor, err)
	}
	if _, _, err := store.OpenSession(context.Background(), "session-1", "project-b", "actor-a"); err == nil {
		t.Fatal("cross-project session access succeeded")
	}
	if err := store.DeleteSession(context.Background(), "session-1", "project-a"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.OpenSession(context.Background(), "session-1", "project-a", "actor-a"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted session error = %v", err)
	}
}

func TestEncryptedStoreExpiredOpenPreservesCleanupLocator(t *testing.T) {
	store := newEncryptedArtifactTestStore(t)
	ctx := context.Background()
	source, err := store.PutBytes(ctx, "project-a", "raw", []byte("source"), 1024)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := "expired-cleanup-locator"
	if err := store.SaveSessionBytes(ctx, SessionDescriptor{
		ID:        sessionID,
		ProjectID: "project-a",
		Actor:     "actor-a",
		ExpiresAt: time.Now().UTC().Add(-time.Minute),
		RelatedObjects: []OpenRequest{{
			BackendKey: source.BackendKey,
			ProjectID:  "project-a",
			Kind:       "raw",
			SHA256:     source.SHA256,
			SizeBytes:  source.SizeBytes,
		}},
	}, []byte(`{"expired":true}`), 1024); err != nil {
		t.Fatal(err)
	}
	_, descriptor, err := store.OpenSession(ctx, sessionID, "project-a", "actor-a")
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("OpenSession error = %v", err)
	}
	if len(descriptor.RelatedObjects) != 1 || descriptor.RelatedObjects[0].BackendKey != source.BackendKey {
		t.Fatalf("expired descriptor lost related objects: %#v", descriptor)
	}
	if _, err := os.Stat(filepath.Join(store.root, ".sessions", sessionID+".json")); err != nil {
		t.Fatalf("expired cleanup locator was removed: %v", err)
	}
}

func TestEncryptedStorePublicationJournalAndExpiredReservationRecovery(t *testing.T) {
	store := newEncryptedArtifactTestStore(t)
	ctx := context.Background()
	payload := []byte("interrupted-publication")
	planned, err := store.PlanBytes("project-a", "raw", payload, 1024)
	if err != nil {
		t.Fatal(err)
	}
	publication := PublicationDescriptor{
		ID: "publication-a", ProjectID: "project-a",
		ExpiresAt: time.Now().UTC().Add(-time.Minute),
		Objects:   []OpenRequest{planned},
	}
	if err := store.SavePublication(publication); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutBytes(ctx, "project-a", "raw", payload, 1024); err != nil {
		t.Fatal(err)
	}
	expired, err := store.ListExpiredPublications(time.Now().UTC())
	if err != nil || len(expired) != 1 || expired[0].ID != publication.ID {
		t.Fatalf("expired publications = %#v, %v", expired, err)
	}
	if err := store.Delete(ctx, planned); err != nil {
		t.Fatal(err)
	}
	if err := store.DeletePublication(publication.ID, publication.ProjectID); err != nil {
		t.Fatal(err)
	}
	if expired, err := store.ListExpiredPublications(time.Now().UTC()); err != nil || len(expired) != 0 {
		t.Fatalf("deleted publications = %#v, %v", expired, err)
	}

	sessionPayload := []byte("orphaned-session-payload")
	sessionPlan, err := store.PlanBytes("project-a", "session", sessionPayload, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutBytes(ctx, "project-a", "session", sessionPayload, 1024); err != nil {
		t.Fatal(err)
	}
	reservation := sessionCapacityReservation{
		SessionID: "crashed-session", ProjectID: "project-a",
		ExpiresAt: time.Now().UTC().Add(-time.Minute), Payload: &sessionPlan,
	}
	reservationData, err := json.Marshal(reservation)
	if err != nil {
		t.Fatal(err)
	}
	capacityDir := filepath.Join(store.root, ".sessions", ".capacity")
	if err := os.MkdirAll(capacityDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(capacityDir, "slot-000000.json"), reservationData, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DeleteExpiredSessions(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadAll(ctx, sessionPlan, 1024); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphaned session payload survived reservation recovery: %v", err)
	}
}

func TestEncryptedStoreConsumesImportSessionOnlyOnce(t *testing.T) {
	store := newEncryptedArtifactTestStore(t)
	expiry := time.Now().UTC().Add(time.Minute)
	if err := store.SaveSession(context.Background(), SessionDescriptor{
		ID:        "session-consume",
		ProjectID: "project-a",
		Actor:     "actor-a",
		ExpiresAt: expiry,
	}, strings.NewReader(`{"secret":"value"}`), 1024); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ConsumeSession(context.Background(), "session-consume", "project-a", "actor-b", 1024); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("wrong-actor ConsumeSession error = %v, want os.ErrNotExist", err)
	}
	if _, _, err := store.ConsumeSession(context.Background(), "session-consume", "project-b", "actor-a", 1024); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("wrong-project ConsumeSession error = %v, want os.ErrNotExist", err)
	}
	payload, descriptor, err := store.ConsumeSession(context.Background(), "session-consume", "project-a", "actor-a", 1024)
	if err != nil {
		t.Fatalf("ConsumeSession: %v", err)
	}
	if string(payload) != `{"secret":"value"}` || descriptor.ID != "session-consume" {
		t.Fatalf("consumed payload=%q descriptor=%#v", payload, descriptor)
	}
	if _, _, err := store.ConsumeSession(context.Background(), "session-consume", "project-a", "actor-a", 1024); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("second ConsumeSession error = %v, want os.ErrNotExist", err)
	}
}

func TestEncryptedStoreSessionCapacityIsSharedAcrossReplicasAndRestarts(t *testing.T) {
	root := t.TempDir()
	wrapper, err := NewLocalAESKeyWrapper(LocalKeyOptions{Key: bytes.Repeat([]byte{0x3c}, 32), KeyID: "capacity-test"})
	if err != nil {
		t.Fatal(err)
	}
	newStore := func() *EncryptedStore {
		store, createErr := NewEncryptedStore(StoreOptions{
			RootDir: root, Wrapper: wrapper, ChunkSize: 64 << 10, SessionCapacity: 2,
		})
		if createErr != nil {
			t.Fatal(createErr)
		}
		return store
	}
	first := newStore()
	second := newStore()
	expires := time.Now().UTC().Add(time.Hour)
	for _, item := range []struct {
		store   *EncryptedStore
		id      string
		payload string
	}{{first, "capacity-a", "payload-a"}, {second, "capacity-b", "payload-b"}} {
		if saveErr := item.store.SaveSessionBytes(context.Background(), SessionDescriptor{
			ID: item.id, ProjectID: "project-a", Actor: "actor-a", ExpiresAt: expires,
		}, []byte(item.payload), 1024); saveErr != nil {
			t.Fatalf("SaveSessionBytes(%s): %v", item.id, saveErr)
		}
	}

	// A new process observes the same two atomic reservations after restart and
	// rejects admission before writing any encrypted payload or descriptor.
	restarted := newStore()
	const rejectedPayload = "must-not-be-persisted-at-capacity"
	err = restarted.SaveSessionBytes(context.Background(), SessionDescriptor{
		ID: "capacity-rejected", ProjectID: "project-a", Actor: "actor-a", ExpiresAt: expires,
	}, []byte(rejectedPayload), 1024)
	if !errors.Is(err, ErrSessionCapacity) {
		t.Fatalf("capacity error = %v, want ErrSessionCapacity", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, ".sessions", "capacity-rejected.json")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("rejected descriptor stat error = %v", statErr)
	}
	rejectedDigest := sha256.Sum256([]byte(rejectedPayload))
	rejectedKey := artifactBackendKey("project-a", "session", hex.EncodeToString(rejectedDigest[:]))
	rejectedPath, err := restarted.pathForBackendKey(rejectedKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(rejectedPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("capacity rejection persisted payload: %v", statErr)
	}

	if err := first.DeleteSession(context.Background(), "capacity-a", "project-a"); err != nil {
		t.Fatal(err)
	}
	if err := restarted.SaveSessionBytes(context.Background(), SessionDescriptor{
		ID: "capacity-c", ProjectID: "project-a", Actor: "actor-a", ExpiresAt: expires,
	}, []byte("payload-c"), 1024); err != nil {
		t.Fatalf("capacity was not released after delete: %v", err)
	}
	if _, _, err := second.ConsumeSession(context.Background(), "capacity-b", "project-a", "actor-a", 1024); err != nil {
		t.Fatal(err)
	}
	if err := newStore().SaveSessionBytes(context.Background(), SessionDescriptor{
		ID: "capacity-d", ProjectID: "project-a", Actor: "actor-a", ExpiresAt: expires,
	}, []byte("payload-d"), 1024); err != nil {
		t.Fatalf("capacity was not released after consume: %v", err)
	}
}

func TestEncryptedStoreKnownBytesNeverStagesPlaintextAndPrunesLegacyOrphans(t *testing.T) {
	root := t.TempDir()
	staging := filepath.Join(root, ".staging")
	if err := os.MkdirAll(staging, 0o700); err != nil {
		t.Fatal(err)
	}
	const orphanSecret = "legacy-crash-secret-never-retain"
	if err := os.WriteFile(filepath.Join(staging, "plain-crash"), []byte(orphanSecret), 0o600); err != nil {
		t.Fatal(err)
	}
	wrapper, err := NewLocalAESKeyWrapper(LocalKeyOptions{Key: bytes.Repeat([]byte{0x4d}, 32), KeyID: "no-plaintext-test"})
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewEncryptedStore(StoreOptions{RootDir: root, Wrapper: wrapper, ChunkSize: 64 << 10})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(staging, "plain-crash")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy plaintext orphan stat error = %v", err)
	}

	const objectSecret = "known-bytes-object-secret-never-stage"
	if _, err := store.PutBytes(context.Background(), "project-a", "raw", []byte(objectSecret), 1024); err != nil {
		t.Fatal(err)
	}
	const sessionSecret = "known-bytes-session-secret-never-stage"
	if err := store.SaveSessionBytes(context.Background(), SessionDescriptor{
		ID: "known-bytes-session", ProjectID: "project-a", Actor: "actor-a", ExpiresAt: time.Now().UTC().Add(time.Hour),
	}, []byte(sessionSecret), 1024); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{orphanSecret, objectSecret, sessionSecret} {
		walkErr := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			if strings.HasPrefix(entry.Name(), "plain-") {
				t.Errorf("plaintext staging artifact remains: %s", path)
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			if bytes.Contains(data, []byte(secret)) {
				t.Errorf("plaintext %q remains at rest in %s", secret, path)
			}
			return nil
		})
		if walkErr != nil {
			t.Fatal(walkErr)
		}
	}
}

func TestEncryptedStoreConsumeRestoresSessionAfterReferenceScanFailure(t *testing.T) {
	store := newEncryptedArtifactTestStore(t)
	if err := store.SaveSession(context.Background(), SessionDescriptor{
		ID: "session-retry", ProjectID: "project-a", Actor: "actor-a", ExpiresAt: time.Now().UTC().Add(time.Minute),
	}, strings.NewReader(`{"secret":"value"}`), 1024); err != nil {
		t.Fatal(err)
	}
	sessionDir := filepath.Join(store.root, ".sessions")
	corrupt := filepath.Join(sessionDir, "corrupt.json")
	if err := os.WriteFile(corrupt, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ConsumeSession(context.Background(), "session-retry", "project-a", "actor-a", 1024); err == nil {
		t.Fatal("consume succeeded while the durable reference set was unreadable")
	}
	if err := os.Remove(corrupt); err != nil {
		t.Fatal(err)
	}
	payload, _, err := store.ConsumeSession(context.Background(), "session-retry", "project-a", "actor-a", 1024)
	if err != nil || string(payload) != `{"secret":"value"}` {
		t.Fatalf("retried consume payload=%q error=%v", payload, err)
	}
}

func TestEncryptedStoreConcurrentConsumersHaveSingleWinner(t *testing.T) {
	store := newEncryptedArtifactTestStore(t)
	if err := store.SaveSession(context.Background(), SessionDescriptor{
		ID:        "session-race",
		ProjectID: "project-a",
		Actor:     "actor-a",
		ExpiresAt: time.Now().UTC().Add(time.Minute),
	}, strings.NewReader(`{"secret":"value"}`), 1024); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, _, err := store.ConsumeSession(context.Background(), "session-race", "project-a", "actor-a", 1024)
			results <- err
		}()
	}
	close(start)
	successes := 0
	for range 2 {
		if err := <-results; err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful concurrent consumers = %d, want 1", successes)
	}
}

func TestEncryptedStoreConcurrentSharedPayloadConsumersDeleteLastReference(t *testing.T) {
	store := newEncryptedArtifactTestStore(t)
	expiry := time.Now().UTC().Add(time.Minute)
	for _, id := range []string{"shared-consume-a", "shared-consume-b"} {
		if err := store.SaveSession(context.Background(), SessionDescriptor{ID: id, ProjectID: "project-a", Actor: "actor-a", ExpiresAt: expiry}, strings.NewReader(`{"same":"payload"}`), 1024); err != nil {
			t.Fatal(err)
		}
	}
	reader, descriptor, err := store.OpenSession(context.Background(), "shared-consume-a", "project-a", "actor-a")
	if err != nil {
		t.Fatal(err)
	}
	_ = reader.Close()
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, id := range []string{"shared-consume-a", "shared-consume-b"} {
		go func(sessionID string) {
			<-start
			_, _, err := store.ConsumeSession(context.Background(), sessionID, "project-a", "actor-a", 1024)
			results <- err
		}(id)
	}
	close(start)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("ConsumeSession: %v", err)
		}
	}
	if _, _, err := store.Open(context.Background(), OpenRequest{
		BackendKey: descriptor.BackendKey, ProjectID: descriptor.ProjectID, Kind: "session",
		SHA256: descriptor.SHA256, SizeBytes: descriptor.SizeBytes,
	}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("shared consumed payload open error = %v, want os.ErrNotExist", err)
	}
}

func TestEncryptedStoreConcurrentSharedPayloadDeletesDeleteLastReference(t *testing.T) {
	store := newEncryptedArtifactTestStore(t)
	expiry := time.Now().UTC().Add(time.Minute)
	for _, id := range []string{"shared-delete-a", "shared-delete-b"} {
		if err := store.SaveSession(context.Background(), SessionDescriptor{ID: id, ProjectID: "project-a", Actor: "actor-a", ExpiresAt: expiry}, strings.NewReader(`{"same":"payload"}`), 1024); err != nil {
			t.Fatal(err)
		}
	}
	reader, descriptor, err := store.OpenSession(context.Background(), "shared-delete-a", "project-a", "actor-a")
	if err != nil {
		t.Fatal(err)
	}
	_ = reader.Close()
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, id := range []string{"shared-delete-a", "shared-delete-b"} {
		go func(sessionID string) {
			<-start
			results <- store.DeleteSession(context.Background(), sessionID, "project-a")
		}(id)
	}
	close(start)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("DeleteSession: %v", err)
		}
	}
	if _, _, err := store.Open(context.Background(), OpenRequest{
		BackendKey: descriptor.BackendKey, ProjectID: descriptor.ProjectID, Kind: "session",
		SHA256: descriptor.SHA256, SizeBytes: descriptor.SizeBytes,
	}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("shared deleted payload open error = %v, want os.ErrNotExist", err)
	}
}

func TestEncryptedStoreSessionClaimRetainsAndRecoversPayload(t *testing.T) {
	store := newEncryptedArtifactTestStore(t)
	ctx := context.Background()
	const sessionID = "leased-session"
	if err := store.SaveSession(ctx, SessionDescriptor{
		ID:        sessionID,
		ProjectID: "project-a",
		Actor:     "actor-a",
		ExpiresAt: time.Now().UTC().Add(time.Minute),
	}, strings.NewReader(`{"secret":"retained"}`), 1024); err != nil {
		t.Fatal(err)
	}
	payload, descriptor, err := store.ClaimSession(ctx, sessionID, "project-a", "actor-a", 1024)
	if err != nil || string(payload) != `{"secret":"retained"}` {
		t.Fatalf("ClaimSession payload = %q, err = %v", payload, err)
	}
	clear(payload)
	if _, _, err := store.Open(ctx, OpenRequest{
		BackendKey: descriptor.BackendKey,
		ProjectID:  descriptor.ProjectID,
		Kind:       "session",
		SHA256:     descriptor.SHA256,
		SizeBytes:  descriptor.SizeBytes,
	}); err != nil {
		t.Fatalf("claimed encrypted payload was deleted: %v", err)
	}
	if _, _, err := store.OpenSession(ctx, sessionID, "project-a", "actor-a"); err == nil ||
		!strings.Contains(err.Error(), "already being consumed") {
		t.Fatalf("fresh claimed session open error = %v", err)
	}

	claimPath := filepath.Join(store.root, ".sessions", sessionID+".claim.json")
	stale := time.Now().UTC().Add(-sessionClaimLease - time.Second)
	if err := os.Chtimes(claimPath, stale, stale); err != nil {
		t.Fatal(err)
	}
	reader, recovered, err := store.OpenSession(ctx, sessionID, "project-a", "actor-a")
	if err != nil {
		t.Fatalf("recover stale claim: %v", err)
	}
	_ = reader.Close()
	if recovered.ID != sessionID {
		t.Fatalf("recovered descriptor = %#v", recovered)
	}

	_, reclaimed, err := store.ClaimSession(ctx, sessionID, "project-a", "actor-a", 1024)
	if err != nil {
		t.Fatalf("reclaim recovered session: %v", err)
	}
	if err := store.CompleteSessionClaim(ctx, sessionID, "project-a", "actor-a", reclaimed.ClaimToken); err != nil {
		t.Fatalf("CompleteSessionClaim: %v", err)
	}
	if _, _, err := store.Open(ctx, OpenRequest{
		BackendKey: descriptor.BackendKey,
		ProjectID:  descriptor.ProjectID,
		Kind:       "session",
		SHA256:     descriptor.SHA256,
		SizeBytes:  descriptor.SizeBytes,
	}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("completed session payload open error = %v, want os.ErrNotExist", err)
	}
}

func TestEncryptedStoreSaveSessionFailureCleansEncryptedPayload(t *testing.T) {
	store := newEncryptedArtifactTestStore(t)
	if err := os.WriteFile(filepath.Join(store.root, ".sessions"), []byte("blocks session directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := store.SaveSession(context.Background(), SessionDescriptor{
		ID:        "session-failure",
		ProjectID: "project-a",
		Actor:     "actor-a",
		ExpiresAt: time.Now().UTC().Add(time.Minute),
	}, strings.NewReader(`{"secret":"must-not-be-orphaned"}`), 1024)
	if err == nil {
		t.Fatal("SaveSession unexpectedly succeeded")
	}
	projectRoot := filepath.Join(store.root, "bruno", projectDigest("project-a")[:16])
	walkErr := filepath.WalkDir(projectRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			t.Errorf("orphaned session payload %s", path)
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, os.ErrNotExist) {
		t.Fatal(walkErr)
	}
}

func TestEncryptedStoreDeleteExpiredSessionsSkipsInFlightClaims(t *testing.T) {
	store := newEncryptedArtifactTestStore(t)
	now := time.Now().UTC()
	related := OpenRequest{BackendKey: "bruno/example/raw/aa/source.abx", ProjectID: "project-a", Kind: "raw", SHA256: strings.Repeat("a", 64), SizeBytes: 10}
	for _, descriptor := range []SessionDescriptor{
		{ID: "expired", ProjectID: "project-a", Actor: "actor-a", ExpiresAt: now.Add(-time.Minute), RelatedObjects: []OpenRequest{related}},
		{ID: "active", ProjectID: "project-a", Actor: "actor-a", ExpiresAt: now.Add(time.Minute)},
	} {
		if err := store.SaveSession(context.Background(), descriptor, strings.NewReader(`{"payload":true}`), 1024); err != nil {
			t.Fatal(err)
		}
	}
	sessionDir := filepath.Join(store.root, ".sessions")
	if err := os.Rename(filepath.Join(sessionDir, "expired.json"), filepath.Join(sessionDir, "expired.claim.json")); err != nil {
		t.Fatal(err)
	}
	expired, err := store.DeleteExpiredSessions(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(expired) != 0 {
		t.Fatalf("expired sessions = %#v", expired)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, "expired.claim.json")); err != nil {
		t.Fatalf("retryable expired claim stat error = %v", err)
	}
	stale := now.Add(-sessionClaimLease - time.Second)
	if err := os.Chtimes(filepath.Join(sessionDir, "expired.claim.json"), stale, stale); err != nil {
		t.Fatalf("age expired claim: %v", err)
	}
	expired, err = store.DeleteExpiredSessions(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(expired) != 1 || expired[0].ID != "expired" {
		t.Fatalf("stale expired sessions = %#v, want expired", expired)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, "expired.claim.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("finalized expired claim stat error = %v", err)
	}
	reader, _, err := store.OpenSession(context.Background(), "active", "project-a", "actor-a")
	if err != nil {
		t.Fatalf("active session: %v", err)
	}
	_ = reader.Close()
}

func TestEncryptedStoreRecoveredClaimFencesPreviousOwner(t *testing.T) {
	store := newEncryptedArtifactTestStore(t)
	ctx := context.Background()
	const sessionID = "fenced-session"
	if err := store.SaveSession(ctx, SessionDescriptor{
		ID:        sessionID,
		ProjectID: "project-a",
		Actor:     "actor-a",
		ExpiresAt: time.Now().UTC().Add(time.Minute),
	}, strings.NewReader(`{"secret":"retained"}`), 1024); err != nil {
		t.Fatal(err)
	}

	firstPayload, firstClaim, err := store.ClaimSession(ctx, sessionID, "project-a", "actor-a", 1024)
	if err != nil {
		t.Fatalf("first ClaimSession: %v", err)
	}
	clear(firstPayload)
	claimPath := filepath.Join(store.root, ".sessions", sessionID+".claim.json")
	stale := time.Now().UTC().Add(-sessionClaimLease - time.Second)
	if err := os.Chtimes(claimPath, stale, stale); err != nil {
		t.Fatal(err)
	}

	secondStore, err := NewEncryptedStore(StoreOptions{
		RootDir: store.root,
		Wrapper: store.wrapper,
	})
	if err != nil {
		t.Fatal(err)
	}
	secondPayload, secondClaim, err := secondStore.ClaimSession(ctx, sessionID, "project-a", "actor-a", 1024)
	if err != nil {
		t.Fatalf("recovered ClaimSession: %v", err)
	}
	clear(secondPayload)
	if firstClaim.ClaimToken == "" || secondClaim.ClaimToken == "" ||
		firstClaim.ClaimToken == secondClaim.ClaimToken {
		t.Fatalf("claim tokens were not independently fenced: first=%q second=%q", firstClaim.ClaimToken, secondClaim.ClaimToken)
	}
	if err := store.TouchSessionClaim(
		sessionID, "project-a", "actor-a", firstClaim.ClaimToken, time.Now().UTC(),
	); !errors.Is(err, ErrSessionClaimLost) {
		t.Fatalf("stale TouchSessionClaim error = %v, want ErrSessionClaimLost", err)
	}
	if err := store.ReleaseSessionClaim(
		sessionID, "project-a", "actor-a", firstClaim.ClaimToken,
	); !errors.Is(err, ErrSessionClaimLost) {
		t.Fatalf("stale ReleaseSessionClaim error = %v, want ErrSessionClaimLost", err)
	}
	if err := store.CompleteSessionClaim(
		ctx, sessionID, "project-a", "actor-a", firstClaim.ClaimToken,
	); !errors.Is(err, ErrSessionClaimLost) {
		t.Fatalf("stale CompleteSessionClaim error = %v, want ErrSessionClaimLost", err)
	}
	if err := secondStore.TouchSessionClaim(
		sessionID, "project-a", "actor-a", secondClaim.ClaimToken, time.Now().UTC(),
	); err != nil {
		t.Fatalf("current TouchSessionClaim: %v", err)
	}
	if err := secondStore.CompleteSessionClaim(
		ctx, sessionID, "project-a", "actor-a", secondClaim.ClaimToken,
	); err != nil {
		t.Fatalf("current CompleteSessionClaim: %v", err)
	}
}

func TestEncryptedStoreFinalizeCommittedSessionAfterPayloadDeletion(t *testing.T) {
	store := newEncryptedArtifactTestStore(t)
	ctx := context.Background()
	const sessionID = "committed-crash-session"
	if err := store.SaveSession(ctx, SessionDescriptor{
		ID:        sessionID,
		ProjectID: "project-a",
		Actor:     "actor-a",
		ExpiresAt: time.Now().UTC().Add(time.Minute),
	}, strings.NewReader(`{"secret":"retained"}`), 1024); err != nil {
		t.Fatal(err)
	}
	payload, descriptor, err := store.ClaimSession(ctx, sessionID, "project-a", "actor-a", 1024)
	if err != nil {
		t.Fatalf("ClaimSession: %v", err)
	}
	clear(payload)
	if err := store.Delete(ctx, OpenRequest{
		BackendKey: descriptor.BackendKey,
		ProjectID:  descriptor.ProjectID,
		Kind:       "session",
		SHA256:     descriptor.SHA256,
		SizeBytes:  descriptor.SizeBytes,
	}); err != nil {
		t.Fatalf("simulate post-commit payload deletion: %v", err)
	}

	if err := store.FinalizeCommittedSession(ctx, sessionID, "project-a", "actor-a"); err != nil {
		t.Fatalf("FinalizeCommittedSession: %v", err)
	}
	claimPath := filepath.Join(store.root, ".sessions", sessionID+".claim.json")
	if _, err := os.Stat(claimPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("claim descriptor survived committed-session finalization: %v", err)
	}
}

func TestEncryptedStoreDeleteProjectSweepsRestartSessionsAndPreservesOtherProjects(t *testing.T) {
	store := newEncryptedArtifactTestStore(t)
	ctx := context.Background()
	projectAObject, err := store.Put(ctx, "project-a", "raw", strings.NewReader("project-a-source"), 1024)
	if err != nil {
		t.Fatal(err)
	}
	projectBObject, err := store.Put(ctx, "project-b", "raw", strings.NewReader("project-b-source"), 1024)
	if err != nil {
		t.Fatal(err)
	}
	expiry := time.Now().UTC().Add(time.Minute)
	for _, descriptor := range []SessionDescriptor{
		{ID: "session-a", ProjectID: "project-a", Actor: "actor-a", ExpiresAt: expiry},
		{ID: "session-b", ProjectID: "project-b", Actor: "actor-a", ExpiresAt: expiry},
	} {
		if err := store.SaveSession(ctx, descriptor, strings.NewReader(`{"session":"payload"}`), 1024); err != nil {
			t.Fatal(err)
		}
	}
	projectAPlan, err := store.PlanBytes("project-a", "raw", []byte("publication-a"), 1024)
	if err != nil {
		t.Fatal(err)
	}
	projectBPlan, err := store.PlanBytes("project-b", "raw", []byte("publication-b"), 1024)
	if err != nil {
		t.Fatal(err)
	}
	for _, publication := range []PublicationDescriptor{
		{ID: "project-a-publication", ProjectID: "project-a", ExpiresAt: expiry, Objects: []OpenRequest{projectAPlan}},
		{ID: "project-b-publication", ProjectID: "project-b", ExpiresAt: expiry, Objects: []OpenRequest{projectBPlan}},
	} {
		if err := store.SavePublication(publication); err != nil {
			t.Fatal(err)
		}
	}
	// A claim descriptor models a controller restart after a session was claimed
	// but before its encrypted cleanup record could be removed.
	sessionDir := filepath.Join(store.root, ".sessions")
	if err := os.Rename(filepath.Join(sessionDir, "session-a.json"), filepath.Join(sessionDir, "session-a.claim.json")); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteProject(ctx, "project-a"); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, "session-a.claim.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("project-a claim descriptor stat error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.root, ".publications", "project-a-publication.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("project-a publication stat error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.root, ".publications", "project-b-publication.json")); err != nil {
		t.Fatalf("project-b publication was removed: %v", err)
	}
	if _, _, err := store.Open(ctx, OpenRequest{
		BackendKey: projectAObject.BackendKey,
		ProjectID:  "project-a",
		Kind:       "raw",
		SHA256:     projectAObject.SHA256,
		SizeBytes:  projectAObject.SizeBytes,
	}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("project-a artifact open error = %v, want os.ErrNotExist", err)
	}
	projectBPayload, err := store.ReadAll(ctx, OpenRequest{
		BackendKey: projectBObject.BackendKey,
		ProjectID:  "project-b",
		Kind:       "raw",
		SHA256:     projectBObject.SHA256,
		SizeBytes:  projectBObject.SizeBytes,
	}, 1024)
	if err != nil || string(projectBPayload) != "project-b-source" {
		t.Fatalf("project-b artifact = %q, %v", projectBPayload, err)
	}
	reader, _, err := store.OpenSession(ctx, "session-b", "project-b", "actor-a")
	if err != nil {
		t.Fatalf("project-b session: %v", err)
	}
	_ = reader.Close()
}

func TestEncryptedStoreProjectDeletionTombstoneRejectsCrossStoreWrites(t *testing.T) {
	first := newEncryptedArtifactTestStore(t)
	second, err := NewEncryptedStore(StoreOptions{RootDir: first.root, Wrapper: first.wrapper})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Put(context.Background(), "project-deleted", "raw", strings.NewReader("before deletion"), 1024); err != nil {
		t.Fatal(err)
	}
	if err := first.DeleteProject(context.Background(), "project-deleted"); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Put(context.Background(), "project-deleted", "raw", strings.NewReader("after deletion"), 1024); !errors.Is(err, ErrProjectDeleted) {
		t.Fatalf("post-delete Put error = %v, want ErrProjectDeleted", err)
	}
	if err := second.SaveSession(context.Background(), SessionDescriptor{
		ID: "post-delete-session", ProjectID: "project-deleted", Actor: "actor-a", ExpiresAt: time.Now().UTC().Add(time.Minute),
	}, strings.NewReader(`{"secret":"blocked"}`), 1024); !errors.Is(err, ErrProjectDeleted) {
		t.Fatalf("post-delete SaveSession error = %v, want ErrProjectDeleted", err)
	}
}

func TestEncryptedStoreProjectDeletionRacesCrossStorePutWithoutLeavingArtifact(t *testing.T) {
	first := newEncryptedArtifactTestStore(t)
	second, err := NewEncryptedStore(StoreOptions{RootDir: first.root, Wrapper: first.wrapper})
	if err != nil {
		t.Fatal(err)
	}
	for index := range 24 {
		projectID := fmt.Sprintf("project-race-%d", index)
		start := make(chan struct{})
		results := make(chan error, 2)
		go func() {
			<-start
			_, err := first.Put(context.Background(), projectID, "raw", bytes.NewReader(bytes.Repeat([]byte("x"), 256<<10)), 1<<20)
			results <- err
		}()
		go func() {
			<-start
			results <- second.DeleteProject(context.Background(), projectID)
		}()
		close(start)
		for range 2 {
			if err := <-results; err != nil && !errors.Is(err, ErrProjectDeleted) {
				t.Fatalf("race %d error = %v", index, err)
			}
		}
		projectRoot := filepath.Join(first.root, "bruno", projectDigest(projectID)[:16])
		walkErr := filepath.WalkDir(projectRoot, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() {
				t.Errorf("race %d retained artifact %s", index, path)
			}
			return nil
		})
		if walkErr != nil && !errors.Is(walkErr, os.ErrNotExist) {
			t.Fatal(walkErr)
		}
	}
}

func TestEncryptedStoreProjectDeletionRacesSessionDescriptorRename(t *testing.T) {
	first := newEncryptedArtifactTestStore(t)
	second, err := NewEncryptedStore(StoreOptions{RootDir: first.root, Wrapper: first.wrapper})
	if err != nil {
		t.Fatal(err)
	}
	for index := range 16 {
		projectID := fmt.Sprintf("project-session-race-%d", index)
		sessionID := fmt.Sprintf("session-race-%d", index)
		start := make(chan struct{})
		results := make(chan error, 2)
		go func() {
			<-start
			results <- first.SaveSession(context.Background(), SessionDescriptor{
				ID: sessionID, ProjectID: projectID, Actor: "actor-a", ExpiresAt: time.Now().UTC().Add(time.Minute),
			}, strings.NewReader(`{"secret":"race"}`), 1024)
		}()
		go func() {
			<-start
			results <- second.DeleteProject(context.Background(), projectID)
		}()
		close(start)
		for range 2 {
			if err := <-results; err != nil && !errors.Is(err, ErrProjectDeleted) {
				t.Fatalf("session race %d error = %v", index, err)
			}
		}
		if _, err := os.Stat(filepath.Join(first.root, ".sessions", sessionID+".json")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("session race %d descriptor stat error = %v", index, err)
		}
		projectRoot := filepath.Join(first.root, "bruno", projectDigest(projectID)[:16])
		walkErr := filepath.WalkDir(projectRoot, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() && !strings.HasPrefix(entry.Name(), ".encrypted-") {
				t.Errorf("session race %d retained artifact %s", index, path)
			}
			return nil
		})
		if walkErr != nil && !errors.Is(walkErr, os.ErrNotExist) {
			t.Fatal(walkErr)
		}
	}
}

func TestEncryptedStoreRejectsQuotaAndDetectsCiphertextTampering(t *testing.T) {
	store := newEncryptedArtifactTestStore(t)
	if _, err := store.Put(context.Background(), "project-a", "raw", strings.NewReader("12345"), 4); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("quota error = %v", err)
	}
	object, err := store.Put(context.Background(), "project-a", "raw", strings.NewReader("sensitive source payload"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.pathForBackendKey(object.BackendKey)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(target, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := file.Stat()
	if _, err := file.Seek(-1, io.SeekEnd); err != nil {
		t.Fatal(err)
	}
	last := []byte{0}
	if _, err := file.Read(last); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(-1, io.SeekEnd); err != nil {
		t.Fatal(err)
	}
	last[0] ^= 0xff
	if _, err := file.Write(last); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 {
		t.Fatal("encrypted artifact was unexpectedly empty")
	}
	reader, _, err := store.Open(context.Background(), OpenRequest{
		BackendKey: object.BackendKey,
		ProjectID:  "project-a",
		Kind:       "raw",
		SHA256:     object.SHA256,
		SizeBytes:  object.SizeBytes,
	})
	if err != nil {
		t.Fatalf("Open tampered object header: %v", err)
	}
	_, readErr := io.ReadAll(reader)
	_ = reader.Close()
	if readErr == nil || !strings.Contains(readErr.Error(), "decrypt") {
		t.Fatalf("tampered object read error = %v", readErr)
	}
	if _, err := store.Put(context.Background(), "project-a", "raw", strings.NewReader("sensitive source payload"), 1<<20); err == nil {
		t.Fatal("deduplicated Put accepted a corrupt existing encrypted object")
	}
}

func TestLocalKeyWrapperAuthenticatesArtifactContext(t *testing.T) {
	wrapper, err := NewLocalAESKeyWrapper(LocalKeyOptions{Key: bytes.Repeat([]byte{0x42}, 32), KeyID: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := wrapper.WrapKey(context.Background(), []byte("data-key"), []byte("project-a"))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := wrapper.UnwrapKey(context.Background(), wrapped, []byte("project-a"))
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != "data-key" {
		t.Fatalf("unwrapped = %q", decoded)
	}
	if _, err := wrapper.UnwrapKey(context.Background(), wrapped, []byte("project-b")); err == nil {
		t.Fatal("unwrap succeeded with different authenticated context")
	}
}

func TestAzureKeyVaultWrapperUsesVersionedWrapAndUnwrapAPI(t *testing.T) {
	var mu sync.Mutex
	var operations []string
	client := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Authorization") != "Bearer token" {
			t.Fatalf("authorization = %q", request.Header.Get("Authorization"))
		}
		var payload map[string]string
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["alg"] != keyVaultWrapAlgorithm {
			t.Fatalf("algorithm = %q", payload["alg"])
		}
		operation := "unknown"
		if strings.Contains(request.URL.Path, "/wrapkey") {
			operation = "wrapkey"
		}
		if strings.Contains(request.URL.Path, "/unwrapkey") {
			operation = "unwrapkey"
		}
		mu.Lock()
		operations = append(operations, operation)
		mu.Unlock()
		body := io.NopCloser(strings.NewReader(`{"value":"` + payload["value"] + `"}`))
		return &http.Response{StatusCode: http.StatusOK, Body: body, Header: make(http.Header)}, nil
	})}
	wrapper, err := NewAzureKeyVaultKeyWrapper(
		"https://vault.example.test/keys/bruno/version-1",
		staticTokenCredential{},
		client,
	)
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := wrapper.WrapKey(context.Background(), []byte("01234567890123456789012345678901"), nil)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := wrapper.UnwrapKey(context.Background(), wrapped, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != "01234567890123456789012345678901" {
		t.Fatalf("unwrapped = %q", decoded)
	}
	if strings.Join(operations, ",") != "wrapkey,unwrapkey" {
		t.Fatalf("operations = %v", operations)
	}
}

func TestStoreResolverStreamsProjectScopedArtifact(t *testing.T) {
	objects := newEncryptedArtifactTestStore(t)
	content := []byte("deterministic sanitized bundle")
	object, err := objects.Put(context.Background(), "project-a", "sanitized", bytes.NewReader(content), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	metadata := &fakeArtifactMetadataReader{artifact: &storage.BrunoSourceArtifact{
		ID:             "artifact-1",
		ProjectID:      "project-a",
		ArtifactKind:   "sanitized",
		BackendKey:     object.BackendKey,
		SHA256:         object.SHA256,
		SizeBytes:      object.SizeBytes,
		LifecycleState: "active",
	}}
	resolver := &StoreResolver{Objects: objects, Metadata: metadata}
	restore := SetResolver(resolver)
	defer restore()
	reader, size, err := Open(context.Background(), Request{
		Reference: "artifact://artifact-1",
		ProjectID: "project-a",
		SHA256:    object.SHA256,
	})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	if size != int64(len(content)) || !bytes.Equal(decoded, content) {
		t.Fatalf("resolved size/content = %d %q", size, decoded)
	}
	if _, _, err := Open(context.Background(), Request{
		Reference: "artifact://artifact-1",
		ProjectID: "project-a",
	}); err == nil || !strings.Contains(err.Error(), "requires a SHA-256 digest") {
		t.Fatalf("digest-less resolver error = %v", err)
	}
	if _, _, err := Open(context.Background(), Request{
		Reference: "artifact://artifact-1",
		ProjectID: "project-a",
		SHA256:    strings.Repeat("0", 64),
	}); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatched descriptor error = %v", err)
	}
	metadata.artifact.ArtifactKind = "raw"
	if _, _, err := Open(context.Background(), Request{
		Reference: "artifact://artifact-1",
		ProjectID: "project-a",
		SHA256:    object.SHA256,
	}); err == nil || !strings.Contains(err.Error(), "sanitized") {
		t.Fatalf("raw artifact resolver error = %v", err)
	}
	metadata.artifact.ArtifactKind = "sanitized"
	if _, _, err := Open(context.Background(), Request{
		Reference: "artifact://artifact-1",
		ProjectID: "project-b",
		SHA256:    object.SHA256,
	}); err == nil {
		t.Fatal("cross-project resolver request succeeded")
	}
}

func TestSourcePublicationLockSerializesSharedStores(t *testing.T) {
	root := t.TempDir()
	wrapper, err := NewLocalAESKeyWrapper(LocalKeyOptions{
		Key: bytes.Repeat([]byte{0x5a}, 32), KeyID: "publication-lock-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := NewEncryptedStore(StoreOptions{RootDir: root, Wrapper: wrapper})
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewEncryptedStore(StoreOptions{RootDir: root, Wrapper: wrapper})
	if err != nil {
		t.Fatal(err)
	}
	unlockFirst, err := first.LockSourcePublication()
	if err != nil {
		t.Fatal(err)
	}
	acquired := make(chan func() error, 1)
	failures := make(chan error, 1)
	go func() {
		unlockSecond, lockErr := second.LockSourcePublication()
		if lockErr != nil {
			failures <- lockErr
			return
		}
		acquired <- unlockSecond
	}()
	select {
	case unlockSecond := <-acquired:
		_ = unlockSecond()
		_ = unlockFirst()
		t.Fatal("second store acquired the source-publication lock before release")
	case lockErr := <-failures:
		_ = unlockFirst()
		t.Fatal(lockErr)
	case <-time.After(75 * time.Millisecond):
	}
	if err := unlockFirst(); err != nil {
		t.Fatal(err)
	}
	select {
	case unlockSecond := <-acquired:
		if err := unlockSecond(); err != nil {
			t.Fatal(err)
		}
	case lockErr := <-failures:
		t.Fatal(lockErr)
	case <-time.After(5 * time.Second):
		t.Fatal("second store did not acquire the released source-publication lock")
	}
}

func TestSessionRelatedReferenceScanSerializesClaimRename(t *testing.T) {
	root := t.TempDir()
	wrapper, err := NewLocalAESKeyWrapper(LocalKeyOptions{
		Key: bytes.Repeat([]byte{0x61}, 32), KeyID: "reference-lock-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := NewEncryptedStore(StoreOptions{RootDir: root, Wrapper: wrapper})
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewEncryptedStore(StoreOptions{RootDir: root, Wrapper: wrapper})
	if err != nil {
		t.Fatal(err)
	}
	source, err := first.PutBytes(context.Background(), "project-a", "raw", []byte("source"), 1024)
	if err != nil {
		t.Fatal(err)
	}
	const sessionID = "reference-transition"
	if err := first.SaveSessionBytes(context.Background(), SessionDescriptor{
		ID:        sessionID,
		ProjectID: "project-a",
		Actor:     "actor-a",
		ExpiresAt: time.Now().UTC().Add(time.Minute),
		RelatedObjects: []OpenRequest{{
			BackendKey: source.BackendKey,
			ProjectID:  "project-a",
			Kind:       "raw",
			SHA256:     source.SHA256,
			SizeBytes:  source.SizeBytes,
		}},
	}, []byte(`{"session":true}`), 1024); err != nil {
		t.Fatal(err)
	}

	unlock, err := first.lockSessionClaims()
	if err != nil {
		t.Fatal(err)
	}
	lockHeld := true
	defer func() {
		if lockHeld {
			_ = unlock()
		}
	}()
	if referenced, err := first.isSessionRelatedObjectReferencedLocked(source.BackendKey, ""); err != nil || !referenced {
		t.Fatalf("locked reference scan = %v, %v", referenced, err)
	}

	result := make(chan struct {
		referenced bool
		err        error
	}, 1)
	go func() {
		referenced, scanErr := second.IsSessionRelatedObjectReferenced(source.BackendKey, "")
		result <- struct {
			referenced bool
			err        error
		}{referenced: referenced, err: scanErr}
	}()
	select {
	case value := <-result:
		t.Fatalf("reference scan bypassed claim lock: %+v", value)
	case <-time.After(75 * time.Millisecond):
	}
	sessionDir := filepath.Join(root, ".sessions")
	if err := first.renameDurably(
		filepath.Join(sessionDir, sessionID+".json"),
		filepath.Join(sessionDir, sessionID+".claim.json"),
	); err != nil {
		t.Fatal(err)
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	lockHeld = false
	select {
	case value := <-result:
		if value.err != nil || !value.referenced {
			t.Fatalf("reference scan after claim transition = %+v", value)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reference scan did not resume after claim transition")
	}
}

func TestDeleteExpiredSessionsSerializesSharedPayloadClaimTransition(t *testing.T) {
	store := newEncryptedArtifactTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	sweepAt := now.Add(2 * time.Minute)
	const (
		expiredID = "shared-expired"
		activeID  = "shared-active"
	)
	for _, descriptor := range []SessionDescriptor{
		{ID: expiredID, ProjectID: "project-a", Actor: "actor-a", ExpiresAt: now.Add(time.Minute)},
		{ID: activeID, ProjectID: "project-a", Actor: "actor-a", ExpiresAt: now.Add(time.Hour)},
	} {
		if err := store.SaveSession(
			ctx, descriptor, strings.NewReader(`{"same":"payload"}`), 1024,
		); err != nil {
			t.Fatal(err)
		}
	}
	reader, shared, err := store.OpenSession(ctx, activeID, "project-a", "actor-a")
	if err != nil {
		t.Fatal(err)
	}
	_ = reader.Close()

	unlock, err := store.lockSessionClaims()
	if err != nil {
		t.Fatal(err)
	}
	lockHeld := true
	defer func() {
		if lockHeld {
			_ = unlock()
		}
	}()
	result := make(chan struct {
		expired []SessionDescriptor
		err     error
	}, 1)
	go func() {
		expired, sweepErr := store.DeleteExpiredSessions(ctx, sweepAt)
		result <- struct {
			expired []SessionDescriptor
			err     error
		}{expired: expired, err: sweepErr}
	}()
	select {
	case value := <-result:
		t.Fatalf("expiry sweep bypassed claim-transition lock: %+v", value)
	case <-time.After(75 * time.Millisecond):
	}

	sessionDir := filepath.Join(store.root, ".sessions")
	activePath := filepath.Join(sessionDir, activeID+".json")
	activeClaimPath := filepath.Join(sessionDir, activeID+".claim.json")
	if err := store.renameDurably(activePath, activeClaimPath); err != nil {
		t.Fatal(err)
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	lockHeld = false

	select {
	case value := <-result:
		if value.err != nil {
			t.Fatal(value.err)
		}
		if len(value.expired) != 1 || value.expired[0].ID != expiredID {
			t.Fatalf("expired sessions = %#v, want %q", value.expired, expiredID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("expiry sweep did not resume after claim transition")
	}
	if _, _, err := store.Open(ctx, OpenRequest{
		BackendKey: shared.BackendKey,
		ProjectID:  shared.ProjectID,
		Kind:       "session",
		SHA256:     shared.SHA256,
		SizeBytes:  shared.SizeBytes,
	}); err != nil {
		t.Fatalf("active claim's shared payload was deleted: %v", err)
	}
	if err := store.renameDurably(activeClaimPath, activePath); err != nil {
		t.Fatal(err)
	}
}

func TestEncryptedTemporaryCleanupIsLockedAndProjectScoped(t *testing.T) {
	root := t.TempDir()
	wrapper, err := NewLocalAESKeyWrapper(LocalKeyOptions{
		Key: bytes.Repeat([]byte{0x62}, 32), KeyID: "temp-cleanup-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := NewEncryptedStore(StoreOptions{RootDir: root, Wrapper: wrapper})
	if err != nil {
		t.Fatal(err)
	}
	projectRoot := filepath.Join(root, "bruno", projectDigest("project-a")[:16], "raw", "aa")
	if err := os.MkdirAll(projectRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	activeTemp := filepath.Join(projectRoot, ".encrypted-active")
	if err := os.WriteFile(activeTemp, []byte("ciphertext"), 0o600); err != nil {
		t.Fatal(err)
	}
	unlockWriter, err := first.lockEncryptedObjectWrites()
	if err != nil {
		t.Fatal(err)
	}
	type openResult struct {
		store *EncryptedStore
		err   error
	}
	opened := make(chan openResult, 1)
	go func() {
		store, openErr := NewEncryptedStore(StoreOptions{RootDir: root, Wrapper: wrapper})
		opened <- openResult{store: store, err: openErr}
	}()
	select {
	case result := <-opened:
		_ = unlockWriter()
		t.Fatalf("startup cleanup bypassed active-writer lock: %+v", result)
	case <-time.After(75 * time.Millisecond):
	}
	if _, err := os.Stat(activeTemp); err != nil {
		_ = unlockWriter()
		t.Fatalf("active encrypted temporary was deleted: %v", err)
	}
	if err := os.Remove(activeTemp); err != nil {
		_ = unlockWriter()
		t.Fatal(err)
	}
	if err := unlockWriter(); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-opened:
		if result.err != nil || result.store == nil {
			t.Fatalf("startup cleanup result = %+v", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("startup cleanup did not resume after writer completed")
	}

	startupStale := filepath.Join(projectRoot, ".encrypted-startup-stale")
	if err := os.WriteFile(startupStale, []byte("ciphertext"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewEncryptedStore(StoreOptions{RootDir: root, Wrapper: wrapper}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(startupStale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("startup retained stale encrypted temporary: %v", err)
	}

	staleTemp := filepath.Join(projectRoot, ".encrypted-stale")
	if err := os.MkdirAll(projectRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staleTemp, []byte("ciphertext"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := first.DeleteProject(context.Background(), "project-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(staleTemp); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("project deletion retained stale encrypted temporary: %v", err)
	}
}

func TestDurableNamespaceHelpersSyncAfterMutation(t *testing.T) {
	store := newEncryptedArtifactTestStore(t)
	directory := filepath.Join(store.root, "durability")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(directory, "source")
	target := filepath.Join(directory, "target")
	renamed := filepath.Join(directory, "renamed")
	if err := os.WriteFile(source, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	phase := "link"
	syncCalls := 0
	store.syncDirectoryFn = func(string) error {
		syncCalls++
		switch phase {
		case "link":
			if _, err := os.Stat(target); err != nil {
				return fmt.Errorf("link was not visible before directory sync: %w", err)
			}
		case "rename":
			if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("rename source still visible before directory sync: %v", err)
			}
			if _, err := os.Stat(renamed); err != nil {
				return fmt.Errorf("rename target was not visible before directory sync: %w", err)
			}
		case "remove":
			if _, err := os.Stat(renamed); !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("removed path still visible before directory sync: %v", err)
			}
		}
		return nil
	}
	if err := store.linkDurably(source, target); err != nil {
		t.Fatal(err)
	}
	phase = "rename"
	if err := store.renameDurably(target, renamed); err != nil {
		t.Fatal(err)
	}
	phase = "remove"
	if err := store.removeDurably(renamed); err != nil {
		t.Fatal(err)
	}
	if syncCalls < 3 {
		t.Fatalf("directory sync calls = %d, want at least one per namespace mutation", syncCalls)
	}

	uncertainTarget := filepath.Join(directory, "uncertain-target")
	syncFailure := errors.New("simulated directory sync failure")
	store.syncDirectoryFn = func(string) error { return syncFailure }
	if err := store.linkDurably(source, uncertainTarget); !errors.Is(err, syncFailure) {
		t.Fatalf("durability failure = %v, want simulated sync failure", err)
	}
	if _, err := os.Stat(uncertainTarget); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed durable link left a published target: %v", err)
	}
}

func TestInitializeWritableStoreRootSyncsNewDirectoryChain(t *testing.T) {
	anchor := t.TempDir()
	root := filepath.Join(anchor, "new-parent", "artifact-root")
	var synced []string
	if err := initializeWritableStoreRoot(root, func(path string) error {
		synced = append(synced, filepath.Clean(path))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var want []string
	for current := filepath.Clean(root); ; current = filepath.Dir(current) {
		want = append(want, current)
		if parent := filepath.Dir(current); parent == current {
			break
		}
	}
	if strings.Join(synced, "\n") != strings.Join(want, "\n") {
		t.Fatalf("synced root chain = %#v, want %#v", synced, want)
	}
	info, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("artifact root mode = %o, want 700", info.Mode().Perm())
	}
}

func TestRestoreExpiredSessionPreservesCleanupLocatorOnPartialFailure(t *testing.T) {
	tests := []struct {
		name      string
		install   func(store *EncryptedStore, original func(string) error) error
		wantCause string
	}{
		{
			name: "capacity directory sync",
			install: func(store *EncryptedStore, original func(string) error) error {
				capacityDir := filepath.Join(store.root, ".sessions", ".capacity")
				store.syncDirectoryFn = func(path string) error {
					if filepath.Clean(path) == filepath.Clean(capacityDir) {
						return errors.New("capacity sync failed")
					}
					return original(path)
				}
				return nil
			},
			wantCause: "capacity sync failed",
		},
		{
			name: "claim directory sync after unlink",
			install: func(store *EncryptedStore, original func(string) error) error {
				sessionDir := filepath.Join(store.root, ".sessions")
				sessionSyncs := 0
				store.syncDirectoryFn = func(path string) error {
					if filepath.Clean(path) == filepath.Clean(sessionDir) {
						sessionSyncs++
						if sessionSyncs == 2 {
							return errors.New("claim sync failed")
						}
					}
					return original(path)
				}
				return nil
			},
			wantCause: "claim sync failed",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newEncryptedArtifactTestStore(t)
			ctx := context.Background()
			const sessionID = "expired-partial-cleanup"
			related := OpenRequest{
				BackendKey: "bruno/example/raw/aa/source.abx",
				ProjectID:  "project-a",
				Kind:       "raw",
				SHA256:     strings.Repeat("a", 64),
				SizeBytes:  10,
			}
			if err := store.SaveSessionBytes(ctx, SessionDescriptor{
				ID:             sessionID,
				ProjectID:      "project-a",
				Actor:          "actor-a",
				ExpiresAt:      time.Now().UTC().Add(-time.Minute),
				RelatedObjects: []OpenRequest{related},
			}, []byte(`{"secret":"retained"}`), 1024); err != nil {
				t.Fatal(err)
			}
			payload, claim, err := store.ClaimSession(ctx, sessionID, "project-a", "actor-a", 1024)
			if err != nil {
				t.Fatal(err)
			}
			clear(payload)
			originalSync := store.syncDirectoryFn
			if err := test.install(store, originalSync); err != nil {
				t.Fatal(err)
			}
			retained, restoreErr := store.RestoreSessionClaim(
				ctx, sessionID, "project-a", "actor-a", claim.ClaimToken, time.Now().UTC(),
			)
			if retained || restoreErr == nil || !strings.Contains(restoreErr.Error(), test.wantCause) {
				t.Fatalf("expired restoration = retained %v, error %v", retained, restoreErr)
			}
			claimPath := filepath.Join(store.root, ".sessions", sessionID+".claim.json")
			_, locator, err := readSessionDescriptorFile(claimPath)
			if err != nil {
				t.Fatalf("expired cleanup locator was lost: %v", err)
			}
			if len(locator.RelatedObjects) != 1 || locator.RelatedObjects[0].BackendKey != related.BackendKey {
				t.Fatalf("expired cleanup locator = %#v", locator)
			}
			store.syncDirectoryFn = originalSync
			stale := time.Now().UTC().Add(-sessionClaimLease - time.Second)
			if err := os.Chtimes(claimPath, stale, stale); err != nil {
				t.Fatal(err)
			}
			expired, err := store.DeleteExpiredSessions(ctx, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			if len(expired) != 1 || expired[0].ID != sessionID {
				t.Fatalf("recovered expired cleanup locator = %#v", expired)
			}
		})
	}
}

func TestRestoreSessionClaimConsumesExpiredSession(t *testing.T) {
	store := newEncryptedArtifactTestStore(t)
	ctx := context.Background()
	expiry := time.Now().UTC().Add(time.Minute)
	const expiredID = "restore-expired"
	if err := store.SaveSessionBytes(ctx, SessionDescriptor{
		ID: expiredID, ProjectID: "project-a", Actor: "actor-a", ExpiresAt: expiry,
	}, []byte(`{"secret":"value"}`), 1024); err != nil {
		t.Fatal(err)
	}
	_, expiredClaim, err := store.ClaimSession(ctx, expiredID, "project-a", "actor-a", 1024)
	if err != nil {
		t.Fatal(err)
	}
	retained, err := store.RestoreSessionClaim(
		ctx, expiredID, "project-a", "actor-a", expiredClaim.ClaimToken, expiry.Add(time.Nanosecond),
	)
	if err != nil || retained {
		t.Fatalf("expired claim restoration = retained %v, err %v", retained, err)
	}
	for _, suffix := range []string{".json", ".claim.json"} {
		if _, err := os.Stat(filepath.Join(store.root, ".sessions", expiredID+suffix)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expired session descriptor %s remains: %v", suffix, err)
		}
	}

	const retryID = "restore-retryable"
	retryExpiry := time.Now().UTC().Add(time.Hour)
	if err := store.SaveSessionBytes(ctx, SessionDescriptor{
		ID: retryID, ProjectID: "project-a", Actor: "actor-a", ExpiresAt: retryExpiry,
	}, []byte(`{"retry":true}`), 1024); err != nil {
		t.Fatal(err)
	}
	_, retryClaim, err := store.ClaimSession(ctx, retryID, "project-a", "actor-a", 1024)
	if err != nil {
		t.Fatal(err)
	}
	retained, err = store.RestoreSessionClaim(
		ctx, retryID, "project-a", "actor-a", retryClaim.ClaimToken, time.Now().UTC(),
	)
	if err != nil || !retained {
		t.Fatalf("unexpired claim restoration = retained %v, err %v", retained, err)
	}
	reader, _, err := store.OpenSession(ctx, retryID, "project-a", "actor-a")
	if err != nil {
		t.Fatal(err)
	}
	_ = reader.Close()
}

func newEncryptedArtifactTestStore(t *testing.T) *EncryptedStore {
	t.Helper()
	wrapper, err := NewLocalAESKeyWrapper(LocalKeyOptions{Key: bytes.Repeat([]byte{0x5a}, 32), KeyID: "unit-test"})
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewEncryptedStore(StoreOptions{
		RootDir:   t.TempDir(),
		Wrapper:   wrapper,
		ChunkSize: 64 << 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

type staticTokenCredential struct{}

func (staticTokenCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

type fakeArtifactMetadataReader struct {
	artifact *storage.BrunoSourceArtifact
}

func (f *fakeArtifactMetadataReader) GetBrunoSourceArtifact(id string, projectID string) (*storage.BrunoSourceArtifact, error) {
	if f.artifact == nil || f.artifact.ID != id || f.artifact.ProjectID != projectID {
		return nil, errors.New("not found")
	}
	copy := *f.artifact
	return &copy, nil
}
