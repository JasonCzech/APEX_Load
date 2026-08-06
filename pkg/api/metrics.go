package api

// Live-metrics construction and emission: build LiveMetrics from engine
// snapshots, derive transaction/dependency/error-pattern + live-evidence views,
// compute live capacity status, cache run-live-state snapshots, and run the
// publish/broadcast pipeline that fans metrics out to subscribers.

import (
	"context"
	"encoding/json"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
)

// copyErrorClassCounts returns a defensive copy of the error-taxonomy counts, or
// nil when empty so the field is omitted from JSON.
func copyErrorClassCounts(in map[string]int64) map[string]int64 {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]int64, len(in))
	for class, count := range in {
		out[class] = count
	}
	return out
}

// groupActiveVUsers prefers the engine's live per-group active count so
// group-scoped metrics follow the live ramp; it falls back to the configured
// group size when the live count is unavailable (e.g. an engine that does not
// report per-group counts).
func groupActiveVUsers(gm *engine.GroupMetrics, configured int) int {
	if gm != nil && gm.ActiveVUsers > 0 {
		return int(gm.ActiveVUsers)
	}
	return configured
}

func buildLiveMetricsFromSnapshot(snap engine.MetricsSnapshot, groupVUsers map[string]int) *LiveMetrics {
	live := &LiveMetrics{
		Timestamp:              snap.Timestamp,
		ActiveVUsers:           snap.ActiveVUsers,
		TPS:                    snap.TPS,
		TargetTPS:              snap.TargetTPS,
		ThroughputGapPct:       snap.ThroughputGapPct,
		ThroughputTargetMissed: snap.ThroughputTargetMissed,
		AvgResponseMs:          float64(snap.AvgDuration.Microseconds()) / 1000.0,
		TotalTx:                snap.Transactions,
		TotalErrors:            snap.Errors,
		P50Ms:                  float64(snap.P50.Microseconds()) / 1000.0,
		P90Ms:                  float64(snap.P90.Microseconds()) / 1000.0,
		P95Ms:                  float64(snap.P95.Microseconds()) / 1000.0,
		P99Ms:                  float64(snap.P99.Microseconds()) / 1000.0,
		AvgDNSMs:               snap.AvgDNSMs,
		AvgConnectMs:           snap.AvgConnectMs,
		AvgTLSMs:               snap.AvgTLSMs,
		AvgTTFBMs:              snap.AvgTTFBMs,
		AvgWaitMs:              snap.AvgWaitMs,
		ErrorBreakdown:         copyErrorClassCounts(snap.ErrorsByClass),
		Transactions:           make(map[string]*TxMetrics),
		DependencyHealth:       make(map[string]*TxMetrics),
		SOAPOperations:         make(map[string]*TxMetrics),
		GroupMetrics:           make(map[string]*GroupLiveMetrics),
		NetworkProfiles:        make(map[string]*TxMetrics),
		Resources:              snap.Resources,
	}
	if snap.Browser != nil {
		live.Browser = &BrowserLiveMetrics{
			Samples:              snap.Browser.Samples,
			AvgFCPMs:             snap.Browser.AvgFCPMs,
			AvgLCPMs:             snap.Browser.AvgLCPMs,
			AvgCLSScore:          snap.Browser.AvgCLSScore,
			AvgINPMs:             snap.Browser.AvgINPMs,
			AvgTTFBMs:            snap.Browser.AvgTTFBMs,
			AvgBlockingResources: snap.Browser.AvgBlockingResources,
			AvgLongTaskCount:     snap.Browser.AvgLongTaskCount,
			AvgJSHeapUsedMB:      snap.Browser.AvgJSHeapUsedMB,
			AvgBrowserProcessCPU: snap.Browser.AvgBrowserProcessCPU,
			AvgBrowserProcessMem: snap.Browser.AvgBrowserProcessMem,
		}
	}
	if len(snap.BrowserByDevice) > 0 {
		live.BrowserByDevice = make(map[string]*BrowserDeviceLiveMetrics, len(snap.BrowserByDevice))
		for name, metrics := range snap.BrowserByDevice {
			live.BrowserByDevice[name] = &BrowserDeviceLiveMetrics{
				DeviceProfile:        metrics.DeviceProfile,
				DevicePlatform:       metrics.DevicePlatform,
				DeviceTier:           metrics.DeviceTier,
				BrowserEngine:        metrics.BrowserEngine,
				Samples:              metrics.Samples,
				AvgFCPMs:             metrics.AvgFCPMs,
				AvgLCPMs:             metrics.AvgLCPMs,
				AvgCLSScore:          metrics.AvgCLSScore,
				AvgINPMs:             metrics.AvgINPMs,
				AvgTTFBMs:            metrics.AvgTTFBMs,
				AvgBlockingResources: metrics.AvgBlockingResources,
				AvgLongTaskCount:     metrics.AvgLongTaskCount,
				AvgJSHeapUsedMB:      metrics.AvgJSHeapUsedMB,
				AvgBrowserProcessCPU: metrics.AvgBrowserProcessCPU,
				AvgBrowserProcessMem: metrics.AvgBrowserProcessMem,
			}
		}
	}
	if snap.Telemetry != nil {
		live.Telemetry = &TelemetryHealth{
			DroppedResults:  snap.Telemetry.DroppedResults,
			SheddedArrivals: snap.Telemetry.SheddedArrivals,
			CollectedAt:     snap.Timestamp,
		}
	}

	transactionAttempts := live.TotalTx + live.TotalErrors
	if transactionAttempts > 0 {
		live.ErrorPct = float64(live.TotalErrors) / float64(transactionAttempts) * 100
	}

	for name, am := range snap.ByTransaction {
		live.Transactions[name] = buildTxMetricsFromAction(name, am)
	}

	for name, am := range snap.ByDependencyTarget {
		live.DependencyHealth[name] = buildTxMetricsFromAction(name, am)
	}
	live.DependencyMap = buildLiveDependencyMap(live.DependencyHealth)

	for name, am := range snap.BySOAPOperation {
		live.SOAPOperations[name] = buildTxMetricsFromAction(name, am)
	}

	for name, am := range snap.ByNetworkProfile {
		live.NetworkProfiles[name] = buildTxMetricsFromAction(name, am)
	}

	for name, gm := range snap.ByGroup {
		live.GroupMetrics[name] = &GroupLiveMetrics{
			Name:                   name,
			VUsers:                 groupActiveVUsers(gm, groupVUsers[name]),
			Transactions:           gm.Transactions,
			Errors:                 gm.Errors,
			TPS:                    gm.TPS,
			TargetTPS:              gm.TargetTPS,
			ThroughputGapPct:       gm.ThroughputGapPct,
			ThroughputTargetMissed: gm.ThroughputTargetMissed,
			AvgResponseMs:          float64(gm.AvgDuration.Microseconds()) / 1000.0,
			P50Ms:                  float64(gm.P50.Microseconds()) / 1000.0,
			P90Ms:                  float64(gm.P90.Microseconds()) / 1000.0,
			P95Ms:                  float64(gm.P95.Microseconds()) / 1000.0,
			P99Ms:                  float64(gm.P99.Microseconds()) / 1000.0,
			DurationDigest:         gm.DurationDigest,
		}
	}

	return live
}

