package engine

import (
	"context"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func isOpenWorkload(script *dsl.Script) bool {
	if script == nil {
		return false
	}
	return strings.EqualFold(script.Config.RunLogic.WorkloadModel, "open") && script.Config.Pacing.Type == "tps" && script.Config.Pacing.TPS > 0
}

func openWorkloadArrivalLimit(iterationsPerWorker, workers int) (int, bool) {
	if iterationsPerWorker <= 0 || workers <= 0 {
		return 0, false
	}
	return iterationsPerWorker * workers, true
}

func cloneSingleIterationScript(script *dsl.Script) *dsl.Script {
	if script == nil {
		return nil
	}
	clone := *script
	clone.Config = script.Config
	clone.Config.RunLogic = script.Config.RunLogic
	clone.Config.Pacing = dsl.PacingConfig{}
	clone.Config.RunLogic.Iterations = 1
	return &clone
}

func runArrivalScheduler(ctx context.Context, tps float64, totalArrivals int, waitIfPaused func(context.Context) error, dispatch func(arrivalIndex int)) {
	if tps <= 0 {
		return
	}
	interval := time.Duration(float64(time.Second) / tps)
	if interval <= 0 {
		interval = time.Nanosecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for arrival := 0; totalArrivals <= 0 || arrival < totalArrivals; arrival++ {
		if arrival > 0 {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
		if waitIfPaused != nil {
			if err := waitIfPaused(ctx); err != nil {
				return
			}
		}
		if ctx.Err() != nil {
			return
		}
		dispatch(arrival)
	}
}
