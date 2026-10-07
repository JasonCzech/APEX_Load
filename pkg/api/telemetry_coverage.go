package api

import (
	"context"
	"sort"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// Wall-time telemetry coverage (TEL-1): the time each source was observed,
// and the run's healthy, degraded, breached and unknown time. Time no
// observation covers is unknown, never healthy.

const (
	// maxTelemetryCoverageSpans bounds one source's disjoint observed spans.
	// Past it, new disjoint spans are dropped, so coverage is understated
	// (a lower bound), never overstated.
	maxTelemetryCoverageSpans  = 1024
	telemetryCoverageAlgorithm = "telemetry_coverage.v1"
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
			return
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

// intersectCoverage is the time inside [from, to) that every set covers.
func intersectCoverage(sets []*coverageSpans, from, to int64) *coverageSpans {
	out := &coverageSpans{spans: [][2]int64{{from, to}}}
	if to <= from {
		return &coverageSpans{}
	}
	for _, set := range sets {
		next := &coverageSpans{truncated: out.truncated}
		if set == nil {
			return next
		}
		next.truncated = next.truncated || set.truncated
		for _, a := range out.spans {
			for _, b := range set.spans {
				if start, end := max(a[0], b[0]), min(a[1], b[1]); start < end {
					next.spans = append(next.spans, [2]int64{start, end})
				}
			}
		}
		out = next
	}
	return out
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
// Retried and overlapping publications count once.
func (l *telemetrySourceLedger) observeCoverage(source string, windowMs int64, at time.Time) {
	if windowMs <= 0 {
		return
	}
	if l.coverage == nil {
		l.coverage = map[string]*coverageSpans{}
	}
	spans := l.coverage[source]
	if spans == nil {
		spans = &coverageSpans{}
		l.coverage[source] = spans
	}
	end := at.UnixMilli()
	spans.add(end-windowMs, end)
}

// coverageSnapshot copies each source's observed spans.
func (l *telemetrySourceLedger) coverageSnapshot() map[string]*coverageSpans {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make(map[string]*coverageSpans, len(l.coverage))
	for source, spans := range l.coverage {
		out[source] = spans.clone()
	}
	return out
}

// telemetryCoverageOver measures coverage in [from, to) for the required
// sources. It returns the coverage and the time every required source was
// observed. A run executed in this process (inProcess) is observed for its
// whole window; a distributed run without a known source for none of it.
func telemetryCoverageOver(observed map[string]*coverageSpans, required []string, inProcess bool, from, to time.Time) (TelemetryCoverage, *coverageSpans) {
	start, end := from.UnixMilli(), to.UnixMilli()
	coverage := TelemetryCoverage{WindowStart: from.UTC(), WindowEnd: to.UTC(), Sources: []TelemetrySourceCoverage{}}
	if end <= start {
		return coverage, &coverageSpans{}
	}
	coverage.WindowMs = end - start
	all := &coverageSpans{spans: [][2]int64{{start, end}}}
	if !inProcess {
		all = &coverageSpans{}
	}
	if !inProcess && len(required) > 0 {
		sets := make([]*coverageSpans, 0, len(required))
		for _, source := range required {
			spans := observed[source]
			sets = append(sets, spans)
			entry := TelemetrySourceCoverage{Source: source, ObservedMs: spans.within(start, end)}
			if spans != nil && spans.truncated {
				entry.Truncated, coverage.Truncated = true, true
			}
			coverage.Sources = append(coverage.Sources, entry)
		}
		all = intersectCoverage(sets, start, end)
	}
	coverage.ObservedMs = all.within(start, end)
	coverage.UnknownMs = coverage.WindowMs - coverage.ObservedMs
	return coverage, all
}

// requiredTelemetrySources is every source the run published from or was
// assigned, sorted.
func requiredTelemetrySources(observed map[string]*coverageSpans, assigned []string) []string {
	set := map[string]bool{}
	for source := range observed {
		set[source] = true
	}
	for _, source := range assigned {
		if source != "" {
			set[source] = true
		}
	}
	out := make([]string, 0, len(set))
	for source := range set {
		out = append(out, source)
	}
	sort.Strings(out)
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
// captured at admission (or the legacy settings) for the whole window.
func telemetryPolicySegments(test *ManagedTest, from, to time.Time) []TelemetryPolicySegment {
	segment := TelemetryPolicySegment{Start: from.UTC(), End: to.UTC()}
	if test != nil && test.initialArchiveProvenance != nil && test.initialArchiveProvenance.TelemetryPolicy != nil {
		segment.PolicyHash = telemetryBudgetPolicyHash(test.initialArchiveProvenance.TelemetryPolicy)
	} else {
		segment.Legacy = true
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

// assignedTelemetrySourcesLocked lists the generators assigned to the run.
// The caller holds tr.mu.
func assignedTelemetrySourcesLocked(test *ManagedTest) []string {
	out := make([]string, 0, len(test.remoteClientsByTarget))
	for target := range test.remoteClientsByTarget {
		out = append(out, target)
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
	observed := test.telemetrySources.coverageSnapshot()
	coverage, _ := telemetryCoverageOver(observed, requiredTelemetrySources(observed, assignedTelemetrySourcesLocked(test)), false, from, to)
	return &coverage
}

// persistTelemetryCoverage records the run's wall-time telemetry account in
// the manifest after finalize, from its retained interval summaries. A run
// executed in this process counts its whole window as observed; a
// distributed run only the time every required source was observed by this
// controller (time before an HA adoption is unknown here).
func (tr *TestRunner) persistTelemetryCoverage(ctx context.Context, test *ManagedTest, record *storage.RunArchiveRecord, manifest *RunArchiveManifest) error {
	if test == nil || manifest == nil || manifest.TelemetryCoverage != nil {
		return nil
	}
	tr.mu.RLock()
	from, to, ok := telemetryRunWindowLocked(test, time.Now())
	ledger, distributed := test.telemetrySources, test.telemetrySources != nil || len(test.remoteClientsByTarget) > 0
	assigned := assignedTelemetrySourcesLocked(test)
	segments := telemetryPolicySegments(test, from, to)
	tr.mu.RUnlock()
	if !ok {
		return nil
	}
	rows, err := loadArchiveIntervalSummariesForAction(record, "")
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	observed := ledger.coverageSnapshot()
	coverage, all := telemetryCoverageOver(observed, requiredTelemetrySources(observed, assigned), !distributed, from, to)
	manifest.TelemetryCoverage = telemetryCoverageRecord(coverage, all, rows, segments)
	return nil
}
