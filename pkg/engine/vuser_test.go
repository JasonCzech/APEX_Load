package engine

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type iterationResetProtocol struct {
	initCalls     int
	teardownCalls int
	executeCalls  int
}

type iterationSetupFailureProtocol struct {
	executeCalls    int
	teardownStarted chan struct{}
	releaseTeardown <-chan struct{}
}

type iterationContextIgnoringResetProtocol struct {
	executeCalls int
	resetStarted chan struct{}
	releaseReset <-chan struct{}
	resetOnce    sync.Once
}

type loggingProtocol struct {
	result       *protocol.Result
	executeCalls int
}

type sequenceProtocol struct {
	failFirst    bool
	executeError string
	actions      []*protocol.Action
}

type firstErrorProtocol struct {
	executeTimes []time.Time
}

type perActionExtractionProtocol struct{}

type teardownGoalDenialState struct {
	mu             sync.Mutex
	actions        []string
	initCalls      int
	teardownCalls  int
	emitterStarted chan struct{}
	releaseEmitter <-chan struct{}
	emitterOnce    sync.Once
}

type teardownGoalDenialProtocol struct {
	state *teardownGoalDenialState
}

var (
	teardownGoalDenialStateMu      sync.Mutex
	teardownGoalDenialStateCurrent *teardownGoalDenialState
)

type blockingPacingController struct {
	started chan struct{}
}

func (c *blockingPacingController) WaitIfPaused(ctx context.Context) error {
	return ctx.Err()
}

func (c *blockingPacingController) SleepActive(ctx context.Context, _ time.Duration) bool {
	close(c.started)
	<-ctx.Done()
	return false
}

func (*blockingPacingController) ActiveElapsed() time.Duration {
	return 0
}

func (*perActionExtractionProtocol) Name() string { return "http" }
func (*perActionExtractionProtocol) Init(context.Context, map[string]any) error {
	return nil
}
func (*perActionExtractionProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	return &protocol.Result{
		ActionName:  action.Name,
		Success:     true,
		Extractions: map[string]string{action.Name: strings.Repeat("x", protocol.MaxExtractionValueBytes)},
	}, nil
}
func (*perActionExtractionProtocol) Teardown(context.Context) error    { return nil }
func (*perActionExtractionProtocol) HealthCheck(context.Context) error { return nil }

func (*teardownGoalDenialProtocol) Name() string { return "teardown-goal-denial-test" }
func (p *teardownGoalDenialProtocol) Init(context.Context, map[string]any) error {
	if p.state != nil {
		p.state.mu.Lock()
		p.state.initCalls++
		p.state.mu.Unlock()
	}
	return nil
}
func (*teardownGoalDenialProtocol) Execute(context.Context, *protocol.Action) (*protocol.Result, error) {
	panic("teardown goal denial protocol requires ExecuteStream")
}
func (p *teardownGoalDenialProtocol) ExecuteStream(_ context.Context, action *protocol.Action, emit func(*protocol.Result) error) (*protocol.Result, error) {
	if p.state != nil {
		p.state.mu.Lock()
		p.state.actions = append(p.state.actions, action.Name)
		p.state.mu.Unlock()
	}
	switch action.Name {
	case "no transaction emitter":
		if p.state != nil && p.state.emitterStarted != nil {
			p.state.emitterOnce.Do(func() { close(p.state.emitterStarted) })
			if p.state.releaseEmitter != nil {
				<-p.state.releaseEmitter
			}
		}
	case "stream transaction":
		if emit != nil {
			if err := emit(&protocol.Result{
				ActionName:          action.Name,
				BusinessTransaction: true,
				Success:             true,
				Duration:            time.Millisecond,
			}); err != nil {
				return nil, err
			}
		}
	case "final transaction":
		return &protocol.Result{
			ActionName:          action.Name,
			BusinessTransaction: true,
			Success:             true,
			Duration:            time.Millisecond,
		}, nil
	}
	return &protocol.Result{ActionName: action.Name, Success: true, Duration: time.Millisecond}, nil
}
func (p *teardownGoalDenialProtocol) Teardown(context.Context) error {
	if p.state != nil {
		p.state.mu.Lock()
		p.state.teardownCalls++
		p.state.mu.Unlock()
	}
	return nil
}
func (*teardownGoalDenialProtocol) HealthCheck(context.Context) error { return nil }

func init() {
	protocol.Register("teardown-goal-denial-test", func() protocol.Protocol {
		teardownGoalDenialStateMu.Lock()
		defer teardownGoalDenialStateMu.Unlock()
		return &teardownGoalDenialProtocol{state: teardownGoalDenialStateCurrent}
	})
}

func installTeardownGoalDenialState(t *testing.T) *teardownGoalDenialState {
	t.Helper()
	state := &teardownGoalDenialState{}
	teardownGoalDenialStateMu.Lock()
	teardownGoalDenialStateCurrent = state
	teardownGoalDenialStateMu.Unlock()
	t.Cleanup(func() {
		teardownGoalDenialStateMu.Lock()
		if teardownGoalDenialStateCurrent == state {
			teardownGoalDenialStateCurrent = nil
		}
		teardownGoalDenialStateMu.Unlock()
	})
	return state
}

type sequenceCaptureSink struct {
	results      []*protocol.Result
	transactions []*protocol.Result
}

func (p *iterationResetProtocol) Name() string { return "browser" }
func (p *iterationResetProtocol) Init(_ context.Context, _ map[string]any) error {
	p.initCalls++
	return nil
}
func (p *iterationResetProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	p.executeCalls++
	return &protocol.Result{
		ActionName: action.Name,
		Success:    true,
		Duration:   time.Millisecond,
	}, nil
}
func (p *iterationResetProtocol) Teardown(_ context.Context) error {
	p.teardownCalls++
	return nil
}
func (p *iterationResetProtocol) HealthCheck(_ context.Context) error { return nil }

func (*iterationSetupFailureProtocol) Name() string { return "browser" }
func (*iterationSetupFailureProtocol) Init(context.Context, map[string]any) error {
	return fmt.Errorf("injected iteration setup failure")
}
func (p *iterationSetupFailureProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	p.executeCalls++
	if action.Name == "cleanup" && p.teardownStarted != nil {
		close(p.teardownStarted)
		if p.releaseTeardown != nil {
			<-p.releaseTeardown
		}
	}
	return &protocol.Result{ActionName: action.Name, Success: true, Duration: time.Millisecond}, nil
}
func (*iterationSetupFailureProtocol) Teardown(context.Context) error    { return nil }
func (*iterationSetupFailureProtocol) HealthCheck(context.Context) error { return nil }

func (*iterationContextIgnoringResetProtocol) Name() string { return "browser" }
func (p *iterationContextIgnoringResetProtocol) Init(context.Context, map[string]any) error {
	p.resetOnce.Do(func() {
		close(p.resetStarted)
		<-p.releaseReset
	})
	return nil
}
func (p *iterationContextIgnoringResetProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	p.executeCalls++
	return &protocol.Result{ActionName: action.Name, Success: true, Duration: time.Millisecond}, nil
}
func (*iterationContextIgnoringResetProtocol) Teardown(context.Context) error    { return nil }
func (*iterationContextIgnoringResetProtocol) HealthCheck(context.Context) error { return nil }

func (p *loggingProtocol) Name() string                                   { return "http" }
func (p *loggingProtocol) Init(_ context.Context, _ map[string]any) error { return nil }
func (p *loggingProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	p.executeCalls++
	result := *p.result
	result.ActionName = action.Name
	return &result, nil
}
func (p *loggingProtocol) Teardown(_ context.Context) error    { return nil }
func (p *loggingProtocol) HealthCheck(_ context.Context) error { return nil }

func (p *sequenceProtocol) Name() string { return "http" }
func (p *sequenceProtocol) Init(_ context.Context, _ map[string]any) error {
	return nil
}
func (p *sequenceProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	p.actions = append(p.actions, action)
	if p.executeError != "" {
		return nil, fmt.Errorf("sequence adapter failed: %s", p.executeError)
	}
	result := &protocol.Result{
		ActionName:       action.Name,
		Success:          true,
		Duration:         time.Millisecond,
		DependencyType:   "http",
		DependencyTarget: action.Target,
		Extractions:      map[string]string{},
	}
	if p.failFirst && len(p.actions) == 1 {
		result.Success = false
		result.ErrorMessage = "first child failed"
	}
	if action.Name == "Login" {
		result.Extractions["token"] = "abc123"
	}
	return result, nil
}
func (p *sequenceProtocol) Teardown(_ context.Context) error    { return nil }
func (p *sequenceProtocol) HealthCheck(_ context.Context) error { return nil }

func (p *firstErrorProtocol) Name() string                                   { return "http" }
func (p *firstErrorProtocol) Init(_ context.Context, _ map[string]any) error { return nil }
func (p *firstErrorProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	p.executeTimes = append(p.executeTimes, time.Now())
	if len(p.executeTimes) == 1 {
		return nil, fmt.Errorf("first action failed")
	}
	return &protocol.Result{ActionName: action.Name, Success: true, Duration: time.Millisecond}, nil
}
func (p *firstErrorProtocol) Teardown(_ context.Context) error    { return nil }
func (p *firstErrorProtocol) HealthCheck(_ context.Context) error { return nil }

func (s *sequenceCaptureSink) RecordResult(result *protocol.Result) error {
	s.results = append(s.results, result)
	return nil
}

func (s *sequenceCaptureSink) RecordTransaction(result *protocol.Result) error {
	s.transactions = append(s.transactions, result)
	return nil
}

