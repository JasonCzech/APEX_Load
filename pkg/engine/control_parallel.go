package engine

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

const (
	defaultParallelConcurrency = 4
	maxParallelConcurrency     = 16
)

type parallelBranchResult struct {
	index                      int
	name                       string
	started, completed         time.Time
	quotaWaits                 []timeInterval
	variables                  map[string]string
	extracted                  map[string]int
	err                        error
	failed                     bool
	publicationRedactionFailed bool
	// phases sums the branch's request phases for the parent's open
	// authored transactions (a branch VUser has none of its own).
	phases sourceTransactionPhaseRollup
	// traces are the branch's slowest and first failed requests that sent a
	// trace context.
	traces sourceTransactionTraceExemplars
}

type parallelResultSink struct {
	downstream ResultSink
	failed     atomic.Bool
	// dispatchBranch is set when the block started from an async callback:
	// the branch's work is that dispatch's, and its phases go to the
	// parent's transactions opened during it. Otherwise dispatch work of the
	// branch's own (its async callbacks) joins none of the parent's open
	// transactions, which were all opened outside that dispatch.
	dispatchBranch bool
	// phases sums the branch's request phases for the parent's open
	// authored transactions.
	phasesMu sync.Mutex
	phases   sourceTransactionPhaseRollup
	traces   sourceTransactionTraceExemplars
}

func (s *parallelResultSink) RecordResult(result *protocol.Result) error {
	if result != nil && !result.Success {
		s.failed.Store(true)
	}
	s.notePhases(result)
	if s.downstream == nil {
		return nil
	}
	return s.downstream.RecordResult(result)
}

// notePhases applies noteSourceTransactionPhases' rule to a branch request.
func (s *parallelResultSink) notePhases(result *protocol.Result) {
	if s == nil || result == nil || result.BusinessTransaction {
		return
	}
	if resultOutsideSourceTransactions(result) {
		return
	}
	if dispatch, _ := result.ProtocolEvidence["async_dispatch"].(bool); dispatch && !s.dispatchBranch {
		return
	}
	if exemplar, ok := sourceTransactionTraceExemplarOf(result); ok {
		s.phasesMu.Lock()
		s.traces.offer(exemplar)
		s.phasesMu.Unlock()
	}
	sample, ok := sourceTransactionPhaseSample(result)
	if !ok {
		return
	}
	s.phasesMu.Lock()
	s.phases.merge(sample)
	s.phasesMu.Unlock()
}

func (s *parallelResultSink) phaseTotals() sourceTransactionPhaseRollup {
	s.phasesMu.Lock()
	defer s.phasesMu.Unlock()
	return s.phases
}

func (s *parallelResultSink) traceExemplars() sourceTransactionTraceExemplars {
	s.phasesMu.Lock()
	defer s.phasesMu.Unlock()
	return s.traces
}

func (s *parallelResultSink) RecordTransaction(result *protocol.Result) error {
	if result != nil && !result.Success {
		s.failed.Store(true)
	}
	if downstream, ok := s.downstream.(TransactionSink); ok {
		return downstream.RecordTransaction(result)
	}
	return s.RecordResult(result)
}

