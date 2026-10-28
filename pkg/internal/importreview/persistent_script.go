package importreview

import (
	"errors"
	"fmt"
	"github.com/jasonczech/apexload/pkg/dsl"
	"strings"
)

var (
	ErrScriptConcurrentChange           = errors.New("script changed concurrently; reload and retry")
	ErrScriptCredentialBindingsRequired = errors.New("script contains executable credentials that require logical secret bindings")
)

func PreparePersistentLibraryScript(script, prior *dsl.Script) (*dsl.Script, error) {
	return PreparePersistentLibraryScriptWithImportTrust(script, prior, false, "")
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
		}
		if err := SanitizePersistentImportReviewSurfaces(preserved); err != nil {
			return nil, err
		}
		if !trustedImport && prior != nil {
			if err := ValidatePreservedImportCredentialEdits(preserved, prior); err != nil {
				return nil, err
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
