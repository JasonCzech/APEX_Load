package bruno

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"gopkg.in/yaml.v3"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/scrub"
)

const nativeURLRequirementKeyPrefix = "native-url:"

// sanitizeNativeURLCredentials removes credential material from request URL
// query values and query-parameter definitions before either source mappings or
// native candidates are exposed. Runtime query interpolation remains
// review-gated because APEX cannot reproduce Bruno's post-expansion encoding.
func sanitizeNativeURLCredentials(
	files []sourceFile,
) ([]sourceFile, []SecretRequirement, int, error) {
	out := make([]sourceFile, 0, len(files))
	requirements := make([]SecretRequirement, 0)
	sanitizedValues := 0
	for _, file := range files {
		extension := strings.ToLower(path.Ext(file.Path))
		if (extension != ".yml" && extension != ".yaml") ||
			classifyFileRole(file.Path, file.Data) != "request" {
			out = append(out, file)
			continue
		}
		var document map[string]any
		if err := yaml.Unmarshal(file.Data, &document); err != nil {
			return nil, nil, 0, fmt.Errorf("parse native URL request %q: %w", file.Path, err)
		}
		httpBlock := nativeMapValue(document, "http")
		changed := false
		rawTarget := anyString(nativeLookup(httpBlock, "url"))
		if strings.TrimSpace(rawTarget) != "" {
			rewritten, current, targetChanged, err := sanitizeNativeRawURL(file.Path, rawTarget)
			if err != nil {
				return nil, nil, 0, fmt.Errorf("sanitize native URL in %q: %w", file.Path, err)
			}
			if targetChanged {
				if _, err := setNativeJSONPointerWithAliasPeers(
					document,
					file.nativeAliasGroups,
					"/http/url",
					rewritten,
				); err != nil {
					return nil, nil, 0, fmt.Errorf("write sanitized native URL in %q: %w", file.Path, err)
				}
				changed = true
				sanitizedValues += len(current)
				requirements = append(requirements, current...)
			}
		}

		if params, ok := nativeLookup(httpBlock, "params").([]any); ok {
			for index, raw := range params {
				param, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				kind := strings.ToLower(strings.TrimSpace(anyString(nativeLookup(param, "type"))))
				if kind != "query" && kind != "path" {
					continue
				}
				name := strings.TrimSpace(anyString(nativeLookup(param, "name")))
				value, ok := nativeLookup(param, "value").(string)
				if !ok || !nativeSensitiveURLValue(name, value) ||
					strings.TrimSpace(value) == "" ||
					isExactGovernedSecretReference(value) {
					continue
				}
				pointer := "/http/params/" + strconv.Itoa(index) + "/value"
				if nativeBool(nativeLookup(param, "disabled")) {
					if _, err := setNativeJSONPointerWithAliasPeers(
						document,
						file.nativeAliasGroups,
						pointer,
						scrub.Redacted,
					); err != nil {
						return nil, nil, 0, fmt.Errorf("scrub disabled native query parameter in %q: %w", file.Path, err)
					}
					changed = true
					continue
				}
				if brunoTemplatePattern.MatchString(value) || !shouldSanitizeCredentialValue(value) {
					continue
				}
				requirement := nativeURLSecretRequirement(
					file.Path,
					pointer,
					name,
					index+1,
					value,
				)
				if _, err := setNativeJSONPointerWithAliasPeers(
					document,
					file.nativeAliasGroups,
					pointer,
					nativeCredentialProjectionReplacement(requirement),
				); err != nil {
					return nil, nil, 0, fmt.Errorf("write sanitized native query parameter in %q: %w", file.Path, err)
				}
				changed = true
				sanitizedValues++
				requirements = append(requirements, requirement)
			}
		}
		if !changed {
			out = append(out, file)
			continue
		}
		content, err := yaml.Marshal(document)
		if err != nil {
			return nil, nil, 0, fmt.Errorf("serialize sanitized native URL request %q: %w", file.Path, err)
		}
		out = append(out, sourceFile{
			Path:              file.Path,
			Data:              content,
			Mode:              file.Mode,
			nativeAliasGroups: file.nativeAliasGroups,
		})
	}
	sort.Slice(requirements, func(i, j int) bool {
		if requirements[i].Path == requirements[j].Path {
			return requirements[i].Key < requirements[j].Key
		}
		return requirements[i].Path < requirements[j].Path
	})
	return out, requirements, sanitizedValues, nil
}

