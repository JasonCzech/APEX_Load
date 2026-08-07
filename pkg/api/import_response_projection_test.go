package api

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// importProjectionFixtureScript builds a script with enough parameterizable
// literals that the paramize trust pass has real work to do.
func importProjectionFixtureScript(index int) *dsl.Script {
	script := &dsl.Script{
		Name:     fmt.Sprintf("projection fixture %d", index),
		Version:  "1",
		Protocol: "http",
		Config: dsl.ScriptConfig{HTTP: dsl.HTTPConfig{
			Auth: dsl.AuthConfig{
				Type:     "basic",
				Username: "operator",
				Password: fmt.Sprintf("imported-password-%d", index),
			},
		}},
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Source: "jmeter"}},
	}
	for action := 0; action < 12; action++ {
		script.Actions = append(script.Actions, dsl.Action{
			Name:   fmt.Sprintf("request-%d-%d", index, action),
			Type:   "request",
			Method: "POST",
			Target: fmt.Sprintf("https://api.example.com/v1/orders/%d", action),
			Headers: map[string]string{
				"Content-Type": "application/json",
				"X-Tenant":     fmt.Sprintf("tenant-%d", index),
			},
			Body: fmt.Sprintf(`{"customer_id":"cust-%d-%d","email":"user%d@example.com"}`, index, action, action),
		})
	}
	return script
}

// importProjectionFixtureParameterization builds the fixture parameterization
// with the *production* multi-script builder, not a hand-rolled copy of it. That
// matters because buildMultiScriptParameterization stamps Suggestion.ScriptName
// (pkg/api/import_paramize.go), which is what routes every suggestion through
// the script-scoped branch of the trust filter rather than the global-ID branch.
// A local copy that skipped that step left the scoped branch asserted only in
// its reject direction, so a bug that emptied the scoped map went unnoticed.
func importProjectionFixtureParameterization(scripts []*dsl.Script) *paramize.Result {
	// Clone deeply enough that the builder's in-place action-name deduplication
	// cannot reach the caller's fixture scripts.
	clones := make([]*dsl.Script, 0, len(scripts))
	for _, script := range scripts {
		clone := *script
		clone.Init = append([]dsl.Action(nil), script.Init...)
		clone.Actions = append([]dsl.Action(nil), script.Actions...)
		clone.Teardown = append([]dsl.Action(nil), script.Teardown...)
		clones = append(clones, &clone)
	}
	return buildMultiScriptParameterization(clones)
}

// assertImportProjectionFixtureSuggestionsScoped fails if the fixture stopped
// carrying per-script suggestion scoping, which would silently move the trust
// filter back onto its global-ID branch and un-cover the scoped one.
func assertImportProjectionFixtureSuggestionsScoped(t *testing.T, result *paramize.Result) {
	t.Helper()
	if len(result.Suggestions) == 0 {
		t.Fatalf("fixture parameterization produced no suggestions")
	}
	for _, suggestion := range result.Suggestions {
		if suggestion.ScriptName == "" {
			t.Fatalf("fixture suggestion %q has no ScriptName, so the scoped trust branch is not exercised", suggestion.ID)
		}
	}
}

// assertImportProjectionFixtureScripts checks recovered scripts against the
// fixture spec directly (names, action counts, projected credential) instead of
// against another run of the same projection helper, so the assertion fails if
// either path loses actions or stops projecting.
func assertImportProjectionFixtureScripts(t *testing.T, label string, recovered []*dsl.Script) {
	t.Helper()
	if len(recovered) != 2 {
		t.Fatalf("%s: recovered %d scripts, want 2", label, len(recovered))
	}
	for index, script := range recovered {
		wantName := fmt.Sprintf("projection fixture %d", index)
		if script.Name != wantName {
			t.Fatalf("%s: script %d name = %q, want %q", label, index, script.Name, wantName)
		}
		if len(script.Actions) != 12 {
			t.Fatalf("%s: script %d has %d actions, want 12", label, index, len(script.Actions))
		}
		if got := script.Config.HTTP.Auth.Password; got != credential.Redacted {
			t.Fatalf("%s: script %d auth password = %q, want %q", label, index, got, credential.Redacted)
		}
	}
}