func buildLiveDependencyMap(dependencies map[string]*TxMetrics) *LiveDependencyMap {
	if len(dependencies) == 0 {
		return nil
	}
	targets := make([]string, 0, len(dependencies))
	for target, tx := range dependencies {
		if tx == nil {
			continue
		}
		targets = append(targets, target)
	}
	if len(targets) == 0 {
		return nil
	}
	sort.Strings(targets)
	nodes := []LiveDependencyNode{{
		ID:     "apexload",
		Label:  "APEX Load VUsers",
		Kind:   "source",
		Status: "healthy",
	}}
	edges := make([]LiveDependencyEdge, 0, len(targets))
	for _, target := range targets {
		tx := dependencies[target]
		nodeID := "dependency:" + target
		status := dependencyHealthStatus(tx)
		errorPct := txErrorPct(tx)
		nodes = append(nodes, LiveDependencyNode{
			ID:             nodeID,
			Label:          target,
			Kind:           "dependency",
			DependencyType: firstNonEmpty(tx.DependencyType, "unknown"),
			Status:         status,
			Transactions:   tx.Count,
			Errors:         tx.Errors,
			ErrorPct:       errorPct,
			P95Ms:          tx.P95Ms,
		})
		edges = append(edges, LiveDependencyEdge{
			Source:         "apexload",
			Target:         nodeID,
			DependencyType: firstNonEmpty(tx.DependencyType, "unknown"),
			Transactions:   tx.Count,
			Errors:         tx.Errors,
			ErrorPct:       errorPct,
			P95Ms:          tx.P95Ms,
		})
	}
	return &LiveDependencyMap{Nodes: nodes, Edges: edges}
}

func dependencyHealthStatus(tx *TxMetrics) string {
	if tx == nil {
		return "unknown"
	}
	if tx.Errors > 0 {
		return "danger"
	}
	if tx.P95Ms >= slowTransactionDiagnosticThresholdMs {
		return "warning"
	}
	return "healthy"
}

func txErrorPct(tx *TxMetrics) float64 {
	if tx == nil || tx.Count <= 0 {
		return 0
	}
	return float64(tx.Errors) / float64(tx.Count) * 100
}

func buildTxMetricsFromAction(name string, am *engine.ActionMetrics) *TxMetrics {
	if am == nil {
		return nil
	}
	return &TxMetrics{
		Name:             name,
		DependencyType:   am.DependencyType,
		DependencyTarget: am.DependencyTarget,
		LastErrorMessage: am.LastErrorMessage,
		LastStatusCode:   am.LastStatusCode,
		LastErrorAt:      am.LastErrorAt,
		ServerTelemetry:  cloneEngineServerTelemetrySummary(am.ServerTelemetry),
		AvgDNSMs:         am.AvgDNSMs,
		AvgConnectMs:     am.AvgConnectMs,
		AvgTLSMs:         am.AvgTLSMs,
		AvgTTFBMs:        am.AvgTTFBMs,
		CorrelationIDs:   append([]string(nil), am.CorrelationIDs...),
		CorrelationCount: am.CorrelationCount,
		Count:            am.Count,
		Errors:           am.Errors,
		AvgMs:            float64(am.AvgDuration.Microseconds()) / 1000.0,
		P50Ms:            float64(am.P50.Microseconds()) / 1000.0,
		P90Ms:            float64(am.P90.Microseconds()) / 1000.0,
		P95Ms:            float64(am.P95.Microseconds()) / 1000.0,
		P99Ms:            float64(am.P99.Microseconds()) / 1000.0,
	}
}

