package api

import (
	"math"
	"time"
)

func cloneGoalConfig(config *GoalConfig) *GoalConfig {
	if config == nil {
		return nil
	}
	clone := *config
	if config.TargetAverageTPS != nil {
		value := *config.TargetAverageTPS
		clone.TargetAverageTPS = &value
	}
	if config.P95LatencyMs != nil {
		value := *config.P95LatencyMs
		clone.P95LatencyMs = &value
	}
	if config.MaxErrorPct != nil {
		value := *config.MaxErrorPct
		clone.MaxErrorPct = &value
	}
	if config.MaxAvgServerProcessingMs != nil {
		value := *config.MaxAvgServerProcessingMs
		clone.MaxAvgServerProcessingMs = &value
	}
	if config.MaxAvgTransferMs != nil {
		value := *config.MaxAvgTransferMs
		clone.MaxAvgTransferMs = &value
	}
	if config.MaxErrorCount != nil {
		value := *config.MaxErrorCount
		clone.MaxErrorCount = &value
	}
	if config.MinVUsers != nil {
		value := *config.MinVUsers
		clone.MinVUsers = &value
	}
	if config.MaxVUsers != nil {
		value := *config.MaxVUsers
		clone.MaxVUsers = &value
	}
	return &clone
}

func normalizeScenarioMode(mode string) string {
	if mode == "goal_oriented" {
		return mode
	}
	return "vuser_driven"
}

func hasGoalConfig(config *GoalConfig) bool {
	if config == nil {
		return false
	}
	return config.TargetAverageTPS != nil ||
		config.P95LatencyMs != nil ||
		config.MaxErrorPct != nil ||
		config.MaxAvgServerProcessingMs != nil ||
		config.MaxAvgTransferMs != nil ||
		config.MaxErrorCount != nil ||
		config.MinVUsers != nil ||
		config.MaxVUsers != nil ||
		config.StopOnSLABreach ||
		config.StopOnMaxErrors ||
		config.SLABreachWindow != ""
}

func evaluateGoalController(now time.Time, currentTotalVUsers int, live *LiveMetrics, config *GoalConfig, state *goalControllerState) goalControllerUpdate {
	status := &GoalStatus{
		Mode:                 "goal_oriented",
		CurrentTPS:           live.TPS,
		CurrentTotalVUsers:   currentTotalVUsers,
		TargetAverageTPS:     config.TargetAverageTPS,
		MaxErrorCount:        config.MaxErrorCount,
		MinVUsers:            config.MinVUsers,
		MaxVUsers:            config.MaxVUsers,
		SuggestedTotalVUsers: currentTotalVUsers,
	}
	if state != nil {
		status.LastScaleAt = state.lastScaleAt
		status.LastScaleDirection = state.lastScaleDirection
		status.LastScaleReason = state.lastScaleReason
	}

	latencyBreached := config.P95LatencyMs != nil && live.P95Ms > *config.P95LatencyMs
	errorBreached := config.MaxErrorPct != nil && live.ErrorPct > *config.MaxErrorPct
	serverProcessingBreached := hasServerTelemetryBreach(live, config.MaxAvgServerProcessingMs, func(summary *ServerTelemetrySummary) float64 {
		return summary.AvgServerProcessingMs
	})
	transferBreached := hasServerTelemetryBreach(live, config.MaxAvgTransferMs, func(summary *ServerTelemetrySummary) float64 {
		return summary.AvgTransferMs
	})
	errorCountBreached := config.MaxErrorCount != nil && live.TotalErrors > int64(*config.MaxErrorCount)
	status.LatencyBreached = latencyBreached
	status.ErrorBreached = errorBreached
	status.ServerProcessingBreached = serverProcessingBreached
	status.TransferBreached = transferBreached
	status.ErrorCountBreached = errorCountBreached
	status.BreachActive = latencyBreached || errorBreached || serverProcessingBreached || transferBreached

	if status.BreachActive {
		if state != nil && state.breachStartedAt == nil {
			started := now
			state.breachStartedAt = &started
		}
		if state != nil && state.breachStartedAt != nil {
			status.BreachDurationSec = int(now.Sub(*state.breachStartedAt).Seconds())
		}
	} else if state != nil {
		state.breachStartedAt = nil
	}

	if config.StopOnMaxErrors && errorCountBreached {
		status.StopTriggered = true
		status.StopReason = goalStopReason(latencyBreached, errorBreached, serverProcessingBreached, transferBreached, errorCountBreached)
		return goalControllerUpdate{
			Status:     status,
			ShouldStop: true,
			StopReason: status.StopReason,
		}
	}

	if config.StopOnSLABreach && status.BreachActive {
		window := goalBreachWindow(config)
		if time.Duration(status.BreachDurationSec)*time.Second >= window {
			status.StopTriggered = true
			status.StopReason = goalStopReason(latencyBreached, errorBreached, serverProcessingBreached, transferBreached, errorCountBreached)
			return goalControllerUpdate{
				Status:     status,
				ShouldStop: true,
				StopReason: status.StopReason,
			}
		}
	}

	if config.TargetAverageTPS != nil && shouldEvaluateScale(now, state) {
		minTPS := *config.TargetAverageTPS * 0.95
		maxTPS := *config.TargetAverageTPS * 1.05
		desiredTotal := currentTotalVUsers
		scaleDirection := ""
		scaleReason := ""

		if live.TPS < minTPS {
			deltaRatio := (*config.TargetAverageTPS - live.TPS) / math.Max(*config.TargetAverageTPS, 1)
			step := goalScaleStep(currentTotalVUsers, deltaRatio)
			desiredTotal = currentTotalVUsers + step
			scaleDirection = "up"
			scaleReason = "below_target_tps"
		} else if live.TPS > maxTPS {
			deltaRatio := (live.TPS - *config.TargetAverageTPS) / math.Max(live.TPS, 1)
			step := goalScaleStep(currentTotalVUsers, deltaRatio)
			desiredTotal = currentTotalVUsers - step
			scaleDirection = "down"
			scaleReason = "above_target_tps"
		}

		desiredTotal = clampGoalVUsers(desiredTotal, config)
		status.SuggestedTotalVUsers = desiredTotal
		if desiredTotal != currentTotalVUsers {
			return goalControllerUpdate{
				Status:         status,
				ShouldScale:    true,
				TargetTotal:    desiredTotal,
				ScaleDirection: scaleDirection,
				ScaleReason:    scaleReason,
			}
		}
	}

	return goalControllerUpdate{Status: status}
}

