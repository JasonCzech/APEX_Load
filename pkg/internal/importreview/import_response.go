package importreview

import (
	"encoding/json"
	"fmt"
	"github.com/jasonczech/apexload/pkg/convert/convertresult"
	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
	"github.com/jasonczech/apexload/pkg/convert/jmeter"
	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/convert/sample"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/scrub"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"
)

type ScriptImportResponseOptions struct {
	// CanonicalResponse is an explicitly negotiated wire format. Legacy clients
	// keep their convenience aliases; canonical clients receive each draft once.
	CanonicalResponse   bool
	Result              any
	Scripts             any
	Script              any
	Warnings            []string
	Stats               any
	Correlations        any
	Assumptions         []string
	UnsupportedElements []string
	ReviewSummary       *jmeter.ReviewSummary
	Diagnostics         diag.Diagnostics
	ReviewSections      []ScriptImportReviewSection
	RepairSuggestions   []ScriptImportRepairSuggestion
	ScenarioDraft       *jmeter.ScenarioDraft
	Summary             any
	// Result, Scripts, Script, Stats, Summary, and Correlations stay untyped
	// because each converter package declares its own shape for them. They
	// become typeable once the converters share a result core.
	DetectedProtocol *enterprisearchive.DetectedProtocol
	FidelityReport   *enterprisearchive.ImportFidelityReport
	ReviewArtifact   *enterprisearchive.ReviewArtifact
	// SourceReview holds three shapes today — sourceview.Review from most
	// converters, enterprisearchive.SourceReview from the archive, and a plain
	// map from the GitHub importer — so it cannot be typed ahead of that core.
	SourceReview      any
	TrueClient2Compat *TrueClient2CompatReport
	ProtocolCompat    *ProtocolCompatReport
	// Accounting is how much of the source converted, in the terms every
	// importer shares. It is omitted when the converter tracks no detected
	// count, rather than reported as an all-zero conversion.
	Accounting       convertresult.Accounting
	Parameterization *paramize.Result
	Preflight        *ReplayPreflight
	// ProtectedRequirements is an in-memory, one-shot adapter handoff. Values
	// are attached to an encrypted source-secret migration session when one is
	// available and are never serialized into the response.
	ProtectedRequirements []credentialpolicy.Requirement
	// CredentialRequirements carries value-free converter-authored descriptors
	// into the standardized SecretBindingReview contract. Unlike generic
	// sentinel discovery, these preserve source-variable and target context even
	// when the converter already emitted a {{secret.*}} reference.
	CredentialRequirements []credentialpolicy.Requirement
	// PreparedCredentialReview is used by handlers that must register a
	// server-owned candidate after credential descriptors have been attached.
	// CredentialReviewPrepared distinguishes an intentionally empty review from
	// the ordinary build-it-here path.
	PreparedCredentialReview []SecretBindingRequirement
	CredentialReviewPrepared bool
	// SkipDraftAttestation is reserved for import endpoints that have already
	// committed the server-owned conversion before building their response.
	// Those responses replace the editable draft with a masked StoredScript, so
	// issuing a second one-shot save capability would only consume capacity.
	SkipDraftAttestation bool
	// DisableDraftExpiration keeps the one-shot, server-scoped save capability
	// valid until it is consumed or explicitly discarded. Enterprise archive
	// imports expose this as an opt-in expiration policy and default it off.
	DisableDraftExpiration bool
	// RecordingSessionID is trusted server-only lineage for a proxy recording
	// preview. It lets subsequent server-owned binding/parameterization
	// mutations retain baseline provenance without accepting a client-supplied
	// recording ID as proof.
	RecordingSessionID string
	// RecordingSnapshotDigest binds lineage to the immutable request/response
	// snapshot used for the preview, so a still-live recording cannot acquire
	// new baseline observations between preview and save.
	RecordingSnapshotDigest string
	// RecordingBaseline holds the option-resolved, persistence-bounded baseline
	// for a proxy recording preview so save attestation persists the exact
	// action-index aligned baseline rather than re-converting with default
	// options.
	RecordingBaseline *RecordingBaseline
	// EffectiveSettings is the resolved conversion policy and authoring option
	// summary for HTTP authoring entry points.
	EffectiveSettings *sample.EffectiveSettings
	// DraftAttestationExpiresAt is populated only by the server-side response
	// builder. It is never accepted from converter or client input.
	DraftAttestationExpiresAt time.Time
	// AttestedProjections holds the disclosure projection each attested draft's
	// fingerprint computed. Projecting a large draft costs as much as the rest
	// of the response, so the response's private projection reuses it.
	AttestedProjections map[*dsl.Script]*dsl.Script
	// TextMemo spans the response's draft attestations, so the disclosure
	// projections they build scrub a repeated value once.
	TextMemo *ImportProjectionTextMemo
}

type ScriptImportReviewSection struct {
	Key      string   `json:"key"`
	Title    string   `json:"title"`
	Severity string   `json:"severity"`
	Summary  string   `json:"summary"`
	Items    []string `json:"items"`
}

type ScriptImportRepairSuggestion struct {
	ID                     string   `json:"id"`
	Category               string   `json:"category"`
	Severity               string   `json:"severity"`
	Title                  string   `json:"title"`
	Description            string   `json:"description"`
	Action                 string   `json:"action"`
	TargetAction           string   `json:"target_action,omitempty"`
	SelectorStabilityScore int      `json:"selector_stability_score,omitempty"`
	SelectorSignals        []string `json:"selector_signals,omitempty"`
}

