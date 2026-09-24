// Package engine implements the core VUser (virtual user) execution engine.
//
// The engine manages a pool of goroutines (one per VUser), schedules their execution
// via a pacing algorithm (ramp-up, steady-state), and aggregates metrics in a
// zero-lock hot path using buffered channels.
//
// Key design:
//   - 1 VUser = 1 goroutine (~2–4 KB stack); supports 10k+ VUsers per pod
//   - Metrics use buffered channels (cap 10,000) to avoid lock contention
//   - sync.Pool reuses request/response buffers to reduce GC pressure
//   - T-digest for streaming percentile (p50/p95/p99) computation
//
// Example usage:
//
//	cfg := engine.Config{
//	    Script:   script,
//	    VUsers:   100,
//	    Duration: 5 * time.Minute,
//	    RampUp:   30 * time.Second,
//	}
//	eng, err := engine.New(cfg)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	result, err := eng.Run(ctx)
package engine

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

const (
	// MaxEngineVUsers is a defense-in-depth admission limit at the goroutine
	// creation boundary. API/controller validation uses the same value, but the
	// engine must remain safe for CLI and direct library callers as well.
	MaxEngineVUsers = 1_000_000
	MaxEngineGroups = 256
)

// Engine orchestrates VUser goroutines and metrics collection.
//
// The engine manages the full lifecycle of a load test:
//   - Init phase: Protocol initialization and setup actions
//   - Ramp-up phase: Gradual increase of active VUsers per pacing config
//   - Steady-state phase: All VUsers running; actions repeated until duration expires
//   - Teardown phase: Cleanup actions, graceful shutdown
//
// Metrics are collected asynchronously via buffered channels to minimize
// contention. Call MetricsSnapshot() periodically (e.g., every second) to
// extract aggregated metrics during or after a run.
type Engine struct {
	script          *dsl.Script
	vusers          int
	duration        time.Duration
	terminationMode string
	rampUp          time.Duration
	metrics         *MetricsCollector
	data            *DataProvider
	mu              sync.Mutex // guards cancel
	cancel          context.CancelFunc
	softStop        context.CancelFunc
	secrets         SecretResolver
	redaction       *RedactionConfig
	// importedCredentials is the run's single retained-credential locator walk.
	// Every VUser this engine creates is handed it; see
	// runtimeSecretValuesForScript.
	importedCredentials *importedCredentialInspection
	sourceParameters    *sourceParameterState
	// javaVuserCapacityLimit is populated by the governed runtime validation
	// immediately before Run acquires its process-wide persistent-session lease.
	javaVuserCapacityLimit int64
}

// Config contains configuration for Engine creation.
type Config struct {
	TerminationMode string
	Script          *dsl.Script   // Test script (required)
	VUsers          int           // Number of virtual users (required, > 0)
	Duration        time.Duration // Total test duration (required, > 0)
	RampUp          time.Duration // Ramp-up period (can be 0 for immediate start)
	SecretResolver  SecretResolver
}

// RunResult contains final test results after execution completes.
type RunResult struct {
	Duration          time.Duration   // Actual test duration
	TotalTransactions int64           // Total completed transactions across all VUsers
	TotalErrors       int64           // Total failed transactions
	FinalSnapshot     MetricsSnapshot // Aggregated metrics at end of run
}

