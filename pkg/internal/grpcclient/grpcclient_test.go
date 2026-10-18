package grpcclient

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/test/bufconn"
)

func TestPassthroughTargetMatchesDialContext(t *testing.T) {
	for target, want := range map[string]string{
		"generator.example:50051":       "passthrough:///generator.example:50051",
		"localhost:50051":               "passthrough:///localhost:50051",
		"10.0.0.5:50051":                "passthrough:///10.0.0.5:50051",
		"[::1]:50051":                   "passthrough:///[::1]:50051",
		"bufnet":                        "passthrough:///bufnet",
		"dns:///generator.example:443":  "dns:///generator.example:443",
		"passthrough:///reverse-gen-1":  "passthrough:///reverse-gen-1",
		"unix:///var/run/apexload.sock": "unix:///var/run/apexload.sock",
	} {
		if got := passthroughTarget(target); got != want {
			t.Errorf("passthroughTarget(%q) = %q, want %q", target, got, want)
		}
	}
}

func TestNewDialsATargetWithoutSchemeAsWritten(t *testing.T) {
	listener := serveHealth(t)
	dialed := make(chan string, 1)
	conn, err := New("bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, address string) (net.Conn, error) {
			select {
			case dialed <- address:
			default:
			}
			return listener.DialContext(ctx)
		}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{}); err != nil {
		t.Fatalf("health check over a target without a scheme: %v", err)
	}
	if address := <-dialed; address != "bufnet" {
		t.Fatalf("dialer address = %q, want the target as written", address)
	}
}

func TestNewConnectsBeforeTheFirstRPC(t *testing.T) {
	listener := serveHealth(t)
	dialed := make(chan struct{}, 1)
	conn, err := New("bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			select {
			case dialed <- struct{}{}:
			default:
			}
			return listener.DialContext(ctx)
		}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	select {
	case <-dialed:
	case <-time.After(5 * time.Second):
		t.Fatal("the channel did not connect until an RPC was made")
	}
}

func serveHealth(t *testing.T) *bufconn.Listener {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	healthpb.RegisterHealthServer(server, health.NewServer())
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})
	return listener
}
