package browser

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/mxschmitt/playwright-go"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestBrowserDocumentNativeCausalLifetime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html><body>native lifetime</body></html>"))
	}))
	defer server.Close()
	for _, engine := range []string{"chromium", "webkit"} {
		t.Run(engine, func(t *testing.T) {
			b, ctx := documentActualBrowser(t, engine, server.URL)
			before := b.BrowserDocumentReceiptEvidence()
			if before == nil || before.Lifetime == nil || !before.Lifetime.CreationBracketed || before.Lifetime.Closed {
				t.Fatal("missing creation bound")
			}
			documentActualExecute(t, b, ctx, &protocol.Action{Type: "browser_navigate", Target: server.URL})
			if err := b.Teardown(context.Background()); err != nil {
				t.Fatal(err)
			}
			after := b.BrowserDocumentReceiptEvidence()
			if after == nil || after.Lifetime == nil || !after.Lifetime.Closed || after.Lifetime.EndNS <= 0 || after.SourceGeneration != before.SourceGeneration {
				t.Fatal("native closure not bracketed")
			}
			for _, r := range after.Receipts {
				if r.HostElapsedNS > after.Lifetime.EndNS {
					t.Fatal("callback outside native lifetime")
				}
			}
			raw, err := json.Marshal(after)
			if err != nil {
				t.Fatal(err)
			}
			restored, err := protocol.DecodeBrowserDocumentReceiptEvidence(raw)
			if err != nil || restored.Lifetime == nil || *restored.Lifetime != *after.Lifetime {
				t.Fatal("lifetime not durable", err)
			}
			frozen := *after.Lifetime
			if err := b.Teardown(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := b.BrowserDocumentReceiptEvidence(); got == nil || *got.Lifetime != frozen {
				t.Fatal("repeated close revised lifetime")
			}
		})
	}
}

type causalBlockingContext struct {
	playwright.BrowserContext
	entered, release chan struct{}
	once             sync.Once
}

func (c *causalBlockingContext) Close(...playwright.BrowserContextCloseOptions) error {
	c.once.Do(func() { close(c.entered) })
	<-c.release
	return nil
}
func TestBrowserDocumentConcurrentCloseCannotPublishEarlyLifetime(t *testing.T) {
	native := &causalBlockingContext{entered: make(chan struct{}), release: make(chan struct{})}
	var release sync.Once
	defer release.Do(func() { close(native.release) })
	b := &Browser{pwContext: native}
	b.applyDefaultConfig()
	b.documentMetricsEnabled = true
	if err := b.resetBrowserEvidence("test"); err != nil {
		t.Fatal(err)
	}
	b.evidence.clock.lifetime = &protocol.BrowserNativeLifetime{CreationBracketed: true}
	done := make(chan error, 2)
	go func() { done <- b.closeWithContext(context.Background()) }()
	<-native.entered
	go func() { done <- b.closeWithContext(context.Background()) }()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	rejected := make(chan error, 1)
	go func() { rejected <- b.closeWithContext(cancelled) }()
	select {
	case err := <-rejected:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("cancelled close", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled waiter blocked behind native close")
	}
	select {
	case err := <-done:
		t.Fatal("close returned before native join", err)
	case <-time.After(25 * time.Millisecond):
	}
	if got := b.BrowserDocumentReceiptEvidence(); got == nil || got.Lifetime.Closed {
		t.Fatal("early closure proof")
	}
	release.Do(func() { close(native.release) })
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if got := b.BrowserDocumentReceiptEvidence(); got == nil || !got.Lifetime.Closed {
		t.Fatal("joined context lacks proof")
	}
}
