package api

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/stormrunner/pkg/dsl"
	"github.com/jasonczech/stormrunner/pkg/engine"
	"github.com/jasonczech/stormrunner/pkg/protocol"
	"github.com/jasonczech/stormrunner/pkg/storage"
)

type metricsStoreSpy struct {
	savedHistoryTestID    string
	savedHistoryTimestamp time.Time
	savedHistoryMetrics   any
	saveHistoryCalls      int
	savedScripts          []*dsl.Script
	deletedScriptIDs      []string
	savedTests            []*storage.TestRecord
	deletedTestIDs        []string
	listedScripts         []*dsl.Script
	listedTests           []*storage.TestRecord
	historyByTestID       map[string][]any
	archiveByTestID       map[string]*storage.RunArchiveRecord
}

func (s *metricsStoreSpy) SaveScript(script *dsl.Script) error {
	s.savedScripts = append(s.savedScripts, script)
	return nil
}
func (s *metricsStoreSpy) GetScript(id string) (*dsl.Script, error) { return nil, nil }
func (s *metricsStoreSpy) ListScripts(projectID string, folderID *string) ([]*dsl.Script, error) {
	return s.listedScripts, nil
}
func (s *metricsStoreSpy) DeleteScript(id string) error {
	s.deletedScriptIDs = append(s.deletedScriptIDs, id)
	return nil
}
func (s *metricsStoreSpy) MoveScript(id string, folderID *string) error { return nil }
func (s *metricsStoreSpy) SaveTest(test *storage.TestRecord) error {
	s.savedTests = append(s.savedTests, test)
	return nil
}
func (s *metricsStoreSpy) GetTest(id string) (*storage.TestRecord, error) { return nil, nil }
func (s *metricsStoreSpy) ListTests(projectID string, folderID *string) ([]*storage.TestRecord, error) {
	return s.listedTests, nil
}
func (s *metricsStoreSpy) UpdateTestStatus(id string, status string, endTime *time.Time, errStr string) error {
	return nil
}
func (s *metricsStoreSpy) DeleteTest(id string) error {
	s.deletedTestIDs = append(s.deletedTestIDs, id)
	return nil
}
func (s *metricsStoreSpy) MoveTest(id string, folderID *string) error { return nil }
func (s *metricsStoreSpy) ListTestsByContext(applicationID string, releaseID string, scenarioID string) ([]*storage.TestRecord, error) {
	return s.listedTests, nil
}
func (s *metricsStoreSpy) SaveApplication(application *storage.Application) error { return nil }
func (s *metricsStoreSpy) GetApplication(id string) (*storage.Application, error) {
	return nil, nil
}
func (s *metricsStoreSpy) ListApplications() ([]*storage.Application, error) { return nil, nil }
func (s *metricsStoreSpy) DeleteApplication(id string) error                 { return nil }
func (s *metricsStoreSpy) SaveRelease(release *storage.Release) error        { return nil }
func (s *metricsStoreSpy) GetRelease(id string) (*storage.Release, error) {
	return &storage.Release{ID: id, ApplicationID: storage.LegacyApplicationID, Name: id}, nil
}
func (s *metricsStoreSpy) ListReleases(applicationID string, includeArchived bool) ([]*storage.Release, error) {
	return nil, nil
}
func (s *metricsStoreSpy) ArchiveRelease(id string) error { return nil }
func (s *metricsStoreSpy) DeleteRelease(id string) error  { return nil }
func (s *metricsStoreSpy) SaveScenario(scenario *storage.Scenario, scripts []storage.ScenarioScript) error {
	return nil
}
func (s *metricsStoreSpy) GetScenario(id string) (*storage.Scenario, []storage.ScenarioScript, error) {
	return nil, nil, nil
}
func (s *metricsStoreSpy) ListScenarios() ([]*storage.Scenario, error) { return nil, nil }
func (s *metricsStoreSpy) DeleteScenario(id string) error              { return nil }
func (s *metricsStoreSpy) SaveScenarioDataPoolBindings(scenarioID string, bindings []storage.ScenarioDataPoolBinding) error {
	return nil
}
func (s *metricsStoreSpy) ListScenarioDataPoolBindings(scenarioID string) ([]storage.ScenarioDataPoolBinding, error) {
	return nil, nil
}
func (s *metricsStoreSpy) DeleteScenarioDataPoolBinding(id string) error { return nil }
func (s *metricsStoreSpy) SaveDataPool(pool *storage.SharedDataPool) error {
	return nil
}
func (s *metricsStoreSpy) GetDataPool(id string) (*storage.SharedDataPool, error) {
	return nil, nil
}
func (s *metricsStoreSpy) ListDataPools(includeArchived bool) ([]*storage.SharedDataPool, error) {
	return nil, nil
}
func (s *metricsStoreSpy) DeleteDataPool(id string) error { return nil }
func (s *metricsStoreSpy) SaveDataPoolRow(row *storage.SharedDataPoolRow) error {
	return nil
}
func (s *metricsStoreSpy) ListDataPoolRows(poolID string) ([]*storage.SharedDataPoolRow, error) {
	return nil, nil
}
func (s *metricsStoreSpy) DeleteDataPoolRow(id string) error          { return nil }
func (s *metricsStoreSpy) SaveProject(project *storage.Project) error { return nil }
func (s *metricsStoreSpy) GetProject(id string) (*storage.Project, error) {
	return nil, nil
}
func (s *metricsStoreSpy) ListProjects() ([]*storage.Project, error) { return nil, nil }
func (s *metricsStoreSpy) DeleteProject(id string) error             { return nil }
func (s *metricsStoreSpy) SaveFolder(folder *storage.Folder) error   { return nil }
func (s *metricsStoreSpy) GetFolder(id string) (*storage.Folder, error) {
	return nil, nil
}
func (s *metricsStoreSpy) ListFolders(projectID string, category string) ([]*storage.Folder, error) {
	return nil, nil
}
func (s *metricsStoreSpy) MoveFolder(id string, newParentID *string) error { return nil }
func (s *metricsStoreSpy) DeleteFolder(id string) error                    { return nil }
func (s *metricsStoreSpy) SaveHistory(testID string, timestamp time.Time, metrics any) error {
	s.savedHistoryTestID = testID
	s.savedHistoryTimestamp = timestamp
	s.savedHistoryMetrics = metrics
	s.saveHistoryCalls++
	return nil
}
func (s *metricsStoreSpy) GetHistory(testID string) ([]any, error) {
	if s.historyByTestID == nil {
		return nil, nil
	}
	return s.historyByTestID[testID], nil
}
func (s *metricsStoreSpy) SaveRunArchive(record *storage.RunArchiveRecord) error { return nil }
func (s *metricsStoreSpy) GetRunArchive(testID string) (*storage.RunArchiveRecord, error) {
	if s.archiveByTestID == nil {
		return nil, nil
	}
	return s.archiveByTestID[testID], nil
}
func (s *metricsStoreSpy) SaveSetting(key string, value string) error { return nil }
func (s *metricsStoreSpy) GetSetting(key string) (string, error)      { return "", nil }
func (s *metricsStoreSpy) Close() error                               { return nil }

type archiveStubProtocol struct{}

func (p *archiveStubProtocol) Name() string                                   { return "archive-stub" }
func (p *archiveStubProtocol) Init(_ context.Context, _ map[string]any) error { return nil }
func (p *archiveStubProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	return &protocol.Result{
		ActionName:       action.Name,
		StartTime:        time.Now(),
		Duration:         5 * time.Millisecond,
		BytesSent:        128,
		BytesReceived:    512,
		StatusCode:       200,
		Success:          true,
		CorrelationID:    "corr-archive-1",
		DependencyType:   "http",
		DependencyTarget: "archive.example.test",
	}, nil
}
func (p *archiveStubProtocol) Teardown(_ context.Context) error    { return nil }
func (p *archiveStubProtocol) HealthCheck(_ context.Context) error { return nil }

type archiveRecorderStub struct {
	finalizeRecord   *storage.RunArchiveRecord
	finalizeErr      error
	intervalCalls    int
	resourceCalls    int
	recordedLive     *LiveMetrics
	recordedAt       time.Time
	recordedVUsers   int64
	recordedResource *engine.SystemResources
}

func (s *archiveRecorderStub) RecordResult(result *protocol.Result) error { return nil }
func (s *archiveRecorderStub) RecordResourceSample(timestamp time.Time, activeVUsers int64, resources *engine.SystemResources) error {
	s.resourceCalls++
	s.recordedAt = timestamp
	s.recordedVUsers = activeVUsers
	s.recordedResource = resources
	return nil
}
func (s *archiveRecorderStub) RecordIntervalSummary(live *LiveMetrics) error {
	s.intervalCalls++
	s.recordedLive = live
	return nil
}
func (s *archiveRecorderStub) Finalize(status string, completedAt time.Time, errStr string) (*storage.RunArchiveRecord, error) {
	return s.finalizeRecord, s.finalizeErr
}

