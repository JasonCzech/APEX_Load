// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import type { LiveMetrics, PhasePercentileSet, TxMetrics } from '../types'
import LiveWaterfallPanel from './LiveWaterfallPanel'
import TestExecutionConnectionHealth from './TestExecutionConnectionHealth'
import { phasePercentileMs, txPhasePercentileVector } from './livePhaseModel'
import { liveWaterfallHasPhasePercentiles, liveWaterfallNodes, liveWaterfallVector } from './liveWaterfallModel'

const percentiles: PhasePercentileSet = {
  version: 1,
  dns: { samples: 5, p50_ms: 2, p90_ms: 4, p95_ms: 5, p99_ms: 9 },
  connect: { samples: 40, p50_ms: 10, p90_ms: 30, p95_ms: 45, p99_ms: 80 },
  tls: { samples: 40, p50_ms: 20, p90_ms: 50, p95_ms: 60, p99_ms: 120 },
  server: { samples: 100, p50_ms: 150, p90_ms: 300, p95_ms: 400, p99_ms: 900 },
  download: { samples: 100, p50_ms: 10, p90_ms: 20, p95_ms: 25, p99_ms: 60 },
}

function tx(partial: Partial<TxMetrics>): TxMetrics {
  return { name: 'Checkout', count: 100, errors: 0, avg_ms: 300, p50_ms: 90, p90_ms: 140, p99_ms: 200, tps: 10, avg_connect_ms: 20, avg_tls_ms: 10, avg_server_ms: 200, avg_no_response_ms: 30, phase_observed: { dns: 5, connect: 40, tls: 40 }, ...partial }
}

function metrics(transactions: Record<string, TxMetrics>, partial: Partial<LiveMetrics> = {}): LiveMetrics {
  return {
    timestamp: '2026-11-10T10:00:00Z', active_vusers: 10, tps: 10, avg_response_ms: 100, error_pct: 0, total_transactions: 100, total_errors: 0,
    p50_ms: 0, p90_ms: 0, p95_ms: 0, p99_ms: 0, transactions, ...partial,
  }
}

describe('phase percentile model', () => {
  it('reads one phase percentile and treats a missing phase as no data', () => {
    expect(phasePercentileMs(percentiles, 'tls', 'p95')).toBe(60)
    expect(phasePercentileMs({ version: 1 }, 'tls', 'p95')).toBeUndefined()
    expect(phasePercentileMs(undefined, 'tls', 'p95')).toBeUndefined()
    expect(phasePercentileMs({ version: 1, dns: { samples: 0, p50_ms: 0, p90_ms: 0, p95_ms: 0, p99_ms: 0 } }, 'dns', 'p50')).toBeUndefined()
  })

  it('lays each phase percentile end to end and falls back to averages without them', () => {
    const vector = txPhasePercentileVector(tx({ phase_percentiles: percentiles }), 'p95')!
    expect([vector.dns, vector.connect, vector.tls, vector.server, vector.download, vector.noResponse]).toEqual([5, 45, 60, 400, 25, 0])
    expect(vector.avgMs).toBe(535)
    expect(txPhasePercentileVector(tx({}), 'p95')).toBeUndefined()
    const fallback = liveWaterfallVector(tx({}), 'p95')
    expect(fallback.percentileFallback).toBe(true)
    expect(fallback.vector.server).toBe(200)
    expect(liveWaterfallVector(tx({ phase_percentiles: percentiles }), 'avg').vector.server).toBe(200)
  })

  it('marks rows that fall back and describes percentile bars as not additive', () => {
    const nodes = liveWaterfallNodes({ Checkout: tx({ phase_percentiles: percentiles }), Login: tx({ name: 'Login', avg_ms: 50 }) }, { statistic: 'p99' })
    const checkout = nodes.find(n => n.id === 'tx:Checkout')!
    const login = nodes.find(n => n.id === 'tx:Login')!
    expect(checkout.phases?.map(p => p.endMs - p.startMs)).toEqual([9, 80, 120, 900, 60])
    expect(checkout.detail).toContain("each phase's own P99, not additive")
    expect(login.payload?.percentileFallback).toBe(true)
    expect(login.detail).toContain('averages: phase percentiles not reported')
    expect(liveWaterfallHasPhasePercentiles({ Login: tx({}) })).toBe(false)
    expect(liveWaterfallHasPhasePercentiles({ Checkout: tx({ phase_percentiles: percentiles }) })).toBe(true)
  })
})

