package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/jasonczech/apexload/pkg/dsl"
)

type runtimeSnapshotController struct {
	t            *testing.T
	before       []ReplayRuntimeSnapshot
	after        []ReplayRuntimeSnapshot
	legacyBefore int
	legacyAfter  int
	onBefore     func(ReplayDebugAction, ReplayRuntimeSnapshot) error
	onAfter      func(ReplayDebugAction, ReplayRuntimeSnapshot) error
}

func (c *runtimeSnapshotController) BeforeAction(context.Context, ReplayDebugAction) error {
	c.legacyBefore++
	return nil
}
func (c *runtimeSnapshotController) AfterAction(context.Context, ReplayDebugAction, *ReplayActionEvent) error {
	c.legacyAfter++
	return nil
}
func (c *runtimeSnapshotController) BeforeActionWithRuntimeState(_ context.Context, a ReplayDebugAction, s ReplayRuntimeSnapshot) error {
	if !ValidateReplayRuntimeSnapshotBounds(s) {
		c.t.Error("engine emitted invalid before snapshot")
	}
	c.before = append(c.before, CloneReplayRuntimeSnapshot(s))
	if c.onBefore != nil {
		return c.onBefore(a, s)
	}
	return nil
}
func (c *runtimeSnapshotController) AfterActionWithRuntimeState(_ context.Context, a ReplayDebugAction, _ *ReplayActionEvent, s ReplayRuntimeSnapshot) error {
	if !ValidateReplayRuntimeSnapshotBounds(s) {
		c.t.Error("engine emitted invalid after snapshot")
	}
	c.after = append(c.after, CloneReplayRuntimeSnapshot(s))
	if c.onAfter != nil {
		return c.onAfter(a, s)
	}
	return nil
}
func runtimeTestVUser(parameters []dsl.Parameter, provider *DataProvider) *VUser {
	state := &replayRuntimeState{rows: make(map[string]ReplayRuntimeRow)}
	vu := &VUser{Variables: map[string]string{}, DataProvider: provider, replayRuntime: state, Parameters: NewParameterRuntime(parameters, provider, 0), rng: rand.New(rand.NewSource(77))}
	if vu.Parameters != nil {
		vu.Parameters.runtimeObservation = state
		vu.Parameters.runtimeObservations = make(map[string]parameterRuntimeObservation)
	}
	return vu
}
func runtimeTestLocation() ReplayDebugAction {
	return ReplayDebugAction{Phase: "action", ActionIndex: 0, EventIndex: 0, EventID: "observed-event", ActionName: "Inspect", ActionType: "GET", Method: "GET"}
}
func runtimeBinding(t *testing.T, s ReplayRuntimeSnapshot, name string) ReplayRuntimeBinding {
	t.Helper()
	for _, v := range s.Variables {
		if v.Name == name {
			return v
		}
	}
	t.Fatalf("binding %q missing", name)
	return ReplayRuntimeBinding{}
}
func runtimeParameter(t *testing.T, s ReplayRuntimeSnapshot, name string) ReplayRuntimeParameter {
	t.Helper()
	for _, v := range s.Parameters {
		if v.Name == name {
			return v
		}
	}
	t.Fatalf("parameter %q missing", name)
	return ReplayRuntimeParameter{}
}
func runtimeRow(t *testing.T, s ReplayRuntimeSnapshot, kind, name string) ReplayRuntimeRow {
	t.Helper()
	for _, v := range s.Rows {
		if v.Kind == kind && v.Name == name {
			return v
		}
	}
	t.Fatalf("row %s/%s missing", kind, name)
	return ReplayRuntimeRow{}
}
func runtimeHasBinding(s ReplayRuntimeSnapshot, name string) bool {
	for _, v := range s.Variables {
		if v.Name == name {
			return true
		}
	}
	return false
}
func runtimeValue(t *testing.T, v *string) string {
	t.Helper()
	if v == nil {
		t.Fatal("value pointer missing")
	}
	return *v
}
func runtimeSave(name, value, variable string) dsl.Action {
	return dsl.Action{Name: name, Type: "source_parameter", Params: map[string]any{"function": "lr_save_string", "args": []any{value, variable}}}
}

