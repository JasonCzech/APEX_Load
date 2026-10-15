package api

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

const enterpriseArchiveSaveProfile = "enterprise_script_archive"
const enterpriseArchiveDraftInvalidCode = "import_draft_attestation_invalid"

// enterpriseArchiveSaveWorkflow names this endpoint in the shared receipt helpers.
var enterpriseArchiveSaveWorkflow = importBatchWorkflow{
	Profile: enterpriseArchiveSaveProfile,
	Noun:    "enterprise archive batch",
}

type saveEnterpriseArchiveImportRequest struct {
	Scripts     []dsl.Script `json:"scripts"`
	FolderID    *string      `json:"folder_id,omitempty"`
	OperationID string       `json:"operation_id"`
	// UpdateTargets is positional with Scripts: an entry names the earlier
	// imported script that script updates as a new version (it keeps its
	// folder); null, or no list, creates.
	UpdateTargets []*importUpdateTarget `json:"update_targets,omitempty"`
}

// OpenAPI-Responses: 201
// handleSaveEnterpriseArchiveImport persists every generated lane through one
// durable import transaction. The previous dashboard loop used independent
// script creates, so a failure on lane N left lanes 1..N-1 in the library.
func (s *Server) handleSaveEnterpriseArchiveImport(w http.ResponseWriter, r *http.Request) {
	var req saveEnterpriseArchiveImportRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if len(req.Scripts) == 0 {
		writeError(w, http.StatusBadRequest, "at least one enterprise archive script is required")
		return
	}
	if len(req.Scripts) > 1000 {
		writeError(w, http.StatusBadRequest, "enterprise archive batch must contain at most 1000 scripts")
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
	requestScripts := make([]*dsl.Script, 0, len(req.Scripts))
	requestedFolderIDs := make([]*string, len(req.Scripts))
	for index := range req.Scripts {
		draft := req.Scripts[index]
		if err := bindDraftScriptProject(&draft, projectID); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("script %d: %v", index+1, err))
			return
		}
		if status, err := s.applyProtectedImportFolderOverride(&draft, req.FolderID); err != nil {
			writeError(w, status, err.Error())
			return
		}
		requestedFolderIDs[index] = requestedImportFolderOverride(draft.FolderID, req.FolderID)
		requestScripts = append(requestScripts, &draft)
	}
	digestFields := map[string]any{
		"profile": enterpriseArchiveSaveProfile,
		"folder":  req.FolderID,
	}
	if updatesExisting {
		digestFields["update_targets"] = importUpdateTargetsDigestValue(updateTargets)
	}
	requestDigest, err := s.directImportScriptsRequestDigest(requestScripts, digestFields)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to fingerprint enterprise archive batch: "+err.Error())
		return
	}
	attempt, receiptReplay, ok := s.claimImportBatchReceipt(
		w, enterpriseArchiveSaveWorkflow, projectID, operationID, requestDigest,
	)
	if !ok {
		return
	}
	finished := false
	assetsCommitted := false
	var transaction *createImportTransaction
	defer func() {
		if !finished && !assetsCommitted {
			if cleanupErr := attempt.rollback(transaction); cleanupErr != nil {
				slog.Warn("failed to fully compensate enterprise archive batch", "error", cleanupErr)
			}
		}
	}()
	if receiptReplay {
		stored, _, loadErr := s.loadDirectImportReceiptAssets(attempt)
		if loadErr != nil {
			writeError(w, http.StatusConflict, "completed enterprise archive receipt is inconsistent: "+loadErr.Error())
			return
		}
		finished = true
		writeEnterpriseArchiveSaveResponse(w, stored)
		return
	}

	plannedIDs := importBatchPlannedAssetIDs(
		attempt, enterpriseArchiveSaveWorkflow, projectID, operationID, len(requestScripts),
	)
	for index, target := range updateTargets {
		if target != nil {
			plannedIDs[index] = target.ScriptID
		}
	}
	// Adopt only receipt-bound deterministic assets before attempting to claim
	// browser draft capabilities. Local attestations do not survive a controller
	// restart, while a committed batch and its request digest do. A script to
	// update exists before the save, so its presence proves nothing; recovery
	// compares exact content after preparation instead.
	if attempt != nil && !attempt.created && !updatesExisting {
		if !s.fenceImportBatchAdoption(w, enterpriseArchiveSaveWorkflow, attempt, projectID, plannedIDs, "") {
			return
		}
		stored, _, loadErr := s.loadDirectImportAssetsByIDs(projectID, plannedIDs, "")
		if loadErr == nil {
			assetsCommitted = true
			if !recoveredImportScriptBatchIdentityMatches(stored, requestScripts, func(script *dsl.Script) bool {
				return script != nil && script.Metadata != nil && script.Metadata.Import != nil &&
					enterpriseArchiveSourceProfile(script.Metadata.Import.Source)
			}) {
				writeError(w, http.StatusConflict, "a script from the prior enterprise archive batch changed before recovery; use a new operation id")
				return
			}
			if completeErr := attempt.complete(stored, nil); completeErr != nil {
				writeError(w, http.StatusInternalServerError, "failed to reconcile enterprise archive batch receipt: "+completeErr.Error())
				return
			}
			finished = true
			writeEnterpriseArchiveSaveResponse(w, stored)
			return
		}
	}
	prepared := make([]*dsl.Script, 0, len(requestScripts))
	profiles := make([]string, 0, len(requestScripts))
	updates := make(map[string]*StoredScript)
	claims := make([]*importDraftAttestationClaim, len(requestScripts))
	defer func() {
		for _, claim := range claims {
			if claim != nil {
				if assetsCommitted {
					s.retainCommittedImportDraftAttestation(claim)
				} else {
					s.releaseImportDraftAttestation(claim)
				}
			}
		}
	}()
	for index, requestScript := range requestScripts {
		draft := *requestScript
		// Name, project, and folder are the only browser-editable identity fields
		// this commit endpoint carries across an opaque draft restoration. The
		// attestation validates the executable structure/credentials; the normal
		// persistent-script validation below validates this requested name.
		requestedName := requestScript.Name

		profile := enterpriseArchiveSaveProfile
		var claim *importDraftAttestationClaim
		fingerprint, err := fingerprintImportDraftCredentials(&draft)
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to validate enterprise archive credential state")
			return
		}
		if fingerprint.HasRawCredentials || importDraftAttestationToken(&draft) != "" {
			claim, err = s.claimImportDraftAttestation(r, &draft)
			if err != nil {
				if claim != nil {
					s.releaseImportDraftAttestation(claim)
				}
				if writeImportDraftBusy(w, err) {
					return
				}
				writeEnterpriseArchiveDraftInvalid(w, "enterprise archive import draft is invalid or expired; run the conversion again")
				return
			}
			profile = strings.TrimSpace(claim.Profile)
			if !enterpriseArchiveSourceProfile(profile) {
				s.releaseImportDraftAttestation(claim)
				writeError(w, http.StatusConflict, "import draft does not belong to an enterprise archive conversion")
				return
			}
			if claim.Script == nil {
				s.releaseImportDraftAttestation(claim)
				writeEnterpriseArchiveDraftInvalid(w, "enterprise archive import draft is invalid or expired; run the conversion again")
				return
			}
			// Record the claim before any subsequent validation so every error path
			// releases its lease. Keeping this positional also makes completion line
			// up with the corresponding stored script in a mixed attested/plain batch.
			claims[index] = claim
			restored, replaceErr := replaceUnavailableImportCredentialValues(claim.Script)
			if replaceErr != nil {
				writeError(w, http.StatusBadRequest, replaceErr.Error())
				return
			}
			draft = *restored
		} else if scriptHasUnavailableImportCredentialValues(&draft) {
			writeEnterpriseArchiveDraftInvalid(w, "enterprise archive import draft is missing its server capability; run the conversion again")
			return
		}
		draft.Name = requestedName
		// The browser-visible draft was already scoped above, but an attested
		// candidate replaces it with the exact server-owned script. Re-apply both
		// authorization and destination after restoration so neither can be lost.
		if err := bindDraftScriptProject(&draft, projectID); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("script %d: %v", index+1, err))
			return
		}
		if status, err := s.applyProtectedImportFolderOverride(&draft, requestedFolderIDs[index]); err != nil {
			writeError(w, status, err.Error())
			return
		}
		persistent, err := preparePersistentImportedLibraryScript(&draft, nil, profile)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("script %d: %v", index+1, err))
			return
		}
		if issues := validateStoredExecutionScript(persistent); len(issues) > 0 {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("script %d: %s", index+1, strings.Join(issues, "; ")))
			return
		}
		if scriptRequiresJavaVuserAuthoringApproval(persistent) {
			writeError(w, http.StatusConflict, "Java Vuser scripts must be saved through authoring-session review and approval")
			return
		}
		persistent.RefKey = ""
		persistent.ID = plannedIDs[index]
		if !s.reserveImportUpdate(w, projectID, index, persistent, updateTargets[index], updates) {
			return
		}
		stampImportedContentDigest(persistent)
		prepared = append(prepared, persistent)
		profiles = append(profiles, profile)
	}
	allocateAtomicImportBatchScriptNames(prepared)
	keepUpdatedScriptNames(prepared, updates)
	if attempt != nil && !attempt.created {
		if !s.fenceImportBatchAdoption(w, enterpriseArchiveSaveWorkflow, attempt, projectID, plannedIDs, "") {
			return
		}
		stored, _, loadErr := s.loadDirectImportAssetsByIDs(projectID, plannedIDs, "")
		if loadErr == nil && importUpdatesPending(stored, prepared, updateTargets) {
			// The earlier attempt did not update a script it was to update, so
			// it did not finish: save normally (its creates, if any, replay).
			loadErr = errImportUpdateTargetStale
		}
		if loadErr == nil {
			assetsCommitted = true
			if !directImportPreparedAssetsMatch(s, stored, prepared, nil, nil) {
				writeError(w, http.StatusConflict, "a script from the prior enterprise archive batch changed before recovery; use a new operation id")
				return
			}
			if completeErr := attempt.complete(stored, nil); completeErr != nil {
				writeError(w, http.StatusInternalServerError, "failed to reconcile enterprise archive batch receipt: "+completeErr.Error())
				return
			}
			completeEnterpriseArchiveDraftClaims(s, claims, stored)
			finished = true
			writeEnterpriseArchiveSaveResponse(w, stored)
			return
		}
	}

	// Recovery above adopted an update an earlier attempt applied; anything
	// else must still be the version the operator reviewed.
	if !requireReviewedImportUpdates(w, updateTargets, updates) {
		return
	}
	transaction, err = newImportTransaction(s, enterpriseArchiveSaveProfile, prepared, updates, "", nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to prepare enterprise archive batch: "+err.Error())
		return
	}
	stored := make([]*StoredScript, 0, len(prepared))
	for index, script := range prepared {
		if err := attempt.renew(); err != nil {
			writeError(w, http.StatusConflict, "enterprise archive batch receipt ownership was lost; retry the save")
			return
		}
		if index < len(claims) && claims[index] != nil {
			if err := s.assertImportDraftAttestationOwned(claims[index]); err != nil {
				writeError(w, http.StatusConflict, "enterprise archive import draft lease was lost; retry the save")
				return
			}
		}
		created, err := transaction.saveImportedScript(script, profiles[index])
		if errors.Is(err, errImportUpdateTargetStale) {
			writeImportUpdateTargetStale(w, index, err)
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to save enterprise archive script %d: %v", index+1, err))
			return
		}
		stored = append(stored, created)
	}
	if err := transaction.commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit enterprise archive batch: "+err.Error())
		return
	}
	assetsCommitted = true
	if attempt != nil {
		if err := attempt.complete(stored, nil); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to finalize enterprise archive batch receipt: "+err.Error())
			return
		}
	}
	completeEnterpriseArchiveDraftClaims(s, claims, stored)
	finished = true
	writeEnterpriseArchiveSaveResponse(w, stored)
}