func currentGoalTargetStatuses(test *ManagedTest, now time.Time) map[string]GoalTargetStatus {
	if test == nil || len(test.Groups) == 0 {
		return nil
	}

	stagedTargets := stagedGoalTargetsByGroup(test.Groups)
	liveTargets := liveGoalTargetsByGroup(test.LiveMetrics)
	defaultTargets := defaultGoalTargetsByGroup(test)

	targets := make(map[string]GoalTargetStatus, len(test.Groups))
	for _, group := range test.Groups {
		name := group.Name
		target := 0.0
		updatedAt := now
		if existing, ok := test.GoalTargets[name]; ok && existing.TargetTPS > 0 {
			target = existing.TargetTPS
			if !existing.UpdatedAt.IsZero() {
				updatedAt = existing.UpdatedAt
			}
		} else if stagedTargets[name] > 0 {
			target = stagedTargets[name]
		} else if liveTargets[name] > 0 {
			target = liveTargets[name]
		} else if defaultTargets[name] > 0 {
			target = defaultTargets[name]
		}
		if target <= 0 {
			continue
		}
		targets[name] = GoalTargetStatus{
			Group:     name,
			TargetTPS: target,
			UpdatedAt: updatedAt,
		}
	}
	if len(targets) == 0 {
		return nil
	}
	return targets
}

func stagedGoalTargetsByGroup(groups []TestGroup) map[string]float64 {
	targets := make(map[string]float64, len(groups))
	for _, group := range groups {
		for _, stage := range group.Stages {
			if stage.TargetTPS != nil && *stage.TargetTPS > targets[group.Name] {
				targets[group.Name] = *stage.TargetTPS
			}
		}
	}
	return targets
}

func liveGoalTargetsByGroup(live *LiveMetrics) map[string]float64 {
	if live == nil || len(live.GroupMetrics) == 0 {
		return nil
	}
	targets := make(map[string]float64, len(live.GroupMetrics))
	for name, metrics := range live.GroupMetrics {
		if metrics != nil && metrics.TargetTPS > 0 {
			targets[name] = metrics.TargetTPS
		}
	}
	return targets
}

func defaultGoalTargetsByGroup(test *ManagedTest) map[string]float64 {
	if test == nil || len(test.Groups) == 0 || test.GoalConfig == nil || test.GoalConfig.TargetAverageTPS == nil || *test.GoalConfig.TargetAverageTPS <= 0 {
		return nil
	}
	aggregate := *test.GoalConfig.TargetAverageTPS
	targets := make(map[string]float64, len(test.Groups))

	totalVUsers := 0
	for _, group := range test.Groups {
		totalVUsers += group.VUsers
	}
	if totalVUsers <= 0 {
		share := aggregate / float64(len(test.Groups))
		for _, group := range test.Groups {
			targets[group.Name] = share
		}
		return targets
	}

	for _, group := range test.Groups {
		targets[group.Name] = aggregate * float64(group.VUsers) / float64(totalVUsers)
	}
	return targets
}

