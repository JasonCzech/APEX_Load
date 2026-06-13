package api

// Live-metrics construction and emission: build LiveMetrics from engine
// snapshots, derive transaction/dependency/error-pattern + live-evidence views,
// compute live capacity status, cache run-live-state snapshots, and run the
// publish/broadcast pipeline that fans metrics out to subscribers.

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
)

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
			DroppedResults: snap.Telemetry.DroppedResults,
			CollectedAt:    snap.Timestamp,
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
			VUsers:                 groupVUsers[name],
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

func updateLiveErrorPatterns(existing map[string]*LiveErrorPattern, live *LiveMetrics, testID string) map[string]*LiveErrorPattern {
	if existing == nil {
		existing = make(map[string]*LiveErrorPattern)
	}
	if live == nil || len(live.Transactions) == 0 {
		return existing
	}

	affectedGroups := groupsWithErrors(live.GroupMetrics)
	actions := make([]string, 0, len(live.Transactions))
	for action, tx := range live.Transactions {
		if tx == nil || tx.Errors <= 0 {
			continue
		}
		actions = append(actions, action)
	}
	sort.Strings(actions)
	for _, action := range actions {
		tx := live.Transactions[action]
		source := liveErrorPatternSource(tx)
		signature := normalizeArchiveErrorSignature(source)
		observedAt := tx.LastErrorAt
		if observedAt.IsZero() {
			observedAt = live.Timestamp
		}
		pattern, ok := existing[signature]
		if !ok {
			pattern = &LiveErrorPattern{
				Signature: signature,
				Sample:    source,
				FirstSeen: observedAt,
			}
			existing[signature] = pattern
		}
		pattern.Count += tx.Errors
		pattern.LatestSeen = observedAt
		if pattern.FirstSeen.IsZero() || observedAt.Before(pattern.FirstSeen) {
			pattern.FirstSeen = observedAt
		}
		if pattern.StatusCode == 0 {
			pattern.StatusCode = tx.LastStatusCode
		}
		pattern.ErrorPct = txErrorPct(tx)
		pattern.Actions = appendUniqueSorted(pattern.Actions, action)
		pattern.AffectedGroups = mergeStringSets(pattern.AffectedGroups, affectedGroups)
		pattern.SnapshotURL = liveErrorPatternSnapshotURL(testID, pattern.Actions)
	}
	return existing
}

func liveErrorPatternSource(tx *TxMetrics) string {
	if tx == nil {
		return "transaction failed"
	}
	return liveErrorPatternSourceFromValues(tx.LastErrorMessage, tx.LastStatusCode)
}

func liveErrorPatternSourceFromValues(errorMessage string, statusCode int) string {
	if message := strings.TrimSpace(errorMessage); message != "" {
		return message
	}
	if statusCode > 0 {
		return fmt.Sprintf("request failed with status %d", statusCode)
	}
	return "transaction failed"
}

func liveErrorEvidenceFromResult(result *protocol.Result) LiveErrorEvidence {
	if result == nil || result.Success || result.Snapshot == nil {
		return LiveErrorEvidence{}
	}
	statusCode := result.StatusCode
	if statusCode == 0 && result.Snapshot.ResponseStatus > 0 {
		statusCode = result.Snapshot.ResponseStatus
	}
	source := liveErrorPatternSourceFromValues(result.ErrorMessage, statusCode)
	timestamp := result.StartTime
	if timestamp.IsZero() {
		timestamp = time.Now().UTC()
	}
	return LiveErrorEvidence{
		ID:           "live-evidence-" + uuid.NewString(),
		Signature:    normalizeArchiveErrorSignature(source),
		Action:       strings.TrimSpace(result.ActionName),
		Group:        strings.TrimSpace(result.Group),
		Timestamp:    timestamp,
		DurationMs:   float64(result.Duration) / float64(time.Millisecond),
		Status:       "failed",
		StatusCode:   statusCode,
		ErrorMessage: strings.TrimSpace(result.ErrorMessage),
		Snapshot:     cloneProtocolErrorSnapshot(result.Snapshot),
	}
}

