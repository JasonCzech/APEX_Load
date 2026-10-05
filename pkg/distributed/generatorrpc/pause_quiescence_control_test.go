package generatorrpc

import (
	"context"
	"errors"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// These narrow participants exercise partial fanout and compensation states
// that cannot be injected into a concrete engine's SDK without a live broker.
// Engine tests separately verify actual lease and completion barriers.
type pauseParticipantFixture struct {
	paused                          bool
	pauseCalls, proofCalls, resumes int
	proofContextErr                 error
	proofErr, resumeErr             error
	latchOnFailure                  bool
}

func (p *pauseParticipantFixture) Pause() error { p.pauseCalls++; p.paused = true; return nil }
func (p *pauseParticipantFixture) PauseAndWait(ctx context.Context) error {
	p.proofContextErr = ctx.Err()
	p.proofCalls++
	if p.proofErr == nil || p.latchOnFailure {
		p.paused = true
	}
	return p.proofErr
}
func (p *pauseParticipantFixture) Resume() error {
	p.resumes++
	if p.resumeErr == nil {
		p.paused = false
	}
	return p.resumeErr
}

func newPauseControlEngine(t *testing.T) *engine.MultiGroupEngine {
	t.Helper()
	mge, err := engine.NewMultiGroup(engine.MultiGroupConfig{Duration: time.Second, Groups: []engine.GroupConfig{{Name: "group", VUsers: 1, Script: &dsl.Script{Protocol: "http"}}}})
	if err != nil {
		t.Fatal(err)
	}
	return mge
}

func TestPauseQuiescenceCompensatesFailingLatchedParticipant(t *testing.T) {
	first, failing, unused := &pauseParticipantFixture{}, &pauseParticipantFixture{latchOnFailure: true, proofErr: &engine.PauseQuiescenceUnprovenError{Cause: context.DeadlineExceeded}}, &pauseParticipantFixture{}
	participants := []enginePauseParticipant{{engine: first}, {engine: failing}, {engine: unused}}
	applied, err := applyPauseParticipants(context.Background(), participants, true, true)
	if applied != 2 || !errors.Is(err, engine.ErrPauseQuiescenceUnproven) || !failing.paused {
		t.Fatalf("failing latched participant omitted: applied=%d err=%v", applied, err)
	}
	if err := rollbackPauseParticipants(context.Background(), participants, applied, false, true); err != nil {
		t.Fatal(err)
	}
	if first.paused || failing.paused || first.resumes != 1 || failing.resumes != 1 || unused.resumes != 0 || first.pauseCalls != 0 {
		t.Fatal("partial fanout was not fully compensated through strict proof")
	}
}

func TestPauseQuiescenceCompensationRequiresProofForPriorPause(t *testing.T) {
	p := &pauseParticipantFixture{proofErr: &engine.PauseQuiescenceUnprovenError{Cause: context.Canceled}, latchOnFailure: true}
	if err := rollbackPauseParticipants(context.Background(), []enginePauseParticipant{{engine: p}}, 1, true, true); !errors.Is(err, engine.ErrPauseQuiescenceUnproven) || p.pauseCalls != 0 || p.proofCalls != 1 {
		t.Fatalf("a latch was mistaken for compensation proof: %v", err)
	}
}

func TestPauseQuiescenceStrictRetryRevalidatesParticipants(t *testing.T) {
	service := newIsolatedService(t, 1, WithExecution())
	execution := &groupExecution{engine: newPauseControlEngine(t), done: make(chan struct{}), cancel: func() {}}
	execution.started.Store(true)
	service.mu.Lock()
	service.assignments["run"] = assignedTest{testID: "run", committed: true, paused: true, pauseOperationID: "pause", pauseOperationSequence: 1, groups: map[string]int32{"group": 0}}
	service.executions["run"] = map[string]*groupExecution{"group": execution}
	decision := service.lifecycleCoordinator.planPauseWithTokenLocked(service, "run", true, 0, "pause", 1, nil, true)
	if decision.plan != nil {
		service.lifecycleCoordinator.completeControlLocked(decision.plan.control)
	}
	service.mu.Unlock()
	close(execution.done)
	if decision.err != nil || decision.idempotent || decision.plan == nil || len(decision.plan.participants) != 1 || !decision.plan.requireQuiescence {
		t.Fatalf("strict replay returned cached proof: %+v", decision)
	}
}

func TestPauseQuiescenceRejectsSimulatedProofAndStrictResume(t *testing.T) {
	service := newIsolatedService(t, 1)
	service.mu.Lock()
	service.assignments["run"] = assignedTest{testID: "run", committed: true, groups: map[string]int32{}}
	service.mu.Unlock()
	request := &pb.PauseRequest{TestId: "run", Pause: true, OperationId: "pause", OperationSequence: 1, RequireQuiescence: true}
	if ack, err := service.SetPaused(context.Background(), request); status.Code(err) != codes.FailedPrecondition || ack != nil {
		t.Fatalf("simulated assignment obtained strict proof: %+v %v", ack, err)
	}
	request.RequireQuiescence = false
	ack, err := service.SetPaused(context.Background(), request)
	if err != nil || ack.QuiescenceConfirmed != nil {
		t.Fatalf("legacy pause changed proof semantics: %+v %v", ack, err)
	}
	request.Pause, request.RequireQuiescence = false, true
	if ack, err := service.SetPaused(context.Background(), request); status.Code(err) != codes.InvalidArgument || ack != nil {
		t.Fatalf("strict resume was accepted: %+v %v", ack, err)
	}
}

func TestPauseQuiescenceFailClosedRetainsDrainAndFencesLiveState(t *testing.T) {
	service := newIsolatedService(t, 2, WithExecution())
	execution := &groupExecution{engine: newPauseControlEngine(t), done: make(chan struct{}), cancel: func() {}}
	execution.started.Store(true)
	service.mu.Lock()
	assignment := assignedTest{testID: "run", committed: true, vuserCount: 2, groups: map[string]int32{"group": 2}}
	service.assignments["run"] = assignment
	service.executions["run"] = map[string]*groupExecution{"group": execution}
	control, _ := service.lifecycleCoordinator.reserveControlLocked("run", 0, assignmentControlPause)
	service.mu.Unlock()
	service.failClosedPause(&pauseTransitionPlan{control: control, testID: "run"})
	service.mu.Lock()
	_, exists := service.assignments["run"]
	_, stopping := service.lifecycleCoordinator.stoppingTests["run"]
	charged := service.runSupervisionDrainingVUsers
	stale := service.metricsBatchIsCurrentLocked(&pb.MetricsBatch{TestId: "run", LifecycleState: "RUNNING"})
	service.mu.Unlock()
	if exists || !stopping || charged != 2 || stale {
		t.Fatalf("unproved mixed execution remained live or lost drain charge: exists=%v stopping=%v charged=%d stale=%v", exists, stopping, charged, stale)
	}
	close(execution.done)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		service.mu.RLock()
		charged = service.runSupervisionDrainingVUsers
		service.mu.RUnlock()
		if charged == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("completed drain retained capacity")
}

func TestPauseQuiescenceFailClosedCannotDetachNewIncarnation(t *testing.T) {
	service := newIsolatedService(t, 1)
	service.mu.Lock()
	service.assignments["run"] = assignedTest{testID: "run", committed: true, executionGeneration: 2, groups: map[string]int32{}}
	control, _ := service.lifecycleCoordinator.reserveControlLocked("run", 1, assignmentControlPause)
	service.mu.Unlock()
	service.failClosedPause(&pauseTransitionPlan{control: control, testID: "run", generation: 1})
	service.mu.RLock()
	assignment, exists := service.assignments["run"]
	service.mu.RUnlock()
	if !exists || assignment.executionGeneration != 2 {
		t.Fatal("failed old pause detached a successor incarnation")
	}
}

func TestPauseQuiescenceFailedResumeCompensationAlwaysProvesPause(t *testing.T) {
	first := &pauseParticipantFixture{paused: true}
	failed := &pauseParticipantFixture{paused: true, resumeErr: errors.New("resume refused")}
	participants := []enginePauseParticipant{{engine: first}, {engine: failed}}
	applied, err := applyPauseParticipants(context.Background(), participants, false, false)
	if err == nil || applied != 1 || first.paused {
		t.Fatalf("resume failure setup: applied=%d err=%v", applied, err)
	}
	if err := rollbackPauseParticipants(context.Background(), participants, applied, true, false); err != nil || first.proofCalls != 1 || first.pauseCalls != 0 || !first.paused {
		t.Fatalf("failed ordinary Resume restored only a latch: %v", err)
	}
}

func TestPauseQuiescenceCommitFailureUsesFreshBoundedCompensation(t *testing.T) {
	service := newIsolatedService(t, 1)
	participant := &pauseParticipantFixture{paused: true}
	plan := &pauseTransitionPlan{previousPaused: true, participants: []enginePauseParticipant{{engine: participant}}}
	ctx, cancel := context.WithCancel(context.Background())
	applied, err := applyPauseParticipants(ctx, plan.participants, false, false)
	if err != nil || applied != 1 {
		t.Fatal(applied, err)
	}
	cancel() // The request deadline expired after Resume, before commit.
	if err := service.rollbackPausePlan(plan, applied); err != nil || participant.proofContextErr != nil || participant.proofCalls != 1 || participant.pauseCalls != 0 || !participant.paused {
		t.Fatalf("cancelled commit could not independently prove prior pause: %v", err)
	}
	participant.proofErr = &engine.PauseQuiescenceUnprovenError{Cause: context.DeadlineExceeded}
	if err := service.rollbackPausePlan(plan, 1); !errors.Is(err, engine.ErrPauseQuiescenceUnproven) {
		t.Fatalf("unproved rollback became successful: %v", err)
	}
}
