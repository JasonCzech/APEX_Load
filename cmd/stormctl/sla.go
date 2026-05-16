package main

import (
	"fmt"
	"sort"
	"time"

	"github.com/jasonczech/stormrunner/pkg/api"
	"github.com/jasonczech/stormrunner/pkg/engine"
)

type SLASummary struct {
	Evaluated bool     `json:"evaluated"`
	Breached  bool     `json:"breached"`
	Details   []string `json:"details,omitempty"`
}

func buildGoalConfigFromArgs(args runArgs) *api.GoalConfig {
	if args.p95LatencyMs == nil && args.maxErrorPct == nil && args.maxAvgServerProcessingMs == nil && args.maxAvgTransferMs == nil && args.maxErrorCount == nil {
		return nil
	}
	return &api.GoalConfig{
		P95LatencyMs:             args.p95LatencyMs,
		MaxErrorPct:              args.maxErrorPct,
		MaxAvgServerProcessingMs: args.maxAvgServerProcessingMs,
		MaxAvgTransferMs:         args.maxAvgTransferMs,
		MaxErrorCount:            args.maxErrorCount,
	}
}

func evaluateLocalSLA(result *engine.RunResult, goalConfig *api.GoalConfig) *SLASummary {
	if result == nil || goalConfig == nil {
		return nil
	}
	summary := &SLASummary{Evaluated: true}

	if goalConfig.P95LatencyMs != nil {
		p95Ms := float64(result.FinalSnapshot.P95) / float64(time.Millisecond)
		if p95Ms > *goalConfig.P95LatencyMs {
			summary.Details = append(summary.Details, fmt.Sprintf("P95 latency %.1fms exceeded %.1fms", p95Ms, *goalConfig.P95LatencyMs))
		}
	}
	if goalConfig.MaxErrorPct != nil && result.TotalTransactions > 0 {
		errorPct := float64(result.TotalErrors) * 100 / float64(result.TotalTransactions)
		if errorPct > *goalConfig.MaxErrorPct {
			summary.Details = append(summary.Details, fmt.Sprintf("Error rate %.2f%% exceeded %.2f%%", errorPct, *goalConfig.MaxErrorPct))
		}
	}
	if goalConfig.MaxErrorCount != nil && result.TotalErrors > int64(*goalConfig.MaxErrorCount) {
		summary.Details = append(summary.Details, fmt.Sprintf("Error count %d exceeded %d", result.TotalErrors, *goalConfig.MaxErrorCount))
	}
	if goalConfig.MaxAvgServerProcessingMs != nil {
		if actionName, value, ok := maxServerTelemetryMetric(result.FinalSnapshot.ByAction, func(metrics *engine.ServerTelemetrySummary) float64 {
			return metrics.AvgServerProcessingMs
		}); ok && value > *goalConfig.MaxAvgServerProcessingMs {
			summary.Details = append(summary.Details, fmt.Sprintf("Server processing %.1fms exceeded %.1fms on %s", value, *goalConfig.MaxAvgServerProcessingMs, actionName))
		}
	}
	if goalConfig.MaxAvgTransferMs != nil {
		if actionName, value, ok := maxServerTelemetryMetric(result.FinalSnapshot.ByAction, func(metrics *engine.ServerTelemetrySummary) float64 {
			return metrics.AvgTransferMs
		}); ok && value > *goalConfig.MaxAvgTransferMs {
			summary.Details = append(summary.Details, fmt.Sprintf("Transfer time %.1fms exceeded %.1fms on %s", value, *goalConfig.MaxAvgTransferMs, actionName))
		}
	}
	summary.Breached = len(summary.Details) > 0
	return summary
}

func maxServerTelemetryMetric(byAction map[string]*engine.ActionMetrics, selector func(*engine.ServerTelemetrySummary) float64) (string, float64, bool) {
	if len(byAction) == 0 {
		return "", 0, false
	}
	actionNames := make([]string, 0, len(byAction))
	for name := range byAction {
		actionNames = append(actionNames, name)
	}
	sort.Strings(actionNames)
	bestAction := ""
	bestValue := 0.0
	found := false
	for _, name := range actionNames {
		metrics := byAction[name]
		if metrics == nil || metrics.ServerTelemetry == nil {
			continue
		}
		value := selector(metrics.ServerTelemetry)
		if !found || value > bestValue {
			bestAction = name
			bestValue = value
			found = true
		}
	}
	return bestAction, bestValue, found
}
