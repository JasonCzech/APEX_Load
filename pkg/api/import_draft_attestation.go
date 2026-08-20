package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/importsession"
)

const (
	importDraftAttestationMetadataKey      = "server_import_attestation_v1"
	importDraftRecordingLineageMetadataKey = "server_recording_lineage_v1"
	importDraftProtectedTokenPrefix        = "protected:"
	importDraftAttestationTTL              = 30 * time.Minute
	importDraftAttestationCapacity         = 128
)

var errImportDraftAttestationInvalid = errors.New("import draft attestation is invalid or expired")

type importDraftAttestation struct {
	Token                string
	Profile              string
	ProjectID            string
	Actor                string
	Protocol             string
	CredentialDigests    map[string]int
	StructureDigest      string
	ExecutionGraphDigest string
	FullDigest           string
	// HasUnavailableCredentialValues records an exact source placeholder or
	// unsafe encoding. It grants no literal-preservation authority; it only lets
	// commit replace that unchanged slot with a governed reference.
	HasUnavailableCredentialValues bool
	// Script is the exact short-lived server-owned candidate. Responses carry
	// only its disclosure projection plus Token.
	Script                  *dsl.Script
	RecordingSessionID      string
	RecordingSnapshotDigest string
	ExpiresAt               time.Time
	Claimed                 bool
}

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
	// RecordingSessionID is populated only when the submitted execution graph
	// still matches a server-attested recording preview (or a server-owned
	// transformation descended from it).
	RecordingSessionID            string
	RecordingSnapshotDigest       string
	AttestedExecutionGraphDigest  string
	SubmittedExecutionGraphDigest string
}

type importDraftCredentialFingerprint struct {
	CredentialDigests    map[string]int
	StructureDigest      string
	ExecutionGraphDigest string
	FullDigest           string
	HasRawCredentials    bool
}

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
		expiresAt, err := s.attachImportDraftAttestationWithExpiry(
			r,
			script,
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
			(opts.draftAttestationExpiresAt.IsZero() ||
				expiresAt.Before(opts.draftAttestationExpiresAt)) {
			opts.draftAttestationExpiresAt = expiresAt
		}
		if importDraftAttestationToken(script) != "" {
			attached = append(attached, script)
		}
	}
	return nil
}

func importDraftScriptPointers(values ...any) []*dsl.Script {
	result := make([]*dsl.Script, 0)
	for _, value := range values {
		switch typed := value.(type) {
		case *dsl.Script:
			result = append(result, typed)
		case []*dsl.Script:
			result = append(result, typed...)
		case []dsl.Script:
			for index := range typed {
				result = append(result, &typed[index])
			}
		case []any:
			result = append(result, importDraftScriptPointers(typed...)...)
		}
	}
	return result
}

func (s *Server) attachImportDraftAttestation(r *http.Request, script *dsl.Script) error {
	_, err := s.attachImportDraftAttestationWithExpiry(r, script)
	return err
}

