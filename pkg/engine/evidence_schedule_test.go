package engine

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestEvidenceScheduleValidationAndFrozenCopy(t *testing.T) {
	_, cfg, options := newPilotEngineFixture(t)
	cfg.Script.Config.RunLogic.Iterations = 0
	cfg.TerminationMode = "duration"
	cfg.Duration = time.Second
	cfg.EvidenceSchedule = []VUserLifetime{{0, 200 * time.Millisecond}, {100 * time.Millisecond, 400 * time.Millisecond}}
	options.ScriptSHA256, _ = JavaPooledScriptSHA256(cfg.Script)
	e, err := NewJavaPooledPilot(cfg, options)
	if err != nil {
		t.Fatal(err)
	}
	cfg.EvidenceSchedule[0].StopAfter = 0
	if e.evidenceSchedule[0].StopAfter != 200*time.Millisecond {
		t.Fatal("caller mutated schedule")
	}
	for _, schedule := range [][]VUserLifetime{
		{{0, time.Second}}, {{0, time.Second}, {0, time.Second}}, {{-1, 1}, {0, 1}}, {{0, 0}, {0, 1}}, {{100, 200}, {0, 200}},
	} {
		cfg.EvidenceSchedule = schedule
		if _, err := New(cfg); err == nil {
			t.Fatal("invalid schedule admitted", schedule)
		}
	}
}

func TestEvidenceScheduleRampsAndClosesWithoutRestart(t *testing.T) {
	peer, cfg, options := newPilotEngineFixture(t)
	cfg.Script.Config.RunLogic.Iterations = 0
	cfg.TerminationMode = "duration"
	cfg.Duration = 2 * time.Second
	cfg.EvidenceSchedule = []VUserLifetime{{0, 200 * time.Millisecond}, {350 * time.Millisecond, 600 * time.Millisecond}}
	options.ScriptSHA256, _ = JavaPooledScriptSHA256(cfg.Script)
	var mu sync.Mutex
	var origin time.Time
	initialized := map[int]time.Time{}
	completed := map[int]time.Time{}
	cfg.EvidenceStarted = func(at time.Time) { mu.Lock(); origin = at; mu.Unlock() }
	cfg.InitializationObserver = func(id int) { mu.Lock(); initialized[id] = time.Now(); mu.Unlock() }
	cfg.CompletionObserver = func(id int, ok bool) {
		mu.Lock()
		defer mu.Unlock()
		if !ok {
			t.Error("cleanup failed")
		}
		if _, exists := completed[id]; exists {
			t.Error("duplicate completion")
		}
		completed[id] = time.Now()
	}
	e, err := NewJavaPooledPilot(cfg, options)
	if err != nil {
		t.Fatal(err)
	}
	out, err := e.Run(context.Background())
	if err != nil || out.TeardownErrors != 0 {
		t.Fatal(out, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(initialized) != 2 || len(completed) != 2 {
		t.Fatal(initialized, completed)
	}
	if initialized[1].Before(origin.Add(cfg.EvidenceSchedule[1].StartAfter)) || completed[0].Before(origin.Add(cfg.EvidenceSchedule[0].StopAfter)) || !completed[0].Before(initialized[1]) {
		t.Fatal("ramp/closure barriers violated", origin, initialized, completed)
	}
	peer.mu.Lock()
	defer peer.mu.Unlock()
	initCount := 0
	for _, q := range peer.calls {
		if q.Operation == "invoke" && q.Method == "init" {
			initCount++
		}
	}
	if initCount != 2 {
		t.Fatal("sessions restarted", initCount)
	}
}