func ImportResponseValue[T any](value *T) any {
	if value == nil {
		return nil
	}
	return value
}

func NewScriptImportResponseWithProjection(opts ScriptImportResponseOptions) (map[string]any, any) {
	scriptForNativeRefs, _ := opts.Script.(*dsl.Script)
	if review := TrustedNativeSalesforceReview(scriptForNativeRefs); review != nil && review.NativePreview.NativePathDomain == "final_native" {
		opts.Correlations = review.NativePreview.Correlations
	}
	// Imported scripts and collections are operator-owned authoring artifacts.
	// Publish them exactly as converted; redaction belongs only to run/debug
	// evidence and must never rewrite an import draft or its review material.
	exactScripts := ImportResponseRawScriptValue(opts.Scripts)
	exactScript := exactPrimaryImportScript(opts.Scripts, exactScripts, opts.Script)
	scripts := CredentialPreservingImportResponseValueFromExact(opts.Scripts, exactScripts, opts.AttestedProjections)
	// The primary convenience field normally repeats a member of scripts. Its
	// private disclosure projection is read-only, so reuse that member instead
	// of walking and scrubbing the same source-review tree a second time.
	script, reused := PrimaryImportScriptProjection(exactScripts, scripts, exactScript)
	if !reused {
		script = CredentialPreservingImportResponseValueFromExact(opts.Script, exactScript, opts.AttestedProjections)
	}
	if IsNilImportValue(scripts) {
		if !IsNilImportValue(opts.Script) {
			exactScripts = []any{exactScript}
			scripts = []any{script}
		} else {
			exactScripts = []any{}
			scripts = []any{}
		}
	}
	// Credential requirements are optional migration/rebinding affordances for
	// a credential-preserving import draft. Discover them from the disclosure
	// projection while consulting the exact server-owned candidate only to report
	// whether a source value is available for an opaque destination migration.
	projectedScripts := scripts
	// Projecting the script tree is the dominant cost of this builder: it
	// marshals/unmarshals the whole tree and then re-projects every script node
	// through the typed dsl.Script boundary. The parameterization trust pass
	// below needs exactly this projection of `scripts`, so hold on to it instead
	// of letting importParameterizationScripts recompute it. Nothing between
	// here and that call mutates `scripts`, and the projection is only read
	// (never placed in the response), so sharing it is behaviour-preserving.
	parameterizationProjection := exactScripts
	secretBindingRequirements := SecretBindingRequirementsForImportScripts(projectedScripts)
	secretBindingRequirements = AnnotateSecretBindingDestinationValues(
		secretBindingRequirements,
		exactScripts,
		projectedScripts,
	)
	if !HasScriptJSONNode(exactScripts) && !IsNilImportValue(exactScript) {
		exactFallback := []any{exactScript}
		projectedScripts = []any{script}
		parameterizationProjection = exactFallback
		secretBindingRequirements = SecretBindingRequirementsForImportScripts(projectedScripts)
		secretBindingRequirements = AnnotateSecretBindingDestinationValues(
			secretBindingRequirements,
			exactFallback,
			projectedScripts,
		)
	}

	warnings := append([]string(nil), opts.Warnings...)
	if warnings == nil {
		warnings = []string{}
	}
	assumptions := opts.Assumptions
	if assumptions == nil {
		assumptions = []string{}
	}
	unsupported := opts.UnsupportedElements
	if unsupported == nil {
		unsupported = []string{}
	}
	reviewSections := opts.ReviewSections
	if reviewSections == nil {
		reviewSections = []ScriptImportReviewSection{}
	}
	repairSuggestions := opts.RepairSuggestions
	if repairSuggestions == nil {
		repairSuggestions = []ScriptImportRepairSuggestion{}
	}
	correlations := opts.Correlations
	if correlations == nil {
		correlations = []any{}
	}
	stats := opts.Stats
	if stats == nil {
		stats = map[string]any{}
	}
	summary := opts.Summary
	if summary == nil {
		summary = map[string]any{}
	}
	parameterizationScripts := ImportParameterizationScripts(
		ProjectedImportScripts{Value: parameterizationProjection},
	)
	parameterization := FilterTrustedImportParameterization(opts.Parameterization, parameterizationScripts)

	// Collection-wide values are derived from the same disclosure projection the
	// parameterization pass uses, so both panels agree on the tree and the IDs
	// here match what the apply endpoint re-derives. Decoding that projection
	// into typed scripts is the expensive part, so it is done once and shared
	// with the parameterization trust pass above.
	sharedValues := BuildImportSharedValues(parameterizationScripts)

	response := map[string]any{
		"scripts":                     exactScripts,
		"script":                      exactScript,
		"secret_binding_requirements": secretBindingRequirements,
		"warnings":                    warnings,
		"stats":                       stats,
		"correlations":                correlations,
		"assumptions":                 assumptions,
		"diagnostics":                 opts.Diagnostics,
		"unsupported_elements":        unsupported,
		"review_sections":             reviewSections,
		"repair_suggestions":          repairSuggestions,
		"scenario_draft":              ImportResponseValue(opts.ScenarioDraft),
		"summary":                     summary,
		"detected_protocol":           ImportResponseValue(opts.DetectedProtocol),
		"fidelity_report":             ImportResponseValue(opts.FidelityReport),
		"review_artifact":             ImportResponseValue(opts.ReviewArtifact),
		"source_review":               opts.SourceReview,
		"trueclient2_compat_report":   ImportResponseValue(opts.TrueClient2Compat),
		"protocol_compat_report":      ImportResponseValue(opts.ProtocolCompat),
		"parameterization":            parameterization,
		"shared_values":               sharedValues,
		"preflight":                   ImportResponseValue(opts.Preflight),
	}

	effectiveSettings := opts.EffectiveSettings
	if effectiveSettings == nil && scriptForNativeRefs != nil {
		if resolved, ok := sample.ReadEffectiveSettings(scriptForNativeRefs); ok {
			effectiveSettings = resolved
		}
	}
	if opts.ReviewSummary != nil {
		response["review_summary"] = opts.ReviewSummary
	}
	if effectiveSettings != nil {
		response["effective_settings"] = effectiveSettings
	}
	if !opts.Accounting.Empty() {
		response["accounting"] = opts.Accounting
	}
	if opts.CanonicalResponse {
		canonicalImportResponse(response, opts)
	} else if opts.Result != nil {
		response["result"] = ImportResponseReviewValue(opts.Result)
	}

	return response, projectedScripts
}

