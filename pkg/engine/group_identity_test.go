package engine

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

const groupIdentityCaptureProtocolName = "group-identity-capture-test"

type groupIdentityCaptureState struct {
	mu                sync.Mutex
	counts            map[string]map[string]int
	total             int
	expected          int
	reached           chan struct{}
	once              sync.Once
	retireTarget      string
	retireStarted     chan struct{}
	releaseRetirement chan struct{}
	retireOnce        sync.Once
	releaseOnce       sync.Once
}

func newGroupIdentityCaptureState(expected int) *groupIdentityCaptureState {
	return &groupIdentityCaptureState{
		counts:            make(map[string]map[string]int),
		expected:          expected,
		reached:           make(chan struct{}),
		retireStarted:     make(chan struct{}),
		releaseRetirement: make(chan struct{}),
	}
}

func (s *groupIdentityCaptureState) record(group, target string) {
	s.mu.Lock()
	groupCounts := s.counts[group]
	if groupCounts == nil {
		groupCounts = make(map[string]int)
		s.counts[group] = groupCounts
	}
	if groupCounts[target] == 0 {
		s.total++
	}
	groupCounts[target]++
	reached := s.total >= s.expected
	s.mu.Unlock()
	if reached {
		s.once.Do(func() { close(s.reached) })
	}
}

func (s *groupIdentityCaptureState) snapshot(group string) map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]int, len(s.counts[group]))
	for target, count := range s.counts[group] {
		out[target] = count
	}
	return out
}

type groupIdentityCaptureProtocol struct {
	state *groupIdentityCaptureState
	group string
}

func (*groupIdentityCaptureProtocol) Name() string { return groupIdentityCaptureProtocolName }

func (p *groupIdentityCaptureProtocol) Init(_ context.Context, config map[string]any) error {
	p.group = fmt.Sprint(config["group_name"])
	return nil
}

func (p *groupIdentityCaptureProtocol) Execute(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
	if p.state != nil && action != nil {
		p.state.record(p.group, action.Target)
	}
	<-ctx.Done()
	if p.state != nil && action != nil && action.Target == p.state.retireTarget {
		p.state.retireOnce.Do(func() { close(p.state.retireStarted) })
		<-p.state.releaseRetirement
	}
	return &protocol.Result{ActionName: action.Name, Success: true}, nil
}

func (*groupIdentityCaptureProtocol) Teardown(context.Context) error    { return nil }
func (*groupIdentityCaptureProtocol) HealthCheck(context.Context) error { return nil }

var groupIdentityCaptureCurrent struct {
	sync.Mutex
	state *groupIdentityCaptureState
}

func init() {
	protocol.Register(groupIdentityCaptureProtocolName, func() protocol.Protocol {
		groupIdentityCaptureCurrent.Lock()
		defer groupIdentityCaptureCurrent.Unlock()
		return &groupIdentityCaptureProtocol{state: groupIdentityCaptureCurrent.state}
	})
}

func installGroupIdentityCapture(t *testing.T, expected int) *groupIdentityCaptureState {
	t.Helper()
	state := newGroupIdentityCaptureState(expected)
	groupIdentityCaptureCurrent.Lock()
	groupIdentityCaptureCurrent.state = state
	groupIdentityCaptureCurrent.Unlock()
	t.Cleanup(func() {
		groupIdentityCaptureCurrent.Lock()
		if groupIdentityCaptureCurrent.state == state {
			groupIdentityCaptureCurrent.state = nil
		}
		groupIdentityCaptureCurrent.Unlock()
	})
	return state
}

func identityCaptureScript(name string) *dsl.Script {
	return &dsl.Script{
		Name:     name,
		Protocol: groupIdentityCaptureProtocolName,
		Parameters: []dsl.Parameter{{
			Name: "vu",
			Type: "vuser_id",
		}},
		DataSources: []dsl.DataSource{{
			Name:           "users",
			Type:           "csv",
			InlineData:     "id\nrow-0\nrow-1\nrow-2\n",
			FirstRowHeader: true,
			Strategy:       "unique",
		}},
		Actions: []dsl.Action{{
			Name:   "capture",
			Type:   "request",
			Target: "/identity/{{param.vu}}/{{data.users.id}}",
		}},
	}
}

func runGroupIdentityEngine(t *testing.T, mge *MultiGroupEngine) (context.CancelFunc, <-chan struct{}, *error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	var runErr error
	go func() {
		_, runErr = mge.Run(ctx)
		close(finished)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(2 * time.Second):
			t.Errorf("identity capture engine did not stop during cleanup")
		}
	})
	return cancel, finished, &runErr
}

