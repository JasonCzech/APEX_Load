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
// copies in every parked goroutine.
//
// A VUser spends most of its life parked in think time or pacing, and the GC
// halves a goroutine stack only while less than a quarter of it is in use, so
// every frame on the way to those waits stays small: the large ones (an
// action's execution, iteration setup) return before the VUser parks. A
// parked stack keeps two to four times the space its frames use, so a large
// frame on that path stops the halving early. Waits inside an action, or at
// its start, still park under its execution frames, for example think time
// between an http_sequence's requests, rendezvous, parallel branches,
// http_sync, WebSocket and async callback steps, the sampler schedule gate,
// and waits for an execution-goal slot; so do waits in iteration setup, such
// as an init re-run after a session renewal.
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
	// iterTransactionSource is dropped when the iteration ends, so a VUser
	// waiting for its next iteration does not keep its last failed response
	// alive.
	iterTransactionSource  *protocol.Result
	iterTxRecorded         bool
	iterSuccess            bool
	iterationExitRequested bool
}

func (vu *VUser) Run(ctx context.Context) {
	ctx, cleanup, err := vu.bindSourceSamplerBoundary(ctx)
	if err != nil {
		vu.recordDataRowError(err)
		if owner, ok := vu.PauseControl.(interface{ browserDocumentLoadLeaving(bool) }); ok {
			owner.browserDocumentLoadLeaving(true)
		}
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
		if owner, ok := vu.PauseControl.(interface{ browserDocumentLoadLeaving(bool) }); ok {
			owner.browserDocumentLoadLeaving(true)
		}
		return
	}
	defer vu.closeLiveTables()
	defer run.finalize()
	defer func() {
		if owner, ok := vu.PauseControl.(interface{ browserDocumentLoadLeaving(bool) }); ok {
			owner.browserDocumentLoadLeaving(run.genuineExit)
		}
	}()
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

// runIterations runs the main iteration loop, then teardown. Each pass
// goes through small methods so a VUser parked in pacing or think time holds
// none of the setup frames.
func (run *vuserRun) runIterations() {
	// --- Main iteration loop ---
	run.iteration = 0
	run.previousIterStart = time.Time{}
	run.iterationGoalEntitled = false
	for run.runIteration() {
	}
	run.endIterations()
}

// runIteration runs one pass of the main loop and reports whether the loop
// continues; false proceeds to teardown.
func (run *vuserRun) runIteration() bool {
	if !run.claimIteration() {
		return false
	}
	ready, skipped := run.prepareIteration()
	if skipped {
		run.iteration++
		return true
	}
	if !ready || !run.runIterationActions() {
		return false
	}
	return run.completeIteration()
}

// claimIteration reserves the next iteration's shared goals, then paces and
// admits it. It reports false when the loop ends.
func (run *vuserRun) claimIteration() bool {
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
		return false
	}
	// Must precede the goal reservations below: once a reservation is
	// claimed, this iteration is entitled to run and record.
	if run.ctx.Err() != nil && !(run.iteration == 0 && sourceTransactionEntitled) {
		return false
	}
	if run.vu.PauseControl != nil {
		if err := run.vu.PauseControl.WaitIfPaused(run.iterationBoundaryCtx); err != nil {
			return false
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
			return false
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
			return false
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
			return false
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
			return false
		}
	}
	if run.pacingCfg.Type == "tps" && !run.pacingCfg.RateLimiter.WaitContext(run.iterationBoundaryCtx) {
		run.releasePreIterationReservations()
		return false
	}
	if run.vu.PauseControl != nil {
		if err := run.vu.PauseControl.WaitIfPaused(run.iterationBoundaryCtx); err != nil {
			run.releasePreIterationReservations()
			return false
		}
	}
	if !run.vu.ThroughputCap.Admit(run.iterationBoundaryCtx) {
		run.releasePreIterationReservations()
		return false
	}
	if run.iterationBoundaryCtx.Err() != nil || run.iterCtx.Err() != nil {
		run.releasePreIterationReservations()
		return false
	}
	return true
}