func (s *Server) attachImportDraftAttestationWithExpiry(
	r *http.Request,
	script *dsl.Script,
	recordingSessionIDs ...string,
) (time.Time, error) {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil {
		return time.Time{}, nil
	}
	profile := strings.ToLower(strings.TrimSpace(script.Metadata.Import.Source))
	if profile == "" {
		return time.Time{}, nil
	}
	ensureCredentialPreservingImportMetadata(script, profile)
	fingerprint, err := fingerprintImportDraftCredentials(script)
	if err != nil {
		return time.Time{}, err
	}
	recordingSessionID := ""
	recordingSnapshotDigest := ""
	if len(recordingSessionIDs) > 0 {
		recordingSessionID = strings.TrimSpace(recordingSessionIDs[0])
	}
	if len(recordingSessionIDs) > 1 {
		recordingSnapshotDigest = strings.TrimSpace(recordingSessionIDs[1])
	}
	if recordingSessionID != "" && recordingSnapshotDigest == "" {
		return time.Time{}, errors.New("recording preview snapshot digest is required")
	}
	hasUnavailableCredentials := scriptHasUnavailableImportCredentialValues(script)
	if hasUnavailableCredentials && fingerprint.FullDigest == "" {
		fingerprint.FullDigest, err = fullImportDraftExecutionDigest(script)
		if err != nil {
			return time.Time{}, err
		}
	}
	if !fingerprint.HasRawCredentials && !hasUnavailableCredentials && recordingSessionID == "" {
		stripImportDraftAttestation(script)
		return time.Time{}, nil
	}
	exactCandidate := cloneScriptDefinition(script)
	if exactCandidate == nil {
		return time.Time{}, errors.New("failed to clone import draft")
	}
	_, actor, _ := s.resolveAuditActor(r)
	now := time.Now().UTC()
	token := uuid.NewString()
	projectID := importDraftProjectIDForRequest(r, script)

	protectedStorePresent, err := s.brunoArtifactStorePresent()
	if err != nil {
		return time.Time{}, markProtectedImportStoreUnavailable(err)
	}
	if protectedStorePresent {
		protectedCandidate := cloneScriptDefinition(script)
		if protectedCandidate == nil {
			return time.Time{}, errors.New("failed to clone protected import draft")
		}
		protectedCandidate.ProjectID = projectID
		if err := sanitizePersistentImportReviewSurfaces(protectedCandidate); err != nil {
			return time.Time{}, err
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
				"execution_graph_digest":    fingerprint.ExecutionGraphDigest,
			}
		}
		sessionID, expiresAt, err := s.registerImportCandidateSession(
			r,
			profile,
			protectedCandidate,
		)
		if err != nil {
			// A configured protected store is an explicit durability boundary.
			// Never silently downgrade its operational or filesystem failure to
			// a process-local capability, even on a single controller.
			return time.Time{}, err
		} else if strings.TrimSpace(sessionID) == "" {
			return time.Time{}, errors.New("protected import draft session was not created")
		} else {
			preserved := ensureScriptImportPreserved(script, profile)
			preserved[importDraftAttestationMetadataKey] =
				importDraftProtectedTokenPrefix + sessionID
			return expiresAt, nil
		}
	}
	if s.haExecution {
		return time.Time{}, errors.New("credential-preserving legacy import saves require shared protected sessions in HA mode")
	}

	record := &importDraftAttestation{
		Token:                          token,
		Profile:                        profile,
		ProjectID:                      projectID,
		Actor:                          actor,
		Protocol:                       strings.ToLower(strings.TrimSpace(script.Protocol)),
		CredentialDigests:              fingerprint.CredentialDigests,
		StructureDigest:                fingerprint.StructureDigest,
		ExecutionGraphDigest:           fingerprint.ExecutionGraphDigest,
		FullDigest:                     fingerprint.FullDigest,
		HasUnavailableCredentialValues: hasUnavailableCredentials,
		Script:                         exactCandidate,
		RecordingSessionID:             recordingSessionID,
		RecordingSnapshotDigest:        recordingSnapshotDigest,
		ExpiresAt:                      now.Add(importDraftAttestationTTL),
	}

	s.importDraftAttestationMu.Lock()
	defer s.importDraftAttestationMu.Unlock()
	s.sweepImportDraftAttestationsLocked(now)
	if s.importDraftAttestations == nil {
		s.importDraftAttestations = make(map[string]*importDraftAttestation)
	}
	if len(s.importDraftAttestations) >= importDraftAttestationCapacity {
		return time.Time{}, errors.New("credential-preserving import save capacity is busy")
	}
	s.importDraftAttestations[token] = record
	preserved := ensureScriptImportPreserved(script, profile)
	preserved[importDraftAttestationMetadataKey] = token
	return record.ExpiresAt, nil
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
	expiresAt, err := s.attachImportDraftAttestationWithExpiry(
		r,
		script,
		claim.RecordingSessionID,
		claim.RecordingSnapshotDigest,
	)
	if err != nil {
		return time.Time{}, err
	}
	if err := s.completeImportDraftAttestation(claim); err != nil {
		s.discardImportDraftAttestation(r, script)
		return time.Time{}, err
	}
	return expiresAt, nil
}

