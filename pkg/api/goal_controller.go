package api

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// Group roles. Load groups generate protocol traffic and participate in
// goal-based scaling; observer groups are small browser experience canaries
// that continuously report Web Vitals and are pinned during scaling.
const (
	GroupRoleLoad     = "load"
	GroupRoleObserver = "observer"
)

const (
	// observerDefaultVUsers matches the recommended diagnostic canary density
	// per generator (see BrowserBenchmarkDensityEnvelope).
	observerDefaultVUsers = 2
	// observerMaxVUsers caps an observer group at the diagnostic envelope —
	// beyond this it is a browser load group and must carry the load role
	// (and its benchmark-evidence obligations).
	observerMaxVUsers = 10
)

func normalizeGroupRole(role string, protocol string, index int) (string, error) {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "", GroupRoleLoad:
		return GroupRoleLoad, nil
	case GroupRoleObserver, "canary":
		if protocol != "browser" {
			return "", fmt.Errorf("group[%d]: observer groups require a browser-protocol script (got %s); experience canaries observe through a real browser", index, protocol)
		}
		return GroupRoleObserver, nil
	default:
		return "", fmt.Errorf("group[%d]: role must be load or observer", index)
	}
}

func cloneGoalConfig(config *GoalConfig) *GoalConfig {
	if config == nil {
		return nil
	}
	clone := *config
	clone.TransactionRules = append([]TransactionSLARule(nil), config.TransactionRules...)
	if config.EarlyStop != nil {
		value := *config.EarlyStop
		clone.EarlyStop = &value
	}
	if config.WorkloadShapeConfig != nil {
		shape := *config.WorkloadShapeConfig
		clone.WorkloadShapeConfig = &shape
		if config.WorkloadShapeConfig.SteppedRamp != nil {
			stepped := *config.WorkloadShapeConfig.SteppedRamp
			shape.SteppedRamp = &stepped
			if stepped.Scenario != nil {
				definition := *stepped.Scenario
				stepped.Scenario = &definition
			}
			if stepped.Groups != nil {
				stepped.Groups = make(map[string]SteppedRampDefinition, len(stepped.Groups))
				for name, definition := range config.WorkloadShapeConfig.SteppedRamp.Groups {
					stepped.Groups[name] = definition
				}
			}
		}
		if config.WorkloadShapeConfig.ClosedVUser != nil {
			closed := *config.WorkloadShapeConfig.ClosedVUser
			shape.ClosedVUser = &closed
			if closed.Scenario != nil {
				definition := *closed.Scenario
				closed.Scenario = &definition
			}
			if closed.Groups != nil {
				closed.Groups = make(map[string]ClosedVUserDefinition, len(closed.Groups))
				for name, definition := range config.WorkloadShapeConfig.ClosedVUser.Groups {
					closed.Groups[name] = definition
				}
			}
		}
		if config.WorkloadShapeConfig.GoalDriven != nil {
			goalDriven := *config.WorkloadShapeConfig.GoalDriven
			shape.GoalDriven = &goalDriven
			if goalDriven.Groups != nil {
				goalDriven.Groups = make(map[string]GoalDrivenGroupDefinition, len(goalDriven.Groups))
				for name, definition := range config.WorkloadShapeConfig.GoalDriven.Groups {
					copyDefinition := definition
					if definition.ART != nil {
						art := *definition.ART
						copyDefinition.ART = &art
					}
					goalDriven.Groups[name] = copyDefinition
				}
			}
		}
	}
	if config.TargetAverageTPS != nil {
		value := *config.TargetAverageTPS
		clone.TargetAverageTPS = &value
	}
	if config.P95LatencyMs != nil {
		value := *config.P95LatencyMs
		clone.P95LatencyMs = &value
	}
	if config.TransactionResponseTimeSLAMs != nil {
		value := *config.TransactionResponseTimeSLAMs
		clone.TransactionResponseTimeSLAMs = &value
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
	if config.MaxLCPMs != nil {
		value := *config.MaxLCPMs
		clone.MaxLCPMs = &value
	}
	if config.MaxINPMs != nil {
		value := *config.MaxINPMs
		clone.MaxINPMs = &value
	}
	if config.MaxCLS != nil {
		value := *config.MaxCLS
		clone.MaxCLS = &value
	}
	if config.MaxThrottledRequests != nil {
		value := *config.MaxThrottledRequests
		clone.MaxThrottledRequests = &value
	}
	if config.MaxEndToEndLatencyMs != nil {
		value := *config.MaxEndToEndLatencyMs
		clone.MaxEndToEndLatencyMs = &value
	}
	if config.MaxPartitionSkewPct != nil {
		value := *config.MaxPartitionSkewPct
		clone.MaxPartitionSkewPct = &value
	}
	if config.MaxRedeliveryRatePct != nil {
		value := *config.MaxRedeliveryRatePct
		clone.MaxRedeliveryRatePct = &value
	}
	if config.MaxDeadLetterRatePct != nil {
		value := *config.MaxDeadLetterRatePct
		clone.MaxDeadLetterRatePct = &value
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
		config.TransactionResponseTimeSLAMs != nil ||
		config.MaxErrorPct != nil ||
		config.MaxAvgServerProcessingMs != nil ||
		config.MaxAvgTransferMs != nil ||
		config.MaxErrorCount != nil ||
		config.MinVUsers != nil ||
		config.MaxVUsers != nil ||
		config.MaxLCPMs != nil ||
		config.MaxINPMs != nil ||
		config.MaxCLS != nil ||
		config.MaxThrottledRequests != nil ||
		config.MaxEndToEndLatencyMs != nil ||
		config.MaxPartitionSkewPct != nil ||
		config.MaxRedeliveryRatePct != nil ||
		config.MaxDeadLetterRatePct != nil ||
		len(config.TransactionRules) > 0 ||
		config.StopOnSLABreach ||
		config.StopOnMaxErrors
}

func evaluateGoalController(now time.Time, currentTotalVUsers int, live *LiveMetrics, config *GoalConfig, state *goalControllerState) goalControllerUpdate {
	errorCount := live.TotalErrors
	if state != nil && state.errorWindowsTracked {
		errorCount = state.cumulativeErrors
	}
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

	breaches := goalBreachSet{
		latency: config.P95LatencyMs != nil && live.P95Ms > *config.P95LatencyMs,
		errors:  config.MaxErrorPct != nil && live.ErrorPct > *config.MaxErrorPct,
		serverProcessing: hasServerTelemetryBreach(live, config.MaxAvgServerProcessingMs, func(summary *ServerTelemetrySummary) float64 {
			return summary.AvgServerProcessingMs
		}),
		transfer: hasServerTelemetryBreach(live, config.MaxAvgTransferMs, func(summary *ServerTelemetrySummary) float64 {
			return summary.AvgTransferMs
		}),
		errorCount: config.MaxErrorCount != nil && errorCount > int64(*config.MaxErrorCount),
		lcp:        hasBrowserVitalsBreach(live, config.MaxLCPMs, func(browser *BrowserLiveMetrics) float64 { return browser.AvgLCPMs }),
		inp:        hasBrowserVitalsBreach(live, config.MaxINPMs, func(browser *BrowserLiveMetrics) float64 { return browser.AvgINPMs }),
		cls:        hasBrowserVitalsBreach(live, config.MaxCLS, func(browser *BrowserLiveMetrics) float64 { return browser.AvgCLSScore }),
		throttledRequests: config.MaxThrottledRequests != nil &&
			totalServerTelemetryThrottles(live) > int64(*config.MaxThrottledRequests),
		endToEndLatency: hasServerTelemetryBreach(live, config.MaxEndToEndLatencyMs, func(summary *ServerTelemetrySummary) float64 {
			return summary.AvgEndToEndLatencyMs
		}),
		partitionSkew: hasMessagingPartitionSkewBreach(live, config.MaxPartitionSkewPct),
		redeliveryRate: hasServerTelemetryBreach(live, config.MaxRedeliveryRatePct, func(summary *ServerTelemetrySummary) float64 {
			return summary.RedeliveryRatePct
		}),
		deadLetterRate: hasServerTelemetryBreach(live, config.MaxDeadLetterRatePct, func(summary *ServerTelemetrySummary) float64 {
			return summary.DeadLetterRatePct
		}),
	}
	status.LatencyBreached = breaches.latency
	status.ErrorBreached = breaches.errors
	status.ServerProcessingBreached = breaches.serverProcessing
	status.TransferBreached = breaches.transfer
	status.ErrorCountBreached = breaches.errorCount
	status.LCPBreached = breaches.lcp
	status.INPBreached = breaches.inp
	status.CLSBreached = breaches.cls
	status.ThrottledRequestsBreached = breaches.throttledRequests
	status.EndToEndLatencyBreached = breaches.endToEndLatency
	status.PartitionSkewBreached = breaches.partitionSkew
	status.RedeliveryRateBreached = breaches.redeliveryRate
	status.DeadLetterRateBreached = breaches.deadLetterRate
	status.BreachActive = breaches.latency || breaches.errors || breaches.serverProcessing || breaches.transfer ||
		breaches.lcp || breaches.inp || breaches.cls ||
		breaches.throttledRequests || breaches.endToEndLatency || breaches.partitionSkew ||
		breaches.redeliveryRate || breaches.deadLetterRate

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

	if config.StopOnMaxErrors && breaches.errorCount {
		status.StopTriggered = true
		status.StopReason = goalStopReason(breaches)
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
			status.StopReason = goalStopReason(breaches)
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

// recordGoalErrorWindow accumulates each metrics window once. Local engine
// snapshots and per-generator distributed batches report window counts, not
// run totals; comparing a single window to max_error_count lets sustained errors
// evade the stop condition. The source/timestamp key also prevents terminal or
// repeated distributed batches from double-counting a prior target's window.
func recordGoalErrorWindow(state *goalControllerState, source string, timestamp time.Time, errors int64) {
	if state == nil || errors < 0 || timestamp.IsZero() {
		return
	}
	if state.lastErrorWindowBySource == nil {
		state.lastErrorWindowBySource = make(map[string]time.Time)
	}
	if prior := state.lastErrorWindowBySource[source]; !prior.IsZero() && !timestamp.After(prior) {
		return
	}
	state.lastErrorWindowBySource[source] = timestamp
	state.cumulativeErrors += errors
	state.errorWindowsTracked = true
}

func currentGoalTargetStatuses(test *ManagedTest, now time.Time) map[string]GoalTargetStatus {
	if test == nil || len(test.Groups) == 0 {
		return nil
	}

	stagedTargets := stagedGoalTargetsByGroup(test.Groups)
	liveTargets := liveGoalTargetsByGroup(test.LiveMetrics)
	defaultTargets := defaultGoalTargetsByGroup(test)

	managedByProfile := map[string]struct{}(nil)
	if test.TrafficProfile != nil && test.TrafficProfile.RuntimeTarget == "tps" {
		managedByProfile = make(map[string]struct{}, len(test.TrafficProfile.Mappings))
		for _, mapping := range test.TrafficProfile.Mappings {
			managedByProfile[normalizeEffectiveGroupName(mapping.GroupName)] = struct{}{}
		}
	}
	targets := make(map[string]GoalTargetStatus, len(test.Groups))
	for _, group := range test.Groups {
		name := group.Name
		if managedByProfile != nil {
			if _, managed := managedByProfile[normalizeEffectiveGroupName(name)]; !managed {
				continue
			}
		}
		target := 0.0
		updatedAt := now
		profileTargetSet := false
		if existing, ok := test.GoalTargets[name]; ok && (existing.TargetTPS > 0 || managedByProfile != nil) {
			target = existing.TargetTPS
			profileTargetSet = managedByProfile != nil
			if !existing.UpdatedAt.IsZero() {
				updatedAt = existing.UpdatedAt
			}
		} else if managedByProfile != nil {
			profileTargetSet = true
		} else if stagedTargets[name] > 0 {
			target = stagedTargets[name]
		} else if liveTargets[name] > 0 {
			target = liveTargets[name]
		} else if defaultTargets[name] > 0 {
			target = defaultTargets[name]
		}
		if target <= 0 && !profileTargetSet {
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
	if shape := test.GoalConfig.WorkloadShapeConfig; shape != nil && shape.GoalDriven != nil && len(shape.GoalDriven.Groups) > 0 {
		targets := make(map[string]float64, len(test.Groups))
		for index, group := range test.Groups {
			definition, ok := shape.GoalDriven.Groups[fmt.Sprintf("group_%d", index)]
			if !ok || definition.TargetTPS <= 0 || math.IsNaN(definition.TargetTPS) || math.IsInf(definition.TargetTPS, 0) {
				return nil
			}
			targets[group.Name] = definition.TargetTPS
		}
		return targets
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

func evaluateGoalControllerForTest(test *ManagedTest, now time.Time, currentTotalVUsers int, live *LiveMetrics) goalControllerUpdate {
	if test == nil {
		return goalControllerUpdate{}
	}
	controllerLive := live
	if managedTPS, profileOwned := trafficProfileManagedTPS(test, live); profileOwned {
		managedLive := *live
		managedLive.TPS = managedTPS
		controllerLive = &managedLive
	}
	update := evaluateGoalController(now, currentTotalVUsers, controllerLive, test.GoalConfig, test.goalControl)
	if test.TrafficProfile == nil || test.TrafficProfile.RuntimeTarget != "tps" {
		return update
	}
	// A profile-owned aggregate target of zero is a deliberate suspension.
	// The boundary scheduler has already moved every zero-target group to zero;
	// do not let the ordinary minimum-VUser clamp immediately wake it again.
	if sumGoalTargetTPS(currentGoalTargetStatuses(test, now)) == 0 {
		update.ShouldScale = false
		update.TargetTotal = currentTotalVUsers
		if update.Status != nil {
			update.Status.SuggestedTotalVUsers = currentTotalVUsers
		}
	}
	return update
}

func trafficProfileManagedTPS(test *ManagedTest, live *LiveMetrics) (float64, bool) {
	if test == nil || live == nil || test.TrafficProfile == nil || test.TrafficProfile.RuntimeTarget != "tps" {
		return 0, false
	}
	managed := make(map[string]struct{}, len(test.TrafficProfile.Mappings))
	for _, mapping := range test.TrafficProfile.Mappings {
		managed[normalizeEffectiveGroupName(mapping.GroupName)] = struct{}{}
	}
	total := 0.0
	for name, metrics := range live.GroupMetrics {
		if metrics == nil {
			continue
		}
		if _, ok := managed[normalizeEffectiveGroupName(name)]; ok {
			total += metrics.TPS
		}
	}
	return total, true
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
	profileOwned := test.TrafficProfile != nil && test.TrafficProfile.RuntimeTarget == "tps"
	if totalTargetTPS <= 0 && !profileOwned {
		return
	}

	achievedTPS := live.TPS
	if managedTPS, ok := trafficProfileManagedTPS(test, live); ok {
		achievedTPS = managedTPS
	}
	live.TargetTPS = totalTargetTPS
	live.ThroughputGapPct, live.ThroughputTargetMissed = throughputGap(achievedTPS, totalTargetTPS)
	if live.GroupMetrics == nil {
		live.GroupMetrics = make(map[string]*GroupLiveMetrics)
	}
	for _, group := range test.Groups {
		target, ok := targets[group.Name]
		if !ok || (target.TargetTPS <= 0 && !profileOwned) {
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

// goalBreachSet captures which SLA thresholds are currently exceeded so stop
// decisions and reason strings derive from one evaluation pass.
type goalBreachSet struct {
	latency           bool
	errors            bool
	serverProcessing  bool
	transfer          bool
	errorCount        bool
	lcp               bool
	inp               bool
	cls               bool
	throttledRequests bool
	endToEndLatency   bool
	partitionSkew     bool
	redeliveryRate    bool
	deadLetterRate    bool
}

// totalServerTelemetryThrottles sums the run-total throttle/ServerBusy/quota
// signal across every transaction's messaging telemetry. Throttling is a
// backpressure count, so the SLA binds on the aggregate, not a per-transaction
// average.
func totalServerTelemetryThrottles(live *LiveMetrics) int64 {
	if live == nil {
		return 0
	}
	var total int64
	for _, tx := range live.Transactions {
		if tx == nil || tx.ServerTelemetry == nil {
			continue
		}
		total += tx.ServerTelemetry.TotalThrottledRequests
	}
	return total
}

func (b goalBreachSet) vitals() bool {
	return b.lcp || b.inp || b.cls
}

// hasBrowserVitalsBreach evaluates one Web Vitals threshold against the
// aggregated browser metrics. No browser samples means no breach — vitals
// thresholds only bind once browser VUsers or canaries report data.
func hasBrowserVitalsBreach(live *LiveMetrics, threshold *float64, pickValue func(browser *BrowserLiveMetrics) float64) bool {
	if live == nil || threshold == nil || live.Browser == nil || live.Browser.Samples == 0 {
		return false
	}
	return pickValue(live.Browser) > *threshold
}

func goalStopReason(breaches goalBreachSet) string {
	switch {
	case breaches.errorCount:
		return "max error count reached"
	case breaches.latency && breaches.errors:
		return "latency and error SLA breached"
	case breaches.serverProcessing && breaches.transfer:
		return "server processing and transfer SLA breached"
	case breaches.latency:
		return "p95 latency SLA breached"
	case breaches.errors:
		return "error rate SLA breached"
	case breaches.serverProcessing:
		return "server processing SLA breached"
	case breaches.transfer:
		return "transfer SLA breached"
	case breaches.lcp:
		return "LCP experience SLA breached"
	case breaches.inp:
		return "INP experience SLA breached"
	case breaches.cls:
		return "CLS experience SLA breached"
	case breaches.throttledRequests:
		return "Event Hubs throttled-request SLA breached"
	case breaches.endToEndLatency:
		return "Event Hubs end-to-end latency SLA breached"
	case breaches.partitionSkew:
		return "Event Hubs partition skew SLA breached"
	case breaches.redeliveryRate:
		return "messaging redelivery rate SLA breached"
	case breaches.deadLetterRate:
		return "messaging dead-letter rate SLA breached"
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
	loadGroups, observerTargets := splitObserverGroups(groups, current)
	loadTotal := desiredTotal
	for _, vusers := range observerTargets {
		loadTotal -= vusers
	}
	if loadTotal < 0 {
		loadTotal = 0
	}

	targets := distributeTargetVUsersByWeights(loadGroups, loadTotal, goalTargets)
	if len(targets) == 0 {
		targets = distributeTargetVUsers(loadGroups, current, loadTotal)
	}
	if len(observerTargets) == 0 {
		return targets
	}
	if targets == nil {
		targets = make(map[string]int, len(observerTargets))
	}
	for name, vusers := range observerTargets {
		targets[name] = vusers
	}
	return targets
}

func distributeTrafficProfileTargetVUsers(groups []TestGroup, current map[string]int, desiredTotal int, goalTargets map[string]GoalTargetStatus, profile *TrafficProfile) map[string]int {
	if profile == nil || profile.RuntimeTarget != "tps" {
		return distributeTargetVUsersForGoal(groups, current, desiredTotal, goalTargets)
	}
	managedNames := make(map[string]struct{}, len(profile.Mappings))
	for _, mapping := range profile.Mappings {
		managedNames[normalizeEffectiveGroupName(mapping.GroupName)] = struct{}{}
	}
	managed := make([]TestGroup, 0, len(managedNames))
	targets := make(map[string]int, len(groups))
	staticTotal := 0
	for _, group := range groups {
		if _, ok := managedNames[normalizeEffectiveGroupName(group.Name)]; ok {
			managed = append(managed, group)
			continue
		}
		pinned := max(0, current[group.Name])
		targets[group.Name] = pinned
		staticTotal = saturatingAddNonNegative(staticTotal, pinned)
	}
	managedTotal := max(0, desiredTotal-staticTotal)
	managedTargets := distributeTargetVUsersByWeights(managed, managedTotal, goalTargets)
	if len(managedTargets) == 0 && len(managed) > 0 {
		managedTargets = distributeTargetVUsers(managed, current, managedTotal)
	}
	for name, target := range managedTargets {
		targets[name] = target
	}
	return targets
}

// splitObserverGroups separates load groups from observer canaries. Observers
// are pinned at their current allocation: scaling the load up or down must not
// multiply diagnostic browser sessions or starve experience monitoring.
func splitObserverGroups(groups []TestGroup, current map[string]int) ([]TestGroup, map[string]int) {
	loadGroups := make([]TestGroup, 0, len(groups))
	observerTargets := make(map[string]int)
	for _, group := range groups {
		if group.Role == GroupRoleObserver {
			pinned := current[group.Name]
			if pinned <= 0 {
				pinned = group.VUsers
			}
			observerTargets[group.Name] = pinned
			continue
		}
		loadGroups = append(loadGroups, group)
	}
	return loadGroups, observerTargets
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
