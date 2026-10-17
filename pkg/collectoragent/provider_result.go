package collectoragent

import (
	"time"

	"github.com/jasonczech/apexload/pkg/monitorprovider"
)

// completeProviderResult retains evidence from failed reads and supplies only
// missing progress. It never fabricates points or overrides provider coverage.
func completeProviderResult(job RunJob, result providerResult, collectErr error, from, to time.Time) providerResult {
	// Failed provider reads may still produce capability/coverage evidence. Keep
	// them visible without inventing zero-valued measurements.
	if collectErr != nil && len(result.Coverage) == 0 {
		key := "collection"
		if job.Pack == monitorprovider.PackDynatrace {
			key = "controller"
		}
		if job.Prometheus != nil {
			// Prometheus ingestion accepts this bounded failure capability even
			// when no approved query could execute. Keep the failed read durable
			// so its receipt can clear the spool and a later poll can recover.
			key = "query"
		}
		result.Coverage = map[string]monitorprovider.Coverage{key: {Status: "unavailable", From: from, To: to, Message: "provider collection failed"}}
	}
	if job.Prometheus != nil && result.Progress.TotalQueries == 0 {
		total := len(job.Prometheus.Selection.Queries)
		if job.Prometheus.Selection.Targets {
			total++
		}
		if job.Prometheus.Selection.Alerts {
			total++
		}
		if job.Reconciling {
			total += len(job.TerminalWindows)
		}
		result.Progress = monitorprovider.QueryProgress{TotalQueries: total, NextQuery: job.Prometheus.Selection.QueryOffset, NextTime: job.Prometheus.Selection.QueryTime}
	}
	return result
}

// providerNextCollectionTime runs only after the controller accepts the final
// retained batch. An unfinished query (or a captured Prometheus-only prefix)
// must resume immediately; otherwise retain normal polling cadence.
func providerNextCollectionTime(now time.Time, job RunJob, acknowledged *RunSampleBatch) time.Time {
	if acknowledged.Progress != nil && !acknowledged.Progress.Complete ||
		job.Prometheus != nil && job.Reconciling && acknowledged.Progress == nil {
		return now
	}
	if job.Reconciling {
		return now.Add(15 * time.Second)
	}
	return now.Add(time.Duration(job.IntervalSeconds) * time.Second)
}
