package api

// Script response projection and masked-write reconciliation live here rather
// than beside execution-time masking. Runtime scripts must retain credentials;
// viewer, export, federation, and source-control representations must not.

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/javarunner"
	"github.com/jasonczech/apexload/pkg/scriptdisclosure"
	"github.com/jasonczech/apexload/pkg/scrub"
	"github.com/jasonczech/apexload/pkg/storage"
)

const maskedScriptSecret = scrub.Redacted
const credentialParameterSourceRedactedBlocker = "credential_parameter_source_redacted"
const scriptProjectionResponseGateMetadataKey = "script_projection_response_gate_v1"
const scriptProjectionUnprojectableBodyReason = "unprojectable_credential_body"

var maskedImportHeaderBytes = base64.StdEncoding.EncodeToString([]byte(maskedScriptSecret))

var scriptSecretTemplatePattern = regexp.MustCompile(`\{\{\s*secret\.(` + engine.LogicalSecretNameInputPattern + `)\s*\}\}`)
var scriptPortableTemplatePattern = regexp.MustCompile(`\{\{[^{}\r\n]{1,256}\}\}`)
var scriptPortableReferencePattern = regexp.MustCompile(`^(?:(?:env|data|var|param|file)\.[A-Za-z0-9_.:/@-]+|secret\.` + engine.LogicalSecretNameInputPattern + `)$`)
var scriptPortableRandomPattern = regexp.MustCompile(`^fn\.random\(\s*-?\d+\s*,\s*-?\d+\s*\)$`)
var scriptPortableRandomStringPattern = regexp.MustCompile(`^fn\.random_string\(\s*\d+\s*\)$`)
var redactedXMLMessageIDPattern = regexp.MustCompile(`(?is)(<(?:(?:[\w.-]+):)?messageId\b[^>]*>)\s*\[REDACTED\]\s*(</(?:(?:[\w.-]+):)?messageId\s*>)`)
var xmlMessageIDValuePattern = regexp.MustCompile(`(?is)(<(?:(?:[\w.-]+):)?messageId\b[^>]*>)([^<>]*)(</(?:(?:[\w.-]+):)?messageId\s*>)`)
var portableXMLSecretElementPattern = regexp.MustCompile(`(?is)<(?:[\w.-]+:)?[\w.-]+\b[^>]*>\s*\{\{\s*secret\.` + engine.LogicalSecretNameInputPattern + `\s*\}\}\s*</(?:[\w.-]+:)?[\w.-]+\s*>`)
var portableXMLSecretAttributePattern = regexp.MustCompile(`(?is)(?:[\w.-]+:)?[\w.-]+\s*=\s*(?:"\s*\{\{\s*secret\.` + engine.LogicalSecretNameInputPattern + `\s*\}\}\s*"|'\s*\{\{\s*secret\.` + engine.LogicalSecretNameInputPattern + `\s*\}\}\s*')`)
var portableXMLTemplateElementPattern = regexp.MustCompile(`(?is)<(?:[\w.-]+:)?[\w.-]+\b[^>]*>\s*\{\{[^{}\r\n]{1,256}\}\}\s*</(?:[\w.-]+:)?[\w.-]+\s*>`)
var portableXMLTemplateAttributePattern = regexp.MustCompile(`(?is)(?:[\w.-]+:)?[\w.-]+\s*=\s*(?:"\s*\{\{[^{}\r\n]{1,256}\}\}\s*"|'\s*\{\{[^{}\r\n]{1,256}\}\}\s*')`)

// maskScriptSecrets returns a deep, portable copy of script with inline
// credentials removed. It intentionally preserves explicit secret references
// (for example {{secret.api_token}} and *_secret reference fields) so an export
// describes how to bind a secret without disclosing its value.
func maskScriptSecrets(script *dsl.Script) *dsl.Script {
	if script == nil {
		return nil
	}
	payload, err := json.Marshal(script)
	if err != nil {
		return nil
	}
	var working dsl.Script
	if err := json.Unmarshal(payload, &working); err != nil {
		return nil
	}
	if err := maskImportHeaderFidelityBytes(&working); err != nil {
		return nil
	}
	// Import-save attestations are short-lived capabilities for the authorized
	// editable draft only. Review payloads, stored reads, exports, and other
	// disclosure-safe projections must never duplicate them.
	stripImportDraftAttestation(&working)
	// Source commit fences are private, server-owned reconciliation state.
	// They contain no source values, but exposing them would let clients infer
	// pending publication state and would invite accidental round-trip edits.
	stripProtectedImportSourceCommitFence(&working)
	maskRawBrunoRuntimeArtifactReference(&working)
	maskJavaVuserVMParameterValues(&working)
	maskScriptDataSourceCredentials(&working)
	protectedBodies, ok := projectScriptRequestBodies(&working)
	if !ok {
		return nil
	}
	payload, err = json.Marshal(&working)
	if err != nil {
		return nil
	}
	var raw any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil
	}
	projected := projectScriptNode(raw, scriptProjectionContext{})
	projectedPayload, err := json.Marshal(projected)
	if err != nil {
		return nil
	}
	var masked dsl.Script
	if err := json.Unmarshal(projectedPayload, &masked); err != nil {
		return nil
	}
	restored := 0
	if err := dslwalk.Walk(&masked, func(ref dslwalk.SurfaceRef) error {
		body, exists := protectedBodies[ref.Path]
		if !exists {
			return nil
		}
		setImportCommitSurfaceBody(ref, body)
		restored++
		return nil
	}); err != nil || restored != len(protectedBodies) {
		return nil
	}
	restoreBrunoNativeCompactProvenance(&masked, script)
	projectedScript, err := scriptdisclosure.ProjectWithCredentialSource(&masked, script)
	if err != nil {
		return nil
	}
	restoreBrunoNativeCompactProvenance(projectedScript, script)
	restoreProjectedJavaVuserApprovalEvidence(projectedScript, script)
	return projectedScript
}

// maskImportHeaderFidelityBytes projects the exact, inert source bytes retained
// for runtime-incompatible headers and multipart fields. The durable execution
// copy keeps those bytes behind import_review_required, but ordinary reads,
// exports, and review responses must not disclose either an authored name or
// value. The historical function name is retained because header fidelity was
// the first consumer of this shared byte envelope.
func maskImportHeaderFidelityBytes(script *dsl.Script) error {
	if script == nil {
		return nil
	}
	return dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		metadata := ref.Metadata()
		if marker, ok := metadata[dsl.ImportHeaderFidelityMetadataKey].(map[string]any); ok {
			maskHeaderFidelityOccurrenceBytes(marker["omitted_occurrences"])
		}
		if marker, ok := metadata[dsl.ImportMultipartFidelityMetadataKey].(map[string]any); ok {
			maskHeaderFidelityOccurrenceBytes(marker["omitted_fields"])
		}
		return nil
	})
}

func maskHeaderFidelityOccurrenceBytes(raw any) {
	maskOccurrence := func(occurrence map[string]any) {
		if occurrence == nil {
			return
		}
		if _, exists := occurrence[dsl.ImportHeaderFidelityPreservedNameBase64Key]; exists {
			occurrence[dsl.ImportHeaderFidelityPreservedNameBase64Key] = maskedImportHeaderBytes
		}
		if _, exists := occurrence[dsl.ImportHeaderFidelityPreservedValueBase64Key]; exists {
			occurrence[dsl.ImportHeaderFidelityPreservedValueBase64Key] = maskedImportHeaderBytes
		}
	}
	switch occurrences := raw.(type) {
	case []map[string]any:
		for _, occurrence := range occurrences {
			maskOccurrence(occurrence)
		}
	case []any:
		for _, rawOccurrence := range occurrences {
			if occurrence, ok := rawOccurrence.(map[string]any); ok {
				maskOccurrence(occurrence)
			}
		}
	}
}

// importHeaderFidelityProjectionChanged lets the protected import-draft
// attestation recognize credentials that exist only in inert, gated header or
// multipart fidelity envelopes. Secret-binding discovery intentionally skips
// gated actions, so without this comparison a legacy create would treat the
// editable import draft as untrusted and persist its response mask instead of
// the exact bytes. The historical function name remains for compatibility.
func importHeaderFidelityProjectionChanged(raw, projected *dsl.Script) bool {
	rawSnapshot, rawOK := importHeaderFidelitySnapshot(raw)
	projectedSnapshot, projectedOK := importHeaderFidelitySnapshot(projected)
	return !rawOK || !projectedOK || !reflect.DeepEqual(rawSnapshot, projectedSnapshot)
}

func importHeaderFidelitySnapshot(script *dsl.Script) ([]string, bool) {
	if script == nil {
		return nil, true
	}
	var snapshot []string
	err := dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		for _, envelope := range []struct {
			metadataKey   string
			collectionKey string
		}{
			{
				metadataKey:   dsl.ImportHeaderFidelityMetadataKey,
				collectionKey: "omitted_occurrences",
			},
			{
				metadataKey:   dsl.ImportMultipartFidelityMetadataKey,
				collectionKey: "omitted_fields",
			},
		} {
			marker, ok := ref.Metadata()[envelope.metadataKey].(map[string]any)
			if !ok {
				continue
			}
			occurrences, ok := importFidelityOccurrencesJSON(
				marker,
				envelope.collectionKey,
			)
			if !ok {
				return fmt.Errorf("decode %s occurrences", envelope.metadataKey)
			}
			for index, occurrence := range occurrences {
				for _, key := range []string{
					dsl.ImportHeaderFidelityPreservedNameBase64Key,
					dsl.ImportHeaderFidelityPreservedValueBase64Key,
				} {
					value, exists := occurrence[key]
					if !exists {
						continue
					}
					payload, err := json.Marshal(value)
					if err != nil {
						return err
					}
					snapshot = append(
						snapshot,
						fmt.Sprintf(
							"%s\x00%s\x00%d\x00%s\x00%s",
							ref.Path,
							envelope.metadataKey,
							index,
							key,
							payload,
						),
					)
				}
			}
		}
		return nil
	})
	return snapshot, err == nil
}

const (
	maxScriptProjectionDataSourceBytes = 8 << 20
	maxScriptProjectionDataSourceCells = 100_000
)

