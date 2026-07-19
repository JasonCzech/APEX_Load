package api

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

type finalMetricsProtocol struct{}

type partialFailureMetricsProtocol struct{}

func (*finalMetricsProtocol) Name() string                               { return "final-metrics" }
func (*finalMetricsProtocol) Init(context.Context, map[string]any) error { return nil }
func (*finalMetricsProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	return &protocol.Result{ActionName: action.Name, Success: true, Duration: 20 * time.Millisecond}, nil
}
func (*finalMetricsProtocol) Teardown(context.Context) error    { return nil }
func (*finalMetricsProtocol) HealthCheck(context.Context) error { return nil }

func (*partialFailureMetricsProtocol) Name() string { return "partial-failure-metrics" }
func (*partialFailureMetricsProtocol) Init(context.Context, map[string]any) error {
	return nil
}
func (*partialFailureMetricsProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	if action.Name == "fail-init" {
		return nil, errors.New("injected init failure")
	}
	return &protocol.Result{
		ActionName:          action.Name,
		BusinessTransaction: true,
		StartTime:           time.Now(),
		Success:             true,
		Duration:            20 * time.Millisecond,
	}, nil
}
func (*partialFailureMetricsProtocol) Teardown(context.Context) error    { return nil }
func (*partialFailureMetricsProtocol) HealthCheck(context.Context) error { return nil }

type finalMetricsRecorder struct {
	intervals int
}

func (*finalMetricsRecorder) RecordResult(*protocol.Result) error { return nil }
func (*finalMetricsRecorder) RecordResourceSample(time.Time, int64, *engine.SystemResources) error {
	return nil
}
func (r *finalMetricsRecorder) RecordIntervalSummary(*LiveMetrics) error {
	r.intervals++
	return nil
}
func (*finalMetricsRecorder) Finalize(string, time.Time, string, bool, bool, []string) (*storage.RunArchiveRecord, error) {
	return nil, nil
}

func TestPublishFinalLocalMetricsFlushesSubIntervalSLAEvidenceOnce(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(1, hub, nil)
	p95Limit := 10.0
	testRun := &ManagedTest{
		ID:           "short-run",
		Name:         "Short run",
		ProjectID:    storage.DefaultProjectID,
		Status:       "RUNNING",
		ScenarioMode: "goal_oriented",
		Groups:       []TestGroup{{Name: "buyers", VUsers: 1}},
		GoalConfig:   &GoalConfig{P95LatencyMs: &p95Limit},
		goalControl:  &goalControllerState{},
	}
	runner.tests[testRun.ID] = testRun
	recorder := &finalMetricsRecorder{}
	artifacts := &runArtifacts{runner: runner, test: testRun, recorder: recorder}
	timestamp := time.Now().UTC()
	snapshot := engine.MetricsSnapshot{
		Timestamp:    timestamp,
		Window:       25 * time.Millisecond,
		Transactions: 1,
		P95:          20 * time.Millisecond,
		ByGroup: map[string]*engine.GroupMetrics{
			"buyers": {Name: "buyers", Transactions: 1, P95: 20 * time.Millisecond},
		},
	}

	if !runner.publishFinalLocalMetrics(testRun, snapshot, time.Time{}, map[string]int{"buyers": 1}, artifacts) {
		t.Fatal("final collector window was not published")
	}
	if testRun.LiveMetrics == nil || testRun.LiveMetrics.TotalTx != 1 || testRun.LiveMetrics.P95Ms != 20 {
		t.Fatalf("terminal live metrics = %#v", testRun.LiveMetrics)
	}
	if testRun.LiveMetrics.ActiveVUsers != 0 || testRun.LiveMetrics.GroupMetrics["buyers"].VUsers != 0 {
		t.Fatalf("terminal active VUsers = %d/%d, want 0/0", testRun.LiveMetrics.ActiveVUsers, testRun.LiveMetrics.GroupMetrics["buyers"].VUsers)
	}
	if testRun.LiveMetrics.GoalStatus == nil || !testRun.LiveMetrics.GoalStatus.LatencyBreached {
		t.Fatalf("terminal goal status = %#v, want latency breach", testRun.LiveMetrics.GoalStatus)
	}
	if !testRun.ciSLAEvaluated || !testRun.ciSLABreached || !artifacts.slaEvaluated || !artifacts.slaBreached {
		t.Fatalf("terminal SLA evidence = test(%t,%t) artifact(%t,%t)", testRun.ciSLAEvaluated, testRun.ciSLABreached, artifacts.slaEvaluated, artifacts.slaBreached)
	}
	if outcome := ciRunOutcome("COMPLETE", testRun.ciSLABreached, testRun.ciSLAEvaluated, true, ciRunMetadata{RequireSLA: true}, ""); outcome != "sla_breached" {
		t.Fatalf("RequireSLA outcome = %q, want sla_breached", outcome)
	}
	if recorder.intervals != 1 || len(testRun.History) != 1 {
		t.Fatalf("published intervals/history = %d/%d, want 1/1", recorder.intervals, len(testRun.History))
	}

	if runner.publishFinalLocalMetrics(testRun, snapshot, timestamp, map[string]int{"buyers": 1}, artifacts) {
		t.Fatal("duplicate terminal collector window was published")
	}
	if recorder.intervals != 1 || len(testRun.History) != 1 {
		t.Fatalf("duplicate changed intervals/history = %d/%d", recorder.intervals, len(testRun.History))
	}
}

