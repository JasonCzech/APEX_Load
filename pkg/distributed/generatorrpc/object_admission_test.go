package generatorrpc

import (
	"context"
	"fmt"
	"testing"

	pb "github.com/jasonczech/apexload/api/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestAssignWorkBoundsZeroVUserTestObjects(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	service := NewService("zero-vuser-object-cap", 10, WithGeneratorObjectLimits(2, 10, 10, 10))
	for i := 0; i < 2; i++ {
		ack, err := service.AssignWork(context.Background(), zeroVUserAssignment(fmt.Sprintf("test-%d", i), "group"))
		if err != nil || !ack.GetAccepted() {
			t.Fatalf("assignment %d = (%#v, %v)", i, ack, err)
		}
	}
	ack, err := service.AssignWork(context.Background(), zeroVUserAssignment("overflow", "group"))
	if ack != nil || status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("overflow assignment = (%#v, %v), want ResourceExhausted", ack, err)
	}
	service.mu.RLock()
	defer service.mu.RUnlock()
	if len(service.assignments) != 2 || len(service.executions) != 0 || service.archiveManager.writerCount() != 0 {
		t.Fatalf("state after rejection: assignments=%d executions=%d writers=%d", len(service.assignments), len(service.executions), service.archiveManager.writerCount())
	}
}

func TestAssignWorkBoundsZeroVUserGroups(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	service := NewService("zero-vuser-group-cap", 10, WithGeneratorObjectLimits(10, 2, 10, 10))
	for _, group := range []string{"first", "second"} {
		ack, err := service.AssignWork(context.Background(), zeroVUserAssignment("test", group))
		if err != nil || !ack.GetAccepted() {
			t.Fatalf("assignment %q = (%#v, %v)", group, ack, err)
		}
	}
	ack, err := service.AssignWork(context.Background(), zeroVUserAssignment("test", "overflow"))
	if ack != nil || status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("overflow assignment = (%#v, %v), want ResourceExhausted", ack, err)
	}
	if got := len(service.assignments["test"].groups); got != 2 {
		t.Fatalf("group count after rejection = %d, want 2", got)
	}
}

func TestAssignWorkChecksExecutionAndWriterLimitsBeforeMutation(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Run("execution", func(t *testing.T) {
		service := NewService("execution-object-cap", 10, WithExecution(), WithGeneratorObjectLimits(10, 10, 1, 10))
		service.executions["existing"] = map[string]*groupExecution{"group": nil}
		ack, err := service.AssignWork(context.Background(), zeroVUserAssignment("overflow", "group"))
		if ack != nil || status.Code(err) != codes.ResourceExhausted {
			t.Fatalf("execution overflow = (%#v, %v), want ResourceExhausted", ack, err)
		}
		cacheEntries, _ := service.scriptCache.snapshot()
		if len(service.assignments) != 0 || len(cacheEntries) != 0 {
			t.Fatalf("execution rejection mutated assignments/scripts: %d/%d", len(service.assignments), len(cacheEntries))
		}
	})
	t.Run("writer", func(t *testing.T) {
		service := NewService("writer-object-cap", 10, WithExecution(), WithGeneratorObjectLimits(10, 10, 10, 1))
		service.archiveManager.setWriterForTest("existing", nil)
		ack, err := service.AssignWork(context.Background(), zeroVUserAssignment("overflow", "group"))
		if ack != nil || status.Code(err) != codes.ResourceExhausted {
			t.Fatalf("writer overflow = (%#v, %v), want ResourceExhausted", ack, err)
		}
		cacheEntries, _ := service.scriptCache.snapshot()
		if len(service.assignments) != 0 || len(cacheEntries) != 0 {
			t.Fatalf("writer rejection mutated assignments/scripts: %d/%d", len(service.assignments), len(cacheEntries))
		}
	})
}

func zeroVUserAssignment(testID, group string) *pb.WorkAssignment {
	return &pb.WorkAssignment{
		TestId:        testID,
		GroupName:     group,
		ScriptId:      "zero-vuser-script",
		ScriptContent: []byte(`{"name":"zero-vuser","actions":[]}`),
		VuserCount:    0,
	}
}
