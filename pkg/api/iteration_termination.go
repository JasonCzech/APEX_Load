package api

import (
	"fmt"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"time"
)

func parseTerminationDuration(raw, mode string) (time.Duration, error) {
	switch dsl.CanonicalTerminationMode(mode) {
	case dsl.TerminationIterations:
		if raw != "" {
			return 0, fmt.Errorf("iteration termination must not specify duration")
		}
		return 0, nil
	case dsl.TerminationDuration:
		d, e := time.ParseDuration(raw)
		if e != nil {
			return 0, fmt.Errorf("invalid duration: %w", e)
		}
		if d <= 0 {
			return 0, fmt.Errorf("duration must be positive")
		}
		return d, nil
	default:
		return 0, fmt.Errorf("termination_mode must be duration or iterations")
	}
}
func validateIterationRequest(req CreateTestRequest) error {
	if dsl.CanonicalTerminationMode(req.TerminationMode) != dsl.TerminationIterations {
		return nil
	}
	if req.ScheduledStartAt != nil || req.TimeslotID != nil || req.TrafficProfile != nil || req.GoalConfig != nil || (req.ScenarioMode != "" && req.ScenarioMode != "vuser_driven") {
		return fmt.Errorf("iteration termination requires a fixed workload without scheduling, timeslot, traffic-profile, or goal-driven controls")
	}
	for _, g := range req.Groups {
		if len(g.Stages) > 0 || g.RampDown != "" {
			return fmt.Errorf("iteration termination does not support stages or ramp-down")
		}
	}
	return nil
}
func validateIterationEngineGroups(mode string, groups []engine.GroupConfig) error {
	if mode != dsl.TerminationIterations {
		return nil
	}
	scripts := make([]*dsl.Script, 0, len(groups))
	for _, g := range groups {
		if g.VUsers <= 0 {
			return fmt.Errorf("iteration termination requires positive fixed users")
		}
		scripts = append(scripts, g.Script)
	}
	return dsl.ValidateTermination(mode, 0, scripts...)
}

func persistedDurationString(duration int64, mode string) string {
	if mode == dsl.TerminationIterations {
		return ""
	}
	return time.Duration(duration).String()
}

func (tr *TestRunner) rejectIterationControl(id string) error {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	if test := tr.tests[tr.resolveRefKeyLocked(id)]; test != nil && test.TerminationMode == dsl.TerminationIterations {
		return fmt.Errorf("iteration termination has a fixed population and iteration count; scaling and duration adjustment are disabled")
	}
	return nil
}
