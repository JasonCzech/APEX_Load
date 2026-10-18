package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestOrphanRecoveryRejectsDifferentTerminalIncarnation(t *testing.T) {
	// The -same-term modes give the row and the archive one execution term:
	// a matching generation never excuses a different start, outcome or a run
	// that has not ended. The other generation modes keep the exact end rule
	// for unstamped rows and archives, and reject another term's archive.
	for _, mode := range []string{
		"start", "end", "status", "missing-end",
		"end-unstamped-row", "end-unstamped-archive", "end-other-term",
		"start-same-term", "status-same-term", "missing-end-same-term",
	} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
			t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
			store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			saveTerminalOrphanTest(t, store, "orphan", storage.DefaultProjectID)
			record := writeOrphanArchiveFixture(t, root, "orphan", "orphan", storage.DefaultProjectID)
			switch mode {
			case "end-unstamped-row":
				setOrphanManifestExecutionGeneration(t, record, 1)
			case "end-unstamped-archive":
				stampOrphanRunExecutionGeneration(t, store, "orphan")
			case "end-other-term":
				setOrphanManifestExecutionGeneration(t, record, stampOrphanRunExecutionGeneration(t, store, "orphan")+1)
			case "start-same-term", "status-same-term", "missing-end-same-term":
				setOrphanManifestExecutionGeneration(t, record, stampOrphanRunExecutionGeneration(t, store, "orphan"))
			}
			row, err := store.GetTest("orphan")
			if err != nil {
				t.Fatal(err)
			}
			ended := row.EndTime.Add(time.Second)
			switch mode {
			case "start":
				row.StartTime = row.StartTime.Add(time.Second)
			case "end", "end-unstamped-row", "end-unstamped-archive", "end-other-term":
				row.EndTime = &ended
			case "status":
				row.Status = "FAILED"
			case "missing-end", "missing-end-same-term":
				row.EndTime = nil
			case "start-same-term":
				row.StartTime, row.EndTime = row.StartTime.Add(time.Second), &ended
			case "status-same-term":
				row.Status, row.EndTime = "FAILED", &ended
			}
			if err := store.SaveTest(row); err != nil {
				t.Fatal(err)
			}
			tr := NewTestRunner(1, nil, nil)
			tr.testStore, tr.archiveStore = store, store
			before, err := os.ReadFile(record.ManifestPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := tr.recoverOrphanedLocalRunArchives(context.Background()); err == nil {
				t.Fatal("stale orphan queued")
			}
			if _, err := store.GetArchiveFinalizationJob("orphan"); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("stale orphan left publication", err)
			}
			after, err := os.ReadFile(record.ManifestPath)
			if err != nil || string(after) != string(before) {
				t.Fatal("rejected orphan changed", err)
			}
		})
	}
}

func TestOrphanRecoveryVerifiesExistingPayloads(t *testing.T) {
	for _, mode := range []string{"valid", "corrupt", "missing", "symlink", "unanchored", "over-limit"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
			t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
			record := writeOrphanArchiveFixture(t, root, "orphan", "orphan", storage.DefaultProjectID)
			data, err := os.ReadFile(record.ManifestPath)
			if err != nil {
				t.Fatal(err)
			}
			var manifest RunArchiveManifest
			if err := json.Unmarshal(data, &manifest); err != nil {
				t.Fatal(err)
			}
			payload := []byte("{\"value\":1}\n")
			manifest.TransactionsFile = "transactions.jsonl"
			manifest.Artifacts = []RunArchiveArtifact{{Name: manifest.TransactionsFile, SizeBytes: int64(len(payload)), SHA256: sha256BytesHex(payload)}}
			path := filepath.Join(record.ArchivePath, manifest.TransactionsFile)
			if err := os.WriteFile(path, payload, 0600); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "corrupt":
				if err := os.WriteFile(path, []byte("{\"value\":2}\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(t.TempDir(), "elsewhere"), path); err != nil {
					t.Fatal(err)
				}
			case "unanchored":
				manifest.Artifacts = nil
			case "over-limit":
				t.Setenv("APEXLOAD_ARCHIVE_MATERIALIZATION_MAX_BYTES", "1")
			}
			if err := writeIndentedJSONFile(record.ManifestPath, manifest); err != nil {
				t.Fatal(err)
			}
			_, _, err = orphanArchiveRecordFromDirectory(context.Background(), record.ArchivePath)
			if (err == nil) != (mode == "valid") {
				t.Fatalf("verification %s: %v", mode, err)
			}
		})
	}
}

