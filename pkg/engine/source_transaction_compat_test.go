package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func sourceTransactionCall(function string, args ...any) dsl.Action {
	return dsl.Action{
		Name: function,
		Type: "source_transaction",
		Params: map[string]any{
			"function": function,
			"args":     args,
		},
	}
}

func openSourceTransaction(t *testing.T, vu *VUser, name string) {
	t.Helper()
	if err := vu.handleSourceTransactionAction(sourceTransactionCall("lr_start_transaction", name)); err != nil {
		t.Fatalf("start %q: %v", name, err)
	}
}

func openSourceSubTransaction(t *testing.T, vu *VUser, name, parent string) {
	t.Helper()
	if err := vu.handleSourceTransactionAction(sourceTransactionCall("lr_start_sub_transaction", name, parent)); err != nil {
		t.Fatalf("start subtransaction %q under %q: %v", name, parent, err)
	}
}

func TestSourceTransactionStatusSettersUseLoadRunnerArgumentOrderAndScope(t *testing.T) {
	vu := &VUser{Variables: map[string]string{}}
	openSourceTransaction(t, vu, "Outer")
	openSourceSubTransaction(t, vu, "Inner", "Outer")

	if err := vu.handleSourceTransactionAction(sourceTransactionCall("lr_set_transaction_status", "LR_FAIL")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"outer", "inner"} {
		if state := vu.sourceTransactions[name]; state == nil || state.status != sourceTransactionFail {
			t.Fatalf("%s state = %#v, want failed", name, state)
		}
	}

	if err := vu.handleSourceTransactionAction(sourceTransactionCall("lr_set_transaction_status_by_name", "LR_PASS", "Inner")); err != nil {
		t.Fatal(err)
	}
	if vu.sourceTransactions["outer"].status != sourceTransactionFail ||
		vu.sourceTransactions["inner"].status != sourceTransactionPass {
		t.Fatalf("states = %#v, by-name status setter changed the wrong scope", vu.sourceTransactions)
	}

	outerBefore := *vu.sourceTransactions["outer"]
	innerBefore := *vu.sourceTransactions["inner"]
	if err := vu.handleSourceTransactionAction(sourceTransactionCall("lr_set_transaction_status_by_name", "Inner", "LR_STOP")); err == nil {
		t.Fatal("reversed status/name arguments unexpectedly succeeded")
	}
	if *vu.sourceTransactions["outer"] != outerBefore || *vu.sourceTransactions["inner"] != innerBefore {
		t.Fatal("malformed status setter mutated an open transaction")
	}

	if err := vu.handleSourceTransactionAction(sourceTransactionCall("lr_set_transaction_status", "LR_STOP")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"outer", "inner"} {
		state := vu.sourceTransactions[name]
		if state.status != sourceTransactionStop ||
			vu.Variables[sourceTransactionStatusKey(state.name)] != "false" {
			t.Fatalf("%s state/status = %#v / %q, want unsuccessful STOP", name, state, vu.Variables[sourceTransactionStatusKey(state.name)])
		}
	}
}

func TestFailedLeafMarksEveryRuntimeOpenTransaction(t *testing.T) {
	vu := &VUser{Variables: map[string]string{}}
	openSourceTransaction(t, vu, "Outer")
	openSourceSubTransaction(t, vu, "Inner", "Outer")

	vu.noteSourceTransactionResult(dsl.Action{
		Name:     "failed leaf",
		Metadata: map[string]any{"source_transaction": "Inner"},
	}, false)

	for _, name := range []string{"outer", "inner"} {
		state := vu.sourceTransactions[name]
		if state.status != sourceTransactionFail ||
			vu.Variables[sourceTransactionStatusKey(state.name)] != "false" {
			t.Fatalf("%s state/status = %#v / %q, want failed enclosing scope", name, state, vu.Variables[sourceTransactionStatusKey(state.name)])
		}
	}
}

