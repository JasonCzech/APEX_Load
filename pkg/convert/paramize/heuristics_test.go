package paramize

import "testing"

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
		{"Content-Type", false},
		{"Accept", false},
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
