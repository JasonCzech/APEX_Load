package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
)

func digestSnapshotForTest(durationsMs ...float64) *engine.TDigestSnapshot {
	digest := engine.NewTDigest(200)
	for _, durationMs := range durationsMs {
		digest.Add(durationMs * 1_000_000)
	}
	snapshot := digest.Snapshot()
	return &snapshot
}

func liveMetricsForGroupFilterTest() *LiveMetrics {
	timestamp := time.Date(2026, 6, 10, 10, 0, 0, 0, time.UTC)
	return &LiveMetrics{
		Timestamp:     timestamp,
		ActiveVUsers:  20,
		TPS:           10,
		TargetTPS:     12,
		AvgResponseMs: 250,
		ErrorPct:      10,
		TotalTx:       9,
		TotalErrors:   1,
		P50Ms:         120,
		P90Ms:         350,
		P95Ms:         450,
		P99Ms:         700,
		Resources:     &engine.SystemResources{CPUProcessPct: 55},
		Transactions: map[string]*TxMetrics{
			"CheckoutAction": {Name: "CheckoutAction", TPS: 4, Count: 5, Errors: 1},
			"BrowseAction":   {Name: "BrowseAction", TPS: 3, Count: 3},
		},
		GroupMetrics: map[string]*GroupLiveMetrics{
			"Checkout": {
				Name:           "Checkout",
				VUsers:         12,
				Transactions:   4,
				Errors:         1,
				TPS:            4,
				TargetTPS:      5,
				AvgResponseMs:  160,
				P50Ms:          100,
				P90Ms:          200,
				P95Ms:          220,
				P99Ms:          300,
				DurationDigest: digestSnapshotForTest(100, 140, 160, 200, 260),
			},
			"Browse": {
				Name:           "Browse",
				VUsers:         8,
				Transactions:   3,
				Errors:         0,
				TPS:            3,
				TargetTPS:      4,
				AvgResponseMs:  300,
				P50Ms:          250,
				P90Ms:          420,
				P95Ms:          500,
				P99Ms:          650,
				DurationDigest: digestSnapshotForTest(240, 300, 520),
			},
		},
	}
}

func TestFilterLiveMetricsForGroupsAggregatesSelectedPerformanceMetrics(t *testing.T) {
	filtered := filterLiveMetricsForGroups(liveMetricsForGroupFilterTest(), groupFilterQuery{
		Names:    []string{"Checkout"},
		Explicit: true,
	})

	if filtered.ActiveVUsers != 12 || filtered.TPS != 4 || filtered.TargetTPS != 5 {
		t.Fatalf("selected load fields = vusers %d tps %.1f target %.1f", filtered.ActiveVUsers, filtered.TPS, filtered.TargetTPS)
	}
	if filtered.TotalTx != 4 || filtered.TotalErrors != 1 || filtered.ErrorPct != 20 {
		t.Fatalf("selected error fields = tx %d errors %d pct %.1f", filtered.TotalTx, filtered.TotalErrors, filtered.ErrorPct)
	}
	if filtered.AvgResponseMs != 160 {
		t.Fatalf("selected avg = %.1f, want 160", filtered.AvgResponseMs)
	}
	if filtered.P95Ms <= 0 || filtered.P95Ms == 450 {
		t.Fatalf("selected P95 = %.1f, want selected group percentile", filtered.P95Ms)
	}
	if len(filtered.GroupMetrics) != 1 || filtered.GroupMetrics["Checkout"] == nil {
		t.Fatalf("selected groups = %#v, want only Checkout", filtered.GroupMetrics)
	}
	if filtered.Transactions != nil {
		t.Fatalf("selected-group transactions = %#v, want omitted because transaction metrics are global", filtered.Transactions)
	}
	if filtered.Resources == nil || filtered.Resources.CPUProcessPct != 55 {
		t.Fatalf("resources were not preserved: %#v", filtered.Resources)
	}
}

