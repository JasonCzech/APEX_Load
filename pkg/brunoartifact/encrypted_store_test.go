package brunoartifact

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
	if err := store.DeleteSession(context.Background(), "expired", "project-a"); err != nil {
		t.Fatalf("finalize expired session cleanup: %v", err)
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
