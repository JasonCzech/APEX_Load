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
	browserAuthoredDefinition        protocol.BrowserAuthoredDefinition
	browserAuthoredIndexedDefinition protocol.BrowserAuthoredDefinition
	browserInvocation                uint64
	browserDocumentProofVersion      int
	browserInteractionVersion        int
	// browserAuthoredSource is the trusted authored script before an engine
	// iteration/session copy. It is never read from executable script metadata.
	browserAuthoredSource        *dsl.Script
	browserAuthoredIndexedSource *dsl.Script
	browserAuthoredScript        *dsl.Script
	browserAuthored              *browserAuthoredIndex
	currentAuthoredAction        *dsl.Action

	LiveTableClient      livetable.Client
	LiveTableClientID    string
	LiveTableBindings    []LiveTableBinding
	liveTables           *liveTableRuntime
	liveTableLane        *liveTableLane
	nativeServices       *nativeServices
	replayRuntime        *replayRuntimeState
	unredactedDebug      bool
	debugRetryEvidence   *ReplayActionEvent
	debugRetryError      error
	debugRetryGroupCount int

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
	runtimeRedaction runtimeRedactionPublisher
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
	onAsyncResult   func(*protocol.Result, *ReplayActionEvent)
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
	lc := &lifecycleRun{
		vu:                  vu,
		actionCtx:           actionCtx,
		boundaryCtx:         boundaryCtx,
		softBoundaryCtx:     boundaryCtx,
		entitledBoundaryCtx: actionCtx,
		phase:               phase,
		ignoreErrors:        ignoreErrors,
		logger:              logger,
		logCfg:              logCfg,
		errCfg:              errCfg,
	}
	if ignoreErrors {
		lc.errCfg.ContinueOnError = dsl.ContinueOnErrorValue(true)
	}
	// Teardown has a fresh bounded context specifically so cleanup can proceed
	// after graceful/scale/hard-stop boundaries have been canceled. Once a
	// teardown source transaction owns a goal reservation, keep its enclosed
	// actions on that teardown context just like at-once lifecycle teardown.
	if vu.ActionBoundaryCtx != nil && !strings.EqualFold(strings.TrimSpace(phase), "teardown") {
		lc.entitledBoundaryCtx = vu.ActionBoundaryCtx
	}
	if strings.EqualFold(strings.TrimSpace(phase), "init") {
		lc.signIn = vu.recordedSignIn
	}
	return lc.runActions(actions, 0) != flowTeardown
}

// lifecycleRun is the state of one runLifecycleActionsWithContexts call
// (init, teardown, async conversation, WebSocket callback and parallel branch
// steps), kept on the heap for the reason vuserRun is: the frames on the way
// to an action's think time stay small, so the GC can shrink the stack of a
// VUser parked there.
type lifecycleRun struct {
	vu          *VUser
	actionCtx   context.Context
	boundaryCtx context.Context
	// softBoundaryCtx is the boundary the phase started with, and
	// entitledBoundaryCtx the one it switches to while the phase holds a goal
	// reservation: a source transaction's, or a business-transaction
	// emitter's.
	softBoundaryCtx     context.Context
	entitledBoundaryCtx context.Context
	phase               string
	ignoreErrors        bool
	logger              *slog.Logger
	logCfg              dsl.LogConfig
	errCfg              dsl.ErrorHandlingConfig
	// deniedSourceScopes are the source transactions best-effort teardown
	// could not get a goal slot for; their enclosed actions are skipped.
	deniedSourceScopes []string
	// signIn replaces the recorded sign-in of an init phase's top-level
	// actions.
	signIn *recordedSignInPlan
}

func (lc *lifecycleRun) restoreBoundary() {
	if lc.vu.hasOpenSourceTransactionGoalOwnership() {
		lc.boundaryCtx = lc.entitledBoundaryCtx
		return
	}
	lc.boundaryCtx = lc.softBoundaryCtx
}

