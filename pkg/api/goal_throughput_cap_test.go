package api

import (
	"context"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/trafficprofile"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func goalCapTestRun() *ManagedTest {
	target := 20.0
	return &ManagedTest{
		ScenarioMode: "goal_oriented",
		GoalConfig:   &GoalConfig{TargetAverageTPS: &target},
		Groups: []TestGroup{
			{Name: "Store_Browse", VUsers: 2},
			{Name: "Store_Cart", VUsers: 2},
			{Name: "Canary", VUsers: 1, Role: GroupRoleObserver},
		},
		goalControl: &goalControllerState{},
	}
}

func TestGoalThroughputCapsHoldEachGoalGroupToItsTarget(t *testing.T) {
	test := goalCapTestRun()
	caps := goalThroughputCapsLocked(test, time.Now())
	if len(caps) != 2 || caps["Store_Browse"] <= 0 || caps["Store_Cart"] <= 0 {
		t.Fatalf("caps = %v, want the two load groups capped", caps)
	}
	if _, observer := caps["Canary"]; observer {
		t.Fatal("an observer canary must never be throughput-capped")
	}
	if total := caps["Store_Browse"] + caps["Store_Cart"]; math.Abs(total-20) > 1e-9 {
		t.Fatalf("caps sum to %v, want the 20 TPS goal", total)
	}
	test.ScenarioMode = "vuser_driven"
	if caps := goalThroughputCapsLocked(test, time.Now()); caps != nil {
		t.Fatalf("a VUser-driven run must not be capped, got %v", caps)
	}
}

// A traffic-profile run starts at its first point: before the scheduler
// applies it, the seed VUsers would otherwise run uncapped (the 103.5 req/s
// start against a 2 TPS profile).
func TestGoalThroughputCapsStartTrafficProfileAtFirstPoint(t *testing.T) {
	test := goalCapTestRun()
	test.TrafficProfile = &TrafficProfile{
		RuntimeTarget: "tps",
		Mappings: []TrafficProfileMapping{
			{GroupName: "Store_Browse", Points: []trafficprofile.RuntimePoint{{OffsetMS: 0, Target: 1}, {OffsetMS: 12000, Target: 1.5}}},
			{GroupName: "Store_Cart", Points: []trafficprofile.RuntimePoint{{OffsetMS: 0, Target: 1}}},
		},
	}
	initial := goalThroughputCapsLocked(test, time.Now())
	if initial["Store_Browse"] != 1 || initial["Store_Cart"] != 1 {
		t.Fatalf("initial caps = %v, want each group at its first profile point", initial)
	}
	// Once the scheduler has applied a group's point, the applied target wins,
	// and a group whose point is not applied yet stays at its first point
	// rather than being suspended by the other group's sync.
	test.GoalTargets = map[string]GoalTargetStatus{"Store_Browse": {Group: "Store_Browse", TargetTPS: 4}}
	if caps := goalThroughputCapsLocked(test, time.Now()); caps["Store_Browse"] != 4 || caps["Store_Cart"] != 1 {
		t.Fatalf("caps = %v, want the applied 4 TPS target and Store_Cart still at its first point", caps)
	}

	groups := []engine.GroupConfig{{Name: "Store_Browse"}, {Name: "Store_Cart"}, {Name: "Canary"}}
	applyInitialGoalThroughputCaps(groups, initial)
	if !groups[0].ThroughputCapEnabled || groups[0].ThroughputCapTPS != 1 || groups[2].ThroughputCapEnabled {
		t.Fatalf("engine groups = %+v, want the profile groups capped and the canary not", groups)
	}
}

func TestThroughputCapShareSplitsByShardVUsers(t *testing.T) {
	if got := throughputCapShare(12, 1, 4, 2); got != 3 {
		t.Fatalf("share of 1 of 4 VUsers at 12 TPS = %v, want 3", got)
	}
	if got := throughputCapShare(12, 0, 0, 3); got != 4 {
		t.Fatalf("even split before VUsers = %v, want 4", got)
	}
	if got := throughputCapShare(0, 2, 4, 2); got != 0 {
		t.Fatalf("a suspended group's share = %v, want 0", got)
	}
}

