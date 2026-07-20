package generatorrpc

import (
	"context"
	"sync"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/protocol"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type blockingMetricsServerStream struct {
	grpc.ServerStream
	ctx         context.Context
	sendStarted chan struct{}
	releaseSend chan struct{}
	sendOnce    sync.Once
}

func (s *blockingMetricsServerStream) Context() context.Context { return s.ctx }

func (s *blockingMetricsServerStream) Send(*pb.MetricsBatch) error {
	s.sendOnce.Do(func() { close(s.sendStarted) })
	select {
	case <-s.releaseSend:
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}

func (s *blockingMetricsServerStream) Recv() (*pb.ControlSignal, error) {
	<-s.ctx.Done()
	return nil, s.ctx.Err()
}

func TestTerminalMetricsEvictNonterminalWhenSubscriberQueueIsFull(t *testing.T) {
	service := newIsolatedService(t, 50)
	_, subscriber, _, err := service.registerSubscriber(0)
	if err != nil {
		t.Fatalf("register subscriber: %v", err)
	}
	for index := 0; index < metricsSubscriberQueueCapacity; index++ {
		if dropped := subscriber.enqueue(&pb.MetricsBatch{TestId: "live", LifecycleState: "RUNNING"}); dropped != 0 {
			t.Fatalf("fill queue dropped index %d", index)
		}
	}

	terminal := service.metricsBatchForState("run", 0, 12, false, map[string]int32{"buyers": 12}, "COMPLETE", "")
	service.publishTerminalMetrics(terminal)
	if got := service.metricsBroker.droppedCount(); got != 1 {
		t.Fatalf("evicted nonterminal batches = %d, want 1", got)
	}

	foundTerminal := false
	for {
		batch := subscriber.pop()
		if batch == nil {
			break
		}
		if batch.GetTestId() != "run" {
			continue
		}
		foundTerminal = true
		if batch.GetStats().GetActiveVusers() != 0 || len(batch.GetGroups()) != 1 || batch.GetGroups()[0].GetActiveVusers() != 0 {
			t.Fatalf("terminal active VUsers were not zeroed: %#v", batch)
		}
	}
	if !foundTerminal {
		t.Fatal("terminal batch was dropped from a saturated subscriber")
	}
}

func TestTerminalOnlyBacklogFencesAtHardBoundAndReconnectReplays(t *testing.T) {
	service := newIsolatedService(t, 50)
	id, subscriber, _, err := service.registerSubscriber(0)
	if err != nil {
		t.Fatalf("register subscriber: %v", err)
	}
	for index := 0; index < metricsSubscriberQueueCapacity+1; index++ {
		service.publishTerminalMetrics(service.metricsBatchForState(
			string(rune('a'+index)), 0, 0, false, nil, "COMPLETE", "",
		))
	}
	select {
	case <-subscriber.fenced:
	default:
		t.Fatal("terminal-only overflow did not fence the slow subscriber")
	}
	if !subscriber.overflowed.Load() {
		t.Fatal("terminal-only overflow was not recorded on the subscriber")
	}
	subscriber.mu.Lock()
	queued := len(subscriber.queue)
	subscriber.mu.Unlock()
	if queued != metricsSubscriberQueueCapacity {
		t.Fatalf("queued terminal batches = %d, want hard bound %d", queued, metricsSubscriberQueueCapacity)
	}
	service.unregisterSubscriber(id)

	reconnectID, _, replay, err := service.registerSubscriber(0)
	if err != nil {
		t.Fatalf("register reconnect: %v", err)
	}
	defer service.unregisterSubscriber(reconnectID)
	if len(replay) != metricsSubscriberQueueCapacity+1 {
		t.Fatalf("replayed terminal tombstones = %d, want %d", len(replay), metricsSubscriberQueueCapacity+1)
	}
}

func TestStreamMetricsReturnsResourceExhaustedForTerminalBacklog(t *testing.T) {
	service := newIsolatedService(t, 50)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := &blockingMetricsServerStream{
		ctx: ctx, sendStarted: make(chan struct{}), releaseSend: make(chan struct{}),
	}
	streamDone := make(chan error, 1)
	go func() { streamDone <- service.StreamMetrics(stream) }()

	deadline := time.Now().Add(time.Second)
	for {
		registered := service.metricsBroker.subscriberCount() == 1
		if registered {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("metrics stream did not register")
		}
		time.Sleep(time.Millisecond)
	}
	service.publishTerminalMetrics(service.metricsBatchForState("blocked-send", 0, 0, false, nil, "COMPLETE", ""))
	select {
	case <-stream.sendStarted:
	case <-time.After(time.Second):
		t.Fatal("metrics stream did not enter the blocking send")
	}
	for index := 0; index < metricsSubscriberQueueCapacity+1; index++ {
		service.publishTerminalMetrics(service.metricsBatchForState(
			string(rune('A'+index)), 0, 0, false, nil, "COMPLETE", "",
		))
	}
	close(stream.releaseSend)
	select {
	case err := <-streamDone:
		if status.Code(err) != codes.ResourceExhausted {
			t.Fatalf("StreamMetrics error = %v (%s), want ResourceExhausted", err, status.Code(err))
		}
	case <-time.After(time.Second):
		t.Fatal("metrics stream did not disconnect after terminal backlog overflow")
	}
}

func TestDuplicateForcedStopWaitsForQuiescenceAndRetainsFinalEvidence(t *testing.T) {
	service := newIsolatedService(t, 50)
	results := &transactionBuffer{lastDrainAt: time.Now().Add(-time.Second)}
	if err := results.RecordTransaction(&protocol.Result{
		ActionName: "checkout", Group: "buyers", Duration: 125 * time.Millisecond,
		Success: true, BusinessTransaction: true, StartTime: time.Now(),
	}); err != nil {
		t.Fatalf("record final transaction: %v", err)
	}
	execution := &groupExecution{
		groupName: "buyers", executionGeneration: 0, results: results,
		cancel: func() {}, done: make(chan struct{}),
	}
	service.mu.Lock()
	service.assignments["run"] = assignedTest{
		testID: "run", committed: true, vuserCount: 1,
		groups: map[string]int32{"buyers": 1}, activeGroups: map[string]int32{"buyers": 1},
	}
	service.executions["run"] = map[string]*groupExecution{"buyers": execution}
	service.mu.Unlock()

	firstDone := make(chan error, 1)
	go func() {
		ack, err := service.StopTest(context.Background(), &pb.StopRequest{TestId: "run"})
		if err == nil && !ack.GetAcknowledged() {
			err = status.Error(codes.Internal, "first stop was not acknowledged")
		}
		firstDone <- err
	}()

	deadline := time.Now().Add(time.Second)
	for {
		service.mu.RLock()
		_, stopping := service.stoppingTests["run"]
		_, assigned := service.assignments["run"]
		service.mu.RUnlock()
		if stopping && !assigned {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first stop did not detach the assignment")
		}
		time.Sleep(time.Millisecond)
	}

	duplicateCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	ack, err := service.StopTest(duplicateCtx, &pb.StopRequest{TestId: "run"})
	if status.Code(err) != codes.DeadlineExceeded || ack != nil {
		t.Fatalf("duplicate stop before quiescence = (%#v, %v), want DeadlineExceeded", ack, err)
	}

	close(execution.done)
	if err := <-firstDone; err != nil {
		t.Fatalf("first stop: %v", err)
	}
	ack, err = service.StopTest(context.Background(), &pb.StopRequest{TestId: "run"})
	if err != nil || !ack.GetAcknowledged() {
		t.Fatalf("duplicate stop after terminal tombstone = (%#v, %v)", ack, err)
	}

	tombstone := service.metricsBroker.terminalBatch(terminalBatchKey{testID: "run", generation: 0})
	if tombstone == nil || tombstone.GetFleetMetrics().GetCount() != 1 || len(tombstone.GetTransactionMetrics()) != 1 {
		t.Fatalf("forced-stop terminal evidence = %#v", tombstone)
	}
}

func TestTerminalTombstoneReplaysOnEveryReconnect(t *testing.T) {
	service := newIsolatedService(t, 50)
	terminal := service.metricsBatchForState("run", 0, 0, false, map[string]int32{"buyers": 7}, "FAILED", "engine failed")
	service.publishTerminalMetrics(terminal)

	for reconnect := 0; reconnect < 2; reconnect++ {
		id, _, initial, err := service.registerScopedSubscriber(0, "run")
		if err != nil {
			t.Fatalf("register reconnect %d: %v", reconnect, err)
		}
		if len(initial) != 1 || initial[0].GetLifecycleState() != "FAILED" || initial[0].GetLifecycleError() != "engine failed" {
			t.Fatalf("reconnect %d initial batches = %#v", reconnect, initial)
		}
		service.unregisterSubscriber(id)
	}
}

func TestFinalMetricsAreQueuedBeforeTerminalAndRetained(t *testing.T) {
	service := newIsolatedService(t, 50)
	_, subscriber, _, err := service.registerSubscriber(0)
	if err != nil {
		t.Fatalf("register subscriber: %v", err)
	}
	service.mu.Lock()
	service.assignments["run"] = assignedTest{
		testID: "run", committed: true, vuserCount: 2,
		groups: map[string]int32{"buyers": 2}, activeGroups: map[string]int32{"buyers": 2},
	}
	service.mu.Unlock()
	if err := service.publishMetricsWithAggregates(
		"run", "buyers", nil, false,
		GeneratorStats{ActiveVUsers: 2},
		[]TransactionMetric{{Name: "checkout", Group: "buyers", DurationMs: 100, Success: true}},
		MetricsTelemetry{WindowMs: 1000},
		metricsAggregateSet{Fleet: &MetricAggregate{Name: "fleet", Count: 4, P95Ms: 125}},
	); err != nil {
		t.Fatalf("publish final metrics: %v", err)
	}
	service.mu.Lock()
	assignment := service.assignments["run"]
	service.markStoppingLocked("run", 0)
	delete(service.assignments, "run")
	service.mu.Unlock()
	service.finishStoppedTest("run", assignment, nil)

	live := subscriber.pop()
	terminal := subscriber.pop()
	if live == nil || live.GetLifecycleState() != "RUNNING" || live.GetFleetMetrics().GetCount() != 4 {
		t.Fatalf("first queued batch = %#v, want final RUNNING evidence", live)
	}
	if terminal == nil || terminal.GetLifecycleState() != "COMPLETE" || terminal.GetFleetMetrics().GetCount() != 4 {
		t.Fatalf("second queued batch = %#v, want evidence-bearing terminal", terminal)
	}
}

func TestTerminalTombstoneMakesExactGenerationStopIdempotent(t *testing.T) {
	service := newIsolatedService(t, 50)
	service.mu.Lock()
	service.executionGeneration = 7
	service.executionExpiresAt = time.Now().Add(time.Minute)
	service.mu.Unlock()
	service.publishTerminalMetrics(service.metricsBatchForState("run", 7, 0, false, nil, "COMPLETE", ""))

	for attempt := 0; attempt < 2; attempt++ {
		ack, err := service.StopTest(context.Background(), &pb.StopRequest{TestId: "run", ExecutionGeneration: 7})
		if err != nil || !ack.GetAcknowledged() {
			t.Fatalf("duplicate exact-generation stop %d = (%#v, %v)", attempt, ack, err)
		}
	}
	_, err := service.StopTest(context.Background(), &pb.StopRequest{TestId: "run", ExecutionGeneration: 6})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("stale-generation stop error = %v (%s), want FailedPrecondition", err, status.Code(err))
	}

	// A retained old tombstone must never turn a stop for that old generation
	// into permission to tear down a newer assignment that reused the test ID.
	service.mu.Lock()
	service.executionGeneration = 8
	service.executionExpiresAt = time.Now().Add(time.Minute)
	service.assignments["run"] = assignedTest{testID: "run", executionGeneration: 8, vuserCount: 1, groups: map[string]int32{"buyers": 1}}
	service.mu.Unlock()
	_, err = service.StopTest(context.Background(), &pb.StopRequest{TestId: "run", ExecutionGeneration: 7})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("old tombstone stop against newer assignment = %v (%s), want FailedPrecondition", err, status.Code(err))
	}
	service.mu.RLock()
	newer := service.assignments["run"]
	service.mu.RUnlock()
	if newer.executionGeneration != 8 {
		t.Fatalf("newer assignment was stopped by old tombstone: %#v", newer)
	}
}

