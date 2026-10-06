package bruno

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"gopkg.in/yaml.v3"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/credential"
)

const nativeJSONRequirementKeyPrefix = "native-json:"
const nativeMaxJSONDepth = 128

// sanitizeNativeJSONBodies handles JSON encoded as a scalar string inside an
// OpenCollection YAML request. The general YAML sanitizer cannot descend into
// that scalar, so credential fields must be projected before the executable
// native script is built.
func sanitizeNativeJSONBodies(files []sourceFile) ([]sourceFile, []SecretRequirement, int, error) {
	out := make([]sourceFile, 0, len(files))
	requirements := make([]SecretRequirement, 0)
	for _, file := range files {
		extension := strings.ToLower(path.Ext(file.Path))
		if (extension != ".yml" && extension != ".yaml") ||
			classifyFileRole(file.Path, file.Data) != "request" {
			out = append(out, file)
			continue
		}
		var document map[string]any
		if err := yaml.Unmarshal(file.Data, &document); err != nil {
			return nil, nil, 0, fmt.Errorf("parse native JSON request %q: %w", file.Path, err)
		}
		httpBlock := nativeMapValue(document, "http")
		rawBody, bodyPresent := nativeLookupPresent(httpBlock, "body")
		if !bodyPresent {
			out = append(out, file)
			continue
		}
		bodyPointer := "/http/body"
		bodyType := ""
		raw, isString := rawBody.(string)
		if bodyBlock, ok := rawBody.(map[string]any); ok {
			bodyPointer = "/http/body/data"
			bodyType = strings.ToLower(strings.TrimSpace(anyString(nativeLookup(bodyBlock, "type"))))
			raw, isString = nativeLookup(bodyBlock, "data").(string)
		}
		if !isString || strings.TrimSpace(raw) == "" {
			out = append(out, file)
			continue
		}
		explicitJSON := bodyType == "json"
		trimmed := strings.TrimSpace(raw)
		if !explicitJSON &&
			(!strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[")) {
			out = append(out, file)
			continue
		}
		rewritten, current, err := sanitizeNativeJSONStringBody(file.Path, raw)
		if err != nil {
			if !explicitJSON {
				out = append(out, file)
				continue
			}
			return nil, nil, 0, fmt.Errorf("sanitize native JSON body %q: %w", file.Path, err)
		}
		if len(current) == 0 {
			out = append(out, file)
			continue
		}
		if _, err := setNativeJSONPointerWithAliasPeers(
			document,
			file.nativeAliasGroups,
			bodyPointer,
			rewritten,
		); err != nil {
			return nil, nil, 0, fmt.Errorf("write sanitized native JSON body %q: %w", file.Path, err)
		}
		content, err := yaml.Marshal(document)
		if err != nil {
			return nil, nil, 0, fmt.Errorf("serialize sanitized native JSON body %q: %w", file.Path, err)
		}
		out = append(out, sourceFile{
			Path:              file.Path,
			Data:              content,
			Mode:              file.Mode,
			nativeAliasGroups: file.nativeAliasGroups,
		})
		requirements = append(requirements, current...)
	}
	sort.Slice(requirements, func(i, j int) bool {
		if requirements[i].Path == requirements[j].Path {
			return requirements[i].Key < requirements[j].Key
		}
		return requirements[i].Path < requirements[j].Path
	})
	return out, requirements, len(requirements), nil
}

func nativeBodyIsJSON(body string) bool {
	trimmed := strings.TrimSpace(body)
	if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
		return false
	}
	_, err := decodeNativeJSONBody(body)
	return err == nil
}