func TestGoalWindowAveragesTenSecondsOncePerSample(t *testing.T) {
	state := &goalControllerState{}
	start := time.Date(2026, 10, 12, 6, 36, 0, 0, time.UTC)
	for second := 0; second < 15; second++ {
		tps := 20.0
		if second >= 10 {
			tps = 30
		}
		sample := goalThroughputSample{at: start.Add(time.Duration(second) * time.Second), run: tps, runTarget: 20, groups: map[string]float64{"g": tps / 2}, groupTargets: map[string]float64{"g": 10}}
		window := goalWindowMeans(state, sample)
		if second == 14 {
			// The last 10 s hold seconds 5–14: five at 20 and five at 30.
			if math.Abs(window.run-25) > 1e-9 || math.Abs(window.groups["g"]-12.5) > 1e-9 || window.runTarget != 20 {
				t.Fatalf("window = %+v, want 25 run and 12.5 group TPS against 20", window)
			}
		}
		recordGoalThroughputSample(state, sample)
	}
	// Re-recording the same sample adds nothing, and reading the window records nothing.
	before := len(state.throughputWindow)
	recordGoalThroughputSample(state, goalThroughputSample{at: start.Add(14 * time.Second), run: 999})
	goalWindowMeans(state, goalThroughputSample{at: start.Add(15 * time.Second), run: 999})
	if len(state.throughputWindow) != before {
		t.Fatal("the window grew without a newly published sample")
	}
}

// A traffic profile steps its target every few seconds. Each sample is judged
// against the target it ran under: stepping 10 → 8 while delivering exactly
// each target must read as on target, not 25% over.
func TestGoalWindowJudgesEachSampleAgainstItsOwnTarget(t *testing.T) {
	state := &goalControllerState{}
	start := time.Date(2026, 10, 12, 6, 36, 0, 0, time.UTC)
	for second := 0; second < 10; second++ {
		target := 10.0
		if second >= 5 {
			target = 8
		}
		sample := goalThroughputSample{at: start.Add(time.Duration(second) * time.Second), run: target, runTarget: target, groups: map[string]float64{"g": target}, groupTargets: map[string]float64{"g": target}}
		window := goalWindowMeans(state, sample)
		if gap := signedGoalGapPct(window.run, window.runTarget); gap == nil || math.Abs(*gap) > 1e-9 {
			t.Fatalf("second %d: gap = %v, want on target", second, gap)
		}
		if gap := signedGoalGapPct(window.groups["g"], window.groupTargets["g"]); gap == nil || math.Abs(*gap) > 1e-9 {
			t.Fatalf("second %d: group gap = %v, want on target", second, gap)
		}
		recordGoalThroughputSample(state, sample)
	}
}

// An observer canary carries no target, so its traffic is not goal throughput.
func TestGoalThroughputSampleCountsOnlyGoalGroups(t *testing.T) {
	test := goalCapTestRun()
	live := &LiveMetrics{Timestamp: time.Now(), TPS: 21, GroupMetrics: map[string]*GroupLiveMetrics{
		"Store_Browse": {TPS: 10}, "Store_Cart": {TPS: 10}, "Canary": {TPS: 1},
	}}
	sample := goalThroughputSampleOf(test, live, currentGoalTargetStatuses(test, live.Timestamp))
	if sample.run != 20 || sample.runTarget != 20 {
		t.Fatalf("sample = %+v, want 20 TPS against 20 without the canary", sample)
	}
}

func TestSignedGoalGapIsPositiveUnderAndNegativeOver(t *testing.T) {
	if gap := signedGoalGapPct(17, 20); gap == nil || math.Abs(*gap-15) > 1e-9 {
		t.Fatalf("gap for 17 against 20 = %v, want +15 (under)", gap)
	}
	if gap := signedGoalGapPct(27.8, 20); gap == nil || math.Abs(*gap+39) > 1e-9 {
		t.Fatalf("gap for 27.8 against 20 = %v, want -39 (over)", gap)
	}
	if signedGoalGapPct(10, 0) != nil {
		t.Fatal("no target means no gap")
	}
}

// The goal run of the review ran 39% over its goal for a minute and only a
// shortfall ever warned. Both directions now warn, over the 10 s window.
func TestGoalRunWarnsWhenOverAndUnderTarget(t *testing.T) {
	test := goalCapTestRun()
	over := -39.0
	alerts := buildThresholdAlerts(test, &LiveMetrics{TargetTPS: 20, GoalWindowTPS: 27.8, GoalGapPct: &over, TPS: 26}, ThresholdSettings{})
	alert := findAlert(alerts, "throughput_over_pct")
	if alert == nil || alert["value"] != 27.8 || alert["window"] != goalThroughputWindowLabel || alert["gap_pct"] != 39.0 {
		t.Fatalf("over-target alert = %v", alerts)
	}
	event := thresholdAlertNotificationEvent(test, "RUNNING", alert, time.Now())
	if !strings.Contains(event.Summary, "27.80 TPS (10 s avg) is above target 20.00 TPS (39.00% over)") {
		t.Fatalf("summary = %q", event.Summary)
	}

	under := 15.0
	alerts = buildThresholdAlerts(test, &LiveMetrics{TargetTPS: 20, GoalWindowTPS: 17, GoalGapPct: &under, TPS: 21.8, ThroughputTargetMissed: true}, ThresholdSettings{})
	if alert := findAlert(alerts, "throughput_gap_pct"); alert == nil || alert["value"] != 17.0 {
		t.Fatalf("under-target alert = %v, want the 10 s figure, not the last sample", alerts)
	}

	near := 3.0
	if alerts := buildThresholdAlerts(test, &LiveMetrics{TargetTPS: 20, GoalWindowTPS: 19.4, GoalGapPct: &near, ThroughputTargetMissed: true}, ThresholdSettings{}); findAlert(alerts, "throughput_gap_pct") != nil {
		t.Fatal("a goal run within 5% of its target over the window must not warn on one noisy sample")
	}
}

