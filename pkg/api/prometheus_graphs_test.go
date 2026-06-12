package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPrometheusGraphsDisabled(t *testing.T) {
	t.Setenv("APEXLOAD_PROMETHEUS_URL", "")
	server := NewServer(ServerConfig{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/monitoring/prometheus/graphs", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	var payload PrometheusGraphsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Enabled || payload.Configured {
		t.Fatalf("payload enabled/configured = %v/%v, want false/false", payload.Enabled, payload.Configured)
	}
	if len(payload.Graphs) != 0 {
		t.Fatalf("graphs len = %d, want 0", len(payload.Graphs))
	}
}

func TestPrometheusGraphsRejectsBadRange(t *testing.T) {
	t.Setenv("APEXLOAD_PROMETHEUS_URL", "")
	server := NewServer(ServerConfig{})
	settings := server.runner.GetSystemSettings()
	settings.Prometheus = PrometheusSettings{Enabled: true, BaseURL: "http://prometheus:9090", TimeoutSeconds: 5}
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/monitoring/prometheus/graphs?start=2026-06-12T10:00:00Z&end=2026-06-12T09:00:00Z&step_seconds=15", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s, want 400", resp.Code, resp.Body.String())
	}
}

func TestPrometheusGraphsQueriesCuratedPack(t *testing.T) {
	t.Setenv("APEXLOAD_PROMETHEUS_URL", "")
	start := time.Date(2026, time.June, 12, 14, 0, 0, 0, time.UTC)
	end := start.Add(30 * time.Second)
	var queries []string
	var handlerErrors []string

	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query_range" {
			handlerErrors = append(handlerErrors, fmt.Sprintf("path = %q, want /api/v1/query_range", r.URL.Path))
			http.Error(w, "bad path", http.StatusBadRequest)
			return
		}
		queries = append(queries, r.URL.Query().Get("query"))
		if got := r.URL.Query().Get("start"); got != start.Format(time.RFC3339) {
			handlerErrors = append(handlerErrors, fmt.Sprintf("start query = %q, want %q", got, start.Format(time.RFC3339)))
		}
		if got := r.URL.Query().Get("end"); got != end.Format(time.RFC3339) {
			handlerErrors = append(handlerErrors, fmt.Sprintf("end query = %q, want %q", got, end.Format(time.RFC3339)))
		}
		if got := r.URL.Query().Get("step"); got != "15" {
			handlerErrors = append(handlerErrors, fmt.Sprintf("step query = %q, want 15", got))
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"status":"success",
			"data":{
				"resultType":"matrix",
				"result":[
					{
						"metric":{"__name__":"apexload_scaling_vusers","state":"active","region":"east"},
						"values":[[` + strconv.FormatInt(start.Unix(), 10) + `,"12"],[` + strconv.FormatInt(end.Unix(), 10) + `,"18"]]
					}
				]
			}
		}`))
	}))
	defer prom.Close()

	server := NewServer(ServerConfig{})
	settings := server.runner.GetSystemSettings()
	settings.Prometheus = PrometheusSettings{Enabled: true, BaseURL: prom.URL, TimeoutSeconds: 5}
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings: %v", err)
	}

	reqURL := "/api/v1/monitoring/prometheus/graphs?" + url.Values{
		"start":        []string{start.Format(time.RFC3339)},
		"end":          []string{end.Format(time.RFC3339)},
		"step_seconds": []string{"15"},
	}.Encode()
	req := httptest.NewRequest(http.MethodGet, reqURL, nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	if len(handlerErrors) > 0 {
		t.Fatalf("prometheus stub saw request errors: %s", strings.Join(handlerErrors, "; "))
	}
	var payload PrometheusGraphsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !payload.Enabled || !payload.Configured {
		t.Fatalf("payload enabled/configured = %v/%v, want true/true", payload.Enabled, payload.Configured)
	}
	if payload.StepSeconds != 15 || !payload.Start.Equal(start) || !payload.End.Equal(end) {
		t.Fatalf("range = %s..%s step=%d, want requested range", payload.Start, payload.End, payload.StepSeconds)
	}
	if len(payload.Graphs) != len(prometheusGraphDefinitions) {
		t.Fatalf("graphs len = %d, want %d", len(payload.Graphs), len(prometheusGraphDefinitions))
	}
	first := payload.Graphs[0]
	if first.ID != "prometheus_scaling_vusers" || first.Title != "Scaling VUsers" || first.Unit != "VUsers" {
		t.Fatalf("first graph = %#v, want scaling vusers metadata", first)
	}
	if len(first.Series) != 1 {
		t.Fatalf("series len = %d, want 1", len(first.Series))
	}
	series := first.Series[0]
	if series.Label != "active" || !strings.Contains(series.Key, "region_east") || !strings.Contains(series.Key, "state_active") {
		t.Fatalf("series identity = key:%q label:%q, want label and stable label-derived key", series.Key, series.Label)
	}
	if len(series.Values) != 2 || series.Values[0].Value != 12 || series.Values[1].TimestampMs != end.UnixMilli() {
		t.Fatalf("series values = %#v, want decoded Prometheus samples", series.Values)
	}

	sort.Strings(queries)
	wantQueries := make([]string, 0, len(prometheusGraphDefinitions))
	for _, definition := range prometheusGraphDefinitions {
		wantQueries = append(wantQueries, definition.Query)
	}
	sort.Strings(wantQueries)
	if strings.Join(queries, "\n") != strings.Join(wantQueries, "\n") {
		t.Fatalf("queries = %v, want curated pack %v", queries, wantQueries)
	}
}

func TestPrometheusGraphsHandlesEmptySeries(t *testing.T) {
	t.Setenv("APEXLOAD_PROMETHEUS_URL", "")
	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[]}}`))
	}))
	defer prom.Close()

	payload := requestPrometheusGraphsForTest(t, prom.URL)
	if len(payload.Graphs) != len(prometheusGraphDefinitions) {
		t.Fatalf("graphs len = %d, want %d", len(payload.Graphs), len(prometheusGraphDefinitions))
	}
	for _, graph := range payload.Graphs {
		if len(graph.Series) != 0 {
			t.Fatalf("graph %s series len = %d, want 0", graph.ID, len(graph.Series))
		}
	}
}

