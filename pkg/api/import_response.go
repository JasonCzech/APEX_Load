package api

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/scrub"
)

type scriptImportResponseOptions struct {
	Result              any
	Scripts             any
	Script              any
	Warnings            []string
	Stats               any
	Correlations        any
	Assumptions         []string
	UnsupportedElements []string
	ReviewSections      []scriptImportReviewSection
	RepairSuggestions   []scriptImportRepairSuggestion
	ScenarioDraft       any
	Summary             any
	DetectedProtocol    any
	FidelityReport      any
	ReviewArtifact      any
	SourceReview        any
	TrueClient2Compat   any
	ProtocolCompat      any
	Parameterization    *paramize.Result
	Preflight           any
	// ProtectedRequirements is an in-memory, one-shot adapter handoff. Values
	// are attached to an encrypted source-secret migration session when one is
	// available and are never serialized into the response.
	ProtectedRequirements []credentialpolicy.Requirement
	// SkipDraftAttestation is reserved for import endpoints that have already
	// committed the server-owned conversion before building their response.
	// Those responses replace the editable draft with a masked StoredScript, so
	// issuing a second one-shot save capability would only consume capacity.
	SkipDraftAttestation bool
	// RecordingSessionID is trusted server-only lineage for a proxy recording
	// preview. It lets subsequent server-owned binding/parameterization
	// mutations retain baseline provenance without accepting a client-supplied
	// recording ID as proof.
	RecordingSessionID string
	// RecordingSnapshotDigest binds lineage to the immutable request/response
	// snapshot used for the preview, so a still-live recording cannot acquire
	// new baseline observations between preview and save.
	RecordingSnapshotDigest string
	// draftAttestationExpiresAt is populated only by the server-side response
	// builder. It is never accepted from converter or client input.
	draftAttestationExpiresAt time.Time
}

const importDraftResponseFailureKey = "__apexload_import_draft_response_failure"

type scriptImportReviewSection struct {
	Key      string   `json:"key"`
	Title    string   `json:"title"`
	Severity string   `json:"severity"`
	Summary  string   `json:"summary"`
	Items    []string `json:"items"`
}

