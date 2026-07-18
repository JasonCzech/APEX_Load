package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/grafana"
	"github.com/jasonczech/apexload/pkg/storage"
)

type trackingGraphDataSource struct {
	current atomic.Int64
	maximum atomic.Int64
}

type queryCaptureDataSource struct{ queries []graphRangeQuery }

type deadlineCaptureDataSource struct{ remaining time.Duration }

func (d *queryCaptureDataSource) ID() string              { return "capture" }
func (d *queryCaptureDataSource) Available() (bool, bool) { return true, true }
func (d *queryCaptureDataSource) QueryRange(_ context.Context, query graphRangeQuery) ([]PrometheusSeries, error) {
	d.queries = append(d.queries, query)
	return []PrometheusSeries{{Key: query.SeriesKeyPrefix, Values: []PrometheusPoint{{Value: 1}}}}, nil
}

func (d *deadlineCaptureDataSource) ID() string              { return "deadline-capture" }
func (d *deadlineCaptureDataSource) Available() (bool, bool) { return true, true }
func (d *deadlineCaptureDataSource) QueryRange(ctx context.Context, query graphRangeQuery) ([]PrometheusSeries, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return nil, errors.New("query context has no deadline")
	}
	d.remaining = time.Until(deadline)
	return []PrometheusSeries{{Key: query.SeriesKeyPrefix, Values: []PrometheusPoint{{Value: 1}}}}, nil
}

func (d *trackingGraphDataSource) ID() string              { return "tracking" }
func (d *trackingGraphDataSource) Available() (bool, bool) { return true, true }
func (d *trackingGraphDataSource) QueryRange(context.Context, graphRangeQuery) ([]PrometheusSeries, error) {
	current := d.current.Add(1)
	for {
		maximum := d.maximum.Load()
		if current <= maximum || d.maximum.CompareAndSwap(maximum, current) {
			break
		}
	}
	time.Sleep(15 * time.Millisecond)
	d.current.Add(-1)
	return []PrometheusSeries{{Key: "k", Label: "value", Values: []PrometheusPoint{{Value: 1}}}}, nil
}

func matrixSeries(labels string, ts int64, value string) string {
	return `{"metric":{` + labels + `},"values":[[` + strconv.FormatInt(ts, 10) + `,"` + value + `"]]}`
}

func fakePromMatrix(series ...string) string {
	return `{"status":"success","data":{"resultType":"matrix","result":[` + strings.Join(series, ",") + `]}}`
}

func newDashboardTestDataSource(t *testing.T, handler http.HandlerFunc) (graphDataSource, func()) {
	t.Helper()
	prom := httptest.NewServer(handler)
	ds := newPromGraphDataSource(PrometheusSettings{Enabled: true, BaseURL: prom.URL, TimeoutSeconds: 5})
	return ds, prom.Close
}

func linePanel(id int, title, expr string) grafana.Panel {
	return grafana.Panel{
		PanelID: id, Title: title, Kind: grafana.PanelKindLine,
		Targets: []grafana.Target{{RefID: "A", Expr: expr}},
	}
}

func TestResolveDashboardPanelsHappyPath(t *testing.T) {
	now := time.Date(2026, time.June, 12, 14, 0, 0, 0, time.UTC)
	ds, closeFn := newDashboardTestDataSource(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fakePromMatrix(matrixSeries(`"state":"active"`, now.Unix(), "12"))))
	})
	defer closeFn()

	panels := resolveDashboardPanels(context.Background(), ds, dashboardResolvePlan{
		dashboardID:            "dash1",
		panels:                 []grafana.Panel{linePanel(1, "CPU", "cpu"), linePanel(2, "Mem", "mem")},
		start:                  now,
		end:                    now.Add(time.Minute),
		stepSeconds:            15,
		perQueryTimeoutSeconds: 5,
	})
	if len(panels) != 2 {
		t.Fatalf("panels = %d, want 2", len(panels))
	}
	for _, p := range panels {
		if p.Error != "" {
			t.Fatalf("panel %q error = %q, want none", p.Title, p.Error)
		}
		if len(p.Series) != 1 || len(p.Series[0].Values) != 1 || p.Series[0].Values[0].Value != 12 {
			t.Fatalf("panel %q series = %#v, want one decoded sample", p.Title, p.Series)
		}
		// Keys are namespaced so imported panels can never collide with static ids.
		if !strings.HasPrefix(p.Series[0].Key, "gdash_dash1_") {
			t.Fatalf("series key %q not namespaced", p.Series[0].Key)
		}
	}
}

