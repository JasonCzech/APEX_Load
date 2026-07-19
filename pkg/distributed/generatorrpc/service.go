package generatorrpc

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/buildinfo"
	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

type cachedScript struct {
	content  []byte
	hash     string
	protocol string
	lastUsed uint64
}

type assignedTest struct {
	testID                 string
	executionGeneration    uint64
	vuserCount             int32
	paused                 bool
	pauseOperationID       string
	pauseOperationSequence uint64
	duration               time.Duration
	durationSeconds        int64
	committed              bool
	groups                 map[string]int32
	activeGroups           map[string]int32
	groupStats             map[string]GeneratorStats
	groupTelemetry         map[string]MetricsTelemetry
	groupTransactions      map[string][]TransactionMetric
	groupAggregates        map[string]metricsAggregateSet
	groupAssignments       map[string][sha256.Size]byte
	groupSecretBundleIDs   map[string]string
	groupScripts           map[string]string
	terminalError          string
}

const defaultAssignmentStageTTL = 2 * time.Minute

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

type stopCompletion struct {
	generation uint64
	done       chan struct{}
}

const (
	defaultMaxArchiveTransfers      = 64
	defaultMaxArchiveUnaryTransfers = 4
	defaultMaxHeartbeatStreams      = 256
	defaultMaxArchiveFinalizers     = 4
)

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
	if s == nil || batch == nil {
		return 0
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
				return 0
			}
		}
		if len(s.queue) >= metricsSubscriberQueueCapacity {
			for index, queued := range s.queue {
				if isGeneratorTerminalLifecycle(queued.GetLifecycleState()) {
					continue
				}
				copy(s.queue[index:], s.queue[index+1:])
				s.queue[len(s.queue)-1] = nil
				s.queue = s.queue[:len(s.queue)-1]
				s.queue = append(s.queue, batch)
				s.signalLocked()
				return 1
			}
			// Every terminal batch has already been retained as a service tombstone.
			// Fence a subscriber that cannot keep up with a terminal-only backlog;
			// reconnecting replays the complete retained state without allowing this
			// per-stream queue to grow without bound.
			s.overflowed.Store(true)
			s.fence()
			return 1
		}
		s.queue = append(s.queue, batch)
		s.signalLocked()
		return 0
	}
	if _, terminalSeen := s.terminal[key]; terminalSeen {
		// A terminal lifecycle is immutable for a test generation. A delayed
		// nonterminal publisher must never make a live stream regress after that
		// terminal event has been retained and enqueued.
		return 0
	}
	if len(s.queue) >= metricsSubscriberQueueCapacity {
		return 1
	}
	s.queue = append(s.queue, batch)
	s.signalLocked()
	return 0
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

type ServiceOption func(*Service)

type groupExecution struct {
	groupName           string
	executionGeneration uint64
	engine              *engine.MultiGroupEngine
	// durationOffset is the logical steady-test time that had already elapsed
	// when a late evacuation replacement was staged. Duration controls carry the
	// run's total target, so this offset keeps a replacement's local deadline
	// aligned with groups that started at the original fleet barrier.
	durationOffset  time.Duration
	redactionConfig *engine.RedactionConfig
	cancel          context.CancelFunc
	started         atomic.Bool
	startAt         time.Time
	stagedDoneOnce  sync.Once
	results         *transactionBuffer
	publisherDone   chan struct{}
	done            chan struct{}
	metricsMu       sync.Mutex
	lastMetrics     *executionMetricsSnapshot
}

type executionMetricsSnapshot struct {
	stats        GeneratorStats
	transactions []TransactionMetric
	telemetry    MetricsTelemetry
	aggregates   metricsAggregateSet
}

const (
	maxGeneratorDurationSeconds        = int64(^uint64(0)>>1) / int64(time.Second)
	maxGeneratorVUsers                 = int64(^uint32(0) >> 1)
	maxGeneratorGroupNameBytes         = 512
	maxGeneratorScriptIDBytes          = 512
	maxGeneratorHashBytes              = 128
	maxGeneratorOperationIDBytes       = 512
	maxAssignmentEnvironmentEntries    = 1024
	maxAssignmentEnvironmentBytes      = 1 << 20
	maxAssignmentEnvironmentKeyBytes   = 256
	maxAssignmentEnvironmentValueBytes = 64 << 10
)

func validateGeneratorFieldBytes(value string, field string, limit int) error {
	if len(value) > limit {
		return status.Errorf(codes.InvalidArgument, "%s exceeds %d bytes", field, limit)
	}
	return nil
}

func validateAssignmentMetadataBounds(assignment *pb.WorkAssignment) error {
	if err := validateGeneratorFieldBytes(assignment.GetGroupName(), "group_name", maxGeneratorGroupNameBytes); err != nil {
		return err
	}
	if err := validateGeneratorFieldBytes(assignment.GetScriptId(), "script_id", maxGeneratorScriptIDBytes); err != nil {
		return err
	}
	if err := validateGeneratorFieldBytes(assignment.GetExpectedScriptHash(), "expected_script_hash", maxGeneratorHashBytes); err != nil {
		return err
	}
	if err := validateGeneratorFieldBytes(assignment.GetSecretBundleId(), "secret_bundle_id", maxGeneratorHashBytes); err != nil {
		return err
	}
	if len(assignment.GetDataFiles()) > maxAssignmentDataFiles {
		return status.Errorf(codes.InvalidArgument, "data_files exceeds %d entries", maxAssignmentDataFiles)
	}
	if len(assignment.GetRedactionRules()) > engine.MaxRedactionRules {
		return status.Errorf(codes.InvalidArgument, "redaction_rules exceeds %d entries", engine.MaxRedactionRules)
	}
	redactionBytes := 0
	for index, rule := range assignment.GetRedactionRules() {
		if rule == nil {
			return status.Errorf(codes.InvalidArgument, "redaction_rules[%d] is nil", index)
		}
		if len(rule.GetName()) > engine.MaxRedactionRuleNameBytes || len(rule.GetPattern()) > engine.MaxRedactionPatternBytes || len(rule.GetReplacement()) > engine.MaxRedactionReplacementBytes {
			return status.Errorf(codes.InvalidArgument, "redaction_rules[%d] exceeds runtime field limits", index)
		}
		redactionBytes += len(rule.GetName()) + len(rule.GetPattern()) + len(rule.GetReplacement())
		if redactionBytes > engine.MaxRedactionPolicyBytes {
			return status.Errorf(codes.InvalidArgument, "redaction_rules exceeds %d bytes", engine.MaxRedactionPolicyBytes)
		}
	}
	if len(assignment.GetEnvironment()) > maxAssignmentEnvironmentEntries {
		return status.Errorf(codes.InvalidArgument, "environment exceeds %d entries", maxAssignmentEnvironmentEntries)
	}
	environmentBytes := 0
	for key, value := range assignment.GetEnvironment() {
		if len(key) > maxAssignmentEnvironmentKeyBytes || len(value) > maxAssignmentEnvironmentValueBytes {
			return status.Error(codes.InvalidArgument, "environment key or value exceeds generator limits")
		}
		environmentBytes += len(key) + len(value)
		if environmentBytes > maxAssignmentEnvironmentBytes {
			return status.Errorf(codes.InvalidArgument, "environment exceeds %d bytes", maxAssignmentEnvironmentBytes)
		}
	}
	if err := ValidateSecretBundle(assignment.GetSecretBundle()); err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	if len(assignment.GetSecretBundle()) > 0 && strings.TrimSpace(assignment.GetSecretBundleId()) == "" {
		return status.Error(codes.InvalidArgument, "secret_bundle_id is required for secret-bearing assignments")
	}
	if len(assignment.GetSecretBundle()) == 0 && strings.TrimSpace(assignment.GetSecretBundleId()) != "" {
		return status.Error(codes.InvalidArgument, "secret_bundle_id requires a secret bundle")
	}
	return nil
}

func validatedGeneratorDuration(seconds int64, field string, allowZero bool) (time.Duration, error) {
	if seconds < 0 || (!allowZero && seconds == 0) {
		return 0, fmt.Errorf("%s must be positive", field)
	}
	if seconds > maxGeneratorDurationSeconds {
		return 0, fmt.Errorf("%s exceeds maximum %d", field, maxGeneratorDurationSeconds)
	}
	return time.Duration(seconds) * time.Second, nil
}

func validatedAssignmentDuration(precise *durationpb.Duration, legacySeconds int64, field string, allowZero bool) (time.Duration, error) {
	if precise == nil {
		return validatedGeneratorDuration(legacySeconds, field, allowZero)
	}
	if err := precise.CheckValid(); err != nil {
		return 0, fmt.Errorf("%s is invalid: %w", field, err)
	}
	duration := precise.AsDuration()
	if duration < 0 || (!allowZero && duration == 0) {
		return 0, fmt.Errorf("%s must be positive", field)
	}
	return duration, nil
}

func assignedTestDuration(assignment assignedTest) time.Duration {
	if assignment.duration != 0 {
		return assignment.duration
	}
	duration, _ := validatedGeneratorDuration(assignment.durationSeconds, "target_duration", true)
	return duration
}

type transactionBuffer struct {
	mu           sync.Mutex
	transactions []TransactionMetric
	window       *transactionWindowAggregate
	lastDrainAt  time.Time
}

type metricsAggregateSet struct {
	Fleet           *MetricAggregate
	Transactions    []MetricAggregate
	Dependencies    []MetricAggregate
	SOAP            []MetricAggregate
	NetworkProfiles []MetricAggregate
	Browser         *BrowserMetrics
	BrowserByDevice []BrowserDeviceMetrics
	Groups          []GroupMetric
}

type transactionWindowAggregate struct {
	fleet        *metricAccumulator
	transactions map[string]*metricAccumulator
	dependencies map[string]*metricAccumulator
	soap         map[string]*metricAccumulator
	groups       map[string]*metricAccumulator
}

type metricAccumulator struct {
	name             string
	dependencyType   string
	dependencyTarget string
	count            int64
	errors           int64
	durationSumMs    float64
	dnsSumMs         float64
	connectSumMs     float64
	tlsSumMs         float64
	ttfbSumMs        float64
	bytesSent        int64
	bytesReceived    int64
	digest           *engine.TDigest
}

const (
	maxTransactionWindowSeries    = 256
	maxRetainedTransactionSamples = 4096
	metricOverflowSeries          = "[other]"
)

// windowSecondsFromMs converts a batch window in milliseconds to seconds for
// rate (TPS, bytes/sec) computation. A non-positive window — e.g. an older
// generator that does not emit window_ms, or the very first drain — falls back
// to 1.0s, preserving the historical count≈rate behavior at the 1s publish cadence.
func windowSecondsFromMs(windowMs int64) float64 {
	if windowMs <= 0 {
		return 1.0
	}
	return float64(windowMs) / 1000.0
}

// effectiveBatchWindowMs returns the window (ms) that makes totalSuccess/window
// equal the additive fleet rate — the sum over groups of (groupSuccess /
// groupWindow). Rate consumers divide the batch's summed success by its single
// window, so when a pod hosts multiple groups with unequal windows (transiently,
// during ramp/stagger/wind-down) the max window would mis-weight TPS; this
// effective window corrects it. With one group or equal windows it equals that
// window. Falls back to fallbackMs when there are no successes (rate is 0 anyway).
func effectiveBatchWindowMs(a assignedTest, fallbackMs int64) int64 {
	totalSuccess := 0
	additiveTPS := 0.0
	for group, aggregate := range a.groupAggregates {
		success := int64(0)
		if aggregate.Fleet != nil {
			success = aggregate.Fleet.Count - aggregate.Fleet.Errors
		}
		if success == 0 {
			continue
		}
		windowMs := fallbackMs
		if t, ok := a.groupTelemetry[group]; ok && t.WindowMs > 0 {
			windowMs = t.WindowMs
		}
		totalSuccess += int(success)
		additiveTPS += float64(success) / windowSecondsFromMs(windowMs)
	}
	if totalSuccess == 0 || additiveTPS <= 0 {
		return fallbackMs
	}
	return int64(float64(totalSuccess) / additiveTPS * 1000.0)
}

func newTransactionWindowAggregate() *transactionWindowAggregate {
	return &transactionWindowAggregate{
		fleet:        newMetricAccumulator("fleet"),
		transactions: make(map[string]*metricAccumulator),
		dependencies: make(map[string]*metricAccumulator),
		soap:         make(map[string]*metricAccumulator),
		groups:       make(map[string]*metricAccumulator),
	}
}

func newMetricAccumulator(name string) *metricAccumulator {
	return &metricAccumulator{name: name, digest: engine.NewTDigest(200)}
}

func (a *transactionWindowAggregate) add(tx TransactionMetric) {
	if a == nil {
		return
	}
	a.fleet.add(tx)
	if name := strings.TrimSpace(tx.Name); name != "" {
		acc := boundedWindowAccumulator(a.transactions, name)
		acc.add(tx)
	}
	if target := strings.TrimSpace(tx.DependencyTarget); target != "" {
		acc := boundedWindowAccumulator(a.dependencies, target)
		if acc.dependencyTarget == "" {
			acc.dependencyTarget = acc.name
		}
		acc.add(tx)
	}
	if operation := strings.TrimSpace(tx.SOAPOperation); operation != "" {
		acc := boundedWindowAccumulator(a.soap, operation)
		acc.add(tx)
	}
	if groupName := strings.TrimSpace(tx.Group); groupName != "" {
		acc := boundedWindowAccumulator(a.groups, groupName)
		acc.add(tx)
	}
}

func boundedWindowAccumulator(values map[string]*metricAccumulator, name string) *metricAccumulator {
	if existing := values[name]; existing != nil {
		return existing
	}
	if len(values) >= maxTransactionWindowSeries-1 {
		name = metricOverflowSeries
		if existing := values[name]; existing != nil {
			return existing
		}
	}
	accumulator := newMetricAccumulator(name)
	values[name] = accumulator
	return accumulator
}

// boundRetainedTransactionSamples limits the diagnostic transaction samples
// retained by one assignment. Aggregate metrics remain complete; these raw
// entries are only a bounded evidence sample. Prioritize the group that just
// published so high group cardinality cannot permanently starve new samples.
func boundRetainedTransactionSamples(values map[string][]TransactionMetric, priorityGroup string) {
	if len(values) == 0 {
		return
	}
	remaining := maxRetainedTransactionSamples
	retain := func(groupName string) {
		samples, ok := values[groupName]
		if !ok {
			return
		}
		if remaining == 0 {
			delete(values, groupName)
			return
		}
		if len(samples) > remaining {
			// Copy the retained tail so the discarded prefix and its potentially
			// large evidence objects are no longer kept alive by the backing array.
			samples = append([]TransactionMetric(nil), samples[len(samples)-remaining:]...)
			values[groupName] = samples
		}
		remaining -= len(samples)
	}

	retain(priorityGroup)
	for groupName := range values {
		if groupName != priorityGroup {
			retain(groupName)
		}
	}
}

func (a *transactionWindowAggregate) empty() bool {
	return a == nil || a.fleet == nil || a.fleet.count == 0
}

func (a *transactionWindowAggregate) finalize(windowMs int64) metricsAggregateSet {
	if a.empty() {
		return metricsAggregateSet{}
	}
	windowSec := windowSecondsFromMs(windowMs)
	return metricsAggregateSet{
		Fleet:        a.fleet.finalize(windowSec),
		Transactions: finalizeMetricAccumulatorMap(a.transactions, windowSec),
		Dependencies: finalizeMetricAccumulatorMap(a.dependencies, windowSec),
		SOAP:         finalizeMetricAccumulatorMap(a.soap, windowSec),
		Groups:       finalizeGroupAccumulatorMap(a.groups, windowSec),
	}
}

func (a *metricAccumulator) add(tx TransactionMetric) {
	if a == nil {
		return
	}
	a.count++
	if !tx.Success {
		a.errors++
	}
	mergeStableString(&a.dependencyType, tx.DependencyType)
	mergeStableString(&a.dependencyTarget, tx.DependencyTarget)
	a.durationSumMs += tx.DurationMs
	a.dnsSumMs += tx.DNSMs
	a.connectSumMs += tx.ConnectMs
	a.tlsSumMs += tx.TLSMs
	a.ttfbSumMs += tx.TTFBMs
	a.bytesSent += tx.BytesSent
	a.bytesReceived += tx.BytesReceived
	a.digest.Add(tx.DurationMs * float64(time.Millisecond))
}

func (a *metricAccumulator) finalize(windowSec float64) *MetricAggregate {
	if a == nil || a.count == 0 {
		return nil
	}
	digestSnapshot := a.digest.Snapshot()
	successes := a.count - a.errors
	count := float64(a.count)
	return &MetricAggregate{
		Name:             a.name,
		DependencyType:   a.dependencyType,
		DependencyTarget: a.dependencyTarget,
		Count:            a.count,
		Errors:           a.errors,
		AvgMs:            a.durationSumMs / count,
		P50Ms:            a.digest.Quantile(0.50) / float64(time.Millisecond),
		P90Ms:            a.digest.Quantile(0.90) / float64(time.Millisecond),
		P95Ms:            a.digest.Quantile(0.95) / float64(time.Millisecond),
		P99Ms:            a.digest.Quantile(0.99) / float64(time.Millisecond),
		AvgDNSMs:         a.dnsSumMs / count,
		AvgConnectMs:     a.connectSumMs / count,
		AvgTLSMs:         a.tlsSumMs / count,
		AvgTTFBMs:        a.ttfbSumMs / count,
		BytesSent:        a.bytesSent,
		BytesReceived:    a.bytesReceived,
		TPS:              float64(successes) / windowSec,
		DurationDigest:   &digestSnapshot,
	}
}

func finalizeMetricAccumulatorMap(values map[string]*metricAccumulator, windowSec float64) []MetricAggregate {
	if len(values) == 0 {
		return nil
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]MetricAggregate, 0, len(names))
	for _, name := range names {
		if aggregate := values[name].finalize(windowSec); aggregate != nil {
			out = append(out, *aggregate)
		}
	}
	return out
}

func finalizeGroupAccumulatorMap(values map[string]*metricAccumulator, windowSec float64) []GroupMetric {
	if len(values) == 0 {
		return nil
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]GroupMetric, 0, len(names))
	for _, name := range names {
		if aggregate := values[name].finalize(windowSec); aggregate != nil {
			out = append(out, GroupMetric{Name: name, Metrics: aggregate})
		}
	}
	return out
}

func mergeStableString(current *string, next string) {
	if current == nil || next == "" {
		return
	}
	if *current == "" {
		*current = next
		return
	}
	if *current != next {
		*current = "multiple"
	}
}

func (b *transactionBuffer) RecordResult(result *protocol.Result) error {
	if result == nil || !result.BusinessTransaction {
		return nil
	}
	return b.RecordTransaction(result)
}

func (b *transactionBuffer) RecordTransaction(result *protocol.Result) error {
	if result == nil {
		return nil
	}
	tx := TransactionMetric{
		Name:             result.ActionName,
		StartTimeUnixMs:  resultStartTimeUnixMs(result.StartTime),
		Group:            result.Group,
		DurationMs:       float64(result.Duration) / float64(time.Millisecond),
		DNSMs:            float64(result.DNSTime) / float64(time.Millisecond),
		ConnectMs:        float64(result.ConnectTime) / float64(time.Millisecond),
		TLSMs:            float64(result.TLSTime) / float64(time.Millisecond),
		TTFBMs:           float64(result.TTFB) / float64(time.Millisecond),
		StatusCode:       int32(result.StatusCode),
		Success:          result.Success,
		ErrorMessage:     result.ErrorMessage,
		BytesSent:        result.BytesSent,
		BytesReceived:    result.BytesReceived,
		CorrelationID:    result.CorrelationID,
		DependencyType:   result.DependencyType,
		DependencyTarget: result.DependencyTarget,
		SOAPOperation:    strings.TrimSpace(result.Extractions["soap_operation"]),
		ServerTelemetry:  cloneServerTelemetry(result.ServerTelemetry),
		Snapshot:         cloneErrorSnapshot(result.Snapshot),
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.window == nil {
		b.window = newTransactionWindowAggregate()
	}
	b.window.add(tx)
	b.transactions = append(b.transactions, tx)
	if len(b.transactions) > 1024 {
		copy(b.transactions, b.transactions[len(b.transactions)-1024:])
		b.transactions = b.transactions[:1024]
	}
	return nil
}

func (b *transactionBuffer) Drain() []TransactionMetric {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.transactions) == 0 {
		return nil
	}
	out := append([]TransactionMetric(nil), b.transactions...)
	b.transactions = nil
	return out
}

