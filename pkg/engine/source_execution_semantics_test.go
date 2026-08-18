package engine

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type sourceSemanticsProtocol struct {
	results     map[string]bool
	delays      map[string]time.Duration
	executed    []string
	executedAt  []time.Time
	cacheClears int
}

type sourceOutcomeStreamingProtocol struct {
	executed []string
}

func (*sourceOutcomeStreamingProtocol) Name() string { return "source-outcome-streaming" }
func (*sourceOutcomeStreamingProtocol) Init(context.Context, map[string]any) error {
	return nil
}
func (*sourceOutcomeStreamingProtocol) Execute(context.Context, *protocol.Action) (*protocol.Result, error) {
	panic("source outcome streaming protocol requires ExecuteStream")
}
func (p *sourceOutcomeStreamingProtocol) ExecuteStream(_ context.Context, action *protocol.Action, emit func(*protocol.Result) error) (*protocol.Result, error) {
	p.executed = append(p.executed, action.Name)
	if err := emit(&protocol.Result{
		ActionName:   action.Name + " child",
		Success:      false,
		Duration:     time.Millisecond,
		ErrorMessage: "streamed child failed",
	}); err != nil {
		return nil, err
	}
	return &protocol.Result{
		ActionName: action.Name,
		Success:    true,
		Duration:   2 * time.Millisecond,
	}, nil
}
func (*sourceOutcomeStreamingProtocol) Teardown(context.Context) error    { return nil }
func (*sourceOutcomeStreamingProtocol) HealthCheck(context.Context) error { return nil }

type sourceGoalBarrierProtocol struct {
	started chan struct{}
	release <-chan struct{}
}

func (*sourceGoalBarrierProtocol) Name() string { return "source-goal-barrier" }
func (*sourceGoalBarrierProtocol) Init(context.Context, map[string]any) error {
	return nil
}
func (p *sourceGoalBarrierProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	if action.Name == "owner barrier" {
		close(p.started)
		<-p.release
	}
	return &protocol.Result{ActionName: action.Name, Success: true, Duration: time.Millisecond}, nil
}
func (*sourceGoalBarrierProtocol) Teardown(context.Context) error    { return nil }
func (*sourceGoalBarrierProtocol) HealthCheck(context.Context) error { return nil }

func (p *sourceSemanticsProtocol) Name() string { return "http" }
func (p *sourceSemanticsProtocol) Init(context.Context, map[string]any) error {
	return nil
}
func (p *sourceSemanticsProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	p.executed = append(p.executed, action.Name)
	p.executedAt = append(p.executedAt, time.Now())
	if delay := p.delays[action.Name]; delay > 0 {
		time.Sleep(delay)
	}
	success := true
	if value, exists := p.results[action.Name]; exists {
		success = value
	}
	return &protocol.Result{
		ActionName: action.Name,
		StartTime:  time.Now(),
		Duration:   time.Millisecond,
		Success:    success,
	}, nil
}
func (p *sourceSemanticsProtocol) Teardown(context.Context) error    { return nil }
func (p *sourceSemanticsProtocol) HealthCheck(context.Context) error { return nil }
func (p *sourceSemanticsProtocol) ClearCache(context.Context) error {
	p.cacheClears++
	return nil
}

type partialActiveThinkController struct {
	elapsed time.Duration
	waited  time.Duration
}

func (*partialActiveThinkController) WaitIfPaused(context.Context) error { return nil }
func (c *partialActiveThinkController) SleepActive(context.Context, time.Duration) bool {
	c.elapsed += c.waited
	return false
}
func (c *partialActiveThinkController) ActiveElapsed() time.Duration { return c.elapsed }

func sourceTransactionAction(function, status string) dsl.Action {
	args := []any{"ACME_ISBA"}
	if status != "" {
		args = append(args, status)
	}
	displayName := "Start_ACME_ISBA_transaction"
	if function != "lr_start_transaction" {
		displayName = "End_ACME_ISBA_transaction"
	}
	return dsl.Action{
		Name: displayName,
		Type: "source_transaction",
		Params: map[string]any{
			"function": function,
			"args":     args,
		},
	}
}

func TestSourceTransactionExplicitStatusAndAutoDerivation(t *testing.T) {
	tests := []struct {
		name           string
		requestSuccess bool
		status         string
		wantSuccess    bool
	}{
		{name: "auto inherits request failure", requestSuccess: false, status: "LR_AUTO", wantSuccess: false},
		{name: "explicit pass overrides request failure", requestSuccess: false, status: "LR_PASS", wantSuccess: true},
		{name: "explicit fail overrides request success", requestSuccess: true, status: "LR_FAIL", wantSuccess: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			protoStub := &sourceSemanticsProtocol{results: map[string]bool{"request": tc.requestSuccess}}
			sink := &sequenceCaptureSink{}
			vu := &VUser{
				ID:         1,
				Proto:      protoStub,
				Metrics:    NewMetricsCollector(16),
				ResultSink: sink,
				Script: &dsl.Script{
					Name:     "source-transaction",
					Protocol: "http",
					Config: dsl.ScriptConfig{
						RunLogic:      dsl.RunLogicConfig{Iterations: 1},
						ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(true)},
					},
					Actions: []dsl.Action{
						sourceTransactionAction("lr_start_transaction", ""),
						{
							Name:     "request",
							Type:     "GET",
							Method:   "GET",
							Target:   "https://example.test/",
							Metadata: map[string]any{"source_transaction": "ACME_ISBA"},
						},
						sourceTransactionAction("lr_end_transaction", tc.status),
					},
				},
			}

			vu.Run(context.Background())

			if len(sink.transactions) != 1 {
				t.Fatalf("transactions = %#v, want exactly one explicit transaction", sink.transactions)
			}
			tx := sink.transactions[0]
			if tx.ActionName != "ACME_ISBA" || tx.Success != tc.wantSuccess {
				t.Fatalf("transaction = %#v, want ACME_ISBA success=%v", tx, tc.wantSuccess)
			}
			if tx.Duration <= 0 {
				t.Fatalf("transaction duration = %v, want measured source scope", tx.Duration)
			}
		})
	}
}

func TestSourceTransactionAutoFailureWithoutImporterMetadata(t *testing.T) {
	protoStub := &sourceSemanticsProtocol{results: map[string]bool{"request": false}}
	sink := &sequenceCaptureSink{}
	vu := &VUser{
		ID:         1,
		Proto:      protoStub,
		Metrics:    NewMetricsCollector(16),
		ResultSink: sink,
		Script: &dsl.Script{
			Name:     "native-source-transaction",
			Protocol: "http",
			Config: dsl.ScriptConfig{
				RunLogic:      dsl.RunLogicConfig{Iterations: 1},
				ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(true)},
			},
			Actions: []dsl.Action{
				sourceTransactionAction("lr_start_transaction", ""),
				{Name: "request", Type: "GET", Method: "GET", Target: "https://example.test/"},
				sourceTransactionAction("lr_end_transaction", "LR_AUTO"),
			},
		},
	}

	vu.Run(context.Background())

	if len(sink.transactions) != 1 || sink.transactions[0].Success {
		t.Fatalf("transactions = %#v, metadata-less failed action must fail LR_AUTO", sink.transactions)
	}
}

func TestSourceTransactionLegacyStartTimestampIsConsumed(t *testing.T) {
	sink := &sequenceCaptureSink{}
	vu := &VUser{
		Metrics:    NewMetricsCollector(16),
		ResultSink: sink,
		Variables:  map[string]string{},
	}
	start := sourceTransactionAction("lr_start_transaction", "")
	end := sourceTransactionAction("lr_end_transaction", "LR_PASS")
	vu.handleSourceTransactionAction(start)
	legacyKey := sourceTransactionStartKey("ACME_ISBA")
	vu.Variables[legacyKey] = strconv.FormatInt(time.Now().Add(-time.Hour).UnixNano(), 10)

	if err := vu.handleSourceTransactionAction(end); err != nil {
		t.Fatalf("first end: %v", err)
	}
	if _, exists := vu.Variables[legacyKey]; exists {
		t.Fatalf("legacy source start %q survived the authoritative end marker", legacyKey)
	}
	if err := vu.handleSourceTransactionAction(end); err == nil {
		t.Fatal("orphan end unexpectedly succeeded")
	}

	if len(sink.transactions) != 1 {
		t.Fatalf("transactions = %#v, want only the authoritative matched end", sink.transactions)
	}
}

