package api

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

// importPublicationAssetKey is deliberately local to the API layer. Durable
// import claims remain recovery evidence rather than becoming part of the
// public script model or the storage schema.
func importPublicationAssetKey(kind, id string) string {
	return normalizeLibraryKind(kind) + "\x00" + strings.TrimSpace(id)
}

func (tr *TestRunner) setImportPublicationSnapshot(store storage.Store, loader func() (map[string]struct{}, error)) {
	if tr == nil {
		return
	}
	tr.importPublicationMu.Lock()
	tr.importPublicationStore = store
	tr.importPublicationSnapshot = loader
	tr.importPublicationMu.Unlock()
}

// pendingImportPublicationSnapshot reads the transaction publication fence
// after an asset snapshot has been captured. Direct-import claims are durable
// before their first asset write, so this ordering prevents a concurrent create
// from slipping between the claim read and the library read. A commit may race
// the lookup in the other direction; that is safe because the durable commit
// marker is published only after every asset has been written.
func (tr *TestRunner) pendingImportPublicationSnapshot() (map[string]struct{}, error) {
	if tr == nil {
		return nil, nil
	}
	tr.importPublicationMu.RLock()
	loader := tr.importPublicationSnapshot
	tr.importPublicationMu.RUnlock()
	if loader == nil {
		return nil, nil
	}
	pending, err := loader()
	if err == nil && tr.afterImportPublicationSnapshot != nil {
		tr.afterImportPublicationSnapshot()
	}
	return pending, err
}

func (tr *TestRunner) importPublicationConfigured() bool {
	if tr == nil {
		return false
	}
	tr.importPublicationMu.RLock()
	configured := tr.importPublicationSnapshot != nil
	tr.importPublicationMu.RUnlock()
	return configured
}

func importPublicationContains(pending map[string]struct{}, kind, id string) bool {
	_, ok := pending[importPublicationAssetKey(kind, id)]
	return ok
}

func (tr *TestRunner) importAssetPublished(kind, id string) bool {
	pending, err := tr.pendingImportPublicationSnapshot()
	if err != nil {
		slog.Warn("failed to read direct-import publication fence; hiding asset", "kind", kind, "id", id, "error", err)
		return false
	}
	return !importPublicationContains(pending, kind, id)
}

func (tr *TestRunner) getScriptIncludingPending(id string) (*StoredScript, bool) {
	if tr == nil {
		return nil, false
	}
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	id = tr.resolveRefKeyLocked(id)
	stored, ok := tr.scripts[id]
	if ok && isStoredCIScriptSnapshot(stored) {
		return nil, false
	}
	return cloneStoredScriptForReadLocked(stored), ok
}

func (tr *TestRunner) filterPublishedScripts(candidates []*StoredScript) []*StoredScript {
	visible, _ := tr.filterPublishedLibraryAssets(candidates, nil, "", nil)
	return visible
}

func (tr *TestRunner) filterPublishedScriptsScoped(candidates []*StoredScript, projectID string, folderID *string) []*StoredScript {
	visible, _ := tr.filterPublishedLibraryAssets(candidates, nil, projectID, folderID)
	return visible
}

func (tr *TestRunner) filterPublishedScenarios(candidates []*StoredScenario) []*StoredScenario {
	_, visible := tr.filterPublishedLibraryAssets(nil, candidates, "", nil)
	return visible
}

func (tr *TestRunner) filterPublishedScenariosScoped(candidates []*StoredScenario, projectID string) []*StoredScenario {
	_, visible := tr.filterPublishedLibraryAssets(nil, candidates, projectID, nil)
	return visible
}