// DrainWindow drains the buffered transactions and returns the elapsed
// wall-clock window since the previous drain, computed under the buffer lock so
// it is race-free against concurrent publishers (the periodic ticker and the
// final post-run publish). The first drain reports the window since the buffer
// was created (see newTransactionBuffer).
func (b *transactionBuffer) DrainWindow() ([]TransactionMetric, *transactionWindowAggregate, time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	var window time.Duration
	if !b.lastDrainAt.IsZero() {
		window = now.Sub(b.lastDrainAt)
	}
	b.lastDrainAt = now
	aggregate := b.window
	b.window = nil
	if len(b.transactions) == 0 {
		return nil, aggregate, window
	}
	out := append([]TransactionMetric(nil), b.transactions...)
	b.transactions = nil
	return out, aggregate, window
}

type Service struct {
	pb.UnimplementedGeneratorControlServer

	mu                          sync.RWMutex
	adjustMu                    sync.Mutex
	generatorID                 string
	maxVUsers                   int32
	maxAssignedTests            int
	maxAssignedGroups           int
	maxExecutions               int
	maxArchiveWriters           int
	maxArchiveTransfers         int
	maxArchiveUnaryTransfers    int
	maxHeartbeatStreams         int
	activeArchiveTransfers      int
	activeArchiveUnaryTransfers int
	activeHeartbeatStreams      int
	podResources                podResourceCapacity
	scripts                     map[string]cachedScript
	assignments                 map[string]assignedTest
	executions                  map[string]map[string]*groupExecution
	archiveWriters              map[string]*archiveRecorder
	archiveFinalizerSlots       chan struct{}
	archiveRecords              map[string]*storage.RunArchiveRecord
	archiveIdentities           map[string]os.FileInfo
	archiveLeases               map[string]int
	archiveDeleting             map[string]struct{}
	stoppingTests               map[string]struct{}
	stoppingGenerations         map[string]uint64
	stopCompletions             map[string]*stopCompletion
	terminalBatches             map[terminalBatchKey]terminalMetricsTombstone
	metricsPublishMu            sync.Mutex
	lifecyclePublishHook        func(*pb.MetricsBatch)
	scriptCacheMax              int
	scriptCacheBytes            int64
	scriptCacheLimit            int64
	archiveRecordMax            int
	archiveTTL                  time.Duration
	archiveNamespace            *generatorArchiveNamespace
	archiveNamespaceErr         error
	scriptUseCounter            uint64
	resCollector                *engine.ResourceCollector
	resCollectorMu              sync.Mutex // serializes resCollector.Collect (mutates delta state)
	executionEnabled            bool
	draining                    bool
	publishInterval             time.Duration
	completionTimers            map[string]*time.Timer
	completionTokens            map[string]uint64
	nextCompletionToken         uint64
	stageTimers                 map[string]*time.Timer
	stageTokens                 map[string]uint64
	nextStageToken              uint64
	assignmentStageTTL          time.Duration
	subscribers                 map[int]*metricsSubscriber
	subscriberIDsByTest         map[string]map[int]struct{}
	unscopedSubscriberIDs       map[int]struct{}
	maxMetricsSubscribers       int
	nextSubID                   int
	executionGeneration         uint64
	executionExpiresAt          time.Time
	executionEpochTimer         *time.Timer
	executionEpochFencing       bool
	executionFencePending       int
	executionEpochState         *executionEpochStateStore
	executionEpochStateErr      error
	executionEpochStatePath     string
	executionLeaseOwner         string
	// executionRestartRevalidation is set only while reconstructing a service
	// from a positive durable high-water. It permits one exact lease-owner claim
	// to restore the volatile expiry lost with the process. Ordinary timer expiry
	// never sets it, so an expired live session remains non-revivable.
	executionRestartRevalidation bool
	readinessChanged             chan struct{}
	droppedBatches               atomic.Int64
	shutdown                     chan struct{}
	shutdownOnce                 sync.Once
	finalizerWG                  sync.WaitGroup
	finalizerMu                  sync.Mutex
	finalizerClosing             bool
}

func (s *Service) addFinalizer() bool {
	s.finalizerMu.Lock()
	defer s.finalizerMu.Unlock()
	if s.finalizerClosing {
		return false
	}
	s.finalizerWG.Add(1)
	return true
}

func (s *Service) SetDraining(draining bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.draining == draining {
		return
	}
	s.draining = draining
	s.notifyReadinessChangedLocked()
}

func (s *Service) ActiveVUsers() int32 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.activeVUsersLocked()
}

// ReadinessError reports whether the service can enforce its durable execution
// fence. A generator must not advertise readiness when this state is missing or
// unreadable: accepting generation-zero work in that condition could revive a
// controller term that was fenced before the generator restarted.
func (s *Service) ReadinessError() error {
	if s == nil {
		return errors.New("generator service is unavailable")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.draining {
		return errors.New("generator is draining")
	}
	if s.executionEpochStateErr != nil {
		return fmt.Errorf("durable execution epoch state: %w", s.executionEpochStateErr)
	}
	if s.executionEpochState == nil {
		return errors.New("durable execution epoch state is unavailable")
	}
	if s.archiveNamespaceErr != nil {
		return fmt.Errorf("generator archive namespace: %w", s.archiveNamespaceErr)
	}
	if s.archiveNamespace == nil || s.archiveNamespace.root == nil {
		return errors.New("generator archive namespace is unavailable")
	}
	return nil
}

// ReadinessChanges is a level-trigger hint for process health publishers. A
// consumer must call ReadinessError after every notification (and once before
// waiting). The buffered channel preserves a state transition that happens
// before the health watcher begins receiving.
func (s *Service) ReadinessChanges() <-chan struct{} {
	if s == nil {
		return nil
	}
	return s.readinessChanged
}

func (s *Service) notifyReadinessChangedLocked() {
	if s == nil || s.readinessChanged == nil {
		return
	}
	select {
	case s.readinessChanged <- struct{}{}:
	default:
	}
}

func WithExecution() ServiceOption {
	return func(s *Service) {
		s.executionEnabled = true
	}
}

// WithAssignmentStageTTL bounds how long validated, capacity-reserving work may
// remain uncommitted. Production defaults to a two-minute controller-crash
// recovery window.
func WithAssignmentStageTTL(ttl time.Duration) ServiceOption {
	return func(s *Service) {
		if ttl > 0 {
			s.assignmentStageTTL = ttl
		}
	}
}

func WithGeneratorMetricsSubscriberLimit(limit int) ServiceOption {
	return func(s *Service) {
		if limit > 0 {
			s.maxMetricsSubscribers = limit
		}
	}
}

// WithGeneratorControlStreamLimits bounds authenticated but potentially
// duplicated controller streams. It is primarily useful for constrained
// deployments and deterministic admission tests.
func WithGeneratorControlStreamLimits(archiveTransfers, archiveUnaryTransfers, heartbeatStreams int) ServiceOption {
	return func(s *Service) {
		if archiveTransfers > 0 {
			s.maxArchiveTransfers = archiveTransfers
		}
		if archiveUnaryTransfers > 0 {
			s.maxArchiveUnaryTransfers = archiveUnaryTransfers
		}
		if heartbeatStreams > 0 {
			s.maxHeartbeatStreams = heartbeatStreams
		}
	}
}

// WithExecutionEpochStatePath overrides the durable generator fencing-state
// file. It is primarily useful for isolated tests and non-Kubernetes launches;
// production deployments should mount APEXLOAD_GENERATOR_STATE_DIR durably.
func WithExecutionEpochStatePath(path string) ServiceOption {
	return func(s *Service) {
		s.executionEpochStatePath = strings.TrimSpace(path)
	}
}

func NewService(generatorID string, maxVUsers int, opts ...ServiceOption) *Service {
	if generatorID == "" {
		generatorID = "apexload-generator"
	}
	if maxVUsers <= 0 {
		maxVUsers = 10000
	}
	if int64(maxVUsers) > maxGeneratorVUsers {
		maxVUsers = int(maxGeneratorVUsers)
	}

	archiveNamespace, archiveNamespaceErr := prepareGeneratorArchiveNamespace(generatorID, true)
	service := &Service{
		generatorID:              generatorID,
		maxVUsers:                int32(maxVUsers),
		maxAssignedTests:         1024,
		maxAssignedGroups:        4096,
		maxExecutions:            4096,
		maxArchiveWriters:        1024,
		maxArchiveTransfers:      defaultMaxArchiveTransfers,
		maxArchiveUnaryTransfers: defaultMaxArchiveUnaryTransfers,
		maxHeartbeatStreams:      defaultMaxHeartbeatStreams,
		podResources:             detectPodResourceCapacity(int32(maxVUsers)),
		scripts:                  make(map[string]cachedScript),
		assignments:              make(map[string]assignedTest),
		executions:               make(map[string]map[string]*groupExecution),
		archiveWriters:           make(map[string]*archiveRecorder),
		archiveFinalizerSlots:    make(chan struct{}, defaultMaxArchiveFinalizers),
		archiveRecords:           make(map[string]*storage.RunArchiveRecord),
		archiveIdentities:        make(map[string]os.FileInfo),
		archiveLeases:            make(map[string]int),
		archiveDeleting:          make(map[string]struct{}),
		stoppingTests:            make(map[string]struct{}),
		stoppingGenerations:      make(map[string]uint64),
		stopCompletions:          make(map[string]*stopCompletion),
		terminalBatches:          make(map[terminalBatchKey]terminalMetricsTombstone),
		scriptCacheMax:           configuredGeneratorScriptCacheMax(),
		scriptCacheLimit:         configuredGeneratorScriptCacheMaxBytes(),
		archiveRecordMax:         configuredGeneratorArchiveMax(),
		archiveTTL:               configuredGeneratorArchiveTTL(),
		archiveNamespace:         archiveNamespace,
		archiveNamespaceErr:      archiveNamespaceErr,
		resCollector:             engine.NewResourceCollector(),
		publishInterval:          time.Second,
		completionTimers:         make(map[string]*time.Timer),
		completionTokens:         make(map[string]uint64),
		stageTimers:              make(map[string]*time.Timer),
		stageTokens:              make(map[string]uint64),
		assignmentStageTTL:       defaultAssignmentStageTTL,
		subscribers:              make(map[int]*metricsSubscriber),
		subscriberIDsByTest:      make(map[string]map[int]struct{}),
		unscopedSubscriberIDs:    make(map[int]struct{}),
		maxMetricsSubscribers:    defaultMaxMetricsSubscribers,
		readinessChanged:         make(chan struct{}, 1),
		shutdown:                 make(chan struct{}),
	}
	if archiveNamespaceErr != nil {
		slog.Error("initialize generator archive namespace", "generator_id", generatorID, "error", archiveNamespaceErr)
	}
	for _, opt := range opts {
		if opt != nil {
			opt(service)
		}
	}
	service.initializeExecutionEpochState()
	if service.executionEpochStateErr != nil {
		slog.Error("initialize durable generator execution epoch", "generator_id", generatorID, "error", service.executionEpochStateErr)
	}
	service.sweepGeneratorArchiveDirectories(time.Now())
	return service
}

// Shutdown stops active work, waits for engine goroutines to quiesce so their
// archive sinks cannot race finalization, and releases long-lived metrics
// streams so grpc.GracefulStop has a bounded path to completion.
func (s *Service) Shutdown(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.SetDraining(true)
	s.mu.RLock()
	testIDs := make([]string, 0, len(s.assignments))
	for testID := range s.assignments {
		testIDs = append(testIDs, testID)
	}
	s.mu.RUnlock()
	sort.Strings(testIDs)
	var failures []error
	// Detach and cancel the whole fleet before waiting for any one engine. A
	// protocol that is slow to honor cancellation must not leave later tests
	// generating load until the process-level timeout expires.
	if err := s.forceStopTests(ctx, testIDs, ^uint64(0), "FAILED", "generator shut down before test completion"); err != nil {
		failures = append(failures, fmt.Errorf("stop active generator tests: %w", err))
	}
	s.finalizerMu.Lock()
	s.finalizerClosing = true
	s.finalizerMu.Unlock()
	s.shutdownOnce.Do(func() { close(s.shutdown) })
	s.mu.Lock()
	if s.executionEpochTimer != nil {
		s.executionEpochTimer.Stop()
		s.executionEpochTimer = nil
	}
	s.mu.Unlock()
	done := make(chan struct{})
	go func() { s.finalizerWG.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		failures = append(failures, fmt.Errorf("wait for generator finalizers: %w", ctx.Err()))
	}
	if s.executionEpochState != nil {
		if err := s.executionEpochState.close(); err != nil {
			failures = append(failures, fmt.Errorf("close execution epoch state: %w", err))
		}
	}
	return errors.Join(failures...)
}

func (s *Service) DistributeScript(ctx context.Context, payload *pb.ScriptPayload) (*pb.ScriptAck, error) {
	if payload.GetScriptId() == "" {
		return nil, status.Error(codes.InvalidArgument, "script_id is required")
	}
	if err := validateGeneratorFieldBytes(payload.GetScriptId(), "script_id", maxGeneratorScriptIDBytes); err != nil {
		return nil, err
	}
	if err := validateGeneratorFieldBytes(payload.GetContentHash(), "content_hash", maxGeneratorHashBytes); err != nil {
		return nil, err
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, status.FromContextError(err).Err()
		}
	}

	s.mu.Lock()
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			s.mu.Unlock()
			return nil, status.FromContextError(err).Err()
		}
	}
	if s.draining {
		s.mu.Unlock()
		return nil, status.Error(codes.Unavailable, "generator is draining")
	}
	if err := s.validateMutationGenerationLocked(payload.GetExecutionGeneration()); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	if payload.GetExecutionGeneration() > 0 {
		actualHash := scriptContentHash(payload.GetContent())
		if !strings.EqualFold(strings.TrimSpace(payload.GetContentHash()), actualHash) {
			s.mu.Unlock()
			return nil, status.Error(codes.InvalidArgument, "content_hash does not match script content")
		}
	}
	err := s.cacheScriptLocked(payload.GetScriptId(), payload.GetContent(), payload.GetContentHash())
	s.mu.Unlock()
	if err != nil {
		return nil, status.Error(codes.ResourceExhausted, err.Error())
	}

	return &pb.ScriptAck{Cached: true, ContentHash: payload.GetContentHash()}, nil
}

