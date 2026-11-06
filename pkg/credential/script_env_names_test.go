package credential

import "testing"

func TestScriptEnvKeyIsAlwaysReadable(t *testing.T) {
	tests := map[string]string{
		"Authorization": "AUTHORIZATION",
		"password":      "PW",
		"user_passwd":   "USER_PW",
		"client_secret": "CLIENT_SCRT",
		"X-Api-Key":     "X_APIKEY",
		"private_key":   "PRIVKEY",
		"credential_id": "CRED_ID",
		"access_token":  "ACCESS_TKN",
		"aws_key":       "REC_AWS_KEY",
		"region":        "REGION",
		"":              "VALUE",
	}
	for name, want := range tests {
		got := ScriptEnvKey(name)
		if got != want {
			t.Errorf("ScriptEnvKey(%q) = %q, want %q", name, got, want)
		}
		if ScriptEnvNameAlwaysBlocked(got) {
			t.Errorf("ScriptEnvKey(%q) = %q, which scripts can never read", name, got)
		}
	}
	// EnvKey still names project secrets, which have no such limit.
	if got := EnvKey("password"); got != "PASSWORD" {
		t.Fatalf("EnvKey(password) = %q, want it unchanged", got)
	}
}

// A name the import made readable still names a credential, so its value is
// handled as one (runtime-override env values are moved into secrets, masks).
func TestReadableScriptEnvNamesStaySensitive(t *testing.T) {
	for _, name := range []string{"API_TKN", "ACCESS_TKN", "CLIENT_SCRT", "PRIVKEY", "CRED", "CRED_ID", "API_PW", "PW", "X_APIKEY"} {
		if !SensitiveName(name) && !ReadableCredentialEnvName(name) {
			t.Errorf("%q is not recognised as a credential name", name)
		}
	}
}

// The shortened words are recognised only as whole env-name components, not
// inside ordinary field names an import scrubs.
func TestReadableCredentialEnvNameIsNarrow(t *testing.T) {
	for _, name := range []string{"c_red", "credit", "CREDIT_LIMIT", "incredible", "s_crt", "region", "PWR"} {
		if ReadableCredentialEnvName(name) {
			t.Errorf("ReadableCredentialEnvName(%q) = true, want false", name)
		}
	}
	for _, name := range []string{"c_red", "credit", "cred_score"} {
		if SensitiveName(name) {
			t.Errorf("SensitiveName(%q) = true; the shortened words must not widen it", name)
		}
	}
}

// Names written together (accessToken, PASSWORD2) shorten into a component of
// their own, so the env-name protection still knows them.
func TestScriptEnvKeyKeepsRunTogetherCredentialNamesProtected(t *testing.T) {
	tests := map[string]string{
		"accessToken":                "ACCESS_TKN",
		"clientSecret":               "CLIENT_SCRT",
		"userPassword":               "USER_PW",
		"PASSWORD2":                  "PW_2",
		"SECRETKEY":                  "SCRT_KEY",
		"credentials":                "CRED_S",
		"__RequestVerificationToken": "REQUESTVERIFICATION_TKN",
	}
	for name, want := range tests {
		got := ScriptEnvKey(name)
		if got != want {
			t.Errorf("ScriptEnvKey(%q) = %q, want %q", name, got, want)
		}
		if ScriptEnvNameAlwaysBlocked(got) || !SensitiveEnvName(got) || !SensitiveEnvName(got+"_2") {
			t.Errorf("ScriptEnvKey(%q) = %q: want readable and protected (with and without a suffix)", name, got)
		}
	}
}

// Doubled separators collapse before the rewrite, so joining them cannot
// rebuild a blocked word (API__KEY into API_KEY).
func TestReadableScriptEnvNameNeverRebuildsABlockedName(t *testing.T) {
	for _, name := range []string{"API__KEY_TOKEN", "PRIVATE__KEY_SECRET", "X_API__KEY_PASSWORD", "api_ key token"} {
		got := ScriptEnvKey(name)
		if ScriptEnvNameAlwaysBlocked(got) {
			t.Errorf("ScriptEnvKey(%q) = %q, still blocked", name, got)
		}
		if got := ReadableScriptEnvName(EnvKey(name)); ScriptEnvNameAlwaysBlocked(got) {
			t.Errorf("ReadableScriptEnvName(EnvKey(%q)) = %q, still blocked", name, got)
		}
	}
}

// A credential word followed by a property word names a property of the
// credential, as SensitiveName already treats it.
func TestReadableCredentialEnvNameSkipsPropertyNames(t *testing.T) {
	for _, name := range []string{"PW_MIN_LENGTH", "CRED_TYPE", "TKN_TTL", "API_TKN_EXPIRES"} {
		if ReadableCredentialEnvName(name) || SensitiveEnvName(name) {
			t.Errorf("%s treated as a credential", name)
		}
	}
	for _, name := range []string{"PW", "API_TKN", "CLIENT_SCRT", "LOGIN_PW", "PW_2"} {
		if !ReadableCredentialEnvName(name) {
			t.Errorf("%s not treated as a credential", name)
		}
	}
}
