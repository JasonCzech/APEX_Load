package api

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

const iterationTestLifetime = uint64(8123456789012345678)

func iterationIndexFromEvents(events []TransactionEvent) *RunArchiveIterationIndex {
	acc := newArchiveIterationIndexAccumulator()
	for ordinal := range events {
		acc.merge(&events[ordinal], archiveEventPopulation(events[ordinal]), int64(ordinal))
	}
	return acc.finalize()
}

func iterationRefs(index *RunArchiveIterationIndex, reason string) []RunArchiveIterationRef {
	var refs []RunArchiveIterationRef
	for _, ref := range index.Representatives {
		if ref.Reason == reason {
			refs = append(refs, ref)
		}
	}
	return refs
}

func TestIterationIndexSequentialIdentities(t *testing.T) {
	failed := taTransaction(1, iterationTestLifetime, 2, 200, 300, "Checkout")
	failed.Success = false
	events := []TransactionEvent{
		taRequest(1, iterationTestLifetime, 1, 0, 30, "A", 1),      // 0
		taTransaction(1, iterationTestLifetime, 1, 0, 100, "Home"), // 1
		taRequest(2, 42, 1, 0, 10, "X", 1),                         // 2: another VUser
		taRequest(1, iterationTestLifetime, 2, 200, 50, "B", 1),    // 3
		failed, // 4
	}
	index := iterationIndexFromEvents(events)
	if index == nil || index.Schema != iterationIndexSchema {
		t.Fatalf("index = %+v", index)
	}
	if index.IterationsTotal != 3 || index.Identities != 2 || index.IdentityQuality.Lifetime != 3 || index.IdentityQuality.Legacy != 0 {
		t.Fatalf("counts = %+v", index)
	}
	if index.Span.Samples != 3 || index.Span.MaxMs != 300 || index.Span.P50Ms != 100 || index.Span.PercentileQuality != "" {
		t.Fatalf("span = %+v", index.Span)
	}
	slowest := iterationRefs(index, "slowest")
	if len(slowest) == 0 {
		t.Fatalf("no slowest: %+v", index.Representatives)
	}
	top := slowest[0]
	if top.VUserID != 1 || top.VUserLifetime != iterationTestLifetime || top.Iteration != 2 || top.FirstOrdinal != 3 || top.LastOrdinal != 4 ||
		top.SpanMs != 500-200 || top.TransactionRows != 1 || top.RequestRows != 1 || top.FailedRows != 1 || top.Group != "g1" || top.IdentityQuality != "lifetime" {
		t.Fatalf("slowest = %+v", top)
	}
	if !top.StartedAt.Equal(taAt(200)) {
		t.Fatalf("started_at = %v", top.StartedAt)
	}
	median := iterationRefs(index, "p50")
	if len(median) != 1 || median[0].Iteration != 1 || median[0].VUserID != 1 || median[0].FirstOrdinal != 0 || median[0].LastOrdinal != 1 || median[0].SpanMs != 100 || median[0].TargetMs == nil || *median[0].TargetMs != 100 {
		t.Fatalf("p50 = %+v", median)
	}
	// The slowest iteration failed: it is listed once, as the slowest.
	if failedRefs := iterationRefs(index, "failed"); len(failedRefs) != 0 {
		t.Fatalf("failed duplicates the slowest: %+v", failedRefs)
	}
	// The lifetime nonce is a string on the wire.
	raw, err := json.Marshal(top)
	if err != nil || !strings.Contains(string(raw), `"vuser_lifetime":"8123456789012345678"`) {
		t.Fatalf("json = %s (%v)", raw, err)
	}
}

