package api

import "github.com/jasonczech/apexload/pkg/internal/importreview"

// Script response projection and masked-write reconciliation live here rather
// than beside execution-time masking. Trusted imported authoring artifacts
// retain their exact values; ordinary disclosure surfaces and runtime evidence
// continue to use the existing masking boundaries.

import (
	"encoding/json"

	"reflect"

	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"

	"github.com/jasonczech/apexload/pkg/scriptdisclosure"

	"github.com/jasonczech/apexload/pkg/storage"
)

const maskedScriptSecret = importreview.MaskedScriptSecret
const credentialParameterSourceRedactedBlocker = importreview.CredentialParameterSourceRedactedBlocker
const scriptProjectionResponseGateMetadataKey = importreview.ScriptProjectionResponseGateMetadataKey
const scriptProjectionUnprojectableBodyReason = importreview.ScriptProjectionUnprojectableBodyReason

var maskedImportHeaderBytes = importreview.MaskedImportHeaderBytes

var scriptSecretTemplatePattern = importreview.ScriptSecretTemplatePattern
var scriptPortableTemplatePattern = importreview.ScriptPortableTemplatePattern
var scriptPortableReferencePattern = importreview.ScriptPortableReferencePattern
var scriptPortableRandomPattern = importreview.ScriptPortableRandomPattern
var scriptPortableRandomStringPattern = importreview.ScriptPortableRandomStringPattern

// projectedXMLMessageID is how review shows a token-like XML message ID: as
// a fresh UUID per request, rather than masked like a credential.
const projectedXMLMessageID = importreview.ProjectedXMLMessageID

var redactedXMLMessageIDPattern = importreview.RedactedXMLMessageIDPattern
var xmlMessageIDValuePattern = importreview.XmlMessageIDValuePattern
var portableXMLSecretElementPattern = importreview.PortableXMLSecretElementPattern
var portableXMLSecretAttributePattern = importreview.PortableXMLSecretAttributePattern
var portableXMLTemplateElementPattern = importreview.PortableXMLTemplateElementPattern
var portableXMLTemplateAttributePattern = importreview.PortableXMLTemplateAttributePattern

// maskScriptSecrets returns a deep, portable copy of script with inline
// credentials removed. It intentionally preserves explicit secret references
// (for example {{secret.api_token}} and *_secret reference fields) so an export
// describes how to bind a secret without disclosing its value.
var maskScriptSecrets = importreview.MaskScriptSecrets

// maskScriptSecretsWithMemo is maskScriptSecrets scrubbing text through memo,
// which a caller projecting several related scripts shares between them; nil
// gives this projection a memo of its own.
var maskScriptSecretsWithMemo = importreview.MaskScriptSecretsWithMemo

// projectScriptDisclosureCopy is the costly, payload-determined part of
// maskScriptSecrets, and its result is fully masked. It comes from a JSON
// decode, so it survives the cache's JSON round trip unchanged. It reads only
// payload, never a caller's in-memory Go values, so the cache key covers all
// it depends on. Text is scrubbed through memo (nil for a memo of its own).
var projectScriptDisclosureCopy = importreview.ProjectScriptDisclosureCopy

// maskImportHeaderFidelityBytes projects the exact, inert source bytes retained
// for runtime-incompatible headers and multipart fields. The durable execution
// copy keeps those bytes behind import_review_required; ordinary reads,
// exports, and review responses mask them. An occurrence's name field still
// names the header (or quotes a name HTTP cannot carry) on purpose, so the
// review and the editor can say which header was left out: nothing an
// imported script holds is a secret. The historical function name is retained
// because header fidelity was the first consumer of this shared byte envelope.
var maskImportHeaderFidelityBytes = importreview.MaskImportHeaderFidelityBytes

var maskHeaderFidelityOccurrenceBytes = importreview.MaskHeaderFidelityOccurrenceBytes

// importHeaderFidelityProjectionChanged lets the protected import-draft
// attestation recognize credentials that exist only in inert, gated header or
// multipart fidelity envelopes. Secret-binding discovery intentionally skips
// gated actions, so without this comparison a legacy create would treat the
// editable import draft as untrusted and persist its response mask instead of
// the exact bytes. The historical function name remains for compatibility.
var importHeaderFidelityProjectionChanged = importreview.ImportHeaderFidelityProjectionChanged

