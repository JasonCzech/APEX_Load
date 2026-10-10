package sample

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestHARPerLeafHTTPVersionStrictCaptureAdmission(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		raw                     any
		present                 bool
		wantVersion, wantReason string
	}{
		{name: "H1", raw: "HTTP/1.1", present: true, wantVersion: "1.1"},
		{name: "H2", raw: "HTTP/2.0", present: true, wantVersion: "2"},
		{name: "missing", wantReason: "http_version_unavailable"},
		{name: "unknown", raw: "unknown", present: true, wantReason: "http_version_unavailable"},
		{name: "empty", raw: "", present: true, wantReason: "http_version_unavailable"},
		{name: "numeric", raw: 2, present: true, wantReason: "malformed_http_version"},
		{name: "boolean", raw: true, present: true, wantReason: "malformed_http_version"},
		{name: "null", present: true, wantReason: "http_version_unavailable"},
		{name: "object", raw: map[string]any{"token": "synthetic-version-canary"}, present: true, wantReason: "malformed_http_version"},
		{name: "H3", raw: "HTTP/3", present: true, wantVersion: "auto+h3"},
		{name: "H3 draft", raw: "h3-29", present: true, wantReason: "http3_draft_token"},
		{name: "HTTP/1.0", raw: "HTTP/1.0", present: true, wantReason: "unsupported_http_version"},
		{name: "unsupported arbitrary text", raw: "synthetic-version-canary", present: true, wantReason: "unsupported_http_version"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := map[string]any{"method": "GET", "url": "https://fixture.invalid/read"}
			if tc.present {
				request["httpVersion"] = tc.raw
			}
			data, _ := json.Marshal(map[string]any{"log": map[string]any{"entries": []any{map[string]any{"request": request, "response": map[string]any{"status": 200, "httpVersion": "HTTP/2"}}}}})
			result, err := Convert(ImportRequest{Format: "har", Content: string(data)})
			if err != nil {
				t.Fatal(err)
			}
			action := result.Script.Actions[0]
			if action.HTTPVersion.Value() != tc.wantVersion || action.HTTPVersion.IsPresent() != (tc.wantVersion != "") {
				t.Fatalf("leaf override=%v", action.HTTPVersion)
			}
			// Capture gaps keep the inherited policy and stay executable; only
			// malformed observations carry the durable review gate.
			gated := tc.wantReason != "" && !requestimport.HTTPVersionCaptureQualityOnly(tc.wantReason)
			if tc.wantReason != "" {
				marker, hasMarker := action.Metadata[requestimport.ImportHTTPVersionFidelityMetadataKey].(map[string]any)
				observation, _ := action.Metadata["http_version_capture"].(map[string]any)
				switch {
				case gated && marker["reason_code"] != tc.wantReason:
					t.Fatalf("marker=%v", marker)
				case !gated && (hasMarker || observation["reason_code"] != tc.wantReason):
					t.Fatalf("capture gap gated or unrecorded: marker=%v observation=%v", marker, observation)
				}
			}
			for pass := 0; pass < 2; pass++ {
				encoded, _ := json.Marshal(result.Script)
				if strings.Contains(string(encoded), "synthetic-version-canary") {
					t.Fatal("malformed source reached draft detail")
				}
				var reopened dsl.Script
				if err := json.Unmarshal(encoded, &reopened); err != nil {
					t.Fatal(err)
				}
				finalized, err := requestimport.FinalizeScript(&reopened, nil, requestimport.FinalizeOptions{})
				if err != nil {
					t.Fatal(err)
				}
				findings, err := importreview.FindingsFromScript(finalized.Script)
				if err != nil {
					t.Fatal(err)
				}
				blocking, review := 0, 0
				for _, finding := range findings {
					switch {
					case finding.Gate == importreview.GateExecution || finding.Severity == diag.Blocker:
						blocking++
					case finding.Gate == importreview.GatePromotion:
						review++
					}
				}
				// A draft HTTP/3 token waits for review before promotion but
				// does not block execution.
				if (blocking > 0) != gated || (review > 0) != (tc.wantReason == requestimport.ReasonHTTP3DraftToken) {
					t.Fatalf("reopen changed capture gate: blocking=%d review=%d", blocking, review)
				}
				result.Script = finalized.Script
			}
		})
	}
}

