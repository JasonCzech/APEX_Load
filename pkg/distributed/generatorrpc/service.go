package generatorrpc

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type cachedScript struct {
	content []byte
	hash    string
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
}

func (b *transactionBuffer) RecordResult(result *protocol.Result) error {
	if result == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.transactions = append(b.transactions, TransactionMetric{
		Name:             result.ActionName,
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
	})
	if len(b.transactions) > 1024 {
		b.transactions = append([]TransactionMetric(nil), b.transactions[len(b.transactions)-1024:]...)
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

type Service struct {
	pb.UnimplementedGeneratorControlServer

	mu               sync.RWMutex
	generatorID      string
	maxVUsers        int32
	podResources     podResourceCapacity
	scripts          map[string]cachedScript
	assignments      map[string]assignedTest
	executions       map[string]map[string]*groupExecution
	archiveWriters   map[string]*archiveRecorder
	archiveRecords   map[string]*storage.RunArchiveRecord
	resCollector     *engine.ResourceCollector
	executionEnabled bool
	publishInterval  time.Duration
	completionTimers map[string]*time.Timer
	subscribers      map[int]chan *pb.MetricsBatch
	nextSubID        int
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

	service := &Service{
		generatorID:      generatorID,
		maxVUsers:        int32(maxVUsers),
		podResources:     detectPodResourceCapacity(int32(maxVUsers)),
		scripts:          make(map[string]cachedScript),
		assignments:      make(map[string]assignedTest),
		executions:       make(map[string]map[string]*groupExecution),
		archiveWriters:   make(map[string]*archiveRecorder),
		archiveRecords:   make(map[string]*storage.RunArchiveRecord),
		resCollector:     engine.NewResourceCollector(),
		publishInterval:  time.Second,
		completionTimers: make(map[string]*time.Timer),
		subscribers:      make(map[int]chan *pb.MetricsBatch),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(service)
		}
	}
	return service
}

func (s *Service) DistributeScript(_ context.Context, payload *pb.ScriptPayload) (*pb.ScriptAck, error) {
	if payload.GetScriptId() == "" {
		return nil, status.Error(codes.InvalidArgument, "script_id is required")
	}

	s.mu.Lock()
	s.scripts[payload.GetScriptId()] = cachedScript{content: payload.GetContent(), hash: payload.GetContentHash()}
	s.mu.Unlock()

	return &pb.ScriptAck{Cached: true, ContentHash: payload.GetContentHash()}, nil
}

func (s *Service) AssignWork(_ context.Context, assignment *pb.WorkAssignment) (*pb.AssignmentAck, error) {
	if assignment.GetTestId() == "" {
		return nil, status.Error(codes.InvalidArgument, "test_id is required")
	}
	if assignment.GetGroupName() == "" {
		return nil, status.Error(codes.InvalidArgument, "group_name is required")
	}
	if assignment.GetScriptId() == "" {
		return nil, status.Error(codes.InvalidArgument, "script_id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if len(assignment.GetScriptContent()) > 0 {
		s.scripts[assignment.GetScriptId()] = cachedScript{content: assignment.GetScriptContent()}
	}

	if _, ok := s.scripts[assignment.GetScriptId()]; !ok {
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
	if current.durationSeconds == 0 && assignment.GetTargetDurationSeconds() > 0 {
		current.durationSeconds = assignment.GetTargetDurationSeconds()
	}
	current.groups[assignment.GetGroupName()] += assignment.GetVuserCount()
	current.activeGroups[assignment.GetGroupName()] += assignment.GetVuserCount()
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
	} else if !existed && current.durationSeconds > 0 {
		s.scheduleCompletionLocked(current.testID, time.Duration(current.durationSeconds)*time.Second)
	}
	batch := s.metricsBatchLocked(current.testID)

	go s.broadcastMetrics(batch)
	return &pb.AssignmentAck{
		Accepted:          true,
		AvailableCapacity: s.availableCapacityLocked(),
	}, nil
}

func (s *Service) AdjustVUsers(_ context.Context, request *pb.VUserAdjustment) (*pb.AdjustmentAck, error) {
	if request.GetTestId() == "" {
		return nil, status.Error(codes.InvalidArgument, "test_id is required")
	}
	if request.GetGroupName() == "" {
		return nil, status.Error(codes.InvalidArgument, "group_name is required")
	}
	if request.GetTargetVusers() < 0 {
		return nil, status.Error(codes.InvalidArgument, "target_vusers must be non-negative")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	assignment, ok := s.assignments[request.GetTestId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "test assignment not found")
	}
	currentGroupVUsers, ok := assignment.groups[request.GetGroupName()]
	if !ok {
		return nil, status.Error(codes.NotFound, "group assignment not found")
	}

	updatedTotal := assignment.vuserCount - currentGroupVUsers + request.GetTargetVusers()
	projected := s.activeVUsersLocked() - assignment.vuserCount + updatedTotal
	if projected > s.maxVUsers {
		return nil, status.Error(codes.ResourceExhausted, "insufficient generator capacity")
	}
	if s.executionEnabled {
		if execs, ok := s.executions[request.GetTestId()]; ok {
			exec := execs[request.GetGroupName()]
			if exec != nil {
				if err := exec.engine.ScaleVUsers(request.GetGroupName(), int(request.GetTargetVusers())); err != nil {
					return nil, status.Errorf(codes.FailedPrecondition, "scale running group: %v", err)
				}
			}
		}
	}

	assignment.groups[request.GetGroupName()] = request.GetTargetVusers()
	assignment.activeGroups[request.GetGroupName()] = request.GetTargetVusers()
	assignment.vuserCount = updatedTotal
	s.assignments[request.GetTestId()] = assignment
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
	if ok {
		delete(s.assignments, request.GetTestId())
		s.stopCompletionTimerLocked(request.GetTestId())
	}
	execs := s.executions[request.GetTestId()]
	delete(s.executions, request.GetTestId())
	batch := s.metricsBatchForState(request.GetTestId(), 0, false, assignment.groups, "COMPLETE", "")
	s.mu.Unlock()

	for _, exec := range execs {
		exec.cancel()
	}
	if writer := s.takeArchiveWriter(request.GetTestId()); writer != nil {
		record, err := writer.Finalize("COMPLETE", time.Now(), "")
		if err == nil && record != nil {
			s.storeArchiveRecord(record)
		}
	}

	go s.broadcastMetrics(batch)
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

func (s *Service) GetArchiveArtifact(_ context.Context, request *pb.ArchiveArtifactRequest) (*pb.ArchiveArtifact, error) {
	if request.GetTestId() == "" {
		return nil, status.Error(codes.InvalidArgument, "test_id is required")
	}
	if request.GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "name is required")
	}
	s.mu.RLock()
	record := s.archiveRecords[request.GetTestId()]
	s.mu.RUnlock()
	if record == nil {
		return nil, status.Error(codes.NotFound, "archive not found")
	}
	manifestPath := record.ManifestPath
	fullPath := manifestPath
	if request.GetName() != "manifest.json" {
		fullPath = filepath.Join(record.ArchivePath, request.GetName())
	}
	content, err := os.ReadFile(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, status.Error(codes.NotFound, "archive artifact not found")
		}
		return nil, status.Errorf(codes.Internal, "read archive artifact: %v", err)
	}
	return &pb.ArchiveArtifact{Name: request.GetName(), Content: content}, nil
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

		stats := generatorStatsFromResources(s.collectResources(), s.activeVUsersLocked())
		if err := stream.Send(&pb.HeartbeatPong{
			GeneratorId:        s.generatorID,
			TimestampUnixMs:    maxInt64(time.Now().UnixMilli(), ping.GetTimestampUnixMs()),
			ActiveTestIds:      s.activeTestIDs(),
			Stats:              toProtoStats(stats),
			AvailableCapacity:  s.availableCapacityLocked(),
			CpuLimitCores:      s.podResources.CPULimitCores,
			MemoryLimitBytes:   s.podResources.MemoryLimitBytes,
			MaxVusers:          s.podResources.MaxVUsers,
			Version:            "0.1.0",
			SupportedProtocols: supportedProtocolNames(),
			CompatibleFeatures: supportedProtocolNames(),
		}); err != nil {
			return err
		}
	}
}

