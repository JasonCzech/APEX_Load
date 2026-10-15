package requestimport

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"github.com/jasonczech/apexload/pkg/protocol"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
	"golang.org/x/net/http/httpguts"
)

// Phase selects the script section receiving a projected request.
type Phase string

const (
	PhaseInit     Phase = "init"
	PhaseActions  Phase = "actions"
	PhaseTeardown Phase = "teardown"
)

// ActionOverlay carries source-specific DSL features that are intentionally not
// part of the shared HTTP IR.
type ActionOverlay struct {
	Type           string
	Params         map[string]any
	Metadata       map[string]any
	Extractors     []dsl.Extractor
	Assertions     []dsl.Assertion
	ThinkTime      dsl.ThinkTimeConfig
	ThinkTimeAfter bool
	SOAP           *dsl.SOAPActionConfig
}

// RequestInput combines one effective request with source-specific additions.
type RequestInput struct {
	Phase        Phase
	Request      Request
	Overlay      ActionOverlay
	Findings     []importreview.Finding
	Requirements []credentialpolicy.Requirement
}

// BoundsPolicy controls source settings outside native runtime limits.
type BoundsPolicy string

const (
	BoundsReject           BoundsPolicy = "reject"
	BoundsClampWithReview  BoundsPolicy = "clamp_with_review"
	BoundsClampWithWarning BoundsPolicy = "clamp_with_warning"
)

// ProjectOptions controls deterministic request-to-DSL decisions.
type ProjectOptions struct {
	Bounds BoundsPolicy
}

// DefaultProjectOptions returns the conservative import policy: preserve a
// bounded draft, but require review when source semantics exceeded runtime
// capabilities.
func DefaultProjectOptions() ProjectOptions {
	return ProjectOptions{Bounds: BoundsClampWithReview}
}

// Draft is the mutable intermediate returned to a source adapter. Adapters may
// apply source-specific correlation or assertion translation to Script before
// calling Finalize.
type Draft struct {
	Script         *dsl.Script
	Findings       []importreview.Finding
	Requirements   []credentialpolicy.Requirement
	SourceMappings []SourceMapping
}

// FinalizeOptions supplies environment-dependent readiness facts.
type FinalizeOptions struct {
	UnresolvedBindings int
	Project            ProjectOptions
	Credentials        *CredentialProjectionOptions
	Overlay            ActionOverlay
	Findings           []importreview.Finding
	Requirements       []credentialpolicy.Requirement
	ActionPath         string
}

// EffectiveRequest is the public source-neutral request contract. Request is
// retained as the shorter compatibility name used by the initial adopters.
type EffectiveRequest = Request

// Disposition is the stable conversion outcome used by importer adapters.
type Disposition string

const (
	ExecutableExact   Disposition = "executable_exact"
	ExecutableBounded Disposition = "executable_bounded"
	BlockedDraft      Disposition = "blocked_draft"
	Rejected          Disposition = "rejected"
)

// Report is the per-request finalization sidecar. It contains no source
// credential values; Requirement.Value must remain inside a protected session.
type Report struct {
	RequestKey     string                         `json:"request_key,omitempty"`
	ActionPath     string                         `json:"action_path,omitempty"`
	Disposition    Disposition                    `json:"disposition"`
	Findings       []importreview.Finding         `json:"findings,omitempty"`
	Requirements   []credentialpolicy.Requirement `json:"requirements,omitempty"`
	SourceMappings []SourceMapping                `json:"source_mappings,omitempty"`
	Assets         []Asset                        `json:"-"`
	// runtimeFidelity carries source occurrence indexes across credential
	// projection. Preserve mode may also carry exact omitted header bytes until
	// they are encoded into gated action metadata; the field is unexported and
	// never changes the public report.
	runtimeFidelity runtimeFidelityProjection
	// preservedTargets contains disclosure-safe executable field locators. The
	// finalizer writes them to action metadata; no credential value is carried
	// in this report sidecar.
	preservedTargets []credentialpolicy.TargetRef
}

// Result is the common finalization sidecar. Converter-specific public result
// types can map or retain it without changing their wire contracts.
type Result struct {
	Script           *dsl.Script
	Disposition      Disposition
	Findings         []importreview.Finding
	Diagnostics      diag.Diagnostics
	Requirements     []credentialpolicy.Requirement
	SourceMappings   []SourceMapping
	ValidationIssues []string
	Readiness        importreview.Readiness
}

// FinalizeResult is the explicit public name used by source adapters.
type FinalizeResult = Result

// FinalizeAction projects one fully effective request into a DSL action. Source
// parsing, inheritance, and template recognition must already be complete.
func FinalizeAction(request EffectiveRequest, options FinalizeOptions) (dsl.Action, Report, error) {
	credentialReport := Report{RequestKey: request.Key, Disposition: ExecutableExact}
	if options.Credentials != nil {
		var err error
		request, credentialReport, err = ProjectCredentials(request, *options.Credentials)
		if err != nil {
			return dsl.Action{}, credentialReport, credentialProjectionInvariant(credentialReport, err)
		}
	}
	projectOptions := options.Project
	if projectOptions.Bounds == "" {
		projectOptions = DefaultProjectOptions()
	}
	normalizedOptions, err := normalizeProjectOptions(projectOptions)
	if err != nil {
		return dsl.Action{}, Report{RequestKey: request.Key, Disposition: Rejected}, err
	}
	action, generated, assets, runtimeProjection, err := projectRequest(request, options.Overlay, normalizedOptions)
	if err != nil {
		return dsl.Action{}, Report{RequestKey: request.Key, Disposition: Rejected}, err
	}
	attachPreservedCredentialTargets(&action, credentialReport.preservedTargets)
	// projectRequest records any post-projection/synthesized omissions first.
	// Append source-wire omissions discovered before credential replacement so
	// metadata order and source mappings remain aligned.
	applyRuntimeFidelityMetadata(&action, credentialReport.runtimeFidelity)
	runtimeProjection.HeaderOmissions = append(
		runtimeProjection.HeaderOmissions,
		credentialReport.runtimeFidelity.HeaderOmissions...,
	)
	runtimeProjection.MultipartOmissions = append(
		runtimeProjection.MultipartOmissions,
		credentialReport.runtimeFidelity.MultipartOmissions...,
	)
	findings := append([]importreview.Finding(nil), options.Findings...)
	findings = append(findings, credentialReport.Findings...)
	findings = append(findings, generated...)
	for index := range findings {
		findings[index].ActionPath = strings.TrimSpace(options.ActionPath)
		if findings[index].Location == "" {
			findings[index].Location = sourceLocation(request.Source)
		}
	}
	findings = importreview.NormalizeFindings(findings)
	importreview.ApplyToAction(&action, findings)
	requirementProjection := runtimeProjection
	if options.Credentials != nil {
		// Credential-generated requirements already use the sanitized header
		// indexes. Only caller-supplied requirements still refer to the
		// original source occurrences.
		requirementProjection = credentialReport.runtimeFidelity
	}
	optionRequirements := pruneRequirementsForHeaderOmissions(
		options.Requirements,
		request.Key,
		requirementProjection.HeaderOmissions,
	)
	report := Report{
		RequestKey:  request.Key,
		ActionPath:  strings.TrimSpace(options.ActionPath),
		Disposition: worseRequestDisposition(dispositionForFindings(findings), credentialReport.Disposition),
		Findings:    findings,
		SourceMappings: dedupeSourceMappings(append(
			effectiveSourceMappings(request, options.ActionPath),
			runtimeFidelitySourceMappings(runtimeProjection, options.ActionPath, 0)...,
		)),
		Assets: cloneAssets(assets),
		Requirements: append(
			optionRequirements,
			credentialpolicy.CloneRequirements(credentialReport.Requirements)...,
		),
	}
	return action, report, nil
}

func attachPreservedCredentialTargets(action *dsl.Action, targets []credentialpolicy.TargetRef) {
	if action == nil || len(targets) == 0 {
		return
	}
	action.Metadata = attachPreservedCredentialTargetsMetadata(action.Metadata, targets)
}

func attachPreservedCredentialTargetsMetadata(
	metadata map[string]any,
	targets []credentialpolicy.TargetRef,
) map[string]any {
	return MergePreservedCredentialTargetsMetadata(metadata, targets)
}

