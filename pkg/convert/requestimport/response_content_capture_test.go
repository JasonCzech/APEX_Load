package requestimport

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestResponseContentTextEligibilityPreservesBinaryBytes(t *testing.T) {
	for _, tc := range []struct {
		body []byte
		text bool
	}{{nil, true}, {[]byte("text\t\n\r\f€"), true}, {[]byte{0x1f, 0x8b, 0x08, 0}, false}, {[]byte{'a', 0, 'b'}, false}, {[]byte{0xff}, false}, {[]byte{0x7f}, false}} {
		if IsResponseContentText(tc.body) != tc.text {
			t.Fatal("text eligibility changed binary byte classification")
		}
	}
}

func TestResponseContentProvenanceStrictAdmission(t *testing.T) {
	for _, tc := range []struct {
		name     string
		raw      any
		complete bool
		reason   string
	}{
		{"complete", map[string]any{"version": 1, "captured": "wire", "stored": "decoded", "state": "complete", "decoded_size_bytes": 3}, true, ""},
		{"failed", map[string]any{"version": 1, "captured": "wire", "stored": "unavailable", "state": "unavailable", "reason_code": "response_content_truncated"}, false, "response_content_truncated"},
		{"null", nil, false, "malformed_response_content_provenance"},
		{"unknown", map[string]any{"version": 1, "captured": "wire", "stored": "decoded", "state": "complete", "source_canary": "private-source-canary"}, false, "malformed_response_content_provenance"},
		{"optional absence cannot mask recorder failure", map[string]any{"version": 1, "captured": "wire", "stored": "unavailable", "state": "unavailable", "reason_code": "response_content_unavailable"}, false, "malformed_response_content_provenance"},
		{"fractional size", map[string]any{"version": 1, "captured": "wire", "stored": "decoded", "state": "complete", "decoded_size_bytes": 1.5}, false, "malformed_response_content_provenance"},
		{"reason canary", map[string]any{"version": 1, "captured": "wire", "stored": "unavailable", "state": "unavailable", "reason_code": "private-source-canary"}, false, "malformed_response_content_provenance"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			capture := ReviewResponseContentProvenance(tc.raw)
			if capture.Complete() != tc.complete || capture.ReasonCode != tc.reason {
				t.Fatalf("capture=%+v", capture)
			}
			action := dsl.Action{}
			ApplyResponseContentCapture(&action, capture, SourceRef{Format: "har", Pointer: "log.entries[0].response.content"})
			data, err := json.Marshal(action)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "private-source-canary") {
				t.Fatal("untrusted provenance entered active metadata")
			}
		})
	}
}

func TestResponseContentFailureReprojectsAfterRepeatedFinalize(t *testing.T) {
	script := &dsl.Script{Name: "response fidelity", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://fixture.invalid"}, Actions: []dsl.Action{{Type: "http_sequence", Params: map[string]any{"requests": []any{map[string]any{"method": "GET", "target": "/", "metadata": map[string]any{"response_content_capture": map[string]any{"version": 1, "captured": "wire", "stored": "unavailable", "state": "unavailable", "reason_code": "response_content_truncated"}}}}}}}}
	for pass := 0; pass < 3; pass++ {
		child := script.Actions[0].Params["requests"].([]any)[0].(map[string]any)
		metadata := child["metadata"].(map[string]any)
		for _, key := range []string{"execution_blocker", "execution_blockers", "import_review", ImportResponseContentFidelityMetadataKey} {
			delete(metadata, key)
		}
		if _, _, err := ApplyRuntimeFidelityReview(script, RuntimeFidelityOptions{SourceProfile: "har"}); err != nil {
			t.Fatal(err)
		}
		metadata = child["metadata"].(map[string]any)
		marker, _ := metadata[ImportResponseContentFidelityMetadataKey].(map[string]any)
		if marker["reason_code"] != "response_content_truncated" || metadata["execution_blocker"] != "import_review_required" {
			t.Fatalf("pass%d gate lost: %v", pass, metadata)
		}
		data, err := json.Marshal(script)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, script); err != nil {
			t.Fatal(err)
		}
	}
}

func TestResponseContentBaselineNormalizationPreservesCapturedHeaders(t *testing.T) {
	headers := map[string]string{"Content-Encoding": "gzip", "content-length": "45", "Transfer-Encoding": "chunked", "X-Trace": "one\ntwo"}
	normalized := ResponseContentBaselineHeaders(headers, CompleteResponseContent("har_decoded", 100))
	if len(normalized) != 1 || normalized["X-Trace"] != "one\ntwo" || len(headers) != 4 {
		t.Fatal("logical snapshot rewrote captured evidence")
	}
	if failed := ResponseContentBaselineHeaders(headers, UnavailableResponseContent("wire", "response_content_truncated")); len(failed) != 4 {
		t.Fatal("failed decoding falsely normalized captured headers")
	}
}
