package api

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
)

// scriptedMetricsGenerator streams the batches a test hands it, in order.
type scriptedMetricsGenerator struct {
	pb.UnimplementedGeneratorControlServer
	batches chan *pb.MetricsBatch
}

func (g *scriptedMetricsGenerator) StreamMetrics(stream pb.GeneratorControl_StreamMetricsServer) error {
	if err := stream.SendHeader(metadata.Pairs("apexload-metrics-subscribed", "1")); err != nil {
		return err
	}
	for {
		select {
		case batch := <-g.batches:
			if err := stream.Send(batch); err != nil {
				return err
			}
		case <-stream.Context().Done():
			return nil
		}
	}
}

func openScriptedMetricsStream(t *testing.T, testID string, generation uint64) (*scriptedMetricsGenerator, *generatorrpc.MetricsStream) {
	t.Helper()
	generator := &scriptedMetricsGenerator{batches: make(chan *pb.MetricsBatch, 8)}
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	pb.RegisterGeneratorControlServer(server, generator)
	go func() { _ = server.Serve(listener) }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	client, err := generatorrpc.Dial(ctx, "gen-a",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial scripted generator: %v", err)
	}
	streamCtx, streamCancel := context.WithCancel(context.Background())
	stream, err := client.OpenMetricsStreamForTestGeneration(streamCtx, testID, generation)
	if err != nil {
		t.Fatalf("open scripted metrics stream: %v", err)
	}
	t.Cleanup(func() {
		streamCancel()
		_ = client.Close()
		server.Stop()
		_ = listener.Close()
	})
	return generator, stream
}

// A stop requested on the controller sets the run STOPPING before the
// generator stops. A window the generator collected before then still
// arrives, ahead of its terminal batch with the final drain: the controller
// counts the window, reads on, and counts the drain.
func TestStoppingRunCountsTheWindowInTransitAndTheFinalDrain(t *testing.T) {
	const testID, generation = "stop-window", uint64(3)
	generator, stream := openScriptedMetricsStream(t, testID, generation)
	runner := NewTestRunner(1, NewHub(), nil)
	backend := newDistributedExecutionBackend([]string{"gen-a"}, time.Second, func(context.Context, string) (generatorClient, error) {
		return nil, errors.New("no generator to dial in this test")
	})
	runCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	started := time.Now()
	run := &ManagedTest{
		ID: testID, Name: testID, Status: "RUNNING", StartedAt: &started, Duration: time.Minute,
		Groups:                       []TestGroup{{Name: "buyers", VUsers: 1}},
		executionGeneration:          generation,
		executionCtx:                 runCtx,
		cancel:                       cancel,
		remoteMetricsStreamsByTarget: map[string]*generatorrpc.MetricsStream{"gen-a": stream},
		remoteMetricsBatchesByTarget: map[string]generatorrpc.MetricsBatch{},
		remoteTargetStates:           map[string]string{"gen-a": "RUNNING"},
		remoteLifecycleCtx:           runCtx,
		remoteActivated:              true,
		executionBackend:             backend,
	}
	runner.mu.Lock()
	runner.tests[run.ID] = run
	runner.mu.Unlock()
	consumed := make(chan struct{})
	go func() {
		backend.consumeMetrics(runCtx, runner, run, "gen-a", stream)
		close(consumed)
	}()

	send := func(sequence uint64, state string, count int64) {
		at := int64(1_000 * sequence)
		generator.batches <- &pb.MetricsBatch{
			TestId: testID, ExecutionGeneration: generation, LifecycleState: state,
			SourceEpoch: "epoch-1", SourceEpochStartedUnixMs: 1, BatchSequence: sequence,
			TimestampUnixMs: at, WindowMs: 1000,
			FleetMetrics: &pb.MetricAggregate{Name: "fleet", Count: count},
			Groups:       []*pb.GroupMetric{{Name: "buyers", ActiveVusers: 1, Metrics: &pb.MetricAggregate{Name: "buyers", Count: count}}},
			Telemetry:    &pb.MetricsBatchTelemetry{CollectionSequence: sequence, PublishedAtUnixMs: at, CollectedAtUnixMs: at - 5},
		}
	}
	waitFor := func(what string, done func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !done() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s", what)
			}
			time.Sleep(2 * time.Millisecond)
		}
	}

	send(1, "RUNNING", 100)
	waitFor("the first window", func() bool {
		runner.mu.RLock()
		defer runner.mu.RUnlock()
		return run.remoteMetricsBatchesByTarget["gen-a"].BatchSequence == 1
	})
	runner.mu.Lock()
	run.Status = "STOPPING"
	runner.mu.Unlock()
	// Collected before the generator stopped, then the stop drain.
	send(2, "RUNNING", 50)
	send(3, "COMPLETE", 5)
	select {
	case <-consumed:
	case <-time.After(10 * time.Second):
		t.Fatal("the consumer did not finish after the terminal batch")
	}

	runner.mu.RLock()
	defer runner.mu.RUnlock()
	if run.Status != "ABORTED" {
		t.Fatalf("status = %s, want ABORTED from the terminal batch of a stopped run", run.Status)
	}
	if run.LiveMetrics == nil || deref(run.LiveMetrics.RunTotalTx) != 155 {
		var got int64
		if run.LiveMetrics != nil {
			got = deref(run.LiveMetrics.RunTotalTx)
		}
		t.Fatalf("run total = %d, want 155: the window in transit and the final drain both counted", got)
	}
	// Each received batch is in the source ledger: no publication reads as lost.
	if _, loss := telemetrySourceRollupLocked(run); loss == nil || loss.LostPublications == nil || loss.LostPublications.State != "exact" || loss.LostPublications.Value != 0 {
		t.Fatalf("loss accounting = %+v, want exactly 0 lost publications", loss)
	}
}

