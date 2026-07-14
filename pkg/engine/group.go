package engine

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type GroupConfig struct {
	Name            string
	Script          *dsl.Script
	VUsers          int
	RampUp          time.Duration
	RampDown        time.Duration
	AllowZeroVUsers bool
	InitStrategy    string // "at_runtime", "at_once"
	ResultSink      ResultSink
	RedactionConfig *RedactionConfig
	SecretResolver  SecretResolver
	rateLimiter     *TokenBucket
	rendezvous      *RendezvousCoordinator
	executionGoals  *ExecutionGoalLimiter
}

type ResultSink interface {
	RecordResult(result *protocol.Result) error
}

type TransactionSink interface {
	RecordTransaction(result *protocol.Result) error
}

type MultiGroupConfig struct {
	Groups       []GroupConfig
	Duration     time.Duration
	PreBaseline  time.Duration
	PostBaseline time.Duration
}

type MultiGroupEngine struct {
	groups       []GroupConfig
	duration     time.Duration
	preBaseline  time.Duration
	postBaseline time.Duration
	metrics      *MetricsCollector
	cancel       context.CancelFunc
	readyCh      chan struct{}
	readyOnce    sync.Once

	// Dynamic control state
	mu             sync.Mutex
	scaleMu        sync.Mutex
	scalingOpen    bool
	activeReserved int
	totalCtx       context.Context
	wg             *sync.WaitGroup
	vuserCancels   map[int]context.CancelFunc
	vuserGroups    map[int]string
	vuserStopping  map[int]bool
	nextVUserID    int
	paused         bool
	pauseCh        chan struct{}
	pauseStartTime time.Time
	targetDuration time.Duration
	stopVUsers     context.CancelFunc
	baseCtx        context.Context
	loopCtx        context.Context
	arrivalCtx     context.Context
	arrivalStop    context.CancelFunc
	softStop       context.CancelFunc
	mockNamespace  string
	scaleGuard     bool
}

type GroupResult struct {
	Name              string
	TotalTransactions int64
	TotalErrors       int64
}

type MultiGroupRunResult struct {
	Duration          time.Duration
	TotalTransactions int64
	TotalErrors       int64
	FinalSnapshot     MetricsSnapshot
	Groups            []GroupResult
}

func NewMultiGroup(cfg MultiGroupConfig) (*MultiGroupEngine, error) {
	if len(cfg.Groups) == 0 {
		return nil, fmt.Errorf("at least one group is required")
	}
	if cfg.Duration <= 0 {
		return nil, fmt.Errorf("duration must be positive")
	}
	if cfg.PreBaseline < 0 {
		return nil, fmt.Errorf("pre-baseline must be non-negative")
	}
	if cfg.PostBaseline < 0 {
		return nil, fmt.Errorf("post-baseline must be non-negative")
	}
	for i, g := range cfg.Groups {
		if g.Script == nil {
			return nil, fmt.Errorf("group[%d] %q: script is required", i, g.Name)
		}
		if g.VUsers < 0 {
			return nil, fmt.Errorf("group[%d] %q: vusers must be zero or greater", i, g.Name)
		}
		if g.VUsers == 0 && !g.AllowZeroVUsers {
			return nil, fmt.Errorf("group[%d] %q: vusers must be positive", i, g.Name)
		}
		if g.VUsers == 0 && isOpenWorkload(g.Script) {
			return nil, fmt.Errorf("group[%d] %q: open workload groups require positive vusers", i, g.Name)
		}
		if g.Name == "" {
			cfg.Groups[i].Name = g.Script.Name
		}
	}
	sharedSecretResolver := newDefaultSecretResolver()
	for i := range cfg.Groups {
		if cfg.Groups[i].SecretResolver == nil {
			cfg.Groups[i].SecretResolver = sharedSecretResolver
		}
	}
	mge := &MultiGroupEngine{
		groups:       cfg.Groups,
		duration:     cfg.Duration,
		preBaseline:  cfg.PreBaseline,
		postBaseline: cfg.PostBaseline,
		metrics:      NewMetricsCollector(10000),
		readyCh:      make(chan struct{}),
	}
	mge.metrics.SetGroupActiveVUsersProvider(mge.activeVUserCountByGroup)
	return mge, nil
}

// Ready is closed once Run has initialized the internal contexts required by
// runtime controls such as ScaleVUsers.
func (mge *MultiGroupEngine) Ready() <-chan struct{} {
	return mge.readyCh
}

