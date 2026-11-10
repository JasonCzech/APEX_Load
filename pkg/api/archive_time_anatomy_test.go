package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/runcontract"
)

var timeAnatomyTestBase = time.Date(2026, time.November, 1, 10, 0, 0, 0, time.UTC)

func taAt(ms float64) time.Time {
	return timeAnatomyTestBase.Add(time.Duration(math.Round(ms * 1e6)))
}

// taRequest is a request row of VUser vu (lifetime life) in iteration iter
// that started at startMs.
func taRequest(vu int, life uint64, iter int64, startMs, durationMs float64, action string, step int) TransactionEvent {
	return TransactionEvent{
		TestID: "test-query", Population: "action", DependencyType: "http", ActionName: action,
		SourceStepIndex: step, VUserID: vu, VUserLifetime: life, Iteration: iter,
		Timestamp: taAt(startMs), DurationMs: durationMs, TTFBMs: durationMs, Success: true, Group: "g1",
	}
}

func taTransaction(vu int, life uint64, iter int64, startMs, durationMs float64, name string) TransactionEvent {
	return TransactionEvent{
		TestID: "test-query", Population: "transaction", BusinessTransaction: true, ActionName: name,
		VUserID: vu, VUserLifetime: life, Iteration: iter, Timestamp: taAt(startMs), DurationMs: durationMs,
		Success: true, Group: "g1",
	}
}

func buildTimeAnatomyReportForTest(t *testing.T, events []TransactionEvent) *RunArchiveReport {
	t.Helper()
	record := writeArchiveTransactionsForTest(t, events)
	for _, name := range []string{"resources.jsonl", "interval_summaries.jsonl"} {
		if err := os.WriteFile(filepath.Join(record.ArchivePath, name), nil, 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}
	report, err := buildRunArchiveReport(record)
	if err != nil {
		t.Fatalf("buildRunArchiveReport returned error: %v", err)
	}
	return report
}

func timeAnatomyForTest(t *testing.T, events []TransactionEvent) *RunArchiveTimeAnatomy {
	t.Helper()
	anatomy := buildTimeAnatomyReportForTest(t, events).TimeAnatomy
	if anatomy == nil {
		t.Fatal("time_anatomy is nil")
	}
	return anatomy
}

func taEntry(t *testing.T, anatomy *RunArchiveTimeAnatomy, name string) RunArchiveTransactionAnatomy {
	t.Helper()
	for _, entry := range anatomy.Transactions {
		if entry.Name == name {
			return entry
		}
	}
	t.Fatalf("transaction %q not in time anatomy: %+v", name, anatomy.Transactions)
	return RunArchiveTransactionAnatomy{}
}

func taRow(t *testing.T, entry RunArchiveTransactionAnatomy, key string) RunArchiveAnatomyRequest {
	t.Helper()
	for _, row := range entry.Requests {
		if row.Key == key {
			return row
		}
	}
	keys := make([]string, 0, len(entry.Requests))
	for _, row := range entry.Requests {
		keys = append(keys, row.Key)
	}
	t.Fatalf("row %q not in %s: %v", key, entry.Name, keys)
	return RunArchiveAnatomyRequest{}
}

func taPhaseSum(phases RunArchivePhaseMeans) float64 {
	return phases.DNSMs + phases.ConnectMs + phases.TLSMs + phases.ServerMs + phases.DownloadMs + phases.UnsplitMs
}

func assertDecompositionIdentity(t *testing.T, decomposition RunArchiveTimeDecomposition) {
	t.Helper()
	got := taPhaseSum(decomposition.Phases) + decomposition.NonRequestMs - decomposition.OverlapMs
	if math.Abs(got-decomposition.AvgDurationMs) > 1e-9*math.Max(1, decomposition.AvgDurationMs) {
		t.Fatalf("decomposition identity: phases+non_request-overlap = %v, duration %v (%+v)", got, decomposition.AvgDurationMs, decomposition)
	}
}

func setTimeAnatomyCap[T any](t *testing.T, target *T, value T) {
	t.Helper()
	previous := *target
	*target = value
	t.Cleanup(func() { *target = previous })
}

// PHASE_SPLIT_PARITY_CASES in dashboard/src/components/runReport/timeAnatomyModel.ts
// mirrors this table.
func TestArchiveRequestPhaseSplit(t *testing.T) {
	nan, inf := math.NaN(), math.Inf(1)
	cases := []struct {
		name                       string
		dur, dns, connect, tls     float64
		ttfb                       float64
		want                       [timeAnatomyPhaseCount]float64
		clamped                    bool
		connectObserved, ttfbFlags bool
	}{
		{name: "full split", dur: 150, dns: 5, connect: 10, tls: 15, ttfb: 100, want: [6]float64{5, 10, 15, 70, 50, 0}, connectObserved: true, ttfbFlags: true},
		{name: "no setup", dur: 60, ttfb: 40, want: [6]float64{0, 0, 0, 40, 20, 0}, ttfbFlags: true},
		{name: "no ttfb", dur: 80, dns: 5, want: [6]float64{5, 0, 0, 0, 0, 75}},
		{name: "ttfb before setup", dur: 100, dns: 10, connect: 10, tls: 10, ttfb: 20, want: [6]float64{10, 10, 10, 0, 70, 0}, clamped: true, connectObserved: true, ttfbFlags: true},
		{name: "setup longer than duration", dur: 150, dns: 100, connect: 80, tls: 20, ttfb: 160, want: [6]float64{100, 50, 0, 0, 0, 0}, clamped: true, connectObserved: true, ttfbFlags: true},
		{name: "ttfb longer than duration", dur: 150, ttfb: 200, want: [6]float64{0, 0, 0, 150, 0, 0}, clamped: true, ttfbFlags: true},
		{name: "non-finite and negative", dur: 50, dns: nan, connect: -3, tls: inf, ttfb: nan, want: [6]float64{0, 0, 0, 0, 0, 50}},
		{name: "negative duration", dur: -5, dns: 3, ttfb: 4, want: [6]float64{}, clamped: true, ttfbFlags: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			split := archiveRequestPhaseSplit(tc.dur, tc.dns, tc.connect, tc.tls, tc.ttfb)
			if split.Parts != tc.want {
				t.Fatalf("parts = %v, want %v", split.Parts, tc.want)
			}
			if split.Clamped != tc.clamped {
				t.Fatalf("clamped = %v, want %v", split.Clamped, tc.clamped)
			}
			if got := split.Observed&timeAnatomyObservedConnect != 0; got != tc.connectObserved {
				t.Fatalf("connect observed = %v, want %v", got, tc.connectObserved)
			}
			if got := split.Observed&timeAnatomyObservedTTFB != 0; got != tc.ttfbFlags {
				t.Fatalf("ttfb observed = %v, want %v", got, tc.ttfbFlags)
			}
		})
	}

	random := rand.New(rand.NewSource(20261109))
	pick := func() float64 {
		switch random.Intn(8) {
		case 0:
			return 0
		case 1:
			return -random.Float64() * 10
		case 2:
			return math.NaN()
		case 3:
			return math.Inf(1)
		}
		return random.Float64() * 500
	}
	for index := 0; index < 10_000; index++ {
		dur := pick()
		split := archiveRequestPhaseSplit(dur, pick(), pick(), pick(), pick())
		var sum float64
		for _, part := range split.Parts {
			if part < 0 || math.IsNaN(part) {
				t.Fatalf("row %d: negative or NaN part in %v", index, split.Parts)
			}
			sum += part
		}
		if want := timeAnatomyPos(dur); math.Abs(sum-want) > 1e-9*math.Max(1, want) {
			t.Fatalf("row %d: parts %v sum to %v, want %v", index, split.Parts, sum, want)
		}
	}
}

