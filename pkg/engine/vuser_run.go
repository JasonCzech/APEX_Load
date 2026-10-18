package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// vuserRun owns mutable phase state. The admitted script is immutable during a
// run; retaining its configuration and action references avoids keeping large
// copies in every parked goroutine. Execution frames return before think time.
type vuserRun struct {
	vu     *VUser
	cfg    *dsl.ScriptConfig
	logger *slog.Logger
	logCfg dsl.LogConfig

	ctx                  context.Context
	iterCtx              context.Context
	entitledBoundaryCtx  context.Context
	iterationBoundaryCtx context.Context
	genuineExit          bool
	teardownComplete     bool

	pacingCfg                   PacingRuntimeConfig
	actionTxMode                bool
	iterationTxMode             bool
	continueOnError             bool
	preReserveActionTransaction bool
	maxIter                     int
	iteration                   int
	previousIterStart           time.Time

	reservedIteration            bool
	reservedIterationTransaction bool
	reservedActionTransaction    bool
	iterationGoalEntitled        bool
	iterTxStart                  time.Time
	iterTxDuration               time.Duration
	iterTransactionSource        *protocol.Result
	iterTxRecorded               bool
	iterSuccess                  bool
	iterationExitRequested       bool
}

func (vu *VUser) Run(ctx context.Context) {
	ctx, cleanup, err := vu.bindSourceSamplerBoundary(ctx)
	if err != nil {
		vu.recordDataRowError(err)
		return
	}
	defer cleanup()
	run := &vuserRun{vu: vu, cfg: &vu.Script.Config, ctx: ctx, logCfg: vu.Script.Config.Log}
	run.logger = vu.makeLogger(run.logCfg)
	defer func() {
		if vu.errorLogs != nil {
			vu.errorLogs.close()
		}
	}()
	if !run.prepare() {
		return
	}
	defer vu.closeLiveTables()
	defer run.finalize()
	if !run.initialize() {
		return
	}
	run.configurePacing()
	defer run.releasePreIterationReservations()
	run.runIterations()
}

func (run *vuserRun) prepare() bool {
	run.vu.resultPhase = "action"
	run.vu.runCtx = run.ctx
	if run.vu.lifetime == 0 {
		run.vu.lifetime = newVUserLifetime()
	}

	// genuineExit records that the VUser ended on its own (its iterations
	// done, its data exhausted, a genuine failure with continue_on_error off)
	// rather than because a stop, ramp-down, scale-down or goal boundary ended
	// it. It is recorded where the loop ends, never inferred from contexts: a
	// goal limiter can refuse a slot without cancelling anything.
	if err := run.vu.prepareRuntimeState(); err != nil {
		run.vu.recordDataRowError(err)
		return false
	}
	// The soft Run context controls whether another iteration may start. The
	// hard context owns an action already in flight, while the boundary context
	// stops pause gates, waits, and subsequent actions on graceful stop.
	run.iterCtx = run.ctx
	if run.vu.HardStopCtx != nil {
		run.iterCtx = run.vu.HardStopCtx
	}
	run.vu.startLiveTableRenewal(run.iterCtx)
	run.entitledBoundaryCtx = run.iterCtx
	if run.vu.ActionBoundaryCtx != nil {
		run.entitledBoundaryCtx = run.vu.ActionBoundaryCtx
	}

	return true
}

func (run *vuserRun) initialize() bool {
	// Observers start no closer together than their session's start gap.
	if !run.vu.waitForSessionStart(run.ctx) {
		return false
	}

	// An observer whose session signs its browser in skips the recorded
	// sign-in.
	run.vu.recordedSignIn = newRecordedSignInPlan(run.vu.Script)
	if plan := run.vu.recordedSignIn; plan != nil {
		run.logger.Info("skipping the recorded Salesforce sign-in: the session strategy signs the browser in",
			"strategy", plan.strategy, "steps", plan.steps, "landing_page", plan.landing != "")
	}

	// --- Init phase ---
	if !run.vu.SkipInit {
		// Init has not reserved an iteration or transaction yet. Keep an action
		// already in flight on the hard-stop context, but make every lifecycle
		// boundary observe both the soft Run context (shared goals/scale-down)
		// and the explicit graceful-stop boundary.
		initBoundaryCtx, cancelInitBoundary := context.WithCancel(run.ctx)
		stopInitBoundaryLink := context.AfterFunc(run.entitledBoundaryCtx, cancelInitBoundary)
		ok := run.vu.runLifecycleActionsWithContexts(run.iterCtx, initBoundaryCtx, run.vu.Script.Init, "init", false, run.logger, run.logCfg, run.cfg.ErrorHandling)
		stopInitBoundaryLink()
		cancelInitBoundary()
		if !ok {
			// Init failed on its own unless a stop ended it.
			run.genuineExit = run.ctx.Err() == nil && run.iterCtx.Err() == nil && run.entitledBoundaryCtx.Err() == nil
			return false
		}
	}

	return true
}