func TestFailTransWithErrorMarksAllOpenTransactionsWithoutEndingThem(t *testing.T) {
	sink := &sequenceCaptureSink{}
	vu := &VUser{Variables: map[string]string{}, ResultSink: sink}
	openSourceTransaction(t, vu, "Outer")
	openSourceSubTransaction(t, vu, "Inner", "Outer")

	if err := vu.handleSourceTransactionAction(sourceTransactionCall(
		"lr_fail_trans_with_error", "request %s failed", "checkout",
	)); err != nil {
		t.Fatal(err)
	}
	if len(vu.sourceTransactions) != 2 || len(sink.transactions) != 0 {
		t.Fatalf("open states/transactions = %d/%d, failure marker must not close a transaction", len(vu.sourceTransactions), len(sink.transactions))
	}

	if err := vu.handleSourceTransactionAction(sourceTransactionCall("lr_end_sub_transaction", "Inner", "LR_AUTO")); err != nil {
		t.Fatal(err)
	}
	if err := vu.handleSourceTransactionAction(sourceTransactionCall("lr_end_transaction", "Outer", "LR_AUTO")); err != nil {
		t.Fatal(err)
	}
	if len(sink.transactions) != 2 {
		t.Fatalf("transactions = %#v, want independently completed parent and child", sink.transactions)
	}
	for _, transaction := range sink.transactions {
		if transaction.Success {
			t.Fatalf("transaction = %#v, lr_fail_trans_with_error must make LR_AUTO fail", transaction)
		}
	}
}

func TestSourceTransactionStopAndResumeDurationSemantics(t *testing.T) {
	t.Run("missing name does not mutate state", func(t *testing.T) {
		vu := &VUser{Variables: map[string]string{}}
		openSourceTransaction(t, vu, "Checkout")
		before := *vu.sourceTransactions["checkout"]
		if err := vu.handleSourceTransactionAction(sourceTransactionCall("lr_stop_transaction")); err == nil {
			t.Fatal("nameless stop unexpectedly succeeded")
		}
		if *vu.sourceTransactions["checkout"] != before {
			t.Fatal("malformed stop mutated the open transaction")
		}
		if err := vu.handleSourceTransactionAction(sourceTransactionCall("lr_resume_transaction")); err == nil {
			t.Fatal("nameless resume unexpectedly succeeded")
		}
		if *vu.sourceTransactions["checkout"] != before {
			t.Fatal("malformed resume mutated the open transaction")
		}
	})

	t.Run("unresumed stop caps duration", func(t *testing.T) {
		sink := &sequenceCaptureSink{}
		vu := &VUser{Variables: map[string]string{}, ResultSink: sink}
		openSourceTransaction(t, vu, "Checkout")
		state := vu.sourceTransactions["checkout"]
		state.startedAt = time.Now().Add(-200 * time.Millisecond)
		state.thinkTime = 20 * time.Millisecond

		if err := vu.handleSourceTransactionAction(sourceTransactionCall("lr_stop_transaction", "Checkout")); err != nil {
			t.Fatal(err)
		}
		if err := vu.handleSourceTransactionAction(sourceTransactionCall("lr_end_transaction", "Checkout", "LR_AUTO")); err != nil {
			t.Fatal(err)
		}
		if len(sink.transactions) != 1 {
			t.Fatalf("transactions = %#v", sink.transactions)
		}
		duration := sink.transactions[0].Duration
		if duration < 160*time.Millisecond || duration > 220*time.Millisecond {
			t.Fatalf("duration = %v, want start-to-stop minus think time", duration)
		}
	})

	t.Run("resume includes stopped interval", func(t *testing.T) {
		sink := &sequenceCaptureSink{}
		vu := &VUser{Variables: map[string]string{}, ResultSink: sink}
		openSourceTransaction(t, vu, "Checkout")
		state := vu.sourceTransactions["checkout"]
		state.startedAt = time.Now().Add(-200 * time.Millisecond)
		state.thinkTime = 30 * time.Millisecond

		if err := vu.handleSourceTransactionAction(sourceTransactionCall("lr_stop_transaction", "Checkout")); err != nil {
			t.Fatal(err)
		}
		// The exact stop timestamp is irrelevant after resume: LoadRunner reports
		// the full start-to-end interval, including time spent stopped.
		state.stoppedAt = time.Now().Add(-100 * time.Millisecond)
		if err := vu.handleSourceTransactionAction(sourceTransactionCall("lr_resume_transaction", "Checkout")); err != nil {
			t.Fatal(err)
		}
		if err := vu.handleSourceTransactionAction(sourceTransactionCall("lr_end_transaction", "Checkout", "LR_AUTO")); err != nil {
			t.Fatal(err)
		}
		duration := sink.transactions[0].Duration
		if duration < 150*time.Millisecond || duration > 220*time.Millisecond {
			t.Fatalf("duration = %v, want full elapsed interval minus think time", duration)
		}
	})
}

