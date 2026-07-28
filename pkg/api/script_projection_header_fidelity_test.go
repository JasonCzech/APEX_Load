package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestImportHeaderFidelityBytesAreMaskedAndReconciled(t *testing.T) {
	const (
		headerName  = "Authorization"
		headerValue = "Bearer api-projection-canary-73de\r\nX-Injected: yes"
		reason      = "http_header_invalid_value"
	)
	raw := &dsl.Script{
		Name:     "Preserved invalid header",
		Version:  "1.0",
		Protocol: "http",
		Config: dsl.ScriptConfig{
			BaseURL: "https://example.test",
		},
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "fixture",
			Preserved: map[string]any{
				dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
			},
		}},
		Actions: []dsl.Action{{
			Name:   "request",
			Type:   "request",
			Method: "GET",
			Target: "/",
			Metadata: map[string]any{
				"execution_blocker": "import_review_required",
				"import_review": map[string]any{
					"version":      1,
					"reason_codes": []string{reason},
				},
				dsl.ImportHeaderFidelityMetadataKey: map[string]any{
					"version":      1,
					"reason_codes": []string{reason},
					"source_refs":  []string{"fixture.json#/headers/0"},
					"omitted_occurrences": []map[string]any{{
						"source_index": 0,
						"name":         "<header>",
						"reason_code":  reason,
						"source_ref":   "fixture.json#/headers/0",
						dsl.ImportHeaderFidelityPreservedNameBase64Key: base64.StdEncoding.EncodeToString(
							[]byte(headerName),
						),
						dsl.ImportHeaderFidelityPreservedValueBase64Key: base64.StdEncoding.EncodeToString(
							[]byte(headerValue),
						),
					}},
				},
			},
		}},
	}
	if issues := dsl.ValidateScriptIssues(raw); len(issues) != 0 {
		t.Fatalf("raw script validation issues = %v", issues)
	}

	projected := maskScriptSecrets(raw)
	if projected == nil {
		t.Fatal("maskScriptSecrets returned nil")
	}
	assertMaskedHeaderFidelityOccurrence(t, projected.Actions[0])
	if issues := dsl.ValidateScriptIssues(projected); len(issues) != 0 {
		t.Fatalf("masked blocked draft validation issues = %v", issues)
	}
	payload, err := json.Marshal(projected)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) == "" ||
		bytes.Contains(payload, []byte("api-projection-canary-73de")) {
		t.Fatalf("ordinary script projection disclosed inert header bytes: %s", payload)
	}

	response := credentialPreservingImportResponseValue(raw)
	responseScript := decodeProjectedScript(t, response)
	responseOccurrence := fidelityOccurrence(t, responseScript.Actions[0])
	rawOccurrence := fidelityOccurrence(t, raw.Actions[0])
	for _, key := range []string{
		dsl.ImportHeaderFidelityPreservedNameBase64Key,
		dsl.ImportHeaderFidelityPreservedValueBase64Key,
	} {
		if responseOccurrence[key] != rawOccurrence[key] {
			t.Fatalf("editable import response changed durable %s", key)
		}
	}
	reviewScript := decodeProjectedScript(t, sanitizeImportReviewEvidence(raw))
	assertMaskedHeaderFidelityOccurrence(t, reviewScript.Actions[0])

	fingerprint, err := fingerprintImportDraftCredentials(raw)
	if err != nil {
		t.Fatalf("fingerprintImportDraftCredentials: %v", err)
	}
	if !fingerprint.HasRawCredentials || fingerprint.FullDigest == "" {
		t.Fatalf("inert header bytes were not protected by import attestation: %#v", fingerprint)
	}
	persistent, err := preparePersistentImportedLibraryScript(raw, nil, "fixture")
	if err != nil {
		t.Fatalf("preparePersistentImportedLibraryScript: %v", err)
	}
	persistentOccurrence := fidelityOccurrence(t, persistent.Actions[0])
	for _, key := range []string{
		dsl.ImportHeaderFidelityPreservedNameBase64Key,
		dsl.ImportHeaderFidelityPreservedValueBase64Key,
	} {
		if persistentOccurrence[key] != rawOccurrence[key] {
			t.Fatalf("durable imported script changed exact %s", key)
		}
	}

	incoming := maskScriptSecrets(raw)
	if err := reconcileMaskedScriptSecrets(incoming, raw); err != nil {
		t.Fatalf("reconcileMaskedScriptSecrets: %v", err)
	}
	got := fidelityOccurrence(t, incoming.Actions[0])
	want := fidelityOccurrence(t, raw.Actions[0])
	for _, key := range []string{
		dsl.ImportHeaderFidelityPreservedNameBase64Key,
		dsl.ImportHeaderFidelityPreservedValueBase64Key,
	} {
		if got[key] != want[key] {
			t.Fatalf("reconciled %s = %#v, want %#v", key, got[key], want[key])
		}
	}
}

