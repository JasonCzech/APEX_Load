package generatorrpc

import (
	"context"
	"fmt"
	"sync"
	"testing"

	pb "github.com/jasonczech/apexload/api/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestConfinedArchiveArtifactPath(t *testing.T) {
	base := "/var/lib/apexload/archives/test-1"
	cases := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"nested ok", "results/page-1.json", false},
		{"simple ok", "summary.json", false},
		{"parent traversal", "../../etc/passwd", true},
		{"absolute", "/etc/passwd", true},
		{"backslash", "results\\..\\..\\etc", true},
		{"nul byte", "results/page\x00.json", true},
		{"bare dotdot", "..", true},
		{"empty", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := confinedArchiveArtifactPath(base, tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got path %q", tc.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.input, err)
			}
			if got == "" {
				t.Fatalf("expected a confined path for %q", tc.input)
			}
		})
	}
}

func TestServiceAssignWorkRejectsNegativeVUserCount(t *testing.T) {
	service := NewService("generator-1", 50)
	_, err := service.AssignWork(context.Background(), &pb.WorkAssignment{
		TestId:     "test-1",
		GroupName:  "Group A",
		ScriptId:   "script-1",
		VuserCount: -5,
	})
	if err == nil {
		t.Fatal("expected error for negative vuser_count")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", status.Code(err))
	}
}

// TestServiceHeartbeatReadPathIsRaceFree exercises the Heartbeat read path
// (collectResources + the locked capacity reads) concurrently with the
// AssignWork/StopTest writers. Before the fix the heartbeat read s.assignments
// and the shared resCollector without synchronization. Run with -race.
func TestServiceHeartbeatReadPathIsRaceFree(t *testing.T) {
	service := NewService("generator-1", 1000)

	done := make(chan struct{})
	var writerWG sync.WaitGroup
	writerWG.Add(1)
	go func() {
		defer writerWG.Done()
		for i := 0; ; i++ {
			select {
			case <-done:
				return
			default:
			}
			id := fmt.Sprintf("t-%d", i%4)
			service.mu.Lock()
			service.assignments[id] = assignedTest{vuserCount: int32(i % 10)}
			delete(service.assignments, fmt.Sprintf("t-%d", (i+1)%4))
			service.mu.Unlock()
		}
	}()

	var readerWG sync.WaitGroup
	for r := 0; r < 4; r++ {
		readerWG.Add(1)
		go func() {
			defer readerWG.Done()
			for i := 0; i < 400; i++ {
				_ = service.collectResources()
				service.mu.RLock()
				_ = service.activeVUsersLocked()
				_ = service.availableCapacityLocked()
				service.mu.RUnlock()
				_ = service.activeTestIDs()
			}
		}()
	}

	readerWG.Wait()
	close(done)
	writerWG.Wait()
}
