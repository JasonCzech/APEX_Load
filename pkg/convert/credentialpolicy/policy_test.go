package credentialpolicy

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/credential"
)

func TestNewRequirementProjectionModes(t *testing.T) {
	t.Parallel()
	base := RequirementSpec{
		Key:      "authorization",
		Label:    "Authorization",
		Context:  "request header",
		NameHint: "orders authorization",
		Source:   SourceRef{Format: "fixture", Path: "requests/orders.json", Pointer: "/headers/0"},
		Targets:  []TargetRef{{RequestKey: "orders", Field: "header", Index: 0}},
		Value:    "Bearer source-secret",
	}
	tests := []struct {
		mode            ProjectionMode
		wantProjected   string
		wantReplacement string
	}{
		{Audit, base.Value, ""},
		{Redact, credential.Redacted, credential.Redacted},
		{SecretReference, "{{secret.ORDERS_AUTHORIZATION}}", "{{secret.ORDERS_AUTHORIZATION}}"},
	}
	for _, test := range tests {
		test := test
		t.Run(string(test.mode), func(t *testing.T) {
			t.Parallel()
			spec := base
			spec.Mode = test.mode
			requirement, projected, err := NewRequirement(spec)
			if err != nil {
				t.Fatal(err)
			}
			if projected != test.wantProjected || requirement.Replacement != test.wantReplacement {
				t.Fatalf("projection = %q / %q, want %q / %q", projected, requirement.Replacement, test.wantProjected, test.wantReplacement)
			}
			if !requirement.SourceValueAvailable || requirement.Value != base.Value {
				t.Fatalf("source migration fields = %#v", requirement)
			}
		})
	}
}

func TestRequirementIDDoesNotDependOnSourceValue(t *testing.T) {
	t.Parallel()
	spec := RequirementSpec{
		Key:      "password",
		Label:    "Password",
		Source:   SourceRef{Format: "fixture", Path: "request.json", Pointer: "/body/password"},
		Targets:  []TargetRef{{RequestKey: "login", Field: "body", Pointer: "/password"}},
		Mode:     SecretReference,
		NameHint: "login password",
	}
	spec.Value = "first-secret"
	first, _, err := NewRequirement(spec)
	if err != nil {
		t.Fatal(err)
	}
	spec.Value = "rotated-secret"
	second, _, err := NewRequirement(spec)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || first.SuggestedName != second.SuggestedName {
		t.Fatalf("value rotation changed stable identity: %#v vs %#v", first, second)
	}
	if strings.Contains(first.ID, "first") || strings.Contains(second.ID, "rotated") {
		t.Fatal("stable IDs must not expose source values")
	}
}

