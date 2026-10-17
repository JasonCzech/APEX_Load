package importreview

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"sort"
	"strings"
	"time"
)

const (
	ImportDraftAttestationMetadataKey      = "server_import_attestation_v1"
	ImportDraftRecordingLineageMetadataKey = "server_recording_lineage_v1"
	ImportDraftProtectedTokenPrefix        = "protected:"
	ImportDraftAttestationTTL              = 30 * time.Minute
	ImportDraftAttestationCapacity         = 128
)

type ImportDraftCredentialFingerprint struct {
	CredentialDigests    map[string]int
	StructureDigest      string
	ExecutionGraphDigest string
	FullDigest           string
	HasRawCredentials    bool
}

func ImportDraftScriptPointers(values ...any) []*dsl.Script {
	result := make([]*dsl.Script, 0)
	for _, value := range values {
		switch typed := value.(type) {
		case *dsl.Script:
			result = append(result, typed)
		case []*dsl.Script:
			result = append(result, typed...)
		case []dsl.Script:
			for index := range typed {
				result = append(result, &typed[index])
			}
		case []any:
			result = append(result, ImportDraftScriptPointers(typed...)...)
		}
	}
	return result
}

func ImportDraftAttestationToken(script *dsl.Script) string {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil {
		return ""
	}
	token, _ := script.Metadata.Import.Preserved[ImportDraftAttestationMetadataKey].(string)
	return strings.TrimSpace(token)
}

func StripImportDraftAttestation(script *dsl.Script) {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil ||
		script.Metadata.Import.Preserved == nil {
		return
	}
	delete(script.Metadata.Import.Preserved, ImportDraftAttestationMetadataKey)
	delete(script.Metadata.Import.Preserved, ImportDraftRecordingLineageMetadataKey)
}

func FingerprintImportDraftCredentials(
	script *dsl.Script,
) (ImportDraftCredentialFingerprint, error) {
	fingerprint, _, err := FingerprintImportDraftCredentialsWithProjection(script, nil)
	return fingerprint, err
}

type ImportDraftProjection struct {
	script                 *dsl.Script
	rawJSON, projectedJSON any
}

