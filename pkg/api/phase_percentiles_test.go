package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/engine"
)

// phaseGeneratorAggregate is a generator's checkout aggregate with phase
// observations and, when sketched, the matching phase sketches.
func TestPhaseSLARuleHasItsOwnFindingAndRuleID(t *testing.T) {
	whole := TransactionSLARule{Transaction: "Checkout", Metric: "p95_duration_ms", Comparator: "lt", Threshold: 2000}
	connect := whole
	connect.Phase, connect.Threshold = "connect", 50
	observed := 120.0
	report := &RunArchiveReport{Manifest: &RunArchiveManifest{TransactionRuleResults: []TransactionSLARuleResult{
		{Rule: whole, Status: "fail", Observed: &observed, MatchedCount: 10},
		{Rule: connect, Status: "fail", Observed: &observed, MatchedCount: 4},
	}}}
	findings := agentSLARuleFindings("run-1", report)
	if len(findings) != 2 || findings[0].ID == findings[1].ID || findings[0].RuleID == findings[1].RuleID {
		t.Fatalf("phase and whole-duration rules collide: %+v", findings)
	}
	if !strings.Contains(findings[1].Title, "connect") || strings.Contains(findings[0].Title, "connect") {
		t.Fatalf("titles = %q / %q", findings[0].Title, findings[1].Title)
	}
	if got := transactionSLAMetricLabel(connect); got != "connect p95_duration_ms" {
		t.Fatalf("label = %q", got)
	}
	// Phase rules leave no streak, count or coverage behind when the window
	// carries no phase percentiles.
	policies := ThresholdSettings{PhaseLatency: []PhaseLatencyThreshold{{Phase: "tls", Percentile: 95, MaxMs: 10}}}
	if thresholdSampleCovered(&LiveMetrics{TotalTx: 5}, policies) {
		t.Fatal("a tick without phase percentiles does not evaluate phase rules")
	}
	if !thresholdSampleCovered(&LiveMetrics{TotalTx: 5, PhasePercentiles: &engine.PhasePercentileSet{Version: 1}}, policies) {
		t.Fatal("a tick with phase percentiles evaluates phase rules")
	}
}

func phaseGeneratorAggregate(connectMs float64, sketched bool) generatorrpc.MetricAggregate {
	var recorder engine.PhaseSketchRecorder
	var observed engine.PhaseObservations
	samples := [][5]float64{{2, connectMs, 20, 150, 180}, {0, 0, 0, 40, 50}}
	for _, s := range samples {
		recorder.ObserveMs(s[0], s[1], s[2], s[3], s[4])
		observed.ObserveMs(s[0], s[1], s[2], s[3], s[4])
	}
	aggregate := generatorrpc.MetricAggregate{Name: "checkout", Count: 2, AvgMs: 115, AvgDNSMs: 1, AvgConnectMs: connectMs / 2, AvgTLSMs: 10, AvgTTFBMs: 95, PhaseObserved: &observed}
	if sketched {
		aggregate.PhaseSketches = recorder.Snapshot()
	}
	return aggregate
}

func phaseBatch(connectMs float64, version uint32) generatorrpc.MetricsBatch {
	sketched := version == engine.PhaseSketchVersion
	fleet := phaseGeneratorAggregate(connectMs, sketched)
	fleet.Name = "fleet"
	return generatorrpc.MetricsBatch{
		WindowMs:           1000,
		PhaseSketchVersion: version,
		FleetMetrics:       &fleet,
		TransactionMetrics: []generatorrpc.MetricAggregate{phaseGeneratorAggregate(connectMs, sketched)},
	}
}

func liveJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestDistributedLiveMetricsExposePhasePercentiles(t *testing.T) {
	combined, fleetTPS, groupTPS := aggregateDistributedMetricsBatches("t", map[string]generatorrpc.MetricsBatch{
		"gen-a": phaseBatch(30, engine.PhaseSketchVersion),
		"gen-b": phaseBatch(900, engine.PhaseSketchVersion),
	})
	if combined.PhaseSketchVersion != engine.PhaseSketchVersion {
		t.Fatalf("combined version = %d", combined.PhaseSketchVersion)
	}
	live := buildLiveMetricsFromDistributedBatch(&ManagedTest{}, combined, fleetTPS, groupTPS)
	set := live.PhasePercentiles
	if set == nil || set.Connect == nil || set.Connect.Samples != 2 || set.Server == nil || set.Server.Samples != 4 {
		t.Fatalf("fleet phase percentiles = %+v", set)
	}
	// Two connects, 30 and 900 ms: P99 sits near the slow one.
	if p99, ok := set.Connect.Value(99); !ok || p99 < 850 || p99 > 905 {
		t.Fatalf("connect P99 = %v", p99)
	}
	if set.Download == nil || set.DNS == nil || set.DNS.Samples != 2 {
		t.Fatalf("phases = %+v", set)
	}
	tx := live.Transactions["checkout"]
	if tx == nil || tx.PhasePercentiles == nil || tx.PhasePercentiles.TLS == nil || tx.PhasePercentiles.TLS.Samples != 2 {
		t.Fatalf("transaction phase percentiles = %+v", tx)
	}
	body := liveJSON(t, live)
	if !strings.Contains(body, `"phase_percentiles":{"version":1,"dns":{"samples":2`) {
		t.Fatalf("live JSON lacks phase percentiles: %s", body)
	}
}

func TestDistributedPhasePercentilesUnknownWithAnOlderGenerator(t *testing.T) {
	combined, fleetTPS, groupTPS := aggregateDistributedMetricsBatches("t", map[string]generatorrpc.MetricsBatch{
		"gen-a": phaseBatch(30, engine.PhaseSketchVersion),
		"gen-b": phaseBatch(900, 0), // predates phase sketches: sends none
	})
	if combined.PhaseSketchVersion != 0 {
		t.Fatalf("combined version = %d, want 0 with an older generator", combined.PhaseSketchVersion)
	}
	live := buildLiveMetricsFromDistributedBatch(&ManagedTest{}, combined, fleetTPS, groupTPS)
	if live.PhasePercentiles != nil || live.Transactions["checkout"].PhasePercentiles != nil {
		t.Fatalf("a fleet with an older generator must report unknown phase percentiles, got %+v", live.PhasePercentiles)
	}
	if live.PhaseObserved == nil {
		t.Fatal("phase observations stay known")
	}
	if body := liveJSON(t, live); strings.Contains(body, "phase_percentiles") {
		t.Fatalf("unknown phase percentiles must be absent, never zero: %s", body)
	}
}

func TestLocalLiveMetricsExposePhasePercentiles(t *testing.T) {
	var fleet, checkout engine.PhaseSketchRecorder
	for _, s := range [][5]time.Duration{{4 * time.Millisecond, 10 * time.Millisecond, 30 * time.Millisecond, 100 * time.Millisecond, 120 * time.Millisecond}, {0, 0, 0, 20 * time.Millisecond, 25 * time.Millisecond}} {
		fleet.Observe(s[0], s[1], s[2], s[3], s[4])
		checkout.Observe(s[0], s[1], s[2], s[3], s[4])
	}
	snap := engine.MetricsSnapshot{
		Transactions:  2,
		PhaseSketches: fleet.Snapshot(),
		ByTransaction: map[string]*engine.ActionMetrics{"checkout": {Name: "checkout", Count: 2, PhaseSketches: checkout.Snapshot()}},
	}
	live := buildLiveMetricsFromSnapshot(snap, nil)
	if live.PhasePercentiles == nil || live.PhasePercentiles.TLS == nil || live.PhasePercentiles.Server.Samples != 2 {
		t.Fatalf("local fleet phase percentiles = %+v", live.PhasePercentiles)
	}
	if tx := live.Transactions["checkout"]; tx == nil || tx.PhasePercentiles == nil || tx.PhasePercentiles.Connect.Samples != 1 {
		t.Fatalf("local transaction phase percentiles = %+v", tx)
	}
	// A snapshot without sketches reports none.
	snap.PhaseSketches = nil
	if live := buildLiveMetricsFromSnapshot(snap, nil); live.PhasePercentiles != nil {
		t.Fatalf("no sketches = %+v", live.PhasePercentiles)
	}
}

