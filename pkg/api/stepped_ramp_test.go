package api

import (
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestValidateEarlyStopConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  *EarlyStopConfig
		wantErr string
	}{
		{name: "omitted"},
		{name: "disabled retained defaults", config: &EarlyStopConfig{ErrorRatePct: 5, BreachWindow: "1m"}},
		{name: "enabled", config: &EarlyStopConfig{Enabled: true, ErrorRatePct: 2.5, BreachWindow: "45s"}},
		{name: "invalid percent", config: &EarlyStopConfig{Enabled: true, ErrorRatePct: 101, BreachWindow: "1m"}, wantErr: "between 0 and 100"},
		{name: "invalid window", config: &EarlyStopConfig{Enabled: true, ErrorRatePct: 5, BreachWindow: "soon"}, wantErr: "invalid early_stop.breach_window"},
		{name: "zero window", config: &EarlyStopConfig{Enabled: true, ErrorRatePct: 5, BreachWindow: "0s"}, wantErr: "greater than zero"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateEarlyStopConfig(tt.config)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateEarlyStopConfig() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateEarlyStopConfig() error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestScenarioEarlyStopRequiresContinuousActiveRuntime(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	testRun := &ManagedTest{
		ID:     "stepped-run",
		Status: "RUNNING",
		GoalConfig: &GoalConfig{EarlyStop: &EarlyStopConfig{
			Enabled: true, ErrorRatePct: 5, BreachWindow: "1m",
		}},
	}
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	systemThreshold := 1.0
	system := ThresholdSettings{MaxErrorRatePct: &systemThreshold}

	if runner.shouldStopForEffectiveErrorThreshold(testRun, &LiveMetrics{Timestamp: base, ErrorPct: 5}, system) {
		t.Fatal("first at-threshold sample stopped immediately")
	}
	if runner.shouldStopForEffectiveErrorThreshold(testRun, &LiveMetrics{Timestamp: base.Add(59 * time.Second), ErrorPct: 8}, system) {
		t.Fatal("breach stopped before the configured active-runtime window")
	}
	if !runner.shouldStopForEffectiveErrorThreshold(testRun, &LiveMetrics{Timestamp: base.Add(time.Minute), ErrorPct: 5}, system) {
		t.Fatal("continuous at-threshold breach did not stop at the configured window")
	}

	// Recovery resets the continuous window.
	if runner.shouldStopForEffectiveErrorThreshold(testRun, &LiveMetrics{Timestamp: base.Add(61 * time.Second), ErrorPct: 4.9}, system) {
		t.Fatal("recovery sample stopped the run")
	}
	if runner.shouldStopForEffectiveErrorThreshold(testRun, &LiveMetrics{Timestamp: base.Add(2 * time.Minute), ErrorPct: 6}, system) {
		t.Fatal("first post-recovery breach sample inherited the old window")
	}

	// Pause is not active runtime and resets the scenario breach window.
	testRun.Status = "PAUSED"
	if runner.shouldStopForEffectiveErrorThreshold(testRun, &LiveMetrics{Timestamp: base.Add(3 * time.Minute), ErrorPct: 99}, system) {
		t.Fatal("paused run accumulated breach time")
	}
	if testRun.earlyStopBreachStartedAt != nil {
		t.Fatal("paused run retained a breach start")
	}
}

func TestScenarioEarlyStopOverridesGlobalThresholdOnlyWhenEnabled(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	global := 2.0
	system := ThresholdSettings{MaxErrorRatePct: &global}
	testRun := &ManagedTest{ID: "run", Status: "RUNNING", GoalConfig: &GoalConfig{EarlyStop: &EarlyStopConfig{Enabled: false, ErrorRatePct: 10, BreachWindow: "1m"}}}

	if !runner.shouldStopForEffectiveErrorThreshold(testRun, &LiveMetrics{ErrorPct: 3}, system) {
		t.Fatal("disabled scenario rule did not preserve the global rule")
	}
	testRun.GoalConfig.EarlyStop.Enabled = true
	if runner.shouldStopForEffectiveErrorThreshold(testRun, &LiveMetrics{Timestamp: time.Now(), ErrorPct: 3}, system) {
		t.Fatal("enabled scenario rule did not override the lower global threshold")
	}
}

func TestScenarioEarlyStopEmitsInclusiveAlertAndLoadLevelReason(t *testing.T) {
	threshold := 5.0
	live := &LiveMetrics{
		ErrorPct:   5,
		LoadLevels: []ActiveLoadLevel{{Group: "checkout", StageIndex: 1, Name: "Level 2", TargetVUsers: 20}},
	}
	alerts := buildThresholdAlerts(&ManagedTest{ID: "run", Name: "Run"}, live, ThresholdSettings{MaxErrorRatePct: &threshold}, true)
	if len(alerts) != 1 || alerts[0]["metric"] != "error_pct" {
		t.Fatalf("inclusive threshold alerts = %#v", alerts)
	}
	reason := earlyStopReason(live, EarlyStopConfig{ErrorRatePct: 5, BreachWindow: "1m"})
	if !strings.Contains(reason, "checkout Level 2 (20 VUsers)") {
		t.Fatalf("early stop reason = %q", reason)
	}
}

func TestPauseClearsScenarioEarlyStopBreachWithoutWaitingForAnotherMetric(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	started := time.Now().UTC().Add(-time.Minute)
	breachStarted := started.Add(10 * time.Second)
	testRun := &ManagedTest{
		ID: "pause-early-stop", Status: "RUNNING", StartedAt: &started,
		executionBackend:         &snapshotPausableBackend{},
		earlyStopBreachStartedAt: &breachStarted,
	}
	runner.tests[testRun.ID] = testRun
	if err := runner.PauseTest(testRun.ID); err != nil {
		t.Fatalf("PauseTest() error = %v", err)
	}
	if testRun.earlyStopBreachStartedAt != nil {
		t.Fatal("PauseTest retained an early-stop breach clock")
	}
}

func TestActiveLoadLevelsAreCopiedFromEachGroup(t *testing.T) {
	now := time.Now().UTC()
	testRun := &ManagedTest{Groups: []TestGroup{
		{Name: "checkout", ActiveStage: &ActiveLoadLevel{Group: "checkout", StageIndex: 2, Name: "Level 3", TargetVUsers: 30, Duration: "10m", StartedAt: now}},
		{Name: "search", ActiveStage: &ActiveLoadLevel{Group: "search", StageIndex: 1, Name: "Level 2", TargetVUsers: 20, Duration: "10m", StartedAt: now}},
	}}
	levels := activeLoadLevelsLocked(testRun)
	if len(levels) != 3 || levels[0].TargetVUsers != 30 || levels[1].TargetVUsers != 20 || levels[2].Group != "__total__" || levels[2].TargetVUsers != 50 {
		t.Fatalf("activeLoadLevelsLocked() = %+v", levels)
	}
	levels[0].TargetVUsers = 999
	if testRun.Groups[0].ActiveStage.TargetVUsers != 30 {
		t.Fatal("active level projection aliased managed group state")
	}
}

func TestHoldStageIsNotPublishedUntilScaleSucceeds(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	backend := &blockingScaleExecutionBackendSpy{
		calls: make(chan scaleBackendCall, 1), blockTarget: 20,
		blocked: make(chan struct{}), release: make(chan struct{}),
	}
	runner.setExecutionBackend(backend)
	started := time.Now().UTC().Add(-time.Second)
	testRun := &ManagedTest{
		ID: "stage-publication", Status: "RUNNING", StartedAt: &started,
		ScenarioMode: "vuser_driven", executionBackend: backend,
		Groups: []TestGroup{{Name: "checkout", VUsers: 10, currentVUsers: 10, stageDriven: true}},
	}
	runner.tests[testRun.ID] = testRun
	done := make(chan struct{})
	go func() {
		runner.runStageSchedule(t.Context(), testRun.ID, "checkout", []storage.LoadStage{{Name: "Level 2", Type: "hold", Duration: "1ms", TargetVUsers: 20}}, 0, nil)
		close(done)
	}()
	select {
	case <-backend.blocked:
	case <-time.After(time.Second):
		t.Fatal("hold scale did not block")
	}
	runner.mu.RLock()
	active := testRun.Groups[0].ActiveStage
	runner.mu.RUnlock()
	if active != nil {
		t.Fatalf("stage was published before scale succeeded: %+v", active)
	}
	close(backend.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stage schedule did not finish")
	}
}

func TestBuildRunArchiveLoadLevelSummariesPreservesPartialEvidence(t *testing.T) {
	started := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	level := ActiveLoadLevel{Group: "checkout", StageIndex: 1, Name: "Level 2", TargetVUsers: 20, Duration: "1m", StartedAt: started}
	runtime10, runtime20 := 10.0, 20.0
	digest := func(values ...time.Duration) *engine.TDigestSnapshot {
		td := engine.NewTDigest(200)
		for _, value := range values {
			td.Add(float64(value))
		}
		snapshot := td.Snapshot()
		return &snapshot
	}
	summaries := []IntervalSummaryRecord{
		{Timestamp: started, ActiveRuntimeSeconds: &runtime10, SampleWindowSeconds: 10, LoadLevels: []ActiveLoadLevel{level}, LoadLevelDurationDigests: map[string]*engine.TDigestSnapshot{"checkout": digest(10*time.Millisecond, 20*time.Millisecond)}, Groups: map[string]*GroupLiveMetrics{"checkout": {Name: "checkout", Transactions: 2, TPS: 10}}},
		{Timestamp: started.Add(10 * time.Minute), ActiveRuntimeSeconds: &runtime10, SampleWindowSeconds: 10, LoadLevels: []ActiveLoadLevel{level}, Groups: map[string]*GroupLiveMetrics{"checkout": {Name: "checkout"}}},
		{Timestamp: started.Add(10*time.Minute + 10*time.Second), ActiveRuntimeSeconds: &runtime20, SampleWindowSeconds: 10, LoadLevels: []ActiveLoadLevel{level}, LoadLevelDurationDigests: map[string]*engine.TDigestSnapshot{"checkout": digest(30*time.Millisecond, 100*time.Millisecond)}, Groups: map[string]*GroupLiveMetrics{"checkout": {Name: "checkout", Transactions: 1, Errors: 1, TPS: 12}}},
	}
	result := buildRunArchiveLoadLevelSummaries(summaries)
	if len(result) != 1 {
		t.Fatalf("load level summaries = %+v", result)
	}
	got := result[0]
	if got.Requests != 3 || got.Errors != 1 || got.ObservedDurationSeconds != 20 || !got.Partial {
		t.Fatalf("load level summary = %+v", got)
	}
	if got.ErrorRatePct != 25 || got.Throughput != 11 || got.P95Ms <= 30 {
		t.Fatalf("aggregated metrics = %+v", got)
	}
}
