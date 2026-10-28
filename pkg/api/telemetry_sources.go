package api

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	telemetrySourcesFileName           = "telemetry_sources.jsonl"
	telemetrySourcesRecordType         = "telemetry_source_epochs_v1"
	telemetrySourceEpochSchema         = "apexload.telemetry_source_epoch.v1"
	envArchiveTelemetrySourcesMaxBytes = "APEXLOAD_ARCHIVE_TELEMETRY_SOURCES_MAX_BYTES"
	// maxTelemetrySourceEpochs bounds one run's ledger; further epochs are
	// counted as truncated rather than tracked.
	maxTelemetrySourceEpochs = 1024
	// maxTelemetrySourceRanges bounds the received-sequence ranges kept per
	// epoch. Past it, loss is reported as a lower bound.
	maxTelemetrySourceRanges = 128
)

// TelemetrySourceEpoch is the publication coverage of one generator process
// (source epoch) within one execution generation. Lost publications are
// sequence numbers missing between the first and last received. The count
// is exact only when the epoch was observed from its first publication to
// its terminal one and every received range was retained; otherwise it is a
// lower bound (an epoch replaced by a restarted process, or one whose stream
// ended early, never published its terminal record here). An unobserved
// record stands for an execution generation this controller never saw, such
// as the generations before an HA adoption.
type TelemetrySourceEpoch struct {
	CollectionWindows          []TelemetryCollectionEvidence `json:"collection_windows,omitempty"`
	CollectionWindowsTruncated bool                          `json:"collection_windows_truncated,omitempty"`

	Schema              string     `json:"schema"`
	Source              string     `json:"source"`
	ExecutionGeneration uint64     `json:"execution_generation"`
	Epoch               string     `json:"epoch,omitempty"`
	EpochStartedAt      *time.Time `json:"epoch_started_at,omitempty"`
	FirstSequence       uint64     `json:"first_sequence,omitempty"`
	LastSequence        uint64     `json:"last_sequence,omitempty"`
	Received            int64      `json:"received"`
	Duplicates          int64      `json:"duplicates,omitempty"`
	OutOfOrder          int64      `json:"out_of_order,omitempty"`
	Unsequenced         int64      `json:"unsequenced,omitempty"`
	TerminalSequence    uint64     `json:"terminal_sequence,omitempty"`
	LostPublications    int64      `json:"lost_publications"`
	LossExact           bool       `json:"loss_exact"`
	Unobserved          bool       `json:"unobserved,omitempty"`
	RangesTruncated     bool       `json:"ranges_truncated,omitempty"`
	FirstObservedAt     time.Time  `json:"first_observed_at"`
	LastObservedAt      time.Time  `json:"last_observed_at"`
	// Counters holds each loss counter this epoch reported: the process's
	// cumulative count at its last publication, exact or a lower bound.
	Counters map[string]TelemetryCounterTotal `json:"counters,omitempty"`

	ranges   [][2]uint64
	counters map[string]*telemetryCounterTrack
}

// Loss counters accounted per source epoch and per run.
const (
	TelemetryCounterDroppedResults           = "dropped_results"
	TelemetryCounterShedArrivals             = "shedded_arrivals"
	TelemetryCounterDroppedSubscriberBatches = "dropped_subscriber_batches"
	TelemetryCounterLostPublications         = "lost_publications"
	TelemetryCounterWebsocketDroppedMessages = "websocket_dropped_messages"
	TelemetryCounterWebsocketDroppedEvents   = "websocket_dropped_events"
)

// telemetryEpochCounters are the cumulative counters a generator reports
// for its test, from zero, in every publication.
var telemetryEpochCounters = []string{TelemetryCounterDroppedResults, TelemetryCounterShedArrivals, TelemetryCounterDroppedSubscriberBatches}

// States of a loss counter total.
const (
	TelemetryCounterExact       = "exact"
	TelemetryCounterLowerBound  = "lower_bound"
	TelemetryCounterUnavailable = "unavailable"
)

// TelemetryCounterTotal is a loss counter's total: exact, an observed lower
// bound, or unavailable, with the reason it is not exact.
type TelemetryCounterTotal struct {
	State  string `json:"state"`
	Value  int64  `json:"value"`
	Reason string `json:"reason,omitempty"`
}

// TelemetryLossAccounting is a run's total of each loss counter. A counter
// that does not apply (subscriber drops and lost publications in a run
// executed in this process) is absent.
type TelemetryLossAccounting struct {
	DroppedResults           *TelemetryCounterTotal `json:"dropped_results,omitempty"`
	ShedArrivals             *TelemetryCounterTotal `json:"shedded_arrivals,omitempty"`
	DroppedSubscriberBatches *TelemetryCounterTotal `json:"dropped_subscriber_batches,omitempty"`
	LostPublications         *TelemetryCounterTotal `json:"lost_publications,omitempty"`
	WebsocketDroppedMessages *TelemetryCounterTotal `json:"websocket_dropped_messages,omitempty"`
	WebsocketDroppedEvents   *TelemetryCounterTotal `json:"websocket_dropped_events,omitempty"`
}

func telemetryLossAccounting(totals map[string]TelemetryCounterTotal) *TelemetryLossAccounting {
	if len(totals) == 0 {
		return nil
	}
	pick := func(name string) *TelemetryCounterTotal {
		if total, ok := totals[name]; ok {
			return &total
		}
		return nil
	}
	return &TelemetryLossAccounting{
		DroppedResults: pick(TelemetryCounterDroppedResults), ShedArrivals: pick(TelemetryCounterShedArrivals),
		DroppedSubscriberBatches: pick(TelemetryCounterDroppedSubscriberBatches), LostPublications: pick(TelemetryCounterLostPublications),
		WebsocketDroppedMessages: pick(TelemetryCounterWebsocketDroppedMessages), WebsocketDroppedEvents: pick(TelemetryCounterWebsocketDroppedEvents),
	}
}

func (a *TelemetryLossAccounting) clone() *TelemetryLossAccounting {
	if a == nil {
		return nil
	}
	out := *a
	for _, field := range []**TelemetryCounterTotal{&out.DroppedResults, &out.ShedArrivals, &out.DroppedSubscriberBatches, &out.LostPublications, &out.WebsocketDroppedMessages, &out.WebsocketDroppedEvents} {
		if *field != nil {
			copied := **field
			*field = &copied
		}
	}
	return &out
}

// telemetryCounterTrack follows one cumulative counter through an epoch's
// publications in sequence order.
type telemetryCounterTrack struct {
	usable, invalid, absent int64
	processWideOnly         bool
	max                     int64
	lastSequence            uint64
	last                    int64
	unordered               bool
	regressed               bool
	terminal                bool
	terminalValue           int64
}

type telemetryCounterReading struct {
	value       int64
	absent      bool
	processWide bool
}

