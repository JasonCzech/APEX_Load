package api

import (
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/stormrunner/pkg/dsl"
)

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
		Name:           "Checkout Replay",
		Duration:       "90s",
		ScenarioMode:   "goal_oriented",
		ScriptShakeout: true,
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

func waitForTestStatus(t *testing.T, runner *TestRunner, testID string, want string) *ManagedTest {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
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