func TestTimeAnatomyBasicJoin(t *testing.T) {
	events := []TransactionEvent{
		taRequest(1, 11, 1, 0, 30, "A", 1),
		taRequest(1, 11, 1, 30, 40, "B", 2),
		taRequest(1, 11, 1, 70, 20, "C", 3),
		taTransaction(1, 11, 1, 0, 100, "Checkout"),
	}
	anatomy := timeAnatomyForTest(t, events)
	entry := taEntry(t, anatomy, "Checkout")
	if entry.Instances != 1 || entry.JoinedInstances != 1 {
		t.Fatalf("instances = %d joined %d, want 1/1", entry.Instances, entry.JoinedInstances)
	}
	for key, offset := range map[string]float64{"1|A#1": 0, "2|B#1": 30, "3|C#1": 70} {
		if got := taRow(t, entry, key).OffsetMs.P50Ms; got != offset {
			t.Fatalf("%s offset = %v, want %v", key, got, offset)
		}
	}
	if got := []string{entry.Requests[0].Key, entry.Requests[1].Key, entry.Requests[2].Key}; !slices.Equal(got, []string{"1|A#1", "2|B#1", "3|C#1"}) {
		t.Fatalf("row order = %v", got)
	}
	decomposition := entry.Decomposition
	if decomposition.NonRequestMs != 10 || decomposition.OverlapMs != 0 || decomposition.AvgRequestsPerInstance != 3 {
		t.Fatalf("decomposition = %+v, want non_request 10, overlap 0, 3 requests", decomposition)
	}
	assertDecompositionIdentity(t, decomposition)
	if anatomy.Coverage.RequestsInTransactions != 3 || anatomy.Coverage.RequestsOutsideTransactions != 0 || anatomy.Coverage.JoinedTransactionRows != 1 {
		t.Fatalf("coverage = %+v", anatomy.Coverage)
	}
	if anatomy.Schema != timeAnatomySchema || anatomy.PhaseModel != timeAnatomyPhaseModel || anatomy.JoinRule != timeAnatomyJoinRule {
		t.Fatalf("schema fields = %q %q %q", anatomy.Schema, anatomy.PhaseModel, anatomy.JoinRule)
	}
}

func TestTimeAnatomyOverlap(t *testing.T) {
	events := []TransactionEvent{
		taRequest(1, 11, 1, 0, 60, "A", 1),
		taRequest(1, 11, 1, 0, 60, "B", 2),
		taTransaction(1, 11, 1, 0, 70, "Parallel"),
	}
	decomposition := taEntry(t, timeAnatomyForTest(t, events), "Parallel").Decomposition
	if decomposition.OverlapMs != 60 || decomposition.NonRequestMs != 10 {
		t.Fatalf("decomposition = %+v, want overlap 60, non_request 10", decomposition)
	}
	assertDecompositionIdentity(t, decomposition)
}

func TestTimeAnatomyNesting(t *testing.T) {
	events := []TransactionEvent{
		taRequest(1, 11, 1, 0, 20, "A", 1),
		taRequest(1, 11, 1, 20, 20, "B", 2),
		taTransaction(1, 11, 1, 0, 40, "Inner"),
		taRequest(1, 11, 1, 40, 20, "C", 3),
		taTransaction(1, 11, 1, 0, 60, "Outer"),
		taTransaction(1, 11, 1, 0, 65, "Iteration"),
	}
	anatomy := timeAnatomyForTest(t, events)
	for name, want := range map[string]int{"Inner": 2, "Outer": 3, "Iteration": 3} {
		entry := taEntry(t, anatomy, name)
		if len(entry.Requests) != want {
			t.Fatalf("%s rows = %d, want %d", name, len(entry.Requests), want)
		}
		assertDecompositionIdentity(t, entry.Decomposition)
	}
	if anatomy.Coverage.RequestsInTransactions != 3 {
		t.Fatalf("requests_in_transactions = %d, want each child once", anatomy.Coverage.RequestsInTransactions)
	}
}

func TestTimeAnatomyLoopInOneIteration(t *testing.T) {
	events := []TransactionEvent{
		taRequest(1, 11, 1, 0, 30, "A", 1),
		taTransaction(1, 11, 1, 0, 30, "Loop"),
		taRequest(1, 11, 1, 40, 30, "A", 1),
		taTransaction(1, 11, 1, 40, 30, "Loop"),
	}
	entry := taEntry(t, timeAnatomyForTest(t, events), "Loop")
	if entry.Instances != 2 || entry.Decomposition.AvgRequestsPerInstance != 1 {
		t.Fatalf("loop = %+v, want two instances of one request each", entry.Decomposition)
	}
	if row := taRow(t, entry, "1|A#1"); row.Samples != 2 || row.PresencePct != 100 {
		t.Fatalf("row = samples %d presence %v", row.Samples, row.PresencePct)
	}
}

func TestTimeAnatomyOccurrences(t *testing.T) {
	events := []TransactionEvent{
		taRequest(1, 11, 1, 0, 10, "A", 1),
		taRequest(1, 11, 1, 10, 10, "B", 2),
		taRequest(1, 11, 1, 20, 10, "B", 2),
		taTransaction(1, 11, 1, 0, 30, "Repeat"),
	}
	entry := taEntry(t, timeAnatomyForTest(t, events), "Repeat")
	taRow(t, entry, "2|B#1")
	if row := taRow(t, entry, "2|B#2"); row.Occurrence != 2 || row.OffsetMs.P50Ms != 20 {
		t.Fatalf("B#2 = %+v", row)
	}

	events = nil
	for index := 0; index < 10; index++ {
		events = append(events, taRequest(1, 11, 1, float64(index*10), 10, "B", 2))
	}
	events = append(events, taTransaction(1, 11, 1, 0, 100, "Ten"))
	anatomy := timeAnatomyForTest(t, events)
	entry = taEntry(t, anatomy, "Ten")
	if len(entry.Requests) != 9 {
		t.Fatalf("rows = %d, want 9 (#9 folded)", len(entry.Requests))
	}
	folded := taRow(t, entry, "2|B#9")
	if !folded.OccurrenceFolded || folded.Samples != 2 {
		t.Fatalf("B#9 = folded %v samples %d, want folded with 2 samples", folded.OccurrenceFolded, folded.Samples)
	}
	if !slices.Contains(anatomy.Truncation.Reasons, timeAnatomyReasonOccurrenceFold) || !anatomy.Truncation.Truncated {
		t.Fatalf("truncation = %+v, want occurrence_fold", anatomy.Truncation)
	}
}

