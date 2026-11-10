package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"runtime"
	"slices"
	"testing"
	"time"
)

// taFeeder drives the accumulator directly, the way buildRunArchiveReport
// does, and records the high-water marks of its bounded state.
type taFeeder struct {
	acc           *archiveTimeAnatomyAccumulator
	named         map[string]*archivePopulationAccumulator
	ordinal       int64
	maxPending    int
	maxIdentities int
}

func newTAFeeder() *taFeeder {
	return &taFeeder{acc: newArchiveTimeAnatomyAccumulator(), named: map[string]*archivePopulationAccumulator{}}
}

func (f *taFeeder) feed(events ...TransactionEvent) {
	for index := range events {
		event := &events[index]
		population := archiveEventPopulation(*event)
		if population == "transaction" {
			if f.named[event.ActionName] == nil {
				f.named[event.ActionName] = newArchiveGatePopulationAccumulator(newArchiveQuantileBudget())
			}
			f.named[event.ActionName].add(*event)
		}
		f.acc.merge(event, population, f.ordinal)
		f.ordinal++
		f.maxPending = max(f.maxPending, f.acc.pendingTotal)
		f.maxIdentities = max(f.maxIdentities, len(f.acc.identities))
	}
}

func (f *taFeeder) finalize(t testing.TB) *RunArchiveTimeAnatomy {
	t.Helper()
	anatomy := f.acc.finalize(f.named, nil)
	if anatomy == nil {
		t.Fatal("time_anatomy is nil")
	}
	return anatomy
}

// taOpenWorkload feeds arrivals single-iteration VUser lifetimes, two at a
// time interleaved, each with requests requests inside one transaction
// written right after them. VUser IDs are reused, lifetimes never are.
func taOpenWorkload(f *taFeeder, arrivals, requests int, endsIteration bool) {
	for pair := 0; pair < arrivals; pair += 2 {
		var txs []TransactionEvent
		var rows [][]TransactionEvent
		for arrival := pair; arrival < min(pair+2, arrivals); arrival++ {
			vu, life := 1+arrival%500, uint64(arrival+1)*2654435761
			start := float64(arrival) * 20
			var arrivalRows []TransactionEvent
			for step := 0; step < requests; step++ {
				arrivalRows = append(arrivalRows, taRequest(vu, life, 1, start+float64(step*10), 8, fmt.Sprintf("R%d", step), step+1))
			}
			rows = append(rows, arrivalRows)
			tx := taTransaction(vu, life, 1, start, float64(requests*10), "Arrival")
			tx.EndsIteration = endsIteration
			txs = append(txs, tx)
		}
		for step := 0; step < requests; step++ {
			for _, arrivalRows := range rows {
				f.feed(arrivalRows[step])
			}
		}
		f.feed(txs...)
	}
}

func assertNoJoinLoss(t *testing.T, anatomy *RunArchiveTimeAnatomy) {
	t.Helper()
	for _, reason := range []string{timeAnatomyReasonPendingGlobalCap, timeAnatomyReasonPendingKeyCap} {
		if slices.Contains(anatomy.Truncation.Reasons, reason) {
			t.Fatalf("reasons %v include %s", anatomy.Truncation.Reasons, reason)
		}
	}
	if anatomy.Truncation.PendingRequestsDropped != 0 || anatomy.Coverage.PartialTransactionRows != 0 {
		t.Fatalf("dropped %d partial rows %d, want none", anatomy.Truncation.PendingRequestsDropped, anatomy.Coverage.PartialTransactionRows)
	}
}

