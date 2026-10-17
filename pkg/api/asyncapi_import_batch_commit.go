package api

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	asyncAPIBatchCommitProfile    = "asyncapi"
	asyncAPIBatchCommitMaxScripts = 1000
)

type asyncAPIBatchCommitRequest struct {
	Scripts     []dsl.Script `json:"scripts"`
	FolderID    *string      `json:"folder_id,omitempty"`
	OperationID string       `json:"operation_id"`
	// UpdateTargets is positional with Scripts: an entry names the earlier
	// imported script that script updates as a new version (it keeps its
	// folder); null, or no list, creates.
	UpdateTargets []*importUpdateTarget `json:"update_targets,omitempty"`
}

// OpenAPI-Responses: 201
// handleCommitAsyncAPIImportBatch persists every generated AsyncAPI lane as a
// single durable create transaction. Receipt ownership is established from the
// browser-visible request before one-shot draft capabilities are claimed, so a
// lost successful response can replay without needing an already-consumed
// capability.
func (s *Server) handleCommitAsyncAPIImportBatch(w http.ResponseWriter, r *http.Request) {
	var req asyncAPIBatchCommitRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if len(req.Scripts) == 0 {
		writeError(w, http.StatusBadRequest, "at least one AsyncAPI script is required")
		return
	}
	if len(req.Scripts) > asyncAPIBatchCommitMaxScripts {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("AsyncAPI batch must contain at most %d scripts", asyncAPIBatchCommitMaxScripts))
		return
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

	projectID := normalizeProjectID(req.Scripts[0].ProjectID)
	if authorized, scoped := authorizedProjectFromRequest(r); scoped {
		projectID = normalizeProjectID(authorized)
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
		requestScripts = append(requestScripts, &draft)
	}
	digestFields := map[string]any{
		"profile": asyncAPIBatchCommitProfile,
		"folder":  req.FolderID,
	}
	if updatesExisting {
		digestFields["update_targets"] = importUpdateTargetsDigestValue(updateTargets)
	}
	requestDigest, err := s.directImportScriptsRequestDigest(requestScripts, digestFields)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to fingerprint AsyncAPI batch")
		return
	}
	attempt, receiptReplay, err := s.claimDirectImportAttempt(
		projectID,
		asyncAPIBatchCommitProfile,
		operationID,
		requestDigest,
	)
	if err != nil {
		writeAsyncAPIBatchReceiptError(w, err)
		return
	}
	if receiptReplay {
		// A completed receipt is immutable. A storage inconsistency while loading
		// its response must not roll the receipt back or make an already-committed
		// batch eligible for a second create attempt.
		stored, _, loadErr := s.loadDirectImportReceiptAssets(attempt)
		if loadErr != nil {
			writeError(w, http.StatusConflict, "completed AsyncAPI batch receipt is inconsistent: "+loadErr.Error())
			return
		}
		writeAsyncAPIBatchCommitResponse(w, stored)
		return
	}
	batch := newImportBatchCommit(s, attempt, updateTargets)
	defer func() {
		if cleanupErr := batch.close(); cleanupErr != nil {
			slog.Warn("failed to fully compensate AsyncAPI batch", "error", cleanupErr)
		}
	}()
	assetIdentitySeed := ""
	if attempt != nil && attempt.receipt != nil {
		assetIdentitySeed = attempt.receipt.ID
	} else if operationID != "" {
		assetIdentitySeed = directImportOperationHash(projectID, asyncAPIBatchCommitProfile, operationID)
	}
	plannedIDs := make([]string, len(requestScripts))
	for index := range plannedIDs {
		if assetIdentitySeed == "" {
			plannedIDs[index] = uuid.NewString()
		} else {
			plannedIDs[index] = deterministicDirectImportAssetID(assetIdentitySeed, "script", index)
		}
	}
	for index, target := range updateTargets {
		if target != nil {
			plannedIDs[index] = target.ScriptID
		}
	}
	// Recover deterministic assets before claiming ephemeral draft capabilities.
	// A controller restart loses local attestations, but the durable receipt
	// digest and server-derived IDs still authorize replay of an already-
	// committed batch; they never authorize a new create. A script to update
	// exists before the save, so its presence proves nothing; recovery
	// compares exact content after preparation instead.
	if attempt != nil && !attempt.created && !updatesExisting {
		if recoverErr := s.recoverExpiredLibraryImportClaims(); recoverErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to recover prior AsyncAPI batch")
			return
		}
		if renewErr := attempt.renew(); renewErr != nil {
			writeError(w, http.StatusConflict, "AsyncAPI batch receipt ownership was lost; retry the save")
			return
		}
		if fenceErr := s.fenceDirectImportAssetAdoption(attempt, projectID, plannedIDs, ""); fenceErr != nil {
			w.Header().Set("Retry-After", "2")
			writeError(w, http.StatusConflict, "the prior AsyncAPI batch is still finalizing; retry the save")
			return
		}
		stored, _, loadErr := batch.loadRecovery(projectID, plannedIDs, "", nil, nil)
		if loadErr == nil {
			if !recoveredImportScriptBatchIdentityMatches(stored, requestScripts, func(script *dsl.Script) bool {
				return script != nil && script.Metadata != nil && script.Metadata.Import != nil &&
					strings.EqualFold(strings.TrimSpace(script.Metadata.Import.Source), asyncAPIBatchCommitProfile)
			}) {
				writeError(w, http.StatusConflict, "a script from the prior AsyncAPI batch changed before recovery; use a new operation id")
				return
			}
			if completeErr := batch.complete(stored, nil); completeErr != nil {
				writeError(w, http.StatusInternalServerError, "failed to reconcile AsyncAPI batch receipt")
				return
			}
			writeAsyncAPIBatchCommitResponse(w, stored)
			return
		}
	}

	claims := make([]*importDraftAttestationClaim, len(requestScripts))
	defer func() {
		for _, claim := range claims {
			if claim != nil {
				if batch.assetsCommitted {
					s.retainCommittedImportDraftAttestation(claim)
				} else {
					s.releaseImportDraftAttestation(claim)
				}
			}
		}
	}()
	prepared := make([]*dsl.Script, 0, len(requestScripts))
	updates := make(map[string]*StoredScript)
	for index, submitted := range requestScripts {
		draft := cloneScriptDefinition(submitted)
		if draft == nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("script %d is invalid", index+1))
			return
		}
		requestedName := draft.Name
		fingerprint, fingerprintErr := fingerprintImportDraftCredentials(draft)
		if fingerprintErr != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("script %d credential state is invalid", index+1))
			return
		}
		if token := importDraftAttestationToken(draft); token != "" {
			claim, claimErr := s.claimImportDraftAttestation(r, draft)
			if claimErr != nil || claim == nil || claim.Script == nil ||
				!strings.EqualFold(strings.TrimSpace(claim.Profile), asyncAPIBatchCommitProfile) {
				if claim != nil {
					s.releaseImportDraftAttestation(claim)
				}
				if writeImportDraftBusy(w, claimErr) {
					return
				}
				writeError(w, http.StatusConflict, fmt.Sprintf("AsyncAPI script %d draft is invalid or expired; run the conversion again", index+1))
				return
			}
			claims[index] = claim
			draft = claim.Script
		} else if fingerprint.HasRawCredentials || scriptHasUnavailableImportCredentialValues(draft) {
			writeError(w, http.StatusConflict, fmt.Sprintf("AsyncAPI script %d is missing its server capability; run the conversion again", index+1))
			return
		}
		// Opaque restoration replaces the disclosure-safe browser draft with the
		// exact server candidate. Name is the one intentionally editable identity
		// field in this batch contract, so carry the requested name back across the
		// restore and let normal persistent-script validation enforce it.
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
		persistent, err := preparePersistentImportedLibraryScript(draft, nil, asyncAPIBatchCommitProfile)
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
		prepared = append(prepared, persistent)
	}
	allocateAtomicImportBatchScriptNames(prepared)
	keepUpdatedScriptNames(prepared, updates)

	if attempt != nil && !attempt.created {
		if recoverErr := s.recoverExpiredLibraryImportClaims(); recoverErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to recover prior AsyncAPI batch")
			return
		}
		if renewErr := attempt.renew(); renewErr != nil {
			writeError(w, http.StatusConflict, "AsyncAPI batch receipt ownership was lost; retry the save")
			return
		}
		if fenceErr := s.fenceDirectImportAssetAdoption(attempt, projectID, plannedIDs, ""); fenceErr != nil {
			w.Header().Set("Retry-After", "2")
			writeError(w, http.StatusConflict, "the prior AsyncAPI batch is still finalizing; retry the save")
			return
		}
		stored, _, loadErr := batch.loadRecovery(projectID, plannedIDs, "", prepared, updateTargets)
		if loadErr == nil {
			if !directImportPreparedAssetsMatch(s, stored, prepared, nil, nil) {
				writeError(w, http.StatusConflict, "a script from the prior AsyncAPI batch changed before recovery; use a new operation id")
				return
			}
			if completeErr := batch.complete(stored, nil); completeErr != nil {
				writeError(w, http.StatusInternalServerError, "failed to reconcile AsyncAPI batch receipt")
				return
			}
			completeAsyncAPIBatchDraftClaims(s, claims, stored)
			writeAsyncAPIBatchCommitResponse(w, stored)
			return
		}
	}

	stored, _, commitFailure := batch.commitPrepared(preparedImportBatch{
		Source: asyncAPIBatchCommitProfile, Scripts: prepared, Updates: updates, Targets: updateTargets,
		Claims: claims, DefaultProfile: asyncAPIBatchCommitProfile,
	})
	if commitFailure != nil {
		writeImportBatchFailure(w, commitFailure, asyncAPIImportBatchMessages)
		return
	}
	if err := batch.complete(stored, nil); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to finalize AsyncAPI batch receipt")
		return
	}
	completeAsyncAPIBatchDraftClaims(s, claims, stored)
	writeAsyncAPIBatchCommitResponse(w, stored)
}

