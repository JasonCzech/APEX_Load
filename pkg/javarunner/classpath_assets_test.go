package javarunner

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/fileasset"
)

func writeSourceJar(t *testing.T, dir, name string, content []byte) (path, digest, storageRef string) {
	t.Helper()
	path = filepath.Join(dir, name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write source jar: %v", err)
	}
	sum := sha256.Sum256(content)
	digest = hex.EncodeToString(sum[:])
	storageRef = (&url.URL{Scheme: "file", Path: path}).String()
	return path, digest, storageRef
}

func TestMaterializeClasspathAssetsHappyPath(t *testing.T) {
	sourceDir := t.TempDir()
	if err := fileasset.RegisterRoot(sourceDir); err != nil {
		t.Fatalf("register root: %v", err)
	}
	content := []byte("PK\x03\x04 fake jar bytes")
	_, digest, storageRef := writeSourceJar(t, sourceDir, "app.jar", content)

	workDir := t.TempDir()
	classpath := []string{"app.jar"}
	assets := []ClasspathAsset{{Entry: "app.jar", SHA256: digest, StorageRef: storageRef}}

	rewritten, issue := MaterializeClasspathAssets(classpath, assets, workDir)
	if issue != nil {
		t.Fatalf("unexpected issue: %v", issue)
	}
	want := filepath.Join(workDir, ClasspathAssetDirName, digest+".jar")
	if rewritten[0] != want {
		t.Fatalf("rewritten entry = %q, want %q", rewritten[0], want)
	}
	got, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("read materialized jar: %v", err)
	}
	if string(got) != string(content) {
		t.Fatalf("materialized bytes differ from source")
	}

	// The rewritten entry must resolve cleanly (the file now exists).
	resolved, resolveIssue := ResolveClasspath(rewritten, workDir)
	if resolveIssue != nil {
		t.Fatalf("resolve rewritten classpath failed: %v", resolveIssue)
	}
	if len(resolved) != 1 {
		t.Fatalf("resolved len = %d, want 1", len(resolved))
	}
}

func TestMaterializeClasspathAssetsNoAssetsIsPassthrough(t *testing.T) {
	classpath := []string{"/opt/lib/a.jar", "/opt/lib/b.jar"}
	rewritten, issue := MaterializeClasspathAssets(classpath, nil, "")
	if issue != nil {
		t.Fatalf("unexpected issue: %v", issue)
	}
	if len(rewritten) != len(classpath) {
		t.Fatalf("len changed: got %d want %d", len(rewritten), len(classpath))
	}
	for i := range classpath {
		if rewritten[i] != classpath[i] {
			t.Fatalf("entry %d changed: got %q want %q", i, rewritten[i], classpath[i])
		}
	}
}

func TestMaterializeClasspathAssetsUnmatchedEntryIgnored(t *testing.T) {
	sourceDir := t.TempDir()
	if err := fileasset.RegisterRoot(sourceDir); err != nil {
		t.Fatalf("register root: %v", err)
	}
	_, digest, storageRef := writeSourceJar(t, sourceDir, "other.jar", []byte("PK\x03\x04 other"))
	workDir := t.TempDir()
	classpath := []string{"app.jar"}
	// Asset binds an entry not present in the classpath; it must be ignored and
	// the classpath returned unchanged.
	assets := []ClasspathAsset{{Entry: "other.jar", SHA256: digest, StorageRef: storageRef}}
	rewritten, issue := MaterializeClasspathAssets(classpath, assets, workDir)
	if issue != nil {
		t.Fatalf("unexpected issue: %v", issue)
	}
	if rewritten[0] != "app.jar" {
		t.Fatalf("entry rewritten unexpectedly: %q", rewritten[0])
	}
}

func TestMaterializeClasspathAssetsShaMismatch(t *testing.T) {
	sourceDir := t.TempDir()
	if err := fileasset.RegisterRoot(sourceDir); err != nil {
		t.Fatalf("register root: %v", err)
	}
	_, _, storageRef := writeSourceJar(t, sourceDir, "app.jar", []byte("PK\x03\x04 real"))
	workDir := t.TempDir()
	wrongDigest := hex.EncodeToString(make([]byte, sha256.Size)) // all-zero, valid hex, wrong value
	assets := []ClasspathAsset{{Entry: "app.jar", SHA256: wrongDigest, StorageRef: storageRef}}
	_, issue := MaterializeClasspathAssets([]string{"app.jar"}, assets, workDir)
	if issue == nil || issue.Category != ClasspathAssetIntegrity {
		t.Fatalf("expected integrity issue, got %v", issue)
	}
	if issue.Index != 0 {
		t.Fatalf("expected index 0, got %d", issue.Index)
	}
}