// maskScriptDataSourceCredentials projects credential-bearing list parameters
// and inline CSV/DAT cells without changing the durable runtime copy. Generic
// text scrubbing cannot infer that a short value is sensitive merely because a
// neutral source variable later feeds Authorization. Importer provenance and
// column semantics close that response-only leak.
func maskScriptDataSourceCredentials(script *dsl.Script) {
	if script == nil {
		return
	}
	credentialParameterNames := make(map[string]struct{})
	if script.Metadata != nil && script.Metadata.Import != nil {
		for _, name := range importPreservedStringSlice(
			script.Metadata.Import.Preserved[dsl.ImportPreservedCredentialParameterNamesKey],
		) {
			if name = canonicalCredentialParameterName(name); name != "" {
				credentialParameterNames[name] = struct{}{}
			}
		}
	}
	sensitiveColumns := make(map[string][]string)
	for index := range script.Parameters {
		parameter := &script.Parameters[index]
		_, provenCredentialSource := credentialParameterNames[canonicalCredentialParameterName(parameter.Name)]
		sensitive := provenCredentialSource ||
			isSensitiveScriptKey(parameter.Name) ||
			isSensitiveScriptKey(parameter.Column)
		if sensitive {
			for valueIndex, value := range parameter.Values {
				if value == "" || isPortableTemplateBinding(value) ||
					strings.Contains(value, maskedScriptSecret) {
					continue
				}
				parameter.Values[valueIndex] = projectSensitiveScriptValue(value)
			}
		}
		sourceName := strings.ToLower(strings.TrimSpace(parameter.DataSource))
		if sourceName == "" || !sensitive {
			continue
		}
		sensitiveColumns[sourceName] = append(
			sensitiveColumns[sourceName],
			strings.TrimSpace(parameter.Column),
		)
	}
	// Recorder parameterization uses direct {{data.recorded_inputs.column}}
	// references and therefore has no dsl.Parameter object from which the
	// projection can infer sensitive columns. Its server-authored provenance
	// carries the exact column names instead.
	if script.Metadata != nil && script.Metadata.Import != nil {
		if columns := importPreservedStringSlice(
			script.Metadata.Import.Preserved["recorded_input_secret_columns"],
		); len(columns) > 0 {
			sensitiveColumns["recorded_inputs"] = append(
				sensitiveColumns["recorded_inputs"],
				columns...,
			)
		}
	}
	if len(script.DataSources) == 0 {
		return
	}
	for index := range script.DataSources {
		source := &script.DataSources[index]
		requiredColumns := sensitiveColumns[strings.ToLower(strings.TrimSpace(source.Name))]
		sourceSensitive := isSensitiveScriptKey(source.Name)
		if strings.TrimSpace(source.InlineData) == "" {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(source.Encoding), "base64") {
			if sourceSensitive || len(requiredColumns) > 0 {
				source.InlineData = maskedScriptSecret
			}
			continue
		}
		projected, sensitive, ok := projectScriptDataSourceTable(
			*source,
			requiredColumns,
			sourceSensitive,
		)
		if ok {
			if sensitive {
				source.InlineData = projected
			}
			continue
		}
		// A source whose schema already proves credential use must fail closed
		// when its table cannot be parsed within the response-projection budget.
		if sourceSensitive || len(requiredColumns) > 0 {
			source.InlineData = maskedScriptSecret
		}
	}
}

func canonicalCredentialParameterName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func importPreservedStringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				result = append(result, text)
			}
		}
		return result
	default:
		return nil
	}
}

// sanitizePersistentImportReviewSurfaces removes credential material from
// non-executable import provenance before any durable write. Credential-
// preserving imports bypass executable projection by design, so this boundary
// must be applied independently of maskScriptSecrets.
func sanitizePersistentImportReviewSurfaces(script *dsl.Script) error {
	if script == nil {
		return nil
	}
	if script.Metadata != nil && script.Metadata.Import != nil {
		for key, value := range script.Metadata.Import.Preserved {
			switch {
			case key == javaVuserApprovalEvidenceKey:
				// This is server-authored governance evidence, not executable
				// session material. The loop has already removed its wrapper
				// key from projection context, so preserve only the complete,
				// strictly validated UUID/SHA-256 envelope explicitly.
				validated := projectJavaVuserApprovalEvidence(value)
				if len(validated) == 2 {
					script.Metadata.Import.Preserved[key] = validated
				} else {
					delete(script.Metadata.Import.Preserved, key)
				}
			case isOpaqueImportSourcePayloadKey(key):
				// These bounded, server-authored payloads are the only preserved
				// metadata fields allowed to retain exact source bytes. They are
				// required to finish a durably gated conversion and are masked
				// independently on every ordinary response projection.
				continue
			case isExecutableImportPreservedPayloadKey(key):
				// Browser recorded-only resource filtering consumes these exact
				// URLs at runtime. They may contain the same userinfo or signed
				// query credentials already present in the operator's imported
				// recording, so the durable execution copy must retain them.
				// maskScriptSecrets projects them independently on every read,
				// export, receipt, and review surface.
				continue
			case isReviewEvidenceSubtreeKey(key):
				script.Metadata.Import.Preserved[key] = sanitizeImportReviewEvidence(value)
			default:
				// Import provenance is not an executable credential surface.
				// Even a trusted import must not turn arbitrary caller-controlled
				// metadata into a durable raw-source side channel. Preserve
				// ordinary enums, IDs, and mappings while applying the same
				// disclosure projection used for opaque metadata responses.
				script.Metadata.Import.Preserved[key] = projectScriptNode(
					value,
					scriptProjectionContext{
						key:       key,
						opaqueMap: true,
						metadata:  true,
					},
				)
			}
		}
	}
	return dslwalk.WalkActions(script, func(ref dslwalk.ActionRef) error {
		if ref.Action == nil {
			return nil
		}
		switch strings.ToLower(strings.TrimSpace(ref.Action.Type)) {
		case "source_review", "review_required":
		default:
			return nil
		}
		// Runtime-incompatible header and multipart bytes are durable execution
		// evidence, not source-review prose. Preserve only these already-gated
		// envelopes while the rest of a review-only action is disclosure-
		// sanitized; response projection masks them independently.
		fidelityEnvelopes := make(map[string]any, 2)
		if ref.Action.Metadata != nil {
			for _, key := range []string{
				dsl.ImportHeaderFidelityMetadataKey,
				dsl.ImportMultipartFidelityMetadataKey,
			} {
				if value, exists := ref.Action.Metadata[key]; exists {
					fidelityEnvelopes[key] = value
				}
			}
		}
		sanitized := sanitizeImportReviewEvidence(ref.Action)
		payload, err := json.Marshal(sanitized)
		if err != nil {
			return fmt.Errorf("sanitize %s import review action: %w", ref.Path, err)
		}
		var action dsl.Action
		if err := json.Unmarshal(payload, &action); err != nil {
			return fmt.Errorf("decode sanitized %s import review action: %w", ref.Path, err)
		}
		if len(fidelityEnvelopes) > 0 {
			if action.Metadata == nil {
				action.Metadata = make(map[string]any)
			}
			for key, value := range fidelityEnvelopes {
				action.Metadata[key] = value
			}
		}
		*ref.Action = action
		return nil
	})
}

func isExecutableImportPreservedPayloadKey(key string) bool {
	return normalizeScriptFieldName(key) == "recordedresourceurls"
}

func projectScriptDataSourceTable(
	source dsl.DataSource,
	requiredColumns []string,
	sourceSensitive bool,
) (string, bool, bool) {
	if len(source.InlineData) > maxScriptProjectionDataSourceBytes {
		return "", false, false
	}
	delimiter := dsl.ResolveDataSourceDelimiter(source, source.InlineData)
	reader := csv.NewReader(strings.NewReader(source.InlineData))
	reader.Comma = delimiter
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true

	records := make([][]string, 0, 16)
	cellCount := 0
	maxColumns := 0
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil ||
			len(record) > maxScriptProjectionDataSourceCells-cellCount {
			return "", false, false
		}
		cellCount += len(record)
		if len(record) > maxColumns {
			maxColumns = len(record)
		}
		records = append(records, record)
	}
	if len(records) == 0 || maxColumns == 0 {
		return source.InlineData, false, true
	}

	columns := append([]string(nil), source.Columns...)
	dataStart := 0
	if source.FirstRowHeader {
		columns = records[0]
		dataStart = 1
	} else if len(columns) == 0 &&
		scriptDataSourceRecordLooksLikeCredentialHeader(records[0], requiredColumns) {
		// Older imported DAT sources did not always set first_row_header even
		// though the first record carried names. Treat the row as a header only
		// when it contains a credential semantic name, avoiding broad guessing.
		columns = records[0]
		dataStart = 1
	}

	sensitiveIndexes := make([]bool, maxColumns)
	for columnIndex, column := range columns {
		if columnIndex >= len(sensitiveIndexes) {
			break
		}
		if isSensitiveScriptKey(strings.TrimPrefix(column, "\ufeff")) ||
			scriptDataSourceColumnRequired(column, requiredColumns) {
			sensitiveIndexes[columnIndex] = true
		}
	}
	knownSensitiveColumn := false
	for _, sensitive := range sensitiveIndexes {
		knownSensitiveColumn = knownSensitiveColumn || sensitive
	}
	if !knownSensitiveColumn && (sourceSensitive || len(requiredColumns) > 0) {
		// The source/parameter contract proves credential use but the authored
		// column shape is absent or ambiguous. Redact every data cell rather
		// than disclose a credential under an unhelpful column alias.
		for index := range sensitiveIndexes {
			sensitiveIndexes[index] = true
		}
		knownSensitiveColumn = true
	}
	if !knownSensitiveColumn {
		return source.InlineData, false, true
	}

	for rowIndex := dataStart; rowIndex < len(records); rowIndex++ {
		for columnIndex, sensitive := range sensitiveIndexes {
			if !sensitive || columnIndex >= len(records[rowIndex]) {
				continue
			}
			value := records[rowIndex][columnIndex]
			if value == "" || isPortableTemplateBinding(value) ||
				strings.Contains(value, maskedScriptSecret) {
				continue
			}
			records[rowIndex][columnIndex] = maskedScriptSecret
		}
	}

	var output bytes.Buffer
	writer := csv.NewWriter(&output)
	writer.Comma = delimiter
	writer.UseCRLF = strings.Contains(source.InlineData, "\r\n")
	for _, record := range records {
		if err := writer.Write(record); err != nil {
			return "", false, false
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return "", false, false
	}
	projected := output.String()
	if !strings.HasSuffix(source.InlineData, "\n") {
		projected = strings.TrimSuffix(projected, "\n")
		if writer.UseCRLF {
			projected = strings.TrimSuffix(projected, "\r")
		}
	}
	return projected, true, true
}

func scriptDataSourceRecordLooksLikeCredentialHeader(record, requiredColumns []string) bool {
	for _, column := range record {
		column = strings.TrimPrefix(column, "\ufeff")
		if isSensitiveScriptKey(column) ||
			scriptDataSourceColumnRequired(column, requiredColumns) {
			return true
		}
	}
	return false
}

func scriptDataSourceColumnRequired(column string, requiredColumns []string) bool {
	column = strings.TrimSpace(strings.TrimPrefix(column, "\ufeff"))
	if column == "" {
		return false
	}
	for _, required := range requiredColumns {
		if required != "" && strings.EqualFold(column, strings.TrimSpace(required)) {
			return true
		}
	}
	return false
}

func maskRawBrunoRuntimeArtifactReference(script *dsl.Script) {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil {
		return
	}
	source, _ := script.Metadata.Import.Preserved[dsl.ImportPreservedRuntimeArtifactKey].(string)
	if !strings.EqualFold(strings.TrimSpace(source), "raw") {
		return
	}
	// These values identify the protected raw source object and its raw
	// fingerprints. They are required by the runtime, but ordinary script reads
	// must not disclose them. Masked-write reconciliation restores unchanged
	// values from the durable script.
	script.Config.Bruno.BundleRef = maskedScriptSecret
	script.Config.Bruno.BundleSHA256 = maskedScriptSecret
	script.Config.Bruno.ManifestSHA256 = maskedScriptSecret
}

