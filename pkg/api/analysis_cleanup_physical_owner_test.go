package api

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/storage"
	"gocloud.dev/blob"
	"gocloud.dev/blob/memblob"
)

type analysisCleanupFaultRoot struct {
	*os.Root
	closeCalls                  atomic.Int32
	closeErr                    error
	closeEntered, closePermit   chan struct{}
	removeEntered, removePermit chan struct{}
	removeOnce, closeOnce       sync.Once
}

func (r *analysisCleanupFaultRoot) Close() error {
	r.closeCalls.Add(1)
	if r.closeEntered != nil {
		r.closeOnce.Do(func() { close(r.closeEntered) })
		<-r.closePermit
	}
	if r.closeErr != nil {
		return r.closeErr
	}
	return r.Root.Close()
}
func (r *analysisCleanupFaultRoot) Remove(key string) error {
	if r.removeEntered != nil {
		r.removeOnce.Do(func() { close(r.removeEntered) })
		<-r.removePermit
	}
	return r.Root.Remove(key)
}
func (r *analysisCleanupFaultRoot) RemoveAll(key string) error {
	if r.removeEntered != nil {
		r.removeOnce.Do(func() { close(r.removeEntered) })
		<-r.removePermit
	}
	return r.Root.RemoveAll(key)
}

type analysisCleanupFaultBucket struct {
	*blob.Bucket
	closeCalls atomic.Int32
	closeErr   error
}

