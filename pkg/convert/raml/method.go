package raml

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// Explicit method facets override inherited traits. Maps merge recursively;
// sequences are copied so expansion never mutates shared library definitions.
func mergeRAML(base, override map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range base {
		out[key] = value
	}
	for key, value := range override {
		if key == "is" {
			if inherited, ok := out[key].([]any); ok {
				if local, ok := value.([]any); ok {
					out[key] = append(append([]any{}, inherited...), local...)
					continue
				}
			}
		}
		if mapped := mapValue(value); mapped != nil {
			out[key] = mergeRAML(mapValue(out[key]), mapped)
		} else {
			out[key] = value
		}
	}
	return out
}

var ramlTraitParameter = regexp.MustCompile(`<<([^<>]+)>>`)

func substituteRAML(value any, params map[string]any) any {
	switch v := value.(type) {
	case string:
		return ramlTraitParameter.ReplaceAllStringFunc(v, func(token string) string {
			name := strings.TrimSpace(token[2 : len(token)-2])
			if replacement, ok := params[name]; ok {
				return stringValue(replacement)
			}
			return token
		})
	case map[string]any:
		out := map[string]any{}
		for key, item := range v {
			out[stringValue(substituteRAML(key, params))] = substituteRAML(item, params)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = substituteRAML(item, params)
		}
		return out
	default:
		return value
	}
}

func ramlAssetRef(ref any) (string, map[string]any) {
	if name, ok := ref.(string); ok {
		return name, nil
	}
	mapped := mapValue(ref)
	if len(mapped) == 1 {
		for name, params := range mapped {
			return name, mapValue(params)
		}
	}
	return "", nil
}

type ramlExpansion struct {
	active    map[string]bool
	remaining int
}

func expandedRAMLAsset(ref any, section string, doc map[string]any, builtin map[string]any, state *ramlExpansion, depth int) (map[string]any, []string) {
	name, args := ramlAssetRef(ref)
	key := section + ":" + name
	state.remaining--
	if name == "" || depth > 20 || state.active[key] || state.remaining < 0 {
		return nil, []string{"invalid, cyclic, or excessive " + section + " reference"}
	}
	asset := mapValue(mapValue(doc[section])[name])
	if asset == nil {
		return nil, []string{fmt.Sprintf("unresolved %s reference %q", section, name)}
	}
	state.active[key] = true
	defer delete(state.active, key)
	params := mergeRAML(builtin, args)
	expanded := mapValue(substituteRAML(asset, params))
	inherited := map[string]any{}
	var issues []string
	if section == "resourceTypes" && expanded["type"] != nil {
		inherited, issues = expandedRAMLAsset(expanded["type"], section, doc, builtin, state, depth+1)
	}
	if refs, ok := expanded["is"].([]any); ok && section == "traits" {
		for _, item := range refs {
			trait, problems := expandedRAMLAsset(item, "traits", doc, builtin, state, depth+1)
			inherited = mergeRAML(inherited, trait)
			issues = append(issues, problems...)
		}
	}
	if section == "traits" {
		delete(expanded, "is")
	}
	if section == "resourceTypes" {
		delete(expanded, "type")
	}
	return mergeRAML(inherited, expanded), issues
}

func effectiveRAMLResource(resource map[string]any, resourcePath string, doc map[string]any) map[string]any {
	if resource["type"] == nil {
		return resource
	}
	expanded, issues := expandedRAMLAsset(resource["type"], "resourceTypes", doc, map[string]any{"resourcePath": resourcePath, "resourcePathName": lastRAMLPathName(resourcePath)}, &ramlExpansion{active: map[string]bool{}, remaining: 2000}, 0)
	out := mergeRAML(expanded, resource)
	// Optional methods in resource types apply only when explicitly declared.
	for key, value := range out {
		if strings.HasSuffix(key, "?") && ramlMethods[strings.TrimSuffix(key, "?")] {
			plain := strings.TrimSuffix(key, "?")
			if _, ok := resource[plain]; ok {
				out[plain] = mergeRAML(mapValue(value), mapValue(resource[plain]))
			}
			delete(out, key)
		}
	}
	out["__raml_issues"] = issues
	return out
}

func lastRAMLPathName(resourcePath string) string {
	parts := strings.Split(strings.Trim(resourcePath, "/"), "/")
	for i := len(parts) - 1; i >= 0; i-- {
		if !strings.HasPrefix(parts[i], "{") {
			return parts[i]
		}
	}
	return ""
}