func TestVUser_Interpolate(t *testing.T) {
	vu := &VUser{
		Variables: map[string]string{
			"token": "abc123",
			"host":  "example.com",
		},
	}

	tests := []struct {
		input string
		want  string
	}{
		{"{{var.token}}", "abc123"},
		{"https://{{var.host}}/api", "https://example.com/api"},
		{"Bearer {{var.token}}", "Bearer abc123"},
		{"{{data.host}}", "example.com"},
		{"no-vars-here", "no-vars-here"},
		{"", ""},
	}

	for _, tt := range tests {
		got := vu.interpolate(tt.input)
		if got != tt.want {
			t.Errorf("interpolate(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestVUser_Interpolate_EnvAndFn(t *testing.T) {
	t.Setenv("APEXLOAD_TEST_ENV", "env-value")
	t.Setenv("APEXLOAD_SECRET_DB_PASSWORD", "super-secret")
	t.Setenv(EnvScriptEnvAllowlist, "APEXLOAD_TEST_ENV")
	vu := &VUser{Variables: map[string]string{}}

	if got := vu.interpolate("{{env.APEXLOAD_TEST_ENV}}"); got != "env-value" {
		t.Fatalf("env interpolation = %q, want %q", got, "env-value")
	}

	gotUUID := vu.interpolate("{{fn.uuid()}}")
	uuidPattern := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if !uuidPattern.MatchString(gotUUID) {
		t.Fatalf("fn.uuid() interpolation returned non-uuid value: %q", gotUUID)
	}

	if got := vu.interpolate("{{env.APEXLOAD_MISSING_ENV}}"); got != "{{env.APEXLOAD_MISSING_ENV}}" {
		t.Fatalf("missing env should remain unchanged, got %q", got)
	}

	if got := vu.interpolate("{{secret.db_password}}"); got != "super-secret" {
		t.Fatalf("secret interpolation = %q, want %q", got, "super-secret")
	}
}

func TestVUser_InterpolateMap(t *testing.T) {
	vu := &VUser{
		Variables: map[string]string{"env": "staging"},
	}

	m := map[string]string{
		"Host":          "{{var.env}}.example.com",
		"Authorization": "static-value",
	}

	got := vu.interpolateMap(m)
	if got["Host"] != "staging.example.com" {
		t.Errorf("Host = %q, want %q", got["Host"], "staging.example.com")
	}
	if got["Authorization"] != "static-value" {
		t.Errorf("Authorization = %q, want %q", got["Authorization"], "static-value")
	}
}

func TestVUser_InterpolateMap_Nil(t *testing.T) {
	vu := &VUser{Variables: map[string]string{}}
	if got := vu.interpolateMap(nil); got != nil {
		t.Errorf("interpolateMap(nil) = %v, want nil", got)
	}
}

func TestVUser_MergeExtractions(t *testing.T) {
	vu := &VUser{Variables: map[string]string{"existing": "value"}}
	vu.mergeExtractions(&protocol.Result{
		Extractions: map[string]string{
			"token":    "new-token",
			"existing": "overwritten",
		},
	})

	if vu.Variables["token"] != "new-token" {
		t.Errorf("token = %q", vu.Variables["token"])
	}
	if vu.Variables["existing"] != "overwritten" {
		t.Errorf("existing should be overwritten, got %q", vu.Variables["existing"])
	}
	if got := scrubSensitiveTextWithConfig("/users/new-token", vu.RedactionConfig); got != "/users/[REDACTED]" {
		t.Errorf("sensitive extracted value remained observable: %q", got)
	}
}

func TestVUserMergeExtractionsBoundsPersistentVariableGrowth(t *testing.T) {
	vu := &VUser{Variables: map[string]string{}}
	for batch := 0; batch < maxVUserExtractedVariables/protocol.MaxExtractionEntries; batch++ {
		result := &protocol.Result{Extractions: make(map[string]string, protocol.MaxExtractionEntries)}
		for index := 0; index < protocol.MaxExtractionEntries; index++ {
			result.Extractions[fmt.Sprintf("value-%d-%d", batch, index)] = "ok"
		}
		if err := vu.mergeExtractions(result); err != nil {
			t.Fatalf("batch %d: %v", batch, err)
		}
	}
	overflow := &protocol.Result{Extractions: map[string]string{"one-too-many": "x"}}
	if err := vu.mergeExtractions(overflow); err == nil {
		t.Fatal("persistent extraction cardinality overflow was accepted")
	}
	if _, retained := vu.Variables["one-too-many"]; retained {
		t.Fatal("overflow extraction was partially retained")
	}

	oversized := &protocol.Result{Extractions: map[string]string{"payload": strings.Repeat("x", protocol.MaxExtractionValueBytes+1)}}
	if err := vu.mergeExtractions(oversized); err == nil {
		t.Fatal("oversized extraction value was accepted")
	}
}

func TestHTTPSequenceAggregateExtractionBudgetFailsClosed(t *testing.T) {
	requests := make([]dsl.Action, 5)
	for index := range requests {
		requests[index] = dsl.Action{Name: fmt.Sprintf("child-%d", index), Type: "GET", Method: "GET", Target: "/"}
	}
	vu := &VUser{
		Proto:     &perActionExtractionProtocol{},
		Script:    &dsl.Script{},
		Metrics:   NewMetricsCollector(16),
		Variables: map[string]string{},
	}
	result, err := vu.executeHTTPSequenceAction(context.Background(), dsl.Action{
		Name: "sequence", Type: "http_sequence", Params: map[string]any{"requests": requests},
	}, dsl.ErrorHandlingConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Success || !strings.Contains(result.ErrorMessage, "extraction retention failed") {
		t.Fatalf("sequence extraction result success=%v error=%q", result.Success, result.ErrorMessage)
	}
	if len(result.Extractions) != 0 {
		t.Fatalf("sequence retained %d partial extractions", len(result.Extractions))
	}
}

func TestHTTPSequenceTypedChildRequiresTarget(t *testing.T) {
	_, err := httpSequenceChildActions(dsl.Action{
		Name: "sequence",
		Type: "http_sequence",
		Params: map[string]any{
			"requests": []dsl.Action{{
				Name:   "missing target",
				Type:   "GET",
				Method: "GET",
			}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "missing target") {
		t.Fatalf("httpSequenceChildActions() error = %v, want typed-child target rejection", err)
	}
}

func TestHTTPSequenceChildMetadataProjectionDoesNotMutateSharedScript(t *testing.T) {
	childMetadata := map[string]any{"source_step_id": "child-step"}
	action := dsl.Action{
		Name:     "sequence",
		Type:     "http_sequence",
		Metadata: map[string]any{"source_transaction": "parent-transaction"},
		Params: map[string]any{
			"requests": []dsl.Action{{
				Name:     "child",
				Type:     "GET",
				Method:   "GET",
				Target:   "/child",
				Metadata: childMetadata,
			}},
		},
	}

	const workers = 32
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			children, err := httpSequenceChildActions(action)
			if err != nil {
				errs <- err
				return
			}
			if got := children[0].Metadata["source_transaction"]; got != "parent-transaction" {
				errs <- fmt.Errorf("inherited source transaction = %#v", got)
				return
			}
			children[0].Metadata["worker-local"] = true
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if _, mutated := childMetadata["source_transaction"]; mutated {
		t.Fatal("sequence projection mutated shared child metadata with parent inheritance")
	}
	if _, mutated := childMetadata["worker-local"]; mutated {
		t.Fatal("sequence projection returned the shared child metadata map")
	}
}

func TestHTTPSequenceCarriesChildThinkTimeWithoutInflatingResponseDuration(t *testing.T) {
	vu := &VUser{
		Proto: &sequenceProtocol{},
		Script: &dsl.Script{Config: dsl.ScriptConfig{ThinkTime: dsl.ThinkTimeConfig{
			Type: "recorded", Multiplier: 2, LimitMs: 30,
		}}},
		Metrics: NewMetricsCollector(16), Variables: map[string]string{},
	}
	start := time.Now()
	result, err := vu.executeHTTPSequenceAction(context.Background(), dsl.Action{
		Name: "sequence", Type: "http_sequence", Params: map[string]any{"requests": []dsl.Action{
			{Name: "first", Type: "GET", Method: "GET", Target: "/", ThinkTimeAfter: true, ThinkTime: dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 20}},
			{Name: "second", Type: "GET", Method: "GET", Target: "/next"},
		}},
	}, dsl.ErrorHandlingConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < 25*time.Millisecond {
		t.Fatalf("child think time did not execute: elapsed=%s", elapsed)
	}
	if result.Duration != 2*time.Millisecond {
		t.Fatalf("aggregate response duration = %s, want child response durations only", result.Duration)
	}
}

func TestVUserDoesNotPaceAfterLastFiniteIteration(t *testing.T) {
	vu := &VUser{
		Proto: &iterationResetProtocol{},
		Script: &dsl.Script{
			Protocol: "browser",
			Config: dsl.ScriptConfig{
				RunLogic: dsl.RunLogicConfig{Iterations: 1},
				Pacing:   dsl.PacingConfig{Type: "fixed_delay", DelayMs: 400},
			},
		},
		Metrics: NewMetricsCollector(16),
	}
	start := time.Now()
	vu.Run(context.Background())
	if elapsed := time.Since(start); elapsed >= 250*time.Millisecond {
		t.Fatalf("single finite iteration paced after completion: elapsed=%s", elapsed)
	}
}

func TestVUserInterruptedPacingReleasesIterationAndAutomaticTransactionReservations(t *testing.T) {
	runLogic := dsl.RunLogicConfig{
		AutoTransactions: "per_iteration",
		GlobalIterations: 2,
		MaxTransactions:  2,
	}
	limiter := NewExecutionGoalLimiter(runLogic, func() {})
	pauseControl := &blockingPacingController{started: make(chan struct{})}
	protoStub := &iterationResetProtocol{}
	vu := &VUser{
		Proto:          protoStub,
		Metrics:        NewMetricsCollector(16),
		PauseControl:   pauseControl,
		ExecutionGoals: limiter,
		Script: &dsl.Script{
			Name:     "interruptible-reservations",
			Protocol: "browser",
			Config: dsl.ScriptConfig{
				RunLogic: runLogic,
				Pacing:   dsl.PacingConfig{Type: "fixed_delay", DelayMs: 5_000},
			},
			Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		vu.Run(ctx)
	}()

	select {
	case <-pauseControl.started:
		cancel()
	case <-time.After(time.Second):
		cancel()
		t.Fatal("second iteration did not enter pacing")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("VUser did not stop after pacing cancellation")
	}

	if protoStub.executeCalls != 1 {
		t.Fatalf("executions = %d, want only the first committed iteration", protoStub.executeCalls)
	}
	if !limiter.TryStartIteration() {
		t.Fatal("interrupted pacing permanently consumed the second global-iteration slot")
	}
	if !limiter.TryStartTransaction() {
		t.Fatal("interrupted pacing permanently consumed the second per-iteration transaction slot")
	}
}

func TestVUserDoesNotPaceAfterPerActionTransactionGoal(t *testing.T) {
	protoStub := &iterationResetProtocol{}
	sink := &sequenceCaptureSink{}
	runLogic := dsl.RunLogicConfig{AutoTransactions: "per_action", MaxTransactions: 1}
	vu := &VUser{
		Proto: protoStub, Metrics: NewMetricsCollector(16), ResultSink: sink,
		ExecutionGoals: NewExecutionGoalLimiter(runLogic, func() {}),
		Script: &dsl.Script{
			Protocol: "browser",
			Config: dsl.ScriptConfig{
				RunLogic: runLogic,
				Pacing:   dsl.PacingConfig{Type: "fixed_delay", DelayMs: 350},
			},
			Actions: []dsl.Action{{Name: "only action", Type: "request", Target: "/"}},
		},
	}
	start := time.Now()
	vu.Run(context.Background())
	if elapsed := time.Since(start); elapsed >= 200*time.Millisecond {
		t.Fatalf("per-action transaction goal paced after its final transaction: elapsed=%s", elapsed)
	}
	if protoStub.executeCalls != 1 || len(sink.transactions) != 1 {
		t.Fatalf("executions=%d transactions=%d, want one reserved transaction executed once", protoStub.executeCalls, len(sink.transactions))
	}
}

func TestVUserReservesFirstTPSActionTransactionBeforeTokenWait(t *testing.T) {
	runLogic := dsl.RunLogicConfig{
		Iterations:       1,
		AutoTransactions: "per_action",
		MaxTransactions:  1,
	}
	softCtx, softCancel := context.WithCancel(context.Background())
	defer softCancel()
	limiter := NewExecutionGoalLimiter(runLogic, softCancel)
	rateLimiter := &TokenBucket{
		tokens: make(chan struct{}, 1),
		done:   make(chan struct{}),
	}
	script := &dsl.Script{
		Protocol: "browser",
		Config: dsl.ScriptConfig{
			RunLogic: runLogic,
			Pacing:   dsl.PacingConfig{Type: "tps", TPS: 1},
		},
		Actions: []dsl.Action{{Name: "only action", Type: "request", Target: "/"}},
	}

	protocols := make([]*iterationResetProtocol, 3)
	done := make(chan struct{}, len(protocols))
	for index := range protocols {
		protocols[index] = &iterationResetProtocol{}
		vu := &VUser{
			ID:                   index,
			Proto:                protocols[index],
			Script:               script,
			Metrics:              NewMetricsCollector(16),
			IterationRateLimiter: rateLimiter,
			ExecutionGoals:       limiter,
			HardStopCtx:          context.Background(),
		}
		go func() {
			vu.Run(softCtx)
			done <- struct{}{}
		}()
	}

	deadline := time.Now().Add(time.Second)
	for {
		limiter.transactionMu.Lock()
		pending := limiter.pendingTransactions
		limiter.transactionMu.Unlock()
		if pending == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first TPS iteration did not reserve its transaction before waiting")
		}
		time.Sleep(time.Millisecond)
	}
	rateLimiter.tokens <- struct{}{}

	for range protocols {
		select {
		case <-done:
		case <-time.After(time.Second):
			softCancel()
			t.Fatal("TPS VUsers remained blocked after the sole transaction committed")
		}
	}
	executions := 0
	for _, protoStub := range protocols {
		executions += protoStub.executeCalls
	}
	if executions != 1 {
		t.Fatalf("executions = %d, want exactly one transaction-bearing action", executions)
	}
}

func TestVUserTPSIterationAndSourceTransactionsDoNotWaitOnOwnReservation(t *testing.T) {
	runLogic := dsl.RunLogicConfig{
		Iterations:       1,
		AutoTransactions: "per_iteration",
		MaxTransactions:  1,
	}
	softCtx, softCancel := context.WithCancel(context.Background())
	defer softCancel()
	limiter := NewExecutionGoalLimiter(runLogic, softCancel)
	rateLimiter := &TokenBucket{
		tokens: make(chan struct{}, 1),
		done:   make(chan struct{}),
	}
	sink := &sequenceCaptureSink{}
	vu := &VUser{
		Proto:                &iterationResetProtocol{},
		Metrics:              NewMetricsCollector(16),
		ResultSink:           sink,
		IterationRateLimiter: rateLimiter,
		ExecutionGoals:       limiter,
		HardStopCtx:          context.Background(),
		Script: &dsl.Script{
			Protocol: "browser",
			Config: dsl.ScriptConfig{
				RunLogic: runLogic,
				Pacing:   dsl.PacingConfig{Type: "tps", TPS: 1},
			},
			Actions: []dsl.Action{sourceTransactionAction("lr_start_transaction", "")},
		},
	}
	done := make(chan struct{})
	go func() {
		vu.Run(softCtx)
		close(done)
	}()

	deadline := time.Now().Add(time.Second)
	for {
		limiter.transactionMu.Lock()
		pending := limiter.pendingTransactions
		limiter.transactionMu.Unlock()
		if pending == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("automatic iteration transaction was not reserved")
		}
		time.Sleep(time.Millisecond)
	}
	rateLimiter.tokens <- struct{}{}
	select {
	case <-done:
	case <-time.After(time.Second):
		softCancel()
		t.Fatal("VUser waited on a second transaction slot while holding its own reservation")
	}
	if len(sink.transactions) != 1 {
		t.Fatalf("recorded automatic transactions = %d, want the one reserved iteration transaction", len(sink.transactions))
	}
}

func TestVUserSetupFailureReleasesIterationAndAutomaticTransactionReservations(t *testing.T) {
	newUserPerIteration := true
	runLogic := dsl.RunLogicConfig{
		Iterations:       2,
		AutoTransactions: "per_iteration",
		GlobalIterations: 2,
		MaxTransactions:  2,
	}
	limiter := NewExecutionGoalLimiter(runLogic, func() {})
	protoStub := &iterationSetupFailureProtocol{}
	sink := &sequenceCaptureSink{}
	vu := &VUser{
		Proto:          protoStub,
		Metrics:        NewMetricsCollector(16),
		ResultSink:     sink,
		ExecutionGoals: limiter,
		Script: &dsl.Script{
			Protocol: "browser",
			Config: dsl.ScriptConfig{
				RunLogic: runLogic,
				Browser:  dsl.BrowserConfig{NewUserPerIter: &newUserPerIteration},
			},
			Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}},
		},
	}

	vu.Run(context.Background())

	if protoStub.executeCalls != 1 {
		t.Fatalf("executions = %d, want only the first successfully prepared iteration", protoStub.executeCalls)
	}
	if len(sink.transactions) != 1 {
		t.Fatalf("recorded automatic transactions = %d, want one", len(sink.transactions))
	}
	if !limiter.TryStartIteration() {
		t.Fatal("failed setup consumed the second global-iteration reservation")
	}
	if !limiter.TryStartTransaction() {
		t.Fatal("failed setup consumed the second automatic-transaction reservation")
	}
}

func TestVUserSetupFailureReleasesReservationsBeforeTeardown(t *testing.T) {
	newUserPerIteration := true
	runLogic := dsl.RunLogicConfig{
		Iterations:       2,
		AutoTransactions: "per_iteration",
		GlobalIterations: 2,
		MaxTransactions:  2,
	}
	limiter := NewExecutionGoalLimiter(runLogic, func() {})
	teardownStarted := make(chan struct{})
	releaseTeardown := make(chan struct{})
	protoStub := &iterationSetupFailureProtocol{
		teardownStarted: teardownStarted,
		releaseTeardown: releaseTeardown,
	}
	vu := &VUser{
		Proto:          protoStub,
		Metrics:        NewMetricsCollector(16),
		ExecutionGoals: limiter,
		Script: &dsl.Script{
			Protocol: "browser",
			Config: dsl.ScriptConfig{
				RunLogic: runLogic,
				Browser:  dsl.BrowserConfig{NewUserPerIter: &newUserPerIteration},
			},
			Actions:  []dsl.Action{{Name: "request", Type: "request", Target: "/"}},
			Teardown: []dsl.Action{{Name: "cleanup", Type: "request", Target: "/cleanup"}},
		},
	}
	done := make(chan struct{})
	go func() {
		vu.Run(context.Background())
		close(done)
	}()

	select {
	case <-teardownStarted:
	case <-time.After(time.Second):
		t.Fatal("setup failure did not enter teardown")
	}
	if !limiter.TryStartIteration() {
		close(releaseTeardown)
		t.Fatal("failed setup held its iteration reservation throughout teardown")
	}
	if !limiter.TryStartTransaction() {
		close(releaseTeardown)
		t.Fatal("failed setup held its transaction reservation throughout teardown")
	}
	close(releaseTeardown)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("VUser did not finish after teardown was released")
	}
}

func TestIterationResetUsesDynamicGoalBoundaryAndRechecksIgnoredContext(t *testing.T) {
	newUserPerIteration := true
	for _, entitled := range []bool{false, true} {
		name := "soft"
		if entitled {
			name = "goal-entitled"
		}
		t.Run(name, func(t *testing.T) {
			resetStarted := make(chan struct{})
			releaseReset := make(chan struct{})
			protoStub := &iterationContextIgnoringResetProtocol{
				resetStarted: resetStarted,
				releaseReset: releaseReset,
			}
			runLogic := dsl.RunLogicConfig{Iterations: 2}
			softCtx, softCancel := context.WithCancel(context.Background())
			defer softCancel()
			var limiter *ExecutionGoalLimiter
			if entitled {
				runLogic.GlobalIterations = 2
				limiter = NewExecutionGoalLimiter(runLogic, softCancel)
			}
			vu := &VUser{
				Proto:             protoStub,
				Metrics:           NewMetricsCollector(16),
				ExecutionGoals:    limiter,
				HardStopCtx:       context.Background(),
				ActionBoundaryCtx: context.Background(),
				Script: &dsl.Script{
					Protocol: "browser",
					Config: dsl.ScriptConfig{
						RunLogic: runLogic,
						Browser:  dsl.BrowserConfig{NewUserPerIter: &newUserPerIteration},
					},
					Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}},
				},
			}
			done := make(chan struct{})
			go func() {
				vu.Run(softCtx)
				close(done)
			}()
			select {
			case <-resetStarted:
			case <-time.After(time.Second):
				t.Fatal("second iteration did not enter protocol reset")
			}
			softCancel()
			close(releaseReset)
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("VUser did not finish after reset returned")
			}
			wantExecutions := 1
			if entitled {
				wantExecutions = 2
			}
			if protoStub.executeCalls != wantExecutions {
				t.Fatalf("executions = %d, want %d", protoStub.executeCalls, wantExecutions)
			}
		})
	}
}