// New creates a new Engine with the given config.
//
// Validation:
//   - script must not be nil
//   - vusers must be > 0
//   - duration must be > 0
//
// The returned engine is not yet running; call Run() to start it.
func New(cfg Config) (*Engine, error) {
	if cfg.Script == nil {
		return nil, fmt.Errorf("script is required")
	}
	if err := validateMessageDetailRuntimeSupport(cfg.Script); err != nil {
		return nil, err
	}
	if err := importExecutionGateError(cfg.Script, "launch"); err != nil {
		return nil, err
	}
	if javaVuserScriptUsesPhaseLifecycle(cfg.Script) && javaVuserScriptHasLegacyRunAction(cfg.Script) {
		return nil, fmt.Errorf("java_vuser scripts must not mix persistent phase methods with legacy full_lifecycle actions")
	}
	if issues := dsl.ValidateWorkloadSettingsIssues(cfg.Script.Config.RunLogic, cfg.Script.Config.Pacing); len(issues) > 0 {
		return nil, fmt.Errorf("invalid workload settings: %s", issues[0])
	}
	if cfg.VUsers <= 0 {
		return nil, fmt.Errorf("vusers must be positive")
	}
	if cfg.VUsers > MaxEngineVUsers {
		return nil, fmt.Errorf("vusers must not exceed %d", MaxEngineVUsers)
	}
	if err := dsl.ValidateTermination(cfg.TerminationMode, cfg.Duration, cfg.Script); err != nil {
		return nil, err
	}
	dataProvider, err := NewDataProvider(cfg.Script.DataSources)
	if err != nil {
		return nil, fmt.Errorf("initialize data sources: %s", scrubSensitiveText(err.Error()))
	}
	if HasAllocatedParameters(cfg.Script) && isOpenWorkload(cfg.Script) {
		return nil, fmt.Errorf("unique parameter allocation requires a closed workload")
	}
	if err := ValidateParameterAllocationTopology(cfg.Script); err != nil {
		return nil, err
	}
	allocation := ParameterAllocation{Population: cfg.VUsers, Iterations: cfg.Script.Config.RunLogic.Iterations}
	if _, _, err := CompileParameterAllocation(cfg.Script.Parameters, dataProvider, allocation); err != nil {
		return nil, err
	}
	secretResolver := cfg.SecretResolver
	if secretResolver == nil {
		secretResolver = newDefaultSecretResolverForProject(cfg.Script.ProjectID)
	}
	secretResolver, secretValues, err := snapshotReferencedSecrets(cfg.Script, secretResolver)
	if err != nil {
		return nil, fmt.Errorf("inspect script secret references: %s", scrubSensitiveText(err.Error()))
	}
	// Values retained from an import join the matcher only when the operator has
	// left script.Config.Log.RedactRunEvidence on; see script_sourced_run_evidence.go.
	// The locator walk inside the helper always runs as a fail-closed corruption gate.
	secretValues, scriptSourcedValues, importedCredentials, err := runtimeSecretValuesForScript(cfg.Script, secretResolver, secretValues)
	if err != nil {
		return nil, fmt.Errorf("inspect retained import credentials: %s", scrubSensitiveText(err.Error()))
	}
	redactionConfig, scriptSourcedDropped, err := buildRuntimeSecretRedaction(nil, secretValues, scriptSourcedValues)
	if err != nil {
		return nil, fmt.Errorf("prepare runtime secret redaction: %s", scrubSensitiveText(err.Error()))
	}
	if scriptSourcedDropped {
		warnScriptSourcedRedactionDropped("script", cfg.Script.Name)
	}
	return &Engine{
		script:              cfg.Script,
		vusers:              cfg.VUsers,
		duration:            cfg.Duration,
		terminationMode:     cfg.TerminationMode,
		rampUp:              cfg.RampUp,
		metrics:             NewMetricsCollector(10000),
		data:                dataProvider,
		secrets:             secretResolver,
		redaction:           redactionConfig,
		importedCredentials: importedCredentials,
	}, nil
}

