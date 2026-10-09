import { formatTimeLong } from '../../utils/timeUtils'
import type { MetricsHistoryPoint, RunArchiveReport, TestRunManifest, TestSummary } from '../../types'

export function recordedTime(value?: string): number | undefined {
  if (!value) return undefined
  const date = new Date(value)
  return Number.isFinite(date.getTime()) && date.getUTCFullYear() > 1 ? date.getTime() : undefined
}

export function executionDate(value?: string): string {
  const time = recordedTime(value)
  return time === undefined ? 'Not recorded' : new Date(time).toLocaleString()
}

export function recordedNumber(value?: number): string {
  return value !== undefined && Number.isFinite(value) && value >= 0 ? value.toLocaleString() : 'Not recorded'
}

/** Controller lifecycle timing, never a configured duration or creation timestamp. */
export function lifecycleElapsedSeconds(test: TestSummary): number | undefined {
  const start = recordedTime(test.started_at) ?? recordedTime(test.start_time)
  const end = recordedTime(test.ended_at)
  return start !== undefined && end !== undefined && end >= start ? (end - start) / 1000 : undefined
}

export function formatElapsedSeconds(seconds?: number): string {
  if (seconds === undefined || !Number.isFinite(seconds) || seconds < 0) return 'Not recorded'
  return formatTimeLong(seconds)
}

export function executionFacts(test: TestSummary, report?: RunArchiveReport | null, manifest?: TestRunManifest | null) {
  const immutable = manifest?.initial_configuration_provenance?.immutable === true
  const config = immutable ? manifest.initial_configuration : null
  const started = report ? report.manifest?.provenance?.started_at : test.started_at || test.start_time
  const ended = report ? report.manifest?.completed_at : test.ended_at
  const start = recordedTime(started)
  const end = recordedTime(ended)
  const elapsed = start !== undefined && end !== undefined && end >= start ? Math.round((end - start) / 1000) : undefined
  return {
    immutable,
    lifecycleElapsed: formatElapsedSeconds(lifecycleElapsedSeconds(test)),
    duration: immutable ? config?.duration : test.duration,
    vusers: immutable ? config?.total_vusers : test.total_vusers,
    groups: (immutable ? config?.groups : test.groups) ?? [],
    started,
    ended,
    elapsed: elapsed === undefined ? 'Not recorded' : `${Math.floor(elapsed / 60)}m ${elapsed % 60}s`,
    peak: recordedNumber(report?.overview?.peak_active_vusers),
    reason: report?.manifest?.error || test.error,
  }
}

/** Keep elapsed-time spacing and reject timestamps outside the Date range. */
export function executionLoadPoints(history: MetricsHistoryPoint[]) {
  return history.filter(point => !point.resourceSampleOnly
    && point.timestampMs !== undefined
    && Number.isFinite(new Date(point.timestampMs).getTime())
    && new Date(point.timestampMs).getUTCFullYear() > 1
    && Number.isFinite(point.vusers) && point.vusers >= 0)
    .sort((a, b) => a.timestampMs! - b.timestampMs!)
}