func liveErrorEvidenceFromTransaction(tx generatorrpc.TransactionMetric) LiveErrorEvidence {
	if tx.Success || tx.Snapshot == nil {
		return LiveErrorEvidence{}
	}
	statusCode := int(tx.StatusCode)
	if statusCode == 0 && tx.Snapshot.ResponseStatus > 0 {
		statusCode = tx.Snapshot.ResponseStatus
	}
	source := liveErrorPatternSourceFromValues(tx.ErrorMessage, statusCode)
	timestamp := time.Now().UTC()
	if tx.StartTimeUnixMs > 0 {
		timestamp = time.UnixMilli(tx.StartTimeUnixMs)
	}
	return LiveErrorEvidence{
		ID:           "live-evidence-" + uuid.NewString(),
		Signature:    normalizeArchiveErrorSignature(source),
		Action:       strings.TrimSpace(tx.Name),
		Group:        strings.TrimSpace(tx.Group),
		Timestamp:    timestamp,
		DurationMs:   tx.DurationMs,
		Status:       "failed",
		StatusCode:   statusCode,
		ErrorMessage: strings.TrimSpace(tx.ErrorMessage),
		Snapshot:     cloneProtocolErrorSnapshot(tx.Snapshot),
	}
}

func groupsWithErrors(groups map[string]*GroupLiveMetrics) []string {
	if len(groups) == 0 {
		return nil
	}
	result := make([]string, 0, len(groups))
	for name, group := range groups {
		if group != nil && group.Errors > 0 {
			result = append(result, name)
		}
	}
	sort.Strings(result)
	return result
}

func appendUniqueSorted(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	values = append(values, value)
	sort.Strings(values)
	return values
}

func mergeStringSets(left []string, right []string) []string {
	for _, value := range right {
		left = appendUniqueSorted(left, value)
	}
	return left
}

func liveErrorPatternSnapshotURL(testID string, actions []string) string {
	if strings.TrimSpace(testID) == "" {
		return ""
	}
	query := url.Values{}
	query.Set("success", "false")
	query.Set("limit", "20")
	if len(actions) == 1 {
		query.Set("action", actions[0])
	}
	return fmt.Sprintf("/api/v1/tests/%s/archive/transactions?%s", url.PathEscape(testID), query.Encode())
}

func liveErrorPatternEvidenceURL(testID string, signature string) string {
	if strings.TrimSpace(testID) == "" || strings.TrimSpace(signature) == "" {
		return ""
	}
	query := url.Values{}
	query.Set("signature", signature)
	query.Set("limit", strconv.Itoa(defaultLiveErrorEvidenceLimit))
	return fmt.Sprintf("/api/v1/tests/%s/errors/evidence?%s", url.PathEscape(testID), query.Encode())
}

func liveErrorPatternsList(patterns map[string]*LiveErrorPattern) []LiveErrorPattern {
	if len(patterns) == 0 {
		return nil
	}
	result := make([]LiveErrorPattern, 0, len(patterns))
	for _, pattern := range patterns {
		if pattern == nil {
			continue
		}
		clone := *pattern
		clone.Actions = append([]string(nil), pattern.Actions...)
		clone.AffectedGroups = append([]string(nil), pattern.AffectedGroups...)
		result = append(result, clone)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Count == result[right].Count {
			return result[left].FirstSeen.Before(result[right].FirstSeen)
		}
		return result[left].Count > result[right].Count
	})
	return result
}

