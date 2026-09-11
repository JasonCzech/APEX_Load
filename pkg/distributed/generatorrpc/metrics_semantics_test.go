package generatorrpc

import (
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestMergeGeneratorStatsSumsOnlyActiveVUsers(t *testing.T) {
	left := GeneratorStats{
		ActiveVUsers: 2,
		CPUPercent:   40,
		MemoryBytes:  100,
		GCCountDelta: 3,
	}
	right := GeneratorStats{
		ActiveVUsers: 5,
		CPUPercent:   60,
		MemoryBytes:  90,
		GCCountDelta: 2,
	}

	got := mergeGeneratorStats(left, right)
	if got.ActiveVUsers != 7 {
		t.Fatalf("active VUsers = %d, want 7", got.ActiveVUsers)
	}
	if got.CPUPercent != 60 || got.MemoryBytes != 100 || got.GCCountDelta != 3 {
		t.Fatalf("process gauges were summed: %#v", got)
	}
}

func TestCollectExecutionMetricsPublishesFreshIdleWindow(t *testing.T) {
	service := &Service{publishInterval: time.Second}
	execution := &groupExecution{lastMetrics: &executionMetricsSnapshot{
		transactions: []TransactionMetric{{Name: "old"}},
		aggregates: metricsAggregateSet{
			Transactions: []MetricAggregate{{}},
		},
		telemetry: MetricsTelemetry{WindowMs: 250},
	}}

	got, ok := service.collectExecutionMetrics(execution)
	if !ok {
		t.Fatal("idle window was omitted")
	}
	if len(got.transactions) != 0 || metricsAggregateSetHasEvidence(got.aggregates) {
		t.Fatalf("idle window replayed old evidence: %#v", got)
	}
	if got.telemetry.WindowMs != 1000 {
		t.Fatalf("idle window = %dms, want 1000ms", got.telemetry.WindowMs)
	}
}

func TestCollectExecutionMetricsRetainsCumulativeSLAWithoutFreshEngineSnapshot(t *testing.T) {
	service := &Service{publishInterval: time.Second}
	priorRows := []engine.TransactionResponseTimeSLACompliance{{
		Group: "buyers", Transaction: "Checkout", ThresholdMs: 3000,
		TotalCount: 8, EligibleSuccessCount: 8, MissedCount: 1,
	}}
	execution := &groupExecution{
		lastMetrics: &executionMetricsSnapshot{transactionResponseTimeSLA: priorRows},
		results: &transactionBuffer{
			transactions: []TransactionMetric{{Group: "buyers", Name: "Checkout", DurationMs: 2000, Success: true}},
			lastDrainAt:  time.Now().Add(-time.Second),
		},
	}

	got, ok := service.collectExecutionMetricsWithIdle(execution, false)
	if !ok {
		t.Fatal("transaction window was suppressed")
	}
	if len(got.transactionResponseTimeSLA) != 1 || got.transactionResponseTimeSLA[0].TotalCount != 8 {
		t.Fatalf("cumulative SLA rows = %#v, want retained prior row", got.transactionResponseTimeSLA)
	}

	priorRows[0].TotalCount = 99
	if execution.lastMetrics.transactionResponseTimeSLA[0].TotalCount != 8 {
		t.Fatal("stored SLA rows alias caller-owned memory")
	}
}

func TestForcedDrainIgnoresGaugeAndSubscriberOnlyChanges(t *testing.T) {
	sourceAt := time.Now().Add(-time.Second)
	previous := &executionMetricsSnapshot{
		stats: GeneratorStats{GoroutineCount: -1000},
		telemetry: MetricsTelemetry{
			DroppedSubscriberBatches: 4,
		},
		sourceSnapshotAt:    sourceAt,
		sourceSnapshotValid: true,
	}
	currentStats := previous.stats
	currentStats.GoroutineCount = 1000
	changes := classifyExecutionMetricsChanges(
		previous,
		engine.MetricsSnapshot{Timestamp: sourceAt},
		true,
		currentStats,
		5,
		false,
	)
	if changes.sourceSnapshot || !changes.processStats || !changes.subscriberTelemetry {
		t.Fatalf("change classification = %#v, want gauge/subscriber-only churn", changes)
	}
	if changes.shouldPublish(false, true) {
		t.Fatal("forced drain treated gauge/subscriber-only churn as execution evidence")
	}
	if !changes.shouldPublish(true, true) {
		t.Fatal("periodic publisher suppressed gauge/subscriber telemetry cadence")
	}

	service := &Service{
		publishInterval: time.Second,
		metricsBroker:   newMetricsBroker(1, 1, time.Minute),
	}
	service.metricsBroker.droppedBatches.Store(5)
	execution := &groupExecution{
		lastMetrics: previous,
		results:     &transactionBuffer{lastDrainAt: time.Now().Add(-time.Second)},
	}
	if got, ok := service.collectExecutionMetricsWithIdle(execution, false); ok {
		t.Fatalf("forced gauge-only drain published empty metrics: %#v", got)
	}
	periodic, ok := service.collectExecutionMetricsWithIdle(execution, true)
	if !ok {
		t.Fatal("periodic gauge-only window was suppressed")
	}
	if periodic.telemetry.DroppedSubscriberBatches != 5 {
		t.Fatalf("periodic dropped batches = %d, want 5", periodic.telemetry.DroppedSubscriberBatches)
	}
	if periodic.stats.GoroutineCount == previous.stats.GoroutineCount {
		t.Fatalf("periodic goroutine gauge remained stale: %#v", periodic.stats)
	}
}

func TestArchivePublisherIsDeterministicPerTest(t *testing.T) {
	service := &Service{executions: map[string]map[string]*groupExecution{
		"run": {"search": {}, "checkout": {}},
	}}
	if !service.isArchivePublisherLocked("run", "checkout") {
		t.Fatal("lexically first active group was not selected")
	}
	if service.isArchivePublisherLocked("run", "search") {
		t.Fatal("second group would duplicate the per-test archive interval")
	}
}

func TestPerTestPublisherDrainsStaggeredGroupEvidenceExactlyOnce(t *testing.T) {
	service := newIsolatedService(t, 10)
	const generation = uint64(1)
	alpha := &groupExecution{
		groupName: "alpha", executionGeneration: generation,
		results: &transactionBuffer{lastDrainAt: time.Now().Add(-time.Second)},
	}
	beta := &groupExecution{
		groupName: "beta", executionGeneration: generation,
		results: &transactionBuffer{lastDrainAt: time.Now().Add(-time.Second)},
	}
	service.mu.Lock()
	service.assignments["run"] = assignedTest{
		committed:           true,
		executionGeneration: generation,
		groups:              map[string]int32{"alpha": 1, "beta": 1},
		activeGroups:        map[string]int32{"alpha": 1, "beta": 1},
	}
	service.executions["run"] = map[string]*groupExecution{"alpha": alpha, "beta": beta}
	service.mu.Unlock()

	subscriberID, subscriber, _, err := service.metricsBroker.register(generation, "run", nil, time.Now())
	if err != nil {
		t.Fatalf("register metrics subscriber: %v", err)
	}
	defer service.metricsBroker.unregister(subscriberID)
	record := func(execution *groupExecution, groupName, actionName string) {
		t.Helper()
		if err := execution.results.RecordTransaction(&protocol.Result{
			ActionName: actionName,
			Group:      groupName,
			Duration:   10 * time.Millisecond,
			Success:    true,
		}); err != nil {
			t.Fatalf("RecordTransaction(%s): %v", actionName, err)
		}
	}
	counts := make(map[string]int)
	collect := func() {
		t.Helper()
		batch := subscriber.pop()
		if batch == nil {
			t.Fatal("coordinated publisher emitted no metrics batch")
		}
		for _, transaction := range batch.GetTransactions() {
			counts[transaction.GetName()]++
		}
	}

	// Beta's ticker can fire between two beta transactions. Because the
	// non-publisher does not drain independently, evidence from both sides of that
	// stagger remains available to the one coordinated fleet publication.
	record(alpha, "alpha", "alpha-1")
	record(beta, "beta", "beta-1")
	service.publishTestExecutionSnapshot("run", "beta", beta, true)
	if batch := subscriber.pop(); batch != nil {
		t.Fatalf("non-publisher group emitted a batch: %#v", batch)
	}
	record(beta, "beta", "beta-2")
	service.publishTestExecutionSnapshot("run", "alpha", alpha, true)
	collect()

	// A later beta-only window appears once, and the following idle tick must not
	// replay any of the retained diagnostic samples.
	record(beta, "beta", "beta-3")
	service.publishTestExecutionSnapshot("run", "alpha", alpha, true)
	collect()
	service.publishTestExecutionSnapshot("run", "alpha", alpha, true)
	collect()

	for _, name := range []string{"alpha-1", "beta-1", "beta-2", "beta-3"} {
		if counts[name] != 1 {
			t.Fatalf("transaction %q published %d times, want exactly once (all counts %#v)", name, counts[name], counts)
		}
	}
}

func TestSimultaneousGroupFinalDrainsPublishCoordinatedEvidenceOnce(t *testing.T) {
	service := newIsolatedService(t, 10)
	const generation = uint64(1)
	alpha := &groupExecution{
		groupName: "alpha", executionGeneration: generation,
		results: &transactionBuffer{lastDrainAt: time.Now().Add(-time.Second)},
	}
	beta := &groupExecution{
		groupName: "beta", executionGeneration: generation,
		results: &transactionBuffer{lastDrainAt: time.Now().Add(-time.Second)},
	}
	for execution, result := range map[*groupExecution]*protocol.Result{
		alpha: {ActionName: "alpha-final", Group: "alpha", Duration: 10 * time.Millisecond, Success: true},
		beta:  {ActionName: "beta-final", Group: "beta", Duration: 20 * time.Millisecond, Success: true},
	} {
		if err := execution.results.RecordTransaction(result); err != nil {
			t.Fatalf("record final transaction: %v", err)
		}
	}
	service.mu.Lock()
	service.assignments["run"] = assignedTest{
		committed:           true,
		executionGeneration: generation,
		groups:              map[string]int32{"alpha": 1, "beta": 1},
		activeGroups:        map[string]int32{"alpha": 1, "beta": 1},
	}
	service.executions["run"] = map[string]*groupExecution{"alpha": alpha, "beta": beta}
	service.mu.Unlock()

	subscriberID, subscriber, _, err := service.metricsBroker.register(generation, "run", nil, time.Now())
	if err != nil {
		t.Fatalf("register metrics subscriber: %v", err)
	}
	defer service.metricsBroker.unregister(subscriberID)

	start := make(chan struct{})
	var drains sync.WaitGroup
	for groupName, execution := range map[string]*groupExecution{"alpha": alpha, "beta": beta} {
		drains.Add(1)
		go func() {
			defer drains.Done()
			<-start
			// This is the forced coordinated drain each group's completion callback
			// performs after joining its periodic publisher.
			service.publishExecutionSnapshot("run", groupName, execution)
		}()
	}
	close(start)
	drains.Wait()

	batch := subscriber.pop()
	if batch == nil {
		t.Fatal("simultaneous final drains emitted no coordinated metrics batch")
	}
	counts := map[string]int{}
	for _, transaction := range batch.GetTransactions() {
		counts[transaction.GetName()]++
	}
	if counts["alpha-final"] != 1 || counts["beta-final"] != 1 {
		t.Fatalf("coordinated final evidence = %#v, want each transaction once", counts)
	}
	if duplicate := subscriber.pop(); duplicate != nil {
		t.Fatalf("second final callback duplicated the coordinated window: %#v", duplicate)
	}
}

func TestAlphaFinalDrainClearsStaleBetaWindowEvidence(t *testing.T) {
	service := newIsolatedService(t, 10)
	const generation = uint64(1)
	alpha := &groupExecution{
		groupName: "alpha", executionGeneration: generation,
		results: &transactionBuffer{lastDrainAt: time.Now().Add(-time.Second)},
	}
	beta := &groupExecution{
		groupName: "beta", executionGeneration: generation,
		results: &transactionBuffer{lastDrainAt: time.Now().Add(-time.Second)},
	}
	if err := alpha.results.RecordTransaction(&protocol.Result{
		ActionName: "alpha-final", Group: "alpha", Duration: 10 * time.Millisecond, Success: true,
	}); err != nil {
		t.Fatalf("record alpha final transaction: %v", err)
	}
	service.mu.Lock()
	service.assignments["run"] = assignedTest{
		committed:           true,
		executionGeneration: generation,
		groups:              map[string]int32{"alpha": 1, "beta": 1},
		activeGroups:        map[string]int32{"alpha": 1, "beta": 1},
		groupTransactions: map[string][]TransactionMetric{
			"beta": {{Name: "beta-stale", Group: "beta", Success: true}},
		},
		groupAggregates: map[string]metricsAggregateSet{
			"beta": {Transactions: []MetricAggregate{{Name: "beta-stale", Count: 1}}},
		},
	}
	service.executions["run"] = map[string]*groupExecution{"alpha": alpha, "beta": beta}
	service.mu.Unlock()

	subscriberID, subscriber, _, err := service.metricsBroker.register(generation, "run", nil, time.Now())
	if err != nil {
		t.Fatalf("register metrics subscriber: %v", err)
	}
	defer service.metricsBroker.unregister(subscriberID)

	service.publishExecutionSnapshot("run", "alpha", alpha)
	batch := subscriber.pop()
	if batch == nil {
		t.Fatal("alpha final drain emitted no metrics batch")
	}
	for _, transaction := range batch.GetTransactions() {
		if transaction.GetName() == "beta-stale" || transaction.GetGroup() == "beta" {
			t.Fatalf("alpha final batch re-emitted stale beta transaction: %#v", transaction)
		}
	}
	for _, aggregate := range batch.GetTransactionMetrics() {
		if aggregate.GetName() == "beta-stale" {
			t.Fatalf("alpha final batch re-emitted stale beta aggregate: %#v", aggregate)
		}
	}
	if len(batch.GetTransactions()) != 1 || batch.GetTransactions()[0].GetName() != "alpha-final" {
		t.Fatalf("alpha final transactions = %#v, want only alpha-final", batch.GetTransactions())
	}
}

func TestAlphaFinalDrainClearsStaleRemovedBetaWindowEvidence(t *testing.T) {
	service := newIsolatedService(t, 10)
	const generation = uint64(1)
	alpha := &groupExecution{
		groupName: "alpha", executionGeneration: generation,
		results: &transactionBuffer{lastDrainAt: time.Now().Add(-time.Second)},
	}
	if err := alpha.results.RecordTransaction(&protocol.Result{
		ActionName: "alpha-after-beta", Group: "alpha", Duration: 10 * time.Millisecond, Success: true,
	}); err != nil {
		t.Fatalf("record alpha final transaction: %v", err)
	}
	service.mu.Lock()
	service.assignments["run"] = assignedTest{
		committed:           true,
		executionGeneration: generation,
		groups:              map[string]int32{"alpha": 1, "beta": 1},
		activeGroups:        map[string]int32{"alpha": 1, "beta": 0},
		groupTransactions: map[string][]TransactionMetric{
			"beta": {{Name: "beta-removed-stale", Group: "beta", Success: true}},
		},
		groupAggregates: map[string]metricsAggregateSet{
			"beta": {Transactions: []MetricAggregate{{Name: "beta-removed-stale", Count: 1}}},
		},
	}
	// finishExecutionMatching removes a completed group from this map while its
	// latest gauge state remains in the assignment. Window evidence must not
	// survive that removal into a later sibling publication.
	service.executions["run"] = map[string]*groupExecution{"alpha": alpha}
	service.mu.Unlock()

	subscriberID, subscriber, _, err := service.metricsBroker.register(generation, "run", nil, time.Now())
	if err != nil {
		t.Fatalf("register metrics subscriber: %v", err)
	}
	defer service.metricsBroker.unregister(subscriberID)

	service.publishExecutionSnapshot("run", "alpha", alpha)
	batch := subscriber.pop()
	if batch == nil {
		t.Fatal("alpha final drain emitted no metrics batch")
	}
	for _, transaction := range batch.GetTransactions() {
		if transaction.GetName() == "beta-removed-stale" || transaction.GetGroup() == "beta" {
			t.Fatalf("alpha final batch re-emitted removed beta transaction: %#v", transaction)
		}
	}
	for _, aggregate := range batch.GetTransactionMetrics() {
		if aggregate.GetName() == "beta-removed-stale" {
			t.Fatalf("alpha final batch re-emitted removed beta aggregate: %#v", aggregate)
		}
	}
	if len(batch.GetTransactions()) != 1 || batch.GetTransactions()[0].GetName() != "alpha-after-beta" {
		t.Fatalf("alpha final transactions = %#v, want only alpha-after-beta", batch.GetTransactions())
	}
}