// telemetryCounterReadings reads the epoch counters one publication carries.
// A state-only publication carries none (the controller marks its missing
// telemetry with DroppedResultsUnavailable on receipt); a generator that
// reports only its process-wide subscriber drops cannot attribute them to
// this test.
func telemetryCounterReadings(batch generatorrpc.MetricsBatch) map[string]telemetryCounterReading {
	t := batch.Telemetry
	if t == nil || t.DroppedResultsUnavailable {
		return map[string]telemetryCounterReading{TelemetryCounterDroppedResults: {absent: true}, TelemetryCounterShedArrivals: {absent: true}, TelemetryCounterDroppedSubscriberBatches: {absent: true}}
	}
	readings := map[string]telemetryCounterReading{
		TelemetryCounterDroppedResults: {value: t.DroppedResults},
		TelemetryCounterShedArrivals:   {value: t.SheddedArrivals},
	}
	if t.TestDroppedSubscriberBatchesReported {
		readings[TelemetryCounterDroppedSubscriberBatches] = telemetryCounterReading{value: t.TestDroppedSubscriberBatches}
	} else {
		readings[TelemetryCounterDroppedSubscriberBatches] = telemetryCounterReading{absent: true, processWide: true}
	}
	return readings
}

// observeCounters records one new (not duplicate) publication's counters.
func (r *TelemetrySourceEpoch) observeCounters(batch generatorrpc.MetricsBatch) {
	if r.counters == nil {
		r.counters = map[string]*telemetryCounterTrack{}
	}
	sequence, terminal := batch.BatchSequence, isTerminalRemoteLifecycle(batch.LifecycleState)
	for name, reading := range telemetryCounterReadings(batch) {
		track := r.counters[name]
		if track == nil {
			track = &telemetryCounterTrack{}
			r.counters[name] = track
		}
		switch {
		case reading.absent:
			track.absent++
			track.processWideOnly = track.processWideOnly || reading.processWide
			continue
		case reading.value < 0:
			// Negative is malformed, or an overflowed counter.
			track.invalid++
			continue
		}
		track.usable++
		track.max = max(track.max, reading.value)
		if sequence == 0 {
			track.unordered = true
			continue
		}
		if sequence > track.lastSequence {
			// A cumulative counter never decreases within one process.
			if track.lastSequence != 0 && reading.value < track.last {
				track.regressed = true
			}
			track.lastSequence, track.last = sequence, reading.value
		} else if reading.value > track.last {
			// An older publication reported more than a newer one.
			track.regressed = true
		}
		if terminal {
			track.terminal, track.terminalValue = true, reading.value
		}
	}
}

// total settles a counter: exact only within one fully observed epoch, from
// its first publication (the count starts at zero with the process; a
// state-only publication carries none and changes nothing) to its terminal
// one, with every publication received and the count never decreasing.
// Anything less is the largest count seen, a lower bound, or unavailable.
func (t *telemetryCounterTrack) total(r *TelemetrySourceEpoch) TelemetryCounterTotal {
	if t == nil || t.usable == 0 {
		reason := "not_reported"
		switch {
		case t != nil && t.invalid > 0:
			reason = "invalid_value"
		case t != nil && t.processWideOnly:
			reason = "process_wide_only"
		}
		return TelemetryCounterTotal{State: TelemetryCounterUnavailable, Reason: reason}
	}
	reason := ""
	switch {
	case r.Epoch == "":
		reason = "legacy_source"
	case t.regressed:
		reason = "counter_regressed"
	case t.invalid > 0:
		reason = "invalid_value"
	case t.unordered:
		reason = "unsequenced_publication"
	case r.FirstSequence != 1:
		reason = "origin_unobserved"
	case !t.terminal:
		reason = "terminal_unobserved"
	case !r.LossExact:
		reason = "epoch_not_fully_observed"
	case r.LostPublications > 0:
		reason = "publications_lost"
	}
	if reason == "" {
		return TelemetryCounterTotal{State: TelemetryCounterExact, Value: t.terminalValue}
	}
	return TelemetryCounterTotal{State: TelemetryCounterLowerBound, Value: t.max, Reason: reason}
}

// TelemetrySourceSummary is the live roll-up of a run's source epochs.
type TelemetrySourceSummary struct {
	Sources               int   `json:"sources"`
	Epochs                int   `json:"epochs"`
	LegacyEpochs          int   `json:"legacy_epochs,omitempty"`
	UnobservedGenerations int   `json:"unobserved_generations,omitempty"`
	LostPublications      int64 `json:"lost_publications"`
	LossExact             bool  `json:"loss_exact"`
	Truncated             bool  `json:"truncated,omitempty"`
}

type telemetrySourceKey struct {
	source     string
	generation uint64
	epoch      string
}

type telemetrySourceLedger struct {
	collections           []TelemetryCollectionEvidence
	collectionsTruncated  bool
	mu                    sync.Mutex
	epochs                map[telemetrySourceKey]*TelemetrySourceEpoch
	truncated             bool
	jointStreams          map[telemetrySourceKey]*telemetryJointStream
	jointStreamsTruncated bool
	// coverage is each source's observed wall time, requirements when each
	// had to be observed and cursors its last sequenced publication (see
	// telemetry_coverage.go).
	coverage     map[string]*coverageSpans
	requirements map[string]*telemetrySourceRequirement
	cursors      map[string]*coverageCursor
	// adopted records a controller discontinuity. Committed earlier spans
	// may survive recovery, but the unobserved takeover gap remains unknown.
	adopted bool
}

// markAdopted records that this controller adopted the run.
func (l *telemetrySourceLedger) markAdopted() {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.adopted = true
	l.mu.Unlock()
}

// wasAdopted reports whether this controller adopted the run.
func (l *telemetrySourceLedger) wasAdopted() bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.adopted
}

func newTelemetrySourceLedger() *telemetrySourceLedger {
	return &telemetrySourceLedger{
		epochs:       map[telemetrySourceKey]*TelemetrySourceEpoch{},
		jointStreams: map[telemetrySourceKey]*telemetryJointStream{},
	}
}

