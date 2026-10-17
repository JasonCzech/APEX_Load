package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/importsession"
	"github.com/jasonczech/apexload/pkg/internal/importreview"
)

const importDraftAttestationMetadataKey = importreview.ImportDraftAttestationMetadataKey
const importDraftAttestationTTL = importreview.ImportDraftAttestationTTL
const importDraftAttestationCapacity = importreview.ImportDraftAttestationCapacity
const importDraftProtectedTokenPrefix = importreview.ImportDraftProtectedTokenPrefix
const importDraftRecordingLineageMetadataKey = importreview.ImportDraftRecordingLineageMetadataKey

var errImportDraftAttestationInvalid = importreview.ErrDraftAttestationInvalid

// errImportDraftAttestationBusy reports a still-valid import draft (protected
// session or in-memory attestation) that another in-flight request currently
// holds. Unlike an invalid draft it is retryable; telling the operator to
// re-run the conversion would only repeat the race.
var errImportDraftAttestationBusy = importreview.ErrDraftAttestationBusy

// writeImportDraftBusy answers a claim that failed only because another
// in-flight request holds the same, still-valid draft, whether it reached the
// handler as errImportDraftAttestationBusy or as a raw protected-session
// ErrSessionClaimed. Callers check it before reporting the draft as invalid or
// expired, which would send the operator to re-run an import that is fine.
func writeImportDraftBusy(w http.ResponseWriter, err error) bool {
	if !errors.Is(err, errImportDraftAttestationBusy) && !errors.Is(err, importsession.ErrSessionClaimed) {
		return false
	}
	// A protected claim reads as held for up to the store's 30 s claim lease
	// after its last renewal, so this also covers a save that just committed
	// and is still releasing the draft. The message must not imply it is free.
	writeProtectedImportSessionConflict(w, "Import draft is in use by another request or a save of it is finishing. Wait a moment, check the script library, then try again.")
	return true
}

func writeProtectedImportSessionConflict(w http.ResponseWriter, message string) {
	writeJSON(w, http.StatusConflict, map[string]any{
		"error":     message,
		"code":      "protected_import_session_conflict",
		"retryable": true,
	})
}

type importDraftAttestation = importreview.DraftAttestation

type importDraftAttestationClaim struct {
	Token          string
	Profile        string
	Context        context.Context
	ProtectedClaim *importsession.Claim
	ProtectedLease *importCandidateClaimLease
	// RawSource is populated only when the attestation reuses a protected
	// source-bearing import candidate. It remains server-side and is cleared on
	// release/completion unless the commit service explicitly takes ownership
	// for durable source retention.
	RawSource []byte
	// Script is the exact candidate reconciled with disclosure-safe edits from
	// the submitted draft. Commit/mutation callers must use it instead of the
	// browser-visible projection.
	Script *dsl.Script
	// JavaClasspathReviewChecked records the authoring-only trusted Java
	// baseline check, including the unchanged-evidence exception when uploads
	// are disabled. Other claims cannot waive final attachment validation.
	JavaClasspathReviewChecked bool
	// ExpirationDisabled follows the original draft through server-owned
	// mutations so applying a repair or binding cannot silently change policy.
	ExpirationDisabled bool
	// RecordingSessionID is populated only when the submitted execution graph
	// still matches a server-attested recording preview (or a server-owned
	// transformation descended from it).
	RecordingSessionID            string
	RecordingSnapshotDigest       string
	AttestedExecutionGraphDigest  string
	SubmittedExecutionGraphDigest string
}

type importDraftCredentialFingerprint = importreview.ImportDraftCredentialFingerprint