// maskJavaVuserVMParameterValues is a read/export defense for legacy rows and
// any response assembled before current DSL validation runs. JVM arguments are
// observable in the operating-system process table, so an unsafe value must
// never be copied back through an API projection. Safe JVM tuning flags and
// ordinary configuration values remain visible and round-trip unchanged.
func maskJavaVuserVMParameterValues(script *dsl.Script) {
	if script == nil {
		return
	}
	for index, parameter := range script.Config.JavaVuser.AdditionalVMParameters {
		if javarunner.ContainsSensitiveVMParameterValue(parameter) {
			script.Config.JavaVuser.AdditionalVMParameters[index] = maskedScriptSecret
		}
	}
}

// validateJavaVMParameterDisclosureSafety is the write-side companion to the
// legacy response masker above. JVM arguments intentionally do not support
// {{secret.*}} resolution because argv is process-observable. Reject unsafe
// values with value-free diagnostics before generic secret-binding discovery
// can offer an invalid project-secret binding for this surface.
func validateJavaVMParameterDisclosureSafety(script *dsl.Script) error {
	return validateJavaVMParameterSafety(script, false)
}

func validateJavaVMParameterSafety(script *dsl.Script, allowPreservedCredentials bool) error {
	if script == nil {
		return nil
	}
	issues := javarunner.ValidateVMParameters(script.Config.JavaVuser.AdditionalVMParameters)
	if allowPreservedCredentials {
		issues = javarunner.ValidateVMParametersForExecution(
			script.Config.JavaVuser.AdditionalVMParameters,
		)
	}
	if len(issues) == 0 {
		return nil
	}
	issue := issues[0]
	if issue.Index < 0 {
		return fmt.Errorf(
			"config.java_vuser.additional_vm_parameters is invalid (%s) and cannot be stored or injected securely; correct or remove the argument list",
			issue.Category,
		)
	}
	return fmt.Errorf(
		"config.java_vuser.additional_vm_parameters[%d] is invalid (%s) and cannot be stored or injected securely; use a non-sensitive governed option or remove the argument",
		issue.Index,
		issue.Category,
	)
}

// projectScriptRequestBodies keeps every request body out of the generic
// free-text projector. Shared structured codecs preserve source bytes and
// rewrite only credential spans; unrecognized raw/file bytes pass through
// exactly. Unsafe typed or mixed content fails closed.
func projectScriptRequestBodies(script *dsl.Script) (map[string]string, bool) {
	protected := make(map[string]string)
	err := dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		body, ok, err := decodeImportCommitProjectionBody(ref)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		projected, result, err := credentialpolicy.ProjectRequestBody(
			body.wire,
			importCommitSurfaceContentType(ref),
			credentialpolicy.Options{
				Mode: credentialpolicy.Redact,
				Source: credentialpolicy.SourceRef{
					Format:  "script_projection",
					Path:    ref.Path,
					Pointer: "/body",
				},
				RequestKey:         ref.Path,
				TrustedWholeValues: portableBodyTemplateValues(body.text()),
				TargetField:        "body",
			},
		)
		for index := range result.Requirements {
			result.Requirements[index].Value = ""
		}
		if err != nil || result.Disposition == credentialpolicy.Rejected || projected == nil {
			// A read/export projection must remain a structurally valid script
			// even when malformed credential-bearing wire bytes cannot be
			// selectively rewritten. Replace only the response copy with a
			// whole-body sentinel and mark that response copy as non-executable.
			// The marker lets masked-write reconciliation remove this transient
			// gate and restore an unchanged authoritative body; it is never
			// attached to the durable exact script.
			projected = []byte(maskedScriptSecret)
			applyScriptProjectionResponseGate(ref)
		}
		projected = projectMaskedXMLMessageIDs(projected)
		protected[ref.Path] = body.encode(projected)
		setImportCommitSurfaceBody(ref, "")
		return nil
	})
	if err != nil {
		return nil, false
	}
	return protected, true
}

func applyScriptProjectionResponseGate(ref dslwalk.SurfaceRef) {
	metadata := make(map[string]any, len(ref.Metadata())+4)
	for key, value := range ref.Metadata() {
		metadata[key] = value
	}
	if blocker, _ := metadata["execution_blocker"].(string); strings.TrimSpace(blocker) == "" {
		metadata["execution_blocker"] = "import_review_required"
	}
	metadata["execution_blockers"] = appendUniqueStrings(
		executionReviewBlockerCodes(metadata),
		"import_review_required",
	)
	if _, exists := metadata["import_review"]; !exists {
		metadata["import_review"] = map[string]any{
			"version":      1,
			"source":       "script_projection",
			"reason_codes": []string{scriptProjectionUnprojectableBodyReason},
			"source_refs":  []string{ref.Path + ".body"},
			"resolution":   "replace_behavior_and_remove_gate",
		}
	}
	// This marker distinguishes a disclosure-only fail-closed gate from source
	// behavior that was already durably review-gated. It contains no source
	// value and is removed when an unchanged masked response is reconciled with
	// its authoritative stored script.
	metadata[scriptProjectionResponseGateMetadataKey] = map[string]any{"version": 1}
	ref.SetMetadata(metadata)
}

func projectMaskedXMLMessageIDs(payload []byte) []byte {
	value := string(payload)
	matches := xmlMessageIDValuePattern.FindAllStringSubmatchIndex(value, -1)
	if len(matches) == 0 {
		return payload
	}
	var builder strings.Builder
	builder.Grow(len(value))
	cursor := 0
	changed := false
	for _, match := range matches {
		if len(match) != 8 {
			continue
		}
		content := value[match[4]:match[5]]
		if scrubScriptText(content, false) != maskedScriptSecret {
			continue
		}
		builder.WriteString(value[cursor:match[4]])
		builder.WriteString("{{fn.uuid()}}")
		cursor = match[5]
		changed = true
	}
	if !changed {
		return payload
	}
	builder.WriteString(value[cursor:])
	return []byte(builder.String())
}

func maskStoredScript(stored *StoredScript) *StoredScript {
	if stored == nil {
		return nil
	}
	masked := cloneStoredScriptForReadLocked(stored)
	masked.Script = maskScriptSecrets(masked.Script)
	masked.Name = disclosureSafeScriptName(stored.Name, stored.Script)
	return masked
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
	masked.Draft = maskScriptSecrets(masked.Draft)
	masked.Name = disclosureSafeScriptName(session.Name, session.Draft)
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
	projected, _ := projectScriptNode(raw, scriptProjectionContext{opaqueMap: true}).(map[string]any)
	return projected
}

type scriptProjectionContext struct {
	key            string
	opaqueMap      bool
	headers        bool
	headerList     bool
	secretBindings bool
	metadata       bool
}

func projectScriptNode(value any, ctx scriptProjectionContext) any {
	switch typed := value.(type) {
	case map[string]any:
		if ctx.headerList {
			return projectHeaderEntry(typed)
		}
		credentialValue := scriptMapHasCredentialValue(typed)
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			if ctx.metadata &&
				key == dsl.ImportPreservedCredentialTargetsMetadataKey {
				// These are server-authored structural locators, not source
				// values. Masking "/password" as if it were a password creates
				// phantom binding requirements and makes the runtime locator
				// unusable after a masked read-modify-write.
				out[key] = projectImportCredentialTargetLocators(child)
				continue
			}
			// These import-only payloads retain exact source text in the durable
			// destination so the operator can finish a gated conversion without
			// re-importing or re-entering embedded credentials. They are never
			// executable and never cross an ordinary read/export boundary.
			if ctx.metadata && isOpaqueImportSourcePayloadKey(key) {
				out[key] = maskOpaqueImportSourcePayload(child)
				continue
			}
			// Java Vuser approval provenance is a server-authored governance
			// envelope. Its authoring-session UUID is an identifier, not runtime
			// session material. Preserve only the two exact, validated scalar
			// fields here; ordinary session_id keys continue through the normal
			// credential projection and remain masked.
			if key == javaVuserApprovalEvidenceKey {
				out[key] = projectJavaVuserApprovalEvidence(child)
				continue
			}
			if ctx.secretBindings {
				if text, ok := child.(string); ok && isPortableSecretReference(text) {
					out[key] = text
				} else {
					// Bruno forwards every map value as executable credential
					// material regardless of its caller-controlled alias key.
					// Only an exact governed reference may survive projection.
					out[key] = maskedScriptSecret
				}
				continue
			}
			// Proxy usernames are authentication material supplied alongside a
			// proxy password, not ordinary application identity fields. Preserve
			// them in the durable imported execution copy but mask them on stored
			// reads, result/review projections, and exports.
			if normalizeScriptFieldName(ctx.key) == "proxy" &&
				normalizeScriptFieldName(key) == "username" {
				if text, ok := child.(string); ok {
					switch {
					case strings.TrimSpace(text) == "":
						out[key] = text
					case isPortableTemplateBinding(text):
						out[key] = text
					default:
						out[key] = projectSensitiveScriptValue(text)
					}
				} else {
					out[key] = maskedScriptSecret
				}
				continue
			}
			// SOAPAction and operation are executable, adapter-defined strings.
			// They are not credential-named fields, but imported values may
			// contain URL userinfo, signed query parameters, or opaque
			// credential assignments. Treat the action as URL-shaped and both
			// fields as free text while retaining ordinary operation names.
			if normalizeScriptFieldName(ctx.key) == "soap" {
				switch normalizeScriptFieldName(key) {
				case "action":
					if text, ok := child.(string); ok {
						out[key] = maskScriptURL(text)
					} else {
						out[key] = projectScriptNode(
							child,
							scriptProjectionContext{key: key, opaqueMap: true},
						)
					}
					continue
				case "operation":
					if text, ok := child.(string); ok {
						out[key] = scrubScriptText(text, false)
					} else {
						out[key] = projectScriptNode(
							child,
							scriptProjectionContext{key: key, opaqueMap: true},
						)
					}
					continue
				}
			}
			// Import review evidence is client-extensible and frequently embeds
			// raw source snippets or sibling name/value pairs that the ordinary
			// script projection cannot associate. Apply the import-specific,
			// recursive disclosure boundary before this metadata can be persisted.
			if isReviewEvidenceSubtreeKey(key) {
				out[key] = sanitizeImportReviewEvidence(child)
				continue
			}
			if credentialValue && strings.EqualFold(key, "value") {
				if text, ok := child.(string); ok && isPortableTemplateBinding(text) {
					out[key] = text
				} else if text, ok := child.(string); ok {
					out[key] = projectSensitiveScriptValue(text)
				} else {
					out[key] = maskedScriptSecret
				}
				continue
			}
			if credentialValue && strings.EqualFold(key, "values") {
				values, ok := child.([]any)
				if !ok {
					out[key] = maskedScriptSecret
					continue
				}
				projected := make([]any, len(values))
				for index, value := range values {
					if text, ok := value.(string); ok && isPortableTemplateBinding(text) {
						projected[index] = text
					} else if text, ok := value.(string); ok {
						projected[index] = projectSensitiveScriptValue(text)
					} else {
						projected[index] = maskedScriptSecret
					}
				}
				out[key] = projected
				continue
			}
			childCtx := scriptProjectionContext{
				key:            key,
				opaqueMap:      ctx.opaqueMap || isOpaqueScriptMap(key),
				headers:        isScriptHeaderMap(key),
				headerList:     isScriptHeaderList(key),
				secretBindings: isScriptSecretBindingsMap(key),
				metadata:       ctx.metadata || normalizeScriptFieldName(key) == "metadata",
			}
			if ctx.headers {
				out[key] = projectHeaderValue(key, child)
				continue
			}
			out[key] = projectScriptNode(child, childCtx)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			out[i] = projectScriptNode(child, ctx)
		}
		return out
	case string:
		return projectScriptString(typed, ctx)
	default:
		if isSensitiveScriptKey(ctx.key) && !isSecretReferenceKey(ctx.key) {
			return maskedScriptSecret
		}
		return value
	}
}