// Whether a consumer may publish a window, or must read on for the terminal
// batch, is decided under one read lock, so a stop rolled back to RUNNING
// between two reads cannot end the consumer.
func TestDistributedMetricsWindowOwnership(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	stream := &generatorrpc.MetricsStream{}
	run := &ManagedTest{ID: "window-ownership", Status: "RUNNING", remoteMetricsStreamsByTarget: map[string]*generatorrpc.MetricsStream{"gen-a": stream}}
	runner.tests[run.ID] = run
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, tc := range []struct {
		status                    string
		streamOwned               bool
		current, owned, awaitsEnd bool
	}{
		{"RUNNING", true, true, true, false},
		{"PAUSED", true, true, true, false},
		{"STOPPING", true, false, false, true},
		{"STOPPING", false, false, false, false},
		{"ABORTED", true, false, false, false},
	} {
		run.Status = tc.status
		run.remoteMetricsStreamsByTarget = map[string]*generatorrpc.MetricsStream{}
		if tc.streamOwned {
			run.remoteMetricsStreamsByTarget["gen-a"] = stream
		}
		fence, current, awaits := captureDistributedMetricsWindowFence(runner, run, "gen-a", stream, ctx, 0)
		owned, awaitsEnd := distributedMetricsWindowOwnership(runner, run, "gen-a", fence)
		if current != tc.current || awaits != (!tc.current && tc.awaitsEnd) || owned != tc.owned || awaitsEnd != tc.awaitsEnd {
			t.Errorf("%s (stream owned %v): capture = (%v, %v), ownership = (%v, %v); want current %v, owned %v, awaits %v",
				tc.status, tc.streamOwned, current, awaits, owned, awaitsEnd, tc.current, tc.owned, tc.awaitsEnd)
		}
	}
}

