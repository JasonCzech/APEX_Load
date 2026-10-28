package api

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Wall-time telemetry coverage (TEL-1): the time each source was observed,
// and the run's healthy, degraded, breached and unknown time. Time no
// observation covers is unknown, never healthy.

const (
	// maxTelemetryCoverageSpans bounds one source's disjoint observed spans.
	// Past it the shortest span is dropped to admit a longer one, so
	// coverage is understated (a lower bound), never overstated.
	maxTelemetryCoverageSpans                   = 1024
	telemetryCoverageAlgorithm                  = "telemetry_coverage.v4"
	telemetryCoverageRuleQualifiedSourceWindows = "qualified_source_windows"
)

// coverageSpans is a bounded union of half-open [start, end) intervals in
// Unix milliseconds, kept sorted and disjoint.
type coverageSpans struct {
	spans     [][2]int64
	truncated bool
}

func (c *coverageSpans) add(start, end int64) {
	if c == nil || end <= start {
		return
	}
	// The first span that ends at or after start may merge with [start, end).
	i := sort.Search(len(c.spans), func(i int) bool { return c.spans[i][1] >= start })
	j := i
	for j < len(c.spans) && c.spans[j][0] <= end {
		start, end = min(start, c.spans[j][0]), max(end, c.spans[j][1])
		j++
	}
	if i == j {
		if len(c.spans) >= maxTelemetryCoverageSpans {
			c.truncated = true
			shortest := 0
			for k, span := range c.spans {
				if span[1]-span[0] < c.spans[shortest][1]-c.spans[shortest][0] {
					shortest = k
				}
			}
			if end-start <= c.spans[shortest][1]-c.spans[shortest][0] {
				return
			}
			c.spans = append(c.spans[:shortest], c.spans[shortest+1:]...)
			if shortest < i {
				i--
			}
		}
		c.spans = append(c.spans, [2]int64{})
		copy(c.spans[i+1:], c.spans[i:])
		c.spans[i] = [2]int64{start, end}
		return
	}
	c.spans[i] = [2]int64{start, end}
	c.spans = append(c.spans[:i+1], c.spans[j:]...)
}

// within is the covered length inside [from, to).
func (c *coverageSpans) within(from, to int64) int64 {
	if c == nil || to <= from {
		return 0
	}
	var covered int64
	for _, span := range c.spans {
		if span[1] <= from {
			continue
		}
		if span[0] >= to {
			break
		}
		covered += min(span[1], to) - max(span[0], from)
	}
	return covered
}

func (c *coverageSpans) clone() *coverageSpans {
	if c == nil {
		return nil
	}
	return &coverageSpans{spans: append([][2]int64(nil), c.spans...), truncated: c.truncated}
}

// unionSpans merges any intervals into a sorted, disjoint, unbounded list.
func unionSpans(intervals [][2]int64) [][2]int64 {
	sorted := make([][2]int64, 0, len(intervals))
	for _, interval := range intervals {
		if interval[1] > interval[0] {
			sorted = append(sorted, interval)
		}
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i][0] < sorted[j][0] })
	out := sorted[:0]
	for _, interval := range sorted {
		if n := len(out); n > 0 && interval[0] <= out[n-1][1] {
			out[n-1][1] = max(out[n-1][1], interval[1])
			continue
		}
		out = append(out, interval)
	}
	return out
}

// subtractSpans is a minus b, both sorted and disjoint, in one linear sweep.
func subtractSpans(a, b [][2]int64) [][2]int64 {
	out := make([][2]int64, 0, len(a))
	j := 0
	for _, interval := range a {
		start, end := interval[0], interval[1]
		for j < len(b) && b[j][1] <= start {
			j++
		}
		for k := j; k < len(b) && b[k][0] < end; k++ {
			if b[k][0] > start {
				out = append(out, [2]int64{start, b[k][0]})
			}
			start = max(start, b[k][1])
			if start >= end {
				break
			}
		}
		if start < end {
			out = append(out, [2]int64{start, end})
		}
	}
	return out
}

// telemetrySourceRequirement is when a source had to be observed: from its
// assignment (or first observation) until its retirement — an evacuation, a
// terminal publication or completion before an adoption — else the end of
// the window. Zero means not recorded.
type telemetrySourceRequirement struct {
	assignedAtMs int64
	retiredAtMs  int64
}