// observe records one received publication, including duplicates and
// late arrivals that the live view rejects.
func (l *telemetrySourceLedger) observe(source string, batch generatorrpc.MetricsBatch, at time.Time) {
	if l == nil || source == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	key := telemetrySourceKey{source: source, generation: batch.ExecutionGeneration, epoch: batch.SourceEpoch}
	record := l.epochs[key]
	if record == nil {
		if len(l.epochs) >= maxTelemetrySourceEpochs {
			l.truncated = true
			return
		}
		record = &TelemetrySourceEpoch{Schema: telemetrySourceEpochSchema, Source: source, ExecutionGeneration: batch.ExecutionGeneration, Epoch: batch.SourceEpoch, FirstObservedAt: at.UTC()}
		if batch.SourceEpochStartedUnixMs > 0 {
			started := time.UnixMilli(batch.SourceEpochStartedUnixMs).UTC()
			record.EpochStartedAt = &started
		}
		l.epochs[key] = record
	}
	record.LastObservedAt = at.UTC()
	record.Received++
	sequence := batch.BatchSequence
	if sequence == 0 {
		record.Unsequenced++
		record.observeCounters(batch)
		l.observeCoverage(source, batch, at)
		return
	}
	priorTerminal := record.TerminalSequence
	if isTerminalRemoteLifecycle(batch.LifecycleState) && sequence > record.TerminalSequence {
		record.TerminalSequence = sequence
	}
	if record.covers(sequence) {
		// A retried publication repeats a cumulative count already seen.
		record.Duplicates++
		return
	}
	if record.LastSequence != 0 && sequence < record.LastSequence {
		record.OutOfOrder++
	}
	if batch.SequencePrefixStateOnly && sequence > 1 {
		// A registration snapshot after only state-only publications
		// (staging, activation): nothing before it was lost, so the epoch's
		// origin is observed.
		record.addRange(1, sequence-1)
		l.observeJointBenignRangeLocked(key, 1, sequence-1)
	}
	if priorTerminal != 0 && sequence > priorTerminal && !isTerminalRemoteLifecycle(batch.LifecycleState) {
		l.observeJointBenignRangeLocked(key, sequence, sequence)
	} else if strings.EqualFold(strings.TrimSpace(batch.LifecycleState), "STAGED") {
		l.observeJointBenignRangeLocked(key, sequence, sequence)
	}
	record.add(sequence)
	record.observeCounters(batch)
	l.observeCoverage(source, batch, at)
}

func (r *TelemetrySourceEpoch) covers(sequence uint64) bool {
	for _, span := range r.ranges {
		if sequence >= span[0] && sequence <= span[1] {
			return true
		}
	}
	return false
}

func (r *TelemetrySourceEpoch) add(sequence uint64) {
	r.addRange(sequence, sequence)
}

// addRange records sequences from..to as held.
func (r *TelemetrySourceEpoch) addRange(from, to uint64) {
	if r.FirstSequence == 0 || from < r.FirstSequence {
		r.FirstSequence = from
	}
	if to > r.LastSequence {
		r.LastSequence = to
	}
	r.ranges = append(r.ranges, [2]uint64{from, to})
	sort.Slice(r.ranges, func(i, j int) bool { return r.ranges[i][0] < r.ranges[j][0] })
	merged := r.ranges[:1]
	for _, span := range r.ranges[1:] {
		last := &merged[len(merged)-1]
		if span[0] <= last[1]+1 {
			last[1] = max(last[1], span[1])
			continue
		}
		merged = append(merged, span)
	}
	r.ranges = merged
	for len(r.ranges) > maxTelemetrySourceRanges {
		// Fold the oldest gap into its neighbour. That gap's loss is no
		// longer counted, so the total becomes a lower bound.
		r.ranges[1][0] = r.ranges[0][0]
		r.ranges = r.ranges[1:]
		r.RangesTruncated = true
	}
}

// settle computes the loss fields from the retained ranges, then the
// epoch's counter totals.
func (r *TelemetrySourceEpoch) settle() {
	r.settleLoss()
	r.Counters = map[string]TelemetryCounterTotal{}
	for _, name := range telemetryEpochCounters {
		if r.Unobserved {
			r.Counters[name] = TelemetryCounterTotal{State: TelemetryCounterUnavailable, Reason: "generation_unobserved"}
			continue
		}
		r.Counters[name] = r.counters[name].total(r)
	}
}

// telemetryLostPublicationsCap bounds one count of lost publications, so
// sums of them cannot wrap.
const telemetryLostPublicationsCap = uint64(1 << 62)

func (r *TelemetrySourceEpoch) settleLoss() {
	r.LostPublications, r.LossExact = 0, false
	if r.LastSequence == 0 {
		return
	}
	var held uint64
	for _, span := range r.ranges {
		held += span[1] - span[0] + 1
	}
	span := r.LastSequence - r.FirstSequence + 1
	clamped := false
	if span > held {
		lost := span - held
		if lost > telemetryLostPublicationsCap {
			// Only a malformed generator numbers this far: the count is a
			// lower bound.
			lost, clamped = telemetryLostPublicationsCap, true
		}
		r.LostPublications = int64(lost)
	}
	// Publications before the first one received are unknown unless the
	// first one received is the epoch's first, and those after the last one
	// received are unknown unless the epoch's terminal publication arrived.
	r.LossExact = r.Epoch != "" && r.FirstSequence == 1 && r.TerminalSequence != 0 && r.TerminalSequence == r.LastSequence && !r.RangesTruncated && !clamped
}

// snapshot returns settled copies, ordered by source, generation and first
// observation, plus an unobserved record for every earlier execution
// generation (from the run's failovers) that this ledger never saw.
func (l *telemetrySourceLedger) snapshot(failovers ...RunFailover) ([]TelemetrySourceEpoch, bool) {
	return l.snapshotRecords(false, failovers...)
}

func (l *telemetrySourceLedger) snapshotRecords(includeWindows bool, failovers ...RunFailover) ([]TelemetrySourceEpoch, bool) {
	if l == nil {
		return unobservedTelemetryGenerations(nil, failovers), false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]TelemetrySourceEpoch, 0, len(l.epochs))
	for _, record := range l.epochs {
		copied := *record
		copied.CollectionWindowsTruncated = l.collectionsTruncated
		if includeWindows {
			for _, c := range l.collections {
				if c.Source == record.Source && c.Generation == record.ExecutionGeneration && c.Epoch == record.Epoch {
					copied.CollectionWindows = append(copied.CollectionWindows, cloneTelemetryCollections([]TelemetryCollectionEvidence{c})[0])
				}
			}
		}
		copied.ranges = append([][2]uint64(nil), record.ranges...)
		// settle reads the live counter tracks under the ledger lock; the
		// copy keeps only their settled totals.
		copied.settle()
		copied.counters = nil
		out = append(out, copied)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		if a.ExecutionGeneration != b.ExecutionGeneration {
			return a.ExecutionGeneration < b.ExecutionGeneration
		}
		if !a.FirstObservedAt.Equal(b.FirstObservedAt) {
			return a.FirstObservedAt.Before(b.FirstObservedAt)
		}
		return a.Epoch < b.Epoch
	})
	return unobservedTelemetryGenerations(out, failovers), l.truncated
}

// unobservedTelemetryGenerations appends a record for each prior execution
// generation of an adopted run that no observed epoch covers: its coverage is
// unknown, never exact.
func unobservedTelemetryGenerations(epochs []TelemetrySourceEpoch, failovers []RunFailover) []TelemetrySourceEpoch {
	observed := map[uint64]bool{}
	for _, epoch := range epochs {
		observed[epoch.ExecutionGeneration] = true
	}
	for _, failover := range failovers {
		if failover.PriorGeneration == 0 || observed[failover.PriorGeneration] {
			continue
		}
		observed[failover.PriorGeneration] = true
		unobserved := TelemetrySourceEpoch{Schema: telemetrySourceEpochSchema, Source: "*", ExecutionGeneration: failover.PriorGeneration, Unobserved: true, FirstObservedAt: failover.AdoptedAt.UTC(), LastObservedAt: failover.AdoptedAt.UTC()}
		unobserved.settle()
		epochs = append(epochs, unobserved)
	}
	return epochs
}

