package generatorrpc

import (
	"context"
	"fmt"
	"math"
	"sort"

	"github.com/google/uuid"
	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/protocol"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const FeaturePodCapacityV1 = "pod_capacity_v1"

func newPodCapacityIncarnation() string { return uuid.NewString() }

func CapacityVectorToProto(v protocol.CapacityVector) *pb.CapacityVector {
	return &pb.CapacityVector{CpuCores: v.CPUCores, MemoryMb: v.MemoryMB, NetworkMbps: v.NetworkMbps, Vusers: v.VUsers, Protocols: v.Scale(1).Protocols}
}

func CapacityVectorFromProto(v *pb.CapacityVector) protocol.CapacityVector {
	return protocol.CapacityVector{CPUCores: v.GetCpuCores(), MemoryMB: v.GetMemoryMb(), NetworkMbps: v.GetNetworkMbps(), VUsers: v.GetVusers(), Protocols: v.GetProtocols()}.Scale(1)
}

func PodCapacityFromProto(p *pb.PodCapacitySnapshot) *protocol.CapacitySnapshot {
	if p == nil {
		return nil
	}
	s := protocol.NewCapacitySnapshot(CapacityVectorFromProto(p.Schedulable), CapacityVectorFromProto(p.Reserved), p.HeadroomPct)
	s.Incarnation = p.Incarnation
	s.State = p.State
	s.Dedicated = p.Dedicated
	s.ReportedTestIDs = append([]string(nil), p.ReservedTestIds...)
	return &s
}

func (c *Client) ReservePodCapacity(ctx context.Context, claim *pb.PodCapacityClaim) (*pb.PodCapacityAck, error) {
	return c.rpc.ReservePodCapacity(ctx, claim)
}

func (s *Service) podCapacitySnapshotLocked() *pb.PodCapacitySnapshot {
	p := &pb.PodCapacitySnapshot{Incarnation: s.podCapacityIncarnation, State: "unconfigured"}
	used := protocol.CapacityVector{}
	for id, claim := range s.podCapacityClaims {
		p.Schedulable = claim.Schedulable
		p.HeadroomPct = claim.HeadroomPct
		p.State = "ready"
		used = used.Add(CapacityVectorFromProto(claim.Demand))
		p.Dedicated = p.Dedicated || claim.Dedicated
		p.ReservedTestIds = append(p.ReservedTestIds, id)
	}
	for id := range s.assignments {
		if s.podCapacityClaims[id] == nil {
			p.State = "unreconciled"
		}
	}
	p.Reserved = CapacityVectorToProto(used)
	sort.Strings(p.ReservedTestIds)
	return p
}

// ReservePodCapacity is independent of active VUsers: ramp changes never return
// peak capacity to the shared pool. Release requires all execution to be drained.
func (s *Service) ReservePodCapacity(ctx context.Context, claim *pb.PodCapacityClaim) (*pb.PodCapacityAck, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	if claim == nil || claim.TestId == "" || len(claim.TestId) > 512 {
		return nil, status.Error(codes.InvalidArgument, "test_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	if err := s.validateMutationGenerationLocked(claim.ExecutionGeneration); err != nil {
		return nil, err
	}
	reject := func(err error) (*pb.PodCapacityAck, error) {
		return &pb.PodCapacityAck{ErrorMessage: err.Error(), Snapshot: s.podCapacitySnapshotLocked()}, nil
	}
	if claim.Incarnation != s.podCapacityIncarnation {
		return reject(fmt.Errorf("generator incarnation changed"))
	}
	current := s.podCapacityClaims[claim.TestId]
	if claim.ReservationId == "" || (current != nil && current.ReservationId != claim.ReservationId) {
		return reject(fmt.Errorf("reservation attempt changed or missing"))
	}
	if claim.Release {
		if current != nil && current.ExecutionGeneration > claim.ExecutionGeneration {
			return reject(fmt.Errorf("reservation generation changed"))
		}
		if _, ok := s.assignments[claim.TestId]; ok {
			return reject(fmt.Errorf("test still assigned"))
		}
		if len(s.executions[claim.TestId]) > 0 || s.lifecycleCoordinator.pendingStages[claim.TestId] != nil {
			return reject(fmt.Errorf("test still executing or staging"))
		}
		if _, ok := s.stoppingTests[claim.TestId]; ok {
			return reject(fmt.Errorf("test is still draining"))
		}
		delete(s.podCapacityClaims, claim.TestId)
		return &pb.PodCapacityAck{Accepted: true, Snapshot: s.podCapacitySnapshotLocked()}, nil
	}
	if s.draining {
		return reject(fmt.Errorf("generator is draining"))
	}
	if s.podResources.CPULimitCores <= 0 || math.IsNaN(s.podResources.CPULimitCores) || math.IsInf(s.podResources.CPULimitCores, 0) || s.podResources.MemoryLimitBytes <= 0 {
		return reject(fmt.Errorf("generator runtime CPU and memory limits are required"))
	}
	if claim.ModelVersion != protocol.CapacityReservationModel || math.IsNaN(claim.HeadroomPct) || math.IsInf(claim.HeadroomPct, 0) || claim.HeadroomPct < 0 || claim.HeadroomPct >= 100 {
		return reject(fmt.Errorf("invalid capacity model or headroom"))
	}
	budget, demand := CapacityVectorFromProto(claim.Schedulable), CapacityVectorFromProto(claim.Demand)
	if err := budget.Fits(demand); err != nil {
		return reject(err)
	}
	factor := 1 - claim.HeadroomPct/100
	if budget.VUsers > float64(s.maxVUsers)*factor+1e-9 || (s.podResources.CPULimitCores > 0 && budget.CPUCores > s.podResources.CPULimitCores*factor+1e-9) || (s.podResources.MemoryLimitBytes > 0 && budget.MemoryMB > float64(s.podResources.MemoryLimitBytes)/(1024*1024)*factor+1e-9) {
		return reject(fmt.Errorf("budget exceeds generator limits after headroom"))
	}
	if current != nil {
		if proto.Equal(current, claim) {
			return &pb.PodCapacityAck{Accepted: true, Snapshot: s.podCapacitySnapshotLocked()}, nil
		}
		if claim.Revision != current.Revision+1 || claim.Revision == 0 || claim.ExecutionGeneration != current.ExecutionGeneration || claim.Dedicated != current.Dedicated || !proto.Equal(claim.Schedulable, current.Schedulable) || claim.HeadroomPct != current.HeadroomPct {
			return reject(fmt.Errorf("reservation revision or frozen model changed"))
		}
		if s.lifecycleCoordinator.pendingStages[claim.TestId] != nil || s.lifecycleCoordinator.pendingControls[claim.TestId] != nil {
			return reject(fmt.Errorf("reservation cannot change while execution control is pending"))
		}
		if assignment, ok := s.assignments[claim.TestId]; ok {
			for name, n := range assignment.groups {
				peak, found := claim.GroupPeaks[name]
				if !found || n > peak {
					return reject(fmt.Errorf("live VUsers exceed proposed reservation"))
				}
			}
		}
	}
	if current == nil && len(s.podCapacityClaims) >= s.maxAssignedTests {
		return reject(fmt.Errorf("reservation object limit reached"))
	}
	for id := range s.assignments {
		if s.podCapacityClaims[id] == nil {
			return reject(fmt.Errorf("legacy assignments require reconciliation"))
		}
	}
	for id := range s.lifecycleCoordinator.pendingStages {
		if s.podCapacityClaims[id] == nil {
			return reject(fmt.Errorf("legacy assignment is staging"))
		}
	}
	for id := range s.stoppingTests {
		if s.podCapacityClaims[id] == nil {
			return reject(fmt.Errorf("legacy assignment is draining"))
		}
	}
	var totalPeaks int64
	for name, n := range claim.GroupPeaks {
		if name == "" || n < 0 {
			return reject(fmt.Errorf("invalid group peak"))
		}
		totalPeaks += int64(n)
	}
	if len(claim.GroupPeaks) == 0 || float64(totalPeaks) > demand.VUsers+1e-9 {
		return reject(fmt.Errorf("group peaks exceed reserved VUsers"))
	}
	used := protocol.CapacityVector{}
	for id, other := range s.podCapacityClaims {
		if id == claim.TestId {
			continue
		}
		if claim.Dedicated || other.Dedicated {
			return reject(fmt.Errorf("generator is dedicated"))
		}
		if !proto.Equal(other.Schedulable, claim.Schedulable) || other.HeadroomPct != claim.HeadroomPct {
			return reject(fmt.Errorf("capacity configuration changed while reserved"))
		}
		used = used.Add(CapacityVectorFromProto(other.Demand))
	}
	if claim.Dedicated && !proto.Equal(claim.Demand, claim.Schedulable) {
		return reject(fmt.Errorf("dedicated reservation must claim the full budget"))
	}
	if err := budget.Fits(used.Add(demand)); err != nil {
		return reject(err)
	}
	if s.podCapacityClaims == nil {
		s.podCapacityClaims = make(map[string]*pb.PodCapacityClaim)
	}
	s.podCapacityClaims[claim.TestId] = proto.Clone(claim).(*pb.PodCapacityClaim)
	return &pb.PodCapacityAck{Accepted: true, Snapshot: s.podCapacitySnapshotLocked()}, nil
}

func (s *Service) validatePodCapacityAssignmentLocked(testID, group string, n int32) error {
	if len(s.podCapacityClaims) == 0 {
		return nil
	}
	claim := s.podCapacityClaims[testID]
	if claim == nil {
		return fmt.Errorf("peak capacity reservation required")
	}
	peak, ok := claim.GroupPeaks[group]
	if !ok || n > peak {
		return fmt.Errorf("group exceeds reserved peak capacity")
	}
	return nil
}