func PrimaryImportScriptProjection(exactScripts, projectedScripts, Primary any) (any, bool) {
	if IsNilImportValue(Primary) {
		return nil, true
	}
	exact, exactOK := exactScripts.([]any)
	projected, projectedOK := projectedScripts.([]any)
	if !exactOK || !projectedOK || len(exact) != len(projected) {
		return nil, false
	}
	for index, candidate := range exact {
		if reflect.DeepEqual(candidate, Primary) {
			return projected[index], true
		}
	}
	return nil, false
}

func ImportResponseReviewValue(value any) any {
	exact := ImportResponseRawScriptValue(value)
	StripImportResponseCapabilities(exact)
	return exact
}

func StripImportResponseCapabilities(value any) {
	switch typed := value.(type) {
	case map[string]any:
		delete(typed, ImportDraftAttestationMetadataKey)
		delete(typed, ProtectedImportCredentialMetadataKey)
		for _, child := range typed {
			StripImportResponseCapabilities(child)
		}
	case []any:
		for _, child := range typed {
			StripImportResponseCapabilities(child)
		}
	}
}

func CopyImportDraftCapabilities(raw, projected any) {
	switch rawValue := raw.(type) {
	case map[string]any:
		projectedValue, _ := projected.(map[string]any)
		if projectedValue == nil {
			return
		}
		if IsScriptJSONObject(rawValue) {
			CopyImportDraftCapabilityFromScriptMap(rawValue, projectedValue)
			return
		}
		for key, child := range rawValue {
			CopyImportDraftCapabilities(child, projectedValue[key])
		}
	case []any:
		projectedValue, _ := projected.([]any)
		for index, child := range rawValue {
			if index < len(projectedValue) {
				CopyImportDraftCapabilities(child, projectedValue[index])
			}
		}
	}
}

func CopyImportDraftCapabilityFromScriptMap(raw, projected map[string]any) {
	rawMetadata, _ := raw["metadata"].(map[string]any)
	rawImport, _ := rawMetadata["import"].(map[string]any)
	rawPreserved, _ := rawImport["preserved"].(map[string]any)
	token, _ := rawPreserved[ImportDraftAttestationMetadataKey].(string)
	rawProtectedDescriptors := rawPreserved[ProtectedImportCredentialMetadataKey]
	if strings.TrimSpace(token) == "" && rawProtectedDescriptors == nil {
		return
	}
	metadata, _ := projected["metadata"].(map[string]any)
	if metadata == nil {
		metadata = make(map[string]any)
		projected["metadata"] = metadata
	}
	importMetadata, _ := metadata["import"].(map[string]any)
	if importMetadata == nil {
		importMetadata = make(map[string]any)
		metadata["import"] = importMetadata
	}
	preserved, _ := importMetadata["preserved"].(map[string]any)
	if preserved == nil {
		preserved = make(map[string]any)
		importMetadata["preserved"] = preserved
	}
	if strings.TrimSpace(token) != "" {
		preserved[ImportDraftAttestationMetadataKey] = token
	}
	// Header names such as X-API-Key are intentionally classified as sensitive
	// by the general script disclosure projection. A protected descriptor's
	// location, however, is a JSON pointer rather than a credential value. Keep
	// the projected labels/context fail-closed while restoring only the validated
	// structural locations so a browser can return the draft to the binding API
	// without turning those pointers into stale `/[REDACTED]` paths.
	CopyProtectedImportCredentialLocations(
		rawProtectedDescriptors,
		preserved[ProtectedImportCredentialMetadataKey],
	)
}

