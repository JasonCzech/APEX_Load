package api

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func bindDraftScriptProject(script *dsl.Script, projectID string) error {
	if script == nil {
		return nil
	}
	requestProjectID := strings.TrimSpace(projectID)
	scriptProjectID := strings.TrimSpace(script.ProjectID)
	if requestProjectID != "" && scriptProjectID != "" && normalizeProjectID(requestProjectID) != normalizeProjectID(scriptProjectID) {
		return fmt.Errorf("project_id does not match script.project_id")
	}
	if requestProjectID != "" {
		script.ProjectID = normalizeProjectID(requestProjectID)
		return nil
	}
	script.ProjectID = normalizeProjectID(scriptProjectID)
	return nil
}

func bindStoredScriptProject(script *dsl.Script, storedProjectID string, projectID string) error {
	if script == nil {
		return nil
	}
	boundProjectID := normalizeProjectID(strings.TrimSpace(storedProjectID))
	requestProjectID := strings.TrimSpace(projectID)
	scriptProjectID := strings.TrimSpace(script.ProjectID)
	if requestProjectID != "" && normalizeProjectID(requestProjectID) != boundProjectID {
		return fmt.Errorf("project_id does not match stored script project")
	}
	if scriptProjectID != "" && normalizeProjectID(scriptProjectID) != boundProjectID {
		return fmt.Errorf("script.project_id does not match stored script project")
	}
	script.ProjectID = boundProjectID
	return nil
}

// validateScriptFolderProject keeps the folder relationship inside the same
// authorization boundary as the script. Route-level resolution is not enough:
// update payloads can carry a folder_id that is different from the resource
// used by middleware, and global callers do not receive a bound-project value.
func validateScriptFolderProject(store storage.Store, script *dsl.Script) error {
	if store == nil || script == nil || script.FolderID == nil {
		return nil
	}
	folderID := strings.TrimSpace(*script.FolderID)
	if folderID == "" {
		return fmt.Errorf("folder_id must be a non-empty string or null")
	}
	folder, err := store.GetFolder(folderID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("script folder not found")
		}
		return fmt.Errorf("load script folder: %w", err)
	}
	if normalizeProjectID(folder.ProjectID) != normalizeProjectID(script.ProjectID) {
		return fmt.Errorf("script folder belongs to a different project")
	}
	return nil
}

// reconcileProjectIDCandidates returns the common normalized project for all
// non-empty candidates. Callers provide IDs derived from request fields and
// referenced stored objects; a mismatch must be rejected before any referenced
// content is read or promoted.
func reconcileProjectIDCandidates(candidates ...string) (string, error) {
	projectID := ""
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		candidate = normalizeProjectID(candidate)
		if projectID != "" && candidate != projectID {
			return "", fmt.Errorf("referenced resources belong to different projects")
		}
		projectID = candidate
	}
	return projectID, nil
}