var importHeaderFidelitySnapshot = importreview.ImportHeaderFidelitySnapshot

const maxScriptProjectionDataSourceBytes = importreview.MaxScriptProjectionDataSourceBytes
const maxScriptProjectionDataSourceCells = importreview.MaxScriptProjectionDataSourceCells

// maskScriptDataSourceCredentials projects credential-bearing list parameters
// and inline CSV/DAT cells without changing the durable runtime copy. Generic
// text scrubbing cannot infer that a short value is sensitive merely because a
// neutral source variable later feeds Authorization. Importer provenance and
// column semantics close that response-only leak.
var maskScriptDataSourceCredentials = importreview.MaskScriptDataSourceCredentials

var canonicalCredentialParameterName = importreview.CanonicalCredentialParameterName

var importPreservedStringSlice = importreview.ImportPreservedStringSlice

// sanitizePersistentImportReviewSurfaces keeps trusted credential-preserving
// imports exact. Other callers retain the legacy projection boundary so a
// client cannot manufacture import metadata as a general disclosure channel.
var sanitizePersistentImportReviewSurfaces = importreview.SanitizePersistentImportReviewSurfaces

var isExecutableImportPreservedPayloadKey = importreview.IsExecutableImportPreservedPayloadKey

const maxImportedJavaVMParameterRowBytes = importreview.MaxImportedJavaVMParameterRowBytes
const maxImportedJavaVMParameterRows = importreview.MaxImportedJavaVMParameterRows

var isImportPreservedJavaVMSettingsKey = importreview.IsImportPreservedJavaVMSettingsKey

// isExecutableJavaVMParameterMember reports whether a java_vm_settings member
// carries the imported JVM command line that the enterprise-archive converter
// deliberately keeps exact (executableJavaVMParameterRow /
// executableJavaVMParameters). Every other member — classpath provenance, JDK
// selection, raw enums — stays on the ordinary provenance projection.
var isExecutableJavaVMParameterMember = importreview.IsExecutableJavaVMParameterMember

// projectPreservedJavaVMSettings applies the ordinary provenance projection to
// preserved["java_vm_settings"] while retaining the imported JVM command line
// exactly.
//
// Those rows are the operator's own command line, the round-trip record of the
// archive, and the source of the dashboard VM-parameter picker. The converter
// keeps them verbatim, but this durable-write boundary previously ran them
// through the credential projection, so an archive configuring
// "-Dservice.password=..." reached storage as "-Dservice.password=[REDACTED]" —
// and, because export, download, GitHub push, and library bundling ship the
// stored script, that unrunnable command line was what the operator got back.
// Nothing an imported script already contains is a project secret; only a
// platform-managed secret-store value is, and none ever lands here.
var projectPreservedJavaVMSettings = importreview.ProjectPreservedJavaVMSettings

// retainableJavaVMParameterMember returns the member unchanged when it has the
// bounded shape the converter writes: one command-line row, or an ordered list
// of them. Any other shape stays on the ordinary projection so this key cannot
// be used to smuggle arbitrary durable content past the sanitizer.
var retainableJavaVMParameterMember = importreview.RetainableJavaVMParameterMember

var projectScriptDataSourceTable = importreview.ProjectScriptDataSourceTable

var scriptDataSourceRecordLooksLikeCredentialHeader = importreview.ScriptDataSourceRecordLooksLikeCredentialHeader

var scriptDataSourceColumnRequired = importreview.ScriptDataSourceColumnRequired

var maskRawBrunoRuntimeArtifactReference = importreview.MaskRawBrunoRuntimeArtifactReference

// maskJavaVuserVMParameterValues is a read/export defense for legacy rows and
// any response assembled before current DSL validation runs. JVM arguments are
// observable in the operating-system process table, so an unsafe value must
// never be copied back through an API projection. Safe JVM tuning flags and
// ordinary configuration values remain visible and round-trip unchanged.
var maskJavaVuserVMParameterValues = importreview.MaskJavaVuserVMParameterValues

// validateJavaVMParameterDisclosureSafety is the write-side companion to the
// legacy response masker above. JVM arguments intentionally do not support
// {{secret.*}} resolution because argv is process-observable. Reject unsafe
// values with value-free diagnostics before generic secret-binding discovery
// can offer an invalid project-secret binding for this surface.
var validateJavaVMParameterDisclosureSafety = importreview.ValidateJavaVMParameterDisclosureSafety

