package protocol

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
)

func javaHTTPActionFixture() *JavaHTTPSummary {
	var s *JavaHTTPSummary
	zero := int64(0)
	MergeJavaHTTPDiagnostic(&s, &JavaHTTPDiagnostics{Version: 1, Mode: "embedded_opaque_bytes", InputState: "empty", RequestDecodedBytes: &zero, ResponseState: "unavailable", EnclosingMs: 1, ObjectFootprint: "unavailable_embedded_opaque_bytes"})
	return s
}
func TestJavaHTTPActionsBoundsAndOwnership(t *testing.T) {
	sample := javaHTTPActionFixture()
	var s *JavaHTTPActions
	AddJavaHTTPAction(&s, "request", 1, sample)
	AddJavaHTTPAction(&s, "request", 2, sample)
	if !s.Incomplete || s.Actions["request"].Population != 3 || s.Actions["request"].Diagnostics.Samples != 2 {
		t.Fatal(s)
	}
	clone := s.Retained()
	clone.Actions["request"].Diagnostics.Samples = 0
	if s.Actions["request"].Diagnostics.Samples != 2 || sample.Samples != 1 {
		t.Fatal("source alias")
	}
	for _, tc := range []struct {
		name   string
		input  *JavaHTTPActions
		reason string
	}{
		{"version", &JavaHTTPActions{Version: 99}, "unsupported"},
		{"labels", &JavaHTTPActions{Version: 1, Actions: map[string]JavaHTTPActionSummary{strings.Repeat("x", 1025): {Population: 1, Diagnostics: sample}}}, "malformed"},
		{"missing", &JavaHTTPActions{Version: 1, Actions: map[string]JavaHTTPActionSummary{"a": {Population: 1}}}, "malformed"},
		{"negative", &JavaHTTPActions{Version: 1, Actions: map[string]JavaHTTPActionSummary{"a": {Population: -1, Diagnostics: sample}}}, "malformed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.input.Retained()
			if !got.Incomplete || got.UnavailableReason != tc.reason || len(got.Actions) != 0 {
				t.Fatal(got)
			}
		})
	}
	var many *JavaHTTPActions
	for i := 0; i < MaxJavaHTTPActions; i++ {
		AddJavaHTTPAction(&many, fmt.Sprint(i), 1, sample)
	}
	b, err := json.Marshal(many)
	if err != nil || len(b) > MaxJavaHTTPActionsBytes {
		t.Fatal("advertised byte bound", len(b), err)
	}
	if many.Incomplete || len(many.Actions) != MaxJavaHTTPActions {
		t.Fatal("boundary", many)
	}
	AddJavaHTTPAction(&many, "one too many", 1, sample)
	AddJavaHTTPAction(&many, "later", 1, sample)
	if many.UnavailableReason != "action_limit" || len(many.Actions) != 0 {
		t.Fatal("retained arbitrary prefix", many)
	}
	if (*JavaHTTPActions)(nil).Retained() != nil {
		t.Fatal("old peer became evidence")
	}
}
func TestJavaHTTPActionsMerge(t *testing.T) {
	var a, b *JavaHTTPActions
	sample := javaHTTPActionFixture()
	AddJavaHTTPAction(&a, "request", 1, sample)
	AddJavaHTTPAction(&b, "request", 1, sample)
	MergeJavaHTTPActions(&a, b)
	if a.Incomplete || a.Actions["request"].Population != 2 || a.Actions["request"].Diagnostics.Samples != 2 {
		t.Fatal(a)
	}
	b.Actions["request"].Diagnostics.Samples = 100
	if a.Actions["request"].Diagnostics.Samples != 2 {
		t.Fatal("merge alias")
	}
	MergeJavaHTTPActions(&a, &JavaHTTPActions{Version: 1, UnavailableReason: "byte_limit"})
	if a.UnavailableReason != "byte_limit" || !a.Incomplete || len(a.Actions) != 0 {
		t.Fatal(a)
	}
}

