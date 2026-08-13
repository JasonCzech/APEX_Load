package engine

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

const atOnceAdmissionProtocolName = "at-once-startup-admission-test"

type atOnceAdmissionState struct {
	initStarted chan struct{}
	releaseInit chan struct{}
	failInit    bool
	startOnce   sync.Once
	releaseOnce sync.Once
	initCalls   atomic.Int64
	mainCalls   atomic.Int64
}

func (s *atOnceAdmissionState) release() {
	s.releaseOnce.Do(func() { close(s.releaseInit) })
}

type atOnceAdmissionProtocol struct {
	state *atOnceAdmissionState
}

func (*atOnceAdmissionProtocol) Name() string { return atOnceAdmissionProtocolName }

func (p *atOnceAdmissionProtocol) Init(context.Context, map[string]any) error {
	if p.state == nil || p.state.initCalls.Add(1) != 1 {
		return nil
	}
	p.state.startOnce.Do(func() { close(p.state.initStarted) })
	// Deliberately ignore cancellation. The lifecycle contract must recheck its
	// boundary after an adapter returns and must not admit main users meanwhile.
	<-p.state.releaseInit
	if p.state.failInit {
		return errors.New("injected at-once startup failure")
	}
	return nil
}

func (p *atOnceAdmissionProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	if p.state != nil && action.Name == "main" {
		p.state.mainCalls.Add(1)
	}
	return &protocol.Result{ActionName: action.Name, Success: true, Duration: time.Millisecond}, nil
}

func (*atOnceAdmissionProtocol) Teardown(context.Context) error    { return nil }
func (*atOnceAdmissionProtocol) HealthCheck(context.Context) error { return nil }

var atOnceAdmissionCurrent struct {
	sync.Mutex
	state *atOnceAdmissionState
}

func init() {
	protocol.Register(atOnceAdmissionProtocolName, func() protocol.Protocol {
		atOnceAdmissionCurrent.Lock()
		defer atOnceAdmissionCurrent.Unlock()
		return &atOnceAdmissionProtocol{state: atOnceAdmissionCurrent.state}
	})
}

func installAtOnceAdmissionState(t *testing.T, fail bool) *atOnceAdmissionState {
	t.Helper()
	state := &atOnceAdmissionState{
		initStarted: make(chan struct{}),
		releaseInit: make(chan struct{}),
		failInit:    fail,
	}
	atOnceAdmissionCurrent.Lock()
	atOnceAdmissionCurrent.state = state
	atOnceAdmissionCurrent.Unlock()
	t.Cleanup(func() {
		state.release()
		atOnceAdmissionCurrent.Lock()
		if atOnceAdmissionCurrent.state == state {
			atOnceAdmissionCurrent.state = nil
		}
		atOnceAdmissionCurrent.Unlock()
	})
	return state
}

func atOnceAdmissionScript() *dsl.Script {
	return &dsl.Script{
		Name:     "at-once startup admission",
		Protocol: atOnceAdmissionProtocolName,
		Config:   dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}},
		Init:     []dsl.Action{{Name: "setup", Type: "request", Target: "/setup"}},
		Actions:  []dsl.Action{{Name: "main", Type: "request", Target: "/main"}},
	}
}

func waitForEngineRun(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("engine run did not finish")
		return nil
	}
}

func TestScaleVUsersReturnsPromptlyWhileStartupPaused(t *testing.T) {
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups: []GroupConfig{{
			Name:   "buyers",
			Script: &dsl.Script{Name: "paused startup", Protocol: "stub", Actions: []dsl.Action{{Name: "main", Type: "request", Target: "/"}}},
			VUsers: 1,
		}},
		Duration:    5 * time.Second,
		StartPaused: true,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, runErr := mge.Run(context.Background())
		done <- runErr
	}()
	select {
	case <-mge.Ready():
	case <-time.After(time.Second):
		t.Fatal("start-paused engine did not become ready")
	}

	started := time.Now()
	err = mge.ScaleVUsers("buyers", 2)
	if !errors.Is(err, ErrInitialRampInProgress) {
		t.Fatalf("ScaleVUsers error = %v, want initial-ramp retry error", err)
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("ScaleVUsers blocked %s while startup was paused", elapsed)
	}

	if err := mge.Resume(); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	waitForActiveVUsers(t, mge, 1)
	mge.Stop()
	if runErr := waitForEngineRun(t, done); runErr != nil {
		t.Fatalf("Run after stop: %v", runErr)
	}
}

func TestNaturalDeadlineFencesDeferredClosedLaunch(t *testing.T) {
	state := installAtOnceAdmissionState(t, false)
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups: []GroupConfig{{
			Name:         "buyers",
			Script:       atOnceAdmissionScript(),
			VUsers:       1,
			InitStrategy: "at_once",
		}},
		Duration: 40 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, runErr := mge.Run(context.Background())
		done <- runErr
	}()
	select {
	case <-state.initStarted:
	case <-time.After(time.Second):
		t.Fatal("at-once adapter did not enter Init")
	}

	deadline := time.Now().Add(time.Second)
	for {
		mge.mu.Lock()
		launchStopped := mge.launchCtx != nil && mge.launchCtx.Err() != nil
		mge.mu.Unlock()
		if launchStopped {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("natural duration did not close launch admission")
		}
		time.Sleep(time.Millisecond)
	}
	state.release()
	if runErr := waitForEngineRun(t, done); runErr != nil {
		t.Fatalf("Run: %v", runErr)
	}
	if got := state.initCalls.Load(); got != 1 {
		t.Fatalf("protocol Init calls = %d, want only the shared at-once adapter", got)
	}
	if got := state.mainCalls.Load(); got != 0 {
		t.Fatalf("main actions after natural deadline = %d, want 0", got)
	}
	if got := mge.activeVUserCountByGroup()["buyers"]; got != 0 {
		t.Fatalf("active VUsers after deadline = %d, want 0", got)
	}
}