// coverageCursor keeps the current epoch's publication and collection high
// water marks. Sequence loss accounting separately retains late publications;
// their later receipt time cannot establish a new observation window.
type coverageCursor struct {
	key            telemetrySourceKey
	epochStartedMs int64
	sequence       uint64
	windowSequence uint64
	endMs          int64
	collection     distributedWindowMark
	terminal       bool
}

// TelemetrySourceCoverage is the time one source was observed in the run's
// window. RequiredMs is the part of the window it had to be observed for
// (from its assignment until its retirement: an evacuated, replaced or
// completed generator is required for less than the window) and UnknownMs
// the part of that it was not observed for.
type TelemetrySourceCoverage struct {
	Source     string `json:"source"`
	ObservedMs int64  `json:"observed_ms"`
	RequiredMs int64  `json:"required_ms"`
	UnknownMs  int64  `json:"unknown_ms"`
	Truncated  bool   `json:"truncated,omitempty"`
}

// TelemetryCoverage is wall-time observation coverage over a run's window:
// ObservedMs is the time every required source was observed and UnknownMs
// the rest of WindowMs. A source that never published counts as observed for
// none of it.
type TelemetryCoverage struct {
	WindowStart time.Time                 `json:"window_start"`
	WindowEnd   time.Time                 `json:"window_end"`
	WindowMs    int64                     `json:"window_ms"`
	ObservedMs  int64                     `json:"observed_ms"`
	UnknownMs   int64                     `json:"unknown_ms"`
	Sources     []TelemetrySourceCoverage `json:"sources"`
	// Truncated means source epochs or observed spans exceeded their bound, so
	// observed time is a lower bound.
	Truncated bool `json:"truncated,omitempty"`
	// ObserverLost says the controller reporting coverage did not observe
	// the whole run, so time another controller observed counts as unknown
	// and observed time is a lower bound: ledger_lost (a distributed run
	// finalized without its source ledger: after a restart, or by a
	// controller that never executed it) or adopted (a controller adopted
	// the run and observed it only from then).
	ObserverLost string `json:"observer_lost,omitempty"`
	// SourceRequirements says each source's RequiredMs and UnknownMs were
	// recorded; coverage written before them has only observed time.
	SourceRequirements bool `json:"source_requirements,omitempty"`
}

// Why a distributed run's observed time can be a lower bound.
const (
	telemetryCoverageLedgerLost = "ledger_lost"
	telemetryCoverageAdopted    = "adopted"
)

// TelemetryPolicySegment is a span of the run classified under one telemetry
// budget policy: the admission receipt (sequence 1), then each change made
// while the run ran (TEL-B). Its classified time counts the intervals the
// segment classified; an interval a boundary fell inside is unknown.
type TelemetryPolicySegment struct {
	Sequence             int64     `json:"sequence,omitempty"`
	Start                time.Time `json:"start"`
	End                  time.Time `json:"end"`
	PolicyHash           string    `json:"policy_hash,omitempty"`
	HealthyMs            int64     `json:"healthy_ms,omitempty"`
	DegradedMs           int64     `json:"degraded_ms,omitempty"`
	BreachedMs           int64     `json:"breached_ms,omitempty"`
	QualifiedCollections int64     `json:"qualified_collections,omitempty"`
	SourceWindowMs       int64     `json:"source_window_ms,omitempty"`
	// Unavailable says why a captured policy names no hash.
	Unavailable string `json:"unavailable,omitempty"`
	// Legacy marks a run admitted before policies were captured; its
	// thresholds are the controller settings in force at admission.
	Legacy bool `json:"legacy,omitempty"`
}

// TelemetryCoverageRecord is the run's wall-time telemetry account. Healthy,
// degraded and breached time count only where every required source was
// observed and the interval was classified; everything else in the window is
// unknown. BreachedKnownFraction is BreachedMs over ClassifiedMs, absent
// when nothing was classified.
type TelemetryCoverageRecord struct {
	Algorithm                   string                   `json:"algorithm"`
	CoverageRule                string                   `json:"coverage_rule,omitempty"`
	Coverage                    TelemetryCoverage        `json:"coverage"`
	HealthyMs                   int64                    `json:"healthy_ms"`
	DegradedMs                  int64                    `json:"degraded_ms"`
	BreachedMs                  int64                    `json:"breached_ms"`
	UnknownMs                   int64                    `json:"unknown_ms"`
	ClassifiedMs                int64                    `json:"classified_ms"`
	BreachedKnownFraction       *float64                 `json:"breached_known_fraction,omitempty"`
	RetainedIntervals           int64                    `json:"retained_intervals"`
	ClassifiedIntervals         int64                    `json:"classified_intervals"`
	QualifiedCollections        int64                    `json:"qualified_collections,omitempty"`
	UnscoredBoundaryCollections int64                    `json:"unscored_boundary_collections,omitempty"`
	UnqualifiedClockCollections int64                    `json:"unqualified_clock_collections,omitempty"`
	Segments                    []TelemetryPolicySegment `json:"segments"`
}