func TestSourceSetTransactionRecordsExactCompletedTransactionAndStopsAtGoal(t *testing.T) {
	runLogic := dsl.RunLogicConfig{
		Iterations:       1,
		MaxTransactions:  1,
		AutoTransactions: "none",
	}
	softCtx, softCancel := context.WithCancel(context.Background())
	defer softCancel()
	protoStub := &sourceSemanticsProtocol{}
	sink := &sequenceCaptureSink{}
	vu := &VUser{
		Proto:          protoStub,
		ResultSink:     sink,
		ExecutionGoals: NewExecutionGoalLimiter(runLogic, softCancel),
		HardStopCtx:    context.Background(),
		Script: &dsl.Script{
			Name:     "set-transaction-goal",
			Protocol: "http",
			Config:   dsl.ScriptConfig{RunLogic: runLogic},
			Actions: []dsl.Action{
				sourceTransactionCall("lr_set_transaction", "Manual", 1.25, "LR_PASS"),
				{Name: "after goal", Type: "GET", Method: "GET", Target: "https://example.test/after"},
			},
		},
	}

	vu.Run(softCtx)

	if len(sink.transactions) != 1 {
		t.Fatalf("transactions = %#v, want one immediate transaction", sink.transactions)
	}
	transaction := sink.transactions[0]
	if transaction.ActionName != "Manual" || transaction.Duration != 1250*time.Millisecond || !transaction.Success {
		t.Fatalf("transaction = %#v, want exact 1.25s PASS transaction", transaction)
	}
	if len(protoStub.executed) != 0 {
		t.Fatalf("executed = %v, next request ran after the exact transaction goal", protoStub.executed)
	}
}

func TestFailedSourceSetTransactionDoesNotTransferReplayControl(t *testing.T) {
	var executed []string
	protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		executed = append(executed, action.Name)
		return &protocol.Result{ActionName: action.Name, Success: true, Duration: time.Millisecond}, nil
	})
	report, err := ReplayScript(context.Background(), &dsl.Script{
		Name:     "failed-set-transaction",
		Protocol: protocolName,
		Actions: []dsl.Action{
			sourceTransactionCall("lr_set_transaction", "Manual", 0.5, "LR_FAIL"),
			{Name: "after failed status", Type: "GET", Method: "GET", Target: "https://example.test/after"},
		},
	}, ReplayOptions{IgnoreThinkTime: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.Success || len(report.Events) != 2 || report.Events[0].Success {
		t.Fatalf("report = %#v, want a failed transaction event followed by normal control flow", report)
	}
	if len(executed) != 1 || executed[0] != "after failed status" {
		t.Fatalf("executed = %v, LR_FAIL transaction status must not abort source control", executed)
	}
}

func TestDuplicateSourceTransactionStartIsRejectedWithoutMutationOrGoal(t *testing.T) {
	runLogic := dsl.RunLogicConfig{Iterations: 1, MaxTransactions: 1, AutoTransactions: "none"}
	softCtx, softCancel := context.WithCancel(context.Background())
	defer softCancel()
	sink := &sequenceCaptureSink{}
	limiter := NewExecutionGoalLimiter(runLogic, softCancel)
	vu := &VUser{
		ResultSink:     sink,
		ExecutionGoals: limiter,
		HardStopCtx:    context.Background(),
		Script: &dsl.Script{
			Name:     "duplicate-start",
			Protocol: "http",
			Config: dsl.ScriptConfig{
				RunLogic:      runLogic,
				ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(true)},
			},
			Actions: []dsl.Action{
				sourceTransactionCall("lr_start_transaction", "Checkout"),
				sourceTransactionCall("lr_start_transaction", "Checkout"),
				sourceTransactionCall("lr_end_transaction", "Checkout", "LR_PASS"),
			},
		},
	}

	vu.Run(softCtx)

	if len(sink.transactions) != 1 || !sink.transactions[0].Success {
		t.Fatalf("transactions = %#v, original start and explicit PASS end must remain authoritative", sink.transactions)
	}
	limiter.transactionMu.Lock()
	transactions, pending := limiter.transactions, limiter.pendingTransactions
	limiter.transactionMu.Unlock()
	if transactions != 1 || pending != 0 {
		t.Fatalf("goal counts = committed %d pending %d, duplicate start consumed or stranded capacity", transactions, pending)
	}
}