func projectImportCredentialTargetLocators(value any) any {
	payload, err := json.Marshal(value)
	if err != nil || len(payload) > 256<<10 {
		return []any{}
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var targets []credentialpolicy.TargetRef
	if err := decoder.Decode(&targets); err != nil {
		return []any{}
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return []any{}
	}
	for _, target := range targets {
		if !validImportCredentialLocatorText(target.Field, 1024, false) ||
			!validImportCredentialLocatorText(target.Pointer, 4096, true) ||
			!validImportCredentialLocatorText(target.RequestKey, 1024, true) ||
			target.Index < 0 || target.Occurrence < 0 {
			return []any{}
		}
	}
	return value
}

func validImportCredentialLocatorText(value string, maxBytes int, allowEmpty bool) bool {
	if (!allowEmpty && strings.TrimSpace(value) == "") || len(value) > maxBytes {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func isOpaqueImportSourcePayloadKey(key string) bool {
	switch normalizeScriptFieldName(key) {
	case "jsr223sources", "k6gatedsource", "postmaneventsources",
		"credentialtraversalsourcesnapshotbase64", "devwebgatedsource",
		"jmeterjdbcsources", "enterpriseunsupportedproxysource",
		normalizeScriptFieldName(dsl.ImportPreservedEnterpriseJavaVMSourceKey),
		normalizeScriptFieldName(dsl.ImportPreservedEnterpriseJavaJDBCSourceKey):
		return true
	default:
		return false
	}
}

func maskOpaqueImportSourcePayload(value any) any {
	switch typed := value.(type) {
	case []any:
		masked := make([]any, len(typed))
		for index := range typed {
			masked[index] = maskOpaqueImportSourcePayload(typed[index])
		}
		return masked
	case map[string]any:
		masked := make(map[string]any, len(typed))
		for key, child := range typed {
			masked[key] = maskOpaqueImportSourcePayload(child)
		}
		return masked
	case nil:
		return nil
	default:
		return maskedScriptSecret
	}
}

func projectJavaVuserApprovalEvidence(value any) map[string]any {
	evidence, ok := value.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	out := make(map[string]any, 2)
	sessionID, sessionOK := evidence["session_id"].(string)
	sessionID = strings.TrimSpace(sessionID)
	if sessionOK {
		if parsed, err := uuid.Parse(sessionID); err == nil &&
			parsed.String() == sessionID {
			out["session_id"] = sessionID
		}
	}
	contentHash, hashOK := evidence["content_hash"].(string)
	contentHash = strings.TrimSpace(contentHash)
	if hashOK && isCanonicalSHA256(contentHash) {
		out["content_hash"] = contentHash
	}
	return out
}

// restoreProjectedJavaVuserApprovalEvidence repairs the single governance
// envelope that the final generic disclosure pass necessarily sees as opaque
// metadata. Only a complete canonical UUID/SHA-256 pair is copied, and only
// for a Java Vuser script whose executable surfaces require this approval.
// Ordinary session_id fields remain irreversibly projected.
func restoreProjectedJavaVuserApprovalEvidence(projected, source *dsl.Script) {
	if projected == nil || source == nil ||
		!scriptRequiresJavaVuserAuthoringApproval(source) ||
		source.Metadata == nil || source.Metadata.Import == nil {
		return
	}
	validated := projectJavaVuserApprovalEvidence(
		source.Metadata.Import.Preserved[javaVuserApprovalEvidenceKey],
	)
	if len(validated) != 2 {
		clearJavaVuserAuthoringApproval(projected)
		return
	}
	sessionID, sessionOK := validated["session_id"].(string)
	contentHash, hashOK := validated["content_hash"].(string)
	if !sessionOK || !hashOK {
		clearJavaVuserAuthoringApproval(projected)
		return
	}
	writeJavaVuserAuthoringApproval(projected, javaVuserApprovalEvidence{
		sessionID:   sessionID,
		contentHash: contentHash,
	})
}

const sha256HexLength = 64

func isCanonicalSHA256(value string) bool {
	const prefix = "sha256:"
	return strings.HasPrefix(value, prefix) &&
		isCanonicalLowerHexDigest(strings.TrimPrefix(value, prefix), sha256HexLength)
}

func isCanonicalLowerHexDigest(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func scriptMapHasCredentialValue(value map[string]any) bool {
	if _, hasType := value["type"]; !hasType {
		return false
	}
	if metadata, ok := value["metadata"].(map[string]any); ok {
		if marked, _ := metadata["source_marked_secret"].(bool); marked {
			return true
		}
		if masked, _ := metadata["source_masked_secret"].(bool); masked {
			return true
		}
	}
	var hints strings.Builder
	for _, key := range []string{"name", "type", "target", "selector", "object_ref"} {
		if text, ok := value[key].(string); ok {
			hints.WriteByte(' ')
			hints.WriteString(strings.ToLower(text))
		}
	}
	normalized := normalizeScriptFieldName(hints.String())
	for _, marker := range []string{"password", "passwd", "passphrase", "secret", "token", "credential", "authorization", "cookie"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func projectHeaderEntry(entry map[string]any) map[string]any {
	out := make(map[string]any, len(entry))
	name, _ := entry["name"].(string)
	for key, value := range entry {
		if strings.EqualFold(key, "value") {
			out[key] = projectHeaderValue(name, value)
			continue
		}
		out[key] = projectScriptNode(value, scriptProjectionContext{key: key})
	}
	return out
}

func projectHeaderValue(name string, value any) any {
	text, ok := value.(string)
	if !ok {
		return projectScriptNode(value, scriptProjectionContext{key: name, opaqueMap: true})
	}
	if (scrub.Policy{}).SensitiveHeader(name) || isSensitiveScriptKey(name) {
		if isPortableTemplateBinding(text) {
			return text
		}
		return projectSensitiveScriptValue(text)
	}
	if isNonSecretIdentifierHeader(name) {
		return scrub.ValueURL(text, scrub.Policy{})
	}
	return scrubScriptText(text, false)
}

func projectScriptString(value string, ctx scriptProjectionContext) string {
	if value == "" || isPortableTemplateBinding(value) {
		return value
	}
	if ctx.metadata && isPortableImportProvenanceIdentifierKey(ctx.key) {
		return scrub.ValueURL(value, scrub.Policy{})
	}
	if isSecretReferenceKey(ctx.key) && isNamedSecretReference(value) {
		return value
	}
	if isSensitiveScriptKey(ctx.key) {
		return projectSensitiveScriptValue(value)
	}
	if isScriptURLKey(ctx.key) {
		return maskScriptURL(value)
	}
	if ctx.opaqueMap || isScriptFreeTextKey(ctx.key) {
		return scrubScriptText(value, false)
	}
	return value
}

func isNonSecretIdentifierHeader(name string) bool {
	normalized := normalizeScriptFieldName(name)
	if normalized == "" {
		return false
	}
	for _, sensitive := range []string{"authorization", "password", "secret", "token", "cookie", "session", "credential", "apikey", "accesskey", "privatekey"} {
		if strings.Contains(normalized, sensitive) {
			return false
		}
	}
	for _, suffix := range []string{"messageid", "traceid", "correlationid", "requestid"} {
		if strings.HasSuffix(normalized, suffix) {
			return true
		}
	}
	return false
}

func maskScriptURL(value string) string {
	value = scrubScriptText(value, true)
	// ValueURL deliberately preserves the authority. Script configuration is a
	// different trust boundary: URL userinfo is itself a credential and must be
	// removed without normalizing the remainder of an operator-authored URL.
	scheme := strings.Index(value, "://")
	if scheme < 0 {
		return value
	}
	authorityStart := scheme + 3
	authorityEnd := len(value)
	if slash := strings.IndexAny(value[authorityStart:], "/?#"); slash >= 0 {
		authorityEnd = authorityStart + slash
	}
	if at := strings.LastIndex(value[authorityStart:authorityEnd], "@"); at >= 0 {
		at += authorityStart
		value = value[:authorityStart] + maskedScriptSecret + value[at:]
	}
	return value
}

func normalizeScriptFieldName(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	for _, r := range strings.ToLower(value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isSensitiveScriptKey(key string) bool {
	normalized := normalizeScriptFieldName(key)
	// OAuth token_url identifies the remote endpoint; it is not token material.
	// Bruno's persist-session setting is likewise a boolean runtime control, not
	// session material. Keep these explicit exceptions ahead of the shared
	// component-aware vocabulary, which correctly treats standalone token and
	// session components as sensitive in otherwise opaque extension maps.
	if normalized == "tokenurl" || normalized == "persistsessionacrossiterations" ||
		normalized == "credentialpolicy" {
		return false
	}
	// Keep script projection aligned with the shared credential vocabulary used
	// by URL/body/archive scrubbers. Usernames are identity material rather than
	// credentials and must survive the legacy inspection/commit round trip.
	if scrub.SensitiveName(key) {
		return true
	}
	switch normalized {
	case "authorization", "proxyauthorization", "password", "passwd", "passphrase",
		"pwd", "pass",
		"token", "accesstoken", "refreshtoken", "idtoken", "authtoken",
		"apikey", "accesskey", "accesskeyid", "secretkey", "secretaccesskey",
		"awsaccesskeyid", "awssecretaccesskey", "clientsecret", "privatekey",
		"credential", "credentials", "connectionstring", "dsn", "saslpassword",
		"cookie", "setcookie", "session", "sessionid", "csrftoken", "xsrftoken":
		return true
	default:
		for _, suffix := range []string{"authorization", "password", "passwd", "passphrase", "token", "secret", "apikey", "credential", "cookie", "sessionid", "privatekey", "accesskey", "accesskeyid", "secretaccesskey"} {
			if strings.HasSuffix(normalized, suffix) {
				return true
			}
		}
		return false
	}
}

func isSecretReferenceKey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "dsn_secret", "sasl_password_secret", "connection_string_secret", "ca_cert_secret",
		"trust_store_ref", "credential_ref", "storage_ref":
		return true
	default:
		return false
	}
}

func isPortableSecretReference(value string) bool {
	trimmed := strings.TrimSpace(value)
	return scriptSecretTemplatePattern.MatchString(trimmed) && scriptSecretTemplatePattern.FindString(trimmed) == trimmed
}

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

func isPortableTemplateBinding(value string) bool {
	if strings.Contains(value, maskedScriptSecret) {
		return false
	}
	references := portableScriptTemplateReferences(value)
	if len(references) == 0 {
		return false
	}
	remainder := value
	for _, reference := range references {
		remainder = strings.Replace(remainder, reference, "", 1)
	}
	remainder = strings.TrimSpace(remainder)
	switch strings.ToLower(remainder) {
	case "", "bearer", "basic", "digest":
		return true
	default:
		return false
	}
}

// projectSensitiveScriptValue retains only a non-secret authentication scheme
// wrapper. This lets guided binding turn `Bearer [REDACTED]` into
// `Bearer {{secret.NAME}}` without restoring or disclosing the credential.
func projectSensitiveScriptValue(value string) string {
	trimmed := strings.TrimSpace(value)
	for _, scheme := range []string{"Bearer", "Basic", "Digest"} {
		if len(trimmed) > len(scheme) && strings.EqualFold(trimmed[:len(scheme)], scheme) && unicode.IsSpace(rune(trimmed[len(scheme)])) {
			return trimmed[:len(scheme)] + " " + maskedScriptSecret
		}
	}
	return maskedScriptSecret
}

func portableScriptTemplateReferences(value string) []string {
	candidates := scriptPortableTemplatePattern.FindAllString(value, -1)
	references := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if isPortableScriptTemplateReference(candidate) {
			references = append(references, candidate)
		}
	}
	return references
}

func isPortableScriptTemplateReference(value string) bool {
	if len(value) < 5 || !strings.HasPrefix(value, "{{") || !strings.HasSuffix(value, "}}") {
		return false
	}
	expr := strings.TrimSpace(value[2 : len(value)-2])
	if scriptPortableReferencePattern.MatchString(expr) || scriptPortableRandomPattern.MatchString(expr) || scriptPortableRandomStringPattern.MatchString(expr) {
		return true
	}
	switch expr {
	case "fn.uuid()", "fn.uuid", "fn.uuid.v4",
		"fn.timestamp.unix", "fn.timestamp.unix_ms", "fn.timestamp.iso8601", "fn.timestamp.rfc3339",
		"fake.uuid", "fake.uuid()", "fake.email", "fake.email()":
		return true
	default:
		return false
	}
}

func scrubScriptText(value string, urlValue bool) string {
	if value == "" {
		return value
	}
	if !urlValue {
		var structured any
		if json.Unmarshal([]byte(value), &structured) == nil {
			switch structured.(type) {
			case map[string]any, []any:
				projected, result, err := credentialpolicy.ProjectJSON(
					[]byte(value),
					credentialpolicy.Options{
						Mode:               credentialpolicy.Redact,
						Source:             credentialpolicy.SourceRef{Format: "script_projection"},
						RequestKey:         "script_projection",
						TrustedWholeValues: portableBodyTemplateValues(value),
						TargetField:        "text",
					},
				)
				if err == nil && result.Disposition != credentialpolicy.Rejected && projected != nil {
					return string(projected)
				}
			}
		}
	}
	var urlSeparators map[string]string
	if urlValue {
		// A URL has only one formal query delimiter, but imported and generated
		// targets sometimes repeat "?name=value" instead of joining later
		// parameters with '&'. ValueURL correctly treats everything after the
		// first '?' as the query; without this reversible normalization, many
		// protected template bindings become one high-entropy query value and
		// the safe references are redacted together. Expose each later segment
		// to the ordinary parameter scrubber, then restore the source bytes.
		value, urlSeparators = protectAdditionalURLParamSeparators(value)
	}
	value, xmlSecretBindings := protectPortableXMLSecretBindings(value)
	value, templateBindings := protectTemplateBindings(value)
	references := portableScriptTemplateReferences(value)
	protected := value
	referencePlaceholderPrefix := scriptProjectionPlaceholderPrefix(value, "binding")
	for i, reference := range references {
		placeholder := referencePlaceholderPrefix + strconv.Itoa(i) + "Z"
		protected = strings.Replace(protected, reference, placeholder, 1)
	}
	if urlValue {
		protected = scrub.ValueURL(protected, scrub.Policy{})
	} else {
		protected = scrub.Value(protected, scrub.Policy{})
	}
	for i, reference := range references {
		placeholder := referencePlaceholderPrefix + strconv.Itoa(i) + "Z"
		protected = strings.Replace(protected, placeholder, reference, 1)
	}
	for placeholder, binding := range templateBindings {
		protected = strings.ReplaceAll(protected, placeholder, binding)
	}
	for placeholder, binding := range xmlSecretBindings {
		protected = strings.ReplaceAll(protected, placeholder, binding)
	}
	for placeholder, separator := range urlSeparators {
		protected = strings.ReplaceAll(protected, placeholder, separator)
	}
	protected = redactedXMLMessageIDPattern.ReplaceAllString(protected, `${1}{{fn.uuid()}}${2}`)
	return protected
}

func portableBodyTemplateValues(value string) map[string]struct{} {
	decoder := json.NewDecoder(strings.NewReader(value))
	trusted := make(map[string]struct{})
	for _, reference := range portableScriptTemplateReferences(value) {
		trusted[strings.TrimSpace(reference)] = struct{}{}
	}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		text, ok := token.(string)
		if !ok || !isPortableTemplateBinding(text) {
			continue
		}
		trusted[strings.TrimSpace(text)] = struct{}{}
	}
	if len(trusted) == 0 {
		return nil
	}
	return trusted
}

// protectAdditionalURLParamSeparators temporarily represents every '?' after
// the URL's first query delimiter (and every '?' in the fragment) as an
// ordinary '&'-delimited, low-entropy parameter. This lets ValueURL inspect
// each imported parameter independently while the collision-safe marker makes
// restoration exact even when the source already contains marker-like text.
func protectAdditionalURLParamSeparators(value string) (string, map[string]string) {
	if !strings.Contains(value, "?") {
		return value, nil
	}
	placeholderPrefix := scriptProjectionPlaceholderPrefix(value, "urlseparator")
	replacements := map[string]string{}
	var protected strings.Builder
	protected.Grow(len(value))
	queryStarted := false
	inFragment := false
	for offset := 0; offset < len(value); offset++ {
		switch value[offset] {
		case '#':
			inFragment = true
			protected.WriteByte(value[offset])
		case '?':
			if !queryStarted && !inFragment {
				queryStarted = true
				protected.WriteByte(value[offset])
				continue
			}
			placeholder := "&" + placeholderPrefix + strconv.Itoa(len(replacements)) + "Z=safe&"
			replacements[placeholder] = "?"
			protected.WriteString(placeholder)
		default:
			protected.WriteByte(value[offset])
		}
	}
	if len(replacements) == 0 {
		return value, nil
	}
	return protected.String(), replacements
}

// protectPortableXMLSecretBindings preserves already-governed secret
// references when they are the complete value of an XML element or attribute.
// The generic scrubber intentionally replaces the entire value of
// credential-named XML nodes, so protecting only the inner {{secret.*}} token
// would still lose the executable reference.
func protectPortableXMLSecretBindings(value string) (string, map[string]string) {
	protected := value
	replacements := map[string]string{}
	placeholderPrefix := scriptProjectionPlaceholderPrefix(value, "xmlbinding")
	nextPlaceholder := func(binding string) string {
		// The trailing delimiter keeps index 1 from being a prefix of index 10,
		// so restoration remains safe regardless of map iteration order.
		placeholder := placeholderPrefix + strconv.Itoa(len(replacements)) + "Z"
		replacements[placeholder] = binding
		return placeholder
	}
	protected = portableXMLSecretElementPattern.ReplaceAllStringFunc(protected, func(candidate string) string {
		return nextPlaceholder(scrubPortableXMLElementOpeningTag(candidate))
	})
	protected = portableXMLSecretAttributePattern.ReplaceAllStringFunc(protected, nextPlaceholder)
	protectPortable := func(candidate string) string {
		reference := scriptPortableTemplatePattern.FindString(candidate)
		if !isPortableScriptTemplateReference(reference) {
			return candidate
		}
		return nextPlaceholder(scrubPortableXMLElementOpeningTag(candidate))
	}
	protected = portableXMLTemplateElementPattern.ReplaceAllStringFunc(protected, protectPortable)
	protected = portableXMLTemplateAttributePattern.ReplaceAllStringFunc(protected, protectPortable)
	return protected, replacements
}

// scrubPortableXMLElementOpeningTag keeps whole-element template protection from
// hiding plaintext credentials in sibling attributes. Only the element text
// template needs preservation; the opening tag must still pass through the
// ordinary credential scrubber before the complete element is hidden behind a
// placeholder.
func scrubPortableXMLElementOpeningTag(candidate string) string {
	end := strings.IndexByte(candidate, '>')
	if end < 0 {
		return candidate
	}
	return scrub.Value(candidate[:end+1], scrub.Policy{}) + candidate[end+1:]
}

func protectTemplateBindings(value string) (string, map[string]string) {
	replacements := map[string]string{}
	placeholderPrefix := scriptProjectionPlaceholderPrefix(value, "querybinding")
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == '?' || r == '&' || r == '#' })
	for _, part := range parts {
		_, rawValue, ok := strings.Cut(part, "=")
		if !ok || !isPortableTemplateBinding(rawValue) {
			continue
		}
		placeholder := placeholderPrefix + strconv.Itoa(len(replacements)) + "=safe"
		replacements[placeholder] = part
		value = strings.Replace(value, part, placeholder, 1)
	}
	return value, replacements
}

// scriptProjectionPlaceholderPrefix returns a deterministic marker namespace
// that cannot occur in the input. Digest groups are deliberately shorter than
// the generic high-entropy-token threshold so the scrubber cannot redact its
// own temporary markers.
func scriptProjectionPlaceholderPrefix(value, domain string) string {
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(domain+"\x00"+value)))
	var prefix strings.Builder
	prefix.WriteString("apex")
	prefix.WriteString(domain)
	prefix.WriteByte('~')
	for offset := 0; offset < len(digest); offset += 4 {
		prefix.WriteString(digest[offset : offset+4])
		prefix.WriteByte('~')
	}
	result := prefix.String()
	for strings.Contains(value, result) {
		result += "~"
	}
	return result
}

