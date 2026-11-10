package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strconv"
	"testing"
)

// iterationReplayFixture is VUser 1's iteration 1 (Login with think time
// inside it, then a pause, then Browse), after `noise` rows of another VUser
// and before its iteration 2. Rows are in completion order.
func iterationReplayFixture(noise int) []TransactionEvent {
	var events []TransactionEvent
	for index := 0; index < noise; index++ {
		events = append(events, taRequest(3, 99, 1, float64(index), 1, "noise", 1))
	}
	login := taTransaction(1, iterationTestLifetime, 1, 0, 100, "Login")
	login.ElapsedMs = 150
	c := taRequest(1, iterationTestLifetime, 1, 300, 80, "C", 3)
	c.Success, c.StatusCode, c.ErrorMessage = false, 500, "boom"
	events = append(events,
		taRequest(1, iterationTestLifetime, 1, 50, 40, "B", 2),
		taRequest(1, iterationTestLifetime, 1, 0, 40, "A", 1),
		taRequest(2, 42, 1, 10, 10, "X", 1),
		login,
		c,
		taTransaction(1, iterationTestLifetime, 1, 300, 100, "Browse"),
		taRequest(1, iterationTestLifetime, 2, 500, 10, "A", 1),
	)
	return events
}

func iterationQueryForTest(iteration int64) archiveIterationQuery {
	return archiveIterationQuery{group: "g1", vuserID: 1, hasVUserID: true, lifetime: iterationTestLifetime, iteration: iteration}
}

func iterationRowNames(rows []ArchiveWaterfallRow) []string {
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		names = append(names, row.ActionName)
	}
	return names
}

func TestIterationWaterfallRebuildsIterationInStartOrder(t *testing.T) {
	record, _ := waterfallRecordForTest(t, iterationReplayFixture(0))
	waterfall, err := buildArchiveIterationWaterfall(context.Background(), record, iterationQueryForTest(1), 100)
	if err != nil {
		t.Fatal(err)
	}
	if waterfall.Schema != iterationWaterfallSchema || waterfall.IdentityQuality != "lifetime" || waterfall.Iteration != 1 || waterfall.VUserID != 1 ||
		waterfall.FirstOrdinal != 0 || waterfall.LastOrdinal != 5 || waterfall.RowsTotal != 5 || waterfall.Partial || waterfall.Truncated {
		t.Fatalf("header = %+v", waterfall)
	}
	// Sorted by start, then ordinal: A (line 1) before Login (line 3) at 0.
	if names := iterationRowNames(waterfall.Rows); !reflect.DeepEqual(names, []string{"A", "Login", "B", "C", "Browse"}) {
		t.Fatalf("rows = %v", names)
	}
	var offsets []float64
	for _, row := range waterfall.Rows {
		offsets = append(offsets, row.OffsetMs)
	}
	if !reflect.DeepEqual(offsets, []float64{0, 0, 50, 300, 300}) {
		t.Fatalf("offsets = %v", offsets)
	}
	if !waterfall.StartedAt.Equal(taAt(0)) || waterfall.SpanMs != 400 || waterfall.ThinkMsInsideTransactions != 50 {
		t.Fatalf("span %v think %v start %v", waterfall.SpanMs, waterfall.ThinkMsInsideTransactions, waterfall.StartedAt)
	}
	// Covered: [0,150] (Login's elapsed) and [300,400]; 150 ms uncovered.
	if waterfall.GapsMs == nil || *waterfall.GapsMs != 150 {
		t.Fatalf("gaps = %v", waterfall.GapsMs)
	}
	if c := waterfall.Rows[3]; c.Success || c.StatusCode != 500 || c.ErrorMessage != "boom" || c.Population != "action" {
		t.Fatalf("C = %+v", c)
	}
	if login := waterfall.Rows[1]; login.Population != "transaction" || login.Phases.UnsplitMs != 100 || login.ElapsedMs != 150 {
		t.Fatalf("Login = %+v", login)
	}
}

