// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import type { BrowserReplayEvent } from '../types/replay'
import type { ScriptActionDefinition } from '../types/scripts'
import InspectorNetworkTable from './InspectorNetworkTable'
import { MASKED_VALUE, type InspectorSourceContext } from './inspectorHeadersModel'

function event(overrides: Partial<BrowserReplayEvent> = {}): BrowserReplayEvent {
  return {
    phase: 'action', action_name: 'Home', action_type: 'request', method: 'GET', target: 'https://shop.example.test/',
    success: true, status_code: 200, started_at: '', completed_at: '', duration_ms: 400,
    network_entries: [
      { method: 'GET', url: 'https://shop.example.test/', status_code: 200, duration_ms: 156.9, har: { phases: [{ name: 'dns', start_ms: 0, end_ms: 4 }, { name: 'response', start_ms: 10, end_ms: 30 }] } },
      { method: 'GET', url: 'https://shop.example.test/app.js', status_code: 200, duration_ms: 30 },
      { method: 'GET', url: 'https://shop.example.test/img/keys.png', status_code: 404, duration_ms: 54.5 },
    ],
    snapshot: {
      response_body_state: 'captured',
      resources: [
        { request_url: 'https://shop.example.test/app.js', response_status: 200, response_body_state: 'captured', response_body: 'console.log("kept")', response_headers: { 'Content-Type': 'text/javascript' } },
        { request_url: 'https://shop.example.test/img/keys.png', response_status: 404, response_body_state: 'not_captured' },
      ],
      omitted_resources: 1,
    },
    ...overrides,
  } as BrowserReplayEvent
}

let container: HTMLDivElement
let root: Root

beforeEach(() => {
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
})

afterEach(() => {
  act(() => root.unmount())
  container.remove()
})

const noSources: InspectorSourceContext = { view: 'wire', authored: null }
const render = (value: BrowserReplayEvent, phasesUnavailable = false, requestSources: InspectorSourceContext = noSources) => act(() => root.render(
  <InspectorNetworkTable event={value} phasesUnavailable={phasesUnavailable} requestSources={requestSources} copyKeyPrefix="0:network" copiedKey={null} onCopy={() => undefined} />,
))
const buttons = (selector: string) => [...container.querySelectorAll<HTMLButtonElement>(selector)]
const filterButton = (label: string) => buttons('[aria-label="Network filter"] button').find(button => button.textContent?.startsWith(label))
const rowButton = (text: string) => buttons('.rr-network-row-button').find(button => button.textContent?.includes(text))
const bodyRows = () => [...container.querySelectorAll('tbody tr:not(.rr-network-detail-row)')]
/** The open detail: the region a row button's aria-controls names. */
const openDetail = () => container.querySelector<HTMLElement>('section.rr-network-detail')

