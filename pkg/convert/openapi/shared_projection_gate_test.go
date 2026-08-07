package openapi

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// openAPIUnsafeSharedProjectionGateCodes restates the expected contents of
// openAPIUnsafeSharedProjectionCodes independently of the production set. Every
// test below iterates this literal rather than ranging over the map under test,
// so dropping a live gate code fails the suite instead of silently deleting its
// own coverage.
var openAPIUnsafeSharedProjectionGateCodes = []string{
	"sensitive_non_string_schema",
	"malformed_authored_example",
}

// openAPIUnsafeSharedProjectionFixtures pins one converter input per entry of
// openAPIUnsafeSharedProjectionCodes. The reachability test below fails when a
// code has no fixture, which is what keeps unreachable ("dead") review codes
// from accumulating in the projection gate again.
var openAPIUnsafeSharedProjectionFixtures = map[string]struct {
	content string
	action  string
	// pathCredentialParameter names a governed path parameter the fixture
	// declares, if any. The OpenAPI adapter attaches its own "url.path"
	// credential locator for such a parameter while building the action, long
	// before the projection gate is consulted, so a gated action legitimately
	// carries that locator. The reachability test tolerates exactly those
	// locators and requires this one to be present, which keeps the tolerated
	// exception exercised instead of merely assumed.
	pathCredentialParameter string
}{
	"sensitive_non_string_schema": {
		action:                  "createSession",
		pathCredentialParameter: "api_key",
		content: `{
  "openapi":"3.0.3",
  "info":{"title":"Typed credential schema","version":"1"},
  "servers":[{"url":"https://api.example.test"}],
  "paths":{"/accounts/{api_key}/sessions":{"post":{
    "operationId":"createSession",
    "parameters":[{"name":"api_key","in":"path","required":true,"schema":{"type":"string"},"example":"path-key-4412"}],
    "requestBody":{"content":{"application/json":{
      "schema":{"type":"object","properties":{"password":{"type":"integer"}}}
    }}},
    "responses":{"204":{"description":"ok"}}
  }}}
}`,
	},
	"malformed_authored_example": {
		action: "createReport",
		content: `openapi: 3.0.3
info:
  title: Malformed authored example
  version: "1"
servers:
  - url: https://api.example.test
paths:
  /reports:
    post:
      operationId: createReport
      requestBody:
        content:
          application/json:
            example:
              ratio: .nan
      responses:
        "204":
          description: ok
`,
	},
}

// openAPIUngatedProjectionFixture is the control for the gate: the same shape as
// the sensitive_non_string_schema fixture, except the credential is a plain
// string whose authored example is the faithful wire value. It must NOT be
// gated, and the converter must therefore attach shared credential projection
// to it.
const openAPIUngatedProjectionFixture = `{
  "openapi":"3.0.3",
  "info":{"title":"String credential schema","version":"1"},
  "servers":[{"url":"https://api.example.test"}],
  "paths":{"/sessions":{"post":{
    "operationId":"createSession",
    "requestBody":{"content":{"application/json":{
      "schema":{"type":"object","properties":{"password":{"type":"string","example":"session-pass-7319"}}}
    }}},
    "responses":{"204":{"description":"ok"}}
  }}}
}`

// TestOpenAPIUnsafeSharedProjectionCodesMatchExpectation pins the gate set in
// both directions. Ranging over openAPIUnsafeSharedProjectionCodes can only
// catch extra entries; the direction that actually loses credential-safety
// behaviour is a deleted entry, which this equality check catches.
func TestOpenAPIUnsafeSharedProjectionCodesMatchExpectation(t *testing.T) {
	for _, code := range openAPIUnsafeSharedProjectionGateCodes {
		if _, ok := openAPIUnsafeSharedProjectionCodes[code]; !ok {
			t.Errorf("gate code %q is missing from openAPIUnsafeSharedProjectionCodes: removing it re-enables shared credential projection over a draft body that is not the faithful wire payload", code)
		}
		if _, ok := openAPIUnsafeSharedProjectionFixtures[code]; !ok {
			t.Errorf("gate code %q has no converter fixture in openAPIUnsafeSharedProjectionFixtures", code)
		}
	}
	for code := range openAPIUnsafeSharedProjectionCodes {
		if !containsString(openAPIUnsafeSharedProjectionGateCodes, code) {
			t.Errorf("gate code %q is not declared in openAPIUnsafeSharedProjectionGateCodes: add it there with a converter fixture, or drop it from the gate", code)
		}
	}
}