func (l *telemetrySourceLedger) summary(failovers ...RunFailover) *TelemetrySourceSummary {
	epochs, truncated := l.snapshot(failovers...)
	return telemetrySourceSummaryOf(epochs, truncated)
}

// rollup is the live summary and loss totals from one settled snapshot.
func (l *telemetrySourceLedger) rollup(failovers ...RunFailover) (*TelemetrySourceSummary, map[string]TelemetryCounterTotal) {
	epochs, truncated := l.snapshot(failovers...)
	return telemetrySourceSummaryOf(epochs, truncated), telemetryLossTotalsOf(epochs, truncated)
}

func telemetrySourceSummaryOf(epochs []TelemetrySourceEpoch, truncated bool) *TelemetrySourceSummary {
	if len(epochs) == 0 && !truncated {
		return nil
	}
	summary := &TelemetrySourceSummary{LossExact: !truncated, Truncated: truncated}
	sources := map[string]bool{}
	for _, epoch := range epochs {
		summary.LossExact = summary.LossExact && epoch.LossExact
		if epoch.Unobserved {
			summary.UnobservedGenerations++
			continue
		}
		summary.Epochs++
		sources[epoch.Source] = true
		if epoch.Epoch == "" {
			summary.LegacyEpochs++
		}
		if epoch.LostPublications > math.MaxInt64-summary.LostPublications {
			// A saturated sum is a lower bound.
			summary.LostPublications, summary.LossExact = math.MaxInt64, false
		} else {
			summary.LostPublications += epoch.LostPublications
		}
	}
	summary.Sources = len(sources)
	return summary
}

// lossTotals is the run's total of each loss counter. A generator process
// keeps one cumulative count across every execution generation it serves,
// so each process contributes its latest generation's count once, and
// separate processes (epochs) add up. A total is exact only when every
// process's count is exact, no execution generation went unobserved and the
// ledger kept every epoch.
func (l *telemetrySourceLedger) lossTotals(failovers ...RunFailover) map[string]TelemetryCounterTotal {
	epochs, truncated := l.snapshot(failovers...)
	return telemetryLossTotalsOf(epochs, truncated)
}

func telemetryLossTotalsOf(epochs []TelemetrySourceEpoch, truncated bool) map[string]TelemetryCounterTotal {
	if len(epochs) == 0 && !truncated {
		return nil
	}
	unobserved := false
	for _, epoch := range epochs {
		unobserved = unobserved || epoch.Unobserved
	}
	type processKey struct{ source, epoch string }
	totals := map[string]TelemetryCounterTotal{}
	for _, name := range telemetryEpochCounters {
		processes := map[processKey]TelemetryCounterTotal{}
		latest := map[processKey]uint64{}
		for _, epoch := range epochs {
			if epoch.Unobserved {
				continue
			}
			key, count := processKey{epoch.Source, epoch.Epoch}, epoch.Counters[name]
			previous, seen := processes[key]
			switch {
			case !seen:
				processes[key], latest[key] = count, epoch.ExecutionGeneration
			case count.State == TelemetryCounterUnavailable:
			case previous.State == TelemetryCounterUnavailable:
				processes[key], latest[key] = count, epoch.ExecutionGeneration
			case epoch.Epoch == "":
				// Legacy processes cannot be told apart: their largest
				// count is a lower bound.
				processes[key] = TelemetryCounterTotal{State: TelemetryCounterLowerBound, Value: max(previous.Value, count.Value), Reason: "legacy_source"}
			case epoch.ExecutionGeneration > latest[key]:
				if count.Value < previous.Value {
					count = TelemetryCounterTotal{State: TelemetryCounterLowerBound, Value: previous.Value, Reason: "counter_regressed"}
				}
				processes[key], latest[key] = count, epoch.ExecutionGeneration
			case count.Value > previous.Value:
				// An earlier generation reported more than a later one. The
				// snapshot orders generations ascending, so this only guards
				// the totals against a different order.
				processes[key] = TelemetryCounterTotal{State: TelemetryCounterLowerBound, Value: count.Value, Reason: "counter_regressed"}
			}
		}
		keys := make([]processKey, 0, len(processes))
		for key := range processes {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].source != keys[j].source {
				return keys[i].source < keys[j].source
			}
			return keys[i].epoch < keys[j].epoch
		})
		counts := make([]TelemetryCounterTotal, 0, len(keys))
		for _, key := range keys {
			counts = append(counts, processes[key])
		}
		totals[name] = sumTelemetryCounterTotals(counts, truncated, unobserved)
	}
	lost := TelemetryCounterTotal{State: TelemetryCounterExact}
	legacy, overflowed := false, false
	for _, epoch := range epochs {
		if epoch.Unobserved {
			continue
		}
		legacy = legacy || epoch.Epoch == ""
		const lostCap = int64(telemetryLostPublicationsCap)
		// An epoch whose own count was clamped is already a lower bound.
		overflowed = overflowed || epoch.LostPublications >= lostCap
		if epoch.LostPublications > lostCap-lost.Value {
			// Clamp before adding: the sum cannot wrap.
			lost.Value, overflowed = lostCap, true
		} else {
			lost.Value += epoch.LostPublications
		}
		if !epoch.LossExact && lost.Reason == "" {
			lost.Reason = "epoch_not_fully_observed"
		}
	}
	switch {
	case truncated:
		lost.Reason = "ledger_truncated"
	case unobserved:
		lost.Reason = "generation_unobserved"
	case legacy:
		lost.Reason = "legacy_source"
	case overflowed:
		lost.Reason = "overflow"
	}
	if lost.Reason != "" {
		lost.State = TelemetryCounterLowerBound
	}
	totals[TelemetryCounterLostPublications] = lost
	addControllerWideTelemetryCounters(totals)
	return totals
}

// sumTelemetryCounterTotals adds per-process counts. Any process that is not
// exact, is unavailable, or overflows the sum leaves a lower bound; none
// usable leaves the total unavailable.
func sumTelemetryCounterTotals(counts []TelemetryCounterTotal, truncated, unobserved bool) TelemetryCounterTotal {
	total := TelemetryCounterTotal{State: TelemetryCounterExact}
	usable, firstUnavailable := false, ""
	for _, count := range counts {
		if count.State == TelemetryCounterUnavailable {
			if firstUnavailable == "" {
				firstUnavailable = count.Reason
			}
			continue
		}
		usable = true
		if count.State != TelemetryCounterExact && total.Reason == "" {
			total.Reason = count.Reason
		}
		if total.Value > math.MaxInt64-count.Value {
			total.Value, total.Reason = math.MaxInt64, "overflow"
			continue
		}
		total.Value += count.Value
	}
	if !usable {
		reason := firstUnavailable
		if reason == "" {
			reason = "not_reported"
		}
		return TelemetryCounterTotal{State: TelemetryCounterUnavailable, Reason: reason}
	}
	switch {
	case total.Reason != "":
	case firstUnavailable != "":
		total.Reason = "source_" + firstUnavailable
	case truncated:
		total.Reason = "ledger_truncated"
	case unobserved:
		total.Reason = "generation_unobserved"
	}
	if total.Reason != "" {
		total.State = TelemetryCounterLowerBound
	}
	return total
}

