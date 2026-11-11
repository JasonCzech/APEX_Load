// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { BrowserReplayEvent } from '../types/replay'
import { DebugWaterfallPanel } from './DebugWaterfallPanel'
import { WATERFALL_VIRTUALIZE_THRESHOLD } from './waterfall/Waterfall'
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const at = (ms: number) => new Date(Date.UTC(2026, 10, 9, 12) + ms).toISOString()
const ev = (i: number, extra: Partial<BrowserReplayEvent> = {}): BrowserReplayEvent => ({
  phase: 'action', action_index: i, action_name: `Request ${i}`, action_type: 'request', method: 'GET', target: `https://shop.test/r${i}`, status_code: 200,
  success: true, started_at: at(i * 100), completed_at: at(i * 100 + 80), duration_ms: 80,
  protocol_evidence: { timings: { dns_ms: 5, connect_ms: 5, tls_ms: 10, ttfb_ms: 60, duration_ms: 80 } }, ...extra,
})

let cleanup: (() => void) | null = null
afterEach(() => { cleanup?.(); cleanup = null })
function mount(node: React.ReactElement) {
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  act(() => root.render(node))
  cleanup = () => { act(() => root.unmount()); container.remove() }
  return container
}
const grid = (c: HTMLElement) => c.querySelector<HTMLElement>('[role="treegrid"]')!
const press = (c: HTMLElement, key: string) => act(() => { grid(c).dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true })) })
const selectedLabel = (c: HTMLElement) => c.querySelector('.wf-row.selected .wf-name-label')?.textContent

