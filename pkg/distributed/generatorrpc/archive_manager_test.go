package generatorrpc

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestArchiveManagerOwnsWriterAdmission(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	manager := newArchiveManager("archive-manager-writers", 10, time.Hour, 1, 2, 1)

	first, created, err := manager.ensureWriter("run-1", "Run one")
	if err != nil || first == nil || !created {
		t.Fatalf("ensure first writer = (%v, %v, %v), want non-nil/true/nil", first, created, err)
	}
	same, created, err := manager.ensureWriter("run-1", "Run one")
	if err != nil || same != first || created {
		t.Fatalf("ensure existing writer = (%v, %v, %v), want same/false/nil", same, created, err)
	}
	if _, _, err := manager.ensureWriter("run-2", "Run two"); err == nil || !strings.Contains(err.Error(), "limit exceeded (1)") {
		t.Fatalf("ensure over limit error = %v", err)
	}
	if got := manager.takeWriter("run-1"); got != first {
		t.Fatalf("take writer = %p, want %p", got, first)
	}
	if manager.writerCount() != 0 {
		t.Fatalf("writer count after take = %d, want 0", manager.writerCount())
	}
	if err := first.Discard(); err != nil {
		t.Fatalf("discard writer: %v", err)
	}
}

func TestArchiveManagerReturnsImmutableCatalogSnapshots(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	manager := newArchiveManager("archive-manager-snapshots", 10, time.Hour, 1, 2, 1)
	completedAt := time.Now().UTC()
	manager.restoreRecord(&storage.RunArchiveRecord{TestID: "run", ManifestSHA256: "original", CompletedAt: completedAt}, nil)

	snapshot := manager.record("run")
	if snapshot == nil {
		t.Fatal("missing catalog snapshot")
	}
	snapshot.ManifestSHA256 = "mutated"
	snapshot.CompletedAt = time.Time{}
	retained := manager.record("run")
	if retained == nil || retained.ManifestSHA256 != "original" || !retained.CompletedAt.Equal(completedAt) {
		t.Fatalf("manager catalog was mutated through snapshot: %+v", retained)
	}
}

func TestArchiveManagerShutdownWaitsForFinalizers(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	manager := newArchiveManager("archive-manager-shutdown", 10, time.Hour, 1, 2, 1)
	if !manager.addFinalizer() {
		t.Fatal("initial finalizer was rejected")
	}
	manager.beginShutdown()
	if manager.addFinalizer() {
		t.Fatal("finalizer admitted after shutdown began")
	}
	done := manager.finalizersDone()
	select {
	case <-done:
		t.Fatal("shutdown completed before active finalizer")
	default:
	}
	manager.finalizerDone()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not observe completed finalizer")
	}
}

func TestArchiveManagerTransferAdmissionAndIdempotentRelease(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	manager := newArchiveManager("archive-manager-transfers", 10, time.Hour, 1, 2, 1)
	releaseUnary, err := manager.beginTransfer(true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.beginTransfer(true); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("second unary transfer error = %v, want ResourceExhausted", err)
	}
	releaseStream, err := manager.beginTransfer(false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.beginTransfer(false); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("third aggregate transfer error = %v, want ResourceExhausted", err)
	}
	releaseUnary()
	releaseUnary()
	releaseStream()
	if release, err := manager.beginTransfer(true); err != nil {
		t.Fatalf("transfer after release: %v", err)
	} else {
		release()
	}
}

func TestArchiveManagerConcurrentCatalogAndTransferAccess(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	manager := newArchiveManager("archive-manager-race", 64, time.Hour, 8, 64, 32)
	const workers = 16
	const iterations = 100
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			testID := "run-" + string(rune('a'+worker))
			for iteration := 0; iteration < iterations; iteration++ {
				manager.restoreRecord(&storage.RunArchiveRecord{TestID: testID, ManifestSHA256: "retained", CompletedAt: time.Now()}, nil)
				_ = manager.record(testID)
				if lease, ok := manager.acquireCatalogLease(testID); ok {
					_ = lease
					for _, removal := range manager.releaseCatalogLease(testID, time.Now()) {
						manager.completeRemoval(removal, true)
					}
				}
				release, err := manager.beginTransfer(iteration%2 == 0)
				if err == nil {
					release()
				}
			}
		}()
	}
	wg.Wait()

	if manager.recordCount() != workers {
		t.Fatalf("retained record count = %d, want %d", manager.recordCount(), workers)
	}
}
