package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"sync"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func TestProcessorPauseScopeUsesImmutableAdmissionAndLegacyPlans(t *testing.T) {
	claim := func(n int64) *pb.RuntimeResourceClaim {
		return pauseScopeClaim(n)
	}
	consumer := processorResourceGroup("consumer", 0, 4)
	producer := processorResourceGroup("producer", 1, 4)
	producer.script.Actions = nil
	for _, tc := range []struct {
		name string
		test *ManagedTest
		want map[string]bool
		fail bool
	}{
		{name: "modern compacted", test: &ManagedTest{Groups: []TestGroup{{Name: "consumer", Protocol: "eventhubs"}}, GeneratorShards: []GeneratorShardSummary{{Group: "consumer", Target: "a", RuntimeResourceClaim: claim(1)}}}, want: map[string]bool{"a": true}},
		{name: "legacy zero floor consuming plan", test: &ManagedTest{Groups: []TestGroup{consumer}, GeneratorShards: []GeneratorShardSummary{{Group: "consumer", Target: "a"}}}, want: map[string]bool{"a": true}},
		{name: "producer-only processor configuration", test: &ManagedTest{Groups: []TestGroup{producer}, GeneratorShards: []GeneratorShardSummary{{Group: "producer", Target: "a"}}}, want: map[string]bool{}},
		{name: "invalid zero claim cannot override legacy duplicate", test: &ManagedTest{Groups: []TestGroup{consumer}, GeneratorShards: []GeneratorShardSummary{{Group: "consumer", Target: "a", RuntimeResourceClaim: claim(0)}}, remoteGroupShards: map[string][]GeneratorShardSummary{"consumer": {{Group: "consumer", Target: "a"}}}}, fail: true},
		{name: "retired unavailable source", test: &ManagedTest{Groups: []TestGroup{{Name: "consumer", Protocol: "eventhubs"}}, GeneratorShards: []GeneratorShardSummary{{Group: "consumer", Target: "a"}}, remoteTargetStates: map[string]string{"a": "evacuated"}}, want: map[string]bool{}},
		{name: "completed processor", test: &ManagedTest{Groups: []TestGroup{consumer}, GeneratorShards: []GeneratorShardSummary{{Group: "consumer", Target: "a", RuntimeResourceClaim: claim(1)}}, remoteTargetStates: map[string]string{"a": "COMPLETE"}}, want: map[string]bool{}},
		{name: "unconfirmed legacy script", test: &ManagedTest{Groups: []TestGroup{{Name: "consumer", Protocol: "eventhubs"}}, GeneratorShards: []GeneratorShardSummary{{Group: "consumer", Target: "a"}}}, fail: true},
		{name: "missing topology", test: &ManagedTest{Groups: []TestGroup{consumer}}, fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := processorPauseTargetsLocked(tc.test)
			if (err != nil) != tc.fail || !tc.fail && !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("scope=%v err=%v want=%v", got, err, tc.want)
			}
		})
	}
}

func TestProcessorPauseCapabilitiesRequireBothRuntimeAndWireProof(t *testing.T) {
	for _, tc := range []struct {
		name     string
		ready    bool
		features []string
		fail     bool
	}{
		{"modern", true, []string{protocol.FeatureEventHubsProcessorPauseV1, generatorrpc.FeatureMessagingRuntimeQuiescenceV1}, false},
		{"runtime only", true, []string{protocol.FeatureEventHubsProcessorPauseV1}, true},
		{"wire only", true, []string{generatorrpc.FeatureMessagingRuntimeQuiescenceV1}, true},
		{"legacy", true, nil, true},
		{"failed readiness", false, []string{protocol.FeatureEventHubsProcessorPauseV1, generatorrpc.FeatureMessagingRuntimeQuiescenceV1}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if fail := processorPauseCapabilityError(generatorLoadSnapshot{HeartbeatOK: tc.ready, CompatibleFeatures: tc.features}) != nil; fail != tc.fail {
				t.Fatalf("fail=%v want=%v", fail, tc.fail)
			}
		})
	}
}

// This is an external generator transport fixture; the controller and protobuf
// client are real, including required-proof validation and compensation.
type pauseProofGenerator struct {
	pb.UnimplementedGeneratorControlServer
	features    []string
	proof       *bool
	failResume  bool
	unassigned  bool
	onHeartbeat func()
	onPause     func(bool)
	mu          sync.Mutex
	calls       []generatorrpc.PauseControl
}

