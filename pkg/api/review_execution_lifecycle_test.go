package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

type reviewPreparationCreateGateStore struct {
	*storage.SQLiteStore
	created chan struct{}
	release chan struct{}
}

func (s *reviewPreparationCreateGateStore) CreatePreparation(p storage.Preparation) (*storage.Preparation, error) {
	result, err := s.SQLiteStore.CreatePreparation(p)
	if err == nil {
		close(s.created)
		<-s.release
	}
	return result, err
}

type reviewPreparationStartGateBackend struct {
	started chan struct{}
	release chan struct{}
	result  chan error
	done    chan struct{}
}

func (b *reviewPreparationStartGateBackend) Run(tr *TestRunner, test *ManagedTest) {
	defer close(b.done)
	close(b.started)
	<-b.release
	b.result <- tr.awaitPreparedStart(test)
}

func TestReviewPreparationCreateCannotPoisonConcurrentOrdinaryStart(t *testing.T) {
	t.Setenv("APEXLOAD_PREPARATION_PROJECTS", "proj-a")
	server, durable := newTimeslotServer(t)
	store := &reviewPreparationCreateGateStore{SQLiteStore: durable.(*storage.SQLiteStore), created: make(chan struct{}), release: make(chan struct{})}
	server.store = store
	backend := &reviewPreparationStartGateBackend{started: make(chan struct{}), release: make(chan struct{}), result: make(chan error, 1), done: make(chan struct{})}
	WithExecutionBackend(backend)(server.runner)
	script, err := server.runner.StoreScriptDurable(&dsl.Script{ID: "review-concurrent-preparation", Name: "prepare race", Version: "1", Protocol: "http", ProjectID: "proj-a", Config: dsl.ScriptConfig{BaseURL: "http://127.0.0.1:1"}, Actions: []dsl.Action{{Name: "load", Type: "request", Method: "GET", Target: "/"}}})
	if err != nil {
		t.Fatal(err)
	}
	run, err := server.runner.CreateTest(CreateTestRequest{Name: "prepare race", ProjectID: "proj-a", ScriptID: script.ID, VUsers: 1, Duration: "1s", scriptShakeoutProvided: true, resourceBaselineProvided: true})
	if err != nil {
		t.Fatal(err)
	}
	var releasePrepare, releaseRun sync.Once
	defer func() {
		releasePrepare.Do(func() { close(store.release) })
		releaseRun.Do(func() { close(backend.release) })
		server.runner.mu.Lock()
		if current := server.runner.tests[run.ID]; current != nil && current.cancel != nil {
			current.cancel()
		}
		delete(server.runner.tests, run.ID)
		server.runner.mu.Unlock()
	}()
	reply := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+run.ID+"/preparation", nil)
	requestDone := make(chan struct{})
	go func() { defer close(requestDone); server.mux.ServeHTTP(reply, request) }()
	select {
	case <-store.created:
	case <-time.After(5 * time.Second):
		t.Fatal("preparation creation did not reach durable commit")
	}
	if err := server.runner.StartTest(run.ID); err != nil {
		t.Fatalf("ordinary start: %v", err)
	}
	select {
	case <-backend.started:
	case <-time.After(5 * time.Second):
		t.Fatal("ordinary run did not enter backend")
	}
	releasePrepare.Do(func() { close(store.release) })
	select {
	case <-requestDone:
	case <-time.After(5 * time.Second):
		t.Fatal("preparation request stuck")
	}
	if reply.Code != http.StatusConflict {
		t.Fatalf("prepare response %d: %s", reply.Code, reply.Body.String())
	}
	releaseRun.Do(func() { close(backend.release) })
	select {
	case <-backend.done:
	case <-time.After(5 * time.Second):
		t.Fatal("ordinary run gate stuck")
	}
	if err := <-backend.result; err != nil {
		server.runner.mu.RLock()
		prepID := server.runner.tests[run.ID].preparationID
		server.runner.mu.RUnlock()
		t.Fatalf("rejected preparation poisoned ordinary startup: preparationID=%q awaitPreparedStart=%v", prepID, err)
	}
}