describe('InspectorNetworkTable', () => {
  it('renders one accessible table with a row per request and the own request marked', () => {
    render(event())
    const table = container.querySelector('table')
    expect(table?.querySelector('caption')?.textContent).toBe('3 requests')
    expect([...container.querySelectorAll('thead th')].map(th => [th.textContent, th.getAttribute('scope')])).toEqual([
      ['Hop', 'col'], ['Request', 'col'], ['Type', 'col'], ['Status', 'col'], ['Time', 'col'], ['Body', 'col'],
    ])
    expect(bodyRows().map(row => [...row.children].map(cell => cell.textContent))).toEqual([
      ['1', 'GET / (this request)', 'other', '200', '157 ms', 'Kept'],
      ['2', 'GET /app.js', 'script', '200', '30 ms', 'Kept'],
      ['3', 'GET /img/keys.png', 'image', '404', '55 ms', 'Not kept'],
    ])
    expect(rowButton('/app.js')?.title).toBe('https://shop.example.test/app.js')
    // Status and Body carry their whole text in a tooltip (a narrow pane cuts them).
    expect(bodyRows()[2].querySelector('.rr-network-status')?.getAttribute('title')).toBe('404')
    expect(bodyRows()[2].lastElementChild?.getAttribute('title')).toBe('Not kept')
    // Wide layout renders all 6 <col>s, headers and cells.
    expect([...container.querySelectorAll('colgroup > col')].map(col => col.className)).toEqual([
      'rr-network-col-hop', 'rr-network-col-request', 'rr-network-col-type', 'rr-network-col-status', 'rr-network-col-time', 'rr-network-col-body',
    ])
    expect(container.querySelectorAll('th.rr-network-col-type, td.rr-network-col-type')).toHaveLength(4)
    expect(container.querySelectorAll('th.rr-network-col-hop, td.rr-network-col-hop')).toHaveLength(4)
    expect(container.textContent).toContain('2 bodies kept · 1 not kept · 1 more request not recorded (capture limits)')
  })

  it('filters with pressed buttons and only offers types present', () => {
    render(event())
    expect(buttons('[aria-label="Network filter"] button').map(button => button.textContent)).toEqual(['All 3', 'Failed 1', 'Scripts 1', 'Images 1', 'Other 1'])
    act(() => filterButton('Failed')?.click())
    expect(filterButton('Failed')?.getAttribute('aria-pressed')).toBe('true')
    expect(filterButton('All')?.getAttribute('aria-pressed')).toBe('false')
    expect(bodyRows()).toHaveLength(1)
    expect(container.querySelector('caption')?.textContent).toBe('1 of 3 requests shown')
    // A polite status beside the table announces what the filter left.
    expect(container.querySelector('.rr-network > [role="status"]')?.textContent).toBe('1 of 3 requests shown')
    act(() => filterButton('All')?.click())
    expect(container.querySelector('.rr-network > [role="status"]')?.textContent).toBe('')
  })

  it('keeps a filter to the event it was chosen on', () => {
    render(event())
    act(() => filterButton('Failed')?.click())
    expect(bodyRows()).toHaveLength(1)
    render(event({ action_name: 'Next' }))
    expect(filterButton('All')?.getAttribute('aria-pressed')).toBe('true')
    expect(bodyRows()).toHaveLength(3)
  })

  it('opens the selected row’s detail right under it, and toggles it off', () => {
    render(event())
    expect(rowButton('/app.js')?.getAttribute('aria-expanded')).toBe('false')
    expect(rowButton('/app.js')?.hasAttribute('aria-pressed')).toBe(false)
    act(() => rowButton('/app.js')?.click())
    expect(rowButton('/app.js')?.getAttribute('aria-expanded')).toBe('true')
    const detail = openDetail()
    expect(detail?.id).toBe(rowButton('/app.js')?.getAttribute('aria-controls'))
    // Named by its request (the row button), not a generic label.
    expect(detail?.getAttribute('aria-labelledby')).toBe(rowButton('/app.js')?.id)
    expect(detail?.hasAttribute('aria-label')).toBe(false)
    // A full-width row directly after the selected one, inside the table body.
    const detailRow = detail?.closest('tr')
    expect(detailRow?.previousElementSibling).toBe(rowButton('/app.js')?.closest('tr'))
    expect(detailRow?.parentElement?.tagName).toBe('TBODY')
    expect(detailRow?.querySelector('td')?.getAttribute('colspan')).toBe('6')
    // No column header (such as Hop) applies to the detail cell.
    expect(detailRow?.querySelector('td')?.getAttribute('headers')).toBe('')
    expect(detail?.textContent).toContain('console.log("kept")')
    expect(detail?.textContent).toContain('Response body · Kept')
    act(() => rowButton('/app.js')?.click())
    expect(openDetail()).toBeNull()
  })

  it('lists the hop phases and points the own body at the Response tab', () => {
    render(event())
    act(() => rowButton('(this request)')?.click())
    const phases = container.querySelector('[aria-label="Timing phases"]')
    expect(phases?.textContent).toBe('DNS4 msReceive20 ms')
    expect(container.textContent).toContain('This action\'s response body is on the Response tab.')
  })

  it('withholds the phases when runtime evidence is unavailable', () => {
    render(event(), true)
    act(() => rowButton('(this request)')?.click())
    expect(container.querySelector('[aria-label="Timing phases"]')).toBeNull()
  })

  it('leaves out the hop column and says unknown for a resource without an entry', () => {
    render(event({ network_entries: undefined, snapshot: { resources: [{ request_url: 'https://cdn.example.test/a.css', response_status: 200, response_body_state: 'empty' }] } }))
    expect([...container.querySelectorAll('thead th')].map(th => th.textContent)).not.toContain('Hop')
    expect([...bodyRows()[0].children].map(cell => cell.textContent)).toEqual(['GET cdn.example.test/a.css', 'style', '200', 'Unknown', 'Empty'])
  })

  it('clears the selection when the event changes', () => {
    render(event())
    act(() => rowButton('/app.js')?.click())
    render(event({ action_name: 'Next' }))
    expect(openDetail()).toBeNull()
  })

  it('shows the own request’s headers through the Request tab’s sources and masks', () => {
    const action = { name: 'Home', type: 'GET', target: '/', headers: { Authorization: '{{secret.TOKEN}}' } } as ScriptActionDefinition
    const own = event({
      network_entries: [
        { method: 'GET', url: 'https://shop.example.test/', status_code: 200, request_headers: { Authorization: 'Bearer live-token', Accept: '*/*' } },
        { method: 'GET', url: 'https://shop.example.test/app.js', status_code: 200, request_headers: { Accept: 'text/javascript' } },
      ],
    })
    render(own, false, { view: 'wire', authored: { action } })
    act(() => rowButton('(this request)')?.click())
    const detail = openDetail()!
    expect(detail.querySelector('table[aria-label="Request headers of the selected request"]')).not.toBeNull()
    expect(detail.innerHTML).not.toContain('live-token')
    expect(detail.textContent).toContain(MASKED_VALUE)
    expect(detail.textContent).toContain('Secret')
    // Another request's headers stay plain text.
    act(() => rowButton('/app.js')?.click())
    expect(openDetail()?.querySelector('table')).toBeNull()
    expect(openDetail()?.textContent).toContain('text/javascript')
  })

  it('renders nothing without requests', () => {
    render(event({ network_entries: undefined, snapshot: undefined }))
    expect(container.innerHTML).toBe('')
  })

  it('drops Hop and Type from the DOM and adjusts detail colSpan when narrow, then restores them when widened', () => {
    const originalResizeObserver = globalThis.ResizeObserver
    let observerCallback: ResizeObserverCallback | null = null
    const observed: { current: Element | null } = { current: null }
    globalThis.ResizeObserver = class {
      constructor(callback: ResizeObserverCallback) { observerCallback = callback }
      observe(element: Element) { observed.current = element }
      unobserve() {}
      disconnect() { observed.current = null }
    } as unknown as typeof ResizeObserver

    const reportWidth = (width: number) => act(() => {
      observerCallback?.([{ contentRect: { width } } as ResizeObserverEntry], {} as ResizeObserver)
    })

    try {
      // Mounting empty first and later receiving requests still attaches the observer.
      render(event({ network_entries: undefined, snapshot: undefined }))
      expect(observed.current).toBeNull()
      render(event())
      expect(observed.current?.classList.contains('rr-network')).toBe(true)

      // Zero width (hidden tabpanel) keeps the default wide column set.
      reportWidth(0)
      expect([...container.querySelectorAll('thead th')].map(th => th.textContent)).toEqual(['Hop', 'Request', 'Type', 'Status', 'Time', 'Body'])

      act(() => rowButton('/app.js')?.click())
      expect(openDetail()?.closest('tr')?.querySelector('td')?.getAttribute('colspan')).toBe('6')

      // Narrow boundary (580px): Hop and Type leave the DOM and detail colSpan matches the 4 remaining columns.
      reportWidth(580)
      expect(container.querySelector('.rr-network')?.classList.contains('is-narrow')).toBe(true)
      expect([...container.querySelectorAll('colgroup > col')].map(col => col.className)).toEqual([
        'rr-network-col-request', 'rr-network-col-status', 'rr-network-col-time', 'rr-network-col-body',
      ])
      expect([...container.querySelectorAll('thead th')].map(th => th.textContent)).toEqual(['Request', 'Status', 'Time', 'Body'])
      expect(container.querySelectorAll('.rr-network-col-hop, .rr-network-col-type')).toHaveLength(0)
      expect(bodyRows()[0].children).toHaveLength(4)
      expect(openDetail()?.closest('tr')?.querySelector('td')?.getAttribute('colspan')).toBe('4')

      // Widening past 580px (including fractional subpixel 580.4px) restores Hop and Type and updates detail colSpan back to 6.
      reportWidth(580.4)
      expect(container.querySelector('.rr-network')?.classList.contains('is-narrow')).toBe(false)
      expect([...container.querySelectorAll('thead th')].map(th => th.textContent)).toEqual(['Hop', 'Request', 'Type', 'Status', 'Time', 'Body'])
      expect(openDetail()?.closest('tr')?.querySelector('td')?.getAttribute('colspan')).toBe('6')

      // Initial mount measurement uses unrounded getBoundingClientRect().width before integer clientWidth.
      act(() => root.unmount())
      root = createRoot(container)
      const rectSpy = HTMLElement.prototype.getBoundingClientRect
      const clientWidthDesc = Object.getOwnPropertyDescriptor(Element.prototype, 'clientWidth')
      HTMLElement.prototype.getBoundingClientRect = () => ({ width: 580.4, height: 100, top: 0, left: 0, right: 580.4, bottom: 100, x: 0, y: 0, toJSON: () => ({}) }) as DOMRect
      Object.defineProperty(Element.prototype, 'clientWidth', { configurable: true, get: () => 580 })
      try {
        render(event())
        expect(container.querySelector('.rr-network')?.classList.contains('is-narrow')).toBe(false)
        expect([...container.querySelectorAll('thead th')].map(th => th.textContent)).toEqual(['Hop', 'Request', 'Type', 'Status', 'Time', 'Body'])
      } finally {
        HTMLElement.prototype.getBoundingClientRect = rectSpy
        if (clientWidthDesc) Object.defineProperty(Element.prototype, 'clientWidth', clientWidthDesc)
        else Reflect.deleteProperty(Element.prototype, 'clientWidth')
      }

      // Without Hop (5 columns wide), narrow mode still spans the 4 remaining columns.
      render(event({ network_entries: undefined, snapshot: { resources: [{ request_url: 'https://cdn.example.test/a.css', response_status: 200, response_body_state: 'empty' }] } }))
      act(() => rowButton('cdn.example.test/a.css')?.click())
      expect(openDetail()?.closest('tr')?.querySelector('td')?.getAttribute('colspan')).toBe('5')
      reportWidth(420)
      expect(openDetail()?.closest('tr')?.querySelector('td')?.getAttribute('colspan')).toBe('4')
    } finally {
      globalThis.ResizeObserver = originalResizeObserver
    }
  })

  it('uses display: none for narrow hidden columns and gives wrapped filter pills complete borders', async () => {
    // @ts-expect-error node:fs is available in the Vitest runtime.
    const { readFileSync } = await import('node:fs')
    const css = readFileSync('src/components/InspectorNetworkTable.css', 'utf8') as string
    const narrowBlock = css.match(/@container debugger-detail \(max-width: 580px\) \{([\s\S]*?)\n\}/)?.[1] ?? ''
    expect(narrowBlock).toMatch(/\.rr-network-col-type[\s\S]*?\.rr-network-col-hop[\s\S]*?display:\s*none;/)
    expect(narrowBlock).not.toMatch(/\b(?:max-)?width:\s*0\b/)
    expect(css).toMatch(/\.rr-network-toolbar \.rr-inspector-segmented\s*\{[^}]*\bflex-wrap:\s*wrap;[^}]*\bgap:\s*6px;[^}]*\bborder:\s*0;/)
    expect(css).toMatch(/\.rr-network-toolbar \.rr-inspector-segmented button\s*\{[^}]*\bborder:\s*1px solid var\(--border\);[^}]*\bwhite-space:\s*nowrap;/)
  })
})