func sumGoalTargetTPS(targets map[string]GoalTargetStatus) float64 {
	total := 0.0
	for _, target := range targets {
		if target.TargetTPS > 0 {
			total += target.TargetTPS
		}
	}
	return total
}

func liveGroupVUsers(test *ManagedTest, live *LiveMetrics) map[string]int {
	groupVUsers := make(map[string]int)
	if test != nil {
		for _, group := range test.Groups {
			groupVUsers[group.Name] = group.VUsers
		}
	}
	if live != nil {
		for groupName, metrics := range live.GroupMetrics {
			if metrics != nil {
				groupVUsers[groupName] = metrics.VUsers
			}
		}
	}
	return groupVUsers
}

func applyRuntimeGoalTargetsToLiveMetrics(test *ManagedTest, live *LiveMetrics) {
	if test == nil || live == nil || test.ScenarioMode != "goal_oriented" {
		return
	}
	targets := currentGoalTargetStatuses(test, live.Timestamp)
	totalTargetTPS := sumGoalTargetTPS(targets)
	if totalTargetTPS <= 0 {
		return
	}

	live.TargetTPS = totalTargetTPS
	live.ThroughputGapPct, live.ThroughputTargetMissed = throughputGap(live.TPS, totalTargetTPS)
	if live.GroupMetrics == nil {
		live.GroupMetrics = make(map[string]*GroupLiveMetrics)
	}
	for _, group := range test.Groups {
		target, ok := targets[group.Name]
		if !ok || target.TargetTPS <= 0 {
			continue
		}
		metrics := live.GroupMetrics[group.Name]
		if metrics == nil {
			metrics = &GroupLiveMetrics{Name: group.Name, VUsers: group.VUsers}
			if len(test.Groups) == 1 {
				metrics.Transactions = live.TotalTx
				metrics.Errors = live.TotalErrors
				metrics.TPS = live.TPS
				metrics.AvgResponseMs = live.AvgResponseMs
				metrics.P50Ms = live.P50Ms
				metrics.P90Ms = live.P90Ms
				metrics.P95Ms = live.P95Ms
				metrics.P99Ms = live.P99Ms
			}
			live.GroupMetrics[group.Name] = metrics
		}
		metrics.TargetTPS = target.TargetTPS
		metrics.ThroughputGapPct, metrics.ThroughputTargetMissed = throughputGap(metrics.TPS, target.TargetTPS)
	}
	if live.GoalStatus != nil {
		targetAverage := totalTargetTPS
		live.GoalStatus.TargetAverageTPS = &targetAverage
	}
}

func throughputGap(actualTPS, targetTPS float64) (float64, bool) {
	if targetTPS <= 0 || actualTPS >= targetTPS {
		return 0, false
	}
	gap := (targetTPS - actualTPS) / targetTPS * 100
	return gap, gap >= 5.0
}

func hasServerTelemetryBreach(live *LiveMetrics, threshold *float64, pickValue func(summary *ServerTelemetrySummary) float64) bool {
	if live == nil || threshold == nil || len(live.Transactions) == 0 {
		return false
	}
	for _, tx := range live.Transactions {
		if tx == nil || tx.ServerTelemetry == nil {
			continue
		}
		if pickValue(tx.ServerTelemetry) > *threshold {
			return true
		}
	}
	return false
}

func shouldEvaluateScale(now time.Time, state *goalControllerState) bool {
	if state == nil || state.lastScaleAt == nil {
		return true
	}
	return now.Sub(*state.lastScaleAt) >= 5*time.Second
}

func goalScaleStep(currentTotalVUsers int, deltaRatio float64) int {
	base := currentTotalVUsers
	if base < 1 {
		base = 1
	}
	step := int(math.Ceil(float64(base) * math.Min(math.Max(deltaRatio, 0.05), 0.25)))
	if step < 1 {
		return 1
	}
	return step
}

func clampGoalVUsers(value int, config *GoalConfig) int {
	if config == nil {
		if value < 0 {
			return 0
		}
		return value
	}
	if config.MinVUsers != nil && value < *config.MinVUsers {
		value = *config.MinVUsers
	}
	if config.MaxVUsers != nil && value > *config.MaxVUsers {
		value = *config.MaxVUsers
	}
	if value < 0 {
		return 0
	}
	return value
}

func goalBreachWindow(config *GoalConfig) time.Duration {
	if config == nil || config.SLABreachWindow == "" {
		return 30 * time.Second
	}
	window, err := time.ParseDuration(config.SLABreachWindow)
	if err != nil || window <= 0 {
		return 30 * time.Second
	}
	return window
}

