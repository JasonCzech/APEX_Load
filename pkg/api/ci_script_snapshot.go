package api

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

const (
	ciInlineScriptGenerator  = "github-actions-ci-inline"
	ciLibraryScriptGenerator = "github-actions-ci-library-snapshot"
)

func isCIScriptSnapshot(script *dsl.Script) bool {
	if script == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(script.GeneratedBy)) {
	case ciInlineScriptGenerator, ciLibraryScriptGenerator:
		return true
	default:
		return false
	}
}

func isStoredCIScriptSnapshot(script *StoredScript) bool {
	return script != nil && isCIScriptSnapshot(script.Script)
}

// acquirePrivateCIScriptSnapshot makes an approved CI script available only to
// the synchronous test compiler. It is never written to the persistent script
// library, and public script reads deliberately hide it. A reference count keeps
// concurrent users from removing the same private snapshot too early.
func (tr *TestRunner) acquirePrivateCIScriptSnapshot(script *dsl.Script) (*StoredScript, error) {
	if tr == nil || script == nil || strings.TrimSpace(script.ID) == "" || !isCIScriptSnapshot(script) {
		return nil, fmt.Errorf("private CI script snapshot requires an id and reserved generator")
	}
	script = cloneScriptDefinition(script)
	script.ID = strings.TrimSpace(script.ID)
	script.ProjectID = normalizeProjectID(strings.TrimSpace(script.ProjectID))
	script.RefKey = ""
	script.FolderID = nil

	for {
		tr.scriptMutationMu.Lock()
		tr.mu.Lock()
		existing := tr.scripts[script.ID]
		if references := tr.privateCISnapshots[script.ID]; references > 0 {
			if !isStoredCIScriptSnapshot(existing) || !equivalentCreateOnlyScript(existing.Script, script) {
				tr.mu.Unlock()
				tr.scriptMutationMu.Unlock()
				return nil, fmt.Errorf("private CI script snapshot %q is already bound to different content", script.ID)
			}
			tr.privateCISnapshots[script.ID] = references + 1
			stored := cloneStoredScriptForReadLocked(existing)
			tr.mu.Unlock()
			tr.scriptMutationMu.Unlock()
			return stored, nil
		}
		if existing == nil {
			now := time.Now().UTC()
			stored := &StoredScript{
				ID: script.ID, Name: script.Name, Protocol: script.Protocol, Version: script.Version,
				ProjectID: script.ProjectID, CreatedAt: now,
				AuthoringHealth: buildStoredScriptAuthoringHealth(script), Script: script,
			}
			tr.scripts[script.ID] = stored
			tr.privateCISnapshots[script.ID] = 1
			tr.contentRevision++
			result := cloneStoredScriptForReadLocked(stored)
			tr.mu.Unlock()
			tr.scriptMutationMu.Unlock()
			return result, nil
		}
		if !isStoredCIScriptSnapshot(existing) {
			tr.mu.Unlock()
			tr.scriptMutationMu.Unlock()
			return nil, fmt.Errorf("private CI script snapshot id %q collides with a persistent library script", script.ID)
		}
		// Older controller versions persisted these temporary snapshots. Remove
		// the exact durable version before installing the private replacement.
		projectID := existing.ProjectID
		tr.mu.Unlock()
		tr.scriptMutationMu.Unlock()
		deleted, err := tr.DeleteScriptDurableInProject(script.ID, projectID)
		if err != nil {
			return nil, fmt.Errorf("remove leaked CI script snapshot %q: %w", script.ID, err)
		}
		if !deleted {
			return nil, fmt.Errorf("remove leaked CI script snapshot %q: script changed concurrently", script.ID)
		}
	}
}

func (tr *TestRunner) releasePrivateCIScriptSnapshot(id string) {
	if tr == nil {
		return
	}
	id = strings.TrimSpace(id)
	tr.scriptMutationMu.Lock()
	defer tr.scriptMutationMu.Unlock()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	references := tr.privateCISnapshots[id]
	if references <= 1 {
		delete(tr.privateCISnapshots, id)
		if isStoredCIScriptSnapshot(tr.scripts[id]) {
			delete(tr.scripts, id)
			tr.contentRevision++
		}
		return
	}
	tr.privateCISnapshots[id] = references - 1
}

// cleanupLeakedCIScriptSnapshots removes durable temporary rows left by a
// pre-private-snapshot controller crash. Tests already contain their compiled
// script snapshot in groups_json, so deletion does not compromise queued-run
// recovery. Callers run this after stored tests have been hydrated.
func (tr *TestRunner) cleanupLeakedCIScriptSnapshots() error {
	if tr == nil {
		return nil
	}
	type leakedSnapshot struct {
		id        string
		projectID string
	}
	tr.mu.RLock()
	leaked := make([]leakedSnapshot, 0)
	for id, script := range tr.scripts {
		if tr.privateCISnapshots[id] == 0 && isStoredCIScriptSnapshot(script) {
			leaked = append(leaked, leakedSnapshot{id: id, projectID: script.ProjectID})
		}
	}
	tr.mu.RUnlock()
	var cleanupErr error
	for _, snapshot := range leaked {
		deleted, err := tr.DeleteScriptDurableInProject(snapshot.id, snapshot.projectID)
		if err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("delete %s: %w", snapshot.id, err))
			continue
		}
		if !deleted {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("delete %s: snapshot was not removed", snapshot.id))
		}
	}
	return cleanupErr
}