// An open workload makes every arrival a new VUser lifetime that is never
// seen again: the buffer must release finished lifetimes instead of
// saturating, so every arrival stays fully joined.
func TestTimeAnatomyOpenWorkloadStaysBoundedAndJoined(t *testing.T) {
	const arrivals, requests = 25_000, 8
	for _, test := range []struct {
		name          string
		identities    int
		endsIteration bool
	}{
		{name: "global cap", identities: 65536},
		{name: "global cap with ends_iteration", identities: 65536, endsIteration: true},
		{name: "identity cap", identities: 1000},
	} {
		t.Run(test.name, func(t *testing.T) {
			setTimeAnatomyCap(t, &maxTimeAnatomyIdentities, test.identities)
			feeder := newTAFeeder()
			taOpenWorkload(feeder, arrivals, requests, test.endsIteration)
			if feeder.maxPending > maxTimeAnatomyPendingTotal || feeder.maxIdentities > maxTimeAnatomyIdentities {
				t.Fatalf("high water: pending %d (cap %d), identities %d (cap %d)", feeder.maxPending, maxTimeAnatomyPendingTotal, feeder.maxIdentities, maxTimeAnatomyIdentities)
			}
			anatomy := feeder.finalize(t)
			entry := taEntry(t, anatomy, "Arrival")
			if entry.Instances != arrivals || entry.JoinedInstances != arrivals || entry.PartialInstances != 0 || entry.Decomposition.Instances != arrivals || entry.Decomposition.AvgRequestsPerInstance != requests {
				t.Fatalf("arrival = instances %d joined %d partial %d decomposition %+v", entry.Instances, entry.JoinedInstances, entry.PartialInstances, entry.Decomposition)
			}
			if anatomy.Coverage.RequestsInTransactions != arrivals*requests || anatomy.Coverage.RequestsOutsideTransactions != 0 || anatomy.Coverage.JoinedTransactionRows != arrivals {
				t.Fatalf("coverage = %+v", anatomy.Coverage)
			}
			assertNoJoinLoss(t, anatomy)
			if test.identities < arrivals && anatomy.Truncation.IdentitiesEvicted == 0 {
				t.Fatalf("identity cap %d never evicted: %+v", test.identities, anatomy.Truncation)
			}
			if test.identities >= arrivals && anatomy.Truncation.PendingRequestsEvicted+feeder.acc.evictedClaimed == 0 {
				t.Fatalf("global cap never evicted: %+v", anatomy.Truncation)
			}
			// Every evicted request had been claimed by its arrival, so none
			// is reported as of unknown placement.
			if anatomy.Truncation.PendingRequestsEvicted != 0 {
				t.Fatalf("pending_requests_evicted = %d, want 0 (every evicted request was claimed)", anatomy.Truncation.PendingRequestsEvicted)
			}
		})
	}
}

// A closed workload whose current and previous iterations exceed the global
// cap gives up the retired previous-iteration requests, not new ones.
func TestTimeAnatomyClosedWorkloadEvictsRetiredRequestsFirst(t *testing.T) {
	setTimeAnatomyCap(t, &maxTimeAnatomyPendingTotal, 30)
	feeder := newTAFeeder()
	const vusers, iterations = 10, 6
	for iteration := int64(1); iteration <= iterations; iteration++ {
		for vu := 1; vu <= vusers; vu++ {
			start := float64(iteration)*1000 + float64(vu)
			feeder.feed(
				taRequest(vu, uint64(vu)*7, iteration, start, 10, "A", 1),
				taRequest(vu, uint64(vu)*7, iteration, start+10, 10, "B", 2),
				taTransaction(vu, uint64(vu)*7, iteration, start, 30, "T"),
			)
		}
	}
	anatomy := feeder.finalize(t)
	entry := taEntry(t, anatomy, "T")
	if entry.JoinedInstances != vusers*iterations || entry.Decomposition.AvgRequestsPerInstance != 2 {
		t.Fatalf("joined %d of %d, decomposition %+v", entry.JoinedInstances, entry.Instances, entry.Decomposition)
	}
	assertNoJoinLoss(t, anatomy)
	if feeder.acc.evictedClaimed == 0 || anatomy.Truncation.PendingRequestsEvicted != 0 || feeder.maxPending > 30 {
		t.Fatalf("evicted claimed %d unclaimed %d, max pending %d", feeder.acc.evictedClaimed, anatomy.Truncation.PendingRequestsEvicted, feeder.maxPending)
	}
}

