package api

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestArchiveMaterializationCoordinatorDoesNotBlockUnrelatedArchives(t *testing.T) {
	coordinator := newArchiveMaterializationCoordinator()
	held, err := coordinator.reserve(context.Background(), "archive-a")
	if err != nil {
		t.Fatal(err)
	}
	defer coordinator.failReservation(held)

	unrelatedDone := make(chan error, 1)
	go func() {
		entry, err := coordinator.reserve(context.Background(), "archive-b")
		if err == nil {
			coordinator.failReservation(entry)
		}
		unrelatedDone <- err
	}()
	select {
	case err := <-unrelatedDone:
		if err != nil {
			t.Fatalf("unrelated archive reservation failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("unrelated archive was blocked by another archive's materialization")
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if _, err := coordinator.reserve(waitCtx, "archive-a"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("same-archive wait error = %v, want deadline exceeded", err)
	}
}

func TestArchiveMaterializationCoordinatorForgetsIdleEntries(t *testing.T) {
	coordinator := newArchiveMaterializationCoordinator()
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := coordinator.reserve(canceledCtx, "pre-canceled"); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled reservation error = %v, want context canceled", err)
	}
	coordinator.mu.Lock()
	entriesAfterCancellation := len(coordinator.entries)
	coordinator.mu.Unlock()
	if entriesAfterCancellation != 0 {
		t.Fatalf("coordinator retained %d idle entries after pre-canceled reservation", entriesAfterCancellation)
	}

	entry, err := coordinator.reserve(context.Background(), "successful-read")
	if err != nil {
		t.Fatal(err)
	}
	lease := coordinator.promoteReservation("successful-read", t.TempDir(), "", entry)
	lease.Release()
	coordinator.mu.Lock()
	entriesAfterRelease := len(coordinator.entries)
	coordinator.mu.Unlock()
	if entriesAfterRelease != 0 {
		t.Fatalf("coordinator retained %d idle entries after reader release", entriesAfterRelease)
	}

	failed, err := coordinator.reserve(context.Background(), "failed-materialization")
	if err != nil {
		t.Fatal(err)
	}
	coordinator.failReservation(failed)
	coordinator.mu.Lock()
	entriesAfterFailure := len(coordinator.entries)
	coordinator.mu.Unlock()
	if entriesAfterFailure != 0 {
		t.Fatalf("coordinator retained %d idle entries after failed reservation", entriesAfterFailure)
	}

	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	scanRoot := t.TempDir()
	writeArchiveMaterializationCacheFixture(t, scanRoot, "evicted-cache", 8, now.Add(-2*time.Hour))
	t.Setenv(envArchiveMaterializationCacheMaxBytes, strconv.FormatInt(1<<20, 10))
	t.Setenv(envArchiveMaterializationCacheTTL, "1h")
	coordinator.now = func() time.Time { return now }
	coordinator.enforceRetention(scanRoot)
	coordinator.mu.Lock()
	entriesAfterEviction := len(coordinator.entries)
	coordinator.mu.Unlock()
	if entriesAfterEviction != 0 {
		t.Fatalf("coordinator retained %d idle entries after cache eviction", entriesAfterEviction)
	}
}

func TestArchiveMaterializationReplacementWaitsForActiveReader(t *testing.T) {
	coordinator := newArchiveMaterializationCoordinator()
	entry, err := coordinator.reserve(context.Background(), "shared-cache")
	if err != nil {
		t.Fatal(err)
	}
	active := coordinator.promoteReservation("shared-cache", t.TempDir(), "", entry)
	replacement, err := coordinator.reserve(context.Background(), "shared-cache")
	if err != nil {
		t.Fatal(err)
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if err := coordinator.waitForNoReaders(waitCtx, replacement); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("replacement wait error = %v, want deadline exceeded while reader is active", err)
	}
	coordinator.failReservation(replacement)
	active.Release()
}

func TestArchiveMaterializationNamespacesSameTestAcrossObjectLocations(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendS3)
	t.Setenv("APEXLOAD_ARCHIVE_BUCKET", "current-bucket")
	t.Setenv("APEXLOAD_ARCHIVE_PREFIX", "current/prefix")
	configured := currentArchiveBackend()
	records := []*storage.RunArchiveRecord{
		{TestID: "shared-test", ArchiveBackend: archiveBackendS3, ArchiveURI: "s3://bucket-a/one/shared-test", ManifestURI: "s3://bucket-a/one/shared-test/manifest.json"},
		{TestID: "shared-test", ArchiveBackend: archiveBackendS3, ArchiveURI: "s3://bucket-b/two/shared-test", ManifestURI: "s3://bucket-b/two/shared-test/manifest.json"},
	}
	roots := make([]string, len(records))
	for index, record := range records {
		backend, err := archiveMaterializationRuntime(configured, record)
		if err != nil {
			t.Fatal(err)
		}
		root, err := archiveMaterializationLocalRoot(backend, record)
		if err != nil {
			t.Fatal(err)
		}
		roots[index] = root
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		manifestData, err := json.Marshal(RunArchiveManifest{SchemaVersion: archiveSchemaVersion, TestID: record.TestID, TestName: record.ArchiveURI})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, runArchiveManifestFileName), manifestData, 0o600); err != nil {
			t.Fatal(err)
		}
		record.ManifestSHA256 = sha256BytesHex(manifestData)
	}
	if roots[0] == roots[1] {
		t.Fatalf("distinct object namespaces collided at %q", roots[0])
	}
	firstLease, err := acquireArchiveMaterialized(context.Background(), records[0])
	if err != nil {
		t.Fatal(err)
	}
	defer firstLease.Release()
	secondCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	secondLease, err := acquireArchiveMaterialized(secondCtx, records[1])
	if err != nil {
		t.Fatalf("second object namespace blocked by active first reader: %v", err)
	}
	defer secondLease.Release()
	if records[0].ArchivePath == records[1].ArchivePath {
		t.Fatalf("materialized records share archive path %q", records[0].ArchivePath)
	}
	for index, record := range records {
		manifest, err := loadRunArchiveManifest(record.ManifestPath)
		if err != nil {
			t.Fatal(err)
		}
		if manifest.TestName != record.ArchiveURI {
			t.Fatalf("namespace %d loaded manifest %q, want %q", index, manifest.TestName, record.ArchiveURI)
		}
	}
}

