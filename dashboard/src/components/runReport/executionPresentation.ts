import { formatDateTime } from '../../utils/dateTime'
import { formatDurationDisplay, formatTimeLong } from '../../utils/timeUtils'
import type { MetricsHistoryPoint, RunArchiveReport, TestRunManifest, TestSummary } from '../../types'
import { buildQueuedScenarioPreview } from '../testExecutionScenarioModel'

export function recordedTime(value?: string): number | undefined {
  if (!value) return undefined
  const date = new Date(value)
  return Number.isFinite(date.getTime()) && date.getUTCFullYear() > 1 ? date.getTime() : undefined
}

export function executionDate(value?: string): string {
  const time = recordedTime(value)
  return time === undefined ? 'Not recorded' : formatDateTime(time, { seconds: true })
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

/**
 * How long a run has run, in seconds: the time since it started with its
 * paused time left out. The engine makes paused time up (a 4 m run paused for
 * 15 s ends 15 s later), so this is the clock its duration counts against.
 * Undefined until the run has a recorded start.
 */
export function runActiveSeconds(test: Pick<TestSummary, 'started_at' | 'start_time' | 'ended_at' | 'paused_at' | 'cumulative_pause_ns'>, now = Date.now()): number | undefined {
  const start = recordedTime(test.started_at) ?? recordedTime(test.start_time)
  if (start === undefined) return undefined
  const end = recordedTime(test.ended_at) ?? now
  const pausedAt = recordedTime(test.paused_at)
  const pausedMs = Math.max(0, Number(test.cumulative_pause_ns) || 0) / 1e6 + (pausedAt === undefined ? 0 : Math.max(0, end - pausedAt))
  return Math.max(0, (end - start - pausedMs) / 1000)
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
    duration: formatDurationDisplay(immutable ? config?.duration : test.duration) || undefined,
    vusers: immutable ? config?.total_vusers : test.total_vusers,
    groups: (immutable ? config?.groups : test.groups) ?? [],
    started,
    ended,
    elapsed: formatElapsedSeconds(elapsed),
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

/**
 * The run's planned load as the engine ran it, from its immutable initial
 * configuration when one was preserved, otherwise from the retained record.
 * A run to its iterations is planned through the time it ran.
 */
export function executionLoadPlan(test: TestSummary, manifest?: TestRunManifest | null) {
  const config = manifest?.initial_configuration_provenance?.immutable === true ? manifest.initial_configuration : undefined
  return buildQueuedScenarioPreview(config ? {
    ...test,
    duration: config.duration ?? test.duration,
    start_delay: config.start_delay ?? test.start_delay,
    resource_baseline: config.resource_baseline ?? test.resource_baseline,
    scenario_mode: config.scenario_mode ?? test.scenario_mode,
    goal_config: config.goal_config ?? test.goal_config,
    groups: config.groups ?? [],
  } : test, lifecycleElapsedSeconds(test))
}

/** Observed VUser samples on elapsed seconds from the run's start (or its first sample). */
export function executionObservedLoad(points: MetricsHistoryPoint[], started?: string) {
  const origin = recordedTime(started) ?? points[0]?.timestampMs
  if (origin === undefined) return []
  return points.flatMap(point => point.timestampMs !== undefined && point.timestampMs >= origin ? [{ second: (point.timestampMs - origin) / 1000, vusers: point.vusers, timestampMs: point.timestampMs }] : [])
}