type changedOrphanRunStore struct {
	SQLiteStore *storage.SQLiteStore
	// change mutates the durable run between inspection and the fenced insert;
	// nil moves its start.
	change func(*storage.TestRecord)
}

func (s *changedOrphanRunStore) SaveArchiveFinalizationJobIfOrphanForRun(job *storage.ArchiveFinalizationJob, requestedAt time.Time, status, lease, owner string) (bool, error) {
	row, err := s.SQLiteStore.GetTest(job.TestID)
	if err != nil {
		return false, err
	}
	if s.change != nil {
		s.change(row)
	} else {
		row.StartTime = row.StartTime.Add(time.Second)
	}
	if err := s.SQLiteStore.SaveTest(row); err != nil {
		return false, err
	}
	return s.SQLiteStore.SaveArchiveFinalizationJobIfOrphanForRun(job, requestedAt, status, lease, owner)
}
func TestOrphanRecoveryFencesRunChangedAfterInspection(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	saveTerminalOrphanTest(t, store, "orphan", storage.DefaultProjectID)
	writeOrphanArchiveFixture(t, root, "orphan", "orphan", storage.DefaultProjectID)
	tr := NewTestRunner(1, nil, nil)
	tr.testStore, tr.archiveStore = store, sqliteOverlay(&changedOrphanRunStore{SQLiteStore: store})
	if err := tr.recoverOrphanedLocalRunArchives(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetArchiveFinalizationJob("orphan"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("changed incarnation queued", err)
	}
}

// A failed-over archive is restamped to the durable end it inspected. The
// fenced insert must still refuse it when that end changes before the insert,
// and the refused source must stay byte-for-byte untouched.
func TestOrphanRecoveryFencesFailedOverRunChangedAfterRestamp(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	record, _ := saveFailedOverOrphanFixture(t, store, root, "orphan")
	before, err := os.ReadFile(record.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	tr := NewTestRunner(1, nil, nil)
	tr.testStore, tr.archiveStore = store, sqliteOverlay(&changedOrphanRunStore{SQLiteStore: store, change: func(row *storage.TestRecord) {
		ended := row.EndTime.Add(time.Second)
		row.EndTime = &ended
	}})
	if err := tr.recoverOrphanedLocalRunArchives(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetArchiveFinalizationJob("orphan"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("changed failed-over run queued", err)
	}
	if after, err := os.ReadFile(record.ManifestPath); err != nil || string(after) != string(before) {
		t.Fatal("fenced failed-over source was rewritten", err)
	}
}

// The executing term finalized the archive at its own time and the successor
// reconciled the run row later. Recovery publishes that archive at the durable
// end time and keeps the executor's time beside it.
func TestOrphanRecoveryPublishesFailedOverArchiveAtDurableEnd(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	record, executorCompletedAt := saveFailedOverOrphanFixture(t, store, root, "orphan")
	before, err := os.ReadFile(record.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	row, err := store.GetTest("orphan")
	if err != nil {
		t.Fatal(err)
	}
	runner := NewTestRunner(1, NewHub(), store)
	if err := runner.recoverOrphanedLocalRunArchives(context.Background()); err != nil {
		t.Fatal(err)
	}
	job, err := store.GetArchiveFinalizationJob("orphan")
	if err != nil {
		t.Fatal("failed-over orphan was not queued", err)
	}
	// The restamp travels with the fenced job; the source is untouched until
	// finalization restores the prepared bytes.
	if after, err := os.ReadFile(record.ManifestPath); err != nil || string(after) != string(before) {
		t.Fatal("recovery rewrote the source before finalization", err)
	}
	var prepared RunArchiveManifest
	if err := json.Unmarshal([]byte(job.PreparedManifestJSON), &prepared); err != nil {
		t.Fatal("queued job lacks the restamped manifest", err)
	}
	if !prepared.CompletedAt.Equal(*row.EndTime) || prepared.ExecutorCompletedAt == nil || !prepared.ExecutorCompletedAt.Equal(executorCompletedAt) || prepared.Status != "FAILED" {
		t.Fatalf("restamped manifest completed=%v executor=%v status=%s", prepared.CompletedAt, prepared.ExecutorCompletedAt, prepared.Status)
	}
	if !job.ArchiveRecord.CompletedAt.Equal(*row.EndTime) || job.ArchiveRecord.ManifestSHA256 != sha256BytesHex([]byte(job.PreparedManifestJSON)) {
		t.Fatalf("queued record does not anchor the restamped manifest: %+v", job.ArchiveRecord)
	}

	server := &Server{runner: runner, store: store}
	if err := server.retryArchiveFinalizationJobs(context.Background()); err != nil {
		t.Fatal(err)
	}
	published, err := store.GetRunArchive("orphan")
	if err != nil || !published.CompletedAt.Equal(*row.EndTime) {
		t.Fatalf("failed-over archive was not published at the durable end: %+v %v", published, err)
	}
	manifest, err := loadRunArchiveManifestFromRecord(published)
	if err != nil {
		t.Fatal(err)
	}
	if !manifest.CompletedAt.Equal(*row.EndTime) || manifest.ExecutorCompletedAt == nil || !manifest.ExecutorCompletedAt.Equal(executorCompletedAt) {
		t.Fatalf("published manifest completed=%v executor=%v", manifest.CompletedAt, manifest.ExecutorCompletedAt)
	}
	if err := runner.recoverOrphanedLocalRunArchives(context.Background()); err != nil {
		t.Fatal(err)
	}
	again, err := store.GetRunArchive("orphan")
	if err != nil || again.ManifestSHA256 != published.ManifestSHA256 {
		t.Fatalf("repeated recovery replaced the published archive: %+v %v", again, err)
	}
}

// saveFailedOverOrphanFixture models a failover: the run row and the archive
// carry one execution term, the archive is FAILED at the executor's time, and
// the row is FAILED at the successor's later reconcile time.
func saveFailedOverOrphanFixture(t *testing.T, store *storage.SQLiteStore, root, testID string) (*storage.RunArchiveRecord, time.Time) {
	t.Helper()
	saveTerminalOrphanTest(t, store, testID, storage.DefaultProjectID)
	record := writeOrphanArchiveFixture(t, root, testID, testID, storage.DefaultProjectID)
	generation := stampOrphanRunExecutionGeneration(t, store, testID)
	manifest := readOrphanManifestFixture(t, record)
	manifest.Status = "FAILED"
	manifest.Provenance.ExecutionGeneration = generation
	if err := writeIndentedJSONFile(record.ManifestPath, manifest); err != nil {
		t.Fatal(err)
	}
	row, err := store.GetTest(testID)
	if err != nil {
		t.Fatal(err)
	}
	reconciled := manifest.CompletedAt.Add(3 * time.Second)
	row.Status, row.EndTime = "FAILED", &reconciled
	if err := store.SaveTest(row); err != nil {
		t.Fatal(err)
	}
	return record, manifest.CompletedAt
}

// stampOrphanRunExecutionGeneration records an execution term on a terminal
// fixture row the way adoption does, which is only possible while it runs.
func stampOrphanRunExecutionGeneration(t *testing.T, store *storage.SQLiteStore, testID string) uint64 {
	t.Helper()
	row, err := store.GetTest(testID)
	if err != nil {
		t.Fatal(err)
	}
	status, ended := row.Status, row.EndTime
	row.Status, row.EndTime = "RUNNING", nil
	if err := store.SaveTest(row); err != nil {
		t.Fatal(err)
	}
	const lease, owner = "orphan-generation-fixture", "fixture-term"
	held, generation, err := store.TryAcquireSchedulerLeaseWithGeneration(lease, owner, time.Now(), time.Minute)
	if err != nil || !held {
		t.Fatal("acquire fixture execution term", held, err)
	}
	if ok, err := store.AdvanceTestExecutionGenerationIfLeaseTermHeld(testID, lease, owner, generation); err != nil || !ok {
		t.Fatal("stamp fixture execution term", ok, err)
	}
	row.Status, row.EndTime = status, ended
	if err := store.SaveTest(row); err != nil {
		t.Fatal(err)
	}
	return generation
}

func readOrphanManifestFixture(t *testing.T, record *storage.RunArchiveRecord) RunArchiveManifest {
	t.Helper()
	data, err := os.ReadFile(record.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest RunArchiveManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func setOrphanManifestExecutionGeneration(t *testing.T, record *storage.RunArchiveRecord, generation uint64) {
	t.Helper()
	manifest := readOrphanManifestFixture(t, record)
	manifest.Provenance.ExecutionGeneration = generation
	if err := writeIndentedJSONFile(record.ManifestPath, manifest); err != nil {
		t.Fatal(err)
	}
}

func TestOrphanRecoverySkipsPublishedPayloads(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	saveTerminalOrphanTest(t, store, "orphan", storage.DefaultProjectID)
	record := writeOrphanArchiveFixture(t, root, "orphan", "orphan", storage.DefaultProjectID)
	// Deliberately invalid payload declaration: a cataloged source must not be
	// scanned as orphan work. Reads of that archive have their own integrity gate.
	data, err := os.ReadFile(record.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest RunArchiveManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.TransactionsFile = "missing.jsonl"
	if err := writeIndentedJSONFile(record.ManifestPath, manifest); err != nil {
		t.Fatal(err)
	}
	record.CreatedAt, record.CompletedAt = manifest.CreatedAt, manifest.CompletedAt
	if err := store.SaveRunArchive(record); err != nil {
		t.Fatal(err)
	}
	tr := NewTestRunner(1, nil, nil)
	tr.testStore, tr.archiveStore = store, store
	if err := tr.recoverOrphanedLocalRunArchives(context.Background()); err != nil {
		t.Fatalf("published payload was inspected: %v", err)
	}
	if _, err := store.GetArchiveFinalizationJob("orphan"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("published source requeued", err)
	}
}

func TestOrphanDiscoveryBoundsNonCandidateEntries(t *testing.T) {
	root := t.TempDir()
	for i := 0; i <= maxOrphanArchiveRecoveryCandidates; i++ {
		file, err := os.Create(filepath.Join(root, fmt.Sprintf("irrelevant-%05d", i)))
		if err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
	candidates, err := discoverOrphanArchiveDirectories(context.Background(), root)
	if err == nil || !strings.Contains(err.Error(), "entry limit") || len(candidates) != 0 {
		t.Fatalf("unbounded scan: %d %v", len(candidates), err)
	}
	// A real orphan behind more than one bounded page must eventually enter
	// the durable outbox, even when all earlier entries are non-candidates.
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	saveTerminalOrphanTest(t, store, "late-orphan", storage.DefaultProjectID)
	writeOrphanArchiveFixture(t, root, "late-orphan", "late-orphan", storage.DefaultProjectID)
	runner := NewTestRunner(1, nil, store)
	for page := 0; page < 3; page++ {
		if err := runner.recoverOrphanedLocalRunArchives(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := store.GetArchiveFinalizationJob("late-orphan"); err == nil {
			return
		} else if !errors.Is(err, sql.ErrNoRows) {
			t.Fatal(err)
		}
	}
	t.Fatal("orphan behind bounded discovery pages was not recovered")
}

func TestOrphanDiscoveryPageTraversesRootAndRemoteAndRestarts(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 5; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("irrelevant-%d", i)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	remote := filepath.Join(root, "remote")
	writeOrphanArchiveFixture(t, remote, "remote-orphan", "remote-orphan", storage.DefaultProjectID)
	seen := map[string]int{}
	scan, err := newOrphanArchiveScanner(root)
	if err != nil {
		t.Fatal(err)
	}
	defer scan.close()
	for page := 0; page < 5; page++ {
		candidates, done, err := scan.nextPage(context.Background(), 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, candidate := range candidates {
			seen[filepath.Base(candidate.path)]++
		}
		if done {
			break
		}
	}
	if seen["remote-orphan"] != 1 {
		t.Fatalf("remote orphan discovered %d times, want once", seen["remote-orphan"])
	}
	writeOrphanArchiveFixture(t, root, "new-orphan", "new-orphan", storage.DefaultProjectID)
	newScan, err := newOrphanArchiveScanner(root)
	if err != nil {
		t.Fatal(err)
	}
	defer newScan.close()
	candidates, _, err := newScan.nextPage(context.Background(), 20)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, candidate := range candidates {
		found = found || filepath.Base(candidate.path) == "new-orphan"
	}
	if !found {
		t.Fatal("new orphan was not discovered after cursor reset")
	}
}

type archiveScanDeadlineAfterChecks struct {
	context.Context
	checks int
	allow  int
}

func (c *archiveScanDeadlineAfterChecks) Err() error {
	c.checks++
	if c.checks > c.allow {
		return context.DeadlineExceeded
	}
	return nil
}

func TestOrphanScannerResumesAfterDeadlineInsideReadBatch(t *testing.T) {
	root := t.TempDir()
	for _, id := range []string{"first", "second", "third", "fourth"} {
		writeOrphanArchiveFixture(t, root, id, id, storage.DefaultProjectID)
	}
	scan, err := newOrphanArchiveScanner(root)
	if err != nil {
		t.Fatal(err)
	}
	defer scan.close()
	// The first batch has already moved the directory stream when the
	// deadline arrives after inspecting its first entry.
	candidates, done, err := scan.nextPage(&archiveScanDeadlineAfterChecks{Context: context.Background(), allow: 3}, 10)
	if !errors.Is(err, context.DeadlineExceeded) || done || len(candidates) != 0 || len(scan.pending) != 1 || len(scan.queuedEntries) == 0 {
		t.Fatalf("deadline lost scan state: candidates=%d pending=%d queued=%d done=%v err=%v", len(candidates), len(scan.pending), len(scan.queuedEntries), done, err)
	}
	seen := map[string]int{}
	for attempt := 0; attempt < 4; attempt++ {
		candidates, done, err = scan.nextPage(context.Background(), 10)
		if err != nil {
			t.Fatal(err)
		}
		for _, candidate := range candidates {
			seen[filepath.Base(candidate.path)]++
		}
		if done {
			break
		}
	}
	for _, id := range []string{"first", "second", "third", "fourth"} {
		if seen[id] != 1 {
			t.Fatalf("orphan %s scanned %d times after deadline: %v", id, seen[id], seen)
		}
	}
}

func TestOrphanScannerClosesWhenControllerBecomesFollower(t *testing.T) {
	root := t.TempDir()
	scan, err := newOrphanArchiveScanner(root)
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	runner := NewTestRunner(1, nil, store)
	runner.orphanArchiveScan = scan
	runner.leadership = newExecutionLeadership(store, "follower")
	if err := runner.recoverOrphanedLocalRunArchives(context.Background()); !errors.Is(err, errNotExecutionLeader) {
		t.Fatalf("follower recovery error = %v", err)
	}
	if runner.orphanArchiveScan != nil || scan.root != nil || scan.directory != nil {
		t.Fatal("follower retained an orphan scan directory stream")
	}
}

func TestOrphanRecoveryUsesControllerRequestIdentityForDistributedArchive(t *testing.T) {
	tr, store, run, backend := collectionCacheFixture(t, "a")
	ended := time.Now().UTC()
	record, err := backend.stageRemoteArchive(tr, run, "COMPLETE", "", ended, true)
	if err != nil {
		t.Fatal(err)
	}
	if record.CreatedAt.Equal(run.CreatedAt) {
		t.Fatal("fixture must have a distinct generator creation time")
	}
	if _, err := store.UpdateTestStatusIfActive(run.ID, "COMPLETE", &ended, ""); err != nil {
		t.Fatal(err)
	}
	if err := tr.recoverOrphanedLocalRunArchives(context.Background()); err != nil {
		t.Fatal(err)
	}
	job, err := store.GetArchiveFinalizationJob(run.ID)
	if err != nil || job == nil {
		t.Fatal("distributed orphan was not recovered", err)
	}
	if !job.ArchiveRecord.CreatedAt.Equal(record.CreatedAt) {
		t.Fatal("recovery rewrote immutable generator creation time")
	}
}
