package generatorrpc

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	pb "github.com/jasonczech/apexload/api/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const defaultMaxMetricsSubscribers = 4096

const metricsSubscriberQueueCapacity = 16

type terminalBatchKey struct {
	testID     string
	generation uint64
}

type terminalMetricsTombstone struct {
	batch       *pb.MetricsBatch
	completedAt time.Time
}

type metricsSubscriber struct {
	generation uint64
	testID     string
	mu         sync.Mutex
	queue      []*pb.MetricsBatch
	terminal   map[terminalBatchKey]struct{}
	wake       chan struct{}
	fenced     chan struct{}
	fenceOnce  sync.Once
	overflowed atomic.Bool
}

func (s *metricsSubscriber) enqueue(batch *pb.MetricsBatch) int64 {
	dropped, _ := s.enqueueAttributed(batch)
	return dropped
}

// enqueueAttributed queues batch and reports how many batches were dropped
// and the test they belonged to.
func (s *metricsSubscriber) enqueueAttributed(batch *pb.MetricsBatch) (int64, string) {
	if s == nil || batch == nil {
		return 0, ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	terminal := isGeneratorTerminalLifecycle(batch.GetLifecycleState())
	key := terminalBatchKey{testID: batch.GetTestId(), generation: batch.GetExecutionGeneration()}
	if terminal {
		if s.terminal == nil {
			s.terminal = make(map[terminalBatchKey]struct{})
		}
		s.terminal[key] = struct{}{}
		// Coalesce a repeated terminal event for the same generation. Tombstone
		// replay makes retries routine and terminal state is immutable.
		for index, queued := range s.queue {
			if queued.GetTestId() == batch.GetTestId() && queued.GetExecutionGeneration() == batch.GetExecutionGeneration() && isGeneratorTerminalLifecycle(queued.GetLifecycleState()) {
				s.queue[index] = batch
				s.signalLocked()
				return 0, ""
			}
		}
		if len(s.queue) >= metricsSubscriberQueueCapacity {
			// Preserve the oldest contiguous prefix so the consumer establishes
			// the true sequence baseline. Dropping the newest live window makes
			// the subsequent terminal sequence expose an observable gap; dropping
			// the oldest would let a fresh consumer silently baseline after loss.
			for index := len(s.queue) - 1; index >= 0; index-- {
				queued := s.queue[index]
				if isGeneratorTerminalLifecycle(queued.GetLifecycleState()) {
					continue
				}
				evicted := queued.GetTestId()
				copy(s.queue[index:], s.queue[index+1:])
				s.queue[len(s.queue)-1] = nil
				s.queue = s.queue[:len(s.queue)-1]
				s.queue = append(s.queue, batch)
				s.signalLocked()
				return 1, evicted
			}
			// Every terminal batch has already been retained as a broker tombstone.
			// Fence a subscriber that cannot keep up with a terminal-only backlog;
			// reconnecting replays retained state without growing this queue.
			s.overflowed.Store(true)
			s.fence()
			return 1, batch.GetTestId()
		}
		s.queue = append(s.queue, batch)
		s.signalLocked()
		return 0, ""
	}
	if _, terminalSeen := s.terminal[key]; terminalSeen {
		// A terminal lifecycle is immutable for a test generation. A delayed
		// nonterminal publisher must never regress a live stream.
		return 0, ""
	}
	if len(s.queue) >= metricsSubscriberQueueCapacity {
		return 1, batch.GetTestId()
	}
	s.queue = append(s.queue, batch)
	s.signalLocked()
	return 0, ""
}

func (s *metricsSubscriber) signalLocked() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *metricsSubscriber) pop() *pb.MetricsBatch {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) == 0 {
		return nil
	}
	batch := s.queue[0]
	s.queue[0] = nil
	s.queue = s.queue[1:]
	if len(s.queue) > 0 {
		s.signalLocked()
	}
	return batch
}

func (s *metricsSubscriber) fence() {
	if s == nil {
		return
	}
	s.fenceOnce.Do(func() { close(s.fenced) })
}