// attachImportDraftAttestations gives legacy import screens a server-owned
// proof that the credential literals in their editable draft came from this
// authenticated import response. Only hashes are retained. The opaque token is
// removed before persistence and is never itself treated as import provenance.
func (s *Server) attachImportDraftAttestations(
	r *http.Request,
	opts *scriptImportResponseOptions,
) error {
	if s == nil || r == nil || opts == nil {
		return nil
	}
	scripts := importDraftScriptPointers(opts.Script, opts.Scripts)
	seen := make(map[*dsl.Script]struct{}, len(scripts))
	attached := make([]*dsl.Script, 0, len(scripts))
	for _, script := range scripts {
		if script == nil {
			continue
		}
		if _, duplicate := seen[script]; duplicate {
			continue
		}
		seen[script] = struct{}{}
		expiresAt, projected, err := s.attachImportDraftAttestationProjecting(
			r,
			script,
			opts.TextMemo,
			opts.DisableDraftExpiration,
			opts.RecordingSessionID,
			opts.RecordingSnapshotDigest,
		)
		if err != nil {
			for _, prior := range attached {
				s.discardImportDraftAttestation(r, prior)
			}
			return err
		}
		if !expiresAt.IsZero() &&
			(opts.DraftAttestationExpiresAt.IsZero() ||
				expiresAt.Before(opts.DraftAttestationExpiresAt)) {
			opts.DraftAttestationExpiresAt = expiresAt
		}
		if importDraftAttestationToken(script) != "" {
			attached = append(attached, script)
		}
		if projected != nil {
			if opts.AttestedProjections == nil {
				opts.AttestedProjections = make(map[*dsl.Script]*dsl.Script, len(scripts))
			}
			opts.AttestedProjections[script] = projected
		}
	}
	return nil
}

var importDraftScriptPointers = importreview.ImportDraftScriptPointers

func (s *Server) attachImportDraftAttestation(r *http.Request, script *dsl.Script) error {
	_, err := s.attachImportDraftAttestationWithExpiry(r, script)
	return err
}

func (s *Server) attachImportDraftAttestationWithExpiry(
	r *http.Request,
	script *dsl.Script,
	recordingSessionIDs ...string,
) (time.Time, error) {
	return s.attachImportDraftAttestationWithExpirationPolicy(r, script, false, recordingSessionIDs...)
}

func (s *Server) attachImportDraftAttestationWithExpirationPolicy(
	r *http.Request,
	script *dsl.Script,
	disableExpiration bool,
	recordingSessionIDs ...string,
) (time.Time, error) {
	expiresAt, _, err := s.attachImportDraftAttestationProjecting(r, script, nil, disableExpiration, recordingSessionIDs...)
	return expiresAt, err
}

// attachImportDraftAttestationProjecting also returns the disclosure
// projection fingerprinting computed for script (nil when it computed none),
// scrubbing text through memo. Attestation changes only the draft's capability
// marker, which the projection strips, so the projection still describes
// script on return. Callers may read it, never modify it.
func (s *Server) attachImportDraftAttestationProjecting(
	r *http.Request,
	script *dsl.Script,
	memo *importProjectionTextMemo,
	disableExpiration bool,
	recordingSessionIDs ...string,
) (time.Time, *dsl.Script, error) {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil || strings.TrimSpace(script.Metadata.Import.Source) == "" {
		return time.Time{}, nil, nil
	}
	_, actor, _ := s.resolveAuditActor(r)
	options := importreview.DraftAttachOptions{Scope: importreview.DraftScope{Actor: actor, ProjectID: importDraftProjectIDForRequest(r, script)}, DisableExpiration: disableExpiration, TextMemo: memo}
	if len(recordingSessionIDs) > 0 {
		options.RecordingSessionID = recordingSessionIDs[0]
	}
	if len(recordingSessionIDs) > 1 {
		options.RecordingSnapshotDigest = recordingSessionIDs[1]
	}
	record, projection, err := importreview.PrepareDraftAttestation(script, options)
	if err != nil || record == nil {
		return time.Time{}, projection, err
	}
	profile, projectID := record.Profile, record.ProjectID
	recordingSessionID, recordingSnapshotDigest := record.RecordingSessionID, record.RecordingSnapshotDigest

	protectedStorePresent, err := s.brunoArtifactStorePresent()
	if err != nil {
		return time.Time{}, nil, markProtectedImportStoreUnavailable(err)
	}
	if protectedStorePresent {
		protectedCandidate := cloneScriptDefinition(script)
		if protectedCandidate == nil {
			return time.Time{}, nil, errors.New("failed to clone protected import draft")
		}
		protectedCandidate.ProjectID = projectID
		if err := sanitizePersistentImportReviewSurfaces(protectedCandidate); err != nil {
			return time.Time{}, nil, err
		}
		if protectedCandidate.Metadata != nil &&
			protectedCandidate.Metadata.Import != nil &&
			protectedCandidate.Metadata.Import.Preserved != nil {
			delete(
				protectedCandidate.Metadata.Import.Preserved,
				importDraftRecordingLineageMetadataKey,
			)
		}
		if recordingSessionID != "" {
			preserved := ensureScriptImportPreserved(protectedCandidate, profile)
			preserved[importDraftRecordingLineageMetadataKey] = map[string]any{
				"recording_session_id":      recordingSessionID,
				"recording_snapshot_digest": recordingSnapshotDigest,
				"execution_graph_digest":    record.ExecutionGraphDigest,
			}
		}
		sessionID, expiresAt, err := s.registerImportCandidateSessionWithExpirationPolicy(
			r,
			profile,
			protectedCandidate,
			disableExpiration,
		)
		if err != nil {
			// A configured protected store is an explicit durability boundary.
			// Never silently downgrade its operational or filesystem failure to
			// a process-local capability, even on a single controller.
			return time.Time{}, nil, err
		} else if strings.TrimSpace(sessionID) == "" {
			return time.Time{}, nil, errors.New("protected import draft session was not created")
		} else {
			preserved := ensureScriptImportPreserved(script, profile)
			preserved[importDraftAttestationMetadataKey] =
				importDraftProtectedTokenPrefix + sessionID
			return expiresAt, projection, nil
		}
	}
	if s.haExecution {
		return time.Time{}, nil, errors.New("credential-preserving legacy import saves require shared protected sessions in HA mode")
	}

	expiresAt, err := s.memoryImportDraftOwner().AttachPrepared(script, record)
	return expiresAt, projection, err
}

