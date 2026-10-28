package api

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/convert/sample"
	"github.com/jasonczech/apexload/pkg/dsl"
)

const (
	genericImportBatchProfile               = "script_import_batch"
	genericImportBatchMaxScripts            = 1000
	genericImportRecordingSessionIDMaxBytes = 256
)

// genericImportBatchWorkflow names this endpoint in the shared receipt helpers.
var genericImportBatchWorkflow = importBatchWorkflow{
	Profile: genericImportBatchProfile,
	Noun:    "import batch",
}

type genericImportBatchCommitRequest struct {
	Scripts                   []dsl.Script `json:"scripts"`
	FolderID                  *string      `json:"folder_id,omitempty"`
	OperationID               string       `json:"operation_id"`
	SourceProfile             string       `json:"source_profile"`
	ImportSessionIDs          []string     `json:"import_session_ids,omitempty"`
	SourceRecordingSessionIDs []string     `json:"source_recording_session_ids,omitempty"`
	// UpdateTargets is positional with Scripts: an entry names the earlier
	// imported script that script updates as a new version (it keeps its
	// folder); null, or no list, creates. See importExistingScripts.
	UpdateTargets []*importUpdateTarget `json:"update_targets,omitempty"`
}

// OpenAPI-Responses: 201
// handleCommitGenericImportBatch is the create-only persistence boundary for
// importers that do not have format-specific replace semantics. It provides a
// durable operation receipt, opaque credential restoration, and one atomic
// script transaction, so retrying after a lost response cannot duplicate a
// RAML/HAR/WSDL/recorder/DevWeb import.
func (s *Server) handleCommitGenericImportBatch(w http.ResponseWriter, r *http.Request) {
	var req genericImportBatchCommitRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if len(req.Scripts) == 0 {
		writeError(w, http.StatusBadRequest, "at least one imported script is required")
		return
	}
	if len(req.Scripts) > genericImportBatchMaxScripts {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("import batch must contain at most %d scripts", genericImportBatchMaxScripts))
		return
	}
	if len(req.SourceRecordingSessionIDs) != 0 && len(req.SourceRecordingSessionIDs) != len(req.Scripts) {
		writeError(w, http.StatusBadRequest, "source_recording_session_ids must be omitted or contain one entry per script")
		return
	}
	if len(req.ImportSessionIDs) != 0 && len(req.ImportSessionIDs) != len(req.Scripts) {
		writeError(w, http.StatusBadRequest, "import_session_ids must be omitted or contain one entry per script")
		return
	}
	importSessionIDs := make([]string, len(req.Scripts))
	for index, raw := range req.ImportSessionIDs {
		sessionID := strings.TrimSpace(raw)
		if len(sessionID) > genericImportRecordingSessionIDMaxBytes {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("import session %d must be at most %d bytes", index+1, genericImportRecordingSessionIDMaxBytes))
			return
		}
		importSessionIDs[index] = sessionID
	}
	var sourceRecordingSessionIDs []string
	if len(req.SourceRecordingSessionIDs) > 0 {
		sourceRecordingSessionIDs = make([]string, len(req.SourceRecordingSessionIDs))
		for index, raw := range req.SourceRecordingSessionIDs {
			sessionID := strings.TrimSpace(raw)
			if len(sessionID) > genericImportRecordingSessionIDMaxBytes {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("source recording session %d must be at most %d bytes", index+1, genericImportRecordingSessionIDMaxBytes))
				return
			}
			sourceRecordingSessionIDs[index] = sessionID
		}
	}
	operationID, err := validateDirectImportOperationID(req.OperationID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if operationID == "" {
		writeError(w, http.StatusBadRequest, "operation_id is required")
		return
	}
	updateTargets, err := normalizeImportUpdateTargets(req.UpdateTargets, len(req.Scripts))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	updatesExisting := hasImportUpdateTargets(updateTargets)
	requestedProfile := normalizeGenericImportSourceProfile(req.SourceProfile)
	if requestedProfile == "" {
		writeError(w, http.StatusBadRequest, "source_profile is not supported by the generic import batch")
		return
	}
	if requestedProfile == "recorder" {
		if len(sourceRecordingSessionIDs) != len(req.Scripts) {
			writeError(w, http.StatusBadRequest, "proxy recorder saves require one source_recording_session_ids entry per script")
			return
		}
		for index, sessionID := range sourceRecordingSessionIDs {
			if sessionID == "" {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("proxy recorder script %d requires a source recording session", index+1))
				return
			}
		}
	} else {
		for _, sessionID := range sourceRecordingSessionIDs {
			if sessionID != "" {
				writeError(w, http.StatusConflict, "source recording sessions are supported only by the proxy recorder save workflow")
				return
			}
		}
	}

	projectID := normalizeProjectID(req.Scripts[0].ProjectID)
	if authorized, scoped := authorizedProjectFromRequest(r); scoped {
		projectID = normalizeProjectID(authorized)
	}
	if !s.requireDomainFeatureForProject(w, projectID, featureIDForImportProfile(requestedProfile)) {
		return
	}
	boundDrafts, ok := s.bindImportDrafts(w, req.Scripts, projectID)
	if !ok {
		return
	}
	requestScripts := make([]*dsl.Script, 0, len(req.Scripts))
	requestedFolderIDs := make([]*string, len(req.Scripts))
	for index := range boundDrafts {
		draft := boundDrafts[index]
		if status, err := s.applyProtectedImportFolderOverride(&draft, req.FolderID); err != nil {
			writeError(w, status, err.Error())
			return
		}
		requestedFolderIDs[index] = requestedImportFolderOverride(draft.FolderID, req.FolderID)
		profile := genericImportDraftProfile(&draft, requestedProfile)
		if !genericImportSourceProfilesCompatible(requestedProfile, profile) {
			writeError(w, http.StatusConflict, fmt.Sprintf("imported script %d does not belong to this save workflow", index+1))
			return
		}
		requestScripts = append(requestScripts, &draft)
	}
	requestDigest, err := s.directImportScriptsRequestDigest(requestScripts, map[string]any{
		"profile":                      requestedProfile,
		"folder":                       req.FolderID,
		"import_session_ids":           importSessionIDs,
		"source_recording_session_ids": sourceRecordingSessionIDs,
		"update_targets":               importUpdateTargetsDigestValue(updateTargets),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to fingerprint import batch")
		return
	}
	attempt, receiptReplay, ok := s.claimImportBatchReceipt(
		w, genericImportBatchWorkflow, projectID, operationID, requestDigest,
	)
	if !ok {
		return
	}
	if receiptReplay {
		stored, _, loadErr := s.loadDirectImportReceiptAssets(attempt)
		if loadErr != nil {
			writeError(w, http.StatusConflict, "completed import batch receipt is inconsistent: "+loadErr.Error())
			return
		}
		writeGenericImportBatchCommitResponse(w, stored)
		return
	}

	batch := newImportBatchCommit(s, attempt, updateTargets)
	defer func() {
		if cleanupErr := batch.close(); cleanupErr != nil {
			slog.Warn("failed to fully compensate generic import batch", "error", cleanupErr)
		}
	}()
	plannedIDs := importBatchPlannedAssetIDs(
		attempt, genericImportBatchWorkflow, projectID, operationID, len(requestScripts),
	)
	for index, target := range updateTargets {
		if target != nil {
			plannedIDs[index] = target.ScriptID
		}
	}

	// A stale receipt may come from another controller process that committed
	// every deterministic asset before losing the receipt-completion response.
	// Recover those receipt-bound IDs before touching ephemeral draft tokens: an
	// in-memory credential attestation intentionally does not survive restart.
	// This branch can only adopt existing assets for the exact durable request
	// digest; it never authorizes a new create with an invalid token.
	// A script to update exists before the save, so its presence proves
	// nothing; recovery compares exact content after preparation instead.
	if attempt != nil && !attempt.created && !anyNonEmpty(importSessionIDs) && !updatesExisting {
		if !s.fenceImportBatchAdoption(w, genericImportBatchWorkflow, attempt, projectID, plannedIDs, "") {
			return
		}
		stored, _, loadErr := batch.loadRecovery(projectID, plannedIDs, "", nil, nil)
		if loadErr == nil {
			if !recoveredImportScriptBatchIdentityMatches(stored, requestScripts, func(script *dsl.Script) bool {
				return genericImportSourceProfilesCompatible(requestedProfile, genericImportDraftProfile(script, ""))
			}) {
				writeError(w, http.StatusConflict, "a script from the prior import batch changed before recovery; use a new operation id")
				return
			}
			// Prefer the already-bound baseline. If the first process committed
			// scripts but not the sidecar, a still-live source can safely heal it
			// using the exact durable script action graph.
			if baselineErr := s.persistOrVerifyGenericImportBatchBaselines(stored, nil, sourceRecordingSessionIDs, true); baselineErr != nil {
				recoveredBaselines := make([]*attestedRecordingBaseline, len(stored))
				sourceUnavailable := false
				for index, sessionID := range sourceRecordingSessionIDs {
					if sessionID == "" {
						continue
					}
					baseline, attestErr := s.attestRecordingBaselineCapture(projectID, stored[index].Script, sessionID)
					if errors.Is(attestErr, errRecordingCaptureSourceNotFound) {
						sourceUnavailable = true
						continue
					}
					if attestErr != nil {
						writeError(w, http.StatusConflict, fmt.Sprintf("script %d recording baseline could not be recovered from the source recording", index+1))
						return
					}
					recoveredBaselines[index] = baseline
				}
				if baselineErr = s.persistOrVerifyGenericImportBatchBaselines(stored, recoveredBaselines, sourceRecordingSessionIDs, true); baselineErr != nil {
					slog.Error("failed to recover generic import recording baseline", "error", baselineErr)
					message := "The scripts are already committed, but their recording evidence is not finalized. Retry this save with the same operation_id."
					if sourceUnavailable {
						message = "The scripts are already committed, but their recording evidence is not finalized and the source recording is unavailable. Restore the source recording, then retry with the same operation_id."
					}
					writeError(w, http.StatusConflict, message)
					return
				}
			}
			if completeErr := batch.complete(stored, nil); completeErr != nil {
				writeError(w, http.StatusInternalServerError, "failed to reconcile import batch receipt")
				return
			}
			writeGenericImportBatchCommitResponse(w, stored)
			return
		}
	}

	claims := make([]*importDraftAttestationClaim, len(requestScripts))
	sourceRetentionPending := false
	defer func() {
		for _, claim := range claims {
			if claim != nil {
				if batch.assetsCommitted && !sourceRetentionPending {
					s.retainCommittedImportDraftAttestation(claim)
				} else {
					s.releaseImportDraftAttestation(claim)
				}
			}
		}
	}()
	prepared := make([]*dsl.Script, 0, len(requestScripts))
	profiles := make([]string, 0, len(requestScripts))
	updates := make(map[string]*StoredScript)
	baselines := make([]*attestedRecordingBaseline, len(requestScripts))
	baselineSourcesUnavailable := make([]bool, len(requestScripts))
	for index, submitted := range requestScripts {
		draft := cloneScriptDefinition(submitted)
		if draft == nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("script %d is invalid", index+1))
			return
		}
		requestedName := draft.Name
		profile := genericImportDraftProfile(draft, requestedProfile)
		if importSessionIDs[index] != "" {
			candidate, protectedClaim, claimErr := s.claimProtectedImportCandidateSession(
				r,
				requestedProfile,
				importSessionIDs[index],
				draft.ProjectID,
			)
			if claimErr != nil || candidate == nil || candidate.Script == nil || protectedClaim == nil {
				clearProtectedImportCandidate(candidate)
				if protectedClaim != nil {
					s.releaseImportCandidateClaim(protectedClaim)
				}
				if writeImportDraftBusy(w, claimErr) {
					return
				}
				writeError(w, http.StatusConflict, fmt.Sprintf("imported script %d protected candidate is invalid, expired, or already claimed; run the conversion again", index+1))
				return
			}
			candidateDigest, candidateErr := scriptCorrelationActionGraphDigest(candidate.Script)
			submittedDigest, submittedErr := scriptCorrelationActionGraphDigest(draft)
			if candidateErr != nil || submittedErr != nil || candidateDigest != submittedDigest {
				clearProtectedImportCandidate(candidate)
				s.releaseImportCandidateClaim(protectedClaim)
				writeError(w, http.StatusConflict, fmt.Sprintf("imported script %d changed executable behavior after protected inspection; run the conversion again", index+1))
				return
			}
			lease := s.startImportCandidateClaimLease(protectedClaim)
			if leaseErr := lease.assertOwned(); leaseErr != nil {
				lease.stop()
				clearProtectedImportCandidate(candidate)
				s.releaseImportCandidateClaim(protectedClaim)
				writeError(w, http.StatusConflict, fmt.Sprintf("imported script %d protected candidate claim was lost", index+1))
				return
			}
			profile = requestedProfile
			claims[index] = &importDraftAttestationClaim{
				Token:                         importDraftProtectedTokenPrefix + importSessionIDs[index],
				Profile:                       profile,
				Context:                       r.Context(),
				ProtectedClaim:                protectedClaim,
				ProtectedLease:                lease,
				RawSource:                     candidate.RawSource,
				Script:                        candidate.Script,
				AttestedExecutionGraphDigest:  candidateDigest,
				SubmittedExecutionGraphDigest: submittedDigest,
			}
			candidate.RawSource = nil
			candidate.Script = nil
			clearProtectedImportCandidate(candidate)
			draft = claims[index].Script
			draft.Name = requestedName
		}
		fingerprint, fingerprintErr := fingerprintImportDraftCredentials(draft)
		if fingerprintErr != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("script %d credential state is invalid", index+1))
			return
		}
		if claims[index] == nil {
			if token := importDraftAttestationToken(draft); token != "" {
				claim, claimErr := s.claimImportDraftAttestation(r, draft)
				if claimErr != nil || claim == nil || claim.Script == nil {
					if claim != nil {
						s.releaseImportDraftAttestation(claim)
					}
					if writeImportDraftBusy(w, claimErr) {
						return
					}
					writeError(w, http.StatusConflict, fmt.Sprintf("imported script %d draft is invalid or expired; run the conversion again", index+1))
					return
				}
				profile = normalizeGenericImportSourceProfile(claim.Profile)
				if !genericImportSourceProfilesCompatible(requestedProfile, profile) {
					s.releaseImportDraftAttestation(claim)
					writeError(w, http.StatusConflict, fmt.Sprintf("imported script %d does not belong to this save workflow", index+1))
					return
				}
				claims[index] = claim
				draft = claim.Script
			}
		} else {
			stripImportDraftAttestation(draft)
		}
		if requestedProfile == "cloudtest" && claims[index] == nil {
			if err := validateUnattestedCloudTestDraft(draft); err != nil {
				writeError(w, http.StatusConflict, fmt.Sprintf("imported script %d: %v", index+1, err))
				return
			}
		}
		if claims[index] == nil && (fingerprint.HasRawCredentials || scriptHasUnavailableImportCredentialValues(draft)) {
			writeError(w, http.StatusConflict, fmt.Sprintf("imported script %d is missing its server capability; run the conversion again", index+1))
			return
		}
		draft.Name = requestedName
		if err := bindDraftScriptProject(draft, projectID); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("script %d: %v", index+1, err))
			return
		}
		if status, err := s.applyProtectedImportFolderOverride(draft, requestedFolderIDs[index]); err != nil {
			writeError(w, status, err.Error())
			return
		}
		draft, err = replaceUnavailableImportCredentialValues(draft)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("script %d: %v", index+1, err))
			return
		}
		persistent, err := preparePersistentImportedLibraryScript(draft, nil, profile)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("script %d: %v", index+1, err))
			return
		}
		if issues := validateStoredExecutionScript(persistent); len(issues) > 0 {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("script %d: %s", index+1, strings.Join(issues, "; ")))
			return
		}
		persistent.ID = plannedIDs[index]
		if !s.reserveImportUpdate(w, projectID, index, persistent, updateTargets[index], updates) {
			return
		}
		persistent.RefKey = ""
		stampImportedContentDigest(persistent)
		if len(sourceRecordingSessionIDs) > 0 && sourceRecordingSessionIDs[index] != "" {
			baseline, baselineErr := s.attestRecordingBaselineCapture(
				projectID,
				persistent,
				sourceRecordingSessionIDs[index],
				claims[index],
			)
			if baselineErr != nil {
				// A stale receipt can represent a script and baseline that were both
				// committed before the original response was lost. Proxy sessions are
				// intentionally ephemeral, so allow recovery to verify that already-
				// bound durable baseline instead of requiring the source session to
				// survive a controller restart. A new attempt remains fail-closed.
				if errors.Is(baselineErr, errRecordingCaptureSourceNotFound) && attempt != nil && !attempt.created {
					baselineSourcesUnavailable[index] = true
					if claims[index] == nil {
						if settings, ok := sample.ReadEffectiveSettings(persistent); !ok || !sample.HasNonDefaultRecordingConversionSettings(settings) {
							if persistent.Protocol != "websocket" && len(persistent.Actions) > 0 {
								if defaultSettings, resolveErr := sample.ResolveConversionPolicy(sample.PolicyResolutionInput{
									EntryPoint: sample.EntryPointRecorderConvert,
									Format:     "har",
								}); resolveErr == nil {
									sample.AttachEffectiveSettings(persistent, defaultSettings)
								}
							} else if persistent.Metadata != nil && persistent.Metadata.Import != nil && persistent.Metadata.Import.Preserved != nil {
								delete(persistent.Metadata.Import.Preserved, sample.PreservedEffectiveSettingsKey)
							}
						}
					}
				} else {
					status := http.StatusConflict
					if errors.Is(baselineErr, errRecordingCaptureSourceNotFound) {
						status = http.StatusNotFound
					}
					writeError(w, status, fmt.Sprintf("script %d recording baseline: %v", index+1, baselineErr))
					return
				}
			}
			baselines[index] = baseline
		}
		prepared = append(prepared, persistent)
		profiles = append(profiles, profile)
	}
	allocateAtomicImportBatchScriptNames(prepared)
	keepUpdatedScriptNames(prepared, updates)

	if attempt != nil && !attempt.created {
		if !s.fenceImportBatchAdoption(w, genericImportBatchWorkflow, attempt, projectID, plannedIDs, "") {
			return
		}
		stored, _, loadErr := batch.loadRecovery(projectID, plannedIDs, "", prepared, updateTargets)
		if loadErr == nil {
			// The deterministic assets already exist, so never abandon their
			// pending receipt on a subsequent verification error. Keeping the
			// receipt recoverable prevents an orphaned batch and a later duplicate.
			if !directImportPreparedAssetsMatch(s, stored, prepared, nil, nil) {
				writeError(w, http.StatusConflict, "a script from the prior import batch changed before recovery; use a new operation id")
				return
			}
			if baselineErr := s.persistOrVerifyGenericImportBatchBaselines(stored, baselines, sourceRecordingSessionIDs, true); baselineErr != nil {
				slog.Error("failed to recover generic import recording baseline", "error", baselineErr)
				message := "The scripts are already committed, but their recording evidence is not finalized. Retry this save with the same operation_id."
				if anyTrue(baselineSourcesUnavailable) {
					message = "The scripts are already committed, but their recording evidence is not finalized and the source recording is unavailable. Restore the source recording, then retry with the same operation_id."
				}
				writeError(w, http.StatusConflict, message)
				return
			}
			if !s.retainGenericImportBatchSources(r, stored, claims, profiles, true) {
				sourceRetentionPending = true
				writeJSON(w, http.StatusServiceUnavailable, map[string]any{
					"error":     "scripts committed but protected import source retention is pending; retry with the same operation_id and import_session_ids",
					"code":      importSourceRetentionPendingCode,
					"committed": true,
					"retryable": true,
					"ref":       errorReferenceFor(w),
				})
				return
			}
			if completeErr := batch.complete(stored, nil); completeErr != nil {
				writeError(w, http.StatusInternalServerError, "failed to reconcile import batch receipt")
				return
			}
			completeGenericImportBatchDraftClaims(s, claims, stored)
			writeGenericImportBatchCommitResponse(w, stored)
			return
		}
	}

	stored, _, commitFailure := batch.commitPrepared(preparedImportBatch{
		Source: genericImportBatchProfile, Scripts: prepared, Updates: updates, Targets: updateTargets,
		Claims: claims, Profiles: profiles,
	})
	if commitFailure != nil {
		writeImportBatchFailure(w, commitFailure, genericImportBatchMessages)
		return
	}
	if err := s.persistOrVerifyGenericImportBatchBaselines(stored, baselines, sourceRecordingSessionIDs, false); err != nil {
		slog.Error("failed to finalize generic import recording baseline", "error", err)
		writeError(w, http.StatusConflict, "The scripts were committed, but their recording evidence could not be finalized. Retry this save with the same operation_id.")
		return
	}
	if !s.retainGenericImportBatchSources(r, stored, claims, profiles, false) {
		sourceRetentionPending = true
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":     "scripts committed but protected import source retention is pending; retry with the same operation_id and import_session_ids",
			"code":      importSourceRetentionPendingCode,
			"committed": true,
			"retryable": true,
			"ref":       errorReferenceFor(w),
		})
		return
	}
	if err := batch.complete(stored, nil); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to finalize import batch receipt")
		return
	}
	completeGenericImportBatchDraftClaims(s, claims, stored)
	writeGenericImportBatchCommitResponse(w, stored)
}