func TestReplayRuntimeSnapshotInspectionDoesNotEvaluateOrAdvance(t *testing.T) {
	provider, err := NewDataProvider([]dsl.DataSource{{Name: "data", Type: "csv", InlineData: "id\na\nb\nc\n", FirstRowHeader: true, Strategy: "random"}})
	if err != nil {
		t.Fatal(err)
	}
	control, err := NewDataProvider([]dsl.DataSource{{Name: "data", Type: "csv", InlineData: "id\na\nb\nc\n", FirstRowHeader: true, Strategy: "random"}})
	if err != nil {
		t.Fatal(err)
	}
	provider.SeedRandom(17)
	control.SeedRandom(17)
	vu := runtimeTestVUser([]dsl.Parameter{{Name: "ticket", Type: "counter", Start: 42, Step: 1, Update: "each_occurrence"}, {Name: "random", Type: "random_number", Min: 1, Max: 100, Update: "each_occurrence", Seed: func() *int64 { v := int64(9); return &v }()}, {Name: "clock", Type: "timestamp", Update: "each_occurrence"}, {Name: "prepared", Type: "timestamp", Update: "once"}}, provider)
	var nowCalls int
	vu.Parameters.now = func() time.Time { nowCalls++; return time.Unix(123, 0) }
	vu.Parameters.Prime()
	vu.Variables["empty"] = ""
	before := vu.captureReplayRuntimeSnapshot("before_action", runtimeTestLocation())
	if before.State != "complete" || runtimeParameter(t, before, "ticket").State != "not_evaluated" || runtimeParameter(t, before, "clock").Value != nil {
		t.Fatal("capture evaluated an unused occurrence parameter")
	}
	prepared := runtimeParameter(t, before, "prepared")
	if prepared.State != "prepared" || prepared.ValueState != "assigned" || runtimeValue(t, prepared.Value) != "123" || prepared.ObservedEventID != "" {
		t.Fatal("prepared cache was attributed to consumption")
	}
	empty := runtimeBinding(t, before, "empty")
	if empty.ValueState != "assigned" || runtimeValue(t, empty.Value) != "" || runtimeHasBinding(before, "missing") {
		t.Fatal("empty and missing bindings were conflated")
	}
	for i := 0; i < 25; i++ {
		if next := vu.captureReplayRuntimeSnapshot("before_action", runtimeTestLocation()); !reflect.DeepEqual(before, next) {
			t.Fatal("read-only inspection changed its result")
		}
	}
	if nowCalls != 1 || vu.Parameters.states["ticket"].seeded || vu.Parameters.states["random"].hasCached || provider.sources["data"].seqIdx.Load() != 0 {
		t.Fatal("inspection advanced a clock, counter, random parameter or row cursor")
	}
	if got, want := vu.rng.Int63(), rand.New(rand.NewSource(77)).Int63(); got != want {
		t.Fatal("inspection advanced template RNG")
	}
	if !reflect.DeepEqual(provider.GetRow("data", 0), control.GetRow("data", 0)) {
		t.Fatal("inspection advanced data RNG")
	}
	baseline := NewParameterRuntime([]dsl.Parameter{{Name: "random", Type: "random_number", Min: 1, Max: 100, Update: "each_occurrence", Seed: func() *int64 { v := int64(9); return &v }()}}, nil, 0)
	if got, _ := vu.Parameters.ResolveNamed("random"); func() bool { want, _ := baseline.ResolveNamed("random"); return got != want }() {
		t.Fatal("inspection advanced parameter RNG")
	}
	vu.replayRuntime.eventID = "actual-consumer"
	if value, found := vu.Parameters.ResolveNamed("ticket"); !found || value != "42" {
		t.Fatal("inspection changed first consumed counter")
	}
	after := vu.captureReplayRuntimeSnapshot("after_action", runtimeTestLocation())
	observed := runtimeParameter(t, after, "ticket")
	if observed.State != "observed" || observed.ValueState != "observed" || runtimeValue(t, observed.Value) != "42" || observed.ObservedEventID != "actual-consumer" {
		t.Fatal("last actual resolution receipt missing")
	}
	for i := 0; i < 10; i++ {
		_ = vu.captureReplayRuntimeSnapshot("after_action", runtimeTestLocation())
	}
	if value, _ := vu.Parameters.ResolveNamed("ticket"); value != "43" {
		t.Fatal("inspection forecast or consumed the next counter")
	}
	delete(vu.Variables, "empty")
	replacement := vu.captureReplayRuntimeSnapshot("before_action", runtimeTestLocation())
	if len(replacement.Variables) != 0 {
		t.Fatal("full replacement retained deleted binding")
	}
	encoded, _ := json.Marshal(replacement)
	if !strings.Contains(string(encoded), `"variables":[]`) || !strings.Contains(string(encoded), `"frames":[]`) {
		t.Fatal("empty replacement arrays became absent/null")
	}
}

func TestReplayRuntimeSnapshotHooksDescribeActualSourceAndWireConsumption(t *testing.T) {
	var queries atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries.Add(1)
		if r.URL.Query().Get("saved") != "42" || r.URL.Query().Get("a") != "43" || r.URL.Query().Get("b") != "44" {
			w.WriteHeader(400)
			return
		}
		w.WriteHeader(204)
	}))
	defer server.Close()
	c := &runtimeSnapshotController{t: t}
	c.onBefore = func(a ReplayDebugAction, s ReplayRuntimeSnapshot) error {
		if a.ActionName == "Consume" {
			b := runtimeBinding(t, s, "saved")
			if runtimeValue(t, b.Value) != "42" {
				t.Fatal("source binding missing before wire")
			}
			*b.Value = "mutated copy"
		}
		return nil
	}
	script := &dsl.Script{Name: "Actual observed parameters", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: server.URL}, Parameters: []dsl.Parameter{{Name: "ticket", Type: "counter", Start: 42, Step: 1, Update: "each_occurrence"}}, Actions: []dsl.Action{runtimeSave("Assign", "{{param.ticket}}", "saved"), {Name: "Consume", Type: "GET", Target: "/?saved={{var.saved}}&a={{param.ticket}}&b={{param.ticket}}", Assertions: []dsl.Assertion{{Type: "status_code", Expected: 204}}}}}
	report, err := ReplayScript(context.Background(), script, ReplayOptions{IgnoreThinkTime: true, DebugController: c})
	if err != nil || !report.Success || queries.Load() != 1 {
		t.Fatalf("actual source/wire consumption failed: %v", err)
	}
	if c.legacyBefore != 0 || c.legacyAfter != 0 || len(c.before) != 2 || len(c.after) != 2 {
		t.Fatal("extended and legacy hook dispatched more than once")
	}
	if runtimeParameter(t, c.before[0], "ticket").State != "not_evaluated" {
		t.Fatal("before assignment forecast counter")
	}
	p := runtimeParameter(t, c.after[0], "ticket")
	if runtimeValue(t, p.Value) != "42" || p.ObservedEventID != c.after[0].Location.EventID {
		t.Fatal("assignment resolution receipt has wrong value/event")
	}
	p = runtimeParameter(t, c.after[1], "ticket")
	if runtimeValue(t, p.Value) != "44" || p.ObservedEventID != c.after[1].Location.EventID {
		t.Fatal("wire receipt is not last actual occurrence")
	}
	if runtimeValue(t, runtimeBinding(t, c.after[1], "saved").Value) != "42" {
		t.Fatal("controller mutation reached executable variables")
	}
}

