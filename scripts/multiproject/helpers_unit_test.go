package multiproject

import (
	"fmt"
	"net/http"
	"reflect"
	"testing"
)

func TestRetryableUnavailableOnlyRetriesPreHandlerRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want bool
	}{
		{"router has no admitted leader (HTML body)", "<html><body><h1>503 Service Unavailable</h1></body></html>", true},
		{"empty body", "", true},
		{"follower refuses an execution mutation", `{"error":"this controller replica is not the active execution leader; retry against the active controller"}`, true},
		{"start admission asks for a retry", `{"error":"admission evidence changed","code":"start_admission_temporarily_unavailable"}`, true},
		{"a handler's own failure", `{"error":"system settings audit is unavailable; governed settings were not changed"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := retryableUnavailable([]byte(tc.body)); got != tc.want {
				t.Fatalf("retryableUnavailable(%q) = %v, want %v", tc.body, got, tc.want)
			}
		})
	}
}

func TestReportRequestCountsPrefersTheRequestPopulation(t *testing.T) {
	report := map[string]any{
		"metric_populations": map[string]any{
			"action":      map[string]any{"transactions": float64(36), "errors": float64(0)},
			"transaction": map[string]any{"transactions": float64(99)},
		},
		"by_request": map[string]any{"mpalpha-load": map[string]any{"transactions": float64(36)}},
		"by_action":  map[string]any{"mp-1-alpha target traffic": map[string]any{"transactions": float64(36)}},
	}
	requests, errorsCount, names := reportRequestCounts(report)
	if requests == nil || *requests != 36 || errorsCount == nil || *errorsCount != 0 {
		t.Fatalf("requests=%v errors=%v, want 36 and 0 from metric_populations.action", requests, errorsCount)
	}
	if want := []string{"mp-1-alpha target traffic", "mpalpha-load"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
}

func TestReportRequestCountsFallsBackToByRequest(t *testing.T) {
	report := map[string]any{"by_request": map[string]any{
		"one": map[string]any{"transactions": float64(10), "errors": float64(1)},
		"two": map[string]any{"transactions": float64(5)},
	}}
	requests, errorsCount, _ := reportRequestCounts(report)
	if requests == nil || *requests != 15 || errorsCount == nil || *errorsCount != 1 {
		t.Fatalf("requests=%v errors=%v, want 15 and 1 summed from by_request", requests, errorsCount)
	}
	if requests, _, _ := reportRequestCounts(map[string]any{}); requests != nil {
		t.Fatalf("an empty report must have no request population, got %d", *requests)
	}
}

func TestStatusOfReadsWrappedAPIErrors(t *testing.T) {
	err := fmt.Errorf("start: %w", &apiError{Method: http.MethodPost, Path: "/x", Status: http.StatusConflict, Body: "{}"})
	if got := statusOf(err); got != http.StatusConflict {
		t.Fatalf("statusOf = %d, want 409", got)
	}
	if got := statusOf(fmt.Errorf("dial tcp: refused")); got != 0 {
		t.Fatalf("statusOf(non-API error) = %d, want 0", got)
	}
}

func TestSmallHelpers(t *testing.T) {
	for raw, want := range map[string]bool{"1": true, "TRUE": true, "yes": true, "on": true, "0": false, "": false, "nope": false} {
		if got := envBool(raw); got != want {
			t.Errorf("envBool(%q) = %v, want %v", raw, got, want)
		}
	}
	if got := firstNonEmpty("", "  ", "b", "c"); got != "b" {
		t.Errorf("firstNonEmpty = %q, want b", got)
	}
	if got := truncate("  abcdef  ", 3); got != "abc…" {
		t.Errorf("truncate = %q, want abc…", got)
	}
	running := map[string]bool{"a": true, "b": true}
	if !allRunning(running, []string{"a", "b"}) || allRunning(running, []string{"a", "c"}) {
		t.Error("allRunning must require every listed run")
	}
}
