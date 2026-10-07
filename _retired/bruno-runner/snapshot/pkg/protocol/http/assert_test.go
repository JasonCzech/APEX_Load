package http

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestEvaluate_StatusCode_Pass(t *testing.T) {
	r := &protocol.Result{StatusCode: 200}
	err := evaluate(protocol.Assertion{Type: "status_code", Expected: float64(200)}, r, "")
	if err != nil {
		t.Errorf("expected pass, got: %v", err)
	}
}

func TestEvaluate_StatusCode_Fail(t *testing.T) {
	r := &protocol.Result{StatusCode: 404}
	err := evaluate(protocol.Assertion{Type: "status_code", Expected: float64(200)}, r, "")
	if err == nil {
		t.Error("expected failure for status 404 vs expected 200")
	}
}

func TestEvaluate_StatusCode_IntExpected(t *testing.T) {
	r := &protocol.Result{StatusCode: 200}
	err := evaluate(protocol.Assertion{Type: "status_code", Expected: 200}, r, "")
	if err != nil {
		t.Errorf("int expected should work: %v", err)
	}
}

func TestEvaluate_StatusCode_InvalidExpected(t *testing.T) {
	r := &protocol.Result{StatusCode: 200}
	err := evaluate(protocol.Assertion{Type: "status_code", Expected: "not-a-number"}, r, "")
	if err == nil {
		t.Error("expected error for non-integer expected value")
	}
}

func TestEvaluate_StatusCodeRange_Pass(t *testing.T) {
	r := &protocol.Result{StatusCode: 204}
	err := evaluate(protocol.Assertion{Type: "status_code_range", Min: 200, Max: 299}, r, "")
	if err != nil {
		t.Errorf("expected pass, got: %v", err)
	}
}

func TestEvaluate_StatusCodeRange_Fail(t *testing.T) {
	r := &protocol.Result{StatusCode: 500}
	err := evaluate(protocol.Assertion{Type: "status_code_range", Min: 200, Max: 299}, r, "")
	if err == nil {
		t.Error("expected failure for status 500 outside 200-299")
	}
}

func TestEvaluate_StatusCodeRange_InvalidBounds(t *testing.T) {
	r := &protocol.Result{StatusCode: 200}
	err := evaluate(protocol.Assertion{Type: "status_code_range", Min: 300, Max: 200}, r, "")
	if err == nil {
		t.Error("expected error for invalid status range")
	}
}

func TestResponseStatusExplicitlyAcceptedIncludesNegativeStatusAssertion(t *testing.T) {
	result := &protocol.Result{StatusCode: http.StatusNotFound}
	if !responseStatusExplicitlyAccepted([]protocol.Assertion{{
		Type:     "status_code_not",
		Expected: http.StatusInternalServerError,
	}}, result) {
		t.Fatal("status_code_not accepting a recorded client error did not override the default 2xx/3xx policy")
	}
	if responseStatusExplicitlyAccepted([]protocol.Assertion{{
		Type:     "status_code_not",
		Expected: http.StatusNotFound,
	}}, result) {
		t.Fatal("status_code_not accepted the explicitly forbidden response status")
	}
}

func TestEvaluate_ResponseTime_Pass(t *testing.T) {
	r := &protocol.Result{Duration: 100 * time.Millisecond}
	err := evaluate(protocol.Assertion{Type: "response_time_ms", Max: 500}, r, "")
	if err != nil {
		t.Errorf("expected pass: %v", err)
	}
}

func TestEvaluate_ResponseTime_Fail(t *testing.T) {
	r := &protocol.Result{Duration: 600 * time.Millisecond}
	err := evaluate(protocol.Assertion{Type: "response_time_ms", Max: 500}, r, "")
	if err == nil {
		t.Error("expected failure for response time 600ms > max 500ms")
	}
}

func TestEvaluate_BodyContains_Pass(t *testing.T) {
	r := &protocol.Result{}
	err := evaluate(protocol.Assertion{Type: "body_contains", Value: "success"}, r, `{"status":"success"}`)
	if err != nil {
		t.Errorf("expected pass: %v", err)
	}
}

func TestEvaluate_BodyContains_Fail(t *testing.T) {
	r := &protocol.Result{}
	err := evaluate(protocol.Assertion{Type: "body_contains", Value: "success"}, r, `{"status":"error"}`)
	if err == nil {
		t.Error("expected failure when body doesn't contain value")
	}
}

