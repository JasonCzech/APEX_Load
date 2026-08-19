// Package testexec holds the lock-free, value-only helpers of the test-run
// lifecycle, lifted out of the api package's TestRunner so they can be reasoned
// about and unit-tested in isolation. Everything here is pure (depends only on
// the standard library and the engine package) — no shared TestRunner state,
// no locks. The stateful lifecycle/registry logic deliberately remains on
// TestRunner: it is interleaved with capacity/persistence/metrics under one
// shared mutex and cannot cross a package boundary cleanly (see the design
// evaluation for approach B).
package testexec

import (
	"fmt"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/scrub"
)

type LifecycleControl string

const (
	ControlStart  LifecycleControl = "start"
	ControlPause  LifecycleControl = "pause"
	ControlResume LifecycleControl = "resume"
	ControlStop   LifecycleControl = "stop"
	ControlAbort  LifecycleControl = "abort"
)

// lifecycleStatuses is the complete public/durable vocabulary considered by
// the status-only run-control matrix. STARTING is included even though it is a
// process-private persistence reservation: callers can race that reservation,
// and the matrix must prove that every control rejects it.
var lifecycleStatuses = []string{
	"QUEUED", "PENDING", "RESERVED", "SCHEDULED", "SHAKEOUT", "SHAKEOUT_REVIEW",
	"INITIALIZING", "STARTING", "RUNNING", "PAUSED", "STOPPING",
	"COMPLETE", "FAILED", "ABORTED",
}

var lifecycleControls = []LifecycleControl{ControlStart, ControlPause, ControlResume, ControlStop, ControlAbort}

// AllowsLifecycleControl is the shared status-only policy for operator
// lifecycle controls. Leadership, passive-write ownership, finalization, and
// backend capability checks are additional runtime gates and deliberately do
// not belong in this pure matrix.
func AllowsLifecycleControl(status string, control LifecycleControl) bool {
	switch control {
	case ControlStart:
		switch status {
		case "QUEUED", "PENDING", "RESERVED", "SCHEDULED", "SHAKEOUT_REVIEW":
			return true
		}
	case ControlPause:
		return status == "RUNNING"
	case ControlResume:
		return status == "PAUSED"
	case ControlStop, ControlAbort:
		switch status {
		case "QUEUED", "PENDING", "RESERVED", "SCHEDULED", "SHAKEOUT", "SHAKEOUT_REVIEW", "INITIALIZING",
			"RUNNING", "PAUSED", "STOPPING", "COMPLETE", "FAILED", "ABORTED":
			return true
		}
	}
	return false
}

// IsStartableTestStatus reports whether a test in the given status may be started.
func IsStartableTestStatus(status string) bool {
	return AllowsLifecycleControl(status, ControlStart)
}

// StartableTestStatusError is the error returned when a test is not in a startable status.
func StartableTestStatusError(status string) error {
	return fmt.Errorf("test is %s, must be QUEUED, PENDING, RESERVED, SCHEDULED, or SHAKEOUT_REVIEW to start", status)
}

// AbsInt returns the absolute value of an int.
func AbsInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

// CloneGroupSpecsWithResultSink returns a shallow copy of groupSpecs with every
// group's ResultSink set to resultSink.
func CloneGroupSpecsWithResultSink(groupSpecs []engine.GroupConfig, resultSink engine.ResultSink) []engine.GroupConfig {
	cloned := make([]engine.GroupConfig, len(groupSpecs))
	copy(cloned, groupSpecs)
	for i := range cloned {
		cloned[i].ResultSink = resultSink
	}
	return cloned
}

// ResourceBaselineWindows returns the (pre, post) baseline measurement windows,
// or (0, 0) when baselining is disabled.
func ResourceBaselineWindows(enabled bool) (time.Duration, time.Duration) {
	if !enabled {
		return 0, 0
	}
	const baselineWindow = 5 * time.Minute
	return baselineWindow, baselineWindow
}

// StartDelayWindow parses a start-delay duration string, returning 0 for empty
// or invalid input.
func StartDelayWindow(raw string) time.Duration {
	if raw == "" {
		return 0
	}
	duration, err := time.ParseDuration(raw)
	if err != nil || duration <= 0 {
		return 0
	}
	return duration
}

// AppendRunError appends "prefix: err" to an accumulating run-error string,
// returning existing unchanged when err is nil.
func AppendRunError(existing string, prefix string, err error) string {
	if err == nil {
		return scrub.Value(existing, scrub.Policy{})
	}
	message := scrub.Value(prefix+": "+err.Error(), scrub.Policy{})
	if existing == "" {
		return message
	}
	return scrub.Value(existing, scrub.Policy{}) + "; " + message
}
