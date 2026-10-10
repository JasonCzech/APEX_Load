package api

import (
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
)

// runTotalsAccumulator derives run-to-date transaction and error counters. A
// local run counts the per-window live samples it publishes (apply); a
// distributed run counts each generator's accepted windows (countWindow), and
// a controller that takes it over starts from the stored history
// (adoptedRunTotals).
//
// Every window contributes exactly once. For a local run, a sample carrying
// the latest timestamp again replaces that window's earlier contribution
// instead of adding to it, and samples older than the latest window are
// ignored: their window was already counted. When the accumulator starts
// empty while the test already has history, it is seeded from that history
// first.
type runTotalsAccumulator struct {
	initialized bool
	// distributed: the totals are counted where each generator's metrics
	// window is accepted, not from published samples. Distributed
	// publications are throttled fleet-wide and carry each generator's
	// latest window only, so a window replaced before a publication would be
	// lost, and a terminal snapshot that carries a generator's last window
	// forward would count it again.
	distributed bool
	// windows is each generator's last counted window: its source epoch and
	// the number and publication time of the collection that produced it.
	windows    map[string]distributedWindowMark
	lastWindow time.Time
	lastTx     int64
	lastErrors int64
	lastGroups map[string]runTotalsCounts
	tx         int64
	errors     int64
	groups     map[string]runTotalsCounts
}

type distributedWindowMark struct {
	epoch       string
	collection  uint64
	publishedAt int64
}

// newDistributedWindow reports whether batch carries a window not yet
// counted for target, and records it. A generator numbers its collections
// within its source epoch, and a batch that re-sends the last window
// (stopping, paused, terminal, a subscription replay) repeats the number. An
// older generator is matched by the publication time of the collection
// instead. Two of its collections published in the same millisecond count
// once. With several groups, its stop drain carries forward the last window
// of any group with nothing left to drain, and that window is counted again.
// A batch with neither number nor time is counted unless it is terminal; a
// terminal one is taken to re-send.
func (a *runTotalsAccumulator) newDistributedWindow(target string, batch generatorrpc.MetricsBatch) bool {
	var collection uint64
	var published int64
	if batch.Telemetry != nil {
		collection, published = batch.Telemetry.CollectionSequence, batch.Telemetry.PublishedAtUnixMs
	}
	if collection == 0 && published <= 0 {
		return !isTerminalRemoteLifecycle(batch.LifecycleState)
	}
	if a.windows == nil {
		a.windows = map[string]distributedWindowMark{}
	}
	if last, seen := a.windows[target]; seen && last.epoch == batch.SourceEpoch {
		if collection > 0 && collection <= last.collection || collection == 0 && published <= last.publishedAt {
			return false
		}
	}
	a.windows[target] = distributedWindowMark{epoch: batch.SourceEpoch, collection: collection, publishedAt: published}
	return true
}

// runTotalsWindow is one generator window's successful transactions and
// errors, fleet-wide and per group.
type runTotalsWindow struct {
	runTotalsCounts
	groups map[string]runTotalsCounts
}

