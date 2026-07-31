package api

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestDistributedControlsRejectUntilFleetActivation(t *testing.T) {
	backend := newDistributedExecutionBackend(nil, 50*time.Millisecond, nil)
	now := time.Now().UTC()

	tests := []struct {
		name   string
		status string
		invoke func(*TestRunner, string) error
	}{
		{name: "pause", status: "RUNNING", invoke: func(r *TestRunner, id string) error { return r.PauseTest(id) }},
		{name: "resume", status: "PAUSED", invoke: func(r *TestRunner, id string) error { return r.ResumeTest(id) }},
		{name: "duration", status: "RUNNING", invoke: func(r *TestRunner, id string) error { return r.AdjustDuration(id, 2*time.Minute) }},
		{name: "scale", status: "RUNNING", invoke: func(r *TestRunner, id string) error { return r.ScaleVUsers(id, "buyers", 2) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runner := NewTestRunner(2, NewHub(), nil)
			startedAt := now
			test := &ManagedTest{
				ID:                      "activation-" + tc.name,
				Name:                    "activation gate",
				Status:                  tc.status,
				ProjectID:               storage.DefaultProjectID,
				Duration:                time.Minute,
				DurationStr:             time.Minute.String(),
				Groups:                  []TestGroup{{Name: "buyers", VUsers: 1}},
				TotalVUsers:             1,
				StartedAt:               &startedAt,
				executionBackend:        backend,
				remoteActivated:         false,
				remoteActivationPending: true,
			}
			if tc.status == "PAUSED" {
				pausedAt := now
				test.PausedAt = &pausedAt
			}
			runner.tests[test.ID] = test

			err := tc.invoke(runner, test.ID)
			if !errors.Is(err, errDistributedTestActivating) {
				t.Fatalf("control error = %v, want errDistributedTestActivating", err)
			}
			if test.Status != tc.status || test.Duration != time.Minute || test.TotalVUsers != 1 || test.Groups[0].VUsers != 1 {
				t.Fatalf("rejected control mutated test: status=%s duration=%s total=%d group=%d", test.Status, test.Duration, test.TotalVUsers, test.Groups[0].VUsers)
			}
		})
	}
}

func TestScaleAndGoalControlWaitForLoadWindowAndPause(t *testing.T) {
	runner := NewTestRunner(2, NewHub(), nil)
	targetTPS := 100.0
	now := time.Now().UTC()
	startedAt := now
	test := &ManagedTest{
		ID:              "load-readiness",
		Name:            "load readiness",
		Status:          "RUNNING",
		ProjectID:       storage.DefaultProjectID,
		ScenarioMode:    "goal_oriented",
		StartDelay:      "1m",
		Duration:        5 * time.Minute,
		DurationStr:     (5 * time.Minute).String(),
		Groups:          []TestGroup{{Name: "buyers", VUsers: 1}},
		TotalVUsers:     1,
		StartedAt:       &startedAt,
		GoalConfig:      &GoalConfig{TargetAverageTPS: &targetTPS},
		goalControl:     &goalControllerState{},
		multiEngine:     &engine.MultiGroupEngine{},
		remoteActivated: true,
	}
	runner.tests[test.ID] = test

	if err := runner.ScaleVUsers(test.ID, "buyers", 2); !errors.Is(err, errTestLoadNotReady) {
		t.Fatalf("pre-ready ScaleVUsers error = %v, want errTestLoadNotReady", err)
	}
	live := &LiveMetrics{Timestamp: now, TPS: 0, TotalErrors: 3}
	if update, ok := runner.evaluateGoalControllerLocked(test, live, map[string]int{"buyers": 1}); ok || update.Status != nil {
		t.Fatalf("pre-ready goal evaluation = (%+v, %v), want suppressed", update, ok)
	}
	if test.goalControl.cumulativeErrors != 0 || test.goalControl.errorWindowsTracked {
		t.Fatalf("pre-ready snapshot mutated goal error state: %+v", test.goalControl)
	}

	// A tracked distributed run uses its coordinated active clock after the
	// activation barrier. Move beyond start delay and remove the local-engine
	// fixture so the same helper exercises that path.
	backend := newDistributedExecutionBackend(nil, 50*time.Millisecond, nil)
	test.executionBackend = backend
	test.multiEngine = nil
	readyStartedAt := now.Add(-2 * time.Minute)
	test.StartedAt = &readyStartedAt
	if update, ok := runner.evaluateGoalControllerLocked(test, live, map[string]int{"buyers": 1}); !ok || update.Status == nil {
		t.Fatalf("ready goal evaluation = (%+v, %v), want controller status", update, ok)
	}
	if test.goalControl.cumulativeErrors != 3 {
		t.Fatalf("ready cumulative errors = %d, want 3", test.goalControl.cumulativeErrors)
	}

	test.Status = "PAUSED"
	pausedLive := &LiveMetrics{Timestamp: now.Add(time.Second), TPS: 0, TotalErrors: 5}
	if update, ok := runner.evaluateGoalControllerLocked(test, pausedLive, map[string]int{"buyers": 1}); ok || update.Status != nil {
		t.Fatalf("paused goal evaluation = (%+v, %v), want suppressed", update, ok)
	}
	if test.goalControl.cumulativeErrors != 3 {
		t.Fatalf("paused cumulative errors = %d, want unchanged 3", test.goalControl.cumulativeErrors)
	}
}