func FingerprintImportDraftCredentialsWithProjection(
	script *dsl.Script,
	memo *ImportProjectionTextMemo,
) (ImportDraftCredentialFingerprint, ImportDraftProjection, error) {
	if script == nil {
		return ImportDraftCredentialFingerprint{}, ImportDraftProjection{}, errors.New("import draft is required")
	}
	executionGraphDigest, err := CanonicalImportDraftExecutionGraphDigest(script)
	if err != nil {
		return ImportDraftCredentialFingerprint{}, ImportDraftProjection{}, err
	}
	raw := ImportResponseRawScriptValue(script)
	projectedScript := MaskScriptSecretsWithMemo(script, memo)
	projection := ImportDraftProjection{script: projectedScript}
	if _, converted := raw.(map[string]any); converted {
		projection.rawJSON = raw
	}
	if projectedScript == nil {
		digest, err := FullImportDraftExecutionDigest(script)
		return ImportDraftCredentialFingerprint{
			ExecutionGraphDigest: executionGraphDigest,
			FullDigest:           digest,
			HasRawCredentials:    err == nil,
		}, projection, err
	}
	projected := ImportResponseRawScriptValue(projectedScript)
	if _, converted := projected.(map[string]any); converted {
		projection.projectedJSON = projected
	}
	requirements := SecretBindingRequirementsForImportScripts(projected)
	if OpaqueImportSourceProjectionChanged(raw, projected) || UnlocatedRequestBodyProjectionChanged(script, projectedScript, requirements) {
		digest, err := FullImportDraftExecutionDigest(script)
		return ImportDraftCredentialFingerprint{
			ExecutionGraphDigest: executionGraphDigest,
			FullDigest:           digest,
			HasRawCredentials:    err == nil,
		}, projection, err
	}
	if len(requirements) == 0 {
		if RuntimeCredentialSourceProjectionChanged(script, projectedScript) ||
			ImportHeaderFidelityProjectionChanged(script, projectedScript) {
			digest, err := FullImportDraftExecutionDigest(script)
			return ImportDraftCredentialFingerprint{
				ExecutionGraphDigest: executionGraphDigest,
				FullDigest:           digest,
				HasRawCredentials:    err == nil,
			}, projection, err
		}
		return ImportDraftCredentialFingerprint{
			ExecutionGraphDigest: executionGraphDigest,
		}, projection, nil
	}
	values := SourceSecretValuesForImportNodes(raw, projected, requirements)
	if len(values) == 0 {
		return ImportDraftCredentialFingerprint{
			ExecutionGraphDigest: executionGraphDigest,
		}, projection, nil
	}
	if len(values) != len(requirements) {
		digest, err := FullImportDraftExecutionDigest(script)
		return ImportDraftCredentialFingerprint{
			ExecutionGraphDigest: executionGraphDigest,
			FullDigest:           digest,
			HasRawCredentials:    err == nil,
		}, projection, err
	}
	digests := make(map[string]int, len(values))
	requirementsByID := make(map[string]SecretBindingRequirement, len(requirements))
	ids := make([]string, 0, len(requirements))
	for _, requirement := range requirements {
		requirementsByID[requirement.ID] = requirement
		if _, available := values[requirement.ID]; available {
			ids = append(ids, requirement.ID)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		requirement := requirementsByID[id]
		location := requirement.Path + "\x00" +
			fmt.Sprintf("%d", requirement.Occurrence)
		sum := sha256.Sum256([]byte(
			"apexload.import-draft-credential.v2\x00" +
				location + "\x00" + values[id],
		))
		digests[hex.EncodeToString(sum[:])]++
	}
	structureDigest, err := CanonicalImportDraftStructureDigestFromProjection(projectedScript)
	if err != nil {
		return ImportDraftCredentialFingerprint{}, projection, err
	}
	return ImportDraftCredentialFingerprint{
		CredentialDigests:    digests,
		StructureDigest:      structureDigest,
		ExecutionGraphDigest: executionGraphDigest,
		HasRawCredentials:    true,
	}, projection, nil
}

func OpaqueImportSourceProjectionChanged(raw, projected any) bool {
	switch rawValue := raw.(type) {
	case map[string]any:
		projectedValue, _ := projected.(map[string]any)
		for key, child := range rawValue {
			projectedChild := projectedValue[key]
			if IsOpaqueImportSourcePayloadKey(key) {
				rawPayload, rawErr := json.Marshal(child)
				projectedPayload, projectedErr := json.Marshal(projectedChild)
				if rawErr != nil || projectedErr != nil ||
					string(rawPayload) != string(projectedPayload) {
					return true
				}
				continue
			}
			if OpaqueImportSourceProjectionChanged(child, projectedChild) {
				return true
			}
		}
	case []any:
		projectedValue, _ := projected.([]any)
		for index, child := range rawValue {
			// Ordinary review/provenance arrays may be shortened by disclosure
			// sanitization. That is not an opaque executable-source delta by itself.
			// Continue into a missing child with nil so only a nested key explicitly
			// recognized by isOpaqueImportSourcePayloadKey can authorize a full-draft
			// attestation.
			var projectedChild any
			if index < len(projectedValue) {
				projectedChild = projectedValue[index]
			}
			if OpaqueImportSourceProjectionChanged(child, projectedChild) {
				return true
			}
		}
	}
	return false
}

func CanonicalImportDraftStructureDigestFromProjection(projected *dsl.Script) (string, error) {
	// Fingerprinting already has a private disclosure copy. Clone it before
	// canonicalization rather than performing the full credential projection a
	// second time; callers retain ownership of their immutable projection.
	projected = CloneScriptDefinition(projected)
	if projected == nil {
		return "", errors.New("project import draft structure")
	}
	StripImportDraftAttestation(projected)
	if err := SanitizePersistentImportReviewSurfaces(projected); err != nil {
		return "", err
	}
	// A disclosure response may already carry the transient marker before
	// projection runs again. It identifies response-only masking, not source
	// behavior. Keep all durable review gates in the digest, but do not let
	// this marker prevent receipt recovery for the same authoritative draft.
	if err := dslwalk.Walk(projected, func(ref dslwalk.SurfaceRef) error {
		metadata := ref.Metadata()
		if ScriptProjectionResponseGateMarker(metadata) {
			delete(metadata, ScriptProjectionResponseGateMetadataKey)
		}
		return nil
	}); err != nil {
		return "", fmt.Errorf("inspect projected import draft markers: %w", err)
	}
	// An import save stamps the digest of what it writes; a draft never
	// carries it.
	projected.Metadata = WithoutImportedContentDigest(projected.Metadata)
	projected.ID = ""
	projected.RefKey = ""
	projected.Name = ""
	projected.ProjectID = ""
	projected.FolderID = nil
	projected.CreatedAt = time.Time{}
	projected.UpdatedAt = time.Time{}
	value, err := ScriptAsJSONValue(projected)
	if err != nil {
		return "", err
	}
	value = CanonicalizeImportDraftCredentialTokens(value)
	payload, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("marshal import draft structure: %w", err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func CanonicalImportDraftExecutionGraphDigest(script *dsl.Script) (string, error) {
	// Recording lineage is an authorization boundary, not an equivalence
	// comparison. Hash the exact executable graph, including credential bytes
	// and exact secret-reference names. Server-owned binding/parameterization
	// endpoints rotate the capability after their mutation, so no
	// credential-canonicalization is necessary or safe here.
	return ScriptCorrelationActionGraphDigest(script)
}

func CanonicalizeImportDraftCredentialTokens(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			out[key] = CanonicalizeImportDraftCredentialTokens(child)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, child := range typed {
			out[index] = CanonicalizeImportDraftCredentialTokens(child)
		}
		return out
	case string:
		const slot = "__APEXLOAD_IMPORTED_CREDENTIAL_SLOT__"
		normalized := strings.ReplaceAll(typed, MaskedScriptSecret, slot)
		return ScriptSecretTemplatePattern.ReplaceAllString(normalized, slot)
	default:
		return value
	}
}

func RuntimeCredentialSourceProjectionChanged(raw, projected *dsl.Script) bool {
	if raw == nil || projected == nil {
		return false
	}
	if len(raw.Config.JavaVuser.AdditionalVMParameters) !=
		len(projected.Config.JavaVuser.AdditionalVMParameters) {
		return true
	}
	for index := range raw.Config.JavaVuser.AdditionalVMParameters {
		if raw.Config.JavaVuser.AdditionalVMParameters[index] !=
			projected.Config.JavaVuser.AdditionalVMParameters[index] {
			return true
		}
	}
	if UnlocatedRequestBodyProjectionChanged(raw, projected, nil) {
		return true
	}
	if len(raw.DataSources) != len(projected.DataSources) ||
		len(raw.Parameters) != len(projected.Parameters) {
		return false
	}
	for index := range raw.DataSources {
		if raw.DataSources[index].InlineData != projected.DataSources[index].InlineData {
			return true
		}
	}
	for index := range raw.Parameters {
		if len(raw.Parameters[index].Values) != len(projected.Parameters[index].Values) {
			return false
		}
		for valueIndex := range raw.Parameters[index].Values {
			if raw.Parameters[index].Values[valueIndex] != projected.Parameters[index].Values[valueIndex] {
				return true
			}
		}
	}
	return false
}

func UnlocatedRequestBodyProjectionChanged(raw, projected *dsl.Script, requirements []SecretBindingRequirement) bool {
	type bodyIdentity struct{ body, encoding string }
	bodies := map[string]bodyIdentity{}
	readBody := func(ref dslwalk.SurfaceRef) bodyIdentity {
		if ref.Action != nil {
			return bodyIdentity{ref.Action.Body, ref.Action.BodyEncoding}
		}
		body, _ := ref.Request["body"].(string)
		encoding, _ := ref.Request["body_encoding"].(string)
		return bodyIdentity{body, encoding}
	}
	covered := map[string][]SecretBindingRequirement{}
	for _, requirement := range requirements {
		covered[requirement.Path] = append(covered[requirement.Path], requirement)
	}
	if err := dslwalk.Walk(raw, func(ref dslwalk.SurfaceRef) error { bodies[ref.Path] = readBody(ref); return nil }); err != nil {
		return true
	}
	changed := false
	visited := 0
	if err := dslwalk.Walk(projected, func(ref dslwalk.SurfaceRef) error {
		expected, exists := bodies[ref.Path]
		actual := readBody(ref)
		visited++
		if !exists || expected.encoding != actual.encoding {
			changed = true
			return nil
		}
		if expected.body == actual.body {
			return nil
		}
		bodyRequirements := covered[DslWalkPathJSONPointer(ref.Path)+"/body"]
		removed, aligned := ExtractLinearlyProjectedRedactions(expected.body, actual.body)
		if !aligned || len(removed) == 0 || len(removed) != len(bodyRequirements) {
			changed = true
			return nil
		}
		seen := map[int]bool{}
		for _, requirement := range bodyRequirements {
			if requirement.Occurrence < 0 || requirement.Occurrence >= len(removed) || seen[requirement.Occurrence] {
				changed = true
				return nil
			}
			seen[requirement.Occurrence] = true
		}
		return nil
	}); err != nil || changed || visited != len(bodies) {
		return true
	}
	return false
}

func FullImportDraftExecutionDigest(script *dsl.Script) (string, error) {
	clone := CloneScriptDefinition(script)
	if clone == nil {
		return "", errors.New("clone import draft")
	}
	StripImportDraftAttestation(clone)
	if err := SanitizePersistentImportReviewSurfaces(clone); err != nil {
		return "", fmt.Errorf("sanitize import draft fingerprint: %w", err)
	}
	clone.ID = ""
	clone.RefKey = ""
	clone.Name = ""
	clone.ProjectID = ""
	clone.FolderID = nil
	clone.CreatedAt = time.Time{}
	clone.UpdatedAt = time.Time{}
	payload, err := json.Marshal(clone)
	if err != nil {
		return "", fmt.Errorf("marshal import draft fingerprint: %w", err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}