func TestTimeAnatomyIsolation(t *testing.T) {
	events := []TransactionEvent{
		taRequest(1, 11, 1, 0, 30, "A", 1),
		taRequest(2, 22, 1, 0, 50, "B", 1),
		taRequest(1, 99, 1, 5, 10, "C", 1), // reused vuser_id, another lifetime
		taTransaction(1, 11, 1, 0, 40, "T"),
		taTransaction(2, 22, 1, 0, 60, "T"),
	}
	anatomy := timeAnatomyForTest(t, events)
	entry := taEntry(t, anatomy, "T")
	if entry.Decomposition.AvgRequestsPerInstance != 1 {
		t.Fatalf("avg requests = %v, want 1 (no cross-VUser claims)", entry.Decomposition.AvgRequestsPerInstance)
	}
	if anatomy.Coverage.RequestsOutsideTransactions != 1 || anatomy.Coverage.RequestsInTransactions != 2 {
		t.Fatalf("coverage = %+v, want C outside", anatomy.Coverage)
	}
}

func TestTimeAnatomyLegacyAndUnjoinable(t *testing.T) {
	events := []TransactionEvent{
		taRequest(1, 0, 1, 0, 30, "A", 1),
		taTransaction(1, 0, 1, 0, 40, "Legacy"),
		taRequest(0, 0, 1, 0, 30, "A", 1),
		taTransaction(0, 0, 1, 0, 40, "NoIdentity"),
	}
	anatomy := timeAnatomyForTest(t, events)
	if anatomy.Coverage.LegacyIdentityRows != 2 {
		t.Fatalf("legacy_identity_rows = %d, want 2", anatomy.Coverage.LegacyIdentityRows)
	}
	if entry := taEntry(t, anatomy, "Legacy"); entry.JoinedInstances != 1 {
		t.Fatalf("legacy joined = %d, want 1", entry.JoinedInstances)
	}
	entry := taEntry(t, anatomy, "NoIdentity")
	if entry.Instances != 1 || entry.JoinedInstances != 0 || anatomy.Coverage.UnjoinableTransactionRows != 1 {
		t.Fatalf("unjoinable = %+v coverage %+v", entry, anatomy.Coverage)
	}
}

func TestTimeAnatomyLateChild(t *testing.T) {
	events := []TransactionEvent{
		taRequest(1, 11, 1, 0, 30, "A", 1),
		taTransaction(1, 11, 1, 0, 100, "T"),
		taRequest(1, 11, 1, 40, 30, "Late", 2),
	}
	anatomy := timeAnatomyForTest(t, events)
	if anatomy.Coverage.LateRequestRows != 1 {
		t.Fatalf("late_request_rows = %d, want 1", anatomy.Coverage.LateRequestRows)
	}
	if entry := taEntry(t, anatomy, "T"); len(entry.Requests) != 1 {
		t.Fatalf("rows = %d, want the late child unclaimed", len(entry.Requests))
	}
}

func TestTimeAnatomyEndsAfterTransaction(t *testing.T) {
	events := []TransactionEvent{
		taRequest(1, 11, 1, 10, 100, "Long", 1),
		taTransaction(1, 11, 1, 0, 50, "T"),
	}
	anatomy := timeAnatomyForTest(t, events)
	if anatomy.Coverage.RequestsEndingAfterTransaction != 1 {
		t.Fatalf("requests_ending_after_transaction = %d, want 1", anatomy.Coverage.RequestsEndingAfterTransaction)
	}
	assertDecompositionIdentity(t, taEntry(t, anatomy, "T").Decomposition)
}

func TestTimeAnatomyCarryOpenSourceTransactions(t *testing.T) {
	events := []TransactionEvent{
		taRequest(1, 11, 2, 0, 10, "Old", 1),
		taRequest(1, 11, 3, 100, 10, "Carried", 2),
		// Iteration 4 begins before the transaction opened in iteration 3 is
		// recorded (carryOpenSourceTransactions).
		taRequest(1, 11, 4, 120, 10, "Next", 3),
		taTransaction(1, 11, 4, 95, 40, "Carry"),
	}
	anatomy := timeAnatomyForTest(t, events)
	if anatomy.Coverage.RequestsClaimedAcrossIteration == 0 {
		t.Fatalf("requests_claimed_across_iteration = 0, want > 0 (%+v)", anatomy.Coverage)
	}
	entry := taEntry(t, anatomy, "Carry")
	taRow(t, entry, "2|Carried#1")
	if anatomy.Coverage.RequestsOutsideTransactions != 1 {
		t.Fatalf("requests_outside_transactions = %d, want the iteration-2 request flushed", anatomy.Coverage.RequestsOutsideTransactions)
	}
}

func taTwoVUserEvents(vu int, life uint64, offsetMs float64, id string, iterations int64) []TransactionEvent {
	var events []TransactionEvent
	for iteration := int64(1); iteration <= iterations; iteration++ {
		start := offsetMs + float64(iteration)*1000
		a := taRequest(vu, life, iteration, start, 20+float64(vu), "A", 1)
		b := taRequest(vu, life, iteration, start+30, 40+float64(iteration), "B", 2)
		tx := taTransaction(vu, life, iteration, start, 90+float64(vu*10)+float64(iteration), "T")
		a.EventID = fmt.Sprintf("tx_%032x", vu*1000+int(iteration)*10+1)
		b.EventID = fmt.Sprintf("tx_%032x", vu*1000+int(iteration)*10+2)
		tx.EventID = fmt.Sprintf("tx_%s%031x", id, vu*1000+int(iteration)*10+3)
		events = append(events, a, b, tx)
	}
	return events
}

func taStripOrdinals(anatomy *RunArchiveTimeAnatomy) {
	for index := range anatomy.Transactions {
		for rep := range anatomy.Transactions[index].Representatives {
			anatomy.Transactions[index].Representatives[rep].Ordinal = 0
			anatomy.Transactions[index].Representatives[rep].FirstOrdinal = 0
		}
	}
}

func TestTimeAnatomyDistributedLayout(t *testing.T) {
	// An odd instance count keeps the P50 pick free of ordinal ties.
	first := taTwoVUserEvents(1, 11, 0, "a", 3)
	second := taTwoVUserEvents(2, 22, 5, "b", 4)
	concatenated := append(append([]TransactionEvent{}, first...), second...)
	var interleaved []TransactionEvent
	for index := 0; index < len(second); index += 3 {
		if index < len(first) {
			interleaved = append(interleaved, first[index:index+3]...)
		}
		interleaved = append(interleaved, second[index:index+3]...)
	}
	left := timeAnatomyForTest(t, concatenated)
	right := timeAnatomyForTest(t, interleaved)
	taStripOrdinals(left)
	taStripOrdinals(right)
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	if !bytes.Equal(leftJSON, rightJSON) {
		t.Fatalf("layouts differ:\n%s\n%s", leftJSON, rightJSON)
	}
}

