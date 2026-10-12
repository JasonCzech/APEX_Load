// Client-side model for backend "threshold_alert" websocket events (global
// Settings → Thresholds SLA breaches on running tests). The backend rebuilds
// alerts every metrics tick while a breach persists, so entries are keyed and
// upserted rather than appended, and dismissal is an acknowledge flag — a
// removed entry would simply resurrect on the next tick.

import type { WSMessage } from './types'

export interface ThresholdAlertEntry {
  key: string
  testId: string
  testName?: string
  metric: string
  value: number
  threshold: number
  targetTps?: number
  gapPct?: number
  /** The window a goal run's throughput was judged over, e.g. "10 s avg". */
  window?: string
  action?: 'stop_requested'
  transaction?: string
  dependencyType?: string
  dependencyTarget?: string
  firstSeenAt: string
  lastSeenAt: string
  acknowledged: boolean
  active: boolean
}

// Cap sized so that even a project with many concurrently-breaching keys
// rarely evicts a live entry: eviction removes the stalest lastSeenAt first,
// so the victim is normally an alert whose breach (or run) already ended.
// If an acknowledged-but-active entry is ever evicted under extreme pressure,
// its next tick re-adds it unacknowledged — accepted bound on client memory.
export const MAX_THRESHOLD_ALERTS = 50

const THRESHOLD_ALERT_METRIC_LABELS: Record<string, string> = {
  p99_ms: 'P99 latency',
  error_pct: 'Error rate',
  throughput_gap_pct: 'Throughput below target',
  throughput_over_pct: 'Throughput above target',
  avg_server_processing_ms: 'Avg server processing',
  avg_transfer_ms: 'Avg transfer time',
}

export function thresholdAlertMetricLabel(metric: string): string {
  return THRESHOLD_ALERT_METRIC_LABELS[metric] || metric
}

function asFiniteNumber(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined
}

function asNonEmptyString(value: unknown): string | undefined {
  return typeof value === 'string' && value.trim() !== '' ? value : undefined
}

// Runtime-checked parse of the backend event shape (telemetry.go
// buildThresholdAlerts): { type: "threshold_alert", test_id, test_name?,
// timestamp?, metric, value, threshold, action?, transaction?,
// target_tps?, gap_pct?, window?, dependency_type?, dependency_target? }.
export function thresholdAlertFromMessage(msg: WSMessage): ThresholdAlertEntry | null {
  const raw = msg as unknown as Record<string, unknown>
  const testId = asNonEmptyString(raw.test_id)
  const metric = asNonEmptyString(raw.metric)
  const value = asFiniteNumber(raw.value)
  const threshold = asFiniteNumber(raw.threshold)
  if (!testId || !metric || value === undefined || threshold === undefined) return null

  const transaction = asNonEmptyString(raw.transaction)
  const timestamp = asNonEmptyString(raw.timestamp) || new Date().toISOString()
  return {
    key: `${testId}:${metric}:${transaction ?? ''}`,
    testId,
    testName: asNonEmptyString(raw.test_name),
    metric,
    value,
    threshold,
    targetTps: asFiniteNumber(raw.target_tps),
    gapPct: asFiniteNumber(raw.gap_pct),
    window: asNonEmptyString(raw.window),
    action: raw.action === 'stop_requested' ? 'stop_requested' : undefined,
    transaction,
    dependencyType: asNonEmptyString(raw.dependency_type),
    dependencyTarget: asNonEmptyString(raw.dependency_target),
    firstSeenAt: timestamp,
    lastSeenAt: timestamp,
    acknowledged: false,
    active: true,
  }
}

export function upsertThresholdAlert(previous: ThresholdAlertEntry[], next: ThresholdAlertEntry): ThresholdAlertEntry[] {
  const existing = previous.find(entry => entry.key === next.key)
  const merged: ThresholdAlertEntry = existing?.active
    ? {
      ...next,
      firstSeenAt: existing.firstSeenAt,
      acknowledged: existing.acknowledged,
    }
    : next
  const entries = [merged, ...previous.filter(entry => entry.key !== next.key)]
  if (entries.length <= MAX_THRESHOLD_ALERTS) return entries
  // Evict the stalest entry by lastSeenAt (numeric compare; ties fall back to
  // least-recently-upserted, i.e. the deepest list position). The victim is
  // normally an alert whose breach or run already ended. Note this is only a
  // memory bound, not ack protection: under >cap concurrently-live keys an
  // acknowledged entry can still be evicted and re-add unacknowledged.
  const seenAtMillis = (entry: ThresholdAlertEntry) => {
    const parsed = Date.parse(entry.lastSeenAt)
    return Number.isNaN(parsed) ? 0 : parsed
  }
  let stalestIndex = 1
  for (let i = 2; i < entries.length; i++) {
    if (seenAtMillis(entries[i]) <= seenAtMillis(entries[stalestIndex])) stalestIndex = i
  }
  return entries.filter((_, index) => index !== stalestIndex)
}