// activeVUserCountByGroup returns the live active VUser count per group, derived
// from the live vuser→group map. Safe to call concurrently with spawn/teardown.
func (mge *MultiGroupEngine) activeVUserCountByGroup() map[string]int {
	mge.mu.Lock()
	defer mge.mu.Unlock()
	counts := make(map[string]int, len(mge.groups))
	for _, group := range mge.vuserGroups {
		counts[group]++
	}
	return counts
}

func (mge *MultiGroupEngine) Run(ctx context.Context) (*MultiGroupRunResult, error) {
	mge.metrics.EnableIdleSnapshots(mge.preBaseline > 0 || mge.postBaseline > 0)
	mge.metrics.Start(time.Second)
	defer mge.metrics.Stop()

	var wg sync.WaitGroup

	mge.mu.Lock()
	mge.vuserCancels = make(map[int]context.CancelFunc)
	mge.vuserGroups = make(map[int]string)
	mge.vuserStopping = make(map[int]bool)
	mge.nextVUserID = 0
	mge.pauseCh = make(chan struct{})
	close(mge.pauseCh) // Start unpaused
	mge.targetDuration = mge.duration
	mge.mockNamespace = newRuntimeMockNamespace()
	mge.scalingOpen = true
	mge.activeReserved = 0
	mge.scaleGuard = mge.hasZeroCapableClosedGroup()

	// Create a base context for VUsers that only cancels on hard stop/abort
	baseCtx, baseCancel := context.WithCancel(ctx)
	loopCtx, loopCancel := context.WithCancel(baseCtx)
	arrivalCtx, arrivalCancel := context.WithCancel(baseCtx)
	softStopAll := func() {
		loopCancel()
		arrivalCancel()
	}
	mge.baseCtx = baseCtx
	mge.loopCtx = loopCtx
	mge.arrivalCtx = arrivalCtx
	mge.arrivalStop = arrivalCancel
	mge.cancel = baseCancel
	mge.softStop = softStopAll
	// Publish mge.wg under the lock alongside baseCtx so any ScaleVUsers caller
	// that observes a running baseCtx also sees a non-nil wg (avoids a nil-deref
	// in spawnVUser's wg.Add and a data race on the field).
	mge.wg = &wg
	for i := range mge.groups {
		mge.groups[i].executionGoals = NewExecutionGoalLimiter(mge.groups[i].Script.Config.RunLogic, softStopAll)
		if !isOpenWorkload(mge.groups[i].Script) {
			mge.groups[i].rendezvous = NewRendezvousCoordinator()
			mge.groups[i].rateLimiter = newIterationRateLimiter(mge.groups[i].Script)
		}
	}
	runGroups := append([]GroupConfig(nil), mge.groups...)
	mge.stopVUsers = baseCancel
	mge.mu.Unlock()
	for _, group := range runGroups {
		if group.rateLimiter != nil {
			defer group.rateLimiter.Stop()
		}
	}
	releaseMockNamespace := protocol.AcquireRuntimeNamespace(mge.mockNamespace)
	defer releaseMockNamespace()
	go func() {
		<-baseCtx.Done()
		mge.mu.Lock()
		mge.scalingOpen = false
		mge.mu.Unlock()
	}()

	defer baseCancel()
	defer loopCancel()
	defer arrivalCancel()
	defer func() {
		mge.mu.Lock()
		mge.softStop = nil
		mge.arrivalStop = nil
		mge.mu.Unlock()
	}()

	start := time.Now()
	groupResults := make([]GroupResult, len(mge.groups))

	// Background goroutine manages the steady-state deadline and then performs
	// a real progressive ramp-down before the hard stop.
	go func() {
		for {
			mge.mu.Lock()
			if mge.baseCtx.Err() != nil {
				mge.mu.Unlock()
				return
			}
			paused := mge.paused
			deadline := start.Add(mge.preBaseline + mge.targetDuration)
			mge.mu.Unlock()

			if !paused && time.Now().After(deadline) {
				arrivalCancel()
				mge.rampDownVUsers()
				baseCancel()
				return
			}

			if !sleepContext(baseCtx, 100*time.Millisecond) {
				return
			}
		}
	}()

	if err := waitForBaselineWindow(ctx, mge.preBaseline); err != nil {
		return nil, err
	}
	mge.readyOnce.Do(func() {
		close(mge.readyCh)
	})

	if mge.scaleGuard {
		wg.Add(1)
		go mge.holdScaleGuard(mge.loopCtx, &wg)
	}

	totalTargetTPS := 0.0
	atOnceGroups := make([]GroupConfig, 0)
	for gi, group := range runGroups {
		if group.Script.Config.Pacing.Type == "tps" && group.Script.Config.Pacing.TPS > 0 {
			targetTPS := group.Script.Config.Pacing.TPS
			totalTargetTPS += targetTPS
			mge.metrics.SetGroupTargetTPS(group.Name, targetTPS)
		}
		groupResults[gi] = GroupResult{Name: group.Name}

		protoConfig := buildProtoConfigForRunModeWithSecretResolver(group.Script, "load", group.SecretResolver)
		applyRuntimeMockNamespace(protoConfig, mge.mockNamespace)
		if groupLifecycleAtOnce(group) {
			if !mge.runGroupLifecycleBlock(mge.baseCtx, group, protoConfig, group.Script.Init, "init") {
				baseCancel()
				return nil, fmt.Errorf("group %q init-once failed", group.Name)
			}
			atOnceGroups = append(atOnceGroups, group)
		}
		if isOpenWorkload(group.Script) {
			arrivals, bounded := boundedArrivalLimit(group.Script, group.VUsers)
			inFlightLimit := openWorkloadInFlightLimit(group.Script)
			limiter := newInFlightLimiter(inFlightLimit)
			wg.Add(1)
			go func(group GroupConfig, protoConfig map[string]any, totalArrivals int, limiter *inFlightLimiter, inFlightLimit int) {
				defer wg.Done()
				runArrivalScheduler(mge.arrivalCtx, group.Script.Config.Pacing.TPS, totalArrivals, mge.WaitIfPaused, func(int) {
					// Shed the arrival when the in-flight set is saturated: the SUT
					// cannot keep up, so dropping it protects the generator from
					// unbounded goroutine growth instead of silently throttling to a
					// closed model. The reserved slot is released by spawnOpenWorker
					// on every exit path.
					if !limiter.tryAcquire() {
						mge.metrics.RecordSheddedArrival()
						if limiter.shouldWarn() {
							slog.Warn("open workload shedding arrivals: system under test cannot keep up; capping in-flight workers to protect the generator", "group", group.Name, "max_in_flight", inFlightLimit, "tps", group.Script.Config.Pacing.TPS)
						}
						return
					}
					vuserID := mge.allocateVUserID()
					mge.spawnOpenWorker(vuserID, group, protoConfig, &wg, limiter.release)
				})
				if shed := limiter.Shed(); shed > 0 {
					slog.Warn("open workload shed arrivals over the run (offered TPS exceeded achievable throughput)", "group", group.Name, "shed", shed, "max_in_flight", inFlightLimit)
				}
			}(group, protoConfig, func() int {
				if bounded {
					return arrivals
				}
				return 0
			}(), limiter, inFlightLimit)
			continue
		}

		rampDelay := time.Duration(0)
		if group.RampUp > 0 && group.VUsers > 1 {
			rampDelay = group.RampUp / time.Duration(group.VUsers)
		}

		for i := 0; i < group.VUsers; i++ {
			if mge.loopCtx.Err() != nil {
				break
			}

			if i > 0 && rampDelay > 0 && !sleepContext(mge.loopCtx, rampDelay) {
				break
			}

			vuserID := mge.allocateVUserID()
			mge.spawnVUser(vuserID, group, protoConfig, &wg, false)
		}
	}
	if totalTargetTPS > 0 {
		mge.metrics.SetTargetTPS(totalTargetTPS)
	}

	wg.Wait()
	for _, group := range atOnceGroups {
		protoConfig := buildProtoConfigForRunModeWithSecretResolver(group.Script, "load", group.SecretResolver)
		applyRuntimeMockNamespace(protoConfig, mge.mockNamespace)
		tctx, tcancel := teardownContext()
		mge.runGroupLifecycleBlock(tctx, group, protoConfig, group.Script.Teardown, "teardown")
		tcancel()
	}
	if err := waitForBaselineWindow(ctx, mge.postBaseline); err != nil {
		return nil, err
	}
	elapsed := time.Since(start)
	mge.metrics.Stop()

	result := &MultiGroupRunResult{
		Duration:          elapsed,
		TotalTransactions: mge.metrics.TotalTransactions(),
		TotalErrors:       mge.metrics.TotalErrors(),
		Groups:            groupResults,
	}
	if snap, ok := mge.metrics.LatestSnapshot(); ok {
		result.FinalSnapshot = snap
	}
	return result, nil
}

