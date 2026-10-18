package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/rendezvous"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/livetable"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type VUser struct {
	LiveTableClient   livetable.Client
	LiveTableClientID string
	LiveTableBindings []LiveTableBinding
	liveTables        *liveTableRuntime
	liveTableLane     *liveTableLane
	nativeServices    *nativeServices
	replayRuntime     *replayRuntimeState

	webSocketCallbackSteps []dsl.Action
	inWebSocketCallback    bool
	errorLogs              *errorLogBuffer
	ID                     int
	TotalVUsers            int
	ParameterAllocation    *ParameterAllocation
	Group                  string
	Region                 string
	RunID                  string
	Proto                  protocol.Protocol
	Script                 *dsl.Script
	Metrics                *MetricsCollector
	ResultSink             ResultSink
	Variables              map[string]string
	DataProvider           *DataProvider
	Parameters             *ParameterRuntime
	sourceParameters       *sourceParameterState
	sourceSamplers         *sourceSamplerRuntime
	SecretResolver         SecretResolver
	RedactionConfig        *RedactionConfig
	// sourceJSONDocuments holds lr_eval_json documents by lowercased JsonObject
	// name for the VUser's lifetime, as LoadRunner keeps them.
	sourceJSONDocuments map[string]any
	// runtimeRedaction publishes immutable redaction snapshots to protocol
	// callbacks, which may run on a driver/event goroutine while the VUser adds
	// newly extracted credentials on its execution goroutine.
	runtimeRedaction atomic.Pointer[RedactionConfig]
	// importedCredentialInspection is the result of the retained-credential
	// locator walk for this VUser's script, and the marker that the walk already
	// ran. Standard execution paths run the walk once for the whole run
	// (runtimeSecretValuesForScript) and assign the result here; a direct
	// test/library VUser leaves it nil and gets the same walk lazily, once.
	//
	// It is ONE field because a marker without a result is exactly the state that
	// silently disabled every dynamic script-sourced registration in production;
	// see importedCredentialInspection.
	importedCredentialInspection  *importedCredentialInspection
	runtimeCredentialRedactionErr error
	runtimeCredentialStateReady   bool
	runtimeCredentialBase         *RedactionConfig
	runtimeCredentialScopes       map[string][]string
	// scriptSourcedRedactionDropped records that a matcher bound forced the
	// best-effort script-sourced contribution to be abandoned for this VUser, so
	// the warning is surfaced once instead of on every rebuild.
	scriptSourcedRedactionDropped bool
	SnapshotStore                 *SnapshotStore
	PauseControl                  PauseController
	IterationRateLimiter          *TokenBucket
	ThroughputCap                 *GroupThroughputCap // spaces iteration starts to the group's TPS target
	Rendezvous                    *RendezvousCoordinator
	DistributedRendezvous         *rendezvous.Aggregator
	rendezvousRounds              map[string]uint64
	ExecutionGoals                *ExecutionGoalLimiter
	// HardStopCtx, when set, is cancelled only on hard stops (duration
	// timeout, explicit stop/abort) — not when ExecutionGoals exhausts a
	// shared goal. An iteration that already claimed a goal reservation runs
	// under this context so its transaction completes and is recorded instead
	// of being lost when another VUser trips the goal-exhaustion cancel.
	// When nil, the context passed to Run governs everything.
	HardStopCtx context.Context
	// ActionBoundaryCtx is cancelled by explicit graceful stop, per-VUser scale
	// down, and hard stop, but not by shared execution-goal exhaustion. The
	// current action continues under HardStopCtx, while pause gates, think time,
	// nested-control boundaries, and pacing use this context so graceful stop
	// proceeds directly to teardown before another action begins.
	ActionBoundaryCtx context.Context
	SkipInit          bool
	SkipTeardown      bool
	// rng, when set, makes random template builtins ({{fn.random}}, {{fake.*}})
	// deterministic. Set by the replay path; nil in load runs (time-seeded).
	rng *rand.Rand
	// currentIteration is the 1-based iteration being executed (0 during
	// init and teardown); results are stamped with it.
	currentIteration atomic.Int64
	// runCtx is the context Run was given; without a HardStopCtx its
	// cancellation interrupts the action in flight. abandoning is set while a
	// stop or goal boundary records the failed transaction it leaves behind.
	// A failure recorded under either is marked interrupted.
	runCtx     context.Context
	abandoning bool
	// lifetime identifies this VUser's lifetime (see protocol.Result).
	lifetime uint64
	// transactionSpanStart, while set, is the real start of the transaction
	// being recorded (see recordTransactionSpan).
	transactionSpanStart time.Time
	// unresolvedSecretRefs collects {{secret.*}} references that failed to resolve
	// during the most recent resolveAction; the execution path fails the action
	// closed rather than sending the literal placeholder. Per-VUser (single
	// goroutine), so no locking is needed.
	unresolvedSecretRefs []string
	// unresolvedFileRefs mirrors unresolvedSecretRefs for {{file.*}} asset
	// placeholders generated by importers.
	unresolvedFileRefs []string
	// unresolvedParameterRefs prevents malformed or legacy parameter bindings
	// from reaching a target as literal {{param.*}} placeholders.
	unresolvedParameterRefs []string
	// A managed run ID must be supplied by the scheduler. Never send a literal
	// {{run.id}} placeholder to an external target from a direct/library run.
	unresolvedRunRefs []string
	// bodyResolutionError prevents an opted-in structural body from falling back
	// to legacy interpolation after a malformed payload or a codec limit.
	bodyResolutionError                           error
	concurrentPreparation                         *httpPreparationBudget
	concurrentPublicationRedaction                *RedactionConfig
	concurrentPublicationUnavailable              bool
	concurrentGroupMetadata                       map[string]any
	concurrentGroupHierarchy, concurrentGroupPage string
	// The exact resolved action prevents reclassifying inserted literal body
	// data as newly authored parameter references after structural resolution.
	structuredBodyResolvedAction *protocol.Action
	// actionCredentialComposites carries the platform-managed credential
	// spellings for the protocol action produced by the most recent
	// resolveAction, for the execution path to register before that action runs.
	// Its contents are whatever appendPlatformManagedCompositeValues derived from
	// that action's surfaces: the "user:password" pair, its base64 and the
	// "Basic " header spelling — which the exact-value matcher cannot derive from
	// the password alone — and, when the password half resolves from the secret
	// store, the resolved bare password as well. The bare value is usually a
	// duplicate of what snapshotReferencedSecrets registered, and
	// filterRuntimeCredentialBaseValues drops it in that case. Per-VUser (single
	// goroutine), like unresolvedSecretRefs.
	actionCredentialComposites resolvedActionCredentialComposites
	// extractedVariables accounts only for values persisted from protocol
	// responses. It prevents data-driven extractor names from growing the VUser
	// variable map for the lifetime of a long run.
	extractedVariables     map[string]int
	extractedVariableBytes int
	// correlation is the run's controller-approved correlation behaviour.
	correlation vuserCorrelation
	// sourceTransactions tracks open LoadRunner transaction scopes separately
	// from Variables so action-local waits can be excluded from response time.
	sourceTransactions map[string]*sourceTransactionRuntimeState
	iterationThinkTime time.Duration
	userWorkStarted    bool
	resultPhase        string
	// endingIteration marks the transactions recorded because the iteration
	// ended on its own (Result.EndsIteration).
	endingIteration bool
	// iterationTemperature is the running iteration's browser temperature
	// (protocol.IterationTemperatureReporter), "" outside iterations.
	iterationTemperature string
	// browserIdentity is the observer's session identity, and
	// sessionStartNotBefore when it may start (browser_session.go).
	browserIdentity       *browserIdentityBinding
	sessionStartNotBefore time.Time
	// browserSessionRenewal asks for a new browser context at the next
	// iteration, for a lost salesforce_jwt session; recordedSignIn is how the
	// observer runs its script's recorded sign-in (recorded_sign_in.go).
	browserSessionRenewal bool
	recordedSignIn        *recordedSignInPlan
	resultSequence        uint64
	// async holds the running background HTTP conversations (http_async.go);
	// onAsyncResult lets replay report each conversation response.
	async           *asyncConversations
	inAsyncDispatch bool
	onAsyncResult   func(*protocol.Result)
}

// resolvedActionCredentialComposites pins credential spellings to the exact
// resolved action they were derived from. The action pointer is kept so an
// action can only ever be given its OWN composites; a mismatch registers nothing
// rather than carrying another action's credential into this action's scope.
type resolvedActionCredentialComposites struct {
	action *protocol.Action
	values []string
}

type PauseController interface {
	WaitIfPaused(ctx context.Context) error
}

type activePauseController interface {
	PauseController
	SleepActive(ctx context.Context, delay time.Duration) bool
	ActiveElapsed() time.Duration
}

var templatePattern = regexp.MustCompile(`\{\{([^{}]+)\}\}`)

var errTransactionGoalReached = errors.New("transaction execution goal reached")

func (vu *VUser) markUserWorkStarted() {
	if vu != nil {
		vu.userWorkStarted = true
	}
}

func (vu *VUser) reserveGoalTransaction(ctx context.Context) bool {
	if vu == nil || vu.ExecutionGoals == nil {
		return true
	}
	if vu.hasOpenSourceTransactionGoalOwnership() {
		return vu.ExecutionGoals.TryReserveTransaction()
	}
	return vu.ExecutionGoals.WaitReserveTransaction(ctx)
}

