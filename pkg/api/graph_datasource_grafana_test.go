package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDecodeGrafanaFrames(t *testing.T) {
	raw := []byte(`{"results":{"A":{"frames":[{"schema":{"fields":[{"name":"Time","type":"time"},{"name":"CPU","type":"number","labels":{"instance":"api-1"}}]},"data":{"values":[[1000,2000],[1.5,null]]}}]}}}`)
	series, err := decodeGrafanaFrames(raw, graphRangeQuery{SeriesKeyPrefix: "panel", MaxSeries: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(series) != 1 || series[0].Label != "CPU" || series[0].Labels["instance"] != "api-1" {
		t.Fatalf("series = %#v", series)
	}
	if len(series[0].Values) != 1 || series[0].Values[0].Value != 1.5 || !series[0].Values[0].Timestamp.Equal(time.UnixMilli(1000).UTC()) {
		t.Fatalf("points = %#v", series[0].Values)
	}
}

func TestDecodeGrafanaFramesReportsQueryError(t *testing.T) {
	if _, err := decodeGrafanaFrames([]byte(`{"results":{"A":{"error":"denied"}}}`), graphRangeQuery{}); err == nil {
		t.Fatal("expected Grafana query error")
	}
}

func TestDecodeGrafanaFramesRejectsMalformedNonEmptyResponse(t *testing.T) {
	if _, err := decodeGrafanaFrames([]byte(`{"results":{"A":{"frames":[{"schema":{"fields":[{"name":"Time"}]},"data":{"values":[[1000]]}}]}}}`), graphRangeQuery{}); err == nil {
		t.Fatal("expected malformed frame error")
	}
}

func TestGrafanaGraphDataSourceRoutesByUIDAndAuthenticates(t *testing.T) {
	t.Setenv("APEXLOAD_TEST_GRAFANA_TOKEN", "service-token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ds/query" || r.Header.Get("Authorization") != "Bearer service-token" {
			t.Errorf("request path/auth = %s / %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		var payload struct {
			Queries []struct {
				Datasource map[string]string `json:"datasource"`
				Format     string            `json:"format"`
			} `json:"queries"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if len(payload.Queries) != 1 || payload.Queries[0].Datasource["uid"] != "loki-prod" || payload.Queries[0].Format != "time_series" {
			t.Errorf("query payload = %#v", payload)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":{"A":{"frames":[{"schema":{"fields":[{"name":"Time","type":"time"},{"name":"CPU","type":"number"}]},"data":{"values":[[1000],[2.5]]}}]}}}`))
	}))
	defer server.Close()
	ds := newGrafanaGraphDataSource(GrafanaSettings{Enabled: true, BaseURL: server.URL, ServiceAccountTokenEnvVar: "APEXLOAD_TEST_GRAFANA_TOKEN", TimeoutSeconds: 5})
	series, err := ds.QueryRange(t.Context(), graphRangeQuery{DatasourceUID: "loki-prod", Expr: "query", Start: time.Unix(0, 0), End: time.Unix(60, 0), StepSeconds: 15, MaxSeries: 5})
	if err != nil || len(series) != 1 || series[0].Values[0].Value != 2.5 {
		t.Fatalf("series=%#v err=%v", series, err)
	}
}

func TestGrafanaGraphDataSourceFailsClosedWhenTokenIsMissing(t *testing.T) {
	t.Setenv("APEXLOAD_TEST_MISSING_GRAFANA_TOKEN", "")
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	ds := newGrafanaGraphDataSource(GrafanaSettings{Enabled: true, BaseURL: server.URL, ServiceAccountTokenEnvVar: "APEXLOAD_TEST_MISSING_GRAFANA_TOKEN", TimeoutSeconds: 5})
	if _, err := ds.QueryRange(t.Context(), graphRangeQuery{DatasourceUID: "prom", Expr: "up", Start: time.Unix(0, 0), End: time.Unix(60, 0), StepSeconds: 15}); err == nil {
		t.Fatal("expected missing Grafana token error")
	}
	if requests != 0 {
		t.Fatalf("unauthenticated requests = %d, want 0", requests)
	}
}