func findAlert(alerts []map[string]any, metric string) map[string]any {
	for _, alert := range alerts {
		if alert["metric"] == metric {
			return alert
		}
	}
	return nil
}

func TestGoalAttainmentJudgesTenSecondWindows(t *testing.T) {
	start := time.Date(2026, 10, 12, 6, 36, 0, 0, time.UTC)
	var summaries []IntervalSummaryRecord
	for second := 0; second < 120; second++ {
		tps := 21.0
		if second >= 60 {
			tps = 27.8
		}
		summaries = append(summaries, IntervalSummaryRecord{Timestamp: start.Add(time.Duration(second) * time.Second), TPS: tps, TargetTPS: 20})
	}
	attainment := buildRunArchiveGoalAttainment(summaries, nil)
	if attainment == nil || attainment.Windows != 12 || attainment.WithinWindows != 6 || attainment.OverWindows != 6 || attainment.UnderWindows != 0 {
		t.Fatalf("attainment = %+v, want 6 within and 6 over of 12 windows", attainment)
	}
	if math.Abs(attainment.AchievedAverageTPS-24.4) > 1e-9 || attainment.TargetAverageTPS != 20 {
		t.Fatalf("averages = %+v, want 24.4 delivered against 20", attainment)
	}
	if buildRunArchiveGoalAttainment([]IntervalSummaryRecord{{Timestamp: start, TPS: 50}}, nil) != nil {
		t.Fatal("a run without a TPS target has no attainment")
	}

	report := &RunArchiveReport{}
	report.Overview.GoalAttainment = attainment
	concerns := strings.Join(reportConditionalConcerns(report), "; ")
	if !strings.Contains(concerns, "throughput was off its goal in 6 of 12 10-second windows (6 over, 0 under)") {
		t.Fatalf("concerns = %q", concerns)
	}
	findings := deriveReportFindings(report)
	if len(findings) == 0 || findings[0].Title != "Throughput above its goal" ||
		!strings.Contains(findings[0].Observed, "The goal averaged 20.0 TPS and the run delivered 24.4 TPS") {
		t.Fatalf("findings = %+v", findings)
	}
}

type capRecordingClient struct {
	raceFakeGeneratorClient
	mu    sync.Mutex
	calls []generatorrpc.ThroughputCap
	err   error
}

func (c *capRecordingClient) SetThroughputCap(_ context.Context, request generatorrpc.ThroughputCap) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, request)
	return c.err
}

func (c *capRecordingClient) sent() []generatorrpc.ThroughputCap {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]generatorrpc.ThroughputCap(nil), c.calls...)
}

// A distributed goal group's cap is split by each generator's share of its
// VUsers, re-sent only when a share changes, and sent again in full after an
// evacuation (a replacement shard starts uncapped, maybe on a generator that
// held the group before).
func TestDistributedGoalCapsSplitBySharesAndResyncAfterEvacuation(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	a, b := &capRecordingClient{}, &capRecordingClient{}
	test := goalCapTestRun()
	test.ID = "distributed-goal-caps"
	test.Status = "RUNNING"
	test.executionGeneration = 7
	test.remoteGroupShards = map[string][]GeneratorShardSummary{
		"Store_Browse": {{Group: "Store_Browse", Target: "a", VUsers: 1}, {Group: "Store_Browse", Target: "b", VUsers: 3}},
	}
	test.remoteClientsByTarget = map[string]generatorClient{"a": a, "b": b}
	runner.tests[test.ID] = test
	backend := &distributedExecutionBackend{timeout: time.Second}

	// The run's goal shares 20 TPS by VUsers: Store_Browse holds 10.
	caps := goalThroughputCapsLocked(test, time.Now())
	backend.SetGroupThroughputCaps(runner, test, caps)
	if got := a.sent(); len(got) != 1 || got[0].TPS != 2.5 || got[0].GroupName != "Store_Browse" || got[0].ExecutionGeneration != 7 || !got[0].Enabled {
		t.Fatalf("generator a got %+v, want a 2.5 TPS share (1 of 4 VUsers)", got)
	}
	if got := b.sent(); len(got) != 1 || got[0].TPS != 7.5 {
		t.Fatalf("generator b got %+v, want a 7.5 TPS share", got)
	}
	backend.SetGroupThroughputCaps(runner, test, caps)
	if len(a.sent()) != 1 || len(b.sent()) != 1 {
		t.Fatal("an unchanged share was sent again")
	}

	// A sync through the runner with nothing changed sends nothing; after an
	// evacuation every share goes out again.
	test.executionBackend = backend
	runner.syncGoalThroughputCaps(test.ID)
	if len(a.sent()) != 1 || len(b.sent()) != 1 {
		t.Fatalf("a sync with unchanged shares sent again: a=%d b=%d", len(a.sent()), len(b.sent()))
	}
	runner.resyncGoalThroughputCaps(test.ID)
	if len(a.sent()) != 2 || len(b.sent()) != 2 {
		t.Fatalf("after an evacuation resync a=%d b=%d sends, want every share re-sent", len(a.sent()), len(b.sent()))
	}

	// A peer without the call is not asked again on every change.
	b.err = status.Error(codes.Unimplemented, "unknown method")
	test.remoteGroupShards["Store_Browse"][1].VUsers = 1
	backend.SetGroupThroughputCaps(runner, test, caps)
	backend.SetGroupThroughputCaps(runner, test, caps)
	if got := len(b.sent()); got != 3 {
		t.Fatalf("an Unimplemented peer was called %d times, want once more", got)
	}
}

