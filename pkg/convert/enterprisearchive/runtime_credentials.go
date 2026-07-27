package enterprisearchive

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"github.com/jasonczech/apexload/pkg/scrub"
)

const (
	enterpriseProxyUsernameSecretName    = "ENTERPRISE_PROXY_USERNAME"
	enterpriseProxyPasswordSecretName    = "ENTERPRISE_PROXY_PASSWORD"
	enterpriseHTTPAuthPasswordSecretName = "ENTERPRISE_HTTP_AUTH_PASSWORD"
	enterpriseHTTPAuthTokenSecretName    = "ENTERPRISE_HTTP_AUTH_TOKEN"
)

type enterpriseRuntimeSourceCredential struct {
	value    string
	source   credentialpolicy.SourceRef
	nameHint string
	label    string
}

// applyEnterpriseRuntimeBindings maps adapter-specific runtime surfaces through
// the shared credential requirement/reference primitives and retains raw source
// values only in ConvertResult's one-shot protected handoff.
func applyEnterpriseRuntimeBindings(
	result *ConvertResult,
	scripts []*dsl.Script,
	ctx archiveFinalizationContext,
) {
	if result == nil || len(scripts) == 0 {
		return
	}

	requirements := result.ProtectedRequirements.Take()
	seenIDs := make(map[string]struct{}, len(requirements))
	coveredReplacements := make(map[string]struct{}, len(requirements))
	for _, requirement := range requirements {
		seenIDs[requirement.ID] = struct{}{}
		if replacement := strings.TrimSpace(requirement.Replacement); replacement != "" {
			coveredReplacements[replacement] = struct{}{}
		}
	}
	registry := credentialpolicy.NewNameRegistry()
	for _, requirement := range requirements {
		registry.Reserve(requirement.SuggestedName)
		if name, ok := credential.ParseReference(requirement.Replacement); ok {
			registry.Reserve(name)
		}
	}
	sourceValues := enterpriseRuntimeSourceValues(ctx)

	appendRequirement := func(requirement credentialpolicy.Requirement) {
		if requirement.ID == "" {
			return
		}
		if _, exists := seenIDs[requirement.ID]; exists {
			return
		}
		seenIDs[requirement.ID] = struct{}{}
		if replacement := strings.TrimSpace(requirement.Replacement); replacement != "" {
			coveredReplacements[replacement] = struct{}{}
		}
		registry.Reserve(requirement.SuggestedName)
		if name, ok := credential.ParseReference(requirement.Replacement); ok {
			registry.Reserve(name)
		}
		requirements = append(requirements, requirement)
	}
	appendProjectedRequirements := func(projected []credentialpolicy.Requirement) {
		for _, requirement := range projected {
			if requirement.ExistingReference {
				if _, covered := coveredReplacements[strings.TrimSpace(requirement.Replacement)]; covered {
					continue
				}
			}
			appendRequirement(requirement)
		}
	}

	project := func(value, field, key, label, nameHint string, occurrence int) string {
		if strings.TrimSpace(value) == "" {
			return ""
		}

		sourceCredential, hasSourceCredential := sourceValues[field]
		if sourceCredential.nameHint != "" {
			nameHint = sourceCredential.nameHint
		}
		if sourceCredential.label != "" {
			label = sourceCredential.label
		}
		source := sourceCredential.source
		if source.Format == "" {
			source = credentialpolicy.SourceRef{
				Format:  "enterprise_script_archive",
				Pointer: field,
			}
		}

		if _, existingReference := credential.ParseReference(value); existingReference {
			if _, alreadyCovered := coveredReplacements[value]; alreadyCovered {
				return value
			}
			expected := credential.DraftReference(sourceCredential.value, nameHint)
			_, sourceIsReference := credential.ParseReference(sourceCredential.value)
			if !hasSourceCredential || sourceCredential.value == "" ||
				expected != value || sourceIsReference {
				return value
			}
			requirement, projected, err := credentialpolicy.NewRequirement(credentialpolicy.RequirementSpec{
				Key:      key,
				Label:    label,
				Context:  field,
				NameHint: nameHint,
				Source:   source,
				Targets: []credentialpolicy.TargetRef{{
					Field:      field,
					Occurrence: occurrence,
				}},
				Value: sourceCredential.value,
				Mode:  credentialpolicy.SecretReference,
			})
			if err != nil {
				return value
			}
			appendRequirement(requirement)
			return projected
		}

		// A caller-supplied runtime override may still carry a literal even though
		// RuntimeConfig itself now projects safe draft references. Govern it using
		// the same source-neutral requirement contract.
		requirement, projected, err := credentialpolicy.NewRequirement(credentialpolicy.RequirementSpec{
			Key:      key,
			Label:    label,
			Context:  field,
			NameHint: nameHint,
			Source:   source,
			Targets: []credentialpolicy.TargetRef{{
				Field:      field,
				Occurrence: occurrence,
			}},
			Value:    value,
			Mode:     credentialpolicy.SecretReference,
			Registry: registry,
		})
		if err != nil {
			return credential.DraftReference(value, nameHint)
		}
		appendRequirement(requirement)
		return projected
	}

	for scriptIndex, script := range scripts {
		if script == nil {
			continue
		}
		candidateKey := fmt.Sprintf("enterprise:%d", scriptIndex)
		httpConfig := &script.Config.HTTP
		httpConfig.Proxy.URL, httpConfig.Proxy.Username, httpConfig.Proxy.Password =
			splitEnterpriseProxyUserinfo(
				httpConfig.Proxy.URL,
				httpConfig.Proxy.Username,
				httpConfig.Proxy.Password,
			)
		if strings.TrimSpace(httpConfig.Proxy.URL) != "" {
			proxySource := sourceValues["config.http.proxy.url"].source
			if proxySource.Format == "" {
				proxySource = credentialpolicy.SourceRef{
					Format:  "enterprise_script_archive",
					Pointer: "config.http.proxy.url",
				}
			}
			projectedURL, projection, err := credentialpolicy.ProjectRawURL(
				httpConfig.Proxy.URL,
				credentialpolicy.Options{
					Mode:        credentialpolicy.SecretReference,
					Source:      proxySource,
					Registry:    registry,
					TargetField: "config.http.proxy.url",
				},
			)
			if err != nil ||
				projection.Disposition == credentialpolicy.BlockedDraft ||
				projection.Disposition == credentialpolicy.Rejected {
				httpConfig.ProxyMode = "none"
				httpConfig.Proxy = dsl.ProxyConfig{}
				finding := importreview.Finding{
					Code:       "import_review_required",
					Severity:   diag.Blocker,
					Element:    "HTTP proxy URL",
					Detail:     "the source proxy URL could not be projected into an exact disclosure-safe runtime value",
					Location:   proxySource.Pointer,
					Source:     "enterprise_script_archive",
					ReasonCode: "runtime_proxy_url_credential_projection_failed",
					SourceRef:  proxySource.Pointer,
					Resolution: "replace_behavior_and_remove_gate",
					Gate:       importreview.GateExecution,
				}
				_ = dslwalk.WalkActions(script, func(ref dslwalk.ActionRef) error {
					actionFinding := finding
					actionFinding.ActionPath = ref.Path
					importreview.ApplyToAction(ref.Action, []importreview.Finding{actionFinding})
					return nil
				})
				result.Warnings = append(
					result.Warnings,
					"HTTP proxy URL credential projection failed; proxying was disabled and the generated draft requires review before execution.",
				)
			} else {
				httpConfig.Proxy.URL = projectedURL
				for _, requirement := range projection.Requirements {
					if requirement.ExistingReference {
						if _, covered := coveredReplacements[strings.TrimSpace(requirement.Replacement)]; covered {
							continue
						}
					}
					appendRequirement(requirement)
				}
			}
		}
		httpConfig.Proxy.Username = project(
			httpConfig.Proxy.Username,
			"config.http.proxy.username",
			"proxy_username",
			"Proxy username",
			enterpriseProxyUsernameSecretName,
			scriptIndex,
		)
		httpConfig.Proxy.Password = project(
			httpConfig.Proxy.Password,
			"config.http.proxy.password",
			"proxy_password",
			"Proxy password",
			enterpriseProxyPasswordSecretName,
			scriptIndex,
		)
		httpConfig.Auth.Password = project(
			httpConfig.Auth.Password,
			"config.http.auth.password",
			"password",
			"HTTP authentication password",
			enterpriseHTTPAuthPasswordSecretName,
			scriptIndex,
		)
		httpConfig.Auth.Token = project(
			httpConfig.Auth.Token,
			"config.http.auth.token",
			"token",
			"HTTP authentication token",
			enterpriseHTTPAuthTokenSecretName,
			scriptIndex,
		)

		headerNames := make([]string, 0, len(httpConfig.DefaultHeaders))
		for name := range httpConfig.DefaultHeaders {
			if credential.SensitiveHeader(name) {
				headerNames = append(headerNames, name)
			}
		}
		sort.SliceStable(headerNames, func(i, j int) bool {
			return strings.ToLower(headerNames[i]) < strings.ToLower(headerNames[j])
		})
		for headerIndex, name := range headerNames {
			field := "config.http.default_headers." + strings.ToLower(strings.TrimSpace(name))
			httpConfig.DefaultHeaders[name] = project(
				httpConfig.DefaultHeaders[name],
				field,
				name,
				name,
				"ENTERPRISE_DEFAULT_HEADER_"+credential.EnvKey(name),
				scriptIndex*1000+headerIndex,
			)
		}

		attributeNames := make([]string, 0, len(script.Config.WebRuntime.AdditionalAttributes))
		for name := range script.Config.WebRuntime.AdditionalAttributes {
			if credential.SensitiveName(name) {
				attributeNames = append(attributeNames, name)
			}
		}
		sort.SliceStable(attributeNames, func(i, j int) bool {
			return strings.ToLower(attributeNames[i]) < strings.ToLower(attributeNames[j])
		})
		for attributeIndex, name := range attributeNames {
			field := "config.web_runtime.additional_attributes." + strings.ToLower(strings.TrimSpace(name))
			script.Config.WebRuntime.AdditionalAttributes[name] = project(
				script.Config.WebRuntime.AdditionalAttributes[name],
				field,
				name,
				name,
				"ENTERPRISE_RUNTIME_"+credential.EnvKey(name),
				scriptIndex*1000+attributeIndex,
			)
		}

		additionalURLs := make([]requestimport.ScriptCredentialURL, len(result.RecordedResourceURLs))
		for index, value := range result.RecordedResourceURLs {
			additionalURLs[index] = requestimport.ScriptCredentialURL{
				Value:       value,
				TargetField: fmt.Sprintf("metadata.import.preserved.recorded_resource_urls[%d]", index),
				Element:     "recorded resource URL",
				Source: requestimport.SourceRef{
					Format:  "enterprise_script_archive",
					Pointer: fmt.Sprintf("/recorded_resource_urls/%d", index),
				},
			}
		}
		projected, projectionErr := requestimport.ProjectScriptCredentials(
			script,
			requestimport.ScriptCredentialProjectionOptions{
				SourceProfile:          "enterprise_script_archive",
				CandidateKey:           candidateKey,
				NamePrefix:             "ENTERPRISE_REQUEST",
				Registry:               registry,
				AdditionalURLs:         additionalURLs,
				RuntimeParameterPolicy: requestimport.RuntimeParametersPreserveGoverned,
			},
		)
		appendProjectedRequirements(projected.Requirements)
		if len(projected.AdditionalURLs) == len(result.RecordedResourceURLs) {
			result.RecordedResourceURLs = projected.AdditionalURLs
		}
		if projectionErr != nil {
			result.RecordedResourceURLs = nil
			result.Warnings = append(
				result.Warnings,
				"HTTP credential review could not safely traverse the generated request tree; request details were removed and the draft requires review.",
			)
		}
	}

	result.ProtectedRequirements.Store(requirements)
	clearEnterpriseRuntimeSourceValues(ctx.RuntimeConfig)
	for index := range requirements {
		requirements[index].Value = ""
	}
}