func (run *vuserRun) configurePacing() {
	// --- Build pacing config ---
	if run.vu.Metrics != nil && run.vu.Metrics.initializationObserver != nil {
		run.vu.Metrics.initializationObserver(run.vu.ID)
	}
	run.pacingCfg = PacingRuntimeConfig{
		Type:          dsl.CanonicalPacingType(run.cfg.Pacing.Type),
		DelayMs:       run.cfg.Pacing.DelayMs,
		MinDelayMs:    run.cfg.Pacing.MinDelayMs,
		MaxDelayMs:    run.cfg.Pacing.MaxDelayMs,
		IntervalMs:    run.cfg.Pacing.IntervalMs,
		MinIntervalMs: run.cfg.Pacing.MinIntervalMs,
		MaxIntervalMs: run.cfg.Pacing.MaxIntervalMs,
		TPS:           run.cfg.Pacing.TPS,
		RateLimiter:   run.vu.IterationRateLimiter,
	}
	if activeControl, ok := run.vu.PauseControl.(activePauseController); ok {
		run.pacingCfg.activeSleep = activeControl.SleepActive
		run.pacingCfg.activeClock = activeControl.ActiveElapsed
	}

	autoTx := normalizeAutoTransactionMode(run.cfg.RunLogic.AutoTransactions)
	run.actionTxMode = autoTx == "per_action"
	run.iterationTxMode = autoTx == "per_iteration" || (autoTx == "none" && !scriptHasExplicitTransactions(run.vu.Script))
	run.maxIter = run.cfg.RunLogic.Iterations
	run.continueOnError = run.cfg.ErrorHandling.ContinueOnErrorEnabled()
	run.preReserveActionTransaction = run.vu.ExecutionGoals != nil &&
		run.vu.ExecutionGoals.hasTransactionLimit() &&
		!run.iterationTxMode &&
		((run.actionTxMode && actionsMayRecordActionTransaction(run.vu.Script.Actions)) ||
			actionsMayRecordSourceTransaction(run.vu.Script.Actions) ||
			actionsMayRecordHTTPSequenceTransaction(run.vu.Script.Actions) ||
			actionsMayEmitBusinessTransactions(run.vu.Script.Actions))
}