func TestTimeAnatomyElapsedWindow(t *testing.T) {
	tx := taTransaction(1, 11, 1, 0, 50, "Elapsed")
	tx.ElapsedMs = 100
	events := []TransactionEvent{
		taRequest(1, 11, 1, 0, 40, "A", 1),
		taRequest(1, 11, 1, 40, 40, "B", 2),
		tx,
	}
	entry := taEntry(t, timeAnatomyForTest(t, events), "Elapsed")
	if entry.Decomposition.AvgRequestsPerInstance != 2 {
		t.Fatalf("requests = %v, want B claimed through elapsed", entry.Decomposition.AvgRequestsPerInstance)
	}
	if entry.Decomposition.NonRequestMs != -30 {
		t.Fatalf("non_request_ms = %v, want -30", entry.Decomposition.NonRequestMs)
	}
	assertDecompositionIdentity(t, entry.Decomposition)
}

func TestTimeAnatomyObservedQuantileEqualsExact(t *testing.T) {
	random := rand.New(rand.NewSource(7))
	for _, n := range []int{1, 2, 10, 100, 250} {
		observed := newObservedQuantile()
		var all []float64
		for index := 0; index < n; index++ {
			value := 0.0
			if random.Intn(10) == 0 {
				value = math.Round(random.Float64()*1000) + 1
			}
			observed.add(value, value > 0)
			all = append(all, value)
		}
		sort.Float64s(all)
		for _, q := range []float64{0, .1, .5, .89, .9, .91, .95, .99, 1} {
			if got, want := observed.quantile(q), percentileFromSortedSamples(all, q); math.Abs(got-want) > 1e-9 {
				t.Fatalf("n=%d q=%v: got %v, want %v", n, q, got, want)
			}
		}
	}

	var events []TransactionEvent
	for index := 0; index < 100; index++ {
		start := float64(index) * 1000
		request := taRequest(1, 11, int64(index), start, 50, "A", 1)
		if index%10 == 0 {
			request.ConnectMs = 8
		}
		events = append(events, request, taTransaction(1, 11, int64(index), start, 60, "T"))
	}
	connect := taRow(t, taEntry(t, timeAnatomyForTest(t, events), "T"), "1|A#1").Phases.Connect
	if connect.P50Ms != 0 || connect.ObservedPct == nil || *connect.ObservedPct != 10 || connect.AvgWhenObservedMs == nil || *connect.AvgWhenObservedMs != 8 || connect.AvgMs != 0.8 {
		t.Fatalf("connect = %+v (observed %v, when observed %v)", connect, connect.ObservedPct, connect.AvgWhenObservedMs)
	}
}

func TestTimeAnatomySketchQuality(t *testing.T) {
	var events []TransactionEvent
	var durations []float64
	for index := 0; index < 2000; index++ {
		start := float64(index) * 1000
		duration := float64(10 + index%397)
		durations = append(durations, duration)
		events = append(events, taRequest(1, 11, int64(index), start, duration, "A", 1), taTransaction(1, 11, int64(index), start, duration+5, "T"))
	}
	total := taRow(t, taEntry(t, timeAnatomyForTest(t, events), "T"), "1|A#1").Phases.Total
	if total.PercentileQuality != percentileQualityApproximate {
		t.Fatalf("percentile_quality = %q, want approximate", total.PercentileQuality)
	}
	sort.Float64s(durations)
	exact := percentileFromSortedSamples(durations, .95)
	if math.Abs(total.P95Ms-exact) > 0.005*exact+1e-9 {
		t.Fatalf("P95 %v not within 0.5%% of %v", total.P95Ms, exact)
	}
}

func TestTimeAnatomyDurationMatchesSLARow(t *testing.T) {
	report := buildTimeAnatomyReportForTest(t, taMainFixture())
	entry := taEntry(t, report.TimeAnatomy, "Checkout")
	sla := report.ByTransaction["Checkout"]
	if entry.Duration.P50Ms != sla.P50Ms || entry.Duration.P95Ms != sla.P95Ms || entry.Duration.P99Ms != sla.P99Ms || entry.Duration.P90Ms != sla.P90Ms || entry.Duration.MaxMs != sla.MaxResponseMs || entry.Duration.AvgMs != sla.AvgResponseMs || entry.Duration.Samples != sla.Transactions {
		t.Fatalf("duration %+v differs from SLA row %+v", entry.Duration, sla)
	}
}

func TestTimeAnatomyConnectionStats(t *testing.T) {
	reused := taRequest(1, 11, 1, 0, 10, "A", 1)
	reused.ServerTelemetry = &protocol.ServerTelemetry{ConnectionReused: true}
	fresh := taRequest(1, 11, 2, 1000, 10, "A", 1)
	fresh.ConnectMs = 3
	inconsistent := taRequest(1, 11, 3, 2000, 10, "A", 1)
	inconsistent.ConnectMs = 3
	inconsistent.ServerTelemetry = &protocol.ServerTelemetry{ConnectionReused: true}
	unknown := taRequest(1, 11, 4, 3000, 10, "A", 1)
	events := []TransactionEvent{
		reused, taTransaction(1, 11, 1, 0, 10, "T"),
		fresh, taTransaction(1, 11, 2, 1000, 10, "T"),
		inconsistent, taTransaction(1, 11, 3, 2000, 10, "T"),
		unknown, taTransaction(1, 11, 4, 3000, 10, "T"),
	}
	connections := taRow(t, taEntry(t, timeAnatomyForTest(t, events), "T"), "1|A#1").Connections
	if connections.New != 2 || connections.Reused != 1 || connections.Unknown != 1 || connections.Inconsistent != 1 || connections.ReusePct == nil || math.Abs(*connections.ReusePct-100.0/3) > 1e-9 {
		t.Fatalf("connections = %+v", connections)
	}

	events = []TransactionEvent{taRequest(1, 11, 1, 0, 10, "A", 1), taTransaction(1, 11, 1, 0, 10, "T")}
	if pct := taRow(t, taEntry(t, timeAnatomyForTest(t, events), "T"), "1|A#1").Connections.ReusePct; pct != nil {
		t.Fatalf("reuse_pct = %v, want null when nothing is classifiable", *pct)
	}
}

func TestTimeAnatomyInterrupted(t *testing.T) {
	interrupted := taTransaction(1, 11, 2, 1000, 500, "T")
	interrupted.Interrupted = true
	interrupted.Success = false
	events := []TransactionEvent{
		taRequest(1, 11, 1, 0, 30, "A", 1), taTransaction(1, 11, 1, 0, 40, "T"),
		taRequest(1, 11, 2, 1000, 30, "A", 1), interrupted,
	}
	anatomy := timeAnatomyForTest(t, events)
	entry := taEntry(t, anatomy, "T")
	if entry.Instances != 2 || entry.InterruptedInstances != 1 || entry.Decomposition.Instances != 1 || anatomy.Coverage.InterruptedTransactionRows != 1 {
		t.Fatalf("interrupted counts = %+v", entry)
	}
	if row := taRow(t, entry, "1|A#1"); row.Samples != 1 {
		t.Fatalf("row samples = %d, want interrupted excluded", row.Samples)
	}
	if entry.Tail.Instances != 1 || entry.Tail.SampledInstances != 1 {
		t.Fatalf("tail = %+v, want interrupted excluded", entry.Tail)
	}
	for _, rep := range entry.Representatives {
		if rep.DurationMs == 500 {
			t.Fatalf("interrupted instance chosen as representative %+v", rep)
		}
	}
}

