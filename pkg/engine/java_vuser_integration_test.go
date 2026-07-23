package engine

import (
	"context"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	_ "github.com/jasonczech/apexload/pkg/protocol/java_vuser"
)

type javaVuserParameterCaptureProtocol struct {
	actions []*protocol.Action
}

func (*javaVuserParameterCaptureProtocol) Name() string { return "java_vuser" }
func (*javaVuserParameterCaptureProtocol) Init(context.Context, map[string]any) error {
	return nil
}
func (p *javaVuserParameterCaptureProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	cloned := *action
	cloned.Params = cloneAnyMapForTest(action.Params)
	cloned.RedactedParams = cloneAnyMapForTest(action.RedactedParams)
	p.actions = append(p.actions, &cloned)
	return &protocol.Result{ActionName: action.Name, Success: true, Duration: time.Millisecond}, nil
}
func (*javaVuserParameterCaptureProtocol) Teardown(context.Context) error    { return nil }
func (*javaVuserParameterCaptureProtocol) HealthCheck(context.Context) error { return nil }

func cloneAnyMapForTest(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	cloned := make(map[string]any, len(input))
	for key, value := range input {
		switch typed := value.(type) {
		case map[string]any:
			cloned[key] = cloneAnyMapForTest(typed)
		default:
			cloned[key] = value
		}
	}
	return cloned
}

func TestJavaVuserLifecycleActionsReceiveCurrentRuntimeParameters(t *testing.T) {
	script := &dsl.Script{
		Name:     "combined-java",
		Protocol: "java_vuser",
		Config: dsl.ScriptConfig{
			RunLogic: dsl.RunLogicConfig{Iterations: 2},
		},
		DataSources: []dsl.DataSource{{
			Name:           "users",
			Type:           "csv",
			InlineData:     "username\nalice\nbob\n",
			Strategy:       "sequential",
			FirstRowHeader: true,
		}},
		Parameters: []dsl.Parameter{
			{Name: "LoginUser", Type: "file", Update: "each_iteration", DataSource: "users", Column: "username"},
			{Name: "Iteration", Type: "current_iteration", Update: "each_iteration", Start: 1, Step: 1},
		},
		Init: []dsl.Action{{
			Name:   "Actions.init",
			Type:   "java_vuser_run",
			Params: map[string]any{"method": "init"},
		}},
		Actions: []dsl.Action{{
			Name: "Actions.action",
			Type: "java_vuser_run",
			Params: map[string]any{
				"method": "action",
				"lr_parameters": map[string]any{
					"LoginUser":  "explicit-action-user",
					"CallerOnly": "present",
				},
			},
			Metadata: map[string]any{"emits_business_transactions": true},
		}},
		Teardown: []dsl.Action{{
			Name:   "Actions.end",
			Type:   "java_vuser_run",
			Params: map[string]any{"method": "end"},
		}},
	}
	provider, err := NewDataProvider(script.DataSources)
	if err != nil {
		t.Fatalf("NewDataProvider: %v", err)
	}
	adapter := &javaVuserParameterCaptureProtocol{}
	vu := &VUser{
		ID:           0,
		Group:        "java",
		Proto:        adapter,
		Script:       script,
		Metrics:      NewMetricsCollector(32),
		Variables:    map[string]string{"transient_secret": "must-not-be-forwarded"},
		DataProvider: provider,
	}

	vu.Run(context.Background())

	if len(adapter.actions) != 4 {
		t.Fatalf("executed lifecycle actions = %d, want init + two actions + end", len(adapter.actions))
	}
	assertJavaLRParameters(t, adapter.actions[0], map[string]any{
		"LoginUser": "alice", "Iteration": "1", "users.username": "alice",
	})
	assertJavaLRParameters(t, adapter.actions[1], map[string]any{
		"LoginUser": "explicit-action-user", "Iteration": "1", "users.username": "alice", "CallerOnly": "present",
	})
	assertJavaLRParameters(t, adapter.actions[2], map[string]any{
		"LoginUser": "explicit-action-user", "Iteration": "2", "users.username": "bob", "CallerOnly": "present",
	})
	assertJavaLRParameters(t, adapter.actions[3], map[string]any{
		"LoginUser": "bob", "Iteration": "2", "users.username": "bob",
	})
	for _, action := range adapter.actions {
		params, _ := stringAnyMap(action.Params["lr_parameters"])
		if _, exposed := params["transient_secret"]; exposed {
			t.Fatalf("%s exposed an undeclared transient variable to Java: %#v", action.Name, params)
		}
		redacted, ok := stringAnyMap(action.RedactedParams["lr_parameters"])
		if !ok {
			t.Fatalf("%s redacted lr_parameters = %#v", action.Name, action.RedactedParams["lr_parameters"])
		}
		if _, automatic := params["Iteration"]; automatic && redacted["Iteration"] != "[REDACTED]" {
			t.Fatalf("%s automatic parameters were not redacted: %#v", action.Name, redacted)
		}
	}
}