func TestSourceSubtransactionsRequireOpenParentAndCompleteIndependently(t *testing.T) {
	t.Run("valid parent and child", func(t *testing.T) {
		runLogic := dsl.RunLogicConfig{Iterations: 1, MaxTransactions: 2, AutoTransactions: "none"}
		softCtx, softCancel := context.WithCancel(context.Background())
		defer softCancel()
		sink := &sequenceCaptureSink{}
		vu := &VUser{
			ResultSink:     sink,
			ExecutionGoals: NewExecutionGoalLimiter(runLogic, softCancel),
			HardStopCtx:    context.Background(),
			Script: &dsl.Script{
				Name:     "subtransactions",
				Protocol: "http",
				Config:   dsl.ScriptConfig{RunLogic: runLogic},
				Actions: []dsl.Action{
					sourceTransactionCall("lr_start_transaction", "Parent"),
					sourceTransactionCall("lr_start_sub_transaction", "Child", "Parent"),
					sourceTransactionCall("lr_end_sub_transaction", "Child", "LR_PASS"),
					sourceTransactionCall("lr_end_transaction", "Parent", "LR_PASS"),
				},
			},
		}
		vu.Run(softCtx)

		if len(sink.transactions) != 2 {
			t.Fatalf("transactions = %#v, want child and parent", sink.transactions)
		}
		if sink.transactions[0].ActionName != "Child" || sink.transactions[1].ActionName != "Parent" {
			t.Fatalf("transaction order = %#v, want child then parent", sink.transactions)
		}
	})

	t.Run("missing parent consumes no goal", func(t *testing.T) {
		runLogic := dsl.RunLogicConfig{Iterations: 1, MaxTransactions: 1, AutoTransactions: "none"}
		softCtx, softCancel := context.WithCancel(context.Background())
		defer softCancel()
		sink := &sequenceCaptureSink{}
		limiter := NewExecutionGoalLimiter(runLogic, softCancel)
		vu := &VUser{
			ResultSink:     sink,
			ExecutionGoals: limiter,
			HardStopCtx:    context.Background(),
			Script: &dsl.Script{
				Name:     "orphan-subtransaction",
				Protocol: "http",
				Config: dsl.ScriptConfig{
					RunLogic:      runLogic,
					ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(true)},
				},
				Actions: []dsl.Action{
					sourceTransactionCall("lr_start_sub_transaction", "Child", "Missing"),
					sourceTransactionCall("lr_set_transaction", "Fallback", 0.25, "LR_PASS"),
				},
			},
		}
		vu.Run(softCtx)

		if len(sink.transactions) != 1 || sink.transactions[0].ActionName != "Fallback" {
			t.Fatalf("transactions = %#v, invalid child start consumed the only goal", sink.transactions)
		}
		limiter.transactionMu.Lock()
		transactions, pending := limiter.transactions, limiter.pendingTransactions
		limiter.transactionMu.Unlock()
		if transactions != 1 || pending != 0 {
			t.Fatalf("goal counts = committed %d pending %d", transactions, pending)
		}
	})
}

