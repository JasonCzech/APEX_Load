package engine

import (
	"context"
	"fmt"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func runReplayConcurrentHTTP(ctx context.Context, vu *VUser, action dsl.Action, phase string, actionIndex int, stepPath []string, iterationPath []int, leafCounter *int, continueOnError, ignoreThinkTime bool, globalThinkCfg protocol.ThinkTimeConfig, report *ReplayReport, onProgress func(ReplayProgressEvent), debugger ReplayDebugController) (replayFlow, bool) {
	previousConfig, previousUnavailable := vu.concurrentPublicationRedaction, vu.concurrentPublicationUnavailable
	defer func() {
		vu.concurrentPublicationRedaction, vu.concurrentPublicationUnavailable = previousConfig, previousUnavailable
	}()
	boundary := replayDebugActionFor(action, phase, actionIndex, report.nextEventIndex())
	boundary.EventID = replayLeafEventID(report.ReplaySessionID, phase, actionIndex, boundary.EventIndex, appendStepPath(stepPath, "concurrent_group"))
	boundary.StepPath = append([]string(nil), stepPath...)
	boundary.IterationPath = append([]int(nil), iterationPath...)
	runtimeLocation := *cloneReplayDebugAction(boundary)
	defer vu.enterReplayRuntimeEvent(boundary.EventID)()
	if !vu.unredactedDebug {
		scrubReplayDebugAction(&boundary, vu.RedactionConfig)
	}
	if !vu.unredactedDebug && vu.runtimeCredentialRedactionErr != nil {
		omitUnredactableReplayDebugAction(&boundary)
	}
	if debugger != nil {
		if err := vu.replayBeforeBoundary(ctx, debugger, boundary, runtimeLocation); err != nil {
			report.Success = false
			report.Error = vu.replayPublicationError(err)
			return replayStop, true
		}
	}
	group, groupErr := vu.runConcurrentHTTP(ctx, action, vu.Script.Config.ErrorHandling)
	success, aborted := groupErr == nil, false
	var failure string
	if groupErr != nil {
		failure = groupErr.Error()
		report.Success = false
		if report.Error == "" {
			report.Error = vu.replayPublicationError(groupErr)
		}
	}
	if len(group.children) == 0 {
		unit := replayUnit{action: action, phase: phase, actionIndex: actionIndex, parentName: action.Name, parentBusinessTransaction: true, stepPath: stepPath, iterationPath: iterationPath, synthErr: groupErr, skipDebugBoundary: true}
		_, _, aborted = runReplayUnit(ctx, vu, unit, leafCounter, continueOnError, true, globalThinkCfg, report, onProgress, debugger)
	}
	for index := range group.children {
		child := &group.children[index]
		if err := vu.useConcurrentChildRedaction(child); err != nil {
			child.err = err
			clearUnredactableResultEvidence(child.result)
		}
		if child.result != nil {
			if child.result.ProtocolEvidence == nil {
				child.result.ProtocolEvidence = make(map[string]any)
			}
			child.result.ProtocolEvidence["concurrent_group_duration_ms"] = float64(group.completed.Sub(group.started).Microseconds()) / 1000
		}
		ordinal := index
		unit := replayUnit{action: child.authored, phase: phase, actionIndex: actionIndex, parentName: action.Name, parentBusinessTransaction: true, stepPath: appendStepPath(stepPath, requestSegment(index)), iterationPath: iterationPath, requestIndex: &ordinal, requestCount: len(group.children), prepared: child, skipDebugBoundary: true}
		event, ok, abort := runReplayUnit(ctx, vu, unit, leafCounter, continueOnError, true, globalThinkCfg, report, onProgress, debugger)
		if !ok {
			success = false
			if failure == "" {
				failure = event.ErrorMessage
			}
		}
		aborted = aborted || abort
	}
	aggregate := ReplayActionEvent{EventID: boundary.EventID, Phase: phase, ActionIndex: actionIndex, EventIndex: boundary.EventIndex, ActionName: action.Name, ActionType: action.Type, StartedAt: group.started, CompletedAt: group.completed, DurationMs: float64(group.completed.Sub(group.started).Microseconds()) / 1000, Success: success, ErrorMessage: failure}
	// BeforeAction precedes observation. The post-join debugger receipt must
	// use the current publication knowledge, even for an authored group label.
	if !vu.unredactedDebug {
		boundary.ActionName = scrubSensitiveTextWithConfig(boundary.ActionName, vu.resultPublicationRedactionConfig())
		scrubReplayDebugAction(&boundary, vu.resultPublicationRedactionConfig())
		aggregate.ActionName = scrubSensitiveTextWithConfig(aggregate.ActionName, vu.resultPublicationRedactionConfig())
		scrubReplayActionEvent(&aggregate, vu.resultPublicationRedactionConfig())
	}
	if !vu.unredactedDebug && vu.resultPublicationUnavailable() {
		omitUnredactableReplayDebugAction(&boundary)
		omitUnredactableReplayActionEvent(&aggregate)
	}
	if debugger != nil {
		if err := vu.replayAfterBoundary(ctx, debugger, boundary, runtimeLocation, &aggregate); err != nil {
			report.Success = false
			report.Error = vu.replayPublicationError(err)
			aborted = true
		}
	}
	if !success {
		report.Success = false
		if report.Error == "" {
			report.Error = aggregate.ErrorMessage
		}
	}
	if !aborted && (success || continueOnError) {
		if !applyReplayContainerThinkTime(ctx, vu, action, ignoreThinkTime, globalThinkCfg, report) {
			aborted = true
		}
	}
	if aborted || ctx.Err() != nil {
		return replayStop, true
	}
	if !success {
		return replayStop, false
	}
	return replayNext, false
}

func requestSegment(index int) string { return fmt.Sprintf("requests[%d]", index) }
