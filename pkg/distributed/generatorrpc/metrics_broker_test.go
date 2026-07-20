package generatorrpc

import (
	"sync"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func registerMetricsBrokerSubscriber(t testing.TB, broker *metricsBroker, generation uint64, testID string, live ...*pb.MetricsBatch) (int, *metricsSubscriber, []*pb.MetricsBatch, error) {
	t.Helper()
	return broker.register(generation, testID, live, time.Now())
}

func TestMetricsBrokerEnforcesSubscriptionLimitAndGenerationScope(t *testing.T) {
	broker := newMetricsBroker(1, 10, 0)
	live := &pb.MetricsBatch{TestId: "run", ExecutionGeneration: 7, LifecycleState: "RUNNING", LifecycleError: "original"}
	id, subscriber, initial, err := registerMetricsBrokerSubscriber(t, broker, 7, "run",
		live,
		&pb.MetricsBatch{TestId: "other", ExecutionGeneration: 7, LifecycleState: "RUNNING"},
		&pb.MetricsBatch{TestId: "run", ExecutionGeneration: 8, LifecycleState: "RUNNING"},
	)
	if err != nil {
		t.Fatalf("register subscriber: %v", err)
	}
	if len(initial) != 1 || initial[0].GetTestId() != "run" || initial[0].GetExecutionGeneration() != 7 {
		t.Fatalf("generation-scoped initial snapshots = %#v", initial)
	}
	live.LifecycleError = "mutated"
	if initial[0].GetLifecycleError() != "original" {
		t.Fatal("registration exposed a mutable caller-owned batch")
	}

	if _, _, _, err := registerMetricsBrokerSubscriber(t, broker, 7, "other"); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("second registration error = %v (%s), want ResourceExhausted", err, status.Code(err))
	}

	wrongGeneration := &pb.MetricsBatch{TestId: "run", ExecutionGeneration: 8, LifecycleState: "RUNNING"}
	broker.publish(wrongGeneration, nil)
	broker.publish(&pb.MetricsBatch{TestId: "other", ExecutionGeneration: 7, LifecycleState: "RUNNING"}, nil)
	published := &pb.MetricsBatch{TestId: "run", ExecutionGeneration: 7, LifecycleState: "RUNNING", LifecycleError: "ownership-transferred"}
	broker.publish(published, nil)
	if got := subscriber.pop(); got != published || got.GetLifecycleError() != "ownership-transferred" {
		t.Fatalf("scoped published batch = %#v", got)
	}
	if got := subscriber.pop(); got != nil {
		t.Fatalf("out-of-scope batch reached subscriber: %#v", got)
	}

	broker.unregister(id)
	if _, _, _, err := registerMetricsBrokerSubscriber(t, broker, 7, "other"); err != nil {
		t.Fatalf("registration after release: %v", err)
	}
}

func TestMetricsBrokerCoalescesTerminalEventsAndRetainsNewestReplay(t *testing.T) {
	base := time.Now().UTC().Truncate(time.Millisecond)
	broker := newMetricsBroker(10, 2, 0)
	id, subscriber, _, err := registerMetricsBrokerSubscriber(t, broker, 7, "")
	if err != nil {
		t.Fatalf("register subscriber: %v", err)
	}
	defer broker.unregister(id)

	broker.publishTerminalAt(&pb.MetricsBatch{
		TestId: "a", ExecutionGeneration: 7, LifecycleState: "COMPLETE", TimestampUnixMs: base.UnixMilli(),
	}, base)
	broker.publishTerminalAt(&pb.MetricsBatch{
		TestId: "a", ExecutionGeneration: 7, LifecycleState: "FAILED", LifecycleError: "replacement", TimestampUnixMs: base.Add(time.Second).UnixMilli(),
	}, base.Add(time.Second))
	broker.publishTerminalAt(&pb.MetricsBatch{
		TestId: "b", ExecutionGeneration: 7, LifecycleState: "COMPLETE", TimestampUnixMs: base.Add(2 * time.Second).UnixMilli(),
	}, base.Add(2*time.Second))
	broker.publishTerminalAt(&pb.MetricsBatch{
		TestId: "c", ExecutionGeneration: 7, LifecycleState: "COMPLETE", TimestampUnixMs: base.Add(3 * time.Second).UnixMilli(),
	}, base.Add(3*time.Second))

	first := subscriber.pop()
	if first == nil || first.GetTestId() != "a" || first.GetLifecycleState() != "FAILED" || first.GetLifecycleError() != "replacement" {
		t.Fatalf("coalesced terminal event = %#v", first)
	}
	if got := broker.terminalBatch(terminalBatchKey{testID: "a", generation: 7}); got != nil {
		t.Fatalf("oldest terminal tombstone was retained: %#v", got)
	}

	replayID, _, replay, err := registerMetricsBrokerSubscriber(t, broker, 7, "")
	if err != nil {
		t.Fatalf("register replay subscriber: %v", err)
	}
	defer broker.unregister(replayID)
	if len(replay) != 2 || replay[0].GetTestId() != "b" || replay[1].GetTestId() != "c" {
		t.Fatalf("retained terminal replay = %#v, want b/c", replay)
	}
}

