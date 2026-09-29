package api

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestArchiveAdjunctAdmissionRetainsSourceAndRetries(t *testing.T) {
	for _, mode := range []string{"bytes", "free", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
			t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
			store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			run, record := createArchiveFinalizationTest(t, store, "adjunct-budget", root)
			before, err := os.ReadFile(record.ManifestPath)
			if err != nil {
				t.Fatal(err)
			}
			runner := NewTestRunner(1, nil, store)
			t.Setenv("APEXLOAD_ARCHIVE_ADJUNCT_MIN_FREE_BYTES", "1")
			switch mode {
			case "bytes":
				t.Setenv("APEXLOAD_ARCHIVE_ADJUNCT_MAX_BYTES", "1")
			case "free":
				t.Setenv("APEXLOAD_ARCHIVE_ADJUNCT_MIN_FREE_BYTES", "1125899906842624")
			case "invalid":
				t.Setenv("APEXLOAD_ARCHIVE_ADJUNCT_MAX_BYTES", "invalid")
			}
			if err := runner.finalizeRunArchiveDurably(context.Background(), run, record, true); err == nil {
				t.Fatal("adjunct admission was bypassed")
			}
			after, err := os.ReadFile(record.ManifestPath)
			if err != nil || string(before) != string(after) {
				t.Fatal("rejection changed source manifest", err)
			}
			if _, err := os.Stat(filepath.Join(record.ArchivePath, testNotesArchiveFileName)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("rejected notes were written", err)
			}
			job, err := store.GetArchiveFinalizationJob(run.ID)
			if err != nil {
				t.Fatal("failure lost durable retry", err)
			}
			row, err := store.GetTest(run.ID)
			if err != nil || row.Status != "COMPLETE" {
				t.Fatal("failure changed execution outcome", row, err)
			}
			t.Setenv("APEXLOAD_ARCHIVE_ADJUNCT_MAX_BYTES", "67108864")
			t.Setenv("APEXLOAD_ARCHIVE_ADJUNCT_MIN_FREE_BYTES", "1")
			if err := runner.processArchiveFinalizationJob(context.Background(), job); err != nil {
				t.Fatal("retry", err)
			}
			catalog, err := store.GetRunArchive(run.ID)
			if err != nil {
				t.Fatal(err)
			}
			actual, err := os.ReadFile(catalog.ManifestPath)
			if err != nil || sha256BytesHex(actual) != catalog.ManifestSHA256 {
				t.Fatal("retry digest mismatch", err)
			}
		})
	}
}

func TestArchiveAdjunctConfigurationFrozenAndValidated(t *testing.T) {
	for key, maximum := range map[string]int64{envArchiveAdjunctMaxBytes: 1 << 30, envArchiveAdjunctMinFreeBytes: 1 << 50} {
		for _, raw := range []string{"0", "-1", "x", "1.1", strconv.FormatInt(maximum+1, 10)} {
			t.Run(key+raw, func(t *testing.T) {
				t.Setenv(key, raw)
				if _, err := newArchiveAdjunctBudget(context.Background(), t.TempDir()); err == nil {
					t.Fatal("invalid config accepted")
				}
			})
		}
		t.Run(key+"boundary", func(t *testing.T) {
			t.Setenv(key, strconv.FormatInt(maximum, 10))
			if _, err := newArchiveAdjunctBudget(context.Background(), t.TempDir()); err != nil {
				t.Fatal(err)
			}
		})
	}
	t.Setenv(envArchiveAdjunctMaxBytes, "7")
	t.Setenv(envArchiveAdjunctMinFreeBytes, "1")
	budget, err := newArchiveAdjunctBudget(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(envArchiveAdjunctMaxBytes, "999")
	if budget.remaining != 7 || budget.minFree != 1 {
		t.Fatal("allowance changed after creation")
	}
}

func TestArchiveAdjunctAtomicReplacementRetainsPriorFile(t *testing.T) {
	for _, mode := range []string{"bytes", "free", "cancel", "linked"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "notes.json")
			if err := os.WriteFile(path, []byte("prior"), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			budget := &archiveOutputBudget{root: root, remaining: 4, minFree: 1, limitErr: errArchiveAdjunctLimit, freeSpaceErr: errArchiveAdjunctFreeSpace, ctx: ctx}
			expected := errArchiveAdjunctLimit
			switch mode {
			case "free":
				budget.remaining = 100
				budget.minFree = 1 << 50
				expected = errArchiveAdjunctFreeSpace
			case "cancel":
				budget.remaining = 100
				cancel()
				expected = context.Canceled
			case "linked":
				if err := os.Rename(path, filepath.Join(root, "original")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("original", path); err != nil {
					t.Fatal(err)
				}
			}
			err := writeArchiveArtifactDurableWithBudget(root, "notes.json", []byte("replacement"), 0600, budget)
			if err == nil || mode != "linked" && !errors.Is(err, expected) {
				t.Fatal("unexpected rejection", err)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != "prior" {
				t.Fatal("prior file lost", err)
			}
			entries, err := os.ReadDir(root)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if entry.Name() != "notes.json" && entry.Name() != "original" && entry.Name() != "_capture_locks" {
					t.Fatal("temporary file leaked", entry.Name())
				}
			}
		})
	}
}