func TestResolveStoredDashboardResultUsesSelectedGrafanaTimeout(t *testing.T) {
	server, _ := newGrafanaDashboardAPIServer(t)
	t.Setenv("APEXLOAD_TEST_GRAFANA_TIMEOUT_TOKEN", "token")
	settings := server.runner.GetSystemSettings()
	settings.Prometheus.TimeoutSeconds = 1
	settings.Grafana = GrafanaSettings{
		Enabled:                   true,
		BaseURL:                   "https://grafana.example.com",
		ServiceAccountTokenEnvVar: "APEXLOAD_TEST_GRAFANA_TIMEOUT_TOKEN",
		TimeoutSeconds:            2,
	}
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatal(err)
	}
	ds := &deadlineCaptureDataSource{}
	now := time.Now().UTC()
	dashboard := &storage.GrafanaDashboard{
		ID: "dashboard-1", ProjectID: "proj-a", Name: "Infra",
		Panels: []grafana.Panel{linePanel(1, "Up", `up{project_id="$project_id"}`)},
	}
	result, _ := server.resolveStoredDashboardResult(context.Background(), ds, dashboard, "", now, now.Add(time.Minute), 15)
	if len(result.Panels) != 1 || result.Panels[0].Error != "" {
		t.Fatalf("resolved panels = %#v", result.Panels)
	}
	wantBudget := dashboardTotalTimeout(settings.Grafana.TimeoutSeconds)
	if ds.remaining < wantBudget-500*time.Millisecond || ds.remaining > wantBudget {
		t.Fatalf("dashboard deadline remaining = %s, want budget %s derived from Grafana's timeout", ds.remaining, wantBudget)
	}
}

func TestResolveDashboardPanelsSeriesCapErrors(t *testing.T) {
	now := time.Date(2026, time.June, 12, 14, 0, 0, 0, time.UTC)
	var series []string
	for i := 0; i < maxDashboardSeriesPerPanel+1; i++ {
		series = append(series, matrixSeries(fmt.Sprintf(`"id":"%d"`, i), now.Unix(), "1"))
	}
	ds, closeFn := newDashboardTestDataSource(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fakePromMatrix(series...)))
	})
	defer closeFn()

	panels := resolveDashboardPanels(context.Background(), ds, dashboardResolvePlan{
		dashboardID:            "d",
		panels:                 []grafana.Panel{linePanel(1, "Wide", "explode")},
		start:                  now,
		end:                    now.Add(time.Minute),
		stepSeconds:            15,
		perQueryTimeoutSeconds: 5,
	})
	if len(panels) != 1 || !strings.Contains(panels[0].Error, "exceeding the per-panel limit") {
		t.Fatalf("expected series-cap error, got %#v", panels)
	}
	if len(panels[0].Series) != 0 {
		t.Fatalf("series-capped panel must carry no series, got %d", len(panels[0].Series))
	}
}

func TestResolveDashboardPanelsSeriesCapIsAggregateAcrossTargets(t *testing.T) {
	now := time.Date(2026, time.June, 12, 14, 0, 0, 0, time.UTC)
	perTarget := maxDashboardSeriesPerPanel/2 + 1
	series := make([]string, 0, perTarget)
	for i := 0; i < perTarget; i++ {
		series = append(series, matrixSeries(fmt.Sprintf(`"id":"%d"`, i), now.Unix(), "1"))
	}
	ds, closeFn := newDashboardTestDataSource(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fakePromMatrix(series...)))
	})
	defer closeFn()
	panel := linePanel(1, "Wide", "first")
	panel.Targets = append(panel.Targets, grafana.Target{RefID: "B", Expr: "second"})
	panels := resolveDashboardPanels(context.Background(), ds, dashboardResolvePlan{
		dashboardID: "d", panels: []grafana.Panel{panel}, start: now, end: now.Add(time.Minute), stepSeconds: 15, perQueryTimeoutSeconds: 5,
	})
	if len(panels) != 1 || !strings.Contains(panels[0].Error, "per-panel limit") {
		t.Fatalf("expected aggregate series-cap error, got %#v", panels)
	}
}