// addControllerWideTelemetryCounters marks the WebSocket hub's drop counters,
// which every run on the controller shares, as not attributable to one run.
func addControllerWideTelemetryCounters(totals map[string]TelemetryCounterTotal) {
	for _, name := range []string{TelemetryCounterWebsocketDroppedMessages, TelemetryCounterWebsocketDroppedEvents} {
		totals[name] = TelemetryCounterTotal{State: TelemetryCounterUnavailable, Reason: "controller_wide"}
	}
}

// telemetrySourceLedgerLocked returns the run's ledger, creating it on first
// use. It lives as long as the run's ManagedTest, so a finalization retry
// still finds it and a run that is never finalized here releases it with
// the test. The caller holds tr.mu.
func telemetrySourceLedgerLocked(test *ManagedTest) *telemetrySourceLedger {
	if test == nil {
		return nil
	}
	if test.telemetrySources == nil {
		test.telemetrySources = newTelemetrySourceLedger()
	}
	return test.telemetrySources
}

// observeTelemetryPublicationLocked counts one received publication in the
// run's source ledger while this stream still owns the target. Ownership, not
// the topology generation, decides: a topology change elsewhere re-fences the
// consumer's store loop, but the publication still arrived and is still
// stored, so it must still be counted.
func observeTelemetryPublicationLocked(tr *TestRunner, test *ManagedTest, target string, fence distributedMetricsConsumerFence, terminal bool, batch generatorrpc.MetricsBatch, now time.Time) bool {
	if !distributedMetricsConsumerStillOwnedLocked(tr, test, target, fence, terminal) {
		return false
	}
	telemetrySourceLedgerLocked(test).observe(target, batch, now)
	return true
}

// telemetrySourceSummaryLocked is the run's live source summary; generations
// absent from its retained ledger count as unobserved.
func telemetrySourceSummaryLocked(test *ManagedTest) *TelemetrySourceSummary {
	return test.telemetrySources.summary(test.continuityFailovers...)
}

// telemetrySourceRollupLocked is the run's live source summary and loss
// accounting, settled once.
func telemetrySourceRollupLocked(test *ManagedTest) (*TelemetrySourceSummary, *TelemetryLossAccounting) {
	summary, totals := test.telemetrySources.rollup(test.continuityFailovers...)
	return summary, telemetryLossAccounting(totals)
}

// persistTelemetrySourceArchive writes telemetry_sources.jsonl after
// finalize, with its own byte allowance. The ledger stays with the run, so a
// finalization retry rewrites the same sidecar; a manifest that already
// declares it is left alone.
func (tr *TestRunner) persistTelemetrySourceArchive(ctx context.Context, test *ManagedTest, record *storage.RunArchiveRecord, manifest *RunArchiveManifest) error {
	if test == nil || manifest.TelemetrySourcesFile != "" {
		return nil
	}
	tr.mu.RLock()
	ledger, failovers := test.telemetrySources, append([]RunFailover(nil), test.continuityFailovers...)
	tr.mu.RUnlock()
	epochs, truncated := ledger.snapshotRecords(true, failovers...)
	if len(epochs) == 0 {
		return nil
	}
	budget, err := newEvidenceSidecarBudget(ctx, record, envArchiveTelemetrySourcesMaxBytes, 4<<20)
	if err != nil {
		return err
	}
	defer budget.close()
	var count int64
	err = writeArchiveArtifactDurableWithWriter(record.ArchivePath, telemetrySourcesFileName, nil, 0600, func(file *os.File, _ []byte) (int, error) {
		out := &archiveOutputFile{file: file, budget: budget}
		// Preserve every epoch's pre-existing source/counter record before any
		// optional source windows: reserve each later record's window-free size.
		reserve := make([]int, len(epochs)+1)
		for i := len(epochs) - 1; i >= 0; i-- {
			base := epochs[i]
			base.CollectionWindows, base.CollectionWindowsTruncated = nil, true
			line, err := json.Marshal(base)
			if err != nil {
				return 0, err
			}
			reserve[i] = reserve[i+1] + len(line) + 1
		}
		for i, epoch := range epochs {
			line, err := json.Marshal(epoch)
			if err != nil {
				return 0, err
			}
			for !budget.admits(len(line)+1+reserve[i+1]) && len(epoch.CollectionWindows) > 0 {
				epoch.CollectionWindows = epoch.CollectionWindows[:len(epoch.CollectionWindows)/2]
				epoch.CollectionWindowsTruncated = true
				truncated = true
				line, err = json.Marshal(epoch)
				if err != nil {
					return 0, err
				}
			}
			line = append(line, '\n')
			if !budget.admits(len(line)) {
				truncated = true
				return 0, nil
			}
			if _, err := out.Write(line); err != nil {
				return 0, err
			}
			count++
		}
		return 0, nil
	}, ctx)
	if err != nil {
		return err
	}
	manifest.TelemetrySourcesFile = telemetrySourcesFileName
	manifest.TelemetrySourceEpochCount = count
	manifest.TelemetrySourcesTruncated = truncated
	// The ledger stays with the run: a later finalization step that fails
	// is retried from a reloaded manifest and rewrites this sidecar.
	return updateRunArchiveSingleArtifactIntegrity(record.ArchivePath, manifest, RunArchiveArtifact{Name: telemetrySourcesFileName, Kind: "evidence", Format: "jsonl", RecordType: telemetrySourcesRecordType, Count: count})
}

// localTelemetryLossTotals accounts a run executed in this process: its
// collector counts from zero and is read directly, so its counts are exact.
func localTelemetryLossTotals(droppedResults, shedArrivals int64) *TelemetryLossAccounting {
	totals := map[string]TelemetryCounterTotal{}
	for name, value := range map[string]int64{TelemetryCounterDroppedResults: droppedResults, TelemetryCounterShedArrivals: shedArrivals} {
		if value < 0 {
			totals[name] = TelemetryCounterTotal{State: TelemetryCounterUnavailable, Reason: "invalid_value"}
			continue
		}
		totals[name] = TelemetryCounterTotal{State: TelemetryCounterExact, Value: value}
	}
	addControllerWideTelemetryCounters(totals)
	return telemetryLossAccounting(totals)
}