func enterpriseRuntimeSourceValues(ctx archiveFinalizationContext) map[string]enterpriseRuntimeSourceCredential {
	if ctx.RuntimeConfig == nil {
		return nil
	}
	runtimeFile := normalizeRuntimeArchivePath(ctx.RuntimeFileName)
	source := func(pointer string) credentialpolicy.SourceRef {
		location := pointer
		if runtimeFile != "" {
			location = runtimeFile + "#" + pointer
		}
		return credentialpolicy.SourceRef{
			Format:  "enterprise_script_archive",
			Path:    runtimeFile,
			Pointer: location,
		}
	}
	_, embeddedProxyUser, embeddedProxyPass :=
		splitEnterpriseProxyUserinfo(ctx.RuntimeConfig.ProxyURL, "", "")
	proxyUser := ctx.RuntimeConfig.ProxyUser
	proxyUserSource := source("Proxy/ProxyUserName")
	if strings.TrimSpace(proxyUser) == "" && embeddedProxyUser != "" {
		proxyUser = embeddedProxyUser
		proxyUserSource = source("Proxy/ProxyServer")
	}
	proxyPass := ctx.RuntimeConfig.ProxyPass
	proxyPassSource := source("Proxy/ProxyPassword")
	if strings.TrimSpace(proxyPass) == "" && embeddedProxyPass != "" {
		proxyPass = embeddedProxyPass
		proxyPassSource = source("Proxy/ProxyServer")
	}
	out := map[string]enterpriseRuntimeSourceCredential{
		"config.http.proxy.url": {
			source: source("Proxy/ProxyServer"),
		},
		"config.http.proxy.username": {
			value:    proxyUser,
			source:   proxyUserSource,
			nameHint: enterpriseProxyUsernameSecretName,
			label:    "Proxy username",
		},
		"config.http.proxy.password": {
			value:    proxyPass,
			source:   proxyPassSource,
			nameHint: enterpriseProxyPasswordSecretName,
			label:    "Proxy password",
		},
		"config.http.auth.password": {
			value:    ctx.RuntimeConfig.AuthPass,
			source:   source("Authentication/Password"),
			nameHint: enterpriseHTTPAuthPasswordSecretName,
			label:    "HTTP authentication password",
		},
	}
	for name, value := range ctx.RuntimeConfig.AdditionalAttributes {
		if !credential.SensitiveName(name) || strings.TrimSpace(value) == "" {
			continue
		}
		field := "config.web_runtime.additional_attributes." + strings.ToLower(strings.TrimSpace(name))
		out[field] = enterpriseRuntimeSourceCredential{
			value:    value,
			source:   source("Additional Attributes/" + name),
			nameHint: "ENTERPRISE_RUNTIME_" + credential.EnvKey(name),
			label:    name,
		}
	}
	return out
}