func TestIterationWaterfallHintsBoundTheScan(t *testing.T) {
	events := iterationReplayFixture(20)
	record, report := waterfallRecordForTest(t, events)
	var ref *RunArchiveIterationRef
	for index := range report.Iterations.Representatives {
		candidate := &report.Iterations.Representatives[index]
		if candidate.VUserID == 1 && candidate.Iteration == 1 {
			ref = candidate
		}
	}
	if ref == nil || ref.FirstOrdinal != 20 || ref.LastOrdinal != 25 {
		t.Fatalf("index ref = %+v (all %+v)", ref, report.Iterations.Representatives)
	}
	q := iterationQueryForTest(1)
	q.first, q.last, q.hasHints = ref.FirstOrdinal, ref.LastOrdinal, true
	var waterfall *ArchiveIterationWaterfall
	decoded := countWaterfallDecodes(t, func() {
		var err error
		if waterfall, err = buildArchiveIterationWaterfall(context.Background(), record, q, 100); err != nil {
			t.Fatal(err)
		}
	})
	if len(decoded) == 0 || slices.Min(decoded) < ref.FirstOrdinal {
		t.Fatalf("decoded %v, want only lines from %d", decoded, ref.FirstOrdinal)
	}
	if waterfall.RowsTotal != 5 || waterfall.SpanMs != ref.SpanMs {
		t.Fatalf("hinted waterfall = %+v (ref %+v)", waterfall, ref)
	}
}