// TelemetryJointWindow records the observation window bounds [StartUnixMs, EndUnixMs]
// for a contiguous publication sequence span [FirstSequence, LastSequence] of one
// (source, execution_generation, epoch) stream.
type TelemetryJointWindow struct {
	FirstSequence uint64 `json:"first_sequence"`
	LastSequence  uint64 `json:"last_sequence"`
	StartUnixMs   int64  `json:"start_ms"`
	EndUnixMs     int64  `json:"end_ms"`
}

// telemetryJointStream tracks the joint-evidence publication sequence ranges and
// observation window bounds for one (source, execution_generation, epoch) stream.
type telemetryJointStream struct {
	Source              string
	ExecutionGeneration uint64
	Epoch               string
	AcceptedRanges      [][2]uint64
	IgnoredRanges       [][2]uint64
	DroppedRanges       [][2]uint64
	Windows             []TelemetryJointWindow
	DroppedWindows      []TelemetryJointWindow
	FirstStartUnixMs    int64
	LastEndUnixMs       int64
	Truncated           bool
}

func (l *telemetrySourceLedger) jointStreamLocked(key telemetrySourceKey) *telemetryJointStream {
	if l.jointStreams == nil {
		l.jointStreams = map[telemetrySourceKey]*telemetryJointStream{}
	}
	stream := l.jointStreams[key]
	if stream != nil {
		return stream
	}
	if len(l.jointStreams) >= maxTelemetrySourceEpochs {
		l.jointStreamsTruncated = true
		return nil
	}
	stream = &telemetryJointStream{
		Source:              key.source,
		ExecutionGeneration: key.generation,
		Epoch:               key.epoch,
	}
	l.jointStreams[key] = stream
	return stream
}

func mergeJointSequenceRanges(ranges [][2]uint64, from, to uint64) ([][2]uint64, bool) {
	if from == 0 || to < from {
		return ranges, false
	}
	ranges = append(ranges, [2]uint64{from, to})
	sort.Slice(ranges, func(i, j int) bool { return ranges[i][0] < ranges[j][0] })
	merged := ranges[:1]
	for _, span := range ranges[1:] {
		last := &merged[len(merged)-1]
		if span[0] <= last[1]+1 {
			last[1] = max(last[1], span[1])
			continue
		}
		merged = append(merged, span)
	}
	truncated := false
	for len(merged) > maxTelemetrySourceRanges {
		merged[1][0] = merged[0][0]
		merged = merged[1:]
		truncated = true
	}
	return merged, truncated
}

func jointRangesCover(ranges [][2]uint64, seq uint64) bool {
	for _, span := range ranges {
		if seq >= span[0] && seq <= span[1] {
			return true
		}
	}
	return false
}

func mergeJointWindows(windows []TelemetryJointWindow, firstSeq, lastSeq uint64, startMs, endMs int64) ([]TelemetryJointWindow, bool) {
	if startMs < 0 {
		startMs = 0
	}
	if endMs < startMs {
		endMs = startMs
	}
	w := TelemetryJointWindow{FirstSequence: firstSeq, LastSequence: lastSeq, StartUnixMs: startMs, EndUnixMs: endMs}
	windows = append(windows, w)
	if firstSeq > 0 {
		sort.Slice(windows, func(i, j int) bool {
			if windows[i].FirstSequence != windows[j].FirstSequence {
				return windows[i].FirstSequence < windows[j].FirstSequence
			}
			return windows[i].StartUnixMs < windows[j].StartUnixMs
		})
		merged := windows[:1]
		for _, next := range windows[1:] {
			last := &merged[len(merged)-1]
			if last.FirstSequence > 0 && next.FirstSequence > 0 && next.FirstSequence <= last.LastSequence+1 {
				last.LastSequence = max(last.LastSequence, next.LastSequence)
				if next.StartUnixMs > 0 && (last.StartUnixMs == 0 || next.StartUnixMs < last.StartUnixMs) {
					last.StartUnixMs = next.StartUnixMs
				}
				last.EndUnixMs = max(last.EndUnixMs, next.EndUnixMs)
				continue
			}
			merged = append(merged, next)
		}
		windows = merged
	}
	truncated := false
	for len(windows) > maxTelemetrySourceRanges {
		if windows[0].StartUnixMs > 0 && (windows[1].StartUnixMs == 0 || windows[0].StartUnixMs < windows[1].StartUnixMs) {
			windows[1].StartUnixMs = windows[0].StartUnixMs
		}
		if windows[0].FirstSequence > 0 && (windows[1].FirstSequence == 0 || windows[0].FirstSequence < windows[1].FirstSequence) {
			windows[1].FirstSequence = windows[0].FirstSequence
		}
		windows[1].EndUnixMs = max(windows[1].EndUnixMs, windows[0].EndUnixMs)
		windows[1].LastSequence = max(windows[1].LastSequence, windows[0].LastSequence)
		windows = windows[1:]
		truncated = true
	}
	return windows, truncated
}

func (l *telemetrySourceLedger) observeJointBenignRangeLocked(key telemetrySourceKey, from, to uint64) {
	if l == nil || key.source == "" || from == 0 || to < from {
		return
	}
	stream := l.jointStreamLocked(key)
	if stream == nil {
		return
	}
	var truncated bool
	stream.IgnoredRanges, truncated = mergeJointSequenceRanges(stream.IgnoredRanges, from, to)
	stream.Truncated = stream.Truncated || truncated
}

func (l *telemetrySourceLedger) observeJointBenign(source string, batch generatorrpc.MetricsBatch) {
	if l == nil || source == "" || batch.BatchSequence == 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	key := telemetrySourceKey{source: source, generation: batch.ExecutionGeneration, epoch: batch.SourceEpoch}
	l.observeJointBenignRangeLocked(key, batch.BatchSequence, batch.BatchSequence)
}

func (l *telemetrySourceLedger) observeJointAccepted(source string, batch generatorrpc.MetricsBatch, startMs, endMs int64) {
	if l == nil || source == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	key := telemetrySourceKey{source: source, generation: batch.ExecutionGeneration, epoch: batch.SourceEpoch}
	stream := l.jointStreamLocked(key)
	if stream == nil {
		return
	}
	if startMs > 0 && (stream.FirstStartUnixMs == 0 || startMs < stream.FirstStartUnixMs) {
		stream.FirstStartUnixMs = startMs
	}
	if endMs > stream.LastEndUnixMs {
		stream.LastEndUnixMs = endMs
	}
	if batch.BatchSequence == 0 {
		return
	}
	var rTrunc, wTrunc bool
	stream.AcceptedRanges, rTrunc = mergeJointSequenceRanges(stream.AcceptedRanges, batch.BatchSequence, batch.BatchSequence)
	stream.Windows, wTrunc = mergeJointWindows(stream.Windows, batch.BatchSequence, batch.BatchSequence, startMs, endMs)
	stream.Truncated = stream.Truncated || rTrunc || wTrunc
}