func assertJavaLRParameters(t *testing.T, action *protocol.Action, expected map[string]any) {
	t.Helper()
	params, ok := stringAnyMap(action.Params["lr_parameters"])
	if !ok {
		t.Fatalf("%s lr_parameters = %#v, want an object", action.Name, action.Params["lr_parameters"])
	}
	for name, want := range expected {
		if got := params[name]; got != want {
			t.Errorf("%s lr_parameters[%q] = %#v, want %#v; all params: %#v", action.Name, name, got, want, params)
		}
	}
}

// each_occurrence parameters must advance only where LoadRunner would consume a
// row: iteration-phase java_vuser_run invocations. init/end invocations and
// names overridden by explicitly authored lr_parameters must not burn rows.
func TestJavaVuserEachOccurrenceParametersAdvanceOnlyForIterationMethods(t *testing.T) {
	script := &dsl.Script{
		Name:     "combined-java",
		Protocol: "java_vuser",
		Config: dsl.ScriptConfig{
			RunLogic: dsl.RunLogicConfig{Iterations: 2},
		},
		Parameters: []dsl.Parameter{
			{Name: "Ticket", Type: "list", Update: "each_occurrence", Selection: "unique", OnExhaustion: "empty", Values: []string{"t1", "t2", "t3", "t4"}},
			{Name: "Fixed", Type: "list", Update: "each_occurrence", Selection: "unique", Values: []string{"f1", "f2"}},
		},
		Init: []dsl.Action{{
			Name:   "Actions.init",
			Type:   "java_vuser_run",
			Params: map[string]any{"method": "init"},
		}},
		Actions: []dsl.Action{{
			Name: "Actions.action",
			Type: "java_vuser_run",
			Params: map[string]any{
				"method":        "action",
				"lr_parameters": map[string]any{"Fixed": "explicit"},
			},
			Metadata: map[string]any{"emits_business_transactions": true},
		}},
		Teardown: []dsl.Action{{
			Name:   "Actions.end",
			Type:   "java_vuser_run",
			Params: map[string]any{"method": "end"},
		}},
	}
	adapter := &javaVuserParameterCaptureProtocol{}
	vu := &VUser{
		ID:        0,
		Group:     "java",
		Proto:     adapter,
		Script:    script,
		Metrics:   NewMetricsCollector(32),
		Variables: map[string]string{},
	}

	vu.Run(context.Background())

	if len(adapter.actions) != 4 {
		t.Fatalf("executed lifecycle actions = %d, want init + two actions + end", len(adapter.actions))
	}
	assertJavaLRParameters(t, adapter.actions[1], map[string]any{"Ticket": "t1", "Fixed": "explicit"})
	assertJavaLRParameters(t, adapter.actions[2], map[string]any{"Ticket": "t2", "Fixed": "explicit"})
	for _, index := range []int{0, 3} {
		params, _ := stringAnyMap(adapter.actions[index].Params["lr_parameters"])
		if _, consumed := params["Ticket"]; consumed {
			t.Fatalf("%s consumed an each_occurrence row outside the iteration phase: %#v", adapter.actions[index].Name, params)
		}
	}
	if got := vu.Parameters.states["Ticket"].nextIndex; got != 2 {
		t.Fatalf("Ticket rows consumed = %d, want exactly one per iteration-phase invocation (2)", got)
	}
	if got := vu.Parameters.states["Fixed"].nextIndex; got != 0 {
		t.Fatalf("Fixed rows consumed = %d, want none for an explicitly overridden name", got)
	}
}

