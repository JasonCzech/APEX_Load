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
	"github.com/jasonczech/apexload/pkg/javasidecar"
	"github.com/jasonczech/apexload/pkg/protocol"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
	"github.com/jasonczech/apexload/pkg/storage"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
)

const FeatureIterationTerminationV1 = "iteration_termination_v1"
const FeatureRunCorrelationV1 = "run_correlation_v1"

// FeatureCorrelationPolicyV2 accepts correlation carriers beyond the run
// marker (traceparent, transaction_marker) for the approved origins.
const FeatureCorrelationPolicyV2 = "correlation_policy_v2"

// FeatureExemplarsV1 sends each transaction's trace exemplars on live
// aggregates. It is passive evidence: controllers fall back to correlation IDs
// from peers without it.
const FeatureExemplarsV1 = "exemplars_v1"

// FeatureMessagingTraceCarrierV1 honors config.observability.trace_carrier on
// messaging protocols. A run whose scripts opt in fails before traffic on
// generators without it, because older generators would silently send nothing.
const FeatureMessagingTraceCarrierV1 = "messaging_trace_carrier_v1"

type assignedTest struct {
	sourcePlan                *pb.SourcePlanManifest
	terminationMode           string
	testID                    string
	lifecycle                 assignmentLifecycleState
	executionGeneration       uint64
	requireTestExecutionLease bool
	runSupervisionTokenHash   [sha256.Size]byte
	archiveSourceTarget       string
	// adoptionGrace > 0 opts this run into HA continuity: on controller epoch
	// expiry it is orphaned (keeps running) until orphanedUntil instead of being
	// fenced, awaiting a successor's RunAdoption.
	adoptionGrace                   time.Duration
	orphanedUntil                   time.Time
	vuserCount                      int32
	paused                          bool
	pauseOperationID                string
	pauseOperationSequence          uint64
	duration                        time.Duration
	durationSeconds                 int64
	prepareOnly                     bool
	preparationCapacityClaim        *pb.PodCapacityClaim
	committed                       bool
	groups                          map[string]int32
	activeGroups                    map[string]int32
	groupStats                      map[string]GeneratorStats
	groupTelemetry                  map[string]MetricsTelemetry
	groupTransactions               map[string][]TransactionMetric
	groupAggregates                 map[string]metricsAggregateSet
	groupTransactionResponseTimeSLA map[string][]engine.TransactionResponseTimeSLACompliance
	groupHTTPVersions               map[string]*protocol.HTTPVersionMix // run-to-date, per group
	groupAssignments                map[string][sha256.Size]byte
	groupSecretBundleIDs            map[string]string
	groupScripts                    map[string]string
	groupJavaHosts                  map[string]string
	groupJavaHostPrepared           map[string]int32
	groupRuntimeResources           map[string]protocol.RuntimeResourceReservation
	groupRuntimeResourceClaims      map[string]*pb.RuntimeResourceClaim
	groupRuntimeResourcePeaks       map[string]int32
	terminalError                   string

	// metricsCollection identifies the collection that produced the window in
	// groupTransactions and groupAggregates; every batch built from the
	// assignment carries it, so a re-sent window is recognizable.
	metricsCollection uint64
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
	javaHost            bool
	owner               *sourcePlanExecution
	stagedConfig        *engine.MultiGroupConfig
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
	stopOnce        sync.Once
	results         *transactionBuffer
	publisherDone   chan struct{}
	done            chan struct{}
	metricsMu       sync.Mutex
	lastMetrics     *executionMetricsSnapshot
}

type executionMetricsSnapshot struct {
	stats                      GeneratorStats
	transactions               []TransactionMetric
	telemetry                  MetricsTelemetry
	aggregates                 metricsAggregateSet
	transactionResponseTimeSLA []engine.TransactionResponseTimeSLACompliance
	httpVersions               *protocol.HTTPVersionMix
	// sourceSnapshotAt identifies the engine snapshot whose gauges and
	// side-channel aggregates were projected into this window. LatestSnapshot
	// returns the cached latest value, so the timestamp fences that evidence from
	// being emitted again by a second final callback or a faster publisher tick.
	sourceSnapshotAt    time.Time
	sourceSnapshotValid bool
}

