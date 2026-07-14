package generatorrpc

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
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

func TestServiceAssignWorkIsIdempotentForSameGroup(t *testing.T) {
	service := NewService("generator-1", 10)
	if _, err := service.DistributeScript(context.Background(), &pb.ScriptPayload{ScriptId: "script-1", Content: []byte(`{"name":"demo"}`)}); err != nil {
		t.Fatalf("DistributeScript: %v", err)
	}
	assignment := &pb.WorkAssignment{TestId: "test-1", GroupName: "group-1", ScriptId: "script-1", VuserCount: 6}
	for attempt := 0; attempt < 2; attempt++ {
		ack, err := service.AssignWork(context.Background(), assignment)
		if err != nil {
			t.Fatalf("AssignWork attempt %d: %v", attempt+1, err)
		}
		if !ack.GetAccepted() {
			t.Fatalf("AssignWork attempt %d rejected: %s", attempt+1, ack.GetErrorMessage())
		}
	}
	if got := service.ActiveVUsers(); got != 6 {
		t.Fatalf("active VUsers after duplicate assignment = %d, want 6", got)
	}
	conflict := &pb.WorkAssignment{TestId: "test-1", GroupName: "group-1", ScriptId: "script-1", VuserCount: 6, RampUpSeconds: 30}
	ack, err := service.AssignWork(context.Background(), conflict)
	if err != nil {
		t.Fatalf("conflicting AssignWork returned transport error: %v", err)
	}
	if ack.GetAccepted() {
		t.Fatal("same-count assignment with different execution settings was accepted as an idempotent retry")
	}
}