func TestVUserReleasesUnusedPrePacingTransactionReservation(t *testing.T) {
	protoStub := &iterationResetProtocol{}
	runLogic := dsl.RunLogicConfig{
		Iterations:       2,
		AutoTransactions: "per_action",
		MaxTransactions:  1,
	}
	limiter := NewExecutionGoalLimiter(runLogic, func() {})
	vu := &VUser{
		Proto: protoStub, Metrics: NewMetricsCollector(16), ExecutionGoals: limiter,
		Script: &dsl.Script{
			Protocol: "browser",
			Config:   dsl.ScriptConfig{RunLogic: runLogic},
			Actions: []dsl.Action{{
				Name: "skipped request", Type: "control_if",
				Control: &dsl.ControlFlow{
					Condition: &dsl.Condition{Expr: "false"},
					Steps:     []dsl.Action{{Name: "never", Type: "request", Target: "/never"}},
				},
			}},
		},
	}
	vu.Run(context.Background())
	if protoStub.executeCalls != 0 {
		t.Fatalf("skipped control branch executed %d requests", protoStub.executeCalls)
	}
	if !limiter.TryStartTransaction() {
		t.Fatal("unused pre-pacing reservation consumed the transaction goal")
	}
}

func TestUnusedKnownEmitterReservationRestoresSoftActionBoundary(t *testing.T) {
	state := installTeardownGoalDenialState(t)
	emitterStarted := make(chan struct{})
	releaseEmitter := make(chan struct{})
	state.emitterStarted = emitterStarted
	state.releaseEmitter = releaseEmitter
	runLogic := dsl.RunLogicConfig{
		Iterations:      1,
		MaxTransactions: 1,
	}
	softCtx, softCancel := context.WithCancel(context.Background())
	defer softCancel()
	limiter := NewExecutionGoalLimiter(runLogic, softCancel)
	vu := &VUser{
		Proto:             &teardownGoalDenialProtocol{state: state},
		Metrics:           NewMetricsCollector(16),
		ExecutionGoals:    limiter,
		HardStopCtx:       context.Background(),
		ActionBoundaryCtx: context.Background(),
		Script: &dsl.Script{
			Protocol: "teardown-goal-denial-test",
			Config: dsl.ScriptConfig{
				RunLogic:  runLogic,
				ThinkTime: dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 1_000},
			},
			Actions: []dsl.Action{
				{
					Name:           "no transaction emitter",
					Type:           "java_vuser_run",
					Target:         "/emitter",
					ThinkTimeAfter: true,
					Metadata:       map[string]any{"emits_business_transactions": true},
				},
				{Name: "must not run after peer goal", Type: "request", Target: "/after"},
			},
		},
	}
	done := make(chan struct{})
	go func() {
		vu.Run(softCtx)
		close(done)
	}()
	select {
	case <-emitterStarted:
	case <-time.After(time.Second):
		t.Fatal("known emitter did not start after reserving its transaction")
	}
	peerClaimed := make(chan bool, 1)
	go func() {
		peerClaimed <- limiter.WaitStartTransaction(softCtx)
	}()
	close(releaseEmitter)
	select {
	case claimed := <-peerClaimed:
		if !claimed {
			t.Fatal("peer did not inherit the emitter's unused reservation")
		}
	case <-time.After(time.Second):
		t.Fatal("peer remained blocked after the unused emitter reservation was released")
	}
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("VUser retained a hard boundary after releasing its sole entitlement")
	}
	state.mu.Lock()
	got := append([]string(nil), state.actions...)
	state.mu.Unlock()
	if len(got) != 1 || got[0] != "no transaction emitter" {
		t.Fatalf("protocol executions = %v, want only the unentitled emitter action", got)
	}
}

func TestVUserContinuedHardErrorRetainsActionThinkTime(t *testing.T) {
	waitAction := dsl.Action{
		Name: "failed", Type: "GET", Target: "/failed", ThinkTimeAfter: true,
		ThinkTime: dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 35},
	}
	nextAction := dsl.Action{Name: "next", Type: "GET", Target: "/next"}

	t.Run("main", func(t *testing.T) {
		protoStub := &firstErrorProtocol{}
		vu := &VUser{
			Proto: protoStub, Metrics: NewMetricsCollector(16),
			Script: &dsl.Script{
				Protocol: "http",
				Config: dsl.ScriptConfig{
					RunLogic:      dsl.RunLogicConfig{Iterations: 1},
					ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(true)},
				},
				Actions: []dsl.Action{waitAction, nextAction},
			},
		}
		vu.Run(context.Background())
		assertContinuedActionWait(t, protoStub.executeTimes)
	})

	t.Run("teardown", func(t *testing.T) {
		protoStub := &firstErrorProtocol{}
		vu := &VUser{
			Proto: protoStub, Metrics: NewMetricsCollector(16),
			Script: &dsl.Script{
				Protocol: "http",
				Config:   dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}},
				Teardown: []dsl.Action{waitAction, nextAction},
			},
		}
		vu.Run(context.Background())
		assertContinuedActionWait(t, protoStub.executeTimes)
	})
}

func TestVUserMalformedHTTPSequenceRetainsParentThinkTimeWhenContinuing(t *testing.T) {
	protoStub := &sequenceProtocol{}
	vu := &VUser{
		Proto: protoStub, Metrics: NewMetricsCollector(16),
		Script: &dsl.Script{
			Protocol: "http",
			Config: dsl.ScriptConfig{
				RunLogic:      dsl.RunLogicConfig{Iterations: 1},
				ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(true)},
			},
			Actions: []dsl.Action{
				{
					Name: "malformed", Type: "http_sequence",
					Params:         map[string]any{"requests": "not-an-array"},
					ThinkTimeAfter: true,
					ThinkTime:      dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 45},
				},
				{Name: "after", Type: "GET", Method: "GET", Target: "/after"},
			},
		},
	}
	start := time.Now()
	vu.Run(context.Background())
	if elapsed := time.Since(start); elapsed < 35*time.Millisecond {
		t.Fatalf("continued malformed sequence skipped parent think time: elapsed=%s", elapsed)
	}
	if len(protoStub.actions) != 1 || protoStub.actions[0].Name != "after" {
		t.Fatalf("protocol actions = %#v, want only the action after the malformed container", protoStub.actions)
	}
}

func assertContinuedActionWait(t *testing.T, executeTimes []time.Time) {
	t.Helper()
	if len(executeTimes) != 2 {
		t.Fatalf("executed actions = %d, want 2", len(executeTimes))
	}
	if gap := executeTimes[1].Sub(executeTimes[0]); gap < 25*time.Millisecond {
		t.Fatalf("continued action started after %s, want retained think time", gap)
	}
}

func TestVUserLifecycleHTTPSequenceExecutesChildrenAndActionWaits(t *testing.T) {
	protoStub := &sequenceProtocol{}
	sink := &sequenceCaptureSink{}
	vu := &VUser{
		Proto: protoStub, Metrics: NewMetricsCollector(16), ResultSink: sink,
		Variables: map[string]string{},
		Script: &dsl.Script{
			Protocol: "http",
			Config: dsl.ScriptConfig{
				RunLogic:  dsl.RunLogicConfig{Iterations: 1},
				ThinkTime: dsl.ThinkTimeConfig{Type: "recorded", Multiplier: 1},
			},
			Init: []dsl.Action{{
				Name: "init sequence", Type: "http_sequence",
				Params: map[string]any{"requests": []dsl.Action{
					{Name: "Login", Type: "GET", Method: "GET", Target: "/login", ThinkTimeAfter: true, ThinkTime: dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 25}},
					{Name: "warmup", Type: "GET", Method: "GET", Target: "/warmup"},
				}},
			}},
		},
	}
	start := time.Now()
	vu.Run(context.Background())
	if len(protoStub.actions) != 2 || len(sink.results) != 2 || len(sink.transactions) != 1 {
		t.Fatalf("lifecycle sequence actions=%d results=%d transactions=%d", len(protoStub.actions), len(sink.results), len(sink.transactions))
	}
	if sink.transactions[0].ActionName != "init sequence" {
		t.Fatalf("first transaction = %#v, want lifecycle sequence aggregate", sink.transactions[0])
	}
	if elapsed := time.Since(start); elapsed < 20*time.Millisecond {
		t.Fatalf("lifecycle sequence skipped child wait: elapsed=%s", elapsed)
	}
}

func TestRunShakeoutActionRejectsExtractionBudgetViolation(t *testing.T) {
	vu := &VUser{
		Proto: &loggingProtocol{result: &protocol.Result{
			Success:     true,
			Extractions: map[string]string{"payload": strings.Repeat("x", protocol.MaxExtractionValueBytes+1)},
		}},
		Script:    &dsl.Script{},
		Variables: map[string]string{},
	}
	err := runShakeoutAction(context.Background(), vu, dsl.Action{Name: "extract", Type: "request"}, "action")
	if err == nil || !strings.Contains(err.Error(), "extraction retention failed") {
		t.Fatalf("shakeout extraction error = %v", err)
	}
	if _, retained := vu.Variables["payload"]; retained {
		t.Fatal("shakeout retained an extraction that exceeded the runtime budget")
	}
}

