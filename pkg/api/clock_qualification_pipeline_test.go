package api

import (
	"slices"
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
