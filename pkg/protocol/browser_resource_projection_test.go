package protocol

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestBrowserResourceArchiveProjectionPreservesPresenceAndExcludesPrivatePayloads(t *testing.T) {
	native := resourceWireFixture(t)
	source := []byte(`{"test_id":"run-1","timestamp":"2023-11-14T22:13:20.123456789Z","action_name":"load","group":"workers","region":"local","page_url":"https://example.test/page","request_body":"PRIVATE-ROOT-BODY","response_headers":{"Authorization":"PRIVATE-ROOT-HEADER"},"client_metrics":{"browser_engine":"chromium","device_profile":"Desktop","device_platform":"darwin","lcp":0,"debug":"PRIVATE-CLIENT-DEBUG"},"snapshot":{"page_url":"https://example.test/fallback","body":"PRIVATE-SNAPSHOT-BODY","network_entries":[{"url":"https://example.test/fallback","duration_ms":0,"headers":{"Authorization":"PRIVATE-FALLBACK-HEADER"}}]},"network_entries":[{"url":"https://example.test/data","status_code":0,"duration_ms":0,"bytes_received":0,"request_body":"PRIVATE-ENTRY-BODY","browser_evidence":` + string(native) + `}]}`)
	projected := BrowserResourceArchiveProjection(source)
	if bytes.Contains(projected, []byte("PRIVATE")) || bytes.Contains(projected, []byte(`"lcp"`)) {
		t.Fatal("private/non-resource data survived minimal projection")
	}
	transaction, reason, err := DecodeBrowserResourceArchiveProjection(projected)
	if err != nil || reason != "" {
		t.Fatalf("projection refused: %v %s", err, reason)
	}
	var row map[string]json.RawMessage
	if json.Unmarshal(transaction, &row) != nil {
		t.Fatal("transaction")
	}
	var entries []map[string]json.RawMessage
	if json.Unmarshal(row["network_entries"], &entries) != nil {
		t.Fatal("entries")
	}
	for _, name := range []string{"status_code", "duration_ms", "bytes_received"} {
		if !bytes.Equal(entries[0][name], []byte("0")) {
			t.Fatalf("observed zero lost for%s", name)
		}
	}
	evidence, err := DecodeBrowserResourceEvidence(entries[0]["browser_evidence"])
	if err != nil || evidence.ServiceWorker == nil || *evidence.ServiceWorker {
		t.Fatal("native measured false lost")
	}
	// Different serialized absence states remain distinct inside the minimal
	// projection; the reader decides availability using the actual source row.
	for _, input := range []string{`{}`, `{"network_entries":null}`, `{"network_entries":[]}`} {
		transaction, reason, err := DecodeBrowserResourceArchiveProjection(BrowserResourceArchiveProjection([]byte(input)))
		if err != nil || reason != "" || string(transaction) != input {
			t.Fatalf("wire presence changed:%s ->%s %s %v", input, transaction, reason, err)
		}
	}
	transaction[0] = 'x'
	again, _, err := DecodeBrowserResourceArchiveProjection(projected)
	if err != nil || len(again) == 0 || again[0] != '{' {
		t.Fatal("decoded projection aliases prior ownership")
	}
}
func TestBrowserResourceArchiveProjectionPreservesMalformedAndFutureStatesWithoutRawBodies(t *testing.T) {
	for _, version := range []string{"2", "3", "null", `"bad"`} {
		raw := []byte(`{"network_entries":[{"browser_evidence":{"version":` + version + `,"request_body":"PRIVATE-UNKNOWN-BODY"}}]}`)
		projected := BrowserResourceArchiveProjection(raw)
		if bytes.Contains(projected, []byte("PRIVATE")) {
			t.Fatal("malformed raw body retained")
		}
		transaction, reason, err := DecodeBrowserResourceArchiveProjection(projected)
		if err != nil || reason != "" {
			t.Fatalf("state projection refused: %v", err)
		}
		var row struct {
			NetworkEntries []struct {
				BrowserEvidence json.RawMessage `json:"browser_evidence"`
			} `json:"network_entries"`
		}
		if json.Unmarshal(transaction, &row) != nil || len(row.NetworkEntries) != 1 {
			t.Fatal("state row lost")
		}
		if _, err := DecodeBrowserResourceEvidence(row.NetworkEntries[0].BrowserEvidence); err == nil {
			t.Fatal("malformed/future evidence became healthy observed zeros")
		}
	}
}
func TestBrowserResourceArchiveProjectionLimitsAndClosedEnvelope(t *testing.T) {
	for name, source := range map[string][]byte{"malformed": []byte(`{"network_entries":`), "duplicate": []byte(`{"timestamp":"x","timestamp":"y"}`), "oversize": []byte(strings.Repeat(" ", MaxBrowserResourceProjectionBytes+1)), "entry_limit": []byte(`{"network_entries":[` + strings.TrimSuffix(strings.Repeat(`{},`, 4097), ",") + `]}`)} {
		t.Run(name, func(t *testing.T) {
			transaction, reason, err := DecodeBrowserResourceArchiveProjection(BrowserResourceArchiveProjection(source))
			expected := "resource_projection_invalid"
			if name == "oversize" || name == "entry_limit" {
				expected = "resource_projection_limit"
			}
			if err != nil || transaction != nil || reason != expected {
				t.Fatalf("explicit refusal lost: %v %s", err, reason)
			}
		})
	}
	valid := BrowserResourceArchiveProjection([]byte(`{"network_entries":[]}`))
	for name, raw := range map[string][]byte{"future": []byte(`{"version":2,"availability":"retained_projection","transaction":{}}`), "missing": []byte(`{"version":1,"transaction":{}}`), "null": []byte(`{"version":1,"availability":"retained_projection","transaction":null}`), "unknown": append(append([]byte(nil), valid[:len(valid)-1]...), []byte(`,"private":"hidden"}`)...), "trailing": append(append([]byte(nil), valid...), []byte(` {}`)...), "duplicate": []byte(`{"version":1,"version":1,"availability":"retained_projection","transaction":{}}`), "hidden_source_body": []byte(`{"version":1,"availability":"retained_projection","transaction":{"request_body":"PRIVATE"}}`), "marker_with_row": []byte(`{"version":1,"availability":"unavailable","reason":"resource_projection_limit","transaction":{}}`)} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := DecodeBrowserResourceArchiveProjection(raw); err == nil {
				t.Fatal("forged envelope accepted")
			}
		})
	}
}
