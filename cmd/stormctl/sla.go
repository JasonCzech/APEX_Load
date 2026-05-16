package main

import (
	"fmt"
	"sort"
	"time"

	"github.com/jasonczech/stormrunner/pkg/api"
	"github.com/jasonczech/stormrunner/pkg/dsl"
	"github.com/jasonczech/stormrunner/pkg/engine"
)

type SLASummary struct {
	Evaluated bool     `json:"evaluated"`
	Breached  bool     `json:"breached"`
	Details   []string `json:"details,omitempty"`
}

func resolveGoalConfig(script *dsl.Script, args runArgs) *api.GoalConfig {
	return mergeGoalConfigs(goalConfigFromScript(script), buildGoalConfigFromArgs(args))
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

func goalConfigFromScript(script *dsl.Script) *api.GoalConfig {
	if script == nil || script.GoalConfig == nil {
		return nil
	}
	goal := script.GoalConfig
	resolved := &api.GoalConfig{
		TargetAverageTPS:         cloneFloat64(goal.TargetAverageTPS),
		P95LatencyMs:             cloneFloat64(goal.P95LatencyMs),
		MaxErrorPct:              cloneFloat64(goal.MaxErrorPct),
		MaxAvgServerProcessingMs: cloneFloat64(goal.MaxAvgServerProcessingMs),
		MaxAvgTransferMs:         cloneFloat64(goal.MaxAvgTransferMs),
		MaxErrorCount:            cloneInt(goal.MaxErrorCount),
		MinVUsers:                cloneInt(goal.MinVUsers),
		MaxVUsers:                cloneInt(goal.MaxVUsers),
		StopOnSLABreach:          goal.StopOnSLABreach,
		StopOnMaxErrors:          goal.StopOnMaxErrors,
		SLABreachWindow:          goal.SLABreachWindow,
	}
	if !hasGoalConfig(resolved) {
		return nil
	}
	return resolved
}

func mergeGoalConfigs(base, override *api.GoalConfig) *api.GoalConfig {
	if !hasGoalConfig(base) && !hasGoalConfig(override) {
		return nil
	}
	if !hasGoalConfig(base) {
		return cloneGoalConfig(override)
	}
	if !hasGoalConfig(override) {
		return cloneGoalConfig(base)
	}

	merged := cloneGoalConfig(base)
	merged.P95LatencyMs = firstFloat64(override.P95LatencyMs, merged.P95LatencyMs)
	merged.MaxErrorPct = firstFloat64(override.MaxErrorPct, merged.MaxErrorPct)
	merged.MaxAvgServerProcessingMs = firstFloat64(override.MaxAvgServerProcessingMs, merged.MaxAvgServerProcessingMs)
	merged.MaxAvgTransferMs = firstFloat64(override.MaxAvgTransferMs, merged.MaxAvgTransferMs)
	merged.MaxErrorCount = firstInt(override.MaxErrorCount, merged.MaxErrorCount)
	return merged
}

func hasGoalConfig(goal *api.GoalConfig) bool {
	return goal != nil && (goal.TargetAverageTPS != nil || goal.P95LatencyMs != nil || goal.MaxErrorPct != nil || goal.MaxAvgServerProcessingMs != nil || goal.MaxAvgTransferMs != nil || goal.MaxErrorCount != nil || goal.MinVUsers != nil || goal.MaxVUsers != nil || goal.StopOnSLABreach || goal.StopOnMaxErrors || goal.SLABreachWindow != "")
}

func cloneGoalConfig(goal *api.GoalConfig) *api.GoalConfig {
	if goal == nil {
		return nil
	}
	return &api.GoalConfig{
		TargetAverageTPS:         cloneFloat64(goal.TargetAverageTPS),
		P95LatencyMs:             cloneFloat64(goal.P95LatencyMs),
		MaxErrorPct:              cloneFloat64(goal.MaxErrorPct),
		MaxAvgServerProcessingMs: cloneFloat64(goal.MaxAvgServerProcessingMs),
		MaxAvgTransferMs:         cloneFloat64(goal.MaxAvgTransferMs),
		MaxErrorCount:            cloneInt(goal.MaxErrorCount),
		MinVUsers:                cloneInt(goal.MinVUsers),
		MaxVUsers:                cloneInt(goal.MaxVUsers),
		StopOnSLABreach:          goal.StopOnSLABreach,
		StopOnMaxErrors:          goal.StopOnMaxErrors,
		SLABreachWindow:          goal.SLABreachWindow,
	}
}

func cloneFloat64(value *float64) *float64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func firstFloat64(primary, fallback *float64) *float64 {
	if primary != nil {
		return cloneFloat64(primary)
	}
	return cloneFloat64(fallback)
}

func firstInt(primary, fallback *int) *int {
	if primary != nil {
		return cloneInt(primary)
	}
	return cloneInt(fallback)
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