func validateUnattestedCloudTestDraft(script *dsl.Script) error {
	readiness, err := importreview.EvaluateScript(script, importreview.EvaluationInput{})
	if err != nil {
		return fmt.Errorf("CloudTest review evidence is invalid: %w", err)
	}
	if readiness.State != importreview.Blocked {
		return fmt.Errorf("CloudTest fixture-gated drafts must retain their execution blocker; run the inspection again")
	}
	for _, code := range readiness.BlockingCodes {
		if code == "cloudtest_fixture_mapping_unproven" {
			return nil
		}
	}
	return fmt.Errorf("CloudTest fixture-gated drafts must retain the cloudtest_fixture_mapping_unproven blocker; run the inspection again")
}

func genericImportDraftProfile(script *dsl.Script, fallback string) string {
	if script != nil && script.Metadata != nil && script.Metadata.Import != nil {
		if source := strings.TrimSpace(script.Metadata.Import.Source); source != "" {
			return normalizeGenericImportSourceProfile(source)
		}
	}
	return normalizeGenericImportSourceProfile(fallback)
}

func cloneOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

// requestedImportFolderOverride snapshots the already-authorized destination
// before an opaque draft capability restores the server-owned candidate. An
// explicit empty folder means "library root" and must remain distinguishable
// from an omitted override, whose embedded candidate folder is preserved.
func requestedImportFolderOverride(canonicalFolder, explicitFolder *string) *string {
	if explicitFolder != nil {
		value := ""
		if canonicalFolder != nil {
			value = *canonicalFolder
		}
		return &value
	}
	return cloneOptionalString(canonicalFolder)
}