func TestResolveDashboardPanelsFailSoft(t *testing.T) {
	now := time.Date(2026, time.June, 12, 14, 0, 0, 0, time.UTC)
	ds, closeFn := newDashboardTestDataSource(t, func(w http.ResponseWriter, r *http.Request) {
		// The "boom" query fails; every other query succeeds.
		if strings.Contains(r.URL.Query().Get("query"), "boom") {
			http.Error(w, "bad query", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fakePromMatrix(matrixSeries(`"ok":"1"`, now.Unix(), "5"))))
	})
	defer closeFn()

	result := DashboardPanelsResult{Panels: resolveDashboardPanels(context.Background(), ds, dashboardResolvePlan{
		dashboardID:            "d",
		panels:                 []grafana.Panel{linePanel(1, "Good", "healthy"), linePanel(2, "Bad", "boom")},
		start:                  now,
		end:                    now.Add(time.Minute),
		stepSeconds:            15,
		perQueryTimeoutSeconds: 5,
	})}
	if result.AllPanelsFailed() {
		t.Fatalf("one healthy panel should keep AllPanelsFailed false")
	}
	byTitle := map[string]DashboardPanel{}
	for _, p := range result.Panels {
		byTitle[p.Title] = p
	}
	if byTitle["Good"].Error != "" || len(byTitle["Good"].Series) != 1 {
		t.Fatalf("Good panel = %#v, want series and no error", byTitle["Good"])
	}
	if byTitle["Bad"].Error == "" {
		t.Fatalf("Bad panel should carry an error")
	}
}

