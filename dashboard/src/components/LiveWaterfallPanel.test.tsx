// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import type { LiveMetrics, TxMetrics } from '../types'
import LiveWaterfallPanel from './LiveWaterfallPanel'
import { LIVE_WATERFALL_FINISHED_EMPTY_TEXT, LIVE_WATERFALL_HOLD_WINDOWS, LIVE_WATERFALL_MAX_ROWS, LIVE_WATERFALL_RESTORED_TEXT, stableMinuteNote } from './liveWaterfallModel'
import { emptyStableMinute, type StableMinuteState } from './liveWindowRetention'

function tx(partial: Partial<TxMetrics>): TxMetrics {
  return { name: 'Checkout', count: 100, errors: 0, avg_ms: 100, p50_ms: 90, p90_ms: 140, p99_ms: 200, tps: 10, ...partial }
}

const metrics: LiveMetrics = {
  timestamp: '2026-11-10T10:00:00Z', active_vusers: 10, tps: 10, avg_response_ms: 100, error_pct: 0, total_transactions: 10, total_errors: 0,
  p50_ms: 0, p90_ms: 0, p95_ms: 0, p99_ms: 0,
  transactions: {
    Checkout: tx({ avg_ms: 300, avg_connect_ms: 20, avg_tls_ms: 10, avg_server_ms: 200, avg_no_response_ms: 30, phase_observed: { dns: 0, connect: 40, tls: 40 } }),
    Login: tx({ name: 'Login', avg_ms: 50, avg_server_ms: 40, avg_no_response_ms: 0, phase_observed: { dns: 0, connect: 0, tls: 0 } }),
  },
}

const ready: StableMinuteState = {
  phase: 'ready', steadyWindows: 3, startMs: Date.parse('2026-11-10T09:59:00Z'), endMs: Date.parse('2026-11-10T10:00:00Z'), vusers: 10, windows: 60, lastVusers: 10,
  sums: { Checkout: { weight: 10, count: 10, errors: 0, dns: 0, connect: 0, tls: 0, server: 1500, download: 500, noResponse: 0, avgMs: 2000 } },
}