func isNamedSecretReference(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, maskedScriptSecret) || len(value) > 512 {
		return false
	}
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("._:/@-{}", r) {
			continue
		}
		return false
	}
	return true
}

func isScriptURLKey(key string) bool {
	switch normalizeScriptFieldName(key) {
	case "baseurl", "url", "brokerurl", "schemaregistryurl", "server", "origin", "target", "address":
		return true
	default:
		return false
	}
}

func isScriptFreeTextKey(key string) bool {
	switch normalizeScriptFieldName(key) {
	case "body", "value", "expected", "inlinedata", "schema", "objectschema",
		"expression", "left", "right", "runnercommand":
		return true
	default:
		return false
	}
}

func isOpaqueScriptMap(key string) bool {
	switch normalizeScriptFieldName(key) {
	case "params", "metadata", "preserved", "properties", "labels", "options",
		"additionalattributes", "requestmapping", "responsemapping", "namespaces":
		return true
	default:
		return false
	}
}

func isScriptHeaderMap(key string) bool {
	switch normalizeScriptFieldName(key) {
	case "headers", "defaultheaders":
		return true
	default:
		return false
	}
}

func isScriptHeaderList(key string) bool {
	return normalizeScriptFieldName(key) == "headerlist"
}

func isScriptSecretBindingsMap(key string) bool {
	return normalizeScriptFieldName(key) == "secretbindings"
}