func CopyProtectedImportCredentialLocations(raw, projected any) {
	rawDescriptors, ok := raw.([]any)
	if !ok {
		return
	}
	projectedDescriptors, ok := projected.([]any)
	if !ok {
		return
	}
	rawByID := make(map[string][]any, len(rawDescriptors))
	for _, rawDescriptor := range rawDescriptors {
		descriptor, ok := rawDescriptor.(map[string]any)
		if !ok {
			continue
		}
		id, _ := descriptor["id"].(string)
		locations, ok := descriptor["locations"].([]any)
		if strings.TrimSpace(id) == "" || !ok || !ValidProtectedImportCredentialLocationValues(locations) {
			continue
		}
		rawByID[id] = locations
	}
	for _, projectedDescriptor := range projectedDescriptors {
		descriptor, ok := projectedDescriptor.(map[string]any)
		if !ok {
			continue
		}
		id, _ := descriptor["id"].(string)
		if locations, exists := rawByID[id]; exists {
			// A copy: the projection must not share a tree with raw, which the
			// response may publish.
			descriptor["locations"] = ImportResponseRawScriptValue(locations)
		}
	}
}

func ValidProtectedImportCredentialLocationValues(locations []any) bool {
	if len(locations) == 0 {
		return false
	}
	for _, value := range locations {
		location, ok := value.(map[string]any)
		if !ok {
			return false
		}
		path, _ := location["path"].(string)
		encoding, _ := location["encoding"].(string)
		occurrence, _ := location["occurrence"].(float64)
		if !strings.HasPrefix(path, "/") || occurrence < 0 || occurrence != float64(int(occurrence)) ||
			(encoding != "" && encoding != "base64") {
			return false
		}
	}
	return true
}

func CredentialPreservingImportResponseValueFromExact(value, exact any, attested map[*dsl.Script]*dsl.Script) any {
	// Build a private disclosure projection for migration-requirement discovery.
	// This value is never used as the imported script returned to the operator.
	projected := MaskScriptResponseValueReusing(value, exact, attested)
	MarkCredentialPreservingImportJSON(projected)
	// The projection strips capabilities by design. Copy back only validated
	// opaque capability data needed by the binding flow.
	CopyImportDraftCapabilities(exact, projected)
	return projected
}

func MaskScriptResponseValueReusing(value, exact any, attested map[*dsl.Script]*dsl.Script) any {
	if len(attested) == 0 {
		return MaskScriptResponseValue(exact)
	}
	reuse := func(script *dsl.Script, exact any) any {
		object, isObject := exact.(map[string]any)
		if projection := attested[script]; script != nil && projection != nil && isObject && IsScriptJSONObject(object) {
			if projected, err := ScriptAsJSONValue(projection); err == nil {
				return projected
			}
		}
		return MaskScriptResponseValue(exact)
	}
	switch typed := value.(type) {
	case *dsl.Script:
		return reuse(typed, exact)
	case []*dsl.Script:
		members, ok := exact.([]any)
		if !ok || len(members) != len(typed) {
			return MaskScriptResponseValue(exact)
		}
		projected := make([]any, len(members))
		for index, member := range members {
			projected[index] = reuse(typed[index], member)
		}
		return projected
	}
	return MaskScriptResponseValue(exact)
}

func MarkCredentialPreservingImportJSON(value any) {
	switch typed := value.(type) {
	case []any:
		for _, child := range typed {
			MarkCredentialPreservingImportJSON(child)
		}
	case map[string]any:
		if IsScriptJSONObject(typed) {
			metadata, _ := typed["metadata"].(map[string]any)
			if metadata == nil {
				return
			}
			importMetadata, _ := metadata["import"].(map[string]any)
			if importMetadata == nil {
				return
			}
			source, _ := importMetadata["source"].(string)
			if strings.TrimSpace(source) == "" {
				return
			}
			preserved, _ := importMetadata["preserved"].(map[string]any)
			if preserved == nil {
				preserved = make(map[string]any)
				importMetadata["preserved"] = preserved
			}
			preserved[dsl.ImportPreservedCredentialPolicyKey] =
				dsl.ImportPreservedCredentialPolicyPreserve
			return
		}
		for _, child := range typed {
			MarkCredentialPreservingImportJSON(child)
		}
	}
}

func SanitizeImportReviewEvidence(value any) any {
	return SanitizeImportReviewEvidenceWithMemo(value, &ImportProjectionTextMemo{})
}

func SanitizeImportReviewEvidenceWithMemo(value any, memo *ImportProjectionTextMemo) any {
	if value == nil {
		return nil
	}
	normalized, ok := NormalizeImportSanitizationValue(value)
	if !ok {
		return nil
	}
	return SanitizeImportReviewNodeWithMemo(normalized, "", false, true, memo)
}