type scriptImportRepairSuggestion struct {
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

func newScriptImportResponse(opts scriptImportResponseOptions) map[string]any {
	// Import routes are project-authorized authoring boundaries. Preserve the
	// executable destination script exactly as converted so an operator who
	// already supplied a password does not lose it or have to enter it again.
	// Stored-script reads and heterogeneous review/result payloads continue to
	// use maskScriptResponseValue below; only the editable destination draft is
	// returned intact here.
	scripts := credentialPreservingImportResponseValue(opts.Scripts)
	script := credentialPreservingImportResponseValue(opts.Script)
	if isNilImportValue(scripts) {
		if !isNilImportValue(opts.Script) {
			scripts = []any{script}
		} else {
			scripts = []any{}
		}
	}
	// Credential requirements are optional migration/rebinding affordances for
	// a credential-preserving import draft. Discover them from the independent
	// disclosure projection while returning the editable executable draft
	// unchanged to the already-authorized importing operator.
	projectedScripts := maskScriptResponseValue(scripts)
	secretBindingRequirements := secretBindingRequirementsForImportScripts(projectedScripts)
	secretBindingRequirements = annotateSecretBindingDestinationValues(
		secretBindingRequirements,
		scripts,
		projectedScripts,
	)
	if !hasScriptJSONNode(scripts) && !isNilImportValue(script) {
		rawScripts := []any{script}
		projectedScripts = maskScriptResponseValue(rawScripts)
		secretBindingRequirements = secretBindingRequirementsForImportScripts(projectedScripts)
		secretBindingRequirements = annotateSecretBindingDestinationValues(
			secretBindingRequirements,
			rawScripts,
			projectedScripts,
		)
	}

	warnings := sanitizeImportReviewStrings(opts.Warnings)
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
		reviewSections = []scriptImportReviewSection{}
	}
	repairSuggestions := opts.RepairSuggestions
	if repairSuggestions == nil {
		repairSuggestions = []scriptImportRepairSuggestion{}
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
	parameterization := opts.Parameterization
	if parameterization == nil {
		parameterization = &paramize.Result{Suggestions: []paramize.Suggestion{}}
	} else {
		parameterization = sanitizeImportParameterization(parameterization, scripts)
	}

	response := map[string]any{
		"scripts":                     scripts,
		"script":                      script,
		"secret_binding_requirements": secretBindingRequirements,
		"warnings":                    warnings,
		"stats":                       sanitizeImportEmbeddedReviewEvidence(stats),
		"correlations":                sanitizeImportEmbeddedReviewEvidence(sanitizeImportCorrelationCollection(correlations)),
		"assumptions":                 sanitizeImportReviewEvidence(assumptions),
		"unsupported_elements":        sanitizeImportReviewEvidence(unsupported),
		"review_sections":             sanitizeImportReviewEvidence(reviewSections),
		"repair_suggestions":          sanitizeImportReviewEvidence(repairSuggestions),
		"scenario_draft":              sanitizeImportEmbeddedReviewEvidence(maskScriptResponseValue(opts.ScenarioDraft)),
		"summary":                     sanitizeImportEmbeddedReviewEvidence(summary),
		"detected_protocol":           sanitizeImportEmbeddedReviewEvidence(opts.DetectedProtocol),
		"fidelity_report":             sanitizeImportReviewEvidence(opts.FidelityReport),
		"review_artifact":             sanitizeImportReviewEvidence(opts.ReviewArtifact),
		"source_review":               sanitizeImportReviewEvidence(opts.SourceReview),
		"trueclient2_compat_report":   sanitizeImportReviewEvidence(opts.TrueClient2Compat),
		"protocol_compat_report":      sanitizeImportReviewEvidence(opts.ProtocolCompat),
		"parameterization":            parameterization,
		"preflight":                   sanitizeImportEmbeddedReviewEvidence(opts.Preflight),
	}
	if opts.Result != nil {
		response["result"] = sanitizeImportResponseValue(opts.Result)
	}

	return response
}

func credentialPreservingImportResponseValue(value any) any {
	raw := importResponseRawScriptValue(value)
	markCredentialPreservingImportJSON(raw)
	return sanitizeCredentialPreservingImportScriptNodes(raw)
}

// sanitizeCredentialPreservingImportScriptNodes keeps executable destination
// fields byte-faithful while applying the independent disclosure boundary for
// metadata.import.preserved review evidence and explicit review-only actions.
func sanitizeCredentialPreservingImportScriptNodes(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		if isScriptJSONObject(typed) {
			payload, err := json.Marshal(typed)
			if err != nil {
				return nil
			}
			var script dsl.Script
			if err := json.Unmarshal(payload, &script); err != nil {
				return nil
			}
			if err := sanitizePersistentImportReviewSurfaces(&script); err != nil {
				return nil
			}
			projected, err := scriptAsJSONValue(&script)
			if err != nil {
				return nil
			}
			return projected
		}
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			out[key] = sanitizeCredentialPreservingImportScriptNodes(child)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, child := range typed {
			out[index] = sanitizeCredentialPreservingImportScriptNodes(child)
		}
		return out
	default:
		return value
	}
}

func markCredentialPreservingImportJSON(value any) {
	switch typed := value.(type) {
	case []any:
		for _, child := range typed {
			markCredentialPreservingImportJSON(child)
		}
	case map[string]any:
		if isScriptJSONObject(typed) {
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
			markCredentialPreservingImportJSON(child)
		}
	}
}

// sanitizeImportResponseValue applies both script projection and correlation
// redaction to heterogeneous converter results. Correlation candidates are
// commonly derived from response tokens and session identifiers, so their
// source samples are protected material even when the converter classified
// them as replay diagnostics rather than credentials.
func sanitizeImportResponseValue(value any) any {
	projected, ok := normalizeImportSanitizationValue(maskScriptResponseValue(value))
	if !ok {
		return nil
	}
	return sanitizeImportEmbeddedReviewEvidence(sanitizeImportCorrelationFields(projected, false))
}

func sanitizeImportCorrelationCollection(value any) any {
	projected, ok := normalizeImportSanitizationValue(maskScriptResponseValue(value))
	if !ok {
		return []any{}
	}
	return sanitizeImportCorrelationFields(projected, true)
}

// sanitizeImportReviewEvidence is the last disclosure boundary for converter
// evidence. Importers commonly repeat raw review data under root fields,
// result.*, and script metadata. It understands sibling name/value shapes used
// by Postman and TruClient in addition to ordinary sensitive map keys, while
// retaining the review structure and non-secret evidence the UI relies on.
func sanitizeImportReviewEvidence(value any) any {
	if value == nil {
		return nil
	}
	normalized, ok := normalizeImportSanitizationValue(value)
	if !ok {
		return nil
	}
	return sanitizeImportReviewNode(normalized, "", false, true)
}

