package browser

import (
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestRequiresBrowserAssertionHTML(t *testing.T) {
	tests := []struct {
		name       string
		assertions []protocol.Assertion
		want       bool
	}{
		{name: "none", assertions: nil, want: false},
		{name: "response time only", assertions: []protocol.Assertion{{Type: "response_time_ms", Max: 1000}}, want: false},
		{name: "body contains", assertions: []protocol.Assertion{{Type: "body_contains", Value: "ready"}}, want: true},
		{name: "body not contains", assertions: []protocol.Assertion{{Type: "body_not_contains", Value: "error"}}, want: true},
		{name: "later html assertion", assertions: []protocol.Assertion{{Type: "page_title", Value: "Home"}, {Type: "body_contains", Value: "ready"}}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := requiresBrowserAssertionHTML(tt.assertions); got != tt.want {
				t.Fatalf("requiresBrowserAssertionHTML() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEvaluateBrowserAssertionsWithoutPageRuntime(t *testing.T) {
	b := &Browser{}
	result := &protocol.Result{
		Duration:  50 * time.Millisecond,
		HTML:      "<main>ready</main>",
		PageTitle: "Home",
		PageURL:   "https://example.test/home",
	}

	err := b.evaluateBrowserAssertions([]protocol.Assertion{
		{Type: "response_time_ms", Max: 100},
		{Type: "body_contains", Value: "ready"},
		{Type: "body_not_contains", Value: "error"},
		{Type: "page_title", Value: "Home"},
		{Type: "url_contains", Value: "/home"},
	}, result)
	if err != nil {
		t.Fatalf("evaluateBrowserAssertions returned error: %v", err)
	}

	err = b.evaluateBrowserAssertions([]protocol.Assertion{{Type: "page_title"}}, result)
	if err == nil {
		t.Fatal("evaluateBrowserAssertions missing page_title value returned nil error")
	}
}
