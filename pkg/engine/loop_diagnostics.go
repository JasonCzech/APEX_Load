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
