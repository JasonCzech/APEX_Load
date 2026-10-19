package api

import "github.com/jasonczech/apexload/pkg/internal/importreview"

// Script persistence + draft replay: CRUD for stored scripts (store, get,
// list/filter, move, update, delete, usage) plus draft replay entry points and
// summary enrichment. Extracted from runner.go; methods hang off *TestRunner.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/contentmutation"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/refkey"
	"github.com/jasonczech/apexload/pkg/storage"
)

type createOnlyScriptPersistence interface {
	CreateScriptIfAbsent(script *dsl.Script) (bool, error)
	GetScript(id string) (*dsl.Script, error)
}

type conditionalScriptPersistence interface {
	UpdateScriptIfUnchanged(script *dsl.Script, expectedUpdatedAt time.Time) (bool, error)
}

type correlationRulesetFencedScriptPersistence interface {
	UpdateScriptIfUnchangedAndCorrelationRulesetCurrent(
		script *dsl.Script,
		expectedUpdatedAt time.Time,
		rulesetID string,
		expectedRulesetProjectID string,
		expectedRulesetUpdatedAt time.Time,
	) (bool, error)
}

type conditionalScriptDeletePersistence interface {
	DeleteScriptIfUnchanged(id string, expectedUpdatedAt time.Time) (bool, error)
}

var errScriptConcurrentChange = importreview.ErrScriptConcurrentChange
var errScriptCredentialBindingsRequired = importreview.ErrScriptCredentialBindingsRequired

func validatePersistentLibraryScript(script *dsl.Script) error {
	if isCIScriptSnapshot(script) {
		return fmt.Errorf("generated_by value %q is reserved for private CI snapshots", script.GeneratedBy)
	}
	return nil
}

// preparePersistentLibraryScript is the final defense shared by every durable
// library write.
//
// Imported scripts are a deliberate exception to the ordinary authoring
// projection: the importing operator already possessed the source credential,
// and import fidelity requires the durable execution copy to retain it. Read,
// export, federation, and source-control responses still pass through
// maskScriptSecrets. On a masked read-modify-write, reconciliation restores
// unchanged durable values from prior rather than asking the operator to enter
// them again.
var preparePersistentLibraryScript = importreview.PreparePersistentLibraryScript

// preparePersistentImportedLibraryScript is the only create-time entry point
// that may establish the credential-preserving import policy. Callers must
// already be inside an authenticated importer commit/session workflow; ordinary
// script JSON is never an attestation, even if a client forges the same metadata
// marker.
var preparePersistentImportedLibraryScript = importreview.PreparePersistentImportedLibraryScript

var preparePersistentLibraryScriptWithImportTrust = importreview.PreparePersistentLibraryScriptWithImportTrust

var ensureCredentialPreservingImportMetadata = importreview.EnsureCredentialPreservingImportMetadata

var isCredentialPreservingImport = importreview.IsCredentialPreservingImport

// validateStoredExecutionScript is for server-owned durable import/session
// snapshots. A trusted import may retain credential-bearing Java VM argv, but
// all ordinary DSL validation and every structural JVM option rule still
// applies.
var validateStoredExecutionScript = importreview.ValidateStoredExecutionScript

var markCredentialPreservingImportScript = importreview.MarkCredentialPreservingImportScript

func (tr *TestRunner) rejectActivePrivateCISnapshotMutationLocked(id string) error {
	if tr != nil && tr.privateCISnapshots[strings.TrimSpace(id)] > 0 {
		return fmt.Errorf("script %q is a private CI execution snapshot", strings.TrimSpace(id))
	}
	return nil
}

func (tr *TestRunner) durableScriptForMutationLocked(id string) (*dsl.Script, error) {
	if tr.scriptStore == nil {
		stored := tr.scripts[id]
		if stored == nil {
			return nil, nil
		}
		return cloneScriptDefinition(stored.Script), nil
	}
	reader, ok := tr.scriptStore.(interface {
		GetScript(string) (*dsl.Script, error)
	})
	if !ok {
		// Lightweight test/adapter stores implement the historical Store surface
		// only. Production SQLite/PostgreSQL implements the authoritative reader
		// and CAS interfaces below; retain legacy behavior for those adapters.
		stored := tr.scripts[id]
		if stored == nil {
			return nil, nil
		}
		return cloneScriptDefinition(stored.Script), nil
	}
	script, err := contentmutation.OutsideLock(&tr.mu, func() (*dsl.Script, error) { return reader.GetScript(id) })
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return script, nil
}