// reconcileMaskedScriptSecrets restores only executable sentinels that exactly
// match the projection of a prior value. A new/orphan executable sentinel is
// rejected, preventing a downloaded or federated masked script from executing
// "[REDACTED]" as though it were a credential. Redacted import/source-review
// evidence is retained as non-executable provenance and intentionally skipped.
func reconcileMaskedScriptSecrets(incoming *dsl.Script, prior *dsl.Script) error {
	return reconcileMaskedScriptSecretsWithTrust(incoming, prior, false)
}

func matchesStoredScriptDisclosureProjection(candidate, stored *dsl.Script) bool {
	if candidate == nil || stored == nil {
		return false
	}
	projected := maskScriptSecrets(stored)
	return projected != nil && reflect.DeepEqual(candidate, projected)
}

func reconcileMaskedScriptSecretsWithTrust(
	incoming *dsl.Script,
	prior *dsl.Script,
	trustedImport bool,
) error {
	if incoming == nil {
		return fmt.Errorf("script is required")
	}
	if trustedImport {
		// A trusted importer supplies the authoritative executable candidate,
		// not a disclosure projection of the durable prior. Reconciling it
		// against that prior can silently replace a legitimate new credential
		// whose bytes happen to equal the projection sentinel (or another
		// sentinel-free projected value) with the old credential.
		return nil
	}
	hasRedactedRuntimeData := scriptHasRedactedRuntimeCredentialSource(incoming)
	allowRedactedRuntimeData := hasRedactedRuntimeData &&
		scriptHasExecutionBlockerCode(incoming, credentialParameterSourceRedactedBlocker)
	if prior == nil && hasRedactedRuntimeData && !allowRedactedRuntimeData {
		return fmt.Errorf(
			"script contains redacted runtime data without the required %s execution gate",
			credentialParameterSourceRedactedBlocker,
		)
	}
	incomingJSON, err := scriptAsJSONValue(incoming)
	if err != nil {
		return err
	}
	// Reconciliation cannot be gated on the presence of a [REDACTED] sentinel.
	// scrubScriptText also emits SENTINEL-FREE projections (a redacted
	// <messageId> becomes {{fn.uuid()}}), so a read-modify-write of such a script
	// carries no sentinel at all; skipping reconciliation there persisted the
	// projection over the stored value and destroyed it. Whenever a prior version
	// exists we reconcile, and reconcileProjectedJSON restores only values the
	// client returned byte-identical to the projection.
	hasMaskedSecret := jsonValueContainsExecutableMaskedSecretWithOptions(
		incomingJSON,
		"",
		allowRedactedRuntimeData,
	)
	if prior == nil {
		if hasMaskedSecret {
			return fmt.Errorf("script contains an orphan %s sentinel; bind a secret reference instead", maskedScriptSecret)
		}
		return nil
	}
	rawPrior, err := scriptAsJSONValue(prior)
	if err != nil {
		return err
	}
	projectedPrior, err := scriptAsJSONValue(maskScriptSecrets(prior))
	if err != nil {
		return err
	}
	reconciled, err := reconcileProjectedJSONWithOptions(
		incomingJSON,
		projectedPrior,
		rawPrior,
		"script",
		allowRedactedRuntimeData,
		isCredentialPreservingImport(prior),
	)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(reconciled)
	if err != nil {
		return err
	}
	// Decode into a zero value before replacing the caller's script. Decoding
	// directly into incoming leaves maps/slices that were intentionally removed
	// by reconciliation (for example a response-only review gate) resident
	// because encoding/json does not clear fields omitted from the payload.
	var resolved dsl.Script
	if err := json.Unmarshal(payload, &resolved); err != nil {
		return err
	}
	*incoming = resolved
	return nil
}

