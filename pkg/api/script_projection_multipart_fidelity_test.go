package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestImportMultipartFidelityBytesAreMaskedAttestedAndReconciled(t *testing.T) {
	const (
		fieldName  = "api-projection-field-canary-742a"
		fieldValue = "api-projection-value-canary-a1cf\r\nX-Injected: yes"
		reason     = "multipart_field_not_wire_safe"
	)
	raw := multipartFidelityScript(t, fieldName, fieldValue, reason)

	projected := maskScriptSecrets(raw)
	if projected == nil {
		t.Fatal("maskScriptSecrets returned nil")
	}
	assertMaskedMultipartFidelityOccurrence(t, projected.Actions[0])
	payload, err := json.Marshal(projected)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(payload, []byte(fieldName)) ||
		bytes.Contains(payload, []byte(fieldValue)) ||
		bytes.Contains(payload, []byte(base64.StdEncoding.EncodeToString([]byte(fieldValue)))) {
		t.Fatalf("ordinary script projection disclosed inert multipart bytes: %s", payload)
	}

	response := credentialPreservingImportResponseValue(raw)
	responseScript := decodeProjectedScript(t, response)
	assertMultipartFidelityBytesEqual(
		t,
		responseScript.Actions[0],
		raw.Actions[0],
		"editable import response",
	)
	reviewScript := decodeProjectedScript(t, sanitizeImportReviewEvidence(raw))
	assertMaskedMultipartFidelityOccurrence(t, reviewScript.Actions[0])

	fingerprint, err := fingerprintImportDraftCredentials(raw)
	if err != nil {
		t.Fatalf("fingerprintImportDraftCredentials: %v", err)
	}
	if !fingerprint.HasRawCredentials || fingerprint.FullDigest == "" {
		t.Fatalf("multipart fidelity bytes were not protected by import attestation: %#v", fingerprint)
	}
	persistent, err := preparePersistentImportedLibraryScript(raw, nil, "fixture")
	if err != nil {
		t.Fatalf("preparePersistentImportedLibraryScript: %v", err)
	}
	assertMultipartFidelityBytesEqual(
		t,
		persistent.Actions[0],
		raw.Actions[0],
		"durable imported script",
	)

	incoming := maskScriptSecrets(raw)
	if err := reconcileMaskedScriptSecrets(incoming, raw); err != nil {
		t.Fatalf("reconcileMaskedScriptSecrets: %v", err)
	}
	assertMultipartFidelityBytesEqual(
		t,
		incoming.Actions[0],
		raw.Actions[0],
		"reconciled script",
	)

	changed := maskScriptSecrets(raw)
	multipartFidelityOccurrence(t, changed.Actions[0])["part_index"] = float64(2)
	err = reconcileMaskedScriptSecrets(changed, raw)
	if err == nil || !strings.Contains(err.Error(), "changed while containing a masked imported-multipart payload") {
		t.Fatalf("changed multipart identity reconciliation error = %v", err)
	}
}

func TestPersistentReviewSanitizationKeepsInertMultipartEnvelope(t *testing.T) {
	const reason = "multipart_field_not_wire_safe"
	script := multipartFidelityScript(
		t,
		"Authorization",
		"Bearer durable-multipart-placeholder-secret",
		reason,
	)
	script.Actions[0].Type = "review_required"
	script.Actions[0].Method = ""
	script.Actions[0].Target = ""
	script.Actions[0].Params = map[string]any{"reason": "review"}

	before := script.Actions[0]
	if err := sanitizePersistentImportReviewSurfaces(script); err != nil {
		t.Fatalf("sanitizePersistentImportReviewSurfaces: %v", err)
	}
	assertMultipartFidelityBytesEqual(
		t,
		script.Actions[0],
		before,
		"persistent sanitization",
	)
}

func multipartFidelityScript(
	t *testing.T,
	fieldName,
	fieldValue,
	reason string,
) *dsl.Script {
	t.Helper()
	return &dsl.Script{
		Name:     "Preserved invalid multipart field",
		Version:  "1.0",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.test"},
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "fixture",
			Preserved: map[string]any{
				dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
			},
		}},
		Actions: []dsl.Action{{
			Name:   "request",
			Type:   "request",
			Method: "POST",
			Target: "/upload",
			Metadata: map[string]any{
				"execution_blocker": "import_review_required",
				"import_review": map[string]any{
					"version":      1,
					"reason_codes": []string{reason},
				},
				dsl.ImportMultipartFidelityMetadataKey: map[string]any{
					"version":      1,
					"reason_codes": []string{reason},
					"source_refs":  []string{"fixture.json#/parts/0"},
					"omitted_fields": []map[string]any{{
						"part_index":   0,
						"field":        "header",
						"header_index": 1,
						"reason_code":  reason,
						"source_ref":   "fixture.json#/parts/0",
						dsl.ImportHeaderFidelityPreservedNameBase64Key: base64.StdEncoding.EncodeToString(
							[]byte(fieldName),
						),
						dsl.ImportHeaderFidelityPreservedValueBase64Key: base64.StdEncoding.EncodeToString(
							[]byte(fieldValue),
						),
					}},
				},
			},
		}},
	}
}

func assertMaskedMultipartFidelityOccurrence(t *testing.T, action dsl.Action) {
	t.Helper()
	occurrence := multipartFidelityOccurrence(t, action)
	for _, key := range []string{
		dsl.ImportHeaderFidelityPreservedNameBase64Key,
		dsl.ImportHeaderFidelityPreservedValueBase64Key,
	} {
		if value, _ := occurrence[key].(string); value != maskedImportHeaderBytes {
			t.Fatalf("%s = %q, want fixed masked payload %q", key, value, maskedImportHeaderBytes)
		}
	}
}

func assertMultipartFidelityBytesEqual(
	t *testing.T,
	gotAction,
	wantAction dsl.Action,
	context string,
) {
	t.Helper()
	got := multipartFidelityOccurrence(t, gotAction)
	want := multipartFidelityOccurrence(t, wantAction)
	for _, key := range []string{
		dsl.ImportHeaderFidelityPreservedNameBase64Key,
		dsl.ImportHeaderFidelityPreservedValueBase64Key,
	} {
		if got[key] != want[key] {
			t.Fatalf("%s changed exact %s: got %#v want %#v", context, key, got[key], want[key])
		}
	}
}

func multipartFidelityOccurrence(t *testing.T, action dsl.Action) map[string]any {
	t.Helper()
	marker, ok := action.Metadata[dsl.ImportMultipartFidelityMetadataKey].(map[string]any)
	if !ok {
		t.Fatalf("missing multipart fidelity marker: %#v", action.Metadata)
	}
	occurrences, ok := importFidelityOccurrencesJSON(marker, "omitted_fields")
	if !ok || len(occurrences) != 1 {
		t.Fatalf("multipart fidelity occurrences = %#v", marker["omitted_fields"])
	}
	return occurrences[0]
}
