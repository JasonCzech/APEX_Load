package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestDurableExecutionGenerationDoesNotGrantRuntimeOwnership(t *testing.T) {
	now := time.Now().UTC()
	row := &storage.TestRecord{ID: "run", StartTime: now, ExecutionGeneration: 7, InitialProvenance: map[string]any{"run_incarnation": "same"}}
	passive := managedTestFromRecord(row)
	if passive.durableExecutionGeneration != 7 || passive.executionGeneration != 0 || passive.executionFence != "" || passive.executionCtx != nil {
		t.Fatalf("hydration confused evidence identity with execution ownership: %+v", passive)
	}
	if buildManagedTestRecord(passive).ExecutionGeneration != 7 {
		t.Fatal("read projection lost durable generation")
	}
	for _, generation := range []uint64{0, 6, 7, 8} {
		next := *row
		next.ExecutionGeneration = generation
		if sameStoredTestIdentity(passive, &next) != (generation == 7) || sameArchiveRunIncarnation(passive, &next) != (generation == 7) {
			t.Fatalf("generation %d shares identity with generation 7", generation)
		}
	}
	// A local ownership claim must never relabel previously loaded evidence.
	passive.executionGeneration = 99
	if !sameStoredTestIdentity(passive, row) {
		t.Fatal("transient execution claim changed durable identity")
	}
	next := managedTestFromRecord(row)
	next.durableExecutionGeneration = 8
	applyStoredManagedTestSnapshot(passive, next)
	if passive.durableExecutionGeneration != 8 || passive.executionGeneration != 99 {
		t.Fatal("snapshot update changed runtime ownership")
	}
}

func TestFollowerReplacesEvidenceAfterExecutionGenerationAdvance(t *testing.T) {
	for _, mode := range []string{"single", "reload", "metrics", "messaging"} {
		t.Run(mode, func(t *testing.T) {
			store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "generation.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			now := time.Now().UTC()
			row := &storage.TestRecord{ID: "same-run", Name: "run", ProjectID: storage.DefaultProjectID, Status: "COMPLETE", StartTime: now.Add(-time.Minute), EndTime: &now, InitialProvenance: map[string]any{"run_incarnation": "same"}}
			if err := store.SaveTest(row); err != nil {
				t.Fatal(err)
			}
			first := stampOrphanRunExecutionGeneration(t, store, row.ID)
			if err := store.SaveHistory(row.ID, now, &LiveMetrics{Timestamp: now, TotalTx: 7}); err != nil {
				t.Fatal(err)
			}
			runner := NewTestRunner(1, nil, store, WithExecutionLeadership(newExecutionLeadership(store, "reader")))
			prior := runner.tests[row.ID]
			prior.LiveMetrics = &LiveMetrics{Timestamp: now, TotalTx: 999}
			prior.History = []*LiveMetrics{prior.LiveMetrics}
			prior.Archive = &storage.RunArchiveRecord{TestID: row.ID, ArchivePath: "old-term"}
			runner.attachStoredTerminalHistory(cloneManagedTestForReadLocked(prior))
			advanceOrphanFixtureGeneration(t, store, row.ID)
			runner.haTestRefresh.markFresh(time.Now())
			if mode == "reload" {
				if err := runner.reloadStoredTests(); err != nil {
					t.Fatal(err)
				}
			}
			var got *ManagedTest
			var ok bool
			if mode == "metrics" {
				got, ok = runner.GetTestMetricsWindow(row.ID, nil, 10)
			} else {
				if mode == "messaging" {
					if progress, _, exists := runner.latestMessagingProgress(row.ID); !exists || progress != nil {
						t.Fatalf("stale messaging evidence: %+v", progress)
					}
				}
				got, ok = runner.GetTest(row.ID)
			}
			if !ok || got.durableExecutionGeneration <= first || got.Archive != nil || got.LiveMetrics == nil || got.LiveMetrics.TotalTx != 7 {
				t.Fatalf("follower reused old-term state: %+v", got)
			}
			if prior.durableExecutionGeneration != first {
				t.Fatal("old evidence was relabeled with new term")
			}
		})
	}
}

