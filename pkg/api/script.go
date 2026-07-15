package api

// Script persistence + draft replay: CRUD for stored scripts (store, get,
// list/filter, move, update, delete, usage) plus draft replay entry points and
// summary enrichment. Extracted from runner.go; methods hang off *TestRunner.

import (
	"context"
	"encoding/json"
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

func (tr *TestRunner) StoreScript(script *dsl.Script) *StoredScript {
	stored, _ := tr.StoreScriptDurable(script)
	return stored
}

func (tr *TestRunner) StoreScriptDurable(script *dsl.Script) (*StoredScript, error) {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	if script.ID == "" {
		script.ID = uuid.New().String()
	}
	script.ProjectID = normalizeProjectID(script.ProjectID)
	existing := tr.scripts[script.ID]
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

	if err := tr.saveStoredScript(script); err != nil {
		return nil, err
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
	if script == nil {
		return nil, fmt.Errorf("script is required")
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if script.ID == "" {
		script.ID = uuid.New().String()
	}
	script.ProjectID = normalizeProjectID(script.ProjectID)
	if err := tr.projectDeletionErrorLocked(script.ProjectID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(script.Version) == "" {
		script.Version = "1"
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
				return nil, fmt.Errorf("script %s already exists and is not owned by this approval", script.ID)
			}
			persisted = existing
		}
	} else if tr.scriptStore != nil {
		return nil, fmt.Errorf("durable script store does not support atomic create-only persistence")
	} else {
		if existing := tr.scripts[script.ID]; existing != nil {
			if normalizeProjectID(existing.ProjectID) != script.ProjectID || !equivalentCreateOnlyScript(existing.Script, script) {
				return nil, fmt.Errorf("script %s already exists and is not owned by this approval", script.ID)
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
	if script == nil {
		return nil, fmt.Errorf("script is required")
	}
	effectiveRedactionRules, err := tr.resolveEffectiveRedactionRules(script.ProjectID, opts.RedactionRules)
	if err != nil {
		return nil, err
	}
	opts.RedactionRules = effectiveRedactionRules
	return engine.ReplayScript(context.Background(), script, opts)
}

func (tr *TestRunner) ListScripts() []*StoredScript {
	tr.refreshScriptsForHARead()
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	out := make([]*StoredScript, 0, len(tr.scripts))
	for _, s := range tr.scripts {
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
	tr.mu.Lock()
	defer tr.mu.Unlock()
	id = tr.resolveRefKeyLocked(id)
	script, ok := tr.scripts[id]
	if !ok {
		return false, nil
	}
	if err := tr.projectDeletionErrorLocked(script.ProjectID); err != nil {
		return false, err
	}
	if tr.scriptStore != nil {
		if err := tr.scriptStore.MoveScript(id, folderID); err != nil {
			return false, err
		}
	}
	script.FolderID = cloneStringPtr(folderID)
	if script.Script != nil {
		script.Script.FolderID = cloneStringPtr(folderID)
	}
	tr.contentRevision++
	return true, nil
}

func (tr *TestRunner) UpdateScript(id string, script *dsl.Script) (*StoredScript, bool) {
	updated, ok, _ := tr.UpdateScriptDurable(id, script)
	return updated, ok
}

func (tr *TestRunner) UpdateScriptDurable(id string, script *dsl.Script) (*StoredScript, bool, error) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	id = tr.resolveRefKeyLocked(id)
	existing, ok := tr.scripts[id]
	if !ok {
		return nil, false, nil
	}
	if err := tr.projectDeletionErrorLocked(existing.ProjectID); err != nil {
		return nil, false, err
	}
	script.ID = existing.ID
	if strings.TrimSpace(script.Version) == "" {
		script.Version = existing.Version
	}
	if strings.TrimSpace(script.Version) == "" {
		script.Version = "1"
	}
	oldRefKey := existing.RefKey
	parts, ok := refkey.Parse(normalizeRefKeyCandidate(existing.RefKey))
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
	script.ProjectID = existing.ProjectID
	if script.FolderID == nil {
		script.FolderID = existing.FolderID
	}
	if script.CreatedAt.IsZero() && existing.Script != nil {
		script.CreatedAt = existing.Script.CreatedAt
	}
	if err := tr.saveStoredScript(script); err != nil {
		return nil, false, err
	}
	existing.Name = script.Name
	existing.Protocol = script.Protocol
	existing.Version = script.Version
	existing.RefKey = script.RefKey
	existing.ProjectID = script.ProjectID
	existing.FolderID = cloneStringPtr(script.FolderID)
	existing.AuthoringHealth = buildStoredScriptAuthoringHealth(script)
	existing.Script = cloneScriptDefinition(script)
	tr.removeRefKeyLocked(oldRefKey)
	tr.indexRefKeyLocked(existing.RefKey, existing.ID)
	tr.contentRevision++
	return cloneStoredScriptForReadLocked(existing), true, nil
}

func (tr *TestRunner) DeleteScript(id string) bool {
	deleted, _ := tr.DeleteScriptDurable(id)
	return deleted
}

func (tr *TestRunner) DeleteScriptDurable(id string) (bool, error) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	id = tr.resolveRefKeyLocked(id)
	script, ok := tr.scripts[id]
	if !ok {
		return false, nil
	}
	if err := tr.projectDeletionErrorLocked(script.ProjectID); err != nil {
		return false, err
	}
	if err := tr.deleteStoredScript(id); err != nil {
		return false, err
	}
	tr.removeRefKeyLocked(script.RefKey)
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