func (s *Service) AssignWork(ctx context.Context, assignment *pb.WorkAssignment) (*pb.AssignmentAck, error) {
	if assignment == nil {
		return nil, status.Error(codes.InvalidArgument, "assignment is required")
	}
	if assignment.GetTestId() == "" {
		return nil, status.Error(codes.InvalidArgument, "test_id is required")
	}
	if err := validateArchiveTestID(assignment.GetTestId()); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid test_id: %v", err)
	}
	if assignment.GetGroupName() == "" {
		return nil, status.Error(codes.InvalidArgument, "group_name is required")
	}
	if assignment.GetScriptId() == "" {
		return nil, status.Error(codes.InvalidArgument, "script_id is required")
	}
	if err := validateAssignmentMetadataBounds(assignment); err != nil {
		return nil, err
	}
	if len(assignment.GetSecretBundle()) > 0 && !TransportSecurityFromEnv().AllowsSecretDelivery() {
		return nil, status.Error(codes.FailedPrecondition, "secret-bearing assignments require authenticated TLS (mTLS or verified TLS plus generator auth token)")
	}
	if assignment.GetVuserCount() < 0 {
		return nil, status.Error(codes.InvalidArgument, "vuser_count must not be negative")
	}
	targetDuration, err := validatedAssignmentDuration(assignment.GetTargetDuration(), assignment.GetTargetDurationSeconds(), "target_duration", true)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	requestedDurationOffset, err := validatedAssignmentDuration(assignment.GetDurationOffset(), 0, "duration_offset", true)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if requestedDurationOffset > 0 {
		if targetDuration <= 0 {
			return nil, status.Error(codes.InvalidArgument, "duration_offset requires a positive target_duration")
		}
		if targetDuration > time.Duration(1<<63-1)-requestedDurationOffset {
			return nil, status.Error(codes.InvalidArgument, "target_duration plus duration_offset overflows the runtime duration")
		}
	}
	logicalTargetDuration := targetDuration + requestedDurationOffset
	rampUp, err := validatedAssignmentDuration(assignment.GetRampUp(), int64(assignment.GetRampUpSeconds()), "ramp_up", true)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	rampDown, err := validatedAssignmentDuration(assignment.GetRampDown(), 0, "ramp_down", true)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	preBaseline, err := validatedAssignmentDuration(assignment.GetPreBaseline(), 0, "pre_baseline", true)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	postBaseline, err := validatedAssignmentDuration(assignment.GetPostBaseline(), 0, "post_baseline", true)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	initStrategy := strings.ToLower(strings.TrimSpace(assignment.GetInitStrategy()))
	if initStrategy == "" {
		initStrategy = "at_runtime"
	}
	if initStrategy != "at_runtime" && initStrategy != "at_once" {
		return nil, status.Error(codes.InvalidArgument, "init_strategy must be at_runtime or at_once")
	}
	shardCount := assignment.GetShardCount()
	if shardCount == 0 {
		if assignment.GetShardOrdinal() != 0 {
			return nil, status.Error(codes.InvalidArgument, "shard_ordinal requires a positive shard_count")
		}
		shardCount = 1
	}
	if assignment.GetShardOrdinal() >= shardCount {
		return nil, status.Error(codes.InvalidArgument, "shard_ordinal must be smaller than shard_count")
	}
	maxInt := uint64(^uint(0) >> 1)
	namespaceSize := uint64(maxGeneratorVUsers)
	if assignment.GetVuserIdOffset() > maxInt || namespaceSize-1 > maxInt-assignment.GetVuserIdOffset() {
		return nil, status.Error(codes.InvalidArgument, "vuser_id_offset namespace overflows runtime int")
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, status.FromContextError(err).Err()
		}
	}
	ancillaryBytes := int64(proto.Size(assignment) - len(assignment.GetScriptContent()))
	ancillaryLimit := generatorRPCAssignmentAncillaryLimit(s.scriptCacheLimit)
	secretEnvelopeBytes := int64(proto.Size(&pb.WorkAssignment{
		SecretBundle:   assignment.GetSecretBundle(),
		SecretBundleId: assignment.GetSecretBundleId(),
	}))
	if ancillaryLimit <= int64(^uint64(0)>>1)-secretEnvelopeBytes {
		ancillaryLimit += secretEnvelopeBytes
	}
	if ancillaryBytes > ancillaryLimit {
		s.mu.RLock()
		availableCapacity := s.availableCapacityLocked()
		s.mu.RUnlock()
		message := fmt.Sprintf("assignment data and metadata are %d bytes and exceed limit %d", ancillaryBytes, ancillaryLimit)
		if len(assignment.GetSecretBundle()) > 0 {
			// The protobuf size includes the write-only secret envelope. Do not
			// turn an admission error into an aggregate credential-length oracle.
			message = "assignment data and metadata exceed the runtime limit"
		}
		return &pb.AssignmentAck{
			Accepted:          false,
			ErrorMessage:      message,
			AvailableCapacity: availableCapacity,
		}, nil
	}
	assignmentFingerprint := workAssignmentFingerprint(assignment)
	// Expire eligible completed archives before deciding whether this test ID is
	// reusable. A retained archive is immutable evidence; starting a new run in
	// the same namespace would truncate files underneath an in-flight download.
	s.sweepArchivedRecords(time.Now())

	// Lifecycle state mutation and publication share the same ordering lock as
	// periodic and terminal metrics. This prevents a detached STAGED publisher
	// from being overtaken by a later commit or terminal publication.
	var lifecycleBatch *pb.MetricsBatch
	s.metricsPublishMu.Lock()
	defer s.metricsPublishMu.Unlock()
	s.mu.Lock()
	defer func() {
		s.mu.Unlock()
		if lifecycleBatch != nil {
			s.publishLifecycleTransitionLocked(lifecycleBatch)
		}
	}()
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, status.FromContextError(err).Err()
		}
	}
	if err := s.validateMutationGenerationLocked(assignment.GetExecutionGeneration()); err != nil {
		return nil, err
	}
	if assignment.GetExecutionGeneration() > 0 && strings.TrimSpace(assignment.GetExpectedScriptHash()) == "" {
		return nil, status.Error(codes.InvalidArgument, "expected_script_hash is required for fenced assignments")
	}

	if s.draining {
		return &pb.AssignmentAck{
			Accepted:          false,
			ErrorMessage:      "generator is draining",
			AvailableCapacity: s.availableCapacityLocked(),
		}, nil
	}
	if _, deleting := s.archiveDeleting[assignment.GetTestId()]; deleting {
		return &pb.AssignmentAck{Accepted: false, ErrorMessage: "test id archive is being retired", AvailableCapacity: s.availableCapacityLocked()}, nil
	}
	if _, stopping := s.stoppingTests[assignment.GetTestId()]; stopping {
		return &pb.AssignmentAck{Accepted: false, ErrorMessage: "test id is stopping", AvailableCapacity: s.availableCapacityLocked()}, nil
	}
	if _, active := s.assignments[assignment.GetTestId()]; !active && s.archiveRecords[assignment.GetTestId()] != nil {
		return &pb.AssignmentAck{
			Accepted:          false,
			ErrorMessage:      "test id already has a retained completed archive",
			AvailableCapacity: s.availableCapacityLocked(),
		}, nil
	}

	// AssignWork may be retried after an ambiguous transport failure. Treat an
	// identical test/group assignment as an idempotent success instead of
	// accounting the same VUsers twice. Resizing an existing group remains an
	// explicit AdjustVUsers operation.
	if current, ok := s.assignments[assignment.GetTestId()]; ok {
		if current.executionGeneration != assignment.GetExecutionGeneration() {
			return nil, status.Errorf(codes.FailedPrecondition, "test belongs to execution generation %d", current.executionGeneration)
		}
		if existing, groupExists := current.groups[assignment.GetGroupName()]; groupExists {
			existingFingerprint, fingerprintExists := current.groupAssignments[assignment.GetGroupName()]
			existingSecretBundleID := current.groupSecretBundleIDs[assignment.GetGroupName()]
			if existing == assignment.GetVuserCount() &&
				fingerprintExists &&
				existingFingerprint == assignmentFingerprint &&
				existingSecretBundleID == assignment.GetSecretBundleId() {
				return &pb.AssignmentAck{
					Accepted:          true,
					AvailableCapacity: s.availableCapacityLocked(),
				}, nil
			}
			return &pb.AssignmentAck{
				Accepted:          false,
				ErrorMessage:      "group is already assigned; use AdjustVUsers to resize it",
				AvailableCapacity: s.availableCapacityLocked(),
			}, nil
		}
		// A running test may gain a group during controller-driven evacuation.
		// Execution-enabled services stage that group independently: the existing
		// executions stay committed and running until CommitWork releases only the
		// new execution. Non-executing test services retain the legacy aggregate
		// staged bit because they have no per-group execution state.
	}
	if err := s.assignmentObjectAdmissionErrorLocked(assignment); err != nil {
		return nil, status.Error(codes.ResourceExhausted, err.Error())
	}

	if len(assignment.GetScriptContent()) > 0 {
		if err := verifyExpectedScriptHash(assignment.GetExpectedScriptHash(), assignment.GetScriptContent()); err != nil {
			return &pb.AssignmentAck{
				Accepted:          false,
				ErrorMessage:      err.Error(),
				AvailableCapacity: s.availableCapacityLocked(),
			}, nil
		}
		if err := s.cacheScriptLocked(assignment.GetScriptId(), assignment.GetScriptContent(), assignment.GetExpectedScriptHash()); err != nil {
			return &pb.AssignmentAck{
				Accepted:          false,
				ErrorMessage:      "cache inline script: " + err.Error(),
				AvailableCapacity: s.availableCapacityLocked(),
			}, nil
		}
	}

	cached, ok := s.touchScriptLocked(assignment.GetScriptId())
	if !ok {
		return &pb.AssignmentAck{
			Accepted:          false,
			ErrorMessage:      "script is not cached and no inline script content was provided",
			AvailableCapacity: s.availableCapacityLocked(),
		}, nil
	}
	if err := verifyExpectedScriptHash(assignment.GetExpectedScriptHash(), cached.content); err != nil {
		return &pb.AssignmentAck{
			Accepted:          false,
			ErrorMessage:      err.Error(),
			AvailableCapacity: s.availableCapacityLocked(),
		}, nil
	}
	if instance, getErr := protocol.Get(cached.protocol); getErr == nil {
		if provider, ok := instance.(protocol.RuntimeCapabilityProvider); ok {
			capabilities := provider.RuntimeCapabilities()
			if capabilities.MaxVUsers > 0 && int64(assignment.GetVuserCount())+s.activeProtocolVUsersLocked(cached.protocol) > capabilities.MaxVUsers {
				return &pb.AssignmentAck{
					Accepted:          false,
					ErrorMessage:      fmt.Sprintf("insufficient %s runtime capacity: maximum %d", cached.protocol, capabilities.MaxVUsers),
					AvailableCapacity: s.availableCapacityLocked(),
				}, nil
			}
		}
	}

	projected := s.activeVUsers64Locked() + int64(assignment.GetVuserCount())
	if projected > int64(s.maxVUsers) {
		return &pb.AssignmentAck{
			Accepted:          false,
			ErrorMessage:      "insufficient generator capacity",
			AvailableCapacity: s.availableCapacityLocked(),
		}, nil
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, status.FromContextError(err).Err()
		}
	}

	current := s.assignments[assignment.GetTestId()]
	previousCommitted := current.committed
	previousPaused := current.paused
	previousDuration := current.duration
	previousDurationSeconds := current.durationSeconds
	lateCommittedGroup := previousCommitted && s.executionEnabled
	executionDuration := targetDuration
	executionDurationOffset := requestedDurationOffset
	current.testID = assignment.GetTestId()
	current.executionGeneration = assignment.GetExecutionGeneration()
	current.committed = previousCommitted && s.executionEnabled
	// A fresh evacuation target has no local pause history. The controller's
	// staged assignment is authoritative when it says the logical run is already
	// paused. False intentionally does not clear an existing assignment's pause
	// state, preserving compatibility with older controllers adding a group to a
	// generator that already participates in the run.
	current.paused = current.paused || assignment.GetStartPaused()
	if current.groups == nil {
		current.groups = make(map[string]int32)
	}
	if current.activeGroups == nil {
		current.activeGroups = make(map[string]int32)
	}
	if current.groupStats == nil {
		current.groupStats = make(map[string]GeneratorStats)
	}
	if current.groupTelemetry == nil {
		current.groupTelemetry = make(map[string]MetricsTelemetry)
	}
	if current.groupTransactions == nil {
		current.groupTransactions = make(map[string][]TransactionMetric)
	}
	if current.groupAssignments == nil {
		current.groupAssignments = make(map[string][sha256.Size]byte)
	}
	if current.groupSecretBundleIDs == nil {
		current.groupSecretBundleIDs = make(map[string]string)
	}
	if current.groupScripts == nil {
		current.groupScripts = make(map[string]string)
	}
	existingDuration := assignedTestDuration(current)
	if lateCommittedGroup {
		if executionDuration <= 0 {
			executionDuration = existingDuration
		}
		if existingDuration > 0 && executionDuration > existingDuration {
			return &pb.AssignmentAck{
				Accepted:          false,
				ErrorMessage:      "late group remaining duration exceeds the test target duration",
				AvailableCapacity: s.availableCapacityLocked(),
			}, nil
		}
		if requestedDurationOffset > 0 {
			if existingDuration > 0 && logicalTargetDuration != existingDuration {
				return &pb.AssignmentAck{
					Accepted:          false,
					ErrorMessage:      "late group duration offset does not align with the test target duration",
					AvailableCapacity: s.availableCapacityLocked(),
				}, nil
			}
		} else if existingDuration > executionDuration {
			executionDurationOffset = existingDuration - executionDuration
		}
	} else if existingDuration == 0 && logicalTargetDuration > 0 {
		current.duration = logicalTargetDuration
		current.durationSeconds = compatibilityDurationSeconds(logicalTargetDuration)
	} else if logicalTargetDuration > 0 && existingDuration != logicalTargetDuration {
		return &pb.AssignmentAck{
			Accepted:          false,
			ErrorMessage:      "all groups in a test must use the same target duration",
			AvailableCapacity: s.availableCapacityLocked(),
		}, nil
	}
	current.groups[assignment.GetGroupName()] += assignment.GetVuserCount()
	current.activeGroups[assignment.GetGroupName()] += assignment.GetVuserCount()
	current.groupAssignments[assignment.GetGroupName()] = assignmentFingerprint
	current.groupSecretBundleIDs[assignment.GetGroupName()] = assignment.GetSecretBundleId()
	current.groupScripts[assignment.GetGroupName()] = assignment.GetScriptId()
	current.vuserCount += assignment.GetVuserCount()
	s.assignments[assignment.GetTestId()] = current
	if s.executionEnabled {
		if err := s.startExecutionLocked(ctx, assignment, executionDuration, executionDurationOffset, rampUp, rampDown, preBaseline, postBaseline, initStrategy, shardCount); err != nil {
			current.committed = previousCommitted
			current.paused = previousPaused
			current.duration = previousDuration
			current.durationSeconds = previousDurationSeconds
			current.groups[assignment.GetGroupName()] -= assignment.GetVuserCount()
			current.activeGroups[assignment.GetGroupName()] -= assignment.GetVuserCount()
			current.vuserCount -= assignment.GetVuserCount()
			if current.groups[assignment.GetGroupName()] <= 0 {
				delete(current.groups, assignment.GetGroupName())
			}
			if current.activeGroups[assignment.GetGroupName()] <= 0 {
				delete(current.activeGroups, assignment.GetGroupName())
			}
			delete(current.groupAssignments, assignment.GetGroupName())
			delete(current.groupSecretBundleIDs, assignment.GetGroupName())
			delete(current.groupScripts, assignment.GetGroupName())
			if current.vuserCount <= 0 {
				delete(s.assignments, assignment.GetTestId())
			} else {
				s.assignments[assignment.GetTestId()] = current
			}
			return &pb.AssignmentAck{
				Accepted:          false,
				ErrorMessage:      err.Error(),
				AvailableCapacity: s.availableCapacityLocked(),
			}, nil
		}
	} else {
		if assignedTestDuration(current) > 0 && s.archiveWriters[assignment.GetTestId()] == nil {
			testName := assignment.GetScriptId()
			if cached, ok := s.touchScriptLocked(assignment.GetScriptId()); ok {
				var script dsl.Script
				if json.Unmarshal(cached.content, &script) == nil && strings.TrimSpace(script.Name) != "" {
					testName = script.Name
				}
			}
			var writer *archiveRecorder
			archiveErr := s.archiveNamespaceErr
			if archiveErr == nil {
				writer, archiveErr = newArchiveRecorderInNamespace(s.archiveNamespace, assignment.GetTestId(), testName)
			}
			if archiveErr != nil {
				current.committed = previousCommitted
				current.paused = previousPaused
				current.duration = previousDuration
				current.durationSeconds = previousDurationSeconds
				current.groups[assignment.GetGroupName()] -= assignment.GetVuserCount()
				current.activeGroups[assignment.GetGroupName()] -= assignment.GetVuserCount()
				current.vuserCount -= assignment.GetVuserCount()
				delete(current.groupAssignments, assignment.GetGroupName())
				delete(current.groupSecretBundleIDs, assignment.GetGroupName())
				delete(current.groupScripts, assignment.GetGroupName())
				if current.groups[assignment.GetGroupName()] <= 0 {
					delete(current.groups, assignment.GetGroupName())
				}
				if current.activeGroups[assignment.GetGroupName()] <= 0 {
					delete(current.activeGroups, assignment.GetGroupName())
				}
				if current.vuserCount <= 0 {
					delete(s.assignments, assignment.GetTestId())
				} else {
					s.assignments[assignment.GetTestId()] = current
				}
				return &pb.AssignmentAck{Accepted: false, ErrorMessage: archiveErr.Error(), AvailableCapacity: s.availableCapacityLocked()}, nil
			}
			s.archiveWriters[assignment.GetTestId()] = writer
		}
	}
	if writer := s.archiveWriters[assignment.GetTestId()]; writer != nil {
		writer.bindExecution(assignment.GetExecutionGeneration(), assignment.GetGroupName())
	}
	s.scheduleStageExpiryLocked(current.testID)
	lifecycleBatch = s.metricsBatchLocked(current.testID)

	return &pb.AssignmentAck{
		Accepted:          true,
		AvailableCapacity: s.availableCapacityLocked(),
	}, nil
}

// CommitWork atomically releases every staged group for one test on this
// generator. AssignWork performs all validation, capacity reservation, asset
// loading, and engine construction; no protocol action can run before this
// explicit commit boundary.
func (s *Service) CommitWork(ctx context.Context, request *pb.WorkCommitRequest) (*pb.WorkCommitAck, error) {
	if request == nil || strings.TrimSpace(request.GetTestId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "test_id is required")
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, status.FromContextError(err).Err()
		}
	}

	type pendingExecution struct {
		groupName string
		exec      *groupExecution
		ctx       context.Context
	}
	pending := make([]pendingExecution, 0)

	var lifecycleBatch *pb.MetricsBatch
	s.metricsPublishMu.Lock()
	defer s.metricsPublishMu.Unlock()
	s.mu.Lock()
	defer func() {
		s.mu.Unlock()
		if lifecycleBatch != nil {
			s.publishLifecycleTransitionLocked(lifecycleBatch)
		}
	}()
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, status.FromContextError(err).Err()
		}
	}
	if err := s.validateMutationGenerationLocked(request.GetExecutionGeneration()); err != nil {
		return nil, err
	}
	if s.draining {
		return &pb.WorkCommitAck{Accepted: false, ErrorMessage: "generator is draining"}, nil
	}
	if _, stopping := s.stoppingTests[request.GetTestId()]; stopping {
		return nil, status.Error(codes.FailedPrecondition, "test assignment is stopping")
	}
	assignment, ok := s.assignments[request.GetTestId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "test assignment not found")
	}
	if assignment.executionGeneration != request.GetExecutionGeneration() {
		return nil, status.Errorf(codes.FailedPrecondition, "test belongs to execution generation %d", assignment.executionGeneration)
	}
	if s.executionEnabled {
		executions := s.executions[request.GetTestId()]
		if len(executions) != len(assignment.groups) || len(executions) == 0 {
			return nil, status.Error(codes.FailedPrecondition, "not all assigned groups are staged")
		}
		groupNames := make([]string, 0, len(executions))
		for groupName := range executions {
			groupNames = append(groupNames, groupName)
		}
		sort.Strings(groupNames)
		for _, groupName := range groupNames {
			exec := executions[groupName]
			if exec == nil || exec.engine == nil {
				return nil, status.Errorf(codes.FailedPrecondition, "group %q is not in a committable staged state", groupName)
			}
		}
		if assignment.committed {
			allStarted := true
			for _, groupName := range groupNames {
				if !executions[groupName].started.Load() {
					allStarted = false
					break
				}
			}
			if allStarted {
				return &pb.WorkCommitAck{Accepted: true}, nil
			}
		}
		startAt := time.Now()
		if request.GetStartAtUnixNano() != 0 {
			startAt = time.Unix(0, request.GetStartAtUnixNano())
			now := time.Now()
			if !startAt.After(now) {
				return &pb.WorkCommitAck{Accepted: false, ErrorMessage: "coordinated start barrier has already elapsed"}, nil
			}
			maxLead := s.assignmentStageTTL
			if maxLead <= 0 {
				maxLead = defaultAssignmentStageTTL
			}
			if startAt.Sub(now) > maxLead {
				return &pb.WorkCommitAck{Accepted: false, ErrorMessage: "coordinated start barrier is too far in the future"}, nil
			}
		}
		pending = make([]pendingExecution, 0, len(groupNames))
		for _, groupName := range groupNames {
			exec := executions[groupName]
			if exec.started.Load() {
				continue
			}
			execCtx, cancel := context.WithCancel(context.Background())
			exec.cancel = cancel
			exec.startAt = startAt
			exec.started.Store(true)
			pending = append(pending, pendingExecution{groupName: groupName, exec: exec, ctx: execCtx})
		}
		for _, item := range pending {
			item := item
			go func() {
				if !waitForCoordinatedExecutionStart(item.ctx, item.exec.startAt) {
					close(item.exec.publisherDone)
					return
				}
				s.runExecutionPublisher(item.ctx, request.GetTestId(), item.groupName, item.exec)
			}()
			go func() {
				if !waitForCoordinatedExecutionStart(item.ctx, item.exec.startAt) {
					s.completeExecutionCallbacks(request.GetTestId(), item.groupName, item.exec, nil)
					return
				}
				_, runErr := item.exec.engine.Run(item.ctx)
				s.completeExecutionCallbacks(request.GetTestId(), item.groupName, item.exec, runErr)
			}()
		}
		if assignment.committed && len(pending) == 0 {
			return &pb.WorkCommitAck{Accepted: true}, nil
		}
	} else if assignment.committed {
		return &pb.WorkCommitAck{Accepted: true}, nil
	}
	assignment.committed = true
	s.assignments[request.GetTestId()] = assignment
	if !s.hasStagedExecutionLocked(request.GetTestId()) {
		s.stopStageTimerLocked(request.GetTestId())
	}
	if !s.executionEnabled && assignedTestDuration(assignment) > 0 {
		s.scheduleCompletionLocked(assignment.testID, assignedTestDuration(assignment))
	}
	lifecycleBatch = s.metricsBatchLocked(request.GetTestId())
	return &pb.WorkCommitAck{Accepted: true}, nil
}

