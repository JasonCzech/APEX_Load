package api

import (
	"encoding/json"
	"errors"
	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"github.com/jasonczech/apexload/pkg/storage"
	"time"
)

type providerCollectionResult struct {
	Points   []analysis.MetricPoint
	Evidence []monitorprovider.Diagnostic
	Topology []monitorprovider.Entity
	Coverage map[string]monitorprovider.Coverage
	Progress monitorprovider.QueryProgress
}

func boundedProviderMonitorBatch(result providerCollectionResult, job storage.RunMonitorJob, from, to time.Time) (storage.ProviderMonitorBatch, int, int, error) {
	n, m := min(256, len(result.Points)), min(50, len(result.Evidence))
	for {
		payload := storage.ProviderMonitorBatch{Points: result.Points[:n], Evidence: result.Evidence[:m], QueryFromUTC: from, QueryUntilUTC: to}
		if n == len(result.Points) && m == len(result.Evidence) {
			payload.Coverage, payload.Topology, payload.Progress = result.Coverage, result.Topology, &result.Progress
			if result.Progress.Complete {
				payload.QueryWindowEnd = &to
				payload.Final = job.State == "reconciling"
			}
		}
		body, err := json.Marshal(payload)
		// Leave room for server-owned dimensions and run-clock normalization.
		if err == nil && len(body) <= 768<<10 {
			return payload, n, m, nil
		}
		if err != nil {
			return payload, 0, 0, errors.New("invalid monitoring result encoding")
		}
		if n > 1 {
			n = (n + 1) / 2
		} else if m > 1 {
			m = (m + 1) / 2
		} else {
			return payload, 0, 0, errors.New("monitoring record exceeds provider batch limit")
		}
	}
}
func directCoverageBatch(result providerCollectionResult, job storage.RunMonitorJob, from, to time.Time) storage.ProviderMonitorBatch {
	p := storage.ProviderMonitorBatch{Coverage: result.Coverage, Topology: result.Topology, Progress: &result.Progress, QueryFromUTC: from, QueryUntilUTC: to}
	if result.Progress.Complete {
		p.QueryWindowEnd = &to
		p.Final = job.State == "reconciling"
	}
	return p
}
