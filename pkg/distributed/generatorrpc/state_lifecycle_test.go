package generatorrpc

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/storage"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestGeneratorScriptCacheIsLRUBoundedAndPinsActiveScripts(t *testing.T) {
	service := NewService("cache-lifecycle", 20, WithGeneratorStateLimits(2, 10, 0))
	ctx := context.Background()
	put := func(id string) error {
		_, err := service.DistributeScript(ctx, &pb.ScriptPayload{ScriptId: id, Content: []byte(`{"name":"` + id + `"}`)})
		return err
	}
	if err := put("script-1"); err != nil {
		t.Fatal(err)
	}
	if err := put("script-2"); err != nil {
		t.Fatal(err)
	}
	if ack, err := service.AssignWork(ctx, &pb.WorkAssignment{TestId: "run-1", GroupName: "group-1", ScriptId: "script-1", VuserCount: 1}); err != nil || !ack.GetAccepted() {
		t.Fatalf("assign pinned script: ack=%+v err=%v", ack, err)
	}
	if err := put("script-3"); err != nil {
		t.Fatal(err)
	}
	service.mu.RLock()
	_, hasPinned := service.scripts["script-1"]
	_, hasEvicted := service.scripts["script-2"]
	_, hasNewest := service.scripts["script-3"]
	cacheSize := len(service.scripts)
	service.mu.RUnlock()
	if !hasPinned || hasEvicted || !hasNewest || cacheSize != 2 {
		t.Fatalf("cache after eviction: pinned=%v evicted=%v newest=%v size=%d", hasPinned, hasEvicted, hasNewest, cacheSize)
	}

	if ack, err := service.AssignWork(ctx, &pb.WorkAssignment{TestId: "run-2", GroupName: "group-2", ScriptId: "script-3", VuserCount: 1}); err != nil || !ack.GetAccepted() {
		t.Fatalf("assign second pinned script: ack=%+v err=%v", ack, err)
	}
	if err := put("script-4"); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("full pinned cache error = %v, want ResourceExhausted", err)
	}
	service.mu.RLock()
	cacheSize = len(service.scripts)
	service.mu.RUnlock()
	if cacheSize != 2 {
		t.Fatalf("pinned cache grew past cap: %d", cacheSize)
	}
}

func TestGeneratorScriptCacheIsByteBounded(t *testing.T) {
	service := NewService("cache-byte-lifecycle", 20,
		WithGeneratorStateLimits(10, 10, 0),
		WithGeneratorScriptCacheByteLimit(6),
	)
	ctx := context.Background()
	if _, err := service.DistributeScript(ctx, &pb.ScriptPayload{ScriptId: "first", Content: []byte("1234")}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.DistributeScript(ctx, &pb.ScriptPayload{ScriptId: "second", Content: []byte("abc")}); err != nil {
		t.Fatal(err)
	}
	service.mu.RLock()
	_, firstRetained := service.scripts["first"]
	_, secondRetained := service.scripts["second"]
	bytesRetained := service.scriptCacheBytes
	service.mu.RUnlock()
	if firstRetained || !secondRetained || bytesRetained != 3 {
		t.Fatalf("byte-bounded cache: first=%v second=%v bytes=%d", firstRetained, secondRetained, bytesRetained)
	}
	if _, err := service.DistributeScript(ctx, &pb.ScriptPayload{ScriptId: "oversized", Content: []byte("1234567")}); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("oversized script error = %v, want ResourceExhausted", err)
	}
}