func TestSourceTransactionAutoTracksHandledEngineActionFailures(t *testing.T) {
	abort := dsl.Action{
		Name: "abort",
		Type: "source_runtime",
		Params: map[string]any{
			"function": "lr_abort",
		},
	}
	for _, phase := range []string{"main", "teardown"} {
		t.Run(phase, func(t *testing.T) {
			sink := &sequenceCaptureSink{}
			script := &dsl.Script{
				Name:     "handled-engine-error-" + phase,
				Protocol: "http",
				Config: dsl.ScriptConfig{
					RunLogic:      dsl.RunLogicConfig{Iterations: 1},
					ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(true)},
				},
			}
			scope := []dsl.Action{
				sourceTransactionAction("lr_start_transaction", ""),
				abort,
				sourceTransactionAction("lr_end_transaction", "LR_AUTO"),
			}
			if phase == "main" {
				script.Actions = scope
			} else {
				script.Actions = []dsl.Action{{Name: "main", Type: "GET", Method: "GET", Target: "https://example.test/main"}}
				script.Teardown = scope
			}
			vu := &VUser{
				Proto:      &sourceSemanticsProtocol{},
				Metrics:    NewMetricsCollector(16),
				ResultSink: sink,
				Script:     script,
			}

			vu.Run(context.Background())

			if len(sink.transactions) != 1 || sink.transactions[0].Success {
				t.Fatalf("transactions = %#v, handled %s error must fail LR_AUTO", sink.transactions, phase)
			}
		})
	}
}

func TestSourceTransactionAutoTracksFailedStreamingChildren(t *testing.T) {
	for _, phase := range []string{"main", "init"} {
		t.Run(phase, func(t *testing.T) {
			protoStub := &sourceOutcomeStreamingProtocol{}
			sink := &sequenceCaptureSink{}
			script := &dsl.Script{
				Name:     "streamed-child-" + phase,
				Protocol: "source-outcome-streaming",
				Config: dsl.ScriptConfig{
					RunLogic:      dsl.RunLogicConfig{Iterations: 1},
					ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(true)},
				},
			}
			scope := []dsl.Action{
				sourceTransactionAction("lr_start_transaction", ""),
				{Name: "stream", Type: "request", Target: "/"},
				sourceTransactionAction("lr_end_transaction", "LR_AUTO"),
			}
			if phase == "main" {
				script.Actions = scope
			} else {
				script.Init = scope
			}
			vu := &VUser{
				Proto:      protoStub,
				Metrics:    NewMetricsCollector(16),
				ResultSink: sink,
				Script:     script,
			}

			vu.Run(context.Background())

			if len(sink.transactions) != 1 || sink.transactions[0].Success {
				t.Fatalf("transactions = %#v, failed streamed child in %s must fail LR_AUTO", sink.transactions, phase)
			}
		})
	}
}

func TestExplicitTransactionMarkersSuppressHTTPSequenceDuplicate(t *testing.T) {
	protoStub := &sourceSemanticsProtocol{}
	sink := &sequenceCaptureSink{}
	vu := &VUser{
		ID:         1,
		Proto:      protoStub,
		Metrics:    NewMetricsCollector(16),
		ResultSink: sink,
		Script: &dsl.Script{
			Name:     "source-sequence",
			Protocol: "http",
			Config:   dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}},
			Actions: []dsl.Action{
				sourceTransactionAction("lr_start_transaction", ""),
				{
					Name:     "ACME_ISBA",
					Type:     "http_sequence",
					Method:   "SEQUENCE",
					Metadata: map[string]any{"source_transaction": "ACME_ISBA"},
					Params: map[string]any{"requests": []any{
						map[string]any{"name": "one", "type": "GET", "method": "GET", "target": "https://example.test/one"},
						map[string]any{"name": "two", "type": "GET", "method": "GET", "target": "https://example.test/two"},
					}},
				},
				sourceTransactionAction("lr_end_transaction", "LR_AUTO"),
			},
		},
	}

	vu.Run(context.Background())

	if len(protoStub.executed) != 2 {
		t.Fatalf("executed = %v, want both sequence children", protoStub.executed)
	}
	if len(sink.transactions) != 1 || sink.transactions[0].ActionName != "ACME_ISBA" {
		t.Fatalf("transactions = %#v, want one marker-owned transaction", sink.transactions)
	}
}

func TestHTTPSequenceMarkerOwnershipRequiresMatchingTransactionName(t *testing.T) {
	protoStub := &sourceSemanticsProtocol{}
	sink := &sequenceCaptureSink{}
	start := sourceTransactionAction("lr_start_transaction", "")
	start.Name = "Start_OTHER_transaction"
	start.Params["args"] = []any{"OTHER"}
	end := sourceTransactionAction("lr_end_transaction", "LR_PASS")
	end.Name = "End_OTHER_transaction"
	end.Params["args"] = []any{"OTHER", "LR_PASS"}
	vu := &VUser{
		ID:         1,
		Proto:      protoStub,
		Metrics:    NewMetricsCollector(16),
		ResultSink: sink,
		Script: &dsl.Script{
			Name:     "different-marker",
			Protocol: "http",
			Config:   dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}},
			Actions: []dsl.Action{
				start,
				{
					Name:     "ACME_ISBA",
					Type:     "http_sequence",
					Method:   "SEQUENCE",
					Metadata: map[string]any{"source_transaction": "ACME_ISBA"},
					Params: map[string]any{"requests": []any{
						map[string]any{"name": "one", "type": "GET", "method": "GET", "target": "https://example.test/one"},
					}},
				},
				end,
			},
		},
	}

	vu.Run(context.Background())

	if len(sink.transactions) != 2 {
		t.Fatalf("transactions = %#v, unmatched marker must not suppress the sequence transaction", sink.transactions)
	}
}

func TestHTTPSequenceOwnershipRequiresOpenRuntimeScope(t *testing.T) {
	sequence := func() dsl.Action {
		return dsl.Action{
			Name:     "ACME_ISBA",
			Type:     "http_sequence",
			Method:   "SEQUENCE",
			Metadata: map[string]any{"source_transaction": "ACME_ISBA"},
			Params: map[string]any{"requests": []dsl.Action{{
				Name: "one", Type: "GET", Method: "GET", Target: "https://example.test/one",
			}}},
		}
	}
	start := func() dsl.Action { return sourceTransactionAction("lr_start_transaction", "") }
	end := func() dsl.Action { return sourceTransactionAction("lr_end_transaction", "LR_PASS") }

	tests := []struct {
		name       string
		runLogic   dsl.RunLogicConfig
		init       []dsl.Action
		actions    []dsl.Action
		teardown   []dsl.Action
		wantTx     int
		wantTxName []string
	}{
		{
			name:       "later marker",
			actions:    []dsl.Action{sequence(), start(), end()},
			wantTx:     2,
			wantTxName: []string{"ACME_ISBA", "ACME_ISBA"},
		},
		{
			name: "untaken marker branch",
			actions: []dsl.Action{
				{
					Name: "untaken", Type: "control_if",
					Control: &dsl.ControlFlow{
						Condition: &dsl.Condition{Expr: "false"},
						Steps:     []dsl.Action{start(), end()},
					},
				},
				sequence(),
			},
			wantTx:     1,
			wantTxName: []string{"ACME_ISBA"},
		},
		{
			name:       "marker in later teardown phase",
			actions:    []dsl.Action{sequence()},
			teardown:   []dsl.Action{start(), end()},
			wantTx:     2,
			wantTxName: []string{"ACME_ISBA", "ACME_ISBA"},
		},
		{
			name:       "later marker in lifecycle phase",
			runLogic:   dsl.RunLogicConfig{Iterations: 1, AutoTransactions: "per_action"},
			init:       []dsl.Action{sequence(), start(), end()},
			wantTx:     2,
			wantTxName: []string{"ACME_ISBA", "ACME_ISBA"},
		},
		{
			name:       "open init scope owns main sequence",
			init:       []dsl.Action{start()},
			actions:    []dsl.Action{sequence()},
			teardown:   []dsl.Action{end()},
			wantTx:     1,
			wantTxName: []string{"ACME_ISBA"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			protoStub := &sourceSemanticsProtocol{}
			sink := &sequenceCaptureSink{}
			runLogic := tc.runLogic
			if runLogic.Iterations == 0 {
				runLogic.Iterations = 1
			}
			vu := &VUser{
				ID:         1,
				Proto:      protoStub,
				Metrics:    NewMetricsCollector(16),
				ResultSink: sink,
				Script: &dsl.Script{
					Name:     "runtime-sequence-ownership",
					Protocol: "http",
					Config:   dsl.ScriptConfig{RunLogic: runLogic},
					Init:     tc.init,
					Actions:  tc.actions,
					Teardown: tc.teardown,
				},
			}

			vu.Run(context.Background())

			if len(sink.transactions) != tc.wantTx {
				t.Fatalf("transactions = %#v, want %d runtime-owned/standalone transactions", sink.transactions, tc.wantTx)
			}
			for index, want := range tc.wantTxName {
				if sink.transactions[index].ActionName != want {
					t.Fatalf("transaction[%d] = %#v, want name %q", index, sink.transactions[index], want)
				}
			}
		})
	}
}