func durableScriptForMutation(store scriptPersistence, id string, fallback *dsl.Script) (*dsl.Script, error) {
	if store == nil {
		return cloneScriptDefinition(fallback), nil
	}
	reader, ok := store.(interface {
		GetScript(string) (*dsl.Script, error)
	})
	if !ok {
		return cloneScriptDefinition(fallback), nil
	}
	script, err := reader.GetScript(id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return script, nil
}

func sameStoredScriptMutationVersion(current, snapshot *StoredScript) bool {
	if current == nil || snapshot == nil {
		return current == nil && snapshot == nil
	}
	if current.Script == nil || snapshot.Script == nil {
		return current.Script == nil && snapshot.Script == nil && current.ID == snapshot.ID
	}
	return current.ID == snapshot.ID && current.RefKey == snapshot.RefKey &&
		normalizeProjectID(current.ProjectID) == normalizeProjectID(snapshot.ProjectID) &&
		current.Script.UpdatedAt.Equal(snapshot.Script.UpdatedAt)
}

func expectedScriptMutationProject(explicit string, cached *StoredScript) string {
	if strings.TrimSpace(explicit) != "" {
		return normalizeProjectID(explicit)
	}
	if cached != nil {
		return normalizeProjectID(cached.ProjectID)
	}
	return ""
}

func verifyScriptMutationOwnership(current *dsl.Script, expectedProjectID string) error {
	if current == nil {
		return nil
	}
	if expectedProjectID != "" && normalizeProjectID(current.ProjectID) != normalizeProjectID(expectedProjectID) {
		return fmt.Errorf("%w: durable script belongs to a different project", errScriptConcurrentChange)
	}
	return nil
}

func (tr *TestRunner) StoreScript(script *dsl.Script) *StoredScript {
	stored, _ := tr.StoreScriptDurable(script)
	return stored
}

func (tr *TestRunner) StoreScriptDurable(script *dsl.Script) (*StoredScript, error) {
	if script == nil {
		return nil, fmt.Errorf("script is required")
	}
	if err := engine.RetiredBrunoRuntimeError(script); err != nil {
		return nil, err
	}
	releaseReferenceMutation, err := tr.acquireSecretReferenceMutation(script.ProjectID)
	if err != nil {
		return nil, err
	}
	defer releaseReferenceMutation()
	return tr.storeScriptDurable(script, false)
}

func (tr *TestRunner) acquireSecretReferenceMutation(projectID string) (func(), error) {
	if tr == nil || tr.secretReferenceMutations == nil {
		return func() {}, nil
	}
	return tr.secretReferenceMutations.acquireDurable(projectID)
}

func (tr *TestRunner) scriptProjectIDForSecretReferenceMutation(id string) string {
	if tr == nil {
		return ""
	}
	tr.mu.RLock()
	id = tr.resolveRefKeyLocked(strings.TrimSpace(id))
	projectID := ""
	if stored := tr.scripts[id]; stored != nil {
		projectID = stored.ProjectID
	}
	tr.mu.RUnlock()
	return projectID
}

func (tr *TestRunner) prepareGitHubScriptImportLocked(script *dsl.Script) (*dsl.Script, *dsl.Script, error) {
	if script == nil || strings.TrimSpace(script.ID) == "" {
		return nil, nil, fmt.Errorf("github script import requires a deterministic script id")
	}
	if scriptRequiresJavaVuserAuthoringApproval(script) {
		return nil, nil, fmt.Errorf("Java Vuser scripts cannot be imported into executable storage without local authoring-session review and approval")
	}
	if err := validatePersistentLibraryScript(script); err != nil {
		return nil, nil, err
	}
	scriptID := strings.TrimSpace(script.ID)
	if err := tr.rejectActivePrivateCISnapshotMutationLocked(scriptID); err != nil {
		return nil, nil, err
	}
	current, err := tr.durableScriptForMutationLocked(scriptID)
	if err != nil {
		return nil, nil, err
	}
	if scriptRequiresJavaVuserAuthoringApproval(current) {
		return nil, nil, fmt.Errorf("Java Vuser scripts cannot be changed through GitHub import without local authoring-session review and approval")
	}
	projectID := normalizeProjectID(script.ProjectID)
	if current != nil && normalizeProjectID(current.ProjectID) != projectID {
		return nil, nil, fmt.Errorf("%w: imported script id belongs to another project", errScriptConcurrentChange)
	}
	if err := tr.projectDeletionErrorLocked(projectID); err != nil {
		return nil, nil, err
	}
	projected, err := preparePersistentLibraryScript(script, current)
	if err != nil {
		return nil, nil, err
	}
	projected.ID = scriptID
	projected.ProjectID = projectID
	if strings.TrimSpace(projected.Version) == "" {
		projected.Version = "1"
	}
	if current != nil {
		projected.RefKey = current.RefKey
		projected.FolderID = cloneStringPtr(current.FolderID)
		projected.CreatedAt = current.CreatedAt
	} else {
		// Reference keys and folders are workspace placement, not portable source
		// metadata. New GitHub imports start at the project root with a local key.
		projected.RefKey = ""
		projected.FolderID = nil
		projected.CreatedAt = time.Time{}
	}
	return projected, current, nil
}

// PreflightGitHubScriptImport validates governance and existing-ID ownership
// without mutating durable or cached state. Bulk import runs this for every
// remote JSON file before committing the first candidate.
func (tr *TestRunner) PreflightGitHubScriptImport(script *dsl.Script, target storage.GitHubScriptSyncTarget) error {
	tr.scriptMutationMu.Lock()
	defer tr.scriptMutationMu.Unlock()
	defer tr.flushContentReferenceFloors()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	_, current, err := tr.prepareGitHubScriptImportLocked(script)
	if err != nil || current == nil {
		return err
	}
	linkStore, ok := tr.scriptStore.(storage.GitHubScriptLinkStore)
	if !ok {
		return fmt.Errorf("durable script store does not support github link preflight")
	}
	link, err := contentmutation.OutsideLock(&tr.mu, func() (*storage.GitHubScriptLink, error) { return linkStore.GetGitHubScriptLink(current.ID) })
	if err != nil {
		return err
	}
	if !githubSyncLinkMatchesTarget(link, target.Owner, target.Repo, target.Branch, target.Path) ||
		(strings.TrimSpace(link.BaseBranch) != "" && strings.TrimSpace(link.BaseBranch) != strings.TrimSpace(target.Branch)) {
		return fmt.Errorf("%w: imported script id is unlinked or linked to another github target", errScriptConcurrentChange)
	}
	return nil
}

// StoreGitHubScriptImportDurable commits the projected script, GitHub link, and
// target ownership through the store's single atomic boundary, then hydrates
// the runner cache from the committed canonical script.
func (tr *TestRunner) StoreGitHubScriptImportDurable(
	script *dsl.Script,
	link *storage.GitHubScriptLink,
) (*StoredScript, storage.GitHubScriptImportOutcome, error) {
	if script == nil {
		return nil, storage.GitHubScriptImportConflict, fmt.Errorf("script is required")
	}
	if link == nil {
		return nil, storage.GitHubScriptImportConflict, fmt.Errorf("github script import link is required")
	}
	releaseReferenceMutation, err := tr.acquireSecretReferenceMutation(script.ProjectID)
	if err != nil {
		return nil, storage.GitHubScriptImportConflict, err
	}
	defer releaseReferenceMutation()
	tr.scriptMutationMu.Lock()
	defer tr.scriptMutationMu.Unlock()
	defer tr.flushContentReferenceFloors()
	releaseProjectMutation := tr.acquireProjectTestMutation(script.ProjectID)
	defer releaseProjectMutation()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	projected, current, err := tr.prepareGitHubScriptImportLocked(script)
	if err != nil {
		return nil, storage.GitHubScriptImportConflict, err
	}
	// Script versions are stored at microseconds, so a newer version must be
	// at least a microsecond later.
	now := time.Now().UTC().Truncate(time.Microsecond)
	var expectedUpdatedAt time.Time
	if current == nil {
		sequence, err := tr.nextScriptSeqLocked()
		if err != nil {
			return nil, storage.GitHubScriptImportConflict, fmt.Errorf("allocate script reference key: %w", err)
		}
		projected.RefKey = refkey.Script(tr.envCode, sequence, 1)
		projected.CreatedAt = now
		projected.UpdatedAt = now
	} else {
		expectedUpdatedAt = current.UpdatedAt
		currentFingerprint, err := storage.ImportSourceScriptFingerprint(current)
		if err != nil {
			return nil, storage.GitHubScriptImportConflict, err
		}
		desiredFingerprint, err := storage.ImportSourceScriptFingerprint(projected)
		if err != nil {
			return nil, storage.GitHubScriptImportConflict, err
		}
		if currentFingerprint == desiredFingerprint {
			projected.UpdatedAt = current.UpdatedAt
		} else {
			if !now.After(current.UpdatedAt) {
				now = current.UpdatedAt.Truncate(time.Microsecond).Add(time.Microsecond)
			}
			projected.UpdatedAt = now
		}
	}
	linkCopy := *link
	linkCopy.ScriptID = projected.ID
	linkCopy.RefKey = projected.RefKey
	_, contentHash, err := githubScriptEnvelope(projected)
	if err != nil {
		return nil, storage.GitHubScriptImportConflict, err
	}
	linkCopy.ContentHash = contentHash
	importStore, ok := tr.scriptStore.(storage.GitHubScriptImportStore)
	if !ok {
		return nil, storage.GitHubScriptImportConflict, fmt.Errorf("durable script store does not support atomic github imports")
	}
	outcome, err := contentmutation.OutsideLock(&tr.mu, func() (storage.GitHubScriptImportOutcome, error) {
		return importStore.CommitGitHubScriptImport(projected, &linkCopy, expectedUpdatedAt)
	})
	if err != nil || outcome == storage.GitHubScriptImportConflict {
		return nil, outcome, err
	}
	canonicalScript := cloneScriptDefinition(projected)
	createdAt := canonicalScript.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	stored := &StoredScript{
		ArchivedAt:      canonicalScript.ArchivedAt,
		ID:              canonicalScript.ID,
		RefKey:          canonicalScript.RefKey,
		Name:            canonicalScript.Name,
		Protocol:        canonicalScript.Protocol,
		Version:         canonicalScript.Version,
		ProjectID:       canonicalScript.ProjectID,
		FolderID:        cloneStringPtr(canonicalScript.FolderID),
		CreatedAt:       createdAt,
		AuthoringHealth: buildStoredScriptAuthoringHealth(canonicalScript),
		Script:          canonicalScript,
	}
	if previous := tr.scripts[stored.ID]; previous != nil {
		tr.removeRefKeyLocked(previous.RefKey)
	}
	tr.scripts[stored.ID] = stored
	tr.contentRevision++
	tr.observeAndIndexRefKeyLocked(stored.RefKey, stored.ID)
	*script = *cloneScriptDefinition(canonicalScript)
	*link = linkCopy
	return cloneStoredScriptForReadLocked(stored), outcome, nil
}

// storeScriptDurableForRestore is restricted to hydrating records that already
// existed before authoring-session governance was introduced. New external
// persistence paths must use StoreScriptDurable and cannot manufacture legacy
// full_lifecycle grandfathering.
func (tr *TestRunner) storeScriptDurableForRestore(script *dsl.Script) (*StoredScript, error) {
	if script == nil {
		return nil, fmt.Errorf("script is required")
	}
	if err := engine.RetiredBrunoRuntimeError(script); err != nil {
		return nil, err
	}
	releaseReferenceMutation, err := tr.acquireSecretReferenceMutation(script.ProjectID)
	if err != nil {
		return nil, err
	}
	defer releaseReferenceMutation()
	return tr.storeScriptDurable(script, true)
}

func (tr *TestRunner) storeScriptDurable(script *dsl.Script, restoringExistingJava bool) (*StoredScript, error) {
	if script == nil {
		return nil, fmt.Errorf("script is required")
	}
	if err := engine.RetiredBrunoRuntimeError(script); err != nil {
		return nil, err
	}
	if scriptRequiresJavaVuserAuthoringApproval(script) && !restoringExistingJava {
		return nil, fmt.Errorf("Java Vuser scripts must be saved through authoring-session review and approval")
	}
	if err := validatePersistentLibraryScript(script); err != nil {
		return nil, err
	}
	tr.scriptMutationMu.Lock()
	defer tr.scriptMutationMu.Unlock()
	defer tr.flushContentReferenceFloors()

	scriptID := strings.TrimSpace(script.ID)
	if scriptID == "" {
		scriptID = uuid.New().String()
	}
	tr.mu.RLock()
	if err := tr.rejectActivePrivateCISnapshotMutationLocked(scriptID); err != nil {
		tr.mu.RUnlock()
		return nil, err
	}
	if err := tr.projectDeletionErrorLocked(normalizeProjectID(script.ProjectID)); err != nil {
		tr.mu.RUnlock()
		return nil, err
	}
	existing := cloneStoredScriptForReadLocked(tr.scripts[scriptID])
	scriptStore := tr.scriptStore
	envCode := tr.envCode
	tr.mu.RUnlock()

	var cachedScript *dsl.Script
	if existing != nil {
		cachedScript = existing.Script
	}
	current, err := durableScriptForMutation(scriptStore, scriptID, cachedScript)
	if err != nil {
		return nil, err
	}
	if scriptRequiresJavaVuserAuthoringApproval(current) && !restoringExistingJava {
		return nil, fmt.Errorf("Java Vuser scripts must be changed through local authoring-session review and approval")
	}
	var prior *dsl.Script
	if existing != nil {
		prior = existing.Script
	}
	projected, err := preparePersistentLibraryScript(script, prior)
	if err != nil {
		return nil, err
	}
	script = projected
	script.ID = scriptID
	script.ProjectID = normalizeProjectID(script.ProjectID)
	if existing != nil && normalizeProjectID(existing.ProjectID) != script.ProjectID {
		return nil, fmt.Errorf("script %s belongs to project %s and cannot be overwritten from project %s", script.ID, normalizeProjectID(existing.ProjectID), script.ProjectID)
	}
	releaseProjectMutation := tr.acquireProjectTestMutation(script.ProjectID)
	defer releaseProjectMutation()
	tr.mu.RLock()
	if err := tr.projectDeletionErrorLocked(script.ProjectID); err != nil {
		tr.mu.RUnlock()
		return nil, err
	}
	if err := tr.rejectActivePrivateCISnapshotMutationLocked(scriptID); err != nil {
		tr.mu.RUnlock()
		return nil, err
	}
	tr.mu.RUnlock()
	if strings.TrimSpace(script.Version) == "" {
		script.Version = "1"
	}
	if existing != nil && strings.TrimSpace(script.RefKey) == "" {
		script.RefKey = existing.RefKey
	}
	if existing == nil && current == nil && strings.TrimSpace(script.RefKey) != "" {
		// A new script keeps a key it brings only while no other script holds it.
		tr.mu.RLock()
		held := tr.scriptRefKeyHeldElsewhereLocked(script.RefKey, scriptID)
		tr.mu.RUnlock()
		if held {
			script.RefKey = ""
		}
	}
	if script.RefKey == "" {
		sequence, err := tr.nextScriptSeqOutsideRunnerLock()
		if err != nil {
			return nil, fmt.Errorf("allocate script reference key: %w", err)
		}
		script.RefKey = refkey.Script(envCode, sequence, 1)
	}

	// Scripts are stored at microseconds, and the cached copy's CreatedAt must
	// equal the stored one.
	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	if existing != nil && !existing.CreatedAt.IsZero() {
		createdAt = existing.CreatedAt
	}
	if script.CreatedAt.IsZero() {
		script.CreatedAt = createdAt
	}
	if scriptStore == nil {
		now := time.Now().UTC()
		if existing != nil && existing.Script != nil && !now.After(existing.Script.UpdatedAt) {
			now = existing.Script.UpdatedAt.Add(time.Nanosecond)
		}
		script.UpdatedAt = now
	}
	stored := &StoredScript{
		ID:              script.ID,
		RefKey:          script.RefKey,
		Name:            script.Name,
		Protocol:        script.Protocol,
		Version:         script.Version,
		ProjectID:       script.ProjectID,
		FolderID:        script.FolderID,
		CreatedAt:       createdAt,
		AuthoringHealth: buildStoredScriptAuthoringHealth(script),
		Script:          script,
	}

	if scriptStore != nil {
		_, supportsCreateOnly := scriptStore.(createOnlyScriptPersistence)
		_, supportsCAS := scriptStore.(conditionalScriptPersistence)
		if !supportsCreateOnly || !supportsCAS {
			if err := tr.saveStoredScriptWithImportTrust(script, prior, false, ""); err != nil {
				return nil, err
			}
		} else {
			if current == nil {
				store, ok := scriptStore.(createOnlyScriptPersistence)
				if !ok {
					return nil, fmt.Errorf("durable script store does not support atomic create-only persistence")
				}
				created, err := store.CreateScriptIfAbsent(script)
				if errors.Is(err, storage.ErrScriptRefKeyTaken) {
					sequence, seqErr := tr.nextScriptSeqOutsideRunnerLock()
					if seqErr != nil {
						return nil, fmt.Errorf("allocate script reference key: %w", seqErr)
					}
					script.RefKey = refkey.Script(envCode, sequence, 1)
					stored.RefKey = script.RefKey
					created, err = store.CreateScriptIfAbsent(script)
				}
				if err != nil {
					return nil, err
				}
				if !created {
					return nil, errScriptConcurrentChange
				}
			} else {
				if existing == nil || existing.Script == nil ||
					normalizeProjectID(current.ProjectID) != script.ProjectID ||
					normalizeProjectID(existing.ProjectID) != script.ProjectID ||
					!current.UpdatedAt.Equal(existing.Script.UpdatedAt) {
					return nil, errScriptConcurrentChange
				}
				projected, err := preparePersistentLibraryScript(script, current)
				if err != nil {
					return nil, err
				}
				script = projected
				script.CreatedAt = current.CreatedAt
				store, ok := scriptStore.(conditionalScriptPersistence)
				if !ok {
					return nil, fmt.Errorf("durable script store does not support conditional updates")
				}
				updated, err := store.UpdateScriptIfUnchanged(script, current.UpdatedAt)
				if err != nil {
					return nil, err
				}
				if !updated {
					return nil, errScriptConcurrentChange
				}
			}
		}
	}
	canonicalScript := cloneScriptDefinition(script)

	tr.mu.Lock()
	defer tr.mu.Unlock()
	if err := tr.projectDeletionErrorLocked(script.ProjectID); err != nil {
		return nil, err
	}
	if err := tr.rejectActivePrivateCISnapshotMutationLocked(scriptID); err != nil {
		return nil, err
	}
	if scriptStore == nil && !sameStoredScriptMutationVersion(tr.scripts[scriptID], existing) {
		return nil, errScriptConcurrentChange
	}
	if previous := tr.scripts[stored.ID]; previous != nil {
		tr.removeRefKeyLocked(previous.RefKey)
	}
	stored.FolderID = cloneStringPtr(stored.FolderID)
	stored.Script = canonicalScript
	tr.scripts[stored.ID] = stored
	tr.contentRevision++
	tr.observeAndIndexRefKeyLocked(stored.RefKey, stored.ID)
	return cloneStoredScriptForReadLocked(stored), nil
}

// CreateScriptDurable atomically creates a library script and never overwrites
// an existing durable ID. It is idempotent for an identical prior create, which
// lets an interrupted authoring approval resume safely across HA replicas.
func (tr *TestRunner) CreateScriptDurable(script *dsl.Script) (*StoredScript, error) {
	stored, _, err := tr.createScriptDurable(script, false, false, "")
	return stored, err
}

// CreateScriptDurableReportingReplay behaves like CreateScriptDurable and also
// reports whether this call created the durable row or found an equivalent one
// already present.
//
// Create-only persistence is already idempotent: a caller that supplies a
// deterministic script ID (see protectedImportCreateScriptID) and retries after
// an interrupted commit gets the existing row back rather than an error. Only
// the outcome was invisible, so a retrying client could not distinguish "I
// created this" from "this was already committed" and had to assume the former.
func (tr *TestRunner) CreateScriptDurableReportingReplay(script *dsl.Script) (*StoredScript, bool, error) {
	return tr.createScriptDurable(script, false, false, "")
}

// CreateImportedScriptDurableReportingReplay is restricted to authenticated
// importer commit workflows. It establishes the server-owned preservation
// marker and retains imported executable credential values.
func (tr *TestRunner) CreateImportedScriptDurableReportingReplay(
	script *dsl.Script,
	sourceProfile string,
) (*StoredScript, bool, error) {
	if strings.TrimSpace(sourceProfile) == "" {
		return nil, false, fmt.Errorf("import source profile is required")
	}
	return tr.createScriptDurable(script, false, true, sourceProfile)
}

func (tr *TestRunner) createAuthoringApprovedScriptDurable(script *dsl.Script) (*StoredScript, error) {
	if scriptRequiresJavaVuserAuthoringApproval(script) {
		if _, ok := readJavaVuserAuthoringApproval(script); !ok {
			return nil, fmt.Errorf("Java Vuser authoring approval evidence is missing")
		}
	}
	trustedImport := isCredentialPreservingImport(script)
	sourceProfile := ""
	if trustedImport && script.Metadata != nil && script.Metadata.Import != nil {
		sourceProfile = script.Metadata.Import.Source
	}
	stored, _, err := tr.createScriptDurable(
		script,
		true,
		trustedImport,
		sourceProfile,
	)
	return stored, err
}

func (tr *TestRunner) createScriptDurable(
	script *dsl.Script,
	authoringApprovedJava bool,
	trustedImport bool,
	sourceProfile string,
	ordinaryCreates ...bool,
) (*StoredScript, bool, error) {
	created := true
	ordinaryCreate := len(ordinaryCreates) > 0 && ordinaryCreates[0]
	if script == nil {
		return nil, false, fmt.Errorf("script is required")
	}
	releaseReferenceMutation, err := tr.acquireSecretReferenceMutation(script.ProjectID)
	if err != nil {
		return nil, false, err
	}
	defer releaseReferenceMutation()
	if scriptRequiresJavaVuserAuthoringApproval(script) && !authoringApprovedJava {
		return nil, false, fmt.Errorf("Java Vuser scripts must be saved through authoring-session review and approval")
	}
	if err := validatePersistentLibraryScript(script); err != nil {
		return nil, false, err
	}
	tr.scriptMutationMu.Lock()
	defer tr.scriptMutationMu.Unlock()
	defer tr.flushContentReferenceFloors()
	releaseProjectMutation := tr.acquireProjectTestMutation(script.ProjectID)
	defer releaseProjectMutation()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	scriptID := strings.TrimSpace(script.ID)
	if scriptID == "" {
		scriptID = uuid.New().String()
	}
	if err := tr.rejectActivePrivateCISnapshotMutationLocked(scriptID); err != nil {
		return nil, false, err
	}
	var projected *dsl.Script
	if trustedImport {
		projected, err = preparePersistentImportedLibraryScript(script, nil, sourceProfile)
	} else {
		projected, err = preparePersistentLibraryScript(script, nil)
	}
	if err != nil {
		return nil, false, err
	}
	script = projected
	script.ID = scriptID
	script.ProjectID = normalizeProjectID(script.ProjectID)
	if err := tr.projectDeletionErrorLocked(script.ProjectID); err != nil {
		return nil, false, err
	}
	if strings.TrimSpace(script.Version) == "" {
		script.Version = "1"
	}
	if tr.scriptStore == nil {
		now := time.Now().UTC()
		if script.CreatedAt.IsZero() {
			script.CreatedAt = now
		}
		script.UpdatedAt = now
	}
	persisted := script
	var authored *StoredScript
	if store, supported := tr.scriptStore.(storage.ScriptAuthoringStore); supported && ordinaryCreate {
		if err := tr.prepareNewScriptRefKeyLocked(script); err != nil {
			return nil, false, err
		}
		mutation, err := contentmutation.OutsideLock(&tr.mu, func() (*storage.ScriptAuthoringMutationResult, error) {
			return store.CreateScriptAuthoringIfAbsent(script)
		})
		if errors.Is(err, storage.ErrScriptRefKeyTaken) {
			// Another replica took the key since this one indexed its scripts.
			if err := tr.assignNextScriptRefKeyLocked(script); err != nil {
				return nil, false, err
			}
			mutation, err = contentmutation.OutsideLock(&tr.mu, func() (*storage.ScriptAuthoringMutationResult, error) {
				return store.CreateScriptAuthoringIfAbsent(script)
			})
		}
		if err != nil {
			return nil, false, err
		}
		if mutation == nil || mutation.Snapshot == nil || mutation.Snapshot.Script == nil {
			return nil, false, storage.ErrScriptAuthoringUnavailable
		}
		if !mutation.Applied {
			if normalizeProjectID(mutation.Snapshot.Script.ProjectID) != script.ProjectID || isCIScriptSnapshot(mutation.Snapshot.Script) {
				return nil, false, storage.ErrScriptAuthoringNotFound
			}
			return nil, false, errScriptAuthoringAlreadyExists
		}
		authored, err = storedScriptFromAuthoringSnapshot(mutation.Snapshot)
		if err != nil {
			return nil, false, err
		}
		persisted = cloneScriptDefinition(authored.Script)
	} else if store, ok := tr.scriptStore.(createOnlyScriptPersistence); ok {
		// Shared storage is authoritative. A replica can have an equivalent but
		// stale cached script after another replica edited or replaced the durable
		// row, so never let the cache bypass the create-only ownership fence.
		if err := tr.prepareNewScriptRefKeyLocked(script); err != nil {
			return nil, false, err
		}
		created, err = contentmutation.OutsideLock(&tr.mu, func() (bool, error) { return store.CreateScriptIfAbsent(script) })
		if errors.Is(err, storage.ErrScriptRefKeyTaken) {
			if err := tr.assignNextScriptRefKeyLocked(script); err != nil {
				return nil, false, err
			}
			created, err = contentmutation.OutsideLock(&tr.mu, func() (bool, error) { return store.CreateScriptIfAbsent(script) })
		}
		if err != nil {
			return nil, false, err
		}
		if !created {
			existing, err := contentmutation.OutsideLock(&tr.mu, func() (*dsl.Script, error) { return store.GetScript(script.ID) })
			if err != nil {
				return nil, false, fmt.Errorf("load conflicting script %s: %w", script.ID, err)
			}
			if ordinaryCreate {
				if normalizeProjectID(existing.ProjectID) != script.ProjectID || isCIScriptSnapshot(existing) {
					return nil, false, storage.ErrScriptAuthoringNotFound
				}
				return nil, false, errScriptAuthoringAlreadyExists
			}
			if normalizeProjectID(existing.ProjectID) != script.ProjectID || !equivalentCreateOnlyScript(existing, script) {
				return nil, false, fmt.Errorf("%w: script %s already exists and is not owned by this approval", errScriptConcurrentChange, script.ID)
			}
			persisted = existing
			created = false
		}
	} else if tr.scriptStore != nil {
		return nil, false, fmt.Errorf("durable script store does not support atomic create-only persistence")
	} else {
		if existing := tr.scripts[script.ID]; existing != nil {
			if ordinaryCreate {
				if normalizeProjectID(existing.ProjectID) != script.ProjectID || isStoredCIScriptSnapshot(existing) {
					return nil, false, storage.ErrScriptAuthoringNotFound
				}
				return nil, false, errScriptAuthoringAlreadyExists
			}
			if normalizeProjectID(existing.ProjectID) != script.ProjectID || !equivalentCreateOnlyScript(existing.Script, script) {
				return nil, false, fmt.Errorf("%w: script %s already exists and is not owned by this approval", errScriptConcurrentChange, script.ID)
			}
			return cloneStoredScriptForReadLocked(existing), false, nil
		}
		if err := tr.prepareNewScriptRefKeyLocked(script); err != nil {
			return nil, false, err
		}
		if err := contentmutation.WriteOutsideLock(&tr.mu, func() error { return tr.saveStoredScriptWithImportTrust(script, nil, trustedImport, sourceProfile) }); err != nil {
			return nil, false, err
		}
	}

	createdAt := persisted.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC().Truncate(time.Microsecond)
	}
	stored := &StoredScript{
		ID:              persisted.ID,
		RefKey:          persisted.RefKey,
		Name:            persisted.Name,
		Protocol:        persisted.Protocol,
		Version:         persisted.Version,
		ProjectID:       persisted.ProjectID,
		FolderID:        cloneStringPtr(persisted.FolderID),
		CreatedAt:       createdAt,
		AuthoringHealth: buildStoredScriptAuthoringHealth(persisted),
		Script:          cloneScriptDefinition(persisted),
	}
	if previous := tr.scripts[stored.ID]; previous != nil {
		tr.removeRefKeyLocked(previous.RefKey)
	}
	tr.scripts[stored.ID] = stored
	tr.contentRevision++
	tr.observeAndIndexRefKeyLocked(stored.RefKey, stored.ID)
	if authored != nil {
		return authored, created, nil
	}
	return cloneStoredScriptForReadLocked(stored), created, nil
}

