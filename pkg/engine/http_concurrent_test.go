package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

type concurrentFixture struct {
	server                           *httptest.Server
	arrived                          chan string
	release                          chan struct{}
	active, peak, sessions, consumed atomic.Int32
	mu                               sync.Mutex
	requests                         map[string]string
	bothArrived                      chan struct{}
	secondReturned                   chan struct{}
	arrivedCount                     atomic.Int32
}

const concurrentFixtureProtocol = "concurrent-real-http-fixture"

var concurrentFixtureRegistry sync.Map
var registerConcurrentFixture sync.Once

type observedConcurrentHTTP struct {
	*httpprotocol.HTTP
	fixture *concurrentFixture
}

func (h *observedConcurrentHTTP) Init(ctx context.Context, config map[string]any) error {
	if f, ok := concurrentFixtureRegistry.Load(config["base_url"]); ok {
		h.fixture = f.(*concurrentFixture)
	}
	return h.HTTP.Init(ctx, config)
}
func (h *observedConcurrentHTTP) Execute(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
	result, err := h.HTTP.Execute(ctx, action)
	if action.Name == "Second" && h.fixture != nil {
		select {
		case <-h.fixture.secondReturned:
		default:
			close(h.fixture.secondReturned)
		}
	}
	return result, err
}

func registerConcurrentHTTPFixture() {
	registerConcurrentFixture.Do(func() {
		protocol.Register(concurrentFixtureProtocol, func() protocol.Protocol { return &observedConcurrentHTTP{HTTP: &httpprotocol.HTTP{}} })
	})
}

func newConcurrentFixture(t *testing.T) *concurrentFixture {
	t.Helper()
	registerConcurrentHTTPFixture()
	f := &concurrentFixture{arrived: make(chan string, 8), release: make(chan struct{}), requests: make(map[string]string), bothArrived: make(chan struct{}), secondReturned: make(chan struct{})}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			n := f.sessions.Add(1)
			http.SetCookie(w, &http.Cookie{Name: "session", Value: fmt.Sprint(n), Path: "/"})
			io.WriteString(w, `{"ok":true}`)
			return
		}
		cookie, err := r.Cookie("session")
		if err != nil {
			w.WriteHeader(401)
			return
		}
		if r.URL.Path == "/consume" {
			warm, err := r.Cookie("warm")
			body, _ := io.ReadAll(r.Body)
			if err != nil || warm.Value != "yes" || string(body) != "first/second" {
				w.WriteHeader(409)
				return
			}
			f.consumed.Add(1)
			io.WriteString(w, "ok")
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/child/") {
			w.WriteHeader(404)
			return
		}
		current := f.active.Add(1)
		defer f.active.Add(-1)
		for {
			peak := f.peak.Load()
			if current <= peak || f.peak.CompareAndSwap(peak, current) {
				break
			}
		}
		name := strings.TrimPrefix(r.URL.Path, "/child/")
		f.mu.Lock()
		f.requests[cookie.Value+"/"+name] = r.URL.Query().Get("order")
		f.mu.Unlock()
		f.arrived <- name
		if f.arrivedCount.Add(1) == 2 {
			close(f.bothArrived)
		}
		select {
		case <-f.bothArrived:
		case <-r.Context().Done():
			return
		}
		if name == "first" {
			select {
			case <-f.release:
			case <-r.Context().Done():
				return
			}
		}
		if name == "second" {
			http.SetCookie(w, &http.Cookie{Name: "warm", Value: "yes", Path: "/"})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"value": name})
	}))
	concurrentFixtureRegistry.Store(f.server.URL, f)
	t.Cleanup(func() { concurrentFixtureRegistry.Delete(f.server.URL) })
	t.Cleanup(func() {
		select {
		case <-f.release:
		default:
			close(f.release)
		}
		f.server.Close()
	})
	return f
}
func concurrentActions() (dsl.Action, dsl.Action, dsl.Action) {
	login := dsl.Action{Name: "Login", Type: "GET", Target: "/login"}
	first := dsl.Action{Name: "First", Type: "request", Method: "GET", Target: "/child/first?order={{param.order}}", Extractors: []dsl.Extractor{{Name: "first_value", Source: "body", Type: "jsonpath", Expression: "$.value", OnMiss: "fail"}}}
	second := dsl.Action{Name: "Second", Type: "GET", Target: "/child/second?order={{param.order}}", Extractors: []dsl.Extractor{{Name: "second_value", Source: "body", Type: "jsonpath", Expression: "$.value", OnMiss: "fail"}}}
	group := dsl.Action{Name: "Concurrent fetches", Type: "http_sequence", Params: map[string]any{"execution_mode": "concurrent", "max_concurrency": 2, "requests": []dsl.Action{first, second}}}
	consume := dsl.Action{Name: "Consume", Type: "POST", Target: "/consume", Body: "{{var.first_value}}/{{var.second_value}}", Assertions: []dsl.Assertion{{Type: "status_code", Expected: 200}}}
	return login, group, consume
}
func newConcurrentVU(t *testing.T, base string) *VUser {
	t.Helper()
	registerConcurrentHTTPFixture()
	p := &observedConcurrentHTTP{HTTP: &httpprotocol.HTTP{}}
	if err := p.Init(context.Background(), map[string]any{"base_url": base}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Teardown(context.Background()) })
	script := &dsl.Script{Name: "Concurrent fixture", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: base}, Parameters: []dsl.Parameter{{Name: "order", Type: "counter", Update: "each_occurrence", Start: 10, Step: 1}}}
	vu := &VUser{Proto: p, Script: script, Variables: make(map[string]string), Metrics: NewMetricsCollector(32), SnapshotStore: NewSnapshotStore(8)}
	vu.Parameters = NewParameterRuntime(script.Parameters, nil, 0)
	vu.Parameters.Prime()
	return vu
}
func waitConcurrentArrival(t *testing.T, f *concurrentFixture) {
	t.Helper()
	for i := 0; i < 2; i++ {
		select {
		case <-f.arrived:
		case <-time.After(2 * time.Second):
			t.Fatal("HTTP children did not overlap")
		}
	}
}
func TestConcurrentHTTPParentSessionJoinAndSourceOrder(t *testing.T) {
	f := newConcurrentFixture(t)
	vu := newConcurrentVU(t, f.server.URL)
	login, group, consume := concurrentActions()
	r, err := vu.executeWithRetry(context.Background(), vu.resolveAction(login), dsl.ErrorHandlingConfig{})
	if err != nil || !r.Success {
		t.Fatal("login failed")
	}
	done := make(chan *protocol.Result, 1)
	go func() {
		r, _ := vu.executeHTTPSequenceAction(context.Background(), group, dsl.ErrorHandlingConfig{}, nil)
		done <- r
	}()
	waitConcurrentArrival(t, f)
	select {
	case <-done:
		t.Fatal("group returned before held sibling joined")
	default:
	}
	if vu.Variables["second_value"] != "" {
		t.Fatal("sibling extraction published before join")
	}
	select {
	case <-f.secondReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("second HTTP result did not finish")
	}
	close(f.release)
	select {
	case r := <-done:
		if !r.Success || r.Duration <= 0 {
			t.Fatalf("group success=%t", r.Success)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("group join did not finish")
	}
	if f.peak.Load() != 2 || vu.Variables["first_value"] != "first" || vu.Variables["second_value"] != "second" {
		t.Fatal("session overlap or ordered extraction failed")
	}
	f.mu.Lock()
	requests := map[string]string{}
	for k, v := range f.requests {
		requests[k] = v
	}
	f.mu.Unlock()
	if requests["1/first"] != "10" || requests["1/second"] != "11" {
		t.Fatalf("each-occurrence source order changed: %+v", requests)
	}
	result, err := vu.executeWithRetry(context.Background(), vu.resolveAction(consume), dsl.ErrorHandlingConfig{})
	if err != nil || !result.Success || f.consumed.Load() != 1 {
		t.Fatal("post-join cookie/extractor consumer failed")
	}
	isolated := newConcurrentVU(t, f.server.URL)
	without, err := isolated.executeWithRetry(context.Background(), isolated.resolveAction(dsl.Action{Name: "No inherited login", Type: "GET", Target: "/child/second"}), dsl.ErrorHandlingConfig{})
	if err != nil || without.StatusCode != 401 {
		t.Fatal("HTTP session leaked to another VUser")
	}
}

type concurrentBoundaryRecorder struct{ before, after []string }

func (d *concurrentBoundaryRecorder) BeforeAction(_ context.Context, a ReplayDebugAction) error {
	d.before = append(d.before, a.ActionName)
	return nil
}
func (d *concurrentBoundaryRecorder) AfterAction(_ context.Context, a ReplayDebugAction, _ *ReplayActionEvent) error {
	d.after = append(d.after, a.ActionName)
	return nil
}
func TestConcurrentHTTPReplayUsesSameJoinAndOuterDebuggerBoundary(t *testing.T) {
	f := newConcurrentFixture(t)
	login, group, consume := concurrentActions()
	children := group.Params["requests"].([]dsl.Action)
	group.Params["requests"] = append(children, dsl.Action{Name: "Third", Type: "GET", Target: "/child/third"})
	debugger := &concurrentBoundaryRecorder{}
	script := &dsl.Script{Name: "Concurrent replay", Protocol: concurrentFixtureProtocol, Config: dsl.ScriptConfig{BaseURL: f.server.URL}, Parameters: []dsl.Parameter{{Name: "order", Type: "counter", Update: "each_occurrence", Start: 10, Step: 1}}, Actions: []dsl.Action{login, group, consume}}
	done := make(chan *ReplayReport, 1)
	go func() {
		report, _ := ReplayScript(context.Background(), script, ReplayOptions{DebugController: debugger, IgnoreThinkTime: true})
		done <- report
	}()
	waitConcurrentArrival(t, f)
	select {
	case <-done:
		t.Fatal("replay passed group barrier early")
	default:
	}
	// First remains held, so only Second's worker can start queued Third.
	// Its arrival proves Second's completed timestamp was recorded, including
	// result classification and projection, before First is released.
	select {
	case name := <-f.arrived:
		if name != "third" {
			t.Fatalf("queued completion witness = %q, want third", name)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second worker did not advance to queued completion witness")
	}
	close(f.release)
	report := <-done
	if report == nil || !report.Success || len(report.Events) != 5 || f.consumed.Load() != 1 || f.peak.Load() != 2 {
		t.Fatal("real concurrent replay failed")
	}
	if !reflect.DeepEqual(debugger.before, []string{"Login", "Concurrent fetches", "Consume"}) || !reflect.DeepEqual(debugger.before, debugger.after) {
		t.Fatalf("debugger entered child boundaries: %v/%v", debugger.before, debugger.after)
	}
	first, second, third := report.Events[1], report.Events[2], report.Events[3]
	if first.RequestIndex == nil || *first.RequestIndex != 0 || second.RequestIndex == nil || *second.RequestIndex != 1 || first.ParentActionName != "Concurrent fetches" || first.StepPath[0] != "requests[0]" || second.StepPath[0] != "requests[1]" {
		t.Fatal("concurrent leaf source identity changed")
	}
	if third.RequestIndex == nil || *third.RequestIndex != 2 || third.RequestCount != 3 || third.ParentActionName != "Concurrent fetches" || len(third.StepPath) == 0 || third.StepPath[0] != "requests[2]" || third.ActionName != "Third" {
		t.Fatal("queued completion witness source identity changed")
	}
	if !first.StartedAt.Before(second.CompletedAt) || !second.StartedAt.Before(first.CompletedAt) || !second.CompletedAt.Before(first.CompletedAt) || first.DurationMs <= 0 || second.DurationMs <= 0 {
		t.Fatal("leaf timing lost actual overlap/inverted completion")
	}
	duration, ok := first.ProtocolEvidence["concurrent_group_duration_ms"].(float64)
	if !ok || duration < first.DurationMs {
		t.Fatal("wall-clock group timing missing")
	}
}
func TestConcurrentHTTPRejectsWholeGroupBeforeTraffic(t *testing.T) {
	var traffic atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { traffic.Add(1); io.WriteString(w, `{"items":["a"]}`) }))
	defer server.Close()
	for _, tc := range []struct {
		name string
		edit func(*dsl.Action, *VUser)
	}{
		{"duplicate names", func(g *dsl.Action, v *VUser) {
			children := g.Params["requests"].([]dsl.Action)
			children[1].Extractors = children[0].Extractors
			g.Params["requests"] = children
		}},
		{"base dependency stale", func(g *dsl.Action, v *VUser) {
			c := g.Params["requests"].([]dsl.Action)
			c[1].Body = "{{var.first_value}}"
			v.Variables["first_value"] = "stale"
			g.Params["requests"] = c
		}},
		{"all count dependency stale", func(g *dsl.Action, v *VUser) {
			c := g.Params["requests"].([]dsl.Action)
			c[0].Extractors[0].Mode = "all"
			c[1].Body = "{{var.first_value_count}}"
			v.Variables["first_value_count"] = "1"
			g.Params["requests"] = c
		}},
		{"all index dependency stale", func(g *dsl.Action, v *VUser) {
			c := g.Params["requests"].([]dsl.Action)
			c[0].Extractors[0].Mode = "all"
			c[1].Body = "{{var.first_value_1}}"
			v.Variables["first_value_1"] = "old"
			g.Params["requests"] = c
		}},
		{"nonzero child wait", func(g *dsl.Action, v *VUser) {
			c := g.Params["requests"].([]dsl.Action)
			c[1].ThinkTime = dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 1}
			g.Params["requests"] = c
		}},
		{"decoded codec stale dependency", func(g *dsl.Action, v *VUser) {
			c := g.Params["requests"].([]dsl.Action)
			c[1].Body = `{"actions":[{"id":"synthetic;a","descriptor":"synthetic","params":{"payload":"{\"recordId\":\"\\u007b\\u007bvar.first_value\\u007d\\u007d\"}"}}]}`
			c[1].Metadata = map[string]any{dsl.ImportURLFormTemplateValueEncodingMetadataKey: dsl.ImportAuraJSONTemplateValueEncoding}
			v.Variables["first_value"] = "stale"
			g.Params["requests"] = c
		}},
		{"malformed selected codec", func(g *dsl.Action, v *VUser) {
			c := g.Params["requests"].([]dsl.Action)
			c[1].Body = `{"actions":`
			c[1].Metadata = map[string]any{dsl.ImportURLFormTemplateValueEncodingMetadataKey: dsl.ImportAuraJSONTemplateValueEncoding}
			g.Params["requests"] = c
		}},
		{"bad second target", func(g *dsl.Action, v *VUser) {
			c := g.Params["requests"].([]dsl.Action)
			c[1].Target = "http://%invalid"
			g.Params["requests"] = c
		}},
		{"invalid resolved sibling auth", func(g *dsl.Action, v *VUser) {
			c := g.Params["requests"].([]dsl.Action)
			c[1].Auth = &dsl.AuthConfig{Type: "bearer", Token: "{{var.auth_token}}"}
			v.Variables["auth_token"] = "synthetic\r\ninvalid"
			g.Params["requests"] = c
		}},
		{"bad second encoding", func(g *dsl.Action, v *VUser) {
			c := g.Params["requests"].([]dsl.Action)
			c[1].Body = "not-base64"
			c[1].BodyEncoding = "base64"
			g.Params["requests"] = c
		}},
		{"invalid UTF8 source", func(g *dsl.Action, v *VUser) {
			c := g.Params["requests"].([]dsl.Action)
			c[1].Body = string([]byte{0xff})
			g.Params["requests"] = c
		}},
		{"oversized prepared expansion", func(g *dsl.Action, v *VUser) {
			c := g.Params["requests"].([]dsl.Action)
			c[1].Body = strings.Repeat("{{var.large}}", 3)
			v.Variables["large"] = strings.Repeat("x", 8<<20)
			g.Params["requests"] = c
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vu := newConcurrentVU(t, server.URL)
			_, g, _ := concurrentActions()
			tc.edit(&g, vu)
			before := traffic.Load()
			result, err := vu.executeHTTPSequenceAction(context.Background(), g, dsl.ErrorHandlingConfig{}, nil)
			if err != nil || result.Success || traffic.Load() != before {
				t.Fatalf("invalid group sent traffic: success=%t err=%v", result.Success, err)
			}
		})
	}
}

