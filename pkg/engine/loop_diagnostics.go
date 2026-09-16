package engine

// loop_diagnostics.go — surfaces silent control_loop truncation.
//
// Condition-driven loops default to 1000 passes. Count and foreach loops use
// their declared bounds, subject to the hard DSL ceiling or max_iterations.
// Warn when a requested bound exceeds the hard ceiling.

import (
	"fmt"
	"log/slog"

	"github.com/jasonczech/apexload/pkg/internal/boundedcache"
)

// loopTruncationWarned dedupes truncation warnings to once per process per
// (action, requested, cap) so a high VUser count cannot flood the logs.
var loopTruncationWarned = boundedcache.NewStringCache[struct{}](0, 0, 0)

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
	logger.Warn("control_loop count exceeds the runtime safety cap and was truncated; reduce the requested bound",
		"action", actionName,
		"requested", requested,
		"cap", capLimit)
}

// conditionLoopSafetyCapWarned dedupes warnings for condition-driven loops that
// never evaluated false before the default runtime cap.
var conditionLoopSafetyCapWarned = boundedcache.NewStringCache[struct{}](0, 0, 0)

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
var strayControlFlowWarned = boundedcache.NewStringCache[struct{}](0, 0, 0)

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
