package generatorrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/javarunner/broker"
	"github.com/jasonczech/apexload/pkg/javasidecar"
	"github.com/jasonczech/apexload/pkg/protocol"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

type javaHostTestPeer struct {
	mu         sync.Mutex
	calls      []javasidecar.BrokerFacadeRequest
	readyDelay time.Duration
}

func (p *javaHostTestPeer) count(op string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, q := range p.calls {
		if q.Operation == op {
			n++
		}
	}
	return n
}
func javaHostServiceFixture(t *testing.T) (*Service, *pb.WorkAssignment, *javaHostTestPeer, *javasidecar.BrokerRuntime) {
	t.Helper()
	peer := &javaHostTestPeer{}
	profile := broker.BrokerProfileCapability{ID: "approved", ConfigurationSHA256: strings.Repeat("a", 64), PolicySHA256: strings.Repeat("b", 64), MemoryBytes: 100, HostLeaseMS: 3000, PreparationMS: 5000, InvocationMS: 1000, MaxRunMS: 60000}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q javasidecar.BrokerFacadeRequest
		if json.NewDecoder(r.Body).Decode(&q) != nil {
			t.Error("bad request")
			w.WriteHeader(400)
			return
		}
		peer.mu.Lock()
		peer.calls = append(peer.calls, q)
		delay := peer.readyDelay
		peer.mu.Unlock()
		out := javasidecar.BrokerFacadeResponse{Contract: javasidecar.BrokerFacadeContract, RequestID: q.RequestID}
		switch q.Operation {
		case "capabilities":
			out.Capabilities = &broker.BrokerCapabilities{Mode: "dedicated", Containment: "execution_pod", MaxHosts: 4, MaxVUsers: 4, MaxMemoryBytes: 1000, Profiles: []broker.BrokerProfileCapability{profile}}
		case "claim":
			out.OwnerToken = "owner"
			out.LeaseMS = 6000
		case "renew_owner":
			out.Sequence = q.Sequence
			out.LeaseMS = 6000
		case "reserve":
			out.RunID = q.GroupID
			out.RunToken = "token"
			out.State = "preparing"
			out.LeaseMS = 3000
		case "ready":
			select {
			case <-r.Context().Done():
				return
			case <-time.After(delay):
			}
			out.RunID = q.RunID
			out.State = "ready"
		case "commit":
			out.RunID = q.RunID
			out.State = "committed"
		case "activate":
			out.RunID = q.RunID
			out.SessionID = q.SessionID
			out.HostID = "host-" + q.SessionID
			out.SessionToken = "session-token"
			out.LeaseMS = 3000
		case "renew_run":
			out.RunID = q.RunID
			out.Sequence = q.Sequence
			out.LeaseMS = 3000
		case "renew_session":
			out.RunID = q.RunID
			out.SessionID = q.SessionID
			out.HostID = "host-" + q.SessionID
			out.Sequence = q.Sequence
			out.LeaseMS = 3000
		case "invoke":
			out.RunID = q.RunID
			out.SessionID = q.SessionID
			out.HostID = "host-" + q.SessionID
			out.Sequence = q.Sequence
			out.Result = &javasidecar.ExecutionResponse{Contract: javasidecar.ContractVersion, ExecutionID: fmt.Sprintf("call-%d", q.Sequence), Success: true, DurationMS: 1}
		case "close_session":
			out.RunID = q.RunID
			out.SessionID = q.SessionID
			out.HostID = "host-" + q.SessionID
			out.State = "retiring"
		case "abort":
			out.RunID = q.RunID
			out.State = "retiring"
		case "release":
			out.State = "retiring"
		default:
			t.Errorf("unexpected op %s", q.Operation)
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(server.Close)
	client, err := javasidecar.NewBrokerFacadeClient(server.URL, strings.Repeat("x", 32), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := javasidecar.NewBrokerRuntime(t.Context(), client)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	action := func(method string) dsl.Action {
		return dsl.Action{Name: method, Type: "java_vuser_run", Target: "Actions", Params: map[string]any{"method": method}}
	}
	script := dsl.Script{ID: "script", Name: "java", Protocol: "java_vuser", Config: dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}}, Init: []dsl.Action{action("init")}, Actions: []dsl.Action{action("action")}, Teardown: []dsl.Action{action("end")}}
	digest, err := engine.JavaPooledScriptSHA256(&script)
	if err != nil {
		t.Fatal(err)
	}
	option, err := WithJavaHostRuntime(runtime, []JavaHostApproval{{ScriptSHA256: digest, TargetClass: "Actions", Profile: profile}})
	if err != nil {
		t.Fatal(err)
	}
	service := newIsolatedService(t, 4, WithExecution(), option)
	// Test the execution backend independently of the controller deadman tests.
	service.runSupervisionRequired = false
	t.Cleanup(func() { shutdownSupervisionTestService(t, service) })
	content, _ := json.Marshal(script)
	q := &pb.WorkAssignment{TestId: "run", GroupName: "group", ScriptId: "script", ScriptContent: content, VuserCount: 1, TerminationMode: "iterations", JavaHost: &pb.JavaHostSelection{Contract: JavaHostSelectionContract, ScriptSha256: digest, ProfileId: profile.ID, ConfigurationSha256: profile.ConfigurationSHA256, PolicySha256: profile.PolicySHA256, PreparationMs: 2000, RunLifetimeMs: 60000}}
	return service, q, peer, runtime
}
func TestJavaHostAssignmentColdBarrierCommitAndCleanup(t *testing.T) {
	service, q, peer, runtime := javaHostServiceFixture(t)
	ack, err := service.AssignWork(t.Context(), q)
	if err != nil || !ack.GetAccepted() {
		t.Fatal(ack, err)
	}
	if peer.count("reserve") != 1 || peer.count("invoke") != 0 || peer.count("commit") != 0 {
		t.Fatal("staging crossed execution barrier")
	}
	service.mu.RLock()
	execution := service.executions[q.TestId][q.GroupName]
	local := service.activePersistentJavaVusersLocked()
	service.mu.RUnlock()
	if local != 0 {
		t.Fatal("remote counted as local capacity", local)
	}
	retry, err := service.AssignWork(t.Context(), proto.Clone(q).(*pb.WorkAssignment))
	if err != nil || !retry.GetAccepted() || peer.count("reserve") != 1 {
		t.Fatal("retry reserved twice", retry, err)
	}
	committed, err := service.CommitWork(t.Context(), &pb.WorkCommitRequest{TestId: q.TestId})
	if err != nil || !committed.GetAccepted() {
		t.Fatal(committed, err)
	}
	select {
	case <-execution.done:
	case <-time.After(3 * time.Second):
		t.Fatal("engine not finished")
	}
	if peer.count("invoke") != 3 || peer.count("abort") != 1 || peer.count("release") != 0 {
		t.Fatal("wrong lifecycle", peer.count("invoke"), peer.count("abort"), peer.count("release"))
	}
	if runtime.Err() != nil {
		t.Fatal("group released generator owner", runtime.Err())
	}
}
func TestJavaHostAssignmentRejectsDriftAndUnavailableBackend(t *testing.T) {
	for _, kind := range []string{"contract", "script", "policy", "preparation", "backend", "source-plan", "adoption"} {
		t.Run(kind, func(t *testing.T) {
			service, q, peer, _ := javaHostServiceFixture(t)
			switch kind {
			case "adoption":
				q.AdoptionGrace = durationpb.New(time.Minute)
			case "contract":
				q.JavaHost.Contract = "future"
			case "script":
				q.JavaHost.ScriptSha256 = strings.Repeat("c", 64)
			case "policy":
				q.JavaHost.PolicySha256 = strings.Repeat("c", 64)
			case "preparation":
				q.JavaHost.PreparationMs = 5001
			case "backend":
				service.javaHostRuntime = nil
			case "source-plan":
				q.SourcePlan = &pb.SourcePlanManifest{}
			}
			ack, err := service.AssignWork(t.Context(), q)
			if err == nil && ack.GetAccepted() {
				t.Fatal("invalid placement accepted", kind)
			}
			if peer.count("reserve") != 0 || peer.count("invoke") != 0 {
				t.Fatal("invalid selection reserved capacity")
			}
		})
	}
}
func TestJavaHostAssignmentStageStopCleansOnlyGroup(t *testing.T) {
	service, q, peer, runtime := javaHostServiceFixture(t)
	ack, err := service.AssignWork(t.Context(), q)
	if err != nil || !ack.GetAccepted() {
		t.Fatal(ack, err)
	}
	service.mu.RLock()
	execution := service.executions[q.TestId][q.GroupName]
	service.mu.RUnlock()
	stopGroupExecution(execution)
	select {
	case <-execution.done:
	case <-time.After(time.Second):
		t.Fatal("staged cleanup blocked")
	}
	if peer.count("abort") != 1 || peer.count("invoke") != 0 || peer.count("release") != 0 || runtime.Err() != nil {
		t.Fatal("stage cleanup lost isolation")
	}
}
func TestJavaHostAssignmentCancelledPreparationRollsBack(t *testing.T) {
	service, q, peer, _ := javaHostServiceFixture(t)
	peer.readyDelay = time.Second
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	ack, err := service.AssignWork(ctx, q)
	if err == nil && ack.GetAccepted() {
		t.Fatal("cancelled stage accepted")
	}
	service.mu.RLock()
	_, exists := service.assignments[q.TestId]
	pending := len(service.lifecycleCoordinator.pendingStages)
	service.mu.RUnlock()
	if exists || pending != 0 || peer.count("invoke") != 0 || peer.count("abort") != 1 {
		t.Fatal(exists, pending, peer.count("abort"))
	}
}
func TestJavaHostSelectionFingerprintAndAdvertisement(t *testing.T) {
	service, q, _, _ := javaHostServiceFixture(t)
	caps := service.javaHostCapabilities()
	if caps == nil || caps.Contract != JavaHostSelectionContract || caps.Mode != "dedicated" || caps.Containment != "execution_pod" || len(caps.Profiles) != 1 {
		t.Fatal(caps)
	}
	original := workAssignmentFingerprint(q)
	changes := []func(*pb.JavaHostSelection){func(p *pb.JavaHostSelection) { p.PreparedVusers++ }, func(p *pb.JavaHostSelection) { p.FacadeResourcesSha256 = strings.Repeat("d", 64) }, func(p *pb.JavaHostSelection) { p.Contract += "x" }, func(p *pb.JavaHostSelection) { p.ScriptSha256 = "other" }, func(p *pb.JavaHostSelection) { p.ProfileId = "other" }, func(p *pb.JavaHostSelection) { p.ConfigurationSha256 = "other" }, func(p *pb.JavaHostSelection) { p.PolicySha256 = "other" }, func(p *pb.JavaHostSelection) { p.PreparationMs++ }, func(p *pb.JavaHostSelection) { p.RunLifetimeMs++ }}
	for _, change := range changes {
		copy := proto.Clone(q).(*pb.WorkAssignment)
		change(copy.JavaHost)
		if workAssignmentFingerprint(copy) == original {
			t.Fatal("placement change omitted from fingerprint")
		}
	}
	q.JavaHost = nil
	if workAssignmentFingerprint(q) == original {
		t.Fatal("dedicated fallback not fenced")
	}
}

