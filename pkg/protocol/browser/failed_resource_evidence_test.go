package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestBrowserNetworkFailureClassificationRequiresFailedEvent(t *testing.T) {
	for _, tc := range []struct {
		name     string
		finished bool
		status   int
		want     string
	}{
		{"failed without response", false, 0, "transport"},
		{"failed after response headers", false, 200, "transport"},
		{"completed HTTP failure", true, 503, "http"},
		{"failed event with observed HTTP failure", false, 503, "http"},
		{"completed HTTP success", true, 200, ""},
		{"unavailable status is not a failure", true, 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := browserNetworkFailureKind(tc.finished, tc.status); got != tc.want {
				t.Fatalf("classification = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBrowserFailedSubresourceRetainsBoundedFailureEvidence(t *testing.T) {
	// BE-4: controlled pages in Chromium and WebKit.
	for _, engine := range []string{"chromium", "webkit"} {
		t.Run(engine, func(t *testing.T) { testFailedSubresourceRetainsBoundedFailureEvidence(t, engine) })
	}
}

func testFailedSubresourceRetainsBoundedFailureEvidence(t *testing.T, engine string) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/broken.js":
			// A real connection close produces Playwright's failed-request
			// event without an HTTP response. No raw error text is retained.
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Errorf("hijack failed resource: %v", err)
				return
			}
			_ = conn.Close()
		case "/unavailable.js":
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<!doctype html><html><head><script src="/broken.js"></script><script src="/unavailable.js"></script></head><body>retained failures</body></html>`))
		}
	}))
	defer server.Close()

	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{"base_url": server.URL, "browser_engine": engine, "browser_headless": true}); err != nil {
		requireBrowserRuntime(t, engine, err)
	}
	defer b.Teardown(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	result, err := b.Execute(ctx, &protocol.Action{Name: "Navigate failed resources", Type: "browser_navigate", Target: "/"})
	if err != nil || !result.Success {
		t.Fatalf("navigation = %+v, %v", result, err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		entries := b.networkEntriesSince(0)
		var transport, httpFailure bool
		for _, entry := range entries {
			switch {
			case strings.HasSuffix(entry.URL, "/broken.js"):
				if entry.FailureKind != "transport" || entry.StatusCode != 0 {
					t.Fatalf("transport evidence = %+v", entry)
				}
				transport = true
			case strings.HasSuffix(entry.URL, "/unavailable.js"):
				if entry.FailureKind != "http" || entry.StatusCode != http.StatusServiceUnavailable {
					t.Fatalf("HTTP failure evidence = %+v", entry)
				}
				httpFailure = true
			}
		}
		if transport && httpFailure {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("missing retained failures: %+v", entries)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
