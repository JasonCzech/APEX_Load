package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/javarunner"
	"github.com/jasonczech/apexload/pkg/javasidecar"
	javaadapter "github.com/jasonczech/apexload/pkg/protocol/java_vuser"
)

const sharedTestToken = "shared-test-token-0123456789abcdef"

// sharedPeer is a wire-level runner: real engine, client and adapter, with a
// fake instead of contained JVMs.
type sharedPeer struct {
	mu         sync.Mutex
	ops        []string
	claims     []javasidecar.SharedJVMClaim
	committed  bool
	features   []string
	refuse     string // claim_shared refusal code
	refuseFrom int    // when set, claims after this many are refused
	// actionDelay holds each action() invocation; one the caller abandons
	// first is counted in cancelledActions.
	actionDelay      time.Duration
	cancelledActions atomic.Int64
	methods          []string
	ambiguous        bool // claim_shared answers with an unknown error
	failPrep         bool
	prepGate         chan struct{} // when set, prepare waits for it to close
	renewFail        atomic.Bool
	failCommit       bool
}

func (p *sharedPeer) record(op string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ops = append(p.ops, op)
}

func (p *sharedPeer) operations() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.ops...)
}

func newSharedPeer(t *testing.T, mode string) *sharedPeer {
	t.Helper()
	p := &sharedPeer{features: []string{javasidecar.FeatureSharedJVMV1}}
	d := javasidecar.PooledControlDecision{ScriptSHA256: strings.Repeat("0", 64), TargetClass: "Actions", InvocationEnabled: true, PreparationEnabled: true, CommitEnabled: true, Mode: javarunner.SharedJVMMode, ProfileID: javarunner.SharedJVMProfileID, ResourceProfile: "shared-auto-v1", ConfigurationSHA256: strings.Repeat("a", 64), ContainmentBackend: "delegated_linux", SlotsPerHost: 2, HostMemoryBytes: 1024, MaxHosts: 4, MaxVUsers: 8, MaxMemoryBytes: 4096}
	deadline := time.Now().Add(time.Minute).UnixMilli()
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		write := func(status int, value any) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(value)
		}
		switch r.URL.Path {
		case "/v1/owner/claim":
			write(200, javasidecar.ClaimOwnerResponse{Contract: javasidecar.ContractVersion, OwnerID: javaadapter.ProcessOwnerID(), Epoch: "epoch", Claimed: true, LeaseTTLMS: javasidecar.RunnerOwnerLeaseTTL.Milliseconds()})
			return
		case "/v1/owner/renew":
			if p.renewFail.Load() {
				write(409, javasidecar.ErrorResponse{Contract: javasidecar.ContractVersion, ErrorCode: "owner_fenced"})
				return
			}
			write(200, javasidecar.RenewOwnerResponse{Contract: javasidecar.ContractVersion, OwnerID: javaadapter.ProcessOwnerID(), OwnerEpoch: "epoch", Renewed: true, LeaseTTLMS: javasidecar.RunnerOwnerLeaseTTL.Milliseconds()})
			return
		case "/v1/capabilities":
			p.mu.Lock()
			features := append([]string(nil), p.features...)
			p.mu.Unlock()
			write(200, javasidecar.CapabilitiesResponse{Contract: javasidecar.ContractVersion, Healthy: true, Features: features, JavaMajor: 21, MaxProcesses: 4})
			return
		}
		var q javasidecar.PooledControlRequest
		if json.NewDecoder(r.Body).Decode(&q) != nil {
			t.Error("invalid request")
			w.WriteHeader(400)
			return
		}
		p.record(q.Operation)
		response := javasidecar.PooledControlResponse{Contract: javasidecar.ContractVersion, PooledContract: javasidecar.PooledControlContract, Operation: q.Operation, RequestID: q.RequestID, RunID: "run", LeaseTTLMS: javasidecar.RuntimeLeaseTTL.Milliseconds(), Decision: d}
		switch q.Operation {
		case "claim_shared":
			p.mu.Lock()
			p.claims = append(p.claims, *q.Shared)
			refuse, ambiguous := p.refuse, p.ambiguous
			if p.refuseFrom > 0 && len(p.claims) > p.refuseFrom {
				refuse = javarunner.SharedJVMReasonCapacity
			}
			p.mu.Unlock()
			if refuse != "" {
				write(409, javasidecar.ErrorResponse{Contract: javasidecar.ContractVersion, ErrorCode: refuse})
				return
			}
			if ambiguous {
				write(500, javasidecar.ErrorResponse{Contract: javasidecar.ContractVersion, ErrorCode: "unexpected_runner_state"})
				return
			}
			d.ScriptSHA256, d.TargetClass = q.Shared.ScriptSHA256, q.Shared.TargetClass
			response.Decision = d
			response.RunToken = strings.Repeat("b", 64)
			response.WarningCodes = []string{javasidecar.WarningSharedJVMHeapIgnored}
		case "reserve":
			response.ReservationID = "reservation"
			response.PrepareDeadlineUnixMS = deadline
			for host := 0; host*d.SlotsPerHost < q.VUsers; host++ {
				var slots []string
				for i := host * d.SlotsPerHost; i < min(q.VUsers, (host+1)*d.SlotsPerHost); i++ {
					slots = append(slots, fmt.Sprintf("slot-%d", i))
				}
				response.Placements = append(response.Placements, javasidecar.PooledPlacement{HostID: fmt.Sprintf("host-%d", host), SlotIDs: slots, MemoryBytes: d.HostMemoryBytes})
			}
		case "prepare":
			if p.prepGate != nil {
				<-p.prepGate
			}
			if p.failPrep {
				write(409, javasidecar.ErrorResponse{Contract: javasidecar.ContractVersion, ErrorCode: "pooled_capacity_unavailable"})
				return
			}
			response.ReservationID, response.Ready, response.PrepareDeadlineUnixMS = q.ReservationID, true, deadline
		case "commit":
			if p.failCommit {
				write(409, javasidecar.ErrorResponse{Contract: javasidecar.ContractVersion, ErrorCode: "pooled_host_state_invalid"})
				return
			}
			response.ReservationID, response.Committed = q.ReservationID, true
			p.mu.Lock()
			p.committed = true
			p.mu.Unlock()
		case "invoke":
			p.mu.Lock()
			committed := p.committed
			p.mu.Unlock()
			if !committed {
				t.Error("user code before commit")
			}
			p.mu.Lock()
			p.methods = append(p.methods, q.Method)
			p.mu.Unlock()
			if q.Method == "action" && p.actionDelay > 0 {
				select {
				case <-time.After(p.actionDelay):
				case <-r.Context().Done():
					p.cancelledActions.Add(1)
					return
				}
			}
			response.ReservationID, response.HostID, response.SlotID = q.ReservationID, q.HostID, q.SlotID
			response.Invocation = &javasidecar.PooledInvocationResult{Sequence: q.Sequence, Method: q.Method, EventRequestID: strconv.FormatUint(q.Sequence, 10)}
		case "close_slot":
			response.ReservationID, response.HostID, response.SlotID, response.SlotClosed = q.ReservationID, q.HostID, q.SlotID, true
		case "close", "abort":
			response.Reaped = true
			if q.Operation == "abort" {
				response.ReservationID = q.ReservationID
			}
		case "renew":
		default:
			t.Errorf("unexpected operation %s", q.Operation)
		}
		write(200, response)
	}))
	t.Cleanup(h.Close)
	t.Setenv(config.EnvJavaVuserRunnerEnabled, "true")
	t.Setenv(config.EnvJavaVuserRunnerMode, mode)
	t.Setenv(config.EnvJavaVuserRunnerURL, h.URL)
	t.Setenv(config.EnvJavaVuserRunnerAuthToken, sharedTestToken)
	return p
}