func (vu *VUser) startGoalTransaction(ctx context.Context) bool {
	if vu == nil || vu.ExecutionGoals == nil {
		return true
	}
	if vu.hasOpenSourceTransactionGoalOwnership() {
		return vu.ExecutionGoals.TryStartTransaction()
	}
	return vu.ExecutionGoals.WaitStartTransaction(ctx)
}

// SnapshotStore collects error snapshots for post-test analysis.
type SnapshotStore struct {
	snapshots        []*protocol.ErrorSnapshot
	maxSize          int
	maxSnapshotBytes int
	maxBytes         int
	storedBytes      int
}

func NewSnapshotStore(maxSize int) *SnapshotStore {
	return newSnapshotStoreWithLimits(maxSize, defaultErrorSnapshotMaxBytes, defaultSnapshotStoreMaxBytes)
}

func (ss *SnapshotStore) Add(snap *protocol.ErrorSnapshot) {
	if ss == nil || snap == nil {
		return
	}
	if len(ss.snapshots) >= ss.maxSize || ss.storedBytes >= ss.maxBytes {
		return
	}
	limit := min(ss.maxSnapshotBytes, ss.maxBytes-ss.storedBytes)
	bounded, retainedBytes := boundedErrorSnapshot(snap, limit)
	if bounded != nil {
		ss.snapshots = append(ss.snapshots, bounded)
		ss.storedBytes += retainedBytes
	}
}

func (ss *SnapshotStore) Snapshots() []*protocol.ErrorSnapshot {
	if ss == nil {
		return nil
	}
	return ss.snapshots
}

func (vu *VUser) Run(ctx context.Context) {
	var cleanup func()
	var bindErr error
	ctx, cleanup, bindErr = vu.bindSourceSamplerBoundary(ctx)
	if bindErr != nil {
		vu.recordDataRowError(bindErr)
		return
	}
	defer cleanup()
	newVUserRun(vu, ctx).run()
}

// vuserRun is the state of one VUser.Run, kept on the heap. A VUser spends
// most of its life parked in think time or pacing, and the GC halves a
// goroutine stack only while less than a quarter of it is in use, so every
// frame on the way to those waits stays small: the large ones (an action's
// execution, iteration setup) return before the VUser parks. One large frame
// on that path would hold every idle VUser's stack at the size its largest
// action grew it to.
type vuserRun struct {
	vu     *VUser
	cfg    *dsl.ScriptConfig
	logger *slog.Logger

	// ctx (the value passed to Run) stops the loop from starting new
	// iterations; it is cancelled when a shared execution goal exhausts (soft
	// stop) and on per-VUser scale/ramp-down. iterCtx governs the body of an
	// iteration once it holds a goal reservation: it is set to HardStopCtx so
	// a claimed reservation always completes and is recorded on a
	// goal-exhaustion soft stop, while still being interrupted on
	// scale/ramp-down and hard stops. In the MultiGroupEngine, HardStopCtx is
	// a per-VUser "shed" context that is a child of the engine base context
	// (so hard stops cancel it) and is cancelled explicitly on
	// scale/ramp-down — but NOT by goal exhaustion. entitledBoundaryCtx stops
	// pause gates, waits, and subsequent actions on graceful stop.
	ctx                 context.Context
	iterCtx             context.Context
	entitledBoundaryCtx context.Context
	// iterationBoundaryCtx is the action-boundary context in force. Once an
	// execution-goal reservation is claimed, action-boundary checks must use
	// the hard-stop context too. Using the soft loop context here lets
	// another VUser's failed reservation cancel a reserved iteration between
	// actions, producing fewer completed transactions than the global goal.
	iterationBoundaryCtx context.Context

	// genuineExit records that the VUser ended on its own (its iterations
	// done, its data exhausted, a genuine failure with continue_on_error off)
	// rather than because a stop, ramp-down, scale-down or goal boundary ended
	// it. It is recorded where the loop ends, never inferred from contexts: a
	// goal limiter can refuse a slot without cancelling anything.
	genuineExit      bool
	teardownComplete bool

	pacing                      PacingRuntimeConfig
	actionTxMode                bool
	iterationTxMode             bool
	continueOnError             bool
	preReserveActionTransaction bool
	maxIter                     int

	reservedIteration            bool
	reservedIterationTransaction bool
	reservedActionTransaction    bool

	iteration             int
	previousIterStart     time.Time
	iterationGoalEntitled bool

	// The running iteration's transaction and outcome.
	iterTxStart            time.Time
	iterTxDuration         time.Duration
	iterTransactionSource  *protocol.Result
	iterTxRecorded         bool
	iterSuccess            bool
	iterationExitRequested bool
}

func newVUserRun(vu *VUser, ctx context.Context) *vuserRun {
	// Nothing mutates a script's config during a run, so the run reads it in
	// place instead of copying it for every VUser.
	return &vuserRun{vu: vu, cfg: &vu.Script.Config, ctx: ctx}
}

func (r *vuserRun) run() {
	vu := r.vu
	r.logger = vu.makeLogger(r.cfg.Log)
	defer vu.closeErrorLogs()
	vu.resultPhase = "action"
	vu.runCtx = r.ctx
	if vu.lifetime == 0 {
		vu.lifetime = newVUserLifetime()
	}

	if err := vu.prepareRuntimeState(); err != nil {
		vu.recordDataRowError(err)
		return
	}
	defer vu.closeLiveTables()
	// The soft Run context controls whether another iteration may start. The
	// hard context owns an action already in flight, while the boundary context
	// stops pause gates, waits, and subsequent actions on graceful stop.
	r.iterCtx = r.ctx
	if vu.HardStopCtx != nil {
		r.iterCtx = vu.HardStopCtx
	}
	vu.startLiveTableRenewal(r.iterCtx)
	r.entitledBoundaryCtx = r.iterCtx
	if vu.ActionBoundaryCtx != nil {
		r.entitledBoundaryCtx = vu.ActionBoundaryCtx
	}
	// Init and action failures must still execute user-authored compensation
	// actions. The explicit teardown step calls the same once-only runTeardown
	// on the normal path; this defer covers every early return and panic
	// unwind.
	defer r.end()

	// Observers start no closer together than their session's start gap.
	if !vu.waitForSessionStart(r.ctx) {
		return
	}

	// An observer whose session signs its browser in skips the recorded
	// sign-in.
	vu.recordedSignIn = newRecordedSignInPlan(vu.Script)
	if plan := vu.recordedSignIn; plan != nil {
		r.logger.Info("skipping the recorded Salesforce sign-in: the session strategy signs the browser in",
			"strategy", plan.strategy, "steps", plan.steps, "landing_page", plan.landing != "")
	}

	if !vu.SkipInit && !r.runInit() {
		return
	}
	r.configureIterations()
	defer r.releasePreIterationReservations()
	for r.runIteration() {
	}
	r.endIterations()
}

// end runs when Run returns, also on an early return or a panic unwind.
func (r *vuserRun) end() {
	vu := r.vu
	// A background conversation must not outlive the virtual user, also
	// when init fails or a panic unwinds before the teardown step, or when
	// teardown itself started one.
	vu.stopAllAsyncConversations(r.iterCtx, "virtual user end")
	r.runTeardown()
	vu.stopAllAsyncConversations(r.iterCtx, "virtual user end")
	// A VUser that leaves because it was told to stop (the run, its
	// group, a scale-down or a goal boundary) leaves its open
	// transactions cut, not failed; one that ended on its own (its
	// iterations done, a genuine failure) ends their iteration.
	stopped := !r.genuineExit
	previousAbandoning, previousEnding := vu.abandoning, vu.endingIteration
	vu.abandoning, vu.endingIteration = previousAbandoning || stopped, !stopped
	vu.finalizeOpenSourceTransactions("source transaction ended without a matching end marker")
	vu.abandoning, vu.endingIteration = previousAbandoning, previousEnding
}

// runTeardown runs the script's teardown once, and only after user work
// started.
func (r *vuserRun) runTeardown() {
	vu := r.vu
	if r.teardownComplete || vu.SkipTeardown || !vu.userWorkStarted {
		return
	}
	r.teardownComplete = true
	tctx, tcancel := teardownContext()
	if gate, present := protocol.RuntimePauseGateFromContext(r.iterCtx, vu.Script.Protocol); present {
		tctx = bindRuntimePauseTeardown(tctx, vu.Script.Protocol, gate)
	}
	pauseControl := vu.PauseControl
	// Cleanup runs on its own bounded wall-clock context. A load that is
	// paused while stopping must neither hold teardown at the pause gate nor
	// freeze teardown think time in the run's active-time clock.
	vu.PauseControl = nil
	vu.runLifecycleActions(tctx, vu.Script.Teardown, "teardown", true, r.logger, r.cfg.Log, r.cfg.ErrorHandling)
	vu.PauseControl = pauseControl
	tcancel()
}

// runInit runs the script's init and reports whether iterations may follow.
func (r *vuserRun) runInit() bool {
	vu := r.vu
	// Init has not reserved an iteration or transaction yet. Keep an action
	// already in flight on the hard-stop context, but make every lifecycle
	// boundary observe both the soft Run context (shared goals/scale-down)
	// and the explicit graceful-stop boundary.
	initBoundaryCtx, cancelInitBoundary := context.WithCancel(r.ctx)
	stopInitBoundaryLink := context.AfterFunc(r.entitledBoundaryCtx, cancelInitBoundary)
	ok := vu.runLifecycleActionsWithContexts(r.iterCtx, initBoundaryCtx, vu.Script.Init, "init", false, r.logger, r.cfg.Log, r.cfg.ErrorHandling)
	stopInitBoundaryLink()
	cancelInitBoundary()
	if !ok {
		// Init failed on its own unless a stop ended it.
		r.genuineExit = r.ctx.Err() == nil && r.iterCtx.Err() == nil && r.entitledBoundaryCtx.Err() == nil
	}
	return ok
}

