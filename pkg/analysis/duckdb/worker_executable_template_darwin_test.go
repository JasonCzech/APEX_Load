//go:build darwin

package duckdb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// workerTestTemplateRoot points this process's executable templates at a
// directory the test owns, starting from no template.
func workerTestTemplateRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	ReleaseNativeWorkerTemplate()
	workerExecutableTemplates.mu.Lock()
	previous := workerExecutableTemplates.root
	workerExecutableTemplates.root = func() string { return root }
	workerExecutableTemplates.failedAt = time.Time{}
	workerExecutableTemplates.mu.Unlock()
	t.Cleanup(func() {
		ReleaseNativeWorkerTemplate()
		workerExecutableTemplates.mu.Lock()
		workerExecutableTemplates.root = previous
		workerExecutableTemplates.failedAt = time.Time{}
		workerExecutableTemplates.mu.Unlock()
	})
	return root
}

func workerTestInstalledExecutable(t *testing.T, content string) (string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "installer-owned-executable")
	if err := os.WriteFile(path, []byte(content), 0700); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(content))
	return path, hex.EncodeToString(sum[:])
}

func workerTestCurrentTemplate() *workerExecutableTemplate {
	workerExecutableTemplates.mu.Lock()
	defer workerExecutableTemplates.mu.Unlock()
	return workerExecutableTemplates.current
}