// taMainFixture: 1000 Checkout instances whose P50 is 110 ms and P95 130 ms;
// 500 at 100 ms (server 60, connect 4) and 49 at 540 ms (server 460,
// connect 44), with POST /cart carrying the slowness.
func taMainFixture() []TransactionEvent {
	var events []TransactionEvent
	for index := 0; index < 1000; index++ {
		vu := 1 + index%10
		iteration := int64(index / 10)
		start := float64(iteration) * 1000
		duration := 100.0
		switch {
		case index == 500:
			duration = 120
		case index > 500 && index <= 950:
			duration = 130
		case index > 950:
			duration = 540
		}
		cart := taRequest(vu, uint64(vu)*1_000_003, iteration, start, 75, "POST /cart", 2)
		cart.DNSMs, cart.ConnectMs, cart.TLSMs, cart.TTFBMs = 1, 2, 2, 65
		confirm := taRequest(vu, uint64(vu)*1_000_003, iteration, start+75, 12, "GET /confirm", 3)
		confirm.ConnectMs, confirm.TTFBMs = 2, 2
		if duration == 540 {
			cart.DurationMs, cart.ConnectMs, cart.TTFBMs = 505, 32, 495
			confirm.Timestamp = taAt(start + 505)
			confirm.DurationMs, confirm.ConnectMs, confirm.TTFBMs = 22, 12, 12
		}
		tx := taTransaction(vu, uint64(vu)*1_000_003, iteration, start, duration, "Checkout")
		events = append(events, cart, confirm, tx)
	}
	return events
}

const taMainHeadline = "P95 tail is +440 ms vs median: server +400 ms, connect +40 ms (samples above P95 130 ms average 540 ms; samples at or below P50 average 100 ms); most of it in POST /cart (+430 ms)"

func TestTimeAnatomyTailMainFixture(t *testing.T) {
	entry := taEntry(t, timeAnatomyForTest(t, taMainFixture()), "Checkout")
	tail := entry.Tail
	if tail == nil || tail.Status != "ok" {
		t.Fatalf("tail = %+v, want ok", tail)
	}
	if tail.ThresholdP50Ms != 110 || tail.ThresholdP95Ms != 130 || tail.BandEstimate != "exact" || tail.Tail.Count != 49 || tail.Median.Count != 500 {
		t.Fatalf("tail thresholds/bands = %+v", tail)
	}
	if math.Abs(tail.Delta.TotalMs-440) > 1 {
		t.Fatalf("delta total = %v, want 440", tail.Delta.TotalMs)
	}
	if tail.Delta.Components[0].Component != "server" || tail.Delta.Components[0].DeltaMs != 400 || tail.Delta.Components[1].Component != "connect" || tail.Delta.Components[1].DeltaMs != 40 {
		t.Fatalf("components = %+v", tail.Delta.Components)
	}
	var sum float64
	for _, component := range tail.Delta.Components {
		sum += component.DeltaMs
	}
	if math.Abs(sum-tail.Delta.TotalMs) > 1e-6*math.Max(1, math.Abs(tail.Delta.TotalMs)) {
		t.Fatalf("components sum %v, total %v", sum, tail.Delta.TotalMs)
	}
	if tail.Headline != taMainHeadline {
		t.Fatalf("headline =\n%q\nwant\n%q", tail.Headline, taMainHeadline)
	}
	for _, r := range tail.Headline {
		if r > 0x7e || r < 0x20 {
			t.Fatalf("headline is not ASCII: %q", tail.Headline)
		}
	}
	if len(tail.Requests) == 0 || tail.Requests[0].Key != "2|POST /cart#1" || tail.Requests[0].DeltaMs != 430 || tail.Requests[0].DominantPhase != "server" || tail.Requests[0].DominantMs != 400 {
		t.Fatalf("request deltas = %+v", tail.Requests)
	}
	assertDecompositionIdentity(t, entry.Decomposition)
}

func TestTimeAnatomyTailStatuses(t *testing.T) {
	short := taMainFixture()[:39*3]
	if status := taEntry(t, timeAnatomyForTest(t, short), "Checkout").Tail.Status; status != "insufficient_samples" {
		t.Fatalf("39 instances status = %q", status)
	}

	var identical []TransactionEvent
	for index := 0; index < 60; index++ {
		start := float64(index) * 1000
		identical = append(identical, taRequest(1, 11, int64(index), start, 50, "A", 1), taTransaction(1, 11, int64(index), start, 100, "Flat"))
	}
	if status := taEntry(t, timeAnatomyForTest(t, identical), "Flat").Tail.Status; status != "no_tail" {
		t.Fatalf("identical durations status = %q", status)
	}

	lowJoin := taMainFixture()
	stripped := 0
	for index := range lowJoin {
		if lowJoin[index].Population == "transaction" && lowJoin[index].DurationMs == 540 && stripped < 20 {
			lowJoin[index].VUserID, lowJoin[index].VUserLifetime = 0, 0
			stripped++
		}
	}
	tail := taEntry(t, timeAnatomyForTest(t, lowJoin), "Checkout").Tail
	if tail.Status != "low_join_coverage" || tail.Headline != "" || tail.Tail.Joined != 29 {
		t.Fatalf("low join tail = status %q headline %q joined %d", tail.Status, tail.Headline, tail.Tail.Joined)
	}
}

func TestTimeAnatomySampling(t *testing.T) {
	setTimeAnatomyCap(t, &timeAnatomyInstanceK, 64)
	setTimeAnatomyCap(t, &timeAnatomyInstanceKMin, 16)
	anatomy := timeAnatomyForTest(t, taMainFixture())
	tail := taEntry(t, anatomy, "Checkout").Tail
	if tail.BandEstimate != "sampled" || tail.SampledInstances != 64 || tail.Instances != 1000 {
		t.Fatalf("tail sample = %+v", tail)
	}
	if !slices.Contains(anatomy.Truncation.Reasons, timeAnatomyReasonInstanceSample) {
		t.Fatalf("reasons = %v", anatomy.Truncation.Reasons)
	}

	// Bottom-k is independent of insertion order.
	sampleOrdinals := func(order []int64) []int64 {
		accumulator := newArchiveTimeAnatomyAccumulator()
		name := &timeAnatomyName{name: "T", rows: map[timeAnatomyRowKey]*timeAnatomyRow{}, k: 64}
		accumulator.names["T"] = name
		for _, ordinal := range order {
			accumulator.insertSample(name, timeAnatomyInstance{priority: timeAnatomyPriority(ordinal), ordinal: ordinal, durationMs: float64(ordinal)})
		}
		var ordinals []int64
		for _, instance := range name.sample {
			ordinals = append(ordinals, instance.ordinal)
		}
		slices.Sort(ordinals)
		return ordinals
	}
	order := make([]int64, 1000)
	for index := range order {
		order[index] = int64(index)
	}
	want := sampleOrdinals(order)
	rand.New(rand.NewSource(3)).Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
	if got := sampleOrdinals(order); !slices.Equal(got, want) {
		t.Fatalf("shuffled bottom-k = %v, want %v", got, want)
	}

	setTimeAnatomyCap(t, &timeAnatomySampleBudgetBytes, 40*timeAnatomyInstanceBaseBytes)
	anatomy = timeAnatomyForTest(t, taMainFixture())
	tail = taEntry(t, anatomy, "Checkout").Tail
	if !slices.Contains(anatomy.Truncation.Reasons, timeAnatomyReasonSampleBudget) || tail.SampledInstances < int64(timeAnatomyInstanceKMin) || tail.SampledInstances >= 64 {
		t.Fatalf("budget: reasons %v sampled %d", anatomy.Truncation.Reasons, tail.SampledInstances)
	}
}

