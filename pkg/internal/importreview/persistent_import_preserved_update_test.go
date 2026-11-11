package importreview

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/aurapayload"
)

const sourceReviewKeptCredential = "Bearer source-review-synthetic-kept-token"

const sourceReviewActionSource = "Action()\n{\n" +
	"\tweb_add_header(\"Authorization\", \"" + sourceReviewKeptCredential + "\");\n" +
	"\tweb_url(\"orders\", \"URL=https://origin.example.test/orders\", \"Resource=0\", LAST);\n" +
	"\tweb_url(\"cart\", \"URL=https://origin.example.test/cart\", \"Resource=0\", LAST);\n" +
	"\treturn 0;\n}\n"

func sourceReviewCredentialScript() *dsl.Script {
	script := &dsl.Script{
		Name: "Source review credential", Version: "1", Protocol: "http",
		Config: dsl.ScriptConfig{
			BaseURL: "https://origin.example.test",
			HTTP:    dsl.HTTPConfig{ProxyMode: "system"},
		},
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "enterprise_script_archive",
			Preserved: map[string]any{
				"source_review": map[string]any{
					"source_files": []any{map[string]any{"path": "Action.c", "content": sourceReviewActionSource}},
					"mappings":     []any{map[string]any{"source_file": "Action.c", "source_line": 4, "action_path": "actions[0]"}},
				},
			},
		}},
		Actions: []dsl.Action{
			{Name: "orders", Type: "GET", Method: "GET", Target: "https://origin.example.test/orders", Headers: map[string]string{"Authorization": sourceReviewKeptCredential}},
			{Name: "cart", Type: "GET", Method: "GET", Target: "https://origin.example.test/cart"},
		},
	}
	MarkCredentialPreservingImportScript(script)
	return script
}

func sourceReviewFiles(script *dsl.Script) []any {
	review, _ := script.Metadata.Import.Preserved["source_review"].(map[string]any)
	files, _ := review["source_files"].([]any)
	return files
}

func TestMaskScriptSecretsStillRedactsRuntimeSnapshotsWhileAuthoringEditsSucceed(t *testing.T) {
	prior := sourceReviewCredentialScript()
	masked := MaskScriptSecrets(prior)
	if masked == nil {
		t.Fatal("MaskScriptSecrets failed")
	}
	disclosed, _ := sourceReviewFiles(masked)[0].(map[string]any)["content"].(string)
	if disclosed == sourceReviewActionSource || strings.Contains(disclosed, "source-review-synthetic-kept-token") {
		t.Fatalf("disclosed source text = %q, want the quoted credential masked in runtime snapshot projection", disclosed)
	}

	for name, edit := range map[string]func(*dsl.Script){
		"proxy mode system to none": func(script *dsl.Script) {
			script.Config.HTTP.ProxyMode = "none"
		},
		"custom proxy and TLS": func(script *dsl.Script) {
			script.Config.HTTP.ProxyMode = "custom"
			script.Config.HTTP.Proxy = dsl.ProxyConfig{URL: "http://proxy.example.test:8080", Username: "u", Password: "synthetic-proxy-password"}
			script.Config.HTTP.TLS.InsecureSkipVerify = true
		},
		"base URL and action retarget": func(script *dsl.Script) {
			script.Config.BaseURL = "https://staging.example.test"
			script.Actions[0].Target = "https://staging.example.test/orders"
		},
		"new literal credential": func(script *dsl.Script) {
			script.Actions[1].Headers = map[string]string{"Authorization": "Bearer synthetic-new-operator-token"}
		},
		"source_mapping_review attachment": func(script *dsl.Script) {
			script.Metadata.Import.Preserved["source_mapping_review"] = map[string]any{
				"decisions": map[string]any{"actions[0]": "accepted"},
				"snapshot":  sourceReviewActionSource,
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := CloneScriptDefinition(prior)
			edit(candidate)
			if err := ValidatePreservedImportCredentialEdits(candidate, prior); err != nil {
				t.Fatalf("ValidatePreservedImportCredentialEdits failed: %v", err)
			}
			if reason, changed := RetainedImportCredentialContextChange(candidate.Config, prior.Config, candidate.Actions, prior.Actions); changed || reason != "" {
				t.Fatalf("RetainedImportCredentialContextChange = (%q, %v), want (\"\", false)", reason, changed)
			}
			saved, err := PreparePersistentLibraryScript(candidate, prior)
			if err != nil {
				t.Fatalf("PreparePersistentLibraryScript failed: %v", err)
			}
			if got := saved.Actions[0].Headers["Authorization"]; got != sourceReviewKeptCredential {
				t.Fatalf("saved Authorization = %q, want literal credential preserved", got)
			}
			if MaskScriptSecrets(saved) == nil {
				t.Fatal("MaskScriptSecrets failed on saved script")
			}
		})
	}
}