func sanitizeNativeRawURL(
	sourcePath, rawTarget string,
) (string, []SecretRequirement, bool, error) {
	if nativeURLHasUserinfo(rawTarget) {
		return "", nil, false, fmt.Errorf(
			"authored URL userinfo is not supported; move the credentials to governed request auth",
		)
	}
	if nativeURLHasSensitiveFragment(rawTarget) {
		return "", nil, false, fmt.Errorf(
			"authored URL fragment contains credential material and is not supported",
		)
	}
	queryStart := strings.IndexByte(rawTarget, '?')
	if queryStart < 0 {
		return rawTarget, nil, false, nil
	}
	queryEnd := len(rawTarget)
	if fragment := strings.IndexByte(rawTarget[queryStart+1:], '#'); fragment >= 0 {
		queryEnd = queryStart + 1 + fragment
	}
	rawQuery := rawTarget[queryStart+1 : queryEnd]
	parts := strings.Split(rawQuery, "&")
	requirements := make([]SecretRequirement, 0)
	changed := false
	for index, part := range parts {
		rawName, rawValue, hasValue := strings.Cut(part, "=")
		name, err := url.QueryUnescape(rawName)
		if err != nil {
			name = rawName
		}
		if !hasValue ||
			!nativeSensitiveURLValue(name, rawValue) ||
			strings.TrimSpace(rawValue) == "" ||
			brunoTemplatePattern.MatchString(rawValue) ||
			isExactGovernedSecretReference(rawValue) ||
			!shouldSanitizeCredentialValue(rawValue) {
			continue
		}
		pointer := "/http/url/query/" + strconv.Itoa(index) + "/value"
		requirement := nativeURLSecretRequirement(
			sourcePath,
			pointer,
			name,
			index+1,
			rawValue,
		)
		parts[index] = rawName + "=" + nativeCredentialProjectionReplacement(requirement)
		requirements = append(requirements, requirement)
		changed = true
	}
	if !changed {
		return rawTarget, nil, false, nil
	}
	return rawTarget[:queryStart+1] +
		strings.Join(parts, "&") +
		rawTarget[queryEnd:], requirements, true, nil
}

func nativeURLHasUserinfo(rawTarget string) bool {
	target := strings.TrimSpace(rawTarget)
	authorityStart := -1
	if scheme := strings.Index(target, "://"); scheme >= 0 {
		authorityStart = scheme + 3
	} else if strings.HasPrefix(target, "//") {
		authorityStart = 2
	}
	if authorityStart < 0 || authorityStart >= len(target) {
		return false
	}
	authorityEnd := len(target)
	for _, separator := range []byte{'/', '?', '#'} {
		if offset := strings.IndexByte(target[authorityStart:], separator); offset >= 0 &&
			authorityStart+offset < authorityEnd {
			authorityEnd = authorityStart + offset
		}
	}
	return strings.Contains(target[authorityStart:authorityEnd], "@")
}

func nativeSensitiveQueryName(name string) bool {
	normalized := strings.ToLower(strings.TrimSpace(name))
	return scrub.SensitiveName(name) || normalized == "sig"
}

func nativeSensitiveURLValue(name, value string) bool {
	return nativeSensitiveQueryName(name) ||
		strings.Contains(value, "{{secret.") ||
		scrub.URLTokenSensitive(value)
}

func nativeURLHasSensitiveFragment(rawTarget string) bool {
	fragmentStart := strings.IndexByte(rawTarget, '#')
	if fragmentStart < 0 {
		return false
	}
	fragment := rawTarget[fragmentStart+1:]
	for _, part := range strings.Split(fragment, "&") {
		name, value, hasValue := strings.Cut(part, "=")
		if hasValue {
			if nativeSensitiveURLValue(name, value) {
				return true
			}
			continue
		}
		if scrub.URLTokenSensitive(part) {
			return true
		}
	}
	return false
}

func nativeURLSecretRequirement(
	sourcePath, pointer, name string,
	occurrence int,
	value string,
) SecretRequirement {
	identity := pointer + "\x00" + strings.ToLower(name) + "\x00" + strconv.Itoa(occurrence)
	nameHash := sha256.Sum256([]byte("bruno-native-url-name\x00" + sourcePath + "\x00" + identity))
	suffix := "_" + strings.ToUpper(hex.EncodeToString(nameHash[:6]))
	suggested := secretName(sourcePath, "query_"+name)
	if len(suggested)+len(suffix) > credential.MaxLogicalNameBytes {
		suggested = strings.TrimRight(suggested[:credential.MaxLogicalNameBytes-len(suffix)], "_")
	}
	suggested += suffix
	idHash := sha256.Sum256([]byte("bruno-native-url\x00" + sourcePath + "\x00" + identity))
	return SecretRequirement{
		ID:            hex.EncodeToString(idHash[:12]),
		Path:          sourcePath,
		Key:           nativeURLRequirementKeyPrefix + pointer,
		SuggestedName: suggested,
		Replacement:   "{{secret." + suggested + "}}",
		Context:       "Bruno URL component credential " + name,
		Value:         recoverableNativeURLCredentialValue(value),
		AffectedPaths: nativeSecretRequirementPaths(sourcePath),
		readableName:  readableNativeSecretBase(name),
	}
}

