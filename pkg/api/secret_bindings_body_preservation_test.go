package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
)

func largeStructuredCredentialBody(t *testing.T, password any) string {
	t.Helper()
	items := make([]string, 160)
	for index := range items {
		items[index] = fmt.Sprintf("ordinary_payload_candidate_%06d", index)
	}
	payload, err := json.Marshal(map[string]any{"password": password, "count": 1, "items": items})
	if err != nil {
		t.Fatal(err)
	}
	return string(payload)
}

func exactBodyImportFixture(body, contentType string) *dsl.Script {
	script := &dsl.Script{
		Name: "Structured source", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "request", Type: "request", Method: "POST", Target: "/", Body: body,
			Headers: map[string]string{"Content-Type": contentType},
		}},
	}
	ensureCredentialPreservingImportMetadata(script, "enterprise_script_archive")
	return script
}

func TestStructuredImportBodyPreservationUsesBoundedCredentialLeaves(t *testing.T) {
	jsonBody := largeStructuredCredentialBody(t, "fixture-password")
	formBody := "payload=" + url.QueryEscape(jsonBody) + "&password=fixture-form-password"
	for _, test := range []struct {
		name, body, contentType, removed string
	}{
		{name: "JSON body", body: jsonBody, contentType: "application/json", removed: jsonBody},
		{name: "form body", body: formBody, contentType: "application/x-www-form-urlencoded", removed: formBody},
		{name: "encoded JSON form component", body: formBody, contentType: "application/x-www-form-urlencoded", removed: url.QueryEscape(jsonBody)},
		{name: "base64 JSON body", body: base64.StdEncoding.EncodeToString([]byte(jsonBody)), contentType: "application/json", removed: base64.StdEncoding.EncodeToString([]byte(jsonBody))},
	} {
		t.Run(test.name, func(t *testing.T) {
			script := exactBodyImportFixture(test.body, test.contentType)
			if strings.HasPrefix(test.name, "base64") {
				script.Actions[0].BodyEncoding = "base64"
			}
			raw, err := scriptAsJSONValue(script)
			if err != nil {
				t.Fatal(err)
			}
			if status := credentialpolicy.ClassifySourceCredentialValue(test.removed); status != credentialpolicy.SourceCredentialValueUnsafeEncoding {
				t.Fatalf("aggregate status = %s, want bounded encoding scan refusal", status)
			}
			if !structuredImportBodyCanRemainExact(raw, "/actions/0/body", test.removed) {
				t.Fatal("complete structured credential leaves were not proven")
			}
			if test.removed == test.body && script.Actions[0].BodyEncoding == "" {
				projected := cloneScriptDefinition(script)
				projected.Actions[0].Body = maskedScriptSecret
				projectedRoot, err := scriptAsJSONValue(projected)
				if err != nil {
					t.Fatal(err)
				}
				reqs := annotateSecretBindingDestinationValues([]SecretBindingRequirement{{ID: "body", Path: "/actions/0/body"}}, raw, projectedRoot)
				if !reqs[0].DestinationValuePreserved || reqs[0].ValueGroupID != "" || reqs[0].SourceValueAvailable {
					t.Fatalf("whole-body review claimed migration availability or lost the exact destination: %#v", reqs[0])
				}
				if values := sourceSecretValuesForImportNodes(raw, projectedRoot, reqs); len(values) != 0 {
					t.Fatal("opaque document was offered as a recoverable credential scalar")
				}
			}
		})
	}
}

