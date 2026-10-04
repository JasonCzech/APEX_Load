package sample

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
)

func responseContentHAREntry(content any) map[string]any {
	return map[string]any{"request": map[string]any{"method": "GET", "url": "https://fixture.invalid/read", "httpVersion": "HTTP/1.1"}, "response": map[string]any{"status": 200, "httpVersion": "HTTP/1.1", "headers": []any{map[string]any{"name": "Content-Encoding", "value": "gzip"}, map[string]any{"name": "Content-Encoding", "value": "br"}}, "content": content}}
}

func TestHARResponseContentAdmissionAndNoDoubleDecode(t *testing.T) {
	for _, tc := range []struct {
		name            string
		content         any
		envelope        any
		envelopePresent bool
		body, reason    string
	}{
		{"decoded plaintext", map[string]any{"text": `{"token":"decoded-source"}`}, nil, false, `{"token":"decoded-source"}`, ""},
		{"decoded base64", map[string]any{"text": base64.StdEncoding.EncodeToString([]byte(`{"token":"decoded-source"}`)), "encoding": "base64"}, nil, false, `{"token":"decoded-source"}`, ""},
		{"binary resource", map[string]any{"text": base64.StdEncoding.EncodeToString([]byte{0x1f, 0x8b, 0, 0xff}), "encoding": "base64"}, nil, false, "", ""},
		{"missing optional text", map[string]any{}, nil, false, "", "response_content_unavailable"},
		{"malformed text", map[string]any{"text": 42}, nil, false, "", "invalid_har_content_storage"},
		{"malformed content", []any{}, nil, false, "", "invalid_har_content_storage"},
		{"malformed base64", map[string]any{"text": "source-private-canary!", "encoding": "base64"}, nil, false, "", "invalid_har_content_storage"},
		{"unsupported storage", map[string]any{"text": "source-private-canary", "encoding": "source-encoding-canary"}, nil, false, "", "unsupported_har_content_storage"},
		{"explicit failure omits text", map[string]any{"text": "source-private-canary"}, map[string]any{"version": 1, "captured": "wire", "stored": "unavailable", "state": "unavailable", "reason_code": "response_content_truncated"}, true, "", "response_content_truncated"},
		{"malformed envelope cannot fall back", map[string]any{"text": "source-private-canary"}, nil, true, "", "malformed_response_content_provenance"},
		{"size mismatch", map[string]any{"text": "source-private-canary"}, map[string]any{"version": 1, "captured": "wire", "stored": "decoded", "state": "complete", "decoded_size_bytes": 2}, true, "", "malformed_response_content_provenance"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entry := responseContentHAREntry(tc.content)
			if tc.envelopePresent {
				entry[requestimport.HARResponseContentProvenanceKey] = tc.envelope
			}
			var diagnostics diag.Collector
			ex, err := parseHARItem(entry, &diagnostics, 0)
			if err != nil {
				t.Fatal(err)
			}
			if ex.Response.Body != tc.body || ex.Response.ContentCapture.ReasonCode != tc.reason {
				t.Fatalf("body/capture=%q/%+v", ex.Response.Body, ex.Response.ContentCapture)
			}
			if len(ex.Response.HeaderList) != 2 {
				t.Fatal("captured response encoding occurrences were discarded")
			}
			data, err := json.Marshal(ex.Metadata)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "source-private-canary") || strings.Contains(string(data), "source-encoding-canary") {
				t.Fatal("invalid source content entered active metadata")
			}
			if tc.reason != "" && tc.reason != "response_content_unavailable" && ex.Metadata["execution_blocker"] != "import_review_required" {
				t.Fatal("unavailable content was not durably gated")
			}
		})
	}
}

func TestHARBinaryGzipFileRetainsExactResourceSizeWithoutTextBaseline(t *testing.T) {
	resource := []byte{0x1f, 0x8b, 0x08, 0, 0xff, 0x7f}
	entry := responseContentHAREntry(map[string]any{"text": base64.StdEncoding.EncodeToString(resource), "encoding": "base64", "mimeType": "application/gzip"})
	data, err := json.Marshal(map[string]any{"log": map[string]any{"entries": []any{entry}}})
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := HARCorrelationBaseline(string(data), true)
	if err != nil {
		t.Fatal(err)
	}
	if baseline[0].ResponseBody != "" {
		t.Fatal("binary resource was supplied to textual correlation")
	}
	var diagnostics diag.Collector
	ex, err := parseHARItem(entry, &diagnostics, 0)
	if err != nil {
		t.Fatal(err)
	}
	if ex.Response.ContentCapture.DecodedSizeBytes != len(resource) || ex.Response.ContentCapture.BodyKind != "binary" {
		t.Fatal("resource was reinterpreted as a compressed exchange")
	}
	stored, err := base64.StdEncoding.DecodeString(entry["response"].(map[string]any)["content"].(map[string]any)["text"].(string))
	if err != nil || string(stored) != string(resource) {
		t.Fatal("source storage changed")
	}
}

