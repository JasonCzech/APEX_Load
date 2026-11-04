package engine

import (
	"context"
	stdhttp "net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

const quotaSpanProtocolName = "quota-span-test"

// quotaSpanProtocol answers every request at once and counts them by name.
type quotaSpanProtocol struct {
	mu    sync.Mutex
	calls map[string]int
}

func (*quotaSpanProtocol) Name() string                               { return quotaSpanProtocolName }
func (*quotaSpanProtocol) ParallelSafe() bool                         { return true }
func (*quotaSpanProtocol) Init(context.Context, map[string]any) error { return nil }
func (*quotaSpanProtocol) Teardown(context.Context) error             { return nil }
func (*quotaSpanProtocol) HealthCheck(context.Context) error          { return nil }
func (p *quotaSpanProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.calls == nil {
		p.calls = map[string]int{}
	}
	p.calls[action.Name]++
	return &protocol.Result{ActionName: action.Name, StartTime: time.Now(), Duration: time.Millisecond, Success: true, StatusCode: 200}, nil
}

func (p *quotaSpanProtocol) count(name string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls[name]
}

// quotaSpanShared is the instance the registry hands out, so branch VUsers,
// which create their own, count where the test can see.
var quotaSpanShared = &quotaSpanProtocol{}

func (p *quotaSpanProtocol) reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = nil
}

func init() {
	protocol.Register(quotaSpanProtocolName, func() protocol.Protocol { return quotaSpanShared })
}

func quotaSpanChat(name string) dsl.Action {
	return dsl.Action{Name: name, Type: "llm_request", Target: "/v1/chat/completions", Params: map[string]any{"model": "m", "prompt": "hi", "stream": false}}
}

// A retried child queues for quota once per attempt: each wait counts on its
// own, and the time between them, when its failed attempt was in flight, is
// busy time.
func TestIdleQuotaTimeCountsEachRetryWaitOnItsOwn(t *testing.T) {
	base := time.Now()
	at := func(ms int) time.Time { return base.Add(time.Duration(ms) * time.Millisecond) }
	// A runs 0-400ms. B queues 0-300ms, sends a failed attempt to 500ms,
	// queues again 500-900ms and succeeds by 1000ms: only 500-900ms has B
	// queued and nothing in flight.
	spans := []quotaWaitSpan{
		{start: at(0), end: at(400)},
		{start: at(0), end: at(1000), waits: []timeInterval{{at(0), at(300)}, {at(500), at(900)}}},
	}
	if got := idleQuotaTime(spans); got != 400*time.Millisecond {
		t.Fatalf("idle quota time = %v, want 400ms", got)
	}
	// Waits out of order, overlapping or past the span are merged and clipped.
	spans[1].waits = []timeInterval{{at(500), at(900)}, {at(0), at(300)}, {at(550), at(700)}, {at(900), at(1500)}}
	if got := idleQuotaTime(spans); got != 500*time.Millisecond {
		t.Fatalf("idle quota time = %v, want 500ms", got)
	}
}

// A parallel block's sample and the enclosing transactions leave out the time
// its branches only queued for quota.
func TestParallelBlockLeavesOutTimeItsBranchesOnlyQueued(t *testing.T) {
	sink := &quotaTxSink{}
	vu := &VUser{
		ID: 1, Group: "g", Proto: &quotaSpanProtocol{}, Metrics: NewMetricsCollector(32), ResultSink: sink,
		// 60 RPM: one branch goes at once, the other queues about a second.
		QuotaPacer: NewQuotaPacer(&dsl.QuotaPacingConfig{RPM: 60}, 1, 0),
		Script:     &dsl.Script{Protocol: quotaSpanProtocolName, Actions: []dsl.Action{{Name: "unused", Type: "GET", Target: "/"}}},
	}
	block := dsl.Action{Name: "fan-out", Type: "control_parallel", Control: &dsl.ControlFlow{Concurrency: 2, Branches: []dsl.ParallelBranch{
		{Name: "a", Steps: []dsl.Action{quotaSpanChat("a")}},
		{Name: "b", Steps: []dsl.Action{quotaSpanChat("b")}},
	}}}
	started := time.Now()
	if !vu.executeParallelBlock(context.Background(), block, "action") {
		t.Fatal("parallel block failed")
	}
	if elapsed := time.Since(started); elapsed < 800*time.Millisecond {
		t.Fatalf("the second branch did not queue for quota (block took %v)", elapsed)
	}
	if got := sink.resultDurations("fan-out"); len(got) != 1 || got[0] > 300*time.Millisecond {
		t.Fatalf("the block's sample counted the quota queue: %v", got)
	}
	if wait := vu.takeLLMQuotaWait(); wait < 700*time.Millisecond {
		t.Fatalf("the block left %v of idle quota time for its parent, want about a second", wait)
	}
}