// UpdateScriptDurableIfUnchanged applies a synchronized library update only if
// the exact durable script observed during conflict detection still exists.
// This closes the preflight-to-write race with concurrent editors and replicas.
func (tr *TestRunner) UpdateScriptDurableIfUnchanged(script, expected *dsl.Script) (*StoredScript, bool, error) {
	if script == nil || expected == nil {
		return nil, false, fmt.Errorf("script and expected durable version are required")
	}
	if scriptRequiresJavaVuserAuthoringApproval(script) || scriptRequiresJavaVuserAuthoringApproval(expected) {
		return nil, false, fmt.Errorf("Java Vuser scripts cannot be synchronized into executable storage without local authoring-session review and approval")
	}
	if err := validatePersistentLibraryScript(script); err != nil {
		return nil, false, err
	}
	releaseReferenceMutation, err := tr.acquireSecretReferenceMutation(expected.ProjectID)
	if err != nil {
		return nil, false, err
	}
	defer releaseReferenceMutation()
	tr.scriptMutationMu.Lock()
	defer tr.scriptMutationMu.Unlock()
	defer tr.flushContentReferenceFloors()
	releaseProjectMutation := tr.acquireProjectTestMutation(expected.ProjectID)
	defer releaseProjectMutation()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if strings.TrimSpace(script.ID) == "" || script.ID != expected.ID {
		return nil, false, fmt.Errorf("conditional script update requires a matching id")
	}
	if err := tr.rejectActivePrivateCISnapshotMutationLocked(script.ID); err != nil {
		return nil, false, err
	}
	current, err := tr.durableScriptForMutationLocked(script.ID)
	if err != nil {
		return nil, false, err
	}
	if scriptRequiresJavaVuserAuthoringApproval(current) {
		return nil, false, fmt.Errorf("Java Vuser scripts cannot be synchronized into executable storage without local authoring-session review and approval")
	}
	script.ProjectID = normalizeProjectID(script.ProjectID)
	if script.ProjectID != normalizeProjectID(expected.ProjectID) {
		return nil, false, fmt.Errorf("script %s cannot move between projects during synchronization", script.ID)
	}
	if err := tr.projectDeletionErrorLocked(script.ProjectID); err != nil {
		return nil, false, err
	}
	projected, err := preparePersistentLibraryScript(script, expected)
	if err != nil {
		return nil, false, err
	}
	script = projected
	if strings.TrimSpace(script.Version) == "" {
		script.Version = "1"
	}
	// Reference keys, folders, and creation timestamps belong to the local
	// workspace and are never imported from the remote source on update.
	script.RefKey = expected.RefKey
	script.FolderID = cloneStringPtr(expected.FolderID)
	script.CreatedAt = expected.CreatedAt

	if store, ok := tr.scriptStore.(conditionalScriptPersistence); ok {
		updated, err := contentmutation.OutsideLock(&tr.mu, func() (bool, error) { return store.UpdateScriptIfUnchanged(script, expected.UpdatedAt) })
		if err != nil || !updated {
			return nil, updated, err
		}
	} else if tr.scriptStore != nil {
		return nil, false, fmt.Errorf("durable script store does not support conditional updates")
	} else {
		current := tr.scripts[script.ID]
		if current == nil || current.Script == nil || !equivalentCreateOnlyScript(current.Script, expected) {
			return nil, false, nil
		}
		if err := contentmutation.WriteOutsideLock(&tr.mu, func() error { return tr.saveStoredScriptWithImportTrust(script, expected, false, "") }); err != nil {
			return nil, false, err
		}
	}

	createdAt := script.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	stored := &StoredScript{
		ID:              script.ID,
		RefKey:          script.RefKey,
		Name:            script.Name,
		Protocol:        script.Protocol,
		Version:         script.Version,
		ProjectID:       script.ProjectID,
		FolderID:        cloneStringPtr(script.FolderID),
		CreatedAt:       createdAt,
		AuthoringHealth: buildStoredScriptAuthoringHealth(script),
		Script:          cloneScriptDefinition(script),
	}
	if previous := tr.scripts[stored.ID]; previous != nil {
		tr.removeRefKeyLocked(previous.RefKey)
	}
	tr.scripts[stored.ID] = stored
	tr.contentRevision++
	tr.observeAndIndexRefKeyLocked(stored.RefKey, stored.ID)
	return cloneStoredScriptForReadLocked(stored), true, nil
}

