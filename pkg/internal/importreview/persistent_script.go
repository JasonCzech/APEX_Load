package importreview

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/internal/contentcontract"
)

var (
	ErrScriptConcurrentChange           = errors.New("script changed concurrently; reload and retry")
	ErrScriptCredentialBindingsRequired = errors.New("script contains executable credentials that require logical secret bindings")
)

func PreparePersistentLibraryScript(script, prior *dsl.Script) (*dsl.Script, error) {
	return PreparePersistentLibraryScriptWithImportTrust(script, prior, false, "")
}

// PreparePersistentLibraryScriptWithOptions is PreparePersistentLibraryScript
// under the system-admin retained-credential edit policy.
func PreparePersistentLibraryScriptWithOptions(
	script, prior *dsl.Script,
	options PreservedImportEditOptions,
) (*dsl.Script, error) {
	return preparePersistentLibraryScript(script, prior, false, "", options)
}

func PreparePersistentImportedLibraryScript(
	script, prior *dsl.Script,
	sourceProfile string,
) (*dsl.Script, error) {
	return PreparePersistentLibraryScriptWithImportTrust(
		script,
		prior,
		true,
		sourceProfile,
	)
}

func PreparePersistentLibraryScriptWithImportTrust(
	script, prior *dsl.Script,
	trustedImport bool,
	sourceProfile string,
) (*dsl.Script, error) {
	return preparePersistentLibraryScript(script, prior, trustedImport, sourceProfile, PreservedImportEditOptions{})
}

func preparePersistentLibraryScript(
	script, prior *dsl.Script,
	trustedImport bool,
	sourceProfile string,
	options PreservedImportEditOptions,
) (*dsl.Script, error) {
	_ = options
	candidate := CloneScriptDefinition(script)
	if candidate == nil {
		return nil, fmt.Errorf("failed to clone script for persistence")
	}
	StripImportDraftAttestation(candidate)
	script = candidate

	if err := ValidateJavaVMParameterSafety(script, true); err != nil {
		return nil, err
	}

	preserveImportedCredentials := trustedImport || IsCredentialPreservingImport(prior)
	if preserveImportedCredentials {
		if trustedImport {
			if strings.TrimSpace(sourceProfile) == "" && prior != nil &&
				prior.Metadata != nil && prior.Metadata.Import != nil {
				sourceProfile = prior.Metadata.Import.Source
			}
			if script.Metadata == nil {
				script.Metadata = &dsl.ScriptMetadata{}
			}
			if script.Metadata.Import == nil {
				script.Metadata.Import = &dsl.ImportMetadata{}
			}
			script.Metadata.Import.Source = strings.TrimSpace(sourceProfile)
		} else if prior != nil && prior.Metadata != nil && prior.Metadata.Import != nil {
			sourceProfile = prior.Metadata.Import.Source
			if script.Metadata == nil {
				script.Metadata = &dsl.ScriptMetadata{}
			}
			if script.Metadata.Import == nil {
				script.Metadata.Import = &dsl.ImportMetadata{}
			}
			script.Metadata.Import.Source = sourceProfile
		}
		EnsureCredentialPreservingImportMetadata(script, sourceProfile)
		if !trustedImport && prior != nil {
			if err := RestoreServerOwnedImportSecurityMetadata(script, prior); err != nil {
				return nil, err
			}
		} else {
			if err := renameDuplicateImportActionUIDs(script, prior); err != nil {
				return nil, err
			}
			pruneStaleImportedCredentialTargets(script)
		}
		if err := SanitizePersistentImportReviewSurfaces(script); err != nil {
			return nil, err
		}
		if !trustedImport && prior != nil {
			if script.Metadata != nil && script.Metadata.Import != nil &&
				strings.EqualFold(strings.TrimSpace(script.Metadata.Import.Source), "browser_recorder") {
				EnsureScriptImportActionUIDs(script)
			}
			maybeGraduateCredentialPreservingImport(script, prior)
		}
		return script, nil
	}

	StripProtectedImportSourceCommitFence(script)
	if prior != nil && prior.Metadata != nil && prior.Metadata.Import != nil {
		if strings.TrimSpace(prior.Metadata.Import.Source) != "" {
			if script.Metadata == nil {
				script.Metadata = &dsl.ScriptMetadata{}
			}
			if script.Metadata.Import == nil {
				script.Metadata.Import = &dsl.ImportMetadata{}
			}
			script.Metadata.Import.Source = prior.Metadata.Import.Source
		}
		if prior.Metadata.Import.Preserved != nil {
			for _, serverKey := range []string{
				contentcontract.ImportSourceCommitFenceMetadataKey,
				"effective_settings",
			} {
				if script.Metadata != nil && script.Metadata.Import != nil && script.Metadata.Import.Preserved != nil && serverKey == "effective_settings" {
					delete(script.Metadata.Import.Preserved, serverKey)
				}
				if priorVal, ok := prior.Metadata.Import.Preserved[serverKey]; ok {
					cloned, err := CloneImportSecurityMetadata(priorVal)
					if err == nil {
						EnsureScriptImportPreserved(script, prior.Metadata.Import.Source)[serverKey] = cloned
					}
				}
			}
		}
	}

	if err := renameDuplicateImportActionUIDs(script, prior); err != nil {
		return nil, err
	}
	if err := SanitizePersistentImportReviewSurfaces(script); err != nil {
		return nil, err
	}
	return script, nil
}