func TestRequirementTargetNormalizationAndAliases(t *testing.T) {
	t.Parallel()
	spec := RequirementSpec{
		Key:      "authorization",
		Label:    "Authorization",
		NameHint: "authorization",
		Mode:     Redact,
		Targets: []TargetRef{
			{RequestKey: "one", Field: "headers", Pointer: "/Authorization"},
			{RequestKey: "one", Field: "header_list", Index: 1},
			{RequestKey: "one", Field: "headers", Pointer: "/Authorization"},
		},
		Value: "secret",
	}
	requirement, _, err := NewRequirement(spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(requirement.Targets) != 2 {
		t.Fatalf("targets = %#v, want two unique aliases", requirement.Targets)
	}
	if requirement.Targets[0].Field != "header_list" || requirement.Targets[1].Field != "headers" {
		t.Fatalf("targets are not deterministic: %#v", requirement.Targets)
	}
}

func TestExistingReferenceIsNonMigratable(t *testing.T) {
	t.Parallel()
	for _, mode := range []ProjectionMode{Audit, Redact, SecretReference} {
		mode := mode
		t.Run(string(mode), func(t *testing.T) {
			t.Parallel()
			requirement, projected, err := NewRequirement(RequirementSpec{
				Key:               "token",
				Label:             "Token",
				Mode:              mode,
				Targets:           []TargetRef{{Field: "auth.token"}},
				Value:             "{{secret.existing-token}}",
				ExistingReference: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			if projected != "{{secret.EXISTING_TOKEN}}" ||
				requirement.Replacement != "{{secret.EXISTING_TOKEN}}" ||
				requirement.SuggestedName != "EXISTING_TOKEN" {
				t.Fatalf("existing reference projection = %q, requirement %#v", projected, requirement)
			}
			if requirement.SourceValueAvailable || requirement.Value != "" || !requirement.ExistingReference {
				t.Fatalf("existing reference retained migration material: %#v", requirement)
			}
		})
	}
}

func TestRequirementJSONNeverIncludesSourceValue(t *testing.T) {
	t.Parallel()
	requirement, _, err := NewRequirement(RequirementSpec{
		Key:      "password",
		Label:    "Password",
		NameHint: "password",
		Mode:     Redact,
		Targets:  []TargetRef{{Field: "body", Pointer: "/password"}},
		Value:    "must-never-serialize",
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(requirement)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), requirement.Value) {
		t.Fatalf("serialized requirement leaked source value: %s", payload)
	}
}

func TestNameRegistryReservesAndDisambiguatesDeterministically(t *testing.T) {
	t.Parallel()
	registry := NewNameRegistry("API_TOKEN")
	first := registry.Allocate("api-token", "requests/one")
	if first == "API_TOKEN" || !strings.HasPrefix(first, "API_TOKEN_") {
		t.Fatalf("first collision allocation = %q", first)
	}

	again := NewNameRegistry("API_TOKEN").Allocate("api-token", "requests/one")
	if again != first {
		t.Fatalf("allocation is not deterministic: %q vs %q", first, again)
	}

	second := registry.Allocate("api-token", "requests/two")
	if second == first || !strings.HasPrefix(second, "API_TOKEN_") {
		t.Fatalf("second collision allocation = %q", second)
	}
	if duplicate := registry.Allocate("api-token", "requests/one"); duplicate != first {
		t.Fatalf("same logical allocation changed from %q to %q", first, duplicate)
	}
}

func TestProtectedRequirementsIsDetachedAndOneShot(t *testing.T) {
	t.Parallel()
	source := []Requirement{{
		ID:      "requirement-1",
		Value:   "protected-source-value",
		Targets: []TargetRef{{Field: "body", Pointer: "/password"}},
	}}
	var protected ProtectedRequirements
	protected.Store(source)
	source[0].Value = "mutated-after-store"
	source[0].Targets[0].Pointer = "/changed"

	first := protected.Take()
	if len(first) != 1 || first[0].Value != "protected-source-value" ||
		first[0].Targets[0].Pointer != "/password" {
		t.Fatalf("protected handoff = %#v", first)
	}
	first[0].Value = "mutated-after-take"
	if second := protected.Take(); second != nil {
		t.Fatalf("second take = %#v, want nil", second)
	}
	if len(protected.requirements) != 0 {
		t.Fatal("protected handoff retained requirements after take")
	}
}

func TestNewRequirementRejectsInvalidInputs(t *testing.T) {
	t.Parallel()
	if _, _, err := NewRequirement(RequirementSpec{Mode: "unknown", Targets: []TargetRef{{Field: "body"}}}); err == nil {
		t.Fatal("unknown mode unexpectedly accepted")
	}
	if _, _, err := NewRequirement(RequirementSpec{Mode: Redact}); err == nil {
		t.Fatal("missing target unexpectedly accepted")
	}
	if _, _, err := NewRequirement(RequirementSpec{Mode: Redact, Targets: []TargetRef{{Field: "", Index: -1}}}); err == nil {
		t.Fatal("invalid target unexpectedly accepted")
	}
	if _, _, err := NewRequirement(RequirementSpec{
		Mode:              Redact,
		Targets:           []TargetRef{{Field: "auth.token"}},
		Value:             "not-a-reference",
		ExistingReference: true,
	}); err == nil {
		t.Fatal("invalid existing reference unexpectedly accepted")
	}
}
