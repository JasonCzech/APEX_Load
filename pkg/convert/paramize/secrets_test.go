package paramize

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// Bodies that do not start with '{' or '[' still reach formSecretPattern, so the
// value class has to stop at the byte that ends the credential. A greedy class
// consumes the rest of the document and the replacement drops it, destroying
// sibling content that has nothing to do with the secret.
func TestSanitizeBodySecretsKeepsContentAfterFormSecret(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "multipart part carrying a json callback url",
			body: "--b\r\nContent-Disposition: form-data; name=\"payload\"\r\n\r\n" +
				`{"redirect_url":"https://app/cb?code=1&token=xyz","safe":"kept"}` + "\r\n--b--",
			want: "--b\r\nContent-Disposition: form-data; name=\"payload\"\r\n\r\n" +
				`{"redirect_url":"https://app/cb?code=1&token={{secret.redacted}}","safe":"kept"}` + "\r\n--b--",
		},
		{
			name: "form pair followed by a semicolon separated pair",
			body: "token=abc;user=jane",
			want: "token={{secret.redacted}};user=jane",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SanitizeBodySecrets(tt.body); got != tt.want {
				t.Errorf("SanitizeBodySecrets(%q) = %q, want %q", tt.body, got, tt.want)
			}
		})
	}
}

// The XML/SOAP shape has no pair delimiter after the credential, so the sibling
// elements must survive, and the quoted credential still has to be redacted:
// stopping the value class at the quote must not turn the truncation defect into
// a silent non-redaction, because SanitizeBodySecrets is the catch-all for
// source-review redaction.
func TestSanitizeBodySecretsRedactsQuotedPairsWithoutTruncating(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "xml attribute keeps siblings",
			body: `<auth token="abc123"/><keep>yes</keep>`,
			want: `<auth token="{{secret.redacted}}"/><keep>yes</keep>`,
		},
		{
			name: "single quoted pair keeps the following pair",
			body: `api_key='k-1'&x=2`,
			want: `api_key='{{secret.redacted}}'&x=2`,
		},
		{
			name: "apostrophe inside a double quoted value keeps the tail",
			body: `<auth token="it's-me"/><keep>yes</keep>`,
			want: `<auth token="{{secret.redacted}}"/><keep>yes</keep>`,
		},
		{
			name: "governed template value is left alone",
			body: `<auth token="{{env.TOKEN}}"/><keep>yes</keep>`,
			want: `<auth token="{{env.TOKEN}}"/><keep>yes</keep>`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SanitizeBodySecrets(tt.body); got != tt.want {
				t.Errorf("SanitizeBodySecrets(%q) = %q, want %q", tt.body, got, tt.want)
			}
		})
	}
}

// The quoted-pair suggestion must rewrite the credential only, closing quote
// included, so accepting it cannot swallow the rest of the document.
func TestSecretSuggestionQuotedLiteralStopsAtClosingQuote(t *testing.T) {
	script := &dsl.Script{
		Name: "test", Version: "1.0", Protocol: "http",
		Config: dsl.ScriptConfig{BaseURL: "https://api.example.com"},
		Actions: []dsl.Action{{
			Name: "soap", Type: "http_request", Method: "POST", Target: "/svc",
			Body: `<auth token="abc123"/><keep>yes</keep>`,
		}},
	}
	suggestions, err := secretSuggestions(script)
	if err != nil {
		t.Fatalf("secretSuggestions failed: %v", err)
	}
	if len(suggestions) != 1 {
		t.Fatalf("expected 1 secret suggestion, got %d (%+v)", len(suggestions), suggestions)
	}
	location := suggestions[0].Locations[0]
	if location.SearchText != ` token="abc123"` {
		t.Errorf("SearchText = %q, want the quoted credential only", location.SearchText)
	}
	if location.ReplaceText != ` token="{{env.TOKEN}}"` {
		t.Errorf("ReplaceText = %q, want the quoted credential only", location.ReplaceText)
	}
}

// bodyLooksJSON is what keeps the form/quoted passes off a JSON document, where
// an embedded query string ("...?code=1&token=xyz") is a value byte and not a
// form pair. Without the guard the JSON string is rewritten in place, so this
// case pins both guards (SanitizeBodySecrets and collectBodySecrets).
func TestJSONBodyGuardLeavesEmbeddedQueryPairsAlone(t *testing.T) {
	bodies := []string{
		`{"redirect_url":"https://app/cb?code=1&token=xyz","safe":"kept"}`,
		`[{"redirect_url":"https://app/cb?code=1&token=xyz","safe":"kept"}]`,
		`{"callback":"https://app/cb?x=1&api_key='k-1'","safe":"kept"}`,
	}
	for _, body := range bodies {
		if got := SanitizeBodySecrets(body); got != body {
			t.Errorf("SanitizeBodySecrets(%q) = %q, want the JSON body unchanged", body, got)
		}
		script := &dsl.Script{
			Name: "test", Version: "1.0", Protocol: "http",
			Config: dsl.ScriptConfig{BaseURL: "https://api.example.com"},
			Actions: []dsl.Action{{
				Name: "callback", Type: "http_request", Method: "POST", Target: "/cb", Body: body,
			}},
		}
		suggestions, err := secretSuggestions(script)
		if err != nil {
			t.Fatalf("secretSuggestions failed: %v", err)
		}
		if len(suggestions) != 0 {
			t.Errorf("body %q produced %d suggestions, want none: %+v", body, len(suggestions), suggestions)
		}
	}
}

// The accept-a-suggestion path shares the pattern, so its SearchText must cover
// the credential only: a greedy literal rewrites (and truncates) the tail of the
// body when the reviewer accepts the suggestion.
func TestSecretSuggestionFormLiteralStopsAtCredential(t *testing.T) {
	script := &dsl.Script{
		Name: "test", Version: "1.0", Protocol: "http",
		Config: dsl.ScriptConfig{BaseURL: "https://api.example.com"},
		Actions: []dsl.Action{{
			Name: "callback", Type: "http_request", Method: "POST", Target: "/cb",
			Body: `--b` + "\r\n\r\n" + `{"redirect_url":"https://app/cb?code=1&token=xyz","safe":"kept"}` + "\r\n--b--",
		}},
	}
	suggestions, err := secretSuggestions(script)
	if err != nil {
		t.Fatalf("secretSuggestions failed: %v", err)
	}
	if len(suggestions) != 1 {
		t.Fatalf("expected 1 secret suggestion, got %d (%+v)", len(suggestions), suggestions)
	}
	location := suggestions[0].Locations[0]
	if location.SearchText != "&token=xyz" {
		t.Errorf("SearchText = %q, want the credential pair only", location.SearchText)
	}
	if location.ReplaceText != "&token={{env.TOKEN}}" {
		t.Errorf("ReplaceText = %q, want the credential pair only", location.ReplaceText)
	}
}
