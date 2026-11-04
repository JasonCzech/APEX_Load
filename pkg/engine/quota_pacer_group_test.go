package engine

import (
	"context"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// The quota belongs to the deployment: every user of a group, closed or
// open-model, draws from one pacer, so the group as a whole stays near the
// configured rate instead of each user sending the full quota.
func TestQuotaPacerIsSharedByTheWholeGroup(t *testing.T) {
	for _, tc := range []struct {
		name   string
		open   bool
		vusers int
	}{
		{name: "closed", vusers: 4},
		{name: "open", open: true, vusers: 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var llmCalls, plainCalls atomic.Int64
			server := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
				if strings.HasSuffix(r.URL.Path, "/chat/completions") {
					llmCalls.Add(1)
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":1,"total_tokens":6}}`))
					return
				}
				plainCalls.Add(1)
				w.WriteHeader(stdhttp.StatusOK)
			}))
			defer server.Close()
			script := &dsl.Script{
				Name:     "quota",
				Protocol: "http",
				Config: dsl.ScriptConfig{
					BaseURL: server.URL,
					// 60 RPM: one request per second after the first.
					Pacing: dsl.PacingConfig{Type: "none", Quota: &dsl.QuotaPacingConfig{RPM: 60}},
				},
				Actions: []dsl.Action{
					{Name: "chat", Type: "llm_request", Target: "/v1/chat/completions", Params: map[string]any{"model": "m", "prompt": "hi", "stream": false}},
					{Name: "health", Type: "GET", Method: "GET", Target: "/health"},
				},
			}
			if tc.open {
				script.Config.RunLogic.WorkloadModel = "open"
				script.Config.Pacing.Type = "tps"
				script.Config.Pacing.TPS = 20
			}
			mge, err := NewMultiGroup(MultiGroupConfig{
				Groups:   []GroupConfig{{Name: "g", Script: script, VUsers: tc.vusers}},
				Duration: 1500 * time.Millisecond,
			})
			if err != nil {
				t.Fatalf("NewMultiGroup: %v", err)
			}
			if _, err := mge.Run(context.Background()); err != nil {
				t.Fatalf("Run: %v", err)
			}
			// One shared pacer admits about 1 + 1.5 requests; one pacer per
			// user (or none for open arrivals) would admit several times that.
			if got := llmCalls.Load(); got < 1 || got > 3 {
				t.Fatalf("LLM requests = %d, want the group's shared 60 RPM (1-3 in 1.5s)", got)
			}
		})
	}
}

type quotaTxSink struct {
	mu      sync.Mutex
	txs     []*protocol.Result
	results []*protocol.Result
}

func (s *quotaTxSink) RecordResult(r *protocol.Result) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *r
	s.results = append(s.results, &cp)
	return nil
}
func (s *quotaTxSink) RecordTransaction(r *protocol.Result) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *r
	s.txs = append(s.txs, &cp)
	return nil
}

func (s *quotaTxSink) durations(name string) []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []time.Duration
	for _, tx := range s.txs {
		if tx.ActionName == name {
			out = append(out, tx.Duration)
		}
	}
	return out
}

func (s *quotaTxSink) resultDurations(name string) []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []time.Duration
	for _, r := range s.results {
		if r.ActionName == name {
			out = append(out, r.Duration)
		}
	}
	return out
}

func quotaTestServer(t *testing.T, calls *atomic.Int64) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":1,"total_tokens":6}}`))
	}))
	t.Cleanup(server.Close)
	return server
}

func quotaTestScript(baseURL string, rpm int) *dsl.Script {
	return &dsl.Script{
		Name:     "quota",
		Protocol: "http",
		Config: dsl.ScriptConfig{
			BaseURL:  baseURL,
			Pacing:   dsl.PacingConfig{Type: "none", Quota: &dsl.QuotaPacingConfig{RPM: rpm}},
			RunLogic: dsl.RunLogicConfig{AutoTransactions: "per_action"},
		},
		Actions: []dsl.Action{{Name: "chat", Type: "llm_request", Target: "/v1/chat/completions", Params: map[string]any{"model": "m", "prompt": "hi", "stream": false}}},
	}
}