func TestCreateTestPreservesRuntimeSettingsAndGroups(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(2, hub, nil)
	runner.StoreScript(&dsl.Script{
		ID:       "script-1",
		Name:     "Checkout",
		Protocol: "http",
		Version:  "1.0.0",
		Config: dsl.ScriptConfig{
			BaseURL: "https://example.com",
			RunLogic: dsl.RunLogicConfig{
				Iterations: 1,
			},
			Log: dsl.LogConfig{Level: "standard"},
		},
		Actions: []dsl.Action{{Name: "Home", Type: "GET", Target: "/"}},
	})

	iterations := 7
	targetAverageTPS := 300.0
	p95LatencyMs := 850.0
	minVUsers := 15
	maxVUsers := 120
	pacingDelay := 1500
	test, err := runner.CreateTest(CreateTestRequest{
		Name:             "Checkout Replay",
		Duration:         "90s",
		StartDelay:       "30s",
		ScenarioMode:     "goal_oriented",
		ScriptShakeout:   true,
		ResourceBaseline: true,
		GoalConfig: &GoalConfig{
			TargetAverageTPS: &targetAverageTPS,
			P95LatencyMs:     &p95LatencyMs,
			MinVUsers:        &minVUsers,
			MaxVUsers:        &maxVUsers,
			StopOnSLABreach:  true,
			SLABreachWindow:  "45s",
		},
		Groups: []CreateGroupEntry{{
			Name:         "Group A",
			ScriptID:     "script-1",
			VUsers:       25,
			RampUp:       "15s",
			RampDown:     "10s",
			Iterations:   &iterations,
			InitStrategy: "at_once",
		}},
		RuntimeSettings: &RuntimeSettingsOverride{
			Iterations:       &iterations,
			Pacing:           &dsl.PacingConfig{Type: "fixed_delay", DelayMs: pacingDelay},
			ThinkTime:        &dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 750},
			LogLevel:         "extended",
			AutoTransactions: "per_iteration",
			ErrorHandling: &dsl.ErrorHandlingConfig{
				ContinueOnError: true,
				SnapshotOnError: true,
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}

	if test.RuntimeSettings == nil {
		t.Fatal("expected runtime settings to be preserved on managed test")
	}
	if test.RuntimeSettings.Iterations == nil || *test.RuntimeSettings.Iterations != iterations {
		t.Fatalf("expected iterations %d, got %#v", iterations, test.RuntimeSettings.Iterations)
	}
	if test.RuntimeSettings.LogLevel != "extended" {
		t.Fatalf("expected log level extended, got %q", test.RuntimeSettings.LogLevel)
	}
	if test.RuntimeSettings.Pacing == nil || test.RuntimeSettings.Pacing.Type != "fixed_delay" || test.RuntimeSettings.Pacing.DelayMs != pacingDelay {
		t.Fatalf("expected pacing fixed_delay 1500ms, got %#v", test.RuntimeSettings.Pacing)
	}
	if test.RuntimeSettings.ThinkTime == nil || test.RuntimeSettings.ThinkTime.Type != "fixed" || test.RuntimeSettings.ThinkTime.MeanMs != 750 {
		t.Fatalf("expected think time fixed 750ms, got %#v", test.RuntimeSettings.ThinkTime)
	}
	if test.RuntimeSettings.AutoTransactions != "per_iteration" {
		t.Fatalf("expected auto transactions per_iteration, got %q", test.RuntimeSettings.AutoTransactions)
	}
	if test.RuntimeSettings.ErrorHandling == nil || !test.RuntimeSettings.ErrorHandling.SnapshotOnError || !test.RuntimeSettings.ErrorHandling.ContinueOnError {
		t.Fatal("expected continue_on_error and snapshot_on_error overrides to be preserved")
	}
	if test.ScenarioMode != "goal_oriented" {
		t.Fatalf("expected scenario mode goal_oriented, got %q", test.ScenarioMode)
	}
	if !test.ScriptShakeout {
		t.Fatal("expected script shakeout to be preserved on managed test")
	}
	if !test.ResourceBaseline {
		t.Fatal("expected resource baseline to be preserved on managed test")
	}
	if test.GoalConfig == nil {
		t.Fatal("expected goal config to be preserved on managed test")
	}
	if test.GoalConfig.TargetAverageTPS == nil || *test.GoalConfig.TargetAverageTPS != targetAverageTPS {
		t.Fatalf("expected target average TPS %v, got %#v", targetAverageTPS, test.GoalConfig.TargetAverageTPS)
	}
	if test.GoalConfig.P95LatencyMs == nil || *test.GoalConfig.P95LatencyMs != p95LatencyMs {
		t.Fatalf("expected p95 latency %v, got %#v", p95LatencyMs, test.GoalConfig.P95LatencyMs)
	}
	if test.GoalConfig.MinVUsers == nil || *test.GoalConfig.MinVUsers != minVUsers {
		t.Fatalf("expected min vusers %d, got %#v", minVUsers, test.GoalConfig.MinVUsers)
	}
	if test.GoalConfig.MaxVUsers == nil || *test.GoalConfig.MaxVUsers != maxVUsers {
		t.Fatalf("expected max vusers %d, got %#v", maxVUsers, test.GoalConfig.MaxVUsers)
	}
	if !test.GoalConfig.StopOnSLABreach {
		t.Fatal("expected stop_on_sla_breach to be preserved")
	}
	if test.GoalConfig.SLABreachWindow != "45s" {
		t.Fatalf("expected SLA breach window 45s, got %q", test.GoalConfig.SLABreachWindow)
	}
	if len(test.Groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(test.Groups))
	}
	if test.Groups[0].RampDown != "10s" {
		t.Fatalf("expected ramp down 10s, got %q", test.Groups[0].RampDown)
	}
	if test.Groups[0].Iterations == nil || *test.Groups[0].Iterations != iterations {
		t.Fatalf("expected group iterations %d, got %#v", iterations, test.Groups[0].Iterations)
	}
	if test.Groups[0].Initialization != "at_once" {
		t.Fatalf("expected initialization at_once, got %q", test.Groups[0].Initialization)
	}
	if test.DurationStr != "90s" {
		t.Fatalf("expected duration string 90s, got %q", test.DurationStr)
	}
	if test.StartDelay != "30s" {
		t.Fatalf("expected start delay 30s, got %q", test.StartDelay)
	}
}

func TestApplyRuntimeOverridesAppliesExtendedOverrideFields(t *testing.T) {
	iterations := 4
	script := &dsl.Script{
		Config: dsl.ScriptConfig{
			RunLogic:      dsl.RunLogicConfig{Iterations: 1, AutoTransactions: "none"},
			Pacing:        dsl.PacingConfig{Type: "none"},
			ThinkTime:     dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 1000},
			ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: false, SnapshotOnError: false},
			Log:           dsl.LogConfig{Level: "standard"},
		},
	}

	applyRuntimeOverrides(script, &RuntimeSettingsOverride{
		Iterations:       &iterations,
		Pacing:           &dsl.PacingConfig{Type: "fixed_interval", IntervalMs: 2500},
		ThinkTime:        &dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 750},
		LogLevel:         "extended",
		AutoTransactions: "per_iteration",
		ErrorHandling:    &dsl.ErrorHandlingConfig{ContinueOnError: true, SnapshotOnError: true},
	})

	if script.Config.RunLogic.Iterations != 4 {
		t.Fatalf("iterations = %d, want 4", script.Config.RunLogic.Iterations)
	}
	if script.Config.Pacing.Type != "fixed_interval" || script.Config.Pacing.IntervalMs != 2500 {
		t.Fatalf("pacing = %#v, want fixed_interval 2500ms", script.Config.Pacing)
	}
	if script.Config.ThinkTime.Type != "fixed" || script.Config.ThinkTime.MeanMs != 750 {
		t.Fatalf("think_time = %#v, want fixed 750ms", script.Config.ThinkTime)
	}
	if script.Config.Log.Level != "extended" {
		t.Fatalf("log level = %q, want extended", script.Config.Log.Level)
	}
	if script.Config.RunLogic.AutoTransactions != "per_iteration" {
		t.Fatalf("auto transactions = %q, want per_iteration", script.Config.RunLogic.AutoTransactions)
	}
	if !script.Config.ErrorHandling.ContinueOnError || !script.Config.ErrorHandling.SnapshotOnError {
		t.Fatalf("error handling = %#v, want continue_on_error and snapshot_on_error", script.Config.ErrorHandling)
	}
}

func TestRunArtifactsFinalizePersistsArchiveRecord(t *testing.T) {
	record := &storage.RunArchiveRecord{TestID: "test-1", ArchivePath: "/tmp/archive"}
	recorder := &archiveRecorderStub{finalizeRecord: record}
	persisted := 0
	artifacts := &runArtifacts{
		recorder: recorder,
		saveRecord: func(saved *storage.RunArchiveRecord) error {
			if saved != record {
				t.Fatalf("expected persisted record %p, got %p", record, saved)
			}
			persisted++
			return nil
		},
	}

	archiveRecord, finalStatus, finalError := artifacts.finalize("COMPLETE", time.Now(), "")

	if archiveRecord != record {
		t.Fatalf("expected archive record %p, got %p", record, archiveRecord)
	}
	if finalStatus != "COMPLETE" || finalError != "" {
		t.Fatalf("unexpected finalize outcome status=%q error=%q", finalStatus, finalError)
	}
	if persisted != 1 {
		t.Fatalf("expected archive record persistence once, got %d", persisted)
	}
}