func (l *telemetrySourceLedger) observeJointDropped(source string, batch generatorrpc.MetricsBatch, startMs, endMs int64) {
	if l == nil || source == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	key := telemetrySourceKey{source: source, generation: batch.ExecutionGeneration, epoch: batch.SourceEpoch}
	stream := l.jointStreamLocked(key)
	if stream == nil {
		return
	}
	stream.Truncated = true
	if batch.BatchSequence > 0 {
		stream.DroppedRanges, _ = mergeJointSequenceRanges(stream.DroppedRanges, batch.BatchSequence, batch.BatchSequence)
	}
	stream.DroppedWindows, _ = mergeJointWindows(stream.DroppedWindows, batch.BatchSequence, batch.BatchSequence, startMs, endMs)
}

func unionUint64Ranges(a, b [][2]uint64) [][2]uint64 {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	combined := make([][2]uint64, 0, len(a)+len(b))
	for _, span := range a {
		if span[0] > 0 && span[1] >= span[0] {
			combined = append(combined, span)
		}
	}
	for _, span := range b {
		if span[0] > 0 && span[1] >= span[0] {
			combined = append(combined, span)
		}
	}
	if len(combined) == 0 {
		return nil
	}
	sort.Slice(combined, func(i, j int) bool { return combined[i][0] < combined[j][0] })
	merged := combined[:1]
	for _, span := range combined[1:] {
		last := &merged[len(merged)-1]
		if span[0] <= last[1]+1 {
			last[1] = max(last[1], span[1])
			continue
		}
		merged = append(merged, span)
	}
	return merged
}

func subtractUint64Ranges(base, cut [][2]uint64) [][2]uint64 {
	if len(base) == 0 {
		return nil
	}
	cut = unionUint64Ranges(cut, nil)
	if len(cut) == 0 {
		return append([][2]uint64(nil), base...)
	}
	var out [][2]uint64
	for _, span := range unionUint64Ranges(base, nil) {
		curStart, curEnd := span[0], span[1]
		consumed := false
		for _, c := range cut {
			if c[1] < curStart {
				continue
			}
			if c[0] > curEnd {
				break
			}
			if c[0] > curStart {
				out = append(out, [2]uint64{curStart, c[0] - 1})
			}
			if c[1] >= curEnd {
				consumed = true
				break
			}
			curStart = c[1] + 1
		}
		if !consumed && curStart <= curEnd {
			out = append(out, [2]uint64{curStart, curEnd})
		}
	}
	return out
}

type jointInstalledBatchKey struct {
	Source     string
	Epoch      string
	Generation uint64
	Sequence   uint64
}