// MergePreservedCredentialTargetsMetadata appends disclosure-safe runtime
// redaction locators without discarding locators already attached by an
// adapter or an earlier projection pass. Encounter order is stable and exact
// duplicate occurrences are removed.
func MergePreservedCredentialTargetsMetadata(
	metadata map[string]any,
	targets []credentialpolicy.TargetRef,
) map[string]any {
	if len(targets) == 0 {
		return metadata
	}
	existing := preservedCredentialTargetsFromMetadata(metadata)
	seen := make(map[string]struct{}, len(existing)+len(targets))
	normalized := make([]credentialpolicy.TargetRef, 0, len(existing)+len(targets))
	targets = append(existing, targets...)
	for _, target := range targets {
		target.Field = strings.TrimSpace(target.Field)
		if target.Field == "" || target.Index < 0 || target.Occurrence < 0 {
			continue
		}
		key := fmt.Sprintf("%s\x00%s\x00%d\x00%s\x00%d",
			target.RequestKey, target.Field, target.Index, target.Pointer, target.Occurrence)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, target)
	}
	if len(normalized) == 0 {
		return metadata
	}
	if metadata == nil {
		metadata = make(map[string]any)
	}
	metadata[dsl.ImportPreservedCredentialTargetsMetadataKey] = normalized
	return metadata
}

func preservedCredentialTargetsFromMetadata(
	metadata map[string]any,
) []credentialpolicy.TargetRef {
	if metadata == nil {
		return nil
	}
	raw, exists := metadata[dsl.ImportPreservedCredentialTargetsMetadataKey]
	if !exists || raw == nil {
		return nil
	}
	payload, err := json.Marshal(raw)
	if err != nil || len(payload) > 256<<10 {
		return nil
	}
	var targets []credentialpolicy.TargetRef
	if err := json.Unmarshal(payload, &targets); err != nil {
		return nil
	}
	return targets
}

func attachScriptPreservedCredentialTargets(script *dsl.Script, targets []credentialpolicy.TargetRef) {
	if script == nil || len(targets) == 0 {
		return
	}
	if script.Metadata == nil {
		script.Metadata = &dsl.ScriptMetadata{}
	}
	if script.Metadata.Import == nil {
		script.Metadata.Import = &dsl.ImportMetadata{}
	}
	if script.Metadata.Import.Preserved == nil {
		script.Metadata.Import.Preserved = make(map[string]any)
	}
	metadata := make(map[string]any, 1)
	if existing, ok := script.Metadata.Import.Preserved[dsl.ImportPreservedCredentialTargetsMetadataKey]; ok {
		metadata[dsl.ImportPreservedCredentialTargetsMetadataKey] = existing
	}
	metadata = attachPreservedCredentialTargetsMetadata(metadata, targets)
	value, ok := metadata[dsl.ImportPreservedCredentialTargetsMetadataKey]
	if !ok {
		return
	}
	script.Metadata.Import.Preserved[dsl.ImportPreservedCredentialTargetsMetadataKey] = value
}

// FinalizeScript applies all request reports, validates the complete script,
// and returns a source-neutral readiness result. Any DSL validation issue here
// is an internal invariant failure: importers must return valid blocked drafts,
// not invalid runnable scripts.
func FinalizeScript(script *dsl.Script, reports []Report, options FinalizeOptions) (*FinalizeResult, error) {
	if script == nil {
		return nil, errors.New("script is required")
	}
	markCredentialPreservingImport(script)
	if isHTTPFamilyProtocol(script.Protocol) && script.Config.HTTP.Traceparent == nil {
		// Source-exact imports must not acquire the runtime's opt-out
		// traceparent default. Preserve an explicit source/importer choice in
		// either direction; otherwise persist the compatibility-safe false.
		disabled := false
		script.Config.HTTP.Traceparent = &disabled
	}
	// Do not round-trip the completed script through encoding/json here.
	// Assertion expected values and source-specific Params intentionally retain
	// their authored Go scalar types; a JSON clone would turn every integer in
	// an interface value into float64 and silently change converter semantics.
	draft := &Draft{
		Script: script,
	}
	pendingRequirements := []pendingRuntimeRequirements{{
		actionPath:  strings.TrimSpace(options.ActionPath),
		requirement: credentialpolicy.CloneRequirements(options.Requirements),
	}}
	for _, finding := range options.Findings {
		if finding.ActionPath == "" {
			finding.ActionPath = strings.TrimSpace(options.ActionPath)
		}
		draft.Findings = append(draft.Findings, finding)
	}
	for _, report := range reports {
		for _, finding := range report.Findings {
			if finding.ActionPath == "" {
				finding.ActionPath = report.ActionPath
			}
			draft.Findings = append(draft.Findings, finding)
		}
		actionPath := strings.TrimSpace(report.ActionPath)
		if actionPath == "" {
			for _, finding := range report.Findings {
				if strings.TrimSpace(finding.ActionPath) != "" {
					actionPath = strings.TrimSpace(finding.ActionPath)
					break
				}
			}
		}
		pendingRequirements = append(pendingRequirements, pendingRuntimeRequirements{
			actionPath:  actionPath,
			requestKey:  report.RequestKey,
			requirement: credentialpolicy.CloneRequirements(report.Requirements),
		})
		draft.SourceMappings = append(draft.SourceMappings, cloneSourceMappings(report.SourceMappings)...)
		if err := mergeAssets(script, report.Assets); err != nil {
			return nil, fmt.Errorf("merge finalized request assets: %w", err)
		}
	}
	beforeRuntimeHeaders, err := captureRuntimeHeaderSurfaces(script)
	if err != nil {
		return nil, fmt.Errorf("capture HTTP runtime fidelity inputs: %w", err)
	}
	runtimeFindings, runtimeMappings, err := ApplyRuntimeFidelityReview(
		script,
		RuntimeFidelityOptions{
			SourceProfile:              script.GeneratedFrom,
			PreserveOmittedHeaderBytes: importPreservesCredentialValues(script),
		},
	)
	if err != nil {
		return nil, fmt.Errorf("review HTTP runtime fidelity: %w", err)
	}
	draft.Findings = append(draft.Findings, runtimeFindings...)
	draft.SourceMappings = append(draft.SourceMappings, runtimeMappings...)
	afterRuntimeHeaders, err := captureRuntimeHeaderSurfaces(script)
	if err != nil {
		return nil, fmt.Errorf("capture HTTP runtime fidelity outputs: %w", err)
	}
	for _, pending := range pendingRequirements {
		draft.Requirements = append(
			draft.Requirements,
			reconcileRuntimeHeaderRequirements(pending, beforeRuntimeHeaders, afterRuntimeHeaders)...,
		)
	}
	result, err := draft.Finalize(FinalizeOptions{UnresolvedBindings: options.UnresolvedBindings})
	if err != nil {
		return nil, err
	}
	markCredentialParameterProvenance(result.Script, result.Requirements)
	if len(result.ValidationIssues) != 0 {
		return nil, &ScriptValidationError{Issues: result.ValidationIssues}
	}
	result.Disposition = dispositionForReadiness(result.Readiness, reports)
	return result, nil
}

func markCredentialParameterProvenance(
	script *dsl.Script,
	requirements []credentialpolicy.Requirement,
) {
	if script == nil || len(requirements) == 0 ||
		script.Metadata == nil || script.Metadata.Import == nil {
		return
	}
	names := make(map[string]string)
	if script.Metadata.Import.Preserved != nil {
		existingValue := script.Metadata.Import.Preserved[dsl.ImportPreservedCredentialParameterNamesKey]
		switch existing := existingValue.(type) {
		case []string:
			for _, name := range existing {
				if normalized := strings.ToLower(strings.TrimSpace(name)); normalized != "" {
					names[normalized] = strings.TrimSpace(name)
				}
			}
		case []any:
			for _, value := range existing {
				name, ok := value.(string)
				if !ok {
					continue
				}
				if normalized := strings.ToLower(strings.TrimSpace(name)); normalized != "" {
					names[normalized] = strings.TrimSpace(name)
				}
			}
		}
	}
	for _, requirement := range requirements {
		for _, name := range requirement.SourceVariableNames {
			name = strings.TrimSpace(name)
			if normalized := strings.ToLower(name); normalized != "" {
				names[normalized] = name
			}
		}
	}
	if len(names) == 0 {
		return
	}
	if script.Metadata.Import.Preserved == nil {
		script.Metadata.Import.Preserved = make(map[string]any)
	}
	ordered := make([]string, 0, len(names))
	for _, name := range names {
		ordered = append(ordered, name)
	}
	sort.Slice(ordered, func(i, j int) bool {
		return strings.ToLower(ordered[i]) < strings.ToLower(ordered[j])
	})
	script.Metadata.Import.Preserved[dsl.ImportPreservedCredentialParameterNamesKey] =
		ordered
}

