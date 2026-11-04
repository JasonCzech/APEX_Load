package api

import (
	"strings"
	"time"
)

// Automatic stop rules a RunStopTrigger names.
const (
	runStopRuleErrorRate    = "error_rate"
	runStopRuleSustainedP99 = "sustained_p99"
	runStopRuleGoal         = "goal"
)

// RunStopTrigger is the automatic stop that ended a run early: which rule
// fired, its description, and when the stop was requested.
type RunStopTrigger struct {
	Rule   string    `json:"rule"`
	Reason string    `json:"reason,omitempty"`
	At     time.Time `json:"at"`
}

// recordAutoStopTrigger keeps the first automatic stop requested for a
// running test. Later triggers, and triggers after the run left RUNNING,
// change nothing: the first is the one that ended the run.
//
// It returns the trigger it installed, nil when it installed none.
func (tr *TestRunner) recordAutoStopTrigger(test *ManagedTest, rule, reason string) *RunStopTrigger {
	if tr == nil || test == nil {
		return nil
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if test.Status != "RUNNING" || test.autoStopTrigger != nil {
		return nil
	}
	test.autoStopTrigger = &RunStopTrigger{Rule: rule, Reason: strings.TrimSpace(reason), At: time.Now().UTC().Truncate(time.Second)}
	return test.autoStopTrigger
}

// clearAutoStopTrigger removes a trigger whose stop request failed, if the
// run is still running and the trigger is still the one installed.
//
// The trigger is held in memory only: a controller that adopts the run after
// a failover archives it without one.
func (tr *TestRunner) clearAutoStopTrigger(test *ManagedTest, installed *RunStopTrigger) {
	if tr == nil || test == nil || installed == nil {
		return
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if test.Status == "RUNNING" && test.autoStopTrigger == installed {
		test.autoStopTrigger = nil
	}
}

// autoStopTriggerOf returns a copy of the run's recorded automatic stop. It
// reads the live run when the runner holds one, since an archive retry may
// pass a test rebuilt from its stored record.
func (tr *TestRunner) autoStopTriggerOf(test *ManagedTest) *RunStopTrigger {
	if test == nil {
		return nil
	}
	if tr != nil {
		tr.mu.RLock()
		defer tr.mu.RUnlock()
		if live, ok := tr.tests[test.ID]; ok && live != nil && live.autoStopTrigger != nil {
			test = live
		}
	}
	if test.autoStopTrigger == nil {
		return nil
	}
	copied := *test.autoStopTrigger
	return &copied
}

// autoStopSuperseded says an operator stopped the run before the rule that
// installed trigger could: the run already left RUNNING with an operator
// recorded as stopping it.
func (tr *TestRunner) autoStopSuperseded(test *ManagedTest, trigger *RunStopTrigger) bool {
	if tr == nil || test == nil || trigger == nil {
		return false
	}
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	return test.Status != "RUNNING" && strings.TrimSpace(test.StoppedBySubject) != ""
}

// clearAutoStopTriggerAnyStatus removes the installed trigger whatever the
// run's status, if it is still the one installed.
func (tr *TestRunner) clearAutoStopTriggerAnyStatus(test *ManagedTest, installed *RunStopTrigger) {
	if tr == nil || test == nil || installed == nil {
		return
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if test.autoStopTrigger == installed {
		test.autoStopTrigger = nil
	}
}