func TestVUser_ResolveAction(t *testing.T) {
	t.Setenv("APEXLOAD_ACTION_ENV", "env-action")
	t.Setenv(EnvScriptEnvAllowlist, "APEXLOAD_ACTION_ENV")
	maxRedirects := 2

	vu := &VUser{
		Variables: map[string]string{
			"base":           "https://api.test.com",
			"users.username": "alice",
		},
	}

	action := dsl.Action{
		Name:     "get-user",
		Type:     "request",
		Method:   "GET",
		Target:   "{{var.base}}/users/1",
		Selector: "#{{data.users.username}}",
		Value:    "{{env.APEXLOAD_ACTION_ENV}}",
		Headers:  map[string]string{"Accept": "application/json"},
		Body:     `{"key": "{{var.base}}"}`,
		Params: map[string]any{
			"user": "{{data.users.username}}",
			"auth": map[string]any{
				"token": "{{env.APEXLOAD_ACTION_ENV}}",
			},
			"labels": []any{"{{var.base}}", 123},
		},
		Extractors: []dsl.Extractor{
			{Name: "id", Source: "body", Type: "jsonpath", Expression: "$.id"},
		},
		WaitFor: &dsl.WaitCondition{Type: "text", Value: "{{data.users.username}}", TimeoutMs: 2500},
		Capture: &dsl.BrowserCaptureOptions{Screenshot: true, HTML: true},
		Metadata: map[string]any{
			"note": "{{var.base}}",
		},
		Auth: &dsl.AuthConfig{
			Type:  "bearer",
			Token: "{{env.APEXLOAD_ACTION_ENV}}",
		},
		TimeoutMs:    1500,
		MaxRedirects: &maxRedirects,
		SOAP: &dsl.SOAPActionConfig{
			Action:    "urn:user#{{data.users.username}}",
			Operation: "Lookup{{data.users.username}}",
			BodyMode:  "raw_xml",
		},
		Assertions: []dsl.Assertion{
			{Type: "status_code", Expected: 200},
		},
	}

	resolved := vu.resolveAction(action)

	if resolved.Name != "get-user" {
		t.Errorf("Name = %q", resolved.Name)
	}
	if resolved.Target != "https://api.test.com/users/1" {
		t.Errorf("Target = %q", resolved.Target)
	}
	if resolved.Selector != "#alice" {
		t.Errorf("Selector = %q, want #alice", resolved.Selector)
	}
	if resolved.Value != "env-action" {
		t.Errorf("Value = %q, want env-action", resolved.Value)
	}
	if string(resolved.Body) != `{"key": "https://api.test.com"}` {
		t.Errorf("Body = %q", string(resolved.Body))
	}
	if resolved.Params["user"] != "alice" {
		t.Errorf("Params[user] = %v, want alice", resolved.Params["user"])
	}
	auth, ok := resolved.Params["auth"].(map[string]any)
	if !ok || auth["token"] != "env-action" {
		t.Errorf("Params[auth] = %#v, want token env-action", resolved.Params["auth"])
	}
	labels, ok := resolved.Params["labels"].([]any)
	if !ok || len(labels) != 2 || labels[0] != "https://api.test.com" || labels[1] != 123 {
		t.Errorf("Params[labels] = %#v", resolved.Params["labels"])
	}
	if len(resolved.Extractors) != 1 {
		t.Fatalf("Extractors count = %d", len(resolved.Extractors))
	}
	if resolved.WaitFor == nil || resolved.WaitFor.Value != "alice" || resolved.WaitFor.TimeoutMs != 2500 {
		t.Fatalf("WaitFor = %#v, want text wait with alice", resolved.WaitFor)
	}
	if resolved.Capture == nil || !resolved.Capture.Screenshot || !resolved.Capture.HTML {
		t.Fatalf("Capture = %#v, want screenshot/html enabled", resolved.Capture)
	}
	if resolved.Metadata["note"] != "https://api.test.com" {
		t.Errorf("Metadata[note] = %v, want https://api.test.com", resolved.Metadata["note"])
	}
	if resolved.Auth == nil || resolved.Auth.Type != "bearer" || resolved.Auth.Token != "env-action" {
		t.Fatalf("Auth = %#v, want bearer env-action", resolved.Auth)
	}
	if resolved.TimeoutMs != 1500 {
		t.Fatalf("TimeoutMs = %d, want 1500", resolved.TimeoutMs)
	}
	if resolved.MaxRedirects == nil || *resolved.MaxRedirects != 2 {
		t.Fatalf("MaxRedirects = %#v, want 2", resolved.MaxRedirects)
	}
	if resolved.SOAP == nil {
		t.Fatal("SOAP = nil, want interpolated SOAP action config")
	}
	if resolved.SOAP.Action != "urn:user#alice" {
		t.Errorf("SOAP.Action = %q, want urn:user#alice", resolved.SOAP.Action)
	}
	if resolved.SOAP.Operation != "Lookupalice" {
		t.Errorf("SOAP.Operation = %q, want Lookupalice", resolved.SOAP.Operation)
	}
	if resolved.SOAP.BodyMode != "raw_xml" {
		t.Errorf("SOAP.BodyMode = %q, want raw_xml", resolved.SOAP.BodyMode)
	}
	if len(resolved.Assertions) != 1 {
		t.Fatalf("Assertions count = %d", len(resolved.Assertions))
	}
}

func TestVUserResolveActionInterpolatesHTTPDefaultHeadersPerVUser(t *testing.T) {
	t.Setenv("APEXLOAD_DEFAULT_HEADER_ENV", "env-route")
	t.Setenv(EnvScriptEnvAllowlist, "APEXLOAD_DEFAULT_HEADER_ENV")
	vu := &VUser{
		Script: &dsl.Script{Protocol: "http", Config: dsl.ScriptConfig{HTTP: dsl.HTTPConfig{
			DefaultHeaders: map[string]string{
				"X-Tenant": "{{var.tenant}}",
			},
			DefaultHeaderList: dsl.HeaderList{
				{Name: "X-Route", Value: "{{env.APEXLOAD_DEFAULT_HEADER_ENV}}"},
				{Name: "X-Repeat", Value: "{{var.first}}"},
				{Name: "X-Repeat", Value: "{{var.second}}"},
			},
		}}},
		Variables: map[string]string{
			"tenant": "tenant-42",
			"first":  "one",
			"second": "two",
		},
	}

	resolved := vu.resolveAction(dsl.Action{Name: "request", Type: "GET", Target: "/"})

	if !resolved.ResolvedDefaultHeaders {
		t.Fatal("ResolvedDefaultHeaders = false, want engine-owned action defaults")
	}
	if got := resolved.DefaultHeaders["X-Tenant"]; got != "tenant-42" {
		t.Fatalf("X-Tenant = %q, want tenant-42", got)
	}
	want := protocol.HeaderList{
		{Name: "X-Route", Value: "env-route"},
		{Name: "X-Repeat", Value: "one"},
		{Name: "X-Repeat", Value: "two"},
	}
	if !reflect.DeepEqual(resolved.DefaultHeaderList, want) {
		t.Fatalf("DefaultHeaderList = %#v, want %#v", resolved.DefaultHeaderList, want)
	}

	vu.Script.Protocol = "browser"
	ignored := vu.resolveAction(dsl.Action{Name: "browser action", Type: "navigate", Target: "/"})
	if ignored.ResolvedDefaultHeaders || len(ignored.DefaultHeaders) != 0 || len(ignored.DefaultHeaderList) != 0 {
		t.Fatalf("browser action received HTTP transport defaults: %#v", ignored)
	}
}

func TestSnapshotStore(t *testing.T) {
	store := NewSnapshotStore(3)

	store.Add(&protocol.ErrorSnapshot{RequestURL: "http://a.com"})
	store.Add(&protocol.ErrorSnapshot{RequestURL: "http://b.com"})
	store.Add(&protocol.ErrorSnapshot{RequestURL: "http://c.com"})
	store.Add(&protocol.ErrorSnapshot{RequestURL: "http://d.com"}) // should be dropped (max 3)

	snaps := store.Snapshots()
	if len(snaps) != 3 {
		t.Errorf("snapshots count = %d, want 3", len(snaps))
	}
}

func TestSnapshotStore_NilSafe(t *testing.T) {
	var store *SnapshotStore
	store.Add(&protocol.ErrorSnapshot{}) // should not panic
	if store.Snapshots() != nil {
		t.Error("nil store Snapshots() should return nil")
	}
}

func TestSnapshotStore_NilSnapshot(t *testing.T) {
	store := NewSnapshotStore(10)
	store.Add(nil) // should not panic or add
	if len(store.Snapshots()) != 0 {
		t.Error("nil snapshot should not be added")
	}
}

func TestSnapshotStoreBoundsIndividualAndAggregateBytes(t *testing.T) {
	store := newSnapshotStoreWithLimits(10, 1024, 1500)
	large := strings.Repeat("x", 8<<10)

	store.Add(&protocol.ErrorSnapshot{RequestURL: "http://one.example", ResponseBody: large})
	store.Add(&protocol.ErrorSnapshot{RequestURL: "http://two.example", ResponseBody: large})
	store.Add(&protocol.ErrorSnapshot{RequestURL: "http://three.example", ResponseBody: large})

	if store.storedBytes > 1500 {
		t.Fatalf("stored snapshot bytes = %d, limit 1500", store.storedBytes)
	}
	if len(store.Snapshots()) != 2 {
		t.Fatalf("snapshot count = %d, want 2 retained within aggregate budget", len(store.Snapshots()))
	}
	if got := len(store.Snapshots()[0].ResponseBody); got >= len(large) {
		t.Fatalf("first response body length = %d, want truncated below %d", got, len(large))
	}
	if !strings.Contains(store.Snapshots()[0].ResponseBody, snapshotTruncatedMarker) {
		t.Fatalf("first response body does not mark truncation: %q", store.Snapshots()[0].ResponseBody)
	}
}

func TestSnapshotStoreClonesRetainedSnapshot(t *testing.T) {
	store := NewSnapshotStore(1)
	snapshot := &protocol.ErrorSnapshot{
		RequestURL:      "http://example.test",
		ResponseHeaders: map[string]string{"X-Test": "before"},
		ResponseBody:    "before",
	}
	store.Add(snapshot)
	snapshot.ResponseBody = "after"
	snapshot.ResponseHeaders["X-Test"] = "after"

	retained := store.Snapshots()[0]
	if retained.ResponseBody != "before" || retained.ResponseHeaders["X-Test"] != "before" {
		t.Fatalf("retained snapshot aliases caller: %#v", retained)
	}
}

func TestReplaceAll(t *testing.T) {
	tests := []struct {
		s, old, new, want string
	}{
		{"hello world", "world", "go", "hello go"},
		{"aaa", "a", "bb", "bbbbbb"},
		{"no match", "xyz", "abc", "no match"},
		{"", "a", "b", ""},
	}

	for _, tt := range tests {
		got := replaceAll(tt.s, tt.old, tt.new)
		if got != tt.want {
			t.Errorf("replaceAll(%q, %q, %q) = %q, want %q", tt.s, tt.old, tt.new, got, tt.want)
		}
	}
}

func TestIndexOf(t *testing.T) {
	tests := []struct {
		s, substr string
		want      int
	}{
		{"hello world", "world", 6},
		{"hello world", "xyz", -1},
		{"aaa", "a", 0},
		{"", "a", -1},
	}

	for _, tt := range tests {
		got := indexOf(tt.s, tt.substr)
		if got != tt.want {
			t.Errorf("indexOf(%q, %q) = %d, want %d", tt.s, tt.substr, got, tt.want)
		}
	}
}

func TestToProtocolExtractors(t *testing.T) {
	exts := []dsl.Extractor{
		{Name: "token", Source: "body", Type: "jsonpath", Expression: "$.token"},
		{Name: "header_val", Source: "header", Type: "regex", Expression: "X-Request-Id: (.+)"},
	}

	result := toProtocolExtractors(exts, nil)
	if len(result) != 2 {
		t.Fatalf("count = %d, want 2", len(result))
	}
	if result[0].Name != "token" || result[0].Source != "body" {
		t.Errorf("extractor[0] = %+v", result[0])
	}
}

func TestToProtocolAssertions(t *testing.T) {
	asserts := []dsl.Assertion{
		{Type: "status_code", Expected: 200},
		{Type: "status_code_range", Min: 200, Max: 299},
	}

	result := toProtocolAssertions(asserts, nil)
	if len(result) != 2 {
		t.Fatalf("count = %d, want 2", len(result))
	}
	if result[0].Type != "status_code" {
		t.Errorf("assertion[0].Type = %q", result[0].Type)
	}
	if result[1].Min != 200 || result[1].Max != 299 {
		t.Errorf("assertion[1] range = %d-%d, want 200-299", result[1].Min, result[1].Max)
	}
}

func TestResolveActionInterpolatesExtractorAndAssertionInputs(t *testing.T) {
	vu := &VUser{Variables: map[string]string{
		"capture_pattern": `token=([a-z]+)`,
		"expected_body":   "ready",
		"expected_header": "application/json",
	}}
	action := dsl.Action{
		Name: "templated checks",
		Extractors: []dsl.Extractor{{
			Name:       "token",
			Source:     "body",
			Type:       "regex",
			Expression: "{{var.capture_pattern}}",
		}},
		Assertions: []dsl.Assertion{
			{Type: "body_contains", Value: "{{var.expected_body}}"},
			{Type: "header_contains", Value: "Content-Type", Expected: "{{var.expected_header}}"},
		},
	}

	resolved := vu.resolveAction(action)
	if got := resolved.Extractors[0].Expression; got != `token=([a-z]+)` {
		t.Fatalf("extractor expression = %q, want interpolated pattern", got)
	}
	if got := resolved.Assertions[0].Value; got != "ready" {
		t.Fatalf("body assertion value = %q, want interpolated value", got)
	}
	if got := resolved.Assertions[1].Expected; got != "application/json" {
		t.Fatalf("header assertion expected = %#v, want interpolated value", got)
	}
	if action.Extractors[0].Expression != "{{var.capture_pattern}}" ||
		action.Assertions[0].Value != "{{var.expected_body}}" {
		t.Fatal("resolveAction mutated the shared source action")
	}
}

func TestResolveActionTracksUnresolvedParameterInAssertionInput(t *testing.T) {
	adapter := &loggingProtocol{}
	vu := &VUser{
		Variables: map[string]string{},
		Proto:     adapter,
		Script:    &dsl.Script{Protocol: "http"},
	}
	resolved := vu.resolveAction(dsl.Action{
		Name: "unresolved assertion",
		Assertions: []dsl.Assertion{{
			Type:  "body_contains",
			Value: "{{param.expected_body}}",
		}},
	})
	if got := resolved.Assertions[0].Value; got != "{{param.expected_body}}" {
		t.Fatalf("unresolved assertion = %q, want retained token", got)
	}
	if got := strings.Join(vu.unresolvedParameterRefs, ","); !strings.Contains(got, "param.expected_body") {
		t.Fatalf("unresolved parameter refs = %#v, want assertion reference", vu.unresolvedParameterRefs)
	}
	if _, err := vu.safeProtoExecute(context.Background(), resolved); err == nil ||
		!strings.Contains(err.Error(), "unresolved {{param.*}}") {
		t.Fatalf("safeProtoExecute() error = %v, want pre-transport assertion binding rejection", err)
	}
	if adapter.executeCalls != 0 {
		t.Fatalf("protocol execute calls = %d, want rejection before transport", adapter.executeCalls)
	}
}

func TestVUser_HandleSnapshot(t *testing.T) {
	store := NewSnapshotStore(10)
	vu := &VUser{SnapshotStore: store}
	errCfg := dsl.ErrorHandlingConfig{SnapshotOnError: true}

	vu.handleSnapshot(&protocol.Result{
		Success:  false,
		Snapshot: &protocol.ErrorSnapshot{RequestURL: "http://fail.com"},
	}, errCfg)

	if len(store.Snapshots()) != 1 {
		t.Errorf("expected 1 snapshot, got %d", len(store.Snapshots()))
	}

	// Success should not add snapshot
	vu.handleSnapshot(&protocol.Result{
		Success:  true,
		Snapshot: &protocol.ErrorSnapshot{RequestURL: "http://ok.com"},
	}, errCfg)

	if len(store.Snapshots()) != 1 {
		t.Errorf("success should not add snapshot, got %d", len(store.Snapshots()))
	}
}