// An unresolved variable reaches the server literally in a concurrent group,
// exactly as it does when the same request runs on its own.
func TestConcurrentHTTPUnresolvedVariableMatchesSequential(t *testing.T) {
	var mu sync.Mutex
	bodies := map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies[r.URL.Path] = string(body)
		mu.Unlock()
		io.WriteString(w, `{"value":"v"}`)
	}))
	defer server.Close()
	vu := newConcurrentVU(t, server.URL)
	_, group, _ := concurrentActions()
	children := group.Params["requests"].([]dsl.Action)
	children[1].Type, children[1].Method, children[1].Body = "POST", "POST", "{{var.absent}}"
	group.Params["requests"] = children
	result, err := vu.executeHTTPSequenceAction(context.Background(), group, dsl.ErrorHandlingConfig{}, nil)
	if err != nil || !result.Success {
		t.Fatalf("group with an unresolved variable failed: success=%t err=%v", result.Success, err)
	}
	sequential := children[1]
	sequential.Target = "/sequential"
	if r, err := vu.executeWithRetry(context.Background(), vu.resolveAction(sequential), dsl.ErrorHandlingConfig{}); err != nil || !r.Success {
		t.Fatalf("sequential request failed: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if bodies["/child/second"] != "{{var.absent}}" || bodies["/sequential"] != bodies["/child/second"] {
		t.Fatalf("concurrent body %q, sequential body %q", bodies["/child/second"], bodies["/sequential"])
	}
}

func TestConcurrentHTTPRecordsPerRequestTransactions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "second") {
			w.WriteHeader(http.StatusInternalServerError)
		}
		io.WriteString(w, `{"value":"v"}`)
	}))
	defer server.Close()
	for _, tc := range []struct {
		name            string
		named           bool
		maxTransactions int64
		want            string
	}{
		{"each request records its own", true, 0, "first.json:true,second.woff2:false"},
		{"transaction goal counts each request", true, 1, "first.json:true"},
		{"unnamed group records its aggregate", false, 0, "Concurrent fetches:false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runLogic := dsl.RunLogicConfig{Iterations: 1, AutoTransactions: "none", MaxTransactions: tc.maxTransactions}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			vu := newConcurrentVU(t, server.URL)
			_, group, _ := concurrentActions()
			if tc.named {
				children := group.Params["requests"].([]dsl.Action)
				children[0].Metadata = map[string]any{dsl.HTTPConcurrentRequestTransactionMetadataKey: "first.json"}
				children[1].Metadata = map[string]any{dsl.HTTPConcurrentRequestTransactionMetadataKey: "second.woff2"}
				group.Params["requests"] = children
			}
			sink := &sequenceCaptureSink{}
			vu.ResultSink = sink
			vu.ExecutionGoals = NewExecutionGoalLimiter(runLogic, cancel)
			vu.HardStopCtx = context.Background()
			vu.ActionBoundaryCtx = ctx
			vu.Script.Config.RunLogic = runLogic
			vu.Script.Config.ErrorHandling.ContinueOnError = dsl.ContinueOnErrorValue(true)
			vu.Script.Actions = []dsl.Action{group}
			vu.Run(ctx)
			got := make([]string, 0, len(sink.transactions))
			for _, transaction := range sink.transactions {
				got = append(got, fmt.Sprintf("%s:%t", transaction.ActionName, transaction.Success))
			}
			if strings.Join(got, ",") != tc.want {
				t.Fatalf("transactions = %v, want %s", got, tc.want)
			}
		})
	}
}