export function acknowledgeThresholdAlert(previous: ThresholdAlertEntry[], key: string): ThresholdAlertEntry[] {
  return previous.map(entry => (entry.key === key ? { ...entry, acknowledged: true } : entry))
}

export function resolveThresholdAlertsBefore(
  previous: ThresholdAlertEntry[],
  testId: string,
  cutoffTimestamp: string,
): ThresholdAlertEntry[] {
  const cutoff = Date.parse(cutoffTimestamp)
  if (!testId || Number.isNaN(cutoff)) return previous
  let changed = false
  const next = previous.map(entry => {
    if (entry.testId !== testId || !entry.active) return entry
    const lastSeen = Date.parse(entry.lastSeenAt)
    if (Number.isNaN(lastSeen) || lastSeen >= cutoff) return entry
    changed = true
    return { ...entry, active: false }
  })
  return changed ? next : previous
}

export function resolveThresholdAlertsForTest(
  previous: ThresholdAlertEntry[],
  testId: string,
): ThresholdAlertEntry[] {
  let changed = false
  const next = previous.map(entry => {
    if (entry.testId !== testId || !entry.active) return entry
    changed = true
    return { ...entry, active: false }
  })
  return changed ? next : previous
}

export function appendProjectThresholdAlert(
  previous: Record<string, ThresholdAlertEntry[]>,
  projectID: string,
  next: ThresholdAlertEntry,
): Record<string, ThresholdAlertEntry[]> {
  if (!projectID) return previous
  return {
    ...previous,
    [projectID]: upsertThresholdAlert(previous[projectID] || [], next),
  }
}

export function acknowledgeProjectThresholdAlert(
  previous: Record<string, ThresholdAlertEntry[]>,
  projectID: string,
  key: string,
): Record<string, ThresholdAlertEntry[]> {
  if (!projectID || !previous[projectID]) return previous
  return {
    ...previous,
    [projectID]: acknowledgeThresholdAlert(previous[projectID], key),
  }
}

export function resolveProjectThresholdAlertsBefore(
  previous: Record<string, ThresholdAlertEntry[]>,
  projectID: string,
  testId: string,
  cutoffTimestamp: string,
): Record<string, ThresholdAlertEntry[]> {
  if (!projectID || !previous[projectID]) return previous
  const next = resolveThresholdAlertsBefore(previous[projectID], testId, cutoffTimestamp)
  return next === previous[projectID] ? previous : { ...previous, [projectID]: next }
}

export function resolveProjectThresholdAlertsForTest(
  previous: Record<string, ThresholdAlertEntry[]>,
  projectID: string,
  testId: string,
): Record<string, ThresholdAlertEntry[]> {
  if (!projectID || !previous[projectID]) return previous
  const next = resolveThresholdAlertsForTest(previous[projectID], testId)
  return next === previous[projectID] ? previous : { ...previous, [projectID]: next }
}

export function isThresholdAlertTerminalStatus(status?: string): boolean {
  const normalized = status?.trim().toUpperCase()
  return normalized === 'COMPLETE' || normalized === 'FAILED' || normalized === 'ABORTED'
}

export function thresholdAlertsForProject(
  alertsByProject: Record<string, ThresholdAlertEntry[]>,
  projectID?: string,
): ThresholdAlertEntry[] {
  return projectID ? alertsByProject[projectID] || [] : []
}

function formatThresholdAlertNumber(value: number): string {
  return Number.isInteger(value) ? String(value) : value.toFixed(2)
}

export function formatThresholdAlertSummary(entry: ThresholdAlertEntry): string {
  if (entry.metric === 'throughput_gap_pct' || entry.metric === 'throughput_over_pct') {
    const over = entry.metric === 'throughput_over_pct'
    const targetTps = entry.targetTps ?? entry.threshold
    const window = entry.window ? ` (${entry.window})` : ''
    const gap = entry.gapPct === undefined ? '' : ` (${formatThresholdAlertNumber(entry.gapPct)}% ${over ? 'over' : 'gap'})`
    return `Throughput ${formatThresholdAlertNumber(entry.value)} TPS${window} is ${over ? 'above' : 'below'} target ${formatThresholdAlertNumber(targetTps)} TPS${gap}`
  }
  const unit = entry.metric.endsWith('_ms') ? ' ms' : entry.metric.endsWith('_pct') ? '%' : ''
  const base = `${thresholdAlertMetricLabel(entry.metric)} ${formatThresholdAlertNumber(entry.value)}${unit} exceeded threshold ${formatThresholdAlertNumber(entry.threshold)}${unit}`
  const scope = entry.transaction ? ` for transaction ${entry.transaction}` : ''
  const stop = entry.action === 'stop_requested' ? ' — auto-stop requested' : ''
  return `${base}${scope}${stop}`
}
