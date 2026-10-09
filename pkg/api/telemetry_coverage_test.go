package api

import (
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
)

// Observed spans merge into a sorted, disjoint union; overlapping and retried
// windows count once, and past the bound the shortest span gives way to a
// longer one (understated, never overstated).
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
	// A longer disjoint span replaces the shortest; a shorter one is dropped.
	bounded.add(100_000, 100_100)
	if last := bounded.spans[len(bounded.spans)-1]; last != [2]int64{100_000, 100_100} || len(bounded.spans) != maxTelemetryCoverageSpans {
		t.Fatalf("longer span at the bound: %v (%d spans)", last, len(bounded.spans))
	}
	bounded.add(200_000, 200_001)
	if last := bounded.spans[len(bounded.spans)-1]; last != [2]int64{100_000, 100_100} {
		t.Fatalf("shorter span admitted at the bound: %v", last)
	}
	if got := subtractSpans([][2]int64{{0, 100}}, [][2]int64{{10, 20}, {30, 40}, {90, 120}}); len(got) != 3 || got[0] != [2]int64{0, 10} || got[1] != [2]int64{20, 30} || got[2] != [2]int64{40, 90} {
		t.Fatalf("subtract %v", got)
	}
	if got := unionSpans([][2]int64{{30, 40}, {0, 10}, {5, 20}, {40, 41}}); len(got) != 2 || got[0] != [2]int64{0, 20} || got[1] != [2]int64{30, 41} {
		t.Fatalf("union %v", got)
	}
}

// coverageOf measures a ledger's coverage over [from, to) the way the live
// view does.
func coverageOf(ledger *telemetrySourceLedger, assigned []string, from, to time.Time) (TelemetryCoverage, *coverageSpans) {
	observed, requirements := ledger.coverageSnapshot()
	return telemetryCoverageOver(observed, requiredTelemetrySources(observed, requirements, assigned, from, to), false, from, to)
}

// observeSequenced publishes contiguous one-second windows received with
// jitter: receipt i lands i*jitter late, so no two windows touch.
func observeSequenced(ledger *telemetrySourceLedger, source string, start time.Time, count int, jitter time.Duration, skip func(int) bool) {
	for i := 0; i < count; i++ {
		if skip != nil && skip(i) {
			continue
		}
		ledger.observe(source, generatorrpc.MetricsBatch{SourceEpoch: "e1", BatchSequence: uint64(i + 1), WindowMs: 1000}, start.Add(time.Duration(i+1)*time.Second+time.Duration(i)*jitter))
	}
}

// Receipt jitter between contiguous publications does not fragment coverage
// (a long run stays observed instead of filling the span bound); a missing
// publication still leaves its gap unknown.
func TestTelemetryCoverageBridgesJitterButNotLoss(t *testing.T) {
	start := time.Unix(1_800_000_000, 0).UTC()
	ledger := newTelemetrySourceLedger()
	observeSequenced(ledger, "gen-a", start, 7200, 3*time.Millisecond, nil)
	observed, _ := ledger.coverageSnapshot()
	if spans := observed["gen-a"]; len(spans.spans) != 1 || spans.truncated {
		t.Fatalf("jittered two-hour stream fragmented into %d spans (truncated %v)", len(spans.spans), spans.truncated)
	}
	lossy := newTelemetrySourceLedger()
	observeSequenced(lossy, "gen-a", start, 100, 3*time.Millisecond, func(i int) bool { return i == 50 })
	observed, _ = lossy.coverageSnapshot()
	if spans := observed["gen-a"]; len(spans.spans) != 2 {
		t.Fatalf("a lost publication was bridged: %v", spans.spans)
	}
}

