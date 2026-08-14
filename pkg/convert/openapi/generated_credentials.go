package openapi

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
)

// openAPIGeneratedSecurityRequirements distinguishes converter-generated
// placeholders from secret references authored by the source document. The
// shared Preserve projector intentionally treats an authored {{secret.*}}
// reference as already governed; OpenAPI security schemes, however, describe
// only the authentication shape and never supply the generated credential.
func openAPIGeneratedSecurityRequirements(
	script *dsl.Script,
	auth *authContext,
	sourceFile string,
) ([]credentialpolicy.Requirement, error) {
	if script == nil || auth == nil || len(auth.securityNames) == 0 {
		return nil, nil
	}
	targetsByName, err := openAPIGeneratedSecretTargets(script, auth)
	if err != nil {
		return nil, err
	}
	requirements := make([]credentialpolicy.Requirement, 0)
	for _, key := range sortedMapKeys(auth.securityNames) {
		name := auth.securityNames[key]
		reference := "{{secret." + name + "}}"
		targets := targetsByName[name]
		if len(targets) == 0 {
			continue
		}
		schemeName, suffix, ok := strings.Cut(key, "\x00")
		if !ok || strings.TrimSpace(schemeName) == "" || strings.TrimSpace(suffix) == "" {
			continue
		}
		label, credentialKind := openAPIGeneratedCredentialPresentation(
			schemeName,
			suffix,
		)
		pointerRoot := "/components/securitySchemes/"
		if auth.resolver.doc != nil && strings.TrimSpace(auth.resolver.doc.Swagger) != "" {
			pointerRoot = "/securityDefinitions/"
		}
		source := credentialpolicy.SourceRef{
			Format:  "openapi",
			Path:    sourceFile,
			Pointer: pointerRoot + openAPIJSONPointerToken(schemeName),
			ItemID:  schemeName,
		}
		requirement, projected, requirementErr := credentialpolicy.NewRequirement(
			credentialpolicy.RequirementSpec{
				Key:      strings.ToLower(strings.TrimSpace(suffix)),
				Label:    label,
				Context:  fmt.Sprintf("OpenAPI security scheme %q generated a %s placeholder because the specification describes authentication but does not contain a credential value.", schemeName, credentialKind),
				NameHint: name,
				Source:   source,
				Targets:  targets,
				Mode:     credentialpolicy.SecretReference,
			},
		)
		if requirementErr != nil {
			return nil, requirementErr
		}
		if projected != reference {
			return nil, fmt.Errorf("generated credential name %q did not round-trip", name)
		}
		requirements = append(requirements, requirement)
	}
	return requirements, nil
}

// openAPIGeneratedSecretTargets indexes the final script once. The prior
// implementation serialized the complete script and rescanned it for every
// declared security credential, making a large specification with many unused
// schemes O(schemes * script size) and providing only a generic pseudo-target.
// This pass is linear in the final action surface and records every actual DSL
// field affected by a generated placeholder.
func openAPIGeneratedSecretTargets(
	script *dsl.Script,
	auth *authContext,
) (map[string][]credentialpolicy.TargetRef, error) {
	targets := make(map[string][]credentialpolicy.TargetRef)
	seen := make(map[string]struct{})
	add := func(name, requestKey, field, value string) {
		if name == "" || !strings.Contains(value, "{{secret."+name+"}}") {
			return
		}
		target := credentialpolicy.TargetRef{RequestKey: requestKey, Field: field}
		identity := name + "\x00" + requestKey + "\x00" + field
		if _, duplicate := seen[identity]; duplicate {
			return
		}
		seen[identity] = struct{}{}
		targets[name] = append(targets[name], target)
	}
	err := dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		if ref.Action == nil {
			return nil
		}
		action := ref.Action
		requestKey := strings.TrimSpace(action.Name)
		if requestKey == "" {
			requestKey = ref.Path
		}
		metadata, _ := action.Metadata["openapi"].(map[string]any)
		if schemeName, _ := metadata["security_scheme"].(string); schemeName != "" {
			if value, _ := action.Params["client_secret"].(string); value != "" {
				add(auth.securityName(schemeName, "CLIENT_SECRET"), requestKey, ref.Path+".params.client_secret", value)
			}
			if value, _ := action.Params["password"].(string); value != "" {
				add(auth.securityName(schemeName, "PASSWORD"), requestKey, ref.Path+".params.password", value)
			}
		}
		for _, schemeName := range openAPIMetadataStringSlice(metadata["security_schemes"]) {
			scheme, exists := auth.schemes[schemeName]
			if !exists {
				continue
			}
			switch strings.ToLower(strings.TrimSpace(scheme.Type)) {
			case "http":
				switch strings.ToLower(strings.TrimSpace(scheme.Scheme)) {
				case "bearer":
					if action.Auth != nil {
						add(auth.securityName(schemeName, "BEARER_TOKEN"), requestKey, ref.Path+".auth.token", action.Auth.Token)
					}
				case "basic", "digest":
					if action.Auth != nil {
						add(auth.securityName(schemeName, "PASSWORD"), requestKey, ref.Path+".auth.password", action.Auth.Password)
					}
				}
			case "basic":
				if action.Auth != nil {
					add(auth.securityName(schemeName, "PASSWORD"), requestKey, ref.Path+".auth.password", action.Auth.Password)
				}
			case "apikey":
				name := auth.securityName(schemeName, "API_KEY")
				switch strings.ToLower(strings.TrimSpace(scheme.In)) {
				case "query":
					add(name, requestKey, ref.Path+".target", action.Target)
				case "header":
					if headerName, value, ok := openAPIHeaderValue(action.Headers, scheme.Name); ok {
						add(name, requestKey, ref.Path+".headers."+headerName, value)
					}
				case "cookie":
					if headerName, value, ok := openAPIHeaderValue(action.Headers, "Cookie"); ok {
						add(name, requestKey, ref.Path+".headers."+headerName, value)
					}
				}
			case "oauth2", "openidconnect":
				if action.Auth != nil {
					add(auth.securityName(schemeName, "BEARER_TOKEN"), requestKey, ref.Path+".auth.token", action.Auth.Token)
				}
			}
		}
		return nil
	})
	return targets, err
}

func openAPIMetadataStringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				out = append(out, text)
			}
		}
		return out
	}
	return nil
}

func openAPIHeaderValue(headers map[string]string, requested string) (string, string, bool) {
	names := make([]string, 0, len(headers))
	for name := range headers {
		if strings.EqualFold(strings.TrimSpace(name), strings.TrimSpace(requested)) {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return "", "", false
	}
	sort.Strings(names)
	name := names[0]
	return name, headers[name], true
}

func openAPIGeneratedCredentialPresentation(
	schemeName string,
	suffix string,
) (label string, credentialKind string) {
	switch strings.ToUpper(strings.TrimSpace(suffix)) {
	case "BEARER_TOKEN":
		return "Bearer token for OpenAPI security scheme " + schemeName,
			"bearer token"
	case "CLIENT_SECRET":
		return "OAuth client secret for OpenAPI security scheme " + schemeName,
			"client secret"
	case "PASSWORD":
		return "Password for OpenAPI security scheme " + schemeName,
			"password"
	case "API_KEY":
		return "API key for OpenAPI security scheme " + schemeName,
			"API key"
	default:
		return "Credential for OpenAPI security scheme " + schemeName,
			"credential"
	}
}

func openAPIJSONPointerToken(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~", "~0"), "/", "~1")
}
