package api

import (
	"context"
	"errors"
	"github.com/jasonczech/apexload/pkg/distributed/rendezvous"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
	"testing"
	"time"
)

func rendezvousTestGroup() TestGroup {
	return TestGroup{Name: "workers", VUsers: 4, script: &dsl.Script{Actions: []dsl.Action{{Type: "rendezvous", Params: map[string]any{"scope": "distributed_group", "key": "checkout", "timeout_ms": 2000, "on_timeout": "fail"}}}}}
}
func TestDistributedRendezvousControllerRestoreAndShardAuthority(t *testing.T) {
	runner, store, _ := newAdoptionTestRunner(t)
	ctx := context.Background()
	group := rendezvousTestGroup()
	test := &ManagedTest{ID: "rendezvous-run", ProjectID: storage.DefaultProjectID, Name: "Rendezvous", Status: "INITIALIZING", Groups: []TestGroup{group}}
	if err := store.SaveTest(&storage.TestRecord{ID: test.ID, ProjectID: test.ProjectID, Name: test.Name, Status: test.Status, StartTime: time.Now(), Groups: []any{}}); err != nil {
		t.Fatal(err)
	}
	runner.mu.Lock()
	runner.tests[test.ID] = test
	runner.mu.Unlock()
	term, err := runner.captureExecutionLeaseTerm()
	if err != nil {
		t.Fatal(err)
	}
	shards := []GeneratorShardSummary{{Group: group.Name, Target: "a", VUsers: 2, VUserIDOffset: 0}, {Group: group.Name, Target: "b", VUsers: 2, VUserIDOffset: 100}}
	if err = runner.prepareGroupRendezvous(ctx, test, group, shards, false, term); err != nil {
		t.Fatal(err)
	}
	a := runner.rendezvousClient(test.ID, group.Name, []rendezvous.MemberRange{{Count: 2}})
	// A delayed defer from a prior owner must not close this owner's actor.
	runner.dropRunRendezvous(test.ID, executionLeaseTerm{ownerID: "obsolete", generation: term.generation})
	if _, err = a.Exchange(ctx, rendezvous.Batch{Key: "checkout", Round: 1, Arrivals: []uint64{100}}); !errors.Is(err, rendezvous.ErrInvalid) {
		t.Fatalf("cross-shard impersonation %v", err)
	}
	first, err := a.Exchange(ctx, rendezvous.Batch{Key: "checkout", Round: 1, Arrivals: []uint64{0, 1}})
	if err != nil || first.Arrived != 2 || first.State != "waiting" {
		t.Fatalf("arrival %+v %v", first, err)
	}
	runner.dropRunRendezvous(test.ID, term)
	if _, err = a.Exchange(ctx, rendezvous.Batch{Key: "checkout", Round: 1}); !errors.Is(err, rendezvous.ErrUnavailable) {
		t.Fatal("old core still serving", err)
	}
	if err = runner.restoreRendezvous(ctx, test, map[string][]GeneratorShardSummary{group.Name: shards}, term); err != nil {
		t.Fatal(err)
	}
	b := runner.rendezvousClient(test.ID, group.Name, []rendezvous.MemberRange{{First: 100, Count: 2}})
	final, err := b.Exchange(ctx, rendezvous.Batch{Key: "checkout", Round: 1, Arrivals: []uint64{100, 101}})
	if err != nil || final.State != "released" || final.Arrived != 4 {
		t.Fatalf("restored release %+v %v", final, err)
	}
	runner.dropRunRendezvous(test.ID, term)
	if err = runner.restoreRendezvous(ctx, test, map[string][]GeneratorShardSummary{group.Name: shards}, term); err != nil {
		t.Fatal(err)
	}
	late, err := runner.localRendezvousClient(test.ID, group.Name).Exchange(ctx, rendezvous.Batch{Key: "checkout", Round: 1, Arrivals: []uint64{0}})
	if err != nil || late != final {
		t.Fatalf("released outcome changed %+v %v", late, err)
	}
	runner.dropRunRendezvous(test.ID, term)
	shards[1].VUserIDOffset = 200
	if err = runner.restoreRendezvous(ctx, test, map[string][]GeneratorShardSummary{group.Name: shards}, term); err == nil {
		t.Fatal("replacement rewrote frozen participant IDs")
	}
	shards[1].VUserIDOffset = 100
	second := rendezvousTestGroup()
	second.Name = "missing-checkpoint"
	test.Groups = append(test.Groups, second)
	if err = runner.restoreRendezvous(ctx, test, map[string][]GeneratorShardSummary{group.Name: shards}, term); err == nil {
		t.Fatal("partially restored run accepted a missing group")
	}
	if runner.rendezvousRuntime(test.ID, group.Name) != nil {
		t.Fatal("failed adoption retained its coordinator reservation")
	}
}
func TestDistributedRendezvousRejectsDynamicPopulation(t *testing.T) {
	for _, kind := range []string{"goal", "stages", "open", "zero", "over_target"} {
		t.Run(kind, func(t *testing.T) {
			test := &ManagedTest{}
			group := rendezvousTestGroup()
			switch kind {
			case "goal":
				test.ScenarioMode = "goal_oriented"
			case "stages":
				group.Stages = []storage.LoadStage{{TargetVUsers: 1}}
			case "open":
				group.script.Config.RunLogic = dsl.RunLogicConfig{WorkloadModel: "open"}
			case "zero":
				group.VUsers = 0
			case "over_target":
				group.script.Actions[0].Params["count"] = 5
			}
			if _, err := distributedRendezvousContracts(test, group); err == nil {
				t.Fatal("unsafe population admitted")
			}
		})
	}
}
