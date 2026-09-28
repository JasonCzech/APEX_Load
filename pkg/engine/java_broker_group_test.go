package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/javasidecar"
)

func brokerGroupFixture(t *testing.T) (*brokerEnginePeer, *javasidecar.BrokerRuntime, MultiGroupConfig) {
	t.Helper()
	peer, cfg, o := brokerEngineFixture(t)
	runtime, err := javasidecar.NewBrokerRuntime(t.Context(), o.Client)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	options := &JavaBrokerGroupOptions{Runtime: runtime, Profile: o.Profile, ScriptSHA256: o.ScriptSHA256, TargetClass: o.TargetClass, ExecutionID: o.ExecutionID, GroupID: o.GroupID, PreparationTimeout: o.PreparationTimeout, RunLifetime: o.RunLifetime}
	return peer, runtime, MultiGroupConfig{TerminationMode: dsl.TerminationIterations, Groups: []GroupConfig{{Name: "first", ScenarioID: "execution", Script: cfg.Script, VUsers: 1, VUserIDOffset: 40, JavaBroker: options}}}
}
func TestJavaBrokerMultiGroupColdBarrierAndSharedOwner(t *testing.T) {
	peer, runtime, cfg := brokerGroupFixture(t)
	second := cfg.Groups[0]
	second.Name = "second"
	o := *second.JavaBroker
	o.GroupID = "second"
	second.JavaBroker = &o
	second.VUserIDOffset = 80
	cfg.Groups = append(cfg.Groups, second)
	engine, err := NewMultiGroup(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Groups[0].Script.Actions[0].Target = "mutated"
	cfg.Groups[0].JavaBroker.Profile.PolicySHA256 = "mutated"
	if err = engine.PreflightRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	peer.mu.Lock()
	for _, q := range peer.calls {
		if q.Operation == "invoke" || q.Operation == "activate" || q.Operation == "commit" {
			t.Fatal("user admission during preflight", q.Operation)
		}
	}
	peer.mu.Unlock()
	result, err := engine.Run(t.Context())
	if err != nil || result == nil {
		t.Fatal(result, err)
	}
	if runtime.Err() != nil {
		t.Fatal("engine closed generator owner", runtime.Err())
	}
	if _, err = engine.Run(t.Context()); err == nil {
		t.Fatal("engine replay accepted")
	}
	peer.mu.Lock()
	defer peer.mu.Unlock()
	counts := map[string]int{}
	invokes := map[string]int{}
	for _, q := range peer.calls {
		counts[q.Operation]++
		if q.Operation == "invoke" {
			invokes[q.SessionID]++
		}
	}
	if counts["reserve"] != 2 || counts["activate"] != 2 || counts["release"] != 0 || invokes["vuser-40"] != 3 || invokes["vuser-80"] != 3 {
		t.Fatal(counts, invokes)
	}
}
func TestJavaBrokerMultiGroupRollbackAndMissedStart(t *testing.T) {
	for _, operation := range []string{"rollback", "stop", "late-start"} {
		t.Run(operation, func(t *testing.T) {
			peer, runtime, cfg := brokerGroupFixture(t)
			engine, err := NewMultiGroup(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if err = engine.PreflightRuntime(t.Context()); err != nil {
				t.Fatal(err)
			}
			switch operation {
			case "rollback":
				if err = engine.ClosePreparedRuntime(); err != nil {
					t.Fatal(err)
				}
			case "stop":
				engine.Stop()
			}
			if operation == "late-start" {
				_, err = engine.RunAt(t.Context(), time.Now().Add(-time.Second))
			} else {
				_, err = engine.Run(t.Context())
			}
			if err == nil {
				t.Fatal("abandoned or late engine ran")
			}
			if runtime.Err() != nil {
				t.Fatal("rollback closed shared owner")
			}
			peer.mu.Lock()
			defer peer.mu.Unlock()
			for _, q := range peer.calls {
				if q.Operation == "invoke" {
					t.Fatal("user code ran", operation)
				}
			}
		})
	}
}
func TestJavaBrokerMultiGroupScaleAndDurationFailClosed(t *testing.T) {
	peer, _, cfg := brokerGroupFixture(t)
	cfg.TerminationMode = dsl.TerminationDuration
	cfg.Duration = time.Second
	cfg.Groups[0].Script.Config.RunLogic.Iterations = 0
	digest, err := JavaPooledScriptSHA256(cfg.Groups[0].Script)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Groups[0].JavaBroker.ScriptSHA256 = digest
	engine, err := NewMultiGroup(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := engine.Run(ctx); done <- err }()
	select {
	case <-engine.Ready():
	case <-time.After(time.Second):
		t.Fatal("engine not ready")
	}
	deadline := time.Now().Add(time.Second)
	for {
		engine.mu.Lock()
		ready := engine.scalingOpen
		engine.mu.Unlock()
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("scaling never ready")
		}
		time.Sleep(time.Millisecond)
	}
	if err = engine.ScaleVUsers("first", 2); err == nil || !strings.Contains(err.Error(), "ready_reservation") {
		t.Fatal("unprepared scale admitted", err)
	}
	if err = engine.AdjustDuration(2 * time.Second); err == nil {
		t.Fatal("fixed lifetime extended")
	}
	if err = engine.ScaleVUsers("first", 0); err != nil {
		t.Fatal("scale down failed", err)
	}
	if err = engine.ScaleVUsers("first", 1); err == nil {
		t.Fatal("closed state reused")
	}
	engine.Stop()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stop blocked")
	}
	peer.mu.Lock()
	defer peer.mu.Unlock()
	reserves := 0
	for _, q := range peer.calls {
		if q.Operation == "reserve" {
			reserves++
		}
	}
	if reserves != 1 {
		t.Fatal("scale changed placement", reserves)
	}
}
func TestJavaBrokerMultiGroupLocalCapacityExcludesBroker(t *testing.T) {
	_, _, cfg := brokerGroupFixture(t)
	engine, err := NewMultiGroup(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if hasPersistentJavaGroup(engine.groups) || persistentJavaConfiguredDemand(engine.groups) != 0 || persistentJavaScaleCapacityDemand(engine.groups, map[string]int64{"first": 1}, 0, 2) != 0 {
		t.Fatal("broker charged to local capacity")
	}
}

func TestJavaBrokerMultiGroupRejectsExcessBeforeAllocation(t *testing.T) {
	_, _, cfg := brokerGroupFixture(t)
	cfg.Groups[0].VUsers = int(^uint(0) >> 1)
	if _, err := NewMultiGroup(cfg); err == nil {
		t.Fatal("unbounded context allocation admitted")
	}
	cfg.Groups[0].VUsers = 3
	if _, err := NewMultiGroup(cfg); err == nil {
		t.Fatal("host ceiling ignored")
	}
}

func TestJavaBrokerPreparedPopulationKeepsColdSessionsAndNeverReusesIdentity(t *testing.T) {
	peer, _, cfg := brokerGroupFixture(t)
	cfg.TerminationMode = dsl.TerminationDuration
	cfg.Duration = 10 * time.Second
	cfg.Groups[0].VUsers = 0
	cfg.Groups[0].AllowZeroVUsers = true
	cfg.Groups[0].JavaBroker.PreparedVUsers = 2
	cfg.Groups[0].Script.Config.RunLogic.Iterations = 0
	cfg.Groups[0].JavaBroker.ScriptSHA256, _ = JavaPooledScriptSHA256(cfg.Groups[0].Script)
	engine, err := NewMultiGroup(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.PreflightRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	peer.mu.Lock()
	crossedBarrier := false
	for _, q := range peer.calls {
		crossedBarrier = crossedBarrier || q.Operation == "invoke" || q.Operation == "activate"
	}
	peer.mu.Unlock()
	if crossedBarrier {
		t.Fatal("cold preparation ran user code")
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := engine.Run(ctx); done <- err }()
	defer func() {
		engine.Stop()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("stop did not drain prepared hosts")
		}
	}()
	select {
	case <-engine.Ready():
	case <-time.After(3 * time.Second):
		t.Fatal("engine not ready")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		engine.mu.Lock()
		ready := engine.scalingOpen
		engine.mu.Unlock()
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("scale barrier not ready")
		}
		time.Sleep(time.Millisecond)
	}
	peer.mu.Lock()
	initializedEarly := false
	for _, q := range peer.calls {
		initializedEarly = initializedEarly || q.Operation == "invoke"
	}
	peer.mu.Unlock()
	if initializedEarly {
		t.Fatal("unused prepared session initialized")
	}
	initialized := 0
	for _, population := range []int{1, 0, 1, 0} {
		if err := engine.ScaleVUsers("first", population); err != nil {
			t.Fatal(population, err)
		}
		if population > 0 {
			initialized++
			deadline := time.Now().Add(3 * time.Second)
			for {
				peer.mu.Lock()
				count := 0
				for _, q := range peer.calls {
					if q.Operation == "invoke" && q.Method == "init" {
						count++
					}
				}
				peer.mu.Unlock()
				if count == initialized {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("new session did not initialize", initialized, count)
				}
				time.Sleep(time.Millisecond)
			}
		}
	}
	if err := engine.ScaleVUsers("first", 1); err == nil {
		t.Fatal("closed identity reused")
	}
	peer.mu.Lock()
	defer peer.mu.Unlock()
	seen := map[string]int{}
	reserves := 0
	for _, q := range peer.calls {
		if q.Operation == "reserve" {
			reserves++
			if len(q.Sessions) != 2 {
				t.Fatal("prepared wrong population")
			}
		}
		if q.Operation == "invoke" && q.Method == "init" {
			seen[q.SessionID]++
		}
	}
	if reserves != 1 || seen["vuser-40"] != 1 || seen["vuser-41"] != 1 {
		t.Fatal("reservation or identity replay", reserves, seen)
	}
}
