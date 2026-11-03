package api

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func validateCapacitySweepConfig(cfg *CapacitySweepConfig) error {
	if cfg == nil {
		return nil
	}
	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	if mode != "" && mode != "concurrency" && mode != "rate" {
		return fmt.Errorf("workload_shape_config.capacity_sweep.mode must be concurrency or rate")
	}
	if len(cfg.Levels) == 0 {
		return fmt.Errorf("workload_shape_config.capacity_sweep.levels must contain at least one level")
	}
	if len(cfg.Levels) > 64 {
		return fmt.Errorf("workload_shape_config.capacity_sweep.levels cannot exceed 64 levels")
	}
	var prev float64
	for i, lvl := range cfg.Levels {
		if !finiteFloat(lvl) || lvl <= 0 {
			return fmt.Errorf("workload_shape_config.capacity_sweep.levels[%d] must be greater than zero", i)
		}
		if mode != "rate" && lvl > float64(maxManagedTestVUsers) {
			return fmt.Errorf("workload_shape_config.capacity_sweep.levels[%d] cannot exceed %d VUsers", i, maxManagedTestVUsers)
		}
		if i > 0 && lvl <= prev {
			return fmt.Errorf("workload_shape_config.capacity_sweep.levels must be strictly increasing")
		}
		prev = lvl
	}
	measureDur, err := time.ParseDuration(strings.TrimSpace(cfg.MeasureDuration))
	if err != nil || measureDur <= 0 {
		return fmt.Errorf("workload_shape_config.capacity_sweep.measure_duration must be a positive duration")
	}
	if strings.TrimSpace(cfg.WarmupDuration) != "" {
		warmupDur, err := time.ParseDuration(strings.TrimSpace(cfg.WarmupDuration))
		if err != nil || warmupDur < 0 {
			return fmt.Errorf("workload_shape_config.capacity_sweep.warmup_duration must be a non-negative duration")
		}
	}
	if cfg.SLOTTFTMs != nil && (!finiteFloat(*cfg.SLOTTFTMs) || *cfg.SLOTTFTMs <= 0) {
		return fmt.Errorf("workload_shape_config.capacity_sweep.slo_ttft_ms must be greater than zero")
	}
	if cfg.SLOTPOTMs != nil && (!finiteFloat(*cfg.SLOTPOTMs) || *cfg.SLOTPOTMs <= 0) {
		return fmt.Errorf("workload_shape_config.capacity_sweep.slo_tpot_ms must be greater than zero")
	}
	if cfg.SLOE2EMs != nil && (!finiteFloat(*cfg.SLOE2EMs) || *cfg.SLOE2EMs <= 0) {
		return fmt.Errorf("workload_shape_config.capacity_sweep.slo_e2e_ms must be greater than zero")
	}
	if cfg.MinGoodputPct != nil && (!finiteFloat(*cfg.MinGoodputPct) || *cfg.MinGoodputPct < 0 || *cfg.MinGoodputPct > 100) {
		return fmt.Errorf("workload_shape_config.capacity_sweep.min_goodput_pct must be between 0 and 100")
	}
	return nil
}

// ExpandCapacitySweepStages converts a CapacitySweepConfig into an ordered
// sequence of warm-up and measurement LoadStages plus total duration.
func ExpandCapacitySweepStages(cfg *CapacitySweepConfig) ([]storage.LoadStage, time.Duration, error) {
	if err := validateCapacitySweepConfig(cfg); err != nil {
		return nil, 0, err
	}
	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	if mode == "" {
		mode = "concurrency"
	}
	measureDur, _ := time.ParseDuration(strings.TrimSpace(cfg.MeasureDuration))
	var warmupDur time.Duration
	if strings.TrimSpace(cfg.WarmupDuration) != "" {
		warmupDur, _ = time.ParseDuration(strings.TrimSpace(cfg.WarmupDuration))
	}

	stages := make([]storage.LoadStage, 0, len(cfg.Levels)*2)
	var total time.Duration
	for idx, lvl := range cfg.Levels {
		if warmupDur > 0 {
			warmStage := storage.LoadStage{
				Name:     fmt.Sprintf("Level %d warmup (%g)", idx+1, lvl),
				Type:     "hold",
				Duration: warmupDur.String(),
				Warmup:   true,
			}
			if mode == "rate" {
				tps := lvl
				warmStage.TargetTPS = &tps
			} else {
				warmStage.TargetVUsers = int(math.Round(lvl))
			}
			stages = append(stages, warmStage)
			total += warmupDur
		}
		measureStage := storage.LoadStage{
			Name:     fmt.Sprintf("Level %d (%g)", idx+1, lvl),
			Type:     "hold",
			Duration: measureDur.String(),
			Warmup:   false,
		}
		if mode == "rate" {
			tps := lvl
			measureStage.TargetTPS = &tps
		} else {
			measureStage.TargetVUsers = int(math.Round(lvl))
		}
		stages = append(stages, measureStage)
		total += measureDur
	}
	return stages, total, nil
}