// sanitizeImportEmbeddedReviewEvidence preserves ordinary converter/script
// fields verbatim after their existing projection while finding and scrubbing
// review subtrees at any depth. In particular, this avoids treating executable
// URLs containing a portable token={{secret.*}} reference as raw source text.
func sanitizeImportEmbeddedReviewEvidence(value any) any {
	if value == nil {
		return nil
	}
	normalized, ok := normalizeImportSanitizationValue(value)
	if !ok {
		return nil
	}
	return sanitizeImportReviewNode(normalized, "", false, false)
}

func sanitizeImportReviewNode(value any, key string, inheritedSensitive bool, reviewEvidence bool) any {
	switch typed := value.(type) {
	case map[string]any:
		if isScriptJSONObject(typed) {
			// Script-shaped review payloads (for example Salesforce native
			// previews) have already reached the executable-script projection
			// boundary. Applying semantic review redaction again would replace
			// structured fields such as config.http.auth with a scalar. Retain
			// the typed boundary here so encoded request bodies are decoded and
			// projected as wire bytes rather than scrubbed as opaque base64 text.
			return maskScriptJSONObject(typed)
		}
		semanticSensitive := reviewEvidence && (inheritedSensitive || reviewMapHasSensitiveSemanticName(typed))
		out := make(map[string]any, len(typed))
		for childKey, child := range typed {
			childReviewEvidence := reviewEvidence || isReviewEvidenceSubtreeKey(childKey)
			if childReviewEvidence && isReviewSemanticNameKey(childKey) {
				out[childKey] = sanitizeImportReviewNode(child, childKey, false, true)
				continue
			}
			if childReviewEvidence && isSensitiveScriptKey(childKey) && !isSecretReferenceKey(childKey) {
				out[childKey] = redactImportReviewValue(child)
				continue
			}
			childSensitive := childReviewEvidence && semanticSensitive && (isReviewPayloadKey(childKey) || isReviewPayloadContainerKey(childKey))
			out[childKey] = sanitizeImportReviewNode(child, childKey, childSensitive, childReviewEvidence)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, child := range typed {
			out[index] = sanitizeImportReviewNode(child, key, inheritedSensitive, reviewEvidence)
		}
		return out
	case string:
		if isImportDisclosureURLKey(key) {
			return maskScriptURL(typed)
		}
		if !reviewEvidence {
			return typed
		}
		if inheritedSensitive || (isSensitiveScriptKey(key) && !isSecretReferenceKey(key)) {
			if isPortableTemplateBinding(typed) {
				return typed
			}
			return projectSensitiveScriptValue(typed)
		}
		redacted, _ := sourceview.Redact(typed)
		if isReviewFreeTextKey(key) {
			redacted = scrub.Value(redacted, scrub.Policy{})
		}
		return redacted
	case nil:
		return nil
	default:
		if inheritedSensitive || (isSensitiveScriptKey(key) && !isSecretReferenceKey(key)) {
			return maskedScriptSecret
		}
		return value
	}
}

func isImportDisclosureURLKey(key string) bool {
	normalized := normalizeScriptFieldName(key)
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

func reviewMapHasSensitiveSemanticName(value map[string]any) bool {
	for key, child := range value {
		if !isReviewSemanticNameKey(key) {
			continue
		}
		name, ok := child.(string)
		if ok && (isSensitiveScriptKey(name) || scrub.SensitiveName(name) || (scrub.Policy{}).SensitiveHeader(name)) {
			return true
		}
	}
	return false
}

func isReviewSemanticNameKey(key string) bool {
	switch normalizeScriptFieldName(key) {
	case "key", "name", "field", "fieldname", "label", "header", "headername", "attributename", "property":
		return true
	default:
		return false
	}
}

func isReviewPayloadKey(key string) bool {
	switch normalizeScriptFieldName(key) {
	case "value", "text", "content", "selector", "xpath", "regex", "sourcesnippet", "snippet", "raw", "source", "arguments":
		return true
	default:
		return false
	}
}

func isReviewPayloadContainerKey(key string) bool {
	switch normalizeScriptFieldName(key) {
	case "attributes", "selectorcandidates", "objectdescriptor", "objectdescriptors", "steps", "unsupportedfragments":
		return true
	default:
		return false
	}
}

func isReviewFreeTextKey(key string) bool {
	switch normalizeScriptFieldName(key) {
	case "content", "sourcesnippet", "snippet", "raw", "source", "arguments", "summary", "description",
		"warning", "warnings", "assumption", "assumptions", "unsupported", "unsupportedelement",
		"unsupportedelements", "diagnostic", "diagnostics", "issue", "issues", "note", "notes":
		return true
	default:
		return false
	}
}

func isReviewEvidenceSubtreeKey(key string) bool {
	normalized := normalizeScriptFieldName(key)
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

func redactImportReviewValue(value any) any {
	if text, ok := value.(string); ok {
		if isPortableTemplateBinding(text) {
			return text
		}
		return projectSensitiveScriptValue(text)
	}
	return maskedScriptSecret
}

func sanitizeImportReviewStrings(values []string) []string {
	if values == nil {
		return nil
	}
	result := make([]string, len(values))
	for index, value := range values {
		redacted, _ := sourceview.Redact(value)
		result[index] = scrub.Value(redacted, scrub.Policy{})
	}
	return result
}

func normalizeImportSanitizationValue(value any) (any, bool) {
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

func sanitizeImportCorrelationFields(value any, insideCorrelation bool) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			if insideCorrelation {
				switch strings.ToLower(strings.TrimSpace(key)) {
				case "value", "sample_value", "samplevalue", "source_value", "sourcevalue",
					"recorded_value", "recordedvalue", "literal_value", "literalvalue", "search_text", "searchtext":
					continue
				}
			}
			childIsCorrelation := insideCorrelation || strings.EqualFold(strings.TrimSpace(key), "correlations")
			out[key] = sanitizeImportCorrelationFields(child, childIsCorrelation)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, child := range typed {
			out[index] = sanitizeImportCorrelationFields(child, insideCorrelation)
		}
		return out
	default:
		return value
	}
}

// newScriptImportResponseWithSourceSecrets augments the disclosure-safe import
// response with an optional, short-lived server-side migration session. Raw
// source values never enter the returned response; the session is addressed
// only by opaque requirement IDs.
func (s *Server) newScriptImportResponseWithSourceSecrets(r *http.Request, opts scriptImportResponseOptions) map[string]any {
	protectedRequirements := opts.ProtectedRequirements
	defer clearCredentialRequirementValues(protectedRequirements)

	var protectedCredentialReview []SecretBindingRequirement
	if len(protectedRequirements) > 0 {
		script, ok := opts.Script.(*dsl.Script)
		if !ok || script == nil {
			opts.Warnings = append(opts.Warnings, "Protected credential bindings could not be prepared; the generated secret references remain available for manual configuration.")
		} else {
			var err error
			protectedCredentialReview, err = attachProtectedImportCredentialReview(script, protectedRequirements)
			if err != nil {
				opts.Warnings = append(opts.Warnings, "Protected credential bindings could not be prepared; the generated secret references remain available for manual configuration.")
			}
		}
	}
	// Protected requirement descriptors are part of the editable destination
	// draft. Attach them before fingerprinting the draft so a mixed import
	// (an exact inline credential plus an independently bindable reference)
	// can be saved unchanged with the capability returned by this response.
	if !opts.SkipDraftAttestation {
		if err := s.attachImportDraftAttestations(r, &opts); err != nil {
			// Never publish an exact credential-bearing draft that the operator
			// cannot subsequently save. The writer recognizes this private
			// marker and replaces the entire response, including any fields a
			// handler may add later, with a value-free retryable error.
			return map[string]any{importDraftResponseFailureKey: true}
		}
	}

	response := newScriptImportResponse(opts)
	if !opts.draftAttestationExpiresAt.IsZero() {
		response["import_draft_expires_at"] = opts.draftAttestationExpiresAt
	}
	projectID, projectMatches := sourceSecretProjectIDForRequest(r, opts)
	if s != nil {
		response["secret_store"] = s.projectSecretCapabilities(projectID)
	}

	requirements, _ := response["secret_binding_requirements"].([]SecretBindingRequirement)
	mergeProtectedImportCredentialReview(response, protectedCredentialReview)
	requirements, _ = response["secret_binding_requirements"].([]SecretBindingRequirement)
	if len(requirements) == 0 || s == nil || r == nil || !projectMatches {
		return response
	}

	values := sourceSecretValuesForImportResponse(opts, requirements)
	protectedIDs := make(map[string]struct{}, len(protectedCredentialReview))
	for _, requirement := range protectedCredentialReview {
		protectedIDs[requirement.ID] = struct{}{}
	}
	protectedStoreAvailable := s.protectedSourceSessions != nil && s.protectedSourceSessions.Available()
	if protectedStoreAvailable {
		for _, requirement := range protectedRequirements {
			if _, public := protectedIDs[requirement.ID]; public && requirement.Value != "" {
				if values == nil {
					values = make(map[string]string, len(protectedCredentialReview))
				}
				values[requirement.ID] = requirement.Value
			}
		}
	}
	if len(values) == 0 {
		return response
	}
	sessionID, err := s.registerSourceSecretSession(r, projectID, values)
	if err != nil {
		warnings, _ := response["warnings"].([]string)
		response["warnings"] = append(warnings, optionalCredentialMigrationUnavailableWarning)
		return response
	}
	if sessionID == "" {
		return response
	}
	for index := range requirements {
		_, requirements[index].SourceValueAvailable = values[requirements[index].ID]
	}
	response["secret_binding_requirements"] = requirements
	response["source_secret_session_id"] = sessionID
	response["source_secret_session_expires_at"] = time.Now().UTC().Add(sourceSecretSessionTTL)
	response["source_secret_migration_allowed"] = true
	return response
}

// writeScriptImportResponse writes an import response that may contain an
// operator-authorized credential-bearing draft or an opaque protected-session
// capability. Such responses must never be retained by shared or browser
// caches.
func writeScriptImportResponse(w http.ResponseWriter, status int, response map[string]any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	if failed, _ := response[importDraftResponseFailureKey].(bool); failed {
		w.Header().Set("Retry-After", "5")
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":     "credential-preserving import draft could not be secured; retry the import",
			"code":      "import_draft_attestation_unavailable",
			"retryable": true,
		})
		return
	}
	writeJSON(w, status, response)
}

