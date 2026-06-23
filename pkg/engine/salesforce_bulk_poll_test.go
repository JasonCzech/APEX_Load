package engine

import (
	"context"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// bulkStatusProtocol simulates a Salesforce Bulk API 2.0 job-status endpoint: it
// returns state "InProgress" for the first completeAfter polls, then
// "JobComplete", driving the poll loop the importer synthesizes.
type bulkStatusProtocol struct {
	calls         int
	completeAfter int
}

func (p *bulkStatusProtocol) Name() string                               { return "http" }
func (p *bulkStatusProtocol) Init(context.Context, map[string]any) error { return nil }
func (p *bulkStatusProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	p.calls++
	state := "InProgress"
	if p.calls > p.completeAfter {
		state = "JobComplete"
	}
	return &protocol.Result{
		ActionName:  action.Name,
		Success:     true,
		Duration:    time.Millisecond,
		Extractions: map[string]string{"bulk_job_state": state},
	}, nil
}
func (p *bulkStatusProtocol) Teardown(context.Context) error    { return nil }
func (p *bulkStatusProtocol) HealthCheck(context.Context) error { return nil }

// bulkPollCondition mirrors the condition the Salesforce importer synthesizes
// (pkg/convert/salesforce synthesizeBulkPollLoops). This test proves the engine
// executes that exact shape correctly — including the null-safe first pass.
const bulkPollCondition = `!has(var.bulk_job_state) || (var.bulk_job_state != "JobComplete" && var.bulk_job_state != "Failed" && var.bulk_job_state != "Aborted")`

func bulkPollLoopAction(maxIterations int) dsl.Action {
	return dsl.Action{
		Name: "Job Status (poll until JobComplete)",
		Type: "control_loop",
		Control: &dsl.ControlFlow{
			Condition:     &dsl.Condition{Expr: bulkPollCondition},
			MaxIterations: maxIterations,
			// Mirrors the synthesized output (pkg/convert/salesforce): do_while runs
			// the body once before the predicate is first checked so the state var is
			// re-primed on every loop entry, including VUser re-entry.
			DoWhile: true,
			Steps: []dsl.Action{{
				Name:       "Job Status",
				Type:       "GET",
				Method:     "GET",
				Target:     "/services/data/v59.0/jobs/ingest/X",
				Extractors: []dsl.Extractor{{Name: "bulk_job_state", Source: "body", Type: "jsonpath", Expression: "$.state", OnMiss: "fail"}},
			}},
		},
	}
}

func TestBulkPollLoopRunsUntilJobComplete(t *testing.T) {
	// State var is unset on the first pass; the null-safe condition must still let
	// the loop run, then keep polling through InProgress, and stop on JobComplete.
	proto := &bulkStatusProtocol{completeAfter: 2}
	vu := newControlFlowVUser(proto, nil, []dsl.Action{bulkPollLoopAction(60)})
	vu.Run(context.Background())
	if proto.calls != 3 {
		t.Fatalf("poll loop issued %d status GETs, want 3 (InProgress, InProgress, JobComplete)", proto.calls)
	}
}

func TestBulkPollLoopRespectsMaxIterations(t *testing.T) {
	// A job that never completes must be bounded by MaxIterations, not spin forever.
	proto := &bulkStatusProtocol{completeAfter: 1000}
	vu := newControlFlowVUser(proto, nil, []dsl.Action{bulkPollLoopAction(5)})
	vu.Run(context.Background())
	if proto.calls != 5 {
		t.Fatalf("never-completing poll loop issued %d GETs, want 5 (capped at MaxIterations)", proto.calls)
	}
}

func TestBulkPollLoopReExecutesAcrossIterations(t *testing.T) {
	// Regression: VUser.Variables persist across script iterations, so the poll
	// state var keeps its terminal value from a prior iteration. Without do_while,
	// iteration 2's pre-test condition is immediately false and the (only copy of
	// the) status GET is skipped — the poll loop is effectively dead under load.
	// do_while runs the body once per loop entry, re-priming the var each iteration.
	run := func(doWhile bool) int {
		proto := &bulkStatusProtocol{completeAfter: 0} // every poll returns JobComplete
		action := bulkPollLoopAction(60)
		action.Control.DoWhile = doWhile
		vu := newControlFlowVUser(proto, nil, []dsl.Action{action})
		vu.Script.Config.RunLogic.Iterations = 2
		vu.Run(context.Background())
		return proto.calls
	}
	// Pre-test (the bug): iteration 1 sets bulk_job_state=JobComplete, iteration 2 skips.
	if got := run(false); got != 1 {
		t.Fatalf("pre-test loop issued %d status GETs over 2 iterations, want 1 (iteration 2 skipped — the bug this guards)", got)
	}
	// do_while (the fix): the status GET re-runs on every iteration.
	if got := run(true); got != 2 {
		t.Fatalf("do_while loop issued %d status GETs over 2 iterations, want 2 (one re-primed poll per iteration)", got)
	}
}

func TestBulkPollConditionTerminalStates(t *testing.T) {
	cases := []struct {
		state string
		set   bool
		want  bool // true = keep polling
	}{
		{"", false, true}, // unset (first pass) — null-safe, must keep polling
		{"Open", true, true},
		{"InProgress", true, true},
		{"UploadComplete", true, true},
		{"JobComplete", true, false},
		{"Failed", true, false},
		{"Aborted", true, false},
	}
	for _, c := range cases {
		vars := map[string]string{}
		if c.set {
			vars["bulk_job_state"] = c.state
		}
		vu := &VUser{Variables: vars}
		if got := vu.evalCondition(&dsl.Condition{Expr: bulkPollCondition}); got != c.want {
			t.Fatalf("state=%q set=%v: keepPolling=%v, want %v", c.state, c.set, got, c.want)
		}
	}
}