func TestOrphanSourceTransactionEndIsAnActionErrorWithoutTransactionOrGoal(t *testing.T) {
	t.Run("load continues to valid transaction when configured", func(t *testing.T) {
		runLogic := dsl.RunLogicConfig{Iterations: 1, MaxTransactions: 1, AutoTransactions: "none"}
		softCtx, softCancel := context.WithCancel(context.Background())
		defer softCancel()
		sink := &sequenceCaptureSink{}
		limiter := NewExecutionGoalLimiter(runLogic, softCancel)
		vu := &VUser{
			ResultSink:     sink,
			ExecutionGoals: limiter,
			HardStopCtx:    context.Background(),
			Script: &dsl.Script{
				Name:     "orphan-end",
				Protocol: "http",
				Config: dsl.ScriptConfig{
					RunLogic:      runLogic,
					ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(true)},
				},
				Actions: []dsl.Action{
					sourceTransactionCall("lr_end_transaction", "Missing", "LR_PASS"),
					sourceTransactionCall("lr_set_transaction", "Fallback", 0.1, "LR_PASS"),
				},
			},
		}
		vu.Run(softCtx)

		if len(sink.transactions) != 1 || sink.transactions[0].ActionName != "Fallback" {
			t.Fatalf("transactions = %#v, orphan end synthesized a transaction or consumed the goal", sink.transactions)
		}
		limiter.transactionMu.Lock()
		transactions, pending := limiter.transactions, limiter.pendingTransactions
		limiter.transactionMu.Unlock()
		if transactions != 1 || pending != 0 {
			t.Fatalf("goal counts = committed %d pending %d", transactions, pending)
		}
	})

	t.Run("replay obeys ordinary failure policy", func(t *testing.T) {
		var executed []string
		protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
			executed = append(executed, action.Name)
			return &protocol.Result{ActionName: action.Name, Success: true}, nil
		})
		report, err := ReplayScript(context.Background(), &dsl.Script{
			Name:     "orphan-end-replay",
			Protocol: protocolName,
			Actions: []dsl.Action{
				sourceTransactionCall("lr_end_transaction", "Missing", "LR_PASS"),
				{Name: "after orphan", Type: "GET", Method: "GET", Target: "https://example.test/after"},
			},
		}, ReplayOptions{IgnoreThinkTime: true})
		if err != nil {
			t.Fatal(err)
		}
		if report.Success || len(report.Events) != 1 || report.Events[0].Success {
			t.Fatalf("report = %#v, want one failed orphan-end event", report)
		}
		if len(executed) != 0 {
			t.Fatalf("executed = %v, replay continued after an action error", executed)
		}
	})
}

func TestInstanceAndCrossVUserTransactionCallsAreNotNativeBoundaries(t *testing.T) {
	functions := []string{
		"lr_start_cross_vuser_transaction",
		"lr_end_cross_vuser_transaction",
		"lr_start_transaction_instance",
		"lr_end_transaction_instance",
		"lr_stop_transaction_instance",
		"lr_resume_transaction_instance",
		"lr_set_transaction_instance_status",
		"lr_get_trans_instance_duration",
		"lr_get_trans_instance_status",
		"lr_get_trans_instance_think_time",
		"lr_get_trans_instance_wasted_time",
	}
	for _, function := range functions {
		t.Run(function, func(t *testing.T) {
			action := sourceTransactionCall(function, "instance")
			if sourceTransactionStarts(action) || sourceTransactionEnds(action) ||
				sourceTransactionRecordsImmediate(action) {
				t.Fatalf("%s was incorrectly classified as a native transaction boundary", function)
			}
			if err := (&VUser{}).handleSourceTransactionAction(action); err == nil ||
				!strings.Contains(err.Error(), "not executable") {
				t.Fatalf("handler error = %v, want explicit ownership-semantics error", err)
			}
		})
	}

	nearMatch := sourceTransactionCall("wrapper_lr_start_transaction_instance", "instance")
	if sourceTransactionStarts(nearMatch) || sourceTransactionEnds(nearMatch) {
		t.Fatal("substring-matching captured a non-LoadRunner function")
	}
}