func TestIterationIndexLegacyIdentitiesRestartOnChange(t *testing.T) {
	events := []TransactionEvent{
		taRequest(5, 0, 1, 0, 10, "A", 1),
		taRequest(5, 0, 2, 20, 10, "A", 1),
		// A reused VUser ID starting over: a new iteration, not a late row.
		taRequest(5, 0, 1, 40, 10, "A", 1),
	}
	index := iterationIndexFromEvents(events)
	if index.IterationsTotal != 3 || index.IdentityQuality.Legacy != 3 || index.LateRows != 0 || index.Identities != 1 {
		t.Fatalf("legacy index = %+v", index)
	}
	for _, ref := range index.Representatives {
		if ref.IdentityQuality != "legacy" || ref.VUserLifetime != 0 {
			t.Fatalf("legacy ref = %+v", ref)
		}
	}
}

func TestIterationIndexLateRowsAndRowsOutsideIterations(t *testing.T) {
	initRow := taRequest(1, iterationTestLifetime, 1, 0, 5, "init", 1)
	initRow.Phase = "init"
	events := []TransactionEvent{
		initRow,
		taRequest(1, iterationTestLifetime, 0, 0, 5, "zero", 1),
		taRequest(0, 0, 1, 0, 5, "anonymous", 1),
		taRequest(1, iterationTestLifetime, 2, 100, 10, "B", 1),
		// Written after the lifetime moved on to iteration 2.
		taRequest(1, iterationTestLifetime, 1, 0, 10, "late", 1),
	}
	index := iterationIndexFromEvents(events)
	if index.IterationsTotal != 1 || index.LateRows != 1 || index.RowsOutsideIterations != 2 || index.IdentityQuality.NoneRows != 1 {
		t.Fatalf("index = %+v", index)
	}
	if index.Representatives[0].FirstOrdinal != 3 || index.Representatives[0].LastOrdinal != 3 {
		t.Fatalf("refs = %+v", index.Representatives)
	}
	if iterationIndexFromEvents([]TransactionEvent{initRow}) != nil {
		t.Fatal("an archive without iteration rows has an index")
	}
}

func TestIterationIndexOrdinalBoundsInterleaved(t *testing.T) {
	events := []TransactionEvent{
		taRequest(1, iterationTestLifetime, 1, 0, 10, "A", 1),  // 0
		taRequest(2, 77, 1, 0, 10, "X", 1),                     // 1
		taRequest(1, iterationTestLifetime, 1, 10, 10, "B", 2), // 2
		taRequest(2, 77, 1, 10, 10, "Y", 2),                    // 3
		taTransaction(1, iterationTestLifetime, 1, 0, 25, "T"), // 4
		taRequest(2, 77, 2, 30, 10, "X", 1),                    // 5
	}
	index := iterationIndexFromEvents(events)
	bounds := map[[2]int64][2]int64{}
	for _, ref := range index.Representatives {
		bounds[[2]int64{int64(ref.VUserID), ref.Iteration}] = [2]int64{ref.FirstOrdinal, ref.LastOrdinal}
	}
	want := map[[2]int64][2]int64{{1, 1}: {0, 4}, {2, 1}: {1, 3}, {2, 2}: {5, 5}}
	if !reflect.DeepEqual(bounds, want) {
		t.Fatalf("bounds = %v, want %v (refs %+v)", bounds, want, index.Representatives)
	}
}

func TestIterationIndexFailedAndSlowestAreBoundedAndOrdered(t *testing.T) {
	setTimeAnatomyCap(t, &iterationIndexTopN, 2)
	var events []TransactionEvent
	for vu := 1; vu <= 4; vu++ {
		row := taRequest(vu, uint64(1000+vu), 1, 0, float64(10*vu), "A", 1)
		row.Success = vu%2 == 0
		events = append(events, row)
	}
	index := iterationIndexFromEvents(events)
	var slowest, failed []int
	for _, ref := range iterationRefs(index, "slowest") {
		slowest = append(slowest, ref.VUserID)
	}
	for _, ref := range iterationRefs(index, "failed") {
		failed = append(failed, ref.VUserID)
	}
	// Spans 10..40: p50 (25) picks a passing iteration nearest it (VUser 2,
	// then the lower ordinal), p95 (38.5) VUser 4; the slowest are 4 and 3,
	// and 4 is already listed. Failed are the first two by ordinal (1, 3),
	// 3 already listed.
	if !reflect.DeepEqual(slowest, []int{3}) || !reflect.DeepEqual(failed, []int{1}) {
		t.Fatalf("slowest %v failed %v (refs %+v)", slowest, failed, index.Representatives)
	}
	if p50 := iterationRefs(index, "p50"); len(p50) != 1 || p50[0].VUserID != 2 {
		t.Fatalf("p50 = %+v", p50)
	}
	if p95 := iterationRefs(index, "p95"); len(p95) != 1 || p95[0].VUserID != 4 {
		t.Fatalf("p95 = %+v", p95)
	}
}