func TestThinkTimeAfterSourceTransactionEndRunsOutsideTransactionMeasurement(t *testing.T) {
	protoStub := &sourceSemanticsProtocol{}
	sink := &sequenceCaptureSink{}
	end := sourceTransactionAction("lr_end_transaction", "LR_PASS")
	end.ThinkTime = dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 60}
	end.ThinkTimeAfter = true
	vu := &VUser{
		ID:         1,
		Proto:      protoStub,
		Metrics:    NewMetricsCollector(16),
		ResultSink: sink,
		Script: &dsl.Script{
			Name:     "post-transaction-think-time",
			Protocol: "http",
			Config: dsl.ScriptConfig{
				RunLogic: dsl.RunLogicConfig{Iterations: 1},
				ThinkTime: dsl.ThinkTimeConfig{
					Type:       "recorded",
					Multiplier: 2,
					LimitMs:    80,
				},
			},
			Actions: []dsl.Action{
				sourceTransactionAction("lr_start_transaction", ""),
				{
					Name:     "inside",
					Type:     "GET",
					Method:   "GET",
					Target:   "https://example.test/inside",
					Metadata: map[string]any{"source_transaction": "ACME_ISBA"},
				},
				end,
				{Name: "outside", Type: "GET", Method: "GET", Target: "https://example.test/outside"},
			},
		},
	}

	started := time.Now()
	vu.Run(context.Background())
	elapsed := time.Since(started)

	if len(sink.transactions) != 1 {
		t.Fatalf("transactions = %#v, want one source transaction", sink.transactions)
	}
	if sink.transactions[0].Duration >= 50*time.Millisecond {
		t.Fatalf("transaction duration = %v, post-end think time leaked into measurement", sink.transactions[0].Duration)
	}
	if elapsed < 65*time.Millisecond {
		t.Fatalf("elapsed = %v, source-action think time was not applied", elapsed)
	}
	if len(protoStub.executedAt) != 2 || protoStub.executedAt[1].Sub(protoStub.executedAt[0]) < 65*time.Millisecond {
		t.Fatalf("execution times = %v, outside request did not wait after transaction end", protoStub.executedAt)
	}
}

func TestSourceTransactionExcludesEnclosedThinkTimeInLoadAndReplay(t *testing.T) {
	tests := []struct {
		name string
		run  func(*VUser, *dsl.Script, *ReplayReport) bool
	}{
		{
			name: "load",
			run: func(vu *VUser, _ *dsl.Script, _ *ReplayReport) bool {
				vu.Run(context.Background())
				return true
			},
		},
		{
			name: "replay",
			run: func(vu *VUser, script *dsl.Script, report *ReplayReport) bool {
				vu.prepareRuntimeState()
				global := protocol.ThinkTimeConfig{
					Type:       script.Config.ThinkTime.Type,
					Multiplier: script.Config.ThinkTime.Multiplier,
				}
				return runReplayPhase(context.Background(), vu, script.Actions, "action", true, false, global, report, nil, nil)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			protoStub := &sourceSemanticsProtocol{}
			sink := &sequenceCaptureSink{}
			script := &dsl.Script{
				Name:     "enclosed-think-time",
				Protocol: "http",
				Config: dsl.ScriptConfig{
					RunLogic: dsl.RunLogicConfig{Iterations: 1},
					ThinkTime: dsl.ThinkTimeConfig{
						Type:       "recorded",
						Multiplier: 1,
					},
				},
				Actions: []dsl.Action{
					sourceTransactionAction("lr_start_transaction", ""),
					{
						Name:           "inside",
						Type:           "GET",
						Method:         "GET",
						Target:         "https://example.test/inside",
						Metadata:       map[string]any{"source_transaction": "ACME_ISBA"},
						ThinkTime:      dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 80},
						ThinkTimeAfter: true,
					},
					sourceTransactionAction("lr_end_transaction", "LR_PASS"),
				},
			}
			vu := &VUser{
				ID:         1,
				Proto:      protoStub,
				Metrics:    NewMetricsCollector(16),
				ResultSink: sink,
				Script:     script,
				Variables:  map[string]string{},
			}
			report := &ReplayReport{Success: true}

			started := time.Now()
			if !tc.run(vu, script, report) {
				t.Fatal("execution stopped unexpectedly")
			}
			if elapsed := time.Since(started); elapsed < 65*time.Millisecond {
				t.Fatalf("elapsed = %v, enclosed think time did not execute", elapsed)
			}
			if len(sink.transactions) != 1 {
				t.Fatalf("transactions = %#v, want one explicit transaction", sink.transactions)
			}
			if duration := sink.transactions[0].Duration; duration >= 50*time.Millisecond {
				t.Fatalf("transaction duration = %v, enclosed think time leaked into response time", duration)
			}
		})
	}
}

func TestLoadCanceledThinkTimeAccountsActualElapsedAndStops(t *testing.T) {
	protoStub := &sourceSemanticsProtocol{delays: map[string]time.Duration{"inside": 30 * time.Millisecond}}
	sink := &sequenceCaptureSink{}
	vu := &VUser{
		ID:         1,
		Proto:      protoStub,
		Metrics:    NewMetricsCollector(16),
		ResultSink: sink,
		Script: &dsl.Script{
			Name:     "canceled-load-think-time",
			Protocol: "http",
			Config:   dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}},
			Actions: []dsl.Action{
				sourceTransactionAction("lr_start_transaction", ""),
				{
					Name:           "inside",
					Type:           "GET",
					Method:         "GET",
					Target:         "https://example.test/inside",
					Metadata:       map[string]any{"source_transaction": "ACME_ISBA"},
					ThinkTime:      dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 500},
					ThinkTimeAfter: true,
				},
				{Name: "after", Type: "GET", Method: "GET", Target: "https://example.test/after"},
			},
			Teardown: []dsl.Action{sourceTransactionAction("lr_end_transaction", "LR_PASS")},
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	vu.Run(ctx)

	if len(protoStub.executed) != 1 || protoStub.executed[0] != "inside" {
		t.Fatalf("executed = %v, load continued after canceled think time", protoStub.executed)
	}
	if len(sink.transactions) != 1 {
		t.Fatalf("transactions = %#v, want teardown to close the source transaction", sink.transactions)
	}
	if duration := sink.transactions[0].Duration; duration < 15*time.Millisecond || duration >= 70*time.Millisecond {
		t.Fatalf("transaction duration = %v, want request work preserved after subtracting only the completed wait", duration)
	}
}

func TestLoadThinkTimeAccountsActiveElapsed(t *testing.T) {
	controller := &partialActiveThinkController{waited: 25 * time.Millisecond}
	vu := &VUser{
		PauseControl: controller,
		Metrics:      NewMetricsCollector(16),
		Variables:    map[string]string{},
		Script: &dsl.Script{Config: dsl.ScriptConfig{
			ThinkTime: dsl.ThinkTimeConfig{Type: "recorded", Multiplier: 1},
		}},
	}
	vu.handleSourceTransactionAction(sourceTransactionAction("lr_start_transaction", ""))
	completed := vu.applyActionThinkTime(context.Background(), dsl.Action{
		Name:           "inside",
		ThinkTime:      dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 500},
		ThinkTimeAfter: true,
	})
	if completed {
		t.Fatal("partially completed active think time reported completion")
	}
	state := vu.sourceTransactions["acme_isba"]
	if state == nil || state.thinkTime != 25*time.Millisecond {
		t.Fatalf("source transaction think time = %#v, want exactly 25ms active elapsed", state)
	}
	if vu.iterationThinkTime != 25*time.Millisecond {
		t.Fatalf("iteration think time = %s, want exactly 25ms active elapsed", vu.iterationThinkTime)
	}
}

