package engine

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

// asyncPollProtocol answers every request; /poll responses count up and
// extract their count into var.n.
type asyncPollProtocol struct {
	stubProtocol
	mu       sync.Mutex
	polls    []time.Time
	marked   int
	requests []string
	rounds   []string
}

func (p *asyncPollProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, action.Target)
	result := &protocol.Result{ActionName: action.Name, StartTime: time.Now(), Success: true, StatusCode: 200, Extractions: map[string]string{}}
	if action.Target == "/poll" {
		p.polls = append(p.polls, time.Now())
		round, _ := action.Params["round"].(string)
		p.rounds = append(p.rounds, round)
		if id, _ := action.Params["async_conversation"].(string); id != "" {
			p.marked++
		}
		result.Extractions["n"] = fmt.Sprint(len(p.polls))
	}
	return result, nil
}

func (p *asyncPollProtocol) pollCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.polls)
}

func asyncPollStart(extra map[string]any) dsl.Action {
	params := map[string]any{
		"id": "poll", "pattern": "poll", "interval_ms": 100,
		"request": map[string]any{"name": "poll", "type": "request", "method": "GET", "target": "/poll"},
	}
	for key, value := range extra {
		params[key] = value
	}
	return dsl.Action{Name: "start poll", Type: "http_async_start", Params: params}
}

func TestHTTPAsyncPollSendsOneRequestPerIntervalUntilStopped(t *testing.T) {
	proto := &asyncPollProtocol{}
	vu := &VUser{ID: 1, Group: "g", Proto: proto, Metrics: NewMetricsCollector(64), Script: &dsl.Script{
		Name: "poll", Protocol: "http",
		Config: dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}},
		Actions: []dsl.Action{
			asyncPollStart(nil),
			{Name: "wait for 4", Type: "http_sync", Params: map[string]any{"variable": "n", "value": "4", "timeout_ms": 5000}},
			{Name: "stop poll", Type: "http_async_stop", Params: map[string]any{"id": "poll"}},
			{Name: "after", Type: "request", Method: "GET", Target: "/after"},
		},
	}}
	vu.Run(context.Background())

	stopped := proto.pollCount()
	if stopped < 4 {
		t.Fatalf("polls before stop = %d, want at least 4", stopped)
	}
	time.Sleep(350 * time.Millisecond)
	if after := proto.pollCount(); after != stopped {
		t.Fatalf("http_async_stop must end the series: %d polls at stop, %d later", stopped, after)
	}
	proto.mu.Lock()
	defer proto.mu.Unlock()
	for i := 1; i < len(proto.polls); i++ {
		if gap := proto.polls[i].Sub(proto.polls[i-1]); gap < 90*time.Millisecond {
			t.Fatalf("poll %d came %s after the previous one, want one request per 100 ms interval", i, gap)
		}
	}
	if proto.marked != len(proto.polls) {
		t.Fatalf("every async request must carry params.async_conversation: %d of %d", proto.marked, len(proto.polls))
	}
	if proto.requests[len(proto.requests)-1] != "/after" {
		t.Fatalf("the script must continue after the stop: %v", proto.requests)
	}
	if got, _ := strconv.Atoi(vu.Variables["n"]); got < 4 {
		t.Fatalf("var.n = %d, want the extraction of a later poll", got)
	}
}

func TestHTTPAsyncConversationEndsWithTheIteration(t *testing.T) {
	proto := &asyncPollProtocol{}
	vu := &VUser{ID: 1, Group: "g", Proto: proto, Metrics: NewMetricsCollector(64), Script: &dsl.Script{
		Name: "poll", Protocol: "http",
		Config: dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 2}},
		Actions: []dsl.Action{
			// Starting the same ID again in iteration 2 fails unless the
			// conversation ended with iteration 1.
			asyncPollStart(nil),
			{Name: "wait for a poll", Type: "http_sync", Params: map[string]any{"variable": "n", "timeout_ms": 5000}},
		},
	}}
	vu.Run(context.Background())
	errors := vu.Metrics.TotalErrors()
	if errors != 0 {
		t.Fatalf("restarting the conversation in the next iteration failed: %d errors", errors)
	}
	stopped := proto.pollCount()
	time.Sleep(300 * time.Millisecond)
	if proto.pollCount() != stopped {
		t.Fatal("a conversation the script never stopped kept running after the virtual user ended")
	}
	if vu.async != nil && len(vu.async.byID) != 0 {
		t.Fatalf("conversations left registered: %v", vu.async.order)
	}
}

