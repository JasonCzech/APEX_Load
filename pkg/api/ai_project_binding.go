package api

import (
	"fmt"
	"strings"
)

func (s *Server) bindAIDraftProject(req *AIDraftRequest) (string, error) {
	if req == nil {
		return normalizeProjectID(""), nil
	}
	candidates := []string{req.ProjectID}
	if id := strings.TrimSpace(req.ExistingScriptID); id != "" {
		stored, ok := s.runner.GetScript(id)
		if !ok || stored == nil || stored.Script == nil {
			return "", fmt.Errorf("existing script not found")
		}
		candidates = append(candidates, stored.ProjectID, stored.Script.ProjectID)
	}
	projectID, err := reconcileProjectIDCandidates(candidates...)
	if err != nil {
		return "", fmt.Errorf("AI draft project mismatch: %w", err)
	}
	projectID = normalizeProjectID(projectID)
	req.ProjectID = projectID
	return projectID, nil
}

func (s *Server) bindAIRepairProject(req *AIRepairRequest, resolvedScriptProjectID string) (string, error) {
	if req == nil {
		return normalizeProjectID(resolvedScriptProjectID), nil
	}
	candidates := []string{req.ProjectID, resolvedScriptProjectID}
	if req.Script != nil {
		candidates = append(candidates, req.Script.ProjectID)
	}
	if id := strings.TrimSpace(req.ScriptID); id != "" {
		stored, ok := s.runner.GetScript(id)
		if !ok || stored == nil || stored.Script == nil {
			return "", fmt.Errorf("script not found")
		}
		candidates = append(candidates, stored.ProjectID, stored.Script.ProjectID)
	}
	if id := strings.TrimSpace(req.SessionID); id != "" {
		session, err := s.loadAuthoringSession(id)
		if err != nil || session == nil || session.Draft == nil {
			return "", fmt.Errorf("authoring session draft not found")
		}
		candidates = append(candidates, session.ProjectID, session.Draft.ProjectID)
	}
	projectID, err := reconcileProjectIDCandidates(candidates...)
	if err != nil {
		return "", fmt.Errorf("AI repair project mismatch: %w", err)
	}
	projectID = normalizeProjectID(projectID)
	req.ProjectID = projectID
	return projectID, nil
}

func (s *Server) bindAIExplainProject(req *AIExplainRequest) (string, error) {
	if req == nil {
		return normalizeProjectID(""), nil
	}
	candidates := []string{req.ProjectID}
	if req.Script != nil {
		candidates = append(candidates, req.Script.ProjectID)
	}
	if req.Scenario != nil && len(req.Scenario.Groups) > 0 {
		projectID, err := s.scenarioRequestProjectID(*req.Scenario)
		if err != nil {
			return "", err
		}
		candidates = append(candidates, projectID)
	}
	targetID := strings.TrimSpace(req.TargetID)
	if targetID != "" {
		switch normalizeAITargetType(req.TargetType) {
		case "script":
			stored, ok := s.runner.GetScript(targetID)
			if !ok || stored == nil || stored.Script == nil {
				return "", fmt.Errorf("script not found")
			}
			candidates = append(candidates, stored.ProjectID, stored.Script.ProjectID)
		case "scenario":
			scenario, ok := s.runner.GetScenario(targetID)
			if !ok || scenario == nil {
				return "", fmt.Errorf("scenario not found")
			}
			candidates = append(candidates, scenario.ProjectID)
		case "report":
			test, ok := s.runner.GetTest(targetID)
			if !ok || test == nil {
				return "", fmt.Errorf("test not found")
			}
			candidates = append(candidates, test.ProjectID)
		}
	}
	projectID, err := reconcileProjectIDCandidates(candidates...)
	if err != nil {
		return "", fmt.Errorf("AI explanation project mismatch: %w", err)
	}
	projectID = normalizeProjectID(projectID)
	req.ProjectID = projectID
	return projectID, nil
}
