package generatorrpc

import (
	"context"
	"net"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

func TestGeneratorClockSampleBoundsSkewUnderAsymmetricDelay(t *testing.T) {
	sent := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		skew, forward, back time.Duration
	}{
		{2 * time.Second, 30 * time.Millisecond, 30 * time.Millisecond},
		{-2 * time.Second, 30 * time.Millisecond, 30 * time.Millisecond},
		{2 * time.Second, 400 * time.Millisecond, 5 * time.Millisecond},
		{-2 * time.Second, 5 * time.Millisecond, 400 * time.Millisecond},
	} {
		// The generator stamps its pong when the ping arrives, on its own clock.
		generatorAt := sent.Add(tc.forward).Add(tc.skew)
		received := sent.Add(tc.forward + tc.back)
		sample, ok := generatorClockSample(sent, received, generatorAt)
		if !ok {
			t.Fatalf("%+v: sample rejected", tc)
		}
		if sample.uncertainty != (tc.forward+tc.back)/2 {
			t.Fatalf("%+v: uncertainty %s", tc, sample.uncertainty)
		}
		if err := sample.offset - tc.skew; err < -sample.uncertainty || err > sample.uncertainty {
			t.Fatalf("%+v: offset %s misses the skew by more than %s", tc, sample.offset, sample.uncertainty)
		}
	}
	if _, ok := generatorClockSample(sent, sent.Add(-time.Millisecond), sent); ok {
		t.Fatal("a reply before the ping was accepted")
	}
}

func TestHeartbeatStreamReportsItsClockSample(t *testing.T) {
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	pb.RegisterGeneratorControlServer(server, NewService("generator-clock", 10))
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, "bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(conn)
	t.Cleanup(func() { _ = client.Close() })
	heartbeat, err := client.OpenHeartbeat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer heartbeat.CloseSend()
	if _, ok := heartbeat.ClockSample(); ok {
		t.Fatal("a sample was reported before any exchange")
	}
	if err := heartbeat.Send(Heartbeat{GeneratorID: "controller-clock-probe"}); err != nil {
		t.Fatal(err)
	}
	if _, err := heartbeat.Receive(); err != nil {
		t.Fatal(err)
	}
	sample, ok := heartbeat.ClockSample()
	if !ok || sample.RoundTrip < 0 || sample.Uncertainty != sample.RoundTrip/2 || sample.ObservedAt.IsZero() {
		t.Fatalf("sample = %+v ok=%v", sample, ok)
	}
	// Same host, same clock: the offset can only be measurement error.
	if sample.Offset < -sample.Uncertainty-time.Millisecond || sample.Offset > sample.Uncertainty+time.Millisecond {
		t.Fatalf("same-clock offset %s exceeds its uncertainty %s", sample.Offset, sample.Uncertainty)
	}
}
