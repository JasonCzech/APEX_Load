package generatorrpc

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/protocol"
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

func TestGeneratorRehydratesCompletedArchivesAfterRestart(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	const generatorID = "archive-restart"
	writer, err := newArchiveRecorder(generatorID, "completed-run", "Completed")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Finalize("COMPLETE", time.Now().UTC(), ""); err != nil {
		t.Fatal(err)
	}

	service := NewService(generatorID, 10, WithGeneratorStateLimits(10, 10, 24*time.Hour))
	service.mu.RLock()
	rehydrated := service.archiveRecords["completed-run"]
	service.mu.RUnlock()
	if rehydrated == nil || rehydrated.CompletedAt.IsZero() {
		t.Fatalf("completed archive was not rehydrated: %+v", rehydrated)
	}
	ack, err := service.AssignWork(context.Background(), &pb.WorkAssignment{
		TestId: "completed-run", GroupName: "group", ScriptId: "script", ScriptContent: []byte(`{"name":"script"}`), VuserCount: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ack.GetAccepted() || !strings.Contains(ack.GetErrorMessage(), "retained completed archive") {
		t.Fatalf("reused archive assignment = %+v", ack)
	}
}

func TestGeneratorArchiveWriterEnforcesByteAndFreeSpaceBounds(t *testing.T) {
	t.Run("byte limit", func(t *testing.T) {
		t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
		t.Setenv(envGeneratorArchiveMaxBytes, "128")
		t.Setenv(envGeneratorArchiveMinFree, "1")
		writer, err := newArchiveRecorder("quota-byte", "run", "Quota")
		if err != nil {
			t.Fatal(err)
		}
		result := &protocol.Result{ActionName: strings.Repeat("x", 512), StartTime: time.Now()}
		if err := writer.RecordResult(result); err == nil || !strings.Contains(err.Error(), "byte limit") {
			t.Fatalf("RecordResult error = %v, want byte limit", err)
		}
		_ = writer.Discard()
	})
	t.Run("free space reserve", func(t *testing.T) {
		t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
		t.Setenv(envGeneratorArchiveMaxBytes, "1048576")
		t.Setenv(envGeneratorArchiveMinFree, "9223372036854775807")
		writer, err := newArchiveRecorder("quota-free", "run", "Quota")
		if err != nil {
			t.Fatal(err)
		}
		if err := writer.RecordResult(&protocol.Result{ActionName: "x", StartTime: time.Now()}); err == nil || !strings.Contains(err.Error(), "free space") {
			t.Fatalf("RecordResult error = %v, want free-space reserve", err)
		}
		_ = writer.Discard()
	})
}

func TestGeneratorArchiveFreeBudgetResamplesAndReleasesReservations(t *testing.T) {
	free := int64(20)
	budget := &generatorArchiveFreeBudget{freeBytes: func(string) (int64, error) { return free, nil }}
	if _, err := budget.reserve(15, 10); err == nil {
		t.Fatal("reservation unexpectedly passed with insufficient free space")
	}
	free = 100
	release, err := budget.reserve(60, 10)
	if err != nil {
		t.Fatalf("reservation did not resample recovered free space: %v", err)
	}
	if _, err := budget.reserve(35, 10); err == nil {
		t.Fatal("concurrent reservation ignored reserved bytes")
	}
	release()
	if releaseAgain, err := budget.reserve(35, 10); err != nil {
		t.Fatalf("released reservation was not credited: %v", err)
	} else {
		releaseAgain()
	}
}

func TestGeneratorArchiveFinalizeEnforcesParquetQuotaAndCleansFailure(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		configure func(*generatorArchiveByteBudget)
		wantError string
	}{
		{
			name: "byte limit during parquet output",
			configure: func(budget *generatorArchiveByteBudget) {
				budget.maxBytes = budget.written
			},
			wantError: "write transaction parquet: generator archive exceeds byte limit",
		},
		{
			name: "free-space reserve during parquet output",
			configure: func(budget *generatorArchiveByteBudget) {
				budget.minFreeBytes = int64(^uint64(0) >> 1)
				budget.freeKnown = false
				budget.nextFreeCheck = 0
			},
			wantError: "write transaction parquet: generator archive free space",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
			t.Setenv(envGeneratorArchiveMaxBytes, "1073741824")
			t.Setenv(envGeneratorArchiveMinFree, "1")
			writer, err := newArchiveRecorder("parquet-quota-"+strings.ReplaceAll(testCase.name, " ", "-"), "run", "Quota")
			if err != nil {
				t.Fatal(err)
			}
			if err := writer.RecordResult(&protocol.Result{ActionName: "action", StartTime: time.Now(), Success: true}); err != nil {
				t.Fatal(err)
			}
			writer.byteBudget.mu.Lock()
			testCase.configure(writer.byteBudget)
			writer.byteBudget.mu.Unlock()
			archivePath := writer.rootDir
			_, err = writer.Finalize("COMPLETE", time.Now(), "")
			if err == nil || !strings.Contains(err.Error(), testCase.wantError) {
				t.Fatalf("Finalize error = %v, want %q", err, testCase.wantError)
			}
			if _, statErr := os.Stat(archivePath); !os.IsNotExist(statErr) {
				t.Fatalf("failed parquet archive was not removed: %v", statErr)
			}
			if !writer.closed || writer.runRoot != nil {
				t.Fatalf("failed recorder remained open: closed=%v runRoot=%v", writer.closed, writer.runRoot)
			}
			if err := writer.Discard(); err != nil {
				t.Fatalf("idempotent Discard after failed Finalize: %v", err)
			}
		})
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

func TestFailedGeneratorArchiveFinalizationCleansWithoutDiskLeak(t *testing.T) {
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
	if _, err := os.Stat(archivePath); !os.IsNotExist(err) {
		t.Fatalf("failed archive directory remains: %v", err)
	}
	if err := writer.Discard(); err != nil {
		t.Fatalf("idempotent Discard after failed Finalize: %v", err)
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

func TestTerminalPathsReserveTestIDUntilArchiveFinalized(t *testing.T) {
	tests := []struct {
		name        string
		preReserved bool
		terminate   func(*Service, string)
	}{
		{name: "natural execution after graceful stop", preReserved: true, terminate: func(service *Service, testID string) { service.finishExecution(testID, "group", nil) }},
		{name: "failed execution", terminate: func(service *Service, testID string) { service.FailTest(testID, "failed") }},
		{name: "timer completion", terminate: func(service *Service, testID string) { service.completeAssignment(testID) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
			service := NewService("terminal-reservation-"+strings.ReplaceAll(tt.name, " ", "-"), 10)
			testID := "run-terminal"
			writer, err := newArchiveRecorderInNamespace(service.archiveNamespace, testID, tt.name)
			if err != nil {
				t.Fatal(err)
			}
			service.mu.Lock()
			service.assignments[testID] = assignedTest{
				testID: testID,
				groups: map[string]int32{"group": 1},
			}
			service.archiveWriters[testID] = writer
			if tt.preReserved {
				service.stoppingTests[testID] = struct{}{}
			}
			service.mu.Unlock()

			writer.mu.Lock()
			locked := true
			defer func() {
				if locked {
					writer.mu.Unlock()
				}
			}()
			done := make(chan struct{})
			go func() {
				tt.terminate(service, testID)
				close(done)
			}()
			waitForGeneratorTestReservation(t, service, testID)

			ack, err := service.AssignWork(context.Background(), &pb.WorkAssignment{
				TestId: testID, GroupName: "retry", ScriptId: "script", VuserCount: 1,
			})
			if err != nil {
				t.Fatal(err)
			}
			if ack.GetAccepted() || !strings.Contains(ack.GetErrorMessage(), "stopping") {
				t.Fatalf("same-ID retry during finalization = %#v", ack)
			}

			writer.mu.Unlock()
			locked = false
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("terminal finalization did not complete")
			}
			service.mu.Lock()
			_, stopping := service.stoppingTests[testID]
			service.mu.Unlock()
			if stopping {
				t.Fatal("test-ID reservation survived completed finalization")
			}
		})
	}
}

func TestDuplicateStopDoesNotClearFinalizerReservation(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	service := NewService("duplicate-stop-reservation", 10)
	const testID = "run-stop"
	writer, err := newArchiveRecorderInNamespace(service.archiveNamespace, testID, "Stop")
	if err != nil {
		t.Fatal(err)
	}
	service.mu.Lock()
	service.assignments[testID] = assignedTest{testID: testID, vuserCount: 1, groups: map[string]int32{"group": 1}}
	service.archiveWriters[testID] = writer
	service.mu.Unlock()

	writer.mu.Lock()
	locked := true
	defer func() {
		if locked {
			writer.mu.Unlock()
		}
	}()
	firstDone := make(chan error, 1)
	go func() {
		_, stopErr := service.StopTest(context.Background(), &pb.StopRequest{TestId: testID})
		firstDone <- stopErr
	}()
	waitForGeneratorTestReservation(t, service, testID)

	duplicateCtx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	second, err := service.StopTest(duplicateCtx, &pb.StopRequest{TestId: testID})
	if second != nil || status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("duplicate StopTest before finalization = (%#v, %v), want DeadlineExceeded", second, err)
	}
	service.mu.Lock()
	_, stillStopping := service.stoppingTests[testID]
	service.mu.Unlock()
	if !stillStopping {
		t.Fatal("duplicate StopTest cleared the first stop's finalizer reservation")
	}

	writer.mu.Unlock()
	locked = false
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("first StopTest: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("first StopTest did not complete")
	}
	second, err = service.StopTest(context.Background(), &pb.StopRequest{TestId: testID})
	if err != nil || !second.GetAcknowledged() {
		t.Fatalf("duplicate StopTest after finalization = (%#v, %v), want idempotent acknowledgement", second, err)
	}
}

func waitForGeneratorTestReservation(t *testing.T, service *Service, testID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		service.mu.Lock()
		_, assigned := service.assignments[testID]
		_, stopping := service.stoppingTests[testID]
		service.mu.Unlock()
		if !assigned && stopping {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("test %q was not reserved during finalization", testID)
}
