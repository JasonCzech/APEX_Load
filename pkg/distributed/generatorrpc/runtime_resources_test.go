package generatorrpc

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
)

func runtimeResourceScript() *dsl.Script {
	s := &dsl.Script{Name: "processors", Protocol: "eventhubs", Actions: []dsl.Action{{Name: "receive", Type: "eventhubs_consume", Target: "events"}}}
	s.Config.EventHubs.ConsumerMode = "processor"
	s.Config.EventHubs.ProcessorMaxPartitions = 8
	return s
}
func testResourceClaim() *pb.RuntimeResourceClaim {
	return &pb.RuntimeResourceClaim{Contract: FeatureRuntimeResourceReservationsV1, Protocol: "eventhubs", PeakVusers: 2, Requested: &pb.RuntimeResourceDemand{Processors: 2, Workers: 16, MaxPartitionsPerProcessor: 8}}
}
func testResourcePong() *pb.HeartbeatPong {
	return &pb.HeartbeatPong{CompatibleFeatures: []string{FeatureRuntimeResourceReservationsV1}, RuntimeCapacities: []*pb.ProtocolRuntimeCapacity{{Protocol: "eventhubs", Available: true, ResourceContract: FeatureRuntimeResourceReservationsV1, Resources: &pb.RuntimeResourceCapacity{Available: true, MaxProcessors: 4, AvailableProcessors: 4, MaxWorkers: 32, AvailableWorkers: 32, MaxPartitionsPerProcessor: 32}}}}
}

