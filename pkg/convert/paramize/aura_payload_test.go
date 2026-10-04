package paramize

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"github.com/jasonczech/apexload/pkg/internal/aurapayload"
)

func syntheticAuraBody(params any) string {
	b, _ := json.Marshal(map[string]any{"actions": []any{map[string]any{"id": "synthetic-1;a", "descriptor": "synthetic://save", "params": params}}})
	return string(b)
}
func syntheticAuraScript(body, encoding string) *dsl.Script {
	return &dsl.Script{Name: "synthetic Aura", Version: "1.0", Protocol: "http", Actions: []dsl.Action{{Name: "synthetic batch", Type: "http_request", Method: "POST", Target: "/aura", Body: body, BodyEncoding: encoding}}}
}
func auraOnlyAdvice(script *dsl.Script) Result {
	return Suggest(script, Options{EnableDataParams: true})
}
func syntheticSnapshot(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestAuraAdviceExactRoutesAcrossEncodingLayers(t *testing.T) {
	inner := `{"records":[{"recordId":"same"},{"recordId":"same"}],"accountId":"other"}`
	second, _ := json.Marshal(map[string]any{"data": inner})
	raw := syntheticAuraBody(map[string]any{"input": string(second), "description": "same"})
	form := "unrelated=one%20two&message=" + url.QueryEscape(raw) + "&unrelated=three+four&aura.token=synthetic"
	for _, tc := range []struct {
		name, body, encoding string
		layers               []string
	}{{"raw", raw, "", []string{"json", "json_string", "json_string"}}, {"form", form, "", []string{"url_form", "json", "json_string", "json_string"}}, {"base64", base64.StdEncoding.EncodeToString([]byte(form)), "base64", []string{"base64", "url_form", "json", "json_string", "json_string"}}} {
		t.Run(tc.name, func(t *testing.T) {
			script := syntheticAuraScript(tc.body, tc.encoding)
			before := syntheticSnapshot(t, script)
			result := auraOnlyAdvice(script)
			if result.AuraPayload == nil || !result.AuraPayload.Complete || len(result.Suggestions) != 3 {
				t.Fatalf("coverage/count = %+v/%d", result.AuraPayload, len(result.Suggestions))
			}
			var choice Suggestion
			names := map[string]bool{}
			for _, s := range result.Suggestions {
				l := s.Locations[0]
				if !reflect.DeepEqual(l.EncodingLayers, tc.layers) {
					t.Fatalf("unexpected encoding layers: %v", l.EncodingLayers)
				}
				if names[s.VarName] {
					t.Fatal("distinct routes shared a variable")
				}
				names[s.VarName] = true
				if strings.Contains(l.DecodedFieldPath, "records[0]") {
					choice = s
				}
			}
			if choice.ID == "" {
				t.Fatal("missing exact first-record candidate")
			}
			applied, diffs, err := ApplySuggestions(script, result.Suggestions, map[string]bool{choice.ID: true})
			if err != nil {
				t.Fatal(err)
			}
			if len(diffs) != 3 || applied.Actions[0].Metadata[dsl.ImportURLFormTemplateValueEncodingMetadataKey] != dsl.ImportAuraJSONTemplateValueEncoding {
				t.Fatalf("missing executable metadata or data diffs: %d", len(diffs))
			}
			inspection, err := aurapayload.Inspect(applied.Actions[0].Body, applied.Actions[0].BodyEncoding)
			if err != nil {
				t.Fatal(err)
			}
			changed, unchanged := 0, 0
			for _, leaf := range inspection.Leaves {
				if leaf.Value == choice.Replacement {
					changed++
				}
				if leaf.Key == "recordId" && leaf.Value == "same" {
					unchanged++
				}
			}
			if changed != 1 || unchanged != 1 {
				t.Fatalf("exact route changed=%d unchanged sibling=%d", changed, unchanged)
			}
			if tc.name != "raw" {
				wire := applied.Actions[0].Body
				if tc.encoding != "" {
					b, _ := base64.StdEncoding.DecodeString(wire)
					wire = string(b)
				}
				if !strings.HasPrefix(wire, "unrelated=one%20two&message=") || !strings.HasSuffix(wire, "&unrelated=three+four&aura.token=synthetic") {
					t.Fatal("unrelated form source spans changed")
				}
			}
			if syntheticSnapshot(t, script) != before {
				t.Fatal("input mutated")
			}
			if !reflect.DeepEqual(result, auraOnlyAdvice(script)) {
				t.Fatal("advice not deterministic")
			}
		})
	}
}

func TestAuraAdviceBusinessTypesAndCredentialParents(t *testing.T) {
	params := map[string]any{"recordId": 17, "accountId": true, "userId": nil, "externalId": "business", "clientId": "config", "oauthClientId": "config", "context": map[string]any{"recordId": "private"}, "session": map[string]any{"accountId": "private"}, "opaque-captured-map-key": map[string]any{"recordId": "private"}, "accounts": map[string]any{"user_jane_example_com": map[string]any{"recordId": "private"}}}
	result := auraOnlyAdvice(syntheticAuraScript(syntheticAuraBody(params), ""))
	c := result.AuraPayload
	if c == nil || c.Complete || c.UnsupportedNumberFields != 1 || c.UnsupportedOtherFields != 2 || c.UnsafePathFields != 2 || len(result.Suggestions) != 1 {
		t.Fatalf("unexpected safe coverage: %+v/%d", c, len(result.Suggestions))
	}
	if result.Suggestions[0].VarName != "externalid" {
		t.Fatalf("wrong business leaf retained")
	}
	b := syntheticSnapshot(t, result)
	if strings.Contains(b, "opaque-captured-map-key") || strings.Contains(b, "user_jane_example_com") || strings.Contains(b, "private") {
		t.Fatal("unsafe or credential-parent material entered advice")
	}
	for _, key := range []string{"recordId", "externalId", "accountId", "user_id", "orderSKU"} {
		if !LooksAuraParameterName(key) {
			t.Errorf("business key %q not recognized", key)
		}
	}
	for _, key := range []string{"clientId", "oauthClientId", "oauth2ClientId", "fwuid", "applicationContext"} {
		if LooksAuraParameterName(key) {
			t.Errorf("configuration key %q recognized", key)
		}
	}
}

func TestAuraAdviceMalformedBodiesNeverFallBackToRegex(t *testing.T) {
	for _, body := range []string{`{"actions":[{"id":"synthetic","descriptor":"synthetic://save","params":{"user_id":"business"}}],`, "message=" + url.QueryEscape(syntheticAuraBody(map[string]any{"user_id": "business"})) + "&message=" + url.QueryEscape(syntheticAuraBody(map[string]any{"user_id": "business"})), `{"actions":[{"id":"synthetic","descriptor":"synthetic://save","params":{"user_id":"business","user_id":"ambiguous"}}]}`} {
		result := auraOnlyAdvice(syntheticAuraScript(body, ""))
		if result.AuraPayload == nil || result.AuraPayload.Complete || result.AuraPayload.RejectedBodies != 1 || len(result.Suggestions) != 0 || len(result.Warnings) == 0 {
			t.Fatalf("malformed payload produced blind advice: %+v", result.AuraPayload)
		}
	}
	// Arbitrary REST descriptor fields retain the established generic path.
	script := syntheticAuraScript(`{"descriptor":"REST business text","user_id":"ordinary"}`, "")
	script.Actions[0].Target = "/services/data/v60.0/sobjects/Account"
	result := auraOnlyAdvice(script)
	if result.AuraPayload != nil || len(result.Suggestions) != 1 || len(result.Suggestions[0].Locations[0].AuraRoute) != 0 {
		t.Fatal("REST descriptor misclassified")
	}
}

func TestAuraAdviceExactNestedControlAndSequencePaths(t *testing.T) {
	body := syntheticAuraBody(map[string]any{"recordId": "business"})
	request := func(name string) dsl.Action {
		return dsl.Action{Name: name, Type: "http_request", Method: "POST", Target: "/aura", Body: body}
	}
	script := &dsl.Script{Name: "nested synthetic", Version: "1.0", Protocol: "http", Actions: []dsl.Action{{Name: "parallel", Type: "control_parallel", Control: &dsl.ControlFlow{Branches: []dsl.ParallelBranch{{Steps: []dsl.Action{request("first")}}, {Steps: []dsl.Action{{Name: "sequence", Type: "http_sequence", Params: map[string]any{"requests": []any{map[string]any{"name": "second", "method": "POST", "target": "/aura", "body": body}}}}}}}}}}}
	result := auraOnlyAdvice(script)
	if len(result.Suggestions) != 2 {
		t.Fatalf("nested advice=%d", len(result.Suggestions))
	}
	choice := result.Suggestions[1]
	path := choice.Locations[0].ActionPath
	if path != "actions[0].control.branches[1].steps[0].params.requests[0]" {
		t.Fatalf("wrong authoritative path: %s", path)
	}
	applied, _, err := ApplySuggestions(script, result.Suggestions, map[string]bool{choice.ID: true})
	if err != nil {
		t.Fatal(err)
	}
	changed := 0
	err = dslwalk.Walk(applied, func(ref dslwalk.SurfaceRef) error {
		_, b, _ := surfaceBody(ref)
		if strings.Contains(b, choice.Replacement) {
			if ref.Path != path {
				t.Fatal("wrong nested surface changed")
			}
			changed++
			if ref.Metadata()[dsl.ImportURLFormTemplateValueEncodingMetadataKey] != dsl.ImportAuraJSONTemplateValueEncoding {
				t.Fatal("sequence metadata missing")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if changed != 1 {
		t.Fatalf("changed=%d", changed)
	}
	reopened := new(dsl.Script)
	if err := json.Unmarshal([]byte(syntheticSnapshot(t, applied)), reopened); err != nil {
		t.Fatal(err)
	}
	if result := auraOnlyAdvice(reopened); len(result.Suggestions) != 1 {
		t.Fatalf("reopened remaining advice=%d", len(result.Suggestions))
	}
}

func TestAuraApplyRejectsStaleTamperedAndOverlappingEditsAtomically(t *testing.T) {
	script := syntheticAuraScript(syntheticAuraBody(map[string]any{"recordId": "business", "accountId": "other"}), "")
	result := auraOnlyAdvice(script)
	for _, tc := range []struct {
		name string
		edit func(*dsl.Script, *Suggestion)
	}{
		{"value", func(s *dsl.Script, selected *Suggestion) {
			s.Actions[0].Body = strings.Replace(s.Actions[0].Body, selected.LiteralValue, "changed", 1)
		}},
		{"shape", func(s *dsl.Script, _ *Suggestion) {
			s.Actions[0].Body = strings.Replace(s.Actions[0].Body, `"accountId":"other"`, `"accountId":"other","userId":"new"`, 1)
		}},
		{"path", func(_ *dsl.Script, s *Suggestion) { s.Locations[0].ActionPath = "actions[999]" }},
		{"route", func(_ *dsl.Script, s *Suggestion) {
			s.Locations[0].AuraRoute[len(s.Locations[0].AuraRoute)-1].Key = "descriptor"
		}},
		{"layers", func(_ *dsl.Script, s *Suggestion) { s.Locations[0].EncodingLayers = []string{"url_form", "json"} }},
		{"display", func(_ *dsl.Script, s *Suggestion) { s.Locations[0].DecodedFieldPath = "$.captured" }},
		{"replacement", func(_ *dsl.Script, s *Suggestion) { s.Locations[0].ReplaceText = "injected" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := cloneScript(script)
			b, _ := json.Marshal(result.Suggestions)
			var advice []Suggestion
			_ = json.Unmarshal(b, &advice)
			tc.edit(s, &advice[0])
			before := syntheticSnapshot(t, s)
			applied, diffs, err := ApplySuggestions(s, advice, map[string]bool{advice[0].ID: true})
			if err == nil || applied != nil || diffs != nil {
				t.Fatal("stale descriptor accepted")
			}
			if before != syntheticSnapshot(t, s) {
				t.Fatal("failed apply mutated source")
			}
		})
	}
	duplicate := result.Suggestions[0]
	duplicate.ID = "independent duplicate"
	advice := append(append([]Suggestion(nil), result.Suggestions...), duplicate)
	if applied, _, err := ApplySuggestions(script, advice, map[string]bool{duplicate.ID: true, result.Suggestions[0].ID: true}); err == nil || applied != nil {
		t.Fatal("overlapping route accepted")
	}
	literal := Suggestion{ID: "literal", Kind: KindDataParam, Locations: []Location{{Section: "actions", ActionIndex: 0, Field: "body", SearchText: "business", ReplaceText: "{{var.literal}}"}}}
	if applied, _, err := ApplySuggestions(script, append(advice, literal), map[string]bool{result.Suggestions[0].ID: true, literal.ID: true}); err == nil || applied != nil {
		t.Fatal("mixed literal/structured overlap accepted")
	}
}

func TestAuraAdviceRetainsExplicitCountsPastDetailBudget(t *testing.T) {
	records := make([]any, 2400)
	for i := range records {
		records[i] = map[string]any{"recordId": "business"}
	}
	result := auraOnlyAdvice(syntheticAuraScript(syntheticAuraBody(map[string]any{"records": records}), ""))
	c := result.AuraPayload
	if c == nil || c.CandidateFields != 2400 || c.RetainedCandidates != len(result.Suggestions) || c.OmittedCandidates != 2400-len(result.Suggestions) || c.OmittedCandidates == 0 || c.Complete {
		t.Fatalf("wrong bounded detail counts: %+v", c)
	}
	if len(syntheticSnapshot(t, result)) > 2<<20 {
		t.Fatal("bounded advice exceeded conservative disclosure-size budget")
	}
}

func TestAuraAdviceEscapedKeysAndIndependentSelectedBodies(t *testing.T) {
	body := `{"actions":[{"id":"synthetic-1;a","descriptor":"synthetic://save","params":{"inpu\u0074":{"records":[{"record\u0049d":"business"}]}}}]}`
	script := syntheticAuraScript(body, "")
	script.Actions = append(script.Actions, script.Actions[0])
	script.Actions[1].Type = "POST"
	result := auraOnlyAdvice(script)
	if len(result.Suggestions) != 2 {
		t.Fatalf("escaped decoded advice=%d", len(result.Suggestions))
	}
	selected := map[string]bool{}
	for _, s := range result.Suggestions {
		selected[s.ID] = true
		if !strings.HasSuffix(s.Locations[0].DecodedFieldPath, "recordId") {
			t.Fatal("escaped structural key not decoded")
		}
	}
	applied, _, err := ApplySuggestions(script, result.Suggestions, selected)
	if err != nil {
		t.Fatal(err)
	}
	for i := range applied.Actions {
		inspection, err := aurapayload.Inspect(applied.Actions[i].Body, "")
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, leaf := range inspection.Leaves {
			if leaf.Key == "recordId" && strings.HasPrefix(leaf.Value, "{{var.") {
				found = true
			}
		}
		if !found {
			t.Fatal("independent body not applied")
		}
	}
}

func TestAuraAdviceWalksEveryControlBranchAndAvoidsNestedExtractorCollision(t *testing.T) {
	body := syntheticAuraBody(map[string]any{"recordId": "business"})
	request := func() dsl.Action { return dsl.Action{Type: "POST", Target: "/aura", Body: body} }
	script := &dsl.Script{Actions: []dsl.Action{
		{Type: "control_loop", Control: &dsl.ControlFlow{Steps: []dsl.Action{request()}}},
		{Type: "control_if", Control: &dsl.ControlFlow{Steps: []dsl.Action{request()}, Else: []dsl.Action{request()}}},
		{Type: "control_switch", Control: &dsl.ControlFlow{Cases: []dsl.SwitchCase{{Steps: []dsl.Action{request()}}, {Steps: []dsl.Action{request()}}}, Default: []dsl.Action{request()}}},
		{Type: "control_parallel", Control: &dsl.ControlFlow{Branches: []dsl.ParallelBranch{{Steps: []dsl.Action{request()}}, {Steps: []dsl.Action{request()}}}}},
	}}
	script.Actions[0].Control.Steps[0].Extractors = []dsl.Extractor{{Name: "recordid", Type: "jsonpath", Expression: "$.synthetic"}}
	result := auraOnlyAdvice(script)
	if len(result.Suggestions) != 8 || result.AuraPayload == nil || result.AuraPayload.InspectedBodies != 8 {
		t.Fatalf("all branches count=%d", len(result.Suggestions))
	}
	selected := map[string]bool{}
	for _, s := range result.Suggestions {
		if s.VarName == "recordid" {
			t.Fatal("nested extractor namespace collided")
		}
		selected[s.ID] = true
	}
	applied, _, err := ApplySuggestions(script, result.Suggestions, selected)
	if err != nil {
		t.Fatal(err)
	}
	updated := 0
	_ = dslwalk.Walk(applied, func(ref dslwalk.SurfaceRef) error {
		_, body, _ := surfaceBody(ref)
		if strings.Contains(body, "{{var.") {
			updated++
		}
		return nil
	})
	if updated != 8 {
		t.Fatalf("updated branches=%d", updated)
	}
}

func TestAuraAdviceAggregateWorkLimitRemainsExplicit(t *testing.T) {
	padding := strings.Repeat("x", 1<<20)
	body := syntheticAuraBody(map[string]any{"recordId": "business", "description": padding})
	script := syntheticAuraScript(body, "")
	for i := 0; i < 39; i++ {
		script.Actions = append(script.Actions, script.Actions[0])
	}
	result := auraOnlyAdvice(script)
	coverage := result.AuraPayload
	if coverage == nil || coverage.Complete || coverage.RejectedBodies+coverage.OmittedBodies == 0 || coverage.InspectedBodies >= 40 || coverage.RecognizedBodies != 40 {
		t.Fatalf("aggregate limit coverage: %+v", coverage)
	}
	if len(result.Warnings) == 0 {
		t.Fatal("aggregate limit warning missing")
	}
}

func TestAuraAdviceSkipsValuesClaimedByCredentialPass(t *testing.T) {
	script := syntheticAuraScript(syntheticAuraBody(map[string]any{"recordId": "claimed-value", "accountId": "business"}), "")
	advice, coverage, _ := auraPayloadSuggestions(script, newNameRegistry(script), map[string]bool{"claimed-value": true})
	if len(advice) != 1 || advice[0].LiteralValue != "business" || coverage.StringFields != 2 || coverage.CandidateFields != 1 {
		t.Fatal("claimed correlation/credential value proposed again")
	}
}

func TestAuraPublicShapeAndIDsExcludeHiddenKeysAndAllValues(t *testing.T) {
	body := func(hiddenKey, business string) string {
		return syntheticAuraBody(map[string]any{"recordId": business, "accounts": map[string]any{hiddenKey: map[string]any{"recordId": "private"}}})
	}
	first := auraOnlyAdvice(syntheticAuraScript(body("user_jane_example_com", "one"), ""))
	second := auraOnlyAdvice(syntheticAuraScript(body("user_other_example_com", "two"), ""))
	if len(first.Suggestions) != 1 || len(second.Suggestions) != 1 {
		t.Fatal("safe candidate unavailable")
	}
	a, b := first.Suggestions[0], second.Suggestions[0]
	if a.ID != b.ID || a.Locations[0].PayloadShape != b.Locations[0].PayloadShape {
		t.Fatal("public identity depends on hidden key or literal")
	}
	if a.Locations[0].auraExpectedShape == b.Locations[0].auraExpectedShape {
		t.Fatal("internal full shape unexpectedly ignores distinct hidden keys")
	}
	encoded := syntheticSnapshot(t, first)
	if strings.Contains(encoded, a.Locations[0].auraExpectedShape) {
		t.Fatal("internal shape escaped on wire")
	}
	// Direct in-memory advice retains the full private shape for atomic stale
	// checks, without exporting it in the browser review descriptor.
	changed := syntheticAuraScript(body("user_other_example_com", "one"), "")
	if applied, _, err := ApplySuggestions(changed, first.Suggestions, map[string]bool{a.ID: true}); err == nil || applied != nil {
		t.Fatal("private structural drift accepted by direct apply")
	}
}

func TestAuraCandidateDetailLimitCountsActualSerializedRows(t *testing.T) {
	records := make([]any, 1000)
	for i := range records {
		records[i] = map[string]any{"recordId": strings.Repeat("\"\\\n", 100)}
	}
	result := auraOnlyAdvice(syntheticAuraScript(syntheticAuraBody(map[string]any{"records": records}), ""))
	retainedBytes := 0
	for _, s := range result.Suggestions {
		b, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		retainedBytes += len(b)
	}
	if retainedBytes > maxAuraCandidateBytes || result.AuraPayload.OmittedCandidates == 0 || result.AuraPayload.RetainedCandidates != len(result.Suggestions) {
		t.Fatalf("serialized retention bytes=%d coverage=%+v", retainedBytes, result.AuraPayload)
	}
}

func TestAuraRecognitionUsesConcreteAPIPrecedenceAndBoundedStreaming(t *testing.T) {
	valid := syntheticAuraBody(map[string]any{"user_id": "business"})
	for _, target := range []string{"/services/data/v60.0/sobjects/Account", "/services/data/v60.0/sobjects/aura", "/services/oauth2/token", "/services/apexrest/custom", "/ui-api/records"} {
		for _, body := range []string{valid, strings.TrimSuffix(valid, "}")} {
			script := syntheticAuraScript(body, "")
			script.Actions[0].Target = target
			result := auraOnlyAdvice(script)
			if result.AuraPayload != nil || len(result.Suggestions) == 0 || len(result.Suggestions[0].Locations[0].AuraRoute) > 0 {
				t.Fatal("concrete API was co-opted by Aura-shaped body")
			}
		}
	}
	for _, tc := range []struct{ body, encoding string }{
		{strings.Repeat("&", 1000000) + "message=" + url.QueryEscape(valid), ""},
		{base64.StdEncoding.EncodeToString([]byte(valid))[:8] + strings.Repeat(" \n", 100000) + base64.StdEncoding.EncodeToString([]byte(valid))[8:], "base64"},
		{base64.StdEncoding.EncodeToString([]byte(valid))[:8] + " " + base64.StdEncoding.EncodeToString([]byte(valid))[8:], "base64"},
	} {
		script := syntheticAuraScript(tc.body, tc.encoding)
		script.Actions[0].Target = "/custom-rpc"
		result := auraOnlyAdvice(script)
		if result.AuraPayload == nil || result.AuraPayload.Complete || result.AuraPayload.OmittedBodies != 1 || len(result.Suggestions) != 0 {
			t.Fatalf("unbounded/invalid discovery admitted: %+v", result.AuraPayload)
		}
	}
	wrapped := base64.StdEncoding.EncodeToString([]byte(valid))
	wrapped = wrapped[:16] + "\r\n" + wrapped[16:]
	script := syntheticAuraScript(wrapped, " Base64 ")
	script.Actions[0].Target = "/custom-rpc"
	if result := auraOnlyAdvice(script); len(result.Suggestions) != 1 || result.AuraPayload == nil || !result.AuraPayload.Complete {
		t.Fatal("runtime-valid wrapped base64 not inspected")
	}
}

func TestAuraTerminalPathKeysRejectIdentifierShapedPrivateSuffixes(t *testing.T) {
	for _, key := range []string{"user_jane_example_com", "record" + strings.Repeat("_", 100) + "id", "account_private_capture"} {
		result := auraOnlyAdvice(syntheticAuraScript(syntheticAuraBody(map[string]any{key: "private"}), ""))
		if result.AuraPayload == nil || result.AuraPayload.UnsafePathFields != 1 || result.AuraPayload.Complete || len(result.Suggestions) != 0 {
			t.Fatalf("unsafe terminal disclosure not counted for key length%d", len(key))
		}
		if strings.Contains(syntheticSnapshot(t, result), key) {
			t.Fatal("private terminal key disclosed")
		}
	}
}

func TestAuraRecognitionStreamsSlashHeavyTarget(t *testing.T) {
	script := syntheticAuraScript(syntheticAuraBody(map[string]any{"recordId": "business"}), "")
	script.Actions[0].Target = strings.Repeat("/", 1<<20) + "custom-rpc"
	result := auraOnlyAdvice(script)
	if result.AuraPayload == nil || !result.AuraPayload.Complete || len(result.Suggestions) != 1 {
		t.Fatal("slash-heavy target could not be scanned within its byte budget")
	}
}

func TestAuraSharedWorkBudgetStopsLaterScriptsWithoutBodyScan(t *testing.T) {
	budget := NewAuraInspectionBudget()
	first := syntheticAuraScript(strings.Repeat("x", maxAuraInspectionBytes/8+1), "")
	first.Actions[0].Target = "/custom-rpc"
	result := Suggest(first, Options{EnableDataParams: true, AuraInspectionBudget: budget})
	if budget.UsedWorkBytes() != maxAuraInspectionBytes || result.AuraPayload == nil || result.AuraPayload.Complete || result.AuraPayload.OmittedBodies != 1 {
		t.Fatal("shared work exhaustion not explicit")
	}
	later := syntheticAuraScript(syntheticAuraBody(map[string]any{"recordId": "business"}), "")
	result = Suggest(later, Options{EnableDataParams: true, AuraInspectionBudget: budget})
	if len(result.Suggestions) != 0 || result.AuraPayload == nil || result.AuraPayload.Complete || result.AuraPayload.InspectedBodies != 0 || result.AuraPayload.OmittedBodies != 1 || budget.UsedWorkBytes() != maxAuraInspectionBytes {
		t.Fatal("later script inspected past shared budget")
	}
}