// The time an action queues for quota is client-side pacing: its transaction
// is the request, not the queue.
func TestQuotaWaitIsLeftOutOfActionTransactions(t *testing.T) {
	var calls atomic.Int64
	server := quotaTestServer(t, &calls)
	sink := &quotaTxSink{}
	mge, err := NewMultiGroup(MultiGroupConfig{
		// 60 RPM: after the first request each one queues about a second.
		Groups:   []GroupConfig{{Name: "g", Script: quotaTestScript(server.URL, 60), VUsers: 1, ResultSink: sink}},
		Duration: 2500 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	if _, err := mge.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	durations := sink.durations("chat")
	if len(durations) < 2 {
		t.Fatalf("want at least two paced transactions, got %v (calls %d)", durations, calls.Load())
	}
	for _, d := range durations {
		if d > 400*time.Millisecond {
			t.Fatalf("a transaction counted the quota queue: %v", durations)
		}
	}
}

// A pause holds requests already queued for quota, and stopping the run
// releases them instead of leaving them until the hard-stop deadline.
func TestQuotaPacerPauseAndStopInARun(t *testing.T) {
	var calls atomic.Int64
	server := quotaTestServer(t, &calls)
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups:   []GroupConfig{{Name: "g", Script: quotaTestScript(server.URL, 60), VUsers: 2}},
		Duration: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = mge.Run(context.Background())
	}()
	deadline := time.Now().Add(5 * time.Second)
	for calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if err := mge.Pause(); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	mge.mu.Lock()
	paused := mge.groups[0].quotaPacer != nil && mge.groups[0].quotaPacer.paused
	mge.mu.Unlock()
	if !paused {
		t.Fatalf("pausing the run must pause the group's quota pacer")
	}
	held := calls.Load()
	time.Sleep(1500 * time.Millisecond)
	if got := calls.Load(); got != held {
		t.Fatalf("a queued request went out during the pause: %d -> %d", held, got)
	}
	stopped := time.Now()
	mge.GracefulStop()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("the run did not stop while requests were queued in a paused pacer")
	}
	if took := time.Since(stopped); took > 5*time.Second {
		t.Fatalf("stopping took %s", took)
	}
}

// A sequence's LLM hops queue for quota inside the sequence: that wait must
// not leak into the next action's transaction.
func TestQuotaWaitInASequenceStaysWithTheSequence(t *testing.T) {
	var calls atomic.Int64
	server := quotaTestServer(t, &calls)
	sink := &quotaTxSink{}
	script := quotaTestScript(server.URL, 60)
	chat := dsl.Action{Name: "hop", Type: "llm_request", Method: "POST", Target: "/v1/chat/completions", Params: map[string]any{"model": "m", "prompt": "hi", "stream": false}}
	script.Actions = []dsl.Action{
		{Name: "agent", Type: "http_sequence", Params: map[string]any{"requests": []dsl.Action{chat, chat}}},
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
	after := sink.durations("after")
	if len(after) != 1 || after[0] <= 0 || after[0] > 400*time.Millisecond {
		t.Fatalf("the action after the sequence recorded %v; the sequence's quota wait leaked into it", after)
	}
}

func TestIdleQuotaWaitCountsOnlyTimeNoRequestWasInFlight(t *testing.T) {
	base := time.Now()
	at := func(ms int) time.Time { return base.Add(time.Duration(ms) * time.Millisecond) }
	// A runs a request from 0 to 1000ms; B queues 0-1200ms and runs to
	// 1300ms: only 1000-1200ms has a child waiting and none in flight.
	children := []preparedHTTPChild{
		{started: at(0), completed: at(1000)},
		{started: at(0), completed: at(1300), quotaWait: 1200 * time.Millisecond, quotaWaits: []timeInterval{{at(0), at(1200)}}},
	}
	if got := idleQuotaWait(children); got != 200*time.Millisecond {
		t.Fatalf("idle quota wait = %v, want 200ms", got)
	}
	if got := idleQuotaWait([]preparedHTTPChild{{started: at(0), completed: at(500)}}); got != 0 {
		t.Fatalf("no quota wait, got %v", got)
	}
}
