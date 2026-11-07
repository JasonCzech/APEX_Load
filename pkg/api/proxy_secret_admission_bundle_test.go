package api

import (
	"context"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
)

// admitProxySecretRunForTest admits a one-group run of script through
// validateStartSecretRequirements and returns the runner and run, ready for
// the distributed start path.
func admitProxySecretRunForTest(t *testing.T, script *dsl.Script, values map[string]string) (*TestRunner, *ManagedTest, error) {
	t.Helper()
	script.ProjectID = "project-one"
	stored := make(map[string]string, len(values))
	for name, value := range values {
		stored["project-one/"+name] = value
	}
	runner := NewTestRunner(1, NewHub(), nil, WithProjectSecretStore(&runtimeProjectionSecretStore{values: stored}))
	run := &ManagedTest{ID: "proxy-run", ProjectID: "project-one", Status: "QUEUED",
		groupSpecs:    []engine.GroupConfig{{Name: "group", Script: script}},
		Groups:        []TestGroup{{Name: "group"}},
		pinnedSecrets: &runPinnedSecretState{},
		executionCtx:  context.Background(),
	}
	runner.tests[run.ID] = run
	return runner, run, runner.validateStartSecretRequirements(run.ID)
}

// distributedStartSecretsForTest runs the distributed start path exactly as
// distributedExecutionBackend.Run does: the run's pinned resolver, the bundle,
// the unbundled names and the up-front target check, then the snapshot that
// evacuation reuses and evacuation's own target check.
func distributedStartSecretsForTest(t *testing.T, runner *TestRunner, run *ManagedTest, script *dsl.Script, target generatorLoadSnapshot) (map[string]string, error, error) {
	t.Helper()
	resolver := runner.secretResolverForRunGroup(run, 0, script.ProjectID)
	resolver = overlayRuntimeSecretResolver(run.Groups[0].runtimeSecretValues, resolver)
	bundle, err := resolveDistributedSecretBundle(script, resolver)
	if err != nil {
		t.Fatalf("resolve distributed bundle: %v", err)
	}
	if err := validateDistributedSecretTransport(bundle); err != nil {
		t.Fatalf("transport: %v", err)
	}
	unbundled, err := distributedUnbundledSecretNames(script, bundle)
	if err != nil {
		t.Fatal(err)
	}
	startErr := validateDistributedSecretTarget("legacy", target, bundle, unbundled)
	if err := runner.storeDistributedRunSecretSnapshot(run, "group", "bundle-id", bundle); err != nil {
		t.Fatal(err)
	}
	snapshot, found := runner.distributedRunSecretSnapshot(run, "group")
	if !found {
		t.Fatal("run secret snapshot was not retained for evacuation")
	}
	evacuationUnbundled, err := distributedUnbundledSecretNames(script, snapshot.Values)
	if err != nil {
		t.Fatal(err)
	}
	evacuationErr := validateDistributedSecretTarget("legacy", target, snapshot.Values, evacuationUnbundled)
	return bundle, startErr, evacuationErr
}