// scriptHasExplicitTransactions must recurse into control-flow bodies so a
// transaction-emitting java_vuser_run wrapped in a loop still suppresses the
// automatic whole-iteration transaction (which would double-count).
func TestScriptHasExplicitTransactionsRecursesControlFlow(t *testing.T) {
	nested := &dsl.Script{Actions: []dsl.Action{{
		Type: "control_loop",
		Control: &dsl.ControlFlow{Count: 5, Steps: []dsl.Action{{
			Type:     "java_vuser_run",
			Metadata: map[string]any{"emits_business_transactions": true},
			Params:   map[string]any{"method": "action"},
		}}},
	}}}
	if !scriptHasExplicitTransactions(nested) {
		t.Fatal("scriptHasExplicitTransactions missed a marked java_vuser_run inside control_loop steps")
	}
	elseBranch := &dsl.Script{Actions: []dsl.Action{{
		Type: "control_if",
		Control: &dsl.ControlFlow{Else: []dsl.Action{{
			Type:     "java_vuser_run",
			Metadata: map[string]any{"emits_business_transactions": true},
		}}},
	}}}
	if !scriptHasExplicitTransactions(elseBranch) {
		t.Fatal("scriptHasExplicitTransactions missed a marked java_vuser_run inside a control_if else branch")
	}
	unmarked := &dsl.Script{Actions: []dsl.Action{{
		Type: "control_loop",
		Control: &dsl.ControlFlow{Count: 2, Steps: []dsl.Action{{
			Type: "java_vuser_run",
		}}},
	}}}
	if scriptHasExplicitTransactions(unmarked) {
		t.Fatal("scriptHasExplicitTransactions claimed transactions for an unmarked nested java_vuser_run")
	}
}

// Phase lifecycle scripts bind init/action/end to one persistent Actions
// instance per VUser, so group-level at_once initialization would split that
// lifecycle across different instances and must be rejected.
func TestJavaVuserAtOnceInitRejectsPersistentPhaseLifecycle(t *testing.T) {
	phaseScript := &dsl.Script{
		Name:     "phase-java",
		Protocol: "java_vuser",
		Init: []dsl.Action{{
			Name:   "Actions.init",
			Type:   "java_vuser_run",
			Target: "Actions",
			Params: map[string]any{"method": "init"},
		}},
		Actions: []dsl.Action{{
			Name:   "Actions.action",
			Type:   "java_vuser_run",
			Target: "Actions",
			Params: map[string]any{"method": "action"},
		}},
	}
	if _, err := NewMultiGroup(MultiGroupConfig{
		Duration: time.Second,
		Groups:   []GroupConfig{{Name: "persistent-local", Script: phaseScript, VUsers: 1, InitStrategy: "at_once"}},
	}); err == nil {
		t.Fatal("NewMultiGroup(at_once persistent phases) succeeded, want lifecycle rejection")
	}

	// A method-less java_vuser_run defaults to the one-shot full_lifecycle
	// sequence and remains valid with at_once initialization.
	legacyScript := &dsl.Script{
		Name:     "legacy-java",
		Protocol: "java_vuser",
		Init: []dsl.Action{{
			Name:   "Warmup",
			Type:   "java_vuser_run",
			Target: "Actions",
		}},
		Actions: []dsl.Action{{
			Name:   "Run Java Lifecycle",
			Type:   "java_vuser_run",
			Target: "Actions",
			Params: map[string]any{"method": "full_lifecycle"},
		}},
	}
	if _, err := NewMultiGroup(MultiGroupConfig{
		Duration: time.Second,
		Groups:   []GroupConfig{{Name: "legacy", Script: legacyScript, VUsers: 1, InitStrategy: "at_once"}},
	}); err != nil {
		t.Fatalf("NewMultiGroup(at_once one-shot) = %v, want legacy scripts to keep at_once", err)
	}
}

