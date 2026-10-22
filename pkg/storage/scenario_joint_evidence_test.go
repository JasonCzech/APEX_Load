package storage

import (
	"reflect"
	"testing"
)

func TestScenarioJointEvidenceIsAuthoredContent(t *testing.T) {
	exerciseScenarioJointEvidence(t, newScenarioRevisionTestStore(t))
}

// exerciseScenarioJointEvidence: a scenario's joint evidence projection is
// stored with it, read back by every reader, and is authored content: a
// change to it alone mints a new content revision.
func exerciseScenarioJointEvidence(t *testing.T, store *SQLiteStore) {
	old := seedScenarioRevision(t, store)
	projection := map[string]any{"population": "action", "dimensions": []any{"name", "group"}}
	candidate := *old.Scenario
	candidate.JointEvidence = projection
	result, err := store.UpdateScenarioAuthoringIfCurrent(&candidate, old.Scripts, old.DataPools, scenarioAuthoringExpected(old))
	if err != nil || !result.Applied {
		t.Fatalf("save: %#v %v", result, err)
	}
	current := requireScenarioAuthoring(t, store, old.Scenario.ID)
	if current.ContentRevision == old.ContentRevision || !reflect.DeepEqual(current.Scenario.JointEvidence, projection) {
		t.Fatalf("authoring snapshot: revision %q (was %q), projection %#v", current.ContentRevision, old.ContentRevision, current.Scenario.JointEvidence)
	}
	read, _, err := store.GetScenario(old.Scenario.ID)
	if err != nil || !reflect.DeepEqual(read.JointEvidence, projection) {
		t.Fatalf("GetScenario: %#v %v", read, err)
	}
	listed, err := store.ListScenarios()
	if err != nil || len(listed) == 0 || !reflect.DeepEqual(listed[0].JointEvidence, projection) {
		t.Fatalf("ListScenarios: %v", err)
	}
	// A direct write of the column alone is an authored change too.
	if _, err := store.db.Exec(`UPDATE scenarios SET joint_evidence_json=? WHERE id=?`, `{"population":"transaction","dimensions":["group"]}`, old.Scenario.ID); err != nil {
		t.Fatal(err)
	}
	if bumped := requireScenarioAuthoring(t, store, old.Scenario.ID); bumped.ContentRevision == current.ContentRevision {
		t.Fatal("changing only the joint evidence kept the content revision")
	}
}
