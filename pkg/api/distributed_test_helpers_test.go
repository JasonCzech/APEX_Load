package api

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

func newTestGeneratorRPCClient(t *testing.T, generatorID string, maxVUsers int, options ...generatorrpc.ServiceOption) (*generatorrpc.Client, *generatorrpc.Service) {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	service := generatorrpc.NewService(generatorID, maxVUsers, options...)
	pb.RegisterGeneratorControlServer(server, service)
	go func() { _ = server.Serve(listener) }()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	client, err := generatorrpc.Dial(
		ctx,
		generatorID,
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		server.Stop()
		_ = listener.Close()
		t.Fatalf("dial test generator %s: %v", generatorID, err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), time.Second)
		defer shutdownCancel()
		_ = service.Shutdown(shutdownCtx)
		server.Stop()
		_ = listener.Close()
	})
	return client, service
}

// generatorTestClock is a controllable time source for generator services built
// by newTestGeneratorRPCClient. Execution-epoch leases expire on wall-clock
// time, so a test that needs a lapsed epoch would otherwise pick a TTL short
// enough to elapse and then sleep past it — an assertion that races the
// scheduler and fails under load (docs/qa/FLAKE_LOG.md, FLAKE-1). Advancing this
// makes the lapse something the test causes.
type generatorTestClock struct {
	mu  sync.Mutex
	now time.Time
}

func newGeneratorTestClock(start time.Time) *generatorTestClock {
	return &generatorTestClock{now: start}
}

func (c *generatorTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *generatorTestClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}