// distributedWindowCounts counts one generator batch's window as a published
// sample counts it (aggregateDistributedMetricsBatches, then
// buildLiveMetricsFromDistributedBatch) without building the sample. The
// window is the fleet aggregate, else the transaction aggregates, else the
// raw transactions. A group's share is its aggregate, else its raw
// transactions when the batch needs the raw fallback; a raw transaction
// without a group belongs to the batch's only group. A test with one group
// takes the whole window when the batch names no group. A window with no
// count leaves every group at zero.
// TestDistributedWindowCountsMatchPublishedSamples holds the two together.
func distributedWindowCounts(batch generatorrpc.MetricsBatch, configuredGroups []TestGroup) runTotalsWindow {
	window := runTotalsWindow{groups: map[string]runTotalsCounts{}}
	hasTransactionAggregates := hasUsableMetricAggregateSeries(batch.TransactionMetrics)
	var count, errors int64
	switch {
	case batch.FleetMetrics != nil && batch.FleetMetrics.Count > 0:
		count, errors = batch.FleetMetrics.Count, boundedMetricErrors(batch.FleetMetrics.Count, batch.FleetMetrics.Errors)
	case hasTransactionAggregates:
		for _, aggregate := range batch.TransactionMetrics {
			if strings.TrimSpace(aggregate.Name) == "" || aggregate.Count <= 0 {
				continue
			}
			count += aggregate.Count
			errors += boundedMetricErrors(aggregate.Count, aggregate.Errors)
		}
	default:
		for _, tx := range batch.Transactions {
			count++
			if !tx.Success {
				errors++
			}
		}
	}
	if count <= 0 {
		return window
	}
	window.tx, window.errors = successfulMetricCount(count, errors), boundedMetricErrors(count, errors)

	// Per-group attempts and errors, before bounding.
	attempts := map[string]runTotalsCounts{}
	add := func(name string, groupCount, groupErrors int64) {
		counts := attempts[name]
		counts.tx += groupCount
		counts.errors += groupErrors
		attempts[name] = counts
	}
	raw := map[string]runTotalsCounts{}
	if !hasUsableGroupMetricAggregateSeries(batch.Groups) &&
		needsRawDistributedMetricFallback(batch, hasTransactionAggregates, hasUsableMetricAggregateSeries(batch.DependencyMetrics), hasUsableMetricAggregateSeries(batch.SOAPMetrics), false) {
		soleGroup, multipleGroups := "", false
		for _, group := range batch.Groups {
			name := strings.TrimSpace(group.Name)
			if name == "" {
				continue
			}
			if soleGroup == "" {
				soleGroup = name
			} else if soleGroup != name {
				multipleGroups = true
				break
			}
		}
		for _, tx := range batch.Transactions {
			name := strings.TrimSpace(tx.Group)
			// The raw fallback's series bound folds excess names together.
			if _, seen := raw[name]; !seen && len(raw) >= maxDistributedMetricSeries-1 {
				name = distributedOverflowSeries
			}
			counts := raw[name]
			counts.tx++
			if !tx.Success {
				counts.errors++
			}
			raw[name] = counts
		}
		if counts, ok := raw[""]; ok && soleGroup != "" && !multipleGroups {
			delete(raw, "")
			sole := raw[soleGroup]
			sole.tx += counts.tx
			sole.errors += counts.errors
			raw[soleGroup] = sole
		}
	}
	named := map[string]bool{}
	accounted := map[string]bool{}
	for _, group := range batch.Groups {
		name := strings.TrimSpace(group.Name)
		if name == "" {
			continue
		}
		named[name] = true
		if group.Metrics != nil && group.Metrics.Count > 0 {
			add(name, group.Metrics.Count, boundedMetricErrors(group.Metrics.Count, group.Metrics.Errors))
			accounted[name] = true
		} else if counts, ok := raw[name]; ok {
			add(name, counts.tx, counts.errors)
			accounted[name] = true
		}
	}
	for name, counts := range raw {
		if name == "" || accounted[name] {
			continue
		}
		named[name] = true
		add(name, counts.tx, counts.errors)
	}
	for name := range named {
		counts := attempts[name]
		window.groups[name] = runTotalsCounts{tx: successfulMetricCount(counts.tx, counts.errors), errors: boundedMetricErrors(counts.tx, counts.errors)}
	}
	if len(named) == 0 && len(configuredGroups) == 1 {
		window.groups[configuredGroups[0].Name] = window.runTotalsCounts
	}
	return window
}

type runTotalsCounts struct {
	tx     int64
	errors int64
}

// seed rebuilds the totals from retained history before a time (any, when
// zero). History is bounded, so a very long run that failed over may
// undercount; the window counters on every sample are unaffected.
func (a *runTotalsAccumulator) seed(history []*LiveMetrics, before time.Time) {
	a.initialized = true
	a.groups = map[string]runTotalsCounts{}
	for _, point := range history {
		if point == nil || point.Timestamp.IsZero() || !before.IsZero() && !point.Timestamp.Before(before) {
			continue
		}
		if !a.lastWindow.IsZero() && !point.Timestamp.After(a.lastWindow) {
			continue
		}
		a.add(point)
	}
}

func (a *runTotalsAccumulator) add(live *LiveMetrics) {
	a.tx += live.TotalTx
	a.errors += live.TotalErrors
	a.lastWindow = live.Timestamp
	a.lastTx, a.lastErrors = live.TotalTx, live.TotalErrors
	a.lastGroups = make(map[string]runTotalsCounts, len(live.GroupMetrics))
	for name, group := range live.GroupMetrics {
		if group == nil {
			continue
		}
		counts := a.groups[name]
		counts.tx += group.Transactions
		counts.errors += group.Errors
		a.groups[name] = counts
		a.lastGroups[name] = runTotalsCounts{tx: group.Transactions, errors: group.Errors}
	}
}

