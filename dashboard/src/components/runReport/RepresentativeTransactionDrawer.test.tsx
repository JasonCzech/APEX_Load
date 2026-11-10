// @vitest-environment jsdom

import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { IterationReplayTarget } from '../../types/iterationReplay'
import type { ArchiveTransactionWaterfall, ArchiveWaterfallRow, RunArchiveRepresentativeSample, RunArchiveTimeAnatomy } from '../../types/timeAnatomy'
import fixture from './__fixtures__/timeAnatomyReport.json'
import RepresentativeTransactionDrawer from './RepresentativeTransactionDrawer'

const mocks = vi.hoisted(() => ({ api: { getTestArchiveTransactionWaterfall: vi.fn() } }))
vi.mock('../../hooks/useApi', () => ({ useApi: () => mocks.api }))

const anatomy = fixture.time_anatomy as unknown as RunArchiveTimeAnatomy
const sample = anatomy.transactions[0].representatives!.find(item => item.reason === 'p95') as RunArchiveRepresentativeSample

function row(partial: Partial<ArchiveWaterfallRow>): ArchiveWaterfallRow {
  return {
    event_id: 'tx_00000000000000000000000000000001', ordinal: 1, population: 'action', timestamp: '2026-11-01T10:00:00.000Z',
    offset_ms: 0, duration_ms: 0, action_name: 'GET /', success: true,
    phases: { dns_ms: 0, connect_ms: 0, tls_ms: 0, server_ms: 0, download_ms: 0, unsplit_ms: 0 },
    ...partial,
  }
}

const response: ArchiveTransactionWaterfall = {
  schema: 'apexload.transaction_waterfall.v1', join_rule: 'vuser_lifetime_containment_v1', phase_model: 'sequential_clipped_v1', identity_quality: 'lifetime',
  root: row({ event_id: sample.event_id, ordinal: 4021, population: 'transaction', action_name: 'Checkout', duration_ms: 130 }),
  requests: [
    row({ event_id: 'r1', ordinal: 4018, offset_ms: 0, duration_ms: 20, action_name: 'GET /cart', phases: { dns_ms: 0, connect_ms: 0, tls_ms: 0, server_ms: 15, download_ms: 5, unsplit_ms: 0 } }),
    row({ event_id: 'r2', ordinal: 4019, offset_ms: 20, duration_ms: 90, action_name: 'POST /cart', connection: 'new', phases: { dns_ms: 0, connect_ms: 10, tls_ms: 0, server_ms: 70, download_ms: 10, unsplit_ms: 0 } }),
  ],
  requests_total: 2,
  decomposition: { instances: 1, avg_duration_ms: 130, phases: { dns_ms: 0, connect_ms: 10, tls_ms: 0, server_ms: 85, download_ms: 15, unsplit_ms: 0 }, non_request_ms: 20, overlap_ms: 0, avg_requests_per_instance: 2 },
}

function apiError(status: number) {
  return Object.assign(new Error(`HTTP ${status}`), { status })
}

function Harness({ chosen = sample, onOpenIteration }: { chosen?: RunArchiveRepresentativeSample; onOpenIteration?: (target: IterationReplayTarget) => void }) {
  const [open, setOpen] = useState(false)
  return <>
    <button type="button" id="chip" onClick={() => setOpen(true)}>P95 130 ms</button>
    {open && <RepresentativeTransactionDrawer testId="run-1" transactionName="Checkout" sample={chosen} onClose={() => setOpen(false)} onOpenIteration={onOpenIteration} />}
  </>
}

