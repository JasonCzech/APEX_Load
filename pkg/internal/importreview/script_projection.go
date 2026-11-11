package importreview

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/javarunner"
	"github.com/jasonczech/apexload/pkg/scriptdisclosure"
	"github.com/jasonczech/apexload/pkg/scrub"
	"io"
	"maps"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

const MaskedScriptSecret = scrub.Redacted

const CredentialParameterSourceRedactedBlocker = "credential_parameter_source_redacted"

const ScriptProjectionResponseGateMetadataKey = "script_projection_response_gate_v1"

const ScriptProjectionUnprojectableBodyReason = "unprojectable_credential_body"

var MaskedImportHeaderBytes = base64.StdEncoding.EncodeToString([]byte(MaskedScriptSecret))

var ScriptSecretTemplatePattern = regexp.MustCompile(`\{\{\s*secret\.(` + engine.LogicalSecretNameInputPattern + `)\s*\}\}`)

var ScriptPortableTemplatePattern = regexp.MustCompile(`\{\{[^{}\r\n]{1,256}\}\}`)

var ScriptPortableReferencePattern = regexp.MustCompile(`^(?:(?:env|data|var|param|file)\.[A-Za-z0-9_.:/@-]+|secret\.` + engine.LogicalSecretNameInputPattern + `)$`)

var ScriptPortableRandomPattern = regexp.MustCompile(`^fn\.random\(\s*-?\d+\s*,\s*-?\d+\s*\)$`)

var ScriptPortableRandomStringPattern = regexp.MustCompile(`^fn\.random_string\(\s*\d+\s*\)$`)

const ProjectedXMLMessageID = "{{fn.uuid()}}"

var RedactedXMLMessageIDPattern = regexp.MustCompile(`(?is)(<(?:(?:[\w.-]+):)?messageId\b[^>]*>)\s*\[REDACTED\]\s*(</(?:(?:[\w.-]+):)?messageId\s*>)`)

var XmlMessageIDValuePattern = regexp.MustCompile(`(?is)(<(?:(?:[\w.-]+):)?messageId\b[^>]*>)([^<>]*)(</(?:(?:[\w.-]+):)?messageId\s*>)`)

var PortableXMLSecretElementPattern = regexp.MustCompile(`(?is)<(?:[\w.-]+:)?[\w.-]+\b[^>]*>\s*\{\{\s*secret\.` + engine.LogicalSecretNameInputPattern + `\s*\}\}\s*</(?:[\w.-]+:)?[\w.-]+\s*>`)

var PortableXMLSecretAttributePattern = regexp.MustCompile(`(?is)(?:[\w.-]+:)?[\w.-]+\s*=\s*(?:"\s*\{\{\s*secret\.` + engine.LogicalSecretNameInputPattern + `\s*\}\}\s*"|'\s*\{\{\s*secret\.` + engine.LogicalSecretNameInputPattern + `\s*\}\}\s*')`)

var PortableXMLTemplateElementPattern = regexp.MustCompile(`(?is)<(?:[\w.-]+:)?[\w.-]+\b[^>]*>\s*\{\{[^{}\r\n]{1,256}\}\}\s*</(?:[\w.-]+:)?[\w.-]+\s*>`)

var PortableXMLTemplateAttributePattern = regexp.MustCompile(`(?is)(?:[\w.-]+:)?[\w.-]+\s*=\s*(?:"\s*\{\{[^{}\r\n]{1,256}\}\}\s*"|'\s*\{\{[^{}\r\n]{1,256}\}\}\s*')`)

func MaskScriptSecrets(script *dsl.Script) *dsl.Script {
	return MaskScriptSecretsWithMemo(script, nil)
}

func MaskScriptSecretsWithMemo(script *dsl.Script, memo *ImportProjectionTextMemo) *dsl.Script {
	if script == nil {
		return nil
	}
	payload, err := json.Marshal(script)
	if err != nil {
		return nil
	}
	// Only the costly projection is cached. Every caller decodes its own copy
	// of it, whether or not the cache already held it, and the restores below
	// run against the caller's own script, so they keep its typed values.
	key := sha256.Sum256(payload)
	encoded := MaskedScripts.Get(key)
	if encoded == nil {
		projected := ProjectScriptDisclosureCopy(payload, memo)
		if projected == nil {
			return nil
		}
		if encoded, err = json.Marshal(projected); err != nil {
			return nil
		}
		MaskedScripts.Put(key, encoded)
	}
	var projectedScript dsl.Script
	if err := json.Unmarshal(encoded, &projectedScript); err != nil {
		return nil
	}
	RestoreBrunoNativeCompactProvenance(&projectedScript, script)
	RestoreProjectedJavaVuserApprovalEvidence(&projectedScript, script)
	return &projectedScript
}

func ProjectScriptDisclosureCopy(payload []byte, memo *ImportProjectionTextMemo) *dsl.Script {
	if memo == nil {
		memo = &ImportProjectionTextMemo{}
	}
	var source dsl.Script
	if err := json.Unmarshal(payload, &source); err != nil {
		return nil
	}
	var working dsl.Script
	if err := json.Unmarshal(payload, &working); err != nil {
		return nil
	}
	if err := MaskImportHeaderFidelityBytes(&working); err != nil {
		return nil
	}
	// Import-save attestations are short-lived capabilities for the authorized
	// editable draft only. Review payloads, stored reads, exports, and other
	// disclosure-safe projections must never duplicate them.
	StripImportDraftAttestation(&working)
	// Source commit fences are private, server-owned reconciliation state.
	// They contain no source values, but exposing them would let clients infer
	// pending publication state and would invite accidental round-trip edits.
	StripProtectedImportSourceCommitFence(&working)
	MaskRawBrunoRuntimeArtifactReference(&working)
	MaskJavaVuserVMParameterValues(&working)
	MaskScriptDataSourceCredentials(&working)
	protectedBodies, ok := ProjectScriptRequestBodies(&working)
	if !ok {
		return nil
	}
	payload, err := json.Marshal(&working)
	if err != nil {
		return nil
	}
	var raw any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil
	}
	projected := ProjectScriptNode(raw, ScriptProjectionContext{TextMemo: memo})
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
		SetImportCommitSurfaceBody(ref, body)
		restored++
		return nil
	}); err != nil || restored != len(protectedBodies) {
		return nil
	}
	RestoreBrunoNativeCompactProvenance(&masked, &source)
	projectedScript, err := scriptdisclosure.ProjectWithCredentialSourceMemo(&masked, &source, memo.Scrubber())
	if err != nil {
		return nil
	}
	MaskRestoredImportBodies(projectedScript, &source)
	return projectedScript
}

func MaskImportHeaderFidelityBytes(script *dsl.Script) error {
	if script == nil {
		return nil
	}
	return dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		metadata := ref.Metadata()
		if marker, ok := metadata[dsl.ImportHeaderFidelityMetadataKey].(map[string]any); ok {
			MaskHeaderFidelityOccurrenceBytes(marker["omitted_occurrences"])
		}
		if marker, ok := metadata[dsl.ImportMultipartFidelityMetadataKey].(map[string]any); ok {
			MaskHeaderFidelityOccurrenceBytes(marker["omitted_fields"])
		}
		return nil
	})
}