func TestZeroStartAtOnceScaleWaitsForSharedInit(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "success"
		if fail {
			name = "failure"
		}
		t.Run(name, func(t *testing.T) {
			state := installAtOnceAdmissionState(t, fail)
			mge, err := NewMultiGroup(MultiGroupConfig{
				Groups: []GroupConfig{{
					Name:            "buyers",
					Script:          atOnceAdmissionScript(),
					VUsers:          0,
					AllowZeroVUsers: true,
					InitStrategy:    "at_once",
				}},
				Duration: 5 * time.Second,
			})
			if err != nil {
				t.Fatalf("NewMultiGroup: %v", err)
			}
			done := make(chan error, 1)
			go func() {
				_, runErr := mge.Run(context.Background())
				done <- runErr
			}()
			select {
			case <-state.initStarted:
			case <-time.After(time.Second):
				t.Fatal("shared at-once Init did not start")
			}

			started := time.Now()
			if scaleErr := mge.ScaleVUsers("buyers", 1); !errors.Is(scaleErr, ErrInitialRampInProgress) {
				t.Fatalf("ScaleVUsers before shared Init = %v, want initial-ramp retry error", scaleErr)
			}
			if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
				t.Fatalf("ScaleVUsers blocked %s behind shared Init", elapsed)
			}
			if got := state.mainCalls.Load(); got != 0 {
				t.Fatalf("main actions before shared Init completed = %d", got)
			}

			state.release()
			if fail {
				runErr := waitForEngineRun(t, done)
				if runErr == nil || !strings.Contains(runErr.Error(), "injected at-once startup failure") {
					t.Fatalf("Run error = %v, want injected startup failure", runErr)
				}
				if scaleErr := mge.ScaleVUsers("buyers", 1); scaleErr == nil {
					t.Fatal("ScaleVUsers succeeded after shared Init failed")
				}
				if got := state.initCalls.Load(); got != 1 {
					t.Fatalf("protocol Init calls after failure = %d, want 1", got)
				}
				if got := state.mainCalls.Load(); got != 0 {
					t.Fatalf("main actions after shared Init failure = %d, want 0", got)
				}
				return
			}

			var scaleErr error
			deadline := time.Now().Add(time.Second)
			for time.Now().Before(deadline) {
				scaleErr = mge.ScaleVUsers("buyers", 1)
				if scaleErr == nil {
					break
				}
				if !errors.Is(scaleErr, ErrInitialRampInProgress) {
					break
				}
				time.Sleep(time.Millisecond)
			}
			if scaleErr != nil {
				t.Fatalf("ScaleVUsers after shared Init: %v", scaleErr)
			}
			deadline = time.Now().Add(time.Second)
			for state.mainCalls.Load() == 0 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if state.mainCalls.Load() == 0 {
				t.Fatal("scaled VUser did not run after shared Init succeeded")
			}
			mge.Stop()
			if runErr := waitForEngineRun(t, done); runErr != nil {
				t.Fatalf("Run after stop: %v", runErr)
			}
		})
	}
}

func TestAtOnceOpenWorkloadUsesCommonLoadEpoch(t *testing.T) {
	state := installAtOnceAdmissionState(t, false)
	script := atOnceAdmissionScript()
	script.Config.RunLogic.WorkloadModel = "open"
	script.Config.RunLogic.Iterations = 5
	script.Config.RunLogic.MaxInFlight = 10
	script.Config.Pacing = dsl.PacingConfig{Type: "tps", TPS: 10}
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups: []GroupConfig{{
			Name:         "buyers",
			Script:       script,
			VUsers:       1,
			InitStrategy: "at_once",
		}},
		Duration: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, runErr := mge.Run(context.Background())
		done <- runErr
	}()
	joined := false
	t.Cleanup(func() {
		state.release()
		mge.Stop()
		if !joined {
			_ = waitForEngineRun(t, done)
		}
	})
	select {
	case <-state.initStarted:
	case <-time.After(time.Second):
		t.Fatal("shared at-once Init did not start")
	}

	// At 10 TPS, three deadlines (0ms, 100ms, 200ms) have elapsed by the time
	// the shared lifecycle admits the group. A scheduler anchored at gate-open
	// would emit only one arrival in the following 50ms.
	time.Sleep(250 * time.Millisecond)
	state.release()
	deadline := time.Now().Add(50 * time.Millisecond)
	for state.mainCalls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := state.mainCalls.Load(); got < 2 {
		t.Fatalf("main actions immediately after delayed gate = %d, want catch-up from shared load epoch", got)
	}
	mge.Stop()
	if runErr := waitForEngineRun(t, done); runErr != nil {
		t.Fatalf("Run after stop: %v", runErr)
	}
	joined = true
}