func TestIterationIndexCapsRecordTruncation(t *testing.T) {
	setTimeAnatomyCap(t, &maxIterationIndexIdentities, 2)
	setTimeAnatomyCap(t, &iterationIndexSampleK, 1)
	var events []TransactionEvent
	for vu := 1; vu <= 5; vu++ {
		events = append(events, taRequest(vu, uint64(2000+vu), 1, float64(vu), 10, "A", 1))
	}
	index := iterationIndexFromEvents(events)
	// Evictions without a resume split nothing: only the sample truncates.
	if index.IterationsTotal != 5 || index.IdentitiesEvicted == 0 || !index.Truncated || index.PartialIterations != 0 ||
		!reflect.DeepEqual(index.Reasons, []string{iterationIndexReasonSample}) {
		t.Fatalf("index = %+v", index)
	}
	if p50 := iterationRefs(index, "p50"); len(p50) != 1 || !p50[0].Sampled {
		t.Fatalf("p50 = %+v", p50)
	}
}

func TestIterationIndexUntimedFailedIterationIsListed(t *testing.T) {
	failed := taRequest(3, 3003, 1, 0, 10, "A", 1)
	failed.Timestamp = time.Time{}
	failed.Success = false
	index := iterationIndexFromEvents([]TransactionEvent{failed})
	if index == nil || index.UntimedIterations != 1 || index.Span.Samples != 0 {
		t.Fatalf("index = %+v", index)
	}
	refs := iterationRefs(index, "failed")
	if len(refs) != 1 || refs[0].VUserID != 3 || refs[0].FailedRows != 1 || refs[0].SpanMs != 0 || !refs[0].StartedAt.IsZero() {
		t.Fatalf("failed refs = %+v", index.Representatives)
	}
}

func TestIterationIndexInterruptedRowsAreNotFailures(t *testing.T) {
	setTimeAnatomyCap(t, &iterationIndexTopN, 5)
	interrupted := taRequest(1, 1001, 1, 0, 50, "A", 1)
	interrupted.Success, interrupted.Interrupted = false, true
	index := iterationIndexFromEvents([]TransactionEvent{interrupted, taRequest(2, 1002, 1, 0, 10, "A", 1)})
	if refs := iterationRefs(index, "failed"); len(refs) != 0 {
		t.Fatalf("an interrupted iteration is listed as failed: %+v", refs)
	}
	for _, ref := range index.Representatives {
		if ref.FailedRows != 0 {
			t.Fatalf("interrupted row counted as failed: %+v", ref)
		}
	}
	// The interrupted iteration was cut short by the test's stop: it counts
	// as an iteration but stays out of the span distribution and the
	// p50/p95 picks (it is still the slowest).
	if index.IterationsTotal != 2 || index.Span.Samples != 1 || index.PartialIterations != 1 || index.Span.MaxMs != 10 {
		t.Fatalf("index = %+v", index)
	}
	for _, ref := range append(iterationRefs(index, "p50"), iterationRefs(index, "p95")...) {
		if ref.VUserID == 1 {
			t.Fatalf("a percentile pick is the interrupted iteration: %+v", index.Representatives)
		}
	}
	if refs := iterationRefs(index, "slowest"); len(refs) == 0 || refs[0].VUserID != 1 {
		t.Fatalf("slowest = %+v", index.Representatives)
	}
}