func (tr *TestRunner) appendLiveErrorEvidence(test *ManagedTest, evidence []LiveErrorEvidence) {
	if tr == nil || test == nil || len(evidence) == 0 {
		return
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.appendLiveErrorEvidenceLocked(test, evidence)
}

func (tr *TestRunner) appendLiveErrorEvidenceLocked(test *ManagedTest, evidence []LiveErrorEvidence) {
	if test == nil || len(evidence) == 0 {
		return
	}
	if test.liveErrorEvidence == nil {
		test.liveErrorEvidence = make(map[string][]LiveErrorEvidence)
	}
	for _, item := range evidence {
		if strings.TrimSpace(item.Signature) == "" {
			continue
		}
		if item.ID == "" {
			item.ID = "live-evidence-" + uuid.NewString()
		}
		if item.Timestamp.IsZero() {
			item.Timestamp = time.Now().UTC()
		}
		if item.Status == "" {
			item.Status = "failed"
		}
		item.Snapshot = cloneProtocolErrorSnapshot(item.Snapshot)
		items := append(test.liveErrorEvidence[item.Signature], item)
		if len(items) > maxLiveErrorEvidencePerSignature {
			items = items[len(items)-maxLiveErrorEvidencePerSignature:]
		}
		test.liveErrorEvidence[item.Signature] = items
	}
	trimLiveErrorEvidenceLocked(test)
}

func trimLiveErrorEvidenceLocked(test *ManagedTest) {
	if test == nil || len(test.liveErrorEvidence) == 0 {
		return
	}
	total := 0
	for _, items := range test.liveErrorEvidence {
		total += len(items)
	}
	if total <= maxLiveErrorEvidencePerRun {
		return
	}
	signatures := make([]string, 0, len(test.liveErrorEvidence))
	for signature := range test.liveErrorEvidence {
		signatures = append(signatures, signature)
	}
	sort.Slice(signatures, func(left, right int) bool {
		leftItems := test.liveErrorEvidence[signatures[left]]
		rightItems := test.liveErrorEvidence[signatures[right]]
		if len(leftItems) == 0 || len(rightItems) == 0 {
			return len(leftItems) < len(rightItems)
		}
		return leftItems[0].Timestamp.Before(rightItems[0].Timestamp)
	})
	for total > maxLiveErrorEvidencePerRun && len(signatures) > 0 {
		signature := signatures[0]
		items := test.liveErrorEvidence[signature]
		if len(items) <= 1 {
			delete(test.liveErrorEvidence, signature)
			signatures = signatures[1:]
			total -= len(items)
			continue
		}
		test.liveErrorEvidence[signature] = items[1:]
		total--
	}
}

func enrichLiveErrorPatternsWithEvidence(test *ManagedTest, patterns map[string]*LiveErrorPattern) {
	if test == nil || len(patterns) == 0 {
		return
	}
	for signature, pattern := range patterns {
		if pattern == nil {
			continue
		}
		items := test.liveErrorEvidence[signature]
		pattern.EvidenceCount = len(items)
		pattern.LatestEvidenceID = ""
		pattern.EvidenceURL = ""
		if len(items) > 0 {
			pattern.LatestEvidenceID = items[len(items)-1].ID
			pattern.EvidenceURL = liveErrorPatternEvidenceURL(test.ID, signature)
		}
	}
}

func (tr *TestRunner) ListLiveErrorEvidence(testID string, signature string, action string, limit int) ([]LiveErrorEvidence, bool) {
	if tr == nil {
		return nil, false
	}
	if limit <= 0 {
		limit = defaultLiveErrorEvidenceLimit
	}
	if limit > maxLiveErrorEvidenceLimit {
		limit = maxLiveErrorEvidenceLimit
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	testID = tr.resolveRefKeyLocked(testID)
	test, ok := tr.tests[testID]
	if !ok {
		return nil, false
	}
	items := test.liveErrorEvidence[signature]
	if len(items) == 0 {
		return []LiveErrorEvidence{}, true
	}
	filtered := make([]LiveErrorEvidence, 0, minInt(limit, len(items)))
	for i := len(items) - 1; i >= 0 && len(filtered) < limit; i-- {
		item := items[i]
		if action != "" && item.Action != action {
			continue
		}
		item.Snapshot = cloneProtocolErrorSnapshot(item.Snapshot)
		filtered = append(filtered, item)
	}
	return filtered, true
}

func cloneProtocolErrorSnapshot(snapshot *protocol.ErrorSnapshot) *protocol.ErrorSnapshot {
	if snapshot == nil {
		return nil
	}
	cloned := *snapshot
	cloned.RequestHeaders = cloneProtocolStringMap(snapshot.RequestHeaders)
	cloned.ResponseHeaders = cloneProtocolStringMap(snapshot.ResponseHeaders)
	cloned.ConsoleEntries = append([]protocol.ConsoleEntry(nil), snapshot.ConsoleEntries...)
	cloned.NetworkEntries = append([]protocol.NetworkEntry(nil), snapshot.NetworkEntries...)
	if snapshot.DOMSummary != nil {
		domSummary := *snapshot.DOMSummary
		domSummary.VisibleText = append([]string(nil), snapshot.DOMSummary.VisibleText...)
		cloned.DOMSummary = &domSummary
	}
	return &cloned
}

func cloneProtocolStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
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

func (tr *TestRunner) publishLiveMetrics(test *ManagedTest, live *LiveMetrics, artifacts *runArtifacts) {
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
	test.LiveMetrics = live
	test.History = append(test.History, live)
	if len(test.History) > maxManagedTestHistory {
		test.History = append([]*LiveMetrics(nil), test.History[len(test.History)-maxManagedTestHistory:]...)
	}
	live.Telemetry.HistoryPoints = len(test.History)
	if diagnostic != nil {
		publishDiagnostic = shouldPublishTelemetryDiagnostic(&test.telemetryDiagnostic, diagnosticSignature, now)
	}
	snapshot := buildRunLiveStateSnapshotLocked(test, now)
	tr.mu.Unlock()

	tr.cacheRunSnapshot(snapshot)
	tr.saveLiveMetricsHistory(test.ID, live.Timestamp, live)
	artifacts.recordLiveMetrics(test, live)

	tr.hub.BroadcastJSON(map[string]any{
		"type":       "metrics_update",
		"test_id":    test.ID,
		"project_id": normalizeProjectID(test.ProjectID),
		"test_name":  test.Name,
		"timestamp":  live.Timestamp,
		"data":       live,
	})
	if publishDiagnostic {
		tr.hub.BroadcastJSON(map[string]any{
			"type":       "telemetry_diagnostic",
			"test_id":    test.ID,
			"project_id": normalizeProjectID(test.ProjectID),
			"test_name":  test.Name,
			"timestamp":  now,
			"data":       diagnostic,
		})
	}
	tr.broadcastTelemetryHealthTransition(test, previousTelemetryRunHealth, telemetryRunHealth, now)
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
		StartedAt:     test.StartedAt,
		EndedAt:       test.EndedAt,
		LiveMetrics:   test.LiveMetrics,
		HistoryPoints: len(test.History),
	}
}

func (tr *TestRunner) cacheRunSnapshot(snapshot RunLiveStateSnapshot) {
	if tr == nil || strings.TrimSpace(snapshot.TestID) == "" {
		return
	}
	tr.mu.RLock()
	store := tr.liveState
	tr.mu.RUnlock()
	if store == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), defaultCacheWriteTimeout)
		defer cancel()
		if err := store.SetRunSnapshot(ctx, snapshot, defaultRunSnapshotTTL); err != nil {
			slog.Warn("failed to cache live run snapshot", "test_id", snapshot.TestID, "err", err)
		}
	}()
}