// A concurrent sequence's transaction leaves out the time when a child
// queued for quota and none had a request in flight.
func TestQuotaWaitInAConcurrentSequenceCountsOnlyIdleTime(t *testing.T) {
	var calls atomic.Int64
	server := quotaTestServer(t, &calls)
	sink := &quotaTxSink{}
	script := quotaTestScript(server.URL, 60)
	// Concurrent children are HTTP requests; params.llm opts them in.
	hop := func(name string) dsl.Action {
		return dsl.Action{Name: name, Type: "POST", Target: "/v1/chat/completions", Body: `{"model":"m","messages":[{"role":"user","content":"hi"}]}`, Params: map[string]any{"llm": true}}
	}
	script.Actions = []dsl.Action{
		{Name: "fan-out", Type: "http_sequence", Params: map[string]any{"execution_mode": "concurrent", "max_concurrency": 2, "requests": []dsl.Action{hop("a"), hop("b")}}},
		{Name: "after", Type: "GET", Method: "GET", Target: "/health"},
	}
	script.Config.RunLogic.Iterations = 1
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups:   []GroupConfig{{Name: "g", Script: script, VUsers: 1, ResultSink: sink}},
		Duration: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	if _, err := mge.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if calls.Load() < 3 {
		t.Fatalf("calls = %d, want both children and the follow-up", calls.Load())
	}
	if got := sink.durations("fan-out"); len(got) != 1 || got[0] <= 0 || got[0] > 400*time.Millisecond {
		t.Fatalf("the concurrent sequence's transaction = %v, want the requests without the quota queue", got)
	}
	if got := sink.durations("after"); len(got) != 1 || got[0] <= 0 || got[0] > 400*time.Millisecond {
		t.Fatalf("the action after the sequence recorded %v; the sequence's quota wait leaked into it", got)
	}
}

// A sequence in vuser_init queues for quota inside the init phase: the source
// transaction around it leaves the wait out.
func TestQuotaWaitInALifecycleSequenceIsLeftOutOfItsTransaction(t *testing.T) {
	var calls atomic.Int64
	server := quotaTestServer(t, &calls)
	sink := &quotaTxSink{}
	script := quotaTestScript(server.URL, 60)
	hop := dsl.Action{Name: "hop", Type: "llm_request", Method: "POST", Target: "/v1/chat/completions", Params: map[string]any{"model": "m", "prompt": "hi", "stream": false}}
	script.Init = []dsl.Action{
		{Name: "Start warm", Type: "source_transaction", Params: map[string]any{"function": "lr_start_transaction", "args": []any{"warm"}}},
		{Name: "agent", Type: "http_sequence", Params: map[string]any{"requests": []dsl.Action{hop, hop}}},
		{Name: "End warm", Type: "source_transaction", Params: map[string]any{"function": "lr_end_transaction", "args": []any{"warm"}}},
	}
	script.Actions = []dsl.Action{{Name: "main", Type: "GET", Method: "GET", Target: "/health"}}
	script.Config.RunLogic.Iterations = 1
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups:   []GroupConfig{{Name: "g", Script: script, VUsers: 1, ResultSink: sink}},
		Duration: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	started := time.Now()
	if _, err := mge.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if elapsed := time.Since(started); elapsed < 800*time.Millisecond {
		t.Fatalf("the second hop did not queue for quota (run took %v)", elapsed)
	}
	if got := sink.durations("warm"); len(got) != 1 || got[0] > 400*time.Millisecond {
		t.Fatalf("the init transaction counted the quota queue: %v", got)
	}
}

