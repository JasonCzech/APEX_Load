// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { TraceExemplar } from '../types'
import type { ExemplarWindow } from './liveWindowRetention'
import LiveExemplarDrawer from './LiveExemplarDrawer'

const mocks = vi.hoisted(() => ({ api: { getLiveTraceLinks: vi.fn() }, copy: vi.fn() }))
vi.mock('../hooks/useApi', () => ({ useOptionalApi: () => mocks.api, useApi: () => mocks.api }))
vi.mock('../utils/clipboardUtils', () => ({ copyTextToClipboard: mocks.copy }))

const T0 = Date.parse('2026-11-10T10:00:00Z')
const slow: TraceExemplar = { trace_id: '0af7651916cd43dd8448eb211c80319c', duration_ms: 812, success: true, status_code: 200, reason: 'slowest_success', timestamp: '2026-11-10T10:00:00.100Z' }
const failed: TraceExemplar = { trace_id: '4bf92f3577b34da6a3ce929d0e0e4736', duration_ms: 30, success: false, status_code: 503, reason: 'first_error', timestamp: '2026-11-10T10:00:00.200Z' }

const ring: ExemplarWindow[] = [
  { tsMs: T0, timestamp: '2026-11-10T10:00:00Z', transactions: [{ name: 'Checkout', exemplars: [slow, failed] }, { name: 'Login', exemplars: [{ ...slow, trace_id: '11111111111111111111111111111111' }] }] },
  { tsMs: T0 + 10_000, timestamp: '2026-11-10T10:00:10Z', transactions: [{ name: 'Login', exemplars: [{ ...slow, trace_id: '22222222222222222222222222222222' }] }] },
]

