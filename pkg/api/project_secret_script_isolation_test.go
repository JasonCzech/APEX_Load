package api

import (
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
)

func TestMissingProjectSecretBlocksOnlyReferencingScript(t *testing.T) {
	const (
		projectID  = "project-secret-script-isolation"
		secretName = "ISOLATION_TOKEN"
	)
	t.Setenv(engine.ProjectSecretEnvironmentName(projectID, secretName), "")

	runner := NewTestRunner(1, NewHub(), nil)
	backend := &executionBackendSpy{called: make(chan string, 1)}
	runner.setExecutionBackend(backend)

	secretScript := &dsl.Script{
		ID:        "script-with-project-secret",
		ProjectID: projectID,
		Name:      "Script requiring a project secret",
		Protocol:  "http",
		Version:   "1.0.0",
		Config: dsl.ScriptConfig{
			BaseURL: "https://safe.example.test",
			RunLogic: dsl.RunLogicConfig{
				Iterations: 1,
			},
		},
		Actions: []dsl.Action{{
			Name:    "authenticated request",
			Type:    "GET",
			Target:  "/authenticated",
			Headers: map[string]string{"Authorization": "Bearer {{secret.ISOLATION_TOKEN}}"},
		}},
	}
	cleanScript := &dsl.Script{
		ID:        "script-without-project-secret",
		ProjectID: projectID,
		Name:      "Independent script",
		Protocol:  "http",
		Version:   "1.0.0",
		Config: dsl.ScriptConfig{
			BaseURL: "https://safe.example.test",
			RunLogic: dsl.RunLogicConfig{
				Iterations: 1,
			},
		},
		Actions: []dsl.Action{{
			Name:   "public request",
			Type:   "GET",
			Target: "/public",
		}},
	}
	for _, script := range []*dsl.Script{secretScript, cleanScript} {
		if _, err := runner.StoreScriptDurable(script); err != nil {
			t.Fatalf("StoreScriptDurable(%s) rejected an unresolved logical reference: %v", script.ID, err)
		}
	}

	blockedRun, err := runner.CreateTest(CreateTestRequest{
		Name:      "Run requiring missing project secret",
		ProjectID: projectID,
		Duration:  "30s",
		ScriptID:  secretScript.ID,
		VUsers:    1,
	})
	if err != nil {
		t.Fatalf("CreateTest(secret script): %v", err)
	}
	cleanRun, err := runner.CreateTest(CreateTestRequest{
		Name:      "Run independent of missing project secret",
		ProjectID: projectID,
		Duration:  "30s",
		ScriptID:  cleanScript.ID,
		VUsers:    1,
	})
	if err != nil {
		t.Fatalf("CreateTest(clean script): %v", err)
	}

	err = runner.StartTest(blockedRun.ID)
	if err == nil || !strings.Contains(err.Error(), secretName) || !strings.Contains(err.Error(), "configure") {
		t.Fatalf("StartTest(secret script) error = %v, want script-specific unresolved-secret error", err)
	}
	select {
	case id := <-backend.called:
		t.Fatalf("backend launched missing-secret run %q", id)
	default:
	}
	for _, script := range []*dsl.Script{secretScript, cleanScript} {
		stored, ok := runner.GetScript(script.ID)
		if !ok || stored == nil || stored.Script == nil {
			t.Fatalf("failed launch removed saved script %q: %#v", script.ID, stored)
		}
	}

	if err := runner.StartTest(cleanRun.ID); err != nil {
		t.Fatalf("StartTest(clean script) was affected by another script's missing project secret: %v", err)
	}
	select {
	case id := <-backend.called:
		if id != cleanRun.ID {
			t.Fatalf("backend launched %q, want independent run %q", id, cleanRun.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("independent script did not launch")
	}
}