func scriptAsJSONValue(script *dsl.Script) (any, error) {
	if script == nil {
		return nil, nil
	}
	payload, err := json.Marshal(script)
	if err != nil {
		return nil, err
	}
	var value any
	if err := json.Unmarshal(payload, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func reconcileProjectedJSON(incoming any, projectedPrior any, rawPrior any, path string) (any, error) {
	return reconcileProjectedJSONWithOptions(incoming, projectedPrior, rawPrior, path, false, false)
}

func reconcileProjectedJSONWithOptions(
	incoming any,
	projectedPrior any,
	rawPrior any,
	path string,
	allowRedactedRuntimeData bool,
	allowLiteralPriorSentinel bool,
) (any, error) {
	switch typed := incoming.(type) {
	case string:
		if allowRedactedRuntimeData && isLegacyRedactedRuntimeSourceTraversalPath(path) {
			projected, projectedOK := projectedPrior.(string)
			raw, rawOK := rawPrior.(string)
			// A script may contain one still-gated imported table alongside a
			// different table whose real values were masked only for this read.
			// Restore that stored value before admitting durable sentinels, or
			// an unrelated gate would overwrite already-resolved data.
			if projectedOK && rawOK && typed == projected && typed != raw {
				return raw, nil
			}
			return typed, nil
		}
		projected, projectedOK := projectedPrior.(string)
		raw, rawOK := rawPrior.(string)
		// An unmodified projection must restore the stored value even when the
		// projection carries no [REDACTED] sentinel. scrubScriptText rewrites a
		// redacted <messageId> into {{fn.uuid()}}, which is sentinel-free; without
		// this branch a masked read-modify-write would persist the placeholder and
		// destroy the recorded correlation id.
		if projectedOK && rawOK && typed == projected {
			if strings.Contains(raw, maskedScriptSecret) && !allowLiteralPriorSentinel {
				return nil, fmt.Errorf("%s contains an orphan %s sentinel; bind a secret reference instead", path, maskedScriptSecret)
			}
			return raw, nil
		}
		if !strings.Contains(typed, maskedScriptSecret) {
			return typed, nil
		}
		return nil, fmt.Errorf("%s contains an orphan %s sentinel; bind a secret reference instead", path, maskedScriptSecret)
	case map[string]any:
		projectedMap, _ := projectedPrior.(map[string]any)
		rawMap, _ := rawPrior.(map[string]any)
		// A disclosure projection may have to replace an unprojectable typed
		// credential body with a whole-body sentinel. The response-only gate
		// attached for that condition is not durable source behavior. When both
		// the incoming value and the authoritative prior projection carry that
		// exact server-generated gate, remove only its transient fields before
		// ordinary field reconciliation. This restores the durable raw body on
		// an unchanged (or non-wire-edited) read-modify-write without letting a
		// caller promote arbitrary review-gated evidence into execution.
		if isTransientScriptProjectionGate(typed, projectedMap, rawMap) {
			typed = withoutTransientScriptProjectionGate(typed)
			projectedMap = withoutTransientScriptProjectionGate(projectedMap)
		}
		priorWasReviewOnly := isNonExecutableReviewAction(projectedMap) ||
			isNonExecutableReviewAction(rawMap) ||
			isDurablyImportReviewGatedAction(projectedMap) ||
			isDurablyImportReviewGatedAction(rawMap)
		incomingReviewOnly := isNonExecutableReviewAction(typed) ||
			isDurablyImportReviewGatedAction(typed)
		if priorWasReviewOnly && !incomingReviewOnly &&
			jsonValueContainsExecutableMaskedSecretWithOptions(typed, path, allowRedactedRuntimeData) {
			return nil, fmt.Errorf("%s promotes redacted review evidence into executable fields; bind a secret reference instead", path)
		}
		if incomingReviewOnly {
			return reconcileMaskedImportHeaderFidelityBytes(
				typed,
				projectedMap,
				rawMap,
				path,
			)
		}
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			skipPath := path
			if path == "script" {
				skipPath = ""
			}
			if isNonExecutableRedactionSubtree(typed, key, skipPath) {
				out[key] = child
				continue
			}
			var projectedChild, rawChild any
			if projectedMap != nil {
				projectedChild = projectedMap[key]
			}
			if rawMap != nil {
				rawChild = rawMap[key]
			}
			reconciled, err := reconcileProjectedJSONWithOptions(
				child,
				projectedChild,
				rawChild,
				path+"."+key,
				allowRedactedRuntimeData,
				allowLiteralPriorSentinel,
			)
			if err != nil {
				return nil, err
			}
			out[key] = reconciled
		}
		return out, nil
	case []any:
		projectedSlice, projectedOK := projectedPrior.([]any)
		rawSlice, _ := rawPrior.([]any)
		containsMaskedSecret := jsonValueContainsExecutableMaskedSecretWithOptions(
			typed,
			path,
			allowRedactedRuntimeData,
		)
		if allowRedactedRuntimeData && path == "script.data_sources" {
			// The gate permits durable table sentinels, not positional
			// reassociation. Keep detecting them at the collection boundary so
			// a reorder cannot restore a prior raw table into a different name.
			containsMaskedSecret = jsonValueContainsExecutableMaskedSecret(typed, path)
		}
		if containsMaskedSecret &&
			(!projectedOK || !reflect.DeepEqual(typed, projectedSlice)) {
			stableFieldEdit := isStableRuntimeDataSourceEdit(path, typed, projectedSlice) ||
				isStableCredentialPreservingActionCollectionEdit(path, typed, projectedSlice)
			if !stableFieldEdit {
				for i, child := range typed {
					incomingMap, incomingOK := child.(map[string]any)
					if !incomingOK || i >= len(projectedSlice) {
						continue
					}
					projectedMap, _ := projectedSlice[i].(map[string]any)
					var rawMap map[string]any
					if i < len(rawSlice) {
						rawMap, _ = rawSlice[i].(map[string]any)
					}
					priorWasReviewOnly := isNonExecutableReviewAction(projectedMap) ||
						isNonExecutableReviewAction(rawMap) ||
						isDurablyImportReviewGatedAction(projectedMap) ||
						isDurablyImportReviewGatedAction(rawMap)
					incomingReviewOnly := isNonExecutableReviewAction(incomingMap) ||
						isDurablyImportReviewGatedAction(incomingMap)
					if priorWasReviewOnly && !incomingReviewOnly &&
						jsonValueContainsExecutableMaskedSecretWithOptions(incomingMap, path, allowRedactedRuntimeData) {
						return nil, fmt.Errorf("%s[%d] promotes redacted review evidence into executable fields; bind a secret reference instead", path, i)
					}
				}
				return nil, fmt.Errorf("%s changed while containing redacted credentials; bind secret references before modifying or reordering it", path)
			}
		}
		out := make([]any, len(typed))
		for i, child := range typed {
			var projectedChild, rawChild any
			if i < len(projectedSlice) {
				projectedChild = projectedSlice[i]
			}
			if i < len(rawSlice) {
				rawChild = rawSlice[i]
			}
			reconciled, err := reconcileProjectedJSONWithOptions(
				child,
				projectedChild,
				rawChild,
				fmt.Sprintf("%s[%d]", path, i),
				allowRedactedRuntimeData,
				allowLiteralPriorSentinel,
			)
			if err != nil {
				return nil, err
			}
			out[i] = reconciled
		}
		return out, nil
	default:
		return incoming, nil
	}
}

// reconcileMaskedImportHeaderFidelityBytes restores unchanged response
// projections of the inert header and multipart fidelity envelopes. Review-
// gated actions normally skip secret reconciliation because their evidence is
// non-executable, but these bytes are intentionally durable: persisting the
// fixed masked payload would silently destroy the operator's exact imported
// source. The historical function name remains for compatibility.
func reconcileMaskedImportHeaderFidelityBytes(
	incoming,
	projected,
	raw map[string]any,
	path string,
) (map[string]any, error) {
	reconciled, err := reconcileMaskedImportFidelityBytes(
		incoming,
		projected,
		raw,
		path,
		dsl.ImportHeaderFidelityMetadataKey,
		"omitted_occurrences",
		"imported-header",
	)
	if err != nil {
		return nil, err
	}
	return reconcileMaskedImportFidelityBytes(
		reconciled,
		projected,
		raw,
		path,
		dsl.ImportMultipartFidelityMetadataKey,
		"omitted_fields",
		"imported-multipart",
	)
}

func reconcileMaskedImportFidelityBytes(
	incoming,
	projected,
	raw map[string]any,
	path,
	metadataKey,
	collectionKey,
	payloadLabel string,
) (map[string]any, error) {
	incomingMarker := importFidelityMarkerJSON(incoming, metadataKey)
	if incomingMarker == nil {
		return incoming, nil
	}
	incomingOccurrences, incomingOK := importFidelityOccurrencesJSON(
		incomingMarker,
		collectionKey,
	)
	if !incomingOK || !headerFidelityOccurrencesContainMask(incomingOccurrences) {
		return incoming, nil
	}
	projectedOccurrences, projectedOK := importFidelityOccurrencesJSON(
		importFidelityMarkerJSON(projected, metadataKey),
		collectionKey,
	)
	rawOccurrences, rawOK := importFidelityOccurrencesJSON(
		importFidelityMarkerJSON(raw, metadataKey),
		collectionKey,
	)
	if !projectedOK || !rawOK ||
		len(incomingOccurrences) != len(projectedOccurrences) ||
		len(incomingOccurrences) != len(rawOccurrences) {
		return nil, fmt.Errorf(
			"%s contains an orphan masked %s payload; retain or remove the complete fidelity marker",
			path,
			payloadLabel,
		)
	}

	for index := range incomingOccurrences {
		incomingOccurrence := incomingOccurrences[index]
		projectedOccurrence := projectedOccurrences[index]
		rawOccurrence := rawOccurrences[index]
		if !reflect.DeepEqual(
			headerFidelityOccurrenceIdentity(incomingOccurrence),
			headerFidelityOccurrenceIdentity(projectedOccurrence),
		) || !reflect.DeepEqual(
			headerFidelityOccurrenceIdentity(incomingOccurrence),
			headerFidelityOccurrenceIdentity(rawOccurrence),
		) {
			return nil, fmt.Errorf(
				"%s.metadata.%s.%s changed while containing a masked %s payload",
				path,
				metadataKey,
				collectionKey,
				payloadLabel,
			)
		}
		for _, key := range []string{
			dsl.ImportHeaderFidelityPreservedNameBase64Key,
			dsl.ImportHeaderFidelityPreservedValueBase64Key,
		} {
			incomingValue, incomingExists := incomingOccurrence[key].(string)
			if !incomingExists || incomingValue != maskedImportHeaderBytes {
				continue
			}
			projectedValue, projectedExists := projectedOccurrence[key].(string)
			rawValue, rawExists := rawOccurrence[key].(string)
			if !projectedExists || projectedValue != maskedImportHeaderBytes ||
				!rawExists {
				return nil, fmt.Errorf(
					"%s.metadata.%s.%s[%d].%s contains an orphan masked %s payload",
					path,
					metadataKey,
					collectionKey,
					index,
					key,
					payloadLabel,
				)
			}
			incomingOccurrence[key] = rawValue
		}
	}
	return incoming, nil
}

func importHeaderFidelityMarkerJSON(action map[string]any) map[string]any {
	return importFidelityMarkerJSON(action, dsl.ImportHeaderFidelityMetadataKey)
}

func importFidelityMarkerJSON(
	action map[string]any,
	metadataKey string,
) map[string]any {
	if action == nil {
		return nil
	}
	metadata, _ := action["metadata"].(map[string]any)
	marker, _ := metadata[metadataKey].(map[string]any)
	return marker
}

func importHeaderFidelityOccurrencesJSON(marker map[string]any) ([]map[string]any, bool) {
	return importFidelityOccurrencesJSON(marker, "omitted_occurrences")
}

func importFidelityOccurrencesJSON(
	marker map[string]any,
	collectionKey string,
) ([]map[string]any, bool) {
	if marker == nil {
		return nil, false
	}
	switch values := marker[collectionKey].(type) {
	case []any:
		out := make([]map[string]any, 0, len(values))
		for _, raw := range values {
			value, ok := raw.(map[string]any)
			if !ok {
				return nil, false
			}
			out = append(out, value)
		}
		return out, true
	case []map[string]any:
		return values, true
	default:
		return nil, false
	}
}

func headerFidelityOccurrencesContainMask(occurrences []map[string]any) bool {
	for _, occurrence := range occurrences {
		for _, key := range []string{
			dsl.ImportHeaderFidelityPreservedNameBase64Key,
			dsl.ImportHeaderFidelityPreservedValueBase64Key,
		} {
			if value, _ := occurrence[key].(string); value == maskedImportHeaderBytes {
				return true
			}
		}
	}
	return false
}

func headerFidelityOccurrenceIdentity(occurrence map[string]any) map[string]any {
	if occurrence == nil {
		return nil
	}
	out := make(map[string]any, len(occurrence))
	for key, value := range occurrence {
		switch key {
		case dsl.ImportHeaderFidelityPreservedNameBase64Key,
			dsl.ImportHeaderFidelityPreservedValueBase64Key:
			continue
		default:
			out[key] = value
		}
	}
	return out
}

func isTransientScriptProjectionGate(
	incoming,
	projectedPrior,
	rawPrior map[string]any,
) bool {
	return hasScriptProjectionResponseGate(incoming) &&
		hasScriptProjectionResponseGate(projectedPrior) &&
		!hasScriptProjectionResponseGate(rawPrior) &&
		!isDurablyImportReviewGatedAction(rawPrior)
}

func hasScriptProjectionResponseGate(action map[string]any) bool {
	if !isScriptActionJSONValue(action) {
		return false
	}
	metadata, _ := action["metadata"].(map[string]any)
	if metadata == nil {
		return false
	}
	if !scriptProjectionResponseGateMarker(metadata) {
		// Keep recognizing response projections emitted during the transition
		// before the explicit sidecar marker was introduced.
		review, _ := metadata["import_review"].(map[string]any)
		source, _ := review["source"].(string)
		if !strings.EqualFold(strings.TrimSpace(source), "script_projection") {
			return false
		}
	}
	for _, blocker := range executionReviewBlockerCodes(metadata) {
		if blocker == "import_review_required" {
			return true
		}
	}
	return false
}

func scriptProjectionResponseGateMarker(metadata map[string]any) bool {
	marker, _ := metadata[scriptProjectionResponseGateMetadataKey].(map[string]any)
	markerVersion := 0
	switch version := marker["version"].(type) {
	case float64:
		if version == 1 {
			markerVersion = 1
		}
	case int:
		if version == 1 {
			markerVersion = 1
		}
	case json.Number:
		if version.String() == "1" {
			markerVersion = 1
		}
	}
	return markerVersion == 1
}

func withoutTransientScriptProjectionGate(action map[string]any) map[string]any {
	if !hasScriptProjectionResponseGate(action) {
		return action
	}
	out := make(map[string]any, len(action))
	for key, value := range action {
		out[key] = value
	}
	metadata, _ := action["metadata"].(map[string]any)
	cleanMetadata := make(map[string]any, len(metadata))
	for key, value := range metadata {
		cleanMetadata[key] = value
	}
	delete(cleanMetadata, scriptProjectionResponseGateMetadataKey)
	if review, _ := cleanMetadata["import_review"].(map[string]any); review != nil {
		source, _ := review["source"].(string)
		if strings.EqualFold(strings.TrimSpace(source), "script_projection") {
			delete(cleanMetadata, "import_review")
		}
	}
	if blocker, _ := cleanMetadata["execution_blocker"].(string); blocker == "import_review_required" {
		delete(cleanMetadata, "execution_blocker")
	}
	if raw, exists := cleanMetadata["execution_blockers"]; exists {
		filtered := make([]any, 0)
		switch blockers := raw.(type) {
		case []any:
			for _, blocker := range blockers {
				text, _ := blocker.(string)
				if text != "import_review_required" {
					filtered = append(filtered, blocker)
				}
			}
		case []string:
			for _, blocker := range blockers {
				if blocker != "import_review_required" {
					filtered = append(filtered, blocker)
				}
			}
		}
		if len(filtered) == 0 {
			delete(cleanMetadata, "execution_blockers")
		} else {
			cleanMetadata["execution_blockers"] = filtered
		}
	}
	if len(cleanMetadata) == 0 {
		delete(out, "metadata")
	} else {
		out["metadata"] = cleanMetadata
	}
	return out
}

func jsonValueContainsExecutableMaskedSecret(value any, path string) bool {
	return jsonValueContainsExecutableMaskedSecretWithOptions(value, path, false)
}

func jsonValueContainsExecutableMaskedSecretWithOptions(
	value any,
	path string,
	allowRedactedRuntimeData bool,
) bool {
	switch typed := value.(type) {
	case string:
		return strings.Contains(typed, maskedScriptSecret)
	case map[string]any:
		if isNonExecutableReviewAction(typed) || isDurablyImportReviewGatedAction(typed) {
			return false
		}
		for key, child := range typed {
			if isNonExecutableRedactionSubtree(typed, key, path) {
				continue
			}
			childPath := appendJSONPointer(path, key)
			// Redacted imported parameter rows are safe durable placeholders,
			// not a project-secret binding. reconcileMaskedScriptSecrets permits
			// them only while the script retains the matching execution gate.
			if allowRedactedRuntimeData && isLegacyRedactedRuntimeSourceTraversalPath(childPath) {
				continue
			}
			// Must mirror discoverRedactedValues: a provenance entry raises no
			// binding requirement, so treating it as an orphan sentinel here would
			// reject a save the user has no way to satisfy.
			if isImportProvenanceMetadataPath(childPath) {
				continue
			}
			if jsonValueContainsExecutableMaskedSecretWithOptions(child, childPath, allowRedactedRuntimeData) {
				return true
			}
		}
	case []any:
		for index, child := range typed {
			childPath := appendJSONPointer(path, strconv.Itoa(index))
			if allowRedactedRuntimeData &&
				isLegacyRedactedRuntimeSourceTraversalPath(childPath) {
				continue
			}
			if jsonValueContainsExecutableMaskedSecretWithOptions(
				child,
				childPath,
				allowRedactedRuntimeData,
			) {
				return true
			}
		}
	}
	return false
}

// isStableRuntimeDataSourceEdit permits field-level reconciliation for a
// same-size, same-order data-source list. This lets an operator replace one
// gated imported table while a different table is still masked for transport.
// Renames, additions, removals, duplicates, and reordering remain rejected so
// a projected sentinel can never be restored into the wrong source.
func isStableRuntimeDataSourceEdit(path string, incoming, projected []any) bool {
	if path != "script.data_sources" || len(incoming) != len(projected) {
		return false
	}
	seen := make(map[string]struct{}, len(incoming))
	for index := range incoming {
		incomingMap, incomingOK := incoming[index].(map[string]any)
		projectedMap, projectedOK := projected[index].(map[string]any)
		if !incomingOK || !projectedOK {
			return false
		}
		incomingName, incomingOK := incomingMap["name"].(string)
		projectedName, projectedOK := projectedMap["name"].(string)
		incomingName = strings.TrimSpace(incomingName)
		projectedName = strings.TrimSpace(projectedName)
		if !incomingOK || !projectedOK || incomingName == "" || incomingName != projectedName {
			return false
		}
		if _, duplicate := seen[incomingName]; duplicate {
			return false
		}
		seen[incomingName] = struct{}{}
	}
	return true
}

// isStableCredentialPreservingActionCollectionEdit permits field-level
// reconciliation for masked imported requests when every action remains at the
// same structural position and retains its request-routing identity. This lets
// operators edit labels, assertions, extractors, and other non-wire authoring
// fields without re-entering imported credentials. Request destination changes
// and reordering continue to fail closed here; the post-reconciliation import
// context fence independently protects raw/import-response updates.
func isStableCredentialPreservingActionCollectionEdit(path string, incoming, projected []any) bool {
	if len(incoming) != len(projected) || !isActionCollectionTraversalPath(path) {
		return false
	}
	primaryKeys := make([]string, len(projected))
	primaryCounts := make(map[string]int, len(projected))
	for index := range incoming {
		incomingMap, incomingOK := incoming[index].(map[string]any)
		projectedMap, projectedOK := projected[index].(map[string]any)
		if !incomingOK || !projectedOK {
			return false
		}
		if !reflect.DeepEqual(
			credentialPreservingActionIdentity(incomingMap),
			credentialPreservingActionIdentity(projectedMap),
		) {
			return false
		}
		primaryKey, ok := credentialPreservingActionIdentityKey(projectedMap)
		if !ok {
			return false
		}
		primaryKeys[index] = primaryKey
		primaryCounts[primaryKey]++
	}

	// A method/target tuple is not necessarily an action identity. Login
	// collections commonly contain several POST /login actions with different
	// credentials. When that routing identity is duplicated, require a unique
	// source/action discriminator to stay at the same position. If no unique
	// discriminator exists, only an otherwise byte-for-byte unchanged projected
	// action is safe: positional restoration then cannot associate a credential
	// with edited behavior from another action.
	for index := range incoming {
		if primaryCounts[primaryKeys[index]] == 1 {
			continue
		}
		incomingMap := incoming[index].(map[string]any)
		projectedMap := projected[index].(map[string]any)
		if reflect.DeepEqual(incomingMap, projectedMap) {
			continue
		}
		incomingSecondary, incomingOK := credentialPreservingActionSecondaryIdentity(incomingMap)
		projectedSecondary, projectedOK := credentialPreservingActionSecondaryIdentity(projectedMap)
		if !incomingOK || !projectedOK || incomingSecondary != projectedSecondary {
			return false
		}
		for otherIndex := range projected {
			if otherIndex == index || primaryKeys[otherIndex] != primaryKeys[index] {
				continue
			}
			otherMap := projected[otherIndex].(map[string]any)
			otherSecondary, otherOK := credentialPreservingActionSecondaryIdentity(otherMap)
			if !otherOK || otherSecondary == projectedSecondary {
				return false
			}
		}
	}
	return true
}

func isActionCollectionTraversalPath(path string) bool {
	switch path {
	case "script.init", "script.actions", "script.teardown":
		return true
	default:
		return strings.HasSuffix(path, ".control.steps") ||
			strings.HasSuffix(path, ".control.else") ||
			strings.HasSuffix(path, ".params.requests")
	}
}

func credentialPreservingActionIdentity(action map[string]any) map[string]any {
	identity := make(map[string]any, 5)
	for _, key := range []string{"type", "method", "target", "selector", "object_ref"} {
		if value, exists := action[key]; exists {
			identity[key] = value
		}
	}
	return identity
}

func credentialPreservingActionIdentityKey(action map[string]any) (string, bool) {
	payload, err := json.Marshal(credentialPreservingActionIdentity(action))
	if err != nil {
		return "", false
	}
	return string(payload), true
}

func credentialPreservingActionSecondaryIdentity(action map[string]any) (string, bool) {
	if metadata, ok := action["metadata"].(map[string]any); ok {
		for _, key := range []string{"candidate_id", "source_refs", "source_mappings", "source_ref"} {
			value, exists := metadata[key]
			if !exists {
				continue
			}
			payload, err := json.Marshal(value)
			if err == nil && string(payload) != "null" && string(payload) != `""` && string(payload) != "[]" {
				return key + ":" + string(payload), true
			}
		}
	}
	name, ok := action["name"].(string)
	name = strings.TrimSpace(name)
	if !ok || name == "" {
		return "", false
	}
	return "name:" + name, true
}

func scriptHasRedactedRuntimeCredentialSource(script *dsl.Script) bool {
	if script == nil {
		return false
	}
	for _, source := range script.DataSources {
		if strings.Contains(source.InlineData, maskedScriptSecret) {
			return true
		}
	}
	for _, parameter := range script.Parameters {
		for _, value := range parameter.Values {
			if strings.Contains(value, maskedScriptSecret) {
				return true
			}
		}
	}
	return false
}

func scriptHasExecutionBlockerCode(script *dsl.Script, want string) bool {
	if script == nil || strings.TrimSpace(want) == "" {
		return false
	}
	found := false
	err := dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		for _, blocker := range executionReviewBlockerCodes(ref.Metadata()) {
			if strings.EqualFold(strings.TrimSpace(blocker), strings.TrimSpace(want)) {
				found = true
				return nil
			}
		}
		return nil
	})
	return found && err == nil
}

