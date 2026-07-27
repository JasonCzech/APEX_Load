package requestimport

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"github.com/jasonczech/apexload/pkg/scrub"
)

// ScriptCredentialURL is one importer-owned URL surface outside the canonical
// DSL request tree, such as a recorded-resource allowlist entry.
type ScriptCredentialURL struct {
	Value       string
	TargetField string
	Element     string
	Source      SourceRef
}

// ScriptCredentialProjectionOptions configures source-neutral projection for
// an already assembled HTTP-family script.
type ScriptCredentialProjectionOptions struct {
	SourceProfile          string
	CandidateKey           string
	NamePrefix             string
	Registry               *credentialpolicy.NameRegistry
	AdditionalURLs         []ScriptCredentialURL
	RuntimeParameterPolicy RuntimeParameterPolicy
}

// RuntimeParameterPolicy controls whether credential-shaped runtime parameter
// references are externalized as project secrets or retained as governed
// script data. The zero value preserves the conservative cross-import default.
type RuntimeParameterPolicy string

const (
	RuntimeParametersExternalizeStatic RuntimeParameterPolicy = ""
	RuntimeParametersPreserveGoverned  RuntimeParameterPolicy = "preserve_governed"
)

// ScriptCredentialProjectionResult carries only disclosure-safe public
// findings and projected URL values. Requirement.Value is protected material
// and must be transferred into the owning importer's protected handoff.
type ScriptCredentialProjectionResult struct {
	Requirements   []credentialpolicy.Requirement
	Findings       []importreview.Finding
	AdditionalURLs []string
	FallbackDraft  bool
}

func literalScriptSourceVariables(
	script *dsl.Script,
	sourceProfile string,
) map[string]credentialpolicy.SourceVariable {
	if script == nil {
		return nil
	}
	runtimeNameCounts := make(map[string]int, len(script.Parameters))
	for _, parameter := range script.Parameters {
		if name := strings.TrimSpace(parameter.Name); name != "" {
			runtimeNameCounts[strings.ToLower(name)]++
		}
	}
	variables := make(map[string]credentialpolicy.SourceVariable)
	for index, parameter := range script.Parameters {
		name := strings.TrimSpace(parameter.Name)
		if name == "" || runtimeNameCounts[strings.ToLower(name)] != 1 {
			// Parameter resolution is case-insensitive. Do not select one
			// source value from an ambiguous set of case-colliding definitions.
			continue
		}
		// A single inline list value is deterministic source material. Random,
		// generated, and multi-value parameters are not safe to materialize as
		// one credential value. A data-source-backed parameter is equally
		// deterministic when every reachable row contains the same value.
		if strings.EqualFold(strings.TrimSpace(parameter.Type), "list") &&
			len(parameter.Values) == 1 {
			variables[name] = credentialpolicy.SourceVariable{
				Value: parameter.Values[0],
				Source: credentialpolicy.SourceRef{
					Format:  sourceProfile,
					Pointer: fmt.Sprintf("/parameters/%d/values/0", index),
				},
			}
			continue
		}
		if sourceVariable, ok := constantDataSourceParameterVariable(
			script,
			parameter,
			sourceProfile,
		); ok {
			variables[name] = sourceVariable
		}
	}
	if len(variables) == 0 {
		return nil
	}
	return variables
}

func removeConsumedLiteralScriptParameters(
	script *dsl.Script,
	requirements []credentialpolicy.Requirement,
) {
	if script == nil || len(script.Parameters) == 0 || len(requirements) == 0 {
		return
	}
	consumed := make(map[string]struct{})
	for _, requirement := range requirements {
		references := make(map[string]struct{})
		collectVariableReferences(requirement.Value, references)
		collectParameterReferences(requirement.Value, references)
		for name := range references {
			if name = strings.TrimSpace(name); name != "" {
				consumed[strings.ToLower(name)] = struct{}{}
			}
		}
		for _, name := range requirement.SourceVariableNames {
			name = strings.TrimSpace(name)
			if name != "" {
				consumed[strings.ToLower(name)] = struct{}{}
			}
		}
	}
	if len(consumed) == 0 {
		return
	}
	protectedBindings := make([]scriptCredentialDataBinding, 0, len(consumed))
	for _, parameter := range script.Parameters {
		if _, remove := consumed[strings.ToLower(strings.TrimSpace(parameter.Name))]; !remove {
			continue
		}
		if source := strings.TrimSpace(parameter.DataSource); source != "" {
			protectedBindings = append(protectedBindings, scriptCredentialDataBinding{
				source: source,
				column: strings.TrimSpace(parameter.Column),
			})
		}
	}
	kept := script.Parameters[:0]
	for index := range script.Parameters {
		parameter := script.Parameters[index]
		_, removeByName := consumed[strings.ToLower(strings.TrimSpace(parameter.Name))]
		if removeByName || parameterUsesCredentialDataBinding(parameter, protectedBindings) {
			for valueIndex := range parameter.Values {
				parameter.Values[valueIndex] = ""
			}
			continue
		}
		kept = append(kept, parameter)
	}
	script.Parameters = kept
	removeCredentialDataSourceColumns(script, protectedBindings)
}