func (tr *TestRunner) broadcastMetrics(test *ManagedTest, mge *engine.MultiGroupEngine, artifacts *runArtifacts, done chan struct{}) {
	groupVUsers := make(map[string]int)
	for _, g := range test.Groups {
		groupVUsers[g.Name] = g.VUsers
	}

	intervalSec := tr.GetSystemSettings().General.MetricsIntervalSeconds
	if intervalSec < 1 {
		intervalSec = 1
	}
	ticker := time.NewTicker(time.Duration(intervalSec) * time.Second)
	defer ticker.Stop()

	lastLatencyCheck := time.Time{}

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			snap, ok := mge.LatestSnapshot()
			if !ok {
				continue
			}

			live := buildLiveMetricsForBroadcast(snap, groupVUsers, time.Now())
			applyRuntimeGoalTargetsToLiveMetrics(test, live)

			if test.ScenarioMode == "goal_oriented" && test.GoalConfig != nil {
				currentTotalVUsers := totalGroupVUsers(groupVUsers)
				update := evaluateGoalController(live.Timestamp, currentTotalVUsers, live, test.GoalConfig, test.goalControl)
				applyGoalControllerUpdate(tr, test, live, groupVUsers, update, mge.ScaleVUsers)
			}

			// Throttle network latency pings to every 5 seconds
			if time.Since(lastLatencyCheck) >= 5*time.Second {
				go tr.measureNetworkLatency(test, live)
				lastLatencyCheck = time.Now()
			}

			tr.publishLiveMetrics(test, live, artifacts)
		}
	}
}

func (tr *TestRunner) processMetricsSnapshot(test *ManagedTest, snap engine.MetricsSnapshot, groupVUsers map[string]int, scaleVUsers func(groupName string, target int) error, artifacts *runArtifacts) {
	live := buildLiveMetricsFromSnapshot(snap, groupVUsers)
	applyRuntimeGoalTargetsToLiveMetrics(test, live)

	if test.ScenarioMode == "goal_oriented" && test.GoalConfig != nil {
		currentTotalVUsers := totalGroupVUsers(groupVUsers)
		update := evaluateGoalController(live.Timestamp, currentTotalVUsers, live, test.GoalConfig, test.goalControl)
		applyGoalControllerUpdate(tr, test, live, groupVUsers, update, scaleVUsers)
	}

	tr.publishLiveMetrics(test, live, artifacts)

	// Measure network latency in background if not already measured for this interval
	go tr.measureNetworkLatency(test, live)

	settings := tr.GetSystemSettings()
	for _, alert := range buildThresholdAlerts(test, live, settings.Thresholds) {
		alert["project_id"] = normalizeProjectID(test.ProjectID)
		tr.hub.BroadcastJSON(alert)
	}

	if shouldStopForErrorThreshold(live, settings.Thresholds) {
		tr.requestStopIfRunning(test, "threshold stop failed")
	}

	if shouldStopForGoalStatus(live) {
		tr.requestStopIfRunning(test, "goal-oriented stop failed")
	}
}

func (tr *TestRunner) broadcastEvent(eventType string, test *ManagedTest) {
	now := time.Now()
	if tr.hub != nil {
		tr.hub.BroadcastJSON(map[string]any{
			"type":       eventType,
			"test_id":    test.ID,
			"project_id": normalizeProjectID(test.ProjectID),
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
