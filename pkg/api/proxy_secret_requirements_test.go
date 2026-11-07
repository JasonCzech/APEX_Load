package api

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
)

const proxySecretBlockerForTest = "Custom proxy credential not bound: The proxy password uses the secret ENTERPRISE_PROXY_PASSWORD, which is not bound to a project secret. Bind a project secret to it, or change Proxy mode to No proxy, System or PAC."

func proxySecretScriptForTest(mode string) *dsl.Script {
	return &dsl.Script{
		Name: "proxy", Version: "1", Protocol: "http",
		Config: dsl.ScriptConfig{BaseURL: "https://example.test", HTTP: dsl.HTTPConfig{
			ProxyMode: mode,
			Proxy:     dsl.ProxyConfig{URL: "http://proxy.example.test:8080", Password: "{{secret.ENTERPRISE_PROXY_PASSWORD}}"},
		}},
		Actions: []dsl.Action{{Name: "Call", Type: "request", Method: "GET", Target: "/?token={{secret.API_TOKEN}}"}},
	}
}

func onlyAPITokenResolver() engine.SecretResolver {
	return engine.SecretResolverFunc(func(name string) (string, bool) {
		if name == "API_TOKEN" {
			return "api-value", true
		}
		return "", false
	})
}

func TestDistributedAndCISecretChecksRequireOnlyActiveProxyCredentials(t *testing.T) {
	for _, mode := range []string{"none", "system", "pac"} {
		bundle, err := resolveDistributedSecretBundle(proxySecretScriptForTest(mode), onlyAPITokenResolver())
		if err != nil {
			t.Fatalf("%s proxy distributed bundle: %v", mode, err)
		}
		if names := secretBundleNames(bundle); !slices.Equal(names, []string{"API_TOKEN"}) {
			t.Fatalf("%s proxy distributed bundle names = %v, want only API_TOKEN", mode, names)
		}
		if err := validateCIRequiredProjectSecrets(proxySecretScriptForTest(mode), onlyAPITokenResolver()); err != nil {
			t.Fatalf("%s proxy CI secret check: %v", mode, err)
		}
	}
	custom := proxySecretScriptForTest("custom")
	if _, err := resolveDistributedSecretBundle(custom, onlyAPITokenResolver()); err == nil || err.Error() != proxySecretBlockerForTest {
		t.Fatalf("custom proxy distributed bundle error = %v, want %q", err, proxySecretBlockerForTest)
	}
	if err := validateCIRequiredProjectSecrets(custom, onlyAPITokenResolver()); err == nil || err.Error() != proxySecretBlockerForTest {
		t.Fatalf("custom proxy CI error = %v, want %q", err, proxySecretBlockerForTest)
	}
	bound := engine.SecretResolverFunc(func(name string) (string, bool) { return name + "-value", true })
	if err := validateCIRequiredProjectSecrets(custom, bound); err != nil {
		t.Fatalf("custom proxy with a bound secret: %v", err)
	}
}

