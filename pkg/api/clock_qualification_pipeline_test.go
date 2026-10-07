package api

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
)

// clockPipelineRun is a distributed run on two generators whose clocks the
// controller has calibrated, with the default telemetry budgets (transport
// lag 500 ms, sample age 2,000 ms).
func clockPipelineRun(t *testing.T, calibrate func(*clockCalibrations, time.Time)) (*TestRunner, *distributedExecutionBackend, *ManagedTest) {
	t.Helper()
	runner := NewTestRunner(1, NewHub(), nil)
	run := &ManagedTest{ID: "clock-pipeline", Status: "RUNNING", remoteClientsByTarget: map[string]generatorClient{"gen-a": nil, "gen-b": nil}}
	run.clockCalibrations = newClockCalibrations(30 * time.Second)
	calibrate(run.clockCalibrations, time.Now())
	runner.tests[run.ID] = run
	return runner, &distributedExecutionBackend{}, run
}

// publishClockPipeline receives one publication per generator as the
// metrics stream does, aggregates them and publishes the live point.
func publishClockPipeline(t *testing.T, runner *TestRunner, backend *distributedExecutionBackend, run *ManagedTest, batches map[string]generatorrpc.MetricsBatch) *LiveMetrics {
	t.Helper()
	var live *LiveMetrics
	for _, target := range []string{"gen-a", "gen-b"} {
		batch, ok := batches[target]
		if !ok {
			continue
		}
		qualifyRemoteMetricsReceipt(runner, run, target, &batch, time.Now())
		live, _, _ = backend.applyRemoteMetricsBatch(runner, run, target, batch)
	}
	if live == nil || !runner.publishLiveMetrics(run, live, nil) {
		t.Fatal("publication rejected")
	}
	return live
}

// generatorTelemetry builds a publication on a generator whose clock runs
// skewMs ahead of the controller's: its true transport lag and sample age
// are transportMs and ageMs.
func generatorTelemetry(now time.Time, skewMs, transportMs, ageMs int64) generatorrpc.MetricsBatch {
	generatorNow := now.UnixMilli() + skewMs
	return generatorrpc.MetricsBatch{TestID: "clock-pipeline", TimestampUnixMs: generatorNow, Telemetry: &generatorrpc.MetricsTelemetry{
		PublishedAtUnixMs: generatorNow - transportMs,
		CollectedAtUnixMs: generatorNow - ageMs,
	}}
}

// A definite breach on one generator is scored even when another generator
// is far less certain or uncalibrated: the fleet figure keeps the lower bound
// the qualified generator proves. The oldest sample is chosen on the
// controller's clock, not by comparing raw generator clocks.
func TestClockPipelineKeepsADefiniteBreachAcrossGenerators(t *testing.T) {
	for name, calibrateB := range map[string]func(*clockCalibrations, time.Time){
		"remote region": func(c *clockCalibrations, now time.Time) { c.observe("gen-b", 0, 600*time.Millisecond, now) },
		"uncalibrated":  func(*clockCalibrations, time.Time) {},
	} {
		t.Run(name, func(t *testing.T) {
			runner, backend, run := clockPipelineRun(t, func(c *clockCalibrations, now time.Time) {
				c.observe("gen-a", 3*time.Second, time.Millisecond, now)
				calibrateB(c, now)
			})
			now := time.Now()
			// gen-a, 3 s ahead: 1,000 ms transport lag and a 2,500 ms old
			// sample — both over budget. gen-b: 300 ms and 200 ms.
			live := publishClockPipeline(t, runner, backend, run, map[string]generatorrpc.MetricsBatch{
				"gen-a": generatorTelemetry(now, 3000, 1000, 2500),
				"gen-b": generatorTelemetry(now, 0, 300, 200),
			})
			health := run.TelemetryHealth
			if health == nil || !slices.Contains(health.Conditions, "transport_lag") || !slices.Contains(health.Conditions, "stale_sample") {
				t.Fatalf("definite breaches hidden: health %+v telemetry %+v", health, live.Telemetry)
			}
			if bound := live.Telemetry.TransportLagQuality.LowerBoundMs; bound == nil || *bound < 990 || *bound > 1010 {
				t.Fatalf("transport lag lower bound %v", bound)
			}
			if bound := live.Telemetry.SampleAgeQuality.LowerBoundMs; bound == nil || *bound < 2400 || live.Telemetry.SampleAgeMs < 2400 {
				t.Fatalf("sample age %v bound %v: oldest sample chosen by raw generator clocks", live.Telemetry.SampleAgeMs, bound)
			}
		})
	}
}