func clearEnterpriseRuntimeSourceValues(config *RuntimeConfig) {
	if config == nil {
		return
	}
	config.ProxyURL = disclosureSafeProxyEndpoint(config.ProxyURL)
	config.ProxyUser = ""
	config.ProxyPass = ""
	config.AuthPass = ""
	for name := range config.AdditionalAttributes {
		if credential.SensitiveName(name) {
			config.AdditionalAttributes[name] = credential.Redacted
		}
	}
	// Defense in depth for callers that construct RuntimeConfig directly rather
	// than through ParseRuntimeCfg. Never retain a sensitive -D assignment in
	// executable argv or any preserved raw JVM row.
	config.JavaVMAdditionalParameters = executableJavaVMParameters(config.JavaVMAdditionalParameters)
	config.JavaVMParametersRaw = disclosureSafeJavaVMParameterRow(config.JavaVMParametersRaw)
	for index := range config.JavaVMParameterRows {
		config.JavaVMParameterRows[index] = disclosureSafeJavaVMParameterRow(config.JavaVMParameterRows[index])
	}
}

// splitEnterpriseProxyUserinfo ensures credentials embedded in a proxy URL do
// not survive as an ungoverned duplicate. Explicit fields retain precedence.
func splitEnterpriseProxyUserinfo(rawURL, username, password string) (string, string, string) {
	trimmed := strings.TrimSpace(rawURL)
	if fragment := strings.IndexByte(trimmed, '#'); fragment >= 0 {
		// URL fragments are never part of a proxy endpoint request. Removing
		// them avoids retaining an inert credential-bearing suffix while
		// preserving every transport-relevant proxy byte.
		trimmed = trimmed[:fragment]
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.User == nil {
		return trimmed, username, password
	}
	if strings.TrimSpace(username) == "" {
		username = parsed.User.Username()
	}
	if strings.TrimSpace(password) == "" {
		password, _ = parsed.User.Password()
	}
	parsed.User = nil
	return parsed.String(), username, password
}

func disclosureSafeRuntimeSections(
	input map[string]map[string]map[string]string,
) map[string]map[string]map[string]string {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]map[string]map[string]string, len(input))
	for file, sections := range input {
		out[file] = disclosureSafeConfigSections(sections)
	}
	return out
}

