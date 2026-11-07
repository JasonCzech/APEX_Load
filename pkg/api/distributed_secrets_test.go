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
	}, bundle, nil); err != nil {
		t.Fatalf("capable generator rejected: %v", err)
	}
	if err := validateDistributedSecretTarget("legacy", generatorLoadSnapshot{
		HeartbeatOK:        true,
		CompatibleFeatures: []string{"http"},
	}, bundle, nil); err == nil {
		t.Fatal("legacy generator accepted a secret bundle")
	}
}

func configureDistributedSecretTransportForTest(t *testing.T, secure bool) {
	t.Helper()
	t.Setenv("APEXLOAD_GRPC_TLS_CERT", "")
	t.Setenv("APEXLOAD_GRPC_TLS_KEY", "")
	t.Setenv("APEXLOAD_GRPC_TLS_CA", "")
	t.Setenv("APEXLOAD_GRPC_AUTH_TOKEN", "")
	if secure {
		t.Setenv("APEXLOAD_GRPC_TLS_CA", "configured")
		t.Setenv("APEXLOAD_GRPC_AUTH_TOKEN", strings.Repeat("t", 32))
	}
}

// A generator that predates FeatureSecretBundleRequiredOnlyV1 demands every
// referenced secret name. A bound secret behind an inactive proxy credential
// must therefore still ride in the bundle, so a mixed-version fleet starts the
// run exactly as it did before the required-only rule.
func TestDistributedSecretBundleCarriesBoundInactiveProxySecretForLegacyGenerator(t *testing.T) {
	configureDistributedSecretTransportForTest(t, true)
	legacy := generatorLoadSnapshot{HeartbeatOK: true, CompatibleFeatures: []string{generatorrpc.FeatureSecretBundleV1}}
	bound := engine.SecretResolverFunc(func(name string) (string, bool) { return name + "-value", true })
	for _, mode := range []string{"none", "system", "pac"} {
		withAPIToken := proxySecretScriptForTest(mode)
		proxyOnly := proxySecretScriptForTest(mode)
		proxyOnly.Actions[0].Target = "/"
		for name, script := range map[string]*dsl.Script{"with API_TOKEN": withAPIToken, "proxy secret only": proxyOnly} {
			bundle, err := resolveDistributedSecretBundle(script, bound)
			if err != nil {
				t.Fatalf("%s %s: %v", mode, name, err)
			}
			if bundle["ENTERPRISE_PROXY_PASSWORD"] != "ENTERPRISE_PROXY_PASSWORD-value" {
				t.Fatalf("%s %s: bundle names = %v, want the bound inactive proxy secret delivered", mode, name, secretBundleNames(bundle))
			}
			referenced, err := engine.ReferencedSecretNames(script)
			if err != nil {
				t.Fatal(err)
			}
			for _, referencedName := range referenced {
				if _, ok := bundle[referencedName]; !ok {
					t.Fatalf("%s %s: bundle %v omits referenced %q, which a legacy generator demands", mode, name, secretBundleNames(bundle), referencedName)
				}
			}
			unbundled, err := distributedUnbundledSecretNames(script, bundle)
			if err != nil || len(unbundled) != 0 {
				t.Fatalf("%s %s: unbundled = %v err=%v, want none", mode, name, unbundled, err)
			}
			if err := validateDistributedSecretTarget("legacy", legacy, bundle, unbundled); err != nil {
				t.Fatalf("%s %s: legacy generator rejected a complete bundle: %v", mode, name, err)
			}
		}
	}
}