// A live VUser evicted under pressure comes back with an enclosing
// transaction: that instance lost its children, so it is partial, not joined,
// and its missing request time is not reported as time outside requests.
func TestTimeAnatomyEvictedLiveIdentityIsPartial(t *testing.T) {
	setTimeAnatomyCap(t, &maxTimeAnatomyPendingTotal, 16)
	feeder := newTAFeeder()
	feeder.feed(
		taRequest(1, 1, 1, 0, 10, "A", 1),
		taRequest(1, 1, 1, 10, 10, "B", 2),
		taRequest(1, 1, 1, 20, 10, "C", 3),
	)
	taOpenWorkload(feeder, 20, 3, false)
	feeder.feed(taTransaction(1, 1, 1, 0, 100, "Outer"))
	anatomy := feeder.finalize(t)
	outer := taEntry(t, anatomy, "Outer")
	if outer.Instances != 1 || outer.JoinedInstances != 0 || outer.PartialInstances != 1 || outer.Decomposition.Instances != 0 {
		t.Fatalf("outer = instances %d joined %d partial %d decomposition %+v", outer.Instances, outer.JoinedInstances, outer.PartialInstances, outer.Decomposition)
	}
	if anatomy.Coverage.PartialTransactionRows != 1 || !slices.Contains(anatomy.Truncation.Reasons, timeAnatomyReasonPendingGlobalCap) {
		t.Fatalf("coverage %+v truncation %+v", anatomy.Coverage, anatomy.Truncation)
	}
	if arrival := taEntry(t, anatomy, "Arrival"); arrival.JoinedInstances != 20 || arrival.PartialInstances != 0 {
		t.Fatalf("arrivals joined %d partial %d, want 20/0", arrival.JoinedInstances, arrival.PartialInstances)
	}
}

// A request dropped by the per-identity cap makes every transaction that
// could have claimed it partial; a later transaction past the drop is joined.
func TestTimeAnatomyKeyCapDropMarksPartial(t *testing.T) {
	setTimeAnatomyCap(t, &maxTimeAnatomyPendingPerVUser, 2)
	events := []TransactionEvent{
		taRequest(1, 11, 1, 0, 10, "A", 1),
		taRequest(1, 11, 1, 10, 10, "B", 2),
		taRequest(1, 11, 1, 20, 10, "C", 3),
		taTransaction(1, 11, 1, 0, 30, "T"),
		taRequest(1, 11, 1, 100, 10, "D", 4),
		taTransaction(1, 11, 1, 100, 10, "T"),
	}
	anatomy := timeAnatomyForTest(t, events)
	entry := taEntry(t, anatomy, "T")
	if entry.Instances != 2 || entry.JoinedInstances != 1 || entry.PartialInstances != 1 || entry.Decomposition.Instances != 1 {
		t.Fatalf("entry = instances %d joined %d partial %d decomposition %+v", entry.Instances, entry.JoinedInstances, entry.PartialInstances, entry.Decomposition)
	}
	if entry.Decomposition.NonRequestMs != 0 || entry.Decomposition.AvgRequestsPerInstance != 1 {
		t.Fatalf("decomposition %+v, want only the complete instance", entry.Decomposition)
	}
	if anatomy.Coverage.PartialTransactionRows != 1 || anatomy.Coverage.JoinedTransactionRows != 1 || !slices.Contains(anatomy.Truncation.Reasons, timeAnatomyReasonPendingKeyCap) {
		t.Fatalf("coverage %+v reasons %v", anatomy.Coverage, anatomy.Truncation.Reasons)
	}
}

// Partial instances count as unjoined members of their tail band.
func TestTimeAnatomyPartialInstancesLowerTailJoinCoverage(t *testing.T) {
	setTimeAnatomyCap(t, &maxTimeAnatomyPendingPerVUser, 1)
	tail := taEntry(t, timeAnatomyForTest(t, taMainFixture()), "Checkout").Tail
	if tail.Status != "low_join_coverage" && tail.Status != "insufficient_samples" {
		t.Fatalf("tail status %q with every instance partial", tail.Status)
	}
	if tail.Headline != "" || tail.Tail.Joined != 0 || tail.Median.Joined != 0 {
		t.Fatalf("tail = %+v, want no joined members", tail)
	}
}

// taBufferWorkload is one identity that never changes iteration, so its
// buffer stays full: requests requests, one transaction per 64.
func taBufferWorkload(requests int) time.Duration {
	accumulator := newArchiveTimeAnatomyAccumulator()
	events := make([]TransactionEvent, 0, 65)
	started := time.Now()
	var ordinal int64
	for first := 0; first < requests; first += 64 {
		events = events[:0]
		for index := first; index < first+64; index++ {
			events = append(events, taRequest(1, 11, 1, float64(index), 0.5, "R", 1))
		}
		events = append(events, taTransaction(1, 11, 1, float64(first), 64, "T"))
		for index := range events {
			accumulator.merge(&events[index], archiveEventPopulation(events[index]), ordinal)
			ordinal++
		}
	}
	return time.Since(started)
}