// observeCoverage records a fresh collection's receipt-time window. This
// remains an approximation, not a reconstruction of source-clock windows.
// Duplicate/out-of-order envelopes and repeated collections never move old
// evidence onto a later receipt window. Only consecutive window-bearing
// publications in the current epoch can bridge bounded receipt jitter.
// The caller holds l.mu and has admitted the epoch into the bounded ledger.
func (l *telemetrySourceLedger) observeCoverage(source string, batch generatorrpc.MetricsBatch, at time.Time) {
	end := at.UnixMilli()
	key := telemetrySourceKey{source: source, generation: batch.ExecutionGeneration, epoch: batch.SourceEpoch}
	if l.cursors == nil {
		l.cursors = map[string]*coverageCursor{}
	}
	cursor := l.cursors[source]
	if cursor != nil {
		if cursor.key != key {
			// An earlier generation never replaces current evidence. Within
			// a generation, preserve the live receiver's terminal and epoch
			// ordering rules as well.
			if key.generation < cursor.key.generation || key.generation == cursor.key.generation &&
				(cursor.terminal || key.epoch == "" || cursor.key.epoch != "" && batch.SourceEpochStartedUnixMs <= cursor.epochStartedMs) {
				return
			}
			// Adoption changes the execution generation, not the generator's
			// collection identity. Preserve deduplication but never bridge the
			// unobserved takeover gap with a receipt-time window.
			previous := cursor
			cursor = &coverageCursor{key: key, epochStartedMs: batch.SourceEpochStartedUnixMs}
			if previous.key.epoch == key.epoch && key.epoch != "" {
				cursor.collection = previous.collection
			}
			l.cursors[source] = cursor
		} else if cursor.terminal || cursor.sequence > 0 && batch.BatchSequence <= cursor.sequence {
			return
		}
	}
	if cursor == nil {
		cursor = &coverageCursor{key: key, epochStartedMs: batch.SourceEpochStartedUnixMs}
		l.cursors[source] = cursor
	}
	contiguous := cursor.windowSequence > 0 && batch.BatchSequence > cursor.windowSequence && batch.BatchSequence-cursor.windowSequence == 1
	cursor.sequence = batch.BatchSequence
	if windowMs := batch.WindowMs; windowMs > 0 && end >= cursor.endMs && cursor.freshCollection(batch) {
		start := end - windowMs
		if contiguous && cursor.endMs > 0 && cursor.endMs < start && start-cursor.endMs <= windowMs {
			start = cursor.endMs
		}
		if l.coverage == nil {
			l.coverage = map[string]*coverageSpans{}
		}
		spans := l.coverage[source]
		if spans == nil {
			spans = &coverageSpans{}
			l.coverage[source] = spans
		}
		spans.add(start, end)
		cursor.endMs, cursor.windowSequence = end, batch.BatchSequence
	}
	if isTerminalRemoteLifecycle(batch.LifecycleState) {
		l.retireLocked(source, at)
		cursor.terminal = true
	}
}

// freshCollection advances only a comparable collection identity. A legacy
// source with neither collection number nor publication time retains its
// receipt-window behavior; absence after an identified collection is not
// proof of new data. An epoch/generation change creates a fresh cursor.
func (c *coverageCursor) freshCollection(batch generatorrpc.MetricsBatch) bool {
	mark, _ := distributedCollectionMark(batch)
	switch {
	case c.collection.collection > 0:
		if mark.collection <= c.collection.collection {
			return false
		}
	case c.collection.publishedAt > 0:
		if mark.collection > 0 || mark.publishedAt <= c.collection.publishedAt {
			return false
		}
	}
	c.collection = mark
	return true
}

