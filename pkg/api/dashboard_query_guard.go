package api

import (
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// Query-time resource caps for imported-dashboard panels. The curated Prometheus pack
// is six trusted compile-time queries; imported panels carry user-supplied PromQL, so
// these bound what a single dashboard can force the controller (and Prometheus) to do.
// The import-time structural caps (panel/target counts, expr length) live in
// pkg/convert/grafana; these govern execution.
const (
	// maxDashboardSeriesPerPanel refuses a panel whose query fans out past this many
	// series — the single most important guard, since queryPrometheusRange itself
	// imposes no series cap.
	maxDashboardSeriesPerPanel = 50
	// maxDashboardPointsTotal caps the aggregate samples (series × points) a single
	// dashboard resolve may accumulate across all its panels.
	maxDashboardPointsTotal = 200_000
	// maxImportedDashboardQueryResponseBytes is lower than the curated-pack body
	// limit because imported queries fan out and their decoded results are retained
	// together in one response/snapshot.
	maxImportedDashboardQueryResponseBytes = 4 << 20 // 4 MiB
	// Byte budgets account for decoded labels, keys, and points, which the sample
	// count alone cannot bound. They also bound the terminal snapshot payload.
	maxDashboardBytesTotal          = 32 << 20 // 32 MiB
	maxImportedDashboardsBytesTotal = storage.MaxGrafanaDashboardSnapshotBytes
	// Upstream Prometheus error strings are attacker-controlled and otherwise become
	// part of API responses and immutable snapshots once per panel.
	maxDashboardPanelErrorBytes = 1024
	// maxImportedDashboardsPointsTotal caps the complete project aggregate so the
	// bounded dashboard count cannot still multiply into an oversized API/snapshot.
	maxImportedDashboardsPointsTotal = 500_000
	// maxDashboardQueryConcurrency bounds concurrent upstream requests. Aggregate
	// resolves share one semaphore, so dashboard-level fan-out cannot multiply it.
	maxDashboardQueryConcurrency = 8
	// maxDashboardPerQueryTimeoutSeconds bounds a single panel query when the
	// configured Prometheus timeout is unset.
	maxDashboardPerQueryTimeoutSeconds = 15
	// maxDashboardTotalTimeout bounds the whole fan-out. With up to 60 queries at
	// concurrency 8 (~8 waves) a single per-query budget would guarantee failure at
	// scale, so the dashboard budget is a multiple of the per-query one, clamped.
	maxDashboardTotalTimeout = 60 * time.Second
	// maxConcurrentDashboardResolves bounds how many dashboards the aggregate endpoint
	// resolves at once. Kept small because each dashboard already fans out its own
	// panels up to maxDashboardQueryConcurrency, so the peak upstream concurrency is
	// this times that.
	maxConcurrentDashboardResolves = 4
)

// dashboardTotalTimeout derives the whole-dashboard budget from the configured
// per-query timeout: enough headroom for several concurrency waves, clamped so a
// misconfigured large timeout cannot hang the handler indefinitely.
func dashboardTotalTimeout(perQuerySeconds int) time.Duration {
	if perQuerySeconds <= 0 {
		perQuerySeconds = maxDashboardPerQueryTimeoutSeconds
	}
	budget := time.Duration(perQuerySeconds) * 4 * time.Second
	if budget > maxDashboardTotalTimeout {
		return maxDashboardTotalTimeout
	}
	return budget
}