func buildLiveMetricsForBroadcast(snap engine.MetricsSnapshot, groupVUsers map[string]int, now time.Time) *LiveMetrics {
	live := buildLiveMetricsFromSnapshot(snap, groupVUsers)
	live.Timestamp = now
	if live.Telemetry == nil {
		live.Telemetry = &TelemetryHealth{}
	}
	if live.Telemetry.CollectedAt.IsZero() {
		live.Telemetry.CollectedAt = snap.Timestamp
	}
	live.Telemetry.PublishedAt = now
	if !live.Telemetry.CollectedAt.IsZero() {
		live.Telemetry.SampleAgeMs = float64(now.Sub(live.Telemetry.CollectedAt).Microseconds()) / 1000.0
	}
	return live
}

func buildLiveCapacityStatus(current *ManagedTest, live *LiveMetrics, tests []*ManagedTest) *LiveCapacityStatus {
	if current == nil || current.CapacityAdvisory == nil {
		return nil
	}
	status := &LiveCapacityStatus{
		AllocatedVUsers:       int(liveActiveVUsers(live, current)),
		AvailableGenerators:   current.CapacityAdvisory.AvailableGenerators,
		RecommendedGenerators: current.CapacityAdvisory.RecommendedGenerators,
		Saturated:             current.CapacityAdvisory.RequiresOverride || current.CapacityAdvisory.ShortfallGenerators > 0 || current.CapacityAdvisory.RecommendedGenerators >= current.CapacityAdvisory.AvailableGenerators,
		ByProtocol:            make(map[string]*LiveCapacityBreakdown),
		ByRegion:              make(map[string]*LiveCapacityBreakdown),
	}
	for _, test := range tests {
		if test == nil {
			continue
		}
		switch test.Status {
		case "INITIALIZING", "RUNNING", "PAUSED":
			addCapacityDemand(status, test, "allocated")
		case "QUEUED", "PENDING", "RESERVED":
			status.QueuedVUsers += test.TotalVUsers
			addCapacityDemand(status, test, "queued")
		}
		if test.CapacityAdvisory != nil && test.CapacityAdvisory.RequiresOverride {
			status.RejectedVUsers += test.TotalVUsers
			addCapacityDemand(status, test, "rejected")
		}
	}
	if len(status.ByProtocol) == 0 {
		status.ByProtocol = nil
	}
	if len(status.ByRegion) == 0 {
		status.ByRegion = nil
	}
	return status
}

func liveActiveVUsers(live *LiveMetrics, test *ManagedTest) int64 {
	if live != nil && live.ActiveVUsers > 0 {
		return live.ActiveVUsers
	}
	if test != nil && (test.Status == "INITIALIZING" || test.Status == "RUNNING" || test.Status == "PAUSED") {
		return int64(test.TotalVUsers)
	}
	return 0
}

func addCapacityDemand(status *LiveCapacityStatus, test *ManagedTest, bucket string) {
	for protocol, vusers := range capacityDemandByProtocol(test.Groups) {
		addCapacityBreakdown(status.ByProtocol, protocol, bucket, vusers)
	}
	for region, vusers := range capacityDemandByRegion(test) {
		addCapacityBreakdown(status.ByRegion, region, bucket, vusers)
	}
}

func addCapacityBreakdown(target map[string]*LiveCapacityBreakdown, key string, bucket string, vusers int) {
	if vusers <= 0 {
		return
	}
	key = strings.TrimSpace(key)
	if key == "" {
		key = "unknown"
	}
	breakdown := target[key]
	if breakdown == nil {
		breakdown = &LiveCapacityBreakdown{}
		target[key] = breakdown
	}
	switch bucket {
	case "allocated":
		breakdown.AllocatedVUsers += vusers
	case "queued":
		breakdown.QueuedVUsers += vusers
	case "rejected":
		breakdown.RejectedVUsers += vusers
	}
}

func capacityDemandByProtocol(groups []TestGroup) map[string]int {
	out := make(map[string]int)
	for _, group := range groups {
		protocol := strings.TrimSpace(group.Protocol)
		if protocol == "" {
			protocol = "unknown"
		}
		out[protocol] += group.VUsers
	}
	return out
}

func capacityDemandByRegion(test *ManagedTest) map[string]int {
	out := make(map[string]int)
	if test == nil || test.TotalVUsers <= 0 {
		return out
	}
	assignment := test.Assignment
	if assignment == nil || assignment.Mode == "" || assignment.Mode == generatorAssignmentModeAuto {
		out["auto"] = test.TotalVUsers
		return out
	}
	if assignment.Mode == generatorAssignmentModeSpecificHosts {
		if len(assignment.SelectedTargets) == 0 {
			out["selected_hosts"] = test.TotalVUsers
			return out
		}
		for _, target := range assignment.SelectedTargets {
			out[firstNonEmpty(strings.TrimSpace(target), "selected_hosts")] += test.TotalVUsers / len(assignment.SelectedTargets)
		}
		remainder := test.TotalVUsers % len(assignment.SelectedTargets)
		if remainder > 0 {
			out[firstNonEmpty(strings.TrimSpace(assignment.SelectedTargets[0]), "selected_hosts")] += remainder
		}
		return out
	}
	if (assignment.Mode == generatorAssignmentModeRegionWeighted || assignment.Mode == generatorAssignmentModeLoadZoneWeighted) && len(effectiveRegionWeights(assignment)) > 0 {
		weights := effectiveRegionWeights(assignment)
		totalWeight := 0
		regions := make([]string, 0, len(weights))
		for region, weight := range weights {
			if weight <= 0 {
				continue
			}
			regions = append(regions, region)
			totalWeight += weight
		}
		sort.Strings(regions)
		allocated := 0
		for index, region := range regions {
			vusers := 0
			if index == len(regions)-1 {
				vusers = test.TotalVUsers - allocated
			} else {
				vusers = test.TotalVUsers * weights[region] / totalWeight
				allocated += vusers
			}
			out[firstNonEmpty(strings.TrimSpace(region), "unassigned")] += vusers
		}
		if len(out) > 0 {
			return out
		}
	}
	out["unassigned"] = test.TotalVUsers
	return out
}