func TestEvaluate_BodyNotContains_Pass(t *testing.T) {
	r := &protocol.Result{}
	err := evaluate(protocol.Assertion{Type: "body_not_contains", Value: "error"}, r, `{"status":"ok"}`)
	if err != nil {
		t.Errorf("expected pass: %v", err)
	}
}

func TestEvaluate_BodyNotContains_Fail(t *testing.T) {
	r := &protocol.Result{}
	err := evaluate(protocol.Assertion{Type: "body_not_contains", Value: "error"}, r, `{"error":"bad request"}`)
	if err == nil {
		t.Error("expected failure when body contains unexpected value")
	}
}

func TestEvaluate_HeaderContains(t *testing.T) {
	headers := http.Header{}
	headers.Add("Content-Type", "application/json")
	headers.Add("X-Trace", "first-value")
	headers.Add("X-Trace", "second-Needle-value")
	headers["x-trace"] = []string{"third-MixedCase-value"}

	tests := []struct {
		name      string
		assertion protocol.Assertion
		wantError string
	}{
		{
			name:      "case insensitive header name and any value",
			assertion: protocol.Assertion{Type: "header_contains", Value: "x-TRACE", Expected: "Needle"},
		},
		{
			name:      "aggregates differently cased map keys",
			assertion: protocol.Assertion{Type: "header_contains", Value: "X-Trace", Expected: "MixedCase"},
		},
		{
			name:      "substring remains case sensitive",
			assertion: protocol.Assertion{Type: "header_contains", Value: "X-Trace", Expected: "needle"},
			wantError: "does not contain",
		},
		{
			name:      "missing header",
			assertion: protocol.Assertion{Type: "header_contains", Value: "X-Missing", Expected: ""},
			wantError: "is missing",
		},
		{
			name:      "header name required",
			assertion: protocol.Assertion{Type: "header_contains", Value: " ", Expected: "value"},
			wantError: "header name is required",
		},
		{
			name:      "expected must be string",
			assertion: protocol.Assertion{Type: "header_contains", Value: "Content-Type", Expected: 1},
			wantError: "expected string",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := evaluateResponseAssertion(tt.assertion, &protocol.Result{}, headers, "")
			if tt.wantError == "" {
				if err != nil {
					t.Fatalf("evaluateResponseAssertion returned error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("error = %v, want containing %q", err, tt.wantError)
			}
		})
	}
}

func TestEvaluate_BodyRegexCaptureEquals(t *testing.T) {
	tests := []struct {
		name      string
		assertion protocol.Assertion
		body      string
		wantError string
	}{
		{
			name:      "first capture group",
			assertion: protocol.Assertion{Type: "body_regex_capture_equals", Value: `<code>([^<]+)</code>`, Expected: "500"},
			body:      `<code>500</code>`,
		},
		{
			name:      "first capture group wins when pattern has several",
			assertion: protocol.Assertion{Type: "body_regex_capture_equals", Value: `<code>([^<]+)</code><description>([^<]+)</description>`, Expected: "500"},
			body:      `<code>500</code><description>failure</description>`,
		},
		{
			name:      "full match without capture group",
			assertion: protocol.Assertion{Type: "body_regex_capture_equals", Value: `code-\d+`, Expected: "code-500"},
			body:      `before code-500 after`,
		},
		{
			name:      "participating empty first capture may equal empty string",
			assertion: protocol.Assertion{Type: "body_regex_capture_equals", Value: `(a*)b`, Expected: ""},
			body:      `b`,
		},
		{
			name:      "unmatched optional first capture does not equal empty string",
			assertion: protocol.Assertion{Type: "body_regex_capture_equals", Value: `(a)?b`, Expected: ""},
			body:      `b`,
			wantError: "first capture group did not participate",
		},
		{
			name:      "later capture cannot substitute for unmatched first capture",
			assertion: protocol.Assertion{Type: "body_regex_capture_equals", Value: `(a)?(b)`, Expected: ""},
			body:      `b`,
			wantError: "first capture group did not participate",
		},
		{
			name:      "only the first match is compared",
			assertion: protocol.Assertion{Type: "body_regex_capture_equals", Value: `<code>([^<]+)</code>`, Expected: "500"},
			body:      `<code>400</code><code>500</code>`,
			wantError: "does not equal",
		},
		{
			name:      "no match",
			assertion: protocol.Assertion{Type: "body_regex_capture_equals", Value: `<code>([^<]+)</code>`, Expected: "500"},
			body:      `<description>failure</description>`,
			wantError: "does not match",
		},
		{
			name:      "invalid RE2 pattern",
			assertion: protocol.Assertion{Type: "body_regex_capture_equals", Value: `(`, Expected: ""},
			wantError: "invalid pattern",
		},
		{
			name:      "pattern over four KiB",
			assertion: protocol.Assertion{Type: "body_regex_capture_equals", Value: strings.Repeat("a", maxHTTPAssertionRegexBytes+1), Expected: ""},
			wantError: "pattern exceeds 4096 bytes",
		},
		{
			name:      "expected must be string",
			assertion: protocol.Assertion{Type: "body_regex_capture_equals", Value: `ok`, Expected: 1},
			body:      "ok",
			wantError: "expected string",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := evaluateResponseAssertion(tt.assertion, &protocol.Result{}, nil, tt.body)
			if tt.wantError == "" {
				if err != nil {
					t.Fatalf("evaluateResponseAssertion returned error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("error = %v, want containing %q", err, tt.wantError)
			}
		})
	}
}

func TestEvaluate_BodyRegexCaptureEqualsAcceptsMaximumPatternSize(t *testing.T) {
	pattern := strings.Repeat("a", maxHTTPAssertionRegexBytes)
	if err := evaluateResponseAssertion(
		protocol.Assertion{Type: "body_regex_capture_equals", Value: pattern, Expected: pattern},
		&protocol.Result{},
		nil,
		pattern,
	); err != nil {
		t.Fatalf("maximum-size pattern returned error: %v", err)
	}
}

func TestAssertionNeedsResponseBody(t *testing.T) {
	tests := []struct {
		assertionType string
		want          bool
	}{
		{assertionType: "body_contains", want: true},
		{assertionType: "body_not_contains", want: true},
		{assertionType: "body_regex_capture_equals", want: true},
		{assertionType: "header_contains", want: false},
		{assertionType: "status_code", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.assertionType, func(t *testing.T) {
			if got := assertionNeedsResponseBody(protocol.Assertion{Type: tt.assertionType}); got != tt.want {
				t.Fatalf("assertionNeedsResponseBody(%q) = %t, want %t", tt.assertionType, got, tt.want)
			}
		})
	}
}

func TestValidateHTTPAssertionDefinition(t *testing.T) {
	tests := []struct {
		name      string
		assertion protocol.Assertion
		wantError string
	}{
		{
			name:      "valid header",
			assertion: protocol.Assertion{Type: "header_contains", Value: "Content-Type", Expected: "xml"},
		},
		{
			name:      "valid status",
			assertion: protocol.Assertion{Type: "status_code", Expected: float64(500)},
		},
		{
			name:      "fractional status",
			assertion: protocol.Assertion{Type: "status_code", Expected: 500.5},
			wantError: "expected integer",
		},
		{
			name:      "invalid status range",
			assertion: protocol.Assertion{Type: "status_code_range", Min: 500, Max: 400},
			wantError: "exceeds",
		},
		{
			name:      "empty body check",
			assertion: protocol.Assertion{Type: "body_contains"},
			wantError: "value is required",
		},
		{
			name:      "unknown type",
			assertion: protocol.Assertion{Type: "status_cod"},
			wantError: "unsupported assertion type",
		},
		{
			name:      "header missing name",
			assertion: protocol.Assertion{Type: "header_contains", Expected: "xml"},
			wantError: "header name is required",
		},
		{
			name:      "invalid regex",
			assertion: protocol.Assertion{Type: "body_regex_capture_equals", Value: `(`, Expected: ""},
			wantError: "invalid pattern",
		},
		{
			name:      "oversized regex",
			assertion: protocol.Assertion{Type: "body_regex_capture_equals", Value: strings.Repeat("x", maxHTTPAssertionRegexBytes+1), Expected: ""},
			wantError: "pattern exceeds 4096 bytes",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateHTTPAssertionDefinition(tt.assertion)
			if tt.wantError == "" {
				if err != nil {
					t.Fatalf("validateHTTPAssertionDefinition returned error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("error = %v, want containing %q", err, tt.wantError)
			}
		})
	}
}

func TestValidateHTTPAssertionDefinitionCachesRegex(t *testing.T) {
	pattern := `bruno-native-assertion-cache-(\d+)`
	if err := validateHTTPAssertionDefinition(protocol.Assertion{
		Type:     "body_regex_capture_equals",
		Value:    pattern,
		Expected: "42",
	}); err != nil {
		t.Fatalf("validateHTTPAssertionDefinition returned error: %v", err)
	}
	validated, ok := compiledRegexCache.Load(pattern)
	if !ok || validated == nil {
		t.Fatal("validated assertion regex was not retained in the bounded compiled-regex cache")
	}

	if err := evaluateResponseAssertion(
		protocol.Assertion{Type: "body_regex_capture_equals", Value: pattern, Expected: "42"},
		&protocol.Result{},
		nil,
		"bruno-native-assertion-cache-42",
	); err != nil {
		t.Fatalf("evaluateResponseAssertion returned error: %v", err)
	}
	evaluated, ok := compiledRegexCache.Load(pattern)
	if !ok || evaluated != validated {
		t.Fatal("assertion evaluation did not reuse the validated compiled regex")
	}
}

func TestAssertionInputWorkBudget(t *testing.T) {
	t.Run("body scans are charged cumulatively", func(t *testing.T) {
		budget := &assertionInputWorkBudget{limit: 10, bodyBytes: 6}
		assertion := protocol.Assertion{Type: "body_regex_capture_equals"}
		if err := budget.charge(assertion); err != nil {
			t.Fatalf("first charge returned error: %v", err)
		}
		err := budget.charge(assertion)
		if !errors.Is(err, errHTTPAssertionResourceLimit) {
			t.Fatalf("second charge error = %v, want assertion resource limit", err)
		}
	})

	t.Run("header scans and body scans share the limit", func(t *testing.T) {
		budget := &assertionInputWorkBudget{limit: 10, bodyBytes: 7, headerBytes: 4}
		if err := budget.charge(protocol.Assertion{Type: "header_contains"}); err != nil {
			t.Fatalf("header charge returned error: %v", err)
		}
		err := budget.charge(protocol.Assertion{Type: "body_contains"})
		if !errors.Is(err, errHTTPAssertionResourceLimit) {
			t.Fatalf("body charge error = %v, want assertion resource limit", err)
		}
	})

	t.Run("constant-time assertions are free", func(t *testing.T) {
		budget := &assertionInputWorkBudget{limit: 1, used: 1, bodyBytes: 100}
		if err := budget.charge(protocol.Assertion{Type: "status_code"}); err != nil {
			t.Fatalf("status assertion charge returned error: %v", err)
		}
	})

	t.Run("default permits two maximum response passes", func(t *testing.T) {
		budget := &assertionInputWorkBudget{
			limit:     maxHTTPAssertionInputWorkBytes,
			bodyBytes: hardHTTPMaxBodyBytes,
		}
		assertion := protocol.Assertion{Type: "body_not_contains"}
		if err := budget.charge(assertion); err != nil {
			t.Fatalf("first maximum body charge returned error: %v", err)
		}
		if err := budget.charge(assertion); err != nil {
			t.Fatalf("second maximum body charge returned error: %v", err)
		}
		err := budget.charge(assertion)
		if !errors.Is(err, errHTTPAssertionResourceLimit) {
			t.Fatalf("third maximum body charge error = %v, want assertion resource limit", err)
		}
	})
}

func TestEvaluate_UnknownType(t *testing.T) {
	r := &protocol.Result{}
	err := evaluate(protocol.Assertion{Type: "xpath_match"}, r, "")
	if err == nil {
		t.Error("expected error for unsupported assertion type")
	}
}

func TestToInt(t *testing.T) {
	tests := []struct {
		input any
		want  int
		ok    bool
	}{
		{42, 42, true},
		{float64(200), 200, true},
		{int64(300), 300, true},
		{"not-a-number", 0, false},
		{nil, 0, false},
	}

	for _, tt := range tests {
		got, ok := toInt(tt.input)
		if ok != tt.ok || got != tt.want {
			t.Errorf("toInt(%v) = (%d, %v), want (%d, %v)", tt.input, got, ok, tt.want, tt.ok)
		}
	}
}