// Teardown runs with the pause lifted, and its LLM actions do not wait out a
// paused pacer either; the pause still holds every other action.
func TestTeardownLLMActionsPassAPausedQuotaPacer(t *testing.T) {
	llm := protocol.Action{Name: "chat", Type: "llm_request"}
	held := &VUser{QuotaPacer: NewQuotaPacer(&dsl.QuotaPacingConfig{RPM: 6000}, 1, 0)}
	held.QuotaPacer.SetPaused(true)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, ok := held.waitLLMQuota(ctx, &llm); ok {
		t.Fatal("a paused pacer admitted an action outside teardown")
	}

	qp := NewQuotaPacer(&dsl.QuotaPacingConfig{RPM: 60}, 1, 0)
	qp.SetPaused(true)
	proto := &quotaSpanProtocol{}
	vu := &VUser{ID: 1, Group: "g", Proto: proto, Metrics: NewMetricsCollector(32), QuotaPacer: qp, Script: &dsl.Script{
		Name: "teardown", Protocol: quotaSpanProtocolName,
		Config:   dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}},
		Actions:  []dsl.Action{{Name: "main", Type: "GET", Target: "/health"}},
		Teardown: []dsl.Action{quotaSpanChat("bye")},
	}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		vu.Run(context.Background())
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		qp.SetPaused(false)
		<-done
		t.Fatal("teardown waited out the paused quota pacer")
	}
	if proto.count("bye") != 1 || proto.count("main") != 1 {
		t.Fatalf("calls main=%d bye=%d, want one each", proto.count("main"), proto.count("bye"))
	}
	if vu.quotaPauseBypass {
		t.Fatal("the teardown bypass outlived teardown")
	}
}

// A poll's first request runs inside its start step, an engine action with
// no sample of its own: the step takes the request's quota wait at once, so
// it leaves the iteration and does not reach the next step.
func TestAsyncPollFirstRequestQuotaWaitIsTakenAtTheStartStep(t *testing.T) {
	qp := NewQuotaPacer(&dsl.QuotaPacingConfig{RPM: 60}, 1, 0)
	if _, ok := qp.Wait(context.Background()); !ok {
		t.Fatal("first admission failed")
	}
	proto := &asyncPollProtocol{}
	vu := &VUser{ID: 1, Group: "g", Proto: proto, Metrics: NewMetricsCollector(64), QuotaPacer: qp, Script: &dsl.Script{
		Name: "poll", Protocol: "http", Actions: []dsl.Action{{Name: "unused", Type: "GET", Target: "/"}},
	}}
	start := dsl.Action{Name: "start poll", Type: "http_async_start", Params: map[string]any{
		"id": "poll", "pattern": "poll", "interval_ms": 100,
		"request": map[string]any{"name": "poll", "type": "llm_request", "target": "/poll", "params": map[string]any{"model": "m", "prompt": "hi"}},
	}}
	started := time.Now()
	if err := vu.startAsyncConversation(context.Background(), start); err != nil {
		t.Fatalf("startAsyncConversation: %v", err)
	}
	defer func() { _ = vu.stopAsyncConversation(context.Background(), "poll") }()
	if elapsed := time.Since(started); elapsed < 800*time.Millisecond || proto.pollCount() != 1 {
		t.Fatalf("the first poll did not queue for quota (took %v, %d polls)", elapsed, proto.pollCount())
	}
	if vu.llmQuotaWait != 0 {
		t.Fatalf("the start step left %v of quota wait for the next step", vu.llmQuotaWait)
	}
	if vu.iterationThinkTime < 700*time.Millisecond {
		t.Fatalf("the iteration kept the first poll's quota wait (excluded %v)", vu.iterationThinkTime)
	}
}

