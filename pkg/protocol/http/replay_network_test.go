package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestReplayNetworkCaptureRequiresDispatchAndCapability(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(201) }))
	defer target.Close()
	for _, enabled := range []bool{false, true} {
		h := initHTTP(t, map[string]any{"base_url": target.URL, protocol.ReplayNetworkCaptureConfigKey: enabled})
		defer h.Teardown(context.Background())
		result, err := h.Execute(context.Background(), &protocol.Action{Name: "observed", Type: "request", Method: "GET", Target: "/actual"})
		if err != nil || result == nil {
			t.Fatalf("execute: %v", err)
		}
		if enabled {
			if len(result.NetworkEntries) != 1 || result.NetworkEntries[0].URL != target.URL+"/actual" || result.NetworkEntries[0].StatusCode != 201 {
				t.Fatalf("observed network: %+v; result: %+v", result.NetworkEntries, result)
			}
		} else if len(result.NetworkEntries) != 0 {
			t.Fatal("normal load runs must not capture debug traffic")
		}
		refused, _ := h.Execute(context.Background(), &protocol.Action{Name: "invalid", Type: "request", Method: "bad\nmethod", Target: "/invalid"})
		if refused != nil && len(refused.NetworkEntries) != 0 {
			t.Fatal("invalid input produced a network observation")
		}
	}
}

func TestReplayNetworkCaptureRetainsMetaRefreshAndRevalidation(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/refresh":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<meta http-equiv="refresh" content="0;url=/final">`))
		case "/cache":
			w.Header().Set("Cache-Control", "max-age=3600")
			w.Header().Set("ETag", `"test"`)
			if r.Header.Get("If-None-Match") != "" {
				w.WriteHeader(304)
				return
			}
			_, _ = w.Write([]byte("cached"))
		default:
			w.WriteHeader(201)
		}
	}))
	defer target.Close()
	h := initHTTP(t, map[string]any{"base_url": target.URL, "max_meta_refresh": 2, protocol.ReplayNetworkCaptureConfigKey: true})
	defer h.Teardown(context.Background())
	result, err := h.Execute(context.Background(), &protocol.Action{Name: "refresh", Type: "GET", Method: "GET", Target: "/refresh"})
	if err != nil || len(result.NetworkEntries) != 2 {
		t.Fatalf("refresh observations=%+v err=%v", result.NetworkEntries, err)
	}
	if result.NetworkEntries[0].URL != target.URL+"/refresh" || result.NetworkEntries[1].URL != target.URL+"/final" {
		t.Fatalf("refresh lost a dispatched page: %+v", result.NetworkEntries)
	}
	// Revalidation must retain the wire 304, not the replacement cached response.
	cached := initHTTP(t, map[string]any{"base_url": target.URL, "http_cache_enabled": true, "http_cache_check_newer_versions": true, protocol.ReplayNetworkCaptureConfigKey: true})
	defer cached.Teardown(context.Background())
	action := &protocol.Action{Name: "cache", Type: "GET", Method: "GET", Target: "/cache"}
	_, _ = cached.Execute(context.Background(), action)
	second, _ := cached.Execute(context.Background(), action)
	if len(second.NetworkEntries) != 1 || second.NetworkEntries[0].StatusCode != 304 {
		t.Fatalf("revalidation observation=%+v", second.NetworkEntries)
	}
}
