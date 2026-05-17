package grpc

import (
	"context"
	"net"
	"strings"
	"testing"

	pb "github.com/jasonczech/stormrunner/api/proto"
	"github.com/jasonczech/stormrunner/pkg/distributed/generatorrpc"
	"github.com/jasonczech/stormrunner/pkg/protocol"
	googlegrpc "google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func TestGRPCUnaryWithReflection(t *testing.T) {
	addr, stop := startReflectionGenerator(t)
	defer stop()

	client := &GRPC{}
	err := client.Init(context.Background(), map[string]any{
		"base_url":               addr,
		"grpc_plain_text":        true,
		"grpc_use_reflection":    true,
		"grpc_timeout_ms":        float64(1000),
		"grpc_max_recv_msg_size": float64(1 << 20),
		"grpc_metadata":          map[string]string{"x-suite": "stormrunner"},
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer client.Teardown(context.Background())

	result, err := client.Execute(context.Background(), &protocol.Action{
		Name:   "cache script",
		Type:   "grpc_unary",
		Target: "stormrunner.v1.GeneratorControl/DistributeScript",
		Body:   []byte(`{"scriptId":"script-1","content":"e30=","contentHash":"hash-1"}`),
		Headers: map[string]string{
			"x-action": "cache",
		},
		Extractors: []protocol.Extractor{{
			Name:       "hash",
			Source:     "body",
			Type:       "jsonpath",
			Expression: "$.contentHash",
		}},
		Assertions: []protocol.Assertion{{
			Type:     "grpc_status",
			Expected: float64(0),
		}, {
			Type:  "body_contains",
			Value: "hash-1",
		}},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute failed: %s", result.ErrorMessage)
	}
	if result.DependencyType != "grpc" {
		t.Fatalf("DependencyType = %q, want grpc", result.DependencyType)
	}
	if result.DependencyTarget != addr {
		t.Fatalf("DependencyTarget = %q, want %q", result.DependencyTarget, addr)
	}
	if result.BytesSent == 0 || result.BytesReceived == 0 {
		t.Fatalf("bytes sent/received = %d/%d", result.BytesSent, result.BytesReceived)
	}
	if !strings.Contains(result.ResponseBody, `"cached":true`) {
		t.Fatalf("ResponseBody = %s", result.ResponseBody)
	}
	if !strings.Contains(result.ResponseBody, `"contentHash":"hash-1"`) {
		t.Fatalf("ResponseBody = %s", result.ResponseBody)
	}
	if result.Extractions["hash"] != "hash-1" {
		t.Fatalf("hash extraction = %q, want hash-1", result.Extractions["hash"])
	}
}

func TestGRPCUnaryRequiresServiceMethodTarget(t *testing.T) {
	client := &GRPC{}
	result, err := client.Execute(context.Background(), &protocol.Action{
		Name:   "bad",
		Type:   "grpc_unary",
		Target: "stormrunner.v1.GeneratorControl",
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Success {
		t.Fatal("Execute should fail for missing method target")
	}
	if !strings.Contains(result.ErrorMessage, "service/method") {
		t.Fatalf("ErrorMessage = %q", result.ErrorMessage)
	}
}

func startReflectionGenerator(t *testing.T) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	server := googlegrpc.NewServer()
	pb.RegisterGeneratorControlServer(server, generatorrpc.NewService("grpc-test", 100))
	reflection.Register(server)
	go func() {
		_ = server.Serve(listener)
	}()
	return listener.Addr().String(), func() {
		server.Stop()
		_ = listener.Close()
	}
}