func TestNormalizeCreateTestGroupsUsesSingleScriptFallback(t *testing.T) {
	groups, err := normalizeCreateTestGroups(CreateTestRequest{
		ScriptID: "script-1",
		VUsers:   25,
		RampUp:   "15s",
	})
	if err != nil {
		t.Fatalf("normalizeCreateTestGroups returned error: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if groups[0].ScriptID != "script-1" {
		t.Fatalf("expected script-1, got %q", groups[0].ScriptID)
	}
	if groups[0].VUsers != 25 {
		t.Fatalf("expected 25 vusers, got %d", groups[0].VUsers)
	}
	if groups[0].RampUp != "15s" {
		t.Fatalf("expected ramp-up 15s, got %q", groups[0].RampUp)
	}
	if groups[0].RampDown != "" {
		t.Fatalf("expected empty ramp-down, got %q", groups[0].RampDown)
	}
}

func TestAppendRunErrorBuildsMessage(t *testing.T) {
	msg := appendRunError("", "archive finalize failed", errors.New("disk full"))
	if msg != "archive finalize failed: disk full" {
		t.Fatalf("unexpected message: %q", msg)
	}

	msg = appendRunError("engine failed", "archive metadata save failed", errors.New("permission denied"))
	if msg != "engine failed; archive metadata save failed: permission denied" {
		t.Fatalf("unexpected appended message: %q", msg)
	}
}

func TestResourceBaselineWindows(t *testing.T) {
	pre, post := resourceBaselineWindows(false)
	if pre != 0 || post != 0 {
		t.Fatalf("expected disabled baseline windows to be zero, got pre=%s post=%s", pre, post)
	}

	pre, post = resourceBaselineWindows(true)
	if pre != 5*time.Minute || post != 5*time.Minute {
		t.Fatalf("expected enabled baseline windows to be 5m, got pre=%s post=%s", pre, post)
	}
}

func TestStartDelayWindow(t *testing.T) {
	if got := startDelayWindow(""); got != 0 {
		t.Fatalf("expected empty start delay to be zero, got %v", got)
	}
	if got := startDelayWindow("45s"); got != 45*time.Second {
		t.Fatalf("expected 45s start delay, got %v", got)
	}
	if got := startDelayWindow("bad-value"); got != 0 {
		t.Fatalf("expected invalid start delay to normalize to 0, got %v", got)
	}
}

func TestBuildLiveMetricsFromSnapshot(t *testing.T) {
	timestamp := time.Date(2026, time.May, 10, 8, 30, 0, 0, time.UTC)
	resources := &engine.SystemResources{CPUProcessPct: 12.5, MemProcessMb: 256}

	live := buildLiveMetricsFromSnapshot(engine.MetricsSnapshot{
		Timestamp:    timestamp,
		ActiveVUsers: 18,
		TPS:          24.5,
		Transactions: 40,
		Errors:       5,
		AvgDuration:  250 * time.Millisecond,
		P50:          200 * time.Millisecond,
		P90:          350 * time.Millisecond,
		P95:          400 * time.Millisecond,
		P99:          600 * time.Millisecond,
		ByAction: map[string]*engine.ActionMetrics{
			"login": {
				Count:       10,
				Errors:      1,
				AvgDuration: 150 * time.Millisecond,
				P50:         120 * time.Millisecond,
				P90:         240 * time.Millisecond,
				P99:         500 * time.Millisecond,
			},
		},
		ByGroup: map[string]*engine.GroupMetrics{
			"buyers": {
				Transactions: 25,
				Errors:       3,
				TPS:          15.5,
				AvgDuration:  300 * time.Millisecond,
				P50:          210 * time.Millisecond,
				P90:          420 * time.Millisecond,
				P99:          900 * time.Millisecond,
			},
		},
		Browser: &engine.BrowserMetricsSummary{
			Samples:              2,
			AvgFCPMs:             120,
			AvgLCPMs:             240,
			AvgCLSScore:          0.03,
			AvgINPMs:             55,
			AvgBlockingResources: 2,
			AvgLongTaskCount:     1.5,
			AvgJSHeapUsedMB:      18,
		},
		BrowserByDevice: map[string]*engine.BrowserDeviceMetrics{
			"iphone_se": {
				DeviceProfile:        "iphone_se",
				DevicePlatform:       "ios",
				DeviceTier:           "low",
				BrowserEngine:        "webkit",
				Samples:              2,
				AvgFCPMs:             120,
				AvgLCPMs:             240,
				AvgCLSScore:          0.03,
				AvgINPMs:             55,
				AvgBlockingResources: 2,
				AvgLongTaskCount:     1.5,
				AvgJSHeapUsedMB:      18,
			},
		},
		Resources: resources,
	}, map[string]int{"buyers": 12})

	if live.Timestamp != timestamp {
		t.Fatalf("expected timestamp %s, got %s", timestamp, live.Timestamp)
	}
	if live.ActiveVUsers != 18 {
		t.Fatalf("expected 18 active vusers, got %d", live.ActiveVUsers)
	}
	if live.AvgResponseMs != 250 {
		t.Fatalf("expected avg response 250ms, got %v", live.AvgResponseMs)
	}
	if live.ErrorPct != 12.5 {
		t.Fatalf("expected error pct 12.5, got %v", live.ErrorPct)
	}
	if live.Resources != resources {
		t.Fatal("expected resources pointer to be preserved")
	}
	if live.Browser == nil || live.Browser.Samples != 2 {
		t.Fatalf("expected browser summary to be mapped, got %#v", live.Browser)
	}
	if live.BrowserByDevice == nil || live.BrowserByDevice["iphone_se"] == nil {
		t.Fatalf("expected browser-by-device metrics to be mapped, got %#v", live.BrowserByDevice)
	}
	if live.BrowserByDevice["iphone_se"].BrowserEngine != "webkit" {
		t.Fatalf("expected browser engine to be mapped, got %#v", live.BrowserByDevice["iphone_se"])
	}
	if live.BrowserByDevice["iphone_se"].DevicePlatform != "ios" {
		t.Fatalf("expected device platform to be mapped, got %#v", live.BrowserByDevice["iphone_se"])
	}

	login, ok := live.Transactions["login"]
	if !ok {
		t.Fatal("expected login transaction metrics")
	}
	if login.Count != 10 || login.Errors != 1 {
		t.Fatalf("unexpected login metrics: %+v", login)
	}
	if login.AvgMs != 150 || login.P50Ms != 120 || login.P90Ms != 240 || login.P99Ms != 500 {
		t.Fatalf("unexpected converted login percentiles: %+v", login)
	}

	buyers, ok := live.GroupMetrics["buyers"]
	if !ok {
		t.Fatal("expected buyers group metrics")
	}
	if buyers.VUsers != 12 {
		t.Fatalf("expected buyers vusers 12, got %d", buyers.VUsers)
	}
	if buyers.Transactions != 25 || buyers.Errors != 3 {
		t.Fatalf("unexpected buyers counts: %+v", buyers)
	}
	if buyers.AvgResponseMs != 300 || buyers.P50Ms != 210 || buyers.P90Ms != 420 || buyers.P99Ms != 900 {
		t.Fatalf("unexpected converted buyers latencies: %+v", buyers)
	}
}

func TestBuildThresholdAlerts(t *testing.T) {
	p99Threshold := 750.0
	errorThreshold := 2.5
	timestamp := time.Date(2026, time.May, 10, 9, 0, 0, 0, time.UTC)

	alerts := buildThresholdAlerts(&ManagedTest{ID: "test-1", Name: "Checkout"}, &LiveMetrics{
		Timestamp: timestamp,
		P99Ms:     900,
		ErrorPct:  4.2,
	}, ThresholdSettings{
		MaxP99LatencyMs: &p99Threshold,
		MaxErrorRatePct: &errorThreshold,
	})

	if len(alerts) != 2 {
		t.Fatalf("expected 2 threshold alerts, got %d", len(alerts))
	}

	alertsByMetric := make(map[string]map[string]any, len(alerts))
	for _, alert := range alerts {
		metric, ok := alert["metric"].(string)
		if !ok {
			t.Fatalf("expected alert metric to be a string, got %#v", alert["metric"])
		}
		alertsByMetric[metric] = alert
	}

	p99Alert := alertsByMetric["p99_ms"]
	if p99Alert["threshold"] != p99Threshold {
		t.Fatalf("expected p99 threshold %v, got %#v", p99Threshold, p99Alert["threshold"])
	}
	if p99Alert["test_id"] != "test-1" || p99Alert["test_name"] != "Checkout" {
		t.Fatalf("unexpected p99 alert identity: %#v", p99Alert)
	}

	errorAlert := alertsByMetric["error_pct"]
	if errorAlert["threshold"] != errorThreshold {
		t.Fatalf("expected error threshold %v, got %#v", errorThreshold, errorAlert["threshold"])
	}
	if errorAlert["action"] != "stop_requested" {
		t.Fatalf("expected stop_requested action, got %#v", errorAlert["action"])
	}
	if !shouldStopForErrorThreshold(&LiveMetrics{ErrorPct: 4.2}, ThresholdSettings{MaxErrorRatePct: &errorThreshold}) {
		t.Fatal("expected error threshold helper to request stop")
	}
	if shouldStopForErrorThreshold(&LiveMetrics{ErrorPct: 1.0}, ThresholdSettings{MaxErrorRatePct: &errorThreshold}) {
		t.Fatal("did not expect stop below error threshold")
	}
}

func TestInlineCSVFromDataPoolRows(t *testing.T) {
	csvText, columns, err := inlineCSVFromDataPoolRows([]*storage.SharedDataPoolRow{{
		ID:     "row-1",
		PoolID: "pool-1",
		Data:   map[string]string{"zip": "10001", "customer_id": "C-100"},
	}, {
		ID:     "row-2",
		PoolID: "pool-1",
		Data:   map[string]string{"zip": "10002", "customer_id": "C-101"},
	}})
	if err != nil {
		t.Fatalf("inlineCSVFromDataPoolRows returned error: %v", err)
	}
	if strings.Join(columns, ",") != "customer_id,zip" {
		t.Fatalf("unexpected columns: %#v", columns)
	}
	want := "customer_id,zip\nC-100,10001\nC-101,10002\n"
	if csvText != want {
		t.Fatalf("unexpected csv:\n%s", csvText)
	}
}

func TestInlineCSVFromDataPoolRowsRequiresColumns(t *testing.T) {
	_, _, err := inlineCSVFromDataPoolRows([]*storage.SharedDataPoolRow{{ID: "row-1", PoolID: "pool-1"}})
	if err == nil {
		t.Fatal("expected empty data pool rows to fail")
	}
}

func TestPublishLiveMetricsUpdatesStatePersistsHistoryAndBroadcasts(t *testing.T) {
	hub := NewHub()
	store := &metricsStoreSpy{}
	runner := NewTestRunner(2, hub, store)
	timestamp := time.Date(2026, time.May, 10, 10, 15, 0, 0, time.UTC)
	testRun := &ManagedTest{ID: "test-1", Name: "Checkout"}
	live := &LiveMetrics{Timestamp: timestamp, ActiveVUsers: 12, TotalTx: 44}

	runner.publishLiveMetrics(testRun, live, nil)

	if testRun.LiveMetrics != live {
		t.Fatal("expected live metrics pointer to be stored on test")
	}
	if len(testRun.History) != 1 || testRun.History[0] != live {
		t.Fatalf("expected live metrics to be appended to history, got %#v", testRun.History)
	}
	if store.saveHistoryCalls != 1 {
		t.Fatalf("expected SaveHistory to be called once, got %d", store.saveHistoryCalls)
	}
	if store.savedHistoryTestID != "test-1" || store.savedHistoryTimestamp != timestamp || store.savedHistoryMetrics != live {
		t.Fatalf("unexpected SaveHistory inputs: %+v %+v %#v", store.savedHistoryTestID, store.savedHistoryTimestamp, store.savedHistoryMetrics)
	}

	select {
	case msg := <-hub.broadcast:
		var payload struct {
			Type      string       `json:"type"`
			TestID    string       `json:"test_id"`
			TestName  string       `json:"test_name"`
			Timestamp time.Time    `json:"timestamp"`
			Data      *LiveMetrics `json:"data"`
		}
		if err := json.Unmarshal(msg, &payload); err != nil {
			t.Fatalf("failed to decode broadcast payload: %v", err)
		}
		if payload.Type != "metrics_update" || payload.TestID != "test-1" || payload.TestName != "Checkout" {
			t.Fatalf("unexpected broadcast identity: %#v", payload)
		}
		if payload.Timestamp != timestamp {
			t.Fatalf("expected timestamp %s, got %s", timestamp, payload.Timestamp)
		}
		if payload.Data == nil || payload.Data.ActiveVUsers != 12 || payload.Data.TotalTx != 44 {
			t.Fatalf("unexpected broadcast metrics payload: %#v", payload.Data)
		}
		if payload.Data.Telemetry == nil {
			t.Fatal("expected telemetry payload to be populated")
		}
		if payload.Data.Telemetry.HistoryPoints != 1 {
			t.Fatalf("expected history points = 1, got %#v", payload.Data.Telemetry)
		}
		if payload.Data.Telemetry.PublishedAt.IsZero() {
			t.Fatal("expected published timestamp in telemetry payload")
		}
	default:
		t.Fatal("expected metrics_update broadcast")
	}
}

func TestPublishLiveMetricsTrimsInMemoryHistory(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(2, hub, nil)
	testRun := &ManagedTest{ID: "test-1", Name: "Checkout"}
	testRun.History = make([]*LiveMetrics, 0, maxManagedTestHistory)
	for i := 0; i < maxManagedTestHistory; i++ {
		testRun.History = append(testRun.History, &LiveMetrics{Timestamp: time.Unix(int64(i), 0)})
	}

	newest := &LiveMetrics{Timestamp: time.Unix(999999, 0)}
	runner.publishLiveMetrics(testRun, newest, nil)

	if got := len(testRun.History); got != maxManagedTestHistory {
		t.Fatalf("history length = %d, want %d", got, maxManagedTestHistory)
	}
	if testRun.History[len(testRun.History)-1] != newest {
		t.Fatal("expected newest metrics to remain at tail of trimmed history")
	}
	if oldest := testRun.History[0].Timestamp; !oldest.Equal(time.Unix(1, 0)) {
		t.Fatalf("expected oldest retained timestamp %s, got %s", time.Unix(1, 0), oldest)
	}
	if newest.Telemetry == nil || newest.Telemetry.HistoryPoints != maxManagedTestHistory {
		t.Fatalf("expected telemetry history points = %d, got %#v", maxManagedTestHistory, newest.Telemetry)
	}
	select {
	case <-hub.broadcast:
	default:
		t.Fatal("expected metrics_update broadcast")
	}
}

func TestPublishLiveMetricsBroadcastsTelemetryDiagnosticOnDegradation(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(2, hub, nil)
	testRun := &ManagedTest{ID: "test-1", Name: "Checkout"}
	live := &LiveMetrics{
		Timestamp:    time.Now(),
		ActiveVUsers: 4,
		TotalTx:      10,
		Telemetry: &TelemetryHealth{
			DroppedResults: 3,
		},
	}

	runner.publishLiveMetrics(testRun, live, nil)

	var metricsPayload struct {
		Type string `json:"type"`
	}
	select {
	case msg := <-hub.broadcast:
		if err := json.Unmarshal(msg, &metricsPayload); err != nil {
			t.Fatalf("failed to decode metrics payload: %v", err)
		}
		if metricsPayload.Type != "metrics_update" {
			t.Fatalf("expected metrics_update first, got %#v", metricsPayload)
		}
	default:
		t.Fatal("expected metrics_update broadcast")
	}

	var diagnosticPayload struct {
		Type string               `json:"type"`
		Data *TelemetryDiagnostic `json:"data"`
	}
	select {
	case msg := <-hub.broadcast:
		if err := json.Unmarshal(msg, &diagnosticPayload); err != nil {
			t.Fatalf("failed to decode diagnostic payload: %v", err)
		}
		if diagnosticPayload.Type != "telemetry_diagnostic" {
			t.Fatalf("expected telemetry_diagnostic second, got %#v", diagnosticPayload)
		}
		if diagnosticPayload.Data == nil || diagnosticPayload.Data.Severity != "danger" {
			t.Fatalf("expected danger severity, got %#v", diagnosticPayload.Data)
		}
		if !strings.Contains(diagnosticPayload.Data.Summary, "collector dropped 3 metric samples") {
			t.Fatalf("unexpected diagnostic summary: %#v", diagnosticPayload.Data)
		}
		if len(diagnosticPayload.Data.Conditions) != 1 || diagnosticPayload.Data.Conditions[0] != "collector_drops" {
			t.Fatalf("unexpected diagnostic conditions: %#v", diagnosticPayload.Data)
		}
	default:
		t.Fatal("expected telemetry_diagnostic broadcast payload")
	}
}

func TestPublishLiveMetricsBroadcastsSampledTransactionDiagnostics(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(2, hub, nil)
	testRun := &ManagedTest{ID: "test-1", Name: "Checkout"}
	live := &LiveMetrics{
		Timestamp: time.Now(),
		TotalTx:   18,
		Transactions: map[string]*TxMetrics{
			"Checkout": {
				Name:             "Checkout",
				DependencyType:   "http",
				DependencyTarget: "payments.example.test",
				CorrelationIDs:   []string{"corr-checkout-1"},
				CorrelationCount: 3,
				Count:            10,
				Errors:           3,
				AvgMs:            420,
				P99Ms:            880,
			},
			"Search": {
				Name:             "Search",
				DependencyType:   "http",
				DependencyTarget: "catalog.example.test",
				CorrelationIDs:   []string{"corr-search-1"},
				CorrelationCount: 1,
				Count:            8,
				AvgMs:            1225,
				P99Ms:            2400,
			},
		},
		Telemetry: &TelemetryHealth{},
	}

	runner.publishLiveMetrics(testRun, live, nil)

	var metricsPayload struct {
		Type string `json:"type"`
	}
	select {
	case msg := <-hub.broadcast:
		if err := json.Unmarshal(msg, &metricsPayload); err != nil {
			t.Fatalf("failed to decode metrics payload: %v", err)
		}
		if metricsPayload.Type != "metrics_update" {
			t.Fatalf("expected metrics_update first, got %#v", metricsPayload)
		}
	default:
		t.Fatal("expected metrics_update broadcast")
	}

	var diagnosticPayload struct {
		Type string               `json:"type"`
		Data *TelemetryDiagnostic `json:"data"`
	}
	select {
	case msg := <-hub.broadcast:
		if err := json.Unmarshal(msg, &diagnosticPayload); err != nil {
			t.Fatalf("failed to decode diagnostic payload: %v", err)
		}
		if diagnosticPayload.Type != "telemetry_diagnostic" {
			t.Fatalf("expected telemetry_diagnostic second, got %#v", diagnosticPayload)
		}
		if diagnosticPayload.Data == nil || diagnosticPayload.Data.Severity != "danger" {
			t.Fatalf("expected danger severity, got %#v", diagnosticPayload.Data)
		}
		if !strings.Contains(diagnosticPayload.Data.Summary, "transaction error hotspots") || !strings.Contains(diagnosticPayload.Data.Summary, "slow transaction hotspots") {
			t.Fatalf("unexpected diagnostic summary: %#v", diagnosticPayload.Data)
		}
		if len(diagnosticPayload.Data.Samples) != 2 {
			t.Fatalf("expected 2 diagnostic samples, got %#v", diagnosticPayload.Data)
		}
		if diagnosticPayload.Data.Samples[0].Transaction != "Checkout" || diagnosticPayload.Data.Samples[0].Issue != "error" {
			t.Fatalf("unexpected first diagnostic sample: %#v", diagnosticPayload.Data.Samples[0])
		}
		if diagnosticPayload.Data.Samples[1].Transaction != "Search" || diagnosticPayload.Data.Samples[1].DependencyTarget != "catalog.example.test" {
			t.Fatalf("unexpected second diagnostic sample: %#v", diagnosticPayload.Data.Samples[1])
		}
	default:
		t.Fatal("expected telemetry_diagnostic broadcast payload")
	}
}

func TestPublishLiveMetricsSamplesTelemetryDiagnosticUntilStateChanges(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(2, hub, nil)
	testRun := &ManagedTest{ID: "test-1", Name: "Checkout"}

	runner.publishLiveMetrics(testRun, &LiveMetrics{
		Timestamp: time.Now().Add(-3 * time.Second),
		Telemetry: &TelemetryHealth{DroppedResults: 1},
	}, nil)
	runner.publishLiveMetrics(testRun, &LiveMetrics{
		Timestamp: time.Now().Add(-3 * time.Second),
		Telemetry: &TelemetryHealth{DroppedResults: 2},
	}, nil)
	runner.publishLiveMetrics(testRun, &LiveMetrics{
		Timestamp: time.Now(),
		Telemetry: &TelemetryHealth{},
	}, nil)

	messageTypes := make([]string, 0, 5)
	var recovery struct {
		Type string               `json:"type"`
		Data *TelemetryDiagnostic `json:"data"`
	}
	for i := 0; i < 5; i++ {
		select {
		case msg := <-hub.broadcast:
			if err := json.Unmarshal(msg, &recovery); err != nil {
				t.Fatalf("failed to decode diagnostic payload: %v", err)
			}
			messageTypes = append(messageTypes, recovery.Type)
		default:
			t.Fatalf("expected 5 broadcast messages, got %d", len(messageTypes))
		}
	}
	if len(messageTypes) != 5 {
		t.Fatalf("expected 5 broadcast messages, got %#v", messageTypes)
	}
	joined := strings.Join(messageTypes, ",")
	if joined != "metrics_update,telemetry_diagnostic,metrics_update,metrics_update,telemetry_diagnostic" {
		t.Fatalf("unexpected broadcast sequence: %s", joined)
	}
	if recovery.Type != "telemetry_diagnostic" || recovery.Data == nil || recovery.Data.Severity != "healthy" {
		t.Fatalf("expected final recovery diagnostic, got %#v", recovery)
	}
}

func TestLoadStoredTestHistoryTrimsHistoryWindow(t *testing.T) {
	store := &metricsStoreSpy{historyByTestID: map[string][]any{"test-1": make([]any, 0, maxManagedTestHistory+5)}}
	for i := 0; i < maxManagedTestHistory+5; i++ {
		store.historyByTestID["test-1"] = append(store.historyByTestID["test-1"], map[string]any{
			"timestamp": time.Unix(int64(i), 0),
		})
	}
	runner := NewTestRunner(2, NewHub(), store)
	testRun := &ManagedTest{ID: "test-1"}

	runner.loadStoredTestHistory(testRun)

	if got := len(testRun.History); got != maxManagedTestHistory {
		t.Fatalf("history length = %d, want %d", got, maxManagedTestHistory)
	}
	if first := testRun.History[0].Timestamp; !first.Equal(time.Unix(5, 0)) {
		t.Fatalf("expected trimmed first timestamp %s, got %s", time.Unix(5, 0), first)
	}
	if last := testRun.History[len(testRun.History)-1].Timestamp; !last.Equal(time.Unix(int64(maxManagedTestHistory+4), 0)) {
		t.Fatalf("unexpected last timestamp %s", last)
	}
}

func TestLoadStoredTestHistoryBuildsLiveMetricsFromTypedIntervalSummaries(t *testing.T) {
	store := &metricsStoreSpy{historyByTestID: map[string][]any{
		"test-1": {
			storage.IntervalSummaryRecord{
				Timestamp:     time.Date(2026, time.May, 13, 17, 15, 0, 0, time.UTC),
				ActiveVUsers:  9,
				TPS:           22.5,
				AvgResponseMs: 180.5,
				ErrorPct:      2.5,
				TotalTx:       90,
				TotalErrors:   2,
				P50Ms:         140,
				P90Ms:         260,
				P95Ms:         310,
				P99Ms:         400,
				Browser: &storage.BrowserMetricsSummaryRecord{
					Samples:              2,
					AvgFCPMs:             120,
					AvgLCPMs:             240,
					AvgCLSScore:          0.031,
					AvgINPMs:             55,
					AvgBlockingResources: 2,
					AvgLongTaskCount:     1.5,
					AvgJSHeapUsedMB:      18,
				},
				BrowserByDevice: map[string]*storage.BrowserDeviceMetricsRecord{
					"iphone_se": {
						DeviceProfile:        "iphone_se",
						DevicePlatform:       "ios",
						DeviceTier:           "low",
						BrowserEngine:        "webkit",
						Samples:              2,
						AvgFCPMs:             120,
						AvgLCPMs:             240,
						AvgCLSScore:          0.031,
						AvgINPMs:             55,
						AvgBlockingResources: 2,
						AvgLongTaskCount:     1.5,
						AvgJSHeapUsedMB:      18,
					},
				},
				DroppedResults:           5,
				WebsocketDroppedMessages: 2,
				CollectedAt:              time.Date(2026, time.May, 13, 17, 14, 59, 0, time.UTC),
				PublishedAt:              time.Date(2026, time.May, 13, 17, 15, 0, 0, time.UTC),
				SampleAgeMs:              1000,
			},
		},
	}}
	runner := NewTestRunner(2, NewHub(), store)
	testRun := &ManagedTest{ID: "test-1"}

	runner.loadStoredTestHistory(testRun)

	if len(testRun.History) != 1 {
		t.Fatalf("history length = %d, want 1", len(testRun.History))
	}
	if testRun.LiveMetrics == nil {
		t.Fatal("expected live metrics to be set from typed history")
	}
	if testRun.LiveMetrics.ActiveVUsers != 9 || testRun.LiveMetrics.TotalTx != 90 || testRun.LiveMetrics.P95Ms != 310 {
		t.Fatalf("unexpected live metrics from typed history: %#v", testRun.LiveMetrics)
	}
	if testRun.LiveMetrics.Telemetry == nil || testRun.LiveMetrics.Telemetry.DroppedResults != 5 || testRun.LiveMetrics.Telemetry.WebsocketDroppedMessages != 2 {
		t.Fatalf("expected telemetry to round-trip from typed history, got %#v", testRun.LiveMetrics.Telemetry)
	}
	if testRun.LiveMetrics.Browser == nil || testRun.LiveMetrics.Browser.AvgCLSScore != 0.031 {
		t.Fatalf("expected browser summary to round-trip from typed history, got %#v", testRun.LiveMetrics.Browser)
	}
	if testRun.LiveMetrics.BrowserByDevice == nil || testRun.LiveMetrics.BrowserByDevice["iphone_se"] == nil || testRun.LiveMetrics.BrowserByDevice["iphone_se"].DeviceTier != "low" {
		t.Fatalf("expected browser-by-device summary to round-trip from typed history, got %#v", testRun.LiveMetrics.BrowserByDevice)
	}
	if testRun.LiveMetrics.BrowserByDevice["iphone_se"].BrowserEngine != "webkit" {
		t.Fatalf("expected browser engine to round-trip from typed history, got %#v", testRun.LiveMetrics.BrowserByDevice["iphone_se"])
	}
	if testRun.LiveMetrics.BrowserByDevice["iphone_se"].DevicePlatform != "ios" {
		t.Fatalf("expected device platform to round-trip from typed history, got %#v", testRun.LiveMetrics.BrowserByDevice["iphone_se"])
	}
}

func TestBuildLiveMetricsForBroadcastOverridesTimestamp(t *testing.T) {
	snapshotTime := time.Date(2026, time.May, 10, 10, 15, 0, 0, time.UTC)
	broadcastTime := snapshotTime.Add(4 * time.Second)

	live := buildLiveMetricsForBroadcast(engine.MetricsSnapshot{
		Timestamp:    snapshotTime,
		ActiveVUsers: 7,
		TPS:          3.5,
		AvgDuration:  125 * time.Millisecond,
	}, map[string]int{"buyers": 7}, broadcastTime)

	if live.Timestamp != broadcastTime {
		t.Fatalf("expected broadcast timestamp %s, got %s", broadcastTime, live.Timestamp)
	}
	if live.ActiveVUsers != 7 {
		t.Fatalf("active vusers = %d, want 7", live.ActiveVUsers)
	}
	if live.TPS != 3.5 {
		t.Fatalf("TPS = %v, want 3.5", live.TPS)
	}
	if live.AvgResponseMs != 125 {
		t.Fatalf("avg response ms = %v, want 125", live.AvgResponseMs)
	}
	if live.Telemetry == nil {
		t.Fatal("expected telemetry to be initialized")
	}
	if !live.Telemetry.CollectedAt.Equal(snapshotTime) {
		t.Fatalf("expected collected at %s, got %s", snapshotTime, live.Telemetry.CollectedAt)
	}
	if !live.Telemetry.PublishedAt.Equal(broadcastTime) {
		t.Fatalf("expected published at %s, got %s", broadcastTime, live.Telemetry.PublishedAt)
	}
	if live.Telemetry.SampleAgeMs != 4000 {
		t.Fatalf("expected sample age ms 4000, got %v", live.Telemetry.SampleAgeMs)
	}
}

func TestApplyGoalControllerUpdateScalesGroupsAndUpdatesStatus(t *testing.T) {
	timestamp := time.Date(2026, time.May, 10, 11, 0, 0, 0, time.UTC)
	testRun := &ManagedTest{
		ID: "test-1",
		Groups: []TestGroup{
			{Name: "buyers", VUsers: 20},
			{Name: "admins", VUsers: 10},
		},
		goalControl: &goalControllerState{},
	}
	live := &LiveMetrics{Timestamp: timestamp}
	groupVUsers := map[string]int{"buyers": 20, "admins": 10}
	update := goalControllerUpdate{
		Status: &GoalStatus{
			CurrentTotalVUsers:   30,
			SuggestedTotalVUsers: 45,
		},
		ShouldScale:    true,
		TargetTotal:    45,
		ScaleDirection: "up",
		ScaleReason:    "tps below target",
	}

	var scaled []struct {
		group  string
		target int
	}
	applyGoalControllerUpdate(testRun, live, groupVUsers, update, func(groupName string, target int) error {
		scaled = append(scaled, struct {
			group  string
			target int
		}{group: groupName, target: target})
		return nil
	})

	if len(scaled) != 2 {
		t.Fatalf("expected 2 scale operations, got %d", len(scaled))
	}
	if totalGroupVUsers(groupVUsers) != 45 {
		t.Fatalf("expected scaled group total 45, got %d", totalGroupVUsers(groupVUsers))
	}
	if testRun.Groups[0].VUsers != groupVUsers["buyers"] || testRun.Groups[1].VUsers != groupVUsers["admins"] {
		t.Fatalf("expected group config vusers to track scaled map, got %+v vs %+v", testRun.Groups, groupVUsers)
	}
	if testRun.goalControl.lastScaleAt == nil || !testRun.goalControl.lastScaleAt.Equal(timestamp) {
		t.Fatalf("expected goal control lastScaleAt %s, got %#v", timestamp, testRun.goalControl.lastScaleAt)
	}
	if testRun.goalControl.lastScaleDirection != "up" || testRun.goalControl.lastScaleReason != "tps below target" {
		t.Fatalf("unexpected goal control metadata: %+v", testRun.goalControl)
	}
	if live.GoalStatus == nil {
		t.Fatal("expected goal status to be preserved")
	}
	if live.GoalStatus.LastScaleAt == nil || !live.GoalStatus.LastScaleAt.Equal(timestamp) {
		t.Fatalf("expected live goal status timestamp %s, got %#v", timestamp, live.GoalStatus.LastScaleAt)
	}
	if live.GoalStatus.LastScaleDirection != "up" || live.GoalStatus.LastScaleReason != "tps below target" {
		t.Fatalf("unexpected live goal status metadata: %+v", live.GoalStatus)
	}
	if live.GoalStatus.CurrentTotalVUsers != 45 || live.GoalStatus.SuggestedTotalVUsers != 45 {
		t.Fatalf("expected goal status totals to settle at 45, got %+v", live.GoalStatus)
	}
}

func TestRequestStopIfRunningTransitionsAndBroadcasts(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(2, hub, nil)
	testRun := &ManagedTest{ID: "test-1", Name: "Checkout", Status: "RUNNING", cancel: func() {}}
	runner.tests[testRun.ID] = testRun

	runner.requestStopIfRunning(testRun, "threshold stop failed")

	if testRun.Status != "STOPPING" {
		t.Fatalf("expected STOPPING status, got %q", testRun.Status)
	}

	select {
	case msg := <-hub.broadcast:
		var payload struct {
			Type   string `json:"type"`
			TestID string `json:"test_id"`
		}
		if err := json.Unmarshal(msg, &payload); err != nil {
			t.Fatalf("failed to decode broadcast payload: %v", err)
		}
		if payload.Type != "test_stopping" || payload.TestID != "test-1" {
			t.Fatalf("unexpected stop payload: %#v", payload)
		}
	default:
		t.Fatal("expected test_stopping broadcast")
	}
}

func TestRequestStopIfRunningIgnoresNonRunningTests(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(2, hub, nil)
	testRun := &ManagedTest{ID: "test-1", Name: "Checkout", Status: "COMPLETE"}
	runner.tests[testRun.ID] = testRun

	runner.requestStopIfRunning(testRun, "goal-oriented stop failed")

	if testRun.Status != "COMPLETE" {
		t.Fatalf("expected COMPLETE status to remain unchanged, got %q", testRun.Status)
	}
	select {
	case msg := <-hub.broadcast:
		t.Fatalf("did not expect broadcast for non-running test, got %s", string(msg))
	default:
	}
	if shouldStopForGoalStatus(&LiveMetrics{}) {
		t.Fatal("did not expect empty live metrics to request goal stop")
	}
	if !shouldStopForGoalStatus(&LiveMetrics{GoalStatus: &GoalStatus{StopTriggered: true}}) {
		t.Fatal("expected goal stop helper to detect triggered status")
	}
}

func TestScriptAndTestCrudPersistThroughStoreHelpers(t *testing.T) {
	hub := NewHub()
	store := &metricsStoreSpy{}
	runner := NewTestRunner(2, hub, store)

	script := &dsl.Script{
		ID:       "script-1",
		Name:     "Checkout",
		Protocol: "http",
		Version:  "1.0.0",
	}
	runner.StoreScript(script)
	if len(store.savedScripts) != 1 || store.savedScripts[0] != script {
		t.Fatalf("expected initial SaveScript call with stored script, got %#v", store.savedScripts)
	}

	updatedScript := &dsl.Script{
		ID:       "script-1",
		Name:     "Checkout v2",
		Protocol: "http",
		Version:  "1.1.0",
	}
	if _, ok := runner.UpdateScript("script-1", updatedScript); !ok {
		t.Fatal("expected UpdateScript to succeed")
	}
	if len(store.savedScripts) != 2 || store.savedScripts[1] != updatedScript {
		t.Fatalf("expected SaveScript to be called for update, got %#v", store.savedScripts)
	}
	if !runner.DeleteScript("script-1") {
		t.Fatal("expected DeleteScript to succeed")
	}
	if len(store.deletedScriptIDs) != 1 || store.deletedScriptIDs[0] != "script-1" {
		t.Fatalf("expected DeleteScript store call for script-1, got %#v", store.deletedScriptIDs)
	}

	runner.StoreScript(&dsl.Script{
		ID:       "script-2",
		Name:     "Search",
		Protocol: "http",
		Version:  "1.0.0",
		Config: dsl.ScriptConfig{
			BaseURL: "https://example.com",
		},
	})
	testRun, err := runner.CreateTest(CreateTestRequest{
		Name:     "Queued Test",
		Duration: "30s",
		ScriptID: "script-2",
		VUsers:   5,
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}
	if len(store.savedTests) != 1 {
		t.Fatalf("expected one SaveTest call, got %d", len(store.savedTests))
	}
	if store.savedTests[0].ID != testRun.ID || store.savedTests[0].Status != "QUEUED" {
		t.Fatalf("unexpected saved test record: %#v", store.savedTests[0])
	}
	if !runner.DeleteTest(testRun.ID) {
		t.Fatal("expected DeleteTest to succeed for queued test")
	}
	if len(store.deletedTestIDs) != 1 || store.deletedTestIDs[0] != testRun.ID {
		t.Fatalf("expected DeleteTest store call for %s, got %#v", testRun.ID, store.deletedTestIDs)
	}
}

func TestNewTestRunnerLoadsPersistedScriptsAndTests(t *testing.T) {
	archive := &storage.RunArchiveRecord{TestID: "test-1", ArchivePath: "/tmp/archive", ManifestPath: "/tmp/archive/manifest.json"}
	store := &metricsStoreSpy{
		listedScripts: []*dsl.Script{{
			ID:        "script-1",
			Name:      "Checkout",
			Protocol:  "http",
			Version:   "1.0.0",
			CreatedAt: time.Date(2026, time.May, 10, 12, 0, 0, 0, time.UTC),
		}},
		listedTests: []*storage.TestRecord{{
			ID:               "test-1",
			Name:             "Recovered Run",
			Status:           "COMPLETE",
			ScenarioMode:     "goal_oriented",
			ScriptShakeout:   true,
			ResourceBaseline: true,
			TotalVUsers:      25,
			Duration:         int64(45 * time.Second),
			StartTime:        time.Date(2026, time.May, 10, 12, 1, 0, 0, time.UTC),
			Groups: []TestGroup{{
				Name:       "buyers",
				ScriptID:   "script-1",
				ScriptName: "Checkout",
				Protocol:   "http",
				VUsers:     25,
			}},
		}},
		historyByTestID: map[string][]any{
			"test-1": {map[string]any{
				"timestamp":          time.Date(2026, time.May, 10, 12, 1, 30, 0, time.UTC),
				"active_vusers":      float64(20),
				"tps":                float64(12.5),
				"avg_response_ms":    float64(150),
				"error_pct":          float64(0),
				"total_transactions": float64(100),
				"total_errors":       float64(0),
			}},
		},
		archiveByTestID: map[string]*storage.RunArchiveRecord{"test-1": archive},
	}

	runner := NewTestRunner(2, NewHub(), store)

	script, ok := runner.GetScript("script-1")
	if !ok || script.Name != "Checkout" {
		t.Fatalf("expected persisted script to be loaded, got %#v ok=%v", script, ok)
	}

	testRun, ok := runner.GetTest("test-1")
	if !ok {
		t.Fatal("expected persisted test to be loaded")
	}
	if testRun.Name != "Recovered Run" || testRun.Status != "COMPLETE" {
		t.Fatalf("unexpected loaded test metadata: %#v", testRun)
	}
	if len(testRun.History) != 1 || testRun.LiveMetrics == nil {
		t.Fatalf("expected persisted history to be loaded, got history=%#v live=%#v", testRun.History, testRun.LiveMetrics)
	}
	if testRun.Archive != archive {
		t.Fatalf("expected persisted archive to be attached, got %#v", testRun.Archive)
	}
	if cachedArchive, ok := runner.GetRunArchive("test-1"); !ok || cachedArchive != archive {
		t.Fatalf("expected archive cache lookup to return persisted archive, got %#v ok=%v", cachedArchive, ok)
	}
}

func TestProcessMetricsSnapshotBroadcastsAlertAndStopRequest(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(2, hub, nil)
	maxErrorPct := 5.0
	runner.settings.Thresholds.MaxErrorRatePct = &maxErrorPct

	testRun := &ManagedTest{ID: "test-1", Name: "Checkout", Status: "RUNNING"}
	testRun.cancel = func() {}
	runner.tests[testRun.ID] = testRun

	runner.processMetricsSnapshot(testRun, engine.MetricsSnapshot{
		Timestamp:    time.Date(2026, time.May, 10, 13, 0, 0, 0, time.UTC),
		ActiveVUsers: 10,
		Transactions: 20,
		Errors:       2,
		TPS:          15,
		AvgDuration:  200 * time.Millisecond,
	}, map[string]int{}, func(string, int) error { return nil }, nil)

	if testRun.Status != "STOPPING" {
		t.Fatalf("expected test status STOPPING, got %q", testRun.Status)
	}
	if testRun.LiveMetrics == nil || testRun.LiveMetrics.ErrorPct != 10 {
		t.Fatalf("expected live metrics with 10%% error rate, got %#v", testRun.LiveMetrics)
	}

	messageTypes := make([]string, 0, 4)
	for i := 0; i < 4; i++ {
		select {
		case msg := <-hub.broadcast:
			var payload struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(msg, &payload); err != nil {
				t.Fatalf("failed to decode broadcast payload: %v", err)
			}
			messageTypes = append(messageTypes, payload.Type)
		default:
			t.Fatalf("expected 4 broadcast messages, got %d", len(messageTypes))
		}
	}

	if messageTypes[0] != "metrics_update" || messageTypes[1] != "telemetry_diagnostic" || messageTypes[2] != "threshold_alert" || messageTypes[3] != "test_stopping" {
		t.Fatalf("unexpected broadcast order: %#v", messageTypes)
	}
}

func TestEvaluateGoalControllerPlansScaleUpWithinGuardrails(t *testing.T) {
	targetTPS := 300.0
	minVUsers := 10
	maxVUsers := 120
	now := time.Date(2026, time.May, 9, 12, 0, 0, 0, time.UTC)

	update := evaluateGoalController(now, 40, &LiveMetrics{
		TPS:      180,
		P95Ms:    320,
		ErrorPct: 0.4,
	}, &GoalConfig{
		TargetAverageTPS: &targetTPS,
		MinVUsers:        &minVUsers,
		MaxVUsers:        &maxVUsers,
	}, &goalControllerState{})

	if !update.ShouldScale {
		t.Fatal("expected goal controller to request scaling")
	}
	if update.TargetTotal <= 40 {
		t.Fatalf("expected target total to increase above 40, got %d", update.TargetTotal)
	}
	if update.TargetTotal > maxVUsers {
		t.Fatalf("expected target total to respect max guardrail %d, got %d", maxVUsers, update.TargetTotal)
	}
	if update.ScaleDirection != "up" {
		t.Fatalf("expected scale direction up, got %q", update.ScaleDirection)
	}
	if update.Status == nil || update.Status.SuggestedTotalVUsers != update.TargetTotal {
		t.Fatalf("expected suggested total to match target total, got %#v", update.Status)
	}
	if update.ShouldStop {
		t.Fatal("did not expect SLA stop for healthy latency and error metrics")
	}
}

func TestEvaluateGoalControllerStopsOnSustainedSLABreach(t *testing.T) {
	p95LatencyMs := 900.0
	maxErrorPct := 2.0
	now := time.Date(2026, time.May, 9, 12, 0, 45, 0, time.UTC)
	breachStarted := now.Add(-31 * time.Second)

	update := evaluateGoalController(now, 55, &LiveMetrics{
		TPS:      260,
		P95Ms:    1200,
		ErrorPct: 3.2,
	}, &GoalConfig{
		P95LatencyMs:    &p95LatencyMs,
		MaxErrorPct:     &maxErrorPct,
		StopOnSLABreach: true,
		SLABreachWindow: "30s",
	}, &goalControllerState{breachStartedAt: &breachStarted})

	if !update.ShouldStop {
		t.Fatal("expected sustained SLA breach to trigger stop")
	}
	if update.StopReason == "" {
		t.Fatal("expected stop reason to be populated")
	}
	if update.Status == nil || !update.Status.StopTriggered {
		t.Fatalf("expected goal status to mark stop triggered, got %#v", update.Status)
	}
	if !update.Status.LatencyBreached || !update.Status.ErrorBreached {
		t.Fatalf("expected both SLA breach flags to be true, got %#v", update.Status)
	}
	if update.ShouldScale {
		t.Fatal("did not expect scaling decision to take precedence over stop")
	}
}

func TestEvaluateGoalControllerStopsOnMaxErrorCount(t *testing.T) {
	maxErrorCount := 10
	now := time.Date(2026, time.May, 9, 12, 1, 0, 0, time.UTC)

	update := evaluateGoalController(now, 40, &LiveMetrics{
		TPS:         210,
		P95Ms:       300,
		ErrorPct:    0.5,
		TotalErrors: 11,
	}, &GoalConfig{
		MaxErrorCount:   &maxErrorCount,
		StopOnMaxErrors: true,
	}, &goalControllerState{})

	if !update.ShouldStop {
		t.Fatal("expected max error count breach to trigger stop")
	}
	if update.StopReason != "max error count reached" {
		t.Fatalf("expected max error count stop reason, got %q", update.StopReason)
	}
	if update.Status == nil || !update.Status.ErrorCountBreached {
		t.Fatalf("expected goal status to mark error-count breach, got %#v", update.Status)
	}
	if update.ShouldScale {
		t.Fatal("did not expect scaling when hard stop threshold is reached")
	}
}

func TestEvaluateGoalControllerStopTakesPrecedenceOverScaling(t *testing.T) {
	targetTPS := 300.0
	p95LatencyMs := 900.0
	maxErrorPct := 2.0
	now := time.Date(2026, time.May, 9, 12, 2, 0, 0, time.UTC)
	breachStarted := now.Add(-31 * time.Second)

	update := evaluateGoalController(now, 40, &LiveMetrics{
		TPS:      150,
		P95Ms:    1200,
		ErrorPct: 3.2,
	}, &GoalConfig{
		TargetAverageTPS: &targetTPS,
		P95LatencyMs:     &p95LatencyMs,
		MaxErrorPct:      &maxErrorPct,
		StopOnSLABreach:  true,
		SLABreachWindow:  "30s",
	}, &goalControllerState{breachStartedAt: &breachStarted})

	if !update.ShouldStop {
		t.Fatal("expected sustained SLA stop to take precedence over scaling")
	}
	if update.ShouldScale {
		t.Fatal("did not expect scaling to take precedence over stop")
	}
}

func TestValidateScenarioRequestRequiresMaxErrorCountWhenEnabled(t *testing.T) {
	_, err := validateScenarioRequest(CreateTestRequest{
		Duration:     "5m",
		ScenarioMode: "goal_oriented",
		GoalConfig: &GoalConfig{
			StopOnMaxErrors: true,
		},
	})

	if err == nil {
		t.Fatal("expected stop_on_max_errors without max_error_count to fail validation")
	}
}

func TestValidateScenarioRequestRequiresSLAThresholdWhenStopOnBreachEnabled(t *testing.T) {
	_, err := validateScenarioRequest(CreateTestRequest{
		Duration:     "5m",
		ScenarioMode: "goal_oriented",
		GoalConfig: &GoalConfig{
			StopOnSLABreach: true,
		},
	})

	if err == nil {
		t.Fatal("expected stop_on_sla_breach without SLA thresholds to fail validation")
	}
}

func TestValidateScenarioRequestRejectsInvalidGoalGuardrails(t *testing.T) {
	floatPtr := func(value float64) *float64 { return &value }
	intPtr := func(value int) *int { return &value }

	tests := []struct {
		name string
		goal GoalConfig
	}{
		{
			name: "non-positive p95 latency",
			goal: GoalConfig{P95LatencyMs: floatPtr(0)},
		},
		{
			name: "negative max error pct",
			goal: GoalConfig{MaxErrorPct: floatPtr(-1)},
		},
		{
			name: "max error pct above 100",
			goal: GoalConfig{MaxErrorPct: floatPtr(101)},
		},
		{
			name: "non-positive min vusers",
			goal: GoalConfig{MinVUsers: intPtr(0)},
		},
		{
			name: "non-positive max vusers",
			goal: GoalConfig{MaxVUsers: intPtr(0)},
		},
		{
			name: "invalid sla breach window",
			goal: GoalConfig{SLABreachWindow: "later"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validateScenarioRequest(CreateTestRequest{
				Duration:     "5m",
				ScenarioMode: "goal_oriented",
				GoalConfig:   &tt.goal,
			})

			if err == nil {
				t.Fatalf("expected %s to fail validation", tt.name)
			}
		})
	}
}

func TestValidateScenarioRequestRejectsInvalidStartDelay(t *testing.T) {
	_, err := validateScenarioRequest(CreateTestRequest{
		Duration:   "5m",
		StartDelay: "later",
	})

	if err == nil {
		t.Fatal("expected invalid start_delay to fail validation")
	}
}

func TestCompileManagedTestGroupsRejectsInvalidGroupIterations(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(2, hub, nil)
	runner.StoreScript(&dsl.Script{
		ID:       "script-1",
		Name:     "Checkout",
		Protocol: "http",
		Version:  "1.0.0",
		Config:   dsl.ScriptConfig{},
	})
	zero := 0

	_, _, _, err := runner.compileManagedTestGroupsLocked([]CreateGroupEntry{{
		ScriptID:   "script-1",
		VUsers:     10,
		Iterations: &zero,
	}}, nil)

	if err == nil {
		t.Fatal("expected invalid group iterations to fail compilation")
	}
}

func TestDistributeTargetVUsersPreservesTotal(t *testing.T) {
	targets := distributeTargetVUsers([]TestGroup{
		{Name: "buyers", VUsers: 20},
		{Name: "admins", VUsers: 10},
	}, map[string]int{
		"buyers": 20,
		"admins": 10,
	}, 45)

	if targets["buyers"]+targets["admins"] != 45 {
		t.Fatalf("expected distributed targets to sum to 45, got %d", targets["buyers"]+targets["admins"])
	}
	if targets["buyers"] <= targets["admins"] {
		t.Fatalf("expected buyers to retain larger share, got buyers=%d admins=%d", targets["buyers"], targets["admins"])
	}
}

func TestStartTestFailsWhenScriptShakeoutFails(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(2, hub, nil)
	runner.StoreScript(&dsl.Script{
		ID:       "script-bad",
		Name:     "Broken Script",
		Protocol: "definitely-not-supported",
		Version:  "1.0.0",
	})

	test, err := runner.CreateTest(CreateTestRequest{
		Name:           "Broken Scenario",
		Duration:       "30s",
		ScriptShakeout: true,
		ScriptID:       "script-bad",
		VUsers:         25,
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}

	if err := runner.StartTest(test.ID); err != nil {
		t.Fatalf("StartTest returned error: %v", err)
	}

	failed := waitForTestStatus(t, runner, test.ID, "FAILED")
	if failed.Error == "" {
		t.Fatal("expected shakeout failure to populate test error")
	}
	if !strings.Contains(strings.ToLower(failed.Error), "shakeout") {
		t.Fatalf("expected shakeout failure error, got %q", failed.Error)
	}
	if failed.StartedAt != nil {
		t.Fatal("expected failed shakeout to halt before the main run starts")
	}
}

func TestStartTestBlocksWhenCapacityShortfallRequiresOverride(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(2, hub, nil)
	runner.StoreScript(&dsl.Script{
		ID:       "script-capacity",
		Name:     "Capacity Script",
		Protocol: "http",
		Version:  "1.0.0",
		Actions:  []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})

	settings := runner.GetSystemSettings()
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 1
	settings.CapacityPlanning.Generator.MaxVUsers = 10000
	if _, err := runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}

	test, err := runner.CreateTest(CreateTestRequest{
		Name:     "Blocked Scenario",
		Duration: "30s",
		ScriptID: "script-capacity",
		VUsers:   15000,
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}

	err = runner.StartTest(test.ID)
	if err == nil {
		t.Fatal("expected StartTest to fail when capacity shortfall requires override")
	}
	var capacityErr *CapacityShortfallError
	if !errors.As(err, &capacityErr) {
		t.Fatalf("expected CapacityShortfallError, got %T", err)
	}
	if capacityErr.Advisory == nil || !capacityErr.Advisory.RequiresOverride {
		t.Fatalf("expected advisory requiring override, got %#v", capacityErr.Advisory)
	}
	queued, ok := runner.GetTest(test.ID)
	if !ok {
		t.Fatalf("expected queued test %s to remain in runner", test.ID)
	}
	if queued.Status != "QUEUED" {
		t.Fatalf("test status = %s, want QUEUED", queued.Status)
	}
}

func TestStartTestAllowsOverrideOnCapacityShortfall(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(2, hub, nil)
	runner.StoreScript(&dsl.Script{
		ID:       "script-capacity-override",
		Name:     "Capacity Override Script",
		Protocol: "http",
		Version:  "1.0.0",
		Actions:  []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})

	settings := runner.GetSystemSettings()
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 1
	settings.CapacityPlanning.Generator.MaxVUsers = 10000
	if _, err := runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}

	test, err := runner.CreateTest(CreateTestRequest{
		Name:     "Override Scenario",
		Duration: "30s",
		ScriptID: "script-capacity-override",
		VUsers:   15000,
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}

	if err := runner.StartTestWithOptions(test.ID, StartTestOptions{OverrideCapacityShortfall: true}); err != nil {
		t.Fatalf("StartTestWithOptions returned error: %v", err)
	}

	started := waitForStatusAny(t, runner, test.ID, 5*time.Second, "INITIALIZING", "RUNNING", "COMPLETE")
	if started.Status == "QUEUED" {
		t.Fatalf("expected override start to leave QUEUED state, got %s", started.Status)
	}
}

func TestStartTestFailsWhenMaxConcurrentTestsReached(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(1, hub, nil)
	runner.StoreScript(&dsl.Script{
		ID:       "script-concurrency",
		Name:     "Concurrency Script",
		Protocol: "http",
		Version:  "1.0.0",
		Actions:  []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})

	first, err := runner.CreateTest(CreateTestRequest{
		Name:     "First Scenario",
		Duration: "30s",
		ScriptID: "script-concurrency",
		VUsers:   10,
	})
	if err != nil {
		t.Fatalf("CreateTest(first) returned error: %v", err)
	}
	second, err := runner.CreateTest(CreateTestRequest{
		Name:     "Second Scenario",
		Duration: "30s",
		ScriptID: "script-concurrency",
		VUsers:   10,
	})
	if err != nil {
		t.Fatalf("CreateTest(second) returned error: %v", err)
	}

	runner.mu.Lock()
	runner.tests[first.ID].Status = "RUNNING"
	runner.mu.Unlock()

	err = runner.StartTest(second.ID)
	if err == nil {
		t.Fatal("expected StartTest to fail when max concurrent tests is reached")
	}
	if err.Error() != "max concurrent tests (1) reached" {
		t.Fatalf("expected max concurrent error, got %q", err.Error())
	}
	queued, ok := runner.GetTest(second.ID)
	if !ok {
		t.Fatalf("expected queued test %s to remain in runner", second.ID)
	}
	if queued.Status != "QUEUED" {
		t.Fatalf("test status = %s, want QUEUED", queued.Status)
	}
}

func TestGetTestRefreshesCapacityAdvisoryUsingLiveAvailability(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(2, hub, nil)
	runner.StoreScript(&dsl.Script{
		ID:       "script-refresh-capacity",
		Name:     "Refresh Capacity Script",
		Protocol: "http",
		Version:  "1.0.0",
		Actions:  []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})

	settings := runner.GetSystemSettings()
	settings.CapacityPlanning.Availability.Source = capacityAvailabilityConfiguredPlusLive
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 5
	settings.CapacityPlanning.Generator.MaxVUsers = 10000
	if _, err := runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}

	t.Setenv("STORMRUNNER_LIVE_AVAILABLE_GENERATORS", "5")
	test, err := runner.CreateTest(CreateTestRequest{
		Name:     "Refresh Advisory Scenario",
		Duration: "30s",
		ScriptID: "script-refresh-capacity",
		VUsers:   15000,
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}
	if test.CapacityAdvisory == nil || test.CapacityAdvisory.AvailableGenerators != 5 {
		t.Fatalf("initial available generators = %#v, want 5", test.CapacityAdvisory)
	}

	t.Setenv("STORMRUNNER_LIVE_AVAILABLE_GENERATORS", "1")
	refreshed, ok := runner.GetTest(test.ID)
	if !ok {
		t.Fatalf("expected test %s", test.ID)
	}
	if refreshed.CapacityAdvisory == nil {
		t.Fatal("expected refreshed advisory")
	}
	if refreshed.CapacityAdvisory.AvailableGenerators != 1 {
		t.Fatalf("refreshed available generators = %d, want 1", refreshed.CapacityAdvisory.AvailableGenerators)
	}
	if !refreshed.CapacityAdvisory.RequiresOverride {
		t.Fatal("expected refreshed advisory to require override after live capacity shrank")
	}
}

func TestRunTestCreatesArchiveBundle(t *testing.T) {
	archiveDir := t.TempDir()
	t.Setenv("STORMRUNNER_ARCHIVE_DIR", archiveDir)

	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	protocolName := "archive-stub-" + strings.ReplaceAll(strings.ToLower(t.Name()), "/", "-")
	protocol.Register(protocolName, func() protocol.Protocol {
		return &archiveStubProtocol{}
	})

	hub := NewHub()
	runner := NewTestRunner(1, hub, store)
	runner.StoreScript(&dsl.Script{
		ID:       "script-archive",
		Name:     "Archive Script",
		Protocol: protocolName,
		Version:  "1.0.0",
		Config:   dsl.ScriptConfig{},
		Actions:  []dsl.Action{{Name: "step-1", Type: "request", Target: "/"}},
	})

	test, err := runner.CreateTest(CreateTestRequest{
		Name:     "Archive Run",
		Duration: "2200ms",
		ScriptID: "script-archive",
		VUsers:   1,
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}

	if err := runner.StartTest(test.ID); err != nil {
		t.Fatalf("StartTest returned error: %v", err)
	}

	completed := waitForTestStatus(t, runner, test.ID, "COMPLETE")
	if completed.Archive == nil {
		t.Fatal("expected completed test to include archive metadata")
	}
	if completed.Archive.TransactionCount == 0 {
		t.Fatal("expected archive to record transaction events")
	}
	if completed.Archive.IntervalSummaryCount == 0 {
		t.Fatal("expected archive to record interval summaries")
	}

	manifest, err := runner.GetRunArchiveManifest(test.ID)
	if err != nil {
		t.Fatalf("GetRunArchiveManifest returned error: %v", err)
	}
	if manifest.TransactionCount != completed.Archive.TransactionCount {
		t.Fatalf("expected manifest transaction count %d, got %d", completed.Archive.TransactionCount, manifest.TransactionCount)
	}
	if manifest.TransactionsParquetFile == "" || manifest.ResourceSamplesParquetFile == "" || manifest.IntervalSummariesParquetFile == "" {
		t.Fatalf("expected manifest parquet files to be populated, got %#v", manifest)
	}

	for _, name := range []string{"manifest.json", "transactions.jsonl", "interval_summaries.jsonl", "transactions.parquet", "resources.parquet", "interval_summaries.parquet"} {
		if _, err := os.Stat(filepath.Join(completed.Archive.ArchivePath, name)); err != nil {
			t.Fatalf("expected archive file %s to exist: %v", name, err)
		}
	}
}

func TestRunTestIncludesConfiguredStartDelay(t *testing.T) {
	archiveDir := t.TempDir()
	t.Setenv("STORMRUNNER_ARCHIVE_DIR", archiveDir)

	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	protocolName := "start-delay-" + strings.ReplaceAll(strings.ToLower(t.Name()), "/", "-")
	protocol.Register(protocolName, func() protocol.Protocol {
		return &archiveStubProtocol{}
	})

	hub := NewHub()
	runner := NewTestRunner(1, hub, store)
	runner.StoreScript(&dsl.Script{
		ID:       "script-start-delay",
		Name:     "Start Delay Script",
		Protocol: protocolName,
		Version:  "1.0.0",
		Config:   dsl.ScriptConfig{},
		Actions:  []dsl.Action{{Name: "step-1", Type: "request", Target: "/"}},
	})

	test, err := runner.CreateTest(CreateTestRequest{
		Name:       "Delayed Run",
		Duration:   "1100ms",
		StartDelay: "900ms",
		ScriptID:   "script-start-delay",
		VUsers:     1,
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}

	startedAt := time.Now()
	if err := runner.StartTest(test.ID); err != nil {
		t.Fatalf("StartTest returned error: %v", err)
	}

	completed := waitForTestStatus(t, runner, test.ID, "COMPLETE")
	if completed.Archive == nil {
		t.Fatal("expected completed test to include archive metadata")
	}
	if completed.EndedAt == nil {
		t.Fatal("expected completed test to have ended_at populated")
	}
	if completed.EndedAt.Sub(startedAt) < 1800*time.Millisecond {
		t.Fatalf("expected run duration to include start delay, got %v", completed.EndedAt.Sub(startedAt))
	}
}

func TestDeleteTestRemovesArchiveBundle(t *testing.T) {
	archiveDir := t.TempDir()
	t.Setenv("STORMRUNNER_ARCHIVE_DIR", archiveDir)

	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	protocolName := "archive-delete-" + strings.ReplaceAll(strings.ToLower(t.Name()), "/", "-")
	protocol.Register(protocolName, func() protocol.Protocol {
		return &archiveStubProtocol{}
	})

	hub := NewHub()
	runner := NewTestRunner(1, hub, store)
	runner.StoreScript(&dsl.Script{
		ID:       "script-delete-archive",
		Name:     "Delete Archive Script",
		Protocol: protocolName,
		Version:  "1.0.0",
		Config:   dsl.ScriptConfig{},
		Actions:  []dsl.Action{{Name: "step-1", Type: "request", Target: "/"}},
	})

	test, err := runner.CreateTest(CreateTestRequest{
		Name:     "Delete Archive Run",
		Duration: "2200ms",
		ScriptID: "script-delete-archive",
		VUsers:   1,
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}
	if err := runner.StartTest(test.ID); err != nil {
		t.Fatalf("StartTest returned error: %v", err)
	}

	completed := waitForTestStatus(t, runner, test.ID, "COMPLETE")
	if completed.Archive == nil {
		t.Fatal("expected archive metadata after completion")
	}
	archivePath := completed.Archive.ArchivePath
	if _, err := os.Stat(archivePath); err != nil {
		t.Fatalf("expected archive directory to exist before delete: %v", err)
	}

	if !runner.DeleteTest(test.ID) {
		t.Fatal("expected DeleteTest to succeed for completed run")
	}
	if _, ok := runner.GetTest(test.ID); ok {
		t.Fatal("expected deleted test to be removed from runner")
	}
	if _, err := os.Stat(archivePath); !os.IsNotExist(err) {
		t.Fatalf("expected archive directory to be removed, stat err=%v", err)
	}
	if _, ok := runner.GetRunArchive(test.ID); ok {
		t.Fatal("expected archive metadata to be removed after delete")
	}
}

func TestDeleteTestRejectsPausedRuns(t *testing.T) {
	archiveDir := t.TempDir()
	t.Setenv("STORMRUNNER_ARCHIVE_DIR", archiveDir)

	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	protocolName := "archive-delete-paused-" + strings.ReplaceAll(strings.ToLower(t.Name()), "/", "-")
	protocol.Register(protocolName, func() protocol.Protocol {
		return &archiveStubProtocol{}
	})

	hub := NewHub()
	runner := NewTestRunner(1, hub, store)
	runner.StoreScript(&dsl.Script{
		ID:       "script-delete-paused",
		Name:     "Delete Paused Script",
		Protocol: protocolName,
		Version:  "1.0.0",
		Config:   dsl.ScriptConfig{},
		Actions:  []dsl.Action{{Name: "step-1", Type: "request", Target: "/"}},
	})

	test, err := runner.CreateTest(CreateTestRequest{
		Name:     "Delete Paused Run",
		Duration: "10s",
		ScriptID: "script-delete-paused",
		VUsers:   1,
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}
	if err := runner.StartTest(test.ID); err != nil {
		t.Fatalf("StartTest returned error: %v", err)
	}

	waitForStatusAny(t, runner, test.ID, 5*time.Second, "RUNNING", "COMPLETE")
	if err := runner.PauseTest(test.ID); err != nil {
		t.Fatalf("PauseTest returned error: %v", err)
	}
	waitForTestStatus(t, runner, test.ID, "PAUSED")

	if runner.DeleteTest(test.ID) {
		t.Fatal("expected DeleteTest to reject paused run")
	}
	if stillPresent, ok := runner.GetTest(test.ID); !ok {
		t.Fatal("expected paused test to remain in runner after delete rejection")
	} else if stillPresent.Status != "PAUSED" {
		t.Fatalf("expected paused test to remain paused, got %q", stillPresent.Status)
	}

	if err := runner.ResumeTest(test.ID); err != nil {
		t.Fatalf("ResumeTest returned error: %v", err)
	}
	if err := runner.StopTest(test.ID); err != nil {
		t.Fatalf("StopTest returned error: %v", err)
	}
	waitForStatusAny(t, runner, test.ID, 5*time.Second, "FAILED", "COMPLETE")
	if !runner.DeleteTest(test.ID) {
		t.Fatal("expected DeleteTest to succeed once run is no longer active")
	}
	if _, ok := runner.GetTest(test.ID); ok {
		t.Fatal("expected test to be removed after terminal delete")
	}
	if _, ok := runner.GetRunArchive(test.ID); ok {
		t.Fatal("expected archive metadata to be removed after terminal delete")
	}
	if len(hub.broadcast) == 0 {
		// The lifecycle path should have emitted pause/resume/stop events; if this ever changes,
		// the test should fail here rather than silently ignoring a missing active run path.
		t.Fatal("expected lifecycle broadcasts during pause-delete-resume flow")
	}
}

func waitForTestStatus(t *testing.T, runner *TestRunner, testID string, want string) *ManagedTest {
	t.Helper()
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		test, ok := runner.GetTest(testID)
		if !ok {
			t.Fatalf("test %s not found", testID)
		}
		if test.Status == want {
			return test
		}
		time.Sleep(25 * time.Millisecond)
	}

	test, _ := runner.GetTest(testID)
	t.Fatalf("timed out waiting for test %s to reach %s, current status %s", testID, want, test.Status)
	return nil
}

func waitForStatusAny(t *testing.T, runner *TestRunner, testID string, timeout time.Duration, want ...string) *ManagedTest {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		test, ok := runner.GetTest(testID)
		if !ok {
			t.Fatalf("test %s not found", testID)
		}
		for _, status := range want {
			if test.Status == status {
				return test
			}
		}
		time.Sleep(25 * time.Millisecond)
	}

	test, _ := runner.GetTest(testID)
	t.Fatalf("timed out waiting for test %s to reach one of %v, current status %s", testID, want, test.Status)
	return nil
}