func markCredentialPreservingImport(script *dsl.Script) {
	if script == nil {
		return
	}
	source := strings.TrimSpace(script.GeneratedFrom)
	if script.Metadata != nil && script.Metadata.Import != nil {
		source = firstNonEmpty(strings.TrimSpace(script.Metadata.Import.Source), source)
	}
	if source == "" {
		return
	}
	if script.Metadata == nil {
		script.Metadata = &dsl.ScriptMetadata{}
	}
	if script.Metadata.Import == nil {
		script.Metadata.Import = &dsl.ImportMetadata{}
	}
	if strings.TrimSpace(script.Metadata.Import.Source) == "" {
		script.Metadata.Import.Source = source
	}
	if script.Metadata.Import.Preserved == nil {
		script.Metadata.Import.Preserved = make(map[string]any)
	}
	script.Metadata.Import.Preserved[dsl.ImportPreservedCredentialPolicyKey] =
		dsl.ImportPreservedCredentialPolicyPreserve
}

func importPreservesCredentialValues(script *dsl.Script) bool {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil {
		return false
	}
	value, _ := script.Metadata.Import.Preserved[dsl.ImportPreservedCredentialPolicyKey].(string)
	return strings.EqualFold(
		strings.TrimSpace(value),
		dsl.ImportPreservedCredentialPolicyPreserve,
	)
}

func projectScriptBaseURLCredentials(
	script *dsl.Script,
	reports []Report,
	options FinalizeOptions,
) (Report, error) {
	if script == nil {
		return Report{Disposition: Rejected}, errors.New("script is required")
	}
	report := Report{
		RequestKey:  strings.TrimSpace(script.GeneratedFrom) + ":script_base_url",
		Disposition: ExecutableExact,
	}
	if !isHTTPFamilyProtocol(script.Protocol) {
		return report, nil
	}
	if strings.HasPrefix(report.RequestKey, ":") {
		report.RequestKey = "import:script_base_url"
	}

	registry := credentialpolicy.NewNameRegistry()
	reserveRequirementNames := func(requirements []credentialpolicy.Requirement) {
		for _, requirement := range requirements {
			registry.Reserve(requirement.SuggestedName)
			if name, ok := credential.ParseReference(requirement.Replacement); ok {
				registry.Reserve(name)
			}
		}
	}
	reserveRequirementNames(options.Requirements)
	for _, existing := range reports {
		reserveRequirementNames(existing.Requirements)
	}
	reserveScriptSecretReferences(script, registry)

	baseSource := SourceRef{
		Format:  firstNonEmpty(strings.TrimSpace(script.GeneratedFrom), "import"),
		Path:    scriptImportSourceName(script),
		Pointer: "/config/base_url",
	}
	rawBaseURL := script.Config.BaseURL
	// General execution readiness accepts legacy script parameter names as
	// providers for {{var.*}} references. Credential provenance is stricter:
	// only a prior extractor/OAuth output proves that a var-shaped sensitive
	// value is response-derived rather than a static imported parameter.
	provenanceScript := *script
	provenanceScript.Parameters = nil
	variableReadiness := AnalyzeVariableReadiness(&provenanceScript, nil)

	// Note that per-action surfaces below DO consult this provenance, but
	// config.base_url never can: buildProtoConfigWithSecretResolver flattens
	// base_url once at protocol init and resolves only {{secret.*}} there, and
	// HTTP.ResolveURL then concatenates the stored string verbatim per request.
	// A preserved {{var.*}} would go on the wire literally, so base URL
	// credentials always govern. Pinned by
	// TestProjectScriptCredentialsGovernsBaseURLRuntimeVariables.
	var baseFindings []importreview.Finding
	if strings.TrimSpace(script.Config.BaseURL) != "" {
		projected, result, err := credentialpolicy.ProjectRawURL(
			script.Config.BaseURL,
			credentialpolicy.Options{
				Mode:        credentialpolicy.Preserve,
				Source:      baseSource,
				RequestKey:  report.RequestKey,
				TargetField: "base_url",
				NamePrefix:  "IMPORT_BASE_URL",
				Registry:    registry,
			},
		)
		if err != nil {
			return Report{RequestKey: report.RequestKey, Disposition: Rejected}, err
		}
		review := ReviewCredentialProjection(result, "HTTP base URL")
		report.Disposition = review.Disposition
		report.Requirements = review.Requirements
		report.preservedTargets = append(
			report.preservedTargets,
			result.PreservedTargets...,
		)
		baseFindings = review.Findings
		if review.Rejected {
			var codes []string
			for _, finding := range result.Findings {
				if finding.Disposition == credentialpolicy.Rejected {
					codes = append(codes, finding.Code)
				}
			}
			return report, &CredentialProjectionError{Codes: uniqueSorted(codes)}
		}
		if baseURLHasUnresolvableRuntimeTemplate(rawBaseURL) {
			baseFindings = append(baseFindings, baseURLRuntimeTemplateFinding(baseSource))
			baseFindings = importreview.NormalizeFindings(baseFindings)
			report.Disposition = worseRequestDisposition(report.Disposition, BlockedDraft)
		}
		script.Config.BaseURL = projected
	}

	// The script-level safety sweep also visits requests that an adapter did
	// not submit through FinalizeAction. Preserve {{var.*}} credential values
	// only when shared execution-order analysis proves the variable is
	// available at that exact request surface. Without carrying this
	// provenance into the second projection pass, a correctly correlated token
	// is mistaken for an authored static credential and rewritten to a secret.
	var actionFindings []importreview.Finding
	err := dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		surfaces := importURLCredentialSurfaces(ref)
		if len(surfaces) == 0 {
			return nil
		}

		for _, surface := range surfaces {
			rawSurface := surface.raw
			projectedFromBase := false
			if inherited, ok := projectURLFromBase(
				rawSurface,
				rawBaseURL,
				script.Config.BaseURL,
			); ok {
				rawSurface = inherited
				projectedFromBase = true
			}
			if surface.usesBaseURL || projectedFromBase {
				for _, finding := range baseFindings {
					finding.ActionPath = ref.Path
					if finding.Location == "" {
						finding.Location = sourceLocation(baseSource)
					}
					actionFindings = append(actionFindings, finding)
				}
			}
			surfaceSource := baseSource
			surfaceSource.Pointer = ref.Path + surface.pointerSuffix
			surfaceRequestKey := firstNonEmpty(strings.TrimSpace(ref.Name()), ref.Path) + surface.requestKeySuffix
			projected, result, projectErr := credentialpolicy.ProjectRawURL(
				rawSurface,
				credentialpolicy.Options{
					Mode:        credentialpolicy.Preserve,
					Source:      surfaceSource,
					RequestKey:  surfaceRequestKey,
					TargetField: surface.targetField,
					NamePrefix:  surface.namePrefix,
					Registry:    registry,
					ProvenVars: provenVariableReferences(
						rawSurface,
						variableReadiness.ByAction[ref.Path],
					),
				},
			)
			if projectErr != nil {
				return projectErr
			}
			review := ReviewCredentialProjection(result, surface.reviewElement)
			if review.Rejected {
				return &CredentialProjectionError{Codes: credentialProjectionRejectedCodes(result)}
			}
			surface.set(projected)
			report.Disposition = worseRequestDisposition(report.Disposition, review.Disposition)
			for _, requirement := range review.Requirements {
				if requirement.ExistingReference {
					continue
				}
				report.Requirements = append(report.Requirements, requirement)
			}
			for _, finding := range review.Findings {
				finding.ActionPath = ref.Path
				if finding.Location == "" {
					finding.Location = sourceLocation(surfaceSource)
				}
				actionFindings = append(actionFindings, finding)
			}
		}
		return nil
	})
	if err != nil {
		return Report{RequestKey: report.RequestKey, Disposition: Rejected}, fmt.Errorf(
			"project script URL credentials: %w",
			err,
		)
	}
	if len(actionFindings) > 0 {
		report.Findings = importreview.NormalizeFindings(actionFindings)
	}
	return report, nil
}

const reasonBaseURLRuntimeTemplateUnavailable = "base_url_runtime_template_unavailable"

var baseURLRuntimeTemplatePattern = regexp.MustCompile(`\{\{\s*([^{}]+?)\s*\}\}`)

func baseURLHasUnresolvableRuntimeTemplate(value string) bool {
	for _, match := range baseURLRuntimeTemplatePattern.FindAllStringSubmatch(value, -1) {
		if len(match) != 2 {
			continue
		}
		expression := strings.ToLower(strings.TrimSpace(match[1]))
		// Protocol initialization resolves project-secret references before the
		// HTTP client receives Config.BaseURL. Every other namespace is
		// action/VUser scoped (or API-override scoped) and would otherwise be
		// sent literally by direct replay, shakeout, or CLI execution.
		if !strings.HasPrefix(expression, "secret.") {
			return true
		}
	}
	return false
}