// DeleteScriptDurableIfUnchanged removes a just-created library import only
// while its durable version still matches the import journal. A concurrent
// editor therefore wins over rollback instead of having their update erased.
func (tr *TestRunner) DeleteScriptDurableIfUnchanged(id string, expectedUpdatedAt time.Time) (bool, error) {
	releaseReferenceMutation, err := tr.acquireSecretReferenceMutation(tr.scriptProjectIDForSecretReferenceMutation(id))
	if err != nil {
		return false, err
	}
	defer releaseReferenceMutation()
	tr.scriptMutationMu.Lock()
	defer tr.scriptMutationMu.Unlock()
	defer tr.flushContentReferenceFloors()
	releaseProjectMutation := tr.acquireProjectTestMutation(tr.scriptProjectIDForSecretReferenceMutation(id))
	defer releaseProjectMutation()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	id = strings.TrimSpace(id)
	if id == "" || expectedUpdatedAt.IsZero() {
		return false, fmt.Errorf("script id and expected updated_at are required")
	}
	if err := tr.rejectActivePrivateCISnapshotMutationLocked(id); err != nil {
		return false, err
	}
	if store, ok := tr.scriptStore.(conditionalScriptDeletePersistence); ok {
		deleted, err := contentmutation.OutsideLock(&tr.mu, func() (bool, error) { return store.DeleteScriptIfUnchanged(id, expectedUpdatedAt) })
		if err != nil || !deleted {
			return deleted, err
		}
	} else if tr.scriptStore != nil {
		return false, fmt.Errorf("durable script store does not support conditional delete")
	} else {
		current := tr.scripts[id]
		if current == nil || current.Script == nil || !current.Script.UpdatedAt.Equal(expectedUpdatedAt) {
			return false, nil
		}
		if err := contentmutation.WriteOutsideLock(&tr.mu, func() error { return tr.deleteStoredScript(id) }); err != nil {
			return false, err
		}
	}
	if previous := tr.scripts[id]; previous != nil {
		tr.removeRefKeyLocked(previous.RefKey)
	}
	delete(tr.scripts, id)
	tr.contentRevision++
	return true, nil
}