func (a *runTotalsAccumulator) replaceLatest(live *LiveMetrics) {
	a.tx += live.TotalTx - a.lastTx
	a.errors += live.TotalErrors - a.lastErrors
	for name, previous := range a.lastGroups {
		counts := a.groups[name]
		counts.tx -= previous.tx
		counts.errors -= previous.errors
		a.groups[name] = counts
	}
	a.lastTx, a.lastErrors = live.TotalTx, live.TotalErrors
	a.lastGroups = make(map[string]runTotalsCounts, len(live.GroupMetrics))
	for name, group := range live.GroupMetrics {
		if group == nil {
			continue
		}
		counts := a.groups[name]
		counts.tx += group.Transactions
		counts.errors += group.Errors
		a.groups[name] = counts
		a.lastGroups[name] = runTotalsCounts{tx: group.Transactions, errors: group.Errors}
	}
}

// countWindow adds one generator's accepted metrics window (its own window,
// never one carried forward) to a distributed run's totals. The first count
// on a controller starts from the run totals its retained history recorded:
// the latest point's run totals, or, for history restored from storage, which
// keeps no run totals, the sum of its windows (bounded and approximate, as
// the published windows it sums were). Stored windows keep no groups either,
// so only a run with one group starts its group's totals from that sum. The
// caller holds the runner lock.
func (a *runTotalsAccumulator) countWindow(window *runTotalsWindow, history []*LiveMetrics, configuredGroups []TestGroup) {
	if !a.initialized && !a.seedFromRunTotals(history) {
		a.seed(history, time.Time{})
		if len(a.groups) == 0 && len(configuredGroups) == 1 {
			a.groups[configuredGroups[0].Name] = runTotalsCounts{tx: a.tx, errors: a.errors}
		}
	}
	if a.groups == nil {
		a.groups = map[string]runTotalsCounts{}
	}
	a.distributed = true
	if window == nil {
		return
	}
	a.tx += window.tx
	a.errors += window.errors
	for name, group := range window.groups {
		counts := a.groups[name]
		counts.tx += group.tx
		counts.errors += group.errors
		a.groups[name] = counts
	}
}

// seedFromRunTotals starts the totals from the latest history point that
// carries run totals, and reports whether one did.
func (a *runTotalsAccumulator) seedFromRunTotals(history []*LiveMetrics) bool {
	for index := len(history) - 1; index >= 0; index-- {
		point := history[index]
		if point == nil || point.RunTotalTx == nil || point.RunTotalErrors == nil {
			continue
		}
		a.initialized = true
		a.tx, a.errors = *point.RunTotalTx, *point.RunTotalErrors
		a.groups = map[string]runTotalsCounts{}
		for name, group := range point.GroupMetrics {
			if group != nil && group.RunTransactions != nil && group.RunErrors != nil {
				a.groups[name] = runTotalsCounts{tx: *group.RunTransactions, errors: *group.RunErrors}
			}
		}
		return true
	}
	return false
}

// apply folds one published sample into the totals (unless a distributed
// run counts its accepted windows instead) and writes the run-to-date fields
// onto it. The caller holds the runner lock.
func (a *runTotalsAccumulator) apply(live *LiveMetrics, history []*LiveMetrics) {
	if live == nil {
		return
	}
	if !a.initialized {
		a.seed(history, live.Timestamp)
	}
	switch {
	case a.distributed:
	case live.Timestamp.IsZero() || live.Timestamp.After(a.lastWindow):
		a.add(live)
	case live.Timestamp.Equal(a.lastWindow):
		a.replaceLatest(live)
	}
	live.RunTotalTx = ptrTo(a.tx)
	live.RunTotalErrors = ptrTo(a.errors)
	live.RunErrorPct = ptrTo(errorPercent(a.tx, a.errors))
	for name, group := range live.GroupMetrics {
		if group == nil {
			continue
		}
		counts := a.groups[name]
		group.RunTransactions = ptrTo(counts.tx)
		group.RunErrors = ptrTo(counts.errors)
		group.RunErrorPct = ptrTo(errorPercent(counts.tx, counts.errors))
	}
}

// errorPercent is failed attempts over all attempts. Successful transactions
// and errors are counted separately throughout live metrics.
func errorPercent(successes, errors int64) float64 {
	attempts := successes + errors
	if attempts <= 0 {
		return 0
	}
	return float64(errors) / float64(attempts) * 100
}

func ptrTo[T any](value T) *T { return &value }
