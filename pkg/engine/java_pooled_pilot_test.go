package engine

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/jasonczech/apexload/pkg/javarunner"
	"github.com/jasonczech/apexload/pkg/javasidecar"
)

// Real engine/client/adapter, with a wire peer instead of a contained JVM.
// Hooks are installed before Run and all recorded calls are protected by mu.
type pilotPeer struct {
	mu           sync.Mutex
	calls        []javasidecar.PooledControlRequest
	committed    bool
	hook         func(http.ResponseWriter, *http.Request, javasidecar.PooledControlRequest) bool
	renewFailure atomic.Bool
	renewals     atomic.Int32
}

func newPilotEngineFixture(t *testing.T) (*pilotPeer, Config, JavaPooledPilotOptions) {
	t.Helper()
	p := &pilotPeer{}
	d := javasidecar.PooledControlDecision{TargetClass: "Actions", InvocationEnabled: true, PreparationEnabled: true, CommitEnabled: true, Mode: "pooled_pilot", ProfileID: "pilot", ResourceProfile: "resource", ConfigurationSHA256: strings.Repeat("a", 64), ContainmentBackend: "delegated_linux", SlotsPerHost: 2, HostMemoryBytes: 1024, MaxHosts: 1, MaxVUsers: 2, MaxMemoryBytes: 1024}
	deadline := time.Now().Add(time.Minute).UnixMilli()
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		write := func(value any) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(value)
		}
		switch r.URL.Path {
		case "/v1/owner/claim":
			write(javasidecar.ClaimOwnerResponse{Contract: javasidecar.ContractVersion, OwnerID: "owner", Epoch: "epoch", Claimed: true, LeaseTTLMS: javasidecar.RunnerOwnerLeaseTTL.Milliseconds()})
			return
		case "/v1/owner/renew":
			p.renewals.Add(1)
			if p.renewFailure.Load() {
				w.WriteHeader(409)
				write(javasidecar.ErrorResponse{Contract: javasidecar.ContractVersion, ErrorCode: "owner_fenced"})
				return
			}
			write(javasidecar.RenewOwnerResponse{Contract: javasidecar.ContractVersion, OwnerID: "owner", OwnerEpoch: "epoch", Renewed: true, LeaseTTLMS: javasidecar.RunnerOwnerLeaseTTL.Milliseconds()})
			return
		}
		var q javasidecar.PooledControlRequest
		if json.NewDecoder(r.Body).Decode(&q) != nil {
			t.Error("invalid request")
			w.WriteHeader(400)
			return
		}
		p.mu.Lock()
		p.calls = append(p.calls, q)
		if q.Operation == "invoke" && !p.committed {
			t.Error("user code before commit")
		}
		p.mu.Unlock()
		if p.hook != nil && p.hook(w, r, q) {
			return
		}
		response := javasidecar.PooledControlResponse{Contract: javasidecar.ContractVersion, PooledContract: javasidecar.PooledControlContract, Operation: q.Operation, RequestID: q.RequestID, RunID: "run", LeaseTTLMS: javasidecar.RuntimeLeaseTTL.Milliseconds(), Decision: d}
		switch q.Operation {
		case "claim":
			response.RunToken = strings.Repeat("b", 64)
		case "reserve":
			response.ReservationID = "reservation"
			response.PrepareDeadlineUnixMS = deadline
			slots := make([]string, q.VUsers)
			for i := range slots {
				slots[i] = fmt.Sprintf("slot-%d", i)
			}
			response.Placements = []javasidecar.PooledPlacement{{HostID: "host", SlotIDs: slots, MemoryBytes: 1024}}
		case "prepare":
			response.ReservationID = q.ReservationID
			response.Ready = true
			response.PrepareDeadlineUnixMS = deadline
		case "commit":
			response.ReservationID = q.ReservationID
			response.Committed = true
			p.mu.Lock()
			p.committed = true
			p.mu.Unlock()
		case "invoke":
			response.ReservationID, response.HostID, response.SlotID = q.ReservationID, q.HostID, q.SlotID
			response.Invocation = &javasidecar.PooledInvocationResult{Sequence: q.Sequence, Method: q.Method, EventRequestID: strconv.FormatUint(q.Sequence, 10)}
		case "diagnostics":
			response.HostID = q.HostID
			response.Diagnostics = &javarunner.HostDiagnostics{GCPauses: &javarunner.GCPauseSnapshot{Status: "available"}}
		case "close_slot":
			response.ReservationID, response.HostID, response.SlotID = q.ReservationID, q.HostID, q.SlotID
			response.SlotClosed = true
		case "close", "abort":
			response.Reaped = true
		case "renew":
		default:
			t.Errorf("unexpected operation %s", q.Operation)
		}
		write(response)
	}))
	t.Cleanup(h.Close)
	c, err := javasidecar.NewClient(h.URL, h.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.ClaimOwner(context.Background(), "owner"); err != nil {
		t.Fatal(err)
	}
	action := func(method string) dsl.Action {
		return dsl.Action{Name: method, Type: "java_vuser_run", Target: "Actions", Params: map[string]any{"method": method}}
	}
	script := &dsl.Script{ID: "execution", Name: "pilot", Protocol: "java_vuser", Config: dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}}, Init: []dsl.Action{action("init")}, Actions: []dsl.Action{action("action")}, Teardown: []dsl.Action{action("end")}}
	digest, err := JavaPooledScriptSHA256(script)
	if err != nil {
		t.Fatal(err)
	}
	return p, Config{Script: script, VUsers: 2, TerminationMode: dsl.TerminationIterations}, JavaPooledPilotOptions{Client: c, Run: javasidecar.PooledClientOptions{ExecutionID: "execution", GroupID: "group", Decision: d}, ScriptSHA256: digest, PreparationTimeout: 5 * time.Second, ActionTimeout: time.Second}
}
func (p *pilotPeer) snapshot() []javasidecar.PooledControlRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]javasidecar.PooledControlRequest(nil), p.calls...)
}