func (e *Engine) Run(ctx context.Context) (*RunResult, error) {
	// Re-check durable import gates at the actual execution boundary. Engine
	// construction and execution are intentionally separate, and callers retain
	// the Script pointer, so a draft can acquire a blocker after New returns.
	// No protocol lookup or runtime preflight may happen before this check.
	if err := importExecutionGateError(e.script, "launch"); err != nil {
		return nil, err
	}
	if err := validateMessageDetailRuntimeSupport(e.script); err != nil {
		return nil, err
	}

	var sourceErr error
	e.sourceParameters, sourceErr = newSourceParameterState([]*dsl.Script{e.script})
	if sourceErr != nil {
		return nil, sourceErr
	}
	var cancel context.CancelFunc
	if e.terminationMode == dsl.TerminationIterations {
		ctx, cancel = context.WithCancel(ctx)
	} else {
		ctx, cancel = context.WithTimeout(ctx, e.duration)
	}
	e.mu.Lock()
	e.cancel = cancel
	e.mu.Unlock()
	defer cancel()

	proto, err := protocol.Get(e.script.Protocol)
	if err != nil {
		return nil, fmt.Errorf("protocol init: %s", scrubSensitiveTextWithConfig(err.Error(), e.redaction))
	}
	if err := e.validateJavaVuserRuntime(ctx, proto); err != nil {
		return nil, fmt.Errorf("protocol runtime: %s", scrubSensitiveTextWithConfig(err.Error(), e.redaction))
	}
	javaCapacityPlan := NewJavaVuserCapacityPlan(e.script)
	var javaCapacityReservation *javaVuserCapacityReservation
	var javaLegacyReservation *javaVuserLegacyRunReservation
	if javaCapacityPlan.RequiresPersistentLifecycle() {
		javaCapacityReservation, err = reserveJavaVuserCapacity(javaCapacityPlan.Demand(e.vusers), e.javaVuserCapacityLimit)
		if err != nil {
			return nil, fmt.Errorf("protocol runtime: %s", scrubSensitiveTextWithConfig(err.Error(), e.redaction))
		}
	} else if javaVuserScriptUsesLegacyOneShot(e.script) {
		javaLegacyReservation, err = reserveLegacyJavaVuserRun()
		if err != nil {
			return nil, fmt.Errorf("protocol runtime: %s", scrubSensitiveTextWithConfig(err.Error(), e.redaction))
		}
	}
	defer javaCapacityReservation.Release()
	defer javaLegacyReservation.Release()

	protoConfig := buildProtoConfigForRunModeWithSecretResolver(e.script, "load", e.secrets)
	if _, ok := proto.(protocol.RuntimePreflightProvider); ok {
		targets, targetErr := runtimePreflightTargets(ctx, e.script, e.secrets)
		if targetErr != nil {
			return nil, fmt.Errorf("protocol runtime preflight: %s", scrubSensitiveTextWithConfig(targetErr.Error(), e.redaction))
		}
		preflight, _, preflightErr := protocol.RunRuntimePreflight(ctx, proto, protocol.RuntimePreflightRequest{
			Config:  protoConfig,
			Targets: targets,
		})
		if preflightErr != nil {
			return nil, fmt.Errorf("protocol runtime preflight: %s", scrubSensitiveTextWithConfig(preflightErr.Error(), e.redaction))
		}
		for _, warning := range preflight.Warnings {
			slog.Warn("protocol runtime preflight warning", "code", safeRuntimeWarningCode(warning))
		}
	}
	mockNamespace := newRuntimeMockNamespace()
	applyRuntimeMockNamespace(protoConfig, mockNamespace)
	releaseMockNamespace := protocol.AcquireRuntimeNamespace(mockNamespace)
	defer releaseMockNamespace()
	if !strings.EqualFold(strings.TrimSpace(e.script.Protocol), "java_vuser") {
		if err := callProtocolHealthCheck(ctx, proto, e.redaction); err != nil {
			slog.Warn("health check failed, proceeding anyway", "err", err)
		}
	} else if _, ok := proto.(protocol.RuntimeHealthChecker); !ok {
		// Defensive fallback for a future java_vuser implementation that exposes
		// static capabilities without the runtime health extension.
		if err := callProtocolHealthCheck(ctx, proto, e.redaction); err != nil {
			return nil, fmt.Errorf("protocol runtime health: %s", scrubSensitiveTextWithConfig(err.Error(), e.redaction))
		}
	}

	e.metrics.Start(time.Second)
	defer e.metrics.Stop()

	iterationRateLimiter := newIterationRateLimiter(e.script)
	if iterationRateLimiter != nil {
		e.metrics.SetTargetTPS(e.script.Config.Pacing.TPS)
		defer iterationRateLimiter.Stop()
	}
	if isOpenWorkload(e.script) {
		loopCtx, loopCancel := context.WithCancel(ctx)
		boundaryCtx, boundaryCancel := context.WithCancel(ctx)
		explicitGracefulStop := func() {
			boundaryCancel()
			loopCancel()
		}
		e.mu.Lock()
		e.softStop = explicitGracefulStop
		e.mu.Unlock()
		defer func() {
			loopCancel()
			boundaryCancel()
			e.mu.Lock()
			e.softStop = nil
			e.mu.Unlock()
		}()
		result, err := e.runOpenWorkload(loopCtx, ctx, boundaryCtx, protoConfig)
		e.metrics.Stop()
		if result != nil {
			if snap, ok := e.metrics.LatestSnapshot(); ok {
				result.FinalSnapshot = snap
			}
		}
		return result, err
	}

	// Execution goals cancel only loopCtx so VUsers stop claiming new
	// iterations; iterations already holding a reservation finish under ctx
	// (hard stop: duration timeout or Stop) and record their transactions.
	loopCtx, loopCancel := context.WithCancel(ctx)
	defer loopCancel()
	boundaryCtx, boundaryCancel := context.WithCancel(ctx)
	defer boundaryCancel()
	explicitGracefulStop := func() {
		boundaryCancel()
		loopCancel()
	}
	var executionGoals *ExecutionGoalLimiter
	if hasExecutionGoals(e.script.Config.RunLogic) {
		executionGoals = NewExecutionGoalLimiter(e.script.Config.RunLogic, loopCancel)
	}
	e.mu.Lock()
	e.softStop = explicitGracefulStop
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.softStop = nil
		e.mu.Unlock()
	}()

	start := time.Now()
	var wg sync.WaitGroup
	var activeVUsers atomic.Int64
	rendezvous := NewRendezvousCoordinator()

	rampDelay := e.rampDelay()
	var startupErr error

	for i := 0; i < e.vusers; i++ {
		if loopCtx.Err() != nil {
			break
		}

		if i > 0 && rampDelay > 0 && !sleepContext(loopCtx, rampDelay) {
			break
		}

		wg.Add(1)
		vuserID := i

		vuProto, err := protocol.Get(e.script.Protocol)
		if err != nil {
			safeErr := scrubSensitiveError(err, e.redaction)
			slog.Error("failed to create protocol for vuser", "vuser", vuserID, "err", safeErr)
			wg.Done()
			startupErr = safeErr
			break
		}
		bindProtocolRuntimeRedaction(vuProto, func() *RedactionConfig { return e.redaction })
		if err := callProtocolInit(loopCtx, vuProto, vuserProtoConfig(protoConfig, vuserID), e.redaction); err != nil {
			if loopCtx.Err() != nil {
				runProtocolTeardown(vuProto, func() *RedactionConfig { return e.redaction })
				wg.Done()
				break
			}
			safeErr := err
			slog.Error("failed to init protocol for vuser", "vuser", vuserID, "err", safeErr)
			runProtocolTeardown(vuProto, func() *RedactionConfig { return e.redaction })
			wg.Done()
			startupErr = safeErr
			break
		}
		// A context-ignoring adapter may return success after a peer has already
		// satisfied the exact goal. Do not launch this still-unentitled VUser.
		if loopCtx.Err() != nil {
			runProtocolTeardown(vuProto, func() *RedactionConfig { return e.redaction })
			wg.Done()
			break
		}

		e.metrics.SetActiveVUsers(activeVUsers.Add(1))

		go func() {
			var vu *VUser
			redaction := func() *RedactionConfig {
				if vu != nil {
					return vu.RedactionConfig
				}
				return e.redaction
			}
			defer recoverVUserGoroutine(vuserID, redaction)
			defer wg.Done()
			defer runProtocolTeardown(vuProto, redaction)
			defer func() { e.metrics.SetActiveVUsers(activeVUsers.Add(-1)) }()

			vu = &VUser{
				ID:                           vuserID,
				TotalVUsers:                  e.vusers,
				ParameterAllocation:          &ParameterAllocation{Population: e.vusers, Offset: vuserID, Iterations: e.script.Config.RunLogic.Iterations},
				Proto:                        vuProto,
				Script:                       e.script,
				Metrics:                      e.metrics,
				SecretResolver:               e.secrets,
				RedactionConfig:              e.redaction,
				importedCredentialInspection: e.importedCredentials,
				sourceParameters:             e.sourceParameters,
				IterationRateLimiter:         iterationRateLimiter,
				Rendezvous:                   rendezvous,
				ExecutionGoals:               executionGoals,
				HardStopCtx:                  ctx,
				ActionBoundaryCtx:            boundaryCtx,
				DataProvider:                 e.data,
			}
			vu.Run(loopCtx)
		}()
	}
	if startupErr != nil {
		cancel()
		wg.Wait()
		return nil, fmt.Errorf("initialize configured vuser load: %w", startupErr)
	}

	wg.Wait()
	elapsed := time.Since(start)
	e.metrics.Stop()

	result := &RunResult{
		Duration:          elapsed,
		TotalTransactions: e.metrics.TotalTransactions(),
		TotalErrors:       e.metrics.TotalErrors(),
	}
	if snap, ok := e.metrics.LatestSnapshot(); ok {
		result.FinalSnapshot = snap
	}
	if e.terminationMode == dsl.TerminationIterations {
		if got, expected := e.metrics.CompletedIterations(), int64(e.vusers)*int64(e.script.Config.RunLogic.Iterations); got != expected {
			return result, fmt.Errorf("iteration work incomplete: completed %d of %d iterations", got, expected)
		}
	}
	return result, nil
}

