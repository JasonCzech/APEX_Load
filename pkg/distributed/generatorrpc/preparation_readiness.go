package generatorrpc

import (
	"context"
	"fmt"
	"strings"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// A separate RPC makes traffic-free revalidation safe with older generators:
// an unsupported peer returns Unimplemented instead of interpreting a commit.
const FeaturePreparationReadinessV1 = "prepare_readiness_v1"

// CheckPreparation checks only staged controller-owned state. It must never
// call runtime preflight, initialize protocols or execute user actions.
func (s *Service) CheckPreparation(ctx context.Context, req *pb.PreparationCheckRequest) (*pb.WorkCommitAck, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	if req == nil || (len(req.GetRunSupervisionToken()) != 0 && len(req.GetRunSupervisionToken()) != RunSupervisionTokenSize) || strings.TrimSpace(req.TestId) == "" || len(req.TestId) > 512 || len(req.ExpectedGroups) == 0 || len(req.ExpectedGroups) > 1024 {
		return nil, status.Error(codes.InvalidArgument, "test identity and 1-1024 expected groups required")
	}
	for name, count := range req.ExpectedGroups {
		if strings.TrimSpace(name) == "" || count < 0 {
			return nil, status.Error(codes.InvalidArgument, "invalid expected group")
		}
		if err := validateGeneratorFieldBytes(name, "group_name", maxGeneratorGroupNameBytes); err != nil {
			return nil, err
		}
	}
	if len(req.ExpectedRuntimeResources) > len(req.ExpectedGroups) {
		return nil, status.Error(codes.InvalidArgument, "unexpected runtime resource groups")
	}
	for name, claim := range req.ExpectedRuntimeResources {
		count, ok := req.ExpectedGroups[name]
		if !ok || claim == nil {
			return nil, status.Error(codes.InvalidArgument, "invalid expected runtime resource group")
		}
		if err := validateRuntimeResourceClaim(claim, count); err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	if s.draining || !s.executionEnabled || s.lifecycleCoordinator.stageTTL <= 0 {
		return nil, status.Error(codes.FailedPrecondition, "generator cannot retain a preparation")
	}
	if err := s.validateMutationGenerationLocked(req.ExecutionGeneration); err != nil {
		return nil, err
	}
	assignment, exists := s.assignments[req.TestId]
	if !exists || assignment.executionGeneration != req.ExecutionGeneration || !assignment.prepareOnly || assignment.committed {
		return nil, status.Error(codes.FailedPrecondition, "prepared assignment no longer available")
	}
	if stopping := s.lifecycleCoordinator.isStoppingLocked(req.TestId); stopping || s.lifecycleCoordinator.pendingStages[req.TestId] != nil || s.lifecycleCoordinator.pendingControls[req.TestId] != nil {
		return nil, status.Error(codes.FailedPrecondition, "prepared assignment is changing")
	}
	// Preparation retains an uncommitted STAGED supervision claim. Reuse the
	// read-only pre-commit validator so identity, expiry and restart fences are
	// checked without arming the claim or crossing the execution boundary.
	if _, err := s.validateRunSupervisionCommitLocked(&pb.WorkCommitRequest{
		TestId: req.TestId, ExecutionGeneration: req.ExecutionGeneration, RunSupervisionToken: req.RunSupervisionToken,
	}, s.nowTime()); err != nil {
		return nil, err
	}
	if assignment.requireTestExecutionLease && !s.testExecutionLeaseCurrentLocked(req.TestId, req.ExecutionGeneration, s.nowTime()) {
		return nil, status.Error(codes.FailedPrecondition, "prepared execution lease expired")
	}
	if assignment.preparationCapacityClaim != nil && !proto.Equal(assignment.preparationCapacityClaim, s.podCapacityClaims[req.TestId]) {
		return nil, status.Error(codes.FailedPrecondition, "prepared peak capacity reservation changed")
	}
	if len(assignment.groups) != len(req.ExpectedGroups) || len(s.executions[req.TestId]) != len(req.ExpectedGroups) {
		return nil, status.Error(codes.FailedPrecondition, "prepared group membership changed")
	}
	if len(req.ExpectedRuntimeResources) != len(assignment.groupRuntimeResourceClaims) {
		return nil, status.Error(codes.FailedPrecondition, "prepared runtime resource membership changed")
	}
	for name, expected := range req.ExpectedRuntimeResources {
		actual := assignment.groupRuntimeResourceClaims[name]
		admission := runtimeResourceAdmission(actual, assignment.groupRuntimeResources[name])
		if !ValidRuntimeResourceAdmission(expected, admission) {
			return nil, status.Error(codes.FailedPrecondition, "prepared runtime resource reservation changed")
		}
	}
	for name, count := range req.ExpectedGroups {
		actual, ok := assignment.groups[name]
		execution := s.executions[req.TestId][name]
		if !ok || actual != count || execution == nil || execution.started.Load() || execution.executionGeneration != req.ExecutionGeneration {
			return nil, status.Error(codes.FailedPrecondition, "prepared group reservation changed")
		}
		if execution.engine == nil || !execution.engine.RuntimeResourceReservationsCurrent() {
			return nil, status.Error(codes.FailedPrecondition, "prepared engine resource reservation changed")
		}
		select {
		case <-execution.done:
			return nil, status.Error(codes.FailedPrecondition, "prepared execution ended")
		default:
		}
	}
	// Refresh only an existing, validated reservation; never resurrect expired
	// stages. The ordinary stage timer still reclaims abandoned preparations.
	if !time.Now().Before(s.lifecycleCoordinator.stageExpiresAt[req.TestId]) {
		return nil, status.Error(codes.FailedPrecondition, "prepared stage deadline expired")
	}
	if req.RenewStaging {
		s.lifecycleCoordinator.scheduleStageExpiryLocked(s, req.TestId)
	}
	return &pb.WorkCommitAck{Accepted: true, RuntimeResourceAdmissions: s.runtimeResourceAdmissionsLocked(assignment)}, nil
}

func (c *Client) CheckPreparation(ctx context.Context, testID string, generation uint64, groups map[string]int32, token []byte) error {
	_, err := c.CheckPreparationResources(ctx, testID, generation, groups, token, nil)
	return err
}

// CheckPreparationResources retains the old state-only RPC and verifies every
// expected resource admission. An old peer's empty acknowledgement fails closed.
func (c *Client) CheckPreparationResources(ctx context.Context, testID string, generation uint64, groups map[string]int32, token []byte, claims map[string]*pb.RuntimeResourceClaim) (map[string]*pb.RuntimeResourceAdmission, error) {
	request := &pb.PreparationCheckRequest{TestId: testID, ExecutionGeneration: generation, ExpectedGroups: cloneStringInt32Map(groups), RunSupervisionToken: append([]byte(nil), token...), RenewStaging: true, ExpectedRuntimeResources: CloneRuntimeResourceClaims(claims)}
	ack, err := c.rpc.CheckPreparation(ctx, request)
	if err != nil {
		return nil, err
	}
	if !ack.GetAccepted() {
		return nil, fmt.Errorf("generator rejected preparation readiness: %s", ack.GetErrorMessage())
	}
	admissions := ack.GetRuntimeResourceAdmissions()
	if len(admissions) != len(claims) {
		return nil, fmt.Errorf("runtime resource readiness membership mismatch")
	}
	for name, claim := range claims {
		if !ValidRuntimeResourceAdmission(claim, admissions[name]) {
			return nil, fmt.Errorf("runtime resource readiness admission mismatch")
		}
	}
	return CloneRuntimeResourceAdmissions(admissions), nil
}