func (run *vuserRun) runIterations() {
	// --- Main iteration loop ---
	run.iteration = 0
	run.previousIterStart = time.Time{}
	run.iterationGoalEntitled = false
	for {
		// Until this iteration owns a shared goal reservation, peer goal
		// cancellation must stop pacing and prevent another action from starting.
		// Once a reservation is held, switch to the goal-immune boundary so the
		// entitled owner can drain its complete iteration.
		run.iterationBoundaryCtx = run.ctx
		run.iterationGoalEntitled = false
		sourceTransactionEntitled := run.vu.hasOpenSourceTransactionGoalOwnership()
		if sourceTransactionEntitled {
			run.iterationBoundaryCtx = run.entitledBoundaryCtx
		}
		// Check iteration limit
		if run.maxIter > 0 && run.iteration >= run.maxIter {
			run.genuineExit = true
			break
		}
		// Must precede the goal reservations below: once a reservation is
		// claimed, this iteration is entitled to run and record.
		if run.ctx.Err() != nil && !(run.iteration == 0 && sourceTransactionEntitled) {
			break
		}
		if run.vu.PauseControl != nil {
			if err := run.vu.PauseControl.WaitIfPaused(run.iterationBoundaryCtx); err != nil {
				break
			}
		}
		if run.vu.ExecutionGoals != nil && run.vu.ExecutionGoals.hasIterationLimit() {
			reserved := false
			if sourceTransactionEntitled {
				// A lifecycle source transaction can already own a pending
				// MaxTransactions slot. Waiting here for a pending iteration
				// slot would invert the ordering used by a peer that owns the
				// iteration and is waiting for this transaction slot. Fail this
				// incomplete source transaction immediately so its reservation
				// becomes definitive and the peer can release its iteration.
				reserved = run.vu.ExecutionGoals.TryReserveIteration()
				if !reserved {
					// The goal boundary, not the target, ends it.
					run.vu.abandoning = true
					run.vu.finalizeOpenSourceTransactions("source transaction could not acquire a global iteration slot")
					run.vu.abandoning = false
				}
			} else {
				reserved = run.vu.ExecutionGoals.WaitReserveIteration(run.iterationBoundaryCtx)
			}
			if !reserved {
				break
			}
			run.reservedIteration = true
		}
		if run.iterationTxMode && run.vu.ExecutionGoals != nil && run.vu.ExecutionGoals.hasTransactionLimit() {
			reserved := false
			if sourceTransactionEntitled {
				// A lifecycle source scope may already own the only pending
				// transaction slot. Waiting for the automatic per-iteration slot
				// would make this VUser wait on itself forever. Resolve the
				// incompatible overlap as an incomplete source transaction and
				// relinquish any earlier iteration reservation.
				reserved = run.vu.ExecutionGoals.TryReserveTransaction()
				if !reserved {
					run.vu.abandoning = true
					run.vu.finalizeOpenSourceTransactions("source transaction could not acquire a per-iteration transaction slot")
					run.vu.abandoning = false
				}
			} else {
				reserved = run.vu.ExecutionGoals.WaitReserveTransaction(run.iterationBoundaryCtx)
			}
			if !reserved {
				run.releasePreIterationReservations()
				break
			}
			run.reservedIterationTransaction = true
		}
		// TPS pacing also delays iteration zero. Reserve its first action/source
		// transaction before waiting for a token so goal exhaustion can wake all
		// other VUsers instead of leaving them blocked on future tokens.
		if (run.iteration > 0 || run.pacingCfg.Type == "tps") &&
			run.preReserveActionTransaction &&
			!sourceTransactionEntitled &&
			!run.reservedActionTransaction {
			if !run.vu.ExecutionGoals.WaitReserveTransaction(run.iterationBoundaryCtx) {
				run.releasePreIterationReservations()
				break
			}
			run.reservedActionTransaction = true
		}
		if run.reservedIteration || run.reservedIterationTransaction || run.reservedActionTransaction {
			run.iterationBoundaryCtx = run.entitledBoundaryCtx
		}
		// Pace only after the next finite iteration has successfully reserved its
		// shared goal. This avoids both an unnecessary final sleep and the race in
		// which several VUsers observed remaining capacity before one claimed it.
		if run.iteration > 0 {
			applyIterationPacingCtx(run.iterationBoundaryCtx, run.pacingCfg, run.previousIterStart)
			if run.iterationBoundaryCtx.Err() != nil {
				run.releasePreIterationReservations()
				break
			}
		}
		if run.pacingCfg.Type == "tps" && !run.pacingCfg.RateLimiter.WaitContext(run.iterationBoundaryCtx) {
			run.releasePreIterationReservations()
			break
		}
		if run.vu.PauseControl != nil {
			if err := run.vu.PauseControl.WaitIfPaused(run.iterationBoundaryCtx); err != nil {
				run.releasePreIterationReservations()
				break
			}
		}
		if !run.vu.ThroughputCap.Admit(run.iterationBoundaryCtx) {
			run.releasePreIterationReservations()
			break
		}
		if run.iterationBoundaryCtx.Err() != nil || run.iterCtx.Err() != nil {
			run.releasePreIterationReservations()
			break
		}

		iterStart := time.Now()
		run.previousIterStart = iterStart
		run.vu.currentIteration.Store(int64(run.iteration + 1))
		// A transaction still open from an earlier iteration is carried into
		// this one, so the iteration it is recorded in never goes backward.
		run.vu.carryOpenSourceTransactions(int64(run.iteration + 1))
		if run.pacingCfg.activeClock != nil {
			run.pacingCfg.iterStartActive = run.pacingCfg.activeClock()
		}

		// Reload data row at the start of each iteration
		if run.iteration > 0 {
			cleanupBindings := run.vu.currentDataBindings()
			if err := run.vu.loadDataRow(); err != nil {
				// Main execution stops; teardown still needs the last completed
				// iteration's row to clean up resources it created.
				run.vu.restoreDataBindings(cleanupBindings)
				run.vu.recordDataRowError(err)
				run.releasePreIterationReservations()
				// Its data ran out: the VUser ends on its own.
				run.genuineExit = true
				goto teardown
			}
			if run.vu.Parameters != nil {
				run.vu.Parameters.RefreshIteration()
			}
			_ = run.vu.refreshImportedCredentialRuntimeValues()
			// A long browser session hands over each iteration's interactions.
			handOffBrowserInteractions(run.vu.Proto, run.vu.ResultSink, run.vu.RedactionConfig)
			// Only a browser observer's lost salesforce_jwt session asks for renewal.
			renewSession := run.vu.takeBrowserSessionRenewal()
			if renewSession || run.vu.Script.Protocol == "browser" && run.cfg.Browser.NewUserPerIter != nil && *run.cfg.Browser.NewUserPerIter {
				if err := run.vu.resetProtocolForIteration(run.iterationBoundaryCtx); err != nil {
					run.logger.Error("iteration protocol reset failed", "iteration", run.iteration+1, "err", scrubSensitiveError(err, run.vu.RedactionConfig))
					goto teardown
				}
			}
			// A renewed session starts on a blank page: init opens the page
			// the iteration starts from again, its recorded sign-in skipped.
			if renewSession && !run.vu.SkipInit && len(run.vu.Script.Init) > 0 &&
				!run.vu.runLifecycleActionsWithContexts(run.iterCtx, run.iterationBoundaryCtx, run.vu.Script.Init, "init", false, run.logger, run.logCfg, run.cfg.ErrorHandling) {
				run.logger.Error("init after the session renewal failed", "iteration", run.iteration+1)
				goto teardown
			}
			newUser := browserEmulationNewUserEachIteration(run.cfg)
			if newUser || (run.vu.Script.Protocol == "http" && run.cfg.HTTP.ClearCookiesPerIter != nil && *run.cfg.HTTP.ClearCookiesPerIter) {
				if err := run.vu.clearProtocolCookies(run.iterationBoundaryCtx); err != nil {
					run.logger.Error("iteration cookie clear failed", "iteration", run.iteration+1, "err", scrubSensitiveError(err, run.vu.RedactionConfig))
					goto teardown
				}
			}
			if newUser && (run.cfg.BrowserEmulation.Cache.ClearCacheForNewUser == nil || *run.cfg.BrowserEmulation.Cache.ClearCacheForNewUser) {
				if err := run.vu.clearProtocolCache(run.iterationBoundaryCtx); err != nil {
					run.logger.Error("iteration cache clear failed", "iteration", run.iteration+1, "err", scrubSensitiveError(err, run.vu.RedactionConfig))
					goto teardown
				}
			}
		}
		if err := run.vu.beginLiveTableIteration(run.iterationBoundaryCtx); err != nil {
			run.releasePreIterationReservations()
			if errors.Is(err, errLiveTableSkipIteration) {
				if settleErr := run.vu.settleLiveTableLeases(run.iterCtx, false, false); settleErr != nil {
					run.vu.recordDataRowError(settleErr)
					goto teardown
				}
				// Bound polling even when the script has no pacing or duration.
				select {
				case <-run.iterationBoundaryCtx.Done():
					goto teardown
				case <-time.After(100 * time.Millisecond):
				}
				run.iteration++
				continue
			}
			run.vu.recordDataRowError(err)
			run.genuineExit = run.iterationBoundaryCtx.Err() == nil
			goto teardown
		}
		if controller, ok := run.vu.Proto.(protocol.IterationController); ok {
			if err := controller.BeginIteration(run.iterationBoundaryCtx); err != nil {
				run.logger.Error("iteration setup failed", "iteration", run.iteration+1, "err", scrubSensitiveError(err, run.vu.RedactionConfig))
				goto teardown
			}
		}
		run.vu.iterationTemperature = ""
		if reporter, ok := run.vu.Proto.(protocol.IterationTemperatureReporter); ok {
			run.vu.iterationTemperature = reporter.IterationTemperature()
		}
		// Protocol/session reset is iteration setup, not a started iteration or
		// transaction. Keep reservations pending until every fallible setup step
		// succeeds so another VUser can inherit their capacity after a failure.
		if run.iterationBoundaryCtx.Err() != nil || run.iterCtx.Err() != nil {
			run.releasePreIterationReservations()
			break
		}
		if run.reservedIteration {
			run.vu.ExecutionGoals.CommitIterationReservation()
			run.reservedIteration = false
			run.iterationGoalEntitled = true
		}
		if run.reservedIterationTransaction {
			run.vu.ExecutionGoals.CommitTransactionReservation()
			run.reservedIterationTransaction = false
			run.iterationGoalEntitled = true
		}
		// Crossing the fallible setup boundary starts a user iteration even when
		// its action list is empty. Teardown-only scripts depend on that lifecycle
		// contract, while VUsers denied before this point still skip teardown.
		run.vu.markUserWorkStarted()

		// Track iteration-level transaction
		run.iterTxStart = time.Now()
		run.iterTxDuration = time.Duration(0)
		run.vu.iterationThinkTime = 0
		run.iterTransactionSource = nil
		run.iterTxRecorded = false

		run.iterSuccess = true
		run.iterationExitRequested = false

		// Drive the top-level action list directly (rather than via runActions) so a
		// stray break/continue with no enclosing control_loop is a no-op that
		// proceeds to the next action — matching the replay path's runReplayPhase.
		order := run.vu.iterationOrder()
	actions:
		for position := 0; position < run.vu.iterationLength(order); position++ {
			action := run.vu.iterationActionRef(order, position)
			switch run.runAction(action, 0) {
			case flowTeardown:
				if run.iterationExitRequested {
					break actions
				}
				// Once the automatic per-iteration reservation is committed,
				// every post-setup boundary abort must emit the corresponding
				// failed transaction instead of silently consuming the goal.
				run.vu.abandoning = true
				run.recordIterationTransaction(false)
				run.vu.abandoning = false
				run.releaseReservedActionTransaction()
				goto teardown
			case flowBreak, flowContinue:
				warnStrayControlFlow(run.logger, action.Name, "actions")
			}
		}

		// LoadRunner ends an async conversation the script did not stop at
		// the end of the iteration.
		run.vu.stopAllAsyncConversations(run.iterCtx, "iteration end")

		if err := run.vu.settleLiveTableLeases(run.iterCtx, run.iterSuccess && run.iterCtx.Err() == nil && run.iterationBoundaryCtx.Err() == nil, false); err != nil {
			run.vu.recordDataRowError(err)
			run.recordIterationTransaction(false)
			run.releaseReservedActionTransaction()
			goto teardown
		}
		// Record iteration-level transaction
		run.recordIterationTransaction(run.iterSuccess)
		run.releaseReservedActionTransaction()

		run.iteration++
		if run.vu.Metrics != nil {
			run.vu.Metrics.RecordCompletedIteration(run.vu.Group)
		}

	}

teardown:
	run.vu.iterationTemperature = ""
	run.vu.stopAllAsyncConversations(run.iterCtx, "virtual user end")
	run.vu.currentIteration.Store(0)
	run.releasePreIterationReservations()
	run.runTeardown()
}