func (s *pauseProofGenerator) Heartbeat(stream grpc.BidiStreamingServer[pb.HeartbeatPing, pb.HeartbeatPong]) error {
	if _, err := stream.Recv(); err != nil {
		return err
	}
	if s.onHeartbeat != nil {
		s.onHeartbeat()
	}
	if err := stream.Send(&pb.HeartbeatPong{GeneratorId: "fixture", CompatibleFeatures: s.features}); err != nil {
		return err
	}
	_, err := stream.Recv()
	if err == io.EOF {
		return nil
	}
	return err
}
func (s *pauseProofGenerator) SetPaused(_ context.Context, r *pb.PauseRequest) (*pb.PauseAck, error) {
	s.mu.Lock()
	s.calls = append(s.calls, generatorrpc.PauseControl{Pause: r.Pause, RequireQuiescence: r.RequireQuiescence, OperationID: r.OperationId, OperationSequence: r.OperationSequence})
	s.mu.Unlock()
	if s.onPause != nil {
		s.onPause(r.Pause)
	}
	if s.unassigned {
		return nil, status.Error(codes.NotFound, "completed assignment no longer exists")
	}
	if !r.Pause && s.failResume {
		return nil, status.Error(codes.Internal, "fixture cannot restore running admission")
	}
	return &pb.PauseAck{Accepted: true, CurrentPaused: r.Pause, AppliedOperationId: r.OperationId, AppliedOperationSequence: r.OperationSequence, QuiescenceConfirmed: s.proof}, nil
}
func (s *pauseProofGenerator) snapshotCalls() []generatorrpc.PauseControl {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]generatorrpc.PauseControl(nil), s.calls...)
}
func newPauseProofGeneratorClient(t *testing.T, s *pauseProofGenerator) *generatorrpc.Client {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	pb.RegisterGeneratorControlServer(server, s)
	go func() { _ = server.Serve(listener) }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	client, err := generatorrpc.Dial(ctx, "pause-fixture", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		server.Stop()
		_ = listener.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close(); server.Stop(); _ = listener.Close() })
	return client
}
func TestProcessorPausePreflightsAllPeersBeforeMutation(t *testing.T) {
	yes := true
	modern := &pauseProofGenerator{features: []string{protocol.FeatureEventHubsProcessorPauseV1, generatorrpc.FeatureMessagingRuntimeQuiescenceV1}, proof: &yes}
	legacy := &pauseProofGenerator{}
	a, b := newPauseProofGeneratorClient(t, modern), newPauseProofGeneratorClient(t, legacy)
	backend := newDistributedExecutionBackend([]string{"a", "b"}, time.Second, nil)
	runner := NewTestRunner(1, NewHub(), nil)
	claim := pauseScopeClaim(1)
	test := &ManagedTest{ID: "mixed", remoteClientsByTarget: map[string]generatorClient{"a": a, "b": b}, GeneratorShards: []GeneratorShardSummary{{Target: "a", RuntimeResourceClaim: claim}, {Target: "b", RuntimeResourceClaim: claim}}}
	used, err := backend.applyAcknowledgedPauseControl(runner, test, true)
	if !used || err == nil {
		t.Fatalf("used=%v err=%v", used, err)
	}
	if len(modern.snapshotCalls())+len(legacy.snapshotCalls()) != 0 || test.remotePauseOperationSequence != 0 {
		t.Fatal("unsupported readiness mutated a participant or allocated an operation")
	}
}
func TestProcessorPauseRequiresProofAndCompensatesMissingProof(t *testing.T) {
	yes, no := true, false
	for _, tc := range []struct {
		name                     string
		proof                    *bool
		failResume               bool
		wantError, wantUncertain bool
	}{
		{name: "confirmed", proof: &yes},
		{name: "absent", wantError: true},
		{name: "false", proof: &no, wantError: true},
		{name: "unproven compensation", failResume: true, wantError: true, wantUncertain: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			peer := &pauseProofGenerator{features: []string{protocol.FeatureEventHubsProcessorPauseV1, generatorrpc.FeatureMessagingRuntimeQuiescenceV1}, proof: tc.proof, failResume: tc.failResume}
			client := newPauseProofGeneratorClient(t, peer)
			backend := newDistributedExecutionBackend([]string{"a"}, time.Second, nil)
			runner := NewTestRunner(1, NewHub(), nil)
			test := &ManagedTest{ID: "processor", remoteClientsByTarget: map[string]generatorClient{"a": client}, GeneratorShards: []GeneratorShardSummary{{Target: "a", RuntimeResourceClaim: pauseScopeClaim(1)}}}
			used, err := backend.applyAcknowledgedPauseControl(runner, test, true)
			var uncertain *distributedPauseUncertainError
			if !used || (err != nil) != tc.wantError || errors.As(err, &uncertain) != tc.wantUncertain {
				t.Fatalf("used=%v err=%v", used, err)
			}
			calls := peer.snapshotCalls()
			if len(calls) == 0 || !calls[0].RequireQuiescence {
				t.Fatal("processor pause did not require proof")
			}
			if tc.wantError && (calls[len(calls)-1].Pause || calls[len(calls)-1].RequireQuiescence) {
				t.Fatal("compensation did not restore running admission")
			}
		})
	}
}
func TestLocalUnprovenPauseStopsDurablyAndCancelsExecution(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cause := &engine.PauseQuiescenceUnprovenError{Cause: context.DeadlineExceeded}
	backend := &lifecycleTransitionBackend{pauseErr: cause}
	test := &ManagedTest{ID: "local-unproven", Status: "RUNNING", executionBackend: backend, executionCtx: ctx, cancel: cancel}
	runner.tests[test.ID] = test
	err := runner.PauseTest(test.ID)
	if !errors.Is(err, engine.ErrPauseQuiescenceUnproven) || !errors.Is(err, context.DeadlineExceeded) || test.Status != "STOPPING" || test.PausedAt != nil {
		t.Fatalf("status=%s err=%v", test.Status, err)
	}
	_, stops := backend.counts()
	if stops != 1 {
		t.Fatalf("stops=%d", stops)
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("unproven local work remained admitted")
	}
}
func TestLocalUnprovenPauseStopsEngineWithoutRemoteBackend(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	mge, createErr := engine.NewMultiGroup(engine.MultiGroupConfig{Duration: time.Minute, Groups: []engine.GroupConfig{{Name: "http", VUsers: 1, Script: &dsl.Script{Name: "http", Protocol: "http"}}}})
	if createErr != nil {
		t.Fatal(createErr)
	}
	test := &ManagedTest{ID: "local-engine", Status: "RUNNING", multiEngine: mge}
	runner.tests[test.ID] = test
	err := runner.failClosedUncertainPauseControl(test, "RUNNING", localExecutionBackend{}, &engine.PauseQuiescenceUnprovenError{Cause: context.Canceled})
	if !errors.Is(err, engine.ErrPauseQuiescenceUnproven) || !mge.HardStopRequested() || test.Status != "STOPPING" {
		t.Fatalf("status=%s stopped=%v err=%v", test.Status, mge.HardStopRequested(), err)
	}
}

