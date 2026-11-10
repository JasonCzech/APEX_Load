/**
 * L6 phase-share heatmap: time × network phase, each cell the phase's share
 * of the interval's sampled time. Every phase average divides by the same
 * samples, so a phase's share of the summed averages is its share of the
 * summed time. Built from the same history points as the network-phase chart,
 * so a reload (controller history or archive summaries) draws it too.
 */
import type { MetricsHistoryPoint } from '../types'

export interface PhaseShareRow {
  key: 'dns' | 'connect' | 'tls' | 'server' | 'download' | 'noResponse'
  field: 'txDnsMs' | 'txConnectMs' | 'txTlsMs' | 'txServerMs' | 'txDownloadMs' | 'txNoResponseMs'
  label: string
  /** The shared waterfall phase colour token, with its default. */
  color: string
}

export const PHASE_SHARE_ROWS: readonly PhaseShareRow[] = [
  { key: 'dns', field: 'txDnsMs', label: 'DNS', color: 'var(--phase-dns, #0d9488)' },
  { key: 'connect', field: 'txConnectMs', label: 'Connect', color: 'var(--phase-connect, #f59e0b)' },
  { key: 'tls', field: 'txTlsMs', label: 'TLS', color: 'var(--phase-tls, #8b5cf6)' },
  { key: 'server', field: 'txServerMs', label: 'Server (TTFB − setup)', color: 'var(--phase-wait, #10b981)' },
  { key: 'download', field: 'txDownloadMs', label: 'Download', color: 'var(--phase-receive, #0ea5e9)' },
  { key: 'noResponse', field: 'txNoResponseMs', label: 'No TTFB (unsplit)', color: 'var(--phase-total, #64748b)' },
]

/** Columns past this many merge neighbouring intervals. */
export const PHASE_SHARE_MAX_COLUMNS = 60

export interface PhaseShareColumn {
  /** The first merged interval's time. */
  startMs?: number
  label: string
  /** Intervals merged into the column. */
  intervals: number
  /**
   * 0..1 per PHASE_SHARE_ROWS entry; undefined where the phase was not
   * reported (an older generator, or a row stored before phases were kept).
   */
  shares: Array<number | undefined>
  /** False when no interval in the column reported any phase time. */
  reported: boolean
}

const finite = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v)

/**
 * The heatmap columns, oldest first. Intervals merge into at most maxColumns
 * columns, each interval counting equally: throughput counts successes only,
 * so weighting by it would hide an interval where every request failed.
 */
export function phaseShareColumns(points: readonly MetricsHistoryPoint[] | undefined, maxColumns = PHASE_SHARE_MAX_COLUMNS): PhaseShareColumn[] {
  const usable = (points ?? []).filter(Boolean)
  if (!usable.length) return []
  const perColumn = Math.max(1, Math.ceil(usable.length / Math.max(1, maxColumns)))
  const columns: PhaseShareColumn[] = []
  for (let start = 0; start < usable.length; start += perColumn) {
    const group = usable.slice(start, start + perColumn)
    const sums = PHASE_SHARE_ROWS.map(() => 0)
    const seen = PHASE_SHARE_ROWS.map(() => false)
    let total = 0
    const intervalShares = group.map(point => {
      const values = PHASE_SHARE_ROWS.map(row => (finite(point[row.field]) ? Math.max(0, point[row.field] as number) : undefined))
      const sum = values.reduce<number>((acc, v) => acc + (v ?? 0), 0)
      return { values, sum }
    })
    let reportedIntervals = 0
    intervalShares.forEach(({ values, sum }) => {
      if (!(sum > 0)) return
      reportedIntervals++
      values.forEach((value, rowIndex) => {
        if (value === undefined) return
        seen[rowIndex] = true
        sums[rowIndex] += value / sum
      })
    })
    total = reportedIntervals
    const reported = total > 0
    columns.push({
      startMs: group[0].timestampMs,
      label: group[0].time,
      intervals: group.length,
      shares: sums.map((sum, rowIndex) => (reported && seen[rowIndex] ? sum / total : undefined)),
      reported,
    })
  }
  return columns
}

export const formatShare = (share: number | undefined) => (share === undefined ? 'Not reported' : `${Math.round(share * 100)}%`)

/** A one-line description of the heatmap for assistive technology. */
export function phaseShareSummary(columns: readonly PhaseShareColumn[]): string {
  const reported = columns.filter(c => c.reported)
  if (!reported.length) return 'No interval reported network phases yet.'
  const meanShares = PHASE_SHARE_ROWS.map((_, rowIndex) => {
    const values = reported.map(c => c.shares[rowIndex]).filter(finite)
    return values.length ? values.reduce((a, b) => a + b, 0) / values.length : undefined
  })
  let top = -1
  meanShares.forEach((share, index) => {
    if (share !== undefined && (top < 0 || share > (meanShares[top] ?? 0))) top = index
  })
  const lead = top >= 0 ? ` ${PHASE_SHARE_ROWS[top].label} has the largest average share, ${formatShare(meanShares[top])}.` : ''
  return `Share of sampled time per network phase across ${reported.length} of ${columns.length} intervals.${lead}`
}
