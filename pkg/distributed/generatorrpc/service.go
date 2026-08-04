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
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
)

type assignedTest struct {
	testID                 string
	lifecycle              assignmentLifecycleState
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
	maxGeneratorScenarioIDBytes        = 512
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
	if err := validateGeneratorFieldBytes(assignment.GetScenarioId(), "scenario_id", maxGeneratorScenarioIDBytes); err != nil {
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

	mu                      sync.RWMutex
	adjustMu                sync.Mutex
	generatorID             string
	maxVUsers               int32
	maxAssignedTests        int
	maxAssignedGroups       int
	maxExecutions           int
	maxHeartbeatStreams     int
	activeHeartbeatStreams  int
	podResources            podResourceCapacity
	scriptCache             *scriptCacheManager
	assignments             map[string]assignedTest
	executions              map[string]map[string]*groupExecution
	persistentJavaDrains    map[uint64]javaVuserCapacityDrain
	nextPersistentJavaDrain uint64
	stoppingTests           map[string]struct{}
	stoppingGenerations     map[string]uint64
	stopCompletions         map[string]*stopCompletion
	resCollector            *engine.ResourceCollector
	resCollectorMu          sync.Mutex // serializes resCollector.Collect (mutates delta state)
	executionEnabled        bool
	draining                bool
	publishInterval         time.Duration
	completionTimers        map[string]*time.Timer
	completionTokens        map[string]uint64
	nextCompletionToken     uint64
	stageTimers             map[string]*time.Timer
	stageTokens             map[string]uint64
	*executionEpochManager
	shutdown     chan struct{}
	shutdownOnce sync.Once

	// The legacy stopping map fields above alias coordinator-owned maps during
	// this incremental migration, preserving internal characterization seams.
	lifecycleCoordinator *assignmentLifecycleCoordinator
	metricsBroker        *metricsBroker
	archiveManager       *archiveManager
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
	if err := s.executionEpochManager.readinessErrorLocked(); err != nil {
		return err
	}
	if err := s.archiveManager.readinessError(); err != nil {
		return fmt.Errorf("generator archive namespace: %w", err)
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
	return s.executionEpochManager.changes()
}

func (s *Service) notifyReadinessChangedLocked() {
	if s != nil {
		s.executionEpochManager.notifyReadinessChanged()
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
		if s.lifecycleCoordinator != nil {
			s.lifecycleCoordinator.configureStageTTL(ttl)
		}
	}
}

func WithGeneratorMetricsSubscriberLimit(limit int) ServiceOption {
	return func(s *Service) {
		if s.metricsBroker != nil {
			s.metricsBroker.configureSubscriberLimit(limit)
		}
	}
}

// WithGeneratorControlStreamLimits bounds authenticated but potentially
// duplicated controller streams. It is primarily useful for constrained
// deployments and deterministic admission tests.
func WithGeneratorControlStreamLimits(archiveTransfers, archiveUnaryTransfers, heartbeatStreams int) ServiceOption {
	return func(s *Service) {
		if s.archiveManager != nil {
			s.archiveManager.configureTransferLimits(archiveTransfers, archiveUnaryTransfers)
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

	lifecycleCoordinator := newAssignmentLifecycleCoordinator()
	executionEpochManager := newExecutionEpochManager()
	archiveRecordMax := configuredGeneratorArchiveMax()
	archiveTTL := configuredGeneratorArchiveTTL()
	archiveManager := newArchiveManager(generatorID, archiveRecordMax, archiveTTL, 1024, defaultMaxArchiveTransfers, defaultMaxArchiveUnaryTransfers)
	service := &Service{
		generatorID:           generatorID,
		maxVUsers:             int32(maxVUsers),
		maxAssignedTests:      1024,
		maxAssignedGroups:     4096,
		maxExecutions:         4096,
		maxHeartbeatStreams:   defaultMaxHeartbeatStreams,
		podResources:          detectPodResourceCapacity(int32(maxVUsers)),
		scriptCache:           newScriptCacheManager(configuredGeneratorScriptCacheMax(), configuredGeneratorScriptCacheMaxBytes()),
		assignments:           make(map[string]assignedTest),
		executions:            make(map[string]map[string]*groupExecution),
		persistentJavaDrains:  make(map[uint64]javaVuserCapacityDrain),
		lifecycleCoordinator:  lifecycleCoordinator,
		stoppingTests:         lifecycleCoordinator.stoppingTests,
		stoppingGenerations:   lifecycleCoordinator.stoppingGenerations,
		stopCompletions:       lifecycleCoordinator.stopCompletions,
		resCollector:          engine.NewResourceCollector(),
		publishInterval:       time.Second,
		completionTimers:      make(map[string]*time.Timer),
		completionTokens:      make(map[string]uint64),
		stageTimers:           lifecycleCoordinator.stageTimers,
		stageTokens:           lifecycleCoordinator.stageTokens,
		executionEpochManager: executionEpochManager,
		shutdown:              make(chan struct{}),
		metricsBroker:         newMetricsBroker(defaultMaxMetricsSubscribers, archiveRecordMax, archiveTTL),
		archiveManager:        archiveManager,
	}
	if _, archiveNamespaceErr := archiveManager.namespaceSnapshot(); archiveNamespaceErr != nil {
		slog.Error("initialize generator archive namespace", "generator_id", generatorID, "error", archiveNamespaceErr)
	}
	for _, opt := range opts {
		if opt != nil {
			opt(service)
		}
	}
	archiveRecordMax, archiveTTL = service.archiveManager.retention()
	service.metricsBroker.configureTerminalRetention(archiveRecordMax, archiveTTL)
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
	var failures []error
	epochQuiesced := true
	if err := s.executionEpochManager.waitForPersistence(ctx, s); err != nil {
		epochQuiesced = false
		failures = append(failures, fmt.Errorf("wait for execution epoch persistence: %w", err))
	}
	if err := s.waitForAllPendingStages(ctx); err != nil {
		failures = append(failures, fmt.Errorf("wait for pending generator assignments: %w", err))
	}
	if err := s.waitForAllPendingControls(ctx); err != nil {
		failures = append(failures, fmt.Errorf("wait for pending generator controls: %w", err))
	}
	s.mu.RLock()
	testIDs := make([]string, 0, len(s.assignments))
	for testID := range s.assignments {
		testIDs = append(testIDs, testID)
	}
	s.mu.RUnlock()
	sort.Strings(testIDs)
	// Detach and cancel the whole fleet before waiting for any one engine. A
	// protocol that is slow to honor cancellation must not leave later tests
	// generating load until the process-level timeout expires.
	if err := s.forceStopTests(ctx, testIDs, ^uint64(0), "FAILED", "generator shut down before test completion"); err != nil {
		failures = append(failures, fmt.Errorf("stop active generator tests: %w", err))
	}
	s.archiveManager.beginShutdown()
	s.shutdownOnce.Do(func() { close(s.shutdown) })
	s.mu.Lock()
	s.executionEpochManager.stopTimerLocked()
	var epochState *executionEpochStateStore
	if epochQuiesced {
		epochState = s.executionEpochManager.stateForCloseLocked()
	}
	s.mu.Unlock()
	done := s.archiveManager.finalizersDone()
	select {
	case <-done:
	case <-ctx.Done():
		failures = append(failures, fmt.Errorf("wait for generator finalizers: %w", ctx.Err()))
	}
	if epochState != nil {
		if err := epochState.close(); err != nil {
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
	assignment = s.lifecycleCoordinator.finish(assignment)
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
	return s.archiveManager.beginTransfer(unary)
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
		persistentJavaReserved := s.heartbeatJavaVuserCapacityInUseLocked()
		javaOccupancyMode := s.heartbeatJavaVuserOccupancyModeLocked()
		s.mu.RUnlock()
		stats := generatorStatsFromResources(s.collectResources(), active)
		supportedProtocols, compatibleFeatures, runtimeCapacities := runtimeAdvertisementWithCapacities(
			stream.Context(),
			true,
			map[string]int64{"java_vuser": persistentJavaReserved},
			map[string]string{"java_vuser": javaOccupancyMode},
		)
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
			SupportedProtocols: supportedProtocols,
			CompatibleFeatures: compatibleFeatures,
			RuntimeCapacities:  runtimeCapacities,
		}); err != nil {
			return err
		}
	}
}

func supportedProtocolNames() []string {
	names, _ := runtimeAdvertisement(context.Background(), false)
	return names
}

func compatibleFeatureNames() []string {
	_, features := runtimeAdvertisement(context.Background(), false)
	return features
}

func supportedProtocolNamesForHeartbeat(ctx context.Context) []string {
	names, _ := runtimeAdvertisement(ctx, true)
	return names
}

func compatibleFeatureNamesForHeartbeat(ctx context.Context) []string {
	_, features := runtimeAdvertisement(ctx, true)
	return features
}

// runtimeAdvertisement computes the protocol and feature lists from one
// capability/health observation per protocol. Heartbeats must not build these
// lists independently: a runner that changes health between two probes could
// otherwise advertise a feature without its owning protocol (or vice versa).
func runtimeAdvertisement(ctx context.Context, requireHealthy bool) ([]string, []string) {
	protocols, features, _ := runtimeAdvertisementWithCapacities(ctx, requireHealthy, nil, nil)
	return protocols, features
}

// runtimeAdvertisementWithCapacities extends runtimeAdvertisement with the
// protocol-specific ceilings reported by RuntimeCapabilityProvider. inUse is a
// generator-owned reservation snapshot captured before any external runtime
// probe, so heartbeat generation never holds the service state lock while
// invoking sidecars or runner health checks. occupancyModes comes from the same
// state snapshot and is optional for non-session runtimes.
func runtimeAdvertisementWithCapacities(ctx context.Context, requireHealthy bool, inUse map[string]int64, occupancyModes map[string]string) ([]string, []string, []*pb.ProtocolRuntimeCapacity) {
	if ctx == nil {
		ctx = context.Background()
	}
	names := protocol.List()
	sort.Strings(names)
	available := make([]string, 0, len(names))
	features := []string{FeatureEvacuationAssignmentStateV1, FeatureSecretBundleV1}
	runtimeCapacities := make([]*pb.ProtocolRuntimeCapacity, 0)
	for _, name := range names {
		normalized := strings.ToLower(strings.TrimSpace(name))
		if normalized == "" {
			continue
		}
		if normalized == "java_vuser" && !config.JavaVuserRunnerEnabled() {
			runtimeCapacities = append(runtimeCapacities, &pb.ProtocolRuntimeCapacity{
				Protocol:      normalized,
				OccupancyMode: occupancyModes[normalized],
			})
			continue
		}
		instance, err := protocol.Get(normalized)
		if err != nil {
			continue
		}
		capabilities, capabilitySupported, capabilityErr := protocol.ReadRuntimeCapabilities(instance)
		runtimeAvailable := !capabilitySupported || capabilities.Available
		if capabilityErr != nil {
			runtimeAvailable = false
		}
		if requireHealthy && runtimeAvailable {
			healthy, healthSupported, healthErr := protocol.CheckRuntimeHealth(ctx, instance)
			if healthErr != nil || healthSupported && !healthy {
				runtimeAvailable = false
			}
		}
		if capabilitySupported {
			used := inUse[normalized]
			if used < 0 {
				used = 0
			}
			maxVUsers := max(capabilities.MaxVUsers, 0)
			remaining := int64(0)
			if runtimeAvailable && used < maxVUsers {
				remaining = maxVUsers - used
			}
			runtimeCapacities = append(runtimeCapacities, &pb.ProtocolRuntimeCapacity{
				Protocol:        normalized,
				Available:       runtimeAvailable,
				MaxVusers:       maxVUsers,
				AvailableVusers: remaining,
				Features:        sortedUniqueRuntimeNames(capabilities.Features),
				OccupancyMode:   occupancyModes[normalized],
			})
		}
		if !runtimeAvailable {
			continue
		}
		available = append(available, normalized)
		features = append(features, normalized)
		if capabilitySupported {
			features = append(features, capabilities.Features...)
		}
	}
	return sortedUniqueRuntimeNames(available), sortedUniqueRuntimeNames(features), runtimeCapacities
}

func sortedUniqueRuntimeNames(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		key := strings.ToLower(value)
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
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
			if cached, ok := s.scriptCache.get(scriptID); ok && strings.EqualFold(cached.protocol, protocolName) {
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
	return collectSystemResourcesSafely(s.resCollector.Collect)
}

// collectSystemResourcesSafely prevents a platform metrics parser panic from
// terminating the generator's gRPC server. Resource telemetry is advisory; a
// failed sample degrades to assignment-derived VUser counts and a later
// heartbeat gets another chance to collect host metrics.
func collectSystemResourcesSafely(collect func() *engine.SystemResources) (resources *engine.SystemResources) {
	if collect == nil {
		return nil
	}
	defer func() {
		if recover() != nil {
			resources = nil
		}
	}()
	return collect()
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
	reserved := int64(0)
	if s.lifecycleCoordinator != nil {
		reserved = s.lifecycleCoordinator.pendingVUsersLocked()
	}
	available := int64(s.maxVUsers) - s.activeVUsers64Locked() - reserved
	if available <= 0 {
		return 0
	}
	if available > int64(s.maxVUsers) {
		return s.maxVUsers
	}
	return int32(available)
}

func (s *Service) FailTest(testID string, message string) {
	s.mu.Lock()
	assignment, ok := s.assignments[testID]
	persistentJavaDrainToken := uint64(0)
	if ok {
		s.markStoppingLocked(testID, assignment.executionGeneration)
		persistentJavaDrainToken = s.reserveAssignmentJavaDrainLocked(assignment)
		delete(s.assignments, testID)
	}
	execs := s.executions[testID]
	delete(s.executions, testID)
	writer := s.archiveManager.takeWriter(testID)
	trackedFinalizer := writer != nil && s.archiveManager.addFinalizer()
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
			s.archiveManager.finalizerDone()
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
				s.releaseStopDrain(persistentJavaDrainToken)
				s.captureStoppedExecutionMetrics(&assignment, executions)
				s.finishFailedTest(testID, assignment, writer, message)
			}()
			return
		}
		go func() {
			defer s.archiveManager.finalizerDone()
			_ = waitForGeneratorExecutions(context.Background(), executions)
			s.releaseStopDrain(persistentJavaDrainToken)
			s.captureStoppedExecutionMetrics(&assignment, executions)
			s.finishFailedTest(testID, assignment, writer, message)
		}()
		return
	}
	if trackedFinalizer {
		defer s.archiveManager.finalizerDone()
	}
	s.releaseStopDrain(persistentJavaDrainToken)
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
	s.lifecycleCoordinator.scheduleStageExpiryLocked(s, testID)
}

func (s *Service) stopStageTimerLocked(testID string) {
	s.lifecycleCoordinator.stopStageTimerLocked(testID)
}

func (s *Service) expireStagedAssignment(testID string, token uint64) {
	s.mu.Lock()
	if !s.lifecycleCoordinator.claimStageExpiryLocked(s, testID, token) {
		s.mu.Unlock()
		return
	}
	// Claim teardown before releasing the lock so a concurrent CommitWork cannot
	// release staged traffic after the expiry decision.
	s.markStoppingLocked(testID, s.assignments[testID].executionGeneration)
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
	s.metricsBroker.lockPublication()
	defer s.metricsBroker.unlockPublication()

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
	writer := s.archiveManager.writer(testID)
	recordArchive := !requireMatch || s.isArchivePublisherLocked(testID, groupName)
	s.mu.Unlock()

	if recordArchive {
		s.recordMetricsBatch(testID, writer, batch)
	}

	s.broadcastMetricsLocked(batch)
	return nil
}

func (s *Service) isArchivePublisherLocked(testID, groupName string) bool {
	groups := s.executions[testID]
	if len(groups) == 0 {
		return true
	}
	selected := ""
	for candidate := range groups {
		if selected == "" || candidate < selected {
			selected = candidate
		}
	}
	return groupName == selected
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
		Timestamp:       time.UnixMilli(batch.TimestampUnixMs),
		ActiveVUsers:    int64(batch.GetStats().GetActiveVusers()),
		TPS:             float64(totalSuccess) / windowSecondsFromMs(batch.GetWindowMs()),
		AvgResponseMs:   avgResponse,
		ErrorPct:        errorPct(totalErrors, totalSuccess+totalErrors),
		TotalTx:         totalSuccess,
		TotalErrors:     totalErrors,
		P50Ms:           quantileFromMetricAggregate(batch.FleetMetrics, batch.Transactions, 0.50),
		P90Ms:           quantileFromMetricAggregate(batch.FleetMetrics, batch.Transactions, 0.90),
		P95Ms:           quantileFromMetricAggregate(batch.FleetMetrics, batch.Transactions, 0.95),
		P99Ms:           quantileFromMetricAggregate(batch.FleetMetrics, batch.Transactions, 0.99),
		Groups:          archiveSummaryGroupsFromBatch(batch),
		Browser:         archiveSummaryBrowserFromBatch(batch),
		BrowserByDevice: archiveSummaryBrowserDevicesFromBatch(batch),
		Telemetry:       archiveSummaryTelemetryFromBatch(batch),
	})
	if archiveErr := writer.claimRecordingFailure(); archiveErr != nil {
		// The engine's result-sink contract is non-blocking and intentionally
		// ignores returned sink errors. Escalate the recorder's sticky failure
		// out-of-band: calling FailTest inline from the publisher could wait on
		// this same execution's completion barrier.
		go s.FailTest(testID, "generator archive recording failed: "+archiveErr.Error())
	}
}

