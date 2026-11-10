/**
 * Shared phase vocabulary for every waterfall in the dashboard.
 *
 * Keys keep the debugger's historical names so CSS classes (`phase-wait`,
 * `phase-receive`) and retained evidence stay stable. Aggregate load-run
 * vocabulary (`server`, `download`) maps onto them via AGGREGATE_PHASE_KEYS.
 */
export type WaterfallPhaseKey =
  | 'queueing'
  | 'dns'
  | 'connect'
  | 'tls'
  | 'send'
  | 'wait'
  | 'receive'
  | 'total'

export type WaterfallPhaseCategory = 'connection' | 'request' | 'execution'

export interface WaterfallPhaseDescriptor {
  key: WaterfallPhaseKey
  label: string
  shortLabel: string
  category: WaterfallPhaseCategory
}

export const WATERFALL_PHASES: readonly WaterfallPhaseDescriptor[] = (
  [
    ['queueing', 'Queueing / stalled', 'Queueing', 'connection'],
    ['dns', 'DNS lookup', 'DNS', 'connection'],
    ['connect', 'Initial connection', 'Connect', 'connection'],
    ['tls', 'TLS handshake', 'TLS', 'connection'],
    ['send', 'Request sent', 'Send', 'request'],
    ['wait', 'Waiting (TTFB)', 'Wait (TTFB)', 'request'],
    ['receive', 'Content download', 'Download', 'request'],
    ['total', 'Action execution', 'Execution', 'execution'],
  ] as const
).map(([key, label, shortLabel, category]) => ({ key, label, shortLabel, category }))

export const WATERFALL_PHASE_BY_KEY = Object.fromEntries(WATERFALL_PHASES.map(p => [p.key, p])) as Record<
  WaterfallPhaseKey,
  WaterfallPhaseDescriptor
>

export const WATERFALL_PHASE_ORDER = Object.fromEntries(WATERFALL_PHASES.map((p, i) => [p.key, i])) as Record<WaterfallPhaseKey, number>

/** Phases a legend can toggle: the wire phases between queueing and execution. */
export const WATERFALL_LEGEND_PHASES: readonly WaterfallPhaseDescriptor[] = WATERFALL_PHASES.slice(1, 7)

/** Every phase a bar can draw, in legend order (queueing and execution included). */
export const WATERFALL_TOGGLE_PHASES: readonly WaterfallPhaseDescriptor[] = WATERFALL_PHASES

/** Load-run aggregate vocabulary (plan F1) mapped onto the shared keys. */
export const AGGREGATE_PHASE_KEYS = {
  queueing: 'queueing',
  dns: 'dns',
  connect: 'connect',
  tls: 'tls',
  server: 'wait',
  download: 'receive',
} as const satisfies Record<string, WaterfallPhaseKey>

export type AggregatePhaseName = keyof typeof AGGREGATE_PHASE_KEYS

export type WaterfallTimingSource = 'har' | 'browser' | 'protocol_evidence' | 'duration' | 'aggregate'

export const TIMING_SOURCE_LABELS: Record<WaterfallTimingSource, { label: string; description: string }> = {
  har: { label: 'Measured', description: 'Measured per phase from the retained HAR exchange.' },
  browser: { label: 'Browser', description: 'Reported by the browser resource-timing API.' },
  protocol_evidence: { label: 'Derived', description: 'Derived from protocol trace timings (DNS, connect, TLS, TTFB).' },
  duration: { label: 'Total only', description: 'Only the total action time was retained; phases are unavailable.' },
  aggregate: { label: 'Aggregate', description: 'Aggregated across many requests; phase values are statistics, not one request.' },
}

const isNum = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v)
const posNum = (v: unknown) => (isNum(v) && v > 0 ? v : 0)

export function formatWaterfallMs(ms: number): string {
  const safe = posNum(ms)
  return safe >= 1000 ? `${(safe / 1000).toFixed(2)} s` : `${safe.toFixed(1)} ms`
}