func waitForCoordinatedExecutionStart(ctx context.Context, startAt time.Time) bool {
	delay := time.Until(startAt)
	if delay <= 0 {
		return ctx == nil || ctx.Err() == nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// assignmentObjectAdmissionErrorLocked applies hard state-object limits before
// script-cache mutation, map insertion, archive creation, or goroutine startup.
// This is deliberately independent of VUser capacity: zero-VUser assignments
// still allocate test/group/execution/writer state. The caller must hold s.mu,
// which makes admission and the following reservations atomic with cleanup.
func (s *Service) assignmentObjectAdmissionErrorLocked(assignment *pb.WorkAssignment) error {
	_, testExists := s.assignments[assignment.GetTestId()]
	if !testExists && len(s.assignments) >= s.maxAssignedTests {
		return fmt.Errorf("generator active test limit exceeded (%d)", s.maxAssignedTests)
	}
	groupCount := 0
	for _, assigned := range s.assignments {
		groupCount += len(assigned.groups)
	}
	if groupCount >= s.maxAssignedGroups {
		return fmt.Errorf("generator active group limit exceeded (%d)", s.maxAssignedGroups)
	}

	if s.executionEnabled {
		executionCount := 0
		for _, groups := range s.executions {
			executionCount += len(groups)
		}
		if executionCount >= s.maxExecutions {
			return fmt.Errorf("generator active execution limit exceeded (%d)", s.maxExecutions)
		}
		if s.archiveWriters[assignment.GetTestId()] == nil && len(s.archiveWriters) >= s.maxArchiveWriters {
			return fmt.Errorf("generator active archive writer limit exceeded (%d)", s.maxArchiveWriters)
		}
		return nil
	}

	effectiveDuration, _ := validatedAssignmentDuration(assignment.GetTargetDuration(), assignment.GetTargetDurationSeconds(), "target_duration", true)
	if existing, ok := s.assignments[assignment.GetTestId()]; ok && assignedTestDuration(existing) > effectiveDuration {
		effectiveDuration = assignedTestDuration(existing)
	}
	if effectiveDuration > 0 && s.archiveWriters[assignment.GetTestId()] == nil && len(s.archiveWriters) >= s.maxArchiveWriters {
		return fmt.Errorf("generator active archive writer limit exceeded (%d)", s.maxArchiveWriters)
	}
	return nil
}

func (s *Service) AdjustVUsers(ctx context.Context, request *pb.VUserAdjustment) (*pb.AdjustmentAck, error) {
	if request.GetTestId() == "" {
		return nil, status.Error(codes.InvalidArgument, "test_id is required")
	}
	if request.GetGroupName() == "" {
		return nil, status.Error(codes.InvalidArgument, "group_name is required")
	}
	if request.GetTargetVusers() < 0 {
		return nil, status.Error(codes.InvalidArgument, "target_vusers must be non-negative")
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, status.FromContextError(err).Err()
		}
	}

	// Scaling the concrete engine happens outside s.mu and may block until the
	// engine is ready. Serialize the complete reserve/scale/commit sequence so a
	// slower earlier request cannot finish after a newer request and leave engine
	// state behind the recorded assignment.
	s.adjustMu.Lock()
	defer s.adjustMu.Unlock()
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, status.FromContextError(err).Err()
		}
	}

	s.mu.Lock()
	if err := s.validateMutationGenerationLocked(request.GetExecutionGeneration()); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	assignment, ok := s.assignments[request.GetTestId()]
	if !ok {
		s.mu.Unlock()
		return nil, status.Error(codes.NotFound, "test assignment not found")
	}
	if _, stopping := s.stoppingTests[request.GetTestId()]; stopping {
		s.mu.Unlock()
		return nil, status.Error(codes.FailedPrecondition, "test assignment is stopping")
	}
	if assignment.executionGeneration != request.GetExecutionGeneration() {
		s.mu.Unlock()
		return nil, status.Errorf(codes.FailedPrecondition, "test belongs to execution generation %d", assignment.executionGeneration)
	}
	if !assignment.committed {
		s.mu.Unlock()
		return nil, status.Error(codes.FailedPrecondition, "test assignment is staged but not committed")
	}
	currentGroupVUsers, ok := assignment.groups[request.GetGroupName()]
	if !ok {
		s.mu.Unlock()
		return nil, status.Error(codes.NotFound, "group assignment not found")
	}
	if s.draining && request.GetTargetVusers() > currentGroupVUsers {
		s.mu.Unlock()
		return nil, status.Error(codes.Unavailable, "generator is draining and cannot increase vusers")
	}

	updatedTotal64 := int64(assignment.vuserCount) - int64(currentGroupVUsers) + int64(request.GetTargetVusers())
	projected := s.activeVUsers64Locked() - int64(assignment.vuserCount) + updatedTotal64
	if updatedTotal64 < 0 || updatedTotal64 > maxGeneratorVUsers || projected > int64(s.maxVUsers) {
		s.mu.Unlock()
		return nil, status.Error(codes.ResourceExhausted, "insufficient generator capacity")
	}
	updatedTotal := int32(updatedTotal64)
	var execEngine *engine.MultiGroupEngine
	if s.executionEnabled {
		if execs, ok := s.executions[request.GetTestId()]; ok {
			exec := execs[request.GetGroupName()]
			if exec != nil {
				if !exec.started.Load() {
					s.mu.Unlock()
					return nil, status.Error(codes.FailedPrecondition, "group assignment is staged but not committed")
				}
				execEngine = exec.engine
			}
		}
	}

	// Reserve the requested capacity before releasing s.mu. Concurrent
	// AssignWork/AdjustVUsers calls then see the reservation and cannot all pass
	// the same stale capacity check. The reservation is rolled back if the engine
	// cannot be scaled.
	assignment.groups[request.GetGroupName()] = request.GetTargetVusers()
	assignment.activeGroups[request.GetGroupName()] = request.GetTargetVusers()
	assignment.vuserCount = updatedTotal
	s.assignments[request.GetTestId()] = assignment
	s.mu.Unlock()

	rollbackReservation := func() bool {
		restored := false
		s.mu.Lock()
		current, exists := s.assignments[request.GetTestId()]
		if exists && current.executionGeneration == request.GetExecutionGeneration() {
			if reserved, groupExists := current.groups[request.GetGroupName()]; groupExists && reserved == request.GetTargetVusers() {
				current.groups[request.GetGroupName()] = currentGroupVUsers
				current.activeGroups[request.GetGroupName()] = currentGroupVUsers
				current.vuserCount = current.vuserCount - request.GetTargetVusers() + currentGroupVUsers
				s.assignments[request.GetTestId()] = current
				restored = true
			}
		}
		s.mu.Unlock()
		return restored
	}

	if execEngine != nil {
		if ctx == nil {
			<-execEngine.Ready()
		} else {
			select {
			case <-ctx.Done():
				rollbackReservation()
				return nil, status.FromContextError(ctx.Err()).Err()
			case <-execEngine.Ready():
			}
			if err := ctx.Err(); err != nil {
				rollbackReservation()
				return nil, status.FromContextError(err).Err()
			}
		}
		if err := execEngine.ScaleVUsers(request.GetGroupName(), int(request.GetTargetVusers())); err != nil {
			rollbackReservation()
			return nil, status.Errorf(codes.FailedPrecondition, "scale running group: %v", err)
		}
	}

	s.mu.Lock()
	var commitErr error
	if ctx != nil && ctx.Err() != nil {
		commitErr = status.FromContextError(ctx.Err()).Err()
	} else if err := s.validateMutationGenerationLocked(request.GetExecutionGeneration()); err != nil {
		commitErr = err
	} else if assignment, ok = s.assignments[request.GetTestId()]; !ok {
		commitErr = status.Error(codes.NotFound, "test assignment not found")
	} else if assignment.executionGeneration != request.GetExecutionGeneration() {
		commitErr = status.Errorf(codes.FailedPrecondition, "test belongs to execution generation %d", assignment.executionGeneration)
	} else if _, stopping := s.stoppingTests[request.GetTestId()]; stopping {
		commitErr = status.Error(codes.FailedPrecondition, "test assignment is stopping")
	} else if reservedVUsers, groupExists := assignment.groups[request.GetGroupName()]; !groupExists {
		commitErr = status.Error(codes.NotFound, "group assignment not found")
	} else if reservedVUsers != request.GetTargetVusers() {
		commitErr = status.Error(codes.Aborted, "group assignment changed while scaling")
	}
	var batch *pb.MetricsBatch
	if commitErr == nil {
		batch = s.metricsBatchLocked(request.GetTestId())
	}
	s.mu.Unlock()
	if commitErr != nil {
		// The engine operation and the in-memory capacity reservation form one
		// control-plane mutation. If the request is canceled or fenced after the
		// concrete engine scaled, restore both sides before reporting rejection.
		// A detached/canceled engine may reject the rollback; that is harmless once
		// the reservation is gone, but a still-current assignment must retain the
		// applied reservation so service and engine state never silently diverge.
		if execEngine != nil {
			if rollbackErr := execEngine.ScaleVUsers(request.GetGroupName(), int(currentGroupVUsers)); rollbackErr != nil {
				s.mu.RLock()
				current, exists := s.assignments[request.GetTestId()]
				reservationCurrent := exists && current.executionGeneration == request.GetExecutionGeneration() && current.groups[request.GetGroupName()] == request.GetTargetVusers()
				s.mu.RUnlock()
				if reservationCurrent {
					return nil, status.Errorf(codes.Internal, "%v; rollback running group: %v", commitErr, rollbackErr)
				}
				return nil, commitErr
			}
		}
		rollbackReservation()
		return nil, commitErr
	}

	s.broadcastMetrics(batch)
	return &pb.AdjustmentAck{
		Accepted:      true,
		CurrentVusers: assignment.vuserCount,
	}, nil
}

func (s *Service) AdjustDuration(ctx context.Context, request *pb.DurationAdjustment) (*pb.DurationAck, error) {
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "duration adjustment is required")
	}
	if request.GetTestId() == "" {
		return nil, status.Error(codes.InvalidArgument, "test_id is required")
	}
	duration, err := validatedAssignmentDuration(request.GetTargetDuration(), request.GetTargetDurationSeconds(), "target_duration", false)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, status.FromContextError(err).Err()
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, status.FromContextError(err).Err()
		}
	}
	if err := s.validateMutationGenerationLocked(request.GetExecutionGeneration()); err != nil {
		return nil, err
	}

	assignment, ok := s.assignments[request.GetTestId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "test assignment not found")
	}
	if _, stopping := s.stoppingTests[request.GetTestId()]; stopping {
		return nil, status.Error(codes.FailedPrecondition, "test assignment is stopping")
	}
	if assignment.executionGeneration != request.GetExecutionGeneration() {
		return nil, status.Errorf(codes.FailedPrecondition, "test belongs to execution generation %d", assignment.executionGeneration)
	}
	if !assignment.committed {
		return nil, status.Error(codes.FailedPrecondition, "test assignment is staged but not committed")
	}
	if s.draining && duration > assignedTestDuration(assignment) {
		return nil, status.Error(codes.Unavailable, "generator is draining and cannot extend test duration")
	}
	if s.executionEnabled {
		if execs, ok := s.executions[request.GetTestId()]; ok {
			type durationParticipant struct {
				engine   *engine.MultiGroupEngine
				previous time.Duration
				target   time.Duration
			}
			groupNames := make([]string, 0, len(execs))
			for groupName := range execs {
				groupNames = append(groupNames, groupName)
			}
			sort.Strings(groupNames)
			participants := make([]durationParticipant, 0, len(execs))
			for _, groupName := range groupNames {
				exec := execs[groupName]
				if exec != nil && exec.engine != nil {
					if !exec.started.Load() {
						return nil, status.Error(codes.FailedPrecondition, "test has staged groups that are not committed")
					}
					target := duration - exec.durationOffset
					if target <= 0 {
						// The replacement began after the newly shortened logical
						// deadline. A positive, already-elapsed local target makes the
						// engine terminate immediately without violating its duration
						// invariant.
						target = time.Nanosecond
					}
					participants = append(participants, durationParticipant{engine: exec.engine, previous: exec.engine.GetBaseTargetDuration(), target: target})
				}
			}
			for index, participant := range participants {
				if err := participant.engine.AdjustDuration(participant.target); err != nil {
					rollbackErrors := make([]error, 0, index)
					for rollbackIndex := index - 1; rollbackIndex >= 0; rollbackIndex-- {
						prior := participants[rollbackIndex]
						if rollbackErr := prior.engine.AdjustDuration(prior.previous); rollbackErr != nil {
							rollbackErrors = append(rollbackErrors, rollbackErr)
						}
					}
					if rollbackErr := errors.Join(rollbackErrors...); rollbackErr != nil {
						return nil, status.Errorf(codes.Internal, "adjust running duration: %v; rollback failed: %v", err, rollbackErr)
					}
					return nil, status.Errorf(codes.FailedPrecondition, "adjust running duration: %v", err)
				}
			}
		}
	}
	assignment.duration = duration
	assignment.durationSeconds = compatibilityDurationSeconds(duration)
	s.assignments[request.GetTestId()] = assignment
	if !s.executionEnabled {
		s.scheduleCompletionLocked(assignment.testID, duration)
	}

	return &pb.DurationAck{
		Accepted:               true,
		CurrentDurationSeconds: assignment.durationSeconds,
		CurrentDuration:        durationpb.New(duration),
	}, nil
}

func (s *Service) SetPaused(ctx context.Context, request *pb.PauseRequest) (*pb.PauseAck, error) {
	operationID := strings.TrimSpace(request.GetOperationId())
	if operationID == "" {
		return nil, status.Error(codes.InvalidArgument, "operation_id is required")
	}
	if err := validateGeneratorFieldBytes(operationID, "operation_id", maxGeneratorOperationIDBytes); err != nil {
		return nil, err
	}
	if request.GetOperationSequence() == 0 {
		return nil, status.Error(codes.InvalidArgument, "operation_sequence is required")
	}
	appliedOperationID, appliedSequence, err := s.setPausedWithOperation(ctx, request.GetTestId(), request.GetPause(), request.GetExecutionGeneration(), operationID, request.GetOperationSequence())
	if err != nil {
		return nil, err
	}
	return &pb.PauseAck{
		Accepted:                 true,
		CurrentPaused:            request.GetPause(),
		AppliedOperationId:       appliedOperationID,
		AppliedOperationSequence: appliedSequence,
	}, nil
}

func (s *Service) StopTest(ctx context.Context, request *pb.StopRequest) (*pb.StopAck, error) {
	return s.stopTest(ctx, request, false, false, 0)
}

func (s *Service) stopTest(ctx context.Context, request *pb.StopRequest, bypassExecutionFence bool, enforceMaximumGeneration bool, maximumGeneration uint64) (*pb.StopAck, error) {
	if request.GetTestId() == "" {
		return nil, status.Error(codes.InvalidArgument, "test_id is required")
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, status.FromContextError(err).Err()
		}
	}

	s.mu.Lock()
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			s.mu.Unlock()
			return nil, status.FromContextError(err).Err()
		}
	}
	if !bypassExecutionFence {
		if err := s.validateMutationGenerationLocked(request.GetExecutionGeneration()); err != nil {
			s.mu.Unlock()
			return nil, err
		}
	}

	assignment, ok := s.assignments[request.GetTestId()]
	if ok && bypassExecutionFence && enforceMaximumGeneration && assignment.executionGeneration > maximumGeneration {
		s.mu.Unlock()
		// The lower-generation assignment identified by the fencing snapshot is
		// already gone. Treat that cleanup as complete without stopping a newer
		// assignment that reused the same test ID.
		return &pb.StopAck{Acknowledged: true}, nil
	}
	if ok && !bypassExecutionFence && assignment.executionGeneration != request.GetExecutionGeneration() {
		s.mu.Unlock()
		return nil, status.Errorf(codes.FailedPrecondition, "test belongs to execution generation %d", assignment.executionGeneration)
	}
	if !ok {
		_, stopping := s.stoppingTests[request.GetTestId()]
		stoppingGeneration, trackedGeneration := s.stoppingGenerations[request.GetTestId()]
		completion := s.stopCompletions[request.GetTestId()]
		if trackedGeneration && !bypassExecutionFence && stoppingGeneration != request.GetExecutionGeneration() {
			s.mu.Unlock()
			return nil, status.Errorf(codes.FailedPrecondition, "test belongs to execution generation %d", stoppingGeneration)
		}
		if trackedGeneration && bypassExecutionFence && enforceMaximumGeneration && stoppingGeneration > maximumGeneration {
			s.mu.Unlock()
			return &pb.StopAck{Acknowledged: true}, nil
		}
		if terminalGeneration, terminal := s.terminalGenerationLocked(request.GetTestId()); terminal {
			if !bypassExecutionFence && terminalGeneration != request.GetExecutionGeneration() {
				s.mu.Unlock()
				return nil, status.Errorf(codes.FailedPrecondition, "test belongs to execution generation %d", terminalGeneration)
			}
			s.mu.Unlock()
			return &pb.StopAck{Acknowledged: true}, nil
		}
		s.mu.Unlock()
		if !stopping || completion == nil {
			return &pb.StopAck{Acknowledged: false}, nil
		}
		if ctx == nil {
			ctx = context.Background()
		}
		select {
		case <-completion.done:
			return &pb.StopAck{Acknowledged: true}, nil
		case <-ctx.Done():
			return nil, status.FromContextError(ctx.Err()).Err()
		}
	}
	s.markStoppingLocked(request.GetTestId(), assignment.executionGeneration)
	if request.GetGraceful() && assignment.committed {
		execs := s.executions[request.GetTestId()]
		if len(execs) > 0 {
			// A committed run can temporarily contain newly staged evacuation
			// groups. They have no goroutine to complete a graceful stop and must be
			// discarded before the already-running groups drain, otherwise the final
			// callback leaves the assignment and its capacity reserved forever.
			running := make([]*groupExecution, 0, len(execs))
			staged := make([]*groupExecution, 0)
			stagedNames := make([]string, 0)
			for groupName, exec := range execs {
				if exec != nil && exec.started.Load() {
					running = append(running, exec)
					continue
				}
				staged = append(staged, exec)
				stagedNames = append(stagedNames, groupName)
			}
			if len(running) > 0 {
				for _, groupName := range stagedNames {
					delete(execs, groupName)
					if assignment.activeGroups != nil {
						assignment.activeGroups[groupName] = 0
					}
				}
				s.assignments[request.GetTestId()] = assignment
				s.stopStageTimerLocked(request.GetTestId())
				// Copy pointers while holding s.mu. finishExecution mutates the
				// nested map, so iterating the aliased map after unlocking is a data
				// race and can panic.
				s.mu.Unlock()
				for _, exec := range staged {
					stopGroupExecution(exec)
				}
				for _, exec := range running {
					if !exec.startAt.IsZero() && time.Now().Before(exec.startAt) && exec.cancel != nil {
						exec.cancel()
					} else {
						exec.engine.GracefulStop()
					}
				}
				return &pb.StopAck{
					Acknowledged:  true,
					PendingVusers: assignment.vuserCount,
				}, nil
			}
		}
	}
	delete(s.assignments, request.GetTestId())
	s.stopCompletionTimerLocked(request.GetTestId())
	s.stopStageTimerLocked(request.GetTestId())
	execs := s.executions[request.GetTestId()]
	delete(s.executions, request.GetTestId())
	writer := s.archiveWriters[request.GetTestId()]
	delete(s.archiveWriters, request.GetTestId())
	trackedFinalizer := writer != nil && s.addFinalizer()
	s.mu.Unlock()

	executions := make([]*groupExecution, 0, len(execs))
	for _, exec := range execs {
		executions = append(executions, exec)
		stopGroupExecution(exec)
	}
	if err := waitForGeneratorExecutions(ctx, executions); err != nil {
		if !trackedFinalizer {
			go func() {
				_ = waitForGeneratorExecutions(context.Background(), executions)
				s.captureStoppedExecutionMetrics(&assignment, executions)
				s.finishStoppedTest(request.GetTestId(), assignment, writer)
			}()
			return nil, status.Errorf(codes.DeadlineExceeded, "wait for stopped execution: %v", err)
		}
		go func() {
			defer s.finalizerWG.Done()
			_ = waitForGeneratorExecutions(context.Background(), executions)
			s.captureStoppedExecutionMetrics(&assignment, executions)
			s.finishStoppedTest(request.GetTestId(), assignment, writer)
		}()
		return nil, status.Errorf(codes.DeadlineExceeded, "wait for stopped execution: %v", err)
	}
	if trackedFinalizer {
		defer s.finalizerWG.Done()
	}
	s.captureStoppedExecutionMetrics(&assignment, executions)
	_, finalError := s.finishStoppedTest(request.GetTestId(), assignment, writer)
	if finalError != "" {
		return nil, status.Error(codes.Internal, finalError)
	}
	return &pb.StopAck{Acknowledged: true, PendingVusers: assignment.vuserCount}, nil
}

func waitForGeneratorExecutions(ctx context.Context, executions []*groupExecution) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for _, execution := range executions {
		if execution == nil || execution.done == nil {
			continue
		}
		select {
		case <-execution.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func stopGroupExecution(execution *groupExecution) {
	if execution == nil {
		return
	}
	if execution.cancel != nil {
		execution.cancel()
		return
	}
	// A staged execution has no goroutines that could close done. Closing it at
	// detach time preserves the reuse/fencing barrier without ever starting the
	// engine merely to tear it down.
	execution.stagedDoneOnce.Do(func() {
		if execution.done != nil {
			close(execution.done)
		}
	})
}

func (s *Service) finishStoppedTest(testID string, assignment assignedTest, writer *archiveRecorder) (string, string) {
	return s.finishStoppedTestWithState(testID, assignment, writer, "COMPLETE", "")
}

func (s *Service) finishStoppedTestWithState(testID string, assignment assignedTest, writer *archiveRecorder, finalState string, finalError string) (string, string) {
	defer s.completeStoppingTest(testID, assignment.executionGeneration)
	finalState = strings.ToUpper(strings.TrimSpace(finalState))
	if finalState == "" {
		finalState = "FAILED"
	}
	finalError = strings.TrimSpace(finalError)
	batch := s.metricsBatchForAssignment(testID, assignment, finalState, finalError)
	if writer != nil {
		_ = writer.setTerminalMetrics(batch)
		err := s.finalizeArchiveWriter(writer, finalState, time.Now(), finalError)
		if err != nil {
			finalState = "FAILED"
			if finalError != "" {
				finalError += "; "
			}
			finalError += "archive finalization failed: " + err.Error()
		}
	}
	batch = s.metricsBatchForAssignment(testID, assignment, finalState, finalError)
	s.publishTerminalMetrics(batch)
	return finalState, finalError
}

func (s *Service) StreamMetrics(stream pb.GeneratorControl_StreamMetricsServer) error {
	executionGeneration, err := incomingExecutionGeneration(stream.Context())
	if err != nil {
		return err
	}
	testID, err := incomingMetricsTestID(stream.Context())
	if err != nil {
		return err
	}
	subscriberID, subscriber, initial, err := s.registerScopedSubscriber(executionGeneration, testID)
	if err != nil {
		return err
	}
	defer s.unregisterSubscriber(subscriberID)

	recvErr := make(chan error, 1)
	go func() {
		for {
			signal, err := stream.Recv()
			if err == io.EOF {
				return
			}
			if err != nil {
				recvErr <- err
				return
			}

			pause := signal.GetPause()
			if pause == nil {
				recvErr <- status.Error(codes.Unimplemented, "only pause control is implemented")
				return
			}
			if pause.GetExecutionGeneration() != executionGeneration {
				recvErr <- status.Error(codes.FailedPrecondition, "pause control generation does not match metrics stream generation")
				return
			}
			if testID != "" && pause.GetTestId() != testID {
				recvErr <- status.Error(codes.FailedPrecondition, "pause control test does not match metrics stream scope")
				return
			}

			if err := s.setPaused(stream.Context(), pause.GetTestId(), pause.GetPause(), pause.GetExecutionGeneration()); err != nil {
				recvErr <- err
				return
			}
		}
	}()

	for _, batch := range initial {
		if err := stream.Send(batch); err != nil {
			return err
		}
	}

	for {
		select {
		case <-s.shutdown:
			return nil
		case <-stream.Context().Done():
			return nil
		case <-subscriber.fenced:
			if subscriber.overflowed.Load() {
				return status.Error(codes.ResourceExhausted, "metrics subscriber backlog exceeded the bounded terminal replay queue; reconnect to replay retained terminal state")
			}
			return status.Error(codes.FailedPrecondition, "metrics stream execution generation was fenced")
		case err := <-recvErr:
			return err
		case <-subscriber.wake:
			batch := subscriber.pop()
			if batch != nil {
				if err := stream.Send(batch); err != nil {
					return err
				}
			}
		}
	}
}

const (
	archiveArtifactChunkBytes          = 1 << 20
	archiveArtifactUnaryMaxSize        = 64 << 20
	archiveArtifactUnaryReceiveMaxSize = archiveArtifactUnaryMaxSize + generatorRPCEnvelopeHeadroomBytes
)

func (s *Service) beginArchiveTransfer(unary bool) (func(), error) {
	s.mu.Lock()
	if s.activeArchiveTransfers >= s.maxArchiveTransfers || (unary && s.activeArchiveUnaryTransfers >= s.maxArchiveUnaryTransfers) {
		s.mu.Unlock()
		return nil, status.Error(codes.ResourceExhausted, "generator archive transfer concurrency limit exceeded")
	}
	s.activeArchiveTransfers++
	if unary {
		s.activeArchiveUnaryTransfers++
	}
	s.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			if s.activeArchiveTransfers > 0 {
				s.activeArchiveTransfers--
			}
			if unary && s.activeArchiveUnaryTransfers > 0 {
				s.activeArchiveUnaryTransfers--
			}
			s.mu.Unlock()
		})
	}, nil
}

