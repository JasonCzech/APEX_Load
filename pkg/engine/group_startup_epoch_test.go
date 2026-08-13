package engine

import (
	"context"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

const startupEpochProtocolName = "startup-epoch-test"

type startupEpochProtocol struct {
	state *startupEpochState
	group string
}

type startupEpochState struct {
	laterInitStarted chan struct{}
	releaseLaterInit chan struct{}
	firstAction      chan startupEpochAction
	allActions       chan startupEpochAction
	allInits         chan startupEpochAction

	gateOnce    sync.Once
	releaseOnce sync.Once
	mu          sync.Mutex
	actionTimes map[string]time.Time
}

type startupEpochAction struct {
	group string
	at    time.Time
}

var (
	startupEpochMu           sync.Mutex
	startupEpochStateCurrent *startupEpochState
)

func (*startupEpochProtocol) Name() string { return startupEpochProtocolName }

func (p *startupEpochProtocol) Init(ctx context.Context, config map[string]any) error {
	if group, ok := config["group_name"].(string); ok {
		p.group = group
	}
	if p.state != nil {
		p.state.allInits <- startupEpochAction{group: p.group, at: time.Now()}
	}
	if p.state == nil || p.group != "later" {
		return nil
	}

	var gateErr error
	p.state.gateOnce.Do(func() {
		close(p.state.laterInitStarted)
		select {
		case <-ctx.Done():
			gateErr = ctx.Err()
		case <-p.state.releaseLaterInit:
		}
	})
	return gateErr
}

func (p *startupEpochProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	if p.state != nil {
		now := time.Now()
		p.state.allActions <- startupEpochAction{group: p.group, at: now}
		p.state.mu.Lock()
		if _, exists := p.state.actionTimes[p.group]; !exists {
			p.state.actionTimes[p.group] = now
			p.state.firstAction <- startupEpochAction{group: p.group, at: now}
		}
		p.state.mu.Unlock()
	}
	return &protocol.Result{ActionName: action.Name, Success: true, Duration: time.Millisecond}, nil
}

func (*startupEpochProtocol) Teardown(context.Context) error    { return nil }
func (*startupEpochProtocol) HealthCheck(context.Context) error { return nil }

func init() {
	protocol.Register(startupEpochProtocolName, func() protocol.Protocol {
		startupEpochMu.Lock()
		defer startupEpochMu.Unlock()
		return &startupEpochProtocol{state: startupEpochStateCurrent}
	})
}

func installStartupEpochState(t *testing.T) *startupEpochState {
	t.Helper()
	state := &startupEpochState{
		laterInitStarted: make(chan struct{}),
		releaseLaterInit: make(chan struct{}),
		firstAction:      make(chan startupEpochAction, 2),
		allActions:       make(chan startupEpochAction, 8),
		allInits:         make(chan startupEpochAction, 16),
		actionTimes:      make(map[string]time.Time),
	}
	startupEpochMu.Lock()
	startupEpochStateCurrent = state
	startupEpochMu.Unlock()
	t.Cleanup(func() {
		state.releaseOnce.Do(func() { close(state.releaseLaterInit) })
		startupEpochMu.Lock()
		if startupEpochStateCurrent == state {
			startupEpochStateCurrent = nil
		}
		startupEpochMu.Unlock()
	})
	return state
}

func TestMultiGroupStartupUsesCommonRampEpochAfterSlowInit(t *testing.T) {
	state := installStartupEpochState(t)
	script := func(name string) *dsl.Script {
		return &dsl.Script{
			Name:     name,
			Protocol: startupEpochProtocolName,
			Config: dsl.ScriptConfig{
				RunLogic: dsl.RunLogicConfig{Iterations: 1},
			},
			Actions: []dsl.Action{{Name: "main", Type: "request", Target: "/"}},
		}
	}
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups: []GroupConfig{
			{Name: "early", Script: script("early"), VUsers: 2, RampUp: 400 * time.Millisecond},
			{Name: "later", Script: script("later"), VUsers: 2, RampUp: 400 * time.Millisecond},
		},
		Duration: 3 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	joined := false
	go func() {
		_, runErr := mge.Run(ctx)
		done <- runErr
	}()
	t.Cleanup(func() {
		state.releaseOnce.Do(func() { close(state.releaseLaterInit) })
		cancel()
		if joined {
			return
		}
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("multi-group startup epoch run did not stop")
		}
	})

	select {
	case <-state.laterInitStarted:
	case <-time.After(time.Second):
		t.Fatal("later group did not enter its gated protocol initialization")
	}

	// The earlier group is fully prepared while the later group is blocked in
	// Init. No action may escape before all groups have passed startup admission.
	select {
	case action := <-state.firstAction:
		t.Fatalf("group %q started an action while later-group Init was blocked", action.group)
	case <-time.After(150 * time.Millisecond):
	}

	releasedAt := time.Now()
	state.releaseOnce.Do(func() { close(state.releaseLaterInit) })

	firstActions := make(map[string]time.Time, 2)
	deadline := time.After(time.Second)
	for len(firstActions) < 2 {
		select {
		case action := <-state.firstAction:
			firstActions[action.group] = action.at
		case <-deadline:
			t.Fatalf("first actions = %v, want both early and later groups", firstActions)
		}
	}
	for group, startedAt := range firstActions {
		if startedAt.Before(releasedAt) {
			t.Fatalf("group %q first action started at %s before Init release at %s", group, startedAt, releasedAt)
		}
	}

	skew := firstActions["early"].Sub(firstActions["later"])
	if skew < 0 {
		skew = -skew
	}
	if skew > 100*time.Millisecond {
		t.Fatalf("first-action ramp epoch skew = %s, want at most 100ms (times: %v)", skew, firstActions)
	}

	select {
	case runErr := <-done:
		joined = true
		if runErr != nil {
			t.Fatalf("Run: %v", runErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("multi-group startup epoch run did not finish")
	}
}

func TestRunAtPreparesBeforeFutureEpochAndAlignsGenerators(t *testing.T) {
	state := installStartupEpochState(t)
	script := func(name string) *dsl.Script {
		return &dsl.Script{
			Name:     name,
			Protocol: startupEpochProtocolName,
			Config: dsl.ScriptConfig{
				RunLogic: dsl.RunLogicConfig{Iterations: 1},
			},
			Actions: []dsl.Action{{Name: "main", Type: "request", Target: "/"}},
		}
	}
	newEngine := func(group string) *MultiGroupEngine {
		mge, err := NewMultiGroup(MultiGroupConfig{
			Groups: []GroupConfig{{
				Name: group, Script: script(group), VUsers: 2, RampUp: 200 * time.Millisecond,
			}},
			Duration: time.Second,
		})
		if err != nil {
			t.Fatalf("NewMultiGroup(%s): %v", group, err)
		}
		return mge
	}
	early := newEngine("early")
	later := newEngine("later")
	startAt := time.Now().Add(500 * time.Millisecond)
	done := make(chan error, 2)
	for _, mge := range []*MultiGroupEngine{early, later} {
		mge := mge
		go func() {
			_, runErr := mge.RunAt(context.Background(), startAt)
			done <- runErr
		}()
	}
	t.Cleanup(func() {
		state.releaseOnce.Do(func() { close(state.releaseLaterInit) })
		early.Stop()
		later.Stop()
	})

	select {
	case <-state.laterInitStarted:
		if !time.Now().Before(startAt) {
			t.Fatal("later generator did not begin preparation before the coordinated epoch")
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("later generator did not prepare during coordinated commit lead time")
	}
	state.releaseOnce.Do(func() { close(state.releaseLaterInit) })

	if wait := time.Until(startAt.Add(-75 * time.Millisecond)); wait > 0 {
		select {
		case action := <-state.firstAction:
			t.Fatalf("group %q started before coordinated epoch: %+v", action.group, action)
		case <-time.After(wait):
		}
	}

	firstActions := make(map[string]time.Time, 2)
	deadline := time.After(2 * time.Second)
	for len(firstActions) < 2 {
		select {
		case action := <-state.firstAction:
			firstActions[action.group] = action.at
		case <-deadline:
			t.Fatalf("first actions = %v, want both coordinated generators", firstActions)
		}
	}
	for group, at := range firstActions {
		if at.Before(startAt) {
			t.Fatalf("group %q action at %s preceded epoch %s", group, at, startAt)
		}
	}
	skew := firstActions["early"].Sub(firstActions["later"])
	if skew < 0 {
		skew = -skew
	}
	if skew > 75*time.Millisecond {
		t.Fatalf("coordinated generator skew = %s, want <= 75ms (times: %v)", skew, firstActions)
	}
	actionsByGroup := map[string][]time.Time{"early": {}, "later": {}}
	for total := 0; total < 4; total++ {
		select {
		case action := <-state.allActions:
			actionsByGroup[action.group] = append(actionsByGroup[action.group], action.at)
		case <-time.After(2 * time.Second):
			t.Fatalf("all coordinated actions = %v, want two per generator", actionsByGroup)
		}
	}
	for group := range actionsByGroup {
		sort.Slice(actionsByGroup[group], func(i, j int) bool { return actionsByGroup[group][i].Before(actionsByGroup[group][j]) })
		if len(actionsByGroup[group]) != 2 {
			t.Fatalf("group %q action times = %v, want two VUsers", group, actionsByGroup[group])
		}
		if actionsByGroup[group][1].Before(startAt.Add(70 * time.Millisecond)) {
			t.Fatalf("group %q second ramp action at %s, want near/after %s", group, actionsByGroup[group][1], startAt.Add(100*time.Millisecond))
		}
	}
	secondSkew := actionsByGroup["early"][1].Sub(actionsByGroup["later"][1])
	if secondSkew < 0 {
		secondSkew = -secondSkew
	}
	if secondSkew > 75*time.Millisecond {
		t.Fatalf("coordinated second-step skew = %s, want <= 75ms (times: %v)", secondSkew, actionsByGroup)
	}
	for range 2 {
		select {
		case runErr := <-done:
			if runErr != nil {
				t.Fatalf("RunAt: %v", runErr)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("coordinated RunAt did not finish")
		}
	}
}

func TestRunAtFailsClosedWhenPreparationMissesCoordinatedBarrier(t *testing.T) {
	state := installStartupEpochState(t)
	script := &dsl.Script{
		Name: "missed coordinated barrier", Protocol: startupEpochProtocolName,
		Config:  dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}},
		Actions: []dsl.Action{{Name: "main", Type: "request", Target: "/"}},
	}
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups:   []GroupConfig{{Name: "later", Script: script, VUsers: 1}},
		Duration: time.Second,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	startAt := time.Now().Add(100 * time.Millisecond)
	done := make(chan error, 1)
	go func() {
		_, runErr := mge.RunAt(context.Background(), startAt)
		done <- runErr
	}()
	select {
	case <-state.laterInitStarted:
	case <-time.After(time.Second):
		t.Fatal("protocol preparation did not begin before coordinated barrier")
	}
	if wait := time.Until(startAt.Add(50 * time.Millisecond)); wait > 0 {
		time.Sleep(wait)
	}
	state.releaseOnce.Do(func() { close(state.releaseLaterInit) })
	select {
	case runErr := <-done:
		if runErr == nil || !strings.Contains(runErr.Error(), "missed the execution barrier") {
			t.Fatalf("RunAt error = %v, want missed-barrier failure", runErr)
		}
	case <-time.After(time.Second):
		t.Fatal("missed-barrier RunAt did not fail promptly")
	}
	select {
	case action := <-state.firstAction:
		t.Fatalf("missed-barrier run emitted action: %+v", action)
	default:
	}
}

func TestRunAtHonorsFutureEpochAndPastWindowWithoutTraffic(t *testing.T) {
	script := &dsl.Script{
		Name:     "run-at epoch contract",
		Protocol: startupEpochProtocolName,
		Config:   dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}},
		Actions:  []dsl.Action{{Name: "main", Type: "request", Target: "/"}},
	}

	t.Run("future epoch", func(t *testing.T) {
		state := installStartupEpochState(t)
		mge, err := NewMultiGroup(MultiGroupConfig{
			Groups:   []GroupConfig{{Name: "early", Script: script, VUsers: 1}},
			Duration: 500 * time.Millisecond,
		})
		if err != nil {
			t.Fatalf("NewMultiGroup: %v", err)
		}
		startAt := time.Now().Add(150 * time.Millisecond)
		done := make(chan error, 1)
		go func() {
			_, runErr := mge.RunAt(context.Background(), startAt)
			done <- runErr
		}()
		select {
		case action := <-state.firstAction:
			t.Fatalf("action started before future epoch: %+v", action)
		case <-time.After(100 * time.Millisecond):
		}
		select {
		case action := <-state.firstAction:
			if action.at.Before(startAt) {
				t.Fatalf("action at %s preceded epoch %s", action.at, startAt)
			}
		case <-time.After(time.Second):
			t.Fatal("future-epoch action did not start")
		}
		mge.Stop()
		select {
		case runErr := <-done:
			if runErr != nil {
				t.Fatalf("RunAt: %v", runErr)
			}
		case <-time.After(time.Second):
			t.Fatal("future-epoch run did not stop")
		}
	})

	t.Run("stop before future epoch", func(t *testing.T) {
		state := installStartupEpochState(t)
		mge, err := NewMultiGroup(MultiGroupConfig{
			Groups:   []GroupConfig{{Name: "early", Script: script, VUsers: 1}},
			Duration: time.Second,
		})
		if err != nil {
			t.Fatalf("NewMultiGroup: %v", err)
		}
		done := make(chan struct {
			result *MultiGroupRunResult
			err    error
		}, 1)
		go func() {
			result, runErr := mge.RunAt(context.Background(), time.Now().Add(time.Second))
			done <- struct {
				result *MultiGroupRunResult
				err    error
			}{result: result, err: runErr}
		}()
		select {
		case <-mge.Ready():
		case <-time.After(time.Second):
			t.Fatal("future execution did not reach preparation readiness")
		}
		mge.Stop()
		select {
		case outcome := <-done:
			if outcome.err != nil {
				t.Fatalf("RunAt: %v", outcome.err)
			}
			if outcome.result == nil || outcome.result.Duration < 0 {
				t.Fatalf("pre-barrier result = %#v, want non-negative duration", outcome.result)
			}
		case <-time.After(time.Second):
			t.Fatal("pre-barrier Stop did not finish promptly")
		}
		select {
		case action := <-state.firstAction:
			t.Fatalf("pre-barrier stopped run emitted action: %+v", action)
		default:
		}
	})

	t.Run("past completed window", func(t *testing.T) {
		state := installStartupEpochState(t)
		for i := 0; i < 50; i++ {
			mge, err := NewMultiGroup(MultiGroupConfig{
				Groups:   []GroupConfig{{Name: "early", Script: script, VUsers: 1}},
				Duration: 20 * time.Millisecond,
			})
			if err != nil {
				t.Fatalf("NewMultiGroup: %v", err)
			}
			result, runErr := mge.RunAt(context.Background(), time.Now().Add(-time.Second))
			if runErr != nil {
				t.Fatalf("iteration %d RunAt: %v", i, runErr)
			}
			if result == nil || len(result.Groups) != 1 || result.Groups[0].Name != "early" {
				t.Fatalf("iteration %d result groups = %#v, want named early group", i, result)
			}
		}
		select {
		case action := <-state.firstAction:
			t.Fatalf("past-window run emitted action: %+v", action)
		default:
		}
	})
}

