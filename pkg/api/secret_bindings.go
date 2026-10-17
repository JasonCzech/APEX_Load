package api

import "github.com/jasonczech/apexload/pkg/recordbridge"

import "github.com/jasonczech/apexload/pkg/internal/importreview"

// Guided secret binding turns masked import artifacts into portable scripts.
// The API never accepts or stores a secret value here: callers choose only a
// logical name and the script receives a {{secret.NAME}} reference.

import (
	"errors"
	"fmt"
	"net/http"
	"os"

	"strings"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/dsl"
)

const secretBindingRebindUnavailableDescription = importreview.SecretBindingRebindUnavailableDescription
const secretBindingsRequiredCode = importreview.SecretBindingsRequiredCode
const secretBindingRebindUnavailableCode = importreview.SecretBindingRebindUnavailableCode
const secretBindingRebindUnavailableReason = importreview.SecretBindingRebindUnavailableReason
const secretBindingRequirementsChangedCode = importreview.SecretBindingRequirementsChangedCode

var logicalSecretNamePattern = importreview.LogicalSecretNamePattern
var httpProvenanceActionPathPattern = importreview.HttpProvenanceActionPathPattern
var redactedValueKeyPattern = importreview.RedactedValueKeyPattern

// SecretBindingRequirement identifies one exact [REDACTED] occurrence in an
// executable script field. Path is an RFC 6901 JSON Pointer to the containing
// string; Occurrence distinguishes multiple sentinels in that same string.
type SecretBindingRequirement = importreview.SecretBindingRequirement

type secretBindingSelection = importreview.SecretBindingSelection

type applySecretBindingsRequest = recordbridge.ApplySecretBindingsRequest

type redactionTraversalContext = importreview.RedactionTraversalContext

type internalSecretBindingRequirement = importreview.InternalSecretBindingRequirement

type staleSecretBindingRequirementsError = importreview.StaleSecretBindingRequirementsError

// secretBindingRebindUnavailableError names, in words an operator can find
// in the editor, a selected requirement whose occurrence cannot be replaced
// without changing other imported bytes in its field.
type secretBindingRebindUnavailableError = importreview.SecretBindingRebindUnavailableError

// devFakeSecretsEnv opts a local workstation into substituting a placeholder for
// every project secret. It is deliberately NOT keyed on APEXLOAD_ENV: compose.yaml
// ships APEXLOAD_ENV=local, so keying on it silently turned the shipped local
// stack into one that sends a fake credential to whatever target the script
// points at while reporting every secret as configured.
const devFakeSecretsEnv = "APEXLOAD_DEV_FAKE_SECRETS"

// secretBindingEnforcementDisabled reports whether placeholder secrets are
// allowed. It requires an explicit opt-in and is refused outright in production.
func secretBindingEnforcementDisabled() bool {
	if !parseBoolDefault(strings.TrimSpace(os.Getenv(devFakeSecretsEnv)), false) {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("APEXLOAD_ENV"))) {
	case "production", "prod":
		return false
	default:
		return true
	}
}

var discoverSecretBindingRequirements = importreview.DiscoverSecretBindingRequirements

var discoverSecretBindingRequirementsValue = importreview.DiscoverSecretBindingRequirementsValue

type secretSuggestionRegistry = importreview.SecretSuggestionRegistry

var newSecretSuggestionRegistry = importreview.NewSecretSuggestionRegistry

var reserveExistingSecretNames = importreview.ReserveExistingSecretNames

var discoverSecretBindingRequirementsValueWithRegistry = importreview.DiscoverSecretBindingRequirementsValueWithRegistry

var discoverRedactedValues = importreview.DiscoverRedactedValues

var isImportCredentialLocatorMetadataPath = importreview.IsImportCredentialLocatorMetadataPath

// isRuntimeDataTableJSONPath reports whether path addresses the inline rows of
// a script data source (/data_sources/{n}/inline_data).
var isRuntimeDataTableJSONPath = importreview.IsRuntimeDataTableJSONPath