func (run *vuserRun) finalize() {
	// A background conversation must not outlive the virtual user, also
	// when init fails or a panic unwinds before the teardown label, or
	// when teardown itself started one.
	run.vu.stopAllAsyncConversations(run.iterCtx, "virtual user end")
	run.runTeardown()
	run.vu.stopAllAsyncConversations(run.iterCtx, "virtual user end")
	// A VUser that leaves because it was told to stop (the run, its
	// group, a scale-down or a goal boundary) leaves its open
	// transactions cut, not failed; one that ended on its own (its
	// iterations done, a genuine failure) ends their iteration.
	stopped := !run.genuineExit
	previousAbandoning, previousEnding := run.vu.abandoning, run.vu.endingIteration
	run.vu.abandoning, run.vu.endingIteration = previousAbandoning || stopped, !stopped
	run.vu.finalizeOpenSourceTransactions("source transaction ended without a matching end marker")
	run.vu.abandoning, run.vu.endingIteration = previousAbandoning, previousEnding
}

func (run *vuserRun) runTeardown() {
	if run.teardownComplete || run.vu.SkipTeardown || !run.vu.userWorkStarted {
		return
	}
	run.teardownComplete = true
	tctx, tcancel := teardownContext()
	if gate, present := protocol.RuntimePauseGateFromContext(run.iterCtx, run.vu.Script.Protocol); present {
		tctx = bindRuntimePauseTeardown(tctx, run.vu.Script.Protocol, gate)
	}
	pauseControl := run.vu.PauseControl
	// Cleanup runs on its own bounded wall-clock context. A load that is
	// paused while stopping must neither hold teardown at the pause gate nor
	// freeze teardown think time in the run's active-time clock.
	run.vu.PauseControl = nil
	run.vu.runLifecycleActions(tctx, run.vu.Script.Teardown, "teardown", true, run.logger, run.logCfg, run.cfg.ErrorHandling)
	run.vu.PauseControl = pauseControl
	tcancel()
}