func writeEnterpriseArchiveDraftInvalid(w http.ResponseWriter, message string) {
	writeJSON(w, http.StatusConflict, map[string]string{
		"error": message,
		"code":  enterpriseArchiveDraftInvalidCode,
	})
}

// allocateAtomicImportBatchScriptNames mirrors the dashboard's friendly
// collision handling at the durable trust boundary. UI conversion happens one
// archive at a time, so independently generated scripts can share a human name.
// Allocation is deterministic by request order, stable across receipt retries,
// and case-folded so platforms with case-insensitive navigation never receive
// an ambiguous batch.
func allocateAtomicImportBatchScriptNames(scripts []*dsl.Script) {
	registry := make(map[string]int, len(scripts))
	for _, script := range scripts {
		if script == nil {
			continue
		}
		base := strings.TrimSpace(script.Name)
		identity := strings.ToLower(base)
		count := registry[identity]
		if count == 0 {
			script.Name = base
			registry[identity] = 1
			continue
		}
		for {
			count++
			candidate := fmt.Sprintf("%s_%d", base, count)
			candidateIdentity := strings.ToLower(strings.TrimSpace(candidate))
			if _, collision := registry[candidateIdentity]; collision {
				continue
			}
			script.Name = candidate
			registry[identity] = count
			registry[candidateIdentity] = 1
			break
		}
	}
}