func goalStopReason(latencyBreached bool, errorBreached bool, serverProcessingBreached bool, transferBreached bool, errorCountBreached bool) string {
	switch {
	case errorCountBreached:
		return "max error count reached"
	case latencyBreached && errorBreached:
		return "latency and error SLA breached"
	case serverProcessingBreached && transferBreached:
		return "server processing and transfer SLA breached"
	case latencyBreached:
		return "p95 latency SLA breached"
	case errorBreached:
		return "error rate SLA breached"
	case serverProcessingBreached:
		return "server processing SLA breached"
	case transferBreached:
		return "transfer SLA breached"
	default:
		return "SLA breached"
	}
}

func totalGroupVUsers(groupVUsers map[string]int) int {
	total := 0
	for _, vusers := range groupVUsers {
		total += vusers
	}
	return total
}

func distributeTargetVUsers(groups []TestGroup, current map[string]int, desiredTotal int) map[string]int {
	if len(groups) == 0 {
		return nil
	}
	if desiredTotal < 0 {
		desiredTotal = 0
	}

	totalCurrent := 0
	for _, group := range groups {
		totalCurrent += current[group.Name]
	}

	targets := make(map[string]int, len(groups))
	if totalCurrent <= 0 {
		remaining := desiredTotal
		for idx, group := range groups {
			share := 0
			if len(groups)-idx > 0 {
				share = remaining / (len(groups) - idx)
			}
			targets[group.Name] = share
			remaining -= share
		}
		return targets
	}

	type allocation struct {
		name      string
		base      int
		remainder float64
	}
	allocations := make([]allocation, 0, len(groups))
	assigned := 0
	for _, group := range groups {
		exact := float64(desiredTotal) * float64(current[group.Name]) / float64(totalCurrent)
		base := int(math.Floor(exact))
		allocations = append(allocations, allocation{name: group.Name, base: base, remainder: exact - float64(base)})
		assigned += base
	}

	remaining := desiredTotal - assigned
	for i := 0; i < len(allocations) && remaining > 0; i++ {
		bestIdx := i
		for j := i + 1; j < len(allocations); j++ {
			if allocations[j].remainder > allocations[bestIdx].remainder {
				bestIdx = j
			}
		}
		allocations[i], allocations[bestIdx] = allocations[bestIdx], allocations[i]
		allocations[i].base++
		remaining--
	}

	for _, allocation := range allocations {
		targets[allocation.name] = allocation.base
	}
	return targets
}

func distributeTargetVUsersForGoal(groups []TestGroup, current map[string]int, desiredTotal int, goalTargets map[string]GoalTargetStatus) map[string]int {
	weighted := distributeTargetVUsersByWeights(groups, desiredTotal, goalTargets)
	if len(weighted) > 0 {
		return weighted
	}
	return distributeTargetVUsers(groups, current, desiredTotal)
}

func distributeTargetVUsersByWeights(groups []TestGroup, desiredTotal int, goalTargets map[string]GoalTargetStatus) map[string]int {
	if len(groups) == 0 || desiredTotal < 0 || len(goalTargets) == 0 {
		return nil
	}
	totalWeight := 0.0
	for _, group := range groups {
		target := goalTargets[group.Name]
		if target.TargetTPS > 0 && !math.IsNaN(target.TargetTPS) && !math.IsInf(target.TargetTPS, 0) {
			totalWeight += target.TargetTPS
		}
	}
	if totalWeight <= 0 {
		return nil
	}

	type allocation struct {
		name      string
		base      int
		remainder float64
	}
	allocations := make([]allocation, 0, len(groups))
	assigned := 0
	for _, group := range groups {
		target := goalTargets[group.Name]
		exact := 0.0
		if target.TargetTPS > 0 {
			exact = float64(desiredTotal) * target.TargetTPS / totalWeight
		}
		base := int(math.Floor(exact))
		allocations = append(allocations, allocation{name: group.Name, base: base, remainder: exact - float64(base)})
		assigned += base
	}

	remaining := desiredTotal - assigned
	for i := 0; i < len(allocations) && remaining > 0; i++ {
		bestIdx := i
		for j := i + 1; j < len(allocations); j++ {
			if allocations[j].remainder > allocations[bestIdx].remainder {
				bestIdx = j
			}
		}
		allocations[i], allocations[bestIdx] = allocations[bestIdx], allocations[i]
		allocations[i].base++
		remaining--
	}

	targets := make(map[string]int, len(groups))
	for _, allocation := range allocations {
		targets[allocation.name] = allocation.base
	}
	return targets
}