func TestRuntimeResourceClaimUsesImmutablePeakAndZeroPhaseRules(t *testing.T) {
	s := runtimeResourceScript()
	claim, err := NewRuntimeResourceClaim(s, "at_runtime", 2)
	if err != nil || !proto.Equal(claim, testResourceClaim()) {
		t.Fatalf("peak demand: %v %v", claim, err)
	}
	zero, err := NewRuntimeResourceClaim(s, "at_runtime", 0)
	if err != nil || zero != nil {
		t.Fatalf("zero demand created registry claim: %v %v", zero, err)
	}
	s.Init = append([]dsl.Action(nil), s.Actions...)
	s.Actions = nil
	once, err := NewRuntimeResourceClaim(s, "at_once", 0)
	if err != nil || once.GetRequested().GetProcessors() != 1 || once.GetRequested().GetWorkers() != 8 || once.PeakVusers != 0 {
		t.Fatalf("once phase lost zero-shard demand: %v %v", once, err)
	}
	s.Config.EventHubs.ConsumerMode = "observer"
	if c, err := NewRuntimeResourceClaim(s, "at_once", 10); err != nil || c != nil {
		t.Fatalf("observer acquired processor resources: %v %v", c, err)
	}
	if _, err := NewRuntimeResourceClaim(s, "at_once", -1); err == nil {
		t.Fatal("negative peak accepted")
	}
}
func TestRuntimeResourceClaimBoundsAndFingerprint(t *testing.T) {
	original := testResourceClaim()
	for _, mutate := range []func(*pb.RuntimeResourceClaim){
		func(c *pb.RuntimeResourceClaim) { c.Contract = "unknown" }, func(c *pb.RuntimeResourceClaim) { c.Protocol = "kafka" },
		func(c *pb.RuntimeResourceClaim) { c.PeakVusers = 0 }, func(c *pb.RuntimeResourceClaim) { c.Requested = nil },
		func(c *pb.RuntimeResourceClaim) { c.Requested.Processors = -1 }, func(c *pb.RuntimeResourceClaim) { c.Requested.Processors = 0; c.Requested.Workers = 0 }, func(c *pb.RuntimeResourceClaim) { c.Requested.Workers-- },
		func(c *pb.RuntimeResourceClaim) { c.Requested.MaxPartitionsPerProcessor = 33 },
	} {
		c := CloneRuntimeResourceClaim(original)
		mutate(c)
		if err := validateRuntimeResourceClaim(c, 1); err == nil {
			t.Fatalf("invalid claim accepted: %v", c)
		}
	}
	assignment := &pb.WorkAssignment{TestId: "run", GroupName: "group", VuserCount: 1, RuntimeResourceClaim: original}
	fingerprint := workAssignmentFingerprint(assignment)
	for _, mutate := range []func(*pb.RuntimeResourceClaim){func(c *pb.RuntimeResourceClaim) { c.PeakVusers++ }, func(c *pb.RuntimeResourceClaim) { c.Requested.Workers++ }, func(c *pb.RuntimeResourceClaim) { c.Requested.Processors++ }, func(c *pb.RuntimeResourceClaim) { c.Requested.MaxPartitionsPerProcessor++ }, func(c *pb.RuntimeResourceClaim) { c.Contract = "next" }, func(c *pb.RuntimeResourceClaim) { c.Protocol = "next" }} {
		copy := proto.Clone(assignment).(*pb.WorkAssignment)
		mutate(copy.RuntimeResourceClaim)
		if workAssignmentFingerprint(copy) == fingerprint {
			t.Fatal("claim mutation omitted from fingerprint")
		}
	}
	copy := toProtoAssignment(Assignment{VUserCount: 1, RuntimeResourceClaim: original})
	copy.RuntimeResourceClaim.Requested.Workers = 1
	if original.Requested.Workers != 16 {
		t.Fatal("assignment claim aliases caller")
	}
}
func TestValidRuntimeResourceClaimRejectsMalformedAndZeroIntents(t *testing.T) {
	if ValidRuntimeResourceClaim(nil) {
		t.Fatal("canonical nil zero demand became positive resource intent")
	}
	for _, tc := range []struct {
		name  string
		edit  func(*pb.RuntimeResourceClaim)
		valid bool
	}{
		{name: "valid peak claim", valid: true},
		{name: "zero-floor once phase", edit: func(c *pb.RuntimeResourceClaim) {
			c.PeakVusers = 0
			c.Requested.Processors = 1
			c.Requested.Workers = 8
		}, valid: true},
		{name: "unknown contract", edit: func(c *pb.RuntimeResourceClaim) { c.Contract = "next" }},
		{name: "wrong protocol", edit: func(c *pb.RuntimeResourceClaim) { c.Protocol = "kafka" }},
		{name: "missing vector", edit: func(c *pb.RuntimeResourceClaim) { c.Requested = nil }},
		{name: "negative peak", edit: func(c *pb.RuntimeResourceClaim) { c.PeakVusers = -1 }},
		{name: "zero processors", edit: func(c *pb.RuntimeResourceClaim) { c.Requested.Processors = 0; c.Requested.Workers = 0 }},
		{name: "negative processors", edit: func(c *pb.RuntimeResourceClaim) { c.Requested.Processors = -1 }},
		{name: "oversized processors", edit: func(c *pb.RuntimeResourceClaim) { c.Requested.Processors = maxGeneratorVUsers + 1 }},
		{name: "wrong worker charge", edit: func(c *pb.RuntimeResourceClaim) { c.Requested.Workers-- }},
		{name: "missing partition ceiling", edit: func(c *pb.RuntimeResourceClaim) { c.Requested.MaxPartitionsPerProcessor = 0 }},
		{name: "oversized partition ceiling", edit: func(c *pb.RuntimeResourceClaim) { c.Requested.MaxPartitionsPerProcessor = 33 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claim := testResourceClaim()
			if tc.edit != nil {
				tc.edit(claim)
			}
			if got := ValidRuntimeResourceClaim(claim); got != tc.valid {
				t.Fatalf("claim valid=%t want=%t: %v", got, tc.valid, claim)
			}
		})
	}
}

