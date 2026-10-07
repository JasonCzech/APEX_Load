package api

import (
	"testing"
	"time"
)

// Observed spans merge into a sorted, disjoint union; overlapping and retried
// windows count once, and past the bound new disjoint spans are dropped
// (understated, never overstated).
func TestCoverageSpansUnionAndBound(t *testing.T) {
	c := &coverageSpans{}
	c.add(10, 20)
	c.add(15, 25)
	c.add(15, 25)
	c.add(40, 50)
	c.add(25, 30)
	if len(c.spans) != 2 || c.spans[0] != [2]int64{10, 30} || c.spans[1] != [2]int64{40, 50} {
		t.Fatalf("spans %v", c.spans)
	}
	if got := c.within(0, 100); got != 30 {
		t.Fatalf("covered %d, want 30", got)
	}
	if got := c.within(28, 45); got != 7 {
		t.Fatalf("covered in [28,45) %d, want 7", got)
	}
	bounded := &coverageSpans{}
	for i := int64(0); i < maxTelemetryCoverageSpans+5; i++ {
		bounded.add(i*10, i*10+5)
	}
	if len(bounded.spans) != maxTelemetryCoverageSpans || !bounded.truncated {
		t.Fatalf("bounded spans %d truncated %v", len(bounded.spans), bounded.truncated)
	}
	// A span that merges into an existing one is still recorded at the bound.
	bounded.add(0, 8)
	if bounded.spans[0] != [2]int64{0, 8} {
		t.Fatalf("merge at the bound: %v", bounded.spans[0])
	}
	both := intersectCoverage([]*coverageSpans{{spans: [][2]int64{{0, 50}}}, {spans: [][2]int64{{20, 30}, {40, 60}}}}, 0, 100)
	if both.within(0, 100) != 20 {
		t.Fatalf("intersection %v", both.spans)
	}
	if intersectCoverage([]*coverageSpans{nil}, 0, 100).within(0, 100) != 0 {
		t.Fatal("a source never observed covered time")
	}
}

// observeSecondly publishes one-second windows from start to end, skipping
// the seconds in skip.
func observeSecondly(ledger *telemetrySourceLedger, source string, start time.Time, from, to int, skip func(int) bool) {
	for second := from; second < to; second++ {
		if skip != nil && skip(second) {
			continue
		}
		ledger.mu.Lock()
		ledger.observeCoverage(source, 1000, start.Add(time.Duration(second+1)*time.Second))
		ledger.mu.Unlock()
	}
}

func healthyRows(start time.Time, seconds int) []IntervalSummaryRecord {
	rows := make([]IntervalSummaryRecord, 0, seconds)
	for second := 1; second <= seconds; second++ {
		rows = append(rows, IntervalSummaryRecord{Timestamp: start.Add(time.Duration(second) * time.Second), SampleWindowSeconds: 1, TelemetryStatus: &TelemetryRunHealth{Status: "healthy"}})
	}
	return rows
}

// A 10-minute run whose generator was silent for 60 seconds is 90% covered,
// and that minute is unknown even though every interval was classified.
func TestTelemetryCoverageCountsAGapAsUnknown(t *testing.T) {
	start := time.Unix(1_800_000_000, 0).UTC()
	ledger := newTelemetrySourceLedger()
	observeSecondly(ledger, "gen-a", start, 0, 600, func(second int) bool { return second >= 300 && second < 360 })
	observed := ledger.coverageSnapshot()
	coverage, all := telemetryCoverageOver(observed, requiredTelemetrySources(observed, []string{"gen-a"}), false, start, start.Add(10*time.Minute))
	if coverage.WindowMs != 600_000 || coverage.ObservedMs != 540_000 || coverage.UnknownMs != 60_000 {
		t.Fatalf("coverage %+v", coverage)
	}
	record := telemetryCoverageRecord(coverage, all, healthyRows(start, 600), nil)
	if record.HealthyMs != 540_000 || record.UnknownMs != 60_000 || record.ClassifiedMs != 540_000 || record.RetainedIntervals != 600 || record.ClassifiedIntervals != 600 {
		t.Fatalf("record %+v", record)
	}
	if record.BreachedKnownFraction == nil || *record.BreachedKnownFraction != 0 {
		t.Fatalf("breached fraction %v", record.BreachedKnownFraction)
	}
}

