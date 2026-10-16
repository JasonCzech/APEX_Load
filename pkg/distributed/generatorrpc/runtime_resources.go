package generatorrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	"google.golang.org/protobuf/proto"
)

const FeatureRuntimeResourceReservationsV1 = "eventhubs_processor_reservations_v1"

func RuntimeResourceDemandToProto(d protocol.RuntimeResourceDemand) *pb.RuntimeResourceDemand {
	return &pb.RuntimeResourceDemand{Processors: d.Processors, Workers: d.Workers, MaxPartitionsPerProcessor: d.MaxPartitionsPerProcessor}
}
func RuntimeResourceDemandFromProto(d *pb.RuntimeResourceDemand) protocol.RuntimeResourceDemand {
	return protocol.RuntimeResourceDemand{Processors: d.GetProcessors(), Workers: d.GetWorkers(), MaxPartitionsPerProcessor: d.GetMaxPartitionsPerProcessor()}
}
func CloneRuntimeResourceClaim(c *pb.RuntimeResourceClaim) *pb.RuntimeResourceClaim {
	if c == nil {
		return nil
	}
	return proto.Clone(c).(*pb.RuntimeResourceClaim)
}
func CloneRuntimeResourceAdmission(a *pb.RuntimeResourceAdmission) *pb.RuntimeResourceAdmission {
	if a == nil {
		return nil
	}
	return proto.Clone(a).(*pb.RuntimeResourceAdmission)
}
func CloneRuntimeResourceClaims(values map[string]*pb.RuntimeResourceClaim) map[string]*pb.RuntimeResourceClaim {
	if values == nil {
		return nil
	}
	out := make(map[string]*pb.RuntimeResourceClaim, len(values))
	for name, claim := range values {
		out[name] = CloneRuntimeResourceClaim(claim)
	}
	return out
}
func CloneRuntimeResourceAdmissions(values map[string]*pb.RuntimeResourceAdmission) map[string]*pb.RuntimeResourceAdmission {
	if values == nil {
		return nil
	}
	out := make(map[string]*pb.RuntimeResourceAdmission, len(values))
	for name, admission := range values {
		out[name] = CloneRuntimeResourceAdmission(admission)
	}
	return out
}

// ValidRuntimeResourceClaim verifies a nonempty versioned resource intent.
// Canonical zero demand is represented by nil and is not a positive claim.
func ValidRuntimeResourceClaim(claim *pb.RuntimeResourceClaim) bool {
	return claim != nil && validateRuntimeResourceClaim(claim, 0) == nil
}

// ValidRuntimeResourceAdmission verifies the exact negotiated intent, not a
// generator's ability to run an arbitrary claim or a future target inventory.
func ValidRuntimeResourceAdmission(claim *pb.RuntimeResourceClaim, a *pb.RuntimeResourceAdmission) bool {
	return ValidRuntimeResourceClaim(claim) && a != nil && a.Current && proto.Equal(claim, a.Claim) && proto.Equal(claim.Requested, a.Admitted)
}

// NewRuntimeResourceClaim is the single controller/generator bridge to the
// engine's pure phase and concurrency planner. It performs no target calls.
func NewRuntimeResourceClaim(script *dsl.Script, initStrategy string, peakVUsers int) (*pb.RuntimeResourceClaim, error) {
	claim, _, err := buildRuntimeResourceClaim(script, initStrategy, peakVUsers)
	return claim, err
}
func buildRuntimeResourceClaim(script *dsl.Script, initStrategy string, peakVUsers int) (*pb.RuntimeResourceClaim, bool, error) {
	if peakVUsers < 0 || int64(peakVUsers) > maxGeneratorVUsers {
		return nil, false, fmt.Errorf("invalid runtime resource peak")
	}
	plan, err := engine.NewRuntimeResourcePlan(script, initStrategy)
	if err != nil {
		return nil, false, err
	}
	if !plan.RequiresReservation() {
		return nil, false, nil
	}
	demand, err := plan.Demand(peakVUsers)
	if err != nil {
		return nil, true, err
	}
	if demand.IsZero() {
		return nil, true, nil
	}
	return &pb.RuntimeResourceClaim{Contract: FeatureRuntimeResourceReservationsV1, Protocol: "eventhubs", PeakVusers: int32(peakVUsers), Requested: RuntimeResourceDemandToProto(demand)}, true, nil
}
func validateRuntimeResourceClaim(claim *pb.RuntimeResourceClaim, initial int32) error {
	if claim == nil {
		return nil
	}
	if claim.Contract != FeatureRuntimeResourceReservationsV1 || claim.Protocol != "eventhubs" || claim.PeakVusers < initial || claim.PeakVusers < 0 || int64(claim.PeakVusers) > maxGeneratorVUsers || claim.Requested == nil {
		return fmt.Errorf("invalid runtime resource contract or immutable peak")
	}
	d := claim.Requested
	if d.Processors <= 0 || d.Processors > maxGeneratorVUsers || d.MaxPartitionsPerProcessor < 1 || d.MaxPartitionsPerProcessor > 32 || d.Workers != d.Processors*d.MaxPartitionsPerProcessor {
		return fmt.Errorf("invalid Event Hubs runtime resource demand")
	}
	return nil
}

// reserveAssignmentRuntimeResourcesLocked binds the verified cached source to
// the requested peak. Reserve is a bounded local operation under the same lock
// as assignment admission; no protocol initialization or target discovery runs.
type assignmentRuntimeResources struct {
	reservation protocol.RuntimeResourceReservation
	managed     bool
	peak        int32
}