// configureIterations builds the pacing and transaction settings the
// iteration loop runs with.
func (r *vuserRun) configureIterations() {
	vu, cfg := r.vu, r.cfg
	if vu.Metrics != nil && vu.Metrics.initializationObserver != nil {
		vu.Metrics.initializationObserver(vu.ID)
	}
	r.pacing = PacingRuntimeConfig{
		Type:          dsl.CanonicalPacingType(cfg.Pacing.Type),
		DelayMs:       cfg.Pacing.DelayMs,
		MinDelayMs:    cfg.Pacing.MinDelayMs,
		MaxDelayMs:    cfg.Pacing.MaxDelayMs,
		IntervalMs:    cfg.Pacing.IntervalMs,
		MinIntervalMs: cfg.Pacing.MinIntervalMs,
		MaxIntervalMs: cfg.Pacing.MaxIntervalMs,
		TPS:           cfg.Pacing.TPS,
		RateLimiter:   vu.IterationRateLimiter,
	}
	if activeControl, ok := vu.PauseControl.(activePauseController); ok {
		r.pacing.activeSleep = activeControl.SleepActive
		r.pacing.activeClock = activeControl.ActiveElapsed
	}

	autoTx := normalizeAutoTransactionMode(cfg.RunLogic.AutoTransactions)
	r.actionTxMode = autoTx == "per_action"
	r.iterationTxMode = autoTx == "per_iteration" || (autoTx == "none" && !scriptHasExplicitTransactions(vu.Script))
	r.maxIter = cfg.RunLogic.Iterations
	r.continueOnError = cfg.ErrorHandling.ContinueOnErrorEnabled()
	r.preReserveActionTransaction = vu.ExecutionGoals != nil &&
		vu.ExecutionGoals.hasTransactionLimit() &&
		!r.iterationTxMode &&
		((r.actionTxMode && actionsMayRecordActionTransaction(vu.Script.Actions)) ||
			actionsMayRecordSourceTransaction(vu.Script.Actions) ||
			actionsMayRecordHTTPSequenceTransaction(vu.Script.Actions) ||
			actionsMayEmitBusinessTransactions(vu.Script.Actions))
	r.iterationBoundaryCtx = r.ctx
}

func (r *vuserRun) releaseReservedIteration() {
	if !r.reservedIteration {
		return
	}
	r.vu.ExecutionGoals.ReleaseIterationReservation()
	r.reservedIteration = false
}

func (r *vuserRun) releaseReservedIterationTransaction() {
	if !r.reservedIterationTransaction {
		return
	}
	r.vu.ExecutionGoals.ReleaseTransactionReservation()
	r.reservedIterationTransaction = false
}

func (r *vuserRun) releaseReservedActionTransaction() {
	if !r.reservedActionTransaction {
		return
	}
	r.vu.ExecutionGoals.ReleaseTransactionReservation()
	r.reservedActionTransaction = false
}

func (r *vuserRun) releasePreIterationReservations() {
	r.releaseReservedActionTransaction()
	r.releaseReservedIterationTransaction()
	r.releaseReservedIteration()
}

// runIteration runs one pass of the main loop and reports whether the loop
// continues; false proceeds to teardown.
func (r *vuserRun) runIteration() bool {
	if !r.claimIteration() {
		return false
	}
	ready, skipped := r.prepareIteration()
	if skipped {
		r.iteration++
		return true
	}
	if !ready || !r.runIterationActions() {
		return false
	}
	return r.completeIteration()
}

// claimIteration reserves the next iteration's shared goals, then paces and
// admits it. It reports false when the loop ends.
func (r *vuserRun) claimIteration() bool {
	vu := r.vu
	// Until this iteration owns a shared goal reservation, peer goal
	// cancellation must stop pacing and prevent another action from starting.
	// Once a reservation is held, switch to the goal-immune boundary so the
	// entitled owner can drain its complete iteration.
	r.iterationBoundaryCtx = r.ctx
	r.iterationGoalEntitled = false
	sourceTransactionEntitled := vu.hasOpenSourceTransactionGoalOwnership()
	if sourceTransactionEntitled {
		r.iterationBoundaryCtx = r.entitledBoundaryCtx
	}
	// Check iteration limit
	if r.maxIter > 0 && r.iteration >= r.maxIter {
		r.genuineExit = true
		return false
	}
	// Must precede the goal reservations below: once a reservation is
	// claimed, this iteration is entitled to run and record.
	if r.ctx.Err() != nil && !(r.iteration == 0 && sourceTransactionEntitled) {
		return false
	}
	if vu.PauseControl != nil {
		if err := vu.PauseControl.WaitIfPaused(r.iterationBoundaryCtx); err != nil {
			return false
		}
	}
	if vu.ExecutionGoals != nil && vu.ExecutionGoals.hasIterationLimit() {
		reserved := false
		if sourceTransactionEntitled {
			// A lifecycle source transaction can already own a pending
			// MaxTransactions slot. Waiting here for a pending iteration
			// slot would invert the ordering used by a peer that owns the
			// iteration and is waiting for this transaction slot. Fail this
			// incomplete source transaction immediately so its reservation
			// becomes definitive and the peer can release its iteration.
			reserved = vu.ExecutionGoals.TryReserveIteration()
			if !reserved {
				// The goal boundary, not the target, ends it.
				vu.abandoning = true
				vu.finalizeOpenSourceTransactions("source transaction could not acquire a global iteration slot")
				vu.abandoning = false
			}
		} else {
			reserved = vu.ExecutionGoals.WaitReserveIteration(r.iterationBoundaryCtx)
		}
		if !reserved {
			return false
		}
		r.reservedIteration = true
	}
	if r.iterationTxMode && vu.ExecutionGoals != nil && vu.ExecutionGoals.hasTransactionLimit() {
		reserved := false
		if sourceTransactionEntitled {
			// A lifecycle source scope may already own the only pending
			// transaction slot. Waiting for the automatic per-iteration slot
			// would make this VUser wait on itself forever. Resolve the
			// incompatible overlap as an incomplete source transaction and
			// relinquish any earlier iteration reservation.
			reserved = vu.ExecutionGoals.TryReserveTransaction()
			if !reserved {
				vu.abandoning = true
				vu.finalizeOpenSourceTransactions("source transaction could not acquire a per-iteration transaction slot")
				vu.abandoning = false
			}
		} else {
			reserved = vu.ExecutionGoals.WaitReserveTransaction(r.iterationBoundaryCtx)
		}
		if !reserved {
			r.releasePreIterationReservations()
			return false
		}
		r.reservedIterationTransaction = true
	}
	// TPS pacing also delays iteration zero. Reserve its first action/source
	// transaction before waiting for a token so goal exhaustion can wake all
	// other VUsers instead of leaving them blocked on future tokens.
	if (r.iteration > 0 || r.pacing.Type == "tps") &&
		r.preReserveActionTransaction &&
		!sourceTransactionEntitled &&
		!r.reservedActionTransaction {
		if !vu.ExecutionGoals.WaitReserveTransaction(r.iterationBoundaryCtx) {
			r.releasePreIterationReservations()
			return false
		}
		r.reservedActionTransaction = true
	}
	if r.reservedIteration || r.reservedIterationTransaction || r.reservedActionTransaction {
		r.iterationBoundaryCtx = r.entitledBoundaryCtx
	}
	// Pace only after the next finite iteration has successfully reserved its
	// shared goal. This avoids both an unnecessary final sleep and the race in
	// which several VUsers observed remaining capacity before one claimed it.
	if r.iteration > 0 {
		applyIterationPacingCtx(r.iterationBoundaryCtx, r.pacing, r.previousIterStart)
		if r.iterationBoundaryCtx.Err() != nil {
			r.releasePreIterationReservations()
			return false
		}
	}
	if r.pacing.Type == "tps" && !r.pacing.RateLimiter.WaitContext(r.iterationBoundaryCtx) {
		r.releasePreIterationReservations()
		return false
	}
	if vu.PauseControl != nil {
		if err := vu.PauseControl.WaitIfPaused(r.iterationBoundaryCtx); err != nil {
			r.releasePreIterationReservations()
			return false
		}
	}
	if !vu.ThroughputCap.Admit(r.iterationBoundaryCtx) {
		r.releasePreIterationReservations()
		return false
	}
	if r.iterationBoundaryCtx.Err() != nil || r.iterCtx.Err() != nil {
		r.releasePreIterationReservations()
		return false
	}
	return true
}