func TestIterationWaterfallBadHintsFallBackToFullScan(t *testing.T) {
	record, _ := waterfallRecordForTest(t, iterationReplayFixture(5))
	want, err := buildArchiveIterationWaterfall(context.Background(), record, iterationQueryForTest(1), 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, hints := range [][2]int64{{0, 2}, {6, 7}, {5, 11}, {100, 200}} {
		q := iterationQueryForTest(1)
		q.first, q.last, q.hasHints = hints[0], hints[1], true
		got, err := buildArchiveIterationWaterfall(context.Background(), record, q, 100)
		if err != nil {
			t.Fatalf("hints %v: %v", hints, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("hints %v: got %+v, want %+v", hints, got, want)
		}
	}
}

func TestIterationWaterfallWrongHintsDoNotScanTwice(t *testing.T) {
	record, _ := waterfallRecordForTest(t, iterationReplayFixture(5))
	want, err := buildArchiveIterationWaterfall(context.Background(), record, iterationQueryForTest(1), 100)
	if err != nil {
		t.Fatal(err)
	}
	// Line 0 is another VUser's row: the hinted read stops there, and the
	// fallback decodes each of the iteration's rows once.
	q := iterationQueryForTest(1)
	q.first, q.last, q.hasHints = 0, 999_999_999, true
	var got *ArchiveIterationWaterfall
	decoded := countWaterfallDecodes(t, func() {
		if got, err = buildArchiveIterationWaterfall(context.Background(), record, q, 100); err != nil {
			t.Fatal(err)
		}
	})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	seen := map[int64]bool{}
	for _, ordinal := range decoded {
		if seen[ordinal] {
			t.Fatalf("line %d decoded twice: %v", ordinal, decoded)
		}
		seen[ordinal] = true
	}
	// The hinted pass and the fallback share one budget: a verified first row
	// with a last_ordinal past the end reads the archive once in the hinted
	// pass, and the fallback has only what is left (12 lines, budget 15:
	// it runs out among the other VUser's rows).
	setTimeAnatomyCap(t, &archiveWaterfallScanRecords, 15)
	q.first = 6
	if shared, err := buildArchiveIterationWaterfall(context.Background(), record, q, 100); !isArchiveScanBudget(err) {
		t.Fatalf("second pass got a fresh budget: %+v (%v)", shared, err)
	}
}

func TestIterationWaterfallCacheKeyIgnoresAnchorForLifetime(t *testing.T) {
	record, _ := waterfallRecordForTest(t, iterationReplayFixture(0))
	q := iterationQueryForTest(1)
	base := archiveIterationWaterfallCacheKey(record, q)
	q.anchor, q.hasAnchor = 3, true
	if key := archiveIterationWaterfallCacheKey(record, q); key != base {
		t.Fatalf("lifetime anchor changes the key: %q vs %q", key, base)
	}
	legacy := archiveIterationQuery{group: "g1", vuserID: 5, hasVUserID: true, iteration: 1}
	legacyBase := archiveIterationWaterfallCacheKey(record, legacy)
	legacy.anchor, legacy.hasAnchor = 3, true
	if key := archiveIterationWaterfallCacheKey(record, legacy); key == legacyBase {
		t.Fatal("a legacy anchor must stay in the key: it picks the occurrence")
	}
}

func TestIterationWaterfallLegacyReusedIdentity(t *testing.T) {
	events := []TransactionEvent{
		taRequest(5, 0, 1, 0, 10, "first", 1),   // 0
		taRequest(5, 0, 2, 20, 10, "other", 1),  // 1
		taRequest(5, 0, 1, 40, 10, "reused", 1), // 2: the ID restarted
	}
	record, _ := waterfallRecordForTest(t, events)
	q := archiveIterationQuery{group: "g1", vuserID: 5, hasVUserID: true, iteration: 1}
	waterfall, err := buildArchiveIterationWaterfall(context.Background(), record, q, 100)
	if err != nil {
		t.Fatal(err)
	}
	if waterfall.IdentityQuality != "legacy" || !waterfall.Partial || !reflect.DeepEqual(waterfall.PartialReasons, []string{iterationWaterfallReasonAmbiguousVUser}) ||
		!reflect.DeepEqual(iterationRowNames(waterfall.Rows), []string{"first"}) || waterfall.GapsMs != nil {
		t.Fatalf("legacy = %+v", waterfall)
	}
	q.anchor, q.hasAnchor = 2, true
	anchored, err := buildArchiveIterationWaterfall(context.Background(), record, q, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(iterationRowNames(anchored.Rows), []string{"reused"}) {
		t.Fatalf("anchored = %+v", anchored)
	}
	// Index hints pick the occurrence without ambiguity.
	q = archiveIterationQuery{group: "g1", vuserID: 5, hasVUserID: true, iteration: 1, first: 2, last: 2, hasHints: true}
	hinted, err := buildArchiveIterationWaterfall(context.Background(), record, q, 100)
	if err != nil {
		t.Fatal(err)
	}
	if hinted.Partial || !reflect.DeepEqual(iterationRowNames(hinted.Rows), []string{"reused"}) {
		t.Fatalf("hinted = %+v", hinted)
	}
}

func TestIterationWaterfallRowCapAndScanBudget(t *testing.T) {
	record, _ := waterfallRecordForTest(t, iterationReplayFixture(0))
	capped, err := buildArchiveIterationWaterfall(context.Background(), record, iterationQueryForTest(1), 2)
	if err != nil {
		t.Fatal(err)
	}
	if !capped.Truncated || capped.RowsTotal != 5 || len(capped.Rows) != 2 || capped.GapsMs != nil ||
		!reflect.DeepEqual(iterationRowNames(capped.Rows), []string{"A", "Login"}) || capped.SpanMs != 400 {
		t.Fatalf("capped = %+v", capped)
	}
	// The budget runs out after VUser 1's first rows: a partial result.
	setTimeAnatomyCap(t, &archiveWaterfallScanRecords, 4)
	partial, err := buildArchiveIterationWaterfall(context.Background(), record, iterationQueryForTest(1), 100)
	if err != nil {
		t.Fatal(err)
	}
	if !partial.Partial || !slices.Contains(partial.PartialReasons, iterationWaterfallReasonScanBudget) || partial.GapsMs != nil || partial.RowsTotal >= 5 {
		t.Fatalf("partial = %+v", partial)
	}
}

func serveIterationWaterfallForTest(server *Server, query string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/test-query/archive/iterations/waterfall?"+query, nil)
	req.SetPathValue("id", "test-query")
	rec := httptest.NewRecorder()
	server.handleGetRunArchiveIterationWaterfall(rec, req)
	return rec
}

func TestIterationWaterfallHandlerValidationNotFoundAndCache(t *testing.T) {
	server, _ := waterfallHandlerServer(t, iterationReplayFixture(0))
	life := strconv.FormatUint(iterationTestLifetime, 10)
	base := "group=g1&vuser_id=1&vuser_lifetime=" + life
	for _, tc := range []struct {
		name, query string
		want        int
	}{
		{"no identity", "group=g1&iteration=1", http.StatusBadRequest},
		{"zero vuser only", "vuser_id=0&iteration=1", http.StatusBadRequest},
		{"negative vuser", "vuser_id=-1&iteration=1", http.StatusBadRequest},
		{"bad lifetime", "vuser_lifetime=abc&iteration=1", http.StatusBadRequest},
		{"zero lifetime", "vuser_id=1&vuser_lifetime=0&iteration=1", http.StatusBadRequest},
		{"no iteration", base, http.StatusBadRequest},
		{"zero iteration", base + "&iteration=0", http.StatusBadRequest},
		{"first without last", base + "&iteration=1&first_ordinal=0", http.StatusBadRequest},
		{"first after last", base + "&iteration=1&first_ordinal=3&last_ordinal=2", http.StatusBadRequest},
		{"negative ordinal", base + "&iteration=1&ordinal=-2", http.StatusBadRequest},
		{"max_rows zero", base + "&iteration=1&max_rows=0", http.StatusBadRequest},
		{"max_rows too large", base + "&iteration=1&max_rows=2001", http.StatusBadRequest},
		{"unknown iteration", base + "&iteration=9", http.StatusNotFound},
		{"unknown vuser", "group=g1&vuser_id=77&iteration=1", http.StatusNotFound},
		// VUser 0 is the first VUser; with its lifetime it is addressable.
		{"vuser zero with lifetime", "group=g1&vuser_id=0&vuser_lifetime=" + life + "&iteration=1", http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if rec := serveIterationWaterfallForTest(server, tc.query); rec.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}

	query := base + "&iteration=1&max_rows=3"
	var first *httptest.ResponseRecorder
	firstDecodes := countWaterfallDecodes(t, func() { first = serveIterationWaterfallForTest(server, query) })
	if first.Code != http.StatusOK || len(firstDecodes) == 0 {
		t.Fatalf("first = %d decodes %v: %s", first.Code, firstDecodes, first.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(first.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["schema"] != iterationWaterfallSchema || payload["rows_total"] != float64(5) || payload["truncated"] != true ||
		payload["vuser_lifetime"] != life || len(payload["rows"].([]any)) != 3 {
		t.Fatalf("payload = %s", first.Body.String())
	}
	if _, ok := payload["gaps_ms"]; ok {
		t.Fatalf("truncated replay reports gaps: %s", first.Body.String())
	}
	var second *httptest.ResponseRecorder
	secondDecodes := countWaterfallDecodes(t, func() { second = serveIterationWaterfallForTest(server, base+"&iteration=1") })
	if second.Code != http.StatusOK || len(secondDecodes) != 0 {
		t.Fatalf("second was not served from the cache: %d decodes %v", second.Code, secondDecodes)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &payload); err != nil || len(payload["rows"].([]any)) != 5 || payload["gaps_ms"] != float64(150) {
		t.Fatalf("second = %s", second.Body.String())
	}
	// A miss is remembered.
	missDecodes := countWaterfallDecodes(t, func() {
		if rec := serveIterationWaterfallForTest(server, base+"&iteration=9"); rec.Code != http.StatusNotFound {
			t.Fatalf("miss = %d", rec.Code)
		}
	})
	if len(missDecodes) != 0 {
		t.Fatalf("a remembered miss decoded %v", missDecodes)
	}
}

func TestIterationWaterfallHandlerMapsScanBudgetWithoutRows(t *testing.T) {
	server, _ := waterfallHandlerServer(t, iterationReplayFixture(20))
	setTimeAnatomyCap(t, &archiveWaterfallScanRecords, 5)
	rec := serveIterationWaterfallForTest(server, fmt.Sprintf("group=g1&vuser_id=1&vuser_lifetime=%d&iteration=1", iterationTestLifetime))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413: %s", rec.Code, rec.Body.String())
	}
}

func TestIterationWaterfallByVUserIDSplitsLifetimes(t *testing.T) {
	events := []TransactionEvent{
		taRequest(7, 111, 1, 0, 10, "gen-a", 1),
		taRequest(7, 111, 2, 20, 10, "gen-a-next", 1),
		// Another generator's VUser 7: a different lifetime, iteration 1 again.
		taRequest(7, 222, 1, 0, 10, "gen-b", 1),
		taRequest(7, 222, 1, 15, 10, "gen-b-2", 2),
	}
	record, _ := waterfallRecordForTest(t, events)
	q := archiveIterationQuery{group: "g1", vuserID: 7, hasVUserID: true, iteration: 1}
	waterfall, err := buildArchiveIterationWaterfall(context.Background(), record, q, 100)
	if err != nil {
		t.Fatal(err)
	}
	if waterfall.IdentityQuality != "lifetime" || waterfall.VUserLifetime != 111 || !waterfall.Partial ||
		!reflect.DeepEqual(waterfall.PartialReasons, []string{iterationWaterfallReasonAmbiguousVUser}) || !reflect.DeepEqual(iterationRowNames(waterfall.Rows), []string{"gen-a"}) {
		t.Fatalf("by VUser ID = %+v", waterfall)
	}
	q.anchor, q.hasAnchor = 3, true
	anchored, err := buildArchiveIterationWaterfall(context.Background(), record, q, 100)
	if err != nil {
		t.Fatal(err)
	}
	if anchored.VUserLifetime != 222 || !reflect.DeepEqual(iterationRowNames(anchored.Rows), []string{"gen-b", "gen-b-2"}) {
		t.Fatalf("anchored = %+v", anchored)
	}
	// With the lifetime there is nothing to disambiguate.
	exact, err := buildArchiveIterationWaterfall(context.Background(), record, archiveIterationQuery{group: "g1", lifetime: 222, iteration: 1}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if exact.Partial || exact.RowsTotal != 2 || exact.VUserID != 7 {
		t.Fatalf("exact = %+v", exact)
	}
}

func TestIterationWaterfallThinkTimeCountsNestedTransactionsOnce(t *testing.T) {
	// Checkout (elapsed 9 s, duration 5 s) holds Pay (elapsed 5 s, duration
	// 1 s): all 4 s of think time is inside Pay, and Checkout's elapsed holds it.
	pay := taTransaction(1, iterationTestLifetime, 1, 1000, 1000, "Pay")
	pay.ElapsedMs = 5000
	checkout := taTransaction(1, iterationTestLifetime, 1, 0, 5000, "Checkout")
	checkout.ElapsedMs = 9000
	// A later sibling with its own think time counts on its own.
	logout := taTransaction(1, iterationTestLifetime, 1, 9500, 100, "Logout")
	logout.ElapsedMs = 400
	record, _ := waterfallRecordForTest(t, []TransactionEvent{pay, checkout, logout})
	waterfall, err := buildArchiveIterationWaterfall(context.Background(), record, iterationQueryForTest(1), 100)
	if err != nil {
		t.Fatal(err)
	}
	if waterfall.ThinkMsInsideTransactions != 4300 {
		t.Fatalf("think = %v, want 4300 (4000 inside Checkout once, 300 in Logout)", waterfall.ThinkMsInsideTransactions)
	}
}

func TestIterationWaterfallLegacyLateRowStaysInItsIteration(t *testing.T) {
	events := []TransactionEvent{
		taRequest(5, 0, 4, 0, 10, "four-a", 1),  // 0
		taRequest(5, 0, 5, 20, 10, "five-a", 1), // 1: iteration 5 opens
		taRequest(5, 0, 4, 10, 10, "four-b", 2), // 2: late row of iteration 4
		taRequest(5, 0, 5, 40, 10, "five-b", 2), // 3
		taRequest(5, 0, 6, 60, 10, "six-a", 1),  // 4: iteration 6 opens
		taRequest(5, 0, 5, 50, 10, "five-c", 3), // 5: late row of iteration 5
		taRequest(5, 0, 6, 80, 10, "six-b", 2),  // 6
	}
	record, _ := waterfallRecordForTest(t, events)
	index := iterationIndexFromEvents(events)
	var hints RunArchiveIterationRef
	for _, ref := range index.Representatives {
		if ref.Iteration == 5 {
			hints = ref
		}
	}
	// five-c (ordinal 5) arrived after iteration 6 opened: it joins
	// iteration 5's record, so the hints cover it.
	if index.IterationsTotal != 3 || hints.FirstOrdinal != 1 || hints.LastOrdinal != 5 || hints.RequestRows != 3 {
		t.Fatalf("index = %+v", index)
	}
	want := []string{"five-a", "five-b", "five-c"}
	full := archiveIterationQuery{group: "g1", vuserID: 5, hasVUserID: true, iteration: 5}
	for name, q := range map[string]archiveIterationQuery{
		"full":   full,
		"hinted": {group: "g1", vuserID: 5, hasVUserID: true, iteration: 5, first: hints.FirstOrdinal, last: hints.LastOrdinal, hasHints: true},
	} {
		waterfall, err := buildArchiveIterationWaterfall(context.Background(), record, q, 100)
		if err != nil {
			t.Fatal(name, err)
		}
		if waterfall.Partial || !reflect.DeepEqual(iterationRowNames(waterfall.Rows), want) {
			t.Fatalf("%s: partial %v %v rows %v", name, waterfall.Partial, waterfall.PartialReasons, iterationRowNames(waterfall.Rows))
		}
	}
	// Iteration 4 keeps its late row too.
	four, err := buildArchiveIterationWaterfall(context.Background(), record, archiveIterationQuery{group: "g1", vuserID: 5, hasVUserID: true, iteration: 4}, 100)
	if err != nil || four.Partial || !reflect.DeepEqual(iterationRowNames(four.Rows), []string{"four-a", "four-b"}) {
		t.Fatalf("iteration 4 = %+v (%v)", four, err)
	}
}

func TestIterationWaterfallThinkTimeStateIsBounded(t *testing.T) {
	// One iteration that loops inside the action: many sequential
	// transactions, each with 1 ms of think time, in completion order.
	const loops = 3 * archiveIterationThinkCapFloor
	sequential := newArchiveIterationCollector(2)
	for index := 0; index < loops; index++ {
		tx := taTransaction(1, iterationTestLifetime, 1, float64(index*10), 5, "Loop")
		tx.ElapsedMs = 6
		sequential.add(int64(index), &tx)
		if len(sequential.txs) >= sequential.thinkCap || len(sequential.entries) >= 2*sequential.maxRows {
			t.Fatalf("row %d: think windows %d (cap %d), entries %d", index, len(sequential.txs), sequential.thinkCap, len(sequential.entries))
		}
	}
	result := sequential.result(iterationQueryForTest(1))
	if math.Abs(result.ThinkMsInsideTransactions-loops) > 1e-6 || !result.ThinkMsApproximate || result.RowsTotal != loops || len(result.Rows) != 2 {
		t.Fatalf("think %v approx %v total %d rows %d", result.ThinkMsInsideTransactions, result.ThinkMsApproximate, result.RowsTotal, len(result.Rows))
	}

	// Many transactions nested in one outer row compact without loss: only
	// the outer row's think time counts, and the total stays exact.
	nested := newArchiveIterationCollector(2)
	for index := 0; index < loops; index++ {
		child := taTransaction(1, iterationTestLifetime, 1, float64(1+index), 0.5, "Child")
		child.ElapsedMs = 0.75
		nested.add(int64(index), &child)
	}
	outer := taTransaction(1, iterationTestLifetime, 1, 0, float64(loops+10), "Outer")
	outer.ElapsedMs = float64(loops + 1010)
	nested.add(loops, &outer)
	if len(nested.txs) >= nested.thinkCap {
		t.Fatalf("nested think windows %d (cap %d)", len(nested.txs), nested.thinkCap)
	}
	if got := nested.result(iterationQueryForTest(1)); got.ThinkMsInsideTransactions != 1000 || got.ThinkMsApproximate {
		t.Fatalf("nested think %v approx %v, want 1000 exact", got.ThinkMsInsideTransactions, got.ThinkMsApproximate)
	}
}