// assign records that source is required from at (the first assignment
// wins).
func (l *telemetrySourceLedger) assign(source string, at time.Time) {
	if l == nil || source == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	requirement := l.requirementLocked(source)
	if requirement.assignedAtMs == 0 {
		requirement.assignedAtMs = at.UnixMilli()
	}
}

// retire records that source is no longer required after at.
func (l *telemetrySourceLedger) retire(source string, at time.Time) {
	if l == nil || source == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.retireLocked(source, at)
}

// retireAtAdoption retires a source that completed during a takeover gap. A
// retirement restored from the checkpoint is an observed terminal publication
// and keeps its earlier boundary instead of extending to the adoption.
func (l *telemetrySourceLedger) retireAtAdoption(source string, at time.Time) {
	if l == nil || source == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	requirement := l.requirementLocked(source)
	if requirement.retiredAtMs == 0 {
		requirement.retiredAtMs = at.UnixMilli()
	}
}

func (l *telemetrySourceLedger) retireLocked(source string, at time.Time) {
	requirement := l.requirementLocked(source)
	if requirement.retiredAtMs == 0 || at.UnixMilli() > requirement.retiredAtMs {
		requirement.retiredAtMs = at.UnixMilli()
	}
}

func (l *telemetrySourceLedger) requirementLocked(source string) *telemetrySourceRequirement {
	if l.requirements == nil {
		l.requirements = map[string]*telemetrySourceRequirement{}
	}
	requirement := l.requirements[source]
	if requirement == nil {
		requirement = &telemetrySourceRequirement{}
		l.requirements[source] = requirement
	}
	return requirement
}