// claimImportDraftMutation authenticates an exact credential-bearing import
// draft before a server-owned transformation. The caller must either rotate
// the capability with completeImportDraftMutation or release the claim.
func (s *Server) claimImportDraftMutation(
	r *http.Request,
	script *dsl.Script,
) (*importDraftAttestationClaim, error) {
	if !isCredentialPreservingImport(script) {
		return nil, nil
	}
	fingerprint, err := fingerprintImportDraftCredentials(script)
	if err != nil {
		return nil, err
	}
	if !fingerprint.HasRawCredentials && importDraftAttestationToken(script) == "" {
		return nil, nil
	}
	return s.claimImportDraftAttestation(r, script)
}

// completeImportDraftMutation issues a fresh, one-shot capability for the
// transformed exact draft before consuming the old capability. This ordering
// ensures a transient capacity/store error never strands the operator with an
// unsaveable transformed draft.
func (s *Server) completeImportDraftMutation(
	r *http.Request,
	claim *importDraftAttestationClaim,
	script *dsl.Script,
) (time.Time, error) {
	if claim == nil {
		stripImportDraftAttestation(script)
		return time.Time{}, nil
	}
	stripImportDraftAttestation(script)
	expiresAt, err := s.attachImportDraftAttestationWithExpirationPolicy(
		r,
		script,
		claim.ExpirationDisabled,
		claim.RecordingSessionID,
		claim.RecordingSnapshotDigest,
	)
	if err != nil {
		return time.Time{}, err
	}
	if err := s.completeImportDraftAttestation(claim); err != nil {
		// Completion marks a protected claim consumed before its cleanup, and
		// a consumed claim can no longer be released for a retry. Once the old
		// capability is durably retired, keep the secured replacement rather
		// than discard it and leave the operator with no usable draft.
		if markErr := s.markImportDraftAttestationConsumed(claim); markErr != nil {
			s.discardImportDraftAttestation(r, script)
			return time.Time{}, err
		}
		slog.Warn("previous import draft capability retired but not cleaned up", "error", err)
	}
	return expiresAt, nil
}