func TestHARStorageLimitAndLegacyEncodingPresence(t *testing.T) {
	if body, reason := decodeHARContentStorage("Zh==", "base64"); body != nil || reason != "invalid_har_content_storage" {
		t.Fatal("nonzero trailing response storage pad bits were admitted")
	}
	var diagnostics diag.Collector
	if body, encoding := decodeHARRequestBody("Zh==", "base64", &diagnostics, "request"); body != "f" || encoding != "" {
		t.Fatal("response storage strictness changed existing request storage behavior")
	}
	if body, reason := decodeHARContentStorage(strings.Repeat("x", maxHARDecodedContentBytes+1), ""); body != nil || reason != "har_content_storage_limit" {
		t.Fatal("plaintext storage limit escaped")
	}
	entry := responseContentHAREntry(map[string]any{"text": strings.Repeat("x", (16<<20)+1)})
	ex, err := parseHARItem(entry, &diagnostics, 0)
	if err != nil {
		t.Fatal(err)
	}
	if ex.Response.Body != "" || ex.Response.ContentCapture.ReasonCode != "har_content_storage_limit" || ex.Metadata["execution_blocker"] != "import_review_required" {
		t.Fatal("oversized HAR logical storage was not omitted and gated")
	}
	for _, encoding := range []string{"", " ", "gzip"} {
		entry := responseContentHAREntry(map[string]any{"text": "legacy-logical"})
		entry["response"].(map[string]any)["headers"] = []any{map[string]any{"name": "Content-Encoding", "value": encoding}, map[string]any{"name": "Content-Encoding", "value": encoding}}
		var diagnostics diag.Collector
		ex, err := parseHARItemWithProvenance(entry, &diagnostics, 0, true)
		if err != nil {
			t.Fatal(err)
		}
		if ex.Response.Body != "" || ex.Response.ContentCapture.ReasonCode != "response_content_provenance_unavailable" {
			t.Fatal("legacy encoding presence falsely established a decoded body")
		}
	}
}

func TestHARBodylessWireProvenanceRejectsBodyButNormalCachedContentSurvives(t *testing.T) {
	for _, tc := range []struct {
		method string
		status int
	}{{"HEAD", 200}, {"GET", 204}, {"GET", 304}} {
		entry := responseContentHAREntry(map[string]any{"text": "cached"})
		entry["request"].(map[string]any)["method"] = tc.method
		entry["response"].(map[string]any)["status"] = tc.status
		entry[requestimport.HARResponseContentProvenanceKey] = map[string]any{"version": 1, "captured": "wire", "stored": "decoded", "state": "complete", "decoded_size_bytes": 6}
		var diagnostics diag.Collector
		ex, err := parseHARItem(entry, &diagnostics, 0)
		if err != nil {
			t.Fatal(err)
		}
		if ex.Response.Body != "" || ex.Response.ContentCapture.ReasonCode != "malformed_response_content_provenance" {
			t.Fatal("bodyless wire exchange accepted a resource body")
		}
		delete(entry, requestimport.HARResponseContentProvenanceKey)
		ex, err = parseHARItem(entry, &diagnostics, 0)
		if err != nil {
			t.Fatal(err)
		}
		if ex.Response.Body != "cached" || !ex.Response.ContentCapture.Complete() {
			t.Fatal("normal HAR cached logical content was discarded")
		}
	}
}

func TestHARExplicitContentEnvelopeCannotDisappearWithMissingResponseObject(t *testing.T) {
	for _, envelope := range []any{nil, map[string]any{"version": 1, "captured": "wire", "stored": "decoded", "state": "complete"}, map[string]any{"version": 1, "captured": "wire", "stored": "unavailable", "state": "unavailable", "reason_code": "response_content_truncated"}} {
		entry := responseContentHAREntry(nil)
		delete(entry, "response")
		entry[requestimport.HARResponseContentProvenanceKey] = envelope
		var diagnostics diag.Collector
		ex, err := parseHARItem(entry, &diagnostics, 0)
		if err != nil {
			t.Fatal(err)
		}
		if ex.Response != nil || ex.Metadata["execution_blocker"] != "import_review_required" || ex.Metadata[requestimport.ImportResponseContentFidelityMetadataKey] == nil {
			t.Fatal("explicit unavailable envelope lost its source gate with response object")
		}
	}
}