func TestConcurrentHTTPCancellationJoinsInflightRequests(t *testing.T) {
	f := newConcurrentFixture(t)
	vu := newConcurrentVU(t, f.server.URL)
	login, g, _ := concurrentActions()
	vu.executeWithRetry(context.Background(), vu.resolveAction(login), dsl.ErrorHandlingConfig{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := vu.executeHTTPSequenceAction(ctx, g, dsl.ErrorHandlingConfig{}, nil); done <- err }()
	waitConcurrentArrival(t, f)
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("cancellation not retained: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled group did not join")
	}
	deadline := time.Now().Add(time.Second)
	for f.active.Load() != 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if f.active.Load() != 0 {
		t.Fatal("HTTP request survived cancellation/join")
	}
}
func TestConcurrentHTTPAllowedTargetPolicyRejectsMixedGroup(t *testing.T) {
	var traffic atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { traffic.Add(1); io.WriteString(w, "ok") }))
	defer s.Close()
	u, _ := url.Parse(s.URL)
	p := &httpprotocol.HTTP{}
	if err := p.Init(context.Background(), map[string]any{"base_url": s.URL, protocol.TargetPolicyConfigKey: []string{u.Hostname()}}); err != nil {
		t.Fatal(err)
	}
	defer p.Teardown(context.Background())
	vu := &VUser{Proto: p, Script: &dsl.Script{Protocol: "http"}, Metrics: NewMetricsCollector(8), Variables: map[string]string{}}
	group := dsl.Action{Name: "Policy group", Type: "http_sequence", Params: map[string]any{"execution_mode": "concurrent", "requests": []dsl.Action{{Type: "GET", Target: s.URL}, {Type: "GET", Target: "http://not-allowed.invalid/path"}}}}
	result, err := vu.executeHTTPSequenceAction(context.Background(), group, dsl.ErrorHandlingConfig{}, nil)
	if err != nil || result.Success || traffic.Load() != 0 {
		t.Fatal("mixed forbidden group sent allowed sibling traffic")
	}
}
func TestConcurrentHTTPExpansionIsBoundedBeforeLargeAllocation(t *testing.T) {
	vu := newConcurrentVU(t, "http://127.0.0.1:1")
	vu.Variables["large"] = strings.Repeat("x", 8<<20)
	group := dsl.Action{Name: "Large expansion", Type: "http_sequence", Params: map[string]any{"execution_mode": "concurrent", "requests": []dsl.Action{{Type: "POST", Target: "/", Body: strings.Repeat("{{var.large}}", 32)}}}}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	r, err := vu.executeHTTPSequenceAction(context.Background(), group, dsl.ErrorHandlingConfig{}, nil)
	runtime.ReadMemStats(&after)
	if err != nil || r.Success || after.TotalAlloc-before.TotalAlloc > 4<<20 {
		t.Fatalf("expanded request allocation was not bounded: %d", after.TotalAlloc-before.TotalAlloc)
	}
}

type concurrentCaptureSink struct{ results []*protocol.Result }

func (s *concurrentCaptureSink) RecordResult(result *protocol.Result) error {
	copy := *result
	s.results = append(s.results, &copy)
	return nil
}

func TestHTTPSequenceRawSensitiveExtractionSurvivesPublication(t *testing.T) {
	for _, mode := range []string{"sequential", "concurrent"} {
		t.Run(mode, func(t *testing.T) {
			token := "synthetic-sequence-session-credential"
			var accepted atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/token" {
					json.NewEncoder(w).Encode(map[string]string{"value": token})
					return
				}
				if r.Header.Get("X-Session") == token {
					accepted.Add(1)
					io.WriteString(w, "ok")
				} else {
					w.WriteHeader(409)
				}
			}))
			defer server.Close()
			vu := newConcurrentVU(t, server.URL)
			sink := &concurrentCaptureSink{}
			vu.ResultSink = sink
			child := dsl.Action{Name: "Extract session", Type: "GET", Target: "/token", Extractors: []dsl.Extractor{{Name: "session_token", Source: "body", Type: "jsonpath", Expression: "$.value", OnMiss: "fail"}}}
			group := dsl.Action{Name: "Group", Type: "http_sequence", Params: map[string]any{"execution_mode": mode, "requests": []dsl.Action{child}}}
			result, err := vu.executeHTTPSequenceAction(context.Background(), group, dsl.ErrorHandlingConfig{}, nil)
			if err != nil || !result.Success {
				t.Fatal("group failed")
			}
			vu.recordResult(result, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
			if vu.Variables["session_token"] != token {
				t.Fatal("group overwrote raw runtime extraction")
			}
			consume := dsl.Action{Name: "Consume token", Type: "GET", Target: "/consume", Headers: map[string]string{"X-Session": "{{var.session_token}}"}}
			r, err := vu.executeWithRetry(context.Background(), vu.resolveAction(consume), dsl.ErrorHandlingConfig{})
			if err != nil || !r.Success || accepted.Load() != 1 {
				t.Fatal("raw token not consumed")
			}
			if len(sink.results) != 2 {
				t.Fatal("leaf and aggregate publication missing")
			}
			for _, r := range sink.results {
				encoded, _ := json.Marshal(r)
				if strings.Contains(string(encoded), token) || r.Extractions["session_token"] == token {
					t.Fatal("publication retained raw token")
				}
			}
		})
	}
}
func TestConcurrentHTTPCompletedEvidenceHasGroupCeiling(t *testing.T) {
	const count = 12
	payload := strings.Repeat("x", 2<<20)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, payload) }))
	defer server.Close()
	vu := newConcurrentVU(t, server.URL)
	// Real protocol capture exercises failure snapshots and the private exchange.
	if err := vu.Proto.Init(context.Background(), map[string]any{"base_url": server.URL, protocol.CorrelationEvidenceCaptureConfigKey: true}); err != nil {
		t.Fatal(err)
	}
	children := make([]dsl.Action, count)
	for i := range children {
		children[i] = dsl.Action{Name: fmt.Sprintf("Body %d", i+1), Type: "GET", Target: "/body", Assertions: []dsl.Assertion{{Type: "status_code", Expected: 201}}}
	}
	group, err := vu.runConcurrentHTTP(context.Background(), dsl.Action{Name: "Bounded evidence", Type: "http_sequence", Params: map[string]any{"execution_mode": "concurrent", "max_concurrency": 1, "requests": children}}, dsl.ErrorHandlingConfig{})
	if err != nil || len(group.children) != count {
		t.Fatal("group failed")
	}
	bodyBytes, snapshotBytes := 0, 0
	for _, child := range group.children {
		r := child.result
		if r == nil || r.Success || r.StatusCode != 200 {
			t.Fatal("assertion result lost")
		}
		bodyBytes += len(r.ResponseBody)
		if r.Snapshot != nil {
			snapshotBytes += len(r.Snapshot.ResponseBody) + len(r.Snapshot.RequestBody)
		}
		if r.CorrelationEvidence == nil || r.CorrelationEvidence.UnavailableReason != concurrentCorrelationOmitted || r.CorrelationEvidence.ResponseBody != "" {
			t.Fatal("private capture omission absent")
		}
		if r.ProtocolEvidence["concurrent_group_evidence_details_complete"] != false || r.ProtocolEvidence["concurrent_group_response_bytes_omitted"].(float64) <= 0 {
			t.Fatal("retention omissions hidden")
		}
	}
	if bodyBytes+snapshotBytes > maxConcurrentRichEvidenceBytes {
		t.Fatal("completed graph exceeded group evidence bound")
	}
}
func TestConcurrentHTTPGroupAttributionAndRetry(t *testing.T) {
	var first atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/retry" && first.Add(1) == 1 {
			w.WriteHeader(503)
			return
		}
		io.WriteString(w, "ok")
	}))
	defer server.Close()
	vu := newConcurrentVU(t, server.URL)
	vu.Variables["page"] = "Wrapper page"
	children := []dsl.Action{{Name: "Retry leaf", Type: "GET", Target: "/retry", Attribution: &dsl.AnalyticsAttribution{HierarchyPath: "Leaf hierarchy", PageName: "Leaf page"}}, {Name: "Other leaf", Type: "GET", Target: "/other"}}
	group := dsl.Action{Name: "Attributed group", Type: "http_sequence", Attribution: &dsl.AnalyticsAttribution{HierarchyPath: "Wrapper hierarchy", PageName: "{{var.page}}"}, Params: map[string]any{"execution_mode": "concurrent", "requests": children}}
	sink := &concurrentCaptureSink{}
	vu.ResultSink = sink
	r, err := vu.executeHTTPSequenceAction(context.Background(), group, dsl.ErrorHandlingConfig{MaxRetries: 1, RetryDelayMs: 1}, nil)
	if err != nil || !r.Success || first.Load() != 2 {
		t.Fatal("joined retry failed")
	}
	if r.Hierarchy != "Wrapper hierarchy" || r.PageName != "Wrapper page" {
		t.Fatal("wrapper attribution overwritten")
	}
	if len(sink.results) != 2 || sink.results[0].Hierarchy != "Leaf hierarchy" {
		t.Fatal("leaf attribution lost")
	}
}
func TestConcurrentHTTPInheritedPercentageWaitRejectsBeforeTraffic(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer server.Close()
	vu := newConcurrentVU(t, server.URL)
	vu.Script.Config.ThinkTime = dsl.ThinkTimeConfig{Type: "random_percentage", MinPercent: 50, MaxPercent: 100}
	child := dsl.Action{Name: "Waiting leaf", Type: "GET", Target: "/", ThinkTimeAfter: true}
	r, err := vu.executeHTTPSequenceAction(context.Background(), dsl.Action{Name: "Group", Type: "http_sequence", Params: map[string]any{"execution_mode": "concurrent", "requests": []dsl.Action{child}}}, dsl.ErrorHandlingConfig{}, nil)
	if err != nil || r.Success || requests.Load() != 0 {
		t.Fatal("inherited child percentage wait silently ignored")
	}
}

