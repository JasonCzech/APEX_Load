package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func TestFilterLiveMetricsForGroupsHandlesEmptySelection(t *testing.T) {
	filtered := filterLiveMetricsForGroups(liveMetricsForGroupFilterTest(), groupFilterQuery{Explicit: true})

	if filtered.ActiveVUsers != 0 || filtered.TPS != 0 || filtered.AvgResponseMs != 0 || filtered.P99Ms != 0 {
		t.Fatalf("empty selection did not clear performance metrics: %#v", filtered)
	}
	if len(filtered.GroupMetrics) != 0 {
		t.Fatalf("empty selection groups = %#v, want none", filtered.GroupMetrics)
	}
	if filtered.Resources == nil || filtered.Resources.CPUProcessPct != 55 {
		t.Fatalf("resources were not preserved: %#v", filtered.Resources)
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
	if len(payload.History) != 1 || payload.History[0].ActiveVUsers != 12 {
		t.Fatalf("filtered history = %#v, want Checkout-only history", payload.History)
	}
}