// A bypassing (teardown) waiter is admitted while the pacer is paused, even
// when a paused waiter holds the gate or the pause begins while it is queued.
func TestQuotaBypassPassesAPausedWaiterHoldingTheGate(t *testing.T) {
	qp := NewQuotaPacer(&dsl.QuotaPacingConfig{RPM: 60}, 1, 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, ok := qp.Wait(ctx); !ok {
		t.Fatal("first admission failed")
	}
	// An ordinary waiter takes the gate and queues for the next credit.
	held := make(chan struct{})
	go func() {
		defer close(held)
		_, _ = qp.Wait(ctx)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for len(qp.gate) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(qp.gate) == 0 {
		t.Fatal("the ordinary waiter did not take the gate")
	}
	// The bypassing waiter queues behind it; then the pause begins.
	admitted := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		if _, ok := qp.wait(context.Background(), true); ok {
			admitted <- time.Since(start)
		}
	}()
	time.Sleep(50 * time.Millisecond)
	qp.SetPaused(true)
	select {
	case <-admitted:
	case <-time.After(700 * time.Millisecond):
		t.Fatal("a bypassing waiter queued behind a paused waiter was not admitted")
	}
	start := time.Now()
	if _, ok := qp.wait(context.Background(), true); !ok || time.Since(start) > 100*time.Millisecond {
		t.Fatalf("a bypassing waiter arriving during the pause waited %v", time.Since(start))
	}
	cancel()
	<-held
}

// Teardown's parallel branches inherit the bypass.
func TestTeardownParallelLLMBranchesPassAPausedQuotaPacer(t *testing.T) {
	qp := NewQuotaPacer(&dsl.QuotaPacingConfig{RPM: 60}, 1, 0)
	qp.SetPaused(true)
	proto := quotaSpanShared
	proto.reset()
	vu := &VUser{ID: 1, Group: "g", Proto: proto, Metrics: NewMetricsCollector(32), QuotaPacer: qp, Script: &dsl.Script{
		Name: "teardown", Protocol: quotaSpanProtocolName,
		Config:  dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}},
		Actions: []dsl.Action{{Name: "main", Type: "GET", Target: "/health"}},
		Teardown: []dsl.Action{{Name: "bye", Type: "control_parallel", Control: &dsl.ControlFlow{Concurrency: 2, Branches: []dsl.ParallelBranch{
			{Name: "a", Steps: []dsl.Action{quotaSpanChat("bye-a")}},
			{Name: "b", Steps: []dsl.Action{quotaSpanChat("bye-b")}},
		}}}},
	}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		vu.Run(context.Background())
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		qp.SetPaused(false)
		<-done
		t.Fatal("teardown's parallel branches waited out the paused quota pacer")
	}
	if proto.count("bye-a") != 1 || proto.count("bye-b") != 1 {
		t.Fatalf("teardown branches sent a=%d b=%d, want one each", proto.count("bye-a"), proto.count("bye-b"))
	}
}

// A poll started in teardown keeps the bypass it started with: its worker
// reads the conversation's copy, not the VUser's field, which teardown resets
// on its own goroutine (a data race, and a stalled poll, otherwise).
func TestTeardownAsyncPollKeepsItsQuotaBypass(t *testing.T) {
	qp := NewQuotaPacer(&dsl.QuotaPacingConfig{RPM: 6000}, 1, 0)
	qp.SetPaused(true)
	proto := &asyncPollProtocol{}
	vu := &VUser{ID: 1, Group: "g", Proto: proto, Metrics: NewMetricsCollector(64), QuotaPacer: qp, Script: &dsl.Script{
		Name: "poll", Protocol: "http", Actions: []dsl.Action{{Name: "unused", Type: "GET", Target: "/"}},
	}}
	start := dsl.Action{Name: "start poll", Type: "http_async_start", Params: map[string]any{
		"id": "poll", "pattern": "poll", "interval_ms": 100,
		"request": map[string]any{"name": "poll", "type": "llm_request", "target": "/poll", "params": map[string]any{"model": "m", "prompt": "hi"}},
	}}
	vu.quotaPauseBypass = true
	if err := vu.startAsyncConversation(context.Background(), start); err != nil {
		t.Fatalf("startAsyncConversation: %v", err)
	}
	defer func() { _ = vu.stopAsyncConversation(context.Background(), "poll") }()
	vu.quotaPauseBypass = false
	deadline := time.Now().Add(2 * time.Second)
	for proto.pollCount() < 4 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := proto.pollCount(); got < 4 {
		t.Fatalf("polls = %d; the worker lost the bypass it started with", got)
	}
}