func TestJavaHostPlacementPreservesArchiveSourceWireIdentity(t *testing.T) {
	q := &pb.WorkAssignment{ArchiveSourceTarget: "generator:9090", JavaHost: &pb.JavaHostSelection{Contract: JavaHostSelectionContract, ProfileId: "approved"}}
	fields := q.ProtoReflect().Descriptor().Fields()
	if fields.ByName("archive_source_target").Number() != 40 || fields.ByName("java_host").Number() != 41 {
		t.Fatal("published archive field number changed or Java placement reused it")
	}
	wire, err := proto.Marshal(q)
	if err != nil {
		t.Fatal(err)
	}
	var decoded pb.WorkAssignment
	if err := proto.Unmarshal(wire, &decoded); err != nil || !proto.Equal(q, &decoded) {
		t.Fatalf("placement and archive identity did not survive transport: %v", err)
	}
	original := workAssignmentFingerprint(q)
	for _, change := range []func(*pb.WorkAssignment){
		func(v *pb.WorkAssignment) { v.ArchiveSourceTarget = "other:9090" },
		func(v *pb.WorkAssignment) { v.JavaHost.ProfileId = "other" },
	} {
		copy := proto.Clone(q).(*pb.WorkAssignment)
		change(copy)
		if workAssignmentFingerprint(copy) == original {
			t.Fatal("assignment fingerprint lost an independently bound identity")
		}
	}
}