describe('LiveWaterfallPanel', () => {
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

  it('draws one aggregate row per transaction and opens the phase drawer', async () => {
    await act(async () => root.render(<LiveWaterfallPanel title="Live waterfall" latestMetrics={metrics} stableMinute={emptyStableMinute()} dataChartId="tx_live_waterfall" />))
    const rows = Array.from(container.querySelectorAll<HTMLElement>('[data-node-id]'))
    expect(rows.map(r => r.dataset.nodeId)).toEqual(['tx:Checkout', 'tx:Login'])
    expect(container.textContent).toContain('No TTFB (unsplit)')
    expect(container.textContent).toContain('Slowest: Checkout')
    // No critical path for independent rows.
    expect(container.textContent).not.toContain('Critical path')
    const compare = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(b => b.textContent === 'Compare with first stable minute')!
    expect(compare.disabled).toBe(true)
    await act(async () => rows[0].click())
    const drawer = container.querySelector('.wf-drawer')!
    expect(drawer.textContent).toContain('60.0% of 100 transaction samples opened no new connection')
    // Per-transaction setup time sums several handshakes: labelled as such, not as one handshake.
    expect(drawer.textContent).toContain('TLS time per sample with a handshake')
    expect(drawer.textContent).not.toContain('Avg TLS handshake')
    // Phase definitions are visible text, not a hover-only title.
    const definitions = drawer.querySelector('.live-waterfall-definitions')!
    expect(definitions.textContent).toContain('The rest of the average after every other phase')
    expect(definitions.textContent).toContain('Time to first byte minus DNS, connect and TLS')
    expect(drawer.querySelector('th[title]')).toBeNull()
    // Independent aggregate rows: no session scale, no chronological sort.
    const scale = Array.from(container.querySelectorAll('[aria-label="Timeline scale"] button')).map(b => b.textContent)
    expect(scale).toEqual(['Absolute (ms)', 'Stacked (100%)'])
    const sorts = Array.from(container.querySelectorAll('.wf-toolbar select option')).map(o => o.textContent)
    expect(sorts).not.toContain('Chronological')
    expect(sorts).toContain('Slowest first')
    expect(drawer.textContent).toContain('25.0 ms')
    expect(drawer.textContent).toContain('Per-phase percentiles not reported')
  })

  it('ghosts the first stable minute when it is complete', async () => {
    await act(async () => root.render(<LiveWaterfallPanel title="Live waterfall" latestMetrics={metrics} stableMinute={ready} dataChartId="tx_live_waterfall" />))
    expect(container.querySelectorAll('.wf-ghost').length).toBeGreaterThan(0)
    expect(container.textContent).toContain('First stable minute this page received')
    const compare = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(b => b.textContent === 'Compare with first stable minute')!
    expect(compare.getAttribute('aria-pressed')).toBe('true')
    await act(async () => compare.click())
    expect(container.querySelectorAll('.wf-ghost')).toHaveLength(0)
  })

  const render = (latestMetrics?: LiveMetrics) => act(async () => root.render(<LiveWaterfallPanel title="Live waterfall" latestMetrics={latestMetrics} stableMinute={emptyStableMinute()} dataChartId="tx_live_waterfall" />))
  const rowIds = () => Array.from(container.querySelectorAll<HTMLElement>('[data-node-id]')).map(r => r.dataset.nodeId)

  it('lists only the phases a live row can draw and no idle gap', async () => {
    await render(metrics)
    const legend = Array.from(container.querySelectorAll('.wf-legend .wf-legend-chip')).map(el => el.textContent)
    expect(legend).not.toContain('Think / idle')
    expect(legend.some(text => /Queue|Send/.test(text ?? ''))).toBe(false)
    expect(legend).toContain('Download')
  })

  it('names the error share in the Status column', async () => {
    await render({ ...metrics, transactions: { Checkout: tx({ count: 1000, errors: 990 }) } })
    expect(container.querySelector('.wf-status-text')?.textContent).toBe('Failing · 99% errors')
  })

  it('notes when rows past the cap are left out', async () => {
    const transactions: Record<string, TxMetrics> = {}
    for (let i = 0; i < LIVE_WATERFALL_MAX_ROWS + 5; i++) transactions[`T${i}`] = tx({ name: `T${i}`, avg_ms: 10 + i })
    await render({ ...metrics, transactions })
    expect(container.textContent).toContain(`Showing the ${LIVE_WATERFALL_MAX_ROWS} slowest of ${LIVE_WATERFALL_MAX_ROWS + 5} transactions`)
    await render(metrics)
    expect(container.textContent).not.toContain('slowest of')
  })

  it('keeps a low-rate row and its drawer through a window without samples, then closes it for good', async () => {
    const at = (second: number, transactions: LiveMetrics['transactions']): LiveMetrics => ({ ...metrics, timestamp: `2026-11-10T10:00:${String(second).padStart(2, '0')}Z`, transactions })
    await render(at(0, metrics.transactions))
    await act(async () => container.querySelector<HTMLElement>('[data-node-id="tx:Login"]')!.click())
    expect(container.querySelector('.wf-drawer')?.textContent).toContain('Login')
    const checkoutOnly = { Checkout: metrics.transactions!.Checkout }
    await render(at(1, checkoutOnly))
    // Held: still listed, and the open drawer says the figures are older.
    expect(rowIds()).toContain('tx:Login')
    expect(container.querySelector('.wf-drawer')?.textContent).toContain('No samples in the latest window')
    for (let i = 2; i <= LIVE_WATERFALL_HOLD_WINDOWS + 2; i++) await render(at(i, checkoutOnly))
    expect(rowIds()).toEqual(['tx:Checkout'])
    expect(container.querySelector('.wf-drawer')).toBeNull()
    // Coming back does not reopen the drawer by itself.
    await render(at(30, metrics.transactions))
    expect(rowIds()).toContain('tx:Login')
    expect(container.querySelector('.wf-drawer')).toBeNull()
  })

  it('moves focus to the grid when the drawer closes under focus because its row dropped out', async () => {
    const at = (second: number, transactions: LiveMetrics['transactions']): LiveMetrics => ({ ...metrics, timestamp: `2026-11-10T10:00:${String(second).padStart(2, '0')}Z`, transactions })
    await render(at(0, metrics.transactions))
    await act(async () => container.querySelector<HTMLElement>('[data-node-id="tx:Login"]')!.click())
    const close = Array.from(container.querySelectorAll<HTMLButtonElement>('.wf-drawer button')).find(b => b.textContent === 'Close')!
    await act(async () => close.focus())
    expect(document.activeElement).toBe(close)
    const checkoutOnly = { Checkout: metrics.transactions!.Checkout }
    for (let i = 1; i <= LIVE_WATERFALL_HOLD_WINDOWS + 2; i++) await render(at(i, checkoutOnly))
    expect(container.querySelector('.wf-drawer')).toBeNull()
    expect(document.activeElement).not.toBe(document.body)
    expect(document.activeElement).toBe(container.querySelector('[role="treegrid"]'))
  })

  it('keeps focus in the card when the drawer closes because every row dropped out', async () => {
    const at = (second: number, transactions: LiveMetrics['transactions'], actions: boolean): Promise<void> => {
      const out: LiveMetrics = { ...metrics, timestamp: `2026-11-10T10:00:${String(second).padStart(2, '0')}Z`, transactions }
      if (!transactions) delete out.transactions
      return act(async () => root.render(
        <LiveWaterfallPanel
          title="Live waterfall"
          latestMetrics={out}
          stableMinute={emptyStableMinute()}
          dataChartId="tx_live_waterfall"
          headerActions={actions ? <button type="button" className="chart-actions-button">Actions</button> : undefined}
        />,
      ))
    }
    for (const actions of [true, false]) {
      await at(0, metrics.transactions, actions)
      await act(async () => container.querySelector<HTMLElement>('[data-node-id="tx:Login"]')!.click())
      const close = Array.from(container.querySelectorAll<HTMLButtonElement>('.wf-drawer button')).find(b => b.textContent === 'Close')!
      await act(async () => close.focus())
      // Nothing completes any more: the held rows age out and the grid is replaced by the empty message.
      for (let i = 1; i <= LIVE_WATERFALL_HOLD_WINDOWS + 2; i++) await at(i, undefined, actions)
      expect(container.querySelector('[role="treegrid"]')).toBeNull()
      expect(container.querySelector('.wf-drawer')).toBeNull()
      expect(document.activeElement).not.toBe(document.body)
      expect(document.activeElement).toBe(actions
        ? container.querySelector('.chart-actions-button')
        : container.querySelector('[data-chart-id="tx_live_waterfall"]'))
      await act(async () => root.render(<></>))
    }
  })

  it('draws a held row that last failed as neutral, not failing now', async () => {
    const at = (second: number, transactions: LiveMetrics['transactions']): LiveMetrics => ({ ...metrics, timestamp: `2026-11-10T10:00:${String(second).padStart(2, '0')}Z`, transactions })
    await render(at(0, { ...metrics.transactions, Login: tx({ name: 'Login', count: 10, errors: 8, avg_ms: 50 }) }))
    expect(container.querySelector('[data-node-id="tx:Login"]')?.className).toContain('wf-status-failed')
    await render(at(1, { Checkout: metrics.transactions!.Checkout }))
    const held = container.querySelector<HTMLElement>('[data-node-id="tx:Login"]')!
    expect(held.textContent).toContain('No samples')
    expect(held.className).not.toContain('wf-status-failed')
    expect(held.querySelector('.wf-status-text.failed')).toBeNull()
  })

  it('names no start offset in a row tooltip: rows share no timeline', async () => {
    await render(metrics)
    const cell = container.querySelector<HTMLElement>('[data-node-id="tx:Checkout"] .wf-cell-timeline')!
    await act(async () => { cell.dispatchEvent(new MouseEvent('pointermove', { bubbles: true, clientX: 40, clientY: 40 })) })
    const tip = document.querySelector('.wf-tooltip')
    expect(tip?.textContent).toContain('Checkout')
    expect(tip?.textContent).toContain('Aggregate')
    expect(tip?.textContent).not.toContain('starts +')
  })

  it('says no first stable minute was captured on a finished run', async () => {
    await act(async () => root.render(<LiveWaterfallPanel title="Live waterfall" latestMetrics={metrics} stableMinute={emptyStableMinute()} runFinished dataChartId="tx_live_waterfall" />))
    const note = container.querySelector('#tx_live_waterfall-baseline-note')!
    expect(note.textContent).toContain('the run ended before one was received')
    expect(note.textContent).not.toContain('starts once')
    const compare = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(b => b.textContent === 'Compare with first stable minute')!
    expect(compare.getAttribute('aria-describedby')).toBe('tx_live_waterfall-baseline-note')
  })

  it('keeps held rows and the open drawer through a window where nothing completed', async () => {
    const at = (second: number, transactions: LiveMetrics['transactions']): LiveMetrics => {
      const out: LiveMetrics = { ...metrics, timestamp: `2026-11-10T10:00:${String(second).padStart(2, '0')}Z`, transactions }
      // Go omits an empty transactions map (omitempty).
      if (!transactions) delete out.transactions
      return out
    }
    await render(at(0, metrics.transactions))
    await act(async () => container.querySelector<HTMLElement>('[data-node-id="tx:Login"]')!.click())
    await render(at(1, undefined))
    expect(rowIds()).toEqual(['tx:Checkout', 'tx:Login'])
    expect(container.textContent).not.toContain('No transactions completed in the latest window.')
    expect(container.querySelector('.wf-drawer')?.textContent).toContain('No samples in the latest window')
    await render(at(2, metrics.transactions))
    expect(container.querySelector('.wf-drawer')?.textContent).toContain('Login')
    expect(container.querySelector('.wf-drawer')?.textContent).not.toContain('No samples in the latest window')
  })

  it('caveats fallback percentiles, older-generator download and unmeasured setup in the drawer', async () => {
    await render({ ...metrics, transactions: {
      Old: tx({ name: 'Old', avg_ms: 100, avg_ttfb_ms: 40, percentile_quality: 'averaged' }),
      Grpc: tx({ name: 'Grpc', avg_ms: 20, avg_server_ms: 0, avg_no_response_ms: 20, phase_observed: { dns: 0, connect: 0, tls: 0, no_ttfb: 100 } }),
    } })
    await act(async () => container.querySelector<HTMLElement>('[data-node-id="tx:Old"]')!.click())
    let drawer = container.querySelector('.wf-drawer')!.textContent ?? ''
    expect(drawer).toContain('P95 / P99 (averaged)')
    expect(drawer).toContain('average the sources’ own')
    expect(drawer).toContain('samples with no first byte (timeouts)')
    await act(async () => container.querySelector<HTMLElement>('[data-node-id="tx:Grpc"]')!.click())
    drawer = container.querySelector('.wf-drawer')!.textContent ?? ''
    expect(drawer).toContain('Not measured for this protocol')
    expect(drawer).not.toContain('100.0% of 100 transaction samples opened no new connection')
  })

  it('on a finished run whose live tail was compacted, says the breakdown is not kept, not that nothing completed', async () => {
    // compactTerminalLiveMetricsTail drops `transactions` on every terminal run.
    const compacted: LiveMetrics = { ...metrics }
    delete compacted.transactions
    await act(async () => root.render(<LiveWaterfallPanel title="Live waterfall" latestMetrics={compacted} stableMinute={emptyStableMinute()} runFinished dataChartId="tx_live_waterfall" />))
    expect(container.textContent).not.toContain('No transactions completed in the latest window.')
    expect(container.textContent).toContain(LIVE_WATERFALL_FINISHED_EMPTY_TEXT)
    expect(container.textContent).toContain('time anatomy waterfall')
    expect(container.textContent).not.toContain('One row per transaction from the latest window')
    // A running run with an empty window still says so.
    await act(async () => root.render(<LiveWaterfallPanel title="Live waterfall" latestMetrics={compacted} stableMinute={emptyStableMinute()} dataChartId="tx_live_waterfall" />))
    expect(container.textContent).toContain('No transactions completed in the latest window.')
  })

  it('keeps the last received window on a run that finished while watched', async () => {
    await act(async () => root.render(<LiveWaterfallPanel title="Live waterfall" latestMetrics={metrics} stableMinute={emptyStableMinute()} runFinished dataChartId="tx_live_waterfall" />))
    expect(rowIds()).toEqual(['tx:Checkout', 'tx:Login'])
    expect(container.textContent).toContain('last window this page received')
    expect(container.textContent).not.toContain(LIVE_WATERFALL_FINISHED_EMPTY_TEXT)
  })

  it('draws a window restored from history as totals with phases and percentiles not reported', async () => {
    // intervalTransactionRecordsToLiveMetrics: name, tps, count, errors, avg_ms only; percentiles arrive as 0.
    const restoredTx = (name: string, avg: number): TxMetrics => ({ name, tps: 10, count: 100, errors: 0, avg_ms: avg, p50_ms: 0, p90_ms: 0, p99_ms: 0 })
    await render({ ...metrics, transactions: { Checkout: restoredTx('Checkout', 300), Login: restoredTx('Login', 50) } })
    expect(container.textContent).toContain(LIVE_WATERFALL_RESTORED_TEXT)
    expect(container.textContent).not.toContain('predate phase observations')
    const row = container.querySelector<HTMLElement>('[data-node-id="tx:Checkout"]')!
    expect(row.querySelector('.wf-seg')).toBeNull()
    expect(row.querySelector('.wf-bar-derived')).not.toBeNull()
    expect(row.textContent).toContain('restored from history')
    await act(async () => row.click())
    const drawer = container.querySelector('.wf-drawer')!.textContent ?? ''
    expect(drawer).toContain('Phases not reported for this window')
    expect(drawer).toContain('Percentiles not reported for this window')
    expect(drawer).not.toContain('No sample reported a first byte')
    expect(drawer).not.toContain('predates phase observations')
    expect(drawer).not.toContain('0.0 ms / 0.0 ms')
    expect(drawer).toContain('300.0 ms')
  })

  it('folds the error share and the held marker into the name cell for narrow widths', async () => {
    const at = (second: number, transactions: LiveMetrics['transactions']): LiveMetrics => ({ ...metrics, timestamp: `2026-11-10T10:00:${String(second).padStart(2, '0')}Z`, transactions })
    await render(at(0, { Checkout: tx({ count: 100, errors: 40 }), Login: tx({ name: 'Login', avg_ms: 50 }) }))
    const label = (id: string) => container.querySelector(`[data-node-id="${id}"] .wf-cell-name .wf-code-label`)?.textContent
    expect(label('tx:Checkout')).toBe('40% errors')
    // A healthy row repeats nothing.
    expect(label('tx:Login')).toBeUndefined()
    await render(at(1, { Checkout: tx({ count: 100, errors: 40 }) }))
    expect(label('tx:Login')).toBe('No samples')
  })

  it('waits for metrics', async () => {
    await act(async () => root.render(<LiveWaterfallPanel title="Live waterfall" />))
    expect(container.textContent).toContain('Waiting for live metrics.')
    expect(stableMinuteNote(undefined)).toContain('not tracked')
  })
})