func recoveredImportScriptBatchIdentityMatches(
	stored []*StoredScript,
	requested []*dsl.Script,
	profileAllowed func(*dsl.Script) bool,
) bool {
	if len(stored) != len(requested) {
		return false
	}
	expected := make([]*dsl.Script, len(requested))
	for index, script := range requested {
		expected[index] = cloneScriptDefinition(script)
		if expected[index] == nil {
			return false
		}
	}
	allocateAtomicImportBatchScriptNames(expected)
	for index := range expected {
		actual := stored[index]
		if actual == nil || actual.Script == nil ||
			actual.Name != expected[index].Name ||
			normalizeProjectID(actual.ProjectID) != normalizeProjectID(expected[index].ProjectID) ||
			!optionalStringValuesEqual(actual.FolderID, expected[index].FolderID) ||
			(profileAllowed != nil && !profileAllowed(actual.Script)) ||
			!equivalentCreateOnlyScript(
				recoveredImportDisclosureIdentity(actual.Script),
				recoveredImportDisclosureIdentity(expected[index]),
			) {
			return false
		}
	}
	return true
}

// recoveredImportDisclosureIdentity compares every non-secret durable field,
// including the action graph, without requiring the vanished exact credential
// candidate. Import source aliases are validated separately by profileAllowed;
// normalize that one field so an accepted wire alias cannot defeat recovery.
func recoveredImportDisclosureIdentity(script *dsl.Script) *dsl.Script {
	candidate := cloneScriptDefinition(script)
	if candidate == nil {
		return nil
	}
	projected := maskScriptSecrets(candidate)
	if projected == nil {
		return nil
	}
	stripImportDraftAttestation(projected)
	stripProtectedImportSourceCommitFence(projected)
	if err := sanitizePersistentImportReviewSurfaces(projected); err != nil {
		return nil
	}
	if projected.Metadata != nil && projected.Metadata.Import != nil {
		projected.Metadata.Import.Source = ""
		if projected.Metadata.Import.Preserved != nil {
			delete(projected.Metadata.Import.Preserved, sample.PreservedEffectiveSettingsKey)
			if len(projected.Metadata.Import.Preserved) == 0 {
				projected.Metadata.Import.Preserved = nil
			}
		}
		if projected.Metadata.Import.SourceScriptName == "" &&
			projected.Metadata.Import.Preserved == nil &&
			len(projected.Metadata.Import.Inputs) == 0 {
			projected.Metadata.Import = nil
		}
		if projected.Metadata.Import == nil && projected.Metadata.RuntimePolicy == nil {
			projected.Metadata = nil
		}
	}
	return projected
}