func TestConcurrentHTTPCompletedEvidenceCompletenessUsesFinalBounds(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*protocol.Result)
	}{
		{"carried unavailable", func(r *protocol.Result) {
			r.CorrelationEvidence = protocol.UnavailableCorrelationEvidence("capture unavailable")
		}},
		{"long title", func(r *protocol.Result) { r.PageTitle = strings.Repeat("x", 3<<10) }},
		{"long URL", func(r *protocol.Result) { r.PageURL = strings.Repeat("x", 9<<10) }},
		{"snapshot per-field", func(r *protocol.Result) {
			r.Snapshot = &protocol.ErrorSnapshot{ResponseBody: strings.Repeat("x", 2<<20)}
		}},
		{"snapshot header cap", func(r *protocol.Result) {
			r.Snapshot = &protocol.ErrorSnapshot{RequestHeaders: map[string]string{"X-Large": strings.Repeat("x", 20<<10)}}
		}},
		{"network entry field", func(r *protocol.Result) {
			r.NetworkEntries = []protocol.NetworkEntry{{URL: strings.Repeat("x", 20<<10)}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &protocol.Result{Success: true, ResponseBody: "safe"}
			tc.edit(r)
			_, err := boundConcurrentCompletedResult(r, 1, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !r.Success || r.ProtocolEvidence["concurrent_group_evidence_details_complete"] != false {
				t.Fatal("incomplete retained evidence reported complete")
			}
			if tc.name == "carried unavailable" && r.ProtocolEvidence["concurrent_group_correlation_omitted"] != true {
				t.Fatal("carried unavailable capture hidden")
			}
		})
	}
	r := &protocol.Result{Success: true, ResponseBody: "safe", CorrelationEvidence: &protocol.CorrelationEvidence{RequestURL: "http://localhost/", ResponseBody: "safe"}}
	_, err := boundConcurrentCompletedResult(r, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.ProtocolEvidence["concurrent_group_evidence_details_complete"] != true || r.CorrelationEvidence.ResponseBody != "safe" {
		t.Fatal("small complete exchange lost")
	}
}
func TestConcurrentHTTPInterpolationDoesNotPreallocateUnadmittedPair(t *testing.T) {
	vu := &VUser{Variables: map[string]string{"small": "ok"}}
	// Earlier prepared children consumed the budget. This later scalar fits once
	// but its actual+redacted pair cannot fit, and must reject before either Grow.
	text := strings.Repeat("x", 1<<20) + "{{var.small}}"
	vu.concurrentPreparation = &httpPreparationBudget{bytesRemaining: len(text) + len(text)/2, workRemaining: 64 << 20, templatesRemaining: 4096}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, _, err := vu.interpolateWithSecretsTransformBounded(text, nil, vu.concurrentPreparation.bytesRemaining, &vu.concurrentPreparation.templatesRemaining)
	runtime.ReadMemStats(&after)
	if err == nil || after.TotalAlloc-before.TotalAlloc > 1<<20 {
		t.Fatal("unadmitted scalar pair allocated source capacities")
	}
}

func TestConcurrentHTTPAggregateExtractionLimitsMatchLoadAndReplay(t *testing.T) {
	newConcurrentFixture(t) // Register the real HTTP fixture protocol once.
	value := strings.Repeat("x", protocol.MaxExtractionValueBytes)
	for _, count := range []int{3, 5} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			var received atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				received.Add(1)
				json.NewEncoder(w).Encode(map[string]string{"value": value})
			}))
			defer server.Close()
			children := make([]dsl.Action, count)
			for i := range children {
				children[i] = dsl.Action{Name: fmt.Sprintf("Leaf %d", i+1), Type: "GET", Target: "/value", Extractors: []dsl.Extractor{{Name: fmt.Sprintf("output_%d", i+1), Source: "body", Type: "jsonpath", Expression: "$.value", OnMiss: "fail"}}}
			}
			action := dsl.Action{Name: "Bounded outputs", Type: "http_sequence", Params: map[string]any{"execution_mode": "concurrent", "requests": children}}
			vu := newConcurrentVU(t, server.URL)
			sink := &concurrentCaptureSink{}
			vu.ResultSink = sink
			load, err := vu.executeHTTPSequenceAction(context.Background(), action, dsl.ErrorHandlingConfig{}, nil)
			expectedSuccess := count == 3
			if err != nil || load.Success != expectedSuccess || len(sink.results) != count || received.Load() != int32(count) {
				t.Fatal("load output admission or joined leaf evidence incorrect")
			}
			if expectedSuccess {
				if len(load.Extractions) != count {
					t.Fatal("accepted combined outputs missing")
				}
			} else if load.Extractions != nil || load.ErrorMessage != "concurrent HTTP extraction retention failed" {
				t.Fatal("failed aggregate retained partial combined outputs")
			}
			script := &dsl.Script{Name: "Replay output bounds", Protocol: concurrentFixtureProtocol, Config: dsl.ScriptConfig{BaseURL: server.URL}, Actions: []dsl.Action{action}}
			replay, err := ReplayScript(context.Background(), script, ReplayOptions{IgnoreThinkTime: true})
			if err != nil || replay.Success != expectedSuccess || len(replay.Events) != count || received.Load() != int32(2*count) {
				t.Fatal("replay output admission differs from load")
			}
			if !expectedSuccess && replay.Error != "concurrent HTTP extraction retention failed" {
				t.Fatal("replay aggregate omission hidden")
			}
			for index, event := range replay.Events {
				if !event.Success || event.RequestIndex == nil || *event.RequestIndex != index {
					t.Fatal("real successful leaf outcome lost while aggregate failed")
				}
				if expectedSuccess && len(event.Extractions) != 1 {
					t.Fatal("accepted leaf output evidence lost")
				}
				if !expectedSuccess && (len(event.Extractions) != 0 || event.ProtocolEvidence["concurrent_group_extractions_omitted"] != true) {
					t.Fatal("failed joined admission published potentially incomplete extraction evidence")
				}
			}
		})
	}
}

func TestConcurrentHTTPCompletedEvidenceScrubsLongSecretsBeforeBounds(t *testing.T) {
	secret := strings.Repeat("q", 24<<10)
	config, err := WithSecretValueRedactionChecked(nil, []string{secret})
	if err != nil {
		t.Fatal(err)
	}
	for _, count := range []int{1, dsl.MaxHTTPConcurrentChildren} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			body := strings.Repeat("p", 8<<10) + secret
			r := &protocol.Result{
				Success: false, StatusCode: 200, ActionName: "Long secret leaf",
				ErrorMessage: "error-" + secret, CorrelationID: "correlation-" + secret,
				ResponseBody: body, Extractions: map[string]string{"ordinary": "exact-runtime-output"},
				ProtocolEvidence:    map[string]any{"ordinary": "evidence-" + secret},
				Snapshot:            &protocol.ErrorSnapshot{ResponseBody: body, RequestHeaders: map[string]string{"X-Ordinary": "header-" + secret}},
				CorrelationEvidence: &protocol.CorrelationEvidence{ResponseBody: body},
			}
			used, err := boundConcurrentCompletedResult(r, count, config)
			if err != nil {
				t.Fatal(err)
			}
			if r.Extractions["ordinary"] != "exact-runtime-output" || r.CorrelationEvidence.ResponseBody != body {
				t.Fatal("public projection changed exact runtime/private state")
			}
			if r.ResponseBody != strings.Repeat("p", 8<<10)+redactedPayloadValue || r.Snapshot == nil || r.Snapshot.RequestHeaders["X-Ordinary"] != "header-"+redactedPayloadValue {
				t.Fatal("known full secret was bounded before masking")
			}
			sink := &captureResultSink{}
			vu := &VUser{Metrics: NewMetricsCollector(2), Variables: map[string]string{}, ResultSink: sink, RedactionConfig: used}
			vu.recordResult(r, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
			encoded, err := json.Marshal(sink.result)
			if err != nil || strings.Contains(string(encoded), strings.Repeat("q", 128)) || !strings.Contains(string(encoded), redactedPayloadValue) {
				t.Fatal("published evidence retained a long-secret prefix")
			}
		})
	}
}