func (s *Service) beginHeartbeatStream() (func(), error) {
	s.mu.Lock()
	if s.activeHeartbeatStreams >= s.maxHeartbeatStreams {
		s.mu.Unlock()
		return nil, status.Error(codes.ResourceExhausted, "generator heartbeat stream concurrency limit exceeded")
	}
	s.activeHeartbeatStreams++
	s.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			if s.activeHeartbeatStreams > 0 {
				s.activeHeartbeatStreams--
			}
			s.mu.Unlock()
		})
	}, nil
}

func (s *Service) GetArchiveArtifact(_ context.Context, request *pb.ArchiveArtifactRequest) (*pb.ArchiveArtifact, error) {
	releaseTransfer, err := s.beginArchiveTransfer(true)
	if err != nil {
		return nil, err
	}
	defer releaseTransfer()
	artifact, release, err := s.acquireArchiveArtifact(request)
	if err != nil {
		return nil, err
	}
	defer release()
	if artifact.info.Size() > archiveArtifactUnaryMaxSize {
		return nil, status.Errorf(codes.ResourceExhausted, "archive artifact is %d bytes; use StreamArchiveArtifact", artifact.info.Size())
	}
	content, err := io.ReadAll(io.LimitReader(artifact.file, archiveArtifactUnaryMaxSize+1))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "read archive artifact: %v", err)
	}
	if len(content) > archiveArtifactUnaryMaxSize {
		return nil, status.Errorf(codes.ResourceExhausted, "archive artifact exceeds %d bytes; use StreamArchiveArtifact", archiveArtifactUnaryMaxSize)
	}
	return &pb.ArchiveArtifact{Name: request.GetName(), Content: content}, nil
}

// StreamArchiveArtifact transfers an artifact without materializing it in a
// single protobuf message. Chunks are deliberately well below gRPC's default
// message limit, and offsets/total_size let the receiver prove completeness.
func (s *Service) StreamArchiveArtifact(request *pb.ArchiveArtifactRequest, stream grpc.ServerStreamingServer[pb.ArchiveArtifactChunk]) error {
	releaseTransfer, err := s.beginArchiveTransfer(false)
	if err != nil {
		return err
	}
	defer releaseTransfer()
	artifact, release, err := s.acquireArchiveArtifact(request)
	if err != nil {
		return err
	}
	defer release()
	totalSize := artifact.info.Size()
	if totalSize == 0 {
		return stream.Send(&pb.ArchiveArtifactChunk{
			Name:      request.GetName(),
			TotalSize: 0,
			Eof:       true,
		})
	}

	buffer := make([]byte, archiveArtifactChunkBytes)
	var offset int64
	for offset < totalSize {
		if err := stream.Context().Err(); err != nil {
			return err
		}
		remaining := totalSize - offset
		readBuffer := buffer
		if remaining < int64(len(readBuffer)) {
			readBuffer = readBuffer[:remaining]
		}
		n, readErr := io.ReadFull(artifact.file, readBuffer)
		if readErr != nil {
			return status.Errorf(codes.Internal, "read archive artifact at offset %d: %v", offset, readErr)
		}
		chunkOffset := offset
		offset += int64(n)
		if err := stream.Send(&pb.ArchiveArtifactChunk{
			Name:      request.GetName(),
			Content:   readBuffer[:n],
			Offset:    chunkOffset,
			TotalSize: totalSize,
			Eof:       offset == totalSize,
		}); err != nil {
			return err
		}
	}
	return nil
}

// confinedArchiveArtifactPath joins name onto base and verifies the result stays
// within base, rejecting path-traversal names ("..", absolute, backslash, NUL,
// Windows volume). The controller validates artifact names the same way
// (pkg/api/archive_manifest.go); the generator must not trust the field either.
func confinedArchiveArtifactPath(base, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("empty name")
	}
	if strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("name contains NUL byte")
	}
	if strings.ContainsRune(name, '\\') {
		return "", fmt.Errorf("name contains backslash")
	}
	if filepath.IsAbs(name) || filepath.VolumeName(name) != "" {
		return "", fmt.Errorf("name must be relative")
	}
	cleanBase := filepath.Clean(base)
	full := filepath.Join(cleanBase, filepath.FromSlash(name))
	rel, err := filepath.Rel(cleanBase, full)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("name escapes archive directory")
	}
	return full, nil
}

func (s *Service) Heartbeat(stream pb.GeneratorControl_HeartbeatServer) error {
	releaseStream, err := s.beginHeartbeatStream()
	if err != nil {
		return err
	}
	defer releaseStream()
	for {
		ping, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		// activeVUsersLocked/availableCapacityLocked iterate s.assignments and
		// assume s.mu is held; the Heartbeat handler runs concurrently with the
		// AssignWork/StopTest writers, so take the read lock around them (a bare
		// read here is a concurrent map iteration vs locked writes — a hard race
		// that can fatal-crash the generator). collectResources() touches the
		// resource collector, not s.assignments, so compute it outside the lock.
		s.mu.RLock()
		active := s.activeVUsersLocked()
		avail := s.availableCapacityLocked()
		s.mu.RUnlock()
		stats := generatorStatsFromResources(s.collectResources(), active)
		if err := stream.Send(&pb.HeartbeatPong{
			GeneratorId:        s.generatorID,
			TimestampUnixMs:    maxInt64(time.Now().UnixMilli(), ping.GetTimestampUnixMs()),
			ActiveTestIds:      s.activeTestIDs(),
			Stats:              toProtoStats(stats),
			AvailableCapacity:  avail,
			CpuLimitCores:      s.podResources.CPULimitCores,
			MemoryLimitBytes:   s.podResources.MemoryLimitBytes,
			MaxVusers:          s.podResources.MaxVUsers,
			Version:            buildinfo.Get().Version,
			SupportedProtocols: supportedProtocolNamesForHeartbeat(stream.Context()),
			CompatibleFeatures: compatibleFeatureNamesForHeartbeat(stream.Context()),
		}); err != nil {
			return err
		}
	}
}

func supportedProtocolNames() []string {
	names := protocol.List()
	filtered := names[:0]
	for _, name := range names {
		normalized := strings.ToLower(strings.TrimSpace(name))
		if normalized == "java_vuser" && !config.JavaVuserRunnerEnabled() {
			continue
		}
		if instance, err := protocol.Get(normalized); err == nil {
			if provider, ok := instance.(protocol.RuntimeCapabilityProvider); ok && !provider.RuntimeCapabilities().Available {
				continue
			}
		}
		filtered = append(filtered, name)
	}
	names = filtered
	sort.Strings(names)
	return names
}

func compatibleFeatureNames() []string {
	protocols := supportedProtocolNames()
	features := append(protocols, FeatureEvacuationAssignmentStateV1, FeatureSecretBundleV1)
	for _, name := range protocols {
		if instance, err := protocol.Get(name); err == nil {
			if provider, ok := instance.(protocol.RuntimeCapabilityProvider); ok {
				features = append(features, provider.RuntimeCapabilities().Features...)
			}
		}
	}
	sort.Strings(features)
	return features
}

func supportedProtocolNamesForHeartbeat(ctx context.Context) []string {
	names := supportedProtocolNames()
	filtered := names[:0]
	for _, name := range names {
		if instance, err := protocol.Get(name); err == nil {
			if checker, ok := instance.(protocol.RuntimeHealthChecker); ok && !checker.RuntimeHealthy(ctx) {
				continue
			}
		}
		filtered = append(filtered, name)
	}
	return filtered
}

func compatibleFeatureNamesForHeartbeat(ctx context.Context) []string {
	available := supportedProtocolNamesForHeartbeat(ctx)
	features := append([]string{}, available...)
	features = append(features, FeatureEvacuationAssignmentStateV1, FeatureSecretBundleV1)
	for _, name := range available {
		if instance, err := protocol.Get(name); err == nil {
			if provider, ok := instance.(protocol.RuntimeCapabilityProvider); ok {
				features = append(features, provider.RuntimeCapabilities().Features...)
			}
		}
	}
	sort.Strings(features)
	return features
}

func containsProtocolName(values []string, expected string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), expected) {
			return true
		}
	}
	return false
}

func removeProtocolName(values []string, expected string) []string {
	filtered := values[:0]
	for _, value := range values {
		if !strings.EqualFold(strings.TrimSpace(value), expected) {
			filtered = append(filtered, value)
		}
	}
	return filtered
}

func configuredBrunoSessionCapacity() int64 {
	const defaultCapacity = int64(256)
	raw := strings.TrimSpace(os.Getenv("APEXLOAD_BRUNO_MAX_SESSIONS"))
	if raw == "" {
		return defaultCapacity
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 || value > maxGeneratorVUsers {
		return defaultCapacity
	}
	return value
}

func scriptProtocol(content []byte) string {
	var header struct {
		Protocol string `json:"protocol"`
	}
	if json.Unmarshal(content, &header) != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(header.Protocol))
}

func (s *Service) activeProtocolVUsersLocked(protocolName string) int64 {
	var total int64
	for _, assignment := range s.assignments {
		for groupName, scriptID := range assignment.groupScripts {
			if cached, ok := s.scripts[scriptID]; ok && strings.EqualFold(cached.protocol, protocolName) {
				total += int64(assignment.groups[groupName])
			}
		}
	}
	return total
}

func (s *Service) collectResources() *engine.SystemResources {
	if s == nil || s.resCollector == nil {
		return nil
	}
	// ResourceCollector.Collect mutates internal delta-tracking state with no
	// mutex of its own; concurrent Heartbeat streams share one collector, so
	// serialize Collect to avoid a data race (and garbage CPU/GC deltas).
	s.resCollectorMu.Lock()
	defer s.resCollectorMu.Unlock()
	return s.resCollector.Collect()
}

func (s *Service) activeTestIDs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ids := make([]string, 0, len(s.assignments))
	for id := range s.assignments {
		ids = append(ids, id)
	}
	return ids
}

func (s *Service) activeVUsersLocked() int32 {
	total := s.activeVUsers64Locked()
	if total > maxGeneratorVUsers {
		return int32(maxGeneratorVUsers)
	}
	return int32(total)
}

func (s *Service) activeVUsers64Locked() int64 {
	var total int64
	for _, assignment := range s.assignments {
		total += int64(assignment.vuserCount)
	}
	return total
}

func (s *Service) availableCapacityLocked() int32 {
	available := s.maxVUsers - s.activeVUsersLocked()
	if available < 0 {
		return 0
	}
	return available
}

func (s *Service) setPaused(ctx context.Context, testID string, paused bool, executionGeneration uint64) error {
	_, _, err := s.setPausedWithOperation(ctx, testID, paused, executionGeneration, "", 0)
	return err
}

func (s *Service) setPausedWithOperation(ctx context.Context, testID string, paused bool, executionGeneration uint64, operationID string, operationSequence uint64) (string, uint64, error) {
	if testID == "" {
		return "", 0, status.Error(codes.InvalidArgument, "test_id is required")
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return "", 0, status.FromContextError(err).Err()
		}
	}

	var lifecycleBatch *pb.MetricsBatch
	s.metricsPublishMu.Lock()
	defer s.metricsPublishMu.Unlock()
	s.mu.Lock()
	defer func() {
		s.mu.Unlock()
		if lifecycleBatch != nil {
			s.publishLifecycleTransitionLocked(lifecycleBatch)
		}
	}()
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return "", 0, status.FromContextError(err).Err()
		}
	}
	if err := s.validateMutationGenerationLocked(executionGeneration); err != nil {
		return "", 0, err
	}

	assignment, ok := s.assignments[testID]
	if !ok {
		return "", 0, status.Error(codes.NotFound, "test assignment not found")
	}
	if _, stopping := s.stoppingTests[testID]; stopping {
		return "", 0, status.Error(codes.FailedPrecondition, "test assignment is stopping")
	}
	if assignment.executionGeneration != executionGeneration {
		return "", 0, status.Errorf(codes.FailedPrecondition, "test belongs to execution generation %d", assignment.executionGeneration)
	}
	if !assignment.committed {
		return "", 0, status.Error(codes.FailedPrecondition, "test assignment is staged but not committed")
	}
	if operationID != "" {
		if operationSequence < assignment.pauseOperationSequence {
			return "", 0, status.Errorf(codes.FailedPrecondition, "pause operation sequence %d is stale; current sequence is %d", operationSequence, assignment.pauseOperationSequence)
		}
		if operationSequence == assignment.pauseOperationSequence {
			if assignment.pauseOperationID != operationID || assignment.paused != paused {
				return "", 0, status.Error(codes.FailedPrecondition, "pause operation sequence was already used for a different operation")
			}
			return operationID, operationSequence, nil
		}
	}
	if s.executionEnabled {
		if execs, ok := s.executions[testID]; ok {
			for _, exec := range execs {
				if exec != nil && !exec.started.Load() {
					return "", 0, status.Error(codes.FailedPrecondition, "test has staged groups that are not committed")
				}
			}
			for _, exec := range execs {
				var err error
				if paused {
					err = exec.engine.Pause()
				} else {
					err = exec.engine.Resume()
				}
				if err != nil {
					return "", 0, status.Errorf(codes.FailedPrecondition, "update running pause state: %v", err)
				}
			}
		}
	}
	assignment.paused = paused
	if operationID != "" {
		assignment.pauseOperationID = operationID
		assignment.pauseOperationSequence = operationSequence
	}
	s.assignments[testID] = assignment
	lifecycleBatch = s.metricsBatchLocked(testID)
	return operationID, operationSequence, nil
}

