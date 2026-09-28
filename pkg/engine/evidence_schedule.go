package engine

import (
	"errors"
	"time"
)

// VUserLifetime is a benchmark-only, immutable admission schedule. All capacity
// is reserved before Run starts; each VUser initializes once, then permanently
// closes at StopAfter. It does not implement distributed scaling or slot reuse.
type VUserLifetime struct {
	StartAfter time.Duration
	StopAfter  time.Duration
}

func validateEvidenceSchedule(cfg Config) error {
	if len(cfg.EvidenceSchedule) == 0 {
		return nil
	}
	if len(cfg.EvidenceSchedule) != cfg.VUsers || cfg.RampUp != 0 || isOpenWorkload(cfg.Script) || cfg.Script.Config.RunLogic.Iterations != 0 || cfg.Script.Config.RunLogic.GlobalIterations != 0 || cfg.Script.Config.RunLogic.MaxTransactions != 0 {
		return errors.New("evidence schedule requires duration-based closed workload and complete population")
	}
	for i, s := range cfg.EvidenceSchedule {
		if s.StartAfter < 0 || s.StopAfter <= s.StartAfter || s.StopAfter >= cfg.Duration || i > 0 && s.StartAfter < cfg.EvidenceSchedule[i-1].StartAfter {
			return errors.New("invalid evidence VUser lifetime")
		}
	}
	return nil
}