func TestTimeAnatomyRepresentatives(t *testing.T) {
	events := taMainFixture()
	// Make one instance fail and give lifetimes above 2^53.
	for index := range events {
		events[index].VUserLifetime += 1 << 60
	}
	var failedOrdinal int64 = -1
	for index := range events {
		if events[index].Population == "transaction" && index > 30 {
			events[index].Success = false
			events[index].StatusCode = 503
			failedOrdinal = int64(index)
			break
		}
	}
	report := buildTimeAnatomyReportForTest(t, events)
	entry := taEntry(t, report.TimeAnatomy, "Checkout")
	reasons := map[string]RunArchiveRepresentativeSample{}
	ids := map[string]bool{}
	for _, rep := range entry.Representatives {
		if ids[rep.EventID] {
			t.Fatalf("duplicate representative %s", rep.EventID)
		}
		ids[rep.EventID] = true
		reasons[rep.Reason] = rep
		if rep.FirstOrdinal > rep.Ordinal {
			t.Fatalf("first_ordinal %d > ordinal %d", rep.FirstOrdinal, rep.Ordinal)
		}
		if !strings.HasPrefix(rep.EventID, "tx_") {
			t.Fatalf("event_id = %q", rep.EventID)
		}
	}
	p50 := reasons["p50"]
	if p50.DurationMs != 100 || p50.Ordinal != 2 || p50.TargetMs == nil || *p50.TargetMs != 110 {
		t.Fatalf("p50 = %+v, want the lowest-ordinal 100 ms instance", p50)
	}
	if p95 := reasons["p95"]; p95.DurationMs != 130 || p95.Ordinal != 501*3+2 {
		t.Fatalf("p95 = %+v", p95)
	}
	if slowest := reasons["slowest"]; slowest.DurationMs != 540 || slowest.Ordinal != 951*3+2 {
		t.Fatalf("slowest = %+v", slowest)
	}
	if failed := reasons["failed"]; failed.Ordinal != failedOrdinal || failed.Success || failed.StatusCode != 503 {
		t.Fatalf("failed = %+v, want ordinal %d", failed, failedOrdinal)
	}
	raw, err := json.Marshal(entry.Representatives[0])
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf(`"vuser_lifetime":"%d"`, entry.Representatives[0].VUserLifetime)
	if !bytes.Contains(raw, []byte(want)) || entry.Representatives[0].VUserLifetime <= 1<<53 {
		t.Fatalf("vuser_lifetime not a JSON string: %s", raw)
	}

	// slowest stays exact under sampling.
	setTimeAnatomyCap(t, &timeAnatomyInstanceK, 8)
	setTimeAnatomyCap(t, &timeAnatomyInstanceKMin, 8)
	entry = taEntry(t, timeAnatomyForTest(t, events), "Checkout")
	for _, rep := range entry.Representatives {
		if rep.Reason == "slowest" && (rep.DurationMs != 540 || rep.Ordinal != 951*3+2) {
			t.Fatalf("sampled slowest = %+v", rep)
		}
		if (rep.Reason == "p50" || rep.Reason == "p95") && !rep.Sampled {
			t.Fatalf("sampled %s not marked sampled", rep.Reason)
		}
	}
}