func TestJavaVuserPersistentCapacityPlanUsesMaximumLiveOpenWorkers(t *testing.T) {
	tests := []struct {
		name    string
		script  *dsl.Script
		vusers  int
		want    int64
		persist bool
	}{
		{
			name: "closed phase lifecycle",
			script: &dsl.Script{
				Protocol: "java_vuser",
				Actions:  []dsl.Action{{Type: "java_vuser_run", Params: map[string]any{"method": "action"}}},
			},
			vusers:  7,
			want:    7,
			persist: true,
		},
		{
			name: "explicit open max in flight",
			script: &dsl.Script{
				Protocol: "java_vuser",
				Config: dsl.ScriptConfig{
					RunLogic: dsl.RunLogicConfig{WorkloadModel: "open", MaxInFlight: 11},
					Pacing:   dsl.PacingConfig{Type: "tps", TPS: 2},
				},
				Actions: []dsl.Action{{Type: "java_vuser_run", Params: map[string]any{"method": "action"}}},
			},
			vusers:  2,
			want:    11,
			persist: true,
		},
		{
			name: "finite arrivals bound open max",
			script: &dsl.Script{
				Protocol: "java_vuser",
				Config: dsl.ScriptConfig{
					RunLogic: dsl.RunLogicConfig{WorkloadModel: "open", MaxInFlight: 50, Iterations: 2},
					Pacing:   dsl.PacingConfig{Type: "tps", TPS: 5},
				},
				Actions: []dsl.Action{{Type: "java_vuser_run", Params: map[string]any{"method": "action"}}},
			},
			vusers:  3,
			want:    6,
			persist: true,
		},
		{
			name: "global arrivals are the tighter bound",
			script: &dsl.Script{
				Protocol: "java_vuser",
				Config: dsl.ScriptConfig{
					RunLogic: dsl.RunLogicConfig{WorkloadModel: "open", MaxInFlight: 50, Iterations: 10, GlobalIterations: 4},
					Pacing:   dsl.PacingConfig{Type: "tps", TPS: 5},
				},
				Actions: []dsl.Action{{Type: "java_vuser_run", Params: map[string]any{"method": "action"}}},
			},
			vusers:  3,
			want:    4,
			persist: true,
		},
		{
			name: "legacy full lifecycle bypasses pinned capacity",
			script: &dsl.Script{
				Protocol: "java_vuser",
				Actions:  []dsl.Action{{Type: "java_vuser_run", Params: map[string]any{"method": "full_lifecycle"}}},
			},
			vusers: 25,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := NewJavaVuserCapacityPlan(test.script)
			if got := plan.RequiresPersistentLifecycle(); got != test.persist {
				t.Fatalf("RequiresPersistentLifecycle = %t, want %t", got, test.persist)
			}
			if got := plan.Demand(test.vusers); got != test.want {
				t.Fatalf("Demand(%d) = %d, want %d", test.vusers, got, test.want)
			}
		})
	}
}

func TestPersistentJavaScaleCapacityKeepsSessionsThatAreStillStopping(t *testing.T) {
	persistent := &dsl.Script{
		Protocol: "java_vuser",
		Actions:  []dsl.Action{{Type: "java_vuser_run", Params: map[string]any{"method": "action"}}},
	}
	oneShot := &dsl.Script{
		Protocol: "java_vuser",
		Actions:  []dsl.Action{{Type: "java_vuser_run", Params: map[string]any{"method": "full_lifecycle"}}},
	}
	groups := []GroupConfig{
		{Name: "persistent", Script: persistent, VUsers: 5},
		{Name: "legacy", Script: oneShot, VUsers: 100},
	}
	// Ten old sessions are still live after a prior 10 -> 5 scale-down. A
	// subsequent 5 -> 8 reservation starts three more sessions before those old
	// sessions are guaranteed reaped, so the safe projected demand is 13.
	if got := persistentJavaScaleCapacityDemand(groups, map[string]int64{"persistent": 10, "legacy": 100}, 0, 8); got != 13 {
		t.Fatalf("persistent scale capacity demand = %d, want 13", got)
	}

	open := &dsl.Script{
		Protocol: "java_vuser",
		Config: dsl.ScriptConfig{
			RunLogic: dsl.RunLogicConfig{WorkloadModel: "open", MaxInFlight: 5},
			Pacing:   dsl.PacingConfig{Type: "tps", TPS: 1},
		},
		Actions: []dsl.Action{{Type: "java_vuser_run", Params: map[string]any{"method": "action"}}},
	}
	openGroups := []GroupConfig{{Name: "open", Script: open, VUsers: 1}}
	if got := persistentJavaScaleCapacityDemand(openGroups, map[string]int64{"open": 5}, 0, 2); got != 5 {
		t.Fatalf("open persistent scale capacity demand = %d, want capped demand 5", got)
	}
}

