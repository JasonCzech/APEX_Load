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
	testID     string
	vuserCount int32
	paused     bool
}

type Service struct {
	pb.UnimplementedGeneratorControlServer

	mu          sync.RWMutex
	generatorID string
	maxVUsers   int32
	scripts     map[string]cachedScript
	assignments map[string]assignedTest
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

	current := s.assignments[assignment.GetTestId()]
	current.testID = assignment.GetTestId()
	current.vuserCount += assignment.GetVuserCount()
	s.assignments[assignment.GetTestId()] = current
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
	if request.GetTargetVusers() < 0 {
		return nil, status.Error(codes.InvalidArgument, "target_vusers must be non-negative")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	assignment, ok := s.assignments[request.GetTestId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "test assignment not found")
	}

	projected := s.activeVUsersLocked() - assignment.vuserCount + request.GetTargetVusers()
	if projected > s.maxVUsers {
		return nil, status.Error(codes.ResourceExhausted, "insufficient generator capacity")
	}

	assignment.vuserCount = request.GetTargetVusers()
	s.assignments[request.GetTestId()] = assignment
	batch := s.metricsBatchLocked(request.GetTestId())

	go s.broadcastMetrics(batch)
	return &pb.AdjustmentAck{
		Accepted:      true,
		CurrentVusers: assignment.vuserCount,
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
	}
	batch := s.metricsBatchForState(request.GetTestId(), 0, false)

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
		return s.metricsBatchForState(testID, 0, false)
	}
	return s.metricsBatchForState(testID, assignment.vuserCount, assignment.paused)
}

func (s *Service) metricsBatchForState(testID string, vuserCount int32, paused bool) *pb.MetricsBatch {
	activeVUsers := vuserCount
	if paused {
		activeVUsers = 0
	}
	return &pb.MetricsBatch{
		TestId:          testID,
		GeneratorId:     s.generatorID,
		TimestampUnixMs: time.Now().UnixMilli(),
		Stats: &pb.GeneratorStats{
			ActiveVusers: activeVUsers,
		},
	}
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}