// markImportDraftAttestationConsumed durably retires a protected claim whose
// consumer is done with it, so stale-claim recovery can never hand the same
// draft out again. In-memory capabilities have no durable claim to mark.
func (s *Server) markImportDraftAttestationConsumed(claim *importDraftAttestationClaim) error {
	if s == nil || claim == nil || claim.ProtectedClaim == nil {
		return errors.New("import draft capability has no protected claim to retire")
	}
	manager, err := s.protectedImportSessionManager()
	if err != nil {
		return err
	}
	return manager.MarkConsumed(*claim.ProtectedClaim)
}

func (s *Server) claimImportDraftAttestation(
	r *http.Request,
	script *dsl.Script,
) (*importDraftAttestationClaim, error) {
	return s.claimImportDraftAttestationWithJavaReview(r, script, false)
}

// Java asset verification and baseline reconciliation are available only to
// authoring-session create. Other callers retain their existing claim contract.
func (s *Server) claimImportDraftAttestationWithJavaReview(
	r *http.Request,
	script *dsl.Script,
	javaReview bool,
) (*importDraftAttestationClaim, error) {
	if s == nil || r == nil || script == nil {
		return nil, errImportDraftAttestationInvalid
	}
	token := importDraftAttestationToken(script)
	if token == "" {
		return nil, errImportDraftAttestationInvalid
	}
	if strings.HasPrefix(token, importDraftProtectedTokenPrefix) {
		return s.claimProtectedImportDraftAttestationWithJavaReview(r, script, token, javaReview)
	}
	_, actor, _ := s.resolveAuditActor(r)
	var requestedAssets []dsl.JavaClasspathAsset
	transform := &importreview.DraftClaimTransform{JavaReview: javaReview,
		Prepare: func(submitted, baseline *dsl.Script) (*dsl.Script, error) {
			var err error
			submitted, requestedAssets, err = s.prepareAuthoringJavaClasspathAttestation(submitted, baseline)
			return submitted, err
		},
		Verify: verifyAuthoringJavaClasspathBaseline,
		Finish: func(restored *dsl.Script) { restored.Config.JavaVuser.ClasspathAssets = requestedAssets },
	}
	claim, err := s.memoryImportDraftOwner().Claim(importreview.DraftScope{Actor: actor, ProjectID: script.ProjectID}, script, transform)
	if err != nil {
		return nil, err
	}
	return &importDraftAttestationClaim{Token: claim.Token, Profile: claim.Profile, Script: claim.Script, ExpirationDisabled: claim.ExpirationDisabled, JavaClasspathReviewChecked: claim.JavaClasspathReviewChecked, RecordingSessionID: claim.RecordingSessionID, RecordingSnapshotDigest: claim.RecordingSnapshotDigest, AttestedExecutionGraphDigest: claim.AttestedExecutionGraphDigest, SubmittedExecutionGraphDigest: claim.SubmittedExecutionGraphDigest}, nil
}

func (s *Server) claimProtectedImportDraftAttestation(
	r *http.Request,
	script *dsl.Script,
	token string,
) (*importDraftAttestationClaim, error) {
	return s.claimProtectedImportDraftAttestationWithJavaReview(r, script, token, false)
}

