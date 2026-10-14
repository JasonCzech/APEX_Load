package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/aurapayload"
)

func syntheticAuraParameterizeScript(t *testing.T) *dsl.Script {
	t.Helper()
	inner := `{"records":[{"recordId":"synthetic-selected"},{"recordId":"synthetic-unselected"}],"accountId":"synthetic-account"}`
	message, err := json.Marshal(map[string]any{"actions": []any{map[string]any{"id": "synthetic-1;a", "descriptor": "synthetic://save", "params": map[string]any{"input": inner}}}})
	if err != nil {
		t.Fatal(err)
	}
	return &dsl.Script{Name: "synthetic Aura parameters", Version: "1.0", Protocol: "http", Actions: []dsl.Action{{Name: "synthetic batch", Type: "POST", Target: "https://synthetic.invalid/aura", Body: "unrelated=one%20two&message=" + url.QueryEscape(string(message)) + "&unrelated=three+four"}}}
}

func TestApplyAuraParameterizationRegeneratesTrustedExactRoute(t *testing.T) {
	server := NewServer(ServerConfig{})
	script := syntheticAuraParameterizeScript(t)
	trusted := buildTrustedImportParameterization(script)
	if len(trusted.Suggestions) != 3 {
		t.Fatalf("trusted candidates=%d, coverage=%+v", len(trusted.Suggestions), trusted.AuraPayload)
	}
	wire := sanitizeImportParameterization(trusted, script)
	var selected paramize.Suggestion
	for _, s := range wire.Suggestions {
		if strings.Contains(s.Locations[0].DecodedFieldPath, "records[0]") {
			selected = s
		}
	}
	if selected.ID == "" {
		t.Fatal("missing server exact candidate")
	}
	for _, s := range wire.Suggestions {
		if s.LiteralValue != "" || s.SampleValue != "" || s.Locations[0].SearchText != "" || s.Parameter != nil {
			t.Fatal("Aura expected value or internal parameter exposed")
		}
	}
	// Browser-controlled samples are ignored; the trusted decoded value is
	// regenerated, and only the selected business route changes.
	selected.LiteralValue = "injected-client-value"
	selected.SampleValue = "injected-client-value"
	resp, decoded := postParameterizeApply(t, server, importParameterizeApplyRequest{Script: script, Suggestions: []paramize.Suggestion{selected}, SelectedIDs: []string{selected.ID}})
	if resp.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
	if decoded.AppliedCount != 1 || len(decoded.Script.Parameters) != 1 || len(decoded.Script.DataSources) != 1 {
		t.Fatalf("wrong applied counts")
	}
	inspection, err := aurapayload.Inspect(decoded.Script.Actions[0].Body, decoded.Script.Actions[0].BodyEncoding)
	if err != nil {
		t.Fatal(err)
	}
	changed, unchanged := 0, 0
	for _, leaf := range inspection.Leaves {
		if leaf.Value == selected.Replacement {
			changed++
		}
		if leaf.Value == "synthetic-unselected" {
			unchanged++
		}
	}
	if changed != 1 || unchanged != 1 {
		t.Fatalf("changed=%d unchanged=%d", changed, unchanged)
	}
	if strings.Contains(resp.Body.String(), "injected-client-value") {
		t.Fatal("client mutation instruction accepted")
	}
	for _, diff := range decoded.Diffs {
		if diff.Before != "" || diff.After != "" {
			t.Fatal("raw payload diff exposed")
		}
	}
	if decoded.Script.Actions[0].Metadata[dsl.ImportURLFormTemplateValueEncodingMetadataKey] != dsl.ImportAuraJSONTemplateValueEncoding {
		t.Fatal("runtime escaping metadata missing")
	}
}

