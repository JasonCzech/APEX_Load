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
	regionMetrics := make([]generatorrpc.MetricAggregate, 0)
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
		windowSec := batchWindowSeconds(batch.WindowMs)

		hasTransactionAggregates := false
		batchTransactionMetrics := make([]generatorrpc.MetricAggregate, 0, len(batch.TransactionMetrics))
		for _, aggregate := range batch.TransactionMetrics {
			name := strings.TrimSpace(aggregate.Name)
			if name == "" || aggregate.Count <= 0 {
				continue
			}
			hasTransactionAggregates = true
			aggregate.Name = name
			aggregate.Errors = boundedMetricErrors(aggregate.Count, aggregate.Errors)
			aggregate.TPS = resolvedAggregateTPS(aggregate, windowSec)
			batchTransactionMetrics = append(batchTransactionMetrics, aggregate)
			transactionMetrics = append(transactionMetrics, aggregate)
		}

		hasFleetAggregate := batch.FleetMetrics != nil && batch.FleetMetrics.Count > 0
		hasDependencyAggregates := hasUsableMetricAggregateSeries(batch.DependencyMetrics)
		hasSOAPAggregates := hasUsableMetricAggregateSeries(batch.SOAPMetrics)
		hasGroupAggregates := hasUsableGroupMetricAggregateSeries(batch.Groups)
		var rawFallback rawDistributedMetricFallbacks
		if needsRawDistributedMetricFallback(batch, hasTransactionAggregates, hasDependencyAggregates, hasSOAPAggregates, hasGroupAggregates) {
			rawFallback = buildRawDistributedMetricFallbacks(batch.Transactions, batch.WindowMs)
		}
		if hasFleetAggregate {
			fleet := *batch.FleetMetrics
			fleet.Errors = boundedMetricErrors(fleet.Count, fleet.Errors)
			fleet.TPS = resolvedAggregateTPS(fleet, windowSec)
			fleetMetrics = append(fleetMetrics, fleet)
			fleetTPS += fleet.TPS
		} else if hasTransactionAggregates {
			fleet := mergeGeneratorMetricAggregates(batchTransactionMetrics)
			if fleet != nil {
				fleet.Name = "fleet"
				fleetMetrics = append(fleetMetrics, *fleet)
				fleetTPS += fleet.TPS
			}
		} else if rawFallback.Fleet != nil {
			fleetMetrics = append(fleetMetrics, *rawFallback.Fleet)
			fleetTPS += rawFallback.Fleet.TPS
		}

		// Raw transactions are retained alongside modern aggregates as bounded
		// exemplars. Fall back only when the whole per-transaction aggregate
		// capability is absent: high-cardinality modern senders intentionally fold
		// excess names into [other], so name-by-name fallback would double count
		// those exemplars and defeat the generator's series bound.
		if !hasTransactionAggregates {
			transactionMetrics = append(transactionMetrics, rawFallback.Transactions...)
		}
		if !hasDependencyAggregates {
			dependencyMetrics = append(dependencyMetrics, rawFallback.Dependencies...)
		}
		if !hasSOAPAggregates {
			soapMetrics = append(soapMetrics, rawFallback.SOAP...)
		}
		dependencyMetrics = append(dependencyMetrics, batch.DependencyMetrics...)
		soapMetrics = append(soapMetrics, batch.SOAPMetrics...)
		networkProfileMetrics = append(networkProfileMetrics, batch.NetworkProfileMetrics...)
		regionMetrics = append(regionMetrics, batch.RegionMetrics...)
		if batch.Browser != nil {
			browserMetrics = append(browserMetrics, batch.Browser)
		}
		browserDeviceMetrics = append(browserDeviceMetrics, batch.BrowserByDevice...)

		soleBatchGroup := ""
		multipleBatchGroups := false
		for _, group := range batch.Groups {
			name := strings.TrimSpace(group.Name)
			if name == "" {
				continue
			}
			if soleBatchGroup == "" {
				soleBatchGroup = name
			} else if soleBatchGroup != name {
				multipleBatchGroups = true
				break
			}
		}
		resolvedRawGroups := make(map[string][]generatorrpc.MetricAggregate, len(rawFallback.Groups))
		if !hasGroupAggregates {
			for rawName, aggregate := range rawFallback.Groups {
				name := rawName
				if name == "" && soleBatchGroup != "" && !multipleBatchGroups {
					name = soleBatchGroup
					aggregate.Name = name
				}
				resolvedRawGroups[name] = append(resolvedRawGroups[name], aggregate)
			}
		}
		accountedGroups := make(map[string]struct{}, len(batch.Groups))
		for _, group := range batch.Groups {
			name := strings.TrimSpace(group.Name)
			if name == "" {
				continue
			}
			groupsByName[name] += group.ActiveVUsers
			if group.Metrics != nil && group.Metrics.Count > 0 {
				metrics := *group.Metrics
				metrics.Name = name
				metrics.Errors = boundedMetricErrors(metrics.Count, metrics.Errors)
				metrics.TPS = resolvedAggregateTPS(metrics, windowSec)
				groupMetricsByName[name] = append(groupMetricsByName[name], metrics)
				groupTPS[name] += metrics.TPS
				accountedGroups[name] = struct{}{}
				continue
			}
			if rawGroups := resolvedRawGroups[name]; len(rawGroups) > 0 {
				groupMetricsByName[name] = append(groupMetricsByName[name], rawGroups...)
				for _, metrics := range rawGroups {
					groupTPS[name] += metrics.TPS
				}
				accountedGroups[name] = struct{}{}
			}
		}
		for name, rawGroups := range resolvedRawGroups {
			if _, accounted := accountedGroups[name]; accounted {
				continue
			}
			for _, metrics := range rawGroups {
				groupTPS[name] += metrics.TPS
			}
			if name == "" {
				continue
			}
			if _, exists := groupsByName[name]; !exists {
				groupsByName[name] = 0
			}
			groupMetricsByName[name] = append(groupMetricsByName[name], rawGroups...)
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
			combined.Telemetry.MissingMetricWindows += batch.Telemetry.MissingMetricWindows
		}
	}
	combined.FleetMetrics = mergeGeneratorMetricAggregates(fleetMetrics)
	combined.TransactionMetrics = mergeGeneratorMetricAggregateSeries(transactionMetrics)
	combined.DependencyMetrics = mergeGeneratorMetricAggregateSeries(dependencyMetrics)
	combined.SOAPMetrics = mergeGeneratorMetricAggregateSeries(soapMetrics)
	combined.NetworkProfileMetrics = mergeGeneratorMetricAggregateSeries(networkProfileMetrics)
	combined.RegionMetrics = mergeGeneratorMetricAggregateSeries(regionMetrics)
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

