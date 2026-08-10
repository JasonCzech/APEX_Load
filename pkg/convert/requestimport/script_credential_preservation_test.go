package requestimport

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
)

// unprojectableURL is a URL the shared policy always rejects: it is not valid
// UTF-8, so no projector can classify it. It is the only reachable way to drive
// the Preserve-mode rejection branches from a script projection, which is
// exactly why those branches must not destroy the imported value.
const unprojectableURL = "https://api.example.test/login?password=reject-canary-\xff\xfe"

func preservedCredentialTargetFields(t *testing.T, metadata map[string]any) []string {
	t.Helper()
	raw, ok := metadata[dsl.ImportPreservedCredentialTargetsMetadataKey]
	if !ok || raw == nil {
		return nil
	}
	payload, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal preserved credential targets %#v: %v", raw, err)
	}
	var targets []credentialpolicy.TargetRef
	if err := json.Unmarshal(payload, &targets); err != nil {
		t.Fatalf("decode preserved credential targets %s: %v", payload, err)
	}
	fields := make([]string, 0, len(targets))
	for _, target := range targets {
		fields = append(fields, target.Field)
	}
	return fields
}

func containsFold(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// A base URL the shared policy cannot project is a review gate, not permission
// to throw the operator's own base URL away and substitute an .invalid host.
func TestProjectScriptCredentialsPreservesUnprojectableBaseURL(t *testing.T) {
	t.Parallel()
	script := validBaseScript()
	script.Config.BaseURL = unprojectableURL
	script.Actions = []dsl.Action{{
		Name:   "request",
		Type:   "GET",
		Method: "GET",
		Target: "/",
	}}

	result, err := ProjectScriptCredentials(script, ScriptCredentialProjectionOptions{
		SourceProfile: "test_import",
		CandidateKey:  "candidate",
		NamePrefix:    "TEST_IMPORT",
	})
	if err != nil {
		t.Fatalf("ProjectScriptCredentials: %v", err)
	}
	if script.Config.BaseURL != unprojectableURL {
		t.Fatalf("base URL = %q, want the exact imported value preserved", script.Config.BaseURL)
	}
	if len(result.Findings) == 0 {
		t.Fatalf("findings = %#v, want a review gate for the unprojectable base URL", result.Findings)
	}
	preserved := script.Metadata.Import.Preserved
	if fields := preservedCredentialTargetFields(t, preserved); !containsFold(fields, "config.base_url") {
		t.Fatalf("preserved credential targets = %#v, want a config.base_url locator", fields)
	}
}

// An additional (recorded-resource) URL the policy cannot project must survive
// as authored: replacing it with the redaction sentinel destroys a value the
// operator already owns and leaves an allowlist entry that can never match.
func TestProjectScriptCredentialsPreservesUnprojectableAdditionalURL(t *testing.T) {
	t.Parallel()
	script := validBaseScript()
	script.Actions = []dsl.Action{{
		Name:   "request",
		Type:   "GET",
		Method: "GET",
		Target: "/",
	}}

	result, err := ProjectScriptCredentials(script, ScriptCredentialProjectionOptions{
		SourceProfile: "test_import",
		CandidateKey:  "candidate",
		NamePrefix:    "TEST_IMPORT",
		AdditionalURLs: []ScriptCredentialURL{{
			Value:       unprojectableURL,
			TargetField: "metadata.import.preserved.recorded_resource_urls[0]",
			Element:     "recorded resource URL",
		}},
	})
	if err != nil {
		t.Fatalf("ProjectScriptCredentials: %v", err)
	}
	if len(result.AdditionalURLs) != 1 || result.AdditionalURLs[0] != unprojectableURL {
		t.Fatalf("additional URLs = %#v, want the exact imported value preserved", result.AdditionalURLs)
	}
	for _, url := range result.AdditionalURLs {
		if strings.Contains(url, "[REDACTED]") {
			t.Fatalf("additional URL = %q, want no redaction sentinel", url)
		}
	}
}

// A request target the policy cannot project must not be silently repointed at
// "/": that both destroys the imported value and sends a request the operator
// never wrote.
func TestProjectScriptCredentialsPreservesUnprojectableRequestTarget(t *testing.T) {
	t.Parallel()
	script := validBaseScript()
	script.Actions = []dsl.Action{{
		Name:   "login",
		Type:   "POST",
		Method: "POST",
		Target: unprojectableURL,
	}}

	if _, err := ProjectScriptCredentials(script, ScriptCredentialProjectionOptions{
		SourceProfile: "test_import",
		CandidateKey:  "candidate",
		NamePrefix:    "TEST_IMPORT",
	}); err != nil {
		t.Fatalf("ProjectScriptCredentials: %v", err)
	}
	if script.Actions[0].Target != unprojectableURL {
		t.Fatalf("target = %q, want the exact imported value preserved", script.Actions[0].Target)
	}
	metadata := script.Actions[0].Metadata
	if metadata["execution_blocker"] != "credential_request_url_projection_failed" {
		t.Fatalf("metadata = %#v, want the unprojectable target to block execution", metadata)
	}
	if fields := preservedCredentialTargetFields(t, metadata); !containsFold(fields, "actions[0].target") {
		t.Fatalf("preserved credential targets = %#v, want an actions[0].target locator", fields)
	}
}

// Failing to project the provenance-only original_url metadata must not blank
// the metadata and must not repoint the executable target at "/".
func TestProjectScriptCredentialsPreservesUnprojectableOriginalURLMetadata(t *testing.T) {
	t.Parallel()
	script := validBaseScript()
	script.Actions = []dsl.Action{{
		Name:     "login",
		Type:     "POST",
		Method:   "POST",
		Target:   "/login",
		Metadata: map[string]any{"original_url": unprojectableURL},
	}}

	if _, err := ProjectScriptCredentials(script, ScriptCredentialProjectionOptions{
		SourceProfile: "test_import",
		CandidateKey:  "candidate",
		NamePrefix:    "TEST_IMPORT",
	}); err != nil {
		t.Fatalf("ProjectScriptCredentials: %v", err)
	}
	if script.Actions[0].Target != "/login" {
		t.Fatalf("target = %q, want the authored target untouched", script.Actions[0].Target)
	}
	if original, _ := script.Actions[0].Metadata["original_url"].(string); original == "[REDACTED]" {
		t.Fatalf("original_url = %q, want no redaction sentinel", original)
	}
	if script.Actions[0].Metadata["execution_blocker"] != "credential_request_url_projection_failed" {
		t.Fatalf("metadata = %#v, want the unprojectable original_url to block execution",
			script.Actions[0].Metadata)
	}
}

// An OAuth token URL the policy cannot project must keep the authored endpoint.
// Substituting https://set-token-url.invalid guarantees the token exchange
// fails at replay even after the operator resolves the gate.
func TestProjectScriptCredentialsPreservesUnprojectableOAuthTokenURL(t *testing.T) {
	t.Parallel()
	script := validBaseScript()
	script.Actions = []dsl.Action{{
		Name:   "token",
		Type:   "http_oauth2_token",
		Target: "/token",
		Params: map[string]any{
			"token_url":     unprojectableURL,
			"grant_type":    "client_credentials",
			"client_id":     "client",
			"client_secret": "secret-value",
		},
	}}

	if _, err := ProjectScriptCredentials(script, ScriptCredentialProjectionOptions{
		SourceProfile: "test_import",
		CandidateKey:  "candidate",
		NamePrefix:    "TEST_IMPORT",
	}); err != nil {
		t.Fatalf("ProjectScriptCredentials: %v", err)
	}
	tokenURL, _ := script.Actions[0].Params["token_url"].(string)
	if tokenURL != unprojectableURL {
		t.Fatalf("token_url = %q, want the exact imported value preserved", tokenURL)
	}
	if strings.Contains(tokenURL, "set-token-url.invalid") {
		t.Fatalf("token_url = %q, want no placeholder endpoint", tokenURL)
	}
}

// A credential review that cannot be completed must gate the draft, not replace
// the operator's imported request tree with a review stub.
func TestProjectScriptCredentialsGatesInsteadOfReplacingScriptOnReviewFailure(t *testing.T) {
	t.Parallel()
	const canary = "review-failure-password-canary"
	script := validBaseScript()
	script.Init = []dsl.Action{{
		Name:   "warmup",
		Type:   "GET",
		Method: "GET",
		Target: "/warmup",
	}}
	script.Actions = []dsl.Action{{
		Name:   "login",
		Type:   "POST",
		Method: "POST",
		Target: "/login",
		Body:   `{"password":"` + canary + `"}`,
	}}
	script.Teardown = []dsl.Action{{
		Name:   "logout",
		Type:   "POST",
		Method: "POST",
		Target: "/logout",
	}}

	gateScriptWithCredentialReviewFailure(
		script,
		"test_import",
		credentialTraversalSnapshot(script),
		"credential_request_review_conflict",
		"conflicting credential requirement provenance",
	)

	if script.Config.BaseURL != "https://example.test" {
		t.Fatalf("base URL = %q, want the authored base URL retained", script.Config.BaseURL)
	}
	if len(script.Init) != 1 || len(script.Actions) != 1 || len(script.Teardown) != 1 {
		t.Fatalf("script shape = init %d actions %d teardown %d, want the imported tree retained",
			len(script.Init), len(script.Actions), len(script.Teardown))
	}
	if script.Actions[0].Body != `{"password":"`+canary+`"}` {
		t.Fatalf("action body = %q, want the imported credential preserved", script.Actions[0].Body)
	}
	gated := 0
	_ = dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		if ref.Metadata()["execution_blocker"] == "credential_request_review_conflict" {
			gated++
		}
		return nil
	})
	if gated != 3 {
		t.Fatalf("gated surfaces = %d, want every top-level action execution-blocked", gated)
	}
	if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("DSL validation issues = %#v", issues)
	}
}