type javaHostLegacyClient struct {
	pb.GeneratorControlClient
	ack *pb.AssignmentAck
}

func (c javaHostLegacyClient) AssignWork(context.Context, *pb.WorkAssignment, ...grpc.CallOption) (*pb.AssignmentAck, error) {
	return c.ack, nil
}
func TestJavaHostClientRejectsUnnegotiatedAcknowledgement(t *testing.T) {
	service, q, _, _ := javaHostServiceFixture(t)
	a := Assignment{TestID: q.TestId, GroupName: q.GroupName, ScriptID: q.ScriptId, ScriptContent: q.ScriptContent, VUserCount: 1, JavaHost: q.JavaHost}
	for _, placement := range []*pb.JavaHostPlacement{nil, {Selection: cloneJavaHostSelection(q.JavaHost), ReservationId: "reserved", Mode: "pooled", Containment: "execution_pod", MaxHosts: 4, MaxVusers: 4}} {
		c := &Client{rpc: javaHostLegacyClient{ack: &pb.AssignmentAck{Accepted: true, JavaHost: placement}}, connection: &sharedClientConnection{refs: 1}}
		c.observeJavaHosts(&pb.HeartbeatPong{CompatibleFeatures: []string{FeatureJavaHostBrokerV1}, JavaHost: service.javaHostCapabilities()})
		if _, err := c.AssignWork(t.Context(), a); err == nil {
			t.Fatal("legacy or mismatched peer accepted")
		}
	}
	frozen := toProtoAssignment(a)
	a.JavaHost.PolicySha256 = "mutated"
	if frozen.GetJavaHost().PolicySha256 == "mutated" {
		t.Fatal("request retains mutable selection")
	}
}
func TestJavaHostClientRoundTrip(t *testing.T) {
	service, q, _, _ := javaHostServiceFixture(t)
	client := supervisionHeartbeatClient(t, service)
	heartbeat, err := client.OpenHeartbeat(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer heartbeat.CloseSend()
	if err = heartbeat.Send(Heartbeat{}); err != nil {
		t.Fatal(err)
	}
	if _, err = heartbeat.Receive(); err != nil {
		t.Fatal(err)
	}
	result, err := client.AssignWork(t.Context(), Assignment{TestID: q.TestId, GroupName: q.GroupName, ScriptID: q.ScriptId, ScriptContent: q.ScriptContent, VUserCount: 1, TerminationMode: q.TerminationMode, JavaHost: q.JavaHost})
	if err != nil || !result.Accepted || result.JavaHost == nil || result.JavaHost.ReservationId == "" || !proto.Equal(result.JavaHost.Selection, q.JavaHost) {
		t.Fatal(result, err)
	}
}

func TestJavaHostNegotiationRequiresRecentMatchingHeartbeat(t *testing.T) {
	service, q, _, _ := javaHostServiceFixture(t)
	c := &Client{connection: &sharedClientConnection{refs: 1}}
	if c.javaHostSelectionNegotiated(q) {
		t.Fatal("unknown peer selected")
	}
	pong := &pb.HeartbeatPong{CompatibleFeatures: []string{FeatureJavaHostBrokerV1}, JavaHost: service.javaHostCapabilities()}
	c.observeJavaHosts(pong)
	if !c.javaHostSelectionNegotiated(q) {
		t.Fatal("matching peer rejected")
	}
	retained, err := c.Retain()
	if err != nil {
		t.Fatal(err)
	}
	if !retained.javaHostSelectionNegotiated(q) {
		t.Fatal("retained client lost negotiation")
	}
	c.connection.javaHostObserved = time.Now().Add(-time.Minute)
	if c.javaHostSelectionNegotiated(q) {
		t.Fatal("stale peer selected")
	}
	c.observeJavaHosts(pong)
	c.observeJavaHosts(&pb.HeartbeatPong{})
	if retained.javaHostSelectionNegotiated(q) {
		t.Fatal("old peer retained capabilities")
	}
	pong.JavaHost.Profiles[0].MemoryBytes = 0
	c.observeJavaHosts(pong)
	if c.javaHostSelectionNegotiated(q) {
		t.Fatal("malformed capabilities accepted")
	}
}

func TestJavaHostFacadeResourcesAreBoundedAndFrozen(t *testing.T) {
	unit := JavaHostFacadeResources{CPU: 100, MemoryBytes: 8 << 20, NetworkKbps: 100}
	digest, err := unit.Digest()
	if err != nil || !javaHostDigest(digest) {
		t.Fatal(digest, err)
	}
	for _, r := range []JavaHostFacadeResources{{CPU: 1}, {CPU: -1, MemoryBytes: 1, NetworkKbps: 1}, {CPU: 1000001, MemoryBytes: 1, NetworkKbps: 1}, {CPU: 1, MemoryBytes: 1<<40 + 1, NetworkKbps: 1}, {CPU: 1, MemoryBytes: 1, NetworkKbps: 1000000001}} {
		if _, err := r.Digest(); err == nil {
			t.Fatalf("invalid facade budget accepted: %+v", r)
		}
	}
	service, q, _, _ := javaHostServiceFixture(t)
	a := service.javaHostApprovals[q.JavaHost.ScriptSha256]
	a.FacadeResources = unit
	service.javaHostApprovals[q.JavaHost.ScriptSha256] = a
	caps := service.javaHostCapabilities()
	if !ValidJavaHostCapabilities(caps) || caps.Profiles[0].FacadeResourcesSha256 != digest {
		t.Fatal("budget not advertised", caps)
	}
	c := &Client{connection: &sharedClientConnection{refs: 1}}
	c.observeJavaHosts(&pb.HeartbeatPong{CompatibleFeatures: []string{FeatureJavaHostBrokerV1}, JavaHost: caps})
	if c.javaHostSelectionNegotiated(q) {
		t.Fatal("missing resource identity accepted")
	}
	q.JavaHost.FacadeResourcesSha256 = digest
	if !c.javaHostSelectionNegotiated(q) {
		t.Fatal("frozen resource profile rejected")
	}
	caps.Profiles[0].FacadeMemoryBytes++
	if ValidJavaHostCapabilities(caps) {
		t.Fatal("forged resource identity accepted")
	}
	var source dsl.Script
	if err := json.Unmarshal(q.ScriptContent, &source); err != nil {
		t.Fatal(err)
	}
	if _, err := service.javaHostGroupOptions(q, &source, &source); err != nil {
		t.Fatal(err)
	}
	q.JavaHost.FacadeResourcesSha256 = strings.Repeat("d", 64)
	if _, err := service.javaHostGroupOptions(q, &source, &source); err == nil {
		t.Fatal("resource drift admitted")
	}
}

func TestJavaHostPreparedPopulationReservesColdGeneratorCapacity(t *testing.T) {
	service, q, peer, _ := javaHostServiceFixture(t)
	q.TerminationMode = "duration"
	q.TargetDurationSeconds = 10
	q.VuserCount = 0
	q.JavaHost.PreparedVusers = 3
	ack, err := service.AssignWork(t.Context(), q)
	if err != nil || !ack.GetAccepted() {
		t.Fatal(ack, err)
	}
	service.mu.RLock()
	active, available := service.activeVUsersLocked(), service.availableCapacityLocked()
	spare := javaHostPreparedSpare(service.assignments[q.TestId])
	service.mu.RUnlock()
	if active != 0 || available != 1 || spare != 3 || peer.count("invoke") != 0 {
		t.Fatal("cold capacity not retained", active, available, spare)
	}
	retry, err := service.AssignWork(t.Context(), proto.Clone(q).(*pb.WorkAssignment))
	if err != nil || !retry.GetAccepted() || peer.count("reserve") != 1 {
		t.Fatal("prepared retry changed reservation", retry, err)
	}
	other := proto.Clone(q).(*pb.WorkAssignment)
	other.TestId = "another"
	other.JavaHost.PreparedVusers = 2
	rejected, err := service.AssignWork(t.Context(), other)
	if err == nil && rejected.GetAccepted() {
		t.Fatal("oversubscribed unused prepared sessions")
	}
	// Drain evidence must retain the full prepared population, even with no
	// active Vusers. The generic stop path owns eventual exactly-once release.
	service.mu.RLock()
	if reservedAssignmentVUsers(service.assignments[q.TestId]) != 3 {
		t.Error("stop would drop unused capacity")
	}
	service.mu.RUnlock()
	for i := 0; i < 2; i++ {
		stopped, err := service.StopTest(t.Context(), &pb.StopRequest{TestId: q.TestId})
		if err != nil || !stopped.GetAcknowledged() {
			t.Fatal(stopped, err)
		}
	}
	service.mu.RLock()
	released, draining := service.availableCapacityLocked(), service.runSupervisionDrainingVUsers
	service.mu.RUnlock()
	if released != 4 || draining != 0 || peer.count("abort") != 1 || peer.count("invoke") != 0 {
		t.Fatal("cold reservation release was not exactly once", released, draining, peer.count("abort"))
	}
}

func TestJavaHostPreparedPopulationRequiresExplicitNegotiation(t *testing.T) {
	service, q, _, _ := javaHostServiceFixture(t)
	q.TerminationMode = "duration"
	q.JavaHost.PreparedVusers = 2
	c := &Client{connection: &sharedClientConnection{refs: 1}}
	pong := &pb.HeartbeatPong{CompatibleFeatures: []string{FeatureJavaHostBrokerV1}, JavaHost: service.javaHostCapabilities()}
	c.observeJavaHosts(pong)
	if c.javaHostSelectionNegotiated(q) {
		t.Fatal("old peer accepted prepared sessions")
	}
	pong.CompatibleFeatures = append(pong.CompatibleFeatures, FeatureJavaHostPreparedPopulationV1)
	c.observeJavaHosts(pong)
	if !c.javaHostSelectionNegotiated(q) {
		t.Fatal("prepared capability lost")
	}
	q.JavaHost.PreparedVusers = 5
	if c.javaHostSelectionNegotiated(q) {
		t.Fatal("validated initial count instead of reserved count")
	}
	q.JavaHost.PreparedVusers = -1
	if validateJavaHostSelection(q) == nil {
		t.Fatal("negative prepared count accepted")
	}
}

func TestJavaHostPreparedPopulationCannotOutgrowOrShrinkPodClaim(t *testing.T) {
	service, q, peer, _ := javaHostServiceFixture(t)
	q.TerminationMode = "duration"
	q.TargetDurationSeconds = 10
	q.VuserCount = 0
	q.JavaHost.PreparedVusers = 3
	service.podResources = podResourceCapacity{CPULimitCores: 4, MemoryLimitBytes: 1 << 30, MaxVUsers: 4}
	budget := protocol.CapacityVector{CPUCores: 4, MemoryMB: 1024, NetworkMbps: 100, VUsers: 4, Protocols: map[string]float64{"java_host_facade": 4}}
	claim := &pb.PodCapacityClaim{TestId: q.TestId, ReservationId: "claim", Incarnation: service.podCapacityIncarnation, ModelVersion: protocol.CapacityReservationModel, Revision: 1, Schedulable: CapacityVectorToProto(budget), Demand: CapacityVectorToProto(budget.Scale(.75)), GroupPeaks: map[string]int32{q.GroupName: 1}}
	ack, err := service.ReservePodCapacity(t.Context(), claim)
	if err != nil || !ack.Accepted {
		t.Fatal(ack, err)
	}
	staged, err := service.AssignWork(t.Context(), q)
	if err == nil && staged.GetAccepted() || peer.count("reserve") != 0 {
		t.Fatal("prepared population exceeded pod claim", staged, err)
	}
	claim = proto.Clone(claim).(*pb.PodCapacityClaim)
	claim.Revision = 2
	claim.GroupPeaks[q.GroupName] = 3
	ack, err = service.ReservePodCapacity(t.Context(), claim)
	if err != nil || !ack.Accepted {
		t.Fatal(ack, err)
	}
	staged, err = service.AssignWork(t.Context(), q)
	if err != nil || !staged.GetAccepted() {
		t.Fatal(staged, err)
	}
	claim = proto.Clone(claim).(*pb.PodCapacityClaim)
	claim.Revision = 3
	claim.GroupPeaks[q.GroupName] = 0
	ack, err = service.ReservePodCapacity(t.Context(), claim)
	if err != nil || ack.Accepted {
		t.Fatal("shrunk a live cold reservation", ack, err)
	}
}