func hasUsableMetricAggregateSeries(values []generatorrpc.MetricAggregate) bool {
	for _, value := range values {
		if strings.TrimSpace(value.Name) != "" && value.Count > 0 {
			return true
		}
	}
	return false
}

func hasUsableGroupMetricAggregateSeries(values []generatorrpc.GroupMetric) bool {
	for _, value := range values {
		if strings.TrimSpace(value.Name) != "" && value.Metrics != nil && value.Metrics.Count > 0 {
			return true
		}
	}
	return false
}

func needsRawDistributedMetricFallback(batch generatorrpc.MetricsBatch, hasTransactions, hasDependencies, hasSOAP, hasGroups bool) bool {
	if len(batch.Transactions) == 0 {
		return false
	}
	if !hasTransactions {
		return true
	}
	soleBatchGroup := ""
	multipleBatchGroups := false
	for _, group := range batch.Groups {
		name := strings.TrimSpace(group.Name)
		if name == "" {
			continue
		}
		if soleBatchGroup == "" {
			soleBatchGroup = name
		} else if soleBatchGroup != name {
			multipleBatchGroups = true
			break
		}
	}
	for _, transaction := range batch.Transactions {
		if !hasDependencies && strings.TrimSpace(transaction.DependencyTarget) != "" {
			return true
		}
		if !hasSOAP && strings.TrimSpace(transaction.SOAPOperation) != "" {
			return true
		}
		if !hasGroups && (strings.TrimSpace(transaction.Group) != "" || (soleBatchGroup != "" && !multipleBatchGroups)) {
			return true
		}
	}
	return false
}

func boundedMetricErrors(count, errors int64) int64 {
	if count <= 0 || errors <= 0 {
		return 0
	}
	if errors >= count {
		return count
	}
	return errors
}

