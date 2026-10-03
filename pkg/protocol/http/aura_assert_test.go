package http

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/protocol/aura"
)

func TestAuraAssertionDefinitionAndInputBudget(t *testing.T) {
	valid := protocol.Assertion{Type: "aura_actions_success"}
	if err := ValidateAssertionDefinition(valid); err != nil {
		t.Fatal(err)
	}
	if !assertionNeedsResponseBody(valid) {
		t.Fatal("Aura assertion must require captured response bytes")
	}
	for _, invalid := range []protocol.Assertion{
		{Type: valid.Type, Value: "captured-id"}, {Type: valid.Type, Expected: "SUCCESS"},
		{Type: valid.Type, Min: 1}, {Type: valid.Type, Max: 1}, {Type: valid.Type, LegacyHTMLCheck: true},
	} {
		if err := ValidateAssertionDefinition(invalid); err == nil {
			t.Fatalf("invalid definition accepted: %+v", invalid)
		}
	}
	budget := &assertionInputWorkBudget{limit: 10, bodyBytes: 3, requestBytes: 4}
	if err := budget.charge(valid); err != nil {
		t.Fatal(err)
	}
	if budget.used != 7 {
		t.Fatalf("request and response charge=%d", budget.used)
	}
	if err := budget.charge(valid); !errors.Is(err, errHTTPAssertionResourceLimit) {
		t.Fatalf("cumulative request work ignored: %v", err)
	}
	for _, budget := range []*assertionInputWorkBudget{
		{limit: 10, bodyBytes: 1, requestBytes: 10}, {limit: 10, bodyBytes: -1, requestBytes: 1},
		{limit: 10, bodyBytes: 1, requestBytes: -1}, {limit: 10, bodyBytes: 11, requestBytes: 0},
	} {
		if err := budget.charge(valid); !errors.Is(err, errHTTPAssertionResourceLimit) {
			t.Fatalf("invalid budget allowed: %+v %v", budget, err)
		}
	}
}

func TestHTTPAuraAssertionMatchesActualOutgoingRequest(t *testing.T) {
	const request = `{"actions":[{"id":"runtime-2","descriptor":"private-descriptor"},{"id":"runtime-1","descriptor":"synthetic"}]}`
	const good = `{"actions":[{"id":"runtime-1","state":"SUCCESS"},{"id":"runtime-2","state":"SUCCESS"}]}`
	for _, tc := range []struct {
		name, response    string
		status            int
		success, complete bool
		encoding          string
		legacy            bool
	}{
		{"reordered success", good, 200, true, true, "", false},
		{"base64 outgoing body", good, 200, true, true, "base64", false},
		{"HTTP 200 ERROR", strings.Replace(good, `"SUCCESS"`, `"ERROR"`, 1), 200, false, true, "", false},
		{"HTTP 200 INCOMPLETE", strings.Replace(good, `"SUCCESS"`, `"INCOMPLETE"`, 1), 200, false, true, "", false},
		{"unknown source state", strings.Replace(good, `"SUCCESS"`, `"private-unknown-state"`, 1), 200, false, false, "", false},
		{"duplicate response", strings.Replace(good, `"runtime-1"`, `"runtime-2"`, 1), 200, false, false, "", false},
		{"missing response", `{"actions":[{"id":"runtime-1","state":"SUCCESS"}]}`, 200, false, false, "", false},
		{"captured IDs cannot pass", strings.ReplaceAll(good, "runtime-", "captured-"), 200, false, false, "", false},
		{"malformed response", `{"actions":[`, 200, false, false, "", false},
		{"status evidence remains", good, 503, false, true, "", false},
		{"legacy action cannot bypass Aura", strings.Replace(good, `"SUCCESS"`, `"ERROR"`, 1), 200, false, true, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				if string(body) != request {
					t.Errorf("wire body differs from runtime request")
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.response)
			}))
			defer server.Close()
			h := initHTTP(t, map[string]any{"base_url": server.URL, "legacy_html_checks": false})
			defer h.Teardown(context.Background())
			body := []byte(request)
			if tc.encoding == "base64" {
				body = []byte(base64.StdEncoding.EncodeToString(body))
			}
			result, err := h.Execute(context.Background(), &protocol.Action{Name: "Synthetic Aura", Type: "POST", Method: "POST", Target: "/aura", Body: body, BodyEncoding: tc.encoding,
				RedactedBody: []byte(`{"actions":[{"id":"captured-only","descriptor":"redacted"}]}`),
				Params:       map[string]any{"legacy_html_check": tc.legacy}, DiscardResponseBodyOnSuccess: true, Assertions: []protocol.Assertion{{Type: "aura_actions_success"}}})
			if err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 1 || result.Success != tc.success || result.StatusCode != tc.status {
				t.Fatalf("outcome calls=%d success=%v status=%d error=%s", calls.Load(), result.Success, result.StatusCode, result.ErrorMessage)
			}
			report, ok := result.ProtocolEvidence["aura"].(aura.Report)
			if !ok || report.Complete != tc.complete || report.Success != (tc.success || tc.status == 503) || !report.HasResponse || report.RequestActions != 2 {
				t.Fatalf("safe report=%+v", report)
			}
			if tc.success && result.ResponseBody != "" {
				t.Fatal("assertion must work when success body is discarded")
			}
			encoded, err := json.Marshal(result.ProtocolEvidence)
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{"runtime-", "captured-", "private-descriptor", "private-unknown-state"} {
				if strings.Contains(string(encoded)+result.ErrorMessage, secret) {
					t.Fatalf("diagnostic disclosed source %q", secret)
				}
			}
		})
	}
}

func TestHTTPAuraInvalidAssertionIsRejectedBeforeTraffic(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(200) }))
	defer server.Close()
	h := initHTTP(t, map[string]any{"base_url": server.URL, "legacy_html_checks": false})
	defer h.Teardown(context.Background())
	result, err := h.Execute(context.Background(), &protocol.Action{Type: "POST", Target: "/aura", Assertions: []protocol.Assertion{{Type: "aura_actions_success", Expected: "captured-secret"}}})
	if err != nil || result.Success || calls.Load() != 0 || strings.Contains(result.ErrorMessage, "captured-secret") {
		t.Fatalf("definition admission: calls=%d result=%+v err=%v", calls.Load(), result, err)
	}
}