func equivalentCreateOnlyScript(existing, candidate *dsl.Script) bool {
	if existing == nil || candidate == nil {
		return false
	}
	left, right := *existing, *candidate
	left.ID, right.ID = "", ""
	left.RefKey, right.RefKey = "", ""
	left.CreatedAt, right.CreatedAt = time.Time{}, time.Time{}
	left.UpdatedAt, right.UpdatedAt = time.Time{}, time.Time{}
	// An import save stamps the digest of what it writes; a draft never
	// carries it, so it is not part of what makes two scripts the same.
	left.Metadata = withoutImportedContentDigest(left.Metadata)
	right.Metadata = withoutImportedContentDigest(right.Metadata)
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}

func (tr *TestRunner) GetScript(id string) (*StoredScript, bool) {
	tr.refreshScriptsForHARead()
	tr.mu.RLock()
	id = tr.resolveRefKeyLocked(id)
	s, ok := tr.scripts[id]
	if ok && isStoredCIScriptSnapshot(s) {
		tr.mu.RUnlock()
		return nil, false
	}
	snapshot := cloneStoredScriptForReadLocked(s)
	if snapshot != nil && snapshot.Script != nil {
		snapshot.ArchivedAt = snapshot.Script.ArchivedAt
	}
	tr.mu.RUnlock()
	if !ok {
		return nil, false
	}
	return tr.publishedScriptSnapshot(snapshot)
}

func (tr *TestRunner) refreshScriptsForHARead(force ...bool) {
	if tr == nil || tr.leadership == nil {
		return
	}
	// A forced refresh drops the staleness window, so a sequential caller always
	// issues its own ListScripts scan and observes a mutation another replica
	// committed before it arrived. It is not a promise of a caller-fresh
	// snapshot: the shared coalescer still collapses a concurrent burst onto one
	// scan rather than running the full library scan per caller, and a forced
	// caller that parks on an in-flight scan accepts that scan's result even
	// though it queried before the caller arrived. Read-after-write paths that
	// need a strictly newer snapshot use GetScriptDurable, which queries the
	// store directly.
	window := haTestRefreshInterval
	if len(force) > 0 && force[0] {
		window = 0
	}
	if err := tr.haScriptRefresh.refresh(window, tr.reloadStoredScripts); err != nil {
		slog.Warn("failed to refresh scripts for HA read", "error", err)
	}
}

// GetScriptDurable refreshes from shared storage when available. Approval
// retries use it to return an already-promoted script without writing the old
// session draft back over edits made after approval on another HA replica.
func (tr *TestRunner) GetScriptDurable(id string) (*StoredScript, bool, error) {
	stored, ok, err := tr.getScriptDurableIncludingPending(id)
	if err != nil || !ok {
		return stored, ok, err
	}
	if _, visible := tr.publishedScriptSnapshot(stored); !visible {
		return nil, false, sql.ErrNoRows
	}
	return stored, true, nil
}

// getScriptDurableIncludingPending is reserved for import recovery. Public
// durable reads use GetScriptDurable, which applies the batch publication
// fence after loading the row.
func (tr *TestRunner) getScriptDurableIncludingPending(id string) (*StoredScript, bool, error) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	id = tr.resolveRefKeyLocked(id)
	revision, observation := tr.contentRevision, tr.scriptMutationMu.Observe()
	if store, ok := tr.scriptStore.(createOnlyScriptPersistence); ok {
		script, err := contentmutation.OutsideLock(&tr.mu, func() (*dsl.Script, error) { return store.GetScript(id) })
		if err != nil {
			return nil, false, err
		}
		if isCIScriptSnapshot(script) {
			return nil, false, nil
		}
		createdAt := script.CreatedAt
		if createdAt.IsZero() {
			createdAt = time.Now()
		}
		stored := &StoredScript{
			ID: script.ID, RefKey: script.RefKey, Name: script.Name, Protocol: script.Protocol,
			Version: script.Version, ProjectID: script.ProjectID, FolderID: cloneStringPtr(script.FolderID),
			ArchivedAt: cloneTimePtr(script.ArchivedAt),
			CreatedAt:  createdAt, AuthoringHealth: buildStoredScriptAuthoringHealth(script), Script: cloneScriptDefinition(script),
		}
		if tr.contentRevision != revision || !tr.scriptMutationMu.CanPublish(observation) || tr.projectDeletionErrorLocked(stored.ProjectID) != nil {
			return cloneStoredScriptForReadLocked(stored), true, nil
		}
		if previous := tr.scripts[id]; previous != nil {
			tr.removeRefKeyLocked(previous.RefKey)
		}
		tr.scripts[id] = stored
		tr.contentRevision++
		tr.observeAndIndexRefKeyLocked(stored.RefKey, stored.ID)
		return cloneStoredScriptForReadLocked(stored), true, nil
	}
	stored, ok := tr.scripts[id]
	if ok && isStoredCIScriptSnapshot(stored) {
		return nil, false, nil
	}
	return cloneStoredScriptForReadLocked(stored), ok, nil
}