// A full identity buffer costs O(1) per request and O(log n + claims) per
// transaction, so the per-identity cap does not change the cost.
func TestTimeAnatomyFullBufferCostIsFlat(t *testing.T) {
	const requests = 40_000
	measure := func(limit int) time.Duration {
		setTimeAnatomyCap(t, &maxTimeAnatomyPendingPerVUser, limit)
		best := time.Duration(1 << 62)
		for range 3 {
			best = min(best, taBufferWorkload(requests))
		}
		return best
	}
	small, large := measure(256), measure(16384)
	if large > 8*small+20*time.Millisecond {
		t.Fatalf("full buffer cost grows with the cap: cap 256 %v, cap 16384 %v", small, large)
	}
}

func TestTimeAnatomyClaimRule(t *testing.T) {
	const ms = int64(time.Millisecond)
	window := timeAnatomyClaimWindow{StartNs: 100 * ms, EndNs: 200 * ms, Name: "Checkout"}
	cases := []struct {
		name string
		mod  func(*timeAnatomyClaimWindow, *timeAnatomyClaimChild)
		want bool
	}{
		{"inside", func(_ *timeAnatomyClaimWindow, c *timeAnatomyClaimChild) { c.StartNs = 150 * ms }, true},
		{"at start", func(_ *timeAnatomyClaimWindow, c *timeAnatomyClaimChild) { c.StartNs = 100 * ms }, true},
		{"end slack", func(_ *timeAnatomyClaimWindow, c *timeAnatomyClaimChild) { c.StartNs = 201 * ms }, true},
		{"after end slack", func(_ *timeAnatomyClaimWindow, c *timeAnatomyClaimChild) { c.StartNs = 203 * ms }, false},
		{"before start slack", func(_ *timeAnatomyClaimWindow, c *timeAnatomyClaimChild) { c.StartNs = 97 * ms; c.DurationMs = 50 }, false},
		{"previous sibling ends at S", func(_ *timeAnatomyClaimWindow, c *timeAnatomyClaimChild) { c.StartNs = 99 * ms; c.DurationMs = 1 }, false},
		{"previous sibling ends before S", func(_ *timeAnatomyClaimWindow, c *timeAnatomyClaimChild) {
			c.StartNs = 98*ms + ms/2
			c.DurationMs = 0.8
		}, false},
		{"slack request still running at S", func(_ *timeAnatomyClaimWindow, c *timeAnatomyClaimChild) { c.StartNs = 99 * ms; c.DurationMs = 5 }, true},
		{"hint names another transaction", func(_ *timeAnatomyClaimWindow, c *timeAnatomyClaimChild) {
			c.StartNs, c.DurationMs, c.SourceTransaction = 99*ms, 5, "Login"
		}, false},
		{"hint names this transaction", func(_ *timeAnatomyClaimWindow, c *timeAnatomyClaimChild) {
			c.StartNs, c.DurationMs, c.SourceTransaction = 99*ms, 0.5, " checkout "
		}, true},
		{"scrubbed hint falls back to the end test", func(_ *timeAnatomyClaimWindow, c *timeAnatomyClaimChild) {
			c.StartNs, c.DurationMs, c.SourceTransaction = 99*ms, 5, "[REDACTED]"
		}, true},
		{"scrubbed window name falls back to the end test", func(w *timeAnatomyClaimWindow, c *timeAnatomyClaimChild) {
			w.Name = "GET /[REDACTED]"
			c.StartNs, c.DurationMs, c.SourceTransaction = 99*ms, 0.5, "Checkout"
		}, false},
		{"hint never gates a request inside", func(_ *timeAnatomyClaimWindow, c *timeAnatomyClaimChild) {
			c.StartNs, c.SourceTransaction = 150*ms, "Inner"
		}, true},
	}
	for _, test := range cases {
		w, child := window, timeAnatomyClaimChild{DurationMs: 10}
		test.mod(&w, &child)
		if got := timeAnatomyClaimsRequest(w, child); got != test.want {
			t.Errorf("%s: claims = %v, want %v", test.name, got, test.want)
		}
	}
}

