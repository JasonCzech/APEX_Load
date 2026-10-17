package api

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

type interruptedArchiveManifestStore struct {
	SQLiteStore     *storage.SQLiteStore
	failUpdate      bool
	failPreparation string
}

func (s *interruptedArchiveManifestStore) UpdateArchiveFinalizationRecordIfToken(id, token string, record *storage.RunArchiveRecord) (bool, error) {
	if s.failUpdate {
		return false, errors.New("interrupted after manifest replacement")
	}
	return s.SQLiteStore.UpdateArchiveFinalizationRecordIfToken(id, token, record)
}
func (s *interruptedArchiveManifestStore) PrepareArchiveFinalizationManifestIfToken(id, token string, record *storage.RunArchiveRecord, data string) (bool, error) {
	if s.failPreparation == "before" {
		return false, errors.New("preparation unavailable")
	}
	current, err := s.SQLiteStore.PrepareArchiveFinalizationManifestIfToken(id, token, record, data)
	if err == nil && current && s.failPreparation == "after" {
		return false, errors.New("interrupted after durable preparation")
	}
	return current, err
}

func TestArchiveManifestPreparationSurvivesCommitBoundaries(t *testing.T) {
	for _, mode := range []string{"before preparation", "after preparation", "after manifest"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
			t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
			sqlite, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer sqlite.Close()
			store := &interruptedArchiveManifestStore{SQLiteStore: sqlite, failUpdate: mode == "after manifest"}
			if mode == "before preparation" {
				store.failPreparation = "before"
			}
			if mode == "after preparation" {
				store.failPreparation = "after"
			}
			test, record := createArchiveFinalizationTest(t, sqliteOverlay(store), "prepared-run", root)
			original, err := os.ReadFile(record.ManifestPath)
			if err != nil {
				t.Fatal(err)
			}
			runner := NewTestRunner(1, NewHub(), sqliteOverlay(store))
			if err := runner.finalizeRunArchiveDurably(context.Background(), test, record, true); err == nil {
				t.Fatal("interruption ignored")
			}
			job, err := store.SQLiteStore.GetArchiveFinalizationJob(test.ID)
			if err != nil {
				t.Fatal(err)
			}
			local, err := os.ReadFile(record.ManifestPath)
			if err != nil {
				t.Fatal(err)
			}
			if mode != "after manifest" && string(local) != string(original) {
				t.Fatal("manifest replaced before durable preparation returned")
			}
			if mode == "before preparation" && job.PreparedManifestJSON != "" {
				t.Fatal("failed preparation persisted")
			}
			if mode != "before preparation" && (job.PreparedManifestJSON == "" || sha256BytesHex([]byte(job.PreparedManifestJSON)) != job.ArchiveRecord.ManifestSHA256) {
				t.Fatal("durable preparation absent or inconsistent")
			}
			restarted := NewTestRunner(1, NewHub(), sqlite)
			if err := restarted.processArchiveFinalizationJob(context.Background(), job); err != nil {
				t.Fatalf("restart failed: %v", err)
			}
			catalog, err := sqlite.GetRunArchive(test.ID)
			if err != nil {
				t.Fatal(err)
			}
			actual, err := os.ReadFile(catalog.ManifestPath)
			if err != nil || sha256BytesHex(actual) != catalog.ManifestSHA256 {
				t.Fatalf("catalog trust anchor: %v", err)
			}
		})
	}
}

func TestArchiveManifestPreparationRejectsChangedArtifact(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	sqlite, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlite.Close()
	store := &interruptedArchiveManifestStore{SQLiteStore: sqlite, failPreparation: "after"}
	test, record := createArchiveFinalizationTest(t, sqliteOverlay(store), "prepared-corrupt", root)
	runner := NewTestRunner(1, NewHub(), sqliteOverlay(store))
	if err := runner.finalizeRunArchiveDurably(context.Background(), test, record, true); err == nil {
		t.Fatal("interruption ignored")
	}
	before, err := os.ReadFile(record.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(record.ArchivePath, testNotesArchiveFileName), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	job, err := sqlite.GetArchiveFinalizationJob(test.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := NewTestRunner(1, NewHub(), sqlite).processArchiveFinalizationJob(context.Background(), job); err == nil {
		t.Fatal("changed artifact was re-blessed")
	}
	after, err := os.ReadFile(record.ManifestPath)
	if err != nil || string(after) != string(before) {
		t.Fatal("failed recovery changed manifest")
	}
}
