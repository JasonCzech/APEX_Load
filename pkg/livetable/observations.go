package livetable

import (
	"errors"
	"time"
)

// Observations are bounded, value-free evidence of serving attempts. They are
// checkpointed separately from exact operation receipts; a crash can lose the
// uncheckpointed observations, which recovery explicitly marks as incomplete.
// They never claim target-side completion or exactly-once business processing.
type Observations struct {
	Version            int                `json:"version"`
	CapturedAt         time.Time          `json:"captured_at"`
	WaitCount          uint64             `json:"wait_count"`
	WaitTotalNS        uint64             `json:"wait_total_ns"`
	EmptyOutcomes      uint64             `json:"empty_outcomes"`
	Throttled          uint64             `json:"throttled"`
	UnknownOutcomes    uint64             `json:"unknown_outcomes"`
	UnknownPopAttempts uint64             `json:"unknown_pop_attempts"`
	RecoveryGaps       uint64             `json:"recovery_gaps"`
	Incomplete         bool               `json:"incomplete"`
	DroppedEvents      uint64             `json:"dropped_events"`
	Events             []ObservationEvent `json:"events,omitempty"`
}

type ObservationEvent struct {
	Kind  string    `json:"kind"`
	First time.Time `json:"first"`
	Last  time.Time `json:"last"`
	Count uint64    `json:"count"`
}

const maxObservationEvents = 16

func (t *Table) observationEventLocked(kind string, now time.Time) {
	events := t.observations.Events
	if n := len(events); n > 0 && events[n-1].Kind == kind {
		if now.After(events[n-1].Last) {
			events[n-1].Last = now
		}
		events[n-1].Count++
		return
	}
	if len(events) == maxObservationEvents {
		copy(events, events[1:])
		events = events[:len(events)-1]
		t.observations.DroppedEvents++
	}
	t.observations.Events = append(events, ObservationEvent{Kind: kind, First: now, Last: now, Count: 1})
}

func (t *Table) observeEvidence(op Operation, result Result, err error) {
	t.observationMu.Lock()
	defer t.observationMu.Unlock()
	now := time.Now().UTC()
	if result.Status == StatusEmpty {
		t.observations.EmptyOutcomes++
		t.observationEventLocked("empty", now)
	}
	if CodeOf(err) == CodeCapacity {
		t.observations.Throttled++
		t.observationEventLocked("capacity_rejection", now)
	}
	var typed *Error
	if errors.As(err, &typed) && typed.OutcomeUnknown {
		t.observations.UnknownOutcomes++
		kind := "unknown_outcome"
		if op == OpPop {
			t.observations.UnknownPopAttempts++
			kind = "unknown_pop_attempt"
		}
		t.observationEventLocked(kind, now)
	}
}

func (t *Table) observeWait(elapsed time.Duration) {
	t.observationMu.Lock()
	defer t.observationMu.Unlock()
	t.observations.WaitCount++
	t.observations.WaitTotalNS += uint64(max(elapsed, 0))
}

func (t *Table) observationSnapshot(at time.Time) Observations {
	t.observationMu.Lock()
	defer t.observationMu.Unlock()
	out := t.observations
	out.CapturedAt = at.UTC()
	out.Events = append([]ObservationEvent(nil), out.Events...)
	out.Incomplete = out.Incomplete || t.activeAttempts.Load() > 0
	return out
}

// RecordRecoveryGap is called only when a restored table returns to service,
// never for raw inspection of a terminal snapshot. The interval bounds missing
// observations since the last checkpoint; it is not an exact outage duration.
func (t *Table) RecordRecoveryGap() {
	t.observationMu.Lock()
	defer t.observationMu.Unlock()
	start, now := t.observations.CapturedAt, time.Now().UTC()
	if start.IsZero() || start.After(now) {
		start = now
	}
	t.observations.Version = 1
	t.observations.Incomplete = true
	t.observations.RecoveryGaps++
	t.observationEventLocked("recovery_observation_gap", now)
	event := &t.observations.Events[len(t.observations.Events)-1]
	if start.Before(event.First) {
		event.First = start
	}
}

func validateObservations(value Observations) error {
	if value.Version != 0 && value.Version != 1 || len(value.Events) > maxObservationEvents {
		return fail(CodeInvalid, "invalid observation checkpoint")
	}
	for _, event := range value.Events {
		switch event.Kind {
		case "empty", "capacity_rejection", "unknown_outcome", "unknown_pop_attempt", "recovery_observation_gap":
		default:
			return fail(CodeInvalid, "invalid observation event")
		}
		if event.Count == 0 || event.First.IsZero() || event.Last.Before(event.First) {
			return fail(CodeInvalid, "invalid observation interval")
		}
	}
	return nil
}