const (
	maxGeneratorDurationSeconds        = int64(^uint64(0)>>1) / int64(time.Second)
	maxGeneratorVUsers                 = int64(^uint32(0) >> 1)
	maxGeneratorGroupNameBytes         = 512
	maxGeneratorScriptIDBytes          = 512
	maxGeneratorScenarioIDBytes        = 512
	maxGeneratorRegionBytes            = 512
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
	if err := httpprotocol.ValidateManagedRunCorrelation(assignment.GetTestId(), assignment.GetRunCorrelationOrigins(), assignment.GetCorrelationCarriers()); err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	if err := validateGeneratorFieldBytes(assignment.GetCorrelationTestName(), "correlation_test_name", 256); err != nil {
		return err
	}
	if _, err := jointProjectionFromProto(assignment.GetJointEvidenceProjection()); err != nil {
		return status.Error(codes.InvalidArgument, "joint_evidence_projection: "+err.Error())
	}
	if err := validateJavaHostSelection(assignment); err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	if err := validateGeneratorFieldBytes(assignment.GetGroupName(), "group_name", maxGeneratorGroupNameBytes); err != nil {
		return err
	}
	if err := validateGeneratorFieldBytes(assignment.GetScriptId(), "script_id", maxGeneratorScriptIDBytes); err != nil {
		return err
	}
	if err := validateGeneratorFieldBytes(assignment.GetScenarioId(), "scenario_id", maxGeneratorScenarioIDBytes); err != nil {
		return err
	}
	if err := validateGeneratorFieldBytes(assignment.GetRegion(), "region", maxGeneratorRegionBytes); err != nil {
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
	BrowserDocuments  *protocol.BrowserDocumentSummary
	MessagingProgress *protocol.MessagingProgressSnapshot
	NativeVersion     uint32
	Fleet             *MetricAggregate
	Transactions      []MetricAggregate
	Dependencies      []MetricAggregate
	SOAP              []MetricAggregate
	NetworkProfiles   []MetricAggregate
	Actions           []MetricAggregate
	ActionDimensions  []MetricAggregate
	Joint             []MetricAggregate
	JointInfo         *JointMetricsInfo
	ActionProperties  []MetricAggregate
	Properties        []MetricAggregate
	Regions           []MetricAggregate
	Browser           *BrowserMetrics
	BrowserByDevice   []BrowserDeviceMetrics
	Groups            []GroupMetric
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
		Region:           result.Region,
		NetworkProfile:   result.NetworkProfile,
		Hierarchy:        result.Hierarchy,
		Phase:            result.Phase,
		PageName:         result.PageName,
		DurationMs:       float64(result.Duration) / float64(time.Millisecond),
		WorkerDurationMs: float64(result.WorkerDuration) / float64(time.Millisecond),
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
	return b.drainWindow(true)
}

// drainWindow optionally leaves an empty window untouched. Periodic publishers
// advance idle windows to preserve cadence, while out-of-band final drains must
// not split a sibling's rate window unless they actually publish new evidence.
func (b *transactionBuffer) drainWindow(advanceEmpty bool) ([]TransactionMetric, *transactionWindowAggregate, time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !advanceEmpty && len(b.transactions) == 0 && b.window.empty() {
		return nil, nil, 0
	}
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
	javaHostRuntime   *javasidecar.BrokerRuntime
	javaHostApprovals map[string]JavaHostApproval
	pb.UnimplementedGeneratorControlServer

	mu          sync.RWMutex
	adjustMu    sync.Mutex
	generatorID string
	// now is the service's time source. Execution-epoch expiry reads it, so
	// tests can drive lapse deterministically via WithClock. Always call
	// nowTime() rather than this field directly; it may be nil.
	now                                  func() time.Time
	maxVUsers                            int32
	maxAssignedTests                     int
	maxAssignedGroups                    int
	maxExecutions                        int
	maxHeartbeatStreams                  int
	activeHeartbeatStreams               int
	podResources                         podResourceCapacity
	scriptCache                          *scriptCacheManager
	assignments                          map[string]assignedTest
	podCapacityClaims                    map[string]*pb.PodCapacityClaim
	podCapacityIncarnation               string
	executions                           map[string]map[string]*groupExecution
	persistentJavaDrains                 map[uint64]javaVuserCapacityDrain
	nextPersistentJavaDrain              uint64
	stoppingTests                        map[string]struct{}
	stoppingGenerations                  map[string]uint64
	stopCompletions                      map[string]*stopCompletion
	resCollector                         *engine.ResourceCollector
	resCollectorMu                       sync.Mutex // serializes resCollector.Collect (mutates delta state)
	metricsSnapshotMu                    sync.Mutex // serializes per-test multi-group evidence drains
	executionEnabled                     bool
	draining                             bool
	publishInterval                      time.Duration
	completionTimers                     map[string]*time.Timer
	completionTokens                     map[string]uint64
	nextCompletionToken                  uint64
	stageTimers                          map[string]*time.Timer
	stageTokens                          map[string]uint64
	testExecutionLeases                  map[string]testExecutionLease
	testExecutionLeaseFences             map[string]uint64
	testExecutionLeaseFenceAllGeneration uint64
	nextTestLeaseToken                   uint64
	runSupervisions                      map[string]*runSupervisionLease
	runSupervisionTombstoneOrder         []runSupervisionTombstoneKey
	runSupervisionRequired               bool
	runSupervisionTTL                    time.Duration
	// maxAdoptionGrace caps WorkAssignment.adoption_grace; see orphanLocked.
	maxAdoptionGrace                 time.Duration
	runSupervisionDrain              time.Duration
	runSupervisionHardGrace          time.Duration
	runSupervisionDrainingVUsers     int64
	runSupervisionExpirations        atomic.Uint64
	runSupervisionEscalations        atomic.Uint64
	runSupervisionFencePersistenceMu sync.Mutex
	runSupervisionIntentMu           sync.Mutex
	runSupervisionIntentErr          error
	safetyExitEvidence               []runSupervisionCrashIntent
	selfTermination                  chan SelfTerminationRequest
	selfTerminationOnce              sync.Once
	runtimeContainmentFailure        <-chan struct{}
	runtimeContainmentBeforePersist  func()
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
	if s.runSupervisionIntentErr != nil {
		return fmt.Errorf("generator safety-exit evidence: %w", s.runSupervisionIntentErr)
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

// WithClock replaces the service's time source. Execution-epoch leases are
// expiry-driven, so tests that exercise a lapsed epoch otherwise have to pick a
// TTL short enough to elapse during the test and then sleep past it. That makes
// the assertion a race against the scheduler: under a loaded machine the epoch
// can lapse before the claim that installed it is even observed, which is how
// TestLapsedProcessEpochRejectsTheClaimAndFencesTheAssignmentLeaseDifferently
// came to fail 2 runs in 5 (see docs/qa/FLAKE_LOG.md, FLAKE-1).
//
// Passing a controlled clock lets a test install a comfortable TTL and then
// advance time deliberately, so lapse is something the test causes rather than
// something it waits for. Nil restores time.Now.
func WithClock(now func() time.Time) ServiceOption {
	return func(s *Service) {
		s.now = now
	}
}

// WithRunSupervisionRequired enables the production fail-closed distributed
// execution contract. Tests that intentionally characterize legacy RPC shapes
// omit this option.
func WithRunSupervisionRequired(ttl, drain time.Duration) ServiceOption {
	return func(s *Service) {
		s.runSupervisionRequired = true
		if ttl > 0 {
			s.runSupervisionTTL = ttl
		}
		if drain >= 0 {
			s.runSupervisionDrain = drain
		}
	}
}

// WithMaxAdoptionGrace caps the HA adoption grace a controller may request for
// a run (see WorkAssignment.adoption_grace). Zero disables orphaning.
func WithMaxAdoptionGrace(limit time.Duration) ServiceOption {
	return func(s *Service) {
		s.maxAdoptionGrace = min(max(limit, 0), maxAdoptionGrace)
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
		podCapacityIncarnation:   newPodCapacityIncarnation(),
		generatorID:              generatorID,
		now:                      time.Now,
		maxVUsers:                int32(maxVUsers),
		maxAssignedTests:         1024,
		maxAssignedGroups:        4096,
		maxExecutions:            4096,
		maxHeartbeatStreams:      defaultMaxHeartbeatStreams,
		podResources:             detectPodResourceCapacity(int32(maxVUsers)),
		scriptCache:              newScriptCacheManager(configuredGeneratorScriptCacheMax(), configuredGeneratorScriptCacheMaxBytes()),
		assignments:              make(map[string]assignedTest),
		executions:               make(map[string]map[string]*groupExecution),
		persistentJavaDrains:     make(map[uint64]javaVuserCapacityDrain),
		lifecycleCoordinator:     lifecycleCoordinator,
		stoppingTests:            lifecycleCoordinator.stoppingTests,
		stoppingGenerations:      lifecycleCoordinator.stoppingGenerations,
		stopCompletions:          lifecycleCoordinator.stopCompletions,
		resCollector:             engine.NewResourceCollector(),
		publishInterval:          time.Second,
		completionTimers:         make(map[string]*time.Timer),
		completionTokens:         make(map[string]uint64),
		stageTimers:              lifecycleCoordinator.stageTimers,
		stageTokens:              lifecycleCoordinator.stageTokens,
		testExecutionLeases:      make(map[string]testExecutionLease),
		testExecutionLeaseFences: make(map[string]uint64),
		runSupervisions:          make(map[string]*runSupervisionLease),
		runSupervisionTTL:        defaultRunSupervisionTTL,
		maxAdoptionGrace:         maxAdoptionGrace,
		runSupervisionDrain:      defaultRunSupervisionDrain,
		runSupervisionHardGrace:  defaultRunSupervisionHardGrace,
		selfTermination:          make(chan SelfTerminationRequest, 1),
		executionEpochManager:    executionEpochManager,
		shutdown:                 make(chan struct{}),
		metricsBroker:            newMetricsBroker(defaultMaxMetricsSubscribers, archiveRecordMax, archiveTTL),
		archiveManager:           archiveManager,
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
	if service.executionEpochStateErr == nil {
		if err := service.reconcileRunSupervisionCrashIntent(); err != nil {
			service.runSupervisionIntentErr = err
			slog.Error("reconcile generator safety-exit intent", "generator_id", generatorID, "error", err)
		}
	}
	service.startRuntimeContainmentFailureMonitor()
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
	s.mu.RLock()
	testIDs := make([]string, 0, len(s.assignments))
	for testID := range s.assignments {
		testIDs = append(testIDs, testID)
	}
	s.mu.RUnlock()
	sort.Strings(testIDs)
	// Detach and synchronously signal the whole fleet before persistence,
	// staging, or control waits consume the caller's shutdown budget.
	if err := s.forceStopTests(ctx, testIDs, ^uint64(0), "FAILED", "generator shut down before test completion", shutdownEscalationReason, true); err != nil {
		failures = append(failures, fmt.Errorf("stop active generator tests: %w", err))
	}
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
	s.archiveManager.beginShutdown()
	s.shutdownOnce.Do(func() { close(s.shutdown) })
	s.mu.Lock()
	s.executionEpochManager.stopTimerLocked()
	for testID := range s.testExecutionLeases {
		s.clearTestExecutionLeaseLocked(testID)
	}
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
		s.runSupervisionIntentMu.Lock()
		if err := epochState.close(); err != nil {
			failures = append(failures, fmt.Errorf("close execution epoch state: %w", err))
		}
		s.runSupervisionIntentMu.Unlock()
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
	execution.stopOnce.Do(func() {
		if execution.javaHost && execution.engine != nil {
			execution.engine.Stop()
		}

		if execution.cancel != nil {
			execution.cancel()
			return
		}
		// A staged execution has no goroutines that could close done. Closing it at
		// detach time preserves the reuse/fencing barrier without ever starting the
		// engine merely to tear it down.
		if execution.javaHost && execution.engine != nil {
			go func() {
				_ = execution.engine.ClosePreparedRuntime()
				execution.stagedDoneOnce.Do(func() {
					if execution.done != nil {
						close(execution.done)
					}
				})
			}()
			return
		}
		// Non-broker runtime reservations release bounded process-local state.
		// Close it before signalling staged completion so graceful detach remains
		// synchronous and no unused Event Hubs budget outlives the stopped stage.
		if execution.engine != nil {
			_ = execution.engine.ClosePreparedRuntime()
		}
		execution.stagedDoneOnce.Do(func() {
			if execution.done != nil {
				close(execution.done)
			}
		})
	})
}

func (s *Service) finishStoppedTest(testID string, assignment assignedTest, writer *archiveRecorder) (string, string) {
	return s.finishStoppedTestWithState(testID, assignment, writer, "COMPLETE", "")
}

func (s *Service) finishStoppedTestWithState(testID string, assignment assignedTest, writer *archiveRecorder, finalState string, finalError string) (string, string) {
	defer s.completeStoppingTest(testID, assignment.executionGeneration)
	archiveFinalized := true
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
			archiveFinalized = false
			finalState = "FAILED"
			if finalError != "" {
				finalError += "; "
			}
			finalError += "archive finalization failed: " + err.Error()
		}
	}
	batch = s.metricsBatchForAssignment(testID, assignment, finalState, finalError)
	s.publishTerminalMetrics(batch)
	if archiveFinalized {
		s.maybeDisarmRunSupervisionRestartFence(assignment.executionGeneration)
	}
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
	// Tell the controller the subscription is installed: every publication
	// from here on is delivered or shows as a sequence gap.
	if err := stream.SendHeader(metadata.Pairs(metricsSubscribedHeader, "1")); err != nil {
		return err
	}

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

			if pause.GetRequireQuiescence() {
				recvErr <- status.Error(codes.InvalidArgument, "strict pause requires the acknowledged SetPaused RPC")
				return
			}

			if err := s.setPaused(stream.Context(), pause.GetTestId(), pause.GetPause(), pause.GetExecutionGeneration(), pause.GetRunSupervisionToken()); err != nil {
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
	return &pb.ArchiveArtifact{Name: request.GetName(), Content: content, SourceIdentity: request.GetSourceIdentity()}, nil
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
			Name:           request.GetName(),
			SourceIdentity: request.GetSourceIdentity(),
			TotalSize:      0,
			Eof:            true,
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
			Name:           request.GetName(),
			SourceIdentity: request.GetSourceIdentity(),
			Content:        readBuffer[:n],
			Offset:         chunkOffset,
			TotalSize:      totalSize,
			Eof:            offset == totalSize,
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
		_, err := stream.Recv()
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
		podCapacity := s.podCapacitySnapshotLocked()
		javaOccupancyMode := s.heartbeatJavaVuserOccupancyModeLocked()
		s.mu.RUnlock()
		stats := generatorStatsFromResources(s.collectResources(), active)
		supportedProtocols, compatibleFeatures, runtimeCapacities := runtimeAdvertisementWithCapacities(
			stream.Context(),
			true,
			map[string]int64{"java_vuser": persistentJavaReserved},
			map[string]string{"java_vuser": javaOccupancyMode},
		)
		activeTestIDs := s.activeTestIDs()
		compatibleFeatures = append(compatibleFeatures, FeatureLiveTablesV1, FeaturePodCapacityV1, FeaturePodCapacityStandbyV1, FeatureIterationTerminationV1, FeatureSourcePlanV1, FeatureRunCorrelationV1, FeatureCorrelationPolicyV2, FeatureExemplarsV1, FeatureMessagingTraceCarrierV1, FeatureJointEvidenceV4)
		compatibleFeatures = append(compatibleFeatures, dsl.FeatureDistributedRendezvousV1, protocol.FeatureNativeDataServicesV1)
		if engine.NativeJavaScriptAvailable() {
			compatibleFeatures = append(compatibleFeatures, protocol.FeatureNativeJavaScriptV1)
		}
		javaHosts := s.javaHostCapabilities()
		if javaHosts != nil {
			compatibleFeatures = append(compatibleFeatures, FeatureJavaHostBrokerV1, FeatureJavaHostPreparedPopulationV1)
			if s.javaHostRuntime.Capabilities().NativeDataTransport {
				// This advertises the negotiated callback transport. Each cold
				// prepared JVM still proves SDK readiness before work commits.
				compatibleFeatures = append(compatibleFeatures, protocol.FeatureNativeDataBrokerV1)
			}
		}
		lastSafetyExit, safetyExits := s.safetyExitEvidenceProto()
		// Pongs and telemetry share the published clock. It re-anchors only
		// at a heartbeat while no test is assigned, so a jump shows at a
		// probe outside any run: any controller replica's inventory or
		// placement heartbeat can arrive between one run's clock probes, and
		// re-anchoring then would move that run's stamps under the offset its
		// last probe measured.
		wallNow := time.Now()
		if len(activeTestIDs) == 0 {
			generatorPublishedClock.reanchor(wallNow)
		}
		generatorNow := generatorPublishedClock.at(wallNow)
		if err := stream.Send(&pb.HeartbeatPong{
			GeneratorId:                    s.generatorID,
			JavaHost:                       javaHosts,
			PodCapacity:                    podCapacity,
			TimestampUnixMs:                generatorNow.UnixMilli(),
			TimestampUnixNano:              generatorNow.UnixNano(),
			ActiveTestIds:                  activeTestIDs,
			Stats:                          toProtoStats(stats),
			AvailableCapacity:              avail,
			CpuLimitCores:                  s.podResources.CPULimitCores,
			MemoryLimitBytes:               s.podResources.MemoryLimitBytes,
			MaxVusers:                      s.podResources.MaxVUsers,
			Version:                        buildinfo.Get().Version,
			SupportedProtocols:             supportedProtocols,
			CompatibleFeatures:             compatibleFeatures,
			RuntimeCapacities:              runtimeCapacities,
			RunSupervisionExpirationsTotal: s.runSupervisionExpirations.Load(),
			RunSupervisionEscalationsTotal: s.runSupervisionEscalations.Load(),
			LastSafetyExit:                 lastSafetyExit,
			SafetyExits:                    safetyExits,
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
// http3ExecutionError and http3UpgradeExecutionError decide whether this
// generator advertises native_http3_v1 and http3_upgrade_v1; tests replace
// them.
var (
	http3ExecutionError        = httpprotocol.HTTP3ExecutionError
	http3UpgradeExecutionError = httpprotocol.HTTP3UpgradeExecutionError
)

func runtimeAdvertisementWithCapacities(ctx context.Context, requireHealthy bool, inUse map[string]int64, occupancyModes map[string]string) ([]string, []string, []*pb.ProtocolRuntimeCapacity) {
	if ctx == nil {
		ctx = context.Background()
	}
	names := protocol.List()
	sort.Strings(names)
	available := make([]string, 0, len(names))
	features := []string{dsl.FeatureParallelLoopControlV1, dsl.FeaturePropertyAnalyticsV1, FeaturePreparationV1, FeaturePreparationReadinessV1, dsl.FeatureLRFidelityV1, FeatureEvacuationAssignmentStateV1, FeatureSecretBundleV1, FeatureRunSupervisionV1, FeatureMessageDetailCaptureV1, FeatureControlParallelV1, FeatureMessageDetailControlParallelV1}
	if http3ExecutionError() == nil {
		// Only a generator whose operator enabled HTTP/3 and whose build
		// carries the transport may receive exact-3 work.
		features = append(features, dsl.FeatureNativeHTTP3V1, FeatureHTTP3PreflightV1)
	}
	if http3UpgradeExecutionError() == nil {
		features = append(features, dsl.FeatureHTTP3UpgradeV1)
	}
	runtimeCapacities := make([]*pb.ProtocolRuntimeCapacity, 0)
	for _, name := range names {
		normalized := strings.ToLower(strings.TrimSpace(name))
		if normalized == "" {
			continue
		}
		if normalized == "bruno" {
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
		if normalized == "java_vuser" &&
			(!capabilitySupported || !javaVuserRuntimeContained(capabilities.Features)) {
			runtimeAvailable = false
		}
		if requireHealthy && runtimeAvailable {
			healthy, healthSupported, healthErr := protocol.CheckRuntimeHealth(ctx, instance)
			if healthErr != nil || healthSupported && !healthy {
				runtimeAvailable = false
			}
		}
		resourceCapacity, resourceSupported, resourceErr := protocol.ReadRuntimeResourceCapacity(instance)
		if resourceErr != nil {
			resourceCapacity = protocol.RuntimeResourceCapacity{}
		}
		if capabilitySupported || resourceSupported {
			used := inUse[normalized]
			if used < 0 {
				used = 0
			}
			maxVUsers := max(capabilities.MaxVUsers, 0)
			remaining := int64(0)
			if runtimeAvailable && used < maxVUsers {
				remaining = maxVUsers - used
			}
			runtimeEntry := &pb.ProtocolRuntimeCapacity{
				Protocol:        normalized,
				Available:       runtimeAvailable,
				MaxVusers:       maxVUsers,
				AvailableVusers: remaining,
				Features:        sortedUniqueRuntimeNames(capabilities.Features),
				OccupancyMode:   occupancyModes[normalized],
			}
			if resourceSupported && normalized == "eventhubs" {
				runtimeEntry.ResourceContract = FeatureRuntimeResourceReservationsV1
				runtimeEntry.Resources = RuntimeResourceCapacityToProto(resourceCapacity)
				if runtimeAvailable && normalized == "eventhubs" && resourceErr == nil && resourceCapacity.Available {
					features = append(features, FeatureRuntimeResourceReservationsV1)
				}
			}
			runtimeCapacities = append(runtimeCapacities, runtimeEntry)
		}
		if !runtimeAvailable {
			continue
		}
		available = append(available, normalized)
		features = append(features, normalized)
		if strings.HasPrefix(normalized, "jms") || normalized == "kafka" || normalized == "eventhubs" {
			features = append(features, FeatureMessagingRollupV1, FeatureMessagingScopeV1, FeatureMessagingEvidenceV2)
			if _, ok := instance.(protocol.MessagingProgressProvider); ok {
				features = append(features, FeatureMessagingProgressV1)
			}
			// This feature describes the acknowledged engine/runtime pause gate,
			// not ordinary messaging execution. Never infer it from progress alone.
			if messagingRuntimeQuiescenceAvailable(instance, runtimeAvailable) {
				features = append(features, FeatureMessagingRuntimeQuiescenceV1)
			}
		}
		if normalized == "browser" {
			if _, ok := instance.(protocol.BrowserDocumentEvidenceProvider); ok {
				features = append(features, FeatureBrowserDocumentEvidenceV2)
			}
			if _, ok := instance.(protocol.BrowserInteractionEvidenceProvider); ok {
				features = append(features, FeatureBrowserInteractionEvidenceV1)
			}
		}
		if normalized == "websocket" {
			features = append(features, FeatureWebSocketDiagnosticsV1)
		}
		if normalized == "grpc" {
			features = append(features, FeatureGRPCDiagnosticsV1)
		}
		if capabilitySupported {
			features = append(features, capabilities.Features...)
		}
	}
	return sortedUniqueRuntimeNames(available), sortedUniqueRuntimeNames(features), runtimeCapacities
}

func javaVuserRuntimeContained(features []string) bool {
	return protocol.RuntimeFeaturesSatisfy(features, "java_vuser_local_process_containment_v1") ||
		(protocol.RuntimeFeaturesSatisfy(features, "java_vuser_external_runtime_v1") &&
			protocol.RuntimeFeaturesSatisfy(features, "runner_owner_lease_v1") &&
			protocol.RuntimeFeaturesSatisfy(features, "java_vuser_runtime_lease_v1"))
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
	total := s.runSupervisionDrainingVUsers
	for _, assignment := range s.assignments {
		if assignment.sourcePlan != nil && !assignment.committed {
			total += sourcePlanUsers(assignment.sourcePlan)
		} else {
			total += int64(assignment.vuserCount)
		}
	}
	return total
}

func (s *Service) availableCapacityLocked() int32 {
	reserved := int64(0)
	if s.lifecycleCoordinator != nil {
		reserved = s.lifecycleCoordinator.pendingVUsersLocked()
	}
	available := int64(s.maxVUsers) - s.capacityVUsers64Locked() - reserved
	if available <= 0 {
		return 0
	}
	if available > int64(s.maxVUsers) {
		return s.maxVUsers
	}
	return int32(available)
}

func (s *Service) FailTest(testID string, message string) {
	stops := s.detachFencedTests([]string{testID}, ^uint64(0), false)
	if len(stops) == 0 {
		return
	}
	stop := stops[0]
	for _, exec := range stop.executions {
		if exec != nil {
			message = scrubGeneratorTerminalError(message, exec.redactionConfig)
		}
	}
	if len(stop.executions) == 0 {
		message = scrubGeneratorTerminalError(message, nil)
	}
	stop.finalState = "FAILED"
	stop.finalError = message
	stop.escalationReason = testFailureEscalationReason
	<-s.startDetachedRunDrain(stop, false)
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
	// A publication is a new window, as a coordinated publication is
	// (publishTestExecutionSnapshot): it replaces every group's published
	// window, so another group's earlier window is not sent, and counted,
	// again.
	assignment.groupTransactions = make(map[string][]TransactionMetric)
	assignment.groupAggregates = make(map[string]metricsAggregateSet)
	if assignment.groupTransactionResponseTimeSLA == nil {
		assignment.groupTransactionResponseTimeSLA = make(map[string][]engine.TransactionResponseTimeSLACompliance)
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
	assignment.metricsCollection = nextMetricsCollection()
	if expected != nil {
		expected.metricsMu.Lock()
		if expected.lastMetrics != nil {
			assignment.groupTransactionResponseTimeSLA[groupName] = append([]engine.TransactionResponseTimeSLACompliance(nil), expected.lastMetrics.transactionResponseTimeSLA...)
			setGroupHTTPVersions(&assignment, groupName, expected.lastMetrics.httpVersions)
		}
		expected.metricsMu.Unlock()
	}
	s.assignments[testID] = assignment
	batch := s.metricsBatchLocked(testID)
	writer := s.archiveManager.writer(testID)
	s.mu.Unlock()

	// Each publication carries only this group's window, so each is archived.
	s.recordMetricsBatch(testID, writer, batch)
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
		Messaging:           archiveMessagingFromBatch(batch),
		BrowserDocuments:    browserDocumentsFromJSON(batch.GetBrowserDocumentSummaryJson(), batch.GetBrowserDocumentSummaryVersion()),
		MessagingProgress:   messagingProgressFromJSON(batch.GetMessagingProgressJson(), batch.GetMessagingProgressVersion()),
		CompletedIterations: batch.GetStats().GetCompletedIterations(),
		Timestamp:           time.UnixMilli(batch.TimestampUnixMs),
		ActiveVUsers:        int64(batch.GetStats().GetActiveVusers()),
		TPS:                 float64(totalSuccess) / windowSecondsFromMs(batch.GetWindowMs()),
		AvgResponseMs:       avgResponse,
		ErrorPct:            errorPct(totalErrors, totalSuccess+totalErrors),
		TotalTx:             totalSuccess,
		TotalErrors:         totalErrors,
		P50Ms:               quantileFromMetricAggregate(batch.FleetMetrics, batch.Transactions, 0.50),
		P90Ms:               quantileFromMetricAggregate(batch.FleetMetrics, batch.Transactions, 0.90),
		P95Ms:               quantileFromMetricAggregate(batch.FleetMetrics, batch.Transactions, 0.95),
		P99Ms:               quantileFromMetricAggregate(batch.FleetMetrics, batch.Transactions, 0.99),
		DurationDigest:      tdigestSnapshotFromProto(batch.GetFleetMetrics().GetDurationDigest()),
		Groups:              archiveSummaryGroupsFromBatch(batch),
		Transactions:        archiveSummaryTransactionsFromBatch(batch.GetTransactionMetrics()),
		ByRegion:            archiveSummaryTransactionsFromBatch(batch.GetRegionMetrics()),
		Browser:             archiveSummaryBrowserFromBatch(batch),
		BrowserByDevice:     archiveSummaryBrowserDevicesFromBatch(batch),
		Telemetry:           archiveSummaryTelemetryFromBatch(batch),
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
		DroppedResultsAvailable: telemetry.GetDroppedResults() >= 0,
		DroppedResults:          telemetry.GetDroppedResults(), SheddedArrivals: telemetry.GetSheddedArrivals(),
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
	transactionResponseTimeSLA := make([]*pb.TransactionResponseTimeSLACompliance, 0)
	for _, groupRows := range assignment.groupTransactionResponseTimeSLA {
		for _, row := range groupRows {
			transactionResponseTimeSLA = append(transactionResponseTimeSLA, &pb.TransactionResponseTimeSLACompliance{
				Group: row.Group, Transaction: row.Transaction, ThresholdMs: row.ThresholdMs,
				TotalCount: row.TotalCount, EligibleSuccessCount: row.EligibleSuccessCount,
				ExcludedErrorCount: row.ExcludedErrorCount, MissedCount: row.MissedCount,
			})
		}
	}
	sort.Slice(transactionResponseTimeSLA, func(i, j int) bool {
		if transactionResponseTimeSLA[i].Group == transactionResponseTimeSLA[j].Group {
			return transactionResponseTimeSLA[i].Transaction < transactionResponseTimeSLA[j].Transaction
		}
		return transactionResponseTimeSLA[i].Group < transactionResponseTimeSLA[j].Group
	})
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
		telemetry.CollectionSequence = assignment.metricsCollection
	}
	for groupName, activeVUsers := range groupAssignments {
		if !groupsWithActiveStats[groupName] {
			stats.ActiveVUsers += activeVUsers
		}
	}
	if stats.ActiveVUsers == 0 && len(groupAssignments) == 0 {
		stats.ActiveVUsers = assignment.vuserCount
	}
	batch := s.metricsBatchForMetrics(testID, assignment.executionGeneration, stats, transactions, aggregates, assignment.paused, groupAssignments, lifecycleState, lifecycleError, telemetry)
	batch.TransactionResponseTimeSla = transactionResponseTimeSLA
	batch.HttpVersionMix = httpVersionMixToProto(assignment.groupHTTPVersions)
	return batch
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
				SubscriptionId:              tx.ServerTelemetry.SubscriptionID,
				AcknowledgementMs:           cloneMessagingAck(tx.ServerTelemetry.AcknowledgementMs),
				MessagingDiagnosticsJson:    messagingDiagnosticsJSON(tx.ServerTelemetry.Messaging),
				MessagingDiagnosticsVersion: messagingDiagnosticsVersion(tx.ServerTelemetry.Messaging),
				Grpc:                        grpcDiagnosticsToProto(tx.ServerTelemetry.GRPC),
				Websocket:                   websocketDiagnosticsToProto(tx.ServerTelemetry.WebSocket),
				ServerProcessingMs:          tx.ServerTelemetry.ServerProcessingMs,
				TransferMs:                  tx.ServerTelemetry.TransferMs,
				RequestPayloadKb:            tx.ServerTelemetry.RequestPayloadKB,
				ResponsePayloadKb:           tx.ServerTelemetry.ResponsePayloadKB,
				ConnectionReused:            tx.ServerTelemetry.ConnectionReused,
				RequestSizeClass:            tx.ServerTelemetry.RequestSizeClass,
				ResponseSizeClass:           tx.ServerTelemetry.ResponseSizeClass,
				FirstEventMs:                tx.ServerTelemetry.FirstEventMs,
				EventCount:                  tx.ServerTelemetry.EventCount,
				MessageLatencyMs:            tx.ServerTelemetry.MessageLatencyMs,
				MessagesSent:                tx.ServerTelemetry.MessagesSent,
				MessagesReceived:            tx.ServerTelemetry.MessagesReceived,
				MessagesSentPerSec:          tx.ServerTelemetry.MessagesSentPerSec,
				MessagesReceivedPerSec:      tx.ServerTelemetry.MessagesReceivedPerSec,
				ConsumerLagMs:               tx.ServerTelemetry.ConsumerLagMs,
				ConsumerLagObserved:         tx.ServerTelemetry.ConsumerLagObserved,
				ConsumerOffsetLagObserved:   tx.ServerTelemetry.ConsumerOffsetLagObserved,
				SendFailureCount:            tx.ServerTelemetry.SendFailureCount,
				RedeliveryCount:             tx.ServerTelemetry.RedeliveryCount,
				DeadLetteredCount:           tx.ServerTelemetry.DeadLetteredCount,
				DuplicateCount:              tx.ServerTelemetry.DuplicateCount,
				OutOfOrderCount:             tx.ServerTelemetry.OutOfOrderCount,
				CommitCount:                 tx.ServerTelemetry.CommitCount,
				AbortCount:                  tx.ServerTelemetry.AbortCount,
				ConsumerOffsetLag:           tx.ServerTelemetry.ConsumerOffsetLag,
				ThrottledRequests:           tx.ServerTelemetry.ThrottledRequests,
				ServerBusyCount:             tx.ServerTelemetry.ServerBusyCount,
				QuotaExceededCount:          tx.ServerTelemetry.QuotaExceededCount,
				EventTooLargeCount:          tx.ServerTelemetry.EventTooLargeCount,
				EndToEndLatencyMs:           tx.ServerTelemetry.EndToEndLatencyMs,
				PartitionId:                 tx.ServerTelemetry.PartitionID,
			}
		}
		protoTransactions = append(protoTransactions, &pb.TransactionMetric{
			Name:             tx.Name,
			StartTimeUnixMs:  tx.StartTimeUnixMs,
			Group:            tx.Group,
			Region:           tx.Region,
			NetworkProfile:   tx.NetworkProfile,
			Hierarchy:        tx.Hierarchy,
			Phase:            tx.Phase,
			PageName:         tx.PageName,
			DurationMs:       tx.DurationMs,
			WorkerDurationMs: tx.WorkerDurationMs,
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
			CollectionSequence:       telemetry.CollectionSequence,
		}
		// The broker attributes every dropped batch to its test, so this
		// count starts at zero with the test.
		testDropped := s.metricsBroker.droppedCountForTest(testID)
		protoTelemetry.TestDroppedSubscriberBatches = &testDropped
	}
	return &pb.MetricsBatch{
		BrowserDocumentSummaryJson:    browserDocumentsJSON(aggregates.BrowserDocuments),
		BrowserDocumentSummaryVersion: browserDocumentsVersion(aggregates.BrowserDocuments),
		MessagingProgressJson:         messagingProgressJSON(aggregates.MessagingProgress),
		MessagingProgressVersion:      messagingProgressVersion(aggregates.MessagingProgress),
		TestId:                        testID,
		ExecutionGeneration:           executionGeneration,
		GeneratorId:                   s.generatorID,
		TimestampUnixMs:               time.Now().UnixMilli(),
		WindowMs:                      windowMs,
		Transactions:                  protoTransactions,
		Stats:                         toProtoStats(stats),
		Groups:                        groups,
		LifecycleState:                lifecycleState,
		LifecycleError:                lifecycleError,
		Telemetry:                     protoTelemetry,
		TransactionMetrics:            metricAggregatesToProto(aggregates.Transactions),
		DependencyMetrics:             metricAggregatesToProto(aggregates.Dependencies),
		SoapMetrics:                   metricAggregatesToProto(aggregates.SOAP),
		NetworkProfileMetrics:         metricAggregatesToProto(aggregates.NetworkProfiles),
		ActionMetrics:                 metricAggregatesToProto(aggregates.Actions),
		ActionDimensionMetrics:        metricAggregatesToProto(aggregates.ActionDimensions),
		JointMetricsV4:                metricAggregatesToProto(aggregates.Joint),
		JointMetricsInfo:              jointInfoToProto(aggregates.JointInfo),
		ActionPropertyMetrics:         metricAggregatesToProto(aggregates.ActionProperties),
		ActionMetricsVersion:          aggregates.NativeVersion,
		PropertyMetrics:               metricAggregatesToProto(aggregates.Properties),
		RegionMetrics:                 metricAggregatesToProto(aggregates.Regions),
		FleetMetrics:                  metricAggregateToProto(aggregates.Fleet),
		BrowserMetrics:                browserMetricsToProto(aggregates.Browser),
		BrowserDeviceMetrics:          browserDeviceMetricsToProto(aggregates.BrowserByDevice),
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
	if ok && len(s.executions[testID]) > 0 {
		stops := s.detachFencedTestsLocked([]string{testID}, assignment.executionGeneration, false)
		s.mu.Unlock()
		if len(stops) > 0 {
			stops[0].finalState = "COMPLETE"
			stops[0].escalationReason = durationEscalationReason
			s.startDetachedRunDrain(stops[0], true)
		}
		return
	}
	if ok {
		s.markStoppingLocked(testID, assignment.executionGeneration)
		s.clearTestExecutionLeaseLocked(testID)
		s.clearRunSupervisionLocked(testID)
		releaseAssignmentRuntimeResources(assignment)
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
			s.publishTestExecutionSnapshot(testID, groupName, exec, true)
		}
	}
}

func (s *Service) publishExecutionSnapshot(testID string, groupName string, exec *groupExecution) {
	s.publishTestExecutionSnapshot(testID, groupName, exec, false)
}

// publishTestExecutionSnapshot drains every group in a test as one publication
// window. Per-group tickers deliberately elect one publisher; final callbacks
// may force a coordinated drain so a group that ends between ticks cannot strand
// evidence. Holding the service lock across the bounded snapshot reads prevents
// a stop transition from removing the assignment after a transaction window was
// drained but before that evidence was attached to the archive batch.
func (s *Service) publishTestExecutionSnapshot(testID string, groupName string, expected *groupExecution, requirePublisher bool) {
	if expected == nil {
		return
	}
	s.metricsSnapshotMu.Lock()
	defer s.metricsSnapshotMu.Unlock()

	// Match the publication-lock -> service-lock ordering used by lifecycle
	// mutations so the combined metrics batch stays ordered with terminal state.
	s.metricsBroker.lockPublication()
	defer s.metricsBroker.unlockPublication()
	s.mu.Lock()
	assignment, ok := s.assignments[testID]
	if !ok || assignment.executionGeneration != expected.executionGeneration ||
		s.executions[testID][groupName] != expected {
		s.mu.Unlock()
		return
	}
	if requirePublisher && !s.isArchivePublisherLocked(testID, groupName) {
		s.mu.Unlock()
		return
	}

	groupNames := make([]string, 0, len(s.executions[testID]))
	for candidate := range s.executions[testID] {
		groupNames = append(groupNames, candidate)
	}
	sort.Strings(groupNames)
	type collectedGroupMetrics struct {
		groupName string
		metrics   executionMetricsSnapshot
		ok        bool
	}
	collected := make([]collectedGroupMetrics, 0, len(groupNames))
	published := false
	for _, candidate := range groupNames {
		execution := s.executions[testID][candidate]
		if execution == nil || execution.executionGeneration != assignment.executionGeneration {
			continue
		}
		// The elected periodic publisher emits an explicit idle cadence. Forced
		// final callbacks only publish groups with changed/new evidence; otherwise
		// simultaneous group completions would each repeat the coordinated window.
		metrics, metricsOK := s.collectExecutionMetricsWithIdle(execution, requirePublisher)
		collected = append(collected, collectedGroupMetrics{groupName: candidate, metrics: metrics, ok: metricsOK})
		published = published || metricsOK
	}
	if !published {
		s.mu.Unlock()
		return
	}
	// assignment contains shallow map fields. Mutate those maps only after the
	// collection pass proves a batch will be emitted; otherwise an all-empty
	// forced drain could alter the live assignment despite returning early.
	// Transaction samples and aggregates are window evidence, not gauges. Start
	// each coordinated publication with fresh maps so evidence from a group that
	// already finished and was removed from s.executions cannot leak into a
	// sibling's later final window. Stats and telemetry remain per-group snapshots
	// and deliberately retain their latest values.
	assignment.groupTransactions = make(map[string][]TransactionMetric)
	assignment.groupAggregates = make(map[string]metricsAggregateSet)
	assignment.metricsCollection = nextMetricsCollection()
	for _, group := range collected {
		if group.ok {
			applyExecutionMetricsToAssignment(&assignment, group.groupName, group.metrics)
		}
	}
	s.assignments[testID] = assignment
	batch := s.metricsBatchLocked(testID)
	writer := s.archiveManager.writer(testID)
	s.mu.Unlock()

	s.recordMetricsBatch(testID, writer, batch)
	s.broadcastMetricsLocked(batch)
}

func (s *Service) collectExecutionMetrics(exec *groupExecution) (executionMetricsSnapshot, bool) {
	return s.collectExecutionMetricsWithIdle(exec, true)
}

func (s *Service) collectExecutionMetricsWithIdle(exec *groupExecution, includeIdle bool) (executionMetricsSnapshot, bool) {
	if exec == nil {
		return executionMetricsSnapshot{}, false
	}
	exec.metricsMu.Lock()
	var previous *executionMetricsSnapshot
	if exec.lastMetrics != nil {
		copy := *exec.lastMetrics
		previous = &copy
	}
	exec.metricsMu.Unlock()

	var snapshot engine.MetricsSnapshot
	snapshotOK := false
	if exec.engine != nil {
		snapshot, snapshotOK = exec.engine.LatestSnapshot()
		if exec.owner != nil && snapshotOK {
			snapshot = exec.owner.groupSnapshot(snapshot, exec.groupName)
		}
	}
	stats := GeneratorStats{GoroutineCount: int32(runtime.NumGoroutine())}
	if previous != nil {
		stats = previous.stats
		stats.GoroutineCount = int32(runtime.NumGoroutine())
	}
	if snapshotOK {
		stats = generatorStatsFromResources(snapshot.Resources, int32(snapshot.ActiveVUsers))
	}
	if exec.engine != nil && exec.owner == nil {
		stats.CompletedIterations = exec.engine.CompletedIterations()
	} else if exec.owner != nil {
		stats.CompletedIterations = exec.engine.GroupCompletedIterations(exec.groupName)
	}
	droppedSubscriberBatches := s.metricsBroker.droppedCount()
	changes := classifyExecutionMetricsChanges(previous, snapshot, snapshotOK, stats, droppedSubscriberBatches, false)

	var transactions []TransactionMetric
	var aggregateWindow *transactionWindowAggregate
	var window time.Duration
	if exec.results != nil {
		transactions, aggregateWindow, window = exec.results.drainWindow(includeIdle || changes.sourceSnapshot)
	}
	hasWindowEvidence := len(transactions) > 0 || !aggregateWindow.empty()
	changes.windowEvidence = hasWindowEvidence
	if !changes.shouldPublish(includeIdle, previous != nil) {
		return executionMetricsSnapshot{}, false
	}
	if !changes.sourceSnapshot && !hasWindowEvidence {
		idle := *previous
		idle.stats = stats
		idle.transactions = nil
		idle.aggregates = metricsAggregateSet{MessagingProgress: previous.aggregates.MessagingProgress.Clone()}
		idle.telemetry.WindowMs = s.publishInterval.Milliseconds()
		now := time.Now()
		idle.telemetry.PublishedAtUnixMs = generatorPublishedClock.at(now).UnixMilli()
		idle.telemetry.DroppedSubscriberBatches = droppedSubscriberBatches
		if snapshotOK {
			idle.telemetry.CollectedAtUnixMs = generatorPublishedClock.at(snapshot.Timestamp).UnixMilli()
			idle.telemetry.SampleAgeMs = float64(now.Sub(snapshot.Timestamp).Microseconds()) / 1000.0
			idle.telemetry.GeneratorProcessingLagMs = idle.telemetry.SampleAgeMs
			if snapshot.Telemetry != nil {
				idle.telemetry.DroppedResults = snapshot.Telemetry.DroppedResults
				idle.telemetry.SheddedArrivals = snapshot.Telemetry.SheddedArrivals
			}
		}
		storeExecutionMetrics(exec, idle)
		return idle, true
	}
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
	if previous != nil {
		telemetry.DroppedResults = previous.telemetry.DroppedResults
		telemetry.SheddedArrivals = previous.telemetry.SheddedArrivals
		telemetry.CollectedAtUnixMs = previous.telemetry.CollectedAtUnixMs
	}
	aggregates := aggregateWindow.finalize(windowMs)
	if snapshotOK {
		if changes.sourceSnapshot {
			aggregates.BrowserDocuments = snapshot.BrowserDocuments.Clone()
		}
		aggregates.MessagingProgress = snapshot.MessagingProgress.Clone()
	} else if previous != nil {
		aggregates.MessagingProgress = previous.aggregates.MessagingProgress.Clone()
	}
	if snapshotOK {
		telemetry.CollectedAtUnixMs = generatorPublishedClock.at(snapshot.Timestamp).UnixMilli()
		now := time.Now()
		telemetry.PublishedAtUnixMs = generatorPublishedClock.at(now).UnixMilli()
		telemetry.SampleAgeMs = float64(now.Sub(snapshot.Timestamp).Microseconds()) / 1000.0
		telemetry.GeneratorProcessingLagMs = telemetry.SampleAgeMs
		if snapshot.Telemetry != nil {
			telemetry.DroppedResults = snapshot.Telemetry.DroppedResults
			telemetry.SheddedArrivals = snapshot.Telemetry.SheddedArrivals
		}
		if changes.sourceSnapshot {
			mergeSnapshotSideChannelAggregates(&aggregates, snapshot)
		}
	}
	telemetry.DroppedSubscriberBatches = droppedSubscriberBatches
	transactionResponseTimeSLA := snapshot.TransactionResponseTimeSLA
	httpVersions := snapshot.HTTPVersions
	if !snapshotOK && previous != nil {
		transactionResponseTimeSLA = previous.transactionResponseTimeSLA
		httpVersions = previous.httpVersions
	}
	metrics := executionMetricsSnapshot{
		stats: stats, transactions: transactions, telemetry: telemetry, aggregates: aggregates,
		transactionResponseTimeSLA: append([]engine.TransactionResponseTimeSLACompliance(nil), transactionResponseTimeSLA...),
		httpVersions:               httpVersions.Clone(),
	}
	if snapshotOK {
		metrics.sourceSnapshotAt = snapshot.Timestamp
		metrics.sourceSnapshotValid = true
	} else if previous != nil {
		metrics.sourceSnapshotAt = previous.sourceSnapshotAt
		metrics.sourceSnapshotValid = previous.sourceSnapshotValid
	}
	storeExecutionMetrics(exec, metrics)
	return metrics, true
}

type executionMetricsChanges struct {
	sourceSnapshot      bool
	processStats        bool
	subscriberTelemetry bool
	windowEvidence      bool
}

func classifyExecutionMetricsChanges(
	previous *executionMetricsSnapshot,
	snapshot engine.MetricsSnapshot,
	snapshotOK bool,
	stats GeneratorStats,
	droppedSubscriberBatches int64,
	windowEvidence bool,
) executionMetricsChanges {
	changes := executionMetricsChanges{windowEvidence: windowEvidence}
	changes.sourceSnapshot = snapshotOK && (previous == nil || !previous.sourceSnapshotValid ||
		!snapshot.Timestamp.Equal(previous.sourceSnapshotAt))
	if previous == nil {
		return changes
	}
	changes.processStats = stats != previous.stats || snapshotTelemetryChanged(snapshot, previous.telemetry)
	changes.subscriberTelemetry = droppedSubscriberBatches != previous.telemetry.DroppedSubscriberBatches
	return changes
}

func (changes executionMetricsChanges) shouldPublish(includeIdle bool, hasPrevious bool) bool {
	if changes.sourceSnapshot || changes.windowEvidence {
		return true
	}
	// Process gauges and subscriber-drop telemetry remain observable on the
	// regular cadence, but they are not execution evidence. A forced completion
	// callback must therefore not create a second empty final window merely
	// because one of those values changed after the first callback published.
	return includeIdle && hasPrevious
}

func snapshotTelemetryChanged(snapshot engine.MetricsSnapshot, previous MetricsTelemetry) bool {
	if snapshot.Telemetry == nil {
		return false
	}
	return snapshot.Telemetry.DroppedResults != previous.DroppedResults ||
		snapshot.Telemetry.SheddedArrivals != previous.SheddedArrivals
}

func storeExecutionMetrics(exec *groupExecution, metrics executionMetricsSnapshot) {
	if exec == nil {
		return
	}
	exec.metricsMu.Lock()
	stored := metrics
	stored.aggregates.BrowserDocuments = metrics.aggregates.BrowserDocuments.Clone()
	stored.aggregates.MessagingProgress = metrics.aggregates.MessagingProgress.Clone()
	stored.transactions = append([]TransactionMetric(nil), metrics.transactions...)
	stored.transactionResponseTimeSLA = append([]engine.TransactionResponseTimeSLACompliance(nil), metrics.transactionResponseTimeSLA...)
	stored.httpVersions = metrics.httpVersions.Clone()
	exec.lastMetrics = &stored
	exec.metricsMu.Unlock()
}

func metricsAggregateSetHasEvidence(aggregates metricsAggregateSet) bool {
	return aggregates.BrowserDocuments != nil || aggregates.MessagingProgress != nil || aggregates.Fleet != nil || len(aggregates.Transactions) > 0 || len(aggregates.Dependencies) > 0 || len(aggregates.SOAP) > 0 || len(aggregates.NetworkProfiles) > 0 || len(aggregates.Actions) > 0 || len(aggregates.ActionDimensions) > 0 || len(aggregates.ActionProperties) > 0 || len(aggregates.Properties) > 0 || len(aggregates.Regions) > 0 || aggregates.Browser != nil || len(aggregates.BrowserByDevice) > 0 || len(aggregates.Groups) > 0
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
	if assignment.groupTransactionResponseTimeSLA == nil {
		assignment.groupTransactionResponseTimeSLA = make(map[string][]engine.TransactionResponseTimeSLACompliance)
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
	assignment.groupTransactionResponseTimeSLA[groupName] = append([]engine.TransactionResponseTimeSLACompliance(nil), metrics.transactionResponseTimeSLA...)
	setGroupHTTPVersions(assignment, groupName, metrics.httpVersions)
}

// setGroupHTTPVersions records a group's run-to-date HTTP version mix; a
// snapshot without one keeps the group's earlier counts.
func setGroupHTTPVersions(assignment *assignedTest, groupName string, mix *protocol.HTTPVersionMix) {
	if mix == nil {
		return
	}
	if assignment.groupHTTPVersions == nil {
		assignment.groupHTTPVersions = make(map[string]*protocol.HTTPVersionMix)
	}
	assignment.groupHTTPVersions[groupName] = mix.Clone()
}

// httpVersionMixToProto sums the groups' run-to-date mixes (nil when no group
// ran an HTTP action).
func httpVersionMixToProto(groups map[string]*protocol.HTTPVersionMix) *pb.HTTPVersionMix {
	var total protocol.HTTPVersionMix
	for _, mix := range groups {
		total.Merge(mix)
	}
	if total.Actions == 0 {
		return nil
	}
	return &pb.HTTPVersionMix{
		Actions: total.Actions, ByVersion: total.ByVersion, BySelection: total.BySelection, ByFallback: total.ByFallback, ByQuicError: total.ByQUICError,
		Http3Policies: total.HTTP3Policies, UpgradeEligible: total.UpgradeEligible, FellBack: total.FellBack,
		QuicAttempted: total.QUICAttempted, QuicFailed: total.QUICFailed, Attempts: total.Attempts,
	}
}

// captureStoppedExecutionMetrics drains evidence after detached executions have
// crossed their done barriers. Their normal final publisher is fenced by the
// missing assignment, so this is the sole owner of any remaining result window.
// A drain is a new window, as a coordinated publication is
// (publishTestExecutionSnapshot): it replaces every group's published window,
// so a group with nothing left to drain does not carry its last window into
// the terminal batch a second time.
func (s *Service) captureStoppedExecutionMetrics(assignment *assignedTest, executions []*groupExecution) {
	if assignment == nil {
		return
	}
	type drainedGroup struct {
		groupName string
		metrics   executionMetricsSnapshot
	}
	drained := make([]drainedGroup, 0, len(executions))
	for _, execution := range executions {
		metrics, ok := s.collectExecutionMetricsWithIdle(execution, false)
		if !ok {
			continue
		}
		drained = append(drained, drainedGroup{groupName: execution.groupName, metrics: metrics})
	}
	if len(drained) == 0 {
		return
	}
	assignment.groupTransactions = make(map[string][]TransactionMetric)
	assignment.groupAggregates = make(map[string]metricsAggregateSet)
	assignment.metricsCollection = nextMetricsCollection()
	for _, group := range drained {
		applyExecutionMetricsToAssignment(assignment, group.groupName, group.metrics)
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
	s.clearTestExecutionLeaseLocked(testID)
	s.clearRunSupervisionLocked(testID)
	releaseAssignmentRuntimeResources(assignment)
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
	if err == nil {
		writer.retireRegisteredHARSources()
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
	out.BrowserDocuments = snapshot.BrowserDocuments.Clone()
	out.MessagingProgress = snapshot.MessagingProgress.Clone()
	out.NetworkProfiles = engineActionMetricsToAggregates(snapshot.ByNetworkProfile)
	if snapshot.ByAction != nil {
		out.NativeVersion = 1
	}
	out.Actions = engineActionMetricsToAggregates(snapshot.ByAction)
	if snapshot.ByAction != nil && snapshot.ByActionProperty != nil {
		out.NativeVersion = 2
	}
	if snapshot.ByAction != nil && snapshot.ByActionProperty != nil && snapshot.ByDimensions != nil {
		out.NativeVersion = 3
	}
	out.ActionDimensions = engineActionMetricsToAggregates(snapshot.ByDimensions)
	out.Joint, out.JointInfo = jointAggregatesFromEngine(snapshot.Joint)
	out.ActionProperties = engineActionMetricsToAggregates(snapshot.ByActionProperty)
	out.Properties = engineActionMetricsToAggregates(snapshot.ByProperty)
	out.Regions = engineActionMetricsToAggregates(snapshot.ByRegion)
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
			DurationDigest:   engine.CloneTDigestSnapshot(metrics.DurationDigest),
			Messaging:        engineMessagingSummary(metrics.ServerTelemetry),
			MessagingVersion: messagingSummaryVersion(metrics.ServerTelemetry),
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
			Exemplars:        append([]engine.TraceExemplar(nil), metrics.Exemplars...),
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
	out := metricsAggregateSet{NativeVersion: 3}
	fleet := make([]MetricAggregate, 0, len(values))
	transactionMetrics := make([]MetricAggregate, 0)
	dimensionMetrics := make([]MetricAggregate, 0)
	jointMetrics := make([]MetricAggregate, 0)
	dependencyMetrics := make([]MetricAggregate, 0)
	soapMetrics := make([]MetricAggregate, 0)
	networkProfileMetrics := make([]MetricAggregate, 0)
	actionMetrics := make([]MetricAggregate, 0)
	propertyMetrics := make([]MetricAggregate, 0)
	regionMetrics := make([]MetricAggregate, 0)
	groupMetrics := make([]GroupMetric, 0)
	browsers := make([]*BrowserMetrics, 0)
	browserDevices := make([]BrowserDeviceMetrics, 0)
	jointParts := make([][]MetricAggregate, 0, len(values))
	jointInfos := make([]*JointMetricsInfo, 0, len(values))
	for _, value := range values {
		jointParts = append(jointParts, value.Joint)
		jointInfos = append(jointInfos, value.JointInfo)
		protocol.MergeBrowserDocumentSummaries(&out.BrowserDocuments, value.BrowserDocuments)
		if value.MessagingProgress != nil {
			protocol.MergeMessagingProgress(&out.MessagingProgress, value.MessagingProgress)
		}
		if value.Fleet != nil {
			fleet = append(fleet, *value.Fleet)
		}
		if value.NativeVersion > 3 {
			out.NativeVersion = 0
		} else {
			out.NativeVersion = min(out.NativeVersion, value.NativeVersion)
		}
		transactionMetrics = append(transactionMetrics, value.Transactions...)
		dependencyMetrics = append(dependencyMetrics, value.Dependencies...)
		soapMetrics = append(soapMetrics, value.SOAP...)
		networkProfileMetrics = append(networkProfileMetrics, value.NetworkProfiles...)
		actionMetrics = append(actionMetrics, value.Actions...)
		dimensionMetrics = append(dimensionMetrics, value.ActionDimensions...)
		jointMetrics = append(jointMetrics, value.ActionProperties...)
		propertyMetrics = append(propertyMetrics, value.Properties...)
		regionMetrics = append(regionMetrics, value.Regions...)
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
	out.Actions = mergeMetricAggregateSeries(actionMetrics)
	if out.NativeVersion == 3 {
		out.ActionDimensions = mergeMetricAggregateSeries(dimensionMetrics)
	}
	if out.NativeVersion >= 2 {
		out.ActionProperties = mergeMetricAggregateSeries(jointMetrics)
	}
	out.Properties = mergeMetricAggregateSeries(propertyMetrics)
	out.Regions = mergeMetricAggregateSeries(regionMetrics)
	if joint, info, err := mergeJointEvidence(jointParts, jointInfos); err == nil {
		// Groups counted under different projections publish no joint
		// evidence for the window: unavailable, never mixed.
		out.Joint, out.JointInfo = joint, info
	}
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
		if name == "" || value.Count <= 0 {
			continue
		}
		byName[name] = append(byName[name], value)
	}
	if len(byName) == 0 {
		return nil
	}

	names := make([]string, 0, len(byName))
	for name := range byName {
		if name != metricOverflowSeries {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	overflowValues := append([]MetricAggregate(nil), byName[metricOverflowSeries]...)
	if len(names) > maxTransactionWindowSeries-1 {
		for _, name := range names[maxTransactionWindowSeries-1:] {
			overflowValues = append(overflowValues, byName[name]...)
		}
		names = names[:maxTransactionWindowSeries-1]
	}
	if len(overflowValues) > 0 {
		names = append(names, metricOverflowSeries)
		sort.Strings(names)
	}

	out := make([]MetricAggregate, 0, min(len(names), maxTransactionWindowSeries))
	for _, name := range names {
		series := byName[name]
		if name == metricOverflowSeries {
			series = overflowValues
		}
		if aggregate := mergeMetricAggregates(series); aggregate != nil {
			aggregate.Name = name
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
		protocol.MergeMessagingSummary(&out.Messaging, value.Messaging)
		weightedAvgMs += value.AvgMs * count
		weightedDNSMs += value.AvgDNSMs * count
		weightedConnectMs += value.AvgConnectMs * count
		weightedTLSMs += value.AvgTLSMs * count
		weightedTTFBMs += value.AvgTTFBMs * count
		out.BytesSent += value.BytesSent
		out.BytesReceived += value.BytesReceived
		out.TPS += value.TPS
		if engine.ValidateDurationDigest(value.DurationDigest, value.Count) == nil {
			if durationDigest == nil {
				durationDigest = engine.NewTDigest(value.DurationDigest.Compression)
			}
			durationDigest.Merge(engine.NewTDigestFromSnapshot(*value.DurationDigest))
		}
	}
	if out.Count == 0 {
		return nil
	}
	out.MessagingVersion = 1
	for _, v := range values {
		if v.Count > 0 && !supportedMessagingSummaryVersion(v.MessagingVersion) {
			out.MessagingVersion = 0
			if out.Messaging != nil {
				out.Messaging.Incomplete = true
			}
		}
	}
	if out.Messaging != nil {
		out.MessagingVersion = out.Messaging.EvidenceVersion()
	}
	divisor := float64(out.Count)
	out.AvgMs = weightedAvgMs / divisor
	out.AvgDNSMs = weightedDNSMs / divisor
	out.AvgConnectMs = weightedConnectMs / divisor
	out.AvgTLSMs = weightedTLSMs / divisor
	out.AvgTTFBMs = weightedTTFBMs / divisor
	if durationDigest != nil && durationDigest.Count() == out.Count {
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

// nowTime returns the service's current time, falling back to time.Now when no
// clock was configured. Options run after the struct literal, so a caller can
// pass WithClock(nil) and reach here with an unset field; production paths must
// never depend on the option having been applied.
func (s *Service) nowTime() time.Time {
	if s == nil || s.now == nil {
		return time.Now()
	}
	return s.now()
}

func (s *combinedSink) RecordBrowserDocumentEvidence(evidence *protocol.BrowserDocumentEvidence) error {
	supported := false
	for _, sink := range s.sinks {
		if target, ok := sink.(engine.BrowserDocumentEvidenceSink); ok {
			supported = true
			if err := target.RecordBrowserDocumentEvidence(evidence.Clone()); err != nil {
				return err
			}
		}
	}
	if !supported {
		return fmt.Errorf("browser document archive sink is unavailable")
	}
	return nil
}

// RecordBrowserHARArtifact hands a HAR to the first sink that retains HARs;
// the artifact moves, so only one sink may take it.
func (s *combinedSink) RecordBrowserHARArtifact(artifact protocol.BrowserHARArtifact) error {
	for _, sink := range s.sinks {
		if target, ok := sink.(engine.BrowserHARArtifactSink); ok {
			return target.RecordBrowserHARArtifact(artifact)
		}
	}
	return fmt.Errorf("browser HAR archive sink is unavailable")
}

// RecordBrowserInteractionEvidence hands interaction evidence to the first
// sink that retains it.
func (s *combinedSink) RecordBrowserInteractionEvidence(evidence *protocol.BrowserInteractionEvidence) error {
	for _, sink := range s.sinks {
		if target, ok := sink.(engine.BrowserInteractionEvidenceSink); ok {
			return target.RecordBrowserInteractionEvidence(evidence)
		}
	}
	return fmt.Errorf("browser interaction archive sink is unavailable")
}