func TestScriptSecretStatusRequiresOnlyActiveProxyCredentials(t *testing.T) {
	server, _, _, projectID := newProjectSecretAPIServer(t)
	script := proxySecretScriptForTest("system")
	script.ID, script.ProjectID = "system-proxy-secret", projectID
	stored, err := server.runner.StoreScriptDurable(script)
	if err != nil {
		t.Fatal(err)
	}
	response := secretAPIRequest(t, server, http.MethodGet, "/api/v1/scripts/"+stored.ID+"/secret-status", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Referenced []string `json:"referenced"`
		Required   []string `json:"required"`
		Missing    []string `json:"missing"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(payload.Referenced, []string{"API_TOKEN", "ENTERPRISE_PROXY_PASSWORD"}) ||
		!slices.Equal(payload.Required, []string{"API_TOKEN"}) ||
		!slices.Equal(payload.Missing, []string{"API_TOKEN"}) {
		t.Fatalf("secret status = %+v, want the System proxy secret referenced but not required or missing", payload)
	}
}

// legacyEncryptedProxyGateForTest is the callout as imports wrote it before it
// became advisory: execution_blocker metadata, which a generator or controller
// built before that change blocks on.
func legacyEncryptedProxyGateForTest() dsl.Action {
	return dsl.Action{Name: "proxy setting", Type: "source_review", Metadata: map[string]any{
		"execution_blocker":  "encrypted_proxy_password",
		"execution_blockers": []any{"encrypted_proxy_password"},
		"import_review": map[string]any{"version": 1, "source": enterpriseArchiveSaveProfile,
			"reason_codes": []any{"encrypted_proxy_password"}, "resolution": "replace_proxy_secret"},
	}}
}

// Scripts saved before the encrypted proxy callout became advisory carry it as
// execution_blocker metadata; current imports write it as advisory_callouts.
// Neither shape may hold readiness, replay or launch, and the guided repairs
// must apply to both.
func TestLegacyEncryptedProxyPasswordGateIsAdvisory(t *testing.T) {
	for shape, gate := range map[string]func() dsl.Action{"legacy execution_blocker": legacyEncryptedProxyGateForTest, "advisory_callouts": encryptedProxyGateForTest} {
		script := proxySecretScriptForTest("system")
		script.Init = []dsl.Action{gate()}
		readiness, err := importreview.EvaluateScript(script, importreview.EvaluationInput{})
		if err != nil {
			t.Fatal(err)
		}
		if !readiness.ReplayReady || slices.Contains(readiness.BlockingCodes, "encrypted_proxy_password") {
			t.Fatalf("%s: readiness = %+v, want the encrypted proxy callout not to block", shape, readiness)
		}
		if issues := launchReviewGateIssues(script); len(issues) != 0 {
			t.Fatalf("%s: launch review gates = %v, want none", shape, issues)
		}
		findings, err := importreview.FindingsFromScript(script)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, finding := range findings {
			if finding.Code == "encrypted_proxy_password" && finding.ActionPath == "init[0]" {
				found = true
				if finding.Gate == importreview.GateExecution || !strings.Contains(finding.Detail, "needed only while Proxy mode is Custom") {
					t.Fatalf("%s: callout finding = %#v", shape, finding)
				}
			}
		}
		if !found {
			t.Fatalf("%s: findings = %#v, want the encrypted proxy callout", shape, findings)
		}
		if _, err := applyImportRepair(script, ImportRepairRequest{Finding: "encrypted_proxy_password", ActionPath: "init[0]", Operation: ImportRepairOperation{Type: "replace_proxy_secret", SecretName: "PROJECT_PROXY"}}); err != nil {
			t.Fatalf("%s: replace_proxy_secret on the advisory callout: %v", shape, err)
		}
		if script.Config.HTTP.Proxy.Password != "{{secret.PROJECT_PROXY}}" {
			t.Fatalf("%s: proxy password = %q, want the replacement secret", shape, script.Config.HTTP.Proxy.Password)
		}
		if paths := executionGatePaths(script, "encrypted_proxy_password"); len(paths) != 0 {
			t.Fatalf("%s: callout survived the repair at %v", shape, paths)
		}
	}
	// omit_proxy_credentials clears every copy of the callout, in either shape.
	script := proxySecretScriptForTest("custom")
	script.Init = []dsl.Action{legacyEncryptedProxyGateForTest(), encryptedProxyGateForTest()}
	if _, err := applyImportRepair(script, ImportRepairRequest{Finding: "encrypted_proxy_password", ActionPath: "init[1]", Operation: ImportRepairOperation{Type: "omit_proxy_credentials"}}); err != nil {
		t.Fatalf("omit_proxy_credentials: %v", err)
	}
	if paths := executionGatePaths(script, "encrypted_proxy_password"); len(paths) != 0 {
		t.Fatalf("omit_proxy_credentials left the callout at %v", paths)
	}
}