// validateJavaVuserRuntime keeps direct engine and CLI launches behind the same
// governed runtime boundary as controller and generator assignments. Combined
// lifecycle scripts pin one JVM per live VUser, so direct launches must reserve
// the advertised persistent-runner capacity before any VUser starts.
func (e *Engine) validateJavaVuserRuntime(ctx context.Context, instance protocol.Protocol) error {
	if e == nil || e.script == nil || !strings.EqualFold(strings.TrimSpace(e.script.Protocol), "java_vuser") {
		return nil
	}
	plan := NewJavaVuserCapacityPlan(e.script)
	capabilities, err := validatedJavaVuserRuntimeCapacityForPlan(ctx, instance, plan, plan.Demand(e.vusers))
	if err != nil {
		return err
	}
	e.javaVuserCapacityLimit = capabilities.MaxVUsers
	return nil
}

func javaVuserScriptUsesPhaseLifecycle(script *dsl.Script) bool {
	if script == nil {
		return false
	}
	return javaVuserActionsUsePhaseLifecycle(script.Init) ||
		javaVuserActionsUsePhaseLifecycle(script.Actions) ||
		javaVuserActionsUsePhaseLifecycle(script.Teardown)
}

func javaVuserScriptHasLegacyRunAction(script *dsl.Script) bool {
	if script == nil || !strings.EqualFold(strings.TrimSpace(script.Protocol), "java_vuser") {
		return false
	}
	return javaVuserActionsHaveLegacyRunAction(script.Init) ||
		javaVuserActionsHaveLegacyRunAction(script.Actions) ||
		javaVuserActionsHaveLegacyRunAction(script.Teardown)
}

