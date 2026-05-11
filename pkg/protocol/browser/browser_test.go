package browser

import (
	"context"
	"testing"

	"github.com/jasonczech/stormrunner/pkg/protocol"
)

func TestBrowserProtocolRegistered(t *testing.T) {
	p, err := protocol.Get("browser")
	if err != nil {
		t.Fatalf("Get(browser) returned error: %v", err)
	}
	if p.Name() != "browser" {
		t.Fatalf("Name() = %q, want browser", p.Name())
	}
}

func TestBrowserProtocolExecuteReturnsScaffoldError(t *testing.T) {
	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{"base_url": "https://example.com"}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}

	result, err := b.Execute(context.Background(), &protocol.Action{
		Name:   "Open homepage",
		Type:   "browser_navigate",
		Target: "/",
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.PageURL != "https://example.com/" {
		t.Fatalf("PageURL = %q, want https://example.com/", result.PageURL)
	}
	if result.Success {
		t.Fatal("Success = true, want false while scaffold runtime is incomplete")
	}
	if result.ErrorMessage != scaffoldError {
		t.Fatalf("ErrorMessage = %q, want %q", result.ErrorMessage, scaffoldError)
	}
}