func TestStructuredImportBodyPreservationRetainsSafetyGuards(t *testing.T) {
	for _, test := range []struct {
		name, body, contentType string
		untrusted               bool
	}{
		{name: "literal placeholder", body: largeStructuredCredentialBody(t, "[REDACTED]"), contentType: "application/json"},
		{name: "encoded placeholder", body: largeStructuredCredentialBody(t, base64.StdEncoding.EncodeToString([]byte(url.QueryEscape("[REDACTED]")))), contentType: "application/json"},
		{name: "credential scalar candidate overflow", body: largeStructuredCredentialBody(t, strings.Repeat("ordinarycandidate012345 ", 160)), contentType: "application/json"},
		{name: "malformed JSON", body: `{"password":"fixture-password",`, contentType: "application/json"},
		{name: "opaque body", body: strings.Repeat("ordinarycandidate012345 ", 160), contentType: "text/plain"},
		{name: "untrusted marker", body: largeStructuredCredentialBody(t, "fixture-password"), contentType: "application/json", untrusted: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			script := exactBodyImportFixture(test.body, test.contentType)
			if test.untrusted {
				script.Metadata = nil
			}
			raw, err := scriptAsJSONValue(script)
			if err != nil {
				t.Fatal(err)
			}
			if structuredImportBodyCanRemainExact(raw, "/actions/0/body", test.body) {
				t.Fatal("incomplete or unavailable credential source bypassed its guard")
			}
		})
	}
}

func TestImportedEmptyCredentialFieldRemainsEmpty(t *testing.T) {
	script := exactBodyImportFixture("ordinary=value&password=", "application/x-www-form-urlencoded")
	projected := maskScriptSecrets(script)
	if projected == nil {
		t.Fatal("empty credential projection failed")
	}
	raw, _ := scriptAsJSONValue(script)
	masked, _ := scriptAsJSONValue(projected)
	reqs := annotateSecretBindingDestinationValues(discoverSecretBindingRequirements(projected, 0), raw, masked)
	if len(reqs) != 1 || !reqs[0].DestinationValuePreserved || reqs[0].SourceValueAvailable || reqs[0].ValueGroupID != "" {
		t.Fatalf("empty source field preservation = %#v", reqs)
	}
	resolved, err := replaceUnavailableImportCredentialValues(script)
	if err != nil || resolved == nil || resolved.Actions[0].Body != script.Actions[0].Body {
		t.Fatalf("source-empty body was changed during commit: error=%v", err)
	}
}

func TestWholeBodySecretBindingReconcilesOnlyBodyLocators(t *testing.T) {
	for _, mediaType := range []string{"application/json", "application/x-www-form-urlencoded"} {
		t.Run(mediaType, func(t *testing.T) {
			body := `{"password":"1","count":1,"note":"ordinary"}`
			field, pointer := "body.json", "/password"
			if mediaType == "application/x-www-form-urlencoded" {
				body, field, pointer = "payload="+url.QueryEscape(body)+"&password=fixture-password", "body.form", ""
			}
			script := exactBodyImportFixture(body, mediaType)
			script.Actions[0].HeaderList = dsl.HeaderList{{Name: "Content-Type", Value: mediaType}, {Name: "Authorization", Value: "Bearer fixture-header"}}
			script.Actions[0].Metadata = map[string]any{dsl.ImportPreservedCredentialTargetsMetadataKey: []credentialpolicy.TargetRef{
				{Field: "header_list", Index: 1}, {Field: field, Pointer: pointer, Index: 1},
			}}
			// A source-independent response gate models a disclosure body that
			// cannot be selectively projected. The selected occurrence is the
			// complete body, not a guessed JSON/form credential field.
			projected := maskScriptSecrets(script)
			if projected == nil {
				t.Fatal("source projection failed")
			}
			if mediaType != "application/json" {
				// Exercise the same trusted full-body contract without depending
				// on a particular free-text scrubber's form fallback.
				script.Actions[0].Body = "{{secret.WHOLE_BODY}}"
				if err := reconcileWholeBodySecretBindingLocators(script); err != nil {
					t.Fatal(err)
				}
			} else {
				var selection []secretBindingSelection
				for _, req := range discoverSecretBindingRequirements(projected, 0) {
					if req.Path == "/actions/0/body" {
						selection = append(selection, secretBindingSelection{RequirementID: req.ID, SecretName: "WHOLE_BODY"})
					}
				}
				if len(selection) != 1 {
					t.Fatalf("whole-body requirements = %d", len(selection))
				}
				resolved, remaining, err := applyCredentialPreservingSecretBindings(script, 0, selection)
				if err != nil {
					t.Fatalf("full-body binding failed: %v", err)
				}
				for _, req := range remaining {
					if req.Path == "/actions/0/body" {
						t.Fatal("governed full body was replaced by another requirement")
					}
				}
				script = resolved
			}
			if script.Actions[0].Body != "{{secret.WHOLE_BODY}}" || script.Actions[0].HeaderList[1].Value != "Bearer fixture-header" {
				t.Fatal("binding changed an unselected credential or the governed body reference")
			}
			payload, _ := json.Marshal(script.Actions[0].Metadata[dsl.ImportPreservedCredentialTargetsMetadataKey])
			var targets []credentialpolicy.TargetRef
			if json.Unmarshal(payload, &targets) != nil || len(targets) != 2 || targets[0].Field != "header_list" || targets[0].Index != 1 || targets[1].Field != "body.opaque" || targets[1].Pointer != "" {
				t.Fatalf("locator reconciliation = %s", payload)
			}
			if _, err := engine.ImportedCredentialValuesForDisclosure(script); err != nil {
				t.Fatalf("governed body rejected by disclosure locator resolver: %v", err)
			}
			if masked := maskScriptSecrets(script); masked == nil || masked.Actions[0].Body != script.Actions[0].Body {
				t.Fatal("governed body reference was lost in disclosure")
			}
			script.Actions[0].Metadata[dsl.ImportPreservedCredentialTargetsMetadataKey] = []credentialpolicy.TargetRef{{Field: "body.json", Pointer: "/password"}}
			script.Actions[0].Body = "{malformed"
			if err := reconcileWholeBodySecretBindingLocators(script); err != nil {
				t.Fatal(err)
			}
			if _, err := engine.ImportedCredentialValuesForDisclosure(script); err == nil && mediaType == "application/json" {
				t.Fatal("ordinary malformed body bypassed retained JSON locator checks")
			}
		})
	}
}

