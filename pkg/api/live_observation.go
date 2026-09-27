package api

import (
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
)

// A presentation freshness bound, not a recovery deadline or proof of traffic.
// Normal collectors emit every second. Keep the absolute deadline in the public
// sample so browsers and HA readers can expire it even if updates stop entirely.
const liveObservationFreshness = 5 * time.Second

func observationDeadline(at, now time.Time) time.Time {
	if at.IsZero() || at.After(now) {
		return time.Unix(0, 0).UTC()
	}
	return at.Add(liveObservationFreshness)
}

// annotateLiveObservation only accepts detached projections. It never changes
// measured values, sample time, or the canonical history used for SLA/archive work.
func annotateLiveObservation(metrics *LiveMetrics, status string, now time.Time) {
	if metrics == nil {
		return
	}
	if isTerminalTestStatus(status) {
		metrics.ObservationState = "historical"
		metrics.ObservationValidUntil = nil
		return
	}
	deadline := observationDeadline(metrics.Timestamp, now)
	if metrics.ObservationValidUntil != nil && metrics.ObservationValidUntil.Before(deadline) {
		deadline = *metrics.ObservationValidUntil
	}
	metrics.ObservationValidUntil = &deadline
	metrics.ObservationState = ""
	if !now.Before(deadline) {
		metrics.ObservationState = "stale"
	}
}

// Called under tr.mu with the exact batch snapshot used to build the aggregate.
// The newest generator timestamp must not make a silent/missing shard look fresh.
func distributedObservationValidUntilLocked(test *ManagedTest, batches map[string]generatorrpc.MetricsBatch, now time.Time) time.Time {
	unknown := time.Unix(0, 0).UTC()
	if len(batches) == 0 {
		return unknown
	}
	for target := range test.remoteClientsByTarget {
		if _, ok := batches[target]; !ok && !remoteTargetStateIntentionallyRemoved(test.remoteTargetStates[target]) {
			return unknown
		}
	}
	deadline := now.Add(liveObservationFreshness)
	for _, batch := range batches {
		if batch.TimestampUnixMs <= 0 {
			return unknown
		}
		candidate := observationDeadline(time.UnixMilli(batch.TimestampUnixMs), now)
		if candidate.Before(deadline) {
			deadline = candidate
		}
	}
	return deadline
}