func (lc *lifecycleRun) claimTransaction() bool {
	return lc.vu.startGoalTransaction(lc.boundaryCtx)
}

func lifecycleSourceScopeKey(action dsl.Action) string {
	return strings.ToLower(strings.TrimSpace(sourceTransactionActionName(action)))
}

// runActions runs a lifecycle action list. Lifecycle actions use the same
// actionFlow model as the main path so control_break / control_continue
// inside a control_loop are honored here too (and not silently dropped);
// flowTeardown means "abort the phase".
func (lc *lifecycleRun) runActions(actions []dsl.Action, depth int) actionFlow {
	for index := range actions {
		action := &actions[index]
		if depth == 0 && lc.signIn != nil {
			if lc.signIn.skips(index) {
				continue
			}
			action = lc.signIn.actionRef(action)
		}
		switch f := lc.runOne(action, depth); f {
		case flowTeardown:
			return flowTeardown
		case flowBreak, flowContinue:
			if depth == 0 {
				// A stray break/continue with no enclosing loop is a no-op that
				// proceeds to the next action (matching the main + replay paths).
				warnStrayControlFlow(lc.logger, action.Name, lc.phase)
				continue
			}
			return f // bubble up to the nearest enclosing control_loop
		}
	}
	return flowNext
}

// runOne runs one lifecycle action and applies its think time once the
// frame that executed it has returned.
func (lc *lifecycleRun) runOne(action *dsl.Action, depth int) actionFlow {
	vu := lc.vu
	if vu.PauseControl != nil {
		if err := vu.PauseControl.WaitIfPaused(lc.boundaryCtx); err != nil {
			return flowTeardown
		}
	}
	if lc.stopped() {
		return flowTeardown
	}
	var flow actionFlow
	var thinkAfter bool
	switch strings.ToLower(strings.TrimSpace(action.Type)) {
	case "control_break":
		return flowBreak
	case "control_continue":
		return flowContinue
	case "control_if":
		flow, thinkAfter = runIfBlock(lc, action, depth)
	case "control_switch":
		flow, thinkAfter = runSwitchBlock(lc, action, depth)
	case "control_parallel":
		flow, thinkAfter = lc.runParallel(action, depth)
	case "control_loop":
		flow, thinkAfter = runLoopBlock(lc, action, depth)
	default:
		flow, thinkAfter = lc.runLeaf(action)
	}
	if thinkAfter && !vu.applyActionThinkTimeConfig(lc.boundaryCtx, action.ThinkTimeAfter, &action.ThinkTime) {
		return flowTeardown
	}
	return flow
}

func (lc *lifecycleRun) stopped() bool {
	return lc.boundaryCtx.Err() != nil || lc.actionCtx.Err() != nil
}

func (lc *lifecycleRun) vuser() *VUser     { return lc.vu }
func (lc *lifecycleRun) log() *slog.Logger { return lc.logger }

// controlBlockRuns starts user work for a control block and reports whether
// the block can run at depth.
func (lc *lifecycleRun) controlBlockRuns(action *dsl.Action, depth int) bool {
	lc.vu.markUserWorkStarted()
	if action.Control == nil {
		return false
	}
	if depth >= maxControlDepth {
		lc.logger.Error(lc.phase+" control flow nesting exceeded", "action", action.Name)
		return false
	}
	return true
}

func (lc *lifecycleRun) runParallel(action *dsl.Action, depth int) (actionFlow, bool) {
	prevAuthoredAction := lc.vu.currentAuthoredAction
	lc.vu.currentAuthoredAction = action
	defer func() { lc.vu.currentAuthoredAction = prevAuthoredAction }()
	lc.vu.markUserWorkStarted()
	if action.Control == nil || depth >= maxControlDepth {
		if lc.ignoreErrors {
			return flowNext, false
		}
		return flowTeardown, false
	}
	if !lc.vu.executeParallelBlock(lc.actionCtx, *action, lc.phase) && !lc.ignoreErrors {
		return flowTeardown, false
	}
	return flowNext, true
}

