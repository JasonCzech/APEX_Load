package engine

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestZeroArgumentBuiltinsAcceptParentheses(t *testing.T) {
	now := func() time.Time { return time.Unix(1234567890, 123000000).UTC() }
	for _, expr := range []string{"fn.uuid", "fn.uuid.v4", "fake.uuid", "fake.email", "fn.timestamp.unix", "fn.timestamp.unix_ms", "fn.timestamp.iso8601", "fn.timestamp.rfc3339"} {
		for _, suffix := range []string{"", "()"} {
			t.Run(expr+suffix, func(t *testing.T) {
				got, ok := resolveBuiltinFunction(expr+suffix, now, nil)
				if !ok || got == "" {
					t.Fatalf("builtin unresolved: %q", expr+suffix)
				}
				if strings.HasPrefix(expr, "fn.timestamp.") {
					want, _ := resolveBuiltinFunction(expr, now, nil)
					if got != want {
						t.Fatalf("got %q, want %q", got, want)
					}
				}
			})
		}
	}
	for _, expr := range []string{"fn.timestamp.unix(1)", "fn.random()", "fn.random_string()", "fn.now()"} {
		if _, ok := resolveBuiltinFunction(expr, now, nil); ok {
			t.Fatalf("accepted invalid builtin %q", expr)
		}
	}
}

func popTestProvider(t *testing.T) *DataProvider {
	t.Helper()
	dp, err := NewDataProvider([]dsl.DataSource{{Name: "accounts", Type: "csv", Strategy: "pop", FirstRowHeader: true, InlineData: "id\none\ntwo\n"}})
	if err != nil {
		t.Fatal(err)
	}
	return dp
}

