// @vitest-environment jsdom

import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { ArchiveIterationWaterfall, IterationReplayTarget, RunArchiveIterationIndex } from '../../types/iterationReplay'
import type { ArchiveWaterfallRow } from '../../types/timeAnatomy'
import IterationReplay from './IterationReplay'

const mocks = vi.hoisted(() => ({ api: { getTestArchiveIterationWaterfall: vi.fn() } }))
vi.mock('../../hooks/useApi', () => ({ useApi: () => mocks.api }))

const START = Date.parse('2026-11-01T10:00:00.000Z')

function row(partial: Partial<ArchiveWaterfallRow> & { ordinal: number; offset_ms: number }): ArchiveWaterfallRow {
  return {
    event_id: `tx_${String(partial.ordinal).padStart(32, '0')}`, population: 'action',
    timestamp: new Date(START + partial.offset_ms).toISOString(), duration_ms: 0, action_name: 'GET /', success: true,
    phases: { dns_ms: 0, connect_ms: 0, tls_ms: 0, server_ms: 0, download_ms: 0, unsplit_ms: 0 },
    ...partial,
  }
}

const index: RunArchiveIterationIndex = {
  schema: 'apexload.iteration_index.v1', iterations_total: 40, identities: 4,
  identity_quality: { lifetime: 40, legacy: 0, none_rows: 0 },
  span: { samples: 40, p50_ms: 400, p95_ms: 900, max_ms: 1200 },
  truncated: false,
  representatives: [
    { reason: 'p50', target_ms: 400, identity_quality: 'lifetime', group: 'shop', vuser_id: 2, vuser_lifetime: '9007199254740993', iteration: 7, first_ordinal: 100, last_ordinal: 140, span_ms: 401, transaction_rows: 2, request_rows: 3, failed_rows: 0 },
    { reason: 'slowest', identity_quality: 'lifetime', group: 'shop', vuser_id: 3, vuser_lifetime: '9007199254740995', iteration: 2, first_ordinal: 10, last_ordinal: 30, span_ms: 1200, transaction_rows: 2, request_rows: 3, failed_rows: 1 },
  ],
}

const response: ArchiveIterationWaterfall = {
  schema: 'apexload.iteration_waterfall.v1', identity_quality: 'lifetime', group: 'shop', vuser_id: 3, vuser_lifetime: '9007199254740995',
  iteration: 2, first_ordinal: 10, last_ordinal: 30, started_at: new Date(START).toISOString(), span_ms: 400,
  rows: [
    row({ ordinal: 10, offset_ms: 0, duration_ms: 40, action_name: 'GET /login', source_transaction: 'Login', phases: { dns_ms: 5, connect_ms: 10, tls_ms: 0, server_ms: 20, download_ms: 5, unsplit_ms: 0 } }),
    row({ ordinal: 12, offset_ms: 0, duration_ms: 100, elapsed_ms: 150, population: 'transaction', action_name: 'Login' }),
    row({ ordinal: 20, offset_ms: 300, duration_ms: 80, action_name: 'GET /cart', success: false, status_code: 503, error_class: 'http_5xx', error_message: 'upstream unavailable', trace_id: 'abc123', source_transaction: 'Browse' }),
    row({ ordinal: 30, offset_ms: 300, duration_ms: 100, population: 'transaction', action_name: 'Browse', success: false }),
  ],
  rows_total: 4, think_ms_inside_transactions: 50, gaps_ms: 150,
}

function apiError(status: number) {
  return Object.assign(new Error(`HTTP ${status}`), { status })
}

function Harness({ withIndex = true, initial = null, indexOverride }: { withIndex?: boolean; initial?: IterationReplayTarget | null; indexOverride?: RunArchiveIterationIndex }) {
  const [target, setTarget] = useState<IterationReplayTarget | null>(initial)
  return <IterationReplay testId="run-1" index={withIndex ? indexOverride ?? index : undefined} target={target} onTargetChange={setTarget} />
}

