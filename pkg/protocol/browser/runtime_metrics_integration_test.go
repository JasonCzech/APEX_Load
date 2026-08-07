package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestBrowserProtocolClientMetrics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/style.css":
			w.Header().Set("Content-Type", "text/css")
			_, _ = w.Write([]byte(`body { background: #fff; }`))
		default:
			_, _ = w.Write([]byte(`<!doctype html>
<html>
  <head>
    <title>Metrics Test</title>
    <link rel="stylesheet" href="/style.css" />
  </head>
  <body>
    <div id="status">metrics ready</div>
  </body>
</html>`))
		}
	}))
	defer server.Close()

	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{
		"base_url":               server.URL,
		"browser_headless":       true,
		"browser_client_metrics": true,
		"browser_device_profile": "pixel_8",
	}); err != nil {
		requireBrowserRuntime(t, "", err)
	}
	defer b.Teardown(context.Background())

	result, err := b.Execute(context.Background(), &protocol.Action{
		Name:   "Open metrics page",
		Type:   "browser_navigate",
		Target: "/",
	})
	if err != nil {
		t.Fatalf("navigate returned error: %v", err)
	}
	if !result.Success {
		t.Fatalf("navigate failed: %s", result.ErrorMessage)
	}
	if result.ClientMetrics == nil {
		t.Fatal("ClientMetrics = nil, want populated metrics")
	}
	if result.ClientMetrics.DeviceProfile != "pixel_8" {
		t.Fatalf("DeviceProfile = %q, want pixel_8", result.ClientMetrics.DeviceProfile)
	}
	if result.ClientMetrics.DeviceTier != "high" {
		t.Fatalf("DeviceTier = %q, want high", result.ClientMetrics.DeviceTier)
	}
	if result.ClientMetrics.ActionDurationMs <= 0 {
		t.Fatalf("ActionDurationMs = %v, want > 0", result.ClientMetrics.ActionDurationMs)
	}
	if result.ClientMetrics.JSHeapTotalMB <= 0 {
		t.Fatalf("JSHeapTotalMB = %v, want > 0", result.ClientMetrics.JSHeapTotalMB)
	}
	if result.ClientMetrics.TotalResourceCount <= 0 {
		t.Fatalf("TotalResourceCount = %d, want > 0", result.ClientMetrics.TotalResourceCount)
	}
	if result.ClientMetrics.LongTaskCount < 0 {
		t.Fatalf("LongTaskCount = %d, want >= 0", result.ClientMetrics.LongTaskCount)
	}
}