// renameDuplicateImportActionUIDs keeps the first step (in walk order) that
// carries each import_action_uid and gives every later copy a fresh
// "<uid>-copy-N" UID that no step in script or prior uses. A copy whose
// suffixed name would exceed the UID limit loses its UID, as a step duplicated
// in the editor does. Steps without a UID are left alone. Stored scripts must
// never repeat a UID: step matching treats a repeated stored UID as ambiguous
// and falls back to legacy matching, which skips every UID-stamped step.
func renameDuplicateImportActionUIDs(script, prior *dsl.Script) error {
	if script == nil {
		return nil
	}
	used := make(map[string]struct{})
	for _, s := range []*dsl.Script{script, prior} {
		if s == nil {
			continue
		}
		if err := dslwalk.Walk(s, func(ref dslwalk.SurfaceRef) error {
			if uid, ok := ref.Metadata()[dsl.ImportActionUIDMetadataKey].(string); ok && uid != "" {
				used[uid] = struct{}{}
			}
			return nil
		}); err != nil {
			return fmt.Errorf("inspect import action identifiers: %w", err)
		}
	}
	seen := make(map[string]struct{})
	copies := make(map[string]int)
	if err := dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		meta := ref.Metadata()
		uidVal, ok := meta[dsl.ImportActionUIDMetadataKey]
		if !ok || !dsl.IsValidImportActionUID(uidVal) {
			return nil
		}
		uid := uidVal.(string)
		if _, dup := seen[uid]; !dup {
			seen[uid] = struct{}{}
			return nil
		}
		next := ""
		for {
			copies[uid]++
			next = fmt.Sprintf("%s-copy-%d", uid, copies[uid]+1)
			if _, taken := used[next]; !taken {
				break
			}
		}
		if dsl.IsValidImportActionUID(next) {
			used[next] = struct{}{}
			seen[next] = struct{}{}
			meta[dsl.ImportActionUIDMetadataKey] = next
		} else {
			delete(meta, dsl.ImportActionUIDMetadataKey)
		}
		ref.SetMetadata(meta)
		return nil
	}); err != nil {
		return fmt.Errorf("rename duplicate import action identifiers: %w", err)
	}
	return nil
}

// EnsureScriptImportActionUIDs stamps a deterministic, unique import_action_uid
// on every action and nested sequence request that does not already carry one.
func EnsureScriptImportActionUIDs(script *dsl.Script) {
	if script == nil {
		return
	}
	seen := make(map[string]struct{})
	_ = dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		meta := ref.Metadata()
		if existing, ok := meta[dsl.ImportActionUIDMetadataKey]; ok && dsl.IsValidImportActionUID(existing) {
			uid := existing.(string)
			if _, dup := seen[uid]; !dup {
				seen[uid] = struct{}{}
				return nil
			}
		}
		sum := sha256.Sum256([]byte(script.Name + "\x00" + ref.Path + "\x00" + ref.Name()))
		uid := "act-" + hex.EncodeToString(sum[:8])
		for attempt := 2; ; attempt++ {
			if _, dup := seen[uid]; !dup {
				break
			}
			uid = fmt.Sprintf("act-%s-%d", hex.EncodeToString(sum[:8]), attempt)
		}
		seen[uid] = struct{}{}
		if meta == nil {
			meta = make(map[string]any)
		}
		meta[dsl.ImportActionUIDMetadataKey] = uid
		ref.SetMetadata(meta)
		return nil
	})
}