func sharedTestScript() *dsl.Script {
	action := func(method string) dsl.Action {
		return dsl.Action{Name: method, Type: "java_vuser_run", Target: "Actions", Params: map[string]any{"method": method, "lr_parameters": map[string]any{"user": "u1"}}}
	}
	return &dsl.Script{ID: "scenario-1", Name: "Shared group", Protocol: "java_vuser", Config: dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}, JavaVuser: dsl.JavaVuserConfig{JVMSharing: dsl.JavaVuserJVMSharingShared, VUsersPerJVM: 2, AdditionalVMParameters: []string{"-Xmx512m"}}}, Init: []dsl.Action{action("init")}, Actions: []dsl.Action{action("action")}, Teardown: []dsl.Action{action("end")}}
}

func TestJavaSharedEngineRunsInSharedJVMs(t *testing.T) {
	p := newSharedPeer(t, config.JavaVuserRunnerModeSidecar)
	e, err := New(Config{Script: sharedTestScript(), VUsers: 3, TerminationMode: dsl.TerminationIterations})
	if err != nil {
		t.Fatal(err)
	}
	result, err := e.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalErrors != 0 || result.CompletedIterations != 3 {
		t.Fatalf("result %+v", result)
	}
	if len(result.JavaVMPlacements) != 1 {
		t.Fatalf("placements %+v", result.JavaVMPlacements)
	}
	placement := result.JavaVMPlacements[0]
	if placement.Mode != "shared" || placement.JVMs != 2 || placement.VUsersPerJVM != 2 || placement.Reason != "" || len(placement.Warnings) != 1 {
		t.Fatalf("placement %+v", placement)
	}
	p.mu.Lock()
	claim := p.claims[0]
	p.mu.Unlock()
	if claim.TargetClass != "Actions" || claim.VUsersPerJVM != 2 || claim.GroupName != "Shared group" || claim.ScenarioID != "scenario-1" || strings.Join(claim.Runtime.AdditionalVMParameters, " ") != "-Xmx512m" {
		t.Fatalf("claim %+v", claim)
	}
	digest, _ := JavaPooledScriptSHA256(sharedTestScript())
	if claim.ScriptSHA256 != digest {
		t.Fatal("claim does not bind the script digest")
	}
	ops := strings.Join(p.operations(), ",")
	for _, want := range []string{"claim_shared,reserve,prepare,commit", "invoke", "close"} {
		if !strings.Contains(ops, want) {
			t.Fatalf("operations %s missing %s", ops, want)
		}
	}
	if strings.Count(ops, "invoke") != 9 {
		t.Fatalf("want init+action+end for 3 VUsers, got %s", ops)
	}
}