func (tr *TestRunner) ReplayScript(id string, opts engine.ReplayOptions) (*engine.ReplayReport, error) {
	tr.mu.RLock()
	id = tr.resolveRefKeyLocked(id)
	stored, ok := tr.scripts[id]
	if !ok {
		tr.mu.RUnlock()
		return nil, fmt.Errorf("script not found")
	}
	script := cloneScriptDefinition(stored.Script)
	tr.mu.RUnlock()
	if _, visible := tr.publishedScriptSnapshot(&StoredScript{ID: id, Script: script}); !visible {
		return nil, fmt.Errorf("script not found")
	}
	return tr.replayScriptDefinitionContext(
		context.Background(),
		script,
		opts,
		isCredentialPreservingImport(script),
	)
}

// errRedactionPolicyUnavailable refuses a replay whose project redaction
// policy cannot be read: a storage failure the client can retry, not a fault
// in the script.
var errRedactionPolicyUnavailable = errors.New("project redaction policy is unavailable")

func (tr *TestRunner) ReplayScriptDefinition(script *dsl.Script, opts engine.ReplayOptions) (*engine.ReplayReport, error) {
	return tr.ReplayScriptDefinitionContext(context.Background(), script, opts)
}

func (tr *TestRunner) ReplayScriptDefinitionContext(ctx context.Context, script *dsl.Script, opts engine.ReplayOptions) (*engine.ReplayReport, error) {
	return tr.replayScriptDefinitionContext(ctx, script, opts, false)
}

func (tr *TestRunner) replayScriptDefinitionContext(
	ctx context.Context,
	script *dsl.Script,
	opts engine.ReplayOptions,
	trustedPreservedImport bool,
) (*engine.ReplayReport, error) {
	if script == nil {
		return nil, fmt.Errorf("script is required")
	}
	if err := engine.RetiredBrunoRuntimeError(script); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	issues := dsl.ValidateScriptIssues(script)
	if trustedPreservedImport {
		issues = validateStoredExecutionScript(script)
	}
	if len(issues) > 0 {
		return nil, fmt.Errorf("script is invalid: %s", strings.Join(issues, "; "))
	}
	if err := replayReviewGateError(script); err != nil {
		return nil, err
	}
	effectiveRedactionRules, err := tr.resolveEffectiveRedactionRules(script.ProjectID, opts.RedactionRules)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errRedactionPolicyUnavailable, err)
	}
	opts.RedactionRules = effectiveRedactionRules
	if opts.SecretResolver == nil {
		opts.SecretResolver = tr.secretResolverForScript(script)
	}
	return engine.ReplayScript(ctx, script, opts)
}

func (tr *TestRunner) ListScripts() []*StoredScript {
	tr.refreshScriptsForHARead()
	tr.mu.RLock()
	out := make([]*StoredScript, 0, len(tr.scripts))
	for _, s := range tr.scripts {
		if isStoredCIScriptSnapshot(s) {
			continue
		}
		out = append(out, cloneStoredScriptForReadLocked(s))
	}
	tr.mu.RUnlock()
	return tr.filterPublishedScripts(out)
}

func (tr *TestRunner) ListScriptsFiltered(projectID string, folderID *string) []*StoredScript {
	tr.refreshScriptsForHARead()
	projectID = normalizeProjectID(projectID)
	tr.mu.RLock()
	out := make([]*StoredScript, 0, len(tr.scripts))
	for _, s := range tr.scripts {
		if isStoredCIScriptSnapshot(s) {
			continue
		}
		if s.ProjectID != projectID {
			continue
		}
		if folderID != nil {
			if *folderID == "" {
				if s.FolderID != nil && *s.FolderID != "" {
					continue
				}
			} else if s.FolderID == nil || *s.FolderID != *folderID {
				continue
			}
		}
		out = append(out, tr.enrichScriptSummaryLocked(s))
	}
	tr.mu.RUnlock()
	return tr.filterPublishedScriptsScoped(out, projectID, folderID)
}

func (tr *TestRunner) MoveScript(id string, folderID *string) bool {
	moved, _ := tr.MoveScriptDurable(id, folderID)
	return moved
}

func (tr *TestRunner) MoveScriptDurable(id string, folderID *string) (bool, error) {
	return tr.MoveScriptDurableInProject(id, folderID, "")
}

// MoveScriptDurableInProject binds the mutation to the project scope resolved
// by request authorization. The durable version is then updated with CAS, so a
// delete/recreate or edit on another HA replica cannot be moved by a request
// authorized against an older row with the same ID.
func (tr *TestRunner) MoveScriptDurableInProject(id string, folderID *string, expectedProjectID string) (bool, error) {
	tr.scriptMutationMu.Lock()
	defer tr.scriptMutationMu.Unlock()
	defer tr.flushContentReferenceFloors()
	releaseProjectMutation := tr.acquireProjectTestMutation(tr.scriptProjectIDForSecretReferenceMutation(id))
	defer releaseProjectMutation()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	id = tr.resolveRefKeyLocked(id)
	if err := tr.rejectActivePrivateCISnapshotMutationLocked(id); err != nil {
		return false, err
	}
	cached := cloneStoredScriptForReadLocked(tr.scripts[id])
	if cached == nil {
		return false, nil
	}
	expectedProjectID = expectedScriptMutationProject(expectedProjectID, cached)
	current, err := tr.durableScriptForMutationLocked(id)
	if err != nil {
		return false, err
	}
	if current == nil {
		return false, nil
	}
	if err := verifyScriptMutationOwnership(current, expectedProjectID); err != nil {
		return false, err
	}
	if err := tr.projectDeletionErrorLocked(current.ProjectID); err != nil {
		return false, err
	}
	if tr.scriptStore != nil {
		store, ok := tr.scriptStore.(conditionalScriptPersistence)
		if !ok {
			if err := contentmutation.WriteOutsideLock(&tr.mu, func() error { return tr.scriptStore.MoveScript(id, folderID) }); err != nil {
				return false, err
			}
			current.FolderID = cloneStringPtr(folderID)
		} else {
			candidate := cloneScriptDefinition(current)
			candidate.FolderID = cloneStringPtr(folderID)
			candidate, err = preparePersistentLibraryScript(candidate, current)
			if err != nil {
				return false, err
			}
			moved, err := contentmutation.OutsideLock(&tr.mu, func() (bool, error) { return store.UpdateScriptIfUnchanged(candidate, current.UpdatedAt) })
			if err != nil {
				return false, err
			}
			if !moved {
				return false, errScriptConcurrentChange
			}
			current = candidate
		}
	} else {
		current.FolderID = cloneStringPtr(folderID)
	}
	cached.FolderID = cloneStringPtr(current.FolderID)
	if cached.Script != nil {
		cached.Script.FolderID = cloneStringPtr(current.FolderID)
		cached.Script.UpdatedAt = current.UpdatedAt
	}
	tr.scripts[id] = cached
	tr.contentRevision++
	return true, nil
}

func (tr *TestRunner) UpdateScript(id string, script *dsl.Script) (*StoredScript, bool) {
	updated, ok, _ := tr.UpdateScriptDurable(id, script)
	return updated, ok
}

func (tr *TestRunner) UpdateScriptDurable(id string, script *dsl.Script) (*StoredScript, bool, error) {
	return tr.UpdateScriptDurableInProject(id, script, "")
}

// UpdateScriptDurableInProject performs an ownership-bound, compare-and-swap
// update against the authoritative row. It prevents stale HA caches and an
// ID's delete/recreate window from turning a previously authorized request into
// a cross-project overwrite.
func (tr *TestRunner) UpdateScriptDurableInProject(id string, script *dsl.Script, expectedProjectID string) (*StoredScript, bool, error) {
	return tr.updateScriptDurableInProject(
		id, script, expectedProjectID, time.Time{}, "", "", time.Time{}, false, "",
	)
}

// UpdateScriptDurableInProjectIfUnchanged applies a mutation only when the
// authoritative script still has the exact timestamp and executable action
// graph observed by the caller. It is used by multi-step authoring operations
// that derive a clone before committing it; the ordinary durable update's
// storage CAS alone cannot detect a change that happened before its internal
// authoritative read.
func (tr *TestRunner) UpdateScriptDurableInProjectIfUnchanged(id string, script *dsl.Script, expectedProjectID string, expectedUpdatedAt time.Time, expectedActionGraphDigest string) (*StoredScript, bool, error) {
	if expectedUpdatedAt.IsZero() || strings.TrimSpace(expectedActionGraphDigest) == "" {
		return nil, false, fmt.Errorf("script mutation requires an original timestamp and action-graph digest")
	}
	return tr.updateScriptDurableInProject(
		id,
		script,
		expectedProjectID,
		expectedUpdatedAt,
		expectedActionGraphDigest,
		"",
		time.Time{},
		false,
		"",
	)
}

// UpdateImportedScriptDurableInProjectIfUnchanged is the replace-mode
// counterpart to CreateImportedScriptDurableReportingReplay. The authenticated
// import workflow, not caller-controlled metadata, authorizes preservation.
func (tr *TestRunner) UpdateImportedScriptDurableInProjectIfUnchanged(
	id string,
	script *dsl.Script,
	expectedProjectID string,
	expectedUpdatedAt time.Time,
	expectedActionGraphDigest string,
	sourceProfile string,
) (*StoredScript, bool, error) {
	if expectedUpdatedAt.IsZero() || strings.TrimSpace(expectedActionGraphDigest) == "" {
		return nil, false, fmt.Errorf("script mutation requires an original timestamp and action-graph digest")
	}
	if strings.TrimSpace(sourceProfile) == "" {
		return nil, false, fmt.Errorf("import source profile is required")
	}
	return tr.updateScriptDurableInProject(
		id,
		script,
		expectedProjectID,
		expectedUpdatedAt,
		expectedActionGraphDigest,
		"",
		time.Time{},
		true,
		sourceProfile,
	)
}