// A fast previous sibling that ended just before the next transaction began
// is not claimed by it.
func TestTimeAnatomySequentialSiblingNotClaimed(t *testing.T) {
	events := []TransactionEvent{
		taRequest(1, 11, 1, 0, 0.8, "Fast", 1),
		taTransaction(1, 11, 1, 0, 0.8, "A"),
		taRequest(1, 11, 1, 1.5, 10, "Next", 2),
		taTransaction(1, 11, 1, 1.5, 10, "B"),
	}
	anatomy := timeAnatomyForTest(t, events)
	b := taEntry(t, anatomy, "B")
	if len(b.Requests) != 1 || b.Requests[0].ActionName != "Next" || b.Decomposition.OverlapMs != 0 {
		t.Fatalf("B = rows %+v decomposition %+v, want only Next", b.Requests, b.Decomposition)
	}
	if a := taEntry(t, anatomy, "A"); len(a.Requests) != 1 || a.Requests[0].ActionName != "Fast" {
		t.Fatalf("A rows = %+v", a.Requests)
	}
}

// A representative from a row without a timestamp has no timestamp, not
// 1970-01-01.
func TestTimeAnatomyRepresentativeWithoutTimestamp(t *testing.T) {
	feeder := newTAFeeder()
	timed := taTransaction(1, 11, 1, 0, 20, "T")
	timed.EventID = "tx_timed"
	feeder.feed(taRequest(1, 11, 1, 0, 10, "A", 1), timed)
	untimed := taTransaction(1, 11, 1, 0, 900, "T")
	untimed.Timestamp, untimed.EventID = time.Time{}, "tx_untimed"
	feeder.feed(untimed)
	entry := taEntry(t, feeder.finalize(t), "T")
	var slowest *RunArchiveRepresentativeSample
	for index := range entry.Representatives {
		if entry.Representatives[index].EventID == "tx_untimed" {
			slowest = &entry.Representatives[index]
		}
	}
	if slowest == nil || slowest.DurationMs != 900 || !slowest.Timestamp.IsZero() {
		t.Fatalf("slowest = %+v, want the untimed row without a timestamp", slowest)
	}
	raw, err := json.Marshal(slowest)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(`"timestamp"`)) {
		t.Fatalf("untimed representative JSON carries a timestamp: %s", raw)
	}
	for _, rep := range entry.Representatives {
		if rep.DurationMs == 20 && rep.Timestamp.IsZero() {
			t.Fatalf("timed representative lost its timestamp: %+v", rep)
		}
	}
}

// taSampleFootprint recounts what the instance samples really hold.
func taSampleFootprint(accumulator *archiveTimeAnatomyAccumulator) int64 {
	var total int64
	for _, name := range accumulator.names {
		total += int64(cap(name.sample)) * timeAnatomyInstanceBaseBytes
		for index := range name.sample {
			instance := &name.sample[index]
			total += int64(len(instance.eventID)+len(instance.traceID)) + int64(cap(instance.cells))*timeAnatomyChildBytes
		}
	}
	return total
}

