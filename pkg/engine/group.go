package engine

import (
	"context"
	"fmt"
	"log/slog"
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
	InitStrategy    string // "at_runtime", "at_once"
	ResultSink      ResultSink
	RedactionConfig *RedactionConfig
	rateLimiter     *TokenBucket
	rendezvous      *RendezvousCoordinator
	executionGoals  *ExecutionGoalLimiter
}

type ResultSink interface {
	RecordResult(result *protocol.Result) error
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

	// Dynamic control state
	mu             sync.Mutex
	totalCtx       context.Context
	wg             *sync.WaitGroup
	vuserCancels   map[int]context.CancelFunc
	vuserGroups    map[int]string
	nextVUserID    int
	paused         bool
	pauseCh        chan struct{}
	pauseStartTime time.Time
	targetDuration time.Duration
	stopVUsers     context.CancelFunc
	baseCtx        context.Context
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
		if g.VUsers <= 0 {
			return nil, fmt.Errorf("group[%d] %q: vusers must be positive", i, g.Name)
		}
		if g.Name == "" {
			cfg.Groups[i].Name = g.Script.Name
		}
	}
	return &MultiGroupEngine{
		groups:       cfg.Groups,
		duration:     cfg.Duration,
		preBaseline:  cfg.PreBaseline,
		postBaseline: cfg.PostBaseline,
		metrics:      NewMetricsCollector(10000),
	}, nil
}