func archiveSummaryGroupsFromBatch(batch *pb.MetricsBatch) map[string]*archiveGroupLiveMetrics {
	if batch == nil || len(batch.GetGroups()) == 0 {
		return nil
	}
	groups := make(map[string]*archiveGroupLiveMetrics, len(batch.GetGroups()))
	windowSeconds := windowSecondsFromMs(batch.GetWindowMs())
	for _, group := range batch.GetGroups() {
		name := strings.TrimSpace(group.GetName())
		if name == "" {
			continue
		}
		out := &archiveGroupLiveMetrics{
			Name: name, VUsers: int(group.GetActiveVusers()),
			PercentileQuality: "source_generator",
		}
		if aggregate := metricAggregateFromProto(group.GetMetrics()); aggregate != nil {
			out.Transactions = aggregate.Count - aggregate.Errors
			if out.Transactions < 0 {
				out.Transactions = 0
			}
			out.Errors = aggregate.Errors
			out.TPS = aggregate.TPS
			if out.TPS <= 0 && out.Transactions > 0 {
				out.TPS = float64(out.Transactions) / windowSeconds
			}
			out.AvgResponseMs = aggregate.AvgMs
			out.P50Ms = aggregate.P50Ms
			out.P90Ms = aggregate.P90Ms
			out.P95Ms = aggregate.P95Ms
			out.P99Ms = aggregate.P99Ms
		}
		groups[name] = out
	}
	if len(groups) == 0 {
		return nil
	}
	return groups
}