// coverageSnapshot copies each source's observed spans and requirement.
func (l *telemetrySourceLedger) coverageSnapshot() (map[string]*coverageSpans, map[string]telemetrySourceRequirement, bool) {
	if l == nil {
		return nil, nil, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	observed := make(map[string]*coverageSpans, len(l.coverage))
	for source, spans := range l.coverage {
		observed[source] = spans.clone()
	}
	requirements := make(map[string]telemetrySourceRequirement, len(l.requirements))
	for source, requirement := range l.requirements {
		requirements[source] = *requirement
	}
	return observed, requirements, l.truncated
}

// telemetryCoverageInterval is when one source had to be observed, clipped
// to the run's window.
type telemetryCoverageInterval struct {
	startMs, endMs int64
}

// telemetryCoverageOver measures coverage in [from, to): a moment is
// observed when at least one source was required then and every source
// required then was observed. It returns the coverage and the observed time.
// A run executed in this process (inProcess) is observed for its whole
// window; a distributed run without a known source for none of it.
func telemetryCoverageOver(observed map[string]*coverageSpans, required map[string]telemetryCoverageInterval, inProcess bool, from, to time.Time) (TelemetryCoverage, *coverageSpans) {
	start, end := from.UnixMilli(), to.UnixMilli()
	coverage := TelemetryCoverage{WindowStart: from.UTC(), WindowEnd: to.UTC(), Sources: []TelemetrySourceCoverage{}, SourceRequirements: true}
	if end <= start {
		return coverage, &coverageSpans{}
	}
	coverage.WindowMs = end - start
	window := [][2]int64{{start, end}}
	known := window
	if !inProcess {
		sources := make([]string, 0, len(required))
		for source := range required {
			sources = append(sources, source)
		}
		sort.Strings(sources)
		requiredSpans := make([][2]int64, 0, len(sources))
		var missing [][2]int64
		for _, source := range sources {
			interval := required[source]
			spans := observed[source]
			entry := TelemetrySourceCoverage{Source: source, ObservedMs: spans.within(start, end)}
			if spans != nil && spans.truncated {
				entry.Truncated, coverage.Truncated = true, true
			}
			span := [2]int64{max(interval.startMs, start), min(interval.endMs, end)}
			if span[1] <= span[0] {
				coverage.Sources = append(coverage.Sources, entry)
				continue
			}
			requiredSpans = append(requiredSpans, span)
			var seen [][2]int64
			if spans != nil {
				seen = spans.spans
			}
			unseen := subtractSpans([][2]int64{span}, seen)
			entry.RequiredMs = span[1] - span[0]
			for _, gap := range unseen {
				entry.UnknownMs += gap[1] - gap[0]
			}
			coverage.Sources = append(coverage.Sources, entry)
			missing = append(missing, unseen...)
		}
		// Time no source was required for is unknown too.
		unknown := unionSpans(append(subtractSpans(window, unionSpans(requiredSpans)), missing...))
		known = subtractSpans(window, unknown)
	}
	all := &coverageSpans{spans: known}
	coverage.ObservedMs = all.within(start, end)
	coverage.UnknownMs = coverage.WindowMs - coverage.ObservedMs
	return coverage, all
}

// requiredTelemetrySources is when each source the run published from or
// was assigned had to be observed within [from, to): from its recorded
// assignment, else its first observation, else the window start; until its
// retirement, else the window end. An assigned source this controller has no
// record of is required for the whole window.
func requiredTelemetrySources(observed map[string]*coverageSpans, requirements map[string]telemetrySourceRequirement, assigned []string, from, to time.Time) map[string]telemetryCoverageInterval {
	start, end := from.UnixMilli(), to.UnixMilli()
	out := map[string]telemetryCoverageInterval{}
	sources := map[string]bool{}
	for source := range observed {
		sources[source] = true
	}
	for source := range requirements {
		sources[source] = true
	}
	for _, source := range assigned {
		if source != "" {
			sources[source] = true
		}
	}
	for source := range sources {
		interval := telemetryCoverageInterval{startMs: start, endMs: end}
		requirement := requirements[source]
		switch {
		case requirement.assignedAtMs > 0:
			interval.startMs = requirement.assignedAtMs
		case observed[source] != nil && len(observed[source].spans) > 0:
			interval.startMs = observed[source].spans[0][0]
		}
		if requirement.retiredAtMs > 0 {
			interval.endMs = requirement.retiredAtMs
		}
		out[source] = interval
	}
	return out
}

// telemetryRowSpan is an archived interval's span as coverage counts it
// (milliseconds): its sample window before its timestamp, trimmed to the run
// window and to after the previous interval, in timestamp order; ok is false
// for an interval that covers nothing.
type telemetryRowSpan struct {
	row        IntervalSummaryRecord
	start, end int64
	ok         bool
}

// telemetryRowSpans returns the rows' spans in timestamp order; coverage and
// re-evaluation share it, so both attribute a row to the same segment.
func telemetryRowSpans(rows []IntervalSummaryRecord, windowStart, windowEnd int64) []telemetryRowSpan {
	sorted := append([]IntervalSummaryRecord(nil), rows...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Timestamp.Before(sorted[j].Timestamp) })
	out := make([]telemetryRowSpan, len(sorted))
	counted := windowStart
	for i, row := range sorted {
		out[i].row = row
		rowEnd := row.Timestamp.UnixMilli()
		window := int64(row.SampleWindowSeconds * 1000)
		if window <= 0 && i > 0 {
			window = rowEnd - sorted[i-1].Timestamp.UnixMilli()
		}
		spanStart, spanEnd := max(rowEnd-window, counted, windowStart), min(rowEnd, windowEnd)
		if window <= 0 || spanEnd <= spanStart {
			continue
		}
		counted = spanEnd
		out[i].start, out[i].end, out[i].ok = spanStart, spanEnd, true
	}
	return out
}

// telemetryCoverageRecord classifies the run's window by its retained
// intervals: each interval covers its sample window before its timestamp
// (overlaps count once), and only the part every required source observed
// takes the interval's status.
func telemetryCoverageRecord(coverage TelemetryCoverage, allObserved *coverageSpans, rows []IntervalSummaryRecord, segments []TelemetryPolicySegment) *TelemetryCoverageRecord {
	record := &TelemetryCoverageRecord{Algorithm: telemetryCoverageAlgorithm, CoverageRule: telemetryCoverageRuleQualifiedSourceWindows, Coverage: coverage, RetainedIntervals: int64(len(rows)), Segments: segments}
	for _, span := range telemetryRowSpans(rows, coverage.WindowStart.UnixMilli(), coverage.WindowEnd.UnixMilli()) {
		row := span.row
		status := ""
		if row.TelemetryStatus != nil {
			switch row.TelemetryStatus.Status {
			case "healthy", "degraded", "breached":
				status = row.TelemetryStatus.Status
				record.ClassifiedIntervals++
			}
		}
		if !span.ok {
			continue
		}
		spanStart, spanEnd := span.start, span.end
		known := allObserved.within(spanStart, spanEnd)
		var segment *TelemetryPolicySegment
		if index := telemetryRowSegmentIndex(record.Segments, row.TelemetryStatus, spanStart, spanEnd); index >= 0 {
			segment = &record.Segments[index]
		}
		switch status {
		case "healthy":
			record.HealthyMs += known
			if segment != nil {
				segment.HealthyMs += known
			}
		case "degraded":
			record.DegradedMs += known
			if segment != nil {
				segment.DegradedMs += known
			}
		case "breached":
			record.BreachedMs += known
			if segment != nil {
				segment.BreachedMs += known
			}
		}
	}
	record.ClassifiedMs = record.HealthyMs + record.DegradedMs + record.BreachedMs
	record.UnknownMs = max(coverage.WindowMs-record.ClassifiedMs, 0)
	if record.ClassifiedMs > 0 {
		fraction := float64(record.BreachedMs) / float64(record.ClassifiedMs)
		record.BreachedKnownFraction = &fraction
	}
	return record
}