func baseURLRuntimeTemplateFinding(source SourceRef) importreview.Finding {
	location := sourceLocation(source)
	return importreview.Finding{
		Code:       importReviewRequiredBlocker,
		Severity:   diag.Blocker,
		Element:    "HTTP base URL",
		Detail:     "config.base_url contains a per-VUser template that the HTTP runtime cannot resolve before protocol initialization",
		Location:   location,
		Source:     firstNonEmpty(strings.TrimSpace(source.Format), "import"),
		ReasonCode: reasonBaseURLRuntimeTemplateUnavailable,
		SourceRef:  location,
		Resolution: "replace_with_literal_or_secret_reference_and_remove_gate",
		Gate:       importreview.GateExecution,
	}
}

type importURLCredentialSurface struct {
	raw              string
	set              func(string)
	pointerSuffix    string
	requestKeySuffix string
	targetField      string
	namePrefix       string
	reviewElement    string
	usesBaseURL      bool
}

func importURLCredentialSurfaces(ref dslwalk.SurfaceRef) []importURLCredentialSurface {
	if ref.Action != nil {
		actionType := strings.ToLower(strings.TrimSpace(ref.Action.Type))
		if !runtimeSurfaceIsHTTPRequest(ref) && actionType != "http_oauth2_token" {
			return nil
		}
		action := ref.Action
		var surfaces []importURLCredentialSurface
		rawTarget := action.Target
		tokenURL, hasTokenURL := "", false
		if actionType == "http_oauth2_token" && action.Params != nil {
			tokenURL, hasTokenURL = action.Params["token_url"].(string)
		}
		sameOAuthURL := actionType == "http_oauth2_token" && hasTokenURL &&
			strings.TrimSpace(tokenURL) != "" &&
			strings.TrimSpace(tokenURL) == strings.TrimSpace(rawTarget)

		// params.token_url is the endpoint the runtime actually executes.
		// Project it as the authoritative OAuth URL surface and synchronize an
		// exact Target mirror for editor/import compatibility.
		if actionType == "http_oauth2_token" && hasTokenURL &&
			strings.TrimSpace(tokenURL) != "" {
			surfaces = append(surfaces, importURLCredentialSurface{
				raw: tokenURL,
				set: func(projected string) {
					action.Params["token_url"] = projected
					if sameOAuthURL {
						action.Target = projected
					}
				},
				pointerSuffix:    "/params/token_url",
				requestKeySuffix: ":token_url",
				targetField:      "params.token_url",
				namePrefix:       "IMPORT_OAUTH_TOKEN_URL",
				reviewElement:    "OAuth token URL",
			})
		}

		if strings.TrimSpace(rawTarget) != "" && !sameOAuthURL {
			surfaces = append(surfaces, importURLCredentialSurface{
				raw: rawTarget,
				set: func(projected string) {
					action.Target = projected
				},
				pointerSuffix: "/target",
				targetField:   "url",
				namePrefix:    "IMPORT_URL",
				reviewElement: "HTTP request URL",
				usesBaseURL: actionType != "http_oauth2_token" &&
					!absoluteHTTPActionTarget(rawTarget),
			})
		}
		return surfaces
	}
	if ref.Request == nil || !ref.IsNestedRequest() {
		return nil
	}
	raw, _ := ref.Request["target"].(string)
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	request := ref.Request
	return []importURLCredentialSurface{{
		raw: raw,
		set: func(projected string) {
			request["target"] = projected
		},
		pointerSuffix: "/target",
		targetField:   "url",
		namePrefix:    "IMPORT_URL",
		reviewElement: "HTTP request URL",
		usesBaseURL:   !absoluteHTTPActionTarget(raw),
	}}
}

// projectURLFromBase reuses an already projected base URL only when the whole
// authored base is the same URL occurrence or an exact path-boundary prefix of
// the other surface. It intentionally never replaces a requirement's raw
// value in isolation: short/common credential values can legitimately occur
// in unrelated URL paths and nonsensitive query values.
func projectURLFromBase(raw, authoredBase, projectedBase string) (string, bool) {
	raw = strings.TrimSpace(raw)
	authoredBase = strings.TrimSpace(authoredBase)
	projectedBase = strings.TrimSpace(projectedBase)
	if raw == "" || authoredBase == "" || projectedBase == "" ||
		authoredBase == projectedBase {
		return "", false
	}
	if raw == authoredBase {
		return projectedBase, true
	}
	if strings.ContainsAny(authoredBase, "?#") {
		return "", false
	}
	authoredPrefix := strings.TrimRight(authoredBase, "/")
	projectedPrefix := strings.TrimRight(projectedBase, "/")
	if authoredPrefix == "" || projectedPrefix == "" ||
		!strings.HasPrefix(raw, authoredPrefix) {
		return "", false
	}
	suffix := raw[len(authoredPrefix):]
	if suffix == "" || !strings.ContainsRune("/?#", rune(suffix[0])) {
		return "", false
	}
	return projectedPrefix + suffix, true
}

func credentialProjectionRejectedCodes(result credentialpolicy.Result) []string {
	var codes []string
	for _, finding := range result.Findings {
		if finding.Disposition == credentialpolicy.Rejected {
			codes = append(codes, finding.Code)
		}
	}
	return uniqueSorted(codes)
}

func reserveScriptSecretReferences(script *dsl.Script, registry *credentialpolicy.NameRegistry) {
	if script == nil || registry == nil {
		return
	}
	payload, err := json.Marshal(script)
	if err != nil {
		return
	}
	text := string(payload)
	for cursor := 0; cursor < len(text); {
		start := strings.Index(text[cursor:], "{{")
		if start < 0 {
			return
		}
		start += cursor
		end := strings.Index(text[start+2:], "}}")
		if end < 0 {
			return
		}
		end += start + 4
		if name, ok := credential.ParseReference(text[start:end]); ok {
			registry.Reserve(name)
		}
		cursor = end
	}
}

func scriptImportSourceName(script *dsl.Script) string {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil {
		return ""
	}
	return strings.TrimSpace(script.Metadata.Import.SourceScriptName)
}

// Project clones base and appends every request in order. It never mutates base.
func Project(base *dsl.Script, inputs []RequestInput, options ProjectOptions) (*Draft, error) {
	if base == nil {
		return nil, errors.New("base script is required")
	}
	var err error
	options, err = normalizeProjectOptions(options)
	if err != nil {
		return nil, err
	}
	script, err := cloneScript(base)
	if err != nil {
		return nil, fmt.Errorf("clone base script: %w", err)
	}
	draft := &Draft{Script: script}
	for inputIndex := range inputs {
		input := inputs[inputIndex]
		if script.Protocol == "" && input.Request.Protocol != "" {
			script.Protocol = string(input.Request.Protocol)
		}
		if strings.TrimSpace(script.Config.BaseURL) == "" && strings.TrimSpace(input.Request.BaseURL) != "" {
			script.Config.BaseURL = strings.TrimSpace(input.Request.BaseURL)
		}
		action, findings, assets, runtimeProjection, err := projectRequest(input.Request, input.Overlay, options)
		if err != nil {
			return nil, fmt.Errorf("project request %d: %w", inputIndex, err)
		}
		phase := input.Phase
		if phase == "" {
			phase = PhaseActions
		}
		var actionPath string
		switch phase {
		case PhaseInit:
			actionPath = fmt.Sprintf("init[%d]", len(script.Init))
			script.Init = append(script.Init, action)
		case PhaseActions:
			actionPath = fmt.Sprintf("actions[%d]", len(script.Actions))
			script.Actions = append(script.Actions, action)
		case PhaseTeardown:
			actionPath = fmt.Sprintf("teardown[%d]", len(script.Teardown))
			script.Teardown = append(script.Teardown, action)
		default:
			return nil, fmt.Errorf("unsupported request phase %q", input.Phase)
		}
		for index := range findings {
			findings[index].ActionPath = actionPath
			if findings[index].Location == "" {
				findings[index].Location = sourceLocation(input.Request.Source)
			}
		}
		inputFindings := append([]importreview.Finding(nil), input.Findings...)
		for index := range inputFindings {
			inputFindings[index].ActionPath = actionPath
			if inputFindings[index].Location == "" {
				inputFindings[index].Location = sourceLocation(input.Request.Source)
			}
		}
		actionFindings := append(inputFindings, findings...)
		importreview.ApplyToAction(lastAction(script, phase), actionFindings)
		draft.Findings = append(draft.Findings, actionFindings...)
		draft.Requirements = append(
			draft.Requirements,
			pruneRequirementsForHeaderOmissions(
				input.Requirements,
				input.Request.Key,
				runtimeProjection.HeaderOmissions,
			)...,
		)
		draft.SourceMappings = append(draft.SourceMappings, effectiveSourceMappings(input.Request, actionPath)...)
		draft.SourceMappings = append(draft.SourceMappings, runtimeFidelitySourceMappings(runtimeProjection, actionPath, 0)...)
		if err := mergeAssets(script, assets); err != nil {
			return nil, fmt.Errorf("project request %d assets: %w", inputIndex, err)
		}
	}
	draft.Findings = importreview.NormalizeFindings(draft.Findings)
	return draft, nil
}