func javaVuserActionsHaveLegacyRunAction(actions []dsl.Action) bool {
	for _, action := range actions {
		if strings.EqualFold(strings.TrimSpace(action.Type), "java_vuser_run") {
			method := ""
			if value, ok := action.Params["method"]; ok && value != nil {
				method = strings.TrimSpace(fmt.Sprint(value))
			}
			if method == "" || strings.EqualFold(method, "full_lifecycle") {
				return true
			}
		}
		if action.Control != nil {
			for _, branch := range action.Control.ActionBranches() {
				if javaVuserActionsHaveLegacyRunAction(branch.Actions) {
					return true
				}
			}
		}
	}
	return false
}

func javaVuserActionsUsePhaseLifecycle(actions []dsl.Action) bool {
	for _, action := range actions {
		if strings.EqualFold(strings.TrimSpace(action.Type), "java_vuser_run") {
			method := ""
			if value, ok := action.Params["method"]; ok && value != nil {
				method = strings.TrimSpace(fmt.Sprint(value))
			}
			if method != "" && !strings.EqualFold(method, "full_lifecycle") {
				return true
			}
		}
		if action.Control != nil {
			for _, branch := range action.Control.ActionBranches() {
				if javaVuserActionsUsePhaseLifecycle(branch.Actions) {
					return true
				}
			}
		}
	}
	return false
}

