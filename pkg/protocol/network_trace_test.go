package protocol

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestHTTPNetworkTraceConnectionReusedDistinguishesFreshFromUnknown(t *testing.T) {
	fresh, reused := false, true
	for _, tc := range []struct {
		name  string
		value *bool
	}{
		{name: "unknown", value: nil},
		{name: "fresh", value: &fresh},
		{name: "reused", value: &reused},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trace := &HTTPNetworkTrace{RemoteAddr: "203.0.113.1:443", ConnectionReused: tc.value}
			evidence := trace.EvidenceMap()
			got, present := evidence["connection_reused"]
			if tc.value == nil {
				if present {
					t.Fatalf("expected connection_reused omitted when unknown, got %v", got)
				}
			} else if got != *tc.value {
				t.Fatalf("expected connection_reused=%v in evidence, got %v (present=%v)", *tc.value, got, present)
			}

			encoded, err := json.Marshal(trace)
			if err != nil {
				t.Fatal(err)
			}
			if tc.value == nil && strings.Contains(string(encoded), "connection_reused") {
				t.Fatalf("expected connection_reused omitted from JSON when unknown, got %s", encoded)
			}
			if tc.value != nil && !strings.Contains(string(encoded), `"connection_reused":`+map[bool]string{true: "true", false: "false"}[*tc.value]) {
				t.Fatalf("expected connection_reused in JSON, got %s", encoded)
			}

			roundTrip := HTTPNetworkTraceFromEvidence(evidence)
			if (roundTrip.ConnectionReused == nil) != (tc.value == nil) || (tc.value != nil && *roundTrip.ConnectionReused != *tc.value) {
				t.Fatalf("expected round trip to keep connection_reused=%v, got %v", tc.value, roundTrip.ConnectionReused)
			}
		})
	}

	clone := (&HTTPNetworkTrace{ConnectionReused: &fresh}).Clone()
	*clone.ConnectionReused = true
	if fresh {
		t.Fatal("Clone shared the ConnectionReused pointer with the original")
	}
}

func TestHTTPNetworkTraceTransformStringsKeepsListsAsLists(t *testing.T) {
	trace := &HTTPNetworkTrace{
		RemoteAddr:              "10.0.0.1:443",
		TLSServerName:           "secret.internal",
		CookieNamesSent:         []string{"sid", "secret-cookie"},
		SetCookieNamesReceived:  []string{"csrf"},
		StrippedRedirectHeaders: []string{"Cookie"},
		DNSResolvedIPs:          []string{"10.0.0.1"},
		CookiesSentCount:        2,
	}
	got := trace.TransformStrings(func(value string) string {
		if strings.Contains(value, "secret") {
			return "[REDACTED]"
		}
		return value
	})
	if got.TLSServerName != "[REDACTED]" || !slices.Equal(got.CookieNamesSent, []string{"sid", "[REDACTED]"}) {
		t.Fatalf("expected strings transformed in place, got %+v", got)
	}
	if !slices.Equal(got.SetCookieNamesReceived, []string{"csrf"}) || got.CookiesSentCount != 2 {
		t.Fatalf("expected untouched fields kept, got %+v", got)
	}
	if trace.TLSServerName != "secret.internal" || trace.CookieNamesSent[1] != "secret-cookie" {
		t.Fatal("TransformStrings mutated the original trace")
	}
	evidence := got.EvidenceMap()
	if _, ok := evidence["cookie_names_sent"].([]string); !ok {
		t.Fatalf("expected cookie_names_sent to stay a list, got %T", evidence["cookie_names_sent"])
	}
}

func TestHTTPNetworkTraceFromEvidenceAcceptsJSONNumbers(t *testing.T) {
	var decoded map[string]any
	decoder := json.NewDecoder(strings.NewReader(`{"cookies_sent_count":2,"idle_time_ms":1.5,"cookie_names_sent":["a","b"]}`))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	got := HTTPNetworkTraceFromEvidence(decoded)
	if got == nil || got.CookiesSentCount != 2 || got.IdleTimeMs != 1.5 || !slices.Equal(got.CookieNamesSent, []string{"a", "b"}) {
		t.Fatalf("expected json.Number fields parsed, got %+v", got)
	}
}