func (run *vuserRun) releaseReservedIteration() {
	if !run.reservedIteration {
		return
	}
	run.vu.ExecutionGoals.ReleaseIterationReservation()
	run.reservedIteration = false
}

func (run *vuserRun) releaseReservedIterationTransaction() {
	if !run.reservedIterationTransaction {
		return
	}
	run.vu.ExecutionGoals.ReleaseTransactionReservation()
	run.reservedIterationTransaction = false
}

func (run *vuserRun) releaseReservedActionTransaction() {
	if !run.reservedActionTransaction {
		return
	}
	run.vu.ExecutionGoals.ReleaseTransactionReservation()
	run.reservedActionTransaction = false
}

func (run *vuserRun) releasePreIterationReservations() {
	run.releaseReservedActionTransaction()
	run.releaseReservedIterationTransaction()
	run.releaseReservedIteration()
}

func (run *vuserRun) recordIterationTransaction(success bool) {
	if !run.iterationTxMode || run.iterTxRecorded {
		return
	}
	run.vu.endingIteration = true
	defer func() { run.vu.endingIteration = false }()
	duration := run.iterTxDuration
	if duration <= 0 {
		duration = time.Since(run.iterTxStart) - run.vu.iterationThinkTime
		if duration < 0 {
			duration = 0
		}
	}
	var source *protocol.Result
	if !success {
		source = run.iterTransactionSource
	}
	run.vu.recordTransactionSpan(run.vu.iterationTransactionName(), duration, success, source, run.iterTxStart)
	run.iterTxRecorded = true
}

// failIteration ends the iteration on a genuine failure with
// continue_on_error off: the VUser ends on its own. A block's failure
// can also be a goal limit refusing one of its slots (a parallel
// branch, a WebSocket callback step), which the block cannot tell
// apart: once a shared goal is spent, the failure is taken for the
// goal boundary's (interrupted, the VUser stopped), never the
// target's.
func (run *vuserRun) failIteration() {
	if run.vu.ExecutionGoals.spent() {
		run.vu.abandoning = true
		run.recordIterationTransaction(false)
		run.vu.abandoning = false
		return
	}
	run.genuineExit = true
	run.recordIterationTransaction(false)
}

func (run *vuserRun) claimActionTransaction() bool {
	if run.vu.ExecutionGoals == nil {
		return true
	}
	if run.reservedActionTransaction {
		run.vu.ExecutionGoals.CommitTransactionReservation()
		run.reservedActionTransaction = false
		run.iterationBoundaryCtx = run.entitledBoundaryCtx
		return true
	}
	if !run.vu.startGoalTransaction(run.iterationBoundaryCtx) {
		return false
	}
	run.iterationBoundaryCtx = run.entitledBoundaryCtx
	return true
}

func (run *vuserRun) restoreIterationBoundary() {
	if run.iterationGoalEntitled ||
		run.reservedIteration ||
		run.reservedIterationTransaction ||
		run.reservedActionTransaction ||
		run.vu.hasOpenSourceTransactionGoalOwnership() {
		run.iterationBoundaryCtx = run.entitledBoundaryCtx
		return
	}
	run.iterationBoundaryCtx = run.ctx
}

func (run *vuserRun) runActions(actions []dsl.Action, depth int) actionFlow {
	for i := range actions {
		if flow := run.runAction(&actions[i], depth); flow != flowNext {
			return flow
		}
	}
	return flowNext
}