func TestReplayRuntimeSnapshotOriginalParameterRowOrdinals(t *testing.T) {
	provider, err := NewShardedDataProvider([]dsl.DataSource{{Name: "rows", Type: "csv", InlineData: "id,other\nr1,a\nr2,b\nr3,c\nr4,d\nr5,e\nr6,f\nr7,g\nr8,h\n", FirstRowHeader: true}}, DataShardConfig{Ordinal: 1, Count: 2})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name       string
		param      dsl.Parameter
		vuser      int
		allocation bool
		want       string
		ordinal    int
	}{
		{"sharded start", dsl.Parameter{Type: "file", StartRow: 2}, 0, false, "r4", 4},
		{"canonical alias", dsl.Parameter{Type: "File", StartRow: 2}, 0, false, "r4", 4},
		{"sharded block", dsl.Parameter{Type: "file", BlockSize: 1}, 1, false, "r4", 4},
		{"per-vuser", dsl.Parameter{Type: "file", ValueForEachVUser: true}, 2, false, "r6", 6},
		{"allocated unique unsharded", dsl.Parameter{Type: "file", Selection: "unique", BlockSize: 2, StartRow: 2}, 1, true, "r4", 4},
		{"inline list start block", dsl.Parameter{Type: "List", Values: []string{"r1", "r2", "r3", "r4", "r5"}, StartRow: 2, BlockSize: 1}, 2, false, "r4", 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.param
			p.Name = "pick"
			p.Update = "once"
			if len(p.Values) == 0 {
				p.DataSource = "rows"
				p.Column = "id"
			}
			vu := runtimeTestVUser([]dsl.Parameter{p}, provider)
			vu.Parameters.vuserID = tc.vuser
			if tc.allocation {
				vu.Parameters.allocation = &ParameterAllocation{Population: 2, Offset: tc.vuser, Iterations: 1}
			}
			vu.Parameters.Prime()
			prepared := vu.captureReplayRuntimeSnapshot("before_action", runtimeTestLocation())
			r := runtimeRow(t, prepared, "parameter", "pick")
			if r.RowOrdinal == nil || *r.RowOrdinal != tc.ordinal || runtimeParameter(t, prepared, "pick").State != "prepared" {
				t.Fatal("Prime lost actual selected original row")
			}
			vu.replayRuntime.eventID = "consumer"
			got, found := vu.Parameters.ResolveNamed("pick")
			if !found || got != tc.want {
				t.Fatalf("actual selected value=%q", got)
			}
			observed := vu.captureReplayRuntimeSnapshot("after_action", runtimeTestLocation())
			r = runtimeRow(t, observed, "parameter", "pick")
			if *r.RowOrdinal != tc.ordinal || r.ObservedEventID != "consumer" || runtimeValue(t, runtimeParameter(t, observed, "pick").Value) != tc.want {
				t.Fatal("observed parameter and same-selection original row disagree")
			}
		})
	}
	vu := runtimeTestVUser([]dsl.Parameter{{Name: "pick", Type: "File", DataSource: "rows", Column: "id", Update: "each_occurrence", StartRow: 2}, {Name: "other", Type: "file", DataSource: "rows", Column: "other", Update: "each_occurrence", StartRow: 2, SameLineAs: "pick"}}, provider)
	_, _ = vu.Parameters.ResolveNamed("pick")
	value, ok := vu.Parameters.ResolveNamed("other")
	if !ok || value != "d" {
		t.Fatal("same-line actual resolution failed")
	}
	s := vu.captureReplayRuntimeSnapshot("after_action", runtimeTestLocation())
	a, b := runtimeRow(t, s, "parameter", "pick"), runtimeRow(t, s, "parameter", "other")
	if *a.RowOrdinal != 4 || *b.RowOrdinal != 4 || b.Selection != "same_line" {
		t.Fatal("same-line receipt reported a local slice index")
	}
}

func TestReplayRuntimeSnapshotDataPopReceiptsAndExhaustedDeletion(t *testing.T) {
	var data strings.Builder
	data.WriteString("id\n")
	for i := 1; i <= 30; i++ {
		fmt.Fprintf(&data, "%d\n", i)
	}
	provider, err := NewDataProvider([]dsl.DataSource{{Name: "queue", Type: "csv", Strategy: "pop", InlineData: data.String(), FirstRowHeader: true}})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan dataRowReceipt, 30)
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			row, receipt := provider.getRowWithReceipt("queue", 0)
			if row == nil || row["queue.id"] != strconv.Itoa(receipt.ordinal) {
				t.Error("pop value and receipt came from different allocations")
			}
			results <- receipt
		}()
	}
	wg.Wait()
	close(results)
	seen := map[int]bool{}
	for r := range results {
		if seen[r.ordinal] || r.state != "selected" {
			t.Fatal("pop reused an ordinal")
		}
		seen[r.ordinal] = true
	}
	if len(seen) != 30 {
		t.Fatal("pop lost allocation receipts")
	}
	vu := runtimeTestVUser(nil, provider)
	vu.Variables["queue.id"] = "stale"
	if err := vu.loadDataRow(); err == nil {
		t.Fatal("exhausted pop did not fail")
	}
	s := vu.captureReplayRuntimeSnapshot("before_action", runtimeTestLocation())
	r := runtimeRow(t, s, "datasource", "queue")
	if r.State != "exhausted" || r.RowOrdinal != nil || runtimeHasBinding(s, "queue.id") {
		t.Fatal("exhausted row retained old bindings/ordinal")
	}
	for i := 0; i < 10; i++ {
		_ = vu.captureReplayRuntimeSnapshot("before_action", runtimeTestLocation())
	}
	if provider.sources["queue"].seqIdx.Load() != 30 {
		t.Fatal("inspection advanced exhausted pop")
	}
}