func TestApplyAuraParameterizationRejectsTamperedDescriptorsAndStaleShape(t *testing.T) {
	server := NewServer(ServerConfig{})
	for _, tc := range []struct {
		name   string
		change func(*dsl.Script, *paramize.Suggestion)
	}{
		{"route", func(_ *dsl.Script, s *paramize.Suggestion) {
			s.Locations[0].AuraRoute[len(s.Locations[0].AuraRoute)-1].Key = "captured"
		}},
		{"path", func(_ *dsl.Script, s *paramize.Suggestion) { s.Locations[0].ActionPath = "actions[1]" }},
		{"layers", func(_ *dsl.Script, s *paramize.Suggestion) { s.Locations[0].EncodingLayers = []string{"json"} }},
		{"shape", func(_ *dsl.Script, s *paramize.Suggestion) { s.Locations[0].PayloadShape = "untrusted" }},
		{"replacement", func(_ *dsl.Script, s *paramize.Suggestion) { s.Locations[0].ReplaceText = "injected" }},
		{"stale-structure", func(script *dsl.Script, _ *paramize.Suggestion) {
			script.Actions[0].Body = strings.Replace(script.Actions[0].Body, url.QueryEscape("accountId"), url.QueryEscape("externalId"), 1)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := syntheticAuraParameterizeScript(t)
			wire := sanitizeImportParameterization(buildTrustedImportParameterization(script), script)
			if len(wire.Suggestions) == 0 {
				t.Fatal("missing advice")
			}
			selected := wire.Suggestions[0]
			tc.change(script, &selected)
			before, _ := json.Marshal(script)
			resp, _ := postParameterizeApply(t, server, importParameterizeApplyRequest{Script: script, Suggestions: []paramize.Suggestion{selected}, SelectedIDs: []string{selected.ID}})
			if resp.Code != http.StatusConflict {
				t.Fatalf("tampered status=%d body=%s", resp.Code, resp.Body.String())
			}
			after, _ := json.Marshal(script)
			if string(before) != string(after) {
				t.Fatal("rejected request mutated submitted script")
			}
		})
	}
}

func TestTrustedAuraParameterizationProjectionKeepsSafeCoverageAndDeepCopiesRoutes(t *testing.T) {
	script := syntheticAuraParameterizeScript(t)
	input := buildImportParameterization(script, nil)
	protected := filterTrustedImportParameterization(input, script)
	ordinary := sanitizeImportParameterization(input, script)
	for _, projected := range []*paramize.Result{protected, ordinary} {
		if projected.AuraPayload == nil || projected.AuraPayload.CandidateFields != 3 || !projected.AuraPayload.Complete {
			t.Fatal("safe coverage lost")
		}
		if len(projected.Suggestions) != 3 {
			t.Fatalf("projected rows=%d", len(projected.Suggestions))
		}
		for _, s := range projected.Suggestions {
			if s.LiteralValue != "" || s.SampleValue != "" || s.Locations[0].SearchText != "" || s.Parameter != nil {
				t.Fatal("Aura import projection retained expected value")
			}
		}
		projected.Suggestions[0].Locations[0].AuraRoute[0].Key = "mutated"
		projected.Suggestions[0].Locations[0].EncodingLayers[0] = "mutated"
	}
	if input.Suggestions[0].Locations[0].AuraRoute[0].Key != "actions" || input.Suggestions[0].Locations[0].EncodingLayers[0] != "url_form" {
		t.Fatal("projection aliased trusted route slices")
	}
}

func TestMultiScriptAuraParameterizationMergesIncompleteCoverage(t *testing.T) {
	first := syntheticAuraParameterizeScript(t)
	second := syntheticAuraParameterizeScript(t)
	second.Name = "second"
	second.Actions[0].Body = "message=%7Bmalformed%7D"
	result := buildMultiScriptParameterization([]*dsl.Script{first, second})
	if result.AuraPayload == nil || result.AuraPayload.Complete || result.AuraPayload.RecognizedBodies != 2 || result.AuraPayload.InspectedBodies != 1 || result.AuraPayload.RejectedBodies != 1 || len(result.Warnings) == 0 {
		t.Fatalf("multi-script coverage=%+v", result.AuraPayload)
	}
	for _, s := range result.Suggestions {
		if s.ScriptName != first.Name {
			t.Fatal("selectable route lost script scope")
		}
	}
}