// With no proven breach, an unqualified generator leaves the decision
// unscored: run health says so, coverage counts the interval unknown and the
// archive summary does not call the run healthy.
func TestClockPipelineUnscoredIsNotHealthy(t *testing.T) {
	runner, backend, run := clockPipelineRun(t, func(c *clockCalibrations, now time.Time) {
		c.observe("gen-a", 0, time.Millisecond, now)
	})
	now := time.Now()
	publishClockPipeline(t, runner, backend, run, map[string]generatorrpc.MetricsBatch{
		"gen-a": generatorTelemetry(now, 0, 100, 100),
		"gen-b": generatorTelemetry(now, 5000, 100, 100),
	})
	health := run.TelemetryHealth
	if health == nil || health.Status != "unscored" || len(health.Conditions) != 0 || !slices.Contains(health.Unscored, "transport_lag:uncalibrated") {
		t.Fatalf("unscored decision recorded as %+v", health)
	}
	if run.telemetryDiagnostic.LastSignature == "healthy" || run.telemetryDiagnostic.LastSignature == "" {
		t.Fatalf("unscored diagnostic was not published: %q", run.telemetryDiagnostic.LastSignature)
	}

	start := now.Add(-10 * time.Second)
	observed := &coverageSpans{}
	observed.add(start.UnixMilli(), now.UnixMilli())
	record := telemetryCoverageRecord(TelemetryCoverage{WindowStart: start, WindowEnd: now, WindowMs: 10_000}, observed, []IntervalSummaryRecord{{Timestamp: now, SampleWindowSeconds: 10, TelemetryStatus: health}}, nil)
	if record.HealthyMs != 0 || record.ClassifiedMs != 0 || record.UnknownMs != 10_000 {
		t.Fatalf("unscored interval classified: %+v", record)
	}
	summary := RunArchiveTelemetrySummary{}
	mergeRunArchiveTelemetrySummary(&summary, IntervalSummaryRecord{Timestamp: now, TelemetryStatus: health})
	if final := finalizeRunArchiveTelemetrySummary(summary); final.Status != "unscored" || final.UnscoredIntervals != 1 || final.HealthyIntervals != 0 {
		t.Fatalf("archive summary %+v", final)
	}
}

// A publication without a collection time falls back to the batch time,
// which is a generator's uncorrected clock: its age is never scored.
func TestClockPipelineFallbackCollectionTimeIsUnqualified(t *testing.T) {
	runner, backend, run := clockPipelineRun(t, func(c *clockCalibrations, now time.Time) {
		c.observe("gen-a", -3*time.Second, time.Millisecond, now)
	})
	now := time.Now()
	batch := generatorTelemetry(now, -3000, 100, 0)
	batch.Telemetry.CollectedAtUnixMs = 0
	live := publishClockPipeline(t, runner, backend, run, map[string]generatorrpc.MetricsBatch{"gen-a": batch})
	if live.Telemetry.SampleAgeQuality == nil || live.Telemetry.SampleAgeQuality.Qualified || slices.Contains(run.TelemetryHealth.Conditions, "stale_sample") {
		t.Fatalf("generator batch time scored as a sample age: %+v %+v", live.Telemetry, run.TelemetryHealth)
	}
}

// A batch qualified on receipt is no longer qualified once the controller's
// wall clock has stepped before it is published: its age measured now would
// include the step, so the stored batch neither scores a breach nor proves
// one.
func TestClockPipelineStepAfterReceiptIsNotABreach(t *testing.T) {
	runner, backend, run := clockPipelineRun(t, func(c *clockCalibrations, now time.Time) {
		c.observe("gen-a", 0, time.Millisecond, now)
		c.observe("gen-b", 0, time.Millisecond, now)
	})
	now := time.Now()
	batch := generatorTelemetry(now, 0, 100, 500)
	qualifyRemoteMetricsReceipt(runner, run, "gen-a", &batch, time.Now())
	if batch.Telemetry.CollectedAtQuality == nil || !batch.Telemetry.CollectedAtQuality.Qualified {
		t.Fatalf("receipt not qualified: %+v", batch.Telemetry.CollectedAtQuality)
	}
	// The wall clock stepped 3 s forward after the receipt: every wall time
	// recorded before the step now reads 3 s earlier than the clock.
	const step = 3000
	batch.TimestampUnixMs -= step
	batch.Telemetry.CollectedAtUnixMs -= step
	batch.Telemetry.PublishedAtUnixMs -= step
	batch.Telemetry.ControllerReceivedAtUnixMs -= step
	batch.Telemetry.ReceivedAt = batch.Telemetry.ReceivedAt.Add(-step * time.Millisecond)
	live, _, _ := backend.applyRemoteMetricsBatch(runner, run, "gen-a", batch)
	if live == nil || !runner.publishLiveMetrics(run, live, nil) {
		t.Fatal("publication rejected")
	}
	health := run.TelemetryHealth
	if health == nil || slices.Contains(health.Conditions, "stale_sample") || !slices.Contains(health.Unscored, "stale_sample:"+clockReasonStep) {
		t.Fatalf("a step after receipt scored the stored batch: health %+v telemetry %+v", health, live.Telemetry)
	}
	if live.Telemetry.SampleAgeQuality == nil || live.Telemetry.SampleAgeQuality.LowerBoundMs != nil {
		t.Fatalf("a stepped batch still proves a bound: %+v", live.Telemetry.SampleAgeQuality)
	}
	if wallClockSteppedSince(time.Now().Round(0), time.Now().Sub(clockProcessStart), time.Now()) {
		t.Fatal("no step reported as one")
	}
}

