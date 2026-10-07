package api

import (
	"context"
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
	maxTelemetryCoverageSpans  = 1024
	telemetryCoverageAlgorithm = "telemetry_coverage.v2"
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

// coverageCursor is the last sequenced publication seen from one source, so
// a contiguous next publication bridges receipt-time jitter.
type coverageCursor struct {
	key      telemetrySourceKey
	sequence uint64
	endMs    int64
}

// TelemetrySourceCoverage is the time one source was observed in the run's
// window.
type TelemetrySourceCoverage struct {
	Source     string `json:"source"`
	ObservedMs int64  `json:"observed_ms"`
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
	// Truncated means a source's observed spans exceeded their bound, so
	// observed time is a lower bound.
	Truncated bool `json:"truncated,omitempty"`
}

// TelemetryPolicySegment is a span of the run classified under one telemetry
// budget policy. An intentional policy change during a run would open a new
// segment; the policy is fixed at admission today, so a run has one.
type TelemetryPolicySegment struct {
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
	PolicyHash string    `json:"policy_hash,omitempty"`
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
	Algorithm             string                   `json:"algorithm"`
	Coverage              TelemetryCoverage        `json:"coverage"`
	HealthyMs             int64                    `json:"healthy_ms"`
	DegradedMs            int64                    `json:"degraded_ms"`
	BreachedMs            int64                    `json:"breached_ms"`
	UnknownMs             int64                    `json:"unknown_ms"`
	ClassifiedMs          int64                    `json:"classified_ms"`
	BreachedKnownFraction *float64                 `json:"breached_known_fraction,omitempty"`
	RetainedIntervals     int64                    `json:"retained_intervals"`
	ClassifiedIntervals   int64                    `json:"classified_intervals"`
	Segments              []TelemetryPolicySegment `json:"segments"`
}

// observeCoverage records one publication's observation window, timed by
// the controller's receipt clock so it lines up with the run's window.
// Retried and overlapping publications count once. A publication whose
// sequence directly follows the source's previous one in the same epoch
// covers the receipt jitter since that one (up to its own window), so jitter
// does not fragment a continuous stream. A terminal publication retires the
// source. The caller holds l.mu.
func (l *telemetrySourceLedger) observeCoverage(source string, batch generatorrpc.MetricsBatch, at time.Time) {
	end := at.UnixMilli()
	key := telemetrySourceKey{source: source, generation: batch.ExecutionGeneration, epoch: batch.SourceEpoch}
	if l.cursors == nil {
		l.cursors = map[string]*coverageCursor{}
	}
	cursor := l.cursors[source]
	contiguous := cursor != nil && cursor.key == key && batch.BatchSequence > 0 && batch.BatchSequence == cursor.sequence+1
	if cursor == nil || cursor.key != key {
		cursor = &coverageCursor{key: key}
		l.cursors[source] = cursor
	}
	if batch.BatchSequence > 0 {
		cursor.sequence = batch.BatchSequence
	}
	if windowMs := batch.WindowMs; windowMs > 0 {
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
		cursor.endMs = end
	}
	if isTerminalRemoteLifecycle(batch.LifecycleState) {
		l.retireLocked(source, at)
	}
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
func (l *telemetrySourceLedger) coverageSnapshot() (map[string]*coverageSpans, map[string]telemetrySourceRequirement) {
	if l == nil {
		return nil, nil
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
	return observed, requirements
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
	coverage := TelemetryCoverage{WindowStart: from.UTC(), WindowEnd: to.UTC(), Sources: []TelemetrySourceCoverage{}}
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
			coverage.Sources = append(coverage.Sources, entry)
			span := [2]int64{max(interval.startMs, start), min(interval.endMs, end)}
			if span[1] <= span[0] {
				continue
			}
			requiredSpans = append(requiredSpans, span)
			var seen [][2]int64
			if spans != nil {
				seen = spans.spans
			}
			missing = append(missing, subtractSpans([][2]int64{span}, seen)...)
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

// telemetryCoverageRecord classifies the run's window by its retained
// intervals: each interval covers its sample window before its timestamp
// (overlaps count once), and only the part every required source observed
// takes the interval's status.
func telemetryCoverageRecord(coverage TelemetryCoverage, allObserved *coverageSpans, rows []IntervalSummaryRecord, segments []TelemetryPolicySegment) *TelemetryCoverageRecord {
	record := &TelemetryCoverageRecord{Algorithm: telemetryCoverageAlgorithm, Coverage: coverage, RetainedIntervals: int64(len(rows)), Segments: segments}
	start, end := coverage.WindowStart.UnixMilli(), coverage.WindowEnd.UnixMilli()
	sorted := append([]IntervalSummaryRecord(nil), rows...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Timestamp.Before(sorted[j].Timestamp) })
	counted := start
	for i, row := range sorted {
		status := ""
		if row.TelemetryStatus != nil {
			switch row.TelemetryStatus.Status {
			case "healthy", "degraded", "breached":
				status = row.TelemetryStatus.Status
				record.ClassifiedIntervals++
			}
		}
		rowEnd := row.Timestamp.UnixMilli()
		window := int64(row.SampleWindowSeconds * 1000)
		if window <= 0 && i > 0 {
			window = rowEnd - sorted[i-1].Timestamp.UnixMilli()
		}
		spanStart, spanEnd := max(rowEnd-window, counted, start), min(rowEnd, end)
		if window <= 0 || spanEnd <= spanStart {
			continue
		}
		counted = spanEnd
		known := allObserved.within(spanStart, spanEnd)
		switch status {
		case "healthy":
			record.HealthyMs += known
		case "degraded":
			record.DegradedMs += known
		case "breached":
			record.BreachedMs += known
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

// telemetryPolicySegments is the run's one policy segment: the budget policy
// captured at admission (or the legacy settings) for the whole window. A
// captured policy that fails validation names no hash.
func telemetryPolicySegments(test *ManagedTest, from, to time.Time) []TelemetryPolicySegment {
	segment := TelemetryPolicySegment{Start: from.UTC(), End: to.UTC()}
	switch {
	case test == nil || test.initialArchiveProvenance == nil || test.initialArchiveProvenance.TelemetryPolicy == nil:
		segment.Legacy = true
	case validTelemetryBudgetPolicy(test.initialArchiveProvenance.TelemetryPolicy):
		segment.PolicyHash = test.initialArchiveProvenance.TelemetryPolicy.Hash
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
	for _, shard := range test.GeneratorShards {
		out = append(out, shard.Target)
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
	observed, requirements := test.telemetrySources.coverageSnapshot()
	coverage, _ := telemetryCoverageOver(observed, requiredTelemetrySources(observed, requirements, assignedTelemetrySourcesLocked(test), from, to), false, from, to)
	return &coverage
}

// coverageIntervalRow is the part of an archived interval summary coverage
// classification reads.
type coverageIntervalRow struct {
	Timestamp           time.Time `json:"timestamp"`
	SampleWindowSeconds float64   `json:"sample_window_seconds"`
	TelemetryStatus     *struct {
		Status string `json:"status"`
	} `json:"telemetry_status"`
}

// persistTelemetryCoverage records the run's wall-time telemetry account in
// the manifest after finalize, from its retained interval summaries. A run
// executed in this process counts its whole window as observed. A
// distributed run — known from its ledger, its clients or its durable shard
// placement, which survive finalize, restarts and adoption — counts only the
// time every required source was observed by this controller; with no ledger
// here (a restart or adoption before finalize) its whole window is unknown.
func (tr *TestRunner) persistTelemetryCoverage(ctx context.Context, test *ManagedTest, record *storage.RunArchiveRecord, manifest *RunArchiveManifest) error {
	if test == nil || manifest == nil || manifest.TelemetryCoverage != nil {
		return nil
	}
	tr.mu.RLock()
	from, to, ok := telemetryRunWindowLocked(test, time.Now())
	ledger := test.telemetrySources
	assigned := assignedTelemetrySourcesLocked(test)
	segments := telemetryPolicySegments(test, from, to)
	tr.mu.RUnlock()
	if !ok {
		return nil
	}
	for _, shard := range manifest.Provenance.GeneratorShards {
		assigned = append(assigned, shard.Target)
	}
	var rows []IntervalSummaryRecord
	err := forEachArchiveRecordContext(ctx, record, func(manifest *RunArchiveManifest) string { return manifest.IntervalSummariesFile }, func(row *coverageIntervalRow) error {
		summary := IntervalSummaryRecord{Timestamp: row.Timestamp, SampleWindowSeconds: row.SampleWindowSeconds}
		if row.TelemetryStatus != nil {
			summary.TelemetryStatus = &TelemetryRunHealth{Status: row.TelemetryStatus.Status}
		}
		rows = append(rows, summary)
		return nil
	})
	if err != nil {
		return err
	}
	manifest.TelemetryCoverage = telemetryRunCoverageRecord(ledger, assigned, from, to, rows, segments)
	return nil
}

// telemetryRunCoverageRecord is a finished run's coverage account. A run
// with a ledger or an assigned generator is distributed; any other run was
// executed in this process.
func telemetryRunCoverageRecord(ledger *telemetrySourceLedger, assigned []string, from, to time.Time, rows []IntervalSummaryRecord, segments []TelemetryPolicySegment) *TelemetryCoverageRecord {
	distributed := ledger != nil || len(assigned) > 0
	observed, requirements := ledger.coverageSnapshot()
	coverage, all := telemetryCoverageOver(observed, requiredTelemetrySources(observed, requirements, assigned, from, to), !distributed, from, to)
	return telemetryCoverageRecord(coverage, all, rows, segments)
}
