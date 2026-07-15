package engine

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

var rejectScaleInit atomic.Bool

type scaleInitFailureProtocol struct{}

func (*scaleInitFailureProtocol) Name() string { return "scale-init-failure-test" }
func (*scaleInitFailureProtocol) Init(context.Context, map[string]any) error {
	if rejectScaleInit.Load() {
		return errors.New("injected init failure")
	}
	return nil
}
func (*scaleInitFailureProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	return &protocol.Result{ActionName: action.Name, Success: true}, nil
}
func (*scaleInitFailureProtocol) Teardown(context.Context) error    { return nil }
func (*scaleInitFailureProtocol) HealthCheck(context.Context) error { return nil }

func init() {
	protocol.Register("scale-init-failure-test", func() protocol.Protocol { return &scaleInitFailureProtocol{} })
}

func TestAdjustDurationRetainsCompletedPauseCompensation(t *testing.T) {
	ready := make(chan struct{})
	close(ready)
	mge := &MultiGroupEngine{
		baseCtx:            context.Background(),
		pauseCh:            ready,
		pauseChanged:       make(chan struct{}),
		baseTargetDuration: time.Minute,
		targetDuration:     time.Minute,
		controlsReady:      true,
		runStartedAt:       time.Now(),
	}
	if err := mge.Pause(); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	mge.mu.Lock()
	mge.pauseStartTime = time.Now().Add(-30 * time.Second)
	mge.mu.Unlock()
	if err := mge.Resume(); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	mge.mu.Lock()
	compensation := mge.pauseCompensation
	mge.mu.Unlock()
	if compensation < 30*time.Second {
		t.Fatalf("pause compensation = %s, want at least 30s", compensation)
	}

	if err := mge.AdjustDuration(2 * time.Minute); err != nil {
		t.Fatalf("AdjustDuration: %v", err)
	}
	if got, want := mge.GetTargetDuration(), 2*time.Minute+compensation; got != want {
		t.Fatalf("target duration after adjust = %s, want %s", got, want)
	}
}

func TestGracefulStopBeforeRunIsLatched(t *testing.T) {
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups: []GroupConfig{{
			Name:   "group",
			Script: &dsl.Script{Name: "script", Protocol: "stub", Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}}},
			VUsers: 1,
		}},
		Duration: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	mge.GracefulStop()
	result, err := mge.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.TotalTransactions != 0 {
		t.Fatalf("pre-run graceful stop emitted %d transactions", result.TotalTransactions)
	}
}

func TestPauseBeforeRunIsLatched(t *testing.T) {
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups: []GroupConfig{{
			Name:   "group",
			Script: &dsl.Script{Name: "script", Protocol: "stub", Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}}},
			VUsers: 1,
		}},
		Duration: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	if err := mge.Pause(); err != nil {
		t.Fatalf("Pause before Run: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, runErr := mge.Run(context.Background())
		done <- runErr
	}()
	select {
	case <-mge.Ready():
	case <-time.After(time.Second):
		t.Fatal("engine did not become ready")
	}
	select {
	case err := <-done:
		t.Fatalf("paused engine finished before resume: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := mge.Resume(); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("resumed engine did not finish")
	}
}

func TestPauseResumeOpenWorkloadWithoutClosedModelRateLimiter(t *testing.T) {
	script := &dsl.Script{
		Name:     "open-workload",
		Protocol: "stub",
		Config: dsl.ScriptConfig{
			RunLogic: dsl.RunLogicConfig{WorkloadModel: "open"},
			Pacing:   dsl.PacingConfig{Type: "tps", TPS: 10},
		},
		Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}},
	}
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups:   []GroupConfig{{Name: "group", Script: script, VUsers: 1}},
		Duration: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, runErr := mge.Run(ctx)
		done <- runErr
	}()
	select {
	case <-mge.Ready():
	case <-time.After(time.Second):
		cancel()
		t.Fatal("engine did not become ready")
	}

	// Open workloads have no per-group TokenBucket. Pause and Resume must use
	// the shared execution gate without dereferencing that nil limiter.
	if err := mge.Pause(); err != nil {
		cancel()
		t.Fatalf("Pause: %v", err)
	}
	if err := mge.Resume(); err != nil {
		cancel()
		t.Fatalf("Resume: %v", err)
	}
	cancel()
	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatalf("Run: %v", runErr)
		}
	case <-time.After(time.Second):
		t.Fatal("engine did not stop")
	}
}