func TestMaskScriptSecretsKeepsExecutableCredentialContextRedacted(t *testing.T) {
	for name, fixture := range map[string]func(*dsl.Script){
		"script-wide auth": func(script *dsl.Script) {
			script.Config.HTTP.Auth = dsl.AuthConfig{Type: "bearer", Token: "source-review-synthetic-script-token"}
		},
		"recorded resource URL": func(script *dsl.Script) {
			script.Metadata.Import.Preserved["recorded_resource_urls_available"] = true
			script.Metadata.Import.Preserved["recorded_resource_urls"] = []any{"https://user:source-review-synthetic-url-secret@origin.example.test/app.js"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			prior := sourceReviewCredentialScript()
			fixture(prior)
			disclosed, err := json.Marshal(MaskScriptSecrets(prior))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(disclosed), "synthetic-script-token") || strings.Contains(string(disclosed), "synthetic-url-secret") {
				t.Fatal("fixture credential is not masked on runtime snapshot disclosure")
			}
			candidate := CloneScriptDefinition(prior)
			candidate.Parameters = append(candidate.Parameters, dsl.Parameter{Name: "region", Type: "list", Values: []string{"west"}, Update: "once"})
			if err := ValidatePreservedImportCredentialEdits(candidate, prior); err != nil {
				t.Fatalf("adding parameter failed: %v", err)
			}
		})
	}
}

func TestTrustedParameterizationPreservesSecretsAndAuraValues(t *testing.T) {
	script := &dsl.Script{
		Name: "Paramize unredacted", Version: "1.0.0", Protocol: "http",
		Actions: []dsl.Action{{
			Name:   "POST login",
			Type:   "request",
			Method: http.MethodPost,
			Target: "https://api.example.test/login",
			Headers: map[string]string{
				"Authorization": "Bearer synthetic-param-bearer-token-998877",
				"Content-Type":  "application/json",
			},
			Body: `{"password":"synthetic-param-password-112233"}`,
		}},
	}
	res := BuildTrustedImportParameterization(script)
	if res == nil {
		t.Fatal("BuildTrustedImportParameterization returned nil")
	}
	hasSecretKind := false
	for _, s := range res.Suggestions {
		if s.Kind == paramize.KindSecret {
			hasSecretKind = true
			break
		}
	}
	if !hasSecretKind {
		t.Fatalf("expected BuildTrustedImportParameterization to preserve KindSecret suggestions, got %#v", res.Suggestions)
	}

	auraFiltered := FilterTrustedImportParameterization(&paramize.Result{
		Suggestions: []paramize.Suggestion{{
			ID:           "aura-1",
			Kind:         paramize.KindDataParam,
			LiteralValue: "001xx000003DGbYAAW",
			SampleValue:  "001xx000003DGbYAAW",
			Locations: []paramize.Location{{
				ActionIndex: 0,
				Field:       "body",
				SearchText:  "001xx000003DGbYAAW",
				AuraRoute: []aurapayload.Segment{
					{Kind: aurapayload.KeySegment, Key: "actions"},
					{Kind: aurapayload.IndexSegment, Index: 0},
					{Kind: aurapayload.KeySegment, Key: "params"},
					{Kind: aurapayload.KeySegment, Key: "recordId"},
				},
			}},
		}},
	})
	if auraFiltered == nil || len(auraFiltered.Suggestions) != 1 {
		t.Fatalf("FilterTrustedImportParameterization = %#v, want 1 suggestion", auraFiltered)
	}
	got := auraFiltered.Suggestions[0]
	if got.LiteralValue != "" || got.SampleValue != "" || got.Locations[0].SearchText != "" || got.Parameter != nil || len(got.Locations[0].AuraRoute) != 4 {
		t.Fatalf("FilterTrustedImportParameterization unexpected Aura route projection: %#v", got)
	}
}

