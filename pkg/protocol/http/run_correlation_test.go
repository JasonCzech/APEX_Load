package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestManagedRunHeaderApprovedOriginsAndRedirects(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "off by default", true: "approved origin only"}[enabled], func(t *testing.T) {
			var first, redirected string
			target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				redirected = r.Header.Get(ManagedRunHeader)
				w.WriteHeader(http.StatusOK)
			}))
			defer target.Close()
			origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				first = r.Header.Get(ManagedRunHeader)
				http.Redirect(w, r, target.URL, http.StatusFound)
			}))
			defer origin.Close()
			cfg := map[string]any{"tls_insecure_skip_verify": true}
			if enabled {
				cfg["apexload_run_correlation_id"] = "run-123"
				cfg["apexload_run_correlation_origins"] = []string{origin.URL}
			}
			runtime := initHTTP(t, cfg)
			defer runtime.Teardown(context.Background())
			result, err := runtime.Execute(context.Background(), &protocol.Action{Type: "GET", Method: http.MethodGet, Target: origin.URL})
			if err != nil || !result.Success {
				t.Fatalf("Execute: result=%+v err=%v", result, err)
			}
			want := ""
			if enabled {
				want = "run-123"
			}
			if first != want || redirected != "" {
				t.Fatalf("initial=%q redirected=%q, want %q and empty", first, redirected, want)
			}
		})
	}
}

func TestManagedRunHeaderRejectsConflictsBeforeRequest(t *testing.T) {
	calls := 0
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(http.StatusOK) }))
	defer origin.Close()
	runtime := initHTTP(t, map[string]any{"tls_insecure_skip_verify": true, "apexload_run_correlation_id": "run-123", "apexload_run_correlation_origins": []string{origin.URL}})
	defer runtime.Teardown(context.Background())
	_, err := runtime.Execute(context.Background(), &protocol.Action{Type: "GET", Method: http.MethodGet, Target: origin.URL, Headers: map[string]string{"x-apex-test-run": "another-run"}})
	if err == nil || !strings.Contains(err.Error(), "conflicts") || calls != 0 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestManagedRunCorrelationConfigValidation(t *testing.T) {
	cases := []struct {
		name, origin, id string
		wantError        bool
	}{
		{"canonical default port", "https://APP.example:443", "run", false},
		{"path denied", "https://app.example/secret", "run", true},
		{"userinfo denied", "https://user@APP.example", "run", true},
		{"query denied", "https://app.example?x=y", "run", true},
		{"http approved", "http://app.example", "run", false},
		{"non-HTTP denied", "ftp://app.example", "run", true},
		{"missing identity", "https://app.example", "", true},
		{"newline identity", "https://app.example", "run\r\nother", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			policy, err := newRunCorrelationPolicy(map[string]any{"apexload_run_correlation_id": tc.id, "apexload_run_correlation_origins": []string{tc.origin}})
			if (err != nil) != tc.wantError {
				t.Fatalf("policy=%+v err=%v", policy, err)
			}
			if err == nil {
				target := "https://app.example/path"
				if strings.HasPrefix(tc.origin, "http://") {
					target = "http://app.example/path"
				}
				req, _ := http.NewRequest(http.MethodGet, target, nil)
				if err := policy.apply(req); err != nil || req.Header.Get(ManagedRunHeader) != tc.id {
					t.Fatalf("header=%q err=%v", req.Header.Get(ManagedRunHeader), err)
				}
			}
		})
	}
}