func TestParseGroupFilterQueryBlankMeansNoFilter(t *testing.T) {
	cases := []struct {
		name         string
		query        string
		wantExplicit bool
		wantNames    []string
	}{
		{name: "absent", query: "", wantExplicit: false},
		{name: "present blank", query: "groups=", wantExplicit: false},
		{name: "present commas only", query: "groups=,,", wantExplicit: false},
		{name: "real names", query: "groups=Checkout,Search", wantExplicit: true, wantNames: []string{"Checkout", "Search"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			values, err := url.ParseQuery(tc.query)
			if err != nil {
				t.Fatalf("ParseQuery(%q): %v", tc.query, err)
			}
			got := parseGroupFilterQuery(values)
			if got.Explicit != tc.wantExplicit {
				t.Fatalf("Explicit = %v, want %v (blank groups must not filter to nothing)", got.Explicit, tc.wantExplicit)
			}
			if len(got.Names) != len(tc.wantNames) {
				t.Fatalf("Names = %#v, want %#v", got.Names, tc.wantNames)
			}
		})
	}
}

func TestParseGroupFilterQueryRejectsResourceAmplification(t *testing.T) {
	tooMany := make([]string, 0, maxGroupFilterNames+1)
	for index := 0; index <= maxGroupFilterNames; index++ {
		tooMany = append(tooMany, "group-"+strconv.Itoa(index))
	}
	for name, values := range map[string]url.Values{
		"too many names": {"groups": []string{strings.Join(tooMany, ",")}},
		"name too long":  {"groups": []string{strings.Repeat("x", maxGroupFilterNameBytes+1)}},
		"query too large": {"groups": []string{
			strings.Repeat("x", maxGroupFilterQueryBytes),
			"overflow",
		}},
	} {
		t.Run(name, func(t *testing.T) {
			if filter := parseGroupFilterQuery(values); !filter.Invalid {
				t.Fatalf("filter = %#v, want invalid", filter)
			}
		})
	}
}

func TestFilterLiveMetricsForGroupsHandlesEmptySelection(t *testing.T) {
	filtered := filterLiveMetricsForGroups(liveMetricsForGroupFilterTest(), groupFilterQuery{Explicit: true})

	if filtered.ActiveVUsers != 0 || filtered.TPS != 0 || filtered.AvgResponseMs != 0 || filtered.P99Ms != 0 {
		t.Fatalf("empty selection did not clear performance metrics: %#v", filtered)
	}
	if len(filtered.GroupMetrics) != 0 {
		t.Fatalf("empty selection groups = %#v, want none", filtered.GroupMetrics)
	}
	if filtered.Transactions != nil {
		t.Fatalf("empty selection transactions = %#v, want none", filtered.Transactions)
	}
	if filtered.Resources == nil || filtered.Resources.CPUProcessPct != 55 {
		t.Fatalf("resources were not preserved: %#v", filtered.Resources)
	}
}

func TestFilterLiveMetricsHistoryForGroupsCachedInvalidatesOnNewPoint(t *testing.T) {
	const testID = "run-group-filter-cache"
	deleteGroupMetricsHistoryCache(testID)
	t.Cleanup(func() { deleteGroupMetricsHistoryCache(testID) })

	filter := groupFilterQuery{Names: []string{"Checkout"}, Explicit: true}
	first := liveMetricsForGroupFilterTest()
	second := liveMetricsForGroupFilterTest()
	second.Timestamp = first.Timestamp.Add(time.Second)
	second.GroupMetrics["Checkout"].TPS = 9

	initial := filterLiveMetricsHistoryForGroupsCached(testID, []*LiveMetrics{first}, filter)
	if len(initial) != 1 || initial[0].TPS != 4 {
		t.Fatalf("initial cached filter = %#v, want one checkout point with tps 4", initial)
	}

	cachedAgain := filterLiveMetricsHistoryForGroupsCached(testID, []*LiveMetrics{first}, filter)
	if len(cachedAgain) != 1 || cachedAgain[0].TPS != 4 {
		t.Fatalf("cached filter = %#v, want one checkout point with tps 4", cachedAgain)
	}

	updated := filterLiveMetricsHistoryForGroupsCached(testID, []*LiveMetrics{first, second}, filter)
	if len(updated) != 2 || updated[1].TPS != 9 {
		t.Fatalf("updated cached filter = %#v, want second checkout point with tps 9", updated)
	}
}

