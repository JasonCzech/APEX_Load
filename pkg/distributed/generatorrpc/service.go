package generatorrpc

import (
	"context"
	"crypto/sha256"
	"encoding/json"
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
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type cachedScript struct {
	content  []byte
	hash     string
	lastUsed uint64
}

type assignedTest struct {
	testID            string
	vuserCount        int32
	paused            bool
	durationSeconds   int64
	groups            map[string]int32
	activeGroups      map[string]int32
	groupStats        map[string]GeneratorStats
	groupTelemetry    map[string]MetricsTelemetry
	groupTransactions map[string][]TransactionMetric
	groupAggregates   map[string]metricsAggregateSet
	groupAssignments  map[string][sha256.Size]byte
	groupScripts      map[string]string
	terminalError     string
}

type ServiceOption func(*Service)

type groupExecution struct {
	groupName string
	engine    *engine.MultiGroupEngine
	cancel    context.CancelFunc
	results   *transactionBuffer
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
		acc := a.transactions[name]
		if acc == nil {
			acc = newMetricAccumulator(name)
			a.transactions[name] = acc
		}
		acc.add(tx)
	}
	if target := strings.TrimSpace(tx.DependencyTarget); target != "" {
		acc := a.dependencies[target]
		if acc == nil {
			acc = newMetricAccumulator(target)
			acc.dependencyTarget = target
			a.dependencies[target] = acc
		}
		acc.add(tx)
	}
	if operation := strings.TrimSpace(tx.SOAPOperation); operation != "" {
		acc := a.soap[operation]
		if acc == nil {
			acc = newMetricAccumulator(operation)
			a.soap[operation] = acc
		}
		acc.add(tx)
	}
	if groupName := strings.TrimSpace(tx.Group); groupName != "" {
		acc := a.groups[groupName]
		if acc == nil {
			acc = newMetricAccumulator(groupName)
			a.groups[groupName] = acc
		}
		acc.add(tx)
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

	mu                  sync.RWMutex
	adjustMu            sync.Mutex
	generatorID         string
	maxVUsers           int32
	podResources        podResourceCapacity
	scripts             map[string]cachedScript
	assignments         map[string]assignedTest
	executions          map[string]map[string]*groupExecution
	archiveWriters      map[string]*archiveRecorder
	archiveRecords      map[string]*storage.RunArchiveRecord
	archiveIdentities   map[string]os.FileInfo
	archiveLeases       map[string]int
	scriptCacheMax      int
	scriptCacheBytes    int64
	scriptCacheLimit    int64
	archiveRecordMax    int
	archiveTTL          time.Duration
	archiveNamespace    *generatorArchiveNamespace
	archiveNamespaceErr error
	scriptUseCounter    uint64
	resCollector        *engine.ResourceCollector
	resCollectorMu      sync.Mutex // serializes resCollector.Collect (mutates delta state)
	executionEnabled    bool
	draining            bool
	publishInterval     time.Duration
	completionTimers    map[string]*time.Timer
	subscribers         map[int]chan *pb.MetricsBatch
	nextSubID           int
	droppedBatches      atomic.Int64
}

func (s *Service) SetDraining(draining bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.draining = draining
}

func (s *Service) ActiveVUsers() int32 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.activeVUsersLocked()
}

func WithExecution() ServiceOption {
	return func(s *Service) {
		s.executionEnabled = true
	}
}