func TestVUser_Run_LogsExtendedOptions(t *testing.T) {
	t.Setenv("APEXLOAD_LOG_HOST", "api.example.test")
	t.Setenv("APEXLOAD_LOG_VALUE", "abc123")
	t.Setenv(EnvScriptEnvAllowlist, "APEXLOAD_LOG_HOST,APEXLOAD_LOG_VALUE")

	protoStub := &loggingProtocol{result: &protocol.Result{
		Success:          true,
		StatusCode:       200,
		Duration:         25 * time.Millisecond,
		BytesReceived:    128,
		ResponseBody:     `{"status":"ok"}`,
		DependencyTarget: "api.example.test",
		CorrelationID:    "corr-123",
		DNSTime:          2 * time.Millisecond,
		ConnectTime:      3 * time.Millisecond,
		TLSTime:          4 * time.Millisecond,
		TTFB:             5 * time.Millisecond,
	}}

	var logBuf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)

	vu := &VUser{
		ID:    1,
		Group: "Checkout",
		Proto: protoStub,
		Script: &dsl.Script{
			Protocol: "http",
			Config: dsl.ScriptConfig{
				RunLogic: dsl.RunLogicConfig{Iterations: 1},
				Log: dsl.LogConfig{
					Level:           "extended",
					ExtendedOptions: []string{"parameter_substitution", "data_returned", "advanced_trace"},
				},
			},
			Actions: []dsl.Action{{
				Name:   "Checkout",
				Type:   "request",
				Method: "POST",
				Target: "https://{{env.APEXLOAD_LOG_HOST}}/checkout",
				Body:   "token={{env.APEXLOAD_LOG_VALUE}}",
			}},
		},
		Metrics: NewMetricsCollector(16),
	}

	vu.Run(context.Background())

	logOutput := logBuf.String()
	for _, expected := range []string{
		"msg=\"action ok\"",
		"target=https://api.example.test/checkout",
		"request_body=\"token=[REDACTED]\"",
		"response_body=\"{\\\"status\\\":\\\"ok\\\"}\"",
		"dependency_target=api.example.test",
		"correlation_id=corr-123",
		"dns_ms=2",
		"ttfb_ms=5",
	} {
		if !strings.Contains(logOutput, expected) {
			t.Fatalf("log output missing %q: %s", expected, logOutput)
		}
	}
	if strings.Contains(logOutput, "abc123") {
		t.Fatalf("extended parameter-substitution log leaked a sensitive value: %s", logOutput)
	}
}

func TestResolveActionRequestsOnlyConfiguredSuccessfulResponseBodies(t *testing.T) {
	vu := &VUser{Script: &dsl.Script{}}
	if got := vu.resolveAction(dsl.Action{Name: "default"}); !got.DiscardResponseBodyOnSuccess {
		t.Fatal("default logging retained successful response bodies")
	}
	vu.Script.Config.Log = dsl.LogConfig{Level: "extended", ExtendedOptions: []string{"data_returned"}}
	if got := vu.resolveAction(dsl.Action{Name: "extended"}); got.DiscardResponseBodyOnSuccess {
		t.Fatal("data_returned logging discarded successful response bodies")
	}
}

func TestResolveActionRequestsSuccessfulMessageDetailDeterministically(t *testing.T) {
	vu := &VUser{ID: 3, Group: "checkout", Script: &dsl.Script{Config: dsl.ScriptConfig{
		Results: dsl.ResultsConfig{MessageDetail: &dsl.MessageDetailConfig{
			Mode: "sampled", SampleRate: 1, MaxItems: 4, MaxItemBytes: 2048, MaxTotalBytes: 4096,
		}},
	}}}
	resolved := vu.resolveAction(dsl.Action{Name: "submit", Type: "GET", Target: "/"})
	if !resolved.CaptureMessageDetail || resolved.DiscardResponseBodyOnSuccess {
		t.Fatalf("capture=%v discard=%v, want capture without discard", resolved.CaptureMessageDetail, resolved.DiscardResponseBodyOnSuccess)
	}
	if resolved.MessageDetailMaxItems != 4 || resolved.MessageDetailMaxItemBytes != 2048 || resolved.MessageDetailMaxTotalBytes != 4096 {
		t.Fatalf("resolved detail budget = %#v", resolved)
	}

	vu.Script.Config.Results.MessageDetail.Mode = "errors"
	resolved = vu.resolveAction(dsl.Action{Name: "submit", Type: "GET", Target: "/"})
	if resolved.CaptureMessageDetail || !resolved.DiscardResponseBodyOnSuccess {
		t.Fatalf("errors-mode capture=%v discard=%v, want legacy success-body behavior", resolved.CaptureMessageDetail, resolved.DiscardResponseBodyOnSuccess)
	}
}

func TestVUser_Run_HTTPSequencePropagatesExtractionsAndRecordsOneTransaction(t *testing.T) {
	protoStub := &sequenceProtocol{}
	sink := &sequenceCaptureSink{}
	vu := &VUser{
		ID:         1,
		Group:      "Checkout",
		Proto:      protoStub,
		Variables:  map[string]string{},
		Metrics:    NewMetricsCollector(16),
		ResultSink: sink,
		Script: &dsl.Script{
			Name:     "Imported",
			Protocol: "http",
			Config: dsl.ScriptConfig{
				RunLogic: dsl.RunLogicConfig{Iterations: 1},
			},
			Actions: []dsl.Action{{
				Name: "CheckoutFlow",
				Type: "http_sequence",
				Params: map[string]any{
					"requests": []map[string]any{{
						"name":   "Login",
						"type":   "POST",
						"method": "POST",
						"target": "/login",
						"body":   "user=ada",
					}, {
						"name":   "Checkout",
						"type":   "POST",
						"method": "POST",
						"target": "/checkout",
						"body":   "token={{var.token}}",
					}},
				},
				Metadata: map[string]any{"source_transaction": "CheckoutFlow"},
			}},
		},
	}

	vu.Run(context.Background())

	if len(protoStub.actions) != 2 {
		t.Fatalf("executed children = %d, want 2", len(protoStub.actions))
	}
	if protoStub.actions[0].Name != "Login" || protoStub.actions[1].Name != "Checkout" {
		t.Fatalf("child order = [%s %s], want Login then Checkout", protoStub.actions[0].Name, protoStub.actions[1].Name)
	}
	if got := string(protoStub.actions[1].Body); got != "token=abc123" {
		t.Fatalf("checkout body = %q, want token from first child extraction", got)
	}
	if len(sink.results) != 2 {
		t.Fatalf("recorded child results = %d, want 2", len(sink.results))
	}
	if len(sink.transactions) != 1 {
		t.Fatalf("recorded transactions = %d, want one logical transaction", len(sink.transactions))
	}
	if tx := sink.transactions[0]; tx.ActionName != "CheckoutFlow" || !tx.Success || tx.SourceTransaction != "CheckoutFlow" {
		t.Fatalf("transaction = %#v, want successful CheckoutFlow source transaction", tx)
	}
}

func TestVUser_Run_HTTPSequenceContinueOnErrorRunsRemainingChildren(t *testing.T) {
	protoStub := &sequenceProtocol{failFirst: true}
	sink := &sequenceCaptureSink{}
	vu := &VUser{
		ID:         1,
		Group:      "Checkout",
		Proto:      protoStub,
		Variables:  map[string]string{},
		Metrics:    NewMetricsCollector(16),
		ResultSink: sink,
		Script: &dsl.Script{
			Name:     "Imported",
			Protocol: "http",
			Config: dsl.ScriptConfig{
				RunLogic:      dsl.RunLogicConfig{Iterations: 1},
				ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(true)},
			},
			Actions: []dsl.Action{{
				Name: "CheckoutFlow",
				Type: "http_sequence",
				Params: map[string]any{
					"requests": []map[string]any{{
						"name":   "Login",
						"type":   "POST",
						"method": "POST",
						"target": "/login",
					}, {
						"name":   "Checkout",
						"type":   "POST",
						"method": "POST",
						"target": "/checkout",
					}},
				},
				Metadata: map[string]any{"source_transaction": "CheckoutFlow"},
			}},
		},
	}

	vu.Run(context.Background())

	if len(protoStub.actions) != 2 {
		t.Fatalf("executed children = %d, want both children despite first failure", len(protoStub.actions))
	}
	if len(sink.transactions) != 1 {
		t.Fatalf("recorded transactions = %d, want one logical transaction", len(sink.transactions))
	}
	if tx := sink.transactions[0]; tx.Success || tx.ErrorMessage == "" {
		t.Fatalf("transaction = %#v, want failed logical transaction with an error", tx)
	}
}

func TestVUser_Run_HTTPSequenceScrubsAdapterErrorBeforeTransactionSinks(t *testing.T) {
	const secret = "http-sequence-runtime-secret"
	var logBuffer bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuffer, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(previousLogger)

	protoStub := &sequenceProtocol{executeError: secret}
	sink := &sequenceCaptureSink{}
	metrics := NewMetricsCollector(16)
	vu := &VUser{
		ID:              1,
		Group:           "Checkout",
		Proto:           protoStub,
		Variables:       map[string]string{},
		Metrics:         metrics,
		ResultSink:      sink,
		RedactionConfig: WithSecretValueRedaction(nil, []string{secret}),
		Script: &dsl.Script{
			Name:     "Imported",
			Protocol: "http",
			Config: dsl.ScriptConfig{
				RunLogic: dsl.RunLogicConfig{Iterations: 1},
				Log: dsl.LogConfig{
					Level:           "extended",
					ExtendedOptions: []string{"data_returned", "advanced_trace"},
				},
			},
			Actions: []dsl.Action{{
				Name: "CheckoutFlow",
				Type: "http_sequence",
				Params: map[string]any{
					"requests": []map[string]any{{
						"name":   "Login",
						"type":   "POST",
						"method": "POST",
						"target": "/login",
					}},
				},
				Metadata: map[string]any{"source_transaction": "CheckoutFlow"},
			}},
		},
	}

	vu.Run(context.Background())

	if len(sink.transactions) != 1 {
		t.Fatalf("recorded transactions = %d, want one logical transaction", len(sink.transactions))
	}
	if encoded := fmt.Sprintf("%#v", sink.transactions[0]); strings.Contains(encoded, secret) {
		t.Fatalf("transaction sink retained adapter secret: %s", encoded)
	}
	select {
	case metric := <-metrics.ch:
		if encoded := fmt.Sprintf("%#v", metric); strings.Contains(encoded, secret) {
			t.Fatalf("metrics sink retained adapter secret: %s", encoded)
		}
	default:
		t.Fatal("transaction metric was not recorded")
	}
	if strings.Contains(logBuffer.String(), secret) {
		t.Fatalf("extended http_sequence log retained adapter secret: %s", logBuffer.String())
	}
}

func TestVUser_Run_RedactsSecretValuesFromLogs(t *testing.T) {
	t.Setenv("APEXLOAD_SECRET_LOG_TOKEN", "super-secret")

	protoStub := &loggingProtocol{result: &protocol.Result{
		Success:       true,
		StatusCode:    200,
		Duration:      10 * time.Millisecond,
		BytesReceived: 64,
	}}

	var logBuf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)

	vu := &VUser{
		ID:    1,
		Group: "Checkout",
		Proto: protoStub,
		Script: &dsl.Script{
			Protocol: "http",
			Config: dsl.ScriptConfig{
				RunLogic: dsl.RunLogicConfig{Iterations: 1},
				Log:      dsl.LogConfig{Level: "extended", ExtendedOptions: []string{"parameter_substitution"}},
			},
			Actions: []dsl.Action{{
				Name:   "Checkout",
				Type:   "request",
				Method: "POST",
				Target: "https://example.test/login?token={{secret.log_token}}",
				Body:   "token={{secret.log_token}}",
			}},
		},
		Metrics: NewMetricsCollector(16),
	}

	vu.Run(context.Background())

	logOutput := logBuf.String()
	if strings.Contains(logOutput, "super-secret") {
		t.Fatalf("expected secret value to be redacted from logs, got %s", logOutput)
	}
	for _, expected := range []string{"target=\"https://example.test/login?token=[REDACTED]\"", "request_body=\"token=[REDACTED]\""} {
		if !strings.Contains(logOutput, expected) {
			t.Fatalf("log output missing %q: %s", expected, logOutput)
		}
	}
}

func TestVUser_Run_RedactsExtractedSecretValuesFromLogs(t *testing.T) {
	protoStub := &sequenceProtocol{}

	var logBuf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)

	vu := &VUser{
		ID:    1,
		Group: "Checkout",
		Proto: protoStub,
		Script: &dsl.Script{
			Protocol: "http",
			Config: dsl.ScriptConfig{
				RunLogic: dsl.RunLogicConfig{Iterations: 1},
				Log:      dsl.LogConfig{Level: "extended", ExtendedOptions: []string{"parameter_substitution"}},
			},
			Actions: []dsl.Action{
				{
					Name:   "Login",
					Type:   "request",
					Method: "POST",
					Target: "https://example.test/login",
				},
				{
					Name:   "Use token",
					Type:   "request",
					Method: "POST",
					Target: "https://example.test/use/{{var.token}}",
					Body:   "credential={{var.token}}",
				},
			},
		},
		Metrics: NewMetricsCollector(16),
	}

	vu.Run(context.Background())

	logOutput := logBuf.String()
	if strings.Contains(logOutput, "abc123") {
		t.Fatalf("expected extracted token to be redacted from logs, got %s", logOutput)
	}
	for _, expected := range []string{
		`target=https://example.test/use/[REDACTED]`,
		`request_body="credential=[REDACTED]"`,
	} {
		if !strings.Contains(logOutput, expected) {
			t.Fatalf("log output missing %q: %s", expected, logOutput)
		}
	}
}

func TestVUser_Run_ResetsBrowserProtocolPerIteration(t *testing.T) {
	newUserPerIteration := true
	protoStub := &iterationResetProtocol{}
	if err := protoStub.Init(context.Background(), nil); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}

	vu := &VUser{
		ID:    1,
		Proto: protoStub,
		Script: &dsl.Script{
			Protocol: "browser",
			Config: dsl.ScriptConfig{
				RunLogic: dsl.RunLogicConfig{Iterations: 2},
				Browser:  dsl.BrowserConfig{NewUserPerIter: &newUserPerIteration},
			},
			Actions: []dsl.Action{{Name: "Navigate", Type: "browser_navigate", Target: "/"}},
		},
		Metrics: NewMetricsCollector(16),
	}

	vu.Run(context.Background())

	if protoStub.executeCalls != 2 {
		t.Fatalf("executeCalls = %d, want 2", protoStub.executeCalls)
	}
	if protoStub.initCalls != 2 {
		t.Fatalf("initCalls = %d, want 2 after per-iteration reset", protoStub.initCalls)
	}
	if protoStub.teardownCalls != 1 {
		t.Fatalf("teardownCalls = %d, want 1 between iterations", protoStub.teardownCalls)
	}
}