describe('RepresentativeTransactionDrawer', () => {
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
    vi.clearAllMocks()
  })

  const open = async (chosen?: RunArchiveRepresentativeSample, onOpenIteration?: (target: IterationReplayTarget) => void) => {
    await act(async () => root.render(<Harness chosen={chosen} onOpenIteration={onOpenIteration} />))
    const chip = container.querySelector<HTMLButtonElement>('#chip')
    expect(chip).toBeTruthy()
    if (!chip) return
    chip.focus()
    await act(async () => chip.click())
  }

  it('opens the instance\'s iteration and closes', async () => {
    mocks.api.getTestArchiveTransactionWaterfall.mockResolvedValue(response)
    const onOpenIteration = vi.fn()
    const chosen = { ...sample, group: 'shop', vuser_id: 4, vuser_lifetime: '77', iteration: 9 }
    await open(chosen, onOpenIteration)
    const button = [...container.querySelectorAll<HTMLButtonElement>('button')].find(item => item.textContent === 'Open iteration')
    expect(button).toBeTruthy()
    await act(async () => button!.click())
    expect(onOpenIteration).toHaveBeenCalledWith({ group: 'shop', vuser_id: 4, vuser_lifetime: '77', iteration: 9, ordinal: chosen.ordinal, source: 'Checkout' })
    expect(container.querySelector('[role="dialog"]')).toBeNull()
  })

  it('offers no iteration without an identity or a handler', async () => {
    mocks.api.getTestArchiveTransactionWaterfall.mockResolvedValue(response)
    await open({ ...sample, iteration: undefined }, vi.fn())
    expect([...container.querySelectorAll('button')].some(item => item.textContent === 'Open iteration')).toBe(false)
  })

  it('asks for the event with its ordinal hints and shows a loading status', async () => {
    mocks.api.getTestArchiveTransactionWaterfall.mockReturnValue(new Promise(() => {}))
    await open()
    expect(mocks.api.getTestArchiveTransactionWaterfall).toHaveBeenCalledWith('run-1', sample.event_id, { ordinal: 4021, first_ordinal: 4018 }, expect.objectContaining({ signal: expect.any(AbortSignal) }))
    const dialog = container.querySelector('[role="dialog"]')
    expect(dialog).toBeTruthy()
    if (!dialog) return
    expect(dialog.getAttribute('aria-modal')).toBe('true')
    expect(document.getElementById(dialog.getAttribute('aria-labelledby') || '')?.textContent).toBe('P95 Checkout · 130 ms')
    expect(dialog.querySelector('[role="status"]')?.textContent).toBe('Rebuilding transaction...')
  })

  it('draws the rebuilt transaction from the backend phases', async () => {
    mocks.api.getTestArchiveTransactionWaterfall.mockResolvedValue(response)
    await open()
    const rows = Array.from(container.querySelectorAll('.rr-phase-row'))
    expect(rows.map(item => item.querySelector('.rr-phase-label')?.textContent)).toEqual(['Checkout', 'GET /cart', 'POST /cart', 'Outside requests'])
    expect(container.textContent).toContain('This Checkout: server 65%')
    const post = Array.from(container.querySelectorAll<HTMLButtonElement>('.rr-phase-row button')).find(button => button.textContent?.includes('POST /cart'))
    expect(post).toBeTruthy()
    if (!post) return
    await act(async () => post.click())
    expect(container.querySelector('.rr-anatomy-row-pane')?.textContent).toContain('Connectionnew')
  })

  it('sends the ordinal hints for a transaction open across any number of rows', async () => {
    mocks.api.getTestArchiveTransactionWaterfall.mockReturnValue(new Promise(() => {}))
    await open({ ...sample, ordinal: 2_500_000, first_ordinal: 100 })
    expect(mocks.api.getTestArchiveTransactionWaterfall).toHaveBeenCalledWith('run-1', sample.event_id, { ordinal: 2_500_000, first_ordinal: 100 }, expect.objectContaining({ signal: expect.any(AbortSignal) }))
  })

  it.each([
    ['partial with a decomposition', { partial: true, partial_reasons: ['pending_key_cap'] }],
    ['partial without a decomposition', { partial: true, partial_reasons: ['pending_key_cap'], decomposition: undefined }],
    ['partial and join-may-differ', { partial: true, partial_reasons: ['pending_key_cap'], join_may_differ: true, join_may_differ_reasons: ['pending_global_cap'] }],
  ])('says a %s drill-down lost requests and calls no time outside requests', async (_name, flags) => {
    mocks.api.getTestArchiveTransactionWaterfall.mockResolvedValue({ ...response, root: { ...response.root, duration_ms: 400 }, ...flags })
    await open()
    expect(container.textContent).toContain('Some requests of this transaction were not retained by the report because this VUser had too many requests waiting for their transaction; outside-request time is unknown.')
    expect(container.textContent).not.toContain('pending_key_cap')
    // The partial callout already says the report lost requests; no second caveat.
    expect(container.querySelectorAll('.rr-anatomy-callout')).toHaveLength(1)
    expect(container.querySelector('.rr-anatomy-composition')).toBeNull()
    expect(container.textContent).not.toContain('This Checkout:')
    const rows = Array.from(container.querySelectorAll('.rr-phase-row'))
    expect(rows.map(item => item.querySelector('.rr-phase-label')?.textContent)).toEqual(['Checkout', 'GET /cart', 'POST /cart'])
    expect(rows[0].querySelectorAll('.rr-phase-gap')).toHaveLength(0)
  })

  it('says why a work-budget drill-down has no requests', async () => {
    mocks.api.getTestArchiveTransactionWaterfall.mockResolvedValue({ ...response, requests: [], requests_total: 0, decomposition: undefined, partial: true, partial_reasons: ['work_budget'] })
    await open()
    expect(container.textContent).toContain('The report reached its processing budget before this transaction, so it matched no requests to it and left it out of its averages')
    expect(container.textContent).not.toContain('were not retained')
    expect(container.textContent).not.toContain('work_budget')
  })

  it.each([
    ['run-wide limits', ['pending_global_cap', 'identity_cap'], 'During this run too many requests across the run were waiting for their transactions and too many VUsers were being tracked at once, so the report may have matched fewer requests to this instance than shown here'],
    ['an unreadable report', undefined, 'The report could not be checked against this view'],
  ])('keeps the breakdown but warns when the report join may differ (%s)', async (_name, reasons, copy) => {
    mocks.api.getTestArchiveTransactionWaterfall.mockResolvedValue({ ...response, partial: false, join_may_differ: true, join_may_differ_reasons: reasons })
    await open()
    const callouts = Array.from(container.querySelectorAll('.rr-anatomy-callout')).map(item => item.textContent)
    expect(callouts).toHaveLength(1)
    expect(callouts[0]).toContain(copy)
    expect(callouts[0]).not.toMatch(/pending_global_cap|identity_cap/)
    // The one-VUser replay holds its full request set, so its breakdown and outside time stay.
    expect(container.textContent).toContain('This Checkout: server 65%')
    const rows = Array.from(container.querySelectorAll('.rr-phase-row'))
    expect(rows.map(item => item.querySelector('.rr-phase-label')?.textContent)).toEqual(['Checkout', 'GET /cart', 'POST /cart', 'Outside requests'])
  })

  it('draws the root as its decomposition and keeps tiny gaps off the list', async () => {
    const phases = { dns_ms: 0, connect_ms: 0, tls_ms: 0, server_ms: 0, download_ms: 0, unsplit_ms: 0 }
    mocks.api.getTestArchiveTransactionWaterfall.mockResolvedValue({
      ...response,
      root: { ...response.root, duration_ms: 400 },
      requests: [
        row({ event_id: 'a', ordinal: 1, offset_ms: 0, duration_ms: 100, action_name: 'GET /a', phases: { ...phases, server_ms: 80, download_ms: 20 } }),
        row({ event_id: 'b', ordinal: 2, offset_ms: 100.1, duration_ms: 99.9, action_name: 'GET /b', phases: { ...phases, server_ms: 99.9 } }),
        row({ event_id: 'c', ordinal: 3, offset_ms: 200.2, duration_ms: 99.8, action_name: 'GET /c', phases: { ...phases, server_ms: 99.8 } }),
      ],
      requests_total: 3,
    })
    await open()
    const rows = Array.from(container.querySelectorAll('.rr-phase-row'))
    // The 0.1 ms gaps get no rows; the 100 ms one at the end does.
    expect(rows.map(item => item.querySelector('.rr-phase-label')?.textContent)).toEqual(['Checkout', 'GET /a', 'GET /b', 'GET /c', 'Outside requests'])
    const rootTrack = rows[0].querySelector('.rr-phase-track')
    expect(rootTrack?.querySelector('.rr-phase-parent')).toBeNull()
    expect(rootTrack?.querySelectorAll('.rr-phase-wait').length).toBeGreaterThan(0)
    expect(rootTrack?.querySelectorAll('.rr-phase-receive')).toHaveLength(1)
    expect(rootTrack?.querySelectorAll('.rr-phase-gap')).toHaveLength(3)
    expect(rows[0].querySelector('.sr-only')?.textContent).toContain('Server wait 280 ms, Download 20 ms, outside requests 100 ms')
    expect(container.querySelector('.rr-phase-legend')?.textContent).toContain('Outside requests')
  })

  it.each([
    [400, 'This transaction could not be located in the archive.'],
    [404, 'Event no longer in archive.'],
    [413, 'Archive too large to rebuild this transaction.'],
    [422, 'Archive too large to rebuild this transaction.'],
  ])('explains a %s', async (status, copy) => {
    mocks.api.getTestArchiveTransactionWaterfall.mockRejectedValue(apiError(status))
    await open()
    expect(container.querySelector('[role="alert"]')?.textContent).toBe(copy)
  })

  it.each([429, 503])('says the archive is busy on a %s and retries on request', async status => {
    mocks.api.getTestArchiveTransactionWaterfall.mockRejectedValueOnce(Object.assign(new Error('too many transaction drill-downs in flight; wait for one to finish'), { status }))
    mocks.api.getTestArchiveTransactionWaterfall.mockResolvedValueOnce(response)
    await open()
    expect(container.querySelector('[role="alert"]')?.textContent).toBe('The archive is busy rebuilding other transactions. Try again in a moment.')
    const retry = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Retry')
    expect(retry).toBeTruthy()
    await act(async () => retry?.click())
    // The Retry button unmounts; keyboard focus must stay inside the dialog.
    const dialog = container.querySelector('[role="dialog"]')
    expect(dialog).toBeTruthy()
    expect(dialog?.contains(document.activeElement)).toBe(true)
    expect(mocks.api.getTestArchiveTransactionWaterfall).toHaveBeenCalledTimes(2)
    expect(container.querySelector('[role="alert"]')).toBeNull()
    expect(container.textContent).toContain('GET /cart')
  })

  it('offers no Retry for a missing event', async () => {
    mocks.api.getTestArchiveTransactionWaterfall.mockRejectedValue(apiError(404))
    await open()
    expect(Array.from(container.querySelectorAll('button')).some(button => button.textContent === 'Retry')).toBe(false)
  })

  it('says when the requests were truncated or the identity is legacy', async () => {
    mocks.api.getTestArchiveTransactionWaterfall.mockResolvedValue({ ...response, truncated: true, requests_total: 812, identity_quality: 'legacy' })
    await open()
    expect(container.textContent).toContain('Showing first 2 of 812 requests.')
    expect(container.textContent).toContain('Legacy archive: VUser IDs may be reused, so requests from another VUser may appear.')
  })

  it('closes on Escape and returns focus to the chip', async () => {
    mocks.api.getTestArchiveTransactionWaterfall.mockResolvedValue(response)
    await open()
    expect(container.querySelector('[role="dialog"]')).not.toBeNull()
    await act(async () => { document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })) })
    expect(container.querySelector('[role="dialog"]')).toBeNull()
    expect(document.activeElement?.id).toBe('chip')
  })
})