func supportedProtocolNames() []string {
	names := protocol.List()
	sort.Strings(names)
	return names
}

func (s *Service) collectResources() *engine.SystemResources {
	if s == nil || s.resCollector == nil {
		return nil
	}
	return s.resCollector.Collect()
}

func generatorStatsFromResources(resources *engine.SystemResources, activeVUsers int32) GeneratorStats {
	stats := GeneratorStats{
		ActiveVUsers:   activeVUsers,
		GoroutineCount: int32(runtime.NumGoroutine()),
	}
	if resources == nil {
		return stats
	}
	stats.CPUPercent = resources.CPUProcessPct
	stats.MemoryBytes = int64(resources.MemProcessMb * 1024 * 1024)
	stats.GoroutineCount = firstNonZeroInt32(resources.GoroutineCount, stats.GoroutineCount)
	stats.CPUUserPct = resources.CPUUserPct
	stats.CPUSystemPct = resources.CPUSystemPct
	stats.CPUIOWaitPct = resources.CPUIOWaitPct
	stats.CPUStealPct = resources.CPUStealPct
	stats.Load1 = resources.Load1
	stats.Load5 = resources.Load5
	stats.Load15 = resources.Load15
	stats.ThreadCount = resources.ThreadCount
	stats.OpenFDCount = resources.OpenFDCount
	stats.HeapAllocMB = resources.HeapAllocMB
	stats.HeapInUseMB = resources.HeapInUseMB
	stats.GCCountDelta = resources.GCCountDelta
	stats.GCPauseMs = resources.GCPauseMs
	stats.ContainerCPULimitCores = resources.ContainerCPULimitCores
	stats.ContainerCPUUsagePct = resources.ContainerCPUUsagePct
	stats.ContainerCPUThrottledPeriodsDelta = resources.ContainerCPUThrottledPeriodsDelta
	stats.ContainerCPUThrottledSecondsRate = resources.ContainerCPUThrottledSecondsRate
	stats.ContainerMemoryWorkingSetMB = resources.ContainerMemoryWorkingSetMB
	stats.ContainerMemoryLimitMB = resources.ContainerMemoryLimitMB
	stats.ContainerMemoryUsagePct = resources.ContainerMemoryUsagePct
	stats.ContainerMemoryFailCountDelta = resources.ContainerMemoryFailCountDelta
	stats.ContainerOOMKillCountDelta = resources.ContainerOOMKillCountDelta
	stats.NetDropInPerSec = resources.NetDropInPerSec
	stats.NetDropOutPerSec = resources.NetDropOutPerSec
	stats.NetErrorInPerSec = resources.NetErrorInPerSec
	stats.NetErrorOutPerSec = resources.NetErrorOutPerSec
	stats.TCPRetransmitsPerSec = resources.TCPRetransmitsPerSec
	stats.TCPEstablished = int32(resources.TCPEstablished)
	stats.TCPTimeWait = int32(resources.TCPTimeWait)
	stats.EphemeralPortUtilizationPct = resources.EphemeralPortUtilizationPct
	stats.DiskIOTimePct = resources.DiskIOTimePct
	stats.DiskWeightedIOTimePct = resources.DiskWeightedIOTimePct
	stats.DiskInProgress = resources.DiskInProgress
	return stats
}