func TestArchiveHistoryAndAttachmentSeparateExecutionGenerations(t *testing.T) {
	now := time.Now().UTC()
	old := &ManagedTest{ID: "history-term", Status: "COMPLETE", ProjectID: storage.DefaultProjectID, CreatedAt: now.Add(-time.Minute), EndedAt: &now, durableExecutionGeneration: 1, initialArchiveProvenance: &RunArchiveProvenance{RunIncarnation: "same"}}
	next := *old
	next.durableExecutionGeneration = 2
	if storedTestHistoryCacheKey(old) == storedTestHistoryCacheKey(&next) || sameStoredTestHistoryIdentity(old, &next) {
		t.Fatal("history cache conflated execution terms")
	}
	deleteGroupMetricsHistoryCache(old.ID)
	t.Cleanup(func() { deleteGroupMetricsHistoryCache(old.ID) })
	oldPoint, newPoint := liveMetricsForGroupFilterTest(), liveMetricsForGroupFilterTest()
	newPoint.Timestamp = oldPoint.Timestamp
	newPoint.GroupMetrics["Checkout"].TPS = 91
	filter := groupFilterQuery{Names: []string{"Checkout"}, Explicit: true}
	filterLiveMetricsHistoryForGroupsCached(old, []*LiveMetrics{oldPoint}, filter)
	if got := filterLiveMetricsHistoryForGroupsCached(&next, []*LiveMetrics{newPoint}, filter); len(got) != 1 || got[0].TPS != 91 {
		t.Fatalf("group cache reused old execution term: %+v", got)
	}
	runner := NewTestRunner(1, nil, nil)
	runner.tests[next.ID] = &next
	record := &storage.RunArchiveRecord{TestID: next.ID}
	identity := &storage.ArchiveFinalizationRunIdentity{RunIncarnation: "same", ExecutionGeneration: 1, ProjectID: next.ProjectID, StartTime: next.CreatedAt, EndTime: now, Status: next.Status}
	runner.attachFinalizedRunArchive(next.ID, record, identity)
	if next.Archive != nil {
		t.Fatal("late prior-term completion attached to adopted run")
	}
	identity.ExecutionGeneration = 2
	runner.attachFinalizedRunArchive(next.ID, record, identity)
	if next.Archive == nil {
		t.Fatal("matching-term completion did not attach")
	}
}

// The durable write can finish before an older caller receives its result. A
// newer adoption installed on the same handle must not be relabeled afterward.
type delayedGenerationReturnStore struct {
	SQLiteStore  *storage.SQLiteStore
	beforeReturn func()
}

func (s *delayedGenerationReturnStore) AdvanceTestExecutionGenerationIfLeaseTermHeld(id, lease, owner string, generation uint64) (bool, error) {
	advanced, err := s.SQLiteStore.AdvanceTestExecutionGenerationIfLeaseTermHeld(id, lease, owner, generation)
	if advanced && err == nil && s.beforeReturn != nil {
		s.beforeReturn()
	}
	return advanced, err
}

func TestLateAdoptionGenerationResultCannotRegressEvidenceIdentity(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "adoption.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	const owner = "old-adoption"
	held, generation, err := store.TryAcquireSchedulerLeaseWithGeneration(executionLeaseName, owner, time.Now(), time.Minute)
	if err != nil || !held {
		t.Fatalf("acquire: %t %v", held, err)
	}
	row := &storage.TestRecord{ID: "adopting", Name: "adopting", ProjectID: storage.DefaultProjectID, StartTime: time.Now(), Status: "RUNNING"}
	if err := store.SaveTest(row); err != nil {
		t.Fatal(err)
	}
	runner := NewTestRunner(1, nil, nil)
	run := managedTestFromRecord(row)
	runner.tests[run.ID] = run
	wrapper := &delayedGenerationReturnStore{SQLiteStore: store, beforeReturn: func() {
		runner.mu.Lock()
		run.durableExecutionGeneration = generation + 1
		runner.mu.Unlock()
	}}
	runner.testStore = sqliteOverlay(wrapper)
	if err := runner.advanceAdoptedRunExecutionGeneration(run, generation, owner); err != nil {
		t.Fatal(err)
	}
	if run.durableExecutionGeneration != generation+1 {
		t.Fatal("late adoption return regressed evidence generation")
	}
	wrapper.beforeReturn = nil
	run.durableExecutionGeneration = 0
	if err := runner.advanceAdoptedRunExecutionGeneration(run, generation, owner); err != nil {
		t.Fatal(err)
	}
	if run.durableExecutionGeneration != generation {
		t.Fatal("successful adoption did not publish evidence generation")
	}
}

