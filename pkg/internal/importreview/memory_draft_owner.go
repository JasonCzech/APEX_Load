package importreview

import (
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/dsl"
)

var ErrDraftAttestationInvalid = errors.New("import draft attestation is invalid or expired")
var ErrDraftAttestationBusy = errors.New("import draft is in use by another request")

type DraftAttestation struct {
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
	// Script is the exact server-owned candidate. Responses carry only its
	// disclosure projection plus Token; expiry is policy-controlled.
	Script                  *dsl.Script
	RecordingSessionID      string
	RecordingSnapshotDigest string
	RecordingBaseline       *RecordingBaseline
	ExpiresAt               time.Time
	ExpirationDisabled      bool
	Claimed                 bool
	// Retained marks a capability whose assets committed but whose cleanup did
	// not finish. It stays claimed so it cannot be consumed again, and it is
	// reported as invalid rather than as a request still in flight.
	Retained bool
}

func DraftFingerprintAllowed(
	record *DraftAttestation,
	fingerprint ImportDraftCredentialFingerprint,
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

// MemoryDraftOwner serializes one-shot draft capabilities for hosts without a
// protected durable store. The exact candidate and every state transition have
// one owner shared by controller and standalone recording bridge.
type MemoryDraftOwner struct {
	mu       sync.Mutex
	records  map[string]*DraftAttestation
	now      func() time.Time
	capacity int
}
type MemoryDraftOptions struct {
	Now      func() time.Time
	Capacity int
}

func NewMemoryDraftOwner(options ...MemoryDraftOptions) *MemoryDraftOwner {
	owner := &MemoryDraftOwner{records: make(map[string]*DraftAttestation), now: time.Now, capacity: ImportDraftAttestationCapacity}
	if len(options) > 0 {
		if options[0].Now != nil {
			owner.now = options[0].Now
		}
		if options[0].Capacity > 0 {
			owner.capacity = options[0].Capacity
		}
	}
	return owner
}

type DraftScope struct{ ProjectID, Actor string }
type DraftAttachOptions struct {
	Scope                                       DraftScope
	DisableExpiration                           bool
	RecordingSessionID, RecordingSnapshotDigest string
	RecordingBaseline                           *RecordingBaseline
	TextMemo                                    *ImportProjectionTextMemo
}
type MemoryDraftClaim struct {
	Token, Profile                                                                                           string
	Script                                                                                                   *dsl.Script
	ExpirationDisabled                                                                                       bool
	RecordingSessionID, RecordingSnapshotDigest, AttestedExecutionGraphDigest, SubmittedExecutionGraphDigest string
	RecordingBaseline                                                                                        *RecordingBaseline
	JavaClasspathReviewChecked                                                                               bool
}

// DraftClaimTransform preserves host-owned authoring checks without giving the
// shared owner a dependency on controller storage or Java artifact services.
type DraftClaimTransform struct {
	Prepare    func(submitted, baseline *dsl.Script) (*dsl.Script, error)
	Verify     func(restored, baseline *dsl.Script) error
	Finish     func(restored *dsl.Script)
	JavaReview bool
}

func normalizeDraftProject(id string) string {
	if id = strings.TrimSpace(id); id != "" {
		return id
	}
	return config.DefaultProjectID
}

// PrepareDraftAttestation fingerprints and snapshots a candidate once. Durable
// and memory adapters reuse the same projection and preservation decision.
func PrepareDraftAttestation(script *dsl.Script, options DraftAttachOptions) (*DraftAttestation, *dsl.Script, error) {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil {
		return nil, nil, nil
	}
	profile := strings.ToLower(strings.TrimSpace(script.Metadata.Import.Source))
	if profile == "" {
		return nil, nil, nil
	}
	EnsureCredentialPreservingImportMetadata(script, profile)
	fingerprint, projection, err := FingerprintImportDraftCredentialsWithProjection(script, options.TextMemo)
	if err != nil {
		return nil, nil, err
	}
	recordingID, digest := strings.TrimSpace(options.RecordingSessionID), strings.TrimSpace(options.RecordingSnapshotDigest)
	if recordingID != "" && digest == "" {
		return nil, nil, errors.New("recording preview snapshot digest is required")
	}
	unavailable := ScriptHasUnavailableImportCredentialValuesFromProjection(script, projection) || RequiresExactRepairDraft(script)
	if unavailable && fingerprint.FullDigest == "" {
		fingerprint.FullDigest, err = FullImportDraftExecutionDigest(script)
		if err != nil {
			return nil, nil, err
		}
	}
	if !fingerprint.HasRawCredentials && !unavailable && recordingID == "" {
		StripImportDraftAttestation(script)
		return nil, projection.Script(), nil
	}
	exact := CloneScriptDefinition(script)
	if exact == nil {
		return nil, nil, errors.New("failed to clone import draft")
	}
	return &DraftAttestation{Profile: profile, ProjectID: normalizeDraftProject(options.Scope.ProjectID), Actor: options.Scope.Actor, Protocol: strings.ToLower(strings.TrimSpace(script.Protocol)), CredentialDigests: fingerprint.CredentialDigests, StructureDigest: fingerprint.StructureDigest, ExecutionGraphDigest: fingerprint.ExecutionGraphDigest, FullDigest: fingerprint.FullDigest, HasUnavailableCredentialValues: unavailable, Script: exact, RecordingSessionID: recordingID, RecordingSnapshotDigest: digest, RecordingBaseline: options.RecordingBaseline.Clone(), ExpirationDisabled: options.DisableExpiration}, projection.Script(), nil
}
func (o *MemoryDraftOwner) Attach(script *dsl.Script, options DraftAttachOptions) (time.Time, *dsl.Script, error) {
	record, projection, err := PrepareDraftAttestation(script, options)
	if err != nil || record == nil {
		return time.Time{}, projection, err
	}
	expires, err := o.AttachPrepared(script, record)
	return expires, projection, err
}

// AttachPrepared takes ownership of a detached prepared record after host admission.
func (o *MemoryDraftOwner) AttachPrepared(script *dsl.Script, record *DraftAttestation) (time.Time, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	now := o.now().UTC()
	o.sweepLocked(now)
	if len(o.records) >= o.capacity {
		return time.Time{}, errors.New("credential-preserving import save capacity is busy")
	}
	record.Token = uuid.NewString()
	if !record.ExpirationDisabled {
		record.ExpiresAt = now.Add(ImportDraftAttestationTTL)
	}
	o.records[record.Token] = record
	EnsureScriptImportPreserved(script, record.Profile)[ImportDraftAttestationMetadataKey] = record.Token
	return record.ExpiresAt, nil
}
func (o *MemoryDraftOwner) Claim(scope DraftScope, script *dsl.Script, transform *DraftClaimTransform) (*MemoryDraftClaim, error) {
	token := ImportDraftAttestationToken(script)
	if token == "" || script == nil {
		return nil, ErrDraftAttestationInvalid
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.sweepLocked(o.now().UTC())
	record := o.records[token]
	if record == nil || record.ProjectID != normalizeDraftProject(scope.ProjectID) || record.ProjectID != normalizeDraftProject(script.ProjectID) || record.Actor != scope.Actor || record.Protocol != strings.ToLower(strings.TrimSpace(script.Protocol)) || script.Metadata == nil || script.Metadata.Import == nil || !strings.EqualFold(strings.TrimSpace(script.Metadata.Import.Source), record.Profile) {
		return nil, ErrDraftAttestationInvalid
	}
	if record.Claimed {
		if record.Retained {
			return nil, ErrDraftAttestationInvalid
		}
		return nil, ErrDraftAttestationBusy
	}
	javaReview := transform != nil && transform.JavaReview && strings.EqualFold(record.Protocol, "java_vuser")
	baseline := record.Script
	if javaReview {
		baseline = CloneScriptDefinition(record.Script)
	}
	if javaReview && transform.Prepare != nil {
		var err error
		script, err = transform.Prepare(script, baseline)
		if err != nil {
			return nil, err
		}
	}
	restored := CloneScriptDefinition(script)
	if restored == nil || record.Script == nil || ReconcileMaskedScriptSecrets(restored, record.Script) != nil {
		return nil, ErrDraftAttestationInvalid
	}
	if javaReview && transform.Verify != nil {
		if err := transform.Verify(restored, baseline); err != nil {
			return nil, err
		}
	}
	fingerprint, err := FingerprintImportDraftCredentials(restored)
	if err != nil {
		return nil, ErrDraftAttestationInvalid
	}
	if record.HasUnavailableCredentialValues && fingerprint.FullDigest == "" {
		fingerprint.FullDigest, err = FullImportDraftExecutionDigest(restored)
		if err != nil {
			return nil, ErrDraftAttestationInvalid
		}
	}
	credentials := (fingerprint.HasRawCredentials || record.HasUnavailableCredentialValues) && DraftFingerprintAllowed(record, fingerprint)
	if record.HasUnavailableCredentialValues && !fingerprint.HasRawCredentials {
		credentials = fingerprint.FullDigest != "" && fingerprint.FullDigest == record.FullDigest
	}
	lineage := record.RecordingSessionID != "" && record.ExecutionGraphDigest != "" && record.ExecutionGraphDigest == fingerprint.ExecutionGraphDigest
	if !credentials && !lineage {
		return nil, ErrDraftAttestationInvalid
	}
	if javaReview && transform.Finish != nil {
		transform.Finish(restored)
	}
	record.Claimed = true
	claim := &MemoryDraftClaim{Token: token, Profile: record.Profile, Script: restored, ExpirationDisabled: record.ExpirationDisabled, AttestedExecutionGraphDigest: record.ExecutionGraphDigest, SubmittedExecutionGraphDigest: fingerprint.ExecutionGraphDigest, JavaClasspathReviewChecked: javaReview}
	if lineage {
		claim.RecordingSessionID = record.RecordingSessionID
		claim.RecordingSnapshotDigest = record.RecordingSnapshotDigest
		claim.RecordingBaseline = record.RecordingBaseline.Clone()
	}
	return claim, nil
}
func (o *MemoryDraftOwner) Release(token string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if record := o.records[token]; record != nil && !record.Retained {
		record.Claimed = false
	}
}
func (o *MemoryDraftOwner) RetainCommitted(token string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if record := o.records[token]; record != nil && record.Claimed {
		record.Retained = true
	}
}
func (o *MemoryDraftOwner) Complete(token string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if record := o.records[token]; record != nil && record.Claimed {
		record.Script = nil
		delete(o.records, token)
	}
}
func (o *MemoryDraftOwner) Discard(token string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if record := o.records[token]; record != nil {
		record.Script = nil
	}
	delete(o.records, token)
}
func (o *MemoryDraftOwner) PruneExpired() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.sweepLocked(o.now().UTC())
}
func (o *MemoryDraftOwner) sweepLocked(now time.Time) {
	for token, record := range o.records {
		if record == nil || (!record.ExpirationDisabled && !record.ExpiresAt.After(now)) {
			if record != nil {
				record.Script = nil
			}
			delete(o.records, token)
		}
	}
}
func (o *MemoryDraftOwner) Len() int { o.mu.Lock(); defer o.mu.Unlock(); return len(o.records) }
func (o *MemoryDraftOwner) Rotate(scope DraftScope, claim *MemoryDraftClaim, script *dsl.Script) (time.Time, error) {
	StripImportDraftAttestation(script)
	if claim == nil {
		return time.Time{}, nil
	}
	expires, _, err := o.Attach(script, DraftAttachOptions{Scope: scope, DisableExpiration: claim.ExpirationDisabled, RecordingSessionID: claim.RecordingSessionID, RecordingSnapshotDigest: claim.RecordingSnapshotDigest, RecordingBaseline: claim.RecordingBaseline})
	if err != nil {
		return time.Time{}, err
	}
	o.Complete(claim.Token)
	return expires, nil
}