func TestReplayRuntimeSnapshotNestedForeachRestoresBindingsReceiptsAndFrames(t *testing.T) {
	var traffic atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { traffic.Add(1); w.WriteHeader(204) }))
	defer server.Close()
	c := &runtimeSnapshotController{t: t}
	innerLeaf := dsl.Action{Name: "Inner", Type: "GET", Target: "/inner/{{var.row.id}}"}
	inner := dsl.Action{Name: "Inner rows", Type: "control_loop", Control: &dsl.ControlFlow{ForEach: &dsl.ForEachLoop{Source: "inner", Var: "row"}, Steps: []dsl.Action{{Name: "Taken", Type: "control_if", Control: &dsl.ControlFlow{Condition: &dsl.Condition{Left: "yes", Op: "eq", Right: "yes"}, Steps: []dsl.Action{innerLeaf}}}}}}
	outer := dsl.Action{Name: "Outer rows", Type: "control_loop", Control: &dsl.ControlFlow{ForEach: &dsl.ForEachLoop{Source: "outer", Var: "row"}, Steps: []dsl.Action{inner, {Name: "Outer", Type: "GET", Target: "/outer/{{var.row.id}}"}}}}
	defaultSwitch := dsl.Action{Name: "Default", Type: "control_switch", Control: &dsl.ControlFlow{Cases: []dsl.SwitchCase{{Condition: &dsl.Condition{Left: "a", Op: "eq", Right: "b"}, Steps: []dsl.Action{{Name: "Untaken", Type: "GET", Target: "/untaken"}}}}, Default: []dsl.Action{{Name: "Outside", Type: "GET", Target: "/outside/{{var.row.id}}"}}}}
	script := &dsl.Script{Name: "Scope restoration", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: server.URL}, DataSources: []dsl.DataSource{{Name: "outer", Type: "csv", InlineData: "id\nouter1\nouter2\n", FirstRowHeader: true}, {Name: "inner", Type: "csv", InlineData: "id,only_inner\ninner1,temporary\n", FirstRowHeader: true}}, Actions: []dsl.Action{outer, defaultSwitch}}
	report, err := ReplayScript(context.Background(), script, ReplayOptions{IgnoreThinkTime: true, DebugController: c, Variables: map[string]string{"row.id": "prior", "loop_index": "parent"}})
	if err != nil || !report.Success || traffic.Load() != 5 {
		t.Fatalf("nested replay failed: %v", err)
	}
	innerCount, outerCount := 0, 0
	for _, s := range c.before {
		switch s.Location.ActionName {
		case "Inner":
			innerCount++
			if runtimeValue(t, runtimeBinding(t, s, "row.id").Value) != "inner1" || len(s.Frames) != 4 || s.Frames[1].Kind != "foreach" || s.Frames[2].Kind != "foreach" || s.Frames[3].Kind != "control_if" {
				t.Fatal("actual nested frame/binding stack missing")
			}
			r := runtimeRow(t, s, "foreach", "row")
			if r.Source != "inner" || *r.RowOrdinal != 1 {
				t.Fatal("inner foreach receipt mismatched actual row")
			}
		case "Outer":
			outerCount++
			if runtimeValue(t, runtimeBinding(t, s, "row.id").Value) != fmt.Sprintf("outer%d", outerCount) || runtimeHasBinding(s, "row.only_inner") || len(s.Frames) != 2 {
				t.Fatal("inner scope deletion/restoration failed")
			}
			r := runtimeRow(t, s, "foreach", "row")
			if r.Source != "outer" || *r.RowOrdinal != outerCount {
				t.Fatal("outer foreach row receipt was not restored")
			}
		case "Outside":
			if runtimeValue(t, runtimeBinding(t, s, "row.id").Value) != "prior" || runtimeValue(t, runtimeBinding(t, s, "loop_index").Value) != "parent" || runtimeHasBinding(s, "row.only_inner") || len(s.Frames) != 2 || s.Frames[1].Kind != "control_switch" || !reflect.DeepEqual(s.Location.StepPath, []string{"default[0]"}) {
				t.Fatal("final replacement/default structural path is incorrect")
			}
			for _, r := range s.Rows {
				if r.Kind == "foreach" {
					t.Fatal("exited foreach receipt was retained")
				}
			}
		}
	}
	if innerCount != 2 || outerCount != 2 {
		t.Fatal("nested scopes did not execute expected passes")
	}
}