func archiveSummaryBrowserFromBatch(batch *pb.MetricsBatch) *archiveBrowserLiveMetrics {
	if batch == nil {
		return nil
	}
	metrics := browserMetricsFromProto(batch.GetBrowserMetrics())
	if metrics == nil {
		return nil
	}
	return &archiveBrowserLiveMetrics{
		Samples: metrics.Samples, AvgFCPMs: metrics.AvgFCPMs, AvgLCPMs: metrics.AvgLCPMs,
		AvgCLSScore: metrics.AvgCLSScore, AvgINPMs: metrics.AvgINPMs, AvgTTFBMs: metrics.AvgTTFBMs,
		AvgBlockingResources: metrics.AvgBlockingResources, AvgLongTaskCount: metrics.AvgLongTaskCount,
		AvgJSHeapUsedMB: metrics.AvgJSHeapUsedMB, AvgBrowserProcessCPU: metrics.AvgBrowserProcessCPU,
		AvgBrowserProcessMem: metrics.AvgBrowserProcessMem,
	}
}

func archiveSummaryBrowserDevicesFromBatch(batch *pb.MetricsBatch) map[string]*archiveBrowserDeviceMetric {
	if batch == nil {
		return nil
	}
	values := browserDeviceMetricsFromProto(batch.GetBrowserDeviceMetrics())
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]*archiveBrowserDeviceMetric, len(values))
	for _, value := range values {
		profile := strings.TrimSpace(value.DeviceProfile)
		if profile == "" || value.Metrics.Samples <= 0 {
			continue
		}
		metrics := value.Metrics
		out[profile] = &archiveBrowserDeviceMetric{
			DeviceProfile: profile, DevicePlatform: value.DevicePlatform, DeviceTier: value.DeviceTier, BrowserEngine: value.BrowserEngine,
			Samples: metrics.Samples, AvgFCPMs: metrics.AvgFCPMs, AvgLCPMs: metrics.AvgLCPMs,
			AvgCLSScore: metrics.AvgCLSScore, AvgINPMs: metrics.AvgINPMs, AvgTTFBMs: metrics.AvgTTFBMs,
			AvgBlockingResources: metrics.AvgBlockingResources, AvgLongTaskCount: metrics.AvgLongTaskCount,
			AvgJSHeapUsedMB: metrics.AvgJSHeapUsedMB, AvgBrowserProcessCPU: metrics.AvgBrowserProcessCPU,
			AvgBrowserProcessMem: metrics.AvgBrowserProcessMem,
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func archiveSummaryTelemetryFromBatch(batch *pb.MetricsBatch) *archiveTelemetryHealth {
	if batch == nil || batch.GetTelemetry() == nil {
		return nil
	}
	telemetry := batch.GetTelemetry()
	out := &archiveTelemetryHealth{
		DroppedResults: telemetry.GetDroppedResults(), SheddedArrivals: telemetry.GetSheddedArrivals(),
		DroppedSubscriberBatches: telemetry.GetDroppedSubscriberBatches(),
		GeneratorProcessingLagMs: telemetry.GetGeneratorProcessingLagMs(), TransportLagMs: telemetry.GetTransportLagMs(),
		SampleAgeMs: telemetry.GetSampleAgeMs(),
	}
	if telemetry.GetCollectedAtUnixMs() > 0 {
		out.CollectedAt = time.UnixMilli(telemetry.GetCollectedAtUnixMs())
	}
	if telemetry.GetPublishedAtUnixMs() > 0 {
		out.GeneratorPublishedAt = time.UnixMilli(telemetry.GetPublishedAtUnixMs())
	}
	return out
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
	s.metricsBroker.lockPublication()
	defer s.metricsBroker.unlockPublication()
	s.mu.Lock()
	if err := s.validateMutationGenerationLocked(executionGeneration); err != nil {
		s.mu.Unlock()
		return 0, nil, nil, err
	}
	live := make([]*pb.MetricsBatch, 0, len(s.assignments))
	for assignmentTestID, assignment := range s.assignments {
		if assignment.executionGeneration == executionGeneration && (testID == "" || assignmentTestID == testID) {
			live = append(live, s.metricsBatchLocked(assignmentTestID))
		}
	}
	s.mu.Unlock()
	return s.metricsBroker.registerLocked(executionGeneration, testID, live, time.Now())
}

func (s *Service) unregisterSubscriber(id int) {
	s.metricsBroker.unregister(id)
}

func (s *Service) broadcastMetrics(batch *pb.MetricsBatch) {
	s.metricsBroker.publish(batch, s.metricsBatchIsCurrent)
}

// publishLifecycleTransitionLocked publishes a control-plane lifecycle
// transition while the broker publication lock is held by the state mutation
// that created it.
func (s *Service) publishLifecycleTransitionLocked(batch *pb.MetricsBatch) {
	s.metricsBroker.publishLifecycleLocked(batch, s.metricsBatchIsCurrent)
}

func (s *Service) broadcastMetricsLocked(batch *pb.MetricsBatch) {
	s.metricsBroker.publishLocked(batch, s.metricsBatchIsCurrent)
}

func (s *Service) metricsBatchIsCurrent(batch *pb.MetricsBatch) bool {
	s.mu.RLock()
	current := s.metricsBatchIsCurrentLocked(batch)
	s.mu.RUnlock()
	return current
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
	if s.metricsBroker.hasTerminal(key) {
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
	s.metricsBroker.publishTerminal(batch)
}

func (s *Service) terminalGenerationLocked(testID string) (uint64, bool) {
	return s.metricsBroker.terminalGeneration(testID)
}

func (s *Service) deleteTerminalBatchesForTestLocked(testID string) {
	s.metricsBroker.deleteTerminalsForTest(testID)
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
	writer := s.archiveManager.takeWriter(testID)
	tracked := ok && writer != nil && s.archiveManager.addFinalizer()
	s.mu.Unlock()
	if !ok {
		return
	}
	if tracked {
		defer s.archiveManager.finalizerDone()
	}
	s.finishStoppedTest(testID, assignment, writer)
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
		idle := *exec.lastMetrics
		idle.transactions = nil
		idle.aggregates = metricsAggregateSet{}
		idle.telemetry.WindowMs = s.publishInterval.Milliseconds()
		idle.telemetry.PublishedAtUnixMs = time.Now().UnixMilli()
		return idle, true
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
	telemetry.DroppedSubscriberBatches = s.metricsBroker.droppedCount()
	metrics := executionMetricsSnapshot{stats: stats, transactions: transactions, telemetry: telemetry, aggregates: aggregates}
	exec.metricsMu.Lock()
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
	} else {
		delete(assignment.groupTransactions, groupName)
	}
	if metricsAggregateSetHasEvidence(metrics.aggregates) {
		assignment.groupAggregates[groupName] = metrics.aggregates
	} else {
		delete(assignment.groupAggregates, groupName)
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
	writer := s.archiveManager.takeWriter(testID)
	tracked := writer != nil && s.archiveManager.addFinalizer()
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
		defer s.archiveManager.finalizerDone()
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
	assignment, assigned := s.assignments[testID]
	assignment, completion := s.lifecycleCoordinator.beginStopping(testID, generation, assignment)
	if assigned {
		s.assignments[testID] = assignment
	}
	return completion
}

// completeStoppingTest releases test-ID reuse only after the terminal batch has
// been retained. Duplicate StopTest calls wait on this barrier instead of
// treating an empty assignments map as proof that detached goroutines are done.
func (s *Service) completeStoppingTest(testID string, generation uint64) {
	s.mu.Lock()
	s.lifecycleCoordinator.completeStopping(testID, generation)
	s.mu.Unlock()
}

func (s *Service) takeArchiveWriter(testID string) *archiveRecorder {
	return s.archiveManager.takeWriter(testID)
}

func (s *Service) storeArchiveRecord(record *storage.RunArchiveRecord) {
	s.storeArchiveRecordWithLifecycle(record)
}

func (s *Service) finalizeArchiveWriter(writer *archiveRecorder, status string, completedAt time.Time, errText string) error {
	if writer == nil {
		return nil
	}
	release := s.archiveManager.acquireFinalizerSlot()
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