func (s *Server) claimImportDraftAttestation(
	r *http.Request,
	script *dsl.Script,
) (*importDraftAttestationClaim, error) {
	if s == nil || r == nil || script == nil {
		return nil, errImportDraftAttestationInvalid
	}
	token := importDraftAttestationToken(script)
	if token == "" {
		return nil, errImportDraftAttestationInvalid
	}
	if strings.HasPrefix(token, importDraftProtectedTokenPrefix) {
		return s.claimProtectedImportDraftAttestation(r, script, token)
	}
	_, actor, _ := s.resolveAuditActor(r)
	now := time.Now().UTC()

	s.importDraftAttestationMu.Lock()
	defer s.importDraftAttestationMu.Unlock()
	s.sweepImportDraftAttestationsLocked(now)
	record := s.importDraftAttestations[token]
	if record == nil || record.Claimed ||
		record.ProjectID != normalizeProjectID(script.ProjectID) ||
		record.Actor != actor ||
		record.Protocol != strings.ToLower(strings.TrimSpace(script.Protocol)) ||
		script.Metadata == nil || script.Metadata.Import == nil ||
		!strings.EqualFold(strings.TrimSpace(script.Metadata.Import.Source), record.Profile) {
		return nil, errImportDraftAttestationInvalid
	}
	restored := cloneScriptDefinition(script)
	if restored == nil || record.Script == nil ||
		reconcileMaskedScriptSecrets(restored, record.Script) != nil {
		return nil, errImportDraftAttestationInvalid
	}
	fingerprint, err := fingerprintImportDraftCredentials(restored)
	if err != nil {
		return nil, errImportDraftAttestationInvalid
	}
	if record.HasUnavailableCredentialValues && fingerprint.FullDigest == "" {
		fingerprint.FullDigest, err = fullImportDraftExecutionDigest(restored)
		if err != nil {
			return nil, errImportDraftAttestationInvalid
		}
	}
	credentialAllowed := (fingerprint.HasRawCredentials || record.HasUnavailableCredentialValues) &&
		importDraftFingerprintAllowed(record, fingerprint)
	if record.HasUnavailableCredentialValues && !fingerprint.HasRawCredentials {
		credentialAllowed = fingerprint.FullDigest != "" && fingerprint.FullDigest == record.FullDigest
	}
	lineageAllowed := record.RecordingSessionID != "" &&
		record.ExecutionGraphDigest != "" &&
		record.ExecutionGraphDigest == fingerprint.ExecutionGraphDigest
	if !credentialAllowed && !lineageAllowed {
		return nil, errImportDraftAttestationInvalid
	}
	record.Claimed = true
	claim := &importDraftAttestationClaim{
		Token:                         token,
		Profile:                       record.Profile,
		AttestedExecutionGraphDigest:  record.ExecutionGraphDigest,
		SubmittedExecutionGraphDigest: fingerprint.ExecutionGraphDigest,
		Script:                        restored,
	}
	if lineageAllowed {
		claim.RecordingSessionID = record.RecordingSessionID
		claim.RecordingSnapshotDigest = record.RecordingSnapshotDigest
	}
	return claim, nil
}

