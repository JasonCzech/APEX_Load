// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { ReportLens } from '../../appRouting'
import type { RunArchiveTimeAnatomy } from '../../types/timeAnatomy'
import fixture from './__fixtures__/timeAnatomyReport.json'
import TransactionTimeAnatomy, { ANATOMY_STAT_STORAGE_KEY } from './TransactionTimeAnatomy'
import { reportPhaseRowId } from './timeAnatomyModel'

const mocks = vi.hoisted(() => ({ api: { getTestArchiveTransactionWaterfall: vi.fn() } }))
vi.mock('../../hooks/useApi', () => ({ useApi: () => mocks.api }))

const anatomy = fixture.time_anatomy as unknown as RunArchiveTimeAnatomy

function buttonByText(root: HTMLElement, text: string) {
  return Array.from(root.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes(text))
}

/** The shared waterfall's rows, in display order. */
const gridRows = (root: ParentNode) => Array.from(root.querySelectorAll<HTMLElement>('[role="treegrid"] [data-node-id]'))
const clickRow = (row: HTMLElement | undefined) => act(async () => { row?.querySelector('.wf-name-label')?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })

describe('TransactionTimeAnatomy', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    window.localStorage.removeItem(ANATOMY_STAT_STORAGE_KEY)
    mocks.api.getTestArchiveTransactionWaterfall.mockReturnValue(new Promise(() => {}))
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
    vi.restoreAllMocks()
    window.localStorage.removeItem(ANATOMY_STAT_STORAGE_KEY)
  })

  const render = (props: { lens?: ReportLens; selectedName?: string; anatomy?: RunArchiveTimeAnatomy; onSelectName?: (name: string) => void } = {}) => act(async () => {
    root.render(<TransactionTimeAnatomy testId="run-1" anatomy={props.anatomy ?? anatomy} selectedName={props.selectedName} onSelectName={props.onSelectName} lens={props.lens ?? 'engineer'} />)
  })
  const liveRegion = () => container.querySelector('.sr-only[aria-live="polite"]')
  const pick = async (name: string) => {
    const select = container.querySelector('select')
    expect(select).toBeTruthy()
    if (!select) return
    await act(async () => { select.value = name; select.dispatchEvent(new Event('change', { bubbles: true })) })
  }
  const section = () => container.querySelector<HTMLElement>('[data-report-focus="time-anatomy"]')
  const radios = () => Array.from(container.querySelectorAll<HTMLButtonElement>('[role="radio"]'))
  const checked = () => radios().find(radio => radio.getAttribute('aria-checked') === 'true')?.textContent

  it('leads with the tail headline and the SLA duration line', async () => {
    await render()
    const panel = section()
    expect(panel).toBeTruthy()
    if (!panel) return
    expect(panel.getAttribute('aria-labelledby')).toBe(panel.querySelector('h2')?.id)
    expect(panel.querySelector('.rr-anatomy-headline')?.textContent).toBe(anatomy.transactions[0].tail?.headline)
    // No announcement on the first render.
    expect(panel.querySelector('.rr-anatomy-headline')?.getAttribute('aria-live')).toBeNull()
    expect(panel.querySelector('.rr-anatomy-duration')?.textContent).toBe('Duration P50 110 ms · P95 130 ms · n=1,000')
    expect(panel.textContent).toContain('Average Checkout: server 61%')
    expect(Array.from(panel.querySelectorAll('.rr-anatomy-chip')).map(chip => chip.textContent)).toEqual(['Median 110 ms', 'P95 130 ms', 'Slowest 600 ms', 'Failed · HTTP 503'])
    expect(panel.querySelector('.rr-anatomy-chip')?.getAttribute('aria-label')).toBe('Median 110 ms, VUser 3, iteration 8. Open its waterfall')
  })

  it('changes the statistic and the honesty note with the arrow keys', async () => {
    await render()
    expect(checked()).toBe('P95')
    expect(container.querySelector('.rr-anatomy-honesty')?.textContent).toContain("don't add up to the transaction's P95 (130 ms)")
    const p95 = radios().find(radio => radio.textContent === 'P95')
    expect(p95).toBeTruthy()
    if (!p95) return
    await act(async () => { p95.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true })) })
    expect(checked()).toBe('P99')
    expect(document.activeElement?.textContent).toBe('P99')
    expect(container.querySelector('.rr-anatomy-honesty')?.textContent).toContain("transaction's P99 (560 ms)")
    expect(window.localStorage.getItem(ANATOMY_STAT_STORAGE_KEY)).toBe('p99')
    await act(async () => { (document.activeElement as HTMLElement).dispatchEvent(new KeyboardEvent('keydown', { key: 'Home', bubbles: true })) })
    expect(checked()).toBe('Avg')
    expect(container.querySelector('.rr-anatomy-honesty')?.textContent).toBe('Each bar shows the average of each phase. Averages add up; rows start at their median offset.')
  })

  it('keeps the breakdown behind a button for the executive lens', async () => {
    await render({ lens: 'executive' })
    expect(container.querySelector('[aria-label="Aggregate waterfall for Checkout"]')).toBeNull()
    // Executive keeps the headline, at most three bars, the chips and the composition.
    expect(container.querySelector('.rr-anatomy-headline')).not.toBeNull()
    expect(container.querySelectorAll('.rr-anatomy-diverging-row').length).toBeLessThanOrEqual(3)
    expect(container.querySelectorAll('.rr-anatomy-chip')).toHaveLength(4)
    const show = buttonByText(container, 'Show request breakdown')
    expect(show).toBeTruthy()
    if (!show) return
    await act(async () => show.click())
    expect(container.querySelector('[aria-label="Aggregate waterfall for Checkout"]')).not.toBeNull()
    expect(checked()).toBe('P50')
  })

  it('shows every coverage counter and the limits in plain words for the audit lens', async () => {
    await render({ lens: 'audit' })
    const audit = container.querySelector('.rr-anatomy-audit')
    expect(audit).toBeTruthy()
    if (!audit) return
    expect(audit.textContent).toContain('vuser_lifetime_containment_v1')
    expect(audit.textContent).toMatch(/matched to requests, \d+ may have lost requests, \d+ failed/)
    expect(audit.textContent).toContain('Request samples with clamped phases5')
    expect(audit.textContent).toContain('Rows of unknown population7')
    expect(audit.textContent).toContain('Tail bands and representative samples come from a uniform sample of instances.')
    expect(container.querySelector('.rr-anatomy-coverage')?.textContent).toContain('2 requests were written after their transaction and are excluded.')
    // The identity and work-budget limits read in plain words too, not as reason codes.
    await render({ lens: 'audit', anatomy: { ...anatomy, truncation: { ...anatomy.truncation, reasons: ['identity_cap', 'work_budget'] } } })
    const limits = Array.from(container.querySelectorAll('.rr-anatomy-audit li')).map(item => item.textContent)
    expect(limits).toEqual([
      'Too many VUsers were being tracked at once; requests held for VUsers dropped to make room were not matched.',
      'The report reached its processing budget; later transactions were not matched to requests and are left out of the averages.',
    ])
    // The engineer lens has no audit block.
    await render({ lens: 'engineer' })
    expect(container.querySelector('.rr-anatomy-audit')).toBeNull()
  })

  it('selects a contributor row and shows its phase statistics', async () => {
    await render()
    const contributor = buttonByText(container, 'POST /cart')
    expect(contributor).toBeTruthy()
    if (!contributor) return
    await act(async () => contributor.click())
    const selected = container.querySelectorAll('[role="treegrid"] [aria-selected="true"]')
    expect(selected).toHaveLength(1)
    expect(selected[0].textContent).toContain('POST /cart')
    const pane = container.querySelector('.rr-anatomy-row-pane')
    expect(pane?.textContent).toContain('90 new · 900 reused · 10 unknown · reuse 91%')
    expect(pane?.querySelectorAll('tbody tr')).toHaveLength(7)
    // A click on the selected row clears it, as before.
    await clickRow(gridRows(container).find(row => row.textContent?.includes('POST /cart')))
    expect(container.querySelector('[role="treegrid"] [aria-selected="true"]')).toBeNull()
    expect(container.querySelector('.rr-anatomy-row-pane')).toBeNull()
  })

  it('draws the aggregate rows through the shared waterfall, labelled as statistics', async () => {
    await render()
    const grid = container.querySelector('[role="treegrid"]')
    expect(grid?.getAttribute('aria-label')).toBe('Aggregate waterfall for Checkout')
    const rows = gridRows(container)
    // Chronological by median offset; Other requests has no meaningful offset and starts at 0.
    expect(rows.map(row => row.querySelector('.wf-name-label')?.textContent)).toEqual(['GET /cart', 'Other requests', 'POST /cart', 'GET /poll #9+'])
    // Each row states its statistic and is marked as an aggregate, not one request.
    expect(rows[0].querySelector('small')?.textContent).toMatch(/^P95 of each phase/)
    expect(rows.every(row => row.querySelector('[data-source]')?.getAttribute('data-source') === 'aggregate')).toBe(true)
    expect(rows[2].querySelector('.wf-status-text')?.textContent).toMatch(/^[\d,]+ failed$/)
    expect(rows[1].querySelector('.wf-bar-derived')).not.toBeNull()
    // Every row keeps an anchor id the report can scroll to.
    expect(rows[0].id).toBe(reportPhaseRowId('rr-anatomy-row', '1|GET /cart#1'))
    // Independent statistics: no critical path, no idle legend, no time-sink facts.
    expect(buttonByText(container, 'Critical path')).toBeUndefined()
    expect(container.querySelector('.wf-legend')?.textContent).not.toContain('Think / idle')
    expect(container.querySelector('.wf-sinks')).toBeNull()
    expect(buttonByText(container, 'Median offsets')).toBeTruthy()
    // The statistic changes the bars, not the rows.
    const p50 = radios().find(radio => radio.textContent === 'P50')
    await act(async () => p50?.click())
    expect(gridRows(container)[0].querySelector('small')?.textContent).toMatch(/^P50 of each phase/)
  })

  it('selects rows from the keyboard', async () => {
    await render()
    const grid = container.querySelector<HTMLElement>('[role="treegrid"]')
    expect(grid).toBeTruthy()
    if (!grid) return
    grid.focus()
    for (let step = 0; step < 3; step += 1) await act(async () => { grid.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true })) })
    expect(container.querySelector('[role="treegrid"] [aria-selected="true"]')?.textContent).toContain('POST /cart')
    expect(container.querySelector('.rr-anatomy-row-pane h4')?.textContent).toBe('POST /cart')
    expect(grid.getAttribute('aria-activedescendant')).toBe(reportPhaseRowId('rr-anatomy-row', '2|POST /cart#1'))
    // Enter on the selected row clears it, as a second click does; the arrows select again.
    await act(async () => { grid.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })) })
    expect(container.querySelector('.rr-anatomy-row-pane')).toBeNull()
    expect(container.querySelector('[role="treegrid"] [aria-selected="true"]')).toBeNull()
    await act(async () => { grid.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true })) })
    expect(container.querySelector('.rr-anatomy-row-pane h4')?.textContent).toBe('GET /cart')
  })

  it('follows the report selection and notes a request selection', async () => {
    await render({ selectedName: 'Login' })
    expect(container.querySelector('select')?.value).toBe('Login')
    expect(container.querySelector('.rr-anatomy-headline')?.textContent).toBe('Tail attribution needs at least 40 completed instances with 3 in the tail; this run has 29.')
    expect(container.textContent).toContain('This protocol records no phase timings; bars show request duration only.')
    // The untimed remainder keeps the report's own name in the legend.
    expect(container.querySelector('.wf-legend')?.textContent).toContain('No TTFB split')
    expect(container.querySelector('.rr-anatomy-definitions')?.textContent).toContain('No TTFB split: The request recorded no time to first byte')
    await render({ selectedName: 'POST /cart' })
    expect(container.querySelector('select')?.value).toBe('Checkout')
    expect(container.querySelector('.rr-anatomy-note')?.textContent).toBe('Showing Checkout; time anatomy covers business transactions')
  })

  it('announces the headline through a live region that exists before the change', async () => {
    await render({ selectedName: 'Login' })
    // Mounted and empty before any change, so the first change is announced.
    const region = liveRegion()
    expect(region).toBeTruthy()
    if (!region) return
    expect(region.textContent).toBe('')
    await pick('Checkout')
    expect(liveRegion()).toBe(region)
    expect(region.textContent).toBe(`Checkout: ${anatomy.transactions[0].tail?.headline}`)
    expect(container.querySelector('.rr-anatomy-headline')?.textContent).toContain('P95 tail is +440 ms')
    expect(container.querySelector('.rr-anatomy-headline')?.getAttribute('aria-live')).toBeNull()
    // A transaction with no matched instances unmounts the tail card; the region stays and still speaks.
    await render({ selectedName: 'Login', anatomy: { ...anatomy, transactions: [anatomy.transactions[0], { ...anatomy.transactions[1], joined_instances: 0 }] } })
    await pick('Login')
    expect(container.querySelector('.rr-anatomy-tail')).toBeNull()
    expect(liveRegion()).toBe(region)
    expect(region.textContent).toContain('Child requests could not be matched to Login')
  })

  it('lets the report own the selection when it takes the picker choice', async () => {
    const onSelectName = vi.fn()
    await render({ selectedName: 'Checkout', onSelectName })
    await pick('Login')
    expect(onSelectName).toHaveBeenCalledWith('Login')
    // The report did not change its selection, so the panel keeps showing it: no local pick wins over it.
    expect(container.querySelector('select')?.value).toBe('Checkout')
    await render({ selectedName: 'Login', onSelectName })
    expect(container.querySelector('select')?.value).toBe('Login')
  })

  it('scrolls to a contributor on every click and never replays a stale scroll', async () => {
    const scroll = vi.fn()
    Object.defineProperty(Element.prototype, 'scrollIntoView', { configurable: true, writable: true, value: scroll })
    try {
      await render()
      const contributor = container.querySelector<HTMLButtonElement>('.rr-anatomy-contributor')
      expect(contributor).toBeTruthy()
      if (!contributor) return
      await act(async () => contributor.click())
      expect(scroll).toHaveBeenCalledTimes(1)
      const target = scroll.mock.contexts[0] as HTMLElement
      expect(target.textContent).toContain('POST /cart')
      expect(target.getAttribute('role')).toBe('row')
      // The same contributor again: its row is already selected, but the reader asked to see it.
      await act(async () => contributor.click())
      expect(scroll).toHaveBeenCalledTimes(2)
      // Selecting another bar row must not jump back to the contributor.
      const other = gridRows(container).find(row => !row.textContent?.includes('POST /cart'))
      expect(other).toBeTruthy()
      await clickRow(other)
      expect(other?.getAttribute('aria-selected')).toBe('true')
      expect(scroll).toHaveBeenCalledTimes(2)
    } finally {
      delete (Element.prototype as { scrollIntoView?: unknown }).scrollIntoView
    }
  })

  it('names a failed representative without an uninformative error class', async () => {
    const [checkout] = anatomy.transactions
    const representatives = (checkout.representatives || []).map(sample => (sample.reason === 'failed' ? { ...sample, status_code: undefined, error_class: 'other' } : sample))
    await render({ anatomy: { ...anatomy, transactions: [{ ...checkout, representatives }] } })
    const chips = Array.from(container.querySelectorAll('.rr-anatomy-chip')).map(chip => chip.textContent)
    expect(chips[chips.length - 1]).toBe('Failed')
  })

  it('labels the server phase as server wait and defines it', async () => {
    await render()
    const legend = container.querySelector('.wf-legend')
    expect(legend?.textContent).toContain('Server wait')
    expect(legend?.textContent).not.toContain('TTFB)')
    expect(container.querySelector('.rr-anatomy-definitions')?.textContent).toContain('Server wait: Time to first byte minus connection setup')
    const contributor = container.querySelector<HTMLButtonElement>('.rr-anatomy-contributor')
    expect(contributor).toBeTruthy()
    if (!contributor) return
    await act(async () => contributor.click())
    const server = Array.from(container.querySelectorAll('.rr-anatomy-row-pane tbody th')).find(cell => cell.textContent === 'Server wait')
    expect(server?.getAttribute('title')).toContain('minus connection setup')
  })

  it('renders the empty, unmatched and truncation copy', async () => {
    await render()
    expect(container.textContent).toContain('Showing 2 of 4 transactions (SLA-failed first).')
    expect(container.textContent).toContain('Showing 3 request rows; 12 samples grouped as Other requests.')
    await render({ anatomy: { ...anatomy, transactions: [] } })
    expect(section()?.textContent).toContain('This run recorded no business transactions.')
    await render({ anatomy: { ...anatomy, transactions: [{ ...anatomy.transactions[0], joined_instances: 0 }] } })
    expect(container.textContent).toContain('Child requests could not be matched to Checkout (no VUser identity or no requests inside its time window).')
    expect(container.querySelector('.rr-anatomy-tail')).toBeNull()
    // Every matched instance was partial: the report's limits are the reason, not a missing identity.
    await render({ anatomy: { ...anatomy, transactions: [{ ...anatomy.transactions[0], joined_instances: 0, partial_instances: 6 }] } })
    expect(container.textContent).toContain("Every matched instance of Checkout may have lost requests to the report's processing limits")
    expect(container.textContent).not.toContain('Child requests could not be matched to Checkout')
    expect(container.querySelector('.rr-anatomy-tail')).toBeNull()
  })

  it('works when storage throws', async () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('blocked') })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('blocked') })
    await render()
    expect(checked()).toBe('P95')
    const p50 = radios().find(radio => radio.textContent === 'P50')
    expect(p50).toBeTruthy()
    if (!p50) return
    await act(async () => p50.click())
    expect(checked()).toBe('P50')
  })

  it('opens a representative in the drawer with its ordinal hints', async () => {
    await render()
    const chip = buttonByText(container, 'P95 130 ms')
    expect(chip).toBeTruthy()
    if (!chip) return
    await act(async () => chip.click())
    expect(container.querySelector('[role="dialog"]')).not.toBeNull()
    expect(mocks.api.getTestArchiveTransactionWaterfall).toHaveBeenCalledWith('run-1', 'tx_0123456789abcdef0123456789abcdef', { ordinal: 4021, first_ordinal: 4018 }, expect.objectContaining({ signal: expect.any(AbortSignal) }))
  })
})