func TestTerminalTombstoneRecoversFromFinalizedArchiveAfterRestart(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	const generatorID = "terminal-restart-generator"
	const testID = "terminal-restart-run"
	first := NewService(generatorID, 10)
	claim := &pb.ExecutionEpochClaim{ExecutionGeneration: 9, LeaseTtlMilliseconds: 60_000, ExecutionLeaseOwnerId: "controller/9"}
	if ack, err := first.ClaimExecutionEpoch(context.Background(), claim); err != nil || !ack.GetAccepted() {
		t.Fatalf("claim first execution epoch = (%#v, %v)", ack, err)
	}
	namespace, namespaceErr := first.archiveManager.namespaceSnapshot()
	if namespaceErr != nil {
		t.Fatal(namespaceErr)
	}
	writer, err := newArchiveRecorderInNamespace(namespace, testID, "Restart run")
	if err != nil {
		t.Fatalf("new archive recorder: %v", err)
	}
	writer.bindExecution(9, "buyers")
	state, finalError := first.finishStoppedTestWithState(testID, assignedTest{
		testID: testID, executionGeneration: 9, groups: map[string]int32{"buyers": 4},
		groupStats:     map[string]GeneratorStats{"buyers": {CPUPercent: 17.5}},
		groupTelemetry: map[string]MetricsTelemetry{"buyers": {WindowMs: 1000}},
		groupAggregates: map[string]metricsAggregateSet{"buyers": {
			Fleet:        &MetricAggregate{Name: "fleet", Count: 10, Errors: 2, P95Ms: 321},
			Transactions: []MetricAggregate{{Name: "checkout", Count: 10, Errors: 2, P95Ms: 321}},
			Groups:       []GroupMetric{{Name: "buyers", Metrics: &MetricAggregate{Name: "buyers", Count: 10, Errors: 2, P95Ms: 321}}},
		}},
	}, writer, "FAILED", "recovered failure")
	if state != "FAILED" || finalError != "recovered failure" {
		t.Fatalf("first terminal state = %q/%q", state, finalError)
	}

	restarted := NewService(generatorID, 10)
	if ack, err := restarted.ClaimExecutionEpoch(context.Background(), claim); err != nil || !ack.GetAccepted() {
		t.Fatalf("revalidate restarted execution epoch = (%#v, %v)", ack, err)
	}
	id, _, initial, err := restarted.registerScopedSubscriber(9, testID)
	if err != nil {
		t.Fatalf("register restarted subscriber: %v", err)
	}
	defer restarted.unregisterSubscriber(id)
	if len(initial) != 1 {
		t.Fatalf("restarted initial batches = %#v", initial)
	}
	batch := initial[0]
	if batch.GetLifecycleState() != "FAILED" || batch.GetLifecycleError() != "recovered failure" || batch.GetExecutionGeneration() != 9 {
		t.Fatalf("restarted terminal batch = %#v", batch)
	}
	if len(batch.GetGroups()) != 1 || batch.GetGroups()[0].GetName() != "buyers" || batch.GetGroups()[0].GetActiveVusers() != 0 {
		t.Fatalf("restarted terminal groups = %#v", batch.GetGroups())
	}
	if batch.GetFleetMetrics().GetCount() != 10 || batch.GetFleetMetrics().GetErrors() != 2 || batch.GetFleetMetrics().GetP95Ms() != 321 {
		t.Fatalf("restarted fleet aggregate = %#v", batch.GetFleetMetrics())
	}
	if len(batch.GetTransactionMetrics()) != 1 || batch.GetTransactionMetrics()[0].GetName() != "checkout" || batch.GetTransactionMetrics()[0].GetCount() != 10 {
		t.Fatalf("restarted transaction aggregates = %#v", batch.GetTransactionMetrics())
	}
}