func recoverableNativeURLCredentialValue(value string) string {
	decoded, stable := decodeBrunoCredentialValueToStable(value, url.QueryUnescape)
	if !stable || containsBrunoCredentialRedactionSentinel(decoded) {
		return ""
	}
	return recoverableBrunoCredentialValue(value)
}

func rewriteNativeURLTemplates(
	rawTarget string,
	state *nativeTemplateState,
	sourceRequirements map[string][]SecretRequirement,
) string {
	queryStart := strings.IndexByte(rawTarget, '?')
	if queryStart < 0 {
		return rewriteNativeTemplates(rawTarget, nativeTemplateOrdinary, "", state)
	}
	queryEnd := len(rawTarget)
	if fragment := strings.IndexByte(rawTarget[queryStart+1:], '#'); fragment >= 0 {
		queryEnd = queryStart + 1 + fragment
	}
	var rewritten strings.Builder
	rewritten.WriteString(rewriteNativeTemplates(
		rawTarget[:queryStart+1],
		nativeTemplateOrdinary,
		"",
		state,
	))
	parts := strings.Split(rawTarget[queryStart+1:queryEnd], "&")
	nameOccurrences := make(map[string]int, len(parts))
	for index, part := range parts {
		if index > 0 {
			rewritten.WriteByte('&')
		}
		rawName, rawValue, hasValue := strings.Cut(part, "=")
		rewritten.WriteString(rewriteNativeTemplates(
			rawName,
			nativeTemplateOrdinary,
			"",
			state,
		))
		name, err := url.QueryUnescape(rawName)
		if err != nil {
			name = rawName
		}
		nameIdentity := strings.ToLower(name)
		nameOccurrences[nameIdentity]++
		occurrence := nameOccurrences[nameIdentity]
		if !hasValue {
			continue
		}
		rewritten.WriteByte('=')
		if nativeSensitiveURLValue(name, rawValue) &&
			brunoTemplatePattern.MatchString(rawValue) &&
			!isExactGovernedSecretReference(rawValue) {
			pointer := "/http/url/query/" + strconv.Itoa(index) + "/value"
			value, _ := rewriteNativeComposedURLCredential(
				rawValue,
				pointer,
				name,
				occurrence,
				state,
				sourceRequirements,
				nativeSecretScrubTarget{
					Path:     state.sourcePath,
					Pointer:  "/http/url",
					Kind:     "url_query_value",
					Selector: name,
					Index:    occurrence,
				},
			)
			rewritten.WriteString(value)
			continue
		}
		context := nativeTemplateOrdinary
		if nativeSensitiveURLValue(name, rawValue) {
			context = nativeTemplateSensitiveHeader
		}
		rewritten.WriteString(rewriteNativeTemplates(rawValue, context, name, state))
	}
	rewritten.WriteString(rewriteNativeTemplates(
		rawTarget[queryEnd:],
		nativeTemplateOrdinary,
		"",
		state,
	))
	return rewritten.String()
}

