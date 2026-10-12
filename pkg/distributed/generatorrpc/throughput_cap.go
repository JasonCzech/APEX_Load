package generatorrpc

import (
	"context"
	"math"
	"strings"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/engine"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// FeatureThroughputCapV1 accepts a goal group's throughput cap on its work
// assignment and moves it with SetThroughputCap. A goal or traffic-profile
// run caps each group at its TPS target so the controller's VUser scaling can
// add capacity without overshooting; a peer without the feature runs its
// groups uncapped.
const FeatureThroughputCapV1 = "throughput_cap_v1"

// ThroughputCap moves a running group's throughput cap on one generator. TPS
// is this generator's share of the group's target.
type ThroughputCap struct {
	TestID              string
	ExecutionGeneration uint64
	RunSupervisionToken []byte
	GroupName           string
	Enabled             bool
	TPS                 float64
}

// SetThroughputCap moves the throughput cap of a staged or running group.
func (service *Service) SetThroughputCap(ctx context.Context, request *pb.ThroughputCapRequest) (*pb.ThroughputCapAck, error) {
	if request.GetTestId() == "" {
		return nil, status.Error(codes.InvalidArgument, "test_id is required")
	}
	groupName := request.GetGroupName()
	if strings.TrimSpace(groupName) == "" {
		return nil, status.Error(codes.InvalidArgument, "group_name is required")
	}
	if err := validateGeneratorFieldBytes(groupName, "group_name", maxGeneratorGroupNameBytes); err != nil {
		return nil, err
	}
	if tps := request.GetTps(); request.GetEnabled() && (tps < 0 || math.IsNaN(tps) || math.IsInf(tps, 0)) {
		return nil, status.Error(codes.InvalidArgument, "tps must be a finite, non-negative rate")
	}
	if err := service.validateRunMutationToken(request.GetTestId(), request.GetExecutionGeneration(), request.GetRunSupervisionToken()); err != nil {
		return nil, err
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, status.FromContextError(err).Err()
		}
	}
	// The execution's fields are written under service.mu (a source-plan
	// owner attaches its engine at commit), so read them under it.
	service.mu.RLock()
	execution := service.executions[request.GetTestId()][groupName]
	var groupEngine *engine.MultiGroupEngine
	generation := uint64(0)
	if execution != nil {
		groupEngine, generation = execution.engine, execution.executionGeneration
	}
	service.mu.RUnlock()
	if execution == nil {
		return nil, status.Error(codes.NotFound, "group is not staged on this generator")
	}
	if generation != request.GetExecutionGeneration() {
		return nil, status.Error(codes.FailedPrecondition, "execution generation does not match the staged group")
	}
	if groupEngine == nil {
		return nil, status.Error(codes.FailedPrecondition, "group has no engine to cap")
	}
	if err := groupEngine.SetGroupThroughputCap(groupName, request.GetTps(), request.GetEnabled()); err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "set throughput cap: %v", err)
	}
	return &pb.ThroughputCapAck{Accepted: true}, nil
}

// SetThroughputCap moves a running group's throughput cap on the generator.
func (c *Client) SetThroughputCap(ctx context.Context, request ThroughputCap) error {
	ack, err := c.rpc.SetThroughputCap(ctx, &pb.ThroughputCapRequest{
		TestId:              request.TestID,
		ExecutionGeneration: request.ExecutionGeneration,
		RunSupervisionToken: append([]byte(nil), request.RunSupervisionToken...),
		GroupName:           request.GroupName,
		Enabled:             request.Enabled,
		Tps:                 request.TPS,
	})
	if err != nil {
		return err
	}
	if !ack.GetAccepted() {
		return status.Error(codes.FailedPrecondition, "generator did not accept the throughput cap")
	}
	return nil
}