func TestMultiScriptAuraRetentionCountsSerializedScopeAndEscapedValues(t *testing.T) {
	scripts := []*dsl.Script{}
	for scriptIndex := 0; scriptIndex < 4; scriptIndex++ {
		script := syntheticAuraParameterizeScript(t)
		script.Name = strings.Repeat("synthetic_scope_", 10) + strings.Repeat("x", scriptIndex)
		records := make([]any, 100)
		for i := range records {
			records[i] = map[string]any{"recordId": strings.Repeat("\"\\\n", 60)}
		}
		inner, _ := json.Marshal(map[string]any{"records": records})
		message, _ := json.Marshal(map[string]any{"actions": []any{map[string]any{"id": "synthetic-id", "descriptor": "synthetic://save", "params": map[string]any{"input": string(inner)}}}})
		script.Actions[0].Body = "message=" + url.QueryEscape(string(message))
		// Legacy advice remains outside the new Aura detail budget.
		script.Actions = append(script.Actions, dsl.Action{Name: "legacy", Type: "GET", Target: "/ordinary?user_id=legacy"})
		scripts = append(scripts, script)
	}
	result := buildMultiScriptParameterization(scripts)
	retainedBytes, retainedRows, legacyRows := 0, 0, 0
	for _, s := range result.Suggestions {
		if len(s.Locations[0].AuraRoute) > 0 {
			row, err := json.Marshal(s)
			if err != nil {
				t.Fatal(err)
			}
			retainedBytes += len(row)
			retainedRows++
		} else {
			legacyRows++
		}
	}
	coverage := result.AuraPayload
	if coverage == nil || coverage.Complete || coverage.CandidateFields != 400 || coverage.RetainedCandidates != retainedRows || coverage.OmittedCandidates != 400-retainedRows || coverage.OmittedCandidates == 0 || retainedBytes > 256<<10 || legacyRows != 4 {
		t.Fatalf("multi-script retention bytes=%d rows=%d legacy=%d coverage=%+v", retainedBytes, retainedRows, legacyRows, coverage)
	}
}

func TestMultiScriptAuraSharesInspectionBudget(t *testing.T) {
	first := syntheticAuraParameterizeScript(t)
	first.Actions[0].Target = "/custom-rpc"
	first.Actions[0].Body = strings.Repeat("x", (64<<20)/8+1)
	second := syntheticAuraParameterizeScript(t)
	second.Name = "later script"
	result := buildMultiScriptParameterization([]*dsl.Script{first, second})
	if result.AuraPayload == nil || result.AuraPayload.Complete || result.AuraPayload.OmittedBodies != 2 || result.AuraPayload.InspectedBodies != 0 || len(result.Suggestions) != 0 {
		t.Fatalf("later script crossed work limit: %+v", result.AuraPayload)
	}
}