func TestRunArchiveAndManagedTestReadsReturnIndependentArchiveCopies(t *testing.T) {
	internal := &storage.RunArchiveRecord{TestID: "copy-run", ArchivePath: "/internal/archive", ManifestPath: "/internal/archive/manifest.json"}
	runner := &TestRunner{
		archives: map[string]*storage.RunArchiveRecord{"copy-run": internal},
		tests: map[string]*ManagedTest{
			"copy-run": {ID: "copy-run", Name: "Copy Run", Archive: internal},
		},
	}
	first, ok := runner.GetRunArchive("copy-run")
	if !ok || first == nil {
		t.Fatal("first archive read was unavailable")
	}
	second, ok := runner.GetRunArchive("copy-run")
	if !ok || second == nil {
		t.Fatal("second archive read was unavailable")
	}
	test, ok := runner.GetTest("copy-run")
	if !ok || test == nil || test.Archive == nil {
		t.Fatal("managed test archive read was unavailable")
	}
	if first == internal || second == internal || test.Archive == internal || first == second {
		t.Fatal("archive read aliases shared runner state")
	}
	first.ArchivePath = "/consumer/one"
	second.ManifestPath = "/consumer/two/manifest.json"
	test.Archive.ArchivePath = "/consumer/test"
	if internal.ArchivePath != "/internal/archive" || internal.ManifestPath != "/internal/archive/manifest.json" {
		t.Fatalf("consumer mutation reached shared archive state: %+v", internal)
	}

	var wg sync.WaitGroup
	for index := 0; index < 64; index++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			record, found := runner.GetRunArchive("copy-run")
			if found && record != nil {
				record.ArchivePath = "/concurrent/consumer"
				_ = record.ManifestPath
			}
		}()
		go func() {
			defer wg.Done()
			managed, found := runner.GetTest("copy-run")
			if found && managed != nil && managed.Archive != nil {
				managed.Archive.ManifestPath = "/concurrent/test/manifest.json"
				_ = managed.Archive.ArchivePath
			}
		}()
	}
	wg.Wait()
	if internal.ArchivePath != "/internal/archive" || internal.ManifestPath != "/internal/archive/manifest.json" {
		t.Fatalf("concurrent consumer mutation reached shared archive state: %+v", internal)
	}
}

