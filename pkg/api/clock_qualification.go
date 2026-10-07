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
// expires after three missed probes (measured on the controller's monotonic
// clock); an offset that jumps by more than twice its uncertainty, or a step
// in the controller's wall clock — between probes or since the last one —
// invalidates it until a consistent probe confirms the new mapping.

const (
	envClockProbeInterval = "APEXLOAD_CLOCK_PROBE_INTERVAL"
	// minClockProbeInterval is the specification's maximum live cadence
	// (at most one exchange per 30 seconds per source), and the default.
	minClockProbeInterval = 30 * time.Second
	// clockCalibrationMissedProbes expires a calibration that many probe
	// intervals after its last observation.
	clockCalibrationMissedProbes = 3
	// clockStepTolerance is the largest disagreement between the
	// controller's wall and monotonic clocks that is not a wall-clock step.
	// It stays well below the smallest cross-host budget (500 ms transport
	// lag), so a step that could cross a budget is caught; slewing moves the
	// wall clock at most 500 ppm, 45 ms over three probe intervals.
	clockStepTolerance = 100 * time.Millisecond
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
// (a Go duration) when valid, never more often than once per 30 seconds.
func clockProbeInterval() time.Duration {
	if raw := os.Getenv(envClockProbeInterval); raw != "" {
		if interval, err := time.ParseDuration(raw); err == nil && interval > 0 {
			return max(interval, minClockProbeInterval)
		}
	}
	return minClockProbeInterval
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
		interval = minClockProbeInterval
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
	return c.qualityAt(source, at.Round(0), at.Sub(clockProcessStart))
}

// qualityAt judges source's calibration at the controller's wall time and
// monotonic reading. A wall clock that moved differently from the monotonic
// clock since the probe has stepped, so every wall-clock reading taken now
// (receipt times, sample ages) disagrees with the offset: clock_step until
// the next probes re-anchor it. Expiry runs on the monotonic clock.
func (c *clockCalibrations) qualityAt(source string, wallNow time.Time, monotonicNow time.Duration) (float64, generatorrpc.ClockQuality) {
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
	}
	elapsed := monotonicNow - calibration.monotonic
	switch {
	case (wallNow.Round(0).Sub(calibration.observedAt) - elapsed).Abs() > clockStepTolerance:
		return 0, generatorrpc.ClockQuality{Reason: clockReasonStep}
	case elapsed > clockCalibrationMissedProbes*c.interval:
		return 0, generatorrpc.ClockQuality{Reason: clockReasonExpired}
	}
	return calibration.offsetMs, generatorrpc.ClockQuality{Qualified: true, UncertaintyMs: calibration.uncertaintyMs}
}

// recordClockCalibration adds one generator probe to a run this controller
// holds; interval is the cadence the probes actually run at.
func (tr *TestRunner) recordClockCalibration(test *ManagedTest, source string, offset, uncertainty, interval time.Duration, now time.Time) {
	tr.mu.Lock()
	if tr.tests[test.ID] != test {
		tr.mu.Unlock()
		return
	}
	if test.clockCalibrations == nil {
		test.clockCalibrations = newClockCalibrations(interval)
	}
	calibrations := test.clockCalibrations
	tr.mu.Unlock()
	calibrations.observe(source, offset, uncertainty, now)
}