// publishLiveMetrics installs and broadcasts one live snapshot. Optional
// install guards run while tr.mu is held immediately before any ManagedTest
// mutation; returning false rejects the snapshot atomically.
func (tr *TestRunner) publishLiveMetrics(test *ManagedTest, live *LiveMetrics, artifacts *runArtifacts, installGuards ...func() bool) bool {
	now := time.Now()
	hubStats := HubStats{}
	if tr.hub != nil {
		hubStats = tr.hub.Stats()
	}
	if live.Telemetry == nil {
		live.Telemetry = &TelemetryHealth{}
	}
	if live.Telemetry.ControllerReceivedAt.IsZero() {
		live.Telemetry.ControllerReceivedAt = now
	}
	if live.Telemetry.CollectedAt.IsZero() {
		live.Telemetry.CollectedAt = live.Timestamp
	}
	live.Telemetry.PublishedAt = now
	if !live.Telemetry.ControllerReceivedAt.IsZero() {
		live.Telemetry.ControllerFanoutLagMs = float64(now.Sub(live.Telemetry.ControllerReceivedAt).Microseconds()) / 1000.0
	}
	if !live.Telemetry.CollectedAt.IsZero() {
		live.Telemetry.SampleAgeMs = float64(now.Sub(live.Telemetry.CollectedAt).Microseconds()) / 1000.0
	}
	live.Telemetry.WebsocketClients = hubStats.ConnectedClients
	live.Telemetry.WebsocketDroppedMessages = hubStats.DroppedClientMessages
	live.Telemetry.WebsocketDroppedEvents = hubStats.DroppedBroadcasts
	artifacts.enrichLiveMetrics(test, live)
	diagnostic, diagnosticSignature := buildTelemetryDiagnostic(test, live, tr.GetSystemSettings().Thresholds)
	publishDiagnostic := false
	var telemetryRunHealth *TelemetryRunHealth
	var previousTelemetryRunHealth *TelemetryRunHealth

	tr.mu.Lock()
	for _, guard := range installGuards {
		if guard != nil && !guard() {
			tr.mu.Unlock()
			return false
		}
	}
	tests := make([]*ManagedTest, 0, len(tr.tests))
	for _, candidate := range tr.tests {
		tests = append(tests, candidate)
	}
	previousTelemetryRunHealth = cloneTelemetryRunHealth(test.TelemetryHealth)
	telemetryRunHealth = updateTelemetryRunHealth(&test.telemetryDiagnostic, diagnostic, diagnosticSignature, now)
	live.TelemetryStatus = cloneTelemetryRunHealth(telemetryRunHealth)
	live.CapacityStatus = buildLiveCapacityStatus(test, live, tests)
	tr.appendLiveErrorEvidenceLocked(test, live.ErrorEvidence)
	test.errorPatterns = updateLiveErrorPatterns(test.errorPatterns, live, test.ID)
	trimLiveErrorPatternsLocked(test.errorPatterns)
	enrichLiveErrorPatternsWithEvidence(test, test.errorPatterns)
	live.ErrorPatterns = liveErrorPatternsList(test.errorPatterns)
	live.ErrorEvidence = nil
	test.TelemetryHealth = cloneTelemetryRunHealth(telemetryRunHealth)
	if live.Latency == nil {
		live.Latency = cloneNetworkLatency(test.lastNetworkLatency)
		if live.Latency == nil && test.LiveMetrics != nil {
			live.Latency = cloneNetworkLatency(test.LiveMetrics.Latency)
		}
	} else {
		test.lastNetworkLatency = cloneNetworkLatency(live.Latency)
	}
	live.Telemetry.HistoryPoints = min(len(test.History)+1, maxManagedTestHistory)
	installedLive := cloneLiveMetrics(live)
	normalizePausedLiveVUsers(test, installedLive)
	recordCISLAStateLocked(test, installedLive)
	test.LiveMetrics = installedLive
	appendManagedTestHistoryWithLimits(test, installedLive, maxManagedTestHistory, maxManagedTestHistoryBytes)
	if installedLive.Telemetry != nil {
		installedLive.Telemetry.HistoryPoints = len(test.History)
	}
	if diagnostic != nil {
		publishDiagnostic = shouldPublishTelemetryDiagnostic(&test.telemetryDiagnostic, diagnosticSignature, now)
	}
	// Emit a one-time warning the first time the generator(s) shed open-workload
	// arrivals — the offered TPS is exceeding achievable throughput, so the
	// in-flight worker cap is dropping load to protect the generator.
	emitSheddingWarning := false
	sheddedArrivals := int64(0)
	if live.Telemetry != nil && live.Telemetry.SheddedArrivals > 0 && !test.openWorkloadSheddingWarned {
		test.openWorkloadSheddingWarned = true
		emitSheddingWarning = true
		sheddedArrivals = live.Telemetry.SheddedArrivals
	}
	snapshot := buildRunLiveStateSnapshotLocked(test, now)
	publishedLive := cloneLiveMetrics(installedLive)
	testID := test.ID
	testName := test.Name
	projectID := normalizeProjectID(test.ProjectID)
	goalConfig := cloneGoalConfig(test.GoalConfig)
	tr.mu.Unlock()

	tr.cacheRunSnapshot(snapshot)
	if err := tr.saveLiveMetricsHistory(testID, publishedLive.Timestamp, publishedLive); err != nil {
		slog.Warn("failed to persist live metrics history", "test_id", testID, "timestamp", publishedLive.Timestamp, "error", err)
	}
	artifacts.recordLiveMetrics(test, goalConfig, publishedLive)

	tr.hub.BroadcastScopedJSON(testID, projectID, map[string]any{
		"type":       "metrics_update",
		"test_id":    testID,
		"project_id": projectID,
		"test_name":  testName,
		"timestamp":  publishedLive.Timestamp,
		"data":       publishedLive,
	})
	if publishDiagnostic {
		tr.hub.BroadcastScopedJSON(testID, projectID, map[string]any{
			"type":       "telemetry_diagnostic",
			"test_id":    testID,
			"project_id": projectID,
			"test_name":  testName,
			"timestamp":  now,
			"data":       diagnostic,
		})
	}
	tr.broadcastTelemetryHealthTransition(test, previousTelemetryRunHealth, telemetryRunHealth, now)
	if emitSheddingWarning {
		tr.broadcastOpenWorkloadShedding(test, sheddedArrivals, now)
	}
	return true
}

