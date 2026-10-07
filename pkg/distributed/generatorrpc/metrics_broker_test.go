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

func TestMetricsBrokerAssignsMonotonicBatchSequences(t *testing.T) {
	broker := newMetricsBroker(10, 10, 0)
	id, subscriber, _, err := registerMetricsBrokerSubscriber(t, broker, 7, "run")
	if err != nil {
		t.Fatalf("register subscriber: %v", err)
	}
	defer broker.unregister(id)

	first := &pb.MetricsBatch{TestId: "run", ExecutionGeneration: 7, LifecycleState: "RUNNING"}
	second := &pb.MetricsBatch{TestId: "run", ExecutionGeneration: 7, LifecycleState: "RUNNING"}
	broker.publish(first, nil)
	broker.publish(second, nil)
	if got := subscriber.pop(); got == nil || got.GetBatchSequence() != 1 {
		t.Fatalf("first sequence = %#v, want 1", got)
	}
	if got := subscriber.pop(); got == nil || got.GetBatchSequence() != 2 {
		t.Fatalf("second sequence = %#v, want 2", got)
	}

	// A different execution generation owns an independent sequence.
	other := &pb.MetricsBatch{TestId: "run", ExecutionGeneration: 8, LifecycleState: "RUNNING"}
	broker.publish(other, nil)
	if other.GetBatchSequence() != 1 {
		t.Fatalf("new-generation sequence = %d, want 1", other.GetBatchSequence())
	}

	// A caller-supplied terminal sequence still ends the broker's per-run
	// counter; otherwise the map would retain one entry per completed run.
	terminal := &pb.MetricsBatch{TestId: "run", ExecutionGeneration: 7, LifecycleState: "COMPLETE", BatchSequence: 99}
	broker.publishTerminal(terminal)
	broker.publishMu.Lock()
	_, retained := broker.batchSequences[terminalBatchKey{testID: "run", generation: 7}]
	broker.publishMu.Unlock()
	if retained {
		t.Fatal("terminal publication retained its batch-sequence counter")
	}

	// Forced cleanup can remove an execution that never reached a terminal
	// publication; its independent generation counter must be reclaimed too.
	broker.deleteTerminalsForTest("run")
	broker.publishMu.Lock()
	_, retained = broker.batchSequences[terminalBatchKey{testID: "run", generation: 8}]
	broker.publishMu.Unlock()
	if retained {
		t.Fatal("forced cleanup retained a non-terminal batch-sequence counter")
	}
}

func TestMetricsBrokerReconnectSnapshotRetainsCurrentBatchSequence(t *testing.T) {
	broker := newMetricsBroker(10, 10, 0)
	for index := 0; index < 5; index++ {
		broker.publish(&pb.MetricsBatch{
			TestId:              "run",
			ExecutionGeneration: 7,
			LifecycleState:      "RUNNING",
		}, nil)
	}

	live := &pb.MetricsBatch{
		TestId:              "run",
		ExecutionGeneration: 7,
		LifecycleState:      "RUNNING",
		TimestampUnixMs:     time.Now().UnixMilli(),
	}
	id, _, initial, err := registerMetricsBrokerSubscriber(t, broker, 7, "run", live)
	if err != nil {
		t.Fatalf("register reconnecting subscriber: %v", err)
	}
	defer broker.unregister(id)
	if len(initial) != 1 {
		t.Fatalf("initial snapshots = %#v, want one live snapshot", initial)
	}
	if got := initial[0].GetBatchSequence(); got != 5 {
		t.Fatalf("reconnect snapshot sequence = %d, want current sequence 5", got)
	}
	if live.GetBatchSequence() != 0 {
		t.Fatalf("registration mutated caller-owned live snapshot sequence to %d", live.GetBatchSequence())
	}
}