func TestThrottledRemoteMetricsDoNotAdvanceGoalErrorsWhilePaused(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	backend := newDistributedExecutionBackend(nil, 50*time.Millisecond, nil)
	targetTPS := 10.0
	now := time.Now().UTC()
	startedAt := now.Add(-time.Minute)
	test := &ManagedTest{
		ID:               "paused-remote-errors",
		Status:           "PAUSED",
		ScenarioMode:     "goal_oriented",
		GoalConfig:       &GoalConfig{TargetAverageTPS: &targetTPS},
		goalControl:      &goalControllerState{},
		StartedAt:        &startedAt,
		executionBackend: backend,
		remoteActivated:  true,
	}
	runner.tests[test.ID] = test

	backend.storeRemoteMetricsBatchOnly(runner, test, "generator-a", generatorrpc.MetricsBatch{
		TestID:          test.ID,
		TimestampUnixMs: now.UnixMilli(),
		FleetMetrics:    &generatorrpc.MetricAggregate{Errors: 7},
	})
	if test.goalControl.cumulativeErrors != 0 || test.goalControl.errorWindowsTracked {
		t.Fatalf("paused remote batch mutated goal errors: %+v", test.goalControl)
	}

	test.Status = "RUNNING"
	backend.storeRemoteMetricsBatchOnly(runner, test, "generator-a", generatorrpc.MetricsBatch{
		TestID:          test.ID,
		TimestampUnixMs: now.Add(time.Second).UnixMilli(),
		FleetMetrics:    &generatorrpc.MetricAggregate{Errors: 5},
	})
	if test.goalControl.cumulativeErrors != 0 || test.goalControl.errorWindowsTracked {
		t.Fatalf("accepted running batch mutated goal errors before publication: %+v", test.goalControl)
	}
	live, _, _, fresh, publication := backend.applyStoredRemoteMetricsBatch(runner, test, "generator-a")
	if !fresh {
		t.Fatal("stored running batch was not available for publication")
	}
	if result := backend.publishDistributedMetricsRevision(context.Background(), runner, test, live, publication); !result.published {
		t.Fatalf("running batch publication result = %+v, want published", result)
	}
	if test.goalControl.cumulativeErrors != 5 {
		t.Fatalf("published running remote cumulative errors = %d, want 5", test.goalControl.cumulativeErrors)
	}
}

func TestResumeShiftsGoalControllerTimersByPauseDuration(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	backend := &lifecycleTransitionBackend{}
	runner.setExecutionBackend(backend)
	now := time.Now().UTC()
	pausedAt := now.Add(-10 * time.Minute)
	breachStartedAt := pausedAt.Add(-5 * time.Second)
	lastScaleAt := pausedAt.Add(-2 * time.Second)
	startedAt := pausedAt.Add(-time.Minute)
	targetTPS := 100.0
	maxP95 := 1.0
	test := &ManagedTest{
		ID:           "goal-pause-clock",
		Status:       "PAUSED",
		ProjectID:    storage.DefaultProjectID,
		Duration:     time.Hour,
		DurationStr:  time.Hour.String(),
		StartedAt:    &startedAt,
		PausedAt:     &pausedAt,
		Groups:       []TestGroup{{Name: "buyers", VUsers: 1}},
		TotalVUsers:  1,
		ScenarioMode: "goal_oriented",
		GoalConfig: &GoalConfig{
			TargetAverageTPS: &targetTPS,
			P95LatencyMs:     &maxP95,
			StopOnSLABreach:  true,
			SLABreachWindow:  "30s",
		},
		goalControl:      &goalControllerState{breachStartedAt: &breachStartedAt, lastScaleAt: &lastScaleAt},
		executionBackend: backend,
		LiveMetrics:      &LiveMetrics{GoalStatus: &GoalStatus{LastScaleAt: &lastScaleAt}},
	}
	runner.tests[test.ID] = test

	if err := runner.ResumeTest(test.ID); err != nil {
		t.Fatalf("ResumeTest: %v", err)
	}
	pauseDelta := test.CumulativePause
	if pauseDelta < 10*time.Minute {
		t.Fatalf("cumulative pause = %s, want at least 10m", pauseDelta)
	}
	wantBreachStart := breachStartedAt.Add(pauseDelta)
	wantLastScale := lastScaleAt.Add(pauseDelta)
	if test.goalControl.breachStartedAt == nil || !test.goalControl.breachStartedAt.Equal(wantBreachStart) {
		t.Fatalf("breach start = %v, want %v", test.goalControl.breachStartedAt, wantBreachStart)
	}
	if test.goalControl.lastScaleAt == nil || !test.goalControl.lastScaleAt.Equal(wantLastScale) {
		t.Fatalf("last scale = %v, want %v", test.goalControl.lastScaleAt, wantLastScale)
	}
	if test.LiveMetrics == nil || test.LiveMetrics.GoalStatus == nil || test.LiveMetrics.GoalStatus.LastScaleAt == nil || !test.LiveMetrics.GoalStatus.LastScaleAt.Equal(wantLastScale) {
		t.Fatalf("live last scale = %+v, want %v", test.LiveMetrics, wantLastScale)
	}

	update := evaluateGoalController(time.Now().UTC(), 1, &LiveMetrics{TPS: 0, P95Ms: 100}, test.GoalConfig, test.goalControl)
	if update.ShouldStop || update.ShouldScale {
		t.Fatalf("first post-resume sample consumed paused time: %+v", update)
	}
}