// A lifetime's late row of the iteration just before the open one counts in
// that iteration's span and rows, as its replay shows it; a row two or more
// iterations back is only counted in late_rows.
func TestIterationIndexLifetimeLateRowJoinsItsIteration(t *testing.T) {
	events := []TransactionEvent{
		taRequest(3, iterationTestLifetime, 7, 0, 100, "seven-a", 1),    // 0
		taRequest(3, iterationTestLifetime, 8, 200, 100, "eight-a", 1),  // 1: iteration 8 opens
		taRequest(3, iterationTestLifetime, 7, 150, 450, "seven-bg", 2), // 2: iteration 7's background request
		taRequest(3, iterationTestLifetime, 9, 700, 100, "nine-a", 1),   // 3: iteration 9 opens
		taRequest(3, iterationTestLifetime, 7, 160, 10, "seven-old", 3), // 4: two iterations back
	}
	index := iterationIndexFromEvents(events)
	if index.IterationsTotal != 3 || index.LateRows != 1 {
		t.Fatalf("index = %+v", index)
	}
	var seven *RunArchiveIterationRef
	for i := range index.Representatives {
		if index.Representatives[i].Iteration == 7 {
			seven = &index.Representatives[i]
		}
	}
	if seven == nil || seven.SpanMs != 600 || seven.RequestRows != 2 || seven.FirstOrdinal != 0 || seven.LastOrdinal != 2 {
		t.Fatalf("iteration 7 = %+v", index.Representatives)
	}
	if index.Span.MaxMs != 600 {
		t.Fatalf("span = %+v", index.Span)
	}
	record, _ := waterfallRecordForTest(t, events)
	q := archiveIterationQuery{group: "g1", vuserID: 3, hasVUserID: true, lifetime: iterationTestLifetime, iteration: 7,
		first: seven.FirstOrdinal, last: seven.LastOrdinal, hasHints: true}
	waterfall, err := buildArchiveIterationWaterfall(context.Background(), record, q, 100)
	if err != nil {
		t.Fatal(err)
	}
	// The replay also finds the older late row; the chip's span and the
	// replay heading agree on the request the index folded in.
	if waterfall.SpanMs != 600 || !slices.Contains(iterationRowNames(waterfall.Rows), "seven-bg") {
		t.Fatalf("replay span %v rows %v", waterfall.SpanMs, iterationRowNames(waterfall.Rows))
	}
}

func TestIterationIndexLegacyLateRowDoesNotFragmentTheIteration(t *testing.T) {
	events := []TransactionEvent{
		taRequest(5, 0, 4, 0, 10, "A", 1),  // 0: iteration 4
		taRequest(5, 0, 5, 20, 10, "A", 1), // 1: iteration 5 opens
		taRequest(5, 0, 4, 10, 10, "B", 2), // 2: a late row of iteration 4
		taRequest(5, 0, 5, 40, 10, "B", 2), // 3: iteration 5 continues
		taRequest(5, 0, 5, 60, 10, "C", 3), // 4
	}
	index := iterationIndexFromEvents(events)
	// The late row joins iteration 4 (the one just before the open one), as
	// the replay of iteration 4 includes it.
	if index.IterationsTotal != 2 || index.LateRows != 0 || index.IdentityQuality.Legacy != 2 {
		t.Fatalf("index = %+v", index)
	}
	var four, five []RunArchiveIterationRef
	for _, ref := range index.Representatives {
		switch ref.Iteration {
		case 4:
			four = append(four, ref)
		case 5:
			five = append(five, ref)
		}
	}
	if len(five) != 1 || five[0].FirstOrdinal != 1 || five[0].LastOrdinal != 4 || five[0].RequestRows != 3 {
		t.Fatalf("iteration 5 = %+v", index.Representatives)
	}
	if len(four) != 1 || four[0].FirstOrdinal != 0 || four[0].LastOrdinal != 2 || four[0].RequestRows != 2 || four[0].SpanMs != 20 {
		t.Fatalf("iteration 4 = %+v", index.Representatives)
	}
	// A lower row the identity does not continue past is a restart (a
	// reused ID), with or without more rows after it.
	restart := append(append([]TransactionEvent{}, events[:2]...), taRequest(5, 0, 1, 80, 10, "A", 1), taRequest(5, 0, 1, 90, 10, "B", 2))
	if got := iterationIndexFromEvents(restart); got.IterationsTotal != 3 || got.LateRows != 0 {
		t.Fatalf("restart index = %+v", got)
	}
}