func TestJavaSharedGroupFallsBackWithStableReasons(t *testing.T) {
	for name, tc := range map[string]struct {
		mode    string
		setup   func(*sharedPeer)
		reason  string
		claimed bool
	}{
		"policy off on runner": {config.JavaVuserRunnerModeSidecar, func(p *sharedPeer) { p.features = nil }, javaadapter.SharedJVMReasonUnavailable, false},
		"native code":          {config.JavaVuserRunnerModeSidecar, func(p *sharedPeer) { p.refuse = javarunner.SharedJVMReasonNative }, javarunner.SharedJVMReasonNative, true},
		"java 8 runtime":       {config.JavaVuserRunnerModeSidecar, func(p *sharedPeer) { p.refuse = "jdk_unavailable" }, "jdk_unavailable", true},
		"local runner":         {config.JavaVuserRunnerModeLocal, func(*sharedPeer) {}, javaadapter.SharedJVMReasonNeedsSidecar, false},
	} {
		t.Run(name, func(t *testing.T) {
			p := newSharedPeer(t, tc.mode)
			tc.setup(p)
			script := sharedTestScript()
			g, err := newJavaSharedGroup(GroupConfig{Name: script.Name, ScenarioID: script.ID, Script: script}, 3)
			if err != nil || !g.active() {
				t.Fatalf("group %v %v", g, err)
			}
			if err := g.prepare(context.Background(), nil); err != nil {
				t.Fatal(err)
			}
			if g.active() {
				t.Fatal("refused group still shared")
			}
			placement := g.placement()
			if placement.Mode != "dedicated" || placement.Reason != tc.reason || placement.Requested != "shared" {
				t.Fatalf("placement %+v", placement)
			}
			p.mu.Lock()
			claimed := len(p.claims) > 0
			p.mu.Unlock()
			if claimed != tc.claimed {
				t.Fatalf("claimed=%v", claimed)
			}
			if err := g.prepare(context.Background(), nil); err != nil || g.active() {
				t.Fatal("second preparation changed the decision")
			}
			if _, err := g.protocol(0); err == nil {
				t.Fatal("dedicated group handed out a shared slot")
			}
		})
	}
	t.Setenv(config.EnvJavaVuserRunnerEnabled, "false")
	script := sharedTestScript()
	g, _ := newJavaSharedGroup(GroupConfig{Name: script.Name, Script: script}, 1)
	if err := g.prepare(context.Background(), nil); err != nil || g.placement().Reason != javaadapter.SharedJVMReasonRunnerDisabled {
		t.Fatalf("disabled runner: %v %+v", err, g.placement())
	}
}

