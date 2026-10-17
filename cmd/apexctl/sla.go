package main

import (
	"fmt"
	"sort"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/runcontract"
)

type SLASummary struct {
	Evaluated              bool                                   `json:"evaluated"`
	Breached               bool                                   `json:"breached"`
	Details                []string                               `json:"details,omitempty"`
	TransactionRuleResults []runcontract.TransactionSLARuleResult `json:"transaction_rule_results,omitempty"`
}

func resolveGoalConfig(script *dsl.Script, args runArgs) *runcontract.GoalConfig {
	return mergeGoalConfigs(goalConfigFromScript(script), buildGoalConfigFromArgs(args))
}

func buildGoalConfigFromArgs(args runArgs) *runcontract.GoalConfig {
	if args.p95LatencyMs == nil && args.maxErrorPct == nil && args.maxAvgServerProcessingMs == nil && args.maxAvgTransferMs == nil && args.maxErrorCount == nil && len(args.transactionRules) == 0 {
		return nil
	}
	return &runcontract.GoalConfig{
		P95LatencyMs:             args.p95LatencyMs,
		MaxErrorPct:              args.maxErrorPct,
		MaxAvgServerProcessingMs: args.maxAvgServerProcessingMs,
		MaxAvgTransferMs:         args.maxAvgTransferMs,
		MaxErrorCount:            args.maxErrorCount,
		TransactionRules:         append([]runcontract.TransactionSLARule(nil), args.transactionRules...),
	}
}

func goalConfigFromScript(script *dsl.Script) *runcontract.GoalConfig {
	if script == nil || script.GoalConfig == nil {
		return nil
	}
	goal := script.GoalConfig
	resolved := &runcontract.GoalConfig{
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
		TransactionRules:         convertDSLTransactionSLARules(goal.TransactionRules),
	}
	if !hasGoalConfig(resolved) {
		return nil
	}
	return resolved
}

func mergeGoalConfigs(base, override *runcontract.GoalConfig) *runcontract.GoalConfig {
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
	if len(override.TransactionRules) > 0 {
		merged.TransactionRules = append([]runcontract.TransactionSLARule(nil), override.TransactionRules...)
	}
	return merged
}

func hasGoalConfig(goal *runcontract.GoalConfig) bool {
	return goal != nil && (goal.TargetAverageTPS != nil || goal.P95LatencyMs != nil || goal.MaxErrorPct != nil || goal.MaxAvgServerProcessingMs != nil || goal.MaxAvgTransferMs != nil || goal.MaxErrorCount != nil || goal.MinVUsers != nil || goal.MaxVUsers != nil || len(goal.TransactionRules) > 0 || goal.StopOnSLABreach || goal.StopOnMaxErrors)
}

func cloneGoalConfig(goal *runcontract.GoalConfig) *runcontract.GoalConfig {
	if goal == nil {
		return nil
	}
	return &runcontract.GoalConfig{
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
		TransactionRules:         append([]runcontract.TransactionSLARule(nil), goal.TransactionRules...),
	}
}

func convertDSLTransactionSLARules(rules []dsl.TransactionSLARule) []runcontract.TransactionSLARule {
	if len(rules) == 0 {
		return nil
	}
	converted := make([]runcontract.TransactionSLARule, len(rules))
	for index, rule := range rules {
		converted[index] = runcontract.TransactionSLARule{
			Transaction: rule.Transaction,
			Population:  rule.Population, Protocol: rule.Protocol, Group: rule.Group,
			Metric:     rule.Metric,
			Comparator: rule.Comparator,
			Threshold:  rule.Threshold,
			Severity:   rule.Severity,
			Iterations: rule.Iterations,
		}
	}
	return converted
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

func evaluateLocalSLA(result *engine.RunResult, goalConfig *runcontract.GoalConfig) *SLASummary {
	if result == nil || goalConfig == nil {
		return nil
	}
	summary := &SLASummary{}
	configured := 0
	evaluated := 0

	if goalConfig.P95LatencyMs != nil {
		configured++
		if result.TotalTransactions > 0 {
			evaluated++
			p95Ms := float64(result.FinalSnapshot.P95) / float64(time.Millisecond)
			if p95Ms > *goalConfig.P95LatencyMs {
				summary.Details = append(summary.Details, fmt.Sprintf("P95 latency %.1fms exceeded %.1fms", p95Ms, *goalConfig.P95LatencyMs))
			}
		}
	}
	if goalConfig.MaxErrorPct != nil {
		configured++
		attempts := result.TotalTransactions + result.TotalErrors
		if attempts > 0 {
			evaluated++
			errorPct := float64(result.TotalErrors) * 100 / float64(attempts)
			if errorPct > *goalConfig.MaxErrorPct {
				summary.Details = append(summary.Details, fmt.Sprintf("Error rate %.2f%% exceeded %.2f%%", errorPct, *goalConfig.MaxErrorPct))
			}
		}
	}
	if goalConfig.MaxErrorCount != nil {
		configured++
		evaluated++
		if result.TotalErrors > int64(*goalConfig.MaxErrorCount) {
			summary.Details = append(summary.Details, fmt.Sprintf("Error count %d exceeded %d", result.TotalErrors, *goalConfig.MaxErrorCount))
		}
	}
	if goalConfig.MaxAvgServerProcessingMs != nil {
		configured++
		if actionName, value, ok := maxServerTelemetryMetric(result.FinalSnapshot.ByAction, func(metrics *engine.ServerTelemetrySummary) float64 {
			return metrics.AvgServerProcessingMs
		}); ok {
			evaluated++
			if value > *goalConfig.MaxAvgServerProcessingMs {
				summary.Details = append(summary.Details, fmt.Sprintf("Server processing %.1fms exceeded %.1fms on %s", value, *goalConfig.MaxAvgServerProcessingMs, actionName))
			}
		}
	}
	if goalConfig.MaxAvgTransferMs != nil {
		configured++
		if actionName, value, ok := maxServerTelemetryMetric(result.FinalSnapshot.ByAction, func(metrics *engine.ServerTelemetrySummary) float64 {
			return metrics.AvgTransferMs
		}); ok {
			evaluated++
			if value > *goalConfig.MaxAvgTransferMs {
				summary.Details = append(summary.Details, fmt.Sprintf("Transfer time %.1fms exceeded %.1fms on %s", value, *goalConfig.MaxAvgTransferMs, actionName))
			}
		}
	}
	summary.Evaluated = configured > 0 && configured == evaluated
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
		if metrics == nil || metrics.ServerTelemetry == nil || metrics.ServerTelemetry.Samples <= 0 {
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