// Finalize validates the completed draft and computes common readiness.
func (draft *Draft) Finalize(options FinalizeOptions) (*Result, error) {
	if draft == nil || draft.Script == nil {
		return nil, errors.New("request import draft is required")
	}
	// Some adapters use Project followed by Draft.Finalize directly instead of
	// the FinalizeScript convenience path. Establish the same durable import
	// provenance and runtime credential-redaction contract for both entry points.
	markCredentialPreservingImport(draft.Script)
	if options.UnresolvedBindings < 0 {
		return nil, errors.New("unresolved binding count must be non-negative")
	}
	baseURLReport, err := projectScriptBaseURLCredentials(
		draft.Script,
		[]Report{{Requirements: draft.Requirements}},
		FinalizeOptions{},
	)
	if err != nil {
		return nil, err
	}
	attachScriptPreservedCredentialTargets(draft.Script, baseURLReport.preservedTargets)
	draft.Findings = append(draft.Findings, baseURLReport.Findings...)
	draft.Requirements = append(
		draft.Requirements,
		credentialpolicy.CloneRequirements(baseURLReport.Requirements)...,
	)
	for _, requirement := range baseURLReport.Requirements {
		if requirement.SourceValueAvailable && requirement.Value != "" {
			if options.UnresolvedBindings == int(^uint(0)>>1) {
				return nil, errors.New("unresolved binding count exceeds supported range")
			}
			options.UnresolvedBindings++
		}
	}
	fileFindings, err := prepareMissingFileBindings(draft.Script)
	if err != nil {
		return nil, fmt.Errorf("analyze file bindings: %w", err)
	}
	draft.Findings = append(draft.Findings, fileFindings...)
	variableFindings, err := prepareMissingVariableBindings(draft.Script)
	if err != nil {
		return nil, fmt.Errorf("analyze variable bindings: %w", err)
	}
	draft.Findings = append(draft.Findings, variableFindings...)
	draft.Findings = importreview.NormalizeFindings(draft.Findings)
	if err := importreview.ApplyToScript(draft.Script, draft.Findings); err != nil {
		return nil, err
	}
	issues := dsl.ValidateScriptIssues(draft.Script)
	readiness, err := importreview.EvaluateScript(draft.Script, importreview.EvaluationInput{
		ValidationIssues:   issues,
		Findings:           draft.Findings,
		UnresolvedBindings: options.UnresolvedBindings,
	})
	if err != nil {
		return nil, err
	}
	findings := importreview.NormalizeFindings(draft.Findings)
	return &Result{
		Script:           draft.Script,
		Findings:         findings,
		Diagnostics:      importreview.Diagnostics(findings),
		Requirements:     credentialpolicy.CloneRequirements(draft.Requirements),
		SourceMappings:   cloneSourceMappings(draft.SourceMappings),
		ValidationIssues: append([]string(nil), issues...),
		Readiness:        readiness,
	}, nil
}

func projectRequest(request Request, overlay ActionOverlay, options ProjectOptions) (dsl.Action, []importreview.Finding, []Asset, runtimeFidelityProjection, error) {
	var err error
	request, err = materializeEffectiveRequest(request)
	if err != nil {
		return dsl.Action{}, nil, nil, runtimeFidelityProjection{}, err
	}
	method := strings.ToUpper(strings.TrimSpace(request.Method))
	if method == "" {
		method = "GET"
	}
	if !httpguts.ValidHeaderFieldName(method) {
		return dsl.Action{}, nil, nil, runtimeFidelityProjection{}, fmt.Errorf("invalid HTTP method %q", request.Method)
	}
	runtimeMethod := method
	var findings []importreview.Finding
	if !httpprotocol.MethodExecutable(method) {
		runtimeMethod = "GET"
		findings = append(findings, requestReviewFinding(
			request,
			"unsupported_http_method",
			"HTTP method",
			"the source HTTP method is not supported by the native runtime and was replaced by a non-executable placeholder",
		))
	}
	target := strings.TrimSpace(request.Target)
	if target == "" {
		return dsl.Action{}, nil, nil, runtimeFidelityProjection{}, errors.New("request target is required")
	}
	headers := append(HeaderList(nil), request.Headers...)
	body, bodyEncoding, err := projectBody(request.Body)
	if err != nil {
		return dsl.Action{}, nil, nil, runtimeFidelityProjection{}, err
	}
	if mediaType := strings.TrimSpace(request.Body.MediaType); mediaType != "" && !headers.Has("Content-Type") {
		headers = append(headers, Header{Name: "Content-Type", Value: mediaType, Source: request.Body.Source})
	}
	headers, runtimeProjection, runtimeFindings := projectRuntimeFidelity(request, headers)
	findings = append(findings, runtimeFindings...)
	if err := validateHeaders(headers, request.HTTPVersion); err != nil {
		return dsl.Action{}, nil, nil, runtimeFidelityProjection{}, err
	}
	headerList := make(dsl.HeaderList, 0, len(headers))
	for _, header := range headers {
		headerList = append(headerList, dsl.HeaderEntry{Name: header.Name, Value: header.Value, Enabled: header.Enabled})
	}
	actionType := strings.TrimSpace(overlay.Type)
	if actionType == "" {
		actionType = "request"
	}
	action := dsl.Action{
		Name:           firstNonEmpty(request.Name, request.Key, method+" "+target),
		Type:           actionType,
		Method:         runtimeMethod,
		Target:         target,
		Headers:        headers.Summary(),
		HeaderList:     headerList,
		Body:           body,
		BodyEncoding:   bodyEncoding,
		Params:         cloneAnyMap(overlay.Params),
		Metadata:       mergeMetadata(request.Metadata, overlay.Metadata),
		Extractors:     append(append([]dsl.Extractor(nil), request.Extractors...), overlay.Extractors...),
		ThinkTime:      effectiveThinkTime(request.ThinkTime, overlay.ThinkTime),
		ThinkTimeAfter: request.ThinkTimeAfter || overlay.ThinkTimeAfter,
		SOAP:           effectiveSOAP(request.SOAP, overlay.SOAP),
	}
	applyRuntimeFidelityMetadata(&action, runtimeProjection)
	action.Assertions, findings = projectAssertions(
		append(append([]dsl.Assertion(nil), request.Assertions...), overlay.Assertions...),
		request,
		findings,
	)
	applyRequestMetadata(&action, request, method != runtimeMethod)
	if version := strings.TrimSpace(request.HTTPVersion); version != "" {
		if action.Metadata == nil {
			action.Metadata = make(map[string]any)
		}
		// Preserve source provenance separately from the executable leaf policy.
		action.Metadata["http_version"] = version
		if policy, ok := sourceHTTPVersionPolicy(version); ok {
			action.HTTPVersion, _ = dsl.NewHTTPVersionOverride(policy)
		}
	}
	effectiveRequestAuth := effectiveAuth(request)
	action.Auth, findings = projectAuth(effectiveRequestAuth, request, findings)
	action.TimeoutMs, findings, err = projectTimeout(request.Timeout, options, findings)
	if err != nil {
		return dsl.Action{}, nil, nil, runtimeFidelityProjection{}, err
	}
	action.MaxRedirects, findings, err = projectRedirects(request.Redirects, options, findings)
	if err != nil {
		return dsl.Action{}, nil, nil, runtimeFidelityProjection{}, err
	}
	if len(request.MissingReferences) > 0 {
		names := make([]string, 0, len(request.MissingReferences))
		for index, reference := range request.MissingReferences {
			name := strings.TrimSpace(reference.Name)
			if name == "" {
				return dsl.Action{}, nil, nil, runtimeFidelityProjection{}, fmt.Errorf("missing reference %d has no name", index)
			}
			if namespace := strings.TrimSpace(reference.Namespace); namespace != "" {
				name = namespace + "." + name
			}
			names = append(names, name)
		}
		names = uniqueSorted(names)
		if len(names) > 0 {
			if action.Metadata == nil {
				action.Metadata = make(map[string]any)
			}
			action.Metadata["missing_variables"] = names
			findings = append(findings, importreview.Finding{
				Code:       "import_missing_variables",
				Severity:   diag.Blocker,
				Element:    action.Name,
				Detail:     fmt.Sprintf("%d unresolved source reference(s) must be bound before execution", len(names)),
				Source:     requestSource(request),
				ReasonCode: "missing_variable_binding",
				SourceRef:  sourceLocation(request.Source),
				Resolution: "bind_variables_and_remove_gate",
				Gate:       importreview.GateExecution,
			})
		}
	}
	findings = append(findings, diagnosticFindings(request)...)
	return action, findings, request.Body.Assets, runtimeProjection, nil
}