func TestJavaSharedGroupAmbiguousFailuresDoNotFallBack(t *testing.T) {
	p := newSharedPeer(t, config.JavaVuserRunnerModeSidecar)
	p.ambiguous = true
	script := sharedTestScript()
	g, _ := newJavaSharedGroup(GroupConfig{Name: script.Name, Script: script}, 2)
	if err := g.prepare(context.Background(), nil); err == nil || !g.active() {
		t.Fatalf("ambiguous claim fell back: %v", err)
	}
	if err := g.prepare(context.Background(), nil); err == nil {
		t.Fatal("failure not retained")
	}

	p = newSharedPeer(t, config.JavaVuserRunnerModeSidecar)
	p.failPrep = true
	g, _ = newJavaSharedGroup(GroupConfig{Name: script.Name, Script: script}, 2)
	if err := g.prepare(context.Background(), nil); err == nil || !g.active() || !strings.Contains(err.Error(), "pooled_capacity_unavailable") {
		t.Fatalf("failed preparation fell back or lost the runner code: %v", err)
	}
	if ops := strings.Join(p.operations(), ","); !strings.HasSuffix(ops, "close") {
		t.Fatalf("failed preparation left the claimed run open: %s", ops)
	}
	if g.close() != nil {
		t.Fatal("close after abandoned run reported cleanup failure")
	}
}

func TestJavaSharedScriptShapeFallsBackStatically(t *testing.T) {
	for name, mutate := range map[string]func(*dsl.Script){
		"missing init":      func(s *dsl.Script) { s.Init = nil },
		"two actions":       func(s *dsl.Script) { s.Actions = append(s.Actions, s.Actions[0]) },
		"other method name": func(s *dsl.Script) { s.Actions[0].Params["method"] = "run" },
		"other target":      func(s *dsl.Script) { s.Teardown[0].Target = "Other" },
		"control flow":      func(s *dsl.Script) { s.Actions[0].Control = &dsl.ControlFlow{} },
		"numeric parameter": func(s *dsl.Script) { s.Actions[0].Params["parameters"] = map[string]any{"n": 1} },
		"action sequence":   func(s *dsl.Script) { s.Config.RunLogic.ActionSequence = []string{"a"} },
		"random blocks":     func(s *dsl.Script) { s.Config.RunLogic.BlockScheduling = "random" },
	} {
		script := sharedTestScript()
		mutate(script)
		g, err := newJavaSharedGroup(GroupConfig{Name: script.Name, Script: script}, 1)
		if err != nil || g == nil || g.active() || g.placement().Reason == "" {
			t.Fatalf("%s: %v %+v", name, err, g)
		}
	}
	at := sharedTestScript()
	g, _ := newJavaSharedGroup(GroupConfig{Name: at.Name, Script: at, InitStrategy: "at_once"}, 1)
	if g.active() || g.placement().Reason != JavaSharedReasonAtOnce {
		t.Fatalf("at_once %+v", g.placement())
	}
	empty := sharedTestScript()
	if g, _ := newJavaSharedGroup(GroupConfig{Name: empty.Name, Script: empty, AllowZeroVUsers: true}, 0); g.active() || g.placement().Reason != JavaSharedReasonNoVUsers {
		t.Fatalf("zero staged VUsers %+v", g.placement())
	}
	dedicated := sharedTestScript()
	dedicated.Config.JavaVuser.JVMSharing = ""
	if g, err := newJavaSharedGroup(GroupConfig{Script: dedicated}, 1); g != nil || err != nil {
		t.Fatal("dedicated script built a shared group")
	}
}