func (s *Server) claimProtectedImportDraftAttestationWithJavaReview(
	r *http.Request,
	script *dsl.Script,
	token string,
	javaReview bool,
) (*importDraftAttestationClaim, error) {
	sessionID := strings.TrimSpace(strings.TrimPrefix(token, importDraftProtectedTokenPrefix))
	if sessionID == "" || script == nil || script.Metadata == nil ||
		script.Metadata.Import == nil {
		return nil, errImportDraftAttestationInvalid
	}
	profile := strings.ToLower(strings.TrimSpace(script.Metadata.Import.Source))
	if profile == "" {
		return nil, errImportDraftAttestationInvalid
	}
	candidate, protectedClaim, err := s.claimProtectedImportCandidateSession(
		r,
		profile,
		sessionID,
		script.ProjectID,
	)
	if err != nil {
		if errors.Is(err, importsession.ErrSessionClaimed) {
			return nil, errImportDraftAttestationBusy
		}
		return nil, errImportDraftAttestationInvalid
	}
	release := true
	defer func() {
		clearProtectedImportCandidate(candidate)
		if release {
			s.releaseImportCandidateClaim(protectedClaim)
		}
	}()

	if expected, batched := scriptBatch(candidate.Script); batched {
		if allowed, _ := r.Context().Value(jmeterBatchSaveContextKey{}).(bool); !allowed {
			return nil, errImportDraftAttestationInvalid
		}
		supplied, ok := scriptBatch(script)
		if !ok || supplied != expected {
			return nil, errImportDraftAttestationInvalid
		}
	}
	// Select the exception from the trusted source, not a submitted protocol
	// spelling or family that could skip its capability and baseline checks.
	javaReviewDraft := javaReview && candidate.Script != nil && strings.EqualFold(strings.TrimSpace(candidate.Script.Protocol), "java_vuser")
	var requestedAssets []dsl.JavaClasspathAsset
	if javaReviewDraft {
		script, requestedAssets, err = s.prepareAuthoringJavaClasspathAttestation(script, candidate.Script)
		if err != nil {
			return nil, err
		}
	}
	attestedFingerprint, attestedProjection, err := fingerprintImportDraftCredentialsWithProjection(candidate.Script, nil)
	if err != nil {
		return nil, errImportDraftAttestationInvalid
	}
	hasUnavailableCredentials := scriptHasUnavailableImportCredentialValuesFromProjection(candidate.Script, attestedProjection) || requiresExactRepairDraft(candidate.Script)
	if hasUnavailableCredentials && attestedFingerprint.FullDigest == "" {
		attestedFingerprint.FullDigest, err = fullImportDraftExecutionDigest(candidate.Script)
		if err != nil {
			return nil, errImportDraftAttestationInvalid
		}
	}
	restored := cloneScriptDefinition(script)
	if requiresExactRepairDraft(candidate.Script) {
		submittedDigest, digestErr := fullImportDraftExecutionDigest(script)
		projection := repairDisclosure(candidate.Script)
		if _, ok := scriptBatch(candidate.Script); ok {
			projection = batchDisclosure(candidate.Script)
		}
		projectedDigest, projectionErr := fullImportDraftExecutionDigest(projection)
		if digestErr == nil && projectionErr == nil && submittedDigest == projectedDigest {
			restored = cloneScriptDefinition(candidate.Script)
			restored.ID = script.ID
			restored.RefKey = script.RefKey
			restored.Name = script.Name
			restored.FolderID = script.FolderID
		}
	}
	if restored == nil || reconcileMaskedScriptSecrets(restored, candidate.Script) != nil {
		return nil, errImportDraftAttestationInvalid
	}
	if javaReviewDraft {
		if err := verifyAuthoringJavaClasspathBaseline(restored, candidate.Script); err != nil {
			return nil, err
		}
	}
	submittedFingerprint, err := fingerprintImportDraftCredentials(restored)
	if err != nil {
		return nil, errImportDraftAttestationInvalid
	}
	if hasUnavailableCredentials && submittedFingerprint.FullDigest == "" {
		submittedFingerprint.FullDigest, err = fullImportDraftExecutionDigest(restored)
		if err != nil {
			return nil, errImportDraftAttestationInvalid
		}
	}
	recordingSessionID, recordingSnapshotDigest, lineageDigest := protectedImportDraftRecordingLineage(
		candidate.Script,
	)
	record := &importDraftAttestation{
		CredentialDigests:              attestedFingerprint.CredentialDigests,
		StructureDigest:                attestedFingerprint.StructureDigest,
		ExecutionGraphDigest:           attestedFingerprint.ExecutionGraphDigest,
		FullDigest:                     attestedFingerprint.FullDigest,
		HasUnavailableCredentialValues: hasUnavailableCredentials,
	}
	credentialAllowed := (attestedFingerprint.HasRawCredentials || record.HasUnavailableCredentialValues) &&
		(submittedFingerprint.HasRawCredentials || record.HasUnavailableCredentialValues) &&
		importDraftFingerprintAllowed(record, submittedFingerprint)
	if record.HasUnavailableCredentialValues && !attestedFingerprint.HasRawCredentials &&
		!submittedFingerprint.HasRawCredentials {
		credentialAllowed = submittedFingerprint.FullDigest != "" &&
			submittedFingerprint.FullDigest == attestedFingerprint.FullDigest
	}
	lineageAllowed := recordingSessionID != "" &&
		lineageDigest == attestedFingerprint.ExecutionGraphDigest &&
		lineageDigest == submittedFingerprint.ExecutionGraphDigest
	if !credentialAllowed && !lineageAllowed {
		return nil, errImportDraftAttestationInvalid
	}
	if javaReviewDraft {
		restored.Config.JavaVuser.ClasspathAssets = requestedAssets
	}
	lease := s.startImportCandidateClaimLease(protectedClaim)
	if err := lease.assertOwned(); err != nil {
		lease.stop()
		return nil, errImportDraftAttestationInvalid
	}
	// Transfer the bounded payload instead of duplicating up to the full
	// protected-session size in memory. clearProtectedImportCandidate below
	// still clears every other sensitive candidate field; the claim now owns
	// these bytes and clears or transfers them on every terminal path.
	rawSource := candidate.RawSource
	candidate.RawSource = nil
	release = false
	claim := &importDraftAttestationClaim{
		Token:                         token,
		Profile:                       profile,
		ExpirationDisabled:            candidate.ExpirationDisabled,
		Context:                       r.Context(),
		ProtectedClaim:                protectedClaim,
		ProtectedLease:                lease,
		RawSource:                     rawSource,
		Script:                        restored,
		JavaClasspathReviewChecked:    javaReviewDraft,
		AttestedExecutionGraphDigest:  attestedFingerprint.ExecutionGraphDigest,
		SubmittedExecutionGraphDigest: submittedFingerprint.ExecutionGraphDigest,
	}
	if lineageAllowed {
		claim.RecordingSessionID = recordingSessionID
		claim.RecordingSnapshotDigest = recordingSnapshotDigest
	}
	return claim, nil
}

