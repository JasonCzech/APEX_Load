package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

var traceparentPattern = regexp.MustCompile(`^00-[0-9a-f]{32}-[0-9a-f]{16}-01$`)

func TestNewBrowserTraceparentFormat(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 5; i++ {
		value := newBrowserTraceparent()
		if !traceparentPattern.MatchString(value) {
			t.Fatalf("traceparent %q does not match W3C format", value)
		}
		if seen[value] {
			t.Fatalf("traceparent %q repeated — ids must be random", value)
		}
		seen[value] = true
	}
}

func TestBrowserTraceparentInjection(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser integration test in -short mode")
	}
	var mu sync.Mutex
	received := make([]string, 0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		received = append(received, r.Header.Get("traceparent"))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><html><body><p id="ready">ok</p></body></html>`))
	}))
	defer server.Close()

	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{
		"base_url":            server.URL,
		"browser_headless":    true,
		"browser_traceparent": true,
	}); err != nil {
		requireBrowserRuntime(t, "", err)
	}
	defer func() { _ = b.Teardown(context.Background()) }()

	result, err := b.Execute(context.Background(), &protocol.Action{
		Name: "open", Type: "browser_navigate", Target: "/",
	})
	if err != nil || result.ErrorMessage != "" {
		t.Fatalf("navigate failed: err=%v msg=%s", err, result.ErrorMessage)
	}

	if !traceparentPattern.MatchString(result.CorrelationID) {
		t.Errorf("result correlation id = %q, want W3C traceparent", result.CorrelationID)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(received) == 0 {
		t.Fatal("server received no requests")
	}
	matched := false
	for _, header := range received {
		if header == result.CorrelationID {
			matched = true
		}
	}
	if !matched {
		t.Errorf("no request carried the action traceparent %q; received %v", result.CorrelationID, received)
	}
}

func TestBrowserTraceparentDisabledByDefault(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser integration test in -short mode")
	}
	var mu sync.Mutex
	sawHeader := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if r.Header.Get("traceparent") != "" {
			sawHeader = true
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><html><body>ok</body></html>`))
	}))
	defer server.Close()

	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{
		"base_url":         server.URL,
		"browser_headless": true,
	}); err != nil {
		requireBrowserRuntime(t, "", err)
	}
	defer func() { _ = b.Teardown(context.Background()) }()

	result, err := b.Execute(context.Background(), &protocol.Action{
		Name: "open", Type: "browser_navigate", Target: "/",
	})
	if err != nil || result.ErrorMessage != "" {
		t.Fatalf("navigate failed: err=%v msg=%s", err, result.ErrorMessage)
	}
	if result.CorrelationID != "" {
		t.Errorf("correlation id should be empty when traceparent is off, got %q", result.CorrelationID)
	}
	mu.Lock()
	defer mu.Unlock()
	if sawHeader {
		t.Error("traceparent header sent despite being disabled")
	}
}