func SanitizeImportReviewNodeWithMemo(value any, key string, inheritedSensitive bool, reviewEvidence bool, memo *ImportProjectionTextMemo) any {
	switch typed := value.(type) {
	case map[string]any:
		if IsScriptJSONObject(typed) {
			// Script-shaped review payloads (for example Salesforce native
			// previews) have already reached the executable-script projection
			// boundary. Applying semantic review redaction again would replace
			// structured fields such as config.http.auth with a scalar. Retain
			// the typed boundary here so encoded request bodies are decoded and
			// projected as wire bytes rather than scrubbed as opaque base64 text.
			return MaskScriptJSONObject(typed)
		}
		semanticSensitive := reviewEvidence && (inheritedSensitive || ReviewMapHasSensitiveSemanticName(typed))
		out := make(map[string]any, len(typed))
		for childKey, child := range typed {
			childReviewEvidence := reviewEvidence || IsReviewEvidenceSubtreeKey(childKey)
			if childReviewEvidence && IsReviewSemanticNameKey(childKey) {
				out[childKey] = SanitizeImportReviewNodeWithMemo(child, childKey, false, true, memo)
				continue
			}
			if childReviewEvidence && IsSensitiveScriptKey(childKey) && !IsSecretReferenceKey(childKey) {
				out[childKey] = RedactImportReviewValue(child)
				continue
			}
			childSensitive := childReviewEvidence && semanticSensitive && (IsReviewPayloadKey(childKey) || IsReviewPayloadContainerKey(childKey))
			out[childKey] = SanitizeImportReviewNodeWithMemo(child, childKey, childSensitive, childReviewEvidence, memo)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, child := range typed {
			out[index] = SanitizeImportReviewNodeWithMemo(child, key, inheritedSensitive, reviewEvidence, memo)
		}
		return out
	case string:
		if IsImportDisclosureURLKey(key) {
			return MaskScriptURL(typed)
		}
		if !reviewEvidence {
			return typed
		}
		if inheritedSensitive || (IsSensitiveScriptKey(key) && !IsSecretReferenceKey(key)) {
			if IsPortableTemplateBinding(typed) {
				return typed
			}
			return ProjectSensitiveScriptValue(typed)
		}
		return memo.ScrubReviewText(typed, IsReviewFreeTextKey(key))
	case nil:
		return nil
	default:
		if inheritedSensitive || (IsSensitiveScriptKey(key) && !IsSecretReferenceKey(key)) {
			return MaskedScriptSecret
		}
		return value
	}
}

func IsImportDisclosureURLKey(key string) bool {
	normalized := NormalizeScriptFieldName(key)
	if normalized == "" {
		return false
	}
	if strings.Contains(normalized, "url") {
		return true
	}
	for _, suffix := range []string{"server", "servers", "endpoint", "endpoints", "origin", "origins", "target", "targets"} {
		if strings.HasSuffix(normalized, suffix) {
			return true
		}
	}
	switch normalized {
	case "server", "servers", "endpoint", "endpoints", "origin", "origins", "target", "targets":
		return true
	default:
		return false
	}
}

func ReviewMapHasSensitiveSemanticName(value map[string]any) bool {
	for key, child := range value {
		if !IsReviewSemanticNameKey(key) {
			continue
		}
		name, ok := child.(string)
		if ok && (IsSensitiveScriptKey(name) || scrub.SensitiveName(name) || (scrub.Policy{}).SensitiveHeader(name)) {
			return true
		}
	}
	return false
}

func IsReviewSemanticNameKey(key string) bool {
	switch NormalizeScriptFieldName(key) {
	case "key", "name", "field", "fieldname", "label", "header", "headername", "attributename", "property":
		return true
	default:
		return false
	}
}

func IsReviewPayloadKey(key string) bool {
	switch NormalizeScriptFieldName(key) {
	case "value", "text", "content", "selector", "xpath", "regex", "sourcesnippet", "snippet", "raw", "source", "arguments":
		return true
	default:
		return false
	}
}

func IsReviewPayloadContainerKey(key string) bool {
	switch NormalizeScriptFieldName(key) {
	case "attributes", "selectorcandidates", "objectdescriptor", "objectdescriptors", "steps", "unsupportedfragments":
		return true
	default:
		return false
	}
}

func IsReviewFreeTextKey(key string) bool {
	switch NormalizeScriptFieldName(key) {
	case "content", "sourcesnippet", "snippet", "raw", "source", "arguments", "summary", "description",
		"warning", "warnings", "assumption", "assumptions", "unsupported", "unsupportedelement",
		"unsupportedelements", "diagnostic", "diagnostics", "issue", "issues", "note", "notes":
		return true
	default:
		return false
	}
}

func IsReviewEvidenceSubtreeKey(key string) bool {
	normalized := NormalizeScriptFieldName(key)
	if normalized == "sourcereview" || normalized == "reviewartifact" || normalized == "trueclientarchiveinspection" ||
		normalized == "archiveinspection" || normalized == "sourcesnippet" {
		return true
	}
	switch normalized {
	case "warning", "warnings", "assumption", "assumptions", "unsupported", "unsupportedelement",
		"unsupportedelements", "summary", "diagnostic", "diagnostics", "issue", "issues":
		return true
	}
	return strings.HasSuffix(normalized, "review") || strings.HasSuffix(normalized, "inspection") || strings.HasSuffix(normalized, "reviewartifact")
}

func RedactImportReviewValue(value any) any {
	if Text, ok := value.(string); ok {
		if IsPortableTemplateBinding(Text) {
			return Text
		}
		return ProjectSensitiveScriptValue(Text)
	}
	return MaskedScriptSecret
}

func NormalizeImportSanitizationValue(value any) (any, bool) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	var normalized any
	if err := json.Unmarshal(payload, &normalized); err != nil {
		return nil, false
	}
	return normalized, true
}

func MergeImportCredentialRequirementSets(
	authoritative []credentialpolicy.Requirement,
	supplemental []credentialpolicy.Requirement,
) []credentialpolicy.Requirement {
	merged := credentialpolicy.CloneRequirements(authoritative)
	seen := make(map[string]struct{}, len(authoritative)+len(supplemental))
	for _, requirement := range authoritative {
		if id := strings.TrimSpace(requirement.ID); id != "" {
			seen[id] = struct{}{}
		}
	}
	for _, requirement := range credentialpolicy.CloneRequirements(supplemental) {
		id := strings.TrimSpace(requirement.ID)
		if id != "" {
			if _, duplicate := seen[id]; duplicate {
				continue
			}
			seen[id] = struct{}{}
		}
		merged = append(merged, requirement)
	}
	return merged
}