func TestAssignWorkRejectsInlineScriptWhenPinnedCacheCannotStoreMissingID(t *testing.T) {
	service := NewService("inline-missing-cache", 20, WithGeneratorStateLimits(1, 10, 0))
	ctx := context.Background()
	if _, err := service.DistributeScript(ctx, &pb.ScriptPayload{ScriptId: "pinned", Content: []byte("pinned")}); err != nil {
		t.Fatal(err)
	}
	if ack, err := service.AssignWork(ctx, &pb.WorkAssignment{TestId: "run-pinned", GroupName: "group", ScriptId: "pinned", VuserCount: 1}); err != nil || !ack.GetAccepted() {
		t.Fatalf("pin script assignment: ack=%+v err=%v", ack, err)
	}

	ack, err := service.AssignWork(ctx, &pb.WorkAssignment{
		TestId: "run-missing", GroupName: "group", ScriptId: "inline-missing", ScriptContent: []byte("inline"), VuserCount: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ack.GetAccepted() || ack.GetErrorMessage() == "" {
		t.Fatalf("inline missing-ID assignment ack = %+v, want cache rejection", ack)
	}
	service.mu.RLock()
	_, cached := service.scripts["inline-missing"]
	_, assigned := service.assignments["run-missing"]
	service.mu.RUnlock()
	if cached || assigned {
		t.Fatalf("rejected inline script leaked state: cached=%v assigned=%v", cached, assigned)
	}
}

func TestAssignWorkRejectsInlineReplacementWhenPinnedCacheWouldLeaveStaleID(t *testing.T) {
	service := NewService("inline-stale-cache", 20,
		WithGeneratorStateLimits(2, 10, 0),
		WithGeneratorScriptCacheByteLimit(6),
	)
	ctx := context.Background()
	for id, content := range map[string]string{"shared": "old", "blocker": "xx"} {
		if _, err := service.DistributeScript(ctx, &pb.ScriptPayload{ScriptId: id, Content: []byte(content)}); err != nil {
			t.Fatal(err)
		}
		if ack, err := service.AssignWork(ctx, &pb.WorkAssignment{TestId: "run-" + id, GroupName: "group", ScriptId: id, VuserCount: 1}); err != nil || !ack.GetAccepted() {
			t.Fatalf("pin %s: ack=%+v err=%v", id, ack, err)
		}
	}

	ack, err := service.AssignWork(ctx, &pb.WorkAssignment{
		TestId: "run-replacement", GroupName: "group", ScriptId: "shared", ScriptContent: []byte("newnew"), VuserCount: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ack.GetAccepted() || ack.GetErrorMessage() == "" {
		t.Fatalf("inline stale-ID replacement ack = %+v, want cache rejection", ack)
	}
	service.mu.RLock()
	cached := service.scripts["shared"]
	_, assigned := service.assignments["run-replacement"]
	service.mu.RUnlock()
	if string(cached.content) != "old" || assigned {
		t.Fatalf("rejected replacement state: cached=%q assigned=%v", cached.content, assigned)
	}
}

func TestGeneratorArchiveRecordsAndDirectoriesAreCapped(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	service := NewService("archive-cap", 10, WithGeneratorStateLimits(10, 2, 0))
	base := time.Now().Add(-time.Hour)
	for index, testID := range []string{"run-1", "run-2", "run-3"} {
		path := filepath.Join(service.generatorArchiveRoot(), testID)
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		service.storeArchiveRecord(&storage.RunArchiveRecord{
			TestID: testID, ArchivePath: path, ManifestPath: filepath.Join(path, "manifest.json"),
			CompletedAt: base.Add(time.Duration(index) * time.Minute),
		})
	}
	service.mu.RLock()
	_, oldestRetained := service.archiveRecords["run-1"]
	recordCount := len(service.archiveRecords)
	service.mu.RUnlock()
	if oldestRetained || recordCount != 2 {
		t.Fatalf("archive records: oldest retained=%v count=%d", oldestRetained, recordCount)
	}
	if _, err := os.Stat(filepath.Join(service.generatorArchiveRoot(), "run-1")); !os.IsNotExist(err) {
		t.Fatalf("oldest archive directory was not removed: %v", err)
	}
	for _, testID := range []string{"run-2", "run-3"} {
		if _, err := os.Stat(filepath.Join(service.generatorArchiveRoot(), testID)); err != nil {
			t.Fatalf("retained archive %s missing: %v", testID, err)
		}
	}
}

func TestGeneratorArchiveTTLAndStartupOrphanSweep(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	generatorID := "archive-ttl"
	archiveRoot := filepath.Join(root, generatorID)
	oldPath := filepath.Join(archiveRoot, "orphan-old")
	newPath := filepath.Join(archiveRoot, "orphan-new")
	for _, path := range []string{oldPath, newPath} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	if err := os.Chtimes(oldPath, now.Add(-3*time.Hour), now.Add(-3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newPath, now, now); err != nil {
		t.Fatal(err)
	}
	service := NewService(generatorID, 10, WithGeneratorStateLimits(10, 10, time.Hour))
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("expired orphan archive was not swept: %v", err)
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("fresh orphan archive was removed: %v", err)
	}

	expiredPath := filepath.Join(service.generatorArchiveRoot(), "record-expired")
	if err := os.MkdirAll(expiredPath, 0o755); err != nil {
		t.Fatal(err)
	}
	service.storeArchiveRecord(&storage.RunArchiveRecord{TestID: "record-expired", ArchivePath: expiredPath, CompletedAt: now.Add(-2 * time.Hour)})
	service.mu.RLock()
	_, retained := service.archiveRecords["record-expired"]
	service.mu.RUnlock()
	if retained {
		t.Fatal("expired archive record remained in memory")
	}
	if _, err := os.Stat(expiredPath); !os.IsNotExist(err) {
		t.Fatalf("expired archive record directory was not removed: %v", err)
	}
}

func TestGeneratorArchiveLeaseDefersCapCleanupUntilReadCompletes(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	service := NewService("archive-lease", 10, WithGeneratorStateLimits(10, 1, 0))
	base := time.Now().Add(-time.Hour)
	store := func(testID string, completedAt time.Time) string {
		path := filepath.Join(service.generatorArchiveRoot(), testID)
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		manifestPath := filepath.Join(path, "manifest.json")
		if err := os.WriteFile(manifestPath, []byte(testID), 0o644); err != nil {
			t.Fatal(err)
		}
		service.storeArchiveRecord(&storage.RunArchiveRecord{TestID: testID, ArchivePath: path, ManifestPath: manifestPath, CompletedAt: completedAt})
		return path
	}
	oldPath := store("run-old", base)
	_, release, err := service.acquireArchiveArtifact(&pb.ArchiveArtifactRequest{TestId: "run-old", Name: "manifest.json"})
	if err != nil {
		t.Fatal(err)
	}
	newPath := store("run-new", base.Add(time.Minute))
	service.mu.RLock()
	duringLeaseCount := len(service.archiveRecords)
	service.mu.RUnlock()
	if duringLeaseCount != 2 {
		t.Fatalf("leased temporary overage count = %d, want 2", duringLeaseCount)
	}
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatalf("leased archive removed during read: %v", err)
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("newest archive sacrificed to leased record: %v", err)
	}

	release()
	service.mu.RLock()
	_, oldRetained := service.archiveRecords["run-old"]
	_, newRetained := service.archiveRecords["run-new"]
	afterLeaseCount := len(service.archiveRecords)
	service.mu.RUnlock()
	if oldRetained || !newRetained || afterLeaseCount != 1 {
		t.Fatalf("post-lease records: old=%v new=%v count=%d", oldRetained, newRetained, afterLeaseCount)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("released over-cap archive directory remains: %v", err)
	}
}

func TestGeneratorRejectsCompletedTestIDReuseWhileArchiveIsRetained(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	service := NewService("archive-reuse", 10, WithGeneratorStateLimits(10, 10, 0))
	archivePath := filepath.Join(service.generatorArchiveRoot(), "completed-run")
	if err := os.MkdirAll(archivePath, 0o755); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(archivePath, "manifest.json")
	if err := os.WriteFile(manifestPath, []byte("retained"), 0o644); err != nil {
		t.Fatal(err)
	}
	service.storeArchiveRecord(&storage.RunArchiveRecord{
		TestID: "completed-run", ArchivePath: archivePath, ManifestPath: manifestPath, CompletedAt: time.Now(),
	})
	_, release, err := service.acquireArchiveArtifact(&pb.ArchiveArtifactRequest{TestId: "completed-run", Name: "manifest.json"})
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ack, err := service.AssignWork(context.Background(), &pb.WorkAssignment{
		TestId: "completed-run", GroupName: "group", ScriptId: "new-script", ScriptContent: []byte(`{"name":"new"}`), VuserCount: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ack.GetAccepted() || ack.GetErrorMessage() == "" {
		t.Fatalf("completed test ID reuse ack = %+v, want rejected", ack)
	}
	got, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "retained" {
		t.Fatalf("retained archive was modified: %q", got)
	}
}

func TestGeneratorArchiveCleanupRefusesPathOutsideNamespace(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := removeConfinedGeneratorArchiveDirectory(root, outside); err == nil {
		t.Fatal("expected outside archive cleanup to be rejected")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside path was removed: %v", err)
	}
}

func TestFailedGeneratorArchiveCanBeDiscardedWithoutDiskLeak(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	writer, err := newArchiveRecorder("discard-generator", "discard-run", "Discard")
	if err != nil {
		t.Fatal(err)
	}
	archivePath := writer.rootDir
	if err := writer.txFile.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Finalize("COMPLETE", time.Now(), ""); err == nil {
		t.Fatal("expected finalization with a closed transaction file to fail")
	}
	if err := writer.Discard(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(archivePath); !os.IsNotExist(err) {
		t.Fatalf("failed archive directory remains: %v", err)
	}
}

func TestGeneratorArchiveNamespaceCannotEscapeRoot(t *testing.T) {
	for _, unsafe := range []string{".", "..", "\x00"} {
		if got := sanitizeArchiveNamespace(unsafe); got == "." || got == ".." || got == "" || got == unsafe {
			t.Fatalf("sanitizeArchiveNamespace(%q) = %q", unsafe, got)
		}
	}
}

func TestGeneratorArchiveReadRejectsRegisteredRunReplacement(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	service := NewService("archive-replaced-read", 10, WithGeneratorStateLimits(10, 10, 0))
	runPath := filepath.Join(service.generatorArchiveRoot(), "run-replaced")
	if err := os.Mkdir(runPath, 0o700); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(runPath, "manifest.json")
	if err := os.WriteFile(manifestPath, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	service.storeArchiveRecord(&storage.RunArchiveRecord{TestID: "run-replaced", ArchivePath: runPath, ManifestPath: manifestPath})

	originalPath := runPath + ".original"
	if err := os.Rename(runPath, originalPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(runPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, release, err := service.acquireArchiveArtifact(&pb.ArchiveArtifactRequest{TestId: "run-replaced", Name: "manifest.json"})
	if release != nil {
		release()
	}
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("replaced archive read error = %v, want FailedPrecondition", err)
	}
}

func TestGeneratorArchiveRetentionDoesNotDeleteReplacement(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	service := NewService("archive-replaced-retention", 10, WithGeneratorStateLimits(10, 1, 0))
	base := time.Now().Add(-time.Hour)
	oldPath := filepath.Join(service.generatorArchiveRoot(), "run-old")
	if err := os.Mkdir(oldPath, 0o700); err != nil {
		t.Fatal(err)
	}
	oldManifest := filepath.Join(oldPath, "manifest.json")
	if err := os.WriteFile(oldManifest, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	service.storeArchiveRecord(&storage.RunArchiveRecord{TestID: "run-old", ArchivePath: oldPath, ManifestPath: oldManifest, CompletedAt: base})

	originalPath := oldPath + ".original"
	if err := os.Rename(oldPath, originalPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(oldPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldManifest, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	newPath := filepath.Join(service.generatorArchiveRoot(), "run-new")
	if err := os.Mkdir(newPath, 0o700); err != nil {
		t.Fatal(err)
	}
	service.storeArchiveRecord(&storage.RunArchiveRecord{TestID: "run-new", ArchivePath: newPath, CompletedAt: base.Add(time.Minute)})

	got, err := os.ReadFile(oldManifest)
	if err != nil {
		t.Fatalf("replacement archive was deleted: %v", err)
	}
	if string(got) != "replacement" {
		t.Fatalf("replacement archive changed: %q", got)
	}
	if _, err := os.Stat(originalPath); err != nil {
		t.Fatalf("detached original archive was unexpectedly deleted: %v", err)
	}
}

func TestGeneratorArchiveReadRejectsIntermediateSymlink(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	service := NewService("archive-nested-link", 10, WithGeneratorStateLimits(10, 10, 0))
	runPath := filepath.Join(service.generatorArchiveRoot(), "run-link")
	if err := os.Mkdir(runPath, 0o700); err != nil {
		t.Fatal(err)
	}
	service.storeArchiveRecord(&storage.RunArchiveRecord{TestID: "run-link", ArchivePath: runPath})
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(runPath, "nested")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	_, release, err := service.acquireArchiveArtifact(&pb.ArchiveArtifactRequest{TestId: "run-link", Name: "nested/secret.txt"})
	if release != nil {
		release()
	}
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("intermediate symlink read error = %v, want FailedPrecondition", err)
	}
}

func TestGeneratorArchiveLeaseReadsOpenedInodeAcrossReplacement(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	service := NewService("archive-open-inode", 10, WithGeneratorStateLimits(10, 10, 0))
	runPath := filepath.Join(service.generatorArchiveRoot(), "run-open")
	if err := os.Mkdir(runPath, 0o700); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(runPath, "manifest.json")
	if err := os.WriteFile(manifestPath, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	service.storeArchiveRecord(&storage.RunArchiveRecord{TestID: "run-open", ArchivePath: runPath, ManifestPath: manifestPath})
	artifact, release, err := service.acquireArchiveArtifact(&pb.ArchiveArtifactRequest{TestId: "run-open", Name: "manifest.json"})
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := os.Rename(runPath, runPath+".original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(runPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(artifact.file)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "original" {
		t.Fatalf("leased descriptor read %q, want original", got)
	}
}

func TestGeneratorArchiveFinalizeRejectsRunDirectoryReplacement(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	writer, err := newArchiveRecorder("archive-finalize-swap", "run-finalize", "Finalize")
	if err != nil {
		t.Fatal(err)
	}
	detached := writer.rootDir + ".original"
	if err := os.Rename(writer.rootDir, detached); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(writer.rootDir, 0o700); err != nil {
		t.Fatal(err)
	}
	replacementManifest := filepath.Join(writer.rootDir, "manifest.json")
	if err := os.WriteFile(replacementManifest, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Finalize("COMPLETE", time.Now(), ""); err == nil {
		t.Fatal("finalization accepted a replaced run directory")
	}
	_ = writer.Discard() // Replacement-safe cleanup intentionally refuses this path.
	got, err := os.ReadFile(replacementManifest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "replacement" {
		t.Fatalf("replacement manifest changed: %q", got)
	}
}
