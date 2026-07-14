package api

import (
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
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