func (s *Server) claimProtectedImportDraftAttestation(
	r *http.Request,
	script *dsl.Script,
	token string,
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
		return nil, errImportDraftAttestationInvalid
	}
	release := true
	defer func() {
		clearProtectedImportCandidate(candidate)
		if release {
			s.releaseImportCandidateClaim(protectedClaim)
		}
	}()

	attestedFingerprint, err := fingerprintImportDraftCredentials(candidate.Script)
	if err != nil {
		return nil, errImportDraftAttestationInvalid
	}
	hasUnavailableCredentials := scriptHasUnavailableImportCredentialValues(candidate.Script)
	if hasUnavailableCredentials && attestedFingerprint.FullDigest == "" {
		attestedFingerprint.FullDigest, err = fullImportDraftExecutionDigest(candidate.Script)
		if err != nil {
			return nil, errImportDraftAttestationInvalid
		}
	}
	restored := cloneScriptDefinition(script)
	if restored == nil || reconcileMaskedScriptSecrets(restored, candidate.Script) != nil {
		return nil, errImportDraftAttestationInvalid
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
		Context:                       r.Context(),
		ProtectedClaim:                protectedClaim,
		ProtectedLease:                lease,
		RawSource:                     rawSource,
		Script:                        restored,
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
	if record == nil {
		return false
	}
	if record.FullDigest != "" || fingerprint.FullDigest != "" {
		return record.FullDigest != "" &&
			fingerprint.FullDigest != "" &&
			record.FullDigest == fingerprint.FullDigest
	}
	if len(record.CredentialDigests) == 0 ||
		len(fingerprint.CredentialDigests) == 0 {
		return false
	}
	// Credential digests are bound to both the exact value and its executable
	// path/occurrence. That is the authority this one-shot capability grants.
	// Non-credential authoring changes (pacing, timeout, display metadata,
	// protocol profile, and similar import-screen options) do not need to be
	// byte-identical to the inspection response and must not force the operator
	// to re-import or re-enter a password. A new, moved, or changed credential
	// produces a different location-bound digest and is still rejected below.
	for digest, count := range fingerprint.CredentialDigests {
		if count <= 0 || record.CredentialDigests[digest] < count {
			return false
		}
	}
	return len(fingerprint.CredentialDigests) > 0
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
	s.importDraftAttestationMu.Lock()
	defer s.importDraftAttestationMu.Unlock()
	if record := s.importDraftAttestations[claim.Token]; record != nil {
		record.Claimed = false
	}
}