func workerTestSnapshot(t *testing.T, source, expected string) (string, os.FileInfo) {
	t.Helper()
	a, _ := workerRuntimeTestAttempt(t, context.Background())
	path, err := workerSnapshotExecutable(a, source, expected)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != a.path || filepath.Base(path) != workerExecutableSnapshotName() {
		t.Fatalf("snapshot %s is not the attempt's own private path", path)
	}
	if err = workerScratchQuotas(a); err != nil {
		t.Fatal("linked snapshot bypassed the explicit scratch budget", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, info
}

// Every attempt launches its own path, all of them one inode that macOS assesses
// once, holding the approved bytes even after the installer's file changes.
func TestWorkerExecutableSnapshotsLinkOneVerifiedTemplate(t *testing.T) {
	root := workerTestTemplateRoot(t)
	source, expected := workerTestInstalledExecutable(t, "approved immutable build bytes")
	first, firstInfo := workerTestSnapshot(t, source, expected)
	if err := os.WriteFile(source, []byte("different installer build bytes"), 0700); err != nil {
		t.Fatal(err)
	}
	second, secondInfo := workerTestSnapshot(t, source, expected)
	if first == second || !os.SameFile(firstInfo, secondInfo) {
		t.Fatal("attempts did not share the verified template's inode")
	}
	for _, path := range []string{first, second} {
		content, err := os.ReadFile(path)
		if err != nil || string(content) != "approved immutable build bytes" {
			t.Fatalf("%s holds %q (%v), not the approved bytes", path, content, err)
		}
	}
	if firstInfo.Mode().Perm() != 0500 {
		t.Fatalf("snapshot mode %v, want 0500", firstInfo.Mode().Perm())
	}
	template := workerTestCurrentTemplate()
	if template == nil || filepath.Dir(template.dir) != root || template.digest != expected {
		t.Fatalf("template %+v is not this process's template for the approved digest", template)
	}
}

// A template whose shared bytes changed fails the attempt closed, and the next
// attempt builds a fresh one.
func TestWorkerExecutableTemplateTamperingFailsClosed(t *testing.T) {
	workerTestTemplateRoot(t)
	source, expected := workerTestInstalledExecutable(t, "approved immutable build bytes")
	first, firstInfo := workerTestSnapshot(t, source, expected)
	tampered := workerTestCurrentTemplate()
	templatePath := filepath.Join(tampered.dir, workerExecutableSnapshotName())
	if err := os.Chmod(templatePath, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(templatePath, []byte("tampered immutable build bytes"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(templatePath, 0500); err != nil {
		t.Fatal(err)
	}
	a, _ := workerRuntimeTestAttempt(t, context.Background())
	if _, err := workerSnapshotExecutable(a, source, expected); !errors.Is(err, errWorkerOwnership) {
		t.Fatal("tampered template was launched:", err)
	}
	if workerTestCurrentTemplate() != nil {
		t.Fatal("tampered template stayed current")
	}
	if _, err := os.Stat(tampered.dir); !os.IsNotExist(err) {
		t.Fatal("tampered template directory survived", err)
	}
	fresh, freshInfo := workerTestSnapshot(t, source, expected)
	content, err := os.ReadFile(fresh)
	if err != nil || string(content) != "approved immutable build bytes" || os.SameFile(firstInfo, freshInfo) {
		t.Fatalf("fresh template %s holds %q (%v); first snapshot was %s", fresh, content, err, first)
	}
}

// A template that disappeared (a temp-directory cleaner, an operator) is
// retired: the attempt copies as before, and a later one builds a new template.
func TestWorkerExecutableTemplateRemovedFallsBackToCopy(t *testing.T) {
	workerTestTemplateRoot(t)
	source, expected := workerTestInstalledExecutable(t, "approved immutable build bytes")
	_, firstInfo := workerTestSnapshot(t, source, expected)
	if err := os.RemoveAll(workerTestCurrentTemplate().dir); err != nil {
		t.Fatal(err)
	}
	copied, copiedInfo := workerTestSnapshot(t, source, expected)
	content, err := os.ReadFile(copied)
	if err != nil || string(content) != "approved immutable build bytes" || os.SameFile(firstInfo, copiedInfo) {
		t.Fatalf("fallback snapshot holds %q (%v)", content, err)
	}
	if workerTestCurrentTemplate() != nil {
		t.Fatal("vanished template stayed current")
	}
	_, linkedInfo := workerTestSnapshot(t, source, expected)
	if template := workerTestCurrentTemplate(); template == nil || !os.SameFile(template.info, linkedInfo) {
		t.Fatal("a later attempt did not link a new template")
	}
}

// A different approved build replaces the template; release removes it while
// attempts keep the bytes they linked.
func TestWorkerExecutableTemplateFollowsTheApprovedDigest(t *testing.T) {
	workerTestTemplateRoot(t)
	source, expected := workerTestInstalledExecutable(t, "approved immutable build bytes")
	workerTestSnapshot(t, source, expected)
	previous := workerTestCurrentTemplate()
	otherSource, otherExpected := workerTestInstalledExecutable(t, "next approved build's bytes")
	linked, _ := workerTestSnapshot(t, otherSource, otherExpected)
	current := workerTestCurrentTemplate()
	if current == nil || current == previous || current.digest != otherExpected {
		t.Fatal("template did not follow the approved digest")
	}
	if _, err := os.Stat(previous.dir); !os.IsNotExist(err) {
		t.Fatal("replaced template directory survived", err)
	}
	ReleaseNativeWorkerTemplate()
	if _, err := os.Stat(current.dir); !os.IsNotExist(err) || workerTestCurrentTemplate() != nil {
		t.Fatal("release left the template", err)
	}
	if content, err := os.ReadFile(linked); err != nil || string(content) != "next approved build's bytes" {
		t.Fatal("release took an attempt's linked snapshot", err)
	}
}

// The sweep removes templates whose process exited (their lock is free) and
// lock-less ones old enough to be crash leftovers, and nothing else.
func TestWorkerExecutableTemplateSweepRemovesOnlyDeadOwners(t *testing.T) {
	root := t.TempDir()
	dir := func(name string, lock bool) string {
		path := filepath.Join(root, name)
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		if lock {
			if err := os.WriteFile(filepath.Join(path, workerExecutableTemplateLockName), nil, 0600); err != nil {
				t.Fatal(err)
			}
		}
		return path
	}
	dead := dir(workerExecutableTemplatePrefix+"dead", true)
	live := dir(workerExecutableTemplatePrefix+"live", true)
	creating := dir(workerExecutableTemplatePrefix+"creating", false)
	crashed := dir(workerExecutableTemplatePrefix+"crashed", false)
	unrelated := dir("apexload-other", true)
	old := time.Now().Add(-2 * workerExecutableTemplateStaleAge)
	if err := os.Chtimes(crashed, old, old); err != nil {
		t.Fatal(err)
	}
	holder, err := os.OpenFile(filepath.Join(live, workerExecutableTemplateLockName), os.O_RDWR|unix.O_EXLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	workerSweepExecutableTemplates(root)
	for path, want := range map[string]bool{dead: false, crashed: false, live: true, creating: true, unrelated: true} {
		if _, err := os.Stat(path); (err == nil) != want {
			t.Errorf("%s: kept=%t, want %t (%v)", filepath.Base(path), err == nil, want, err)
		}
	}
}

// A template that cannot be built is not retried for a while: attempts copy as
// before instead of each writing a partial template first.
func TestWorkerExecutableTemplateFailureBacksOff(t *testing.T) {
	root := workerTestTemplateRoot(t)
	source, expected := workerTestInstalledExecutable(t, "approved immutable build bytes")
	if err := os.Chmod(root, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0700) })
	copied, _ := workerTestSnapshot(t, source, expected)
	if content, err := os.ReadFile(copied); err != nil || string(content) != "approved immutable build bytes" {
		t.Fatal("fallback copy did not hold the approved bytes", err)
	}
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	workerTestSnapshot(t, source, expected)
	if workerTestCurrentTemplate() != nil {
		t.Fatal("template was retried inside the back-off")
	}
	workerExecutableTemplates.mu.Lock()
	workerExecutableTemplates.failedAt = time.Now().Add(-2 * workerExecutableTemplateRetryAfter)
	workerExecutableTemplates.mu.Unlock()
	workerTestSnapshot(t, source, expected)
	if workerTestCurrentTemplate() == nil {
		t.Fatal("template was not retried after the back-off")
	}
}

// A lock path that is not a regular file is skipped rather than opened as a
// lock, which for a FIFO would block the sweep.
func TestWorkerExecutableTemplateSweepSkipsIrregularLock(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, workerExecutableTemplatePrefix+"fifo")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(filepath.Join(dir, workerExecutableTemplateLockName), 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		workerSweepExecutableTemplates(root)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("sweep blocked on a FIFO lock path")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal("sweep removed a template it could not prove dead", err)
	}
}