func TestConcurrentArchiveAcquisitionsKeepSharedRecordPathsStable(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendS3)
	t.Setenv("APEXLOAD_ARCHIVE_BUCKET", "concurrent-bucket")
	t.Setenv("APEXLOAD_ARCHIVE_PREFIX", "concurrent/prefix")
	record := &storage.RunArchiveRecord{
		TestID:         "concurrent-run",
		ArchiveBackend: archiveBackendS3,
		ArchiveURI:     "s3://concurrent-bucket/concurrent/prefix/concurrent-run",
		ManifestURI:    "s3://concurrent-bucket/concurrent/prefix/concurrent-run/manifest.json",
	}
	backend, err := archiveMaterializationRuntime(currentArchiveBackend(), record)
	if err != nil {
		t.Fatal(err)
	}
	root, err := archiveMaterializationLocalRoot(backend, record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	manifestData, err := json.Marshal(RunArchiveManifest{SchemaVersion: archiveSchemaVersion, TestID: record.TestID})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, runArchiveManifestFileName), manifestData, 0o600); err != nil {
		t.Fatal(err)
	}
	record.ManifestSHA256 = sha256BytesHex(manifestData)

	var wg sync.WaitGroup
	errorsCh := make(chan error, 64)
	for index := 0; index < 64; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lease, err := acquireArchiveMaterialized(context.Background(), record)
			if err != nil {
				errorsCh <- err
				return
			}
			if record.ArchivePath != root || record.ManifestPath != filepath.Join(root, runArchiveManifestFileName) {
				errorsCh <- errors.New("materialized record paths changed unexpectedly")
			}
			lease.Release()
		}()
	}
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestAcquireArchiveMaterializedHonorsCanceledContext(t *testing.T) {
	archiveRoot := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", archiveRoot)
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendS3)
	t.Setenv("APEXLOAD_ARCHIVE_BUCKET", "archive-bucket")
	record := &storage.RunArchiveRecord{
		TestID:         "run-canceled",
		ArchiveBackend: archiveBackendS3,
		ArchiveURI:     "s3://archive-bucket/apexload/archives/run-canceled",
		ManifestURI:    "s3://archive-bucket/apexload/archives/run-canceled/manifest.json",
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	lease, err := acquireArchiveMaterialized(ctx, record)
	if lease != nil {
		lease.Release()
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("materialization error = %v, want context canceled", err)
	}
	backend, runtimeErr := archiveMaterializationRuntime(currentArchiveBackend(), record)
	if runtimeErr != nil {
		t.Fatal(runtimeErr)
	}
	localRoot, rootErr := archiveMaterializationLocalRoot(backend, record)
	if rootErr != nil {
		t.Fatal(rootErr)
	}
	if _, statErr := os.Stat(localRoot); !os.IsNotExist(statErr) {
		t.Fatalf("canceled materialization created a cache directory: %v", statErr)
	}
}

