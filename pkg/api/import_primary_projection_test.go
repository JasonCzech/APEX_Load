package api

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestPrimaryImportScriptProjectionOnlyReusesExactMember(t *testing.T) {
	exact := []any{map[string]any{"name": "first", "body": "credential-canary"}, map[string]any{"name": "second"}}
	projected := []any{map[string]any{"name": "first", "body": "[REDACTED]"}, map[string]any{"name": "second"}}
	for _, tc := range []struct {
		name      string
		primary   any
		projected any
		want      any
		reused    bool
	}{
		{"equal detached member", map[string]any{"name": "first", "body": "credential-canary"}, projected, projected[0], true},
		{"later member", exact[1], projected, projected[1], true},
		{"same name changed source", map[string]any{"name": "first", "body": "other-canary"}, projected, nil, false},
		{"nil primary", nil, projected, nil, true},
		{"mismatched projection shape", exact[0], []any{}, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, reused := primaryImportScriptProjection(exact, tc.projected, tc.primary)
			if reused != tc.reused || !reflect.DeepEqual(got, tc.want) {
				t.Fatal("primary projection reused a different source or failed to reuse its exact member")
			}
		})
	}
}

func TestPrivateImportResponseProjectionPreservesSourceCredentialDiscovery(t *testing.T) {
	first, second := importProjectionFixtureScript(0), importProjectionFixtureScript(1)
	for _, opts := range []scriptImportResponseOptions{
		{Script: first},
		{Script: first, Scripts: []*dsl.Script{first}},
		{Script: second, Scripts: []*dsl.Script{first, second}},
	} {
		response, projection := newScriptImportResponseWithProjection(opts)
		requirements, _ := response["secret_binding_requirements"].([]SecretBindingRequirement)
		got := sourceSecretValuesForImportResponseWithProjection(opts, requirements, projection)
		want := sourceSecretValuesForImportResponse(opts, requirements)
		if len(want) == 0 || !reflect.DeepEqual(got, want) {
			t.Fatal("private projection reuse changed source credential recovery")
		}
		if _, exposed := response["private_projection"]; exposed {
			t.Fatal("private disclosure projection was included in the response")
		}
	}
}

func TestCanonicalImportDraftStructureDigestReusesDetachedProjection(t *testing.T) {
	script := importProjectionFixtureScript(0)
	projected := maskScriptSecrets(script)
	before, err := json.Marshal(projected)
	if err != nil {
		t.Fatal(err)
	}
	got, err := canonicalImportDraftStructureDigestFromProjection(projected)
	if err != nil {
		t.Fatal(err)
	}
	want, err := canonicalImportDraftStructureDigest(script)
	if err != nil || got != want {
		t.Fatal("reused projection changed the canonical attestation digest")
	}
	after, err := json.Marshal(projected)
	if err != nil || string(before) != string(after) {
		t.Fatal("canonicalization mutated its shared projection")
	}
	if _, err := canonicalImportDraftStructureDigestFromProjection(nil); err == nil {
		t.Fatal("missing disclosure projection did not fail closed")
	}
}