func TestConcurrentHTTPFreshSensitiveOutputsProtectEarlierSiblingsAndRemainExact(t *testing.T) {
	const count = dsl.MaxHTTPConcurrentChildren
	token := strings.Repeat("z", protocol.MaxExtractionValueBytes)
	padding := strings.Repeat("p", 12<<10)
	var consumed atomic.Int32
	var requestReplicasExact atomic.Int32
	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(previousLogger)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/early":
			body, _ := io.ReadAll(r.Body)
			if r.URL.Query().Get("ordinary") == token && r.Header.Get("X-Echo") == token && strings.Contains(string(body), token) {
				requestReplicasExact.Add(1)
			}
			io.WriteString(w, token)
		case "/early-large":
			io.WriteString(w, padding+token)
		case "/producer":
			json.NewEncoder(w).Encode(map[string]string{"ordinary": token, "padding": padding})
		case "/consume":
			if r.Header.Get("X-Session") != token || r.Header.Get("X-Ordinary") != token {
				w.WriteHeader(409)
				return
			}
			consumed.Add(1)
			io.WriteString(w, "ok")
		default:
			io.WriteString(w, "ok")
		}
	}))
	defer server.Close()
	children := make([]dsl.Action, count)
	for i := range children {
		children[i] = dsl.Action{Name: fmt.Sprintf("Leaf %d", i+1), Type: "GET", Target: "/ordinary"}
	}
	children[0].Target, children[1].Target = "/early", "/early-large"
	children[0].Type, children[0].Target, children[0].Body, children[0].Headers = "POST", "/early?ordinary="+url.QueryEscape(token), `{"ordinary":"`+token+`"}`, map[string]string{"X-Echo": token}
	children[0].Extractors = []dsl.Extractor{{Name: "ordinary", Source: "body", Type: "regex", Expression: "(z+)", OnMiss: "fail"}}
	children[count-1].Target = "/producer"
	for _, i := range []int{0, 1, count - 1} {
		children[i].Assertions = []dsl.Assertion{{Type: "status_code", Expected: 201}}
	}
	children[count-1].Extractors = []dsl.Extractor{{Name: "session_token", Source: "body", Type: "jsonpath", Expression: "$.ordinary", OnMiss: "fail"}}
	action := dsl.Action{Name: "Fresh sensitive outputs", Type: "http_sequence", Params: map[string]any{"execution_mode": "concurrent", "max_concurrency": 1, "requests": children}}
	vu := newConcurrentVU(t, server.URL)
	vu.Script.Config.Log = dsl.LogConfig{Level: "extended", OnErrorOnly: true, ExtendedOptions: []string{"parameter_substitution", "data_returned"}}
	defer vu.closeErrorLogs()
	sink := &concurrentCaptureSink{}
	vu.ResultSink = sink
	result, err := vu.executeHTTPSequenceAction(context.Background(), action, dsl.ErrorHandlingConfig{}, nil)
	if err != nil || result.Success || result.Extractions["session_token"] != token || len(sink.results) != count {
		t.Fatal("raw joined output or assertion outcomes lost")
	}
	if sink.results[0].ResponseBody != redactedPayloadValue || (sink.results[0].Snapshot != nil && sink.results[0].Snapshot.ResponseBody != redactedPayloadValue) || sink.results[0].Extractions["ordinary"] != redactedPayloadValue {
		t.Fatal("later sibling token did not mask complete earlier evidence")
	}
	if sink.results[1].ResponseBody != "" || sink.results[1].Snapshot != nil || sink.results[1].ProtocolEvidence["concurrent_group_evidence_details_complete"] != false {
		t.Fatal("unknown sibling token prefix survived pre-join truncation")
	}
	if !strings.Contains(sink.results[count-1].ResponseBody, redactedPayloadValue) || sink.results[count-1].Snapshot == nil {
		t.Fatal("fresh named output was not masked before its retention boundary")
	}
	vu.recordResult(result, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
	consume := dsl.Action{Name: "Consume exact token", Type: "GET", Target: "/consume", Headers: map[string]string{"X-Ordinary": "{{var.ordinary}}", "X-Session": "{{var.session_token}}"}}
	r, err := vu.executeWithRetry(context.Background(), vu.resolveAction(consume), dsl.ErrorHandlingConfig{})
	if err != nil || !r.Success || consumed.Load() != 1 || vu.Variables["session_token"] != token || vu.Variables["ordinary"] != token {
		t.Fatal("runtime token changed during public projection")
	}
	for _, r := range sink.results {
		encoded, err := json.Marshal(r)
		if err != nil || strings.Contains(string(encoded), strings.Repeat("z", 128)) {
			t.Fatal("published response retained fresh token or prefix")
		}
	}
	// Replay uses the same post-join protection and exact next-request output.
	script := &dsl.Script{Name: "Fresh output replay", Protocol: concurrentFixtureProtocol, Config: dsl.ScriptConfig{BaseURL: server.URL, ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(true)}}, Actions: []dsl.Action{action, consume}}
	observer := &concurrentPublicationObserver{token: token}
	var progress [][]byte
	report, err := ReplayScript(context.Background(), script, ReplayOptions{IgnoreThinkTime: true, DebugController: observer, OnProgress: func(event ReplayProgressEvent) { data, _ := json.Marshal(event); progress = append(progress, data) }})
	if err != nil || report.Success || requestReplicasExact.Load() != 2 || len(report.Events) != count+1 || !report.Events[count].Success || consumed.Load() != 2 {
		t.Fatal("replay fresh output/publication semantics differ")
	}
	encoded, err := json.Marshal(report)
	if err != nil || strings.Contains(string(encoded), strings.Repeat("z", 128)) {
		t.Fatal("replay evidence retained fresh token prefix")
	}
	for _, data := range append(progress, observer.public...) {
		if bytes.Contains(data, []byte(strings.Repeat("z", 128))) {
			t.Fatal("joined progress/debug publication retained fresh token prefix")
		}
	}
	if strings.Contains(logs.String(), strings.Repeat("z", 128)) || !strings.Contains(logs.String(), "request_body") || !strings.Contains(logs.String(), redactedPayloadValue) {
		t.Fatal("joined load logs retained a raw request replica")
	}
}

func TestConcurrentHTTPJoinedAdmissionFailureOmitsUnknownSiblingSecretEvidence(t *testing.T) {
	const producers = 5
	values := make([]string, producers)
	for i := range values {
		values[i] = strings.Repeat(string(rune('v'+i)), protocol.MaxExtractionValueBytes)
	}
	var consumed atomic.Int32
	var requestReplicasExact atomic.Int32
	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(previousLogger)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/consume" {
			if r.Header.Get("X-Ordinary") != values[producers-1] || r.Header.Get("X-Session") != values[producers-1] {
				w.WriteHeader(409)
				return
			}
			consumed.Add(1)
			io.WriteString(w, "ok")
			return
		}
		if r.URL.Path == "/early" {
			body, _ := io.ReadAll(r.Body)
			if r.URL.Query().Get("ordinary") == values[producers-1] && r.Header.Get("X-Echo") == values[producers-1] && strings.Contains(string(body), values[producers-1]) {
				requestReplicasExact.Add(1)
			}
			io.WriteString(w, values[producers-1])
			return
		}
		index := 0
		fmt.Sscanf(r.URL.Path, "/producer/%d", &index)
		json.NewEncoder(w).Encode(map[string]string{"ordinary": values[index]})
	}))
	defer server.Close()
	children := []dsl.Action{{Name: "Early neutral replica", Type: "GET", Target: "/early", Extractors: []dsl.Extractor{{Name: "ordinary", Source: "body", Type: "regex", Expression: "(z+)", OnMiss: "fail"}}, Assertions: []dsl.Assertion{{Type: "status_code", Expected: 201}}}}
	children[0].Name = "Early " + values[producers-1]
	children[0].Metadata = map[string]any{"source_action_name": values[producers-1], "source_profile": values[producers-1]}
	children[0].Type, children[0].Target, children[0].Body, children[0].Headers = "POST", "/early?ordinary="+url.QueryEscape(values[producers-1]), `{"ordinary":"`+values[producers-1]+`"}`, map[string]string{"X-Echo": values[producers-1]}
	for i := range values {
		children = append(children, dsl.Action{Name: fmt.Sprintf("Producer %d", i), Type: "GET", Target: fmt.Sprintf("/producer/%d", i), Extractors: []dsl.Extractor{{Name: fmt.Sprintf("session_token_%d", i), Source: "body", Type: "jsonpath", Expression: "$.ordinary", OnMiss: "fail"}}})
	}
	action := dsl.Action{Name: "Oversized joined outputs", Type: "http_sequence", Params: map[string]any{"execution_mode": "concurrent", "max_concurrency": 1, "requests": children}}
	vu := newConcurrentVU(t, server.URL)
	vu.Script.Config.Log = dsl.LogConfig{Level: "extended", OnErrorOnly: true, ExtendedOptions: []string{"parameter_substitution", "data_returned"}}
	defer vu.closeErrorLogs()
	sink := &concurrentCaptureSink{}
	vu.ResultSink = sink
	result, err := vu.executeHTTPSequenceAction(context.Background(), action, dsl.ErrorHandlingConfig{}, nil)
	if err != nil || result.Success || result.Extractions != nil || result.ErrorMessage != "concurrent HTTP extraction retention failed" || len(sink.results) != len(children) {
		t.Fatalf("combined output failure: error_present=%t success=%t outputs_nil=%t expected_reason=%t leaves=%d", err != nil, result.Success, result.Extractions == nil, result.ErrorMessage == "concurrent HTTP extraction retention failed", len(sink.results))
	}
	if sink.results[0].ResponseBody != "" || sink.results[0].Snapshot != nil || sink.results[0].ProtocolEvidence["concurrent_group_evidence_details_complete"] != false || len(sink.results[0].Extractions) != 0 || sink.results[0].ProtocolEvidence["concurrent_group_extractions_omitted"] != true || !sink.results[0].ExtractionsIncomplete {
		t.Fatal("failed admission retained unknown sibling replica")
	}
	if vu.Variables["ordinary"] != values[producers-1] {
		t.Fatal("ordinary runtime output changed by failed public projection")
	}
	for i, value := range values {
		if !sink.results[i+1].Success || vu.Variables[fmt.Sprintf("session_token_%d", i)] != value {
			t.Fatal("leaf outcome/exact runtime extraction lost")
		}
	}
	for _, r := range sink.results {
		encoded, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range values {
			if strings.Contains(string(encoded), value[:128]) {
				t.Fatal("failed admission published token prefix")
			}
		}
	}
	if vu.concurrentPublicationRedaction != nil || vu.concurrentPublicationUnavailable {
		t.Fatal("group publication override escaped its lifetime")
	}
	consume := dsl.Action{Name: "Consume both exact values", Type: "GET", Target: "/consume", Headers: map[string]string{"X-Ordinary": "{{var.ordinary}}", "X-Session": "{{var.session_token_4}}"}, Extractors: []dsl.Extractor{{Name: "consumer_value", Source: "body", Type: "regex", Expression: "(ok)", OnMiss: "fail"}}}
	consumedResult, err := vu.executeWithRetry(context.Background(), vu.resolveAction(consume), dsl.ErrorHandlingConfig{})
	if err != nil || !consumedResult.Success || consumed.Load() != 1 {
		t.Fatal("failed group changed either exact consumer value")
	}
	vu.recordResult(consumedResult, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
	if sink.results[len(sink.results)-1].Extractions["consumer_value"] != "ok" {
		t.Fatal("group omission leaked into the following ordinary publication")
	}
	script := &dsl.Script{Name: "Failed output admission replay", Protocol: concurrentFixtureProtocol, Config: dsl.ScriptConfig{BaseURL: server.URL, ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(true)}}, Actions: []dsl.Action{action, consume}}
	observer := &concurrentPublicationObserver{token: values[producers-1]}
	var progress [][]byte
	report, err := ReplayScript(context.Background(), script, ReplayOptions{IgnoreThinkTime: true, DebugController: observer, OnProgress: func(event ReplayProgressEvent) { data, _ := json.Marshal(event); progress = append(progress, data) }})
	if err != nil || report.Success || requestReplicasExact.Load() != 2 || !observer.rawRequestMatches || len(report.Events) != len(children)+1 || !report.Events[len(children)].Success || report.Events[len(children)].Extractions["consumer_value"] != "ok" || consumed.Load() != 2 || report.Error != "concurrent HTTP extraction retention failed" {
		t.Fatal("replay joined admission differs")
	}
	if report.Events[0].Target != "" || report.Events[0].Body != "" || len(report.Events[0].Headers) != 0 {
		t.Fatal("unavailable public request fields survived")
	}
	for _, data := range append(progress, observer.public...) {
		if bytes.Contains(data, []byte(values[producers-1][:128])) {
			t.Fatal("unavailable progress/debug request replica survived")
		}
	}
	if strings.Contains(logs.String(), values[producers-1][:128]) {
		t.Fatal("unavailable load logs retained request replica")
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range values {
		if strings.Contains(string(encoded), value[:128]) {
			t.Fatal("failed replay admission published token prefix")
		}
	}
}