// runLeaf executes a lifecycle action that is not a control block and
// reports whether its think time follows.
func (lc *lifecycleRun) runLeaf(step *dsl.Action) (actionFlow, bool) {
	vu, logger, phase, ignoreErrors := lc.vu, lc.logger, lc.phase, lc.ignoreErrors
	prevAuthoredAction := vu.currentAuthoredAction
	vu.currentAuthoredAction = step
	defer func() { vu.currentAuthoredAction = prevAuthoredAction }()
	action := *step
	// Best-effort teardown must not execute a denied source scope's enclosed
	// SUT calls without a transaction slot. This check follows control
	// containers so a matching end in the selected branch/loop is still
	// observed; once it closes the scope, subsequent cleanup can resume.
	if len(lc.deniedSourceScopes) > 0 {
		switch {
		case sourceTransactionStarts(action):
			lc.deniedSourceScopes = append(lc.deniedSourceScopes, lifecycleSourceScopeKey(action))
		case sourceTransactionEnds(action):
			key := lifecycleSourceScopeKey(action)
			for index := len(lc.deniedSourceScopes) - 1; index >= 0; index-- {
				if lc.deniedSourceScopes[index] == key {
					lc.deniedSourceScopes = append(lc.deniedSourceScopes[:index], lc.deniedSourceScopes[index+1:]...)
					break
				}
			}
		}
		return flowNext, false
	}
	sourceStartReserved := false
	if vu.sourceTransactionStartWillOpen(action) {
		if vu.ExecutionGoals != nil && vu.ExecutionGoals.hasTransactionLimit() {
			if !vu.reserveGoalTransaction(lc.boundaryCtx) {
				if ignoreErrors {
					lc.deniedSourceScopes = append(lc.deniedSourceScopes, lifecycleSourceScopeKey(action))
					return flowNext, false
				}
				return flowTeardown, false
			}
			sourceStartReserved = true
			lc.boundaryCtx = lc.entitledBoundaryCtx
		}
	}
	if (vu.sourceTransactionEndNeedsGoalClaim(action) || sourceTransactionRecordsImmediate(action)) &&
		!lc.claimTransaction() {
		if ignoreErrors {
			return flowNext, false
		}
		return flowTeardown, false
	}
	sourceCompletionWillExecute := vu.sourceTransactionCompletionWillExecute(action)
	if handled, err := vu.handleEngineAction(lc.actionCtx, action); handled {
		vu.markUserWorkStarted()
		if sourceStartReserved {
			vu.markSourceTransactionGoalReserved(action)
		}
		if sourceCompletionWillExecute {
			lc.restoreBoundary()
		}
		if err != nil {
			if dsl.CanonicalActionType(action.Type) != "source_transaction" {
				vu.noteSourceTransactionResult(action, false)
			}
			logger.Error(phase+" action failed", "action", action.Name, "err", scrubSensitiveError(err, vu.RedactionConfig))
			if !ignoreErrors {
				return flowTeardown, false
			}
		}
		return flowNext, lc.actionCtx.Err() == nil && lc.boundaryCtx.Err() == nil
	}
	if strings.EqualFold(strings.TrimSpace(action.Type), "http_sequence") {
		// A transparent group records no transaction, as one an open source
		// transaction owns records none of its own.
		markerOwned := vu.activeSourceTransactionOwnsHTTPSequence(action) || !dsl.HTTPSequenceRecordsTransaction(action)
		if !markerOwned && !lc.claimTransaction() {
			if ignoreErrors {
				return flowNext, false
			}
			return flowTeardown, false
		}
		requestTransactions := dsl.HTTPSequenceRequestTransactions(action) != nil
		aggregateTransaction := !markerOwned && !requestTransactions
		slotClaimed := !markerOwned
		claimRequestTransaction := func() bool {
			if slotClaimed {
				slotClaimed = false
				return true
			}
			return lc.claimTransaction()
		}
		vu.markUserWorkStarted()
		result, err := vu.executeHTTPSequenceAction(lc.actionCtx, action, lc.errCfg, claimRequestTransaction)
		if errors.Is(err, errTransactionGoalReached) {
			if ignoreErrors {
				return flowNext, false
			}
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
			logger.Error(phase+" action failed", "action", action.Name, "err", scrubSensitiveError(err, vu.RedactionConfig))
			if !ignoreErrors {
				return flowTeardown, false
			}
			return flowNext, false
		}
		if result != nil {
			vu.noteSourceTransactionResult(action, result.Success)
			if aggregateTransaction {
				vu.recordTransaction(action.Name, result.Duration, result.Success, result)
			}
			if !result.Success && !ignoreErrors {
				return flowTeardown, false
			}
		}
		return flowNext, true
	}
	if err := vu.beforeSourceSampler(lc.boundaryCtx, action); err != nil {
		if !errors.Is(err, errSamplerScheduleComplete) && !errors.Is(err, context.Canceled) {
			logger.Error("sampler gate failed", "err", err)
		}
		return flowTeardown, false
	}
	if err := vu.beforeWebSocketAction(lc.actionCtx, action.Type); err != nil {
		logger.Error("WebSocket callback failed", "err", scrubSensitiveError(err, vu.RedactionConfig))
		return flowTeardown, false
	}

	leasedCtx, releasePause, pauseErr := vu.admitRuntimePauseAction(lc.actionCtx)
	if pauseErr != nil {
		return flowTeardown, false
	}
	defer releasePause()
	resolved := vu.resolveBrowserAuthoredAction(action, step)
	knownBusinessTransactionEmitter := actionEmitsBusinessTransactions(action)
	emitterTransactionReserved := false
	if knownBusinessTransactionEmitter &&
		vu.ExecutionGoals != nil &&
		vu.ExecutionGoals.hasTransactionLimit() {
		if !vu.reserveGoalTransaction(lc.boundaryCtx) {
			if ignoreErrors {
				return flowNext, false
			}
			return flowTeardown, false
		}
		emitterTransactionReserved = true
		lc.boundaryCtx = lc.entitledBoundaryCtx
	}
	claimEmittedTransaction := func() bool {
		if emitterTransactionReserved {
			vu.ExecutionGoals.CommitTransactionReservation()
			emitterTransactionReserved = false
			return true
		}
		return lc.claimTransaction()
	}
	releaseEmitterReservation := func() {
		if emitterTransactionReserved {
			vu.ExecutionGoals.ReleaseTransactionReservation()
			emitterTransactionReserved = false
		}
		if !vu.hasOpenSourceTransactionGoalOwnership() {
			lc.boundaryCtx = lc.softBoundaryCtx
		}
	}
	vu.markUserWorkStarted()
	result, err := vu.executeWithRetryStream(leasedCtx, resolved, lc.errCfg, func(child *protocol.Result) error {
		if child != nil && child.BusinessTransaction && !claimEmittedTransaction() {
			return errTransactionGoalReached
		}
		vu.recordResult(child, lc.errCfg, lc.logCfg)
		if child != nil {
			vu.noteSourceTransactionResult(*step, child.Success)
		}
		return nil
	})
	releasePause()
	vu.acceptWebSocketCallback(action, result, err)
	if err != nil {
		releaseEmitterReservation()
		if errors.Is(err, errTransactionGoalReached) {
			if ignoreErrors {
				return flowNext, false
			}
			return flowTeardown, false
		}
		vu.noteSourceTransactionResult(action, false)
		logger.Error(phase+" action failed", "action", action.Name, "err", scrubSensitiveError(err, vu.RedactionConfig))
		if !ignoreErrors {
			return flowTeardown, false
		}
		return flowNext, lc.actionCtx.Err() == nil && lc.boundaryCtx.Err() == nil
	}
	if result != nil && result.BusinessTransaction && !claimEmittedTransaction() {
		releaseEmitterReservation()
		if ignoreErrors {
			return flowNext, false
		}
		return flowTeardown, false
	}
	releaseEmitterReservation()
	vu.recordResult(result, lc.errCfg, lc.logCfg)
	if result != nil {
		vu.noteSourceTransactionResult(action, result.Success)
	}
	if result != nil && result.Success && (lc.logCfg.OnErrorOnly || lc.logCfg.Level == "extended") {
		logger.Info(phase+" action ok", actionLogAttrs(action.Name, resolved, result, lc.logCfg, false, vu.RedactionConfig)...)
	}
	if result != nil && !result.Success {
		if lc.logCfg.OnErrorOnly || lc.logCfg.Level == "extended" {
			logger.Error(phase+" action unsuccessful", "action", action.Name, "status", result.StatusCode, "error", result.ErrorMessage)
		} else {
			logger.Warn(phase+" action unsuccessful", "action", action.Name, "error", result.ErrorMessage)
		}
		if !ignoreErrors {
			return flowTeardown, false
		}
	}
	return flowNext, true
}

