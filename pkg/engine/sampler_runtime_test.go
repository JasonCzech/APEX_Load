package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestSourceSamplerRuntimeGroupCompletionDrains(t *testing.T) {
	first := scheduleScript("plan", 42)
	other := scheduleScript("other", 42)
	runtime, err := newSourceSamplerRuntime([]*dsl.Script{first, other}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.close()
	vu := &VUser{Script: first, sourceSamplers: runtime}
	ctx, cleanup, err := vu.bindSourceSamplerBoundary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	group, stop, err := runtime.group(first.Config.JMeter)
	if err != nil {
		t.Fatal(err)
	}
	stop()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("group did not stop")
	}
	select {
	case <-vu.ActionBoundaryCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("boundary did not stop")
	}
	if group.Err() == nil || vu.HardStopCtx.Err() != nil {
		t.Fatal("group stop interrupted an in-flight send")
	}
	peer, _, _ := runtime.group(other.Config.JMeter)
	if peer.Err() != nil {
		t.Fatal("cross-plan cancellation")
	}
}

func TestSourceSamplerRuntimeExhaustionIsNotASample(t *testing.T) {
	script := scheduleScript("plan", 42)
	runtime, err := newSourceSamplerRuntime([]*dsl.Script{script}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.close()
	now := runtime.clock.NowMillis()
	runtime.state.attempt("plan", "timer", now, 0)
	runtime.clock.epoch += 3902 * 1000
	action := dsl.Action{Type: "kafka_produce", Name: "send", PacingIDs: []string{"timer"}}
	if err = runtime.before(context.Background(), script, action); !errors.Is(err, errSamplerScheduleComplete) {
		t.Fatal(err)
	}
	script.Actions = []dsl.Action{action}
	script.Config.RunLogic.Iterations = 1
	proto := &sequenceProtocol{}
	vu := &VUser{Script: script, sourceSamplers: runtime, Proto: proto, Metrics: NewMetricsCollector(16)}
	vu.Run(context.Background())
	if len(proto.actions) != 0 {
		t.Fatal("sent after source schedule completion")
	}
}

func TestSourceSamplerClockFreezesWhilePaused(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gate := make(chan struct{})
	close(gate)
	mge := &MultiGroupEngine{baseCtx: ctx, controlsReady: true, pauseCh: gate, pauseChanged: make(chan struct{}), runStartedAt: time.Now(), baseTargetDuration: time.Second, targetDuration: time.Second}
	clock := &sourceSamplerClock{epoch: 1000000, control: mge}
	if err := mge.Pause(); err != nil {
		t.Fatal(err)
	}
	before := clock.NowMillis()
	done := make(chan error, 1)
	go func() { done <- clock.Wait(ctx, 20*time.Millisecond, make(chan struct{})) }()
	select {
	case err := <-done:
		t.Fatalf("expired while paused: %v", err)
	case <-time.After(40 * time.Millisecond):
	}
	if clock.NowMillis() != before {
		t.Fatal("pause advanced schedule")
	}
	if err := mge.Resume(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("did not resume")
	}
}

func TestSourceSamplerMissingRuntimeCannotSend(t *testing.T) {
	script := &dsl.Script{Protocol: "http", Actions: []dsl.Action{{Type: "http", Name: "send", PacingIDs: []string{"missing"}}}}
	script.Config.RunLogic.Iterations = 1
	proto := &sequenceProtocol{}
	vu := &VUser{Script: script, Proto: proto, Metrics: NewMetricsCollector(16)}
	vu.Run(context.Background())
	if len(proto.actions) != 0 {
		t.Fatal("unpaced action executed")
	}
}

func TestSourceThreadPopulationCannotScale(t *testing.T) {
	script := scheduleScript("plan", 42)
	engine := &MultiGroupEngine{groups: []GroupConfig{{Name: "source", VUsers: 50, Script: script}}}
	if err := engine.ScaleVUsers("source", 51); err == nil {
		t.Fatal("source population changed")
	}
	if err := engine.ScaleVUsers("source", 50); err != nil {
		t.Fatal("idempotent population rejected", err)
	}
	if engine.groups[0].VUsers != 50 {
		t.Fatal("source threads mutated")
	}
}

func TestSourceGroupStopCancelsPendingSamplerWithoutVUserRun(t *testing.T) {
	script := scheduleScript("plan", 1)
	runtime, err := newSourceSamplerRuntime([]*dsl.Script{script}, &blockingPacingController{})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.close()
	runtime.state.mu.Lock()
	entry := runtime.state.entries["plan\x00timer"]
	now := runtime.clock.NowMillis()
	entry.started = true
	entry.startSecond = now / 1000
	entry.second = now / 1000
	entry.rate = 1
	entry.sent = 100000
	runtime.state.mu.Unlock()
	done := make(chan error, 1)
	go func() { done <- runtime.before(context.Background(), script, dsl.Action{PacingIDs: []string{"timer"}}) }()
	deadline := time.After(time.Second)
	for {
		runtime.state.mu.Lock()
		waiting := len(entry.waiters) > 0
		runtime.state.mu.Unlock()
		if waiting {
			break
		}
		select {
		case <-deadline:
			t.Fatal("sampler did not wait")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	_, stop, _ := runtime.group(script.Config.JMeter)
	stop()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("group stop left sampler waiting")
	}
}
