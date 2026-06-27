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