func TestGroupMetricsHistoryCacheHasPerTestGlobalAndByteBounds(t *testing.T) {
	cache := newGroupMetricsHistoryCacheManager()
	point := []*LiveMetrics{{Timestamp: time.Now(), TPS: 1}}
	for index := 0; index <= maxGroupMetricsHistoryCacheEntriesPerTest; index++ {
		cache.put("one-test", strconv.Itoa(index), point)
	}
	if got := cache.perTest["one-test"]; got != maxGroupMetricsHistoryCacheEntriesPerTest {
		t.Fatalf("per-test cache entries = %d, want %d", got, maxGroupMetricsHistoryCacheEntriesPerTest)
	}
	for index := 0; index <= maxGroupMetricsHistoryCacheEntriesGlobal; index++ {
		cache.put("test-"+strconv.Itoa(index), "key", point)
	}
	if len(cache.entries) > maxGroupMetricsHistoryCacheEntriesGlobal || cache.total > maxGroupMetricsHistoryCacheBytes {
		t.Fatalf("global cache retained entries=%d bytes=%d", len(cache.entries), cache.total)
	}

	oversized := []*LiveMetrics{{ErrorBreakdown: map[string]int64{strings.Repeat("x", maxGroupMetricsHistoryCacheEntryBytes/4): 1}}}
	before := len(cache.entries)
	cache.put("oversized", "key", oversized)
	if len(cache.entries) != before {
		t.Fatal("oversized filtered history was admitted to cache")
	}
}

func TestGroupMetricsHistoryCacheDropsNonResponseEvidence(t *testing.T) {
	const testID = "run-group-filter-evidence-cache"
	deleteGroupMetricsHistoryCache(testID)
	t.Cleanup(func() { deleteGroupMetricsHistoryCache(testID) })
	original := liveMetricsForGroupFilterTest()
	original.ErrorEvidence = []LiveErrorEvidence{{ID: "sensitive-evidence"}}
	filtered := filterLiveMetricsHistoryForGroupsCached(testID, []*LiveMetrics{original}, groupFilterQuery{Names: []string{"Checkout"}, Explicit: true})
	if len(filtered) != 1 || filtered[0] == nil {
		t.Fatalf("filtered history = %#v", filtered)
	}
	if filtered[0].ErrorEvidence != nil || filtered[0].GroupMetrics["Checkout"].DurationDigest != nil {
		t.Fatal("response cache retained non-serialized evidence")
	}
	if filtered[0].Transactions != nil {
		t.Fatalf("response cache retained global transactions: %#v", filtered[0].Transactions)
	}
	if len(original.ErrorEvidence) != 1 || original.GroupMetrics["Checkout"].DurationDigest == nil || len(original.Transactions) != 2 {
		t.Fatal("cache compaction mutated canonical live history")
	}
}

