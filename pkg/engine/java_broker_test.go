package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jasonczech/apexload/pkg/protocol"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/javarunner/broker"
	"github.com/jasonczech/apexload/pkg/javasidecar"
)

type brokerEnginePeer struct {
	mu                   sync.Mutex
	calls                []javasidecar.BrokerFacadeRequest
	committed            bool
	delayReady, delayEnd time.Duration
	failRenew            bool
}

func brokerEngineFixture(t *testing.T) (*brokerEnginePeer, Config, JavaBrokerOptions) {
	t.Helper()
	p := &brokerEnginePeer{}
	profile := broker.BrokerProfileCapability{ID: "approved", ConfigurationSHA256: strings.Repeat("a", 64), PolicySHA256: strings.Repeat("b", 64), MemoryBytes: 100, HostLeaseMS: 900, PreparationMS: 5000, InvocationMS: 1000, MaxRunMS: 60000}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q javasidecar.BrokerFacadeRequest
		if json.NewDecoder(r.Body).Decode(&q) != nil {
			t.Error("invalid request")
			w.WriteHeader(400)
			return
		}
		p.mu.Lock()
		p.calls = append(p.calls, q)
		if q.Operation == "commit" {
			p.committed = true
		}
		committed := p.committed
		readyDelay, endDelay, failRenew := p.delayReady, p.delayEnd, p.failRenew
		p.mu.Unlock()
		if q.Operation == "invoke" && !committed {
			t.Error("user code before commit")
		}
		out := javasidecar.BrokerFacadeResponse{Contract: javasidecar.BrokerFacadeContract, RequestID: q.RequestID}
		switch q.Operation {
		case "capabilities":
			out.Capabilities = &broker.BrokerCapabilities{Mode: "dedicated", Containment: "execution_pod", MaxHosts: 2, MaxVUsers: 2, MaxMemoryBytes: 1000, Profiles: []broker.BrokerProfileCapability{profile}}
		case "claim":
			out.OwnerToken = "owner"
			out.LeaseMS = 3000
		case "renew_owner":
			out.Sequence = q.Sequence
			out.LeaseMS = 3000
		case "reserve":
			out.RunID = q.GroupID + "-run"
			out.RunToken = "run-token"
			out.State = "preparing"
			out.LeaseMS = profile.HostLeaseMS
		case "ready":
			select {
			case <-r.Context().Done():
				return
			case <-time.After(readyDelay):
			}
			out.RunID = q.RunID
			out.State = "ready"
		case "commit":
			out.RunID = q.RunID
			out.State = "committed"
		case "activate":
			out.RunID = q.RunID
			out.SessionID = q.SessionID
			out.HostID = "host-" + q.SessionID
			out.SessionToken = "session-token"
			out.LeaseMS = profile.HostLeaseMS
		case "renew_run":
			out.RunID = q.RunID
			out.Sequence = q.Sequence
			out.LeaseMS = profile.HostLeaseMS
		case "renew_session":
			out.RunID = q.RunID
			out.SessionID = q.SessionID
			out.HostID = "host-" + q.SessionID
			out.Sequence = q.Sequence
			out.LeaseMS = profile.HostLeaseMS
		case "invoke":
			if q.Method == "end" {
				select {
				case <-r.Context().Done():
					return
				case <-time.After(endDelay):
				}
			}
			out.RunID = q.RunID
			out.SessionID = q.SessionID
			out.HostID = "host-" + q.SessionID
			out.Sequence = q.Sequence
			out.Result = &javasidecar.ExecutionResponse{Contract: javasidecar.ContractVersion, ExecutionID: fmt.Sprintf("request-%d", q.Sequence), Success: true, DurationMS: 1}
		case "close_session":
			out.RunID = q.RunID
			out.SessionID = q.SessionID
			out.HostID = "host-" + q.SessionID
			out.State = "retiring"
		case "abort":
			out.RunID = q.RunID
			out.State = "retiring"
		case "release":
			out.State = "retiring"
		default:
			t.Errorf("unexpected op %s", q.Operation)
		}
		if failRenew && q.Operation == "renew_session" {
			out = javasidecar.BrokerFacadeResponse{Contract: out.Contract, RequestID: out.RequestID, Failure: "java_facade_lease_fenced"}
			w.WriteHeader(409)
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(server.Close)
	client, e := javasidecar.NewBrokerFacadeClient(server.URL, strings.Repeat("x", 32), time.Second)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(client.Close)
	action := func(method string) dsl.Action {
		return dsl.Action{Name: method, Type: "java_vuser_run", Target: "Actions", Params: map[string]any{"method": method}}
	}
	script := &dsl.Script{ID: "execution", Name: "dedicated", Protocol: "java_vuser", Config: dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}}, Init: []dsl.Action{action("init")}, Actions: []dsl.Action{action("action")}, Teardown: []dsl.Action{action("end")}}
	digest, e := JavaPooledScriptSHA256(script)
	if e != nil {
		t.Fatal(e)
	}
	return p, Config{Script: script, VUsers: 2, TerminationMode: dsl.TerminationIterations}, JavaBrokerOptions{Client: client, Profile: profile, ScriptSHA256: digest, TargetClass: "Actions", ExecutionID: "execution", GroupID: "group", PreparationTimeout: 2 * time.Second, RunLifetime: time.Minute}
}
func TestJavaBrokerEngineStageLifecycleAndSingleUse(t *testing.T) {
	p, cfg, o := brokerEngineFixture(t)
	p.delayReady = 100 * time.Millisecond
	var start time.Time
	cfg.EvidenceStarted = func(v time.Time) { start = v }
	before := time.Now()
	e, err := NewJavaBrokerExecution(cfg, o)
	if err != nil {
		t.Fatal(err)
	}
	// Mutation of the original script cannot alter the admitted snapshot.
	cfg.Script.Actions[0].Target = "Changed"
	result, err := e.Run(t.Context())
	if err != nil || result == nil || result.CompletedIterations != 2 || result.TotalErrors != 0 {
		t.Fatal(result, err)
	}
	if _, err = e.Run(t.Context()); err == nil {
		t.Fatal("reused broker engine")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	counts := map[string]int{}
	invokes := map[string]int{}
	for _, q := range p.calls {
		counts[q.Operation]++
		if q.Operation == "invoke" {
			invokes[q.SessionID]++
		}
	}
	if counts["reserve"] != 1 || counts["activate"] != 2 || counts["release"] != 1 || invokes["vuser-0"] != 3 || invokes["vuser-1"] != 3 {
		t.Fatal(counts, invokes)
	}
	if start.Sub(before) < p.delayReady {
		t.Fatal("execution clock started before readiness")
	}
}
func TestJavaBrokerEnginePreparationExpiryRunsNoCode(t *testing.T) {
	p, cfg, o := brokerEngineFixture(t)
	p.delayReady = time.Second
	o.PreparationTimeout = 30 * time.Millisecond
	e, err := NewJavaBrokerExecution(cfg, o)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Run(t.Context()); err == nil {
		t.Fatal("expired preparation succeeded")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, q := range p.calls {
		if q.Operation == "commit" || q.Operation == "invoke" {
			t.Fatal("expired stage advanced", q.Operation)
		}
	}
}
func TestJavaBrokerEngineLongEndRenewsAndStops(t *testing.T) {
	p, cfg, o := brokerEngineFixture(t)
	p.delayEnd = 650 * time.Millisecond
	e, err := NewJavaBrokerExecution(cfg, o)
	if err != nil {
		t.Fatal(err)
	}
	result, err := e.Run(t.Context())
	if err != nil || result.TotalErrors != 0 {
		t.Fatal(result, err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	renewed := map[string]bool{}
	for _, q := range p.calls {
		if q.Operation == "renew_session" {
			renewed[q.SessionID] = true
		}
	}
	if len(renewed) != 2 {
		t.Fatal("end lost its lease", renewed)
	}
}
func TestJavaBrokerEngineProfileDriftBeforeClaim(t *testing.T) {
	p, cfg, o := brokerEngineFixture(t)
	o.Profile.PolicySHA256 = strings.Repeat("c", 64)
	e, err := NewJavaBrokerExecution(cfg, o)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Run(t.Context()); err == nil {
		t.Fatal("profile drift accepted")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, q := range p.calls {
		if q.Operation == "claim" || q.Operation == "reserve" {
			t.Fatal("claimed changed policy")
		}
	}
}
func TestJavaBrokerEngineRejectsInvalidBounds(t *testing.T) {
	_, cfg, o := brokerEngineFixture(t)
	for _, mutate := range []func(*JavaBrokerOptions){func(o *JavaBrokerOptions) { o.Profile.MemoryBytes = 0 }, func(o *JavaBrokerOptions) { o.Profile.HostLeaseMS = 0 }, func(o *JavaBrokerOptions) { o.RunLifetime = 2 * time.Minute }, func(o *JavaBrokerOptions) { o.ScriptSHA256 = "wrong" }} {
		bad := o
		mutate(&bad)
		if _, e := NewJavaBrokerExecution(cfg, bad); e == nil {
			t.Fatal("invalid admission")
		}
	}
}

func TestJavaBrokerEngineRejectsTimeoutAboveFrozenProfile(t *testing.T) {
	_, cfg, o := brokerEngineFixture(t)
	cfg.Script.Actions[0].Params["timeout_ms"] = 2000
	digest, e := JavaPooledScriptSHA256(cfg.Script)
	if e != nil {
		t.Fatal(e)
	}
	o.ScriptSHA256 = digest
	if _, e = NewJavaBrokerExecution(cfg, o); e == nil {
		t.Fatal("script timeout silently shortened by profile")
	}
}

func TestJavaBrokerEngineRequiresActivationAndTeardownMargin(t *testing.T) {
	_, cfg, o := brokerEngineFixture(t)
	cfg.TerminationMode = dsl.TerminationDuration
	cfg.Duration = o.RunLifetime
	if _, e := NewJavaBrokerExecution(cfg, o); e == nil {
		t.Fatal("no lifetime left for activation and teardown")
	}
	cfg.Duration = o.RunLifetime - o.PreparationTimeout - time.Duration(o.Profile.InvocationMS)*time.Millisecond
	if _, e := NewJavaBrokerExecution(cfg, o); e != nil {
		t.Fatal(e)
	}
}

func TestJavaBrokerEngineCarriesFrozenReservationEvidence(t *testing.T) {
	_, cfg, o := brokerEngineFixture(t)
	e, err := NewJavaBrokerExecution(cfg, o)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	b := e.brokerExecution
	defer b.finish()
	if err = b.stage(ctx, cancel, e); err != nil {
		t.Fatal(err)
	}
	result, err := b.adapters[0].Execute(ctx, &protocol.Action{Type: "java_vuser_run", Target: "Actions", Params: map[string]any{"method": "init"}})
	if err != nil || !result.Success {
		t.Fatal(result, err)
	}
	evidence := result.ProtocolEvidence
	if evidence["runner_reservation_id"] != "group-run" || evidence["runner_profile_id"] != o.Profile.ID || evidence["runner_resource_profile"] != o.Profile.PolicySHA256 || evidence["runner_configuration_sha256"] != o.Profile.ConfigurationSHA256 {
		t.Fatal("lost admitted identity", evidence)
	}
	if _, ok := evidence["run_token"]; ok {
		t.Fatal("disclosed authority")
	}
}
