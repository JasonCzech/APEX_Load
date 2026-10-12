package generatorrpc

import (
	"context"
	"testing"

	pb "github.com/jasonczech/apexload/api/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestArchiveRequestedWhileTheTestStopsSaysItIsStillBeingFinalized(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	service := NewService("generator-finalizing", 10)
	request := &pb.ArchiveArtifactRequest{TestId: "stopping-run", Name: "manifest.json"}

	if _, err := service.GetArchiveArtifact(context.Background(), request); status.Code(err) != codes.NotFound || IsArchiveFinalizing(err) {
		t.Fatalf("archive of an unknown test = %v, want not found", err)
	}
	// A test still running has no archive until something stops it.
	service.mu.Lock()
	service.assignments["stopping-run"] = assignedTest{}
	service.mu.Unlock()
	if _, err := service.GetArchiveArtifact(context.Background(), request); status.Code(err) != codes.NotFound {
		t.Fatalf("archive of a running test = %v, want not found", err)
	}

	// A test whose stop has begun writes its archive before the stop completes.
	service.mu.Lock()
	service.lifecycleCoordinator.beginStopping("stopping-run", 1, assignedTest{})
	service.mu.Unlock()
	_, err := service.GetArchiveArtifact(context.Background(), request)
	if !IsArchiveFinalizing(err) || status.Code(err) != codes.Unavailable {
		t.Fatalf("archive of a stopping test = %v, want %q", err, ArchiveFinalizingMessage)
	}
	_, release, streamErr := service.acquireArchiveArtifact(request)
	if release != nil {
		release()
	}
	if !IsArchiveFinalizing(streamErr) {
		t.Fatalf("streamed archive of a stopping test = %v, want %q", streamErr, ArchiveFinalizingMessage)
	}

	service.completeStoppingTest("stopping-run", 1)
	if _, err := service.GetArchiveArtifact(context.Background(), request); status.Code(err) != codes.NotFound {
		t.Fatalf("archive after the stop finished without one = %v, want not found", err)
	}
}

func TestIsArchiveFinalizingNeedsTheExactSignal(t *testing.T) {
	for _, err := range []error{
		nil,
		status.Error(codes.Unavailable, "connection refused"),
		status.Error(codes.NotFound, ArchiveFinalizingMessage),
	} {
		if IsArchiveFinalizing(err) {
			t.Fatalf("IsArchiveFinalizing(%v) = true", err)
		}
	}
}
