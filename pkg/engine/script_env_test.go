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

func TestScriptEnvUnsetAllowlistDeniesOrdinaryKeys(t *testing.T) {
	t.Setenv(EnvScriptEnvAllowlist, "")
	t.Setenv("APEXLOAD_PUBLIC_REGION", "east")
	t.Setenv("APEXLOAD_LOG_TOKEN", "abc123")

	if _, ok := lookupScriptEnv("APEXLOAD_PUBLIC_REGION"); ok {
		t.Fatal("unset allowlist must deny process environment access")
	}
	if _, ok := lookupScriptEnv("APEXLOAD_LOG_TOKEN"); ok {
		t.Fatal("credential-like names must always be denied")
	}
}

func TestScriptEnvAllowlistCannotExposeCredentials(t *testing.T) {
	t.Setenv(EnvScriptEnvAllowlist, "APEXLOAD_GRPC_AUTH_TOKEN,AWS_ACCESS_KEY_ID,SAFE_API_KEY")
	t.Setenv("APEXLOAD_GRPC_AUTH_TOKEN", "controller-token")
	t.Setenv("AWS_ACCESS_KEY_ID", "cloud-key")
	t.Setenv("SAFE_API_KEY", "api-key")
	for _, key := range []string{"APEXLOAD_GRPC_AUTH_TOKEN", "AWS_ACCESS_KEY_ID", "SAFE_API_KEY"} {
		if _, ok := lookupScriptEnv(key); ok {
			t.Errorf("credential key %q was exposed despite the hard block", key)
		}
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