func disclosureSafeConfigSections(
	input map[string]map[string]string,
) map[string]map[string]string {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]map[string]string, len(input))
	for section, values := range input {
		safeValues := make(map[string]string, len(values))
		for key, value := range values {
			if credential.SensitiveRuntimeSetting(section, key) {
				safeValues[key] = credential.Redacted
			} else if javaVMParameterRuntimeSetting(section, key) {
				safeValues[key] = disclosureSafeJavaVMParameterRow(value)
			} else if enterpriseRuntimeProxyEndpointKey(section, key) {
				safeValues[key] = disclosureSafeProxyEndpoint(value)
			} else {
				safeValues[key] = scrub.Value(value, scrub.Policy{})
			}
		}
		out[section] = safeValues
	}
	return out
}

func disclosureSafeParameterSections(
	input map[string]map[string]map[string]string,
) map[string]map[string]map[string]string {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]map[string]map[string]string, len(input))
	for file, sections := range input {
		safeSections := make(map[string]map[string]string, len(sections))
		for section, values := range sections {
			sensitiveParameter := enterpriseParameterSectionSensitive(section, values)
			safeValues := make(map[string]string, len(values))
			for key, value := range values {
				switch {
				case credential.SensitiveName(key):
					safeValues[key] = credential.Redacted
				case sensitiveParameter && enterpriseParameterLiteralValueKey(key):
					safeValues[key] = credential.Redacted
				default:
					safeValues[key] = scrub.Value(value, scrub.Policy{})
				}
			}
			safeSections[section] = safeValues
		}
		out[file] = safeSections
	}
	return out
}