func TestRunAtStartPausedResumesAfterFutureEpoch(t *testing.T) {
	state := installStartupEpochState(t)
	script := &dsl.Script{
		Name: "paused coordinated start", Protocol: startupEpochProtocolName,
		Config:  dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}},
		Actions: []dsl.Action{{Name: "main", Type: "request", Target: "/"}},
	}
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups:      []GroupConfig{{Name: "early", Script: script, VUsers: 1}},
		Duration:    time.Second,
		StartPaused: true,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	startAt := time.Now().Add(80 * time.Millisecond)
	done := make(chan error, 1)
	go func() {
		_, runErr := mge.RunAt(context.Background(), startAt)
		done <- runErr
	}()
	select {
	case <-mge.Ready():
	case <-time.After(time.Second):
		t.Fatal("start-paused execution did not become control-ready")
	}
	if wait := time.Until(startAt.Add(50 * time.Millisecond)); wait > 0 {
		time.Sleep(wait)
	}
	select {
	case action := <-state.allActions:
		t.Fatalf("start-paused execution emitted action before Resume: %+v", action)
	default:
	}
	if err := mge.Resume(); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	select {
	case action := <-state.allActions:
		if action.at.Before(startAt) {
			t.Fatalf("resumed action at %s preceded coordinated epoch %s", action.at, startAt)
		}
	case <-time.After(time.Second):
		t.Fatal("start-paused execution did not run after Resume")
	}
	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatalf("RunAt: %v", runErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("start-paused coordinated run did not finish")
	}
}