// An evacuated source is required only until it is retired and its
// replacement only from its assignment: planned topology changes leave only
// the hand-off unknown.
func TestTelemetryCoverageFollowsEvacuation(t *testing.T) {
	start := time.Unix(1_800_000_000, 0).UTC()
	end := start.Add(10 * time.Minute)
	ledger := newTelemetrySourceLedger()
	ledger.assign("gen-a", start)
	ledger.assign("gen-b", start)
	observeSecondly(ledger, "gen-a", start, 0, 300, nil)
	observeSecondly(ledger, "gen-b", start, 0, 600, nil)
	ledger.retire("gen-a", start.Add(300*time.Second))
	ledger.assign("gen-c", start.Add(300*time.Second))
	observeSecondly(ledger, "gen-c", start, 302, 600, nil)
	coverage, _ := coverageOf(ledger, []string{"gen-b", "gen-c"}, start, end)
	if coverage.ObservedMs != 598_000 || coverage.UnknownMs != 2000 {
		t.Fatalf("evacuation coverage %+v", coverage)
	}
}

// After an adoption this controller requires its generators only from the
// adoption, and one that completed during the gap never; a run whose
// generators all finished during the gap is unknown throughout.
func TestTelemetryCoverageAfterAdoption(t *testing.T) {
	start := time.Unix(1_800_000_000, 0).UTC()
	end := start.Add(10 * time.Minute)
	adoptedAt := start.Add(300 * time.Second)
	ledger := newTelemetrySourceLedger()
	ledger.assign("gen-a", adoptedAt)
	ledger.assign("gen-b", adoptedAt)
	ledger.retire("gen-b", adoptedAt)
	observeSecondly(ledger, "gen-a", start, 300, 600, nil)
	coverage, _ := coverageOf(ledger, []string{"gen-a", "gen-b"}, start, end)
	if coverage.ObservedMs != 300_000 || coverage.UnknownMs != 300_000 {
		t.Fatalf("adopted coverage %+v", coverage)
	}
	outcome := newTelemetrySourceLedger()
	outcome.assign("gen-a", end)
	outcome.retire("gen-a", end)
	if coverage, _ := coverageOf(outcome, []string{"gen-a"}, start, end); coverage.ObservedMs != 0 {
		t.Fatalf("an outcome-only adoption observed time: %+v", coverage)
	}
}

// At finalize a distributed run is recognised by its durable shard
// placement even when its clients are closed and no ledger survived (a
// restart or adoption before finalize): its window is unknown, never
// observed throughout.
func TestTelemetryRunCoverageWithoutALedger(t *testing.T) {
	start := time.Unix(1_800_000_000, 0).UTC()
	end := start.Add(100 * time.Second)
	rows := healthyRows(start, 100)
	if record := telemetryRunCoverageRecord(nil, []string{"gen-a"}, start, end, rows, nil); record.HealthyMs != 0 || record.UnknownMs != 100_000 {
		t.Fatalf("distributed run without a ledger: %+v", record)
	}
	if record := telemetryRunCoverageRecord(nil, nil, start, end, rows, nil); record.HealthyMs != 100_000 {
		t.Fatalf("run in this process: %+v", record)
	}
	// A silent assigned generator known only from the shards keeps the
	// window unknown even when another published throughout.
	ledger := newTelemetrySourceLedger()
	observeSecondly(ledger, "gen-a", start, 0, 100, nil)
	if record := telemetryRunCoverageRecord(ledger, []string{"gen-a", "gen-b"}, start, end, rows, nil); record.HealthyMs != 0 {
		t.Fatalf("silent shard generator: %+v", record)
	}
}

// Each source's unknown time is measured over the time it was required: a
// generator evacuated halfway and its replacement are each required for half
// the window, and neither is unknown for the other half.
func TestTelemetrySourceCoverageIsMeasuredOverItsRequirement(t *testing.T) {
	start := time.Unix(1_800_000_000, 0).UTC()
	end := start.Add(100 * time.Second)
	half := start.Add(50 * time.Second)
	ledger := newTelemetrySourceLedger()
	ledger.assign("gen-a", start)
	observeSecondly(ledger, "gen-a", start, 0, 50, nil)
	ledger.retire("gen-a", half)
	ledger.assign("gen-b", half)
	observeSecondly(ledger, "gen-b", start, 50, 90, nil)
	record := telemetryRunCoverageRecord(ledger, []string{"gen-a", "gen-b"}, start, end, healthyRows(start, 100), nil)
	want := map[string][2]int64{"gen-a": {50_000, 0}, "gen-b": {50_000, 10_000}}
	if len(record.Coverage.Sources) != 2 || !record.Coverage.SourceRequirements {
		t.Fatalf("coverage = %+v", record.Coverage)
	}
	for _, source := range record.Coverage.Sources {
		if got := [2]int64{source.RequiredMs, source.UnknownMs}; got != want[source.Source] {
			t.Fatalf("%s required/unknown = %v, want %v", source.Source, got, want[source.Source])
		}
	}
}