func TestMaterializeClasspathAssetsMissingSource(t *testing.T) {
	workDir := t.TempDir()
	// StorageRef points at a path that is not within any registered root.
	assets := []ClasspathAsset{{
		Entry:      "app.jar",
		SHA256:     hex.EncodeToString(make([]byte, sha256.Size)),
		StorageRef: (&url.URL{Scheme: "file", Path: filepath.Join(t.TempDir(), "missing.jar")}).String(),
	}}
	_, issue := MaterializeClasspathAssets([]string{"app.jar"}, assets, workDir)
	if issue == nil || issue.Category != ClasspathAssetUnavailable {
		t.Fatalf("expected unavailable issue, got %v", issue)
	}
}

func TestMaterializeClasspathAssetsEmptyStorageRef(t *testing.T) {
	workDir := t.TempDir()
	assets := []ClasspathAsset{{Entry: "app.jar", SHA256: hex.EncodeToString(make([]byte, sha256.Size))}}
	_, issue := MaterializeClasspathAssets([]string{"app.jar"}, assets, workDir)
	if issue == nil || issue.Category != ClasspathAssetUnavailable {
		t.Fatalf("expected unavailable issue for empty storage_ref, got %v", issue)
	}
}

func TestMaterializeClasspathAssetsRequiresWorkDir(t *testing.T) {
	assets := []ClasspathAsset{{Entry: "app.jar", SHA256: hex.EncodeToString(make([]byte, sha256.Size)), StorageRef: "file:///x"}}
	_, issue := MaterializeClasspathAssets([]string{"app.jar"}, assets, "")
	if issue == nil || issue.Category != ClasspathAssetWorkDirRequired {
		t.Fatalf("expected workdir_required issue, got %v", issue)
	}
	if issue.Index != -1 {
		t.Fatalf("expected index -1, got %d", issue.Index)
	}
}

func TestMaterializeClasspathAssetsIdempotent(t *testing.T) {
	sourceDir := t.TempDir()
	if err := fileasset.RegisterRoot(sourceDir); err != nil {
		t.Fatalf("register root: %v", err)
	}
	_, digest, storageRef := writeSourceJar(t, sourceDir, "app.jar", []byte("PK\x03\x04 twice"))
	workDir := t.TempDir()
	assets := []ClasspathAsset{{Entry: "app.jar", SHA256: digest, StorageRef: storageRef}}

	first, issue := MaterializeClasspathAssets([]string{"app.jar"}, assets, workDir)
	if issue != nil {
		t.Fatalf("first materialize failed: %v", issue)
	}
	firstInfo, err := os.Stat(first[0])
	if err != nil {
		t.Fatalf("stat first: %v", err)
	}
	second, issue := MaterializeClasspathAssets([]string{"app.jar"}, assets, workDir)
	if issue != nil {
		t.Fatalf("second materialize failed: %v", issue)
	}
	if first[0] != second[0] {
		t.Fatalf("idempotent path mismatch: %q vs %q", first[0], second[0])
	}
	// The second call must take the os.Stat fast path and NOT re-write the file:
	// the modification time must be unchanged (proving no re-copy).
	secondInfo, err := os.Stat(second[0])
	if err != nil {
		t.Fatalf("stat second: %v", err)
	}
	if !firstInfo.ModTime().Equal(secondInfo.ModTime()) {
		t.Fatalf("file was re-written on the second call (modtime changed): %v -> %v", firstInfo.ModTime(), secondInfo.ModTime())
	}
	// No leftover temp files from either call.
	assertNoPartials(t, filepath.Join(workDir, ClasspathAssetDirName))
}