var validateJavaVMParameterSafety = importreview.ValidateJavaVMParameterSafety

// projectScriptRequestBodies keeps every request body out of the generic
// free-text projector. Shared structured codecs preserve source bytes and
// rewrite only credential spans; unrecognized raw/file bytes pass through
// exactly. Unsafe typed or mixed content fails closed.
var projectScriptRequestBodies = importreview.ProjectScriptRequestBodies

var applyScriptProjectionResponseGate = importreview.ApplyScriptProjectionResponseGate

var projectMaskedXMLMessageIDs = importreview.ProjectMaskedXMLMessageIDs

// rewrittenMessageIDs pairs each XML message ID review shows as
// projectedXMLMessageID with the one raw records at the same position, as the
// [start, end) of their contents: projected's, then raw's. ok is false when
// raw and projected disagree on how many message IDs there are.
var rewrittenMessageIDs = importreview.RewrittenMessageIDs

// projectedMessageIDsAsRecorded returns projected with each XML message ID
// review shows as projectedXMLMessageID put back as raw recorded it. That
// rewrite is the one change the projection makes without a [REDACTED]
// sentinel, so aligning a field with its projection, and binding a
// credential in it, compare against and keep the recorded ID there.
// projected comes back unchanged when raw and projected disagree on how many
// message IDs there are, or when a recorded ID it would put back is itself
// the [REDACTED] placeholder: that would add a sentinel review never showed
// and shift every later occurrence.
var projectedMessageIDsAsRecorded = importreview.ProjectedMessageIDsAsRecorded

func maskStoredScript(stored *StoredScript) *StoredScript {
	if stored == nil {
		return nil
	}
	view := cloneStoredScriptForReadLocked(stored)
	if isCredentialPreservingImport(stored.Script) {
		return view
	}
	view.Script = maskScriptSecrets(view.Script)
	view.Name = disclosureSafeScriptName(stored.Name, stored.Script)
	return view
}

// viewStoredScript is the authenticated stored-script authoring projection.
// Unlike import receipts and recorder conversion responses, this is the
// surface used by the data-file editor after a script has been saved. Keep the
// exact authored CSV/DAT bytes visible so an operator can verify the values
// that will be supplied to a run. Runtime evidence has its own redaction
// boundary and remains unaffected.
func viewStoredScript(stored *StoredScript) *StoredScript {
	view := maskStoredScript(stored)
	if view == nil || isCredentialPreservingImport(stored.Script) {
		return view
	}
	restoreStoredScriptDataFileContents(view.Script, stored.Script)
	return view
}

func restoreStoredScriptDataFileContents(view, stored *dsl.Script) {
	if view == nil || stored == nil || len(view.DataSources) != len(stored.DataSources) {
		return
	}
	for index := range view.DataSources {
		sourceType := strings.ToLower(strings.TrimSpace(stored.DataSources[index].Type))
		if sourceType != "csv" && sourceType != "dat" {
			continue
		}
		view.DataSources[index].InlineData = stored.DataSources[index].InlineData
	}
}

// disclosureSafeScriptName projects a detached wrapper, filename, or audit
// name using the executable script as the credential source. Import formats
// commonly repeat a collection/request label outside the nested DSL object;
// that duplicate must not turn an otherwise masked response into a disclosure.
func disclosureSafeScriptName(name string, credentialSource *dsl.Script) string {
	if credentialSource == nil {
		return scrubScriptText(name, false)
	}
	return scriptdisclosure.ProjectLabel(name, credentialSource)
}

func maskStoredScripts(scripts []*StoredScript) []*StoredScript {
	if scripts == nil {
		return nil
	}
	masked := make([]*StoredScript, len(scripts))
	for i, script := range scripts {
		masked[i] = maskStoredScript(script)
	}
	return masked
}

func maskAuthoringSession(session *storage.AuthoringSession) *storage.AuthoringSession {
	if session == nil {
		return nil
	}
	masked := cloneAuthoringSession(session)
	if !isCredentialPreservingImport(session.Draft) {
		masked.Draft = maskScriptSecrets(masked.Draft)
		masked.Name = disclosureSafeScriptName(session.Name, session.Draft)
	}
	masked.ReviewState = projectAuthoringReviewState(masked.ReviewState)
	delete(masked.ReviewState, authoringSessionIdempotencyReviewKey)
	return masked
}