func MaskHeaderFidelityOccurrenceBytes(raw any) {
	maskOccurrence := func(occurrence map[string]any) {
		if occurrence == nil {
			return
		}
		if _, exists := occurrence[dsl.ImportHeaderFidelityPreservedNameBase64Key]; exists {
			occurrence[dsl.ImportHeaderFidelityPreservedNameBase64Key] = MaskedImportHeaderBytes
		}
		if _, exists := occurrence[dsl.ImportHeaderFidelityPreservedValueBase64Key]; exists {
			occurrence[dsl.ImportHeaderFidelityPreservedValueBase64Key] = MaskedImportHeaderBytes
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

func ImportHeaderFidelityProjectionChanged(raw, projected *dsl.Script) bool {
	rawSnapshot, rawOK := ImportHeaderFidelitySnapshot(raw)
	projectedSnapshot, projectedOK := ImportHeaderFidelitySnapshot(projected)
	return !rawOK || !projectedOK || !reflect.DeepEqual(rawSnapshot, projectedSnapshot)
}

func ImportHeaderFidelitySnapshot(script *dsl.Script) ([]string, bool) {
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
			occurrences, ok := ImportFidelityOccurrencesJSON(
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
	MaxScriptProjectionDataSourceBytes = 8 << 20
	MaxScriptProjectionDataSourceCells = 100_000
)

func MaskScriptDataSourceCredentials(script *dsl.Script) {
	if script == nil {
		return
	}
	credentialParameterNames := make(map[string]struct{})
	if script.Metadata != nil && script.Metadata.Import != nil {
		for _, name := range ImportPreservedStringSlice(
			script.Metadata.Import.Preserved[dsl.ImportPreservedCredentialParameterNamesKey],
		) {
			if name = CanonicalCredentialParameterName(name); name != "" {
				credentialParameterNames[name] = struct{}{}
			}
		}
	}
	sensitiveColumns := make(map[string][]string)
	for index := range script.Parameters {
		parameter := &script.Parameters[index]
		_, provenCredentialSource := credentialParameterNames[CanonicalCredentialParameterName(parameter.Name)]
		sensitive := provenCredentialSource ||
			IsSensitiveScriptKey(parameter.Name) ||
			IsSensitiveScriptKey(parameter.Column)
		if sensitive {
			for valueIndex, value := range parameter.Values {
				if value == "" || IsPortableTemplateBinding(value) ||
					strings.Contains(value, MaskedScriptSecret) {
					continue
				}
				parameter.Values[valueIndex] = ProjectSensitiveScriptValue(value)
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
		if columns := ImportPreservedStringSlice(
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
		sourceSensitive := IsSensitiveScriptKey(source.Name)
		if strings.TrimSpace(source.InlineData) == "" {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(source.Encoding), "base64") {
			if sourceSensitive || len(requiredColumns) > 0 {
				source.InlineData = MaskedScriptSecret
			}
			continue
		}
		projected, sensitive, ok := ProjectScriptDataSourceTable(
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
			source.InlineData = MaskedScriptSecret
		}
	}
}

func CanonicalCredentialParameterName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func ImportPreservedStringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			if Text, ok := item.(string); ok && strings.TrimSpace(Text) != "" {
				result = append(result, Text)
			}
		}
		return result
	default:
		return nil
	}
}

func SanitizePersistentImportReviewSurfaces(script *dsl.Script) error {
	if script == nil {
		return nil
	}
	if script.Metadata != nil && script.Metadata.Import != nil && script.Metadata.Import.Preserved != nil {
		if value, exists := script.Metadata.Import.Preserved[JavaVuserApprovalEvidenceKey]; exists {
			validated := ProjectJavaVuserApprovalEvidence(value)
			if len(validated) == 2 {
				script.Metadata.Import.Preserved[JavaVuserApprovalEvidenceKey] = validated
			} else {
				delete(script.Metadata.Import.Preserved, JavaVuserApprovalEvidenceKey)
			}
		}
	}
	return nil
}

func IsExecutableImportPreservedPayloadKey(key string) bool {
	return NormalizeScriptFieldName(key) == "recordedresourceurls"
}

const (
	// A [JavaVM:Options] row is one command line, not a document. These bounds
	// keep the verbatim retention below from turning a known key name into an
	// unbounded durable raw-source channel for caller-supplied metadata.
	MaxImportedJavaVMParameterRowBytes = 16 << 10
	MaxImportedJavaVMParameterRows     = 256
)

func IsImportPreservedJavaVMSettingsKey(key string) bool {
	return NormalizeScriptFieldName(key) == "javavmsettings"
}

func IsExecutableJavaVMParameterMember(key string) bool {
	switch NormalizeScriptFieldName(key) {
	case "additionalvmparameters", "additionalvmparametersraw", "additionalvmparameterrows":
		return true
	default:
		return false
	}
}

func ProjectPreservedJavaVMSettings(key string, value any) any {
	retained := make(map[string]any, 3)
	if settings, isObject := value.(map[string]any); isObject {
		for member, child := range settings {
			if !IsExecutableJavaVMParameterMember(member) {
				continue
			}
			if rows, ok := RetainableJavaVMParameterMember(child); ok {
				retained[member] = rows
			}
		}
	}
	projected := ProjectScriptNode(
		value,
		ScriptProjectionContext{
			key:       key,
			opaqueMap: true,
			metadata:  true,
		},
	)
	projectedSettings, ok := projected.(map[string]any)
	if !ok {
		return projected
	}
	for member, rows := range retained {
		projectedSettings[member] = rows
	}
	return projectedSettings
}

func RetainableJavaVMParameterMember(value any) (any, bool) {
	switch typed := value.(type) {
	case string:
		return typed, len(typed) <= MaxImportedJavaVMParameterRowBytes
	case []string:
		if len(typed) > MaxImportedJavaVMParameterRows {
			return nil, false
		}
		for _, row := range typed {
			if len(row) > MaxImportedJavaVMParameterRowBytes {
				return nil, false
			}
		}
		return typed, true
	case []any:
		if len(typed) > MaxImportedJavaVMParameterRows {
			return nil, false
		}
		for _, row := range typed {
			Text, isText := row.(string)
			if !isText || len(Text) > MaxImportedJavaVMParameterRowBytes {
				return nil, false
			}
		}
		return typed, true
	default:
		return nil, false
	}
}

func ProjectScriptDataSourceTable(
	source dsl.DataSource,
	requiredColumns []string,
	sourceSensitive bool,
) (string, bool, bool) {
	if len(source.InlineData) > MaxScriptProjectionDataSourceBytes {
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
			len(record) > MaxScriptProjectionDataSourceCells-cellCount {
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
		ScriptDataSourceRecordLooksLikeCredentialHeader(records[0], requiredColumns) {
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
		if IsSensitiveScriptKey(strings.TrimPrefix(column, "\ufeff")) ||
			ScriptDataSourceColumnRequired(column, requiredColumns) {
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
			if value == "" || IsPortableTemplateBinding(value) ||
				strings.Contains(value, MaskedScriptSecret) {
				continue
			}
			records[rowIndex][columnIndex] = MaskedScriptSecret
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

func ScriptDataSourceRecordLooksLikeCredentialHeader(record, requiredColumns []string) bool {
	for _, column := range record {
		column = strings.TrimPrefix(column, "\ufeff")
		if IsSensitiveScriptKey(column) ||
			ScriptDataSourceColumnRequired(column, requiredColumns) {
			return true
		}
	}
	return false
}

func ScriptDataSourceColumnRequired(column string, requiredColumns []string) bool {
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

func MaskRawBrunoRuntimeArtifactReference(script *dsl.Script) {
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
	script.Config.Bruno.BundleRef = MaskedScriptSecret
	script.Config.Bruno.BundleSHA256 = MaskedScriptSecret
	script.Config.Bruno.ManifestSHA256 = MaskedScriptSecret
}

func MaskJavaVuserVMParameterValues(script *dsl.Script) {
	if script == nil {
		return
	}
	for index, parameter := range script.Config.JavaVuser.AdditionalVMParameters {
		if javarunner.ContainsSensitiveVMParameterValue(parameter) {
			script.Config.JavaVuser.AdditionalVMParameters[index] = MaskedScriptSecret
		}
	}
}

func ValidateJavaVMParameterDisclosureSafety(script *dsl.Script) error {
	return ValidateJavaVMParameterSafety(script, false)
}

func ValidateJavaVMParameterSafety(script *dsl.Script, allowPreservedCredentials bool) error {
	_ = allowPreservedCredentials
	if script == nil {
		return nil
	}
	issues := javarunner.ValidateVMParametersForExecution(
		script.Config.JavaVuser.AdditionalVMParameters,
	)
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

func ProjectScriptRequestBodies(script *dsl.Script) (map[string]string, bool) {
	protected := make(map[string]string)
	err := dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		body, ok, err := DecodeImportCommitProjectionBody(ref)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		// A whole governed body reference resolves to its structured wire
		// document only at runtime. Its authoring token must not be parsed as
		// literal JSON/form/XML and replaced with a new body sentinel.
		if _, governed := credential.ParseReference(body.stored); governed {
			return nil
		}
		projected, result, err := credentialpolicy.ProjectRequestBody(
			body.wire,
			ImportCommitSurfaceContentType(ref),
			credentialpolicy.Options{
				Mode: credentialpolicy.Redact,
				Source: credentialpolicy.SourceRef{
					Format:  "script_projection",
					Path:    ref.Path,
					Pointer: "/body",
				},
				RequestKey:         ref.Path,
				TrustedWholeValues: PortableBodyTemplateValues(body.Text()),
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
			projected = []byte(MaskedScriptSecret)
			ApplyScriptProjectionResponseGate(ref)
		}
		projected = ProjectMaskedXMLMessageIDs(projected)
		protected[ref.Path] = body.Encode(projected)
		SetImportCommitSurfaceBody(ref, "")
		return nil
	})
	if err != nil {
		return nil, false
	}
	return protected, true
}

func ApplyScriptProjectionResponseGate(ref dslwalk.SurfaceRef) {
	metadata := make(map[string]any, len(ref.Metadata())+4)
	for key, value := range ref.Metadata() {
		metadata[key] = value
	}
	if blocker, _ := metadata["execution_blocker"].(string); strings.TrimSpace(blocker) == "" {
		metadata["execution_blocker"] = "import_review_required"
	}
	metadata["execution_blockers"] = AppendUniqueStrings(
		ExecutionReviewBlockerCodes(metadata),
		"import_review_required",
	)
	if _, exists := metadata["import_review"]; !exists {
		metadata["import_review"] = map[string]any{
			"version":      1,
			"source":       "script_projection",
			"reason_codes": []string{ScriptProjectionUnprojectableBodyReason},
			"source_refs":  []string{ref.Path + ".body"},
			"resolution":   "replace_behavior_and_remove_gate",
		}
	}
	// This marker distinguishes a disclosure-only fail-closed gate from source
	// behavior that was already durably review-gated. It contains no source
	// value and is removed when an unchanged masked response is reconciled with
	// its authoritative stored script.
	metadata[ScriptProjectionResponseGateMetadataKey] = map[string]any{"version": 1}
	ref.SetMetadata(metadata)
}

func ProjectMaskedXMLMessageIDs(payload []byte) []byte {
	value := string(payload)
	matches := XmlMessageIDValuePattern.FindAllStringSubmatchIndex(value, -1)
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
		if ScrubScriptText(content, false) != MaskedScriptSecret {
			continue
		}
		builder.WriteString(value[cursor:match[4]])
		builder.WriteString(ProjectedXMLMessageID)
		cursor = match[5]
		changed = true
	}
	if !changed {
		return payload
	}
	builder.WriteString(value[cursor:])
	return []byte(builder.String())
}

func RewrittenMessageIDs(raw, projected string) (pairs [][4]int, ok bool) {
	if !strings.Contains(projected, ProjectedXMLMessageID) {
		return nil, true
	}
	projectedIDs := XmlMessageIDValuePattern.FindAllStringSubmatchIndex(projected, -1)
	rawIDs := XmlMessageIDValuePattern.FindAllStringSubmatchIndex(raw, -1)
	if len(projectedIDs) != len(rawIDs) {
		return nil, false
	}
	for index, id := range projectedIDs {
		if projected[id[4]:id[5]] == ProjectedXMLMessageID {
			pairs = append(pairs, [4]int{id[4], id[5], rawIDs[index][4], rawIDs[index][5]})
		}
	}
	return pairs, true
}

func ProjectedMessageIDsAsRecorded(raw, projected string) string {
	pairs, ok := RewrittenMessageIDs(raw, projected)
	if !ok || len(pairs) == 0 {
		return projected
	}
	var out strings.Builder
	out.Grow(len(projected))
	cursor := 0
	for _, pair := range pairs {
		recorded := raw[pair[2]:pair[3]]
		if strings.Contains(recorded, MaskedScriptSecret) {
			return projected
		}
		out.WriteString(projected[cursor:pair[0]])
		out.WriteString(recorded)
		cursor = pair[1]
	}
	out.WriteString(projected[cursor:])
	return out.String()
}

type ScriptProjectionContext struct {
	TextMemo            *ImportProjectionTextMemo
	transactionParams   bool
	transactionFunction string
	label               bool
	key                 string
	opaqueMap           bool
	headers             bool
	headerList          bool
	secretBindings      bool
	metadata            bool
	// actionMetadata is set on the metadata map of an action-shaped object
	// (one with a string type), not on a map merely named metadata inside
	// configuration or parameters.
	actionMetadata bool
}

func ProjectScriptNode(value any, ctx ScriptProjectionContext) any {
	if ctx.TextMemo == nil {
		ctx.TextMemo = &ImportProjectionTextMemo{}
	}
	switch typed := value.(type) {
	case map[string]any:
		if ctx.headerList {
			return ProjectHeaderEntry(typed, ctx.TextMemo)
		}
		if ctx.metadata && ctx.key == "har.request.query_string" && publicCacheKeyPair(typed) {
			// A recorded query parameter whose value is a public cache key
			// (scrub.PublicCacheKey) is kept as is, as it is in the URL. The
			// disclosure projection that follows still masks known credential
			// values.
			return map[string]any{"name": typed["name"], "value": typed["value"]}
		}
		credentialValue := ScriptMapHasCredentialValue(typed)
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			if ctx.metadata &&
				key == dsl.ImportPreservedCredentialTargetsMetadataKey {
				// These are server-authored structural locators, not source
				// values. Masking "/password" as if it were a password creates
				// phantom binding requirements and makes the runtime locator
				// unusable after a masked read-modify-write.
				out[key] = ProjectImportCredentialTargetLocators(child)
				continue
			}
			if ctx.metadata &&
				key == dsl.ImportActionUIDMetadataKey &&
				dsl.IsValidImportActionUID(child) {
				out[key] = child
				continue
			}
			// Ranked locators of the step's own element (see
			// scriptdisclosure.IsSelectorCandidateList), kept as the step's
			// selector is.
			if ctx.actionMetadata && scriptdisclosure.IsSelectorCandidateList(key, child) {
				out[key] = child
				continue
			}
			// These import-only payloads retain exact source text in the durable
			// destination so the operator can finish a gated conversion without
			// re-importing or re-entering embedded credentials. They are never
			// executable and never cross an ordinary read/export boundary.
			if ctx.metadata && IsOpaqueImportSourcePayloadKey(key) {
				out[key] = MaskOpaqueImportSourcePayload(child)
				continue
			}
			// Java Vuser approval provenance is a server-authored governance
			// envelope. Its authoring-session UUID is an identifier, not runtime
			// session material. Preserve only the two exact, validated scalar
			// fields here; ordinary session_id keys continue through the normal
			// credential projection and remain masked.
			if key == JavaVuserApprovalEvidenceKey {
				out[key] = ProjectJavaVuserApprovalEvidence(child)
				continue
			}
			// A browser step's target is a URL only where the step navigates
			// or matches responses. A locator or window title is not one: read
			// as a URL, a CSS "#id" became a fragment to mask, and the review
			// offered the selector as a secret.
			if Text, ok := child.(string); ok && key == "target" && !ctx.metadata && !ctx.opaqueMap {
				actionType, _ := typed["type"].(string)
				switch dsl.BrowserTargetValueKind(actionType, Text) {
				case dsl.BrowserTargetLocator:
					out[key] = Text
					continue
				case dsl.BrowserTargetScript:
					out[key] = ctx.TextMemo.ScrubScriptText(Text)
					continue
				}
			}
			if ctx.secretBindings {
				if Text, ok := child.(string); ok && IsPortableSecretReference(Text) {
					out[key] = Text
				} else {
					// Bruno forwards every map value as executable credential
					// material regardless of its caller-controlled alias key.
					// Only an exact governed reference may survive projection.
					out[key] = MaskedScriptSecret
				}
				continue
			}
			// Proxy usernames are authentication material supplied alongside a
			// proxy password, not ordinary application identity fields. Preserve
			// them in the durable imported execution copy but mask them on stored
			// reads, result/review projections, and exports.
			if NormalizeScriptFieldName(ctx.key) == "proxy" &&
				NormalizeScriptFieldName(key) == "username" {
				if Text, ok := child.(string); ok {
					switch {
					case strings.TrimSpace(Text) == "":
						out[key] = Text
					case IsPortableTemplateBinding(Text):
						out[key] = Text
					default:
						out[key] = ProjectSensitiveScriptValue(Text)
					}
				} else {
					out[key] = MaskedScriptSecret
				}
				continue
			}
			// SOAPAction and operation are executable, adapter-defined strings.
			// They are not credential-named fields, but imported values may
			// contain URL userinfo, signed query parameters, or opaque
			// credential assignments. Treat the action as URL-shaped and both
			// fields as free text while retaining ordinary operation names.
			if NormalizeScriptFieldName(ctx.key) == "soap" {
				switch NormalizeScriptFieldName(key) {
				case "action":
					if Text, ok := child.(string); ok {
						out[key] = MaskScriptURL(Text)
					} else {
						out[key] = ProjectScriptNode(
							child,
							ScriptProjectionContext{key: key, opaqueMap: true},
						)
					}
					continue
				case "operation":
					if Text, ok := child.(string); ok {
						out[key] = ScrubScriptText(Text, false)
					} else {
						out[key] = ProjectScriptNode(
							child,
							ScriptProjectionContext{key: key, opaqueMap: true},
						)
					}
					continue
				}
			}
			// Import review evidence is client-extensible and frequently embeds
			// raw source snippets or sibling name/value pairs that the ordinary
			// script projection cannot associate. Apply the import-specific,
			// recursive disclosure boundary before this metadata can be persisted.
			if IsReviewEvidenceSubtreeKey(key) {
				out[key] = SanitizeImportReviewEvidenceWithMemo(child, ctx.TextMemo)
				continue
			}
			if credentialValue && strings.EqualFold(key, "value") {
				if Text, ok := child.(string); ok && IsPortableTemplateBinding(Text) {
					out[key] = Text
				} else if Text, ok := child.(string); ok {
					out[key] = ProjectSensitiveScriptValue(Text)
				} else {
					out[key] = MaskedScriptSecret
				}
				continue
			}
			if credentialValue && strings.EqualFold(key, "values") {
				values, ok := child.([]any)
				if !ok {
					out[key] = MaskedScriptSecret
					continue
				}
				projected := make([]any, len(values))
				for index, value := range values {
					if Text, ok := value.(string); ok && IsPortableTemplateBinding(Text) {
						projected[index] = Text
					} else if Text, ok := value.(string); ok {
						projected[index] = ProjectSensitiveScriptValue(Text)
					} else {
						projected[index] = MaskedScriptSecret
					}
				}
				out[key] = projected
				continue
			}
			if ctx.key == "sapgui" && !ctx.metadata && !ctx.opaqueMap && key == "session_behavior" {
				if Text, ok := child.(string); ok && dsl.IsSAPGUISessionBehavior(Text) {
					out[key] = Text
					continue
				}
			}
			childCtx := ScriptProjectionContext{
				TextMemo:          ctx.TextMemo,
				transactionParams: key == "params" && typed["type"] == "source_transaction",
				label:             ctx.label && (key == "value" || key == "raw"),
				key:               key,
				opaqueMap:         ctx.opaqueMap || IsOpaqueScriptMap(key),
				headers:           IsScriptHeaderMap(key),
				headerList:        IsScriptHeaderList(key),
				secretBindings:    IsScriptSecretBindingsMap(key),
				metadata:          ctx.metadata || NormalizeScriptFieldName(key) == "metadata",
			}
			if !ctx.metadata && !ctx.opaqueMap && NormalizeScriptFieldName(key) == "metadata" {
				_, actionShaped := typed["type"].(string)
				childCtx.actionMetadata = actionShaped
			}
			if ctx.transactionParams && (key == "args" || key == "call_args") {
				childCtx.transactionFunction, _ = typed["function"].(string)
			}
			if ctx.headers {
				out[key] = ProjectHeaderValue(key, child, ctx.TextMemo)
				continue
			}
			out[key] = ProjectScriptNode(child, childCtx)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			childCtx := ctx
			childCtx.label = credential.TransactionLabelArgument(ctx.transactionFunction, i)
			out[i] = ProjectScriptNode(child, childCtx)
		}
		return out
	case string:
		return ProjectScriptString(typed, ctx)
	case bool:
		// A boolean carries one bit and is never credential material, whatever
		// its key (a recorder's source_masked_secret or salesforce_auth flag).
		// Masked, it became the string sentinel: a phantom kept credential that
		// pinned every edit of the step, and a type the restore cannot put back.
		return typed
	default:
		if IsSensitiveScriptKey(ctx.key) && !IsSecretReferenceKey(ctx.key) && !isSecretGatedCount(ctx.key, value) {
			return MaskedScriptSecret
		}
		return value
	}
}

// isSecretGatedCount reports a number under secret_gated: the import fidelity
// report's count of secret-gated payloads, not secret material. Masked, it
// reads as a kept credential with script-wide context to every later save of
// a credential-preserving import. Any other value under that key, text above
// all, is masked like every secret-named value.
func isSecretGatedCount(key string, value any) bool {
	if NormalizeScriptFieldName(key) != "secretgated" || value == nil {
		return false
	}
	if _, number := value.(json.Number); number {
		return true
	}
	switch reflect.ValueOf(value).Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	default:
		return false
	}
}

func ProjectImportCredentialTargetLocators(value any) any {
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
		if !ValidImportCredentialLocatorText(target.Field, 1024, false) ||
			!ValidImportCredentialLocatorText(target.Pointer, 4096, true) ||
			!ValidImportCredentialLocatorText(target.RequestKey, 1024, true) ||
			target.Index < 0 || target.Occurrence < 0 {
			return []any{}
		}
	}
	return value
}

func ValidImportCredentialLocatorText(value string, maxBytes int, allowEmpty bool) bool {
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

func IsOpaqueImportSourcePayloadKey(key string) bool {
	switch NormalizeScriptFieldName(key) {
	case "jsr223sources", "k6gatedsource", "postmaneventsources",
		"credentialtraversalsourcesnapshotbase64", "devwebgatedsource",
		"jmeterjdbcsources", "enterpriseunsupportedproxysource",
		NormalizeScriptFieldName(dsl.ImportPreservedIgnoredProxyCredentialsKey),
		NormalizeScriptFieldName(dsl.ImportPreservedEnterpriseJavaVMSourceKey),
		NormalizeScriptFieldName(dsl.ImportPreservedEnterpriseJavaJDBCSourceKey):
		return true
	default:
		return false
	}
}

func MaskOpaqueImportSourcePayload(value any) any {
	switch typed := value.(type) {
	case []any:
		masked := make([]any, len(typed))
		for index := range typed {
			masked[index] = MaskOpaqueImportSourcePayload(typed[index])
		}
		return masked
	case map[string]any:
		masked := make(map[string]any, len(typed))
		for key, child := range typed {
			masked[key] = MaskOpaqueImportSourcePayload(child)
		}
		return masked
	case nil:
		return nil
	default:
		return MaskedScriptSecret
	}
}

func ProjectJavaVuserApprovalEvidence(value any) map[string]any {
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
	if hashOK && IsCanonicalSHA256(contentHash) {
		out["content_hash"] = contentHash
	}
	return out
}

func RestoreProjectedJavaVuserApprovalEvidence(projected, source *dsl.Script) {
	if projected == nil || source == nil ||
		!ScriptRequiresJavaVuserAuthoringApproval(source) ||
		source.Metadata == nil || source.Metadata.Import == nil {
		return
	}
	validated := ProjectJavaVuserApprovalEvidence(
		source.Metadata.Import.Preserved[JavaVuserApprovalEvidenceKey],
	)
	if len(validated) != 2 {
		ClearJavaVuserAuthoringApproval(projected)
		return
	}
	sessionID, sessionOK := validated["session_id"].(string)
	contentHash, hashOK := validated["content_hash"].(string)
	if !sessionOK || !hashOK {
		ClearJavaVuserAuthoringApproval(projected)
		return
	}
	WriteJavaVuserAuthoringApproval(projected, JavaVuserApprovalEvidence{
		SessionID:   sessionID,
		ContentHash: contentHash,
	})
}

const Sha256HexLength = 64

func IsCanonicalSHA256(value string) bool {
	const prefix = "sha256:"
	return strings.HasPrefix(value, prefix) &&
		IsCanonicalLowerHexDigest(strings.TrimPrefix(value, prefix), Sha256HexLength)
}

func IsCanonicalLowerHexDigest(value string, length int) bool {
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

// publicCacheKeyPair reports whether a map is exactly a recorded
// {name, value} query parameter whose value is a public cache key.
func publicCacheKeyPair(value map[string]any) bool {
	if len(value) != 2 {
		return false
	}
	name, nameOK := value["name"].(string)
	text, valueOK := value["value"].(string)
	return nameOK && valueOK && scrub.PublicCacheKey(name, text)
}

func ScriptMapHasCredentialValue(value map[string]any) bool {
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
		if Text, ok := value[key].(string); ok {
			hints.WriteByte(' ')
			hints.WriteString(strings.ToLower(Text))
		}
	}
	normalized := NormalizeScriptFieldName(hints.String())
	for _, marker := range []string{"password", "passwd", "passphrase", "secret", "token", "credential", "authorization", "cookie"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func ProjectHeaderEntry(entry map[string]any, memo *ImportProjectionTextMemo) map[string]any {
	out := make(map[string]any, len(entry))
	name, _ := entry["name"].(string)
	for key, value := range entry {
		if strings.EqualFold(key, "value") {
			out[key] = ProjectHeaderValue(name, value, memo)
			continue
		}
		out[key] = ProjectScriptNode(value, ScriptProjectionContext{key: key})
	}
	return out
}

func ProjectHeaderValue(name string, value any, memo *ImportProjectionTextMemo) any {
	Text, ok := value.(string)
	if !ok {
		return ProjectScriptNode(value, ScriptProjectionContext{key: name, opaqueMap: true, TextMemo: memo})
	}
	if credential.RecordedStateHeader(name) {
		// Cookies and session, anti-forgery and tracing headers are state the
		// source recorded, not credentials, so they show as recorded. The
		// disclosure projection that follows still masks a value that is a
		// credential held elsewhere in the script or a known project secret.
		return Text
	}
	if (scrub.Policy{}).SensitiveHeader(name) || IsSensitiveScriptKey(name) {
		if IsPortableTemplateBinding(Text) {
			return Text
		}
		return ProjectSensitiveScriptValue(Text)
	}
	if strings.EqualFold(strings.TrimSpace(name), "User-Agent") ||
		strings.EqualFold(strings.TrimSpace(name), "x-dtpc") {
		if IsPortableTemplateBinding(Text) {
			return Text
		}
		return ScrubScriptTextWithProjector(Text, false, func(value string) string {
			return scrub.ValueDescriptiveHeader(name, value, scrub.Policy{})
		})
	}
	if IsNonSecretIdentifierHeader(name) {
		return scrub.ValueURL(Text, scrub.Policy{})
	}
	return memo.ScrubScriptText(Text)
}

func ProjectScriptString(value string, ctx ScriptProjectionContext) string {
	if value == "" || IsPortableTemplateBinding(value) {
		return value
	}
	if ctx.metadata && NormalizeScriptFieldName(ctx.key) == "authmode" {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "anonymous", "blocked", "configured", "basic", "none":
			// Bounded server-authored authentication mode enums are review
			// locators, not credential material. Preserving only known enum values
			// avoids creating a bogus secret requirement while retaining the
			// fail-closed behavior for arbitrary metadata under this key.
			return value
		}
	}
	if ctx.metadata && (IsPortableImportProvenanceIdentifierKey(ctx.key) || IsSourceLocationProvenance(ctx.key, value)) {
		return scrub.ValueURL(value, scrub.Policy{})
	}
	if IsSecretReferenceKey(ctx.key) && IsNamedSecretReference(value) {
		return value
	}
	if IsNonSecretIdentifierScriptKey(ctx.key) {
		// Hub and SAS policy names are identifiers the reviewer must read, so
		// they skip the random-looking-text heuristic but keep the structured
		// credential scrub: a connection string pasted where a hub name
		// belongs still masks its SharedAccessKey.
		return ctx.TextMemo.Scrubber().ValueLabel(value)
	}
	if ctx.metadata && isCredentialReviewLocatorKey(ctx.key) {
		// Secret names and the paths a credential requirement points at are
		// identifiers the reviewer must read; the random-looking-text scrub
		// below hid them. The disclosure projection that follows still masks a
		// known credential inside them.
		return value
	}
	if IsSensitiveScriptKey(ctx.key) {
		return ProjectSensitiveScriptValue(value)
	}
	if IsScriptURLKey(ctx.key) {
		return MaskScriptURL(value)
	}
	if ctx.label || credential.LabelField(ctx.key) {
		return ctx.TextMemo.Scrubber().ValueLabel(value)
	}
	if NormalizeScriptFieldName(ctx.key) == "expression" {
		if _, generated := correlate.GeneratedSetCookieExtractor(value); generated {
			// The importer's own Set-Cookie extractor names a cookie and
			// captures its value; the name is an identifier, not a credential.
			return value
		}
	}
	if ctx.opaqueMap || IsScriptFreeTextKey(ctx.key) {
		return ctx.TextMemo.ScrubScriptText(value)
	}
	return value
}

func IsNonSecretIdentifierHeader(name string) bool {
	normalized := NormalizeScriptFieldName(name)
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

func MaskScriptURL(value string) string {
	value = ScrubScriptText(value, true)
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
		value = value[:authorityStart] + MaskedScriptSecret + value[at:]
	}
	return value
}

func NormalizeScriptFieldName(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	for _, r := range strings.ToLower(value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func IsSensitiveScriptKey(key string) bool {
	normalized := NormalizeScriptFieldName(key)
	// OAuth token_url identifies the remote endpoint; it is not token material.
	// Bruno's persist-session setting is likewise a boolean runtime control, not
	// session material. Keep these explicit exceptions ahead of the shared
	// component-aware vocabulary, which correctly treats standalone token and
	// session components as sensitive in otherwise opaque extension maps.
	if normalized == "tokenurl" || normalized == "persistsessionacrossiterations" ||
		normalized == "credentialpolicy" {
		return false
	}
	// Kafka's sasl.oauthbearer.token.endpoint.url is the same endpoint address
	// (IsScriptURLKey masks credentials in its userinfo), and an Event Hubs
	// shared_access_key_name is a SAS policy name, not the key.
	if isScriptTokenEndpointKey(normalized) || IsNonSecretIdentifierScriptKey(key) {
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

func IsSecretReferenceKey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "client_secret_ref", "dsn_secret", "sasl_password_secret", "connection_string_secret", "ca_cert_secret",
		"trust_store_ref", "credential_ref", "storage_ref":
		return true
	default:
		return false
	}
}

func IsPortableSecretReference(value string) bool {
	trimmed := strings.TrimSpace(value)
	return ScriptSecretTemplatePattern.MatchString(trimmed) && ScriptSecretTemplatePattern.FindString(trimmed) == trimmed
}

func IsPortableTemplateBinding(value string) bool {
	if strings.Contains(value, MaskedScriptSecret) {
		return false
	}
	references := PortableScriptTemplateReferences(value)
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

func ProjectSensitiveScriptValue(value string) string {
	trimmed := strings.TrimSpace(value)
	for _, scheme := range []string{"Bearer", "Basic", "Digest"} {
		if len(trimmed) > len(scheme) && strings.EqualFold(trimmed[:len(scheme)], scheme) && unicode.IsSpace(rune(trimmed[len(scheme)])) {
			return trimmed[:len(scheme)] + " " + MaskedScriptSecret
		}
	}
	return MaskedScriptSecret
}

func PortableScriptTemplateReferences(value string) []string {
	candidates := ScriptPortableTemplatePattern.FindAllString(value, -1)
	references := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if IsPortableScriptTemplateReference(candidate) {
			references = append(references, candidate)
		}
	}
	return references
}

func IsPortableScriptTemplateReference(value string) bool {
	if len(value) < 5 || !strings.HasPrefix(value, "{{") || !strings.HasSuffix(value, "}}") {
		return false
	}
	expr := strings.TrimSpace(value[2 : len(value)-2])
	if ScriptPortableReferencePattern.MatchString(expr) || ScriptPortableRandomPattern.MatchString(expr) || ScriptPortableRandomStringPattern.MatchString(expr) {
		return true
	}
	switch strings.TrimSuffix(expr, "()") {
	case "fn.uuid", "fn.uuid.v4",
		"fn.timestamp.unix", "fn.timestamp.unix_ms", "fn.timestamp.iso8601", "fn.timestamp.rfc3339",
		"fake.uuid", "fake.email":
		return true
	default:
		return false
	}
}

func ScrubScriptText(value string, urlValue bool) string {
	return ScrubScriptTextWithProjector(value, urlValue, nil)
}

func ScrubScriptTextWithProjector(value string, urlValue bool, projectText func(string) string) string {
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
						TrustedWholeValues: PortableBodyTemplateValues(value),
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
		value, urlSeparators = ProtectAdditionalURLParamSeparators(value)
	}
	value, xmlSecretBindings := ProtectPortableXMLSecretBindings(value)
	value, templateBindings := ProtectTemplateBindings(value)
	references := PortableScriptTemplateReferences(value)
	protected := value
	referencePlaceholderPrefix := ScriptProjectionPlaceholderPrefix(value, "binding")
	for i, reference := range references {
		placeholder := referencePlaceholderPrefix + strconv.Itoa(i) + "Z"
		protected = strings.Replace(protected, reference, placeholder, 1)
	}
	if urlValue {
		protected = scrub.ValueURL(protected, scrub.Policy{})
	} else if projectText != nil {
		protected = projectText(protected)
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
	protected = RedactedXMLMessageIDPattern.ReplaceAllString(protected, "${1}"+ProjectedXMLMessageID+"${2}")
	return protected
}

func PortableBodyTemplateValues(value string) map[string]struct{} {
	decoder := json.NewDecoder(strings.NewReader(value))
	trusted := make(map[string]struct{})
	for _, reference := range PortableScriptTemplateReferences(value) {
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
		Text, ok := token.(string)
		if !ok || !IsPortableTemplateBinding(Text) {
			continue
		}
		trusted[strings.TrimSpace(Text)] = struct{}{}
	}
	if len(trusted) == 0 {
		return nil
	}
	return trusted
}

func ProtectAdditionalURLParamSeparators(value string) (string, map[string]string) {
	if !strings.Contains(value, "?") {
		return value, nil
	}
	placeholderPrefix := ScriptProjectionPlaceholderPrefix(value, "urlseparator")
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

func ProtectPortableXMLSecretBindings(value string) (string, map[string]string) {
	protected := value
	replacements := map[string]string{}
	placeholderPrefix := ScriptProjectionPlaceholderPrefix(value, "xmlbinding")
	nextPlaceholder := func(binding string) string {
		// The trailing delimiter keeps index 1 from being a prefix of index 10,
		// so restoration remains safe regardless of map iteration order.
		placeholder := placeholderPrefix + strconv.Itoa(len(replacements)) + "Z"
		replacements[placeholder] = binding
		return placeholder
	}
	protected = PortableXMLSecretElementPattern.ReplaceAllStringFunc(protected, func(candidate string) string {
		return nextPlaceholder(ScrubPortableXMLElementOpeningTag(candidate))
	})
	protected = PortableXMLSecretAttributePattern.ReplaceAllStringFunc(protected, nextPlaceholder)
	protectPortable := func(candidate string) string {
		reference := ScriptPortableTemplatePattern.FindString(candidate)
		if !IsPortableScriptTemplateReference(reference) {
			return candidate
		}
		return nextPlaceholder(ScrubPortableXMLElementOpeningTag(candidate))
	}
	protected = PortableXMLTemplateElementPattern.ReplaceAllStringFunc(protected, protectPortable)
	protected = PortableXMLTemplateAttributePattern.ReplaceAllStringFunc(protected, protectPortable)
	return protected, replacements
}

func ScrubPortableXMLElementOpeningTag(candidate string) string {
	end := strings.IndexByte(candidate, '>')
	if end < 0 {
		return candidate
	}
	return scrub.Value(candidate[:end+1], scrub.Policy{}) + candidate[end+1:]
}

func ProtectTemplateBindings(value string) (string, map[string]string) {
	replacements := map[string]string{}
	placeholderPrefix := ScriptProjectionPlaceholderPrefix(value, "querybinding")
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == '?' || r == '&' || r == '#' })
	for _, part := range parts {
		_, rawValue, ok := strings.Cut(part, "=")
		if !ok || !IsPortableTemplateBinding(rawValue) {
			continue
		}
		placeholder := placeholderPrefix + strconv.Itoa(len(replacements)) + "=safe"
		replacements[placeholder] = part
		value = strings.Replace(value, part, placeholder, 1)
	}
	// A reference bound inside a nested URL is protected the same way.
	var references [][]int
	for _, match := range ScriptPortableTemplatePattern.FindAllStringIndex(value, -1) {
		if IsPortableScriptTemplateReference(value[match[0]:match[1]]) {
			references = append(references, match)
		}
	}
	offset := len(replacements)
	value, nested := scrub.ProtectNestedQueryPairs(value, references, func(index int) string {
		return placeholderPrefix + strconv.Itoa(offset+index) + "=safe"
	})
	maps.Copy(replacements, nested)
	return value, replacements
}

func ScriptProjectionPlaceholderPrefix(value, domain string) string {
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

func IsNamedSecretReference(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, MaskedScriptSecret) || len(value) > 512 {
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

func IsScriptURLKey(key string) bool {
	normalized := NormalizeScriptFieldName(key)
	switch normalized {
	case "baseurl", "url", "tokenurl", "brokerurl", "schemaregistryurl", "server", "origin", "target", "address":
		return true
	default:
		return isScriptTokenEndpointKey(normalized)
	}
}

// isScriptTokenEndpointKey reports Kafka's OAuth token endpoint setting
// (sasl.oauthbearer.token.endpoint.url) by its normalized name.
func isScriptTokenEndpointKey(normalized string) bool {
	return strings.HasSuffix(normalized, "tokenendpointurl")
}

// IsNonSecretIdentifierScriptKey reports fields that name a messaging
// resource rather than hold a credential: the event hub, and the Event Hubs
// SAS policy name (the key itself is the connection string's SharedAccessKey).
func IsNonSecretIdentifierScriptKey(key string) bool {
	switch NormalizeScriptFieldName(key) {
	case "sharedaccesskeyname", "eventhub":
		return true
	default:
		return false
	}
}

// isCredentialReviewLocatorKey reports import-review metadata that names a
// secret or the fields a credential requirement covers: a protected
// requirement's suggested_name and affected_paths, and the replay preflight's
// required and missing secret names.
func isCredentialReviewLocatorKey(key string) bool {
	switch NormalizeScriptFieldName(key) {
	case "suggestedname", "affectedpaths", "requiredsecrets", "missingsecrets":
		return true
	default:
		return false
	}
}

func IsScriptFreeTextKey(key string) bool {
	switch NormalizeScriptFieldName(key) {
	case "body", "value", "expected", "inlinedata", "schema", "objectschema",
		"expression", "left", "right", "runnercommand":
		return true
	default:
		return false
	}
}

func IsOpaqueScriptMap(key string) bool {
	switch NormalizeScriptFieldName(key) {
	case "params", "metadata", "preserved", "properties", "labels", "options",
		"additionalattributes", "requestmapping", "responsemapping", "namespaces":
		return true
	default:
		return false
	}
}

func IsScriptHeaderMap(key string) bool {
	switch NormalizeScriptFieldName(key) {
	case "headers", "defaultheaders":
		return true
	default:
		return false
	}
}

func IsScriptHeaderList(key string) bool {
	switch NormalizeScriptFieldName(key) {
	case "headerlist", "defaultheaderlist":
		return true
	default:
		return false
	}
}

func IsScriptSecretBindingsMap(key string) bool {
	return NormalizeScriptFieldName(key) == "secretbindings"
}

func ReconcileMaskedScriptSecrets(incoming *dsl.Script, prior *dsl.Script) error {
	return ReconcileMaskedScriptSecretsWithTrust(incoming, prior, false)
}

func ReconcileMaskedScriptSecretsWithTrust(
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
	hasRedactedRuntimeData := ScriptHasRedactedRuntimeCredentialSource(incoming)
	allowRedactedRuntimeData := hasRedactedRuntimeData &&
		ScriptHasExecutionBlockerCode(incoming, CredentialParameterSourceRedactedBlocker)
	if prior == nil && hasRedactedRuntimeData && !allowRedactedRuntimeData {
		return fmt.Errorf(
			"script contains redacted runtime data without the required %s execution gate",
			CredentialParameterSourceRedactedBlocker,
		)
	}
	incomingJSON, err := ScriptAsJSONValue(incoming)
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
	hasMaskedSecret := JsonValueContainsExecutableMaskedSecretWithOptions(
		incomingJSON,
		"",
		allowRedactedRuntimeData,
	)
	if prior == nil {
		if hasMaskedSecret {
			return fmt.Errorf("script contains an orphan %s sentinel; bind a secret reference instead", MaskedScriptSecret)
		}
		return nil
	}
	rawPrior, err := ScriptAsJSONValue(prior)
	if err != nil {
		return err
	}
	projectedPrior, err := ScriptAsJSONValue(MaskScriptSecrets(prior))
	if err != nil {
		return err
	}
	reconciled, err := ReconcileProjectedJSONWithOptions(
		incomingJSON,
		projectedPrior,
		rawPrior,
		"script",
		allowRedactedRuntimeData,
		IsCredentialPreservingImport(prior),
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

func ScriptAsJSONValue(script *dsl.Script) (any, error) {
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

func ReconcileProjectedJSONWithOptions(
	incoming any,
	projectedPrior any,
	rawPrior any,
	path string,
	allowRedactedRuntimeData bool,
	allowLiteralPriorSentinel bool,
) (any, error) {
	initialJSONPath := ""
	if path != "" && path != "script" {
		initialJSONPath = displayPathToJSONPointer(path)
	}
	return reconcileProjectedJSONWithContext(
		incoming,
		projectedPrior,
		rawPrior,
		path,
		initialJSONPath,
		RedactionTraversalContext{},
		false,
		allowRedactedRuntimeData,
		allowLiteralPriorSentinel,
	)
}

func displayPathToJSONPointer(displayPath string) string {
	trimmed := strings.TrimPrefix(displayPath, "script")
	trimmed = strings.TrimPrefix(trimmed, ".")
	if trimmed == "" {
		return ""
	}
	var out strings.Builder
	for i := 0; i < len(trimmed); {
		switch trimmed[i] {
		case '.':
			i++
		case '[':
			end := strings.IndexByte(trimmed[i:], ']')
			if end < 0 {
				return AppendJSONPointer(out.String(), trimmed[i:])
			}
			out.WriteString(AppendJSONPointer("", trimmed[i+1:i+end]))
			i += end + 1
		default:
			nextDot := strings.IndexAny(trimmed[i:], ".[")
			if nextDot < 0 {
				out.WriteString(AppendJSONPointer("", trimmed[i:]))
				i = len(trimmed)
			} else {
				out.WriteString(AppendJSONPointer("", trimmed[i:i+nextDot]))
				i += nextDot
			}
		}
	}
	return out.String()
}

func reconcileProjectedJSONWithContext(
	incoming any,
	projectedPrior any,
	rawPrior any,
	path string,
	jsonPath string,
	ctx RedactionTraversalContext,
	inNonWireProvenance bool,
	allowRedactedRuntimeData bool,
	allowLiteralPriorSentinel bool,
) (any, error) {
	switch typed := incoming.(type) {
	case string:
		if allowRedactedRuntimeData && IsLegacyRedactedRuntimeSourceTraversalPath(path) {
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
			if strings.Contains(raw, MaskedScriptSecret) && !allowLiteralPriorSentinel && !inNonWireProvenance {
				return nil, fmt.Errorf("%s contains an orphan %s sentinel; bind a secret reference instead", path, MaskedScriptSecret)
			}
			return raw, nil
		}
		if !strings.Contains(typed, MaskedScriptSecret) || inNonWireProvenance {
			return typed, nil
		}
		return nil, fmt.Errorf("%s contains an orphan %s sentinel; bind a secret reference instead", path, MaskedScriptSecret)
	case map[string]any:
		ctx = RedactionContextForMap(typed, jsonPath, ctx)
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
		if IsTransientScriptProjectionGate(typed, projectedMap, rawMap) {
			typed = WithoutTransientScriptProjectionGate(typed)
			projectedMap = WithoutTransientScriptProjectionGate(projectedMap)
		}
		priorWasReviewOnly := IsNonExecutableReviewAction(projectedMap) ||
			IsNonExecutableReviewAction(rawMap) ||
			IsDurablyImportReviewGatedAction(projectedMap) ||
			IsDurablyImportReviewGatedAction(rawMap)
		incomingReviewOnly := IsNonExecutableReviewAction(typed) ||
			IsDurablyImportReviewGatedAction(typed)
		if priorWasReviewOnly && !incomingReviewOnly &&
			JsonValueContainsExecutableMaskedSecretWithContext(typed, jsonPath, allowRedactedRuntimeData, ctx) {
			return nil, fmt.Errorf("%s promotes redacted review evidence into executable fields; bind a secret reference instead", path)
		}
		if incomingReviewOnly {
			return ReconcileMaskedImportHeaderFidelityBytes(
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
			if IsNonExecutableRedactionSubtree(typed, key, skipPath) {
				out[key] = child
				continue
			}
			childJSONPath := AppendJSONPointer(jsonPath, key)
			childInProvenance := inNonWireProvenance || IsImportProvenanceMetadataPathForContext(childJSONPath, ctx)
			var projectedChild, rawChild any
			if projectedMap != nil {
				projectedChild = projectedMap[key]
			}
			if rawMap != nil {
				rawChild = rawMap[key]
			}
			reconciled, err := reconcileProjectedJSONWithContext(
				child,
				projectedChild,
				rawChild,
				path+"."+key,
				childJSONPath,
				ctx,
				childInProvenance,
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
		containsMaskedSecret := JsonValueContainsExecutableMaskedSecretWithContext(
			typed,
			jsonPath,
			allowRedactedRuntimeData,
			ctx,
		)
		if allowRedactedRuntimeData && path == "script.data_sources" {
			// The gate permits durable table sentinels, not positional
			// reassociation. Keep detecting them at the collection boundary so
			// a reorder cannot restore a prior raw table into a different name.
			containsMaskedSecret = JsonValueContainsExecutableMaskedSecret(typed, path)
		}
		var actionMatches []int
		hasActionMatches := false
		if projectedOK && IsActionCollectionTraversalPath(path) {
			actionMatches, hasActionMatches = MatchCredentialPreservingActionCollectionWithContext(
				typed,
				projectedSlice,
				jsonPath,
				ctx,
				allowRedactedRuntimeData,
			)
		}
		if containsMaskedSecret &&
			(!projectedOK || !reflect.DeepEqual(typed, projectedSlice)) {
			stableFieldEdit := IsStableRuntimeDataSourceEdit(path, typed, projectedSlice) ||
				hasActionMatches ||
				IsStableJavaVMParameterEdit(path, typed, projectedSlice)
			if !stableFieldEdit {
				for i, child := range typed {
					incomingMap, incomingOK := child.(map[string]any)
					if !incomingOK {
						continue
					}
					priorIdx := i
					if hasActionMatches {
						priorIdx = actionMatches[i]
					}
					if priorIdx < 0 || priorIdx >= len(projectedSlice) {
						continue
					}
					projectedMap, _ := projectedSlice[priorIdx].(map[string]any)
					var rawMap map[string]any
					if priorIdx < len(rawSlice) {
						rawMap, _ = rawSlice[priorIdx].(map[string]any)
					}
					priorWasReviewOnly := IsNonExecutableReviewAction(projectedMap) ||
						IsNonExecutableReviewAction(rawMap) ||
						IsDurablyImportReviewGatedAction(projectedMap) ||
						IsDurablyImportReviewGatedAction(rawMap)
					incomingReviewOnly := IsNonExecutableReviewAction(incomingMap) ||
						IsDurablyImportReviewGatedAction(incomingMap)
					if priorWasReviewOnly && !incomingReviewOnly &&
						JsonValueContainsExecutableMaskedSecretWithContext(incomingMap, AppendJSONPointer(jsonPath, strconv.Itoa(i)), allowRedactedRuntimeData, ctx) {
						return nil, fmt.Errorf("%s[%d] promotes redacted review evidence into executable fields; bind a secret reference instead", path, i)
					}
				}
				return nil, fmt.Errorf("%s changed while containing redacted credentials; bind secret references before modifying or reordering it", path)
			}
		}
		if projectedOK && IsActionCollectionTraversalPath(path) && !hasActionMatches &&
			!reflect.DeepEqual(typed, projectedSlice) && actionCollectionHasProjectionResponseGate(typed) {
			// A step whose credential body this read replaced with a whole-body
			// sentinel (the transient script_projection gate) can only be
			// restored from the stored step it was matched to. Reconciling it by
			// position after the collection changed would persist the sentinel
			// body and the response-only gate as durable state.
			return nil, fmt.Errorf("%s changed while containing redacted credentials; bind secret references before modifying or reordering it", path)
		}
		if hasActionMatches {
			if index, carried := unmatchedStepCarriesProjectedValue(typed, projectedSlice, rawSlice, actionMatches); carried {
				// The step no longer matches the stored step whose projected
				// value it carries, so that value cannot be restored and saving
				// would replace the stored value with the read's placeholder.
				return nil, fmt.Errorf(
					"%s[%d] changes a step that holds stored values this read shows as placeholders; keep the step's type, method and target, or bind secret references before changing them",
					path,
					index,
				)
			}
		}
		out := make([]any, len(typed))
		for i, child := range typed {
			priorIdx := i
			if hasActionMatches {
				priorIdx = actionMatches[i]
			}
			var projectedChild, rawChild any
			if priorIdx >= 0 && priorIdx < len(projectedSlice) {
				projectedChild = projectedSlice[priorIdx]
			}
			if priorIdx >= 0 && priorIdx < len(rawSlice) {
				rawChild = rawSlice[priorIdx]
			}
			reconciled, err := reconcileProjectedJSONWithContext(
				child,
				projectedChild,
				rawChild,
				fmt.Sprintf("%s[%d]", path, i),
				AppendJSONPointer(jsonPath, strconv.Itoa(i)),
				ctx,
				inNonWireProvenance,
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

func ReconcileMaskedImportHeaderFidelityBytes(
	incoming,
	projected,
	raw map[string]any,
	path string,
) (map[string]any, error) {
	reconciled, err := ReconcileMaskedImportFidelityBytes(
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
	return ReconcileMaskedImportFidelityBytes(
		reconciled,
		projected,
		raw,
		path,
		dsl.ImportMultipartFidelityMetadataKey,
		"omitted_fields",
		"imported-multipart",
	)
}

func ReconcileMaskedImportFidelityBytes(
	incoming,
	projected,
	raw map[string]any,
	path,
	metadataKey,
	collectionKey,
	payloadLabel string,
) (map[string]any, error) {
	incomingMarker := ImportFidelityMarkerJSON(incoming, metadataKey)
	if incomingMarker == nil {
		return incoming, nil
	}
	incomingOccurrences, incomingOK := ImportFidelityOccurrencesJSON(
		incomingMarker,
		collectionKey,
	)
	if !incomingOK || !HeaderFidelityOccurrencesContainMask(incomingOccurrences) {
		return incoming, nil
	}
	projectedOccurrences, projectedOK := ImportFidelityOccurrencesJSON(
		ImportFidelityMarkerJSON(projected, metadataKey),
		collectionKey,
	)
	rawOccurrences, rawOK := ImportFidelityOccurrencesJSON(
		ImportFidelityMarkerJSON(raw, metadataKey),
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
			HeaderFidelityOccurrenceIdentity(incomingOccurrence),
			HeaderFidelityOccurrenceIdentity(projectedOccurrence),
		) || !reflect.DeepEqual(
			HeaderFidelityOccurrenceIdentity(incomingOccurrence),
			HeaderFidelityOccurrenceIdentity(rawOccurrence),
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
			if !incomingExists || incomingValue != MaskedImportHeaderBytes {
				continue
			}
			projectedValue, projectedExists := projectedOccurrence[key].(string)
			rawValue, rawExists := rawOccurrence[key].(string)
			if !projectedExists || projectedValue != MaskedImportHeaderBytes ||
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

func ImportFidelityMarkerJSON(
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

func ImportFidelityOccurrencesJSON(
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

func HeaderFidelityOccurrencesContainMask(occurrences []map[string]any) bool {
	for _, occurrence := range occurrences {
		for _, key := range []string{
			dsl.ImportHeaderFidelityPreservedNameBase64Key,
			dsl.ImportHeaderFidelityPreservedValueBase64Key,
		} {
			if value, _ := occurrence[key].(string); value == MaskedImportHeaderBytes {
				return true
			}
		}
	}
	return false
}

func HeaderFidelityOccurrenceIdentity(occurrence map[string]any) map[string]any {
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

func IsTransientScriptProjectionGate(
	incoming,
	projectedPrior,
	rawPrior map[string]any,
) bool {
	return HasScriptProjectionResponseGate(incoming) &&
		HasScriptProjectionResponseGate(projectedPrior) &&
		!HasScriptProjectionResponseGate(rawPrior) &&
		!IsDurablyImportReviewGatedAction(rawPrior)
}

func HasScriptProjectionResponseGate(action map[string]any) bool {
	if !IsScriptActionJSONValue(action) {
		return false
	}
	metadata, _ := action["metadata"].(map[string]any)
	if metadata == nil {
		return false
	}
	if !ScriptProjectionResponseGateMarker(metadata) {
		// Keep recognizing response projections emitted during the transition
		// before the explicit sidecar marker was introduced.
		review, _ := metadata["import_review"].(map[string]any)
		source, _ := review["source"].(string)
		if !strings.EqualFold(strings.TrimSpace(source), "script_projection") {
			return false
		}
	}
	for _, blocker := range ExecutionReviewBlockerCodes(metadata) {
		if blocker == "import_review_required" {
			return true
		}
	}
	return false
}

func ScriptProjectionResponseGateMarker(metadata map[string]any) bool {
	marker, _ := metadata[ScriptProjectionResponseGateMetadataKey].(map[string]any)
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

func WithoutTransientScriptProjectionGate(action map[string]any) map[string]any {
	if !HasScriptProjectionResponseGate(action) {
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
	delete(cleanMetadata, ScriptProjectionResponseGateMetadataKey)
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
				Text, _ := blocker.(string)
				if Text != "import_review_required" {
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

func JsonValueContainsExecutableMaskedSecret(value any, path string) bool {
	return JsonValueContainsExecutableMaskedSecretWithOptions(value, path, false)
}

func JsonValueContainsExecutableMaskedSecretWithOptions(
	value any,
	path string,
	allowRedactedRuntimeData bool,
) bool {
	return JsonValueContainsExecutableMaskedSecretWithContext(value, path, allowRedactedRuntimeData, RedactionTraversalContext{})
}

func JsonValueContainsExecutableMaskedSecretWithContext(value any, path string, allowRedactedRuntimeData bool, ctx RedactionTraversalContext) bool {
	switch typed := value.(type) {
	case string:
		return strings.Contains(typed, MaskedScriptSecret)
	case map[string]any:
		ctx = RedactionContextForMap(typed, path, ctx)
		if IsNonExecutableReviewAction(typed) || IsDurablyImportReviewGatedAction(typed) {
			return false
		}
		for key, child := range typed {
			if IsNonExecutableRedactionSubtree(typed, key, path) {
				continue
			}
			childPath := AppendJSONPointer(path, key)
			// Redacted imported parameter rows are safe durable placeholders,
			// not a project-secret binding. reconcileMaskedScriptSecrets permits
			// them only while the script retains the matching execution gate.
			if allowRedactedRuntimeData && IsLegacyRedactedRuntimeSourceTraversalPath(childPath) {
				continue
			}
			// Must mirror discoverRedactedValues: a provenance entry raises no
			// binding requirement, so treating it as an orphan sentinel here would
			// reject a save the user has no way to satisfy.
			if IsImportProvenanceMetadataPathForContext(childPath, ctx) {
				continue
			}
			if JsonValueContainsExecutableMaskedSecretWithContext(child, childPath, allowRedactedRuntimeData, ctx) {
				return true
			}
		}
	case []any:
		for index, child := range typed {
			childPath := AppendJSONPointer(path, strconv.Itoa(index))
			if allowRedactedRuntimeData &&
				IsLegacyRedactedRuntimeSourceTraversalPath(childPath) {
				continue
			}
			if JsonValueContainsExecutableMaskedSecretWithContext(
				child,
				childPath,
				allowRedactedRuntimeData,
				ctx,
			) {
				return true
			}
		}
	}
	return false
}

func IsStableRuntimeDataSourceEdit(path string, incoming, projected []any) bool {
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

// IsStableJavaVMParameterEdit reports whether an edited Java VM argument list
// keeps every masked argument at the index it was projected at. Adding,
// removing or changing a non-sensitive argument can then be reconciled
// positionally. Moving a masked argument, or deleting one while a later one
// remains, fails closed because a sentinel could be restored into a different
// system property; a sentinel appended past the projected list is refused later
// as an orphan. Deleting every masked argument leaves nothing to restore.
func IsStableJavaVMParameterEdit(path string, incoming, projected []any) bool {
	if path != "script.config.java_vuser.additional_vm_parameters" {
		return false
	}
	isMasked := func(values []any, index int) bool {
		if index >= len(values) {
			return false
		}
		text, _ := values[index].(string)
		return text == MaskedScriptSecret
	}
	for index := range projected {
		if isMasked(projected, index) != isMasked(incoming, index) {
			return false
		}
	}
	return true
}

func IsStableCredentialPreservingActionCollectionEdit(path string, incoming, projected []any) bool {
	if !IsActionCollectionTraversalPath(path) {
		return false
	}
	_, ok := MatchCredentialPreservingActionCollection(incoming, projected)
	return ok
}

func MatchCredentialPreservingActionCollection(incoming, projected []any) ([]int, bool) {
	return MatchCredentialPreservingActionCollectionWithContext(
		incoming,
		projected,
		"/actions",
		RedactionTraversalContext{httpProtocol: true},
		false,
	)
}

func extractValidImportActionUIDFromMap(action map[string]any) (string, bool) {
	if action == nil {
		return "", false
	}
	metadata, ok := action["metadata"].(map[string]any)
	if !ok || metadata == nil {
		return "", false
	}
	raw, exists := metadata[dsl.ImportActionUIDMetadataKey]
	if !exists || !dsl.IsValidImportActionUID(raw) {
		return "", false
	}
	return raw.(string), true
}

func actionCarriesProtectedMaskedMaterial(
	action map[string]any,
	elementJSONPath string,
	ctx RedactionTraversalContext,
	allowRedactedRuntimeData bool,
) bool {
	if JsonValueContainsExecutableMaskedSecretWithContext(action, elementJSONPath, allowRedactedRuntimeData, ctx) {
		return true
	}
	// The executable-sentinel scan skips review-gated steps, but the transient
	// script_projection gate marks a step whose credential body this read
	// replaced with a whole-body sentinel. Only its matched stored step can
	// restore that body.
	if HasScriptProjectionResponseGate(action) {
		return true
	}
	if metadata, ok := action["metadata"].(map[string]any); ok && metadata != nil {
		if _, hasHeaderFidelity := metadata[dsl.ImportHeaderFidelityMetadataKey]; hasHeaderFidelity {
			return true
		}
		if _, hasMultipartFidelity := metadata[dsl.ImportMultipartFidelityMetadataKey]; hasMultipartFidelity {
			return true
		}
	}
	return false
}

// actionCollectionHasProjectionResponseGate reports whether any step of an
// action collection carries the transient script_projection response gate.
func actionCollectionHasProjectionResponseGate(actions []any) bool {
	for _, item := range actions {
		if action, ok := item.(map[string]any); ok && HasScriptProjectionResponseGate(action) {
			return true
		}
	}
	return false
}

// unmatchedStepCarriesProjectedValue reports the first incoming step that
// matched no stored step yet carries a sentinel-free projected value (such as
// a redacted <messageId> shown as {{fn.uuid()}}) of a stored step that no
// incoming step matched either: the edited step itself, whose stored value
// reconciliation can no longer restore. A copy of a step that still matches
// keeps the placeholder, since the stored value survives on the original.
// Values carrying the [REDACTED] sentinel are governed by the protected
// material and orphan-sentinel rules instead.
func unmatchedStepCarriesProjectedValue(incoming, projected, raw []any, matches []int) (int, bool) {
	matchedPrior := make([]bool, len(projected))
	hasUnmatched := false
	for _, priorIdx := range matches {
		if priorIdx >= 0 && priorIdx < len(projected) {
			matchedPrior[priorIdx] = true
		} else {
			hasUnmatched = true
		}
	}
	if !hasUnmatched {
		return -1, false
	}
	placeholders := make(map[string]struct{})
	for index := range projected {
		if matchedPrior[index] || index >= len(raw) {
			continue
		}
		collectSentinelFreeProjectedStrings(projected[index], raw[index], placeholders)
	}
	if len(placeholders) == 0 {
		return -1, false
	}
	for index, priorIdx := range matches {
		if priorIdx >= 0 || index >= len(incoming) {
			continue
		}
		if jsonValueContainsAnyString(incoming[index], placeholders) {
			return index, true
		}
	}
	return -1, false
}

// collectSentinelFreeProjectedStrings adds every string leaf of projected that
// replaces a different stored string at the same location without carrying
// the [REDACTED] sentinel.
func collectSentinelFreeProjectedStrings(projected, raw any, out map[string]struct{}) {
	switch typed := projected.(type) {
	case string:
		if stored, ok := raw.(string); ok && stored != typed && !strings.Contains(typed, MaskedScriptSecret) {
			out[typed] = struct{}{}
		}
	case map[string]any:
		rawMap, _ := raw.(map[string]any)
		for key, child := range typed {
			collectSentinelFreeProjectedStrings(child, rawMap[key], out)
		}
	case []any:
		rawSlice, _ := raw.([]any)
		for index, child := range typed {
			var rawChild any
			if index < len(rawSlice) {
				rawChild = rawSlice[index]
			}
			collectSentinelFreeProjectedStrings(child, rawChild, out)
		}
	}
}

func jsonValueContainsAnyString(value any, texts map[string]struct{}) bool {
	switch typed := value.(type) {
	case string:
		_, found := texts[typed]
		return found
	case map[string]any:
		for _, child := range typed {
			if jsonValueContainsAnyString(child, texts) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if jsonValueContainsAnyString(child, texts) {
				return true
			}
		}
	}
	return false
}

func MatchCredentialPreservingActionCollectionWithContext(
	incoming,
	projected []any,
	collectionJSONPath string,
	ctx RedactionTraversalContext,
	allowRedactedRuntimeData bool,
) ([]int, bool) {
	projectedMaps := make([]map[string]any, len(projected))
	priorUIDs := make([]string, len(projected))
	priorByUID := make(map[string]int, len(projected))
	uidUniqueInPrior := true
	primaryKeys := make([]string, len(projected))
	primaryCounts := make(map[string]int, len(projected))
	secondaryKeys := make([]string, len(projected))
	secondaryOK := make([]bool, len(projected))
	compositeCounts := make(map[string]int, len(projected))

	for index, item := range projected {
		projectedMap, ok := item.(map[string]any)
		if !ok {
			return nil, false
		}
		projectedMaps[index] = projectedMap
		if uid, hasUID := extractValidImportActionUIDFromMap(projectedMap); hasUID {
			priorUIDs[index] = uid
			if _, duplicate := priorByUID[uid]; duplicate {
				uidUniqueInPrior = false
			} else {
				priorByUID[uid] = index
			}
		}
		primaryKey, ok := CredentialPreservingActionIdentityKey(projectedMap)
		if !ok {
			return nil, false
		}
		primaryKeys[index] = primaryKey
		primaryCounts[primaryKey]++
		sec, secOK := CredentialPreservingActionSecondaryIdentity(projectedMap)
		secondaryKeys[index] = sec
		secondaryOK[index] = secOK
		if secOK {
			compositeCounts[primaryKey+"\x00"+sec]++
		}
	}

	matches := make([]int, len(incoming))
	lastMatchedPrior := -1
	sameLength := len(incoming) == len(projected)

	for i, item := range incoming {
		incomingMap, ok := item.(map[string]any)
		if !ok {
			return nil, false
		}
		elemPath := AppendJSONPointer(collectionJSONPath, strconv.Itoa(i))
		carriesProtected := actionCarriesProtectedMaskedMaterial(incomingMap, elemPath, ctx, allowRedactedRuntimeData)
		inUID, hasUID := extractValidImportActionUIDFromMap(incomingMap)
		if hasUID && uidUniqueInPrior {
			if priorIdx, exists := priorByUID[inUID]; exists {
				if priorIdx <= lastMatchedPrior {
					if carriesProtected {
						return nil, false
					}
					matches[i] = -1
					continue
				}
				identityMatches := reflect.DeepEqual(
					CredentialPreservingActionIdentity(incomingMap),
					CredentialPreservingActionIdentity(projectedMaps[priorIdx]),
				)
				if !identityMatches {
					if carriesProtected {
						return nil, false
					}
					matches[i] = -1
					continue
				}
				if IsNonExecutableReviewAction(projectedMaps[priorIdx]) || IsDurablyImportReviewGatedAction(projectedMaps[priorIdx]) {
					if !(IsNonExecutableReviewAction(incomingMap) || IsDurablyImportReviewGatedAction(incomingMap)) && carriesProtected {
						return nil, false
					}
				}
				matches[i] = priorIdx
				lastMatchedPrior = priorIdx
				continue
			}
			// Unrecognized UID on a newly authored step.
			if carriesProtected {
				return nil, false
			}
			matches[i] = -1
			continue
		}

		// Legacy / UID-less fallback.
		inPrimary, ok := CredentialPreservingActionIdentityKey(incomingMap)
		if !ok {
			return nil, false
		}
		inSec, inSecOK := CredentialPreservingActionSecondaryIdentity(incomingMap)

		if sameLength && lastMatchedPrior == i-1 && priorUIDs[i] == "" && primaryKeys[i] == inPrimary {
			validSamePos := primaryCounts[inPrimary] == 1 ||
				reflect.DeepEqual(incomingMap, projectedMaps[i]) ||
				(inSecOK && secondaryOK[i] && inSec == secondaryKeys[i] && compositeCounts[inPrimary+"\x00"+inSec] == 1)
			if validSamePos {
				matches[i] = i
				lastMatchedPrior = i
				continue
			}
		}

		foundPrior := -1
		ambiguous := false
		for j := lastMatchedPrior + 1; j < len(projectedMaps); j++ {
			if priorUIDs[j] != "" {
				// Prevent a UID-stripped incoming action from matching a UID-stamped
				// prior action that held protected credentials.
				continue
			}
			if primaryKeys[j] != inPrimary {
				continue
			}
			if primaryCounts[inPrimary] > 1 {
				if !inSecOK || !secondaryOK[j] || inSec != secondaryKeys[j] || compositeCounts[inPrimary+"\x00"+inSec] != 1 {
					continue
				}
			}
			if foundPrior >= 0 {
				ambiguous = true
				break
			}
			foundPrior = j
		}
		if foundPrior >= 0 && !ambiguous {
			if IsNonExecutableReviewAction(projectedMaps[foundPrior]) || IsDurablyImportReviewGatedAction(projectedMaps[foundPrior]) {
				if !(IsNonExecutableReviewAction(incomingMap) || IsDurablyImportReviewGatedAction(incomingMap)) && carriesProtected {
					return nil, false
				}
			}
			matches[i] = foundPrior
			lastMatchedPrior = foundPrior
			continue
		}
		if carriesProtected {
			return nil, false
		}
		matches[i] = -1
	}

	return matches, true
}

func IsActionCollectionTraversalPath(path string) bool {
	switch path {
	case "script.init", "script.actions", "script.teardown":
		return true
	default:
		if strings.HasSuffix(path, ".control.steps") ||
			strings.HasSuffix(path, ".control.else") ||
			strings.HasSuffix(path, ".control.default") ||
			strings.HasSuffix(path, ".params.requests") {
			return true
		}
		if strings.HasSuffix(path, "].steps") &&
			(strings.Contains(path, ".control.cases[") || strings.Contains(path, ".control.branches[")) {
			return true
		}
		return false
	}
}

func IsActionCollectionContextPath(path string) bool {
	switch path {
	case "/init", "/actions", "/teardown":
		return true
	default:
		if strings.HasSuffix(path, "/control/steps") ||
			strings.HasSuffix(path, "/control/else") ||
			strings.HasSuffix(path, "/control/default") ||
			strings.HasSuffix(path, "/params/requests") {
			return true
		}
		if strings.HasSuffix(path, "/steps") &&
			(strings.Contains(path, "/control/cases/") || strings.Contains(path, "/control/branches/")) {
			return true
		}
		return false
	}
}

func CredentialPreservingActionIdentity(action map[string]any) map[string]any {
	identity := make(map[string]any, 5)
	for _, key := range []string{"type", "method", "target", "selector", "object_ref"} {
		if value, exists := action[key]; exists {
			identity[key] = value
		}
	}
	return identity
}

func CredentialPreservingActionIdentityKey(action map[string]any) (string, bool) {
	payload, err := json.Marshal(CredentialPreservingActionIdentity(action))
	if err != nil {
		return "", false
	}
	return string(payload), true
}

func CredentialPreservingActionSecondaryIdentity(action map[string]any) (string, bool) {
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

func ScriptHasRedactedRuntimeCredentialSource(script *dsl.Script) bool {
	if script == nil {
		return false
	}
	for _, source := range script.DataSources {
		if strings.Contains(source.InlineData, MaskedScriptSecret) {
			return true
		}
	}
	for _, parameter := range script.Parameters {
		for _, value := range parameter.Values {
			if strings.Contains(value, MaskedScriptSecret) {
				return true
			}
		}
	}
	return false
}

func ScriptHasExecutionBlockerCode(script *dsl.Script, want string) bool {
	if script == nil || strings.TrimSpace(want) == "" {
		return false
	}
	found := false
	err := dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		for _, blocker := range ExecutionReviewBlockerCodes(ref.Metadata()) {
			if strings.EqualFold(strings.TrimSpace(blocker), strings.TrimSpace(want)) {
				found = true
				return nil
			}
		}
		return nil
	})
	return found && err == nil
}

func IsRuntimeDataTableTraversalPath(path string) bool {
	if IsRuntimeDataTableJSONPath(path) {
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
		return index != "" && IsDecimalToken(index)
	}
	if strings.HasPrefix(path, hybridPrefix) && strings.HasSuffix(path, hybridSuffix) {
		index := strings.TrimSuffix(strings.TrimPrefix(path, hybridPrefix), hybridSuffix)
		return index != "" && IsDecimalToken(index)
	}
	return false
}

func IsLegacyRedactedRuntimeSourceTraversalPath(path string) bool {
	if IsRuntimeDataTableTraversalPath(path) {
		return true
	}
	if tokens, err := SplitJSONPointer(path); err == nil && len(tokens) == 4 {
		return tokens[0] == "parameters" &&
			IsDecimalToken(tokens[1]) &&
			tokens[2] == "values" &&
			IsDecimalToken(tokens[3])
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
		return ok && IsDecimalToken(parameterIndex) && IsDecimalToken(valueIndex)
	}
	if strings.HasPrefix(path, hybridPrefix) {
		remainder := strings.TrimPrefix(path, hybridPrefix)
		parameterIndex, valueIndex, ok := strings.Cut(remainder, hybridMiddle)
		return ok && IsDecimalToken(parameterIndex) && IsDecimalToken(valueIndex)
	}
	return false
}

func MaskScriptResponseValue(value any) any {
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
	return ProjectEmbeddedScriptNodes(raw)
}

func ProjectEmbeddedScriptNodes(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		if IsScriptJSONObject(typed) {
			return MaskScriptJSONObject(typed)
		}
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			out[key] = ProjectEmbeddedScriptNodes(child)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			out[i] = ProjectEmbeddedScriptNodes(child)
		}
		return out
	default:
		return value
	}
}

func MaskScriptJSONObject(value map[string]any) any {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var script dsl.Script
	if err := json.Unmarshal(payload, &script); err != nil {
		return nil
	}
	masked := MaskScriptSecrets(&script)
	if masked == nil {
		return nil
	}
	projected, err := ScriptAsJSONValue(masked)
	if err != nil {
		return nil
	}
	return projected
}

func IsScriptJSONObject(value map[string]any) bool {
	_, hasConfig := value["config"]
	_, hasActions := value["actions"]
	_, hasProtocol := value["protocol"]
	return hasConfig && hasActions && hasProtocol
}

func MaskRestoredImportBodies(projected, source *dsl.Script) {
	paths := map[string]bool{}
	_ = dslwalk.Walk(source, func(ref dslwalk.SurfaceRef) error {
		if dslwalk.MetadataBool(ref.Metadata(), "restored_body_protected") {
			paths[ref.Path] = true
		}
		return nil
	})
	_ = dslwalk.Walk(projected, func(ref dslwalk.SurfaceRef) error {
		if paths[ref.Path] {
			SetImportCommitSurfaceBody(ref, "[REDACTED]")
		}
		return nil
	})
}

var (
	SourceFileNamePattern   = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_ .\-\\/]{0,510}\.[A-Za-z]{1,5}$`)
	SourceIdentifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,255}$`)
)

func IsSourceLocationProvenance(key, value string) bool {
	switch NormalizeScriptFieldName(key) {
	case "sourcefile":
		return SourceFileNamePattern.MatchString(value)
	case "sourcefunction", "sourceactionname":
		return SourceIdentifierPattern.MatchString(value)
	}
	return false
}