// TestImportParameterizationScriptsReusesPreProjectedTree pins the contract the
// import-response builder now relies on: handing importParameterizationScripts a
// value that has already been through maskScriptResponseValue must yield exactly
// the same scripts as handing it the raw tree and letting it project, and the
// wrapped value must be consumed verbatim rather than projected a second time.
func TestImportParameterizationScriptsReusesPreProjectedTree(t *testing.T) {
	scripts := []*dsl.Script{importProjectionFixtureScript(0), importProjectionFixtureScript(1)}
	raw := credentialPreservingImportResponseValue(scripts)
	projection := maskScriptResponseValue(raw)

	fromRaw := importParameterizationScripts(raw)
	fromProjection := importParameterizationScripts(projectedImportScripts{value: projection})

	assertImportProjectionFixtureScripts(t, "raw path", fromRaw)
	assertImportProjectionFixtureScripts(t, "pre-projected path", fromProjection)

	rawJSON, err := json.Marshal(fromRaw)
	if err != nil {
		t.Fatalf("marshal raw-path scripts: %v", err)
	}
	projectedJSON, err := json.Marshal(fromProjection)
	if err != nil {
		t.Fatalf("marshal projected-path scripts: %v", err)
	}
	if string(rawJSON) != string(projectedJSON) {
		t.Fatalf("pre-projected path diverged from raw path:\n raw = %s\nproj = %s", rawJSON, projectedJSON)
	}

	// The wrapper means "already projected, use as-is" — the whole point of the
	// optimization. Feeding it an unprojected tree must therefore come back with
	// the raw credential intact; if it comes back redacted, the reuse branch is
	// silently re-projecting and the projection cost was never removed.
	fromWrappedRaw := importParameterizationScripts(projectedImportScripts{value: raw})
	if len(fromWrappedRaw) != 2 {
		t.Fatalf("wrapped raw tree recovered %d scripts, want 2", len(fromWrappedRaw))
	}
	for index, script := range fromWrappedRaw {
		want := fmt.Sprintf("imported-password-%d", index)
		if got := script.Config.HTTP.Auth.Password; got != want {
			t.Fatalf(
				"wrapped value was re-projected instead of reused: script %d auth password = %q, want %q",
				index, got, want,
			)
		}
	}
}

// TestSanitizeImportParameterizationPreProjectedMatchesRaw covers the same
// equivalence one level up, at the trust-filtering boundary the response builder
// actually calls. The fixture carries a forged suggestion so the trust filter is
// actually load-bearing on both paths: a path that recovers no scripts fails
// open and lets the forgery through.
func TestSanitizeImportParameterizationPreProjectedMatchesRaw(t *testing.T) {
	scripts := []*dsl.Script{importProjectionFixtureScript(0), importProjectionFixtureScript(1)}
	raw := credentialPreservingImportResponseValue(scripts)
	projection := maskScriptResponseValue(raw)

	assertImportProjectionFixtureSuggestionsScoped(t, importProjectionFixtureParameterization(scripts))

	input := func() *paramize.Result {
		result := importProjectionFixtureParameterization(scripts)
		result.Suggestions = append(result.Suggestions, paramize.Suggestion{
			ID:           "client-forged-suggestion",
			Kind:         paramize.KindDataParam,
			LiteralValue: "forged",
			ScriptName:   scripts[0].Name,
		})
		return result
	}
	untrustedInput := input()

	fromRaw := sanitizeImportParameterization(input(), raw)
	fromProjection := sanitizeImportParameterization(
		input(),
		projectedImportScripts{value: projection},
	)

	for label, sanitized := range map[string]*paramize.Result{
		"raw path":           fromRaw,
		"pre-projected path": fromProjection,
	} {
		if len(sanitized.Suggestions) == 0 {
			t.Fatalf("%s: trust filter dropped every suggestion", label)
		}
		if len(sanitized.Suggestions) >= len(untrustedInput.Suggestions) {
			t.Fatalf(
				"%s: kept %d of %d suggestions, so the trust filter dropped nothing",
				label, len(sanitized.Suggestions), len(untrustedInput.Suggestions),
			)
		}
		for _, suggestion := range sanitized.Suggestions {
			if suggestion.ID == "client-forged-suggestion" {
				t.Fatalf("%s: untrusted suggestion survived the trust filter: %#v", label, suggestion)
			}
		}
	}

	if !reflect.DeepEqual(fromRaw, fromProjection) {
		t.Fatalf("pre-projected sanitization diverged:\n raw = %#v\nproj = %#v", fromRaw, fromProjection)
	}
}