func TestResolveDashboardPanelsSingleValueTile(t *testing.T) {
	now := time.Date(2026, time.June, 12, 14, 0, 0, 0, time.UTC)
	ds, closeFn := newDashboardTestDataSource(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Two points; the tile takes the newest.
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[` +
			strconv.FormatInt(now.Unix(), 10) + `,"3"],[` +
			strconv.FormatInt(now.Add(30*time.Second).Unix(), 10) + `,"9"]]}]}}`))
	})
	defer closeFn()

	panels := resolveDashboardPanels(context.Background(), ds, dashboardResolvePlan{
		dashboardID: "d",
		panels: []grafana.Panel{{
			PanelID: 1, Title: "Up", Kind: grafana.PanelKindSingleValue,
			Targets: []grafana.Target{{RefID: "A", Expr: "up"}},
		}},
		start:                  now,
		end:                    now.Add(time.Minute),
		stepSeconds:            15,
		perQueryTimeoutSeconds: 5,
	})
	if len(panels) != 1 || panels[0].Value == nil {
		t.Fatalf("expected a single-value tile, got %#v", panels)
	}
	if *panels[0].Value != 9 {
		t.Fatalf("tile value = %v, want newest sample 9", *panels[0].Value)
	}
	if len(panels[0].Series) != 1 || len(panels[0].Series[0].Values) != 2 {
		t.Fatalf("tile sparkline series = %#v", panels[0].Series)
	}
}

func TestResolveDashboardPanelsRejectsAmbiguousSingleValueSeries(t *testing.T) {
	now := time.Now().UTC()
	ds, closeFn := newDashboardTestDataSource(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(fakePromMatrix(
			matrixSeries(`"instance":"a"`, now.Unix(), "1"),
			matrixSeries(`"instance":"b"`, now.Unix(), "2"),
		)))
	})
	defer closeFn()
	panels := resolveDashboardPanels(context.Background(), ds, dashboardResolvePlan{
		dashboardID: "d", panels: []grafana.Panel{{
			PanelID: 1, Title: "Ambiguous", Kind: grafana.PanelKindSingleValue,
			Targets: []grafana.Target{{RefID: "A", Expr: "up"}},
		}}, start: now, end: now.Add(time.Minute), stepSeconds: 15, perQueryTimeoutSeconds: 5,
	})
	if len(panels) != 1 || !strings.Contains(panels[0].Error, "multiple series") || panels[0].Value != nil {
		t.Fatalf("ambiguous tile = %#v, want explicit error", panels)
	}
}

func TestEvaluatePanelThresholdsMarksCriticalRange(t *testing.T) {
	warn := 70.0
	critical := 90.0
	evaluated, breached, detail := evaluatePanelThresholds([]grafana.PanelThreshold{
		{Value: nil, Severity: "normal"},
		{Value: &warn, Severity: "warning"},
		{Value: &critical, Severity: "critical"},
	}, []PrometheusSeries{{Label: "api-1", Values: []PrometheusPoint{{Value: 75}, {Value: 91}}}})
	if !evaluated || !breached || !strings.Contains(detail, "api-1") {
		t.Fatalf("evaluated=%t breached=%t detail=%q", evaluated, breached, detail)
	}
}

func TestResolveDashboardPanelsResolvesTemplateVars(t *testing.T) {
	now := time.Date(2026, time.June, 12, 14, 0, 0, 0, time.UTC)
	var seenQuery string
	ds, closeFn := newDashboardTestDataSource(t, func(w http.ResponseWriter, r *http.Request) {
		seenQuery = r.URL.Query().Get("query")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fakePromMatrix(matrixSeries(`"x":"1"`, now.Unix(), "1"))))
	})
	defer closeFn()

	panels := resolveDashboardPanels(context.Background(), ds, dashboardResolvePlan{
		dashboardID:            "d",
		panels:                 []grafana.Panel{linePanel(1, "Rate", "rate(http_total{test_id=\"$test_id\"}[$__interval])")},
		resolution:             grafana.QueryResolution{TestID: "run-9"},
		start:                  now,
		end:                    now.Add(time.Minute),
		stepSeconds:            15,
		perQueryTimeoutSeconds: 5,
	})
	if len(panels) != 1 || panels[0].Error != "" {
		t.Fatalf("panel error = %#v", panels)
	}
	if seenQuery != `rate(http_total{test_id="run-9"}[15s])` {
		t.Fatalf("resolved query = %q, want template vars substituted", seenQuery)
	}
}

func TestResolveDashboardPanelsBlocksLegacyUnscopedStoredQuery(t *testing.T) {
	now := time.Date(2026, time.June, 12, 14, 0, 0, 0, time.UTC)
	queries := 0
	ds, closeFn := newDashboardTestDataSource(t, func(w http.ResponseWriter, r *http.Request) {
		queries++
		_, _ = w.Write([]byte(fakePromMatrix()))
	})
	defer closeFn()
	panels := resolveDashboardPanels(context.Background(), ds, dashboardResolvePlan{
		dashboardID: "legacy", panels: []grafana.Panel{linePanel(1, "Unsafe", "up")},
		resolution: grafana.QueryResolution{ProjectID: "project-a"},
		start:      now, end: now.Add(time.Minute), stepSeconds: 15, perQueryTimeoutSeconds: 5,
	})
	if len(panels) != 1 || !strings.Contains(panels[0].Error, "not run-scoped") {
		t.Fatalf("expected unscoped stored query to be blocked, got %#v", panels)
	}
	if queries != 0 {
		t.Fatalf("datasource received %d queries, want zero", queries)
	}
}

func TestResolveDashboardPanelsRevalidatesScopeAfterBindingSubstitution(t *testing.T) {
	now := time.Date(2026, time.June, 12, 14, 0, 0, 0, time.UTC)
	queries := 0
	ds, closeFn := newDashboardTestDataSource(t, func(w http.ResponseWriter, r *http.Request) {
		queries++
		_, _ = w.Write([]byte(fakePromMatrix()))
	})
	defer closeFn()
	panels := resolveDashboardPanels(context.Background(), ds, dashboardResolvePlan{
		dashboardID: "bound", panels: []grafana.Panel{linePanel(1, "Unsafe binding", `up{project_id="$project_id"} + $extra`)},
		resolution: grafana.QueryResolution{ProjectID: "project-a", Bindings: map[string]string{"extra": "foo+bar"}},
		start:      now, end: now.Add(time.Minute), stepSeconds: 15, perQueryTimeoutSeconds: 5,
	})
	if len(panels) != 1 || !strings.Contains(panels[0].Error, "resolved query is not run-scoped") {
		t.Fatalf("expected structurally unsafe binding to be blocked, got %#v", panels)
	}
	if queries != 0 {
		t.Fatalf("datasource received %d queries, want zero", queries)
	}
}

func TestResolveDashboardPanelsUsesPerTargetDatasourceUID(t *testing.T) {
	now := time.Date(2026, time.June, 12, 14, 0, 0, 0, time.UTC)
	ds := &queryCaptureDataSource{}
	panel := linePanel(1, "Mixed", `up{project_id="$project_id"}`)
	panel.DatasourceUID = "panel-default"
	panel.Targets[0].DatasourceUID = "target-override"
	panels := resolveDashboardPanels(context.Background(), ds, dashboardResolvePlan{
		dashboardID: "routed", panels: []grafana.Panel{panel}, resolution: grafana.QueryResolution{ProjectID: "project-a"},
		start: now, end: now.Add(time.Minute), stepSeconds: 15, perQueryTimeoutSeconds: 5,
	})
	if len(panels) != 1 || panels[0].Error != "" || len(ds.queries) != 1 || ds.queries[0].DatasourceUID != "target-override" {
		t.Fatalf("panels=%#v queries=%#v", panels, ds.queries)
	}
}

func TestResolveDashboardPanelsSharesAggregateQuerySemaphore(t *testing.T) {
	ds := &trackingGraphDataSource{}
	panels := make([]grafana.Panel, 0, 4)
	for i := 1; i <= 4; i++ {
		panels = append(panels, linePanel(i, fmt.Sprintf("Panel %d", i), fmt.Sprintf("metric_%d", i)))
	}
	resolveDashboardPanels(context.Background(), ds, dashboardResolvePlan{
		dashboardID: "d", panels: panels,
		start: time.Now(), end: time.Now().Add(time.Minute), stepSeconds: 15,
		perQueryTimeoutSeconds: 5, querySemaphore: make(chan struct{}, 1),
	})
	if got := ds.maximum.Load(); got != 1 {
		t.Fatalf("maximum concurrent queries = %d, want 1", got)
	}
}

func TestResolveDashboardPanelsAggregateByteBudget(t *testing.T) {
	now := time.Now().UTC()
	ds := &trackingGraphDataSource{}
	var aggregateBytes atomic.Int64
	panels := resolveDashboardPanels(context.Background(), ds, dashboardResolvePlan{
		dashboardID: "d", panels: []grafana.Panel{linePanel(1, "CPU", "cpu")},
		start: now, end: now.Add(time.Minute), stepSeconds: 15, perQueryTimeoutSeconds: 5,
		aggregateBytesUsed: &aggregateBytes, aggregateBytesMax: 1,
	})
	if len(panels) != 1 || !strings.Contains(panels[0].Error, "decoded-byte budget") {
		t.Fatalf("expected aggregate byte-budget error, got %#v", panels)
	}
}

func TestDashboardPanelErrorIsBounded(t *testing.T) {
	got := dashboardPanelError(errors.New(strings.Repeat("é", maxDashboardPanelErrorBytes)))
	if len(got) > maxDashboardPanelErrorBytes {
		t.Fatalf("bounded error length = %d, max %d", len(got), maxDashboardPanelErrorBytes)
	}
	if got == strings.Repeat("é", maxDashboardPanelErrorBytes) {
		t.Fatal("oversized error was not truncated")
	}
}