func TestScaleVUsersFailsPromptlyBeforeReady(t *testing.T) {
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups: []GroupConfig{{
			Name:   "group",
			Script: &dsl.Script{Name: "script", Protocol: "stub", Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}}},
			VUsers: 1,
		}},
		Duration:    50 * time.Millisecond,
		PreBaseline: 150 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, runErr := mge.Run(context.Background())
		done <- runErr
	}()

	deadline := time.Now().Add(time.Second)
	for {
		mge.mu.Lock()
		initialized := mge.baseCtx != nil
		mge.mu.Unlock()
		if initialized {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("engine did not initialize")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-mge.Ready():
		t.Fatal("engine unexpectedly ready during pre-baseline")
	default:
	}
	started := time.Now()
	if err := mge.ScaleVUsers("group", 2); err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("ScaleVUsers before Ready error = %v, want engine not running", err)
	}
	if elapsed := time.Since(started); elapsed > 50*time.Millisecond {
		t.Fatalf("ScaleVUsers before Ready took %s, want prompt rejection", elapsed)
	}
	if got := mge.GetGroupVUsers()["group"]; got != 1 {
		t.Fatalf("group target changed before Ready: got %d, want 1", got)
	}

	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatalf("Run: %v", runErr)
		}
	case <-time.After(time.Second):
		t.Fatal("engine did not finish")
	}
}

func TestSleepActiveFreezesWhilePaused(t *testing.T) {
	baseCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	openGate := make(chan struct{})
	close(openGate)
	mge := &MultiGroupEngine{
		baseCtx:            baseCtx,
		controlsReady:      true,
		pauseCh:            openGate,
		pauseChanged:       make(chan struct{}),
		runStartedAt:       time.Now(),
		baseTargetDuration: time.Second,
		targetDuration:     time.Second,
	}

	done := make(chan struct{})
	go func() {
		mge.SleepActive(baseCtx, 80*time.Millisecond)
		close(done)
	}()
	time.Sleep(20 * time.Millisecond)
	if err := mge.Pause(); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	select {
	case <-done:
		t.Fatal("active-time sleep expired during pause")
	case <-time.After(100 * time.Millisecond):
	}
	if err := mge.Resume(); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	select {
	case <-done:
	case <-time.After(150 * time.Millisecond):
		t.Fatal("active-time sleep did not finish after resume")
	}
}

func TestScaleVUsersRollsBackWhenProtocolInitFails(t *testing.T) {
	rejectScaleInit.Store(false)
	defer rejectScaleInit.Store(false)
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups: []GroupConfig{{
			Name: "group",
			Script: &dsl.Script{
				Name:     "script",
				Protocol: "scale-init-failure-test",
				Actions:  []dsl.Action{{Name: "request", Type: "request", Target: "/"}},
			},
			VUsers: 1,
		}},
		Duration: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, runErr := mge.Run(ctx)
		done <- runErr
	}()
	select {
	case <-mge.Ready():
	case <-time.After(time.Second):
		t.Fatal("engine did not become ready")
	}
	waitForActiveVUsers(t, mge, 1)

	rejectScaleInit.Store(true)
	if err := mge.ScaleVUsers("group", 2); err == nil || !strings.Contains(err.Error(), "injected init failure") {
		t.Fatalf("ScaleVUsers error = %v, want injected init failure", err)
	}
	rejectScaleInit.Store(false)
	if got := mge.GetGroupVUsers()["group"]; got != 1 {
		t.Fatalf("group target after failed scale = %d, want 1", got)
	}
	waitForActiveVUsers(t, mge, 1)
	cancel()
	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatalf("Run: %v", runErr)
		}
	case <-time.After(time.Second):
		t.Fatal("engine did not stop")
	}
}