func effectiveRAMLMethod(resource, method map[string]any, resourcePath, verb string, doc map[string]any) map[string]any {
	merged := map[string]any{}
	issues, _ := resource["__raml_issues"].([]string)
	builtin := map[string]any{"resourcePath": resourcePath, "resourcePathName": lastRAMLPathName(resourcePath), "methodName": verb}
	for _, scope := range []map[string]any{resource, method} {
		if refs, ok := scope["is"].([]any); ok {
			for _, ref := range refs {
				expanded, problems := expandedRAMLAsset(ref, "traits", doc, builtin, &ramlExpansion{active: map[string]bool{}, remaining: 2000}, 0)
				merged = mergeRAML(merged, expanded)
				issues = append(issues, problems...)
			}
		}
	}
	merged = mergeRAML(merged, method)
	merged["__raml_issues"] = issues
	return merged
}

func ramlReview(action *dsl.Action, result *ConvertResult, element, detail string) {
	if action.Metadata == nil {
		action.Metadata = map[string]any{}
	}
	action.Metadata["execution_blocker"] = "import_review_required"
	result.Unsupported = append(result.Unsupported, diag.Diagnostic{Severity: diag.Warning, Element: element, Location: action.Method + " " + action.Target, Detail: detail})
}

func ramlDeclaredValue(raw any) (any, bool) {
	declaration := mapValue(raw)
	for _, key := range []string{"example", "default"} {
		if value, ok := declaration[key]; ok && value != nil {
			if wrapper := mapValue(value); wrapper != nil && wrapper["value"] != nil {
				value = wrapper["value"]
			}
			return value, true
		}
	}
	if values, ok := declaration["enum"].([]any); ok && len(values) > 0 {
		return values[0], true
	}
	return nil, false
}

func bindRAMLRequestParameters(action *dsl.Action, method map[string]any, result *ConvertResult) {
	if issues, ok := method["__raml_issues"].([]string); ok {
		for _, issue := range issues {
			ramlReview(action, result, "RAML inheritance", issue)
		}
	}
	for _, kind := range []string{"headers", "queryParameters"} {
		declarations := mapValue(method[kind])
		for _, authoredName := range sortedMapKeys(declarations) {
			name := strings.TrimSuffix(authoredName, "?")
			declaration := mapValue(declarations[authoredName])
			required := !strings.HasSuffix(authoredName, "?") && declaration["required"] != false
			value, bound := ramlDeclaredValue(declaration)
			text := ""
			if bound {
				text = stringValue(value)
			}
			normalized := credential.NormalizeName(name)
			switch normalized {
			case "clientid":
				text = "{{env.RAML_CLIENT_ID}}"
				bound = true
			case "clientsecret":
				text = "{{secret.RAML_CLIENT_SECRET}}"
				bound = true
			default:
				if kind == "headers" && credential.ImportCredentialHeader(name) ||
					kind != "headers" && credential.SensitiveName(name) {
					text = "{{secret.RAML_" + credential.EnvKey(name) + "}}"
					bound = true
				}
			}
			if bound && !strings.HasPrefix(text, "{{") {
				switch value.(type) {
				case map[string]any, map[any]any, []any:
					ramlReview(action, result, "RAML "+kind, fmt.Sprintf("field %q has a structured example; select its wire serialization before replay", name))
					continue
				}
			}
			if !bound {
				if required {
					ramlReview(action, result, "RAML "+kind, fmt.Sprintf("required field %q has no example/default/enum binding", name))
				}
				continue
			}
			if kind == "headers" {
				action.Headers[name] = text
			} else {
				separator := "?"
				if strings.Contains(action.Target, "?") {
					separator = "&"
				}
				if !strings.HasPrefix(text, "{{") {
					text = url.QueryEscape(text)
				}
				action.Target += separator + url.QueryEscape(name) + "=" + text
			}
		}
	}
}

