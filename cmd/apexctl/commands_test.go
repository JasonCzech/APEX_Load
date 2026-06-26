package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIsTerminalStatus(t *testing.T) {
	terminal := []string{"COMPLETE", "FAILED", "ABORTED"}
	for _, s := range terminal {
		if !isTerminalStatus(s) {
			t.Fatalf("%s should be terminal", s)
		}
	}
	for _, s := range []string{"RUNNING", "QUEUED", "PENDING", ""} {
		if isTerminalStatus(s) {
			t.Fatalf("%s should not be terminal", s)
		}
	}
}

func TestGetTestStatusNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	client := NewApiClient(srv.URL)
	if _, err := client.GetTestStatus("missing"); err == nil {
		t.Fatal("expected error for 404")
	}
}

func TestParseCtlArgsAuthFlags(t *testing.T) {
	args := parseCtlArgs([]string{"--api", "http://localhost:8080", "--api-key", "key-123", "--bearer-token", "token-456", "--test", "t1"})
	if args.api != "http://localhost:8080" || args.apiKey != "key-123" || args.bearerToken != "token-456" || args.test != "t1" {
		t.Fatalf("ctl args = %#v", args)
	}
}

// testController returns a handler that reports the given status and SLA report.
func testController(t *testing.T, status string, slaBreached bool, details []string) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/tests/t1/sla-status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body := `{"test_id":"t1","status":"` + status + `","sla_breached":` + boolStr(slaBreached)
		if len(details) > 0 {
			body += `,"details":["` + strings.Join(details, `","`) + `"]`
		}
		body += `}`
		_, _ = w.Write([]byte(body))
	})
	mux.HandleFunc("/api/v1/tests/t1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"t1","status":"` + status + `","live_metrics":{"total_transactions":1000,"total_errors":5,"tps":42.5}}`))
	})
	return mux
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func TestWaitForTestCompletePassing(t *testing.T) {
	srv := httptest.NewServer(testController(t, "COMPLETE", false, nil))
	defer srv.Close()

	client := NewApiClient(srv.URL)
	res, err := client.WaitForTest("t1", "json", 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.TotalTransactions != 1000 || res.TotalErrors != 5 {
		t.Fatalf("metrics not resolved: %+v", res)
	}
	if res.SLASummary != nil && res.SLASummary.Breached {
		t.Fatalf("expected no SLA breach, got %+v", res.SLASummary)
	}
}

func TestWaitForTestSLABreach(t *testing.T) {
	srv := httptest.NewServer(testController(t, "COMPLETE", true, []string{"P95 latency 900ms exceeded 800ms"}))
	defer srv.Close()

	client := NewApiClient(srv.URL)
	res, err := client.WaitForTest("t1", "json", 5*time.Second)
	if err != nil {
		t.Fatalf("a breach is not an execution error; got %v", err)
	}
	if res.SLASummary == nil || !res.SLASummary.Breached {
		t.Fatalf("expected SLA breach recorded, got %+v", res.SLASummary)
	}
	if len(res.SLASummary.Details) != 1 {
		t.Fatalf("expected 1 detail, got %+v", res.SLASummary.Details)
	}
}

func TestWaitForTestFailedStatus(t *testing.T) {
	srv := httptest.NewServer(testController(t, "FAILED", false, nil))
	defer srv.Close()

	client := NewApiClient(srv.URL)
	_, err := client.WaitForTest("t1", "json", 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), "failed") {
		t.Fatalf("expected failure error, got %v", err)
	}
}

func TestWaitForTestTimeout(t *testing.T) {
	srv := httptest.NewServer(testController(t, "RUNNING", false, nil))
	defer srv.Close()

	client := NewApiClient(srv.URL)
	// Deadline already elapsed on the first poll, so this returns promptly.
	_, err := client.WaitForTest("t1", "json", time.Nanosecond)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout error, got %v", err)
	}
}

func TestParseCtlArgs(t *testing.T) {
	c := parseCtlArgs([]string{"--api", "http://x:8080", "--test", "abc", "--format", "json", "--junit", "out.xml", "--timeout", "5m"})
	if c.api != "http://x:8080" || c.test != "abc" || c.format != "json" || c.junit != "out.xml" {
		t.Fatalf("parse mismatch: %+v", c)
	}
	if c.timeout != 5*time.Minute {
		t.Fatalf("timeout = %v, want 5m", c.timeout)
	}
}

func TestParseCtlArgsDefaults(t *testing.T) {
	c := parseCtlArgs(nil)
	if c.format != "text" {
		t.Fatalf("default format = %q, want text", c.format)
	}
	if c.timeout != 30*time.Minute {
		t.Fatalf("default timeout = %v, want 30m", c.timeout)
	}
}