func (b *analysisCleanupFaultBucket) Close() error {
	b.closeCalls.Add(1)
	if b.closeErr != nil {
		return b.closeErr
	}
	return b.Bucket.Close()
}
func cleanupOwnerContext(t *testing.T, limit int, ops analysisCleanupPhysicalOps) (context.Context, *analysisCleanupPhysicalRegistry) {
	t.Helper()
	registry := newAnalysisCleanupPhysicalRegistry(limit)
	return context.WithValue(t.Context(), analysisCleanupPhysicalRuntimeKey{}, analysisCleanupPhysicalRuntime{registry, ops}), registry
}
func cleanupOwnerFor(t *testing.T, r *analysisCleanupPhysicalRegistry) *analysisCleanupPhysicalOwner {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.owners) != 1 {
		t.Fatalf("expected one exact retained owner, got %d", len(r.owners))
	}
	for _, o := range r.owners {
		return o
	}
	return nil
}
func cleanupOwnerCount(r *analysisCleanupPhysicalRegistry) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.owners)
}
func cleanupActualRoot(t *testing.T) (archiveBackendRuntime, *os.Root, string) {
	t.Helper()
	backend := archiveBackendRuntime{Backend: archiveBackendFilesystem, LocalRoot: t.TempDir()}
	path := filepath.Join(backend.LocalRoot, "_analysis")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	key := analysisObjectKey("proj-a", uuid.NewString(), "part.parquet")
	if err := os.MkdirAll(filepath.Join(path, filepath.Dir(key)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, key), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return backend, root, key
}
func TestAnalysisCleanupLogicalDeleteCannotRetireFailedExactRoot(t *testing.T) {
	for _, kind := range []string{"object", "namespace"} {
		t.Run(kind, func(t *testing.T) {
			backend, root, key := cleanupActualRoot(t)
			fault := &analysisCleanupFaultRoot{Root: root, closeErr: errors.New("controlled root close failure")}
			var opens atomic.Int32
			ops := defaultAnalysisCleanupPhysicalOps
			ops.openRoot = func(string) (analysisCleanupRoot, error) { opens.Add(1); return fault, nil }
			ctx, registry := cleanupOwnerContext(t, 1, ops)
			remove := func() error {
				if kind == "object" {
					return deleteAnalysisObjectPhysical(ctx, backend, key)
				}
				return deleteAnalysisNamespacePhysical(ctx, backend, filepath.Dir(key))
			}
			if err := remove(); !errors.Is(err, errAnalysisCleanupPhysical) {
				t.Fatal("logical deletion hid root close failure", err)
			}
			if _, err := os.Stat(filepath.Join(backend.LocalRoot, "_analysis", key)); !os.IsNotExist(err) {
				t.Fatal("fixture was not actually deleted", err)
			}
			owner := cleanupOwnerFor(t, registry)
			owner.mu.Lock()
			exact := owner.descriptors[0].closer
			joined := owner.descriptors[0].joined
			owner.mu.Unlock()
			if exact != fault || joined {
				t.Fatal("failed exact root dropped or falsely joined")
			}
			// A real descriptor remains usable; only the fixture owner closes it during
			// teardown. Production never retries/forgives an ambiguous Close result.
			probe, err := root.Open(".")
			if err != nil {
				t.Fatal("close-fault fixture did not retain actual root", err)
			}
			if err = probe.Close(); err != nil {
				t.Fatal(err)
			}
			if err = remove(); !errors.Is(err, errAnalysisCleanupPhysical) || opens.Load() != 1 || fault.closeCalls.Load() != 1 {
				t.Fatal("retry created a second physical deletion or Close", err)
			}
		})
	}
}
func TestAnalysisCleanupLogicalCloudDeleteCannotRetireFailedExactBucket(t *testing.T) {
	bucket := memblob.OpenBucket(nil)
	t.Cleanup(func() { _ = bucket.Close() })
	backend := archiveBackendRuntime{Backend: archiveBackendGCS, Bucket: "fixture", Prefix: "approved"}
	key := analysisObjectKey("proj-a", uuid.NewString(), "part.parquet")
	objectKey := filepath.ToSlash(filepath.Join(backend.Prefix, "_analysis", key))
	if err := bucket.WriteAll(t.Context(), objectKey, []byte("fixture"), nil); err != nil {
		t.Fatal(err)
	}
	fault := &analysisCleanupFaultBucket{Bucket: bucket, closeErr: errors.New("controlled bucket close failure")}
	var opens atomic.Int32
	ops := defaultAnalysisCleanupPhysicalOps
	ops.openBucket = func(context.Context, archiveBackendRuntime) (analysisCleanupBucket, error) {
		opens.Add(1)
		return fault, nil
	}
	ctx, registry := cleanupOwnerContext(t, 1, ops)
	if err := deleteAnalysisObjectPhysical(ctx, backend, key); !errors.Is(err, errAnalysisCleanupPhysical) {
		t.Fatal("logical object absence hid bucket failure", err)
	}
	if exists, err := bucket.Exists(t.Context(), objectKey); err != nil || exists {
		t.Fatal("object was not actually deleted", exists, err)
	}
	owner := cleanupOwnerFor(t, registry)
	owner.mu.Lock()
	exact, joined := owner.descriptors[0].closer, owner.descriptors[0].joined
	owner.mu.Unlock()
	if exact != fault || joined {
		t.Fatal("lost exact failed bucket")
	}
	if err := deleteAnalysisObjectPhysical(ctx, backend, key); !errors.Is(err, errAnalysisCleanupPhysical) || opens.Load() != 1 || fault.closeCalls.Load() != 1 {
		t.Fatal("bucket retry duplicated physical owner", err)
	}
}
func TestAnalysisCleanupWaiterExpiryRetainsActualCloseAndReleasesOnlyLateNilProof(t *testing.T) {
	backend, root, key := cleanupActualRoot(t)
	fault := &analysisCleanupFaultRoot{Root: root, closeEntered: make(chan struct{}), closePermit: make(chan struct{})}
	release := sync.OnceFunc(func() { close(fault.closePermit) })
	t.Cleanup(release)
	var opens atomic.Int32
	ops := defaultAnalysisCleanupPhysicalOps
	ops.openRoot = func(string) (analysisCleanupRoot, error) { opens.Add(1); return fault, nil }
	ctx, registry := cleanupOwnerContext(t, 1, ops)
	started := time.Now()
	if err := deleteAnalysisObjectPhysical(ctx, backend, key); !errors.Is(err, errAnalysisCleanupPhysical) || time.Since(started) > 2*time.Second {
		t.Fatal("blocked actual Close held public waiter or invented proof", err)
	}
	<-fault.closeEntered
	owner := cleanupOwnerFor(t, registry)
	select {
	case <-owner.done:
		t.Fatal("waiter expiry joined actual Close")
	default:
	}
	// Capacity is bounded while unknown. This distinct key must not open.
	if err := deleteAnalysisObjectPhysical(ctx, backend, analysisObjectKey("proj-a", uuid.NewString(), "other.parquet")); !errors.Is(err, errAnalysisCleanupPhysical) || opens.Load() != 1 {
		t.Fatal("unknown physical parent released capacity", err)
	}
	again, start, err := registry.acquire(owner.identity)
	if err != nil || start || again != owner {
		t.Fatal("retry did not rejoin exact pending owner", err)
	}
	second := make(chan error, 1)
	go func() { second <- again.wait(ctx) }()
	release()
	select {
	case err := <-second:
		if err != nil {
			t.Fatal("actual nil proof did not reconcile waiter", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("actual close did not resume retained waiter")
	}
	select {
	case <-owner.done:
	case <-time.After(time.Second):
		t.Fatal("late descriptor owner did not join")
	}
	if cleanupOwnerCount(registry) != 0 || opens.Load() != 1 || fault.closeCalls.Load() != 1 {
		t.Fatal("late nil proof did not release exactly once")
	}
}
func TestAnalysisCleanupCancellationCannotInventEnteredRemoveJoin(t *testing.T) {
	backend, root, key := cleanupActualRoot(t)
	fault := &analysisCleanupFaultRoot{Root: root, removeEntered: make(chan struct{}), removePermit: make(chan struct{})}
	release := sync.OnceFunc(func() { close(fault.removePermit) })
	t.Cleanup(release)
	ops := defaultAnalysisCleanupPhysicalOps
	ops.openRoot = func(string) (analysisCleanupRoot, error) { return fault, nil }
	parent, registry := cleanupOwnerContext(t, 1, ops)
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- deleteAnalysisObjectPhysical(ctx, backend, key) }()
	<-fault.removeEntered
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || !errors.Is(err, errAnalysisCleanupPhysical) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled public waiter held by Remove")
	}
	owner := cleanupOwnerFor(t, registry)
	owner.mu.Lock()
	bodyDone := owner.bodyDone
	exact := owner.descriptors[0].closer
	owner.mu.Unlock()
	if bodyDone || exact != fault || fault.closeCalls.Load() != 0 {
		t.Fatal("cancellation discarded entered Remove ownership")
	}
	release()
	select {
	case <-owner.done:
	case <-time.After(time.Second):
		t.Fatal("actual Remove and Close did not join")
	}
	if cleanupOwnerCount(registry) != 0 || fault.closeCalls.Load() != 1 {
		t.Fatal("physical parent released incorrectly")
	}
}
func TestAnalysisCleanupMissingRootDoesNotRetainTypedNilDescriptor(t *testing.T) {
	ctx, registry := cleanupOwnerContext(t, 1, defaultAnalysisCleanupPhysicalOps)
	backend := archiveBackendRuntime{Backend: archiveBackendFilesystem, LocalRoot: t.TempDir()}
	if err := deleteAnalysisObjectPhysical(ctx, backend, analysisObjectKey("proj-a", uuid.NewString(), "missing.parquet")); err != nil {
		t.Fatal("missing root treated as an unjoined typed-nil descriptor", err)
	}
	if cleanupOwnerCount(registry) != 0 {
		t.Fatal("absent root retained a nonexistent physical owner")
	}
}

type analysisCleanupCountingStore struct {
	SQLiteStore *storage.SQLiteStore
	completed   atomic.Int32
}

func (s *analysisCleanupCountingStore) ListAnalysisCleanupJobs(now time.Time, limit int) ([]storage.AnalysisCleanupJob, error) {
	return s.SQLiteStore.ListAnalysisCleanupJobs(now.Add(time.Hour), limit)
}
func (s *analysisCleanupCountingStore) CompleteAnalysisCleanupJob(job storage.AnalysisCleanupJob) error {
	s.completed.Add(1)
	return s.SQLiteStore.CompleteAnalysisCleanupJob(job)
}
func TestAnalysisCleanupRootCloseFailurePreservesDurableJobAndFairRetry(t *testing.T) {
	t.Setenv(config.EnvArchiveDir, t.TempDir())
	_, durable := newTimeslotServer(t)
	store := &analysisCleanupCountingStore{SQLiteStore: durable.(*storage.SQLiteStore)}
	in, err := store.SQLiteStore.SaveAnalysisImport(storage.AnalysisImport{ID: uuid.NewString(), ProjectID: "proj-a", Name: "cancelled completed fixture", State: "uploading", Files: []storage.AnalysisImportFile{{ID: uuid.NewString(), RelativePath: "data.parquet", Size: 12, Uploaded: 12}}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	// This fixture has no physical writer. Seal its already-complete legacy
	// cursor before cancellation so it does not manufacture unknown-writer proof.
	in.State = "mapped"
	in, err = store.SQLiteStore.SaveAnalysisImport(*in, in.Revision)
	if err != nil {
		t.Fatal(err)
	}
	in.State = "cancelled"
	if _, err = store.SQLiteStore.SaveAnalysisImport(*in, in.Revision); err != nil {
		t.Fatal(err)
	}
	backend := currentArchiveBackend()
	rootPath := filepath.Join(backend.LocalRoot, "_analysis")
	if err = os.MkdirAll(rootPath, 0700); err != nil {
		t.Fatal(err)
	}
	raw, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	fault := &analysisCleanupFaultRoot{Root: raw, closeErr: errors.New("controlled durable cleanup close failure")}
	ops := defaultAnalysisCleanupPhysicalOps
	ops.openRoot = func(string) (analysisCleanupRoot, error) { return fault, nil }
	ctx, registry := cleanupOwnerContext(t, 1, ops)
	server := &Server{store: sqliteOverlay(store)}
	before, err := store.ListAnalysisCleanupJobs(time.Now(), 50)
	if err != nil || len(before) != 1 {
		t.Fatal("missing cancellation cleanup job", before, err)
	}
	if err = server.retryAnalysisCleanup(ctx); err != nil {
		t.Fatal("unjoined cleanup did not defer fairly", err)
	}
	after, err := store.ListAnalysisCleanupJobs(time.Now(), 50)
	if err != nil || len(after) != 1 || !after[0].NotBefore.After(time.Now()) || store.completed.Load() != 0 {
		t.Fatal("root Close fault retired durable job", after, err)
	}
	if cleanupOwnerCount(registry) != 1 || fault.closeCalls.Load() != 1 {
		t.Fatal("job retry lost physical root")
	}
	if err = server.retryAnalysisCleanup(ctx); err != nil || store.completed.Load() != 0 || fault.closeCalls.Load() != 1 {
		t.Fatal("second sweep forged proof or repeated Close", err)
	}
}

// The provider's Close panic is a stable unknown outcome, never a successful
// join and never permission to retry a physical closer or free its charge.
type analysisCleanupPanicCloser struct{ calls atomic.Int32 }

func (c *analysisCleanupPanicCloser) Close() error { c.calls.Add(1); panic("controlled close panic") }
func TestAnalysisCleanupClosePanicRetainsExactOwnerAndNoProof(t *testing.T) {
	registry := newAnalysisCleanupPhysicalRegistry(1)
	identity := analysisCleanupIdentity{backend: archiveBackendRuntime{Backend: archiveBackendFilesystem, LocalRoot: t.TempDir()}, kind: "fixture", key: "owned"}
	owner, start, err := registry.acquire(identity)
	if err != nil || !start {
		t.Fatal(err)
	}
	closer := &analysisCleanupPanicCloser{}
	go owner.run(t.Context(), func(context.Context, *analysisCleanupPhysicalOwner) error { owner.track(closer); return nil })
	if err = owner.wait(t.Context()); !errors.Is(err, errAnalysisCleanupPhysical) {
		t.Fatal("Close panic became physical proof", err)
	}
	if cleanupOwnerCount(registry) != 1 || closer.calls.Load() != 1 {
		t.Fatal("Close panic lost exact parent charge")
	}
	again, start, err := registry.acquire(identity)
	if err != nil || start || again != owner {
		t.Fatal("panic Close retried via replacement owner", err)
	}
	select {
	case <-owner.done:
		t.Fatal("panic Close reported successful join")
	default:
	}
}

var _ io.Closer = (*analysisCleanupFaultRoot)(nil)
