package engine

// loop_diagnostics.go — surfaces silent control_loop truncation.
//
// A control_loop is bounded by controlLoopMaxPasses: an explicit max_iterations,
// else defaultControlLoopMaxIterations (1000). A fixed Count — or a foreach over
// a data source — larger than that default is silently truncated, so an
// intended `count: 5000` (or a 50k-row foreach) quietly runs only 1000 passes.
// This warns when that happens so the truncation is visible and the operator can
// raise max_iterations to run all passes.

import (
	"fmt"
	"log/slog"
	"sync"
)

// loopTruncationWarned dedupes truncation warnings to once per process per
// (action, requested, cap) so a high VUser count cannot flood the logs.
var loopTruncationWarned sync.Map

// warnLoopTruncation emits a one-time warning that a control_loop's requested
// pass count (a fixed Count or a foreach row count) exceeds the runtime safety
// cap and was therefore truncated. Callers invoke it only when max_iterations
// was not set — an explicit max_iterations is treated as an intentional bound.
func warnLoopTruncation(logger *slog.Logger, actionName string, requested, capLimit int) {
	key := fmt.Sprintf("%s|%d|%d", actionName, requested, capLimit)
	if _, loaded := loopTruncationWarned.LoadOrStore(key, struct{}{}); loaded {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}
	logger.Warn("control_loop count exceeds the runtime safety cap and was truncated; set max_iterations to run all passes",
		"action", actionName,
		"requested", requested,
		"cap", capLimit)
}

// conditionLoopSafetyCapWarned dedupes warnings for condition-driven loops that
// never evaluated false before the default runtime cap.
var conditionLoopSafetyCapWarned sync.Map

func warnConditionLoopSafetyCap(logger *slog.Logger, actionName string, capLimit int) {
	key := fmt.Sprintf("%s|%d", actionName, capLimit)
	if _, loaded := conditionLoopSafetyCapWarned.LoadOrStore(key, struct{}{}); loaded {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}
	logger.Warn("control_loop condition reached the runtime safety cap; set max_iterations or fix the loop condition",
		"action", actionName,
		"cap", capLimit)
}

// strayControlFlowWarned dedupes the "break/continue outside a loop" warning to
// once per process per (action, phase) so a high VUser/iteration count cannot
// flood the logs (mirrors loopTruncationWarned).
var strayControlFlowWarned sync.Map

// warnStrayControlFlow emits a one-time warning that a control_break or
// control_continue executed outside any enclosing control_loop, where it is a
// no-op. It runs in the per-iteration hot path, so without this dedup a single
// mis-authored script at the 10k-VUsers target would emit millions of identical
// lines.
func warnStrayControlFlow(logger *slog.Logger, actionName, phase string) {
	key := actionName + "|" + phase
	if _, loaded := strayControlFlowWarned.LoadOrStore(key, struct{}{}); loaded {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}
	logger.Warn("control_break/control_continue outside a control_loop was ignored", "action", actionName, "phase", phase)
}