// An unbound secret behind an inactive proxy credential never blocks, but a
// generator that demands every referenced name cannot run without it: the
// controller rejects that target up front, naming the feature and the secret,
// even when nothing else is bundled.
func TestDistributedSecretTargetRejectsLegacyGeneratorForUnboundInactiveProxySecret(t *testing.T) {
	configureDistributedSecretTransportForTest(t, true)
	legacy := generatorLoadSnapshot{HeartbeatOK: true, CompatibleFeatures: []string{generatorrpc.FeatureSecretBundleV1}}
	upgraded := generatorLoadSnapshot{HeartbeatOK: true, CompatibleFeatures: []string{generatorrpc.FeatureSecretBundleV1, generatorrpc.FeatureSecretBundleRequiredOnlyV1}}
	proxyOnly := proxySecretScriptForTest("system")
	proxyOnly.Actions[0].Target = "/"
	unbound := engine.SecretResolverFunc(func(string) (string, bool) { return "", false })
	for name, test := range map[string]struct {
		script   *dsl.Script
		resolver engine.SecretResolver
		bundled  []string
	}{
		"empty bundle":         {script: proxyOnly, resolver: unbound},
		"no resolver":          {script: proxyOnly},
		"required-only bundle": {script: proxySecretScriptForTest("system"), resolver: onlyAPITokenResolver(), bundled: []string{"API_TOKEN"}},
	} {
		bundle, err := resolveDistributedSecretBundle(test.script, test.resolver)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if names := secretBundleNames(bundle); len(names) != len(test.bundled) || (len(names) == 1 && names[0] != test.bundled[0]) {
			t.Fatalf("%s: bundle names = %v, want %v", name, names, test.bundled)
		}
		unbundled, err := distributedUnbundledSecretNames(test.script, bundle)
		if err != nil || len(unbundled) != 1 || unbundled[0] != "ENTERPRISE_PROXY_PASSWORD" {
			t.Fatalf("%s: unbundled = %v err=%v", name, unbundled, err)
		}
		err = validateDistributedSecretTarget("legacy", legacy, bundle, unbundled)
		if err == nil || !strings.Contains(err.Error(), generatorrpc.FeatureSecretBundleRequiredOnlyV1) || !strings.Contains(err.Error(), "ENTERPRISE_PROXY_PASSWORD") {
			t.Fatalf("%s: legacy generator error = %v, want the required-only feature and the secret named", name, err)
		}
		if err := validateDistributedSecretTarget("upgraded", upgraded, bundle, unbundled); err != nil {
			t.Fatalf("%s: upgraded generator rejected: %v", name, err)
		}
	}
	if err := validateDistributedSecretTarget("offline", generatorLoadSnapshot{CompatibleFeatures: upgraded.CompatibleFeatures}, nil, []string{"ENTERPRISE_PROXY_PASSWORD"}); err == nil {
		t.Fatal("a generator without a heartbeat was trusted to run without a referenced secret")
	}
}

// Without a transport that may carry secrets, a bound inactive proxy secret is
// withheld instead of failing the run; only an upgraded generator may then run
// the assignment.
func TestDistributedSecretBundleWithholdsInactiveProxySecretOnPlaintextTransport(t *testing.T) {
	configureDistributedSecretTransportForTest(t, false)
	proxyOnly := proxySecretScriptForTest("pac")
	proxyOnly.Actions[0].Target = "/"
	bound := engine.SecretResolverFunc(func(name string) (string, bool) { return name + "-value", true })
	bundle, err := resolveDistributedSecretBundle(proxyOnly, bound)
	if err != nil || len(bundle) != 0 {
		t.Fatalf("bundle = %v err=%v, want the optional proxy secret withheld", secretBundleNames(bundle), err)
	}
	if err := validateDistributedSecretTransport(bundle); err != nil {
		t.Fatalf("transport rejected a run that delivers no secret: %v", err)
	}
	unbundled, _ := distributedUnbundledSecretNames(proxyOnly, bundle)
	upgraded := generatorLoadSnapshot{HeartbeatOK: true, CompatibleFeatures: []string{generatorrpc.FeatureSecretBundleRequiredOnlyV1}}
	if err := validateDistributedSecretTarget("upgraded", upgraded, bundle, unbundled); err != nil {
		t.Fatalf("upgraded generator rejected: %v", err)
	}
	legacy := generatorLoadSnapshot{HeartbeatOK: true, CompatibleFeatures: []string{generatorrpc.FeatureSecretBundleV1}}
	if err := validateDistributedSecretTarget("legacy", legacy, bundle, unbundled); err == nil || !strings.Contains(err.Error(), "authenticated generator TLS") {
		t.Fatalf("legacy generator error = %v, want the TLS remedy", err)
	}
	// A required secret still fails closed on plaintext transport.
	withToken := proxySecretScriptForTest("pac")
	required, err := resolveDistributedSecretBundle(withToken, bound)
	if err != nil {
		t.Fatal(err)
	}
	if names := secretBundleNames(required); len(names) != 1 || names[0] != "API_TOKEN" {
		t.Fatalf("plaintext bundle names = %v, want only the required API_TOKEN", names)
	}
	if err := validateDistributedSecretTransport(required); err == nil {
		t.Fatal("plaintext transport accepted a required secret")
	}
}

