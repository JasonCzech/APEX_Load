package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMetricsOnAPIPortByDefault(t *testing.T) {
	server := NewServer(ServerConfig{})
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	server.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /metrics on API mux = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "apexload_up") {
		t.Fatalf("expected apexload_up in metrics body, got %q", rec.Body.String()[:min(200, rec.Body.Len())])
	}
}

func TestDedicatedMetricsListenerMovesMetricsOffAPIPort(t *testing.T) {
	server := NewServer(ServerConfig{MetricsAddr: "127.0.0.1:0"})

	// The API mux must no longer expose Prometheus metrics. (Unknown paths
	// fall through to the embedded dashboard SPA, so check the content, not
	// the status code.)
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	server.mux.ServeHTTP(rec, req)
	if strings.Contains(rec.Body.String(), "apexload_up") {
		t.Fatal("GET /metrics still served on the API mux despite MetricsAddr")
	}

	// The dedicated listener serves it.
	if err := server.startMetricsListener(); err != nil {
		t.Fatalf("startMetricsListener: %v", err)
	}
	t.Cleanup(func() {
		if server.metricsServer != nil {
			_ = server.metricsServer.Shutdown(context.Background())
		}
	})

	addr := server.MetricsBoundAddr()
	if addr == "" {
		t.Fatal("MetricsBoundAddr is empty after startMetricsListener")
	}
	resp, err := http.Get("http://" + addr + "/metrics")
	if err != nil {
		t.Fatalf("GET metrics listener: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("metrics listener status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(string(body), "apexload_up") {
		t.Fatal("expected apexload_up in dedicated metrics body")
	}

	// And it serves ONLY metrics — no API surface.
	apiResp, err := http.Get("http://" + addr + "/api/v1/tests")
	if err != nil {
		t.Fatalf("GET api path on metrics listener: %v", err)
	}
	defer apiResp.Body.Close()
	if apiResp.StatusCode == http.StatusOK {
		t.Fatal("metrics listener must not serve API routes")
	}
}