func ramlResolvedMethodBody(method, doc map[string]any, action *dsl.Action, result *ConvertResult) (string, string, bool) {
	bodies := mapValue(method["body"])
	// Handle shorthand under the root's default mediaType.
	if bodies["type"] != nil || bodies["properties"] != nil || bodies["example"] != nil || bodies["examples"] != nil {
		media := stringValue(doc["mediaType"])
		if media == "" {
			media = "application/json"
		}
		bodies = map[string]any{media: bodies}
	}
	// The first declared media type, in sorted order, shapes the body.
	contentTypes := sortedMapKeys(bodies)
	if len(contentTypes) == 0 {
		return "", "", false
	}
	contentType := contentTypes[0]
	media := mapValue(bodies[contentType])
	if media == nil {
		if text, ok := bodies[contentType].(string); ok {
			media = map[string]any{"type": text}
		} else {
			media = map[string]any{}
		}
	}
	if value, ok := ramlExample(media); ok {
		text, _ := bodyExampleValue(value)
		return text, contentType, true
	}
	value, complete := sampleRAMLType(media, mapValue(doc["types"]), &ramlSample{active: map[string]bool{}, remaining: 2000}, 0)
	if !complete {
		if action.Metadata == nil {
			action.Metadata = map[string]any{}
		}
		if declared := stringValue(media["type"]); declared != "" {
			action.Metadata["raml_request_body_type"] = declared
		}
		ramlReview(action, result, "RAML request body", "No complete request example is available; review the generated body and supply missing required fields or select a union variant")
	}
	if strings.Contains(contentType, "json") {
		return jsonBody(value), contentType, true
	}
	if value != nil {
		return stringValue(value), contentType, true
	}
	return "", contentType, true
}

func ramlExample(declaration map[string]any) (any, bool) {
	if value, ok := declaration["example"]; ok && value != nil {
		if wrapper := mapValue(value); wrapper != nil {
			if inner, exists := wrapper["value"]; exists {
				return inner, true
			}
		}
		return value, true
	}
	examples := mapValue(declaration["examples"])
	for _, name := range sortedMapKeys(examples) {
		value := examples[name]
		if wrapper := mapValue(value); wrapper != nil {
			if inner, exists := wrapper["value"]; exists {
				value = inner
			}
		}
		if value != nil {
			return value, true
		}
	}
	return nil, false
}

// Assemble only authored examples/defaults/enums. Never fabricate IDs or other
// required values. Incomplete samples remain useful drafts with a replay gate.
type ramlSample struct {
	active    map[string]bool
	remaining int
}

func sampleRAMLType(raw any, types map[string]any, state *ramlSample, depth int) (any, bool) {
	state.remaining--
	if raw == nil || depth > 32 || state.remaining < 0 {
		return nil, false
	}
	declaration := mapValue(raw)
	if text, ok := raw.(string); ok {
		declaration = map[string]any{"type": text}
	}
	if value, ok := ramlExample(declaration); ok {
		return value, true
	}
	if value, ok := ramlDeclaredValue(declaration); ok {
		return value, true
	}
	typeName := stringValue(declaration["type"])
	if strings.Contains(typeName, "|") {
		return nil, false
	}
	if strings.HasSuffix(typeName, "[]") {
		value, complete := sampleRAMLType(strings.TrimSpace(strings.TrimSuffix(typeName, "[]")), types, state, depth+1)
		if value == nil {
			return []any{}, false
		}
		return []any{value}, complete
	}
	if base, ok := types[typeName]; ok {
		if state.active[typeName] {
			return nil, false
		}
		state.active[typeName] = true
		defer delete(state.active, typeName)
		inherited := mapValue(base)
		if name, ok := base.(string); ok {
			inherited = map[string]any{"type": name}
		}
		local := mergeRAML(nil, declaration)
		delete(local, "type")
		return sampleRAMLType(mergeRAML(inherited, local), types, state, depth+1)
	}
	properties := mapValue(declaration["properties"])
	if properties != nil || typeName == "object" || len(declaration) == 0 {
		object := map[string]any{}
		complete := true
		for _, key := range sortedMapKeys(properties) {
			if state.remaining < 0 {
				complete = false
				break
			}
			property := mapValue(properties[key])
			required := !strings.HasSuffix(key, "?") && property["required"] != false
			value, ok := sampleRAMLType(properties[key], types, state, depth+1)
			if value != nil && (ok || required) {
				object[strings.TrimSuffix(key, "?")] = value
			}
			if required && !ok {
				complete = false
			}
		}
		return object, complete
	}
	if typeName == "array" {
		value, complete := sampleRAMLType(declaration["items"], types, state, depth+1)
		if value == nil {
			return []any{}, false
		}
		return []any{value}, complete
	}
	return nil, false
}