// successfulMetricCount protects every rate/count projection from malformed or
// mixed-version aggregates whose error count exceeds their attempt count.
func successfulMetricCount(count, errors int64) int64 {
	if count <= 0 {
		return 0
	}
	return count - boundedMetricErrors(count, errors)
}

func resolvedAggregateTPS(aggregate generatorrpc.MetricAggregate, windowSec float64) float64 {
	successes := successfulMetricCount(aggregate.Count, aggregate.Errors)
	if successes == 0 {
		return 0
	}
	if aggregate.TPS > 0 {
		return aggregate.TPS
	}
	if windowSec <= 0 {
		windowSec = 1
	}
	return float64(successes) / windowSec
}

type rawMetricAccumulator struct {
	metric    generatorrpc.MetricAggregate
	digest    *engine.TDigest
	durations []float64
}

func newRawMetricAccumulator(name string) *rawMetricAccumulator {
	return &rawMetricAccumulator{
		metric: generatorrpc.MetricAggregate{Name: name},
		digest: engine.NewTDigest(200),
	}
}

func (a *rawMetricAccumulator) add(tx generatorrpc.TransactionMetric) {
	if a == nil {
		return
	}
	a.metric.Count++
	if !tx.Success {
		a.metric.Errors++
	}
	mergeStableTxMetadata(&a.metric.DependencyType, tx.DependencyType)
	mergeStableTxMetadata(&a.metric.DependencyTarget, tx.DependencyTarget)
	a.metric.AvgMs += tx.DurationMs
	a.metric.AvgDNSMs += tx.DNSMs
	a.metric.AvgConnectMs += tx.ConnectMs
	a.metric.AvgTLSMs += tx.TLSMs
	a.metric.AvgTTFBMs += tx.TTFBMs
	a.metric.BytesSent += tx.BytesSent
	a.metric.BytesReceived += tx.BytesReceived
	a.durations = append(a.durations, tx.DurationMs)
	a.digest.Add(tx.DurationMs * float64(time.Millisecond))
}

func (a *rawMetricAccumulator) finalize(windowSec float64) *generatorrpc.MetricAggregate {
	if a == nil || a.metric.Count == 0 {
		return nil
	}
	metric := a.metric
	count := float64(metric.Count)
	metric.AvgMs /= count
	metric.AvgDNSMs /= count
	metric.AvgConnectMs /= count
	metric.AvgTLSMs /= count
	metric.AvgTTFBMs /= count
	metric.TPS = float64(successfulMetricCount(metric.Count, metric.Errors)) / windowSec
	sort.Float64s(a.durations)
	metric.P50Ms, metric.P90Ms, metric.P95Ms, metric.P99Ms = percentilesFromSorted(a.durations)
	digestSnapshot := a.digest.Snapshot()
	metric.DurationDigest = &digestSnapshot
	return &metric
}

type rawDistributedMetricFallbacks struct {
	Fleet        *generatorrpc.MetricAggregate
	Transactions []generatorrpc.MetricAggregate
	Dependencies []generatorrpc.MetricAggregate
	SOAP         []generatorrpc.MetricAggregate
	Groups       map[string]generatorrpc.MetricAggregate
}

const (
	// Keep these aligned with generatorrpc's transaction-window series bound and
	// overflow label so legacy fallback behaves like an aggregate-capable sender.
	maxDistributedMetricSeries = 256
	distributedOverflowSeries  = "[other]"
)

func boundedRawMetricAccumulator(values map[string]*rawMetricAccumulator, name string) *rawMetricAccumulator {
	if accumulator := values[name]; accumulator != nil {
		return accumulator
	}
	if len(values) >= maxDistributedMetricSeries-1 {
		name = distributedOverflowSeries
		if accumulator := values[name]; accumulator != nil {
			return accumulator
		}
	}
	accumulator := newRawMetricAccumulator(name)
	values[name] = accumulator
	return accumulator
}