// With no sample reported (state-only publications at run start) there is
// no age to judge: no stale-sample decision, scored or unscored.
func TestClockPipelineNoSampleMakesNoAgeDecision(t *testing.T) {
	runner, backend, run := clockPipelineRun(t, func(c *clockCalibrations, now time.Time) {
		c.observe("gen-a", 0, time.Millisecond, now)
	})
	batch := generatorTelemetry(time.Now(), 0, 100, 0)
	batch.Telemetry.CollectedAtUnixMs = 0
	publishClockPipeline(t, runner, backend, run, map[string]generatorrpc.MetricsBatch{"gen-a": batch})
	health := run.TelemetryHealth
	if health == nil || health.Status != "healthy" || slices.ContainsFunc(health.Unscored, func(entry string) bool { return strings.HasPrefix(entry, "stale_sample") }) {
		t.Fatalf("a missing sample was judged: %+v", health)
	}
}

// Leaving an unscored stretch is not a recovery; the end of an announced
// degradation is, even with an unscored stretch in between.
func TestTelemetryRecoveryFollowsOnlyAnAnnouncedDegradation(t *testing.T) {
	state := &telemetryDiagnosticState{}
	now := time.Now()
	unscored := &TelemetryDiagnostic{Severity: "healthy", Unscored: []string{"stale_sample:uncalibrated"}}
	degraded := &TelemetryDiagnostic{Severity: "warning", Conditions: []string{"transport_lag"}}
	var previous *TelemetryRunHealth
	var events []string
	for i, diagnostic := range []*TelemetryDiagnostic{unscored, nil, degraded, unscored, nil, nil, unscored, nil} {
		current := updateTelemetryRunHealth(state, diagnostic, "", now.Add(time.Duration(i)*time.Second))
		if event := telemetryHealthEvent(previous, current); event != "" {
			events = append(events, event)
		}
		state.Health, previous = current, current
	}
	if strings.Join(events, ",") != "telemetry_degraded,telemetry_recovered" {
		t.Fatalf("events %v", events)
	}
}

// A sub-second step is still a step: below the smallest cross-host budget.
func TestClockStepToleranceBelowTheTransportBudget(t *testing.T) {
	now := time.Now()
	if !wallClockSteppedSince(now.Round(0).Add(-600*time.Millisecond), now.Sub(clockProcessStart), now) {
		t.Fatal("a 600 ms wall-clock step went unnoticed")
	}
	if wallClockSteppedSince(now.Round(0).Add(-20*time.Millisecond), now.Sub(clockProcessStart), now) {
		t.Fatal("slew-sized drift reported as a step")
	}
}

// An archive whose telemetry was unscored is unknown to RCA, never healthy.
func TestRootCauseTelemetryUnscoredIsUnknown(t *testing.T) {
	if telemetrySeverity("unscored") != "unknown" || telemetryScore("unscored") != 0 || telemetrySeverity("healthy") != "healthy" {
		t.Fatalf("unscored telemetry: %s %v", telemetrySeverity("unscored"), telemetryScore("unscored"))
	}
}

// A controller clock step between aggregation and publication (a publication
// that waited on the previous one) voids the aggregate's qualification too.
func TestClockPipelineStepBeforePublicationIsNotABreach(t *testing.T) {
	runner, backend, run := clockPipelineRun(t, func(c *clockCalibrations, now time.Time) {
		c.observe("gen-a", 0, time.Millisecond, now)
	})
	batch := generatorTelemetry(time.Now(), 0, 100, 500)
	qualifyRemoteMetricsReceipt(runner, run, "gen-a", &batch, time.Now())
	live, _, _ := backend.applyRemoteMetricsBatch(runner, run, "gen-a", batch)
	if live == nil || live.Telemetry.qualifiedAt.IsZero() {
		t.Fatalf("aggregate carries no qualification instant: %+v", live)
	}
	// The wall clock stepped 3 s forward after aggregation.
	const step = 3 * time.Second
	live.Telemetry.CollectedAt = live.Telemetry.CollectedAt.Add(-step)
	live.Telemetry.collectedAtBoundUnixMs -= float64(step / time.Millisecond)
	live.Telemetry.qualifiedAt = live.Telemetry.qualifiedAt.Add(-step)
	live.Telemetry.ControllerReceivedAt = live.Telemetry.ControllerReceivedAt.Add(-step)
	if !runner.publishLiveMetrics(run, live, nil) {
		t.Fatal("publication rejected")
	}
	health := run.TelemetryHealth
	if health == nil || slices.Contains(health.Conditions, "stale_sample") || !slices.Contains(health.Unscored, "stale_sample:"+clockReasonStep) {
		t.Fatalf("a step before publication scored the aggregate: health %+v telemetry %+v", health, live.Telemetry)
	}
	// The controller's own fan-out lag is measured on the monotonic clock
	// from aggregation on, so the step does not inflate it either.
	if live.Telemetry.ControllerFanoutLagMs > 1000 || slices.Contains(health.Conditions, "controller_fanout_lag") {
		t.Fatalf("a step inflated the fan-out lag: %v ms, health %+v", live.Telemetry.ControllerFanoutLagMs, health)
	}
}
