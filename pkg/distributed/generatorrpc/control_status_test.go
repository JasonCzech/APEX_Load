package generatorrpc

import (
	"context"
	"errors"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/engine"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
)

type adjustErrorRPCClient struct {
	pb.GeneratorControlClient
	err error
}

func (client adjustErrorRPCClient) AdjustVUsers(context.Context, *pb.VUserAdjustment, ...grpc.CallOption) (*pb.AdjustmentAck, error) {
	return nil, client.err
}

func TestClientTranslatesOnlyStructuredInitialRampStatus(t *testing.T) {
	structured := initialRampInProgressStatusError()
	state, ok := status.FromError(structured)
	if !ok || state.Code() != codes.FailedPrecondition {
		t.Fatalf("structured status = (%v, %v), want FailedPrecondition", state, ok)
	}
	foundReason := false
	for _, detail := range state.Details() {
		if info, ok := detail.(*errdetails.ErrorInfo); ok && info.GetReason() == InitialRampInProgressReason {
			foundReason = true
		}
	}
	if !foundReason {
		t.Fatalf("structured status details = %#v, want reason %q", state.Details(), InitialRampInProgressReason)
	}

	tests := []struct {
		name       string
		wireErr    error
		wantRamp   bool
		wantStatus codes.Code
	}{
		{name: "structured reason", wireErr: structured, wantRamp: true, wantStatus: codes.FailedPrecondition},
		{name: "same message without reason", wireErr: status.Error(codes.FailedPrecondition, engine.ErrInitialRampInProgress.Error()), wantStatus: codes.FailedPrecondition},
		{name: "unrelated transport failure", wireErr: status.Error(codes.Unavailable, "offline"), wantStatus: codes.Unavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &Client{rpc: adjustErrorRPCClient{err: test.wireErr}}
			_, err := client.AdjustVUsers(context.Background(), VUserAdjustment{TestID: "run", GroupName: "group", TargetVUsers: 2})
			if got := errors.Is(err, engine.ErrInitialRampInProgress); got != test.wantRamp {
				t.Fatalf("errors.Is(initial ramp) = %v, want %v: %v", got, test.wantRamp, err)
			}
			if got := status.Code(err); got != test.wantStatus {
				t.Fatalf("status code = %s, want %s: %v", got, test.wantStatus, err)
			}
		})
	}
}

func TestAdjustVUsersRejectsInitialRampWithStructuredStatusAndRollsBackReservation(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")
	service := newIsolatedService(t, 10, WithExecution())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := service.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})

	assignment := &pb.WorkAssignment{
		TestId:         "initial-ramp-control",
		GroupName:      "group",
		ScriptId:       "initial-ramp-script",
		ScriptContent:  []byte(`{"name":"initial ramp","protocol":"http","config":{"base_url":"http://127.0.0.1:1","run_logic":{"iterations":1000000}},"actions":[{"name":"request","type":"GET","target":"/"}]}`),
		VuserCount:     3,
		RampUp:         durationpb.New(time.Minute),
		TargetDuration: durationpb.New(time.Minute),
	}
	ack, err := service.AssignWork(context.Background(), assignment)
	if err != nil || !ack.GetAccepted() {
		t.Fatalf("AssignWork = (%#v, %v)", ack, err)
	}
	commit, err := service.CommitWork(context.Background(), &pb.WorkCommitRequest{TestId: assignment.GetTestId()})
	if err != nil || !commit.GetAccepted() {
		t.Fatalf("CommitWork = (%#v, %v)", commit, err)
	}

	service.mu.RLock()
	execution := service.executions[assignment.GetTestId()][assignment.GetGroupName()]
	service.mu.RUnlock()
	if execution == nil || execution.engine == nil {
		t.Fatal("committed execution engine is missing")
	}
	select {
	case <-execution.engine.Ready():
	case <-time.After(2 * time.Second):
		t.Fatal("execution engine did not reach its public readiness boundary")
	}
	if execution.engine.ScalingReady() {
		t.Fatal("engine unexpectedly completed its one-minute initial ramp")
	}

	adjustment, err := service.AdjustVUsers(context.Background(), &pb.VUserAdjustment{
		TestId: assignment.GetTestId(), GroupName: assignment.GetGroupName(), TargetVusers: 4,
	})
	if adjustment != nil || status.Code(err) != codes.FailedPrecondition || !isInitialRampInProgressStatus(err) {
		t.Fatalf("AdjustVUsers = (%#v, %v [%s]), want structured initial-ramp FailedPrecondition", adjustment, err, status.Code(err))
	}

	service.mu.RLock()
	stored := service.assignments[assignment.GetTestId()]
	pending := service.lifecycleCoordinator.pendingControls[assignment.GetTestId()]
	service.mu.RUnlock()
	if stored.groups[assignment.GetGroupName()] != 3 || stored.vuserCount != 3 {
		t.Fatalf("rolled-back assignment = group:%d total:%d, want 3/3", stored.groups[assignment.GetGroupName()], stored.vuserCount)
	}
	if pending != nil {
		t.Fatal("initial-ramp rejection leaked a pending scale control")
	}
}