// qualifyCrossHostTelemetry corrects a received publication's cross-host
// stages by its generator's offset (the generator's clock minus the
// controller's): transport lag gains the offset, and its collection time maps
// to the controller's clock by subtracting it. Without a current calibration
// the stages stay as measured, unqualified, and a negative one is unavailable
// (zero). A corrected transport lag below zero by more than its uncertainty
// is implausible and unavailable.
func qualifyCrossHostTelemetry(telemetry *generatorrpc.MetricsTelemetry, offsetMs float64, quality generatorrpc.ClockQuality) {
	if telemetry == nil {
		return
	}
	if telemetry.PublishedAtUnixMs > 0 && telemetry.ControllerReceivedAtUnixMs > 0 {
		transport := quality
		if !quality.Qualified && telemetry.TransportLagMs < 0 {
			// An unqualified negative delta is unavailable, never a figure.
			telemetry.TransportLagMs = 0
		}
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
// nextValueMs is next's corrected figure; a qualified source raises the
// combined lower bound to that figure minus its uncertainty.
func combineClockQuality(current, next *generatorrpc.ClockQuality, nextValueMs float64) *generatorrpc.ClockQuality {
	if next == nil {
		return current
	}
	var combined generatorrpc.ClockQuality
	switch {
	case current == nil:
		combined = *next
	case !current.Qualified:
		combined = *current
	case !next.Qualified:
		combined = *next
	default:
		combined = *current
		combined.UncertaintyMs = max(current.UncertaintyMs, next.UncertaintyMs)
	}
	combined.LowerBoundMs = nil
	var bound *float64
	if current != nil && current.LowerBoundMs != nil {
		bound = current.LowerBoundMs
	}
	nextBound := next.LowerBoundMs
	if next.Qualified {
		value := max(nextValueMs-next.UncertaintyMs, 0)
		nextBound = &value
	}
	if nextBound != nil && (bound == nil || *nextBound > *bound) {
		bound = nextBound
	}
	if bound != nil {
		value := *bound
		combined.LowerBoundMs = &value
	}
	return &combined
}

// sampleAgeQualified corrects a distributed run's end-to-end sample age by
// the offset of the generator whose sample was oldest. Without it the age
// stays as measured, unqualified, and a negative one is unavailable (zero);
// an age below zero by more than its uncertainty is implausible and
// unavailable. boundAgeMs, when positive, is the age the qualified
// generators prove, kept as the quality's lower bound.
func sampleAgeQualified(rawMs, offsetMs float64, quality *generatorrpc.ClockQuality, boundAgeMs float64) (float64, *generatorrpc.ClockQuality) {
	if quality == nil {
		return rawMs, nil
	}
	copied := *quality
	copied.LowerBoundMs = nil
	if boundAgeMs > 0 {
		bound := boundAgeMs
		copied.LowerBoundMs = &bound
	}
	// offsetMs is the oldest sample's own generator's offset, zero unless
	// that generator is qualified; the fleet quality may still be worse.
	corrected := rawMs + offsetMs
	if !quality.Qualified {
		return max(corrected, 0), &copied
	}
	if corrected < -quality.UncertaintyMs {
		return 0, &generatorrpc.ClockQuality{Reason: clockReasonImplausible}
	}
	return max(corrected, 0), &copied
}

// clockBudgetUnscored says why a budget decision on a cross-host duration
// cannot be scored: the duration is not qualified, or its uncertainty
// interval straddles the threshold — unless a qualified source's lower
// bound already exceeds the threshold, a definite breach.
func clockBudgetUnscored(value float64, threshold *float64, quality *generatorrpc.ClockQuality) string {
	if quality == nil || threshold == nil || *threshold <= 0 {
		return ""
	}
	if quality.LowerBoundMs != nil && *quality.LowerBoundMs > *threshold {
		// A qualified source proves the breach, whatever the others say.
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

// wallClockSteppedSince says whether the controller's wall clock stepped
// between a reading (wall time and monotonic reading since
// clockProcessStart) and now: their wall and monotonic distances disagree by
// more than clockStepTolerance. No reading shows no step.
func wallClockSteppedSince(wall time.Time, monotonic time.Duration, now time.Time) bool {
	if wall.IsZero() {
		return false
	}
	return (now.Round(0).Sub(wall) - (now.Sub(clockProcessStart) - monotonic)).Abs() > clockStepTolerance
}

// clockScoredValue is the figure a budget decision reports: the proven lower
// bound when the figure itself is not qualified.
func clockScoredValue(value float64, quality *generatorrpc.ClockQuality) float64 {
	if quality != nil && !quality.Qualified && quality.LowerBoundMs != nil {
		return *quality.LowerBoundMs
	}
	return value
}
