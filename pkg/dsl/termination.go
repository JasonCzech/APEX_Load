package dsl

import (
	"fmt"
	"time"
)

const (
	TerminationDuration   = "duration"
	TerminationIterations = "iterations"
)

func CanonicalTerminationMode(mode string) string {
	if mode == "" {
		return TerminationDuration
	}
	return mode
}

func ValidateTermination(mode string, duration time.Duration, scripts ...*Script) error {
	switch CanonicalTerminationMode(mode) {
	case TerminationDuration:
		if duration <= 0 {
			return fmt.Errorf("duration must be positive")
		}
	case TerminationIterations:
		if duration != 0 {
			return fmt.Errorf("iteration termination must not specify a duration")
		}
		if len(scripts) == 0 {
			return fmt.Errorf("iteration termination requires finite scripts")
		}
		for _, s := range scripts {
			if s == nil || s.Config.RunLogic.Iterations <= 0 || CanonicalWorkloadModel(s.Config.RunLogic.WorkloadModel) != "closed" {
				return fmt.Errorf("iteration termination requires positive per-user iterations and a closed workload")
			}
			if s.Config.RunLogic.GlobalIterations != 0 || s.Config.RunLogic.MaxTransactions != 0 {
				return fmt.Errorf("iteration termination cannot use competing shared execution limits")
			}
		}
	default:
		return fmt.Errorf("termination_mode must be duration or iterations")
	}
	return nil
}
