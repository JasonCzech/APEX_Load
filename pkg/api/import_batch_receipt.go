package api

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

// importBatchWorkflow names one create-only batch save. Every batch importer
// reserves a durable receipt, derives deterministic asset IDs from it, and
// fences prior assets before adopting them; only the profile the receipt is
// recorded under and the noun in the operator-facing message differ.
//
// The trust decisions each importer makes about draft attestation and credential
// state are deliberately NOT shared. Those rules genuinely differ per workflow —
// the generic batch claims an attestation only when the draft carries a token
// and refuses raw credentials without one, while the archive save claims
// whenever either is present — and hiding that behind a flag would make each
// workflow's posture harder to review, not easier.
type importBatchWorkflow struct {
	// Profile is the source profile the durable receipt is recorded under.
	Profile string
	// Noun names the batch in operator-facing messages, e.g. "import batch".
	Noun string
	// SkipBrowserClaimCompensation runs claim recovery without the browser
	// compensation pass. Browser-recording saves set it because the
	// format-specific reconciler is what calls this, and letting generic
	// recovery compensate browser rows here would recurse back into it. Every
	// other workflow wants the full pass, which gives that reconciler first
	// refusal before ordinary compensation touches anything.
	SkipBrowserClaimCompensation bool
}

// claimImportBatchReceipt reserves the durable receipt for one batch save.
//
// It returns ok=false after writing the response when the reservation fails.
// replay reports that a completed receipt already covers this exact request, in
// which case the caller answers from the stored assets rather than saving again.
func (s *Server) claimImportBatchReceipt(
	w http.ResponseWriter,
	workflow importBatchWorkflow,
	projectID, operationID, requestDigest string,
) (attempt *directImportAttempt, replay bool, ok bool) {
	attempt, replay, err := s.claimDirectImportAttempt(
		projectID, workflow.Profile, operationID, requestDigest,
	)
	if err == nil {
		return attempt, replay, true
	}
	switch {
	case errors.Is(err, storage.ErrDirectImportReceiptBusy):
		w.Header().Set("Retry-After", "2")
		writeError(w, http.StatusConflict, "this "+workflow.Noun+" save is already in progress")
	case errors.Is(err, storage.ErrDirectImportReceiptConflict):
		writeError(w, http.StatusConflict, "operation_id was already used for a different "+workflow.Noun)
	default:
		writeError(w, http.StatusInternalServerError, "failed to reserve "+workflow.Noun+": "+err.Error())
	}
	return nil, false, false
}

// importBatchPlannedAssetIDs derives the script IDs this attempt will write.
//
// They are deterministic in the receipt so a retry after a lost response lands
// on the same rows rather than creating a duplicate batch. Only a save with no
// receipt at all falls back to fresh identifiers.
func importBatchPlannedAssetIDs(
	attempt *directImportAttempt,
	workflow importBatchWorkflow,
	projectID, operationID string,
	count int,
) []string {
	seed := ""
	switch {
	case attempt != nil && attempt.receipt != nil:
		seed = attempt.receipt.ID
	case operationID != "":
		seed = directImportOperationHash(projectID, workflow.Profile, operationID)
	}
	ids := make([]string, count)
	for index := range ids {
		if seed == "" {
			ids[index] = uuid.NewString()
			continue
		}
		ids[index] = deterministicDirectImportAssetID(seed, "script", index)
	}
	return ids
}

// fenceImportBatchAdoption runs the preamble every adoption pass shares: recover
// expired claims from a process that died mid-save, renew this attempt's
// ownership, and confirm no uncommitted transaction still fences the assets.
//
// It returns false after writing the response when adoption must not proceed. A
// still-fenced batch is a 409 the caller can retry; anything else is a storage
// failure and answers 500 — the generic batch commit used to report both as
// "still finalizing", so an unreadable store looked like a transient conflict.
func (s *Server) fenceImportBatchAdoption(
	w http.ResponseWriter,
	workflow importBatchWorkflow,
	attempt *directImportAttempt,
	projectID string,
	plannedIDs []string,
	scenarioID string,
) bool {
	recoverClaims := s.recoverExpiredLibraryImportClaims
	if workflow.SkipBrowserClaimCompensation {
		recoverClaims = func() error { return s.recoverExpiredLibraryImportClaimsWithBrowserCompensation(false) }
	}
	if err := recoverClaims(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to recover prior "+workflow.Noun+": "+err.Error())
		return false
	}
	// Losing the receipt lease means another process took the attempt over. Three
	// of the five call sites already answered 409 and two answered 500; a lease
	// another writer now holds is a conflict the caller can retry, so they are
	// standardized on 409 here.
	if err := attempt.renew(); err != nil {
		writeError(w, http.StatusConflict, workflow.Noun+" receipt ownership was lost; retry the save")
		return false
	}
	if err := s.fenceDirectImportAssetAdoption(attempt, projectID, plannedIDs, scenarioID); err != nil {
		if errors.Is(err, errDirectImportAssetsClaimed) {
			w.Header().Set("Retry-After", "2")
			writeError(w, http.StatusConflict, "the prior "+workflow.Noun+" is still finalizing; retry the save")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to inspect prior "+workflow.Noun+": "+err.Error())
		}
		return false
	}
	return true
}
