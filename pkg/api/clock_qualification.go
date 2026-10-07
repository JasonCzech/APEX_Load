package api

import (
	"math"
	"os"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
)

// Clock qualification (TEL-1 §3.1): a generator's measured clock offset
// corrects the stages measured across its clock and the controller's
// (transport lag, end-to-end sample age), and qualifies them. A calibration
// expires after three missed probes; an offset that jumps by more than twice
// its uncertainty, or a step in the controller's wall clock, invalidates it
// until a consistent probe confirms the new mapping.

const (
	envClockProbeInterval = "APEXLOAD_CLOCK_PROBE_INTERVAL"
	// maxClockProbeInterval is the specification's maximum live cadence,
	// and the default.
	maxClockProbeInterval = 30 * time.Second
	// clockCalibrationMissedProbes expires a calibration that many probe
	// intervals after its last observation.
	clockCalibrationMissedProbes = 3
	// clockStepTolerance is the largest disagreement between the
	// controller's wall and monotonic clocks across two probes that is not
	// a wall-clock step.
	clockStepTolerance = time.Second
)

// Reasons a cross-host duration is not qualified.
const (
	clockReasonUncalibrated = "uncalibrated"
	clockReasonExpired      = "calibration_expired"
	clockReasonJump         = "clock_jump"
	clockReasonStep         = "clock_step"
	clockReasonImplausible  = "implausible"
)

// clockProbeInterval is the live clock-probe cadence: APEXLOAD_CLOCK_PROBE_INTERVAL
// (a Go duration) when valid, never longer than 30 seconds.
func clockProbeInterval() time.Duration {
	if raw := os.Getenv(envClockProbeInterval); raw != "" {
		if interval, err := time.ParseDuration(raw); err == nil && interval > 0 {
			return min(interval, maxClockProbeInterval)
		}
	}
	return maxClockProbeInterval
}

// clockProcessStart anchors the controller's monotonic clock readings.
var clockProcessStart = time.Now()

type clockCalibration struct {
	offsetMs      float64
	uncertaintyMs float64
	// observedAt is the controller's wall time of the probe and monotonic
	// its monotonic reading, so a step in its wall clock is detectable.
	observedAt time.Time
	monotonic  time.Duration
	// reason is set while the mapping is not trusted: a jump or step that
	// a later consistent probe has not yet confirmed.
	reason string
}

// clockCalibrations is one run's current offset per generator.
type clockCalibrations struct {
	mu       sync.Mutex
	interval time.Duration
	sources  map[string]*clockCalibration
}

func newClockCalibrations(interval time.Duration) *clockCalibrations {
	if interval <= 0 {
		interval = maxClockProbeInterval
	}
	return &clockCalibrations{interval: interval, sources: map[string]*clockCalibration{}}
}

// observe records one probe of source's offset at the controller's time now.
func (c *clockCalibrations) observe(source string, offset, uncertainty time.Duration, now time.Time) {
	c.observeAt(source, offset, uncertainty, now, now.Sub(clockProcessStart))
}