// buildRawDistributedMetricFallbacks reconstructs aggregate evidence for a
// legacy generator batch in one pass. Its output is authoritative only for a
// component whose aggregate capability is absent from that batch.
func buildRawDistributedMetricFallbacks(transactions []generatorrpc.TransactionMetric, windowMs int64) rawDistributedMetricFallbacks {
	if len(transactions) == 0 {
		return rawDistributedMetricFallbacks{}
	}
	fleet := newRawMetricAccumulator("fleet")
	transactionsByName := make(map[string]*rawMetricAccumulator)
	dependenciesByName := make(map[string]*rawMetricAccumulator)
	soapByName := make(map[string]*rawMetricAccumulator)
	groupsByName := make(map[string]*rawMetricAccumulator)
	for _, tx := range transactions {
		fleet.add(tx)
		if name := strings.TrimSpace(tx.Name); name != "" {
			boundedRawMetricAccumulator(transactionsByName, name).add(tx)
		}
		if target := strings.TrimSpace(tx.DependencyTarget); target != "" {
			boundedRawMetricAccumulator(dependenciesByName, target).add(tx)
		}
		if operation := strings.TrimSpace(tx.SOAPOperation); operation != "" {
			boundedRawMetricAccumulator(soapByName, operation).add(tx)
		}
		groupName := strings.TrimSpace(tx.Group)
		boundedRawMetricAccumulator(groupsByName, groupName).add(tx)
	}

	windowSec := batchWindowSeconds(windowMs)
	result := rawDistributedMetricFallbacks{
		Fleet:  fleet.finalize(windowSec),
		Groups: make(map[string]generatorrpc.MetricAggregate, len(groupsByName)),
	}
	result.Transactions = finalizeRawMetricAccumulatorSeries(transactionsByName, windowSec)
	result.Dependencies = finalizeRawMetricAccumulatorSeries(dependenciesByName, windowSec)
	result.SOAP = finalizeRawMetricAccumulatorSeries(soapByName, windowSec)
	for name, accumulator := range groupsByName {
		if aggregate := accumulator.finalize(windowSec); aggregate != nil {
			result.Groups[name] = *aggregate
		}
	}
	return result
}