func (mge *MultiGroupEngine) holdScaleGuard(loopCtx context.Context, wg *sync.WaitGroup) {
	<-loopCtx.Done()
	mge.mu.Lock()
	mge.scalingOpen = false
	mge.mu.Unlock()
	wg.Done()
}

func (mge *MultiGroupEngine) hasZeroCapableClosedGroup() bool {
	for _, group := range mge.groups {
		if group.AllowZeroVUsers && !isOpenWorkload(group.Script) {
			return true
		}
	}
	return false
}

func groupLifecycleAtOnce(group GroupConfig) bool {
	return strings.EqualFold(strings.TrimSpace(group.InitStrategy), "at_once")
}

func (mge *MultiGroupEngine) runGroupLifecycleBlock(ctx context.Context, group GroupConfig, protoConfig map[string]any, actions []dsl.Action, phase string) bool {
	if len(actions) == 0 {
		return true
	}
	vuProto, err := protocol.Get(group.Script.Protocol)
	if err != nil {
		slog.Error("failed to create lifecycle protocol", "group", group.Name, "phase", phase, "err", err)
		return false
	}
	if err := vuProto.Init(ctx, protoConfig); err != nil {
		slog.Error("failed to init lifecycle protocol", "group", group.Name, "phase", phase, "err", err)
		return false
	}
	defer runProtocolTeardown(vuProto)

	vu := &VUser{
		ID:              -1,
		TotalVUsers:     group.VUsers,
		Group:           group.Name,
		Proto:           vuProto,
		Script:          group.Script,
		Metrics:         mge.metrics,
		ResultSink:      group.ResultSink,
		RedactionConfig: group.RedactionConfig,
		SecretResolver:  group.SecretResolver,
		Rendezvous:      group.rendezvous,
	}
	return vu.RunLifecycleBlock(ctx, actions, phase)
}

