package api

import (
	"encoding/json"
	"io"

	"github.com/jasonczech/apexload/pkg/engine"
)

// archiveIntervalPhaseMerge combines the network-phase breakdown of the
// generator rows that make up one fleet interval, the way the live fleet merge
// (mergeMetricAggregates and applyRemoteMetricsBatch) does: averages weighted
// by attempts, observation counts summed (unknown when any row with attempts
// lacks them), and the server/no-response/download split recomputed from the
// merged figures. A row with attempts but no phases at all (a generator build
// that archived none, although it sent them live) makes the whole fleet
// interval's phases unknown: averaging it in as 0 ms would understate every
// phase by its share.
type archiveIntervalPhaseMerge struct {
	dnsSum, connectSum, tlsSum, ttfbSum, waitSum float64
	observed                                     engine.PhaseObservationMerge
	present, missing                             bool
}

func (m *archiveIntervalPhaseMerge) add(record IntervalSummaryRecord, attempts int64) {
	if attempts <= 0 {
		return
	}
	if !archiveIntervalHasPhases(record) {
		m.missing = true
		return
	}
	m.present = true
	count := float64(attempts)
	m.dnsSum += record.AvgDNSMs * count
	m.connectSum += record.AvgConnectMs * count
	m.tlsSum += record.AvgTLSMs * count
	m.ttfbSum += record.AvgTTFBMs * count
	m.waitSum += record.AvgWaitMs * count
	m.observed.Add(record.PhaseObserved, attempts)
}

// apply sets the merged phases on the fleet row. avgResponseMs must already
// be the fleet average over the same attempts. A fleet interval with a row
// that carried no phases keeps them absent (unknown).
func (m *archiveIntervalPhaseMerge) apply(aggregate *IntervalSummaryRecord, attempts int64) {
	if aggregate == nil || !m.present || m.missing || attempts <= 0 {
		return
	}
	divisor := float64(attempts)
	aggregate.AvgDNSMs = m.dnsSum / divisor
	aggregate.AvgConnectMs = m.connectSum / divisor
	aggregate.AvgTLSMs = m.tlsSum / divisor
	aggregate.AvgTTFBMs = m.ttfbSum / divisor
	aggregate.PhaseObserved = m.observed.Result()
	var download float64
	var ok bool
	aggregate.AvgServerMs, aggregate.AvgNoResponseMs, download, ok = phaseSplitFields(aggregate.PhaseObserved, attempts, aggregate.AvgResponseMs, aggregate.AvgDNSMs, aggregate.AvgConnectMs, aggregate.AvgTLSMs, aggregate.AvgTTFBMs)
	switch {
	case ok:
		// Download over the samples that got a first byte.
		aggregate.AvgWaitMs = download
	case m.waitSum > 0:
		// A generator that predates the observations: its own download tail.
		aggregate.AvgWaitMs = m.waitSum / divisor
	default:
		aggregate.AvgWaitMs = 0
	}
}

// archiveIntervalHasPhases reports whether an archived interval row carries
// any network-phase data.
func archiveIntervalHasPhases(record IntervalSummaryRecord) bool {
	return record.AvgDNSMs != 0 || record.AvgConnectMs != 0 || record.AvgTLSMs != 0 || record.AvgTTFBMs != 0 || record.AvgWaitMs != 0 ||
		record.PhaseObserved != nil || record.AvgServerMs != nil || record.AvgNoResponseMs != nil
}

// archiveRawPhaseSums accumulates the network phases of raw transaction
// events, for the distributed finalization pass that rebuilds a fleet
// interval exactly from them.
type archiveRawPhaseSums struct {
	dnsMs, connectMs, tlsMs, ttfbMs float64
	observed                        engine.PhaseObservations
}

func (s *archiveRawPhaseSums) observe(transaction TransactionEvent) {
	s.dnsMs += transaction.DNSMs
	s.connectMs += transaction.ConnectMs
	s.tlsMs += transaction.TLSMs
	s.ttfbMs += transaction.TTFBMs
	s.observed.ObserveMs(transaction.DNSMs, transaction.ConnectMs, transaction.TLSMs, transaction.TTFBMs, transaction.DurationMs)
}