func TestReviewCancelledStageHoldCannotScaleSuccessorRun(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	backend := &scalableExecutionBackendSpy{calls: make(chan scaleBackendCall, 8)}
	runner.setExecutionBackend(backend)
	const id = "review-stale-hold"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner.tests[id] = &ManagedTest{ID: id, Status: "RUNNING", ScenarioMode: "vuser_driven", ProjectID: storage.DefaultProjectID, TotalVUsers: 10, executionCtx: ctx, Groups: []TestGroup{{Name: "buyers", VUsers: 10, stageDriven: true, initialVUsers: 1, currentVUsers: 1}}}
	unlock := runner.lockDistributedGroupControl(id, "buyers")
	var release sync.Once
	defer release.Do(unlock)
	done := make(chan error, 1)
	go func() { done <- runner.applyStageHoldTarget(ctx, id, "buyers", 10) }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		runner.remoteControlMu.Lock()
		gate := runner.remoteControlGates[id]
		waiting := gate != nil && gate.refs >= 2
		runner.remoteControlMu.Unlock()
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("hold never queued at control gate")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	// Same-process HA recovery publishes the recovered run with a fresh context.
	runner.mu.Lock()
	runner.tests[id] = &ManagedTest{ID: id, Status: "RUNNING", ScenarioMode: "vuser_driven", ProjectID: storage.DefaultProjectID, TotalVUsers: 10, executionCtx: context.Background(), Groups: []TestGroup{{Name: "buyers", VUsers: 10, stageDriven: true, initialVUsers: 1, currentVUsers: 4}}}
	runner.mu.Unlock()
	release.Do(unlock)
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled old hold completed with %v, want cancellation; successor target=%d", err, runner.tests[id].Groups[0].currentVUsers)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("hold remained blocked")
	}
	select {
	case call := <-backend.calls:
		t.Fatalf("old hold scaled successor to %d", call.target)
	default:
	}
}

// The closed-VUser stop at the end of a stage schedule queues on the same
// control gate as a stage hold; once its run is replaced it must not stop the
// successor's group.
func TestReviewCancelledStageScheduleStopCannotScaleSuccessorRun(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	backend := &scalableExecutionBackendSpy{calls: make(chan scaleBackendCall, 8)}
	runner.setExecutionBackend(backend)
	const id = "review-stale-schedule-stop"
	closed := &GoalConfig{WorkloadShapeConfig: &WorkloadShapeConfig{ClosedVUser: &ClosedVUserConfig{Scope: "scenario", Scenario: &ClosedVUserDefinition{PeakVUsers: 4, RampUp: "0s", Sustain: "0s", RampDownEnabled: false, RampDown: "0s"}}}}
	run := func(ctx context.Context, current int) *ManagedTest {
		return &ManagedTest{ID: id, Status: "RUNNING", ScenarioMode: "vuser_driven", ProjectID: storage.DefaultProjectID, TotalVUsers: 4, executionCtx: ctx, GoalConfig: closed, Groups: []TestGroup{{Name: "buyers", VUsers: 4, stageDriven: true, initialVUsers: 4, currentVUsers: current}}}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner.mu.Lock()
	runner.tests[id] = run(ctx, 4)
	runner.mu.Unlock()
	unlock := runner.lockDistributedGroupControl(id, "buyers")
	var release sync.Once
	defer release.Do(unlock)
	done := make(chan struct{})
	go func() { defer close(done); runner.runStageSchedule(ctx, id, "buyers", nil, 0, nil) }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		runner.remoteControlMu.Lock()
		gate := runner.remoteControlGates[id]
		waiting := gate != nil && gate.refs >= 2
		runner.remoteControlMu.Unlock()
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("closed-VUser stop never queued at control gate")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	runner.mu.Lock()
	runner.tests[id] = run(context.Background(), 4)
	runner.mu.Unlock()
	release.Do(unlock)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("stage schedule remained blocked")
	}
	select {
	case call := <-backend.calls:
		t.Fatalf("old schedule scaled successor to %d", call.target)
	default:
	}
}