// telemetryRowPolicyUnclassified reports an interval this controller left
// without a segment on purpose (a change fell inside it, or the run's
// segments could not be read): never attributed to one by its span, which
// is how an interval archived before segments existed is attributed.
func telemetryRowPolicyUnclassified(status *TelemetryRunHealth) bool {
	if status == nil {
		return false
	}
	for _, reason := range status.Unscored {
		if reason == telemetryPolicyBoundaryUnscored || reason == telemetryPolicySegmentsUnavailableUnscored {
			return true
		}
	}
	return false
}

// telemetryRowSegmentIndex is the segment that classified an archived
// interval: the one it names; for an interval archived without one, the
// run's single segment, or (by a controller before segments were recorded,
// not one left unclassified on purpose) the segment whose span holds the
// interval's whole span (milliseconds); -1 for none.
func telemetryRowSegmentIndex(segments []TelemetryPolicySegment, status *TelemetryRunHealth, spanStart, spanEnd int64) int {
	if status == nil {
		return -1
	}
	if status.PolicySegment == 0 {
		if len(segments) == 1 {
			return 0
		}
		if telemetryRowPolicyUnclassified(status) {
			return -1
		}
		for index, segment := range segments {
			if segment.Start.UnixMilli() <= spanStart && spanEnd <= segment.End.UnixMilli() {
				return index
			}
		}
		return -1
	}
	for index := range segments {
		if segments[index].Sequence == status.PolicySegment {
			return index
		}
	}
	return -1
}

// telemetryPolicySegments is the run's policy segments over its window: the
// admission receipt, then each change made while it ran, each until the next
// begins. A run without a valid admission receipt has one segment, legacy or
// naming no hash.
func telemetryPolicySegments(test *ManagedTest, from, to time.Time, invalid ...TelemetryPolicySegmentReceipt) []TelemetryPolicySegment {
	if receipts := telemetryPolicySegmentsWithInvalid(telemetryPolicySegmentsLocked(test), invalid); len(receipts) > 0 {
		out := make([]TelemetryPolicySegment, 0, len(receipts))
		for index, receipt := range receipts {
			start, end := from, to
			if index > 0 {
				start = receipt.EffectiveAt
			}
			if index+1 < len(receipts) {
				end = receipts[index+1].EffectiveAt
			}
			start, end = maxTime(start, from), minTime(end, to)
			if end.Before(start) {
				end = start
			}
			segment := TelemetryPolicySegment{Sequence: receipt.Sequence, Start: start.UTC(), End: end.UTC()}
			if receipt.Policy != nil {
				segment.PolicyHash = receipt.Policy.Hash
			} else {
				segment.Unavailable = "immutable-policy-invalid"
			}
			out = append(out, segment)
		}
		return out
	}
	segment := TelemetryPolicySegment{Sequence: 1, Start: from.UTC(), End: to.UTC()}
	switch {
	case test == nil || test.initialArchiveProvenance == nil || test.initialArchiveProvenance.TelemetryPolicy == nil:
		segment.Legacy = true
	default:
		segment.Unavailable = "immutable-policy-invalid"
	}
	return []TelemetryPolicySegment{segment}
}

