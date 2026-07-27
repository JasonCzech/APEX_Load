package requestimport

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestFinalizeScriptDoesNotReplaceRequirementValueInUnrelatedURL(t *testing.T) {
	t.Parallel()

	registry := credentialpolicy.NewNameRegistry()
	firstAction, firstReport, err := FinalizeAction(
		EffectiveRequest{
			Key:    "credential request",
			Name:   "credential request",
			Method: "GET",
			Target: "/login?api_key=cat",
			Source: SourceRef{Format: "har", Pointer: "/entries/0"},
		},
		FinalizeOptions{
			ActionPath: "actions[0]",
			Credentials: &CredentialProjectionOptions{
				Mode:       credentialpolicy.SecretReference,
				Registry:   registry,
				NamePrefix: "HAR",
			},
		},
	)
	if err != nil {
		t.Fatalf("FinalizeAction: %v", err)
	}
	if len(firstReport.Requirements) != 1 || firstReport.Requirements[0].Value != "cat" {
		t.Fatalf("credential requirements = %#v", firstReport.Requirements)
	}

	script := &dsl.Script{
		Name:          "requirement scope",
		Version:       "1.0.0",
		Protocol:      "http",
		GeneratedFrom: "har",
		Config:        dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{
			firstAction,
			{
				Name:   "unrelated request",
				Type:   "GET",
				Method: "GET",
				Target: "/cat/catalog?view=cat",
			},
		},
	}

	result, err := FinalizeScript(script, []Report{firstReport}, FinalizeOptions{})
	if err != nil {
		t.Fatalf("FinalizeScript: %v", err)
	}
	if got := result.Script.Actions[1].Target; got != "/cat/catalog?view=cat" {
		t.Fatalf("unrelated target = %q, want byte-identical nonsensitive URL", got)
	}
}

func TestProjectURLFromBaseRequiresWholeURLBoundary(t *testing.T) {
	t.Parallel()

	const (
		authored  = "https://user:password@example.test/api"
		projected = "https://{{secret.BASE_USERINFO}}@example.test/api"
	)
	tests := []struct {
		name string
		raw  string
		want string
		ok   bool
	}{
		{
			name: "same URL",
			raw:  authored,
			want: projected,
			ok:   true,
		},
		{
			name: "child path",
			raw:  authored + "/orders",
			want: projected + "/orders",
			ok:   true,
		},
		{
			name: "query suffix",
			raw:  authored + "?page=1",
			want: projected + "?page=1",
			ok:   true,
		},
		{
			name: "textual prefix is not a path boundary",
			raw:  authored + "cal/orders",
		},
		{
			name: "different URL",
			raw:  "https://example.test/catalog",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := projectURLFromBase(test.raw, authored, projected)
			if got != test.want || ok != test.ok {
				t.Fatalf("projectURLFromBase() = %q, %t; want %q, %t", got, ok, test.want, test.ok)
			}
		})
	}
}
