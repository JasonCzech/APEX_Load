package api

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
)

func TestResolveDistributedSecretBundleIncludesOnlyReferencedNames(t *testing.T) {
	script := &dsl.Script{
		Config: dsl.ScriptConfig{
			Database: dsl.DatabaseConfig{
				DSN:       "{{secret.DATABASE_DSN}}",
				DSNSecret: "LEGACY_DATABASE_DSN",
			},
		},
		Actions: []dsl.Action{{
			Headers: map[string]string{"Authorization": "Bearer {{secret.API_TOKEN}}"},
		}},
	}
	values := map[string]string{
		"API_TOKEN":           "api-value",
		"DATABASE_DSN":        "database-value",
		"LEGACY_DATABASE_DSN": "legacy-database-value",
		"UNUSED":              "must-not-be-delivered",
	}
	bundle, err := resolveDistributedSecretBundle(script, engine.SecretResolverFunc(func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle) != 3 || bundle["API_TOKEN"] != "api-value" || bundle["DATABASE_DSN"] != "database-value" || bundle["LEGACY_DATABASE_DSN"] != "legacy-database-value" {
		t.Fatalf("bundle names = %v", secretBundleNames(bundle))
	}
	if _, exists := bundle["UNUSED"]; exists {
		t.Fatal("unreferenced secret was delivered")
	}
}

func TestResolveDistributedSecretBundleFailsClosedWithoutLeakingValues(t *testing.T) {
	const storedValue = "never-include-this-value"
	script := &dsl.Script{Actions: []dsl.Action{{
		Body: "{{secret.PRESENT}} {{secret.MISSING}}",
	}}}
	_, err := resolveDistributedSecretBundle(script, engine.SecretResolverFunc(func(name string) (string, bool) {
		if name == "PRESENT" {
			return storedValue, true
		}
		return "", false
	}))
	if err == nil || !strings.Contains(err.Error(), "MISSING") {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(err.Error(), storedValue) {
		t.Fatalf("error leaked secret value: %v", err)
	}
}

func TestValidateDistributedSecretDeliveryRequirements(t *testing.T) {
	t.Setenv("APEXLOAD_GRPC_TLS_CERT", "")
	t.Setenv("APEXLOAD_GRPC_TLS_KEY", "")
	t.Setenv("APEXLOAD_GRPC_TLS_CA", "")
	t.Setenv("APEXLOAD_GRPC_AUTH_TOKEN", "")
	bundle := map[string]string{"TOKEN": "value"}
	if err := validateDistributedSecretTransport(bundle); err == nil {
		t.Fatal("plaintext secret delivery was accepted")
	}
	t.Setenv("APEXLOAD_GRPC_TLS_CA", "configured")
	t.Setenv("APEXLOAD_GRPC_AUTH_TOKEN", strings.Repeat("t", 32))
	if err := validateDistributedSecretTransport(bundle); err != nil {
		t.Fatalf("authenticated TLS rejected: %v", err)
	}
	if err := validateDistributedSecretTarget("generator-a", generatorLoadSnapshot{
		HeartbeatOK:        true,
		CompatibleFeatures: []string{generatorrpc.FeatureSecretBundleV1},
	}, bundle); err != nil {
		t.Fatalf("capable generator rejected: %v", err)
	}
	if err := validateDistributedSecretTarget("legacy", generatorLoadSnapshot{
		HeartbeatOK:        true,
		CompatibleFeatures: []string{"http"},
	}, bundle); err == nil {
		t.Fatal("legacy generator accepted a secret bundle")
	}
}

func secretBundleNames(bundle map[string]string) []string {
	names := make([]string, 0, len(bundle))
	for name := range bundle {
		names = append(names, name)
	}
	return names
}
