package api

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
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/refkey"
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

var errScriptConcurrentChange = errors.New("script changed concurrently; reload and retry")

func validatePersistentLibraryScript(script *dsl.Script) error {
	if isCIScriptSnapshot(script) {
		return fmt.Errorf("generated_by value %q is reserved for private CI snapshots", script.GeneratedBy)
	}
	return nil
}

// preparePersistentLibraryScript is the final defense shared by every durable
// library write. HTTP handlers provide richer structured binding responses,
// but internal import/sync/authoring callers must not be able to bypass the
// invariant: executable credentials require {{secret.*}} references, and only
// the disclosure-safe projection may reach storage.
func preparePersistentLibraryScript(script, prior *dsl.Script) (*dsl.Script, error) {
	if err := validateJavaVMParameterDisclosureSafety(script); err != nil {
		return nil, err
	}
	projected := maskScriptSecrets(script)
	if projected == nil {
		return nil, fmt.Errorf("failed to project script for durable secret binding")
	}
	if unresolved := discoverSecretBindingRequirements(projected, 0); len(unresolved) > 0 {
		return nil, fmt.Errorf("script contains executable credentials that require logical secret bindings")
	}
	if err := reconcileMaskedScriptSecrets(projected, prior); err != nil {
		return nil, err
	}
	return projected, nil
}

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
	script, err := reader.GetScript(id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return script, nil
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
	return tr.storeScriptDurable(script, false)
}

// storeScriptDurableForRestore is restricted to hydrating records that already
// existed before authoring-session governance was introduced. New external
// persistence paths must use StoreScriptDurable and cannot manufacture legacy
// full_lifecycle grandfathering.
func (tr *TestRunner) storeScriptDurableForRestore(script *dsl.Script) (*StoredScript, error) {
	return tr.storeScriptDurable(script, true)
}

