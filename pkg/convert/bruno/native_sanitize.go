package bruno

import (
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const nativeVariableRequirementKeyPrefix = "native-variable:"

type nativeSecretScrubTarget struct {
	Path     string
	Pointer  string
	Kind     string
	Selector string
	Index    int
}

func nativeProjectedVariableRequirements(requirements []SecretRequirement) []SecretRequirement {
	projected := make([]SecretRequirement, 0)
	seen := make(map[string]struct{})
	for _, requirement := range requirements {
		// Scrubbing is independent from migration. An unresolved composed
		// credential intentionally has no migratable Value, but its literal
		// fragments and every resolved dependency still have to be removed from
		// the protected sanitized source.
		if len(requirement.scrubTargets) == 0 &&
			!strings.HasPrefix(requirement.Key, nativeVariableRequirementKeyPrefix) {
			continue
		}
		if _, exists := seen[requirement.ID]; exists {
			continue
		}
		seen[requirement.ID] = struct{}{}
		projected = append(projected, requirement)
	}
	return projected
}

func rewriteNativeComposedCredential(
	value string,
	requirement SecretRequirement,
	state *nativeTemplateState,
	sourceRequirements map[string][]SecretRequirement,
	usageTarget nativeSecretScrubTarget,
) (string, bool) {
	if secretReference, ok := exactNativeSecretReference(value); ok {
		return secretReference, true
	}
	_, projectedRequirementAvailable := nativeSourceSecretValue(
		requirement.Replacement,
		sourceRequirements,
	)
	beforeMissing := make(map[string]struct{}, len(state.missing))
	for missing := range state.missing {
		beforeMissing[missing] = struct{}{}
	}
	dependencies := make([]nativeVariable, 0)
	complete := true
	projectedWholeCredential := false
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
			if projectedRequirementAvailable {
				projectedWholeCredential = true
				return resolution.Value
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
	requirement.scrubTargets = append(
		nativeVariableScrubTargets(dependencies),
		usageTarget,
	)
	if complete && !brunoTemplatePattern.MatchString(resolved) {
		requirement.Value = resolved
	}
	registerNativeGeneratedRequirement(state, requirement)
	return requirement.Replacement, complete
}

func applyNativeProjectedVariableSecrets(
	files []sourceFile,
	requirements []SecretRequirement,
) ([]sourceFile, int, error) {
	type replacement struct {
		path        string
		pointer     string
		kind        string
		selector    string
		index       int
		replacement string
	}
	plansByTarget := make(map[string]replacement)
	filesByPath := make(map[string]sourceFile, len(files))
	for _, file := range files {
		filesByPath[file.Path] = file
	}
	for _, requirement := range requirements {
		targets := requirement.scrubTargets
		if len(targets) == 0 {
			if !strings.HasPrefix(requirement.Key, nativeVariableRequirementKeyPrefix) {
				continue
			}
			targets = []nativeSecretScrubTarget{{
				Path:    requirement.Path,
				Pointer: strings.TrimPrefix(requirement.Key, nativeVariableRequirementKeyPrefix),
			}}
		}
		for _, target := range targets {
			expandedTargets := []nativeSecretScrubTarget{target}
			aliasIdentity, err := nativeAliasPointerIdentity(target.Pointer)
			if err != nil {
				return nil, 0, fmt.Errorf(
					"resolve native credential alias pointer %q: %w",
					target.Pointer,
					err,
				)
			}
			for _, pointer := range filesByPath[target.Path].nativeAliasGroups[aliasIdentity] {
				if pointer == target.Pointer {
					continue
				}
				aliasTarget := target
				aliasTarget.Pointer = pointer
				expandedTargets = append(expandedTargets, aliasTarget)
			}
			for _, expandedTarget := range expandedTargets {
				pointerIdentity, err := nativeAliasPointerIdentity(expandedTarget.Pointer)
				if err != nil {
					return nil, 0, fmt.Errorf(
						"normalize native credential alias pointer %q: %w",
						expandedTarget.Pointer,
						err,
					)
				}
				identity := expandedTarget.Path + "\x00" + pointerIdentity + "\x00" +
					expandedTarget.Kind + "\x00" + expandedTarget.Selector + "\x00" +
					strconv.Itoa(expandedTarget.Index)
				plan := replacement{
					path:        expandedTarget.Path,
					pointer:     expandedTarget.Pointer,
					kind:        expandedTarget.Kind,
					selector:    expandedTarget.Selector,
					index:       expandedTarget.Index,
					replacement: requirement.Replacement,
				}
				if existing, duplicate := plansByTarget[identity]; duplicate {
					// One authored variable may feed several independently governed
					// whole-value credentials. The sanitized bundle needs only one
					// deterministic placeholder at the shared source definition;
					// each native action retains its own usage-specific requirement.
					if plan.replacement < existing.replacement {
						plansByTarget[identity] = plan
					}
					continue
				}
				plansByTarget[identity] = plan
			}
		}
	}
	if len(plansByTarget) == 0 {
		return append([]sourceFile(nil), files...), 0, nil
	}
	identities := make([]string, 0, len(plansByTarget))
	for identity := range plansByTarget {
		identities = append(identities, identity)
	}
	sort.Strings(identities)
	byPath := make(map[string][]replacement)
	for _, identity := range identities {
		plan := plansByTarget[identity]
		byPath[plan.path] = append(byPath[plan.path], plan)
	}

	out := make([]sourceFile, 0, len(files))
	replaced := 0
	for _, file := range files {
		replacements := byPath[file.Path]
		if len(replacements) == 0 {
			out = append(out, file)
			continue
		}
		var document any
		if err := yaml.Unmarshal(file.Data, &document); err != nil {
			return nil, 0, fmt.Errorf("parse native credential definition %q: %w", file.Path, err)
		}
		for _, plan := range replacements {
			var changed bool
			var err error
			switch plan.kind {
			case "":
				changed, err = setNativeJSONPointer(document, plan.pointer, plan.replacement)
			case "url_query_value":
				changed, err = setNativeURLQueryValue(
					document,
					plan.pointer,
					plan.index,
					plan.replacement,
				)
			case "json_string_value":
				changed, err = setNativeJSONStringValue(
					document,
					plan.pointer,
					plan.selector,
					plan.replacement,
				)
			case "xml_credential_value":
				changed, err = setNativeXMLCredentialValue(
					document,
					plan.pointer,
					plan.selector,
					plan.index,
					plan.replacement,
				)
			default:
				err = fmt.Errorf("unsupported projected native scrub target kind %q", plan.kind)
			}
			if err != nil {
				return nil, 0, fmt.Errorf("scrub native credential definition %q%s: %w", file.Path, plan.pointer, err)
			}
			if changed {
				replaced++
			}
		}
		content, err := yaml.Marshal(document)
		if err != nil {
			return nil, 0, fmt.Errorf("serialize native credential definition %q: %w", file.Path, err)
		}
		out = append(out, sourceFile{
			Path:              file.Path,
			Data:              content,
			Mode:              file.Mode,
			nativeAliasGroups: file.nativeAliasGroups,
		})
	}
	if replaced != len(plansByTarget) {
		return nil, 0, fmt.Errorf("scrubbed %d of %d projected native credential definitions", replaced, len(plansByTarget))
	}
	return out, replaced, nil
}

func setNativeJSONPointerWithAliasPeers(
	document any,
	aliasGroups map[string][]string,
	pointer, replacement string,
) (int, error) {
	pointers := []string{pointer}
	identity, err := nativeAliasPointerIdentity(pointer)
	if err != nil {
		return 0, err
	}
	if peers := aliasGroups[identity]; len(peers) > 0 {
		pointers = peers
	}
	changed := 0
	for _, current := range pointers {
		updated, err := setNativeJSONPointer(document, current, replacement)
		if err != nil {
			return 0, err
		}
		if updated {
			changed++
		}
	}
	return changed, nil
}

func validateNoUnprojectedNativeCredentialTemplates(files []sourceFile) error {
	for _, file := range files {
		extension := strings.ToLower(path.Ext(file.Path))
		role := classifyFileRole(file.Path, file.Data)
		if (extension != ".yml" && extension != ".yaml") ||
			(role != "request" && role != "folder" && role != "collection") {
			continue
		}
		var document map[string]any
		if err := yaml.Unmarshal(file.Data, &document); err != nil {
			return err
		}
		containerName := "request"
		if role == "request" {
			containerName = "http"
		}
		container := nativeMapValue(document, containerName)
		lists := [][]nativeVariable{
			parseNativeVariables(nativeLookup(document, "vars"), file.Path, "/vars"),
			parseNativeVariables(nativeLookup(document, "variables"), file.Path, "/variables"),
			parseNativeVariables(
				nativeLookup(container, "vars"),
				file.Path,
				"/"+nativeJSONPointerEscape(containerName)+"/vars",
			),
			parseNativeVariables(
				nativeLookup(container, "variables"),
				file.Path,
				"/"+nativeJSONPointerEscape(containerName)+"/variables",
			),
		}
		for _, variables := range lists {
			for _, variable := range variables {
				if variable.Disabled ||
					!nativeSensitiveVariableName(variable.Name) ||
					!brunoTemplatePattern.MatchString(variable.Value) ||
					isExactGovernedSecretReference(variable.Value) {
					continue
				}
				return fmt.Errorf(
					"enabled sensitive Bruno variable %q in %q contains an unprojected template",
					variable.Name,
					file.Path,
				)
			}
		}
	}
	return nil
}

func nativeSensitiveVariableName(name string) bool {
	return nativeTemplateRequiresSecret(name, nativeTemplateBody, "")
}

func setNativeJSONPointer(document any, pointer, replacement string) (bool, error) {
	segments, err := nativeJSONPointerSegments(pointer)
	if err != nil {
		return false, err
	}
	if len(segments) == 0 {
		return false, fmt.Errorf("definition pointer %q has no target", pointer)
	}

	current := document
	for index, segment := range segments {
		last := index == len(segments)-1
		switch typed := current.(type) {
		case map[string]any:
			key, exists := nativeMapKey(typed, segment)
			if !exists {
				return false, fmt.Errorf("mapping segment %q was not found", segment)
			}
			if last {
				if anyString(typed[key]) == replacement {
					return false, nil
				}
				typed[key] = replacement
				return true, nil
			}
			current = typed[key]
		case []any:
			position, err := strconv.Atoi(segment)
			if err != nil || position < 0 || position >= len(typed) {
				return false, fmt.Errorf("sequence segment %q is out of range", segment)
			}
			if last {
				if anyString(typed[position]) == replacement {
					return false, nil
				}
				typed[position] = replacement
				return true, nil
			}
			current = typed[position]
		default:
			return false, fmt.Errorf("segment %q traverses a scalar", segment)
		}
	}
	return false, fmt.Errorf("definition pointer %q has no target", pointer)
}

func nativeJSONPointerValue(document any, pointer string) (any, error) {
	segments, err := nativeJSONPointerSegments(pointer)
	if err != nil {
		return nil, err
	}
	if len(segments) == 0 {
		return nil, fmt.Errorf("definition pointer %q has no target", pointer)
	}
	current := document
	for _, segment := range segments {
		switch typed := current.(type) {
		case map[string]any:
			key, exists := nativeMapKey(typed, segment)
			if !exists {
				return nil, fmt.Errorf("mapping segment %q was not found", segment)
			}
			current = typed[key]
		case []any:
			position, err := strconv.Atoi(segment)
			if err != nil || position < 0 || position >= len(typed) {
				return nil, fmt.Errorf("sequence segment %q is out of range", segment)
			}
			current = typed[position]
		default:
			return nil, fmt.Errorf("segment %q traverses a scalar", segment)
		}
	}
	return current, nil
}

func nativeJSONPointerSegments(pointer string) ([]string, error) {
	if pointer == "" || pointer[0] != '/' {
		return nil, fmt.Errorf("invalid definition pointer %q", pointer)
	}
	rawSegments := strings.Split(pointer[1:], "/")
	segments := make([]string, len(rawSegments))
	for index, segment := range rawSegments {
		decoded, err := nativeJSONPointerUnescape(segment)
		if err != nil {
			return nil, err
		}
		segments[index] = decoded
	}
	return segments, nil
}

func nativeMapKey(parent map[string]any, wanted string) (string, bool) {
	if _, exists := parent[wanted]; exists {
		return wanted, true
	}
	for key := range parent {
		if strings.EqualFold(strings.TrimSpace(key), strings.TrimSpace(wanted)) {
			return key, true
		}
	}
	return "", false
}

func nativeJSONPointerUnescape(value string) (string, error) {
	var out strings.Builder
	for index := 0; index < len(value); index++ {
		if value[index] != '~' {
			out.WriteByte(value[index])
			continue
		}
		if index+1 >= len(value) {
			return "", fmt.Errorf("invalid JSON pointer escape in %q", value)
		}
		index++
		switch value[index] {
		case '0':
			out.WriteByte('~')
		case '1':
			out.WriteByte('/')
		default:
			return "", fmt.Errorf("invalid JSON pointer escape in %q", value)
		}
	}
	return out.String(), nil
}