func TestRuntimeResourceAdmissionClonesAndRejectsUnprovenClaims(t *testing.T) {
	claim := testResourceClaim()
	a := &pb.RuntimeResourceAdmission{Claim: CloneRuntimeResourceClaim(claim), Admitted: proto.Clone(claim.Requested).(*pb.RuntimeResourceDemand), Current: true}
	if !ValidRuntimeResourceAdmission(claim, a) {
		t.Fatal("exact admission rejected")
	}
	for _, mutate := range []func(*pb.RuntimeResourceAdmission){func(a *pb.RuntimeResourceAdmission) { a.Current = false }, func(a *pb.RuntimeResourceAdmission) { a.Claim.PeakVusers-- }, func(a *pb.RuntimeResourceAdmission) { a.Admitted.Workers-- }, func(a *pb.RuntimeResourceAdmission) { a.Admitted = nil }} {
		copy := CloneRuntimeResourceAdmission(a)
		mutate(copy)
		if ValidRuntimeResourceAdmission(claim, copy) {
			t.Fatal("unqualified admission accepted")
		}
	}
	unknown := CloneRuntimeResourceAdmission(a)
	unknown.Claim.Contract = "arbitrary"
	if ValidRuntimeResourceAdmission(unknown.Claim, unknown) {
		t.Fatal("untyped matched admission qualified")
	}
	copies := CloneRuntimeResourceAdmissions(map[string]*pb.RuntimeResourceAdmission{"group": a})
	copies["group"].Claim.Requested.Workers = 1
	if a.Claim.Requested.Workers != 16 {
		t.Fatal("admission map aliases source")
	}
	capacity := testResourcePong().RuntimeCapacities[0].Resources
	values := protocolRuntimeCapacitiesFromProto([]*pb.ProtocolRuntimeCapacity{{Protocol: "eventhubs", Resources: capacity}})
	values[0].Resources.ReservedProcessors = 2
	if capacity.ReservedProcessors != 0 {
		t.Fatal("capability snapshot aliases protobuf")
	}
}

type resourceAckServer struct {
	pb.UnimplementedGeneratorControlServer
	calls       atomic.Int64
	assignment  *pb.AssignmentAck
	preparation *pb.WorkCommitAck
}

