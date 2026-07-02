package api

// distributed_metrics.go — split from distributed_execution_backend.go (same package, behavior-preserving).

import (
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/engine"
)

// aggregateDistributedMetricsBatches merges per-generator batches into one
// combined batch. It also returns the fleet TPS and per-(raw-)group TPS computed
// as a sum of per-generator rates: TPS is a per-second rate and is additive
// across generators, but each generator's success count must be divided by its
// OWN window before summing. The combined transaction list collapses to a single
// representative window, so these rates are computed here, before concatenation.
func aggregateDistributedMetricsBatches(testID string, batches map[string]generatorrpc.MetricsBatch) (generatorrpc.MetricsBatch, float64, map[string]float64) {
	combined := generatorrpc.MetricsBatch{TestID: testID, GeneratorID: "distributed"}
	targets := make([]string, 0, len(batches))
	hasCollectedAt := false
	hasPublishedAt := false
	hasControllerReceivedAt := false
	groupsByName := make(map[string]int32)
	groupMetricsByName := make(map[string][]generatorrpc.MetricAggregate)
	fleetTPS := 0.0
	groupTPS := make(map[string]float64)
	statsList := make([]generatorrpc.GeneratorStats, 0, len(batches))
	fleetMetrics := make([]generatorrpc.MetricAggregate, 0, len(batches))
	transactionMetrics := make([]generatorrpc.MetricAggregate, 0)
	dependencyMetrics := make([]generatorrpc.MetricAggregate, 0)
	soapMetrics := make([]generatorrpc.MetricAggregate, 0)
	networkProfileMetrics := make([]generatorrpc.MetricAggregate, 0)
	browserMetrics := make([]*generatorrpc.BrowserMetrics, 0)
	browserDeviceMetrics := make([]generatorrpc.BrowserDeviceMetrics, 0)
	for target := range batches {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	for _, target := range targets {
		batch := batches[target]
		if batch.TimestampUnixMs > combined.TimestampUnixMs {
			combined.TimestampUnixMs = batch.TimestampUnixMs
		}
		if batch.WindowMs > combined.WindowMs {
			combined.WindowMs = batch.WindowMs
		}
		statsList = append(statsList, batch.Stats)
		combined.Transactions = append(combined.Transactions, batch.Transactions...)
		if batch.FleetMetrics != nil {
			fleetMetrics = append(fleetMetrics, *batch.FleetMetrics)
		}
		transactionMetrics = append(transactionMetrics, batch.TransactionMetrics...)
		dependencyMetrics = append(dependencyMetrics, batch.DependencyMetrics...)
		soapMetrics = append(soapMetrics, batch.SOAPMetrics...)
		networkProfileMetrics = append(networkProfileMetrics, batch.NetworkProfileMetrics...)
		if batch.Browser != nil {
			browserMetrics = append(browserMetrics, batch.Browser)
		}
		browserDeviceMetrics = append(browserDeviceMetrics, batch.BrowserByDevice...)
		windowSec := batchWindowSeconds(batch.WindowMs)
		batchSuccess := 0.0
		batchGroupSuccess := make(map[string]float64)
		if batch.FleetMetrics != nil && batch.FleetMetrics.Count > 0 {
			batchSuccess = float64(batch.FleetMetrics.Count - batch.FleetMetrics.Errors)
			if batch.FleetMetrics.TPS > 0 {
				fleetTPS += batch.FleetMetrics.TPS
			} else {
				fleetTPS += batchSuccess / windowSec
			}
		} else {
			for _, tx := range batch.Transactions {
				if tx.Success {
					batchSuccess++
					batchGroupSuccess[strings.TrimSpace(tx.Group)]++
				}
			}
			fleetTPS += batchSuccess / windowSec
		}
		for groupName, success := range batchGroupSuccess {
			groupTPS[groupName] += success / windowSec
		}
		for _, group := range batch.Groups {
			if group.Name == "" {
				continue
			}
			groupsByName[group.Name] += group.ActiveVUsers
			if group.Metrics != nil && group.Metrics.Count > 0 {
				groupMetricsByName[group.Name] = append(groupMetricsByName[group.Name], *group.Metrics)
				if group.Metrics.TPS > 0 {
					groupTPS[group.Name] += group.Metrics.TPS
				} else {
					groupTPS[group.Name] += float64(group.Metrics.Count-group.Metrics.Errors) / windowSec
				}
			}
		}
		if batch.Telemetry != nil {
			if combined.Telemetry == nil {
				combined.Telemetry = &generatorrpc.MetricsTelemetry{}
			}
			combined.Telemetry.DroppedResults += batch.Telemetry.DroppedResults
			combined.Telemetry.SheddedArrivals += batch.Telemetry.SheddedArrivals
			if batch.Telemetry.CollectedAtUnixMs > 0 && (!hasCollectedAt || batch.Telemetry.CollectedAtUnixMs < combined.Telemetry.CollectedAtUnixMs) {
				combined.Telemetry.CollectedAtUnixMs = batch.Telemetry.CollectedAtUnixMs
				hasCollectedAt = true
			}
			if batch.Telemetry.PublishedAtUnixMs > 0 && (!hasPublishedAt || batch.Telemetry.PublishedAtUnixMs > combined.Telemetry.PublishedAtUnixMs) {
				combined.Telemetry.PublishedAtUnixMs = batch.Telemetry.PublishedAtUnixMs
				hasPublishedAt = true
			}
			if batch.Telemetry.ControllerReceivedAtUnixMs > 0 && (!hasControllerReceivedAt || batch.Telemetry.ControllerReceivedAtUnixMs > combined.Telemetry.ControllerReceivedAtUnixMs) {
				combined.Telemetry.ControllerReceivedAtUnixMs = batch.Telemetry.ControllerReceivedAtUnixMs
				hasControllerReceivedAt = true
			}
			if batch.Telemetry.SampleAgeMs > combined.Telemetry.SampleAgeMs {
				combined.Telemetry.SampleAgeMs = batch.Telemetry.SampleAgeMs
			}
			if batch.Telemetry.GeneratorProcessingLagMs > combined.Telemetry.GeneratorProcessingLagMs {
				combined.Telemetry.GeneratorProcessingLagMs = batch.Telemetry.GeneratorProcessingLagMs
			}
			if batch.Telemetry.TransportLagMs > combined.Telemetry.TransportLagMs {
				combined.Telemetry.TransportLagMs = batch.Telemetry.TransportLagMs
			}
			combined.Telemetry.DroppedSubscriberBatches += batch.Telemetry.DroppedSubscriberBatches
		}
	}
	combined.FleetMetrics = mergeGeneratorMetricAggregates(fleetMetrics)
	combined.TransactionMetrics = mergeGeneratorMetricAggregateSeries(transactionMetrics)
	combined.DependencyMetrics = mergeGeneratorMetricAggregateSeries(dependencyMetrics)
	combined.SOAPMetrics = mergeGeneratorMetricAggregateSeries(soapMetrics)
	combined.NetworkProfileMetrics = mergeGeneratorMetricAggregateSeries(networkProfileMetrics)
	combined.Browser = mergeGeneratorBrowserMetrics(browserMetrics)
	combined.BrowserByDevice = mergeGeneratorBrowserDeviceMetrics(browserDeviceMetrics)
	groupNames := make([]string, 0, len(groupsByName))
	for name := range groupsByName {
		groupNames = append(groupNames, name)
	}
	sort.Strings(groupNames)
	for _, name := range groupNames {
		combined.Groups = append(combined.Groups, generatorrpc.GroupMetric{
			Name:         name,
			ActiveVUsers: groupsByName[name],
			Metrics:      mergeGeneratorMetricAggregates(groupMetricsByName[name]),
		})
	}
	combined.Stats = aggregateDistributedGeneratorStats(statsList)
	return combined, fleetTPS, groupTPS
}

// batchWindowSeconds converts a batch window (ms) to seconds for rate math.
// A non-positive window (older generator, or first drain) falls back to 1.0s,
// preserving the historical count≈rate behavior at the 1s publish cadence.
func batchWindowSeconds(windowMs int64) float64 {
	if windowMs <= 0 {
		return 1.0
	}
	return float64(windowMs) / 1000.0
}

func summarizeGeneratorBatchTransactions(transactions []generatorrpc.TransactionMetric, windowMs int64) (float64, float64, float64, float64) {
	if len(transactions) == 0 {
		return 0, 0, 0, 0
	}
	durations := make([]float64, 0, len(transactions))
	errors := 0
	successes := 0
	for _, tx := range transactions {
		durations = append(durations, tx.DurationMs)
		if !tx.Success {
			errors++
		} else {
			successes++
		}
	}
	sort.Float64s(durations)
	errorPct := float64(errors) / float64(len(transactions)) * 100
	tps := float64(successes) / batchWindowSeconds(windowMs)
	return percentileValue(durations, 0.95), percentileValue(durations, 0.99), errorPct, tps
}

func buildLiveMetricsFromDistributedBatch(test *ManagedTest, batch generatorrpc.MetricsBatch, fleetTPS float64, groupTPS map[string]float64) *LiveMetrics {
	timestamp := time.UnixMilli(batch.TimestampUnixMs)
	if batch.TimestampUnixMs == 0 {
		timestamp = time.Now()
	}

	live := &LiveMetrics{
		Timestamp:        timestamp,
		ActiveVUsers:     int64(batch.Stats.ActiveVUsers),
		Transactions:     make(map[string]*TxMetrics),
		DependencyHealth: make(map[string]*TxMetrics),
		SOAPOperations:   make(map[string]*TxMetrics),
		NetworkProfiles:  make(map[string]*TxMetrics),
		GroupMetrics:     make(map[string]*GroupLiveMetrics),
		Telemetry:        &TelemetryHealth{},
		Resources: &engine.SystemResources{
			CPUProcessPct: batch.Stats.CPUPercent,
			MemProcessMb:  float64(batch.Stats.MemoryBytes) / (1024 * 1024),
		},
	}
	live.Resources = resourcesFromGeneratorStats(batch.Stats)
	if batch.Telemetry != nil {
		live.Telemetry.DroppedResults = batch.Telemetry.DroppedResults
		live.Telemetry.SheddedArrivals = batch.Telemetry.SheddedArrivals
		live.Telemetry.DroppedSubscriberBatches = batch.Telemetry.DroppedSubscriberBatches
		if batch.Telemetry.CollectedAtUnixMs > 0 {
			live.Telemetry.CollectedAt = time.UnixMilli(batch.Telemetry.CollectedAtUnixMs)
		}
		if batch.Telemetry.PublishedAtUnixMs > 0 {
			live.Telemetry.GeneratorPublishedAt = time.UnixMilli(batch.Telemetry.PublishedAtUnixMs)
		}
		if batch.Telemetry.ControllerReceivedAtUnixMs > 0 {
			live.Telemetry.ControllerReceivedAt = time.UnixMilli(batch.Telemetry.ControllerReceivedAtUnixMs)
		}
		if batch.Telemetry.GeneratorProcessingLagMs > 0 {
			live.Telemetry.GeneratorProcessingLagMs = batch.Telemetry.GeneratorProcessingLagMs
		} else if !live.Telemetry.CollectedAt.IsZero() && !live.Telemetry.GeneratorPublishedAt.IsZero() {
			live.Telemetry.GeneratorProcessingLagMs = float64(live.Telemetry.GeneratorPublishedAt.Sub(live.Telemetry.CollectedAt).Microseconds()) / 1000.0
		}
		if batch.Telemetry.TransportLagMs > 0 {
			live.Telemetry.TransportLagMs = batch.Telemetry.TransportLagMs
		} else if !live.Telemetry.GeneratorPublishedAt.IsZero() && !live.Telemetry.ControllerReceivedAt.IsZero() {
			live.Telemetry.TransportLagMs = float64(live.Telemetry.ControllerReceivedAt.Sub(live.Telemetry.GeneratorPublishedAt).Microseconds()) / 1000.0
		}
		live.Telemetry.SampleAgeMs = batch.Telemetry.SampleAgeMs
	}
	if live.Telemetry.CollectedAt.IsZero() {
		live.Telemetry.CollectedAt = timestamp
	}
	if applyDistributedAggregateMetrics(test, live, batch, fleetTPS, groupTPS) {
		return live
	}

	durationsByName := make(map[string][]float64)
	durationsByDependencyTarget := make(map[string][]float64)
	durationsBySOAPOperation := make(map[string][]float64)
	durationsByGroup := make(map[string][]float64)
	groupAggregates := make(map[string]*GroupLiveMetrics)
	transactionAttempts := int64(0)
	var totalDNSMs, totalConnectMs, totalTLSMs, totalTTFBMs, totalWaitMs float64
	errorBreakdown := make(map[string]int64)
	for _, tx := range batch.Transactions {
		transactionAttempts++
		if !tx.Success {
			live.TotalErrors++
			if class := engine.ClassifyError(int(tx.StatusCode), tx.ErrorMessage, false); class != "" {
				errorBreakdown[class]++
			}
		} else {
			live.TotalTx++
		}
		live.AvgResponseMs += tx.DurationMs
		totalDNSMs += tx.DNSMs
		totalConnectMs += tx.ConnectMs
		totalTLSMs += tx.TLSMs
		totalTTFBMs += tx.TTFBMs
		if wait := tx.DurationMs - tx.TTFBMs; wait > 0 {
			totalWaitMs += wait
		}
		durationsByName[tx.Name] = append(durationsByName[tx.Name], tx.DurationMs)
		if !tx.Success && tx.Snapshot != nil {
			evidence := liveErrorEvidenceFromTransaction(tx)
			if evidence.Signature != "" {
				live.ErrorEvidence = append(live.ErrorEvidence, evidence)
			}
		}
		groupName := strings.TrimSpace(tx.Group)
		if groupName == "" && len(test.Groups) == 1 {
			groupName = test.Groups[0].Name
		}
		if groupName != "" {
			groupMetrics := groupAggregates[groupName]
			if groupMetrics == nil {
				groupMetrics = &GroupLiveMetrics{Name: groupName}
				groupAggregates[groupName] = groupMetrics
			}
			if !tx.Success {
				groupMetrics.Errors++
			} else {
				groupMetrics.Transactions++
			}
			groupMetrics.AvgResponseMs += tx.DurationMs
			durationsByGroup[groupName] = append(durationsByGroup[groupName], tx.DurationMs)
		}
		metrics := live.Transactions[tx.Name]
		if metrics == nil {
			metrics = &TxMetrics{Name: tx.Name}
			live.Transactions[tx.Name] = metrics
		}
		mergeDistributedTransactionMetrics(metrics, tx)

		if dependencyTarget := strings.TrimSpace(tx.DependencyTarget); dependencyTarget != "" {
			durationsByDependencyTarget[dependencyTarget] = append(durationsByDependencyTarget[dependencyTarget], tx.DurationMs)
			dependencyMetrics := live.DependencyHealth[dependencyTarget]
			if dependencyMetrics == nil {
				dependencyMetrics = &TxMetrics{Name: dependencyTarget}
				live.DependencyHealth[dependencyTarget] = dependencyMetrics
			}
			mergeDistributedTransactionMetrics(dependencyMetrics, tx)
		}

		if operation := strings.TrimSpace(tx.SOAPOperation); operation != "" {
			soapMetrics := live.SOAPOperations[operation]
			if soapMetrics == nil {
				soapMetrics = &TxMetrics{Name: operation}
				live.SOAPOperations[operation] = soapMetrics
			}
			durationsBySOAPOperation[operation] = append(durationsBySOAPOperation[operation], tx.DurationMs)
			mergeDistributedTransactionMetrics(soapMetrics, tx)
		}
	}

	if transactionAttempts > 0 {
		attempts := float64(transactionAttempts)
		live.ErrorPct = float64(live.TotalErrors) / attempts * 100
		live.AvgResponseMs /= attempts
		live.AvgDNSMs = totalDNSMs / attempts
		live.AvgConnectMs = totalConnectMs / attempts
		live.AvgTLSMs = totalTLSMs / attempts
		live.AvgTTFBMs = totalTTFBMs / attempts
		live.AvgWaitMs = totalWaitMs / attempts
	}
	live.ErrorBreakdown = copyErrorClassCounts(errorBreakdown)
	// TPS is a true per-second rate summed across generators (see
	// aggregateDistributedMetricsBatches), not a per-batch transaction count.
	live.TPS = fleetTPS

	// Resolve raw per-group rates to configured group names, applying the same
	// single-group fallback used when bucketing transactions below.
	resolvedGroupTPS := make(map[string]float64, len(groupTPS))
	for rawGroup, tps := range groupTPS {
		name := strings.TrimSpace(rawGroup)
		if name == "" && len(test.Groups) == 1 {
			name = test.Groups[0].Name
		}
		if name != "" {
			resolvedGroupTPS[name] += tps
		}
	}

	names := make([]string, 0, len(durationsByName))
	allDurations := make([]float64, 0, len(batch.Transactions))
	for name, durations := range durationsByName {
		names = append(names, name)
		sort.Float64s(durations)
		allDurations = append(allDurations, durations...)
		finalizeDistributedTxMetrics(live.Transactions[name], durations)
	}
	for dependencyTarget, durations := range durationsByDependencyTarget {
		sort.Float64s(durations)
		finalizeDistributedTxMetrics(live.DependencyHealth[dependencyTarget], durations)
	}
	for operation, durations := range durationsBySOAPOperation {
		sort.Float64s(durations)
		finalizeDistributedTxMetrics(live.SOAPOperations[operation], durations)
	}
	for groupName, durations := range durationsByGroup {
		sort.Float64s(durations)
		metrics := groupAggregates[groupName]
		if metrics == nil {
			continue
		}
		attempts := metrics.Transactions + metrics.Errors
		if attempts > 0 {
			metrics.AvgResponseMs /= float64(attempts)
		}
		metrics.TPS = resolvedGroupTPS[groupName]
		metrics.P50Ms, metrics.P90Ms, metrics.P95Ms, metrics.P99Ms = percentilesFromSorted(durations)
		digest := engine.NewTDigest(200)
		for _, duration := range durations {
			digest.Add(duration * 1_000_000)
		}
		digestSnapshot := digest.Snapshot()
		metrics.DurationDigest = &digestSnapshot
	}
	sort.Strings(names)
	if len(allDurations) > 0 {
		sort.Float64s(allDurations)
		live.P50Ms, live.P90Ms, live.P95Ms, live.P99Ms = percentilesFromSorted(allDurations)
	}
	if len(batch.Groups) > 0 {
		for _, group := range batch.Groups {
			groupMetrics := groupAggregates[group.Name]
			if groupMetrics == nil {
				groupMetrics = &GroupLiveMetrics{Name: group.Name}
			}
			groupMetrics.VUsers = int(group.ActiveVUsers)
			live.GroupMetrics[group.Name] = groupMetrics
		}
		for groupName, groupMetrics := range groupAggregates {
			if _, ok := live.GroupMetrics[groupName]; !ok {
				live.GroupMetrics[groupName] = groupMetrics
			}
		}
	} else if len(groupAggregates) > 0 {
		configuredVUsers := make(map[string]int, len(test.Groups))
		for _, group := range test.Groups {
			configuredVUsers[group.Name] = group.VUsers
		}
		for groupName, groupMetrics := range groupAggregates {
			groupMetrics.VUsers = configuredVUsers[groupName]
			live.GroupMetrics[groupName] = groupMetrics
		}
	} else if len(test.Groups) == 1 {
		groupName := test.Groups[0].Name
		live.GroupMetrics[groupName] = &GroupLiveMetrics{
			Name:          groupName,
			VUsers:        int(batch.Stats.ActiveVUsers),
			Transactions:  live.TotalTx,
			Errors:        live.TotalErrors,
			TPS:           live.TPS,
			AvgResponseMs: live.AvgResponseMs,
			P50Ms:         live.P50Ms,
			P90Ms:         live.P90Ms,
			P95Ms:         live.P95Ms,
			P99Ms:         live.P99Ms,
		}
	}

	return live
}

func applyDistributedAggregateMetrics(test *ManagedTest, live *LiveMetrics, batch generatorrpc.MetricsBatch, fleetTPS float64, groupTPS map[string]float64) bool {
	if batch.FleetMetrics == nil || batch.FleetMetrics.Count == 0 {
		return false
	}
	fleet := batch.FleetMetrics
	live.TotalErrors = fleet.Errors
	live.TotalTx = fleet.Count - fleet.Errors
	if fleet.Count > 0 {
		live.ErrorPct = float64(fleet.Errors) / float64(fleet.Count) * 100
	}
	live.TPS = fleetTPS
	if live.TPS == 0 {
		live.TPS = fleet.TPS
	}
	live.AvgResponseMs = fleet.AvgMs
	live.P50Ms = fleet.P50Ms
	live.P90Ms = fleet.P90Ms
	live.P95Ms = fleet.P95Ms
	live.P99Ms = fleet.P99Ms
	live.AvgDNSMs = fleet.AvgDNSMs
	live.AvgConnectMs = fleet.AvgConnectMs
	live.AvgTLSMs = fleet.AvgTLSMs
	live.AvgTTFBMs = fleet.AvgTTFBMs
	if wait := fleet.AvgMs - fleet.AvgTTFBMs; wait > 0 {
		live.AvgWaitMs = wait
	}

	for _, aggregate := range batch.TransactionMetrics {
		metrics := txMetricsFromAggregate(aggregate)
		if metrics != nil {
			live.Transactions[metrics.Name] = metrics
		}
	}
	for _, aggregate := range batch.DependencyMetrics {
		metrics := txMetricsFromAggregate(aggregate)
		if metrics != nil {
			live.DependencyHealth[metrics.Name] = metrics
		}
	}
	live.DependencyMap = buildLiveDependencyMap(live.DependencyHealth)
	for _, aggregate := range batch.SOAPMetrics {
		metrics := txMetricsFromAggregate(aggregate)
		if metrics != nil {
			live.SOAPOperations[metrics.Name] = metrics
		}
	}
	for _, aggregate := range batch.NetworkProfileMetrics {
		metrics := txMetricsFromAggregate(aggregate)
		if metrics != nil {
			live.NetworkProfiles[metrics.Name] = metrics
		}
	}
	if batch.Browser != nil {
		live.Browser = browserLiveMetricsFromGenerator(*batch.Browser)
	}
	if len(batch.BrowserByDevice) > 0 {
		live.BrowserByDevice = make(map[string]*BrowserDeviceLiveMetrics, len(batch.BrowserByDevice))
		for _, metrics := range batch.BrowserByDevice {
			live.BrowserByDevice[metrics.DeviceProfile] = browserDeviceLiveMetricsFromGenerator(metrics)
		}
	}

	resolvedGroupTPS := make(map[string]float64, len(groupTPS))
	for rawGroup, tps := range groupTPS {
		name := strings.TrimSpace(rawGroup)
		if name == "" && len(test.Groups) == 1 {
			name = test.Groups[0].Name
		}
		if name != "" {
			resolvedGroupTPS[name] += tps
		}
	}
	for _, group := range batch.Groups {
		groupLive := &GroupLiveMetrics{Name: group.Name, VUsers: int(group.ActiveVUsers)}
		if group.Metrics != nil && group.Metrics.Count > 0 {
			groupLive.Transactions = group.Metrics.Count - group.Metrics.Errors
			groupLive.Errors = group.Metrics.Errors
			groupLive.TPS = resolvedGroupTPS[group.Name]
			if groupLive.TPS == 0 {
				groupLive.TPS = group.Metrics.TPS
			}
			groupLive.AvgResponseMs = group.Metrics.AvgMs
			groupLive.P50Ms = group.Metrics.P50Ms
			groupLive.P90Ms = group.Metrics.P90Ms
			groupLive.P95Ms = group.Metrics.P95Ms
			groupLive.P99Ms = group.Metrics.P99Ms
			groupLive.DurationDigest = group.Metrics.DurationDigest
		}
		live.GroupMetrics[group.Name] = groupLive
	}
	if len(live.GroupMetrics) == 0 && len(test.Groups) == 1 {
		groupName := test.Groups[0].Name
		live.GroupMetrics[groupName] = &GroupLiveMetrics{
			Name:           groupName,
			VUsers:         int(batch.Stats.ActiveVUsers),
			Transactions:   live.TotalTx,
			Errors:         live.TotalErrors,
			TPS:            live.TPS,
			AvgResponseMs:  live.AvgResponseMs,
			P50Ms:          live.P50Ms,
			P90Ms:          live.P90Ms,
			P95Ms:          live.P95Ms,
			P99Ms:          live.P99Ms,
			DurationDigest: fleet.DurationDigest,
		}
	}

	errorBreakdown := make(map[string]int64)
	for _, tx := range batch.Transactions {
		if tx.Success {
			continue
		}
		if class := engine.ClassifyError(int(tx.StatusCode), tx.ErrorMessage, false); class != "" {
			errorBreakdown[class]++
		}
		if tx.Snapshot != nil {
			evidence := liveErrorEvidenceFromTransaction(tx)
			if evidence.Signature != "" {
				live.ErrorEvidence = append(live.ErrorEvidence, evidence)
			}
		}
	}
	live.ErrorBreakdown = copyErrorClassCounts(errorBreakdown)
	return true
}

func txMetricsFromAggregate(aggregate generatorrpc.MetricAggregate) *TxMetrics {
	if strings.TrimSpace(aggregate.Name) == "" || aggregate.Count == 0 {
		return nil
	}
	return &TxMetrics{
		Name:             aggregate.Name,
		DependencyType:   aggregate.DependencyType,
		DependencyTarget: aggregate.DependencyTarget,
		AvgDNSMs:         aggregate.AvgDNSMs,
		AvgConnectMs:     aggregate.AvgConnectMs,
		AvgTLSMs:         aggregate.AvgTLSMs,
		AvgTTFBMs:        aggregate.AvgTTFBMs,
		Count:            aggregate.Count,
		Errors:           aggregate.Errors,
		AvgMs:            aggregate.AvgMs,
		P50Ms:            aggregate.P50Ms,
		P90Ms:            aggregate.P90Ms,
		P95Ms:            aggregate.P95Ms,
		P99Ms:            aggregate.P99Ms,
	}
}

func browserLiveMetricsFromGenerator(metrics generatorrpc.BrowserMetrics) *BrowserLiveMetrics {
	if metrics.Samples == 0 {
		return nil
	}
	return &BrowserLiveMetrics{
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

func browserDeviceLiveMetricsFromGenerator(metrics generatorrpc.BrowserDeviceMetrics) *BrowserDeviceLiveMetrics {
	if metrics.Metrics.Samples == 0 {
		return nil
	}
	return &BrowserDeviceLiveMetrics{
		DeviceProfile:        metrics.DeviceProfile,
		DevicePlatform:       metrics.DevicePlatform,
		DeviceTier:           metrics.DeviceTier,
		BrowserEngine:        metrics.BrowserEngine,
		Samples:              metrics.Metrics.Samples,
		AvgFCPMs:             metrics.Metrics.AvgFCPMs,
		AvgLCPMs:             metrics.Metrics.AvgLCPMs,
		AvgCLSScore:          metrics.Metrics.AvgCLSScore,
		AvgINPMs:             metrics.Metrics.AvgINPMs,
		AvgTTFBMs:            metrics.Metrics.AvgTTFBMs,
		AvgBlockingResources: metrics.Metrics.AvgBlockingResources,
		AvgLongTaskCount:     metrics.Metrics.AvgLongTaskCount,
		AvgJSHeapUsedMB:      metrics.Metrics.AvgJSHeapUsedMB,
		AvgBrowserProcessCPU: metrics.Metrics.AvgBrowserProcessCPU,
		AvgBrowserProcessMem: metrics.Metrics.AvgBrowserProcessMem,
	}
}

func mergeDistributedTransactionMetrics(metrics *TxMetrics, tx generatorrpc.TransactionMetric) {
	if metrics == nil {
		return
	}
	metrics.Count++
	if !tx.Success {
		metrics.Errors++
		metrics.LastErrorMessage = tx.ErrorMessage
		metrics.LastStatusCode = int(tx.StatusCode)
		if tx.StartTimeUnixMs > 0 {
			metrics.LastErrorAt = time.UnixMilli(tx.StartTimeUnixMs)
		} else {
			metrics.LastErrorAt = time.Now()
		}
	}
	mergeTransactionMetricMetadata(metrics, tx.DependencyType, tx.DependencyTarget, tx.CorrelationID)
	mergeDistributedServerTelemetry(metrics, tx.ServerTelemetry)
	metrics.AvgDNSMs += tx.DNSMs
	metrics.AvgConnectMs += tx.ConnectMs
	metrics.AvgTLSMs += tx.TLSMs
	metrics.AvgTTFBMs += tx.TTFBMs
	metrics.AvgMs += tx.DurationMs
}

func mergeTransactionMetricMetadata(metrics *TxMetrics, dependencyType string, dependencyTarget string, correlationID string) {
	if metrics == nil {
		return
	}
	mergeStableTxMetadata(&metrics.DependencyType, dependencyType)
	mergeStableTxMetadata(&metrics.DependencyTarget, dependencyTarget)
	if correlationID == "" {
		return
	}
	metrics.CorrelationCount++
	for _, existing := range metrics.CorrelationIDs {
		if existing == correlationID {
			return
		}
	}
	if len(metrics.CorrelationIDs) < 3 {
		metrics.CorrelationIDs = append(metrics.CorrelationIDs, correlationID)
	}
}

func mergeStableTxMetadata(current *string, next string) {
	if current == nil || next == "" {
		return
	}
	if *current == "" {
		*current = next
		return
	}
	if *current != next {
		*current = "multiple"
	}
}

func cloneEngineServerTelemetrySummary(summary *engine.ServerTelemetrySummary) *ServerTelemetrySummary {
	if summary == nil {
		return nil
	}
	return &ServerTelemetrySummary{
		Samples:                   summary.Samples,
		AvgServerProcessingMs:     summary.AvgServerProcessingMs,
		AvgTransferMs:             summary.AvgTransferMs,
		AvgRequestPayloadKB:       summary.AvgRequestPayloadKB,
		AvgResponsePayloadKB:      summary.AvgResponsePayloadKB,
		RequestPayloadHistogram:   cloneTelemetryClassCounts(summary.RequestPayloadHistogram),
		ResponsePayloadHistogram:  cloneTelemetryClassCounts(summary.ResponsePayloadHistogram),
		AvgFirstEventMs:           summary.AvgFirstEventMs,
		MaxFirstEventMs:           summary.MaxFirstEventMs,
		EventCount:                summary.EventCount,
		AvgMessageLatencyMs:       summary.AvgMessageLatencyMs,
		MaxMessageLatencyMs:       summary.MaxMessageLatencyMs,
		MessagesSent:              summary.MessagesSent,
		MessagesReceived:          summary.MessagesReceived,
		AvgMessagesSentPerSec:     summary.AvgMessagesSentPerSec,
		AvgMessagesReceivedPerSec: summary.AvgMessagesReceivedPerSec,
		AvgConsumerLagMs:          summary.AvgConsumerLagMs,
		MaxConsumerLagMs:          summary.MaxConsumerLagMs,
		AvgConsumerOffsetLag:      summary.AvgConsumerOffsetLag,
		MaxConsumerOffsetLag:      summary.MaxConsumerOffsetLag,
		SendFailureCount:          summary.SendFailureCount,
		RedeliveryCount:           summary.RedeliveryCount,
		DeadLetteredCount:         summary.DeadLetteredCount,
		DuplicateCount:            summary.DuplicateCount,
		OutOfOrderCount:           summary.OutOfOrderCount,
		CommitCount:               summary.CommitCount,
		AbortCount:                summary.AbortCount,
		RedeliveryRatePct:         summary.RedeliveryRatePct,
		DeadLetterRatePct:         summary.DeadLetterRatePct,
		DuplicateRatePct:          summary.DuplicateRatePct,
		TotalThrottledRequests:    summary.TotalThrottledRequests,
		TotalServerBusyCount:      summary.TotalServerBusyCount,
		TotalQuotaExceededCount:   summary.TotalQuotaExceededCount,
		TotalEventTooLargeCount:   summary.TotalEventTooLargeCount,
		AvgEndToEndLatencyMs:      summary.AvgEndToEndLatencyMs,
		MaxEndToEndLatencyMs:      summary.MaxEndToEndLatencyMs,
		PartitionEvents:           cloneTelemetryClassCounts(summary.PartitionEvents),
		PartitionSkewPct:          summary.PartitionSkewPct,
		RequestSizeClasses:        cloneTelemetryClassCounts(summary.RequestSizeClasses),
		ResponseSizeClasses:       cloneTelemetryClassCounts(summary.ResponseSizeClasses),
		ConnectionReusedCount:     summary.ConnectionReusedCount,
		ConnectionReusedPct:       summary.ConnectionReusedPct,
	}
}

func mergeDistributedServerTelemetry(metrics *TxMetrics, telemetry *generatorrpc.ServerTelemetry) {
	if metrics == nil || telemetry == nil {
		return
	}
	if metrics.ServerTelemetry == nil {
		metrics.ServerTelemetry = &ServerTelemetrySummary{}
	}
	// Reliability counters are run totals and accumulate for every record,
	// including control-only ops (commit/rollback) that carry no measured sample.
	metrics.ServerTelemetry.SendFailureCount += int64(telemetry.SendFailureCount)
	metrics.ServerTelemetry.RedeliveryCount += int64(telemetry.RedeliveryCount)
	metrics.ServerTelemetry.DeadLetteredCount += int64(telemetry.DeadLetteredCount)
	metrics.ServerTelemetry.DuplicateCount += int64(telemetry.DuplicateCount)
	metrics.ServerTelemetry.OutOfOrderCount += int64(telemetry.OutOfOrderCount)
	metrics.ServerTelemetry.CommitCount += int64(telemetry.CommitCount)
	metrics.ServerTelemetry.AbortCount += int64(telemetry.AbortCount)
	// Streaming-ingestion counters are run totals too (EH.4).
	metrics.ServerTelemetry.TotalThrottledRequests += int64(telemetry.ThrottledRequests)
	metrics.ServerTelemetry.TotalServerBusyCount += int64(telemetry.ServerBusyCount)
	metrics.ServerTelemetry.TotalQuotaExceededCount += int64(telemetry.QuotaExceededCount)
	metrics.ServerTelemetry.TotalEventTooLargeCount += int64(telemetry.EventTooLargeCount)
	// Control-only ops must not contribute to the averaging divisor or they would
	// dilute the average payload/latency/throughput.
	if !generatorTelemetryHasMeasuredSample(telemetry) {
		return
	}
	metrics.ServerTelemetry.Samples++
	metrics.ServerTelemetry.AvgServerProcessingMs += telemetry.ServerProcessingMs
	metrics.ServerTelemetry.AvgTransferMs += telemetry.TransferMs
	metrics.ServerTelemetry.AvgRequestPayloadKB += telemetry.RequestPayloadKB
	metrics.ServerTelemetry.AvgResponsePayloadKB += telemetry.ResponsePayloadKB
	incrementTelemetryHistogramCount(&metrics.ServerTelemetry.RequestPayloadHistogram, telemetry.RequestPayloadKB)
	incrementTelemetryHistogramCount(&metrics.ServerTelemetry.ResponsePayloadHistogram, telemetry.ResponsePayloadKB)
	metrics.ServerTelemetry.AvgFirstEventMs += telemetry.FirstEventMs
	if telemetry.FirstEventMs > metrics.ServerTelemetry.MaxFirstEventMs {
		metrics.ServerTelemetry.MaxFirstEventMs = telemetry.FirstEventMs
	}
	metrics.ServerTelemetry.EventCount += int64(telemetry.EventCount)
	metrics.ServerTelemetry.AvgMessageLatencyMs += telemetry.MessageLatencyMs
	if telemetry.MessageLatencyMs > metrics.ServerTelemetry.MaxMessageLatencyMs {
		metrics.ServerTelemetry.MaxMessageLatencyMs = telemetry.MessageLatencyMs
	}
	metrics.ServerTelemetry.MessagesSent += int64(telemetry.MessagesSent)
	metrics.ServerTelemetry.MessagesReceived += int64(telemetry.MessagesReceived)
	metrics.ServerTelemetry.AvgMessagesSentPerSec += telemetry.MessagesSentPerSec
	metrics.ServerTelemetry.AvgMessagesReceivedPerSec += telemetry.MessagesReceivedPerSec
	metrics.ServerTelemetry.AvgConsumerLagMs += telemetry.ConsumerLagMs
	if telemetry.ConsumerLagMs > metrics.ServerTelemetry.MaxConsumerLagMs {
		metrics.ServerTelemetry.MaxConsumerLagMs = telemetry.ConsumerLagMs
	}
	metrics.ServerTelemetry.AvgConsumerOffsetLag += float64(telemetry.ConsumerOffsetLag)
	if telemetry.ConsumerOffsetLag > metrics.ServerTelemetry.MaxConsumerOffsetLag {
		metrics.ServerTelemetry.MaxConsumerOffsetLag = telemetry.ConsumerOffsetLag
	}
	metrics.ServerTelemetry.AvgEndToEndLatencyMs += telemetry.EndToEndLatencyMs
	if telemetry.EndToEndLatencyMs > metrics.ServerTelemetry.MaxEndToEndLatencyMs {
		metrics.ServerTelemetry.MaxEndToEndLatencyMs = telemetry.EndToEndLatencyMs
	}
	// Bucket produce volume per partition across generators (skew source).
	// Producer-only: a consume probe leaves MessagesSent == 0 and is excluded.
	if telemetry.PartitionID != "" && telemetry.MessagesSent > 0 {
		if metrics.ServerTelemetry.PartitionEvents == nil {
			metrics.ServerTelemetry.PartitionEvents = make(map[string]int64)
		}
		metrics.ServerTelemetry.PartitionEvents[telemetry.PartitionID] += int64(telemetry.MessagesSent)
	}
	incrementTelemetryClassCount(&metrics.ServerTelemetry.RequestSizeClasses, telemetry.RequestSizeClass)
	incrementTelemetryClassCount(&metrics.ServerTelemetry.ResponseSizeClasses, telemetry.ResponseSizeClass)
	if telemetry.ConnectionReused {
		metrics.ServerTelemetry.ConnectionReusedCount++
	}
}

// generatorTelemetryHasMeasuredSample mirrors protocol.ServerTelemetry.HasMeasuredSample
// for the gRPC transport struct: it reports whether the record carries a measured
// transfer/latency/payload sample (vs. a control-only op carrying only reliability
// counters), so control ops do not dilute the averaged telemetry.
func generatorTelemetryHasMeasuredSample(t *generatorrpc.ServerTelemetry) bool {
	if t == nil {
		return false
	}
	return t.ServerProcessingMs != 0 || t.TransferMs != 0 || t.RequestPayloadKB != 0 ||
		t.ResponsePayloadKB != 0 || t.FirstEventMs != 0 || t.EventCount != 0 ||
		t.MessageLatencyMs != 0 || t.MessagesSent != 0 || t.MessagesReceived != 0 ||
		t.MessagesSentPerSec != 0 || t.MessagesReceivedPerSec != 0 || t.ConsumerLagMs != 0 ||
		t.ConsumerOffsetLag != 0 || t.ThrottledRequests != 0 || t.ServerBusyCount != 0 ||
		t.QuotaExceededCount != 0 || t.EventTooLargeCount != 0 || t.EndToEndLatencyMs != 0 ||
		t.RequestSizeClass != "" || t.ResponseSizeClass != "" || t.ConnectionReused
}

func finalizeAPIServerTelemetrySummary(summary *ServerTelemetrySummary) {
	if summary == nil || summary.Samples == 0 {
		return
	}
	divisor := float64(summary.Samples)
	summary.AvgServerProcessingMs /= divisor
	summary.AvgTransferMs /= divisor
	summary.AvgRequestPayloadKB /= divisor
	summary.AvgResponsePayloadKB /= divisor
	summary.AvgFirstEventMs /= divisor
	summary.AvgMessageLatencyMs /= divisor
	summary.AvgMessagesSentPerSec /= divisor
	summary.AvgMessagesReceivedPerSec /= divisor
	summary.AvgConsumerLagMs /= divisor
	summary.AvgConsumerOffsetLag /= divisor
	summary.AvgEndToEndLatencyMs /= divisor
	// Skew is recomputed POST-merge from the pooled per-partition map, not averaged
	// from per-generator skews (the min/max may come from different generators).
	summary.PartitionSkewPct = engine.ComputePartitionSkewPct(summary.PartitionEvents)
	// Reliability rates recomputed from pooled counts (not averaged per-generator).
	summary.RedeliveryRatePct = engine.MessagingRatePct(summary.RedeliveryCount, summary.MessagesReceived)
	summary.DeadLetterRatePct = engine.MessagingRatePct(summary.DeadLetteredCount, summary.MessagesReceived)
	summary.DuplicateRatePct = engine.MessagingRatePct(summary.DuplicateCount, summary.MessagesReceived)
	summary.ConnectionReusedPct = float64(summary.ConnectionReusedCount) / divisor * 100
	if len(summary.PartitionEvents) == 0 {
		summary.PartitionEvents = nil
	}
	if len(summary.RequestSizeClasses) == 0 {
		summary.RequestSizeClasses = nil
	}
	if len(summary.ResponseSizeClasses) == 0 {
		summary.ResponseSizeClasses = nil
	}
	if len(summary.RequestPayloadHistogram) == 0 {
		summary.RequestPayloadHistogram = nil
	}
	if len(summary.ResponsePayloadHistogram) == 0 {
		summary.ResponsePayloadHistogram = nil
	}
}

func cloneTelemetryClassCounts(in map[string]int64) map[string]int64 {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]int64, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func incrementTelemetryHistogramCount(values *map[string]int64, payloadKB float64) {
	if values == nil {
		return
	}
	key := payloadHistogramBucket(payloadKB)
	if key == "" {
		return
	}
	if *values == nil {
		*values = make(map[string]int64)
	}
	(*values)[key]++
}

func payloadHistogramBucket(payloadKB float64) string {
	switch {
	case payloadKB <= 0:
		return "0 KB"
	case payloadKB < 1:
		return "0-1 KB"
	case payloadKB < 4:
		return "1-4 KB"
	case payloadKB < 16:
		return "4-16 KB"
	case payloadKB < 64:
		return "16-64 KB"
	default:
		return "64+ KB"
	}
}

func incrementTelemetryClassCount(values *map[string]int64, key string) {
	if values == nil || key == "" {
		return
	}
	if *values == nil {
		*values = make(map[string]int64)
	}
	(*values)[key]++
}

func percentileValue(sorted []float64, percentile float64) float64 {
	return percentileFromSortedSamples(sorted, percentile)
}

// percentilesFromSorted returns the P50/P90/P95/P99 of a pre-sorted sample set,
// keeping the percentile set defined in a single place.
func percentilesFromSorted(sorted []float64) (p50, p90, p95, p99 float64) {
	return percentileValue(sorted, 0.50), percentileValue(sorted, 0.90), percentileValue(sorted, 0.95), percentileValue(sorted, 0.99)
}

// finalizeDistributedTxMetrics averages the per-attempt accumulators by Count,
// finalizes the server-telemetry summary, and assigns percentiles from the
// already-sorted durations. Shared by the per-transaction, per-dependency-target
// and per-SOAP-operation finalize loops.
func finalizeDistributedTxMetrics(metrics *TxMetrics, sortedDurations []float64) {
	metrics.AvgDNSMs /= float64(metrics.Count)
	metrics.AvgConnectMs /= float64(metrics.Count)
	metrics.AvgTLSMs /= float64(metrics.Count)
	metrics.AvgTTFBMs /= float64(metrics.Count)
	metrics.AvgMs /= float64(metrics.Count)
	finalizeAPIServerTelemetrySummary(metrics.ServerTelemetry)
	metrics.P50Ms, metrics.P90Ms, metrics.P95Ms, metrics.P99Ms = percentilesFromSorted(sortedDurations)
}

func mergeGeneratorMetricAggregateSeries(values []generatorrpc.MetricAggregate) []generatorrpc.MetricAggregate {
	if len(values) == 0 {
		return nil
	}
	byName := make(map[string][]generatorrpc.MetricAggregate)
	for _, value := range values {
		name := strings.TrimSpace(value.Name)
		if name == "" {
			continue
		}
		byName[name] = append(byName[name], value)
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]generatorrpc.MetricAggregate, 0, len(names))
	for _, name := range names {
		if aggregate := mergeGeneratorMetricAggregates(byName[name]); aggregate != nil {
			out = append(out, *aggregate)
		}
	}
	return out
}

func mergeGeneratorMetricAggregates(values []generatorrpc.MetricAggregate) *generatorrpc.MetricAggregate {
	if len(values) == 0 {
		return nil
	}
	out := generatorrpc.MetricAggregate{}
	var durationDigest *engine.TDigest
	var avgMsSum, dnsSum, connectSum, tlsSum, ttfbSum float64
	for _, value := range values {
		if value.Count == 0 {
			continue
		}
		if out.Name == "" {
			out.Name = value.Name
		}
		mergeStableTxMetadata(&out.DependencyType, value.DependencyType)
		mergeStableTxMetadata(&out.DependencyTarget, value.DependencyTarget)
		count := float64(value.Count)
		out.Count += value.Count
		out.Errors += value.Errors
		avgMsSum += value.AvgMs * count
		dnsSum += value.AvgDNSMs * count
		connectSum += value.AvgConnectMs * count
		tlsSum += value.AvgTLSMs * count
		ttfbSum += value.AvgTTFBMs * count
		out.BytesSent += value.BytesSent
		out.BytesReceived += value.BytesReceived
		out.TPS += value.TPS
		if value.DurationDigest != nil && value.DurationDigest.Count > 0 {
			if durationDigest == nil {
				durationDigest = engine.NewTDigest(value.DurationDigest.Compression)
			}
			durationDigest.Merge(engine.NewTDigestFromSnapshot(*value.DurationDigest))
		}
	}
	if out.Count == 0 {
		return nil
	}
	divisor := float64(out.Count)
	out.AvgMs = avgMsSum / divisor
	out.AvgDNSMs = dnsSum / divisor
	out.AvgConnectMs = connectSum / divisor
	out.AvgTLSMs = tlsSum / divisor
	out.AvgTTFBMs = ttfbSum / divisor
	if durationDigest != nil && durationDigest.Count() > 0 {
		out.P50Ms = durationDigest.Quantile(0.50) / float64(time.Millisecond)
		out.P90Ms = durationDigest.Quantile(0.90) / float64(time.Millisecond)
		out.P95Ms = durationDigest.Quantile(0.95) / float64(time.Millisecond)
		out.P99Ms = durationDigest.Quantile(0.99) / float64(time.Millisecond)
		snapshot := durationDigest.Snapshot()
		out.DurationDigest = &snapshot
	} else {
		out.P50Ms, out.P90Ms, out.P95Ms, out.P99Ms = weightedAggregatePercentiles(values)
	}
	return &out
}

func weightedAggregatePercentiles(values []generatorrpc.MetricAggregate) (float64, float64, float64, float64) {
	total := int64(0)
	for _, value := range values {
		total += value.Count
	}
	if total == 0 {
		return 0, 0, 0, 0
	}
	weighted := func(pick func(generatorrpc.MetricAggregate) float64) float64 {
		sum := 0.0
		for _, value := range values {
			sum += pick(value) * float64(value.Count)
		}
		return sum / float64(total)
	}
	return weighted(func(v generatorrpc.MetricAggregate) float64 { return v.P50Ms }),
		weighted(func(v generatorrpc.MetricAggregate) float64 { return v.P90Ms }),
		weighted(func(v generatorrpc.MetricAggregate) float64 { return v.P95Ms }),
		weighted(func(v generatorrpc.MetricAggregate) float64 { return v.P99Ms })
}

func mergeGeneratorBrowserMetrics(values []*generatorrpc.BrowserMetrics) *generatorrpc.BrowserMetrics {
	if len(values) == 0 {
		return nil
	}
	out := &generatorrpc.BrowserMetrics{}
	for _, value := range values {
		if value == nil || value.Samples == 0 {
			continue
		}
		addGeneratorBrowserMetrics(out, *value)
	}
	finalizeGeneratorBrowserMetrics(out)
	if out.Samples == 0 {
		return nil
	}
	return out
}

func mergeGeneratorBrowserDeviceMetrics(values []generatorrpc.BrowserDeviceMetrics) []generatorrpc.BrowserDeviceMetrics {
	if len(values) == 0 {
		return nil
	}
	byProfile := make(map[string]*generatorrpc.BrowserDeviceMetrics)
	for _, value := range values {
		profile := strings.TrimSpace(value.DeviceProfile)
		if profile == "" || value.Metrics.Samples == 0 {
			continue
		}
		out := byProfile[profile]
		if out == nil {
			out = &generatorrpc.BrowserDeviceMetrics{
				DeviceProfile:  profile,
				DevicePlatform: value.DevicePlatform,
				DeviceTier:     value.DeviceTier,
				BrowserEngine:  value.BrowserEngine,
			}
			byProfile[profile] = out
		}
		if out.DevicePlatform == "" {
			out.DevicePlatform = value.DevicePlatform
		}
		if out.DeviceTier == "" {
			out.DeviceTier = value.DeviceTier
		}
		if out.BrowserEngine == "" {
			out.BrowserEngine = value.BrowserEngine
		}
		addGeneratorBrowserMetrics(&out.Metrics, value.Metrics)
	}
	profiles := make([]string, 0, len(byProfile))
	for profile := range byProfile {
		profiles = append(profiles, profile)
	}
	sort.Strings(profiles)
	out := make([]generatorrpc.BrowserDeviceMetrics, 0, len(profiles))
	for _, profile := range profiles {
		metrics := byProfile[profile]
		finalizeGeneratorBrowserMetrics(&metrics.Metrics)
		out = append(out, *metrics)
	}
	return out
}

func addGeneratorBrowserMetrics(out *generatorrpc.BrowserMetrics, value generatorrpc.BrowserMetrics) {
	if out == nil || value.Samples == 0 {
		return
	}
	samples := float64(value.Samples)
	out.Samples += value.Samples
	out.AvgFCPMs += value.AvgFCPMs * samples
	out.AvgLCPMs += value.AvgLCPMs * samples
	out.AvgCLSScore += value.AvgCLSScore * samples
	out.AvgINPMs += value.AvgINPMs * samples
	out.AvgTTFBMs += value.AvgTTFBMs * samples
	out.AvgBlockingResources += value.AvgBlockingResources * samples
	out.AvgLongTaskCount += value.AvgLongTaskCount * samples
	out.AvgJSHeapUsedMB += value.AvgJSHeapUsedMB * samples
	out.AvgBrowserProcessCPU += value.AvgBrowserProcessCPU * samples
	out.AvgBrowserProcessMem += value.AvgBrowserProcessMem * samples
}

func finalizeGeneratorBrowserMetrics(out *generatorrpc.BrowserMetrics) {
	if out == nil || out.Samples == 0 {
		return
	}
	divisor := float64(out.Samples)
	out.AvgFCPMs /= divisor
	out.AvgLCPMs /= divisor
	out.AvgCLSScore /= divisor
	out.AvgINPMs /= divisor
	out.AvgTTFBMs /= divisor
	out.AvgBlockingResources /= divisor
	out.AvgLongTaskCount /= divisor
	out.AvgJSHeapUsedMB /= divisor
	out.AvgBrowserProcessCPU /= divisor
	out.AvgBrowserProcessMem /= divisor
}
