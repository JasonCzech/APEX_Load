package api

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// The import suggests readable env names (API_TOKEN becomes API_TKN); a value
// an operator supplies for one is still protected as a credential.
func TestSensitiveEnvKeyCoversReadableNames(t *testing.T) {
	for _, name := range []string{"API_TKN", "ACCESS_TKN", "CLIENT_SCRT", "PRIVKEY", "API_PW"} {
		if !sensitiveEnvKey(name) {
			t.Errorf("sensitiveEnvKey(%q) = false, want it protected", name)
		}
	}
}

// Repairing a stored row keeps the rule it was written under: a literal whose
// key reads as a credential only by the readable-name rule (CRED_ID) is kept,
// while a new save protects it.
func TestLegacyRuntimeOverrideRepairKeepsReadableNameLiterals(t *testing.T) {
	overrides := &RuntimeSettingsOverride{Env: map[string]string{"CRED_ID": "abc-123", "API_TOKEN": "literal-token-value"}}
	protected, _, _, err := protectStoredRuntimeOverrideSecrets("test", 0, &dsl.Script{}, overrides)
	if err != nil {
		t.Fatal(err)
	}
	if protected.Env["CRED_ID"] != "abc-123" {
		t.Fatalf("legacy repair replaced CRED_ID with %q, want the stored literal kept", protected.Env["CRED_ID"])
	}
	if !runtimeOverrideExactSecretPattern.MatchString(protected.Env["API_TOKEN"]) {
		t.Fatalf("legacy repair left API_TOKEN as %q, want it protected as before", protected.Env["API_TOKEN"])
	}
	fresh, _, err := protectRuntimeOverrideSecrets("test", 0, &dsl.Script{}, &RuntimeSettingsOverride{Env: map[string]string{"CRED_ID": "abc-123"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !runtimeOverrideExactSecretPattern.MatchString(fresh.Env["CRED_ID"]) {
		t.Fatalf("new save left CRED_ID as %q, want it protected", fresh.Env["CRED_ID"])
	}
}

// A scenario save (which tolerates a stored [REDACTED] sentinel) still
// classifies keys by the current rule: a readable credential name an import
// wrote is protected, not stored as a literal.
func TestScenarioSaveProtectsReadableCredentialEnvNames(t *testing.T) {
	protected, values, _, err := protectLegacyRuntimeOverrideSecrets("scenario", -1, nil, &RuntimeSettingsOverride{Env: map[string]string{"API_TKN": "eyJ-token-value"}})
	if err != nil {
		t.Fatal(err)
	}
	if !runtimeOverrideExactSecretPattern.MatchString(protected.Env["API_TKN"]) || len(values) != 1 {
		t.Fatalf("API_TKN stored as %q (values %d), want a secret reference", protected.Env["API_TKN"], len(values))
	}
}

// The stored-content projection keeps its form for a literal a stored row
// keeps, so library hashes do not change on upgrade; the editor masks it.
func TestStoredRuntimeProjectionKeepsLegacyLiterals(t *testing.T) {
	stored := &RuntimeSettingsOverride{Env: map[string]string{"CRED_ID": "abc-123", "API_TOKEN": "literal-token-value"}}
	if got := maskRuntimeSettings(stored).Env; got["CRED_ID"] != "abc-123" || got["API_TOKEN"] == "literal-token-value" {
		t.Fatalf("stored projection = %v", got)
	}
	if got := maskRuntimeSettingsForEditor(stored).Env; got["CRED_ID"] == "abc-123" {
		t.Fatalf("editor projection = %v, want CRED_ID masked", got)
	}
}