// openAPIAdapterPathCredentialPointerPrefixes lists the pointer schemes the
// OpenAPI adapter stamps on the path-parameter credential locators it attaches
// itself (buildOpenAPIPathCredentialTargets and its unresolved fallback). The
// shared projector never emits either scheme, so a locator carrying one of them
// is provably adapter-owned rather than shared-projection output.
var openAPIAdapterPathCredentialPointerPrefixes = []string{
	openAPIPathCredentialPointerPrefix,
	"openapi-path-unresolved:",
}

// openAPIAdapterOwnedPathCredentialLocator reports whether a preserved locator
// is one the adapter attached for a governed path parameter before the
// projection gate is consulted.
func openAPIAdapterOwnedPathCredentialLocator(target credentialpolicy.TargetRef) bool {
	if target.Field != "url.path" {
		return false
	}
	for _, prefix := range openAPIAdapterPathCredentialPointerPrefixes {
		if strings.HasPrefix(target.Pointer, prefix) {
			return true
		}
	}
	return false
}

// openAPIPreservedCredentialLocators decodes the disclosure-safe redaction
// locators an action carries. It fails rather than returning an empty slice
// when the metadata is present but does not decode, because a silently empty
// decode would make every locator assertion below pass vacuously.
func openAPIPreservedCredentialLocators(t *testing.T, action dsl.Action) []credentialpolicy.TargetRef {
	t.Helper()
	raw, ok := action.Metadata[dsl.ImportPreservedCredentialTargetsMetadataKey]
	if !ok || raw == nil {
		return nil
	}
	payload, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal %s metadata %#v: %v", dsl.ImportPreservedCredentialTargetsMetadataKey, raw, err)
	}
	var targets []credentialpolicy.TargetRef
	if err := json.Unmarshal(payload, &targets); err != nil {
		t.Fatalf("decode %s metadata %s: %v", dsl.ImportPreservedCredentialTargetsMetadataKey, payload, err)
	}
	if len(targets) == 0 {
		t.Fatalf(
			"%s metadata %#v decoded to zero locators: the locator assertions would pass vacuously",
			dsl.ImportPreservedCredentialTargetsMetadataKey,
			raw,
		)
	}
	return targets
}