func TestVUser_Run_ErrorMessageActionLogsWhenScriptLoggingDisabled(t *testing.T) {
	protoStub := &loggingProtocol{result: &protocol.Result{
		Success:       true,
		StatusCode:    200,
		Duration:      time.Millisecond,
		BytesReceived: 16,
	}}

	var logBuf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)

	vu := &VUser{
		ID:        7,
		Group:     "Login",
		Proto:     protoStub,
		Variables: map[string]string{"userid": "qa-user"},
		Script: &dsl.Script{
			Protocol: "http",
			Config: dsl.ScriptConfig{
				RunLogic: dsl.RunLogicConfig{Iterations: 1},
				Log:      dsl.LogConfig{Level: "disabled"},
			},
			Actions: []dsl.Action{{
				Name:  "LR_Error_Message_12",
				Type:  "error_message",
				Value: "Login failed for User: %s with status %d",
				Params: map[string]any{
					"args": []any{"{{var.userid}}", "500"},
				},
			}, {
				Name:   "Home",
				Type:   "request",
				Method: "GET",
				Target: "/home",
			}},
		},
		Metrics: NewMetricsCollector(16),
	}

	vu.Run(context.Background())

	if protoStub.executeCalls != 1 {
		t.Fatalf("executeCalls = %d, want only the HTTP request to reach protocol", protoStub.executeCalls)
	}
	logOutput := logBuf.String()
	for _, expected := range []string{
		"msg=\"source error message\"",
		"vuser=7",
		"group=Login",
		"action=LR_Error_Message_12",
		"message=\"Login failed for User: qa-user with status 500\"",
	} {
		if !strings.Contains(logOutput, expected) {
			t.Fatalf("log output missing %q: %s", expected, logOutput)
		}
	}
	if strings.Contains(logOutput, "msg=\"action ok\"") {
		t.Fatalf("disabled script logging emitted request logs: %s", logOutput)
	}
}

func TestRenderPrintfLikeMessageSupportsSourceFormatting(t *testing.T) {
	tests := []struct {
		name   string
		format string
		args   []any
		want   string
	}{
		{
			name:   "numeric flags precision and escaped percent",
			format: "HTML error %03d was triggered. Value #1 is %s (%.1f%%)",
			args:   []any{"7", "Param1", "12.34"},
			want:   "HTML error 007 was triggered. Value #1 is Param1 (12.3%)",
		},
		{
			name:   "c integer length modifiers",
			format: "total=%ld unsigned=%u",
			args:   []any{"42", "9"},
			want:   "total=42 unsigned=9",
		},
		{
			name:   "missing arguments keep specifier visible",
			format: "HTML error %d was triggered",
			args:   nil,
			want:   "HTML error %d was triggered",
		},
		{
			name:   "literal percent text does not consume arguments",
			format: "Progress was 100% complete for %s",
			args:   []any{"UserID"},
			want:   "Progress was 100% complete for UserID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := renderPrintfLikeMessage(tt.format, tt.args); got != tt.want {
				t.Fatalf("renderPrintfLikeMessage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestVUser_Run_SourceUtilityActionsStayInEngine(t *testing.T) {
	vu := &VUser{
		ID:        3,
		Group:     "Checkout",
		Variables: map[string]string{},
		Script: &dsl.Script{
			Protocol: "http",
			Config:   dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}},
			Actions: []dsl.Action{{
				Name: "SaveUser",
				Type: "source_parameter",
				Params: map[string]any{
					"function": "lr_save_string",
					"args":     []any{"ada", "UserID"},
				},
			}, {
				Name: "Greeting",
				Type: "source_parameter",
				Params: map[string]any{
					"function": "lr_param_sprintf",
					"args":     []any{"Greeting", "Hello %s", "{{var.userid}}"},
				},
			}, {
				Name: "Timer1",
				Type: "source_timer",
				Params: map[string]any{
					"function": "lr_start_timer",
					"args":     []any{"Timer1"},
				},
			}, {
				Name: "Timer1",
				Type: "source_timer",
				Params: map[string]any{
					"function": "lr_end_timer",
					"args":     []any{"Timer1"},
				},
			}, {
				Name: "Checkout",
				Type: "source_transaction",
				Params: map[string]any{
					"function": "lr_start_transaction",
					"args":     []any{"Checkout"},
				},
			}, {
				Name: "Checkout",
				Type: "source_transaction",
				Params: map[string]any{
					"function": "lr_end_transaction",
					"args":     []any{"Checkout", "LR_PASS"},
				},
			}, {
				Name:  "Status",
				Type:  "source_message",
				Value: "Working as %s",
				Params: map[string]any{
					"function":       "lr_vuser_status_message",
					"format":         "Working as %s",
					"args":           []any{"{{var.userid}}"},
					"status_message": true,
				},
			}, {
				Name: "helper",
				Type: "custom_call",
				Params: map[string]any{
					"function":     "helper",
					"support_mode": "custom_review",
					"args":         []any{"42"},
				},
			}},
		},
		Metrics: NewMetricsCollector(16),
	}

	vu.Run(context.Background())

	if got := vu.Variables["userid"]; got != "ada" {
		t.Fatalf("userid = %q, want ada", got)
	}
	if got := vu.Variables["greeting"]; got != "Hello ada" {
		t.Fatalf("greeting = %q, want Hello ada", got)
	}
	if got := vu.Variables["vuser_status"]; got != "Working as ada" {
		t.Fatalf("vuser_status = %q, want rendered status", got)
	}
	if got := vu.Variables[sourceTimerDurationKey("Timer1")]; got == "" {
		t.Fatalf("timer duration variable missing: %#v", vu.Variables)
	}
	if got := vu.Variables[sourceTransactionStatusKey("Checkout")]; got != "true" {
		t.Fatalf("transaction status = %q, want true", got)
	}
}

func TestVUser_HandleWebConvertParamAction(t *testing.T) {
	vu := &VUser{
		ID:        1,
		Variables: map[string]string{"auratoken": "a&amp;b c"},
	}
	action := dsl.Action{
		Name: "ConvertAuraToken",
		Type: "source_parameter",
		Params: map[string]any{
			"function":        "web_convert_param",
			"save_param":      "AuraToken_URL",
			"source_string":   "{{var.auratoken}}",
			"source_encoding": "HTML",
			"target_encoding": "URL",
		},
	}

	vu.handleSourceParameterAction("web_convert_param", action)

	if got := vu.Variables["auratoken_url"]; got != "a%26b+c" {
		t.Fatalf("auratoken_url = %q, want URL-encoded HTML-decoded value", got)
	}
}

type rendezvousTestProtocol struct {
	executeTimes chan time.Time
}

func (p *rendezvousTestProtocol) Name() string                               { return "rendezvous-test" }
func (p *rendezvousTestProtocol) Init(context.Context, map[string]any) error { return nil }
func (p *rendezvousTestProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	p.executeTimes <- time.Now()
	return &protocol.Result{ActionName: action.Name, Success: true, Duration: time.Millisecond}, nil
}
func (p *rendezvousTestProtocol) Teardown(context.Context) error    { return nil }
func (p *rendezvousTestProtocol) HealthCheck(context.Context) error { return nil }

func TestVUser_Run_RendezvousSynchronizesStaggeredVUsers(t *testing.T) {
	executeTimes := make(chan time.Time, 2)
	script := &dsl.Script{
		Protocol: "http",
		Config:   dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}},
		Actions: []dsl.Action{{
			Name:   "sync-buyers",
			Type:   "rendezvous",
			Params: map[string]any{"count": 2, "timeout_ms": 500},
		}, {
			Name:   "checkout",
			Type:   "request",
			Target: "/checkout",
		}},
	}
	coordinator := NewRendezvousCoordinator()

	makeVU := func(id int) *VUser {
		return &VUser{
			ID:          id,
			TotalVUsers: 2,
			Proto:       &rendezvousTestProtocol{executeTimes: executeTimes},
			Script:      script,
			Metrics:     NewMetricsCollector(16),
			Rendezvous:  coordinator,
		}
	}

	vuOne := makeVU(0)
	vuTwo := makeVU(1)

	done := make(chan struct{}, 2)
	go func() {
		vuOne.Run(context.Background())
		done <- struct{}{}
	}()
	time.Sleep(150 * time.Millisecond)
	go func() {
		vuTwo.Run(context.Background())
		done <- struct{}{}
	}()

	first := <-executeTimes
	second := <-executeTimes
	<-done
	<-done

	delta := first.Sub(second)
	if delta < 0 {
		delta = -delta
	}
	if delta > 60*time.Millisecond {
		t.Fatalf("execute delta = %v, want rendezvous to release both VUsers together", delta)
	}
}

func TestScriptHasExplicitTransactionsRecognizesLegacyTypes(t *testing.T) {
	legacyTypeScript := &dsl.Script{
		Actions: []dsl.Action{{Name: "txn", Type: "loadrunner_transaction"}},
	}
	if !scriptHasExplicitTransactions(legacyTypeScript) {
		t.Error("expected legacy loadrunner_transaction action type to count as an explicit transaction")
	}

	legacyMetadataScript := &dsl.Script{
		Actions: []dsl.Action{{
			Name:     "seq",
			Type:     "http_sequence",
			Metadata: map[string]any{"loadrunner_transaction": "CheckoutFlow"},
		}},
	}
	if !scriptHasExplicitTransactions(legacyMetadataScript) {
		t.Error("expected legacy loadrunner_transaction metadata key to count as an explicit transaction")
	}

	javaVuserScript := &dsl.Script{
		Actions: []dsl.Action{{
			Name:     "Actions.action",
			Type:     "java_vuser_run",
			Metadata: map[string]any{"emits_business_transactions": true},
		}},
	}
	if !scriptHasExplicitTransactions(javaVuserScript) {
		t.Error("expected Java Vuser runner event metadata to count as an explicit transaction")
	}

	javaVuserWithoutTransactions := &dsl.Script{
		Actions: []dsl.Action{{
			Name: "Actions.action",
			Type: "java_vuser_run",
		}},
	}
	if scriptHasExplicitTransactions(javaVuserWithoutTransactions) {
		t.Error("Java Vuser actions without transaction event metadata must retain automatic iteration transactions")
	}
}

func TestSourceTransactionNameAcceptsLegacyKey(t *testing.T) {
	if got := sourceTransactionName(map[string]any{"source_transaction": "Checkout"}); got != "Checkout" {
		t.Errorf("source_transaction key = %q, want Checkout", got)
	}
	if got := sourceTransactionName(map[string]any{"loadrunner_transaction": "Checkout"}); got != "Checkout" {
		t.Errorf("loadrunner_transaction key = %q, want Checkout", got)
	}
	if got := sourceTransactionName(map[string]any{
		"source_transaction":     "New",
		"loadrunner_transaction": "Old",
	}); got != "New" {
		t.Errorf("canonical key should win, got %q, want New", got)
	}
}

func TestVUserInitStopsAfterPeerSatisfiesTransactionGoal(t *testing.T) {
	state := installGracefulBoundaryState(t)
	runLogic := dsl.RunLogicConfig{
		Iterations:       1,
		AutoTransactions: "per_action",
		MaxTransactions:  1,
	}
	softCtx, softCancel := context.WithCancel(context.Background())
	defer softCancel()
	limiter := NewExecutionGoalLimiter(runLogic, softCancel)

	target := &VUser{
		Proto:             &gracefulBoundaryTestProtocol{state: state},
		Metrics:           NewMetricsCollector(16),
		ExecutionGoals:    limiter,
		HardStopCtx:       context.Background(),
		ActionBoundaryCtx: context.Background(),
		Script: &dsl.Script{
			Protocol: "graceful-boundary-test",
			Config:   dsl.ScriptConfig{RunLogic: runLogic},
			Init: []dsl.Action{
				{Name: "first", Type: "request", Target: "/first"},
				{Name: "second", Type: "request", Target: "/second"},
			},
			Actions: []dsl.Action{{Name: "target-main", Type: "request", Target: "/main"}},
		},
	}
	targetDone := make(chan struct{})
	go func() {
		target.Run(softCtx)
		close(targetDone)
	}()
	select {
	case <-state.firstStarted:
	case <-time.After(time.Second):
		t.Fatal("target VUser did not start its first init action")
	}

	peerProto := &iterationResetProtocol{}
	peer := &VUser{
		Proto:          peerProto,
		Metrics:        NewMetricsCollector(16),
		ExecutionGoals: limiter,
		HardStopCtx:    context.Background(),
		Script: &dsl.Script{
			Protocol: "browser",
			Config:   dsl.ScriptConfig{RunLogic: runLogic},
			Actions:  []dsl.Action{{Name: "peer-main", Type: "request", Target: "/peer"}},
		},
	}
	peerDone := make(chan struct{})
	go func() {
		peer.Run(softCtx)
		close(peerDone)
	}()
	select {
	case <-peerDone:
	case <-time.After(time.Second):
		t.Fatal("peer VUser did not satisfy the transaction goal")
	}
	select {
	case <-softCtx.Done():
	default:
		t.Fatal("peer transaction did not cancel the shared soft context")
	}

	close(state.releaseFirst)
	select {
	case <-targetDone:
	case <-time.After(time.Second):
		t.Fatal("target VUser did not stop after its in-flight init action completed")
	}
	select {
	case <-state.firstReturned:
	default:
		t.Fatal("soft goal cancellation interrupted the in-flight init action")
	}
	if got := boundaryActionNames(state); len(got) != 1 || got[0] != "first" {
		t.Fatalf("target actions = %v, want only the in-flight first init action", got)
	}
	if peerProto.executeCalls != 1 {
		t.Fatalf("peer executions = %d, want one goal-owning action", peerProto.executeCalls)
	}
}

