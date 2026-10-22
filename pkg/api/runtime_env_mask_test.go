package api

import (
	"reflect"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/scrub"
)

func TestMaskURLUserinfoDoesNotClassifyBareAtSignAsCredential(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "email", raw: "alice@example.com", want: "alice@example.com"},
		{name: "mailto URI", raw: "mailto:alice@example.com", want: "mailto:alice@example.com"},
		{name: "url path", raw: "https://api.example.test/users/@me", want: "https://api.example.test/users/@me"},
		{name: "credential shape in path", raw: "/users/alice:password@example.com", want: "/users/alice:password@example.com"},
		{name: "missing host", raw: "alice:password@/path", want: "alice:password@/path"},
		{name: "scheme-less userinfo", raw: "alice:password@example.com", want: scrub.Redacted},
		{name: "parsed userinfo", raw: "https://alice:password@example.com/path", want: "https://%5BREDACTED%5D@example.com/path"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := maskURLUserinfo(tc.raw); got != tc.want {
				t.Fatalf("maskURLUserinfo(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
	if sensitiveEnvValue("ops@example.com") {
		t.Fatal("bare email was classified as credential-shaped")
	}
}

func TestMaskRunArchiveManifestClonesAndMasksInitialProvenance(t *testing.T) {
	const secret = "initial-provenance-secret"
	initial := RunArchiveProvenance{
		RuntimeSettings: &RuntimeSettingsOverride{
			HTTP: &dsl.HTTPConfig{Auth: dsl.AuthConfig{Password: secret}},
		},
	}
	manifest := &RunArchiveManifest{InitialProvenance: &initial}

	masked := maskRunArchiveManifest(manifest)
	if masked == nil || masked.InitialProvenance == nil || masked.InitialProvenance.RuntimeSettings == nil {
		t.Fatalf("masked initial provenance = %#v", masked)
	}
	if got := masked.InitialProvenance.RuntimeSettings.HTTP.Auth.Password; got != scrub.Redacted {
		t.Fatalf("masked password = %q, want redaction", got)
	}
	if got := manifest.InitialProvenance.RuntimeSettings.HTTP.Auth.Password; got != secret {
		t.Fatalf("source password = %q, want unchanged secret", got)
	}
	if masked.InitialProvenance == manifest.InitialProvenance {
		t.Fatal("masked initial provenance aliases source pointer")
	}
}

func TestMaskRuntimeSettingsCoversConnectionCredentialEnvironmentValues(t *testing.T) {
	input := &RuntimeSettingsOverride{Env: map[string]string{
		"DATABASE_URL":               "postgres://app:short-password@db.internal/app",
		"AZURE_CONNECTION_STRING":    "Endpoint=sb://example/;SharedAccessKey=short",
		"AWS_ACCESS_KEY_ID":          "AKIAEXAMPLE",
		"STORAGE_ACCOUNT_KEY":        "short-account-key",
		"SERVICE_DSN":                "host=db.internal user=app password=short",
		"NEUTRAL_URL":                "https://user:password@example.test/path",
		"NEUTRAL_CONNECTION_VALUE":   "Endpoint=sb://example/;SharedAccessKey=short",
		"AUTHOR":                     "Ada Lovelace",
		"PUBLIC_REGION":              "eastus",
		"PUBLIC_PASSWORDLESS_ORIGIN": "https://example.test/path",
	}}

	masked := maskRuntimeSettings(input)
	for _, key := range []string{
		"DATABASE_URL", "AZURE_CONNECTION_STRING", "AWS_ACCESS_KEY_ID",
		"STORAGE_ACCOUNT_KEY", "SERVICE_DSN", "NEUTRAL_URL", "NEUTRAL_CONNECTION_VALUE",
	} {
		if got := masked.Env[key]; got != scrub.Redacted {
			t.Errorf("masked Env[%q] = %q, want redaction", key, got)
		}
	}
	for key, want := range map[string]string{
		"AUTHOR":                     "Ada Lovelace",
		"PUBLIC_REGION":              "eastus",
		"PUBLIC_PASSWORDLESS_ORIGIN": "https://example.test/path",
	} {
		if got := masked.Env[key]; got != want {
			t.Errorf("masked Env[%q] = %q, want %q", key, got, want)
		}
	}
	if input.Env["DATABASE_URL"] == scrub.Redacted {
		t.Fatal("maskRuntimeSettings mutated stored runtime settings")
	}
}

func TestReconcileRuntimeEnvironmentRedactionRequiresPriorValue(t *testing.T) {
	prior := &RuntimeSettingsOverride{Env: map[string]string{"DATABASE_URL": "postgres://user:pass@db/app"}}
	reconciled := reconcileGroupRuntimeSecrets(&RuntimeSettingsOverride{Env: map[string]string{
		"DATABASE_URL": scrub.Redacted,
		"ORPHAN_DSN":   scrub.Redacted,
	}}, prior)
	if got := reconciled.Env["DATABASE_URL"]; got != prior.Env["DATABASE_URL"] {
		t.Fatalf("reconciled DATABASE_URL = %q", got)
	}
	if _, exists := reconciled.Env["ORPHAN_DSN"]; exists {
		t.Fatal("orphan redaction sentinel was persisted as a runtime credential")
	}
}

// In a scenario response, a value that is only a project-secret reference
// names a secret and stays readable; a controller-owned runtime reference, a
// plaintext credential and a reference inside a credential-shaped value stay
// masked. The content form (library export, hashes) still masks a reference
// under a sensitive name.
func TestMaskRuntimeSettingsForEditorShowsOnlyProjectSecretReferences(t *testing.T) {
	settings := &RuntimeSettingsOverride{Env: map[string]string{
		"API_TOKEN":     "{{secret.api_token}}",
		"SPACED":        " {{ secret.spaced_token }} ",
		"SESSION_TOKEN": "{{secret." + runtimeOverrideSecretName("scenario", 0, "env.SESSION_TOKEN") + "}}",
		"PASSWORD":      "plaintext-password",
		"AUTH":          "Bearer {{secret.api_token}}",
	}}
	want := map[string]string{
		"API_TOKEN":     "{{secret.api_token}}",
		"SPACED":        " {{ secret.spaced_token }} ",
		"SESSION_TOKEN": scrub.Redacted,
		"PASSWORD":      scrub.Redacted,
		"AUTH":          scrub.Redacted,
	}
	if got := maskRuntimeSettingsForEditor(settings).Env; !reflect.DeepEqual(got, want) {
		t.Fatalf("editor env = %#v, want %#v", got, want)
	}
	if got := maskRuntimeSettings(settings).Env["API_TOKEN"]; got != scrub.Redacted {
		t.Fatalf("content env API_TOKEN = %q, want it masked as before", got)
	}
}