func TestMetricsBrokerTerminalOverflowPreservesDetectableSequenceGap(t *testing.T) {
	broker := newMetricsBroker(10, 10, 0)
	id, subscriber, _, err := registerMetricsBrokerSubscriber(t, broker, 7, "run")
	if err != nil {
		t.Fatalf("register subscriber: %v", err)
	}
	defer broker.unregister(id)

	for index := 0; index < metricsSubscriberQueueCapacity; index++ {
		broker.publish(&pb.MetricsBatch{TestId: "run", ExecutionGeneration: 7, LifecycleState: "RUNNING"}, nil)
	}
	broker.publishTerminal(&pb.MetricsBatch{TestId: "run", ExecutionGeneration: 7, LifecycleState: "COMPLETE"})
	// A routine terminal retry arrives before this slow subscriber drains the
	// queue. It must replace the terminal payload without resetting sequence 17.
	broker.publishTerminal(&pb.MetricsBatch{TestId: "run", ExecutionGeneration: 7, LifecycleState: "FAILED", LifecycleError: "replayed"})

	sequences := make([]uint64, 0, metricsSubscriberQueueCapacity)
	for batch := subscriber.pop(); batch != nil; batch = subscriber.pop() {
		sequences = append(sequences, batch.GetBatchSequence())
	}
	if len(sequences) != metricsSubscriberQueueCapacity {
		t.Fatalf("delivered sequence count = %d, want %d (%v)", len(sequences), metricsSubscriberQueueCapacity, sequences)
	}
	for index := 0; index < metricsSubscriberQueueCapacity-1; index++ {
		if want := uint64(index + 1); sequences[index] != want {
			t.Fatalf("sequences[%d] = %d, want preserved prefix %d (%v)", index, sequences[index], want, sequences)
		}
	}
	if got := sequences[len(sequences)-1]; got != metricsSubscriberQueueCapacity+1 {
		t.Fatalf("terminal sequence = %d, want %d (%v)", got, metricsSubscriberQueueCapacity+1, sequences)
	}
	retained := broker.terminalBatch(terminalBatchKey{testID: "run", generation: 7})
	if retained == nil || retained.GetBatchSequence() != metricsSubscriberQueueCapacity+1 || retained.GetLifecycleState() != "FAILED" {
		t.Fatalf("replayed terminal tombstone = %#v, want FAILED at stable sequence %d", retained, metricsSubscriberQueueCapacity+1)
	}
	if dropped := broker.droppedCount(); dropped != 1 {
		t.Fatalf("dropped batches = %d, want 1", dropped)
	}
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

// Every publication and every re-anchored registration snapshot carries this
// process's source epoch, so the controller can tell a restarted process's
// sequences from this one's.
func TestMetricsBrokerStampsTheProcessSourceEpoch(t *testing.T) {
	if processSourceEpoch.id == "" || processSourceEpoch.startedUnixMs <= 0 {
		t.Fatalf("process epoch %+v", processSourceEpoch)
	}
	broker := newMetricsBroker(10, 10, 0)
	published := &pb.MetricsBatch{TestId: "run", ExecutionGeneration: 3, LifecycleState: "RUNNING"}
	broker.publish(published, nil)
	if published.GetSourceEpoch() != processSourceEpoch.id || published.GetSourceEpochStartedUnixMs() != processSourceEpoch.startedUnixMs || published.GetBatchSequence() != 1 {
		t.Fatalf("published batch epoch %q/%d sequence %d", published.GetSourceEpoch(), published.GetSourceEpochStartedUnixMs(), published.GetBatchSequence())
	}
	// A batch relayed with its own epoch keeps it.
	relayed := &pb.MetricsBatch{TestId: "run", ExecutionGeneration: 3, LifecycleState: "RUNNING", SourceEpoch: "other-process", SourceEpochStartedUnixMs: 1}
	broker.publish(relayed, nil)
	if relayed.GetSourceEpoch() != "other-process" {
		t.Fatalf("relayed epoch overwritten: %q", relayed.GetSourceEpoch())
	}
	live := &pb.MetricsBatch{TestId: "run", ExecutionGeneration: 3, LifecycleState: "RUNNING", TimestampUnixMs: time.Now().UnixMilli()}
	id, _, initial, err := registerMetricsBrokerSubscriber(t, broker, 3, "run", live)
	if err != nil {
		t.Fatal(err)
	}
	defer broker.unregister(id)
	if len(initial) != 1 || initial[0].GetSourceEpoch() != processSourceEpoch.id || initial[0].GetBatchSequence() != 2 {
		t.Fatalf("snapshot %#v", initial)
	}
	if live.GetSourceEpoch() != "" {
		t.Fatal("registration stamped the caller's live snapshot")
	}
}

// A terminal restored after a restart was retained before it was published,
// so it has no epoch of its own; it replays under this process's epoch,
// unsequenced, rather than reading as an older generator.
func TestRestoredTerminalReplaysUnderThisProcessEpoch(t *testing.T) {
	broker := newMetricsBroker(10, 10, 0)
	broker.restoreTerminal(&pb.MetricsBatch{TestId: "run", ExecutionGeneration: 4, LifecycleState: "COMPLETE", TimestampUnixMs: time.Now().UnixMilli()}, time.Now())
	broker.restoreTerminal(&pb.MetricsBatch{TestId: "kept", ExecutionGeneration: 4, LifecycleState: "COMPLETE", SourceEpoch: "earlier-process", SourceEpochStartedUnixMs: 1, BatchSequence: 7}, time.Now())
	id, _, initial, err := registerMetricsBrokerSubscriber(t, broker, 4, "")
	if err != nil {
		t.Fatal(err)
	}
	defer broker.unregister(id)
	epochs := map[string]*pb.MetricsBatch{}
	for _, batch := range initial {
		epochs[batch.GetTestId()] = batch
	}
	if restored := epochs["run"]; restored == nil || restored.GetSourceEpoch() != processSourceEpoch.id || restored.GetBatchSequence() != 0 {
		t.Fatalf("restored terminal %#v", restored)
	}
	if kept := epochs["kept"]; kept == nil || kept.GetSourceEpoch() != "earlier-process" || kept.GetBatchSequence() != 7 {
		t.Fatalf("a terminal with its own epoch was restamped: %#v", kept)
	}
}