type javaLikeStreamingProtocol struct{}

func (*javaLikeStreamingProtocol) Name() string { return "java_vuser" }
func (*javaLikeStreamingProtocol) Init(context.Context, map[string]any) error {
	return nil
}
func (*javaLikeStreamingProtocol) Execute(context.Context, *protocol.Action) (*protocol.Result, error) {
	panic("streaming protocol must use ExecuteStream")
}
func (*javaLikeStreamingProtocol) ExecuteStream(_ context.Context, action *protocol.Action, emit func(*protocol.Result) error) (*protocol.Result, error) {
	if err := emit(&protocol.Result{
		ActionName:          "Login",
		BusinessTransaction: true,
		Success:             true,
		Duration:            7 * time.Millisecond,
	}); err != nil {
		return nil, err
	}
	if err := emit(&protocol.Result{
		ActionName:          "Checkout",
		BusinessTransaction: true,
		Success:             false,
		ErrorMessage:        "transaction failed",
		Duration:            11 * time.Millisecond,
	}); err != nil {
		return nil, err
	}
	return &protocol.Result{ActionName: action.Name, Success: true, Duration: 20 * time.Millisecond}, nil
}
func (*javaLikeStreamingProtocol) Teardown(context.Context) error    { return nil }
func (*javaLikeStreamingProtocol) HealthCheck(context.Context) error { return nil }

func TestJavaStreamingBusinessTransactionsUpdateRunTotalsWithoutDuplicateSinkRecords(t *testing.T) {
	metrics := NewMetricsCollector(16)
	sink := &sequenceCaptureSink{}
	vu := &VUser{
		ID:         1,
		Group:      "java",
		Proto:      &javaLikeStreamingProtocol{},
		Metrics:    metrics,
		ResultSink: sink,
		Variables:  map[string]string{},
		Script: &dsl.Script{
			Name:     "combined-java",
			Protocol: "java_vuser",
			Config:   dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}},
			Actions: []dsl.Action{{
				Name:     "Actions.action",
				Type:     "java_vuser_run",
				Metadata: map[string]any{"emits_business_transactions": true},
			}},
		},
	}

	vu.Run(context.Background())

	if got := metrics.TotalTransactions(); got != 1 {
		t.Fatalf("TotalTransactions = %d, want 1", got)
	}
	if got := metrics.TotalErrors(); got != 1 {
		t.Fatalf("TotalErrors = %d, want 1", got)
	}
	if successes, failures := metrics.GroupRunTotals("java"); successes != 1 || failures != 1 {
		t.Fatalf("GroupRunTotals(java) = %d successes, %d failures; want 1, 1", successes, failures)
	}
	if len(sink.results) != 3 {
		t.Fatalf("ResultSink records = %d, want two transaction children and one aggregate", len(sink.results))
	}
	if len(sink.transactions) != 0 {
		t.Fatalf("TransactionSink records = %d, want no second sink delivery for streamed children", len(sink.transactions))
	}
	if len(metrics.ch) != 3 {
		t.Fatalf("queued metric samples = %d, want exactly three (no duplicate transactions)", len(metrics.ch))
	}

	recorded := make([]*protocol.Result, 0, len(metrics.ch))
	for len(metrics.ch) > 0 {
		recorded = append(recorded, <-metrics.ch)
	}
	snapshot := metrics.computeSnapshot(recorded, time.Second)
	if snapshot.Transactions != 1 || snapshot.Errors != 1 {
		t.Fatalf("snapshot transactions/errors = %d/%d, want 1/1", snapshot.Transactions, snapshot.Errors)
	}
	if snapshot.ByTransaction["Login"] == nil || snapshot.ByTransaction["Checkout"] == nil {
		t.Fatalf("snapshot transaction dimensions = %#v", snapshot.ByTransaction)
	}
}