func TestCompletedProducerRetryVerifiesExecutionGeneration(t *testing.T) {
	for _, sameTerm := range []bool{false, true} {
		t.Run(map[bool]string{false: "old term", true: "current term"}[sameTerm], func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
			t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
			store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			run, record := createArchiveFinalizationTest(t, store, "completed-term", root)
			generation := stampOrphanRunExecutionGeneration(t, store, run.ID)
			run.durableExecutionGeneration = generation
			manifest, err := loadRunArchiveManifestFromRecord(record)
			if err != nil {
				t.Fatal(err)
			}
			if sameTerm {
				manifest.Provenance.ExecutionGeneration = generation
			}
			if err := writeIndentedJSONFile(record.ManifestPath, manifest); err != nil {
				t.Fatal(err)
			}
			bytes, err := os.ReadFile(record.ManifestPath)
			if err != nil {
				t.Fatal(err)
			}
			record.ManifestSHA256 = sha256BytesHex(bytes)
			if err := store.SaveRunArchive(record); err != nil {
				t.Fatal(err)
			}
			runner := NewTestRunner(1, nil, store)
			// Remove startup's unverified pointer; only the producer retry may attach.
			runner.tests[run.ID].Archive = nil
			delete(runner.archives, run.ID)
			err = runner.finalizeRunArchiveDurably(context.Background(), run, record, true)
			if (err == nil) != sameTerm {
				t.Fatalf("completed retry accepted=%t want=%t: %v", err == nil, sameTerm, err)
			}
			if (runner.tests[run.ID].Archive != nil) != sameTerm {
				t.Fatal("completed retry attached wrong-term archive")
			}
		})
	}
}

func TestCompletedProducerRetrySupportsPrivateObjectReaderCache(t *testing.T) {
	sourceRoot, privateRoot := t.TempDir(), t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", sourceRoot)
	t.Setenv("APEXLOAD_ARCHIVE_STAGING_DIR", t.TempDir())
	t.Setenv(envArchiveReaderCacheDir, privateRoot)
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	run, record := createArchiveFinalizationTest(t, store, "private-completed", sourceRoot)
	data, err := os.ReadFile(record.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var expectedPath string
	var manifestReads atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != expectedPath {
			http.NotFound(w, r)
			return
		}
		manifestReads.Add(1)
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		_, _ = w.Write(data)
	}))
	defer server.Close()
	// Uppercase forces S3 path addressing for the loopback fixture, avoiding
	// virtual-host DNS resolution of a bucket-prefixed 127.0.0.1 hostname.
	const bucket = "Completed-Test"
	for key, value := range map[string]string{
		"APEXLOAD_ARCHIVE_BACKEND": archiveBackendS3, "APEXLOAD_ARCHIVE_BUCKET": bucket,
		"APEXLOAD_ARCHIVE_ENDPOINT": server.URL, "APEXLOAD_ARCHIVE_REGION": "us-east-1",
		"AWS_ACCESS_KEY_ID": "test", "AWS_SECRET_ACCESS_KEY": "test", "AWS_EC2_METADATA_DISABLED": "true",
		"AWS_REQUEST_CHECKSUM_CALCULATION": "when_required", "AWS_RESPONSE_CHECKSUM_VALIDATION": "when_required",
	} {
		t.Setenv(key, value)
	}
	backend := currentArchiveBackend()
	expectedPath = "/" + bucket + "/" + backend.objectKey(run.ID, runArchiveManifestFileName)
	record.ArchiveBackend, record.ArchiveURI, record.ManifestURI = archiveBackendS3, backend.runURI(run.ID), backend.manifestURI(run.ID)
	if err := store.SaveRunArchive(record); err != nil {
		t.Fatal(err)
	}
	runner := NewTestRunner(1, nil, store)
	if err := runner.finalizeRunArchiveDurably(context.Background(), run, record, false); err != nil {
		t.Fatal(err)
	}
	if manifestReads.Load() == 0 {
		t.Fatal("completed retry did not read the published object manifest")
	}
	canonicalPrivate, err := archiveReaderCacheCanonicalRoot(privateRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(record.ArchivePath, canonicalPrivate+string(filepath.Separator)) {
		t.Fatalf("retry bypassed private reader root: %s", record.ArchivePath)
	}
	if bytes, err := os.ReadFile(filepath.Join(sourceRoot, run.ID, runArchiveManifestFileName)); err != nil || string(bytes) != string(data) {
		t.Fatalf("retry changed publication source: %v", err)
	}
}