// Only LLM actions that queued record a wait interval: plain actions and
// admissions that took no time would fill the cap.
func TestOnlyQueuedLLMWaitsRecordAnInterval(t *testing.T) {
	vu := &VUser{
		ID: 1, Group: "g", Proto: &quotaSpanProtocol{}, Metrics: NewMetricsCollector(32), trackQuotaWaitIntervals: true,
		QuotaPacer: NewQuotaPacer(&dsl.QuotaPacingConfig{RPM: 60}, 1, 0),
		Script:     &dsl.Script{Protocol: quotaSpanProtocolName, Actions: []dsl.Action{{Name: "unused", Type: "GET", Target: "/"}}},
	}
	plain := vu.resolveAction(dsl.Action{Name: "plain", Type: "GET", Target: "/plain"})
	for i := 0; i < 2*maxTrackedQuotaWaits; i++ {
		if _, err := vu.executeWithRetry(context.Background(), plain, dsl.ErrorHandlingConfig{}); err != nil {
			t.Fatalf("plain: %v", err)
		}
	}
	chat := vu.resolveAction(quotaSpanChat("chat"))
	if _, err := vu.executeWithRetry(context.Background(), chat, dsl.ErrorHandlingConfig{}); err != nil {
		t.Fatalf("chat: %v", err)
	}
	if n := len(vu.llmQuotaWaitIntervals); n != 0 {
		t.Fatalf("plain actions and an immediate admission recorded %d intervals", n)
	}
	if _, err := vu.executeWithRetry(context.Background(), chat, dsl.ErrorHandlingConfig{}); err != nil {
		t.Fatalf("queued chat: %v", err)
	}
	if n := len(vu.llmQuotaWaitIntervals); n != 1 || vu.llmQuotaWaitIntervals[0].end.Sub(vu.llmQuotaWaitIntervals[0].start) < 700*time.Millisecond {
		t.Fatalf("intervals = %v, want the one queued wait of about a second", vu.llmQuotaWaitIntervals)
	}
}

// Actions the pacer does not pace leave no wait intervals, so a branch's
// many plain steps cannot crowd out the quota wait that follows them.
func TestParallelBranchPlainStepsDoNotCrowdOutQuotaWaits(t *testing.T) {
	sink := &quotaTxSink{}
	vu := &VUser{
		ID: 1, Group: "g", Proto: &quotaSpanProtocol{}, Metrics: NewMetricsCollector(32), ResultSink: sink,
		QuotaPacer: NewQuotaPacer(&dsl.QuotaPacingConfig{RPM: 60}, 1, 0),
		Script:     &dsl.Script{Protocol: quotaSpanProtocolName, Actions: []dsl.Action{{Name: "unused", Type: "GET", Target: "/"}}},
	}
	if _, ok := vu.QuotaPacer.Wait(context.Background()); !ok {
		t.Fatal("first admission failed")
	}
	steps := make([]dsl.Action, 0, maxTrackedQuotaWaits+51)
	for i := 0; i < maxTrackedQuotaWaits+50; i++ {
		steps = append(steps, dsl.Action{Name: "plain", Type: "GET", Target: "/plain"})
	}
	steps = append(steps, quotaSpanChat("queued"))
	block := dsl.Action{Name: "fan-out", Type: "control_parallel", Control: &dsl.ControlFlow{Concurrency: 2, Branches: []dsl.ParallelBranch{
		{Name: "busy", Steps: steps},
		{Name: "quick", Steps: []dsl.Action{{Name: "quick", Type: "GET", Target: "/quick"}}},
	}}}
	started := time.Now()
	if !vu.executeParallelBlock(context.Background(), block, "action") {
		t.Fatal("parallel block failed")
	}
	if elapsed := time.Since(started); elapsed < 800*time.Millisecond {
		t.Fatalf("the LLM step did not queue for quota (block took %v)", elapsed)
	}
	if got := sink.resultDurations("fan-out"); len(got) != 1 || got[0] > 400*time.Millisecond {
		t.Fatalf("the block's sample counted the quota queue: %v", got)
	}
}

