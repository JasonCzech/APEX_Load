package protocol

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func resourceWireFixture(t *testing.T) []byte {
	t.Helper()
	generation := "0123456789abcdef0123456789abcdef"
	zero, bytes, observed := 0.0, int64(0), false
	evidence := BrowserResourceEvidence{Version: 2, SourceGeneration: generation, RequestID: generation + "-request-1", RedirectHop: 0,
		ObservedAt: time.Unix(1700000000, 123456789).UTC(), TimingSource: "playwright", State: "finished",
		Phases: []BrowserResourcePhase{{Name: "response", StartMS: &zero, EndMS: &zero}}, Bytes: BrowserResourceBytes{ResponseEncodedBody: &bytes}, ServiceWorker: &observed}
	raw, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func TestDecodeBrowserResourceEvidencePreservesPresenceAndOwnership(t *testing.T) {
	raw := resourceWireFixture(t)
	evidence, err := DecodeBrowserResourceEvidence(raw)
	if err != nil || evidence.RedirectHop != 0 || *evidence.Phases[0].StartMS != 0 || *evidence.Bytes.ResponseEncodedBody != 0 || evidence.ServiceWorker == nil || *evidence.ServiceWorker {
		t.Fatalf("lost zero or false: %v", err)
	}
	again, err := DecodeBrowserResourceEvidence(raw)
	if err != nil {
		t.Fatal(err)
	}
	*evidence.Phases[0].StartMS = 9
	*evidence.Bytes.ResponseEncodedBody = 9
	*evidence.ServiceWorker = true
	if *again.Phases[0].StartMS != 0 || *again.Bytes.ResponseEncodedBody != 0 || *again.ServiceWorker {
		t.Fatal("decoded evidence aliases another copy")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		t.Fatal("fixture")
	}
	fields["phases"] = json.RawMessage("null")
	nilPhases, _ := json.Marshal(fields)
	if e, err := DecodeBrowserResourceEvidence(nilPhases); err != nil || e.Phases != nil {
		t.Fatalf("producer nil phases refused: %v", err)
	}
}
func TestDecodeBrowserResourceEvidenceRefusesOmittedOrForgedWire(t *testing.T) {
	raw := resourceWireFixture(t)
	for _, field := range []string{"version", "source_generation", "request_id", "redirect_hop", "observed_at", "timing_source", "phases", "bytes", "state"} {
		t.Run("missing_"+field, func(t *testing.T) {
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(raw, &fields)
			delete(fields, field)
			value, _ := json.Marshal(fields)
			if _, err := DecodeBrowserResourceEvidence(value); err == nil {
				t.Fatal("omitted required value admitted")
			}
		})
	}
	replacements := map[string]map[string]json.RawMessage{
		"null_scalar": {"redirect_hop": json.RawMessage("null")}, "null_bytes": {"bytes": json.RawMessage("null")}, "wrong_boolean": {"service_worker": json.RawMessage(`"false"`)},
		"future": {"version": json.RawMessage("4")}, "v2_with_association": {"document_association": json.RawMessage(`"cdp_loader_join"`), "document_id": json.RawMessage(`"0123456789abcdef0123456789abcdef-d1"`)}, "subnanosecond": {"observed_at": json.RawMessage(`"2023-11-14T22:13:20.1234567891Z"`)}, "offset": {"observed_at": json.RawMessage(`"2023-11-14T22:13:20+00:00"`)},
		"invalid_date": {"observed_at": json.RawMessage(`"2023-02-30T22:13:20Z"`)}, "wrong_generation": {"request_id": json.RawMessage(`"fedcba9876543210fedcba9876543210-request-1"`)},
		"unsafe_origin": {"origin": json.RawMessage(`"https://secret@example.test"`)}, "unknown": {"server_time_ms": json.RawMessage("0")},
		"duplicate_phase":      {"phases": json.RawMessage(`[{"name":"response","start_ms":0,"end_ms":0},{"name":"response","start_ms":0,"end_ms":0}]`)},
		"incomplete_no_reason": {"phases": json.RawMessage(`[{"name":"response","start_ms":0}]`)}, "negative_bytes": {"bytes": json.RawMessage(`{"response_encoded_body":-1}`)},
	}
	for name, patch := range replacements {
		t.Run(name, func(t *testing.T) {
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(raw, &fields)
			for k, v := range patch {
				fields[k] = v
			}
			value, _ := json.Marshal(fields)
			if _, err := DecodeBrowserResourceEvidence(value); err == nil {
				t.Fatal("forged evidence admitted")
			}
		})
	}
	for name, value := range map[string][]byte{"duplicate": []byte(strings.Replace(string(raw), `"version":2`, `"version":2,"version":2`, 1)), "trailing": append(append([]byte{}, raw...), []byte(" {}")...), "oversize": []byte(strings.Repeat(" ", MaxBrowserResourceEvidenceBytes+1)), "invalid_utf8": append(append([]byte{}, raw...), 0xff)} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeBrowserResourceEvidence(value); err == nil {
				t.Fatal("invalid wire admitted")
			}
		})
	}
}
