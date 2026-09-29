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
	for _, mode := range []string{"start", "end", "status", "missing-end"} {
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
			row, err := store.GetTest("orphan")
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "start":
				row.StartTime = row.StartTime.Add(time.Second)
			case "end":
				ended := row.EndTime.Add(time.Second)
				row.EndTime = &ended
			case "status":
				row.Status = "FAILED"
			case "missing-end":
				row.EndTime = nil
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

type changedOrphanRunStore struct{ *storage.SQLiteStore }

func (s *changedOrphanRunStore) SaveArchiveFinalizationJobIfOrphanForRun(job *storage.ArchiveFinalizationJob, requestedAt time.Time, status, lease, owner string) (bool, error) {
	row, err := s.GetTest(job.TestID)
	if err != nil {
		return false, err
	}
	row.StartTime = row.StartTime.Add(time.Second)
	if err := s.SaveTest(row); err != nil {
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
	tr.testStore, tr.archiveStore = store, &changedOrphanRunStore{store}
	if err := tr.recoverOrphanedLocalRunArchives(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetArchiveFinalizationJob("orphan"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("changed incarnation queued", err)
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
