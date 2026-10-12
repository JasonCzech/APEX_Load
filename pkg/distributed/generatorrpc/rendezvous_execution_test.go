package generatorrpc

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/distributed/rendezvous"
	"github.com/jasonczech/apexload/pkg/dsl"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

func rendezvousRealClient(t *testing.T, s *Service) *Client {
	listener := bufconn.Listen(2 << 20)
	server := grpc.NewServer()
	pb.RegisterGeneratorControlServer(server, s)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	conn, err := grpc.NewClient("passthrough:///rendezvous-real", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return &Client{rpc: pb.NewGeneratorControlClient(conn)}
}
func TestRendezvousRealGeneratorEnginesHoldBeforeTargetRequests(t *testing.T) {
	var requests atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(http.StatusNoContent) }))
	defer target.Close()
	script, err := json.Marshal(&dsl.Script{ID: "script", Name: "Native checkout rendezvous", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: target.URL, RunLogic: dsl.RunLogicConfig{Iterations: 3}}, Actions: []dsl.Action{
		{Name: "Wait for checkout peers", Type: "rendezvous", Params: map[string]any{"scope": "distributed_group", "key": "checkout", "timeout_ms": 2000, "on_timeout": "fail"}},
		{Name: "Checkout request", Type: "GET", Target: "/"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	contract := rendezvous.Contract{Key: "checkout", Target: 4, TimeoutMS: 2000}
	body, _ := json.Marshal(RendezvousAssignment{Population: 4, Contracts: []rendezvous.Contract{contract}})
	c, err := rendezvous.New([]rendezvous.MemberRange{{Count: 2}, {First: DistributedVUserIDNamespaceSize, Count: 2}}, []rendezvous.Contract{contract}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	token := supervisionTestToken(99)
	services := []*Service{newIsolatedService(t, 10, WithExecution()), newIsolatedService(t, 10, WithExecution())}
	clients := make([]*Client, 2)
	for i, s := range services {
		clients[i] = rendezvousRealClient(t, s)
		t.Cleanup(func() { _ = s.Shutdown(context.Background()) })
		ack, err := s.AssignWork(ctx, &pb.WorkAssignment{TestId: "run", GroupName: "group", ScriptId: "script", ScriptContent: script, VuserCount: 2, VuserIdOffset: uint64(i) * DistributedVUserIDNamespaceSize, ShardOrdinal: uint32(i), ShardCount: 2, TerminationMode: "iterations", RunSupervisionToken: token, RendezvousConfigJson: body})
		if err != nil || !ack.GetAccepted() {
			t.Fatalf("stage%d %+v %v", i, ack, err)
		}
		claim, err := s.ClaimRunSupervision(ctx, &pb.RunSupervisionClaim{TestId: "run", RunSupervisionToken: token, RequestedTtlMilliseconds: 60000})
		if err != nil || !claim.GetAccepted() {
			t.Fatalf("supervision%d %+v %v", i, claim, err)
		}
	}
	if ack, err := services[0].CommitWork(ctx, &pb.WorkCommitRequest{TestId: "run", RunSupervisionToken: token}); err == nil && ack.GetAccepted() {
		t.Fatal("work started without a rendezvous channel")
	}
	for _, client := range clients {
		if err = client.OpenRendezvous(ctx, "run", "group", 0, token, c); err != nil {
			t.Fatal(err)
		}
	}
	ack, err := services[0].CommitWork(ctx, &pb.WorkCommitRequest{TestId: "run", RunSupervisionToken: token})
	if err != nil || !ack.GetAccepted() {
		t.Fatalf("first commit %+v %v", ack, err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		s := c.Snapshot()
		if s.Rounds["checkout/1"].Arrived == 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if arrived := c.Snapshot().Rounds["checkout/1"].Arrived; arrived != 2 {
		t.Fatalf("first generator did not reach rendezvous: %d arrivals", arrived)
	}
	if requests.Load() != 0 {
		t.Fatal("first generator sent target traffic before its peers arrived")
	}
	ack, err = services[1].CommitWork(ctx, &pb.WorkCommitRequest{TestId: "run", RunSupervisionToken: token})
	if err != nil || !ack.GetAccepted() {
		t.Fatalf("second commit %+v %v", ack, err)
	}
	for time.Now().Before(deadline.Add(3*time.Second)) && requests.Load() != 12 {
		time.Sleep(5 * time.Millisecond)
	}
	if requests.Load() != 12 {
		t.Fatalf("got %d requests, expected4 participants across3 rounds; state %+v", requests.Load(), c.Snapshot())
	}
	snapshot := c.Snapshot()
	if snapshot.High["checkout"] != 3 {
		t.Fatalf("rounds %+v", snapshot.High)
	}
	for _, s := range services {
		_, _ = s.StopTest(ctx, &pb.StopRequest{TestId: "run", RunSupervisionToken: token})
	}
}