func waitForBaselineWindow(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(duration):
		return nil
	}
}

func (mge *MultiGroupEngine) spawnVUser(vuserID int, group GroupConfig, protoConfig map[string]any, wg *sync.WaitGroup, reserved bool) {
	if !reserved {
		mge.mu.Lock()
		mge.activeReserved++
		wg.Add(1)
		mge.mu.Unlock()
	}
	parentCtx := mge.loopCtx
	if parentCtx == nil {
		parentCtx = mge.baseCtx
	}
	vuCtx, vuCancel := context.WithCancel(parentCtx)
	// shedCtx interrupts the in-flight iteration body on scale/ramp-down and on
	// hard stops. It is a child of baseCtx (NOT loopCtx, so NOT a descendant of
	// vuCtx): a shared-goal exhaustion cancels loopCtx — and thus vuCtx — which
	// stops new iterations at the top of the loop, but leaves shedCtx alive so a
	// VUser that already claimed a goal reservation finishes its current
	// iteration. cancelVUser sheds load promptly by cancelling BOTH shedCtx (the
	// running iteration) and vuCtx (no further iterations).
	shedCtx, shedCancel := context.WithCancel(mge.baseCtx)
	cancelVUser := func() {
		shedCancel()
		vuCancel()
	}

	mge.mu.Lock()
	mge.vuserCancels[vuserID] = cancelVUser
	mge.vuserGroups[vuserID] = group.Name
	mge.mu.Unlock()

	vuProto, err := protocol.Get(group.Script.Protocol)
	if err != nil {
		slog.Error("failed to create protocol", "group", group.Name, "vuser", vuserID, "err", err)
		cancelVUser()
		mge.finishReservedVUser(vuserID, wg)
		return
	}
	if err := vuProto.Init(vuCtx, vuserProtoConfig(protoConfig, vuserID)); err != nil {
		slog.Error("failed to init protocol", "group", group.Name, "vuser", vuserID, "err", err)
		cancelVUser()
		mge.finishReservedVUser(vuserID, wg)
		return
	}

	mge.mu.Lock()
	activeVUsers := mge.activeVUserCount()
	mge.mu.Unlock()
	mge.metrics.SetActiveVUsers(int64(activeVUsers))

	go func() {
		defer recoverVUserGoroutine(vuserID)
		defer cancelVUser()
		defer func() {
			runProtocolTeardown(vuProto)
			mge.finishReservedVUser(vuserID, wg)
		}()

		vu := &VUser{
			ID:                   vuserID,
			TotalVUsers:          group.VUsers,
			Group:                group.Name,
			Proto:                vuProto,
			Script:               group.Script,
			Metrics:              mge.metrics,
			ResultSink:           group.ResultSink,
			RedactionConfig:      group.RedactionConfig,
			SecretResolver:       group.SecretResolver,
			PauseControl:         mge,
			IterationRateLimiter: group.rateLimiter,
			Rendezvous:           group.rendezvous,
			ExecutionGoals:       group.executionGoals,
			HardStopCtx:          shedCtx,
			SkipInit:             groupLifecycleAtOnce(group),
			SkipTeardown:         groupLifecycleAtOnce(group),
		}
		vu.Run(vuCtx)
	}()
}