// metricsBroker owns metrics subscriptions, publication ordering, subscriber
// backpressure accounting, and terminal replay state. publishMu serializes the
// lifecycle event stream; mu protects broker-owned indexes and retention data.
// No callback is invoked while mu is held.
type metricsBroker struct {
	publishMu sync.Mutex
	mu        sync.RWMutex

	subscribers           map[int]*metricsSubscriber
	subscriberIDsByTest   map[string]map[int]struct{}
	unscopedSubscriberIDs map[int]struct{}
	maxSubscribers        int
	nextSubscriberID      int

	terminalBatches map[terminalBatchKey]terminalMetricsTombstone
	// finalizingBatches lives only while the corresponding stopped execution
	// owns an archive finalizer. Terminal publication removes it. Unlike a
	// terminal tombstone, this phase never reports a completed outcome.
	finalizingBatches map[terminalBatchKey]*pb.MetricsBatch
	batchSequences    map[terminalBatchKey]uint64
	// windowPublished marks a test generation that has published a
	// sequenced metric window; before it, every sequence was state-only.
	windowPublished map[terminalBatchKey]bool
	terminalMax     int
	terminalTTL     time.Duration

	droppedBatches atomic.Int64
	// droppedByTest attributes dropped batches to their test.
	droppedByTestMu sync.Mutex
	droppedByTest   map[string]int64
	lifecycleHook   func(*pb.MetricsBatch)
}

func newMetricsBroker(maxSubscribers, terminalMax int, terminalTTL time.Duration) *metricsBroker {
	if maxSubscribers <= 0 {
		maxSubscribers = defaultMaxMetricsSubscribers
	}
	if terminalMax <= 0 {
		terminalMax = defaultGeneratorArchiveMax
	}
	return &metricsBroker{
		subscribers:           make(map[int]*metricsSubscriber),
		subscriberIDsByTest:   make(map[string]map[int]struct{}),
		unscopedSubscriberIDs: make(map[int]struct{}),
		maxSubscribers:        maxSubscribers,
		terminalBatches:       make(map[terminalBatchKey]terminalMetricsTombstone),
		finalizingBatches:     make(map[terminalBatchKey]*pb.MetricsBatch),
		batchSequences:        make(map[terminalBatchKey]uint64),
		windowPublished:       make(map[terminalBatchKey]bool),
		terminalMax:           terminalMax,
		terminalTTL:           terminalTTL,
	}
}

func (broker *metricsBroker) configureSubscriberLimit(limit int) {
	if broker == nil || limit <= 0 {
		return
	}
	broker.mu.Lock()
	broker.maxSubscribers = limit
	broker.mu.Unlock()
}

func (broker *metricsBroker) configureTerminalRetention(limit int, ttl time.Duration) {
	if broker == nil {
		return
	}
	if limit <= 0 {
		limit = defaultGeneratorArchiveMax
	}
	broker.mu.Lock()
	broker.terminalMax = limit
	broker.terminalTTL = ttl
	broker.pruneTerminalLocked(time.Now())
	broker.mu.Unlock()
}

func (broker *metricsBroker) lockPublication() {
	broker.publishMu.Lock()
}

func (broker *metricsBroker) unlockPublication() {
	broker.publishMu.Unlock()
}

func (broker *metricsBroker) setLifecycleHook(hook func(*pb.MetricsBatch)) {
	if broker == nil {
		return
	}
	broker.publishMu.Lock()
	broker.lifecycleHook = hook
	broker.publishMu.Unlock()
}

func (broker *metricsBroker) register(executionGeneration uint64, testID string, live []*pb.MetricsBatch, now time.Time) (int, *metricsSubscriber, []*pb.MetricsBatch, error) {
	if broker == nil {
		return 0, nil, nil, status.Error(codes.Unavailable, "metrics broker is unavailable")
	}
	broker.publishMu.Lock()
	defer broker.publishMu.Unlock()
	return broker.registerLocked(executionGeneration, testID, live, now)
}