func (run *vuserRun) runAction(action *dsl.Action, depth int) actionFlow {
	if run.vu.PauseControl != nil {
		if err := run.vu.PauseControl.WaitIfPaused(run.iterationBoundaryCtx); err != nil {
			return flowTeardown
		}
	}
	if run.iterationBoundaryCtx.Err() != nil || run.iterCtx.Err() != nil {
		return flowTeardown
	}
	run.vu.markUserWorkStarted()
	var flow actionFlow
	var think bool
	switch strings.ToLower(strings.TrimSpace(action.Type)) {
	case "control_break", "control_continue", "control_if", "control_switch", "control_loop":
		flow, think = run.runControl(action, depth)
	default:
		flow, think = run.runLeaf(action, depth)
	}
	if think && !run.vu.applyActionThinkTimeConfig(run.iterationBoundaryCtx, action.ThinkTimeAfter, action.ThinkTime) {
		return flowTeardown
	}
	return flow
}

func (run *vuserRun) runControl(action *dsl.Action, depth int) (actionFlow, bool) {
	switch strings.ToLower(strings.TrimSpace(action.Type)) {
	case "control_break":
		return flowBreak, false
	case "control_continue":
		return flowContinue, false
	case "control_if":
		if action.Control == nil || depth >= maxControlDepth {
			if depth >= maxControlDepth {
				run.logger.Error("control flow nesting exceeded", "action", action.Name)
			}
			return flowNext, false
		}
		body := action.Control.Else
		if run.vu.evalCondition(action.Control.Condition) {
			body = action.Control.Steps
		}
		// A control_if propagates a break/continue from its body up to the
		// enclosing loop; a teardown aborts the iteration.
		switch flow := run.runActions(body, depth+1); flow {
		case flowTeardown:
			return flowTeardown, false
		case flowBreak, flowContinue:
			return flow, false
		}
		return flowNext, true
	case "control_switch":
		if action.Control == nil || depth >= maxControlDepth {
			if depth >= maxControlDepth {
				run.logger.Error("control flow nesting exceeded", "action", action.Name)
			}
			return flowNext, false
		}
		body, _ := run.vu.selectSwitchBranch(action.Control)
		switch flow := run.runActions(body, depth+1); flow {
		case flowTeardown:
			return flowTeardown, false
		case flowBreak, flowContinue:
			return flow, false
		}
		return flowNext, true
	case "control_loop":
		if action.Control == nil || depth >= maxControlDepth {
			if depth >= maxControlDepth {
				run.logger.Error("control flow nesting exceeded", "action", action.Name)
			}
			return flowNext, false
		}
		loop := action.Control
		cursor := newControlLoopCursor(run.vu, loop)
		if count := cursor.implicitTruncation(); count > 0 {
			warnLoopTruncation(run.logger, action.Name, count, cursor.maxPasses)
		}
	passes:
		for cursor.pending() {
			if run.iterationBoundaryCtx.Err() != nil || run.iterCtx.Err() != nil {
				cursor.restoreBindings()
				return flowTeardown, false
			}
			pass, ok := cursor.next()
			if !ok {
				if cursor.conditionCapReached {
					warnConditionLoopSafetyCap(run.logger, action.Name, cursor.maxPasses)
				}
				break
			}
			if pass.checkCondition && !run.vu.evalCondition(pass.condition) {
				break
			}
			switch run.runActions(loop.Steps, depth+1) {
			case flowTeardown:
				cursor.restoreBindings()
				return flowTeardown, false
			case flowBreak:
				break passes
			}
			// flowContinue / flowNext: advance to the next pass.
		}
		cursor.restoreBindings()
		return flowNext, true
	}
	return flowNext, false
}