func (s *archiveRawPhaseSums) add(other archiveRawPhaseSums) {
	s.dnsMs += other.dnsMs
	s.connectMs += other.connectMs
	s.tlsMs += other.tlsMs
	s.ttfbMs += other.ttfbMs
	s.observed.Add(other.observed)
}

// archiveFleetPhaseCoverage says, for a whole distributed archive, whether
// its generators reported network phases: phases when every merged fleet
// interval with attempts carries them, observed when every such interval also
// carries the observation counts. The exact pass rebuilds an interval's phases
// from raw events under this fleet-wide answer, because raw events are
// bucketed by start time while generator rows count completions, so a window's
// own provisional row can have no attempts (and no phases) while the exact
// pass gives it some.
type archiveFleetPhaseCoverage struct {
	phases, observed bool
}

func scanArchiveFleetPhaseCoverage(decoder *json.Decoder) (archiveFleetPhaseCoverage, error) {
	coverage := archiveFleetPhaseCoverage{phases: true, observed: true}
	var previousAttempts int64
	seen := false
	for {
		var record IntervalSummaryRecord
		if err := decoder.Decode(&record); err != nil {
			if err == io.EOF {
				break
			}
			return archiveFleetPhaseCoverage{}, err
		}
		cumulative := record.TotalTx + record.TotalErrors
		windowAttempts := cumulative - previousAttempts
		previousAttempts = cumulative
		if windowAttempts <= 0 {
			continue
		}
		if !archiveIntervalHasPhases(record) {
			return archiveFleetPhaseCoverage{}, nil
		}
		seen = true
		coverage.observed = coverage.observed && record.PhaseObserved != nil
	}
	if !seen {
		return archiveFleetPhaseCoverage{}, nil
	}
	return coverage, nil
}

// applyExact rebuilds a fleet interval's phases from the raw events whose
// attempts and AvgResponseMs the exact pass has just written onto it, so the
// stacked phases still add up to that average. It rebuilds them only when the
// whole fleet reported phases (coverage): a fleet with a generator that
// archived none stays unknown in every window, because that generator's raw
// business-transaction events may lack phase timings and, bucketed by start
// time, can land in any window, including one whose provisional row carries
// another generator's phases. The observation counts, and with them the
// split, are restored on the same fleet-wide terms. A window with no raw
// attempts has no phases either.
func (s *archiveRawPhaseSums) applyExact(record *IntervalSummaryRecord, attempts int64, coverage archiveFleetPhaseCoverage) {
	if record == nil {
		return
	}
	record.AvgDNSMs, record.AvgConnectMs, record.AvgTLSMs, record.AvgTTFBMs, record.AvgWaitMs = 0, 0, 0, 0, 0
	record.PhaseObserved, record.AvgServerMs, record.AvgNoResponseMs = nil, nil, nil
	reported := coverage.observed
	if !coverage.phases || attempts <= 0 {
		return
	}
	divisor := float64(attempts)
	record.AvgDNSMs = s.dnsMs / divisor
	record.AvgConnectMs = s.connectMs / divisor
	record.AvgTLSMs = s.tlsMs / divisor
	record.AvgTTFBMs = s.ttfbMs / divisor
	if reported {
		// The raw events carry every timing the observations are built from.
		observed := s.observed
		record.PhaseObserved = &observed
	}
	var download float64
	var ok bool
	record.AvgServerMs, record.AvgNoResponseMs, download, ok = phaseSplitFields(record.PhaseObserved, attempts, record.AvgResponseMs, record.AvgDNSMs, record.AvgConnectMs, record.AvgTLSMs, record.AvgTTFBMs)
	if ok {
		record.AvgWaitMs = download
	} else if wait := record.AvgResponseMs - record.AvgTTFBMs; wait > 0 {
		record.AvgWaitMs = wait
	}
}
