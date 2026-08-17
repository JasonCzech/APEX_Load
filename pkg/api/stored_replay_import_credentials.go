package api

import (
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// resolveStoredReplayImportScript decides which executable script a saved-script
// replay, debugger, or browser-replay start actually runs.
//
// The client never receives the retained credential bytes of a
// credential-preserving import: every read is a disclosure projection whose
// credentials are [REDACTED] sentinels. Replaying that projection verbatim would
// execute the sentinel, so the durable server-owned snapshot has to be
// substituted before executable preparation.
//
// An unchanged echo of the projection resolves to the durable snapshot as-is.
// An edited draft is reconciled against that same durable prior using the
// identical pipeline a durable save runs, so a script the operator is allowed to
// save is also a script they can debug. Without this, editing anything at all
// (even an unrelated action) dropped the whole script onto the untrusted path,
// where every imported credential became a sentinel and the request was rejected
// with secret_bindings_required — while the same script kept running fine in a
// scenario, which executes the durable snapshot server-side.
//
// Reconciliation restores only values the client returned byte-identical to the
// projection; introducing a new credential, moving a retained credential to a
// different request context, or rewriting server-owned import metadata is still
// refused.
func resolveStoredReplayImportScript(candidate, stored *dsl.Script) (*dsl.Script, bool, error) {
	if candidate == nil {
		return stored, isCredentialPreservingImport(stored), nil
	}
	if !isCredentialPreservingImport(stored) {
		return candidate, false, nil
	}
	if matchesStoredScriptDisclosureProjection(candidate, stored) {
		return stored, true, nil
	}
	restored, err := restoreDurableImportCredentialsForExecution(candidate, stored)
	if err != nil {
		return candidate, false, err
	}
	return restored, true, nil
}

// restoreDurableImportCredentialsForExecution mirrors the credential-preserving
// branch of preparePersistentLibraryScriptWithImportTrust for a transient
// execution copy. It is deliberately the same sequence of checks, in the same
// order, as an ordinary (untrusted) masked read-modify-write save: the two must
// accept and reject exactly the same edits, or the debugger and the durable
// library would disagree about what an operator is allowed to run.
func restoreDurableImportCredentialsForExecution(candidate, prior *dsl.Script) (*dsl.Script, error) {
	if candidate == nil || prior == nil {
		return nil, fmt.Errorf("credential-preserving replay requires a durable prior script")
	}
	restored := cloneScriptDefinition(candidate)
	if restored == nil {
		return nil, fmt.Errorf("failed to prepare imported script for replay")
	}
	// A save attestation is a short-lived capability for one durable write. It
	// is never execution provenance and must not reach a runtime session.
	stripImportDraftAttestation(restored)
	if err := validateJavaVMParameterSafety(restored, true); err != nil {
		return nil, err
	}
	// Adapter provenance is durable server-owned state. An ordinary editor may
	// omit it but cannot relabel the script to a different adapter profile,
	// which would also change source-specific replay-gate interpretation.
	sourceProfile := ""
	if prior.Metadata != nil && prior.Metadata.Import != nil {
		sourceProfile = strings.TrimSpace(prior.Metadata.Import.Source)
	}
	ensureCredentialPreservingImportMetadata(restored, sourceProfile)
	if restored.Metadata != nil && restored.Metadata.Import != nil && sourceProfile != "" {
		restored.Metadata.Import.Source = sourceProfile
	}
	if err := reconcileMaskedScriptSecretsWithTrust(restored, prior, false); err != nil {
		return nil, err
	}
	if err := restoreServerOwnedImportSecurityMetadata(restored, prior); err != nil {
		return nil, err
	}
	if err := sanitizePersistentImportReviewSurfaces(restored); err != nil {
		return nil, err
	}
	if err := validatePreservedImportCredentialEdits(restored, prior); err != nil {
		return nil, err
	}
	return restored, nil
}