func writeEnterpriseArchiveSaveResponse(w http.ResponseWriter, stored []*StoredScript) {
	writeJSON(w, http.StatusCreated, map[string]any{
		"scripts": maskStoredScripts(stored),
		"count":   len(stored),
	})
}

func completeEnterpriseArchiveDraftClaims(s *Server, claims []*importDraftAttestationClaim, stored []*StoredScript) {
	for index, claim := range claims {
		if claim == nil {
			continue
		}
		if err := s.completeImportDraftAttestation(claim); err != nil {
			claims[index] = nil
			// The assets and idempotency receipt are already durable. Do not report
			// a false rollback; this is best-effort capability cleanup.
			scriptID := ""
			if index < len(stored) && stored[index] != nil {
				scriptID = stored[index].ID
			}
			slogImportAttestationCompletionFailure("enterprise archive", scriptID, err)
			continue
		}
		claims[index] = nil
	}
}

func enterpriseArchiveSourceProfile(profile string) bool {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case "enterprise_script_archive", "enterprise_archive", "loadrunner", "devweb":
		return true
	default:
		return false
	}
}

func slogImportAttestationCompletionFailure(kind, scriptID string, err error) {
	if err == nil {
		return
	}
	slog.Warn("failed to finalize credential-preserving import draft session", "import_kind", kind, "script_id", scriptID, "error", err)
}