// Reconciling attached source parameters must not overwrite the parameter list
// or the inline data source with a redaction sentinel: those are executable
// runtime data, so "[REDACTED]" would go on the wire at replay.
func TestReconcileProtectedScriptCredentialSourcesPreservesParameterValues(t *testing.T) {
	t.Parallel()
	const canary = "parameter-source-password-canary"
	script := validBaseScript()
	script.Parameters = []dsl.Parameter{{
		Name:   "login_password",
		Type:   "list",
		Values: []string{canary},
	}}
	script.DataSources = []dsl.DataSource{{
		Name:           "creds",
		InlineData:     "password\n" + canary + "\n",
		FirstRowHeader: true,
	}}
	script.Actions = []dsl.Action{{
		Name:   "login",
		Type:   "POST",
		Method: "POST",
		Target: "/login",
		Body:   `{"password":"{{param.login_password}}","note":"{{param.login_password}}"}`,
	}}

	requirements := []credentialpolicy.Requirement{{
		ID:      "requirement-one",
		Key:     "password",
		Label:   "password",
		Context: "actions[0].body",
		Value:   "{{param.login_password}}",
		Targets: []credentialpolicy.TargetRef{{
			RequestKey: "candidate:actions[0]",
			Field:      "actions[0].body",
		}},
	}}

	ReconcileProtectedScriptCredentialSources(script, requirements, "test_import", "candidate")

	if len(script.Parameters) != 1 || len(script.Parameters[0].Values) != 1 ||
		script.Parameters[0].Values[0] != canary {
		t.Fatalf("parameters = %#v, want the imported runtime value preserved", script.Parameters)
	}
	if len(script.DataSources) != 1 || !strings.Contains(script.DataSources[0].InlineData, canary) {
		t.Fatalf("data sources = %#v, want the imported inline data preserved", script.DataSources)
	}
	if strings.Contains(script.DataSources[0].InlineData, "[REDACTED]") {
		t.Fatalf("inline data = %q, want no redaction sentinel", script.DataSources[0].InlineData)
	}
}