func (mge *MultiGroupEngine) finishReservedVUser(vuserID int, wg *sync.WaitGroup) {
	mge.mu.Lock()
	delete(mge.vuserCancels, vuserID)
	delete(mge.vuserGroups, vuserID)
	delete(mge.vuserStopping, vuserID)
	if mge.activeReserved > 0 {
		mge.activeReserved--
	}
	if mge.activeReserved == 0 && !mge.scaleGuard {
		mge.scalingOpen = false
	}
	mge.metrics.SetActiveVUsers(int64(mge.activeVUserCount()))
	wg.Done()
	mge.mu.Unlock()
}

func (mge *MultiGroupEngine) rampDownVUsers() {
	type rampGroup struct {
		duration time.Duration
		cancels  []context.CancelFunc
	}
	mge.mu.Lock()
	ramps := make(map[string]*rampGroup, len(mge.groups))
	for _, group := range mge.groups {
		ramps[group.Name] = &rampGroup{duration: group.RampDown}
	}
	ids := make([]int, 0, len(mge.vuserCancels))
	for id := range mge.vuserCancels {
		ids = append(ids, id)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(ids)))
	for _, id := range ids {
		group := ramps[mge.vuserGroups[id]]
		if group != nil {
			mge.vuserStopping[id] = true
			group.cancels = append(group.cancels, mge.vuserCancels[id])
		}
	}
	mge.mu.Unlock()

	var rampsWG sync.WaitGroup
	for _, group := range ramps {
		group := group
		if len(group.cancels) == 0 {
			continue
		}
		rampsWG.Add(1)
		go func() {
			defer rampsWG.Done()
			if group.duration <= 0 {
				for _, cancel := range group.cancels {
					cancel()
				}
				return
			}
			delay := group.duration / time.Duration(len(group.cancels))
			for _, cancel := range group.cancels {
				if !sleepContext(mge.baseCtx, delay) {
					return
				}
				cancel()
			}
		}()
	}
	rampsWG.Wait()
}

