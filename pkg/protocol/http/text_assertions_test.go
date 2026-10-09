package http

import (
	"net/http"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestTextAssertions(t *testing.T) {
	headers := http.Header{"X-Status": {"ok"}, "Content-Type": {"text/html"}}
	body := "<title>Welcome</title> Order 42"
	tests := []struct {
		name      string
		assertion protocol.Assertion
		pass      bool
	}{
		{"body contains", protocol.Assertion{Type: "text_contains", Value: "Order 42"}, true},
		{"headers block line", protocol.Assertion{Type: "text_contains", Value: "X-Status: ok\r\n", Source: "headers"}, true},
		{"headers do not include the body", protocol.Assertion{Type: "text_contains", Value: "Welcome", Source: "headers"}, false},
		{"all spans headers and body", protocol.Assertion{Type: "text_matches", Value: `(?s)X-Status: ok.*Order [0-9]+`, Source: "all"}, true},
		{"case-insensitive pattern", protocol.Assertion{Type: "text_matches", Value: `welcome`, CaseInsensitive: true}, true},
		{"not contains", protocol.Assertion{Type: "text_not_contains", Value: "Error"}, true},
		{"not contains fails when found", protocol.Assertion{Type: "text_not_contains", Value: "Order"}, false},
		{"not matches fails when matched", protocol.Assertion{Type: "text_not_matches", Value: `Order \d+`}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateAssertionDefinition(tt.assertion); err != nil {
				t.Fatalf("definition rejected: %v", err)
			}
			err := evaluateResponseAssertion(tt.assertion, &protocol.Result{}, headers, body)
			if (err == nil) != tt.pass {
				t.Fatalf("err = %v, want pass=%v", err, tt.pass)
			}
		})
	}
}

func TestTextAssertionDefinitions(t *testing.T) {
	for _, tt := range []struct {
		assertion protocol.Assertion
		want      string
	}{
		{protocol.Assertion{Type: "text_contains"}, "value is required"},
		{protocol.Assertion{Type: "text_matches", Value: "("}, "invalid pattern"},
		{protocol.Assertion{Type: "text_contains", Value: "x", Source: "cookies"}, "source must be body, headers or all"},
		{protocol.Assertion{Type: "body_contains", Value: "x", Source: "headers"}, "source applies only to text assertions"},
	} {
		if err := ValidateAssertionDefinition(tt.assertion); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%+v: err = %v, want %q", tt.assertion, err, tt.want)
		}
	}
	if assertionNeedsResponseBody(protocol.Assertion{Type: "text_contains", Value: "x", Source: "headers"}) {
		t.Error("a headers-only check must not load the body")
	}
	budget := newAssertionInputWorkBudget(http.Header{"A": {"b"}}, []byte("body"))
	if err := budget.charge(protocol.Assertion{Type: "text_contains", Value: "x", Source: "all"}); err != nil || budget.used != budget.headerBytes+budget.bodyBytes {
		t.Fatalf("all-source charge = %d (%v), want headers plus body", budget.used, err)
	}
}