describe('DebugWaterfallPanel', () => {
  it('moves, collapses, inspects and steps between failures from the keyboard', async () => {
    const onInspect = vi.fn(), onSelect = vi.fn()
    const events = [ev(0), ev(1, { success: false, status_code: 500, error_message: 'HTTP 500' }), ev(2), ev(3, { success: false, status_code: 503 })]
    const c = mount(<DebugWaterfallPanel events={events} onInspectEvent={onInspect} onSelectEvent={onSelect} />)
    const g = grid(c)
    expect(g.getAttribute('tabindex')).toBe('0')
    press(c, 'ArrowDown')
    expect(selectedLabel(c)).toBe('Actions')
    expect(g.getAttribute('aria-activedescendant')).toBe(c.querySelector('.wf-row.selected')!.id)
    press(c, 'ArrowDown')
    expect(selectedLabel(c)).toBe('Request 0')
    // Keyboard moves select without telling the debugger; Enter inspects.
    expect(onSelect).not.toHaveBeenCalled()
    press(c, 'Enter')
    expect(onInspect).toHaveBeenCalledWith(0)
    press(c, ']')
    expect(selectedLabel(c)).toBe('Request 1')
    press(c, ']')
    expect(selectedLabel(c)).toBe('Request 3')
    press(c, ']')
    expect(selectedLabel(c)).toBe('Request 1')
    press(c, '[')
    expect(selectedLabel(c)).toBe('Request 3')
    // ← goes to the parent, ← again collapses it, → expands.
    press(c, 'ArrowLeft')
    expect(selectedLabel(c)).toBe('Actions')
    press(c, 'ArrowLeft')
    expect(c.querySelectorAll('.wf-row[data-node-id]')).toHaveLength(1)
    press(c, 'ArrowRight')
    expect(c.querySelectorAll('.wf-row[data-node-id]')).toHaveLength(5)
    // Keyboard focus shows the tooltip (portaled to the body, measured next frame).
    await act(async () => { await new Promise(r => requestAnimationFrame(() => r(null))) })
    const tooltip = document.body.querySelector('[role="tooltip"]')
    expect(tooltip?.textContent).toContain('Actions')
    expect(c.contains(tooltip)).toBe(false)
    // Described from the active row: screen readers announce a row's description when it becomes active.
    expect(c.querySelector('.wf-row.active')?.getAttribute('aria-describedby')).toBe(tooltip?.id)
    // Modified keys belong to the browser and the app, not the grid.
    const cmdF = new KeyboardEvent('keydown', { key: 'f', metaKey: true, bubbles: true, cancelable: true })
    act(() => { g.dispatchEvent(cmdF) })
    expect(cmdF.defaultPrevented).toBe(false)
    // A click selects and tells the debugger.
    act(() => c.querySelectorAll<HTMLElement>('.wf-row[data-node-id]')[3].click())
    expect(onSelect).toHaveBeenCalledWith(2)
  })

  it('toggles phases from the legend and zooms the axis', () => {
    const c = mount(<DebugWaterfallPanel events={[ev(0), ev(1)]} selectedEventIndex={1} />)
    const chip = [...c.querySelectorAll<HTMLButtonElement>('.wf-legend-chip')].find(b => b.textContent === 'TLS')!
    act(() => chip.click())
    expect(chip.getAttribute('aria-pressed')).toBe('false')
    expect(c.querySelectorAll('.wf-seg.phase-tls.dimmed')).toHaveLength(2)
    expect(c.querySelectorAll('.wf-seg.phase-wait.dimmed')).toHaveLength(0)
    act(() => c.querySelector<HTMLButtonElement>('button[aria-label="Zoom in"]')!.click())
    const reset = [...c.querySelectorAll<HTMLButtonElement>('button')].find(b => b.textContent === 'Reset')!
    expect(reset.disabled).toBe(false)
    const fit = [...c.querySelectorAll<HTMLButtonElement>('button')].find(b => b.textContent === 'Fit to selection')!
    act(() => fit.click())
    const ticks = [...c.querySelectorAll('.wf-axis .wf-tick')].map(t => t.textContent)
    expect(ticks[0]).not.toBe('0')
    expect(c.textContent).toContain('2 requests = ')
    act(() => reset.click())
    expect([...c.querySelectorAll('.wf-axis .wf-tick')][0].textContent).toBe('0')
  })

  it('virtualizes long trees', () => {
    const events = Array.from({ length: WATERFALL_VIRTUALIZE_THRESHOLD + 30 }, (_, i) => ev(i))
    const c = mount(<DebugWaterfallPanel events={events} />)
    expect(c.querySelector('.wf-virtual')).not.toBeNull()
    expect(grid(c).getAttribute('aria-rowcount')).toBe(String(events.length + 2))
    expect(c.querySelectorAll('.wf-row[data-node-id]').length).toBeLessThan(events.length)
  })

  it('opens collapsed groups so filtered matches are visible', () => {
    const page = (i: number, extra: Partial<BrowserReplayEvent> = {}) => ev(i, { action_name: `Request ${i}`, source_transaction: 'Checkout', ...extra })
    const events = [page(0), page(1, { success: false, status_code: 500 }), ev(2)]
    const c = mount(<DebugWaterfallPanel events={events} />)
    const failuresOnly = [...c.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')].find(i => i.parentElement?.textContent?.includes('Failures only'))!
    act(() => failuresOnly.click())
    const labels = [...c.querySelectorAll('.wf-row[data-node-id] .wf-name-label')].map(n => n.textContent)
    expect(labels).toEqual(['Actions', 'Checkout', 'Request 1'])
  })


  it('reopens a collapsed parent while a filter shows its matches', () => {
    const events = [ev(0, { source_transaction: 'Checkout' }), ev(1, { source_transaction: 'Checkout', success: false, status_code: 500 })]
    const c = mount(<DebugWaterfallPanel events={events} />)
    const checkout = () => [...c.querySelectorAll<HTMLElement>('.wf-row[data-node-id]')].find(r => r.querySelector('.wf-name-label')?.textContent === 'Checkout')!
    act(() => checkout().querySelector<HTMLButtonElement>('button[aria-expanded]')!.click())
    expect(checkout().getAttribute('aria-expanded')).toBe('false')
    const failuresOnly = [...c.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')].find(i => i.parentElement?.textContent?.includes('Failures only'))!
    act(() => failuresOnly.click())
    expect(checkout().getAttribute('aria-expanded')).toBe('true')
    expect(c.textContent).toContain('Request 1')
    // While filtering, the user can still collapse the forced-open parent.
    act(() => checkout().querySelector<HTMLButtonElement>('button[aria-expanded]')!.click())
    expect(checkout().getAttribute('aria-expanded')).toBe('false')
  })


  it('keeps showing the whole run while it grows', () => {
    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    cleanup = () => { act(() => root.unmount()); container.remove() }
    act(() => root.render(<DebugWaterfallPanel events={[ev(0)]} />))
    act(() => root.render(<DebugWaterfallPanel events={[ev(0), ev(1), ev(2), ev(3)]} />))
    const slider = container.querySelector('.wf-minimap-window')!
    expect(slider.getAttribute('aria-valuetext')).toMatch(/^Showing 0 to 380 ms of 380 ms$/)
  })

  it('renders concise Inspect and Edit drawer actions with accessible action names', () => {
    const onInspect = vi.fn()
    const onEditAction = vi.fn()
    const canEditAction = vi.fn((event: BrowserReplayEvent) => event.action_index === 0)
    const events = [
      ev(0, {
        action_name: 'Pay',
        network_entries: [
          { url: 'https://shop.test/pay', method: 'POST', status_code: 302, duration_ms: 30, har: { phases: [{ name: 'wait', start_ms: 0, end_ms: 30 }] } },
          { url: 'https://shop.test/pay/done', method: 'GET', status_code: 200, duration_ms: 50, har: { phases: [{ name: 'wait', start_ms: 0, end_ms: 50 }] } },
        ],
      }),
      ev(1, { action_name: 'Teardown_only_step' }),
    ]
    const c = mount(
      <DebugWaterfallPanel
        events={events}
        selectedEventIndex={0}
        onInspectEvent={onInspect}
        canEditAction={canEditAction}
        onEditAction={onEditAction}
      />,
    )
    const drawer = () => c.querySelector('[aria-label="Selected row details"]')!
    const editPay = drawer().querySelector<HTMLButtonElement>('button[aria-label="Edit Pay"]')!
    const inspectPay = drawer().querySelector<HTMLButtonElement>('button[aria-label="Inspect Pay"]')!
    expect(editPay.textContent).toBe('Edit')
    expect(editPay.disabled).toBe(false)
    expect(inspectPay.textContent).toBe('Inspect')
    act(() => editPay.click())
    expect(onEditAction).toHaveBeenCalledTimes(1)
    expect(onEditAction).toHaveBeenLastCalledWith(events[0])
    act(() => inspectPay.click())
    expect(onInspect).toHaveBeenCalledWith(0)

    // Hop rows label Edit and Inspect with the owning action name ('Pay'), not the hop URL.
    const payRow = [...c.querySelectorAll<HTMLElement>('.wf-row[data-node-id]')].find(r => r.querySelector('.wf-name-label')?.textContent === 'Pay')!
    act(() => payRow.querySelector<HTMLButtonElement>('button[aria-expanded]')!.click())
    act(() => c.querySelector<HTMLElement>('.wf-kind-hop')!.click())
    const hopEdit = drawer().querySelector<HTMLButtonElement>('button[aria-label="Edit Pay"]')!
    expect(hopEdit.textContent).toBe('Edit')
    act(() => hopEdit.click())
    expect(onEditAction).toHaveBeenCalledTimes(2)
    expect(onEditAction).toHaveBeenLastCalledWith(events[0])

    // Non-editable action disables Edit while keeping Inspect active.
    const secondRow = [...c.querySelectorAll<HTMLElement>('.wf-row[data-node-id]')].find(r => r.querySelector('.wf-name-label')?.textContent === 'Teardown_only_step')!
    act(() => secondRow.click())
    const disabledEdit = drawer().querySelector<HTMLButtonElement>('button[aria-label="Edit Teardown_only_step"]')!
    expect(disabledEdit.textContent).toBe('Edit')
    expect(disabledEdit.disabled).toBe(true)

    // Iteration parent row omits Edit.
    const actionsRow = [...c.querySelectorAll<HTMLElement>('.wf-row[data-node-id]')].find(r => r.querySelector('.wf-name-label')?.textContent === 'Actions')!
    act(() => actionsRow.click())
    expect(drawer().querySelector('button[aria-label^="Edit "]')).toBeNull()
  })

  it('labels the single-event HAR button Download HAR and surfaces curlCommandGaps notes when truncated', () => {
    const events = [
      ev(0, {
        snapshot: {
          request_method: 'POST',
          request_url: 'https://shop.test/r0',
          request_body: 'payload...[truncated]',
          request_headers_omitted: true,
          response_status: 200,
        },
      }),
    ]
    const c = mount(<DebugWaterfallPanel events={events} selectedEventIndex={0} />)
    const drawer = c.querySelector('[aria-label="Selected row details"]')!
    const drawerButtons = [...drawer.querySelectorAll<HTMLButtonElement>('.wf-drawer-actions button')].map(b => b.textContent)
    expect(drawerButtons).toContain('Download HAR')
    expect(drawerButtons).not.toContain('Open in HAR')
    const copyCurlBtn = [...drawer.querySelectorAll<HTMLButtonElement>('.wf-drawer-actions button')].find(b => b.textContent === 'Copy as cURL')!
    const describedById = copyCurlBtn.getAttribute('aria-describedby')
    expect(describedById).toBeTruthy()
    const noteEl = drawer.querySelector(`#${CSS.escape(describedById!)}`)
    expect(noteEl?.textContent).toContain('cURL note: body truncated by retention limits, some headers omitted or shortened by retention limits.')
  })

})
