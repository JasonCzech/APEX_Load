package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// A session refused at Init opens its context at a later iteration boundary
// once memory recovers, keeps its evidence lifecycle, and runs that
// iteration from its first action.
func TestRefusedChromiumSessionRejoinsAtAnIterationBoundary(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser integration test in -short mode")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><title>ok</title><p>ok</p>`))
	}))
	t.Cleanup(server.Close)
	var usage atomic.Int64
	usage.Store(2 << 30)
	budget := useTestEvidenceBudget(t, 64<<30, func() (int64, error) { return usage.Load(), nil }, 1<<30)
	shortenReadmissionBackoff(t, 0)
	lifecycle, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	b := &Browser{}
	if err := b.Init(lifecycle, map[string]any{"base_url": server.URL, "browser_headless": true, "browser_engine": "chromium", "browser_capture_document_metrics": true}); err != nil {
		requireBrowserRuntime(t, "chromium", err)
	}
	t.Cleanup(func() { _ = b.Teardown(context.Background()) })
	if b.currentPage() != nil {
		t.Fatal("fixture: Init was not refused")
	}
	// Memory recovers and the next sample sees it.
	usage.Store(100 << 20)
	budget.mu.Lock()
	budget.sampledAt = time.Time{}
	budget.mu.Unlock()
	if err := b.BeginIteration(context.Background()); err != nil {
		t.Fatalf("re-admission at the boundary: %v", err)
	}
	if b.currentPage() == nil || b.resourceLimitReason() != "" {
		t.Fatalf("the session did not rejoin: limit %q", b.resourceLimitReason())
	}
	b.mu.Lock()
	kept := b.evidence.lifecycleContext == lifecycle
	b.mu.Unlock()
	if !kept {
		t.Fatal("re-admission lost the session's evidence lifecycle")
	}
	result, err := b.Execute(context.Background(), &protocol.Action{Name: "open", Type: "browser_navigate", Target: "/"})
	if err != nil || result.ErrorMessage != "" {
		t.Fatalf("the rejoined iteration's first action: %v %s", err, result.ErrorMessage)
	}
}