// Live coverage on a controller that adopted the run says observed time is a
// lower bound, as the coverage recorded at finalize will.
func TestTelemetryLiveCoverageAfterAdoptionIsALowerBound(t *testing.T) {
	start := time.Unix(1_800_000_000, 0).UTC()
	ledger := newTelemetrySourceLedger()
	ledger.assign("gen-a", start.Add(30*time.Second))
	observeSecondly(ledger, "gen-a", start, 30, 60, nil)
	test := &ManagedTest{ID: "adopted-run", Status: "RUNNING", StartedAt: &start, telemetrySources: ledger}
	if coverage := telemetryLiveCoverageLocked(test, start.Add(60*time.Second)); coverage == nil || coverage.ObserverLost != "" || !coverage.SourceRequirements {
		t.Fatalf("an unadopted run's live coverage = %+v", coverage)
	}
	ledger.markAdopted()
	if coverage := telemetryLiveCoverageLocked(test, start.Add(60*time.Second)); coverage == nil || coverage.ObserverLost != telemetryCoverageAdopted {
		t.Fatalf("an adopted run's live coverage = %+v", coverage)
	}
}

// A captured policy that fails validation names no hash.
func TestTelemetryPolicySegmentRejectsAnInvalidPolicy(t *testing.T) {
	start := time.Unix(1_800_000_000, 0).UTC()
	test := &ManagedTest{initialArchiveProvenance: &RunArchiveProvenance{TelemetryPolicy: &TelemetryBudgetPolicy{Hash: "tampered"}}}
	if segment := telemetryPolicySegments(test, start, start.Add(time.Second))[0]; segment.PolicyHash != "" || segment.Unavailable != "immutable-policy-invalid" || segment.Legacy {
		t.Fatalf("invalid policy segment %+v", segment)
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
		ledger.observeCoverage(source, generatorrpc.MetricsBatch{WindowMs: 1000}, start.Add(time.Duration(second+1)*time.Second))
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
	coverage, all := coverageOf(ledger, []string{"gen-a"}, start, start.Add(10*time.Minute))
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
	end := start.Add(10 * time.Minute)
	// gen-a was assigned at the start, so its silence before this
	// controller observed it is unknown.
	ledger.assign("gen-a", start)
	coverage, all := coverageOf(ledger, nil, start, end)
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
	silent, _ := coverageOf(ledger, []string{"gen-c"}, start, end)
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

// A generator whose shard finished early (its terminal publication arrived)
// is no longer required, so the rest of the run is observed by the
// generators still running.
func TestTelemetryCoverageRetiresAFinishedGenerator(t *testing.T) {
	start := time.Unix(1_800_000_000, 0).UTC()
	end := start.Add(10 * time.Minute)
	ledger := newTelemetrySourceLedger()
	ledger.assign("gen-a", start)
	ledger.assign("gen-b", start)
	observeSecondly(ledger, "gen-a", start, 0, 300, nil)
	ledger.observe("gen-a", generatorrpc.MetricsBatch{LifecycleState: "COMPLETE"}, start.Add(300*time.Second))
	observeSecondly(ledger, "gen-b", start, 0, 600, nil)
	if coverage, _ := coverageOf(ledger, []string{"gen-a", "gen-b"}, start, end); coverage.ObservedMs != 600_000 {
		t.Fatalf("finished generator still required: %+v", coverage)
	}
}
