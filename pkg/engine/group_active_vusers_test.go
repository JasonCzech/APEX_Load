package engine

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

const slowInitRampProtocolName = "slow-init-ramp-active-vusers-test"

type slowInitRampState struct {
	initCalls     atomic.Int64
	secondStarted chan struct{}
	releaseSecond chan struct{}
	releaseOnce   sync.Once
}

func newSlowInitRampState() *slowInitRampState {
	return &slowInitRampState{
		secondStarted: make(chan struct{}),
		releaseSecond: make(chan struct{}),
	}
}

func (s *slowInitRampState) release() {
	s.releaseOnce.Do(func() { close(s.releaseSecond) })
}

type slowInitRampProtocol struct {
	state *slowInitRampState
}

func (*slowInitRampProtocol) Name() string { return slowInitRampProtocolName }

func (p *slowInitRampProtocol) Init(ctx context.Context, _ map[string]any) error {
	if p.state == nil || p.state.initCalls.Add(1) != 2 {
		return nil
	}
	close(p.state.secondStarted)
	select {
	case <-p.state.releaseSecond:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (*slowInitRampProtocol) Execute(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
	<-ctx.Done()
	return &protocol.Result{ActionName: action.Name, Success: true}, nil
}

func (*slowInitRampProtocol) Teardown(context.Context) error    { return nil }
func (*slowInitRampProtocol) HealthCheck(context.Context) error { return nil }

var slowInitRampCurrent struct {
	sync.Mutex
	state *slowInitRampState
}

func init() {
	protocol.Register(slowInitRampProtocolName, func() protocol.Protocol {
		slowInitRampCurrent.Lock()
		defer slowInitRampCurrent.Unlock()
		return &slowInitRampProtocol{state: slowInitRampCurrent.state}
	})
}

func installSlowInitRampState(t *testing.T) *slowInitRampState {
	t.Helper()
	state := newSlowInitRampState()
	slowInitRampCurrent.Lock()
	slowInitRampCurrent.state = state
	slowInitRampCurrent.Unlock()
	t.Cleanup(func() {
		state.release()
		slowInitRampCurrent.Lock()
		if slowInitRampCurrent.state == state {
			slowInitRampCurrent.state = nil
		}
		slowInitRampCurrent.Unlock()
	})
	return state
}

func waitForGroupActiveSnapshot(t *testing.T, mge *MultiGroupEngine, after time.Time, want int64) MetricsSnapshot {
	t.Helper()
	deadline := time.Now().Add(2500 * time.Millisecond)
	for time.Now().Before(deadline) {
		snapshot, ok := mge.LatestSnapshot()
		if ok && snapshot.Timestamp.After(after) && snapshot.ActiveVUsers == want {
			group := snapshot.ByGroup["buyers"]
			if group != nil && group.ActiveVUsersReported && group.ActiveVUsers == want {
				return snapshot
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for aggregate/group active VUsers = %d", want)
	return MetricsSnapshot{}
}

func TestMultiGroupActiveVUsersExcludeInitializingRampSteps(t *testing.T) {
	state := installSlowInitRampState(t)
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups: []GroupConfig{{
			Name: "buyers",
			Script: &dsl.Script{
				Name:     "slow ramp init",
				Protocol: slowInitRampProtocolName,
				Actions:  []dsl.Action{{Name: "hold", Type: "request", Target: "/"}},
			},
			VUsers: 2,
			RampUp: 4 * time.Second,
		}},
		Duration:     5 * time.Second,
		PostBaseline: time.Millisecond,
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
	joined := false
	t.Cleanup(func() {
		state.release()
		cancel()
		if joined {
			return
		}
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Errorf("slow-init ramp did not stop during cleanup")
		}
	})

	select {
	case <-state.secondStarted:
	case <-time.After(time.Second):
		t.Fatal("second ramp VUser did not enter protocol initialization")
	}

	counts := mge.activeVUserCountByGroup()
	if counts["buyers"] != 0 {
		t.Fatalf("active VUsers while configured VUsers are being prepared = %d, want 0", counts["buyers"])
	}
	mge.mu.Lock()
	reserved := len(mge.vuserCancels)
	running := len(mge.vuserRunning)
	mge.mu.Unlock()
	if reserved != 2 || running != 0 {
		t.Fatalf("blocked ramp bookkeeping = reserved %d / running %d, want 2 / 0", reserved, running)
	}

	blockedSnapshot := waitForGroupActiveSnapshot(t, mge, time.Time{}, 0)
	state.release()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if mge.activeVUserCountByGroup()["buyers"] == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if got := mge.activeVUserCountByGroup()["buyers"]; got != 1 {
		t.Fatalf("active VUsers at first ramp step = %d, want 1", got)
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if mge.activeVUserCountByGroup()["buyers"] == 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if got := mge.activeVUserCountByGroup()["buyers"]; got != 2 {
		t.Fatalf("active VUsers after second init = %d, want 2", got)
	}
	waitForGroupActiveSnapshot(t, mge, blockedSnapshot.Timestamp, 2)

	mge.Stop()
	select {
	case runErr := <-done:
		joined = true
		if runErr != nil {
			t.Fatalf("Run after stop: %v", runErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("slow-init ramp did not stop")
	}

	if got := mge.activeVUserCountByGroup()["buyers"]; got != 0 {
		t.Fatalf("active VUsers after cleanup = %d, want 0", got)
	}
	mge.mu.Lock()
	defer mge.mu.Unlock()
	if len(mge.vuserRunning) != 0 || len(mge.vuserCancels) != 0 || len(mge.vuserIDsInUse) != 0 {
		t.Fatalf("VUser state after cleanup = running %d / reserved %d / ids %d, want 0 / 0 / 0",
			len(mge.vuserRunning), len(mge.vuserCancels), len(mge.vuserIDsInUse))
	}
}