func enterpriseParameterSectionSensitive(
	section string,
	values map[string]string,
) bool {
	if credential.SensitiveName(cleanParameterName(section)) {
		return true
	}
	for key, value := range values {
		switch credential.NormalizeName(key) {
		case "paramname", "name", "columnname", "column":
			if credential.SensitiveName(cleanConfigValue(value)) {
				return true
			}
		}
	}
	return false
}

func enterpriseParameterLiteralValueKey(key string) bool {
	normalized := credential.NormalizeName(key)
	switch normalized {
	case "value", "values", "valuelist", "originalvalue", "currentvalue",
		"defaultvalue", "startvalue", "endvalue", "seedvalue":
		return true
	default:
		return strings.HasPrefix(normalized, "valuelist") ||
			strings.HasPrefix(normalized, "originalvalue")
	}
}

func javaVMParameterRuntimeSetting(section, key string) bool {
	if runtimeConfigCanonicalSection(section) != "javavm" {
		return false
	}
	if runtimeConfigCanonicalKey("javavm", key) == "javavmparameters" {
		return true
	}
	_, ok := javaVMParameterExtraArgIndex(key)
	return ok
}

func enterpriseRuntimeProxyEndpointKey(section, key string) bool {
	switch strings.ToLower(strings.TrimSpace(section)) {
	case "proxy", "web":
	default:
		return false
	}
	switch credential.NormalizeName(key) {
	case "proxyserver", "proxyurl", "server", "proxyhttphost", "proxyhttpshost":
		return true
	default:
		return false
	}
}