func resourcesFromGeneratorStats(stats GeneratorStats) *engine.SystemResources {
	return &engine.SystemResources{
		CPUProcessPct:                     stats.CPUPercent,
		MemProcessMb:                      float64(stats.MemoryBytes) / (1024 * 1024),
		GoroutineCount:                    stats.GoroutineCount,
		CPUUserPct:                        stats.CPUUserPct,
		CPUSystemPct:                      stats.CPUSystemPct,
		CPUIOWaitPct:                      stats.CPUIOWaitPct,
		CPUStealPct:                       stats.CPUStealPct,
		Load1:                             stats.Load1,
		Load5:                             stats.Load5,
		Load15:                            stats.Load15,
		ThreadCount:                       stats.ThreadCount,
		OpenFDCount:                       stats.OpenFDCount,
		HeapAllocMB:                       stats.HeapAllocMB,
		HeapInUseMB:                       stats.HeapInUseMB,
		GCCountDelta:                      stats.GCCountDelta,
		GCPauseMs:                         stats.GCPauseMs,
		ContainerCPULimitCores:            stats.ContainerCPULimitCores,
		ContainerCPUUsagePct:              stats.ContainerCPUUsagePct,
		ContainerCPUThrottledPeriodsDelta: stats.ContainerCPUThrottledPeriodsDelta,
		ContainerCPUThrottledSecondsRate:  stats.ContainerCPUThrottledSecondsRate,
		ContainerMemoryWorkingSetMB:       stats.ContainerMemoryWorkingSetMB,
		ContainerMemoryLimitMB:            stats.ContainerMemoryLimitMB,
		ContainerMemoryUsagePct:           stats.ContainerMemoryUsagePct,
		ContainerMemoryFailCountDelta:     stats.ContainerMemoryFailCountDelta,
		ContainerOOMKillCountDelta:        stats.ContainerOOMKillCountDelta,
		NetDropInPerSec:                   stats.NetDropInPerSec,
		NetDropOutPerSec:                  stats.NetDropOutPerSec,
		NetErrorInPerSec:                  stats.NetErrorInPerSec,
		NetErrorOutPerSec:                 stats.NetErrorOutPerSec,
		TCPRetransmitsPerSec:              stats.TCPRetransmitsPerSec,
		TCPEstablished:                    int(stats.TCPEstablished),
		TCPTimeWait:                       int(stats.TCPTimeWait),
		EphemeralPortUtilizationPct:       stats.EphemeralPortUtilizationPct,
		DiskIOTimePct:                     stats.DiskIOTimePct,
		DiskWeightedIOTimePct:             stats.DiskWeightedIOTimePct,
		DiskInProgress:                    stats.DiskInProgress,
	}
}