// After an HA adoption this controller observed only the later half: the
// earlier half, and any assigned generator that never published, are unknown.
func TestTelemetryCoverageBeforeAdoptionAndForSilentSourcesIsUnknown(t *testing.T) {
	start := time.Unix(1_800_000_000, 0).UTC()
	ledger := newTelemetrySourceLedger()
	observeSecondly(ledger, "gen-a", start, 300, 600, nil)
	observeSecondly(ledger, "gen-b", start, 0, 600, nil)
	observed := ledger.coverageSnapshot()
	end := start.Add(10 * time.Minute)
	coverage, all := telemetryCoverageOver(observed, requiredTelemetrySources(observed, nil), false, start, end)
	if coverage.ObservedMs != 300_000 || len(coverage.Sources) != 2 || coverage.Sources[1].ObservedMs != 600_000 {
		t.Fatalf("coverage after adoption %+v", coverage)
	}
	rows := healthyRows(start, 600)
	rows[450].TelemetryStatus = &TelemetryRunHealth{Status: "breached"}
	record := telemetryCoverageRecord(coverage, all, rows, nil)
	if record.HealthyMs != 299_000 || record.BreachedMs != 1000 || record.UnknownMs != 300_000 {
		t.Fatalf("record after adoption %+v", record)
	}
	if fraction := *record.BreachedKnownFraction; fraction != 1.0/300 {
		t.Fatalf("breached fraction %v", fraction)
	}
	silent, _ := telemetryCoverageOver(observed, requiredTelemetrySources(observed, []string{"gen-c"}), false, start, end)
	if silent.ObservedMs != 0 || silent.UnknownMs != 600_000 {
		t.Fatalf("a silent assigned generator left time observed: %+v", silent)
	}
	none, _ := telemetryCoverageOver(nil, nil, false, start, end)
	if none.ObservedMs != 0 {
		t.Fatalf("a distributed run without sources was observed: %+v", none)
	}
}

// A run executed in this process is observed throughout; its time is
// classified by its retained intervals, and time no interval covers, or an
// unclassified interval, is unknown.
func TestTelemetryCoverageOfARunInThisProcess(t *testing.T) {
	start := time.Unix(1_800_000_000, 0).UTC()
	coverage, all := telemetryCoverageOver(nil, nil, true, start, start.Add(100*time.Second))
	rows := healthyRows(start, 80)
	rows[10].TelemetryStatus = nil
	rows[20].TelemetryStatus = &TelemetryRunHealth{Status: "degraded"}
	record := telemetryCoverageRecord(coverage, all, rows, []TelemetryPolicySegment{{Start: start, End: start.Add(100 * time.Second), Legacy: true}})
	if record.HealthyMs != 78_000 || record.DegradedMs != 1000 || record.UnknownMs != 21_000 || record.ClassifiedIntervals != 79 || len(record.Segments) != 1 {
		t.Fatalf("in-process record %+v", record)
	}
	// Overlapping sample windows count once.
	overlapping := []IntervalSummaryRecord{
		{Timestamp: start.Add(10 * time.Second), SampleWindowSeconds: 10, TelemetryStatus: &TelemetryRunHealth{Status: "healthy"}},
		{Timestamp: start.Add(12 * time.Second), SampleWindowSeconds: 10, TelemetryStatus: &TelemetryRunHealth{Status: "breached"}},
	}
	if record := telemetryCoverageRecord(coverage, all, overlapping, nil); record.HealthyMs != 10_000 || record.BreachedMs != 2000 {
		t.Fatalf("overlapping windows %+v", record)
	}
}
