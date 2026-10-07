package api

import (
	"slices"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
)

// A calibration qualifies cross-host durations until three probes are
// missed; a jump beyond twice the uncertainty, or a controller wall-clock
// step, invalidates it until a consistent probe confirms the new mapping.
func TestClockCalibrationExpiryJumpAndStep(t *testing.T) {
	start := time.Unix(1_800_000_000, 0).UTC()
	c := newClockCalibrations(30 * time.Second)
	if _, q := c.quality("gen-a", start); q.Qualified || q.Reason != clockReasonUncalibrated {
		t.Fatalf("uncalibrated: %+v", q)
	}
	c.observeAt("gen-a", 500*time.Millisecond, 2*time.Millisecond, start, 0)
	if offset, q := c.quality("gen-a", start.Add(time.Minute)); !q.Qualified || offset != 500 || q.UncertaintyMs != 2 {
		t.Fatalf("calibrated: %v %+v", offset, q)
	}
	if _, q := c.quality("gen-a", start.Add(91*time.Second)); q.Qualified || q.Reason != clockReasonExpired {
		t.Fatalf("three missed probes: %+v", q)
	}
	// A jump of 100 ms against a 2 ms uncertainty is not trusted until
	// confirmed.
	c.observeAt("gen-a", 600*time.Millisecond, 2*time.Millisecond, start.Add(30*time.Second), 30*time.Second)
	if _, q := c.quality("gen-a", start.Add(31*time.Second)); q.Qualified || q.Reason != clockReasonJump {
		t.Fatalf("jump: %+v", q)
	}
	c.observeAt("gen-a", 601*time.Millisecond, 2*time.Millisecond, start.Add(60*time.Second), 60*time.Second)
	if offset, q := c.quality("gen-a", start.Add(61*time.Second)); !q.Qualified || offset != 601 {
		t.Fatalf("confirmed after a jump: %v %+v", offset, q)
	}
	// The controller's wall clock stepped 10 s while 30 s passed.
	c.observeAt("gen-a", 601*time.Millisecond, 2*time.Millisecond, start.Add(100*time.Second), 90*time.Second)
	if _, q := c.quality("gen-a", start.Add(100*time.Second)); q.Qualified || q.Reason != clockReasonStep {
		t.Fatalf("wall-clock step: %+v", q)
	}
}

// Correction adds the generator's offset to its transport lag and maps its
// collection time; a corrected lag below zero beyond its uncertainty is
// implausible; without a calibration the stages stay as measured.
func TestCrossHostTelemetryCorrection(t *testing.T) {
	qualified := generatorrpc.ClockQuality{Qualified: true, UncertaintyMs: 5}
	telemetry := &generatorrpc.MetricsTelemetry{PublishedAtUnixMs: 1, ControllerReceivedAtUnixMs: 2, CollectedAtUnixMs: 1, TransportLagMs: 1000}
	qualifyCrossHostTelemetry(telemetry, -400, qualified)
	if telemetry.TransportLagMs != 600 || !telemetry.TransportLagQuality.Qualified || telemetry.CollectedAtOffsetMs != -400 || !telemetry.CollectedAtQuality.Qualified {
		t.Fatalf("corrected %+v", telemetry)
	}
	implausible := &generatorrpc.MetricsTelemetry{PublishedAtUnixMs: 1, ControllerReceivedAtUnixMs: 2, TransportLagMs: 100}
	qualifyCrossHostTelemetry(implausible, -500, qualified)
	if implausible.TransportLagMs != 0 || implausible.TransportLagQuality.Qualified || implausible.TransportLagQuality.Reason != clockReasonImplausible {
		t.Fatalf("implausible %+v", implausible)
	}
	uncalibrated := &generatorrpc.MetricsTelemetry{PublishedAtUnixMs: 1, ControllerReceivedAtUnixMs: 2, TransportLagMs: 900}
	qualifyCrossHostTelemetry(uncalibrated, 0, generatorrpc.ClockQuality{Reason: clockReasonUncalibrated})
	if uncalibrated.TransportLagMs != 900 || uncalibrated.TransportLagQuality.Qualified || uncalibrated.TransportLagQuality.Reason != clockReasonUncalibrated {
		t.Fatalf("uncalibrated %+v", uncalibrated)
	}
	if age, q := sampleAgeQualified(3000, -2900, &qualified); age != 100 || !q.Qualified {
		t.Fatalf("corrected age %v %+v", age, q)
	}
	if age, q := sampleAgeQualified(100, -500, &qualified); age != 0 || q.Reason != clockReasonImplausible {
		t.Fatalf("implausible age %v %+v", age, q)
	}
	combined := combineClockQuality(combineClockQuality(nil, &qualified), &generatorrpc.ClockQuality{Qualified: true, UncertaintyMs: 9})
	if !combined.Qualified || combined.UncertaintyMs != 9 {
		t.Fatalf("combined %+v", combined)
	}
	if combined = combineClockQuality(combined, &generatorrpc.ClockQuality{Reason: clockReasonExpired}); combined.Qualified || combined.Reason != clockReasonExpired {
		t.Fatalf("combined with an expired source %+v", combined)
	}
}

// Injected skew without a calibration, an expired calibration and an
// uncertainty straddling the threshold leave the budget decision unscored:
// never a breach. A qualified lag clearly over the budget still breaches.
func TestCrossHostBudgetDecisionsAreUnscoredNotBreached(t *testing.T) {
	limit := 1000.0
	thresholds := ThresholdSettings{MaxTelemetryTransportLagMs: &limit}
	for name, tc := range map[string]struct {
		lag      float64
		quality  *generatorrpc.ClockQuality
		unscored string
		breach   bool
	}{
		"skewed, uncalibrated": {5000, &generatorrpc.ClockQuality{Reason: clockReasonUncalibrated}, "transport_lag:uncalibrated", false},
		"expired":              {5000, &generatorrpc.ClockQuality{Reason: clockReasonExpired}, "transport_lag:calibration_expired", false},
		"straddles":            {1004, &generatorrpc.ClockQuality{Qualified: true, UncertaintyMs: 10}, "transport_lag:uncertainty_crosses_threshold", false},
		"qualified breach":     {1100, &generatorrpc.ClockQuality{Qualified: true, UncertaintyMs: 10}, "", true},
		"qualified pass":       {500, &generatorrpc.ClockQuality{Qualified: true, UncertaintyMs: 10}, "", false},
	} {
		t.Run(name, func(t *testing.T) {
			live := &LiveMetrics{Telemetry: &TelemetryHealth{TransportLagMs: tc.lag, TransportLagQuality: tc.quality}}
			diagnostic, _ := buildTelemetryDiagnostic(nil, live, thresholds)
			if breached := slices.Contains(diagnostic.Conditions, "transport_lag"); breached != tc.breach {
				t.Fatalf("breach = %v, want %v (%+v)", breached, tc.breach, diagnostic)
			}
			if tc.unscored != "" && !slices.Contains(diagnostic.Unscored, tc.unscored) || tc.unscored == "" && len(diagnostic.Unscored) != 0 {
				t.Fatalf("unscored = %v, want %q", diagnostic.Unscored, tc.unscored)
			}
		})
	}
}