func firstNonZeroInt32(values ...int32) int32 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func mergeGeneratorStats(left GeneratorStats, right GeneratorStats) GeneratorStats {
	left.ActiveVUsers += right.ActiveVUsers
	left.CPUPercent += right.CPUPercent
	left.MemoryBytes += right.MemoryBytes
	left.GoroutineCount += right.GoroutineCount
	left.ThreadCount += right.ThreadCount
	left.OpenFDCount += right.OpenFDCount
	left.GCCountDelta += right.GCCountDelta
	left.ContainerCPUThrottledPeriodsDelta += right.ContainerCPUThrottledPeriodsDelta
	left.ContainerMemoryFailCountDelta += right.ContainerMemoryFailCountDelta
	left.ContainerOOMKillCountDelta += right.ContainerOOMKillCountDelta
	left.DiskInProgress += right.DiskInProgress
	left.CPUUserPct = maxFloat64(left.CPUUserPct, right.CPUUserPct)
	left.CPUSystemPct = maxFloat64(left.CPUSystemPct, right.CPUSystemPct)
	left.CPUIOWaitPct = maxFloat64(left.CPUIOWaitPct, right.CPUIOWaitPct)
	left.CPUStealPct = maxFloat64(left.CPUStealPct, right.CPUStealPct)
	left.Load1 = maxFloat64(left.Load1, right.Load1)
	left.Load5 = maxFloat64(left.Load5, right.Load5)
	left.Load15 = maxFloat64(left.Load15, right.Load15)
	left.HeapAllocMB += right.HeapAllocMB
	left.HeapInUseMB += right.HeapInUseMB
	left.GCPauseMs += right.GCPauseMs
	left.ContainerCPULimitCores += right.ContainerCPULimitCores
	left.ContainerCPUUsagePct = maxFloat64(left.ContainerCPUUsagePct, right.ContainerCPUUsagePct)
	left.ContainerCPUThrottledSecondsRate += right.ContainerCPUThrottledSecondsRate
	left.ContainerMemoryWorkingSetMB += right.ContainerMemoryWorkingSetMB
	left.ContainerMemoryLimitMB += right.ContainerMemoryLimitMB
	left.ContainerMemoryUsagePct = maxFloat64(left.ContainerMemoryUsagePct, right.ContainerMemoryUsagePct)
	left.NetDropInPerSec += right.NetDropInPerSec
	left.NetDropOutPerSec += right.NetDropOutPerSec
	left.NetErrorInPerSec += right.NetErrorInPerSec
	left.NetErrorOutPerSec += right.NetErrorOutPerSec
	left.TCPRetransmitsPerSec += right.TCPRetransmitsPerSec
	left.TCPEstablished += right.TCPEstablished
	left.TCPTimeWait += right.TCPTimeWait
	left.EphemeralPortUtilizationPct = maxFloat64(left.EphemeralPortUtilizationPct, right.EphemeralPortUtilizationPct)
	left.DiskIOTimePct = maxFloat64(left.DiskIOTimePct, right.DiskIOTimePct)
	left.DiskWeightedIOTimePct = maxFloat64(left.DiskWeightedIOTimePct, right.DiskWeightedIOTimePct)
	return left
}

