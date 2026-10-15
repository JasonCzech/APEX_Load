package engine

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/javasidecar"
	"github.com/jasonczech/apexload/pkg/protocol"
	javaadapter "github.com/jasonczech/apexload/pkg/protocol/java_vuser"
)

// Stable reasons a group that asked for shared JVMs runs dedicated because of
// the script's own shape. Runner and qualification reasons come from the
// java_vuser adapter and the runner's claim_shared refusal codes.
const (
	JavaSharedReasonLifecycle = "shared_jvm_lifecycle_unsupported"
	JavaSharedReasonRunLogic  = "shared_jvm_run_logic_unsupported"
	JavaSharedReasonOpen      = "shared_jvm_open_workload_unsupported"
	JavaSharedReasonParams    = "shared_jvm_parameters_unsupported"
	JavaSharedReasonAtOnce    = "shared_jvm_init_at_once_unsupported"
	// JavaSharedReasonNoVUsers: a group staged with no VUsers (one that only
	// grows later) has no slots to prepare, and shared slots cannot grow.
	JavaSharedReasonNoVUsers = "shared_jvm_no_staged_vusers"
	// JavaSharedPrepareTimeout bounds cold preparation of every JVM in a group;
	// the runner also enforces each reservation's own deadline. A controller
	// staging a shared group must wait at least this long for AssignWork.
	JavaSharedPrepareTimeout = 5 * time.Minute
)

// JavaVMPlacement is the value-free outcome of a java_vuser group's JVM mode.
type JavaVMPlacement struct {
	Group        string   `json:"group"`
	Mode         string   `json:"mode"` // "shared", "dedicated", or "pending" before staging settles
	Requested    string   `json:"requested"`
	Reason       string   `json:"reason,omitempty"`
	VUsersPerJVM int      `json:"vusers_per_jvm,omitempty"`
	JVMs         int      `json:"jvms,omitempty"`
	Warnings     []string `json:"warnings,omitempty"`
}

type javaSharedState int

const (
	javaSharedPending javaSharedState = iota
	javaSharedActive
	javaSharedDedicated
)

// javaSharedGroup runs one java_vuser group in runner-qualified shared JVMs.
// It starts pending; preparation either stages every slot cold (active) or
// records a stable reason and hands the group to dedicated JVMs (dedicated).
// A group never switches modes after preparation succeeds.
type javaSharedGroup struct {
	group, scenario string
	script          *dsl.Script
	target, digest  string
	vusersPerJVM    int
	prepared        int
	offset          int
	execution, id   string

	prepareMu sync.Mutex // serializes staging and commit; never held by state readers
	mu        sync.Mutex
	state     javaSharedState
	reason    string
	warnings  []string
	jvms      int
	slotsPer  int
	run       *javasidecar.PooledClientRun
	adapters  []protocol.Protocol
	taken     map[int]bool
	started   bool
	failure   error
	// failureReported marks a failure already returned by prepare or start.
	failureReported bool
	keeper          context.CancelFunc
	keeperEnd       chan struct{}
}

func javaSharedRequested(script *dsl.Script) bool {
	return script != nil && strings.EqualFold(strings.TrimSpace(script.Protocol), "java_vuser") && script.Config.JavaVuser.JVMSharing == dsl.JavaVuserJVMSharingShared
}

// javaSharedScriptReason mirrors the slot adapter's contract: one exact
// init/action/end lifecycle on one target, string parameters, a closed
// workload and sequential run logic. Anything else stays dedicated.
func javaSharedScriptReason(script *dsl.Script) (target, reason string) {
	if isOpenWorkload(script) {
		return "", JavaSharedReasonOpen
	}
	logic := script.Config.RunLogic
	if len(logic.ActionSequence) != 0 || len(logic.Blocks) != 0 || logic.BlockScheduling != "" && logic.BlockScheduling != "sequential" {
		return "", JavaSharedReasonRunLogic
	}
	limit := script.Config.ThinkTime.LimitMs
	if math.IsNaN(limit) || math.IsInf(limit, 0) || limit < 0 || limit > 3600000 {
		return "", JavaSharedReasonParams
	}
	for i, phase := range [][]dsl.Action{script.Init, script.Actions, script.Teardown} {
		if len(phase) != 1 {
			return "", JavaSharedReasonLifecycle
		}
		a := phase[0]
		method, _ := a.Params["method"].(string)
		if a.Type != "java_vuser_run" || a.Control != nil || method != []string{"init", "action", "end"}[i] || (target != "" && a.Target != target) || strings.TrimSpace(a.Target) == "" {
			return "", JavaSharedReasonLifecycle
		}
		target = a.Target
		for _, key := range []string{"parameters", "lr_parameters"} {
			raw, ok := a.Params[key]
			if !ok || raw == nil {
				continue
			}
			values := map[string]any{}
			switch typed := raw.(type) {
			case map[string]any:
				values = typed
			case map[string]string:
				for k, v := range typed {
					values[k] = v
				}
			default:
				return "", JavaSharedReasonParams
			}
			if len(values) > 256 {
				return "", JavaSharedReasonParams
			}
			for k, v := range values {
				s, ok := v.(string)
				if !ok || k == "" || len(k) > 126 || len(s) > 65536 || !utf8.ValidString(k) || !utf8.ValidString(s) {
					return "", JavaSharedReasonParams
				}
			}
		}
	}
	return target, ""
}