func TestMaterializeClasspathAssetsConcurrent(t *testing.T) {
	sourceDir := t.TempDir()
	if err := fileasset.RegisterRoot(sourceDir); err != nil {
		t.Fatalf("register root: %v", err)
	}
	// A multi-megabyte body so the copy window is wide enough for workers to
	// genuinely overlap (a tiny payload copies instantly and never exercises the
	// concurrent create/sweep/rename interleavings this test exists to cover).
	content := append([]byte("PK\x03\x04"), bytes.Repeat([]byte("concurrent-materialization-body"), 200_000)...)
	_, digest, storageRef := writeSourceJar(t, sourceDir, "app.jar", content)
	workDir := t.TempDir()
	assets := []ClasspathAsset{{Entry: "app.jar", SHA256: digest, StorageRef: storageRef}}

	const workers = 16
	var wg sync.WaitGroup
	results := make([]string, workers)
	issues := make([]*ClasspathAssetIssue, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			out, issue := MaterializeClasspathAssets([]string{"app.jar"}, assets, workDir)
			issues[idx] = issue
			if issue == nil && len(out) == 1 {
				results[idx] = out[0]
			}
		}(i)
	}
	wg.Wait()

	final := filepath.Join(workDir, ClasspathAssetDirName, digest+".jar")
	for i := 0; i < workers; i++ {
		if issues[i] != nil {
			t.Fatalf("worker %d failed: %v", i, issues[i])
		}
		if results[i] != final {
			t.Fatalf("worker %d path = %q, want %q", i, results[i], final)
		}
	}
	got, err := os.ReadFile(final)
	if err != nil {
		t.Fatalf("read final: %v", err)
	}
	if string(got) != string(content) {
		t.Fatalf("final bytes corrupted under concurrency")
	}
	assertNoPartials(t, filepath.Join(workDir, ClasspathAssetDirName))
}

// The reclaim sweep must never remove a temp a concurrent worker is still
// writing: doing so makes that worker's os.Rename fail with ENOENT and surface a
// spurious classpath_asset_workdir_required. Only temps older than the grace
// window (orphaned by a dead process) may be reclaimed.
func TestSweepClasspathAssetPartialsPreservesInFlightTemps(t *testing.T) {
	sourceDir := t.TempDir()
	if err := fileasset.RegisterRoot(sourceDir); err != nil {
		t.Fatalf("register root: %v", err)
	}
	_, digest, storageRef := writeSourceJar(t, sourceDir, "app.jar", []byte("PK\x03\x04 sweep guard"))
	workDir := t.TempDir()
	assetDir := filepath.Join(workDir, ClasspathAssetDirName)
	if err := os.MkdirAll(assetDir, 0o700); err != nil {
		t.Fatalf("mkdir asset dir: %v", err)
	}

	// A sibling worker's in-flight copy (fresh mtime).
	inFlight := filepath.Join(assetDir, classpathAssetPartialPrefix+"inflight")
	if err := os.WriteFile(inFlight, []byte("partially copied bytes"), 0o600); err != nil {
		t.Fatalf("write in-flight temp: %v", err)
	}
	// A temp orphaned by a process that died long ago.
	orphan := filepath.Join(assetDir, classpathAssetPartialPrefix+"orphan")
	if err := os.WriteFile(orphan, []byte("abandoned"), 0o600); err != nil {
		t.Fatalf("write orphan temp: %v", err)
	}
	stale := time.Now().Add(-2 * classpathAssetPartialGrace)
	if err := os.Chtimes(orphan, stale, stale); err != nil {
		t.Fatalf("backdate orphan: %v", err)
	}

	// Materializing (which sweeps first) must leave the in-flight temp alone.
	if _, issue := MaterializeClasspathAssets(
		[]string{"app.jar"},
		[]ClasspathAsset{{Entry: "app.jar", SHA256: digest, StorageRef: storageRef}},
		workDir,
	); issue != nil {
		t.Fatalf("materialize failed: %v", issue)
	}

	if _, err := os.Stat(inFlight); err != nil {
		t.Fatalf("sweep deleted a concurrent worker's in-flight temp: %v", err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("sweep did not reclaim the orphaned temp: stat err=%v", err)
	}
}

func assertNoPartials(t *testing.T, assetDir string) {
	t.Helper()
	entries, err := os.ReadDir(assetDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".partial-") {
			t.Fatalf("leftover temp file: %s", entry.Name())
		}
	}
}

func TestMaterializeClasspathAssetsRejectsBadDigest(t *testing.T) {
	workDir := t.TempDir()
	assets := []ClasspathAsset{{Entry: "app.jar", SHA256: "not-a-hash", StorageRef: "file:///x"}}
	_, issue := MaterializeClasspathAssets([]string{"app.jar"}, assets, workDir)
	if issue == nil || issue.Category != ClasspathAssetIntegrity {
		t.Fatalf("expected integrity issue for malformed digest, got %v", issue)
	}
}
