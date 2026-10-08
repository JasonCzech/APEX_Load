package api

import "time"

// runTotalsAccumulator derives run-to-date transaction and error counters
// from the stream of per-window live samples a test publishes.
//
// Every window contributes exactly once. Distributed publication can revise
// the most recent window (a late generator contribution is folded into the
// same timestamp), so a sample carrying the latest timestamp replaces that
// window's earlier contribution instead of adding to it. Samples older than
// the latest window are ignored: their window was already counted. When the
// accumulator starts empty while the test already has history (controller
// failover), it is seeded from the retained history first.
type runTotalsAccumulator struct {
	initialized bool
	lastWindow  time.Time
	lastTx      int64
	lastErrors  int64
	lastGroups  map[string]runTotalsCounts
	tx          int64
	errors      int64
	groups      map[string]runTotalsCounts
}

type runTotalsCounts struct {
	tx     int64
	errors int64
}

// seed rebuilds the totals from retained history. History is bounded, so a
// very long run that failed over may undercount; the window counters on every
// sample are unaffected.
func (a *runTotalsAccumulator) seed(history []*LiveMetrics, before time.Time) {
	a.initialized = true
	a.groups = map[string]runTotalsCounts{}
	for _, point := range history {
		if point == nil || point.Timestamp.IsZero() || !point.Timestamp.Before(before) {
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

// apply folds one published sample into the totals and writes the run-to-date
// fields onto it. The caller holds the runner lock.
func (a *runTotalsAccumulator) apply(live *LiveMetrics, history []*LiveMetrics) {
	if live == nil {
		return
	}
	if !a.initialized {
		a.seed(history, live.Timestamp)
	}
	switch {
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