// ProjectScriptCredentials applies the shared URL, ordered-header, auth, and
// structured-body policy to every request-shaped surface in an already
// assembled HTTP-family DSL script. It preserves action ordering and
// source-specific overlays while adding durable gates for unsafe projections.
func ProjectScriptCredentials(
	script *dsl.Script,
	options ScriptCredentialProjectionOptions,
) (ScriptCredentialProjectionResult, error) {
	var result ScriptCredentialProjectionResult
	if script == nil || !isHTTPFamilyProtocol(script.Protocol) {
		return result, nil
	}
	options.SourceProfile = firstNonEmpty(strings.TrimSpace(options.SourceProfile), strings.TrimSpace(script.GeneratedFrom), "import")
	options.CandidateKey = firstNonEmpty(strings.TrimSpace(options.CandidateKey), options.SourceProfile+":script")
	options.NamePrefix = firstNonEmpty(strings.TrimSpace(options.NamePrefix), "IMPORT_REQUEST")
	if options.Registry == nil {
		options.Registry = credentialpolicy.NewNameRegistry()
	}
	reserveScriptSecretReferences(script, options.Registry)
	// Script parameters may be static source values. They satisfy execution
	// readiness, but they are not proof that a credential-shaped {{var.*}}
	// reference was produced by a prior response. Remove them from the
	// provenance analysis so only execution-path-guaranteed extractor outputs
	// (and other runtime outputs recognized by the shared analyzer) are trusted.
	provenanceScript := *script
	provenanceScript.Parameters = nil
	variableReadiness := AnalyzeVariableReadiness(&provenanceScript, nil)
	sourceVariables := literalScriptSourceVariables(script, options.SourceProfile)
	var trustedWholeValues map[string]struct{}
	runtimeParameterSourcesRedacted := false
	if options.RuntimeParameterPolicy == RuntimeParametersPreserveGoverned {
		// LoadRunner parameter files are executable runtime data rather than
		// declarations of project secrets. Preserve exact whole parameter
		// references even in credential-shaped fields, while removing source
		// credential rows from the public draft and attaching a durable gate.
		sourceVariables = nil
		trustedWholeValues = trustedScriptParameterValues(script)
		runtimeParameterSourcesRedacted = redactSensitiveRuntimeParameterValues(script)
	}
	// A {{var.*}} reference inside config.base_url is never proven, however well
	// an extractor's execution path is guaranteed:
	// buildProtoConfigWithSecretResolver flattens base_url once at protocol init
	// and resolves only {{secret.*}} there, and HTTP.ResolveURL then concatenates
	// the stored string verbatim per request. A preserved {{var.*}} would go on
	// the wire literally, so base URL credentials always govern. Pinned by
	// TestProjectScriptCredentialsGovernsBaseURLRuntimeVariables.
	var baseProvenVars map[string]struct{}
	requirementIndexes := make(map[string]int)
	requirementConflict := false
	appendRequirements := func(input []credentialpolicy.Requirement) {
		for _, requirement := range credentialpolicy.CloneRequirements(input) {
			if strings.TrimSpace(requirement.ID) == "" {
				requirementConflict = true
				continue
			}
			if existingIndex, exists := requirementIndexes[requirement.ID]; exists {
				existing := result.Requirements[existingIndex]
				if !compatibleCredentialRequirements(existing, requirement) {
					requirementConflict = true
					continue
				}
				existing.SourceVariableNames = mergeSourceVariableNames(
					existing.SourceVariableNames,
					requirement.SourceVariableNames,
				)
				result.Requirements[existingIndex] = existing
				continue
			}
			requirementIndexes[requirement.ID] = len(result.Requirements)
			result.Requirements = append(result.Requirements, requirement)
		}
	}

	var findings []importreview.Finding
	if runtimeParameterSourcesRedacted {
		_ = dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
			if !runtimeSurfaceIsHTTPRequest(ref) {
				return nil
			}
			findings = append(findings, importreview.Finding{
				Code:       "credential_parameter_source_redacted",
				Severity:   diag.Blocker,
				Element:    "runtime parameter credential source",
				Detail:     "a credential-named runtime parameter was preserved, but its imported source rows were redacted at the disclosure boundary",
				ActionPath: ref.Path,
				Source:     options.SourceProfile,
				ReasonCode: "credential_parameter_source_redacted",
				Resolution: "replace the redacted parameter rows with an approved executable data source and remove the gate",
				Gate:       importreview.GateExecution,
			})
			return nil
		})
	}
	projectBaseURLCredentials(
		script,
		options,
		baseProvenVars,
		sourceVariables,
		trustedWholeValues,
		appendRequirements,
		&findings,
	)
	result.AdditionalURLs = projectAdditionalURLCredentials(
		script,
		options,
		sourceVariables,
		trustedWholeValues,
		appendRequirements,
		&findings,
	)

	walkErr := dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		if ref.Action != nil &&
			dsl.CanonicalActionType(ref.Action.Type) == "http_oauth2_token" {
			projectOAuthTokenURLCredentials(
				ref,
				options,
				provenVariableReferencesForSurface(ref, variableReadiness.ByAction[ref.Path]),
				sourceVariables,
				trustedWholeValues,
				appendRequirements,
				&findings,
			)
			sanitizeEnterpriseURLMetadata(ref)
			return nil
		}
		if !runtimeSurfaceIsHTTPRequest(ref) {
			sanitizeEnterpriseURLMetadata(ref)
			return nil
		}
		projectEnterpriseRequestSurface(
			ref,
			options,
			provenVariableReferencesForSurface(ref, variableReadiness.ByAction[ref.Path]),
			sourceVariables,
			trustedWholeValues,
			appendRequirements,
			&findings,
		)
		return nil
	})
	if walkErr != nil {
		// No unvisited source-controlled request values may be returned after a
		// bounded-walker failure. Replace the script with a valid review draft;
		// source evidence remains available through the redacted SourceReview.
		replaceScriptWithCredentialTraversalReviewDraft(script, options.SourceProfile)
		result.AdditionalURLs = nil
		result.FallbackDraft = true
		return result, fmt.Errorf("HTTP credential review exceeded the bounded action traversal limit")
	}
	if requirementConflict {
		replaceScriptWithCredentialTraversalReviewDraft(script, options.SourceProfile)
		result.AdditionalURLs = nil
		result.FallbackDraft = true
		return result, fmt.Errorf("HTTP credential review found conflicting requirement provenance")
	}
	removeConsumedLiteralScriptParameters(script, result.Requirements)
	redactCredentialSourceVariables(script, result.Requirements)

	synchronizeEnterpriseSequenceProvenance(script)
	if err := importreview.ApplyToScript(script, findings); err != nil {
		replaceScriptWithCredentialTraversalReviewDraft(script, options.SourceProfile)
		result.AdditionalURLs = nil
		result.FallbackDraft = true
		return result, fmt.Errorf("attach durable HTTP credential review: %w", err)
	}
	result.Findings = importreview.NormalizeFindings(findings)
	return result, nil
}