// newJavaSharedGroup returns nil when the group did not ask for shared JVMs.
// A script shape the slot adapter cannot run yields a group that is already
// dedicated, so the run records why without contacting the runner.
func newJavaSharedGroup(group GroupConfig, prepared int) (*javaSharedGroup, error) {
	if !javaSharedRequested(group.Script) {
		return nil, nil
	}
	g := &javaSharedGroup{group: group.Name, scenario: group.ScenarioID, script: group.Script, vusersPerJVM: group.Script.Config.JavaVuser.VUsersPerJVM, prepared: prepared, offset: group.VUserIDOffset, taken: map[int]bool{}}
	target, reason := javaSharedScriptReason(group.Script)
	if reason == "" && groupLifecycleAtOnce(group) {
		reason = JavaSharedReasonAtOnce
	}
	if reason == "" && prepared < 1 {
		reason = JavaSharedReasonNoVUsers
	}
	if reason != "" {
		g.state, g.reason = javaSharedDedicated, reason
		return g, nil
	}
	digest, err := JavaPooledScriptSHA256(group.Script)
	if err != nil {
		return nil, err
	}
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	// Runner identities must be unique per engine run: an execution/group
	// pair can never be reopened in one owner epoch. Human labels travel
	// separately in the claim so scripts see the dedicated-mode names.
	name := sha256.Sum256([]byte(group.Name))
	g.target, g.digest = target, digest
	g.execution = "engine-" + hex.EncodeToString(nonce[:])
	g.id = "group-" + hex.EncodeToString(name[:12])
	return g, nil
}

// active reports whether the group is (or may still become) shared. Dedicated
// capacity, ID reuse and scaling treat active groups like prepared runtimes.
func (g *javaSharedGroup) active() bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.state != javaSharedDedicated
}

// javaSharedStaged is the outcome of one staging attempt, applied under g.mu.
type javaSharedStaged struct {
	reason   string
	err      error
	run      *javasidecar.PooledClientRun
	warnings []string
	adapters []protocol.Protocol
	jvms     int
}

// prepare stages every slot cold, or settles on dedicated JVMs with a stable
// reason. It is idempotent: later calls report the first outcome. secrets is
// the group's resolver, so the claim carries the values a dedicated VUser
// would receive. Network work runs under prepareMu only, so state readers
// (active, placement) never wait on staging.
func (g *javaSharedGroup) prepare(ctx context.Context, secrets SecretResolver) error {
	g.prepareMu.Lock()
	defer g.prepareMu.Unlock()
	g.mu.Lock()
	state, failure, run := g.state, g.failure, g.run
	g.mu.Unlock()
	if state != javaSharedPending {
		if failure != nil {
			return failure
		}
		if run != nil {
			return run.Err()
		}
		return nil
	}
	staged := g.stage(ctx, secrets)
	g.mu.Lock()
	defer g.mu.Unlock()
	switch {
	case staged.reason != "":
		g.state, g.reason = javaSharedDedicated, staged.reason
	case staged.err != nil:
		return g.failLocked(staged.err)
	default:
		g.state, g.run, g.warnings, g.adapters = javaSharedActive, staged.run, staged.warnings, staged.adapters
		g.jvms, g.slotsPer = staged.jvms, staged.run.Decision().SlotsPerHost
	}
	return nil
}