func TestLifecycleControlContainerThinkTimeExcludesSourceTransactionMeasurement(t *testing.T) {
	tests := []struct {
		name      string
		container dsl.Action
	}{
		{
			name: "control_if normal completion",
			container: dsl.Action{
				Name: "conditional",
				Type: "control_if",
				Control: &dsl.ControlFlow{
					Condition: &dsl.Condition{Expr: "true"},
					Steps: []dsl.Action{{
						Name:   "inside",
						Type:   "GET",
						Method: "GET",
						Target: "https://example.test/inside",
					}},
				},
			},
		},
		{
			name: "control_loop consumed break",
			container: dsl.Action{
				Name: "loop",
				Type: "control_loop",
				Control: &dsl.ControlFlow{
					Count: 5,
					Steps: []dsl.Action{{
						Name: "break",
						Type: "control_break",
					}},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sink := &sequenceCaptureSink{}
			tc.container.ThinkTimeAfter = true
			tc.container.ThinkTime = dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 45}
			vu := &VUser{
				ID:         1,
				Proto:      &sourceSemanticsProtocol{},
				Metrics:    NewMetricsCollector(16),
				ResultSink: sink,
				Script: &dsl.Script{
					Name:     "lifecycle-container-think-time",
					Protocol: "http",
					Config: dsl.ScriptConfig{ThinkTime: dsl.ThinkTimeConfig{
						Type: "recorded", Multiplier: 1,
					}},
				},
			}
			actions := []dsl.Action{
				sourceTransactionAction("lr_start_transaction", ""),
				tc.container,
				sourceTransactionAction("lr_end_transaction", "LR_PASS"),
			}

			started := time.Now()
			if !vu.RunLifecycleBlock(context.Background(), actions, "init") {
				t.Fatal("RunLifecycleBlock returned false")
			}
			if elapsed := time.Since(started); elapsed < 35*time.Millisecond {
				t.Fatalf("elapsed = %s, container think time did not run", elapsed)
			}
			if len(sink.transactions) != 1 {
				t.Fatalf("transactions = %#v, want one source transaction", sink.transactions)
			}
			if duration := sink.transactions[0].Duration; duration >= 30*time.Millisecond {
				t.Fatalf("source transaction duration = %s, container think time leaked into response measurement", duration)
			}
		})
	}
}

func TestLifecycleControlContainerThinkTimeHonorsCancellation(t *testing.T) {
	containers := []dsl.Action{
		{
			Name: "conditional",
			Type: "control_if",
			Control: &dsl.ControlFlow{
				Condition: &dsl.Condition{Expr: "true"},
			},
		},
		{
			Name: "loop",
			Type: "control_loop",
			Control: &dsl.ControlFlow{
				Count: 1,
				Steps: []dsl.Action{{Name: "break", Type: "control_break"}},
			},
		},
	}

	for _, container := range containers {
		t.Run(container.Type, func(t *testing.T) {
			protoStub := &sourceSemanticsProtocol{}
			container.ThinkTimeAfter = true
			container.ThinkTime = dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 500}
			vu := &VUser{
				ID:      1,
				Proto:   protoStub,
				Metrics: NewMetricsCollector(16),
				Script: &dsl.Script{
					Name:     "cancel-lifecycle-container-think-time",
					Protocol: "http",
				},
			}
			actions := []dsl.Action{
				container,
				{Name: "after", Type: "GET", Method: "GET", Target: "https://example.test/after"},
			}
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
			defer cancel()

			started := time.Now()
			if vu.RunLifecycleBlock(ctx, actions, "init") {
				t.Fatal("RunLifecycleBlock returned true after container think-time cancellation")
			}
			if elapsed := time.Since(started); elapsed >= 250*time.Millisecond {
				t.Fatalf("canceled container think time stopped after %s, want prompt cancellation", elapsed)
			}
			if len(protoStub.executed) != 0 {
				t.Fatalf("executed = %v, lifecycle continued after canceled container think time", protoStub.executed)
			}
		})
	}
}

func TestIterationFallbackTransactionExcludesThinkTime(t *testing.T) {
	sink := &sequenceCaptureSink{}
	vu := &VUser{
		ID:         1,
		Proto:      &sourceSemanticsProtocol{},
		Metrics:    NewMetricsCollector(16),
		ResultSink: sink,
		Script: &dsl.Script{
			Name:     "engine-only-iteration",
			Protocol: "http",
			Config:   dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}},
			Actions: []dsl.Action{{
				Name:           "message",
				Type:           "source_message",
				Value:          "iteration message",
				ThinkTime:      dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 60},
				ThinkTimeAfter: true,
			}},
		},
	}

	started := time.Now()
	vu.Run(context.Background())
	if elapsed := time.Since(started); elapsed < 50*time.Millisecond {
		t.Fatalf("elapsed = %s, think time did not execute", elapsed)
	}
	if len(sink.transactions) != 1 {
		t.Fatalf("transactions = %#v, want one automatic iteration transaction", sink.transactions)
	}
	if duration := sink.transactions[0].Duration; duration >= 30*time.Millisecond {
		t.Fatalf("iteration response duration = %s, think time leaked into the zero-protocol-duration fallback", duration)
	}
}

func TestThinkTimeAfterEngineActionHonorsGlobalIgnore(t *testing.T) {
	protoStub := &sourceSemanticsProtocol{}
	vu := &VUser{
		ID:      1,
		Proto:   protoStub,
		Metrics: NewMetricsCollector(16),
		Script: &dsl.Script{
			Name:     "ignored-source-think-time",
			Protocol: "http",
			Config: dsl.ScriptConfig{
				RunLogic: dsl.RunLogicConfig{Iterations: 1},
				ThinkTime: dsl.ThinkTimeConfig{
					Type:       "recorded",
					Multiplier: 1,
					Ignore:     true,
				},
			},
			Actions: []dsl.Action{
				{
					Name:           "source message",
					Type:           "source_message",
					Value:          "ready",
					ThinkTime:      dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 200},
					ThinkTimeAfter: true,
				},
				{Name: "request", Type: "GET", Method: "GET", Target: "https://example.test/"},
			},
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()

	vu.Run(ctx)

	if len(protoStub.executed) != 1 || protoStub.executed[0] != "request" {
		t.Fatalf("executed = %v, ignored think time must not consume the deadline", protoStub.executed)
	}
}

func TestReplayAppliesThinkTimeAfterEngineActionWithoutInflatingEventDuration(t *testing.T) {
	var requestAt time.Time
	protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		requestAt = time.Now()
		return &protocol.Result{
			ActionName: action.Name,
			StartTime:  requestAt,
			Duration:   time.Millisecond,
			Success:    true,
		}, nil
	})
	script := &dsl.Script{
		Name:     "replay-source-think-time",
		Protocol: protocolName,
		Config: dsl.ScriptConfig{ThinkTime: dsl.ThinkTimeConfig{
			Type:       "recorded",
			Multiplier: 2,
			LimitMs:    80,
		}},
		Actions: []dsl.Action{
			{
				Name:           "source message",
				Type:           "source_message",
				Value:          "ready",
				ThinkTime:      dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 60},
				ThinkTimeAfter: true,
			},
			{Name: "request", Type: "GET", Method: "GET", Target: "https://example.test/"},
		},
	}

	started := time.Now()
	report, err := ReplayScript(context.Background(), script, ReplayOptions{})
	if err != nil {
		t.Fatalf("ReplayScript: %v", err)
	}
	if requestAt.Sub(started) < 65*time.Millisecond {
		t.Fatalf("request started after %v, want replay think time after engine action", requestAt.Sub(started))
	}
	if len(report.Events) < 1 {
		t.Fatalf("events = %#v", report.Events)
	}
	if duration := report.Events[0].CompletedAt.Sub(report.Events[0].StartedAt); duration >= 50*time.Millisecond {
		t.Fatalf("source event duration = %v, think time must remain outside event timing", duration)
	}
}

func TestReplayAppliesParentContainerThinkTime(t *testing.T) {
	tests := []struct {
		name      string
		container dsl.Action
	}{
		{
			name: "http sequence",
			container: dsl.Action{
				Name: "sequence", Type: "http_sequence", Method: "SEQUENCE",
				Params: map[string]any{"requests": []dsl.Action{{
					Name: "inside", Type: "GET", Method: "GET", Target: "https://example.test/inside",
				}}},
			},
		},
		{
			name: "conditional",
			container: dsl.Action{
				Name: "conditional", Type: "control_if",
				Control: &dsl.ControlFlow{
					Condition: &dsl.Condition{Expr: "true"},
					Steps: []dsl.Action{{
						Name: "inside", Type: "GET", Method: "GET", Target: "https://example.test/inside",
					}},
				},
			},
		},
		{
			name: "loop",
			container: dsl.Action{
				Name: "loop", Type: "control_loop",
				Control: &dsl.ControlFlow{
					Count: 1,
					Steps: []dsl.Action{{
						Name: "inside", Type: "GET", Method: "GET", Target: "https://example.test/inside",
					}},
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var executedAt []time.Time
			protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
				executedAt = append(executedAt, time.Now())
				return &protocol.Result{
					ActionName: action.Name,
					StartTime:  time.Now(),
					Duration:   time.Millisecond,
					Success:    true,
				}, nil
			})
			container := test.container
			container.ThinkTime = dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 20}
			container.ThinkTimeAfter = true
			report, err := ReplayScript(context.Background(), &dsl.Script{
				Name:     "container-parent-think-time",
				Protocol: protocolName,
				Config: dsl.ScriptConfig{ThinkTime: dsl.ThinkTimeConfig{
					Type: "recorded", Multiplier: 2, LimitMs: 30,
				}},
				Actions: []dsl.Action{
					container,
					{Name: "after", Type: "GET", Method: "GET", Target: "https://example.test/after"},
				},
			}, ReplayOptions{})
			if err != nil {
				t.Fatalf("ReplayScript: %v", err)
			}
			if !report.Success {
				t.Fatalf("report failed: %s", report.Error)
			}
			if len(executedAt) != 2 {
				t.Fatalf("protocol executions = %d, want container child plus following action", len(executedAt))
			}
			if gap := executedAt[1].Sub(executedAt[0]); gap < 25*time.Millisecond {
				t.Fatalf("following action gap = %s, parent container think time was skipped", gap)
			}
			if len(report.Events) != 2 || report.Events[0].DurationMs >= 20 {
				t.Fatalf("events = %#v, parent wait must remain outside child response duration", report.Events)
			}
		})
	}
}