func (mge *MultiGroupEngine) Run(ctx context.Context) (*MultiGroupRunResult, error) {
	mge.metrics.EnableIdleSnapshots(mge.preBaseline > 0 || mge.postBaseline > 0)
	mge.metrics.Start(time.Second)
	defer mge.metrics.Stop()

	mge.mu.Lock()
	mge.vuserCancels = make(map[int]context.CancelFunc)
	mge.vuserGroups = make(map[int]string)
	mge.nextVUserID = 0
	mge.pauseCh = make(chan struct{})
	close(mge.pauseCh) // Start unpaused
	mge.targetDuration = mge.duration

	// Create a base context for VUsers that only cancels on hard stop/abort
	baseCtx, baseCancel := context.WithCancel(ctx)
	mge.baseCtx = baseCtx
	for i := range mge.groups {
		mge.groups[i].executionGoals = NewExecutionGoalLimiter(mge.groups[i].Script.Config.RunLogic, baseCancel)
		if !isOpenWorkload(mge.groups[i].Script) {
			mge.groups[i].rendezvous = NewRendezvousCoordinator()
		}
	}
	mge.stopVUsers = baseCancel
	mge.mu.Unlock()

	defer baseCancel()

	start := time.Now()
	var wg sync.WaitGroup
	mge.wg = &wg
	groupResults := make([]GroupResult, len(mge.groups))

	maxRampDown := time.Duration(0)
	for _, g := range mge.groups {
		if g.RampDown > maxRampDown {
			maxRampDown = g.RampDown
		}
	}

	// background goroutine to manage the test deadline
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
				baseCancel()
				return
			}

			time.Sleep(100 * time.Millisecond)
		}
	}()

	if err := waitForBaselineWindow(ctx, mge.preBaseline); err != nil {
		return nil, err
	}

	totalTargetTPS := 0.0
	atOnceGroups := make([]GroupConfig, 0)
	for gi, _ := range mge.groups {
		if mge.groups[gi].Script.Config.Pacing.Type == "tps" && mge.groups[gi].Script.Config.Pacing.TPS > 0 {
			targetTPS := mge.groups[gi].Script.Config.Pacing.TPS
			totalTargetTPS += targetTPS
			mge.metrics.SetGroupTargetTPS(mge.groups[gi].Name, targetTPS)
		}
		if !isOpenWorkload(mge.groups[gi].Script) {
			mge.groups[gi].rateLimiter = newIterationRateLimiter(mge.groups[gi].Script)
			if mge.groups[gi].rateLimiter != nil {
				defer mge.groups[gi].rateLimiter.Stop()
			}
		}
		gi, group := gi, mge.groups[gi]
		groupResults[gi] = GroupResult{Name: group.Name}

		protoConfig := buildProtoConfig(group.Script)
		if groupLifecycleAtOnce(group) {
			if !mge.runGroupLifecycleBlock(mge.baseCtx, group, protoConfig, group.Script.Init, "init") {
				baseCancel()
				return nil, fmt.Errorf("group %q init-once failed", group.Name)
			}
			atOnceGroups = append(atOnceGroups, group)
		}
		if isOpenWorkload(group.Script) {
			arrivals, bounded := boundedArrivalLimit(group.Script, group.VUsers)
			wg.Add(1)
			go func(group GroupConfig, protoConfig map[string]any, totalArrivals int) {
				defer wg.Done()
				runArrivalScheduler(mge.baseCtx, group.Script.Config.Pacing.TPS, totalArrivals, mge.WaitIfPaused, func(int) {
					vuserID := mge.allocateVUserID()
					mge.spawnOpenWorker(vuserID, group, protoConfig, &wg)
				})
			}(group, protoConfig, func() int {
				if bounded {
					return arrivals
				}
				return 0
			}())
			continue
		}

		rampDelay := time.Duration(0)
		if group.RampUp > 0 && group.VUsers > 1 {
			rampDelay = group.RampUp / time.Duration(group.VUsers)
		}

		rampDownDelay := time.Duration(0)
		if group.RampDown > 0 && group.VUsers > 1 {
			rampDownDelay = group.RampDown / time.Duration(group.VUsers)
		}

		for i := 0; i < group.VUsers; i++ {
			if mge.baseCtx.Err() != nil {
				break
			}

			if i > 0 && rampDelay > 0 {
				time.Sleep(rampDelay)
			}

			vuserID := mge.allocateVUserID()
			mge.spawnVUser(vuserID, group, protoConfig, rampDownDelay, i, &wg)
		}
	}
	if totalTargetTPS > 0 {
		mge.metrics.SetTargetTPS(totalTargetTPS)
	}

	wg.Wait()
	for _, group := range atOnceGroups {
		protoConfig := buildProtoConfig(group.Script)
		mge.runGroupLifecycleBlock(context.Background(), group, protoConfig, group.Script.Teardown, "teardown")
	}
	if err := waitForBaselineWindow(ctx, mge.postBaseline); err != nil {
		return nil, err
	}
	elapsed := time.Since(start)

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
	defer vuProto.Teardown(context.Background())

	vu := &VUser{
		ID:              -1,
		TotalVUsers:     group.VUsers,
		Group:           group.Name,
		Proto:           vuProto,
		Script:          group.Script,
		Metrics:         mge.metrics,
		ResultSink:      group.ResultSink,
		RedactionConfig: group.RedactionConfig,
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

func (mge *MultiGroupEngine) spawnVUser(vuserID int, group GroupConfig, protoConfig map[string]any, rampDownDelay time.Duration, rampIdx int, wg *sync.WaitGroup) {
	vuCtx, vuCancel := context.WithCancel(mge.baseCtx)

	mge.mu.Lock()
	mge.vuserCancels[vuserID] = vuCancel
	mge.vuserGroups[vuserID] = group.Name
	mge.mu.Unlock()

	if rampDownDelay > 0 {
		go func(idx int, cancelFunc context.CancelFunc) {
			// Wait for steady state to end (base context cancellation)
			<-mge.baseCtx.Done()

			// Optional: if aborted, don't do ramp down
			// But for now, we'll just follow the pattern
			if rampDownDelay > 0 {
				time.Sleep(time.Duration(idx) * rampDownDelay)
			}
			cancelFunc()
		}(rampIdx, vuCancel)
	}

	vuProto, err := protocol.Get(group.Script.Protocol)
	if err != nil {
		slog.Error("failed to create protocol", "group", group.Name, "vuser", vuserID, "err", err)
		vuCancel()
		return
	}
	if err := vuProto.Init(vuCtx, vuserProtoConfig(protoConfig, vuserID)); err != nil {
		slog.Error("failed to init protocol", "group", group.Name, "vuser", vuserID, "err", err)
		vuCancel()
		return
	}

	wg.Add(1)
	mge.metrics.SetActiveVUsers(int64(mge.activeVUserCount()))

	go func() {
		defer wg.Done()
		defer func() {
			vuProto.Teardown(context.Background())
			mge.mu.Lock()
			delete(mge.vuserCancels, vuserID)
			delete(mge.vuserGroups, vuserID)
			mge.metrics.SetActiveVUsers(int64(mge.activeVUserCount()))
			mge.mu.Unlock()
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
			PauseControl:         mge,
			IterationRateLimiter: group.rateLimiter,
			Rendezvous:           group.rendezvous,
			ExecutionGoals:       group.executionGoals,
			SkipInit:             groupLifecycleAtOnce(group),
			SkipTeardown:         groupLifecycleAtOnce(group),
		}
		vu.Run(vuCtx)
	}()
}

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

func (mge *MultiGroupEngine) spawnOpenWorker(vuserID int, group GroupConfig, protoConfig map[string]any, wg *sync.WaitGroup) {
	vuCtx, vuCancel := context.WithCancel(mge.baseCtx)

	mge.mu.Lock()
	mge.vuserCancels[vuserID] = vuCancel
	mge.vuserGroups[vuserID] = group.Name
	mge.metrics.SetActiveVUsers(int64(mge.activeVUserCount()))
	mge.mu.Unlock()

	vuProto, err := protocol.Get(group.Script.Protocol)
	if err != nil {
		slog.Error("failed to create protocol for open workload worker", "group", group.Name, "worker", vuserID, "err", err)
		vuCancel()
		mge.mu.Lock()
		delete(mge.vuserCancels, vuserID)
		delete(mge.vuserGroups, vuserID)
		mge.metrics.SetActiveVUsers(int64(mge.activeVUserCount()))
		mge.mu.Unlock()
		return
	}
	if err := vuProto.Init(vuCtx, vuserProtoConfig(protoConfig, vuserID)); err != nil {
		slog.Error("failed to init protocol for open workload worker", "group", group.Name, "worker", vuserID, "err", err)
		vuCancel()
		mge.mu.Lock()
		delete(mge.vuserCancels, vuserID)
		delete(mge.vuserGroups, vuserID)
		mge.metrics.SetActiveVUsers(int64(mge.activeVUserCount()))
		mge.mu.Unlock()
		return
	}

	iterationScript := cloneSingleIterationScript(group.Script)
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			vuProto.Teardown(context.Background())
			mge.mu.Lock()
			delete(mge.vuserCancels, vuserID)
			delete(mge.vuserGroups, vuserID)
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
			PauseControl:    mge,
			ExecutionGoals:  group.executionGoals,
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
	mge.mu.Lock()
	defer mge.mu.Unlock()

	if mge.baseCtx == nil || mge.baseCtx.Err() != nil {
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

	currentVUsers := group.VUsers
	delta := targetVUsers - currentVUsers

	if delta > 0 {
		protoConfig := buildProtoConfig(group.Script)

		for i := 0; i < delta; i++ {
			vuserID := mge.nextVUserID
			mge.nextVUserID++

			mge.mu.Unlock()
			mge.spawnVUser(vuserID, *group, protoConfig, 0, 0, mge.wg)
			mge.mu.Lock()
		}
	} else if delta < 0 {
		toRemove := -delta
		removed := 0
		for id, cancel := range mge.vuserCancels {
			if removed >= toRemove {
				break
			}
			if mge.vuserGroups[id] != groupName {
				continue
			}
			cancel()
			delete(mge.vuserCancels, id)
			delete(mge.vuserGroups, id)
			removed++
		}
	}

	group.VUsers = targetVUsers
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
	if mge.cancel != nil {
		mge.cancel()
	}
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