func optionalStringValuesEqual(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return strings.TrimSpace(*left) == strings.TrimSpace(*right)
}

// genericImportSourceProfilesCompatible fences a draft to the converter
// workflow that produced it. The only cross-profile relationships are
// deliberate compound workflows: a browser recording can emit a browser
// script plus an HTTP side-car, a HAR import can promote detected Salesforce
// traffic, and the sample importer labels drafts with its selected wire format.
func genericImportSourceProfilesCompatible(requested, actual string) bool {
	requested = normalizeGenericImportSourceProfile(requested)
	actual = normalizeGenericImportSourceProfile(actual)
	if requested == "" || actual == "" {
		return false
	}
	if requested == actual {
		return true
	}
	switch requested {
	case "browser_recorder":
		return actual == "browser_recording_har"
	case "har":
		return actual == "salesforce"
	case "sample":
		switch actual {
		case "raw_http", "curl", "rest_json", "har":
			return true
		default:
			return false
		}
	default:
		return false
	}
}

// normalizeGenericImportSourceProfile resolves an accepted wire spelling to its
// durable profile, or "" when this endpoint does not accept the profile. The
// allowlist comes from the importer registry, so registering a format is what
// makes it committable here.
func normalizeGenericImportSourceProfile(raw string) string {
	profile := strings.ToLower(strings.TrimSpace(raw))
	return importerProfileNormalization()[profile]
}