func TestIterationIndexEvictionWithoutResumeIsNotASplit(t *testing.T) {
	setTimeAnatomyCap(t, &maxIterationIndexIdentities, 2)
	events := []TransactionEvent{
		taRequest(1, 1001, 7, 0, 10, "A", 1), // 0: VUser 1 iteration 7 (its last)
		taRequest(2, 1002, 1, 1, 10, "A", 1), // 1
		taRequest(3, 1003, 1, 2, 10, "A", 1), // 2: table full, VUser 1 evicted
		taRequest(4, 1004, 1, 3, 10, "A", 1), // 3
	}
	index := iterationIndexFromEvents(events)
	if index.IdentitiesEvicted == 0 || index.Truncated || len(index.Reasons) != 0 || index.PartialIterations != 0 || index.IterationsTotal != 4 {
		t.Fatalf("index = %+v", index)
	}
	for _, ref := range index.Representatives {
		if ref.Split {
			t.Fatalf("a finished evicted iteration is marked split: %+v", ref)
		}
	}
}

func TestIterationIndexMarksEvictionSplitIterations(t *testing.T) {
	setTimeAnatomyCap(t, &maxIterationIndexIdentities, 2)
	events := []TransactionEvent{
		taRequest(1, 1001, 7, 0, 10, "A", 1), // 0: VUser 1 iteration 7 opens
		taRequest(2, 1002, 1, 1, 10, "A", 1), // 1
		taRequest(3, 1003, 1, 2, 10, "A", 1), // 2: table full, VUser 1 evicted
		taRequest(1, 1001, 7, 3, 10, "B", 2), // 3: VUser 1 resumes iteration 7
		taRequest(4, 1004, 1, 4, 10, "A", 1), // 4
	}
	index := iterationIndexFromEvents(events)
	split := map[int64]bool{}
	for _, ref := range index.Representatives {
		if ref.VUserID == 1 {
			split[ref.FirstOrdinal] = ref.Split
		}
	}
	if !split[0] || !split[3] {
		t.Fatalf("both halves of the split iteration must be marked: %v (refs %+v)", split, index.Representatives)
	}
	// The resumed fragment is partial; VUser 4 was never evicted and stays
	// whole.
	for _, ref := range index.Representatives {
		if ref.VUserID == 4 && ref.Split {
			t.Fatalf("a never-evicted identity is marked split: %+v", ref)
		}
	}
	if !index.Truncated || !reflect.DeepEqual(index.Reasons, []string{iterationIndexReasonIdentityEvicted}) || index.PartialIterations < 1 {
		t.Fatalf("index = %+v", index)
	}
	// Neither fragment is a percentile pick.
	for _, ref := range append(iterationRefs(index, "p50"), iterationRefs(index, "p95")...) {
		if ref.VUserID == 1 {
			t.Fatalf("a split fragment is a percentile pick: %+v", index.Representatives)
		}
	}
	raw, err := json.Marshal(index.Representatives)
	if err != nil || !strings.Contains(string(raw), `"split":true`) {
		t.Fatalf("json = %s (%v)", raw, err)
	}
	// Without eviction nothing is split.
	for _, ref := range iterationIndexFromEvents(events[:2]).Representatives {
		if ref.Split {
			t.Fatalf("unevicted ref marked split: %+v", ref)
		}
	}
}