func TestPopExhaustionClearsBindingsAndErrors(t *testing.T) {
	dp := popTestProvider(t)
	vu := &VUser{ID: 1, DataProvider: dp, Variables: map[string]string{"other.id": "keep"}}
	for _, want := range []string{"one", "two"} {
		if err := vu.loadDataRow(); err != nil {
			t.Fatal(err)
		}
		if got := vu.Variables["accounts.id"]; got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
	if err := vu.loadDataRow(); err == nil || !strings.Contains(err.Error(), `data source "accounts" exhausted`) {
		t.Fatalf("unexpected exhaustion error: %v", err)
	}
	if _, ok := vu.Variables["accounts.id"]; ok {
		t.Fatal("exhaustion retained previous row")
	}
	if vu.Variables["other.id"] != "keep" {
		t.Fatal("cleared unrelated binding")
	}
	if err := (&VUser{ID: 2, DataProvider: dp}).loadDataRow(); err == nil {
		t.Fatal("new VUser did not see exhaustion")
	}
}

func TestPopExhaustionStopsVUserAndRecordsFailure(t *testing.T) {
	dp := popTestProvider(t)
	for _, wantRequests := range []int{2, 0} {
		proto := &captureProtocol{}
		vu := newControlFlowVUser(proto, nil, []dsl.Action{click("submit")})
		vu.DataProvider = dp
		vu.Script.Config.RunLogic.Iterations = 5
		vu.Run(context.Background())
		if len(proto.names) != wantRequests {
			t.Fatalf("got %d requests, want %d", len(proto.names), wantRequests)
		}
		found := false
		for len(vu.Metrics.ch) > 0 {
			result := <-vu.Metrics.ch
			if strings.Contains(result.ErrorMessage, `data source "accounts" exhausted`) && !result.Success {
				found = true
			}
		}
		if !found {
			t.Fatal("exhaustion missing from metrics")
		}
	}
}

func TestForEachAboveDefaultCapRunsFully(t *testing.T) {
	var csv strings.Builder
	csv.WriteString("id\n")
	for i := 0; i < 2000; i++ {
		csv.WriteString(strconv.Itoa(i) + "\n")
	}
	dp, err := NewDataProvider([]dsl.DataSource{{Name: "rows", Type: "csv", FirstRowHeader: true, InlineData: csv.String()}})
	if err != nil {
		t.Fatal(err)
	}
	proto := &captureProtocol{}
	vu := newControlFlowVUser(proto, nil, []dsl.Action{{Name: "each", Type: "control_loop", Control: &dsl.ControlFlow{ForEach: &dsl.ForEachLoop{Source: "rows", Var: "row"}, Steps: []dsl.Action{click("step")}}}})
	vu.DataProvider = dp
	vu.Run(context.Background())
	if len(proto.names) != 2000 {
		t.Fatalf("got %d steps, want 2000", len(proto.names))
	}
}

func TestControlLoopEffectiveBounds(t *testing.T) {
	for _, tc := range []struct {
		control *dsl.ControlFlow
		want    int
	}{
		{nil, defaultControlLoopMaxIterations},
		{&dsl.ControlFlow{Condition: &dsl.Condition{Expr: "true"}}, defaultControlLoopMaxIterations},
		{&dsl.ControlFlow{Count: 5000}, dsl.MaxControlLoopIterations},
		{&dsl.ControlFlow{ForEach: &dsl.ForEachLoop{Source: "rows"}}, dsl.MaxControlLoopIterations},
		{&dsl.ControlFlow{Count: 5000, MaxIterations: 3}, 3},
		{&dsl.ControlFlow{MaxIterations: dsl.MaxControlLoopIterations + 1}, dsl.MaxControlLoopIterations},
	} {
		if got := controlLoopMaxPasses(tc.control); got != tc.want {
			t.Fatalf("%+v: got %d, want %d", tc.control, got, tc.want)
		}
	}
}

func TestPopExhaustionDoesNotSkipLifecycleTeardown(t *testing.T) {
	dp := popTestProvider(t)
	dp.GetRow("accounts", 1)
	dp.GetRow("accounts", 1)
	proto := &captureProtocol{}
	vu := newControlFlowVUser(proto, nil, nil)
	vu.DataProvider = dp
	if !vu.RunLifecycleBlock(context.Background(), []dsl.Action{click("cleanup")}, "teardown") {
		t.Fatal("teardown skipped on exhaustion")
	}
	if len(proto.names) != 1 || proto.names[0] != "cleanup" {
		t.Fatalf("cleanup not executed: %v", proto.names)
	}
}

func TestParallelPopUsesParentRow(t *testing.T) {
	dp := popTestProvider(t)
	vu := &VUser{ID: 1, Proto: &parallelTestProtocol{}, DataProvider: dp, Metrics: NewMetricsCollector(16), Script: &dsl.Script{Protocol: parallelTestProtocolName}}
	if err := vu.prepareRuntimeState(); err != nil {
		t.Fatal(err)
	}
	control := &dsl.ControlFlow{Concurrency: 2, Branches: []dsl.ParallelBranch{
		{Name: "one", Steps: []dsl.Action{{Name: "one", Type: "GET", Target: "/{{data.accounts.id}}"}}},
		{Name: "two", Steps: []dsl.Action{{Name: "two", Type: "GET", Target: "/{{data.accounts.id}}"}}},
	}}
	if !vu.runParallelBranches(context.Background(), control, "action") {
		t.Fatal("branches failed")
	}
	for _, name := range []string{"one", "two"} {
		if got := vu.Variables["parallel."+name+".observed"]; got != "/one" {
			t.Fatalf("branch %s target = %q", name, got)
		}
	}
	if row := dp.GetRow("accounts", 1); row["accounts.id"] != "two" {
		t.Fatalf("branches consumed next row: %v", row)
	}
}

func TestPopExhaustionTeardownRetainsLastSuccessfulRow(t *testing.T) {
	vu := newControlFlowVUser(&parallelTestProtocol{}, nil, []dsl.Action{{Name: "main", Type: "GET", Target: "/{{data.accounts.id}}"}})
	vu.DataProvider = popTestProvider(t)
	vu.Script.Config.RunLogic.Iterations = 5
	vu.Script.Teardown = []dsl.Action{{Name: "cleanup", Type: "GET", Target: "/cleanup/{{data.accounts.id}}"}}
	vu.Run(context.Background())
	if got := vu.Variables["observed"]; got != "/cleanup/two" {
		t.Fatalf("cleanup target = %q", got)
	}
}

func TestLifecyclePopTeardownRetainsExistingRow(t *testing.T) {
	vu := newControlFlowVUser(&parallelTestProtocol{}, nil, nil)
	vu.DataProvider = popTestProvider(t)
	if err := vu.prepareRuntimeState(); err != nil {
		t.Fatal(err)
	}
	if err := vu.loadDataRow(); err != nil {
		t.Fatal(err)
	}
	if !vu.RunLifecycleBlock(context.Background(), []dsl.Action{{Name: "cleanup", Type: "GET", Target: "/cleanup/{{data.accounts.id}}"}}, "teardown") {
		t.Fatal("cleanup failed")
	}
	if got := vu.Variables["observed"]; got != "/cleanup/two" {
		t.Fatalf("cleanup target = %q", got)
	}
}

func TestCleanupDataBindingsRestoreAllSourcesWithoutChangingExtractions(t *testing.T) {
	dp, err := NewDataProvider([]dsl.DataSource{
		{Name: "accounts", Type: "csv", Strategy: "pop", FirstRowHeader: true, InlineData: "id\none\n"},
		{Name: "orders", Type: "csv", FirstRowHeader: true, InlineData: "id\norder\n"},
	})
	if err != nil {
		t.Fatal(err)
	}
	vu := &VUser{DataProvider: dp, Variables: map[string]string{"accounts.id": "one", "orders.id": "order", "extracted": "before"}}
	snapshot := vu.currentDataBindings()
	delete(vu.Variables, "accounts.id")
	vu.Variables["orders.id"] = "next"
	vu.Variables["extracted"] = "after"
	vu.restoreDataBindings(snapshot)
	if vu.Variables["accounts.id"] != "one" || vu.Variables["orders.id"] != "order" || vu.Variables["extracted"] != "after" {
		t.Fatalf("restored bindings: %v", vu.Variables)
	}
}