func genericImportSourceProfileWireValues() []string {
	return importerProfileWireValues()
}

func (s *Server) persistOrVerifyGenericImportBatchBaselines(
	stored []*StoredScript,
	baselines []*attestedRecordingBaseline,
	sessionIDs []string,
	allowExisting bool,
) error {
	for index, sessionID := range sessionIDs {
		if sessionID == "" {
			continue
		}
		if index >= len(stored) || stored[index] == nil || stored[index].Script == nil {
			return fmt.Errorf("recording baseline %d has no stored script", index+1)
		}
		var baseline *attestedRecordingBaseline
		if index < len(baselines) {
			baseline = baselines[index]
		}
		if baseline != nil {
			if err := s.persistRecordedBaselineCounted(
				stored[index].ID,
				baseline.source,
				stored[index].Script,
				baseline.observations,
				baseline.count(),
			); err != nil {
				return fmt.Errorf("script %d: %w", index+1, err)
			}
		} else if !allowExisting {
			return fmt.Errorf("script %d source recording could not be attested", index+1)
		}
		if s.store == nil {
			return fmt.Errorf("script %d recording baseline storage is unavailable", index+1)
		}
		record, err := s.store.GetScriptRecording(stored[index].ID)
		if err != nil {
			return fmt.Errorf("verify script %d: %w", index+1, err)
		}
		expectedSource := "proxy_recording"
		if baseline != nil {
			expectedSource = baseline.source
		}
		if record == nil || record.Source != expectedSource ||
			validateRecordingActionGraph(record, stored[index].Script) != nil {
			return fmt.Errorf("script %d recording baseline was not durably bound", index+1)
		}
	}
	return nil
}