func rewriteNativeComposedURLCredential(
	value, pointer, name string,
	occurrence int,
	state *nativeTemplateState,
	sourceRequirements map[string][]SecretRequirement,
	usageTarget nativeSecretScrubTarget,
) (string, bool) {
	if secretReference, ok := exactNativeSecretReference(value); ok {
		return secretReference, true
	}
	sourceVariable, hasExactSourceVariable := exactNativeCredentialSourceReference(value)
	requirement := nativeURLSecretRequirement(
		state.sourcePath,
		pointer,
		name,
		occurrence,
		"",
	)
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
	context := "Bruno URL component credential " + name
	if hasExactSourceVariable {
		if len(dependencies) > 1 {
			// A chained source may include request-local overrides. Keep the
			// URL usage identity in that case so different resolved values do
			// not collapse into one destination binding.
			requirement.Context = context
			requirement.scrubTargets = append(
				nativeVariableScrubTargets(dependencies),
				usageTarget,
			)
		} else if len(dependencies) > 0 {
			requirement = nativeVariableSecretRequirement(
				dependencies[0],
				state.sourcePath,
				context,
			)
			requirement.scrubTargets = append(
				nativeVariableScrubTargets(dependencies),
				usageTarget,
			)
		} else {
			requirement = nativeUnresolvedSourceVariableSecretRequirement(
				sourceVariable,
				state.sourcePath,
				context,
			)
			requirement.scrubTargets = []nativeSecretScrubTarget{usageTarget}
		}
		requirement.DisplayLabel = nativeSourceVariableDisplayLabel(sourceVariable)
		requirement.SourceVariable = sourceVariable
		requirement.UnresolvedSourceVariable = requirement.Value == ""
		requirement.AffectedPaths = nativeSecretRequirementPaths(
			append(requirement.AffectedPaths, state.sourcePath)...,
		)
	} else {
		requirement.Context = context
		requirement.scrubTargets = append(
			nativeVariableScrubTargets(dependencies),
			usageTarget,
		)
	}
	if complete && !brunoTemplatePattern.MatchString(resolved) {
		requirement.Value = recoverableNativeURLCredentialValue(resolved)
		if hasExactSourceVariable {
			requirement.UnresolvedSourceVariable = requirement.Value == ""
		}
	}
	registerNativeGeneratedRequirement(state, requirement)
	return nativeCredentialProjectionReplacement(requirement), complete
}

func setNativeURLQueryValue(
	document any,
	pointer string,
	selector string,
	occurrence int,
	replacement string,
) (bool, error) {
	rawValue, err := nativeJSONPointerValue(document, pointer)
	if err != nil {
		return false, err
	}
	rawTarget, ok := rawValue.(string)
	if !ok {
		return false, fmt.Errorf("URL query scrub target %q is not a string", pointer)
	}
	queryStart := strings.IndexByte(rawTarget, '?')
	if queryStart < 0 {
		return false, fmt.Errorf("URL query scrub target has no query")
	}
	queryEnd := len(rawTarget)
	if fragment := strings.IndexByte(rawTarget[queryStart+1:], '#'); fragment >= 0 {
		queryEnd = queryStart + 1 + fragment
	}
	parts := strings.Split(rawTarget[queryStart+1:queryEnd], "&")
	if occurrence < 1 {
		return false, fmt.Errorf("URL query scrub occurrence %d is invalid", occurrence)
	}
	index := -1
	seen := 0
	for partIndex, part := range parts {
		rawName, _, _ := strings.Cut(part, "=")
		name, decodeErr := url.QueryUnescape(rawName)
		if decodeErr != nil {
			name = rawName
		}
		if !strings.EqualFold(name, selector) {
			continue
		}
		seen++
		if seen == occurrence {
			index = partIndex
			break
		}
	}
	if index < 0 {
		return false, fmt.Errorf(
			"URL query scrub target %q occurrence %d was not found",
			selector,
			occurrence,
		)
	}
	name, current, hasValue := strings.Cut(parts[index], "=")
	if !hasValue {
		return false, fmt.Errorf(
			"URL query scrub target %q occurrence %d has no value",
			selector,
			occurrence,
		)
	}
	if current == replacement {
		return false, nil
	}
	parts[index] = name + "=" + replacement
	rewritten := rawTarget[:queryStart+1] +
		strings.Join(parts, "&") +
		rawTarget[queryEnd:]
	return setNativeJSONPointer(document, pointer, rewritten)
}

func nativeURLHasSensitiveQuery(rawTarget string) bool {
	queryStart := strings.IndexByte(rawTarget, '?')
	if queryStart < 0 {
		return false
	}
	query := rawTarget[queryStart+1:]
	if fragment := strings.IndexByte(query, '#'); fragment >= 0 {
		query = query[:fragment]
	}
	for _, part := range strings.Split(query, "&") {
		rawName, rawValue, _ := strings.Cut(part, "=")
		name, err := url.QueryUnescape(rawName)
		if err != nil {
			name = rawName
		}
		if nativeSensitiveURLValue(name, rawValue) {
			return true
		}
	}
	return false
}

func nativeHTTPParamsContainSensitiveCredentials(params []nativeHTTPParam) bool {
	for _, param := range params {
		if !param.Disabled && nativeSensitiveURLValue(param.Name, param.Value) {
			return true
		}
	}
	return false
}