func TestLifecycleHTTPSequencesRespectTransactionGoal(t *testing.T) {
	sequence := func(name string) dsl.Action {
		return dsl.Action{
			Name: name,
			Type: "http_sequence",
			Params: map[string]any{"requests": []dsl.Action{{
				Name: name + " child", Type: "GET", Method: "GET", Target: "/",
			}}},
		}
	}
	for _, phase := range []string{"init", "teardown"} {
		t.Run(phase, func(t *testing.T) {
			runLogic := dsl.RunLogicConfig{
				Iterations:       1,
				AutoTransactions: "per_action",
				MaxTransactions:  1,
			}
			softCtx, softCancel := context.WithCancel(context.Background())
			defer softCancel()
			limiter := NewExecutionGoalLimiter(runLogic, softCancel)
			protoStub := &iterationResetProtocol{}
			sink := &sequenceCaptureSink{}
			script := &dsl.Script{
				Protocol: "browser",
				Config:   dsl.ScriptConfig{RunLogic: runLogic},
			}
			lifecycle := []dsl.Action{sequence("first sequence"), sequence("second sequence")}
			if phase == "init" {
				script.Init = lifecycle
			} else {
				script.Teardown = lifecycle
			}
			vu := &VUser{
				Proto:          protoStub,
				Script:         script,
				Metrics:        NewMetricsCollector(16),
				ResultSink:     sink,
				ExecutionGoals: limiter,
				HardStopCtx:    context.Background(),
			}

			vu.Run(softCtx)

			if protoStub.executeCalls != 1 {
				t.Fatalf("lifecycle child executions = %d, want only the claimed first sequence", protoStub.executeCalls)
			}
			if len(sink.transactions) != 1 || sink.transactions[0].ActionName != "first sequence" {
				t.Fatalf("lifecycle transactions = %#v, want only first sequence", sink.transactions)
			}
			if limiter.TryStartTransaction() {
				t.Fatal("lifecycle sequence did not consume MaxTransactions capacity")
			}
		})
	}
}

func TestKnownStreamEmittersReserveBeforeProtocolExecution(t *testing.T) {
	for _, phase := range []string{"init", "actions"} {
		t.Run(phase, func(t *testing.T) {
			state := installTeardownGoalDenialState(t)
			runLogic := dsl.RunLogicConfig{
				Iterations:      1,
				MaxTransactions: 1,
			}
			softCtx, softCancel := context.WithCancel(context.Background())
			defer softCancel()
			limiter := NewExecutionGoalLimiter(runLogic, softCancel)
			metrics := NewMetricsCollector(16)
			emitter := dsl.Action{
				Name:     "stream transaction",
				Type:     "java_vuser_run",
				Metadata: map[string]any{"emits_business_transactions": true},
			}
			script := &dsl.Script{
				Protocol: "teardown-goal-denial-test",
				Config:   dsl.ScriptConfig{RunLogic: runLogic},
			}
			if phase == "init" {
				script.Init = []dsl.Action{emitter}
			} else {
				script.Actions = []dsl.Action{emitter}
			}
			var wait sync.WaitGroup
			wait.Add(2)
			for index := 0; index < 2; index++ {
				vu := &VUser{
					ID:                index,
					Proto:             &teardownGoalDenialProtocol{state: state},
					Metrics:           metrics,
					ExecutionGoals:    limiter,
					HardStopCtx:       context.Background(),
					ActionBoundaryCtx: context.Background(),
					Script:            script,
				}
				go func() {
					defer wait.Done()
					vu.Run(softCtx)
				}()
			}
			done := make(chan struct{})
			go func() {
				wait.Wait()
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("peer emitter remained blocked after exact goal completion")
			}
			state.mu.Lock()
			executions := append([]string(nil), state.actions...)
			state.mu.Unlock()
			if len(executions) != 1 || executions[0] != "stream transaction" {
				t.Fatalf("protocol executions = %v, want exactly one pre-reserved emitter", executions)
			}
			if got := metrics.TotalTransactions(); got != 1 {
				t.Fatalf("transactions = %d, want one emitted business transaction", got)
			}
		})
	}
}

func TestTeardownTransactionGoalDenialContinuesCleanup(t *testing.T) {
	cleanup := dsl.Action{Name: "unrelated cleanup", Type: "request", Target: "/cleanup"}
	tests := []struct {
		name           string
		init           []dsl.Action
		denied         dsl.Action
		wantExecutions []string
	}{
		{
			name:           "source end",
			denied:         sourceTransactionAction("lr_end_transaction", "LR_PASS"),
			wantExecutions: []string{"main", "unrelated cleanup"},
		},
		{
			name: "http sequence",
			denied: dsl.Action{
				Name: "denied sequence",
				Type: "http_sequence",
				Params: map[string]any{"requests": []dsl.Action{{
					Name: "sequence child", Type: "GET", Method: "GET", Target: "/denied",
				}}},
			},
			wantExecutions: []string{"main", "unrelated cleanup"},
		},
		{
			name:           "streamed child transaction",
			denied:         dsl.Action{Name: "stream transaction", Type: "request", Target: "/stream"},
			wantExecutions: []string{"main", "stream transaction", "unrelated cleanup"},
		},
		{
			name:           "final result transaction",
			denied:         dsl.Action{Name: "final transaction", Type: "request", Target: "/final"},
			wantExecutions: []string{"main", "final transaction", "unrelated cleanup"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := installTeardownGoalDenialState(t)
			runLogic := dsl.RunLogicConfig{
				Iterations:       1,
				AutoTransactions: "per_action",
				MaxTransactions:  1,
			}
			script := &dsl.Script{
				Name:     "teardown-denial-" + tc.name,
				Protocol: "teardown-goal-denial-test",
				Config:   dsl.ScriptConfig{RunLogic: runLogic},
				Init:     tc.init,
				Actions:  []dsl.Action{{Name: "main", Type: "request", Target: "/main"}},
				Teardown: []dsl.Action{tc.denied, cleanup},
			}
			engine, err := New(Config{Script: script, VUsers: 1, Duration: 2 * time.Second})
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			result, err := engine.Run(context.Background())
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if result.TotalTransactions != 1 {
				t.Fatalf("total transactions = %d, want only the main action transaction", result.TotalTransactions)
			}

			state.mu.Lock()
			gotExecutions := append([]string(nil), state.actions...)
			initCalls := state.initCalls
			teardownCalls := state.teardownCalls
			state.mu.Unlock()
			if len(gotExecutions) != len(tc.wantExecutions) {
				t.Fatalf("protocol executions = %v, want %v", gotExecutions, tc.wantExecutions)
			}
			for index := range tc.wantExecutions {
				if gotExecutions[index] != tc.wantExecutions[index] {
					t.Fatalf("protocol executions = %v, want %v", gotExecutions, tc.wantExecutions)
				}
			}
			if initCalls != 1 || teardownCalls != 1 {
				t.Fatalf("protocol init/teardown calls = %d/%d, want 1/1", initCalls, teardownCalls)
			}
		})
	}
}

func TestTeardownDeniedSourceStartSkipsEnclosedWorkThenResumesCleanup(t *testing.T) {
	state := installTeardownGoalDenialState(t)
	runLogic := dsl.RunLogicConfig{
		Iterations:       1,
		AutoTransactions: "per_action",
		MaxTransactions:  1,
	}
	script := &dsl.Script{
		Name:     "teardown-denied-source-scope",
		Protocol: "teardown-goal-denial-test",
		Config:   dsl.ScriptConfig{RunLogic: runLogic},
		Actions:  []dsl.Action{{Name: "main", Type: "request", Target: "/main"}},
		Teardown: []dsl.Action{
			sourceTransactionAction("lr_start_transaction", ""),
			{
				Name: "nested denied scope", Type: "control_if",
				Control: &dsl.ControlFlow{
					Condition: &dsl.Condition{Expr: "true"},
					Steps: []dsl.Action{
						{Name: "must not execute", Type: "request", Target: "/unreported"},
						sourceTransactionAction("lr_end_transaction", "LR_PASS"),
					},
				},
			},
			{Name: "unrelated cleanup", Type: "request", Target: "/cleanup"},
		},
	}
	engine, err := New(Config{Script: script, VUsers: 1, Duration: 2 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.TotalTransactions != 1 {
		t.Fatalf("total transactions = %d, want only the main action", result.TotalTransactions)
	}
	state.mu.Lock()
	got := append([]string(nil), state.actions...)
	state.mu.Unlock()
	want := []string{"main", "unrelated cleanup"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("protocol executions = %v, want %v", got, want)
	}
}

func TestCanceledVUserBeforeFirstUserActionDoesNotRunUserTeardown(t *testing.T) {
	state := installTeardownGoalDenialState(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	vu := &VUser{
		Proto:   &teardownGoalDenialProtocol{state: state},
		Metrics: NewMetricsCollector(16),
		Script: &dsl.Script{
			Protocol: "teardown-goal-denial-test",
			Actions:  []dsl.Action{{Name: "main", Type: "request", Target: "/main"}},
			Teardown: []dsl.Action{{Name: "user teardown", Type: "request", Target: "/cleanup"}},
		},
	}
	vu.Run(ctx)
	state.mu.Lock()
	defer state.mu.Unlock()
	if len(state.actions) != 0 {
		t.Fatalf("protocol executions = %v, want no user actions after pre-start cancellation", state.actions)
	}
}

func TestSourceTransactionSpanningLifecycleClaimsGoalAtEnd(t *testing.T) {
	runLogic := dsl.RunLogicConfig{
		Iterations:       1,
		AutoTransactions: "none",
		MaxTransactions:  1,
	}
	softCtx, softCancel := context.WithCancel(context.Background())
	defer softCancel()
	limiter := NewExecutionGoalLimiter(runLogic, softCancel)
	protoStub := &iterationResetProtocol{}
	sink := &sequenceCaptureSink{}
	vu := &VUser{
		Proto:             protoStub,
		Metrics:           NewMetricsCollector(16),
		ResultSink:        sink,
		ExecutionGoals:    limiter,
		HardStopCtx:       context.Background(),
		ActionBoundaryCtx: context.Background(),
		Script: &dsl.Script{
			Name:     "lifecycle-source-transaction",
			Protocol: "browser",
			Config:   dsl.ScriptConfig{RunLogic: runLogic},
			Init:     []dsl.Action{sourceTransactionAction("lr_start_transaction", "")},
			Actions: []dsl.Action{{
				Name:     "owned sequence",
				Type:     "http_sequence",
				Metadata: map[string]any{"source_transaction": "ACME_ISBA"},
				Params: map[string]any{"requests": []dsl.Action{{
					Name: "owned child", Type: "GET", Method: "GET", Target: "/",
				}}},
			}},
			Teardown: []dsl.Action{sourceTransactionAction("lr_end_transaction", "LR_PASS")},
		},
	}

	vu.Run(softCtx)

	if protoStub.executeCalls != 1 {
		t.Fatalf("owned sequence child executions = %d, want 1", protoStub.executeCalls)
	}
	if len(sink.transactions) != 1 || sink.transactions[0].ActionName != "ACME_ISBA" {
		t.Fatalf("transactions = %#v, want one lifecycle-spanning ACME_ISBA transaction", sink.transactions)
	}
	if limiter.TryStartTransaction() {
		t.Fatal("lifecycle source transaction end did not consume MaxTransactions capacity")
	}
}

func TestLifecycleSourceReservationAdmitsOnlyOneVUserAtExactGoal(t *testing.T) {
	state := installTeardownGoalDenialState(t)
	runLogic := dsl.RunLogicConfig{
		Iterations:      1,
		MaxTransactions: 1,
	}
	softCtx, softCancel := context.WithCancel(context.Background())
	defer softCancel()
	limiter := NewExecutionGoalLimiter(runLogic, softCancel)
	metrics := NewMetricsCollector(16)
	script := &dsl.Script{
		Name:     "lifecycle-source-owner",
		Protocol: "teardown-goal-denial-test",
		Config:   dsl.ScriptConfig{RunLogic: runLogic},
		Init:     []dsl.Action{sourceTransactionAction("lr_start_transaction", "")},
		Actions:  []dsl.Action{{Name: "owned main", Type: "request", Target: "/main"}},
		Teardown: []dsl.Action{sourceTransactionAction("lr_end_transaction", "LR_PASS")},
	}
	var wait sync.WaitGroup
	wait.Add(2)
	for index := 0; index < 2; index++ {
		vu := &VUser{
			ID:                index,
			Proto:             &teardownGoalDenialProtocol{state: state},
			Metrics:           metrics,
			ExecutionGoals:    limiter,
			HardStopCtx:       context.Background(),
			ActionBoundaryCtx: context.Background(),
			Script:            script,
		}
		go func() {
			defer wait.Done()
			vu.Run(softCtx)
		}()
	}
	done := make(chan struct{})
	go func() {
		wait.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("peer VUser remained blocked on the pending lifecycle source reservation")
	}
	state.mu.Lock()
	got := append([]string(nil), state.actions...)
	state.mu.Unlock()
	if len(got) != 1 || got[0] != "owned main" {
		t.Fatalf("protocol executions = %v, want only the admitted source owner", got)
	}
	if got := metrics.TotalTransactions(); got != 1 {
		t.Fatalf("transactions = %d, want one completed source transaction", got)
	}
}

func TestVUserFinalizesUnclosedAndRestartedSourceTransactionsAsFailed(t *testing.T) {
	tests := []struct {
		name    string
		actions []dsl.Action
		want    int
	}{
		{
			name:    "unclosed",
			actions: []dsl.Action{sourceTransactionAction("lr_start_transaction", "")},
			want:    1,
		},
		{
			// A duplicate start is rejected and must not synthesize a second
			// business transaction. The authoritative original remains open
			// and is finalized once as failed when execution stops.
			name: "same name restart rejected",
			actions: []dsl.Action{
				sourceTransactionAction("lr_start_transaction", ""),
				sourceTransactionAction("lr_start_transaction", ""),
			},
			want: 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sink := &sequenceCaptureSink{}
			vu := &VUser{
				Proto:      &iterationResetProtocol{},
				Metrics:    NewMetricsCollector(16),
				ResultSink: sink,
				Script: &dsl.Script{
					Protocol: "browser",
					Config: dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{
						Iterations:       1,
						AutoTransactions: "none",
					}},
					Actions: tc.actions,
				},
			}
			vu.Run(context.Background())
			if len(sink.transactions) != tc.want {
				t.Fatalf("transactions = %#v, want %d failed incomplete transactions", sink.transactions, tc.want)
			}
			for _, transaction := range sink.transactions {
				if transaction.Success || transaction.ErrorMessage == "" {
					t.Fatalf("incomplete source transaction = %#v, want visible failed evidence", transaction)
				}
			}
		})
	}
}

func TestInterruptedHTTPSequenceRecordsFailedClaimedAggregate(t *testing.T) {
	for _, phase := range []string{"init", "actions", "teardown"} {
		t.Run(phase, func(t *testing.T) {
			if phase == "teardown" {
				// Teardown intentionally ignores the load run's active-time pause
				// controller. Interrupt this phase through its own bounded cleanup
				// context instead.
				t.Setenv("APEXLOAD_TEARDOWN_TIMEOUT", "20ms")
			}
			sink := &sequenceCaptureSink{}
			sequence := dsl.Action{
				Name: "interrupted sequence", Type: "http_sequence",
				Params: map[string]any{"requests": []dsl.Action{{
					Name: "child", Type: "GET", Method: "GET", Target: "/",
					ThinkTimeAfter: true,
					ThinkTime:      dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 100},
				}}},
			}
			script := &dsl.Script{
				Protocol: "http",
				Config: dsl.ScriptConfig{
					RunLogic: dsl.RunLogicConfig{Iterations: 1},
				},
			}
			switch phase {
			case "init":
				script.Init = []dsl.Action{sequence}
			case "actions":
				script.Actions = []dsl.Action{sequence}
			case "teardown":
				script.Actions = []dsl.Action{{Name: "main", Type: "GET", Method: "GET", Target: "/main"}}
				script.Teardown = []dsl.Action{sequence}
			}
			vu := &VUser{
				Proto:        &sequenceProtocol{},
				Metrics:      NewMetricsCollector(16),
				ResultSink:   sink,
				PauseControl: &partialActiveThinkController{},
				Script:       script,
			}
			vu.Run(context.Background())
			if len(sink.transactions) != 1 {
				t.Fatalf("transactions = %#v, want one claimed sequence aggregate", sink.transactions)
			}
			if sink.transactions[0].Success {
				t.Fatalf("interrupted aggregate = %#v, want failed", sink.transactions[0])
			}
		})
	}
}