func TestTimeAnatomyLegacyEventIDsResolveThroughPaging(t *testing.T) {
	events := taMainFixture()[:60*3]
	record := writeArchiveTransactionsForTest(t, events)
	for _, name := range []string{"resources.jsonl", "interval_summaries.jsonl"} {
		if err := os.WriteFile(filepath.Join(record.ArchivePath, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	report, err := buildRunArchiveReport(record)
	if err != nil {
		t.Fatal(err)
	}
	items, total, err := loadArchiveTransactions(record, transactionEventFilter{}, archivePage{Limit: 500})
	if err != nil || total != len(events) {
		t.Fatalf("paged load: total %d err %v", total, err)
	}
	paged := map[string]int{}
	for index, item := range items {
		paged[item.EventID] = index
	}
	entry := taEntry(t, report.TimeAnatomy, "Checkout")
	if len(entry.Representatives) == 0 {
		t.Fatal("no representatives")
	}
	for _, rep := range entry.Representatives {
		index, ok := paged[rep.EventID]
		if !ok || int64(index) != rep.Ordinal {
			t.Fatalf("representative %s (ordinal %d) resolves to %d/%v through paging", rep.EventID, rep.Ordinal, index, ok)
		}
	}
	// The streaming pass and the paged loader derive the same ids.
	ordinal := 0
	if err := forEachArchiveTransaction(record, func(tx *TransactionEvent) error {
		if items[ordinal].EventID != tx.EventID {
			return fmt.Errorf("ordinal %d: stream %s, page %s", ordinal, tx.EventID, items[ordinal].EventID)
		}
		ordinal++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestTimeAnatomyBounds(t *testing.T) {
	setTimeAnatomyCap(t, &maxTimeAnatomyTransactions, 3)
	setTimeAnatomyCap(t, &maxTimeAnatomyReportTransactions, 2)
	setTimeAnatomyCap(t, &maxTimeAnatomyRowsPerTransaction, 2)
	setTimeAnatomyCap(t, &maxTimeAnatomyPendingPerVUser, 2)
	var events []TransactionEvent
	for index, name := range []string{"N1", "N2", "N3", "N4"} {
		start := float64(index) * 1000
		iteration := int64(index * 2) // a gap, so each iteration flushes the last
		events = append(events,
			taRequest(1, 11, iteration, start, 10, "A", 1),
			taRequest(1, 11, iteration, start+10, 10, "B", 2),
			taTransaction(1, 11, iteration, start, 30, name))
	}
	// Three requests in one transaction against a row cap of 2 and a pending
	// cap of 2.
	events = append(events,
		taRequest(2, 22, 1, 0, 10, "A", 1),
		taRequest(2, 22, 1, 10, 10, "B", 2),
		taRequest(2, 22, 1, 20, 10, "C", 3),
		taTransaction(2, 22, 1, 0, 30, "N1"))
	anatomy := timeAnatomyForTest(t, events)
	if len(anatomy.Transactions) != 2 || anatomy.Truncation.TransactionsTracked != 3 || anatomy.Truncation.TransactionsNotEmitted != 1 || anatomy.Truncation.TransactionsOmitted != 1 {
		t.Fatalf("truncation = %+v (%d emitted)", anatomy.Truncation, len(anatomy.Transactions))
	}
	if anatomy.Truncation.PendingRequestsDropped != 1 {
		t.Fatalf("pending dropped = %d, want 1", anatomy.Truncation.PendingRequestsDropped)
	}
	for _, reason := range []string{timeAnatomyReasonTransactionCap, timeAnatomyReasonReportTransactionCap, timeAnatomyReasonPendingKeyCap} {
		if !slices.Contains(anatomy.Truncation.Reasons, reason) {
			t.Fatalf("reasons %v missing %s", anatomy.Truncation.Reasons, reason)
		}
	}
	if !slices.IsSorted(anatomy.Truncation.Reasons) || len(slices.Compact(slices.Clone(anatomy.Truncation.Reasons))) != len(anatomy.Truncation.Reasons) {
		t.Fatalf("reasons not sorted and unique: %v", anatomy.Truncation.Reasons)
	}

	// Row cap: a third distinct row goes to other_requests.
	setTimeAnatomyCap(t, &maxTimeAnatomyPendingPerVUser, 4096)
	anatomy = timeAnatomyForTest(t, events)
	var withOther *RunArchiveTransactionAnatomy
	for index := range anatomy.Transactions {
		if anatomy.Transactions[index].OtherRequests != nil {
			withOther = &anatomy.Transactions[index]
		}
	}
	if withOther == nil || !withOther.RequestsTruncated || withOther.OtherRequests.Samples != 1 || !slices.Contains(anatomy.Truncation.Reasons, timeAnatomyReasonRequestRowCap) || anatomy.Truncation.RequestRowsOmitted != 1 {
		t.Fatalf("row cap: %+v reasons %v", withOther, anatomy.Truncation.Reasons)
	}

	// Global pending cap.
	setTimeAnatomyCap(t, &maxTimeAnatomyPendingTotal, 1)
	anatomy = timeAnatomyForTest(t, events)
	if !slices.Contains(anatomy.Truncation.Reasons, timeAnatomyReasonPendingGlobalCap) || anatomy.Truncation.PendingRequestsDropped == 0 {
		t.Fatalf("global cap: %+v", anatomy.Truncation)
	}
}

func TestTimeAnatomyDeterminism(t *testing.T) {
	setTimeAnatomyCap(t, &timeAnatomyInstanceK, 64)
	events := taMainFixture()
	first, _ := json.Marshal(timeAnatomyForTest(t, events))
	second, _ := json.Marshal(timeAnatomyForTest(t, events))
	if !bytes.Equal(first, second) {
		t.Fatal("two builds differ")
	}
}

func TestTimeAnatomyAbsentWithoutTransactions(t *testing.T) {
	report := buildTimeAnatomyReportForTest(t, []TransactionEvent{taRequest(1, 11, 1, 0, 10, "A", 1)})
	if report.TimeAnatomy != nil {
		t.Fatalf("time_anatomy = %+v, want nil without business transactions", report.TimeAnatomy)
	}
}

func TestTimeAnatomySLAStatus(t *testing.T) {
	manifest := &RunArchiveManifest{
		TransactionRuleResults: []TransactionSLARuleResult{
			{Rule: runcontract.TransactionSLARule{Transaction: "A"}, Status: "pass"},
			{Rule: runcontract.TransactionSLARule{Transaction: "A"}, Status: "fail"},
			{Rule: runcontract.TransactionSLARule{Transaction: "B"}, Status: "pass"},
			{Rule: runcontract.TransactionSLARule{Transaction: "C"}, Status: "not_evaluated"},
			{Rule: runcontract.TransactionSLARule{Transaction: "D", Population: "action"}, Status: "fail"},
		},
		TransactionResponseTimeSLAResults: []TransactionResponseTimeSLAResult{{Group: "g", Transaction: "E", Status: "fail"}},
	}
	got := timeAnatomySLAStatus(manifest)
	want := map[string]string{"A": "failed", "B": "passed", "E": "failed"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sla status = %v, want %v", got, want)
	}
}

// taJSONKeyPaths lists every object key path in a JSON value; array elements
// share their array's path.
func taJSONKeyPaths(prefix string, value any, out map[string]bool) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			path := key
			if prefix != "" {
				path = prefix + "." + key
			}
			out[path] = true
			taJSONKeyPaths(path, child, out)
		}
	case []any:
		for _, child := range typed {
			taJSONKeyPaths(prefix+"[]", child, out)
		}
	}
}

func TestTimeAnatomyJSONContractKeys(t *testing.T) {
	pct := 1.0
	stat := RunArchivePhaseStat{Samples: 1, AvgMs: 1, P50Ms: 1, P90Ms: 1, P95Ms: 1, P99Ms: 1, MaxMs: 1, ObservedPct: &pct, AvgWhenObservedMs: &pct, PercentileQuality: "approximate"}
	phases := RunArchivePhaseMeans{DNSMs: 1, ConnectMs: 1, TLSMs: 1, ServerMs: 1, DownloadMs: 1, UnsplitMs: 1}
	request := RunArchiveAnatomyRequest{
		Key: "1|A#1", ActionName: "A", SourceStepIndex: 1, Occurrence: 1, OccurrenceFolded: true, DependencyType: "http",
		Samples: 1, Failed: 1, PresencePct: 1, OffsetMs: stat,
		Phases:      RunArchiveAnatomyPhaseStats{DNS: stat, Connect: stat, TLS: stat, Server: stat, Download: stat, Unsplit: stat, Total: stat},
		Connections: RunArchiveConnectionStats{New: 1, Reused: 1, Unknown: 1, Inconsistent: 1, ReusePct: &pct},
	}
	band := RunArchiveTailBand{Count: 1, Joined: 1, Failed: 1, AvgDurationMs: 1, Phases: phases, NonRequestMs: 1, OverlapMs: 1}
	anatomy := RunArchiveTimeAnatomy{
		Schema: timeAnatomySchema, PhaseModel: timeAnatomyPhaseModel, JoinRule: timeAnatomyJoinRule,
		Transactions: []RunArchiveTransactionAnatomy{{
			Name: "T", Instances: 1, JoinedInstances: 1, PartialInstances: 1, FailedInstances: 1, InterruptedInstances: 1, SLAStatus: "failed",
			Duration:          stat,
			Decomposition:     RunArchiveTimeDecomposition{Instances: 1, AvgDurationMs: 1, Phases: phases, NonRequestMs: 1, OverlapMs: 1, AvgRequestsPerInstance: 1},
			Requests:          []RunArchiveAnatomyRequest{request},
			OtherRequests:     &request,
			RequestsTruncated: true,
			Tail: &RunArchiveTailAttribution{
				Status: "ok", ThresholdP50Ms: 1, ThresholdP95Ms: 1, ThresholdQuality: "approximate", BandEstimate: "exact", Instances: 1, SampledInstances: 1,
				Tail: band, Median: band,
				Delta:    RunArchiveTailDelta{TotalMs: 1, Components: []RunArchiveTailComponent{{Component: "server", DeltaMs: 1, SharePct: &pct}}},
				Requests: []RunArchiveRequestDelta{{Key: "k", ActionName: "A", DeltaMs: 1, DominantPhase: "server", DominantMs: 1}},
				Headline: "h",
			},
			Representatives: []RunArchiveRepresentativeSample{{
				Reason: "p95", TargetMs: &pct, EventID: "tx_0", Ordinal: 1, FirstOrdinal: 1, Timestamp: timeAnatomyTestBase, DurationMs: 1, ElapsedMs: 1,
				Success: true, StatusCode: 200, ErrorClass: "e", Group: "g", VUserID: 1, VUserLifetime: 1, Iteration: 1, TraceID: "t", Children: 1, Sampled: true,
			}},
		}},
		Coverage: RunArchiveTimeAnatomyCoverage{TransactionRows: 1, PartialTransactionRows: 1},
		Truncation: RunArchiveTimeAnatomyTruncation{
			Truncated: true, Reasons: []string{"instance_sample"}, TransactionsTracked: 1, TransactionsOmitted: 1, TransactionsNotEmitted: 1, RequestRowsOmitted: 1, PendingRequestsDropped: 1,
			PendingRequestsEvicted: 1, IdentitiesEvicted: 1,
		},
	}
	raw, err := json.Marshal(map[string]any{"time_anatomy": anatomy})
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	paths := map[string]bool{}
	taJSONKeyPaths("", decoded, paths)
	got := make([]string, 0, len(paths))
	for path := range paths {
		got = append(got, path)
	}
	sort.Strings(got)

	statKeys := []string{"samples", "avg_ms", "p50_ms", "p90_ms", "p95_ms", "p99_ms", "max_ms", "observed_pct", "avg_when_observed_ms", "percentile_quality"}
	phaseMeanKeys := []string{"dns_ms", "connect_ms", "tls_ms", "server_ms", "download_ms", "unsplit_ms"}
	var want []string
	add := func(prefix string, keys ...string) {
		for _, key := range keys {
			want = append(want, prefix+"."+key)
		}
	}
	root := "time_anatomy"
	want = append(want, root)
	add(root, "schema", "phase_model", "join_rule", "transactions", "coverage", "truncation")
	add(root+".coverage", "transaction_rows", "joined_transaction_rows", "transaction_rows_without_requests", "unjoinable_transaction_rows", "interrupted_transaction_rows", "request_rows", "requests_in_transactions", "requests_outside_transactions", "requests_claimed_across_iteration", "late_request_rows", "requests_ending_after_transaction", "clamped_request_samples", "retried_request_samples", "legacy_identity_rows", "unknown_population_rows", "partial_transaction_rows")
	add(root+".truncation", "truncated", "reasons", "transactions_tracked", "transactions_omitted", "transactions_not_emitted", "request_rows_omitted", "pending_requests_dropped", "pending_requests_evicted", "identities_evicted")
	tx := root + ".transactions[]"
	add(tx, "name", "instances", "joined_instances", "partial_instances", "failed_instances", "interrupted_instances", "sla_status", "duration", "decomposition", "requests", "other_requests", "requests_truncated", "tail", "representatives")
	add(tx+".duration", statKeys...)
	add(tx+".decomposition", "instances", "avg_duration_ms", "phases", "non_request_ms", "overlap_ms", "avg_requests_per_instance")
	add(tx+".decomposition.phases", phaseMeanKeys...)
	for _, requestPath := range []string{tx + ".requests[]", tx + ".other_requests"} {
		add(requestPath, "key", "action_name", "source_step_index", "occurrence", "occurrence_folded", "dependency_type", "samples", "failed", "presence_pct", "offset_ms", "phases", "connections")
		add(requestPath+".offset_ms", statKeys...)
		add(requestPath+".phases", "dns", "connect", "tls", "server", "download", "unsplit", "total")
		for _, phase := range []string{"dns", "connect", "tls", "server", "download", "unsplit", "total"} {
			add(requestPath+".phases."+phase, statKeys...)
		}
		add(requestPath+".connections", "new", "reused", "unknown", "inconsistent", "reuse_pct")
	}
	add(tx+".tail", "status", "threshold_p50_ms", "threshold_p95_ms", "threshold_quality", "band_estimate", "instances", "sampled_instances", "tail", "median", "delta", "requests", "headline")
	for _, bandPath := range []string{tx + ".tail.tail", tx + ".tail.median"} {
		add(bandPath, "count", "joined", "failed", "avg_duration_ms", "phases", "non_request_ms", "overlap_ms")
		add(bandPath+".phases", phaseMeanKeys...)
	}
	add(tx+".tail.delta", "total_ms", "components")
	add(tx+".tail.delta.components[]", "component", "delta_ms", "share_pct")
	add(tx+".tail.requests[]", "key", "action_name", "delta_ms", "dominant_phase", "dominant_ms")
	add(tx+".representatives[]", "reason", "target_ms", "event_id", "ordinal", "first_ordinal", "timestamp", "duration_ms", "elapsed_ms", "success", "status_code", "error_class", "group", "vuser_id", "vuser_lifetime", "iteration", "trace_id", "children", "sampled")
	sort.Strings(want)
	if !slices.Equal(got, want) {
		missing, extra := []string{}, []string{}
		for _, path := range want {
			if !paths[path] {
				missing = append(missing, path)
			}
		}
		wantSet := map[string]bool{}
		for _, path := range want {
			wantSet[path] = true
		}
		for _, path := range got {
			if !wantSet[path] {
				extra = append(extra, path)
			}
		}
		t.Fatalf("JSON key paths differ from the contract:\nmissing %v\nextra %v", missing, extra)
	}
	if !bytes.Contains(raw, []byte(`"vuser_lifetime":"1"`)) {
		t.Fatalf("vuser_lifetime not a string: %s", raw)
	}
}

func BenchmarkTimeAnatomy2MRows(b *testing.B) {
	// 2,000,000 rows: 500 VUsers, each iteration three requests and one
	// transaction, across 20 transaction names.
	const rows = 2_000_000
	events := make([]TransactionEvent, 0, 4)
	b.ReportAllocs()
	for iteration := 0; iteration < b.N; iteration++ {
		accumulator := newArchiveTimeAnatomyAccumulator()
		named := map[string]*archivePopulationAccumulator{}
		var ordinal int64
		for ordinal < rows {
			cycle := ordinal / 4
			vu := int(cycle % 500)
			iter := cycle / 500
			start := float64(iter) * 2000
			name := fmt.Sprintf("T%02d", vu%20)
			events = events[:0]
			for step := 0; step < 3; step++ {
				request := taRequest(vu+1, uint64(vu+1)*7919, iter, start+float64(step*30), 25+float64(cycle%17), "R"+string(rune('A'+step)), step+1)
				request.DNSMs, request.ConnectMs, request.TTFBMs = 1, float64(cycle%3), 20
				events = append(events, request)
			}
			events = append(events, taTransaction(vu+1, uint64(vu+1)*7919, iter, start, 100+float64(cycle%97), name))
			for index := range events {
				event := &events[index]
				population := archiveEventPopulation(*event)
				if population == "transaction" {
					if named[event.ActionName] == nil {
						named[event.ActionName] = newArchiveGatePopulationAccumulator(newArchiveQuantileBudget())
					}
					named[event.ActionName].add(*event)
				}
				accumulator.merge(event, population, ordinal)
				ordinal++
			}
		}
		runtime.GC()
		var memory runtime.MemStats
		runtime.ReadMemStats(&memory)
		b.ReportMetric(float64(memory.HeapAlloc)/(1<<20), "live-heap-MiB")
		if anatomy := accumulator.finalize(named, nil); anatomy == nil || len(anatomy.Transactions) != 20 {
			b.Fatalf("anatomy = %+v", anatomy)
		}
	}
}