func TestHandleGetMetricsAppliesGroupFilter(t *testing.T) {
	server := NewServer(ServerConfig{})
	live := liveMetricsForGroupFilterTest()
	server.runner.mu.Lock()
	server.runner.tests["run-group-filter"] = &ManagedTest{
		ID:          "run-group-filter",
		Name:        "Group Filter Run",
		Status:      "RUNNING",
		LiveMetrics: live,
		History:     []*LiveMetrics{live},
	}
	server.runner.mu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/run-group-filter/metrics?groups=Checkout", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload struct {
		Metrics *LiveMetrics   `json:"metrics"`
		History []*LiveMetrics `json:"history"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode metrics response: %v", err)
	}
	if payload.Metrics == nil || payload.Metrics.ActiveVUsers != 12 || payload.Metrics.TPS != 4 {
		t.Fatalf("filtered metrics = %#v, want Checkout-only aggregate", payload.Metrics)
	}
	if payload.Metrics.Transactions != nil {
		t.Fatalf("filtered metrics transactions = %#v, want omitted global transaction metrics", payload.Metrics.Transactions)
	}
	if len(payload.History) != 1 || payload.History[0].ActiveVUsers != 12 {
		t.Fatalf("filtered history = %#v, want Checkout-only history", payload.History)
	}
	if payload.History[0].Transactions != nil {
		t.Fatalf("filtered history transactions = %#v, want omitted global transaction metrics", payload.History[0].Transactions)
	}
}

func TestHandleGetMetricsRejectsOversizedGroupFilter(t *testing.T) {
	server := NewServer(ServerConfig{})
	server.runner.mu.Lock()
	server.runner.tests["run-group-filter-limit"] = &ManagedTest{
		ID: "run-group-filter-limit", Name: "Group Filter Limit", Status: "RUNNING", LiveMetrics: liveMetricsForGroupFilterTest(),
	}
	server.runner.mu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/run-group-filter-limit/metrics?groups="+url.QueryEscape(strings.Repeat("x", maxGroupFilterNameBytes+1)), nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d: %s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
}

func TestHandleGetTestHistoryOptIn(t *testing.T) {
	server := NewServer(ServerConfig{})
	base := time.Date(2026, 6, 10, 10, 0, 0, 0, time.UTC)
	server.runner.mu.Lock()
	server.runner.tests["run-history-opt-in"] = &ManagedTest{
		ID:     "run-history-opt-in",
		Name:   "History Opt In",
		Status: "RUNNING",
		History: []*LiveMetrics{
			{Timestamp: base, TPS: 1},
			{Timestamp: base.Add(time.Second), TPS: 2},
			{Timestamp: base.Add(2 * time.Second), TPS: 3},
		},
	}
	server.runner.mu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/run-history-opt-in", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("default status = %d: %s", resp.Code, resp.Body.String())
	}
	var defaultPayload ManagedTest
	if err := json.Unmarshal(resp.Body.Bytes(), &defaultPayload); err != nil {
		t.Fatalf("decode default response: %v", err)
	}
	if len(defaultPayload.History) != 0 {
		t.Fatalf("default history length = %d, want 0", len(defaultPayload.History))
	}
	if len(defaultPayload.Sparkline) != 3 {
		t.Fatalf("sparkline length = %d, want 3", len(defaultPayload.Sparkline))
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/tests/run-history-opt-in?include=history&since="+url.QueryEscape(base.Format(time.RFC3339Nano))+"&limit=1", nil)
	resp = httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("opt-in status = %d: %s", resp.Code, resp.Body.String())
	}
	var optInPayload ManagedTest
	if err := json.Unmarshal(resp.Body.Bytes(), &optInPayload); err != nil {
		t.Fatalf("decode opt-in response: %v", err)
	}
	if len(optInPayload.History) != 1 || optInPayload.History[0].TPS != 3 {
		t.Fatalf("opt-in history = %#v, want latest point after since", optInPayload.History)
	}
}

func TestHandleGetMetricsAppliesHistoryWindow(t *testing.T) {
	server := NewServer(ServerConfig{})
	base := time.Date(2026, 6, 10, 10, 0, 0, 0, time.UTC)
	server.runner.mu.Lock()
	server.runner.tests["run-metrics-window"] = &ManagedTest{
		ID:          "run-metrics-window",
		Name:        "Metrics Window",
		Status:      "RUNNING",
		LiveMetrics: &LiveMetrics{Timestamp: base.Add(2 * time.Second), TPS: 3},
		History: []*LiveMetrics{
			{Timestamp: base, TPS: 1},
			{Timestamp: base.Add(time.Second), TPS: 2},
			{Timestamp: base.Add(2 * time.Second), TPS: 3},
		},
	}
	server.runner.mu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/run-metrics-window/metrics?since="+url.QueryEscape(base.Format(time.RFC3339Nano))+"&limit=1", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.Code, resp.Body.String())
	}
	var payload struct {
		History []*LiveMetrics `json:"history"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode metrics response: %v", err)
	}
	if len(payload.History) != 1 || payload.History[0].TPS != 3 {
		t.Fatalf("metrics history window = %#v, want latest point after since", payload.History)
	}
}
