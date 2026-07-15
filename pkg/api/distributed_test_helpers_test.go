package api

import (
	"context"
	"net"
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
		grpc.WithBlock(),
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