func TestReplayRuntimeSnapshotParallelRestoresParentAndSharesActualParameters(t *testing.T) {
	for _, abort := range []bool{false, true} {
		t.Run(fmt.Sprint(abort), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Marker", strings.TrimPrefix(r.URL.Path, "/"))
				w.WriteHeader(204)
			}))
			defer server.Close()
			c := &runtimeSnapshotController{t: t}
			c.onBefore = func(a ReplayDebugAction, s ReplayRuntimeSnapshot) error {
				if a.ActionName == "Stop" && abort {
					return errors.New("operator stop")
				}
				return nil
			}
			get := func(name, path string) dsl.Action {
				return dsl.Action{Name: name, Type: "GET", Target: path, Extractors: []dsl.Extractor{{Name: "marker", Source: "header", Type: "header", Expression: "X-Marker"}}}
			}
			script := &dsl.Script{Name: "Parallel scopes", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: server.URL}, Parameters: []dsl.Parameter{{Name: "ticket", Type: "counter", Start: 41, Step: 1, Update: "each_occurrence"}}, Actions: []dsl.Action{{Name: "Branches", Type: "control_parallel", Control: &dsl.ControlFlow{Branches: []dsl.ParallelBranch{{Name: "first", Steps: []dsl.Action{get("First", "/first?ticket={{param.ticket}}"), {Name: "Stop", Type: "GET", Target: "/stop"}}}, {Name: "second", Steps: []dsl.Action{get("Second", "/second?ticket={{param.ticket}}")}}}}}, {Name: "Outside", Type: "GET", Target: "/outside"}}, Teardown: []dsl.Action{{Name: "Cleanup", Type: "GET", Target: "/cleanup/{{var.marker}}"}}}
			report, err := ReplayScript(context.Background(), script, ReplayOptions{IgnoreThinkTime: true, DebugController: c, Variables: map[string]string{"marker": "parent"}})
			if err != nil || report == nil || report.Success == abort {
				t.Fatalf("parallel replay unexpected result: %v", err)
			}
			foundCleanup, foundSecond := false, false
			for _, s := range c.before {
				switch s.Location.ActionName {
				case "First":
					if len(s.Frames) != 3 || s.Frames[1].Kind != "parallel" || s.Frames[2].Kind != "parallel_branch" || *s.Frames[2].BranchIndex != 0 {
						t.Fatal("branch frame missing")
					}
				case "Second":
					foundSecond = true
					if runtimeValue(t, runtimeBinding(t, s, "marker").Value) != "parent" || runtimeValue(t, runtimeParameter(t, s, "ticket").Value) != "41" || *s.Frames[2].BranchIndex != 1 {
						t.Fatal("branch scope restoration rolled back shared consumption")
					}
				case "Cleanup":
					foundCleanup = true
					if runtimeValue(t, runtimeBinding(t, s, "marker").Value) != "parent" || len(s.Frames) != 1 || s.Frames[0].Phase != "teardown" {
						t.Fatal("abort/exit leaked branch state into teardown")
					}
				}
			}
			if !foundCleanup || foundSecond == abort {
				t.Fatal("parallel abort/continue path changed")
			}
		})
	}
}

func TestReplayRuntimeSnapshotConcurrentHTTPOnlyOuterBeforeAndPostJoin(t *testing.T) {
	var hits atomic.Int32
	ready := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 2 {
			close(ready)
		}
		select {
		case <-ready:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("X-Observed", r.URL.Query().Get("value"))
		w.WriteHeader(204)
	}))
	defer server.Close()
	child := func(name string) dsl.Action {
		return dsl.Action{Name: name, Type: "GET", Target: "/?value={{param.ticket}}", Extractors: []dsl.Extractor{{Name: name, Source: "header", Type: "header", Expression: "X-Observed"}}}
	}
	group := dsl.Action{Name: "Joined", Type: "http_sequence", Params: map[string]any{"execution_mode": "concurrent", "max_concurrency": 2, "requests": []dsl.Action{child("left"), child("right")}}}
	c := &runtimeSnapshotController{t: t}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	report, err := ReplayScript(ctx, &dsl.Script{Name: "Join snapshots", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: server.URL}, Parameters: []dsl.Parameter{{Name: "ticket", Type: "counter", Start: 41, Step: 1, Update: "each_occurrence"}}, Actions: []dsl.Action{group}}, ReplayOptions{IgnoreThinkTime: true, DebugController: c})
	if err != nil || !report.Success || hits.Load() != 2 || len(report.Events) != 2 || len(c.before) != 1 || len(c.after) != 1 || c.legacyBefore+c.legacyAfter != 0 {
		t.Fatalf("concurrent boundary/traffic changed: %v", err)
	}
	if runtimeParameter(t, c.before[0], "ticket").State != "not_evaluated" {
		t.Fatal("concurrent before snapshot prepared child requests")
	}
	after := c.after[0]
	p := runtimeParameter(t, after, "ticket")
	if p.State != "observed" || runtimeValue(t, p.Value) != "42" || p.ObservedEventID != after.Location.EventID || runtimeValue(t, runtimeBinding(t, after, "left").Value) != "41" || runtimeValue(t, runtimeBinding(t, after, "right").Value) != "42" {
		t.Fatal("post-join snapshot missed actual preparations or joined extractions")
	}
}

func TestReplayRuntimeSnapshotRedactsLocalKnownValuesAndKeepsStructuralIndices(t *testing.T) {
	vu := runtimeTestVUser(nil, nil)
	vu.Variables = map[string]string{"password": "2", "neutral": "2", "slot2": "ordinary", "slot[REDACTED]": "other"}
	vu.replayRuntime.frames = []ReplayRuntimeFrame{{Kind: "foreach", Phase: "action", ActionIndex: 2, StepPath: []string{"loop[2]"}, IterationPath: []int{2}}}
	vu.recordReplayRuntimeRow("datasource", "source2", "row2", dataRowReceipt{ordinal: 2, selection: "sequential", state: "selected"})
	location := runtimeTestLocation()
	location.ActionIndex = 2
	location.ActionName = "Transaction2"
	location.ParentActionName = "Parent2"
	location.Target = "/value2"
	location.StepPath = []string{"default[2]"}
	location.IterationPath = []int{2}
	s := vu.captureReplayRuntimeSnapshot("before_action", location)
	if s.State != "complete" || runtimeBinding(t, s, "neutral").ValueState != "redacted" || runtimeValue(t, runtimeBinding(t, s, "neutral").Value) != "[REDACTED]" || s.Location.ActionName == location.ActionName || s.Location.ParentActionName == location.ParentActionName || s.Location.Target == location.Target {
		t.Fatal("snapshot-local privacy closure missed neutral/authored replicas")
	}
	if s.Location.ActionIndex != 2 || s.Location.StepPath[0] != "default[2]" || s.Frames[0].StepPath[0] != "loop[2]" || s.Frames[0].IterationPath[0] != 2 || *s.Rows[0].RowOrdinal != 2 || s.Rows[0].Selection != "sequential" || s.Location.Method != "GET" {
		t.Fatal("known short secret corrupted structural receipts")
	}
	duplicates := 0
	for _, v := range s.Variables {
		if v.Name == "slot[REDACTED]" {
			duplicates++
		}
	}
	if duplicates != 2 {
		t.Fatal("masked name collisions overwrote independent bindings")
	}
	if vu.RedactionConfig != nil || vu.Variables["neutral"] != "2" {
		t.Fatal("privacy projection mutated publication policy or execution bindings")
	}
	vu.Variables = map[string]string{"a": "local-secret-value", "z_password": "local-secret-value"}
	for i := 0; i < 300; i++ {
		vu.Variables[fmt.Sprintf("m%03d", i)] = "x"
	}
	vu.replayRuntime.frames = nil
	vu.replayRuntime.rows = map[string]ReplayRuntimeRow{}
	bounded := vu.captureReplayRuntimeSnapshot("before_action", runtimeTestLocation())
	if bounded.State != "bounded" || len(bounded.Variables) != 256 || bounded.Omissions.Variables != 46 || runtimeBinding(t, bounded, "a").ValueState != "redacted" {
		t.Fatal("display-bound omission disabled complete privacy closure")
	}
}

