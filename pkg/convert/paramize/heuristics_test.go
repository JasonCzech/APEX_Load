package paramize

import (
	"strings"
	"testing"
)

// Cases ported from the pkg/api browser-to-HTTP conversion tests to lock
// behavior after lifting the heuristics into this shared package.
func TestLooksDynamicValue(t *testing.T) {
	tests := []struct {
		key   string
		value string
		want  bool
	}{
		{"csrf_token", "abc", true},
		{"session", "x", true},
		{"nonce", "1", true},
		{"page", "2", false},
		{"q", "averyveryverylongopaquevalue1234", true},
		{"q", "a-b-c-d-e", true},
		{"q", "short", false},
	}
	for _, tt := range tests {
		if got := LooksDynamicValue(tt.key, tt.value); got != tt.want {
			t.Errorf("LooksDynamicValue(%q, %q) = %v, want %v", tt.key, tt.value, got, tt.want)
		}
	}
}

func TestLooksParameterName(t *testing.T) {
	tests := []struct {
		key  string
		want bool
	}{
		{"user_id", true},
		{"id", true},
		{"customer", true},
		{"account_number", true},
		{"order_sku", true},
		{"email", true},
		{"zip", true},
		{"page", false},
		{"limit", false},
		{"format", false},
	}
	for _, tt := range tests {
		if got := LooksParameterName(tt.key); got != tt.want {
			t.Errorf("LooksParameterName(%q) = %v, want %v", tt.key, got, tt.want)
		}
	}
}

func TestSensitiveHeaderKey(t *testing.T) {
	tests := []struct {
		key  string
		want bool
	}{
		{"Authorization", true},
		{"authorization", true},
		{"Proxy-Authorization", true},
		{"Cookie", true},
		{"X-Api-Key", true},
		{"api-key", true},
		{"X-Auth", true},
		{"X-CSRF-Token", true},
		{"Client-Secret", true},
		{"X-Credential-Id", true},
		{"Subscription-Key", true},
		{"Content-Type", false},
		{"Accept", false},
		{"X-Author", false},
	}
	for _, tt := range tests {
		if got := SensitiveHeaderKey(tt.key); got != tt.want {
			t.Errorf("SensitiveHeaderKey(%q) = %v, want %v", tt.key, got, tt.want)
		}
	}
}

func TestEnvKey(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"Authorization", "AUTHORIZATION"},
		{"X-Api-Key", "X_API_KEY"},
		{"api_key", "API_KEY"},
		{"password", "PASSWORD"},
		{"", "SECRET"},
		{"--", "SECRET"},
	}
	for _, tt := range tests {
		if got := EnvKey(tt.name); got != tt.want {
			t.Errorf("EnvKey(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestSelectorFieldHint(t *testing.T) {
	tests := []struct {
		selector string
		want     string
	}{
		{"#user_email", "user_email"},
		{`input[name="user_id"]`, "user_id"},
		{"#login_password", "login_password"},
		{".form-control", "form-control"},
		{"input", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := SelectorFieldHint(tt.selector); got != tt.want {
			t.Errorf("SelectorFieldHint(%q) = %q, want %q", tt.selector, got, tt.want)
		}
	}
}

func TestSelectorFieldHintFailsClosedOnMatchOverflow(t *testing.T) {
	selector := strings.Repeat("field ", maxSuggestionRegexMatches+1)
	if got := SelectorFieldHint(selector); got != "" {
		t.Fatalf("SelectorFieldHint overflow = %q, want empty", got)
	}
}

func TestSelectorFieldHintFailsClosedOnInputByteOverflow(t *testing.T) {
	selector := strings.Repeat("!", int(maxSuggestionRegexSearchBytes)+1)
	if got := SelectorFieldHint(selector); got != "" {
		t.Fatalf("SelectorFieldHint oversized sparse input = %q, want empty", got)
	}
}

// Cases ported from the sample importer's sanitize tests.
func TestSanitizeBodySecrets(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "json password redacted",
			body: `{"password": "hunter2"}`,
			want: `{"password": "{{secret.redacted}}"}`,
		},
		{
			name: "form token redacted",
			body: "token=abc&x=1",
			want: "token={{secret.redacted}}&x=1",
		},
		{
			name: "json client secret redacted",
			body: `{"client_secret": "top-secret"}`,
			want: `{"client_secret": "{{secret.redacted}}"}`,
		},
		{
			name: "json escaped quote secret redacted atomically",
			body: `{"password":"abc\"residual-secret","safe":"ok"}`,
			want: `{"password":"{{secret.redacted}}","safe":"ok"}`,
		},
		{
			name: "json secret does not consume trailing object as form data",
			body: `{"password":"hunter2","profile":{"enabled":true},"safe":"kept"}`,
			want: `{"password":"{{secret.redacted}}","profile":{"enabled":true},"safe":"kept"}`,
		},
		{
			name: "json numeric passcode redacted",
			body: `{"passcode":123456,"safe":1}`,
			want: `{"passcode":"{{secret.redacted}}","safe":1}`,
		},
		{
			name: "json hyphen api key redacted",
			body: `{"api-key": "k-123"}`,
			want: `{"api-key": "{{secret.redacted}}"}`,
		},
		{
			name: "form bearer token redacted",
			body: "bearer_token=abc&x=1",
			want: "bearer_token={{secret.redacted}}&x=1",
		},
		{
			name: "already templated untouched",
			body: `{"password": "{{var.pw}}"}`,
			want: `{"password": "{{var.pw}}"}`,
		},
		{
			name: "empty body untouched",
			body: "",
			want: "",
		},
		{
			name: "non-secret fields untouched",
			body: `{"username": "jane"}`,
			want: `{"username": "jane"}`,
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

func TestOAuthApplicationIdentifiersAreNotPerUserData(t *testing.T) {
	for _, key := range []string{"client_id", "client-id", "clientId", "CLIENT_ID", "oauth_client_id", "oauth2_client_id"} {
		if LooksParameterName(key) {
			t.Errorf("%q must not be varied per VUser", key)
		}
	}
	if !LooksParameterName("customer_id") {
		t.Fatal("business identifiers should remain candidates")
	}
}
