package api

import (
	"testing"
	"time"
)

func TestCloneGoalConfigDoesNotAliasPointers(t *testing.T) {
	targetTPS := 300.0
	p95LatencyMs := 900.0
	maxErrors := 10
	config := &GoalConfig{
		TargetAverageTPS: &targetTPS,
		P95LatencyMs:     &p95LatencyMs,
		MaxErrorCount:    &maxErrors,
		StopOnMaxErrors:  true,
	}

	clone := cloneGoalConfig(config)
	*config.TargetAverageTPS = 100
	*config.P95LatencyMs = 1200
	*config.MaxErrorCount = 50

	if clone.TargetAverageTPS == nil || *clone.TargetAverageTPS != 300 {
		t.Fatalf("TargetAverageTPS = %#v, want 300", clone.TargetAverageTPS)
	}
	if clone.P95LatencyMs == nil || *clone.P95LatencyMs != 900 {
		t.Fatalf("P95LatencyMs = %#v, want 900", clone.P95LatencyMs)
	}
	if clone.MaxErrorCount == nil || *clone.MaxErrorCount != 10 {
		t.Fatalf("MaxErrorCount = %#v, want 10", clone.MaxErrorCount)
	}
	if !clone.StopOnMaxErrors {
		t.Fatal("StopOnMaxErrors = false, want true")
	}
}

func TestHasGoalConfigRecognizesThresholdsAndModes(t *testing.T) {
	if hasGoalConfig(nil) {
		t.Fatal("nil config reported as populated")
	}
	if hasGoalConfig(&GoalConfig{}) {
		t.Fatal("empty config reported as populated")
	}
	if !hasGoalConfig(&GoalConfig{StopOnSLABreach: true}) {
		t.Fatal("StopOnSLABreach was not recognized as goal config")
	}
	if normalizeScenarioMode("goal_oriented") != "goal_oriented" {
		t.Fatal("goal_oriented mode should be preserved")
	}
	if normalizeScenarioMode("anything_else") != "vuser_driven" {
		t.Fatal("unknown mode should normalize to vuser_driven")
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

func TestEvaluateGoalControllerStopsOnServerTelemetrySLABreach(t *testing.T) {
	maxServerProcessingMs := 300.0
	maxTransferMs := 100.0
	now := time.Date(2026, time.May, 9, 12, 0, 45, 0, time.UTC)
	breachStarted := now.Add(-31 * time.Second)

	update := evaluateGoalController(now, 55, &LiveMetrics{
		TPS: 260,
		Transactions: map[string]*TxMetrics{
			"POST /checkout": {
				Name: "POST /checkout",
				ServerTelemetry: &ServerTelemetrySummary{
					AvgServerProcessingMs: 420,
					AvgTransferMs:         140,
				},
			},
		},
	}, &GoalConfig{
		MaxAvgServerProcessingMs: &maxServerProcessingMs,
		MaxAvgTransferMs:         &maxTransferMs,
		StopOnSLABreach:          true,
		SLABreachWindow:          "30s",
	}, &goalControllerState{breachStartedAt: &breachStarted})

	if !update.ShouldStop {
		t.Fatal("expected sustained server telemetry SLA breach to trigger stop")
	}
	if update.Status == nil || !update.Status.ServerProcessingBreached || !update.Status.TransferBreached {
		t.Fatalf("expected goal status to mark server telemetry breaches, got %#v", update.Status)
	}
	if update.StopReason != "server processing and transfer SLA breached" {
		t.Fatalf("expected server telemetry stop reason, got %q", update.StopReason)
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

func TestDistributeTargetVUsersForGoalUsesTargetTPSWeights(t *testing.T) {
	targets := distributeTargetVUsersForGoal([]TestGroup{
		{Name: "buyers", VUsers: 10},
		{Name: "admins", VUsers: 90},
	}, map[string]int{
		"buyers": 10,
		"admins": 90,
	}, 100, map[string]GoalTargetStatus{
		"buyers": {Group: "buyers", TargetTPS: 70},
		"admins": {Group: "admins", TargetTPS: 30},
	})

	if targets["buyers"] != 70 || targets["admins"] != 30 {
		t.Fatalf("weighted targets = %#v, want buyers=70 admins=30", targets)
	}
}

func TestApplyRuntimeGoalTargetsToLiveMetricsOverlaysAggregateAndGroupTargets(t *testing.T) {
	targetTPS := 150.0
	updatedAt := time.Date(2026, time.June, 8, 12, 0, 0, 0, time.UTC)
	testRun := &ManagedTest{
		ScenarioMode: "goal_oriented",
		GoalConfig:   &GoalConfig{TargetAverageTPS: &targetTPS},
		Groups: []TestGroup{
			{Name: "buyers", VUsers: 30},
			{Name: "admins", VUsers: 10},
		},
		GoalTargets: map[string]GoalTargetStatus{
			"buyers": {Group: "buyers", TargetTPS: 120, UpdatedAt: updatedAt},
			"admins": {Group: "admins", TargetTPS: 60, UpdatedAt: updatedAt},
		},
	}
	live := &LiveMetrics{
		Timestamp: time.Date(2026, time.June, 8, 12, 0, 1, 0, time.UTC),
		TPS:       150,
		GroupMetrics: map[string]*GroupLiveMetrics{
			"buyers": {Name: "buyers", TPS: 100, VUsers: 30},
			"admins": {Name: "admins", TPS: 50, VUsers: 10},
		},
	}

	applyRuntimeGoalTargetsToLiveMetrics(testRun, live)

	if live.TargetTPS != 180 {
		t.Fatalf("target tps = %v, want 180", live.TargetTPS)
	}
	if live.ThroughputGapPct < 16 || live.ThroughputGapPct > 17 || !live.ThroughputTargetMissed {
		t.Fatalf("throughput gap = %v missed=%v, want about 16.7 and missed", live.ThroughputGapPct, live.ThroughputTargetMissed)
	}
	if live.GroupMetrics["buyers"].TargetTPS != 120 || live.GroupMetrics["admins"].TargetTPS != 60 {
		t.Fatalf("group target tps not overlaid: %#v", live.GroupMetrics)
	}
	if !live.GroupMetrics["buyers"].ThroughputTargetMissed {
		t.Fatal("expected buyers target gap to be marked missed")
	}
}