func (vu *VUser) applyActionThinkTimeConfig(ctx context.Context, enabled bool, override *dsl.ThinkTimeConfig) bool {
	if vu == nil || vu.Script == nil || !enabled {
		return true
	}
	elapsed, completed := vu.sleepActive(ctx, vu.actionThinkTime(override))
	vu.noteSourceTransactionThinkTime(elapsed)
	vu.iterationThinkTime += elapsed
	return completed
}

// actionThinkTime computes the think time an action's override gives. It is
// kept out of applyActionThinkTimeConfig so the configurations it builds are
// not held on the stack of a VUser parked in that think time.
func (vu *VUser) actionThinkTime(override *dsl.ThinkTimeConfig) time.Duration {
	global := vu.Script.Config.ThinkTime
	thinkCfg := protocol.ThinkTimeConfig{
		Type: global.Type, MeanMs: global.MeanMs, StdMs: global.StdMs,
		MinMs: global.MinMs, MaxMs: global.MaxMs, Multiplier: global.Multiplier,
		LimitMs: global.LimitMs, Ignore: global.Ignore, MinPercent: global.MinPercent, MaxPercent: global.MaxPercent,
	}
	return computeThinkTime(resolveActionThinkTime(thinkCfg, *override))
}

func actionThinkTimeInterruptedError(ctx context.Context) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return fmt.Errorf("action think time was interrupted")
}