// disclosureSafeProxyEndpoint removes URL userinfo from every runtime proxy
// endpoint spelling understood by parseRuntimeProxySettings. If malformed
// source text still places an @ in the authority and cannot be parsed safely,
// retain only the disclosure sentinel.
func disclosureSafeProxyEndpoint(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return value
	}
	candidate := trimmed
	addedScheme := !strings.Contains(candidate, "://")
	if addedScheme {
		candidate = "http://" + candidate
	}
	parsed, err := url.Parse(candidate)
	if err != nil || parsed.User == nil {
		if enterpriseProxyAuthorityContainsUserinfo(trimmed) {
			return credential.Redacted
		}
		safe := scrub.ValueURL(trimmed, scrub.Policy{})
		return strings.Replace(value, trimmed, safe, 1)
	}
	parsed.User = nil
	safe := parsed.String()
	if addedScheme {
		safe = strings.TrimPrefix(safe, "http://")
	}
	safe = scrub.ValueURL(safe, scrub.Policy{})
	return strings.Replace(value, trimmed, safe, 1)
}

func enterpriseProxyAuthorityContainsUserinfo(value string) bool {
	authority := strings.TrimSpace(value)
	if separator := strings.Index(authority, "://"); separator >= 0 {
		authority = authority[separator+3:]
	}
	if end := strings.IndexAny(authority, "/?#"); end >= 0 {
		authority = authority[:end]
	}
	return strings.Contains(authority, "@")
}

// disclosureSafeEnterpriseSourceReview applies enterprise runtime semantics
// after the generic source-review scrubber. The generic vocabulary
// intentionally does not classify usernames or URL userinfo as credentials,
// while enterprise proxy replay does; those values must therefore be removed
// before the review is attached to converter/API JSON.
func disclosureSafeEnterpriseSourceReview(review *SourceReview) {
	if review == nil {
		return
	}
	for index := range review.SourceFiles {
		source := &review.SourceFiles[index]
		switch strings.ToLower(pathExtension(source.Path)) {
		case ".cfg", ".usp", ".usr", ".ini":
			safe := disclosureSafeEnterpriseINIContent(source.Content)
			if safe != source.Content {
				source.Content = safe
				source.Redacted = true
			}
		case ".prm":
			safe := disclosureSafeEnterprisePRMContent(source.Content)
			if safe != source.Content {
				source.Content = safe
				source.Redacted = true
			}
		case ".c", ".h", ".java":
			safe := scrub.Value(source.Content, scrub.Policy{})
			safe = redactEnterpriseCallArgument(safe, "web_set_user", 1)
			if safe != source.Content {
				source.Content = safe
				source.Redacted = true
			}
		}
	}
}

func finalizeEnterpriseSourceDisclosure(
	result *ConvertResult,
	files map[string]string,
	parameterFileNames []string,
) {
	if result == nil {
		return
	}
	if result.SourceReview == nil {
		result.SourceReview = BuildSourceReview(files, sourceReviewMappings(result))
	}
	disclosureSafeEnterpriseSourceReview(result.SourceReview)
	requestimport.RedactSourceReviewFilesByBaseName(
		result.SourceReview,
		enterpriseProtectedParameterDataFiles(parameterFileNames, files),
	)
}

