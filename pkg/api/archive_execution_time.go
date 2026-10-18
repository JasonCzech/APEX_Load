package api

import "time"

// archiveExecutionCompletedAt is the end of the measured execution. Recovery
// may restamp CompletedAt to the durable run end for archive identity; that
// reconciliation delay must not extend analytical windows or lower rates.
// Older archives have no retained executor time and keep their prior behavior.
func archiveExecutionCompletedAt(manifest *RunArchiveManifest) time.Time {
	if manifest == nil {
		return time.Time{}
	}
	if manifest.ExecutorCompletedAt != nil && !manifest.ExecutorCompletedAt.IsZero() {
		return *manifest.ExecutorCompletedAt
	}
	return manifest.CompletedAt
}
