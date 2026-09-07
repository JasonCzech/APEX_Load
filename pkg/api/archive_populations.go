package api

import "time"

// ArchiveMetricPopulation keeps protocol actions separate from enclosing
// business transactions. Archive records remain available as raw evidence.
type ArchiveMetricPopulation struct {
	RunArchiveActionStats
	AverageRate float64 `json:"average_rate_per_second"`
}

type archivePopulationAccumulator struct {
	stats       RunArchiveActionStats
	digest      archiveQuantileAccumulator
	total       float64
	first, last time.Time
}

func archiveEventPopulation(tx TransactionEvent) string {
	if tx.Population == "action" || tx.Population == "transaction" {
		return tx.Population
	}
	if tx.BusinessTransaction {
		return "transaction"
	}
	// Dependency identity is positive protocol evidence. Lack of retained detail
	// is not evidence that a legacy event was an enclosing transaction.
	if tx.DependencyType != "" {
		return "action"
	}
	return "unknown"
}

func (a *archivePopulationAccumulator) add(tx TransactionEvent) {
	a.stats.Transactions++
	if !tx.Success {
		a.stats.Errors++
	}
	a.total += tx.DurationMs
	a.digest.Add(tx.DurationMs)
	if a.stats.Transactions == 1 || tx.DurationMs < a.stats.MinResponseMs {
		a.stats.MinResponseMs = tx.DurationMs
	}
	if tx.DurationMs > a.stats.MaxResponseMs {
		a.stats.MaxResponseMs = tx.DurationMs
	}
	if a.first.IsZero() || tx.Timestamp.Before(a.first) {
		a.first = tx.Timestamp
	}
	end := tx.Timestamp.Add(time.Duration(tx.DurationMs * float64(time.Millisecond)))
	if end.After(a.last) {
		a.last = end
	}
}

func (a *archivePopulationAccumulator) finish() ArchiveMetricPopulation {
	result := ArchiveMetricPopulation{RunArchiveActionStats: a.stats}
	if a.stats.Transactions == 0 {
		return result
	}
	result.AvgResponseMs = a.total / float64(a.stats.Transactions)
	result.ErrorPct = 100 * float64(a.stats.Errors) / float64(a.stats.Transactions)
	result.P50Ms, result.P90Ms = a.digest.Quantile(.5), a.digest.Quantile(.9)
	result.P95Ms, result.P99Ms = a.digest.Quantile(.95), a.digest.Quantile(.99)
	if seconds := a.last.Sub(a.first).Seconds(); seconds > 0 {
		result.AverageRate = float64(a.stats.Transactions) / seconds
	}
	return result
}