func projectAuth(auth Auth, request Request, findings []importreview.Finding) (*dsl.AuthConfig, []importreview.Finding) {
	mode := auth.Mode
	switch mode {
	case AuthUnspecified, AuthInherit:
		// Nil is the only action representation that truly inherits script-level
		// auth. The runtime's literal "inherit" alias is an explicit empty
		// override, so it must not be emitted here.
		return nil, findings
	case AuthNone:
		return &dsl.AuthConfig{Type: httpprotocol.AuthTypeNone}, findings
	case AuthBasic, AuthBearer, AuthDigest:
		return &dsl.AuthConfig{
			Type:     string(mode),
			Username: auth.Username,
			Password: auth.Password,
			Token:    auth.Token,
			Domain:   auth.Domain,
		}, findings
	case AuthUnsupported:
		sourceMode := strings.ToLower(strings.TrimSpace(auth.SourceMode))
		if sourceMode == "" {
			sourceMode = "unsupported"
		}
		capability := httpprotocol.LookupAuthCapability(sourceMode)
		finding := requestReviewFinding(
			request,
			"unsupported_http_auth_"+stableCode(sourceMode),
			"HTTP authentication",
			fmt.Sprintf("the source HTTP authentication mode is %s and must be translated before execution", capability.Support),
		)
		findings = append(findings, finding)
		// The durable import-review gate makes an unsupported auth configuration
		// inert before transport. Retain its exact fields in the destination so
		// an operator can translate the scheme (for example NTLM to digest)
		// without re-entering a password already present in the imported source.
		// DSL validation explicitly permits unsupported auth only while that
		// durable gate remains attached.
		return &dsl.AuthConfig{
			Type:     sourceMode,
			Username: auth.Username,
			Password: auth.Password,
			Token:    auth.Token,
			Domain:   auth.Domain,
		}, findings
	default:
		sourceMode := firstNonEmpty(auth.SourceMode, string(mode))
		findings = append(findings, requestReviewFinding(
			request,
			"unsupported_http_auth_"+stableCode(sourceMode),
			"HTTP authentication",
			"the effective HTTP authentication mode is unknown and must be translated before execution",
		))
		return &dsl.AuthConfig{
			Type:     strings.ToLower(strings.TrimSpace(sourceMode)),
			Username: auth.Username,
			Password: auth.Password,
			Token:    auth.Token,
			Domain:   auth.Domain,
		}, findings
	}
}

func effectiveAuth(request Request) Auth {
	if request.AuthConfig == nil {
		return request.Auth
	}
	config := request.AuthConfig
	mode := httpprotocol.NormalizeAuthType(config.Type)
	auth := Auth{
		Mode:       AuthMode(mode),
		SourceMode: mode,
		Username:   config.Username,
		Password:   config.Password,
		Token:      config.Token,
		Domain:     config.Domain,
		Source:     chooseRequestSource(request.AuthSource, request.Source),
	}
	switch mode {
	case "":
		auth.Mode = AuthUnspecified
	case httpprotocol.AuthTypeInherit:
		auth.Mode = AuthInherit
	case httpprotocol.AuthTypeNone:
		auth.Mode = AuthNone
	case httpprotocol.AuthTypeBasic:
		auth.Mode = AuthBasic
	case httpprotocol.AuthTypeBearer:
		auth.Mode = AuthBearer
	case httpprotocol.AuthTypeDigest:
		auth.Mode = AuthDigest
	default:
		auth.Mode = AuthUnsupported
	}
	return auth
}

func projectAssertions(assertions []dsl.Assertion, request Request, findings []importreview.Finding) ([]dsl.Assertion, []importreview.Finding) {
	if len(assertions) == 0 {
		return nil, findings
	}
	maximum := httpprotocol.Capabilities().MaxAssertions
	projected := make([]dsl.Assertion, 0, min(len(assertions), maximum))
	var invalidIndexes []int
	var overflowIndexes []int
	for index, assertion := range assertions {
		if index >= maximum {
			overflowIndexes = append(overflowIndexes, index)
			continue
		}
		if err := httpprotocol.ValidateAssertionDefinition(protocol.Assertion{
			Type:     assertion.Type,
			Expected: assertion.Expected,
			Value:    assertion.Value,
			Min:      assertion.Min,
			Max:      assertion.Max,
		}); err != nil {
			invalidIndexes = append(invalidIndexes, index)
			continue
		}
		projected = append(projected, assertion)
	}
	if len(invalidIndexes) > 0 {
		findings = append(findings, requestReviewFinding(
			request,
			"unsupported_http_assertion",
			"HTTP assertions",
			fmt.Sprintf("%d invalid assertion definition(s) were omitted from the blocked draft", len(invalidIndexes)),
		))
	}
	if len(overflowIndexes) > 0 {
		findings = append(findings, requestReviewFinding(
			request,
			"http_assertion_limit_exceeded",
			"HTTP assertions",
			fmt.Sprintf("%d assertion definition(s) beyond the runtime limit were omitted from the blocked draft", len(overflowIndexes)),
		))
	}
	return projected, findings
}

func effectiveThinkTime(request, overlay dsl.ThinkTimeConfig) dsl.ThinkTimeConfig {
	if !reflect.ValueOf(overlay).IsZero() {
		return overlay
	}
	return request
}

func effectiveSOAP(request, overlay *dsl.SOAPActionConfig) *dsl.SOAPActionConfig {
	if overlay != nil {
		return cloneSOAP(overlay)
	}
	return cloneSOAP(request)
}

func applyRequestMetadata(action *dsl.Action, request Request, methodReplaced bool) {
	if action == nil {
		return
	}
	add := func(key string, value any) {
		if action.Metadata == nil {
			action.Metadata = make(map[string]any)
		}
		action.Metadata[key] = value
	}
	if request.CandidateID != "" {
		add("candidate_id", request.CandidateID)
	}
	if request.SourceProfile != "" {
		add("source_profile", request.SourceProfile)
	}
	if request.Protocol != "" {
		add("request_protocol", string(request.Protocol))
	}
	if request.URLPolicy != "" {
		add("url_encoding_policy", string(request.URLPolicy))
	}
	if methodReplaced {
		add("source_http_method", strings.ToUpper(strings.TrimSpace(request.Method)))
	}
	if location := sourceLocation(request.BaseURLSource); location != "" {
		add("base_url_source_ref", location)
	}
	if len(request.SourceRefs) > 0 {
		refs := make([]string, 0, len(request.SourceRefs))
		for _, source := range request.SourceRefs {
			refs = append(refs, sourceLocation(source))
		}
		if refs = uniqueSorted(refs); len(refs) > 0 {
			add("source_refs", refs)
		}
	}
	if len(request.SourceMappings) > 0 {
		mappings := make([]map[string]string, 0, len(request.SourceMappings))
		for _, mapping := range request.SourceMappings {
			if target := strings.TrimSpace(mapping.Target); target != "" {
				mappings = append(mappings, map[string]string{
					"source_ref": sourceLocation(mapping.Source),
					"target":     target,
				})
			}
		}
		if len(mappings) > 0 {
			add("source_mappings", mappings)
		}
	}
}

func effectiveSourceMappings(request Request, actionPath string) []SourceMapping {
	actionPath = strings.TrimSpace(actionPath)
	if len(request.SourceMappings) > 0 {
		mappings := cloneSourceMappings(request.SourceMappings)
		for index := range mappings {
			if strings.TrimSpace(mappings[index].Target) == "" {
				mappings[index].Target = actionPath
			}
		}
		return mappings
	}
	source := request.Source
	if source.Format == "" {
		source.Format = request.SourceProfile
	}
	if source.ItemID == "" {
		source.ItemID = firstNonEmpty(request.CandidateID, request.Key)
	}
	return []SourceMapping{{
		Source: source,
		Target: firstNonEmpty(actionPath, request.Key, "request"),
	}}
}

func cloneSourceMappings(input []SourceMapping) []SourceMapping {
	return append([]SourceMapping(nil), input...)
}

func cloneAssets(input []Asset) []Asset {
	if len(input) == 0 {
		return nil
	}
	output := append([]Asset(nil), input...)
	for index := range output {
		output[index].Data = append([]byte(nil), input[index].Data...)
	}
	return output
}