func sanitizeNativeJSONStringBody(sourcePath, body string) (string, []SecretRequirement, error) {
	value, err := decodeNativeJSONBody(body)
	if err != nil {
		return "", nil, fmt.Errorf("JSON could not be safely parsed: %w", err)
	}
	var requirements []SecretRequirement
	occurrences := make(map[string]int)
	var walkErr error
	var walk func(any, []string)
	walk = func(current any, context []string) {
		if walkErr != nil {
			return
		}
		switch typed := current.(type) {
		case map[string]any:
			namedCredential := structuredNamedCredential(typed)
			keys := make([]string, 0, len(typed))
			for key := range typed {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				child := typed[key]
				nextContext := append(append([]string(nil), context...), key)
				scalar, scalarValue := structuredScalarString(child)
				secretKey := key
				fullContext := append([]string{"http", "body", "data"}, context...)
				if strings.EqualFold(key, "value") &&
					isStructuredCredentialName(namedCredential, fullContext, "request") {
					secretKey = namedCredential
				}
				secretField := isSecretKey(secretKey, fullContext, "request") ||
					nativeJSONCredentialContainerKey(key)
				if strings.EqualFold(key, "value") &&
					nativeCredentialContainerContext(context) {
					secretField = true
				}
				if secretField && !nativeJSONCredentialContainerKey(key) {
					switch child.(type) {
					case map[string]any, []any:
						walkErr = fmt.Errorf(
							"composite JSON credential field %q cannot be governed without changing its wire shape",
							nativeJSONPointer(nextContext),
						)
						return
					}
				}
				sensitive := scalarValue && secretField &&
					shouldSanitizeCredentialValue(scalar)
				if sensitive {
					if _, stringValue := child.(string); !stringValue {
						walkErr = fmt.Errorf(
							"non-string JSON credential field %q cannot be governed without changing its wire type",
							nativeJSONPointer(nextContext),
						)
						return
					}
				}
				if sensitive &&
					!isExactGovernedSecretReference(scalar) &&
					!brunoTemplatePattern.MatchString(scalar) {
					identity := nativeJSONPointer(nextContext)
					occurrences[identity]++
					requirement := nativeJSONSecretRequirement(
						sourcePath,
						identity,
						secretKey,
						occurrences[identity],
						scalar,
					)
					typed[key] = nativeCredentialProjectionReplacement(requirement)
					requirements = append(requirements, requirement)
					continue
				}
				walk(child, nextContext)
			}
		case []any:
			for index, child := range typed {
				walk(child, append(append([]string(nil), context...), strconv.Itoa(index)))
			}
		}
	}
	walk(value, nil)
	if walkErr != nil {
		return "", nil, walkErr
	}
	if len(requirements) == 0 {
		return body, nil, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", nil, err
	}
	return string(encoded), requirements, nil
}

func nativeJSONCredentialContainerKey(key string) bool {
	normalized := strings.ToLower(strings.NewReplacer("-", "", "_", "", ".", "").Replace(strings.TrimSpace(key)))
	switch normalized {
	case "auth", "authentication", "credential", "credentials", "secret", "secrets":
		return true
	default:
		return false
	}
}