// prepareIteration runs the fallible setup of an admitted iteration and then
// commits its reservations. ready is false when the loop ends; skipped
// reports an iteration the live tables skipped, which counts without running.
func (r *vuserRun) prepareIteration() (ready, skipped bool) {
	vu, cfg, logger := r.vu, r.cfg, r.logger
	iteration := r.iteration
	iterStart := time.Now()
	r.previousIterStart = iterStart
	vu.currentIteration.Store(int64(iteration + 1))
	// A transaction still open from an earlier iteration is carried into
	// this one, so the iteration it is recorded in never goes backward.
	vu.carryOpenSourceTransactions(int64(iteration + 1))
	if r.pacing.activeClock != nil {
		r.pacing.iterStartActive = r.pacing.activeClock()
	}

	// Reload data row at the start of each iteration
	if iteration > 0 {
		cleanupBindings := vu.currentDataBindings()
		if err := vu.loadDataRow(); err != nil {
			// Main execution stops; teardown still needs the last completed
			// iteration's row to clean up resources it created.
			vu.restoreDataBindings(cleanupBindings)
			vu.recordDataRowError(err)
			r.releasePreIterationReservations()
			// Its data ran out: the VUser ends on its own.
			r.genuineExit = true
			return false, false
		}
		if vu.Parameters != nil {
			vu.Parameters.RefreshIteration()
		}
		_ = vu.refreshImportedCredentialRuntimeValues()
		// A long browser session hands over each iteration's interactions.
		handOffBrowserInteractions(vu.Proto, vu.ResultSink, vu.RedactionConfig)
		// Only a browser observer's lost salesforce_jwt session asks for renewal.
		renewSession := vu.takeBrowserSessionRenewal()
		if renewSession || vu.Script.Protocol == "browser" && cfg.Browser.NewUserPerIter != nil && *cfg.Browser.NewUserPerIter {
			if err := vu.resetProtocolForIteration(r.iterationBoundaryCtx); err != nil {
				logger.Error("iteration protocol reset failed", "iteration", iteration+1, "err", scrubSensitiveError(err, vu.RedactionConfig))
				return false, false
			}
		}
		// A renewed session starts on a blank page: init opens the page
		// the iteration starts from again, its recorded sign-in skipped.
		if renewSession && !vu.SkipInit && len(vu.Script.Init) > 0 &&
			!vu.runLifecycleActionsWithContexts(r.iterCtx, r.iterationBoundaryCtx, vu.Script.Init, "init", false, logger, cfg.Log, cfg.ErrorHandling) {
			logger.Error("init after the session renewal failed", "iteration", iteration+1)
			return false, false
		}
		newUser := browserEmulationNewUserEachIteration(cfg)
		if newUser || (vu.Script.Protocol == "http" && cfg.HTTP.ClearCookiesPerIter != nil && *cfg.HTTP.ClearCookiesPerIter) {
			if err := vu.clearProtocolCookies(r.iterationBoundaryCtx); err != nil {
				logger.Error("iteration cookie clear failed", "iteration", iteration+1, "err", scrubSensitiveError(err, vu.RedactionConfig))
				return false, false
			}
		}
		if newUser && (cfg.BrowserEmulation.Cache.ClearCacheForNewUser == nil || *cfg.BrowserEmulation.Cache.ClearCacheForNewUser) {
			if err := vu.clearProtocolCache(r.iterationBoundaryCtx); err != nil {
				logger.Error("iteration cache clear failed", "iteration", iteration+1, "err", scrubSensitiveError(err, vu.RedactionConfig))
				return false, false
			}
		}
	}
	if err := vu.beginLiveTableIteration(r.iterationBoundaryCtx); err != nil {
		r.releasePreIterationReservations()
		if errors.Is(err, errLiveTableSkipIteration) {
			if settleErr := vu.settleLiveTableLeases(r.iterCtx, false, false); settleErr != nil {
				vu.recordDataRowError(settleErr)
				return false, false
			}
			// Bound polling even when the script has no pacing or duration.
			select {
			case <-r.iterationBoundaryCtx.Done():
				return false, false
			case <-time.After(100 * time.Millisecond):
			}
			return false, true
		}
		vu.recordDataRowError(err)
		r.genuineExit = r.iterationBoundaryCtx.Err() == nil
		return false, false
	}
	if controller, ok := vu.Proto.(protocol.IterationController); ok {
		if err := controller.BeginIteration(r.iterationBoundaryCtx); err != nil {
			logger.Error("iteration setup failed", "iteration", iteration+1, "err", scrubSensitiveError(err, vu.RedactionConfig))
			return false, false
		}
	}
	vu.iterationTemperature = ""
	if reporter, ok := vu.Proto.(protocol.IterationTemperatureReporter); ok {
		vu.iterationTemperature = reporter.IterationTemperature()
	}
	// Protocol/session reset is iteration setup, not a started iteration or
	// transaction. Keep reservations pending until every fallible setup step
	// succeeds so another VUser can inherit their capacity after a failure.
	if r.iterationBoundaryCtx.Err() != nil || r.iterCtx.Err() != nil {
		r.releasePreIterationReservations()
		return false, false
	}
	if r.reservedIteration {
		vu.ExecutionGoals.CommitIterationReservation()
		r.reservedIteration = false
		r.iterationGoalEntitled = true
	}
	if r.reservedIterationTransaction {
		vu.ExecutionGoals.CommitTransactionReservation()
		r.reservedIterationTransaction = false
		r.iterationGoalEntitled = true
	}
	// Crossing the fallible setup boundary starts a user iteration even when
	// its action list is empty. Teardown-only scripts depend on that lifecycle
	// contract, while VUsers denied before this point still skip teardown.
	vu.markUserWorkStarted()
	return true, false
}

// runIterationActions runs the iteration's action list and reports whether
// the iteration reached its end; false proceeds to teardown.
func (r *vuserRun) runIterationActions() bool {
	vu := r.vu
	// Track iteration-level transaction
	r.iterTxStart = time.Now()
	r.iterTxDuration = 0
	vu.iterationThinkTime = 0
	r.iterTransactionSource = nil
	r.iterTxRecorded = false
	r.iterSuccess = true
	r.iterationExitRequested = false

	// Drive the top-level action list directly (rather than via runActions) so a
	// stray break/continue with no enclosing control_loop is a no-op that
	// proceeds to the next action — matching the replay path's runReplayPhase.
	order := vu.iterationOrder()
	for position := 0; position < vu.iterationLength(order); position++ {
		action := vu.iterationAction(order, position)
		switch r.runOne(action, 0) {
		case flowTeardown:
			if r.iterationExitRequested {
				return true
			}
			// Once the automatic per-iteration reservation is committed,
			// every post-setup boundary abort must emit the corresponding
			// failed transaction instead of silently consuming the goal.
			vu.abandoning = true
			r.recordIterationTransaction(false)
			vu.abandoning = false
			r.releaseReservedActionTransaction()
			return false
		case flowBreak, flowContinue:
			warnStrayControlFlow(r.logger, action.Name, "actions")
		}
	}
	return true
}

// completeIteration settles and records an iteration that reached its end.
func (r *vuserRun) completeIteration() bool {
	vu := r.vu
	// LoadRunner ends an async conversation the script did not stop at
	// the end of the iteration.
	vu.stopAllAsyncConversations(r.iterCtx, "iteration end")

	if err := vu.settleLiveTableLeases(r.iterCtx, r.iterSuccess && r.iterCtx.Err() == nil && r.iterationBoundaryCtx.Err() == nil, false); err != nil {
		vu.recordDataRowError(err)
		r.recordIterationTransaction(false)
		r.releaseReservedActionTransaction()
		return false
	}
	// Record iteration-level transaction
	r.recordIterationTransaction(r.iterSuccess)
	r.releaseReservedActionTransaction()

	r.iteration++
	if vu.Metrics != nil {
		vu.Metrics.RecordCompletedIteration(vu.Group)
	}
	return true
}

// endIterations is where the iteration loop ends, before teardown.
func (r *vuserRun) endIterations() {
	vu := r.vu
	vu.iterationTemperature = ""
	vu.stopAllAsyncConversations(r.iterCtx, "virtual user end")
	vu.currentIteration.Store(0)
	r.releasePreIterationReservations()
	r.runTeardown()
}

func (r *vuserRun) recordIterationTransaction(success bool) {
	if !r.iterationTxMode || r.iterTxRecorded {
		return
	}
	vu := r.vu
	vu.endingIteration = true
	defer func() { vu.endingIteration = false }()
	duration := r.iterTxDuration
	if duration <= 0 {
		duration = time.Since(r.iterTxStart) - vu.iterationThinkTime
		if duration < 0 {
			duration = 0
		}
	}
	var source *protocol.Result
	if !success {
		source = r.iterTransactionSource
	}
	vu.recordTransactionSpan(vu.iterationTransactionName(), duration, success, source, r.iterTxStart)
	r.iterTxRecorded = true
}

// failIteration ends the iteration on a genuine failure with
// continue_on_error off: the VUser ends on its own. A block's failure
// can also be a goal limit refusing one of its slots (a parallel
// branch, a WebSocket callback step), which the block cannot tell
// apart: once a shared goal is spent, the failure is taken for the
// goal boundary's (interrupted, the VUser stopped), never the
// target's.
func (r *vuserRun) failIteration() {
	vu := r.vu
	if vu.ExecutionGoals.spent() {
		vu.abandoning = true
		r.recordIterationTransaction(false)
		vu.abandoning = false
		return
	}
	r.genuineExit = true
	r.recordIterationTransaction(false)
}

func (r *vuserRun) claimActionTransaction() bool {
	vu := r.vu
	if vu.ExecutionGoals == nil {
		return true
	}
	if r.reservedActionTransaction {
		vu.ExecutionGoals.CommitTransactionReservation()
		r.reservedActionTransaction = false
		r.iterationBoundaryCtx = r.entitledBoundaryCtx
		return true
	}
	if !vu.startGoalTransaction(r.iterationBoundaryCtx) {
		return false
	}
	r.iterationBoundaryCtx = r.entitledBoundaryCtx
	return true
}