func TestAttachLLMAndAgentforceReviewPreserveLiteralCredentials(t *testing.T) {
	const literalKey = "sk-synthetic-llm-key-0123456789abcdef"
	llmScript := &dsl.Script{
		Name: "LLM import", Version: "1.0.0", Protocol: "http",
		Config: dsl.ScriptConfig{BaseURL: "https://api.openai.com"},
		Actions: []dsl.Action{{
			Name:   "Chat completion",
			Type:   "request",
			Method: http.MethodPost,
			Target: "https://api.openai.com/v1/chat/completions",
			Headers: map[string]string{
				"Authorization": "Bearer " + literalKey,
				"Content-Type":  "application/json",
			},
			HeaderList: []dsl.HeaderEntry{
				{Name: "Authorization", Value: "Bearer " + literalKey},
				{Name: "Content-Type", Value: "application/json"},
			},
			Body: `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hello"}]}`,
		}},
	}
	AttachLLMAPIReview(llmScript, "har", nil, true)
	if got := llmScript.Actions[0].Headers["Authorization"]; got != "Bearer "+literalKey {
		t.Fatalf("AttachLLMAPIReview rewrote Authorization = %q, want literal preserved", got)
	}
	if got := llmScript.Actions[0].HeaderList[0].Value; strings.Contains(got, "{{secret.") {
		t.Fatalf("AttachLLMAPIReview rewrote HeaderList Authorization to secret template: %q", got)
	}

	const agentforceBearer = "synthetic-agentforce-session-token-abcdef123456"
	afScript := &dsl.Script{
		Name: "Agentforce import", Version: "1.0.0", Protocol: "http",
		Config: dsl.ScriptConfig{BaseURL: "https://api.salesforce.com"},
		Actions: []dsl.Action{{
			Name:   "Start session",
			Type:   "request",
			Method: http.MethodPost,
			Target: "https://api.salesforce.com/einstein/ai-agent/v1/agents/0Xx000000000001/sessions",
			Headers: map[string]string{
				"Authorization": "Bearer " + agentforceBearer,
				"Content-Type":  "application/json",
			},
			Body: `{"externalSessionKey":"ext-1"}`,
		}},
	}
	AttachAgentforceReview(afScript, "har", nil, false)
	if len(afScript.Init) != 0 || afScript.Actions[0].Headers["Authorization"] != "Bearer "+agentforceBearer {
		t.Fatalf("AttachAgentforceReview(transform=false) mutated literal auth: init=%#v auth=%q", afScript.Init, afScript.Actions[0].Headers["Authorization"])
	}
	AttachAgentforceReview(afScript, "har", nil, true)
	if len(afScript.Init) != 1 || afScript.Init[0].Params["client_secret"] != "{{secret.SALESFORCE_CLIENT_SECRET}}" {
		t.Fatalf("AttachAgentforceReview(transform=true) did not prepend OAuth step with {{secret.SALESFORCE_CLIENT_SECRET}}: %#v", afScript.Init)
	}
}