func TestHTTPAsyncCallbacksRunOnTheVirtualUser(t *testing.T) {
	proto := &asyncPollProtocol{}
	vu := &VUser{ID: 1, Group: "g", Proto: proto, Metrics: NewMetricsCollector(64), Script: &dsl.Script{Name: "poll", Protocol: "http"}}
	vu.ensureVariables()
	start := asyncPollStart(nil)
	start.Control = &dsl.ControlFlow{Branches: []dsl.ParallelBranch{{Name: AsyncOnResponse, Steps: []dsl.Action{{Name: "seen", Type: "request", Method: "GET", Target: "/seen/{{var.async_conversation_id}}/{{var.async_response_status}}"}}}}}
	ctx := context.Background()
	if _, err := vu.handleEngineAction(ctx, start); err != nil {
		t.Fatal(err)
	}
	if _, err := vu.handleEngineAction(ctx, dsl.Action{Name: "dup", Type: "http_async_start", Params: start.Params}); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("a second conversation with the same ID must be refused: %v", err)
	}
	if _, err := vu.handleEngineAction(ctx, dsl.Action{Name: "sync", Type: "http_sync", Params: map[string]any{"variable": "n", "value": "2", "timeout_ms": 5000}}); err != nil {
		t.Fatal(err)
	}
	if _, err := vu.handleEngineAction(ctx, dsl.Action{Name: "stop", Type: "http_async_stop", Params: map[string]any{"id": "poll"}}); err != nil {
		t.Fatal(err)
	}
	proto.mu.Lock()
	defer proto.mu.Unlock()
	callbacks := 0
	for _, target := range proto.requests {
		if target == "/seen/poll/200" {
			callbacks++
		}
	}
	if callbacks != len(proto.polls) {
		t.Fatalf("callbacks = %d, want one per poll response (%d): %v", callbacks, len(proto.polls), proto.requests)
	}
	if _, err := vu.handleEngineAction(ctx, dsl.Action{Name: "stop", Type: "http_async_stop", Params: map[string]any{"id": "poll"}}); err == nil {
		t.Fatal("stopping a conversation that is not running must fail")
	}
}

func TestHTTPAsyncSyncTimesOut(t *testing.T) {
	vu := &VUser{ID: 1, Group: "g", Proto: &asyncPollProtocol{}, Metrics: NewMetricsCollector(16), Script: &dsl.Script{Name: "s", Protocol: "http"}}
	vu.ensureVariables()
	started := time.Now()
	_, err := vu.handleEngineAction(context.Background(), dsl.Action{Name: "sync", Type: "http_sync", Params: map[string]any{"variable": "never", "timeout_ms": 150}})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("http_sync must fail when the variable never arrives: %v", err)
	}
	if elapsed := time.Since(started); elapsed < 140*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("http_sync waited %s for a 150 ms timeout", elapsed)
	}
}

var registerAsyncHTTPProtocol sync.Once