// TestOpenAPIUnsafeSharedProjectionCodesAreReachable proves every code that
// disables shared credential projection is actually produced by the converter,
// and that the production call site honours the gate: a gated action must carry
// no credential locator produced by the shared projector (body, query, header
// or auth locators). It may still carry the "url.path" locators the adapter
// attaches itself for a governed path parameter: openAPIPathCredentialTargets
// merges those into the action's metadata while the action is being built,
// unconditionally and long before openAPISharedCredentialProjectionSafe is
// consulted, so they are not shared projection and their presence is not a gate
// violation. The fixture that declares such a parameter requires its locator to
// be present, which keeps that tolerated case exercised.
func TestOpenAPIUnsafeSharedProjectionCodesAreReachable(t *testing.T) {
	for _, code := range openAPIUnsafeSharedProjectionGateCodes {
		fixture, ok := openAPIUnsafeSharedProjectionFixtures[code]
		if !ok {
			t.Fatalf("projection gate code %q has no converter fixture: either the code is unreachable and must be removed from openAPIUnsafeSharedProjectionCodes, or a fixture that produces it must be added", code)
		}
		result, err := Convert(ImportRequest{Content: fixture.content})
		if err != nil {
			t.Fatalf("Convert for %q returned error: %v", code, err)
		}
		action := openAPIActionFull(result, fixture.action)
		if action.Name == "" {
			t.Fatalf("Convert for %q produced no action named %q", code, fixture.action)
		}
		review, ok := action.Metadata["import_review"].(map[string]any)
		if !ok {
			t.Fatalf("import review for %q = %#v", code, action.Metadata["import_review"])
		}
		if reasons := stringSlice(review["reason_codes"]); !containsString(reasons, code) {
			t.Fatalf("reason codes for %q = %#v", code, review["reason_codes"])
		}
		if openAPISharedCredentialProjectionSafe(action) {
			t.Fatalf("shared credential projection stayed enabled for %q", code)
		}
		locators := openAPIPreservedCredentialLocators(t, action)
		for _, locator := range locators {
			if !openAPIAdapterOwnedPathCredentialLocator(locator) {
				t.Fatalf(
					"gated action for %q carries shared credential projection locator %+v: the converter attached credential projection to a body that is not the faithful wire payload",
					code,
					locator,
				)
			}
		}
		if fixture.pathCredentialParameter == "" {
			if len(locators) > 0 {
				t.Fatalf(
					"gated action for %q carries path credential locators %+v, but its fixture declares no governed path parameter",
					code,
					locators,
				)
			}
			continue
		}
		encodedParameter := base64.RawURLEncoding.EncodeToString([]byte(fixture.pathCredentialParameter))
		matched := false
		for _, locator := range locators {
			if strings.Contains(locator.Pointer, encodedParameter) {
				matched = true
				break
			}
		}
		if !matched {
			t.Fatalf(
				"gated action for %q carries path credential locators %+v, want one for path parameter %q: the adapter-owned locator this assertion tolerates is no longer exercised, so it no longer proves the gate is distinguished from shared projection",
				code,
				locators,
				fixture.pathCredentialParameter,
			)
		}
	}
}

// TestOpenAPISharedCredentialProjectionRunsWhenUngated is the other half of the
// call-site pin: without it, the "no locators" assertions above would also hold
// if the converter never attached shared credential projection at all.
func TestOpenAPISharedCredentialProjectionRunsWhenUngated(t *testing.T) {
	result, err := Convert(ImportRequest{Content: openAPIUngatedProjectionFixture})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := openAPIActionFull(result, "createSession")
	if action.Name == "" {
		t.Fatalf("Convert produced no action named %q", "createSession")
	}
	if review, ok := action.Metadata["import_review"]; ok {
		t.Fatalf("control fixture was gated by import review %#v; it must exercise the ungated path", review)
	}
	targets, ok := action.Metadata[dsl.ImportPreservedCredentialTargetsMetadataKey]
	if !ok {
		t.Fatalf(
			"ungated action carries no %s metadata: the converter no longer attaches shared credential projection, so the gated assertions prove nothing",
			dsl.ImportPreservedCredentialTargetsMetadataKey,
		)
	}
	rendered := fmt.Sprintf("%v", targets)
	for _, want := range []string{"POST /sessions", "/password"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("ungated credential locators = %s, want a locator mentioning %q", rendered, want)
		}
	}
}

func TestOpenAPISharedCredentialProjectionSafeGating(t *testing.T) {
	gated := func(codes ...string) dsl.Action {
		return dsl.Action{Metadata: map[string]any{
			"import_review": map[string]any{"reason_codes": codes},
		}}
	}
	for _, code := range openAPIUnsafeSharedProjectionGateCodes {
		if openAPISharedCredentialProjectionSafe(gated(code)) {
			t.Fatalf("projection reported safe for gate code %q", code)
		}
		if openAPISharedCredentialProjectionSafe(gated("duplicate_media_example_name", code)) {
			t.Fatalf("projection reported safe for gate code %q alongside an unrelated code", code)
		}
	}
	if !openAPISharedCredentialProjectionSafe(dsl.Action{}) {
		t.Fatalf("projection reported unsafe for an action without review metadata")
	}
	if !openAPISharedCredentialProjectionSafe(gated("duplicate_media_example_name")) {
		t.Fatalf("projection reported unsafe for a review code that does not damage the drafted body")
	}
}