// reconcileJointEvidence compares the source publications present in the joint
// archive (installed) against the source epoch ledger, the per-stream joint
// sequence ledger, and the run's failovers/cursors. It reports whether the
// archived joint evidence is provably complete from the start of the run and
// returns exact JointTruncatedWindow entries for any missing or dropped spans.
func (l *telemetrySourceLedger) reconcileJointEvidence(
	installed map[jointInstalledBatchKey]bool,
	installedWindows map[jointInstalledBatchKey][2]int64,
	expectedSources []string,
	failovers []RunFailover,
	cursorWindows map[string]distributedWindowMark,
	requireOrigin bool,
) (complete bool, truncatedWindows []JointTruncatedWindow, verified bool) {
	if l == nil {
		return false, nil, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.epochs) == 0 && len(l.jointStreams) == 0 && len(failovers) == 0 {
		return false, nil, false
	}
	verified = true
	observedGen := map[uint64]bool{}
	keyMap := map[telemetrySourceKey]bool{}
	for k := range l.epochs {
		keyMap[k] = true
		observedGen[k.generation] = true
	}
	for k := range l.jointStreams {
		keyMap[k] = true
		observedGen[k.generation] = true
	}
	for source, mark := range cursorWindows {
		if source != "" && mark.batchSequence > 0 {
			k := telemetrySourceKey{source: source, generation: mark.generation, epoch: mark.epoch}
			keyMap[k] = true
			observedGen[k.generation] = true
		}
	}
	for _, failover := range failovers {
		if failover.PriorGeneration == 0 || observedGen[failover.PriorGeneration] {
			continue
		}
		observedGen[failover.PriorGeneration] = true
		endMs := failover.AdoptedAt.UnixMilli()
		startMs := endMs
		if failover.LastObservedAt != nil && !failover.LastObservedAt.IsZero() {
			startMs = failover.LastObservedAt.UnixMilli()
		}
		if startMs < 0 {
			startMs = 0
		}
		if endMs < startMs {
			endMs = startMs
		}
		truncatedWindows = append(truncatedWindows, JointTruncatedWindow{
			Source:              "*",
			ExecutionGeneration: failover.PriorGeneration,
			StartUnixMs:         startMs,
			EndUnixMs:           endMs,
			Reason:              "unobserved_generation",
		})
	}

	keys := make([]telemetrySourceKey, 0, len(keyMap))
	seenSources := map[string]bool{}
	for k := range keyMap {
		keys = append(keys, k)
		seenSources[k.source] = true
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].source != keys[j].source {
			return keys[i].source < keys[j].source
		}
		if keys[i].generation != keys[j].generation {
			return keys[i].generation < keys[j].generation
		}
		return keys[i].epoch < keys[j].epoch
	})

	installedByKey := map[telemetrySourceKey][][2]uint64{}
	for id := range installed {
		if id.Source == "" || id.Sequence == 0 {
			continue
		}
		k := telemetrySourceKey{source: id.Source, epoch: id.Epoch, generation: id.Generation}
		installedByKey[k], _ = mergeJointSequenceRanges(installedByKey[k], id.Sequence, id.Sequence)
		seenSources[id.Source] = true
	}

	windowBoundsFor := func(key telemetrySourceKey, fromSeq, toSeq uint64) (int64, int64) {
		var startMs, endMs int64
		applyBounds := func(s, e int64) {
			if s > 0 && (startMs == 0 || s < startMs) {
				startMs = s
			}
			if e > endMs {
				endMs = e
			}
		}
		if js := l.jointStreams[key]; js != nil {
			var prevEnd, nextStart int64
			for _, w := range js.Windows {
				if w.FirstSequence <= toSeq && w.LastSequence >= fromSeq {
					applyBounds(w.StartUnixMs, w.EndUnixMs)
				}
				if w.LastSequence < fromSeq && w.EndUnixMs > prevEnd {
					prevEnd = w.EndUnixMs
				}
				if w.FirstSequence > toSeq && w.StartUnixMs > 0 && (nextStart == 0 || w.StartUnixMs < nextStart) {
					nextStart = w.StartUnixMs
				}
			}
			for _, w := range js.DroppedWindows {
				if w.FirstSequence <= toSeq && w.LastSequence >= fromSeq {
					applyBounds(w.StartUnixMs, w.EndUnixMs)
				}
			}
			if startMs == 0 && prevEnd > 0 {
				startMs = prevEnd
			}
			if endMs == 0 && nextStart > 0 {
				endMs = nextStart
			}
		}
		if installedWindows != nil {
			if fromSeq > 0 {
				if b, ok := installedWindows[jointInstalledBatchKey{Source: key.source, Epoch: key.epoch, Generation: key.generation, Sequence: fromSeq}]; ok {
					applyBounds(b[0], b[1])
				} else if fromSeq > 1 {
					if prev, ok := installedWindows[jointInstalledBatchKey{Source: key.source, Epoch: key.epoch, Generation: key.generation, Sequence: fromSeq - 1}]; ok && startMs == 0 {
						startMs = prev[1]
					}
				}
			}
			if toSeq > 0 {
				if b, ok := installedWindows[jointInstalledBatchKey{Source: key.source, Epoch: key.epoch, Generation: key.generation, Sequence: toSeq}]; ok {
					applyBounds(b[0], b[1])
				} else if next, ok := installedWindows[jointInstalledBatchKey{Source: key.source, Epoch: key.epoch, Generation: key.generation, Sequence: toSeq + 1}]; ok && endMs == 0 {
					endMs = next[0]
				}
			}
		}
		for _, c := range l.collections {
			if c.Source == key.source && c.Generation == key.generation && c.Epoch == key.epoch && c.Publication >= fromSeq && c.Publication <= toSeq {
				for _, w := range c.Windows {
					applyBounds(w.Start.UnixMilli(), w.End.UnixMilli())
				}
			}
		}
		if mark, ok := cursorWindows[key.source]; ok && mark.generation == key.generation && mark.epoch == key.epoch && mark.batchSequence >= fromSeq && mark.batchSequence <= toSeq && mark.batchTimestamp > 0 {
			if endMs == 0 {
				endMs = mark.batchTimestamp
			}
			if startMs == 0 && mark.batchTimestamp >= 1000 {
				startMs = mark.batchTimestamp - 1000
			}
		}
		if ep := l.epochs[key]; ep != nil {
			if startMs == 0 {
				if fromSeq == 1 && ep.EpochStartedAt != nil && !ep.EpochStartedAt.IsZero() {
					startMs = ep.EpochStartedAt.UnixMilli()
				} else if !ep.FirstObservedAt.IsZero() {
					startMs = ep.FirstObservedAt.UnixMilli()
				}
			}
			if endMs == 0 && !ep.LastObservedAt.IsZero() {
				endMs = ep.LastObservedAt.UnixMilli()
			}
		}
		if startMs < 0 {
			startMs = 0
		}
		if endMs < startMs {
			endMs = startMs
		}
		return startMs, endMs
	}

	originComplete := len(keys) > 0 && !l.truncated && !l.jointStreamsTruncated
	for _, src := range expectedSources {
		if src != "" && !seenSources[src] {
			originComplete = false
		}
	}

	for _, key := range keys {
		ep := l.epochs[key]
		js := l.jointStreams[key]
		var ignoredRanges, droppedRanges, acceptedRanges [][2]uint64
		if js != nil {
			ignoredRanges = js.IgnoredRanges
			droppedRanges = js.DroppedRanges
			acceptedRanges = js.AcceptedRanges
			if js.Truncated || len(js.DroppedRanges) > 0 || len(js.DroppedWindows) > 0 {
				originComplete = false
			}
			for _, dw := range js.DroppedWindows {
				truncatedWindows = append(truncatedWindows, JointTruncatedWindow{
					Source:              key.source,
					ExecutionGeneration: key.generation,
					Epoch:               key.epoch,
					FirstSequence:       dw.FirstSequence,
					LastSequence:        dw.LastSequence,
					StartUnixMs:         dw.StartUnixMs,
					EndUnixMs:           dw.EndUnixMs,
					Reason:              "dropped_publications",
				})
			}
		}
		var epRanges [][2]uint64
		maxSeq := uint64(0)
		if ep != nil {
			epRanges = ep.ranges
			maxSeq = ep.LastSequence
			if ep.RangesTruncated || ep.Unobserved {
				originComplete = false
			}
		}
		if len(acceptedRanges) > 0 {
			if last := acceptedRanges[len(acceptedRanges)-1][1]; last > maxSeq {
				maxSeq = last
			}
		}
		if mark, ok := cursorWindows[key.source]; ok && mark.generation == key.generation && mark.epoch == key.epoch && mark.batchSequence > maxSeq {
			maxSeq = mark.batchSequence
			acceptedRanges = unionUint64Ranges(acceptedRanges, [][2]uint64{{mark.batchSequence, mark.batchSequence}})
		}

		// Sequences received or accepted by the controller that are missing from
		// the archived joint evidence (and not benign ignored or already counted as dropped).
		observedUnion := unionUint64Ranges(epRanges, acceptedRanges)
		covered := unionUint64Ranges(installedByKey[key], unionUint64Ranges(ignoredRanges, droppedRanges))
		uncommittedLost := subtractUint64Ranges(observedUnion, covered)
		for _, span := range uncommittedLost {
			startMs, endMs := windowBoundsFor(key, span[0], span[1])
			truncatedWindows = append(truncatedWindows, JointTruncatedWindow{
				Source:              key.source,
				ExecutionGeneration: key.generation,
				Epoch:               key.epoch,
				FirstSequence:       span[0],
				LastSequence:        span[1],
				StartUnixMs:         startMs,
				EndUnixMs:           endMs,
				Reason:              "uncommitted_batch_lost",
			})
		}

		// Sequence gaps between 1..maxSeq that were neither received nor ignored.
		if maxSeq > 0 {
			fullSpan := [][2]uint64{{1, maxSeq}}
			accounted := unionUint64Ranges(observedUnion, unionUint64Ranges(installedByKey[key], unionUint64Ranges(ignoredRanges, droppedRanges)))
			gaps := subtractUint64Ranges(fullSpan, accounted)
			for _, gap := range gaps {
				// If only js exists on a non-adopted run (from unit tests calling
				// rememberJointPublicationLocked directly on an isolated sequence > 1
				// without epoch tracking), only treat leading 1..first-1 as a missing
				// range when ep != nil or requireOrigin is true.
				if ep == nil && !requireOrigin && len(acceptedRanges) > 0 && gap[1] < acceptedRanges[0][0] {
					continue
				}
				originComplete = false
				startMs, endMs := windowBoundsFor(key, gap[0], gap[1])
				truncatedWindows = append(truncatedWindows, JointTruncatedWindow{
					Source:              key.source,
					ExecutionGeneration: key.generation,
					Epoch:               key.epoch,
					FirstSequence:       gap[0],
					LastSequence:        gap[1],
					StartUnixMs:         startMs,
					EndUnixMs:           endMs,
					Reason:              "missing_sequence_range",
				})
			}
		} else {
			originComplete = false
		}
	}

	complete = originComplete && len(truncatedWindows) == 0
	return complete, truncatedWindows, verified
}