// The real HTTP protocol serves a background conversation concurrently with
// the script's own requests; replay reports each response as an async event.
func TestHTTPAsyncReplayWithTheHTTPProtocol(t *testing.T) {
	const protocolName = "http-async-replay-test"
	registerAsyncHTTPProtocol.Do(func() {
		protocol.Register(protocolName, func() protocol.Protocol { return &httpprotocol.HTTP{} })
	})
	var polls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/poll":
			n := polls.Add(1)
			fmt.Fprintf(w, `{"status":"%s"}`, map[bool]string{true: "ready", false: "pending"}[n >= 3])
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()
	script := &dsl.Script{Name: "async", Protocol: protocolName, Config: dsl.ScriptConfig{BaseURL: server.URL}, Actions: []dsl.Action{
		{Name: "start", Type: "http_async_start", Params: map[string]any{
			"id": "status", "pattern": "long_poll",
			"request": map[string]any{"name": "status", "type": "request", "method": "GET", "target": "/poll",
				"extractors": []any{map[string]any{"name": "status", "type": "jsonpath", "source": "body", "expression": "$.status"}}},
		}},
		{Name: "work", Type: "request", Method: "GET", Target: "/work"},
		{Name: "ready", Type: "http_sync", Params: map[string]any{"variable": "status", "value": "ready", "timeout_ms": 5000}},
		{Name: "stop", Type: "http_async_stop", Params: map[string]any{"id": "status"}},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	report, err := ReplayScript(ctx, script, ReplayOptions{IgnoreThinkTime: true})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Success {
		t.Fatalf("replay failed: %s %+v", report.Error, report.Events)
	}
	async := 0
	for _, event := range report.Events {
		if event.Phase == "async" {
			async++
			if event.ActionName != "async status" || event.StatusCode != 200 || !event.Success {
				t.Fatalf("async event = %+v", event)
			}
		}
	}
	if async < 3 || int64(async) > polls.Load() {
		t.Fatalf("async events = %d for %d polls", async, polls.Load())
	}
	if report.FinalVariables["status"] != "ready" {
		t.Fatalf("final status = %q", report.FinalVariables["status"])
	}
}

func TestHTTPAsyncOnRequestAndStopFromCallback(t *testing.T) {
	proto := &asyncPollProtocol{}
	vu := &VUser{ID: 1, Group: "g", Proto: proto, Metrics: NewMetricsCollector(64), Script: &dsl.Script{Name: "poll", Protocol: "http"}}
	vu.ensureVariables()
	vu.Variables["n"] = "0"
	start := asyncPollStart(nil)
	start.Params["request"] = map[string]any{"name": "poll", "type": "request", "method": "GET", "target": "/poll", "params": map[string]any{"round": "{{var.round}}"}}
	start.Control = &dsl.ControlFlow{Branches: []dsl.ParallelBranch{
		{Name: AsyncOnRequest, Steps: []dsl.Action{{Name: "count", Type: "source_parameter", Params: map[string]any{"function": "lr_save_string", "args": []any{"r{{var.n}}", "round"}}}}},
		// The ResponseCB of a VuGen script ends its conversation by
		// returning WEB_ASYNC_CB_RC_ABORT_ASYNC_NOT_ERROR.
		{Name: AsyncOnResponse, Steps: []dsl.Action{{Name: "enough", Type: "control_if", Control: &dsl.ControlFlow{
			Condition: &dsl.Condition{Left: "{{var.n}}", Op: "==", Right: "3"},
			Steps:     []dsl.Action{{Name: "stop", Type: "http_async_stop", Params: map[string]any{"id": "poll"}}},
		}}}},
	}}
	ctx := context.Background()
	if _, err := vu.handleEngineAction(ctx, start); err != nil {
		t.Fatal(err)
	}
	proto.mu.Lock()
	firstRound := proto.rounds[0]
	proto.mu.Unlock()
	if firstRound != "r0" {
		t.Fatalf("on_request must run before the first request is resolved: round=%q", firstRound)
	}
	if vu.Variables["round"] != "r1" {
		t.Fatalf("a poll's first request runs in the start step, and on_request runs again after its response: round=%q", vu.Variables["round"])
	}
	deadline := time.Now().Add(5 * time.Second)
	for vu.asyncConversation("poll") != nil && time.Now().Before(deadline) {
		if err := vu.dispatchAsyncResults(ctx); err != nil {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if vu.asyncConversation("poll") != nil {
		t.Fatal("http_async_stop inside on_response did not end the conversation")
	}
	stopped := proto.pollCount()
	time.Sleep(250 * time.Millisecond)
	if proto.pollCount() != stopped {
		t.Fatal("the conversation kept polling after its callback stopped it")
	}
	if round := vu.Variables["round"]; round == "r0" || round == "r3" {
		// It runs after each applied batch of responses, except after the
		// one whose callback stopped the conversation.
		t.Fatalf("on_request must run again after responses are applied: round=%q", vu.Variables["round"])
	}
}

var registerAsyncPushProtocol sync.Once

// A push conversation reads the server-sent event stream and runs
// on_body_chunk as each event arrives, then re-opens the stream.
func TestHTTPAsyncPushRunsChunkCallbacksAsEventsArrive(t *testing.T) {
	const protocolName = "http-async-push-test"
	registerAsyncPushProtocol.Do(func() {
		protocol.Register(protocolName, func() protocol.Protocol { return &httpprotocol.HTTP{} })
	})
	var streams atomic.Int64
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/events" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		n := streams.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		fmt.Fprintf(w, "data: first-%d\n\n", n)
		flusher.Flush()
		if n == 1 {
			// The second event waits until the script has seen the first.
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		fmt.Fprintf(w, "data: second-%d\n\n", n)
		flusher.Flush()
	}))
	defer server.Close()
	script := &dsl.Script{Name: "push", Protocol: protocolName, Config: dsl.ScriptConfig{BaseURL: server.URL}, Actions: []dsl.Action{
		{Name: "start", Type: "http_async_start", Params: map[string]any{
			"id": "feed", "pattern": "push",
			"request": map[string]any{"name": "feed", "type": "request", "method": "GET", "target": "/events"},
		}, Control: &dsl.ControlFlow{Branches: []dsl.ParallelBranch{{Name: AsyncOnBodyChunk, Steps: []dsl.Action{
			{Name: "keep", Type: "source_parameter", Params: map[string]any{"function": "lr_save_string", "args": []any{"{{var.async_chunk}}", "last_chunk"}}},
		}}}}},
		{Name: "first", Type: "http_sync", Params: map[string]any{"variable": "last_chunk", "value": "first-1", "timeout_ms": 5000}},
		{Name: "release", Type: "request", Method: "GET", Target: "/release"},
		{Name: "reopened", Type: "http_sync", Params: map[string]any{"variable": "last_chunk", "value": "second-2", "timeout_ms": 5000}},
		{Name: "stop", Type: "http_async_stop", Params: map[string]any{"id": "feed"}},
	}}
	go func() {
		// The first stream holds its second event until the script has
		// synced on the first, proving chunks arrive before the response ends.
		for streams.Load() == 0 {
			time.Sleep(10 * time.Millisecond)
		}
		time.Sleep(300 * time.Millisecond)
		close(release)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	report, err := ReplayScript(ctx, script, ReplayOptions{IgnoreThinkTime: true})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Success {
		t.Fatalf("replay failed: %s %+v", report.Error, report.Events)
	}
	if streams.Load() < 2 {
		t.Fatalf("push must re-open the stream when it ends: %d streams", streams.Load())
	}
}

// A conversation init started stops when init fails and the virtual user
// returns early.
func TestHTTPAsyncConversationStopsWhenInitFails(t *testing.T) {
	proto := &asyncPollProtocol{}
	stop := false
	vu := &VUser{ID: 1, Group: "g", Proto: proto, Metrics: NewMetricsCollector(64), Script: &dsl.Script{
		Name: "poll", Protocol: "http",
		Config: dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}, ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: &stop}},
		Init: []dsl.Action{
			asyncPollStart(nil),
			{Name: "never ready", Type: "http_sync", Params: map[string]any{"variable": "never", "timeout_ms": 50}},
		},
		Actions: []dsl.Action{{Name: "unreached", Type: "request", Method: "GET", Target: "/unreached"}},
	}}
	vu.Run(context.Background())
	stopped := proto.pollCount()
	time.Sleep(300 * time.Millisecond)
	if proto.pollCount() != stopped {
		t.Fatal("a conversation started in init kept running after init failed")
	}
	for _, target := range proto.requests {
		if target == "/unreached" {
			t.Fatal("the actions ran although init failed")
		}
	}
}