func compatibleCredentialRequirements(left, right credentialpolicy.Requirement) bool {
	if left.ID != right.ID ||
		left.Key != right.Key ||
		left.Label != right.Label ||
		left.Context != right.Context ||
		left.SuggestedName != right.SuggestedName ||
		left.Replacement != right.Replacement ||
		left.Source != right.Source ||
		left.SourceValueAvailable != right.SourceValueAvailable ||
		left.ExistingReference != right.ExistingReference ||
		left.Value != right.Value ||
		left.DeferredSourceExpression != right.DeferredSourceExpression ||
		len(left.Targets) != len(right.Targets) {
		return false
	}
	for index := range left.Targets {
		if left.Targets[index] != right.Targets[index] {
			return false
		}
	}
	return true
}

func mergeSourceVariableNames(left, right []string) []string {
	if len(left) == 0 && len(right) == 0 {
		return nil
	}
	merged := make(map[string]struct{}, len(left)+len(right))
	for _, names := range [][]string{left, right} {
		for _, name := range names {
			name = strings.TrimSpace(name)
			if name != "" {
				merged[name] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(merged))
	for name := range merged {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func redactCredentialSourceVariables(
	script *dsl.Script,
	requirements []credentialpolicy.Requirement,
) {
	if script == nil || len(requirements) == 0 {
		return
	}
	names := make(map[string]struct{})
	for _, requirement := range requirements {
		// Unproven {{var.*}} syntax is governed as a complete credential value
		// even when an adapter did not provide a SourceVariables materialization
		// map. The original parameter value must still leave the public draft.
		collectVariableReferences(requirement.Value, names)
		collectParameterReferences(requirement.Value, names)
		for _, name := range requirement.SourceVariableNames {
			name = strings.TrimSpace(name)
			if name != "" {
				names[name] = struct{}{}
			}
		}
	}
	if len(names) == 0 {
		return
	}
	dataSources := make(map[string]struct{})
	for index := range script.Parameters {
		parameter := &script.Parameters[index]
		if !stringSetContainsFold(names, parameter.Name) {
			continue
		}
		if len(parameter.Values) > 0 {
			parameter.Values = []string{credential.Redacted}
		}
		if source := strings.TrimSpace(parameter.DataSource); source != "" {
			dataSources[source] = struct{}{}
		}
	}
	for index := range script.DataSources {
		source := &script.DataSources[index]
		if _, protected := dataSources[strings.TrimSpace(source.Name)]; !protected {
			continue
		}
		if source.InlineData != "" {
			source.InlineData = credential.Redacted
			source.Encoding = ""
		}
	}
}

func projectBaseURLCredentials(
	script *dsl.Script,
	options ScriptCredentialProjectionOptions,
	provenVars map[string]struct{},
	sourceVariables map[string]credentialpolicy.SourceVariable,
	trustedWholeValues map[string]struct{},
	appendRequirements func([]credentialpolicy.Requirement),
	findings *[]importreview.Finding,
) {
	if strings.TrimSpace(script.Config.BaseURL) == "" {
		return
	}
	source := credentialpolicy.SourceRef{
		Format:  options.SourceProfile,
		Pointer: "/config/base_url",
	}
	projected, projection, err := credentialpolicy.ProjectRawURL(
		script.Config.BaseURL,
		credentialpolicy.Options{
			Mode:               credentialpolicy.SecretReference,
			Source:             source,
			RequestKey:         options.CandidateKey + ":base_url",
			NamePrefix:         options.NamePrefix + "_BASE_URL",
			Registry:           options.Registry,
			ProvenVars:         provenVars,
			SourceVariables:    sourceVariables,
			TrustedWholeValues: trustedWholeValues,
			TargetField:        "config.base_url",
		},
	)
	if err != nil || projection.Disposition == credentialpolicy.Rejected {
		script.Config.BaseURL = "https://set-base-url.invalid"
		if err != nil {
			projection = rejectedScriptCredentialProjection(source, "credential_base_url_projection_failed")
		}
	} else {
		script.Config.BaseURL = projected
	}
	appendRequirements(projection.Requirements)
	appendEnterpriseProjectionFindingsForAllRequests(
		script,
		ReviewCredentialProjection(projection, "HTTP base URL").Findings,
		findings,
	)
}

func projectAdditionalURLCredentials(
	script *dsl.Script,
	options ScriptCredentialProjectionOptions,
	sourceVariables map[string]credentialpolicy.SourceVariable,
	trustedWholeValues map[string]struct{},
	appendRequirements func([]credentialpolicy.Requirement),
	findings *[]importreview.Finding,
) []string {
	if len(options.AdditionalURLs) == 0 {
		return nil
	}
	projectedURLs := make([]string, len(options.AdditionalURLs))
	for index, item := range options.AdditionalURLs {
		source := item.Source
		if strings.TrimSpace(source.Format) == "" {
			source.Format = options.SourceProfile
		}
		if strings.TrimSpace(source.Pointer) == "" {
			source.Pointer = firstNonEmpty(strings.TrimSpace(item.TargetField), fmt.Sprintf("/additional_urls/%d", index))
		}
		projected, projection, err := credentialpolicy.ProjectRawURL(
			item.Value,
			credentialpolicy.Options{
				Mode:               credentialpolicy.SecretReference,
				Source:             source,
				RequestKey:         fmt.Sprintf("%s:additional_url:%d", options.CandidateKey, index),
				NamePrefix:         options.NamePrefix + "_ADDITIONAL_URL",
				Registry:           options.Registry,
				SourceVariables:    sourceVariables,
				TrustedWholeValues: trustedWholeValues,
				TargetField:        firstNonEmpty(strings.TrimSpace(item.TargetField), fmt.Sprintf("additional_urls[%d]", index)),
			},
		)
		if err != nil || projection.Disposition == credentialpolicy.Rejected {
			projectedURLs[index] = credential.Redacted
			if err != nil {
				projection = rejectedScriptCredentialProjection(source, "credential_additional_url_projection_failed")
			}
		} else {
			projectedURLs[index] = projected
		}
		appendRequirements(projection.Requirements)
		element := firstNonEmpty(strings.TrimSpace(item.Element), "additional URL")
		appendEnterpriseProjectionFindingsForAllRequests(
			script,
			ReviewCredentialProjection(projection, element).Findings,
			findings,
		)
	}
	return projectedURLs
}

func projectOAuthTokenURLCredentials(
	ref dslwalk.SurfaceRef,
	scriptOptions ScriptCredentialProjectionOptions,
	provenVars map[string]struct{},
	sourceVariables map[string]credentialpolicy.SourceVariable,
	trustedWholeValues map[string]struct{},
	appendRequirements func([]credentialpolicy.Requirement),
	findings *[]importreview.Finding,
) {
	source := enterpriseSurfaceCredentialSource(ref, scriptOptions.SourceProfile)
	aggregate := credentialpolicy.Result{Disposition: credentialpolicy.ExecutableExact}
	for _, surface := range importURLCredentialSurfaces(ref) {
		surfaceSource := source
		surfaceSource.Pointer = ref.Path + surface.pointerSuffix
		projected, projection, err := credentialpolicy.ProjectRawURL(
			surface.raw,
			credentialpolicy.Options{
				Mode:               credentialpolicy.SecretReference,
				Source:             surfaceSource,
				RequestKey:         scriptOptions.CandidateKey + ":" + ref.Path + surface.requestKeySuffix,
				NamePrefix:         firstNonEmpty(scriptOptions.NamePrefix, surface.namePrefix),
				Registry:           scriptOptions.Registry,
				ProvenVars:         provenVars,
				SourceVariables:    sourceVariables,
				TrustedWholeValues: trustedWholeValues,
				TargetField:        surface.targetField,
			},
		)
		aggregate.Merge(projection)
		if err != nil || projection.Disposition == credentialpolicy.Rejected {
			surface.set("https://set-token-url.invalid")
			if err != nil {
				aggregate.Merge(rejectedScriptCredentialProjection(
					surfaceSource,
					"credential_oauth_token_url_projection_failed",
				))
			}
			continue
		}
		surface.set(projected)
	}
	appendRequirements(aggregate.Requirements)
	review := ReviewCredentialProjection(aggregate, "OAuth token URL")
	for _, finding := range review.Findings {
		finding.ActionPath = ref.Path
		*findings = append(*findings, finding)
	}
}

func projectEnterpriseRequestSurface(
	ref dslwalk.SurfaceRef,
	scriptOptions ScriptCredentialProjectionOptions,
	provenVars map[string]struct{},
	sourceVariables map[string]credentialpolicy.SourceVariable,
	trustedWholeValues map[string]struct{},
	appendRequirements func([]credentialpolicy.Requirement),
	findings *[]importreview.Finding,
) {
	source := enterpriseSurfaceCredentialSource(ref, scriptOptions.SourceProfile)
	requestKey := scriptOptions.CandidateKey + ":" + ref.Path
	options := credentialpolicy.Options{
		Mode:               credentialpolicy.SecretReference,
		Source:             source,
		RequestKey:         requestKey,
		NamePrefix:         scriptOptions.NamePrefix,
		Registry:           scriptOptions.Registry,
		ProvenVars:         provenVars,
		SourceVariables:    sourceVariables,
		TrustedWholeValues: trustedWholeValues,
	}
	aggregate := credentialpolicy.Result{Disposition: credentialpolicy.ExecutableExact}

	targetInput := enterpriseSurfaceTarget(ref)
	metadata := ref.Metadata()
	originalURL := ""
	projectedOriginalURL := ""
	if metadata != nil {
		originalURL, _ = metadata["original_url"].(string)
	}
	if strings.TrimSpace(originalURL) != "" {
		urlOptions := options
		urlOptions.TargetField = ref.Path + ".target"
		projected, projection, err := credentialpolicy.ProjectRawURL(originalURL, urlOptions)
		if err != nil || projection.Disposition == credentialpolicy.Rejected {
			aggregate.Merge(projection)
			targetInput = "/"
			metadata["original_url"] = credential.Redacted
			projectedOriginalURL = credential.Redacted
			if err != nil {
				aggregate.Merge(rejectedScriptCredentialProjection(source, "credential_request_url_projection_failed"))
			}
		} else {
			metadata["original_url"] = projected
			projectedOriginalURL = projected
			// The adapter may have made the executable target relative and
			// thereby removed userinfo or another governed component from this
			// provenance copy. Retain its protected requirement even when the
			// remaining target projection is exact; identical target/query
			// occurrences are de-duplicated by stable requirement ID.
			aggregate.Merge(projection)
		}
		ref.SetMetadata(metadata)
	}
	if strings.TrimSpace(targetInput) == "" {
		targetInput = "/"
	}
	targetOptions := options
	targetOptions.TargetField = ref.Path + ".target"
	projectedTarget, targetProjection, targetErr := credentialpolicy.ProjectRawURL(targetInput, targetOptions)
	aggregate.Merge(targetProjection)
	if targetErr != nil || targetProjection.Disposition == credentialpolicy.Rejected {
		enterpriseSetSurfaceTarget(ref, "/")
		if targetErr != nil {
			aggregate.Merge(rejectedScriptCredentialProjection(source, "credential_request_url_projection_failed"))
		}
	} else {
		enterpriseSetSurfaceTarget(ref, projectedTarget)
	}

	headers := enterpriseSurfaceHeaders(ref, source)
	headerInput := make([]credentialpolicy.HeaderOccurrence, len(headers))
	for index, header := range headers {
		headerInput[index] = credentialpolicy.HeaderOccurrence{
			Name:   header.Name,
			Value:  header.Value,
			Source: header.Source,
		}
	}
	headerOptions := options
	headerOptions.TargetField = ref.Path + ".header_list"
	projectedHeaders, headerProjection, headerErr := credentialpolicy.ProjectHeaders(headerInput, headerOptions)
	aggregate.Merge(headerProjection)
	if headerErr != nil || headerProjection.Disposition == credentialpolicy.Rejected {
		for index := range headers {
			headers[index].Value = credential.Redacted
		}
		if headerErr != nil {
			aggregate.Merge(rejectedScriptCredentialProjection(source, "credential_request_header_projection_failed"))
		}
	} else {
		for index := range projectedHeaders {
			headers[index].Name = projectedHeaders[index].Name
			headers[index].Value = projectedHeaders[index].Value
		}
	}
	enterpriseSetSurfaceHeaders(ref, headers)

	body, encoding, bodyPresent, bodyShapeValid := enterpriseSurfaceBody(ref)
	if bodyPresent && !bodyShapeValid {
		enterpriseSetSurfaceBody(ref, credential.Redacted)
		enterpriseSetSurfaceBodyEncoding(ref, "")
		aggregate.Merge(rejectedScriptCredentialProjection(source, "credential_request_body_shape_invalid"))
	} else if body != "" {
		payload := []byte(body)
		base64Encoded := strings.EqualFold(strings.TrimSpace(encoding), "base64")
		deferredBase64Template := base64Encoded && dsl.IsTrustedFullBodyBase64Template(body)
		if base64Encoded && !deferredBase64Template {
			decoded, decodeErr := base64.StdEncoding.DecodeString(body)
			if decodeErr != nil {
				payload = nil
				aggregate.Merge(rejectedScriptCredentialProjection(source, "credential_request_body_encoding_invalid"))
			} else {
				payload = decoded
			}
		}
		if payload == nil {
			enterpriseSetSurfaceBody(ref, enterpriseRedactedBody(encoding))
		} else if !deferredBase64Template {
			_, governed := credential.ParseReference(strings.TrimSpace(string(payload)))
			if !governed {
				bodyOptions := options
				bodyOptions.TargetField = ref.Path + ".body"
				projectedBody, bodyProjection, bodyErr := credentialpolicy.ProjectRequestBody(
					payload,
					enterpriseHeaderValue(headers, "Content-Type"),
					bodyOptions,
				)
				aggregate.Merge(bodyProjection)
				if bodyErr != nil || bodyProjection.Disposition == credentialpolicy.Rejected {
					enterpriseSetSurfaceBody(ref, enterpriseRedactedBody(encoding))
					if bodyErr != nil {
						aggregate.Merge(rejectedScriptCredentialProjection(source, "credential_request_body_projection_failed"))
					}
				} else if base64Encoded {
					enterpriseSetSurfaceBody(ref, base64.StdEncoding.EncodeToString(projectedBody))
				} else {
					enterpriseSetSurfaceBody(ref, string(projectedBody))
				}
			}
		}
	}

	if auth, ok := enterpriseSurfaceAuth(ref); ok {
		authOptions := options
		authOptions.TargetField = ref.Path + ".auth"
		projectedAuth, authProjection, authErr := credentialpolicy.ProjectAuth(
			credentialpolicy.AuthFields{
				Type:     auth.Type,
				Username: auth.Username,
				Password: auth.Password,
				Token:    auth.Token,
				Domain:   auth.Domain,
				Source:   source,
			},
			authOptions,
		)
		aggregate.Merge(authProjection)
		if authErr != nil || authProjection.Disposition == credentialpolicy.Rejected {
			auth.Password = credential.Redacted
			auth.Token = credential.Redacted
			if authErr != nil {
				aggregate.Merge(rejectedScriptCredentialProjection(source, "credential_request_auth_projection_failed"))
			}
		} else {
			auth.Password = projectedAuth.Password
			auth.Token = projectedAuth.Token
		}
		enterpriseSetSurfaceAuth(ref, auth)
	}

	appendRequirements(aggregate.Requirements)
	review := ReviewCredentialProjection(aggregate, "imported HTTP request")
	for _, finding := range review.Findings {
		finding.ActionPath = ref.Path
		*findings = append(*findings, finding)
	}
	sanitizeEnterpriseURLMetadata(ref)
	if projectedOriginalURL != "" {
		metadata = ref.Metadata()
		if metadata == nil {
			metadata = make(map[string]any)
		}
		metadata["original_url"] = projectedOriginalURL
		ref.SetMetadata(metadata)
	}
}

func enterpriseSurfaceCredentialSource(ref dslwalk.SurfaceRef, sourceProfile string) credentialpolicy.SourceRef {
	metadata := ref.Metadata()
	file, _ := metadata["source_file"].(string)
	line := 0
	switch value := metadata["source_line"].(type) {
	case int:
		line = value
	case int64:
		line = int(value)
	case float64:
		line = int(value)
	}
	pointer := ref.Path
	if strings.TrimSpace(file) != "" {
		pointer = strings.TrimSpace(file)
		if line > 0 {
			pointer += fmt.Sprintf(":%d", line)
		}
	}
	return credentialpolicy.SourceRef{
		Format:  firstNonEmpty(strings.TrimSpace(sourceProfile), "import"),
		Path:    strings.TrimSpace(file),
		Pointer: pointer,
	}
}

func rejectedScriptCredentialProjection(source credentialpolicy.SourceRef, code string) credentialpolicy.Result {
	return credentialpolicy.Result{
		Disposition: credentialpolicy.Rejected,
		Findings: []credentialpolicy.Finding{{
			Code:        code,
			Source:      source,
			Disposition: credentialpolicy.Rejected,
		}},
	}
}

func appendEnterpriseProjectionFindingsForAllRequests(
	script *dsl.Script,
	input []importreview.Finding,
	findings *[]importreview.Finding,
) {
	if len(input) == 0 {
		return
	}
	_ = dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		if !runtimeSurfaceIsHTTPRequest(ref) {
			return nil
		}
		for _, finding := range input {
			finding.ActionPath = ref.Path
			*findings = append(*findings, finding)
		}
		return nil
	})
}

func enterpriseSurfaceTarget(ref dslwalk.SurfaceRef) string {
	if ref.Action != nil {
		return ref.Action.Target
	}
	value, _ := ref.Request["target"].(string)
	return value
}

func enterpriseSetSurfaceTarget(ref dslwalk.SurfaceRef, target string) {
	if ref.Action != nil {
		ref.Action.Target = target
		return
	}
	ref.Request["target"] = target
}

func enterpriseSurfaceHeaders(ref dslwalk.SurfaceRef, source credentialpolicy.SourceRef) HeaderList {
	var list dsl.HeaderList
	var legacy map[string]string
	if ref.Action != nil {
		list = append(dsl.HeaderList(nil), ref.Action.HeaderList...)
		legacy = ref.Action.Headers
	} else {
		list = enterpriseMapHeaderList(ref.Request["header_list"])
		legacy = enterpriseStringMap(ref.Request["headers"])
	}
	out := make(HeaderList, 0, len(list)+len(legacy))
	seen := make(map[string]struct{}, len(list))
	for _, header := range list {
		identity := strings.ToLower(strings.TrimSpace(header.Name))
		if identity != "" {
			seen[identity] = struct{}{}
		}
		// HeaderList is authoritative. Preserve even malformed names exactly so
		// the runtime-fidelity pass can remove and gate the occurrence with
		// value-free evidence; normalizing or dropping it here would silently
		// erase source semantics before that review.
		out = append(out, Header{Name: header.Name, Value: header.Value, Source: source})
	}
	names := make([]string, 0, len(legacy))
	for name := range legacy {
		if _, exists := seen[strings.ToLower(strings.TrimSpace(name))]; !exists {
			names = append(names, name)
		}
	}
	sort.SliceStable(names, func(i, j int) bool {
		left := strings.ToLower(strings.TrimSpace(names[i]))
		right := strings.ToLower(strings.TrimSpace(names[j]))
		if left == right {
			return names[i] < names[j]
		}
		return left < right
	})
	for _, name := range names {
		out = append(out, Header{Name: name, Value: legacy[name], Source: source})
	}
	return out
}

func enterpriseSetSurfaceHeaders(ref dslwalk.SurfaceRef, headers HeaderList) {
	list := make(dsl.HeaderList, 0, len(headers))
	for _, header := range headers {
		list = append(list, dsl.HeaderEntry{Name: header.Name, Value: header.Value})
	}
	compatibility := list.ToMap()
	if ref.Action != nil {
		ref.Action.HeaderList = list
		ref.Action.Headers = compatibility
		return
	}
	if len(list) == 0 {
		delete(ref.Request, "header_list")
		delete(ref.Request, "headers")
		return
	}
	ref.Request["header_list"] = list
	ref.Request["headers"] = compatibility
}

func enterpriseMapHeaderList(value any) dsl.HeaderList {
	switch typed := value.(type) {
	case dsl.HeaderList:
		return append(dsl.HeaderList(nil), typed...)
	case []dsl.HeaderEntry:
		return append(dsl.HeaderList(nil), typed...)
	case []any:
		out := make(dsl.HeaderList, 0, len(typed))
		for _, item := range typed {
			entry, ok := item.(map[string]any)
			if !ok {
				continue
			}
			name, _ := entry["name"].(string)
			headerValue, _ := entry["value"].(string)
			if strings.TrimSpace(name) != "" {
				out = append(out, dsl.HeaderEntry{Name: name, Value: headerValue})
			}
		}
		return out
	default:
		return nil
	}
}

func enterpriseStringMap(value any) map[string]string {
	switch typed := value.(type) {
	case map[string]string:
		return typed
	case map[string]any:
		out := make(map[string]string, len(typed))
		for key, value := range typed {
			if text, ok := value.(string); ok {
				out[key] = text
			}
		}
		return out
	default:
		return nil
	}
}

func enterpriseHeaderValue(headers HeaderList, name string) string {
	value := ""
	for _, header := range headers {
		if strings.EqualFold(strings.TrimSpace(header.Name), strings.TrimSpace(name)) {
			value = header.Value
		}
	}
	return value
}

func enterpriseSurfaceBody(ref dslwalk.SurfaceRef) (body, encoding string, present, valid bool) {
	if ref.Action != nil {
		return ref.Action.Body, ref.Action.BodyEncoding,
			ref.Action.Body != "" || ref.Action.BodyEncoding != "",
			true
	}
	rawBody, bodyPresent := ref.Request["body"]
	rawEncoding, encodingPresent := ref.Request["body_encoding"]
	if !bodyPresent && !encodingPresent {
		return "", "", false, true
	}
	if bodyPresent {
		var ok bool
		body, ok = rawBody.(string)
		if !ok {
			return "", "", true, false
		}
	}
	if encodingPresent {
		var ok bool
		encoding, ok = rawEncoding.(string)
		if !ok {
			return "", "", true, false
		}
	}
	return body, encoding, true, true
}

func enterpriseSetSurfaceBody(ref dslwalk.SurfaceRef, body string) {
	if ref.Action != nil {
		ref.Action.Body = body
		return
	}
	ref.Request["body"] = body
}

func enterpriseSetSurfaceBodyEncoding(ref dslwalk.SurfaceRef, encoding string) {
	if ref.Action != nil {
		ref.Action.BodyEncoding = encoding
		return
	}
	if strings.TrimSpace(encoding) == "" {
		delete(ref.Request, "body_encoding")
		return
	}
	ref.Request["body_encoding"] = encoding
}

func provenVariableReferencesForSurface(
	ref dslwalk.SurfaceRef,
	missing []string,
) map[string]struct{} {
	referenced := make(map[string]struct{})
	if ref.Action != nil {
		for _, name := range actionVariableReferences(*ref.Action) {
			referenced[name] = struct{}{}
		}
	} else if ref.Request != nil {
		if payload, err := json.Marshal(ref.Request); err == nil {
			collectVariableReferences(string(payload), referenced)
		}
		body, encoding, _, valid := enterpriseSurfaceBody(ref)
		if !valid {
			return nil
		}
		if decoded, encoded, err := dsl.DecodeBodyEncoding(body, encoding, 0); encoded && err == nil {
			collectVariableReferences(string(decoded), referenced)
		}
	}
	for _, name := range missing {
		delete(referenced, strings.TrimSpace(name))
	}
	if len(referenced) == 0 {
		return nil
	}
	return referenced
}

func enterpriseRedactedBody(encoding string) string {
	if strings.EqualFold(strings.TrimSpace(encoding), "base64") {
		return base64.StdEncoding.EncodeToString([]byte(credential.Redacted))
	}
	return credential.Redacted
}

func enterpriseSurfaceAuth(ref dslwalk.SurfaceRef) (dsl.AuthConfig, bool) {
	if ref.Action != nil {
		if ref.Action.Auth == nil {
			return dsl.AuthConfig{}, false
		}
		return *ref.Action.Auth, true
	}
	switch auth := ref.Request["auth"].(type) {
	case *dsl.AuthConfig:
		if auth == nil {
			return dsl.AuthConfig{}, false
		}
		return *auth, true
	case dsl.AuthConfig:
		return auth, true
	case map[string]any:
		return dsl.AuthConfig{
			Type:     enterpriseMapString(auth, "type"),
			Username: enterpriseMapString(auth, "username"),
			Password: enterpriseMapString(auth, "password"),
			Token:    enterpriseMapString(auth, "token"),
			Domain:   enterpriseMapString(auth, "domain"),
		}, true
	default:
		return dsl.AuthConfig{}, false
	}
}

func enterpriseSetSurfaceAuth(ref dslwalk.SurfaceRef, auth dsl.AuthConfig) {
	if ref.Action != nil {
		copy := auth
		ref.Action.Auth = &copy
		return
	}
	ref.Request["auth"] = &auth
}

func enterpriseMapString(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return value
}

func sanitizeEnterpriseURLMetadata(ref dslwalk.SurfaceRef) {
	metadata := ref.Metadata()
	if metadata == nil {
		return
	}
	for _, key := range []string{
		"original_url",
		"inferred_url_source",
		"soap_endpoint_placeholder",
	} {
		if value, ok := metadata[key].(string); ok {
			metadata[key] = scrub.ValueURL(value, scrub.Policy{})
		}
	}
	ref.SetMetadata(metadata)
}

func synchronizeEnterpriseSequenceProvenance(script *dsl.Script) {
	_ = dslwalk.WalkActions(script, func(ref dslwalk.ActionRef) error {
		action := ref.Action
		if action == nil {
			return nil
		}
		if strings.EqualFold(strings.TrimSpace(action.Type), "http_sequence") {
			if target := enterpriseFirstSequenceTarget(action); target != "" {
				action.Target = target
			} else {
				action.Target = "/"
			}
			// The sequence owner itself is not an executable request surface;
			// its provenance metadata was not restored from a governed URL
			// projection the way each child request was.
			metadataRef := dslwalk.SurfaceRef{Action: action, OwnerAction: action}
			sanitizeEnterpriseURLMetadata(metadataRef)
		}
		return nil
	})
}

func enterpriseFirstSequenceTarget(action *dsl.Action) string {
	if action == nil || action.Params == nil {
		return ""
	}
	switch requests := action.Params["requests"].(type) {
	case []map[string]any:
		if len(requests) > 0 {
			target, _ := requests[0]["target"].(string)
			return target
		}
	case []any:
		if len(requests) == 0 {
			return ""
		}
		switch first := requests[0].(type) {
		case map[string]any:
			target, _ := first["target"].(string)
			return target
		case dsl.Action:
			return first.Target
		case *dsl.Action:
			if first != nil {
				return first.Target
			}
		}
	case []dsl.Action:
		if len(requests) > 0 {
			return requests[0].Target
		}
	case []*dsl.Action:
		if len(requests) > 0 && requests[0] != nil {
			return requests[0].Target
		}
	}
	return ""
}

func replaceScriptWithCredentialTraversalReviewDraft(script *dsl.Script, sourceProfile string) {
	if script == nil {
		return
	}
	action := dsl.Action{
		Name:   "Import_request_review",
		Type:   "source_review",
		Target: "",
		Params: map[string]any{
			"function":     "request_import",
			"support_mode": "review_only",
		},
		Metadata: map[string]any{},
	}
	sourceProfile = firstNonEmpty(strings.TrimSpace(sourceProfile), "import")
	importreview.ApplyToAction(&action, []importreview.Finding{{
		Code:       "credential_request_traversal_failed",
		Severity:   diag.Blocker,
		Element:    "imported HTTP requests",
		Detail:     "the imported request tree could not be reviewed within the bounded traversal contract",
		Source:     sourceProfile,
		ReasonCode: "credential_request_traversal_failed",
		Resolution: "reduce_or_replace_the_request_tree_and_remove_gate",
		Gate:       importreview.GateExecution,
	}})
	script.Config.BaseURL = "https://set-base-url.invalid"
	script.Init = nil
	script.Actions = []dsl.Action{action}
	script.Teardown = nil
}