func pauseScopeClaim(n int64) *pb.RuntimeResourceClaim {
	return &pb.RuntimeResourceClaim{Contract: generatorrpc.FeatureRuntimeResourceReservationsV1, Protocol: "eventhubs", PeakVusers: 1, Requested: &pb.RuntimeResourceDemand{Processors: n, Workers: n, MaxPartitionsPerProcessor: 1}}
}
func TestProcessorPauseSkipsRetainedTerminalArchivePeers(t *testing.T) {
	yes := true
	for _, state := range []string{"COMPLETE", "FAILED", "evacuated"} {
		for _, pause := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/%t", state, pause), func(t *testing.T) {
				active := &pauseProofGenerator{features: []string{protocol.FeatureEventHubsProcessorPauseV1, generatorrpc.FeatureMessagingRuntimeQuiescenceV1}, proof: &yes}
				retired := &pauseProofGenerator{unassigned: true}
				a, b := newPauseProofGeneratorClient(t, active), newPauseProofGeneratorClient(t, retired)
				backend := newDistributedExecutionBackend([]string{"retired", "active"}, time.Second, nil)
				runner := NewTestRunner(1, NewHub(), nil)
				testRun := &ManagedTest{ID: "retained-archive", remoteClientsByTarget: map[string]generatorClient{"active": a, "retired": b}, GeneratorShards: []GeneratorShardSummary{{Group: "workers", Target: "active", RuntimeResourceClaim: pauseScopeClaim(1)}, {Group: "workers", Target: "retired", RuntimeResourceClaim: pauseScopeClaim(1)}}, remoteTargetStates: map[string]string{"active": "RUNNING", "retired": state}}
				used, err := backend.applyAcknowledgedPauseControl(runner, testRun, pause)
				if !used || err != nil {
					t.Fatalf("used=%t err=%v", used, err)
				}
				if len(active.snapshotCalls()) != 1 || len(retired.snapshotCalls()) != 0 {
					t.Fatalf("active=%v retired=%v", active.snapshotCalls(), retired.snapshotCalls())
				}
			})
		}
	}
}
func TestProcessorPauseRejectsMalformedAdmissionBeforeMutation(t *testing.T) {
	yes := true
	for _, mutate := range []func(*pb.RuntimeResourceClaim){func(c *pb.RuntimeResourceClaim) { c.Requested.Processors = 0 }, func(c *pb.RuntimeResourceClaim) { c.Requested.Processors = -1 }, func(c *pb.RuntimeResourceClaim) { c.Protocol = "http" }, func(c *pb.RuntimeResourceClaim) { c.Requested.Workers = 0 }} {
		peer := &pauseProofGenerator{features: []string{protocol.FeatureEventHubsProcessorPauseV1, generatorrpc.FeatureMessagingRuntimeQuiescenceV1}, proof: &yes}
		client := newPauseProofGeneratorClient(t, peer)
		claim := pauseScopeClaim(1)
		mutate(claim)
		backend := newDistributedExecutionBackend([]string{"a"}, time.Second, nil)
		runner := NewTestRunner(1, NewHub(), nil)
		testRun := &ManagedTest{ID: "invalid-admission", Groups: []TestGroup{{Name: "workers", Protocol: "eventhubs"}}, remoteClientsByTarget: map[string]generatorClient{"a": client}, GeneratorShards: []GeneratorShardSummary{{Group: "workers", Target: "a", RuntimeResourceClaim: claim}}}
		used, err := backend.applyAcknowledgedPauseControl(runner, testRun, true)
		if !used || err == nil || len(peer.snapshotCalls()) != 0 || testRun.remotePauseOperationSequence != 0 {
			t.Fatalf("used=%t err=%v calls=%v seq=%d", used, err, peer.snapshotCalls(), testRun.remotePauseOperationSequence)
		}
	}
}