// backgroundProtocol holds every /log request for delay and counts how many
// are in flight at once.
type backgroundProtocol struct {
	stubProtocol
	delay       time.Duration
	mu          sync.Mutex
	inFlight    int
	maxInFlight int
	logsDone    int
	nextAt      time.Time
	logDoneAt   time.Time
}

func (p *backgroundProtocol) Execute(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
	started := time.Now()
	if action.Target == "/log" {
		p.mu.Lock()
		p.inFlight++
		p.maxInFlight = max(p.maxInFlight, p.inFlight)
		p.mu.Unlock()
		select {
		case <-time.After(p.delay):
		case <-ctx.Done():
		}
		p.mu.Lock()
		p.inFlight--
		p.logsDone++
		p.logDoneAt = time.Now()
		p.mu.Unlock()
	} else if action.Target == "/next" {
		p.mu.Lock()
		p.nextAt = started
		p.mu.Unlock()
	}
	return &protocol.Result{ActionName: action.Name, StartTime: started, Duration: time.Since(started), Success: true, StatusCode: 200}, nil
}

func backgroundRequestStart(id string) dsl.Action {
	return dsl.Action{Name: "start " + id, Type: "http_async_start", Params: map[string]any{
		"id": id, "pattern": "once",
		"request": map[string]any{"name": "POST log", "type": "request", "method": "POST", "target": "/log"},
	}}
}