// A window whose fence went stale (a topology change) is stored under a fresh
// fence; during a stop it is counted, not published, and the consumer reads
// on. A consumer that lost its stream ends.
func TestStoreDistributedWindowOutcomes(t *testing.T) {
	runner, run, backend := distributedRunTotalsRun(t, "store-window", []TestGroup{{Name: "buyers", VUsers: 1}}, "gen-a")
	stream := &generatorrpc.MetricsStream{}
	run.remoteMetricsStreamsByTarget = map[string]*generatorrpc.MetricsStream{"gen-a": stream}
	ctx := context.Background()
	capture := func() distributedMetricsConsumerFence {
		t.Helper()
		fence, current, _ := captureDistributedMetricsWindowFence(runner, run, "gen-a", stream, ctx, 0)
		if !current {
			t.Fatal("the fence is not current")
		}
		return fence
	}
	store := func(fence distributedMetricsConsumerFence, sequence uint64, count int64) distributedWindowStore {
		t.Helper()
		_, outcome := backend.storeDistributedWindow(runner, run, "gen-a", stream, ctx, 0, collected(distributedRunTotalsBatch(run.ID, int64(sequence), count, 0), sequence, int64(1_000*sequence)), fence)
		return outcome
	}
	tally := func() int64 {
		runner.mu.RLock()
		defer runner.mu.RUnlock()
		return run.runTotals.tx
	}

	fence := capture()
	if outcome := store(fence, 1, 100); outcome != distributedWindowStored || tally() != 100 {
		t.Fatalf("a running window = outcome %d, tally %d; want stored, 100", outcome, tally())
	}
	if outcome := store(fence, 1, 100); outcome != distributedWindowSkipped || tally() != 100 {
		t.Fatalf("a duplicate window = outcome %d, tally %d; want skipped, 100", outcome, tally())
	}
	// A topology change while the run runs: the window is stored under a
	// fresh fence, and the consumer goes on to publish it.
	runner.mu.Lock()
	run.remoteControlGeneration++
	runner.mu.Unlock()
	if outcome := store(fence, 2, 20); outcome != distributedWindowStored || tally() != 120 {
		t.Fatalf("a window after a topology change = outcome %d, tally %d; want stored, 120", outcome, tally())
	}
	// A topology change and a stop land before the next window is stored.
	runner.mu.Lock()
	run.remoteControlGeneration++
	run.Status = "STOPPING"
	runner.mu.Unlock()
	if outcome := store(fence, 3, 50); outcome != distributedWindowSkipped || tally() != 170 {
		t.Fatalf("a window during a stop = outcome %d, tally %d; want skipped (read on), 170", outcome, tally())
	}
	runner.mu.Lock()
	run.remoteMetricsStreamsByTarget = map[string]*generatorrpc.MetricsStream{}
	runner.mu.Unlock()
	if outcome := store(fence, 4, 5); outcome != distributedWindowConsumerEnded || tally() != 170 {
		t.Fatalf("a window after the stream was taken away = outcome %d, tally %d; want ended, 170", outcome, tally())
	}
}

// Once a window is stored, a topology that moved past its fence does not stop
// its publication; a stop or a lost stream does.
func TestStoredDistributedWindowOutcomeIgnoresTheTopology(t *testing.T) {
	runner, run, _ := distributedRunTotalsRun(t, "stored-window-outcome", []TestGroup{{Name: "buyers", VUsers: 1}}, "gen-a")
	stream := &generatorrpc.MetricsStream{}
	run.remoteMetricsStreamsByTarget = map[string]*generatorrpc.MetricsStream{"gen-a": stream}
	fence, current, _ := captureDistributedMetricsWindowFence(runner, run, "gen-a", stream, context.Background(), 0)
	if !current {
		t.Fatal("the fence is not current")
	}
	run.remoteControlGeneration++ // the fence is now stale
	for _, tc := range []struct {
		status      string
		streamOwned bool
		want        distributedWindowStore
	}{
		{"RUNNING", true, distributedWindowStored},
		{"STOPPING", true, distributedWindowSkipped},
		{"RUNNING", false, distributedWindowConsumerEnded},
	} {
		run.Status = tc.status
		run.remoteMetricsStreamsByTarget = map[string]*generatorrpc.MetricsStream{}
		if tc.streamOwned {
			run.remoteMetricsStreamsByTarget["gen-a"] = stream
		}
		if got := storedDistributedWindowOutcome(runner, run, "gen-a", fence); got != tc.want {
			t.Errorf("%s (stream owned %v) with a stale fence = %d, want %d", tc.status, tc.streamOwned, got, tc.want)
		}
	}
}
