package engine

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestResolveActionEncodesURLFormTemplateValuesAfterInterpolation(t *testing.T) {
	vu := &VUser{Variables: map[string]string{
		"field": "dynamic key",
		"value": "A&B + café",
		"empty": "",
	}}
	action := dsl.Action{
		Name: "form",
		Type: "POST",
		Body: "full+name={{var.value}}&{{var.field}}=static+value&empty={{var.empty}}",
		Metadata: map[string]any{
			dsl.ImportURLFormTemplateValueEncodingMetadataKey: dsl.ImportURLFormTemplateValueEncodingQueryComponent,
		},
	}

	resolved := vu.resolveAction(action)
	want := "full+name=A%26B+%2B+caf%C3%A9&dynamic+key=static+value&empty="
	if got := string(resolved.Body); got != want {
		t.Fatalf("resolved body = %q, want %q", got, want)
	}
	if got := string(resolved.RedactedBody); got != want {
		t.Fatalf("redacted body = %q, want %q", got, want)
	}
}

func TestResolveActionLeavesUnresolvedURLFormParameterVisibleToExecutionGate(t *testing.T) {
	vu := &VUser{}
	action := dsl.Action{
		Name: "form",
		Type: "POST",
		Body: "token={{param.missing}}",
		Metadata: map[string]any{
			dsl.ImportURLFormTemplateValueEncodingMetadataKey: dsl.ImportURLFormTemplateValueEncodingQueryComponent,
		},
	}

	resolved := vu.resolveAction(action)
	if got := string(resolved.Body); got != action.Body {
		t.Fatalf("unresolved body = %q, want intact template %q", got, action.Body)
	}
	if !actionContainsUnresolvedParameter(resolved) {
		t.Fatal("unresolved form parameter was hidden from the execution gate")
	}
}

func TestResolveActionDoesNotEncodeOrdinaryBodyTemplates(t *testing.T) {
	vu := &VUser{Variables: map[string]string{"value": "A&B"}}
	resolved := vu.resolveAction(dsl.Action{
		Name: "raw",
		Type: "POST",
		Body: `{"value":"{{var.value}}"}`,
	})
	if got, want := string(resolved.Body), `{"value":"A&B"}`; got != want {
		t.Fatalf("ordinary body = %q, want %q", got, want)
	}
}
