// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import type { MetricsHistoryPoint } from '../types'
import PhaseShareHeatmap from './PhaseShareHeatmap'
import { PHASE_SHARE_ROWS, phaseShareColumns, phaseShareSummary } from './phaseShareHeatmapModel'

function point(time: string, tps: number, phases: Partial<MetricsHistoryPoint>): MetricsHistoryPoint {
  return { time, timestampMs: Date.parse(`2026-11-10T${time}Z`), vusers: 10, tps, avgMs: 0, errorPct: 0, p50: 0, p90: 0, p99: 0, ...phases }
}

const full = { txDnsMs: 10, txConnectMs: 20, txTlsMs: 30, txServerMs: 100, txDownloadMs: 40, txNoResponseMs: 0 }

describe('phaseShareColumns', () => {
  it('gives each phase its share of the interval time', () => {
    const [column] = phaseShareColumns([point('10:00:00', 5, full)])
    expect(column.reported).toBe(true)
    expect(column.shares.map(s => Math.round((s ?? -1) * 1000))).toEqual([50, 100, 150, 500, 200, 0])
  })

  it('keeps an unreported phase unknown, never zero, and marks intervals with no phases', () => {
    const columns = phaseShareColumns([point('10:00:00', 5, { txDnsMs: 1, txServerMs: 3 }), point('10:00:01', 5, {})])
    expect(columns[0].shares[PHASE_SHARE_ROWS.findIndex(r => r.key === 'tls')]).toBeUndefined()
    expect(columns[0].shares[0]).toBeCloseTo(0.25)
    expect(columns[1].reported).toBe(false)
    expect(columns[1].shares.every(s => s === undefined)).toBe(true)
  })

  it('merges intervals past the column limit, each counting equally', () => {
    const busy = point('10:00:00', 9, { txServerMs: 100, txDnsMs: 0 })
    // Every request failed: no throughput, but its phases still count.
    const outage = point('10:00:01', 0, { txServerMs: 0, txDnsMs: 0, txNoResponseMs: 3000 })
    const [merged] = phaseShareColumns([busy, outage], 1)
    expect(merged.intervals).toBe(2)
    expect(merged.shares[3]).toBeCloseTo(0.5)
    expect(merged.shares[5]).toBeCloseTo(0.5)
    expect(phaseShareColumns(Array.from({ length: 250 }, (_, i) => point(`10:0${Math.floor(i / 60) % 10}:${String(i % 60).padStart(2, '0')}`, 1, full)), 60).length).toBeLessThanOrEqual(60)
  })

  it('summarizes the largest share in sentence case', () => {
    expect(phaseShareSummary(phaseShareColumns([point('10:00:00', 5, full)]))).toBe('Share of sampled time per network phase across 1 of 1 intervals. Server (TTFB − setup) has the largest average share, 50%.')
    expect(phaseShareSummary([])).toBe('No interval reported network phases yet.')
  })
})

describe('PhaseShareHeatmap', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
  })

  it('draws a described grid and offers a table fallback', async () => {
    await act(async () => root.render(<PhaseShareHeatmap title="Phase share heatmap" points={[point('10:00:00', 5, full), point('10:00:01', 5, { ...full, txServerMs: 300 })]} dataChartId="tx_phase_share_heatmap" />))
    const grid = container.querySelector('[role="img"]')!
    expect(grid.getAttribute('aria-label')).toBe('Phase share heatmap heatmap')
    expect(document.getElementById(grid.getAttribute('aria-describedby')!)?.textContent).toContain('Server (TTFB − setup) has the largest average share')
    expect(container.querySelectorAll('.phase-share-cell')).toHaveLength(12)
    const toggle = Array.from(container.querySelectorAll('button')).find(b => b.textContent === 'Show as table')!
    await act(async () => toggle.click())
    const rows = Array.from(container.querySelectorAll('tbody tr'))
    expect(rows).toHaveLength(2)
    expect(Array.from(rows[0].querySelectorAll('td')).map(td => td.textContent)).toEqual(['5%', '10%', '15%', '50%', '20%', '0%'])
    expect(toggle.getAttribute('aria-pressed')).toBe('true')
  })

  it('says why there is nothing to draw', async () => {
    await act(async () => root.render(<PhaseShareHeatmap title="Phase share heatmap" points={[point('10:00:00', 5, {})]} />))
    expect(container.textContent).toContain('No interval in view reported network phases')
    await act(async () => root.render(<PhaseShareHeatmap title="Phase share heatmap" points={[]} />))
    expect(container.textContent).toContain('Waiting for live metrics.')
  })
})