func maxFloat64(left float64, right float64) float64 {
	if right > left {
		return right
	}
	return left
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
	s.stopCompletionTimerLocked(testID)
	s.mu.Unlock()
	for _, exec := range execs {
		exec.cancel()
	}
	if !ok {
		return
	}
	s.broadcastMetrics(s.metricsBatchForState(testID, 0, false, assignment.groups, "FAILED", message))
}

func (s *Service) PublishMetrics(testID string, groupName string, stats GeneratorStats, transactions []TransactionMetric, telemetry MetricsTelemetry) error {
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
	assignment.activeGroups[groupName] = stats.ActiveVUsers
	assignment.groupStats[groupName] = stats
	assignment.groupTelemetry[groupName] = telemetry
	assignment.groupTransactions[groupName] = append([]TransactionMetric(nil), transactions...)
	s.assignments[testID] = assignment
	batch := s.metricsBatchLocked(testID)
	writer := s.archiveWriters[testID]
	s.mu.Unlock()

	if writer != nil {
		if batch.GetStats() != nil {
			_ = writer.RecordResourceSample(time.UnixMilli(batch.TimestampUnixMs), int64(batch.GetStats().GetActiveVusers()), resourcesFromGeneratorStats(generatorStatsFromProto(batch.GetStats())))
		}
		totalErrors := int64(0)
		totalDuration := 0.0
		durations := make([]float64, 0, len(batch.Transactions))
		for _, tx := range batch.Transactions {
			totalDuration += tx.DurationMs
			durations = append(durations, tx.DurationMs)
			if !tx.Success {
				totalErrors++
			}
		}
		avgResponse := 0.0
		if len(batch.Transactions) > 0 {
			avgResponse = totalDuration / float64(len(batch.Transactions))
		}
		_ = writer.RecordIntervalSummary(archiveIntervalSummary{
			Timestamp:     time.UnixMilli(batch.TimestampUnixMs),
			ActiveVUsers:  int64(batch.GetStats().GetActiveVusers()),
			TPS:           float64(len(batch.Transactions)),
			AvgResponseMs: avgResponse,
			ErrorPct:      errorPct(totalErrors, int64(len(batch.Transactions))),
			TotalTx:       int64(len(batch.Transactions)),
			TotalErrors:   totalErrors,
			P50Ms:         percentileValue(durations, 0.50),
			P90Ms:         percentileValue(durations, 0.90),
			P95Ms:         percentileValue(durations, 0.95),
			P99Ms:         percentileValue(durations, 0.99),
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
	for groupName, activeVUsers := range groupAssignments {
		if !groupsWithActiveStats[groupName] {
			stats.ActiveVUsers += activeVUsers
		}
	}
	if stats.ActiveVUsers == 0 && len(groupAssignments) == 0 {
		stats.ActiveVUsers = assignment.vuserCount
	}
	return s.metricsBatchForMetrics(testID, stats, transactions, assignment.paused, groupAssignments, lifecycleState, "", telemetry)
}

func (s *Service) metricsBatchForState(testID string, vuserCount int32, paused bool, groupAssignments map[string]int32, lifecycleState string, lifecycleError string) *pb.MetricsBatch {
	return s.metricsBatchForMetrics(testID, GeneratorStats{ActiveVUsers: vuserCount}, nil, paused, groupAssignments, lifecycleState, lifecycleError, nil)
}

func (s *Service) metricsBatchForMetrics(testID string, stats GeneratorStats, transactions []TransactionMetric, paused bool, groupAssignments map[string]int32, lifecycleState string, lifecycleError string, telemetry *MetricsTelemetry) *pb.MetricsBatch {
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
			groups = append(groups, &pb.GroupMetric{Name: name, ActiveVusers: activeGroupVUsers})
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
				MessageLatencyMs:       tx.ServerTelemetry.MessageLatencyMs,
				MessagesSent:           tx.ServerTelemetry.MessagesSent,
				MessagesReceived:       tx.ServerTelemetry.MessagesReceived,
				MessagesSentPerSec:     tx.ServerTelemetry.MessagesSentPerSec,
				MessagesReceivedPerSec: tx.ServerTelemetry.MessagesReceivedPerSec,
				ConsumerLagMs:          tx.ServerTelemetry.ConsumerLagMs,
			}
		}
		protoTransactions = append(protoTransactions, &pb.TransactionMetric{
			Name:             tx.Name,
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
		})
	}
	var protoTelemetry *pb.MetricsBatchTelemetry
	if telemetry != nil {
		protoTelemetry = &pb.MetricsBatchTelemetry{
			DroppedResults:    telemetry.DroppedResults,
			CollectedAtUnixMs: telemetry.CollectedAtUnixMs,
			PublishedAtUnixMs: telemetry.PublishedAtUnixMs,
			SampleAgeMs:       telemetry.SampleAgeMs,
		}
	}
	return &pb.MetricsBatch{
		TestId:          testID,
		GeneratorId:     s.generatorID,
		TimestampUnixMs: time.Now().UnixMilli(),
		Transactions:    protoTransactions,
		Stats:           toProtoStats(stats),
		Groups:          groups,
		LifecycleState:  lifecycleState,
		LifecycleError:  lifecycleError,
		Telemetry:       protoTelemetry,
	}
}

