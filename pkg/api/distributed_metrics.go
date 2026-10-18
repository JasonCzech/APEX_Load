package api

// distributed_metrics.go — split from distributed_execution_backend.go (same package, behavior-preserving).

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

// mergeRetainedDropCounters preserves a nonnegative loss lower bound when an
// invalid contributor or signed overflow makes the exact fleet counter unknown.
// Saturation retains observed loss without letting overflow manufacture zero.
func mergeRetainedDropCounters(current, incoming int64) (int64, bool) {
	valid := current >= 0 && incoming >= 0
	current = max(current, 0)
	incoming = max(incoming, 0)
	if incoming > math.MaxInt64-current {
		return math.MaxInt64, false
	}
	return current + incoming, valid
}

// distributedAggregateGeneratorID names a fleet aggregate batch.
const distributedAggregateGeneratorID = "distributed"

// aggregateDistributedMetricsBatches merges per-generator batches into one
// combined batch. It also returns the fleet TPS and per-(raw-)group TPS computed
// as a sum of per-generator rates: TPS is a per-second rate and is additive
// across generators, but each generator's success count must be divided by its
// OWN window before summing. The combined transaction list collapses to a single
// representative window, so these rates are computed here, before concatenation.
func aggregateDistributedMetricsBatches(testID string, batches map[string]generatorrpc.MetricsBatch) (generatorrpc.MetricsBatch, float64, map[string]float64) {
	combined := generatorrpc.MetricsBatch{TestID: testID, GeneratorID: distributedAggregateGeneratorID}
	telemetry := distributedTelemetryAccumulator{publishedAt: time.Now()}
	targets := make([]string, 0, len(batches))
	groupsByName := make(map[string]int32)
	groupMetricsByName := make(map[string][]generatorrpc.MetricAggregate)
	// Each group's transactions, by generator, merged by name below.
	groupTransactionsByName := make(map[string][]generatorrpc.MetricAggregate)
	// Requests, failed requests and the request rate per group. Generators'
	// windows can differ in length, so each rates its own window, as TPS does.
	groupRequestsByName := make(map[string]generatorrpc.GroupMetric)
	// Each group's completed iterations: the generators' running totals
	// summed, unknown when one of the group's generators does not count them.
	groupIterationsByName := make(map[string]int64)
	groupIterationsUnknown := make(map[string]bool)
	// A fleet's request counts are only whole when every generator counts
	// them; otherwise the sample carries none rather than part of the fleet's.
	requestCountsVersion := uint32(generatorrpc.CurrentRequestCountsVersion)
	if len(batches) == 0 {
		requestCountsVersion = 0
	}
	// Likewise the groups' transactions: a generator that does not report them
	// leaves the fleet's transactions unattributed rather than part-attributed.
	groupTransactionsVersion := uint32(generatorrpc.CurrentGroupTransactionsVersion)
	if len(batches) == 0 {
		groupTransactionsVersion = 0
	}
	fleetTPS := 0.0
	groupTPS := make(map[string]float64)
	statsList := make([]generatorrpc.GeneratorStats, 0, len(batches))
	fleetMetrics := make([]generatorrpc.MetricAggregate, 0, len(batches))
	transactionMetrics := make([]generatorrpc.MetricAggregate, 0)
	dependencyMetrics := make([]generatorrpc.MetricAggregate, 0)
	soapMetrics := make([]generatorrpc.MetricAggregate, 0)
	networkProfileMetrics := make([]generatorrpc.MetricAggregate, 0)
	actionMetrics := make([]generatorrpc.MetricAggregate, 0)
	actionsVersion := uint32(3)
	if len(batches) == 0 {
		actionsVersion = 0
	}
	dimensionMetrics := make([]generatorrpc.MetricAggregate, 0)
	jointV4Parts, jointV4Infos, jointV4Unavailable := [][]generatorrpc.MetricAggregate{}, []*generatorrpc.JointMetricsInfo{}, 0
	jointMetrics := make([]generatorrpc.MetricAggregate, 0)
	propertyMetrics := make([]generatorrpc.MetricAggregate, 0)
	regionMetrics := make([]generatorrpc.MetricAggregate, 0)
	browserMetrics := make([]*generatorrpc.BrowserMetrics, 0)
	browserDeviceMetrics := make([]generatorrpc.BrowserDeviceMetrics, 0)
	for target := range batches {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	for _, target := range targets {
		batch := batches[target]
		protocol.MergeBrowserDocumentSummaries(&combined.BrowserDocuments, browserDocumentsForLive(batch.BrowserDocuments, batch.Browser != nil || len(batch.BrowserByDevice) > 0))
		// Transport identity, rather than a producer-controlled label, separates
		// cumulative partition rows emitted by different generator executions.
		if batch.MessagingProgress != nil {
			protocol.MergeMessagingProgress(&combined.MessagingProgress, batch.MessagingProgress, target)
			combined.MessagingProgressVersion = protocol.MessagingProgressVersion
		}
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
		if batch.ActionMetricsVersion > 3 {
			actionsVersion = 0
		} else {
			actionsVersion = min(actionsVersion, batch.ActionMetricsVersion)
		}
		if batch.RequestCountsVersion != generatorrpc.CurrentRequestCountsVersion {
			requestCountsVersion = 0
		}
		if batch.GroupTransactionsVersion != generatorrpc.CurrentGroupTransactionsVersion {
			groupTransactionsVersion = 0
		}
		dimensionMetrics = append(dimensionMetrics, batch.ActionDimensionMetrics...)
		jointMetrics = append(jointMetrics, batch.ActionPropertyMetrics...)
		actionMetrics = append(actionMetrics, batch.ActionMetrics...)
		propertyMetrics = append(propertyMetrics, batch.PropertyMetrics...)
		regionMetrics = append(regionMetrics, batch.RegionMetrics...)
		if batch.JointInfo != nil {
			jointV4Parts = append(jointV4Parts, batch.JointMetrics)
			jointV4Infos = append(jointV4Infos, batch.JointInfo)
		} else {
			jointV4Unavailable++
		}
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
			for _, aggregate := range group.TransactionMetrics {
				transaction := strings.TrimSpace(aggregate.Name)
				if transaction == "" || aggregate.Count <= 0 {
					continue
				}
				aggregate.Name = transaction
				aggregate.Errors = boundedMetricErrors(aggregate.Count, aggregate.Errors)
				aggregate.TPS = resolvedAggregateTPS(aggregate, windowSec)
				groupTransactionsByName[name] = append(groupTransactionsByName[name], aggregate)
			}
			if group.CompletedIterations != nil {
				groupIterationsByName[name] += max(*group.CompletedIterations, 0)
			} else {
				groupIterationsUnknown[name] = true
			}
			if requests, requestErrors := distributedGroupRequestCounts(group); requests > 0 {
				merged := groupRequestsByName[name]
				merged.Requests += requests
				merged.RequestErrors += requestErrors
				merged.RequestRate += float64(requests) / windowSec
				groupRequestsByName[name] = merged
			}
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
		telemetry.add(batch.LifecycleState, batch.Telemetry)
	}
	// Joint evidence v4 merges by exact tuple across generators; a generator
	// without it leaves the fleet window incomplete, never zero.
	if len(jointV4Infos) > 0 {
		if merged, info, err := generatorrpc.MergeJointEvidence(jointV4Parts, jointV4Infos); err == nil && info != nil {
			info.UnavailableSources = jointV4Unavailable
			info.Incomplete = info.Incomplete || jointV4Unavailable > 0
			combined.JointMetrics, combined.JointInfo = merged, info
		}
	}
	combined.Telemetry = telemetry.result()
	combined.FleetMetrics = mergeGeneratorMetricAggregates(fleetMetrics)
	if actionsVersion > 0 {
		combined.ActionMetricsVersion = actionsVersion
		if actionsVersion == 3 {
			combined.ActionDimensionMetrics = mergeGeneratorMetricAggregateSeries(dimensionMetrics)
		}
		if actionsVersion >= 2 {
			combined.ActionPropertyMetrics = mergeGeneratorMetricAggregateSeries(jointMetrics)
		}
		combined.ActionMetrics = mergeGeneratorMetricAggregateSeries(actionMetrics)
	}
	combined.TransactionMetrics = mergeGeneratorMetricAggregateSeries(transactionMetrics)
	combined.DependencyMetrics = mergeGeneratorMetricAggregateSeries(dependencyMetrics)
	combined.SOAPMetrics = mergeGeneratorMetricAggregateSeries(soapMetrics)
	combined.NetworkProfileMetrics = mergeGeneratorMetricAggregateSeries(networkProfileMetrics)
	combined.PropertyMetrics = mergeGeneratorMetricAggregateSeries(propertyMetrics)
	combined.RegionMetrics = mergeGeneratorMetricAggregateSeries(regionMetrics)
	combined.Browser = mergeGeneratorBrowserMetrics(browserMetrics)
	combined.BrowserByDevice = mergeGeneratorBrowserDeviceMetrics(browserDeviceMetrics)
	groupNames := make([]string, 0, len(groupsByName))
	for name := range groupsByName {
		groupNames = append(groupNames, name)
	}
	sort.Strings(groupNames)
	for _, name := range groupNames {
		requests := groupRequestsByName[name]
		var iterations *int64
		if total, counted := groupIterationsByName[name]; counted && !groupIterationsUnknown[name] {
			iterations = &total
		}
		combined.Groups = append(combined.Groups, generatorrpc.GroupMetric{
			Name:                name,
			ActiveVUsers:        groupsByName[name],
			Metrics:             mergeGeneratorMetricAggregates(groupMetricsByName[name]),
			Requests:            requests.Requests,
			RequestErrors:       requests.RequestErrors,
			RequestRate:         requests.RequestRate,
			CompletedIterations: iterations,
			TransactionMetrics:  mergeGeneratorMetricAggregateSeries(groupTransactionsByName[name]),
		})
	}
	combined.RequestCountsVersion = requestCountsVersion
	combined.GroupTransactionsVersion = groupTransactionsVersion
	combined.Stats = aggregateDistributedGeneratorStats(statsList)
	return combined, fleetTPS, groupTPS
}

// distributedGroupRequestCounts is a group's attempted requests in a
// window and the failed ones, bounded to the attempts.
func distributedGroupRequestCounts(group generatorrpc.GroupMetric) (int64, int64) {
	if group.Requests <= 0 {
		return 0, 0
	}
	return group.Requests, min(max(group.RequestErrors, 0), group.Requests)
}

// distributedRequestCounts is a batch's attempted and failed requests: the
// sum of its named groups', as the merge of generators' batches keeps them.
// It reports false for a batch whose generator does not count requests.
func distributedRequestCounts(batch generatorrpc.MetricsBatch) (requests, requestErrors int64, counted bool) {
	if batch.RequestCountsVersion != generatorrpc.CurrentRequestCountsVersion {
		return 0, 0, false
	}
	for _, group := range batch.Groups {
		if strings.TrimSpace(group.Name) == "" {
			continue
		}
		groupRequests, groupErrors := distributedGroupRequestCounts(group)
		requests += groupRequests
		requestErrors += groupErrors
	}
	return requests, requestErrors, true
}

// applyDistributedRequestMetrics sets a sample's request counters from its
// named groups', as distributedWindowCounts counts them, unless a generator
// in the batch does not count requests. A group's rate is the one the merge
// of the generators' windows set, else its requests over the batch's window.
func applyDistributedRequestMetrics(live *LiveMetrics, batch generatorrpc.MetricsBatch) {
	if batch.RequestCountsVersion != generatorrpc.CurrentRequestCountsVersion {
		return
	}
	windowSec := batchWindowSeconds(batch.WindowMs)
	for _, group := range batch.Groups {
		requests, requestErrors := distributedGroupRequestCounts(group)
		if requests == 0 {
			continue
		}
		rate := group.RequestRate
		if rate <= 0 {
			rate = float64(requests) / windowSec
		}
		name := strings.TrimSpace(group.Name)
		if name == "" {
			continue
		}
		live.Requests += requests
		live.RequestErrors += requestErrors
		live.RequestRate += rate
		if groupLive := live.GroupMetrics[name]; groupLive != nil {
			groupLive.Requests += requests
			groupLive.RequestErrors += requestErrors
			groupLive.RequestRate += rate
		}
	}
}

// carryGroupCompletedIterations keeps a group's last reported completed
// iterations on a terminal batch that leaves them out (terminal evidence
// rebuilt without its counters), so the run's last sample does not lose them.
func carryGroupCompletedIterations(groups, prior []generatorrpc.GroupMetric) []generatorrpc.GroupMetric {
	priorIterations := make(map[string]*int64, len(prior))
	for _, group := range prior {
		if group.CompletedIterations != nil {
			priorIterations[strings.TrimSpace(group.Name)] = group.CompletedIterations
		}
	}
	if len(priorIterations) == 0 {
		return groups
	}
	carried := append([]generatorrpc.GroupMetric(nil), groups...)
	for index := range carried {
		if carried[index].CompletedIterations == nil {
			carried[index].CompletedIterations = priorIterations[strings.TrimSpace(carried[index].Name)]
		}
	}
	return carried
}

// applyDistributedGroupIterations sets each named group's completed
// iterations from the batch's, leaving a group whose count is unknown
// without one.
func applyDistributedGroupIterations(live *LiveMetrics, batch generatorrpc.MetricsBatch) {
	for _, group := range batch.Groups {
		if group.CompletedIterations == nil {
			continue
		}
		if groupLive := live.GroupMetrics[strings.TrimSpace(group.Name)]; groupLive != nil {
			iterations := *group.CompletedIterations
			groupLive.CompletedIterations = &iterations
		}
	}
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
	sketch    *engine.DurationSketch
	durations []float64
}

func newRawMetricAccumulator(name string) *rawMetricAccumulator {
	return &rawMetricAccumulator{
		metric: generatorrpc.MetricAggregate{Name: name},
		digest: engine.NewTDigest(200),
		sketch: engine.NewDurationSketch(),
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
	a.sketch.Add(tx.DurationMs * float64(time.Millisecond))
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
	digestSnapshot, sketchSnapshot := a.digest.Snapshot(), a.sketch.Snapshot()
	metric.DurationDigest, metric.DurationSketch = &digestSnapshot, &sketchSnapshot
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
		BrowserDocuments:           browserDocumentsForLive(batch.BrowserDocuments, batch.Browser != nil || len(batch.BrowserByDevice) > 0),
		MessagingProgress:          batch.MessagingProgress.Clone(),
		TransactionResponseTimeSLA: remoteTransactionResponseTimeSLAResults(test),
		HTTPVersions:               buildRunArchiveHTTPVersionMix(remoteHTTPVersionMix(test), nil),
		Timestamp:                  timestamp,
		SampleWindowSeconds:        batchWindowSeconds(batch.WindowMs),
		ActiveVUsers:               int64(batch.Stats.ActiveVUsers),
		CompletedIterations:        batch.Stats.CompletedIterations,
		Transactions:               make(map[string]*TxMetrics),
		DependencyHealth:           make(map[string]*TxMetrics),
		SOAPOperations:             make(map[string]*TxMetrics),
		NetworkProfiles:            make(map[string]*TxMetrics),
		Properties:                 make(map[string]*TxMetrics),
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
		live.Telemetry.DroppedResultsAvailable = !batch.Telemetry.DroppedResultsUnavailable && batch.Telemetry.DroppedResults >= 0
		live.Telemetry.SheddedArrivals = batch.Telemetry.SheddedArrivals
		live.Telemetry.DroppedSubscriberBatches = batch.Telemetry.DroppedSubscriberBatches
		live.Telemetry.MissingMetricWindows = batch.Telemetry.MissingMetricWindows
	}
	applyDistributedTelemetryTiming(live, batch)
	defer func() {
		applyDistributedRequestMetrics(live, batch)
		applyDistributedGroupIterations(live, batch)
		actions := map[string]*TxMetrics{}
		if batch.ActionMetricsVersion >= 1 && batch.ActionMetricsVersion <= 3 {
			for _, a := range batch.ActionMetrics {
				if m := txMetricsFromAggregate(a); m != nil {
					actions[m.Name] = m
				}
			}
		}
		if batch.ActionMetricsVersion >= 1 && batch.ActionMetricsVersion <= 3 {
			live.NativeEvidence = nativeIntervalEvidence(live, actions, true, batchWindowSeconds(batch.WindowMs))
			if batch.ActionMetricsVersion == 3 {
				live.NativeEvidence.Version = 3
				dimensions := map[string]*TxMetrics{}
				for _, a := range batch.ActionDimensionMetrics {
					if m := txMetricsFromAggregate(a); m != nil {
						dimensions[m.Name] = m
					}
				}
				addNativeDimensionEvidence(live.NativeEvidence, dimensions)
			}
			if batch.ActionMetricsVersion >= 2 {
				live.NativeEvidence.Version = int(batch.ActionMetricsVersion)
				joint := map[string]*TxMetrics{}
				for _, a := range batch.ActionPropertyMetrics {
					if m := txMetricsFromAggregate(a); m != nil {
						joint[m.Name] = m
					}
				}
				addNativeJointEvidence(live.NativeEvidence, joint)
			}
			if batch.JointInfo != nil {
				published, tuples, other, omitted, incomplete := jointEvidenceFromBatch(batch)
				setNativeJointEvidence(live.NativeEvidence, runJointProjection(test), published, tuples, other, omitted, incomplete)
				if live.NativeEvidence.Joint != nil {
					live.NativeEvidence.Joint.UnavailableSources = batch.JointInfo.UnavailableSources
					// Merged queries and the archive read each publication's own
					// tuples; the fleet re-merge of resident batches keeps only its
					// availability, or every point would carry the tuples twice.
					live.NativeEvidence.Joint.Other = nil
				}
				delete(live.NativeEvidence.Dimensions, storage.NativeJointDimension)
			} else if projection := runJointProjection(test); projection != nil {
				setNativeJointEvidence(live.NativeEvidence, projection, nil, nil, nil, 0, false)
			}
		}
	}()
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
		digest, sketch := engine.NewTDigest(200), engine.NewDurationSketch()
		for _, duration := range durations {
			digest.Add(duration * 1_000_000)
			sketch.Add(duration * 1_000_000)
		}
		digestSnapshot, sketchSnapshot := digest.Snapshot(), sketch.Snapshot()
		metrics.DurationDigest, metrics.DurationSketch = &digestSnapshot, &sketchSnapshot
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
	live.Messaging = map[string]*protocol.MessagingSummary{}
	for _, a := range batch.ActionMetrics {
		if a.Messaging != nil {
			live.Messaging[a.Name] = a.Messaging.Clone()
		}
	}

	live.Messaging = protocol.CloneMessagingSummaries(live.Messaging)
	// Protocol actions may have no business-transaction wrapper. Their evidence
	// remains useful without manufacturing a fleet transaction population.
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
	live.PercentileQuality = fleet.PercentileQuality
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
	// Each transaction's groups, once every generator in the window reports them.
	if batch.GroupTransactionsVersion == generatorrpc.CurrentGroupTransactionsVersion {
		groupTransactions := make(map[string]map[string]*TransactionGroupShare, len(batch.Groups))
		for _, group := range batch.Groups {
			shares := make(map[string]*TransactionGroupShare, len(group.TransactionMetrics))
			for _, aggregate := range group.TransactionMetrics {
				if share := groupTransactionShareFromAggregate(aggregate); share != nil {
					shares[strings.TrimSpace(aggregate.Name)] = share
				}
			}
			groupTransactions[group.Name] = shares
		}
		attributeTransactionsToGroups(live.Transactions, groupTransactions)
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
	for _, aggregate := range batch.PropertyMetrics {
		metrics := txMetricsFromAggregate(aggregate)
		if metrics != nil {
			live.Properties[metrics.Name] = metrics
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
			groupLive.DurationSketch = group.Metrics.DurationSketch
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
			DurationSketch: fleet.DurationSketch,
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
	mergeDistributedServerTelemetry(metrics, tx.ServerTelemetry, tx.Group)
}

func txMetricsFromAggregate(aggregate generatorrpc.MetricAggregate) *TxMetrics {
	if strings.TrimSpace(aggregate.Name) == "" || aggregate.Count == 0 {
		return nil
	}
	return &TxMetrics{
		DurationDigest:     engine.CloneTDigestSnapshot(aggregate.DurationDigest),
		DurationSketch:     engine.CloneDurationSketchSnapshot(aggregate.DurationSketch),
		PercentileQuality:  aggregate.PercentileQuality,
		messagingAggregate: aggregate.MessagingVersion == 1 || aggregate.MessagingVersion == 2 || aggregate.Messaging != nil,
		ServerTelemetry:    apiMessagingSummary(aggregate.Messaging),
		Name:               aggregate.Name,
		TPS:                aggregate.TPS,
		DependencyType:     aggregate.DependencyType,
		DependencyTarget:   aggregate.DependencyTarget,
		AvgDNSMs:           aggregate.AvgDNSMs,
		AvgConnectMs:       aggregate.AvgConnectMs,
		AvgTLSMs:           aggregate.AvgTLSMs,
		AvgTTFBMs:          aggregate.AvgTTFBMs,
		Count:              aggregate.Count,
		Errors:             aggregate.Errors,
		AvgMs:              aggregate.AvgMs,
		P50Ms:              aggregate.P50Ms,
		P90Ms:              aggregate.P90Ms,
		P95Ms:              aggregate.P95Ms,
		P99Ms:              aggregate.P99Ms,
		Exemplars:          append([]engine.TraceExemplar(nil), aggregate.Exemplars...),
	}
}

func browserLiveMetricsFromGenerator(metrics generatorrpc.BrowserMetrics) *BrowserLiveMetrics {
	if metrics.Samples == 0 {
		return nil
	}
	return &BrowserLiveMetrics{
		Samples:              metrics.Samples,
		DocumentLoads:        metrics.DocumentLoads,
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
		DocumentLoads:        metrics.Metrics.DocumentLoads,
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
	mergeDistributedServerTelemetry(metrics, tx.ServerTelemetry, tx.Group)
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
		Messaging:                 summary.Messaging.Clone(),
		ConsumerLagSamples:        summary.ConsumerLagSamples,
		ConsumerOffsetLagSamples:  summary.ConsumerOffsetLagSamples,
		DiagnosticFindings:        protocol.ProtocolDiagnosticFindings(summary.GRPC, summary.WebSocket),
		FirstEventSamples:         summary.FirstEventSamples,
		GRPC:                      summary.GRPC.Clone(),
		WebSocket:                 summary.WebSocket.Clone(),
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

func mergeDistributedServerTelemetry(metrics *TxMetrics, telemetry *generatorrpc.ServerTelemetry, groups ...string) {
	if metrics == nil || telemetry == nil {
		return
	}
	if metrics.ServerTelemetry == nil {
		metrics.ServerTelemetry = &ServerTelemetrySummary{}
	}
	if !metrics.messagingAggregate {
		group := ""
		if len(groups) > 0 {
			group = groups[0]
		}
		protocol.MergeMessagingTelemetry(&metrics.ServerTelemetry.Messaging, telemetry.MessagingTelemetry(), metrics.DependencyTarget, metrics.DependencyType, group)
		if metrics.ServerTelemetry.Messaging != nil {
			metrics.ServerTelemetry.Messaging.Incomplete = true
		}
	}
	protocol.MergeGRPCDiagnostic(&metrics.ServerTelemetry.GRPC, telemetry.GRPC)
	protocol.MergeWebSocketDiagnostic(&metrics.ServerTelemetry.WebSocket, telemetry.WebSocket)
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
	if (telemetry.GRPC == nil || telemetry.GRPC.FirstMessageMs != nil) && telemetry.WebSocket == nil {
		metrics.ServerTelemetry.FirstEventSamples++
		metrics.ServerTelemetry.AvgFirstEventMs += telemetry.FirstEventMs
	}
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
	if telemetry.ConsumerLagObserved || telemetry.ConsumerLagMs > 0 {
		metrics.ServerTelemetry.ConsumerLagSamples++
		metrics.ServerTelemetry.AvgConsumerLagMs += telemetry.ConsumerLagMs
	}
	if telemetry.ConsumerLagMs > metrics.ServerTelemetry.MaxConsumerLagMs {
		metrics.ServerTelemetry.MaxConsumerLagMs = telemetry.ConsumerLagMs
	}
	if telemetry.ConsumerOffsetLagObserved || telemetry.ConsumerOffsetLag > 0 {
		metrics.ServerTelemetry.ConsumerOffsetLagSamples++
		metrics.ServerTelemetry.AvgConsumerOffsetLag += float64(telemetry.ConsumerOffsetLag)
	}
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
		t.MessagesSentPerSec != 0 || t.MessagesReceivedPerSec != 0 || t.ConsumerLagObserved || t.ConsumerOffsetLagObserved || t.ConsumerLagMs != 0 ||
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
	if summary.FirstEventSamples > 0 {
		summary.AvgFirstEventMs /= float64(summary.FirstEventSamples)
	}
	summary.AvgMessageLatencyMs /= divisor
	summary.AvgMessagesSentPerSec /= divisor
	summary.AvgMessagesReceivedPerSec /= divisor
	if summary.ConsumerLagSamples > 0 {
		summary.AvgConsumerLagMs /= float64(summary.ConsumerLagSamples)
	}
	if summary.ConsumerOffsetLagSamples > 0 {
		summary.AvgConsumerOffsetLag /= float64(summary.ConsumerOffsetLagSamples)
	}
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
		copy.Messaging = copy.Messaging.Clone()
		copy.Exemplars = append([]engine.TraceExemplar(nil), copy.Exemplars...)
		if copy.PercentileQuality == "" && copy.DurationSketch == nil && copy.DurationDigest != nil {
			// A generator that predates the sketch took its percentiles from
			// its t-digest.
			copy.PercentileQuality = percentileQualityTDigest
		}
		return &copy
	}
	out := generatorrpc.MetricAggregate{}
	durations := engine.NewDurationDistributionMerge()
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
		protocol.MergeMessagingSummary(&out.Messaging, value.Messaging)
		avgMsSum += value.AvgMs * count
		dnsSum += value.AvgDNSMs * count
		connectSum += value.AvgConnectMs * count
		tlsSum += value.AvgTLSMs * count
		ttfbSum += value.AvgTTFBMs * count
		out.BytesSent += value.BytesSent
		out.BytesReceived += value.BytesReceived
		out.TPS += value.TPS
		out.Exemplars = engine.MergeTraceExemplars(out.Exemplars, value.Exemplars)
		durations.Add(value.Count, value.DurationDigest, value.DurationSketch)
	}
	if out.Count == 0 {
		return nil
	}
	out.MessagingVersion = 1
	for _, v := range values {
		if v.Count > 0 && (v.MessagingVersion != 1 && v.MessagingVersion != 2) {
			out.MessagingVersion = 0
			if out.Messaging != nil {
				out.Messaging.Incomplete = true
			}
		}
	}
	if out.Messaging != nil {
		out.MessagingVersion = out.Messaging.EvidenceVersion()
	}
	divisor := float64(out.Count)
	out.AvgMs = avgMsSum / divisor
	out.AvgDNSMs = dnsSum / divisor
	out.AvgConnectMs = connectSum / divisor
	out.AvgTLSMs = tlsSum / divisor
	out.AvgTTFBMs = ttfbSum / divisor
	// Sketches merge exactly and are preferred; t-digests serve older
	// generators.
	if kind := durations.Kind(); kind != "" && durations.Count() == out.Count {
		out.DurationDigest, out.DurationSketch = durations.Snapshots()
		out.P50Ms = durations.Quantile(0.50) / float64(time.Millisecond)
		out.P90Ms = durations.Quantile(0.90) / float64(time.Millisecond)
		out.P95Ms = durations.Quantile(0.95) / float64(time.Millisecond)
		out.P99Ms = durations.Quantile(0.99) / float64(time.Millisecond)
		if kind == engine.DurationDistributionTDigest {
			out.PercentileQuality = percentileQualityTDigest
		}
	} else {
		out.P50Ms, out.P90Ms, out.P95Ms, out.P99Ms = weightedAggregatePercentiles(values)
		out.PercentileQuality = percentileQualityAveraged
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
	// Page-load averages weigh by the loads behind them.
	loads := engine.BrowserDocumentLoads(value.DocumentLoads, value.Samples, value.AvgFCPMs, value.AvgLCPMs, value.AvgTTFBMs, value.AvgBlockingResources)
	out.DocumentLoads += loads
	out.AvgFCPMs += value.AvgFCPMs * float64(loads)
	out.AvgLCPMs += value.AvgLCPMs * float64(loads)
	out.AvgTTFBMs += value.AvgTTFBMs * float64(loads)
	out.AvgBlockingResources += value.AvgBlockingResources * float64(loads)
	out.AvgCLSScore += value.AvgCLSScore * samples
	out.AvgINPMs += value.AvgINPMs * samples
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
	out.AvgFCPMs = browserLoadAverage(out.AvgFCPMs, out.DocumentLoads)
	out.AvgLCPMs = browserLoadAverage(out.AvgLCPMs, out.DocumentLoads)
	out.AvgTTFBMs = browserLoadAverage(out.AvgTTFBMs, out.DocumentLoads)
	out.AvgBlockingResources = browserLoadAverage(out.AvgBlockingResources, out.DocumentLoads)
	out.AvgCLSScore /= divisor
	out.AvgINPMs /= divisor
	out.AvgLongTaskCount /= divisor
	out.AvgJSHeapUsedMB /= divisor
	out.AvgBrowserProcessCPU /= divisor
	out.AvgBrowserProcessMem /= divisor
}