func TestReplayCanceledParentContainerThinkTimeStopsNextAction(t *testing.T) {
	var executed []string
	protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		executed = append(executed, action.Name)
		return &protocol.Result{
			ActionName: action.Name,
			StartTime:  time.Now(),
			Duration:   time.Millisecond,
			Success:    true,
		}, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	report, err := ReplayScript(ctx, &dsl.Script{
		Name:     "canceled-container-parent-think-time",
		Protocol: protocolName,
		Actions: []dsl.Action{
			{
				Name: "sequence", Type: "http_sequence", Method: "SEQUENCE",
				ThinkTime:      dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 500},
				ThinkTimeAfter: true,
				Params: map[string]any{"requests": []dsl.Action{{
					Name: "inside", Type: "GET", Method: "GET", Target: "https://example.test/inside",
				}}},
			},
			{Name: "after", Type: "GET", Method: "GET", Target: "https://example.test/after"},
		},
	}, ReplayOptions{})
	if err != nil && ctx.Err() == nil {
		t.Fatalf("ReplayScript: %v", err)
	}
	if report == nil || report.Success || !strings.Contains(report.Error, "canceled during think time") {
		t.Fatalf("report = %#v, want parent think-time cancellation", report)
	}
	if len(executed) != 1 || executed[0] != "inside" {
		t.Fatalf("executed = %v, replay continued after canceled parent think time", executed)
	}
	if len(report.Events) != 1 || !report.Events[0].Success {
		t.Fatalf("events = %#v, completed child response must remain successful", report.Events)
	}
}

func TestReplayCanceledThinkTimeStopsBeforeTransactionEndAndNextAction(t *testing.T) {
	var executed []string
	protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		executed = append(executed, action.Name)
		return &protocol.Result{
			ActionName: action.Name,
			StartTime:  time.Now(),
			Duration:   time.Millisecond,
			Success:    true,
		}, nil
	})
	script := &dsl.Script{
		Name:     "canceled-source-think-time",
		Protocol: protocolName,
		Config: dsl.ScriptConfig{ErrorHandling: dsl.ErrorHandlingConfig{
			ContinueOnError: dsl.ContinueOnErrorValue(true),
		}},
		Actions: []dsl.Action{
			sourceTransactionAction("lr_start_transaction", ""),
			{
				Name:           "inside",
				Type:           "GET",
				Method:         "GET",
				Target:         "https://example.test/inside",
				Metadata:       map[string]any{"source_transaction": "ACME_ISBA"},
				ThinkTime:      dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 500},
				ThinkTimeAfter: true,
			},
			sourceTransactionAction("lr_end_transaction", "LR_PASS"),
			{Name: "after", Type: "GET", Method: "GET", Target: "https://example.test/after"},
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	report, err := ReplayScript(ctx, script, ReplayOptions{})
	if err != nil && ctx.Err() == nil {
		t.Fatalf("ReplayScript: %v", err)
	}
	if report == nil || report.Success {
		t.Fatalf("report = %#v, canceled replay must be incomplete", report)
	}
	if len(executed) != 1 || executed[0] != "inside" {
		t.Fatalf("executed = %v, replay continued after canceled think time", executed)
	}
	if len(report.Events) != 3 || report.Events[0].ActionName != "Start_ACME_ISBA_transaction" ||
		report.Events[1].ActionName != "inside" ||
		report.Events[2].Phase != "finalize" ||
		report.Events[2].ActionName != "ACME_ISBA" ||
		report.Events[2].Success {
		t.Fatalf("events = %#v, transaction end/next action ran after cancellation", report.Events)
	}
	if report.Events[1].Success || !strings.Contains(report.Events[1].ErrorMessage, "canceled during think time") {
		t.Fatalf("canceled wait event = %#v, want explicit cancellation failure", report.Events[1])
	}
	if _, exists := report.FinalVariables[sourceTransactionStartKey("ACME_ISBA")]; exists {
		t.Fatalf("final variables retained consumed source start: %#v", report.FinalVariables)
	}
	if got := report.FinalVariables[sourceTransactionStatusKey("ACME_ISBA")]; got != "false" {
		t.Fatalf("final source status = %q, want false", got)
	}
}

func TestReplayFinalizesUnclosedSourceTransactionWithFailedProgressEvidence(t *testing.T) {
	protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true}, nil
	})
	var progress []ReplayProgressEvent
	report, err := ReplayScript(context.Background(), &dsl.Script{
		Name:     "unclosed-replay-source-transaction",
		Protocol: protocolName,
		Actions:  []dsl.Action{sourceTransactionAction("lr_start_transaction", "")},
	}, ReplayOptions{OnProgress: func(event ReplayProgressEvent) {
		progress = append(progress, event)
	}})
	if err != nil {
		t.Fatalf("ReplayScript: %v", err)
	}
	if report.Success || !strings.Contains(report.Error, "without a matching end marker") {
		t.Fatalf("report = %#v, want failed unclosed transaction", report)
	}
	if len(report.Events) != 2 || report.Events[1].Phase != "finalize" || report.Events[1].Success {
		t.Fatalf("events = %#v, want failed finalization evidence", report.Events)
	}
	foundFailedProgress := false
	for _, event := range progress {
		if event.Type == ReplayProgressActionFailed && event.Event != nil && event.Event.Phase == "finalize" {
			foundFailedProgress = true
		}
	}
	if !foundFailedProgress {
		t.Fatalf("progress = %#v, want failed finalization event", progress)
	}
}

func TestReplaySourceTransactionAutoTracksMetadataLessAndHandledFailures(t *testing.T) {
	tests := []struct {
		name   string
		middle dsl.Action
		result *protocol.Result
	}{
		{
			name:   "protocol result",
			middle: dsl.Action{Name: "request", Type: "GET", Method: "GET", Target: "https://example.test/"},
			result: &protocol.Result{ActionName: "request", Success: false, ErrorMessage: "request failed"},
		},
		{
			name: "handled engine action",
			middle: dsl.Action{
				Name: "abort",
				Type: "source_runtime",
				Params: map[string]any{
					"function": "lr_abort",
				},
			},
			result: &protocol.Result{Success: true},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
				result := *tc.result
				if result.ActionName == "" {
					result.ActionName = action.Name
				}
				return &result, nil
			})
			report, err := ReplayScript(context.Background(), &dsl.Script{
				Name:     "metadata-less-replay-" + tc.name,
				Protocol: protocolName,
				Config: dsl.ScriptConfig{ErrorHandling: dsl.ErrorHandlingConfig{
					ContinueOnError: dsl.ContinueOnErrorValue(true),
				}},
				Actions: []dsl.Action{
					sourceTransactionAction("lr_start_transaction", ""),
					tc.middle,
					sourceTransactionAction("lr_end_transaction", "LR_AUTO"),
				},
			}, ReplayOptions{})
			if err != nil {
				t.Fatalf("ReplayScript: %v", err)
			}
			if report.Success || len(report.Events) != 3 || report.Events[2].Success {
				t.Fatalf("report = %#v, %s must fail LR_AUTO", report, tc.name)
			}
		})
	}
}

