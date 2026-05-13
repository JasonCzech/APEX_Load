package generatorrpc

import (
	"context"
	"io"
	"sync"
	"time"

	pb "github.com/jasonczech/stormrunner/api/proto"
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
	stats           GeneratorStats
	transactions    []TransactionMetric
}

type Service struct {
	pb.UnimplementedGeneratorControlServer

	mu          sync.RWMutex
	generatorID string
	maxVUsers   int32
	scripts     map[string]cachedScript
	assignments map[string]assignedTest
	completionTimers map[string]*time.Timer
	subscribers map[int]chan *pb.MetricsBatch
	nextSubID   int
}

func NewService(generatorID string, maxVUsers int) *Service {
	if generatorID == "" {
		generatorID = "stormrunner-generator"
	}
	if maxVUsers <= 0 {
		maxVUsers = 10000
	}

	return &Service{
		generatorID: generatorID,
		maxVUsers:   int32(maxVUsers),
		scripts:     make(map[string]cachedScript),
		assignments: make(map[string]assignedTest),
		completionTimers: make(map[string]*time.Timer),
		subscribers: make(map[int]chan *pb.MetricsBatch),
	}
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
	if current.durationSeconds == 0 && assignment.GetTargetDurationSeconds() > 0 {
		current.durationSeconds = assignment.GetTargetDurationSeconds()
	}
	current.groups[assignment.GetGroupName()] += assignment.GetVuserCount()
	current.vuserCount += assignment.GetVuserCount()
	s.assignments[assignment.GetTestId()] = current
	if !existed && current.durationSeconds > 0 {
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

	assignment.groups[request.GetGroupName()] = request.GetTargetVusers()
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
	assignment.durationSeconds = request.GetTargetDurationSeconds()
	s.assignments[request.GetTestId()] = assignment
	s.scheduleCompletionLocked(assignment.testID, time.Duration(assignment.durationSeconds)*time.Second)

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
	defer s.mu.Unlock()

	assignment, ok := s.assignments[request.GetTestId()]
	if ok {
		delete(s.assignments, request.GetTestId())
		s.stopCompletionTimerLocked(request.GetTestId())
	}
	batch := s.metricsBatchForState(request.GetTestId(), 0, false, assignment.groups, "COMPLETE", "")

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
	s.stopCompletionTimerLocked(testID)
	s.mu.Unlock()
	if !ok {
		return
	}
	s.broadcastMetrics(s.metricsBatchForState(testID, 0, false, assignment.groups, "FAILED", message))
}

func (s *Service) PublishMetrics(testID string, stats GeneratorStats, transactions []TransactionMetric) error {
	if testID == "" {
		return status.Error(codes.InvalidArgument, "test_id is required")
	}

	s.mu.Lock()
	assignment, ok := s.assignments[testID]
	if !ok {
		s.mu.Unlock()
		return status.Error(codes.NotFound, "test assignment not found")
	}
	assignment.stats = stats
	assignment.transactions = append([]TransactionMetric(nil), transactions...)
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
	stats := assignment.stats
	stats.ActiveVUsers = assignment.vuserCount
	return s.metricsBatchForMetrics(testID, stats, assignment.transactions, assignment.paused, assignment.groups, lifecycleState, "")
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
	s.stopCompletionTimerLocked(testID)
	s.mu.Unlock()
	if !ok {
		return
	}
	s.broadcastMetrics(s.metricsBatchForState(testID, 0, false, assignment.groups, "COMPLETE", ""))
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}