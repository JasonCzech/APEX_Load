package api

import "github.com/jasonczech/apexload/pkg/internal/importreview"

import (
	"context"

	"errors"
	"fmt"
	"net/http"

	"sort"

	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"

	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"

	"github.com/jasonczech/apexload/pkg/dsl"

	"github.com/jasonczech/apexload/pkg/importartifact"
	"github.com/jasonczech/apexload/pkg/importsession"
	"github.com/jasonczech/apexload/pkg/scrub"
)

type scriptImportResponseOptions = importreview.ScriptImportResponseOptions

const (
	importDraftResponseFailureKey      = "__apexload_import_draft_response_failure"
	importDraftResponseFailureErrorKey = "__apexload_import_draft_response_error"
)

var errProtectedImportStoreUnavailable = errors.New("protected import store is unavailable")

func markProtectedImportStoreUnavailable(err error) error {
	if err == nil ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, importsession.ErrSessionCapacity) ||
		errors.Is(err, importsession.ErrProjectDeleted) ||
		errors.Is(err, importsession.ErrSessionClaimed) ||
		errors.Is(err, importsession.ErrSessionClaimLost) ||
		errors.Is(err, importsession.ErrSessionExpired) ||
		errors.Is(err, importsession.ErrSessionScope) {
		return err
	}
	return fmt.Errorf("%w: %w", errProtectedImportStoreUnavailable, err)
}

type scriptImportReviewSection = importreview.ScriptImportReviewSection

type scriptImportRepairSuggestion = importreview.ScriptImportRepairSuggestion

// importResponseValue hands a typed pointer to the response sanitizers as a
// true nil when it is nil. A nil typed pointer boxed into an `any` is not a nil
// interface, and the sanitizers only short-circuit on the latter — so a field
// left unset would otherwise take the marshal-and-reparse path on its way to
// producing the same null.
func importResponseValue[T any](value *T) any { return importreview.ImportResponseValue(value) }

func newScriptImportResponse(opts scriptImportResponseOptions) map[string]any {
	response, _ := newScriptImportResponseWithProjection(opts)
	return response
}

// Return the private, read-only disclosure projection separately so source
// credential discovery can reuse it. It must never be an HTTP response field.
var newScriptImportResponseWithProjection = importreview.NewScriptImportResponseWithProjection

var primaryImportScriptProjection = importreview.PrimaryImportScriptProjection

var importResponseReviewValue = importreview.ImportResponseReviewValue

var stripImportResponseCapabilities = importreview.StripImportResponseCapabilities

var copyImportDraftCapabilities = importreview.CopyImportDraftCapabilities

var copyImportDraftCapabilityFromScriptMap = importreview.CopyImportDraftCapabilityFromScriptMap

var copyProtectedImportCredentialLocations = importreview.CopyProtectedImportCredentialLocations

var validProtectedImportCredentialLocationValues = importreview.ValidProtectedImportCredentialLocationValues

func maskImportDraftForResponse(script *dsl.Script) *dsl.Script {
	return cloneScriptDefinition(script)
}

func credentialPreservingImportResponseValue(value any) any {
	return credentialPreservingImportResponseValueFromExact(value, importResponseRawScriptValue(value), nil)
}

// credentialPreservingImportResponseValueFromExact projects value given exact,
// its JSON form, which it only reads, so a caller may also publish exact. It
// takes each script's disclosure projection from attested when it has one
// there, instead of projecting that script again.
var credentialPreservingImportResponseValueFromExact = importreview.CredentialPreservingImportResponseValueFromExact

// maskScriptResponseValueReusing returns maskScriptResponseValue(exact), where
// exact is value's JSON form, taking a script's projection from attested when
// value is that script or a slice holding it. maskScriptJSONObject would
// derive the same projection: maskScriptSecrets' costly projection reads only
// its input's JSON form, the script decoded from exact differs from the
// attested draft only by the capability marker that projection strips, and
// the cheap restores after it read the same values from the draft as from its
// JSON copy (TestImportResponseReusesAttestationProjection pins this).
var maskScriptResponseValueReusing = importreview.MaskScriptResponseValueReusing