func appendManagedTestHistoryWithLimits(test *ManagedTest, point *LiveMetrics, maxPoints int, maxBytes int64) bool {
	if test == nil || point == nil || maxPoints <= 0 || maxBytes <= 0 {
		return false
	}
	ensureManagedTestHistoryAccounting(test, maxPoints, maxBytes)
	weight := managedTestHistoryPointWeight(point, maxBytes)
	if weight > maxBytes {
		return false
	}
	test.History = append(test.History, point)
	test.historyWeights = append(test.historyWeights, weight)
	test.historyBytes += weight
	trimManagedTestHistoryToLimits(test, maxPoints, maxBytes)
	return len(test.History) > 0 && test.History[len(test.History)-1] == point
}

func ensureManagedTestHistoryAccounting(test *ManagedTest, maxPoints int, maxBytes int64) {
	if test == nil {
		return
	}
	if len(test.historyWeights) != len(test.History) {
		test.historyWeights = make([]int64, len(test.History))
		test.historyBytes = 0
		for index, point := range test.History {
			weight := managedTestHistoryPointWeight(point, maxBytes)
			test.historyWeights[index] = weight
			test.historyBytes += weight
		}
	}
	trimManagedTestHistoryToLimits(test, maxPoints, maxBytes)
}

func trimManagedTestHistoryToLimits(test *ManagedTest, maxPoints int, maxBytes int64) {
	if test == nil {
		return
	}
	for len(test.History) > 0 && (len(test.History) > maxPoints || test.historyBytes > maxBytes) {
		test.historyBytes -= test.historyWeights[0]
		test.History[0] = nil
		test.historyWeights[0] = 0
		test.History = test.History[1:]
		test.historyWeights = test.historyWeights[1:]
	}
	if len(test.History) == 0 {
		test.History = nil
		test.historyWeights = nil
		test.historyBytes = 0
	}
}

func managedTestHistoryPointWeight(point *LiveMetrics, maxBytes int64) int64 {
	if point == nil {
		return 4 // JSON "null"
	}
	payload, err := json.Marshal(point)
	if err != nil || int64(len(payload)) > maxBytes {
		return maxBytes + 1
	}
	return int64(len(payload))
}

// normalizePausedLiveVUsers keeps API/history/WebSocket snapshots aligned with
// the durable lifecycle state. Engines and generators can emit a buffered
// nonzero sample after Pause has committed; capacity reservations remain on the
// ManagedTest, while the user-facing active counts stay at zero until Resume.
func normalizePausedLiveVUsers(test *ManagedTest, live *LiveMetrics) {
	if test == nil || live == nil || test.Status != "PAUSED" {
		return
	}
	live.ActiveVUsers = 0
	for _, group := range live.GroupMetrics {
		if group != nil {
			group.VUsers = 0
		}
	}
}

// broadcastOpenWorkloadShedding emits a one-time WebSocket warning when an
// open-model run begins shedding arrivals because the in-flight worker cap is
// saturated (the system under test cannot keep up with the offered TPS).
func (tr *TestRunner) broadcastOpenWorkloadShedding(test *ManagedTest, sheddedArrivals int64, now time.Time) {
	if tr == nil || tr.hub == nil || test == nil {
		return
	}
	projectID := normalizeProjectID(test.ProjectID)
	tr.hub.BroadcastScopedJSON(test.ID, projectID, map[string]any{
		"type":       "open_workload_shedding",
		"test_id":    test.ID,
		"project_id": projectID,
		"test_name":  test.Name,
		"timestamp":  now,
		"data": map[string]any{
			"shedded_arrivals": sheddedArrivals,
			"message":          "Open-model arrivals are being shed: the system under test cannot keep up with the offered rate. In-flight workers are capped to protect the generator, so effective throughput is below target. Lower the TPS or raise run_logic.max_in_flight.",
		},
	})
}

