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
}

type Service struct {
	pb.UnimplementedGeneratorControlServer

	mu          sync.RWMutex
	generatorID string
	maxVUsers   int32
	scripts     map[string]cachedScript
	assignments map[string]assignedTest
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

	return &pb.AssignmentAck{
		Accepted:          true,
		AvailableCapacity: s.availableCapacityLocked(),
	}, nil
}

func (s *Service) AdjustVUsers(context.Context, *pb.VUserAdjustment) (*pb.AdjustmentAck, error) {
	return nil, status.Error(codes.Unimplemented, "adjust_vusers is not implemented yet")
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

	return &pb.StopAck{
		Acknowledged: ok,
		PendingVusers: assignment.vuserCount,
	}, nil
}

func (s *Service) StreamMetrics(pb.GeneratorControl_StreamMetricsServer) error {
	return status.Error(codes.Unimplemented, "stream_metrics is not implemented yet")
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

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}