func NewService(generatorID string, maxVUsers int, opts ...ServiceOption) *Service {
	if generatorID == "" {
		generatorID = "apexload-generator"
	}
	if maxVUsers <= 0 {
		maxVUsers = 10000
	}

	archiveNamespace, archiveNamespaceErr := prepareGeneratorArchiveNamespace(generatorID, true)
	service := &Service{
		generatorID:         generatorID,
		maxVUsers:           int32(maxVUsers),
		podResources:        detectPodResourceCapacity(int32(maxVUsers)),
		scripts:             make(map[string]cachedScript),
		assignments:         make(map[string]assignedTest),
		executions:          make(map[string]map[string]*groupExecution),
		archiveWriters:      make(map[string]*archiveRecorder),
		archiveRecords:      make(map[string]*storage.RunArchiveRecord),
		archiveIdentities:   make(map[string]os.FileInfo),
		archiveLeases:       make(map[string]int),
		scriptCacheMax:      configuredGeneratorScriptCacheMax(),
		scriptCacheLimit:    configuredGeneratorScriptCacheMaxBytes(),
		archiveRecordMax:    configuredGeneratorArchiveMax(),
		archiveTTL:          configuredGeneratorArchiveTTL(),
		archiveNamespace:    archiveNamespace,
		archiveNamespaceErr: archiveNamespaceErr,
		resCollector:        engine.NewResourceCollector(),
		publishInterval:     time.Second,
		completionTimers:    make(map[string]*time.Timer),
		subscribers:         make(map[int]chan *pb.MetricsBatch),
	}
	if archiveNamespaceErr != nil {
		slog.Error("initialize generator archive namespace", "generator_id", generatorID, "error", archiveNamespaceErr)
	}
	for _, opt := range opts {
		if opt != nil {
			opt(service)
		}
	}
	service.sweepGeneratorArchiveDirectories(time.Now())
	return service
}

func (s *Service) DistributeScript(_ context.Context, payload *pb.ScriptPayload) (*pb.ScriptAck, error) {
	if payload.GetScriptId() == "" {
		return nil, status.Error(codes.InvalidArgument, "script_id is required")
	}

	s.mu.Lock()
	err := s.cacheScriptLocked(payload.GetScriptId(), payload.GetContent(), payload.GetContentHash())
	s.mu.Unlock()
	if err != nil {
		return nil, status.Error(codes.ResourceExhausted, err.Error())
	}

	return &pb.ScriptAck{Cached: true, ContentHash: payload.GetContentHash()}, nil
}