func TestReplayFailedStreamingChildFailsAggregateAndSourceTransaction(t *testing.T) {
	protocolName := "replay-" + strings.ReplaceAll(strings.ToLower(t.Name()), "/", "-")
	protocol.Register(protocolName, func() protocol.Protocol {
		return &sourceOutcomeStreamingProtocol{}
	})
	report, err := ReplayScript(context.Background(), &dsl.Script{
		Name:     "replay-streamed-child-source-outcome",
		Protocol: protocolName,
		Config: dsl.ScriptConfig{ErrorHandling: dsl.ErrorHandlingConfig{
			ContinueOnError: dsl.ContinueOnErrorValue(true),
		}},
		Actions: []dsl.Action{
			sourceTransactionAction("lr_start_transaction", ""),
			{Name: "stream", Type: "request", Target: "/"},
			sourceTransactionAction("lr_end_transaction", "LR_AUTO"),
		},
	}, ReplayOptions{})
	if err != nil {
		t.Fatalf("ReplayScript: %v", err)
	}
	if report.Success || len(report.Events) != 3 {
		t.Fatalf("report = %#v, want failed streamed child and completed source end", report)
	}
	if report.Events[1].Success || !strings.Contains(report.Events[1].ErrorMessage, "streamed child failed") {
		t.Fatalf("stream aggregate event = %#v, want failed child evidence", report.Events[1])
	}
	if report.Events[2].Success {
		t.Fatalf("source end event = %#v, failed child must fail LR_AUTO", report.Events[2])
	}
}

func TestReplayCanceledThinkTimeAccountsOnlyElapsedWait(t *testing.T) {
	sink := &sequenceCaptureSink{}
	vu := &VUser{
		Metrics:    NewMetricsCollector(16),
		ResultSink: sink,
		Variables:  map[string]string{},
	}
	startAction := sourceTransactionAction("lr_start_transaction", "")
	vu.handleSourceTransactionAction(startAction)
	state := vu.sourceTransactions["acme_isba"]
	if state == nil {
		t.Fatal("source transaction runtime state was not created")
	}
	// Simulate 25ms of response/work before the wait. If replay records the
	// planned 500ms rather than the canceled wait's actual elapsed duration, the
	// transaction will incorrectly clamp to zero.
	state.startedAt = state.startedAt.Add(-25 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	completed := applyReplayActionThinkTime(ctx, vu, dsl.Action{
		Name:           "inside",
		ThinkTime:      dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 500},
		ThinkTimeAfter: true,
	}, false, protocol.ThinkTimeConfig{})
	if completed {
		t.Fatal("canceled replay think time reported completion")
	}
	vu.handleSourceTransactionAction(sourceTransactionAction("lr_end_transaction", "LR_PASS"))

	if len(sink.transactions) != 1 {
		t.Fatalf("transactions = %#v, want one completed source transaction", sink.transactions)
	}
	if duration := sink.transactions[0].Duration; duration < 15*time.Millisecond || duration >= 80*time.Millisecond {
		t.Fatalf("transaction duration = %v, want pre-wait work preserved after subtracting only elapsed wait", duration)
	}
}

func TestReplayZeroThinkTimeDoesNotMislabelAmbientCancellation(t *testing.T) {
	var cancel context.CancelFunc
	protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		cancel()
		return &protocol.Result{
			ActionName: action.Name,
			StartTime:  time.Now(),
			Duration:   time.Millisecond,
			Success:    true,
		}, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	report, err := ReplayScript(ctx, &dsl.Script{
		Name:     "zero-think-time-cancellation-boundary",
		Protocol: protocolName,
		Actions: []dsl.Action{{
			Name:           "request",
			Type:           "GET",
			Method:         "GET",
			Target:         "https://example.test/",
			ThinkTime:      dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 0},
			ThinkTimeAfter: true,
		}},
	}, ReplayOptions{})
	if err != nil {
		t.Fatalf("ReplayScript: %v", err)
	}
	if len(report.Events) != 1 {
		t.Fatalf("events = %#v, want one completed action", report.Events)
	}
	if event := report.Events[0]; !event.Success || strings.Contains(event.ErrorMessage, "think time") {
		t.Fatalf("event = %#v, zero-delay marker must not be reported as canceled think time", event)
	}
	if report.Success {
		t.Fatal("ambient cancellation should still mark the replay incomplete")
	}
}

func TestReplaySourceTransactionStatusAndMainIterationExit(t *testing.T) {
	t.Run("explicit and auto transaction failures", func(t *testing.T) {
		tests := []struct {
			name           string
			requestSuccess bool
			status         string
		}{
			{name: "explicit fail", requestSuccess: true, status: "LR_FAIL"},
			{name: "auto inherits request failure", requestSuccess: false, status: "LR_AUTO"},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
					return &protocol.Result{
						ActionName: action.Name,
						StartTime:  time.Now(),
						Duration:   time.Millisecond,
						Success:    tc.requestSuccess,
					}, nil
				})
				script := &dsl.Script{
					Name:     "replay-source-transaction",
					Protocol: protocolName,
					Config: dsl.ScriptConfig{
						ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(true)},
					},
					Actions: []dsl.Action{
						sourceTransactionAction("lr_start_transaction", ""),
						{
							Name:     "request",
							Type:     "GET",
							Method:   "GET",
							Target:   "https://example.test/",
							Metadata: map[string]any{"source_transaction": "ACME_ISBA"},
						},
						sourceTransactionAction("lr_end_transaction", tc.status),
					},
				}

				report, err := ReplayScript(context.Background(), script, ReplayOptions{IgnoreThinkTime: true})
				if err != nil {
					t.Fatalf("ReplayScript: %v", err)
				}
				if report.Success {
					t.Fatalf("report = %#v, failed source transaction must fail replay", report)
				}
				if len(report.Events) != 3 || report.Events[2].Success {
					t.Fatalf("events = %#v, transaction end must surface failure", report.Events)
				}
				if got := report.FinalVariables[sourceTransactionStatusKey("ACME_ISBA")]; got != "false" {
					t.Fatalf("final transaction status = %q, want false", got)
				}
			})
		}
	})

	for _, status := range []string{"LR_FAIL", "LR_AUTO"} {
		t.Run("main iteration exit "+status, func(t *testing.T) {
			var executed []string
			protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
				executed = append(executed, action.Name)
				return &protocol.Result{
					ActionName: action.Name,
					StartTime:  time.Now(),
					Duration:   time.Millisecond,
					Success:    true,
				}, nil
			})
			script := &dsl.Script{
				Name:     "replay-lr-exit",
				Protocol: protocolName,
				Config: dsl.ScriptConfig{
					ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(true)},
				},
				Actions: []dsl.Action{
					{Name: "before", Type: "GET", Method: "GET", Target: "https://example.test/before"},
					{
						Name: "exit",
						Type: "source_runtime",
						Params: map[string]any{
							"function": "lr_exit",
							"args":     []any{"LR_EXIT_MAIN_ITERATION_AND_CONTINUE", status},
						},
					},
					{Name: "after", Type: "GET", Method: "GET", Target: "https://example.test/after"},
				},
			}

			report, err := ReplayScript(context.Background(), script, ReplayOptions{IgnoreThinkTime: true})
			if err != nil {
				t.Fatalf("ReplayScript: %v", err)
			}
			if len(executed) != 1 || executed[0] != "before" {
				t.Fatalf("executed = %v, source-unreachable action ran after lr_exit", executed)
			}
			if len(report.Events) != 2 || report.Events[1].ActionName != "exit" {
				t.Fatalf("events = %#v, want before and exit only", report.Events)
			}
			wantSuccess := status != "LR_FAIL"
			if report.Success != wantSuccess || report.Events[1].Success != wantSuccess {
				t.Fatalf("report/event success = %v/%v, want %v for %s", report.Success, report.Events[1].Success, wantSuccess, status)
			}
		})
	}
}