// filterPublishedLibraryAssets uses one publication snapshot for both asset
// kinds. Catalog reads can therefore never mix pre-commit scripts with
// post-commit scenarios from the same direct-import transaction. Revalidating
// after the claim read also prevents a rollback from returning a stale clone.
func (tr *TestRunner) filterPublishedLibraryAssets(scriptCandidates []*StoredScript, scenarioCandidates []*StoredScenario, projectID string, folderID *string) ([]*StoredScript, []*StoredScenario) {
	projectID = strings.TrimSpace(projectID)
	if projectID != "" {
		projectID = normalizeProjectID(projectID)
	}
	scriptInScope := func(script *dsl.Script) bool {
		if script == nil || isCIScriptSnapshot(script) || (projectID != "" && normalizeProjectID(script.ProjectID) != projectID) {
			return false
		}
		if folderID == nil {
			return true
		}
		if *folderID == "" {
			return script.FolderID == nil || *script.FolderID == ""
		}
		return script.FolderID != nil && *script.FolderID == *folderID
	}
	pending, err := tr.pendingImportPublicationSnapshot()
	if err != nil {
		slog.Warn("failed to read direct-import publication fence; hiding library assets", "error", err)
		return nil, nil
	}
	tr.importPublicationMu.RLock()
	store := tr.importPublicationStore
	tr.importPublicationMu.RUnlock()
	if store == nil {
		tr.mu.RLock()
		currentScriptVersions := make(map[string]time.Time, len(scriptCandidates))
		if scriptCandidates != nil {
			for _, current := range tr.scripts {
				if current != nil && scriptInScope(current.Script) {
					currentScriptVersions[current.ID] = current.Script.UpdatedAt
				}
			}
		}
		currentScenarioVersions := make(map[string]time.Time, len(scenarioCandidates))
		if scenarioCandidates != nil {
			for _, current := range tr.scenarios {
				if current != nil && (projectID == "" || normalizeProjectID(current.ProjectID) == projectID) {
					currentScenarioVersions[current.ID] = current.UpdatedAt
				}
			}
		}
		if (scriptCandidates != nil && !storedScriptVersionSetMatches(scriptCandidates, currentScriptVersions)) ||
			(scenarioCandidates != nil && !storedScenarioVersionSetMatches(scenarioCandidates, currentScenarioVersions)) {
			tr.mu.RUnlock()
			return nil, nil
		}
		visibleScripts := scriptCandidates[:0]
		for _, candidate := range scriptCandidates {
			if candidate == nil || candidate.Script == nil {
				continue
			}
			if importPublicationContains(pending, "script", candidate.ID) {
				continue
			}
			visibleScripts = append(visibleScripts, candidate)
		}
		visibleScenarios := scenarioCandidates[:0]
		for _, candidate := range scenarioCandidates {
			if candidate == nil {
				continue
			}
			if importPublicationContains(pending, "scenario", candidate.ID) {
				continue
			}
			visibleScenarios = append(visibleScenarios, candidate)
		}
		tr.mu.RUnlock()
		return visibleScripts, visibleScenarios
	}
	currentScriptVersions := make(map[string]time.Time, len(scriptCandidates))
	if scriptCandidates != nil {
		current, loadErr := store.ListScripts(projectID, folderID)
		if loadErr != nil {
			slog.Warn("failed to revalidate direct-import script listing; hiding library assets", "error", loadErr)
			return nil, nil
		}
		for _, script := range current {
			if scriptInScope(script) {
				currentScriptVersions[script.ID] = script.UpdatedAt
			}
		}
	}
	currentScenarioVersions := make(map[string]time.Time, len(scenarioCandidates))
	if scenarioCandidates != nil {
		applied, loadErr := tr.reloadStoredScenariosApplied()
		if loadErr != nil || !applied {
			slog.Warn("failed to revalidate direct-import scenario listing; hiding library assets", "error", loadErr)
			return nil, nil
		}
		tr.mu.RLock()
		for _, scenario := range tr.scenarios {
			if scenario != nil && (projectID == "" || normalizeProjectID(scenario.ProjectID) == projectID) {
				currentScenarioVersions[scenario.ID] = scenario.UpdatedAt
			}
		}
		tr.mu.RUnlock()
	}
	if (scriptCandidates != nil && !storedScriptVersionSetMatches(scriptCandidates, currentScriptVersions)) ||
		(scenarioCandidates != nil && !storedScenarioVersionSetMatches(scenarioCandidates, currentScenarioVersions)) {
		return nil, nil
	}
	visibleScripts := scriptCandidates[:0]
	for _, candidate := range scriptCandidates {
		if candidate == nil || importPublicationContains(pending, "script", candidate.ID) {
			continue
		}
		if candidate.Script == nil || !currentScriptVersions[candidate.ID].Equal(candidate.Script.UpdatedAt) {
			continue
		}
		visibleScripts = append(visibleScripts, candidate)
	}
	visibleScenarios := scenarioCandidates[:0]
	for _, candidate := range scenarioCandidates {
		if candidate == nil || importPublicationContains(pending, "scenario", candidate.ID) {
			continue
		}
		if !currentScenarioVersions[candidate.ID].Equal(candidate.UpdatedAt) {
			continue
		}
		visibleScenarios = append(visibleScenarios, candidate)
	}
	return visibleScripts, visibleScenarios
}

func storedScriptVersionSetMatches(candidates []*StoredScript, current map[string]time.Time) bool {
	if len(candidates) != len(current) {
		return false
	}
	for _, candidate := range candidates {
		if candidate == nil || candidate.Script == nil || !current[candidate.ID].Equal(candidate.Script.UpdatedAt) {
			return false
		}
	}
	return true
}

func storedScenarioVersionSetMatches(candidates []*StoredScenario, current map[string]time.Time) bool {
	if len(candidates) != len(current) {
		return false
	}
	for _, candidate := range candidates {
		if candidate == nil || !current[candidate.ID].Equal(candidate.UpdatedAt) {
			return false
		}
	}
	return true
}