func sleepContext(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// activeVUserCount returns the number of live VUsers. Callers MUST hold mge.mu;
// the spawn/teardown paths mutate vuserCancels under that lock.
func (mge *MultiGroupEngine) activeVUserCount() int {
	return len(mge.vuserCancels)
}

func (mge *MultiGroupEngine) allocateVUserID() int {
	mge.mu.Lock()
	defer mge.mu.Unlock()
	id := mge.nextVUserID
	mge.nextVUserID++
	return id
}

func (mge *MultiGroupEngine) spawnOpenWorker(vuserID int, group GroupConfig, protoConfig map[string]any, wg *sync.WaitGroup, release func()) {
	// release returns the in-flight slot reserved by the caller; it must run
	// exactly once across every exit path (including the early protocol
	// failures below) so the limiter does not leak capacity.
	if release == nil {
		release = func() {}
	}
	parentCtx := mge.loopCtx
	if parentCtx == nil {
		parentCtx = mge.baseCtx
	}
	vuCtx, vuCancel := context.WithCancel(parentCtx)
	// See spawnVUser: shedCtx interrupts the running iteration on stop/hard-stop
	// without being tripped by a shared-goal soft stop.
	shedCtx, shedCancel := context.WithCancel(mge.baseCtx)
	cancelVUser := func() {
		shedCancel()
		vuCancel()
	}

	mge.mu.Lock()
	mge.vuserCancels[vuserID] = cancelVUser
	mge.vuserGroups[vuserID] = group.Name
	mge.metrics.SetActiveVUsers(int64(mge.activeVUserCount()))
	mge.mu.Unlock()

	vuProto, err := protocol.Get(group.Script.Protocol)
	if err != nil {
		slog.Error("failed to create protocol for open workload worker", "group", group.Name, "worker", vuserID, "err", err)
		cancelVUser()
		mge.mu.Lock()
		delete(mge.vuserCancels, vuserID)
		delete(mge.vuserGroups, vuserID)
		delete(mge.vuserStopping, vuserID)
		mge.metrics.SetActiveVUsers(int64(mge.activeVUserCount()))
		mge.mu.Unlock()
		release()
		return
	}
	if err := vuProto.Init(vuCtx, vuserProtoConfig(protoConfig, vuserID)); err != nil {
		slog.Error("failed to init protocol for open workload worker", "group", group.Name, "worker", vuserID, "err", err)
		cancelVUser()
		mge.mu.Lock()
		delete(mge.vuserCancels, vuserID)
		delete(mge.vuserGroups, vuserID)
		delete(mge.vuserStopping, vuserID)
		mge.metrics.SetActiveVUsers(int64(mge.activeVUserCount()))
		mge.mu.Unlock()
		release()
		return
	}

	iterationScript := cloneSingleIterationScript(group.Script)
	wg.Add(1)
	go func() {
		defer recoverVUserGoroutine(vuserID)
		defer wg.Done()
		defer release()
		defer cancelVUser()
		defer func() {
			runProtocolTeardown(vuProto)
			mge.mu.Lock()
			delete(mge.vuserCancels, vuserID)
			delete(mge.vuserGroups, vuserID)
			delete(mge.vuserStopping, vuserID)
			mge.metrics.SetActiveVUsers(int64(mge.activeVUserCount()))
			mge.mu.Unlock()
		}()

		vu := &VUser{
			ID:              vuserID,
			Group:           group.Name,
			Proto:           vuProto,
			Script:          iterationScript,
			Metrics:         mge.metrics,
			ResultSink:      group.ResultSink,
			RedactionConfig: group.RedactionConfig,
			SecretResolver:  group.SecretResolver,
			PauseControl:    mge,
			ExecutionGoals:  group.executionGoals,
			HardStopCtx:     shedCtx,
			SkipInit:        groupLifecycleAtOnce(group),
			SkipTeardown:    groupLifecycleAtOnce(group),
		}
		vu.Run(vuCtx)
	}()
}

// ScaleVUsers adds or removes VUsers for a specific group.
// If targetVUsers > current, new VUsers are spawned.
// If targetVUsers < current, excess VUsers are cancelled (LIFO).
func (mge *MultiGroupEngine) ScaleVUsers(groupName string, targetVUsers int) error {
	mge.scaleMu.Lock()
	defer mge.scaleMu.Unlock()
	mge.mu.Lock()
	defer mge.mu.Unlock()

	if mge.baseCtx == nil || mge.baseCtx.Err() != nil || mge.wg == nil || !mge.scalingOpen {
		return fmt.Errorf("engine not running")
	}

	var group *GroupConfig
	for i := range mge.groups {
		if mge.groups[i].Name == groupName {
			group = &mge.groups[i]
			break
		}
	}
	if group == nil {
		return fmt.Errorf("group not found: %s", groupName)
	}
	if isOpenWorkload(group.Script) {
		return fmt.Errorf("scaling open workload groups is not supported")
	}
	if targetVUsers < 0 {
		return fmt.Errorf("target vusers must be non-negative")
	}
	if targetVUsers == 0 && !mge.scaleGuard {
		// Keep Run's WaitGroup non-zero while the group is paused at zero so a
		// later resume can safely Add VUsers. Installing the guard while at least
		// one VUser is still reserved avoids Add racing a completed Wait.
		mge.scaleGuard = true
		mge.wg.Add(1)
		go mge.holdScaleGuard(mge.loopCtx, mge.wg)
	}

	currentVUsers := group.VUsers
	delta := targetVUsers - currentVUsers
	group.VUsers = targetVUsers

	if delta > 0 {
		protoConfig := buildProtoConfigForRunModeWithSecretResolver(group.Script, "load", group.SecretResolver)
		applyRuntimeMockNamespace(protoConfig, mge.mockNamespace)

		for i := 0; i < delta; i++ {
			vuserID := mge.nextVUserID
			mge.nextVUserID++
			mge.activeReserved++
			mge.wg.Add(1)

			mge.mu.Unlock()
			mge.spawnVUser(vuserID, *group, protoConfig, mge.wg, true)
			mge.mu.Lock()
		}
	} else if delta < 0 {
		toRemove := -delta
		ids := make([]int, 0, toRemove)
		for id := range mge.vuserCancels {
			if mge.vuserGroups[id] == groupName && !mge.vuserStopping[id] {
				ids = append(ids, id)
			}
		}
		sort.Sort(sort.Reverse(sort.IntSlice(ids)))
		if len(ids) < toRemove {
			group.VUsers = currentVUsers
			return fmt.Errorf("group %q only has %d active vusers available to remove", groupName, len(ids))
		}
		for _, id := range ids[:toRemove] {
			mge.vuserStopping[id] = true
			mge.vuserCancels[id]()
		}
	}

	slog.Info("vuser count adjusted", "group", groupName, "from", currentVUsers, "to", targetVUsers)
	return nil
}

// AdjustDuration extends or shortens the remaining test duration.
// The new duration is relative to the original start time.
func (mge *MultiGroupEngine) AdjustDuration(newDuration time.Duration) error {
	mge.mu.Lock()
	defer mge.mu.Unlock()

	return mge.adjustDurationLocked(newDuration)
}

func (mge *MultiGroupEngine) adjustDurationLocked(newDuration time.Duration) error {
	if mge.baseCtx == nil || mge.baseCtx.Err() != nil {
		return fmt.Errorf("engine not running")
	}

	mge.targetDuration = newDuration

	slog.Info("test duration adjusted", "new_duration", newDuration)
	return nil
}

// GetTargetDuration returns the current target duration (may differ from original if adjusted).
func (mge *MultiGroupEngine) GetTargetDuration() time.Duration {
	mge.mu.Lock()
	defer mge.mu.Unlock()
	return mge.targetDuration
}

// GetGroupVUsers returns the current VUser count per group.
func (mge *MultiGroupEngine) GetGroupVUsers() map[string]int {
	mge.mu.Lock()
	defer mge.mu.Unlock()
	result := make(map[string]int, len(mge.groups))
	for _, g := range mge.groups {
		result[g.Name] = g.VUsers
	}
	return result
}

func (mge *MultiGroupEngine) Pause() error {
	mge.mu.Lock()
	defer mge.mu.Unlock()

	if mge.paused {
		return nil
	}

	mge.paused = true
	mge.pauseStartTime = time.Now()
	mge.pauseCh = make(chan struct{}) // New channel blocks waiters

	slog.Info("test execution paused")
	return nil
}

func (mge *MultiGroupEngine) Resume() error {
	mge.mu.Lock()
	defer mge.mu.Unlock()

	if !mge.paused {
		return nil
	}

	pauseDuration := time.Since(mge.pauseStartTime)
	mge.paused = false
	close(mge.pauseCh) // Closing unblocks all waiters

	// Adjust duration to account for time spent paused
	newDuration := mge.targetDuration + pauseDuration
	err := mge.adjustDurationLocked(newDuration)

	slog.Info("test execution resumed", "pause_duration", pauseDuration)
	return err
}

func (mge *MultiGroupEngine) WaitIfPaused(ctx context.Context) error {
	for {
		mge.mu.Lock()
		paused := mge.paused
		pauseCh := mge.pauseCh
		mge.mu.Unlock()

		if !paused {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-pauseCh:
			// Unblocked! Check again in case it was re-paused immediately
		}
	}
}

func (mge *MultiGroupEngine) Stop() {
	mge.mu.Lock()
	cancel := mge.cancel
	mge.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (mge *MultiGroupEngine) GracefulStop() {
	mge.mu.Lock()
	cancel := mge.softStop
	mge.mu.Unlock()
	if cancel != nil {
		cancel()
		return
	}
	mge.Stop()
}

func (mge *MultiGroupEngine) LatestSnapshot() (MetricsSnapshot, bool) {
	return mge.metrics.LatestSnapshot()
}

func (mge *MultiGroupEngine) GroupNames() []string {
	names := make([]string, len(mge.groups))
	for i, g := range mge.groups {
		names[i] = g.Name
	}
	return names
}