func (run *vuserRun) runLeaf(actionRef *dsl.Action, depth int) (actionFlow, bool) {
	action := *actionRef
	if strings.EqualFold(strings.TrimSpace(action.Type), "control_parallel") {
		if action.Control == nil || depth >= maxControlDepth {
			run.iterSuccess = false
			if !run.continueOnError {
				// A genuine failure ends the iteration: record it as one,
				// never as a boundary abort.
				run.failIteration()
				return flowTeardown, false
			}
			return flowNext, false
		}
		if !run.vu.executeParallelBlock(run.iterCtx, action, "action") {
			run.iterSuccess = false
			if !run.continueOnError {
				run.failIteration()
				return flowTeardown, false
			}
		}
		return flowNext, true
	}

	sourceGoalClaimed := false
	if run.vu.ExecutionGoals != nil &&
		run.vu.ExecutionGoals.hasTransactionLimit() &&
		(run.vu.sourceTransactionStartWillOpen(action) ||
			run.vu.sourceTransactionEndNeedsGoalClaim(action) ||
			sourceTransactionRecordsImmediate(action)) {
		if !run.claimActionTransaction() {
			return flowTeardown, false
		}
		sourceGoalClaimed = true
	}
	if exitsIteration, failed := sourceMainIterationExit(action); exitsIteration {
		run.vu.markUserWorkStarted()
		handled, err := run.vu.handleEngineAction(run.iterCtx, action)
		if !handled {
			err = fmt.Errorf("lr_exit was not handled as an engine action")
		}
		if err != nil {
			run.logger.Error("action error", "action", action.Name, "err", scrubSensitiveError(err, run.vu.RedactionConfig))
			run.iterSuccess = false
		}
		if failed {
			run.iterSuccess = false
		}
		run.iterationExitRequested = true
		return flowTeardown, false
	}

	run.vu.markUserWorkStarted()
	sourceCompletionWillExecute := run.vu.sourceTransactionCompletionWillExecute(action)
	if handled, err := run.vu.handleEngineAction(run.iterCtx, action); handled {
		if dsl.CanonicalActionType(action.Type) == "error_message" && run.cfg.ErrorHandling.FailTransactionOnErrorMessage != nil && *run.cfg.ErrorHandling.FailTransactionOnErrorMessage {
			run.iterSuccess = false
		}
		if sourceGoalClaimed && sourceTransactionStarts(action) {
			run.vu.markSourceTransactionGoalClaimed(action)
		}
		if sourceCompletionWillExecute {
			run.restoreIterationBoundary()
		}
		if err != nil {
			if dsl.CanonicalActionType(action.Type) != "source_transaction" {
				run.vu.noteSourceTransactionResult(action, false)
			}
			run.logger.Error("action error", "action", action.Name, "err", scrubSensitiveError(err, run.vu.RedactionConfig))
			run.iterSuccess = false
			if !run.continueOnError {
				run.failIteration()
				return flowTeardown, false
			}
		}
		return flowNext, run.iterCtx.Err() == nil && run.iterationBoundaryCtx.Err() == nil
	}

	if strings.EqualFold(strings.TrimSpace(action.Type), "http_sequence") {
		markerOwned := run.vu.activeSourceTransactionOwnsHTTPSequence(action)
		if !markerOwned && !run.claimActionTransaction() {
			return flowTeardown, false
		}
		// A concurrent group whose every child names a transaction
		// records those, each timed by its request, in place of its
		// aggregate; the slot claimed above is its first one.
		requestTransactions := dsl.HTTPSequenceRequestTransactions(action) != nil
		aggregateTransaction := !markerOwned && !requestTransactions
		slotClaimed := !markerOwned
		claimRequestTransaction := func() bool {
			if slotClaimed {
				slotClaimed = false
				return true
			}
			return run.claimActionTransaction()
		}
		run.vu.markUserWorkStarted()
		result, err := run.vu.executeHTTPSequenceAction(run.iterCtx, action, run.cfg.ErrorHandling, claimRequestTransaction)
		if errors.Is(err, errTransactionGoalReached) {
			run.restoreIterationBoundary()
			return flowTeardown, false
		}
		if err != nil {
			run.vu.noteSourceTransactionResult(action, false)
			if aggregateTransaction {
				if result == nil {
					result = &protocol.Result{
						ActionName:   action.Name,
						Success:      false,
						ErrorMessage: scrubSensitiveTextWithConfig(err.Error(), run.vu.RedactionConfig),
					}
				} else {
					result.Success = false
					if result.ErrorMessage == "" {
						result.ErrorMessage = scrubSensitiveTextWithConfig(err.Error(), run.vu.RedactionConfig)
					}
				}
				run.vu.recordTransaction(action.Name, result.Duration, false, result)
			}
			if !markerOwned {
				run.restoreIterationBoundary()
			}
			run.logger.Error("action error", "action", action.Name, "err", scrubSensitiveError(err, run.vu.RedactionConfig))
			run.iterSuccess = false
			if !run.continueOnError {
				run.failIteration()
				return flowTeardown, false
			}
			return flowNext, false
		}
		run.iterTxDuration += result.Duration
		run.vu.noteSourceTransactionResult(action, result.Success)
		if aggregateTransaction {
			run.vu.recordTransaction(action.Name, result.Duration, result.Success, result)
		}
		if !markerOwned {
			run.restoreIterationBoundary()
		}
		if !result.Success {
			run.iterSuccess = false
			run.iterTransactionSource = result
			if run.logCfg.OnErrorOnly || run.logCfg.Level == "extended" {
				run.logger.Error("action failed", actionLogAttrs(action.Name, nil, result, run.logCfg, true, run.vu.RedactionConfig)...)
			}
			if !run.continueOnError {
				run.failIteration()
				return flowTeardown, false
			}
		} else if run.logCfg.Level == "extended" || run.logCfg.OnErrorOnly {
			run.logger.Info("action ok", actionLogAttrs(action.Name, nil, result, run.logCfg, false, run.vu.RedactionConfig)...)
		}
		return flowNext, true
	}

	if err := run.vu.beforeSourceSampler(run.iterationBoundaryCtx, action); err != nil {
		if !errors.Is(err, errSamplerScheduleComplete) && !errors.Is(err, context.Canceled) {
			run.logger.Error("sampler gate failed", "err", err)
		}
		return flowTeardown, false
	}
	if err := run.vu.beforeWebSocketAction(run.iterCtx, action.Type); err != nil {
		run.logger.Error("WebSocket callback failed", "err", scrubSensitiveError(err, run.vu.RedactionConfig))
		run.failIteration()
		return flowTeardown, false
	}
	leasedCtx, releasePause, pauseErr := run.vu.admitRuntimePauseAction(run.iterCtx)
	if pauseErr != nil {
		return flowTeardown, false
	}
	defer releasePause()
	resolved := run.vu.resolveAction(action)
	var actionTxStart time.Time
	if run.actionTxMode && !run.claimActionTransaction() {
		return flowTeardown, false
	}
	if run.actionTxMode {
		actionTxStart = time.Now()
	}
	knownBusinessTransactionEmitter := actionEmitsBusinessTransactions(action)
	if knownBusinessTransactionEmitter &&
		run.vu.ExecutionGoals != nil &&
		run.vu.ExecutionGoals.hasTransactionLimit() &&
		!run.reservedActionTransaction {
		if !run.vu.reserveGoalTransaction(run.iterationBoundaryCtx) {
			if run.actionTxMode {
				run.vu.recordAbandonedTransaction(action.Name, time.Since(actionTxStart), nil)
				run.restoreIterationBoundary()
			}
			return flowTeardown, false
		}
		run.reservedActionTransaction = true
		run.iterationBoundaryCtx = run.entitledBoundaryCtx
	}

	run.vu.markUserWorkStarted()
	result, err := run.vu.executeWithRetryStream(leasedCtx, resolved, run.cfg.ErrorHandling, func(child *protocol.Result) error {
		if child != nil && child.BusinessTransaction && !run.claimActionTransaction() {
			return errTransactionGoalReached
		}
		run.vu.recordResult(child, run.cfg.ErrorHandling, run.logCfg)
		if child != nil {
			run.vu.noteSourceTransactionResult(action, child.Success)
		}
		if child != nil && child.BusinessTransaction {
			run.restoreIterationBoundary()
		}
		return nil
	})
	releasePause()
	run.vu.acceptWebSocketCallback(action, result, err)
	if err != nil {
		if knownBusinessTransactionEmitter {
			run.releaseReservedActionTransaction()
			run.restoreIterationBoundary()
		}
		if errors.Is(err, errTransactionGoalReached) {
			if run.actionTxMode {
				// The automatic action slot was claimed before protocol
				// execution. Preserve one metric for that consumed slot even
				// when an additional streamed child is refused by the goal.
				run.vu.recordAbandonedTransaction(action.Name, time.Since(actionTxStart), nil)
				run.restoreIterationBoundary()
			}
			return flowTeardown, false
		}
		run.vu.noteSourceTransactionResult(action, false)
		run.logger.Error("action error", "action", action.Name, "err", scrubSensitiveError(err, run.vu.RedactionConfig))
		run.iterSuccess = false
		if run.actionTxMode {
			run.vu.recordTransaction(action.Name, time.Since(actionTxStart), false, nil)
			run.restoreIterationBoundary()
		}
		if !run.continueOnError {
			run.failIteration()
			return flowTeardown, false
		}
		return flowNext, run.iterCtx.Err() == nil && run.iterationBoundaryCtx.Err() == nil
	}

	if result != nil && result.BusinessTransaction && !run.claimActionTransaction() {
		if knownBusinessTransactionEmitter {
			run.releaseReservedActionTransaction()
			run.restoreIterationBoundary()
		}
		if run.actionTxMode {
			run.vu.recordAbandonedTransaction(action.Name, run.vu.sourceSampleDuration(actionTxStart, result), result)
			run.restoreIterationBoundary()
		}
		return flowTeardown, false
	}
	if knownBusinessTransactionEmitter {
		// A metadata-known emitter that returned no business transaction
		// must relinquish its pending slot for another VUser/action.
		run.releaseReservedActionTransaction()
		run.restoreIterationBoundary()
	}
	// An observer sent to sign in mid-iteration has lost its session.
	run.vu.markBrowserSessionLost(result)
	run.vu.recordResult(result, run.cfg.ErrorHandling, run.logCfg)
	if result != nil && result.BusinessTransaction {
		run.restoreIterationBoundary()
	}
	if result != nil {
		run.vu.noteSourceTransactionResult(action, result.Success)
	}
	run.iterTxDuration += result.Duration

	// Record auto-transaction for this action
	if run.actionTxMode {
		run.vu.recordTransaction(action.Name, run.vu.sourceSampleDuration(actionTxStart, result), result.Success, result)
		run.restoreIterationBoundary()
	}

	// A stateful adapter's authenticated iteration-exit receipt is a
	// control transfer, independent of ContinueOnError. Nested action
	// lists unwind through the existing iteration boundary; teardown
	// and subsequent main iterations retain their normal lifecycle.
	if result.LifecycleControl != nil {
		run.iterSuccess = run.iterSuccess && result.Success && !result.LifecycleControl.Failed
		if result.LifecycleControl.EndIteration {
			run.iterationExitRequested = true
			return flowTeardown, false
		}
	}

	if !result.Success {
		run.iterSuccess = false
		run.iterTransactionSource = result
		if run.logCfg.OnErrorOnly || run.logCfg.Level == "extended" {
			run.logger.Error("action failed", actionLogAttrs(action.Name, resolved, result, run.logCfg, true, run.vu.RedactionConfig)...)
		}
		if !run.continueOnError {
			run.failIteration()
			return flowTeardown, false
		}
	} else if run.logCfg.Level == "extended" || run.logCfg.OnErrorOnly {
		run.logger.Info("action ok", actionLogAttrs(action.Name, resolved, result, run.logCfg, false, run.vu.RedactionConfig)...)
	}

	return flowNext, true
}