func pruneRequirementsForHeaderOmissions(
	input []credentialpolicy.Requirement,
	requestKey string,
	omissions []runtimeHeaderOmission,
) []credentialpolicy.Requirement {
	if len(input) == 0 {
		return nil
	}
	if len(omissions) == 0 {
		return credentialpolicy.CloneRequirements(input)
	}
	omittedIndexes := make(map[int]struct{}, len(omissions))
	omittedIdentities := make(map[string]struct{}, len(omissions))
	for _, omission := range omissions {
		omittedIndexes[omission.SourceIndex] = struct{}{}
		if omission.identity != "" {
			omittedIdentities[omission.identity] = struct{}{}
		}
	}
	output := make([]credentialpolicy.Requirement, 0, len(input))
	for _, requirement := range credentialpolicy.CloneRequirements(input) {
		targets := make([]credentialpolicy.TargetRef, 0, len(requirement.Targets))
		for _, target := range requirement.Targets {
			if target.RequestKey != "" && requestKey != "" && target.RequestKey != requestKey {
				targets = append(targets, target)
				continue
			}
			field := strings.ToLower(strings.TrimSpace(target.Field))
			switch field {
			case "header", "header_list":
				if _, omitted := omittedIndexes[target.Index]; omitted {
					continue
				}
				shift := omittedHeaderIndexesBefore(omittedIndexes, target.Index)
				target.Index -= shift
				target.Occurrence = max(0, target.Occurrence-shift)
			case "headers":
				if _, omitted := omittedIdentities[headerTargetPointerIdentity(target.Pointer)]; omitted {
					continue
				}
			default:
				if strings.HasPrefix(field, "headers.") {
					if _, omitted := omittedIdentities[headerIdentity(strings.TrimPrefix(field, "headers."))]; omitted {
						continue
					}
				}
			}
			targets = append(targets, target)
		}
		if len(targets) == 0 {
			continue
		}
		requirement.Targets = targets
		output = append(output, requirement)
	}
	if len(output) == 0 {
		return nil
	}
	return output
}

func omittedHeaderIndexesBefore(omitted map[int]struct{}, index int) int {
	count := 0
	for omittedIndex := range omitted {
		if omittedIndex < index {
			count++
		}
	}
	return count
}

func headerTargetPointerIdentity(pointer string) string {
	pointer = strings.TrimSpace(pointer)
	if slash := strings.LastIndex(pointer, "/"); slash >= 0 {
		pointer = pointer[slash+1:]
	}
	pointer = strings.ReplaceAll(pointer, "~1", "/")
	pointer = strings.ReplaceAll(pointer, "~0", "~")
	return headerIdentity(pointer)
}

func requestReviewFinding(request Request, reason, element, detail string) importreview.Finding {
	return importreview.Finding{
		Code:       "import_review_required",
		Severity:   diag.Blocker,
		Element:    element,
		Detail:     detail,
		Source:     requestSource(request),
		ReasonCode: reason,
		SourceRef:  sourceLocation(request.Source),
		Resolution: "replace_behavior_and_remove_gate",
		Gate:       importreview.GateExecution,
	}
}

func requestSource(request Request) string {
	return firstNonEmpty(request.SourceProfile, request.Source.Format, "import")
}

func diagnosticFindings(request Request) []importreview.Finding {
	if len(request.Diagnostics) == 0 {
		return nil
	}
	findings := make([]importreview.Finding, 0, len(request.Diagnostics))
	for _, diagnostic := range request.Diagnostics {
		finding := importreview.Finding{
			Code:      "request_import_diagnostic",
			Severity:  diagnostic.Severity,
			Element:   diagnostic.Element,
			Detail:    diagnostic.Detail,
			Location:  diagnostic.Location,
			Source:    requestSource(request),
			SourceRef: firstNonEmpty(diagnostic.Location, sourceLocation(request.Source)),
		}
		if diagnostic.Severity == diag.Blocker {
			finding = requestReviewFinding(
				request,
				"source_diagnostic_blocker",
				diagnostic.Element,
				diagnostic.Detail,
			)
			finding.Location = diagnostic.Location
			finding.SourceRef = firstNonEmpty(diagnostic.Location, sourceLocation(request.Source))
		}
		findings = append(findings, finding)
	}
	return findings
}

func projectTimeout(timeout Timeout, options ProjectOptions, findings []importreview.Finding) (int, []importreview.Finding, error) {
	maximum := httpprotocol.Capabilities().MaxTimeoutMS
	switch timeout.Mode {
	case TimeoutInherit:
		return 0, findings, nil
	case TimeoutBounded:
		if timeout.Milliseconds <= 0 {
			return 0, findings, errors.New("bounded timeout must be positive")
		}
		if timeout.Milliseconds <= maximum {
			return timeout.Milliseconds, findings, nil
		}
		return clampBound("timeout", timeout.Milliseconds, maximum, options, findings)
	case TimeoutUnbounded:
		return clampBound("unbounded timeout", 0, maximum, options, findings)
	default:
		return 0, findings, fmt.Errorf("unsupported timeout mode %q", timeout.Mode)
	}
}

func projectRedirects(redirects Redirects, options ProjectOptions, findings []importreview.Finding) (*int, []importreview.Finding, error) {
	maximum := httpprotocol.Capabilities().MaxRedirects
	switch redirects.Mode {
	case RedirectInherit:
		return nil, findings, nil
	case RedirectDisabled:
		value := 0
		return &value, findings, nil
	case RedirectLimited:
		if redirects.Max < 0 {
			return nil, findings, errors.New("limited redirects must be non-negative")
		}
		if redirects.Max <= maximum {
			value := redirects.Max
			return &value, findings, nil
		}
		value, next, err := clampBound("redirect limit", redirects.Max, maximum, options, findings)
		return &value, next, err
	case RedirectUnlimited:
		value, next, err := clampBound("unlimited redirects", 0, maximum, options, findings)
		return &value, next, err
	default:
		return nil, findings, fmt.Errorf("unsupported redirect mode %q", redirects.Mode)
	}
}

func clampBound(label string, sourceValue, maximum int, options ProjectOptions, findings []importreview.Finding) (int, []importreview.Finding, error) {
	if options.Bounds == BoundsReject {
		return 0, findings, fmt.Errorf("%s exceeds native HTTP maximum %d", label, maximum)
	}
	finding := importreview.Finding{
		Code:     "request_import_" + stableCode(label) + "_clamped",
		Severity: diag.Warning,
		Element:  "HTTP runtime bound",
		Detail:   fmt.Sprintf("source %s was clamped to native HTTP maximum %d", label, maximum),
	}
	if sourceValue > 0 {
		finding.Detail = fmt.Sprintf("source %s %d was clamped to native HTTP maximum %d", label, sourceValue, maximum)
	}
	if options.Bounds == BoundsClampWithReview {
		finding.Gate = importreview.GatePromotion
		finding.AcknowledgementRequired = true
	}
	return maximum, append(findings, finding), nil
}

func projectBody(body Body) (string, string, error) {
	switch body.Kind {
	case BodyNone, BodyRaw, BodyJSON, BodyXML, BodyFormURLEncoded, BodyMultipart, BodyGraphQL, BodyFile:
	default:
		return "", "", fmt.Errorf("unsupported body kind %q", body.Kind)
	}
	if body.Kind == BodyNone &&
		(len(body.Payload) > 0 || len(body.Assets) > 0 || strings.TrimSpace(body.MediaType) != "") {
		return "", "", errors.New("body kind is required when body content, media type, or assets are present")
	}
	switch body.Encoding {
	case BodyEncodingAuto:
		if utf8.Valid(body.Payload) {
			return string(body.Payload), "", nil
		}
		return base64.StdEncoding.EncodeToString(body.Payload), string(BodyEncodingBase64), nil
	case BodyEncodingText:
		if !utf8.Valid(body.Payload) {
			return "", "", errors.New("text body contains invalid UTF-8")
		}
		return string(body.Payload), "", nil
	case BodyEncodingBase64:
		return base64.StdEncoding.EncodeToString(body.Payload), string(BodyEncodingBase64), nil
	default:
		return "", "", fmt.Errorf("unsupported body encoding %q", body.Encoding)
	}
}

func validateHeaders(headers HeaderList, version string) error {
	projected := make(protocol.HeaderList, 0, len(headers))
	for _, header := range headers {
		projected = append(projected, protocol.HeaderEntry{Name: header.Name, Value: header.Value, Enabled: header.Enabled})
	}
	issues := httpprotocol.HeaderListIssuesForVersionWithUnresolvedTemplates(projected, version)
	if len(issues) == 0 {
		return nil
	}
	first := issues[0]
	return fmt.Errorf("header %d failed native HTTP validation (%s)", first.Index, first.Code)
}

