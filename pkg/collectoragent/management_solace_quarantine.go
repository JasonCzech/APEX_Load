package collectoragent

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const solaceManagementQuarantineBytes = 256 << 10
const solaceManagementQuarantineRecords = 256
const solaceManagementQuarantineBudgetKey = "solace-session-quarantine"

// Provider sessions are distinct from local socket ownership. This intent must
// become durable before any Basic bytes; cookies never enter this document.
// Only confirmed own-session logout or an audited provider-session release
// permits removal. A local process/socket-stop audit is insufficient proof.
type solaceManagementSessionIntent struct {
	Origin              string    `json:"origin"`
	ProjectID           string    `json:"project_id"`
	SourceID            string    `json:"source_id"`
	SourceRevision      int64     `json:"source_revision"`
	CollectorInstanceID string    `json:"collector_instance_id"`
	PollID              string    `json:"poll_id"`
	IdentityHash        string    `json:"identity_hash"`
	CreatedAt           time.Time `json:"created_at"`
}
type solaceManagementQuarantineDocument struct {
	Version int                                      `json:"version"`
	Intents map[string]solaceManagementSessionIntent `json:"intents"`
}
type solaceManagementQuarantine struct {
	mu       sync.Mutex
	path     string
	budget   *collectorSpoolBudget
	intents  map[string]solaceManagementSessionIntent
	poisoned bool
}

func solaceManagementIntent(origin string, grant ManagementPollGrant) solaceManagementSessionIntent {
	return solaceManagementSessionIntent{Origin: origin, ProjectID: grant.ProjectID, SourceID: grant.SourceID, SourceRevision: grant.SourceRevision, CollectorInstanceID: grant.CollectorInstanceID, PollID: grant.PollID, IdentityHash: grant.IdentityHash, CreatedAt: time.Now().UTC()}
}
func (i solaceManagementSessionIntent) valid() bool {
	origin, _, _, err := canonicalSolaceOrigin(i.Origin)
	return err == nil && origin == i.Origin && managementLabel(i.ProjectID, 256) && managementLabel(i.SourceID, 256) && i.SourceRevision > 0 && managementLabel(i.CollectorInstanceID, 256) && managementLabel(i.PollID, 256) && managementHash(i.IdentityHash) && !i.CreatedAt.IsZero() && i.CreatedAt.Location() == time.UTC
}
func (i solaceManagementSessionIntent) same(other solaceManagementSessionIntent) bool {
	return i.Origin == other.Origin && i.ProjectID == other.ProjectID && i.SourceID == other.SourceID && i.SourceRevision == other.SourceRevision && i.CollectorInstanceID == other.CollectorInstanceID && i.PollID == other.PollID && i.IdentityHash == other.IdentityHash
}

// Caller holds the existing collector spool process lock. Run must reload this
// after acquiring that lock, even if a preceding New inspected configuration.
// A fresh observer/agent cannot substitute a new empty store for this file.
func openSolaceManagementQuarantine(path string, budget *collectorSpoolBudget) (*solaceManagementQuarantine, error) {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || budget == nil {
		return nil, &managementObservationError{reason: "permission_denied"}
	}
	q := &solaceManagementQuarantine{path: path, budget: budget, intents: map[string]solaceManagementSessionIntent{}}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return q, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > solaceManagementQuarantineBytes {
		return nil, &managementObservationError{reason: "invalid"}
	}
	file, err := openKafkaManagementTrustFile(path)
	if err != nil {
		return nil, &managementObservationError{reason: "invalid"}
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) || opened.Mode().Perm()&0077 != 0 {
		return nil, &managementObservationError{reason: "invalid"}
	}
	body, err := io.ReadAll(io.LimitReader(file, solaceManagementQuarantineBytes+1))
	if err != nil || len(body) > solaceManagementQuarantineBytes {
		return nil, &managementObservationError{reason: "invalid"}
	}
	defer clear(body)
	var document solaceManagementQuarantineDocument
	if strictManagementJSON(body, &document) != nil || document.Version != 1 || document.Intents == nil || len(document.Intents) > solaceManagementQuarantineRecords {
		return nil, &managementObservationError{reason: "invalid"}
	}
	for origin, intent := range document.Intents {
		if origin != intent.Origin || !intent.valid() {
			return nil, &managementObservationError{reason: "invalid"}
		}
	}
	if budget.restore(solaceManagementQuarantineBudgetKey, body, nil) != nil {
		return nil, &managementObservationError{reason: "budget_exhausted"}
	}
	q.intents = document.Intents
	return q, nil
}
func (q *solaceManagementQuarantine) blocked(origin string) bool {
	if q == nil {
		return true
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	_, blocked := q.intents[origin]
	return q.poisoned || blocked
}
func (q *solaceManagementQuarantine) persistLocked(intents map[string]solaceManagementSessionIntent) error {
	body, err := json.Marshal(solaceManagementQuarantineDocument{Version: 1, Intents: intents})
	if err != nil || len(body) > solaceManagementQuarantineBytes {
		return &managementObservationError{reason: "budget_exhausted"}
	}
	defer clear(body)
	if err = q.budget.persist(solaceManagementQuarantineBudgetKey, q.path, body, nil); err != nil {
		// Rename/fsync may have partially installed a document. Memory remains
		// conservative and all new authentication stops until explicit recovery.
		q.poisoned = true
		return &managementObservationError{reason: "invalid"}
	}
	return nil
}
func (q *solaceManagementQuarantine) begin(intent solaceManagementSessionIntent) error {
	if q == nil || !intent.valid() {
		return &managementObservationError{reason: "permission_denied"}
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.poisoned {
		return &managementObservationError{reason: "invalid"}
	}
	if _, blocked := q.intents[intent.Origin]; blocked {
		return &managementObservationError{reason: "not_observed"}
	}
	if len(q.intents) >= solaceManagementQuarantineRecords {
		return &managementBudgetError{}
	}
	q.intents[intent.Origin] = intent
	return q.persistLocked(q.intents)
}
func (q *solaceManagementQuarantine) clearAfterProviderProof(intent solaceManagementSessionIntent) error {
	if q == nil {
		return &managementObservationError{reason: "permission_denied"}
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	stored, ok := q.intents[intent.Origin]
	if !ok || !stored.same(intent) {
		return &managementObservationError{reason: "permission_denied"}
	}
	next := make(map[string]solaceManagementSessionIntent, len(q.intents)-1)
	for origin, entry := range q.intents {
		if origin != intent.Origin {
			next[origin] = entry
		}
	}
	if err := q.persistLocked(next); err != nil {
		return err
	}
	q.intents = next
	q.poisoned = false
	return nil
}

// This is an internal integration boundary for the future audited operator
// provider-session workflow. No ordinary New, timeout, local process stop or
// socket-close path may call it. The supplied identity must match the retained
// durable intent, and the caller must already have proved broker-session release.
func (q *solaceManagementQuarantine) auditedProviderSessionRelease(intent solaceManagementSessionIntent) error {
	if q == nil {
		return errors.New("provider-session quarantine is unavailable")
	}
	return q.clearAfterProviderProof(intent)
}