func anyTrue(values []bool) bool {
	for _, value := range values {
		if value {
			return true
		}
	}
	return false
}

func anyNonEmpty(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

// retainGenericImportBatchSources publishes any raw source carried by a
// protected generic-import candidate after the atomic script batch commits but
// before its receipt and one-shot sessions are completed. Empty-source claims
// retain their existing credential/recording behavior.
func (s *Server) retainGenericImportBatchSources(
	r *http.Request,
	stored []*StoredScript,
	claims []*importDraftAttestationClaim,
	profiles []string,
	skipMatchingActive bool,
) bool {
	_, actor, _ := s.resolveAuditActor(r)
	for index, claim := range claims {
		if claim == nil || len(claim.RawSource) == 0 {
			continue
		}
		if index >= len(stored) || stored[index] == nil || stored[index].Script == nil || index >= len(profiles) {
			return false
		}
		if !s.retainProtectedImportSource(
			r.Context(),
			stored[index].Script,
			profiles[index],
			claim.RawSource,
			actor,
			skipMatchingActive,
		) {
			return false
		}
	}
	return true
}

func completeGenericImportBatchDraftClaims(s *Server, claims []*importDraftAttestationClaim, stored []*StoredScript) {
	for index, claim := range claims {
		if claim == nil {
			continue
		}
		if err := s.completeImportDraftAttestation(claim); err != nil {
			// Assets and their receipt are already durable. Strand this one-shot
			// claim for expiry instead of letting the handler defer reopen it.
			claims[index] = nil
			scriptID := ""
			if index < len(stored) && stored[index] != nil {
				scriptID = stored[index].ID
			}
			slogImportAttestationCompletionFailure("generic import batch", scriptID, err)
			continue
		}
		claims[index] = nil
	}
}

func writeGenericImportBatchCommitResponse(w http.ResponseWriter, stored []*StoredScript) {
	writeJSON(w, http.StatusCreated, map[string]any{
		"scripts": maskStoredScripts(stored),
		"count":   len(stored),
	})
}