// Only the private callback inspects the exact exchange. The captured public
// action/event graph intentionally excludes that JSON-invisible side channel.
type concurrentPublicationObserver struct {
	token             string
	public            [][]byte
	rawRequestMatches bool
}

func (o *concurrentPublicationObserver) capture(action ReplayDebugAction, event *ReplayActionEvent) {
	data, _ := json.Marshal(struct {
		Action ReplayDebugAction
		Event  *ReplayActionEvent
	}{action, event})
	o.public = append(o.public, data)
}
func (o *concurrentPublicationObserver) BeforeAction(_ context.Context, action ReplayDebugAction) error {
	o.capture(action, nil)
	return nil
}
func (o *concurrentPublicationObserver) AfterAction(_ context.Context, action ReplayDebugAction, event *ReplayActionEvent) error {
	o.capture(action, event)
	return nil
}
func (o *concurrentPublicationObserver) ObserveCorrelationEvidence(_ context.Context, action ReplayDebugAction, event *ReplayActionEvent) {
	o.capture(action, event)
	if action.RequestIndex != nil && *action.RequestIndex == 0 && event.CorrelationEvidence != nil {
		e := event.CorrelationEvidence
		o.rawRequestMatches = strings.Contains(e.RequestURL, o.token) && e.RequestHeaders["X-Echo"] == o.token && strings.Contains(e.RequestBody, o.token)
	}
}

func TestConcurrentHTTPFreshMatcherSharesInheritedAndJoinedLayers(t *testing.T) {
	baseSecret := strings.Repeat("q", 64<<10)
	base, err := WithSecretValueRedactionChecked(nil, []string{baseSecret})
	if err != nil {
		t.Fatal(err)
	}
	fresh := strings.Repeat("z", protocol.MaxExtractionValueBytes)
	r := &protocol.Result{Success: true, ResponseBody: baseSecret + " | " + fresh, Extractions: map[string]string{"session_token": fresh}}
	config, err := boundConcurrentCompletedResult(r, 1, base)
	if err != nil || config.runtimeParent != base || r.ResponseBody != redactedPayloadValue+" | "+redactedPayloadValue {
		t.Fatal("worker did not share inherited matcher or mask both layers")
	}
	children := make([]preparedHTTPChild, 32)
	for i := range children {
		children[i] = preparedHTTPChild{redaction: base, result: &protocol.Result{Success: true, ResponseBody: "ordinary"}}
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	err = scrubConcurrentJoinedEvidence(children, map[string]string{"session_token": fresh})
	runtime.ReadMemStats(&after)
	if err != nil || after.TotalAlloc-before.TotalAlloc > 8<<20 {
		t.Fatal("joined matcher was recompiled per sibling")
	}
	for _, child := range children {
		if child.redaction != children[0].redaction || child.redaction.runtimeParent != base {
			t.Fatal("joined immutable matcher graph was copied per sibling")
		}
	}
}

func TestConcurrentHTTPInheritedMembershipDoesNotMultiplyAllocation(t *testing.T) {
	values := make([]string, 20000)
	for i := range values {
		values[i] = fmt.Sprintf("synthetic-inherited-value-%08d", i)
	}
	base, err := WithSecretValueRedactionChecked(nil, values)
	if err != nil {
		t.Fatal("synthetic inherited matcher admission failed")
	}
	// strings.Replacer builds its immutable index lazily. Warm it and generic
	// public projection before measuring repeated worker/join attachments.
	_, err = boundConcurrentCompletedResult(&protocol.Result{Success: true, ResponseBody: values[0]}, 1, base)
	if err != nil {
		t.Fatal(err)
	}
	for _, count := range []int{1, 16, 64} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			children := make([]preparedHTTPChild, count)
			outputs := make(map[string]string, count)
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			for i := range children {
				key, value := fmt.Sprintf("session_token_%d", i), fmt.Sprintf("synthetic-fresh-value-%08d", i)
				result := &protocol.Result{Success: true, ResponseBody: value, Extractions: map[string]string{key: value}}
				config, err := boundConcurrentCompletedResult(result, count, base)
				if err != nil || config.runtimeParent != base || result.ResponseBody != redactedPayloadValue {
					t.Fatal("worker attachment did not share and project its matcher")
				}
				children[i] = preparedHTTPChild{redaction: config, result: result}
				outputs[key] = value
			}
			if err := scrubConcurrentJoinedEvidence(children, outputs); err != nil {
				t.Fatal(err)
			}
			runtime.ReadMemStats(&after)
			if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 16<<20 {
				t.Fatalf("inherited membership copied per worker/join: children=%d allocated_bytes=%d", count, allocated)
			}
		})
	}
}

func TestConcurrentHTTPVariableCapacityRetainsSensitiveObservation(t *testing.T) {
	const producers = 96
	token, repeated := strings.Repeat("z", 16<<10), strings.Repeat("x", 16<<10)
	var consumed atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/consume" {
			body, _ := io.ReadAll(r.Body)
			if r.URL.Query().Get("ordinary") != token || string(body) != token || r.Header.Get("X-Ordinary") != token {
				w.WriteHeader(http.StatusConflict)
				return
			}
			consumed.Add(1)
			io.WriteString(w, "ok")
			return
		}
		value := repeated
		if r.URL.Path == "/early" || r.URL.Path == "/producer/95" {
			value = token
		}
		json.NewEncoder(w).Encode(map[string]string{"ordinary": value, "second": value, "third": value})
	}))
	defer server.Close()
	children := []dsl.Action{{Name: "Earlier ordinary output", Type: "GET", Target: "/early", Extractors: []dsl.Extractor{{Name: "ordinary_value", Source: "body", Type: "jsonpath", Expression: "$.ordinary", OnMiss: "fail"}}}}
	for i := 0; i < producers; i++ {
		children = append(children, dsl.Action{Name: fmt.Sprintf("Producer %d", i), Type: "GET", Target: fmt.Sprintf("/producer/%d", i), Extractors: []dsl.Extractor{
			{Name: fmt.Sprintf("session_token_%d", i), Source: "body", Type: "jsonpath", Expression: "$.ordinary", OnMiss: "fail"},
			{Name: fmt.Sprintf("session_token_%d_b", i), Source: "body", Type: "jsonpath", Expression: "$.second", OnMiss: "fail"},
			{Name: fmt.Sprintf("session_token_%d_c", i), Source: "body", Type: "jsonpath", Expression: "$.third", OnMiss: "fail"},
		}})
	}
	group := dsl.Action{Name: "Over-capacity outputs", Type: "http_sequence", Params: map[string]any{"execution_mode": "concurrent", "max_concurrency": 1, "requests": children}}
	consume := dsl.Action{Name: "Following ordinary consumer", Type: "POST", Target: "/consume?ordinary={{var.ordinary_value}}", Headers: map[string]string{"X-Ordinary": "{{var.ordinary_value}}"}, Body: "{{var.ordinary_value}}", Assertions: []dsl.Assertion{{Type: "status_code", Expected: 200}}}
	vu := newConcurrentVU(t, server.URL)
	sink := &concurrentCaptureSink{}
	vu.ResultSink = sink
	var futureLogs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&futureLogs, nil)))
	defer slog.SetDefault(previousLogger)
	retainedLogger := vu.makeLogger(dsl.LogConfig{Level: "extended"}).With("ordinary_alias", token).WithGroup("future")
	result, err := vu.executeHTTPSequenceAction(context.Background(), group, dsl.ErrorHandlingConfig{}, nil)
	if err != nil || result.Success || vu.Variables["ordinary_value"] != token || vu.Variables["session_token_95"] != "" || vu.extractedVariableBytes > maxVUserExtractionBytes {
		t.Fatal("real variable-capacity fixture did not reject the later producer atomically")
	}
	if err := vu.refreshImportedCredentialRuntimeValues(); err != nil {
		t.Fatal(err)
	}
	if err := vu.replaceRuntimeCredentialScopes(map[string][]string{runtimeCredentialActionScope: {"synthetic-other-context"}, runtimeCredentialScriptActionScope: nil}); err != nil {
		t.Fatal(err)
	}
	if scrubSensitiveTextWithConfig(token, vu.RedactionConfig) != redactedPayloadValue {
		t.Fatal("late rejected producer lost classification after unrelated scope refresh")
	}
	retainedLogger.Error("future ordinary failure "+token, "action", token, "status", 200, "success", false)
	if strings.Contains(futureLogs.String(), token[:128]) || !strings.Contains(futureLogs.String(), "status=200") || !strings.Contains(futureLogs.String(), "success=false") {
		t.Fatal("retained regular logger exposed a classified alias or changed numeric outcome fields")
	}
	consumedResult, err := vu.executeWithRetry(context.Background(), vu.resolveAction(consume), dsl.ErrorHandlingConfig{})
	if err != nil || !consumedResult.Success || consumed.Load() != 1 {
		t.Fatal("earlier raw output was not available to the real following consumer")
	}
	vu.recordResult(consumedResult, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
	script := &dsl.Script{Name: "Variable-capacity replay", Protocol: concurrentFixtureProtocol, Config: dsl.ScriptConfig{BaseURL: server.URL, ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(true)}}, Actions: []dsl.Action{group, consume}}
	observer := &concurrentPublicationObserver{token: token}
	var progress [][]byte
	report, err := ReplayScript(context.Background(), script, ReplayOptions{IgnoreThinkTime: true, DebugController: observer, OnProgress: func(event ReplayProgressEvent) { data, _ := json.Marshal(event); progress = append(progress, data) }})
	if err != nil || report.Success || len(report.Events) != len(children)+1 || !report.Events[len(children)].Success || consumed.Load() != 2 || report.FinalVariables["session_token_95"] != "" || report.FinalVariables["ordinary_value"] != redactedPayloadValue {
		t.Fatal("replay final variables or following real consumer lost late classification")
	}
	consumer := report.Events[len(children)]
	if strings.Contains(consumer.Target, token[:128]) || strings.Contains(consumer.Body, token[:128]) || strings.Contains(consumer.Headers["X-Ordinary"], token[:128]) {
		t.Fatal("following public request exposed a late rejected sensitive output alias")
	}
	for _, data := range append(progress, observer.public...) {
		if bytes.Contains(data, []byte(token[:128])) {
			t.Fatal("future public progress/debug evidence retained a late sensitive alias")
		}
	}
	data, _ := json.Marshal(report)
	if bytes.Contains(data, []byte(token[:128])) {
		t.Fatal("public report retained a late rejected sensitive output alias")
	}
}