func mergeMetricsTelemetry(values map[string]MetricsTelemetry) *MetricsTelemetry {
	if len(values) == 0 {
		return nil
	}
	combined := &MetricsTelemetry{}
	hasCollectedAt := false
	hasPublishedAt := false
	for _, value := range values {
		combined.DroppedResults += value.DroppedResults
		if value.CollectedAtUnixMs > 0 && (!hasCollectedAt || value.CollectedAtUnixMs < combined.CollectedAtUnixMs) {
			combined.CollectedAtUnixMs = value.CollectedAtUnixMs
			hasCollectedAt = true
		}
		if value.PublishedAtUnixMs > 0 && (!hasPublishedAt || value.PublishedAtUnixMs > combined.PublishedAtUnixMs) {
			combined.PublishedAtUnixMs = value.PublishedAtUnixMs
			hasPublishedAt = true
		}
		if value.SampleAgeMs > combined.SampleAgeMs {
			combined.SampleAgeMs = value.SampleAgeMs
		}
	}
	return combined
}

func cloneServerTelemetry(input *protocol.ServerTelemetry) *ServerTelemetry {
	if input == nil {
		return nil
	}
	return &ServerTelemetry{
		ServerProcessingMs:     input.ServerProcessingMs,
		TransferMs:             input.TransferMs,
		RequestPayloadKB:       input.RequestPayloadKB,
		ResponsePayloadKB:      input.ResponsePayloadKB,
		ConnectionReused:       input.ConnectionReused,
		RequestSizeClass:       input.RequestSizeClass,
		ResponseSizeClass:      input.ResponseSizeClass,
		MessageLatencyMs:       input.MessageLatencyMs,
		MessagesSent:           int32(input.MessagesSent),
		MessagesReceived:       int32(input.MessagesReceived),
		MessagesSentPerSec:     input.MessagesSentPerSec,
		MessagesReceivedPerSec: input.MessagesReceivedPerSec,
		ConsumerLagMs:          input.ConsumerLagMs,
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
	s.mu.Unlock()
	if !ok {
		return
	}
	s.broadcastMetrics(s.metricsBatchForState(testID, 0, false, assignment.groups, "COMPLETE", ""))
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
		cached, ok := s.scripts[assignment.GetScriptId()]
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
	results := &transactionBuffer{}
	writer := s.archiveWriters[assignment.GetTestId()]
	if writer == nil {
		archiveWriter, archiveErr := newArchiveRecorder(s.generatorID, assignment.GetTestId(), script.Name)
		if archiveErr != nil {
			return status.Errorf(codes.Internal, "initialize archive recorder: %v", archiveErr)
		}
		writer = archiveWriter
		s.archiveWriters[assignment.GetTestId()] = writer
	}
	mge, err := engine.NewMultiGroup(engine.MultiGroupConfig{
		Groups: []engine.GroupConfig{{
			Name:         assignment.GetGroupName(),
			Script:       &script,
			VUsers:       int(assignment.GetVuserCount()),
			RampUp:       time.Duration(assignment.GetRampUpSeconds()) * time.Second,
			InitStrategy: "at_runtime",
			ResultSink:   combineSinks(results, writer),
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
	transactions := exec.results.Drain()
	if !ok && len(transactions) == 0 {
		return
	}
	if ok && snapshot.Transactions == 0 && len(transactions) == 0 {
		return
	}
	stats := GeneratorStats{GoroutineCount: int32(runtime.NumGoroutine())}
	telemetry := MetricsTelemetry{}
	if ok {
		stats = generatorStatsFromResources(snapshot.Resources, int32(snapshot.ActiveVUsers))
		telemetry.CollectedAtUnixMs = snapshot.Timestamp.UnixMilli()
		now := time.Now()
		telemetry.PublishedAtUnixMs = now.UnixMilli()
		telemetry.SampleAgeMs = float64(now.Sub(snapshot.Timestamp).Microseconds()) / 1000.0
		if snapshot.Telemetry != nil {
			telemetry.DroppedResults = snapshot.Telemetry.DroppedResults
		}
	}
	_ = s.PublishMetrics(testID, groupName, stats, transactions, telemetry)
}

func (s *Service) finishExecution(testID string, groupName string, err error) {
	s.mu.Lock()
	assignment, ok := s.assignments[testID]
	if !ok {
		s.mu.Unlock()
		return
	}
	if execs, ok := s.executions[testID]; ok {
		delete(execs, groupName)
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
	hasRemainingExecutions := len(s.executions[testID]) > 0
	if err != nil {
		delete(s.assignments, testID)
		s.stopCompletionTimerLocked(testID)
		s.mu.Unlock()
		finalError := err.Error()
		if writer := s.takeArchiveWriter(testID); writer != nil {
			if record, finalizeErr := writer.Finalize("FAILED", time.Now(), finalError); finalizeErr == nil && record != nil {
				s.storeArchiveRecord(record)
			}
		}
		s.broadcastMetrics(s.metricsBatchForState(testID, 0, false, assignment.groups, "FAILED", finalError))
		return
	}
	if hasRemainingExecutions {
		s.assignments[testID] = assignment
		s.mu.Unlock()
		return
	}
	delete(s.assignments, testID)
	s.stopCompletionTimerLocked(testID)
	s.mu.Unlock()
	if writer := s.takeArchiveWriter(testID); writer != nil {
		if record, err := writer.Finalize("COMPLETE", time.Now(), ""); err == nil && record != nil {
			s.storeArchiveRecord(record)
		}
	}
	s.broadcastMetrics(s.metricsBatchForState(testID, 0, false, assignment.groups, "COMPLETE", ""))
}

func (s *Service) takeArchiveWriter(testID string) *archiveRecorder {
	s.mu.Lock()
	defer s.mu.Unlock()
	writer := s.archiveWriters[testID]
	delete(s.archiveWriters, testID)
	return writer
}

func (s *Service) storeArchiveRecord(record *storage.RunArchiveRecord) {
	if record == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.archiveRecords[record.TestID] = record
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
