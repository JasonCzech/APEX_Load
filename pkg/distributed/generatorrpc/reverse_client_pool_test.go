package generatorrpc

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
)

type reversePoolProbeService struct {
	pb.UnimplementedGeneratorControlServer
}

func (*reversePoolProbeService) DistributeScript(_ context.Context, script *pb.ScriptPayload) (*pb.ScriptAck, error) {
	return &pb.ScriptAck{Cached: true, ContentHash: script.ContentHash}, nil
}

func setupReversePoolTest(t *testing.T) (*ReverseRegistry, func() uint64) {
	t.Helper()
	for _, key := range []string{grpcTLSCertEnvVar, grpcTLSKeyEnvVar, grpcTLSCAEnvVar, grpcTLSServerNameEnvVar, grpcAuthTokenEnvVar, "APEXLOAD_ENV"} {
		t.Setenv(key, "")
	}
	previous := DefaultReverseRegistry
	registry := NewReverseRegistry()
	DefaultReverseRegistry = registry
	t.Cleanup(func() { DefaultReverseRegistry = previous })
	listener := NewConnListener(&net.TCPAddr{})
	server := grpc.NewServer()
	pb.RegisterGeneratorControlServer(server, &reversePoolProbeService{})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	enroll := func() uint64 {
		controllerConn, generatorConn := net.Pipe()
		// Also close connections that have been offered to the server but not
		// consumed by a client dial. Server.Stop waits for those handshakes.
		t.Cleanup(func() { _ = controllerConn.Close(); _ = generatorConn.Close() })
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		if err := listener.Offer(ctx, generatorConn); err != nil {
			t.Fatal(err)
		}
		generation, err := registry.Register("pooled", controllerConn)
		if err != nil {
			t.Fatal(err)
		}
		return generation
	}
	return registry, enroll
}

func assertReversePoolRPC(t *testing.T, client *Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	ack, err := client.rpc.DistributeScript(ctx, &pb.ScriptPayload{ScriptId: "probe", ContentHash: "probe-hash"}, grpc.WaitForReady(true))
	if err != nil || !ack.GetCached() || ack.GetContentHash() != "probe-hash" {
		t.Fatalf("reverse RPC = %v, %v", ack, err)
	}
}

func TestReverseDialSharesTransportUntilFinalClientClose(t *testing.T) {
	registry, enroll := setupReversePoolTest(t)
	enroll()
	first, err := Dial(t.Context(), "tunnel://pooled")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	assertReversePoolRPC(t, first)
	second, err := Dial(t.Context(), "tunnel://pooled")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	if first == second || first.conn != second.conn {
		t.Fatal("reverse dials must have independent handles on one transport")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	assertReversePoolRPC(t, second)
	third, err := Dial(t.Context(), "tunnel://pooled")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = third.Close() })
	if third.conn != second.conn {
		t.Fatal("closing the initial caller invalidated the canonical pool handle")
	}
	_ = second.Close()
	assertReversePoolRPC(t, third)
	_ = third.Close()
	if third.conn.GetState() != connectivity.Shutdown {
		t.Fatal("last client did not release physical transport")
	}
	registry.clientsMu.Lock()
	remaining := len(registry.clients)
	registry.clientsMu.Unlock()
	if remaining != 0 {
		t.Fatalf("closed reverse clients retained in pool: %d", remaining)
	}
	enroll()
	fresh, err := Dial(t.Context(), "tunnel://pooled")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fresh.Close() })
	if fresh.conn == third.conn {
		t.Fatal("new enrollment reused an already closed connection")
	}
	assertReversePoolRPC(t, fresh)
}

func TestReverseSharedClientReconnectsOnSameTransport(t *testing.T) {
	registry, enroll := setupReversePoolTest(t)
	firstGeneration := enroll()
	client, err := Dial(t.Context(), "tunnel://pooled")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	assertReversePoolRPC(t, client)
	retained, err := client.Retain()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = retained.Close() })
	_ = client.Close()
	registry.Disconnect("pooled", firstGeneration)
	// Disconnect closes the stream immediately, while gRPC notices that close
	// asynchronously. Starting an RPC on the old Ready transport can fail
	// before WaitForReady has a chance to wait for the replacement transport.
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	for retained.conn.GetState() == connectivity.Ready {
		if !retained.conn.WaitForStateChange(ctx, connectivity.Ready) {
			t.Fatal("gRPC did not observe the disconnected reverse transport")
		}
	}
	secondGeneration := enroll()
	registry.Disconnect("pooled", firstGeneration)
	if secondGeneration <= firstGeneration || !registry.Connected("pooled") {
		t.Fatal("stale disconnect removed a later enrollment")
	}
	assertReversePoolRPC(t, retained)
}

func TestReverseSharedClientConcurrentDialsKeepActiveTransport(t *testing.T) {
	_, enroll := setupReversePoolTest(t)
	enroll()
	active, err := Dial(t.Context(), "tunnel://pooled")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = active.Close() })
	assertReversePoolRPC(t, active)
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() {
			client, err := Dial(t.Context(), "tunnel://pooled")
			if err != nil {
				t.Errorf("concurrent dial: %v", err)
				return
			}
			defer client.Close()
			if client.conn != active.conn {
				t.Error("concurrent dial created another physical connection")
			}
		})
	}
	workers.Wait()
	assertReversePoolRPC(t, active)
}

func TestReverseDialCustomOptionsKeepPrivateTransport(t *testing.T) {
	_, enroll := setupReversePoolTest(t)
	enroll()
	pooled, err := Dial(t.Context(), "tunnel://pooled")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pooled.Close() })
	assertReversePoolRPC(t, pooled)
	custom, err := Dial(t.Context(), "tunnel://pooled", grpc.WithUserAgent("custom-transport"))
	if err != nil {
		t.Fatal(err)
	}
	defer custom.Close()
	if custom.conn == pooled.conn {
		t.Fatal("custom dial options were silently replaced by a pooled connection")
	}
}

func TestReverseRegistryRejectsAmbiguousGeneratorIdentities(t *testing.T) {
	registry := NewReverseRegistry()
	for _, id := range []string{"tunnel://victim", "victim|region=other", "victim?identity=other", "victim/alias"} {
		controllerConn, generatorConn := net.Pipe()
		_, err := registry.Register(id, controllerConn)
		_ = controllerConn.Close()
		_ = generatorConn.Close()
		if err == nil {
			t.Fatalf("Register(%q) accepted an ambiguous identity", id)
		}
		if _, valid := ReverseGeneratorID(ReverseTargetPrefix + id); valid {
			t.Fatalf("nested/ambiguous target %q accepted", ReverseTargetPrefix+id)
		}
	}
}