func TestServiceConcurrentAdjustVUsersCannotExceedCapacity(t *testing.T) {
	service := NewService("generator-1", 10)
	if _, err := service.DistributeScript(context.Background(), &pb.ScriptPayload{ScriptId: "script-1", Content: []byte(`{"name":"demo"}`)}); err != nil {
		t.Fatalf("DistributeScript: %v", err)
	}
	for _, testID := range []string{"test-a", "test-b"} {
		ack, err := service.AssignWork(context.Background(), &pb.WorkAssignment{TestId: testID, GroupName: "group", ScriptId: "script-1", VuserCount: 2})
		if err != nil || !ack.GetAccepted() {
			t.Fatalf("AssignWork %s: ack=%#v err=%v", testID, ack, err)
		}
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	for _, testID := range []string{"test-a", "test-b"} {
		testID := testID
		go func() {
			<-start
			_, err := service.AdjustVUsers(context.Background(), &pb.VUserAdjustment{TestId: testID, GroupName: "group", TargetVusers: 7})
			errs <- err
		}()
	}
	close(start)
	var rejected int
	for range 2 {
		if err := <-errs; err != nil {
			if status.Code(err) != codes.ResourceExhausted {
				t.Fatalf("AdjustVUsers error = %v, want ResourceExhausted", err)
			}
			rejected++
		}
	}
	if rejected != 1 {
		t.Fatalf("rejected adjustments = %d, want 1", rejected)
	}
	if got := service.ActiveVUsers(); got > 10 {
		t.Fatalf("active VUsers = %d, exceeds max 10", got)
	}
}

func TestServiceSerializesSameGroupEngineAdjustments(t *testing.T) {
	mge, err := engine.NewMultiGroup(engine.MultiGroupConfig{
		Groups: []engine.GroupConfig{{
			Name: "group",
			Script: &dsl.Script{
				Name:     "waiting-engine",
				Protocol: "http",
				Config:   dsl.ScriptConfig{BaseURL: "http://127.0.0.1"},
				Actions:  []dsl.Action{{Name: "request", Type: "GET", Target: "/"}},
			},
			VUsers: 1,
		}},
		Duration: time.Minute,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	service := NewService("generator-1", 10, WithExecution())
	service.assignments["test"] = assignedTest{
		testID:       "test",
		vuserCount:   1,
		groups:       map[string]int32{"group": 1},
		activeGroups: map[string]int32{"group": 1},
	}
	service.executions["test"] = map[string]*groupExecution{"group": {groupName: "group", engine: mge}}

	ctxFirst, cancelFirst := context.WithCancel(context.Background())
	ctxSecond, cancelSecond := context.WithCancel(context.Background())
	defer cancelFirst()
	defer cancelSecond()
	firstDone := make(chan error, 1)
	secondDone := make(chan error, 1)
	go func() {
		_, err := service.AdjustVUsers(ctxFirst, &pb.VUserAdjustment{TestId: "test", GroupName: "group", TargetVusers: 3})
		firstDone <- err
	}()
	waitForAssignedVUsers(t, service, "test", "group", 3)
	go func() {
		_, err := service.AdjustVUsers(ctxSecond, &pb.VUserAdjustment{TestId: "test", GroupName: "group", TargetVusers: 4})
		secondDone <- err
	}()

	// The first request is blocked on engine readiness. The second must not be
	// allowed to reserve/scale the same group until the first sequence exits.
	time.Sleep(25 * time.Millisecond)
	service.mu.RLock()
	got := service.assignments["test"].groups["group"]
	service.mu.RUnlock()
	if got != 3 {
		t.Fatalf("second adjustment overtook first: reserved VUsers = %d, want 3", got)
	}

	cancelFirst()
	if err := <-firstDone; status.Code(err) != codes.Canceled {
		t.Fatalf("first adjustment error = %v, want Canceled", err)
	}
	waitForAssignedVUsers(t, service, "test", "group", 4)
	cancelSecond()
	if err := <-secondDone; status.Code(err) != codes.Canceled {
		t.Fatalf("second adjustment error = %v, want Canceled", err)
	}
}

func waitForAssignedVUsers(t *testing.T, service *Service, testID string, group string, want int32) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		service.mu.RLock()
		got := service.assignments[testID].groups[group]
		service.mu.RUnlock()
		if got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("group %s did not reserve %d VUsers", group, want)
}

func TestValidateArchiveTestIDRejectsTraversal(t *testing.T) {
	for _, testID := range []string{"../escape", "a/b", `a\b`, "..", "/absolute"} {
		if err := validateArchiveTestID(testID); err == nil {
			t.Errorf("validateArchiveTestID(%q) unexpectedly succeeded", testID)
		}
	}
	if err := validateArchiveTestID("run-1234"); err != nil {
		t.Fatalf("safe test id rejected: %v", err)
	}
}

func TestServiceExecutionFailureCancelsSiblingsAndRetainsCapacityUntilTheyExit(t *testing.T) {
	service := NewService("generator-1", 10, WithExecution())
	siblingCanceled := make(chan struct{})
	var cancelOnce sync.Once
	service.assignments["test-1"] = assignedTest{
		testID:           "test-1",
		vuserCount:       10,
		groups:           map[string]int32{"failing": 4, "sibling": 6},
		activeGroups:     map[string]int32{"failing": 4, "sibling": 6},
		groupAssignments: make(map[string][32]byte),
	}
	service.executions["test-1"] = map[string]*groupExecution{
		"failing": {groupName: "failing"},
		"sibling": {
			groupName: "sibling",
			cancel: func() {
				cancelOnce.Do(func() { close(siblingCanceled) })
			},
		},
	}

	service.finishExecution("test-1", "failing", fmt.Errorf("engine failed"))

	select {
	case <-siblingCanceled:
	case <-time.After(time.Second):
		t.Fatal("sibling execution was not canceled after group failure")
	}
	if got := service.ActiveVUsers(); got != 10 {
		t.Fatalf("active VUsers while sibling unwinds = %d, want 10", got)
	}
	service.mu.RLock()
	assignment, assignmentExists := service.assignments["test-1"]
	_, siblingExists := service.executions["test-1"]["sibling"]
	service.mu.RUnlock()
	if !assignmentExists || !siblingExists {
		t.Fatal("assignment was released before all sibling engines exited")
	}
	if assignment.terminalError != "engine failed" {
		t.Fatalf("terminal error = %q, want first execution error", assignment.terminalError)
	}

	service.finishExecution("test-1", "sibling", context.Canceled)
	if got := service.ActiveVUsers(); got != 0 {
		t.Fatalf("active VUsers after all executions exit = %d, want 0", got)
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