func (s *Service) FailTest(testID string, message string) {
	s.mu.Lock()
	assignment, ok := s.assignments[testID]
	if ok {
		s.markStoppingLocked(testID, assignment.executionGeneration)
		delete(s.assignments, testID)
	}
	execs := s.executions[testID]
	delete(s.executions, testID)
	writer := s.archiveWriters[testID]
	delete(s.archiveWriters, testID)
	trackedFinalizer := writer != nil && s.addFinalizer()
	s.stopCompletionTimerLocked(testID)
	s.stopStageTimerLocked(testID)
	s.mu.Unlock()
	executions := make([]*groupExecution, 0, len(execs))
	for _, exec := range execs {
		executions = append(executions, exec)
		if exec != nil {
			message = scrubGeneratorTerminalError(message, exec.redactionConfig)
		}
		stopGroupExecution(exec)
	}
	if len(executions) == 0 {
		message = scrubGeneratorTerminalError(message, nil)
	}
	if !ok {
		if trackedFinalizer {
			s.finalizerWG.Done()
		}
		return
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	waitErr := waitForGeneratorExecutions(waitCtx, executions)
	cancel()
	if waitErr != nil {
		if !trackedFinalizer {
			go func() {
				_ = waitForGeneratorExecutions(context.Background(), executions)
				s.captureStoppedExecutionMetrics(&assignment, executions)
				s.finishFailedTest(testID, assignment, writer, message)
			}()
			return
		}
		go func() {
			defer s.finalizerWG.Done()
			_ = waitForGeneratorExecutions(context.Background(), executions)
			s.captureStoppedExecutionMetrics(&assignment, executions)
			s.finishFailedTest(testID, assignment, writer, message)
		}()
		return
	}
	if trackedFinalizer {
		defer s.finalizerWG.Done()
	}
	s.captureStoppedExecutionMetrics(&assignment, executions)
	s.finishFailedTest(testID, assignment, writer, message)
}

func (s *Service) hasStagedExecutionLocked(testID string) bool {
	assignment, ok := s.assignments[testID]
	if !ok {
		return false
	}
	if !s.executionEnabled {
		return !assignment.committed
	}
	for _, execution := range s.executions[testID] {
		if execution != nil && !execution.started.Load() {
			return true
		}
	}
	return false
}

func (s *Service) scheduleStageExpiryLocked(testID string) {
	if strings.TrimSpace(testID) == "" || s.assignmentStageTTL <= 0 || !s.hasStagedExecutionLocked(testID) {
		return
	}
	s.stopStageTimerLocked(testID)
	s.nextStageToken++
	if s.nextStageToken == 0 {
		s.nextStageToken++
	}
	token := s.nextStageToken
	s.stageTokens[testID] = token
	s.stageTimers[testID] = time.AfterFunc(s.assignmentStageTTL, func() {
		s.expireStagedAssignment(testID, token)
	})
}

func (s *Service) stopStageTimerLocked(testID string) {
	delete(s.stageTokens, testID)
	timer := s.stageTimers[testID]
	delete(s.stageTimers, testID)
	if timer != nil {
		timer.Stop()
	}
}

func (s *Service) expireStagedAssignment(testID string, token uint64) {
	s.mu.Lock()
	if s.stageTokens[testID] != token || !s.hasStagedExecutionLocked(testID) {
		s.mu.Unlock()
		return
	}
	// Claim teardown before releasing the lock so a concurrent CommitWork cannot
	// release staged traffic after the expiry decision.
	s.markStoppingLocked(testID, s.assignments[testID].executionGeneration)
	s.stopStageTimerLocked(testID)
	s.mu.Unlock()
	s.FailTest(testID, "staged assignment expired before commit")
}

func (s *Service) finishFailedTest(testID string, assignment assignedTest, writer *archiveRecorder, message string) {
	s.finishStoppedTestWithState(testID, assignment, writer, "FAILED", message)
}

func (s *Service) PublishMetrics(testID string, groupName string, stats GeneratorStats, transactions []TransactionMetric, telemetry MetricsTelemetry) error {
	return s.publishMetricsWithAggregates(testID, groupName, nil, false, stats, transactions, telemetry, metricsAggregateSet{})
}

func (s *Service) publishMetricsForExecution(testID string, groupName string, exec *groupExecution, stats GeneratorStats, transactions []TransactionMetric, telemetry MetricsTelemetry, aggregates metricsAggregateSet) error {
	if exec == nil {
		return status.Error(codes.Aborted, "execution is no longer current")
	}
	return s.publishMetricsWithAggregates(testID, groupName, exec, true, stats, transactions, telemetry, aggregates)
}

func (s *Service) publishMetricsWithAggregates(testID string, groupName string, expected *groupExecution, requireMatch bool, stats GeneratorStats, transactions []TransactionMetric, telemetry MetricsTelemetry, aggregates metricsAggregateSet) error {
	if testID == "" {
		return status.Error(codes.InvalidArgument, "test_id is required")
	}
	if groupName == "" {
		return status.Error(codes.InvalidArgument, "group_name is required")
	}
	s.metricsPublishMu.Lock()
	defer s.metricsPublishMu.Unlock()

	s.mu.Lock()
	assignment, ok := s.assignments[testID]
	if !ok {
		s.mu.Unlock()
		return status.Error(codes.NotFound, "test assignment not found")
	}
	if requireMatch {
		current := s.executions[testID][groupName]
		if current != expected || assignment.executionGeneration != expected.executionGeneration {
			s.mu.Unlock()
			return status.Error(codes.Aborted, "execution is no longer current")
		}
	}
	if assignment.activeGroups == nil {
		assignment.activeGroups = make(map[string]int32)
	}
	if assignment.groupStats == nil {
		assignment.groupStats = make(map[string]GeneratorStats)
	}
	if assignment.groupTelemetry == nil {
		assignment.groupTelemetry = make(map[string]MetricsTelemetry)
	}
	if assignment.groupTransactions == nil {
		assignment.groupTransactions = make(map[string][]TransactionMetric)
	}
	if assignment.groupAggregates == nil {
		assignment.groupAggregates = make(map[string]metricsAggregateSet)
	}
	assignment.activeGroups[groupName] = stats.ActiveVUsers
	assignment.groupStats[groupName] = stats
	assignment.groupTelemetry[groupName] = telemetry
	if len(transactions) > maxRetainedTransactionSamples {
		transactions = transactions[len(transactions)-maxRetainedTransactionSamples:]
	}
	groupTransactions := append([]TransactionMetric(nil), transactions...)
	for index := range groupTransactions {
		if strings.TrimSpace(groupTransactions[index].Group) == "" {
			groupTransactions[index].Group = groupName
		}
	}
	assignment.groupTransactions[groupName] = groupTransactions
	boundRetainedTransactionSamples(assignment.groupTransactions, groupName)
	assignment.groupAggregates[groupName] = aggregates
	s.assignments[testID] = assignment
	batch := s.metricsBatchLocked(testID)
	writer := s.archiveWriters[testID]
	s.mu.Unlock()

	s.recordMetricsBatch(testID, writer, batch)

	s.broadcastMetricsLocked(batch)
	return nil
}

func (s *Service) recordMetricsBatch(testID string, writer *archiveRecorder, batch *pb.MetricsBatch) {
	if writer == nil || batch == nil {
		return
	}
	if batch.GetStats() != nil {
		_ = writer.RecordResourceSample(time.UnixMilli(batch.TimestampUnixMs), int64(batch.GetStats().GetActiveVusers()), resourcesFromGeneratorStats(generatorStatsFromProto(batch.GetStats())))
	}
	totalErrors, totalSuccess, totalDuration := archiveTotalsFromBatch(batch)
	avgResponse := 0.0
	totalAttempts := totalSuccess + totalErrors
	if totalAttempts > 0 {
		avgResponse = totalDuration / float64(totalAttempts)
	}
	_ = writer.RecordIntervalSummary(archiveIntervalSummary{
		Timestamp:     time.UnixMilli(batch.TimestampUnixMs),
		ActiveVUsers:  int64(batch.GetStats().GetActiveVusers()),
		TPS:           float64(totalSuccess) / windowSecondsFromMs(batch.GetWindowMs()),
		AvgResponseMs: avgResponse,
		ErrorPct:      errorPct(totalErrors, totalSuccess+totalErrors),
		TotalTx:       totalSuccess,
		TotalErrors:   totalErrors,
		P50Ms:         quantileFromMetricAggregate(batch.FleetMetrics, batch.Transactions, 0.50),
		P90Ms:         quantileFromMetricAggregate(batch.FleetMetrics, batch.Transactions, 0.90),
		P95Ms:         quantileFromMetricAggregate(batch.FleetMetrics, batch.Transactions, 0.95),
		P99Ms:         quantileFromMetricAggregate(batch.FleetMetrics, batch.Transactions, 0.99),
	})
	if archiveErr := writer.claimRecordingFailure(); archiveErr != nil {
		// The engine's result-sink contract is non-blocking and intentionally
		// ignores returned sink errors. Escalate the recorder's sticky failure
		// out-of-band: calling FailTest inline from the publisher could wait on
		// this same execution's completion barrier.
		go s.FailTest(testID, "generator archive recording failed: "+archiveErr.Error())
	}
}

func (s *Service) isCurrentExecution(testID string, groupName string, expected *groupExecution) bool {
	if expected == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	assignment, ok := s.assignments[testID]
	if !ok || assignment.executionGeneration != expected.executionGeneration {
		return false
	}
	return s.executions[testID][groupName] == expected
}

func (s *Service) registerSubscriber(executionGeneration uint64) (int, *metricsSubscriber, []*pb.MetricsBatch, error) {
	return s.registerScopedSubscriber(executionGeneration, "")
}

func (s *Service) registerScopedSubscriber(executionGeneration uint64, testID string) (int, *metricsSubscriber, []*pb.MetricsBatch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneTerminalBatchesLocked(time.Now())
	if err := s.validateMutationGenerationLocked(executionGeneration); err != nil {
		return 0, nil, nil, err
	}
	if len(s.subscribers) >= s.maxMetricsSubscribers {
		return 0, nil, nil, status.Errorf(codes.ResourceExhausted, "metrics subscriber limit exceeded (%d)", s.maxMetricsSubscribers)
	}

	id := s.nextSubID
	s.nextSubID++
	subscriber := &metricsSubscriber{
		generation: executionGeneration,
		testID:     testID,
		queue:      make([]*pb.MetricsBatch, 0, metricsSubscriberQueueCapacity),
		wake:       make(chan struct{}, 1),
		fenced:     make(chan struct{}),
	}
	s.subscribers[id] = subscriber
	if testID == "" {
		s.unscopedSubscriberIDs[id] = struct{}{}
	} else {
		if s.subscriberIDsByTest[testID] == nil {
			s.subscriberIDsByTest[testID] = make(map[int]struct{})
		}
		s.subscriberIDsByTest[testID][id] = struct{}{}
	}

	initial := make([]*pb.MetricsBatch, 0, len(s.assignments))
	for assignmentTestID, assignment := range s.assignments {
		if assignment.executionGeneration == executionGeneration && (testID == "" || assignmentTestID == testID) {
			initial = append(initial, s.metricsBatchLocked(assignmentTestID))
		}
	}
	for key, tombstone := range s.terminalBatches {
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

func (s *Service) unregisterSubscriber(id int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	subscriber, ok := s.subscribers[id]
	if !ok {
		return
	}
	delete(s.subscribers, id)
	if subscriber.testID == "" {
		delete(s.unscopedSubscriberIDs, id)
	} else if ids := s.subscriberIDsByTest[subscriber.testID]; ids != nil {
		delete(ids, id)
		if len(ids) == 0 {
			delete(s.subscriberIDsByTest, subscriber.testID)
		}
	}
}

func (s *Service) broadcastMetrics(batch *pb.MetricsBatch) {
	s.metricsPublishMu.Lock()
	defer s.metricsPublishMu.Unlock()
	s.broadcastMetricsLocked(batch)
}

// publishLifecycleTransitionLocked publishes a control-plane lifecycle
// transition while metricsPublishMu is held by the state mutation that created
// it. The hook is intentionally unexported and exists only to make competing
// transition/terminal paths deterministic in regression tests.
func (s *Service) publishLifecycleTransitionLocked(batch *pb.MetricsBatch) {
	if s.lifecyclePublishHook != nil {
		s.lifecyclePublishHook(batch)
	}
	s.broadcastMetricsLocked(batch)
}

func (s *Service) broadcastMetricsLocked(batch *pb.MetricsBatch) {
	if batch == nil {
		return
	}

	s.mu.RLock()
	if !s.metricsBatchIsCurrentLocked(batch) {
		s.mu.RUnlock()
		return
	}
	testIDs := s.subscriberIDsByTest[batch.GetTestId()]
	subscribers := make([]*metricsSubscriber, 0, len(testIDs)+len(s.unscopedSubscriberIDs))
	for id := range testIDs {
		if subscriber := s.subscribers[id]; subscriber != nil {
			subscribers = append(subscribers, subscriber)
		}
	}
	for id := range s.unscopedSubscriberIDs {
		if subscriber := s.subscribers[id]; subscriber != nil {
			subscribers = append(subscribers, subscriber)
		}
	}
	s.mu.RUnlock()

	for _, subscriber := range subscribers {
		if subscriber == nil || subscriber.generation != batch.GetExecutionGeneration() || (subscriber.testID != "" && subscriber.testID != batch.GetTestId()) {
			continue
		}
		if dropped := subscriber.enqueue(batch); dropped > 0 {
			s.droppedBatches.Add(dropped)
		}
	}
}

// metricsBatchIsCurrentLocked rejects delayed nonterminal snapshots. Terminal
// retention is immutable for an exact test generation, and a lifecycle
// transition is publishable only while it still describes the current
// assignment state. The caller must hold s.mu for reading or writing.
func (s *Service) metricsBatchIsCurrentLocked(batch *pb.MetricsBatch) bool {
	if batch == nil || isGeneratorTerminalLifecycle(batch.GetLifecycleState()) {
		return batch != nil
	}
	key := terminalBatchKey{testID: batch.GetTestId(), generation: batch.GetExecutionGeneration()}
	if tombstone := s.terminalBatches[key]; tombstone.batch != nil {
		return false
	}

	state := strings.ToUpper(strings.TrimSpace(batch.GetLifecycleState()))
	switch state {
	case "STAGED", "RUNNING", "PAUSED":
	default:
		// Preserve forward compatibility for telemetry batches that do not carry
		// one of the lifecycle states owned by this service.
		return true
	}
	if _, stopping := s.stoppingTests[batch.GetTestId()]; stopping {
		return false
	}
	assignment, ok := s.assignments[batch.GetTestId()]
	if !ok || assignment.executionGeneration != batch.GetExecutionGeneration() {
		return false
	}
	want := "RUNNING"
	if !assignment.committed {
		want = "STAGED"
	} else if assignment.paused {
		want = "PAUSED"
	}
	return state == want
}

func (s *Service) publishTerminalMetrics(batch *pb.MetricsBatch) {
	if batch == nil || !isGeneratorTerminalLifecycle(batch.GetLifecycleState()) {
		return
	}
	s.metricsPublishMu.Lock()
	defer s.metricsPublishMu.Unlock()
	clone := proto.Clone(batch).(*pb.MetricsBatch)
	key := terminalBatchKey{testID: clone.GetTestId(), generation: clone.GetExecutionGeneration()}
	completedAt := time.UnixMilli(clone.GetTimestampUnixMs())
	if clone.GetTimestampUnixMs() <= 0 {
		completedAt = time.Now()
	}
	s.mu.Lock()
	s.terminalBatches[key] = terminalMetricsTombstone{batch: clone, completedAt: completedAt}
	s.pruneTerminalBatchesLocked(time.Now())
	s.mu.Unlock()
	s.broadcastMetricsLocked(clone)
}

func (s *Service) pruneTerminalBatchesLocked(now time.Time) {
	if s.archiveTTL > 0 {
		for key, tombstone := range s.terminalBatches {
			if !tombstone.completedAt.IsZero() && now.Sub(tombstone.completedAt) > s.archiveTTL {
				delete(s.terminalBatches, key)
			}
		}
	}
	limit := s.archiveRecordMax
	if limit <= 0 {
		limit = defaultGeneratorArchiveMax
	}
	for len(s.terminalBatches) > limit {
		var oldestKey terminalBatchKey
		var oldestAt time.Time
		found := false
		for key, tombstone := range s.terminalBatches {
			if !found || tombstone.completedAt.Before(oldestAt) || (tombstone.completedAt.Equal(oldestAt) && (key.testID < oldestKey.testID || key.testID == oldestKey.testID && key.generation < oldestKey.generation)) {
				oldestKey = key
				oldestAt = tombstone.completedAt
				found = true
			}
		}
		if !found {
			break
		}
		delete(s.terminalBatches, oldestKey)
	}
}

func (s *Service) terminalGenerationLocked(testID string) (uint64, bool) {
	var generation uint64
	found := false
	for key := range s.terminalBatches {
		if key.testID == testID && (!found || key.generation > generation) {
			generation = key.generation
			found = true
		}
	}
	return generation, found
}

func (s *Service) deleteTerminalBatchesForTestLocked(testID string) {
	for key := range s.terminalBatches {
		if key.testID == testID {
			delete(s.terminalBatches, key)
		}
	}
}

func (s *Service) metricsBatchLocked(testID string) *pb.MetricsBatch {
	assignment, ok := s.assignments[testID]
	if !ok {
		return s.metricsBatchForState(testID, 0, 0, false, nil, "COMPLETE", "")
	}
	if !assignment.committed {
		return s.metricsBatchForState(testID, assignment.executionGeneration, 0, false, assignment.groups, "STAGED", "")
	}
	lifecycleState := "RUNNING"
	if assignment.paused {
		lifecycleState = "PAUSED"
	}
	return s.metricsBatchForAssignment(testID, assignment, lifecycleState, "")
}

func (s *Service) metricsBatchForAssignment(testID string, assignment assignedTest, lifecycleState string, lifecycleError string) *pb.MetricsBatch {
	groupAssignments := assignment.activeGroups
	if len(groupAssignments) == 0 || isGeneratorTerminalLifecycle(lifecycleState) {
		groupAssignments = assignment.groups
	}
	stats := GeneratorStats{}
	groupsWithActiveStats := make(map[string]bool, len(assignment.groupStats))
	for groupName, groupStats := range assignment.groupStats {
		stats = mergeGeneratorStats(stats, groupStats)
		if groupStats.ActiveVUsers > 0 {
			groupsWithActiveStats[groupName] = true
		}
	}
	telemetry := mergeMetricsTelemetry(assignment.groupTelemetry)
	transactions := make([]TransactionMetric, 0, maxRetainedTransactionSamples)
	for _, groupTransactions := range assignment.groupTransactions {
		remaining := maxRetainedTransactionSamples - len(transactions)
		if remaining == 0 {
			break
		}
		if len(groupTransactions) > remaining {
			groupTransactions = groupTransactions[len(groupTransactions)-remaining:]
		}
		transactions = append(transactions, groupTransactions...)
	}
	aggregates := mergeMetricsAggregateSets(assignment.groupAggregates)
	// The batch carries a single window, but every rate consumer computes
	// totalSuccess/window. When a pod hosts multiple groups whose windows differ
	// (ramp/stagger/wind-down), dividing the summed cross-group success by the max
	// window mis-weights TPS. Replace the window with the effective window that
	// makes totalSuccess/window equal the additive sum-of-(groupSuccess/groupWindow)
	// rate, so both the archive interval summary and the controller aggregation
	// report the correct fleet TPS. In steady state (equal windows) this equals the
	// window, so behavior is unchanged.
	if telemetry != nil {
		telemetry.WindowMs = effectiveBatchWindowMs(assignment, telemetry.WindowMs)
	}
	for groupName, activeVUsers := range groupAssignments {
		if !groupsWithActiveStats[groupName] {
			stats.ActiveVUsers += activeVUsers
		}
	}
	if stats.ActiveVUsers == 0 && len(groupAssignments) == 0 {
		stats.ActiveVUsers = assignment.vuserCount
	}
	return s.metricsBatchForMetrics(testID, assignment.executionGeneration, stats, transactions, aggregates, assignment.paused, groupAssignments, lifecycleState, lifecycleError, telemetry)
}

func (s *Service) metricsBatchForState(testID string, executionGeneration uint64, vuserCount int32, paused bool, groupAssignments map[string]int32, lifecycleState string, lifecycleError string) *pb.MetricsBatch {
	return s.metricsBatchForMetrics(testID, executionGeneration, GeneratorStats{ActiveVUsers: vuserCount}, nil, metricsAggregateSet{}, paused, groupAssignments, lifecycleState, lifecycleError, nil)
}

func isGeneratorTerminalLifecycle(state string) bool {
	switch strings.ToUpper(strings.TrimSpace(state)) {
	case "COMPLETE", "FAILED", "ABORTED":
		return true
	default:
		return false
	}
}

func (s *Service) metricsBatchForMetrics(testID string, executionGeneration uint64, stats GeneratorStats, transactions []TransactionMetric, aggregates metricsAggregateSet, paused bool, groupAssignments map[string]int32, lifecycleState string, lifecycleError string, telemetry *MetricsTelemetry) *pb.MetricsBatch {
	activeVUsers := stats.ActiveVUsers
	terminal := isGeneratorTerminalLifecycle(lifecycleState)
	if paused || terminal {
		activeVUsers = 0
	}
	stats.ActiveVUsers = activeVUsers
	groups := make([]*pb.GroupMetric, 0)
	if len(groupAssignments) > 0 {
		groups = make([]*pb.GroupMetric, 0, len(groupAssignments))
		for name, groupVUsers := range groupAssignments {
			activeGroupVUsers := groupVUsers
			if paused || terminal {
				activeGroupVUsers = 0
			}
			groups = append(groups, &pb.GroupMetric{
				Name:         name,
				ActiveVusers: activeGroupVUsers,
				Metrics:      metricAggregateToProto(findGroupMetricAggregate(aggregates.Groups, name)),
			})
		}
	}
	protoTransactions := make([]*pb.TransactionMetric, 0, len(transactions))
	for _, tx := range transactions {
		var protoServerTelemetry *pb.ServerTelemetry
		if tx.ServerTelemetry != nil {
			protoServerTelemetry = &pb.ServerTelemetry{
				ServerProcessingMs:     tx.ServerTelemetry.ServerProcessingMs,
				TransferMs:             tx.ServerTelemetry.TransferMs,
				RequestPayloadKb:       tx.ServerTelemetry.RequestPayloadKB,
				ResponsePayloadKb:      tx.ServerTelemetry.ResponsePayloadKB,
				ConnectionReused:       tx.ServerTelemetry.ConnectionReused,
				RequestSizeClass:       tx.ServerTelemetry.RequestSizeClass,
				ResponseSizeClass:      tx.ServerTelemetry.ResponseSizeClass,
				FirstEventMs:           tx.ServerTelemetry.FirstEventMs,
				EventCount:             tx.ServerTelemetry.EventCount,
				MessageLatencyMs:       tx.ServerTelemetry.MessageLatencyMs,
				MessagesSent:           tx.ServerTelemetry.MessagesSent,
				MessagesReceived:       tx.ServerTelemetry.MessagesReceived,
				MessagesSentPerSec:     tx.ServerTelemetry.MessagesSentPerSec,
				MessagesReceivedPerSec: tx.ServerTelemetry.MessagesReceivedPerSec,
				ConsumerLagMs:          tx.ServerTelemetry.ConsumerLagMs,
				SendFailureCount:       tx.ServerTelemetry.SendFailureCount,
				RedeliveryCount:        tx.ServerTelemetry.RedeliveryCount,
				DeadLetteredCount:      tx.ServerTelemetry.DeadLetteredCount,
				DuplicateCount:         tx.ServerTelemetry.DuplicateCount,
				OutOfOrderCount:        tx.ServerTelemetry.OutOfOrderCount,
				CommitCount:            tx.ServerTelemetry.CommitCount,
				AbortCount:             tx.ServerTelemetry.AbortCount,
				ConsumerOffsetLag:      tx.ServerTelemetry.ConsumerOffsetLag,
				ThrottledRequests:      tx.ServerTelemetry.ThrottledRequests,
				ServerBusyCount:        tx.ServerTelemetry.ServerBusyCount,
				QuotaExceededCount:     tx.ServerTelemetry.QuotaExceededCount,
				EventTooLargeCount:     tx.ServerTelemetry.EventTooLargeCount,
				EndToEndLatencyMs:      tx.ServerTelemetry.EndToEndLatencyMs,
				PartitionId:            tx.ServerTelemetry.PartitionID,
			}
		}
		protoTransactions = append(protoTransactions, &pb.TransactionMetric{
			Name:             tx.Name,
			StartTimeUnixMs:  tx.StartTimeUnixMs,
			Group:            tx.Group,
			DurationMs:       tx.DurationMs,
			DnsMs:            tx.DNSMs,
			ConnectMs:        tx.ConnectMs,
			TlsMs:            tx.TLSMs,
			TtfbMs:           tx.TTFBMs,
			StatusCode:       tx.StatusCode,
			Success:          tx.Success,
			ErrorMessage:     tx.ErrorMessage,
			BytesSent:        tx.BytesSent,
			BytesReceived:    tx.BytesReceived,
			CorrelationId:    tx.CorrelationID,
			DependencyType:   tx.DependencyType,
			DependencyTarget: tx.DependencyTarget,
			SoapOperation:    tx.SOAPOperation,
			ServerTelemetry:  protoServerTelemetry,
			SnapshotJson:     snapshotJSON(tx.Snapshot),
		})
	}
	var protoTelemetry *pb.MetricsBatchTelemetry
	windowMs := int64(0)
	if telemetry != nil {
		windowMs = telemetry.WindowMs
		protoTelemetry = &pb.MetricsBatchTelemetry{
			DroppedResults:           telemetry.DroppedResults,
			SheddedArrivals:          telemetry.SheddedArrivals,
			CollectedAtUnixMs:        telemetry.CollectedAtUnixMs,
			PublishedAtUnixMs:        telemetry.PublishedAtUnixMs,
			SampleAgeMs:              telemetry.SampleAgeMs,
			GeneratorProcessingLagMs: telemetry.GeneratorProcessingLagMs,
			TransportLagMs:           telemetry.TransportLagMs,
			DroppedSubscriberBatches: telemetry.DroppedSubscriberBatches,
		}
	}
	return &pb.MetricsBatch{
		TestId:                testID,
		ExecutionGeneration:   executionGeneration,
		GeneratorId:           s.generatorID,
		TimestampUnixMs:       time.Now().UnixMilli(),
		WindowMs:              windowMs,
		Transactions:          protoTransactions,
		Stats:                 toProtoStats(stats),
		Groups:                groups,
		LifecycleState:        lifecycleState,
		LifecycleError:        lifecycleError,
		Telemetry:             protoTelemetry,
		TransactionMetrics:    metricAggregatesToProto(aggregates.Transactions),
		DependencyMetrics:     metricAggregatesToProto(aggregates.Dependencies),
		SoapMetrics:           metricAggregatesToProto(aggregates.SOAP),
		NetworkProfileMetrics: metricAggregatesToProto(aggregates.NetworkProfiles),
		FleetMetrics:          metricAggregateToProto(aggregates.Fleet),
		BrowserMetrics:        browserMetricsToProto(aggregates.Browser),
		BrowserDeviceMetrics:  browserDeviceMetricsToProto(aggregates.BrowserByDevice),
	}
}

func (s *Service) scheduleCompletionLocked(testID string, duration time.Duration) {
	if duration <= 0 {
		return
	}
	s.stopCompletionTimerLocked(testID)
	s.nextCompletionToken++
	if s.nextCompletionToken == 0 {
		// Reserve zero for callers that deliberately bypass timer ownership.
		s.nextCompletionToken++
	}
	token := s.nextCompletionToken
	s.completionTokens[testID] = token
	s.completionTimers[testID] = time.AfterFunc(duration, func() {
		s.completeAssignmentForTimer(testID, token)
	})
}

func (s *Service) stopCompletionTimerLocked(testID string) {
	delete(s.completionTokens, testID)
	timer, ok := s.completionTimers[testID]
	if !ok {
		return
	}
	delete(s.completionTimers, testID)
	timer.Stop()
}

func (s *Service) completeAssignment(testID string) {
	s.completeAssignmentMatchingTimer(testID, 0, false)
}

func (s *Service) completeAssignmentForTimer(testID string, token uint64) {
	s.completeAssignmentMatchingTimer(testID, token, true)
}

func (s *Service) completeAssignmentMatchingTimer(testID string, token uint64, requireTimerOwnership bool) {
	s.mu.Lock()
	if requireTimerOwnership && s.completionTokens[testID] != token {
		s.mu.Unlock()
		return
	}
	assignment, ok := s.assignments[testID]
	if ok {
		s.markStoppingLocked(testID, assignment.executionGeneration)
		delete(s.assignments, testID)
	}
	delete(s.executions, testID)
	s.stopCompletionTimerLocked(testID)
	s.stopStageTimerLocked(testID)
	writer := s.archiveWriters[testID]
	delete(s.archiveWriters, testID)
	tracked := ok && writer != nil && s.addFinalizer()
	s.mu.Unlock()
	if !ok {
		return
	}
	if tracked {
		defer s.finalizerWG.Done()
	}
	s.finishStoppedTest(testID, assignment, writer)
}

func (s *Service) startExecutionLocked(ctx context.Context, assignment *pb.WorkAssignment, executionDuration time.Duration, durationOffset time.Duration, rampUp time.Duration, rampDown time.Duration, preBaseline time.Duration, postBaseline time.Duration, initStrategy string, shardCount uint32) (err error) {
	if !s.executionEnabled {
		return nil
	}
	if ctx != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return status.FromContextError(ctxErr).Err()
		}
	}
	if assignment.GetGroupName() == "" {
		return status.Error(codes.InvalidArgument, "group_name is required")
	}
	createdExecutionMap := false
	if _, ok := s.executions[assignment.GetTestId()]; !ok {
		s.executions[assignment.GetTestId()] = make(map[string]*groupExecution)
		createdExecutionMap = true
	}
	createdWriter := false
	defer func() {
		if err == nil {
			return
		}
		if createdWriter {
			writer := s.archiveWriters[assignment.GetTestId()]
			delete(s.archiveWriters, assignment.GetTestId())
			if discardErr := writer.Discard(); discardErr != nil {
				slog.Warn("discard failed execution archive", "test_id", assignment.GetTestId(), "error", discardErr)
			}
		}
		if createdExecutionMap && len(s.executions[assignment.GetTestId()]) == 0 {
			delete(s.executions, assignment.GetTestId())
		}
	}()
	if s.executions[assignment.GetTestId()][assignment.GetGroupName()] != nil {
		return nil
	}
	rawScript := assignment.GetScriptContent()
	if len(rawScript) == 0 {
		cached, ok := s.touchScriptLocked(assignment.GetScriptId())
		if !ok {
			return status.Error(codes.NotFound, "script is not cached")
		}
		rawScript = cached.content
	}
	var script dsl.Script
	if err := json.Unmarshal(rawScript, &script); err != nil {
		return status.Errorf(codes.InvalidArgument, "decode script: %v", err)
	}
	if instance, getErr := protocol.Get(strings.ToLower(strings.TrimSpace(script.Protocol))); getErr == nil {
		if provider, ok := instance.(protocol.RuntimeCapabilityProvider); ok && !provider.RuntimeCapabilities().Available {
			return status.Errorf(codes.FailedPrecondition, "%s runtime is unavailable on this generator", script.Protocol)
		}
	}
	if issues := dsl.ValidateScriptIssues(&script); len(issues) > 0 {
		const maxValidationMessageBytes = 16 << 10
		message := strings.Join(issues, "; ")
		if len(message) > maxValidationMessageBytes {
			message = boundedtext.TruncateUTF8(message, maxValidationMessageBytes-len("...[truncated]")) + "...[truncated]"
		}
		return status.Errorf(codes.InvalidArgument, "validate script: %s", message)
	}
	referencedSecrets, err := engine.ReferencedSecretNames(&script)
	if err != nil {
		return status.Error(codes.InvalidArgument, "inspect script secret references")
	}
	if err := validateAssignmentSecretReferences(referencedSecrets, assignment.GetSecretBundle()); err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	runLogic := script.Config.RunLogic
	if shardCount > 1 && (runLogic.GlobalIterations > 0 || runLogic.MaxTransactions > 0) {
		return status.Error(codes.InvalidArgument, "global iteration/transaction goals require a single shard because no cross-generator goal coordinator is available")
	}
	if durationOffset > 0 && (runLogic.Iterations > 0 || runLogic.GlobalIterations > 0 || runLogic.MaxTransactions > 0) {
		return status.Error(codes.InvalidArgument, "bounded iteration/transaction goals cannot resume from a duration offset without a progress checkpoint")
	}
	if ctx != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return status.FromContextError(ctxErr).Err()
		}
	}
	duration := executionDuration
	if duration <= 0 {
		duration = assignedTestDuration(s.assignments[assignment.GetTestId()])
	}
	if duration <= 0 {
		duration = time.Second
	}
	stagedDataFiles, err := assignmentDataFiles(assignment, &script, s.scriptCacheLimit)
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "validate staged data files: %v", err)
	}
	redactionConfig, err := engine.NewRedactionConfig(redactionRulesFromProto(assignment.GetRedactionRules()))
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "build redaction config: %v", err)
	}
	redactionConfig, err = engine.WithSecretValueRedactionChecked(redactionConfig, secretBundleValues(assignment.GetSecretBundle()))
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "prepare runtime secret redaction: %v", err)
	}
	results := &transactionBuffer{lastDrainAt: time.Now()}
	writer := s.archiveWriters[assignment.GetTestId()]
	if writer == nil {
		var archiveWriter *archiveRecorder
		archiveErr := s.archiveNamespaceErr
		if archiveErr == nil {
			archiveWriter, archiveErr = newArchiveRecorderInNamespace(s.archiveNamespace, assignment.GetTestId(), script.Name)
		}
		if archiveErr != nil {
			return status.Errorf(codes.Internal, "initialize archive recorder: %v", archiveErr)
		}
		writer = archiveWriter
		s.archiveWriters[assignment.GetTestId()] = writer
		createdWriter = true
	}
	mge, err := engine.NewMultiGroup(engine.MultiGroupConfig{
		Groups: []engine.GroupConfig{{
			Name:            assignment.GetGroupName(),
			Script:          &script,
			VUsers:          int(assignment.GetVuserCount()),
			RampUp:          rampUp,
			RampDown:        rampDown,
			AllowZeroVUsers: assignment.GetVuserCount() == 0,
			InitStrategy:    initStrategy,
			DataShard: engine.DataShardConfig{
				Ordinal: assignment.GetShardOrdinal(),
				Count:   shardCount,
			},
			StagedDataFiles: stagedDataFiles,
			VUserIDOffset:   int(assignment.GetVuserIdOffset()),
			VUserIDSpan:     int(maxGeneratorVUsers),
			ResultSink:      combineSinks(results, writer),
			RedactionConfig: redactionConfig,
			SecretResolver:  secretBundleResolver(assignment.GetSecretBundle()),
		}},
		Duration:     duration,
		PreBaseline:  preBaseline,
		PostBaseline: postBaseline,
		StartPaused:  s.assignments[assignment.GetTestId()].paused,
	})
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "build execution engine: %v", err)
	}
	if ctx != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return status.FromContextError(ctxErr).Err()
		}
	}
	exec := &groupExecution{
		groupName:           assignment.GetGroupName(),
		executionGeneration: assignment.GetExecutionGeneration(),
		engine:              mge,
		durationOffset:      durationOffset,
		redactionConfig:     redactionConfig,
		results:             results,
		publisherDone:       make(chan struct{}),
		done:                make(chan struct{}),
	}
	s.executions[assignment.GetTestId()][assignment.GetGroupName()] = exec
	return nil
}