func finalizeRawMetricAccumulatorSeries(values map[string]*rawMetricAccumulator, windowSec float64) []generatorrpc.MetricAggregate {
	if len(values) == 0 {
		return nil
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]generatorrpc.MetricAggregate, 0, len(names))
	for _, name := range names {
		if aggregate := values[name].finalize(windowSec); aggregate != nil {
			result = append(result, *aggregate)
		}
	}
	return result
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
		TransactionResponseTimeSLA: remoteTransactionResponseTimeSLAResults(test),
		Timestamp:                  timestamp,
		SampleWindowSeconds:        batchWindowSeconds(batch.WindowMs),
		ActiveVUsers:               int64(batch.Stats.ActiveVUsers),
		Transactions:               make(map[string]*TxMetrics),
		DependencyHealth:           make(map[string]*TxMetrics),
		SOAPOperations:             make(map[string]*TxMetrics),
		NetworkProfiles:            make(map[string]*TxMetrics),
		ByRegion:                   make(map[string]*TxMetrics),
		GroupMetrics:               make(map[string]*GroupLiveMetrics),
		Telemetry:                  &TelemetryHealth{},
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
		live.Telemetry.MissingMetricWindows = batch.Telemetry.MissingMetricWindows
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
		if tx.Snapshot != nil {
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
	transactionTPS := make(map[string]float64, len(batch.TransactionMetrics))
	for _, aggregate := range batch.TransactionMetrics {
		transactionTPS[aggregate.Name] = aggregate.TPS
	}
	allDurations := make([]float64, 0, len(batch.Transactions))
	for name, durations := range durationsByName {
		names = append(names, name)
		sort.Float64s(durations)
		allDurations = append(allDurations, durations...)
		finalizeDistributedTxMetrics(live.Transactions[name], durations)
		if tps, ok := transactionTPS[name]; ok {
			live.Transactions[name].TPS = tps
		} else {
			live.Transactions[name].TPS = float64(successfulMetricCount(live.Transactions[name].Count, live.Transactions[name].Errors)) / batchWindowSeconds(batch.WindowMs)
		}
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
	live.TotalErrors = boundedMetricErrors(fleet.Count, fleet.Errors)
	live.TotalTx = successfulMetricCount(fleet.Count, fleet.Errors)
	if fleet.Count > 0 {
		live.ErrorPct = float64(live.TotalErrors) / float64(fleet.Count) * 100
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
	for _, aggregate := range batch.RegionMetrics {
		metrics := txMetricsFromAggregate(aggregate)
		if metrics != nil {
			live.ByRegion[metrics.Name] = metrics
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
			groupLive.Transactions = successfulMetricCount(group.Metrics.Count, group.Metrics.Errors)
			groupLive.Errors = boundedMetricErrors(group.Metrics.Count, group.Metrics.Errors)
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
		mergeDistributedAggregateTransactionDetails(metricForAggregateSeries(live.Transactions, tx.Name), tx)
		if dependencyTarget := strings.TrimSpace(tx.DependencyTarget); dependencyTarget != "" {
			mergeDistributedAggregateTransactionDetails(metricForAggregateSeries(live.DependencyHealth, dependencyTarget), tx)
		}
		if operation := strings.TrimSpace(tx.SOAPOperation); operation != "" {
			mergeDistributedAggregateTransactionDetails(metricForAggregateSeries(live.SOAPOperations, operation), tx)
		}
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
	for _, metricsByName := range []map[string]*TxMetrics{live.Transactions, live.DependencyHealth, live.SOAPOperations} {
		for _, metrics := range metricsByName {
			finalizeAPIServerTelemetrySummary(metrics.ServerTelemetry)
		}
	}
	live.ErrorBreakdown = copyErrorClassCounts(errorBreakdown)
	return true
}

func metricForAggregateSeries(values map[string]*TxMetrics, name string) *TxMetrics {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	if metrics := values[name]; metrics != nil {
		return metrics
	}
	return values[distributedOverflowSeries]
}

// mergeDistributedAggregateTransactionDetails enriches authoritative aggregate
// counts/rates with diagnostic fields that exist only on retained raw samples.
// It deliberately does not change count, error, latency, or TPS accumulators.
func mergeDistributedAggregateTransactionDetails(metrics *TxMetrics, tx generatorrpc.TransactionMetric) {
	if metrics == nil {
		return
	}
	if !tx.Success {
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
}

func txMetricsFromAggregate(aggregate generatorrpc.MetricAggregate) *TxMetrics {
	if strings.TrimSpace(aggregate.Name) == "" || aggregate.Count == 0 {
		return nil
	}
	return &TxMetrics{
		Name:             aggregate.Name,
		TPS:              aggregate.TPS,
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
		if name == "" || value.Count <= 0 {
			continue
		}
		byName[name] = append(byName[name], value)
	}
	if len(byName) == 0 {
		return nil
	}

	names := make([]string, 0, len(byName))
	for name := range byName {
		if name != distributedOverflowSeries {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	overflowValues := append([]generatorrpc.MetricAggregate(nil), byName[distributedOverflowSeries]...)
	if len(names) > maxDistributedMetricSeries-1 {
		for _, name := range names[maxDistributedMetricSeries-1:] {
			overflowValues = append(overflowValues, byName[name]...)
		}
		names = names[:maxDistributedMetricSeries-1]
	}
	if len(overflowValues) > 0 {
		names = append(names, distributedOverflowSeries)
		sort.Strings(names)
	}

	out := make([]generatorrpc.MetricAggregate, 0, min(len(names), maxDistributedMetricSeries))
	for _, name := range names {
		series := byName[name]
		if name == distributedOverflowSeries {
			series = overflowValues
		}
		if aggregate := mergeGeneratorMetricAggregates(series); aggregate != nil {
			aggregate.Name = name
			out = append(out, *aggregate)
		}
	}
	return out
}

func mergeGeneratorMetricAggregates(values []generatorrpc.MetricAggregate) *generatorrpc.MetricAggregate {
	if len(values) == 0 {
		return nil
	}
	if len(values) == 1 && values[0].Count > 0 {
		copy := values[0]
		return &copy
	}
	out := generatorrpc.MetricAggregate{}
	var durationDigest *engine.TDigest
	durationDigestComplete := true
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
		if value.DurationDigest != nil && value.DurationDigest.Count == value.Count {
			if durationDigest == nil {
				durationDigest = engine.NewTDigest(value.DurationDigest.Compression)
			}
			durationDigest.Merge(engine.NewTDigestFromSnapshot(*value.DurationDigest))
		} else {
			durationDigestComplete = false
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
	if durationDigestComplete && durationDigest != nil && durationDigest.Count() == out.Count {
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
