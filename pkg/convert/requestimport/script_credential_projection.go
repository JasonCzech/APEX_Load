package requestimport

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"github.com/jasonczech/apexload/pkg/protocol"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
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
	remainingReferences, referenceScanComplete := scanRemainingScriptReferences(script)
	retainedConsumed := make(map[string]struct{})
	for name := range consumed {
		// If an extension put a non-JSON value in an executable action surface,
		// serialization cannot prove that no reference remains. Retaining the source
		// is the fidelity-safe failure mode; deleting it would recreate the exact
		// dangling-template defect this reconciliation is meant to prevent.
		if !referenceScanComplete || stringSetContainsFold(remainingReferences.parameters, name) {
			retainedConsumed[name] = struct{}{}
		}
	}
	// Protect every still-referenced parameter binding, not only consumed
	// parameters that themselves remain. Two differently named parameters may
	// alias the same data-source column; removing the consumed alias must not
	// delete the shared column underneath an otherwise unrelated live reference.
	retainedBindings := append([]scriptCredentialDataBinding(nil), remainingReferences.dataBindings...)
	for _, parameter := range script.Parameters {
		name := strings.ToLower(strings.TrimSpace(parameter.Name))
		_, retainConsumed := retainedConsumed[name]
		if !retainConsumed && !stringSetContainsFold(remainingReferences.parameters, name) {
			continue
		}
		if source := strings.TrimSpace(parameter.DataSource); source != "" {
			retainedBindings = append(retainedBindings, scriptCredentialDataBinding{
				source: source,
				column: strings.TrimSpace(parameter.Column),
			})
		}
	}
	protectedBindings := make([]scriptCredentialDataBinding, 0, len(consumed))
	for _, parameter := range script.Parameters {
		name := strings.ToLower(strings.TrimSpace(parameter.Name))
		if _, remove := consumed[name]; !remove {
			continue
		}
		if _, retain := retainedConsumed[name]; retain {
			continue
		}
		if source := strings.TrimSpace(parameter.DataSource); source != "" {
			binding := scriptCredentialDataBinding{
				source: source,
				column: strings.TrimSpace(parameter.Column),
			}
			if !scriptCredentialDataBindingOverlapsAny(binding, retainedBindings) {
				protectedBindings = append(protectedBindings, binding)
			}
		}
	}
	kept := script.Parameters[:0]
	for index := range script.Parameters {
		parameter := script.Parameters[index]
		name := strings.ToLower(strings.TrimSpace(parameter.Name))
		_, removeByName := consumed[name]
		if _, retain := retainedConsumed[name]; retain {
			// MaterializeSourceVariables rewrites only credential-bearing
			// surfaces. A parameter still referenced anywhere else remains part of
			// the executable script contract, so replacing its source value with a
			// redaction marker is as destructive as deleting it: the authored
			// non-credential surface would send "[REDACTED]" at runtime. Preserve
			// the existing parameter unchanged until every reference is consumed.
			kept = append(kept, parameter)
			continue
		}
		if removeByName || parameterUsesCredentialDataBinding(parameter, protectedBindings) {
			for valueIndex := range parameter.Values {
				parameter.Values[valueIndex] = ""
			}
			continue
		}
		kept = append(kept, parameter)
	}
	script.Parameters = kept
	removeCredentialDataSourceColumns(script, protectedBindings, retainedBindings)
}

func scriptCredentialDataBindingOverlapsAny(candidate scriptCredentialDataBinding, retained []scriptCredentialDataBinding) bool {
	for _, binding := range retained {
		if !strings.EqualFold(strings.TrimSpace(candidate.source), strings.TrimSpace(binding.source)) {
			continue
		}
		candidateColumn := strings.TrimSpace(candidate.column)
		retainedColumn := strings.TrimSpace(binding.column)
		if candidateColumn == "" || retainedColumn == "" || strings.EqualFold(candidateColumn, retainedColumn) {
			return true
		}
	}
	return false
}