// registerLocked installs a subscriber and returns immutable initial snapshots.
// The caller holds publishMu so a lifecycle event cannot fall between its live
// assignment snapshot and subscriber installation.
func (broker *metricsBroker) registerLocked(executionGeneration uint64, testID string, live []*pb.MetricsBatch, now time.Time) (int, *metricsSubscriber, []*pb.MetricsBatch, error) {
	broker.mu.Lock()
	defer broker.mu.Unlock()
	broker.pruneTerminalLocked(now)
	if len(broker.subscribers) >= broker.maxSubscribers {
		return 0, nil, nil, status.Errorf(codes.ResourceExhausted, "metrics subscriber limit exceeded (%d)", broker.maxSubscribers)
	}

	id := broker.nextSubscriberID
	broker.nextSubscriberID++
	subscriber := &metricsSubscriber{
		generation: executionGeneration,
		testID:     testID,
		queue:      make([]*pb.MetricsBatch, 0, metricsSubscriberQueueCapacity),
		wake:       make(chan struct{}, 1),
		fenced:     make(chan struct{}),
	}
	broker.subscribers[id] = subscriber
	if testID == "" {
		broker.unscopedSubscriberIDs[id] = struct{}{}
	} else {
		if broker.subscriberIDsByTest[testID] == nil {
			broker.subscriberIDsByTest[testID] = make(map[int]struct{})
		}
		broker.subscriberIDsByTest[testID][id] = struct{}{}
	}

	initial := make([]*pb.MetricsBatch, 0, len(live)+len(broker.terminalBatches))
	for _, batch := range live {
		if batch == nil || batch.GetExecutionGeneration() != executionGeneration || (testID != "" && batch.GetTestId() != testID) {
			continue
		}
		clone := proto.Clone(batch).(*pb.MetricsBatch)
		key := terminalBatchKey{testID: clone.GetTestId(), generation: clone.GetExecutionGeneration()}
		if current := broker.batchSequences[key]; current > 0 {
			// The live assignment snapshot is rebuilt from current engine state and
			// therefore has no publication sequence of its own. Anchor it to the
			// latest published window so a reconnect cannot reset the controller's
			// gap-detection baseline to zero.
			clone.BatchSequence = current
			// Sequences numbered before any metric window (staging and
			// activation) carried nothing a late subscriber could lose.
			clone.SequencePrefixStateOnly = !broker.windowPublished[key]
		}
		if clone.GetSourceEpoch() == "" {
			clone.SourceEpoch, clone.SourceEpochStartedUnixMs = processSourceEpoch.id, processSourceEpoch.startedUnixMs
		}
		initial = append(initial, clone)
	}
	for key, batch := range broker.finalizingBatches {
		if key.generation == executionGeneration && (testID == "" || key.testID == testID) && batch != nil {
			initial = append(initial, proto.Clone(batch).(*pb.MetricsBatch))
		}
	}
	for key, tombstone := range broker.terminalBatches {
		if key.generation == executionGeneration && (testID == "" || key.testID == testID) && tombstone.batch != nil {
			initial = append(initial, proto.Clone(tombstone.batch).(*pb.MetricsBatch))
		}
	}
	sort.Slice(initial, func(i, j int) bool {
		if initial[i].GetTimestampUnixMs() == initial[j].GetTimestampUnixMs() {
			return initial[i].GetTestId() < initial[j].GetTestId()
		}
		return initial[i].GetTimestampUnixMs() < initial[j].GetTimestampUnixMs()
	})
	return id, subscriber, initial, nil
}

func (broker *metricsBroker) unregister(id int) {
	if broker == nil {
		return
	}
	broker.mu.Lock()
	defer broker.mu.Unlock()
	subscriber, ok := broker.subscribers[id]
	if !ok {
		return
	}
	delete(broker.subscribers, id)
	if subscriber.testID == "" {
		delete(broker.unscopedSubscriberIDs, id)
	} else if ids := broker.subscriberIDsByTest[subscriber.testID]; ids != nil {
		delete(ids, id)
		if len(ids) == 0 {
			delete(broker.subscriberIDsByTest, subscriber.testID)
		}
	}
}

func (broker *metricsBroker) publish(batch *pb.MetricsBatch, accept func(*pb.MetricsBatch) bool) {
	if broker == nil || batch == nil {
		return
	}
	broker.publishMu.Lock()
	defer broker.publishMu.Unlock()
	broker.publishLocked(batch, accept)
}

func (broker *metricsBroker) publishLifecycleLocked(batch *pb.MetricsBatch, accept func(*pb.MetricsBatch) bool) {
	if broker == nil || batch == nil {
		return
	}
	if broker.lifecycleHook != nil {
		broker.lifecycleHook(batch)
	}
	broker.publishOwnedLocked(batch, accept)
}

// publishLocked transfers ownership of batch to the broker. Metrics batches are
// freshly constructed publication events; callers must not mutate them after
// this call. Retained tombstones and replay snapshots are cloned at their true
// retention/external boundaries instead of charging every live publication.
func (broker *metricsBroker) publishLocked(batch *pb.MetricsBatch, accept func(*pb.MetricsBatch) bool) {
	if broker == nil || batch == nil {
		return
	}
	broker.publishOwnedLocked(batch, accept)
}