func maskAuthoringSessions(sessions []*storage.AuthoringSession) []*storage.AuthoringSession {
	if sessions == nil {
		return nil
	}
	masked := make([]*storage.AuthoringSession, len(sessions))
	for i, session := range sessions {
		masked[i] = maskAuthoringSession(session)
	}
	return masked
}

// projectAuthoringReviewState makes the client-extensible review_state map a
// disclosure-safe authoring surface. It preserves ordinary workflow metadata
// while scrubbing credential-named fields and secret-shaped free text. The map
// is never executable, so retained {{secret.*}} descriptors are informational
// and do not become runtime requirements.
func projectAuthoringReviewState(state map[string]any) map[string]any {
	if len(state) == 0 {
		return nil
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return nil
	}
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil
	}
	projected, _ := projectScriptNode(raw, importreview.ScriptProjectionContextFor("", false, true)).(map[string]any)
	return projected
}

type scriptProjectionContext = importreview.ScriptProjectionContext

var projectScriptNode = importreview.ProjectScriptNode

var projectImportCredentialTargetLocators = importreview.ProjectImportCredentialTargetLocators

var validImportCredentialLocatorText = importreview.ValidImportCredentialLocatorText

var isOpaqueImportSourcePayloadKey = importreview.IsOpaqueImportSourcePayloadKey

var maskOpaqueImportSourcePayload = importreview.MaskOpaqueImportSourcePayload

var projectJavaVuserApprovalEvidence = importreview.ProjectJavaVuserApprovalEvidence

// restoreProjectedJavaVuserApprovalEvidence repairs the single governance
// envelope that the final generic disclosure pass necessarily sees as opaque
// metadata. Only a complete canonical UUID/SHA-256 pair is copied, and only
// for a Java Vuser script whose executable surfaces require this approval.
// Ordinary session_id fields remain irreversibly projected.
var restoreProjectedJavaVuserApprovalEvidence = importreview.RestoreProjectedJavaVuserApprovalEvidence

const sha256HexLength = importreview.Sha256HexLength

var isCanonicalSHA256 = importreview.IsCanonicalSHA256

var isCanonicalLowerHexDigest = importreview.IsCanonicalLowerHexDigest

var scriptMapHasCredentialValue = importreview.ScriptMapHasCredentialValue

var projectHeaderEntry = importreview.ProjectHeaderEntry

var projectHeaderValue = importreview.ProjectHeaderValue

var projectScriptString = importreview.ProjectScriptString

var isNonSecretIdentifierHeader = importreview.IsNonSecretIdentifierHeader

var maskScriptURL = importreview.MaskScriptURL

var normalizeScriptFieldName = importreview.NormalizeScriptFieldName

var isSensitiveScriptKey = importreview.IsSensitiveScriptKey

var isSecretReferenceKey = importreview.IsSecretReferenceKey

var isPortableSecretReference = importreview.IsPortableSecretReference

func isPortableSecretBinding(value string) bool {
	if strings.Contains(value, maskedScriptSecret) || !scriptSecretTemplatePattern.MatchString(value) {
		return false
	}
	remainder := strings.TrimSpace(scriptSecretTemplatePattern.ReplaceAllString(value, ""))
	switch strings.ToLower(remainder) {
	case "", "bearer", "basic", "digest":
		return true
	default:
		return false
	}
}

var isPortableTemplateBinding = importreview.IsPortableTemplateBinding

// projectSensitiveScriptValue retains only a non-secret authentication scheme
// wrapper. This lets guided binding turn `Bearer [REDACTED]` into
// `Bearer {{secret.NAME}}` without restoring or disclosing the credential.
var projectSensitiveScriptValue = importreview.ProjectSensitiveScriptValue

var portableScriptTemplateReferences = importreview.PortableScriptTemplateReferences

var isPortableScriptTemplateReference = importreview.IsPortableScriptTemplateReference

var scrubScriptText = importreview.ScrubScriptText

var scrubScriptTextWithProjector = importreview.ScrubScriptTextWithProjector

var portableBodyTemplateValues = importreview.PortableBodyTemplateValues

