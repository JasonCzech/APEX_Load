package importreview

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestValidatePreservedImportCredentialEditsAllowsReferencedParameterAndProducerEdits(t *testing.T) {
	defaultOrders := "/orders"
	defaultV2Orders := "/v2/orders"
	prior := &dsl.Script{
		Name: "Referenced parameters", Version: "1", Protocol: "http",
		Config: dsl.ScriptConfig{BaseURL: "https://origin.example.test"},
		Parameters: []dsl.Parameter{
			{Name: "host", Type: "list", Update: "once", Values: []string{"origin.example.test"}},
		},
		Actions: []dsl.Action{
			{
				Name: "login", Type: "GET", Method: "GET", Target: "https://origin.example.test/login",
				Extractors: []dsl.Extractor{{Name: "endpoint", Type: "jsonpath", Expression: "$.url", DefaultValue: &defaultOrders}},
			},
			{
				Name: "orders", Type: "GET", Method: "GET",
				Target:  "https://{{param.host}}{{var.endpoint}}",
				Headers: map[string]string{"Authorization": "Bearer synthetic-referenced-token"},
			},
		},
	}
	MarkCredentialPreservingImportScript(prior)

	candidate := CloneScriptDefinition(prior)
	candidate.Parameters[0].Values = []string{"staging.example.test"}
	candidate.Actions[0].Extractors[0].DefaultValue = &defaultV2Orders

	if err := ValidatePreservedImportCredentialEdits(candidate, prior); err != nil {
		t.Fatalf("ValidatePreservedImportCredentialEdits returned unexpected error: %v", err)
	}
	saved, err := PreparePersistentLibraryScript(candidate, prior)
	if err != nil {
		t.Fatalf("PreparePersistentLibraryScript returned unexpected error: %v", err)
	}
	if got := saved.Parameters[0].Values[0]; got != "staging.example.test" {
		t.Fatalf("saved parameter = %q, want staging.example.test", got)
	}
	if got := saved.Actions[1].Headers["Authorization"]; got != "Bearer synthetic-referenced-token" {
		t.Fatalf("saved Authorization = %q, want literal credential preserved", got)
	}
}
