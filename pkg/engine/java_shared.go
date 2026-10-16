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
	"github.com/jasonczech/apexload/pkg/javarunner"
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
	threads         bool // LoadRunner thread mode
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
	runs      []*javasidecar.PooledClientRun
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
	return script != nil && strings.EqualFold(strings.TrimSpace(script.Protocol), "java_vuser") && dsl.JavaVuserSharesJVMs(script.Config.JavaVuser.JVMSharing)
}

// javaSharedScriptReason mirrors the slot adapter's contract: one exact
// init/action/end lifecycle on one target, string parameters, a closed
// workload and sequential run logic. Anything else stays dedicated.
func javaSharedScriptReason(script *dsl.Script) (target, reason string) {
	if isOpenWorkload(script) {
		return "", JavaSharedReasonOpen
	}
	logic := script.Config.RunLogic
	if !lifecycleOnlySequence(logic.ActionSequence) || len(logic.Blocks) != 0 || logic.BlockScheduling != "" && logic.BlockScheduling != "sequential" {
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

// lifecycleOnlySequence reports an action sequence that at most restates the
// init, action, end order, as an imported LoadRunner script's
// [vuser_init, action, vuser_end] does; the slot adapter runs that order.
func lifecycleOnlySequence(sequence []string) bool {
	rank := map[string]int{"init": 0, "vuser_init": 0, "action": 1, "teardown": 2, "vuser_end": 2}
	last := -1
	for _, step := range sequence {
		current, ok := rank[strings.ToLower(strings.TrimSpace(step))]
		if !ok || current <= last {
			return false
		}
		last = current
	}
	return true
}

// newJavaSharedGroup returns nil when the group did not ask for shared JVMs.
// A script shape the slot adapter cannot run yields a group that is already
// dedicated, so the run records why without contacting the runner.
func newJavaSharedGroup(group GroupConfig, prepared int) (*javaSharedGroup, error) {
	if !javaSharedRequested(group.Script) {
		return nil, nil
	}
	g := &javaSharedGroup{group: group.Name, scenario: group.ScenarioID, script: group.Script, threads: group.Script.Config.JavaVuser.JVMSharing == dsl.JavaVuserJVMSharingThreads, vusersPerJVM: group.Script.Config.JavaVuser.VUsersPerJVM, prepared: prepared, offset: group.VUserIDOffset, taken: map[int]bool{}}
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
	runs     []*javasidecar.PooledClientRun
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
	state, failure, runs := g.state, g.failure, g.runs
	g.mu.Unlock()
	if state != javaSharedPending {
		if failure != nil {
			return failure
		}
		var errs []error
		for _, run := range runs {
			errs = append(errs, run.Err())
		}
		return errors.Join(errs...)
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
		g.state, g.runs, g.warnings, g.adapters = javaSharedActive, staged.runs, staged.warnings, staged.adapters
		g.jvms, g.slotsPer = staged.jvms, staged.runs[0].Decision().SlotsPerHost
	}
	return nil
}

// stage performs the claims and cold preparation. One runner run holds at
// most javasidecar.MaxPooledRunVUsers slots, so a larger group is split into
// runs of whole JVMs; the runner's shared admission budget covers them all. A
// refusal of the first claim leaves the group dedicated; any later failure
// abandons every claimed run. It reads only fields fixed at construction and
// touches no group state.
func (g *javaSharedGroup) stage(ctx context.Context, secrets SecretResolver) javaSharedStaged {
	failed := func(err error) javaSharedStaged { return javaSharedStaged{err: err} }
	deadline := time.Now().Add(JavaSharedPrepareTimeout)
	cfg := buildProtoConfigForRunModeWithSecretResolver(g.script, "load", secrets)
	cfg["group_name"], cfg["scenario_id"] = g.group, g.scenario
	rt, reason, err := javaadapter.PrepareSharedJVMRuntime(ctx, cfg, g.target, g.digest, g.vusersPerJVM, g.threads)
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
	if g.threads && !protocol.RuntimeFeaturesSatisfy(caps.Features, javasidecar.FeatureLoadRunnerThreadsV1) {
		return javaSharedStaged{reason: javarunner.SharedJVMReasonLoadRunnerThreads}
	}
	open := func(part int) (*javasidecar.PooledClientRun, []string, error) {
		id := g.id
		if part > 0 {
			id = fmt.Sprintf("%s-%d", g.id, part)
		}
		return rt.Client.OpenSharedClientRun(ctx, javasidecar.SharedClientOptions{ExecutionID: g.execution, GroupID: id, Claim: rt.Claim})
	}
	first, warnings, err := open(0)
	if code, refused := javaadapter.SharedJVMRefusal(err); refused {
		return javaSharedStaged{reason: code}
	}
	if err != nil {
		return failed(fmt.Errorf("java_vuser shared JVM claim failed (%s)", javaSharedSidecarCode(err)))
	}
	// From here a failure abandons every claimed run at once, so a discarded
	// engine never leaves a lease renewing on the runner.
	runs := []*javasidecar.PooledClientRun{first}
	abandon := func(err error) javaSharedStaged {
		if closeSharedRuns(runs) != nil {
			err = errors.Join(err, errors.New("java_vuser shared JVM cleanup unconfirmed"))
		}
		return failed(err)
	}
	decision := first.Decision()
	slotsPerJVM := decision.SlotsPerHost
	perRun := max(slotsPerJVM, min(javaSharedRunVUsers, decision.MaxVUsers)/slotsPerJVM*slotsPerJVM)
	parts := (g.prepared + perRun - 1) / perRun
	if parts > 1 {
		more, errs := make([]*javasidecar.PooledClientRun, parts-1), make([]error, parts-1)
		var wg sync.WaitGroup
		for part := 1; part < parts; part++ {
			wg.Go(func() { more[part-1], _, errs[part-1] = open(part) })
		}
		wg.Wait()
		for _, run := range more {
			if run != nil {
				runs = append(runs, run)
			}
		}
		if err := firstError(errs); err != nil {
			return abandon(fmt.Errorf("java_vuser shared JVM claim failed (%s)", javaSharedSidecarCode(err)))
		}
		for _, run := range runs[1:] {
			if run.Decision().SlotsPerHost != slotsPerJVM {
				return abandon(errors.New("java_vuser shared JVM claims disagree"))
			}
		}
	}
	slots, errs := make([][]*javasidecar.PooledClientSlot, len(runs)), make([]error, len(runs))
	var wg sync.WaitGroup
	for i, run := range runs {
		users := make([]javasidecar.PooledSlotContext, min(g.prepared, (i+1)*perRun)-i*perRun)
		for j := range users {
			users[j] = javasidecar.PooledSlotContext{VUserID: int64(g.offset + i*perRun + j), IgnoreThinkTime: g.script.Config.ThinkTime.Ignore, ThinkTimeLimitMS: int64(g.script.Config.ThinkTime.LimitMs)}
		}
		wg.Go(func() { slots[i], errs[i] = run.Prepare(ctx, users, time.Until(deadline)) })
	}
	wg.Wait()
	if err := firstError(errs); err != nil {
		return abandon(fmt.Errorf("java_vuser shared JVM preparation failed (%s)", javaSharedSidecarCode(err)))
	}
	hosts := map[string]bool{}
	adapters := make([]protocol.Protocol, 0, g.prepared)
	for i, run := range runs {
		if g.script.Config.NativeServices != nil && len(g.script.Config.NativeServices.LiveTables) > 0 && !run.NativeDataAvailable() {
			return abandon(errors.New("native_data_pooled_v1 capability unavailable"))
		}
		for _, slot := range slots[i] {
			adapter, err := javaadapter.NewPooledSlotProtocol(slot, rt.ActionTimeout)
			if err != nil {
				return abandon(errors.New("java_vuser shared JVM adapter unavailable"))
			}
			hosts[fmt.Sprintf("%d/%s", i, slot.HostID())] = true
			adapters = append(adapters, adapter)
		}
	}
	return javaSharedStaged{runs: runs, warnings: warnings, adapters: adapters, jvms: len(hosts)}
}

// javaDurationDrainMargin lets the result of an action that ends right at its
// timeout reach the engine before a duration drain cancels it.
const javaDurationDrainMargin = 2 * time.Second

// javaDurationDrain is how long a Java VUser may finish the action in flight
// when the run's duration ends, before the run cancels it: the longest timeout
// any init or action call can have (after which it fails anyway) plus a margin.
// Each Java iteration is one action() call, so this finishes the iteration and
// lets end() run on a usable session, as LoadRunner does by default. Other
// scripts, and legacy one-shot Java actions, stop at once (0).
func javaDurationDrain(script *dsl.Script) time.Duration {
	if script == nil || !strings.EqualFold(strings.TrimSpace(script.Protocol), "java_vuser") || !javaVuserScriptUsesPhaseLifecycle(script) {
		return 0
	}
	fallback := time.Duration(script.Config.JavaVuser.ActionTimeoutMs) * time.Millisecond
	if fallback <= 0 {
		fallback = javaadapter.DefaultActionTimeout
	}
	fallback = min(fallback, dsl.MaxConfiguredProtocolTimeout)
	return max(javaLongestActionTimeout(script.Init, fallback), javaLongestActionTimeout(script.Actions, fallback)) + javaDurationDrainMargin
}

// javaLongestActionTimeout is the longest timeout of the java_vuser_run calls
// in actions, each falling back to the script's action timeout as the
// protocol does.
func javaLongestActionTimeout(actions []dsl.Action, fallback time.Duration) time.Duration {
	longest := fallback
	for _, action := range actions {
		if strings.EqualFold(strings.TrimSpace(action.Type), "java_vuser_run") {
			if value, present := action.Params["timeout_ms"]; present {
				if timeout, err := protocol.BoundedMilliseconds(value, fallback, dsl.MaxConfiguredProtocolTimeout); err == nil {
					longest = max(longest, timeout)
				}
			}
		}
		if action.Control != nil {
			for _, branch := range action.Control.ActionBranches() {
				longest = max(longest, javaLongestActionTimeout(branch.Actions, fallback))
			}
		}
	}
	return longest
}

// javaSharedRunVUsers is the most VUsers one runner run holds (a variable
// only so tests can split small groups).
var javaSharedRunVUsers = javasidecar.MaxPooledRunVUsers

func firstError(errs []error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// closeSharedRuns closes runs in parallel and waits for positive cleanup.
func closeSharedRuns(runs []*javasidecar.PooledClientRun) error {
	ctx, cancel := context.WithTimeout(context.Background(), javasidecar.RuntimeLeaseTTL+5*time.Second)
	defer cancel()
	errs := make([]error, len(runs))
	var wg sync.WaitGroup
	for i, run := range runs {
		wg.Go(func() { errs[i] = run.Close(ctx) })
	}
	wg.Wait()
	return errors.Join(errs...)
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
	runs, failure, started, ready := g.runs, g.failure, g.started, g.state == javaSharedActive && len(g.runs) > 0
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
	for _, run := range runs {
		if err := run.Commit(ctx); err != nil {
			g.mu.Lock()
			defer g.mu.Unlock()
			return g.failLocked(fmt.Errorf("java_vuser shared JVM commit failed (%s)", javaSharedSidecarCode(err)))
		}
	}
	keeper, cancel := context.WithCancel(context.Background())
	g.mu.Lock()
	g.keeper, g.keeperEnd = cancel, make(chan struct{})
	done := g.keeperEnd
	g.mu.Unlock()
	go g.keepOwner(keeper, stop, done, runs)
	return nil
}

// keepOwner renews the owner lease, which covers every run of this client,
// and stops the engine when any run ends early.
func (g *javaSharedGroup) keepOwner(ctx context.Context, stop context.CancelFunc, done chan struct{}, runs []*javasidecar.PooledClientRun) {
	defer close(done)
	ended := make(chan struct{})
	var once sync.Once
	for _, run := range runs {
		go func() {
			select {
			case <-run.Done():
				once.Do(func() { close(ended) })
			case <-ctx.Done():
			}
		}()
	}
	ticker := time.NewTicker(javasidecar.RunnerOwnerLeaseRenewInterval)
	defer ticker.Stop()
	for {
		var lost error
		select {
		case <-ctx.Done():
			return
		case <-ended:
			lost = errors.New("java_vuser shared JVM run lease lost")
		case <-ticker.C:
			call, cancel := context.WithTimeout(ctx, javasidecar.RunnerOwnerLeaseRenewInterval)
			if err := runs[0].RenewOwner(call); err != nil && ctx.Err() == nil {
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
	cancel, done, runs := g.keeper, g.keeperEnd, g.runs
	g.keeper = nil
	g.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
	var cleanup error
	if closeSharedRuns(runs) != nil {
		cleanup = errors.New("java_vuser shared JVM cleanup unconfirmed")
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
	p := JavaVMPlacement{Group: g.group, Requested: g.script.Config.JavaVuser.JVMSharing, Mode: "dedicated", Reason: g.reason, Warnings: append([]string(nil), g.warnings...)}
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
