package api

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestCaptureAPEXLoadEnvUsesSafeAllowlist(t *testing.T) {
	t.Setenv("APEXLOAD_ENVIRONMENT", "production\nwest")
	t.Setenv("APEXLOAD_VERSION", strings.Repeat("v", 600))
	t.Setenv("APEXLOAD_DATABASE_URL", "postgres://admin:database-password@db.example.test/apexload")
	t.Setenv("APEXLOAD_REDIS_URL", "redis://:redis-password@cache.example.test:6379/0")
	t.Setenv("APEXLOAD_ARCHIVE_ENDPOINT", "https://user:password@objects.example.test?signature=sensitive")
	t.Setenv("APEXLOAD_SAMPLE_FLAG", "not-approved-for-provenance")

	env := captureAPEXLoadEnv()
	if got := env["APEXLOAD_ENVIRONMENT"]; got != "productionwest" {
		t.Fatalf("APEXLOAD_ENVIRONMENT = %q, want sanitized deployment value", got)
	}
	if got := env["APEXLOAD_VERSION"]; len(got) != 512 {
		t.Fatalf("len(APEXLOAD_VERSION) = %d, want bounded value length 512", len(got))
	}
	for _, key := range []string{
		"APEXLOAD_DATABASE_URL",
		"APEXLOAD_REDIS_URL",
		"APEXLOAD_ARCHIVE_ENDPOINT",
		"APEXLOAD_SAMPLE_FLAG",
	} {
		if value, exists := env[key]; exists {
			t.Fatalf("captureAPEXLoadEnv exposed non-allowlisted %s=%q", key, value)
		}
	}
}

func TestRunArchiveWriterRejectsUnsafeTestIDBeforeFilesystemIO(t *testing.T) {
	for _, testID := range []string{"../escape", "nested/run", `nested\\run`, " run-with-padding ", "run\nnewline"} {
		t.Run(strings.NewReplacer("/", "-", `\\`, "-").Replace(testID), func(t *testing.T) {
			archiveRoot := filepath.Join(t.TempDir(), "must-not-be-created")
			t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
			t.Setenv("APEXLOAD_ARCHIVE_DIR", archiveRoot)

			if _, err := newRunArchiveWriter(&ManagedTest{ID: testID, Name: "unsafe"}, RunArchiveProvenance{}); err == nil {
				t.Fatalf("newRunArchiveWriter accepted unsafe test id %q", testID)
			}
			if _, err := os.Lstat(archiveRoot); !os.IsNotExist(err) {
				t.Fatalf("unsafe id %q caused filesystem I/O at archive root: %v", testID, err)
			}
		})
	}
}

func TestRunArchiveWriterRejectsLinkedRootsAndTargets(t *testing.T) {
	t.Run("configured root", func(t *testing.T) {
		parent := t.TempDir()
		outside := t.TempDir()
		archiveRoot := filepath.Join(parent, "archives")
		if err := os.Symlink(outside, archiveRoot); err != nil {
			t.Fatal(err)
		}
		t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
		t.Setenv("APEXLOAD_ARCHIVE_DIR", archiveRoot)

		if _, err := newRunArchiveWriter(&ManagedTest{ID: "linked-root-run", Name: "linked"}, RunArchiveProvenance{}); err == nil {
			t.Fatal("newRunArchiveWriter accepted a linked configured root")
		}
		if _, err := os.Lstat(filepath.Join(outside, "linked-root-run")); !os.IsNotExist(err) {
			t.Fatalf("writer created a run through configured-root symlink: %v", err)
		}
	})

	t.Run("run root", func(t *testing.T) {
		archiveRoot := t.TempDir()
		outside := t.TempDir()
		outsideTx := filepath.Join(outside, "transactions.jsonl")
		if err := os.WriteFile(outsideTx, []byte("outside-sentinel"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(archiveRoot, "linked-run")); err != nil {
			t.Fatal(err)
		}
		t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
		t.Setenv("APEXLOAD_ARCHIVE_DIR", archiveRoot)

		if _, err := newRunArchiveWriter(&ManagedTest{ID: "linked-run", Name: "linked"}, RunArchiveProvenance{}); err == nil {
			t.Fatal("newRunArchiveWriter accepted a linked run root")
		}
		content, err := os.ReadFile(outsideTx)
		if err != nil || string(content) != "outside-sentinel" {
			t.Fatalf("linked run root modified outside transaction file: %q, %v", content, err)
		}
	})

	t.Run("artifact target", func(t *testing.T) {
		archiveRoot := t.TempDir()
		runRoot := filepath.Join(archiveRoot, "linked-artifact-run")
		if err := os.Mkdir(runRoot, 0o755); err != nil {
			t.Fatal(err)
		}
		outsideTx := filepath.Join(t.TempDir(), "outside-transactions.jsonl")
		if err := os.WriteFile(outsideTx, []byte("outside-sentinel"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outsideTx, filepath.Join(runRoot, "transactions.jsonl")); err != nil {
			t.Fatal(err)
		}
		t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
		t.Setenv("APEXLOAD_ARCHIVE_DIR", archiveRoot)

		if _, err := newRunArchiveWriter(&ManagedTest{ID: "linked-artifact-run", Name: "linked"}, RunArchiveProvenance{}); err == nil {
			t.Fatal("newRunArchiveWriter accepted a linked artifact target")
		}
		content, err := os.ReadFile(outsideTx)
		if err != nil || string(content) != "outside-sentinel" {
			t.Fatalf("linked artifact target was truncated: %q, %v", content, err)
		}
	})

	t.Run("non-directory run root", func(t *testing.T) {
		archiveRoot := t.TempDir()
		if err := os.WriteFile(filepath.Join(archiveRoot, "file-run"), []byte("sentinel"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
		t.Setenv("APEXLOAD_ARCHIVE_DIR", archiveRoot)

		if _, err := newRunArchiveWriter(&ManagedTest{ID: "file-run", Name: "file"}, RunArchiveProvenance{}); err == nil {
			t.Fatal("newRunArchiveWriter accepted a non-directory run root")
		}
	})
}

func TestArchiveFreshInodeWriteResistsRunRootSymlinkSwap(t *testing.T) {
	parent := t.TempDir()
	runRoot := filepath.Join(parent, "run-live")
	parkedRoot := filepath.Join(parent, "run-parked")
	outside := t.TempDir()
	outsideTarget := filepath.Join(outside, "transactions.jsonl")
	if err := os.Mkdir(runRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outsideTarget, []byte("outside-sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if err := os.Rename(runRoot, parkedRoot); err != nil {
				continue
			}
			if err := os.Symlink(outside, runRoot); err == nil {
				_ = os.Remove(runRoot)
			}
			_ = os.Rename(parkedRoot, runRoot)
		}
	}()

	for attempt := 0; attempt < 500; attempt++ {
		file, err := replaceArchiveArtifactForWrite(runRoot, "transactions.jsonl", 0o600, false)
		if err != nil {
			continue
		}
		_, _ = file.Write([]byte("owned\n"))
		_ = file.Close()
	}
	close(stop)
	wg.Wait()
	if info, err := os.Lstat(runRoot); err == nil && info.Mode()&os.ModeSymlink != 0 {
		_ = os.Remove(runRoot)
	}
	if _, err := os.Lstat(runRoot); os.IsNotExist(err) {
		_ = os.Rename(parkedRoot, runRoot)
	}

	content, err := os.ReadFile(outsideTarget)
	if err != nil || string(content) != "outside-sentinel" {
		t.Fatalf("symlink swap redirected archive write outside run root: %q, %v", content, err)
	}
}