func waitForIdentityCaptures(t *testing.T, state *groupIdentityCaptureState) {
	t.Helper()
	select {
	case <-state.reached:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runtime VUser identity captures")
	}
}

func stopGroupIdentityEngine(t *testing.T, cancel context.CancelFunc, finished <-chan struct{}, runErr *error) {
	t.Helper()
	cancel()
	select {
	case <-finished:
		if *runErr != nil {
			t.Fatalf("Run after cancellation: %v", *runErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("identity capture engine did not stop after cancellation")
	}
}

func assertIdentityTargets(t *testing.T, got map[string]int) {
	t.Helper()
	want := []string{
		"/identity/1/row-0",
		"/identity/2/row-1",
		"/identity/3/row-2",
	}
	if len(got) != len(want) {
		t.Fatalf("captured targets = %#v, want exactly %v", got, want)
	}
	for _, target := range want {
		if got[target] == 0 {
			t.Fatalf("captured targets = %#v, missing %q", got, target)
		}
	}
}

func TestConcurrentGroupsUseDeterministicGroupLocalRuntimeIdentities(t *testing.T) {
	state := installGroupIdentityCapture(t, 6)
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups: []GroupConfig{
			{Name: "buyers", Script: identityCaptureScript("buyers"), VUsers: 3, RampUp: 150 * time.Millisecond},
			{Name: "sellers", Script: identityCaptureScript("sellers"), VUsers: 3, RampUp: 150 * time.Millisecond},
		},
		Duration: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	cancel, finished, runErr := runGroupIdentityEngine(t, mge)

	waitForIdentityCaptures(t, state)
	assertIdentityTargets(t, state.snapshot("buyers"))
	assertIdentityTargets(t, state.snapshot("sellers"))
	stopGroupIdentityEngine(t, cancel, finished, runErr)

	mge.mu.Lock()
	defer mge.mu.Unlock()
	if len(mge.vuserIDsInUse) != 0 || len(mge.vuserIdentities) != 0 || len(mge.vuserCancels) != 0 {
		t.Fatalf("identity state after stop = handles:%d identities:%d vusers:%d, want 0/0/0", len(mge.vuserIDsInUse), len(mge.vuserIdentities), len(mge.vuserCancels))
	}
	for name, pool := range mge.groupVUserIDPools {
		if pool.allocated != 0 || pool.next != 3 || len(pool.free) != 3 {
			t.Fatalf("group %q identity pool after stop = allocated:%d next:%d free:%d, want 0/3/3", name, pool.allocated, pool.next, len(pool.free))
		}
	}
}

func TestScaledGroupReusesReleasedRuntimeIdentity(t *testing.T) {
	state := installGroupIdentityCapture(t, 3)
	state.retireTarget = "/identity/3/row-2"
	defer state.releaseOnce.Do(func() { close(state.releaseRetirement) })
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups: []GroupConfig{{
			Name:            "buyers",
			Script:          identityCaptureScript("buyers"),
			VUsers:          0,
			AllowZeroVUsers: true,
		}},
		Duration: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	cancel, finished, runErr := runGroupIdentityEngine(t, mge)
	select {
	case <-mge.Ready():
	case <-time.After(time.Second):
		t.Fatal("identity capture engine did not become ready")
	}

	if err := scaleAfterInitialRamp(t, mge, "buyers", 3); err != nil {
		t.Fatalf("scale to three VUsers: %v", err)
	}
	waitForIdentityCaptures(t, state)
	assertIdentityTargets(t, state.snapshot("buyers"))

	if err := mge.ScaleVUsers("buyers", 2); err != nil {
		t.Fatalf("scale down to two VUsers: %v", err)
	}
	select {
	case <-state.retireStarted:
	case <-time.After(time.Second):
		t.Fatal("scaled-down VUser did not enter its gated retirement")
	}

	rescaled := make(chan error, 1)
	go func() { rescaled <- mge.ScaleVUsers("buyers", 3) }()
	select {
	case err := <-rescaled:
		t.Fatalf("scale reversal completed before the retired identity was released: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if captures := state.snapshot("buyers"); captures["/identity/4/row-0"] != 0 {
		t.Fatalf("scale reversal allocated a sparse duplicate identity: captures = %#v", captures)
	}

	state.releaseOnce.Do(func() { close(state.releaseRetirement) })
	select {
	case err := <-rescaled:
		if err != nil {
			t.Fatalf("scale back to three VUsers: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("scale reversal did not resume after the retired identity was released")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if state.snapshot("buyers")["/identity/3/row-2"] >= 2 {
			stopGroupIdentityEngine(t, cancel, finished, runErr)
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("released runtime identity was not reused: captures = %#v", state.snapshot("buyers"))
}