type remainingScriptReferenceSet struct {
	parameters   map[string]struct{}
	dataBindings []scriptCredentialDataBinding
}

var dataTemplatePattern = regexp.MustCompile(`\{\{\s*data\.([^{}]+)\}\}`)

func scanRemainingScriptReferences(script *dsl.Script) (remainingScriptReferenceSet, bool) {
	if script == nil {
		return remainingScriptReferenceSet{}, true
	}
	// Scan only executable surfaces. Parameter definitions, data-source bytes,
	// and top-level import provenance may legitimately preserve the authored
	// token as evidence, but they do not resolve {{param.*}} at runtime. Treating
	// those copies as live references would retain public credential material
	// even after every executable use had been governed.
	surfaces := struct {
		Config   dsl.ScriptConfig `json:"config"`
		Init     []dsl.Action     `json:"init,omitempty"`
		Actions  []dsl.Action     `json:"actions,omitempty"`
		Teardown []dsl.Action     `json:"teardown,omitempty"`
	}{
		Config:   script.Config,
		Init:     script.Init,
		Actions:  script.Actions,
		Teardown: script.Teardown,
	}
	encoded, err := json.Marshal(surfaces)
	if err != nil {
		return remainingScriptReferenceSet{}, false
	}
	references := remainingScriptReferenceSet{parameters: make(map[string]struct{})}
	collectParameterReferences(string(encoded), references.parameters)
	directDataKeys := make(map[string]struct{})
	for _, match := range dataTemplatePattern.FindAllStringSubmatch(string(encoded), -1) {
		if len(match) != 2 {
			continue
		}
		if key := strings.ToLower(strings.TrimSpace(match[1])); key != "" {
			directDataKeys[key] = struct{}{}
		}
	}
	// Resolve the expression against every known source name instead of splitting
	// at the first dot. Both source and column names may themselves contain dots;
	// ambiguous composites deliberately protect every matching binding.
	for _, source := range script.DataSources {
		sourceName := strings.TrimSpace(source.Name)
		normalizedSource := strings.ToLower(sourceName)
		if normalizedSource == "" {
			continue
		}
		prefix := normalizedSource + "."
		for key := range directDataKeys {
			if key == normalizedSource {
				references.dataBindings = append(references.dataBindings, scriptCredentialDataBinding{source: sourceName})
				continue
			}
			if strings.HasPrefix(key, prefix) {
				references.dataBindings = append(references.dataBindings, scriptCredentialDataBinding{
					source: sourceName,
					column: strings.TrimSpace(strings.TrimPrefix(key, prefix)),
				})
			}
		}
	}
	return references, true
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
	fallbackSnapshot := credentialTraversalSnapshot(script)
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
	if options.RuntimeParameterPolicy == RuntimeParametersPreserveGoverned {
		// LoadRunner parameter files are executable runtime data rather than
		// declarations of project secrets. Preserve both their exact whole
		// references and their imported source values in the executable script.
		// API/read projections mask those values without changing the stored
		// runtime data, and masked updates are reconciled against that stored
		// copy. Removing the values here would make rotating credential tables
		// impossible to replay without asking the importer to recreate them.
		sourceVariables = nil
		trustedWholeValues = trustedScriptParameterValues(script)
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
		replaceScriptWithCredentialTraversalReviewDraft(script, options.SourceProfile, fallbackSnapshot)
		result.AdditionalURLs = nil
		result.FallbackDraft = true
		return result, fmt.Errorf("HTTP credential review exceeded the bounded action traversal limit")
	}
	if requirementConflict {
		// The request tree itself is intact and valid here; only the credential
		// bookkeeping is ambiguous. Keep every authored action, base URL, and
		// imported value and gate execution instead of swapping in a review stub
		// that throws the operator's script away.
		gateScriptWithCredentialReviewFailure(
			script,
			options.SourceProfile,
			fallbackSnapshot,
			"credential_request_review_conflict",
			"conflicting credential requirement provenance prevented a complete HTTP credential review",
		)
		result.FallbackDraft = true
		return result, fmt.Errorf("HTTP credential review found conflicting requirement provenance")
	}
	synchronizeEnterpriseSequenceProvenance(script)
	if err := importreview.ApplyToScript(script, findings); err != nil {
		gateScriptWithCredentialReviewFailure(
			script,
			options.SourceProfile,
			fallbackSnapshot,
			"credential_request_review_unattachable",
			"the HTTP credential review could not be attached to every imported request",
		)
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

// preserveCredentialSourceVariables records runtime-redaction locators for the
// script parameters and data sources that supplied a credential value.
//
// It used to overwrite parameter.Values and DataSource.InlineData with the
// redaction sentinel. That destroyed executable runtime data the operator had
// already imported: a parameter list rewritten to "[REDACTED]" sends that
// literal on the wire, and a blanked inline data source strands every other
// column in the same CSV. Nothing from an imported script is a project secret,
// so the value stays and only its locator is published; run-output redaction
// consumes those locators to mask logs, snapshots, and reports.
//
// Every field name published here must be one pkg/engine can resolve
// (collectScriptConfigTarget). engine.ImportedCredentialValues is fail-closed:
// a locator it does not understand refuses the run instead of redacting
// anything, so an unresolvable locator is worse than no locator at all. The
// cross-package guard is TestPublishedCredentialLocatorFieldsAreRuntimeResolvable.
func preserveCredentialSourceVariables(
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
		// map, so its backing parameter still needs a redaction locator.
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
	var targets []credentialpolicy.TargetRef
	dataSourceColumns := make(map[string]map[string]struct{})
	for index := range script.Parameters {
		parameter := &script.Parameters[index]
		if !stringSetContainsFold(names, parameter.Name) {
			continue
		}
		if parameterHasRuntimeValue(*parameter) {
			targets = append(targets, credentialpolicy.TargetRef{
				Field: fmt.Sprintf(
					scriptParameterValuesLocatorPrefix+"%d"+scriptParameterValuesLocatorSuffix,
					index,
				),
				Index: index,
			})
		}
		source := strings.TrimSpace(parameter.DataSource)
		column := strings.TrimSpace(parameter.Column)
		if source == "" || column == "" {
			// The runtime resolves a data-source-backed parameter only when it
			// has BOTH a source and a column, so a binding without a column
			// supplies no runtime value for redaction to mask.
			continue
		}
		if dataSourceColumns[source] == nil {
			dataSourceColumns[source] = make(map[string]struct{})
		}
		dataSourceColumns[source][column] = struct{}{}
	}
	for index := range script.DataSources {
		source := &script.DataSources[index]
		columns := dataSourceColumns[strings.TrimSpace(source.Name)]
		if len(columns) == 0 || source.InlineData == "" {
			continue
		}
		// The credential column travels in the locator. A whole-sheet locator
		// would make the runtime register every cell of the source, redacting
		// unrelated columns out of the operator's own run evidence.
		for _, column := range sortedStringSet(columns) {
			targets = append(targets, credentialpolicy.TargetRef{
				Field: fmt.Sprintf(
					scriptDataSourceLocatorPrefix+"%d"+scriptDataSourceLocatorSuffix,
					index,
				),
				Index:   index,
				Pointer: column,
			})
		}
	}
	attachScriptPreservedCredentialTargets(script, targets)
}

// parameterHasRuntimeValue reports whether an inline value list still carries
// something a run could emit. An earlier conversion pass can blank the entries
// of a list it consumed, and a locator pointing at an all-empty list would name
// no executable value.
func parameterHasRuntimeValue(parameter dsl.Parameter) bool {
	for _, value := range parameter.Values {
		if strings.TrimSpace(value) != "" && value != credential.Redacted {
			return true
		}
	}
	return false
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
	rawBaseURL := script.Config.BaseURL
	source := credentialpolicy.SourceRef{
		Format:  options.SourceProfile,
		Pointer: "/config/base_url",
	}
	projected, projection, err := credentialpolicy.ProjectRawURL(
		script.Config.BaseURL,
		credentialpolicy.Options{
			Mode:               credentialpolicy.Preserve,
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
		// A projection failure is a review gate, not permission to discard the
		// operator's own base URL. Retain the authored bytes and record the
		// locator so runtime redaction can still mask them in run output.
		script.Config.BaseURL = rawBaseURL
		projection.Merge(blockedPreservedScriptCredentialProjection(
			source,
			"credential_base_url_projection_failed",
			credentialpolicy.TargetRef{
				RequestKey: options.CandidateKey + ":base_url",
				Field:      "config.base_url",
			},
		))
	} else {
		script.Config.BaseURL = projected
	}
	attachScriptPreservedCredentialTargets(script, projection.PreservedTargets)
	appendRequirements(projection.Requirements)
	reviewFindings := ReviewCredentialProjection(projection, "HTTP base URL").Findings
	if err == nil &&
		projection.Disposition != credentialpolicy.Rejected &&
		baseURLHasUnresolvableRuntimeTemplate(rawBaseURL) {
		reviewFindings = append(reviewFindings, baseURLRuntimeTemplateFinding(source))
		reviewFindings = importreview.NormalizeFindings(reviewFindings)
	}
	appendBaseURLProjectionFindings(
		script,
		reviewFindings,
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
				Mode:               credentialpolicy.Preserve,
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
			// Retain the exact imported URL. Replacing it with a redaction
			// sentinel destroys a value the operator already owns and leaves an
			// allowlist/recorded-resource entry that can never match again.
			projectedURLs[index] = item.Value
			projection.Merge(blockedScriptCredentialProjection(
				source,
				"credential_additional_url_projection_failed",
			))
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
				Mode:               credentialpolicy.Preserve,
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
			// Keep the authored token endpoint. Substituting an .invalid host
			// guarantees the OAuth exchange fails at replay; the review gate
			// below already prevents execution until an operator looks at it.
			surface.set(surface.raw)
			aggregate.Merge(blockedPreservedScriptCredentialProjection(
				surfaceSource,
				"credential_oauth_token_url_projection_failed",
				credentialpolicy.TargetRef{
					RequestKey: scriptOptions.CandidateKey + ":" + ref.Path + surface.requestKeySuffix,
					Field:      surface.targetField,
				},
			))
			continue
		}
		surface.set(projected)
	}
	appendRequirements(aggregate.Requirements)
	ref.SetMetadata(attachPreservedCredentialTargetsMetadata(ref.Metadata(), aggregate.PreservedTargets))
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
		Mode:               credentialpolicy.Preserve,
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
	if metadata != nil {
		originalURL, _ = metadata["original_url"].(string)
	}
	if strings.TrimSpace(originalURL) != "" {
		urlOptions := options
		urlOptions.TargetField = ref.Path + ".metadata.original_url"
		projected, projection, err := credentialpolicy.ProjectRawURL(originalURL, urlOptions)
		if err != nil || projection.Disposition == credentialpolicy.Rejected {
			aggregate.Merge(projection)
			// original_url is provenance metadata. Failing to project it is not
			// a reason to overwrite the executable target with "/" nor to erase
			// the recorded URL; keep both and gate the action for review.
			aggregate.Merge(blockedScriptCredentialProjection(
				source,
				"credential_request_url_projection_failed",
			))
		} else {
			metadata["original_url"] = projected
			// original_url is provenance only. Its disclosure-safe review
			// findings remain useful, but its locators must never be attached to
			// the action as if this metadata were the executable target.
			projection.PreservedTargets = nil
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
		// Retain the authored request target byte-for-byte. Rewriting it to "/"
		// silently repoints the request at the base URL root, which is both a
		// destroyed credential value and a request the operator never wrote.
		enterpriseSetSurfaceTarget(ref, targetInput)
		aggregate.Merge(blockedPreservedScriptCredentialProjection(
			source,
			"credential_request_url_projection_failed",
			credentialpolicy.TargetRef{
				RequestKey: requestKey,
				Field:      ref.Path + ".target",
			},
		))
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
		// One unprojectable header must not blank every header on the request.
		// Retain the authored occurrences exactly and record their locators so
		// runtime redaction can mask them without an operator re-entering them.
		preservedHeaderTargets := make([]credentialpolicy.TargetRef, 0, len(headers))
		for index := range headers {
			preservedHeaderTargets = append(preservedHeaderTargets, credentialpolicy.TargetRef{
				RequestKey: requestKey,
				Field:      ref.Path + ".header_list",
				Index:      index,
				Occurrence: index,
			})
		}
		aggregate.Merge(blockedPreservedScriptCredentialProjection(
			source,
			"credential_request_header_projection_failed",
			preservedHeaderTargets...,
		))
	} else {
		for index := range projectedHeaders {
			headers[index].Name = projectedHeaders[index].Name
			headers[index].Value = projectedHeaders[index].Value
		}
	}
	enterpriseSetSurfaceHeaders(ref, headers)

	body, encoding, bodyPresent, bodyShapeValid := enterpriseSurfaceBody(ref)
	if bodyPresent && !bodyShapeValid {
		// The adapter stored a non-string body (or encoding), which no projector
		// can inspect and the DSL cannot hold. Overwriting it with a redaction
		// sentinel destroyed an imported payload the operator already owned.
		// Store the exact imported value using the DSL's valid representation —
		// the same contract the malformed-base64 branch below uses — and gate
		// execution; never replace credential material with a sentinel.
		enterpriseSetSurfaceBody(ref, representableEnterpriseSurfaceBody(ref.Request["body"]))
		enterpriseSetSurfaceBodyEncoding(ref, "")
		aggregate.Merge(blockedPreservedScriptCredentialProjection(
			source,
			"credential_request_body_shape_invalid",
			credentialpolicy.TargetRef{
				RequestKey: requestKey,
				Field:      ref.Path + ".body.opaque",
			},
		))
	} else if body != "" {
		payload := []byte(body)
		base64Encoded := strings.EqualFold(strings.TrimSpace(encoding), "base64")
		deferredBase64Template := base64Encoded && dsl.IsTrustedFullBodyBase64Template(body)
		if base64Encoded && !deferredBase64Template {
			decoded, decodeErr := base64.StdEncoding.DecodeString(body)
			if decodeErr != nil {
				// The declared base64 is malformed, so its intended wire bytes
				// are unknowable. Store the exact imported text using the DSL's
				// valid base64 representation and gate execution; never replace
				// credential material with a redaction sentinel.
				payload = []byte(body)
				enterpriseSetSurfaceBody(
					ref,
					base64.StdEncoding.EncodeToString(payload),
				)
				enterpriseSetSurfaceBodyEncoding(ref, "base64")
				aggregate.Merge(blockedPreservedScriptCredentialProjection(
					source,
					"credential_request_body_encoding_invalid",
					credentialpolicy.TargetRef{
						RequestKey: requestKey,
						Field:      ref.Path + ".body.opaque",
					},
				))
			} else {
				payload = decoded
			}
		}
		if payload != nil && !deferredBase64Template {
			_, governed := credential.ParseReference(strings.TrimSpace(string(payload)))
			if !governed {
				bodyOptions := options
				bodyOptions.TargetField = ref.Path + ".body"
				projectedBody, bodyProjection, bodyErr := credentialpolicy.ProjectRequestBody(
					payload,
					enterpriseHeaderValue(headers, "Content-Type"),
					bodyOptions,
				)
				if bodyErr != nil || bodyProjection.Disposition == credentialpolicy.Rejected {
					// Preserve the original executable body. A parser failure
					// is a review gate, not permission to erase a password the
					// operator already imported.
					aggregate.Merge(blockedPreservedScriptCredentialProjection(
						source,
						"credential_request_body_projection_failed",
						credentialpolicy.TargetRef{
							RequestKey: requestKey,
							Field:      ref.Path + ".body.opaque",
						},
					))
				} else if base64Encoded {
					aggregate.Merge(bodyProjection)
					enterpriseSetSurfaceBody(ref, base64.StdEncoding.EncodeToString(projectedBody))
				} else {
					aggregate.Merge(bodyProjection)
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
		if authErr != nil || authProjection.Disposition == credentialpolicy.Rejected {
			aggregate.Merge(blockedPreservedScriptCredentialProjection(
				source,
				"credential_request_auth_projection_failed",
				preservedAuthTargets(requestKey, ref.Path, auth)...,
			))
		} else {
			aggregate.Merge(authProjection)
			auth.Password = projectedAuth.Password
			auth.Token = projectedAuth.Token
			switch httpprotocol.NormalizeAuthType(auth.Type) {
			case httpprotocol.AuthTypeBasic,
				httpprotocol.AuthTypeBearer,
				httpprotocol.AuthTypeDigest:
				if err := httpprotocol.ValidateAuthWireValues(&protocol.AuthConfig{
					Type:     auth.Type,
					Username: auth.Username,
					Password: auth.Password,
					Token:    auth.Token,
					Domain:   auth.Domain,
				}); err != nil {
					aggregate.Merge(blockedPreservedScriptCredentialProjection(
						source,
						"invalid_auth_wire_value",
						preservedAuthTargets(requestKey, ref.Path, auth)...,
					))
				}
			}
		}
		enterpriseSetSurfaceAuth(ref, auth)
	}

	appendRequirements(aggregate.Requirements)
	ref.SetMetadata(attachPreservedCredentialTargetsMetadata(ref.Metadata(), aggregate.PreservedTargets))
	review := ReviewCredentialProjection(aggregate, "imported HTTP request")
	for _, finding := range review.Findings {
		finding.ActionPath = ref.Path
		*findings = append(*findings, finding)
	}
	sanitizeEnterpriseURLMetadata(ref)
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

// blockedScriptCredentialProjection gates a surface whose projection failed
// without claiming an executable preserved locator. It is used where the
// affected value is provenance metadata rather than a wire field, so the
// imported bytes stay exactly as authored but the draft cannot execute until
// an operator reviews it.
func blockedScriptCredentialProjection(source credentialpolicy.SourceRef, code string) credentialpolicy.Result {
	return credentialpolicy.Result{
		Disposition: credentialpolicy.BlockedDraft,
		Findings: []credentialpolicy.Finding{{
			Code:        code,
			Source:      source,
			Disposition: credentialpolicy.BlockedDraft,
		}},
	}
}

func blockedPreservedScriptCredentialProjection(
	source credentialpolicy.SourceRef,
	code string,
	targets ...credentialpolicy.TargetRef,
) credentialpolicy.Result {
	return credentialpolicy.Result{
		Disposition: credentialpolicy.BlockedDraft,
		Findings: []credentialpolicy.Finding{{
			Code:        code,
			Source:      source,
			Disposition: credentialpolicy.BlockedDraft,
		}},
		PreservedTargets: append([]credentialpolicy.TargetRef(nil), targets...),
	}
}

func preservedAuthTargets(
	requestKey string,
	actionPath string,
	auth dsl.AuthConfig,
) []credentialpolicy.TargetRef {
	var targets []credentialpolicy.TargetRef
	if auth.Password != "" {
		targets = append(targets, credentialpolicy.TargetRef{
			RequestKey: requestKey,
			Field:      actionPath + ".auth.password",
		})
	}
	if auth.Token != "" {
		targets = append(targets, credentialpolicy.TargetRef{
			RequestKey: requestKey,
			Field:      actionPath + ".auth.token",
			Occurrence: 1,
		})
	}
	return targets
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

func appendBaseURLProjectionFindings(
	script *dsl.Script,
	input []importreview.Finding,
	findings *[]importreview.Finding,
) {
	if len(input) == 0 {
		return
	}
	_ = dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		usesBaseURL := false
		for _, surface := range importURLCredentialSurfaces(ref) {
			if surface.usesBaseURL {
				usesBaseURL = true
				break
			}
		}
		if !usesBaseURL {
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
		out = append(out, Header{Name: header.Name, Value: header.Value, Enabled: header.Enabled, Source: source})
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
		list = append(list, dsl.HeaderEntry{Name: header.Name, Value: header.Value, Enabled: header.Enabled})
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
				header := dsl.HeaderEntry{Name: name, Value: headerValue}
				if enabled, ok := entry["enabled"].(bool); ok {
					header.Enabled = &enabled
				}
				out = append(out, header)
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
		if header.Enabled != nil && !*header.Enabled {
			continue
		}
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

// representableEnterpriseSurfaceBody renders a non-string imported body into
// the string the DSL can hold without discarding what the source contained.
// JSON is preferred because it round trips objects, arrays, numbers, and
// booleans exactly; the Go rendering is only a last resort for values JSON
// cannot encode, and the surface is execution-gated either way.
func representableEnterpriseSurfaceBody(body any) string {
	if body == nil {
		return ""
	}
	if text, ok := body.(string); ok {
		return text
	}
	if encoded, err := json.Marshal(body); err == nil {
		return string(encoded)
	}
	return fmt.Sprint(body)
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
	// Imported provenance is part of the operator-owned script and remains exact.
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

func credentialTraversalSnapshot(script *dsl.Script) string {
	if script == nil {
		return ""
	}
	payload, err := json.Marshal(script)
	if err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(payload)
}

// gateScriptWithCredentialReviewFailure blocks execution of an imported script
// whose credential review could not be completed, without altering a single
// executable byte of it.
//
// The credential review failing is not evidence that the imported requests are
// wrong: the base URL, actions, bodies, headers, and imported credential values
// are exactly what the operator's source contained, and they must survive into
// the stored draft so the operator can repair the gate instead of re-authoring
// the script. Gates are written directly onto the top-level actions because the
// bounded walker is exactly what may have failed.
func gateScriptWithCredentialReviewFailure(
	script *dsl.Script,
	sourceProfile string,
	snapshot string,
	code string,
	detail string,
) {
	if script == nil {
		return
	}
	sourceProfile = firstNonEmpty(strings.TrimSpace(sourceProfile), "import")
	retainCredentialTraversalSnapshot(script, snapshot)
	finding := importreview.Finding{
		Code:       code,
		Severity:   diag.Blocker,
		Element:    "imported HTTP requests",
		Detail:     detail,
		Source:     sourceProfile,
		ReasonCode: code,
		Resolution: "resolve_the_credential_review_and_remove_gate",
		Gate:       importreview.GateExecution,
	}
	for _, actions := range [][]dsl.Action{script.Init, script.Actions, script.Teardown} {
		for index := range actions {
			importreview.ApplyToAction(&actions[index], []importreview.Finding{finding})
		}
	}
}

func retainCredentialTraversalSnapshot(script *dsl.Script, snapshot string) {
	if script == nil || snapshot == "" {
		return
	}
	if script.Metadata == nil {
		script.Metadata = &dsl.ScriptMetadata{}
	}
	if script.Metadata.Import == nil {
		script.Metadata.Import = &dsl.ImportMetadata{}
	}
	if script.Metadata.Import.Preserved == nil {
		script.Metadata.Import.Preserved = map[string]any{}
	}
	script.Metadata.Import.Preserved["credential_traversal_source_snapshot_base64"] = snapshot
}

// replaceScriptWithCredentialTraversalReviewDraft is the last-resort fallback
// for a request tree the bounded walker cannot traverse. Such a tree exceeds
// dsl.MaxScriptActionDefinitions, so it can never be stored as a valid script;
// the exact source is retained verbatim in the preserved snapshot instead. The
// authored base URL is deliberately kept: it is an executable value in its own
// right and nothing about the traversal failure makes it unusable.
func replaceScriptWithCredentialTraversalReviewDraft(script *dsl.Script, sourceProfile, snapshot string) {
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
	retainCredentialTraversalSnapshot(script, snapshot)
	script.Init = nil
	script.Actions = []dsl.Action{action}
	script.Teardown = nil
}
