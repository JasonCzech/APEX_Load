package grafana

import (
	"strings"
	"testing"
)

func TestResolvePanelExprBuiltins(t *testing.T) {
	q := QueryResolution{StepSeconds: 15, RangeSeconds: 3600}
	got, err := ResolvePanelExpr("rate(x[$__interval]) / $__range_s", q)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "rate(x[15s]) / 3600" {
		t.Fatalf("got %q", got)
	}
}

func TestResolvePanelExprRateInterval(t *testing.T) {
	q := QueryResolution{StepSeconds: 5, RangeSeconds: 60}
	got, err := ResolvePanelExpr("rate(x[$__rate_interval])", q)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	// max(4*5, 60) = 60s
	if got != "rate(x[60s])" {
		t.Fatalf("got %q", got)
	}
}

func TestResolvePanelExprReservedAndUserBindings(t *testing.T) {
	q := QueryResolution{
		StepSeconds:  15,
		RangeSeconds: 60,
		TestID:       "run-123",
		Bindings:     map[string]string{"env": "prod"},
	}
	got, err := ResolvePanelExpr(`http_requests_total{test_id="$test_id",env="${env}"}`, q)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != `http_requests_total{test_id="run-123",env="prod"}` {
		t.Fatalf("got %q", got)
	}
}

func TestResolvePanelExprRejectsUnsafeBindingValue(t *testing.T) {
	q := QueryResolution{StepSeconds: 15, RangeSeconds: 60, Bindings: map[string]string{"env": `x"} or vector(1) #`}}
	if _, err := ResolvePanelExpr(`up{env="$env"}`, q); err == nil {
		t.Fatalf("expected error for injection binding value")
	}
}

func TestResolvePanelExprRejectsReservedBindingOverride(t *testing.T) {
	q := QueryResolution{ProjectID: "project-a", Bindings: map[string]string{"project_id": "project-b"}}
	if _, err := ResolvePanelExpr(`up{project_id="$project_id"}`, q); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("expected reserved binding error, got %v", err)
	}
	q = QueryResolution{Bindings: map[string]string{"__interval": "1h"}}
	if _, err := ResolvePanelExpr(`rate(up[$__interval])`, q); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("expected builtin binding error, got %v", err)
	}
}

func TestResolvePanelExprRejectsUnsafeTestID(t *testing.T) {
	// test_id is a request parameter, so an injection payload must be rejected the
	// same way an unsafe binding value is — not spliced into the query.
	q := QueryResolution{StepSeconds: 15, RangeSeconds: 60, TestID: `x"} or vector(1) #`}
	if _, err := ResolvePanelExpr(`up{test_id="$test_id"}`, q); err == nil {
		t.Fatalf("expected error for injection test_id")
	}
	// A well-formed test id (uuid-style, with hyphens/colons) still resolves.
	ok := QueryResolution{StepSeconds: 15, RangeSeconds: 60, TestID: "run-2026:abc-123"}
	got, err := ResolvePanelExpr(`up{test_id="$test_id"}`, ok)
	if err != nil || got != `up{test_id="run-2026:abc-123"}` {
		t.Fatalf("valid test id: got %q err %v", got, err)
	}
}

func TestResolvePanelExprRejectsUnresolvedToken(t *testing.T) {
	q := QueryResolution{StepSeconds: 15, RangeSeconds: 60}
	if _, err := ResolvePanelExpr(`up{env="$env"}`, q); err == nil {
		t.Fatalf("expected error for unresolved $env")
	}
}

func TestResolvePanelExprLongestMatchWins(t *testing.T) {
	// $__rate_interval must not be shadowed by $__range or $__interval.
	q := QueryResolution{StepSeconds: 30, RangeSeconds: 300}
	got, err := ResolvePanelExpr("a[$__rate_interval] b[$__interval]", q)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "a[120s] b[30s]" {
		t.Fatalf("got %q", got)
	}
}

func TestValidateBindingValue(t *testing.T) {
	valid := []string{"prod", "us-east-1", "10.0.0.1", "a.b:c/d=e+f", strings.Repeat("a", 128)}
	for _, v := range valid {
		if !ValidateBindingValue(v) {
			t.Fatalf("expected %q valid", v)
		}
	}
	invalid := []string{"", "has space", `quote"`, "brace{", "pipe|x", "comma,x", strings.Repeat("a", 129)}
	for _, v := range invalid {
		if ValidateBindingValue(v) {
			t.Fatalf("expected %q invalid", v)
		}
	}
}

func TestScanExprVariables(t *testing.T) {
	refs := scanExprVariables(`up{a="$env",b="${dc}",c="[[zone]]",d="${x:regex}"} + $__interval`)
	names := map[string]string{}
	for _, r := range refs {
		names[r.Name] = r.Modifier
	}
	for _, want := range []string{"env", "dc", "zone", "x", "__interval"} {
		if _, ok := names[want]; !ok {
			t.Fatalf("expected variable %q in %v", want, names)
		}
	}
	if names["x"] != "regex" {
		t.Fatalf("expected x modifier regex, got %q", names["x"])
	}
}