// preserveCredentialSourceVariables is exercised directly because the
// reconciliation pass in front of it removes the parameters that used to reach
// its redaction branch. Nothing from an imported script is a project secret, so
// a consumed parameter's values and its backing inline data must stay intact
// and only their locators may be published.
func TestPreserveCredentialSourceVariablesKeepsParameterAndDataSourceValues(t *testing.T) {
	t.Parallel()
	const canary = "consumed-parameter-password-canary"
	script := validBaseScript()
	script.Parameters = []dsl.Parameter{{
		Name:       "login_password",
		Type:       "list",
		Values:     []string{canary},
		DataSource: "creds",
		Column:     "password",
	}}
	script.DataSources = []dsl.DataSource{{
		Name:           "creds",
		InlineData:     "password\n" + canary + "\n",
		FirstRowHeader: true,
	}}
	script.Actions = []dsl.Action{{
		Name:   "login",
		Type:   "POST",
		Method: "POST",
		Target: "/login",
		Body:   `{"password":"` + canary + `"}`,
	}}

	preserveCredentialSourceVariables(script, []credentialpolicy.Requirement{{
		ID:      "requirement-one",
		Key:     "password",
		Label:   "password",
		Context: "actions[0].body",
		Value:   "{{param.login_password}}",
		Targets: []credentialpolicy.TargetRef{{
			RequestKey: "candidate:actions[0]",
			Field:      "actions[0].body",
		}},
	}})

	if len(script.Parameters[0].Values) != 1 || script.Parameters[0].Values[0] != canary {
		t.Fatalf("parameter values = %#v, want the imported runtime value preserved",
			script.Parameters[0].Values)
	}
	if !strings.Contains(script.DataSources[0].InlineData, canary) {
		t.Fatalf("inline data = %q, want the imported runtime value preserved",
			script.DataSources[0].InlineData)
	}
	fields := preservedCredentialTargetFields(t, script.Metadata.Import.Preserved)
	for _, want := range []string{"parameters[0].values", "data_sources[0].inline_data"} {
		if !containsFold(fields, want) {
			t.Fatalf("preserved credential targets = %#v, want a %q locator", fields, want)
		}
	}
}
