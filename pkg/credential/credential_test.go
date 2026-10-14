package credential

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSensitiveNamePreservesNormalizedVocabulary(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		want bool
	}{
		{"password", true},
		{"clientSecret", true},
		{"apiToken", true},
		{"api_token", true},
		{"API_TOKEN", true},
		{"tokenURL", false},
		{"apiTokenCount", false},
		{"businessTokenLabel", false},
		{"credentials[client_secret]", true},
		{"auth.private-key", true},
		{"client%255Fsecret", true},
		{"JMSSettlementToken", true},
		{"passphrase", true},
		{"author", false},
		{"tokenizer", false},
		// A credential component followed by a property word describes the
		// credential (a recorded Tableau session_feature_flags form field held
		// feature toggles, and import asked for a secret).
		{"session_duration", false},
		{":session_feature_flags", false},
		{"token_type", false},
		{"auth.mode", false},
		{"session_id", true},
		{"user_session", true},
		{"access_token_expires_in", true},
		{"type_token", true},
		{"flags", false},
		{"session.flags.token", true},
		// A URL can carry the credential (an Azure SAS URL holds its signature).
		{"sas_url", true},
		{"session_uri", true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := SensitiveName(test.name); got != test.want {
				t.Fatalf("SensitiveName(%q) = %t, want %t", test.name, got, test.want)
			}
		})
	}
}

func TestSensitiveHeaderPreservesAuthoringHeuristic(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		want bool
	}{
		{"Authorization", true},
		{"Proxy-Authorization", true},
		{"Cookie", true},
		{"X-Api-Key", true},
		{"X-Auth", true},
		{"X-CSRF-Token", true},
		{"Client-Secret", true},
		{"X-Credential-Id", true},
		{"Subscription-Key", true},
		{"Content-Type", false},
		{"Accept", false},
		{"X-Author", false},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := SensitiveHeader(test.name); got != test.want {
				t.Fatalf("SensitiveHeader(%q) = %t, want %t", test.name, got, test.want)
			}
		})
	}
}

func TestScrubHeaderClassifiersKeepCookiePolicySeparate(t *testing.T) {
	t.Parallel()
	if !AlwaysSensitiveHeader("X-Access-Token") {
		t.Fatal("X-Access-Token must always be sensitive")
	}
	if AlwaysSensitiveHeader("Cookie") {
		t.Fatal("cookie handling must remain policy-controlled")
	}
	if !CookieHeader("SET-cookie") {
		t.Fatal("Set-Cookie must be recognized case-insensitively")
	}
}

func TestStaticFieldDoesNotConsumeDynamicCorrelationNames(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"password", "client-secret", "access_token", "refreshToken",
		"credentials[client_secret]", "auth.private-key",
		"credentials%5Bclient_secret%5D",
	} {
		if !StaticField(name) {
			t.Fatalf("StaticField(%q) = false", name)
		}
	}
	for _, name := range []string{
		"csrf", "session_id", "authorization", "username",
		"credentials[session_id]", "auth.csrf",
	} {
		if StaticField(name) {
			t.Fatalf("StaticField(%q) = true; dynamic/non-static fields must survive recorder correlation", name)
		}
	}
}

func TestSensitiveXMLNameRecognizesQualifiedSuffixes(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"auth:password", "mes:partnerAuthToken", "userPassword", "client_secret"} {
		if !SensitiveXMLName(name) {
			t.Fatalf("SensitiveXMLName(%q) = false", name)
		}
	}
	for _, name := range []string{"soap:operation", "author", "tokenizer"} {
		if SensitiveXMLName(name) {
			t.Fatalf("SensitiveXMLName(%q) = true", name)
		}
	}
}