func enterpriseProtectedParameterDataFiles(
	parameterFileNames []string,
	archiveFiles map[string]string,
) []string {
	if len(parameterFileNames) == 0 || len(archiveFiles) == 0 {
		return nil
	}
	sensitiveDataFiles := make(map[string]struct{})
	for _, parameterFileName := range parameterFileNames {
		content, ok := findFile(archiveFiles, parameterFileName)
		if !ok {
			continue
		}
		for sectionName, values := range ParseConfigSections(content) {
			lower := make(map[string]string, len(values))
			for key, value := range values {
				lower[strings.ToLower(strings.TrimSpace(key))] = cleanConfigValue(value)
			}
			parameterName := cleanParameterName(convertutil.FirstNonEmpty(
				lower["paramname"],
				lower["name"],
				sectionName,
			))
			columnName := cleanConfigValue(convertutil.FirstNonEmpty(
				lower["columnname"],
				lower["column"],
				lower["colname"],
			))
			if !credential.SensitiveName(parameterName) &&
				!credential.SensitiveName(columnName) {
				continue
			}
			dataFile := cleanConfigValue(convertutil.FirstNonEmpty(
				lower["table"],
				lower["filename"],
				lower["file"],
				lower["datafile"],
				lower["sourcefile"],
			))
			if dataFile == "" {
				continue
			}
			sensitiveDataFiles[strings.ToLower(runtimeArchiveBase(dataFile))] = struct{}{}
		}
	}
	if len(sensitiveDataFiles) == 0 {
		return nil
	}
	fileNames := make([]string, 0, len(sensitiveDataFiles))
	for fileName := range sensitiveDataFiles {
		fileNames = append(fileNames, fileName)
	}
	sort.Strings(fileNames)
	return fileNames
}

type enterpriseSourceSpan struct {
	start int
	end   int
}

// redactEnterpriseCallArgument removes a whole semantic call argument rather
// than only its first quoted literal. LoadRunner C permits adjacent string
// literals, comments, nested calls, and macros inside web_set_user's password
// expression. Keeping only line breaks preserves source-review line mappings
// while ensuring no suffix expression survives.
func redactEnterpriseCallArgument(content, function string, argumentIndex int) string {
	if content == "" || function == "" || argumentIndex < 0 {
		return content
	}
	spans := enterpriseCallArgumentSpans(content, function, argumentIndex)
	for index := len(spans) - 1; index >= 0; index-- {
		span := spans[index]
		content = content[:span.start] +
			enterpriseRedactedSourceExpression(content[span.start:span.end]) +
			content[span.end:]
	}
	return content
}

func enterpriseCallArgumentSpans(content, function string, argumentIndex int) []enterpriseSourceSpan {
	lower := strings.ToLower(content)
	needle := strings.ToLower(function)
	var spans []enterpriseSourceSpan
	for cursor := 0; cursor < len(content); {
		relative := strings.Index(lower[cursor:], needle)
		if relative < 0 {
			break
		}
		start := cursor + relative
		endName := start + len(needle)
		cursor = endName
		if (start > 0 && enterpriseIdentifierByte(content[start-1])) ||
			(endName < len(content) && enterpriseIdentifierByte(content[endName])) {
			continue
		}
		open := endName
		for open < len(content) && (content[open] == ' ' || content[open] == '\t' ||
			content[open] == '\r' || content[open] == '\n') {
			open++
		}
		if open >= len(content) || content[open] != '(' {
			continue
		}
		if span, ok := enterpriseCallArgumentSpan(content, open, argumentIndex); ok {
			spans = append(spans, span)
			cursor = span.end
		}
	}
	return spans
}

func enterpriseIdentifierByte(value byte) bool {
	return value == '_' ||
		value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z' ||
		value >= '0' && value <= '9'
}

func enterpriseCallArgumentSpan(content string, open int, target int) (enterpriseSourceSpan, bool) {
	if open < 0 || open >= len(content) || content[open] != '(' {
		return enterpriseSourceSpan{}, false
	}
	argument := 0
	argumentStart := open + 1
	parenDepth := 1
	bracketDepth := 0
	braceDepth := 0
	quote := byte(0)
	lineComment := false
	blockComment := false
	escaped := false

	for index := open + 1; index < len(content); index++ {
		current := content[index]
		next := byte(0)
		if index+1 < len(content) {
			next = content[index+1]
		}
		if lineComment {
			if current == '\n' {
				lineComment = false
			}
			continue
		}
		if blockComment {
			if current == '*' && next == '/' {
				blockComment = false
				index++
			}
			continue
		}
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if current == '\\' {
				escaped = true
				continue
			}
			if current == quote {
				quote = 0
			}
			continue
		}
		switch {
		case current == '/' && next == '/':
			lineComment = true
			index++
		case current == '/' && next == '*':
			blockComment = true
			index++
		case current == '"' || current == '\'':
			quote = current
		case current == '(':
			parenDepth++
		case current == ')':
			parenDepth--
			if parenDepth == 0 {
				if argument == target {
					return enterpriseSourceSpan{start: argumentStart, end: index}, true
				}
				return enterpriseSourceSpan{}, false
			}
		case current == '[':
			bracketDepth++
		case current == ']':
			if bracketDepth > 0 {
				bracketDepth--
			}
		case current == '{':
			braceDepth++
		case current == '}':
			if braceDepth > 0 {
				braceDepth--
			}
		case current == ',' && parenDepth == 1 && bracketDepth == 0 && braceDepth == 0:
			if argument == target {
				return enterpriseSourceSpan{start: argumentStart, end: index}, true
			}
			argument++
			argumentStart = index + 1
		}
	}
	return enterpriseSourceSpan{}, false
}