func TestPrometheusGraphsReturnsBadGatewayForUpstreamError(t *testing.T) {
	t.Setenv("APEXLOAD_PROMETHEUS_URL", "")
	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
	}))
	defer prom.Close()

	server := prometheusGraphAPIServerForTest(t, prom.URL, 5)
	start := time.Date(2026, time.June, 12, 14, 0, 0, 0, time.UTC)
	req := prometheusGraphRequestForTest(start, start.Add(time.Minute), 15)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadGateway {
		t.Fatalf("status = %d body=%s, want 502", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "internal server error") {
		t.Fatalf("body = %q, want redacted internal server error", resp.Body.String())
	}
}

func TestPrometheusGraphsReturnsBadGatewayForTimeout(t *testing.T) {
	t.Setenv("APEXLOAD_PROMETHEUS_URL", "")
	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[]}}`))
	}))
	defer prom.Close()

	server := prometheusGraphAPIServerForTest(t, prom.URL, 1)
	start := time.Date(2026, time.June, 12, 14, 0, 0, 0, time.UTC)
	req := prometheusGraphRequestForTest(start, start.Add(time.Minute), 15)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadGateway {
		t.Fatalf("status = %d body=%s, want 502", resp.Code, resp.Body.String())
	}
}

func requestPrometheusGraphsForTest(t *testing.T, baseURL string) PrometheusGraphsResponse {
	t.Helper()
	server := prometheusGraphAPIServerForTest(t, baseURL, 5)
	start := time.Date(2026, time.June, 12, 14, 0, 0, 0, time.UTC)
	req := prometheusGraphRequestForTest(start, start.Add(time.Minute), 15)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	var payload PrometheusGraphsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return payload
}

func prometheusGraphAPIServerForTest(t *testing.T, baseURL string, timeoutSeconds int) *Server {
	t.Helper()
	server := NewServer(ServerConfig{})
	settings := server.runner.GetSystemSettings()
	settings.Prometheus = PrometheusSettings{Enabled: true, BaseURL: baseURL, TimeoutSeconds: timeoutSeconds}
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings: %v", err)
	}
	return server
}

func prometheusGraphRequestForTest(start time.Time, end time.Time, stepSeconds int) *http.Request {
	reqURL := "/api/v1/monitoring/prometheus/graphs?" + url.Values{
		"start":        []string{start.Format(time.RFC3339)},
		"end":          []string{end.Format(time.RFC3339)},
		"step_seconds": []string{strconv.Itoa(stepSeconds)},
	}.Encode()
	return httptest.NewRequest(http.MethodGet, reqURL, nil)
}