func TestReplayRuntimeSnapshotShowsExtractedSecret(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Session", "debug-session-secret")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	controller := &runtimeSnapshotController{t: t}
	script := &dsl.Script{Name: "Extractor", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: server.URL}, Actions: []dsl.Action{{Name: "Request", Type: "GET", Target: "/", Extractors: []dsl.Extractor{{Name: "session_token", Source: "header", Type: "header", Expression: "X-Session"}}}}}
	report, err := ReplayScript(context.Background(), script, ReplayOptions{IgnoreThinkTime: true, DebugController: controller, UnredactedDebug: true})
	if err != nil || report == nil || !report.Success || len(controller.after) != 1 {
		t.Fatalf("replay failed: %v", err)
	}
	binding := runtimeBinding(t, controller.after[0], "session_token")
	if binding.ValueState != "assigned" || runtimeValue(t, binding.Value) != "debug-session-secret" {
		t.Fatalf("live extractor value: %#v", binding)
	}
	ordinary, err := ReplayScript(context.Background(), script, ReplayOptions{IgnoreThinkTime: true})
	if err != nil || ordinary == nil || len(ordinary.Events) != 1 || ordinary.Events[0].Extractions["session_token"] != redactedPayloadValue {
		t.Fatal("ordinary replay report exposed the extractor value")
	}
}

func TestReplayRuntimeSnapshotShowsLiveValuesWithoutRedaction(t *testing.T) {
	vu := runtimeTestVUser([]dsl.Parameter{{Name: "password", Type: "counter"}}, nil)
	vu.unredactedDebug = true
	config, err := NewRedactionConfig([]RedactionRule{{Pattern: "secret", Replacement: "masked"}})
	if err != nil {
		t.Fatal(err)
	}
	vu.RedactionConfig = WithSecretValueRedaction(config, []string{"secret-value"})
	vu.Variables = map[string]string{"password": "secret-value", "neutral": "secret-value", "slot2": "ordinary", "slot[REDACTED]": "other"}
	vu.Parameters.runtimeObservations["password"] = parameterRuntimeObservation{state: "observed", value: "secret-value", eventID: "observed-event"}
	vu.replayRuntime.frames = []ReplayRuntimeFrame{{Kind: "foreach", Phase: "action", ActionIndex: 2, StepPath: []string{"loop[2]"}, IterationPath: []int{2}}}
	vu.recordReplayRuntimeRow("datasource", "source2", "row2", dataRowReceipt{ordinal: 2, selection: "sequential", state: "selected"})
	location := runtimeTestLocation()
	location.ActionIndex = 2
	location.ActionName = "secret-value"
	location.ParentActionName = "Parent2"
	location.Target = "/secret-value"
	location.StepPath = []string{"default[2]"}
	location.IterationPath = []int{2}
	s := vu.captureReplayRuntimeSnapshot("before_action", location)
	for _, name := range []string{"password", "neutral"} {
		v := runtimeBinding(t, s, name)
		if v.ValueState != "assigned" || runtimeValue(t, v.Value) != "secret-value" {
			t.Fatalf("binding %s: %#v", name, v)
		}
	}
	parameter := runtimeParameter(t, s, "password")
	if parameter.ValueState != "observed" || runtimeValue(t, parameter.Value) != "secret-value" {
		t.Fatalf("parameter: %#v", parameter)
	}
	if s.State != "complete" || s.Location.ActionName != location.ActionName || s.Location.Target != location.Target {
		t.Fatal("authored location was changed")
	}
	if s.Location.ActionIndex != 2 || s.Location.StepPath[0] != "default[2]" || s.Frames[0].StepPath[0] != "loop[2]" || s.Frames[0].IterationPath[0] != 2 || *s.Rows[0].RowOrdinal != 2 || s.Rows[0].Selection != "sequential" || s.Location.Method != "GET" {
		t.Fatal("structural receipts changed")
	}
	if runtimeBinding(t, s, "slot2").Name != "slot2" || runtimeBinding(t, s, "slot[REDACTED]").Name != "slot[REDACTED]" {
		t.Fatal("binding names were changed")
	}
	if vu.Variables["neutral"] != "secret-value" || vu.RedactionConfig == nil {
		t.Fatal("snapshot mutated execution state")
	}
	// Debug capture is independent of the normal publication redactor's health.
	vu.RedactionConfig = &RedactionConfig{runtimeSecretValues: map[string]struct{}{"secret-value": {}}}
	unavailableMatcher := vu.captureReplayRuntimeSnapshot("before_action", location)
	if unavailableMatcher.State != "complete" || runtimeValue(t, runtimeBinding(t, unavailableMatcher, "password").Value) != "secret-value" {
		t.Fatal("unusable publication matcher suppressed live debugger data")
	}

	vu.Variables = map[string]string{"a": "secret-value", "z_password": "secret-value"}
	for i := 0; i < 300; i++ {
		vu.Variables[fmt.Sprintf("m%03d", i)] = "x"
	}
	vu.Parameters = nil
	vu.replayRuntime.frames = nil
	vu.replayRuntime.rows = map[string]ReplayRuntimeRow{}
	bounded := vu.captureReplayRuntimeSnapshot("before_action", runtimeTestLocation())
	if bounded.State != "bounded" || len(bounded.Variables) != 256 || bounded.Omissions.Variables != 46 || runtimeValue(t, runtimeBinding(t, bounded, "a").Value) != "secret-value" {
		t.Fatal("bounded snapshot did not retain live value")
	}
}