describe('live waterfall and connection health percentiles', () => {
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

  const button = (label: string) => Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(b => b.textContent === label)!

  it('switches the bars between averages and phase percentiles', async () => {
    await act(async () => root.render(<LiveWaterfallPanel title="Live waterfall" latestMetrics={metrics({ Checkout: tx({ phase_percentiles: percentiles }) })} dataChartId="tx_live_waterfall" />))
    expect(button('Average').getAttribute('aria-pressed')).toBe('true')
    expect(container.textContent).toContain('Bars show phase averages.')
    await act(async () => button('P95').click())
    expect(button('P95').getAttribute('aria-pressed')).toBe('true')
    expect(container.querySelector('[role="treegrid"]')?.getAttribute('aria-label')).toBe('Live waterfall: phase P95s per transaction')
    expect(container.textContent).toContain("Each segment is that phase's own P95")
    expect(container.textContent).toContain('Percentiles do not add up')
    expect(button('Compare with first stable minute').disabled).toBe(true)
    // The drawer lists every phase's percentiles.
    await act(async () => container.querySelector<HTMLElement>('[data-node-id="tx:Checkout"]')!.click())
    const table = Array.from(container.querySelectorAll('table')).find(t => t.querySelector('caption')?.textContent === 'Per-phase percentiles for Checkout')!
    expect(table).toBeTruthy()
    const tlsRow = Array.from(table.querySelectorAll('tr')).find(r => r.querySelector('th')?.textContent === 'TLS')!
    expect(Array.from(tlsRow.querySelectorAll('td')).map(td => td.textContent)).toEqual(['40', '20.0 ms', '50.0 ms', '60.0 ms', '120.0 ms'])
  })

  it('keeps averages and disables the percentiles when no generator reports them', async () => {
    await act(async () => root.render(<LiveWaterfallPanel title="Live waterfall" latestMetrics={metrics({ Checkout: tx({}) })} dataChartId="tx_live_waterfall" />))
    expect(button('P95').disabled).toBe(true)
    expect(button('P50').disabled).toBe(true)
    expect(container.textContent).toContain('or the window had too many transactions and actions to keep them all, so P50, P95 and P99 are unavailable.')
  })

  it('shows TLS and DNS P95 tiles, and explains when they are unknown', async () => {
    const base = { avg_dns_ms: 0.25, avg_connect_ms: 2, avg_tls_ms: 6, phase_observed: { dns: 5, connect: 25, tls: 20 } }
    await act(async () => root.render(<TestExecutionConnectionHealth metrics={metrics({}, { ...base, phase_percentiles: percentiles })} />))
    const tile = (label: string) => Array.from(container.querySelectorAll('.connection-health-tile')).find(t => t.querySelector('dt')?.textContent === label)
    expect(tile('TLS P95 per transaction with a handshake')?.textContent).toContain('60.0 ms')
    expect(tile('TLS P95 per transaction with a handshake')?.textContent).toContain('95% of the 40 transaction samples with a handshake')
    expect(tile('DNS P95 per transaction with a lookup')?.textContent).toContain('5.0 ms')
    await act(async () => root.render(<TestExecutionConnectionHealth metrics={metrics({}, { ...base, phase_percentiles: { version: 1 } })} />))
    expect(tile('TLS P95 per transaction with a handshake')?.textContent).toContain('None')
    await act(async () => root.render(<TestExecutionConnectionHealth metrics={metrics({}, base)} />))
    expect(tile('TLS P95 per transaction with a handshake')?.textContent).toContain('Not reported')
    expect(tile('TLS P95 per transaction with a handshake')?.textContent).toContain('the average is in the tile beside')
    // The average tile still shows the average.
    expect(tile('TLS time per transaction with a handshake')?.textContent).toContain('30.0 ms')
  })
})