// telemetryRunWindowLocked is the run's window: from its start to its end,
// or to now while it runs. The caller holds tr.mu.
func telemetryRunWindowLocked(test *ManagedTest, now time.Time) (time.Time, time.Time, bool) {
	if test == nil || test.StartedAt == nil {
		return time.Time{}, time.Time{}, false
	}
	end := now
	if test.EndedAt != nil {
		end = *test.EndedAt
	}
	return *test.StartedAt, end, end.After(*test.StartedAt)
}

// assignedTelemetrySourcesLocked lists the generators assigned to the run:
// its live clients and its durable shard placement, which outlives them. The
// caller holds tr.mu.
func assignedTelemetrySourcesLocked(test *ManagedTest) []string {
	out := make([]string, 0, len(test.remoteClientsByTarget)+len(test.GeneratorShards))
	for target := range test.remoteClientsByTarget {
		out = append(out, target)
	}
	return appendShardTelemetrySources(out, test.GeneratorShards)
}

// appendShardTelemetrySources adds the generators a shard placement names. A
// local run's runtime-resource placement names the controller's own process,
// which is no generator: that run stays one executed in this process.
func appendShardTelemetrySources(out []string, shards []GeneratorShardSummary) []string {
	for _, shard := range shards {
		if shard.Target != "" && shard.Target != localRuntimeResourceTarget {
			out = append(out, shard.Target)
		}
	}
	return out
}

// telemetryLiveCoverageLocked is a distributed run's coverage so far. The
// caller holds tr.mu.
func telemetryLiveCoverageLocked(test *ManagedTest, now time.Time) *TelemetryCoverage {
	from, to, ok := telemetryRunWindowLocked(test, now)
	if !ok || test.telemetrySources == nil {
		return nil
	}
	observed, requirements, truncated := test.telemetrySources.coverageSnapshot()
	assigned := assignedTelemetrySourcesLocked(test)
	coverage, _ := telemetryCoverageOver(observed, requiredTelemetrySources(observed, requirements, assigned, from, to), false, from, to)
	coverage.Truncated = coverage.Truncated || truncated
	// An adopting controller observed the run only from the adoption, as
	// the coverage recorded at finalize will say.
	coverage.ObserverLost = telemetryCoverageObserverLost(test.telemetrySources, assigned, len(test.continuityFailovers) > 0 || test.telemetrySources.wasAdopted())
	return &coverage
}

// coverageIntervalRow is the part of an archived interval summary coverage
// classification reads.
type coverageIntervalRow struct {
	Timestamp           time.Time `json:"timestamp"`
	SampleWindowSeconds float64   `json:"sample_window_seconds"`
	TelemetryStatus     *struct {
		Status        string   `json:"status"`
		PolicySegment int64    `json:"policy_segment"`
		Unscored      []string `json:"unscored"`
	} `json:"telemetry_status"`
}