func buildRunLiveStateSnapshotLocked(test *ManagedTest, updatedAt time.Time) RunLiveStateSnapshot {
	if test == nil {
		return RunLiveStateSnapshot{UpdatedAt: updatedAt}
	}
	return RunLiveStateSnapshot{
		TestID:        test.ID,
		TestName:      test.Name,
		ProjectID:     normalizeProjectID(test.ProjectID),
		Status:        test.Status,
		UpdatedAt:     updatedAt.UTC(),
		StartedAt:     cloneTimePtr(test.StartedAt),
		EndedAt:       cloneTimePtr(test.EndedAt),
		LiveMetrics:   cloneLiveMetrics(test.LiveMetrics),
		HistoryPoints: len(test.History),
	}
}

// replaceLiveMetricsLocked installs a copy-on-write replacement and keeps the
// latest history slot aligned when it referred to the replaced live snapshot.
// Installed snapshots are otherwise immutable so background encoders can use
// them safely after tr.mu is released.
func replaceLiveMetricsLocked(test *ManagedTest, replacement *LiveMetrics) {
	if test == nil {
		return
	}
	previous := test.LiveMetrics
	test.LiveMetrics = replacement
	if len(test.History) > 0 && test.History[len(test.History)-1] == previous {
		ensureManagedTestHistoryAccounting(test, maxManagedTestHistory, maxManagedTestHistoryBytes)
		if len(test.History) == 0 || test.History[len(test.History)-1] != previous {
			return
		}
		last := len(test.History) - 1
		test.historyBytes -= test.historyWeights[last]
		test.History[last] = replacement
		test.historyWeights[last] = managedTestHistoryPointWeight(replacement, maxManagedTestHistoryBytes)
		test.historyBytes += test.historyWeights[last]
		trimManagedTestHistoryToLimits(test, maxManagedTestHistory, maxManagedTestHistoryBytes)
	}
}

type runSnapshotWriteState struct {
	latest  RunLiveStateSnapshot
	dirty   bool
	writing bool
}

func (tr *TestRunner) cacheRunSnapshot(snapshot RunLiveStateSnapshot) {
	testID := strings.TrimSpace(snapshot.TestID)
	if tr == nil || testID == "" {
		return
	}
	tr.mu.RLock()
	store := tr.liveState
	current := tr.tests[testID]
	// A snapshot is derived under tr.mu, then queued after that lock is released.
	// Reject it if a lifecycle transition or deletion won in between; otherwise
	// a delayed RUNNING sample can overwrite a terminal cache record even after
	// the per-run writer has drained.
	if store == nil || current == nil || snapshot.Status != current.Status {
		tr.mu.RUnlock()
		return
	}
	// Keep the lifecycle read lock through queue admission. A terminal mutation
	// cannot otherwise win after the status check but before this snapshot is
	// admitted as the newest per-run value.
	tr.snapshotWriteMu.Lock()
	tr.mu.RUnlock()
	if tr.snapshotWrites == nil {
		tr.snapshotWrites = make(map[string]*runSnapshotWriteState)
	}
	if pending := tr.snapshotWrites[testID]; pending != nil {
		if pending.latest.UpdatedAt.IsZero() || snapshot.Status != pending.latest.Status || !snapshot.UpdatedAt.Before(pending.latest.UpdatedAt) {
			pending.latest = snapshot
			pending.dirty = true
		}
		if !pending.writing && pending.dirty {
			pending.writing = true
			tr.snapshotWriteMu.Unlock()
			go tr.runSnapshotWriter(testID, pending)
			return
		}
		tr.snapshotWriteMu.Unlock()
		return
	}
	state := &runSnapshotWriteState{latest: snapshot, dirty: true, writing: true}
	tr.snapshotWrites[testID] = state
	tr.snapshotWriteMu.Unlock()
	go tr.runSnapshotWriter(testID, state)
}

// runSnapshotWriter serializes and coalesces cache writes for one run. Metrics
// streams can publish concurrently; a goroutine-per-sample both grows without
// bound during a cache stall and lets an older RUNNING write land after a newer
// terminal snapshot. One short-lived writer per run preserves write order and
// always drains the latest coalesced state.
func (tr *TestRunner) runSnapshotWriter(testID string, state *runSnapshotWriteState) {
	for {
		tr.snapshotWriteMu.Lock()
		current := tr.snapshotWrites[testID]
		if current != state {
			tr.snapshotWriteMu.Unlock()
			return
		}
		if !state.dirty {
			state.writing = false
			tr.snapshotWriteMu.Unlock()
			return
		}
		snapshot := state.latest
		state.dirty = false
		tr.snapshotWriteMu.Unlock()

		tr.mu.RLock()
		store := tr.liveState
		tr.mu.RUnlock()
		if store != nil {
			ctx, cancel := context.WithTimeout(context.Background(), defaultCacheWriteTimeout)
			err := store.SetRunSnapshot(ctx, snapshot, defaultRunSnapshotTTL)
			cancel()
			if err != nil {
				slog.Warn("failed to cache live run snapshot", "test_id", snapshot.TestID, "err", err)
			}
		}

		tr.snapshotWriteMu.Lock()
		if tr.snapshotWrites[testID] != state {
			tr.snapshotWriteMu.Unlock()
			return
		}
		if !state.dirty {
			state.writing = false
			tr.snapshotWriteMu.Unlock()
			return
		}
		tr.snapshotWriteMu.Unlock()
	}
}