func (e *Engine) Stop() {
	e.mu.Lock()
	cancel := e.cancel
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (e *Engine) GracefulStop() {
	e.mu.Lock()
	cancel := e.softStop
	e.mu.Unlock()
	if cancel != nil {
		cancel()
		return
	}
	e.Stop()
}

func (e *Engine) LatestSnapshot() (MetricsSnapshot, bool) {
	return e.metrics.LatestSnapshot()
}

func (e *Engine) rampDelay() time.Duration {
	if e.rampUp <= 0 || e.vusers <= 1 {
		return 0
	}
	return e.rampUp / time.Duration(e.vusers)
}

func (e *Engine) runOpenWorkload(
	schedulerCtx context.Context,
	hardStopCtx context.Context,
	actionBoundaryCtx context.Context,
	protoConfig map[string]any,
) (*RunResult, error) {
	start := time.Now()
	iterationScript := cloneSingleIterationScript(e.script)
	arrivals, bounded := boundedArrivalLimit(e.script, e.vusers)
	loopCtx := schedulerCtx
	var executionGoals *ExecutionGoalLimiter
	if hasExecutionGoals(e.script.Config.RunLogic) {
		var goalCancel context.CancelFunc
		loopCtx, goalCancel = context.WithCancel(schedulerCtx)
		defer goalCancel()
		executionGoals = NewExecutionGoalLimiter(e.script.Config.RunLogic, goalCancel)
	}

	var wg sync.WaitGroup
	var activeWorkers atomic.Int64
	var initializedWorkers atomic.Int64
	var initErrMu sync.Mutex
	var firstInitErr error
	recordInitError := func(err error) {
		initErrMu.Lock()
		if firstInitErr == nil {
			firstInitErr = err
		}
		initErrMu.Unlock()
	}
	setActive := func(delta int64) {
		e.metrics.SetActiveVUsers(activeWorkers.Add(delta))
	}

	inFlightLimit := openWorkloadInFlightLimit(e.script)
	limiter := newInFlightLimiter(inFlightLimit)

	runArrivalScheduler(loopCtx, e.script.Config.Pacing.TPS, func() int {
		if bounded {
			return arrivals
		}
		return 0
	}(), nil, nil, func() time.Duration { return time.Since(start) }, func(arrival int) {
		// Shed the arrival when the in-flight set is saturated: the SUT cannot
		// keep up, so dropping it protects the generator from unbounded
		// goroutine growth instead of silently throttling to a closed model.
		if !limiter.tryAcquire() {
			e.metrics.RecordSheddedArrival()
			if limiter.shouldWarn() {
				slog.Warn("open workload shedding arrivals: system under test cannot keep up; capping in-flight workers to protect the generator", "max_in_flight", inFlightLimit, "tps", e.script.Config.Pacing.TPS)
			}
			return
		}
		wg.Add(1)
		setActive(1)

		go func(workerID int) {
			var vu *VUser
			redaction := func() *RedactionConfig {
				if vu != nil {
					return vu.RedactionConfig
				}
				return e.redaction
			}
			defer recoverVUserGoroutine(workerID, redaction)
			defer wg.Done()
			defer limiter.release()
			defer func() { setActive(-1) }()

			vuProto, err := protocol.Get(e.script.Protocol)
			if err != nil {
				safeErr := scrubSensitiveError(err, e.redaction)
				slog.Error("failed to create protocol for arrival worker", "arrival", workerID, "err", safeErr)
				recordInitError(safeErr)
				return
			}
			bindProtocolRuntimeRedaction(vuProto, func() *RedactionConfig { return e.redaction })
			if err := callProtocolInit(loopCtx, vuProto, vuserProtoConfig(protoConfig, workerID), e.redaction); err != nil {
				if loopCtx.Err() != nil {
					runProtocolTeardown(vuProto, redaction)
					return
				}
				safeErr := err
				slog.Error("failed to init protocol for arrival worker", "arrival", workerID, "err", safeErr)
				recordInitError(safeErr)
				runProtocolTeardown(vuProto, redaction)
				return
			}
			if loopCtx.Err() != nil {
				runProtocolTeardown(vuProto, redaction)
				return
			}
			defer runProtocolTeardown(vuProto, redaction)
			initializedWorkers.Add(1)

			vu = &VUser{
				ID:                           workerID,
				Proto:                        vuProto,
				Script:                       iterationScript,
				Metrics:                      e.metrics,
				SecretResolver:               e.secrets,
				RedactionConfig:              e.redaction,
				importedCredentialInspection: e.importedCredentials,
				sourceParameters:             e.sourceParameters,
				ExecutionGoals:               executionGoals,
				HardStopCtx:                  hardStopCtx,
				ActionBoundaryCtx:            actionBoundaryCtx,
				DataProvider:                 e.data,
			}
			vu.Run(loopCtx)
		}(arrival)
	}, func(_ int, count int) {
		limiter.recordShed(int64(count))
		e.metrics.RecordSheddedArrivals(int64(count))
		if limiter.shouldWarn() {
			slog.Warn("open workload shedding overdue arrivals: generator scheduler exceeded bounded catch-up burst", "shed", count, "max_catch_up_burst", maxOpenWorkloadCatchUpBurst, "tps", e.script.Config.Pacing.TPS)
		}
	})

	if shed := limiter.Shed(); shed > 0 {
		slog.Warn("open workload shed arrivals over the run (offered TPS exceeded achievable throughput)", "shed", shed, "max_in_flight", inFlightLimit)
	}

	wg.Wait()
	initErrMu.Lock()
	startupErr := firstInitErr
	initErrMu.Unlock()
	if initializedWorkers.Load() == 0 && startupErr != nil {
		return nil, fmt.Errorf("open workload could not initialize any arrival worker: %w", startupErr)
	}
	elapsed := time.Since(start)
	result := &RunResult{
		Duration:          elapsed,
		TotalTransactions: e.metrics.TotalTransactions(),
		TotalErrors:       e.metrics.TotalErrors(),
	}
	if snap, ok := e.metrics.LatestSnapshot(); ok {
		result.FinalSnapshot = snap
	}
	return result, nil
}

func newIterationRateLimiter(script *dsl.Script) *TokenBucket {
	if script == nil || dsl.CanonicalPacingType(script.Config.Pacing.Type) != "tps" || script.Config.Pacing.TPS <= 0 {
		return nil
	}
	return NewTokenBucket(script.Config.Pacing.TPS)
}