func phaseLive(connectP95 float64, known bool) *LiveMetrics {
	live := &LiveMetrics{Timestamp: time.Date(2026, time.November, 10, 12, 0, 0, 0, time.UTC)}
	if known {
		live.PhasePercentiles = &engine.PhasePercentileSet{Version: 1, Connect: &engine.PhasePercentiles{Samples: 10, P50Ms: connectP95 / 2, P90Ms: connectP95, P95Ms: connectP95, P99Ms: connectP95}}
	}
	return live
}

func TestPhaseLatencyThresholdAlertsAndSustainedStop(t *testing.T) {
	runner := NewTestRunner(2, NewHub(), nil)
	samples := 3
	thresholds := ThresholdSettings{PhaseLatency: []PhaseLatencyThreshold{
		{Phase: "connect", Percentile: 95, MaxMs: 500, StopConsecutiveSamples: &samples},
		{Phase: "tls", Percentile: 99, MaxMs: 100}, // alert-only, and no TLS data in these samples
	}}
	test := &ManagedTest{ID: "phase", Name: "Checkout", Status: "RUNNING"}

	alerts := buildThresholdAlerts(test, phaseLive(800, true), thresholds)
	if len(alerts) != 1 || alerts[0]["metric"] != "phase_connect_p95_ms" || alerts[0]["value"] != 800.0 || alerts[0]["phase"] != "connect" {
		t.Fatalf("alerts = %#v", alerts)
	}
	if alerts := buildThresholdAlerts(test, phaseLive(800, false), thresholds); len(alerts) != 0 {
		t.Fatalf("unknown phase data must never alert, got %#v", alerts)
	}

	stop := func(live *LiveMetrics) bool {
		_, _, stopped := runner.shouldStopForSustainedPhaseLatency(test, live, thresholds)
		return stopped
	}
	if stop(phaseLive(800, true)) || stop(phaseLive(800, true)) {
		t.Fatal("stopped before 3 consecutive breaches")
	}
	// Unknown data (an older generator joined) is no data: it never counts
	// as a breach and starts the streak over.
	if stop(phaseLive(0, false)) {
		t.Fatal("unknown data stopped the run")
	}
	if test.phaseLatencyBreachConsecutive["phase_connect_p95_ms"] != 0 {
		t.Fatalf("unknown data kept the streak %v", test.phaseLatencyBreachConsecutive)
	}
	if stop(phaseLive(800, true)) || stop(phaseLive(800, true)) {
		t.Fatal("stopped before 3 consecutive breaches after the reset")
	}
	rule, value, stopped := runner.shouldStopForSustainedPhaseLatency(test, phaseLive(800, true), thresholds)
	if !stopped || rule.Phase != "connect" || value != 800 {
		t.Fatalf("third consecutive breach: stopped=%v rule=%+v value=%v", stopped, rule, value)
	}
	if reason := phaseLatencyStopReason(rule, value); !strings.Contains(reason, "connect P95") || !strings.Contains(reason, "3 consecutive samples") {
		t.Fatalf("reason = %q", reason)
	}

	// Many unknown samples in a row never stop a run.
	test.phaseLatencyBreachConsecutive = nil
	for i := 0; i < 10; i++ {
		if stop(phaseLive(0, false)) {
			t.Fatal("unknown data stopped the run")
		}
	}
	// A phase with no sample in the window is no data too.
	empty := &LiveMetrics{PhasePercentiles: &engine.PhasePercentileSet{Version: 1}}
	for i := 0; i < 10; i++ {
		if stop(empty) {
			t.Fatal("a phase with no samples stopped the run")
		}
	}
	// Not running: no streak.
	test.Status = "PAUSED"
	test.phaseLatencyBreachConsecutive = map[string]int{"phase_connect_p95_ms": 2}
	if stop(phaseLive(800, true)) || test.phaseLatencyBreachConsecutive != nil {
		t.Fatal("a paused run must not stop or keep a streak")
	}
}

