/**
 * Shared headroom-bar tone. Extracted from EntitlementHeadroom so the
 * entitlement strip and the cluster quota display cannot drift apart on their
 * thresholds.
 *
 * Note: MonitoringPage's capacity gauge uses different (85%/60%) thresholds.
 * That is a different semantic — gauge severity, not headroom — and is
 * deliberately NOT unified here; merging them would be a behavior change
 * disguised as a refactor.
 */
export function headroomTone(used: number, limit: number): { pct: number; cls: string } {
  if (!limit || limit <= 0) return { pct: 0, cls: 'pp-bar-fill-success' }
  const pct = Math.min(100, Math.round((used / limit) * 100))
  const cls = pct >= 90 ? 'pp-bar-fill-danger' : pct >= 70 ? 'pp-bar-fill-warning' : 'pp-bar-fill-success'
  return { pct, cls }
}
