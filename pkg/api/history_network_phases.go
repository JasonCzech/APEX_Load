package api

import (
	"encoding/json"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

// applyIntervalNetworkPhases restores the fleet network-phase fields of a
// stored interval onto its live point. A nil record (a row written before
// phases were stored, or a sample without phases) leaves them absent. The
// server and no-response split is only meaningful with the phase observation
// counts it was derived from, so a record whose observations are missing or
// unreadable restores the averages alone and keeps the split unknown.
func applyIntervalNetworkPhases(live *LiveMetrics, record *storage.IntervalNetworkPhasesRecord) {
	if live == nil || record == nil {
		return
	}
	live.AvgDNSMs = record.AvgDNSMs
	live.AvgConnectMs = record.AvgConnectMs
	live.AvgTLSMs = record.AvgTLSMs
	live.AvgTTFBMs = record.AvgTTFBMs
	live.AvgWaitMs = record.AvgWaitMs
	if len(record.PhaseObserved) == 0 {
		return
	}
	var observed engine.PhaseObservations
	if err := json.Unmarshal(record.PhaseObserved, &observed); err != nil {
		return
	}
	live.PhaseObserved = &observed
	live.AvgServerMs = cloneFloat64Ptr(record.AvgServerMs)
	live.AvgNoResponseMs = cloneFloat64Ptr(record.AvgNoResponseMs)
}
