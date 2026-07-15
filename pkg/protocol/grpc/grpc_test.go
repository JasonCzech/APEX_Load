package grpc

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/fileasset"
	"github.com/jasonczech/apexload/pkg/protocol"
	googlegrpc "google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	reflectionpb "google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
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
		"grpc_metadata":          map[string]string{"x-suite": "apexload"},
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer client.Teardown(context.Background())

	result, err := client.Execute(context.Background(), &protocol.Action{
		Name:   "cache script",
		Type:   "grpc_unary",
		Target: "apexload.v1.GeneratorControl/DistributeScript",
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

func TestGRPCActionDeadlineIncludesBlockingReflection(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	server := googlegrpc.NewServer()
	reflectionpb.RegisterServerReflectionServer(server, blockingReflectionServer{})
	go func() { _ = server.Serve(listener) }()
	defer func() {
		server.Stop()
		_ = listener.Close()
	}()

	client := &GRPC{}
	if err := client.Init(context.Background(), map[string]any{
		"base_url": listener.Addr().String(), "grpc_plain_text": true, "grpc_use_reflection": true,
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer client.Teardown(context.Background())
	started := time.Now()
	result, err := client.Execute(context.Background(), &protocol.Action{
		Name: "blocked", Type: "grpc_unary", Target: "example.Blocked/Call", Params: map[string]any{"timeout_ms": float64(25)},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Success || !strings.Contains(strings.ToLower(result.ErrorMessage), "deadline") {
		t.Fatalf("result = %#v, want deadline failure", result)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("reflection ignored action deadline: %s", elapsed)
	}
}

type blockingReflectionServer struct {
	reflectionpb.UnimplementedServerReflectionServer
}

func (blockingReflectionServer) ServerReflectionInfo(stream googlegrpc.BidiStreamingServer[reflectionpb.ServerReflectionRequest, reflectionpb.ServerReflectionResponse]) error {
	<-stream.Context().Done()
	return stream.Context().Err()
}

func TestGRPCStreamBodiesEnforceInputCountAndBytesBeforeCopy(t *testing.T) {
	tooMany := make([]string, dsl.MaxGRPCStreamMessages+1)
	if _, err := bodiesFromParam(tooMany); err == nil || !strings.Contains(err.Error(), "message limit") {
		t.Fatalf("too-many input error = %v", err)
	}
	if _, err := bodiesFromParam([]string{strings.Repeat("x", dsl.MaxGRPCStreamBytes+1)}); err == nil || !strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("oversized input error = %v", err)
	}
	action := &protocol.Action{Body: []byte(strings.Repeat("x", dsl.MaxGRPCStreamBytes+1))}
	if _, err := streamBodies(action); err == nil || !strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("oversized body error = %v", err)
	}
}

func TestGRPCUnaryWithDescriptorFile(t *testing.T) {
	addr, stop := startReflectionGenerator(t)
	defer stop()

	descriptorPath := writeGeneratorDescriptorSet(t)
	if err := fileasset.RegisterRoot(filepath.Dir(descriptorPath)); err != nil {
		t.Fatal(err)
	}
	client := &GRPC{}
	err := client.Init(context.Background(), map[string]any{
		"base_url":        addr,
		"grpc_plain_text": true,
		"grpc_proto_files": []string{
			descriptorPath,
		},
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer client.Teardown(context.Background())

	result, err := client.Execute(context.Background(), &protocol.Action{
		Name:   "cache script",
		Type:   "grpc_unary",
		Target: "apexload.v1.GeneratorControl/DistributeScript",
		Body:   []byte(`{"scriptId":"script-2","content":"e30=","contentHash":"hash-2"}`),
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute failed: %s", result.ErrorMessage)
	}
	if !strings.Contains(result.ResponseBody, `"contentHash":"hash-2"`) {
		t.Fatalf("ResponseBody = %s", result.ResponseBody)
	}
}

func TestGRPCBidiStreamWithReflection(t *testing.T) {
	addr, stop := startReflectionGenerator(t)
	defer stop()

	client := &GRPC{}
	err := client.Init(context.Background(), map[string]any{
		"base_url":            addr,
		"grpc_plain_text":     true,
		"grpc_use_reflection": true,
		"grpc_timeout_ms":     float64(1000),
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer client.Teardown(context.Background())

	result, err := client.Execute(context.Background(), &protocol.Action{
		Name:   "heartbeat",
		Type:   "grpc_bidi_stream",
		Target: "apexload.v1.GeneratorControl/Heartbeat",
		Params: map[string]any{
			"max_messages": float64(1),
			"messages": []any{
				map[string]any{"generatorId": "client-1", "timestampUnixMs": float64(42)},
			},
		},
		Extractors: []protocol.Extractor{{
			Name:       "generator",
			Source:     "body",
			Type:       "jmespath",
			Expression: "[0].generatorId",
		}},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute failed: %s", result.ErrorMessage)
	}
	if result.ServerTelemetry == nil || result.ServerTelemetry.MessagesSent != 1 || result.ServerTelemetry.MessagesReceived != 1 {
		t.Fatalf("ServerTelemetry = %#v", result.ServerTelemetry)
	}
	if !strings.Contains(result.ResponseBody, `"generatorId":"grpc-test"`) {
		t.Fatalf("ResponseBody = %s", result.ResponseBody)
	}
	if result.Extractions["generator"] != "grpc-test" {
		t.Fatalf("generator extraction = %q", result.Extractions["generator"])
	}
}

func TestGRPCFailurePopulatesSnapshot(t *testing.T) {
	addr, stop := startReflectionGenerator(t)
	defer stop()

	client := &GRPC{}
	if err := client.Init(context.Background(), map[string]any{
		"base_url":            addr,
		"grpc_plain_text":     true,
		"grpc_use_reflection": true,
		"grpc_timeout_ms":     float64(1000),
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer client.Teardown(context.Background())

	result, err := client.Execute(context.Background(), &protocol.Action{
		Name:            "cache script",
		Type:            "grpc_unary",
		Target:          "apexload.v1.GeneratorControl/DistributeScript",
		Body:            []byte(`{"scriptId":"script-3","content":"e30=","contentHash":"secret-hash"}`),
		RedactedBody:    []byte(`{"scriptId":"script-3","content":"[REDACTED]","contentHash":"[REDACTED]"}`),
		Headers:         map[string]string{"authorization": "Bearer secret"},
		RedactedHeaders: map[string]string{"authorization": "Bearer [REDACTED]"},
		Assertions: []protocol.Assertion{{
			Type:  "body_contains",
			Value: "missing-hash",
		}},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Success {
		t.Fatal("Execute should fail for unmet assertion")
	}
	if result.Snapshot == nil {
		t.Fatal("Snapshot = nil, want populated grpc snapshot")
	}
	if result.Snapshot.RequestMethod != "grpc_unary" {
		t.Fatalf("Snapshot.RequestMethod = %q, want grpc_unary", result.Snapshot.RequestMethod)
	}
	if !strings.Contains(result.Snapshot.RequestURL, "apexload.v1.GeneratorControl/DistributeScript") {
		t.Fatalf("Snapshot.RequestURL = %q, want full method", result.Snapshot.RequestURL)
	}
	if strings.Contains(result.Snapshot.RequestBody, "secret-hash") || !strings.Contains(result.Snapshot.RequestBody, "[REDACTED]") {
		t.Fatalf("Snapshot.RequestBody = %q, want redacted body", result.Snapshot.RequestBody)
	}
	if result.Snapshot.RequestHeaders["authorization"] != "Bearer [REDACTED]" {
		t.Fatalf("Snapshot.RequestHeaders = %#v, want redacted authorization", result.Snapshot.RequestHeaders)
	}
	if !strings.Contains(result.Snapshot.ResponseBody, `"contentHash":"secret-hash"`) {
		t.Fatalf("Snapshot.ResponseBody = %q, want grpc response body", result.Snapshot.ResponseBody)
	}
}

func TestGRPCUnaryRequiresServiceMethodTarget(t *testing.T) {
	client := &GRPC{}
	result, err := client.Execute(context.Background(), &protocol.Action{
		Name:   "bad",
		Type:   "grpc_unary",
		Target: "apexload.v1.GeneratorControl",
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

func BenchmarkGRPCUnaryDescriptorSet(b *testing.B) {
	addr, stop := startReflectionGenerator(b)
	defer stop()

	client := &GRPC{}
	err := client.Init(context.Background(), map[string]any{
		"base_url":        addr,
		"grpc_plain_text": true,
		"grpc_proto_files": []string{
			writeGeneratorDescriptorSet(b),
		},
	})
	if err != nil {
		b.Fatalf("Init: %v", err)
	}
	defer client.Teardown(context.Background())

	action := &protocol.Action{
		Name:   "cache script",
		Type:   "grpc_unary",
		Target: "apexload.v1.GeneratorControl/DistributeScript",
		Body:   []byte(`{"scriptId":"bench-script","content":"e30=","contentHash":"bench-hash"}`),
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result, err := client.Execute(context.Background(), action)
		if err != nil {
			b.Fatalf("Execute returned error: %v", err)
		}
		if !result.Success {
			b.Fatalf("Execute failed: %s", result.ErrorMessage)
		}
	}
}

func startReflectionGenerator(t testing.TB) (string, func()) {
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

func writeGeneratorDescriptorSet(t testing.TB) string {
	t.Helper()
	file, err := protoregistry.GlobalFiles.FindFileByPath("api/proto/generator.proto")
	if err != nil {
		t.Fatalf("FindFileByPath: %v", err)
	}
	set := &descriptorpb.FileDescriptorSet{}
	seen := make(map[string]struct{})
	var appendWithDependencies func(protoreflect.FileDescriptor)
	appendWithDependencies = func(descriptor protoreflect.FileDescriptor) {
		path := descriptor.Path()
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		imports := descriptor.Imports()
		for i := 0; i < imports.Len(); i++ {
			dependencyPath := imports.Get(i).Path()
			dependency, findErr := protoregistry.GlobalFiles.FindFileByPath(dependencyPath)
			if findErr != nil {
				t.Fatalf("FindFileByPath(%q): %v", dependencyPath, findErr)
			}
			appendWithDependencies(dependency)
		}
		set.File = append(set.File, protodesc.ToFileDescriptorProto(descriptor))
	}
	appendWithDependencies(file)
	body, err := proto.Marshal(set)
	if err != nil {
		t.Fatalf("Marshal descriptor set: %v", err)
	}
	path := filepath.Join(t.TempDir(), "generator.pb")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}