// stage performs the claim and cold preparation. It reads only fields fixed
// at construction and touches no group state.
func (g *javaSharedGroup) stage(ctx context.Context, secrets SecretResolver) javaSharedStaged {
	failed := func(err error) javaSharedStaged { return javaSharedStaged{err: err} }
	cfg := buildProtoConfigForRunModeWithSecretResolver(g.script, "load", secrets)
	cfg["group_name"], cfg["scenario_id"] = g.group, g.scenario
	rt, reason, err := javaadapter.PrepareSharedJVMRuntime(ctx, cfg, g.target, g.digest, g.vusersPerJVM)
	if err != nil {
		return failed(fmt.Errorf("java_vuser shared JVM runtime: %s", scrubSensitiveText(err.Error())))
	}
	if reason != "" {
		return javaSharedStaged{reason: reason}
	}
	if _, err := rt.Client.ClaimOwner(ctx, javaadapter.ProcessOwnerID()); err != nil {
		return failed(fmt.Errorf("java_vuser sidecar owner claim failed (%s)", javaSharedSidecarCode(err)))
	}
	caps, err := rt.Client.Capabilities(ctx)
	if err != nil {
		return failed(fmt.Errorf("java_vuser sidecar capabilities unavailable (%s)", javaSharedSidecarCode(err)))
	}
	if !protocol.RuntimeFeaturesSatisfy(caps.Features, javasidecar.FeatureSharedJVMV1) {
		return javaSharedStaged{reason: javaadapter.SharedJVMReasonUnavailable}
	}
	run, warnings, err := rt.Client.OpenSharedClientRun(ctx, javasidecar.SharedClientOptions{ExecutionID: g.execution, GroupID: g.id, Claim: rt.Claim})
	if code, refused := javaadapter.SharedJVMRefusal(err); refused {
		return javaSharedStaged{reason: code}
	}
	if err != nil {
		return failed(fmt.Errorf("java_vuser shared JVM claim failed (%s)", javaSharedSidecarCode(err)))
	}
	// From here a failure abandons the claimed run at once, so a discarded
	// engine never leaves its lease renewing on the runner.
	abandon := func(err error) javaSharedStaged {
		closeCtx, cancel := context.WithTimeout(context.Background(), javasidecar.RuntimeLeaseTTL+5*time.Second)
		defer cancel()
		if closeErr := run.Close(closeCtx); closeErr != nil {
			err = errors.Join(err, errors.New("java_vuser shared JVM cleanup unconfirmed"))
		}
		return failed(err)
	}
	users := make([]javasidecar.PooledSlotContext, g.prepared)
	for i := range users {
		users[i] = javasidecar.PooledSlotContext{VUserID: int64(g.offset + i), IgnoreThinkTime: g.script.Config.ThinkTime.Ignore, ThinkTimeLimitMS: int64(g.script.Config.ThinkTime.LimitMs)}
	}
	slots, err := run.Prepare(ctx, users, JavaSharedPrepareTimeout)
	if err != nil {
		return abandon(fmt.Errorf("java_vuser shared JVM preparation failed (%s)", javaSharedSidecarCode(err)))
	}
	if g.script.Config.NativeServices != nil && len(g.script.Config.NativeServices.LiveTables) > 0 && !run.NativeDataAvailable() {
		return abandon(errors.New("native_data_pooled_v1 capability unavailable"))
	}
	hosts := map[string]bool{}
	adapters := make([]protocol.Protocol, 0, len(slots))
	for _, slot := range slots {
		adapter, err := javaadapter.NewPooledSlotProtocol(slot, rt.ActionTimeout)
		if err != nil {
			return abandon(errors.New("java_vuser shared JVM adapter unavailable"))
		}
		hosts[slot.HostID()] = true
		adapters = append(adapters, adapter)
	}
	return javaSharedStaged{run: run, warnings: warnings, adapters: adapters, jvms: len(hosts)}
}

// javaSharedSidecarCode is the runner's stable code, never free text.
func javaSharedSidecarCode(err error) string {
	var remote *javasidecar.RemoteError
	if errors.As(err, &remote) && remote.Code != "" {
		return remote.Code
	}
	if javasidecar.PooledControlOutcomeAmbiguous(err) {
		return "outcome_ambiguous"
	}
	return "unavailable"
}

// failLocked records a terminal failure that its caller returns; close then
// reports only later lease or cleanup failures. Callers hold g.mu.
func (g *javaSharedGroup) failLocked(err error) error {
	g.state = javaSharedActive
	if g.failure == nil {
		g.failure, g.failureReported = err, true
	}
	return g.failure
}

