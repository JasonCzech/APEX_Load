package generatorrpc

import (
	"context"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	pb "github.com/jasonczech/stormrunner/api/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

func TestServiceAssignWorkRejectsMissingCachedScript(t *testing.T) {
	service := NewService("generator-1", 50)

	ack, err := service.AssignWork(context.Background(), &pb.WorkAssignment{
		TestId:     "test-1",
		ScriptId:   "script-1",
		VuserCount: 5,
	})
	if err != nil {
		t.Fatalf("AssignWork returned error: %v", err)
	}
	if ack.GetAccepted() {
		t.Fatal("expected assignment to be rejected when no script is cached")
	}
	if !strings.Contains(ack.GetErrorMessage(), "not cached") {
		t.Fatalf("expected missing script error, got %q", ack.GetErrorMessage())
	}
}

func TestClientRoundTripDistributeAssignAndHeartbeat(t *testing.T) {
	const bufSize = 1024 * 1024

	listener := bufconn.Listen(bufSize)
	server := grpc.NewServer()
	service := NewService("generator-1", 50)
	pb.RegisterGeneratorControlServer(server, service)

	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(
		ctx,
		"bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}

	client := NewClient(conn)
	t.Cleanup(func() {
		_ = client.Close()
	})

	scriptAck, err := client.DistributeScript(ctx, Script{
		ID:          "script-1",
		Content:     []byte(`{"name":"demo"}`),
		ContentHash: "hash-1",
	})
	if err != nil {
		t.Fatalf("DistributeScript: %v", err)
	}
	if !scriptAck.Cached {
		t.Fatal("expected distributed script to be cached")
	}

	assignmentAck, err := client.AssignWork(ctx, Assignment{
		TestID:     "test-1",
		ScriptID:   "script-1",
		VUserCount: 10,
		Environment: map[string]string{
			"TARGET_HOST": "https://example.test",
		},
	})
	if err != nil {
		t.Fatalf("AssignWork: %v", err)
	}
	if !assignmentAck.Accepted {
		t.Fatalf("expected assignment acceptance, got error %q", assignmentAck.ErrorMessage)
	}
	if assignmentAck.AvailableCapacity != 40 {
		t.Fatalf("available capacity = %d, want 40", assignmentAck.AvailableCapacity)
	}

	adjustmentAck, err := client.AdjustVUsers(ctx, VUserAdjustment{
		TestID:       "test-1",
		TargetVUsers: 12,
	})
	if err != nil {
		t.Fatalf("AdjustVUsers: %v", err)
	}
	if !adjustmentAck.Accepted {
		t.Fatal("expected vuser adjustment to be accepted")
	}
	if adjustmentAck.CurrentVUsers != 12 {
		t.Fatalf("current vusers = %d, want 12", adjustmentAck.CurrentVUsers)
	}

	heartbeat, err := client.OpenHeartbeat(ctx)
	if err != nil {
		t.Fatalf("OpenHeartbeat: %v", err)
	}
	if err := heartbeat.Send(Heartbeat{GeneratorID: "generator-1", TimestampUnixMs: 1234}); err != nil {
		t.Fatalf("Heartbeat Send: %v", err)
	}
	pong, err := heartbeat.Receive()
	if err != nil {
		t.Fatalf("Heartbeat Receive: %v", err)
	}
	if pong.TimestampUnixMs < 1234 {
		t.Fatalf("heartbeat pong timestamp = %d, want >= 1234", pong.TimestampUnixMs)
	}
	if !reflect.DeepEqual(pong.ActiveTestIDs, []string{"test-1"}) {
		t.Fatalf("active test ids = %v, want [test-1]", pong.ActiveTestIDs)
	}
	stopAck, err := client.StopTest(ctx, "test-1", true)
	if err != nil {
		t.Fatalf("StopTest: %v", err)
	}
	if !stopAck.Acknowledged {
		t.Fatal("expected stop acknowledgement")
	}
	if stopAck.PendingVUsers != 12 {
		t.Fatalf("pending vusers = %d, want 12", stopAck.PendingVUsers)
	}
	if err := heartbeat.Send(Heartbeat{GeneratorID: "generator-1", TimestampUnixMs: 1235}); err != nil {
		t.Fatalf("Heartbeat Send after stop: %v", err)
	}
	pong, err = heartbeat.Receive()
	if err != nil {
		t.Fatalf("Heartbeat Receive after stop: %v", err)
	}
	if len(pong.ActiveTestIDs) != 0 {
		t.Fatalf("active test ids after stop = %v, want empty", pong.ActiveTestIDs)
	}
	if err := heartbeat.CloseSend(); err != nil {
		t.Fatalf("Heartbeat CloseSend: %v", err)
	}
}