func TestJavaSharedMultiGroupStagesAndReportsPlacement(t *testing.T) {
	p := newSharedPeer(t, config.JavaVuserRunnerModeSidecar)
	mge, err := NewMultiGroup(MultiGroupConfig{Groups: []GroupConfig{{Name: "java", Script: sharedTestScript(), VUsers: 4}}, TerminationMode: dsl.TerminationIterations})
	if err != nil {
		t.Fatal(err)
	}
	if err := mge.PreflightRuntime(context.Background()); err != nil {
		t.Fatal(err)
	}
	if demand := persistentJavaConfiguredDemand(mge.groups); demand != 0 {
		t.Fatalf("shared group charged %d dedicated JVMs", demand)
	}
	result, err := mge.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("no result")
	}
	placements := mge.JavaVMPlacements()
	if len(placements) != 1 || placements[0].Mode != "shared" || placements[0].Group != "java" || placements[0].JVMs != 2 {
		t.Fatalf("placements %+v", placements)
	}
	if ops := strings.Join(p.operations(), ","); strings.Count(ops, "invoke") != 12 || !strings.HasSuffix(ops, "close") {
		t.Fatalf("operations %s", ops)
	}
}

func TestJavaSharedGroupStateReadersDoNotWaitOnStaging(t *testing.T) {
	p := newSharedPeer(t, config.JavaVuserRunnerModeSidecar)
	p.prepGate = make(chan struct{})
	script := sharedTestScript()
	g, _ := newJavaSharedGroup(GroupConfig{Name: script.Name, Script: script}, 2)
	done := make(chan error, 1)
	go func() { done <- g.prepare(context.Background(), nil) }()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(strings.Join(p.operations(), ","), "prepare") {
		if time.Now().After(deadline) {
			t.Fatal("staging never reached prepare")
		}
		time.Sleep(5 * time.Millisecond)
	}
	read := make(chan JavaVMPlacement, 1)
	go func() { _ = g.active(); read <- g.placement() }()
	select {
	case placement := <-read:
		if placement.Mode != "pending" || placement.Reason != "" {
			t.Fatalf("pending placement %+v", placement)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("state readers blocked behind staging")
	}
	close(p.prepGate)
	if err := <-done; err != nil || g.placement().Mode != "shared" {
		t.Fatalf("staging %v %+v", err, g.placement())
	}
	_ = g.close()
}

func TestJavaSharedScriptAcceptsStringMapParameters(t *testing.T) {
	script := sharedTestScript()
	script.Actions[0].Params["parameters"] = map[string]string{"user": "u2"}
	if _, reason := javaSharedScriptReason(script); reason != "" {
		t.Fatalf("string map refused: %s", reason)
	}
	script.Actions[0].Params["parameters"] = []string{"user"}
	if _, reason := javaSharedScriptReason(script); reason != JavaSharedReasonParams {
		t.Fatalf("list parameters accepted: %q", reason)
	}
}

func TestJavaSharedEngineStopsOnOwnerLeaseLoss(t *testing.T) {
	p := newSharedPeer(t, config.JavaVuserRunnerModeSidecar)
	p.renewFail.Store(true)
	script := sharedTestScript()
	script.Config.RunLogic.Iterations = 0
	script.Config.Pacing = dsl.PacingConfig{Type: "fixed_delay", DelayMs: 50}
	e, err := New(Config{Script: script, VUsers: 2, Duration: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, err = e.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "owner lease lost") {
		t.Fatalf("lease loss not reported: %v", err)
	}
	if strings.Count(err.Error(), "owner lease lost") != 1 {
		t.Fatalf("lease loss reported more than once: %v", err)
	}
	if time.Since(started) > 20*time.Second {
		t.Fatal("lease loss did not stop the run promptly")
	}
}

func TestJavaSharedEngineCommitFailureReportedOnce(t *testing.T) {
	p := newSharedPeer(t, config.JavaVuserRunnerModeSidecar)
	p.failCommit = true
	e, err := New(Config{Script: sharedTestScript(), VUsers: 2, TerminationMode: dsl.TerminationIterations})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Run(context.Background())
	if err == nil || strings.Count(err.Error(), "commit failed (pooled_host_state_invalid)") != 1 {
		t.Fatalf("commit failure %v", err)
	}
	if ops := strings.Join(p.operations(), ","); strings.Contains(ops, "invoke") || !strings.HasSuffix(ops, "close") {
		t.Fatalf("operations after failed commit: %s", ops)
	}
}

// A group larger than one runner run is split into runs of whole JVMs that
// stage, commit and close together.
func TestJavaSharedGroupSplitsAcrossRuns(t *testing.T) {
	previous := javaSharedRunVUsers
	javaSharedRunVUsers = 5 // two 2-slot JVMs per run
	t.Cleanup(func() { javaSharedRunVUsers = previous })
	p := newSharedPeer(t, config.JavaVuserRunnerModeSidecar)
	e, err := New(Config{Script: sharedTestScript(), VUsers: 6, TerminationMode: dsl.TerminationIterations})
	if err != nil {
		t.Fatal(err)
	}
	result, err := e.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalErrors != 0 || result.CompletedIterations != 6 {
		t.Fatalf("result %+v", result)
	}
	if placement := result.JavaVMPlacements[0]; placement.Mode != "shared" || placement.JVMs != 3 {
		t.Fatalf("placement %+v", placement)
	}
	counts := map[string]int{}
	for _, op := range p.operations() {
		counts[op]++
	}
	for op, want := range map[string]int{"claim_shared": 2, "reserve": 2, "prepare": 2, "commit": 2, "close": 2, "invoke": 18} {
		if counts[op] != want {
			t.Fatalf("%d %s calls, want %d: %v", counts[op], op, want, p.operations())
		}
	}
}

// A refusal after the first claim is a failure, not a quiet switch to
// dedicated JVMs, and the claimed run is closed.
func TestJavaSharedGroupAbandonsEveryRunWhenALaterClaimFails(t *testing.T) {
	previous := javaSharedRunVUsers
	javaSharedRunVUsers = 4
	t.Cleanup(func() { javaSharedRunVUsers = previous })
	p := newSharedPeer(t, config.JavaVuserRunnerModeSidecar)
	p.refuseFrom = 1
	script := sharedTestScript()
	g, err := newJavaSharedGroup(GroupConfig{Name: script.Name, ScenarioID: script.ID, Script: script}, 6)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.prepare(context.Background(), nil); err == nil || !strings.Contains(err.Error(), javarunner.SharedJVMReasonCapacity) {
		t.Fatalf("later refusal: %v", err)
	}
	if placement := g.placement(); placement.Mode != "shared" {
		t.Fatalf("failed group reported as %+v", placement)
	}
	counts := map[string]int{}
	for _, op := range p.operations() {
		counts[op]++
	}
	if counts["claim_shared"] != 2 || counts["close"] != 1 || counts["prepare"] != 0 {
		t.Fatalf("operations %v", p.operations())
	}
}

func (p *sharedPeer) methodCount(method string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	count := 0
	for _, m := range p.methods {
		if m == method {
			count++
		}
	}
	return count
}

func durationJavaScript() *dsl.Script {
	script := sharedTestScript()
	script.Config.RunLogic.Iterations = 0
	return script
}

// A duration end lets each Java VUser finish the action() it is running and
// then run end(), as LoadRunner does; it does not cut the call and skip end().
// Actions outlast the duration, so any action that started was in flight then.
func TestJavaDurationEndFinishesTheActionAndRunsEnd(t *testing.T) {
	p := newSharedPeer(t, config.JavaVuserRunnerModeSidecar)
	p.actionDelay = 800 * time.Millisecond
	e, err := New(Config{Script: durationJavaScript(), VUsers: 2, Duration: 500 * time.Millisecond, TerminationMode: dsl.TerminationDuration})
	if err != nil {
		t.Fatal(err)
	}
	result, err := e.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	assertJavaDurationDrained(t, p, result.CompletedIterations, result.TotalErrors)
	if result.TeardownErrors != 0 || result.Duration > 5*time.Second {
		t.Fatalf("result %+v", result)
	}
}

// assertJavaDurationDrained checks that every started action completed and
// both VUsers ran end(); iterations < 0 skips the iteration count.
func assertJavaDurationDrained(t *testing.T, p *sharedPeer, iterations, errors int64) {
	t.Helper()
	actions := p.methodCount("action")
	if actions < 1 || iterations >= 0 && iterations != int64(actions) || errors != 0 || p.cancelledActions.Load() != 0 || p.methodCount("end") != 2 {
		t.Fatalf("actions %d, iterations %d, errors %d, cancelled %d, end %d", actions, iterations, errors, p.cancelledActions.Load(), p.methodCount("end"))
	}
}

func TestJavaDurationEndDrainsMultiGroupJavaVUsers(t *testing.T) {
	p := newSharedPeer(t, config.JavaVuserRunnerModeSidecar)
	p.actionDelay = 800 * time.Millisecond
	mge, err := NewMultiGroup(MultiGroupConfig{Groups: []GroupConfig{{Name: "java", Script: durationJavaScript(), VUsers: 2}}, Duration: 500 * time.Millisecond, TerminationMode: dsl.TerminationDuration})
	if err != nil {
		t.Fatal(err)
	}
	if err := mge.PreflightRuntime(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, err := mge.Run(context.Background())
	if err != nil || result == nil {
		t.Fatalf("run %+v %v", result, err)
	}
	assertJavaDurationDrained(t, p, -1, result.TotalErrors)
}

// Explicit Stop stays immediate: the action in flight is cancelled.
func TestJavaExplicitStopStillCancelsTheAction(t *testing.T) {
	p := newSharedPeer(t, config.JavaVuserRunnerModeSidecar)
	p.actionDelay = 5 * time.Second
	e, err := New(Config{Script: durationJavaScript(), VUsers: 1, Duration: time.Minute, TerminationMode: dsl.TerminationDuration})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for p.methodCount("action") == 0 {
			time.Sleep(10 * time.Millisecond)
		}
		e.Stop()
	}()
	start := time.Now()
	if _, err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("explicit stop waited for the action: %s", time.Since(start))
	}
	deadline := time.Now().Add(2 * time.Second)
	for p.cancelledActions.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if p.cancelledActions.Load() != 1 {
		t.Fatalf("cancelled actions %d", p.cancelledActions.Load())
	}
}

func TestJavaDurationDrainCoversTheLongestActionTimeout(t *testing.T) {
	script := durationJavaScript()
	if got := javaDurationDrain(script); got != javaadapter.DefaultActionTimeout+javaDurationDrainMargin {
		t.Fatalf("default drain %s", got)
	}
	script.Config.JavaVuser.ActionTimeoutMs = 5000
	script.Actions[0].Params["timeout_ms"] = 90000
	if got := javaDurationDrain(script); got != 90*time.Second+javaDurationDrainMargin {
		t.Fatalf("per-action timeout ignored: %s", got)
	}
	delete(script.Actions[0].Params, "timeout_ms")
	if got := javaDurationDrain(script); got != 5*time.Second+javaDurationDrainMargin {
		t.Fatalf("script timeout ignored: %s", got)
	}
	http := &dsl.Script{Protocol: "http", Actions: script.Actions}
	if javaDurationDrain(http) != 0 || javaDurationDrain(nil) != 0 {
		t.Fatal("non-Java script drained")
	}
}

func TestJavaSharedLoadRunnerThreadsClaimsThreadMode(t *testing.T) {
	p := newSharedPeer(t, config.JavaVuserRunnerModeSidecar)
	p.features = append(p.features, javasidecar.FeatureLoadRunnerThreadsV1)
	script := sharedTestScript()
	script.Config.JavaVuser.JVMSharing = dsl.JavaVuserJVMSharingThreads
	e, err := New(Config{Script: script, VUsers: 2, TerminationMode: dsl.TerminationIterations})
	if err != nil {
		t.Fatal(err)
	}
	result, err := e.Run(context.Background())
	if err != nil || result.TotalErrors != 0 || result.CompletedIterations != 2 {
		t.Fatalf("result %+v err %v", result, err)
	}
	placement := result.JavaVMPlacements[0]
	if placement.Mode != "shared" || placement.Requested != dsl.JavaVuserJVMSharingThreads {
		t.Fatalf("placement %+v", placement)
	}
	p.mu.Lock()
	claim := p.claims[0]
	p.mu.Unlock()
	if !claim.LoadRunnerThreads {
		t.Fatal("a threads group must claim LoadRunner thread mode")
	}
}

func TestJavaSharedLoadRunnerThreadsFallsBackWithoutTheRunnerFeature(t *testing.T) {
	p := newSharedPeer(t, config.JavaVuserRunnerModeSidecar)
	script := sharedTestScript()
	script.Config.JavaVuser.JVMSharing = dsl.JavaVuserJVMSharingThreads
	g, err := newJavaSharedGroup(GroupConfig{Name: script.Name, Script: script}, 2)
	if err != nil || g == nil {
		t.Fatalf("group %v %v", g, err)
	}
	if err := g.prepare(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	placement := g.placement()
	if placement.Mode != "dedicated" || placement.Reason != javarunner.SharedJVMReasonLoadRunnerThreads || placement.Requested != dsl.JavaVuserJVMSharingThreads {
		t.Fatalf("placement %+v", placement)
	}
	p.mu.Lock()
	claimed := len(p.claims)
	p.mu.Unlock()
	if claimed != 0 {
		t.Fatal("a runner without thread mode must not be claimed")
	}
}

func TestJavaSharedAcceptsAnImportedLifecycleSequence(t *testing.T) {
	for name, tc := range map[string]struct {
		sequence []string
		reason   string
	}{
		"LoadRunner lifecycle": {[]string{"vuser_init", "action", "vuser_end"}, ""},
		"action only":          {[]string{"action"}, ""},
		"init and teardown":    {[]string{"init", "action", "teardown"}, ""},
		"repeated action":      {[]string{"vuser_init", "action", "action", "vuser_end"}, JavaSharedReasonRunLogic},
		"out of order":         {[]string{"action", "vuser_init"}, JavaSharedReasonRunLogic},
		"unknown step":         {[]string{"vuser_init", "checkout"}, JavaSharedReasonRunLogic},
	} {
		t.Run(name, func(t *testing.T) {
			script := sharedTestScript()
			script.Config.RunLogic.ActionSequence = tc.sequence
			if _, reason := javaSharedScriptReason(script); reason != tc.reason {
				t.Fatalf("reason %q, want %q", reason, tc.reason)
			}
		})
	}
}