func (broker *metricsBroker) publishOwnedLocked(batch *pb.MetricsBatch, accept func(*pb.MetricsBatch) bool) {
	if batch == nil || (accept != nil && !accept(batch)) {
		return
	}
	broker.assignBatchSequenceLocked(batch)
	broker.mu.RLock()
	testIDs := broker.subscriberIDsByTest[batch.GetTestId()]
	subscribers := make([]*metricsSubscriber, 0, len(testIDs)+len(broker.unscopedSubscriberIDs))
	for id := range testIDs {
		if subscriber := broker.subscribers[id]; subscriber != nil {
			subscribers = append(subscribers, subscriber)
		}
	}
	for id := range broker.unscopedSubscriberIDs {
		if subscriber := broker.subscribers[id]; subscriber != nil {
			subscribers = append(subscribers, subscriber)
		}
	}
	broker.mu.RUnlock()

	for _, subscriber := range subscribers {
		if subscriber.generation != batch.GetExecutionGeneration() || (subscriber.testID != "" && subscriber.testID != batch.GetTestId()) {
			continue
		}
		if dropped, testID := subscriber.enqueueAttributed(batch); dropped > 0 {
			broker.droppedBatches.Add(dropped)
			broker.droppedByTestMu.Lock()
			if broker.droppedByTest == nil {
				broker.droppedByTest = map[string]int64{}
			}
			broker.droppedByTest[testID] += dropped
			broker.droppedByTestMu.Unlock()
		}
	}
}

// processSourceEpoch identifies this generator process. A restarted process
// starts a new epoch whose batch sequences begin again at one.
var processSourceEpoch = struct {
	id            string
	startedUnixMs int64
}{id: uuid.NewString(), startedUnixMs: time.Now().UnixMilli()}

// metricsCollections numbers this process's metric collections, so within a
// source epoch a batch that re-sends a window can be told from a new one.
var metricsCollections atomic.Uint64

func nextMetricsCollection() uint64 { return metricsCollections.Add(1) }

// assignBatchSequenceLocked runs under publishMu and gives every newly
// published window a stable position in its execution-generation stream.
// Terminal state ends the sequence's lifetime; the retained terminal clone
// carries its assigned value for reconnecting subscribers.
func (broker *metricsBroker) assignBatchSequenceLocked(batch *pb.MetricsBatch) {
	if broker == nil || batch == nil {
		return
	}
	// Sequences are numbered by this process, so they belong to its epoch.
	if batch.GetSourceEpoch() == "" {
		batch.SourceEpoch, batch.SourceEpochStartedUnixMs = processSourceEpoch.id, processSourceEpoch.startedUnixMs
	}
	key := terminalBatchKey{testID: batch.GetTestId(), generation: batch.GetExecutionGeneration()}
	if batch.GetBatchSequence() != 0 {
		if isGeneratorTerminalLifecycle(batch.GetLifecycleState()) {
			delete(broker.batchSequences, key)
			delete(broker.windowPublished, key)
		}
		return
	}
	next := broker.batchSequences[key] + 1
	if next == 0 {
		next = 1
	}
	batch.BatchSequence = next
	if isGeneratorTerminalLifecycle(batch.GetLifecycleState()) {
		delete(broker.batchSequences, key)
		delete(broker.windowPublished, key)
	} else {
		broker.batchSequences[key] = next
		if batch.GetWindowMs() > 0 || len(batch.GetTransactions()) > 0 {
			broker.windowPublished[key] = true
		}
	}
}

// publishFinalizing retains explicit completion-phase evidence until the
// durable terminal outcome exists. Publication and reconnect snapshots share
// one immutable sequence; a stale stopping generation cannot replace it.
func (broker *metricsBroker) publishFinalizing(batch *pb.MetricsBatch, accept func(*pb.MetricsBatch) bool) {
	if broker == nil || batch == nil || batch.GetLifecycleState() != MetricsLifecycleFinalizing {
		return
	}
	broker.publishMu.Lock()
	defer broker.publishMu.Unlock()
	if accept == nil || !accept(batch) {
		return
	}
	clone := proto.Clone(batch).(*pb.MetricsBatch)
	key := terminalBatchKey{testID: clone.GetTestId(), generation: clone.GetExecutionGeneration()}
	broker.mu.RLock()
	prior, terminal := broker.finalizingBatches[key], broker.terminalBatches[key].batch
	broker.mu.RUnlock()
	if terminal != nil {
		return
	}
	if prior != nil {
		return // The completed collection cannot change during finalization.
	}
	broker.assignBatchSequenceLocked(clone)
	broker.mu.Lock()
	broker.finalizingBatches[key] = clone
	broker.mu.Unlock()
	broker.publishOwnedLocked(clone, nil)
}