func TestAuraBusinessAdviceSurvivesCredentialProjectionAndProtectedRebind(t *testing.T) {
	for _, tc := range []struct {
		name, credential      string
		form, encoded, header bool
	}{
		{name: "plain", credential: "synthetic-credential-only"},
		{name: "json-string", credential: `{"opaque":{"credential":"synthetic-credential-only"}}`},
		{name: "form-plain", credential: "synthetic-credential-only", form: true},
		{name: "form-json-string", credential: `{"opaque":{"credential":"synthetic-credential-only"}}`, form: true},
		{name: "base64-form-plain", credential: "synthetic-credential-only", form: true, encoded: true},
		{name: "base64-form-json-string", credential: `{"opaque":{"credential":"synthetic-credential-only"}}`, form: true, encoded: true},
		{name: "mixed-header-form", credential: "synthetic-credential-only", form: true, header: true},
		{name: "mixed-header-base64-form", credential: `{"opaque":{"credential":"synthetic-credential-only"}}`, form: true, encoded: true, header: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := NewServer(ServerConfig{})
			script := syntheticAuraParameterizeScript(t)
			inner := `{"records":[{"recordId":"synthetic-selected"}]}`
			message, _ := json.Marshal(map[string]any{"actions": []any{map[string]any{"id": "synthetic-id", "descriptor": "synthetic://save", "params": map[string]any{"input": inner, "apiToken": tc.credential}}}})
			script.Actions[0].Body = "  \n" + string(message) + "\n "
			if tc.form {
				script.Actions[0].Body = "unrelated=one%20two&message=" + url.QueryEscape(string(message)) + "&unrelated=three+four"
			}
			if tc.encoded {
				script.Actions[0].Body = base64.StdEncoding.EncodeToString([]byte(script.Actions[0].Body))
				script.Actions[0].BodyEncoding = "base64"
			}
			if tc.header {
				script.Actions[0].Headers = map[string]string{"Authorization": "Bearer synthetic-guided-header"}
			}
			script.Config.BaseURL = "https://synthetic.invalid"
			script.ID = "synthetic-protected-aura-" + tc.name
			script.ProjectID = "project-a"
			safe := maskScriptSecrets(script)
			if safe == nil {
				t.Fatal("fixture credential projection failed")
			}
			safeWire, safeEncoded, decodeErr := dsl.DecodeBodyEncoding(safe.Actions[0].Body, safe.Actions[0].BodyEncoding, 8<<20)
			if !safeEncoded {
				safeWire = []byte(safe.Actions[0].Body)
			}
			if decodeErr != nil || strings.Contains(string(safeWire), "synthetic-credential-only") {
				t.Fatal("fixture credential did not project")
			}
			trusted := buildTrustedImportParameterization(safe)
			if len(trusted.Suggestions) != 1 {
				t.Fatalf("projected business candidates=%d coverage=%+v", len(trusted.Suggestions), trusted.AuraPayload)
			}
			selected := sanitizeImportParameterization(trusted, safe).Suggestions[0]
			ensureCredentialPreservingImportMetadata(script, "har")
			inspection := server.newScriptImportResponseWithSourceSecrets(httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/har", nil), scriptImportResponseOptions{Script: script})
			if failed, _ := inspection[importDraftResponseFailureKey].(bool); failed {
				t.Fatal("could not attest synthetic draft")
			}
			raw, _ := json.Marshal(inspection["script"])
			var submitted dsl.Script
			if err := json.Unmarshal(raw, &submitted); err != nil {
				t.Fatal(err)
			}
			oldToken := importDraftAttestationToken(&submitted)
			if oldToken == "" {
				t.Fatal("missing draft attestation")
			}
			tampered := cloneScriptDefinition(&submitted)
			tamperedBytes, tamperedEncoded, err := dsl.DecodeBodyEncoding(tampered.Actions[0].Body, tampered.Actions[0].BodyEncoding, 8<<20)
			if err != nil {
				t.Fatal(err)
			}
			if !tamperedEncoded {
				tamperedBytes = []byte(tampered.Actions[0].Body)
			}
			tamperedWire := strings.ReplaceAll(string(tamperedBytes), "synthetic-credential-only", "synthetic-tampered-credential")
			if tamperedWire == string(tamperedBytes) {
				t.Fatal("fixture tamper did not modify credential")
			}
			if tamperedEncoded {
				tampered.Actions[0].Body = base64.StdEncoding.EncodeToString([]byte(tamperedWire))
			} else {
				tampered.Actions[0].Body = tamperedWire
			}
			rejected, _ := postParameterizeApply(t, server, importParameterizeApplyRequest{Script: tampered, Suggestions: []paramize.Suggestion{selected}, SelectedIDs: []string{selected.ID}})
			if rejected.Code != http.StatusConflict {
				t.Fatal("changed source credential bypassed exact draft protection")
			}
			response, applied := postParameterizeApply(t, server, importParameterizeApplyRequest{Script: &submitted, Suggestions: []paramize.Suggestion{selected}, SelectedIDs: []string{selected.ID}})
			if response.Code != http.StatusOK {
				t.Fatalf("protected apply status=%d body=%s", response.Code, response.Body.String())
			}
			if applied.AppliedCount != 1 || applied.Script == nil || importDraftAttestationToken(applied.Script) == oldToken {
				t.Fatal("protected apply or capability rotation missing")
			}
			if applied.Script.Actions[0].BodyEncoding != script.Actions[0].BodyEncoding {
				t.Fatal("authored body encoding changed")
			}
			appliedBytes, appliedEncoded, decodeErr := dsl.DecodeBodyEncoding(applied.Script.Actions[0].Body, applied.Script.Actions[0].BodyEncoding, 8<<20)
			if decodeErr != nil {
				t.Fatal(decodeErr)
			}
			if !appliedEncoded {
				appliedBytes = []byte(applied.Script.Actions[0].Body)
			}
			appliedWire := string(appliedBytes)
			if tc.form {
				if !strings.HasPrefix(appliedWire, "unrelated=one%20two&message=") || !strings.HasSuffix(appliedWire, "&unrelated=three+four") {
					t.Fatal("unselected form source spans changed")
				}
				formValues, err := url.ParseQuery(appliedWire)
				if err != nil {
					t.Fatal(err)
				}
				appliedWire = formValues.Get("message")
			} else if !strings.HasPrefix(appliedWire, "  \n{") || !strings.HasSuffix(appliedWire, "}\n ") {
				t.Fatal("unselected JSON source spans changed")
			}
			var envelope struct {
				Actions []struct {
					Params struct {
						Input    string `json:"input"`
						APIToken string `json:"apiToken"`
					} `json:"params"`
				} `json:"actions"`
			}
			if err := json.Unmarshal([]byte(appliedWire), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Actions[0].Params.APIToken != tc.credential {
				t.Fatal("unselected attested credential bytes changed")
			}
			var business struct {
				Records []struct {
					RecordID string `json:"recordId"`
				} `json:"records"`
			}
			if err := json.Unmarshal([]byte(envelope.Actions[0].Params.Input), &business); err != nil {
				t.Fatal(err)
			}
			if business.Records[0].RecordID != selected.Replacement {
				t.Fatal("reviewed decoded business field not replaced")
			}
			if created := createAttestedImportScript(t, server, applied.Script); created.Code != http.StatusCreated {
				t.Fatalf("protected save status=%d body=%s", created.Code, created.Body.String())
			}
			durable, exists := server.runner.GetScript(applied.Script.ID)
			if !exists || durable == nil || durable.Script == nil || durable.Script.Actions[0].Body != applied.Script.Actions[0].Body {
				t.Fatal("saved executable body did not preserve exact apply result")
			}
		})
	}
}

func TestAuraNestedFormCredentialDisclosureBoundary(t *testing.T) {
	script := syntheticAuraParameterizeScript(t)
	message, _ := json.Marshal(map[string]any{"actions": []any{map[string]any{"id": "synthetic-id", "descriptor": "synthetic://save", "params": map[string]any{"recordId": "synthetic-business", "apiToken": "synthetic-nested-credential"}}}})
	script.Actions[0].Body = "unrelated=one%20two&message=" + url.QueryEscape(string(message)) + "&unrelated=three+four"
	safe := maskScriptSecrets(script)
	if safe == nil {
		t.Fatal("disclosure projection failed")
	}
	if strings.Contains(safe.Actions[0].Body, "synthetic-nested-credential") {
		t.Fatal("ordinary disclosure projection exposes nested form credential")
	}
}

// bodyPlacements puts request at the top level, inside a control step and
// inside an HTTP sequence.
func bodyPlacements(request dsl.Action) map[string]dsl.Action {
	return map[string]dsl.Action{
		"top-level": request,
		"control":   {Type: "control_loop", Control: &dsl.ControlFlow{Steps: []dsl.Action{request}}},
		"sequence":  {Type: "http_sequence", Params: map[string]any{"requests": []any{map[string]any{"type": "POST", "target": request.Target, "body": request.Body}}}},
	}
}

// A body difference review cannot locate, here a SOAP body whose token-like
// message ID review shows as {{fn.uuid()}}, is protected by the draft's exact
// execution digest.
func TestRequestBodyProjectionDifferenceRequiresExactDraftProtection(t *testing.T) {
	request := dsl.Action{Type: "POST", Target: "/ws", Body: unalignedSOAPBody("synthetic-credential", "")}
	for name, action := range bodyPlacements(request) {
		t.Run(name, func(t *testing.T) {
			script := &dsl.Script{Name: "safe", Protocol: "http", Actions: []dsl.Action{action}}
			projected := maskScriptSecrets(script)
			if projected == nil || !runtimeCredentialSourceProjectionChanged(script, projected) {
				t.Fatal("unlocated body credential change had no exact draft fallback")
			}
			fingerprint, err := fingerprintImportDraftCredentials(script)
			if err != nil || !fingerprint.HasRawCredentials || fingerprint.FullDigest == "" {
				t.Fatal("credential body lacked exact execution fingerprint")
			}
			unchanged := cloneScriptDefinition(script)
			if runtimeCredentialSourceProjectionChanged(script, unchanged) {
				t.Fatal("unchanged body was classified as a credential difference")
			}
		})
	}
}

// A credential nested in a JSON form value keeps its recorded encoding, so
// review locates it and its digest protects the draft, as for any guided body
// credential, instead of the draft's exact execution digest.
func TestLocatedNestedFormCredentialKeepsGuidedDraftProtection(t *testing.T) {
	request := dsl.Action{Type: "POST", Target: "/aura", Body: "message=" + url.QueryEscape(`{"actions":[{"id":"synthetic-id","descriptor":"synthetic://save","params":{"apiToken":"synthetic-credential","recordId":"safe"}}]}`)}
	for name, action := range bodyPlacements(request) {
		t.Run(name, func(t *testing.T) {
			script := &dsl.Script{Name: "safe", Protocol: "http", Actions: []dsl.Action{action}}
			fingerprint, err := fingerprintImportDraftCredentials(script)
			if err != nil || !fingerprint.HasRawCredentials || fingerprint.FullDigest != "" ||
				fingerprint.StructureDigest == "" || len(fingerprint.CredentialDigests) != 1 {
				t.Fatalf("fingerprint = %+v, %v; want the located credential's digest", fingerprint, err)
			}
		})
	}
}

func TestLocatedBodyProjectionKeepsGuidedEditCompatibility(t *testing.T) {
	script := &dsl.Script{Name: "safe", Protocol: "http", Actions: []dsl.Action{{Type: "POST", Target: "/", Body: `{"password":"synthetic-guided","recordId":"safe"}`}}}
	projected := maskScriptSecrets(script)
	if projected == nil {
		t.Fatal("projection failed")
	}
	requirements := secretBindingRequirementsForImportScripts(importResponseRawScriptValue(projected))
	if len(requirements) != 1 || unlocatedRequestBodyProjectionChanged(script, projected, requirements) {
		t.Fatal("complete guided body locator lost its compatibility")
	}
	fingerprint, err := fingerprintImportDraftCredentials(script)
	if err != nil || !fingerprint.HasRawCredentials || fingerprint.StructureDigest == "" || fingerprint.FullDigest != "" {
		t.Fatal("guided credential body unnecessarily became a full exact draft")
	}
}