func (s *Server) writeScriptImportResponseWithSourceSecrets(
	w http.ResponseWriter,
	r *http.Request,
	status int,
	opts scriptImportResponseOptions,
) {
	writeScriptImportResponse(w, status, s.newScriptImportResponseWithSourceSecrets(r, opts))
}

// Secret parameterization is represented by the guided Credentials workflow.
// The wire representation is intentionally a review descriptor, not a replayable
// mutation instruction: raw literals and search text stay server-side, and the
// apply endpoint re-derives authoritative instructions from the submitted safe
// script and selected opaque IDs.
func sanitizeImportParameterization(input *paramize.Result, scriptValues ...any) *paramize.Result {
	if input == nil {
		return &paramize.Result{Suggestions: []paramize.Suggestion{}}
	}
	trustedIDs, trustedScopedIDs, hasScriptContext := trustedParameterizationIDs(scriptValues...)
	output := *input
	output.Suggestions = make([]paramize.Suggestion, 0, len(input.Suggestions))
	for _, suggestion := range input.Suggestions {
		if suggestion.Kind == paramize.KindSecret {
			continue
		}
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
		cloned.Locations = append([]paramize.Location(nil), suggestion.Locations...)
		for index := range cloned.Locations {
			cloned.Locations[index].SearchText = ""
		}
		if suggestion.FoundInAction != nil {
			found := *suggestion.FoundInAction
			found.SearchText = ""
			cloned.FoundInAction = &found
		}
		cloned.LiteralValue = ""
		cloned.SampleValue = ""
		cloned.Rationale = safeParameterizationRationale(cloned)
		// These internal instructions are not rendered by the review UI. Keeping
		// them off the wire makes the server-side re-derivation boundary explicit.
		cloned.Extractor = nil
		cloned.Parameter = nil
		output.Suggestions = append(output.Suggestions, cloned)
	}
	output.Summary = paramizeSummary(output.Suggestions)
	return &output
}

