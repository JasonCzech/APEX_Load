package convertutil

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestNeutralizeDSLTemplates(t *testing.T) {
	script := &dsl.Script{
		Name:     "template-safety",
		Version:  "1.0.0",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		DataSources: []dsl.DataSource{
			{Name: "declared_asset", Type: "file", InlineData: "hello"},
		},
	}
	declared := DeclaredFileDataSources(script)
	if !declared["declared_asset"] {
		t.Fatalf("DeclaredFileDataSources = %#v, want declared_asset", declared)
	}

	raw := `secret="P@ssw0rd!" id="{{run.id}}" bad="{{run.name}}" triple="{{{run.status}}}" ok="{{file.declared_asset}}" missing="{{file.unknown_asset}}"`

	scriptMeta := NeutralizeScriptMetadataString(raw)
	if HasAnyRunTemplate(scriptMeta) {
		t.Fatalf("NeutralizeScriptMetadataString left run.* token: %q", scriptMeta)
	}
	if !HasUndeclaredFileTemplateRef(scriptMeta, declared) {
		t.Fatalf("NeutralizeScriptMetadataString altered file.* token unnecessarily: %q", scriptMeta)
	}

	actionMeta := NeutralizeActionMetadataString(raw)
	if HasUnsupportedRunTemplate(actionMeta) {
		t.Fatalf("NeutralizeActionMetadataString left unsupported run.* token: %q", actionMeta)
	}
	if !HasAnyRunTemplate(actionMeta) {
		t.Fatalf("NeutralizeActionMetadataString did not preserve valid {{run.id}}: %q", actionMeta)
	}

	surface := NeutralizeActionSurfaceString(raw, declared)
	if HasUnsupportedRunTemplate(surface) || HasUndeclaredFileTemplateRef(surface, declared) {
		t.Fatalf("NeutralizeActionSurfaceString left invalid surface token: %q", surface)
	}

	script.Metadata = &dsl.ScriptMetadata{
		Import: &dsl.ImportMetadata{
			Source:    "test",
			Preserved: map[string]any{"raw": scriptMeta},
		},
	}
	script.Actions = []dsl.Action{{
		Name:  NeutralizeScriptMetadataString("review {{run.id}} {{run.name}}"),
		Type:  "source_review",
		Value: surface,
		Params: NeutralizeActionSurfaceValue(map[string]any{
			"call_args": []map[string]any{{"raw": raw, "value": raw}},
		}, declared).(map[string]any),
		Metadata: NeutralizeActionMetadataValue(map[string]any{
			"source_text":       raw,
			"source_attributes": map[string]string{"attr": raw, "{{run.id}}": "v"},
		}).(map[string]any),
	}}
	if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("ValidateScriptIssues = %#v, want empty", issues)
	}
}

func TestNeutralizeScriptMetadataDeepCopyOnWriteAndCycleSafety(t *testing.T) {
	type customLabel string
	type nestedReview struct {
		Label    customLabel
		Contents []string
		Next     *nestedReview
		private  string
	}
	orig := &nestedReview{
		Label:    "banner={{run.id}}/{{run.name}}",
		Contents: []string{"plain", "tpl={{run.status}}"},
		private:  "keep-private",
	}
	orig.Next = orig // self-cycle

	sanitized, ok := NeutralizeScriptMetadataDeep(orig).(*nestedReview)
	if !ok || sanitized == nil {
		t.Fatalf("NeutralizeScriptMetadataDeep type = %T, want *nestedReview", sanitized)
	}
	if orig.Label != "banner={{run.id}}/{{run.name}}" || orig.Contents[1] != "tpl={{run.status}}" {
		t.Fatalf("NeutralizeScriptMetadataDeep mutated caller struct: %+v", orig)
	}
	if HasAnyRunTemplate(string(sanitized.Label)) || HasAnyRunTemplate(sanitized.Contents[1]) {
		t.Fatalf("NeutralizeScriptMetadataDeep left run.* token: %+v", sanitized)
	}
	if sanitized.private != "keep-private" {
		t.Fatalf("NeutralizeScriptMetadataDeep lost unexported field: %q", sanitized.private)
	}

	clean := &nestedReview{Label: "no-run-templates", Contents: []string{"{{file.keep}}"}}
	if got := NeutralizeScriptMetadataDeep(clean).(*nestedReview); got != clean {
		t.Fatal("NeutralizeScriptMetadataDeep allocated on unchanged value")
	}
}