func TestReplayFailedTransactionContinuesToExactExitWhenContinueOnErrorIsOff(t *testing.T) {
	tests := []struct {
		name       string
		beforeEnd  []dsl.Action
		endStatus  string
		endIndex   int
		eventCount int
	}{
		{
			name:       "explicit fail",
			endStatus:  "LR_FAIL",
			endIndex:   1,
			eventCount: 4,
		},
		{
			name: "auto fail",
			beforeEnd: []dsl.Action{{
				Name: "mark transaction failed",
				Type: "source_transaction",
				Params: map[string]any{
					"function": "lr_set_transaction_status",
					"args":     []any{"LR_FAIL"},
				},
			}},
			endStatus:  "LR_AUTO",
			endIndex:   2,
			eventCount: 5,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var executed []string
			protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
				executed = append(executed, action.Name)
				return &protocol.Result{
					ActionName: action.Name,
					StartTime:  time.Now(),
					Duration:   time.Millisecond,
					Success:    true,
				}, nil
			})
			actions := []dsl.Action{sourceTransactionAction("lr_start_transaction", "")}
			actions = append(actions, tc.beforeEnd...)
			actions = append(actions,
				sourceTransactionAction("lr_end_transaction", tc.endStatus),
				dsl.Action{Name: "failure message", Type: "error_message", Value: "transaction failed"},
				dsl.Action{
					Name: "exit",
					Type: "source_runtime",
					Params: map[string]any{
						"function": "lr_exit",
						"args":     []any{"LR_EXIT_MAIN_ITERATION_AND_CONTINUE", "LR_FAIL"},
					},
				},
				dsl.Action{Name: "after", Type: "GET", Method: "GET", Target: "https://example.test/after"},
			)
			script := &dsl.Script{
				Name:     "replay-failed-transaction-control",
				Protocol: protocolName,
				// ContinueOnError deliberately remains false: an LR transaction
				// status is not an action-level abort policy.
				Actions: actions,
			}

			report, err := ReplayScript(context.Background(), script, ReplayOptions{IgnoreThinkTime: true})
			if err != nil {
				t.Fatalf("ReplayScript: %v", err)
			}
			if report.Success {
				t.Fatalf("report = %#v, failed transaction/exit must fail replay", report)
			}
			if len(executed) != 0 {
				t.Fatalf("executed = %v, source-unreachable request ran after exact lr_exit", executed)
			}
			if len(report.Events) != tc.eventCount {
				t.Fatalf("events = %#v, want transaction branch through exact lr_exit", report.Events)
			}
			if report.Events[tc.endIndex].Success {
				t.Fatalf("transaction event = %#v, want failed status", report.Events[tc.endIndex])
			}
			if report.Events[len(report.Events)-2].ActionName != "failure message" ||
				report.Events[len(report.Events)-1].ActionName != "exit" {
				t.Fatalf("tail events = %#v, want failure message followed by exact lr_exit", report.Events)
			}
		})
	}
}

func TestLRExitMainIterationAndContinueStopsRemainingActions(t *testing.T) {
	protoStub := &sourceSemanticsProtocol{}
	sink := &sequenceCaptureSink{}
	vu := &VUser{
		ID:         1,
		Proto:      protoStub,
		Metrics:    NewMetricsCollector(16),
		ResultSink: sink,
		Script: &dsl.Script{
			Name:     "lr-exit",
			Protocol: "http",
			Config:   dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 2}},
			Actions: []dsl.Action{
				{Name: "before", Type: "GET", Method: "GET", Target: "https://example.test/before"},
				{
					Name: "exit",
					Type: "source_runtime",
					Params: map[string]any{
						"function": "lr_exit",
						"args":     []any{"LR_EXIT_MAIN_ITERATION_AND_CONTINUE", "LR_FAIL"},
					},
				},
				{Name: "after", Type: "GET", Method: "GET", Target: "https://example.test/after"},
			},
		},
	}

	vu.Run(context.Background())

	if len(protoStub.executed) != 2 || protoStub.executed[0] != "before" || protoStub.executed[1] != "before" {
		t.Fatalf("executed = %v, want only the pre-exit action in both iterations", protoStub.executed)
	}
	if len(sink.transactions) != 2 {
		t.Fatalf("iteration transactions = %#v, want two failed iterations", sink.transactions)
	}
	for _, tx := range sink.transactions {
		if tx.Success {
			t.Fatalf("LR_FAIL iteration transaction = %#v, want failed", tx)
		}
	}
}

func TestVUserDoesNotPaceAfterExplicitSourceTransactionGoal(t *testing.T) {
	protoStub := &sourceSemanticsProtocol{}
	sink := &sequenceCaptureSink{}
	runLogic := dsl.RunLogicConfig{MaxTransactions: 1}
	vu := &VUser{
		ID:             1,
		Proto:          protoStub,
		Metrics:        NewMetricsCollector(16),
		ResultSink:     sink,
		ExecutionGoals: NewExecutionGoalLimiter(runLogic, func() {}),
		Script: &dsl.Script{
			Name:     "explicit-transaction-goal",
			Protocol: "http",
			Config: dsl.ScriptConfig{
				RunLogic: runLogic,
				Pacing:   dsl.PacingConfig{Type: "fixed_delay", DelayMs: 350},
			},
			Actions: []dsl.Action{
				sourceTransactionAction("lr_start_transaction", ""),
				{
					Name:     "request",
					Type:     "GET",
					Method:   "GET",
					Target:   "https://example.test/",
					Metadata: map[string]any{"source_transaction": "ACME_ISBA"},
				},
				sourceTransactionAction("lr_end_transaction", "LR_PASS"),
			},
		},
	}

	started := time.Now()
	vu.Run(context.Background())

	if elapsed := time.Since(started); elapsed >= 200*time.Millisecond {
		t.Fatalf("explicit transaction goal paced after final transaction: elapsed=%v", elapsed)
	}
	if len(protoStub.executed) != 1 || len(sink.transactions) != 1 {
		t.Fatalf("executed=%v transactions=%#v, want one explicit transaction", protoStub.executed, sink.transactions)
	}
}

func TestVUserDoesNotPaceAfterStandaloneHTTPSequenceGoal(t *testing.T) {
	protoStub := &sourceSemanticsProtocol{}
	sink := &sequenceCaptureSink{}
	runLogic := dsl.RunLogicConfig{Iterations: 2, MaxTransactions: 1}
	vu := &VUser{
		ID:             1,
		Proto:          protoStub,
		Metrics:        NewMetricsCollector(16),
		ResultSink:     sink,
		ExecutionGoals: NewExecutionGoalLimiter(runLogic, func() {}),
		Script: &dsl.Script{
			Name:     "standalone-sequence-goal",
			Protocol: "http",
			Config: dsl.ScriptConfig{
				RunLogic: runLogic,
				Pacing:   dsl.PacingConfig{Type: "fixed_delay", DelayMs: 350},
			},
			Actions: []dsl.Action{{
				Name:     "Checkout",
				Type:     "http_sequence",
				Method:   "SEQUENCE",
				Metadata: map[string]any{"source_transaction": "Checkout"},
				Params: map[string]any{"requests": []dsl.Action{{
					Name: "request", Type: "GET", Method: "GET", Target: "https://example.test/",
				}}},
			}},
		},
	}

	started := time.Now()
	vu.Run(context.Background())

	if elapsed := time.Since(started); elapsed >= 200*time.Millisecond {
		t.Fatalf("standalone sequence goal paced after its final transaction: elapsed=%v", elapsed)
	}
	if len(protoStub.executed) != 1 || len(sink.transactions) != 1 {
		t.Fatalf("executed=%v transactions=%#v, want one standalone sequence transaction", protoStub.executed, sink.transactions)
	}
}

func TestVUserActionClaimWaitsForPendingTransactionRelease(t *testing.T) {
	runLogic := dsl.RunLogicConfig{Iterations: 1, MaxTransactions: 1}
	softCtx, softCancel := context.WithCancel(context.Background())
	defer softCancel()
	limiter := NewExecutionGoalLimiter(runLogic, softCancel)
	if !limiter.TryReserveTransaction() {
		t.Fatal("competing VUser failed to reserve the sole transaction slot")
	}
	sink := &sequenceCaptureSink{}
	vu := &VUser{
		ID:             1,
		Proto:          &sourceSemanticsProtocol{},
		Metrics:        NewMetricsCollector(16),
		ResultSink:     sink,
		ExecutionGoals: limiter,
		HardStopCtx:    context.Background(),
		Script: &dsl.Script{
			Name:     "wait-for-pending-source-transaction",
			Protocol: "http",
			Config:   dsl.ScriptConfig{RunLogic: runLogic},
			Actions: []dsl.Action{
				sourceTransactionAction("lr_start_transaction", ""),
				sourceTransactionAction("lr_end_transaction", "LR_PASS"),
			},
		},
	}
	done := make(chan struct{})
	go func() {
		vu.Run(softCtx)
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("VUser abandoned temporarily pending transaction capacity")
	case <-time.After(25 * time.Millisecond):
	}
	limiter.ReleaseTransactionReservation()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("VUser did not claim capacity after the competing reservation released")
	}
	if len(sink.transactions) != 1 || sink.transactions[0].ActionName != "ACME_ISBA" {
		t.Fatalf("transactions = %#v, want the waiting source transaction to complete", sink.transactions)
	}
}