func TestJavaPooledPilotEngineStagesBeforeDuration(t *testing.T) {
	p, cfg, o := newPilotEngineFixture(t)
	cfg.TerminationMode = dsl.TerminationDuration
	cfg.Duration = 200 * time.Millisecond
	p.hook = func(_ http.ResponseWriter, _ *http.Request, q javasidecar.PooledControlRequest) bool {
		if q.Operation == "prepare" {
			time.Sleep(300 * time.Millisecond)
		}
		return false
	}
	e, err := NewJavaPooledPilot(cfg, o)
	if err != nil {
		t.Fatal(err)
	}
	// Caller's later edits cannot redirect the approved script snapshot.
	cfg.Script.Actions[0].Target = "Unapproved"
	result, err := e.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalErrors != 0 {
		t.Fatal(result)
	}
	bySlot := map[string][]string{}
	closed := 0
	prepared := false
	for _, q := range p.snapshot() {
		switch q.Operation {
		case "prepare":
			prepared = true
			if len(q.Slots) != 2 || q.Slots[0].VUserID != 0 || q.Slots[1].VUserID != 1 {
				t.Fatal("identity differs from dedicated", q.Slots)
			}
		case "invoke":
			bySlot[q.SlotID] = append(bySlot[q.SlotID], q.Method)
		case "close_slot":
			closed++
		}
	}
	if !prepared || len(bySlot) != 2 || closed != 2 {
		t.Fatal("missing lifecycle", bySlot, closed)
	}
	for _, methods := range bySlot {
		if strings.Join(methods, ",") != "init,action,end" {
			t.Fatal(methods)
		}
	}
	if _, err = e.Run(context.Background()); err == nil || err.Error() != "pooled_pilot_already_used" {
		t.Fatal("run reused", err)
	}
}

func TestJavaPooledPilotBlockedPreparation(t *testing.T) {
	for _, mode := range []string{"deadline", "stop", "owner_loss"} {
		t.Run(mode, func(t *testing.T) {
			p, cfg, o := newPilotEngineFixture(t)
			entered := make(chan struct{})
			p.hook = func(_ http.ResponseWriter, r *http.Request, q javasidecar.PooledControlRequest) bool {
				if q.Operation != "prepare" {
					return false
				}
				close(entered)
				<-r.Context().Done()
				return true
			}
			if mode == "deadline" {
				o.PreparationTimeout = 100 * time.Millisecond
			}
			e, err := NewJavaPooledPilot(cfg, o)
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { _, err := e.Run(context.Background()); done <- err }()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("no preparation")
			}
			if _, err = e.Run(context.Background()); err == nil {
				t.Fatal("concurrent reuse allowed")
			}
			if mode == "stop" {
				e.Stop()
			}
			if mode == "owner_loss" {
				p.renewFailure.Store(true)
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("staging failure lost")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("staging did not abort")
			}
			closed := false
			for _, q := range p.snapshot() {
				if q.Operation == "commit" || q.Operation == "invoke" {
					t.Fatal("expired staging executed user work")
				}
				closed = closed || q.Operation == "close"
			}
			if !closed {
				t.Fatal("cleanup omitted")
			}
		})
	}
}

