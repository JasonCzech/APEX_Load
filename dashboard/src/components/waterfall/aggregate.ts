/**
 * Adapters for the non-debug waterfall inputs (plan F4 b and c). Report and
 * live views build `WaterfallNode`s here and render them with `<Waterfall>`.
 */
import { AGGREGATE_PHASE_KEYS, type AggregatePhaseName } from './phases'
import type { WaterfallNode, WaterfallPhaseSpan, WaterfallStatus } from './types'

const pos = (v: unknown) => (typeof v === 'number' && Number.isFinite(v) && v > 0 ? v : 0)

/**
 * Sequential phases from trace timings. TTFB is measured from request start,
 * so it already contains DNS, connect and TLS: server = ttfb − setup and
 * download = duration − ttfb (plan F1).
 */
export function phasesFromTraceTimings(t: { dns_ms?: number; connect_ms?: number; tls_ms?: number; ttfb_ms?: number; duration_ms?: number }): WaterfallPhaseSpan[] {
  const dns = pos(t.dns_ms)
  const connect = pos(t.connect_ms)
  const tls = pos(t.tls_ms)
  const ttfb = pos(t.ttfb_ms)
  const duration = pos(t.duration_ms)
  const setup = dns + connect + tls
  // Without TTFB the rest of the duration cannot be split into server/download.
  const server = ttfb > 0 ? Math.max(0, ttfb - setup) : 0
  const download = ttfb > 0 ? Math.max(0, duration - Math.max(ttfb, setup)) : 0
  const unsplit = ttfb > 0 ? 0 : Math.max(0, duration - setup)
  const spans: WaterfallPhaseSpan[] = []
  let cursor = 0
  for (const [key, ms] of [['dns', dns], ['connect', connect], ['tls', tls], ['wait', server], ['receive', download]] as const) {
    if (ms > 0) {
      spans.push({ key, startMs: cursor, endMs: cursor + ms })
      cursor += ms
    }
  }
  if (unsplit > 0) spans.push({ key: 'total', startMs: cursor, endMs: cursor + unsplit })
  return spans
}

/** The subset of an archive `TransactionEvent` row a waterfall needs. */
export interface TransactionEventLike {
  id?: string
  name?: string
  request_name?: string
  url?: string
  method?: string
  status_code?: number
  success?: boolean
  error?: string
  timestamp?: string
  offset_ms?: number
  duration_ms?: number
  dns_ms?: number
  connect_ms?: number
  tls_ms?: number
  ttfb_ms?: number
}

export function nodeFromTransactionEvent(row: TransactionEventLike, startMs: number, id = row.id || `${row.name}:${startMs}`): WaterfallNode<TransactionEventLike> {
  const phases = phasesFromTraceTimings(row)
  const hasPhases = phases.some(p => p.key !== 'total')
  return {
    id,
    kind: 'request',
    label: row.request_name || row.name || row.url || 'Request',
    detail: row.url,
    startMs,
    durationMs: pos(row.duration_ms),
    status: row.success === false ? 'failed' : 'passed',
    phases,
    timingSource: hasPhases ? 'protocol_evidence' : 'duration',
    statusCode: row.status_code,
    method: row.method,
    errorMessage: row.error,
    children: [],
    payload: row,
  }
}

export type AggregateStatistic = 'avg' | 'p50' | 'p90' | 'p95' | 'p99'

export interface AggregatePhaseStat {
  avg?: number
  p50?: number
  p90?: number
  p95?: number
  p99?: number
  /** Share of samples where the phase happened (e.g. TLS only on new connections), 0..100. */
  observedPct?: number
}

export type AggregatePhaseVector = Partial<Record<AggregatePhaseName, AggregatePhaseStat>>

export interface AggregateWaterfallInput {
  id: string
  label: string
  detail?: string
  /** Typical offset of this request within its transaction. */
  offsetMs: number
  vector: AggregatePhaseVector
  statistic: AggregateStatistic
  status?: WaterfallStatus
}

/**
 * One aggregate row. Per-phase percentiles do not sum to the total's
 * percentile, so the label states the statistic per phase.
 */
export function nodeFromAggregatePhaseVector(input: AggregateWaterfallInput): WaterfallNode<AggregateWaterfallInput> {
  const spans: WaterfallPhaseSpan[] = []
  let cursor = 0
  for (const name of Object.keys(AGGREGATE_PHASE_KEYS) as AggregatePhaseName[]) {
    const ms = pos(input.vector[name]?.[input.statistic])
    if (ms > 0) {
      spans.push({ key: AGGREGATE_PHASE_KEYS[name], startMs: cursor, endMs: cursor + ms })
      cursor += ms
    }
  }
  return {
    id: input.id,
    kind: 'request',
    label: input.label,
    detail: input.detail ?? `${input.statistic.toUpperCase()} of each phase`,
    startMs: pos(input.offsetMs),
    durationMs: cursor,
    status: input.status ?? 'passed',
    phases: spans,
    timingSource: 'aggregate',
    children: [],
    payload: input,
  }
}