func TestPublishLiveMetricsKeepsPausedVUsersZero(t *testing.T) {
	hub := NewHub()
	store := &metricsStoreSpy{}
	runner := NewTestRunner(1, hub, store)
	test := &ManagedTest{ID: "paused-metrics", Name: "paused metrics", Status: "PAUSED", Groups: []TestGroup{{Name: "buyers", VUsers: 12}}, TotalVUsers: 12}
	runner.tests[test.ID] = test
	input := &LiveMetrics{
		Timestamp:    time.Now().UTC(),
		ActiveVUsers: 12,
		GroupMetrics: map[string]*GroupLiveMetrics{"buyers": {Name: "buyers", VUsers: 12}},
	}

	runner.publishLiveMetrics(test, input, nil)
	if input.ActiveVUsers != 12 || input.GroupMetrics["buyers"].VUsers != 12 {
		t.Fatalf("publisher mutated raw input: %+v", input)
	}
	if test.LiveMetrics == nil || test.LiveMetrics.ActiveVUsers != 0 || test.LiveMetrics.GroupMetrics["buyers"].VUsers != 0 {
		t.Fatalf("paused installed metrics = %+v, want zero active VUsers", test.LiveMetrics)
	}
	if len(test.History) != 1 || test.History[0].ActiveVUsers != 0 || test.History[0].GroupMetrics["buyers"].VUsers != 0 {
		t.Fatalf("paused history = %+v, want zero active VUsers", test.History)
	}
	saved, ok := store.savedHistoryMetrics.(*LiveMetrics)
	if !ok || saved.ActiveVUsers != 0 || saved.GroupMetrics["buyers"].VUsers != 0 {
		t.Fatalf("paused persisted metrics = %+v, want zero active VUsers", store.savedHistoryMetrics)
	}

	select {
	case msg := <-hub.broadcast:
		var payload struct {
			Data *LiveMetrics `json:"data"`
		}
		if err := json.Unmarshal(msg.payload, &payload); err != nil {
			t.Fatalf("decode metrics broadcast: %v", err)
		}
		if payload.Data == nil || payload.Data.ActiveVUsers != 0 || payload.Data.GroupMetrics["buyers"].VUsers != 0 {
			t.Fatalf("paused broadcast metrics = %+v, want zero active VUsers", payload.Data)
		}
	default:
		t.Fatal("expected paused metrics broadcast")
	}
}

func TestLocalExecutionBackendHandlesCanceledPreBaselineWithoutResult(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	runner := NewTestRunner(1, NewHub(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	test := &ManagedTest{
		ID:           "cancelled-pre-baseline",
		Name:         "cancelled pre-baseline",
		Status:       "INITIALIZING",
		ProjectID:    storage.DefaultProjectID,
		Duration:     time.Minute,
		DurationStr:  time.Minute.String(),
		StartDelay:   "1h",
		executionCtx: ctx,
		cancel:       cancel,
		Groups:       []TestGroup{{Name: "buyers", ScriptName: "script", VUsers: 1}},
		TotalVUsers:  1,
		groupSpecs: []engine.GroupConfig{{
			Name: "buyers",
			Script: &dsl.Script{
				Name: "script", Protocol: "unused-pre-baseline",
				Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}},
			},
			VUsers: 1,
		}},
	}
	runner.tests[test.ID] = test
	cancelled := make(chan struct{})
	go func() {
		defer close(cancelled)
		deadline := time.Now().Add(time.Second)
		for {
			runner.mu.RLock()
			status := test.Status
			runner.mu.RUnlock()
			if status == "RUNNING" {
				cancel()
				return
			}
			if time.Now().After(deadline) {
				cancel()
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()

	// MultiGroupEngine.Run returns (nil, context.Canceled) when cancellation
	// lands in the pre-baseline window. The backend must finalize the failure
	// without dereferencing the absent result in its completion log.
	(localExecutionBackend{}).Run(runner, test)
	<-cancelled
	if test.Status != "FAILED" {
		t.Fatalf("status = %s, want FAILED after canceled pre-baseline", test.Status)
	}
}