// forgetRunSnapshotWriteStateLocked removes the per-run ordering watermark
// when its owning ManagedTest is removed. Callers hold tr.mu, matching the
// queue-admission lock order (tr.mu -> snapshotWriteMu).
func (tr *TestRunner) forgetRunSnapshotWriteStateLocked(testID string) {
	if tr == nil || strings.TrimSpace(testID) == "" {
		return
	}
	tr.snapshotWriteMu.Lock()
	delete(tr.snapshotWrites, testID)
	tr.snapshotWriteMu.Unlock()
}

func (tr *TestRunner) broadcastMetrics(test *ManagedTest, mge *engine.MultiGroupEngine, artifacts *runArtifacts, done chan struct{}) time.Time {
	intervalSec := tr.GetSystemSettings().General.MetricsIntervalSeconds
	interval, ok := checkedDuration(intervalSec, time.Second)
	if !ok || interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	lastLatencyCheck := time.Time{}
	lastPublishedSnapshot := time.Time{}

	for {
		select {
		case <-done:
			return lastPublishedSnapshot
		case <-ticker.C:
			snap, ok := mge.LatestSnapshot()
			if !ok || (!lastPublishedSnapshot.IsZero() && !snap.Timestamp.After(lastPublishedSnapshot)) {
				continue
			}
			lastPublishedSnapshot = snap.Timestamp

			// Runtime scaling mutates the group targets while this broadcaster is
			// alive. Snapshot them for every sample so active-VUser accounting and
			// goal-control decisions use the configuration that produced it.
			groupVUsers := tr.groupVUsersSnapshot(test)
			live := buildLiveMetricsForBroadcast(snap, groupVUsers, time.Now())
			// Prefer the engine's observed per-group count when the collector
			// reports one; the runner snapshot remains the fallback for quiet groups.
			for groupName, metrics := range live.GroupMetrics {
				if metrics != nil {
					groupVUsers[groupName] = metrics.VUsers
				}
			}
			if update, ok := tr.evaluateGoalControllerLocked(test, live, groupVUsers); ok {
				applyGoalControllerUpdate(tr, test, live, groupVUsers, update, mge.ScaleVUsers)
			}

			// Throttle network latency pings to every 5 seconds
			if time.Since(lastLatencyCheck) >= 5*time.Second {
				tr.mu.RLock()
				probeCtx := test.executionCtx
				tr.mu.RUnlock()
				if probeCtx == nil {
					probeCtx = context.Background()
				}
				go tr.measureNetworkLatency(probeCtx, test, live)
				lastLatencyCheck = time.Now()
			}

			tr.publishLiveMetrics(test, live, artifacts)
			tr.evaluateThresholdAlertsAndStops(test, live, true)
		}
	}
}

func (tr *TestRunner) groupVUsersSnapshot(test *ManagedTest) map[string]int {
	groupVUsers := make(map[string]int)
	if tr == nil || test == nil {
		return groupVUsers
	}
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	for _, group := range test.Groups {
		groupVUsers[group.Name] = groupCurrentVUsers(group)
	}
	return groupVUsers
}

// publishFinalLocalMetrics sends the collector's shutdown drain through the
// same goal/SLA/live/archive pipeline as periodic samples. It deliberately does
// not issue runtime scale/stop operations: the engine has already quiesced.
// lastPublishedSnapshot suppresses a duplicate when the periodic broadcaster
// won the shutdown race and already emitted this exact collector window.
func (tr *TestRunner) publishFinalLocalMetrics(test *ManagedTest, snap engine.MetricsSnapshot, lastPublishedSnapshot time.Time, groupVUsers map[string]int, artifacts *runArtifacts) bool {
	if snap.Timestamp.IsZero() || (!lastPublishedSnapshot.IsZero() && !snap.Timestamp.After(lastPublishedSnapshot)) {
		return false
	}
	live := buildLiveMetricsFromSnapshot(snap, groupVUsers)
	live.ActiveVUsers = 0
	for _, group := range live.GroupMetrics {
		if group != nil {
			group.VUsers = 0
		}
	}
	if update, ok := tr.evaluateGoalControllerLocked(test, live, groupVUsers); ok {
		// Retain the terminal evaluation and breach evidence without attempting a
		// late scale after MultiGroupEngine.Run has stopped accepting controls.
		live.GoalStatus = update.Status
	}
	tr.publishLiveMetrics(test, live, artifacts)

	// Broadcast + webhook-route final-drain breaches, but never issue stops
	// here: the engine has already quiesced (see the function contract above).
	tr.evaluateThresholdAlertsAndStops(test, live, false)
	return true
}

