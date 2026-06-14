package engine

import "testing"

func TestScriptEnvAlwaysBlocksSecretNamespace(t *testing.T) {
	t.Setenv(EnvScriptEnvAllowlist, "")
	t.Setenv("APEXLOAD_SECRET_DB_PASSWORD", "super-secret")

	if _, ok := lookupScriptEnv("APEXLOAD_SECRET_DB_PASSWORD"); ok {
		t.Fatal("APEXLOAD_SECRET_* must never be readable via script env, even with no allowlist")
	}
	if _, present := scriptVisibleEnvMap()["APEXLOAD_SECRET_DB_PASSWORD"]; present {
		t.Fatal("APEXLOAD_SECRET_* must be excluded from the CEL env map")
	}
}

func TestScriptEnvUnsetAllowlistAllowsOrdinaryKeys(t *testing.T) {
	t.Setenv(EnvScriptEnvAllowlist, "")
	t.Setenv("APEXLOAD_PUBLIC_REGION", "east")
	// A name containing TOKEN is legitimately used by scripts; without an
	// allowlist it remains readable (backward-compatible default).
	t.Setenv("APEXLOAD_LOG_TOKEN", "abc123")

	if v, ok := lookupScriptEnv("APEXLOAD_PUBLIC_REGION"); !ok || v != "east" {
		t.Fatalf("expected ordinary key readable, got %q ok=%v", v, ok)
	}
	if v, ok := lookupScriptEnv("APEXLOAD_LOG_TOKEN"); !ok || v != "abc123" {
		t.Fatalf("expected TOKEN-named key readable without allowlist, got %q ok=%v", v, ok)
	}
}

func TestScriptEnvAllowlistRestrictsAccess(t *testing.T) {
	t.Setenv(EnvScriptEnvAllowlist, "APEXLOAD_PUBLIC_REGION, APEXLOAD_TIER")
	t.Setenv("APEXLOAD_PUBLIC_REGION", "east")
	t.Setenv("APEXLOAD_TIER", "gold")
	t.Setenv("APEXLOAD_INTERNAL_HOST", "db.internal")

	if v, ok := lookupScriptEnv("APEXLOAD_PUBLIC_REGION"); !ok || v != "east" {
		t.Fatalf("allowlisted key should be readable, got %q ok=%v", v, ok)
	}
	if v, ok := lookupScriptEnv("APEXLOAD_TIER"); !ok || v != "gold" {
		t.Fatalf("allowlisted key (with whitespace) should be readable, got %q ok=%v", v, ok)
	}
	if _, ok := lookupScriptEnv("APEXLOAD_INTERNAL_HOST"); ok {
		t.Fatal("non-allowlisted key must not be readable when allowlist is set")
	}

	envMap := scriptVisibleEnvMap()
	if _, present := envMap["APEXLOAD_INTERNAL_HOST"]; present {
		t.Fatal("non-allowlisted key must be excluded from the CEL env map")
	}
	if _, present := envMap["APEXLOAD_PUBLIC_REGION"]; !present {
		t.Fatal("allowlisted key should be present in the CEL env map")
	}
}

func TestConditionExprEnvMapRespectsAllowlist(t *testing.T) {
	t.Setenv(EnvScriptEnvAllowlist, "APEXLOAD_PUBLIC_REGION")
	t.Setenv("APEXLOAD_PUBLIC_REGION", "east")
	t.Setenv("APEXLOAD_AZURE_KEY_VAULT_TOKEN", "leak-me")

	envMap := conditionExprEnvMap()
	if _, present := envMap["APEXLOAD_AZURE_KEY_VAULT_TOKEN"]; present {
		t.Fatal("CEL condition env must not expose non-allowlisted secrets")
	}
	if envMap["APEXLOAD_PUBLIC_REGION"] != "east" {
		t.Fatalf("CEL condition env should expose allowlisted key, got %v", envMap["APEXLOAD_PUBLIC_REGION"])
	}
}