func trustedParameterizationIDs(scriptValues ...any) (map[string]bool, map[string]map[string]bool, bool) {
	ids := map[string]bool{}
	scoped := map[string]map[string]bool{}
	scripts := importParameterizationScripts(scriptValues...)
	for _, script := range scripts {
		trusted := buildTrustedImportParameterization(script)
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

func importParameterizationScripts(values ...any) []*dsl.Script {
	result := make([]*dsl.Script, 0)
	for _, value := range values {
		projected := maskScriptResponseValue(value)
		nodes := make([]map[string]any, 0)
		collectScriptJSONNodes(projected, &nodes)
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

func safeParameterizationRationale(suggestion paramize.Suggestion) string {
	if suggestion.PreApplied {
		return "Applied during conversion; recorded values are not returned."
	}
	switch suggestion.Kind {
	case paramize.KindCorrelation:
		return "Detected a runtime correlation; review the source and usage locations before applying."
	case paramize.KindDataParam:
		return "Detected per-user test data; apply to bind the listed locations to a generated data source."
	default:
		return "Review the suggested parameterization and its usage locations before applying."
	}
}

func isNilImportValue(value any) bool {
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

func newRecorderImportStats(actionCount int, correlationCount int) map[string]int {
	return map[string]int{
		"converted_actions":     actionCount,
		"correlations_detected": correlationCount,
	}
}