func TestCrossGoalLifecycleSourceOwnerDoesNotDeadlockIterationReservation(t *testing.T) {
	runLogic := dsl.RunLogicConfig{
		Iterations:       1,
		GlobalIterations: 1,
		MaxTransactions:  1,
		AutoTransactions: "none",
	}
	softCtx, softCancel := context.WithCancel(context.Background())
	defer softCancel()
	limiter := NewExecutionGoalLimiter(runLogic, softCancel)
	barrierStarted := make(chan struct{})
	releaseBarrier := make(chan struct{})
	script := &dsl.Script{
		Name:     "cross-goal-lifecycle-source-owner",
		Protocol: "http",
		Config: dsl.ScriptConfig{
			RunLogic: runLogic,
			Pacing:   dsl.PacingConfig{Type: "tps", TPS: 1000},
		},
		Init: []dsl.Action{{
			Name: "conditional owner",
			Type: "control_if",
			Control: &dsl.ControlFlow{
				Condition: &dsl.Condition{Left: "{{var.owner}}", Op: "eq", Right: "yes"},
				Steps: []dsl.Action{
					sourceTransactionAction("lr_start_transaction", ""),
					{Name: "owner barrier", Type: "GET", Method: "GET", Target: "https://example.test/barrier"},
				},
			},
		}},
		Actions: []dsl.Action{
			sourceTransactionAction("lr_start_transaction", ""),
			sourceTransactionAction("lr_end_transaction", "LR_PASS"),
		},
	}
	ownerSink := &sequenceCaptureSink{}
	owner := &VUser{
		ID:                   1,
		Proto:                &sourceGoalBarrierProtocol{started: barrierStarted, release: releaseBarrier},
		Metrics:              NewMetricsCollector(16),
		ResultSink:           ownerSink,
		Variables:            map[string]string{"owner": "yes"},
		ExecutionGoals:       limiter,
		IterationRateLimiter: NewTokenBucket(1000),
		HardStopCtx:          context.Background(),
		ActionBoundaryCtx:    context.Background(),
		Script:               script,
	}
	peer := &VUser{
		ID:                   2,
		Proto:                &sourceSemanticsProtocol{},
		Metrics:              NewMetricsCollector(16),
		ResultSink:           &sequenceCaptureSink{},
		Variables:            map[string]string{"owner": "no"},
		ExecutionGoals:       limiter,
		IterationRateLimiter: NewTokenBucket(1000),
		HardStopCtx:          context.Background(),
		ActionBoundaryCtx:    context.Background(),
		Script:               script,
	}

	ownerDone := make(chan struct{})
	go func() {
		owner.Run(softCtx)
		close(ownerDone)
	}()
	select {
	case <-barrierStarted:
	case <-time.After(time.Second):
		t.Fatal("lifecycle source owner did not reach its barrier")
	}
	peerDone := make(chan struct{})
	go func() {
		peer.Run(softCtx)
		close(peerDone)
	}()
	deadline := time.Now().Add(time.Second)
	for {
		limiter.iterationMu.Lock()
		pending := limiter.pendingIterations
		limiter.iterationMu.Unlock()
		if pending == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("peer did not reserve the sole global iteration")
		}
		time.Sleep(time.Millisecond)
	}
	close(releaseBarrier)

	for name, done := range map[string]<-chan struct{}{"owner": ownerDone, "peer": peerDone} {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("%s VUser deadlocked across iteration/transaction goals", name)
		}
	}
	if len(ownerSink.transactions) != 1 || ownerSink.transactions[0].Success ||
		!strings.Contains(ownerSink.transactions[0].ErrorMessage, "global iteration slot") {
		t.Fatalf("owner transactions = %#v, want one failed finalized source scope", ownerSink.transactions)
	}
	limiter.iterationMu.Lock()
	pendingIterations := limiter.pendingIterations
	iterations := limiter.iterations
	limiter.iterationMu.Unlock()
	if pendingIterations != 0 || iterations != 0 {
		t.Fatalf("iteration limiter retained count/pending = %d/%d", iterations, pendingIterations)
	}
	limiter.transactionMu.Lock()
	transactions := limiter.transactions
	pendingTransactions := limiter.pendingTransactions
	limiter.transactionMu.Unlock()
	if transactions != 1 || pendingTransactions != 0 {
		t.Fatalf("transaction limiter count/pending = %d/%d, want 1/0", transactions, pendingTransactions)
	}
	if !limiter.TryStartIteration() {
		t.Fatal("released global iteration capacity remained unavailable")
	}
}

func TestLifecycleSourceOwnerDoesNotDeadlockPerIterationTransactionReservation(t *testing.T) {
	runLogic := dsl.RunLogicConfig{
		Iterations:       1,
		AutoTransactions: "per_iteration",
		MaxTransactions:  1,
	}
	softCtx, softCancel := context.WithCancel(context.Background())
	defer softCancel()
	limiter := NewExecutionGoalLimiter(runLogic, softCancel)
	sink := &sequenceCaptureSink{}
	protoStub := &sourceSemanticsProtocol{}
	vu := &VUser{
		Proto:          protoStub,
		Metrics:        NewMetricsCollector(16),
		ResultSink:     sink,
		ExecutionGoals: limiter,
		HardStopCtx:    context.Background(),
		Script: &dsl.Script{
			Name:     "lifecycle-source-per-iteration-overlap",
			Protocol: "http",
			Config:   dsl.ScriptConfig{RunLogic: runLogic},
			Init:     []dsl.Action{sourceTransactionAction("lr_start_transaction", "")},
			Actions:  []dsl.Action{{Name: "must not run", Type: "GET", Method: "GET", Target: "https://example.test/"}},
			Teardown: []dsl.Action{sourceTransactionAction("lr_end_transaction", "LR_PASS")},
		},
	}

	done := make(chan struct{})
	go func() {
		vu.Run(softCtx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("VUser deadlocked waiting on its own lifecycle transaction reservation")
	}
	if len(protoStub.executed) != 0 {
		t.Fatalf("protocol executions = %v, incompatible per-iteration work must not run", protoStub.executed)
	}
	if len(sink.transactions) != 1 || sink.transactions[0].Success ||
		!strings.Contains(sink.transactions[0].ErrorMessage, "per-iteration transaction slot") {
		t.Fatalf("transactions = %#v, want failed finalized lifecycle source scope", sink.transactions)
	}
	limiter.transactionMu.Lock()
	transactions := limiter.transactions
	pending := limiter.pendingTransactions
	limiter.transactionMu.Unlock()
	if transactions != 1 || pending != 0 {
		t.Fatalf("transaction limiter count/pending = %d/%d, want 1/0", transactions, pending)
	}
	if len(vu.sourceTransactions) != 0 {
		t.Fatalf("source transactions remained open: %#v", vu.sourceTransactions)
	}
	if _, exists := vu.Variables[sourceTransactionStartKey("ACME_ISBA")]; exists {
		t.Fatalf("legacy source start survived failed finalization: %#v", vu.Variables)
	}
}

func TestVUserReleasesUnusedExplicitSourceTransactionReservation(t *testing.T) {
	runLogic := dsl.RunLogicConfig{Iterations: 2, MaxTransactions: 1}
	limiter := NewExecutionGoalLimiter(runLogic, func() {})
	vu := &VUser{
		ID:             1,
		Proto:          &sourceSemanticsProtocol{},
		Metrics:        NewMetricsCollector(16),
		ExecutionGoals: limiter,
		Script: &dsl.Script{
			Name:     "skipped-explicit-transaction",
			Protocol: "http",
			Config:   dsl.ScriptConfig{RunLogic: runLogic},
			Actions: []dsl.Action{{
				Name: "never starts transaction",
				Type: "control_if",
				Control: &dsl.ControlFlow{
					Condition: &dsl.Condition{Expr: "false"},
					Steps: []dsl.Action{
						sourceTransactionAction("lr_start_transaction", ""),
						sourceTransactionAction("lr_end_transaction", "LR_PASS"),
					},
				},
			}},
		},
	}

	vu.Run(context.Background())

	if !limiter.TryStartTransaction() {
		t.Fatal("unexecuted source-transaction branch consumed its pre-pacing reservation")
	}
}

func TestWebCacheCleanupExecutesProtocolCacheController(t *testing.T) {
	protoStub := &sourceSemanticsProtocol{}
	vu := &VUser{
		ID:      1,
		Proto:   protoStub,
		Metrics: NewMetricsCollector(16),
		Script: &dsl.Script{
			Name:     "cache-clear",
			Protocol: "http",
			Config:   dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}},
			Actions: []dsl.Action{
				{
					Name: "cache clear",
					Type: "source_runtime",
					Params: map[string]any{
						"function": "web_cache_cleanup",
						"args":     []any{},
					},
				},
				{Name: "request", Type: "GET", Method: "GET", Target: "https://example.test/"},
			},
		},
	}

	vu.Run(context.Background())

	if protoStub.cacheClears != 1 {
		t.Fatalf("cache clears = %d, want 1", protoStub.cacheClears)
	}
}