func enterpriseRedactedSourceExpression(source string) string {
	first := -1
	for index := 0; index < len(source); index++ {
		switch source[index] {
		case ' ', '\t', '\r', '\n':
		default:
			first = index
			index = len(source)
		}
	}
	if first < 0 {
		return source
	}
	var out strings.Builder
	out.Grow(len(source) + len(credential.Redacted))
	out.WriteString(source[:first])
	out.WriteByte('"')
	out.WriteString(credential.Redacted)
	out.WriteByte('"')
	for _, value := range source[first:] {
		switch value {
		case '\r', '\n':
			out.WriteRune(value)
		}
	}
	return out.String()
}

func disclosureSafeEnterpriseINIContent(content string) string {
	lines := strings.Split(content, "\n")
	section := ""
	for index, line := range lines {
		trimmed := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			section = strings.TrimSpace(trimmed[1 : len(trimmed)-1])
			continue
		}
		separator := strings.IndexByte(line, '=')
		if separator < 0 {
			continue
		}
		key := strings.TrimSpace(line[:separator])
		value := line[separator+1:]
		switch {
		case credential.SensitiveRuntimeSetting(section, key):
			lines[index] = line[:separator+1] + credential.Redacted
		case javaVMParameterRuntimeSetting(section, key):
			lines[index] = line[:separator+1] + disclosureSafeJavaVMParameterRow(value)
		case enterpriseRuntimeProxyEndpointKey(section, key):
			lines[index] = line[:separator+1] + disclosureSafeProxyEndpoint(value)
		default:
			lines[index] = line[:separator+1] + scrub.Value(value, scrub.Policy{})
		}
	}
	return strings.Join(lines, "\n")
}

func disclosureSafeEnterprisePRMContent(content string) string {
	sections := ParseConfigSections(content)
	sensitiveSections := make(map[string]bool, len(sections))
	for section, values := range sections {
		sensitiveSections[strings.ToLower(strings.TrimSpace(section))] =
			enterpriseParameterSectionSensitive(section, values)
	}
	lines := strings.Split(content, "\n")
	section := ""
	for index, line := range lines {
		trimmed := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			section = strings.TrimSpace(trimmed[1 : len(trimmed)-1])
			continue
		}
		separator := strings.IndexByte(line, '=')
		if separator < 0 {
			continue
		}
		key := strings.TrimSpace(line[:separator])
		value := line[separator+1:]
		switch {
		case credential.SensitiveName(key):
			lines[index] = line[:separator+1] + credential.Redacted
		case sensitiveSections[strings.ToLower(section)] &&
			enterpriseParameterLiteralValueKey(key):
			lines[index] = line[:separator+1] + credential.Redacted
		default:
			lines[index] = line[:separator+1] + scrub.Value(value, scrub.Policy{})
		}
	}
	return strings.Join(lines, "\n")
}

func pathExtension(path string) string {
	path = strings.ReplaceAll(path, `\`, "/")
	if separator := strings.LastIndexByte(path, '/'); separator >= 0 {
		path = path[separator+1:]
	}
	if dot := strings.LastIndexByte(path, '.'); dot >= 0 {
		return path[dot:]
	}
	return ""
}
