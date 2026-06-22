package http

import (
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestEvaluate_StatusCode_Pass(t *testing.T) {
	r := &protocol.Result{StatusCode: 200}
	err := evaluate(protocol.Assertion{Type: "status_code", Expected: float64(200)}, r, nil)
	if err != nil {
		t.Errorf("expected pass, got: %v", err)
	}
}

func TestEvaluate_StatusCode_Fail(t *testing.T) {
	r := &protocol.Result{StatusCode: 404}
	err := evaluate(protocol.Assertion{Type: "status_code", Expected: float64(200)}, r, nil)
	if err == nil {
		t.Error("expected failure for status 404 vs expected 200")
	}
}

func TestEvaluate_StatusCode_IntExpected(t *testing.T) {
	r := &protocol.Result{StatusCode: 200}
	err := evaluate(protocol.Assertion{Type: "status_code", Expected: 200}, r, nil)
	if err != nil {
		t.Errorf("int expected should work: %v", err)
	}
}

func TestEvaluate_StatusCode_InvalidExpected(t *testing.T) {
	r := &protocol.Result{StatusCode: 200}
	err := evaluate(protocol.Assertion{Type: "status_code", Expected: "not-a-number"}, r, nil)
	if err == nil {
		t.Error("expected error for non-integer expected value")
	}
}

func TestEvaluate_StatusCodeRange_Pass(t *testing.T) {
	r := &protocol.Result{StatusCode: 204}
	err := evaluate(protocol.Assertion{Type: "status_code_range", Min: 200, Max: 299}, r, nil)
	if err != nil {
		t.Errorf("expected pass, got: %v", err)
	}
}

func TestEvaluate_StatusCodeRange_Fail(t *testing.T) {
	r := &protocol.Result{StatusCode: 500}
	err := evaluate(protocol.Assertion{Type: "status_code_range", Min: 200, Max: 299}, r, nil)
	if err == nil {
		t.Error("expected failure for status 500 outside 200-299")
	}
}

func TestEvaluate_StatusCodeRange_InvalidBounds(t *testing.T) {
	r := &protocol.Result{StatusCode: 200}
	err := evaluate(protocol.Assertion{Type: "status_code_range", Min: 300, Max: 200}, r, nil)
	if err == nil {
		t.Error("expected error for invalid status range")
	}
}

func TestEvaluate_ResponseTime_Pass(t *testing.T) {
	r := &protocol.Result{Duration: 100 * time.Millisecond}
	err := evaluate(protocol.Assertion{Type: "response_time_ms", Max: 500}, r, nil)
	if err != nil {
		t.Errorf("expected pass: %v", err)
	}
}

func TestEvaluate_ResponseTime_Fail(t *testing.T) {
	r := &protocol.Result{Duration: 600 * time.Millisecond}
	err := evaluate(protocol.Assertion{Type: "response_time_ms", Max: 500}, r, nil)
	if err == nil {
		t.Error("expected failure for response time 600ms > max 500ms")
	}
}

func TestEvaluate_BodyContains_Pass(t *testing.T) {
	r := &protocol.Result{}
	err := evaluate(protocol.Assertion{Type: "body_contains", Value: "success"}, r, []byte(`{"status":"success"}`))
	if err != nil {
		t.Errorf("expected pass: %v", err)
	}
}

func TestEvaluate_BodyContains_Fail(t *testing.T) {
	r := &protocol.Result{}
	err := evaluate(protocol.Assertion{Type: "body_contains", Value: "success"}, r, []byte(`{"status":"error"}`))
	if err == nil {
		t.Error("expected failure when body doesn't contain value")
	}
}

func TestEvaluate_BodyNotContains_Pass(t *testing.T) {
	r := &protocol.Result{}
	err := evaluate(protocol.Assertion{Type: "body_not_contains", Value: "error"}, r, []byte(`{"status":"ok"}`))
	if err != nil {
		t.Errorf("expected pass: %v", err)
	}
}

func TestEvaluate_BodyNotContains_Fail(t *testing.T) {
	r := &protocol.Result{}
	err := evaluate(protocol.Assertion{Type: "body_not_contains", Value: "error"}, r, []byte(`{"error":"bad request"}`))
	if err == nil {
		t.Error("expected failure when body contains unexpected value")
	}
}

func TestEvaluate_UnknownType(t *testing.T) {
	r := &protocol.Result{}
	err := evaluate(protocol.Assertion{Type: "xpath_match"}, r, nil)
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