func TestPublishFinalLocalMetricsSatisfiesPassingRequireSLA(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	p95Limit := 50.0
	testRun := &ManagedTest{
		ID: "passing-short-run", Status: "RUNNING", ScenarioMode: "goal_oriented",
		Groups: []TestGroup{{Name: "buyers", VUsers: 1}}, GoalConfig: &GoalConfig{P95LatencyMs: &p95Limit}, goalControl: &goalControllerState{},
	}
	runner.tests[testRun.ID] = testRun
	snapshot := engine.MetricsSnapshot{Timestamp: time.Now().UTC(), Window: 10 * time.Millisecond, Transactions: 1, P95: 5 * time.Millisecond}
	if !runner.publishFinalLocalMetrics(testRun, snapshot, time.Time{}, map[string]int{"buyers": 1}, nil) {
		t.Fatal("final collector window was not published")
	}
	if !testRun.ciSLAEvaluated || testRun.ciSLABreached {
		t.Fatalf("passing SLA evidence = evaluated:%t breached:%t", testRun.ciSLAEvaluated, testRun.ciSLABreached)
	}
	if outcome := ciRunOutcome("COMPLETE", false, testRun.ciSLAEvaluated, true, ciRunMetadata{RequireSLA: true}, ""); outcome != "passed" {
		t.Fatalf("RequireSLA outcome = %q, want passed", outcome)
	}
}

func TestLocalExecutionFlushesMetricsWhenRunEndsBeforePublishInterval(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "final-metrics.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	protocolName := registerRunnerTestProtocol(t, "final-metrics", func() protocol.Protocol { return &finalMetricsProtocol{} })
	runner := NewTestRunner(1, NewHub(), store)
	settings := runner.GetSystemSettings()
	settings.General.MetricsIntervalSeconds = 5
	if _, err := runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings: %v", err)
	}
	runner.StoreScript(&dsl.Script{
		ID: "final-metrics-script", Name: "Final metrics", Protocol: protocolName, Version: "1.0.0",
		Config:  dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}},
		Actions: []dsl.Action{{Name: "transaction", Type: "request", Target: "/"}},
	})
	p95Limit := 1.0
	testRun, err := runner.CreateTest(CreateTestRequest{
		Name: "Sub-interval run", Duration: "5s", ScriptID: "final-metrics-script", VUsers: 1,
		ScenarioMode: "goal_oriented", GoalConfig: &GoalConfig{P95LatencyMs: &p95Limit},
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	if err := runner.StartTest(testRun.ID); err != nil {
		t.Fatalf("StartTest: %v", err)
	}
	completed := waitForTestStatusWithin(t, runner, testRun.ID, 10*time.Second, "COMPLETE")
	if len(completed.History) != 1 || completed.LiveMetrics == nil || completed.LiveMetrics.TotalTx != 1 {
		t.Fatalf("sub-interval terminal metrics/history = %#v/%d", completed.LiveMetrics, len(completed.History))
	}
	if !completed.ciSLAEvaluated || !completed.ciSLABreached {
		t.Fatalf("sub-interval SLA evidence = evaluated:%t breached:%t", completed.ciSLAEvaluated, completed.ciSLABreached)
	}
	if completed.Archive == nil || completed.Archive.IntervalSummaryCount != 1 {
		t.Fatalf("sub-interval archive = %#v, want one interval summary", completed.Archive)
	}
	manifest, err := runner.GetRunArchiveManifest(testRun.ID)
	if err != nil {
		t.Fatalf("GetRunArchiveManifest: %v", err)
	}
	if !manifest.SLAEvaluated || !manifest.SLABreached {
		t.Fatalf("sub-interval manifest SLA = evaluated:%t breached:%t", manifest.SLAEvaluated, manifest.SLABreached)
	}
}

func TestLocalExecutionPublishesPartialMetricsWhenRunReturnsNilResult(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "partial-final-metrics.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	protocolName := registerRunnerTestProtocol(t, "partial-failure-metrics", func() protocol.Protocol {
		return &partialFailureMetricsProtocol{}
	})
	runner := NewTestRunner(1, NewHub(), store)
	settings := runner.GetSystemSettings()
	settings.General.MetricsIntervalSeconds = 5
	if _, err := runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings: %v", err)
	}
	runner.StoreScript(&dsl.Script{
		ID: "partial-final-metrics-script", Name: "Partial final metrics", Protocol: protocolName, Version: "1.0.0",
		Config: dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}},
		Init: []dsl.Action{
			{Name: "partial-transaction", Type: "request", Target: "/partial"},
			{Name: "fail-init", Type: "request", Target: "/fail"},
		},
		Actions: []dsl.Action{{Name: "unreached-main", Type: "request", Target: "/"}},
	})
	testRun, err := runner.CreateTest(CreateTestRequest{
		Name:     "Partial metrics failure",
		Duration: "5s",
		Groups: []CreateGroupEntry{{
			Name:         "failing-group",
			ScriptID:     "partial-final-metrics-script",
			VUsers:       1,
			InitStrategy: "at_once",
		}},
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	if err := runner.StartTest(testRun.ID); err != nil {
		t.Fatalf("StartTest: %v", err)
	}
	failed := waitForTestStatusWithin(t, runner, testRun.ID, 10*time.Second, "FAILED")
	if !strings.Contains(failed.Error, "init-once failed") {
		t.Fatalf("failure = %q, want init-once error path", failed.Error)
	}
	if failed.LiveMetrics == nil || failed.LiveMetrics.TotalTx != 1 || len(failed.History) != 1 {
		t.Fatalf("partial terminal metrics/history = %#v/%d, want one published transaction window", failed.LiveMetrics, len(failed.History))
	}
	if failed.Archive == nil || failed.Archive.IntervalSummaryCount != 1 {
		t.Fatalf("partial failure archive = %#v, want one interval summary", failed.Archive)
	}
}