func TestCrossPhaseSourceTransactionSuppressesFallbackIterationTransaction(t *testing.T) {
	script := &dsl.Script{
		Name:     "cross-phase-source-transaction",
		Protocol: "browser",
		Config: dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{
			Iterations:       1,
			AutoTransactions: "none",
		}},
		Init:     []dsl.Action{sourceTransactionAction("lr_start_transaction", "")},
		Actions:  []dsl.Action{{Name: "ordinary main request", Type: "request", Target: "/"}},
		Teardown: []dsl.Action{sourceTransactionAction("lr_end_transaction", "LR_PASS")},
	}
	if !scriptHasExplicitTransactions(script) {
		t.Fatal("cross-phase lifecycle transaction was not classified as explicit")
	}
	protoStub := &iterationResetProtocol{}
	sink := &sequenceCaptureSink{}
	vu := &VUser{
		Proto:      protoStub,
		Script:     script,
		Metrics:    NewMetricsCollector(16),
		ResultSink: sink,
	}

	vu.Run(context.Background())

	if protoStub.executeCalls != 1 {
		t.Fatalf("ordinary main executions = %d, want 1", protoStub.executeCalls)
	}
	if len(sink.transactions) != 1 || sink.transactions[0].ActionName != "ACME_ISBA" {
		t.Fatalf("transactions = %#v, want only the cross-phase ACME_ISBA transaction", sink.transactions)
	}
}

func TestLifecycleOnlySequenceSuppressesFallbackIterationTransaction(t *testing.T) {
	script := &dsl.Script{
		Name:     "lifecycle-only-sequence",
		Protocol: "browser",
		Config: dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{
			Iterations:       1,
			AutoTransactions: "none",
		}},
		Init: []dsl.Action{{
			Name: "init sequence",
			Type: "http_sequence",
			Params: map[string]any{"requests": []dsl.Action{{
				Name: "init child", Type: "GET", Method: "GET", Target: "/init",
			}}},
		}},
		Actions: []dsl.Action{{Name: "ordinary main request", Type: "request", Target: "/main"}},
	}
	if !scriptHasExplicitTransactions(script) {
		t.Fatal("lifecycle-only sequence was not classified as explicit")
	}
	protoStub := &iterationResetProtocol{}
	sink := &sequenceCaptureSink{}
	vu := &VUser{
		Proto:      protoStub,
		Script:     script,
		Metrics:    NewMetricsCollector(16),
		ResultSink: sink,
	}

	vu.Run(context.Background())

	if protoStub.executeCalls != 2 {
		t.Fatalf("protocol executions = %d, want init child plus ordinary main request", protoStub.executeCalls)
	}
	if len(sink.transactions) != 1 || sink.transactions[0].ActionName != "init sequence" {
		t.Fatalf("transactions = %#v, want only the lifecycle sequence aggregate", sink.transactions)
	}
}

func TestMetadataLessHTTPSequenceIsOnlyDefaultTransactionAtGoal(t *testing.T) {
	runLogic := dsl.RunLogicConfig{Iterations: 1, MaxTransactions: 1}
	softCtx, softCancel := context.WithCancel(context.Background())
	defer softCancel()
	limiter := NewExecutionGoalLimiter(runLogic, softCancel)
	protoStub := &iterationResetProtocol{}
	sink := &sequenceCaptureSink{}
	script := &dsl.Script{
		Name:     "metadata-less-sequence",
		Protocol: "browser",
		Config:   dsl.ScriptConfig{RunLogic: runLogic},
		Actions: []dsl.Action{{
			Name: "sequence",
			Type: "http_sequence",
			Params: map[string]any{"requests": []dsl.Action{{
				Name: "child", Type: "GET", Method: "GET", Target: "/",
			}}},
		}},
	}
	if !scriptHasExplicitTransactions(script) {
		t.Fatal("metadata-less http_sequence was not classified as an explicit transaction")
	}
	vu := &VUser{
		Proto:          protoStub,
		Script:         script,
		Metrics:        NewMetricsCollector(16),
		ResultSink:     sink,
		ExecutionGoals: limiter,
		HardStopCtx:    context.Background(),
	}

	vu.Run(softCtx)

	if protoStub.executeCalls != 1 {
		t.Fatalf("sequence child executions = %d, want 1", protoStub.executeCalls)
	}
	if len(sink.transactions) != 1 || sink.transactions[0].ActionName != "sequence" {
		t.Fatalf("transactions = %#v, want the sequence aggregate only", sink.transactions)
	}
}

func TestActionScopedTransactionGoalRestoresSoftBoundary(t *testing.T) {
	t.Run("http sequence", func(t *testing.T) {
		runLogic := dsl.RunLogicConfig{Iterations: 1, MaxTransactions: 1, AutoTransactions: "none"}
		softCtx, softCancel := context.WithCancel(context.Background())
		defer softCancel()
		protoStub := &iterationResetProtocol{}
		vu := &VUser{
			Proto:          protoStub,
			Metrics:        NewMetricsCollector(16),
			ExecutionGoals: NewExecutionGoalLimiter(runLogic, softCancel),
			HardStopCtx:    context.Background(),
			Script: &dsl.Script{
				Name:     "sequence-goal-boundary",
				Protocol: "browser",
				Config:   dsl.ScriptConfig{RunLogic: runLogic},
				Actions: []dsl.Action{
					{
						Name: "sequence",
						Type: "http_sequence",
						Params: map[string]any{"requests": []dsl.Action{{
							Name: "sequence child", Type: "GET", Method: "GET", Target: "/sequence",
						}}},
					},
					{Name: "must not run", Type: "GET", Method: "GET", Target: "/after"},
				},
			},
		}

		vu.Run(softCtx)

		if protoStub.executeCalls != 1 {
			t.Fatalf("protocol executions = %d, ordinary action ran after sequence exhausted MaxTransactions", protoStub.executeCalls)
		}
	})

	t.Run("per action", func(t *testing.T) {
		runLogic := dsl.RunLogicConfig{Iterations: 1, MaxTransactions: 1, AutoTransactions: "per_action"}
		softCtx, softCancel := context.WithCancel(context.Background())
		defer softCancel()
		protoStub := &iterationResetProtocol{}
		sink := &sequenceCaptureSink{}
		vu := &VUser{
			Proto:          protoStub,
			Metrics:        NewMetricsCollector(16),
			ResultSink:     sink,
			ExecutionGoals: NewExecutionGoalLimiter(runLogic, softCancel),
			HardStopCtx:    context.Background(),
			Script: &dsl.Script{
				Name:     "per-action-goal-boundary",
				Protocol: "browser",
				Config:   dsl.ScriptConfig{RunLogic: runLogic},
				Actions: []dsl.Action{
					{Name: "claimed", Type: "GET", Method: "GET", Target: "/claimed"},
					{Name: "must not run", Type: "GET", Method: "GET", Target: "/after"},
				},
			},
		}

		vu.Run(softCtx)

		if protoStub.executeCalls != 1 || len(sink.transactions) != 1 {
			t.Fatalf("executions/transactions = %d/%d, ordinary action ran after per-action goal", protoStub.executeCalls, len(sink.transactions))
		}
	})

	t.Run("unknown streamed transaction", func(t *testing.T) {
		runLogic := dsl.RunLogicConfig{Iterations: 1, MaxTransactions: 1, AutoTransactions: "none"}
		softCtx, softCancel := context.WithCancel(context.Background())
		defer softCancel()
		state := &teardownGoalDenialState{}
		vu := &VUser{
			Proto:          &teardownGoalDenialProtocol{state: state},
			Metrics:        NewMetricsCollector(16),
			ExecutionGoals: NewExecutionGoalLimiter(runLogic, softCancel),
			HardStopCtx:    context.Background(),
			Script: &dsl.Script{
				Name:     "unknown-stream-goal-boundary",
				Protocol: "teardown-goal-denial-test",
				Config:   dsl.ScriptConfig{RunLogic: runLogic},
				Actions: []dsl.Action{
					{
						Name: "untaken explicit marker",
						Type: "control_if",
						Control: &dsl.ControlFlow{
							Condition: &dsl.Condition{Expr: "false"},
							Steps: []dsl.Action{
								sourceTransactionAction("lr_start_transaction", ""),
								sourceTransactionAction("lr_end_transaction", "LR_PASS"),
							},
						},
					},
					{Name: "stream transaction", Type: "request", Target: "/stream"},
					{Name: "must not run", Type: "request", Target: "/after"},
				},
			},
		}

		vu.Run(softCtx)

		state.mu.Lock()
		executions := append([]string(nil), state.actions...)
		state.mu.Unlock()
		if len(executions) != 1 || executions[0] != "stream transaction" {
			t.Fatalf("protocol executions = %v, ordinary action ran after streamed transaction goal", executions)
		}
	})
}

// TestHTTPSequenceFailingBeforeAnyChildReportsElapsedDuration pins the
// measurement gap on the early-error path.
//
// An http_sequence aggregate reports the sum of its children's measured
// durations so think time is excluded from response time. When the first child
// errors before producing a result there is nothing to sum, and reporting the
// resulting zero is not "instant" — recordActionResult pins
// ActionMetrics.MinDuration to 0 and recordDurationSample feeds a 0 into the
// latency digest, skewing P50/P90 for every occurrence of that action.
func TestHTTPSequenceFailingBeforeAnyChildReportsElapsedDuration(t *testing.T) {
	sink := &sequenceCaptureSink{}
	script := &dsl.Script{
		Protocol: "http",
		Config: dsl.ScriptConfig{
			RunLogic: dsl.RunLogicConfig{Iterations: 1},
		},
		Actions: []dsl.Action{{
			Name: "failing sequence", Type: "http_sequence",
			Params: map[string]any{"requests": []dsl.Action{{
				Name: "child", Type: "GET", Method: "GET", Target: "/",
			}}},
		}},
	}
	vu := &VUser{
		Proto:      &sequenceProtocol{executeError: "adapter unavailable"},
		Metrics:    NewMetricsCollector(16),
		ResultSink: sink,
		Script:     script,
	}
	vu.Run(context.Background())

	if len(sink.transactions) != 1 {
		t.Fatalf("transactions = %#v, want one sequence aggregate", sink.transactions)
	}
	aggregate := sink.transactions[0]
	if aggregate.Success {
		t.Fatalf("aggregate = %#v, want failure", aggregate)
	}
	if aggregate.Duration <= 0 {
		t.Fatalf("aggregate duration = %v, want elapsed wall time when no child was measured", aggregate.Duration)
	}
}

// TestHTTPSequenceSucceedingReportsSummedChildDuration keeps the companion
// invariant: when children were measured, the aggregate reports their sum and
// does not fall back to wall time (which would reintroduce think time).
func TestHTTPSequenceSucceedingReportsSummedChildDuration(t *testing.T) {
	sink := &sequenceCaptureSink{}
	script := &dsl.Script{
		Protocol: "http",
		Config: dsl.ScriptConfig{
			RunLogic: dsl.RunLogicConfig{Iterations: 1},
		},
		Actions: []dsl.Action{{
			Name: "sequence", Type: "http_sequence",
			Params: map[string]any{"requests": []dsl.Action{
				{
					Name: "first", Type: "GET", Method: "GET", Target: "/first",
					ThinkTimeAfter: true,
					ThinkTime:      dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 50},
				},
				{Name: "second", Type: "GET", Method: "GET", Target: "/second"},
			}},
		}},
	}
	vu := &VUser{
		Proto:      &sequenceProtocol{},
		Metrics:    NewMetricsCollector(16),
		ResultSink: sink,
		Script:     script,
	}
	vu.Run(context.Background())

	if len(sink.transactions) != 1 {
		t.Fatalf("transactions = %#v, want one sequence aggregate", sink.transactions)
	}
	aggregate := sink.transactions[0]
	if !aggregate.Success {
		t.Fatalf("aggregate = %#v, want success", aggregate)
	}
	// Two children at 1ms each; the 50ms think time must not be counted.
	if aggregate.Duration != 2*time.Millisecond {
		t.Fatalf("aggregate duration = %v, want the 2ms sum of child durations", aggregate.Duration)
	}
}
