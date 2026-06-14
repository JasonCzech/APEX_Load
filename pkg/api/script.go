package api

// Script persistence + draft replay: CRUD for stored scripts (store, get,
// list/filter, move, update, delete, usage) plus draft replay entry points and
// summary enrichment. Extracted from runner.go; methods hang off *TestRunner.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/refkey"
)

func (tr *TestRunner) StoreScript(script *dsl.Script) *StoredScript {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	if script.ID == "" {
		script.ID = uuid.New().String()
	}
	script.ProjectID = normalizeProjectID(script.ProjectID)
	if strings.TrimSpace(script.Version) == "" {
		script.Version = "1"
	}
	if script.RefKey == "" {
		script.RefKey = refkey.Script(tr.envCode, tr.nextScriptSeqLocked(), 1)
	}

	stored := &StoredScript{
		ID:              script.ID,
		RefKey:          script.RefKey,
		Name:            script.Name,
		Protocol:        script.Protocol,
		Version:         script.Version,
		ProjectID:       script.ProjectID,
		FolderID:        script.FolderID,
		CreatedAt:       time.Now(),
		AuthoringHealth: buildStoredScriptAuthoringHealth(script),
		Script:          script,
	}

	tr.saveStoredScript(script)

	tr.scripts[stored.ID] = stored
	tr.observeAndIndexRefKeyLocked(stored.RefKey, stored.ID)
	return stored
}

func (tr *TestRunner) GetScript(id string) (*StoredScript, bool) {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	id = tr.resolveRefKeyLocked(id)
	s, ok := tr.scripts[id]
	return s, ok
}

func (tr *TestRunner) ReplayScript(id string, opts engine.ReplayOptions) (*engine.ReplayReport, error) {
	tr.mu.RLock()
	id = tr.resolveRefKeyLocked(id)
	stored, ok := tr.scripts[id]
	tr.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("script not found")
	}
	return tr.ReplayScriptDefinition(stored.Script, opts)
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
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	out := make([]*StoredScript, 0, len(tr.scripts))
	for _, s := range tr.scripts {
		out = append(out, s)
	}
	return out
}

func (tr *TestRunner) ListScriptsFiltered(projectID string, folderID *string) []*StoredScript {
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
	tr.mu.Lock()
	defer tr.mu.Unlock()
	id = tr.resolveRefKeyLocked(id)
	script, ok := tr.scripts[id]
	if !ok {
		return false
	}
	script.FolderID = folderID
	if script.Script != nil {
		script.Script.FolderID = folderID
	}
	if tr.scriptStore != nil {
		_ = tr.scriptStore.MoveScript(id, folderID)
	}
	return true
}

func (tr *TestRunner) UpdateScript(id string, script *dsl.Script) (*StoredScript, bool) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	id = tr.resolveRefKeyLocked(id)
	existing, ok := tr.scripts[id]
	if !ok {
		return nil, false
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
		script.RefKey = refkey.Script(tr.envCode, tr.nextScriptSeqLocked(), 1)
	}
	existing.Name = script.Name
	existing.Protocol = script.Protocol
	existing.Version = script.Version
	existing.RefKey = script.RefKey
	if script.ProjectID == "" {
		script.ProjectID = existing.ProjectID
	}
	if script.FolderID == nil {
		script.FolderID = existing.FolderID
	}
	if script.CreatedAt.IsZero() && existing.Script != nil {
		script.CreatedAt = existing.Script.CreatedAt
	}
	existing.ProjectID = script.ProjectID
	existing.FolderID = script.FolderID
	existing.AuthoringHealth = buildStoredScriptAuthoringHealth(script)
	existing.Script = script
	tr.saveStoredScript(script)
	tr.removeRefKeyLocked(oldRefKey)
	tr.indexRefKeyLocked(existing.RefKey, existing.ID)
	return existing, true
}

func (tr *TestRunner) DeleteScript(id string) bool {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	id = tr.resolveRefKeyLocked(id)
	script, ok := tr.scripts[id]
	if !ok {
		return false
	}
	tr.removeRefKeyLocked(script.RefKey)
	delete(tr.scripts, id)
	tr.deleteStoredScript(id)
	return true
}

func (tr *TestRunner) ListScriptsWithUsage() []*StoredScript {
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
	copy := *script
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
	return &copy
}