func importDraftFingerprintAllowed(
	record *importDraftAttestation,
	fingerprint importDraftCredentialFingerprint,
) bool {
	return importreview.DraftFingerprintAllowed(record, fingerprint)
}

func (s *Server) releaseImportDraftAttestation(claim *importDraftAttestationClaim) {
	if s == nil || claim == nil {
		return
	}
	clear(claim.RawSource)
	claim.RawSource = nil
	if claim.ProtectedClaim != nil {
		if claim.ProtectedLease != nil {
			claim.ProtectedLease.stop()
		}
		s.releaseImportCandidateClaim(claim.ProtectedClaim)
		return
	}
	s.memoryImportDraftOwner().Release(claim.Token)
}

// retainCommittedImportDraftAttestation drops process-local sensitive bytes and
// stops lease renewal without releasing the one-shot capability. Once imported
// assets are durable, a cleanup failure must leave the capability claimed until
// its normal lifecycle/reconciliation path; releasing it would authorize the same
// credential-bearing draft to create a second batch under a new operation ID.
// A protected claim is also marked consumed, because a claim nobody renews is
// otherwise recovered into a claimable session after its lease.
func (s *Server) retainCommittedImportDraftAttestation(claim *importDraftAttestationClaim) {
	if s == nil || claim == nil {
		return
	}
	clear(claim.RawSource)
	claim.RawSource = nil
	if claim.ProtectedLease != nil {
		claim.ProtectedLease.stop()
	}
	if claim.ProtectedClaim != nil {
		// Without a renewing holder the claim would go stale and be recovered
		// into a claimable draft that a second save could commit again.
		if err := s.markImportDraftAttestationConsumed(claim); err != nil {
			slog.Warn("cannot mark committed import draft consumed", "error", err)
		}
	}
	if claim.ProtectedClaim == nil {
		s.memoryImportDraftOwner().RetainCommitted(claim.Token)
	}
}

func (s *Server) completeImportDraftAttestation(claim *importDraftAttestationClaim) error {
	if s == nil || claim == nil {
		return nil
	}
	clear(claim.RawSource)
	claim.RawSource = nil
	if claim.ProtectedClaim != nil {
		if claim.ProtectedLease != nil {
			if err := claim.ProtectedLease.assertOwned(); err != nil {
				claim.ProtectedLease.stop()
				return err
			}
			claim.ProtectedLease.stop()
		}
		return s.completeImportCandidateClaim(claim.Context, claim.ProtectedClaim)
	}
	s.memoryImportDraftOwner().Complete(claim.Token)
	return nil
}