func TestEnvKeyCompatibility(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"Authorization": "AUTHORIZATION",
		"X-Api-Key":     "X_API_KEY",
		"api_key":       "API_KEY",
		"a__b":          "A__B",
		"a-__b":         "A___B",
		"":              "SECRET",
		"--":            "SECRET",
		" a...b ":       "A_B",
	}
	for input, want := range tests {
		if got := EnvKey(input); got != want {
			t.Fatalf("EnvKey(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestLogicalNameAndReferenceCompatibility(t *testing.T) {
	t.Parallel()
	for input, want := range map[string]string{
		"api-token":  "API_TOKEN",
		"Api.Token":  "API_TOKEN",
		"ALREADY_OK": "ALREADY_OK",
		"client123":  "CLIENT123",
	} {
		got, err := NormalizeLogicalName(input)
		if err != nil {
			t.Fatalf("NormalizeLogicalName(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("NormalizeLogicalName(%q) = %q, want %q", input, got, want)
		}
		ref, err := Reference(input)
		if err != nil {
			t.Fatalf("Reference(%q): %v", input, err)
		}
		if parsed, ok := ParseReference(ref); !ok || parsed != want {
			t.Fatalf("ParseReference(%q) = %q, %t; want %q, true", ref, parsed, ok, want)
		}
	}

	for _, invalid := range []string{"", "white space", strings.Repeat("a", MaxLogicalNameBytes+1), "{{secret.X}}"} {
		if ValidLogicalName(invalid) {
			t.Fatalf("ValidLogicalName(%q) = true", invalid)
		}
	}
}

func TestRedactedSentinelIsStableJSONText(t *testing.T) {
	t.Parallel()
	payload, err := json.Marshal(map[string]string{"value": Redacted})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), `[REDACTED]`) {
		t.Fatalf("encoded sentinel = %s", payload)
	}
}

func TestDraftReferencePreservesExistingNameAndFailsClosed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		value      string
		suggestion string
		want       string
	}{
		{name: "empty", suggestion: "NEW_NAME", want: ""},
		{name: "literal", value: "source-secret", suggestion: "new-name", want: "{{secret.NEW_NAME}}"},
		{name: "existing", value: "{{ secret.Auth.Token }}", suggestion: "OTHER", want: "{{secret.AUTH_TOKEN}}"},
		{name: "invalid suggestion", value: "source-secret", suggestion: "white space", want: Redacted},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := DraftReference(test.value, test.suggestion); got != test.want {
				t.Fatalf("DraftReference(%q, %q) = %q, want %q",
					test.value, test.suggestion, got, test.want)
			}
		})
	}
}

func TestSensitiveRuntimeSetting(t *testing.T) {
	t.Parallel()
	tests := []struct {
		section string
		key     string
		want    bool
	}{
		{section: "Authentication", key: "Password", want: true},
		{section: "Proxy", key: "UserName", want: true},
		{section: "Web", key: "ProxyUser", want: true},
		{section: "General", key: "UserName", want: false},
		{section: "Proxy", key: "Server", want: false},
	}
	for _, test := range tests {
		test := test
		t.Run(test.section+"/"+test.key, func(t *testing.T) {
			t.Parallel()
			if got := SensitiveRuntimeSetting(test.section, test.key); got != test.want {
				t.Fatalf("SensitiveRuntimeSetting(%q, %q) = %t, want %t",
					test.section, test.key, got, test.want)
			}
		})
	}
}

func TestBrowserStorageStateIsCredentialMaterial(t *testing.T) {
	if !SensitiveName("storage_state") {
		t.Fatal("browser session snapshot must be treated as credentials")
	}
}

func TestMatchableNamedValue(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		want        bool
	}{
		{"cookie", "1", false},
		{"session", "en", false},
		{"token", "0:1", false},
		{"token", "abcd", true},
		{"pin", "12", true},
		{"userPin", "7", true},
		{"new_password", "x", true},
		{"passcode", "1", true},
		{"otp", "42", true},
	} {
		if got := MatchableNamedValue(tc.name, tc.value); got != tc.want {
			t.Errorf("MatchableNamedValue(%q, %q) = %t, want %t", tc.name, tc.value, got, tc.want)
		}
	}
}