// persistTelemetryCoverage records the run's wall-time telemetry account in
// the manifest after finalize, from its retained interval summaries. A run
// executed in this process counts its whole window as observed. A
// distributed run — known from its ledger, its clients or its durable shard
// placement, which survive finalize, restarts and adoption — counts only the
// time every required source was observed by this controller. Finalized
// without the run's ledger (after a restart, or by a controller that never
// executed the run) its whole window is unknown; after an adoption, the time
// before it. Either way observer_lost says observed time is a lower bound.
func (tr *TestRunner) persistTelemetryCoverage(ctx context.Context, test *ManagedTest, record *storage.RunArchiveRecord, manifest *RunArchiveManifest) error {
	if test == nil || manifest == nil || manifest.TelemetryCoverage != nil {
		return nil
	}
	// The stored segments are authoritative: finalization works on a snapshot
	// (the run's record, or a copy of the live run), which reads them here.
	// Coverage is written once, so a run whose segments cannot be read is not
	// recorded as classified by one policy: the error fails the finalization
	// step, which is retried.
	// Serialized with changes: a change admitted before the run ended
	// commits before this read, never after it.
	tr.telemetrySegmentMu.Lock()
	receipts, invalid, supported, err := tr.readTelemetryPolicySegments(test.ID)
	tr.telemetrySegmentMu.Unlock()
	if err != nil {
		return fmt.Errorf("%w: %v", errTelemetrySegmentsUnreadable, err)
	}
	if supported {
		tr.mu.Lock()
		for _, receipt := range receipts {
			installTelemetryPolicySegmentLocked(test, receipt)
		}
		tr.mu.Unlock()
	}
	tr.mu.RLock()
	from, to, ok := telemetryRunWindowLocked(test, time.Now())
	ledger := test.telemetrySources
	assigned := assignedTelemetrySourcesLocked(test)
	// A stored segment whose receipt cannot be decoded is recorded without
	// one (its time is classified by no receipt the archive can name), not
	// retried forever and not dropped.
	segments := telemetryPolicySegments(test, from, to, invalid...)
	if receipts := telemetryPolicySegmentsWithInvalid(telemetryPolicySegmentsLocked(test), invalid); len(receipts) > 1 {
		manifest.TelemetryPolicySegments = receipts
	}
	adopted := len(test.continuityFailovers) > 0 || ledger.wasAdopted()
	tr.mu.RUnlock()
	if !ok {
		return nil
	}
	assigned = appendShardTelemetrySources(assigned, manifest.Provenance.GeneratorShards)
	var rows []IntervalSummaryRecord
	if record != nil && (record.ManifestPath != "" || manifest.IntervalSummariesFile != "") {
		err = forEachArchiveRecordContext(ctx, record, func(manifest *RunArchiveManifest) string { return manifest.IntervalSummariesFile }, func(row *coverageIntervalRow) error {
			summary := IntervalSummaryRecord{Timestamp: row.Timestamp, SampleWindowSeconds: row.SampleWindowSeconds}
			if row.TelemetryStatus != nil {
				summary.TelemetryStatus = &TelemetryRunHealth{Status: row.TelemetryStatus.Status, PolicySegment: row.TelemetryStatus.PolicySegment, Unscored: row.TelemetryStatus.Unscored}
			}
			rows = append(rows, summary)
			return nil
		})
		if err != nil {
			return err
		}
	}
	manifest.TelemetryCoverage = telemetryRunCoverageRecord(ledger, assigned, from, to, rows, segments)
	manifest.TelemetryCoverage.Coverage.ObserverLost = telemetryCoverageObserverLost(ledger, assigned, adopted)
	return nil
}

// telemetryCoverageObserverLost says why a distributed run's observed time
// is only a lower bound: no ledger here at finalize (another controller
// observed the run, then the record was lost), or a controller adoption.
func telemetryCoverageObserverLost(ledger *telemetrySourceLedger, assigned []string, adopted bool) string {
	switch {
	case ledger == nil && len(assigned) > 0:
		return telemetryCoverageLedgerLost
	case adopted:
		return telemetryCoverageAdopted
	}
	return ""
}

// telemetryRunCoverageRecord is a finished run's coverage account. A run
// with a ledger or an assigned generator is distributed; any other run was
// executed in this process.
func telemetryRunCoverageRecord(ledger *telemetrySourceLedger, assigned []string, from, to time.Time, rows []IntervalSummaryRecord, segments []TelemetryPolicySegment) *TelemetryCoverageRecord {
	distributed := ledger != nil || len(assigned) > 0
	observed, requirements, truncated := ledger.coverageSnapshot()
	coverage, all := telemetryCoverageOver(observed, requiredTelemetrySources(observed, requirements, assigned, from, to), !distributed, from, to)
	coverage.Truncated = coverage.Truncated || truncated
	record := telemetryCoverageRecord(coverage, all, rows, segments)
	for _, c := range ledger.collectionsSnapshot() {
		switch c.Classification {
		case telemetryCollectionClassificationQualifiedSourceWindows:
			record.QualifiedCollections++
			var windowMs int64
			for _, w := range c.Windows {
				if d := w.End.Sub(w.Start).Milliseconds(); d > 0 {
					windowMs += d
				}
			}
			for i := range record.Segments {
				if (c.PolicySegment > 0 && record.Segments[i].Sequence == c.PolicySegment) ||
					(c.PolicySegment == 0 && c.PolicyHash != "" && record.Segments[i].PolicyHash == c.PolicyHash) {
					record.Segments[i].QualifiedCollections++
					record.Segments[i].SourceWindowMs += windowMs
					break
				}
			}
		case telemetryCollectionClassificationUnscoredBoundarySpanning:
			record.UnscoredBoundaryCollections++
		case telemetryCollectionClassificationUnqualifiedClock:
			record.UnqualifiedClockCollections++
		}
	}
	return record
}