// evaluateGoalControllerLocked applies runtime goal targets to live and, for
// goal-oriented runs, evaluates the goal controller — all under tr.mu so the
// reads of test.Groups/GoalConfig/GoalTargets/ScenarioMode and the goalControl
// state machine are serialized with the locked writers (UpdateGoalTargetTPS,
// applyGoalControllerUpdate, updateTestGroupVUsers). The returned scale decision
// must be applied AFTER the lock is released, since applyGoalControllerUpdate
// re-locks internally and issues blocking scale calls.
func (tr *TestRunner) evaluateGoalControllerLocked(test *ManagedTest, live *LiveMetrics, groupVUsers map[string]int) (goalControllerUpdate, bool) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	applyRuntimeGoalTargetsToLiveMetrics(test, live)
	if test.ScenarioMode == "goal_oriented" && test.GoalConfig != nil {
		// Baseline snapshots are deliberately idle and paused snapshots do not
		// represent controllable throughput. Neither may advance breach/cooldown
		// state, accumulate error windows, or trigger an early scale.
		if !tr.testLoadReadyLocked(test, time.Now().UTC()) {
			return goalControllerUpdate{}, false
		}
		recordGoalErrorWindow(test.goalControl, "local", live.Timestamp, live.TotalErrors)
		currentTotalVUsers := totalGroupVUsers(groupVUsers)
		return evaluateGoalController(live.Timestamp, currentTotalVUsers, live, test.GoalConfig, test.goalControl), true
	}
	return goalControllerUpdate{}, false
}

// processMetricsSnapshot is a TEST-HARNESS driver for the shared per-sample
// pipeline (goal controller → publish → evaluateThresholdAlertsAndStops). It
// has no production callers: live local runs sample through broadcastMetrics'
// ticker and distributed runs through consumeMetrics — both of which call the
// same evaluateThresholdAlertsAndStops, with the wiring pinned by
// TestLiveLocalRunEnforcesErrorRateThreshold. Do not add behavior here that is
// not also reachable from those live paths.
func (tr *TestRunner) processMetricsSnapshot(test *ManagedTest, snap engine.MetricsSnapshot, groupVUsers map[string]int, scaleVUsers func(groupName string, target int) error, artifacts *runArtifacts) {
	live := buildLiveMetricsFromSnapshot(snap, groupVUsers)
	if update, ok := tr.evaluateGoalControllerLocked(test, live, groupVUsers); ok {
		applyGoalControllerUpdate(tr, test, live, groupVUsers, update, scaleVUsers)
	}

	tr.publishLiveMetrics(test, live, artifacts)

	// Measure network latency in background if not already measured for this interval
	go tr.measureNetworkLatency(context.Background(), test, live)

	tr.evaluateThresholdAlertsAndStops(test, live, true)
}

// evaluateThresholdAlertsAndStops runs the per-sample global-threshold actions
// on a freshly published live snapshot: broadcast threshold_alert events,
// route them to notification webhooks, and (when allowStops) request the
// error-rate, sustained-P99, and goal-status auto-stops. This is the single
// enforcement point shared by the local broadcast ticker, the distributed
// batch consumers, the final drain (allowStops=false — the engine has already
// quiesced), and processMetricsSnapshot. Callers must not hold tr.mu.
func (tr *TestRunner) evaluateThresholdAlertsAndStops(test *ManagedTest, live *LiveMetrics, allowStops bool) {
	if tr == nil || test == nil || live == nil {
		return
	}
	settings := tr.GetSystemSettings()
	projectID := normalizeProjectID(test.ProjectID)
	alerts := buildThresholdAlerts(test, live, settings.Thresholds)

	// Decide stops before emitting so every alert's "action" reflects what this
	// evaluation actually does: no stop can be requested when allowStops is
	// false (final drain, terminal distributed batches), and the sustained-P99
	// stop must stamp the p99 alert that triggered it.
	stopForError := allowStops && tr.testIsRunning(test) && shouldStopForErrorThreshold(live, settings.Thresholds)
	stopForP99 := allowStops && tr.shouldStopForSustainedP99(test, live, settings.Thresholds)
	applyThresholdAlertStopActions(alerts, stopForError, stopForP99)
	for _, alert := range alerts {
		alert["project_id"] = projectID
		tr.hub.BroadcastScopedJSON(test.ID, projectID, alert)
	}
	tr.routeThresholdAlertNotifications(test, alerts, time.Now())

	if stopForError {
		tr.requestStopIfRunning(test, "threshold stop failed")
	}
	if stopForP99 {
		tr.requestStopIfRunning(test, "p99 threshold stop failed")
	}
	if allowStops && shouldStopForGoalStatus(live) {
		tr.requestStopIfRunning(test, "goal-oriented stop failed")
	}
}

func (tr *TestRunner) broadcastEvent(eventType string, test *ManagedTest) {
	now := time.Now()
	if tr.hub != nil {
		projectID := normalizeProjectID(test.ProjectID)
		tr.hub.BroadcastScopedJSON(test.ID, projectID, map[string]any{
			"type":       eventType,
			"test_id":    test.ID,
			"project_id": projectID,
			"test_name":  test.Name,
			"status":     test.Status,
			"timestamp":  now,
		})
	}
	if tr.notifier != nil {
		tr.notifier.Dispatch(tr.currentNotificationSettings(), NotificationEvent{
			Type:      eventType,
			TestID:    test.ID,
			ProjectID: normalizeProjectID(test.ProjectID),
			TestName:  test.Name,
			Status:    test.Status,
			Timestamp: now,
		})
	}
}

// cloneManagedTestLifecycleEvent returns a lightweight immutable lifecycle
// snapshot used for asynchronous broadcasts. Callers should snapshot while holding
// tr.mu to avoid races with in-progress lifecycle transitions.
func cloneManagedTestLifecycleEvent(test *ManagedTest) *ManagedTest {
	if test == nil {
		return nil
	}
	return &ManagedTest{
		ID:        test.ID,
		ProjectID: test.ProjectID,
		Name:      test.Name,
		Status:    test.Status,
	}
}