// A background request (pattern once) is sent without holding up the script
// or the transaction it was started in, and is recorded under its own step
// name, outside any transaction.
func TestHTTPAsyncOnceRunsInTheBackgroundOutsideTransactions(t *testing.T) {
	proto := &backgroundProtocol{delay: 200 * time.Millisecond}
	sink := &concurrentCaptureSink{}
	vu := &VUser{ID: 1, Group: "g", Proto: proto, Metrics: NewMetricsCollector(64), ResultSink: sink, Script: &dsl.Script{
		Name: "background", Protocol: "http",
		Config: dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}},
		Actions: []dsl.Action{
			dsl.TransactionMarker("Login", true),
			backgroundRequestStart("background_1"),
			dsl.TransactionMarker("Login", false),
			// Sent once the transaction has ended: before the log returns.
			{Name: "next", Type: "request", Method: "GET", Target: "/next"},
		},
	}}
	vu.Run(context.Background())

	proto.mu.Lock()
	nextAt, logDoneAt, logs := proto.nextAt, proto.logDoneAt, proto.logsDone
	proto.mu.Unlock()
	if logs != 1 || !nextAt.Before(logDoneAt) {
		t.Fatalf("logs=%d, next at %v, log done at %v: neither the transaction nor the script may wait for the background request", logs, nextAt, logDoneAt)
	}
	var background *protocol.Result
	for _, result := range sink.results {
		if result.ActionName == "POST log" {
			background = result
		}
	}
	if background == nil || !background.Success || background.BusinessTransaction || background.ProtocolEvidence["background_request"] != true {
		t.Fatalf("background result = %+v; want it recorded under its step name, outside transactions", background)
	}
	if vu.Metrics.TotalErrors() != 0 {
		t.Fatalf("errors = %d", vu.Metrics.TotalErrors())
	}
	if vu.async != nil && len(vu.async.byID) != 0 {
		t.Fatalf("background requests left registered: %v", vu.async.order)
	}
}

// More background requests than may be in flight wait for the oldest instead
// of failing, and do not use the repeating conversations' slots.
func TestHTTPAsyncOnceWaitsForASlotPastTheLimit(t *testing.T) {
	proto := &backgroundProtocol{delay: 60 * time.Millisecond}
	actions := []dsl.Action{asyncPollStart(nil)}
	for i := range MaxBackgroundRequests + 4 {
		actions = append(actions, backgroundRequestStart(fmt.Sprintf("background_%d", i+1)))
	}
	actions = append(actions, dsl.Action{Name: "stop poll", Type: "http_async_stop", Params: map[string]any{"id": "poll"}})
	vu := &VUser{ID: 1, Group: "g", Proto: proto, Metrics: NewMetricsCollector(64), Script: &dsl.Script{
		Name: "burst", Protocol: "http",
		Config:  dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}},
		Actions: actions,
	}}
	vu.Run(context.Background())
	proto.mu.Lock()
	defer proto.mu.Unlock()
	if proto.logsDone != MaxBackgroundRequests+4 || proto.maxInFlight > MaxBackgroundRequests {
		t.Fatalf("sent %d background requests, at most %d at once; want %d sent, never more than %d in flight", proto.logsDone, proto.maxInFlight, MaxBackgroundRequests+4, MaxBackgroundRequests)
	}
	if vu.Metrics.TotalErrors() != 0 {
		t.Fatalf("errors = %d", vu.Metrics.TotalErrors())
	}
}