func rewriteNativeJSONCredentialTemplates(
	body string,
	state *nativeTemplateState,
	sourceRequirements map[string][]SecretRequirement,
	bodyPointer string,
	embeddedJSON bool,
) (string, error) {
	if !brunoTemplatePattern.MatchString(body) {
		return body, nil
	}
	value, err := decodeNativeJSONBody(body)
	if err != nil {
		return "", err
	}
	changed := false
	occurrence := 0
	var walk func(any, []string)
	walk = func(current any, context []string) {
		switch typed := current.(type) {
		case map[string]any:
			namedCredential := structuredNamedCredential(typed)
			keys := make([]string, 0, len(typed))
			for key := range typed {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				child := typed[key]
				nextContext := append(append([]string(nil), context...), key)
				text, ok := child.(string)
				secretKey := key
				fullContext := append([]string{"http", "body", "data"}, context...)
				if strings.EqualFold(key, "value") &&
					isStructuredCredentialName(namedCredential, fullContext, "request") {
					secretKey = namedCredential
				}
				secretField := isSecretKey(secretKey, fullContext, "request") ||
					nativeJSONCredentialContainerKey(key)
				if strings.EqualFold(key, "value") &&
					nativeCredentialContainerContext(context) {
					secretField = true
				}
				if ok &&
					secretField &&
					brunoTemplatePattern.MatchString(text) {
					occurrence++
					rewritten := rewriteNativeJSONCredentialValue(
						text,
						nativeJSONPointer(nextContext),
						secretKey,
						occurrence,
						state,
						sourceRequirements,
						bodyPointer,
						embeddedJSON,
					)
					if rewritten != text {
						typed[key] = rewritten
						changed = true
					}
					continue
				}
				walk(child, nextContext)
			}
		case []any:
			for index, child := range typed {
				walk(child, append(append([]string(nil), context...), strconv.Itoa(index)))
			}
		}
	}
	walk(value, nil)
	if !changed {
		return body, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func rewriteNativeJSONCredentialValue(
	value, pointer, name string,
	occurrence int,
	state *nativeTemplateState,
	sourceRequirements map[string][]SecretRequirement,
	bodyPointer string,
	embeddedJSON bool,
) string {
	if secretReference, ok := exactNativeSecretReference(value); ok {
		return secretReference
	}
	sourceVariable, hasExactSourceVariable := exactNativeCredentialSourceReference(value)
	beforeMissing := make(map[string]struct{}, len(state.missing))
	for missing := range state.missing {
		beforeMissing[missing] = struct{}{}
	}
	dependencies := make([]nativeVariable, 0)
	complete := true
	projectedWholeCredential := false
	matchIndexes := brunoTemplatePattern.FindAllStringIndex(value, -1)
	composedCredential := len(matchIndexes) != 1 ||
		matchIndexes[0][0] != 0 ||
		matchIndexes[0][1] != len(value)
	usageRequirement := nativeJSONSecretRequirement(
		state.sourcePath,
		pointer,
		name,
		occurrence,
		"",
	)
	_, usageRequirementAvailable := nativeSourceSecretValue(
		usageRequirement.Replacement,
		sourceRequirements,
	)
	resolved := brunoTemplatePattern.ReplaceAllStringFunc(value, func(template string) string {
		match := brunoTemplatePattern.FindStringSubmatch(template)
		if len(match) < 2 {
			complete = false
			return template
		}
		expression := strings.TrimSpace(match[1])
		lower := strings.ToLower(expression)
		for _, prefix := range []string{"env.", "data.", "var.", "param.", "secret.", "fn.", "fake.", "file."} {
			if strings.HasPrefix(lower, prefix) {
				complete = false
				return template
			}
		}
		if strings.Contains(expression, "(") || !nativeTemplateNamePattern.MatchString(expression) {
			complete = false
			return template
		}
		if _, exists := state.defined[expression]; !exists {
			complete = false
			return template
		}
		resolution := resolveNativeVariable(expression, state)
		dependencies = appendNativeVariableDependencies(dependencies, resolution.Dependencies...)
		if secretReference, ok := exactNativeSecretReference(resolution.Value); ok {
			if composedCredential && usageRequirementAvailable {
				projectedWholeCredential = true
				return resolution.Value
			}
			if !composedCredential {
				context := "Bruno JSON body credential " + name
				projected := nativeVariableSecretRequirement(
					state.defined[expression],
					state.sourcePath,
					context,
				)
				if secretReference == projected.Replacement {
					projectedWholeCredential = true
					return resolution.Value
				}
			}
			if sourceValue, available := nativeSourceSecretValue(secretReference, sourceRequirements); available {
				return sourceValue
			}
		}
		if !resolution.Complete {
			complete = false
		}
		return resolution.Value
	})
	if brunoTemplatePattern.MatchString(resolved) {
		complete = false
	}
	if projectedWholeCredential && len(dependencies) > 0 {
		complete = true
	}
	for missing := range state.missing {
		if _, existed := beforeMissing[missing]; !existed {
			delete(state.missing, missing)
		}
	}

	context := "Bruno JSON body credential " + name
	var requirement SecretRequirement
	if composedCredential || len(dependencies) > 1 {
		requirement = usageRequirement
		requirement.Context = context
		requirement.scrubTargets = nativeVariableScrubTargets(dependencies)
		if embeddedJSON {
			requirement.scrubTargets = append(
				requirement.scrubTargets,
				nativeSecretScrubTarget{
					Path:     state.sourcePath,
					Pointer:  bodyPointer,
					Kind:     "json_string_value",
					Selector: pointer,
				},
			)
		} else {
			requirement.scrubTargets = append(
				requirement.scrubTargets,
				nativeSecretScrubTarget{
					Path:    state.sourcePath,
					Pointer: bodyPointer + pointer,
				},
			)
		}
	} else if len(dependencies) > 0 {
		requirement = nativeVariableSecretRequirement(dependencies[0], state.sourcePath, context)
		requirement.scrubTargets = nativeVariableScrubTargets(dependencies)
		if embeddedJSON {
			requirement.scrubTargets = append(
				requirement.scrubTargets,
				nativeSecretScrubTarget{
					Path:     state.sourcePath,
					Pointer:  bodyPointer,
					Kind:     "json_string_value",
					Selector: pointer,
				},
			)
		} else {
			requirement.scrubTargets = append(
				requirement.scrubTargets,
				nativeSecretScrubTarget{
					Path:    state.sourcePath,
					Pointer: bodyPointer + pointer,
				},
			)
		}
	} else {
		if hasExactSourceVariable {
			requirement = nativeUnresolvedSourceVariableSecretRequirement(
				sourceVariable,
				state.sourcePath,
				context,
			)
		} else {
			requirement = nativeSecretRequirement(name+"_"+strconv.Itoa(occurrence), state.sourcePath, context)
		}
	}
	if hasExactSourceVariable {
		requirement.DisplayLabel = nativeSourceVariableDisplayLabel(sourceVariable)
		requirement.SourceVariable = sourceVariable
		requirement.AffectedPaths = nativeSecretRequirementPaths(
			append(requirement.AffectedPaths, state.sourcePath)...,
		)
	}
	if complete && !brunoTemplatePattern.MatchString(resolved) {
		requirement.Value = recoverableBrunoCredentialValue(resolved)
	}
	if hasExactSourceVariable {
		requirement.UnresolvedSourceVariable = requirement.Value == ""
	}
	registerNativeGeneratedRequirement(state, requirement)
	return nativeCredentialProjectionReplacement(requirement)
}

func setNativeJSONStringValue(
	document any,
	bodyPointer, valuePointer, replacement string,
) (bool, error) {
	rawValue, err := nativeJSONPointerValue(document, bodyPointer)
	if err != nil {
		return false, err
	}
	raw, ok := rawValue.(string)
	if !ok {
		return false, fmt.Errorf("JSON body scrub target %q is not a string", bodyPointer)
	}
	value, err := decodeNativeJSONBody(raw)
	if err != nil {
		return false, err
	}
	changed, err := setNativeJSONPointer(value, valuePointer, replacement)
	if err != nil || !changed {
		return changed, err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return false, err
	}
	return setNativeJSONPointer(document, bodyPointer, string(encoded))
}

func decodeNativeJSONBody(body string) (any, error) {
	if err := validateNativeJSONUniqueKeys(body); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("multiple JSON values are not supported")
		}
		return nil, err
	}
	return value, nil
}