func (r *vuserRun) restoreIterationBoundary() {
	if r.iterationGoalEntitled ||
		r.reservedIteration ||
		r.reservedIterationTransaction ||
		r.reservedActionTransaction ||
		r.vu.hasOpenSourceTransactionGoalOwnership() {
		r.iterationBoundaryCtx = r.entitledBoundaryCtx
		return
	}
	r.iterationBoundaryCtx = r.ctx
}

// runActions drives a slice of actions for control_if / control_loop block
// bodies.
func (r *vuserRun) runActions(actions []dsl.Action, depth int) actionFlow {
	for i := range actions {
		// Any non-flowNext signal (teardown, break, continue) stops the
		// current slice and bubbles up; the enclosing control_loop
		// consumes break/continue, while teardown unwinds the iteration.
		if flow := r.runOne(&actions[i], depth); flow != flowNext {
			return flow
		}
	}
	return flowNext
}

// runOne executes a single action and reports whether the iteration should
// continue or abort to teardown. It applies the action's think time itself,
// once the frame that executed the action has returned.
func (r *vuserRun) runOne(action *dsl.Action, depth int) actionFlow {
	vu := r.vu
	if vu.PauseControl != nil {
		if err := vu.PauseControl.WaitIfPaused(r.iterationBoundaryCtx); err != nil {
			return flowTeardown
		}
	}
	if r.iterationBoundaryCtx.Err() != nil || r.iterCtx.Err() != nil {
		return flowTeardown
	}
	vu.markUserWorkStarted()

	var flow actionFlow
	var thinkAfter bool
	switch strings.ToLower(strings.TrimSpace(action.Type)) {
	case "control_break":
		return flowBreak
	case "control_continue":
		return flowContinue
	case "control_if":
		flow, thinkAfter = r.runIf(action, depth)
	case "control_switch":
		flow, thinkAfter = r.runSwitch(action, depth)
	case "control_parallel":
		flow, thinkAfter = r.runParallel(action, depth)
	case "control_loop":
		flow, thinkAfter = r.runLoop(action, depth)
	default:
		flow, thinkAfter = r.runLeaf(action)
	}
	if thinkAfter && !vu.applyActionThinkTime(r.iterationBoundaryCtx, action) {
		return flowTeardown
	}
	return flow
}

// controlBlockRuns reports whether a control block can run at depth.
func (r *vuserRun) controlBlockRuns(action *dsl.Action, depth int) bool {
	if action.Control != nil && depth < maxControlDepth {
		return true
	}
	if depth >= maxControlDepth {
		r.logger.Error("control flow nesting exceeded", "action", action.Name)
	}
	return false
}

func (r *vuserRun) runIf(action *dsl.Action, depth int) (actionFlow, bool) {
	if !r.controlBlockRuns(action, depth) {
		return flowNext, false
	}
	body := action.Control.Else
	if r.vu.evalCondition(action.Control.Condition) {
		body = action.Control.Steps
	}
	return r.runBranch(body, depth)
}

func (r *vuserRun) runSwitch(action *dsl.Action, depth int) (actionFlow, bool) {
	if !r.controlBlockRuns(action, depth) {
		return flowNext, false
	}
	body, _ := r.vu.selectSwitchBranch(action.Control)
	return r.runBranch(body, depth)
}

// runBranch runs the body a control_if or control_switch selected. It
// propagates a break/continue from its body up to the enclosing loop, and a
// teardown aborts the iteration; otherwise the block's think time follows.
func (r *vuserRun) runBranch(body []dsl.Action, depth int) (actionFlow, bool) {
	switch flow := r.runActions(body, depth+1); flow {
	case flowTeardown, flowBreak, flowContinue:
		return flow, false
	}
	return flowNext, true
}

func (r *vuserRun) runParallel(action *dsl.Action, depth int) (actionFlow, bool) {
	if action.Control == nil || depth >= maxControlDepth {
		r.iterSuccess = false
		if !r.continueOnError {
			// A genuine failure ends the iteration: record it as one,
			// never as a boundary abort.
			r.failIteration()
			return flowTeardown, false
		}
		return flowNext, false
	}
	if !r.vu.executeParallelBlock(r.iterCtx, action, "action") {
		r.iterSuccess = false
		if !r.continueOnError {
			r.failIteration()
			return flowTeardown, false
		}
	}
	return flowNext, true
}

