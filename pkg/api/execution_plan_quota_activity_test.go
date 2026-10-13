package api

import (
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

// A plan over its project's VUser limit says so in its activity, so Run can
// warn before asking; once an approval is granted it says one is ready, and
// the approval keeps why it was asked for beside the decision's reason.
func TestExecutionPlanActivityReportsQuotaAndApprovalKeepsReason(t *testing.T) {
	server, store := newTimeslotServer(t)
	if err := store.SaveProject(&storage.Project{ID: "proj-a", Name: "A", Settings: storage.ProjectSettings{MaxVUsers: 5}, CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	server.runner.StoreScript(&dsl.Script{ID: "s-quota", Name: "S", Protocol: "http", Version: "1.0.0", ProjectID: "proj-a", Config: dsl.ScriptConfig{BaseURL: "https://example.com"}, Actions: []dsl.Action{{Name: "a", Type: "request", Target: "/"}}})
	scenario, err := server.runner.StoreScenario(CreateScenarioRequest{Name: "Peak", Duration: "5m", Groups: []CreateGroupEntry{{ScriptID: "s-quota", VUsers: 4}}})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
	plan := &storage.ExecutionPlan{ID: "plan-quota", ProjectID: "proj-a", Name: "Quota", RunMode: "parallel", Entries: []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: scenario.ID}, {Order: 1, ScenarioID: scenario.ID}}}
	if err := store.SaveExecutionPlan(plan); err != nil {
		t.Fatalf("SaveExecutionPlan: %v", err)
	}
	activity, err := server.runner.ExecutionPlanActivityFor(plan)
	if err != nil || activity.Quota == nil || activity.Quota.PeakVUsers != 8 || activity.Quota.MaxVUsers != 5 || activity.Quota.ReadyApprovalID != "" {
		t.Fatalf("activity quota = %+v (err %v), want peak 8 over limit 5, no approval ready", activity.Quota, err)
	}
	res, err := server.runner.MaterializeAndRunExecutionPlan(plan, "user:alice", "Alice")
	if err != nil || !res.Blocked || res.Approval == nil {
		t.Fatalf("run = %+v (err %v), want an approval block", res, err)
	}
	approved, ok, err := server.runner.ResolveExecutionPlanApproval(plan.ID, res.Approval.ID, true, "user:admin", "release window")
	if err != nil || !ok {
		t.Fatalf("resolve: ok=%v err=%v", ok, err)
	}
	if !strings.HasPrefix(approved.DecisionReason, "Aggregate peak 8 VUsers exceeds project limit 5") || !strings.HasSuffix(approved.DecisionReason, " Decision: release window") {
		t.Fatalf("decision reason = %q, want the request reason then the decision", approved.DecisionReason)
	}
	activity, err = server.runner.ExecutionPlanActivityFor(plan)
	if err != nil || activity.Quota == nil || activity.Quota.ReadyApprovalID != approved.ID {
		t.Fatalf("activity quota = %+v (err %v), want approval %s ready", activity.Quota, err, approved.ID)
	}

	// A plan within the limit reports no quota position.
	small := &storage.ExecutionPlan{ID: "plan-small", ProjectID: "proj-a", Name: "Small", RunMode: "sequential", Entries: []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: scenario.ID}, {Order: 1, ScenarioID: scenario.ID}}}
	if err := store.SaveExecutionPlan(small); err != nil {
		t.Fatalf("SaveExecutionPlan: %v", err)
	}
	if activity, err := server.runner.ExecutionPlanActivityFor(small); err != nil || activity.Quota != nil {
		t.Fatalf("sequential plan quota = %+v (err %v), want none (its peak is 4)", activity.Quota, err)
	}
	if got := resolvedExecutionPlanApprovalReason("why", ""); got != "why" {
		t.Fatalf("reason without a decision = %q", got)
	}
}
