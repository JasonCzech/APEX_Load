package recordbridge

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/importreview"
)

const draftResponseFailureKey = "__apexload_import_draft_response_failure"
const draftResponseFailureErrorKey = "__apexload_import_draft_response_error"

type DraftScopeResolver func(*http.Request, *dsl.Script) importreview.DraftScope

// NewMemoryDraftPorts adapts the same process-local capability owner used by a
// controller without protected storage to the standalone recording workflow.
func NewMemoryDraftPorts(owner *importreview.MemoryDraftOwner, scope DraftScopeResolver) *DraftPorts {
	if owner == nil {
		owner = importreview.NewMemoryDraftOwner()
	}
	if scope == nil {
		scope = func(r *http.Request, script *dsl.Script) importreview.DraftScope {
			project := ""
			if script != nil {
				project = strings.TrimSpace(script.ProjectID)
			}
			if project == "" && r != nil {
				project = strings.TrimSpace(r.URL.Query().Get("project_id"))
			}
			if project == "" {
				project = config.DefaultProjectID
			}
			return importreview.DraftScope{ProjectID: project, Actor: "local-recording-bridge"}
		}
	}
	ports := &DraftPorts{}
	ports.Response = func(r *http.Request, opts importreview.ScriptImportResponseOptions) map[string]any {
		if opts.TextMemo == nil {
			opts.TextMemo = &importreview.ImportProjectionTextMemo{}
		}
		review, cleanup := importreview.PrepareResponseCredentials(&opts)
		defer cleanup()
		attached := []*dsl.Script{}
		rollback := func() {
			for _, script := range attached {
				owner.Discard(importreview.ImportDraftAttestationToken(script))
				importreview.StripImportDraftAttestation(script)
			}
		}
		if !opts.SkipDraftAttestation {
			for _, script := range importreview.UniqueImportDraftScripts(importreview.ImportDraftScriptPointers(opts.Script, opts.Scripts)) {
				expiry, projection, err := owner.Attach(script, importreview.DraftAttachOptions{Scope: scope(r, script), DisableExpiration: opts.DisableDraftExpiration, RecordingSessionID: opts.RecordingSessionID, RecordingSnapshotDigest: opts.RecordingSnapshotDigest, RecordingBaseline: opts.RecordingBaseline, TextMemo: opts.TextMemo})
				if err != nil {
					rollback()
					return map[string]any{draftResponseFailureKey: true, draftResponseFailureErrorKey: err}
				}
				if importreview.ImportDraftAttestationToken(script) != "" {
					attached = append(attached, script)
				}
				if !expiry.IsZero() && (opts.DraftAttestationExpiresAt.IsZero() || expiry.Before(opts.DraftAttestationExpiresAt)) {
					opts.DraftAttestationExpiresAt = expiry
				}
				if projection != nil {
					if opts.AttestedProjections == nil {
						opts.AttestedProjections = make(map[*dsl.Script]*dsl.Script)
					}
					opts.AttestedProjections[script] = projection
				}
			}
		}
		response, _ := importreview.NewScriptImportResponseWithProjection(opts)
		importreview.MergeProtectedImportCredentialReview(response, review)
		if !opts.DraftAttestationExpiresAt.IsZero() {
			response["import_draft_expires_at"] = opts.DraftAttestationExpiresAt
		}
		return response
	}
	ports.Claim = func(r *http.Request, script *dsl.Script) (*DraftClaim, error) {
		if !importreview.IsCredentialPreservingImport(script) {
			return nil, nil
		}
		fingerprint, err := importreview.FingerprintImportDraftCredentials(script)
		if err != nil {
			return nil, err
		}
		if !fingerprint.HasRawCredentials && importreview.ImportDraftAttestationToken(script) == "" {
			return nil, nil
		}
		claim, err := owner.Claim(scope(r, script), script, nil)
		if err != nil {
			return nil, err
		}
		return &DraftClaim{Script: claim.Script, Profile: claim.Profile, Release: func() { owner.Release(claim.Token) }, Complete: func(r *http.Request, updated *dsl.Script) (time.Time, error) {
			return owner.Rotate(scope(r, updated), claim, updated)
		}}, nil
	}
	ports.WriteClaimError = func(w http.ResponseWriter, err error) bool {
		if !errors.Is(err, importreview.ErrDraftAttestationBusy) {
			return false
		}
		WriteJSON(w, http.StatusConflict, map[string]any{"error": "Import draft is in use by another request or a save of it is finishing. Wait a moment, check the script library, then try again.", "code": "protected_import_session_conflict", "retryable": true})
		return true
	}
	return ports
}

// WriteMemoryDraftResponse translates private response construction failures
// before any editable draft is published.
func WriteMemoryDraftResponse(w http.ResponseWriter, status int, response map[string]any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	if failed, _ := response[draftResponseFailureKey].(bool); failed {
		w.Header().Set("Retry-After", "5")
		WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "credential-preserving import draft could not be secured; retry the import", "code": "import_draft_attestation_unavailable", "retryable": true})
		return
	}
	WriteJSON(w, status, response)
}
