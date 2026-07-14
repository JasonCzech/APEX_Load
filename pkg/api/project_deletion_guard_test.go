package api

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestProjectDeletionTombstoneBlocksStartsAndAssetCreation(t *testing.T) {
	runner := NewTestRunner(2, NewHub(), nil)
	backend := &lifecycleTransitionBackend{}
	runner.setExecutionBackend(backend)
	test := &ManagedTest{ID: "queued-project-delete", Name: "queued-project-delete", ProjectID: "project-delete", Status: "QUEUED"}
	runner.tests[test.ID] = test

	if err := runner.beginProjectDeletion("project-delete"); err != nil {
		t.Fatalf("beginProjectDeletion returned error: %v", err)
	}
	defer runner.endProjectDeletion("project-delete")
	if err := runner.StartTest(test.ID); err == nil || !strings.Contains(err.Error(), "being deleted") {
		t.Fatalf("StartTest error = %v, want project deletion conflict", err)
	}
	if _, err := runner.StoreScriptDurable(&dsl.Script{ID: "script-project-delete", Name: "blocked", Protocol: "http", ProjectID: "project-delete"}); err == nil || !strings.Contains(err.Error(), "being deleted") {
		t.Fatalf("StoreScriptDurable error = %v, want project deletion conflict", err)
	}
	if runs, _ := backend.counts(); runs != 0 {
		t.Fatalf("backend run calls = %d, want 0", runs)
	}
}

func TestProjectDeletionTombstoneRejectsActiveProject(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	runner.tests["active-project-delete"] = &ManagedTest{ID: "active-project-delete", ProjectID: "project-delete", Status: "SHAKEOUT"}
	if err := runner.beginProjectDeletion("project-delete"); err == nil || !strings.Contains(err.Error(), "active tests") {
		t.Fatalf("beginProjectDeletion error = %v, want active-test conflict", err)
	}
	if _, exists := runner.deletingProjects["project-delete"]; exists {
		t.Fatal("failed project deletion left a tombstone installed")
	}
}

func TestDeleteTestDurableRejectsShakeout(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	test := &ManagedTest{ID: "shakeout-delete", Status: "SHAKEOUT"}
	runner.tests[test.ID] = test
	deleted, err := runner.DeleteTestDurable(test.ID)
	if err != nil {
		t.Fatalf("DeleteTestDurable returned error: %v", err)
	}
	if deleted {
		t.Fatal("DeleteTestDurable deleted an active shakeout")
	}
	if _, ok := runner.tests[test.ID]; !ok {
		t.Fatal("shakeout test was removed from memory")
	}
}