func TestJavaPooledPilotRejectsUnqualifiedDSL(t *testing.T) {
	_, cfg, o := newPilotEngineFixture(t)
	for _, tc := range []struct {
		name   string
		change func(*dsl.Script)
	}{
		{"protocol", func(s *dsl.Script) { s.Protocol = "http" }},
		{"extra_action", func(s *dsl.Script) { s.Actions = append(s.Actions, s.Actions[0]) }},
		{"method", func(s *dsl.Script) { s.Actions[0].Params["method"] = "full_lifecycle" }},
		{"target", func(s *dsl.Script) { s.Actions[0].Target = "Other" }},
		{"parameter_type", func(s *dsl.Script) { s.Actions[0].Params["parameters"] = map[string]any{"a": 42} }},
		{"parameter_bound", func(s *dsl.Script) {
			s.Actions[0].Params["parameters"] = map[string]any{"a": strings.Repeat("a", 65537)}
		}},
		{"timeout", func(s *dsl.Script) { s.Actions[0].Params["timeout_ms"] = -1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := json.Marshal(cfg.Script)
			var script dsl.Script
			_ = json.Unmarshal(raw, &script)
			tc.change(&script)
			changed := cfg
			changed.Script = &script
			opt := o
			opt.ScriptSHA256, _ = JavaPooledScriptSHA256(&script)
			if _, err := NewJavaPooledPilot(changed, opt); err == nil {
				t.Fatal("unsupported DSL accepted")
			}
		})
	}
	o.ScriptSHA256 = strings.Repeat("f", 64)
	if _, err := NewJavaPooledPilot(cfg, o); err == nil {
		t.Fatal("unapproved identity accepted")
	}
}

func TestJavaPooledPilotRenewsDuringInvocation(t *testing.T) {
	p, cfg, o := newPilotEngineFixture(t)
	cfg.VUsers = 1
	o.ActionTimeout = 3 * time.Second
	p.hook = func(_ http.ResponseWriter, r *http.Request, q javasidecar.PooledControlRequest) bool {
		if q.Operation == "invoke" && q.Method == "action" {
			// Both independent lease keepers must run while the user call is blocked.
			timer := time.NewTimer(1900 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-r.Context().Done():
				t.Error("invocation canceled")
			}
		}
		return false
	}
	e, err := NewJavaPooledPilot(cfg, o)
	if err != nil {
		t.Fatal(err)
	}
	result, err := e.Run(context.Background())
	if err != nil || result == nil || result.TotalErrors != 0 {
		t.Fatal(result, err)
	}
	renewed := false
	for _, q := range p.snapshot() {
		renewed = renewed || q.Operation == "renew"
	}
	if p.renewals.Load() < 2 || !renewed {
		t.Fatal("blocked invocation stopped renewal")
	}
}

func TestJavaPooledPilotCommitAndCleanupFailures(t *testing.T) {
	for _, phase := range []string{"commit", "close", "close_slot"} {
		t.Run(phase, func(t *testing.T) {
			p, cfg, o := newPilotEngineFixture(t)
			p.hook = func(w http.ResponseWriter, _ *http.Request, q javasidecar.PooledControlRequest) bool {
				if q.Operation != phase {
					return false
				}
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"unconfirmed":true}`))
				return true
			}
			e, err := NewJavaPooledPilot(cfg, o)
			if err != nil {
				t.Fatal(err)
			}
			_, err = e.Run(context.Background())
			if err == nil {
				t.Fatal("uncertainty reported success")
			}
			count := 0
			for _, q := range p.snapshot() {
				if q.Operation == phase {
					count++
				}
				if phase == "commit" && q.Operation == "invoke" {
					t.Fatal("lost commit allowed initialization")
				}
			}
			expected := 1
			if phase == "close_slot" {
				expected = cfg.VUsers
			}
			if count != expected {
				t.Fatal("ambiguous operation replayed", phase, count)
			}
			if phase == "close" && !strings.Contains(err.Error(), "cleanup_unconfirmed") {
				t.Fatal("cleanup error lost", err)
			}
		})
	}
}

func TestJavaPooledPilotFinishPreservesUnobservedLeaseFailure(t *testing.T) {
	_, _, o := newPilotEngineFixture(t)
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(context.Canceled)
	run, err := o.Client.OpenPooledClientRun(ctx, o.Run)
	if err != nil {
		t.Fatal(err)
	}
	p := &javaPooledPilot{run: run}
	cancel(errors.New("lease failure"))
	if err := p.finish(); err == nil || !strings.Contains(err.Error(), "pooled_pilot_run_lease_lost") {
		t.Fatal("lease failure lost without keeper", err)
	}
}

func TestJavaPooledPilotBindsRunnerScriptApproval(t *testing.T) {
	_, cfg, o := newPilotEngineFixture(t)
	o.Run.Decision.ScriptSHA256 = strings.Repeat("f", 64)
	if _, err := NewJavaPooledPilot(cfg, o); err == nil {
		t.Fatal("runner DSL approval mismatch accepted")
	}
	o.Run.Decision.ScriptSHA256 = o.ScriptSHA256
	if _, err := NewJavaPooledPilot(cfg, o); err != nil {
		t.Fatal(err)
	}
}
