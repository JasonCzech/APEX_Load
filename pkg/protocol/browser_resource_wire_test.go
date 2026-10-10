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
		"future": {"version": json.RawMessage("5")}, "v2_with_protocol": {"protocol": json.RawMessage(`"h3"`)}, "v3_with_protocol": {"version": json.RawMessage("3"), "protocol": json.RawMessage(`"h3"`)}, "v4_protocol_and_reason": {"version": json.RawMessage("4"), "protocol": json.RawMessage(`"h3"`), "protocol_unavailable_reason": json.RawMessage(`"cached"`)}, "v4_unknown_protocol": {"version": json.RawMessage("4"), "protocol": json.RawMessage(`"h3-29"`)}, "v4_unknown_reason": {"version": json.RawMessage("4"), "protocol_unavailable_reason": json.RawMessage(`"guessed"`)}, "v2_with_association": {"document_association": json.RawMessage(`"cdp_loader_join"`), "document_id": json.RawMessage(`"0123456789abcdef0123456789abcdef-d1"`)}, "v2_with_association_reason": {"identity_unavailable_reason": json.RawMessage(`"ambiguous_cdp_join"`)}, "subnanosecond": {"observed_at": json.RawMessage(`"2023-11-14T22:13:20.1234567891Z"`)}, "offset": {"observed_at": json.RawMessage(`"2023-11-14T22:13:20+00:00"`)},
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

func TestDecodeBrowserResourceEvidenceVersion4Protocol(t *testing.T) {
	raw := resourceWireFixture(t)
	for _, tc := range []struct{ protocol, reason string }{{"h1", ""}, {"h2", ""}, {"h3", ""}, {"", "cached"}, {"", "service_worker"}, {"", "draft_protocol"}, {"", "not_exposed"}, {"", "no_observation"}, {"", ""}} {
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(raw, &fields)
		fields["version"] = json.RawMessage("4")
		if tc.protocol != "" {
			fields["protocol"], _ = json.Marshal(tc.protocol)
		}
		if tc.reason != "" {
			fields["protocol_unavailable_reason"], _ = json.Marshal(tc.reason)
		}
		value, _ := json.Marshal(fields)
		decoded, err := DecodeBrowserResourceEvidence(value)
		if err != nil || decoded.Protocol != tc.protocol || decoded.ProtocolUnavailableReason != tc.reason {
			t.Fatalf("protocol=%q reason=%q: decoded=%+v err=%v", tc.protocol, tc.reason, decoded, err)
		}
	}
}

func TestNormalizeBrowserResourceProtocol(t *testing.T) {
	cases := map[string][2]string{
		"http/1.1": {"h1", ""}, "HTTP/1.0": {"h1", ""}, "h2": {"h2", ""}, "HTTP/2.0": {"h2", ""}, "h3": {"h3", ""}, "HTTP/3": {"h3", ""},
		"h3-29": {"", "draft_protocol"}, "hq-29": {"", "draft_protocol"}, "quic/1+spdy/3": {"", "draft_protocol"},
		"": {"", "not_exposed"}, "data": {"", "not_network"}, "blob": {"", "not_network"}, "spdy/3.1": {"", "unsupported"},
	}
	for raw, want := range cases {
		protocol, reason := NormalizeBrowserResourceProtocol(raw)
		if protocol != want[0] || reason != want[1] {
			t.Errorf("NormalizeBrowserResourceProtocol(%q) = %q, %q; want %q, %q", raw, protocol, reason, want[0], want[1])
		}
	}
}