func TestRunAtKeepsCoordinatedResourceBaselineFreeOfProtocolInit(t *testing.T) {
	state := installStartupEpochState(t)
	script := &dsl.Script{
		Name: "coordinated resource baseline", Protocol: startupEpochProtocolName,
		Config:  dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}},
		Actions: []dsl.Action{{Name: "main", Type: "request", Target: "/"}},
	}
	const baseline = 120 * time.Millisecond
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups:      []GroupConfig{{Name: "early", Script: script, VUsers: 1}},
		Duration:    time.Second,
		PreBaseline: baseline,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	startAt := time.Now().Add(80 * time.Millisecond)
	done := make(chan error, 1)
	go func() {
		_, runErr := mge.RunAt(context.Background(), startAt)
		done <- runErr
	}()
	if wait := time.Until(startAt.Add(baseline - 30*time.Millisecond)); wait > 0 {
		time.Sleep(wait)
	}
	select {
	case init := <-state.allInits:
		t.Fatalf("protocol Init at %s ran during coordinated resource baseline ending near %s", init.at, startAt.Add(baseline))
	default:
	}
	select {
	case init := <-state.allInits:
		if init.at.Before(startAt.Add(baseline)) {
			t.Fatalf("protocol Init at %s preceded baseline end %s", init.at, startAt.Add(baseline))
		}
	case <-time.After(time.Second):
		t.Fatal("protocol Init did not run after coordinated resource baseline")
	}
	mge.Stop()
	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatalf("RunAt: %v", runErr)
		}
	case <-time.After(time.Second):
		t.Fatal("coordinated resource-baseline run did not stop")
	}
}