func TestHARRecorderVersionProvenanceAdmission(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		creator                 bool
		provenance              any
		present                 bool
		response                string
		wantVersion, wantReason string
	}{
		{name: "legacy synthesized version", creator: true, response: "HTTP/1.1", wantReason: "http_version_unavailable"},
		{name: "observed proxy", creator: true, provenance: requestimport.ProxyHTTPVersionProvenance(), present: true, response: "HTTP/1.1", wantVersion: "1.1"},
		{name: "different proxy legs", provenance: requestimport.ProxyHTTPVersionProvenance(), present: true, response: "HTTP/2", wantReason: "proxy_http_version_legs_differ"},
		{name: "unavailable upstream", provenance: requestimport.ProxyHTTPVersionProvenance(), present: true, response: "unknown", wantReason: "http_version_unavailable"},
		{name: "malformed envelope", provenance: map[string]any{"version": 1, "request_leg": "synthetic-provenance-canary"}, present: true, response: "HTTP/1.1", wantReason: "malformed_http_version_provenance"},
		{name: "null envelope", present: true, response: "HTTP/1.1", wantReason: "malformed_http_version_provenance"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entry := map[string]any{"request": map[string]any{"method": "GET", "url": "https://fixture.invalid/read", "httpVersion": "HTTP/1.1"}, "response": map[string]any{"status": 200, "httpVersion": tc.response}}
			if tc.present {
				entry[requestimport.HARHTTPVersionProvenanceKey] = tc.provenance
			}
			log := map[string]any{"entries": []any{entry}}
			if tc.creator {
				log["creator"] = map[string]any{"name": "APEX Load Recorder", "version": "1.0"}
			}
			data, _ := json.Marshal(map[string]any{"log": log})
			result, err := Convert(ImportRequest{Format: "har", Content: string(data)})
			if err != nil {
				t.Fatal(err)
			}
			action := result.Script.Actions[0]
			marker, hasMarker := action.Metadata[requestimport.ImportHTTPVersionFidelityMetadataKey].(map[string]any)
			observation, _ := action.Metadata["http_version_capture"].(map[string]any)
			gated := tc.wantReason != "" && !requestimport.HTTPVersionCaptureQualityOnly(tc.wantReason)
			if action.HTTPVersion.Value() != tc.wantVersion || gated && marker["reason_code"] != tc.wantReason {
				t.Fatalf("capture=%+v", action)
			}
			if tc.wantReason != "" && !gated && (hasMarker || observation["reason_code"] != tc.wantReason) {
				t.Fatalf("capture gap gated or unrecorded: marker=%v observation=%v", marker, observation)
			}
			if tc.name == "legacy synthesized version" || strings.Contains(tc.name, "envelope") {
				capture := action.Metadata["http_version_capture"].(map[string]any)
				if action.Metadata["har.request.http_version"] != nil || action.Metadata["har.response.http_version"] != nil || capture["request_state"] != "unavailable" || capture["request_version"] != nil {
					t.Fatal("untrusted synthesized capture was presented as an observation")
				}
			}
			encoded, _ := json.Marshal(result.Script)
			if strings.Contains(string(encoded), "synthetic-provenance-canary") {
				t.Fatal("untrusted provenance was copied to draft")
			}
		})
	}
}

func TestHARMixedVersionsRemainExactPerLeaf(t *testing.T) {
	data := `{"log":{"entries":[{"request":{"method":"GET","url":"https://fixture.invalid/h1","httpVersion":"HTTP/1.1","headers":[{"name":"Connection","value":"keep-alive"}]},"response":{"status":200}},{"request":{"method":"GET","url":"https://fixture.invalid/h2","httpVersion":"HTTP/2"},"response":{"status":200}}]}}`
	result, err := Convert(ImportRequest{Format: "har", Content: data})
	if err != nil {
		t.Fatal(err)
	}
	if result.Script.Config.HTTP.Version != "" {
		t.Fatal("mixed capture changed global mode")
	}
	for i, want := range []string{"1.1", "2"} {
		if result.Script.Actions[i].HTTPVersion.Value() != want || result.Script.Actions[i].Metadata["execution_blocker"] != nil {
			t.Fatal("supported mixed capture was not faithful")
		}
	}
	if len(result.Script.Actions[0].HeaderList) != 1 || result.Script.Actions[0].HeaderList[0].Name != "Connection" || result.Script.Actions[0].HeaderList[0].Value != "keep-alive" {
		t.Fatal("H1 header was lost")
	}
}
