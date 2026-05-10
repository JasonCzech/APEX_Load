package api

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/stormrunner/pkg/dsl"
	"github.com/jasonczech/stormrunner/pkg/protocol"
	"github.com/jasonczech/stormrunner/pkg/storage"
)

type archiveStubProtocol struct{}

func (p *archiveStubProtocol) Name() string                                   { return "archive-stub" }
func (p *archiveStubProtocol) Init(_ context.Context, _ map[string]any) error { return nil }
func (p *archiveStubProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	return &protocol.Result{
		ActionName:    action.Name,
		StartTime:     time.Now(),
		Duration:      5 * time.Millisecond,
		BytesSent:     128,
		BytesReceived: 512,
		StatusCode:    200,
		Success:       true,
	}, nil
}
func (p *archiveStubProtocol) Teardown(_ context.Context) error    { return nil }
func (p *archiveStubProtocol) HealthCheck(_ context.Context) error { return nil }

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
	test, err := runner.CreateTest(CreateTestRequest{
		Name:             "Checkout Replay",
		Duration:         "90s",
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
			InitStrategy: "at_once",
		}},
		RuntimeSettings: &RuntimeSettingsOverride{
			Iterations:       &iterations,
			LogLevel:         "extended",
			AutoTransactions: "per_iteration",
			ErrorHandling: &dsl.ErrorHandlingConfig{
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
	if test.RuntimeSettings.AutoTransactions != "per_iteration" {
		t.Fatalf("expected auto transactions per_iteration, got %q", test.RuntimeSettings.AutoTransactions)
	}
	if test.RuntimeSettings.ErrorHandling == nil || !test.RuntimeSettings.ErrorHandling.SnapshotOnError {
		t.Fatal("expected snapshot_on_error override to be preserved")
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
	if test.Groups[0].Initialization != "at_once" {
		t.Fatalf("expected initialization at_once, got %q", test.Groups[0].Initialization)
	}
	if test.DurationStr != "90s" {
		t.Fatalf("expected duration string 90s, got %q", test.DurationStr)
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
