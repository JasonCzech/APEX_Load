package api

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestLaunchVUserCapConflicts(t *testing.T) {
	test := &ManagedTest{ProjectID: "project-1", TotalVUsers: 20, Assignment: &GeneratorAssignmentSettings{PoolID: "pool-east"}}
	evidence := func(quota projectQuotaSnapshot) launchWindowEvidence {
		return launchWindowEvidence{projectQuota: projectQuotaEvidence{projectID: "project-1", poolID: "pool-east", quota: quota, loaded: true}}
	}
	codes := func(conflicts []LaunchConflict) string {
		out := make([]string, 0, len(conflicts))
		for _, conflict := range conflicts {
			out = append(out, conflict.Code)
		}
		return strings.Join(out, ",")
	}

	both := launchVUserCapConflicts(test, evidence(projectQuotaSnapshot{maxVUsers: 5, poolQuota: storage.PoolProjectQuota{MaxVUsers: 10}, hasPoolQuota: true}))
	if got := codes(both); got != "exceeds_project_vuser_cap,exceeds_pool_vuser_cap" {
		t.Fatalf("conflicts = %q", got)
	}
	if !strings.Contains(both[0].Message, "needs 20 VUsers") || !strings.Contains(both[0].Message, "allows 5 at a time") || both[0].Severity != "blocking" {
		t.Fatalf("project conflict = %#v", both[0])
	}
	// Runs within the caps, unset caps and missing quota evidence add nothing.
	if got := launchVUserCapConflicts(test, evidence(projectQuotaSnapshot{maxVUsers: 20, poolQuota: storage.PoolProjectQuota{MaxVUsers: 20}, hasPoolQuota: true})); len(got) != 0 {
		t.Fatalf("at the cap = %#v", got)
	}
	if got := launchVUserCapConflicts(test, evidence(projectQuotaSnapshot{})); len(got) != 0 {
		t.Fatalf("no caps = %#v", got)
	}
	if got := launchVUserCapConflicts(test, launchWindowEvidence{}); len(got) != 0 {
		t.Fatalf("no evidence = %#v", got)
	}
	// A run that has started is judged by the projected-capacity checks, so
	// lowering the cap never blocks changes (such as reductions) to it.
	running := *test
	running.Status = "RUNNING"
	if got := launchVUserCapConflicts(&running, evidence(projectQuotaSnapshot{maxVUsers: 5})); len(got) != 0 {
		t.Fatalf("running run = %#v", got)
	}
	// A plan run approved to burst the project quota may exceed it.
	approved := *test
	approved.ExecutionPlanApproval = &RunArchiveExecutionPlanApprovalSnapshot{ID: "approval", PlanID: "plan", PlanContentHash: "hash", Policy: "quota_burst", Status: "approved", ApprovedPeakVUsers: 20}
	if got := launchVUserCapConflicts(&approved, evidence(projectQuotaSnapshot{maxVUsers: 5})); len(got) != 0 {
		t.Fatalf("approved burst = %#v", got)
	}
	// The approval doesn't cover the pool quota.
	if got := codes(launchVUserCapConflicts(&approved, evidence(projectQuotaSnapshot{maxVUsers: 5, poolQuota: storage.PoolProjectQuota{MaxVUsers: 10}, hasPoolQuota: true}))); got != "exceeds_pool_vuser_cap" {
		t.Fatalf("approved burst over the pool quota = %q", got)
	}
}

func TestRunLargerThanProjectCapIsRefusedRatherThanQueued(t *testing.T) {
	store := &metricsStoreSpy{projectsByID: map[string]*storage.Project{
		"project-1": {ID: "project-1", Name: "Payments", Settings: storage.ProjectSettings{MaxVUsers: 5}},
	}}
	runner := NewTestRunner(10, NewHub(), store)
	storePreflightScript(runner, "script-over-cap", "project-1")

	preflight, err := runner.PreflightLaunch(LaunchPreflightRequest{CreateTestRequest: CreateTestRequest{Name: "Too big", Duration: "5m", ScriptID: "script-over-cap", VUsers: 20}})
	if err != nil {
		t.Fatalf("PreflightLaunch returned error: %v", err)
	}
	if preflight.StartNowAllowed || preflight.QueueAllowed || preflight.Decision != "blocked" {
		t.Fatalf("preflight = %#v, want blocked without a queue offer", preflight)
	}
	if len(preflight.BlockingConflicts) == 0 || preflight.BlockingConflicts[0].Code != "exceeds_project_vuser_cap" {
		t.Fatalf("blocking conflicts = %#v", preflight.BlockingConflicts)
	}

	created, err := runner.CreateTest(CreateTestRequest{Name: "Too big", Duration: "5m", ScriptID: "script-over-cap", VUsers: 20})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}
	err = runner.StartTest(created.ID)
	var conflict *LaunchWindowConflictError
	if !errors.As(err, &conflict) || len(conflict.Conflicts) == 0 || conflict.Conflicts[0].Code != "exceeds_project_vuser_cap" {
		t.Fatalf("StartTest error = %v, want the project cap conflict", err)
	}
	if current, _ := runner.GetTest(created.ID); current.Status == "PENDING" {
		t.Fatalf("an over-cap run was queued: %#v", current)
	}

	// A run that fits the cap still queues behind other runs.
	fits, err := runner.PreflightLaunch(LaunchPreflightRequest{CreateTestRequest: CreateTestRequest{Name: "Fits", Duration: "5m", ScriptID: "script-over-cap", VUsers: 5}})
	if err != nil || !fits.StartNowAllowed {
		t.Fatalf("fitting preflight = %#v %v", fits, err)
	}
}

func TestQueuedRunLargerThanTheCapSaysTheLimitMustBeRaised(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)
	test := &ManagedTest{ID: "queued", ProjectID: "project-1", Status: "PENDING", TotalVUsers: 20}
	runner.mu.Lock()
	blocked, reason := runner.evaluateQuotaBlockWithTimeslotsAndProjectQuotaLocked(test, "", time.Now(), nil, nil, projectQuotaSnapshot{maxVUsers: 5})
	runner.mu.Unlock()
	if !blocked || reason != "waiting for the project VUser limit to be raised: this run needs 20 VUsers and the limit is 5" {
		t.Fatalf("quota block = %v %q", blocked, reason)
	}
	// A run that fits keeps the ordinary capacity wording.
	small := &ManagedTest{ID: "small", ProjectID: "project-1", Status: "PENDING", TotalVUsers: 5}
	runner.mu.Lock()
	_, reason = runner.evaluateQuotaBlockWithTimeslotsAndProjectQuotaLocked(small, "", time.Now(), nil, nil, projectQuotaSnapshot{maxVUsers: 5})
	runner.mu.Unlock()
	if reason != "" {
		t.Fatalf("fitting run reason = %q", reason)
	}
}