func (vu *VUser) flushBrowserIterationEvidence() {
	if vu == nil || vu.Proto == nil || vu.ResultSink == nil {
		return
	}
	_, hasArchiveSink := vu.ResultSink.(BrowserInteractionEvidenceSink)
	_, hasLiveSink := vu.ResultSink.(LiveBrowserInteractionEvidenceSink)
	if !hasArchiveSink && !hasLiveSink {
		return
	}
	handOffBrowserInteractions(vu.Proto, vu.ResultSink, vu.resultPublicationRedactionConfig())
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
	handOffBrowserSessionEvidence(vu.Proto, vu.ResultSink, vu.resultPublicationRedactionConfig())
	vu.bindRuntimeTextRedactor()
	config := buildProtoConfigForRunModeWithSecretResolver(vu.Script, "load", vu.secretResolver())
	if vu.browserDocumentProofVersion != 0 {
		config["browser_document_proof_version"] = vu.browserDocumentProofVersion
	}
	if vu.sourceSamplers != nil {
		vu.sourceSamplers.bindProtocolConfig(config, vu.Script)
	}
	if vu.browserDocumentProofVersion == 0 || vu.browserDocumentProofVersion >= 3 {
		if sink, ok := vu.ResultSink.(protocol.BrowserSourceJournalProvider); ok && dsl.BrowserDocumentCaptureRequested(vu.Script) {
			config["browser_document_source_journal"] = sink
		}
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