// TestNewScriptImportResponseDoesNotMutateSharedProjection guards the aliasing
// hazard the reuse introduces: the projection handed to the parameterization
// pass is also the one the secret-binding discovery walked, so neither may
// mutate it.
func TestNewScriptImportResponseDoesNotMutateSharedProjection(t *testing.T) {
	scripts := []*dsl.Script{importProjectionFixtureScript(0)}
	raw := credentialPreservingImportResponseValue(scripts)
	projection := maskScriptResponseValue(raw)
	before, err := json.Marshal(projection)
	if err != nil {
		t.Fatalf("marshal projection: %v", err)
	}

	requirements := secretBindingRequirementsForImportScripts(projection)
	_ = annotateSecretBindingDestinationValues(requirements, raw, projection)
	_ = importParameterizationScripts(projectedImportScripts{value: projection})

	after, err := json.Marshal(projection)
	if err != nil {
		t.Fatalf("re-marshal projection: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("shared projection was mutated:\nbefore = %s\n after = %s", before, after)
	}
}

// TestNewScriptImportResponseParameterizationStillTrustFiltered makes sure the
// reuse did not silently disable the trust filter (which would show up as
// suggestions surviving that the projected script cannot vouch for).
func TestNewScriptImportResponseParameterizationStillTrustFiltered(t *testing.T) {
	scripts := []*dsl.Script{importProjectionFixtureScript(0)}
	parameterization := importProjectionFixtureParameterization(scripts)
	assertImportProjectionFixtureSuggestionsScoped(t, parameterization)
	legitimate := len(parameterization.Suggestions)
	parameterization.Suggestions = append(parameterization.Suggestions, paramize.Suggestion{
		ID:           "client-forged-suggestion",
		Kind:         paramize.KindDataParam,
		LiteralValue: "forged",
		ScriptName:   scripts[0].Name,
	})

	response := newScriptImportResponse(scriptImportResponseOptions{
		Script:           scripts[0],
		Scripts:          scripts,
		Parameterization: parameterization,
	})
	sanitized, ok := response["parameterization"].(*paramize.Result)
	if !ok {
		t.Fatalf("parameterization = %#v, want *paramize.Result", response["parameterization"])
	}
	for _, suggestion := range sanitized.Suggestions {
		if suggestion.ID == "client-forged-suggestion" {
			t.Fatalf("untrusted suggestion survived the trust filter: %#v", suggestion)
		}
	}
	// Positive direction: the filter must still vouch for the legitimate,
	// script-scoped suggestions. Without this, a regression that empties the
	// scoped trust map (rendering an empty Parameterization review panel) would
	// pass, because dropping everything also drops the forgery.
	if len(sanitized.Suggestions) == 0 {
		t.Fatalf("trust filter dropped every suggestion; %d legitimate suggestions should have survived", legitimate)
	}
	if len(sanitized.Suggestions) > legitimate {
		t.Fatalf("kept %d suggestions, want at most the %d legitimate ones", len(sanitized.Suggestions), legitimate)
	}
}

func BenchmarkNewScriptImportResponseMultiScript(b *testing.B) {
	scripts := make([]*dsl.Script, 0, 8)
	for index := 0; index < 8; index++ {
		scripts = append(scripts, importProjectionFixtureScript(index))
	}
	parameterization := importProjectionFixtureParameterization(scripts)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = newScriptImportResponse(scriptImportResponseOptions{
			Script:           scripts[0],
			Scripts:          scripts,
			Parameterization: parameterization,
		})
	}
}