func (broker *metricsBroker) publishTerminal(batch *pb.MetricsBatch) {
	broker.publishTerminalAt(batch, time.Now())
}

func (broker *metricsBroker) publishTerminalAt(batch *pb.MetricsBatch, now time.Time) {
	if broker == nil || batch == nil || !isGeneratorTerminalLifecycle(batch.GetLifecycleState()) {
		return
	}
	broker.publishMu.Lock()
	defer broker.publishMu.Unlock()
	clone := proto.Clone(batch).(*pb.MetricsBatch)
	key := terminalBatchKey{testID: clone.GetTestId(), generation: clone.GetExecutionGeneration()}
	// A terminal tombstone is the immutable sequence identity for this
	// execution generation. Routine completion retries may reconstruct a fresh
	// zero-sequence batch after the live counter has been reclaimed; reusing the
	// retained sequence prevents a retry from regressing to sequence 1.
	broker.mu.RLock()
	retained := broker.terminalBatches[key]
	broker.mu.RUnlock()
	if retained.batch != nil && retained.batch.GetBatchSequence() > 0 {
		clone.BatchSequence = retained.batch.GetBatchSequence()
	}
	broker.assignBatchSequenceLocked(clone)
	completedAt := time.UnixMilli(clone.GetTimestampUnixMs())
	if clone.GetTimestampUnixMs() <= 0 {
		completedAt = now
	}
	broker.mu.Lock()
	broker.terminalBatches[key] = terminalMetricsTombstone{batch: clone, completedAt: completedAt}
	delete(broker.finalizingBatches, key)
	broker.pruneTerminalLocked(now)
	broker.mu.Unlock()
	broker.publishOwnedLocked(clone, nil)
}

func (broker *metricsBroker) pruneTerminalLocked(now time.Time) {
	if broker.terminalTTL > 0 {
		for key, tombstone := range broker.terminalBatches {
			if !tombstone.completedAt.IsZero() && now.Sub(tombstone.completedAt) > broker.terminalTTL {
				delete(broker.terminalBatches, key)
			}
		}
	}
	limit := broker.terminalMax
	if limit <= 0 {
		limit = defaultGeneratorArchiveMax
	}
	for len(broker.terminalBatches) > limit {
		var oldestKey terminalBatchKey
		var oldestAt time.Time
		found := false
		for key, tombstone := range broker.terminalBatches {
			if !found || tombstone.completedAt.Before(oldestAt) || (tombstone.completedAt.Equal(oldestAt) && (key.testID < oldestKey.testID || key.testID == oldestKey.testID && key.generation < oldestKey.generation)) {
				oldestKey = key
				oldestAt = tombstone.completedAt
				found = true
			}
		}
		if !found {
			break
		}
		delete(broker.terminalBatches, oldestKey)
	}
}

func (broker *metricsBroker) restoreTerminal(batch *pb.MetricsBatch, completedAt time.Time) {
	if broker == nil || batch == nil || !isGeneratorTerminalLifecycle(batch.GetLifecycleState()) {
		return
	}
	clone := proto.Clone(batch).(*pb.MetricsBatch)
	// The terminal is retained before it is published, so a restored one has
	// no sequence and no epoch. This process replays it under its own epoch,
	// unsequenced; an empty epoch would read as an older generator.
	if clone.GetSourceEpoch() == "" {
		clone.SourceEpoch, clone.SourceEpochStartedUnixMs = processSourceEpoch.id, processSourceEpoch.startedUnixMs
	}
	key := terminalBatchKey{testID: clone.GetTestId(), generation: clone.GetExecutionGeneration()}
	broker.mu.Lock()
	broker.terminalBatches[key] = terminalMetricsTombstone{batch: clone, completedAt: completedAt}
	broker.mu.Unlock()
}

