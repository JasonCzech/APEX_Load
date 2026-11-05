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
	candidate := CloneScriptDefinition(script)
	if candidate == nil {
		return nil, fmt.Errorf("failed to clone script for persistence")
	}
	// This opaque, short-lived capability authorizes one compatibility save;
	// it is never durable import provenance and must not enter storage.
	StripImportDraftAttestation(candidate)
	script = candidate
	// Only an authenticated importer commit or a durable server-owned prior may
	// carry exact credential-bearing JVM argv. Structural/process-control
	// validation remains universal.
	preserveImportedCredentials := trustedImport || IsCredentialPreservingImport(prior)
	if err := ValidateJavaVMParameterSafety(script, preserveImportedCredentials); err != nil {
		return nil, err
	}
	// A durable prior marker is server-owned evidence because ordinary create
	// paths below cannot establish it. This permits masked read-modify-write
	// without requiring credential re-entry while rejecting a forged marker on
	// a new or previously ordinary script.
	if preserveImportedCredentials {
		preserved := script
		if trustedImport {
			// Adapter provenance is selected by the authenticated import route,
			// never by the submitted draft. Besides being audit evidence, the
			// profile controls source-specific gates and reconciliation rules.
			// Stamp it even when a client supplied a different non-empty value.
			if strings.TrimSpace(sourceProfile) == "" && prior != nil &&
				prior.Metadata != nil && prior.Metadata.Import != nil {
				sourceProfile = prior.Metadata.Import.Source
			}
			if preserved.Metadata == nil {
				preserved.Metadata = &dsl.ScriptMetadata{}
			}
			if preserved.Metadata.Import == nil {
				preserved.Metadata.Import = &dsl.ImportMetadata{}
			}
			preserved.Metadata.Import.Source = strings.TrimSpace(sourceProfile)
		}
		if prior != nil && prior.Metadata != nil && prior.Metadata.Import != nil {
			if !trustedImport {
				// Source profile is durable server-owned provenance. An ordinary
				// editor may omit it but cannot relabel an imported script to a
				// different adapter profile (which can also change
				// source-specific replay-gate interpretation).
				sourceProfile = prior.Metadata.Import.Source
				if preserved.Metadata == nil {
					preserved.Metadata = &dsl.ScriptMetadata{}
				}
				if preserved.Metadata.Import == nil {
					preserved.Metadata.Import = &dsl.ImportMetadata{}
				}
				preserved.Metadata.Import.Source = sourceProfile
			}
		}
		// Import provenance is server-owned durable state. An ordinary editor may
		// omit or mask it, but cannot erase the attestation while executable
		// imported values are being reconciled from the durable prior.
		EnsureCredentialPreservingImportMetadata(preserved, sourceProfile)
		if err := ReconcileMaskedScriptSecretsWithTrust(
			preserved,
			prior,
			trustedImport,
		); err != nil {
			return nil, err
		}
		if !trustedImport && prior != nil {
			if err := RestoreServerOwnedImportSecurityMetadata(
				preserved,
				prior,
			); err != nil {
				return nil, err
			}
		} else if err := renameDuplicateImportActionUIDs(preserved, prior); err != nil {
			// RestoreServerOwnedImportSecurityMetadata renames repeated UIDs on
			// an ordinary edit; an import commit must store unique UIDs too, or
			// every later masked edit falls back to legacy step matching, which
			// cannot match any UID-stamped credential step.
			return nil, err
		}
		if err := SanitizePersistentImportReviewSurfaces(preserved); err != nil {
			return nil, err
		}
		if !trustedImport && prior != nil {
			if err := ValidatePreservedImportCredentialEditsWithOptions(preserved, prior, options); err != nil {
				return nil, err
			}
			// A recording saved before conversions stamped step identities gets
			// them on its first checked save, so a later edit that deletes or
			// moves steps can match each kept step to its stored self. The
			// editor takes the saved copy back, identities included.
			if preserved.Metadata != nil && preserved.Metadata.Import != nil &&
				strings.EqualFold(strings.TrimSpace(preserved.Metadata.Import.Source), "browser_recorder") {
				EnsureScriptImportActionUIDs(preserved)
			}
			if maybeGraduateCredentialPreservingImport(preserved, prior) {
				// The review-surface sanitizer above returned early because the
				// script was still credential-preserving. Graduation stores the
				// row under the ordinary secret_reference policy, so its
				// non-opaque preserved review evidence must meet the same
				// at-rest sanitization as any other non-preserving save.
				if err := SanitizePersistentImportReviewSurfaces(preserved); err != nil {
					return nil, err
				}
			}
		}
		return preserved, nil
	}
	// The disclosure projection is a read/response and export concern. It is
	// computed here only to run the logical-secret binding gate below; it is
	// never what gets stored. Persisting it silently replaced operator-supplied
	// values with [REDACTED]/{{secret.*}} stubs, so the durable row could be a
	// script that can never run. Durable storage always keeps the executable
	// script.
	projected := MaskScriptSecrets(script)
	if projected == nil {
		return nil, fmt.Errorf("failed to project script for durable secret binding")
	}
	if unresolved := DiscoverSecretBindingRequirements(projected, 0); len(unresolved) > 0 {
		return nil, ErrScriptCredentialBindingsRequired
	}
	// A source commit fence is server-owned protected-import retry state that an
	// ordinary write can neither establish nor carry forward. maskScriptSecrets
	// dropped it on the way to storage; keep that boundary now that the
	// executable script is what gets persisted.
	StripProtectedImportSourceCommitFence(script)
	if err := ReconcileMaskedScriptSecrets(script, prior); err != nil {
		return nil, err
	}
	if err := renameDuplicateImportActionUIDs(script, prior); err != nil {
		return nil, err
	}
	if err := SanitizePersistentImportReviewSurfaces(script); err != nil {
		return nil, err
	}
	if !trustedImport && prior != nil {
		if script.Metadata != nil && script.Metadata.Import != nil && script.Metadata.Import.Preserved != nil {
			delete(script.Metadata.Import.Preserved, "effective_settings")
		}
		if prior.Metadata != nil &&
			prior.Metadata.Import != nil && prior.Metadata.Import.Preserved != nil {
			if priorSettings, ok := prior.Metadata.Import.Preserved["effective_settings"]; ok {
				cloned, err := CloneImportSecurityMetadata(priorSettings)
				if err == nil {
					EnsureScriptImportPreserved(script, prior.Metadata.Import.Source)["effective_settings"] = cloned
				}
			}
		}
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