// The other ordering: a preparation already bound to the queued run owns it
// until its prepare-only start claims it, so an ordinary start is refused
// rather than inheriting a preparation it never requested.
func TestReviewOrdinaryStartCannotInheritBoundPreparation(t *testing.T) {
	t.Setenv("APEXLOAD_PREPARATION_PROJECTS", "proj-a")
	server, durable := newTimeslotServer(t)
	script, err := server.runner.StoreScriptDurable(&dsl.Script{ID: "review-bound-preparation", Name: "bound", Version: "1", Protocol: "http", ProjectID: "proj-a", Config: dsl.ScriptConfig{BaseURL: "http://127.0.0.1:1"}, Actions: []dsl.Action{{Name: "load", Type: "request", Method: "GET", Target: "/"}}})
	if err != nil {
		t.Fatal(err)
	}
	run, err := server.runner.CreateTest(CreateTestRequest{Name: "bound", ProjectID: "proj-a", ScriptID: script.ID, VUsers: 1, Duration: "1s", scriptShakeoutProvided: true, resourceBaselineProvided: true})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	bound, err := durable.(*storage.SQLiteStore).CreatePreparation(storage.Preparation{ID: "bound-preparation", TestID: run.ID, ProjectID: "proj-a", ExecutionRevision: strings.Repeat("a", 64), IdempotencyKey: "bound", CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	server.runner.mu.Lock()
	server.runner.tests[run.ID].preparationID = bound.ID
	server.runner.mu.Unlock()
	if err := server.runner.StartTest(run.ID); !errors.Is(err, errPreparationInProgress) {
		t.Fatalf("ordinary start of a run with a bound preparation = %v, want refusal", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+run.ID+"/start", nil)
	request.SetPathValue("id", run.ID)
	reply := httptest.NewRecorder()
	server.handleStartTest(reply, request)
	if reply.Code != http.StatusConflict {
		t.Fatalf("API start = %d %s, want 409", reply.Code, reply.Body.String())
	}
	if status := server.runner.testExecutionStatus(run.ID); status != "QUEUED" && status != "PENDING" {
		t.Fatalf("refused start changed status to %s", status)
	}
}

// A preparation whose prepare-only start is held back for capacity cannot
// reserve anything; it fails at once instead of leaving a binding that no
// preparation end state releases, and ordinary Start stays available.
func TestReviewPreparationHeldBackForCapacityReleasesTheRun(t *testing.T) {
	t.Setenv("APEXLOAD_PREPARATION_PROJECTS", "proj-a")
	server, _ := newTimeslotServer(t)
	script, err := server.runner.StoreScriptDurable(&dsl.Script{ID: "review-capacity-preparation", Name: "capacity", Version: "1", Protocol: "http", ProjectID: "proj-a", Config: dsl.ScriptConfig{BaseURL: "http://127.0.0.1:1"}, Actions: []dsl.Action{{Name: "load", Type: "request", Method: "GET", Target: "/"}}})
	if err != nil {
		t.Fatal(err)
	}
	create := func(name string) *ManagedTest {
		t.Helper()
		run, err := server.runner.CreateTest(CreateTestRequest{Name: name, ProjectID: "proj-a", ScriptID: script.ID, VUsers: 1, Duration: "1s", scriptShakeoutProvided: true, resourceBaselineProvided: true})
		if err != nil {
			t.Fatal(err)
		}
		return run
	}
	busy := []*ManagedTest{create("busy-1"), create("busy-2")}
	server.runner.mu.Lock()
	for _, run := range busy {
		server.runner.tests[run.ID].Status = "RUNNING"
	}
	server.runner.mu.Unlock()
	t.Cleanup(func() {
		server.runner.mu.Lock()
		for _, run := range busy {
			delete(server.runner.tests, run.ID)
		}
		server.runner.mu.Unlock()
	})
	run := create("held back")
	reply := httptest.NewRecorder()
	server.mux.ServeHTTP(reply, httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+run.ID+"/preparation", nil))
	if reply.Code != http.StatusConflict {
		t.Fatalf("prepare under full capacity = %d %s, want 409", reply.Code, reply.Body.String())
	}
	server.runner.mu.RLock()
	bound, status := server.runner.tests[run.ID].preparationID, server.runner.tests[run.ID].Status
	server.runner.mu.RUnlock()
	// Preparation never queues the run: it stays QUEUED, so nothing promotes it
	// into an unrequested load test.
	if bound != "" || status != "QUEUED" {
		t.Fatalf("held-back preparation left binding %q status %s", bound, status)
	}
	if err := server.runner.StartTest(run.ID); errors.Is(err, errPreparationInProgress) {
		t.Fatalf("ordinary start still refused after the failed preparation: %v", err)
	}
}

// A prepare-only run that cannot get peak pod capacity fails with its
// preparation instead of parking in PENDING with a binding nothing releases.
func TestReviewFailBoundPreparationEndsThePreparation(t *testing.T) {
	t.Setenv("APEXLOAD_PREPARATION_PROJECTS", "proj-a")
	server, durable := newTimeslotServer(t)
	store := durable.(*storage.SQLiteStore)
	script, err := server.runner.StoreScriptDurable(&dsl.Script{ID: "review-pod-preparation", Name: "pod", Version: "1", Protocol: "http", ProjectID: "proj-a", Config: dsl.ScriptConfig{BaseURL: "http://127.0.0.1:1"}, Actions: []dsl.Action{{Name: "load", Type: "request", Method: "GET", Target: "/"}}})
	if err != nil {
		t.Fatal(err)
	}
	run, err := server.runner.CreateTest(CreateTestRequest{Name: "pod", ProjectID: "proj-a", ScriptID: script.ID, VUsers: 1, Duration: "1s", scriptShakeoutProvided: true, resourceBaselineProvided: true})
	if err != nil {
		t.Fatal(err)
	}
	server.runner.mu.RLock()
	test := server.runner.tests[run.ID]
	server.runner.mu.RUnlock()
	if server.runner.failBoundPreparation(test) {
		t.Fatal("unbound run reported a preparation")
	}
	now := time.Now().UTC()
	p, err := store.CreatePreparation(storage.Preparation{ID: "pod-preparation", TestID: run.ID, ProjectID: "proj-a", ExecutionRevision: strings.Repeat("b", 64), IdempotencyKey: "pod", CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	server.runner.mu.Lock()
	test.preparationID = p.ID
	server.runner.mu.Unlock()
	if !server.runner.failBoundPreparation(test) {
		t.Fatal("bound preparation not reported")
	}
	if current, err := store.GetPreparation(p.ID); err != nil || current.State != "failed" {
		t.Fatalf("preparation after pod shortage = %+v %v", current, err)
	}
}