// A late row of an earlier iteration that arrives first after an eviction is
// late: it neither opens a phantom iteration nor hides the resume of the
// evicted one.
func TestIterationIndexLateRowAfterEvictionKeepsTheResume(t *testing.T) {
	setTimeAnatomyCap(t, &maxIterationIndexIdentities, 2)
	events := []TransactionEvent{
		taRequest(1, 1001, 7, 0, 10, "A", 1), // 0: VUser 1 iteration 7 opens
		taRequest(2, 1002, 1, 1, 10, "A", 1), // 1
		taRequest(3, 1003, 1, 2, 10, "A", 1), // 2: table full, VUser 1 evicted
		taRequest(1, 1001, 6, 3, 10, "Z", 1), // 3: late row of iteration 6
		taRequest(1, 1001, 7, 4, 10, "B", 2), // 4: VUser 1 resumes iteration 7
	}
	index := iterationIndexFromEvents(events)
	if index.LateRows != 1 || index.IterationsTotal != 4 {
		t.Fatalf("index = %+v", index)
	}
	split := map[int64]bool{}
	for _, ref := range index.Representatives {
		if ref.VUserID == 1 {
			if ref.Iteration == 6 {
				t.Fatalf("a late row opened a phantom iteration: %+v", ref)
			}
			split[ref.FirstOrdinal] = ref.Split
		}
	}
	if !split[0] || !split[4] {
		t.Fatalf("both halves of the split iteration must be marked: %v (refs %+v)", split, index.Representatives)
	}
	if !index.Truncated || !reflect.DeepEqual(index.Reasons, []string{iterationIndexReasonIdentityEvicted}) {
		t.Fatalf("index = %+v", index)
	}
}

// Many one-iteration lifetimes overflow the evicted-identity table many times
// over; none resumes, so none is split and the span distribution keeps every
// iteration. A long-pruned identity that does resume is still flagged.
func TestIterationIndexEvictedTableStaysBoundedWithoutFalseSplits(t *testing.T) {
	setTimeAnatomyCap(t, &maxIterationIndexIdentities, 8)
	var events []TransactionEvent
	for vu := 1; vu <= 200; vu++ {
		events = append(events, taRequest(vu, uint64(5000+vu), 1, float64(vu), 10, "A", 1))
	}
	acc := newArchiveIterationIndexAccumulator()
	for ordinal := range events {
		acc.merge(&events[ordinal], archiveEventPopulation(events[ordinal]), int64(ordinal))
		if len(acc.evictedIDs) > maxIterationIndexIdentities {
			t.Fatalf("evictedIDs grew to %d", len(acc.evictedIDs))
		}
	}
	if acc.pruned == nil {
		t.Fatal("the evicted table never pruned")
	}
	// VUser 1 (pruned long ago) resumes its iteration 1.
	resume := taRequest(1, 5001, 1, 300, 10, "B", 2)
	acc.merge(&resume, archiveEventPopulation(resume), int64(len(events)))
	index := acc.finalize()
	// The first fragment's span already counted (the distribution cannot
	// forget one); the resumed fragment is partial; both are flagged split.
	if index.IterationsTotal != 201 || index.Span.Samples != 200 || index.PartialIterations != 1 ||
		!reflect.DeepEqual(index.Reasons, []string{iterationIndexReasonIdentityEvicted}) {
		t.Fatalf("index = %+v", index)
	}
	if refs := iterationRefs(index, "slowest"); len(refs) == 0 || refs[0].VUserID != 1 || !refs[0].Split {
		t.Fatalf("the resumed identity's first record is not flagged split: %+v", index.Representatives)
	}
	clean := iterationIndexFromEvents(events)
	if clean.PartialIterations != 0 || clean.Span.Samples != 200 || clean.Truncated || len(clean.Reasons) != 0 {
		t.Fatalf("no identity resumed, yet index = %+v", clean)
	}
}