func (broker *metricsBroker) hasTerminal(key terminalBatchKey) bool {
	if broker == nil {
		return false
	}
	broker.mu.RLock()
	tombstone := broker.terminalBatches[key]
	broker.mu.RUnlock()
	return tombstone.batch != nil
}

// terminalState returns the retained terminal lifecycle state for one exact
// test generation.
func (broker *metricsBroker) terminalState(testID string, generation uint64) (string, bool) {
	if broker == nil {
		return "", false
	}
	broker.mu.RLock()
	defer broker.mu.RUnlock()
	tombstone, ok := broker.terminalBatches[terminalBatchKey{testID: testID, generation: generation}]
	if !ok || tombstone.batch == nil {
		return "", false
	}
	return tombstone.batch.GetLifecycleState(), true
}

func (broker *metricsBroker) terminalGeneration(testID string) (uint64, bool) {
	if broker == nil {
		return 0, false
	}
	broker.mu.RLock()
	defer broker.mu.RUnlock()
	var generation uint64
	found := false
	for key := range broker.terminalBatches {
		if key.testID == testID && (!found || key.generation > generation) {
			generation = key.generation
			found = true
		}
	}
	return generation, found
}

func (broker *metricsBroker) deleteTerminalsForTest(testID string) {
	if broker == nil {
		return
	}
	// Publication owns batchSequences and always acquires publishMu before mu.
	// Keep that order here so an execution removed without a terminal batch does
	// not leave one sequence counter resident forever.
	broker.publishMu.Lock()
	defer broker.publishMu.Unlock()
	broker.mu.Lock()
	for key := range broker.terminalBatches {
		if key.testID == testID {
			delete(broker.terminalBatches, key)
		}
	}
	for key := range broker.finalizingBatches {
		if key.testID == testID {
			delete(broker.finalizingBatches, key)
		}
	}
	broker.mu.Unlock()
	broker.droppedByTestMu.Lock()
	delete(broker.droppedByTest, testID)
	broker.droppedByTestMu.Unlock()
	for key := range broker.batchSequences {
		if key.testID == testID {
			delete(broker.batchSequences, key)
		}
	}
	for key := range broker.windowPublished {
		if key.testID == testID {
			delete(broker.windowPublished, key)
		}
	}
}

func (broker *metricsBroker) terminalBatch(key terminalBatchKey) *pb.MetricsBatch {
	if broker == nil {
		return nil
	}
	broker.mu.RLock()
	tombstone := broker.terminalBatches[key]
	broker.mu.RUnlock()
	if tombstone.batch == nil {
		return nil
	}
	return proto.Clone(tombstone.batch).(*pb.MetricsBatch)
}

func (broker *metricsBroker) droppedCount() int64 {
	if broker == nil {
		return 0
	}
	return broker.droppedBatches.Load()
}

// droppedCountForTest is the number of testID's batches dropped for slow
// subscribers in this process.
func (broker *metricsBroker) droppedCountForTest(testID string) int64 {
	if broker == nil {
		return 0
	}
	broker.droppedByTestMu.Lock()
	defer broker.droppedByTestMu.Unlock()
	return broker.droppedByTest[testID]
}

func (broker *metricsBroker) subscriberCount() int {
	if broker == nil {
		return 0
	}
	broker.mu.RLock()
	count := len(broker.subscribers)
	broker.mu.RUnlock()
	return count
}

func (broker *metricsBroker) fenceBeforeGeneration(generation uint64) {
	if broker == nil {
		return
	}
	broker.mu.RLock()
	subscribers := make([]*metricsSubscriber, 0)
	for _, subscriber := range broker.subscribers {
		if subscriber != nil && subscriber.generation < generation {
			subscribers = append(subscribers, subscriber)
		}
	}
	broker.mu.RUnlock()
	for _, subscriber := range subscribers {
		subscriber.fence()
	}
}

func (broker *metricsBroker) fenceGeneration(generation uint64) {
	if broker == nil {
		return
	}
	broker.mu.RLock()
	subscribers := make([]*metricsSubscriber, 0)
	for _, subscriber := range broker.subscribers {
		if subscriber != nil && subscriber.generation == generation {
			subscribers = append(subscribers, subscriber)
		}
	}
	broker.mu.RUnlock()
	for _, subscriber := range subscribers {
		subscriber.fence()
	}
}