func TestPhaseLatencyAlertActionNamesOnlyTheStoppingRule(t *testing.T) {
	test := &ManagedTest{ID: "phase", Name: "Checkout", Status: "RUNNING"}
	live := phaseLive(800, true)
	live.PhasePercentiles.DNS = &engine.PhasePercentiles{Samples: 3, P95Ms: 300}
	thresholds := ThresholdSettings{PhaseLatency: []PhaseLatencyThreshold{{Phase: "connect", Percentile: 95, MaxMs: 500}, {Phase: "dns", Percentile: 95, MaxMs: 100}}}
	alerts := buildThresholdAlerts(test, live, thresholds)
	applyThresholdAlertStopActions(alerts, false, false, "phase_connect_p95_ms")
	if len(alerts) != 2 || alerts[0]["action"] != "stop_requested" || alerts[1]["action"] != nil {
		t.Fatalf("alerts = %#v", alerts)
	}
}

func TestPhaseLatencyThresholdValidation(t *testing.T) {
	negative := -1
	for _, tc := range []struct {
		rule PhaseLatencyThreshold
		want string
	}{
		{PhaseLatencyThreshold{Phase: "ttfb", Percentile: 95, MaxMs: 1}, "phase must be one of"},
		{PhaseLatencyThreshold{Phase: "dns", Percentile: 75, MaxMs: 1}, "percentile must be"},
		{PhaseLatencyThreshold{Phase: "dns", Percentile: 95}, "max_ms must be"},
		{PhaseLatencyThreshold{Phase: "dns", Percentile: 95, MaxMs: 1, StopConsecutiveSamples: &negative}, "stop_consecutive_samples must be >= 0"},
	} {
		if err := validatePhaseLatencyThresholds([]PhaseLatencyThreshold{tc.rule}); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: err=%v, want %q", tc.rule, err, tc.want)
		}
	}
	dup := []PhaseLatencyThreshold{{Phase: "dns", Percentile: 95, MaxMs: 1}, {Phase: "dns", Percentile: 95, MaxMs: 2}}
	if err := validatePhaseLatencyThresholds(dup); err == nil || !strings.Contains(err.Error(), "duplicates") {
		t.Fatalf("duplicate rule err=%v", err)
	}

	runner := NewTestRunner(10, NewHub(), nil)
	settings := defaultSystemSettings(10)
	settings.Thresholds.PhaseLatency = []PhaseLatencyThreshold{{Phase: "server", Percentile: 99, MaxMs: 0}}
	if _, err := runner.UpdateSystemSettings(settings); err == nil || !strings.Contains(err.Error(), "thresholds.phase_latency[0].max_ms") {
		t.Fatalf("invalid rule accepted, err=%v", err)
	}
	samples := 60
	settings.Thresholds.PhaseLatency = []PhaseLatencyThreshold{{Phase: "connect", Percentile: 95, MaxMs: 500, StopConsecutiveSamples: &samples}}
	saved, err := runner.UpdateSystemSettings(settings)
	if err != nil {
		t.Fatalf("valid rule rejected: %v", err)
	}
	if len(saved.Thresholds.PhaseLatency) != 1 || *saved.Thresholds.PhaseLatency[0].StopConsecutiveSamples != 60 {
		t.Fatalf("saved = %+v", saved.Thresholds.PhaseLatency)
	}
	// A stored blob is not re-validated on load: evaluation skips a bad rule.
	usable := usablePhaseLatencyThresholds([]PhaseLatencyThreshold{{Phase: "bogus", Percentile: 95, MaxMs: 1}, {Phase: "tls", Percentile: 95, MaxMs: 1}})
	if len(usable) != 1 || usable[0].Phase != "tls" {
		t.Fatalf("usable = %+v", usable)
	}
}