func secretBundleNames(bundle map[string]string) []string {
	names := make([]string, 0, len(bundle))
	for name := range bundle {
		names = append(names, name)
	}
	return names
}

func TestDistributedBrowserProxyTargetRefusesLegacyGeneratorForServerlessCredentials(t *testing.T) {
	legacy := generatorLoadSnapshot{HeartbeatOK: true, CompatibleFeatures: []string{generatorrpc.FeatureSecretBundleV1}}
	upgraded := generatorLoadSnapshot{HeartbeatOK: true, CompatibleFeatures: []string{generatorrpc.FeatureSecretBundleV1, generatorrpc.FeatureSecretBundleRequiredOnlyV1}}
	browser := func(proxy dsl.BrowserProxyConfig) *dsl.Script {
		script := &dsl.Script{Name: "browser", Protocol: "browser"}
		script.Config.Browser.Proxy = proxy
		return script
	}
	for _, test := range []struct {
		name       string
		script     *dsl.Script
		wantRefuse bool
	}{
		{name: "bound secret password without server", script: browser(dsl.BrowserProxyConfig{Username: "svc", Password: "{{secret.BROWSER_PROXY_PASSWORD}}"}), wantRefuse: true},
		{name: "plaintext username without server", script: browser(dsl.BrowserProxyConfig{Username: "svc"}), wantRefuse: true},
		{name: "credentials beside a server", script: browser(dsl.BrowserProxyConfig{Server: "http://proxy.example.test:8080", Username: "svc", Password: "{{secret.BROWSER_PROXY_PASSWORD}}"})},
		{name: "bypass without server keeps the old validation error", script: browser(dsl.BrowserProxyConfig{Bypass: "localhost", Username: "svc"})},
		{name: "no browser proxy", script: browser(dsl.BrowserProxyConfig{})},
		{name: "non-browser protocol", script: &dsl.Script{Name: "http", Protocol: "http", Config: dsl.ScriptConfig{Browser: dsl.BrowserConfig{Proxy: dsl.BrowserProxyConfig{Username: "svc"}}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateDistributedBrowserProxyTarget("legacy", legacy, test.script)
			if test.wantRefuse {
				if err == nil || !strings.Contains(err.Error(), "browser proxy username/password are set without a proxy server") ||
					!strings.Contains(err.Error(), generatorrpc.FeatureSecretBundleRequiredOnlyV1) {
					t.Fatalf("legacy generator error = %v, want the upgrade remedy", err)
				}
			} else if err != nil {
				t.Fatalf("legacy generator refused: %v", err)
			}
			if err := validateDistributedBrowserProxyTarget("upgraded", upgraded, test.script); err != nil {
				t.Fatalf("upgraded generator refused: %v", err)
			}
			if test.wantRefuse {
				if err := validateDistributedBrowserProxyTarget("silent", generatorLoadSnapshot{CompatibleFeatures: upgraded.CompatibleFeatures}, test.script); err == nil {
					t.Fatal("generator without a heartbeat was accepted")
				}
			}
		})
	}
}