// Applying one traffic-profile group's point must not store zero placeholders
// for the groups whose points are still to come: those read as applied and
// suspended the other groups at the start of the run.
func TestProfilePointDoesNotStorePlaceholderTargets(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	test := goalCapTestRun()
	test.ID = "profile-placeholders"
	test.Status = "RUNNING"
	test.TrafficProfile = &TrafficProfile{
		RuntimeTarget: "tps",
		Mappings: []TrafficProfileMapping{
			{GroupName: "Store_Browse", Points: []trafficprofile.RuntimePoint{{OffsetMS: 0, Target: 1}}},
			{GroupName: "Store_Cart", Points: []trafficprofile.RuntimePoint{{OffsetMS: 0, Target: 1}}},
		},
	}
	runner.tests[test.ID] = test
	if _, err := runner.updateGoalTargetTPS(test.ID, "Store_Browse", 1, true); err != nil {
		t.Fatalf("updateGoalTargetTPS: %v", err)
	}
	if _, stored := test.GoalTargets["Store_Cart"]; stored {
		t.Fatalf("goal targets = %+v, want no placeholder for Store_Cart", test.GoalTargets)
	}
	runner.mu.RLock()
	caps := goalThroughputCapsLocked(test, time.Now())
	runner.mu.RUnlock()
	if caps["Store_Cart"] != 1 {
		t.Fatalf("caps = %v, want Store_Cart held at its first point", caps)
	}
}

func TestGoalAttainmentLeavesObserverTrafficOut(t *testing.T) {
	start := time.Date(2026, 10, 12, 6, 36, 0, 0, time.UTC)
	var summaries []IntervalSummaryRecord
	for second := 0; second < 20; second++ {
		summaries = append(summaries, IntervalSummaryRecord{
			Timestamp: start.Add(time.Duration(second) * time.Second), TPS: 21, TargetTPS: 20,
			Groups: map[string]*GroupLiveMetrics{"Store_Browse": {TPS: 20}, "Canary": {TPS: 1}},
		})
	}
	attainment := buildRunArchiveGoalAttainment(summaries, map[string]bool{"Canary": true})
	if attainment == nil || attainment.AchievedAverageTPS != 20 || attainment.WithinWindows != 2 {
		t.Fatalf("attainment = %+v, want 20 TPS delivered and both windows on goal", attainment)
	}
}

func TestUntargetedGoalGroupsCoverObserversAndUnmappedProfileGroups(t *testing.T) {
	provenance := RunArchiveProvenance{
		Groups: []TestGroup{{Name: "Store_Browse"}, {Name: "Store_Inventory"}, {Name: "Canary", Role: GroupRoleObserver}},
		TrafficProfile: &TrafficProfile{RuntimeTarget: "tps", Mappings: []TrafficProfileMapping{{GroupName: "Store_Browse"}}},
	}
	got := untargetedGoalGroups(provenance)
	if len(got) != 2 || !got["Store_Inventory"] || !got["Canary"] {
		t.Fatalf("untargeted = %v, want the unmapped group and the canary", got)
	}
	provenance.TrafficProfile = nil
	if got := untargetedGoalGroups(provenance); len(got) != 1 || !got["Canary"] {
		t.Fatalf("goal-driven untargeted = %v, want only the canary", got)
	}
}