var markCredentialPreservingImportJSON = importreview.MarkCredentialPreservingImportJSON

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
var sanitizeImportReviewEvidence = importreview.SanitizeImportReviewEvidence

var sanitizeImportReviewEvidenceWithMemo = importreview.SanitizeImportReviewEvidenceWithMemo

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
	return sanitizeImportReviewNodeWithMemo(value, key, inheritedSensitive, reviewEvidence, &importProjectionTextMemo{})
}

var sanitizeImportReviewNodeWithMemo = importreview.SanitizeImportReviewNodeWithMemo

var isImportDisclosureURLKey = importreview.IsImportDisclosureURLKey

var reviewMapHasSensitiveSemanticName = importreview.ReviewMapHasSensitiveSemanticName

var isReviewSemanticNameKey = importreview.IsReviewSemanticNameKey

var isReviewPayloadKey = importreview.IsReviewPayloadKey

var isReviewPayloadContainerKey = importreview.IsReviewPayloadContainerKey

var isReviewFreeTextKey = importreview.IsReviewFreeTextKey

var isReviewEvidenceSubtreeKey = importreview.IsReviewEvidenceSubtreeKey

var redactImportReviewValue = importreview.RedactImportReviewValue

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

var normalizeImportSanitizationValue = importreview.NormalizeImportSanitizationValue

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

// newScriptImportResponseWithSourceSecrets augments the exact imported
// authoring response with an optional, server-side migration
// session. Migration descriptors remain value-free even though imported
// scripts and source-review material retain their authored values.
func (s *Server) newScriptImportResponseWithSourceSecrets(r *http.Request, opts scriptImportResponseOptions) map[string]any {
	if opts.TextMemo == nil {
		opts.TextMemo = &importProjectionTextMemo{}
	}
	// Store the full text of truncated, mapped source files first: the source
	// reviews record each text's hash, and they are part of the drafts
	// fingerprinted below.
	s.retainImportResponseSourceTexts(r, &opts)
	protectedRequirements := opts.ProtectedRequirements
	defer clearCredentialRequirementValues(protectedRequirements)
	scriptsForCredentialReview := uniqueImportDraftScripts(importDraftScriptPointers(opts.Script, opts.Scripts))
	featureIDs := make([]string, 0, 1)
	seenFeatureIDs := map[string]struct{}{}
	for _, script := range scriptsForCredentialReview {
		candidate := featureIDForImportProfile(genericImportDraftProfile(script, ""))
		if candidate == "" {
			continue
		}
		if _, exists := seenFeatureIDs[candidate]; !exists {
			seenFeatureIDs[candidate] = struct{}{}
			featureIDs = append(featureIDs, candidate)
		}
	}
	sort.Strings(featureIDs)
	protectedCredentialReview, cleanupCredentialReview := importreview.PrepareResponseCredentials(&opts)
	defer cleanupCredentialReview()
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
			return map[string]any{
				importDraftResponseFailureKey:      true,
				importDraftResponseFailureErrorKey: err,
			}
		}
	}

	response, privateProjection := newScriptImportResponseWithProjection(opts)
	if !opts.DraftAttestationExpiresAt.IsZero() {
		response["import_draft_expires_at"] = opts.DraftAttestationExpiresAt
	}
	projectID, projectMatches := sourceSecretProjectIDForRequest(r, opts)
	if s != nil {
		response["secret_store"] = s.projectSecretCapabilities(projectID)
	}
	if projectMatches {
		// Positional with scripts: the earlier imported script each one would
		// update as a new version, or null.
		reviewed := importDraftScriptPointers(opts.Scripts)
		if len(reviewed) == 0 {
			reviewed = importDraftScriptPointers(opts.Script)
		}
		existing := s.importExistingScripts(projectID, reviewed)
		for _, match := range existing {
			if match != nil {
				response["existing_scripts"] = existing
				break
			}
		}
	}

	mergeProtectedImportCredentialReview(response, protectedCredentialReview)
	requirements, _ := response["secret_binding_requirements"].([]SecretBindingRequirement)
	if len(requirements) == 0 || s == nil || r == nil || !projectMatches {
		return response
	}

	values := sourceSecretValuesForImportResponseWithProjection(opts, requirements, privateProjection)
	protectedIDs := make(map[string]struct{}, len(protectedCredentialReview))
	for _, requirement := range protectedCredentialReview {
		protectedIDs[requirement.ID] = struct{}{}
	}
	protectedStoreAvailable := s.protectedSourceSessions != nil && s.protectedSourceSessions.Available()
	if protectedStoreAvailable {
		for _, requirement := range protectedRequirements {
			value, recoverable := credentialpolicy.RecoverableSourceCredentialValue(requirement.Value)
			if _, public := protectedIDs[requirement.ID]; public && recoverable {
				if values == nil {
					values = make(map[string]string, len(protectedCredentialReview))
				}
				values[requirement.ID] = value
			}
		}
	}
	if len(values) == 0 {
		return response
	}
	sessionID, err := s.registerSourceSecretSession(r, projectID, values, featureIDs...)
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

