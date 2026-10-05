package analysis

import (
	"errors"
	"math"
	"time"
)

// Clock sources say whose clock a point's timestamp was read from.
const (
	ClockSourceGenerator  = "generator"
	ClockSourceCollector  = "collector"
	ClockSourceProvider   = "provider"
	ClockSourceController = "controller"
)

// Offset statuses. A measured offset comes from a request and response
// exchange with the controller; an unknown one has no such exchange.
const (
	OffsetMeasured = "measured"
	OffsetUnknown  = "unknown"
)

// maxClockOffset bounds a believable offset; anything larger is a bad clock,
// not a measurement.
const maxClockOffset = 24 * time.Hour

// PointTiming records when and against which clock a point was taken, so
// analysis can order events only when their gap exceeds both uncertainties.
// Offsets are the source's clock minus the controller's; a positive offset
// means the source runs ahead.
type PointTiming struct {
	ReceivedAt         *time.Time `json:"received_at,omitempty"`
	ResolutionSeconds  int        `json:"resolution_seconds,omitempty"`
	FreshnessSeconds   *float64   `json:"freshness_seconds,omitempty"`
	ClockSource        string     `json:"clock_source"`
	ClockOffsetMs      *float64   `json:"clock_offset_ms,omitempty"`
	ClockUncertaintyMs *float64   `json:"clock_uncertainty_ms,omitempty"`
	OffsetStatus       string     `json:"offset_status"`
}

// Validate bounds the timing a controller attaches to a point.
func (t PointTiming) Validate() error {
	switch t.ClockSource {
	case ClockSourceGenerator, ClockSourceCollector, ClockSourceProvider, ClockSourceController:
	default:
		return errors.New("invalid timing clock source")
	}
	if t.ResolutionSeconds < 0 || t.ResolutionSeconds > 86400 {
		return errors.New("invalid timing resolution")
	}
	if t.FreshnessSeconds != nil && (!finiteTiming(*t.FreshnessSeconds) || *t.FreshnessSeconds < 0) {
		return errors.New("invalid timing freshness")
	}
	if t.ReceivedAt != nil && (t.ReceivedAt.IsZero() || t.ReceivedAt.Location() != time.UTC || t.ReceivedAt.Year() < 1 || t.ReceivedAt.Year() > 9999) {
		return errors.New("timing received_at must be a nonzero UTC time")
	}
	bound := float64(maxClockOffset / time.Millisecond)
	if t.ClockUncertaintyMs != nil && (!finiteTiming(*t.ClockUncertaintyMs) || *t.ClockUncertaintyMs < 0 || *t.ClockUncertaintyMs > bound) {
		return errors.New("invalid timing clock uncertainty")
	}
	switch t.OffsetStatus {
	case OffsetMeasured:
		if t.ClockOffsetMs == nil || t.ClockUncertaintyMs == nil || !finiteTiming(*t.ClockOffsetMs) || math.Abs(*t.ClockOffsetMs) > bound {
			return errors.New("a measured offset requires a bounded offset and uncertainty")
		}
	case OffsetUnknown:
		if t.ClockOffsetMs != nil {
			return errors.New("an unknown offset cannot carry an offset value")
		}
	default:
		return errors.New("invalid timing offset status")
	}
	return nil
}

func finiteTiming(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

// ProviderTiming describes a provider's bucket: the provider's clock is never
// exchanged with the controller, so its offset is unknown and its uncertainty
// is at least half the bucket resolution. It holds no receive time, so
// re-reading the same bucket yields the same point.
func ProviderTiming(resolutionSeconds int, freshness *time.Duration) *PointTiming {
	timing := &PointTiming{ClockSource: ClockSourceProvider, OffsetStatus: OffsetUnknown}
	if resolutionSeconds > 0 && resolutionSeconds <= 86400 {
		timing.ResolutionSeconds = resolutionSeconds
		uncertainty := float64(resolutionSeconds) * 1000 / 2
		timing.ClockUncertaintyMs = &uncertainty
	}
	if freshness != nil && *freshness >= 0 {
		seconds := freshness.Seconds()
		timing.FreshnessSeconds = &seconds
	}
	return timing
}

// ClockEstimate is a source clock's offset from the controller, the exchange's
// round trip, and the uncertainty that bounds the offset error: half the round
// trip, however unevenly the delay split between the two directions.
type ClockEstimate struct {
	Offset      time.Duration
	RoundTrip   time.Duration
	Uncertainty time.Duration
}

// EstimateClockOffset uses the four timestamps of one request and response:
// the source sent at sourceSent and received the reply at sourceReceived on
// its own clock; the controller received at controllerReceived and replied at
// controllerSent on its clock.
func EstimateClockOffset(sourceSent, controllerReceived, controllerSent, sourceReceived time.Time) (ClockEstimate, bool) {
	if sourceSent.IsZero() || controllerReceived.IsZero() || controllerSent.IsZero() || sourceReceived.IsZero() || controllerSent.Before(controllerReceived) {
		return ClockEstimate{}, false
	}
	roundTrip := sourceReceived.Sub(sourceSent) - controllerSent.Sub(controllerReceived)
	if roundTrip < 0 {
		return ClockEstimate{}, false
	}
	offset := (sourceSent.Sub(controllerReceived) + sourceReceived.Sub(controllerSent)) / 2
	if offset < -maxClockOffset || offset > maxClockOffset {
		return ClockEstimate{}, false
	}
	return ClockEstimate{Offset: offset, RoundTrip: roundTrip, Uncertainty: roundTrip / 2}, true
}

// MeasuredTiming describes a point read from a source whose offset was
// measured against the controller.
func MeasuredTiming(source string, estimate ClockEstimate) *PointTiming {
	offset := float64(estimate.Offset) / float64(time.Millisecond)
	uncertainty := float64(estimate.Uncertainty) / float64(time.Millisecond)
	return &PointTiming{ClockSource: source, ClockOffsetMs: &offset, ClockUncertaintyMs: &uncertainty, OffsetStatus: OffsetMeasured}
}
