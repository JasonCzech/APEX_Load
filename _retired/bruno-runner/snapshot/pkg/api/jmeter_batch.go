package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/jmeter"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/importsession"
)

const jmeterBatchProfile = "jmeter_batch_v1"
const jmeterBatchReceiptProfile = "jmeter_batch_receipt_v1"
const jmeterBatchMetadata = "jmeter_batch_v1"
const jmeterBatchOriginMetadata = "jmeter_batch_origin_v1"

type jmeterBatchIdentity struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
}
type jmeterBatchCandidate struct {
	Identity     jmeterBatchIdentity    `json:"identity"`
	ProjectID    string                 `json:"project_id"`
	Source       []jmeterImportDocument `json:"source"`
	Advisories   diag.Diagnostics       `json:"advisories,omitempty"`
	SourceReview *sourceview.Review     `json:"source_review,omitempty"`
	SourceDigest string                 `json:"source_digest"`
	Scripts      []*dsl.Script          `json:"scripts"`
	Scenario     *jmeter.ScenarioDraft  `json:"scenario_draft"`
	History      []JMeterBatchOperation `json:"history,omitempty"`
	ExpiresAt    time.Time              `json:"expires_at"`
}
type jmeterBatchReceipt struct {
	Kind          string         `json:"kind"`
	RequestDigest string         `json:"request_digest,omitempty"`
	Digest        string         `json:"digest"`
	Response      map[string]any `json:"response,omitempty"`
}

func batchDigest(v any) string {
	b, _ := json.Marshal(v)
	defer clear(b)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func batchReceiptID(revision string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("jmeter-batch-receipt:"+revision)).String()
}
func stampBatch(b *jmeterBatchCandidate) {
	for _, s := range b.Scripts {
		ensureScriptImportPreserved(s, "jmeter")[jmeterBatchMetadata] = b.Identity
	}
}
func scriptBatch(s *dsl.Script) (jmeterBatchIdentity, bool) {
	var id jmeterBatchIdentity
	if s == nil || s.Metadata == nil || s.Metadata.Import == nil {
		return id, false
	}
	v, ok := s.Metadata.Import.Preserved[jmeterBatchMetadata]
	if !ok {
		return id, false
	}
	data, _ := json.Marshal(v)
	_ = json.Unmarshal(data, &id)
	return id, true
}
func (s *Server) newJMeterBatch(r *http.Request, result *jmeter.ConvertResult, requests []*jmeterImportRequest) (*jmeterBatchCandidate, error) {
	if len(requests) == 0 || requests[0] == nil || len(result.Scripts) == 0 {
		return nil, nil
	}
	messaging := false
	for _, script := range result.Scripts {
		messaging = messaging || script.Config.JMeter != nil
	}
	if !messaging {
		return nil, nil
	}
	present, err := s.brunoArtifactStorePresent()
	if err != nil || !present {
		return nil, err
	}
	request := requests[0]
	docs := request.Documents
	if len(docs) == 0 {
		docs = []jmeterImportDocument{{Data: request.Data, FileName: request.Opts.FileName}}
	}
	id := uuid.NewString()
	b := &jmeterBatchCandidate{Identity: jmeterBatchIdentity{ID: id, Revision: uuid.NewString()}, ProjectID: jmeterDraftProjectID(result), Source: docs, SourceDigest: batchDigest(docs), SourceReview: result.SourceReview, Scripts: result.Scripts, Scenario: result.ScenarioDraft, ExpiresAt: time.Now().UTC().Add(importCandidateSessionTTL)}
	for _, d := range result.Diagnostics {
		if d.Severity != diag.Blocker {
			b.Advisories = append(b.Advisories, d)
		}
	}
	stampBatch(b)
	return b, nil
}
func (s *Server) saveJMeterBatch(r *http.Request, b *jmeterBatchCandidate) error {
	manager, err := s.protectedImportSessionManager()
	if err != nil {
		return err
	}
	_, actor, _ := s.resolveAuditActor(r)
	data, err := json.Marshal(b)
	if err != nil {
		return err
	}
	defer clear(data)
	return manager.Save(r.Context(), importsession.SessionDescriptor{ID: b.Identity.Revision, Profile: jmeterBatchProfile, ProjectID: b.ProjectID, Actor: actor, ExpiresAt: b.ExpiresAt}, data, importCandidateSessionMaxBytes)
}
func (s *Server) batchScope(r *http.Request, id, profile, project string) importsession.Scope {
	_, actor, _ := s.resolveAuditActor(r)
	return importsession.Scope{ID: id, Profile: profile, ProjectID: normalizeProjectID(project), Actor: actor}
}
func (s *Server) openBatchReceipt(r *http.Request, revision, project string) (*jmeterBatchReceipt, error) {
	m, err := s.protectedImportSessionManager()
	if err != nil {
		return nil, err
	}
	b, _, err := m.Open(r.Context(), s.batchScope(r, batchReceiptID(revision), jmeterBatchReceiptProfile, project), importCandidateSessionMaxBytes)
	if err != nil {
		return nil, err
	}
	defer clear(b)
	var receipt jmeterBatchReceipt
	err = json.Unmarshal(b, &receipt)
	return &receipt, err
}
func (s *Server) claimJMeterBatch(r *http.Request, id jmeterBatchIdentity, project string) (*jmeterBatchCandidate, *importsession.Claim, error) {
	if uuid.Validate(id.ID) != nil || uuid.Validate(id.Revision) != nil {
		return nil, nil, errors.New("invalid batch identity")
	}
	if p, ok := authorizedProjectFromRequest(r); ok && p != normalizeProjectID(project) {
		return nil, nil, os.ErrNotExist
	}
	m, err := s.protectedImportSessionManager()
	if err != nil {
		return nil, nil, err
	}
	c, err := m.Claim(r.Context(), s.batchScope(r, id.Revision, jmeterBatchProfile, project), importCandidateSessionMaxBytes)
	if err != nil {
		return nil, nil, err
	}
	defer clear(c.Payload)
	var b jmeterBatchCandidate
	if err = json.Unmarshal(c.Payload, &b); err != nil || b.Identity != id || b.ProjectID != normalizeProjectID(project) || batchDigest(b.Source) != b.SourceDigest {
		_ = m.Release(c.Claim)
		clearJMeterBatchSource(&b)
		return nil, nil, errors.New("invalid protected batch")
	}
	return &b, &c.Claim, nil
}
func (s *Server) publishBatchReceipt(r *http.Request, b *jmeterBatchCandidate, claim *importsession.Claim, receipt jmeterBatchReceipt) error {
	m, err := s.protectedImportSessionManager()
	if err != nil {
		return err
	}
	data, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	defer clear(data)
	return m.PublishReceipt(r.Context(), *claim, importsession.SessionDescriptor{ID: batchReceiptID(b.Identity.Revision), Profile: jmeterBatchReceiptProfile, ProjectID: claim.ProjectID, Actor: claim.Actor, ExpiresAt: b.ExpiresAt}, data, importCandidateSessionMaxBytes)
}