func TestMetricsBrokerExpiresTerminalReplayByTTL(t *testing.T) {
	base := time.Now().UTC().Truncate(time.Millisecond)
	broker := newMetricsBroker(10, 10, time.Minute)
	broker.publishTerminalAt(&pb.MetricsBatch{
		TestId: "expired", ExecutionGeneration: 4, LifecycleState: "COMPLETE", TimestampUnixMs: base.UnixMilli(),
	}, base.Add(2*time.Minute))
	_, _, replay, err := registerMetricsBrokerSubscriber(t, broker, 4, "expired")
	if err != nil {
		t.Fatalf("register subscriber: %v", err)
	}
	if len(replay) != 0 {
		t.Fatalf("expired terminal replay = %#v", replay)
	}
}

func TestMetricsBrokerClonesAtTerminalRetentionBoundary(t *testing.T) {
	broker := newMetricsBroker(10, 10, 0)
	id, subscriber, _, err := registerMetricsBrokerSubscriber(t, broker, 4, "run")
	if err != nil {
		t.Fatal(err)
	}
	defer broker.unregister(id)

	terminal := &pb.MetricsBatch{
		TestId:              "run",
		ExecutionGeneration: 4,
		LifecycleState:      "COMPLETE",
		LifecycleError:      "retained",
	}
	broker.publishTerminal(terminal)
	terminal.LifecycleError = "caller mutation"

	key := terminalBatchKey{testID: "run", generation: 4}
	retained := broker.terminalBatch(key)
	if retained == nil || retained.GetLifecycleError() != "retained" {
		t.Fatalf("terminal retention snapshot = %#v", retained)
	}
	queued := subscriber.pop()
	if queued == nil || queued == terminal || queued.GetLifecycleError() != "retained" {
		t.Fatalf("terminal queued snapshot = %#v", queued)
	}
	retained.LifecycleError = "external mutation"
	if replay := broker.terminalBatch(key); replay == nil || replay.GetLifecycleError() != "retained" {
		t.Fatalf("terminal external snapshot mutated retention: %#v", replay)
	}
}

func TestMetricsBrokerConcurrentTerminalPublicationAndRegistration(t *testing.T) {
	broker := newMetricsBroker(128, 64, 0)
	id, subscriber, _, err := registerMetricsBrokerSubscriber(t, broker, 3, "")
	if err != nil {
		t.Fatalf("register subscriber: %v", err)
	}
	defer broker.unregister(id)

	const publishers = 48
	var workers sync.WaitGroup
	workers.Add(publishers)
	for index := 0; index < publishers; index++ {
		index := index
		go func() {
			defer workers.Done()
			testID := string(rune('a' + index%8))
			broker.publishTerminal(&pb.MetricsBatch{TestId: testID, ExecutionGeneration: 3, LifecycleState: "COMPLETE"})
			registrationID, _, _, registerErr := broker.register(3, testID, nil, time.Now())
			if registerErr == nil {
				broker.unregister(registrationID)
			}
		}()
	}
	workers.Wait()

	queued := 0
	for subscriber.pop() != nil {
		queued++
	}
	if queued != 8 {
		t.Fatalf("coalesced concurrent terminal events = %d, want 8", queued)
	}
	replayID, _, replay, err := registerMetricsBrokerSubscriber(t, broker, 3, "")
	if err != nil {
		t.Fatalf("register replay subscriber: %v", err)
	}
	defer broker.unregister(replayID)
	if len(replay) != 8 {
		t.Fatalf("concurrent retained terminal replay = %d, want 8", len(replay))
	}
}