// start commits every prepared JVM and keeps the generator owner renewed on
// the run's frozen epoch. A lost lease or run stops the engine; nothing is
// replayed or moved to another mode.
func (g *javaSharedGroup) start(ctx context.Context, stop context.CancelFunc) error {
	g.prepareMu.Lock()
	defer g.prepareMu.Unlock()
	g.mu.Lock()
	run, failure, started, ready := g.run, g.failure, g.started, g.state == javaSharedActive && g.run != nil
	g.started = true
	g.mu.Unlock()
	switch {
	case failure != nil:
		return failure
	case !ready:
		return errors.New("java_vuser shared JVM group is not prepared")
	case started:
		return errors.New("java_vuser shared JVM group already started")
	}
	// Commit runs without g.mu so placement and protocol readers never wait on it.
	if err := run.Commit(ctx); err != nil {
		g.mu.Lock()
		defer g.mu.Unlock()
		return g.failLocked(fmt.Errorf("java_vuser shared JVM commit failed (%s)", javaSharedSidecarCode(err)))
	}
	keeper, cancel := context.WithCancel(context.Background())
	g.mu.Lock()
	g.keeper, g.keeperEnd = cancel, make(chan struct{})
	done := g.keeperEnd
	g.mu.Unlock()
	go g.keepOwner(keeper, stop, done)
	return nil
}

func (g *javaSharedGroup) keepOwner(ctx context.Context, stop context.CancelFunc, done chan struct{}) {
	defer close(done)
	ticker := time.NewTicker(javasidecar.RunnerOwnerLeaseRenewInterval)
	defer ticker.Stop()
	for {
		var lost error
		select {
		case <-ctx.Done():
			return
		case <-g.run.Done():
			lost = errors.New("java_vuser shared JVM run lease lost")
		case <-ticker.C:
			call, cancel := context.WithTimeout(ctx, javasidecar.RunnerOwnerLeaseRenewInterval)
			if err := g.run.RenewOwner(call); err != nil && ctx.Err() == nil {
				lost = errors.New("java_vuser shared JVM owner lease lost")
			}
			cancel()
		}
		if lost != nil {
			if ctx.Err() != nil {
				return
			}
			g.mu.Lock()
			if g.failure == nil {
				g.failure = lost
			}
			g.mu.Unlock()
			stop()
			return
		}
	}
}

// protocol hands VUser id its slot exactly once; slots are never reused.
func (g *javaSharedGroup) protocol(id int) (protocol.Protocol, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	index := id - g.offset
	if g.state != javaSharedActive || !g.started || g.failure != nil || index < 0 || index >= len(g.adapters) || g.taken[id] {
		return nil, errors.New("java_vuser shared JVM slot unavailable")
	}
	g.taken[id] = true
	return g.adapters[index], nil
}

// close stops the owner keeper and waits for positive cleanup of every JVM.
// It returns a lease failure the run has not yet reported, joined with any
// cleanup error; failures prepare or start already returned are not repeated.
func (g *javaSharedGroup) close() error {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	cancel, done, run := g.keeper, g.keeperEnd, g.run
	g.keeper = nil
	g.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
	var cleanup error
	if run != nil {
		ctx, stop := context.WithTimeout(context.Background(), javasidecar.RuntimeLeaseTTL+5*time.Second)
		defer stop()
		if err := run.Close(ctx); err != nil {
			cleanup = errors.New("java_vuser shared JVM cleanup unconfirmed")
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	var lease error
	if g.failure != nil && !g.failureReported {
		lease, g.failureReported = g.failure, true
	}
	return errors.Join(lease, cleanup)
}

// adapter returns slot id's protocol without consuming it. The single-group
// engine binds each VUser index exactly once itself.
func (g *javaSharedGroup) adapter(id int) (protocol.Protocol, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state != javaSharedActive || g.failure != nil || id < 0 || id >= len(g.adapters) {
		return nil, errors.New("java_vuser shared JVM slot unavailable")
	}
	return g.adapters[id], nil
}

// placement is the value-free outcome reported with run results. A group that
// failed while shared stays reported as shared: the run itself failed.
func (g *javaSharedGroup) placement() JavaVMPlacement {
	g.mu.Lock()
	defer g.mu.Unlock()
	p := JavaVMPlacement{Group: g.group, Requested: dsl.JavaVuserJVMSharingShared, Mode: "dedicated", Reason: g.reason, Warnings: append([]string(nil), g.warnings...)}
	switch g.state {
	case javaSharedActive:
		p.Mode, p.Reason, p.JVMs, p.VUsersPerJVM = "shared", "", g.jvms, g.slotsPer
	case javaSharedPending:
		p.Mode = "pending"
	}
	return p
}

// JavaVMPlacements reports how a script that asked for shared JVMs ran; it is
// empty for scripts that did not ask.
func (e *Engine) JavaVMPlacements() []JavaVMPlacement {
	if e == nil || e.sharedExecution == nil {
		return nil
	}
	return []JavaVMPlacement{e.sharedExecution.placement()}
}