func (vu *VUser) executeParallelBlock(ctx context.Context, action dsl.Action, phase string) bool {
	started := time.Now()
	// Branch VUsers carry no transaction state, so the block resolves its
	// correlation context once: inside an open transaction every branch keeps
	// that transaction's trace, and otherwise the branches share the block's.
	ctx, endTransactionRoot := vu.correlationContext(ctx, &protocol.Action{Name: action.Name})
	defer endTransactionRoot()
	success, idleWindows := vu.runParallelBranchesTimed(ctx, action.Control, phase)
	// The block's sample leaves out the time its branches only queued for
	// quota; the enclosing transactions and iteration do too, and so does an
	// enclosing parallel block when this one runs in a branch.
	var idleQuota time.Duration
	for _, window := range idleWindows {
		idleQuota += window.end.Sub(window.start)
	}
	vu.llmQuotaWait += idleQuota
	if vu.trackQuotaWaitIntervals {
		vu.noteQuotaWaitIntervals(idleWindows...)
	}
	result := &protocol.Result{
		ActionName:   action.Name,
		Group:        vu.Group,
		Region:       vu.Region,
		Phase:        phase,
		StartTime:    started,
		Duration:     max(0, time.Since(started)-idleQuota),
		Success:      success,
		AuthoredStep: vu.currentBrowserAuthoredStep(),
	}
	if action.Attribution != nil {
		result.Hierarchy = strings.TrimSpace(vu.interpolate(action.Attribution.HierarchyPath))
		result.PageName = strings.TrimSpace(vu.interpolate(action.Attribution.PageName))
	}
	if !success {
		result.ErrorMessage = "one or more parallel branches failed"
	}
	vu.recordResult(result, vu.Script.Config.ErrorHandling, vu.Script.Config.Log)
	return success
}

func (vu *VUser) runParallelBranches(ctx context.Context, control *dsl.ControlFlow, phase string) bool {
	success, _ := vu.runParallelBranchesTimed(ctx, control, phase)
	return success
}

// runParallelBranchesTimed runs the branches and also returns the windows
// when some branch queued for quota and none was busy.
func (vu *VUser) runParallelBranchesTimed(ctx context.Context, control *dsl.ControlFlow, phase string) (bool, []timeInterval) {
	if vu == nil || vu.Script == nil || control == nil || len(control.Branches) < 2 {
		return false, nil
	}
	if vu.runtimeCredentialRedactionError() != nil {
		return false, nil
	}
	if !parallelActionsRuntimeSafe(control.Branches) {
		return false, nil
	}
	if safety, ok := vu.Proto.(protocol.ParallelSafetyProvider); !ok || !safety.ParallelSafe() {
		return false, nil
	}
	concurrency := control.Concurrency
	if concurrency <= 0 {
		concurrency = defaultParallelConcurrency
	}
	concurrency = min(concurrency, maxParallelConcurrency, len(control.Branches))
	branchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make([]parallelBranchResult, len(control.Branches))
	semaphore := make(chan struct{}, concurrency)
	// Branches share the parent's dispatch-epoch counter; create it before
	// they start, as they run concurrently.
	vu.asyncDispatchEpochCounter()
	var wg sync.WaitGroup
	for index, branch := range control.Branches {
		index, branch := index, branch
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-branchCtx.Done():
				results[index] = parallelBranchResult{index: index, name: branch.Name, err: branchCtx.Err(), failed: true}
				return
			}
			result := vu.runParallelBranch(branchCtx, index, branch, phase)
			results[index] = result
			if control.FailFast && (result.err != nil || result.failed) {
				cancel()
			}
		}()
	}
	wg.Wait()
	spans := make([]quotaWaitSpan, 0, len(results))
	for _, result := range results {
		spans = append(spans, quotaWaitSpan{start: result.started, end: result.completed, waits: result.quotaWaits})
	}
	idleWindows := idleQuotaWindows(spans)
	// Branch requests ran inside the parent's open authored transactions.
	// When the block ran from an async callback they are dispatch work, which
	// joins only the transactions opened during that dispatch, like the
	// callback's own requests (sourceTransactionTakes).
	for _, result := range results {
		vu.addSourceTransactionPhases(result.phases, vu.asyncDispatchActive())
		vu.offerSourceTransactionTraces(result.traces, vu.asyncDispatchActive())
	}
	for _, result := range results {
		if result.publicationRedactionFailed {
			vu.failRuntimeCredentialRedaction(fmt.Errorf("runtime credential redaction capacity exceeded"))
			return false, idleWindows
		}
	}
	success := true
	for _, result := range results {
		if result.err != nil || result.failed {
			success = false
		}
		branchName := strings.TrimSpace(result.name)
		for key := range result.extracted {
			if value, ok := result.variables[key]; ok {
				if err := vu.mergeExtractions(&protocol.Result{Extractions: map[string]string{"parallel." + branchName + "." + key: value}}); err != nil {
					success = false
				}
			}
		}
	}
	return success, idleWindows
}