// UpdateScriptDurableInProjectIfUnchangedAndCorrelationRulesetCurrent adds an
// atomic durable ruleset-revision fence to the ordinary script/action-graph
// CAS. Correlation apply uses this boundary so the ruleset cannot change in the
// final window between preview computation and script commit.
func (tr *TestRunner) UpdateScriptDurableInProjectIfUnchangedAndCorrelationRulesetCurrent(
	id string,
	script *dsl.Script,
	expectedProjectID string,
	expectedUpdatedAt time.Time,
	expectedActionGraphDigest string,
	rulesetID string,
	expectedRulesetUpdatedAt time.Time,
) (*StoredScript, bool, error) {
	if expectedUpdatedAt.IsZero() ||
		strings.TrimSpace(expectedActionGraphDigest) == "" ||
		strings.TrimSpace(rulesetID) == "" ||
		expectedRulesetUpdatedAt.IsZero() {
		return nil, false, fmt.Errorf("script mutation requires original script and correlation ruleset revisions")
	}
	return tr.updateScriptDurableInProject(
		id,
		script,
		expectedProjectID,
		expectedUpdatedAt,
		expectedActionGraphDigest,
		rulesetID,
		expectedRulesetUpdatedAt,
		false,
		"",
	)
}

func (tr *TestRunner) updateScriptDurableInProject(
	id string,
	script *dsl.Script,
	expectedProjectID string,
	expectedUpdatedAt time.Time,
	expectedActionGraphDigest string,
	rulesetID string,
	expectedRulesetUpdatedAt time.Time,
	trustedImport bool,
	sourceProfile string,
	contentPreconditions ...*storage.ScriptAuthoringPrecondition,
) (*StoredScript, bool, error) {
	if script == nil {
		return nil, false, fmt.Errorf("script is required")
	}
	if err := validatePersistentLibraryScript(script); err != nil {
		return nil, false, err
	}
	mutationProjectID := expectedProjectID
	if strings.TrimSpace(mutationProjectID) == "" {
		mutationProjectID = script.ProjectID
	}
	releaseReferenceMutation, err := tr.acquireSecretReferenceMutation(mutationProjectID)
	if err != nil {
		return nil, false, err
	}
	defer releaseReferenceMutation()
	tr.scriptMutationMu.Lock()
	defer tr.scriptMutationMu.Unlock()
	defer tr.flushContentReferenceFloors()
	releaseProjectMutation := tr.acquireProjectTestMutation(mutationProjectID)
	defer releaseProjectMutation()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	var expectedContent *storage.ScriptAuthoringPrecondition
	if len(contentPreconditions) > 0 {
		expectedContent = contentPreconditions[0]
	}
	// An admitted authoring snapshot already names the exact durable primary
	// key. Resolving it as a display alias again could select a different row.
	if expectedContent == nil {
		id = tr.resolveRefKeyLocked(id)
	}
	if err := tr.rejectActivePrivateCISnapshotMutationLocked(id); err != nil {
		return nil, false, err
	}
	authoringStore, supportsAuthoring := tr.scriptStore.(storage.ScriptAuthoringStore)
	if expectedContent != nil && !supportsAuthoring {
		return nil, false, errScriptAuthoringUnsupported
	}
	existing := cloneStoredScriptForReadLocked(tr.scripts[id])
	var current *dsl.Script
	var observed *storage.ScriptAuthoringSnapshot
	if supportsAuthoring {
		observed, err = contentmutation.OutsideLock(&tr.mu, func() (*storage.ScriptAuthoringSnapshot, error) { return authoringStore.GetScriptAuthoringSnapshot(id) })
		if errors.Is(err, storage.ErrScriptAuthoringNotFound) || errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		if observed == nil || observed.Script == nil || isCIScriptSnapshot(observed.Script) {
			return nil, false, nil
		}
		current = observed.Script
		if existing == nil {
			existing = &StoredScript{ID: current.ID, ProjectID: current.ProjectID}
		}
	} else {
		if existing == nil {
			return nil, false, nil
		}
		current, err = tr.durableScriptForMutationLocked(id)
		if err != nil {
			return nil, false, err
		}
		if current == nil {
			return nil, false, nil
		}
	}
	expectedProjectID = expectedScriptMutationProject(expectedProjectID, existing)
	if expectedContent != nil {
		if expectedContent.ProjectID != current.ProjectID || !expectedContent.CreatedAt.Equal(current.CreatedAt) {
			return nil, false, storage.ErrScriptAuthoringNotFound
		}
		if expectedContent.ContentRevision != observed.ContentRevision {
			return nil, false, errors.Join(errScriptConcurrentChange, storage.ErrScriptAuthoringConflict)
		}
	}
	if err := verifyScriptMutationOwnership(current, expectedProjectID); err != nil {
		return nil, false, err
	}
	if !expectedUpdatedAt.IsZero() && !current.UpdatedAt.Equal(expectedUpdatedAt) {
		return nil, false, errScriptConcurrentChange
	}
	if expectedActionGraphDigest != "" {
		currentDigest, digestErr := scriptCorrelationActionGraphDigest(current)
		if digestErr != nil {
			return nil, false, digestErr
		}
		if currentDigest != expectedActionGraphDigest {
			return nil, false, errScriptConcurrentChange
		}
	}
	var projected *dsl.Script
	if trustedImport {
		projected, err = preparePersistentImportedLibraryScript(script, current, sourceProfile)
	} else {
		projected, err = preparePersistentLibraryScript(script, current)
	}
	if err != nil {
		return nil, false, err
	}
	script = projected
	script.ID = current.ID
	script.ProjectID = current.ProjectID
	currentRequiresJavaApproval := scriptRequiresJavaVuserAuthoringApproval(current)
	scriptRequiresJavaApproval := scriptRequiresJavaVuserAuthoringApproval(script)
	if currentRequiresJavaApproval && !scriptRequiresJavaApproval {
		return nil, false, fmt.Errorf("Java Vuser scripts must be changed through local authoring-session review and approval")
	}
	if scriptRequiresJavaApproval {
		if conflict, _ := contentmutation.OutsideLock(&tr.mu, func() (string, error) { return tr.javaVuserApprovalConflict(script), nil }); conflict != "" {
			return nil, false, fmt.Errorf("%s", conflict)
		}
	}
	if err := tr.projectDeletionErrorLocked(current.ProjectID); err != nil {
		return nil, false, err
	}
	if strings.TrimSpace(script.Version) == "" {
		script.Version = current.Version
	}
	if strings.TrimSpace(script.Version) == "" {
		script.Version = "1"
	}
	oldRefKey := current.RefKey
	parts, ok := refkey.Parse(normalizeRefKeyCandidate(current.RefKey))
	if ok && parts.Type == refkey.TypeScript {
		script.RefKey = refkey.Script(parts.Env, parts.Seq, parts.Version+1)
	} else {
		sequence, err := tr.nextScriptSeqLocked()
		if err != nil {
			return nil, false, fmt.Errorf("allocate script reference key: %w", err)
		}
		script.RefKey = refkey.Script(tr.envCode, sequence, 1)
	}
	// Project ownership is immutable for an in-place update. Moving a script
	// requires a dedicated operation that authorizes both source and destination;
	// ordinary update callers must never be able to rewrite tenant ownership.
	if script.FolderID == nil {
		script.FolderID = current.FolderID
	}
	script.CreatedAt = current.CreatedAt
	if tr.scriptStore == nil {
		if strings.TrimSpace(rulesetID) != "" {
			return nil, false, fmt.Errorf("atomic correlation ruleset apply requires durable storage")
		}
		now := time.Now().UTC()
		if !now.After(current.UpdatedAt) {
			now = current.UpdatedAt.Add(time.Nanosecond)
		}
		script.UpdatedAt = now
	}
	var committed *StoredScript
	if supportsAuthoring {
		precondition := storage.ScriptAuthoringPrecondition{ProjectID: current.ProjectID, CreatedAt: current.CreatedAt, ContentRevision: observed.ContentRevision}
		if expectedContent != nil {
			precondition = *expectedContent
		}
		var mutation *storage.ScriptAuthoringMutationResult
		if strings.TrimSpace(rulesetID) != "" {
			mutation, err = contentmutation.OutsideLock(&tr.mu, func() (*storage.ScriptAuthoringMutationResult, error) {
				return authoringStore.UpdateScriptAuthoringIfCurrentAndCorrelationRulesetCurrent(script, precondition, rulesetID, expectedProjectID, expectedRulesetUpdatedAt)
			})
		} else {
			mutation, err = contentmutation.OutsideLock(&tr.mu, func() (*storage.ScriptAuthoringMutationResult, error) {
				return authoringStore.UpdateScriptAuthoringIfCurrent(script, precondition)
			})
		}
		if errors.Is(err, storage.ErrScriptAuthoringConflict) {
			return nil, false, errors.Join(errScriptConcurrentChange, err)
		}
		if err != nil {
			return nil, false, err
		}
		if mutation == nil || !mutation.Applied || mutation.Snapshot == nil {
			return nil, false, errors.Join(errScriptConcurrentChange, storage.ErrScriptAuthoringConflict)
		}
		committed, err = storedScriptFromAuthoringSnapshot(mutation.Snapshot)
		if err != nil {
			return nil, false, err
		}
		script = cloneScriptDefinition(committed.Script)
	} else if tr.scriptStore != nil {
		if strings.TrimSpace(rulesetID) != "" {
			store, ok := tr.scriptStore.(correlationRulesetFencedScriptPersistence)
			if !ok {
				return nil, false, fmt.Errorf("atomic correlation ruleset apply is not supported by the configured storage")
			}
			updated, err := contentmutation.OutsideLock(&tr.mu, func() (bool, error) {
				return store.UpdateScriptIfUnchangedAndCorrelationRulesetCurrent(
					script,
					current.UpdatedAt,
					rulesetID,
					expectedProjectID,
					expectedRulesetUpdatedAt,
				)
			})
			if err != nil {
				return nil, false, err
			}
			if !updated {
				return nil, false, errScriptConcurrentChange
			}
		} else if store, ok := tr.scriptStore.(conditionalScriptPersistence); !ok {
			if err := contentmutation.WriteOutsideLock(&tr.mu, func() error {
				return tr.saveStoredScriptWithImportTrust(
					script,
					current,
					trustedImport,
					sourceProfile,
				)
			}); err != nil {
				return nil, false, err
			}
		} else {
			updated, err := contentmutation.OutsideLock(&tr.mu, func() (bool, error) { return store.UpdateScriptIfUnchanged(script, current.UpdatedAt) })
			if err != nil {
				return nil, false, err
			}
			if !updated {
				return nil, false, errScriptConcurrentChange
			}
		}
	}
	existing.Name = script.Name
	existing.Protocol = script.Protocol
	existing.Version = script.Version
	existing.RefKey = script.RefKey
	existing.ProjectID = script.ProjectID
	existing.FolderID = cloneStringPtr(script.FolderID)
	existing.AuthoringHealth = buildStoredScriptAuthoringHealth(script)
	existing.Script = cloneScriptDefinition(script)
	existing.CreatedAt = script.CreatedAt
	// Display caches never retain a nonce separately from its durable snapshot.
	existing.ContentRevision = ""
	existing.AuthoringProjection = nil
	tr.scripts[id] = existing
	tr.removeRefKeyLocked(existing.RefKey)
	if oldRefKey != existing.RefKey {
		tr.removeRefKeyLocked(oldRefKey)
	}
	tr.indexRefKeyLocked(existing.RefKey, existing.ID)
	tr.contentRevision++
	if committed != nil {
		return committed, true, nil
	}
	return cloneStoredScriptForReadLocked(existing), true, nil
}