func TestPersistentReviewSanitizationKeepsInertHeaderEnvelope(t *testing.T) {
	const reason = "http_header_invalid_value"
	action := dsl.Action{
		Name: "Review imported HTTP runtime settings",
		Type: "review_required",
		Params: map[string]any{
			"reason": "review",
		},
		Metadata: map[string]any{
			"execution_blocker": "import_review_required",
			"import_review": map[string]any{
				"version":      1,
				"reason_codes": []string{reason},
			},
			dsl.ImportHeaderFidelityMetadataKey: map[string]any{
				"version":      1,
				"reason_codes": []string{reason},
				"source_refs":  []string{"fixture.json#/headers/0"},
				"omitted_occurrences": []map[string]any{{
					"source_index": 0,
					"name":         "<header>",
					"reason_code":  reason,
					"source_ref":   "fixture.json#/headers/0",
					dsl.ImportHeaderFidelityPreservedNameBase64Key: base64.StdEncoding.EncodeToString(
						[]byte("Authorization"),
					),
					dsl.ImportHeaderFidelityPreservedValueBase64Key: base64.StdEncoding.EncodeToString(
						[]byte("Bearer durable-placeholder-secret\r\nX-Injected: yes"),
					),
				}},
			},
		},
	}
	script := &dsl.Script{
		Name: "placeholder", Version: "1.0", Protocol: "http",
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{action},
	}
	before := fidelityOccurrence(t, script.Actions[0])
	if err := sanitizePersistentImportReviewSurfaces(script); err != nil {
		t.Fatalf("sanitizePersistentImportReviewSurfaces: %v", err)
	}
	after := fidelityOccurrence(t, script.Actions[0])
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("persistent sanitization changed inert header envelope: before %#v after %#v", before, after)
	}
}

func assertMaskedHeaderFidelityOccurrence(t *testing.T, action dsl.Action) {
	t.Helper()
	occurrence := fidelityOccurrence(t, action)
	for _, key := range []string{
		dsl.ImportHeaderFidelityPreservedNameBase64Key,
		dsl.ImportHeaderFidelityPreservedValueBase64Key,
	} {
		value, _ := occurrence[key].(string)
		if value != maskedImportHeaderBytes {
			t.Fatalf("%s = %q, want fixed masked payload %q", key, value, maskedImportHeaderBytes)
		}
		decoded, err := base64.StdEncoding.DecodeString(value)
		if err != nil || string(decoded) != maskedScriptSecret {
			t.Fatalf("%s did not contain the canonical masked payload", key)
		}
	}
}

func fidelityOccurrence(t *testing.T, action dsl.Action) map[string]any {
	t.Helper()
	marker, ok := action.Metadata[dsl.ImportHeaderFidelityMetadataKey].(map[string]any)
	if !ok {
		t.Fatalf("missing header fidelity marker: %#v", action.Metadata)
	}
	occurrences, ok := importHeaderFidelityOccurrencesJSON(marker)
	if !ok || len(occurrences) != 1 {
		t.Fatalf("header fidelity occurrences = %#v", marker["omitted_occurrences"])
	}
	return occurrences[0]
}

func decodeProjectedScript(t *testing.T, value any) *dsl.Script {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var script dsl.Script
	if err := json.Unmarshal(payload, &script); err != nil {
		t.Fatal(err)
	}
	return &script
}
