package credentialpolicy

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestProjectRequestBodyIdentifiesXMLDeclaredAsJSON(t *testing.T) {
	t.Parallel()
	const xmlBody = `<?xml version="1.0"?><s:Envelope xmlns:s="urn:soap"><s:Body><password>mismatch-secret-canary</password></s:Body></s:Envelope>`
	for _, test := range []struct {
		name, contentType, body, wantCode string
		limits                            Limits
	}{
		{name: "SOAP declared JSON", contentType: "application/json", body: xmlBody, wantCode: "request_body_xml_json_content_type_mismatch"},
		{name: "JSON suffix and whitespace", contentType: "Application/Problem+JSON; charset=utf-8", body: " \r\n" + xmlBody, wantCode: "request_body_xml_json_content_type_mismatch"},
		{name: "plain XML", contentType: "application/json", body: "<root/>", wantCode: "request_body_xml_json_content_type_mismatch"},
		{name: "valid JSON", contentType: "application/json", body: `{"value":1}`},
		{name: "XML header", contentType: "text/xml; charset=utf-8", body: xmlBody},
		{name: "inferred XML", body: xmlBody},
		{name: "invalid JSON", contentType: "application/json", body: `{"value":`, wantCode: "credential_json_malformed"},
		{name: "less than is not XML", contentType: "application/json", body: "<not XML", wantCode: "credential_json_malformed"},
		{name: "incomplete XML", contentType: "application/json", body: "<root>", wantCode: "credential_json_malformed"},
		{name: "multiple roots", contentType: "application/json", body: "<a/><b/>", wantCode: "credential_json_malformed"},
		{name: "trailing text", contentType: "application/json", body: "<a/>other", wantCode: "credential_json_malformed"},
		{name: "comment only", contentType: "application/json", body: "<!-- not a document -->", wantCode: "credential_json_malformed"},
		{name: "empty", contentType: "application/json", wantCode: "credential_json_empty"},
		{name: "depth limit", contentType: "application/json", body: "<a><b/></a>", limits: Limits{MaxDepth: 1}, wantCode: "credential_json_malformed"},
		{name: "element limit", contentType: "application/json", body: "<a><b/><c/></a>", limits: Limits{MaxValues: 2}, wantCode: "credential_json_malformed"},
		{name: "attribute limit", contentType: "application/json", body: `<a x="1" y="2"/>`, limits: Limits{MaxValues: 2}, wantCode: "credential_json_malformed"},
		{name: "byte limit", contentType: "application/json", body: "<root/>", limits: Limits{MaxBytes: 2}, wantCode: "import_credential_review_incomplete"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := SourceRef{Format: "enterprise_script_archive", Pointer: "Action.c:7"}
			output, result, err := ProjectRequestBody([]byte(test.body), test.contentType, Options{
				Mode: Preserve, Source: source, TargetField: "actions[0].body", Limits: test.limits,
			})
			if err != nil || !bytes.Equal(output, []byte(test.body)) {
				t.Fatalf("body was changed or rejected: %v", err)
			}
			var blockers []Finding
			for _, finding := range result.Findings {
				if finding.Disposition == BlockedDraft {
					blockers = append(blockers, finding)
				}
			}
			if test.wantCode == "" {
				if len(blockers) != 0 || result.Disposition != ExecutableExact {
					t.Fatalf("unexpected blocker: %#v", result)
				}
			} else if len(blockers) != 1 || blockers[0].Code != test.wantCode || blockers[0].Source != source || result.Disposition != BlockedDraft {
				t.Fatalf("diagnostic = %#v, want %s at Action.c:7", result, test.wantCode)
			}
			if test.wantCode == "request_body_xml_json_content_type_mismatch" && len(result.PreservedTargets) != 1 {
				t.Fatalf("lost opaque credential disclosure locator: %#v", result.PreservedTargets)
			}
			encoded, err := json.Marshal(result)
			if err != nil || strings.Contains(string(encoded), "mismatch-secret-canary") {
				t.Fatal("diagnostic serialization failed or exposed source credentials")
			}
		})
	}
}

func TestXMLJSONMismatchDoesNotRelaxCredentialRedaction(t *testing.T) {
	t.Parallel()
	for _, mode := range []ProjectionMode{Redact, SecretReference} {
		t.Run(string(mode), func(t *testing.T) {
			output, result, err := ProjectRequestBody([]byte(`<root><password>mismatch-secret-canary</password></root>`), "application/json", Options{Mode: mode})
			if err != nil || result.Disposition != Rejected || output != nil {
				t.Fatalf("declared JSON rejection changed: %v / %#v", err, result)
			}
			if len(result.Findings) != 1 || result.Findings[0].Code != "credential_json_malformed_sensitive" {
				t.Fatalf("redaction failure changed: %#v", result.Findings)
			}
		})
	}
}