var isJavaVMParameterJSONPath = importreview.IsJavaVMParameterJSONPath

var redactionContextForMap = importreview.RedactionContextForMap

var isHTTPProvenanceActionType = importreview.IsHTTPProvenanceActionType

var isEngineProvenanceActionType = importreview.IsEngineProvenanceActionType

var isHeaderListEntryJSONPath = importreview.IsHeaderListEntryJSONPath

var isActionObjectJSONPath = importreview.IsActionObjectJSONPath

var isScriptActionJSONValue = importreview.IsScriptActionJSONValue

var isNonExecutableReviewAction = importreview.IsNonExecutableReviewAction

// isDurablyImportReviewGatedAction recognizes only a generic source-neutral
// execution gate. A disclosure-safe blocked draft may retain [REDACTED] inside
// the affected request because transport is impossible until the user replaces
// that behavior and removes the gate. An explicit credential-management blocker
// is different: those sentinels are intentionally bindable even when another
// review gate remains on the action.
var isDurablyImportReviewGatedAction = importreview.IsDurablyImportReviewGatedAction

// Script metadata/import identity and explicit review-only actions are
// authoring provenance. They never participate in runtime credential use.
// isImportProvenanceMetadataPath reports whether a JSON pointer addresses an
// import-provenance entry inside action metadata, e.g.
// /actions/0/metadata/original_url or /actions/0/metadata/source_transaction.
var isImportProvenanceMetadataPath = importreview.IsImportProvenanceMetadataPath

// Context-specific evidence exemptions must not hide outbound message metadata
// or arbitrary maps nested inside action parameters. The orphan-sentinel guard
// uses this same predicate as requirement discovery.
var isImportProvenanceMetadataPathForContext = importreview.IsImportProvenanceMetadataPathForContext

// isImportProvenanceMetadataKey reports whether an action-metadata key records
// where an action came from rather than a value that is sent.
//
// "original_" is NOT a safe prefix: original_request holds captured raw review
// evidence (headers, credentials) whose promotion into an executable action must
// still be rejected. Only the specific provenance keys are exempt.
var isNonExecutableRedactionSubtree = importreview.IsNonExecutableRedactionSubtree

var secretBindingRequirementID = importreview.SecretBindingRequirementID

var appendJSONPointer = importreview.AppendJSONPointer

var splitJSONPointer = importreview.SplitJSONPointer

var secretBindingLabel = importreview.SecretBindingLabel

var humanizeSecretLabel = importreview.HumanizeSecretLabel

var secretBindingContext = importreview.SecretBindingContext

var suggestedSecretName = importreview.SuggestedSecretName

var redactedValueHint = importreview.RedactedValueHint

var stringBeforeRedactedOccurrence = importreview.StringBeforeRedactedOccurrence

var normalizeLogicalSecretName = importreview.NormalizeLogicalSecretName

var isDecimalToken = importreview.IsDecimalToken

var replaceRedactedOccurrence = importreview.ReplaceRedactedOccurrence

var applySecretBindings = importreview.ApplySecretBindings

type redactedSecretBindingResolver = importreview.RedactedSecretBindingResolver

var applyCredentialPreservingSecretBindings = importreview.ApplyCredentialPreservingSecretBindings

var applySecretBindingsWithResolver = importreview.ApplySecretBindingsWithResolver

// clearResolvedCredentialManagementBlockers removes only the credential
// management execution code on request surfaces for which this binding
// operation resolved every detected occurrence. Other surfaces and other
// execution/review gates remain durable.
var clearResolvedCredentialManagementBlockers = importreview.ClearResolvedCredentialManagementBlockers

var selectedSecretBindingScopes = importreview.SelectedSecretBindingScopes

var secretBindingActionScope = importreview.SecretBindingActionScope

var dslWalkPathJSONPointer = importreview.DslWalkPathJSONPointer