// Admission must pin a bound secret behind an inactive proxy credential: the
// run's pinned resolver is the only source the distributed bundle reads, and a
// generator that predates FeatureSecretBundleRequiredOnlyV1 demands every
// referenced name. Starting and evacuating onto such a generator must
// therefore work exactly as before the required-only rule.
func TestStartAdmissionPinsBoundInactiveProxySecretForLegacyGenerator(t *testing.T) {
	configureDistributedSecretTransportForTest(t, true)
	legacy := generatorLoadSnapshot{HeartbeatOK: true, CompatibleFeatures: []string{generatorrpc.FeatureSecretBundleV1}}
	bound := map[string]string{"API_TOKEN": "api-value", "ENTERPRISE_PROXY_PASSWORD": "proxy-value"}
	for _, mode := range []string{"none", "system", "pac"} {
		for _, proxyOnly := range []bool{false, true} {
			script := proxySecretScriptForTest(mode)
			name := mode + " with API_TOKEN"
			if proxyOnly {
				script.Actions[0].Target = "/"
				name = mode + " proxy secret only"
			}
			runner, run, err := admitProxySecretRunForTest(t, script, bound)
			if err != nil {
				t.Fatalf("%s: admission: %v", name, err)
			}
			if got := run.pinnedSecrets.snapshot()[0]["ENTERPRISE_PROXY_PASSWORD"]; got != "proxy-value" {
				t.Fatalf("%s: pinned proxy secret = %q, want the bound value pinned", name, got)
			}
			bundle, startErr, evacuationErr := distributedStartSecretsForTest(t, runner, run, script, legacy)
			if bundle["ENTERPRISE_PROXY_PASSWORD"] != "proxy-value" {
				t.Fatalf("%s: bundle names = %v, want the bound inactive proxy secret delivered", name, secretBundleNames(bundle))
			}
			if !proxyOnly && bundle["API_TOKEN"] != "api-value" {
				t.Fatalf("%s: bundle names = %v, want API_TOKEN", name, secretBundleNames(bundle))
			}
			if startErr != nil {
				t.Fatalf("%s: legacy generator refused at start: %v", name, startErr)
			}
			if evacuationErr != nil {
				t.Fatalf("%s: legacy generator refused at evacuation: %v", name, evacuationErr)
			}
		}
	}
}

// An unbound secret behind an inactive proxy credential still never blocks
// admission. A legacy generator, which demands every referenced name, is then
// refused up front at start and at evacuation; an upgraded one runs.
func TestStartAdmissionIgnoresUnboundInactiveProxySecret(t *testing.T) {
	configureDistributedSecretTransportForTest(t, true)
	legacy := generatorLoadSnapshot{HeartbeatOK: true, CompatibleFeatures: []string{generatorrpc.FeatureSecretBundleV1}}
	upgraded := generatorLoadSnapshot{HeartbeatOK: true, CompatibleFeatures: []string{generatorrpc.FeatureSecretBundleV1, generatorrpc.FeatureSecretBundleRequiredOnlyV1}}
	for _, proxyOnly := range []bool{false, true} {
		script := proxySecretScriptForTest("system")
		if proxyOnly {
			script.Actions[0].Target = "/"
		}
		runner, run, err := admitProxySecretRunForTest(t, script, map[string]string{"API_TOKEN": "api-value"})
		if err != nil {
			t.Fatalf("proxyOnly=%t: admission blocked by an unbound inactive proxy secret: %v", proxyOnly, err)
		}
		if _, pinned := run.pinnedSecrets.snapshot()[0]["ENTERPRISE_PROXY_PASSWORD"]; pinned {
			t.Fatalf("proxyOnly=%t: an unbound secret was pinned", proxyOnly)
		}
		_, startErr, evacuationErr := distributedStartSecretsForTest(t, runner, run, script, legacy)
		for phase, err := range map[string]error{"start": startErr, "evacuation": evacuationErr} {
			if err == nil || !strings.Contains(err.Error(), generatorrpc.FeatureSecretBundleRequiredOnlyV1) || !strings.Contains(err.Error(), "ENTERPRISE_PROXY_PASSWORD") {
				t.Fatalf("proxyOnly=%t: legacy generator %s error = %v, want the required-only feature and the secret named", proxyOnly, phase, err)
			}
		}
		_, startErr, evacuationErr = distributedStartSecretsForTest(t, runner, run, script, upgraded)
		if startErr != nil || evacuationErr != nil {
			t.Fatalf("proxyOnly=%t: upgraded generator refused: start=%v evacuation=%v", proxyOnly, startErr, evacuationErr)
		}
	}
	// A Custom proxy's unbound secret still blocks admission, with the
	// contract wording.
	_, _, err := admitProxySecretRunForTest(t, proxySecretScriptForTest("custom"), map[string]string{"API_TOKEN": "api-value"})
	if err == nil || !strings.Contains(err.Error(), proxySecretBlockerForTest) {
		t.Fatalf("custom proxy admission error = %v, want %q", err, proxySecretBlockerForTest)
	}
}
