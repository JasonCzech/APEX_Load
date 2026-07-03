package engine

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type captureResultSink struct {
	result *protocol.Result
}

func (s *captureResultSink) RecordResult(result *protocol.Result) error {
	s.result = result
	return nil
}

func TestScrubSensitiveText(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "redacts structured fields",
			in:   `{"access_token":"abc123","status":"ok","ssn":"123-45-6789"}`,
			want: `{"access_token":"[REDACTED]","status":"ok","ssn":"[REDACTED]"}`,
		},
		{
			name: "redacts xml and query payloads",
			in:   `<token>abc123</token> https://example.test?session_id=secret-123`,
			want: `<token>[REDACTED]</token> https://example.test?session_id=[REDACTED]`,
		},
		{
			name: "redacts high entropy tokens",
			in:   `eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.signature`,
			want: `[REDACTED]`,
		},
		{
			name: "does not redact ordinary hostnames",
			in:   `https://archive.example.test/customers/customer-1234`,
			want: `https://archive.example.test/customers/customer-1234`,
		},
		{
			name: "preserves benign payloads",
			in:   `{"status":"ok","id":"cart-1"}`,
			want: `{"status":"ok","id":"cart-1"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := scrubSensitiveText(tt.in); got != tt.want {
				t.Fatalf("scrubSensitiveText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewRedactionConfig_ScrubsCustomRules(t *testing.T) {
	config, err := NewRedactionConfig([]RedactionRule{{
		Name:    "customer id",
		Pattern: `customer-[0-9]+`,
	}})
	if err != nil {
		t.Fatalf("NewRedactionConfig returned error: %v", err)
	}
	if config == nil {
		t.Fatal("expected config")
	}
	if got := config.ScrubText(`{"customer":"customer-1234","status":"ok"}`); got != `{"customer":"[REDACTED]","status":"ok"}` {
		t.Fatalf("ScrubText() = %q, want custom id redacted", got)
	}
}

func TestNewRedactionConfig_RejectsInvalidPattern(t *testing.T) {
	if _, err := NewRedactionConfig([]RedactionRule{{Pattern: `[`}}); err == nil {
		t.Fatal("expected invalid pattern to fail")
	}
}

func TestVUser_RecordResultScrubsSensitivePayloads(t *testing.T) {
	store := NewSnapshotStore(10)
	sink := &captureResultSink{}
	vu := &VUser{
		Group:         "Checkout",
		Metrics:       NewMetricsCollector(4),
		ResultSink:    sink,
		SnapshotStore: store,
		Variables:     map[string]string{},
	}

	result := &protocol.Result{
		Success:      false,
		ErrorMessage: "upstream rejected token=abc123&password=hunter2",
		ResponseBody: `{"access_token":"abc123","status":"ok","ssn":"123-45-6789"}`,
		PageURL:      "https://example.test/page?token=abc123",
		PageTitle:    "Account 123-45-6789",
		HTML:         `<html><body>token=AbCdEfGh1234567890ijklMN patient=123-45-6789</body></html>`,
		ConsoleEntries: []protocol.ConsoleEntry{{
			Text:   "client error access_token=console-secret",
			Source: "https://example.test/app.js?token=source-secret",
		}},
		DOMSummary: &protocol.DOMSummary{
			Title:       "Profile 123-45-6789",
			VisibleText: []string{"Authorization token=visible-secret", "ordinary text"},
		},
		NetworkEntries: []protocol.NetworkEntry{{
			URL:             "https://example.test/api?token=abc123",
			RequestHeaders:  map[string]string{"Cookie": "sid=abc123", "X-Correlation-ID": "corr-123"},
			RequestBody:     "password=hunter2",
			ResponseHeaders: map[string]string{"Set-Cookie": "sid=abc123"},
		}},
		Extractions: map[string]string{
			"session_token": "abc123",
		},
		Snapshot: &protocol.ErrorSnapshot{
			RequestURL:      `https://example.test/login?token=abc123&customer=42`,
			RequestHeaders:  map[string]string{"Authorization": "Bearer abc123", "X-Correlation-ID": "corr-123"},
			RequestBody:     `password=hunter2&customer=42`,
			ResponseHeaders: map[string]string{"Set-Cookie": "sessionid=abcdef1234567890", "Content-Type": "application/json"},
			ResponseBody:    `{"patient_id":"p-123","status":"ok"}`,
			HTML:            `<html><body>token=AbCdEfGh1234567890ijklMN patient=123-45-6789</body></html>`,
			PageURL:         "https://example.test/error?token=snapshot-secret",
			PageTitle:       "Failure 123-45-6789",
			ConsoleEntries: []protocol.ConsoleEntry{{
				Text:   "snapshot access_token=snapshot-console-secret",
				Source: "https://example.test/error.js?token=snapshot-source-secret",
			}},
			DOMSummary: &protocol.DOMSummary{
				Title:       "Snapshot 123-45-6789",
				VisibleText: []string{"token=snapshot-visible-secret"},
			},
			NetworkEntries: []protocol.NetworkEntry{{
				URL:             "https://example.test/snapshot?token=abc123",
				RequestHeaders:  map[string]string{"Authorization": "Bearer abc123"},
				ResponseHeaders: map[string]string{"Set-Cookie": "sid=abc123"},
			}},
		},
	}

	vu.recordResult(result, dsl.ErrorHandlingConfig{SnapshotOnError: true})

	if got := vu.Variables["session_token"]; got != "abc123" {
		t.Fatalf("session_token variable = %q, want raw extracted value", got)
	}
	if sink.result == nil || sink.result.Snapshot == nil {
		t.Fatal("expected scrubbed result to reach sink")
	}
	if strings.Contains(sink.result.ErrorMessage, "hunter2") || strings.Contains(sink.result.ErrorMessage, "abc123") {
		t.Fatalf("error message = %q, want secrets redacted", sink.result.ErrorMessage)
	}
	if strings.Contains(sink.result.PageURL, "abc123") || strings.Contains(sink.result.PageTitle, "123-45-6789") {
		t.Fatalf("page evidence leaked: url=%q title=%q", sink.result.PageURL, sink.result.PageTitle)
	}
	if strings.Contains(sink.result.ConsoleEntries[0].Text, "console-secret") || strings.Contains(sink.result.ConsoleEntries[0].Source, "source-secret") {
		t.Fatalf("console entries leaked: %#v", sink.result.ConsoleEntries)
	}
	if strings.Contains(sink.result.DOMSummary.Title, "123-45-6789") || strings.Contains(strings.Join(sink.result.DOMSummary.VisibleText, " "), "visible-secret") {
		t.Fatalf("DOM summary leaked: %#v", sink.result.DOMSummary)
	}
	if sink.result.Snapshot.RequestHeaders["Authorization"] != redactedPayloadValue {
		t.Fatalf("authorization header = %q, want %q", sink.result.Snapshot.RequestHeaders["Authorization"], redactedPayloadValue)
	}
	if sink.result.NetworkEntries[0].RequestHeaders["Cookie"] != redactedPayloadValue {
		t.Fatalf("network cookie header = %q, want %q", sink.result.NetworkEntries[0].RequestHeaders["Cookie"], redactedPayloadValue)
	}
	if sink.result.Snapshot.NetworkEntries[0].RequestHeaders["Authorization"] != redactedPayloadValue {
		t.Fatalf("snapshot network authorization header = %q, want %q", sink.result.Snapshot.NetworkEntries[0].RequestHeaders["Authorization"], redactedPayloadValue)
	}
	if sink.result.Snapshot.RequestHeaders["X-Correlation-ID"] != "corr-123" {
		t.Fatalf("correlation header = %q, want corr-123", sink.result.Snapshot.RequestHeaders["X-Correlation-ID"])
	}
	if !strings.Contains(sink.result.Snapshot.RequestURL, "token=[REDACTED]") {
		t.Fatalf("request url = %q, want token redacted", sink.result.Snapshot.RequestURL)
	}
	if !strings.Contains(sink.result.Snapshot.RequestBody, "password=[REDACTED]") {
		t.Fatalf("request body = %q, want password redacted", sink.result.Snapshot.RequestBody)
	}
	if sink.result.Snapshot.ResponseHeaders["Set-Cookie"] != redactedPayloadValue {
		t.Fatalf("set-cookie header = %q, want %q", sink.result.Snapshot.ResponseHeaders["Set-Cookie"], redactedPayloadValue)
	}
	if !strings.Contains(sink.result.Snapshot.ResponseBody, `"patient_id":"[REDACTED]"`) {
		t.Fatalf("response body = %q, want patient id redacted", sink.result.Snapshot.ResponseBody)
	}
	if !strings.Contains(sink.result.ResponseBody, `"access_token":"[REDACTED]"`) {
		t.Fatalf("result response body = %q, want access token redacted", sink.result.ResponseBody)
	}
	if strings.Contains(sink.result.Snapshot.HTML, "123-45-6789") {
		t.Fatalf("html = %q, want SSN redacted", sink.result.Snapshot.HTML)
	}
	if strings.Contains(sink.result.Snapshot.PageURL, "snapshot-secret") || strings.Contains(sink.result.Snapshot.PageTitle, "123-45-6789") {
		t.Fatalf("snapshot page evidence leaked: url=%q title=%q", sink.result.Snapshot.PageURL, sink.result.Snapshot.PageTitle)
	}
	if strings.Contains(sink.result.Snapshot.ConsoleEntries[0].Text, "snapshot-console-secret") ||
		strings.Contains(sink.result.Snapshot.ConsoleEntries[0].Source, "snapshot-source-secret") {
		t.Fatalf("snapshot console leaked: %#v", sink.result.Snapshot.ConsoleEntries)
	}
	if strings.Contains(sink.result.Snapshot.DOMSummary.Title, "123-45-6789") ||
		strings.Contains(strings.Join(sink.result.Snapshot.DOMSummary.VisibleText, " "), "snapshot-visible-secret") {
		t.Fatalf("snapshot DOM summary leaked: %#v", sink.result.Snapshot.DOMSummary)
	}

	snapshots := store.Snapshots()
	if len(snapshots) != 1 {
		t.Fatalf("snapshot count = %d, want 1", len(snapshots))
	}
	if snapshots[0].RequestHeaders["Authorization"] != redactedPayloadValue {
		t.Fatalf("stored snapshot authorization = %q, want %q", snapshots[0].RequestHeaders["Authorization"], redactedPayloadValue)
	}
}
