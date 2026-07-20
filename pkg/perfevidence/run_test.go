package perfevidence

import (
	"context"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
)

func TestDefaultConfigProfiles(t *testing.T) {
	smoke, err := DefaultConfig("smoke")
	if err != nil {
		t.Fatal(err)
	}
	if err := smoke.Validate(); err != nil {
		t.Fatalf("smoke config: %v", err)
	}
	if smoke.HTTPVUsers > 250 || smoke.HTTPDensityTimeout > 5*time.Second || smoke.MetricsInterval > 100*time.Millisecond || smoke.MetricsLatencySamples > 3 {
		t.Fatalf("smoke profile is not comfortably bounded: %#v", smoke)
	}

	release, err := DefaultConfig("release")
	if err != nil {
		t.Fatal(err)
	}
	if err := release.Validate(); err != nil {
		t.Fatalf("release config: %v", err)
	}
	if release.HTTPVUsers != 10_000 {
		t.Fatalf("release HTTP VUser count = %d, want 10000", release.HTTPVUsers)
	}
	if _, err := DefaultConfig("unknown"); err == nil {
		t.Fatal("unknown profile succeeded")
	}
}

func TestWaitForHTTPDensityReturnsEarlySubTargetOutcome(t *testing.T) {
	var peak atomic.Int64
	peak.Store(7)
	runDone := make(chan engineOutcome, 1)
	runDone <- engineOutcome{result: &engine.RunResult{}}
	started := time.Now()
	reached, outcome, err := waitForHTTPDensity(context.Background(), 100, time.Second, &peak, runDone)
	if err != nil {
		t.Fatal(err)
	}
	if reached || outcome == nil || outcome.result == nil {
		t.Fatalf("reached/outcome = %v/%#v, want sub-target completed outcome", reached, outcome)
	}
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("early outcome took %s, want prompt return", elapsed)
	}
}

func TestSuccessfulTransactionCountUsesCollectorSuccessTotal(t *testing.T) {
	tests := []struct {
		name   string
		result *engine.RunResult
		want   int64
	}{
		{name: "nil result", result: nil, want: 0},
		{name: "all successful", result: &engine.RunResult{TotalTransactions: 10}, want: 10},
		{name: "mixed outcome", result: &engine.RunResult{TotalTransactions: 10, TotalErrors: 3}, want: 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := successfulTransactionCount(tt.result); got != tt.want {
				t.Fatalf("successfulTransactionCount() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestWaitForHTTPDensityTargetReached(t *testing.T) {
	var peak atomic.Int64
	peak.Store(100)
	reached, outcome, err := waitForHTTPDensity(
		context.Background(),
		100,
		time.Second,
		&peak,
		make(chan engineOutcome),
	)
	if err != nil || !reached || outcome != nil {
		t.Fatalf("reached/outcome/error = %v/%#v/%v", reached, outcome, err)
	}
}

func TestWaitForHTTPDensityReturnsSubTargetOnTimeout(t *testing.T) {
	var peak atomic.Int64
	peak.Store(75)
	reached, outcome, err := waitForHTTPDensity(
		context.Background(),
		100,
		5*time.Millisecond,
		&peak,
		make(chan engineOutcome),
	)
	if err != nil || reached || outcome != nil {
		t.Fatalf("reached/outcome/error = %v/%#v/%v", reached, outcome, err)
	}
}

func TestWaitForHTTPDensityReturnsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var peak atomic.Int64
	reached, outcome, err := waitForHTTPDensity(ctx, 100, time.Second, &peak, make(chan engineOutcome))
	if err != context.Canceled || reached || outcome != nil {
		t.Fatalf("reached/outcome/error = %v/%#v/%v, want canceled", reached, outcome, err)
	}
}

func TestBuildMeasurementsReportsSubTargetHTTPPeakWithoutClaim(t *testing.T) {
	measurements, err := buildMeasurements(
		harnessProbe{NanosecondsPerTransaction: []float64{100}, Dropped: 0},
		httpDensityProbe{ConfiguredVUsers: 10_000, PeakConcurrentRequests: 7_500},
		rampProbe{VUsersPerSecond: []float64{600}},
		metricsLatencyProbe{Milliseconds: []float64{1_000}},
	)
	if err != nil {
		t.Fatal(err)
	}
	density := measurements[1].TargetComparison
	if density.NumericRelation != "below_target" || density.AttainmentStatus != AttainmentNotEvaluated {
		t.Fatalf("density comparison = %#v", density)
	}
}

func TestConfigRejectsMetricsTimeoutAtOrBelowInterval(t *testing.T) {
	config, err := DefaultConfig("smoke")
	if err != nil {
		t.Fatal(err)
	}
	config.MetricsProbeTimeout = config.MetricsInterval
	if err := config.Validate(); err == nil {
		t.Fatal("config with timeout equal to interval succeeded")
	}
}

func TestPerformanceEvidenceSmoke(t *testing.T) {
	if os.Getenv("APEXLOAD_RUN_PERFORMANCE_SMOKE") != "1" {
		t.Skip("set APEXLOAD_RUN_PERFORMANCE_SMOKE=1 to execute controlled performance probes")
	}
	config, err := DefaultConfig("smoke")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	report, err := Run(ctx, config)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := ValidateReport(report); err != nil {
		t.Fatalf("ValidateReport: %v", err)
	}
	if len(report.Measurements) != 4 {
		t.Fatalf("measurements = %d, want 4", len(report.Measurements))
	}
	for _, measurement := range report.Measurements {
		if measurement.TargetComparison.AttainmentStatus != AttainmentNotEvaluated {
			t.Fatalf("measurement %s attainment = %q", measurement.ID, measurement.TargetComparison.AttainmentStatus)
		}
	}
}