func TestUnavailableStructuredDocumentDoesNotBecomeWholeBodySecret(t *testing.T) {
	body := largeStructuredCredentialBody(t, "1")
	script := exactBodyImportFixture(body, "application/json")
	script.Actions[0].Metadata = map[string]any{dsl.ImportPreservedCredentialTargetsMetadataKey: []credentialpolicy.TargetRef{{Field: "body.json", Pointer: "/password"}}}
	resolved, err := replaceUnavailableImportCredentialValues(script)
	if err != nil || resolved == nil || resolved.Actions[0].Body != body {
		t.Fatalf("complete attested source body was replaced: %v", err)
	}
}

func TestWholeBodySecretBindingLocatorOwnership(t *testing.T) {
	ownTargets := []credentialpolicy.TargetRef{
		{RequestKey: "importer-key", Field: "actions[0].body.json", Pointer: "/password"},
		{RequestKey: "actions[0]", Field: "body.json", Pointer: "/password"},
		{RequestKey: "actions[1]", Field: "body.json", Pointer: "/other", Index: 4},
		{RequestKey: "importer-key", Field: "actions[1].body.json", Pointer: "/other", Index: 5},
		{Field: "header_list.body", Pointer: "/retained", Index: 3},
		{Field: "headers.body", Index: 2},
		{Field: "auth.body", Index: 1},
		{Field: "target.url.body", Index: 7},
	}
	child0 := []credentialpolicy.TargetRef{
		{Field: "actions[2].params.requests[0].body.json", Pointer: "/password"},
		{Field: "actions[2].params.requests[1].body.json", Pointer: "/other", Index: 9},
	}
	child1 := []credentialpolicy.TargetRef{{Field: "body.json", Pointer: "/password"}}
	script := &dsl.Script{Name: "Ownership", Protocol: "http", Actions: []dsl.Action{
		{Type: "request", Body: "{{secret.WHOLE}}", Metadata: map[string]any{dsl.ImportPreservedCredentialTargetsMetadataKey: ownTargets}},
		{Type: "request", Body: `{"password":"ordinary"}`, Metadata: map[string]any{dsl.ImportPreservedCredentialTargetsMetadataKey: ownTargets}},
		{Type: "http_sequence", Params: map[string]any{"requests": []any{
			map[string]any{"body": "{{secret.CHILD}}", "metadata": map[string]any{dsl.ImportPreservedCredentialTargetsMetadataKey: child0}},
			map[string]any{"body": `{"password":"ordinary"}`, "metadata": map[string]any{dsl.ImportPreservedCredentialTargetsMetadataKey: child1}},
		}}},
	}}
	if err := reconcileWholeBodySecretBindingLocators(script); err != nil {
		t.Fatal(err)
	}
	readTargets := func(raw any) []credentialpolicy.TargetRef {
		t.Helper()
		payload, err := json.Marshal(raw)
		if err != nil {
			t.Fatal(err)
		}
		var result []credentialpolicy.TargetRef
		if err := json.Unmarshal(payload, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	got := readTargets(script.Actions[0].Metadata[dsl.ImportPreservedCredentialTargetsMetadataKey])
	if len(got) != len(ownTargets) || got[0].Field != "actions[0].body.opaque" || got[1].Field != "body.opaque" {
		t.Fatalf("owned locator rebasing = %#v", got)
	}
	for index := 2; index < len(got); index++ {
		if got[index] != ownTargets[index] {
			t.Fatalf("unowned/header locator %d was changed", index)
		}
	}
	sibling := readTargets(script.Actions[1].Metadata[dsl.ImportPreservedCredentialTargetsMetadataKey])
	for index := range sibling {
		if sibling[index] != ownTargets[index] {
			t.Fatalf("sibling locator %d was changed", index)
		}
	}
	requests := script.Actions[2].Params["requests"].([]any)
	first := requests[0].(map[string]any)["metadata"].(map[string]any)
	second := requests[1].(map[string]any)["metadata"].(map[string]any)
	firstTargets := readTargets(first[dsl.ImportPreservedCredentialTargetsMetadataKey])
	secondTargets := readTargets(second[dsl.ImportPreservedCredentialTargetsMetadataKey])
	if len(firstTargets) != 2 || firstTargets[0].Field != "actions[2].params.requests[0].body.opaque" || firstTargets[1] != child0[1] || secondTargets[0] != child1[0] {
		t.Fatal("reconciliation crossed an HTTP sequence child surface")
	}
}

func TestStructuredImportPayloadProofBudgetsDoNotResetForNestedDocuments(t *testing.T) {
	body := largeStructuredCredentialBody(t, "fixture-password")
	for _, test := range []struct {
		name  string
		proof structuredImportPayloadProof
	}{
		{name: "byte limit", proof: structuredImportPayloadProof{remainingBytes: len(body) - 1, remainingValues: 1000}},
		{name: "value limit", proof: structuredImportPayloadProof{remainingBytes: len(body), remainingValues: 10}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if exactStructuredImportPayload([]byte(body), "application/json", 0, &test.proof) {
				t.Fatal("structured recovery exceeded its shared proof budget")
			}
		})
	}
	proof := &structuredImportPayloadProof{remainingBytes: len(body) - 1, remainingValues: 1000}
	if exactStructuredImportScalar(body, 0, proof) {
		t.Fatal("nested document reset the parent byte budget")
	}
}

func TestStructuredImportBodyPreservationRetainsDuplicateNeutralLeafGuards(t *testing.T) {
	encodedPlaceholder := base64.StdEncoding.EncodeToString([]byte("[REDACTED]"))
	body := strings.Replace(largeStructuredCredentialBody(t, "fixture-password"), "{", `{"note":"`+encodedPlaceholder+`","note":"ordinary",`, 1)
	if credentialpolicy.ClassifySourceCredentialValue(body) != credentialpolicy.SourceCredentialValueUnsafeEncoding {
		t.Fatal("fixture did not exercise aggregate encoded-candidate refusal")
	}
	script := exactBodyImportFixture(body, "application/json")
	raw, err := scriptAsJSONValue(script)
	if err != nil {
		t.Fatal(err)
	}
	if structuredImportBodyCanRemainExact(raw, "/actions/0/body", body) {
		t.Fatal("a later duplicate key hid an earlier unavailable neutral leaf")
	}
}