func TestTransactionValueAndWastedTimeCallsReturnNativeErrors(t *testing.T) {
	functions := []string{
		"lr_get_transaction_duration",
		"lr_get_transaction_status",
		"lr_get_transaction_think_time",
		"lr_get_transaction_wasted_time",
		"lr_wasted_time",
	}
	for _, function := range functions {
		t.Run(function, func(t *testing.T) {
			action := sourceTransactionCall(function, "Checkout")
			if sourceTransactionStarts(action) || sourceTransactionEnds(action) ||
				sourceTransactionRecordsImmediate(action) {
				t.Fatalf("%s was incorrectly classified as a native transaction boundary", function)
			}
			err := (&VUser{}).handleSourceTransactionAction(action)
			if err == nil || !strings.Contains(err.Error(), "not executable") ||
				!strings.Contains(err.Error(), function) {
				t.Fatalf("handler error = %v, want explicit native value/wasted-time error", err)
			}
		})
	}
}

func TestUnknownSourceTransactionFunctionDoesNotFailOpen(t *testing.T) {
	err := (&VUser{}).handleSourceTransactionAction(sourceTransactionCall("lr_future_transaction_api", "Checkout"))
	if err == nil || !strings.Contains(err.Error(), "not a supported native source-transaction function") {
		t.Fatalf("handler error = %v, want explicit unsupported-function error", err)
	}
}

type nilResultProtocol struct {
	calls []string
}

func (*nilResultProtocol) Name() string                               { return "nil-result" }
func (*nilResultProtocol) Init(context.Context, map[string]any) error { return nil }
func (p *nilResultProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	p.calls = append(p.calls, action.Name)
	return nil, nil
}
func (*nilResultProtocol) Teardown(context.Context) error    { return nil }
func (*nilResultProtocol) HealthCheck(context.Context) error { return nil }

func TestNilProtocolResultBecomesNormalFailureWithoutStrandingGoal(t *testing.T) {
	t.Run("load", func(t *testing.T) {
		runLogic := dsl.RunLogicConfig{
			Iterations:       1,
			MaxTransactions:  1,
			AutoTransactions: "per_action",
		}
		softCtx, softCancel := context.WithCancel(context.Background())
		defer softCancel()
		protoStub := &nilResultProtocol{}
		sink := &sequenceCaptureSink{}
		limiter := NewExecutionGoalLimiter(runLogic, softCancel)
		vu := &VUser{
			Proto:          protoStub,
			ResultSink:     sink,
			ExecutionGoals: limiter,
			HardStopCtx:    context.Background(),
			Script: &dsl.Script{
				Name:     "nil-result-load",
				Protocol: "nil-result",
				Config: dsl.ScriptConfig{
					RunLogic:      runLogic,
					ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(true)},
				},
				Actions: []dsl.Action{
					{Name: "first", Type: "request", Target: "/first"},
					{Name: "after", Type: "request", Target: "/after"},
				},
			},
		}

		vu.Run(softCtx)

		if len(protoStub.calls) != 1 || protoStub.calls[0] != "first" {
			t.Fatalf("calls = %v, exact transaction goal was overrun", protoStub.calls)
		}
		if len(sink.transactions) != 1 || sink.transactions[0].Success {
			t.Fatalf("transactions = %#v, nil result must record one normal failure", sink.transactions)
		}
		limiter.transactionMu.Lock()
		transactions, pending := limiter.transactions, limiter.pendingTransactions
		limiter.transactionMu.Unlock()
		if transactions != 1 || pending != 0 {
			t.Fatalf("goal counts = committed %d pending %d, nil result stranded capacity", transactions, pending)
		}
	})

	t.Run("replay", func(t *testing.T) {
		protocolName := registerReplayProtocol(t, func(*protocol.Action) (*protocol.Result, error) {
			return nil, nil
		})
		report, err := ReplayScript(context.Background(), &dsl.Script{
			Name:     "nil-result-replay",
			Protocol: protocolName,
			Actions: []dsl.Action{
				{Name: "first", Type: "request", Target: "/first"},
				{Name: "after", Type: "request", Target: "/after"},
			},
		}, ReplayOptions{IgnoreThinkTime: true})
		if err != nil {
			t.Fatal(err)
		}
		if report.Success || len(report.Events) != 1 ||
			!strings.Contains(report.Events[0].ErrorMessage, "protocol returned no result") {
			t.Fatalf("report = %#v, nil result must become an ordinary replay failure", report)
		}
	})
}