func (s *Server) assertImportDraftAttestationOwned(claim *importDraftAttestationClaim) error {
	if claim == nil || claim.ProtectedLease == nil {
		return nil
	}
	return claim.ProtectedLease.assertOwned()
}

func (s *Server) discardImportDraftAttestation(r *http.Request, script *dsl.Script) {
	token := importDraftAttestationToken(script)
	stripImportDraftAttestation(script)
	if token == "" || s == nil {
		return
	}
	if strings.HasPrefix(token, importDraftProtectedTokenPrefix) {
		sessionID := strings.TrimSpace(strings.TrimPrefix(token, importDraftProtectedTokenPrefix))
		if sessionID == "" || r == nil || script == nil || script.Metadata == nil ||
			script.Metadata.Import == nil {
			return
		}
		manager, err := s.protectedImportSessionManager()
		if err != nil {
			return
		}
		_, actor, _ := s.resolveAuditActor(r)
		_ = manager.Delete(r.Context(), importsession.Scope{
			ID:        sessionID,
			Profile:   strings.ToLower(strings.TrimSpace(script.Metadata.Import.Source)),
			ProjectID: importDraftProjectIDForRequest(r, script),
			Actor:     actor,
		})
		return
	}
	s.memoryImportDraftOwner().Discard(token)
}

func importDraftProjectIDForRequest(r *http.Request, script *dsl.Script) string {
	projectID := ""
	if script != nil {
		projectID = strings.TrimSpace(script.ProjectID)
	}
	if projectID == "" && r != nil {
		projectID = strings.TrimSpace(r.URL.Query().Get("project_id"))
	}
	if authorizedProjectID, scoped := authorizedProjectFromRequest(r); scoped {
		projectID = authorizedProjectID
	}
	return normalizeProjectID(projectID)
}

func (s *Server) memoryImportDraftOwner() *importreview.MemoryDraftOwner {
	s.importDraftAttestationMu.Lock()
	defer s.importDraftAttestationMu.Unlock()
	if s.importDraftOwner == nil {
		s.importDraftOwner = importreview.NewMemoryDraftOwner()
	}
	return s.importDraftOwner
}

var importDraftAttestationToken = importreview.ImportDraftAttestationToken

var stripImportDraftAttestation = importreview.StripImportDraftAttestation

func protectedImportDraftRecordingLineage(script *dsl.Script) (string, string, string) {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil ||
		script.Metadata.Import.Preserved == nil {
		return "", "", ""
	}
	raw, ok := script.Metadata.Import.Preserved[importDraftRecordingLineageMetadataKey].(map[string]any)
	if !ok {
		return "", "", ""
	}
	sessionID, _ := raw["recording_session_id"].(string)
	snapshotDigest, _ := raw["recording_snapshot_digest"].(string)
	digest, _ := raw["execution_graph_digest"].(string)
	sessionID = strings.TrimSpace(sessionID)
	snapshotDigest = strings.TrimSpace(snapshotDigest)
	digest = strings.TrimSpace(digest)
	if sessionID == "" || snapshotDigest == "" || digest == "" {
		return "", "", ""
	}
	return sessionID, snapshotDigest, digest
}

var fingerprintImportDraftCredentials = importreview.FingerprintImportDraftCredentials

// importDraftProjection is a draft's maskScriptSecrets projection (script, nil
// when projecting failed) with the JSON forms of the draft and of script that
// fingerprinting compared (nil when not built). Projecting and converting a
// large draft cost more than the rest of an import response, so callers that
// need them again reuse these. Nothing may modify them.
type importDraftProjection = importreview.ImportDraftProjection

// fingerprintImportDraftCredentialsWithProjection also returns the projection
// it fingerprinted, which is empty when fingerprinting failed before
// projecting. The projection scrubs text through memo (nil for a memo of its
// own).
var fingerprintImportDraftCredentialsWithProjection = importreview.FingerprintImportDraftCredentialsWithProjection