func TestSanitizeScriptDSLTemplatesCoversAllValidatedSurfaces(t *testing.T) {
	type customMetaStruct struct {
		Title string
		Items []string
	}
	defVal := "{{run.name}}"
	script := &dsl.Script{
		Name:     "Script {{run.id}} {{run.name}}",
		Version:  "1.0.0",
		Protocol: "http",
		Config: dsl.ScriptConfig{
			BaseURL: "https://api.example.test/{{run.id}}/{{run.name}}",
			HTTP: dsl.HTTPConfig{
				DefaultHeaders: map[string]string{
					"X-Custom-Header": "ok-{{run.id}}-{{run.name}}",
				},
			},
		},
		DataSources: []dsl.DataSource{
			{Name: "declared_file", Type: "file", InlineData: "content"},
		},
		Parameters: []dsl.Parameter{
			{Name: "p1", Type: "list", Values: []string{"{{run.id}}", "{{run.name}}"}},
		},
		Metadata: &dsl.ScriptMetadata{
			Import: &dsl.ImportMetadata{
				Source: "test-{{run.id}}",
				Preserved: map[string]any{
					"struct": customMetaStruct{Title: "{{run.id}}", Items: []string{"{{run.name}}"}},
				},
			},
		},
		Actions: []dsl.Action{
			{
				Name:   "ValidRuntimeRunIDInStringMap {{run.id}}",
				Type:   "POST",
				Method: "POST",
				Target: "https://api.example.test/submit?file={{file.declared_file}}&run={{run.id}}",
				Params: map[string]any{
					"string_map": map[string]string{"k": "{{run.id}}"},
				},
				Metadata: map[string]any{
					"struct_meta": customMetaStruct{Title: "{{run.id}}", Items: []string{"{{run.name}}"}},
				},
			},
			{
				Name:   "InvalidMultipartAndAuthDomain {{run.name}}",
				Type:   "POST",
				Method: "POST",
				Target: "https://api.example.test/upload",
				Auth: &dsl.AuthConfig{
					Type:     "ntlm",
					Username: "u",
					Password: "p",
					Domain:   "{{file.undeclared_domain}}",
				},
				BodyMultipart: &dsl.MultipartBody{
					Parts: []dsl.MultipartPart{
						{Name: "f", Value: "{{run.id}}", ValueEncoding: "base64"},
					},
				},
				Extractors: []dsl.Extractor{
					{Name: "ex", Source: "body", Type: "regex", Expression: "a={{run.name}}", DefaultValue: &defVal},
				},
			},
		},
	}

	gated := 0
	SanitizeScriptDSLTemplates(script, func(action *dsl.Action) {
		gated++
		action.Type = "source_review"
		action.Method = ""
		action.Target = ""
		action.BodyMultipart = nil
		action.Extractors = nil
	})
	if gated != 1 {
		t.Fatalf("gated = %d, want 1 (only the action with invalid Auth.Domain/Multipart templates)", gated)
	}
	if script.Actions[0].Type != "POST" {
		t.Fatalf("Actions[0].Type = %q, want POST preserved when Params string_map is normalized to map[string]any", script.Actions[0].Type)
	}
	if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("expected zero ValidateScriptIssues after SanitizeScriptDSLTemplates, got %v", issues)
	}
}

func TestSanitizeScriptDSLTemplatesHTTPSequenceAndBodyEncodings(t *testing.T) {
	script := &dsl.Script{
		Name:     "HTTP Sequence & Encodings",
		Version:  "1.0.0",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions: []dsl.Action{
			{
				Name:   "Base64AndJSONTemplateBody",
				Type:   "POST",
				Method: "POST",
				Target: "https://api.example.test/post",
				Body:   `{"bad":"{{run.name}}","missing":"{{file.undeclared}}"}`,
				// Both base64 and json template encoding would fail ValidateScriptIssues if left set after neutralization.
				BodyEncoding:         "base64",
				BodyTemplateEncoding: "json",
			},
			{
				Name: "SeqWithTypedActions",
				Type: "http_sequence",
				Params: map[string]any{
					"requests": []dsl.Action{
						{
							Name:         "Child1",
							Type:         "POST",
							Method:       "POST",
							Target:       "https://api.example.test/seq1?bad={{run.name}}",
							Body:         "{{run.id}}",
							BodyEncoding: "base64",
						},
					},
				},
			},
			{
				Name: "SeqWithMapRequests",
				Type: "http_sequence",
				Params: map[string]any{
					"requests": []any{
						map[string]any{
							"name":                   "ChildMap",
							"type":                   "POST",
							"method":                 "POST",
							"target":                 "https://api.example.test/seq2",
							"headers":                map[string]any{"X-Tpl": "{{file.undeclared}}"},
							"body":                   `{"run":"{{run.name}}"}`,
							"body_encoding":          "base64",
							"body_template_encoding": "json",
						},
					},
				},
			},
		},
	}

	var gatedNames []string
	SanitizeScriptDSLTemplates(script, func(action *dsl.Action) {
		gatedNames = append(gatedNames, action.Name)
		if action.Metadata == nil {
			action.Metadata = map[string]any{}
		}
		action.Metadata["execution_blocker"] = "import_review_required"
	})
	if len(gatedNames) != 3 {
		t.Fatalf("gatedNames = %v, want all 3 actions gated before neutralization", gatedNames)
	}
	if script.Actions[0].BodyEncoding != "" || script.Actions[0].BodyTemplateEncoding != "" {
		t.Fatalf("Actions[0] encodings = (%q, %q), want cleared after neutralization", script.Actions[0].BodyEncoding, script.Actions[0].BodyTemplateEncoding)
	}
	if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("ValidateScriptIssues after SanitizeScriptDSLTemplates = %v, want empty", issues)
	}
}