func reserveAssignmentRuntimeResourcesLocked(content []byte, initStrategy string, request *pb.WorkAssignment) (assignmentRuntimeResources, error) {
	var script dsl.Script
	if err := json.Unmarshal(content, &script); err != nil {
		if request.GetRuntimeResourceClaim() != nil {
			return assignmentRuntimeResources{}, fmt.Errorf("runtime resource source is invalid")
		}
		return assignmentRuntimeResources{}, nil
	}
	peak := int(request.GetVuserCount())
	if request.GetRuntimeResourceClaim() != nil {
		peak = int(request.RuntimeResourceClaim.PeakVusers)
	}
	expected, managed, err := buildRuntimeResourceClaim(&script, initStrategy, peak)
	if err != nil {
		return assignmentRuntimeResources{}, err
	}
	if !proto.Equal(expected, request.GetRuntimeResourceClaim()) {
		return assignmentRuntimeResources{}, fmt.Errorf("runtime resource claim does not match verified source and peak")
	}
	resources := assignmentRuntimeResources{managed: managed, peak: int32(peak)}
	if expected == nil {
		return resources, nil
	}
	instance, err := protocol.Get(expected.Protocol)
	if err != nil {
		return assignmentRuntimeResources{}, err
	}
	reservation, err := protocol.ReserveRuntimeResources(context.Background(), instance, RuntimeResourceDemandFromProto(expected.Requested))
	if err != nil {
		return assignmentRuntimeResources{}, err
	}
	if reservation == nil || reservation.ResourceProtocol() != expected.Protocol || !reservation.Current() || reservation.Demand() != RuntimeResourceDemandFromProto(expected.Requested) {
		if reservation != nil {
			reservation.Release()
		}
		return assignmentRuntimeResources{}, fmt.Errorf("runtime resource admission does not match demand")
	}
	resources.reservation = reservation
	return resources, nil
}
func runtimeResourceAdmission(claim *pb.RuntimeResourceClaim, r protocol.RuntimeResourceReservation) *pb.RuntimeResourceAdmission {
	if claim == nil {
		return nil
	}
	a := &pb.RuntimeResourceAdmission{Claim: CloneRuntimeResourceClaim(claim)}
	if r != nil {
		a.Admitted = RuntimeResourceDemandToProto(r.Demand())
		a.Current = r.Current()
	}
	return a
}
func (s *Service) runtimeResourceAdmissionsLocked(assignment assignedTest) map[string]*pb.RuntimeResourceAdmission {
	if len(assignment.groupRuntimeResourceClaims) == 0 {
		return nil
	}
	out := make(map[string]*pb.RuntimeResourceAdmission, len(assignment.groupRuntimeResourceClaims))
	for name, claim := range assignment.groupRuntimeResourceClaims {
		out[name] = runtimeResourceAdmission(claim, assignment.groupRuntimeResources[name])
	}
	return out
}
func releaseAssignmentRuntimeResources(assignment assignedTest) {
	for _, r := range assignment.groupRuntimeResources {
		if r != nil {
			r.Release()
		}
	}
}
func RuntimeResourceCapacityToProto(c protocol.RuntimeResourceCapacity) *pb.RuntimeResourceCapacity {
	return &pb.RuntimeResourceCapacity{Available: c.Available, MaxProcessors: c.MaxProcessors, AvailableProcessors: c.AvailableProcessors, ReservedProcessors: c.ReservedProcessors, ActiveProcessors: c.ActiveProcessors, DrainingProcessors: c.DrainingProcessors, MaxWorkers: c.MaxWorkers, AvailableWorkers: c.AvailableWorkers, ReservedWorkers: c.ReservedWorkers, ActiveWorkers: c.ActiveWorkers, DrainingWorkers: c.DrainingWorkers, MaxPartitionsPerProcessor: c.MaxPartitionsPerProcessor}
}
func RuntimeResourceCapacityFromProto(c *pb.RuntimeResourceCapacity) *protocol.RuntimeResourceCapacity {
	if c == nil {
		return nil
	}
	return &protocol.RuntimeResourceCapacity{Available: c.Available, MaxProcessors: c.MaxProcessors, AvailableProcessors: c.AvailableProcessors, ReservedProcessors: c.ReservedProcessors, ActiveProcessors: c.ActiveProcessors, DrainingProcessors: c.DrainingProcessors, MaxWorkers: c.MaxWorkers, AvailableWorkers: c.AvailableWorkers, ReservedWorkers: c.ReservedWorkers, ActiveWorkers: c.ActiveWorkers, DrainingWorkers: c.DrainingWorkers, MaxPartitionsPerProcessor: c.MaxPartitionsPerProcessor}
}

func (c *Client) observeRuntimeResources(pong *pb.HeartbeatPong) {
	if c.connection == nil {
		return
	}
	negotiated := false
	if pong != nil && protocol.RuntimeFeaturesSatisfy(pong.CompatibleFeatures, FeatureRuntimeResourceReservationsV1) {
		for _, runtime := range pong.RuntimeCapacities {
			resources := RuntimeResourceCapacityFromProto(runtime.GetResources())
			if runtime.GetProtocol() == "eventhubs" && runtime.GetAvailable() && runtime.GetResourceContract() == FeatureRuntimeResourceReservationsV1 && resources != nil && resources.Available && resources.Valid() {
				negotiated = true
				break
			}
		}
	}
	c.connection.mu.Lock()
	c.connection.runtimeResources = negotiated
	c.connection.runtimeResourcesObserved = time.Now()
	c.connection.mu.Unlock()
}
func (c *Client) runtimeResourcesNegotiated() bool {
	if c.connection == nil {
		return false
	}
	c.connection.mu.Lock()
	defer c.connection.mu.Unlock()
	return c.connection.runtimeResources && time.Since(c.connection.runtimeResourcesObserved) <= 30*time.Second
}