func (s *Service) completeExecutionCallbacks(testID string, groupName string, exec *groupExecution, err error) {
	if exec == nil {
		return
	}
	if exec.cancel != nil {
		exec.cancel()
	}
	// Join the periodic publisher before the final snapshot and terminal state
	// transition. A canceled publisher may already be inside a tick; allowing a
	// stop waiter to reuse the test/group ID while that callback is in flight can
	// otherwise write old metrics into the replacement assignment.
	if exec.publisherDone != nil {
		<-exec.publisherDone
	}
	s.publishExecutionSnapshot(testID, groupName, exec)
	s.finishExecutionInstance(testID, groupName, exec, err)
	// done is the reuse barrier: every callback holding this execution identity
	// has completed before stop/fence waiters are allowed to clear stoppingTests.
	if exec.done != nil {
		close(exec.done)
	}
}

func (s *Service) runExecutionPublisher(ctx context.Context, testID string, groupName string, exec *groupExecution) {
	if exec != nil && exec.publisherDone != nil {
		defer close(exec.publisherDone)
	}
	ticker := time.NewTicker(s.publishInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.publishExecutionSnapshot(testID, groupName, exec)
		}
	}
}

func (s *Service) publishExecutionSnapshot(testID string, groupName string, exec *groupExecution) {
	if exec == nil || !s.isCurrentExecution(testID, groupName, exec) {
		return
	}
	metrics, ok := s.collectExecutionMetrics(exec)
	if !ok {
		return
	}
	_ = s.publishMetricsForExecution(testID, groupName, exec, metrics.stats, metrics.transactions, metrics.telemetry, metrics.aggregates)
}

func (s *Service) collectExecutionMetrics(exec *groupExecution) (executionMetricsSnapshot, bool) {
	if exec == nil {
		return executionMetricsSnapshot{}, false
	}
	var snapshot engine.MetricsSnapshot
	snapshotOK := false
	if exec.engine != nil {
		snapshot, snapshotOK = exec.engine.LatestSnapshot()
	}
	var transactions []TransactionMetric
	var aggregateWindow *transactionWindowAggregate
	var window time.Duration
	if exec.results != nil {
		transactions, aggregateWindow, window = exec.results.DrainWindow()
	}
	if !snapshotOK && len(transactions) == 0 {
		exec.metricsMu.Lock()
		defer exec.metricsMu.Unlock()
		if exec.lastMetrics == nil {
			return executionMetricsSnapshot{}, false
		}
		return *exec.lastMetrics, true
	}
	stats := GeneratorStats{GoroutineCount: int32(runtime.NumGoroutine())}
	// The final out-of-band publish (after engine.Run returns) drains leftover
	// transactions over the sub-tick remainder since the last periodic drain. That
	// window can be a few milliseconds, which would yield an order-of-magnitude TPS
	// spike on the last data point. Rate a sub-half-cadence drain against the
	// normal publish cadence instead. Periodic drains are ~publishInterval, so
	// they are never clamped.
	windowMs := window.Milliseconds()
	if window > 0 && window < s.publishInterval/2 {
		windowMs = s.publishInterval.Milliseconds()
	}
	telemetry := MetricsTelemetry{WindowMs: windowMs}
	aggregates := aggregateWindow.finalize(windowMs)
	if snapshotOK {
		stats = generatorStatsFromResources(snapshot.Resources, int32(snapshot.ActiveVUsers))
		telemetry.CollectedAtUnixMs = snapshot.Timestamp.UnixMilli()
		now := time.Now()
		telemetry.PublishedAtUnixMs = now.UnixMilli()
		telemetry.SampleAgeMs = float64(now.Sub(snapshot.Timestamp).Microseconds()) / 1000.0
		telemetry.GeneratorProcessingLagMs = telemetry.SampleAgeMs
		if snapshot.Telemetry != nil {
			telemetry.DroppedResults = snapshot.Telemetry.DroppedResults
			telemetry.SheddedArrivals = snapshot.Telemetry.SheddedArrivals
		}
		mergeSnapshotSideChannelAggregates(&aggregates, snapshot)
	}
	telemetry.DroppedSubscriberBatches = s.droppedBatches.Load()
	metrics := executionMetricsSnapshot{stats: stats, transactions: transactions, telemetry: telemetry, aggregates: aggregates}
	exec.metricsMu.Lock()
	if exec.lastMetrics != nil && !metricsAggregateSetHasEvidence(metrics.aggregates) {
		metrics.transactions = exec.lastMetrics.transactions
		metrics.aggregates = exec.lastMetrics.aggregates
		if exec.lastMetrics.telemetry.WindowMs > 0 {
			metrics.telemetry.WindowMs = exec.lastMetrics.telemetry.WindowMs
		}
	}
	stored := metrics
	stored.transactions = append([]TransactionMetric(nil), metrics.transactions...)
	exec.lastMetrics = &stored
	exec.metricsMu.Unlock()
	return metrics, true
}

func metricsAggregateSetHasEvidence(aggregates metricsAggregateSet) bool {
	return aggregates.Fleet != nil || len(aggregates.Transactions) > 0 || len(aggregates.Dependencies) > 0 || len(aggregates.SOAP) > 0 || len(aggregates.NetworkProfiles) > 0 || aggregates.Browser != nil || len(aggregates.BrowserByDevice) > 0 || len(aggregates.Groups) > 0
}

func applyExecutionMetricsToAssignment(assignment *assignedTest, groupName string, metrics executionMetricsSnapshot) {
	if assignment == nil || strings.TrimSpace(groupName) == "" {
		return
	}
	if assignment.activeGroups == nil {
		assignment.activeGroups = make(map[string]int32)
	}
	if assignment.groupStats == nil {
		assignment.groupStats = make(map[string]GeneratorStats)
	}
	if assignment.groupTelemetry == nil {
		assignment.groupTelemetry = make(map[string]MetricsTelemetry)
	}
	if assignment.groupTransactions == nil {
		assignment.groupTransactions = make(map[string][]TransactionMetric)
	}
	if assignment.groupAggregates == nil {
		assignment.groupAggregates = make(map[string]metricsAggregateSet)
	}
	assignment.activeGroups[groupName] = metrics.stats.ActiveVUsers
	assignment.groupStats[groupName] = metrics.stats
	assignment.groupTelemetry[groupName] = metrics.telemetry
	if len(metrics.transactions) > 0 {
		transactions := append([]TransactionMetric(nil), metrics.transactions...)
		for index := range transactions {
			if strings.TrimSpace(transactions[index].Group) == "" {
				transactions[index].Group = groupName
			}
		}
		assignment.groupTransactions[groupName] = transactions
		boundRetainedTransactionSamples(assignment.groupTransactions, groupName)
	}
	if metricsAggregateSetHasEvidence(metrics.aggregates) {
		assignment.groupAggregates[groupName] = metrics.aggregates
	}
}

// captureStoppedExecutionMetrics drains evidence after detached executions have
// crossed their done barriers. Their normal final publisher is fenced by the
// missing assignment, so this is the sole owner of any remaining result window.
func (s *Service) captureStoppedExecutionMetrics(assignment *assignedTest, executions []*groupExecution) {
	if assignment == nil {
		return
	}
	for _, execution := range executions {
		metrics, ok := s.collectExecutionMetrics(execution)
		if !ok {
			continue
		}
		applyExecutionMetricsToAssignment(assignment, execution.groupName, metrics)
	}
}

// finishExecution is retained for internal lifecycle tests and non-engine
// callers that intentionally operate on whichever execution is current. Engine
// goroutines must use finishExecutionInstance with the exact pointer they own.
func (s *Service) finishExecution(testID string, groupName string, err error) {
	s.finishExecutionMatching(testID, groupName, nil, false, err)
}

func (s *Service) finishExecutionInstance(testID string, groupName string, exec *groupExecution, err error) {
	if exec == nil {
		return
	}
	s.finishExecutionMatching(testID, groupName, exec, true, err)
}

func (s *Service) finishExecutionMatching(testID string, groupName string, expected *groupExecution, requireMatch bool, err error) {
	s.mu.Lock()
	assignment, ok := s.assignments[testID]
	if !ok {
		s.mu.Unlock()
		return
	}
	if requireMatch {
		current := s.executions[testID][groupName]
		if current != expected || assignment.executionGeneration != expected.executionGeneration {
			s.mu.Unlock()
			return
		}
	}
	var siblingCancels []context.CancelFunc
	var terminalRedaction *engine.RedactionConfig
	if execs, ok := s.executions[testID]; ok {
		if current := execs[groupName]; current != nil {
			terminalRedaction = current.redactionConfig
		}
		delete(execs, groupName)
		if err != nil {
			// Fence the test immediately on its first terminal group failure. Waiting
			// until every sibling drains leaves a window in which AssignWork can add a
			// fresh group to a test whose outcome is already terminal.
			s.markStoppingLocked(testID, assignment.executionGeneration)
			if assignment.terminalError == "" {
				assignment.terminalError = scrubGeneratorTerminalError(err.Error(), terminalRedaction)
				for _, sibling := range execs {
					if sibling != nil && sibling.cancel != nil {
						siblingCancels = append(siblingCancels, sibling.cancel)
					}
				}
			}
		}
		if len(execs) == 0 {
			delete(s.executions, testID)
		}
	}
	if assignment.activeGroups != nil {
		assignment.activeGroups[groupName] = 0
	}
	hasRemainingExecutions := len(s.executions[testID]) > 0
	if hasRemainingExecutions {
		s.assignments[testID] = assignment
		s.mu.Unlock()
		for _, cancel := range siblingCancels {
			cancel()
		}
		return
	}
	s.markStoppingLocked(testID, assignment.executionGeneration)
	delete(s.assignments, testID)
	s.stopCompletionTimerLocked(testID)
	s.stopStageTimerLocked(testID)
	writer := s.archiveWriters[testID]
	delete(s.archiveWriters, testID)
	tracked := writer != nil && s.addFinalizer()
	s.mu.Unlock()
	for _, cancel := range siblingCancels {
		cancel()
	}

	finalState := "COMPLETE"
	finalError := assignment.terminalError
	if finalError != "" {
		finalState = "FAILED"
	}
	if tracked {
		defer s.finalizerWG.Done()
	}
	s.finishStoppedTestWithState(testID, assignment, writer, finalState, finalError)
}