func TestReplayRuntimeSnapshotCapsOmitWholeValuesAndBoundAllArrays(t *testing.T) {
	t.Run("unscannable values", func(t *testing.T) {
		vu := runtimeTestVUser(nil, nil)
		vu.Variables = map[string]string{"oversized": strings.Repeat("private-byte-", 400), "nul": "private\x00value", "invalid": string([]byte{0xff, 'x'})}
		s := vu.captureReplayRuntimeSnapshot("before_action", runtimeTestLocation())
		if s.State != "bounded" || s.Omissions.Values != 3 {
			t.Fatal("whole-value omission not counted")
		}
		for _, v := range s.Variables {
			if v.Value != nil || v.ValueState != "omitted" {
				t.Fatal("unscannable raw prefix was retained")
			}
		}
	})
	t.Run("oversized name is a display omission", func(t *testing.T) {
		vu := runtimeTestVUser(nil, nil)
		vu.Variables[strings.Repeat("n", 5<<10)] = "safe"
		s := vu.captureReplayRuntimeSnapshot("before_action", runtimeTestLocation())
		if s.State != "bounded" || s.RuntimeEvidenceUnavailable || s.Omissions.Values != 1 || len(s.Variables) != 1 || s.Variables[0].Name != "[OMITTED]" || runtimeValue(t, s.Variables[0].Value) != "safe" {
			t.Fatal("ordinary name bound became global work unavailability")
		}
	})
	t.Run("authored location bounds", func(t *testing.T) {
		vu := runtimeTestVUser(nil, nil)
		location := runtimeTestLocation()
		location.ActionName = strings.Repeat("n", 5<<10)
		s := vu.captureReplayRuntimeSnapshot("before_action", location)
		if s.State != "bounded" || s.Location.ActionName != "[OMITTED]" || s.Omissions.Values != 1 {
			t.Fatal("ordinary location bound became global work unavailability")
		}
		location.Target = strings.Repeat("x", maxReplayRuntimeInputBytes+1)
		s = vu.captureReplayRuntimeSnapshot("before_action", location)
		if s.State != "unavailable" || s.RuntimeEvidenceUnavailable || s.UnavailableReason != "work_limit" || s.Location.Target != "" {
			t.Fatal("location bypassed aggregate input cap")
		}
	})
	t.Run("combined entry cap", func(t *testing.T) {
		vu := runtimeTestVUser([]dsl.Parameter{{Name: "p", Type: "counter", Update: "each_occurrence"}}, nil)
		for i := 0; i < 80; i++ {
			vu.replayRuntime.frames = append(vu.replayRuntime.frames, ReplayRuntimeFrame{Kind: "control_loop", Phase: "action", ActionIndex: i})
			vu.recordReplayRuntimeRow("datasource", "src", fmt.Sprint(i), dataRowReceipt{ordinal: i + 1, selection: "sequential", state: "selected"})
		}
		for i := 0; i < 200; i++ {
			vu.Variables[fmt.Sprintf("v%03d", i)] = "x"
		}
		s := vu.captureReplayRuntimeSnapshot("before_action", runtimeTestLocation())
		if s.State != "bounded" || len(s.Frames) != 64 || len(s.Rows) != 64 || len(s.Frames)+len(s.Rows)+len(s.Parameters)+len(s.Variables) != 256 || s.Omissions.Frames != 16 || s.Omissions.Rows != 16 || s.Omissions.Variables != 73 {
			t.Fatal("combined/per-array entry bounds not enforced")
		}
	})
	t.Run("exact escaped JSON bytes", func(t *testing.T) {
		vu := runtimeTestVUser(nil, nil)
		for i := 0; i < 8; i++ {
			vu.Variables[fmt.Sprintf("v%d", i)] = strings.Repeat("&", 4000)
		}
		s := vu.captureReplayRuntimeSnapshot("before_action", runtimeTestLocation())
		b, err := json.Marshal(s)
		if err != nil || s.State != "bounded" || len(b) > MaxReplayRuntimeSnapshotJSONBytes || s.Omissions.Variables == 0 || !ValidateReplayRuntimeSnapshotBounds(s) {
			t.Fatal("HTML-escaped JSON byte budget was not admitted before allocation")
		}
	})
	t.Run("input/work closed", func(t *testing.T) {
		for _, tc := range []string{"entries", "bytes", "closure"} {
			vu := runtimeTestVUser(nil, nil)
			switch tc {
			case "entries":
				for i := 0; i <= maxReplayRuntimeInputEntries; i++ {
					vu.Variables[fmt.Sprint(i)] = "x"
				}
			case "bytes":
				vu.Variables["large"] = strings.Repeat("x", maxReplayRuntimeInputBytes+1)
			case "closure":
				vu.Variables["password"] = strings.Repeat("x", maxReplayRuntimeProjectionWork/512+1)
			}
			s := vu.captureReplayRuntimeSnapshot("before_action", runtimeTestLocation())
			if s.State != "unavailable" || s.UnavailableReason != "work_limit" || s.RuntimeEvidenceUnavailable || len(s.Variables) != 0 || s.Location.ActionName != "" {
				t.Fatal("ordinary work bound leaked detail or invoked credential sticky failure")
			}
		}
	})
	t.Run("classification closed", func(t *testing.T) {
		vu := runtimeTestVUser(nil, nil)
		vu.Variables["neutral"] = "secret"
		vu.RedactionConfig = &RedactionConfig{runtimeSecretValues: map[string]struct{}{"secret": {}}}
		s := vu.captureReplayRuntimeSnapshot("before_action", runtimeTestLocation())
		if !s.RuntimeEvidenceUnavailable || s.UnavailableReason != "publication_unavailable" || len(s.Variables) != 0 {
			t.Fatal("unclassifiable immutable matcher failed open")
		}
		vu.replayRuntime.workUnavailable = true
		s = vu.captureReplayRuntimeSnapshot("before_action", runtimeTestLocation())
		if !s.RuntimeEvidenceUnavailable {
			t.Fatal("work exhaustion concealed known classification failure")
		}
	})
	t.Run("custom expansion", func(t *testing.T) {
		vu := runtimeTestVUser(nil, nil)
		config, err := NewRedactionConfig([]RedactionRule{{Pattern: "^(a+)$", Replacement: strings.Repeat("$1", 1000)}})
		if err != nil {
			t.Fatal(err)
		}
		vu.RedactionConfig = config
		vu.Variables["neutral"] = strings.Repeat("a", 100)
		s := vu.captureReplayRuntimeSnapshot("before_action", runtimeTestLocation())
		if s.State != "bounded" || runtimeBinding(t, s, "neutral").Value != nil || s.Omissions.Values == 0 {
			t.Fatal("amplifying custom rule retained partial raw value")
		}
	})
}