func (vu *VUser) runParallelBranch(ctx context.Context, index int, branch dsl.ParallelBranch, phase string) (result parallelBranchResult) {
	result = parallelBranchResult{index: index, name: strings.TrimSpace(branch.Name), started: time.Now()}
	defer func() { result.completed = time.Now() }()
	if err := vu.runtimeCredentialRedactionError(); err != nil {
		result.err, result.failed = err, true
		return
	}
	instance, err := protocol.Get(vu.Script.Protocol)
	if err != nil {
		result.err, result.failed = err, true
		return
	}
	dispatchBranch := vu.asyncDispatchActive()
	sink := &parallelResultSink{downstream: vu.ResultSink, dispatchBranch: dispatchBranch}
	variables := cloneStringMap(vu.Variables)
	if variables == nil {
		variables = make(map[string]string)
	}
	branchVU := &VUser{
		correlation:                  vu.correlation,
		browserInteractionVersion:    vu.browserInteractionVersion,
		browserDocumentProofVersion:  vu.browserDocumentProofVersion,
		ID:                           vu.ID,
		sourceSamplers:               vu.sourceSamplers,
		sourceParameters:             vu.sourceParameters,
		TotalVUsers:                  vu.TotalVUsers,
		Group:                        vu.Group,
		Region:                       vu.Region,
		RunID:                        vu.RunID,
		Proto:                        instance,
		Script:                       vu.Script,
		browserAuthoredSource:        vu.browserAuthoredSource,
		browserAuthoredDefinition:    vu.browserAuthoredDefinition,
		Metrics:                      vu.Metrics,
		ResultSink:                   sink,
		Variables:                    variables,
		DataProvider:                 vu.DataProvider,
		LiveTableClient:              vu.LiveTableClient,
		LiveTableClientID:            vu.LiveTableClientID,
		LiveTableBindings:            vu.LiveTableBindings,
		liveTables:                   vu.liveTables,
		Parameters:                   NewParameterRuntime(vu.Script.Parameters, vu.DataProvider, vu.ID),
		SecretResolver:               vu.SecretResolver,
		RedactionConfig:              vu.RedactionConfig,
		importedCredentialInspection: vu.importedCredentialInspection,
		SnapshotStore:                vu.SnapshotStore,
		PauseControl:                 vu.PauseControl,
		IterationRateLimiter:         vu.IterationRateLimiter,
		QuotaPacer:                   vu.QuotaPacer,
		trackQuotaWaitIntervals:      vu.QuotaPacer != nil,
		quotaPauseBypass:             vu.quotaPauseBypass,
		Rendezvous:                   vu.Rendezvous,
		ExecutionGoals:               vu.ExecutionGoals,
		HardStopCtx:                  vu.HardStopCtx,
		ActionBoundaryCtx:            vu.ActionBoundaryCtx,
		extractedVariables:           make(map[string]int),
		sourceTransactions:           make(map[string]*sourceTransactionRuntimeState),
		asyncDispatchBranch:          dispatchBranch,
		asyncDispatchEpochs:          vu.asyncDispatchEpochs,
	}
	if dispatchBranch {
		// Everything the branch records is this dispatch's work.
		branchVU.asyncDispatchEpoch = vu.asyncDispatchEpoch
	}
	defer func() { result.phases, result.traces = sink.phaseTotals(), sink.traceExemplars() }()
	if vu.liveTables != nil {
		branchVU.liveTableLane = vu.liveTables.branchLane(vu.liveTableLane, index)
		defer func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := branchVU.settleLiveTableLeases(cleanupCtx, false, true); err != nil {
				result.err, result.failed = err, true
			}
		}()
	}
	// A branch's samples belong to the parent's iteration.
	branchVU.currentIteration.Store(vu.currentIteration.Load())
	branchVU.runCtx, branchVU.lifetime = vu.runCtx, vu.lifetime
	defer func() { result.publicationRedactionFailed = branchVU.runtimePublicationUnavailable() }()
	defer branchVU.closeErrorLogs()
	branchVU.ensureImportedCredentialInspection()
	branchVU.bindRuntimeTextRedactor()
	if err := branchVU.prepareNativeServices(); err != nil {
		result.err, result.failed = err, true
		return
	}
	config := vuserProtoConfig(buildProtoConfigForRunModeWithSecretResolver(vu.Script, "load", branchVU.secretResolver()), vu.ID)
	if vu.browserDocumentProofVersion != 0 {
		config["browser_document_proof_version"] = vu.browserDocumentProofVersion
	}
	if vu.browserDocumentProofVersion == 0 || vu.browserDocumentProofVersion >= 3 {
		if sink, ok := vu.ResultSink.(protocol.BrowserSourceJournalProvider); ok && dsl.BrowserDocumentCaptureRequested(vu.Script) {
			config["browser_document_source_journal"] = sink
		}
	}
	if vu.browserInteractionVersion != 0 {
		config["browser_interaction_evidence_version"] = vu.browserInteractionVersion
	}
	if vu.sourceSamplers != nil {
		vu.sourceSamplers.bindProtocolConfig(config, vu.Script)
	}
	if err := callProtocolInit(ctx, instance, config, vu.RedactionConfig); err != nil {
		result.err, result.failed = err, true
		return
	}
	defer func() {
		teardownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if teardownErr := callProtocolTeardown(teardownCtx, instance, branchVU.resultPublicationRedactionConfig()); teardownErr != nil && result.err == nil {
			result.err, result.failed = teardownErr, true
		}
	}()
	if source, ok := vu.Proto.(protocol.VerificationSnapshotter); ok {
		if destination, ok := instance.(protocol.VerificationController); ok {
			for _, check := range source.VerificationSnapshot() {
				if err := destination.ConfigureVerification(check); err != nil {
					result.err, result.failed = err, true
					return
				}
			}
		}
	}
	// Branches inherit the parent iteration's data bindings. Preparing a new
	// lifecycle would advance shared data cursors once per branch.
	if branchVU.Parameters != nil {
		branchVU.Parameters.Prime()
		branchVU.Parameters.inheritAllocatedParameterState(vu.Parameters)
		branchVU.Parameters.inheritSourceParameterState(vu.Parameters)
	}
	_ = branchVU.refreshImportedCredentialRuntimeValues()
	cfg := &vu.Script.Config
	ignoreErrors := strings.EqualFold(strings.TrimSpace(phase), "teardown")
	if !branchVU.runLifecycleActionsWithContexts(ctx, ctx, branch.Steps, phase, ignoreErrors, branchVU.makeLogger(cfg.Log), cfg.Log, cfg.ErrorHandling) {
		result.failed = true
	}
	result.failed = result.failed || sink.failed.Load()
	result.variables = branchVU.Variables
	result.extracted = branchVU.extractedVariables
	result.quotaWaits = branchVU.llmQuotaWaitIntervals
	return
}

func parallelActionsRuntimeSafe(branches []dsl.ParallelBranch) bool {
	for _, branch := range branches {
		if len(dsl.ValidateParallelBranchRuntimeSafety(branch.Steps, "branch")) > 0 {
			return false
		}
		stack := append([]dsl.Action(nil), branch.Steps...)
		for len(stack) > 0 {
			action := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			switch dsl.CanonicalActionType(strings.ToLower(strings.TrimSpace(action.Type))) {
			case "java_vuser_run", "browser":
				return false
			}
			if action.Control != nil {
				for _, nested := range action.Control.ActionBranches() {
					stack = append(stack, nested.Actions...)
				}
			}
		}
	}
	return true
}

func parallelRuntimeError(actionName string) error {
	return fmt.Errorf("control_parallel %q is not runtime-safe for this protocol or branch set", strings.TrimSpace(actionName))
}