func MergeReferencedImportCredentialRequirements(
	scripts []*dsl.Script,
	converter []credentialpolicy.Requirement,
) []credentialpolicy.Requirement {
	merged := credentialpolicy.CloneRequirements(converter)
	type scriptCredentialIndex struct {
		raw               any
		names             []string
		sourcePaths       []string
		requestIdentities []string
	}
	indexed := make([]scriptCredentialIndex, len(scripts))
	sourcePaths := make([][]string, len(scripts))
	requestIdentities := make([][]string, len(scripts))
	AllScripts := make([]int, len(scripts))
	for scriptIndex, script := range scripts {
		AllScripts[scriptIndex] = scriptIndex
		if script == nil {
			continue
		}
		indexed[scriptIndex].names, _ = engine.ReferencedSecretNames(script)
		indexed[scriptIndex].raw, _ = ScriptAsJSONValue(script)
		indexed[scriptIndex].sourcePaths = ImportCredentialScriptSourcePaths(script)
		indexed[scriptIndex].requestIdentities = ImportCredentialScriptRequestIdentities(script)
		sourcePaths[scriptIndex] = indexed[scriptIndex].sourcePaths
		requestIdentities[scriptIndex] = indexed[scriptIndex].requestIdentities
	}
	covered := make(map[string]struct{})
	scopedRequirements := make([]credentialpolicy.Requirement, 0, len(merged))
	for _, requirement := range merged {
		if requirement.ExistingReference {
			// Authored references are configuration needs, not evidence that a
			// converter-authored requirement will be attached to this script.
			scopedRequirements = append(scopedRequirements, requirement)
			continue
		}
		replacement := strings.TrimSpace(requirement.Replacement)
		if replacement == "" {
			scopedRequirements = append(scopedRequirements, requirement)
			continue
		}
		candidates := NarrowImportCredentialScriptMatches(
			requirement, AllScripts, sourcePaths, requestIdentities,
		)
		matches := make([]int, 0, len(candidates))
		for _, scriptIndex := range candidates {
			if scriptIndex < 0 || scriptIndex >= len(indexed) || indexed[scriptIndex].raw == nil ||
				len(FindProtectedImportCredentialLocations(indexed[scriptIndex].raw, replacement)) == 0 {
				continue
			}
			matches = append(matches, scriptIndex)
		}
		// A value-free requirement may intentionally describe one shared project
		// secret used by several generated scripts. Scope it into stable per-script
		// descriptors so applying one batch cannot attach the same unscoped ID to
		// every unrelated draft that happens to use the same logical name.
		if len(matches) > 1 && strings.TrimSpace(requirement.Value) == "" && !requirement.SourceValueAvailable {
			for _, scriptIndex := range matches {
				scoped := ScopeValueFreeImportCredentialRequirement(
					requirement, scripts[scriptIndex], scriptIndex, sourcePaths,
				)
				scopedRequirements = append(scopedRequirements, scoped)
				for _, name := range indexed[scriptIndex].names {
					reference, refErr := credential.Reference(name)
					if refErr == nil && strings.Contains(replacement, reference) {
						covered[fmt.Sprintf("%d\x00%s", scriptIndex, name)] = struct{}{}
					}
				}
			}
			continue
		}
		scopedRequirements = append(scopedRequirements, requirement)
		if len(matches) == 1 {
			scriptIndex := matches[0]
			for _, name := range indexed[scriptIndex].names {
				reference, refErr := credential.Reference(name)
				if refErr == nil && strings.Contains(replacement, reference) {
					covered[fmt.Sprintf("%d\x00%s", scriptIndex, name)] = struct{}{}
				}
			}
		}
	}
	merged = scopedRequirements
	for scriptIndex, script := range scripts {
		if script == nil {
			continue
		}
		names := indexed[scriptIndex].names
		rawScript := indexed[scriptIndex].raw
		sourceProfile := "import"
		if script.Metadata != nil && script.Metadata.Import != nil {
			sourceProfile = FirstNonEmpty(strings.TrimSpace(script.Metadata.Import.Source), sourceProfile)
		}
		sourcePath := ""
		if paths := ImportCredentialScriptSourcePaths(script); len(paths) > 0 {
			sourcePath = paths[0]
		}
		// Labels name requests from the disclosure copy, so a label never shows
		// more of a target than the reviewed draft does.
		var maskedRaw any
		maskedLoaded := false
		for _, name := range names {
			key := fmt.Sprintf("%d\x00%s", scriptIndex, name)
			if _, exists := covered[key]; exists {
				continue
			}
			reference, err := credential.Reference(name)
			if err != nil {
				continue
			}
			locations := FindProtectedImportCredentialLocations(rawScript, reference)
			if len(locations) == 0 {
				// Some runtime fields carry a bare logical name rather than a
				// portable template. Their converter must provide a typed descriptor;
				// do not invent a location that the binding endpoint cannot apply.
				continue
			}
			source := credentialpolicy.SourceRef{Format: sourceProfile, Path: sourcePath}
			targets := []credentialpolicy.TargetRef{{RequestKey: script.Name, Field: "project_secret_reference"}}
			if !maskedLoaded {
				maskedLoaded = true
				if masked := MaskScriptSecrets(script); masked != nil {
					maskedRaw, _ = ScriptAsJSONValue(masked)
				}
			}
			label, purpose := GeneratedImportSecretPresentation(
				name, sourceProfile, ImportSecretReferenceLocation(maskedRaw, locations),
			)
			merged = append(merged, credentialpolicy.Requirement{
				ID:                credentialpolicy.StableID(source, targets, fmt.Sprintf("%s:%d", name, scriptIndex)),
				Key:               name,
				Label:             label,
				Context:           purpose,
				SuggestedName:     name,
				Replacement:       reference,
				Source:            source,
				Targets:           targets,
				SourceValueStatus: credentialpolicy.SourceCredentialValueMissing,
			})
		}
	}
	return merged
}

