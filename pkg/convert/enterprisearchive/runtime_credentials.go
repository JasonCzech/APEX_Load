package enterprisearchive

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"github.com/jasonczech/apexload/pkg/javarunner"
	"github.com/jasonczech/apexload/pkg/protocol"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/httpcontract"
)

const (
	enterpriseProxyUsernameSecretName    = "ENTERPRISE_PROXY_USERNAME"
	enterpriseProxyPasswordSecretName    = "ENTERPRISE_PROXY_PASSWORD"
	enterpriseHTTPAuthPasswordSecretName = "ENTERPRISE_HTTP_AUTH_PASSWORD"
	enterpriseHTTPAuthTokenSecretName    = "ENTERPRISE_HTTP_AUTH_TOKEN"

	// encryptedRuntimeProxyPasswordPlaceholder stands in for a VuGen-encrypted
	// proxy password, which the importer cannot decrypt.
	encryptedRuntimeProxyPasswordPlaceholder = "{{secret." + enterpriseProxyPasswordSecretName + "}}"
)

type enterpriseRuntimeSourceCredential struct {
	value    string
	source   credentialpolicy.SourceRef
	nameHint string
}

// applyEnterpriseRuntimeBindings applies the shared credential safety review
// while retaining safely replayable imported values in the executable
// destination script. Disclosure-safe source review and API read projections
// remain responsible for masking those values outside persistence/replay.
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
	if ctx.RuntimeConfig != nil {
		ctx.RuntimeConfig.proxyPasswordSourceUnavailable = false
	}

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

	project := func(value, field, nameHint, requestKey string) string {
		if strings.TrimSpace(value) == "" {
			return ""
		}

		sourceCredential, hasSourceCredential := sourceValues[field]
		if sourceCredential.nameHint != "" {
			nameHint = sourceCredential.nameHint
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
			// ToHTTPConfig temporarily uses a secret reference so raw runtime
			// values do not leak through intermediate metadata. At the final
			// destination boundary restore the literal source value byte-for-byte;
			// Preserve mode must not create a binding/re-entry requirement.
			return sourceCredential.value
		}

		// The source already supplied this executable literal value (including
		// literal sentinel/encoded text when present in the archive). Preserve it
		// without manufacturing a binding or blocking execution.
		return value
	}

	for scriptIndex, script := range scripts {
		if script == nil {
			continue
		}
		markEnterpriseCredentialPreservingImport(script)
		candidateKey := fmt.Sprintf("enterprise:%d", scriptIndex)
		if hasGeneratedDatabaseDSNMarker(script) {
			requirement, replacement, err := credentialpolicy.NewRequirement(
				credentialpolicy.RequirementSpec{
					Key:      "database_dsn",
					Label:    "Database connection string for " + script.Name,
					Context:  "The enterprise database importer generated this connection-string placeholder because the source contained SQL actions but no complete executable database connection.",
					NameHint: "DATABASE_DSN",
					Source: credentialpolicy.SourceRef{
						Format:  "enterprise_script_archive",
						Path:    ctx.ProjectFileName,
						Pointer: "/database/connection",
						ItemID:  candidateKey,
					},
					Targets: []credentialpolicy.TargetRef{{
						RequestKey: candidateKey,
						Field:      "config.database.dsn",
					}},
					Mode:     credentialpolicy.SecretReference,
					Registry: registry,
				},
			)
			if err == nil {
				script.Config.Database.DSN = replacement
				appendRequirement(requirement)
			} else {
				result.Warnings = append(result.Warnings, "Database connection guidance could not be generated; configure config.database.dsn before replay.")
			}
		}
		httpConfig := &script.Config.HTTP
		if strings.EqualFold(strings.TrimSpace(httpConfig.ProxyMode), "none") {
			httpConfig.Proxy = dsl.ProxyConfig{}
		}
		authoredProxyMode := httpConfig.ProxyMode
		authoredProxy := httpConfig.Proxy
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
			projectedURL := ""
			var projection credentialpolicy.Result
			var err error
			unrepresentable := !utf8.ValidString(httpConfig.Proxy.URL)
			runtimeUnsupported := !unrepresentable &&
				!validRuntimeCustomProxyURL(httpConfig.Proxy.URL)
			if unrepresentable {
				err = fmt.Errorf("proxy URL is not representable in the durable DSL")
			} else {
				projectedURL, projection, err = credentialpolicy.ProjectRawURL(
					httpConfig.Proxy.URL,
					credentialpolicy.Options{
						Mode:        credentialpolicy.Preserve,
						Source:      proxySource,
						Registry:    registry,
						TargetField: "config.http.proxy.url",
					},
				)
			}
			requiresReview := runtimeUnsupported ||
				err != nil ||
				projection.Disposition == credentialpolicy.BlockedDraft ||
				projection.Disposition == credentialpolicy.Rejected
			structurallyUnrepresentable := unrepresentable ||
				runtimeUnsupported ||
				err != nil ||
				projection.Disposition == credentialpolicy.Rejected
			if !structurallyUnrepresentable &&
				projection.Disposition != credentialpolicy.Rejected {
				httpConfig.Proxy.URL = projectedURL
				mergeEnterpriseScriptCredentialTargets(script, projection.PreservedTargets)
				for _, requirement := range projection.Requirements {
					if requirement.ExistingReference {
						if _, covered := coveredReplacements[strings.TrimSpace(requirement.Replacement)]; covered {
							continue
						}
					}
					appendRequirement(requirement)
				}
			}
			if requiresReview {
				detail := "the source proxy URL requires bounded review and was retained in a blocked draft"
				warning := "HTTP proxy URL requires review; the imported value was retained and the generated draft cannot execute until the gate is resolved."
				if structurallyUnrepresentable {
					preserveEnterpriseUnsupportedProxySource(
						script,
						authoredProxyMode,
						authoredProxy,
					)
					httpConfig.ProxyMode = "none"
					httpConfig.Proxy = dsl.ProxyConfig{}
					detail = "the source proxy cannot be represented by the DSL's executable proxy field; proxying was disabled and its exact source record was retained in protected metadata"
					warning = "HTTP proxy URL could not be represented by the executable DSL; proxying was disabled, its exact source record was retained for repair, and the generated draft requires review before execution."
				}
				finding := importreview.Finding{
					Code:       "import_review_required",
					Severity:   diag.Blocker,
					Element:    "HTTP proxy URL",
					Detail:     detail,
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
					warning,
				)
			}
		}
		httpConfig.Proxy.Username = project(
			httpConfig.Proxy.Username,
			"config.http.proxy.username",
			enterpriseProxyUsernameSecretName,
			candidateKey,
		)
		httpConfig.Proxy.Password = project(
			httpConfig.Proxy.Password,
			"config.http.proxy.password",
			enterpriseProxyPasswordSecretName,
			candidateKey,
		)
		httpConfig.Auth.Password = project(
			httpConfig.Auth.Password,
			"config.http.auth.password",
			enterpriseHTTPAuthPasswordSecretName,
			candidateKey,
		)
		httpConfig.Auth.Token = project(
			httpConfig.Auth.Token,
			"config.http.auth.token",
			enterpriseHTTPAuthTokenSecretName,
			candidateKey,
		)
		if enterpriseGlobalHTTPAuthInvalid(httpConfig.Auth) {
			location := "config.http.auth"
			if runtimeFile := normalizeRuntimeArchivePath(ctx.RuntimeFileName); runtimeFile != "" {
				location = runtimeFile + "#Authentication"
			}
			finding := importreview.Finding{
				Code:       "import_review_required",
				Severity:   diag.Blocker,
				Element:    "HTTP authentication",
				Detail:     "the source authentication value cannot be represented safely by the native HTTP runtime and was retained in a blocked draft",
				Location:   location,
				Source:     "enterprise_script_archive",
				ReasonCode: "invalid_auth_wire_value",
				SourceRef:  location,
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
				"HTTP authentication could not be represented safely; the imported value was retained and the generated draft requires review before execution.",
			)
		}

		headerNames := make([]string, 0, len(httpConfig.DefaultHeaders))
		for name := range httpConfig.DefaultHeaders {
			if credential.ImportCredentialHeader(name) {
				headerNames = append(headerNames, name)
			}
		}
		sort.SliceStable(headerNames, func(i, j int) bool {
			return strings.ToLower(headerNames[i]) < strings.ToLower(headerNames[j])
		})
		for _, name := range headerNames {
			field := "config.http.default_headers." + strings.ToLower(strings.TrimSpace(name))
			httpConfig.DefaultHeaders[name] = project(
				httpConfig.DefaultHeaders[name],
				field,
				"ENTERPRISE_DEFAULT_HEADER_"+credential.EnvKey(name),
				candidateKey,
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
		for _, name := range attributeNames {
			field := "config.web_runtime.additional_attributes." + strings.ToLower(strings.TrimSpace(name))
			script.Config.WebRuntime.AdditionalAttributes[name] = project(
				script.Config.WebRuntime.AdditionalAttributes[name],
				field,
				"ENTERPRISE_RUNTIME_"+credential.EnvKey(name),
				candidateKey,
			)
		}
		mergeEnterpriseScriptCredentialTargets(
			script,
			enterpriseOpaqueConfigCredentialTargets(script, candidateKey),
		)

		additionalURLs := make([]requestimport.ScriptCredentialURL, len(result.RecordedResourceURLs))
		parameterNames := make(map[string]struct{}, len(script.Parameters))
		for _, parameter := range script.Parameters {
			parameterNames[canonicalSourceParameterName(parameter.Name)] = struct{}{}
		}
		for index, value := range result.RecordedResourceURLs {
			additionalURLs[index] = requestimport.ScriptCredentialURL{
				Value:       rewriteKnownParameterReferences(convertParamReferences(value), parameterNames),
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
			if len(projected.AdditionalURLs) != len(result.RecordedResourceURLs) {
				// Only the bounded-traversal fallback replaces the request tree.
				// The allowlist it described no longer refers to anything
				// executable, so it is dropped; every other review failure keeps
				// the imported script and must keep its recorded resource URLs.
				result.RecordedResourceURLs = nil
			}
			result.Warnings = append(
				result.Warnings,
				"HTTP credential review could not complete for the generated request tree; the imported requests were retained in a blocked draft for operator repair.",
			)
		}
	}

	result.ProtectedRequirements.Store(requirements)
	clearEnterpriseRuntimeSourceValues(ctx.RuntimeConfig)
	for index := range requirements {
		requirements[index].Value = ""
	}
}

func enterpriseUnavailableRuntimeCredentialPresentation(field string) (string, string) {
	label := "Enterprise runtime credential"
	switch field {
	case "config.http.proxy.username":
		label = "HTTP proxy username"
	case "config.http.proxy.password":
		label = "HTTP proxy password"
	case "config.http.auth.password":
		label = "HTTP authentication password"
	case "config.http.auth.token":
		label = "HTTP authentication token"
	default:
		switch {
		case strings.HasPrefix(field, "config.http.default_headers."):
			label = "Credential-bearing default HTTP header"
		case strings.HasPrefix(field, "config.web_runtime.additional_attributes."):
			label = "Credential-bearing runtime attribute"
		}
	}
	return label,
		"The enterprise source contains a disclosure placeholder instead of the original " + strings.ToLower(label) + ". The imported value cannot be preserved; configure the generated project secret before replay."
}

func markEnterpriseCredentialPreservingImport(script *dsl.Script) {
	if script == nil {
		return
	}
	if script.Metadata == nil {
		script.Metadata = &dsl.ScriptMetadata{}
	}
	if script.Metadata.Import == nil {
		script.Metadata.Import = &dsl.ImportMetadata{}
	}
	if strings.TrimSpace(script.Metadata.Import.Source) == "" {
		script.Metadata.Import.Source = "enterprise_script_archive"
	}
	if script.Metadata.Import.Preserved == nil {
		script.Metadata.Import.Preserved = make(map[string]any)
	}
	script.Metadata.Import.Preserved[dsl.ImportPreservedCredentialPolicyKey] =
		dsl.ImportPreservedCredentialPolicyPreserve
}

func preserveEnterpriseUnsupportedProxySource(
	script *dsl.Script,
	proxyMode string,
	proxy dsl.ProxyConfig,
) {
	if script == nil {
		return
	}
	markEnterpriseCredentialPreservingImport(script)
	encode := func(value string) string {
		return base64.StdEncoding.EncodeToString([]byte(value))
	}
	noProxy := make([]string, len(proxy.NoProxy))
	for index, value := range proxy.NoProxy {
		noProxy[index] = encode(value)
	}
	script.Metadata.Import.Preserved[dsl.ImportPreservedEnterpriseUnsupportedProxySourceKey] =
		map[string]any{
			"version":                1,
			"proxy_mode_base64":      encode(proxyMode),
			"url_base64":             encode(proxy.URL),
			"username_base64":        encode(proxy.Username),
			"password_base64":        encode(proxy.Password),
			"no_proxy_values_base64": noProxy,
		}
}

func mergeEnterpriseScriptCredentialTargets(
	script *dsl.Script,
	targets []credentialpolicy.TargetRef,
) {
	if script == nil || len(targets) == 0 {
		return
	}
	markEnterpriseCredentialPreservingImport(script)
	preserved := script.Metadata.Import.Preserved
	metadata := make(map[string]any, 1)
	if existing, ok := preserved[dsl.ImportPreservedCredentialTargetsMetadataKey]; ok {
		metadata[dsl.ImportPreservedCredentialTargetsMetadataKey] = existing
	}
	metadata = requestimport.MergePreservedCredentialTargetsMetadata(metadata, targets)
	if merged, ok := metadata[dsl.ImportPreservedCredentialTargetsMetadataKey]; ok {
		preserved[dsl.ImportPreservedCredentialTargetsMetadataKey] = merged
	}
}

// enterpriseOpaqueConfigCredentialTargets records executable config scalars
// whose credential components cannot be represented by ordinary field-name
// scanning alone. The locators contain no values; they let runtime redaction
// derive both the complete composite and isolated components immediately
// before execution.
func enterpriseOpaqueConfigCredentialTargets(
	script *dsl.Script,
	requestKey string,
) []credentialpolicy.TargetRef {
	if script == nil {
		return nil
	}
	var targets []credentialpolicy.TargetRef
	dsn := strings.TrimSpace(script.Config.Database.DSN)
	if dsn != "" {
		if _, governed := credential.ParseReference(dsn); !governed {
			targets = append(targets, credentialpolicy.TargetRef{
				RequestKey: requestKey,
				Field:      "config.database.dsn.opaque",
			})
		}
	}
	for index, argument := range script.Config.JavaVuser.AdditionalVMParameters {
		if !javarunner.ContainsSensitiveVMParameterValue(argument) {
			continue
		}
		targets = append(targets, credentialpolicy.TargetRef{
			RequestKey: requestKey,
			Field:      "config.java_vuser.additional_vm_parameters.opaque",
			Index:      index,
		})
	}
	return targets
}

func enterpriseGlobalHTTPAuthInvalid(auth dsl.AuthConfig) bool {
	mode := httpprotocol.NormalizeAuthType(auth.Type)
	switch mode {
	case "", httpprotocol.AuthTypeNone, httpprotocol.AuthTypeInherit:
		return false
	}
	if httpprotocol.LookupAuthCapability(mode).Support != httpprotocol.AuthExecutable {
		return true
	}
	return httpprotocol.ValidateAuthWireValues(&protocol.AuthConfig{
		Type:     mode,
		Username: auth.Username,
		Password: auth.Password,
		Token:    auth.Token,
		Domain:   auth.Domain,
	}) != nil
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
	if proxyUser == "" {
		proxyUser = ctx.RuntimeConfig.proxyReviewUser
	}
	proxyUserSource := source("Proxy/ProxyUserName")
	if ctx.RuntimeConfig.proxyUserFromURL {
		proxyUserSource = source("Proxy/ProxyServer")
	}
	if strings.TrimSpace(proxyUser) == "" && embeddedProxyUser != "" {
		proxyUser = embeddedProxyUser
		proxyUserSource = source("Proxy/ProxyServer")
	}
	proxyPass := ctx.RuntimeConfig.ProxyPass
	if proxyPass == "" {
		proxyPass = ctx.RuntimeConfig.proxyReviewPass
	}
	proxyPassSource := source("Proxy/ProxyPassword")
	if ctx.RuntimeConfig.proxyPassFromURL {
		proxyPassSource = source("Proxy/ProxyServer")
	}
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
		},
		"config.http.proxy.password": {
			value:    proxyPass,
			source:   proxyPassSource,
			nameHint: enterpriseProxyPasswordSecretName,
		},
		"config.http.auth.password": {
			value:    ctx.RuntimeConfig.AuthPass,
			source:   source("Authentication/Password"),
			nameHint: enterpriseHTTPAuthPasswordSecretName,
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
	config.proxyReviewUser = ""
	config.proxyReviewPass = ""
	config.AuthPass = ""
	// The web-runtime additional attributes and the Java VM argv/rows are
	// executable configuration, not project secrets: they were projected into
	// the destination script with Preserve, and the preserved java_vm_settings
	// block is the dashboard's VM-parameter picker source. Overwriting them with
	// the redaction sentinel here removed a value the operator already owned and
	// handed the picker a command line that cannot run. Only the credential
	// fields above, which were migrated into protected requirements, are cleared.
	// Run-output redaction masks whatever remains sensitive at replay time.
	config.javaVMParameterSourceRows = nil
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
		out[convertutil.NeutralizeScriptMetadataString(file)] = disclosureSafeConfigSections(sections)
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
			safeValues[convertutil.NeutralizeScriptMetadataString(key)] = convertutil.NeutralizeScriptMetadataString(value)
		}
		out[convertutil.NeutralizeScriptMetadataString(section)] = safeValues
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
			safeValues := make(map[string]string, len(values))
			for key, value := range values {
				safeValues[convertutil.NeutralizeScriptMetadataString(key)] = convertutil.NeutralizeScriptMetadataString(value)
			}
			safeSections[convertutil.NeutralizeScriptMetadataString(section)] = safeValues
		}
		out[convertutil.NeutralizeScriptMetadataString(file)] = safeSections
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
// endpoint spelling understood by parseRuntimeProxySettings while preserving
// the host, port, path, and query verbatim.
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
		return value
	}
	parsed.User = nil
	safe := parsed.String()
	if addedScheme {
		safe = strings.TrimPrefix(safe, "http://")
	}
	return strings.Replace(value, trimmed, safe, 1)
}

func disclosureSafeEnterpriseSourceReview(_ *SourceReview) {
}

func finalizeEnterpriseSourceDisclosure(
	result *ConvertResult,
	files map[string]string,
	_ []string,
) {
	if result == nil {
		return
	}
	if result.SourceReview == nil {
		result.SourceReview = BuildSourceReview(files, sourceReviewMappings(result))
	}
}