func (tr *TestRunner) storeScriptDurable(script *dsl.Script, restoringExistingJava bool) (*StoredScript, error) {
	if script == nil {
		return nil, fmt.Errorf("script is required")
	}
	if scriptRequiresJavaVuserAuthoringApproval(script) && !restoringExistingJava {
		return nil, fmt.Errorf("Java Vuser scripts must be saved through authoring-session review and approval")
	}
	if err := validatePersistentLibraryScript(script); err != nil {
		return nil, err
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()

	scriptID := strings.TrimSpace(script.ID)
	if scriptID == "" {
		scriptID = uuid.New().String()
	}
	if err := tr.rejectActivePrivateCISnapshotMutationLocked(scriptID); err != nil {
		return nil, err
	}
	existing := tr.scripts[scriptID]
	current, err := tr.durableScriptForMutationLocked(scriptID)
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
	if err := tr.projectDeletionErrorLocked(script.ProjectID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(script.Version) == "" {
		script.Version = "1"
	}
	if existing != nil && strings.TrimSpace(script.RefKey) == "" {
		script.RefKey = existing.RefKey
	}
	if script.RefKey == "" {
		sequence, err := tr.nextScriptSeqLocked()
		if err != nil {
			return nil, fmt.Errorf("allocate script reference key: %w", err)
		}
		script.RefKey = refkey.Script(tr.envCode, sequence, 1)
	}

	createdAt := time.Now()
	if existing != nil && !existing.CreatedAt.IsZero() {
		createdAt = existing.CreatedAt
	}
	if script.CreatedAt.IsZero() {
		script.CreatedAt = createdAt
	}
	if tr.scriptStore == nil {
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

	if tr.scriptStore != nil {
		_, supportsCreateOnly := tr.scriptStore.(createOnlyScriptPersistence)
		_, supportsCAS := tr.scriptStore.(conditionalScriptPersistence)
		if !supportsCreateOnly || !supportsCAS {
			if err := tr.saveStoredScript(script); err != nil {
				return nil, err
			}
		} else {
			if current == nil {
				store, ok := tr.scriptStore.(createOnlyScriptPersistence)
				if !ok {
					return nil, fmt.Errorf("durable script store does not support atomic create-only persistence")
				}
				created, err := store.CreateScriptIfAbsent(script)
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
				store, ok := tr.scriptStore.(conditionalScriptPersistence)
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

	if existing != nil {
		tr.removeRefKeyLocked(existing.RefKey)
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
	return tr.createScriptDurable(script, false)
}

func (tr *TestRunner) createAuthoringApprovedScriptDurable(script *dsl.Script) (*StoredScript, error) {
	if scriptRequiresJavaVuserAuthoringApproval(script) {
		if _, ok := readJavaVuserAuthoringApproval(script); !ok {
			return nil, fmt.Errorf("Java Vuser authoring approval evidence is missing")
		}
	}
	return tr.createScriptDurable(script, true)
}

func (tr *TestRunner) createScriptDurable(script *dsl.Script, authoringApprovedJava bool) (*StoredScript, error) {
	if script == nil {
		return nil, fmt.Errorf("script is required")
	}
	if scriptRequiresJavaVuserAuthoringApproval(script) && !authoringApprovedJava {
		return nil, fmt.Errorf("Java Vuser scripts must be saved through authoring-session review and approval")
	}
	if err := validatePersistentLibraryScript(script); err != nil {
		return nil, err
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	scriptID := strings.TrimSpace(script.ID)
	if scriptID == "" {
		scriptID = uuid.New().String()
	}
	if err := tr.rejectActivePrivateCISnapshotMutationLocked(scriptID); err != nil {
		return nil, err
	}
	projected, err := preparePersistentLibraryScript(script, nil)
	if err != nil {
		return nil, err
	}
	script = projected
	script.ID = scriptID
	script.ProjectID = normalizeProjectID(script.ProjectID)
	if err := tr.projectDeletionErrorLocked(script.ProjectID); err != nil {
		return nil, err
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
	if store, ok := tr.scriptStore.(createOnlyScriptPersistence); ok {
		// Shared storage is authoritative. A replica can have an equivalent but
		// stale cached script after another replica edited or replaced the durable
		// row, so never let the cache bypass the create-only ownership fence.
		if strings.TrimSpace(script.RefKey) == "" {
			sequence, err := tr.nextScriptSeqLocked()
			if err != nil {
				return nil, fmt.Errorf("allocate script reference key: %w", err)
			}
			script.RefKey = refkey.Script(tr.envCode, sequence, 1)
		}
		created, err := store.CreateScriptIfAbsent(script)
		if err != nil {
			return nil, err
		}
		if !created {
			existing, err := store.GetScript(script.ID)
			if err != nil {
				return nil, fmt.Errorf("load conflicting script %s: %w", script.ID, err)
			}
			if normalizeProjectID(existing.ProjectID) != script.ProjectID || !equivalentCreateOnlyScript(existing, script) {
				return nil, fmt.Errorf("%w: script %s already exists and is not owned by this approval", errScriptConcurrentChange, script.ID)
			}
			persisted = existing
		}
	} else if tr.scriptStore != nil {
		return nil, fmt.Errorf("durable script store does not support atomic create-only persistence")
	} else {
		if existing := tr.scripts[script.ID]; existing != nil {
			if normalizeProjectID(existing.ProjectID) != script.ProjectID || !equivalentCreateOnlyScript(existing.Script, script) {
				return nil, fmt.Errorf("%w: script %s already exists and is not owned by this approval", errScriptConcurrentChange, script.ID)
			}
			return cloneStoredScriptForReadLocked(existing), nil
		}
		if strings.TrimSpace(script.RefKey) == "" {
			sequence, err := tr.nextScriptSeqLocked()
			if err != nil {
				return nil, fmt.Errorf("allocate script reference key: %w", err)
			}
			script.RefKey = refkey.Script(tr.envCode, sequence, 1)
		}
		if err := tr.saveStoredScript(script); err != nil {
			return nil, err
		}
	}

	createdAt := persisted.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
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
	return cloneStoredScriptForReadLocked(stored), nil
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
		updated, err := store.UpdateScriptIfUnchanged(script, expected.UpdatedAt)
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
		if err := tr.saveStoredScript(script); err != nil {
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
	tr.mu.Lock()
	defer tr.mu.Unlock()
	id = strings.TrimSpace(id)
	if id == "" || expectedUpdatedAt.IsZero() {
		return false, fmt.Errorf("script id and expected updated_at are required")
	}
	if err := tr.rejectActivePrivateCISnapshotMutationLocked(id); err != nil {
		return false, err
	}
	deleted := false
	if store, ok := tr.scriptStore.(conditionalScriptDeletePersistence); ok {
		var err error
		deleted, err = store.DeleteScriptIfUnchanged(id, expectedUpdatedAt)
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
		if err := tr.deleteStoredScript(id); err != nil {
			return false, err
		}
		deleted = true
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
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}

func (tr *TestRunner) GetScript(id string) (*StoredScript, bool) {
	tr.refreshScriptsForHARead()
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	id = tr.resolveRefKeyLocked(id)
	s, ok := tr.scripts[id]
	if ok && isStoredCIScriptSnapshot(s) {
		return nil, false
	}
	return cloneStoredScriptForReadLocked(s), ok
}

func (tr *TestRunner) refreshScriptsForHARead() {
	if tr == nil || tr.leadership == nil {
		return
	}
	if err := tr.reloadStoredScripts(); err != nil {
		slog.Warn("failed to refresh scripts for HA read", "error", err)
	}
}

// GetScriptDurable refreshes from shared storage when available. Approval
// retries use it to return an already-promoted script without writing the old
// session draft back over edits made after approval on another HA replica.
func (tr *TestRunner) GetScriptDurable(id string) (*StoredScript, bool, error) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	id = tr.resolveRefKeyLocked(id)
	if store, ok := tr.scriptStore.(createOnlyScriptPersistence); ok {
		script, err := store.GetScript(id)
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
			CreatedAt: createdAt, AuthoringHealth: buildStoredScriptAuthoringHealth(script), Script: cloneScriptDefinition(script),
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
	return tr.ReplayScriptDefinition(script, opts)
}

func (tr *TestRunner) ReplayScriptDefinition(script *dsl.Script, opts engine.ReplayOptions) (*engine.ReplayReport, error) {
	return tr.ReplayScriptDefinitionContext(context.Background(), script, opts)
}

func (tr *TestRunner) ReplayScriptDefinitionContext(ctx context.Context, script *dsl.Script, opts engine.ReplayOptions) (*engine.ReplayReport, error) {
	if script == nil {
		return nil, fmt.Errorf("script is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := replayReviewGateError(script); err != nil {
		return nil, err
	}
	effectiveRedactionRules, err := tr.resolveEffectiveRedactionRules(script.ProjectID, opts.RedactionRules)
	if err != nil {
		return nil, err
	}
	opts.RedactionRules = effectiveRedactionRules
	if opts.SecretResolver == nil {
		opts.SecretResolver = tr.secretResolverForProject(script.ProjectID)
	}
	return engine.ReplayScript(ctx, script, opts)
}

func (tr *TestRunner) ListScripts() []*StoredScript {
	tr.refreshScriptsForHARead()
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	out := make([]*StoredScript, 0, len(tr.scripts))
	for _, s := range tr.scripts {
		if isStoredCIScriptSnapshot(s) {
			continue
		}
		out = append(out, cloneStoredScriptForReadLocked(s))
	}
	return out
}

func (tr *TestRunner) ListScriptsFiltered(projectID string, folderID *string) []*StoredScript {
	tr.refreshScriptsForHARead()
	projectID = normalizeProjectID(projectID)
	tr.mu.RLock()
	defer tr.mu.RUnlock()
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
	return out
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
		store, ok := tr.scriptStore.(conditionalScriptPersistence)
		if !ok {
			if err := tr.scriptStore.MoveScript(id, folderID); err != nil {
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
			moved, err := store.UpdateScriptIfUnchanged(candidate, current.UpdatedAt)
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
	return tr.updateScriptDurableInProject(id, script, expectedProjectID, time.Time{}, "", "", time.Time{})
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
	return tr.updateScriptDurableInProject(id, script, expectedProjectID, expectedUpdatedAt, expectedActionGraphDigest, "", time.Time{})
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
) (*StoredScript, bool, error) {
	if script == nil {
		return nil, false, fmt.Errorf("script is required")
	}
	if err := validatePersistentLibraryScript(script); err != nil {
		return nil, false, err
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	id = tr.resolveRefKeyLocked(id)
	if err := tr.rejectActivePrivateCISnapshotMutationLocked(id); err != nil {
		return nil, false, err
	}
	existing, ok := tr.scripts[id]
	if !ok {
		return nil, false, nil
	}
	expectedProjectID = expectedScriptMutationProject(expectedProjectID, existing)
	current, err := tr.durableScriptForMutationLocked(id)
	if err != nil {
		return nil, false, err
	}
	if current == nil {
		return nil, false, nil
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
	projected, err := preparePersistentLibraryScript(script, current)
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
		if conflict := tr.javaVuserApprovalConflict(script); conflict != "" {
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
	if script.CreatedAt.IsZero() {
		script.CreatedAt = current.CreatedAt
	}
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
	if tr.scriptStore != nil {
		if strings.TrimSpace(rulesetID) != "" {
			store, ok := tr.scriptStore.(correlationRulesetFencedScriptPersistence)
			if !ok {
				return nil, false, fmt.Errorf("atomic correlation ruleset apply is not supported by the configured storage")
			}
			updated, err := store.UpdateScriptIfUnchangedAndCorrelationRulesetCurrent(
				script,
				current.UpdatedAt,
				rulesetID,
				expectedProjectID,
				expectedRulesetUpdatedAt,
			)
			if err != nil {
				return nil, false, err
			}
			if !updated {
				return nil, false, errScriptConcurrentChange
			}
		} else if store, ok := tr.scriptStore.(conditionalScriptPersistence); !ok {
			if err := tr.saveStoredScript(script); err != nil {
				return nil, false, err
			}
		} else {
			updated, err := store.UpdateScriptIfUnchanged(script, current.UpdatedAt)
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
	tr.removeRefKeyLocked(existing.RefKey)
	if oldRefKey != existing.RefKey {
		tr.removeRefKeyLocked(oldRefKey)
	}
	tr.indexRefKeyLocked(existing.RefKey, existing.ID)
	tr.contentRevision++
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
			if err := tr.deleteStoredScript(id); err != nil {
				return false, err
			}
		} else {
			deleted, err := store.DeleteScriptIfUnchanged(id, current.UpdatedAt)
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
	defer tr.mu.RUnlock()
	out := make([]*StoredScript, 0, len(tr.scripts))
	for _, script := range tr.scripts {
		if isStoredCIScriptSnapshot(script) {
			continue
		}
		out = append(out, tr.enrichScriptSummaryLocked(script))
	}
	return out
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
