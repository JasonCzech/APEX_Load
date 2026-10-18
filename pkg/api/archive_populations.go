package api

import (
	"fmt"
	"time"
)

// ArchiveMetricPopulation keeps protocol actions separate from enclosing
// business transactions. Archive records remain available as raw evidence.
type ArchiveMetricPopulation struct {
	RunArchiveActionStats
	AverageRate *float64 `json:"average_rate_per_second,omitempty"`
}

type archivePopulationAccumulator struct {
	stats  RunArchiveActionStats
	digest archiveQuantileAccumulator
	total  float64
}

// newArchiveGatePopulationAccumulator returns a population accumulator whose
// percentiles can decide a release gate.
func newArchiveGatePopulationAccumulator(budget *archiveQuantileBudget) *archivePopulationAccumulator {
	return &archivePopulationAccumulator{digest: archiveQuantileAccumulator{gate: true, budget: budget}}
}

func archiveEventPopulation(tx TransactionEvent) string {
	return eventPopulation(tx.Population, tx.BusinessTransaction, tx.DependencyType)
}

// eventPopulation classifies an event from the three fields that decide it.
func eventPopulation(population string, businessTransaction bool, dependencyType string) string {
	if population == "action" || population == "transaction" {
		return population
	}
	if businessTransaction {
		return "transaction"
	}
	// Dependency identity is positive protocol evidence. Lack of retained detail
	// is not evidence that a legacy event was an enclosing transaction.
	if dependencyType != "" {
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

}

func (a *archivePopulationAccumulator) finish(window time.Duration) ArchiveMetricPopulation {
	result := ArchiveMetricPopulation{RunArchiveActionStats: a.stats}
	if window > 0 {
		rate := float64(a.stats.Transactions) / window.Seconds()
		result.AverageRate = &rate
	}
	if a.stats.Transactions == 0 {
		return result
	}
	result.AvgResponseMs = a.total / float64(a.stats.Transactions)
	result.ErrorPct = 100 * float64(a.stats.Errors) / float64(a.stats.Transactions)
	result.P50Ms, result.P90Ms = a.digest.Quantile(.5), a.digest.Quantile(.9)
	result.P95Ms, result.P99Ms = a.digest.Quantile(.95), a.digest.Quantile(.99)
	result.PercentileQuality = a.digest.PercentileQuality()
	return result
}

// Use elapsed execution time, including pauses and idle/pacing time, for every
// population. Missing or invalid legacy timestamps cannot establish a rate.
func archivePopulationWindow(manifest *RunArchiveManifest) time.Duration {
	if manifest == nil || manifest.Provenance.StartedAt == nil || manifest.Provenance.StartedAt.IsZero() || archiveExecutionCompletedAt(manifest).IsZero() {
		return 0
	}
	if elapsed := archiveExecutionCompletedAt(manifest).Sub(*manifest.Provenance.StartedAt); elapsed > 0 {
		return elapsed
	}
	return 0
}

// reportPopulationPeakTPS matches the report screen, which shows the peak as
// unavailable when the archive recorded none.
func reportPopulationPeakTPS(population ArchiveMetricPopulation) string {
	if population.PeakTPS <= 0 {
		return "Unknown"
	}
	return formatCSVFloat(population.PeakTPS)
}

func reportPopulationRate(rate *float64) string {
	if rate == nil {
		return "Unknown"
	}
	return fmt.Sprintf("%.3f", *rate)
}