// Save intent seals the revision before crossing into the asset transaction.
// Failed saves may retry the identical request; a repair cannot race a stale
// saver after its lease expires. Existing save receipts recover committed assets.
func (s *Server) fenceJMeterBatchSave(r *http.Request, scripts []dsl.Script, scenario *jmeter.ScenarioDraft) error {
	id, present := scriptBatch(&scripts[0])
	if !present {
		for i := range scripts {
			if _, ok := scriptBatch(&scripts[i]); ok {
				return errors.New("mixed batch revisions")
			}
		}
		return nil
	}
	for i := range scripts {
		other, ok := scriptBatch(&scripts[i])
		if !ok || other != id {
			return errors.New("mixed batch revisions")
		}
	}
	project := scripts[0].ProjectID
	digest := batchDigest(struct {
		Scripts  []dsl.Script
		Scenario *jmeter.ScenarioDraft
	}{scripts, scenario})
	if receipt, err := s.openBatchReceipt(r, id.Revision, project); err == nil {
		if receipt.Kind == "save" && receipt.Digest == digest {
			return nil
		}
		return errors.New("batch revision has already been replaced or saved")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	b, claim, err := s.claimJMeterBatch(r, id, project)
	if err != nil {
		return err
	}
	defer s.releaseImportCandidateClaim(claim)
	defer clearJMeterBatchSource(b)
	if _, err = s.openBatchReceipt(r, id.Revision, project); !errors.Is(err, os.ErrNotExist) {
		return errors.New("batch revision changed")
	}
	if len(scripts) != len(b.Scripts) || batchDigest(scenario) != batchDigest(b.Scenario) {
		return errors.New("batch membership or scenario differs from protected revision")
	}
	for i := range scripts {
		// Existing attestation validation checks executable content. Membership is
		// pinned independently here so a subset or reordered candidate cannot save.
		if scripts[i].ID != b.Scripts[i].ID || importDraftToken(&scripts[i]) != importDraftToken(b.Scripts[i]) {
			return fmt.Errorf("batch script %d is not the protected candidate", i)
		}
	}
	return s.publishBatchReceipt(r, b, claim, jmeterBatchReceipt{Kind: "save", Digest: digest})
}
func importDraftToken(s *dsl.Script) any {
	if s.Metadata == nil || s.Metadata.Import == nil {
		return nil
	}
	return s.Metadata.Import.Preserved[importDraftAttestationMetadataKey]
}

// Only the whole-batch save handler can consume protected batch children.
// This context capability is internal and cannot be supplied by a client.
type jmeterBatchSaveContextKey struct{}

func withJMeterBatchSave(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), jmeterBatchSaveContextKey{}, true))
}

type importBatchExpiryContextKey struct{}

func withImportBatchExpiry(r *http.Request, expiry time.Time) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), importBatchExpiryContextKey{}, expiry))
}

func clearJMeterBatchSource(b *jmeterBatchCandidate) {
	if b == nil {
		return
	}
	for _, doc := range b.Source {
		clear(doc.Data)
	}
	b.Source = nil
}

// Persist provenance, not an active candidate capability. Once the atomic save
// commits, ordinary library editing/copying must not require an expired parent.
func finalizeJMeterBatchScript(script *dsl.Script) {
	if id, ok := scriptBatch(script); ok {
		preserved := script.Metadata.Import.Preserved
		preserved[jmeterBatchOriginMetadata] = id
		delete(preserved, jmeterBatchMetadata)
	}
}
