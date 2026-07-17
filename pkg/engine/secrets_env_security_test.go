package engine

import "testing"

func TestEnvSecretResolverCannotReadArbitraryProcessCredentials(t *testing.T) {
	t.Setenv("APEXLOAD_GRPC_AUTH_TOKEN", "platform-control-token")
	t.Setenv("AZURE_CLIENT_SECRET", "cloud-identity-secret")
	resolver := envSecretResolver{}

	for _, name := range []string{"APEXLOAD_GRPC_AUTH_TOKEN", "AZURE_CLIENT_SECRET"} {
		if value, ok := resolver.Resolve(name); ok {
			t.Fatalf("Resolve(%q) exposed arbitrary process credential %q", name, value)
		}
	}
}

func TestEnvSecretResolverUsesDedicatedNamespace(t *testing.T) {
	t.Setenv("DB_PASSWORD", "ambient-value")
	t.Setenv("APEXLOAD_SECRET_DB_PASSWORD", "script-secret")

	value, ok := (envSecretResolver{}).Resolve("db.password")
	if !ok || value != "script-secret" {
		t.Fatalf("Resolve(db.password) = %q, %v; want dedicated script secret", value, ok)
	}
}

func TestScriptSecretEnvAvailableDoesNotExposeValue(t *testing.T) {
	t.Setenv("APEXLOAD_SECRET_SERVICE_TOKEN", "script-secret")
	if !ScriptSecretEnvAvailable("service-token") {
		t.Fatal("dedicated script secret was not reported available")
	}
	if ScriptSecretEnvAvailable("APEXLOAD_SECRET_SERVICE_TOKEN") {
		t.Fatal("availability helper accepted a raw process-environment name")
	}
}

func TestExternalSecretResolversRejectRedactionSentinels(t *testing.T) {
	for _, value := range []string{"[REDACTED]", " [redacted] ", "\t[ReDaCtEd]\n"} {
		t.Run(value, func(t *testing.T) {
			resolver := SecretResolverFunc(func(string) (string, bool) {
				return value, true
			})
			resolved, ok := ResolveSecret(nil, resolver, "SERVICE_TOKEN")
			if ok || resolved != "" {
				t.Fatalf("ResolveSecret() = %q, %v; want missing", resolved, ok)
			}

			t.Setenv("APEXLOAD_SECRET_SERVICE_TOKEN", value)
			if ScriptSecretEnvAvailable("service-token") {
				t.Fatal("redaction sentinel was reported as an available environment secret")
			}
		})
	}
}