const maxGeneratorTerminalErrorBytes = 16 << 10

func scrubGeneratorTerminalError(message string, config *engine.RedactionConfig) string {
	message = config.ScrubText(message)
	if len(message) <= maxGeneratorTerminalErrorBytes {
		return message
	}
	const suffix = "...[truncated]"
	return boundedtext.TruncateUTF8(message, maxGeneratorTerminalErrorBytes-len(suffix)) + suffix
}

func (s *Service) markStoppingLocked(testID string, generation uint64) *stopCompletion {
	s.stoppingTests[testID] = struct{}{}
	s.stoppingGenerations[testID] = generation
	if completion := s.stopCompletions[testID]; completion != nil {
		return completion
	}
	completion := &stopCompletion{generation: generation, done: make(chan struct{})}
	s.stopCompletions[testID] = completion
	return completion
}

// completeStoppingTest releases test-ID reuse only after the terminal batch has
// been retained. Duplicate StopTest calls wait on this barrier instead of
// treating an empty assignments map as proof that detached goroutines are done.
func (s *Service) completeStoppingTest(testID string, generation uint64) {
	s.mu.Lock()
	if trackedGeneration, ok := s.stoppingGenerations[testID]; !ok || trackedGeneration != generation {
		s.mu.Unlock()
		return
	}
	delete(s.stoppingTests, testID)
	delete(s.stoppingGenerations, testID)
	completion := s.stopCompletions[testID]
	if completion != nil && completion.generation == generation {
		delete(s.stopCompletions, testID)
		close(completion.done)
	}
	s.mu.Unlock()
}

func (s *Service) takeArchiveWriter(testID string) *archiveRecorder {
	s.mu.Lock()
	defer s.mu.Unlock()
	writer := s.archiveWriters[testID]
	delete(s.archiveWriters, testID)
	return writer
}

func (s *Service) storeArchiveRecord(record *storage.RunArchiveRecord) {
	s.storeArchiveRecordWithLifecycle(record)
}

func (s *Service) finalizeArchiveWriter(writer *archiveRecorder, status string, completedAt time.Time, errText string) error {
	if writer == nil {
		return nil
	}
	release := s.acquireArchiveFinalizerSlot()
	defer release()
	record, err := writer.Finalize(status, completedAt, errText)
	if err == nil && record != nil {
		err = s.storeArchiveRecordWithIdentity(record, writer.runIdentity)
	}
	if err != nil {
		discardErr := writer.Discard()
		removeErr := writer.removeFinalizedRun()
		if cleanupErr := errors.Join(discardErr, removeErr); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("remove unregistered generator archive: %w", cleanupErr))
		}
	}
	return err
}

func (s *Service) acquireArchiveFinalizerSlot() func() {
	if s == nil || s.archiveFinalizerSlots == nil {
		return func() {}
	}
	s.archiveFinalizerSlots <- struct{}{}
	var once sync.Once
	return func() {
		once.Do(func() { <-s.archiveFinalizerSlots })
	}
}

func redactionRulesFromProto(rules []*pb.RedactionRule) []engine.RedactionRule {
	if len(rules) == 0 {
		return nil
	}
	out := make([]engine.RedactionRule, 0, len(rules))
	for _, rule := range rules {
		out = append(out, engine.RedactionRule{
			Name:        rule.GetName(),
			Pattern:     rule.GetPattern(),
			Replacement: rule.GetReplacement(),
		})
	}
	return out
}

func mergeSnapshotSideChannelAggregates(out *metricsAggregateSet, snapshot engine.MetricsSnapshot) {
	if out == nil {
		return
	}
	out.NetworkProfiles = engineActionMetricsToAggregates(snapshot.ByNetworkProfile)
	out.Browser = browserMetricsFromEngine(snapshot.Browser)
	out.BrowserByDevice = browserDeviceMetricsFromEngine(snapshot.BrowserByDevice)
}

func engineActionMetricsToAggregates(values map[string]*engine.ActionMetrics) []MetricAggregate {
	if len(values) == 0 {
		return nil
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]MetricAggregate, 0, len(names))
	for _, name := range names {
		metrics := values[name]
		if metrics == nil || metrics.Count == 0 {
			continue
		}
		out = append(out, MetricAggregate{
			Name:             firstNonEmptyString(metrics.Name, name),
			DependencyType:   metrics.DependencyType,
			DependencyTarget: metrics.DependencyTarget,
			Count:            metrics.Count,
			Errors:           metrics.Errors,
			AvgMs:            float64(metrics.AvgDuration.Microseconds()) / 1000.0,
			P50Ms:            float64(metrics.P50.Microseconds()) / 1000.0,
			P90Ms:            float64(metrics.P90.Microseconds()) / 1000.0,
			P95Ms:            float64(metrics.P95.Microseconds()) / 1000.0,
			P99Ms:            float64(metrics.P99.Microseconds()) / 1000.0,
			AvgDNSMs:         metrics.AvgDNSMs,
			AvgConnectMs:     metrics.AvgConnectMs,
			AvgTLSMs:         metrics.AvgTLSMs,
			AvgTTFBMs:        metrics.AvgTTFBMs,
			BytesSent:        metrics.BytesSent,
			BytesReceived:    metrics.BytesRecv,
		})
	}
	return out
}

func browserMetricsFromEngine(metrics *engine.BrowserMetricsSummary) *BrowserMetrics {
	if metrics == nil || metrics.Samples == 0 {
		return nil
	}
	return &BrowserMetrics{
		Samples:              metrics.Samples,
		AvgFCPMs:             metrics.AvgFCPMs,
		AvgLCPMs:             metrics.AvgLCPMs,
		AvgCLSScore:          metrics.AvgCLSScore,
		AvgINPMs:             metrics.AvgINPMs,
		AvgTTFBMs:            metrics.AvgTTFBMs,
		AvgBlockingResources: metrics.AvgBlockingResources,
		AvgLongTaskCount:     metrics.AvgLongTaskCount,
		AvgJSHeapUsedMB:      metrics.AvgJSHeapUsedMB,
		AvgBrowserProcessCPU: metrics.AvgBrowserProcessCPU,
		AvgBrowserProcessMem: metrics.AvgBrowserProcessMem,
	}
}

func browserDeviceMetricsFromEngine(values map[string]*engine.BrowserDeviceMetrics) []BrowserDeviceMetrics {
	if len(values) == 0 {
		return nil
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]BrowserDeviceMetrics, 0, len(names))
	for _, name := range names {
		metrics := values[name]
		if metrics == nil || metrics.Samples == 0 {
			continue
		}
		out = append(out, BrowserDeviceMetrics{
			DeviceProfile:  firstNonEmptyString(metrics.DeviceProfile, name),
			DevicePlatform: metrics.DevicePlatform,
			DeviceTier:     metrics.DeviceTier,
			BrowserEngine:  metrics.BrowserEngine,
			Metrics: BrowserMetrics{
				Samples:              metrics.Samples,
				AvgFCPMs:             metrics.AvgFCPMs,
				AvgLCPMs:             metrics.AvgLCPMs,
				AvgCLSScore:          metrics.AvgCLSScore,
				AvgINPMs:             metrics.AvgINPMs,
				AvgTTFBMs:            metrics.AvgTTFBMs,
				AvgBlockingResources: metrics.AvgBlockingResources,
				AvgLongTaskCount:     metrics.AvgLongTaskCount,
				AvgJSHeapUsedMB:      metrics.AvgJSHeapUsedMB,
				AvgBrowserProcessCPU: metrics.AvgBrowserProcessCPU,
				AvgBrowserProcessMem: metrics.AvgBrowserProcessMem,
			},
		})
	}
	return out
}

func mergeMetricsAggregateSets(values map[string]metricsAggregateSet) metricsAggregateSet {
	if len(values) == 0 {
		return metricsAggregateSet{}
	}
	out := metricsAggregateSet{}
	fleet := make([]MetricAggregate, 0, len(values))
	transactionMetrics := make([]MetricAggregate, 0)
	dependencyMetrics := make([]MetricAggregate, 0)
	soapMetrics := make([]MetricAggregate, 0)
	networkProfileMetrics := make([]MetricAggregate, 0)
	groupMetrics := make([]GroupMetric, 0)
	browsers := make([]*BrowserMetrics, 0)
	browserDevices := make([]BrowserDeviceMetrics, 0)
	for _, value := range values {
		if value.Fleet != nil {
			fleet = append(fleet, *value.Fleet)
		}
		transactionMetrics = append(transactionMetrics, value.Transactions...)
		dependencyMetrics = append(dependencyMetrics, value.Dependencies...)
		soapMetrics = append(soapMetrics, value.SOAP...)
		networkProfileMetrics = append(networkProfileMetrics, value.NetworkProfiles...)
		groupMetrics = append(groupMetrics, value.Groups...)
		if value.Browser != nil {
			browsers = append(browsers, value.Browser)
		}
		browserDevices = append(browserDevices, value.BrowserByDevice...)
	}
	out.Fleet = mergeMetricAggregates(fleet)
	out.Transactions = mergeMetricAggregateSeries(transactionMetrics)
	out.Dependencies = mergeMetricAggregateSeries(dependencyMetrics)
	out.SOAP = mergeMetricAggregateSeries(soapMetrics)
	out.NetworkProfiles = mergeMetricAggregateSeries(networkProfileMetrics)
	out.Groups = mergeGroupMetricSeries(groupMetrics)
	out.Browser = mergeBrowserMetricSeries(browsers)
	out.BrowserByDevice = mergeBrowserDeviceMetricSeries(browserDevices)
	return out
}

func mergeMetricAggregateSeries(values []MetricAggregate) []MetricAggregate {
	if len(values) == 0 {
		return nil
	}
	byName := make(map[string][]MetricAggregate)
	for _, value := range values {
		name := strings.TrimSpace(value.Name)
		if name == "" {
			continue
		}
		byName[name] = append(byName[name], value)
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]MetricAggregate, 0, len(names))
	for _, name := range names {
		if aggregate := mergeMetricAggregates(byName[name]); aggregate != nil {
			out = append(out, *aggregate)
		}
	}
	return out
}

func mergeGroupMetricSeries(values []GroupMetric) []GroupMetric {
	if len(values) == 0 {
		return nil
	}
	byName := make(map[string][]MetricAggregate)
	for _, value := range values {
		if value.Metrics == nil || strings.TrimSpace(value.Name) == "" {
			continue
		}
		byName[value.Name] = append(byName[value.Name], *value.Metrics)
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]GroupMetric, 0, len(names))
	for _, name := range names {
		if aggregate := mergeMetricAggregates(byName[name]); aggregate != nil {
			out = append(out, GroupMetric{Name: name, Metrics: aggregate})
		}
	}
	return out
}

func mergeMetricAggregates(values []MetricAggregate) *MetricAggregate {
	if len(values) == 0 {
		return nil
	}
	out := MetricAggregate{}
	var durationDigest *engine.TDigest
	var weightedAvgMs, weightedDNSMs, weightedConnectMs, weightedTLSMs, weightedTTFBMs float64
	for _, value := range values {
		if value.Count == 0 {
			continue
		}
		if out.Name == "" {
			out.Name = value.Name
		}
		mergeStableString(&out.DependencyType, value.DependencyType)
		mergeStableString(&out.DependencyTarget, value.DependencyTarget)
		count := float64(value.Count)
		out.Count += value.Count
		out.Errors += value.Errors
		weightedAvgMs += value.AvgMs * count
		weightedDNSMs += value.AvgDNSMs * count
		weightedConnectMs += value.AvgConnectMs * count
		weightedTLSMs += value.AvgTLSMs * count
		weightedTTFBMs += value.AvgTTFBMs * count
		out.BytesSent += value.BytesSent
		out.BytesReceived += value.BytesReceived
		out.TPS += value.TPS
		if value.DurationDigest != nil && value.DurationDigest.Count > 0 {
			if durationDigest == nil {
				durationDigest = engine.NewTDigest(value.DurationDigest.Compression)
			}
			durationDigest.Merge(engine.NewTDigestFromSnapshot(*value.DurationDigest))
		}
	}
	if out.Count == 0 {
		return nil
	}
	divisor := float64(out.Count)
	out.AvgMs = weightedAvgMs / divisor
	out.AvgDNSMs = weightedDNSMs / divisor
	out.AvgConnectMs = weightedConnectMs / divisor
	out.AvgTLSMs = weightedTLSMs / divisor
	out.AvgTTFBMs = weightedTTFBMs / divisor
	if durationDigest != nil && durationDigest.Count() > 0 {
		out.P50Ms = durationDigest.Quantile(0.50) / float64(time.Millisecond)
		out.P90Ms = durationDigest.Quantile(0.90) / float64(time.Millisecond)
		out.P95Ms = durationDigest.Quantile(0.95) / float64(time.Millisecond)
		out.P99Ms = durationDigest.Quantile(0.99) / float64(time.Millisecond)
		snapshot := durationDigest.Snapshot()
		out.DurationDigest = &snapshot
	} else {
		out.P50Ms, out.P90Ms, out.P95Ms, out.P99Ms = fallbackMergedPercentiles(values)
	}
	return &out
}

func fallbackMergedPercentiles(values []MetricAggregate) (float64, float64, float64, float64) {
	if len(values) == 0 {
		return 0, 0, 0, 0
	}
	total := int64(0)
	for _, value := range values {
		total += value.Count
	}
	if total == 0 {
		return 0, 0, 0, 0
	}
	weighted := func(pick func(MetricAggregate) float64) float64 {
		sum := 0.0
		for _, value := range values {
			sum += pick(value) * float64(value.Count)
		}
		return sum / float64(total)
	}
	return weighted(func(v MetricAggregate) float64 { return v.P50Ms }),
		weighted(func(v MetricAggregate) float64 { return v.P90Ms }),
		weighted(func(v MetricAggregate) float64 { return v.P95Ms }),
		weighted(func(v MetricAggregate) float64 { return v.P99Ms })
}

func mergeBrowserMetricSeries(values []*BrowserMetrics) *BrowserMetrics {
	if len(values) == 0 {
		return nil
	}
	out := &BrowserMetrics{}
	for _, value := range values {
		if value == nil || value.Samples == 0 {
			continue
		}
		addBrowserMetrics(out, *value)
	}
	finalizeBrowserMetrics(out)
	if out.Samples == 0 {
		return nil
	}
	return out
}

func mergeBrowserDeviceMetricSeries(values []BrowserDeviceMetrics) []BrowserDeviceMetrics {
	if len(values) == 0 {
		return nil
	}
	byProfile := make(map[string]*BrowserDeviceMetrics)
	for _, value := range values {
		profile := strings.TrimSpace(value.DeviceProfile)
		if profile == "" || value.Metrics.Samples == 0 {
			continue
		}
		out := byProfile[profile]
		if out == nil {
			out = &BrowserDeviceMetrics{
				DeviceProfile:  profile,
				DevicePlatform: value.DevicePlatform,
				DeviceTier:     value.DeviceTier,
				BrowserEngine:  value.BrowserEngine,
			}
			byProfile[profile] = out
		}
		if out.DevicePlatform == "" {
			out.DevicePlatform = value.DevicePlatform
		}
		if out.DeviceTier == "" {
			out.DeviceTier = value.DeviceTier
		}
		if out.BrowserEngine == "" {
			out.BrowserEngine = value.BrowserEngine
		}
		addBrowserMetrics(&out.Metrics, value.Metrics)
	}
	profiles := make([]string, 0, len(byProfile))
	for profile := range byProfile {
		profiles = append(profiles, profile)
	}
	sort.Strings(profiles)
	out := make([]BrowserDeviceMetrics, 0, len(profiles))
	for _, profile := range profiles {
		metrics := byProfile[profile]
		finalizeBrowserMetrics(&metrics.Metrics)
		out = append(out, *metrics)
	}
	return out
}

func addBrowserMetrics(out *BrowserMetrics, value BrowserMetrics) {
	if out == nil || value.Samples == 0 {
		return
	}
	samples := float64(value.Samples)
	out.Samples += value.Samples
	out.AvgFCPMs += value.AvgFCPMs * samples
	out.AvgLCPMs += value.AvgLCPMs * samples
	out.AvgCLSScore += value.AvgCLSScore * samples
	out.AvgINPMs += value.AvgINPMs * samples
	out.AvgTTFBMs += value.AvgTTFBMs * samples
	out.AvgBlockingResources += value.AvgBlockingResources * samples
	out.AvgLongTaskCount += value.AvgLongTaskCount * samples
	out.AvgJSHeapUsedMB += value.AvgJSHeapUsedMB * samples
	out.AvgBrowserProcessCPU += value.AvgBrowserProcessCPU * samples
	out.AvgBrowserProcessMem += value.AvgBrowserProcessMem * samples
}

func finalizeBrowserMetrics(out *BrowserMetrics) {
	if out == nil || out.Samples == 0 {
		return
	}
	divisor := float64(out.Samples)
	out.AvgFCPMs /= divisor
	out.AvgLCPMs /= divisor
	out.AvgCLSScore /= divisor
	out.AvgINPMs /= divisor
	out.AvgTTFBMs /= divisor
	out.AvgBlockingResources /= divisor
	out.AvgLongTaskCount /= divisor
	out.AvgJSHeapUsedMB /= divisor
	out.AvgBrowserProcessCPU /= divisor
	out.AvgBrowserProcessMem /= divisor
}

func findGroupMetricAggregate(groups []GroupMetric, name string) *MetricAggregate {
	for _, group := range groups {
		if group.Name == name {
			return group.Metrics
		}
	}
	return nil
}

func archiveTotalsFromBatch(batch *pb.MetricsBatch) (errors int64, successes int64, totalDurationMs float64) {
	if aggregate := metricAggregateFromProto(batch.GetFleetMetrics()); aggregate != nil && aggregate.Count > 0 {
		errors = aggregate.Errors
		successes = aggregate.Count - aggregate.Errors
		totalDurationMs = aggregate.AvgMs * float64(aggregate.Count)
		return errors, successes, totalDurationMs
	}
	for _, tx := range batch.GetTransactions() {
		totalDurationMs += tx.GetDurationMs()
		if tx.GetSuccess() {
			successes++
		} else {
			errors++
		}
	}
	return errors, successes, totalDurationMs
}

func quantileFromMetricAggregate(aggregate *pb.MetricAggregate, transactions []*pb.TransactionMetric, percentile float64) float64 {
	if aggregate != nil && aggregate.GetDurationDigest() != nil && aggregate.GetDurationDigest().GetCount() > 0 {
		return engine.NewTDigestFromSnapshot(*tdigestSnapshotFromProto(aggregate.GetDurationDigest())).Quantile(percentile) / float64(time.Millisecond)
	}
	durations := make([]float64, 0, len(transactions))
	for _, tx := range transactions {
		durations = append(durations, tx.GetDurationMs())
	}
	return percentileValue(durations, percentile)
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

type combinedSink struct {
	sinks []engine.ResultSink
}

func combineSinks(sinks ...engine.ResultSink) engine.ResultSink {
	filtered := make([]engine.ResultSink, 0, len(sinks))
	for _, sink := range sinks {
		if sink != nil {
			filtered = append(filtered, sink)
		}
	}
	if len(filtered) == 1 {
		return filtered[0]
	}
	return &combinedSink{sinks: filtered}
}

func (s *combinedSink) RecordResult(result *protocol.Result) error {
	for _, sink := range s.sinks {
		if sink == nil {
			continue
		}
		if err := sink.RecordResult(result); err != nil {
			return err
		}
	}
	return nil
}

func (s *combinedSink) RecordTransaction(result *protocol.Result) error {
	for _, sink := range s.sinks {
		transactionSink, ok := sink.(engine.TransactionSink)
		if !ok {
			continue
		}
		if err := transactionSink.RecordTransaction(result); err != nil {
			return err
		}
	}
	return nil
}

func percentileValue(values []float64, percentile float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	if percentile <= 0 {
		return sorted[0]
	}
	if percentile >= 1 {
		return sorted[len(sorted)-1]
	}
	index := int(float64(len(sorted)-1) * percentile)
	return sorted[index]
}

func errorPct(errors int64, total int64) float64 {
	if total == 0 {
		return 0
	}
	return float64(errors) / float64(total) * 100
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}