func isRuntimeDataTableTraversalPath(path string) bool {
	if isRuntimeDataTableJSONPath(path) {
		return true
	}

	const (
		reconcilePrefix = "script.data_sources["
		reconcileSuffix = "].inline_data"
		hybridPrefix    = "script.data_sources/"
		hybridSuffix    = "/inline_data"
	)
	if strings.HasPrefix(path, reconcilePrefix) && strings.HasSuffix(path, reconcileSuffix) {
		index := strings.TrimSuffix(strings.TrimPrefix(path, reconcilePrefix), reconcileSuffix)
		return index != "" && isDecimalToken(index)
	}
	if strings.HasPrefix(path, hybridPrefix) && strings.HasSuffix(path, hybridSuffix) {
		index := strings.TrimSuffix(strings.TrimPrefix(path, hybridPrefix), hybridSuffix)
		return index != "" && isDecimalToken(index)
	}
	return false
}

func isLegacyRedactedRuntimeSourceTraversalPath(path string) bool {
	if isRuntimeDataTableTraversalPath(path) {
		return true
	}
	if tokens, err := splitJSONPointer(path); err == nil && len(tokens) == 4 {
		return tokens[0] == "parameters" &&
			isDecimalToken(tokens[1]) &&
			tokens[2] == "values" &&
			isDecimalToken(tokens[3])
	}

	const (
		reconcilePrefix = "script.parameters["
		reconcileMiddle = "].values["
		reconcileSuffix = "]"
		hybridPrefix    = "script.parameters/"
		hybridMiddle    = "/values/"
	)
	if strings.HasPrefix(path, reconcilePrefix) && strings.HasSuffix(path, reconcileSuffix) {
		remainder := strings.TrimSuffix(strings.TrimPrefix(path, reconcilePrefix), reconcileSuffix)
		parameterIndex, valueIndex, ok := strings.Cut(remainder, reconcileMiddle)
		return ok && isDecimalToken(parameterIndex) && isDecimalToken(valueIndex)
	}
	if strings.HasPrefix(path, hybridPrefix) {
		remainder := strings.TrimPrefix(path, hybridPrefix)
		parameterIndex, valueIndex, ok := strings.Cut(remainder, hybridMiddle)
		return ok && isDecimalToken(parameterIndex) && isDecimalToken(valueIndex)
	}
	return false
}

// maskScriptResponseValue recursively projects script-shaped objects embedded
// in heterogeneous import/review responses. Converter result structs often
// repeat the same script under result.script, so masking only the top-level
// convenience field would leave a second disclosure path.
func maskScriptResponseValue(value any) any {
	if value == nil {
		return nil
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var raw any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return value
	}
	return projectEmbeddedScriptNodes(raw)
}

func projectEmbeddedScriptNodes(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		if isScriptJSONObject(typed) {
			return maskScriptJSONObject(typed)
		}
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			out[key] = projectEmbeddedScriptNodes(child)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			out[i] = projectEmbeddedScriptNodes(child)
		}
		return out
	default:
		return value
	}
}

// maskScriptJSONObject routes script-shaped values through the typed projection
// boundary. In particular, maskScriptSecrets decodes body_encoding=base64,
// projects the wire bytes, and re-encodes them; sending an encoded body directly
// through the generic free-text projector would corrupt it as a high-entropy
// token while leaving the base64 encoding marker in place.
func maskScriptJSONObject(value map[string]any) any {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var script dsl.Script
	if err := json.Unmarshal(payload, &script); err != nil {
		return nil
	}
	masked := maskScriptSecrets(&script)
	if masked == nil {
		return nil
	}
	projected, err := scriptAsJSONValue(masked)
	if err != nil {
		return nil
	}
	return projected
}

func isScriptJSONObject(value map[string]any) bool {
	_, hasConfig := value["config"]
	_, hasActions := value["actions"]
	_, hasProtocol := value["protocol"]
	return hasConfig && hasActions && hasProtocol
}
