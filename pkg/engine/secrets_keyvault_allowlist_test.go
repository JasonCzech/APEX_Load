package engine

import (
	"strings"
	"sync/atomic"
	"testing"
)

func TestAzureKeyVaultResolverFromEnvRequiresExplicitSecretAllowlist(t *testing.T) {
	t.Setenv("APEXLOAD_AZURE_KEY_VAULT_URL", "https://vault.example.test")
	t.Setenv("APEXLOAD_AZURE_KEY_VAULT_AUTH", "token")
	t.Setenv("APEXLOAD_AZURE_KEY_VAULT_TOKEN", "test-token")
	t.Setenv("APEXLOAD_AZURE_KEY_VAULT_ALLOWED_SECRETS", "")

	if resolver := newAzureKeyVaultSecretResolverFromEnv(); resolver != nil {
		t.Fatalf("resolver = %T, want nil without an operator-owned allowlist", resolver)
	}
}

func TestAzureKeyVaultAllowlistRejectsUnlistedNamesBeforeDelegate(t *testing.T) {
	var calls atomic.Int64
	delegate := SecretResolverFunc(func(name string) (string, bool) {
		calls.Add(1)
		return "value-for-" + name, true
	})
	resolver := allowlistedAzureKeyVaultSecretResolver{
		delegate: delegate,
		allowed: map[string]struct{}{
			"db-password": {},
		},
	}

	if value, ok := resolver.Resolve("db_password"); !ok || value != "value-for-db-password" {
		t.Fatalf("allowed Resolve = %q/%v", value, ok)
	}
	if value, ok := resolver.Resolve("controller-signing-key"); ok || value != "" {
		t.Fatalf("unlisted Resolve = %q/%v, want rejection", value, ok)
	}
	if calls.Load() != 1 {
		t.Fatalf("delegate calls = %d, want only the allowed lookup", calls.Load())
	}
}

func TestAzureKeyVaultAllowedSecretNamesFailClosed(t *testing.T) {
	t.Setenv("APEXLOAD_AZURE_KEY_VAULT_ALLOWED_SECRETS", "db-password, ../../platform-token")
	if allowed, err := azureKeyVaultAllowedSecretNamesFromEnv(); err == nil || allowed != nil {
		t.Fatalf("invalid allowlist = %#v, %v; want nil error result", allowed, err)
	}

	names := make([]string, maxAzureKeyVaultAllowedNames+1)
	for i := range names {
		names[i] = "secret-" + strings.Repeat("a", i/26) + string(rune('a'+i%26))
	}
	t.Setenv("APEXLOAD_AZURE_KEY_VAULT_ALLOWED_SECRETS", strings.Join(names, ","))
	if allowed, err := azureKeyVaultAllowedSecretNamesFromEnv(); err == nil || allowed != nil {
		t.Fatalf("oversized allowlist = %d names, %v; want rejection", len(allowed), err)
	}
}

func TestProjectKeyVaultBindingAllowlistIsProjectScoped(t *testing.T) {
	const projectID = "allowlisted-project"
	name := ProjectSecretBindingName(projectID, "LOGIN_PASSWORD")
	t.Setenv("APEXLOAD_AZURE_KEY_VAULT_URL", "https://vault.example.test")
	t.Setenv("APEXLOAD_AZURE_KEY_VAULT_ALLOWED_SECRETS", name)
	if !ProjectKeyVaultAllowlistConfigured() || !ProjectKeyVaultProjectAllowlisted(projectID) ||
		!ProjectKeyVaultBindingAllowlisted(projectID, "LOGIN_PASSWORD") ||
		!ProjectKeyVaultBindingAllowlisted(projectID, "login.password") {
		t.Fatal("project binding was not recognized as reserved")
	}
	if ProjectKeyVaultProjectAllowlisted("other-project") || ProjectKeyVaultBindingAllowlisted("other-project", "LOGIN_PASSWORD") ||
		ProjectKeyVaultBindingAllowlisted(projectID, "OTHER") || ProjectKeyVaultBindingAllowlisted("\x00application:app", "LOGIN_PASSWORD") {
		t.Fatal("allowlist reservation crossed project or logical name")
	}
	t.Setenv("APEXLOAD_AZURE_KEY_VAULT_URL", "http://vault.example.test")
	if ProjectKeyVaultAllowlistConfigured() || ProjectKeyVaultBindingAllowlisted(projectID, "LOGIN_PASSWORD") {
		t.Fatal("invalid vault URL reserved a project binding")
	}
}