type concurrentAfterOnlyObserver struct{ after [][]byte }

func (*concurrentAfterOnlyObserver) BeforeAction(context.Context, ReplayDebugAction) error {
	return nil
}
func (o *concurrentAfterOnlyObserver) AfterAction(_ context.Context, action ReplayDebugAction, event *ReplayActionEvent) error {
	data, _ := json.Marshal(struct {
		Action ReplayDebugAction
		Event  *ReplayActionEvent
	}{action, event})
	o.after = append(o.after, data)
	return nil
}

func TestConcurrentHTTPAfterBoundaryUsesJoinedObservation(t *testing.T) {
	registerConcurrentHTTPFixture()
	token := "synthetic-late-boundary-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"value": token})
	}))
	defer server.Close()
	group := dsl.Action{Name: token, Type: "http_sequence", Metadata: map[string]any{"source_step_id": token}, Params: map[string]any{"execution_mode": "concurrent", "requests": []dsl.Action{{Name: "Producer", Type: "GET", Target: "/", Extractors: []dsl.Extractor{{Name: "session_token", Source: "body", Type: "jsonpath", Expression: "$.value"}}}}}}
	observer := &concurrentAfterOnlyObserver{}
	report, err := ReplayScript(context.Background(), &dsl.Script{Name: "Boundary observation", Protocol: concurrentFixtureProtocol, Config: dsl.ScriptConfig{BaseURL: server.URL}, Actions: []dsl.Action{group}}, ReplayOptions{IgnoreThinkTime: true, DebugController: observer})
	if err != nil || !report.Success || len(observer.after) != 1 || bytes.Contains(observer.after[0], []byte(token)) {
		t.Fatal("post-join group boundary retained a newly observed token")
	}
}

func TestSensitiveExtractionObservationHistorySurvivesReplacement(t *testing.T) {
	vu := newConcurrentVU(t, "http://127.0.0.1:1")
	old, next := "synthetic-earlier-observed-value", "synthetic-next-observed-value"
	if err := vu.mergeExtractions(&protocol.Result{Extractions: map[string]string{"ordinary_alias": old, "session_token": old}}); err != nil {
		t.Fatal(err)
	}
	if err := vu.mergeExtractions(&protocol.Result{Extractions: map[string]string{"session_token": next}}); err != nil {
		t.Fatal(err)
	}
	if err := vu.refreshImportedCredentialRuntimeValues(); err != nil {
		t.Fatal(err)
	}
	if err := vu.replaceRuntimeCredentialScopes(map[string][]string{runtimeCredentialActionScope: {"synthetic-other-action"}, runtimeCredentialScriptActionScope: nil}); err != nil {
		t.Fatal(err)
	}
	if vu.Variables["ordinary_alias"] != old || vu.Variables["session_token"] != next || vu.replayFinalVariables()["ordinary_alias"] != redactedPayloadValue || scrubSensitiveTextWithConfig(next, vu.RedactionConfig) != redactedPayloadValue {
		t.Fatal("a later named variable replacement retired classification of an earlier neutral alias")
	}
}

func TestSensitiveExtractionClassificationFailureClosesFuturePublication(t *testing.T) {
	token := "synthetic-unclassified-late-value"
	var traffic atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { traffic.Add(1) }))
	defer server.Close()
	parallel := &dsl.ControlFlow{Branches: []dsl.ParallelBranch{
		{Name: "first", Steps: []dsl.Action{{Name: "First", Type: "GET", Target: "/"}}},
		{Name: "second", Steps: []dsl.Action{{Name: "Second", Type: "GET", Target: "/"}}},
	}}
	positive := newConcurrentVU(t, server.URL)
	positive.Script.Protocol = concurrentFixtureProtocol
	if !positive.runParallelBranches(context.Background(), parallel, "action") || traffic.Load() != 2 {
		t.Fatal("real HTTP branch fixture did not execute both positive controls")
	}
	traffic.Store(0)
	vu := newConcurrentVU(t, server.URL)
	vu.Script.Config.Log = dsl.LogConfig{Level: "extended", OnErrorOnly: true, ExtendedOptions: []string{"parameter_substitution"}}
	defer vu.closeErrorLogs()
	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(previousLogger)
	// Seed the exact supported observation cardinality. This tests the next
	// real builder admission before it allocates a new matcher index.
	vu.ensureRuntimeCredentialRedactionState()
	values := make([]string, maxRuntimeSecretRedactionValues)
	for i := range values {
		values[i] = fmt.Sprintf("synthetic-observed-%08d", i)
	}
	vu.runtimeCredentialScopes["extraction:observed"] = values
	regularLogger := vu.makeLogger(dsl.LogConfig{Level: "extended"}).With("inherited", token).WithGroup("synthetic")
	errorOnlyLogger := vu.makeLogger(vu.Script.Config.Log).With("inherited", token).WithGroup("synthetic")
	vu.Variables["ordinary_alias"] = token
	sink := &concurrentCaptureSink{}
	vu.ResultSink = sink
	result := &protocol.Result{ActionName: token, Success: true, Extractions: map[string]string{"session_token": token}, ResponseBody: token}
	vu.recordResult(result, dsl.ErrorHandlingConfig{}, vu.Script.Config.Log)
	if vu.runtimeCredentialRedactionErr == nil || vu.Variables["session_token"] != "" || vu.replayFinalVariables() != nil || !vu.resultPublicationUnavailable() {
		t.Fatal("classification overflow did not fail closed before storing the sensitive output")
	}
	if len(vu.runtimeCredentialScopes["extraction:observed"]) != maxRuntimeSecretRedactionValues {
		t.Fatal("failed matcher admission grew persistent observation state")
	}
	vu.Script.Protocol = concurrentFixtureProtocol
	if vu.runParallelBranches(context.Background(), parallel, "action") || traffic.Load() != 0 {
		t.Fatal("parallel branch creation bypassed the parent classification failure")
	}
	observer := &concurrentPublicationObserver{token: token}
	var progress [][]byte
	conditionResults := 0
	report := &ReplayReport{ReplaySessionID: "synthetic-classification-failure", Success: true}
	actions := []dsl.Action{
		{Name: token, Type: "POST", Target: "/consume?ordinary={{var.ordinary_alias}}", Body: "{{var.ordinary_alias}}", Headers: map[string]string{"X-Echo": "{{var.ordinary_alias}}"}},
		{Name: token, Type: "source_message", Params: map[string]any{"message": "{{var.ordinary_alias}}", "level": "error"}},
		{Name: token, Type: "control_if", Control: &dsl.ControlFlow{Condition: &dsl.Condition{Left: "{{var.ordinary_alias}}", Right: token, Op: "eq"}}},
		{Name: token, Type: "control_if", Control: &dsl.ControlFlow{Condition: &dsl.Condition{Expr: "vars.ordinary_alias == \"" + token + "\""}}},
	}
	progressCallback := vu.guardedReplayProgress(report, func(event ReplayProgressEvent) {
		if !event.RuntimeEvidenceUnavailable {
			t.Fatal("classification failure did not signal unavailable runtime evidence")
		}
		data, _ := json.Marshal(event)
		progress = append(progress, data)
		if event.Type == ReplayProgressCondition && event.Condition != nil {
			if !event.Condition.Result || event.Condition.Branch != "then" || event.Condition.Expr != "" || event.Condition.Left != "" || event.Condition.Right != "" {
				t.Fatal("unavailable condition lost its private result/branch or exposed operand detail")
			}
			conditionResults++
		}
	})
	runReplayPhase(context.Background(), vu, actions, "action", true, true, protocol.ThinkTimeConfig{}, report, progressCallback, observer)
	for _, mode := range []string{"before", "after"} {
		for _, group := range []bool{false, true} {
			callbackReport := &ReplayReport{ReplaySessionID: "synthetic-callback-failure", Success: true}
			callback := &concurrentErrorObserver{mode: mode, token: token}
			if group {
				action := dsl.Action{Name: token, Type: "http_sequence", Params: map[string]any{"execution_mode": "concurrent", "requests": []dsl.Action{{Name: token, Type: "GET", Target: "/"}}}}
				leaf := 0
				runReplayConcurrentHTTP(context.Background(), vu, action, "action", 0, nil, nil, &leaf, true, true, protocol.ThinkTimeConfig{}, callbackReport, nil, callback)
			} else {
				leaf := 0
				runReplayUnit(context.Background(), vu, replayUnit{action: actions[0], phase: "action"}, &leaf, true, true, protocol.ThinkTimeConfig{}, callbackReport, nil, callback)
			}
			data, _ := json.Marshal(callbackReport)
			if callbackReport.Error == "" || bytes.Contains(data, []byte(token)) {
				t.Fatal("unavailable debugger callback error exposed unclassified detail")
			}
		}
	}
	// A source transaction can remain open when classification fails. Its
	// metrics/report finalization must use the same unavailable evidence policy.
	vu.sourceTransactions = map[string]*sourceTransactionRuntimeState{"synthetic": {name: token, startedAt: time.Now()}}
	finalized := vu.finalizeOpenSourceTransactions("synthetic finalization failure")
	appendReplaySourceTransactionFinalizations(report, finalized, vu.RedactionConfig, progressCallback)
	vu.runtimeLogger().Error("future error "+token, "message", token)
	regularLogger.Error("future retained logger "+token, "action", token)
	errorOnlyLogger.Error("future retained error logger "+token, "action", token)
	if traffic.Load() != 0 || len(report.Events) != 3 || conditionResults != 2 || logs.Len() != 0 {
		t.Fatal("classification failure allowed traffic, lost numeric finalization, or emitted unprojectable logs")
	}
	for _, data := range append(progress, observer.public...) {
		if bytes.Contains(data, []byte(token)) {
			t.Fatal("future debug/progress evidence retained an unclassified alias")
		}
	}
	for _, r := range sink.results {
		data, _ := json.Marshal(r)
		if bytes.Contains(data, []byte(token)) {
			t.Fatal("future sink evidence retained an unclassified alias")
		}
	}
	data, _ := json.Marshal(report)
	if !report.RuntimeEvidenceUnavailable || bytes.Contains(data, []byte(token)) {
		t.Fatal("future replay/finalization report retained an unclassified alias or lost its unavailable flag")
	}
}