func ScopeValueFreeImportCredentialRequirement(
	requirement credentialpolicy.Requirement,
	script *dsl.Script,
	scriptIndex int,
	allSourcePaths [][]string,
) credentialpolicy.Requirement {
	scoped := credentialpolicy.CloneRequirements([]credentialpolicy.Requirement{requirement})[0]
	scriptName := ""
	if script != nil {
		scriptName = strings.TrimSpace(script.Name)
	}
	field := "project_secret_reference"
	if len(requirement.Targets) > 0 && strings.TrimSpace(requirement.Targets[0].Field) != "" {
		field = strings.TrimSpace(requirement.Targets[0].Field)
	}
	// Source path wins before request identity in the shared matcher. Retain it
	// only when it uniquely identifies this script; otherwise the script name is
	// the unambiguous target discriminator.
	path := NormalizeImportCredentialSourcePath(requirement.Source.Path)
	if path != "" {
		matches := 0
		for _, paths := range allSourcePaths {
			for _, candidate := range paths {
				if candidate == path {
					matches++
					break
				}
			}
		}
		if matches != 1 {
			scoped.Source.Path = ""
		}
	}
	scoped.Targets = []credentialpolicy.TargetRef{{RequestKey: scriptName, Field: field}}
	scoped.ID = credentialpolicy.StableID(
		scoped.Source,
		scoped.Targets,
		fmt.Sprintf("%s:script:%d", FirstNonEmpty(scoped.Key, scoped.SuggestedName), scriptIndex),
	)
	return scoped
}

func ImportSecretReferenceLocation(raw any, locations []ProtectedImportCredentialLocation) string {
	first := ""
	requests := make(map[string]struct{})
	base := ""
	if script, ok := raw.(map[string]any); ok {
		if config, ok := script["config"].(map[string]any); ok {
			base, _ = config["base_url"].(string)
		}
	}
	for _, location := range locations {
		request, field := ImportCredentialLocationRequest(raw, location.Path, base)
		if request == "" {
			continue
		}
		if first == "" {
			first = field + " of " + request
		}
		requests[request] = struct{}{}
	}
	if first == "" {
		return ""
	}
	switch others := len(requests) - 1; others {
	case 0:
		return first
	case 1:
		return first + " and 1 more request"
	default:
		return fmt.Sprintf("%s and %d more requests", first, others)
	}
}

func ImportCredentialLocationRequest(raw any, path, base string) (string, string) {
	tokens, err := SplitJSONPointer(path)
	if err != nil || len(tokens) == 0 {
		return "", ""
	}
	var action map[string]any
	fieldTokens := tokens
	node := raw
	for index, token := range tokens {
		switch typed := node.(type) {
		case map[string]any:
			if _, hasTarget := typed["target"].(string); hasTarget {
				action, fieldTokens = typed, tokens[index:]
			}
			node = typed[token]
		case []any:
			position, convErr := strconv.Atoi(token)
			if convErr != nil || position < 0 || position >= len(typed) {
				return "", ""
			}
			node = typed[position]
		default:
			return "", ""
		}
	}
	// Only an HTTP request carries a method; a token-acquisition or other
	// step with a target is not named as one.
	method, _ := action["method"].(string)
	method = strings.ToUpper(strings.TrimSpace(method))
	if action == nil || method == "" {
		return "", ""
	}
	target, _ := action["target"].(string)
	field := HumanizeSecretLabel(fieldTokens[len(fieldTokens)-1])
	switch {
	case len(fieldTokens) >= 2 && strings.EqualFold(fieldTokens[len(fieldTokens)-2], "headers"):
		field = fieldTokens[len(fieldTokens)-1] + " header"
	case len(fieldTokens) >= 2 && strings.EqualFold(fieldTokens[0], "header_list"):
		field = "Header"
		if entries, ok := action["header_list"].([]any); ok {
			if position, convErr := strconv.Atoi(fieldTokens[1]); convErr == nil && position >= 0 && position < len(entries) {
				if entry, ok := entries[position].(map[string]any); ok {
					if name, _ := entry["name"].(string); strings.TrimSpace(name) != "" {
						field = strings.TrimSpace(name) + " header"
					}
				}
			}
		}
	case strings.EqualFold(fieldTokens[0], "body") || strings.EqualFold(fieldTokens[0], "body_multipart"):
		field = "Body"
	case strings.EqualFold(fieldTokens[0], "target"):
		field = "URL"
	}
	return method + " " + RequestHostPath(target, base), field
}