// mergeImportCredentialRequirementSets keeps protected requirements first so
// their in-memory value/status fields win, while retaining independently
// generated value-free requirements. Stable IDs are source/target-derived and
// therefore provide the correct cross-channel deduplication key.
var mergeImportCredentialRequirementSets = importreview.MergeImportCredentialRequirementSets

func prepareImportCredentialReview(
	scripts []*dsl.Script,
	protected []credentialpolicy.Requirement,
	supplemental []credentialpolicy.Requirement,
) ([]SecretBindingRequirement, error) {
	requirements := mergeImportCredentialRequirementSets(protected, supplemental)
	requirements = mergeReferencedImportCredentialRequirements(scripts, requirements)
	defer clearCredentialRequirementValues(requirements)
	if len(requirements) == 0 {
		return nil, nil
	}
	return attachImportCredentialReview(scripts, requirements)
}

// mergeReferencedImportCredentialRequirements fills the standardized review
// gap for converters that intentionally generate a project-secret reference
// (OpenAPI auth, WSDL unavailable schema values, AsyncAPI live broker auth,
// and similar) but omit ExistingReference requirements from their finalizer.
// These value-free descriptors are configuration guidance, never source-value
// migration authority.
var mergeReferencedImportCredentialRequirements = importreview.MergeReferencedImportCredentialRequirements

var scopeValueFreeImportCredentialRequirement = importreview.ScopeValueFreeImportCredentialRequirement

// importSecretReferenceLocation names the request a project secret reference
// sits in, such as "Body of POST telemetry.example.test/ctp", and how many
// other requests use it, so a reviewer can tell a third-party beacon from the
// application's own login. It is empty when no location is in a request.
var importSecretReferenceLocation = importreview.ImportSecretReferenceLocation

// importCredentialLocationRequest describes the HTTP request holding the
// field at path, as its method, host and path ("POST
// telemetry.example.test/ctp"), and names the field ("Body", "Authorization
// header"). A relative target is read against base. Query strings stay out:
// they often carry the very value.
var importCredentialLocationRequest = importreview.ImportCredentialLocationRequest

// requestHostPath is a request target as host and path, without scheme or
// query, resolved against base when it is relative. A templated target is
// shown as written, since its host is only known at run time.
var requestHostPath = importreview.RequestHostPath

// generatedImportSecretPresentation labels a project secret the import wrote
// into the draft: by the request it sits in (where) when known, otherwise by
// its name.
var generatedImportSecretPresentation = importreview.GeneratedImportSecretPresentation

func uniqueImportDraftScripts(scripts []*dsl.Script) []*dsl.Script {
	return importreview.UniqueImportDraftScripts(scripts)
}