func TestConcurrentHTTPWorkerClassificationFailureClosesWholeGroup(t *testing.T) {
	values := make([]string, maxRuntimeSecretRedactionValues)
	for i := range values {
		values[i] = fmt.Sprintf("synthetic-inherited-capacity-%08d", i)
	}
	base, err := WithSecretValueRedactionChecked(nil, values)
	if err != nil {
		t.Fatal("supported inherited cardinality was rejected")
	}
	token := "synthetic-worker-unclassified-value"
	var traffic atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traffic.Add(1)
		json.NewEncoder(w).Encode(map[string]string{"value": token})
	}))
	defer server.Close()
	group := dsl.Action{Name: "Group with unclassified worker", Type: "http_sequence", Params: map[string]any{"execution_mode": "concurrent", "max_concurrency": 1, "requests": []dsl.Action{
		{Name: "Earlier ordinary output", Type: "GET", Target: "/early", Extractors: []dsl.Extractor{{Name: "ordinary_alias", Source: "body", Type: "jsonpath", Expression: "$.value"}}},
		{Name: "Later sensitive output", Type: "GET", Target: "/producer", Extractors: []dsl.Extractor{{Name: "session_token", Source: "body", Type: "jsonpath", Expression: "$.value"}}},
	}}}
	for _, replay := range []bool{false, true} {
		t.Run(fmt.Sprint("replay_", replay), func(t *testing.T) {
			vu := newConcurrentVU(t, server.URL)
			vu.RedactionConfig = base
			sink := &concurrentCaptureSink{}
			vu.ResultSink = sink
			before := traffic.Load()
			if !replay {
				result, err := vu.executeHTTPSequenceAction(context.Background(), group, dsl.ErrorHandlingConfig{}, nil)
				if err != nil || result.Success || result.ErrorMessage != "concurrent HTTP publication redaction failed" {
					t.Fatal("worker failure was not an authoritative group failure")
				}
			} else {
				report := &ReplayReport{ReplaySessionID: "synthetic-worker-failure", Success: true}
				observer := &concurrentPublicationObserver{token: token}
				var progress [][]byte
				consume := dsl.Action{Name: "Following " + token, Type: "POST", Target: "/consume?ordinary={{var.ordinary_alias}}", Body: "{{var.ordinary_alias}}"}
				runReplayPhase(context.Background(), vu, []dsl.Action{group, consume}, "action", true, true, protocol.ThinkTimeConfig{}, report, vu.guardedReplayProgress(report, func(event ReplayProgressEvent) { data, _ := json.Marshal(event); progress = append(progress, data) }), observer)
				if report.Success || len(report.Events) != 3 {
					t.Fatal("worker failure lost replay structural outcomes")
				}
				for _, data := range append(progress, observer.public...) {
					if bytes.Contains(data, []byte(token)) {
						t.Fatal("worker failure published an unclassified alias")
					}
				}
				data, _ := json.Marshal(report)
				if bytes.Contains(data, []byte(token)) {
					t.Fatal("worker failure retained unclassified replay detail")
				}
			}
			if vu.runtimeCredentialRedactionErr == nil || vu.replayFinalVariables() != nil || traffic.Load()-before != 2 {
				t.Fatal("worker failure did not persist classification failure before later traffic/publication")
			}
			for _, result := range sink.results {
				data, _ := json.Marshal(result)
				if bytes.Contains(data, []byte(token)) {
					t.Fatal("worker failure exposed earlier ordinary output in a sink")
				}
			}
		})
	}
	t.Run("nested_parallel_parent", func(t *testing.T) {
		vu := newConcurrentVU(t, server.URL)
		vu.Script.Protocol, vu.RedactionConfig = concurrentFixtureProtocol, base
		before := traffic.Load()
		control := &dsl.ControlFlow{Branches: []dsl.ParallelBranch{
			{Name: "group", Steps: []dsl.Action{group}},
			{Name: "benign", Steps: []dsl.Action{{Name: "Benign", Type: "GET", Target: "/benign"}}},
		}}
		if !parallelActionsRuntimeSafe(control.Branches) {
			t.Fatal("nested HTTP group fixture was not admitted as a parallel branch")
		}
		if vu.runParallelBranches(context.Background(), control, "action") || vu.runtimeCredentialRedactionErr == nil || vu.replayFinalVariables() != nil || traffic.Load()-before != 3 {
			t.Fatal("nested branch classification failure did not propagate before parent publication")
		}
		consumer := vu.resolveAction(dsl.Action{Name: "Following", Type: "GET", Target: "/consume"})
		if _, err := vu.executeWithRetry(context.Background(), consumer, dsl.ErrorHandlingConfig{}); err == nil || traffic.Load()-before != 3 {
			t.Fatal("nested branch failure permitted future parent traffic")
		}
	})

}

type concurrentErrorObserver struct{ mode, token string }

func (o *concurrentErrorObserver) BeforeAction(context.Context, ReplayDebugAction) error {
	if o.mode == "before" {
		return fmt.Errorf("synthetic callback error: %s", o.token)
	}
	return nil
}
func (o *concurrentErrorObserver) AfterAction(context.Context, ReplayDebugAction, *ReplayActionEvent) error {
	if o.mode == "after" {
		return fmt.Errorf("synthetic callback error: %s", o.token)
	}
	return nil
}

func TestSensitiveExtractionObservedValuesProtectBufferedLogs(t *testing.T) {
	vu := newConcurrentVU(t, "http://127.0.0.1:1")
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	defer vu.closeErrorLogs()
	short, long := "synthetic-buffered-session-value", strings.Repeat("z", 16<<10)
	logger := vu.makeLogger(dsl.LogConfig{Level: "extended", OnErrorOnly: true}).With("inherited", short).WithGroup("synthetic")
	logger.Info("earlier "+short, "ordinary", short, "long_unknown", long)
	// Upstream actionLogAttrs already bounds this unknown value to 512 bytes.
	// Re-projecting that prefix after the whole token is observed is unsafe.
	traceConfig := dsl.LogConfig{Level: "extended", OnErrorOnly: true, ExtendedOptions: []string{"advanced_trace"}}
	logger.Info("earlier action", actionLogAttrs(long, nil, &protocol.Result{DependencyTarget: long, CorrelationID: long, StatusCode: 200}, traceConfig, false, vu.RedactionConfig)...)
	if len(vu.errorLogs.records) != 2 {
		t.Fatal("fixture did not retain both records before classification changed")
	}
	if logs.Len() != 0 {
		t.Fatal("error-only history was emitted before an error")
	}
	if err := vu.mergeExtractions(&protocol.Result{Extractions: map[string]string{"session_token": short, "access_token": long}}); err != nil {
		t.Fatal(err)
	}
	logger.Error("later failure", "status", 200)
	if strings.Contains(logs.String(), short) || strings.Contains(logs.String(), long[:128]) || !strings.Contains(logs.String(), redactedPayloadValue) || !strings.Contains(logs.String(), "status=200") || !strings.Contains(logs.String(), "omitted_records=2") {
		t.Fatal("prior buffered classification epoch was not omitted before current safe error publication")
	}
}