func validateDirectImportOperationID(raw string) (string, error) {
	operationID := strings.TrimSpace(raw)
	if len(operationID) > 512 {
		return "", errors.New("operation_id must be at most 512 bytes")
	}
	for _, current := range []byte(operationID) {
		if current < 0x21 || current > 0x7e {
			return "", errors.New("operation_id must contain visible ASCII characters only")
		}
	}
	return operationID, nil
}

func writeAsyncAPIBatchReceiptError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, storage.ErrDirectImportReceiptBusy):
		w.Header().Set("Retry-After", "2")
		writeError(w, http.StatusConflict, "this AsyncAPI batch save is already in progress")
	case errors.Is(err, storage.ErrDirectImportReceiptConflict):
		writeError(w, http.StatusConflict, "operation_id was already used for a different AsyncAPI batch")
	default:
		writeError(w, http.StatusInternalServerError, "failed to reserve AsyncAPI batch")
	}
}

func completeAsyncAPIBatchDraftClaims(s *Server, claims []*importDraftAttestationClaim, stored []*StoredScript) {
	for index, claim := range claims {
		if claim == nil {
			continue
		}
		if err := s.completeImportDraftAttestation(claim); err != nil {
			claims[index] = nil
			scriptID := ""
			if index < len(stored) && stored[index] != nil {
				scriptID = stored[index].ID
			}
			slog.Warn("failed to finalize AsyncAPI import draft capability", "script_id", scriptID, "error", err)
			continue
		}
		claims[index] = nil
	}
}

func writeAsyncAPIBatchCommitResponse(w http.ResponseWriter, stored []*StoredScript) {
	writeJSON(w, http.StatusCreated, map[string]any{
		"scripts": maskStoredScripts(stored),
		"count":   len(stored),
	})
}