// The instance-sample budget is hard: cells are one per row (not per child),
// capacity is counted, and when every sample is at k_min it shrinks below.
func TestTimeAnatomySampleBudgetIsHard(t *testing.T) {
	setTimeAnatomyCap(t, &timeAnatomyInstanceK, 64)
	setTimeAnatomyCap(t, &timeAnatomyInstanceKMin, 16)
	const names, children = 6, 200
	budget := int64(names) * 6 * (timeAnatomyInstanceBaseBytes + 64)
	setTimeAnatomyCap(t, &timeAnatomySampleBudgetBytes, budget)
	feeder := newTAFeeder()
	for instance := 0; instance < 100; instance++ {
		for name := 0; name < names; name++ {
			vu, life := 1+name, uint64(name+1)*13
			start := float64(instance*names+name) * 1000
			iteration := int64(instance + 1)
			for child := 0; child < children; child++ {
				feeder.feed(taRequest(vu, life, iteration, start+float64(child), 1, "Poll", 1))
			}
			tx := taTransaction(vu, life, iteration, start, children, fmt.Sprintf("N%d", name))
			tx.EventID = fmt.Sprintf("tx_%032x", instance*names+name)
			feeder.feed(tx)
			if feeder.acc.sampleBytes > budget {
				t.Fatalf("instance %d name %d: sample bytes %d over budget %d", instance, name, feeder.acc.sampleBytes, budget)
			}
		}
	}
	if real := taSampleFootprint(feeder.acc); real != feeder.acc.sampleBytes || real > budget {
		t.Fatalf("sample bytes: accounted %d, real %d, budget %d", feeder.acc.sampleBytes, real, budget)
	}
	for _, name := range feeder.acc.names {
		for index := range name.sample {
			// Poll#1..#8 and the folded Poll#9: one cell per row, not one
			// per child.
			if cells := len(name.sample[index].cells); cells != maxTimeAnatomyOccurrence || cells != len(name.rowList) {
				t.Fatalf("%s instance holds %d cells for %d rows", name.name, cells, len(name.rowList))
			}
		}
	}
	anatomy := feeder.finalize(t)
	if !slices.Contains(anatomy.Truncation.Reasons, timeAnatomyReasonSampleBudget) {
		t.Fatalf("reasons = %v", anatomy.Truncation.Reasons)
	}
	for _, entry := range anatomy.Transactions {
		if entry.Tail.SampledInstances >= int64(timeAnatomyInstanceKMin) {
			t.Fatalf("%s kept %d samples, want below k_min to fit the budget", entry.Name, entry.Tail.SampledInstances)
		}
		row := taRow(t, entry, "1|Poll#9")
		if !row.OccurrenceFolded || row.Samples != int64(children-8)*100 {
			t.Fatalf("%s folded row samples = %d", entry.Name, row.Samples)
		}
	}
}

// "Other requests" rows count against maxTimeAnatomyRows.
func TestTimeAnatomyOtherRowsCountAgainstRowCap(t *testing.T) {
	for _, rowCap := range []int{4, 16} {
		t.Run(fmt.Sprint(rowCap), func(t *testing.T) {
			setTimeAnatomyCap(t, &maxTimeAnatomyRows, rowCap)
			setTimeAnatomyCap(t, &maxTimeAnatomyRowsPerTransaction, rowCap)
			var events []TransactionEvent
			for name := 0; name < 4; name++ {
				start := float64(name) * 1000
				for step := 0; step < rowCap+4; step++ {
					events = append(events, taRequest(1, 11, 1, start+float64(step), 1, fmt.Sprintf("S%d", step), step+1))
				}
				events = append(events, taTransaction(1, 11, 1, start, float64(rowCap+4), fmt.Sprintf("N%d", name)))
			}
			anatomy := timeAnatomyForTest(t, events)
			rows := 0
			for _, entry := range anatomy.Transactions {
				rows += len(entry.Requests)
				if entry.OtherRequests != nil {
					rows++
				}
				if !entry.RequestsTruncated {
					t.Fatalf("%s not marked requests_truncated", entry.Name)
				}
			}
			if rows > rowCap {
				t.Fatalf("%d rows (request + other) against a cap of %d", rows, rowCap)
			}
		})
	}
}

// BenchmarkTimeAnatomyOpenWorkload: 200,000 single-iteration arrivals of five
// requests and one transaction (1.2M rows); the buffer stays at its caps.
func BenchmarkTimeAnatomyOpenWorkload(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		feeder := newTAFeeder()
		taOpenWorkload(feeder, 200_000, 5, false)
		runtime.GC()
		var memory runtime.MemStats
		runtime.ReadMemStats(&memory)
		b.ReportMetric(float64(memory.HeapAlloc)/(1<<20), "live-heap-MiB")
		b.ReportMetric(float64(feeder.maxPending), "max-pending")
		b.ReportMetric(float64(feeder.maxIdentities), "max-identities")
		anatomy := feeder.finalize(b)
		if entry := anatomy.Transactions[0]; entry.JoinedInstances != 200_000 || entry.PartialInstances != 0 {
			b.Fatalf("joined %d partial %d", entry.JoinedInstances, entry.PartialInstances)
		}
	}
}
