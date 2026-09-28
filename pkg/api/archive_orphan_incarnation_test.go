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

func (s *changedOrphanRunStore) SaveArchiveFinalizationJobIfOrphanForRun(job *storage.ArchiveFinalizationJob, status, lease, owner string) (bool, error) {
	row, err := s.GetTest(job.TestID)
	if err != nil {
		return false, err
	}
	row.StartTime = row.StartTime.Add(time.Second)
	if err := s.SaveTest(row); err != nil {
		return false, err
	}
	return s.SQLiteStore.SaveArchiveFinalizationJobIfOrphanForRun(job, status, lease, owner)
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
}