func (r *vuserRun) runLoop(action *dsl.Action, depth int) (actionFlow, bool) {
	if !r.controlBlockRuns(action, depth) {
		return flowNext, false
	}
	loop := action.Control
	cursor := newControlLoopCursor(r.vu, loop)
	if count := cursor.implicitTruncation(); count > 0 {
		warnLoopTruncation(r.logger, action.Name, count, cursor.maxPasses)
	}
passes:
	for cursor.pending() {
		if r.iterationBoundaryCtx.Err() != nil || r.iterCtx.Err() != nil {
			cursor.restoreBindings()
			return flowTeardown, false
		}
		pass, ok := cursor.next()
		if !ok {
			if cursor.conditionCapReached {
				warnConditionLoopSafetyCap(r.logger, action.Name, cursor.maxPasses)
			}
			break
		}
		if pass.checkCondition && !r.vu.evalCondition(pass.condition) {
			break
		}
		switch r.runActions(loop.Steps, depth+1) {
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

// runLeaf executes an action that is not a control block. It reports whether
// the action's think time follows, which runOne applies after this large
// frame has returned.
func (r *vuserRun) runLeaf(step *dsl.Action) (actionFlow, bool) {
	vu, cfg, logger := r.vu, r.cfg, r.logger
	action := *step
	sourceGoalClaimed := false
	if vu.ExecutionGoals != nil &&
		vu.ExecutionGoals.hasTransactionLimit() &&
		(vu.sourceTransactionStartWillOpen(action) ||
			vu.sourceTransactionEndNeedsGoalClaim(action) ||
			sourceTransactionRecordsImmediate(action)) {
		if !r.claimActionTransaction() {
			return flowTeardown, false
		}
		sourceGoalClaimed = true
	}
	if exitsIteration, failed := sourceMainIterationExit(action); exitsIteration {
		vu.markUserWorkStarted()
		handled, err := vu.handleEngineAction(r.iterCtx, action)
		if !handled {
			err = fmt.Errorf("lr_exit was not handled as an engine action")
		}
		if err != nil {
			logger.Error("action error", "action", action.Name, "err", scrubSensitiveError(err, vu.RedactionConfig))
			r.iterSuccess = false
		}
		if failed {
			r.iterSuccess = false
		}
		r.iterationExitRequested = true
		return flowTeardown, false
	}

	vu.markUserWorkStarted()
	sourceCompletionWillExecute := vu.sourceTransactionCompletionWillExecute(action)
	if handled, err := vu.handleEngineAction(r.iterCtx, action); handled {
		if dsl.CanonicalActionType(action.Type) == "error_message" && cfg.ErrorHandling.FailTransactionOnErrorMessage != nil && *cfg.ErrorHandling.FailTransactionOnErrorMessage {
			r.iterSuccess = false
		}
		if sourceGoalClaimed && sourceTransactionStarts(action) {
			vu.markSourceTransactionGoalClaimed(action)
		}
		if sourceCompletionWillExecute {
			r.restoreIterationBoundary()
		}
		if err != nil {
			if dsl.CanonicalActionType(action.Type) != "source_transaction" {
				vu.noteSourceTransactionResult(action, false)
			}
			logger.Error("action error", "action", action.Name, "err", scrubSensitiveError(err, vu.RedactionConfig))
			r.iterSuccess = false
			if !r.continueOnError {
				r.failIteration()
				return flowTeardown, false
			}
		}
		return flowNext, r.iterCtx.Err() == nil && r.iterationBoundaryCtx.Err() == nil
	}

	if strings.EqualFold(strings.TrimSpace(action.Type), "http_sequence") {
		markerOwned := vu.activeSourceTransactionOwnsHTTPSequence(action)
		if !markerOwned && !r.claimActionTransaction() {
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
			return r.claimActionTransaction()
		}
		vu.markUserWorkStarted()
		result, err := vu.executeHTTPSequenceAction(r.iterCtx, action, cfg.ErrorHandling, claimRequestTransaction)
		if errors.Is(err, errTransactionGoalReached) {
			r.restoreIterationBoundary()
			return flowTeardown, false
		}
		if err != nil {
			vu.noteSourceTransactionResult(action, false)
			if aggregateTransaction {
				if result == nil {
					result = &protocol.Result{
						ActionName:   action.Name,
						Success:      false,
						ErrorMessage: scrubSensitiveTextWithConfig(err.Error(), vu.RedactionConfig),
					}
				} else {
					result.Success = false
					if result.ErrorMessage == "" {
						result.ErrorMessage = scrubSensitiveTextWithConfig(err.Error(), vu.RedactionConfig)
					}
				}
				vu.recordTransaction(action.Name, result.Duration, false, result)
			}
			if !markerOwned {
				r.restoreIterationBoundary()
			}
			logger.Error("action error", "action", action.Name, "err", scrubSensitiveError(err, vu.RedactionConfig))
			r.iterSuccess = false
			if !r.continueOnError {
				r.failIteration()
				return flowTeardown, false
			}
			return flowNext, false
		}
		r.iterTxDuration += result.Duration
		vu.noteSourceTransactionResult(action, result.Success)
		if aggregateTransaction {
			vu.recordTransaction(action.Name, result.Duration, result.Success, result)
		}
		if !markerOwned {
			r.restoreIterationBoundary()
		}
		if !result.Success {
			r.iterSuccess = false
			r.iterTransactionSource = result
			if cfg.Log.OnErrorOnly || cfg.Log.Level == "extended" {
				logger.Error("action failed", actionLogAttrs(action.Name, nil, result, cfg.Log, true, vu.RedactionConfig)...)
			}
			if !r.continueOnError {
				r.failIteration()
				return flowTeardown, false
			}
		} else if cfg.Log.Level == "extended" || cfg.Log.OnErrorOnly {
			logger.Info("action ok", actionLogAttrs(action.Name, nil, result, cfg.Log, false, vu.RedactionConfig)...)
		}
		return flowNext, true
	}

	if err := vu.beforeSourceSampler(r.iterationBoundaryCtx, action); err != nil {
		if !errors.Is(err, errSamplerScheduleComplete) && !errors.Is(err, context.Canceled) {
			logger.Error("sampler gate failed", "err", err)
		}
		return flowTeardown, false
	}
	if err := vu.beforeWebSocketAction(r.iterCtx, action.Type); err != nil {
		logger.Error("WebSocket callback failed", "err", scrubSensitiveError(err, vu.RedactionConfig))
		r.failIteration()
		return flowTeardown, false
	}
	leasedCtx, releasePause, pauseErr := vu.admitRuntimePauseAction(r.iterCtx)
	if pauseErr != nil {
		return flowTeardown, false
	}
	defer releasePause()
	resolved := vu.resolveAction(action)
	var actionTxStart time.Time
	if r.actionTxMode && !r.claimActionTransaction() {
		return flowTeardown, false
	}
	if r.actionTxMode {
		actionTxStart = time.Now()
	}
	knownBusinessTransactionEmitter := actionEmitsBusinessTransactions(action)
	if knownBusinessTransactionEmitter &&
		vu.ExecutionGoals != nil &&
		vu.ExecutionGoals.hasTransactionLimit() &&
		!r.reservedActionTransaction {
		if !vu.reserveGoalTransaction(r.iterationBoundaryCtx) {
			if r.actionTxMode {
				vu.recordAbandonedTransaction(action.Name, time.Since(actionTxStart), nil)
				r.restoreIterationBoundary()
			}
			return flowTeardown, false
		}
		r.reservedActionTransaction = true
		r.iterationBoundaryCtx = r.entitledBoundaryCtx
	}

	vu.markUserWorkStarted()
	result, err := vu.executeWithRetryStream(leasedCtx, resolved, cfg.ErrorHandling, func(child *protocol.Result) error {
		if child != nil && child.BusinessTransaction && !r.claimActionTransaction() {
			return errTransactionGoalReached
		}
		vu.recordResult(child, cfg.ErrorHandling, cfg.Log)
		if child != nil {
			vu.noteSourceTransactionResult(action, child.Success)
		}
		if child != nil && child.BusinessTransaction {
			r.restoreIterationBoundary()
		}
		return nil
	})
	releasePause()
	vu.acceptWebSocketCallback(action, result, err)
	if err != nil {
		if knownBusinessTransactionEmitter {
			r.releaseReservedActionTransaction()
			r.restoreIterationBoundary()
		}
		if errors.Is(err, errTransactionGoalReached) {
			if r.actionTxMode {
				// The automatic action slot was claimed before protocol
				// execution. Preserve one metric for that consumed slot even
				// when an additional streamed child is refused by the goal.
				vu.recordAbandonedTransaction(action.Name, time.Since(actionTxStart), nil)
				r.restoreIterationBoundary()
			}
			return flowTeardown, false
		}
		vu.noteSourceTransactionResult(action, false)
		logger.Error("action error", "action", action.Name, "err", scrubSensitiveError(err, vu.RedactionConfig))
		r.iterSuccess = false
		if r.actionTxMode {
			vu.recordTransaction(action.Name, time.Since(actionTxStart), false, nil)
			r.restoreIterationBoundary()
		}
		if !r.continueOnError {
			r.failIteration()
			return flowTeardown, false
		}
		return flowNext, r.iterCtx.Err() == nil && r.iterationBoundaryCtx.Err() == nil
	}

	if result != nil && result.BusinessTransaction && !r.claimActionTransaction() {
		if knownBusinessTransactionEmitter {
			r.releaseReservedActionTransaction()
			r.restoreIterationBoundary()
		}
		if r.actionTxMode {
			vu.recordAbandonedTransaction(action.Name, vu.sourceSampleDuration(actionTxStart, result), result)
			r.restoreIterationBoundary()
		}
		return flowTeardown, false
	}
	if knownBusinessTransactionEmitter {
		// A metadata-known emitter that returned no business transaction
		// must relinquish its pending slot for another VUser/action.
		r.releaseReservedActionTransaction()
		r.restoreIterationBoundary()
	}
	// An observer sent to sign in mid-iteration has lost its session.
	vu.markBrowserSessionLost(result)
	vu.recordResult(result, cfg.ErrorHandling, cfg.Log)
	if result != nil && result.BusinessTransaction {
		r.restoreIterationBoundary()
	}
	if result != nil {
		vu.noteSourceTransactionResult(action, result.Success)
	}
	r.iterTxDuration += result.Duration

	// Record auto-transaction for this action
	if r.actionTxMode {
		vu.recordTransaction(action.Name, vu.sourceSampleDuration(actionTxStart, result), result.Success, result)
		r.restoreIterationBoundary()
	}

	// A stateful adapter's authenticated iteration-exit receipt is a
	// control transfer, independent of ContinueOnError. Nested action
	// lists unwind through the existing iteration boundary; teardown
	// and subsequent main iterations retain their normal lifecycle.
	if result.LifecycleControl != nil {
		r.iterSuccess = r.iterSuccess && result.Success && !result.LifecycleControl.Failed
		if result.LifecycleControl.EndIteration {
			r.iterationExitRequested = true
			return flowTeardown, false
		}
	}

	if !result.Success {
		r.iterSuccess = false
		r.iterTransactionSource = result
		if cfg.Log.OnErrorOnly || cfg.Log.Level == "extended" {
			logger.Error("action failed", actionLogAttrs(action.Name, resolved, result, cfg.Log, true, vu.RedactionConfig)...)
		}
		if !r.continueOnError {
			r.failIteration()
			return flowTeardown, false
		}
	} else if cfg.Log.Level == "extended" || cfg.Log.OnErrorOnly {
		logger.Info("action ok", actionLogAttrs(action.Name, resolved, result, cfg.Log, false, vu.RedactionConfig)...)
	}
	return flowNext, true
}

func (vu *VUser) sleepActive(ctx context.Context, delay time.Duration) (time.Duration, bool) {
	if delay <= 0 {
		return 0, true
	}
	if activeControl, ok := vu.PauseControl.(activePauseController); ok {
		started := activeControl.ActiveElapsed()
		completed := activeControl.SleepActive(ctx, delay)
		elapsed := activeControl.ActiveElapsed() - started
		if elapsed < 0 {
			elapsed = 0
		}
		if elapsed > delay {
			elapsed = delay
		}
		return elapsed, completed
	}
	return sleepCtxElapsed(ctx, delay)
}

func (vu *VUser) RunLifecycleBlock(ctx context.Context, actions []dsl.Action, phase string) bool {
	return vu.RunLifecycleBlockWithContexts(ctx, ctx, actions, phase)
}

// RunLifecycleBlockWithContexts executes a lifecycle block with a hard
// in-flight action context and a separately cancellable action-boundary
// context. Init-at-once uses this split so graceful/shared-goal soft stops let
// the current protocol call finish but prevent later init actions.
func (vu *VUser) RunLifecycleBlockWithContexts(actionCtx, boundaryCtx context.Context, actions []dsl.Action, phase string) (ok bool) {
	if vu == nil || vu.Script == nil {
		return true
	}
	cfg := &vu.Script.Config
	logCfg := cfg.Log
	ignoreErrors := strings.EqualFold(strings.TrimSpace(phase), "teardown")
	defer func() {
		if ignoreErrors || !ok {
			vu.closeErrorLogs()
		}
	}()
	var cleanupBindings map[string]string
	if ignoreErrors {
		cleanupBindings = vu.currentDataBindings()
	}
	if err := vu.prepareRuntimeState(); err != nil {
		if !ignoreErrors {
			vu.recordDataRowError(err)
			return false
		}
		vu.restoreDataBindings(cleanupBindings)
	}
	vu.startLiveTableRenewal(actionCtx)
	defer vu.closeLiveTables()
	return vu.runLifecycleActionsWithContexts(actionCtx, boundaryCtx, actions, phase, ignoreErrors, vu.makeLogger(logCfg), logCfg, cfg.ErrorHandling)
}

func (vu *VUser) prepareRuntimeState() error {
	if err := vu.prepareLiveTables(); err != nil {
		return err
	}
	vu.ensureImportedCredentialInspection()
	vu.bindRuntimeTextRedactor()
	if err := vu.prepareNativeServices(); err != nil {
		return err
	}
	if vu.Variables == nil {
		vu.Variables = make(map[string]string)
	}
	if vu.ParameterAllocation != nil {
		var err error
		vu.Parameters, err = newAllocatedParameterRuntime(vu.Script.Parameters, vu.DataProvider, vu.ID, *vu.ParameterAllocation)
		if err != nil {
			return err
		}
	} else {
		vu.Parameters = NewParameterRuntime(vu.Script.Parameters, vu.DataProvider, vu.ID)
	}
	if vu.Parameters != nil {
		if vu.replayRuntime != nil {
			vu.Parameters.runtimeObservation = vu.replayRuntime
			vu.Parameters.runtimeObservations = make(map[string]parameterRuntimeObservation)
		}
		vu.Parameters.sourceState = vu.sourceParameters
		if vu.Script.Config.JMeter != nil {
			vu.Parameters.sourcePlan = vu.Script.Config.JMeter.SourcePlanID
		}
		vu.Parameters.Prime()
	}
	if err := vu.loadDataRow(); err != nil {
		return err
	}
	_ = vu.refreshImportedCredentialRuntimeValues()
	return nil
}

func (vu *VUser) runLifecycleActions(ctx context.Context, actions []dsl.Action, phase string, ignoreErrors bool, logger *slog.Logger, logCfg dsl.LogConfig, errCfg dsl.ErrorHandlingConfig) bool {
	return vu.runLifecycleActionsWithContexts(ctx, ctx, actions, phase, ignoreErrors, logger, logCfg, errCfg)
}

func (vu *VUser) runLifecycleActionsWithContexts(actionCtx, boundaryCtx context.Context, actions []dsl.Action, phase string, ignoreErrors bool, logger *slog.Logger, logCfg dsl.LogConfig, errCfg dsl.ErrorHandlingConfig) bool {
	previousPhase := vu.resultPhase
	vu.resultPhase = strings.ToLower(strings.TrimSpace(phase))
	defer func() { vu.resultPhase = previousPhase }()
	if ignoreErrors {
		errCfg.ContinueOnError = dsl.ContinueOnErrorValue(true)
	}
	softBoundaryCtx := boundaryCtx
	entitledBoundaryCtx := actionCtx
	// Teardown has a fresh bounded context specifically so cleanup can proceed
	// after graceful/scale/hard-stop boundaries have been canceled. Once a
	// teardown source transaction owns a goal reservation, keep its enclosed
	// actions on that teardown context just like at-once lifecycle teardown.
	if vu.ActionBoundaryCtx != nil && !strings.EqualFold(strings.TrimSpace(phase), "teardown") {
		entitledBoundaryCtx = vu.ActionBoundaryCtx
	}
	restoreLifecycleBoundary := func() {
		if vu.hasOpenSourceTransactionGoalOwnership() {
			boundaryCtx = entitledBoundaryCtx
			return
		}
		boundaryCtx = softBoundaryCtx
	}
	claimLifecycleTransaction := func() bool {
		return vu.startGoalTransaction(boundaryCtx)
	}
	deniedSourceScopes := make([]string, 0)
	sourceScopeKey := func(action dsl.Action) string {
		return strings.ToLower(strings.TrimSpace(sourceTransactionActionName(action)))
	}
	// Lifecycle (init/teardown) actions use the same actionFlow model as the main
	// path so control_break / control_continue inside a control_loop are honored
	// here too (and not silently dropped); flowTeardown means "abort the phase".
	var runActions func(actions []dsl.Action, depth int) actionFlow
	runOne := func(action dsl.Action, depth int) actionFlow {
		if vu.PauseControl != nil {
			if err := vu.PauseControl.WaitIfPaused(boundaryCtx); err != nil {
				return flowTeardown
			}
		}
		if boundaryCtx.Err() != nil || actionCtx.Err() != nil {
			return flowTeardown
		}
		switch strings.ToLower(strings.TrimSpace(action.Type)) {
		case "control_break":
			return flowBreak
		case "control_continue":
			return flowContinue
		case "control_if":
			vu.markUserWorkStarted()
			if action.Control == nil {
				return flowNext
			}
			if depth >= maxControlDepth {
				logger.Error(phase+" control flow nesting exceeded", "action", action.Name)
				return flowNext
			}
			body := action.Control.Else
			if vu.evalCondition(action.Control.Condition) {
				body = action.Control.Steps
			}
			switch flow := runActions(body, depth+1); flow {
			case flowTeardown:
				return flowTeardown
			case flowBreak, flowContinue:
				return flow
			}
			if !vu.applyActionThinkTime(boundaryCtx, &action) {
				return flowTeardown
			}
			return flowNext
		case "control_switch":
			vu.markUserWorkStarted()
			if action.Control == nil {
				return flowNext
			}
			if depth >= maxControlDepth {
				logger.Error(phase+" control flow nesting exceeded", "action", action.Name)
				return flowNext
			}
			body, _ := vu.selectSwitchBranch(action.Control)
			switch flow := runActions(body, depth+1); flow {
			case flowTeardown:
				return flowTeardown
			case flowBreak, flowContinue:
				return flow
			}
			if !vu.applyActionThinkTime(boundaryCtx, &action) {
				return flowTeardown
			}
			return flowNext
		case "control_parallel":
			vu.markUserWorkStarted()
			if action.Control == nil || depth >= maxControlDepth {
				if ignoreErrors {
					return flowNext
				}
				return flowTeardown
			}
			if !vu.executeParallelBlock(actionCtx, &action, phase) && !ignoreErrors {
				return flowTeardown
			}
			if !vu.applyActionThinkTime(boundaryCtx, &action) {
				return flowTeardown
			}
			return flowNext
		case "control_loop":
			vu.markUserWorkStarted()
			if action.Control == nil {
				return flowNext
			}
			if depth >= maxControlDepth {
				logger.Error(phase+" control flow nesting exceeded", "action", action.Name)
				return flowNext
			}
			loop := action.Control
			cursor := newControlLoopCursor(vu, loop)
			if count := cursor.implicitTruncation(); count > 0 {
				warnLoopTruncation(logger, action.Name, count, cursor.maxPasses)
			}
		passes:
			for cursor.pending() {
				if boundaryCtx.Err() != nil || actionCtx.Err() != nil {
					cursor.restoreBindings()
					return flowTeardown
				}
				pass, ok := cursor.next()
				if !ok {
					if cursor.conditionCapReached {
						warnConditionLoopSafetyCap(logger, action.Name, cursor.maxPasses)
					}
					break
				}
				if pass.checkCondition && !vu.evalCondition(pass.condition) {
					break
				}
				switch runActions(loop.Steps, depth+1) {
				case flowTeardown:
					cursor.restoreBindings()
					return flowTeardown
				case flowBreak:
					break passes
				}
				// flowContinue / flowNext: advance to the next pass.
			}
			cursor.restoreBindings()
			if !vu.applyActionThinkTime(boundaryCtx, &action) {
				return flowTeardown
			}
			return flowNext
		}
		// Best-effort teardown must not execute a denied source scope's enclosed
		// SUT calls without a transaction slot. This check follows control
		// containers so a matching end in the selected branch/loop is still
		// observed; once it closes the scope, subsequent cleanup can resume.
		if len(deniedSourceScopes) > 0 {
			switch {
			case sourceTransactionStarts(action):
				deniedSourceScopes = append(deniedSourceScopes, sourceScopeKey(action))
			case sourceTransactionEnds(action):
				key := sourceScopeKey(action)
				for index := len(deniedSourceScopes) - 1; index >= 0; index-- {
					if deniedSourceScopes[index] == key {
						deniedSourceScopes = append(deniedSourceScopes[:index], deniedSourceScopes[index+1:]...)
						break
					}
				}
			}
			return flowNext
		}
		sourceStartReserved := false
		if vu.sourceTransactionStartWillOpen(action) {
			if vu.ExecutionGoals != nil && vu.ExecutionGoals.hasTransactionLimit() {
				if !vu.reserveGoalTransaction(boundaryCtx) {
					if ignoreErrors {
						deniedSourceScopes = append(deniedSourceScopes, sourceScopeKey(action))
						return flowNext
					}
					return flowTeardown
				}
				sourceStartReserved = true
				boundaryCtx = entitledBoundaryCtx
			}
		}
		if (vu.sourceTransactionEndNeedsGoalClaim(action) || sourceTransactionRecordsImmediate(action)) &&
			!claimLifecycleTransaction() {
			if ignoreErrors {
				return flowNext
			}
			return flowTeardown
		}
		sourceCompletionWillExecute := vu.sourceTransactionCompletionWillExecute(action)
		if handled, err := vu.handleEngineAction(actionCtx, action); handled {
			vu.markUserWorkStarted()
			if sourceStartReserved {
				vu.markSourceTransactionGoalReserved(action)
			}
			if sourceCompletionWillExecute {
				restoreLifecycleBoundary()
			}
			if err != nil {
				if dsl.CanonicalActionType(action.Type) != "source_transaction" {
					vu.noteSourceTransactionResult(action, false)
				}
				logger.Error(phase+" action failed", "action", action.Name, "err", scrubSensitiveError(err, vu.RedactionConfig))
				if !ignoreErrors {
					return flowTeardown
				}
			}
			if actionCtx.Err() == nil && boundaryCtx.Err() == nil {
				if !vu.applyActionThinkTime(boundaryCtx, &action) {
					return flowTeardown
				}
			}
			return flowNext
		}
		if strings.EqualFold(strings.TrimSpace(action.Type), "http_sequence") {
			markerOwned := vu.activeSourceTransactionOwnsHTTPSequence(action)
			if !markerOwned && !claimLifecycleTransaction() {
				if ignoreErrors {
					return flowNext
				}
				return flowTeardown
			}
			requestTransactions := dsl.HTTPSequenceRequestTransactions(action) != nil
			aggregateTransaction := !markerOwned && !requestTransactions
			slotClaimed := !markerOwned
			claimRequestTransaction := func() bool {
				if slotClaimed {
					slotClaimed = false
					return true
				}
				return claimLifecycleTransaction()
			}
			vu.markUserWorkStarted()
			result, err := vu.executeHTTPSequenceAction(actionCtx, action, errCfg, claimRequestTransaction)
			if errors.Is(err, errTransactionGoalReached) {
				if ignoreErrors {
					return flowNext
				}
				return flowTeardown
			}
			if err != nil {
				vu.noteSourceTransactionResult(action, false)
				if aggregateTransaction {
					if result == nil {
						result = &protocol.Result{
							ActionName:   action.Name,
							Success:      false,
							ErrorMessage: scrubSensitiveTextWithConfig(err.Error(), vu.RedactionConfig),
						}
					} else {
						result.Success = false
						if result.ErrorMessage == "" {
							result.ErrorMessage = scrubSensitiveTextWithConfig(err.Error(), vu.RedactionConfig)
						}
					}
					vu.recordTransaction(action.Name, result.Duration, false, result)
				}
				logger.Error(phase+" action failed", "action", action.Name, "err", scrubSensitiveError(err, vu.RedactionConfig))
				if !ignoreErrors {
					return flowTeardown
				}
				return flowNext
			}
			if result != nil {
				vu.noteSourceTransactionResult(action, result.Success)
				if aggregateTransaction {
					vu.recordTransaction(action.Name, result.Duration, result.Success, result)
				}
				if !result.Success && !ignoreErrors {
					return flowTeardown
				}
			}
			if !vu.applyActionThinkTime(boundaryCtx, &action) {
				return flowTeardown
			}
			return flowNext
		}
		if err := vu.beforeSourceSampler(boundaryCtx, action); err != nil {
			if !errors.Is(err, errSamplerScheduleComplete) && !errors.Is(err, context.Canceled) {
				logger.Error("sampler gate failed", "err", err)
			}
			return flowTeardown
		}
		if err := vu.beforeWebSocketAction(actionCtx, action.Type); err != nil {
			logger.Error("WebSocket callback failed", "err", scrubSensitiveError(err, vu.RedactionConfig))
			return flowTeardown
		}

		leasedCtx, releasePause, pauseErr := vu.admitRuntimePauseAction(actionCtx)
		if pauseErr != nil {
			return flowTeardown
		}
		defer releasePause()
		resolved := vu.resolveAction(action)
		knownBusinessTransactionEmitter := actionEmitsBusinessTransactions(action)
		emitterTransactionReserved := false
		if knownBusinessTransactionEmitter &&
			vu.ExecutionGoals != nil &&
			vu.ExecutionGoals.hasTransactionLimit() {
			if !vu.reserveGoalTransaction(boundaryCtx) {
				if ignoreErrors {
					return flowNext
				}
				return flowTeardown
			}
			emitterTransactionReserved = true
			boundaryCtx = entitledBoundaryCtx
		}
		claimEmittedTransaction := func() bool {
			if emitterTransactionReserved {
				vu.ExecutionGoals.CommitTransactionReservation()
				emitterTransactionReserved = false
				return true
			}
			return claimLifecycleTransaction()
		}
		releaseEmitterReservation := func() {
			if emitterTransactionReserved {
				vu.ExecutionGoals.ReleaseTransactionReservation()
				emitterTransactionReserved = false
			}
			if !vu.hasOpenSourceTransactionGoalOwnership() {
				boundaryCtx = softBoundaryCtx
			}
		}
		vu.markUserWorkStarted()
		result, err := vu.executeWithRetryStream(leasedCtx, resolved, errCfg, func(child *protocol.Result) error {
			if child != nil && child.BusinessTransaction && !claimEmittedTransaction() {
				return errTransactionGoalReached
			}
			vu.recordResult(child, errCfg, logCfg)
			if child != nil {
				vu.noteSourceTransactionResult(action, child.Success)
			}
			return nil
		})
		releasePause()
		vu.acceptWebSocketCallback(action, result, err)
		if err != nil {
			releaseEmitterReservation()
			if errors.Is(err, errTransactionGoalReached) {
				if ignoreErrors {
					return flowNext
				}
				return flowTeardown
			}
			vu.noteSourceTransactionResult(action, false)
			logger.Error(phase+" action failed", "action", action.Name, "err", scrubSensitiveError(err, vu.RedactionConfig))
			if !ignoreErrors {
				return flowTeardown
			}
			if actionCtx.Err() == nil && boundaryCtx.Err() == nil {
				if !vu.applyActionThinkTime(boundaryCtx, &action) {
					return flowTeardown
				}
			}
			return flowNext
		}
		if result != nil && result.BusinessTransaction && !claimEmittedTransaction() {
			releaseEmitterReservation()
			if ignoreErrors {
				return flowNext
			}
			return flowTeardown
		}
		releaseEmitterReservation()
		vu.recordResult(result, errCfg, logCfg)
		if result != nil {
			vu.noteSourceTransactionResult(action, result.Success)
		}
		if result != nil && result.Success && (logCfg.OnErrorOnly || logCfg.Level == "extended") {
			logger.Info(phase+" action ok", actionLogAttrs(action.Name, resolved, result, logCfg, false, vu.RedactionConfig)...)
		}
		if result != nil && !result.Success {
			if logCfg.OnErrorOnly || logCfg.Level == "extended" {
				logger.Error(phase+" action unsuccessful", "action", action.Name, "status", result.StatusCode, "error", result.ErrorMessage)
			} else {
				logger.Warn(phase+" action unsuccessful", "action", action.Name, "error", result.ErrorMessage)
			}
			if !ignoreErrors {
				return flowTeardown
			}
		}
		if !vu.applyActionThinkTime(boundaryCtx, &action) {
			return flowTeardown
		}
		return flowNext
	}
	var signIn *recordedSignInPlan
	if strings.EqualFold(strings.TrimSpace(phase), "init") {
		signIn = vu.recordedSignIn
	}
	runActions = func(actions []dsl.Action, depth int) actionFlow {
		for index, action := range actions {
			if depth == 0 && signIn != nil {
				if signIn.skips(index) {
					continue
				}
				action = signIn.action(action)
			}
			switch f := runOne(action, depth); f {
			case flowTeardown:
				return flowTeardown
			case flowBreak, flowContinue:
				if depth == 0 {
					// A stray break/continue with no enclosing loop is a no-op that
					// proceeds to the next action (matching the main + replay paths).
					warnStrayControlFlow(logger, action.Name, phase)
					continue
				}
				return f // bubble up to the nearest enclosing control_loop
			}
		}
		return flowNext
	}
	return runActions(actions, 0) != flowTeardown
}

func (vu *VUser) applyActionThinkTime(ctx context.Context, action *dsl.Action) bool {
	if vu == nil || vu.Script == nil || !action.ThinkTimeAfter {
		return true
	}
	elapsed, completed := vu.sleepActive(ctx, vu.actionThinkTime(action))
	vu.noteSourceTransactionThinkTime(elapsed)
	vu.iterationThinkTime += elapsed
	return completed
}

// actionThinkTime computes the think time after action. It is kept out of
// applyActionThinkTime so the configurations it builds are not held on the
// stack of a VUser parked in that think time.
func (vu *VUser) actionThinkTime(action *dsl.Action) time.Duration {
	global := vu.Script.Config.ThinkTime
	thinkCfg := protocol.ThinkTimeConfig{
		Type: global.Type, MeanMs: global.MeanMs, StdMs: global.StdMs,
		MinMs: global.MinMs, MaxMs: global.MaxMs, Multiplier: global.Multiplier,
		LimitMs: global.LimitMs, Ignore: global.Ignore, MinPercent: global.MinPercent, MaxPercent: global.MaxPercent,
	}
	return computeThinkTime(resolveActionThinkTime(thinkCfg, action.ThinkTime))
}

func actionThinkTimeInterruptedError(ctx context.Context) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return fmt.Errorf("action think time was interrupted")
}

func (vu *VUser) resetProtocolForIteration(ctx context.Context) error {
	if vu == nil || vu.Proto == nil || vu.Script == nil {
		return nil
	}
	tctx, tcancel := teardownContext()
	defer tcancel()
	if err := callProtocolTeardown(tctx, vu.Proto, vu.resultPublicationRedactionConfig()); err != nil {
		return err
	}
	vu.Metrics.captureBrowserProvider(vu.Proto, vu.ResultSink, vu.Group, vu.Region, vu.resultPublicationRedactionConfig())
	handOffBrowserSessionEvidence(vu.Proto, vu.ResultSink, vu.RedactionConfig)
	vu.bindRuntimeTextRedactor()
	config := buildProtoConfigForRunModeWithSecretResolver(vu.Script, "load", vu.secretResolver())
	if vu.sourceSamplers != nil {
		vu.sourceSamplers.bindProtocolConfig(config, vu.Script)
	}
	config, err := withBrowserIdentityConfig(config, vu.browserIdentity, vu.Script)
	if err != nil {
		return err
	}
	if err := callProtocolInit(ctx, vu.Proto, config, vu.RedactionConfig); err != nil {
		return err
	}
	return ctx.Err()
}