func RequestHostPath(target, base string) string {
	target = strings.TrimSpace(target)
	if end := strings.IndexAny(target, "?#"); end >= 0 {
		target = target[:end]
	}
	if strings.Contains(target, "{{") {
		return target
	}
	resolved, err := url.Parse(target)
	if err == nil && resolved.Host == "" && strings.TrimSpace(base) != "" &&
		!strings.Contains(base, "{{") {
		if baseURL, baseErr := url.Parse(strings.TrimSpace(base)); baseErr == nil && baseURL.Host != "" {
			resolved = baseURL.ResolveReference(resolved)
		}
	}
	if err != nil || resolved.Host == "" {
		return target
	}
	return resolved.Host + resolved.Path
}

func GeneratedImportSecretPresentation(name, sourceProfile, where string) (string, string) {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "SALESFORCE_CLIENT_SECRET":
		return "Salesforce client secret", "The Agentforce replay transform generated an OAuth token-acquisition step that uses project secret SALESFORCE_CLIENT_SECRET. Configure the Salesforce connected-app client secret before live replay."
	default:
		label := HumanizeSecretLabel(name) + " project secret"
		if where != "" {
			label = where
		}
		return label, fmt.Sprintf("The imported %s draft references project secret %s. Configure it before live replay.", sourceProfile, name)
	}
}

func FilterTrustedImportParameterization(input *paramize.Result, scriptValues ...any) *paramize.Result {
	if input == nil {
		return &paramize.Result{Suggestions: []paramize.Suggestion{}}
	}
	trustedIDs, trustedScopedIDs, hasScriptContext := TrustedParameterizationIDs(scriptValues...)
	output := *input
	output.Suggestions = make([]paramize.Suggestion, 0, len(input.Suggestions))
	for _, suggestion := range input.Suggestions {
		if !suggestion.PreApplied && hasScriptContext {
			trusted := trustedIDs[suggestion.ID]
			if suggestion.ScriptName != "" {
				trusted = trustedScopedIDs[suggestion.ScriptName][suggestion.ID]
			}
			if !trusted {
				continue
			}
		}
		cloned := suggestion
		cloned.Locations = CloneParameterizationLocations(suggestion.Locations)
		if suggestion.FoundInAction != nil {
			found := *suggestion.FoundInAction
			cloned.FoundInAction = &found
		}

		for index := range cloned.Locations {
			if len(cloned.Locations[index].AuraRoute) > 0 {
				cloned.Locations[index].SearchText = ""
				cloned.LiteralValue = ""
				cloned.SampleValue = ""
				cloned.Parameter = nil
				cloned.Rationale = SafeParameterizationRationale(cloned)
			}
		}
		output.Suggestions = append(output.Suggestions, cloned)
	}
	output.Summary = ParamizeSummary(output.Suggestions)
	return &output
}

func TrustedParameterizationIDs(scriptValues ...any) (map[string]bool, map[string]map[string]bool, bool) {
	ids := map[string]bool{}
	scoped := map[string]map[string]bool{}
	scripts := ImportParameterizationScripts(scriptValues...)
	for _, script := range scripts {
		trusted := BuildTrustedImportParameterization(script)
		if scoped[script.Name] == nil {
			scoped[script.Name] = map[string]bool{}
		}
		for _, suggestion := range trusted.Suggestions {
			ids[suggestion.ID] = true
			scoped[script.Name][suggestion.ID] = true
		}
	}
	return ids, scoped, len(scripts) > 0
}

type ProjectedImportScripts struct {
	Value any
}

func ImportParameterizationScripts(values ...any) []*dsl.Script {
	result := make([]*dsl.Script, 0)
	for _, value := range values {
		// Decoding the projection into typed scripts is the expensive step, so a
		// caller that already did it passes the result straight through instead
		// of paying for a second marshal/unmarshal of the whole tree.
		if decoded, ok := value.([]*dsl.Script); ok {
			result = append(result, decoded...)
			continue
		}
		var projected any
		if preProjected, ok := value.(ProjectedImportScripts); ok {
			projected = preProjected.Value
		} else {
			projected = MaskScriptResponseValue(value)
		}
		nodes := make([]map[string]any, 0)
		CollectScriptJSONNodes(projected, &nodes)
		for _, node := range nodes {
			payload, err := json.Marshal(node)
			if err != nil {
				continue
			}
			var script dsl.Script
			if err := json.Unmarshal(payload, &script); err != nil {
				continue
			}
			result = append(result, &script)
		}
	}
	return result
}

func SafeParameterizationRationale(suggestion paramize.Suggestion) string {
	if suggestion.PreApplied {
		return "Applied during conversion; recorded values are not returned."
	}
	switch suggestion.Kind {
	case paramize.KindCorrelation:
		return "Detected a runtime correlation; review the source and usage locations before applying."
	case paramize.KindDataParam:
		return "Detected per-user test data; apply to bind the listed locations to a generated data source."
	case paramize.KindGenerated:
		// Written by paramize without the recorded value: it names the field
		// and what replaces the value.
		return suggestion.Rationale
	default:
		return "Review the suggested parameterization and its usage locations before applying."
	}
}

func IsNilImportValue(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}