// prepareIteration runs the fallible setup of an admitted iteration and then
// commits its reservations. It reports ready when the iteration's actions may
// run, skipped when the live tables skipped the iteration (it counts without
// running and the loop continues), and neither when the loop ends.
func (run *vuserRun) prepareIteration() (ready, skipped bool) {
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
			return false, false
		}
		if run.vu.Parameters != nil {
			run.vu.Parameters.RefreshIteration()
		}
		_ = run.vu.refreshImportedCredentialRuntimeValues()
		// A long browser session hands over each iteration's interactions.
		run.vu.flushBrowserIterationEvidence()
		// Only a browser observer's lost salesforce_jwt session asks for renewal.
		renewSession := run.vu.takeBrowserSessionRenewal()
		if renewSession || run.vu.Script.Protocol == "browser" && run.cfg.Browser.NewUserPerIter != nil && *run.cfg.Browser.NewUserPerIter {
			if err := run.vu.resetProtocolForIteration(run.iterationBoundaryCtx); err != nil {
				run.logger.Error("iteration protocol reset failed", "iteration", run.iteration+1, "err", scrubSensitiveError(err, run.vu.RedactionConfig))
				return false, false
			}
		}
		// A renewed session starts on a blank page: init opens the page
		// the iteration starts from again, its recorded sign-in skipped.
		if renewSession && !run.vu.SkipInit && len(run.vu.Script.Init) > 0 &&
			!run.vu.runLifecycleActionsWithContexts(run.iterCtx, run.iterationBoundaryCtx, run.vu.Script.Init, "init", false, run.logger, run.logCfg, run.cfg.ErrorHandling) {
			run.logger.Error("init after the session renewal failed", "iteration", run.iteration+1)
			return false, false
		}
		newUser := browserEmulationNewUserEachIteration(run.cfg)
		if newUser || (run.vu.Script.Protocol == "http" && run.cfg.HTTP.ClearCookiesPerIter != nil && *run.cfg.HTTP.ClearCookiesPerIter) {
			if err := run.vu.clearProtocolCookies(run.iterationBoundaryCtx); err != nil {
				run.logger.Error("iteration cookie clear failed", "iteration", run.iteration+1, "err", scrubSensitiveError(err, run.vu.RedactionConfig))
				return false, false
			}
		}
		if newUser && (run.cfg.BrowserEmulation.Cache.ClearCacheForNewUser == nil || *run.cfg.BrowserEmulation.Cache.ClearCacheForNewUser) {
			if err := run.vu.clearProtocolCache(run.iterationBoundaryCtx); err != nil {
				run.logger.Error("iteration cache clear failed", "iteration", run.iteration+1, "err", scrubSensitiveError(err, run.vu.RedactionConfig))
				return false, false
			}
		}
	}
	if err := run.vu.beginLiveTableIteration(run.iterationBoundaryCtx); err != nil {
		run.releasePreIterationReservations()
		if errors.Is(err, errLiveTableSkipIteration) {
			if settleErr := run.vu.settleLiveTableLeases(run.iterCtx, false, false); settleErr != nil {
				run.vu.recordDataRowError(settleErr)
				return false, false
			}
			// Bound polling even when the script has no pacing or duration.
			select {
			case <-run.iterationBoundaryCtx.Done():
				return false, false
			case <-time.After(100 * time.Millisecond):
			}
			return false, true
		}
		run.vu.recordDataRowError(err)
		run.genuineExit = run.iterationBoundaryCtx.Err() == nil
		return false, false
	}
	if controller, ok := run.vu.Proto.(protocol.IterationController); ok {
		if err := controller.BeginIteration(run.iterationBoundaryCtx); err != nil {
			run.logger.Error("iteration setup failed", "iteration", run.iteration+1, "err", scrubSensitiveError(err, run.vu.RedactionConfig))
			return false, false
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
		return false, false
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
	return true, false
}

// runIterationActions runs the iteration's action list and reports whether
// the iteration reached its end; false proceeds to teardown.
func (run *vuserRun) runIterationActions() bool {
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
	for position := 0; position < run.vu.iterationLength(order); position++ {
		action := run.vu.iterationActionRef(order, position)
		switch run.runAction(action, 0) {
		case flowTeardown:
			if run.iterationExitRequested {
				return true
			}
			// Once the automatic per-iteration reservation is committed,
			// every post-setup boundary abort must emit the corresponding
			// failed transaction instead of silently consuming the goal.
			run.vu.abandoning = true
			run.recordIterationTransaction(false)
			run.vu.abandoning = false
			run.releaseReservedActionTransaction()
			return false
		case flowBreak, flowContinue:
			warnStrayControlFlow(run.logger, action.Name, "actions")
		}
	}
	return true
}

// completeIteration settles and records an iteration that reached its end.
func (run *vuserRun) completeIteration() bool {
	// LoadRunner ends an async conversation the script did not stop at
	// the end of the iteration.
	run.vu.stopAllAsyncConversations(run.iterCtx, "iteration end")

	if err := run.vu.settleLiveTableLeases(run.iterCtx, run.iterSuccess && run.iterCtx.Err() == nil && run.iterationBoundaryCtx.Err() == nil, false); err != nil {
		run.vu.recordDataRowError(err)
		run.recordIterationTransaction(false)
		run.releaseReservedActionTransaction()
		return false
	}
	// Record iteration-level transaction
	run.recordIterationTransaction(run.iterSuccess)
	run.iterTransactionSource = nil
	run.releaseReservedActionTransaction()
	run.vu.flushBrowserIterationEvidence()

	run.iteration++
	if run.vu.Metrics != nil {
		run.vu.Metrics.RecordCompletedIteration(run.vu.Group)
	}
	return true
}

// endIterations ends the iteration loop and runs teardown.
func (run *vuserRun) endIterations() {
	run.iterTransactionSource = nil
	run.vu.iterationTemperature = ""
	run.vu.stopAllAsyncConversations(run.iterCtx, "virtual user end")
	run.vu.currentIteration.Store(0)
	run.releasePreIterationReservations()
	run.runTeardown()
}

func (run *vuserRun) finalize() {
	// A background conversation must not outlive the virtual user, also
	// when init fails or a panic unwinds before endIterations, or when
	// teardown itself started one.
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
	prevAuthoredAction := run.vu.currentAuthoredAction
	run.vu.currentAuthoredAction = nil
	run.vu.endingIteration = true
	defer func() {
		run.vu.endingIteration = false
		run.vu.currentAuthoredAction = prevAuthoredAction
	}()
	duration := run.iterTxDuration
	if duration <= 0 {
		duration = time.Since(run.iterTxStart) - run.vu.iterationThinkTime
		if duration < 0 {
			duration = 0
		}
	}
	var source *protocol.Result
	if !success && run.iterTransactionSource != nil {
		copied := *run.iterTransactionSource
		copied.AuthoredStep = nil
		source = &copied
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
	if run.stopped() {
		return flowTeardown
	}
	run.vu.markUserWorkStarted()
	var flow actionFlow
	var think bool
	switch strings.ToLower(strings.TrimSpace(action.Type)) {
	case "control_break":
		return flowBreak
	case "control_continue":
		return flowContinue
	case "control_if":
		flow, think = runIfBlock(run, action, depth)
	case "control_switch":
		flow, think = runSwitchBlock(run, action, depth)
	case "control_loop":
		flow, think = runLoopBlock(run, action, depth)
	default:
		flow, think = run.runLeaf(action, depth)
	}
	if think && !run.vu.applyActionThinkTimeConfig(run.iterationBoundaryCtx, action.ThinkTimeAfter, &action.ThinkTime) {
		return flowTeardown
	}
	return flow
}

func (run *vuserRun) stopped() bool {
	return run.iterationBoundaryCtx.Err() != nil || run.iterCtx.Err() != nil
}

func (run *vuserRun) vuser() *VUser     { return run.vu }
func (run *vuserRun) log() *slog.Logger { return run.logger }

// controlBlockRuns reports whether a control block can run at depth.
func (run *vuserRun) controlBlockRuns(action *dsl.Action, depth int) bool {
	if action.Control != nil && depth < maxControlDepth {
		return true
	}
	if depth >= maxControlDepth {
		run.logger.Error("control flow nesting exceeded", "action", action.Name)
	}
	return false
}

func (run *vuserRun) runLeaf(actionRef *dsl.Action, depth int) (actionFlow, bool) {
	prevAuthoredAction := run.vu.currentAuthoredAction
	run.vu.currentAuthoredAction = actionRef
	defer func() { run.vu.currentAuthoredAction = prevAuthoredAction }()
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
		if run.actionTxMode && !dsl.HTTPSequenceRecordsTransaction(action) {
			// per_action times each action; a transparent group's requests
			// were actions before the import grouped them, so each keeps its
			// own transaction.
			action = withPerRequestTransactions(action)
		}
		// A transparent group records no aggregate transaction, as one an
		// open source transaction owns records none of its own.
		markerOwned := run.vu.activeSourceTransactionOwnsHTTPSequence(action) || !dsl.HTTPSequenceRecordsTransaction(action)
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
	resolved := run.vu.resolveBrowserAuthoredAction(action, actionRef)
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
			run.vu.noteSourceTransactionResult(*actionRef, child.Success)
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