var applyRedactedSecretBindings = importreview.ApplyRedactedSecretBindings

// applyPreservedRedactedSecretBindings performs an operator-selected migration
// against the exact authenticated import draft. It replaces only the selected
// projected occurrence and leaves every other imported value byte-for-byte
// intact. When the disclosure projection cannot be uniquely aligned with the
// original scalar, the operation fails instead of canonicalizing or guessing.
var applyPreservedRedactedSecretBindings = importreview.ApplyPreservedRedactedSecretBindings

// annotateSingleScriptSecretBindingRequirements annotates the requirements of
// one script. Alignment receives that script alone, while public descriptors
// retain the original batch index; do not interpret that batch index as a
// local node.
var annotateSingleScriptSecretBindingRequirements = importreview.AnnotateSingleScriptSecretBindingRequirements

// firstSelectedRequirementAt returns the selected requirement at path with
// the lowest occurrence, so a refusal names the same one every time.
var firstSelectedRequirementAt = importreview.FirstSelectedRequirementAt

// secretBindingLocation names where a script field sits, by the action's
// position and name as the editor lists them. A long JSON Pointer reads as
// noise to an operator and is rewritten by error scrubbing.
var secretBindingLocation = importreview.SecretBindingLocation

func applySuggestedSecretBindings(script *dsl.Script, scriptIndex int) (*dsl.Script, []SecretBindingRequirement, error) {
	masked := maskScriptSecrets(script)
	if masked == nil {
		return nil, nil, fmt.Errorf("failed to project script for secret binding")
	}
	requirements := discoverSecretBindingRequirements(masked, scriptIndex)
	if len(requirements) == 0 {
		return masked, requirements, nil
	}
	bindings := make([]secretBindingSelection, 0, len(requirements))
	for _, requirement := range requirements {
		bindings = append(bindings, secretBindingSelection{
			RequirementID: requirement.ID,
			SecretName:    requirement.SuggestedName,
		})
	}
	resolved, remaining, err := applySecretBindings(masked, scriptIndex, bindings)
	if err != nil {
		return nil, requirements, err
	}
	if len(remaining) != 0 {
		return nil, requirements, fmt.Errorf("failed to apply every suggested secret binding")
	}
	return resolved, requirements, nil
}

var applySecretBindingReplacements = importreview.ApplySecretBindingReplacements

var secretBindingRequirementsForImportScripts = importreview.SecretBindingRequirementsForImportScripts

var annotateSecretBindingDestinationValues = importreview.AnnotateSecretBindingDestinationValues

var annotateSecretBindingDestinationValuesWithPolicy = importreview.AnnotateSecretBindingDestinationValuesWithPolicy

// recordedPlaceholdersCoverProjection reports whether a field's recorded
// bytes already hold every disclosure placeholder its projection shows, so
// masking replaced nothing real. An unaligned field cannot say which recorded
// bytes an occurrence is; a projection that adds placeholders masked at least
// one real value, and the field then keeps a usable recording. A placeholder
// recorded as an XML message ID does not count: review shows that ID as
// projectedXMLMessageID, not as a placeholder.
var recordedPlaceholdersCoverProjection = importreview.RecordedPlaceholdersCoverProjection

var secretBindingProvenancePath = importreview.SecretBindingProvenancePath

// secretBindingInProvenance reports whether a requirement sits in recorded
// provenance: an action's or the script's own metadata, which replay never
// sends.
var secretBindingInProvenance = importreview.SecretBindingInProvenance

