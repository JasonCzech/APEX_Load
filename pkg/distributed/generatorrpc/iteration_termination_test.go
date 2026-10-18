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
	"github.com/jasonczech/apexload/pkg/dsl"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

func TestIterationDistributedCompletesTenThousand(t *testing.T) {
	var samples atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { samples.Add(1); w.WriteHeader(200) }))
	defer target.Close()
	service := newIsolatedService(t, 50, WithExecution())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	script, _ := json.Marshal(&dsl.Script{ID: "finite", Name: "finite", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: target.URL, RunLogic: dsl.RunLogicConfig{Iterations: 200}}, Actions: []dsl.Action{{Name: "sample", Type: "GET", Target: "/"}}})
	// Two independently staged groups exercise shared completion without duration.
	for _, group := range []string{"a", "b"} {
		ack, err := service.AssignWork(ctx, &pb.WorkAssignment{TestId: "finite-run", GroupName: group, ScriptId: "finite", ScriptContent: script, VuserCount: 25, RampUpSeconds: 1, TerminationMode: "iterations"})
		if err != nil || !ack.GetAccepted() {
			t.Fatalf("assign %s: %v %+v", group, err, ack)
		}
	}
	ack, err := service.CommitWork(ctx, &pb.WorkCommitRequest{TestId: "finite-run"})
	if err != nil || !ack.GetAccepted() {
		t.Fatalf("commit: %v %+v", err, ack)
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		service.mu.RLock()
		active := len(service.executions)
		service.mu.RUnlock()
		if active == 0 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("iteration run did not complete")
		case <-ticker.C:
		}
	}
	if samples.Load() != 10000 {
		t.Fatalf("samples=%d", samples.Load())
	}
}

// Exercise protobuf assignment/commit and two independent generator lifecycles.
func TestIterationTwoGeneratorRPCShardsCompleteExactly(t *testing.T) {
	var samples atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { samples.Add(1); w.WriteHeader(http.StatusOK) }))
	defer target.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	script, err := json.Marshal(&dsl.Script{ID: "finite-rpc", Name: "finite-rpc", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: target.URL, RunLogic: dsl.RunLogicConfig{Iterations: 200}}, Actions: []dsl.Action{{Name: "sample", Type: "GET", Target: "/"}}})
	if err != nil {
		t.Fatal(err)
	}
	var services []*Service
	var clients []pb.GeneratorControlClient
	for shard := 0; shard < 2; shard++ {
		service := newIsolatedService(t, 25, WithExecution())
		t.Cleanup(func() { _ = service.Shutdown(context.Background()) })
		listener := bufconn.Listen(2 << 20)
		server := grpc.NewServer()
		pb.RegisterGeneratorControlServer(server, service)
		go func() { _ = server.Serve(listener) }()
		t.Cleanup(func() { server.Stop(); _ = listener.Close() })
		conn, err := grpc.NewClient("passthrough:///iteration-worker", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		client := pb.NewGeneratorControlClient(conn)
		ack, err := client.AssignWork(ctx, &pb.WorkAssignment{TestId: "finite-rpc-run", GroupName: "users", ScriptId: "finite-rpc", ScriptContent: script, VuserCount: 25, RampUpSeconds: 1, TerminationMode: "iterations", ShardOrdinal: uint32(shard), ShardCount: 2, VuserIdOffset: uint64(shard) * DistributedVUserIDNamespaceSize})
		if err != nil || !ack.GetAccepted() {
			t.Fatalf("assign shard %d: %v %+v", shard, err, ack)
		}
		services = append(services, service)
		clients = append(clients, client)
	}
	for _, client := range clients {
		ack, err := client.CommitWork(ctx, &pb.WorkCommitRequest{TestId: "finite-rpc-run"})
		if err != nil || !ack.GetAccepted() {
			t.Fatalf("commit: %v %+v", err, ack)
		}
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		active := 0
		for _, service := range services {
			service.mu.RLock()
			active += len(service.executions)
			service.mu.RUnlock()
		}
		if active == 0 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("two-generator iteration run did not finish")
		case <-ticker.C:
		}
	}
	if got := samples.Load(); got != 10000 {
		t.Fatalf("two-generator requests=%d, want 10000", got)
	}
}