// observeAt records a probe at the controller's wall time and monotonic
// reading.
func (c *clockCalibrations) observeAt(source string, offset, uncertainty time.Duration, wallNow time.Time, monotonicNow time.Duration) {
	if c == nil || source == "" || uncertainty < 0 {
		return
	}
	ms := func(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
	next := &clockCalibration{offsetMs: ms(offset), uncertaintyMs: ms(uncertainty), observedAt: wallNow.Round(0), monotonic: monotonicNow}
	c.mu.Lock()
	defer c.mu.Unlock()
	if previous := c.sources[source]; previous != nil {
		wall := next.observedAt.Sub(previous.observedAt)
		monotonic := next.monotonic - previous.monotonic
		switch {
		case (wall - monotonic).Abs() > clockStepTolerance:
			next.reason = clockReasonStep
		case math.Abs(next.offsetMs-previous.offsetMs) > 2*max(previous.uncertaintyMs, next.uncertaintyMs):
			next.reason = clockReasonJump
		}
	}
	c.sources[source] = next
}

// quality is source's offset and the quality it gives a cross-host duration
// measured at the controller's time at.
func (c *clockCalibrations) quality(source string, at time.Time) (float64, generatorrpc.ClockQuality) {
	if c == nil {
		return 0, generatorrpc.ClockQuality{Reason: clockReasonUncalibrated}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	calibration := c.sources[source]
	switch {
	case calibration == nil:
		return 0, generatorrpc.ClockQuality{Reason: clockReasonUncalibrated}
	case calibration.reason != "":
		return 0, generatorrpc.ClockQuality{Reason: calibration.reason}
	case at.Sub(calibration.observedAt) > clockCalibrationMissedProbes*c.interval:
		return 0, generatorrpc.ClockQuality{Reason: clockReasonExpired}
	}
	return calibration.offsetMs, generatorrpc.ClockQuality{Qualified: true, UncertaintyMs: calibration.uncertaintyMs}
}

// recordClockCalibration adds one generator probe to a run this controller
// holds.
func (tr *TestRunner) recordClockCalibration(test *ManagedTest, source string, offset, uncertainty time.Duration, now time.Time) {
	tr.mu.Lock()
	if tr.tests[test.ID] != test {
		tr.mu.Unlock()
		return
	}
	if test.clockCalibrations == nil {
		test.clockCalibrations = newClockCalibrations(clockProbeInterval())
	}
	calibrations := test.clockCalibrations
	tr.mu.Unlock()
	calibrations.observe(source, offset, uncertainty, now)
}

// qualifyCrossHostTelemetry corrects a received publication's cross-host
// stages by its generator's offset (the generator's clock minus the
// controller's): transport lag gains the offset, and its collection time maps
// to the controller's clock by subtracting it. Without a current calibration
// the stages stay as measured, unqualified. A corrected transport lag below
// zero by more than its uncertainty is implausible and unavailable.
func qualifyCrossHostTelemetry(telemetry *generatorrpc.MetricsTelemetry, offsetMs float64, quality generatorrpc.ClockQuality) {
	if telemetry == nil {
		return
	}
	if telemetry.PublishedAtUnixMs > 0 && telemetry.ControllerReceivedAtUnixMs > 0 {
		transport := quality
		if quality.Qualified {
			corrected := telemetry.TransportLagMs + offsetMs
			switch {
			case corrected < -quality.UncertaintyMs:
				telemetry.TransportLagMs = 0
				transport = generatorrpc.ClockQuality{Reason: clockReasonImplausible}
			default:
				telemetry.TransportLagMs = max(corrected, 0)
			}
		}
		telemetry.TransportLagQuality = &transport
	}
	if telemetry.CollectedAtUnixMs > 0 {
		collected := quality
		telemetry.CollectedAtQuality = &collected
		if quality.Qualified {
			telemetry.CollectedAtOffsetMs = offsetMs
		}
	}
}

// combineClockQuality is the quality of a figure drawn from several
// sources: qualified only when every one is, with the largest uncertainty.
func combineClockQuality(current, next *generatorrpc.ClockQuality) *generatorrpc.ClockQuality {
	if next == nil {
		return current
	}
	if current == nil {
		copied := *next
		return &copied
	}
	switch {
	case !current.Qualified:
		return current
	case !next.Qualified:
		copied := *next
		return &copied
	}
	combined := *current
	combined.UncertaintyMs = max(current.UncertaintyMs, next.UncertaintyMs)
	return &combined
}

// sampleAgeQualified corrects a distributed run's end-to-end sample age by
// the offset of the generator whose sample was oldest. Without it the age
// stays as measured, unqualified; an age below zero by more than its
// uncertainty is implausible and unavailable.
func sampleAgeQualified(rawMs, offsetMs float64, quality *generatorrpc.ClockQuality) (float64, *generatorrpc.ClockQuality) {
	if quality == nil {
		return rawMs, nil
	}
	copied := *quality
	if !quality.Qualified {
		return rawMs, &copied
	}
	corrected := rawMs + offsetMs
	if corrected < -quality.UncertaintyMs {
		return 0, &generatorrpc.ClockQuality{Reason: clockReasonImplausible}
	}
	return max(corrected, 0), &copied
}

// clockBudgetUnscored says why a budget decision on a cross-host duration
// cannot be scored: the duration is not qualified, or its uncertainty
// interval straddles the threshold.
func clockBudgetUnscored(value float64, threshold *float64, quality *generatorrpc.ClockQuality) string {
	if quality == nil || threshold == nil || *threshold <= 0 {
		return ""
	}
	if !quality.Qualified {
		if quality.Reason == "" {
			return clockReasonUncalibrated
		}
		return quality.Reason
	}
	if value-quality.UncertaintyMs <= *threshold && value+quality.UncertaintyMs > *threshold {
		return "uncertainty_crosses_threshold"
	}
	return ""
}