func TestMultiGroupRunFailsWhenConfiguredVUserCannotInitialize(t *testing.T) {
	rejectScaleInit.Store(true)
	defer rejectScaleInit.Store(false)
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups: []GroupConfig{{
			Name:   "group",
			Script: &dsl.Script{Name: "script", Protocol: "scale-init-failure-test", Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}}},
			VUsers: 1,
		}},
		Duration: time.Second,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	result, runErr := mge.Run(context.Background())
	if runErr == nil || !strings.Contains(runErr.Error(), "injected init failure") {
		t.Fatalf("Run error = %v, want init failure", runErr)
	}
	if result != nil {
		t.Fatalf("Run result = %#v, want nil on initialization failure", result)
	}
	if got := mge.activeVUserCountByGroup()["group"]; got != 0 {
		t.Fatalf("active vusers after failed startup = %d, want 0", got)
	}
}

func failingOpenWorkloadScript() *dsl.Script {
	return &dsl.Script{
		Name:     "failing-open",
		Protocol: "scale-init-failure-test",
		Config: dsl.ScriptConfig{
			RunLogic: dsl.RunLogicConfig{WorkloadModel: "open", Iterations: 1},
			Pacing:   dsl.PacingConfig{Type: "tps", TPS: 100},
		},
		Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}},
	}
}

func TestMultiGroupOpenWorkloadFailsWhenNoArrivalWorkerInitializes(t *testing.T) {
	rejectScaleInit.Store(true)
	defer rejectScaleInit.Store(false)
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups:   []GroupConfig{{Name: "open", Script: failingOpenWorkloadScript(), VUsers: 2}},
		Duration: time.Second,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	result, runErr := mge.Run(context.Background())
	if runErr == nil || !strings.Contains(runErr.Error(), "could not initialize any arrival worker") {
		t.Fatalf("Run error = %v, want open worker initialization failure", runErr)
	}
	if result != nil {
		t.Fatalf("Run result = %#v, want nil", result)
	}
}

func TestLegacyOpenWorkloadFailsWhenNoArrivalWorkerInitializes(t *testing.T) {
	rejectScaleInit.Store(true)
	defer rejectScaleInit.Store(false)
	eng, err := New(Config{Script: failingOpenWorkloadScript(), VUsers: 2, Duration: time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	result, runErr := eng.Run(context.Background())
	if runErr == nil || !strings.Contains(runErr.Error(), "could not initialize any arrival worker") {
		t.Fatalf("Run error = %v, want open worker initialization failure", runErr)
	}
	if result != nil {
		t.Fatalf("Run result = %#v, want nil", result)
	}
}

func TestLegacyClosedWorkloadFailsWhenConfiguredVUserCannotInitialize(t *testing.T) {
	rejectScaleInit.Store(true)
	defer rejectScaleInit.Store(false)
	script := &dsl.Script{
		Name:     "failing-closed",
		Protocol: "scale-init-failure-test",
		Actions:  []dsl.Action{{Name: "request", Type: "request", Target: "/"}},
	}
	eng, err := New(Config{Script: script, VUsers: 2, Duration: time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	result, runErr := eng.Run(context.Background())
	if runErr == nil || !strings.Contains(runErr.Error(), "injected init failure") {
		t.Fatalf("Run error = %v, want configured vuser initialization failure", runErr)
	}
	if result != nil {
		t.Fatalf("Run result = %#v, want nil", result)
	}
}

func TestVUserIDPoolReusesReleasedIDsAndStaysBounded(t *testing.T) {
	mge := &MultiGroupEngine{
		vuserIDSpan:   3,
		vuserIDsInUse: make(map[int]struct{}),
	}
	for want := 0; want < 3; want++ {
		got, err := mge.allocateVUserID()
		if err != nil || got != want {
			t.Fatalf("allocation %d = (%d, %v), want (%d, nil)", want, got, err, want)
		}
	}
	if _, err := mge.allocateVUserID(); err == nil || !strings.Contains(err.Error(), "namespace exhausted") {
		t.Fatalf("exhausted allocation error = %v", err)
	}

	mge.mu.Lock()
	mge.releaseVUserIDLocked(1)
	mge.mu.Unlock()
	got, err := mge.allocateVUserID()
	if err != nil || got != 1 {
		t.Fatalf("allocation after release = (%d, %v), want (1, nil)", got, err)
	}
}