describe('IterationReplay', () => {
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

  const render = async (props: Parameters<typeof Harness>[0] = {}) => {
    await act(async () => root.render(<Harness {...props} />))
  }
  const button = (text: string) => [...container.querySelectorAll<HTMLButtonElement>('button')].find(item => item.textContent?.startsWith(text))

  it('shows an empty state without an iteration index', async () => {
    await render({ withIndex: false })
    expect(container.querySelector('h2')?.textContent).toBe('Iteration replay')
    expect(container.textContent).toContain('No iteration index in this report')
    expect(container.querySelector('form')).toBeNull()
    expect(mocks.api.getTestArchiveIterationWaterfall).not.toHaveBeenCalled()
  })

  it('says the span distribution leaves out iterations cut short or split', async () => {
    await render()
    expect(container.textContent).not.toContain('whole iterations')
    await render({ indexOverride: { ...index, span: { ...index.span, samples: 30 }, partial_iterations: 10 } })
    expect(container.querySelector('.rr-anatomy-duration')?.textContent).toContain("span of 30 whole iterations (10 cut short by the test's stop or split are left out)")
  })

  it('counts VUser identities, not VUsers, and says an evicted one can count twice', async () => {
    await render({ indexOverride: { ...index, identities_evicted: 2 } })
    expect(container.querySelector('.rr-anatomy-duration')?.textContent).toContain('40 iterations from 4 VUser lifetimes (an evicted one that wrote again counts twice) ·')
    await render({ indexOverride: { ...index, identity_quality: { lifetime: 0, legacy: 40, none_rows: 0 } } })
    expect(container.querySelector('.rr-anatomy-duration')?.textContent).toContain('40 iterations from 4 VUser identities ·')
    expect(container.querySelector('.rr-anatomy-duration')?.textContent).not.toContain('VUsers')
  })

  it('warns that late rows two or more iterations back are outside the spans, and marks approximate think time', async () => {
    mocks.api.getTestArchiveIterationWaterfall.mockResolvedValue({ ...response, think_ms_approximate: true })
    await render()
    expect(container.textContent).not.toContain('archived after')
    await render({ indexOverride: { ...index, late_rows: 3 } })
    expect(container.textContent).toContain('3 rows were archived after their VUser had moved two or more iterations on')
    await act(async () => button('Slowest')?.click())
    expect(container.textContent).toContain('about 50 ms think time inside transactions')
  })

  it('opens a representative chip as a waterfall with the index hints', async () => {
    mocks.api.getTestArchiveIterationWaterfall.mockResolvedValue(response)
    await render()
    expect(container.textContent).toContain('40 iterations from 4 VUser lifetimes ·')
    const chip = button('Slowest')
    expect(chip?.textContent).toBe('Slowest · 1,200 ms · VUser 3 #2')
    expect(chip?.getAttribute('aria-pressed')).toBe('false')
    await act(async () => chip?.click())
    expect(mocks.api.getTestArchiveIterationWaterfall).toHaveBeenCalledWith('run-1', expect.objectContaining({
      group: 'shop', vuser_id: 3, vuser_lifetime: '9007199254740995', iteration: 2, first_ordinal: 10, last_ordinal: 30,
    }), expect.objectContaining({ signal: expect.any(AbortSignal) }))
    expect(button('Slowest')?.getAttribute('aria-pressed')).toBe('true')
    const grid = container.querySelector('[role="treegrid"]')
    expect(grid?.getAttribute('aria-label')).toBe('Iteration 2 of VUser 3')
    const names = [...container.querySelectorAll('.wf-row[data-node-id] .wf-name-label')].map(item => item.textContent)
    expect(names).toEqual(['Iteration 2', 'Login', 'GET /login', 'Think time or pacing (not recorded)', 'Browse', 'GET /cart'])
    expect(container.textContent).toContain('50 ms think time inside transactions')
    expect(container.textContent).toContain('150 ms not covered by any row')
    // The form follows the chip.
    const inputs = container.querySelectorAll<HTMLInputElement>('form input')
    expect([...inputs].map(input => input.value)).toEqual(['shop', '3', '2'])

    await act(async () => container.querySelector<HTMLElement>('.wf-row[data-node-id="row-20"]')?.click())
    const drawer = container.querySelector('.wf-drawer-inner')
    expect(drawer?.textContent).toContain('GET /cart')
    expect(drawer?.textContent).toContain('HTTP 503')
    expect(drawer?.textContent).toContain('upstream unavailable')
    expect(drawer?.textContent).toContain('abc123')
    expect(drawer?.querySelector('table')).toBeTruthy()
  })

  it('labels a slowest chip cut short or split, since its span can exceed the max', async () => {
    const [p50, slowest] = index.representatives
    await render({ indexOverride: { ...index, partial_iterations: 2, representatives: [p50, { ...slowest, span_ms: 45000, interrupted: true }, { ...slowest, first_ordinal: 50, iteration: 3, split: true }] } })
    const chips = [...container.querySelectorAll<HTMLButtonElement>('.rr-anatomy-chip')]
    expect(chips.map(chip => chip.textContent)).toEqual([
      'Median · 401 ms · VUser 2 #7', 'Slowest (cut short) · 45,000 ms · VUser 3 #2', 'Slowest (split) · 1,200 ms · VUser 3 #3',
    ])
    expect(chips[1].getAttribute('aria-label')).toMatch(/^Slowest iteration \(cut short\): /)
  })

  it('announces a finished replay in the live region, partial and legacy included', async () => {
    let resolve: (value: ArchiveIterationWaterfall) => void = () => {}
    mocks.api.getTestArchiveIterationWaterfall.mockImplementation(() => new Promise(done => { resolve = done }))
    await render()
    const live = () => container.querySelector('[aria-live="polite"]')
    await act(async () => button('Slowest')?.click())
    expect(live()?.textContent).toBe('Rebuilding iteration...')
    await act(async () => resolve({ ...response, partial: true, partial_reasons: ['scan_budget'], identity_quality: 'legacy' }))
    expect(live()?.textContent).toBe("Iteration 2 of VUser 3 rebuilt: 4 rows, 400 ms. Rows may be missing. Legacy archive: a reused VUser ID can mix in another VUser's rows.")
  })

  it('replays a manually entered VUser and iteration', async () => {
    mocks.api.getTestArchiveIterationWaterfall.mockResolvedValue(response)
    await render()
    const [group, vuser, iteration] = [...container.querySelectorAll<HTMLInputElement>('form input')]
    const setValue = (input: HTMLInputElement, value: string) => {
      const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
      setter?.call(input, value)
      input.dispatchEvent(new Event('input', { bubbles: true }))
    }
    expect(group.value).toBe('shop')
    const submit = container.querySelector<HTMLButtonElement>('form button[type="submit"]')
    expect(submit?.disabled).toBe(true)
    await act(async () => { setValue(vuser, '5'); setValue(iteration, '9') })
    expect(submit?.disabled).toBe(false)
    await act(async () => submit?.click())
    expect(mocks.api.getTestArchiveIterationWaterfall).toHaveBeenCalledWith('run-1', expect.objectContaining({ group: 'shop', vuser_id: 5, iteration: 9 }), expect.anything())
    const params = mocks.api.getTestArchiveIterationWaterfall.mock.calls[0][1]
    expect(params.first_ordinal).toBeUndefined()
    expect(params.vuser_lifetime).toBeUndefined()
  })

  it('resubmits an unchanged prefilled form with the target\'s lifetime and hints, and explains VUser 0 by ID', async () => {
    mocks.api.getTestArchiveIterationWaterfall.mockResolvedValue(response)
    await render()
    await act(async () => button('Slowest')?.click())
    const submit = container.querySelector<HTMLButtonElement>('form button[type="submit"]')
    expect(submit?.disabled).toBe(false)
    await act(async () => submit?.click())
    expect(mocks.api.getTestArchiveIterationWaterfall).toHaveBeenCalledTimes(2)
    expect(mocks.api.getTestArchiveIterationWaterfall.mock.calls[1][1]).toMatchObject({ vuser_lifetime: '9007199254740995', first_ordinal: 10, last_ordinal: 30 })
    expect(button('Slowest')?.getAttribute('aria-pressed')).toBe('true')

    const vuser = container.querySelectorAll<HTMLInputElement>('form input')[1]
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(vuser, '0')
      vuser.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(submit?.disabled).toBe(true)
    expect(container.textContent).toContain('VUser 0 can be replayed only from an iteration link or chip')
  })

  it('shows VUser 0 of a target in the form and replays it with its lifetime', async () => {
    mocks.api.getTestArchiveIterationWaterfall.mockResolvedValue({ ...response, vuser_id: 0 })
    await render({ initial: { group: 'shop', vuser_id: 0, vuser_lifetime: '42', iteration: 5, source: 'Business key' } })
    const inputs = [...container.querySelectorAll<HTMLInputElement>('form input')]
    expect(inputs.map(input => input.value)).toEqual(['shop', '0', '5'])
    const submit = container.querySelector<HTMLButtonElement>('form button[type="submit"]')
    expect(submit?.disabled).toBe(false)
    await act(async () => submit?.click())
    expect(mocks.api.getTestArchiveIterationWaterfall).toHaveBeenLastCalledWith('run-1', expect.objectContaining({ vuser_id: 0, vuser_lifetime: '42', iteration: 5 }), expect.anything())
  })

  it('reports a missing iteration and offers a retry when the archive is busy', async () => {
    mocks.api.getTestArchiveIterationWaterfall.mockRejectedValueOnce(apiError(404))
    await render({ initial: { group: 'shop', vuser_id: 9, iteration: 1, source: 'Business key' } })
    expect(container.querySelector('[role="alert"]')?.textContent).toBe('No archived rows for this VUser and iteration.')
    expect(button('Retry')).toBeUndefined()

    mocks.api.getTestArchiveIterationWaterfall.mockRejectedValueOnce(apiError(503)).mockResolvedValueOnce({ ...response, partial: true, partial_reasons: ['scan_budget'], gaps_ms: undefined })
    await act(async () => button('Median')?.click())
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('busy')
    await act(async () => button('Retry')?.click())
    expect(container.querySelector('[role="alert"]')).toBeNull()
    expect(container.textContent).toContain('rows of this iteration may be missing')
    // Gaps are unknown on a partial replay: no think rows.
    expect([...container.querySelectorAll('.wf-row[data-node-id] .wf-name-label')].map(item => item.textContent)).not.toContain('Think time or pacing (not recorded)')
  })
})
