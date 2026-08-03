package api

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/scrub"
)

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