func (s *resourceAckServer) AssignWork(context.Context, *pb.WorkAssignment) (*pb.AssignmentAck, error) {
	s.calls.Add(1)
	return s.assignment, nil
}
func (s *resourceAckServer) CheckPreparation(context.Context, *pb.PreparationCheckRequest) (*pb.WorkCommitAck, error) {
	return s.preparation, nil
}
func resourceWireClient(t *testing.T, server *resourceAckServer) *Client {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	g := grpc.NewServer()
	pb.RegisterGeneratorControlServer(g, server)
	go func() { _ = g.Serve(listener) }()
	connection, err := grpc.NewClient("passthrough:///resource", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close(); g.Stop(); _ = listener.Close() })
	return &Client{rpc: pb.NewGeneratorControlClient(connection), connection: &sharedClientConnection{}}
}
func TestRuntimeResourceClientNegotiationRejectsOldAndMalformedPeers(t *testing.T) {
	claim := testResourceClaim()
	admission := &pb.RuntimeResourceAdmission{Claim: CloneRuntimeResourceClaim(claim), Admitted: proto.Clone(claim.Requested).(*pb.RuntimeResourceDemand), Current: true}
	server := &resourceAckServer{assignment: &pb.AssignmentAck{Accepted: true, RuntimeResourceAdmission: admission}}
	client := resourceWireClient(t, server)
	assignment := Assignment{VUserCount: 1, RuntimeResourceClaim: claim}
	if _, err := client.AssignWork(context.Background(), assignment); err == nil || server.calls.Load() != 0 {
		t.Fatal("unnegotiated peer received required claim")
	}
	bad := testResourcePong()
	bad.RuntimeCapacities[0].Resources.AvailableProcessors = 5
	client.observeRuntimeResources(bad)
	if _, err := client.AssignWork(context.Background(), assignment); err == nil || server.calls.Load() != 0 {
		t.Fatal("malformed peer received claim")
	}
	unavailable := testResourcePong()
	unavailable.RuntimeCapacities[0].Available = false
	client.observeRuntimeResources(unavailable)
	if _, err := client.AssignWork(context.Background(), assignment); err == nil || server.calls.Load() != 0 {
		t.Fatal("unavailable protocol runtime received required claim")
	}
	client.observeRuntimeResources(testResourcePong())
	got, err := client.AssignWork(context.Background(), assignment)
	if err != nil || !ValidRuntimeResourceAdmission(claim, got.RuntimeResourceAdmission) {
		t.Fatalf("valid wire admission: %v %v", got, err)
	}
	server.assignment.RuntimeResourceAdmission = nil
	if _, err := client.AssignWork(context.Background(), assignment); err == nil {
		t.Fatal("old peer empty accepted ack qualified")
	}
	client.observeRuntimeResources(nil)
	calls := server.calls.Load()
	if _, err := client.AssignWork(context.Background(), assignment); err == nil || server.calls.Load() != calls {
		t.Fatal("lost heartbeat did not clear negotiation")
	}
}
func TestRuntimeResourcePreparationClientRejectsMissingAndMismatchedAdmissions(t *testing.T) {
	claim := testResourceClaim()
	server := &resourceAckServer{preparation: &pb.WorkCommitAck{Accepted: true}}
	client := resourceWireClient(t, server)
	if err := client.CheckPreparation(context.Background(), "run", 0, map[string]int32{"group": 1}, nil); err != nil {
		t.Fatal("legacy no-resource readiness rejected", err)
	}
	claims := map[string]*pb.RuntimeResourceClaim{"group": claim}
	if _, err := client.CheckPreparationResources(context.Background(), "run", 0, map[string]int32{"group": 1}, nil, claims); err == nil {
		t.Fatal("old peer readiness qualified resource claims")
	}
	server.preparation.RuntimeResourceAdmissions = map[string]*pb.RuntimeResourceAdmission{"group": {Claim: CloneRuntimeResourceClaim(claim), Admitted: proto.Clone(claim.Requested).(*pb.RuntimeResourceDemand), Current: true}}
	got, err := client.CheckPreparationResources(context.Background(), "run", 0, map[string]int32{"group": 1}, nil, claims)
	if err != nil || !ValidRuntimeResourceAdmission(claim, got["group"]) {
		t.Fatalf("exact readiness: %v %v", got, err)
	}
	server.preparation.RuntimeResourceAdmissions["group"].Current = false
	if _, err := client.CheckPreparationResources(context.Background(), "run", 0, map[string]int32{"group": 1}, nil, claims); err == nil {
		t.Fatal("lost reservation qualified")
	}
}

type testRuntimeReservation struct {
	closed atomic.Bool
	demand protocol.RuntimeResourceDemand
}

