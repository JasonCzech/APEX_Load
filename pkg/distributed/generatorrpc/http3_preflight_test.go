package generatorrpc

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/internal/h3fixture"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestProbeHTTP3ChecksStagedGroupsBeforeCommitOnly(t *testing.T) {
	clearTransportEnv(t)
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")
	fixture := h3fixture.Start(t, h3fixture.Options{})
	t.Setenv(httpprotocol.EnvHTTP3, "true")
	service := newIsolatedService(t, 10, WithExecution())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = service.Shutdown(ctx)
	})
	if _, features, _ := runtimeAdvertisementWithCapacities(context.Background(), false, nil, nil); !containsString(features, FeatureHTTP3PreflightV1) {
		t.Fatalf("features %v lack %s", features, FeatureHTTP3PreflightV1)
	}
	script := fmt.Sprintf(`{"name":"h3","protocol":"http","config":{"base_url":"https://127.0.0.1:%d","http":{"version":"3","tls":{"insecure_skip_verify":true},"quic":{"handshake_timeout_ms":2000}}},"actions":[{"name":"home","type":"GET","target":"/"}]}`, fixture.Port)
	ack, err := service.AssignWork(context.Background(), &pb.WorkAssignment{TestId: "h3-run", GroupName: "buyers", ScriptId: "h3", ScriptContent: []byte(script), VuserCount: 1, TargetDuration: durationpb.New(time.Second)})
	if err != nil || !ack.GetAccepted() {
		t.Fatalf("stage: %v %v", ack, err)
	}

	transport := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	pb.RegisterGeneratorControlServer(server, service)
	go func() { _ = server.Serve(transport) }()
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough:///h3", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return transport.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := &Client{rpc: pb.NewGeneratorControlClient(conn)}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	results, err := client.ProbeHTTP3(ctx, "h3-run", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || !results[0].OK || results[0].Group != "buyers" || results[0].Policy != "3" || results[0].HandshakeMs <= 0 || results[0].CheckedAt.IsZero() {
		t.Fatalf("results = %+v", results)
	}
	if n := fixture.Ledger.Count(func(h3fixture.Attempt) bool { return true }); n != 0 {
		t.Fatalf("the preflight sent %d requests", n)
	}

	if _, err := client.ProbeHTTP3(ctx, "unknown", 0); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("unknown test: %v", err)
	}
	if commit, err := service.CommitWork(context.Background(), &pb.WorkCommitRequest{TestId: "h3-run"}); err != nil || !commit.GetAccepted() {
		t.Fatalf("commit: %v %v", commit, err)
	}
	if _, err := client.ProbeHTTP3(ctx, "h3-run", 0); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("a committed test is not probed: %v", err)
	}

	t.Setenv(httpprotocol.EnvHTTP3, "")
	if _, err := service.ProbeHTTP3(ctx, &pb.HTTP3ProbeRequest{TestId: "h3-run"}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("a generator without HTTP/3 refuses the probe: %v", err)
	}
	if _, features, _ := runtimeAdvertisementWithCapacities(context.Background(), false, nil, nil); containsString(features, FeatureHTTP3PreflightV1) {
		t.Fatalf("features %v advertise the preflight without HTTP/3", features)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
