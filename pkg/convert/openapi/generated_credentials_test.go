package openapi

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestGeneratedOpenAPISecurityPlaceholdersHaveConfigurationRequirements(t *testing.T) {
	result, err := Convert(ImportRequest{Content: `{
  "openapi":"3.0.3",
  "info":{"title":"Generated authentication","version":"1"},
  "servers":[{"url":"https://api.example.test"}],
  "components":{"securitySchemes":{
    "bearerAuth":{"type":"http","scheme":"bearer"},
    "headerKey":{"type":"apiKey","in":"header","name":"X-API-Key"},
    "oauthClient":{"type":"oauth2","flows":{"clientCredentials":{"tokenUrl":"/oauth/token","scopes":{}}}}
  }},
  "paths":{
    "/bearer-a":{"get":{"security":[{"bearerAuth":[]}],"responses":{"200":{}}}},
    "/bearer-b":{"get":{"security":[{"bearerAuth":[]}],"responses":{"200":{}}}},
    "/key":{"get":{"security":[{"headerKey":[]}],"responses":{"200":{}}}},
    "/oauth":{"get":{"security":[{"oauthClient":[]}],"responses":{"200":{}}}}
  }
}`})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}

	requirements := result.ProtectedRequirements.Take()
	if len(requirements) != 3 {
		t.Fatalf("generated credential requirements = %#v, want bearer, API key, and OAuth client secret", requirements)
	}
	byName := make(map[string]credentialpolicy.Requirement, len(requirements))
	for _, requirement := range requirements {
		byName[requirement.SuggestedName] = requirement
		if requirement.Value != "" || requirement.SourceValueAvailable ||
			requirement.SourceValueStatus != credentialpolicy.SourceCredentialValueMissing ||
			requirement.ExistingReference {
			t.Fatalf("generated requirement was mistaken for an authored/configured value: %#v", requirement)
		}
		if requirement.Source.Format != "openapi" || requirement.Source.Path != "openapi.json" ||
			!strings.Contains(requirement.Source.Pointer, "securitySchemes") ||
			len(requirement.Targets) == 0 || requirement.Targets[0].RequestKey == "" ||
			!strings.Contains(requirement.Context, "does not contain a credential value") {
			t.Fatalf("generated requirement lacks descriptive provenance: %#v", requirement)
		}
	}
	for _, name := range []string{
		"OPENAPI_BEARERAUTH_BEARER_TOKEN",
		"OPENAPI_HEADERKEY_API_KEY",
		"OPENAPI_OAUTHCLIENT_CLIENT_SECRET",
	} {
		requirement, ok := byName[name]
		if !ok || requirement.Replacement != "{{secret."+name+"}}" {
			t.Fatalf("missing generated binding %q in %#v", name, requirements)
		}
	}
	if got := byName["OPENAPI_BEARERAUTH_BEARER_TOKEN"].Targets; len(got) != 2 ||
		got[0].Field != "actions[0].auth.token" || got[1].Field != "actions[1].auth.token" {
		t.Fatalf("bearer affected paths = %#v, want both secured operations", got)
	}
	if got := byName["OPENAPI_HEADERKEY_API_KEY"].Targets; len(got) != 1 ||
		got[0].Field != "actions[2].headers.X-API-Key" {
		t.Fatalf("API-key affected paths = %#v", got)
	}
	if got := byName["OPENAPI_OAUTHCLIENT_CLIENT_SECRET"].Targets; len(got) != 1 ||
		got[0].Field != "init[0].params.client_secret" {
		t.Fatalf("OAuth affected paths = %#v", got)
	}
}

func TestGeneratedOpenAPISecurityRequirementsScaleWithAffectedFields(t *testing.T) {
	const (
		unusedSchemes = 4096
		affected      = 1024
	)
	auth := &authContext{
		securityNames: make(map[string]string, unusedSchemes+1),
		schemes:       make(map[string]securityScheme, unusedSchemes+1),
	}
	for index := 0; index < unusedSchemes; index++ {
		scheme := fmt.Sprintf("unused_%04d", index)
		auth.securityNames[openAPISecurityNameKey(scheme, "API_KEY")] = fmt.Sprintf("UNUSED_%04d", index)
		auth.schemes[scheme] = securityScheme{Type: "apiKey", In: "header", Name: "X-Unused"}
	}
	auth.securityNames[openAPISecurityNameKey("used", "BEARER_TOKEN")] = "USED_TOKEN"
	auth.schemes["used"] = securityScheme{Type: "http", Scheme: "bearer"}
	script := &dsl.Script{Actions: make([]dsl.Action, affected)}
	for index := range script.Actions {
		script.Actions[index] = dsl.Action{
			Name: fmt.Sprintf("request-%04d", index),
			Auth: &dsl.AuthConfig{Type: "bearer", Token: "{{secret.USED_TOKEN}}"},
			Metadata: map[string]any{"openapi": map[string]any{
				"security_schemes": []string{"used"},
			}},
		}
	}

	requirements, err := openAPIGeneratedSecurityRequirements(script, auth, "large-openapi.json")
	if err != nil {
		t.Fatalf("openAPIGeneratedSecurityRequirements returned error: %v", err)
	}
	if len(requirements) != 1 {
		t.Fatalf("requirements = %#v, want one used credential", requirements)
	}
	if len(requirements[0].Targets) != affected {
		t.Fatalf("targets = %d, want %d", len(requirements[0].Targets), affected)
	}
	if requirements[0].Targets[affected-1].Field != fmt.Sprintf("actions[%d].auth.token", affected-1) {
		t.Fatalf("last affected path = %#v", requirements[0].Targets[affected-1])
	}
}

func TestGeneratedOpenAPISecurityRequirementIgnoresAuthoredNameCollisionOnUnusedScheme(t *testing.T) {
	auth := &authContext{
		securityNames: map[string]string{
			openAPISecurityNameKey("unused", "BEARER_TOKEN"): "OPENAPI_UNUSED_BEARER_TOKEN",
		},
		schemes: map[string]securityScheme{
			"unused": {Type: "http", Scheme: "bearer"},
		},
	}
	script := &dsl.Script{Actions: []dsl.Action{{
		Name: "authored body reference",
		Body: `{"token":"{{secret.OPENAPI_UNUSED_BEARER_TOKEN}}"}`,
	}}}
	requirements, err := openAPIGeneratedSecurityRequirements(script, auth, "source.json")
	if err != nil {
		t.Fatalf("openAPIGeneratedSecurityRequirements returned error: %v", err)
	}
	if len(requirements) != 0 {
		t.Fatalf("authored reference was misattributed to an unused generated scheme: %#v", requirements)
	}
}