// A slowest pick cut short by the test's stop says so, since its span can
// exceed span.max_ms.
func TestIterationIndexPartialSlowestIsFlagged(t *testing.T) {
	interrupted := taRequest(1, 1001, 1, 0, 45000, "A", 1)
	interrupted.Success, interrupted.Interrupted = false, true
	index := iterationIndexFromEvents([]TransactionEvent{interrupted, taRequest(2, 1002, 1, 0, 8000, "A", 1)})
	refs := iterationRefs(index, "slowest")
	if len(refs) == 0 || refs[0].VUserID != 1 || !refs[0].Interrupted || refs[0].SpanMs <= index.Span.MaxMs {
		t.Fatalf("slowest = %+v (max %v)", refs, index.Span.MaxMs)
	}
	for _, ref := range index.Representatives {
		if ref.VUserID == 2 && ref.Interrupted {
			t.Fatalf("a whole iteration is flagged interrupted: %+v", ref)
		}
	}
	raw, _ := json.Marshal(refs[0])
	if !strings.Contains(string(raw), `"interrupted":true`) {
		t.Fatalf("json = %s", raw)
	}
}

func TestIterationIndexInRunArchiveReport(t *testing.T) {
	report := buildTimeAnatomyReportForTest(t, []TransactionEvent{
		taRequest(1, iterationTestLifetime, 1, 0, 30, "A", 1),
		taTransaction(1, iterationTestLifetime, 1, 0, 100, "Home"),
	})
	if report.Iterations == nil || report.Iterations.IterationsTotal != 1 || len(report.Iterations.Representatives) == 0 {
		t.Fatalf("report iterations = %+v", report.Iterations)
	}
	if report.TimeAnatomy == nil {
		t.Fatal("time anatomy missing")
	}
}

// Rebuilding the index over the same archive gives the same result even when
// the evicted-identity table fills partway through an eviction pass: which
// identities keep an exact evicted record and which fall into the pruned
// filter must not depend on map iteration order, since the two treat a later
// lower-iteration row differently (a late row versus a new iteration).
func TestIterationIndexEvictionPruneIsDeterministic(t *testing.T) {
	setTimeAnatomyCap(t, &maxIterationIndexIdentities, 8)
	build := func() *archiveIterationIndexAccumulator {
		acc := newArchiveIterationIndexAccumulator()
		// Four older evicted identities: the table fills after four of
		// this pass's five victims, so the prune's median is a victim's.
		for vu := 1; vu <= 4; vu++ {
			acc.evictedIDs[timeAnatomyIdentity{group: "g1", vuserID: vu, lifetime: 1}] = iterationIndexEvicted{iteration: 2, lastOrdinal: int64(vu)}
		}
		for vu := 11; vu <= 18; vu++ {
			identity := timeAnatomyIdentity{group: "g1", vuserID: vu, lifetime: 1}
			acc.states[identity] = &iterationIndexState{open: true, cur: iterationIndexRecord{identity: identity, iteration: 2, firstOrdinal: int64(vu), lastOrdinal: int64(vu)}}
		}
		acc.evictStalest()
		return acc
	}
	keys := func(acc *archiveIterationIndexAccumulator) []int {
		var vusers []int
		for identity := range acc.evictedIDs {
			vusers = append(vusers, identity.vuserID)
		}
		slices.Sort(vusers)
		return vusers
	}
	want := keys(build())
	// The victims close oldest first, so the prune drops the four older
	// records and the oldest victim; the other victims keep exact records.
	if !reflect.DeepEqual(want, []int{12, 13, 14, 15}) {
		t.Fatalf("evicted records = %v", want)
	}
	for run := 0; run < 50; run++ {
		if got := keys(build()); !reflect.DeepEqual(got, want) {
			t.Fatalf("run %d: evicted records = %v, want %v", run, got, want)
		}
	}
}