// writeScriptImportResponse writes an import response that may contain an
// operator-authorized credential-bearing draft or an opaque protected-session
// capability. Such responses must never be retained by shared or browser
// caches.
func writeScriptImportResponse(w http.ResponseWriter, status int, response map[string]any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	if failed, _ := response[importDraftResponseFailureKey].(bool); failed {
		if cause, _ := response[importDraftResponseFailureErrorKey].(error); cause != nil {
			switch {
			case errors.Is(cause, importartifact.ErrUnsupportedFilesystem),
				errors.Is(cause, errProtectedImportStoreUnavailable):
				writeProtectedImportStoreError(w, cause)
				return
			case errors.Is(cause, importsession.ErrProjectDeleted),
				errors.Is(cause, importsession.ErrSessionExpired),
				errors.Is(cause, importsession.ErrSessionScope):
				writeJSON(w, http.StatusNotFound, map[string]any{
					"error":     "Protected import session is unavailable or expired. Re-import the source and try again.",
					"code":      "protected_import_session_unavailable",
					"retryable": false,
				})
				return
			case errors.Is(cause, importsession.ErrSessionClaimed),
				errors.Is(cause, importsession.ErrSessionClaimLost):
				writeProtectedImportSessionConflict(w, "Protected import session ownership changed. Retry from the current import review.")
				return
			}
		}
		w.Header().Set("Retry-After", "5")
		cause, _ := response[importDraftResponseFailureErrorKey].(error)
		ref := logServerError(w, http.StatusServiceUnavailable, cause, "code", "import_draft_attestation_unavailable")
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":     "credential-preserving import draft could not be secured; retry the import",
			"code":      "import_draft_attestation_unavailable",
			"retryable": true,
			"ref":       ref,
		})
		return
	}
	writeJSON(w, status, response)
}

func writeProtectedImportStoreError(w http.ResponseWriter, err error) {
	code := "protected_import_store_unavailable"
	message := "Protected import storage is temporarily unavailable. Verify the controller import-artifact directory and retry."
	retryable := true
	if errors.Is(err, importartifact.ErrUnsupportedFilesystem) {
		code = "protected_import_store_unsupported_filesystem"
		message = "Protected imports on Windows require an artifact directory on a local fixed NTFS or ReFS volume. Network, removable, and unsupported filesystems are not accepted."
		retryable = false
	}
	if retryable {
		w.Header().Set("Retry-After", "5")
	}
	ref := logServerError(w, http.StatusServiceUnavailable, err, "code", code)
	writeJSON(w, http.StatusServiceUnavailable, map[string]any{
		"error":     message,
		"code":      code,
		"retryable": retryable,
		"ref":       ref,
	})
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
		cloned.Locations = cloneParameterizationLocations(suggestion.Locations)
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

// filterTrustedImportParameterization keeps exact importer-authored review data
// while rejecting suggestions that cannot be re-derived from the converted
// script. Structured Aura expected values stay server-side; their safe routes
// and encoding layers remain available for review.
var filterTrustedImportParameterization = importreview.FilterTrustedImportParameterization

var trustedParameterizationIDs = importreview.TrustedParameterizationIDs

// projectedImportScripts marks a value that has already passed through
// maskScriptResponseValue. Callers that computed the projection for another
// purpose wrap it so importParameterizationScripts reuses it instead of
// re-serializing the same script tree. The wrapped value must never be mutated
// after wrapping.
type projectedImportScripts = importreview.ProjectedImportScripts

var importParameterizationScripts = importreview.ImportParameterizationScripts

var safeParameterizationRationale = importreview.SafeParameterizationRationale

var isNilImportValue = importreview.IsNilImportValue

func newRecorderImportStats(actionCount int, correlationCount int) map[string]int {
	return map[string]int{
		"converted_actions":     actionCount,
		"correlations_detected": correlationCount,
	}
}