func TestJavaHTTPActionsJSONPreservesOrRefusesEvidence(t *testing.T) {
	var actions *JavaHTTPActions
	AddJavaHTTPAction(&actions, "request", 1, javaHTTPActionFixture())
	raw := JavaHTTPActionsJSON(actions)
	got := ParseJavaHTTPActionsJSON(raw)
	if got == nil || got.Incomplete || got.Actions["request"].Diagnostics.Samples != 1 {
		t.Fatal("roundtrip", raw, got)
	}
	for _, raw := range []string{"null", "", "  "} {
		if ParseJavaHTTPActionsJSON(raw) != nil {
			t.Fatal("absence invented")
		}
	}
	if ParseJavaHTTPActionsJSON(strings.Repeat("x", MaxJavaHTTPActionsBytes+1)).UnavailableReason != "byte_limit" {
		t.Fatal("byte cap")
	}
	// Extra/short arrays must not be silently narrowed to the Go [144]int64.
	for _, length := range []int{143, 145} {
		var object map[string]any
		if err := json.Unmarshal([]byte(raw), &object); err != nil {
			t.Fatal(err)
		}
		entry := object["actions"].(map[string]any)["request"].(map[string]any)["diagnostics"].(map[string]any)
		entry["base64_decode_ms"].(map[string]any)["buckets"] = make([]int64, length)
		malformed, _ := json.Marshal(object)
		refused := ParseJavaHTTPActionsJSON(string(malformed))
		if refused == nil || !refused.Incomplete || refused.Actions["request"].Diagnostics.Samples != 0 {
			t.Fatal("array silently narrowed", length, refused)
		}
	}
	var object map[string]any
	if err := json.Unmarshal([]byte(raw), &object); err != nil {
		t.Fatal(err)
	}
	object["actions"].(map[string]any)["request"].(map[string]any)["diagnostics"].(map[string]any)["future_payload"] = "DO_NOT_RETAIN"
	unknown, _ := json.Marshal(object)
	refused := ParseJavaHTTPActionsJSON(string(unknown))
	if refused == nil || !refused.Incomplete || strings.Contains(JavaHTTPActionsJSON(refused), "DO_NOT_RETAIN") {
		t.Fatal("unknown summary admitted")
	}
}

func TestJavaHTTPActionsDirectJSONBoundsEscapedNamesAndDenseHistograms(t *testing.T) {
	var summary *JavaHTTPSummary
	for i := range 130 {
		value := .001 * math.Pow(1.2, float64(i))
		zero := int64(0)
		MergeJavaHTTPDiagnostic(&summary, &JavaHTTPDiagnostics{Version: 1, Mode: "embedded_opaque_bytes", InputState: "decoded", RequestEncodedBytes: 4, RequestDecodedBytes: &zero, ResponseState: "complete", ResponseRetainedBytes: &zero, ResponseBase64Bytes: &zero, Base64DecodeMs: &value, Base64EncodeMs: &value, EnclosingMs: 2 * value, ObjectFootprint: "unavailable_embedded_opaque_bytes"})
	}
	for range 20 {
		MergeJavaHTTPSummary(&summary, summary.Clone())
	}
	if !summary.Valid() {
		t.Fatal("dense summary fixture invalid")
	}
	var actions *JavaHTTPActions
	for i := range MaxJavaHTTPActions {
		AddJavaHTTPAction(&actions, fmt.Sprintf("%04d", i)+strings.Repeat("<", 1020), summary.Samples, summary)
	}
	type plain JavaHTTPActions
	unbounded, err := json.Marshal((*plain)(actions))
	if err != nil || len(unbounded) <= MaxJavaHTTPActionsBytes {
		t.Fatalf("fixture must exercise escaped JSON overflow: %d %v", len(unbounded), err)
	}
	for name, value := range map[string]any{"pointer": actions, "value": *actions, "nested": struct {
		Evidence *JavaHTTPActions `json:"evidence"`
	}{actions}} {
		b, err := json.Marshal(value)
		if err != nil || len(b) > MaxJavaHTTPActionsBytes || !strings.Contains(string(b), `"unavailable_reason":"byte_limit"`) || strings.Contains(string(b), `"actions"`) {
			t.Fatalf("%s retained oversized or partial evidence: %d %v", name, len(b), err)
		}
	}
	if len(actions.Actions) != MaxJavaHTTPActions || actions.Incomplete {
		t.Fatal("serialization mutated source")
	}
	if JavaHTTPActionsJSON(actions) != string(mustJavaHTTPActionsJSON(t, actions)) {
		t.Fatal("history/live refusal differs")
	}
}

func mustJavaHTTPActionsJSON(t *testing.T, actions *JavaHTTPActions) []byte {
	t.Helper()
	b, err := json.Marshal(actions)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