describe('LiveExemplarDrawer', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    mocks.api.getLiveTraceLinks.mockReset()
    mocks.copy.mockReset()
  })

  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
  })

  async function render(props: Partial<Parameters<typeof LiveExemplarDrawer>[0]> = {}) {
    await act(async () => {
      root.render(<LiveExemplarDrawer testId="run-1" ring={ring} atMs={T0 + 1200} transactionNames={['Checkout']} onClose={() => {}} {...props} />)
    })
    await act(async () => { await Promise.resolve() })
  }

  it('lists the nearest window’s exemplars for the chart’s transactions with their APM links', async () => {
    mocks.api.getLiveTraceLinks.mockResolvedValue({
      configured: true,
      traces: [
        { trace_id: slow.trace_id, links: [{ provider: 'dynatrace', connection_id: 'dt', url: 'https://abc.live.dynatrace.com/ui/trace?traceId=' + slow.trace_id }] },
        { trace_id: failed.trace_id, links: [{ provider: 'elasticsearch', connection_id: 'elk', url: 'https://kibana.example.test/app/discover' }] },
      ],
    })
    await render()
    const body = document.body.textContent || ''
    expect(body).toContain('Checkout')
    expect(body).not.toContain('11111111111111111111111111111111')
    expect(body).toContain('Slowest success')
    expect(body).toContain('First error')
    expect(body).toContain('HTTP 503')
    const [id, request] = mocks.api.getLiveTraceLinks.mock.calls[0]
    expect(id).toBe('run-1')
    expect(request.trace_ids).toEqual([slow.trace_id, failed.trace_id])
    expect(Date.parse(request.to) - Date.parse(request.from)).toBe(10 * 60_000)
    const links = Array.from(document.querySelectorAll<HTMLAnchorElement>('.live-exemplar-links a'))
    expect(links.map(a => a.textContent)).toEqual(['Open in Dynatrace', 'Open in Kibana (ELK)'])
    expect(links[0].rel).toContain('noopener')
    expect(document.querySelector(`button[aria-label="Copy trace id ${slow.trace_id}"]`)).not.toBeNull()
  })

  it('says when no APM connection is configured and steps between windows', async () => {
    mocks.api.getLiveTraceLinks.mockResolvedValue({ configured: false, traces: [{ trace_id: slow.trace_id, links: [] }, { trace_id: failed.trace_id, links: [] }] })
    await render({ transactionNames: [] })
    expect(document.body.textContent).toContain('No APM connection with a trace link is configured')
    expect(document.querySelectorAll('.live-exemplar-links a')).toHaveLength(0)
    const later = Array.from(document.querySelectorAll<HTMLButtonElement>('button')).find(b => b.textContent === 'Later window')!
    expect(later.disabled).toBe(false)
    await act(async () => later.click())
    await act(async () => { await Promise.resolve() })
    expect(document.body.textContent).toContain('22222222222222222222222222222222')
    expect(mocks.api.getLiveTraceLinks).toHaveBeenLastCalledWith('run-1', expect.objectContaining({ trace_ids: ['22222222222222222222222222222222'] }), expect.anything())
  })

  it('explains an empty ring', async () => {
    await render({ ring: [] })
    expect(document.body.textContent).toContain('Exemplars are recorded only for requests that send trace context')
    expect(mocks.api.getLiveTraceLinks).not.toHaveBeenCalled()
  })

  it('explains a click with no retained window within the tolerance', async () => {
    // A fresh mount: 30 s from both windows, beyond the 5 s tolerance.
    await render({ atMs: T0 + 30_000, firstWindowMs: T0 })
    expect(document.body.textContent).toContain('No retained window within 5 s')
    expect(document.body.textContent).toContain('No request in this window carried trace context.')
    expect(mocks.api.getLiveTraceLinks).not.toHaveBeenCalled()
  })

  it('opens a window within the tolerance even when the chart’s transactions have none in it', async () => {
    mocks.api.getLiveTraceLinks.mockResolvedValue({ configured: true, traces: [] })
    // 4.5 s from the second window, which has only Login.
    await render({ atMs: T0 + 5_500 })
    expect(document.body.textContent).toContain('The transactions shown on this chart have no exemplars in this window.')
  })

  it('says a transaction missing from a truncated window was dropped by this page, not that it had none', async () => {
    mocks.api.getLiveTraceLinks.mockResolvedValue({ configured: true, traces: [] })
    const truncated: ExemplarWindow[] = [{ ...ring[1], truncated: { seen: 300, kept: 200 } }]
    await render({ ring: truncated, atMs: T0 + 10_000, transactionNames: ['zz_checkout'] })
    const body = document.body.textContent || ''
    expect(body).not.toContain('have no exemplars in this window')
    expect(body).toContain('keeps exemplars for only the first 200 of the 300 traced transactions in this window')
    // With rows to show, the truncation is still noted.
    await render({ ring: truncated, atMs: T0 + 10_000, transactionNames: [] })
    expect(document.body.textContent).toContain('a transaction missing here may still have had some')
  })

  it('says a time older than the retained span is outside retention', async () => {
    await render({ atMs: T0 - 20 * 60_000, firstWindowMs: T0 - 30 * 60_000 })
    expect(document.body.textContent).toContain('older than the exemplars this page keeps')
  })

  it('reports failed and unavailable APM links but still lists trace ids', async () => {
    mocks.api.getLiveTraceLinks.mockRejectedValue(new Error('boom'))
    await render()
    expect(document.querySelector('[role="alert"]')?.textContent).toBe('APM links could not be loaded.')
    expect(document.body.textContent).toContain(slow.trace_id)
    await act(async () => root.unmount())
    root = createRoot(container)
    mocks.api.getLiveTraceLinks.mockResolvedValue({ configured: false, links_unavailable: true, traces: [{ trace_id: slow.trace_id, links: [] }] })
    await render()
    expect(document.body.textContent).toContain('APM link settings could not be read; trace ids are still listed.')
    expect(document.body.textContent).not.toContain('No APM connection with a trace link is configured')
  })

  const bigWindow = (count: number): { ids: string[]; ring: ExemplarWindow[] } => {
    const ids = Array.from({ length: count }, (_, i) => (i + 1).toString(16).padStart(32, '0'))
    return {
      ids,
      ring: [{
        tsMs: T0, timestamp: '2026-11-10T10:00:00Z',
        transactions: Array.from({ length: Math.ceil(count / 3) }, (_, t) => ({ name: `Tx${t}`, exemplars: ids.slice(t * 3, t * 3 + 3).map(trace_id => ({ ...slow, trace_id })) })),
      }],
    }
  }
  const linkEcho = async (_id: string, request: { trace_ids: string[] }) => ({
    configured: true,
    traces: request.trace_ids.map(trace_id => ({ trace_id, links: [{ provider: 'dynatrace', connection_id: 'dt', url: `https://dt.example.test/${trace_id}` }] })),
  })

  it('requests a whole window’s trace ids (200 transactions x 3 exemplars) in one request', async () => {
    const { ring: big } = bigWindow(600)
    mocks.api.getLiveTraceLinks.mockImplementation(linkEcho)
    await render({ ring: big, atMs: T0, transactionNames: [] })
    expect(mocks.api.getLiveTraceLinks.mock.calls.map(([, request]) => request.trace_ids.length)).toEqual([600])
    expect(document.querySelectorAll('.live-exemplar-links a')).toHaveLength(600)
  })

  it('keeps the links of the batches that loaded when one batch fails', async () => {
    const { ids, ring: big } = bigWindow(603)
    mocks.api.getLiveTraceLinks.mockImplementation(async (id: string, request: { trace_ids: string[] }) => {
      if (request.trace_ids.length < 600) throw new Error('boom')
      return linkEcho(id, request)
    })
    await render({ ring: big, atMs: T0, transactionNames: [] })
    expect(mocks.api.getLiveTraceLinks).toHaveBeenCalledTimes(2)
    expect(document.querySelector('[role="alert"]')).toBeNull()
    expect(document.body.textContent).toContain('Some APM links could not be loaded')
    expect(document.querySelectorAll('.live-exemplar-links a')).toHaveLength(600)
    expect(document.body.textContent).toContain(ids[602])
  })

  it('says a pinned window aged out of the ring and lets the user move on', async () => {
    mocks.api.getLiveTraceLinks.mockResolvedValue({ configured: true, traces: [] })
    // Open the oldest window, then let the ring prune it.
    await render({ atMs: T0, firstWindowMs: T0, transactionNames: [] })
    expect(document.body.textContent).toContain(slow.trace_id)
    const pruned = [ring[1], { tsMs: T0 + 15 * 60_000 + 5_000, timestamp: '2026-11-10T10:15:05Z', transactions: [{ name: 'Checkout', exemplars: [slow] }] }]
    await render({ ring: pruned, atMs: T0, firstWindowMs: T0, transactionNames: [] })
    const body = document.body.textContent || ''
    expect(body).toContain('This window is now older than the exemplars this page keeps')
    expect(body).not.toContain('No request in this window carried trace context.')
    expect(body).toContain('is no longer kept')
    const button = (label: string) => Array.from(document.querySelectorAll<HTMLButtonElement>('button')).find(b => b.textContent === label)!
    expect(button('Earlier window').getAttribute('aria-disabled')).toBe('true')
    expect(button('Later window').getAttribute('aria-disabled')).toBe('false')
    await act(async () => button('Later window').click())
    await act(async () => { await Promise.resolve() })
    expect(document.body.textContent).toContain('22222222222222222222222222222222')
  })

  it('keeps focus on a window button that runs out of windows', async () => {
    mocks.api.getLiveTraceLinks.mockResolvedValue({ configured: true, traces: [] })
    await render({ transactionNames: [] })
    const button = (label: string) => Array.from(document.querySelectorAll<HTMLButtonElement>('button')).find(b => b.textContent === label)!
    // Let the dialog's deferred initial focus (on Close) run first.
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 0)) })
    const later = button('Later window')
    later.focus()
    await act(async () => later.click())
    await act(async () => { await Promise.resolve() })
    // The newest window is open: the button reads disabled but keeps focus inside the dialog.
    expect(later.getAttribute('aria-disabled')).toBe('true')
    expect(later.disabled).toBe(false)
    expect(document.activeElement).toBe(later)
    await act(async () => later.click())
    expect(document.body.textContent).toContain('22222222222222222222222222222222')
    expect(document.activeElement).toBe(later)
  })

  it('announces a repeat copy of the same trace id again', async () => {
    mocks.api.getLiveTraceLinks.mockResolvedValue({ configured: true, traces: [] })
    mocks.copy.mockResolvedValue(true)
    await render()
    const button = () => document.querySelector<HTMLButtonElement>(`.live-exemplar-trace button`)!
    const status = () => Array.from(document.querySelectorAll('[role="status"]')).map(el => el.textContent).find(t => t?.startsWith('Copied trace id'))
    await act(async () => button().click())
    const first = status()
    await act(async () => button().click())
    expect(status()).not.toBe(first)
    expect(status()).toContain(slow.trace_id)
  })

  it('announces a copied trace id and a failed copy', async () => {
    mocks.api.getLiveTraceLinks.mockResolvedValue({ configured: true, traces: [] })
    mocks.copy.mockResolvedValueOnce(true).mockResolvedValueOnce(false)
    await render()
    const button = () => document.querySelector<HTMLButtonElement>(`.live-exemplar-trace button`)!
    await act(async () => button().click())
    expect(button().getAttribute('aria-label')).toBe(`Copied trace id ${slow.trace_id}`)
    expect(Array.from(document.querySelectorAll('[role="status"]')).some(el => el.textContent === `Copied trace id ${slow.trace_id}`)).toBe(true)
    await act(async () => button().click())
    expect(button().textContent).toBe('Copy failed')
    expect(document.body.textContent).toContain('Copy failed: select the id and copy it by hand.')
    expect(Array.from(document.querySelectorAll('[role="status"]')).some(el => el.textContent === `Could not copy trace id ${slow.trace_id}`)).toBe(true)
  })

  it('returns focus to the named control when the opener is gone', async () => {
    const trigger = document.createElement('button')
    trigger.className = 'chart-actions-button'
    const card = document.createElement('div')
    card.dataset.chartId = 'latency'
    card.appendChild(trigger)
    document.body.appendChild(card)
    mocks.api.getLiveTraceLinks.mockResolvedValue({ configured: true, traces: [] })
    await render({ returnFocusSelector: '[data-chart-id="latency"] .chart-actions-button' })
    await act(async () => root.render(<></>))
    expect(document.activeElement).toBe(trigger)
    card.remove()
  })

  it('opens the window with the slowest traced request inside a clicked time bucket, not the bucket\'s first window', async () => {
    mocks.api.getLiveTraceLinks.mockResolvedValue({ configured: true, traces: [] })
    // A 10 s bucket: its point keeps T0 (the first window), but the slow request ran at T0 + 7 s.
    const spike: TraceExemplar = { ...slow, trace_id: '77777777777777777777777777777777', duration_ms: 4200, timestamp: '2026-11-10T10:00:07.300Z' }
    const bucketed: ExemplarWindow[] = [
      { tsMs: T0, timestamp: '2026-11-10T10:00:00Z', transactions: [{ name: 'Checkout', exemplars: [{ ...slow, duration_ms: 90 }] }] },
      { tsMs: T0 + 7000, timestamp: '2026-11-10T10:00:07Z', transactions: [{ name: 'Checkout', exemplars: [spike] }] },
      // Next bucket: never opened from this click, however slow.
      { tsMs: T0 + 10_000, timestamp: '2026-11-10T10:00:10Z', transactions: [{ name: 'Checkout', exemplars: [{ ...spike, trace_id: '88888888888888888888888888888888', duration_ms: 9000 }] }] },
    ]
    await render({ ring: bucketed, atMs: T0, range: { startMs: T0, endMs: T0 + 10_000 } })
    const body = document.body.textContent || ''
    expect(body).toContain('77777777777777777777777777777777')
    expect(body).not.toContain('88888888888888888888888888888888')
    expect(body).toContain('The clicked point averages every window from')
    expect(body).toContain('2 of them kept exemplars')
  })

  it('opens nothing outside a clicked bucket, even within the single-window tolerance', async () => {
    mocks.api.getLiveTraceLinks.mockResolvedValue({ configured: true, traces: [] })
    const later: ExemplarWindow[] = [{ tsMs: T0 + 10_000, timestamp: '2026-11-10T10:00:10Z', transactions: [{ name: 'Checkout', exemplars: [slow] }] }]
    // The window lies 1 s past the bucket: the old 5 s tolerance would have opened it.
    await render({ ring: later, atMs: T0 + 9000, range: { startMs: T0 + 5000, endMs: T0 + 9500 } })
    const body = document.body.textContent || ''
    expect(body).not.toContain(slow.trace_id)
    expect(body).toMatch(/No request from .+ to .+ carried trace context\./)
  })

  it('labels the sample exemplar without promising a fleet-wide random draw', async () => {
    mocks.api.getLiveTraceLinks.mockResolvedValue({ configured: true, traces: [] })
    const sampled: ExemplarWindow[] = [{ tsMs: T0, timestamp: '2026-11-10T10:00:00Z', transactions: [{ name: 'Checkout', exemplars: [{ ...slow, reason: 'sample' }] }] }]
    await render({ ring: sampled, atMs: T0 })
    const body = document.body.textContent || ''
    expect(body).not.toContain('Random sample')
    expect(Array.from(document.querySelectorAll('.live-exemplar-table td')).map(td => td.textContent)).toContain('Sample')
    expect(body).toContain('in a distributed run the fleet keeps one generator')
  })
})
