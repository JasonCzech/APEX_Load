package engine

import (
	"container/heap"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

const maxMultiGroupRuntimeSecretRedactionBytes = 32 << 20

var ErrInitialRampInProgress = errors.New("initial vuser ramp is still in progress")

type GroupConfig struct {
	Name string
	// TransactionResponseTimeSLAMs is the already-resolved response-time SLA
	// for every named business transaction in this group. Zero disables it.
	TransactionResponseTimeSLAMs float64
	// StartOffset delays group activation from the common load epoch. It is
	// measured in active test time, so Pause/Resume never consumes the delay.
	StartOffset time.Duration
	// Region is a controller-approved load-zone label attached before metrics
	// and archive sinks observe a result. Scripts cannot create this dimension.
	Region string
	Script *dsl.Script
	// RunID is the managed test identity exposed to {{run.id}} at execution.
	// Direct/library callers may leave it empty; that template then fails closed.
	RunID string
	// RunCorrelationOrigins permits the managed run header only at these origins.
	// This controller-owned setting is never derived from executable script data.
	RunCorrelationOrigins []string
	// ScenarioID is the managed execution identity exposed to protocol
	// runtimes. Direct/library callers may leave it empty to fall back to the
	// script artifact ID.
	ScenarioID          string
	VUsers              int
	RampUp              time.Duration
	RampDown            time.Duration
	AllowZeroVUsers     bool
	InitStrategy        string // "at_runtime", "at_once"
	DataShard           DataShardConfig
	StagedDataFiles     map[string][]byte
	VUserIDOffset       int
	ParameterAllocation ParameterAllocation
	VUserIDSpan         int
	ResultSink          ResultSink
	RedactionConfig     *RedactionConfig
	SecretResolver      SecretResolver
	// JavaBroker selects an operator-approved dedicated execution-pod backend.
	// Nil preserves the existing local dedicated runtime. Scripts cannot set it.
	JavaBroker     *JavaBrokerGroupOptions
	javaBroker     *javaBrokerGroup
	rateLimiter    *TokenBucket
	rendezvous     *RendezvousCoordinator
	executionGoals *ExecutionGoalLimiter
	dataProvider   *DataProvider
	// importedCredentials is this group's single retained-credential locator
	// walk, produced by NewMultiGroup and handed to every VUser the group
	// creates. Callers do not supply it; see runtimeSecretValuesForScript.
	importedCredentials *importedCredentialInspection
	sourceParameters    *sourceParameterState
	sourceSamplers      *sourceSamplerRuntime
}

type ResultSink interface {
	RecordResult(result *protocol.Result) error
}

type TransactionSink interface {
	RecordTransaction(result *protocol.Result) error
}

type MultiGroupConfig struct {
	TerminationMode string
	Groups          []GroupConfig
	Duration        time.Duration
	PreBaseline     time.Duration
	PostBaseline    time.Duration
	// StartPaused latches the execution gate before Run creates any VUsers.
	// Distributed evacuation uses it when a replacement joins a paused run.
	StartPaused bool
	// RuntimePreflightValidated is set only by distributed two-phase staging
	// after every group has passed PreflightScriptRuntime on this generator.
	// Local/controller callers leave it false so MultiGroupEngine performs the
	// same validation before publishing readiness or allocating VUsers.
	RuntimePreflightValidated bool
}

type MultiGroupEngine struct {
	brokerRunUsed   atomic.Bool
	terminationMode string
	groups          []GroupConfig
	duration        time.Duration
	preBaseline     time.Duration
	postBaseline    time.Duration
	metrics         *MetricsCollector
	cancel          context.CancelFunc
	readyCh         chan struct{}
	readyOnce       sync.Once

	// Dynamic control state
	mu                     sync.Mutex
	scaleMu                sync.Mutex
	scalingOpen            bool
	activeReserved         int
	totalCtx               context.Context
	wg                     *sync.WaitGroup
	vuserCancels           map[int]context.CancelFunc
	vuserGroups            map[int]string
	vuserRunning           map[int]struct{}
	vuserStopping          map[int]bool
	vuserChanged           chan struct{}
	nextVUserID            int
	freeVUserIDs           vuserIDHeap
	vuserIDsInUse          map[int]struct{}
	vuserIDSpan            int
	groupVUserIDPools      map[string]*groupVUserIDPool
	vuserIdentities        map[int]allocatedVUserIdentity
	paused                 bool
	pauseRequested         bool
	pauseCh                chan struct{}
	pauseChanged           chan struct{}
	pauseStartTime         time.Time
	runStartedAt           time.Time
	baseTargetDuration     time.Duration
	pauseCompensation      time.Duration
	targetDuration         time.Duration
	controlsReady          bool
	initialRampInProgress  bool
	stopVUsers             context.CancelFunc
	baseCtx                context.Context
	loopCtx                context.Context
	launchCtx              context.Context
	launchOpen             bool
	arrivalCtx             context.Context
	gracefulCtx            context.Context
	arrivalStop            context.CancelFunc
	gracefulCancel         context.CancelFunc
	softStop               context.CancelFunc
	gracefulStopRequested  bool
	hardStopRequested      bool
	gracefulStopCh         chan struct{}
	mockNamespace          string
	scaleGuard             bool
	runtimePreflightOnce   sync.Once
	runtimePreflightErr    error
	runtimePreflightValid  bool
	javaVuserCapacityLimit int64
	javaVuserReservation   *javaVuserCapacityReservation
	javaVuserLegacyRun     *javaVuserLegacyRunReservation
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

type vuserIDHeap []int

// vuserIdentity separates the globally unique engine handle used by lifecycle
// control maps from the group-local ordinal exposed to scripts, data providers,
// source-IP pools, and protocol runtimes. Groups may ramp concurrently without
// making those user-visible identities scheduler-dependent.
type vuserIdentity struct {
	handle int
	local  int
}

type groupVUserIDPool struct {
	noReuse   bool
	next      int
	allocated int
	span      int
	free      vuserIDHeap
}

type allocatedVUserIdentity struct {
	groupName string
	local     int
}

// preparedVUser owns a fully initialized closed-model protocol instance before
// it becomes active. Multi-group startup prepares every configured closed VUser
// before releasing any user actions, which preserves fail-fast ordering without
// serializing the groups' ramp clocks. cleanupOnce transfers ownership exactly
// once: either abortPreparedVUser tears the instance down before activation, or
// the activated VUser goroutine does so after Run returns.
type preparedVUser struct {
	identity       vuserIdentity
	group          GroupConfig
	runtimeID      int
	proto          protocol.Protocol
	vuCtx          context.Context
	cancelVUser    context.CancelFunc
	cancelProtocol context.CancelFunc
	shedCtx        context.Context
	boundaryCtx    context.Context
	wg             *sync.WaitGroup
	cleanupOnce    sync.Once
}

type preparedClosedGroup struct {
	group GroupConfig
	users []*preparedVUser
}

type deferredClosedGroup struct {
	group      GroupConfig
	startIndex int
}

type openGroupStartupPlan struct {
	group         GroupConfig
	protoConfig   map[string]any
	totalArrivals int
	limiter       *inFlightLimiter
	inFlightLimit int
}

func (h vuserIDHeap) Len() int           { return len(h) }
func (h vuserIDHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h vuserIDHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *vuserIDHeap) Push(value any)    { *h = append(*h, value.(int)) }
func (h *vuserIDHeap) Pop() any {
	old := *h
	last := len(old) - 1
	value := old[last]
	*h = old[:last]
	return value
}

func NewMultiGroup(cfg MultiGroupConfig) (*MultiGroupEngine, error) {
	if len(cfg.Groups) == 0 {
		return nil, fmt.Errorf("at least one group is required")
	}
	if len(cfg.Groups) > MaxEngineGroups {
		return nil, fmt.Errorf("group count must not exceed %d", MaxEngineGroups)
	}
	// Freeze trusted remote bindings without mutating caller-owned group options.
	cfg.Groups = append([]GroupConfig(nil), cfg.Groups...)
	for i := range cfg.Groups {
		g := &cfg.Groups[i]
		if g.JavaBroker != nil {
			if g.Script == nil {
				return nil, fmt.Errorf("group script is required")
			}
			if g.Name == "" {
				g.Name = g.Script.Name
			}
			if g.ScenarioID == "" {
				g.ScenarioID = g.Script.ID
			}
			if cfg.Duration < 0 || cfg.PreBaseline < 0 || cfg.PostBaseline < 0 || cfg.PreBaseline > time.Duration(1<<63-1)-cfg.Duration || cfg.PostBaseline > time.Duration(1<<63-1)-cfg.Duration-cfg.PreBaseline {
				return nil, fmt.Errorf("java_broker_duration_invalid")
			}
			if err := newJavaBrokerGroup(g, cfg.Duration+cfg.PreBaseline+cfg.PostBaseline); err != nil {
				return nil, err
			}
		}
	}
	scripts := make([]*dsl.Script, 0, len(cfg.Groups))
	for _, group := range cfg.Groups {
		scripts = append(scripts, group.Script)
	}
	if err := dsl.ValidateTermination(cfg.TerminationMode, cfg.Duration, scripts...); err != nil {
		return nil, err
	}
	if cfg.PreBaseline < 0 {
		return nil, fmt.Errorf("pre-baseline must be non-negative")
	}
	if cfg.PostBaseline < 0 {
		return nil, fmt.Errorf("post-baseline must be non-negative")
	}
	totalVUsers := int64(0)
	persistentJavaVUsers := int64(0)
	persistentJavaConfigured := false
	legacyJavaConfigured := false
	effectiveGroupNames := make(map[string]int, len(cfg.Groups))
	allocationScripts := make([]*dsl.Script, len(cfg.Groups))
	allocationUsers := make([]int, len(cfg.Groups))
	for i, group := range cfg.Groups {
		allocationScripts[i], allocationUsers[i] = group.Script, group.VUsers
	}
	allocations, err := PlanScenarioParameterAllocations(allocationScripts, allocationUsers)
	if err != nil {
		return nil, err
	}
	for i, g := range cfg.Groups {
		if g.Script == nil {
			return nil, fmt.Errorf("group[%d] %q: script is required", i, g.Name)
		}
		if err := validateMessageDetailRuntimeSupport(g.Script); err != nil {
			return nil, fmt.Errorf("group[%d] %q: %w", i, g.Name, err)
		}
		if g.StartOffset < 0 {
			return nil, fmt.Errorf("group[%d] %q: start offset must be non-negative", i, g.Name)
		}
		if cfg.TerminationMode != dsl.TerminationIterations && g.StartOffset > cfg.Duration {
			return nil, fmt.Errorf("group[%d] %q: start offset %s begins after the %s scenario deadline", i, g.Name, g.StartOffset, cfg.Duration)
		}
		if err := importExecutionGateError(g.Script, "launch"); err != nil {
			return nil, fmt.Errorf("group[%d] %q: %w", i, g.Name, err)
		}
		if issues := dsl.ValidateWorkloadSettingsIssues(g.Script.Config.RunLogic, g.Script.Config.Pacing); len(issues) > 0 {
			return nil, fmt.Errorf("group[%d] %q: invalid workload settings: %s", i, g.Name, issues[0])
		}
		if cfg.TerminationMode == dsl.TerminationIterations && (g.VUsers <= 0 || g.RampDown > 0) {
			return nil, fmt.Errorf("iteration termination requires positive fixed users and no ramp-down")
		}
		if g.VUsers < 0 {
			return nil, fmt.Errorf("group[%d] %q: vusers must be zero or greater", i, g.Name)
		}
		if g.VUsers > MaxEngineVUsers {
			return nil, fmt.Errorf("group[%d] %q: vusers must not exceed %d", i, g.Name, MaxEngineVUsers)
		}
		totalVUsers += int64(g.VUsers)
		if totalVUsers > MaxEngineVUsers {
			return nil, fmt.Errorf("aggregate vusers must not exceed %d", MaxEngineVUsers)
		}
		if g.VUsers == 0 && !g.AllowZeroVUsers {
			return nil, fmt.Errorf("group[%d] %q: vusers must be positive", i, g.Name)
		}
		if g.VUsers == 0 && isOpenWorkload(g.Script) {
			return nil, fmt.Errorf("group[%d] %q: open workload groups require positive vusers", i, g.Name)
		}
		if javaVuserScriptUsesPhaseLifecycle(g.Script) && javaVuserScriptHasLegacyRunAction(g.Script) {
			return nil, fmt.Errorf("group[%d] %q: java_vuser scripts must not mix persistent phase methods with legacy full_lifecycle actions", i, g.Name)
		}
		javaCapacityPlan := NewJavaVuserCapacityPlan(g.Script)
		if g.javaBroker == nil && javaCapacityPlan.RequiresPersistentLifecycle() {
			persistentJavaConfigured = true
			if strings.EqualFold(strings.TrimSpace(g.InitStrategy), "at_once") {
				return nil, fmt.Errorf("group[%d] %q: init_strategy at_once is incompatible with persistent java_vuser lifecycle; use at_runtime", i, g.Name)
			}
			persistentJavaVUsers += javaCapacityPlan.Demand(g.VUsers)
		} else if javaVuserScriptUsesLegacyOneShot(g.Script) {
			legacyJavaConfigured = true
		}
		if groupLifecycleAtOnce(g) && sourceTransactionsCrossLifecycleBlocks(g.Script) {
			return nil, fmt.Errorf("group[%d] %q: init_strategy at_once cannot preserve a source transaction across init, actions, and teardown; use at_runtime", i, g.Name)
		}
		if g.VUserIDSpan < 0 {
			return nil, fmt.Errorf("group[%d] %q: vuser id span must be non-negative", i, g.Name)
		}
		if g.VUserIDSpan > 0 && g.VUsers > g.VUserIDSpan {
			return nil, fmt.Errorf("group[%d] %q: vusers exceed vuser id span", i, g.Name)
		}
		maxLocalID := g.VUsers
		if g.VUserIDSpan > 0 {
			maxLocalID = g.VUserIDSpan
		}
		if g.VUserIDOffset < 0 || (maxLocalID > 0 && g.VUserIDOffset > int(^uint(0)>>1)-(maxLocalID-1)) {
			return nil, fmt.Errorf("group[%d] %q: vuser id offset overflows runtime int", i, g.Name)
		}
		if g.Name == "" {
			cfg.Groups[i].Name = g.Script.Name
		}
		effectiveName := cfg.Groups[i].Name
		effectiveNameKey := strings.ToLower(strings.Join(strings.Fields(effectiveName), " "))
		if previous, exists := effectiveGroupNames[effectiveNameKey]; exists {
			return nil, fmt.Errorf("group[%d] %q: effective group name duplicates group[%d]", i, effectiveName, previous)
		}
		effectiveGroupNames[effectiveNameKey] = i
		dataProvider, err := NewShardedDataProviderWithAssets(g.Script.DataSources, g.DataShard, g.StagedDataFiles)
		if err != nil {
			return nil, fmt.Errorf("group[%d] %q: initialize data sources: %s", i, cfg.Groups[i].Name, scrubSensitiveText(err.Error()))
		}
		if err := ValidateParameterAllocationTopology(g.Script); err != nil {
			return nil, err
		}
		if g.ParameterAllocation.Population == 0 {
			g.ParameterAllocation = allocations[i]
			if g.ParameterAllocation.Population == 0 {
				g.ParameterAllocation.Population = g.VUsers
				g.ParameterAllocation.Iterations = g.Script.Config.RunLogic.Iterations
			}
		}
		if HasAllocatedParameters(g.Script) && (isOpenWorkload(g.Script) || groupLifecycleAtOnce(g)) {
			return nil, fmt.Errorf("unique parameter allocation requires closed workload and per-VUser init")
		}
		if _, _, err := CompileParameterAllocation(g.Script.Parameters, dataProvider, g.ParameterAllocation); err != nil {
			return nil, err
		}
		cfg.Groups[i].ParameterAllocation = g.ParameterAllocation
		cfg.Groups[i].dataProvider = dataProvider
	}
	if persistentJavaConfigured && legacyJavaConfigured {
		return nil, fmt.Errorf("persistent and legacy full_lifecycle java_vuser groups cannot share generator process slots")
	}
	if persistentJavaVUsers > 0 {
		if maxVUsers, available, err := availableJavaVuserPersistentCapacity(); err != nil {
			return nil, fmt.Errorf("inspect java_vuser runtime capacity: %w", err)
		} else if available && persistentJavaVUsers > maxVUsers {
			return nil, fmt.Errorf("java_vuser persistent runtime capacity exceeded: requires %d VUsers, maximum %d", persistentJavaVUsers, maxVUsers)
		}
	}
	defaultSecretResolvers := make(map[string]SecretResolver)
	runtimeRedactionSets := make(map[[32]byte]*RedactionConfig)
	runtimeRedactionBytes := 0
	for i := range cfg.Groups {
		if cfg.Groups[i].SecretResolver == nil {
			projectID := strings.TrimSpace(cfg.Groups[i].Script.ProjectID)
			resolver := defaultSecretResolvers[projectID]
			if resolver == nil {
				resolver = newDefaultSecretResolverForProject(projectID)
				defaultSecretResolvers[projectID] = resolver
			}
			cfg.Groups[i].SecretResolver = resolver
		}
		resolver, values, err := snapshotReferencedSecrets(cfg.Groups[i].Script, cfg.Groups[i].SecretResolver)
		if err != nil {
			return nil, fmt.Errorf("group[%d] %q: inspect secret references: %s", i, cfg.Groups[i].Name, scrubSensitiveText(err.Error()))
		}
		cfg.Groups[i].SecretResolver = resolver
		// One shared chokepoint with engine.New, ReplayScript, and shakeout; see
		// script_sourced_run_evidence.go.
		values, scriptSourcedValues, importedCredentials, err := runtimeSecretValuesForScript(cfg.Groups[i].Script, resolver, values)
		if err != nil {
			return nil, fmt.Errorf("group[%d] %q: inspect retained import credentials: %s", i, cfg.Groups[i].Name, scrubSensitiveText(err.Error()))
		}
		cfg.Groups[i].importedCredentials = importedCredentials
		// An inbound group RedactionConfig may carry values of EITHER origin, so it
		// is merged origin-aware rather than flattened into the platform-managed
		// half. pkg/api installs only project-secret-store values here, but the
		// generator RPC staging boundary
		// (pkg/distributed/generatorrpc/execution_staging.go) registers the
		// script-sourced half itself before handing the config over — and this is
		// the last place that origin still exists. Flattening it re-registered
		// every script-sourced value as platform-managed, which turned screenshot
		// and browser HAR/trace capture off on distributed runs only.
		mergedValues, scriptSourcedValues := mergedRuntimeSecretValuesByOrigin(
			cfg.Groups[i].RedactionConfig,
			values,
			scriptSourcedValues,
		)
		key := runtimeSecretValueSetKey(mergedValues, scriptSourcedValues)
		runtimeConfig := runtimeRedactionSets[key]
		if runtimeConfig == nil {
			var scriptSourcedDropped bool
			runtimeConfig, scriptSourcedDropped, err = buildBoundedRuntimeSecretRedaction(
				mergedValues,
				scriptSourcedValues,
				maxMultiGroupRuntimeSecretRedactionBytes-runtimeRedactionBytes,
			)
			if err != nil {
				return nil, fmt.Errorf("group[%d] %q: prepare runtime secret redaction: %s", i, cfg.Groups[i].Name, scrubSensitiveText(err.Error()))
			}
			if scriptSourcedDropped {
				warnScriptSourcedRedactionDropped("group", cfg.Groups[i].Name)
			}
			if runtimeConfig != nil {
				runtimeRedactionBytes += runtimeConfig.runtimePatternBytes
			}
			runtimeRedactionSets[key] = runtimeConfig
		}
		cfg.Groups[i].RedactionConfig = attachRuntimeSecretRedaction(cfg.Groups[i].RedactionConfig, runtimeConfig)
	}
	mge := &MultiGroupEngine{
		groups:                cfg.Groups,
		duration:              cfg.Duration,
		terminationMode:       cfg.TerminationMode,
		preBaseline:           cfg.PreBaseline,
		postBaseline:          cfg.PostBaseline,
		metrics:               NewMetricsCollector(10000),
		readyCh:               make(chan struct{}),
		gracefulStopCh:        make(chan struct{}),
		vuserIDSpan:           MaxEngineVUsers,
		paused:                cfg.StartPaused,
		pauseRequested:        cfg.StartPaused,
		runtimePreflightValid: cfg.RuntimePreflightValidated,
		vuserChanged:          make(chan struct{}),
	}
	mge.metrics.SetGroupActiveVUsersProvider(mge.activeVUserCountByGroup)
	slaThresholds := make(map[string]float64)
	for _, group := range cfg.Groups {
		if group.TransactionResponseTimeSLAMs > 0 {
			slaThresholds[group.Name] = group.TransactionResponseTimeSLAMs
		}
	}
	mge.metrics.SetTransactionResponseTimeSLAThresholds(slaThresholds)
	return mge, nil
}

// Ready is closed once Run has initialized the internal contexts required by
// runtime controls such as ScaleVUsers.
func (mge *MultiGroupEngine) Ready() <-chan struct{} {
	return mge.readyCh
}

// ScalingReady reports whether runtime VUser adjustments can be applied
// without racing the configured startup target.
func (mge *MultiGroupEngine) ScalingReady() bool {
	if mge == nil {
		return false
	}
	mge.mu.Lock()
	defer mge.mu.Unlock()
	return mge.scalingAdmissionOpenLocked()
}

// scalingAdmissionOpenLocked reports whether the run can still admit a scale
// operation. baseCtx remains live while an exact execution goal drains its
// entitled iteration, so it is not sufficient on its own: loopCtx/launchCtx
// and launchOpen are the lifecycle fence that prevents scaling from shedding
// or replacing that final in-flight work. The caller holds mge.mu.
func (mge *MultiGroupEngine) scalingAdmissionOpenLocked() bool {
	return mge.baseCtx != nil && mge.baseCtx.Err() == nil &&
		mge.loopCtx != nil && mge.loopCtx.Err() == nil &&
		mge.launchCtx != nil && mge.launchCtx.Err() == nil &&
		mge.launchOpen && mge.controlsReady && mge.scalingOpen
}

// activeVUserCountByGroup returns the live active VUser count per group, derived
// from the live vuser→group map. Safe to call concurrently with spawn/teardown.
func (mge *MultiGroupEngine) activeVUserCountByGroup() map[string]int {
	mge.mu.Lock()
	defer mge.mu.Unlock()
	counts := make(map[string]int, len(mge.groups))
	for _, group := range mge.groups {
		counts[group.Name] = 0
	}
	for id := range mge.vuserRunning {
		if group, ok := mge.vuserGroups[id]; ok {
			counts[group]++
		}
	}
	return counts
}

func (mge *MultiGroupEngine) Run(ctx context.Context) (*MultiGroupRunResult, error) {
	return mge.RunAt(ctx, time.Time{})
}

// RunAt starts the engine on an optional shared wall-clock epoch. Distributed
// generators pass a future translated barrier. Closed protocol instances may
// initialize during the commit lead time, but VUser actions and live gauges stay
// fenced until the epoch; this setup phase lets peer generators align their
// ramps without turning initialization latency into a launch burst. Local callers
// use Run, which starts now.
func (mge *MultiGroupEngine) RunAt(ctx context.Context, executionStart time.Time) (runResult *MultiGroupRunResult, runErr error) {
	// Ready is both a successful control boundary and a terminal release for
	// callers already waiting on a committed engine. Every return path—including
	// preflight failure or a graceful stop latched before Run—must unblock it.
	defer mge.readyOnce.Do(func() { close(mge.readyCh) })
	if ctx == nil {
		ctx = context.Background()
	}
	remote := false
	for _, group := range mge.groups {
		remote = remote || group.javaBroker != nil
	}
	if remote {
		if !mge.brokerRunUsed.CompareAndSwap(false, true) {
			return nil, fmt.Errorf("java_broker_engine_already_used")
		}
		defer func() { runErr = errors.Join(runErr, mge.ClosePreparedRuntime()) }()
	}
	ctx, stopBrokerRun := context.WithCancel(ctx)
	defer stopBrokerRun()
	runEnteredAt := time.Now()
	coordinatedStart := !executionStart.IsZero()
	futureCoordinatedStartAtEntry := coordinatedStart && executionStart.After(runEnteredAt)
	if err := mge.importExecutionGateError("launch"); err != nil {
		return nil, err
	}
	sourceScripts := make([]*dsl.Script, len(mge.groups))
	for i := range mge.groups {
		sourceScripts[i] = mge.groups[i].Script
	}
	sourceState, sourceErr := newSourceParameterState(sourceScripts)
	if sourceErr != nil {
		return nil, sourceErr
	}
	sourceSamplers, sourceErr := newSourceSamplerRuntime(sourceScripts, mge)
	if sourceErr != nil {
		return nil, sourceErr
	}
	defer func() { runErr = errors.Join(runErr, sourceSamplers.close()) }()
	for i := range mge.groups {
		mge.groups[i].sourceParameters = sourceState
		mge.groups[i].sourceSamplers = sourceSamplers
	}
	if err := mge.preflightRuntimeAtStart(ctx); err != nil {
		return nil, err
	}
	for _, group := range mge.groups {
		if group.javaBroker != nil {
			if err := group.javaBroker.start(ctx, stopBrokerRun); err != nil {
				return nil, err
			}
		}
	}
	if remote && coordinatedStart && !time.Now().Before(executionStart) {
		return nil, fmt.Errorf("java_broker_commit_missed_start")
	}
	if err := mge.acquireJavaVuserCapacity(); err != nil {
		return nil, err
	}
	defer mge.releaseJavaVuserCapacity()
	mge.metrics.EnableIdleSnapshots(mge.preBaseline > 0 || mge.postBaseline > 0)
	mge.metrics.Start(time.Second)
	defer mge.metrics.Stop()

	var wg sync.WaitGroup
	start := executionStart
	if start.IsZero() {
		start = time.Now()
	} else {
		// Re-anchor the wire timestamp onto this process's monotonic clock. The
		// wall instant stays the same, while later ramp/duration subtraction is
		// insulated from wall-clock corrections after commit.
		now := time.Now()
		start = now.Add(executionStart.Sub(now))
	}

	mge.mu.Lock()
	mge.vuserCancels = make(map[int]context.CancelFunc)
	mge.vuserGroups = make(map[int]string)
	mge.vuserStopping = make(map[int]bool)
	mge.resetVUserIDStateLocked()
	mge.paused = mge.pauseRequested
	startedPaused := mge.paused
	if mge.paused {
		mge.pauseStartTime = start
	} else {
		mge.pauseStartTime = time.Time{}
	}
	mge.pauseCompensation = 0
	mge.pauseCh = make(chan struct{})
	if !mge.paused {
		close(mge.pauseCh)
	}
	mge.pauseChanged = make(chan struct{})
	mge.controlsReady = false
	mge.initialRampInProgress = false
	mge.baseTargetDuration = mge.duration
	mge.targetDuration = mge.duration
	mge.mockNamespace = newRuntimeMockNamespace()
	mge.scalingOpen = false
	mge.activeReserved = 0
	mge.scaleGuard = mge.hasZeroCapableClosedGroup()
	mge.runStartedAt = start

	// Create a base context for VUsers that only cancels on hard stop/abort
	baseCtx, baseCancel := context.WithCancel(ctx)
	loopCtx, loopCancel := context.WithCancel(baseCtx)
	launchCtx, launchCancel := context.WithCancel(loopCtx)
	arrivalCtx, arrivalCancel := context.WithCancel(baseCtx)
	gracefulCtx, gracefulCancel := context.WithCancel(baseCtx)
	softStopAll := func() {
		loopCancel()
		arrivalCancel()
	}
	explicitGracefulStop := func() {
		gracefulCancel()
		softStopAll()
	}
	mge.baseCtx = baseCtx
	mge.loopCtx = loopCtx
	mge.launchCtx = launchCtx
	mge.launchOpen = true
	mge.arrivalCtx = arrivalCtx
	mge.gracefulCtx = gracefulCtx
	mge.arrivalStop = arrivalCancel
	mge.gracefulCancel = gracefulCancel
	mge.cancel = baseCancel
	mge.softStop = explicitGracefulStop
	preRunGracefulStopRequested := mge.gracefulStopRequested
	if mge.gracefulStopRequested {
		// GracefulStop can race immediately after CommitWork marks an execution
		// started but before this Run goroutine initializes its cancellation
		// functions. Honor the latched request before any VUser can be spawned.
		explicitGracefulStop()
	}
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
		mge.controlsReady = false
		mge.mu.Unlock()
	}()

	defer baseCancel()
	defer loopCancel()
	defer launchCancel()
	defer arrivalCancel()
	defer gracefulCancel()
	defer func() {
		mge.mu.Lock()
		mge.softStop = nil
		mge.arrivalStop = nil
		mge.gracefulCancel = nil
		mge.mu.Unlock()
	}()

	groupResults := make([]GroupResult, len(mge.groups))
	for i := range mge.groups {
		groupResults[i].Name = mge.groups[i].Name
	}
	// Resolve a RunAt call that reached this generator after its entire active
	// window before starting the asynchronous deadline manager. This makes the
	// zero-work outcome deterministic instead of racing a context cancellation.
	mge.mu.Lock()
	expiredBeforeBaseline := mge.terminationMode != dsl.TerminationIterations && !mge.paused && mge.activeElapsedAtLocked(time.Now()) >= mge.preBaseline+mge.baseTargetDuration
	if expiredBeforeBaseline {
		mge.controlsReady = false
		mge.scalingOpen = false
		mge.initialRampInProgress = false
		mge.launchOpen = false
	}
	mge.mu.Unlock()
	if expiredBeforeBaseline {
		launchCancel()
		arrivalCancel()
		baseCancel()
		if futureCoordinatedStartAtEntry {
			return nil, fmt.Errorf("coordinated startup preparation missed the complete execution window")
		}
		return &MultiGroupRunResult{Duration: nonNegativeElapsedSince(start), Groups: groupResults}, nil
	}

	// Background goroutine manages the steady-state deadline and then performs
	// a real progressive ramp-down before the hard stop. Baseline, ramp, and
	// duration share the same active-time origin; slow startup consumes the
	// configured post-baseline duration instead of extending the run forever.
	go func() {
		if mge.terminationMode == dsl.TerminationIterations {
			return
		}
		for {
			mge.mu.Lock()
			if mge.baseCtx.Err() != nil {
				mge.mu.Unlock()
				return
			}
			paused := mge.paused
			deadlineActive := mge.preBaseline + mge.baseTargetDuration
			elapsedActive := mge.activeElapsedAtLocked(time.Now())
			mge.mu.Unlock()

			if !paused && elapsedActive >= deadlineActive {
				// Close scaling admission before marking VUsers as stopping. A scale-up
				// waiting for earlier retirements must not reopen load in the gap before
				// the hard stop is published.
				mge.mu.Lock()
				// Pause/duration controls may have changed after the optimistic
				// sample above. Revalidate and close admission under the same lock
				// activation uses, making the deadline a true linearization point.
				if mge.paused || mge.activeElapsedAtLocked(time.Now()) < mge.preBaseline+mge.baseTargetDuration {
					mge.mu.Unlock()
					continue
				}
				mge.scalingOpen = false
				mge.controlsReady = false
				mge.initialRampInProgress = false
				mge.launchOpen = false
				mge.mu.Unlock()
				// Prevent a delayed ramp step from launching after rampDownVUsers
				// snapshots the active set for progressive cancellation.
				launchCancel()
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

	// A graceful stop cancels loopCtx while deliberately preserving baseCtx so
	// in-flight actions can drain. The pre-load baseline must observe that soft
	// stop too; waiting on the caller context alone would strand the run in
	// STOPPING until the entire baseline window elapsed.
	// With no pre-baseline window, a coordinated RunAt may initialize protocol
	// instances before the future release barrier. That is the preparation phase:
	// user actions remain fenced below until executionStart. A real pre-baseline
	// intentionally remains traffic-free, so preparation begins only after that
	// active-time window completes.
	if mge.preBaseline > 0 && !mge.waitUntilActive(loopCtx, mge.preBaseline) {
		if preRunGracefulStopRequested && errors.Is(loopCtx.Err(), context.Canceled) {
			// Ready is also the terminal release for controls already waiting on a
			// committed execution. A graceful stop may be latched before Run creates
			// its contexts, so close this boundary even though no load is admitted.
			mge.readyOnce.Do(func() { close(mge.readyCh) })
			return &MultiGroupRunResult{}, nil
		}
		return nil, loopCtx.Err()
	}
	mge.mu.Lock()
	if (mge.terminationMode != dsl.TerminationIterations && mge.activeElapsedAtLocked(time.Now()) >= mge.preBaseline+mge.baseTargetDuration) || mge.launchCtx.Err() != nil {
		mge.controlsReady = false
		mge.scalingOpen = false
		mge.initialRampInProgress = false
		mge.launchOpen = false
		mge.mu.Unlock()
		launchCancel()
		arrivalCancel()
		baseCancel()
		return &MultiGroupRunResult{Duration: nonNegativeElapsedSince(start), Groups: groupResults}, nil
	}
	loadReady := mge.baseCtx.Err() == nil && mge.loopCtx.Err() == nil
	mge.controlsReady = loadReady
	mge.initialRampInProgress = loadReady
	// Configured VUsers are prepared and released after this public readiness
	// boundary. Scaling stays closed until that initial release is complete so a
	// runtime adjustment cannot race a launcher holding the configured target.
	mge.scalingOpen = false
	allZeroClosed := loadReady
	for _, group := range runGroups {
		if group.VUsers != 0 || isOpenWorkload(group.Script) || groupLifecycleAtOnce(group) {
			allZeroClosed = false
			break
		}
	}
	if allZeroClosed && mge.scaleGuard {
		mge.initialRampInProgress = false
		mge.scalingOpen = true
	}
	mge.mu.Unlock()
	mge.readyOnce.Do(func() {
		close(mge.readyCh)
	})

	if mge.scaleGuard {
		wg.Add(1)
		go mge.holdScaleGuard(mge.loopCtx, &wg)
	}

	totalTargetTPS := 0.0
	atOnceGroups := make([]GroupConfig, 0)
	atOnceStartupGroups := make([]GroupConfig, 0)
	atOnceGates := make(map[string]chan struct{})
	var atOnceGroupsMu sync.Mutex
	preparedGroups := make([]preparedClosedGroup, 0, len(runGroups))
	deferredClosedGroups := make([]deferredClosedGroup, 0)
	openPlans := make([]openGroupStartupPlan, 0, len(runGroups))
	atOnceTeardownComplete := false
	runAtOnceTeardown := func() {
		if atOnceTeardownComplete {
			return
		}
		atOnceTeardownComplete = true
		atOnceGroupsMu.Lock()
		groups := append([]GroupConfig(nil), atOnceGroups...)
		atOnceGroupsMu.Unlock()
		for _, group := range groups {
			protoConfig := buildGroupProtoConfig(group, "load")
			protoConfig["group_name"] = group.Name
			applyRuntimeMockNamespace(protoConfig, mge.mockNamespace)
			tctx, tcancel := teardownContext()
			_ = mge.runGroupLifecycleBlock(tctx, tctx, group, protoConfig, group.Script.Teardown, "teardown")
			tcancel()
		}
	}
	// Once a group begins init-at-once it owns a cleanup obligation, including
	// partial init and failures while starting any later closed/open group.
	defer runAtOnceTeardown()
	groupStartupErrors := make(chan error, len(runGroups))
	abortPreparedGroups := func() {
		var prepared []*preparedVUser
		for _, preparedGroup := range preparedGroups {
			prepared = append(prepared, preparedGroup.users...)
		}
		mge.abortPreparedVUsers(prepared)
	}
	startupStopped := false
groupLoop:
	for gi, group := range runGroups {
		if dsl.CanonicalPacingType(group.Script.Config.Pacing.Type) == "tps" && group.Script.Config.Pacing.TPS > 0 {
			targetTPS := group.Script.Config.Pacing.TPS
			totalTargetTPS += targetTPS
			mge.metrics.SetGroupTargetTPS(group.Name, targetTPS)
		}
		groupResults[gi].Name = group.Name

		protoConfig := buildGroupProtoConfig(group, "load")
		protoConfig["group_name"] = group.Name
		applyRuntimeMockNamespace(protoConfig, mge.mockNamespace)
		if groupLifecycleAtOnce(group) {
			// Per-VUser protocol instances are admitted in the global closed-group
			// startup barrier below. The shared user init block runs at the load
			// epoch, alongside already-admitted peer groups, so an exact execution
			// goal can still cooperatively stop a long or context-ignoring init-once
			// adapter. This gate prevents the group's own main users from starting
			// before that shared lifecycle succeeds.
			atOnceStartupGroups = append(atOnceStartupGroups, group)
			atOnceGates[group.Name] = make(chan struct{})
		}
		if isOpenWorkload(group.Script) {
			arrivals, bounded := boundedArrivalLimit(group.Script, group.VUsers)
			inFlightLimit := openWorkloadInFlightLimit(group.Script)
			totalArrivals := 0
			if bounded {
				totalArrivals = arrivals
			}
			openPlans = append(openPlans, openGroupStartupPlan{
				group:         group,
				protoConfig:   protoConfig,
				totalArrivals: totalArrivals,
				limiter:       newInFlightLimiter(inFlightLimit),
				inFlightLimit: inFlightLimit,
			})
			continue
		}

		if group.VUsers == 0 {
			continue
		}
		if groupLifecycleAtOnce(group) {
			// The shared lifecycle protocol must be allowed to observe execution-
			// goal cancellation from an earlier group before any per-user adapter
			// instances for this group are initialized. Admit these users lazily
			// behind their init-once gate at the common load epoch.
			deferredClosedGroups = append(deferredClosedGroups, deferredClosedGroup{group: group})
			continue
		}

		preparedGroups = append(preparedGroups, preparedClosedGroup{group: group})
		preparedGroup := &preparedGroups[len(preparedGroups)-1]
		prepareCount := group.VUsers
		if hasExactSingleExecutionGoal(group.Script.Config.RunLogic) && group.VUsers > 1 {
			// Exact shared goals can stop the run before every configured VUser is
			// entitled to launch. Admit the first user at the global barrier, then
			// preserve lazy initialization for the remaining contenders so goal
			// completion can cancel an Init that will never own work.
			prepareCount = 1
			deferredClosedGroups = append(deferredClosedGroups, deferredClosedGroup{group: group, startIndex: 1})
		}
		for i := 0; i < prepareCount; i++ {
			if err := mge.WaitIfPaused(mge.loopCtx); err != nil {
				startupStopped = true
				break groupLoop
			}
			identity, err := mge.allocateVUserIdentity(group.Name)
			if err != nil {
				baseCancel()
				abortPreparedGroups()
				wg.Wait()
				return nil, fmt.Errorf("group %q allocate vuser id: %w", group.Name, err)
			}
			prepared, err := mge.prepareVUser(identity, group, protoConfig, &wg, false)
			if err != nil {
				baseCancel()
				abortPreparedGroups()
				wg.Wait()
				return nil, fmt.Errorf("group %q initialize vuser: %w", group.Name, err)
			}
			if prepared == nil {
				startupStopped = true
				break groupLoop
			}
			preparedGroup.users = append(preparedGroup.users, prepared)
		}
	}
	if totalTargetTPS > 0 {
		mge.metrics.SetTargetTPS(totalTargetTPS)
	}

	if startupStopped || mge.loopCtx.Err() != nil {
		startupStopped = true
		abortPreparedGroups()
	} else if err := mge.WaitIfPaused(mge.loopCtx); err != nil {
		startupStopped = true
		abortPreparedGroups()
	}
	if !startupStopped && coordinatedStart {
		// Runtime observation is part of coordinated preparation. It must finish
		// before the release boundary; running it after the boundary charges its
		// latency to ramp-up and can release several overdue steps at once.
		if err := mge.RevalidateRuntime(mge.loopCtx); err != nil {
			baseCancel()
			abortPreparedGroups()
			wg.Wait()
			return nil, fmt.Errorf("revalidate runtime before coordinated execution barrier: %w", err)
		}
	}
	// A zero-baseline coordinated run can use the controller-selected epoch only
	// when every admission step finished before it. A deliberate pause shifts the
	// active release boundary, so resume such runs on a fresh local epoch rather
	// than treating the operator's pause as a startup failure. A real resource
	// baseline is kept idle above and likewise starts its ramp after preparation.
	coordinatedPreprepare := coordinatedStart && mge.preBaseline == 0
	preparedBeforeCoordinatedEpoch := coordinatedPreprepare && time.Now().Before(start)
	mge.mu.Lock()
	coordinatedPauseDelayed := startedPaused || mge.paused || mge.pauseRequested || mge.pauseCompensation > 0
	mge.mu.Unlock()
	if coordinatedPreprepare && !coordinatedPauseDelayed && !preparedBeforeCoordinatedEpoch {
		baseCancel()
		abortPreparedGroups()
		wg.Wait()
		return nil, fmt.Errorf("coordinated startup preparation missed the execution barrier")
	}

	if !startupStopped {
		mge.mu.Lock()
		loadEpochActive := mge.activeElapsedAtLocked(time.Now())
		if preparedBeforeCoordinatedEpoch {
			// All protocol admission completed before the controller-selected
			// barrier. Use its exact active-time origin so independently staged
			// generators release the same ramp step together instead of rebasing
			// each assignment to local scheduling jitter.
			loadEpochActive = mge.preBaseline
		}
		closedLaunchers := 0
		for _, preparedGroup := range preparedGroups {
			if len(preparedGroup.users) > 0 {
				closedLaunchers++
			}
		}
		closedLaunchers += len(deferredClosedGroups)
		mge.mu.Unlock()

		var launcherWG sync.WaitGroup
		startupTasks := closedLaunchers + len(atOnceStartupGroups)
		launcherWG.Add(startupTasks)
		wg.Add(closedLaunchers + len(openPlans) + len(atOnceStartupGroups))
		for _, group := range atOnceStartupGroups {
			group := group
			gate := atOnceGates[group.Name]
			go func() {
				defer wg.Done()
				defer launcherWG.Done()
				if !mge.waitUntilActive(mge.launchCtx, loadEpochActive+group.StartOffset) {
					return
				}
				// A replacement joining a paused distributed run must not execute
				// init-once protocol traffic before the execution gate opens.
				if err := mge.WaitIfPaused(mge.loopCtx); err != nil {
					return
				}
				if mge.loopCtx.Err() != nil {
					return
				}
				protoConfig := buildGroupProtoConfig(group, "load")
				protoConfig["group_name"] = group.Name
				applyRuntimeMockNamespace(protoConfig, mge.mockNamespace)
				outcome := mge.runGroupLifecycleBlock(mge.baseCtx, mge.launchCtx, group, protoConfig, group.Script.Init, "init")
				if outcome.completed || outcome.userWorkStarted {
					atOnceGroupsMu.Lock()
					atOnceGroups = append(atOnceGroups, group)
					atOnceGroupsMu.Unlock()
				}
				if outcome.err != nil {
					groupStartupErrors <- fmt.Errorf("group %q init-once failed: %w", group.Name, outcome.err)
					mge.mu.Lock()
					mge.controlsReady = false
					mge.scalingOpen = false
					mge.launchOpen = false
					mge.mu.Unlock()
					baseCancel()
					return
				}
				if !outcome.completed {
					return
				}
				close(gate)
			}()
		}
		for _, preparedGroup := range preparedGroups {
			preparedGroup := preparedGroup
			if len(preparedGroup.users) == 0 {
				continue
			}
			go func() {
				defer wg.Done()
				defer launcherWG.Done()
				if gate := atOnceGates[preparedGroup.group.Name]; gate != nil {
					select {
					case <-mge.launchCtx.Done():
						mge.abortPreparedVUsers(preparedGroup.users)
						return
					case <-gate:
					}
				}
				rampDelay := time.Duration(0)
				if preparedGroup.group.RampUp > 0 && len(preparedGroup.users) > 1 {
					rampDelay = preparedGroup.group.RampUp / time.Duration(len(preparedGroup.users))
				}
				for index, prepared := range preparedGroup.users {
					deadline := loadEpochActive + preparedGroup.group.StartOffset + time.Duration(index)*rampDelay
					if !mge.waitUntilActive(mge.launchCtx, deadline) {
						mge.abortPreparedVUsers(preparedGroup.users[index:])
						return
					}
					if err := mge.WaitIfPaused(mge.launchCtx); err != nil {
						mge.abortPreparedVUsers(preparedGroup.users[index:])
						return
					}
					if !mge.activatePreparedVUserWithAdmission(prepared, mge.launchCtx) {
						mge.abortPreparedVUsers(preparedGroup.users[index+1:])
						return
					}
				}
			}()
		}
		for _, deferredGroup := range deferredClosedGroups {
			deferredGroup := deferredGroup
			go func() {
				defer wg.Done()
				defer launcherWG.Done()
				group := deferredGroup.group
				deferredEpoch := loadEpochActive + group.StartOffset
				if gate := atOnceGates[group.Name]; gate != nil {
					select {
					case <-mge.launchCtx.Done():
						return
					case <-gate:
					}
					deferredEpoch = mge.ActiveElapsed()
				}
				// Lazy exceptions intentionally initialize after the global barrier.
				// Anchor their own ramp to the point their prerequisite gate opens so
				// slow shared initialization cannot turn elapsed deadlines into a
				// burst of back-to-back users.
				rampDelay := time.Duration(0)
				if group.RampUp > 0 && group.VUsers > 1 {
					rampDelay = group.RampUp / time.Duration(group.VUsers)
				}
				protoConfig := buildGroupProtoConfig(group, "load")
				protoConfig["group_name"] = group.Name
				applyRuntimeMockNamespace(protoConfig, mge.mockNamespace)
				for index := deferredGroup.startIndex; index < group.VUsers; index++ {
					step := index - deferredGroup.startIndex
					if deferredGroup.startIndex > 0 {
						step++
					}
					deadline := deferredEpoch + time.Duration(step)*rampDelay
					if !mge.waitUntilActive(mge.launchCtx, deadline) {
						return
					}
					if err := mge.WaitIfPaused(mge.launchCtx); err != nil {
						return
					}
					identity, err := mge.allocateVUserIdentity(group.Name)
					if err != nil {
						groupStartupErrors <- fmt.Errorf("group %q allocate vuser id: %w", group.Name, err)
						baseCancel()
						return
					}
					launched, err := mge.spawnVUser(identity, group, protoConfig, &wg, false)
					if err != nil {
						groupStartupErrors <- fmt.Errorf("group %q initialize vuser: %w", group.Name, err)
						baseCancel()
						return
					}
					if !launched {
						return
					}
				}
			}()
		}

		for _, plan := range openPlans {
			plan := plan
			go func() {
				defer wg.Done()
				var epochOnce sync.Once
				epochReady := false
				waitForEpoch := func(ctx context.Context) error {
					epochOnce.Do(func() {
						epochReady = mge.waitUntilActive(ctx, loadEpochActive+plan.group.StartOffset)
					})
					if !epochReady {
						if err := ctx.Err(); err != nil {
							return err
						}
						return context.Canceled
					}
					return mge.WaitIfPaused(ctx)
				}
				waitForPlan := waitForEpoch
				preserveActiveOrigin := preparedBeforeCoordinatedEpoch
				if gate := atOnceGates[plan.group.Name]; gate != nil {
					// Start the scheduler clock at the shared load epoch, but keep
					// dispatch behind successful init-once admission. When the gate
					// opens, overdue arrivals are therefore handled by the scheduler's
					// bounded catch-up/shedding policy instead of silently rebasing the
					// group's clock and shortening its offered load.
					waitForPlan = func(ctx context.Context) error {
						if err := waitForEpoch(ctx); err != nil {
							return err
						}
						select {
						case <-ctx.Done():
							return ctx.Err()
						case <-gate:
						}
						return mge.WaitIfPaused(ctx)
					}
					preserveActiveOrigin = false
				}
				var initializedWorkers int64
				var firstInitErr error
				activeSinceEpoch := func() time.Duration {
					elapsed := mge.ActiveElapsed() - loadEpochActive - plan.group.StartOffset
					if elapsed < 0 {
						return 0
					}
					return elapsed
				}
				runArrivalSchedulerFromOrigin(mge.arrivalCtx, plan.group.Script.Config.Pacing.TPS, plan.totalArrivals, waitForPlan, mge.SleepActive, activeSinceEpoch, preserveActiveOrigin, func(int) {
					// Shed the arrival when the in-flight set is saturated: the SUT
					// cannot keep up, so dropping it protects the generator from
					// unbounded goroutine growth instead of silently throttling to a
					// closed model. The reserved slot is released by spawnOpenWorker
					// on every exit path.
					if !plan.limiter.tryAcquire() {
						mge.metrics.RecordSheddedArrival()
						if plan.limiter.shouldWarn() {
							slog.Warn("open workload shedding arrivals: system under test cannot keep up; capping in-flight workers to protect the generator", "group", plan.group.Name, "max_in_flight", plan.inFlightLimit, "tps", plan.group.Script.Config.Pacing.TPS)
						}
						return
					}
					identity, err := mge.allocateVUserIdentity(plan.group.Name)
					if err != nil {
						plan.limiter.release()
						mge.metrics.RecordSheddedArrival()
						return
					}
					launched, err := mge.spawnOpenWorker(identity, plan.group, plan.protoConfig, &wg, plan.limiter.release)
					if err != nil {
						if firstInitErr == nil {
							firstInitErr = err
						}
						return
					}
					if launched {
						initializedWorkers++
					}
				}, func(_ int, count int) {
					plan.limiter.recordShed(int64(count))
					mge.metrics.RecordSheddedArrivals(int64(count))
					if plan.limiter.shouldWarn() {
						slog.Warn("open workload shedding overdue arrivals: generator scheduler exceeded bounded catch-up burst", "group", plan.group.Name, "shed", count, "max_catch_up_burst", maxOpenWorkloadCatchUpBurst, "tps", plan.group.Script.Config.Pacing.TPS)
					}
				})
				if shed := plan.limiter.Shed(); shed > 0 {
					slog.Warn("open workload shed arrivals over the run (offered TPS exceeded achievable throughput)", "group", plan.group.Name, "shed", shed, "max_in_flight", plan.inFlightLimit)
				}
				if initializedWorkers == 0 && firstInitErr != nil {
					groupStartupErrors <- fmt.Errorf("group %q could not initialize any arrival worker: %w", plan.group.Name, firstInitErr)
				}
			}()
		}

		if startupTasks == 0 {
			mge.finishInitialRamp()
		} else {
			go func() {
				launcherWG.Wait()
				mge.finishInitialRamp()
			}()
		}
	}

	wg.Wait()
	close(groupStartupErrors)
	var startupErrors []error
	for err := range groupStartupErrors {
		startupErrors = append(startupErrors, err)
	}
	if err := errors.Join(startupErrors...); err != nil {
		baseCancel()
		return nil, err
	}
	runAtOnceTeardown()
	mge.mu.Lock()
	gracefulStopCh := mge.gracefulStopCh
	mge.mu.Unlock()
	if err := waitForPostBaselineWindow(ctx, gracefulStopCh, mge.postBaseline); err != nil {
		return nil, err
	}
	elapsed := nonNegativeElapsedSince(start)
	mge.metrics.Stop()
	for index := range groupResults {
		groupResults[index].TotalTransactions, groupResults[index].TotalErrors = mge.metrics.GroupRunTotals(groupResults[index].Name)
	}

	result := &MultiGroupRunResult{
		Duration:          elapsed,
		TotalTransactions: mge.metrics.TotalTransactions(),
		TotalErrors:       mge.metrics.TotalErrors(),
		Groups:            groupResults,
	}
	if snap, ok := mge.metrics.LatestSnapshot(); ok {
		result.FinalSnapshot = snap
	}
	if mge.terminationMode == dsl.TerminationIterations {
		var expected int64
		for _, g := range mge.groups {
			expected += int64(g.VUsers) * int64(g.Script.Config.RunLogic.Iterations)
		}
		if got := mge.metrics.CompletedIterations(); got != expected {
			return result, fmt.Errorf("iteration work incomplete: completed %d of %d iterations", got, expected)
		}
	}
	return result, nil
}

func nonNegativeElapsedSince(start time.Time) time.Duration {
	return max(time.Since(start), time.Duration(0))
}

func (mge *MultiGroupEngine) preflightRuntimeAtStart(ctx context.Context) error {
	mge.mu.Lock()
	previouslyValidated := mge.runtimePreflightValid
	mge.mu.Unlock()
	if err := mge.PreflightRuntime(ctx); err != nil {
		return err
	}
	if !previouslyValidated {
		// This Run consumed the once-only preflight itself, at the actual start
		// boundary, so that observation is already fresh.
		return nil
	}
	// A constructor hint or an earlier explicit preflight may be arbitrarily
	// old after a coordinated future-start barrier. Observe the runner again.
	return mge.RevalidateRuntime(ctx)
}

// PreflightRuntime validates each group's secret-resolved runtime settings and
// unique executable targets exactly once for this generator. It deliberately
// runs before metrics, readiness, lifecycle blocks, or VUser allocation.
func (mge *MultiGroupEngine) PreflightRuntime(ctx context.Context) error {
	if mge == nil {
		return fmt.Errorf("multi-group engine is unavailable")
	}
	if err := mge.importExecutionGateError("runtime preflight"); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	mge.runtimePreflightOnce.Do(func() {
		if mge.runtimePreflightValid {
			return
		}
		mge.mu.Lock()
		groups := append([]GroupConfig(nil), mge.groups...)
		mge.mu.Unlock()
		limit, err := preflightGroupRuntimes(ctx, groups)
		mge.mu.Lock()
		mge.javaVuserCapacityLimit = limit
		mge.runtimePreflightErr = err
		if err == nil {
			mge.runtimePreflightValid = true
		}
		mge.mu.Unlock()
		if err != nil {
			return
		}
	})
	mge.mu.Lock()
	err := mge.runtimePreflightErr
	mge.mu.Unlock()
	return err
}

// RevalidateRuntime performs a new runner/target observation without consuming
// the optional cached preflight. Distributed CommitWork uses it after staging,
// and Run invokes it again at the coordinated start boundary.
func (mge *MultiGroupEngine) RevalidateRuntime(ctx context.Context) error {
	if mge == nil {
		return fmt.Errorf("multi-group engine is unavailable")
	}
	if err := mge.importExecutionGateError("runtime preflight"); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	mge.mu.Lock()
	groups := append([]GroupConfig(nil), mge.groups...)
	mge.mu.Unlock()
	limit, err := preflightGroupRuntimes(ctx, groups)
	if err != nil {
		return err
	}
	mge.mu.Lock()
	mge.javaVuserCapacityLimit = limit
	mge.mu.Unlock()
	return nil
}

func (mge *MultiGroupEngine) importExecutionGateError(operation string) error {
	if mge == nil {
		return fmt.Errorf("multi-group engine is unavailable")
	}
	mge.mu.Lock()
	groups := append([]GroupConfig(nil), mge.groups...)
	mge.mu.Unlock()
	for index, group := range groups {
		if err := importExecutionGateError(group.Script, operation); err != nil {
			return fmt.Errorf("group[%d] %q: %w", index, group.Name, err)
		}
	}
	return nil
}

func preflightGroupRuntimes(ctx context.Context, groups []GroupConfig) (int64, error) {
	persistentJavaVUsers := int64(0)
	var persistentJavaPlan JavaVuserCapacityPlan
	persistentJavaConfigured := false
	legacyJavaValidated := false
	for index, group := range groups {
		if group.javaBroker != nil || group.Script == nil || !strings.EqualFold(strings.TrimSpace(group.Script.Protocol), "java_vuser") {
			continue
		}
		plan := NewJavaVuserCapacityPlan(group.Script)
		if plan.RequiresPersistentLifecycle() {
			persistentJavaPlan = plan
			persistentJavaConfigured = true
			persistentJavaVUsers += plan.Demand(group.VUsers)
			continue
		}
		if legacyJavaValidated {
			continue
		}
		instance, err := protocol.Get("java_vuser")
		if err != nil {
			return 0, fmt.Errorf("group[%d] %q protocol runtime: %s", index, group.Name, scrubSensitiveTextWithConfig(err.Error(), group.RedactionConfig))
		}
		if err := validateJavaVuserRuntimeForPlan(ctx, instance, plan, 0); err != nil {
			return 0, fmt.Errorf("group[%d] %q protocol runtime: %s", index, group.Name, scrubSensitiveTextWithConfig(err.Error(), group.RedactionConfig))
		}
		legacyJavaValidated = true
	}
	persistentJavaLimit := int64(0)
	if persistentJavaConfigured {
		instance, err := protocol.Get("java_vuser")
		if err != nil {
			return 0, fmt.Errorf("java_vuser protocol runtime: %s", scrubSensitiveText(err.Error()))
		}
		capabilities, err := validatedJavaVuserRuntimeCapacityForPlan(ctx, instance, persistentJavaPlan, persistentJavaVUsers)
		if err != nil {
			return 0, fmt.Errorf("java_vuser protocol runtime: %s", scrubSensitiveText(err.Error()))
		}
		persistentJavaLimit = capabilities.MaxVUsers
	}
	for index, group := range groups {
		if group.javaBroker != nil {
			if err := group.javaBroker.prepare(ctx); err != nil {
				return 0, err
			}
			continue
		}
		result, err := PreflightScriptRuntime(ctx, group.Script, group.SecretResolver)
		if err != nil {
			message := scrubSensitiveTextWithConfig(err.Error(), group.RedactionConfig)
			return 0, fmt.Errorf("group[%d] %q protocol runtime preflight: %s", index, group.Name, message)
		}
		for _, warning := range result.Warnings {
			slog.Warn("group protocol runtime preflight warning",
				"group", group.Name,
				"protocol", strings.TrimSpace(group.Script.Protocol),
				"code", safeRuntimeWarningCode(warning),
			)
		}
	}
	return persistentJavaLimit, nil
}

func persistentJavaConfiguredDemand(groups []GroupConfig) int64 {
	var demand int64
	for _, group := range groups {
		if group.javaBroker == nil {
			demand += NewJavaVuserCapacityPlan(group.Script).Demand(group.VUsers)
		}
	}
	return demand
}

func hasPersistentJavaGroup(groups []GroupConfig) bool {
	for _, group := range groups {
		if group.javaBroker == nil && NewJavaVuserCapacityPlan(group.Script).RequiresPersistentLifecycle() {
			return true
		}
	}
	return false
}

func hasLegacyJavaGroup(groups []GroupConfig) bool {
	for _, group := range groups {
		if javaVuserScriptUsesLegacyOneShot(group.Script) {
			return true
		}
	}
	return false
}

func (mge *MultiGroupEngine) acquireJavaVuserCapacity() error {
	mge.mu.Lock()
	if mge.javaVuserReservation != nil || mge.javaVuserLegacyRun != nil {
		mge.mu.Unlock()
		return fmt.Errorf("java_vuser runtime capacity is already reserved")
	}
	demand := persistentJavaConfiguredDemand(mge.groups)
	configured := hasPersistentJavaGroup(mge.groups)
	legacyConfigured := hasLegacyJavaGroup(mge.groups)
	limit := mge.javaVuserCapacityLimit
	mge.mu.Unlock()
	if configured && legacyConfigured {
		return fmt.Errorf("persistent and legacy full_lifecycle java_vuser groups cannot share generator process slots")
	}
	if !configured {
		if !legacyConfigured {
			return nil
		}
		reservation, err := reserveLegacyJavaVuserRun()
		if err != nil {
			return err
		}
		mge.mu.Lock()
		if mge.javaVuserReservation != nil || mge.javaVuserLegacyRun != nil {
			mge.mu.Unlock()
			reservation.Release()
			return fmt.Errorf("java_vuser runtime capacity is already reserved")
		}
		mge.javaVuserLegacyRun = reservation
		mge.mu.Unlock()
		return nil
	}
	if limit <= 0 {
		var available bool
		var err error
		limit, available, err = availableJavaVuserPersistentCapacity()
		if err != nil {
			return fmt.Errorf("inspect java_vuser runtime capacity: %w", err)
		}
		if !available {
			return fmt.Errorf("java_vuser persistent runtime capacity is unavailable")
		}
	}
	reservation, err := reserveJavaVuserCapacity(demand, limit)
	if err != nil {
		return err
	}
	mge.mu.Lock()
	if mge.javaVuserReservation != nil || mge.javaVuserLegacyRun != nil {
		mge.mu.Unlock()
		reservation.Release()
		return fmt.Errorf("java_vuser runtime capacity is already reserved")
	}
	mge.javaVuserReservation = reservation
	mge.javaVuserCapacityLimit = limit
	mge.mu.Unlock()
	return nil
}

func (mge *MultiGroupEngine) releaseJavaVuserCapacity() {
	if mge == nil {
		return
	}
	mge.mu.Lock()
	reservation := mge.javaVuserReservation
	legacyReservation := mge.javaVuserLegacyRun
	mge.javaVuserReservation = nil
	mge.javaVuserLegacyRun = nil
	mge.mu.Unlock()
	reservation.Release()
	legacyReservation.Release()
}

func (mge *MultiGroupEngine) signalVUserChangeLocked() {
	if mge.vuserChanged != nil {
		close(mge.vuserChanged)
	}
	mge.vuserChanged = make(chan struct{})
}

func (mge *MultiGroupEngine) unregisterVUserLocked(vuserID int) {
	delete(mge.vuserCancels, vuserID)
	delete(mge.vuserGroups, vuserID)
	delete(mge.vuserRunning, vuserID)
	delete(mge.vuserStopping, vuserID)
	mge.releaseVUserIDLocked(vuserID)
	mge.signalVUserChangeLocked()
	if err := mge.reconcileJavaVuserCapacityLocked(); err != nil {
		// Teardown only reduces live demand. Treat an increase request here as
		// an invariant violation while retaining the existing reservation.
		slog.Error("failed to reconcile java_vuser capacity after teardown", "err", err)
	}
}

func (mge *MultiGroupEngine) liveVUsersByGroupLocked() map[string]int64 {
	liveByGroup := make(map[string]int64, len(mge.groups))
	for _, groupName := range mge.vuserGroups {
		liveByGroup[groupName]++
	}
	return liveByGroup
}

func (mge *MultiGroupEngine) reconcileJavaVuserCapacityLocked() error {
	if mge.javaVuserReservation == nil {
		return nil
	}
	liveByGroup := mge.liveVUsersByGroupLocked()
	var demand int64
	for _, group := range mge.groups {
		plan := NewJavaVuserCapacityPlan(group.Script)
		if group.javaBroker != nil || !plan.RequiresPersistentLifecycle() {
			continue
		}
		demand += max(plan.Demand(group.VUsers), liveByGroup[group.Name])
	}
	return mge.javaVuserReservation.Resize(demand)
}

// WaitForGroupVUsers waits until no more than maxLive VUsers for groupName
// remain registered. Registration is removed only after protocol teardown, so
// callers can use this to retain external capacity through JVM/session reap.
func (mge *MultiGroupEngine) WaitForGroupVUsers(ctx context.Context, groupName string, maxLive int) error {
	if mge == nil {
		return fmt.Errorf("multi-group engine is unavailable")
	}
	if maxLive < 0 {
		return fmt.Errorf("maximum live vusers must be non-negative")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		mge.mu.Lock()
		live := 0
		for _, liveGroup := range mge.vuserGroups {
			if liveGroup == groupName {
				live++
			}
		}
		if live <= maxLive {
			mge.mu.Unlock()
			return nil
		}
		changed := mge.vuserChanged
		mge.mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
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

func sourceTransactionsCrossLifecycleBlocks(script *dsl.Script) bool {
	if script == nil {
		return false
	}
	for _, actions := range [][]dsl.Action{script.Init, script.Actions, script.Teardown} {
		// At-once phases use different VUser instances. Source markers inside a
		// branch or loop are path-dependent, so a net static count is not enough:
		// one reachable path can strand ownership even when another path balances
		// it. Reject that nonlinear shape conservatively.
		if sourceTransactionsInNonlinearControlFlow(actions, false) {
			return true
		}
		balance := make(map[string]int)
		for _, action := range actions {
			switch {
			case sourceTransactionStarts(action):
				key := strings.ToLower(strings.TrimSpace(sourceTransactionActionName(action)))
				// Same-name source transactions are not nestable in the runtime
				// state map; a duplicate start is rejected by the runtime.
				if balance[key] != 0 {
					return true
				}
				balance[key] = 1
			case sourceTransactionEnds(action):
				key := strings.ToLower(strings.TrimSpace(sourceTransactionActionName(action)))
				if balance[key] != 1 {
					return true
				}
				balance[key] = 0
			}
		}
		for _, value := range balance {
			if value != 0 {
				return true
			}
		}
	}
	return false
}

func sourceTransactionsInNonlinearControlFlow(actions []dsl.Action, nonlinear bool) bool {
	for _, action := range actions {
		if nonlinear && (sourceTransactionStarts(action) || sourceTransactionEnds(action)) {
			return true
		}
		if action.Control != nil {
			for _, branch := range action.Control.ActionBranches() {
				if sourceTransactionsInNonlinearControlFlow(branch.Actions, true) {
					return true
				}
			}
		}
	}
	return false
}

type groupLifecycleBlockOutcome struct {
	completed       bool
	userWorkStarted bool
	err             error
}

func (mge *MultiGroupEngine) runGroupLifecycleBlock(actionCtx, boundaryCtx context.Context, group GroupConfig, protoConfig map[string]any, actions []dsl.Action, phase string) groupLifecycleBlockOutcome {
	if len(actions) == 0 {
		return groupLifecycleBlockOutcome{completed: true}
	}
	vuProto, err := protocol.Get(group.Script.Protocol)
	if err != nil {
		slog.Error("failed to create lifecycle protocol", "group", group.Name, "phase", phase, "err", scrubSensitiveError(err, group.RedactionConfig))
		return groupLifecycleBlockOutcome{err: err}
	}
	bindProtocolRuntimeRedaction(vuProto, func() *RedactionConfig { return group.RedactionConfig })
	// Protocols may retain their Init context for session/lease keepers. Give the
	// adapter an independent lifecycle and link action/boundary admission only
	// while Init is in flight. A hard stop must interrupt Init promptly, but after
	// successful admission the adapter context stays live through Teardown.
	initCtx, cancelInit, detachInitAdmission := newProtocolLifecycleContext(
		actionCtx,
		actionCtx,
		boundaryCtx,
	)
	if err := callProtocolInit(initCtx, vuProto, protoConfig, group.RedactionConfig); err != nil {
		detachInitAdmission()
		runProtocolTeardown(vuProto, func() *RedactionConfig { return group.RedactionConfig })
		cancelInit()
		if boundaryCtx.Err() != nil &&
			(errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
			return groupLifecycleBlockOutcome{}
		}
		slog.Error("failed to init lifecycle protocol", "group", group.Name, "phase", phase, "err", err)
		return groupLifecycleBlockOutcome{err: err}
	}
	detachInitAdmission()
	defer cancelInit()
	defer runProtocolTeardown(vuProto, func() *RedactionConfig { return group.RedactionConfig })
	// Protocol adapters are allowed to ignore their Init context. Recheck the
	// lifecycle boundary before creating any user-phase cleanup obligation.
	if boundaryCtx.Err() != nil || actionCtx.Err() != nil {
		return groupLifecycleBlockOutcome{}
	}

	entitledBoundaryCtx := actionCtx
	if phase != "teardown" && mge.gracefulCtx != nil {
		entitledBoundaryCtx = mge.gracefulCtx
	}
	vu := &VUser{
		ID:                           -1,
		TotalVUsers:                  group.VUsers,
		ParameterAllocation:          groupParameterAllocation(group, 0),
		Group:                        group.Name,
		Region:                       group.Region,
		RunID:                        group.RunID,
		Proto:                        vuProto,
		Script:                       group.Script,
		Metrics:                      mge.metrics,
		ResultSink:                   group.ResultSink,
		RedactionConfig:              group.RedactionConfig,
		importedCredentialInspection: group.importedCredentials,
		sourceParameters:             group.sourceParameters,
		sourceSamplers:               group.sourceSamplers,
		SecretResolver:               group.SecretResolver,
		Rendezvous:                   group.rendezvous,
		ExecutionGoals:               group.executionGoals,
		ActionBoundaryCtx:            entitledBoundaryCtx,
		DataProvider:                 group.dataProvider,
	}
	if phase != "teardown" {
		vu.PauseControl = mge
	}
	completed := vu.RunLifecycleBlockWithContexts(actionCtx, boundaryCtx, actions, phase)
	stoppedByContext := boundaryCtx.Err() != nil || actionCtx.Err() != nil
	// An at-once lifecycle VUser is phase-local. Finalize any path-dependent
	// unclosed source scope before discarding it so its reservation cannot leak
	// and the incomplete transaction is visible exactly once.
	vu.finalizeOpenSourceTransactions("source transaction ended without a matching end marker in the at-once lifecycle phase")
	outcome := groupLifecycleBlockOutcome{
		completed:       completed,
		userWorkStarted: vu.userWorkStarted,
	}
	if !completed && !stoppedByContext {
		outcome.err = fmt.Errorf("%s lifecycle action failed", phase)
	}
	return outcome
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

func waitForPostBaselineWindow(ctx context.Context, gracefulStop <-chan struct{}, duration time.Duration) error {
	if duration <= 0 {
		return nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-gracefulStop:
		// Post-baseline is idle observation, not action/teardown drain work. An
		// explicit graceful stop skips it, while natural duration completion keeps
		// the full configured window because it does not close this signal.
		return nil
	case <-timer.C:
		return nil
	}
}

// newProtocolLifecycleContext gives an initialized adapter a lifetime that is
// independent from the VUser's execution contexts. Some adapters retain their
// Init context for session/lease keepers; canceling that context on scale-down
// or a natural deadline before protocol teardown can let the remote resource
// expire while cleanup is waiting for a teardown slot.
//
// Admission contexts are linked only while Init is in flight. The caller must
// detach those links immediately after Init returns successfully, then cancel
// the lifecycle context only after protocol teardown completes.
func newProtocolLifecycleContext(valuesCtx context.Context, admissionCtxs ...context.Context) (context.Context, context.CancelFunc, func()) {
	if valuesCtx == nil {
		valuesCtx = context.Background()
	}
	lifecycleCtx, cancelLifecycle := context.WithCancel(context.WithoutCancel(valuesCtx))
	stopLinks := make([]func() bool, 0, len(admissionCtxs))
	for _, admissionCtx := range admissionCtxs {
		if admissionCtx == nil {
			continue
		}
		stopLinks = append(stopLinks, context.AfterFunc(admissionCtx, cancelLifecycle))
		// AfterFunc schedules its callback asynchronously for an already-cancelled
		// context. Make cancellation visible to Init synchronously as well.
		if admissionCtx.Err() != nil {
			cancelLifecycle()
		}
	}
	detachAdmission := func() {
		for _, stop := range stopLinks {
			stop()
		}
	}
	return lifecycleCtx, cancelLifecycle, detachAdmission
}

func (mge *MultiGroupEngine) prepareVUser(identity vuserIdentity, group GroupConfig, protoConfig map[string]any, wg *sync.WaitGroup, reserved bool) (*preparedVUser, error) {
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
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	baseCtx := mge.baseCtx
	if baseCtx == nil {
		baseCtx = parentCtx
	}
	gracefulCtx := mge.gracefulCtx
	if gracefulCtx == nil {
		gracefulCtx = baseCtx
	}
	vuCtx, vuCancel := context.WithCancel(parentCtx)
	// shedCtx interrupts the in-flight iteration body on scale/ramp-down and on
	// hard stops. It is a child of baseCtx (NOT loopCtx, so NOT a descendant of
	// vuCtx): a shared-goal exhaustion cancels loopCtx — and thus vuCtx — which
	// stops new iterations at the top of the loop, but leaves shedCtx alive so a
	// VUser that already claimed a goal reservation finishes its current
	// iteration. cancelVUser sheds load promptly by cancelling BOTH shedCtx (the
	// running iteration) and vuCtx (no further iterations).
	shedCtx, shedCancel := context.WithCancel(baseCtx)
	// Boundary cancellation is distinct from vuCtx. The shared goal limiter
	// cancels loopCtx/vuCtx after exhaustion, but a VUser that already reserved an
	// iteration is entitled to finish every action. Only explicit graceful stop,
	// hard stop, or this VUser's scale/ramp-down cancellation ends that iteration
	// at its next action boundary.
	boundaryCtx, boundaryCancel := context.WithCancel(shedCtx)
	stopGracefulLink := context.AfterFunc(gracefulCtx, boundaryCancel)
	cancelVUser := func() {
		stopGracefulLink()
		boundaryCancel()
		shedCancel()
		vuCancel()
	}
	prepared := &preparedVUser{
		identity:    identity,
		group:       group,
		runtimeID:   group.VUserIDOffset + identity.local,
		vuCtx:       vuCtx,
		cancelVUser: cancelVUser,
		shedCtx:     shedCtx,
		boundaryCtx: boundaryCtx,
		wg:          wg,
	}

	// Publish the reservation before protocol initialization so cancellation,
	// capacity accounting, and teardown can still find a VUser whose Init call is
	// in flight. Live gauges deliberately key off vuserRunning instead, which is
	// populated only after initialization succeeds and the VUser is activated.
	mge.mu.Lock()
	if !reserved && mge.launchCtx != nil && mge.launchCtx.Err() != nil {
		mge.mu.Unlock()
		mge.abortPreparedVUser(prepared)
		return nil, nil
	}
	mge.vuserCancels[identity.handle] = cancelVUser
	mge.vuserGroups[identity.handle] = group.Name
	mge.signalVUserChangeLocked()
	mge.mu.Unlock()

	if vuCtx.Err() != nil {
		mge.abortPreparedVUser(prepared)
		return nil, nil
	}
	var vuProto protocol.Protocol
	var err error
	if group.javaBroker != nil {
		vuProto, err = group.javaBroker.protocol(prepared.runtimeID)
	} else {
		vuProto, err = protocol.Get(group.Script.Protocol)
	}
	if err != nil {
		safeErr := scrubSensitiveError(err, group.RedactionConfig)
		slog.Error("failed to create protocol", "group", group.Name, "vuser", prepared.runtimeID, "err", safeErr)
		mge.abortPreparedVUser(prepared)
		return nil, safeErr
	}
	prepared.proto = vuProto
	bindProtocolRuntimeRedaction(vuProto, func() *RedactionConfig { return group.RedactionConfig })
	initCtx, cancelProtocol, detachInitAdmission := newProtocolLifecycleContext(
		shedCtx,
		vuCtx,
		shedCtx,
		mge.launchCtx,
	)
	prepared.cancelProtocol = cancelProtocol
	err = callProtocolInit(initCtx, vuProto, vuserProtoConfig(protoConfig, prepared.runtimeID), group.RedactionConfig)
	detachInitAdmission()
	if err != nil {
		safeErr := err
		launchCancellation := (vuCtx.Err() != nil || (mge.launchCtx != nil && mge.launchCtx.Err() != nil)) &&
			(errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded))
		if !launchCancellation {
			slog.Error("failed to init protocol", "group", group.Name, "vuser", prepared.runtimeID, "err", safeErr)
		}
		mge.abortPreparedVUser(prepared)
		if launchCancellation {
			return nil, nil
		}
		return nil, safeErr
	}
	if vuCtx.Err() != nil || (mge.launchCtx != nil && mge.launchCtx.Err() != nil) {
		mge.abortPreparedVUser(prepared)
		return nil, nil
	}
	return prepared, nil
}

func (mge *MultiGroupEngine) activatePreparedVUser(prepared *preparedVUser) bool {
	return mge.activatePreparedVUserWithAdmission(prepared, nil)
}

func (mge *MultiGroupEngine) activatePreparedVUserWithAdmission(prepared *preparedVUser, admissionCtx context.Context) bool {
	if prepared == nil {
		return false
	}
	if prepared.vuCtx.Err() != nil || (admissionCtx != nil && admissionCtx.Err() != nil) {
		mge.abortPreparedVUser(prepared)
		return false
	}

	mge.mu.Lock()
	if !mge.launchOpen || prepared.vuCtx.Err() != nil || (admissionCtx != nil && admissionCtx.Err() != nil) {
		mge.mu.Unlock()
		mge.abortPreparedVUser(prepared)
		return false
	}
	mge.markVUserRunningLocked(prepared.identity.handle)
	mge.signalVUserChangeLocked()
	mge.metrics.SetActiveVUsers(int64(mge.runningVUserCount()))
	mge.mu.Unlock()

	go func() {
		var vu *VUser
		redaction := func() *RedactionConfig {
			if vu != nil {
				return vu.RedactionConfig
			}
			return prepared.group.RedactionConfig
		}
		defer recoverVUserGoroutine(prepared.runtimeID, redaction)
		defer mge.finishPreparedVUser(prepared, redaction)

		vu = &VUser{
			ID:                           prepared.runtimeID,
			TotalVUsers:                  prepared.group.VUsers,
			ParameterAllocation:          groupParameterAllocation(prepared.group, prepared.identity.local),
			Group:                        prepared.group.Name,
			Region:                       prepared.group.Region,
			RunID:                        prepared.group.RunID,
			Proto:                        prepared.proto,
			Script:                       prepared.group.Script,
			Metrics:                      mge.metrics,
			ResultSink:                   prepared.group.ResultSink,
			RedactionConfig:              prepared.group.RedactionConfig,
			importedCredentialInspection: prepared.group.importedCredentials,
			sourceParameters:             prepared.group.sourceParameters,
			sourceSamplers:               prepared.group.sourceSamplers,
			SecretResolver:               prepared.group.SecretResolver,
			PauseControl:                 mge,
			IterationRateLimiter:         prepared.group.rateLimiter,
			Rendezvous:                   prepared.group.rendezvous,
			ExecutionGoals:               prepared.group.executionGoals,
			HardStopCtx:                  prepared.shedCtx,
			ActionBoundaryCtx:            prepared.boundaryCtx,
			DataProvider:                 prepared.group.dataProvider,
			SkipInit:                     groupLifecycleAtOnce(prepared.group),
			SkipTeardown:                 groupLifecycleAtOnce(prepared.group),
		}
		vu.Run(prepared.vuCtx)
	}()
	return true
}

func (mge *MultiGroupEngine) finishPreparedVUser(prepared *preparedVUser, redaction func() *RedactionConfig) {
	if prepared == nil {
		return
	}
	prepared.cleanupOnce.Do(func() {
		if prepared.proto != nil {
			runProtocolTeardown(prepared.proto, redaction)
		}
		if prepared.cancelProtocol != nil {
			prepared.cancelProtocol()
		}
		prepared.cancelVUser()
		mge.finishReservedVUser(prepared.identity.handle, prepared.wg)
	})
}

func (mge *MultiGroupEngine) abortPreparedVUser(prepared *preparedVUser) {
	if prepared == nil {
		return
	}
	mge.finishPreparedVUser(prepared, func() *RedactionConfig { return prepared.group.RedactionConfig })
}

func (mge *MultiGroupEngine) abortPreparedVUsers(prepared []*preparedVUser) {
	work := make(chan *preparedVUser)
	var cleanup sync.WaitGroup
	workers := min(len(prepared), maxConcurrentProtocolTeardowns)
	if workers == 0 {
		return
	}
	cleanup.Add(workers)
	for range workers {
		go func() {
			defer cleanup.Done()
			for vuser := range work {
				mge.abortPreparedVUser(vuser)
			}
		}()
	}
	for _, vuser := range prepared {
		if vuser == nil {
			continue
		}
		work <- vuser
	}
	close(work)
	cleanup.Wait()
}

func (mge *MultiGroupEngine) spawnVUser(identity vuserIdentity, group GroupConfig, protoConfig map[string]any, wg *sync.WaitGroup, reserved bool) (bool, error) {
	prepared, err := mge.prepareVUser(identity, group, protoConfig, wg, reserved)
	if err != nil || prepared == nil {
		return false, err
	}
	return mge.activatePreparedVUserWithAdmission(prepared, mge.launchCtx), nil
}

func (mge *MultiGroupEngine) finishReservedVUser(vuserID int, wg *sync.WaitGroup) {
	mge.mu.Lock()
	mge.unregisterVUserLocked(vuserID)
	if mge.activeReserved > 0 {
		mge.activeReserved--
	}
	if mge.activeReserved == 0 && !mge.scaleGuard {
		mge.scalingOpen = false
	}
	mge.metrics.SetActiveVUsers(int64(mge.runningVUserCount()))
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
		if _, running := mge.vuserRunning[id]; running {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		leftGroup := mge.vuserGroups[ids[i]]
		rightGroup := mge.vuserGroups[ids[j]]
		if leftGroup != rightGroup {
			return leftGroup < rightGroup
		}
		left, leftOK := mge.vuserIdentities[ids[i]]
		right, rightOK := mge.vuserIdentities[ids[j]]
		if leftOK && rightOK && left.local != right.local {
			return left.local > right.local
		}
		return ids[i] > ids[j]
	})
	for _, id := range ids {
		group := ramps[mge.vuserGroups[id]]
		if group != nil {
			mge.vuserStopping[id] = true
			group.cancels = append(group.cancels, mge.vuserCancels[id])
		}
	}
	mge.mu.Unlock()
	// Prepared-but-never-activated users are not part of the progressive
	// ramp-down. Launch cancellation transfers their cleanup back to their
	// launcher; wait for that cleanup before the hard stop so identities and
	// protocol instances cannot survive Run.
	deadline := time.Now().Add(time.Second)
	for {
		mge.mu.Lock()
		pendingPrepared := len(mge.vuserCancels) - len(mge.vuserRunning)
		changed := mge.vuserChanged
		mge.mu.Unlock()
		if pendingPrepared <= 0 || time.Now().After(deadline) {
			break
		}
		select {
		case <-changed:
		case <-time.After(10 * time.Millisecond):
		}
	}

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
				if !mge.SleepActive(mge.baseCtx, delay) {
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

// activeVUserCount returns the number of live VUser reservations. Callers MUST
// hold mge.mu; the spawn/teardown paths mutate vuserCancels under that lock.
func (mge *MultiGroupEngine) activeVUserCount() int {
	return len(mge.vuserCancels)
}

// runningVUserCount returns the successfully initialized subset used by live
// metrics. Callers MUST hold mge.mu.
func (mge *MultiGroupEngine) runningVUserCount() int {
	return len(mge.vuserRunning)
}

func (mge *MultiGroupEngine) markVUserRunningLocked(id int) {
	if mge.vuserRunning == nil {
		mge.vuserRunning = make(map[int]struct{})
	}
	mge.vuserRunning[id] = struct{}{}
}

func (mge *MultiGroupEngine) resetVUserIDStateLocked() {
	mge.nextVUserID = 0
	mge.freeVUserIDs = nil
	mge.vuserIDsInUse = make(map[int]struct{})
	mge.vuserIdentities = make(map[int]allocatedVUserIdentity)
	mge.vuserRunning = make(map[int]struct{})
	mge.groupVUserIDPools = make(map[string]*groupVUserIDPool, len(mge.groups))
	for _, group := range mge.groups {
		span := group.VUserIDSpan
		if span <= 0 {
			span = MaxEngineVUsers
		}
		if group.javaBroker != nil {
			span = min(span, len(group.javaBroker.contexts))
		}
		mge.groupVUserIDPools[group.Name] = &groupVUserIDPool{span: span, noReuse: group.javaBroker != nil}
	}
}

func (mge *MultiGroupEngine) allocateVUserIdentity(groupName string) (vuserIdentity, error) {
	mge.mu.Lock()
	defer mge.mu.Unlock()
	return mge.allocateVUserIdentityLocked(groupName)
}

func (mge *MultiGroupEngine) allocateVUserIdentityLocked(groupName string) (vuserIdentity, error) {
	pool := mge.groupVUserIDPools[groupName]
	if pool == nil {
		return vuserIdentity{}, fmt.Errorf("group %q has no vuser id pool", groupName)
	}
	handle, err := mge.allocateVUserIDLocked()
	if err != nil {
		return vuserIdentity{}, err
	}

	local := 0
	switch {
	case len(pool.free) > 0:
		local = heap.Pop(&pool.free).(int)
	case pool.next < pool.span:
		local = pool.next
		pool.next++
	default:
		mge.releaseVUserHandleLocked(handle)
		return vuserIdentity{}, fmt.Errorf("group %q vuser id namespace exhausted (%d)", groupName, pool.span)
	}
	pool.allocated++
	mge.vuserIdentities[handle] = allocatedVUserIdentity{groupName: groupName, local: local}
	return vuserIdentity{handle: handle, local: local}, nil
}

func (mge *MultiGroupEngine) availableVUserIdentitiesLocked(groupName string) (int, error) {
	pool := mge.groupVUserIDPools[groupName]
	if pool == nil {
		return 0, fmt.Errorf("group %q has no vuser id pool", groupName)
	}
	globalAvailable := mge.vuserIDSpan - len(mge.vuserIDsInUse)
	groupAvailable := pool.span - pool.allocated
	if pool.noReuse {
		groupAvailable = pool.span - pool.next
	}
	return min(globalAvailable, groupAvailable), nil
}

// waitForGroupRetirementsLocked keeps a scale-up from overtaking an earlier
// asynchronous scale-down or rollback. The stopping VUsers still own their
// group-local ordinals until teardown completes; allocating around them can
// create sparse IDs that alias modulo-based unique data. The caller holds
// mge.mu, and this helper returns with it held.
func (mge *MultiGroupEngine) waitForGroupRetirementsLocked(groupName string) error {
	for {
		stopping := false
		for id, isStopping := range mge.vuserStopping {
			if isStopping && mge.vuserGroups[id] == groupName {
				stopping = true
				break
			}
		}
		if !stopping {
			return nil
		}
		changed := mge.vuserChanged
		loopCtx := mge.loopCtx
		launchCtx := mge.launchCtx
		mge.mu.Unlock()
		if loopCtx == nil {
			mge.mu.Lock()
			return fmt.Errorf("engine not running")
		}
		select {
		case <-loopCtx.Done():
			mge.mu.Lock()
			return fmt.Errorf("engine stopped while waiting for group %q vusers to retire", groupName)
		case <-launchCtx.Done():
			mge.mu.Lock()
			return fmt.Errorf("engine stopped while waiting for group %q vusers to retire", groupName)
		case <-changed:
			mge.mu.Lock()
		}
		if mge.baseCtx == nil || mge.baseCtx.Err() != nil || !mge.controlsReady || !mge.scalingOpen {
			return fmt.Errorf("engine not running")
		}
	}
}

func (mge *MultiGroupEngine) allocateVUserID() (int, error) {
	mge.mu.Lock()
	defer mge.mu.Unlock()
	return mge.allocateVUserIDLocked()
}

func (mge *MultiGroupEngine) allocateVUserIDLocked() (int, error) {
	if len(mge.vuserIDsInUse) >= mge.vuserIDSpan {
		return 0, fmt.Errorf("vuser id namespace exhausted (%d)", mge.vuserIDSpan)
	}
	if len(mge.freeVUserIDs) > 0 {
		id := heap.Pop(&mge.freeVUserIDs).(int)
		mge.vuserIDsInUse[id] = struct{}{}
		return id, nil
	}
	if mge.nextVUserID >= mge.vuserIDSpan {
		return 0, fmt.Errorf("vuser id namespace exhausted (%d)", mge.vuserIDSpan)
	}
	id := mge.nextVUserID
	mge.nextVUserID++
	mge.vuserIDsInUse[id] = struct{}{}
	return id, nil
}

func (mge *MultiGroupEngine) releaseVUserIDLocked(id int) {
	if identity, allocated := mge.vuserIdentities[id]; allocated {
		if pool := mge.groupVUserIDPools[identity.groupName]; pool != nil {
			if pool.allocated > 0 {
				pool.allocated--
			}
			if !pool.noReuse {
				heap.Push(&pool.free, identity.local)
			}
		}
		delete(mge.vuserIdentities, id)
	}
	mge.releaseVUserHandleLocked(id)
}

func (mge *MultiGroupEngine) releaseVUserHandleLocked(id int) {
	if _, allocated := mge.vuserIDsInUse[id]; !allocated {
		return
	}
	delete(mge.vuserIDsInUse, id)
	heap.Push(&mge.freeVUserIDs, id)
}

func (mge *MultiGroupEngine) spawnOpenWorker(identity vuserIdentity, group GroupConfig, protoConfig map[string]any, wg *sync.WaitGroup, release func()) (bool, error) {
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
	boundaryCtx, boundaryCancel := context.WithCancel(shedCtx)
	stopGracefulLink := context.AfterFunc(mge.gracefulCtx, boundaryCancel)
	cancelVUser := func() {
		stopGracefulLink()
		boundaryCancel()
		shedCancel()
		vuCancel()
	}

	mge.mu.Lock()
	mge.vuserCancels[identity.handle] = cancelVUser
	mge.vuserGroups[identity.handle] = group.Name
	mge.signalVUserChangeLocked()
	mge.mu.Unlock()

	if vuCtx.Err() != nil {
		cancelVUser()
		mge.mu.Lock()
		mge.unregisterVUserLocked(identity.handle)
		mge.metrics.SetActiveVUsers(int64(mge.runningVUserCount()))
		mge.mu.Unlock()
		release()
		return false, nil
	}

	runtimeVUserID := group.VUserIDOffset + identity.local
	vuProto, err := protocol.Get(group.Script.Protocol)
	if err != nil {
		safeErr := scrubSensitiveError(err, group.RedactionConfig)
		slog.Error("failed to create protocol for open workload worker", "group", group.Name, "worker", runtimeVUserID, "err", safeErr)
		cancelVUser()
		mge.mu.Lock()
		mge.unregisterVUserLocked(identity.handle)
		mge.metrics.SetActiveVUsers(int64(mge.runningVUserCount()))
		mge.mu.Unlock()
		release()
		return false, safeErr
	}
	bindProtocolRuntimeRedaction(vuProto, func() *RedactionConfig { return group.RedactionConfig })
	initCtx, cancelProtocol, detachInitAdmission := newProtocolLifecycleContext(
		shedCtx,
		vuCtx,
		shedCtx,
		mge.launchCtx,
	)
	cleanupProtocol := func() {
		runProtocolTeardown(vuProto, func() *RedactionConfig { return group.RedactionConfig })
		cancelProtocol()
	}
	err = callProtocolInit(initCtx, vuProto, vuserProtoConfig(protoConfig, runtimeVUserID), group.RedactionConfig)
	detachInitAdmission()
	if err != nil {
		safeErr := err
		launchCancellation := (vuCtx.Err() != nil || (mge.launchCtx != nil && mge.launchCtx.Err() != nil)) &&
			(errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded))
		if !launchCancellation {
			slog.Error("failed to init protocol for open workload worker", "group", group.Name, "worker", runtimeVUserID, "err", safeErr)
		}
		cancelVUser()
		cleanupProtocol()
		mge.mu.Lock()
		mge.unregisterVUserLocked(identity.handle)
		mge.metrics.SetActiveVUsers(int64(mge.runningVUserCount()))
		mge.mu.Unlock()
		release()
		if launchCancellation {
			return false, nil
		}
		return false, safeErr
	}
	if vuCtx.Err() != nil {
		cancelVUser()
		cleanupProtocol()
		mge.mu.Lock()
		mge.unregisterVUserLocked(identity.handle)
		mge.metrics.SetActiveVUsers(int64(mge.runningVUserCount()))
		mge.mu.Unlock()
		release()
		return false, nil
	}
	launchCtx := mge.launchCtx
	if launchCtx != nil && launchCtx.Err() != nil {
		cancelVUser()
		cleanupProtocol()
		mge.mu.Lock()
		mge.unregisterVUserLocked(identity.handle)
		mge.metrics.SetActiveVUsers(int64(mge.runningVUserCount()))
		mge.mu.Unlock()
		release()
		return false, nil
	}

	mge.mu.Lock()
	if !mge.launchOpen || (launchCtx != nil && launchCtx.Err() != nil) {
		mge.mu.Unlock()
		cancelVUser()
		cleanupProtocol()
		mge.mu.Lock()
		mge.unregisterVUserLocked(identity.handle)
		mge.metrics.SetActiveVUsers(int64(mge.runningVUserCount()))
		mge.mu.Unlock()
		release()
		return false, nil
	}
	mge.markVUserRunningLocked(identity.handle)
	mge.metrics.SetActiveVUsers(int64(mge.runningVUserCount()))
	mge.mu.Unlock()

	iterationScript := cloneSingleIterationScript(group.Script)
	wg.Add(1)
	go func() {
		var vu *VUser
		redaction := func() *RedactionConfig {
			if vu != nil {
				return vu.RedactionConfig
			}
			return group.RedactionConfig
		}
		defer recoverVUserGoroutine(runtimeVUserID, redaction)
		defer wg.Done()
		defer release()
		defer cancelVUser()
		defer func() {
			runProtocolTeardown(vuProto, redaction)
			cancelProtocol()
			mge.mu.Lock()
			mge.unregisterVUserLocked(identity.handle)
			mge.metrics.SetActiveVUsers(int64(mge.runningVUserCount()))
			mge.mu.Unlock()
		}()

		vu = &VUser{
			ID:                           runtimeVUserID,
			Group:                        group.Name,
			Region:                       group.Region,
			RunID:                        group.RunID,
			Proto:                        vuProto,
			Script:                       iterationScript,
			Metrics:                      mge.metrics,
			ResultSink:                   group.ResultSink,
			RedactionConfig:              group.RedactionConfig,
			importedCredentialInspection: group.importedCredentials,
			sourceParameters:             group.sourceParameters,
			sourceSamplers:               group.sourceSamplers,
			SecretResolver:               group.SecretResolver,
			PauseControl:                 mge,
			ExecutionGoals:               group.executionGoals,
			HardStopCtx:                  shedCtx,
			ActionBoundaryCtx:            boundaryCtx,
			DataProvider:                 group.dataProvider,
			SkipInit:                     groupLifecycleAtOnce(group),
			SkipTeardown:                 groupLifecycleAtOnce(group),
		}
		vu.Run(vuCtx)
	}()
	return true, nil
}

// ScaleVUsers adds or removes VUsers for a specific group.
// If targetVUsers > current, new VUsers are spawned.
// If targetVUsers < current, excess VUsers are cancelled (LIFO).
func (mge *MultiGroupEngine) ScaleVUsers(groupName string, targetVUsers int) error {
	if mge.terminationMode == dsl.TerminationIterations {
		return fmt.Errorf("VUser scaling is unavailable for iteration termination")
	}
	mge.scaleMu.Lock()
	defer mge.scaleMu.Unlock()
	if targetVUsers < 0 {
		return fmt.Errorf("target vusers must be non-negative")
	}
	if targetVUsers > MaxEngineVUsers {
		return fmt.Errorf("target vusers must not exceed %d", MaxEngineVUsers)
	}

	mge.mu.Lock()
	groupIndex := -1
	for i := range mge.groups {
		if mge.groups[i].Name == groupName {
			groupIndex = i
			break
		}
	}
	if groupIndex < 0 {
		mge.mu.Unlock()
		return fmt.Errorf("group not found: %s", groupName)
	}
	if dsl.RequiresJMeterMessaging(mge.groups[groupIndex].Script) {
		current := mge.groups[groupIndex].VUsers
		mge.mu.Unlock()
		if targetVUsers == current {
			return nil
		}
		return fmt.Errorf("JMeter source thread population is fixed; changing VUsers requires a new run")
	}
	if mge.baseCtx != nil && mge.baseCtx.Err() == nil &&
		mge.loopCtx != nil && mge.loopCtx.Err() == nil &&
		mge.launchCtx != nil && mge.launchCtx.Err() == nil && mge.launchOpen &&
		mge.wg != nil && mge.controlsReady && !mge.scalingOpen && mge.initialRampInProgress {
		mge.mu.Unlock()
		return ErrInitialRampInProgress
	}
	if mge.wg == nil || !mge.scalingAdmissionOpenLocked() {
		mge.mu.Unlock()
		return fmt.Errorf("engine not running")
	}
	snapshotGroup := mge.groups[groupIndex]
	if HasAllocatedParameters(snapshotGroup.Script) {
		mge.mu.Unlock()
		if targetVUsers == snapshotGroup.VUsers {
			return nil
		} // no restart or row-cursor reset on an idempotent request
		return fmt.Errorf("unique parameter blocks are frozen; changing VUser population requires a new run")
	}
	if isOpenWorkload(snapshotGroup.Script) {
		mge.mu.Unlock()
		return fmt.Errorf("scaling open workload groups is not supported")
	}
	aggregateTarget := int64(targetVUsers)
	for i := range mge.groups {
		if i != groupIndex {
			aggregateTarget += int64(mge.groups[i].VUsers)
		}
	}
	if aggregateTarget > MaxEngineVUsers {
		mge.mu.Unlock()
		return fmt.Errorf("aggregate target vusers must not exceed %d", MaxEngineVUsers)
	}
	if snapshotGroup.VUserIDSpan > 0 && targetVUsers > snapshotGroup.VUserIDSpan {
		mge.mu.Unlock()
		return fmt.Errorf("target vusers %d exceed vuser id span %d", targetVUsers, snapshotGroup.VUserIDSpan)
	}
	if targetVUsers > 0 && snapshotGroup.VUserIDOffset > int(^uint(0)>>1)-targetVUsers {
		mge.mu.Unlock()
		return fmt.Errorf("target vusers overflow the configured vuser id offset")
	}
	if snapshotGroup.javaBroker != nil && targetVUsers > snapshotGroup.VUsers {
		available, err := mge.availableVUserIdentitiesLocked(groupName)
		if err != nil || targetVUsers-snapshotGroup.VUsers > available {
			mge.mu.Unlock()
			return fmt.Errorf("java_broker_additional_ready_reservation_required")
		}
	}
	targetCapacityPlan := NewJavaVuserCapacityPlan(snapshotGroup.Script)
	currentVUsers := snapshotGroup.VUsers
	mge.mu.Unlock()

	var observedPersistentJavaLimit int64
	if snapshotGroup.javaBroker == nil && targetCapacityPlan.RequiresPersistentLifecycle() && targetVUsers > currentVUsers {
		// Capability discovery can cold-start the sidecar probe. Keep it outside
		// the engine state lock so teardown, metrics, and stop controls continue.
		instance, err := protocol.Get("java_vuser")
		if err != nil {
			return fmt.Errorf("inspect java_vuser runtime capacity: %w", err)
		}
		capabilities, err := javaVuserPersistentCapacityForInstance(instance)
		if err != nil {
			return err
		}
		observedPersistentJavaLimit = capabilities.MaxVUsers
	}

	mge.mu.Lock()
	defer mge.mu.Unlock()

	if mge.wg == nil || !mge.scalingAdmissionOpenLocked() {
		return fmt.Errorf("engine not running")
	}

	group := &mge.groups[groupIndex]
	if group.Name != groupName || group.VUsers != currentVUsers {
		return fmt.Errorf("group changed while inspecting java_vuser runtime capacity")
	}
	if isOpenWorkload(group.Script) {
		return fmt.Errorf("scaling open workload groups is not supported")
	}
	aggregateTarget = int64(targetVUsers)
	for i := range mge.groups {
		if &mge.groups[i] != group {
			aggregateTarget += int64(mge.groups[i].VUsers)
		}
	}
	if aggregateTarget > MaxEngineVUsers {
		return fmt.Errorf("aggregate target vusers must not exceed %d", MaxEngineVUsers)
	}
	if group.VUserIDSpan > 0 && targetVUsers > group.VUserIDSpan {
		return fmt.Errorf("target vusers %d exceed vuser id span %d", targetVUsers, group.VUserIDSpan)
	}
	if targetVUsers > 0 && group.VUserIDOffset > int(^uint(0)>>1)-targetVUsers {
		return fmt.Errorf("target vusers overflow the configured vuser id offset")
	}
	delta := targetVUsers - currentVUsers
	if delta > 0 {
		// A cancelled VUser remains the owner of its group-local runtime ID
		// until its goroutine and protocol teardown finish. Do not allocate
		// around that hole: doing so can expose sparse IDs (and duplicate
		// modulo-selected unique data) on an immediate scale reversal.
		if err := mge.waitForGroupRetirementsLocked(group.Name); err != nil {
			return err
		}
		if group.Name != groupName || group.VUsers != currentVUsers {
			return fmt.Errorf("group changed while waiting for retiring vusers")
		}
		availableIDs, err := mge.availableVUserIdentitiesLocked(group.Name)
		if err != nil {
			return err
		}
		if delta > availableIDs {
			return fmt.Errorf("vuser id namespace has %d available ids, need %d", availableIDs, delta)
		}
	}
	if group.javaBroker == nil && targetCapacityPlan.RequiresPersistentLifecycle() && delta != 0 {
		persistentJavaTarget := persistentJavaScaleCapacityDemand(
			mge.groups,
			mge.liveVUsersByGroupLocked(),
			groupIndex,
			targetVUsers,
		)
		if err := mge.javaVuserReservation.resizeWithinLimit(persistentJavaTarget, observedPersistentJavaLimit); err != nil {
			return err
		}
	}
	group.VUsers = targetVUsers

	if delta > 0 {
		protoConfig := buildGroupProtoConfig(*group, "load")
		protoConfig["group_name"] = group.Name
		applyRuntimeMockNamespace(protoConfig, mge.mockNamespace)

		spawnedIDs := make([]int, 0, delta)
		rollbackScaleUp := func() {
			group.VUsers = currentVUsers
			for _, id := range spawnedIDs {
				if cancel := mge.vuserCancels[id]; cancel != nil {
					mge.vuserStopping[id] = true
					cancel()
				}
			}
			if err := mge.reconcileJavaVuserCapacityLocked(); err != nil {
				slog.Error("failed to reconcile java_vuser capacity after scale rollback", "group", groupName, "err", err)
			}
		}
		for i := 0; i < delta; i++ {
			identity, err := mge.allocateVUserIdentityLocked(group.Name)
			if err != nil {
				rollbackScaleUp()
				return err
			}
			mge.activeReserved++
			mge.wg.Add(1)

			mge.mu.Unlock()
			launched, spawnErr := mge.spawnVUser(identity, *group, protoConfig, mge.wg, true)
			mge.mu.Lock()
			if spawnErr != nil {
				rollbackScaleUp()
				return fmt.Errorf("initialize scaled vuser: %w", spawnErr)
			}
			if !launched {
				rollbackScaleUp()
				return fmt.Errorf("initialize scaled vuser: execution stopped before the VUser launched")
			}
			spawnedIDs = append(spawnedIDs, identity.handle)
		}
	} else if delta < 0 {
		toRemove := -delta
		ids := make([]int, 0, toRemove)
		for id := range mge.vuserCancels {
			if mge.vuserGroups[id] == groupName && !mge.vuserStopping[id] {
				ids = append(ids, id)
			}
		}
		sort.Slice(ids, func(i, j int) bool {
			left, leftOK := mge.vuserIdentities[ids[i]]
			right, rightOK := mge.vuserIdentities[ids[j]]
			if leftOK && rightOK && left.local != right.local {
				return left.local > right.local
			}
			return ids[i] > ids[j]
		})
		if len(ids) < toRemove {
			group.VUsers = currentVUsers
			if err := mge.reconcileJavaVuserCapacityLocked(); err != nil {
				slog.Error("failed to reconcile java_vuser capacity after scale rollback", "group", groupName, "err", err)
			}
			return fmt.Errorf("group %q only has %d active vusers available to remove", groupName, len(ids))
		}
		if targetVUsers == 0 && !mge.scaleGuard {
			// Keep Run's WaitGroup non-zero while the group is paused at zero so a
			// later resume can safely Add VUsers. Install it only after validating
			// the complete removal set; a rejected scale must not pin the engine.
			mge.scaleGuard = true
			mge.wg.Add(1)
			go mge.holdScaleGuard(mge.loopCtx, mge.wg)
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
	for i := range mge.groups {
		if dsl.RequiresJMeterMessaging(mge.groups[i].Script) {
			return fmt.Errorf("JMeter source-plan duration is fixed")
		}
	}
	if mge.terminationMode == dsl.TerminationIterations {
		return fmt.Errorf("duration adjustment is unavailable for iteration termination")
	}
	mge.mu.Lock()
	defer mge.mu.Unlock()

	return mge.adjustDurationLocked(newDuration)
}

func (mge *MultiGroupEngine) adjustDurationLocked(newDuration time.Duration) error {
	if newDuration > mge.baseTargetDuration {
		for _, group := range mge.groups {
			if group.javaBroker != nil {
				return fmt.Errorf("java_broker_execution_lifetime_fixed")
			}
		}
	}
	if mge.baseCtx == nil || mge.baseCtx.Err() != nil || mge.launchCtx == nil || mge.launchCtx.Err() != nil || !mge.launchOpen || !mge.controlsReady {
		return fmt.Errorf("engine not running")
	}

	mge.baseTargetDuration = newDuration
	mge.targetDuration = newDuration + mge.pauseCompensation

	slog.Info("test duration adjusted", "new_duration", newDuration)
	return nil
}

// GetTargetDuration returns the current target duration (may differ from original if adjusted).
func (mge *MultiGroupEngine) GetTargetDuration() time.Duration {
	mge.mu.Lock()
	defer mge.mu.Unlock()
	return mge.targetDuration
}

// GetBaseTargetDuration returns the operator-controlled duration without pause
// compensation. Control planes use it to restore a multi-engine adjustment if
// one participant rejects the new duration.
func (mge *MultiGroupEngine) GetBaseTargetDuration() time.Duration {
	mge.mu.Lock()
	defer mge.mu.Unlock()
	return mge.baseTargetDuration
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

	if mge.baseCtx == nil {
		mge.pauseRequested = true
		mge.paused = true
		return nil
	}
	if mge.baseCtx.Err() != nil || (!mge.controlsReady && !mge.launchOpen) {
		return fmt.Errorf("engine not running")
	}
	if mge.paused {
		mge.pauseRequested = true
		return nil
	}

	mge.pauseRequested = true
	mge.paused = true
	now := time.Now()
	mge.pauseStartTime = now
	if mge.runStartedAt.After(now) {
		// A coordinated execution may be prepared before its future barrier.
		// Time paused before that barrier is not active run time and must not be
		// subtracted again after Resume.
		mge.pauseStartTime = mge.runStartedAt
	}
	mge.pauseCh = make(chan struct{}) // New channel blocks waiters
	mge.notifyPauseChangedLocked()
	for i := range mge.groups {
		// Open-workload groups are arrival-scheduled and intentionally have no
		// closed-model iteration rate limiter. Their scheduler observes the same
		// execution gate through WaitIfPaused/SleepActive, so there is no limiter
		// state to update here.
		if mge.groups[i].rateLimiter != nil {
			mge.groups[i].rateLimiter.SetPaused(true)
		}
	}

	slog.Info("test execution paused")
	return nil
}

func (mge *MultiGroupEngine) Resume() error {
	mge.mu.Lock()
	defer mge.mu.Unlock()

	if mge.baseCtx == nil {
		mge.pauseRequested = false
		mge.paused = false
		return nil
	}
	if mge.baseCtx.Err() != nil || (!mge.controlsReady && !mge.launchOpen) {
		return fmt.Errorf("engine not running")
	}
	mge.pauseRequested = false
	if !mge.paused {
		return nil
	}

	pauseDuration := time.Since(mge.pauseStartTime)
	if pauseDuration < 0 {
		pauseDuration = 0
	}
	mge.paused = false
	close(mge.pauseCh) // Closing unblocks all waiters
	mge.notifyPauseChangedLocked()
	for i := range mge.groups {
		if mge.groups[i].rateLimiter != nil {
			mge.groups[i].rateLimiter.SetPaused(false)
		}
	}

	// Keep the user-visible duration as the active-runtime base and retain pause
	// compensation separately. A later AdjustDuration must replace only the base,
	// not erase time already spent paused.
	mge.pauseCompensation += pauseDuration
	mge.targetDuration = mge.baseTargetDuration + mge.pauseCompensation

	slog.Info("test execution resumed", "pause_duration", pauseDuration)
	return nil
}

func (mge *MultiGroupEngine) notifyPauseChangedLocked() {
	if mge.pauseChanged != nil {
		close(mge.pauseChanged)
	}
	mge.pauseChanged = make(chan struct{})
}

// SleepActive waits for delay units of unpaused execution time. A pause freezes
// the remaining delay rather than allowing ramp, pacing, or arrival timers to
// expire against wall time.
func (mge *MultiGroupEngine) SleepActive(ctx context.Context, delay time.Duration) bool {
	if ctx == nil {
		ctx = context.Background()
	}
	remaining := delay
	for remaining > 0 {
		mge.mu.Lock()
		paused := mge.paused
		pauseCh := mge.pauseCh
		changed := mge.pauseChanged
		mge.mu.Unlock()
		if paused {
			select {
			case <-ctx.Done():
				return false
			case <-pauseCh:
				continue
			}
		}

		startedActive := mge.ActiveElapsed()
		timer := time.NewTimer(remaining)
		select {
		case <-ctx.Done():
			// Go 1.23+ timer channels must not be drained after Stop; doing so
			// can block forever when cancellation wins at the expiry boundary.
			timer.Stop()
			return false
		case <-changed:
			timer.Stop()
			elapsed := mge.ActiveElapsed() - startedActive
			if elapsed > 0 {
				remaining -= elapsed
			}
		case <-timer.C:
			return true
		}
	}
	return ctx.Err() == nil
}

// waitUntilActive waits for an absolute point on the engine's active-time
// clock. Absolute deadlines keep every group aligned to the same load epoch even
// if a launcher goroutine is scheduled late; SleepActive retains pause-aware
// behavior for the remaining interval.
func (mge *MultiGroupEngine) waitUntilActive(ctx context.Context, deadline time.Duration) bool {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		mge.mu.Lock()
		start := mge.runStartedAt
		mge.mu.Unlock()
		if !start.IsZero() {
			if untilStart := time.Until(start); untilStart > 0 {
				// ActiveElapsed deliberately clamps pre-start time to zero. Keep a
				// zero-deadline launcher behind the wall-clock barrier as well, or
				// prepared RunAt VUsers could activate before executionStart.
				if !sleepContext(ctx, untilStart) {
					return false
				}
				continue
			}
		}
		remaining := deadline - mge.ActiveElapsed()
		if remaining <= 0 {
			return ctx.Err() == nil
		}
		if !mge.SleepActive(ctx, remaining) {
			return false
		}
	}
}

func (mge *MultiGroupEngine) finishInitialRamp() {
	mge.mu.Lock()
	defer mge.mu.Unlock()
	mge.initialRampInProgress = false
	defer mge.signalVUserChangeLocked()
	if mge.baseCtx == nil || mge.baseCtx.Err() != nil || mge.loopCtx == nil || mge.loopCtx.Err() != nil || !mge.controlsReady {
		mge.scalingOpen = false
		return
	}
	// The scale guard keeps a zero-capable group alive for future admission. A
	// normal group may accept scaling only while at least one configured VUser is
	// still reserved; otherwise Run's WaitGroup may already be completing.
	mge.scalingOpen = mge.activeReserved > 0 || mge.scaleGuard
}

// ActiveElapsed returns elapsed execution time excluding completed and current
// pauses. It gives interval pacing an active-time clock instead of wall time.
func (mge *MultiGroupEngine) ActiveElapsed() time.Duration {
	mge.mu.Lock()
	defer mge.mu.Unlock()
	return mge.activeElapsedAtLocked(time.Now())
}

func (mge *MultiGroupEngine) activeElapsedAtLocked(now time.Time) time.Duration {
	if mge.runStartedAt.IsZero() {
		return 0
	}
	elapsed := now.Sub(mge.runStartedAt) - mge.pauseCompensation
	if mge.paused && !mge.pauseStartTime.IsZero() {
		elapsed -= now.Sub(mge.pauseStartTime)
	}
	if elapsed < 0 {
		return 0
	}
	return elapsed
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
	mge.stop(true)
}

func (mge *MultiGroupEngine) stop(hard bool) {
	mge.mu.Lock()
	// Close launch/control admission under the same lock used by VUser
	// activation. Cancelling the context after unlocking is still required to
	// wake in-flight work, but must not leave a window where a new VUser can be
	// published after Stop has linearized.
	mge.launchOpen = false
	mge.scalingOpen = false
	mge.controlsReady = false
	if hard {
		mge.hardStopRequested = true
	}
	cancel := mge.cancel
	groups := append([]GroupConfig(nil), mge.groups...)
	mge.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	for _, group := range groups {
		if group.javaBroker != nil {
			group.javaBroker.cancel()
		}
	}
}

func (mge *MultiGroupEngine) GracefulStop() {
	mge.mu.Lock()
	if mge.gracefulStopCh == nil {
		mge.gracefulStopCh = make(chan struct{})
	}
	if !mge.gracefulStopRequested {
		close(mge.gracefulStopCh)
	}
	mge.gracefulStopRequested = true
	// Close scale admission at the same linearization point as the graceful
	// fence. Relying on holdScaleGuard to observe loopCtx asynchronously leaves
	// a window where a control plan admitted before the fence can spawn VUsers
	// after the fence has returned.
	mge.scalingOpen = false
	mge.controlsReady = false
	mge.launchOpen = false
	cancel := mge.softStop
	mge.mu.Unlock()
	if cancel != nil {
		cancel()
		return
	}
	mge.stop(false)
}

// GracefulStopRequested reports whether the engine was asked to drain current
// actions instead of hard-canceling them. It is safe for controller lifecycle
// diagnostics and tests while a run is active.
func (mge *MultiGroupEngine) GracefulStopRequested() bool {
	mge.mu.Lock()
	defer mge.mu.Unlock()
	return mge.gracefulStopRequested
}

// HardStopRequested reports whether Stop escalated this engine to immediate
// cancellation. It remains true after a prior GracefulStop so callers can
// distinguish a completed drain from a later force-abort escalation.
func (mge *MultiGroupEngine) HardStopRequested() bool {
	mge.mu.Lock()
	defer mge.mu.Unlock()
	return mge.hardStopRequested
}

func (mge *MultiGroupEngine) LatestSnapshot() (MetricsSnapshot, bool) {
	return mge.metrics.LatestSnapshot()
}

func (mge *MultiGroupEngine) GroupNames() []string {
	names := make([]string, len(mge.groups))
	for i := range mge.groups {
		names[i] = mge.groups[i].Name
	}
	return names
}

func groupParameterAllocation(group GroupConfig, local int) *ParameterAllocation {
	a := group.ParameterAllocation
	a.Offset += local
	return &a
}

func (mge *MultiGroupEngine) CompletedIterations() int64 { return mge.metrics.CompletedIterations() }

func (mge *MultiGroupEngine) GroupCompletedIterations(group string) int64 {
	return mge.metrics.GroupCompletedIterations(group)
}