// replaceUnavailableImportCredentialValues removes disclosure placeholders
// from the exact server-owned candidate before an opaque draft capability is
// issued. The placeholder is replaced with the same deterministic governed
// reference offered by review. The one exception is a field whose projection
// cannot be aligned byte for byte with its recording: no reference can be
// spliced in without changing other recorded bytes, so the field keeps its
// recorded bytes and the review reports it as kept as recorded.
func replaceUnavailableImportCredentialValues(script *dsl.Script) (*dsl.Script, error) {
	if script == nil {
		return nil, errors.New("script is required")
	}
	working := cloneScriptDefinition(script)
	if working == nil {
		return nil, errors.New("failed to clone import script")
	}
	projected := maskScriptSecrets(working)
	if projected == nil {
		return nil, errors.New("failed to project import script")
	}
	rawRoot, err := scriptAsJSONValue(working)
	if err != nil {
		return nil, err
	}
	projectedRoot, err := scriptAsJSONValue(projected)
	if err != nil {
		return nil, err
	}
	requirements := discoverSecretBindingRequirementsValue(projectedRoot, 0)
	bindings := make([]secretBindingSelection, 0)
	for _, requirement := range requirements {
		rawValue, rawOK := jsonPointerString(rawRoot, requirement.Path)
		projectedValue, projectedOK := jsonPointerString(projectedRoot, requirement.Path)
		if !rawOK || !projectedOK {
			continue
		}
		values, linear := extractLinearlyProjectedRedactions(rawValue, projectedValue)
		if !linear {
			// Only a byte-exact alignment can be rebound (see
			// applyPreservedRedactedSecretBindings). The field keeps its
			// recorded bytes, as the review reported for this requirement.
			continue
		}
		if requirement.Occurrence < 0 || requirement.Occurrence >= len(values) {
			continue
		}
		value := values[requirement.Occurrence]
		status := credentialpolicy.ClassifySourceCredentialValue(value)
		if status == credentialpolicy.SourceCredentialValueUnsafeEncoding &&
			structuredImportBodyCanRemainExact(rawRoot, requirement.Path, value) {
			continue
		}
		if status == credentialpolicy.SourceCredentialValueRedactedPlaceholder ||
			status == credentialpolicy.SourceCredentialValueUnsafeEncoding {
			bindings = append(bindings, secretBindingSelection{
				RequirementID: requirement.ID,
				SecretName:    requirement.SuggestedName,
			})
		}
	}
	if len(bindings) == 0 {
		return working, nil
	}
	resolved, _, err := applyPreservedRedactedSecretBindings(working, 0, bindings)
	if err != nil {
		return nil, fmt.Errorf("replace unavailable imported credential: %w", err)
	}
	return resolved, nil
}

// scriptHasUnavailableImportCredentialValues distinguishes an authored source
// placeholder from an ordinary disclosure projection. Such a draft still needs
// a one-shot server capability even though it contains no recoverable raw
// credential: the capability authorizes replacing the exact placeholder with
// a governed reference during mutation or commit.
func scriptHasUnavailableImportCredentialValues(script *dsl.Script) bool {
	if script == nil {
		return false
	}
	return scriptHasUnavailableImportCredentialValuesFromProjection(script, importreview.NewImportDraftProjection(maskScriptSecrets(script)))
}

// scriptHasUnavailableImportCredentialValuesFromProjection reads projection,
// script's projection from fingerprinting, without modifying it.
var scriptHasUnavailableImportCredentialValuesFromProjection = importreview.ScriptHasUnavailableImportCredentialValuesFromProjection

var setSecretBindingPreservationUnavailable = importreview.SetSecretBindingPreservationUnavailable

var hasScriptJSONNode = importreview.HasScriptJSONNode

var collectScriptJSONNodes = importreview.CollectScriptJSONNodes

func (s *Server) handleApplyScriptSecretBindings(w http.ResponseWriter, r *http.Request) {
	s.recordingHTTPService().HandleApplyScriptSecretBindings(w, r)
}

func writeSecretBindingsRequired(w http.ResponseWriter, requirements []SecretBindingRequirement) {
	writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
		"error":                       "script contains redacted secret values that require logical secret bindings",
		"code":                        secretBindingsRequiredCode,
		"secret_binding_requirements": requirements,
	})
}
