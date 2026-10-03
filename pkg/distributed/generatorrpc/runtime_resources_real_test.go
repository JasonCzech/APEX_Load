//go:build eventhubs_real

package generatorrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

func processorStageService(t *testing.T, execution bool, options ...ServiceOption) *Service {
	t.Helper()
	clearTransportEnv(t)
	if execution {
		options = append(options, WithExecution())
	}
	s := newIsolatedService(t, 10, options...)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	return s
}
func processorStageRequest(t *testing.T, id string, initial, peak int, prepare bool) *pb.WorkAssignment {
	t.Helper()
	script := runtimeResourceScript()
	script.Config.EventHubs.AuthType = "aad"
	script.Config.EventHubs.Namespace = "namespace.servicebus.windows.net"
	script.Config.EventHubs.EventHub = "events"
	script.Config.EventHubs.ConsumerGroup = "workers"
	script.Config.EventHubs.CheckpointContainerURL = "https://account.blob.core.windows.net/checkpoints"
	return processorScriptRequest(t, script, id, initial, peak, prepare)
}
func processorScriptRequest(t *testing.T, script *dsl.Script, id string, initial, peak int, prepare bool) *pb.WorkAssignment {
	t.Helper()
	content, err := json.Marshal(script)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := NewRuntimeResourceClaim(script, "at_runtime", peak)
	if err != nil {
		t.Fatal(err)
	}
	return &pb.WorkAssignment{TestId: id, GroupName: "group", ScriptId: id + "-script", ScriptContent: content, ExpectedScriptHash: scriptContentHash(content), VuserCount: int32(initial), TargetDuration: durationpb.New(time.Minute), PrepareOnly: prepare, RuntimeResourceClaim: claim}
}
func processorCapacity(t *testing.T) protocol.RuntimeResourceCapacity {
	t.Helper()
	instance, err := protocol.Get("eventhubs")
	if err != nil {
		t.Fatal(err)
	}
	c, supported, err := protocol.ReadRuntimeResourceCapacity(instance)
	if err != nil || !supported || !c.Available || !c.Valid() {
		t.Fatalf("processor capability: %+v %t %v", c, supported, err)
	}
	return c
}
func requireProcessorCharge(t *testing.T, processors, workers int64) {
	t.Helper()
	c := processorCapacity(t)
	if c.ReservedProcessors != processors || c.ReservedWorkers != workers || c.ActiveProcessors != 0 || c.ActiveWorkers != 0 {
		t.Fatalf("target-free charge: %+v", c)
	}
}
func stopProcessorStage(t *testing.T, s *Service, id string, generation uint64, token []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := s.StopTest(ctx, &pb.StopRequest{TestId: id, ExecutionGeneration: generation, RunSupervisionToken: token}); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeResourceProcessorStagingPinsPeakWithoutTargetTraffic(t *testing.T) {
	var requests, connections atomic.Int64
	target := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	target.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	target.StartTLS()
	t.Cleanup(target.Close)
	for _, initial := range []int{0, 1} {
		t.Run(fmt.Sprint(initial), func(t *testing.T) {
			s := processorStageService(t, true)
			request := processorStageRequest(t, "prepared", initial, 2, true)
			var script dsl.Script
			if err := json.Unmarshal(request.ScriptContent, &script); err != nil {
				t.Fatal(err)
			}
			script.Config.EventHubs.Namespace = target.Listener.Addr().String()
			script.Config.EventHubs.CheckpointContainerURL = target.URL + "/checkpoints"
			request = processorScriptRequest(t, &script, "prepared", initial, 2, true)
			ack, err := s.AssignWork(context.Background(), request)
			if err != nil || !ack.GetAccepted() || !ValidRuntimeResourceAdmission(request.RuntimeResourceClaim, ack.RuntimeResourceAdmission) {
				t.Fatalf("stage: %v %v", ack, err)
			}
			requireProcessorCharge(t, 2, 16)
			s.mu.RLock()
			a := s.assignments[request.TestId]
			execution := s.executions[request.TestId]["group"]
			lease := a.groupRuntimeResources["group"]
			config := execution.stagedConfig.Groups[0]
			s.mu.RUnlock()
			if a.groupRuntimeResourcePeaks["group"] != 2 || config.RuntimeResourcePeakVUsers != 2 || !config.RuntimeResourcePeakFrozen || config.RuntimeResourceReservation != lease || execution.started.Load() {
				t.Fatal("immutable reservation was not transferred without execution")
			}
			if err := lease.Adopt(context.Background()); err != nil {
				t.Fatal("staging adopted the lease", err)
			}
			// Do not execute this deliberately adopted test lease; cancellation must still reclaim it.
			if retry, err := s.AssignWork(context.Background(), request); err != nil || !retry.GetAccepted() {
				t.Fatalf("duplicate: %v %v", retry, err)
			}
			requireProcessorCharge(t, 2, 16)
			ready, err := s.CheckPreparation(context.Background(), &pb.PreparationCheckRequest{TestId: request.TestId, ExpectedGroups: map[string]int32{"group": int32(initial)}, ExpectedRuntimeResources: map[string]*pb.RuntimeResourceClaim{"group": request.RuntimeResourceClaim}})
			if err != nil || !ready.GetAccepted() {
				t.Fatalf("readiness: %v %v", ready, err)
			}
			if requests.Load() != 0 || connections.Load() != 0 {
				t.Fatal("preparation contacted target")
			}
			stopProcessorStage(t, s, request.TestId, 0, nil)
			requireProcessorCharge(t, 0, 0)
		})
	}
	if requests.Load() != 0 || connections.Load() != 0 {
		t.Fatal("preparation cleanup contacted target")
	}
}

func TestRuntimeResourceProcessorZeroPeakReadinessAndScaling(t *testing.T) {
	s := processorStageService(t, true)
	request := processorStageRequest(t, "zero", 0, 0, true)
	if request.RuntimeResourceClaim != nil {
		t.Fatal("zero demand materialized a claim")
	}
	ack, err := s.AssignWork(context.Background(), request)
	if err != nil || !ack.GetAccepted() {
		t.Fatalf("zero stage: %v %v", ack, err)
	}
	requireProcessorCharge(t, 0, 0)
	ready, err := s.CheckPreparation(context.Background(), &pb.PreparationCheckRequest{TestId: "zero", ExpectedGroups: map[string]int32{"group": 0}})
	if err != nil || !ready.GetAccepted() {
		t.Fatal(ready, err)
	}
	s.mu.Lock()
	decision := s.lifecycleCoordinator.planScaleLocked(s, &pb.VUserAdjustment{TestId: "zero", GroupName: "group", TargetVusers: 1}, javaVuserCapacityObservation{})
	s.mu.Unlock()
	if decision.err == nil {
		t.Fatal("zero immutable peak could scale above reservation")
	}

	s.mu.RLock()
	execution := s.executions["zero"]["group"]
	s.mu.RUnlock()
	if err := execution.engine.ClosePreparedRuntime(); err != nil {
		t.Fatal(err)
	}
	if ack, err := s.CheckPreparation(context.Background(), &pb.PreparationCheckRequest{TestId: "zero", ExpectedGroups: map[string]int32{"group": 0}}); err == nil || ack.GetAccepted() {
		t.Fatal("closed zero-resource engine remained ready")
	}
	if ack, err := s.CommitWork(context.Background(), &pb.WorkCommitRequest{TestId: "zero"}); err == nil && ack.GetAccepted() {
		t.Fatal("closed zero-resource engine crossed commit barrier")
	}
	stopProcessorStage(t, s, "zero", 0, nil)
	requireProcessorCharge(t, 0, 0)
}

func TestRuntimeResourceProcessorOverlapTamperingAndRollback(t *testing.T) {
	first := processorStageService(t, true)
	second := processorStageService(t, true)
	request := processorStageRequest(t, "first", 1, 2, true)
	if ack, err := first.AssignWork(context.Background(), request); err != nil || !ack.GetAccepted() {
		t.Fatal(ack, err)
	}
	requireProcessorCharge(t, 2, 16)
	if ack, err := second.AssignWork(context.Background(), processorStageRequest(t, "second", 0, 2, true)); err != nil || !ack.GetAccepted() {
		t.Fatal(ack, err)
	}
	requireProcessorCharge(t, 4, 32)
	if ack, err := second.AssignWork(context.Background(), processorStageRequest(t, "overflow", 1, 1, true)); err != nil || ack.GetAccepted() {
		t.Fatalf("overlap escaped global pool: %v %v", ack, err)
	}
	requireProcessorCharge(t, 4, 32)
	stopProcessorStage(t, second, "second", 0, nil)
	requireProcessorCharge(t, 2, 16)
	for _, mutate := range []func(*pb.WorkAssignment){
		func(r *pb.WorkAssignment) { r.RuntimeResourceClaim = nil },
		func(r *pb.WorkAssignment) {
			r.RuntimeResourceClaim.Requested.Processors = 1
			r.RuntimeResourceClaim.Requested.Workers = 8
		},
		func(r *pb.WorkAssignment) {
			r.RuntimeResourceClaim.Requested.MaxPartitionsPerProcessor = 4
			r.RuntimeResourceClaim.Requested.Workers = 8
		},
	} {
		r := processorStageRequest(t, "tampered", 1, 2, true)
		mutate(r)
		if ack, err := second.AssignWork(context.Background(), r); err != nil || ack.GetAccepted() {
			t.Fatalf("tampered claim accepted: %v %v", ack, err)
		}
		requireProcessorCharge(t, 2, 16)
	}
	invalid := processorStageRequest(t, "invalid", 1, 1, true)
	var script dsl.Script
	_ = json.Unmarshal(invalid.ScriptContent, &script)
	script.Actions[0].Type = "unsupported"
	// Keep a consuming init phase so reservation succeeds before script validation fails.
	script.Init = []dsl.Action{{Name: "consume", Type: "eventhubs_consume", Target: "events"}}
	invalid = processorScriptRequest(t, &script, "invalid", 1, 1, true)
	if ack, err := second.AssignWork(context.Background(), invalid); err == nil && ack.GetAccepted() {
		t.Fatal("invalid execution source accepted")
	}
	requireProcessorCharge(t, 2, 16)
	first.mu.RLock()
	lease := first.assignments["first"].groupRuntimeResources["group"]
	first.mu.RUnlock()
	lease.Release()
	if ack, err := first.CheckPreparation(context.Background(), &pb.PreparationCheckRequest{TestId: "first", ExpectedGroups: map[string]int32{"group": 1}, ExpectedRuntimeResources: map[string]*pb.RuntimeResourceClaim{"group": request.RuntimeResourceClaim}}); err == nil || ack.GetAccepted() {
		t.Fatal("released lease remained ready")
	}
	if ack, err := first.CommitWork(context.Background(), &pb.WorkCommitRequest{TestId: "first"}); err == nil && ack.GetAccepted() {
		t.Fatal("released lease crossed commit barrier")
	}
	stopProcessorStage(t, first, "first", 0, nil)
	requireProcessorCharge(t, 0, 0)
}

func TestRuntimeResourceProcessorExpiryAndFenceReleaseStagedParents(t *testing.T) {
	for _, kind := range []string{"expiry", "fence"} {
		t.Run(kind, func(t *testing.T) {
			s := processorStageService(t, true)
			r := processorStageRequest(t, "prepared", 1, 2, true)
			if ack, err := s.AssignWork(context.Background(), r); err != nil || !ack.GetAccepted() {
				t.Fatal(ack, err)
			}
			requireProcessorCharge(t, 2, 16)
			if kind == "expiry" {
				s.mu.RLock()
				token := s.lifecycleCoordinator.stageTokens[r.TestId]
				s.mu.RUnlock()
				s.expireStagedAssignment(r.TestId, token)
			} else {
				detached := s.detachFencedTests([]string{r.TestId}, 0, false)
				for _, stop := range detached {
					for _, execution := range stop.executions {
						stopGroupExecution(execution)
					}
					if err := s.finishDetachedFencedTest(context.Background(), stop); err != nil {
						t.Fatal(err)
					}
				}
			}
			requireProcessorCharge(t, 0, 0)
		})
	}
}

func TestRuntimeResourceProcessorHAAdoptionPreservesLeaseAndPeak(t *testing.T) {
	s := processorStageService(t, false)
	if ack := claimEpoch(t, s, 1, time.Minute); !ack.GetAccepted() {
		t.Fatal(ack)
	}
	token, next := supervisionTestToken(81), supervisionTestToken(82)
	r := processorStageRequest(t, "run", 1, 2, false)
	r.ExecutionGeneration = 1
	r.RunSupervisionToken = token
	r.AdoptionGrace = durationpb.New(time.Minute)
	if ack, err := s.AssignWork(context.Background(), r); err != nil || !ack.GetAccepted() {
		t.Fatal(ack, err)
	}
	if ack, err := s.ClaimRunSupervision(context.Background(), &pb.RunSupervisionClaim{TestId: "run", ExecutionGeneration: 1, ExecutionLeaseOwnerId: "controller/1", RunSupervisionToken: token, RequestedTtlMilliseconds: 60000}); err != nil || !ack.GetAccepted() {
		t.Fatal(ack, err)
	}
	if ack, err := s.CommitWork(context.Background(), &pb.WorkCommitRequest{TestId: "run", ExecutionGeneration: 1, RunSupervisionToken: token}); err != nil || !ack.GetAccepted() {
		t.Fatal(ack, err)
	}
	s.mu.RLock()
	before := s.assignments["run"]
	s.mu.RUnlock()
	requireProcessorCharge(t, 2, 16)
	ack := claimWithAdoptions(t, s, 2, &pb.RunAdoption{TestId: "run", PriorGeneration: 1, PriorRunSupervisionToken: token, NewRunSupervisionToken: next})
	if !ack.GetAccepted() || len(ack.Adoptions) != 1 || ack.Adoptions[0].Outcome != pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED {
		t.Fatal(ack)
	}
	s.mu.RLock()
	after := s.assignments["run"]
	s.mu.RUnlock()
	if after.groupRuntimeResources["group"] != before.groupRuntimeResources["group"] || after.groupRuntimeResourcePeaks["group"] != 2 || !proto.Equal(after.groupRuntimeResourceClaims["group"], before.groupRuntimeResourceClaims["group"]) {
		t.Fatal("adoption replaced immutable resource intent")
	}
	requireProcessorCharge(t, 2, 16)
	stopProcessorStage(t, s, "run", 2, next)
	requireProcessorCharge(t, 0, 0)
}

func TestRuntimeResourceProcessorConcurrentServicesShareAdmission(t *testing.T) {
	services := []*Service{processorStageService(t, true), processorStageService(t, true)}
	type outcome struct {
		service *Service
		request *pb.WorkAssignment
		ack     *pb.AssignmentAck
		err     error
	}
	requests := make([]*pb.WorkAssignment, 8)
	for i := range requests {
		requests[i] = processorStageRequest(t, fmt.Sprintf("concurrent-%d", i), 1, 2, true)
	}
	start := make(chan struct{})
	results := make(chan outcome, len(requests))
	for i, request := range requests {
		service := services[i%len(services)]
		go func() {
			<-start
			ack, err := service.AssignWork(context.Background(), request)
			results <- outcome{service, request, ack, err}
		}()
	}
	close(start)
	accepted := make([]outcome, 0, 2)
	for range requests {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.ack.GetAccepted() {
			if !ValidRuntimeResourceAdmission(result.request.RuntimeResourceClaim, result.ack.RuntimeResourceAdmission) {
				t.Fatal("unqualified concurrent admission")
			}
			accepted = append(accepted, result)
		}
	}
	if len(accepted) != 2 {
		t.Fatalf("admitted %d two-processor claims", len(accepted))
	}
	requireProcessorCharge(t, 4, 32)
	for _, result := range accepted {
		stopProcessorStage(t, result.service, result.request.TestId, 0, nil)
	}
	requireProcessorCharge(t, 0, 0)
}
