package storage

import (
	"errors"
	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"time"
)

const maxPrometheusCoveragePoints = 200_000_000

func validatePrometheusCoverage(job RunMonitorJob, coverage map[string]monitorprovider.Coverage, received time.Time) error {
	if len(coverage) > 256 {
		return errors.New("Prometheus coverage capability budget exceeded")
	}
	for key, cov := range coverage {
		if len(key) > 256 || cov.Points < 0 || cov.Points > maxPrometheusCoveragePoints || cov.ExpectedPoints < 0 || cov.ExpectedPoints > maxPrometheusCoveragePoints || cov.MissingPoints < 0 || cov.MissingPoints > maxPrometheusCoveragePoints || cov.Evidence < 0 || cov.Evidence > 10000 {
			return errors.New("invalid Prometheus coverage counters")
		}
		diagnostic := key == "targets" || key == "alerts"
		if cov.CacheAgeSeconds < 0 || cov.CacheAgeSeconds > 60 || cov.Cached && (!diagnostic || cov.ObservedAt == nil) || !cov.Cached && cov.CacheAgeSeconds != 0 {
			return errors.New("invalid Prometheus diagnostic cache metadata")
		}
		if cov.ObservedAt != nil && (!diagnostic || cov.ObservedAt.Location() != time.UTC || cov.ObservedAt.Before(job.StartedAt) || cov.ObservedAt.After(received.Add(30*time.Second)) || job.LoadEndedAt != nil && cov.ObservedAt.After(*job.LoadEndedAt)) {
			return errors.New("invalid Prometheus diagnostic observation time")
		}
	}
	return nil
}

// Cursor receipts count only the newly queried grid segment. Completion status
// belongs to the last segment unless any segment was truncated or warned of
// incomplete upstream results. Missing points remain available for grid coverage.
func mergePrometheusChunkCoverage(prior, next monitorprovider.Coverage) monitorprovider.Coverage {
	next.Points += prior.Points
	next.ExpectedPoints += prior.ExpectedPoints
	next.MissingPoints += prior.MissingPoints
	next.Truncated = next.Truncated || prior.Truncated
	next.Warnings = mergePrometheusMessages(prior.Warnings, next.Warnings)
	next.Infos = mergePrometheusMessages(prior.Infos, next.Infos)
	if prior.Freshness == "stale" {
		next.Freshness = "stale"
	}
	if (next.Truncated || len(next.Warnings) > 0) && next.Status == "complete" {
		next.Status = "partial"
	}
	return next
}

func mergePrometheusMessages(prior, next []string) []string {
	result := make([]string, 0, 8)
	seen := map[string]bool{}
	for _, list := range [][]string{prior, next} {
		for _, message := range list {
			message = boundedtext.TruncateUTF8(message, 512)
			if !seen[message] && message != "" {
				seen[message] = true
				result = append(result, message)
				if len(result) == 8 {
					return result
				}
			}
		}
	}
	return result
}
