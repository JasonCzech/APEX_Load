package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/livetable"
	"github.com/jasonczech/apexload/pkg/storage"
)

func goalRunWithLiveTable(t *testing.T, strategy string) (*TestRunner, *ManagedTest, *livetable.Table) {
	t.Helper()
	table, err := livetable.New(livetable.Config{ID: "goal-run:work", RunID: "goal-run", Profile: livetable.ProfileNative, Columns: []string{"job_id"}})
	if err != nil {
		t.Fatal(err)
	}
	runner := NewTestRunner(1, nil, nil)
	runner.liveTablesMu.Lock()
	if runner.liveTables == nil {
		runner.liveTables = map[string]*runLiveTables{}
	}
	runner.liveTables["goal-run"] = &runLiveTables{tables: map[string]*runLiveTable{"goal-run:work": {
		table:   table,
		binding: storage.ScenarioDataPoolBinding{Alias: "work", AccessMode: "live", SelectionStrategy: strategy},
	}}}
	runner.liveTablesMu.Unlock()
	return runner, &ManagedTest{ID: "goal-run", goalControl: &goalControllerState{}}, table
}

func popEmpty(t *testing.T, table *livetable.Table, sequence uint64) {
	t.Helper()
	_, _ = table.Execute(context.Background(), livetable.Request{ClientID: "consumer", Sequence: sequence, Operation: livetable.OpPop})
}

func TestGoalScaleUpIsHeldWhileEveryIterationWaitsOnAnEmptyLiveTable(t *testing.T) {
	runner, test, table := goalRunWithLiveTable(t, "pop")
	// The first check only records how often the table has answered empty.
	popEmpty(t, table, 1)
	if runner.holdGoalScaleUpForLiveTables(test) {
		t.Fatal("held on the first check, before any new empty answer")
	}
	popEmpty(t, table, 2)
	if !runner.holdGoalScaleUpForLiveTables(test) {
		t.Fatal("scale-up was not held while consumers kept finding the table empty")
	}
	if note := test.goalControl.liveTableNote; !strings.Contains(note, "Holding VUsers") || !strings.Contains(note, "work") {
		t.Fatalf("note = %q", note)
	}
	// No new empty answers: the controller may add VUsers again.
	if runner.holdGoalScaleUpForLiveTables(test) {
		t.Fatal("still held once the table stopped answering empty")
	}
	if test.goalControl.liveTableNote != "" {
		t.Fatalf("note kept after starvation ended: %q", test.goalControl.liveTableNote)
	}
}

func TestGoalLiveTableNoteClearsWithinTheTargetBandAndHoldSuggestsNoVUsers(t *testing.T) {
	target := 20.0
	config := &GoalConfig{TargetAverageTPS: &target}
	state := &goalControllerState{liveTableNote: "Holding VUsers: …", liveTableHolding: true}
	short := evaluateGoalController(time.Now(), 10, &LiveMetrics{TPS: 10}, config, state)
	if short.Status.LiveTableNote == "" || short.Status.SuggestedTotalVUsers != 10 || !short.ShouldScale {
		t.Fatalf("while held: note %q, suggested %d, scale %v; want the note, no added VUsers suggested, and a scale-up to recheck", short.Status.LiveTableNote, short.Status.SuggestedTotalVUsers, short.ShouldScale)
	}
	state.lastScaleAt = nil
	within := evaluateGoalController(time.Now(), 10, &LiveMetrics{TPS: 20}, config, state)
	if within.Status.LiveTableNote != "" || state.liveTableNote != "" || state.liveTableHolding {
		t.Fatalf("within the target band the note stayed: status %q, state %q", within.Status.LiveTableNote, state.liveTableNote)
	}
}

func TestGoalScaleUpIsHeldForAnEmptyProjectLiveTable(t *testing.T) {
	table, err := livetable.New(livetable.Config{ID: "project-table", RunID: "project", Profile: livetable.ProfileNative, Columns: []string{"job_id"}})
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{})
	close(ready)
	runner := NewTestRunner(1, nil, nil)
	runner.liveTablesMu.Lock()
	if runner.liveTables == nil {
		runner.liveTables = map[string]*runLiveTables{}
	}
	if runner.projectLiveTables == nil {
		runner.projectLiveTables = map[string]*projectLiveTableRuntime{}
	}
	runner.projectLiveTables["instance-1"] = &projectLiveTableRuntime{ready: ready, instanceID: "instance-1", table: table}
	runner.liveTables["goal-run"] = &runLiveTables{
		projectAttachments: map[string]storage.ProjectLiveTableAttachment{"a1": {ID: "a1", InstanceID: "instance-1", Alias: "jobs", Group: "consumers"}},
		groupBindings:      map[string][]engine.LiveTableBinding{"consumers": {{Alias: "jobs", Strategy: "lease"}}},
	}
	runner.liveTablesMu.Unlock()
	test := &ManagedTest{ID: "goal-run", goalControl: &goalControllerState{}}
	popEmpty(t, table, 1)
	runner.holdGoalScaleUpForLiveTables(test)
	popEmpty(t, table, 2)
	if !runner.holdGoalScaleUpForLiveTables(test) || !strings.Contains(test.goalControl.liveTableNote, "jobs") {
		t.Fatalf("an empty project table did not hold scale-up: note %q", test.goalControl.liveTableNote)
	}
}

func TestGoalScaleUpExplainsButIsNotHeldForAManualLiveTable(t *testing.T) {
	runner, test, table := goalRunWithLiveTable(t, "manual")
	popEmpty(t, table, 1)
	runner.holdGoalScaleUpForLiveTables(test)
	popEmpty(t, table, 2)
	if runner.holdGoalScaleUpForLiveTables(test) {
		t.Fatal("held for a manual table, whose producers more VUsers may help")
	}
	if !strings.Contains(test.goalControl.liveTableNote, "Consumers are waiting on the empty live table work") {
		t.Fatalf("note = %q", test.goalControl.liveTableNote)
	}
	if runner.holdGoalScaleUpForLiveTables(&ManagedTest{ID: "no-live-tables", goalControl: &goalControllerState{}}) {
		t.Fatal("held for a run without live tables")
	}
}