// opaqueImportSourceProjectionChanged detects exact, inert source payloads
// whose credentials cannot be represented as ordinary guided binding
// requirements. Any such delta is full-digest attested so a legacy save cannot
// silently persist the disclosure projection in place of the operator's exact
// source.
var opaqueImportSourceProjectionChanged = importreview.OpaqueImportSourceProjectionChanged

func canonicalImportDraftStructureDigest(script *dsl.Script) (string, error) {
	projected := maskScriptSecrets(script)
	return canonicalImportDraftStructureDigestFromProjection(projected)
}

var canonicalImportDraftStructureDigestFromProjection = importreview.CanonicalImportDraftStructureDigestFromProjection

var canonicalImportDraftExecutionGraphDigest = importreview.CanonicalImportDraftExecutionGraphDigest

var canonicalizeImportDraftCredentialTokens = importreview.CanonicalizeImportDraftCredentialTokens

var runtimeCredentialSourceProjectionChanged = importreview.RuntimeCredentialSourceProjectionChanged

// Changed wire bytes are guided only when every projected span has a complete
// existing locator. Encoded/nested values without such locators require an
// exact execution digest even when another header/body has guided credentials.
var unlocatedRequestBodyProjectionChanged = importreview.UnlocatedRequestBodyProjectionChanged

var fullImportDraftExecutionDigest = importreview.FullImportDraftExecutionDigest

// completeImportDraftMutations rotates the capabilities of a whole batch of
// credential-preserving drafts, lifting completeImportDraftMutation's two-phase
// ordering from one script to many.
//
// Calling completeImportDraftMutation in a loop looks equivalent but is not: a
// failure on the third script leaves the first two with their old capabilities
// already consumed and their new ones attached only to the server's in-memory
// copies, which the caller then discards along with the error response. Those
// drafts become unsaveable and the operator has to re-import — precisely the
// stranding the single-script ordering exists to prevent.
//
// Phase one issues every new capability and rolls all of them back if any
// fails, which is where the capacity error lives. Phase two consumes the old
// ones, after asserting ownership of every protected lease up front so the
// common failure is caught before anything is consumed. Once phase one has
// completed, replacementReady is true: a later cleanup failure must not cause
// the caller to discard those usable replacements and strand the draft.
//
// claims and scripts are index-parallel; a nil claim means that script needs no
// rotation and reports a zero expiry.
func (s *Server) completeImportDraftMutations(
	r *http.Request,
	claims []*importDraftAttestationClaim,
	scripts []*dsl.Script,
) ([]time.Time, bool, error) {
	expiries := make([]time.Time, len(claims))
	if len(claims) != len(scripts) {
		return nil, false, errors.New("import draft rotation requires index-parallel claims and scripts")
	}

	attached := make([]int, 0, len(claims))
	rollback := func() {
		for _, index := range attached {
			s.discardImportDraftAttestation(r, scripts[index])
		}
	}

	for index, claim := range claims {
		if claim == nil {
			stripImportDraftAttestation(scripts[index])
			continue
		}
		if err := s.assertImportDraftAttestationOwned(claim); err != nil {
			rollback()
			return nil, false, err
		}
		stripImportDraftAttestation(scripts[index])
		expiresAt, err := s.attachImportDraftAttestationWithExpirationPolicy(
			r,
			scripts[index],
			claim.ExpirationDisabled,
			claim.RecordingSessionID,
			claim.RecordingSnapshotDigest,
		)
		if err != nil {
			rollback()
			return nil, false, err
		}
		attached = append(attached, index)
		expiries[index] = expiresAt
	}

	cleanupErr := completeImportDraftAttestationBatch(claims, s.completeImportDraftAttestation)
	return expiries, true, cleanupErr
}

func completeImportDraftAttestationBatch(
	claims []*importDraftAttestationClaim,
	complete func(*importDraftAttestationClaim) error,
) error {
	var cleanupErr error
	for _, claim := range claims {
		if claim == nil {
			continue
		}
		if err := complete(claim); err != nil {
			// Every replacement capability is already durable and attached to the
			// response script. Continue consuming the remaining old claims and let
			// the caller return the replacements with a cleanup warning.
			cleanupErr = errors.Join(cleanupErr, err)
		}
	}
	return cleanupErr
}