// maybeGraduateCredentialPreservingImport transitions an edited imported script
// out of credential-preserving mode once all retained credentials present in
// prior have been bound to runtime {{secret.*}} references (or removed), no
// opaque source credential envelopes remain, no runtime or disclosure
// credential values are retained, and the script validates cleanly under
// ordinary library rules. It reports whether the script graduated.
func maybeGraduateCredentialPreservingImport(script, prior *dsl.Script) bool {
	if script == nil || prior == nil || script.Metadata == nil || script.Metadata.Import == nil || script.Metadata.Import.Preserved == nil {
		return false
	}
	if ScriptRequiresJavaVuserAuthoringApproval(script) || ScriptRequiresJavaVuserAuthoringApproval(prior) {
		return false
	}
	if !scriptRetainsGraduableCredentialState(prior) {
		return false
	}
	preservedMap := script.Metadata.Import.Preserved
	for _, opaqueKey := range []string{
		dsl.ImportPreservedRuntimeArtifactKey,
		dsl.ImportPreservedCredentialParameterNamesKey,
		dsl.ImportPreservedCredentialTargetsMetadataKey,
		"recorded_resource_urls",
		"jsr223_sources",
		"k6_gated_source",
		"postman_event_sources",
		"credential_traversal_source_snapshot_base64",
		"devweb_gated_source",
		dsl.ImportPreservedJMeterJDBCSourcesKey,
		dsl.ImportPreservedEnterpriseUnsupportedProxySourceKey,
		dsl.ImportPreservedIgnoredProxyCredentialsKey,
		dsl.ImportPreservedEnterpriseJavaVMSourceKey,
		dsl.ImportPreservedEnterpriseJavaJDBCSourceKey,
		"recorded_input_secret_columns",
	} {
		if val, exists := preservedMap[opaqueKey]; exists && !isEmptyGraduationMetadataValue(val) {
			return false
		}
	}
	if ValidateJavaVMParameterSafety(script, false) != nil {
		return false
	}
	if retained, err := engine.ImportedCredentialValues(script); err != nil || len(retained) > 0 {
		return false
	}
	projected := MaskScriptSecrets(script)
	if projected == nil || len(DiscoverSecretBindingRequirements(projected, 0)) > 0 {
		return false
	}
	if !scriptsEqualExceptImportGraduationMetadata(script, projected) {
		return false
	}
	// Check whether ordinary validation (without ImportPreservedCredentialPolicyPreserve,
	// which SAP GUI and preserved Java VM argv rely on) passes cleanly.
	trial := CloneScriptDefinition(script)
	if trial == nil || trial.Metadata == nil || trial.Metadata.Import == nil || trial.Metadata.Import.Preserved == nil {
		return false
	}
	trial.Metadata.Import.Preserved[dsl.ImportPreservedCredentialPolicyKey] = "secret_reference"
	_ = dslwalk.Walk(trial, func(ref dslwalk.SurfaceRef) error {
		if meta := ref.Metadata(); meta != nil {
			delete(meta, dsl.ImportPreservedCredentialTargetsMetadataKey)
			if len(meta) == 0 {
				meta = nil
			}
			ref.SetMetadata(meta)
		}
		return nil
	})
	if len(dsl.ValidateScriptIssues(trial)) > 0 {
		return false
	}
	preservedMap[dsl.ImportPreservedCredentialPolicyKey] = "secret_reference"
	_ = dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		if meta := ref.Metadata(); meta != nil {
			delete(meta, dsl.ImportPreservedCredentialTargetsMetadataKey)
			if len(meta) == 0 {
				meta = nil
			}
			ref.SetMetadata(meta)
		}
		return nil
	})
	return true
}

func scriptRetainsGraduableCredentialState(script *dsl.Script) bool {
	if script == nil {
		return false
	}
	if retained, err := engine.ImportedCredentialValues(script); err != nil || len(retained) > 0 {
		return true
	}
	hasActionCredentialTargets := false
	_ = dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		if meta := ref.Metadata(); meta != nil {
			if val, exists := meta[dsl.ImportPreservedCredentialTargetsMetadataKey]; exists && !isEmptyGraduationMetadataValue(val) {
				hasActionCredentialTargets = true
			}
		}
		return nil
	})
	if hasActionCredentialTargets {
		return true
	}
	projected := MaskScriptSecrets(script)
	if projected == nil {
		return true
	}
	return !scriptsEqualExceptImportGraduationMetadata(script, projected)
}

func scriptsEqualExceptImportGraduationMetadata(rawScript, projectedScript *dsl.Script) bool {
	clean := func(in *dsl.Script) (any, bool) {
		cloned := CloneScriptDefinition(in)
		if cloned == nil {
			return nil, false
		}
		if cloned.Metadata != nil {
			cloned.Metadata.Import = nil
		}
		_ = dslwalk.Walk(cloned, func(ref dslwalk.SurfaceRef) error {
			if meta := ref.Metadata(); meta != nil {
				delete(meta, dsl.ImportPreservedCredentialTargetsMetadataKey)
				ref.SetMetadata(meta)
			}
			return nil
		})
		val, err := ScriptAsJSONValue(cloned)
		if err != nil {
			return nil, false
		}
		return val, true
	}
	rawVal, ok1 := clean(rawScript)
	projVal, ok2 := clean(projectedScript)
	return ok1 && ok2 && reflect.DeepEqual(rawVal, projVal)
}

func isEmptyGraduationMetadataValue(value any) bool {
	if value == nil {
		return true
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed) == ""
	case []string:
		return len(typed) == 0
	case []any:
		return len(typed) == 0
	case map[string]any:
		return len(typed) == 0
	case map[string]string:
		return len(typed) == 0
	default:
		return false
	}
}