// A concurrent sequence inside a parallel branch passes the windows when it
// only queued for quota up to the block.
func TestConcurrentSequenceInAParallelBranchReportsItsIdleQuota(t *testing.T) {
	var calls atomic.Int64
	server := quotaTestServer(t, &calls)
	sink := &quotaTxSink{}
	script := quotaTestScript(server.URL, 60)
	hop := func(name string) dsl.Action {
		return dsl.Action{Name: name, Type: "POST", Target: "/v1/chat/completions", Body: `{"model":"m","messages":[{"role":"user","content":"hi"}]}`, Params: map[string]any{"llm": true}}
	}
	script.Actions = []dsl.Action{{Name: "fan-out", Type: "control_parallel", Control: &dsl.ControlFlow{Concurrency: 2, Branches: []dsl.ParallelBranch{
		{Name: "agent", Steps: []dsl.Action{{Name: "pair", Type: "http_sequence", Params: map[string]any{"execution_mode": "concurrent", "max_concurrency": 2, "requests": []dsl.Action{hop("a"), hop("b")}}}}},
		{Name: "quick", Steps: []dsl.Action{{Name: "quick", Type: "GET", Method: "GET", Target: "/health"}}},
	}}}}
	script.Config.RunLogic.Iterations = 1
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups:   []GroupConfig{{Name: "g", Script: script, VUsers: 1, ResultSink: sink}},
		Duration: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	started := time.Now()
	if _, err := mge.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if elapsed := time.Since(started); elapsed < 800*time.Millisecond || calls.Load() < 3 {
		t.Fatalf("a child did not queue for quota (run took %v, %d calls)", elapsed, calls.Load())
	}
	if got := sink.resultDurations("fan-out"); len(got) != 1 || got[0] > 400*time.Millisecond {
		t.Fatalf("the block's sample counted the sequence's quota queue: %v", got)
	}
}

// The quota time a parallel block's branches only queued for stays out of
// the source transaction around the block, in the main phase and in
// vuser_init, and does not reach the step after it.
func TestQuotaWaitInAParallelBlockIsLeftOutOfItsTransactions(t *testing.T) {
	var calls atomic.Int64
	server := quotaTestServer(t, &calls)
	sink := &quotaTxSink{}
	script := quotaTestScript(server.URL, 60)
	fanOut := func(name string) dsl.Action {
		return dsl.Action{Name: name, Type: "control_parallel", Control: &dsl.ControlFlow{Concurrency: 2, Branches: []dsl.ParallelBranch{
			{Name: "a", Steps: []dsl.Action{quotaSpanChat(name + "-a")}},
			{Name: "b", Steps: []dsl.Action{quotaSpanChat(name + "-b")}},
		}}}
	}
	tx := func(function, name string) dsl.Action {
		return dsl.Action{Name: function + " " + name, Type: "source_transaction", Params: map[string]any{"function": function, "args": []any{name}}}
	}
	script.Init = []dsl.Action{tx("lr_start_transaction", "init-par"), fanOut("init-fan"), tx("lr_end_transaction", "init-par")}
	script.Actions = []dsl.Action{tx("lr_start_transaction", "par"), fanOut("fan"), tx("lr_end_transaction", "par"), {Name: "after", Type: "GET", Method: "GET", Target: "/health"}}
	script.Config.RunLogic.Iterations = 1
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups:   []GroupConfig{{Name: "g", Script: script, VUsers: 1, ResultSink: sink}},
		Duration: 15 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	started := time.Now()
	if _, err := mge.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if elapsed := time.Since(started); elapsed < 2500*time.Millisecond {
		t.Fatalf("the branches did not queue for quota (run took %v)", elapsed)
	}
	for _, name := range []string{"init-par", "par"} {
		if got := sink.durations(name); len(got) != 1 || got[0] > 400*time.Millisecond {
			t.Fatalf("transaction %s counted the quota queue: %v", name, got)
		}
	}
	if got := sink.durations("after"); len(got) != 1 || got[0] <= 0 || got[0] > 400*time.Millisecond {
		t.Fatalf("the step after the block recorded %v; the block's quota wait leaked into it", got)
	}
}