func TestArchiveAdjunctFinalManifestSharesAllowance(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	t.Setenv(envArchiveAdjunctMinFreeBytes, "1")
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	run, record := createArchiveFinalizationTest(t, store, "shared-adjunct", root)
	runner := NewTestRunner(1, nil, store)
	if err := runner.persistTestManifestArtifact(run, record); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(record.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, name := range []string{runArchiveManifestFileName, testRunManifestFileName, testNotesArchiveFileName} {
		info, err := os.Stat(filepath.Join(record.ArchivePath, name))
		if err != nil {
			t.Fatal(err)
		}
		total += info.Size()
	}
	// Timestamp formatting can vary by a few bytes; reject comfortably within the
	// final manifest while still allowing both adjuncts to be replaced.
	t.Setenv(envArchiveAdjunctMaxBytes, strconv.FormatInt(total-64, 10))
	prepared := false
	err = runner.persistTestManifestArtifactWithPreparation(run, record, func([]byte) error { prepared = true; return nil }, nil)
	if !prepared || !errors.Is(err, errArchiveAdjunctLimit) {
		t.Fatal("final manifest did not share the allowance", prepared, err)
	}
	after, readErr := os.ReadFile(record.ManifestPath)
	if readErr != nil || string(after) != string(before) {
		t.Fatal("rejected manifest replaced old marker", readErr)
	}
	t.Setenv(envArchiveAdjunctMaxBytes, "67108864")
	if err := runner.persistTestManifestArtifact(run, record); err != nil {
		t.Fatal("retry", err)
	}
}

func TestArchiveAdjunctPreparedRecoveryUsesAdmission(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	t.Setenv(envArchiveAdjunctMinFreeBytes, "1")
	sqlite, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlite.Close()
	store := &interruptedArchiveManifestStore{SQLiteStore: sqlite, failPreparation: "after"}
	run, record := createArchiveFinalizationTest(t, store, "adjunct-recovery", root)
	runner := NewTestRunner(1, nil, store)
	if err := runner.finalizeRunArchiveDurably(context.Background(), run, record, true); err == nil {
		t.Fatal("interruption ignored")
	}
	before, err := os.ReadFile(record.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.GetArchiveFinalizationJob(run.ID)
	if err != nil || job.PreparedManifestJSON == "" {
		t.Fatal("no durable preparation", err)
	}
	restarted := NewTestRunner(1, nil, sqlite)
	t.Setenv(envArchiveAdjunctMaxBytes, "1")
	if err := restarted.processArchiveFinalizationJob(context.Background(), job); !errors.Is(err, errArchiveAdjunctLimit) {
		t.Fatal("prepared recovery bypassed allowance", err)
	}
	after, err := os.ReadFile(record.ManifestPath)
	if err != nil || string(after) != string(before) {
		t.Fatal("rejected recovery changed manifest", err)
	}
	t.Setenv(envArchiveAdjunctMaxBytes, "67108864")
	if err := restarted.processArchiveFinalizationJob(context.Background(), job); err != nil {
		t.Fatal("recovery retry", err)
	}
	if _, err := sqlite.GetRunArchive(run.ID); err != nil {
		t.Fatal(err)
	}
}

func TestArchiveAdjunctCancellationAfterPreparationRetainsManifest(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	t.Setenv(envArchiveAdjunctMinFreeBytes, "1")
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	run, record := createArchiveFinalizationTest(t, store, "adjunct-cancel", root)
	runner := NewTestRunner(1, nil, store)
	before, err := os.ReadFile(record.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	budget, err := newArchiveAdjunctBudget(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer budget.close()
	err = runner.persistTestManifestArtifactWithPreparation(run, record, func([]byte) error { cancel(); return nil }, budget)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
	after, err := os.ReadFile(record.ManifestPath)
	if err != nil || string(after) != string(before) {
		t.Fatal("canceled final manifest replaced prior marker", err)
	}
}
