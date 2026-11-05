package paramize

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// api_key and apikey both suggest APIKEY: two different credentials must not
// share one variable, or one request would send the other's key.
func TestSecretSuggestionsKeepDistinctCredentialsApart(t *testing.T) {
	script := &dsl.Script{Name: "two keys", Protocol: "http", Actions: []dsl.Action{{
		Name: "call", Type: "http_request", Method: "POST", Target: "/x",
		Body: `{"api_key": "alpha1-aaaa-1111", "apikey": "beta2-bbbb-2222"}`,
	}, {
		// The second credential again: its later occurrence must use the
		// same suffixed variable.
		Name: "again", Type: "http_request", Method: "POST", Target: "/y",
		Body: `{"apikey": "beta2-bbbb-2222"}`,
	}}}
	suggestions, err := secretSuggestions(script)
	if err != nil {
		t.Fatal(err)
	}
	byValue := map[string]Suggestion{}
	for _, suggestion := range suggestions {
		byValue[suggestion.LiteralValue] = suggestion
	}
	alpha, beta := byValue["alpha1-aaaa-1111"], byValue["beta2-bbbb-2222"]
	if len(beta.Locations) < 2 {
		t.Fatalf("beta locations = %d, want both occurrences grouped", len(beta.Locations))
	}
	if alpha.VarName == "" || beta.VarName == "" || alpha.VarName == beta.VarName {
		t.Fatalf("var names %q and %q, want two distinct variables", alpha.VarName, beta.VarName)
	}
	for _, suggestion := range []Suggestion{alpha, beta} {
		want := "{{env." + suggestion.VarName + "}}"
		if suggestion.Replacement != want {
			t.Errorf("replacement %q, want %q", suggestion.Replacement, want)
		}
		for _, location := range suggestion.Locations {
			if location.ReplaceText != "" && !strings.Contains(location.ReplaceText, want) {
				t.Errorf("location replaces with %q, want it to use %s", location.ReplaceText, want)
			}
		}
	}
}

// One credential recorded under two spellings of its header keeps one
// variable.
func TestSecretSuggestionsShareOneVariableForOneCredential(t *testing.T) {
	script := &dsl.Script{Name: "mixed case", Protocol: "http", Actions: []dsl.Action{
		{Name: "a", Type: "http_request", Method: "GET", Target: "/a", Headers: map[string]string{"Authorization": "Bearer samesame12345"}},
		{Name: "b", Type: "http_request", Method: "GET", Target: "/b", Headers: map[string]string{"authorization": "Bearer samesame12345"}},
	}}
	suggestions, err := secretSuggestions(script)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, suggestion := range suggestions {
		names[suggestion.VarName] = true
	}
	if len(names) != 1 {
		t.Fatalf("variables = %v, want one for the one token", names)
	}
}

// A variable the script already reads is never suggested for another value.
func TestSecretSuggestionsAvoidVariablesTheScriptReads(t *testing.T) {
	script := &dsl.Script{Name: "half done", Protocol: "http", Actions: []dsl.Action{{
		Name: "call", Type: "http_request", Method: "POST", Target: "/x",
		Body: `{"api_key": "{{env.APIKEY}}", "apikey": "beta2-bbbb-2222"}`,
	}}}
	suggestions, err := secretSuggestions(script)
	if err != nil {
		t.Fatal(err)
	}
	for _, suggestion := range suggestions {
		if suggestion.VarName == "APIKEY" {
			t.Fatalf("suggested APIKEY, which the script already reads for another value")
		}
	}
}