func TestProcessorPauseRechecksPeerCompletionDuringFanout(t *testing.T) {
	yes := true
	for _, moment := range []string{"preflight", "before request", "request rejection", "compensation"} {
		t.Run(moment, func(t *testing.T) {
			runner := NewTestRunner(1, NewHub(), nil)
			run := &ManagedTest{ID: "finishing-peer", remoteTargetStates: map[string]string{"a": "RUNNING", "b": "RUNNING"}, GeneratorShards: []GeneratorShardSummary{{Target: "a", RuntimeResourceClaim: pauseScopeClaim(1)}, {Target: "b", RuntimeResourceClaim: pauseScopeClaim(1)}}}
			finish := func() { runner.mu.Lock(); run.remoteTargetStates["b"] = "COMPLETE"; runner.mu.Unlock() }
			a := &pauseProofGenerator{features: []string{protocol.FeatureEventHubsProcessorPauseV1, generatorrpc.FeatureMessagingRuntimeQuiescenceV1}, proof: &yes}
			b := &pauseProofGenerator{features: a.features, proof: &yes, unassigned: true}
			switch moment {
			case "preflight":
				a.onHeartbeat = finish
			case "before request":
				a.onPause = func(bool) { finish() }
			case "request rejection":
				b.onPause = func(bool) { finish() }
			case "compensation":
				b.onPause = func(pause bool) {
					if !pause {
						finish()
					}
				}
			}
			run.remoteClientsByTarget = map[string]generatorClient{"a": newPauseProofGeneratorClient(t, a), "b": newPauseProofGeneratorClient(t, b)}
			backend := newDistributedExecutionBackend([]string{"a", "b"}, time.Second, nil)
			used, err := backend.applyAcknowledgedPauseControl(runner, run, true)
			var uncertain *distributedPauseUncertainError
			if !used || errors.As(err, &uncertain) || (err != nil) != (moment == "compensation") {
				t.Fatalf("moment=%s used=%t err=%v", moment, used, err)
			}
			calls := b.snapshotCalls()
			if (moment == "preflight" || moment == "before request") && len(calls) != 0 {
				t.Fatalf("terminal peer still controlled: %v", calls)
			}
			if moment == "request rejection" && len(calls) != 1 {
				t.Fatalf("confirmed terminal rejection was retried: %v", calls)
			}
		})
	}
}