// protectAdditionalURLParamSeparators temporarily represents every '?' after
// the URL's first query delimiter (and every '?' in the fragment) as an
// ordinary '&'-delimited, low-entropy parameter. This lets ValueURL inspect
// each imported parameter independently while the collision-safe marker makes
// restoration exact even when the source already contains marker-like text.
var protectAdditionalURLParamSeparators = importreview.ProtectAdditionalURLParamSeparators

// protectPortableXMLSecretBindings preserves already-governed secret
// references when they are the complete value of an XML element or attribute.
// The generic scrubber intentionally replaces the entire value of
// credential-named XML nodes, so protecting only the inner {{secret.*}} token
// would still lose the executable reference.
var protectPortableXMLSecretBindings = importreview.ProtectPortableXMLSecretBindings

// scrubPortableXMLElementOpeningTag keeps whole-element template protection from
// hiding plaintext credentials in sibling attributes. Only the element text
// template needs preservation; the opening tag must still pass through the
// ordinary credential scrubber before the complete element is hidden behind a
// placeholder.
var scrubPortableXMLElementOpeningTag = importreview.ScrubPortableXMLElementOpeningTag

var protectTemplateBindings = importreview.ProtectTemplateBindings

// scriptProjectionPlaceholderPrefix returns a deterministic marker namespace
// that cannot occur in the input. Digest groups are deliberately shorter than
// the generic high-entropy-token threshold so the scrubber cannot redact its
// own temporary markers.
var scriptProjectionPlaceholderPrefix = importreview.ScriptProjectionPlaceholderPrefix

var isNamedSecretReference = importreview.IsNamedSecretReference

var isScriptURLKey = importreview.IsScriptURLKey

var isScriptFreeTextKey = importreview.IsScriptFreeTextKey

var isOpaqueScriptMap = importreview.IsOpaqueScriptMap

var isScriptHeaderMap = importreview.IsScriptHeaderMap

var isScriptHeaderList = importreview.IsScriptHeaderList

var isScriptSecretBindingsMap = importreview.IsScriptSecretBindingsMap

// reconcileMaskedScriptSecrets restores only executable sentinels that exactly
// match the projection of a prior value. A new/orphan executable sentinel is
// rejected, preventing a downloaded or federated masked script from executing
// "[REDACTED]" as though it were a credential. Redacted import/source-review
// evidence is retained as non-executable provenance and intentionally skipped.
var reconcileMaskedScriptSecrets = importreview.ReconcileMaskedScriptSecrets

func matchesStoredScriptDisclosureProjection(candidate, stored *dsl.Script) bool {
	if candidate == nil || stored == nil {
		return false
	}
	projected := maskScriptSecrets(stored)
	return projected != nil && reflect.DeepEqual(candidate, projected)
}

var reconcileMaskedScriptSecretsWithTrust = importreview.ReconcileMaskedScriptSecretsWithTrust

var scriptAsJSONValue = importreview.ScriptAsJSONValue

func reconcileProjectedJSON(incoming any, projectedPrior any, rawPrior any, path string) (any, error) {
	return reconcileProjectedJSONWithOptions(incoming, projectedPrior, rawPrior, path, false, false)
}

var reconcileProjectedJSONWithOptions = importreview.ReconcileProjectedJSONWithOptions

// reconcileMaskedImportHeaderFidelityBytes restores unchanged response
// projections of the inert header and multipart fidelity envelopes. Review-
// gated actions normally skip secret reconciliation because their evidence is
// non-executable, but these bytes are intentionally durable: persisting the
// fixed masked payload would silently destroy the operator's exact imported
// source. The historical function name remains for compatibility.
var reconcileMaskedImportHeaderFidelityBytes = importreview.ReconcileMaskedImportHeaderFidelityBytes

var reconcileMaskedImportFidelityBytes = importreview.ReconcileMaskedImportFidelityBytes

func importHeaderFidelityMarkerJSON(action map[string]any) map[string]any {
	return importFidelityMarkerJSON(action, dsl.ImportHeaderFidelityMetadataKey)
}

var importFidelityMarkerJSON = importreview.ImportFidelityMarkerJSON

func importHeaderFidelityOccurrencesJSON(marker map[string]any) ([]map[string]any, bool) {
	return importFidelityOccurrencesJSON(marker, "omitted_occurrences")
}