func validateNativeJSONUniqueKeys(body string) error {
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.UseNumber()
	if err := validateNativeJSONValue(decoder, "$", 0); err != nil {
		return err
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON token %v", token)
		}
		return err
	}
	return nil
}

func validateNativeJSONValue(decoder *json.Decoder, location string, depth int) error {
	if depth > nativeMaxJSONDepth {
		return fmt.Errorf("JSON nesting exceeds %d levels", nativeMaxJSONDepth)
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("JSON object %s contains a non-string key", location)
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("JSON object %s contains duplicate key %q", location, key)
			}
			seen[key] = struct{}{}
			if err := validateNativeJSONValue(
				decoder,
				location+"/"+nativeJSONPointerEscape(key),
				depth+1,
			); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim('}') {
			return fmt.Errorf("JSON object %s has invalid closing token", location)
		}
	case '[':
		for index := 0; decoder.More(); index++ {
			if err := validateNativeJSONValue(
				decoder,
				location+"/"+strconv.Itoa(index),
				depth+1,
			); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim(']') {
			return fmt.Errorf("JSON array %s has invalid closing token", location)
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
	return nil
}

func nativeJSONSecretRequirement(
	sourcePath, pointer, name string,
	occurrence int,
	value string,
) SecretRequirement {
	identity := pointer + "\x00" + strings.ToLower(name) + "\x00" + strconv.Itoa(occurrence)
	nameHash := sha256.Sum256([]byte("bruno-native-json-name\x00" + sourcePath + "\x00" + identity))
	suffix := "_" + strings.ToUpper(hex.EncodeToString(nameHash[:6]))
	suggested := secretName(sourcePath, "body_json_"+name)
	if len(suggested)+len(suffix) > credential.MaxLogicalNameBytes {
		suggested = strings.TrimRight(suggested[:credential.MaxLogicalNameBytes-len(suffix)], "_")
	}
	suggested += suffix
	idHash := sha256.Sum256([]byte("bruno-native-json\x00" + sourcePath + "\x00" + identity))
	return SecretRequirement{
		ID:            hex.EncodeToString(idHash[:12]),
		Path:          sourcePath,
		Key:           nativeJSONRequirementKeyPrefix + pointer,
		SuggestedName: suggested,
		Replacement:   "{{secret." + suggested + "}}",
		Context:       "Bruno JSON body credential " + name,
		Value:         recoverableBrunoCredentialValue(value),
		AffectedPaths: nativeSecretRequirementPaths(sourcePath),
	}
}

func nativeJSONPointer(segments []string) string {
	var out bytes.Buffer
	for _, segment := range segments {
		out.WriteByte('/')
		out.WriteString(nativeJSONPointerEscape(segment))
	}
	return out.String()
}
