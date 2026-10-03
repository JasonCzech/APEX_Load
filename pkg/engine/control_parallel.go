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
	variables                  map[string]string
	extracted                  map[string]int
	err                        error
	failed                     bool
	publicationRedactionFailed bool
}

type parallelResultSink struct {
	downstream ResultSink
	failed     atomic.Bool
}

func (s *parallelResultSink) RecordResult(result *protocol.Result) error {
	if result != nil && !result.Success {
		s.failed.Store(true)
	}
	if s.downstream == nil {
		return nil
	}
	return s.downstream.RecordResult(result)
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
	success := vu.runParallelBranches(ctx, action.Control, phase)
	result := &protocol.Result{
		ActionName: action.Name,
		Group:      vu.Group,
		Region:     vu.Region,
		Phase:      phase,
		StartTime:  started,
		Duration:   time.Since(started),
		Success:    success,
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
	if vu == nil || vu.Script == nil || control == nil || len(control.Branches) < 2 {
		return false
	}
	if vu.runtimeCredentialRedactionError() != nil {
		return false
	}
	if !parallelActionsRuntimeSafe(control.Branches) {
		return false
	}
	if safety, ok := vu.Proto.(protocol.ParallelSafetyProvider); !ok || !safety.ParallelSafe() {
		return false
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
	for _, result := range results {
		if result.publicationRedactionFailed {
			vu.failRuntimeCredentialRedaction(fmt.Errorf("runtime credential redaction capacity exceeded"))
			return false
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
	return success
}

func (vu *VUser) runParallelBranch(ctx context.Context, index int, branch dsl.ParallelBranch, phase string) (result parallelBranchResult) {
	result = parallelBranchResult{index: index, name: strings.TrimSpace(branch.Name)}
	if err := vu.runtimeCredentialRedactionError(); err != nil {
		result.err, result.failed = err, true
		return
	}
	instance, err := protocol.Get(vu.Script.Protocol)
	if err != nil {
		result.err, result.failed = err, true
		return
	}
	sink := &parallelResultSink{downstream: vu.ResultSink}
	variables := cloneStringMap(vu.Variables)
	if variables == nil {
		variables = make(map[string]string)
	}
	branchVU := &VUser{
		ID:                           vu.ID,
		sourceSamplers:               vu.sourceSamplers,
		sourceParameters:             vu.sourceParameters,
		TotalVUsers:                  vu.TotalVUsers,
		Group:                        vu.Group,
		Region:                       vu.Region,
		RunID:                        vu.RunID,
		Proto:                        instance,
		Script:                       vu.Script,
		Metrics:                      vu.Metrics,
		ResultSink:                   sink,
		Variables:                    variables,
		DataProvider:                 vu.DataProvider,
		Parameters:                   NewParameterRuntime(vu.Script.Parameters, vu.DataProvider, vu.ID),
		SecretResolver:               vu.SecretResolver,
		RedactionConfig:              vu.RedactionConfig,
		importedCredentialInspection: vu.importedCredentialInspection,
		SnapshotStore:                vu.SnapshotStore,
		PauseControl:                 vu.PauseControl,
		IterationRateLimiter:         vu.IterationRateLimiter,
		Rendezvous:                   vu.Rendezvous,
		ExecutionGoals:               vu.ExecutionGoals,
		HardStopCtx:                  vu.HardStopCtx,
		ActionBoundaryCtx:            vu.ActionBoundaryCtx,
		extractedVariables:           make(map[string]int),
		sourceTransactions:           make(map[string]*sourceTransactionRuntimeState),
	}
	defer func() { result.publicationRedactionFailed = branchVU.runtimeCredentialRedactionErr != nil }()
	defer branchVU.closeErrorLogs()
	branchVU.ensureImportedCredentialInspection()
	branchVU.bindRuntimeTextRedactor()
	config := vuserProtoConfig(buildProtoConfigForRunModeWithSecretResolver(vu.Script, "load", branchVU.secretResolver()), vu.ID)
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
	cfg := vu.Script.Config
	ignoreErrors := strings.EqualFold(strings.TrimSpace(phase), "teardown")
	if !branchVU.runLifecycleActionsWithContexts(ctx, ctx, branch.Steps, phase, ignoreErrors, branchVU.makeLogger(cfg.Log), cfg.Log, cfg.ErrorHandling) {
		result.failed = true
	}
	result.failed = result.failed || sink.failed.Load()
	result.variables = branchVU.Variables
	result.extracted = branchVU.extractedVariables
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