func (r *testRuntimeReservation) ResourceProtocol() string               { return "eventhubs" }
func (r *testRuntimeReservation) Demand() protocol.RuntimeResourceDemand { return r.demand }
func (r *testRuntimeReservation) Current() bool                          { return !r.closed.Load() }
func (r *testRuntimeReservation) Adopt(context.Context) error            { return nil }
func (r *testRuntimeReservation) Resize(_ context.Context, d protocol.RuntimeResourceDemand) error {
	r.demand = d
	return nil
}
func (r *testRuntimeReservation) Release() { r.closed.Store(true) }
func TestRuntimeResourcePreparationStateAndRetirement(t *testing.T) {
	s, req := preparedReadinessFixture(t)
	claim := testResourceClaim()
	reservation := &testRuntimeReservation{demand: RuntimeResourceDemandFromProto(claim.Requested)}
	s.mu.Lock()
	assignment := s.assignments[req.TestId]
	assignment.groupRuntimeResources = map[string]protocol.RuntimeResourceReservation{"load": reservation}
	assignment.groupRuntimeResourceClaims = map[string]*pb.RuntimeResourceClaim{"load": CloneRuntimeResourceClaim(claim)}
	assignment.groupRuntimeResourcePeaks = map[string]int32{"load": 2}
	s.assignments[req.TestId] = assignment
	s.mu.Unlock()
	if _, err := s.CheckPreparation(context.Background(), req); err == nil {
		t.Fatal("missing expected claims qualified")
	}
	req.ExpectedRuntimeResources = map[string]*pb.RuntimeResourceClaim{"load": CloneRuntimeResourceClaim(claim)}
	ack, err := s.CheckPreparation(context.Background(), req)
	if err != nil || !ValidRuntimeResourceAdmission(claim, ack.RuntimeResourceAdmissions["load"]) {
		t.Fatalf("resource readiness: %v %v", ack, err)
	}
	req.ExpectedRuntimeResources["load"].PeakVusers = 1
	if _, err := s.CheckPreparation(context.Background(), req); err == nil {
		t.Fatal("changed claim qualified")
	}
	req.ExpectedRuntimeResources["load"] = CloneRuntimeResourceClaim(claim)
	reservation.closed.Store(true)
	if _, err := s.CheckPreparation(context.Background(), req); err == nil {
		t.Fatal("lost pointer qualified")
	}
	reservation.closed.Store(false)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := s.StopTest(ctx, &pb.StopRequest{TestId: req.TestId}); err != nil {
		t.Fatal(err)
	}
	if reservation.Current() {
		t.Fatal("detached parent not closed")
	}
}
func TestRuntimeResourceZeroPeakAndRollbackRemainFenced(t *testing.T) {
	s := newIsolatedService(t, 10)
	defer s.Shutdown(context.Background())
	s.assignments["run"] = assignedTest{testID: "run", committed: true, groups: map[string]int32{"group": 0}, groupRuntimeResourcePeaks: map[string]int32{"group": 0}}
	decision := s.lifecycleCoordinator.planScaleLocked(s, &pb.VUserAdjustment{TestId: "run", GroupName: "group", TargetVusers: 1}, javaVuserCapacityObservation{})
	if decision.err == nil {
		t.Fatal("zero resource peak lost its ceiling")
	}
	reservation := &testRuntimeReservation{demand: RuntimeResourceDemandFromProto(testResourceClaim().Requested)}
	s.lifecycleCoordinator.pendingStages["pending"] = &pendingAssignmentStage{token: 3, done: make(chan struct{}), runtimeResources: assignmentRuntimeResources{reservation: reservation}}
	if !s.lifecycleCoordinator.rollbackStageLocked("pending", 3) || reservation.Current() {
		t.Fatal("rollback retained unused resource budget")
	}
	if s.lifecycleCoordinator.rollbackStageLocked("pending", 3) {
		t.Fatal("duplicate rollback reclaimed another stage")
	}
}

func TestRuntimeResourceCapacityOnlyCommitRejectsReleasedClaim(t *testing.T) {
	s := newIsolatedService(t, 10)
	defer s.Shutdown(context.Background())
	claim := testResourceClaim()
	lease := &testRuntimeReservation{demand: RuntimeResourceDemandFromProto(claim.Requested)}
	lease.Release()
	s.assignments["run"] = assignedTest{testID: "run", groups: map[string]int32{"group": 1}, groupRuntimeResourceClaims: map[string]*pb.RuntimeResourceClaim{"group": claim}, groupRuntimeResources: map[string]protocol.RuntimeResourceReservation{"group": lease}}
	s.mu.Lock()
	decision := s.lifecycleCoordinator.commitWorkLocked(s, context.Background(), &pb.WorkCommitRequest{TestId: "run"}, time.Now())
	s.mu.Unlock()
	if decision.err == nil {
		t.Fatal("released capacity-only reservation committed")
	}
}
