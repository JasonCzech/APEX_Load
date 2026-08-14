package generatorrpc

import (
	"context"
	"net"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"google.golang.org/grpc"
)

func TestReverseRegistryDuplicateIdentityReplacesPriorSession(t *testing.T) {
	registry := NewReverseRegistry()
	firstController, firstGenerator := net.Pipe()
	defer firstGenerator.Close()
	firstGeneration, err := registry.Register("generator-1", firstController)
	if err != nil {
		t.Fatal(err)
	}
	secondController, secondGenerator := net.Pipe()
	defer secondGenerator.Close()
	secondGeneration, err := registry.Register("generator-1", secondController)
	if err != nil {
		t.Fatal(err)
	}
	if secondGeneration <= firstGeneration {
		t.Fatalf("generations = %d then %d", firstGeneration, secondGeneration)
	}
	_ = firstGenerator.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
	if _, err := firstGenerator.Write([]byte("stale")); err == nil {
		t.Fatal("replaced reverse session remained writable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, err := registry.Dial(ctx, "generator-1")
	if err != nil {
		t.Fatal(err)
	}
	if got != secondController {
		t.Fatal("dial did not return the replacement session")
	}
	registry.Disconnect("generator-1", firstGeneration)
	if !registry.Connected("generator-1") {
		t.Fatal("stale disconnect removed the replacement session")
	}
}

func TestGeneratorControlRPCUsesReverseByteStream(t *testing.T) {
	for _, key := range []string{grpcTLSCertEnvVar, grpcTLSKeyEnvVar, grpcTLSCAEnvVar, grpcTLSServerNameEnvVar, grpcAuthTokenEnvVar, "APEXLOAD_ENV"} {
		t.Setenv(key, "")
	}
	previousRegistry := DefaultReverseRegistry
	DefaultReverseRegistry = NewReverseRegistry()
	t.Cleanup(func() { DefaultReverseRegistry = previousRegistry })

	controllerConn, generatorConn := net.Pipe()
	listener := NewConnListener(generatorConn.LocalAddr())
	server := grpc.NewServer()
	service := newIsolatedService(t, 10)
	pb.RegisterGeneratorControlServer(server, service)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := listener.Offer(ctx, generatorConn); err != nil {
		t.Fatalf("Offer: %v", err)
	}
	if _, err := DefaultReverseRegistry.Register("generator-reverse", controllerConn); err != nil {
		t.Fatalf("Register: %v", err)
	}
	client, err := Dial(ctx, "tunnel://generator-reverse")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	ack, err := client.DistributeScript(ctx, Script{ID: "script-reverse", Content: []byte(`{"name":"reverse"}`), ContentHash: "hash-reverse"})
	if err != nil {
		t.Fatalf("DistributeScript over reverse channel: %v", err)
	}
	if !ack.Cached {
		t.Fatal("reverse-channel script was not cached")
	}
}