// retainCommittedImportDraftAttestation drops process-local sensitive bytes and
// stops lease renewal without releasing the one-shot capability. Once imported
// assets are durable, a cleanup failure must leave the capability claimed until
// its normal expiry/reconciliation path; releasing it would authorize the same
// credential-bearing draft to create a second batch under a new operation ID.
func (s *Server) retainCommittedImportDraftAttestation(claim *importDraftAttestationClaim) {
	if s == nil || claim == nil {
		return
	}
	clear(claim.RawSource)
	claim.RawSource = nil
	if claim.ProtectedLease != nil {
		claim.ProtectedLease.stop()
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
	s.importDraftAttestationMu.Lock()
	defer s.importDraftAttestationMu.Unlock()
	if record := s.importDraftAttestations[claim.Token]; record != nil && record.Claimed {
		record.Script = nil
		delete(s.importDraftAttestations, claim.Token)
	}
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
	s.importDraftAttestationMu.Lock()
	if record := s.importDraftAttestations[token]; record != nil {
		record.Script = nil
	}
	delete(s.importDraftAttestations, token)
	s.importDraftAttestationMu.Unlock()
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

func (s *Server) sweepImportDraftAttestationsLocked(now time.Time) {
	for token, record := range s.importDraftAttestations {
		if record == nil || !record.ExpiresAt.After(now) {
			if record != nil {
				record.Script = nil
			}
			delete(s.importDraftAttestations, token)
		}
	}
}

func importDraftAttestationToken(script *dsl.Script) string {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil {
		return ""
	}
	token, _ := script.Metadata.Import.Preserved[importDraftAttestationMetadataKey].(string)
	return strings.TrimSpace(token)
}

func stripImportDraftAttestation(script *dsl.Script) {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil ||
		script.Metadata.Import.Preserved == nil {
		return
	}
	delete(script.Metadata.Import.Preserved, importDraftAttestationMetadataKey)
	delete(script.Metadata.Import.Preserved, importDraftRecordingLineageMetadataKey)
}

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

func fingerprintImportDraftCredentials(
	script *dsl.Script,
) (importDraftCredentialFingerprint, error) {
	if script == nil {
		return importDraftCredentialFingerprint{}, errors.New("import draft is required")
	}
	executionGraphDigest, err := canonicalImportDraftExecutionGraphDigest(script)
	if err != nil {
		return importDraftCredentialFingerprint{}, err
	}
	raw := importResponseRawScriptValue(script)
	projectedScript := maskScriptSecrets(script)
	if projectedScript == nil {
		digest, err := fullImportDraftExecutionDigest(script)
		return importDraftCredentialFingerprint{
			ExecutionGraphDigest: executionGraphDigest,
			FullDigest:           digest,
			HasRawCredentials:    err == nil,
		}, err
	}
	projected := importResponseRawScriptValue(projectedScript)
	requirements := secretBindingRequirementsForImportScripts(projected)
	if opaqueImportSourceProjectionChanged(raw, projected) {
		digest, err := fullImportDraftExecutionDigest(script)
		return importDraftCredentialFingerprint{
			ExecutionGraphDigest: executionGraphDigest,
			FullDigest:           digest,
			HasRawCredentials:    err == nil,
		}, err
	}
	if len(requirements) == 0 {
		if runtimeCredentialSourceProjectionChanged(script, projectedScript) ||
			importHeaderFidelityProjectionChanged(script, projectedScript) {
			digest, err := fullImportDraftExecutionDigest(script)
			return importDraftCredentialFingerprint{
				ExecutionGraphDigest: executionGraphDigest,
				FullDigest:           digest,
				HasRawCredentials:    err == nil,
			}, err
		}
		return importDraftCredentialFingerprint{
			ExecutionGraphDigest: executionGraphDigest,
		}, nil
	}
	values := sourceSecretValuesForImportNodes(raw, projected, requirements)
	if len(values) == 0 {
		return importDraftCredentialFingerprint{
			ExecutionGraphDigest: executionGraphDigest,
		}, nil
	}
	if len(values) != len(requirements) {
		digest, err := fullImportDraftExecutionDigest(script)
		return importDraftCredentialFingerprint{
			ExecutionGraphDigest: executionGraphDigest,
			FullDigest:           digest,
			HasRawCredentials:    err == nil,
		}, err
	}
	digests := make(map[string]int, len(values))
	requirementsByID := make(map[string]SecretBindingRequirement, len(requirements))
	ids := make([]string, 0, len(requirements))
	for _, requirement := range requirements {
		requirementsByID[requirement.ID] = requirement
		if _, available := values[requirement.ID]; available {
			ids = append(ids, requirement.ID)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		requirement := requirementsByID[id]
		location := requirement.Path + "\x00" +
			fmt.Sprintf("%d", requirement.Occurrence)
		sum := sha256.Sum256([]byte(
			"apexload.import-draft-credential.v2\x00" +
				location + "\x00" + values[id],
		))
		digests[hex.EncodeToString(sum[:])]++
	}
	structureDigest, err := canonicalImportDraftStructureDigest(script)
	if err != nil {
		return importDraftCredentialFingerprint{}, err
	}
	return importDraftCredentialFingerprint{
		CredentialDigests:    digests,
		StructureDigest:      structureDigest,
		ExecutionGraphDigest: executionGraphDigest,
		HasRawCredentials:    true,
	}, nil
}

// opaqueImportSourceProjectionChanged detects exact, inert source payloads
// whose credentials cannot be represented as ordinary guided binding
// requirements. Any such delta is full-digest attested so a legacy save cannot
// silently persist the disclosure projection in place of the operator's exact
// source.
func opaqueImportSourceProjectionChanged(raw, projected any) bool {
	switch rawValue := raw.(type) {
	case map[string]any:
		projectedValue, _ := projected.(map[string]any)
		for key, child := range rawValue {
			projectedChild := projectedValue[key]
			if isOpaqueImportSourcePayloadKey(key) {
				rawPayload, rawErr := json.Marshal(child)
				projectedPayload, projectedErr := json.Marshal(projectedChild)
				if rawErr != nil || projectedErr != nil ||
					string(rawPayload) != string(projectedPayload) {
					return true
				}
				continue
			}
			if opaqueImportSourceProjectionChanged(child, projectedChild) {
				return true
			}
		}
	case []any:
		projectedValue, _ := projected.([]any)
		for index, child := range rawValue {
			// Ordinary review/provenance arrays may be shortened by disclosure
			// sanitization. That is not an opaque executable-source delta by itself.
			// Continue into a missing child with nil so only a nested key explicitly
			// recognized by isOpaqueImportSourcePayloadKey can authorize a full-draft
			// attestation.
			var projectedChild any
			if index < len(projectedValue) {
				projectedChild = projectedValue[index]
			}
			if opaqueImportSourceProjectionChanged(child, projectedChild) {
				return true
			}
		}
	}
	return false
}

func canonicalImportDraftStructureDigest(script *dsl.Script) (string, error) {
	projected := maskScriptSecrets(script)
	if projected == nil {
		return "", errors.New("project import draft structure")
	}
	stripImportDraftAttestation(projected)
	if err := sanitizePersistentImportReviewSurfaces(projected); err != nil {
		return "", err
	}
	projected.ID = ""
	projected.RefKey = ""
	projected.Name = ""
	projected.ProjectID = ""
	projected.FolderID = nil
	projected.CreatedAt = time.Time{}
	projected.UpdatedAt = time.Time{}
	value, err := scriptAsJSONValue(projected)
	if err != nil {
		return "", err
	}
	value = canonicalizeImportDraftCredentialTokens(value)
	payload, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("marshal import draft structure: %w", err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func canonicalImportDraftExecutionGraphDigest(script *dsl.Script) (string, error) {
	// Recording lineage is an authorization boundary, not an equivalence
	// comparison. Hash the exact executable graph, including credential bytes
	// and exact secret-reference names. Server-owned binding/parameterization
	// endpoints rotate the capability after their mutation, so no
	// credential-canonicalization is necessary or safe here.
	return scriptCorrelationActionGraphDigest(script)
}

func canonicalizeImportDraftCredentialTokens(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			out[key] = canonicalizeImportDraftCredentialTokens(child)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, child := range typed {
			out[index] = canonicalizeImportDraftCredentialTokens(child)
		}
		return out
	case string:
		const slot = "__APEXLOAD_IMPORTED_CREDENTIAL_SLOT__"
		normalized := strings.ReplaceAll(typed, maskedScriptSecret, slot)
		return scriptSecretTemplatePattern.ReplaceAllString(normalized, slot)
	default:
		return value
	}
}

func runtimeCredentialSourceProjectionChanged(raw, projected *dsl.Script) bool {
	if raw == nil || projected == nil {
		return false
	}
	if len(raw.Config.JavaVuser.AdditionalVMParameters) !=
		len(projected.Config.JavaVuser.AdditionalVMParameters) {
		return true
	}
	for index := range raw.Config.JavaVuser.AdditionalVMParameters {
		if raw.Config.JavaVuser.AdditionalVMParameters[index] !=
			projected.Config.JavaVuser.AdditionalVMParameters[index] {
			return true
		}
	}
	if len(raw.DataSources) != len(projected.DataSources) ||
		len(raw.Parameters) != len(projected.Parameters) {
		return false
	}
	for index := range raw.DataSources {
		if raw.DataSources[index].InlineData != projected.DataSources[index].InlineData {
			return true
		}
	}
	for index := range raw.Parameters {
		if len(raw.Parameters[index].Values) != len(projected.Parameters[index].Values) {
			return false
		}
		for valueIndex := range raw.Parameters[index].Values {
			if raw.Parameters[index].Values[valueIndex] != projected.Parameters[index].Values[valueIndex] {
				return true
			}
		}
	}
	return false
}

func fullImportDraftExecutionDigest(script *dsl.Script) (string, error) {
	clone := cloneScriptDefinition(script)
	if clone == nil {
		return "", errors.New("clone import draft")
	}
	stripImportDraftAttestation(clone)
	if err := sanitizePersistentImportReviewSurfaces(clone); err != nil {
		return "", fmt.Errorf("sanitize import draft fingerprint: %w", err)
	}
	clone.ID = ""
	clone.RefKey = ""
	clone.Name = ""
	clone.ProjectID = ""
	clone.FolderID = nil
	clone.CreatedAt = time.Time{}
	clone.UpdatedAt = time.Time{}
	payload, err := json.Marshal(clone)
	if err != nil {
		return "", fmt.Errorf("marshal import draft fingerprint: %w", err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

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
		expiresAt, err := s.attachImportDraftAttestationWithExpiry(
			r,
			scripts[index],
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
