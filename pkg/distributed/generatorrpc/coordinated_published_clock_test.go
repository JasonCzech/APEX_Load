package generatorrpc

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
)

func TestCoordinatedStartTranslatesPublishedClockToLocalWallDeadline(t *testing.T) {
	previous := generatorPublishedClock
	t.Cleanup(func() { generatorPublishedClock = previous })
	now := time.Now()
	for _, drift := range []time.Duration{-39 * time.Minute, -5 * time.Second, -600 * time.Millisecond, 600 * time.Millisecond, 5 * time.Second, 39 * time.Minute} {
		t.Run(drift.String(), func(t *testing.T) {
			generatorPublishedClock = &publishedClock{wall: now.Round(0).Add(drift), mono: now}
			for _, lead := range []time.Duration{-time.Second, 2 * time.Second} {
				barrier := now.Add(lead)
				for _, transit := range []time.Duration{10 * time.Millisecond, 800 * time.Millisecond} {
					received := now.Add(transit)
					ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
						coordinatedStartControllerMetadataKey, fmt.Sprint(now.UnixNano()),
						coordinatedStartClockOffsetMetadataKey, fmt.Sprint(drift.Nanoseconds()),
						coordinatedStartClockErrorMetadataKey, "0",
					))
					request := &pb.WorkCommitRequest{TestId: "clock-step", StartAtUnixNano: barrier.UnixNano()}
					got, err := translateCoordinatedStartRequest(ctx, request, received)
					if err != nil {
						t.Fatal(err)
					}
					if got.GetStartAtUnixNano() != barrier.UnixNano() {
						t.Fatalf("drift %s transit %s lead %s: got %s want %s", drift, transit, lead, time.Unix(0, got.GetStartAtUnixNano()), barrier)
					}
					if request.GetStartAtUnixNano() != barrier.UnixNano() {
						t.Fatal("mutated request")
					}
					if !generatorPublishedClock.at(now).Equal(now.Round(0).Add(drift)) {
						t.Fatal("active telemetry clock re-anchored")
					}
				}
			}
		})
	}
}

func TestCoordinatedStartExecutesAtWallDeadlineAfterPublishedClockDrift(t *testing.T) {
	previous := generatorPublishedClock
	t.Cleanup(func() { generatorPublishedClock = previous })
	for _, drift := range []time.Duration{-39 * time.Minute, 39 * time.Minute} {
		t.Run(drift.String(), func(t *testing.T) {
			t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
			t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")
			var first atomic.Int64
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				first.CompareAndSwap(0, time.Now().UnixNano())
				w.WriteHeader(http.StatusOK)
			}))
			defer target.Close()
			service := newIsolatedService(t, 10, WithExecution())
			const id = "published-clock-run"
			stageCoordinatedHTTPWork(t, service, id, "script", target.URL, "/")
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				ack, err := service.StopTest(ctx, &pb.StopRequest{TestId: id})
				if err != nil || ack == nil || !ack.GetAcknowledged() {
					t.Errorf("cleanup: %v %v", ack, err)
				}
			}()
			now := time.Now()
			generatorPublishedClock = &publishedClock{wall: now.Round(0).Add(drift), mono: now}
			barrier := now.Add(time.Second)

			listener := bufconn.Listen(1 << 20)
			server := grpc.NewServer()
			pb.RegisterGeneratorControlServer(server, service)
			go func() { _ = server.Serve(listener) }()
			defer server.Stop()
			defer listener.Close()
			conn, err := grpc.NewClient("passthrough:///bufnet",
				grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
				grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				t.Fatal(err)
			}
			client := NewClient(conn)
			defer client.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			ack, err := client.CommitWorkAt(ctx, id, 0, barrier)
			if err != nil || !ack.Accepted {
				t.Fatalf("commit through heartbeat probe: %+v %v", ack, err)
			}
			estimate, ok := client.coordinatedStartClockEstimate(time.Now())
			if !ok || (estimate.offset-drift).Abs() > estimate.uncertainty+time.Millisecond {
				t.Fatalf("clock probe: %+v %v", estimate, ok)
			}

			deadline := time.Now().Add(3 * time.Second)
			for first.Load() == 0 && time.Now().Before(deadline) {
				time.Sleep(5 * time.Millisecond)
			}
			if got := first.Load(); got == 0 || got < barrier.Add(-estimate.uncertainty).UnixNano() {
				t.Fatalf("first request %d, barrier %d", got, barrier.UnixNano())
			}
		})
	}
}
