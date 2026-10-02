package raml

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
)

var ramlSecretReference = regexp.MustCompile(`\{\{secret\.[A-Za-z0-9_]+\}\}`)

func ramlAdditionalHeaderRequirements(script *dsl.Script, source string) ([]credentialpolicy.Requirement, error) {
	targets := map[string][]credentialpolicy.TargetRef{}
	for i, action := range script.Actions {
		for _, header := range sortedHeaderNames(action.Headers) {
			name, ok := credential.ParseReference(action.Headers[header])
			if !ok || name == "RAML_CLIENT_SECRET" {
				continue
			}
			targets[name] = append(targets[name], credentialpolicy.TargetRef{RequestKey: action.Name, Field: fmt.Sprintf("actions[%d].headers.%s", i, header)})
		}
		// Query placeholders are embedded in the request target.
		for _, match := range ramlSecretReference.FindAllString(action.Target, -1) {
			name, ok := credential.ParseReference(match)
			if !ok || name == "RAML_CLIENT_SECRET" {
				continue
			}
			targets[name] = append(targets[name], credentialpolicy.TargetRef{RequestKey: action.Name, Field: fmt.Sprintf("actions[%d].target", i)})
		}
	}
	keys := map[string]any{}
	for key := range targets {
		keys[key] = nil
	}
	var requirements []credentialpolicy.Requirement
	for _, name := range sortedMapKeys(keys) {
		req, _, err := credentialpolicy.NewRequirement(credentialpolicy.RequirementSpec{Key: strings.ToLower(name), Label: name, Context: "RAML declares a credential-bearing request field; bind a secret before replay. Source examples are not used as authentication credentials.", NameHint: name, Source: credentialpolicy.SourceRef{Format: "raml", Path: source, Pointer: "/requestFields"}, Targets: targets[name], Mode: credentialpolicy.SecretReference})
		if err != nil {
			return nil, err
		}
		requirements = append(requirements, req)
	}
	return requirements, nil
}

func sortedHeaderNames(headers map[string]string) []string {
	keys := map[string]any{}
	for key := range headers {
		keys[key] = nil
	}
	return sortedMapKeys(keys)
}
