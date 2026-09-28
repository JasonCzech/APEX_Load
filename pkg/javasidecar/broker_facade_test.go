package javasidecar

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner"
	"github.com/jasonczech/apexload/pkg/javarunner/broker"
)

func facadeFixture(t *testing.T, block bool) (*BrokerFacade, *BrokerFacadeClient, *facadeRuntime, *broker.Store, string) {
	return facadeFixtureWrapped(t, block, nil)
}
func facadeFixtureWrapped(t *testing.T, block bool, wrap func(http.Handler) http.Handler) (*BrokerFacade, *BrokerFacadeClient, *facadeRuntime, *broker.Store, string) {
	t.Helper()
	remote, runtime, store, epoch := facadeBrokerFixtureWrapped(t, block, wrap)
	token := strings.Repeat("x", 32)
	facade, e := NewBrokerFacade(t.Context(), remote, token)
	if e != nil {
		t.Fatal(e)
	}
	server := httptest.NewServer(facade)
	client, e := NewBrokerFacadeClient(server.URL, token, 10*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { client.Close(); server.Close(); facade.Close() })
	if e = client.Claim(t.Context()); e != nil {
		t.Fatal(e)
	}
	return facade, client, runtime, store, epoch
}
func facadePrepared(t *testing.T, c *BrokerFacadeClient, duration time.Duration) *BrokerFacadeRun {
	t.Helper()
	caps, e := c.Capabilities(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	run, e := c.Reserve(t.Context(), "run", "group", caps.Profiles[0], duration, []javarunner.DedicatedHostContext{{Contract: javarunner.DedicatedHostContextContract, SessionID: "session", VUserID: 1, GroupName: "group", ScenarioID: "run"}})
	if e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		ready, e := run.Ready(t.Context())
		if e != nil {
			t.Fatal(e)
		}
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("not ready")
		}
		time.Sleep(time.Millisecond)
	}
	return run
}
func TestBrokerFacadeColdBarrierLifecycle(t *testing.T) {
	_, c, r, store, epoch := facadeFixture(t, false)
	run := facadePrepared(t, c, time.Minute)
	if _, e := run.Activate(t.Context(), "session"); e == nil {
		t.Fatal("activated before commit")
	}
	if r.calls.Load() != 0 {
		t.Fatal("preparation ran user code")
	}
	if e := run.Commit(t.Context()); e != nil {
		t.Fatal(e)
	}
	s, e := run.Activate(t.Context(), "session")
	if e != nil {
		t.Fatal(e)
	}
	if r.calls.Load() != 0 {
		t.Fatal("activation ran user code")
	}
	if s.RunID() != run.id || s.Profile() != run.Profile() {
		t.Fatal("session lost frozen reservation identity")
	}
	for _, method := range []string{"init", "action", "end"} {
		out, e := s.Execute(t.Context(), method, map[string]string{"value": "captured"})
		if e != nil || out.Stdout != "captured" {
			t.Fatal(method, out, e)
		}
	}
	if e = s.Close(t.Context()); e != nil {
		t.Fatal("close after end", e)
	}
	if _, e = s.Execute(t.Context(), "init", nil); e == nil || r.calls.Load() != 3 {
		t.Fatal("replayed closed session")
	}
	records, _, e := store.Snapshot(t.Context(), epoch)
	if e != nil || len(records) != 1 || records[0].State != javarunner.HostQuarantined {
		t.Fatal("cleanup fabricated", records, e)
	}
	if done, e := c.Cleanup(t.Context()); e != nil || done {
		t.Fatal("cleanup accepted without proof", e)
	}
}
func TestBrokerFacadeCloseIndependentOfInvocation(t *testing.T) {
	_, c, r, _, _ := facadeFixture(t, true)
	run := facadePrepared(t, c, time.Minute)
	if e := run.Commit(t.Context()); e != nil {
		t.Fatal(e)
	}
	s, e := run.Activate(t.Context(), "session")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Execute(t.Context(), "init", nil); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { _, e := s.Execute(t.Context(), "action", nil); done <- e }()
	select {
	case <-r.entered:
	case <-time.After(time.Second):
		t.Fatal("not entered")
	}
	if e = c.Renew(t.Context()); e != nil {
		t.Fatal(e)
	}
	if e = run.Renew(t.Context()); e != nil {
		t.Fatal(e)
	}
	if e = s.Renew(t.Context()); e != nil {
		t.Fatal(e)
	}
	if e = s.Close(t.Context()); e != nil {
		t.Fatal(e)
	}
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("accepted closed result")
		}
	case <-time.After(time.Second):
		t.Fatal("close blocked")
	}
}
func TestBrokerFacadeRunRenewDoesNotRenewActiveSession(t *testing.T) {
	f, c, _, _, _ := facadeFixture(t, false)
	run := facadePrepared(t, c, time.Minute)
	if e := run.Commit(t.Context()); e != nil {
		t.Fatal(e)
	}
	s, e := run.Activate(t.Context(), "session")
	if e != nil {
		t.Fatal(e)
	}
	f.mu.Lock()
	f.runs[run.id].sessions[s.id].leaseUntil = time.Now().Add(-time.Second)
	f.mu.Unlock()
	if e = c.Renew(t.Context()); e != nil {
		t.Fatal(e)
	}
	if e = run.Renew(t.Context()); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Execute(t.Context(), "init", nil); e == nil {
		t.Fatal("run revived abandoned Vuser")
	}
}
func TestBrokerFacadeDeadlinesCannotBeRenewed(t *testing.T) {
	for _, phase := range []string{"preparing", "committed"} {
		t.Run(phase, func(t *testing.T) {
			f, c, r, _, _ := facadeFixture(t, false)
			run := facadePrepared(t, c, time.Minute)
			if phase == "committed" {
				if e := run.Commit(t.Context()); e != nil {
					t.Fatal(e)
				}
			}
			f.mu.Lock()
			if phase == "committed" {
				f.runs[run.id].executeUntil = time.Now().Add(-time.Second)
			} else {
				f.runs[run.id].prepareUntil = time.Now().Add(-time.Second)
			}
			f.mu.Unlock()
			if e := run.Renew(t.Context()); e == nil {
				t.Fatal("expired deadline renewed")
			}
			if _, e := run.Activate(t.Context(), "session"); e == nil {
				t.Fatal("expired run activated")
			}
			if r.calls.Load() != 0 {
				t.Fatal("expired preparation ran code")
			}
		})
	}
}
func TestBrokerFacadeProfileDriftAndReplay(t *testing.T) {
	_, c, _, store, epoch := facadeFixture(t, false)
	caps, e := c.Capabilities(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	p := caps.Profiles[0]
	p.PolicySHA256 = strings.Repeat("b", 64)
	_, e = c.Reserve(t.Context(), "run", "group", p, time.Minute, []javarunner.DedicatedHostContext{{Contract: javarunner.DedicatedHostContextContract, SessionID: "session", VUserID: 1, GroupName: "group", ScenarioID: "run"}})
	if e == nil {
		t.Fatal("policy drift accepted")
	}
	records, _, e := store.Snapshot(t.Context(), epoch)
	if e != nil || len(records) != 0 {
		t.Fatal("drift reserved capacity")
	}
	if e = c.Renew(t.Context()); e != nil {
		t.Fatal(e)
	}
	q, _ := c.authority("renew_owner", false)
	q.Sequence = 1
	if _, e = c.exchange(t.Context(), q); e == nil {
		t.Fatal("replayed renewal")
	}
	if e = c.Claim(t.Context()); e == nil {
		t.Fatal("reclaimed owner")
	}
}
func TestBrokerFacadeRejectsUntrustedHTTP(t *testing.T) {
	f, _, _, _, _ := facadeFixture(t, false)
	for _, tc := range []struct{ name, remote, auth, origin string }{{"remote", "10.0.0.1:123", "Bearer " + strings.Repeat("x", 32), ""}, {"token", "127.0.0.1:123", "Bearer bad", ""}, {"browser", "127.0.0.1:123", "Bearer " + strings.Repeat("x", 32), "https://example.com"}} {
		t.Run(tc.name, func(t *testing.T) {
			data, _ := json.Marshal(BrokerFacadeRequest{Contract: BrokerFacadeContract, RequestID: "request", Operation: "capabilities"})
			req := httptest.NewRequest(http.MethodPost, BrokerFacadePath, bytes.NewReader(data))
			req.RemoteAddr = tc.remote
			req.Header.Set("Authorization", tc.auth)
			req.Header.Set("Origin", tc.origin)
			out := httptest.NewRecorder()
			f.ServeHTTP(out, req)
			if out.Code != 401 {
				t.Fatal(out.Code)
			}
		})
	}
}
func TestBrokerFacadeRejectsMalformedResult(t *testing.T) {
	for _, mutate := range []func(*ExecutionResponse){func(o *ExecutionResponse) { o.DurationMS = -1 }, func(o *ExecutionResponse) { o.Stdout = strings.Repeat("x", MaxCaptureBytes+1) }, func(o *ExecutionResponse) { o.Error = "failure" }, func(o *ExecutionResponse) { o.WarningCodes = []string{"unknown"} }} {
		out := ExecutionResponse{Contract: ContractVersion, ExecutionID: "request", Success: true}
		mutate(&out)
		if validFacadeResult(&out) {
			t.Fatal("invalid result accepted")
		}
	}
	if _, e := NewBrokerFacadeClient("http://127.0.0.1:99999", strings.Repeat("x", 32), time.Second); e == nil {
		t.Fatal("bad port")
	}
}

func TestBrokerFacadeLateActivationCannotReviveAbortedRun(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q BrokerFacadeRequest
		_ = json.NewDecoder(r.Body).Decode(&q)
		out := BrokerFacadeResponse{Contract: BrokerFacadeContract, RequestID: q.RequestID, RunID: "run"}
		if q.Operation == "activate" {
			close(entered)
			<-release
			out.SessionID = "session"
			out.SessionToken = "session-token"
			out.HostID = "host"
			out.LeaseMS = 1000
		} else {
			out.State = "retiring"
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	defer server.Close()
	c, e := NewBrokerFacadeClient(server.URL, strings.Repeat("x", 32), time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	c.owner = "owner"
	run := &BrokerFacadeRun{client: c, id: "run", token: "run-token", phase: "committed", lease: time.Second, sessions: map[string]*BrokerFacadeSession{"session": nil}}
	done := make(chan error, 1)
	go func() { _, e := run.Activate(t.Context(), "session"); done <- e }()
	<-entered
	if e = run.Abort(t.Context()); e != nil {
		t.Fatal(e)
	}
	close(release)
	if e = <-done; e == nil {
		t.Fatal("late activation revived an aborted run")
	}
	if run.sessions["session"].Err() == nil {
		t.Fatal("placeholder became usable")
	}
}

func TestBrokerFacadeEndResponseSurvivesRejectedRenewal(t *testing.T) { testFacadeDelayedEnd(t, false) }
func TestBrokerFacadeExplicitCloseRejectsDelayedEnd(t *testing.T)     { testFacadeDelayedEnd(t, true) }
func testFacadeDelayedEnd(t *testing.T, closeSession bool) {
	finished, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	_, c, _, _, _ := facadeFixtureWrapped(t, false, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			data, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(data))
			var q broker.BrokerRequest
			_ = json.Unmarshal(data, &q)
			if q.Operation != "invoke" || q.Method != "end" {
				next.ServeHTTP(w, r)
				return
			}
			saved := httptest.NewRecorder()
			next.ServeHTTP(saved, r)
			close(finished)
			<-release
			for k, values := range saved.Header() {
				w.Header()[k] = values
			}
			w.WriteHeader(saved.Code)
			_, _ = w.Write(saved.Body.Bytes())
		})
	})
	defer unblock()
	run := facadePrepared(t, c, time.Minute)
	if e := run.Commit(t.Context()); e != nil {
		t.Fatal(e)
	}
	s, e := run.Activate(t.Context(), "session")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Execute(t.Context(), "init", nil); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { _, e := s.Execute(t.Context(), "end", nil); done <- e }()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("end did not complete")
	}
	if closeSession {
		if e = s.Close(t.Context()); e != nil {
			t.Fatal(e)
		}
	} else if e = s.Renew(t.Context()); e == nil {
		t.Fatal("closed host unexpectedly renewed")
	}
	unblock()
	select {
	case e := <-done:
		if (e != nil) != closeSession {
			t.Fatal("incorrect delayed end result", e)
		}
	case <-time.After(time.Second):
		t.Fatal("end response stuck")
	}
}