func (tr *TestRunner) DeleteScript(id string) bool {
	deleted, _ := tr.DeleteScriptDurable(id)
	return deleted
}

func (tr *TestRunner) DeleteScriptDurable(id string) (bool, error) {
	return tr.DeleteScriptDurableInProject(id, "")
}

// DeleteScriptDurableInProject deletes only the exact authoritative script
// version in the authorized project. A concurrent edit or same-ID replacement
// wins and the caller must reload instead of deleting the newer row.
func (tr *TestRunner) DeleteScriptDurableInProject(id string, expectedProjectID string) (bool, error) {
	mutationProjectID := expectedProjectID
	if strings.TrimSpace(mutationProjectID) == "" {
		mutationProjectID = tr.scriptProjectIDForSecretReferenceMutation(id)
	}
	releaseReferenceMutation, err := tr.acquireSecretReferenceMutation(mutationProjectID)
	if err != nil {
		return false, err
	}
	defer releaseReferenceMutation()
	tr.scriptMutationMu.Lock()
	defer tr.scriptMutationMu.Unlock()
	defer tr.flushContentReferenceFloors()
	releaseProjectMutation := tr.acquireProjectTestMutation(mutationProjectID)
	defer releaseProjectMutation()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	id = tr.resolveRefKeyLocked(id)
	if err := tr.rejectActivePrivateCISnapshotMutationLocked(id); err != nil {
		return false, err
	}
	cached := tr.scripts[id]
	if cached == nil {
		return false, nil
	}
	expectedProjectID = expectedScriptMutationProject(expectedProjectID, cached)
	current, err := tr.durableScriptForMutationLocked(id)
	if err != nil {
		return false, err
	}
	if current == nil {
		return false, nil
	}
	if err := verifyScriptMutationOwnership(current, expectedProjectID); err != nil {
		return false, err
	}
	if err := tr.projectDeletionErrorLocked(current.ProjectID); err != nil {
		return false, err
	}
	if tr.scriptStore != nil {
		store, ok := tr.scriptStore.(conditionalScriptDeletePersistence)
		if !ok {
			if err := contentmutation.WriteOutsideLock(&tr.mu, func() error { return tr.deleteStoredScript(id) }); err != nil {
				return false, err
			}
		} else {
			deleted, err := contentmutation.OutsideLock(&tr.mu, func() (bool, error) { return store.DeleteScriptIfUnchanged(id, current.UpdatedAt) })
			if err != nil {
				return false, err
			}
			if !deleted {
				return false, errScriptConcurrentChange
			}
		}
	}
	tr.removeRefKeyLocked(cached.RefKey)
	delete(tr.scripts, id)
	tr.contentRevision++
	return true, nil
}

func (tr *TestRunner) ListScriptsWithUsage() []*StoredScript {
	tr.refreshScriptsForHARead()
	tr.mu.RLock()
	out := make([]*StoredScript, 0, len(tr.scripts))
	for _, script := range tr.scripts {
		if isStoredCIScriptSnapshot(script) {
			continue
		}
		out = append(out, tr.enrichScriptSummaryLocked(script))
	}
	tr.mu.RUnlock()
	return tr.filterPublishedScripts(out)
}

func (tr *TestRunner) enrichScriptSummaryLocked(script *StoredScript) *StoredScript {
	if script == nil {
		return nil
	}
	copy := cloneStoredScriptForReadLocked(script)
	scenarioSet := map[string]ScenarioRef{}
	releaseSet := map[string]ReleaseRef{}
	scenarioRunReleaseSet := map[string]ReleaseRef{}
	var lastRun *time.Time
	for _, scenario := range tr.scenarios {
		for _, scenarioScript := range scenario.Scripts {
			if scenarioScript.ScriptID == script.ID {
				scenarioSet[scenario.ID] = ScenarioRef{ID: scenario.ID, Name: scenario.Name}
			}
		}
	}
	for _, test := range tr.tests {
		usesScript := false
		for _, group := range test.Groups {
			if group.ScriptID == script.ID {
				usesScript = true
				break
			}
		}
		if !usesScript {
			continue
		}
		if test.ReleaseID != "" {
			releaseSet[test.ReleaseID] = tr.releaseRefLocked(test.ReleaseID)
			if test.ScenarioID != "" {
				scenarioRunReleaseSet[test.ReleaseID] = tr.releaseRefLocked(test.ReleaseID)
			}
		}
		if lastRun == nil || test.CreatedAt.After(*lastRun) {
			t := test.CreatedAt
			lastRun = &t
		}
	}
	copy.ScenarioCount = len(scenarioSet)
	copy.AssociatedScenarios = scenarioRefsFromSet(scenarioSet)
	copy.AssociatedReleases = releaseRefsFromSet(releaseSet)
	copy.ScenarioRunReleases = releaseRefsFromSet(scenarioRunReleaseSet)
	copy.LastRunAt = lastRun
	return copy
}

// cloneStoredScriptForReadLocked snapshots every mutable field before a public
// runner method releases tr.mu. The canonical script remains private to the
// runner, preventing torn JSON responses and caller mutation of live state.
func cloneStoredScriptForReadLocked(script *StoredScript) *StoredScript {
	if script == nil {
		return nil
	}
	cloned := *script
	cloned.AuthoringProjection = cloneScriptAuthoringProjection(script.AuthoringProjection)
	cloned.FolderID = cloneStringPtr(script.FolderID)
	cloned.AssociatedScenarios = append([]ScenarioRef(nil), script.AssociatedScenarios...)
	cloned.AssociatedReleases = append([]ReleaseRef(nil), script.AssociatedReleases...)
	cloned.ScenarioRunReleases = append([]ReleaseRef(nil), script.ScenarioRunReleases...)
	cloned.LastRunAt = cloneTimePtr(script.LastRunAt)
	if script.AuthoringHealth != nil {
		health := *script.AuthoringHealth
		health.Inputs = append([]AuthoringHealthItem(nil), script.AuthoringHealth.Inputs...)
		cloned.AuthoringHealth = &health
	}
	cloned.Script = cloneScriptDefinition(script.Script)
	return &cloned
}