func mergeAssets(script *dsl.Script, assets []Asset) error {
	if script == nil || len(assets) == 0 {
		return nil
	}
	existing := make(map[string]dsl.DataSource, len(script.DataSources)+len(assets))
	for _, source := range script.DataSources {
		existing[source.Name] = source
	}
	for _, asset := range assets {
		name := strings.TrimSpace(asset.Name)
		if name == "" {
			return errors.New("body asset name is required")
		}
		source := dsl.DataSource{
			Name:        name,
			Type:        "file",
			FileName:    strings.TrimSpace(asset.FileName),
			ContentType: strings.TrimSpace(asset.ContentType),
			StorageRef:  strings.TrimSpace(asset.StorageRef),
		}
		if len(asset.Data) > 0 {
			if utf8.Valid(asset.Data) {
				source.InlineData = string(asset.Data)
				source.Encoding = "text"
			} else {
				source.InlineData = base64.StdEncoding.EncodeToString(asset.Data)
				source.Encoding = "base64"
			}
		}
		if prior, found := existing[name]; found {
			if unboundAssetCompatible(prior, source) {
				continue
			}
			if !dataSourceEqual(prior, source) {
				return fmt.Errorf("body asset %q conflicts with an existing data source", name)
			}
			continue
		}
		existing[name] = source
		script.DataSources = append(script.DataSources, source)
	}
	return nil
}

func dataSourceEqual(left, right dsl.DataSource) bool {
	return reflect.DeepEqual(left, right)
}

func unboundAssetCompatible(existing, incoming dsl.DataSource) bool {
	if !strings.EqualFold(strings.TrimSpace(existing.Type), "file") ||
		!strings.EqualFold(strings.TrimSpace(incoming.Type), "file") {
		return false
	}
	if strings.TrimSpace(incoming.InlineData) != "" ||
		strings.TrimSpace(incoming.Path) != "" ||
		strings.TrimSpace(incoming.StorageRef) != "" {
		return false
	}
	if incoming.FileName != "" && existing.FileName != incoming.FileName {
		return false
	}
	if incoming.ContentType != "" && existing.ContentType != incoming.ContentType {
		return false
	}
	return true
}

func cloneScript(script *dsl.Script) (*dsl.Script, error) {
	payload, err := json.Marshal(script)
	if err != nil {
		return nil, err
	}
	var cloned dsl.Script
	if err := json.Unmarshal(payload, &cloned); err != nil {
		return nil, err
	}
	return &cloned, nil
}

func lastAction(script *dsl.Script, phase Phase) *dsl.Action {
	switch phase {
	case PhaseInit:
		return &script.Init[len(script.Init)-1]
	case PhaseActions:
		return &script.Actions[len(script.Actions)-1]
	case PhaseTeardown:
		return &script.Teardown[len(script.Teardown)-1]
	default:
		return nil
	}
}

func normalizeProjectOptions(options ProjectOptions) (ProjectOptions, error) {
	if options.Bounds == "" {
		return DefaultProjectOptions(), nil
	}
	switch options.Bounds {
	case BoundsReject, BoundsClampWithReview, BoundsClampWithWarning:
		return options, nil
	default:
		return ProjectOptions{}, fmt.Errorf("unsupported bounds policy %q", options.Bounds)
	}
}

func mergeMetadata(groups ...map[string]any) map[string]any {
	var out map[string]any
	for _, group := range groups {
		for key, value := range group {
			if out == nil {
				out = make(map[string]any)
			}
			out[key] = value
		}
	}
	return out
}

func cloneAnyMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func cloneSOAP(input *dsl.SOAPActionConfig) *dsl.SOAPActionConfig {
	if input == nil {
		return nil
	}
	cloned := *input
	return &cloned
}

func sourceLocation(source SourceRef) string {
	return firstNonEmpty(source.Pointer, source.Path, source.ItemID, source.Format)
}

func dispositionForFindings(findings []importreview.Finding) Disposition {
	disposition := ExecutableExact
	for _, finding := range findings {
		if finding.Gate == importreview.GateExecution || finding.Severity == diag.Blocker {
			return BlockedDraft
		}
		if strings.HasSuffix(strings.TrimSpace(finding.Code), "_clamped") {
			disposition = ExecutableBounded
		}
	}
	return disposition
}

func dispositionForReadiness(readiness importreview.Readiness, reports []Report) Disposition {
	if !readiness.DraftValid {
		return Rejected
	}
	if !readiness.ReplayReady || readiness.State == importreview.Blocked {
		return BlockedDraft
	}
	for _, report := range reports {
		if report.Disposition == ExecutableBounded {
			return ExecutableBounded
		}
	}
	return ExecutableExact
}

func stableCode(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	lastUnderscore := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			builder.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if builder.Len() > 0 && !lastUnderscore {
			builder.WriteByte('_')
			lastUnderscore = true
		}
	}
	code := strings.Trim(builder.String(), "_")
	if code == "" {
		return "unsupported"
	}
	return code
}

func uniqueSorted(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

// BaseURLHasUnresolvableRuntimeTemplate reports whether config.base_url still
// holds a template the HTTP runtime cannot resolve. Exported so a later
// mutation pass can tell whether it has actually cleared the condition that
// raised base_url_runtime_template_unavailable, rather than guessing.
func BaseURLHasUnresolvableRuntimeTemplate(value string) bool {
	return baseURLHasUnresolvableRuntimeTemplate(value)
}

// ClearResolvedBaseURLTemplateGate removes the
// base_url_runtime_template_unavailable review gate once config.base_url holds
// a literal (or a project-secret reference) again.
//
// The gate's own documented resolution is "replace_with_literal_or_secret_
// reference_and_remove_gate", so a pass that performs the replacement must also
// remove the gate — otherwise the operator does the fix and the script still
// refuses to run. Actions gated for any other reason keep their gate.
func ClearResolvedBaseURLTemplateGate(script *dsl.Script) ([]string, error) {
	if script == nil || baseURLHasUnresolvableRuntimeTemplate(script.Config.BaseURL) {
		return nil, nil
	}
	var cleared []string
	err := dslwalk.WalkActions(script, func(ref dslwalk.ActionRef) error {
		if ref.Action == nil || ref.Action.Metadata == nil {
			return nil
		}
		review, _ := ref.Action.Metadata["import_review"].(map[string]any)
		if review == nil {
			return nil
		}
		reasons := dslwalk.MetadataStrings(review, "reason_codes")
		remaining := make([]string, 0, len(reasons))
		for _, reason := range reasons {
			if reason != reasonBaseURLRuntimeTemplateUnavailable {
				remaining = append(remaining, reason)
			}
		}
		if len(remaining) == len(reasons) {
			return nil
		}
		if len(remaining) > 0 {
			// Another reason still justifies the review record and its blocker.
			review["reason_codes"] = remaining
			cleared = append(cleared, ref.Path)
			return nil
		}
		delete(ref.Action.Metadata, "import_review")
		clearExecutionBlockerCode(ref.Action, importReviewRequiredBlocker)
		cleared = append(cleared, ref.Path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(cleared)
	return cleared, nil
}

// clearExecutionBlockerCode drops one code from an action's execution gate,
// leaving any other blocker intact.
func clearExecutionBlockerCode(action *dsl.Action, code string) {
	if action == nil || action.Metadata == nil {
		return
	}
	remaining := make([]string, 0, 2)
	for _, existing := range dslwalk.MetadataStrings(action.Metadata, "execution_blockers") {
		if existing != code {
			remaining = append(remaining, existing)
		}
	}
	if blocker, ok := action.Metadata["execution_blocker"].(string); ok {
		blocker = strings.TrimSpace(blocker)
		if blocker != "" && blocker != code && !containsString(remaining, blocker) {
			remaining = append(remaining, blocker)
		}
	}
	if len(remaining) == 0 {
		delete(action.Metadata, "execution_blocker")
		delete(action.Metadata, "execution_blockers")
		return
	}
	sort.Strings(remaining)
	action.Metadata["execution_blocker"] = remaining[0]
	action.Metadata["execution_blockers"] = remaining
}

// ScriptValidationError is a finalized script that fails DSL validation. For
// a converter it is its own bug; a caller finalizing a script it was given
// (an import commit) reports it as that script's fault.
type ScriptValidationError struct {
	Issues []string
}

func (e *ScriptValidationError) Error() string {
	return "internal request import invariant: generated script failed DSL validation: " + strings.Join(e.Issues, "; ")
}