func TestTransactionSLAPhaseRulesJudgeObservedPhaseSamples(t *testing.T) {
	event := func(duration, dns, connect, tls, ttfb float64) TransactionEvent {
		return TransactionEvent{ActionName: "Checkout", Group: "Shoppers", BusinessTransaction: true, Success: true, DurationMs: duration, DNSMs: dns, ConnectMs: connect, TLSMs: tls, TTFBMs: ttfb}
	}
	root := writeTransactionEvents(t,
		event(200, 5, 40, 60, 150),
		event(80, 0, 0, 0, 60),    // reused connection: no connect sample
		event(90, 0, 600, 0, 700), // slow connect
		event(3000, 0, 20, 0, 0),  // timeout: no first byte, so no server sample
		event(50, 0, 0, 0, 0),     // protocol without TTFB
	)
	manifest := RunArchiveManifest{TransactionsFile: "transactions.jsonl", Provenance: RunArchiveProvenance{GoalConfig: &GoalConfig{TransactionRules: []TransactionSLARule{
		{Transaction: "Checkout", Phase: "connect", Metric: "max_duration_ms", Comparator: "lte", Threshold: 500},
		{Transaction: "Checkout", Phase: "server", Metric: "max_duration_ms", Comparator: "lte", Threshold: 500},
		{Transaction: "Checkout", Phase: "tls", Metric: "average_duration_ms", Comparator: "lte", Threshold: 100},
		{Transaction: "Checkout", Phase: "dns", Metric: "minimum_count", Comparator: "gte", Threshold: 0},
	}}}}
	// minimum_count does not take a phase.
	if err := ValidateTransactionSLARules(manifest.Provenance.GoalConfig.TransactionRules); err == nil || !strings.Contains(err.Error(), "phase applies only") {
		t.Fatalf("phase on minimum_count validated: %v", err)
	}
	manifest.Provenance.GoalConfig.TransactionRules = manifest.Provenance.GoalConfig.TransactionRules[:3]
	if err := applyTransactionSLARules(root, &manifest); err != nil {
		t.Fatal(err)
	}
	for index, want := range []struct {
		status   string
		matched  int64
		observed float64
	}{{"fail", 3, 600}, {"pass", 3, 100}, {"pass", 1, 60}} {
		got := manifest.TransactionRuleResults[index]
		if got.Status != want.status || got.MatchedCount != want.matched || got.Observed == nil || *got.Observed != want.observed {
			t.Errorf("rule %d = %s matched %d observed %v (%s), want %s %d %v", index, got.Status, got.MatchedCount, got.Observed, got.Message, want.status, want.matched, want.observed)
		}
	}
	if msg := manifest.TransactionRuleResults[0].Message; !strings.Contains(msg, "connect max_duration_ms") {
		t.Fatalf("message = %q", msg)
	}

	// No sample spent time in the phase: not evaluated, never a pass or fail.
	root = writeTransactionEvents(t, event(80, 0, 0, 0, 60))
	manifest.TransactionRuleResults, manifest.SLABreached, manifest.SLADetails = nil, false, nil
	manifest.Provenance.GoalConfig.TransactionRules = []TransactionSLARule{{Transaction: "Checkout", Phase: "tls", Metric: "p95_duration_ms", Comparator: "lte", Threshold: 1}}
	if err := applyTransactionSLARules(root, &manifest); err != nil {
		t.Fatal(err)
	}
	if got := manifest.TransactionRuleResults[0]; got.Status != "not_evaluated" || manifest.SLABreached {
		t.Fatalf("no phase samples = %+v", got)
	}

	for _, phase := range engine.NetworkPhases {
		if err := ValidateTransactionSLARules([]TransactionSLARule{{Transaction: "x", Phase: phase, Metric: "p95_duration_ms", Comparator: "lte", Threshold: 1}}); err != nil {
			t.Fatalf("engine phase %q is not a valid rule phase: %v", phase, err)
		}
	}
	if err := ValidateTransactionSLARules([]TransactionSLARule{{Transaction: "x", Phase: "ttfb", Metric: "p95_duration_ms", Comparator: "lte", Threshold: 1}}); err == nil {
		t.Fatal("an unknown phase validated")
	}
}