func (s *Service) AssignWork(_ context.Context, assignment *pb.WorkAssignment) (*pb.AssignmentAck, error) {
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
	if assignment.GetVuserCount() < 0 {
		return nil, status.Error(codes.InvalidArgument, "vuser_count must not be negative")
	}
	assignmentBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(assignment)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "fingerprint assignment: %v", err)
	}
	assignmentFingerprint := sha256.Sum256(assignmentBytes)
	// Expire eligible completed archives before deciding whether this test ID is
	// reusable. A retained archive is immutable evidence; starting a new run in
	// the same namespace would truncate files underneath an in-flight download.
	s.sweepArchivedRecords(time.Now())

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.draining {
		return &pb.AssignmentAck{
			Accepted:          false,
			ErrorMessage:      "generator is draining",
			AvailableCapacity: s.availableCapacityLocked(),
		}, nil
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
		if existing, groupExists := current.groups[assignment.GetGroupName()]; groupExists {
			existingFingerprint, fingerprintExists := current.groupAssignments[assignment.GetGroupName()]
			if existing == assignment.GetVuserCount() && fingerprintExists && existingFingerprint == assignmentFingerprint {
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
	}

	if len(assignment.GetScriptContent()) > 0 {
		if err := s.cacheScriptLocked(assignment.GetScriptId(), assignment.GetScriptContent(), ""); err != nil {
			return &pb.AssignmentAck{
				Accepted:          false,
				ErrorMessage:      "cache inline script: " + err.Error(),
				AvailableCapacity: s.availableCapacityLocked(),
			}, nil
		}
	}

	if _, ok := s.touchScriptLocked(assignment.GetScriptId()); !ok {
		return &pb.AssignmentAck{
			Accepted:          false,
			ErrorMessage:      "script is not cached and no inline script content was provided",
			AvailableCapacity: s.availableCapacityLocked(),
		}, nil
	}

	projected := s.activeVUsersLocked() + assignment.GetVuserCount()
	if projected > s.maxVUsers {
		return &pb.AssignmentAck{
			Accepted:          false,
			ErrorMessage:      "insufficient generator capacity",
			AvailableCapacity: s.availableCapacityLocked(),
		}, nil
	}

	current, existed := s.assignments[assignment.GetTestId()]
	current.testID = assignment.GetTestId()
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
	if current.groupScripts == nil {
		current.groupScripts = make(map[string]string)
	}
	if current.durationSeconds == 0 && assignment.GetTargetDurationSeconds() > 0 {
		current.durationSeconds = assignment.GetTargetDurationSeconds()
	}
	current.groups[assignment.GetGroupName()] += assignment.GetVuserCount()
	current.activeGroups[assignment.GetGroupName()] += assignment.GetVuserCount()
	current.groupAssignments[assignment.GetGroupName()] = assignmentFingerprint
	current.groupScripts[assignment.GetGroupName()] = assignment.GetScriptId()
	current.vuserCount += assignment.GetVuserCount()
	s.assignments[assignment.GetTestId()] = current
	if s.executionEnabled {
		if err := s.startExecutionLocked(assignment); err != nil {
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
		if current.durationSeconds > 0 && s.archiveWriters[assignment.GetTestId()] == nil {
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
				current.groups[assignment.GetGroupName()] -= assignment.GetVuserCount()
				current.activeGroups[assignment.GetGroupName()] -= assignment.GetVuserCount()
				current.vuserCount -= assignment.GetVuserCount()
				delete(current.groupAssignments, assignment.GetGroupName())
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
		if !existed && current.durationSeconds > 0 {
			s.scheduleCompletionLocked(current.testID, time.Duration(current.durationSeconds)*time.Second)
		}
	}
	batch := s.metricsBatchLocked(current.testID)

	go s.broadcastMetrics(batch)
	return &pb.AssignmentAck{
		Accepted:          true,
		AvailableCapacity: s.availableCapacityLocked(),
	}, nil
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

	// Scaling the concrete engine happens outside s.mu and may block until the
	// engine is ready. Serialize the complete reserve/scale/commit sequence so a
	// slower earlier request cannot finish after a newer request and leave engine
	// state behind the recorded assignment.
	s.adjustMu.Lock()
	defer s.adjustMu.Unlock()

	s.mu.Lock()
	assignment, ok := s.assignments[request.GetTestId()]
	if !ok {
		s.mu.Unlock()
		return nil, status.Error(codes.NotFound, "test assignment not found")
	}
	currentGroupVUsers, ok := assignment.groups[request.GetGroupName()]
	if !ok {
		s.mu.Unlock()
		return nil, status.Error(codes.NotFound, "group assignment not found")
	}

	updatedTotal := assignment.vuserCount - currentGroupVUsers + request.GetTargetVusers()
	projected := s.activeVUsersLocked() - assignment.vuserCount + updatedTotal
	if projected > s.maxVUsers {
		s.mu.Unlock()
		return nil, status.Error(codes.ResourceExhausted, "insufficient generator capacity")
	}
	var execEngine *engine.MultiGroupEngine
	if s.executionEnabled {
		if execs, ok := s.executions[request.GetTestId()]; ok {
			exec := execs[request.GetGroupName()]
			if exec != nil {
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

	rollbackReservation := func() {
		s.mu.Lock()
		current, exists := s.assignments[request.GetTestId()]
		if exists {
			if reserved, groupExists := current.groups[request.GetGroupName()]; groupExists && reserved == request.GetTargetVusers() {
				current.groups[request.GetGroupName()] = currentGroupVUsers
				current.activeGroups[request.GetGroupName()] = currentGroupVUsers
				current.vuserCount = current.vuserCount - request.GetTargetVusers() + currentGroupVUsers
				s.assignments[request.GetTestId()] = current
			}
		}
		s.mu.Unlock()
	}

	if execEngine != nil {
		select {
		case <-ctx.Done():
			rollbackReservation()
			return nil, status.Errorf(codes.Canceled, "wait for execution readiness: %v", ctx.Err())
		case <-execEngine.Ready():
		}
		if err := execEngine.ScaleVUsers(request.GetGroupName(), int(request.GetTargetVusers())); err != nil {
			rollbackReservation()
			return nil, status.Errorf(codes.FailedPrecondition, "scale running group: %v", err)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	assignment, ok = s.assignments[request.GetTestId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "test assignment not found")
	}
	reservedVUsers, ok := assignment.groups[request.GetGroupName()]
	if !ok {
		return nil, status.Error(codes.NotFound, "group assignment not found")
	}
	if reservedVUsers != request.GetTargetVusers() {
		return nil, status.Error(codes.Aborted, "group assignment changed while scaling")
	}
	batch := s.metricsBatchLocked(request.GetTestId())

	go s.broadcastMetrics(batch)
	return &pb.AdjustmentAck{
		Accepted:      true,
		CurrentVusers: assignment.vuserCount,
	}, nil
}

func (s *Service) AdjustDuration(_ context.Context, request *pb.DurationAdjustment) (*pb.DurationAck, error) {
	if request.GetTestId() == "" {
		return nil, status.Error(codes.InvalidArgument, "test_id is required")
	}
	if request.GetTargetDurationSeconds() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "target_duration_seconds must be positive")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	assignment, ok := s.assignments[request.GetTestId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "test assignment not found")
	}
	if s.executionEnabled {
		if execs, ok := s.executions[request.GetTestId()]; ok {
			for _, exec := range execs {
				if err := exec.engine.AdjustDuration(time.Duration(request.GetTargetDurationSeconds()) * time.Second); err != nil {
					return nil, status.Errorf(codes.FailedPrecondition, "adjust running duration: %v", err)
				}
			}
		}
	}
	assignment.durationSeconds = request.GetTargetDurationSeconds()
	s.assignments[request.GetTestId()] = assignment
	if !s.executionEnabled {
		s.scheduleCompletionLocked(assignment.testID, time.Duration(assignment.durationSeconds)*time.Second)
	}

	return &pb.DurationAck{
		Accepted:               true,
		CurrentDurationSeconds: assignment.durationSeconds,
	}, nil
}

func (s *Service) StopTest(_ context.Context, request *pb.StopRequest) (*pb.StopAck, error) {
	if request.GetTestId() == "" {
		return nil, status.Error(codes.InvalidArgument, "test_id is required")
	}

	s.mu.Lock()

	assignment, ok := s.assignments[request.GetTestId()]
	if request.GetGraceful() && ok {
		execs := s.executions[request.GetTestId()]
		if len(execs) > 0 {
			// Copy pointers while holding s.mu. finishExecution mutates the nested map,
			// so iterating the aliased map after unlocking is a data race and can panic.
			executions := make([]*groupExecution, 0, len(execs))
			for _, exec := range execs {
				executions = append(executions, exec)
			}
			s.mu.Unlock()
			for _, exec := range executions {
				exec.engine.GracefulStop()
			}
			return &pb.StopAck{
				Acknowledged:  true,
				PendingVusers: assignment.vuserCount,
			}, nil
		}
	}
	if ok {
		delete(s.assignments, request.GetTestId())
		s.stopCompletionTimerLocked(request.GetTestId())
	}
	execs := s.executions[request.GetTestId()]
	delete(s.executions, request.GetTestId())
	s.mu.Unlock()

	for _, exec := range execs {
		exec.cancel()
	}
	finalState := "COMPLETE"
	finalError := ""
	if writer := s.takeArchiveWriter(request.GetTestId()); writer != nil {
		err := s.finalizeArchiveWriter(writer, "COMPLETE", time.Now(), "")
		if err != nil {
			finalState = "FAILED"
			finalError = "archive finalization failed: " + err.Error()
		}
	}

	batch := s.metricsBatchForState(request.GetTestId(), 0, false, assignment.groups, finalState, finalError)
	go s.broadcastMetrics(batch)
	if finalError != "" {
		return nil, status.Error(codes.Internal, finalError)
	}
	return &pb.StopAck{
		Acknowledged:  ok,
		PendingVusers: assignment.vuserCount,
	}, nil
}

func (s *Service) StreamMetrics(stream pb.GeneratorControl_StreamMetricsServer) error {
	subscriberID, updates, initial := s.registerSubscriber()
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

			if err := s.setPaused(pause.GetTestId(), pause.GetPause()); err != nil {
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
		case <-stream.Context().Done():
			return nil
		case err := <-recvErr:
			return err
		case batch := <-updates:
			if err := stream.Send(batch); err != nil {
				return err
			}
		}
	}
}

const (
	archiveArtifactChunkBytes   = 1 << 20
	archiveArtifactUnaryMaxSize = 64 << 20
)

func (s *Service) GetArchiveArtifact(_ context.Context, request *pb.ArchiveArtifactRequest) (*pb.ArchiveArtifact, error) {
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
			SupportedProtocols: supportedProtocolNames(),
			CompatibleFeatures: supportedProtocolNames(),
		}); err != nil {
			return err
		}
	}
}

func supportedProtocolNames() []string {
	names := protocol.List()
	if !config.JavaVuserRunnerEnabled() {
		filtered := names[:0]
		for _, name := range names {
			if strings.EqualFold(strings.TrimSpace(name), "java_vuser") {
				continue
			}
			filtered = append(filtered, name)
		}
		names = filtered
	}
	sort.Strings(names)
	return names
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
	var total int32
	for _, assignment := range s.assignments {
		total += assignment.vuserCount
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

func (s *Service) setPaused(testID string, paused bool) error {
	if testID == "" {
		return status.Error(codes.InvalidArgument, "test_id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	assignment, ok := s.assignments[testID]
	if !ok {
		return status.Error(codes.NotFound, "test assignment not found")
	}
	if s.executionEnabled {
		if execs, ok := s.executions[testID]; ok {
			for _, exec := range execs {
				var err error
				if paused {
					err = exec.engine.Pause()
				} else {
					err = exec.engine.Resume()
				}
				if err != nil {
					return status.Errorf(codes.FailedPrecondition, "update running pause state: %v", err)
				}
			}
		}
	}
	assignment.paused = paused
	s.assignments[testID] = assignment
	batch := s.metricsBatchLocked(testID)
	go s.broadcastMetrics(batch)
	return nil
}

func (s *Service) FailTest(testID string, message string) {
	s.mu.Lock()
	assignment, ok := s.assignments[testID]
	if ok {
		delete(s.assignments, testID)
	}
	execs := s.executions[testID]
	delete(s.executions, testID)
	writer := s.archiveWriters[testID]
	delete(s.archiveWriters, testID)
	s.stopCompletionTimerLocked(testID)
	s.mu.Unlock()
	for _, exec := range execs {
		exec.cancel()
	}
	if !ok {
		return
	}
	if writer != nil {
		err := s.finalizeArchiveWriter(writer, "FAILED", time.Now(), message)
		if err != nil {
			if message != "" {
				message += "; "
			}
			message += "archive finalization failed: " + err.Error()
		}
	}
	s.broadcastMetrics(s.metricsBatchForState(testID, 0, false, assignment.groups, "FAILED", message))
}

func (s *Service) PublishMetrics(testID string, groupName string, stats GeneratorStats, transactions []TransactionMetric, telemetry MetricsTelemetry) error {
	return s.publishMetricsWithAggregates(testID, groupName, stats, transactions, telemetry, metricsAggregateSet{})
}

func (s *Service) publishMetricsWithAggregates(testID string, groupName string, stats GeneratorStats, transactions []TransactionMetric, telemetry MetricsTelemetry, aggregates metricsAggregateSet) error {
	if testID == "" {
		return status.Error(codes.InvalidArgument, "test_id is required")
	}
	if groupName == "" {
		return status.Error(codes.InvalidArgument, "group_name is required")
	}

	s.mu.Lock()
	assignment, ok := s.assignments[testID]
	if !ok {
		s.mu.Unlock()
		return status.Error(codes.NotFound, "test assignment not found")
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
	groupTransactions := append([]TransactionMetric(nil), transactions...)
	for index := range groupTransactions {
		if strings.TrimSpace(groupTransactions[index].Group) == "" {
			groupTransactions[index].Group = groupName
		}
	}
	assignment.groupTransactions[groupName] = groupTransactions
	assignment.groupAggregates[groupName] = aggregates
	s.assignments[testID] = assignment
	batch := s.metricsBatchLocked(testID)
	writer := s.archiveWriters[testID]
	s.mu.Unlock()

	if writer != nil {
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
	}

	go s.broadcastMetrics(batch)
	return nil
}

func (s *Service) registerSubscriber() (int, chan *pb.MetricsBatch, []*pb.MetricsBatch) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := s.nextSubID
	s.nextSubID++
	ch := make(chan *pb.MetricsBatch, 16)
	s.subscribers[id] = ch

	initial := make([]*pb.MetricsBatch, 0, len(s.assignments))
	for testID := range s.assignments {
		initial = append(initial, s.metricsBatchLocked(testID))
	}

	return id, ch, initial
}

func (s *Service) unregisterSubscriber(id int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.subscribers[id]; !ok {
		return
	}
	delete(s.subscribers, id)
}

func (s *Service) broadcastMetrics(batch *pb.MetricsBatch) {
	if batch == nil {
		return
	}

	s.mu.RLock()
	subscribers := make([]chan *pb.MetricsBatch, 0, len(s.subscribers))
	for _, subscriber := range s.subscribers {
		subscribers = append(subscribers, subscriber)
	}
	s.mu.RUnlock()

	for _, subscriber := range subscribers {
		select {
		case subscriber <- batch:
		default:
			s.droppedBatches.Add(1)
		}
	}
}

func (s *Service) metricsBatchLocked(testID string) *pb.MetricsBatch {
	assignment, ok := s.assignments[testID]
	if !ok {
		return s.metricsBatchForState(testID, 0, false, nil, "COMPLETE", "")
	}
	lifecycleState := "RUNNING"
	if assignment.paused {
		lifecycleState = "PAUSED"
	}
	groupAssignments := assignment.activeGroups
	if len(groupAssignments) == 0 {
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
	transactions := make([]TransactionMetric, 0)
	for _, groupTransactions := range assignment.groupTransactions {
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
	return s.metricsBatchForMetrics(testID, stats, transactions, aggregates, assignment.paused, groupAssignments, lifecycleState, "", telemetry)
}

func (s *Service) metricsBatchForState(testID string, vuserCount int32, paused bool, groupAssignments map[string]int32, lifecycleState string, lifecycleError string) *pb.MetricsBatch {
	return s.metricsBatchForMetrics(testID, GeneratorStats{ActiveVUsers: vuserCount}, nil, metricsAggregateSet{}, paused, groupAssignments, lifecycleState, lifecycleError, nil)
}

func (s *Service) metricsBatchForMetrics(testID string, stats GeneratorStats, transactions []TransactionMetric, aggregates metricsAggregateSet, paused bool, groupAssignments map[string]int32, lifecycleState string, lifecycleError string, telemetry *MetricsTelemetry) *pb.MetricsBatch {
	activeVUsers := stats.ActiveVUsers
	if paused {
		activeVUsers = 0
	}
	stats.ActiveVUsers = activeVUsers
	groups := make([]*pb.GroupMetric, 0)
	if len(groupAssignments) > 0 {
		groups = make([]*pb.GroupMetric, 0, len(groupAssignments))
		for name, groupVUsers := range groupAssignments {
			activeGroupVUsers := groupVUsers
			if paused {
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
	s.completionTimers[testID] = time.AfterFunc(duration, func() {
		s.completeAssignment(testID)
	})
}

func (s *Service) stopCompletionTimerLocked(testID string) {
	timer, ok := s.completionTimers[testID]
	if !ok {
		return
	}
	delete(s.completionTimers, testID)
	timer.Stop()
}

func (s *Service) completeAssignment(testID string) {
	s.mu.Lock()
	assignment, ok := s.assignments[testID]
	if ok {
		delete(s.assignments, testID)
	}
	delete(s.executions, testID)
	s.stopCompletionTimerLocked(testID)
	writer := s.archiveWriters[testID]
	delete(s.archiveWriters, testID)
	s.mu.Unlock()
	if !ok {
		return
	}
	finalState := "COMPLETE"
	finalError := ""
	if writer != nil {
		err := s.finalizeArchiveWriter(writer, finalState, time.Now(), "")
		if err != nil {
			finalState = "FAILED"
			finalError = "archive finalization failed: " + err.Error()
		}
	}
	s.broadcastMetrics(s.metricsBatchForState(testID, 0, false, assignment.groups, finalState, finalError))
}

func (s *Service) startExecutionLocked(assignment *pb.WorkAssignment) error {
	if !s.executionEnabled {
		return nil
	}
	if assignment.GetGroupName() == "" {
		return status.Error(codes.InvalidArgument, "group_name is required")
	}
	if _, ok := s.executions[assignment.GetTestId()]; !ok {
		s.executions[assignment.GetTestId()] = make(map[string]*groupExecution)
	}
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
	duration := time.Duration(s.assignments[assignment.GetTestId()].durationSeconds) * time.Second
	if duration <= 0 {
		duration = time.Second
	}
	redactionConfig, err := engine.NewRedactionConfig(redactionRulesFromProto(assignment.GetRedactionRules()))
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "build redaction config: %v", err)
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
	}
	mge, err := engine.NewMultiGroup(engine.MultiGroupConfig{
		Groups: []engine.GroupConfig{{
			Name:            assignment.GetGroupName(),
			Script:          &script,
			VUsers:          int(assignment.GetVuserCount()),
			RampUp:          time.Duration(assignment.GetRampUpSeconds()) * time.Second,
			AllowZeroVUsers: assignment.GetVuserCount() == 0,
			InitStrategy:    "at_runtime",
			ResultSink:      combineSinks(results, writer),
			RedactionConfig: redactionConfig,
		}},
		Duration: duration,
	})
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "build execution engine: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	exec := &groupExecution{
		groupName: assignment.GetGroupName(),
		engine:    mge,
		cancel:    cancel,
		results:   results,
	}
	s.executions[assignment.GetTestId()][assignment.GetGroupName()] = exec
	go s.runExecutionPublisher(ctx, assignment.GetTestId(), assignment.GetGroupName(), exec)
	go func(testID string, groupName string, exec *groupExecution) {
		_, err := exec.engine.Run(ctx)
		s.publishExecutionSnapshot(testID, groupName, exec)
		exec.cancel()
		s.finishExecution(testID, groupName, err)
	}(assignment.GetTestId(), assignment.GetGroupName(), exec)
	return nil
}

func (s *Service) runExecutionPublisher(ctx context.Context, testID string, groupName string, exec *groupExecution) {
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
	if exec == nil {
		return
	}
	snapshot, ok := exec.engine.LatestSnapshot()
	transactions, aggregateWindow, window := exec.results.DrainWindow()
	if !ok && len(transactions) == 0 {
		return
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
	if ok {
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
	_ = s.publishMetricsWithAggregates(testID, groupName, stats, transactions, telemetry, aggregates)
}

func (s *Service) finishExecution(testID string, groupName string, err error) {
	s.mu.Lock()
	assignment, ok := s.assignments[testID]
	if !ok {
		s.mu.Unlock()
		return
	}
	var siblingCancels []context.CancelFunc
	if execs, ok := s.executions[testID]; ok {
		delete(execs, groupName)
		if err != nil && assignment.terminalError == "" {
			assignment.terminalError = err.Error()
			for _, sibling := range execs {
				if sibling != nil && sibling.cancel != nil {
					siblingCancels = append(siblingCancels, sibling.cancel)
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
	if assignment.groupStats != nil {
		delete(assignment.groupStats, groupName)
	}
	if assignment.groupTelemetry != nil {
		delete(assignment.groupTelemetry, groupName)
	}
	if assignment.groupTransactions != nil {
		delete(assignment.groupTransactions, groupName)
	}
	if assignment.groupAggregates != nil {
		delete(assignment.groupAggregates, groupName)
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
	delete(s.assignments, testID)
	s.stopCompletionTimerLocked(testID)
	writer := s.archiveWriters[testID]
	delete(s.archiveWriters, testID)
	s.mu.Unlock()
	for _, cancel := range siblingCancels {
		cancel()
	}

	finalState := "COMPLETE"
	finalError := assignment.terminalError
	if finalError != "" {
		finalState = "FAILED"
	}
	if writer != nil {
		finalizeErr := s.finalizeArchiveWriter(writer, finalState, time.Now(), finalError)
		if finalizeErr != nil {
			finalState = "FAILED"
			if finalError != "" {
				finalError += "; "
			}
			finalError += "archive finalization failed: " + finalizeErr.Error()
		}
	}
	s.broadcastMetrics(s.metricsBatchForState(testID, 0, false, assignment.groups, finalState, finalError))
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
	record, err := writer.Finalize(status, completedAt, errText)
	if err == nil && record != nil {
		err = s.storeArchiveRecordWithIdentity(record, writer.runIdentity)
	}
	if err != nil {
		_ = writer.Discard()
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
