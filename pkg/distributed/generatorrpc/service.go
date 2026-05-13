package generatorrpc

import (
	"context"
	"encoding/json"
	"io"
	"runtime"
	"sync"
	"time"

	pb "github.com/jasonczech/stormrunner/api/proto"
	"github.com/jasonczech/stormrunner/pkg/dsl"
	"github.com/jasonczech/stormrunner/pkg/engine"
	"github.com/jasonczech/stormrunner/pkg/protocol"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type cachedScript struct {
	content []byte
	hash    string
}

type assignedTest struct {
	testID          string
	vuserCount      int32
	paused          bool
	durationSeconds int64
	groups          map[string]int32
	activeGroups     map[string]int32
	groupStats       map[string]GeneratorStats
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
		Name:          result.ActionName,
		DurationMs:    float64(result.Duration) / float64(time.Millisecond),
		StatusCode:    int32(result.StatusCode),
		Success:       result.Success,
		ErrorMessage:  result.ErrorMessage,
		BytesSent:     result.BytesSent,
		BytesReceived: result.BytesReceived,
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

	mu          sync.RWMutex
	generatorID string
	maxVUsers   int32
	scripts     map[string]cachedScript
	assignments map[string]assignedTest
	executions  map[string]map[string]*groupExecution
	executionEnabled bool
	publishInterval  time.Duration
	completionTimers map[string]*time.Timer
	subscribers map[int]chan *pb.MetricsBatch
	nextSubID   int
}

func WithExecution() ServiceOption {
	return func(s *Service) {
		s.executionEnabled = true
	}
}

func NewService(generatorID string, maxVUsers int, opts ...ServiceOption) *Service {
	if generatorID == "" {
		generatorID = "stormrunner-generator"
	}
	if maxVUsers <= 0 {
		maxVUsers = 10000
	}

	service := &Service{
		generatorID: generatorID,
		maxVUsers:   int32(maxVUsers),
		scripts:     make(map[string]cachedScript),
		assignments: make(map[string]assignedTest),
		executions:  make(map[string]map[string]*groupExecution),
		publishInterval: time.Second,
		completionTimers: make(map[string]*time.Timer),
		subscribers: make(map[int]chan *pb.MetricsBatch),
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

	go s.broadcastMetrics(batch)
	return &pb.StopAck{
		Acknowledged: ok,
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

func (s *Service) Heartbeat(stream pb.GeneratorControl_HeartbeatServer) error {
	for {
		ping, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		if err := stream.Send(&pb.HeartbeatPong{
			TimestampUnixMs: maxInt64(time.Now().UnixMilli(), ping.GetTimestampUnixMs()),
			ActiveTestIds:   s.activeTestIDs(),
		}); err != nil {
			return err
		}
	}
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

func (s *Service) PublishMetrics(testID string, groupName string, stats GeneratorStats, transactions []TransactionMetric) error {
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
	if assignment.groupTransactions == nil {
		assignment.groupTransactions = make(map[string][]TransactionMetric)
	}
	assignment.activeGroups[groupName] = stats.ActiveVUsers
	assignment.groupStats[groupName] = stats
	assignment.groupTransactions[groupName] = append([]TransactionMetric(nil), transactions...)
	s.assignments[testID] = assignment
	batch := s.metricsBatchLocked(testID)
	s.mu.Unlock()

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
	for _, groupStats := range assignment.groupStats {
		stats.CPUPercent += groupStats.CPUPercent
		stats.MemoryBytes += groupStats.MemoryBytes
		stats.GoroutineCount += groupStats.GoroutineCount
	}
	transactions := make([]TransactionMetric, 0)
	for _, groupTransactions := range assignment.groupTransactions {
		transactions = append(transactions, groupTransactions...)
	}
	for _, activeVUsers := range groupAssignments {
		stats.ActiveVUsers += activeVUsers
	}
	if stats.ActiveVUsers == 0 && len(groupAssignments) == 0 {
		stats.ActiveVUsers = assignment.vuserCount
	}
	return s.metricsBatchForMetrics(testID, stats, transactions, assignment.paused, groupAssignments, lifecycleState, "")
}

func (s *Service) metricsBatchForState(testID string, vuserCount int32, paused bool, groupAssignments map[string]int32, lifecycleState string, lifecycleError string) *pb.MetricsBatch {
	return s.metricsBatchForMetrics(testID, GeneratorStats{ActiveVUsers: vuserCount}, nil, paused, groupAssignments, lifecycleState, lifecycleError)
}

func (s *Service) metricsBatchForMetrics(testID string, stats GeneratorStats, transactions []TransactionMetric, paused bool, groupAssignments map[string]int32, lifecycleState string, lifecycleError string) *pb.MetricsBatch {
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
		protoTransactions = append(protoTransactions, &pb.TransactionMetric{
			Name:          tx.Name,
			DurationMs:    tx.DurationMs,
			StatusCode:    tx.StatusCode,
			Success:       tx.Success,
			ErrorMessage:  tx.ErrorMessage,
			BytesSent:     tx.BytesSent,
			BytesReceived: tx.BytesReceived,
		})
	}
	return &pb.MetricsBatch{
		TestId:          testID,
		GeneratorId:     s.generatorID,
		TimestampUnixMs: time.Now().UnixMilli(),
		Transactions:    protoTransactions,
		Stats:           toProtoStats(stats),
		Groups:         groups,
		LifecycleState: lifecycleState,
		LifecycleError: lifecycleError,
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
	mge, err := engine.NewMultiGroup(engine.MultiGroupConfig{
		Groups: []engine.GroupConfig{{
			Name:         assignment.GetGroupName(),
			Script:       &script,
			VUsers:       int(assignment.GetVuserCount()),
			RampUp:       time.Duration(assignment.GetRampUpSeconds()) * time.Second,
			InitStrategy: "at_runtime",
			ResultSink:   results,
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
	stats := GeneratorStats{GoroutineCount: int32(runtime.NumGoroutine())}
	if ok {
		stats.ActiveVUsers = int32(snapshot.ActiveVUsers)
		if snapshot.Resources != nil {
			stats.CPUPercent = snapshot.Resources.CPUProcessPct
			stats.MemoryBytes = int64(snapshot.Resources.MemProcessMb * 1024 * 1024)
		}
	}
	_ = s.PublishMetrics(testID, groupName, stats, transactions)
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
	if assignment.groupTransactions != nil {
		delete(assignment.groupTransactions, groupName)
	}
	hasRemainingExecutions := len(s.executions[testID]) > 0
	if err != nil {
		delete(s.assignments, testID)
		s.stopCompletionTimerLocked(testID)
		s.mu.Unlock()
		s.broadcastMetrics(s.metricsBatchForState(testID, 0, false, assignment.groups, "FAILED", err.Error()))
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
	s.broadcastMetrics(s.metricsBatchForState(testID, 0, false, assignment.groups, "COMPLETE", ""))
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}