func (tr *TestRunner) revalidatePublishedScriptSnapshot(snapshot *StoredScript) (*StoredScript, bool) {
	if snapshot == nil {
		return nil, false
	}
	if tr.scriptStore == nil {
		tr.mu.RLock()
		current := tr.scripts[snapshot.ID]
		unchanged := current != nil && current.Script != nil && snapshot.Script != nil && current.Script.UpdatedAt.Equal(snapshot.Script.UpdatedAt)
		tr.mu.RUnlock()
		return snapshot, unchanged
	}
	tr.importPublicationMu.RLock()
	store := tr.importPublicationStore
	tr.importPublicationMu.RUnlock()
	if store == nil {
		return nil, false
	}
	if snapshot.ContentRevision != "" {
		if authoringStore, supported := tr.scriptStore.(storage.ScriptAuthoringStore); supported {
			current, err := authoringStore.GetScriptAuthoringSnapshot(snapshot.ID)
			unchanged := err == nil && current != nil && current.Script != nil && snapshot.Script != nil && current.ContentRevision == snapshot.ContentRevision && current.Script.ProjectID == snapshot.Script.ProjectID && current.Script.CreatedAt.Equal(snapshot.Script.CreatedAt)
			return snapshot, unchanged
		}
		return nil, false
	}
	current, err := store.GetScript(snapshot.ID)
	if err != nil || current == nil || snapshot.Script == nil || !current.UpdatedAt.Equal(snapshot.Script.UpdatedAt) {
		return nil, false
	}
	return snapshot, true
}

func (tr *TestRunner) revalidatePublishedScenarioSnapshot(snapshot *StoredScenario) (*StoredScenario, bool) {
	if snapshot == nil {
		return nil, false
	}
	if tr.scenarioStore == nil {
		tr.mu.RLock()
		current := tr.scenarios[snapshot.ID]
		unchanged := current != nil && current.UpdatedAt.Equal(snapshot.UpdatedAt)
		tr.mu.RUnlock()
		return snapshot, unchanged
	}
	tr.importPublicationMu.RLock()
	store := tr.importPublicationStore
	tr.importPublicationMu.RUnlock()
	if store == nil {
		return nil, false
	}
	current, _, err := store.GetScenario(snapshot.ID)
	if err != nil || current == nil || !current.UpdatedAt.Equal(snapshot.UpdatedAt) {
		return nil, false
	}
	return snapshot, true
}

func (tr *TestRunner) publishedScriptSnapshot(snapshot *StoredScript) (*StoredScript, bool) {
	if snapshot == nil {
		return nil, false
	}
	if !tr.importPublicationConfigured() {
		return snapshot, true
	}
	if !tr.importAssetPublished("script", snapshot.ID) {
		return nil, false
	}
	return tr.revalidatePublishedScriptSnapshot(snapshot)
}

func (tr *TestRunner) publishedScenarioSnapshot(snapshot *StoredScenario) (*StoredScenario, bool) {
	if snapshot == nil {
		return nil, false
	}
	if !tr.importPublicationConfigured() {
		return snapshot, true
	}
	if !tr.importAssetPublished("scenario", snapshot.ID) {
		return nil, false
	}
	return tr.revalidatePublishedScenarioSnapshot(snapshot)
}

// validateManagedTestImportPublication runs after the immutable build snapshot
// is captured but before runtime-secret provisioning, persistence, or launch
// mutation. It covers direct script requests and scripts inherited from a
// scenario. Revision-source groups intentionally use an already-created test's
// immutable runtime snapshot and do not depend on the mutable script library.
func (tr *TestRunner) validateManagedTestImportPublication(snapshot *TestRunner, req CreateTestRequest) error {
	pending, err := tr.pendingImportPublicationSnapshot()
	if err != nil {
		return fmt.Errorf("direct-import publication state is unavailable: %w", err)
	}
	if len(pending) == 0 {
		return nil
	}
	if scenarioID := strings.TrimSpace(req.ScenarioID); scenarioID != "" && importPublicationContains(pending, "scenario", scenarioID) {
		return fmt.Errorf("scenario not found: %s", scenarioID)
	}
	ids := make(map[string]struct{}, len(req.Groups)+1)
	if id := strings.TrimSpace(req.ScriptID); id != "" {
		ids[id] = struct{}{}
	}
	for _, group := range req.Groups {
		if group.RevisionSourceGroupIndex != nil && !group.RevisionNewGroup {
			continue
		}
		if id := strings.TrimSpace(group.ScriptID); id != "" {
			ids[id] = struct{}{}
		}
	}
	if snapshot != nil && strings.TrimSpace(req.ScenarioID) != "" {
		if scenario := snapshot.scenarios[strings.TrimSpace(req.ScenarioID)]; scenario != nil {
			for _, group := range scenario.Scripts {
				if id := strings.TrimSpace(group.ScriptID); id != "" {
					ids[id] = struct{}{}
				}
			}
		}
	}
	for id := range ids {
		if importPublicationContains(pending, "script", id) {
			return fmt.Errorf("script not found: %s", id)
		}
	}
	return nil
}