// A bypassing caller on a busy pacer that is not paused keeps its place in
// the gate's queue instead of rejoining it at the back.
func TestQuotaBypassKeepsItsPlaceOnABusyPacer(t *testing.T) {
	qp := NewQuotaPacer(&dsl.QuotaPacingConfig{RPM: 600}, 1, 0)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	defer func() {
		cancel()
		wg.Wait()
	}()
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				_, _ = qp.Wait(ctx)
			}
		}()
	}
	time.Sleep(200 * time.Millisecond)
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer waitCancel()
	start := time.Now()
	_, ok := qp.wait(waitCtx, true)
	if elapsed := time.Since(start); !ok || elapsed > 2*time.Second {
		t.Fatalf("a bypassing caller behind five waiters at 10/s was admitted=%v after %v, want well under 2s", ok, elapsed)
	}
}

// quotaPauseServer answers LLM calls at once, counting them, and holds /slow
// until released, so a test can pause the pacer while the run is busy.
func quotaPauseServer(t *testing.T, llmCalls *atomic.Int64, slowArrived chan<- struct{}) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		if r.URL.Path == "/slow" {
			select {
			case slowArrived <- struct{}{}:
			default:
			}
			time.Sleep(500 * time.Millisecond)
			w.WriteHeader(stdhttp.StatusOK)
			return
		}
		llmCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":1,"total_tokens":6}}`))
	}))
	t.Cleanup(server.Close)
	return server
}

// Teardown that the group runs once (init_strategy at_once), and a
// concurrent sequence in a user's teardown, pass a pacer paused while the
// run was busy.
func TestTeardownBypassReachesGroupTeardownAndConcurrentChildren(t *testing.T) {
	for _, tc := range []struct {
		name     string
		atOnce   bool
		teardown dsl.Action
		want     int64
	}{
		{name: "at_once group teardown", atOnce: true, want: 1,
			teardown: dsl.Action{Name: "bye", Type: "llm_request", Method: "POST", Target: "/v1/chat/completions", Params: map[string]any{"model": "m", "prompt": "bye", "stream": false}}},
		{name: "concurrent sequence in teardown", want: 2,
			teardown: dsl.Action{Name: "bye", Type: "http_sequence", Params: map[string]any{"execution_mode": "concurrent", "max_concurrency": 2, "requests": []dsl.Action{
				{Name: "bye-a", Type: "POST", Target: "/v1/chat/completions", Body: `{"model":"m","messages":[{"role":"user","content":"a"}]}`, Params: map[string]any{"llm": true}},
				{Name: "bye-b", Type: "POST", Target: "/v1/chat/completions", Body: `{"model":"m","messages":[{"role":"user","content":"b"}]}`, Params: map[string]any{"llm": true}},
			}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var llmCalls atomic.Int64
			slowArrived := make(chan struct{}, 1)
			server := quotaPauseServer(t, &llmCalls, slowArrived)
			script := quotaTestScript(server.URL, 60)
			script.Actions = []dsl.Action{{Name: "slow", Type: "GET", Method: "GET", Target: "/slow"}}
			script.Teardown = []dsl.Action{tc.teardown}
			script.Config.RunLogic.Iterations = 1
			group := GroupConfig{Name: "g", Script: script, VUsers: 1}
			if tc.atOnce {
				group.InitStrategy = "at_once"
			}
			mge, err := NewMultiGroup(MultiGroupConfig{Groups: []GroupConfig{group}, Duration: 20 * time.Second})
			if err != nil {
				t.Fatalf("NewMultiGroup: %v", err)
			}
			done := make(chan struct{})
			go func() {
				defer close(done)
				_, _ = mge.Run(context.Background())
			}()
			select {
			case <-slowArrived:
			case <-time.After(5 * time.Second):
				t.Fatal("the slow step never started")
			}
			mge.mu.Lock()
			qp := mge.groups[0].quotaPacer
			mge.mu.Unlock()
			if qp == nil {
				t.Fatal("the group has no quota pacer")
			}
			qp.SetPaused(true)
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				qp.SetPaused(false)
				<-done
				t.Fatal("teardown waited out the paused quota pacer")
			}
			if got := llmCalls.Load(); got != tc.want {
				t.Fatalf("teardown sent %d LLM requests, want %d", got, tc.want)
			}
		})
	}
}