func TestArchiveMaterializationCacheEnforcesAggregateByteCapAndPreservesProducerDirectory(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	scanRoot := t.TempDir()
	oldRoot := writeArchiveMaterializationCacheFixture(t, filepath.Join(scanRoot, archiveBackendS3), "old-cache", 8, now.Add(-2*time.Hour))
	newRoot := writeArchiveMaterializationCacheFixture(t, filepath.Join(scanRoot, archiveBackendGCS), "new-cache", 8, now.Add(-time.Hour))
	producerRoot := filepath.Join(scanRoot, archiveBackendS3, "producer-run")
	if err := os.MkdirAll(producerRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(producerRoot, runArchiveManifestFileName), []byte(`{"test_id":"producer-run"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	orphanRoot := filepath.Join(scanRoot, archiveBackendS3, ".abandoned.materializing-123")
	if err := os.MkdirAll(orphanRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	old := now.Add(-configuredArchiveMaterializationTimeout() - 10*time.Minute)
	if err := os.Chtimes(orphanRoot, old, old); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envArchiveMaterializationCacheMaxBytes, "10")
	t.Setenv(envArchiveMaterializationCacheTTL, "8760h")
	coordinator := newArchiveMaterializationCoordinator()
	coordinator.now = func() time.Time { return now }
	coordinator.enforceRetention(scanRoot)

	if _, err := os.Stat(oldRoot); !os.IsNotExist(err) {
		t.Fatalf("oldest cache was not evicted under aggregate byte cap: %v", err)
	}
	if _, err := os.Stat(newRoot); err != nil {
		t.Fatalf("newest cache was unexpectedly evicted: %v", err)
	}
	if _, err := os.Stat(producerRoot); err != nil {
		t.Fatalf("producer archive directory without a cache marker was evicted: %v", err)
	}
	if _, err := os.Stat(orphanRoot); !os.IsNotExist(err) {
		t.Fatalf("stale incomplete materialization directory was not cleaned up: %v", err)
	}
}

func TestArchiveMaterializationCacheScanBoundsCandidatesAcrossReadBatches(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	scanRoot := t.TempDir()
	entryCount := archiveMaterializationScanReadBatch + 44
	for index := 0; index < entryCount; index++ {
		writeArchiveMaterializationCacheFixture(t, scanRoot, "cache-"+strconv.Itoa(index), 1, now.Add(-time.Duration(index)*time.Minute))
	}

	candidates, totalBytes := scanArchiveMaterializationCacheLimit(scanRoot, now, 24*time.Hour, 10)
	if totalBytes != int64(entryCount) {
		t.Fatalf("scanned bytes = %d, want %d across every marker", totalBytes, entryCount)
	}
	if len(candidates) != 10 {
		t.Fatalf("retained candidates = %d, want hard cap 10", len(candidates))
	}
	oldest := make(map[string]struct{}, 10)
	for index := entryCount - 10; index < entryCount; index++ {
		oldest["cache-"+strconv.Itoa(index)] = struct{}{}
	}
	for _, candidate := range candidates {
		if _, ok := oldest[filepath.Base(candidate.root)]; !ok {
			t.Fatalf("bounded scan retained non-oldest candidate %q", candidate.root)
		}
	}
}

func TestArchiveMaterializationCacheProtectsActiveAndPendingLeases(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	scanRoot := t.TempDir()
	activeRoot := writeArchiveMaterializationCacheFixture(t, scanRoot, "active-cache", 8, now.Add(-2*time.Hour))
	pendingRoot := writeArchiveMaterializationCacheFixture(t, scanRoot, "pending-cache", 8, now.Add(-2*time.Hour))
	unprotectedRoot := writeArchiveMaterializationCacheFixture(t, scanRoot, "unprotected-cache", 8, now.Add(-2*time.Hour))
	t.Setenv(envArchiveMaterializationCacheMaxBytes, strconv.FormatInt(1<<20, 10))
	t.Setenv(envArchiveMaterializationCacheTTL, "1h")

	coordinator := newArchiveMaterializationCoordinator()
	coordinator.now = func() time.Time { return now }
	activeKey, err := canonicalArchiveMaterializationKey(activeRoot)
	if err != nil {
		t.Fatal(err)
	}
	activeEntry, err := coordinator.reserve(context.Background(), activeKey)
	if err != nil {
		t.Fatal(err)
	}
	activeLease := coordinator.promoteReservation(activeKey, scanRoot, activeRoot, activeEntry)
	pendingKey, err := canonicalArchiveMaterializationKey(pendingRoot)
	if err != nil {
		t.Fatal(err)
	}
	pendingEntry, err := coordinator.reserve(context.Background(), pendingKey)
	if err != nil {
		t.Fatal(err)
	}

	coordinator.enforceRetention(scanRoot)
	if _, err := os.Stat(activeRoot); err != nil {
		t.Fatalf("active reader cache was evicted: %v", err)
	}
	if _, err := os.Stat(pendingRoot); err != nil {
		t.Fatalf("pending materialization cache was evicted: %v", err)
	}
	if _, err := os.Stat(unprotectedRoot); !os.IsNotExist(err) {
		t.Fatalf("expired unprotected cache was not evicted: %v", err)
	}

	coordinator.failReservation(pendingEntry)
	activeLease.Release()
	if _, err := os.Stat(activeRoot); err != nil {
		t.Fatalf("recently released cache was unexpectedly evicted: %v", err)
	}
	waitForCondition(t, time.Second, func() bool { _, err := os.Stat(pendingRoot); return os.IsNotExist(err) }, "pending cache eviction after lease release")
}

func TestArchiveMaterializationRetentionSweepsAreCoalesced(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	scanRoot := t.TempDir()
	firstRoot := writeArchiveMaterializationCacheFixture(t, scanRoot, "first-expired", 8, now.Add(-2*time.Hour))
	t.Setenv(envArchiveMaterializationCacheMaxBytes, strconv.FormatInt(1<<20, 10))
	t.Setenv(envArchiveMaterializationCacheTTL, "1h")
	coordinator := newArchiveMaterializationCoordinator()
	coordinator.retentionSweepInterval = time.Hour
	coordinator.now = func() time.Time { return now }
	coordinator.enforceRetentionIfDue(scanRoot)
	waitForCondition(t, time.Second, func() bool { _, err := os.Stat(firstRoot); return os.IsNotExist(err) }, "initial async cache retention")

	secondRoot := writeArchiveMaterializationCacheFixture(t, scanRoot, "second-expired", 8, now.Add(-2*time.Hour))
	coordinator.enforceRetentionIfDue(scanRoot)
	if _, err := os.Stat(secondRoot); err != nil {
		t.Fatalf("coalesced retention call unexpectedly ran another scan: %v", err)
	}
	now = now.Add(time.Hour)
	coordinator.enforceRetentionIfDue(scanRoot)
	waitForCondition(t, time.Second, func() bool { _, err := os.Stat(secondRoot); return os.IsNotExist(err) }, "subsequent async cache retention")
}

func writeArchiveMaterializationCacheFixture(t *testing.T, parent, testID string, sizeBytes int64, accessedAt time.Time) string {
	t.Helper()
	root := filepath.Join(parent, testID)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, runArchiveManifestFileName), []byte(`{"test_id":"`+testID+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeArchiveMaterializationCacheMarker(root, sizeBytes, accessedAt); err != nil {
		t.Fatal(err)
	}
	return root
}