var importFidelityOccurrencesJSON = importreview.ImportFidelityOccurrencesJSON

var headerFidelityOccurrencesContainMask = importreview.HeaderFidelityOccurrencesContainMask

var headerFidelityOccurrenceIdentity = importreview.HeaderFidelityOccurrenceIdentity

var isTransientScriptProjectionGate = importreview.IsTransientScriptProjectionGate

var hasScriptProjectionResponseGate = importreview.HasScriptProjectionResponseGate

var scriptProjectionResponseGateMarker = importreview.ScriptProjectionResponseGateMarker

var withoutTransientScriptProjectionGate = importreview.WithoutTransientScriptProjectionGate

var jsonValueContainsExecutableMaskedSecret = importreview.JsonValueContainsExecutableMaskedSecret

var jsonValueContainsExecutableMaskedSecretWithOptions = importreview.JsonValueContainsExecutableMaskedSecretWithOptions

var jsonValueContainsExecutableMaskedSecretWithContext = importreview.JsonValueContainsExecutableMaskedSecretWithContext

// isStableRuntimeDataSourceEdit permits field-level reconciliation for a
// same-size, same-order data-source list. This lets an operator replace one
// gated imported table while a different table is still masked for transport.
// Renames, additions, removals, duplicates, and reordering remain rejected so
// a projected sentinel can never be restored into the wrong source.
var isStableRuntimeDataSourceEdit = importreview.IsStableRuntimeDataSourceEdit

// isStableCredentialPreservingActionCollectionEdit permits field-level
// reconciliation for masked imported requests when every action remains at the
// same structural position and retains its request-routing identity. This lets
// operators edit labels, assertions, extractors, and other non-wire authoring
// fields without re-entering imported credentials. Request destination changes
// and reordering continue to fail closed here; the post-reconciliation import
// context fence independently protects raw/import-response updates.
var isStableCredentialPreservingActionCollectionEdit = importreview.IsStableCredentialPreservingActionCollectionEdit

var isActionCollectionTraversalPath = importreview.IsActionCollectionTraversalPath

var credentialPreservingActionIdentity = importreview.CredentialPreservingActionIdentity

var credentialPreservingActionIdentityKey = importreview.CredentialPreservingActionIdentityKey

var credentialPreservingActionSecondaryIdentity = importreview.CredentialPreservingActionSecondaryIdentity

var scriptHasRedactedRuntimeCredentialSource = importreview.ScriptHasRedactedRuntimeCredentialSource

var scriptHasExecutionBlockerCode = importreview.ScriptHasExecutionBlockerCode

var isRuntimeDataTableTraversalPath = importreview.IsRuntimeDataTableTraversalPath

var isLegacyRedactedRuntimeSourceTraversalPath = importreview.IsLegacyRedactedRuntimeSourceTraversalPath

// maskScriptResponseValue recursively projects script-shaped objects embedded
// in heterogeneous import/review responses. Converter result structs often
// repeat the same script under result.script, so masking only the top-level
// convenience field would leave a second disclosure path.
var maskScriptResponseValue = importreview.MaskScriptResponseValue

var projectEmbeddedScriptNodes = importreview.ProjectEmbeddedScriptNodes

// maskScriptJSONObject routes script-shaped values through the typed projection
// boundary. In particular, maskScriptSecrets decodes body_encoding=base64,
// projects the wire bytes, and re-encodes them; sending an encoded body directly
// through the generic free-text projector would corrupt it as a high-entropy
// token while leaving the base64 encoding marker in place.
var maskScriptJSONObject = importreview.MaskScriptJSONObject

var isScriptJSONObject = importreview.IsScriptJSONObject

// Restored LR bodies are operator-supplied protected payloads regardless of
// whether their field names happen to match the credential classifier.
var maskRestoredImportBodies = importreview.MaskRestoredImportBodies

var sourceIdentifierPattern = importreview.SourceIdentifierPattern
var sourceFileNamePattern = importreview.SourceFileNamePattern

// isSourceLocationProvenance reports an imported action's source file name or
// C function/action name. Long descriptive names (Action_Store_08_Activation
// _Phone17.c) look token-shaped to the bare-token heuristic, which masked them
// as [REDACTED].c; a value of that exact shape names a source location, not a
// credential.
var isSourceLocationProvenance = importreview.IsSourceLocationProvenance
