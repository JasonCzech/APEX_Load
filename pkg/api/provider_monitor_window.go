package api

import (
	"github.com/jasonczech/apexload/pkg/storage"
	"time"
)

func providerQueryWindow(job storage.RunMonitorJob, now time.Time) (time.Time, time.Time) {
	if !job.QueryUntilUTC.IsZero() {
		return job.QueryFromUTC, job.QueryUntilUTC
	}
	end := now.UTC()
	if job.LoadEndedAt != nil {
		end = *job.LoadEndedAt
	}
	start := job.StartedAt
	if job.State != "reconciling" && !job.QueryWatermark.IsZero() {
		start = job.QueryWatermark.Add(-5 * time.Minute)
		if start.Before(job.StartedAt) {
			start = job.StartedAt
		}
	}
	return start, end
}