func TestReplayRuntimeSnapshotValidationCloneOwnershipAndExactSize(t *testing.T) {
	vu := runtimeTestVUser(nil, nil)
	vu.Variables["x"] = ""
	s := vu.captureReplayRuntimeSnapshot("before_action", runtimeTestLocation())
	backing := strings.Repeat("0123456789", 100000)
	name := backing[3:7]
	value := backing[11:15]
	s.Variables = []ReplayRuntimeBinding{{Name: name, ValueState: "assigned", Value: &value}}
	s.Location.ActionName = name
	s.Location.StepPath = []string{"default[2]"}
	index := 2
	s.Location.RequestCount = 3
	s.Location.RequestIndex = &index
	s.Frames = []ReplayRuntimeFrame{{Kind: "parallel_branch", Phase: "action", ActionIndex: 0, BranchIndex: &index, StepPath: []string{"branches[2].steps[0]"}}}
	ordinal := 2
	s.Rows = []ReplayRuntimeRow{{Kind: "datasource", Source: name, Name: name, State: "selected", Selection: "sequential", RowOrdinal: &ordinal}}
	cloned := CloneReplayRuntimeSnapshot(s)
	if !ValidateReplayRuntimeSnapshotBounds(cloned) || unsafe.StringData(cloned.Variables[0].Name) == unsafe.StringData(name) || unsafe.StringData(cloned.Location.ActionName) == unsafe.StringData(name) || unsafe.StringData(cloned.Rows[0].Source) == unsafe.StringData(name) || unsafe.StringData(*cloned.Variables[0].Value) == unsafe.StringData(value) {
		t.Fatal("clone retained large caller string backing storage")
	}
	*cloned.Variables[0].Value = "changed"
	cloned.Location.StepPath[0] = "default[3]"
	*cloned.Location.RequestIndex = 1
	*cloned.Frames[0].BranchIndex = 1
	*cloned.Rows[0].RowOrdinal = 1
	if *s.Variables[0].Value != value || s.Location.StepPath[0] != "default[2]" || *s.Location.RequestIndex != 2 || *s.Frames[0].BranchIndex != 2 || *s.Rows[0].RowOrdinal != 2 {
		t.Fatal("clone shared mutable value/index/path ownership")
	}
	for _, text := range []string{"", "\n\t\b\f\r\"\\<&>", "é界\u2028\u2029"} {
		sample := CloneReplayRuntimeSnapshot(s)
		sample.Variables[0].Value = &text
		b, _ := json.Marshal(sample)
		n, ok := replayRuntimeJSONSize(sample)
		if !ok || n != len(b) {
			t.Fatal("preallocation JSON byte counter disagrees with actual encoding")
		}
	}
	for _, mutate := range []func(*ReplayRuntimeSnapshot){func(s *ReplayRuntimeSnapshot) { s.Variables = nil }, func(s *ReplayRuntimeSnapshot) { s.State = "surprise" }, func(s *ReplayRuntimeSnapshot) { s.Rows[0].Selection = "future" }, func(s *ReplayRuntimeSnapshot) { s.Frames[0].Kind = "unknown" }, func(s *ReplayRuntimeSnapshot) { s.Location.StepPath = []string{"customer-authored-secret"} }, func(s *ReplayRuntimeSnapshot) { s.Variables[0].ValueState = "omitted" }, func(s *ReplayRuntimeSnapshot) {
		v := strings.Repeat("x", MaxReplayRuntimeSnapshotValueBytes+1)
		s.Variables[0].Value = &v
	}} {
		invalid := CloneReplayRuntimeSnapshot(s)
		mutate(&invalid)
		if ValidateReplayRuntimeSnapshotBounds(invalid) {
			t.Fatal("invalid closed snapshot contract admitted")
		}
		safe := CloneReplayRuntimeSnapshot(invalid)
		if safe.State != "unavailable" || safe.Location.ActionName != "" || len(safe.Variables) != 0 {
			t.Fatal("invalid clone retained raw input")
		}
	}
	invalid := s
	invalid.RuntimeEvidenceUnavailable = true
	safe := CloneReplayRuntimeSnapshot(invalid)
	if !safe.RuntimeEvidenceUnavailable || safe.UnavailableReason != "publication_unavailable" {
		t.Fatal("invalid clone lost sticky privacy signal")
	}
}
