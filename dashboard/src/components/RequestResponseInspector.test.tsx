// @vitest-environment jsdom
import { act, useState, type ReactNode } from 'react'
import { createRoot, type Root } from 'react-dom/client'
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { BrowserReplayEvent } from '../types/replay'
import type { ScriptDefinition } from '../types/scripts'
import RequestResponseInspector, { type RequestResponseInspectorProps } from './RequestResponseInspector'
import type { RequestResponseInspectorTab, ReplayVariableDiff } from './requestResponseInspectorModel'

const script: ScriptDefinition = {
  name: 'Store', version: '1.0', protocol: 'http', config: { base_url: 'https://api.example.test' },
  actions: [{ name: 'Checkout', type: 'POST', target: '/checkout' }],
}

function event(overrides: Partial<BrowserReplayEvent> = {}): BrowserReplayEvent {
  return {
    phase: 'action', action_index: 0, action_name: 'Checkout', action_type: 'request', method: 'POST', target: '/checkout',
    success: true, status_code: 200, started_at: '2026-10-25T12:00:00Z', completed_at: '2026-10-25T12:00:01Z', duration_ms: 1250,
    ...overrides,
  } as BrowserReplayEvent
}

const failed = () => event({
  success: false, status_code: 500, error_message: 'Expected 200, got 500',
  diagnosis: { category: 'correlation', severity: 'error', cause: 'A rotated token was replayed.', evidence: 'csrf differs', action: 'Add an extractor.' },
})

const wireSnapshot = {
  request_method: 'POST', request_url: 'https://api.example.test/checkout', request_headers: { 'X-Wire': 'sent' }, request_body: 'wire-body',
  response_status: 200, response_headers: { 'Content-Type': 'application/json' }, response_body: '{"token":"abc"}',
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

type Overrides = Partial<RequestResponseInspectorProps> & { event: BrowserReplayEvent }

function props(overrides: Overrides): RequestResponseInspectorProps {
  return {
    index: 0, script, tab: 'response', onTabChange: vi.fn(), replaying: false, replayMatchesCurrentScript: true,
    runtimeVariablesUnavailable: false, variableDiff: {}, copiedSection: null, onCopySection: vi.fn(async () => {}),
    onEditAction: vi.fn(), onOpenDebugOptions: vi.fn(), addExtractorDefinition: vi.fn(() => ({ applied: true }) as never),
    onViewScreenshot: vi.fn(),
    ...overrides,
  }
}

/** Holds the sub-tab like ScriptReplayDebuggerView does, so it survives selecting another event. */
function Harness({ initialTab = 'response', onTabChange, ...rest }: Overrides & { initialTab?: RequestResponseInspectorTab }) {
  const [tab, setTab] = useState<RequestResponseInspectorTab>(initialTab)
  return <RequestResponseInspector {...props(rest)} tab={tab} onTabChange={next => { onTabChange?.(next); setTab(next) }} />
}

const render = (node: ReactNode) => act(() => root.render(node))
const tabs = () => Array.from(container.querySelectorAll<HTMLButtonElement>('[role="tablist"][aria-label="Request and response views"] [role="tab"]'))
const tabNames = () => tabs().map(tab => tab.textContent)
const tab = (label: string) => tabs().find(node => node.firstElementChild?.textContent === label)
const selectedTab = () => tabs().find(node => node.getAttribute('aria-selected') === 'true')
// Every view stays mounted; the inactive ones are hidden.
const panel = () => container.querySelector<HTMLElement>('[role="tabpanel"]:not([hidden])')!
const panels = () => Array.from(container.querySelectorAll<HTMLElement>('[role="tabpanel"]'))
const button = (text: string, scope: ParentNode = container) => Array.from(scope.querySelectorAll<HTMLButtonElement>('button')).find(node => node.textContent === text)
const occurrences = (text: string) => (container.textContent ?? '').split(text).length - 1
/** A header or form-field table by its accessible name, in the shown view. */
const namedTable = (label: string, scope: ParentNode = panel()) => scope.querySelector<HTMLTableElement>(`table[aria-label="${label}"]`)
/** Each body row's cells as text. */
/** A cell's text without the narrow-pane direction label the Correlation table repeats in its variable cell. */
const cellText = (cell: Element) => { const copy = cell.cloneNode(true) as Element; copy.querySelectorAll('.rr-correlation-direction-inline').forEach(node => node.remove()); return copy.textContent ?? '' }
const rowCells = (table: Element | null) => Array.from(table?.querySelectorAll('tbody tr') ?? []).map(row => Array.from(row.children).map(cellText))
/** The cells of the first body row whose header cell (or, without one, first cell) is name. */
const rowNamed = (table: Element | null, name: string) => rowCells(table).find(cells => cells.includes(name))
const press = (target: HTMLElement, key: string) => act(() => { target.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true })) })
const typeInto = (input: HTMLInputElement, value: string) => act(() => {
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
})

describe('RequestResponseInspector', () => {
  it('shows the status in the header and the error once, in the verdict', () => {
    const onCompareWithRecording = vi.fn()
    render(<Harness event={failed()} onCompareWithRecording={onCompareWithRecording} />)
    const header = container.querySelector('.rr-inspector-header')!
    expect(header.querySelector('.rr-inspector-name')?.getAttribute('title')).toBe('Checkout')
    expect(header.querySelector('.rr-inspector-target')?.getAttribute('title')).toBe('POST /checkout')
    expect(header.querySelector('.rr-inspector-status')?.textContent).toBe('HTTP 500')
    expect(header.querySelector('.rr-inspector-status')?.classList.contains('is-failure')).toBe(true)
    expect(header.querySelector('.rr-inspector-duration')?.textContent).toBe('1.25s')
    expect(header.textContent).not.toContain('Expected 200, got 500')
    expect(button('Edit this action', header)).toBeDefined()

    const verdict = container.querySelector('[aria-label="Verdict"]')!
    const alerts = container.querySelectorAll('[role="alert"]')
    expect(alerts).toHaveLength(1)
    expect(verdict.contains(alerts[0])).toBe(true)
    expect(alerts[0].textContent).toBe('Expected 200, got 500')
    expect(occurrences('Expected 200, got 500')).toBe(1)
    expect(verdict.querySelector('.replay-diagnosis-panel .replay-diagnosis-chip')?.textContent).toBe('Correlation')
    expect(verdict.textContent).toContain('A rotated token was replayed.')
    expect(verdict.textContent).toContain('csrf differs')
    expect(verdict.textContent).toContain('Add an extractor.')

    act(() => button('Compare with recording', verdict)!.click())
    expect(onCompareWithRecording).toHaveBeenCalledTimes(1)
  })

  it('shows the error once when the diagnosis cause repeats it, and keeps the rest of the diagnosis', () => {
    const error = 'expected status 200, got 401'
    render(<Harness event={event({
      success: false, status_code: 401, error_message: error,
      diagnosis: { category: 'auth', severity: 'error', cause: error, evidence: 'Protocol status: 401', action: 'Check the credentials.' },
      repair_suggestions: [{ id: 'auth-1', title: 'Refresh the session token', description: 'Re-login before this step.' }],
    } as Partial<BrowserReplayEvent>)} />)
    expect(occurrences(error)).toBe(1)
    const verdict = container.querySelector('[aria-label="Verdict"]')!
    expect(verdict.querySelector('[role="alert"]')?.textContent).toBe(error)
    const diagnosis = verdict.querySelector('.replay-diagnosis-panel')!
    expect(diagnosis.querySelector('.replay-diagnosis-chip')).not.toBeNull()
    expect(diagnosis.textContent).toContain('Protocol status: 401')
    expect(diagnosis.textContent).toContain('Check the credentials.')
    expect(diagnosis.textContent).toContain('Refresh the session token')

    // A cause that holds the whole error, or one the error holds, is the same message.
    const extractor = 'extractor "c_cshc" found no match: pattern not found in body'
    render(<Harness event={event({ success: false, error_message: extractor,
      diagnosis: { category: 'correlation', severity: 'error', cause: `  ${extractor}.` } })} />)
    expect(occurrences('extractor "c_cshc" found no match')).toBe(1)
    render(<Harness event={event({ success: false, error_message: `Action failed: ${extractor}`,
      diagnosis: { category: 'correlation', severity: 'error', cause: extractor } })} />)
    expect(occurrences('extractor "c_cshc" found no match')).toBe(1)
    // A cause that says something else still shows.
    render(<Harness event={event({ success: false, error_message: error,
      diagnosis: { category: 'auth', severity: 'error', cause: 'The session expired.' } })} />)
    expect(container.querySelector('[aria-label="Verdict"]')?.textContent).toContain('The session expired.')
  })

  it('says Failed without a status code and offers the comparison only for correlation failures it can reach', () => {
    render(<Harness event={event({ success: false, status_code: undefined, error_message: 'Connection refused' })} onCompareWithRecording={vi.fn()} />)
    expect(container.querySelector('.rr-inspector-status')?.textContent).toBe('Failed')
    expect(occurrences('Connection refused')).toBe(1)
    expect(button('Compare with recording')).toBeUndefined()

    render(<Harness event={failed()} />)
    expect(container.querySelector('.replay-diagnosis-panel')).not.toBeNull()
    expect(button('Compare with recording')).toBeUndefined()

    render(<Harness event={event({ ...failed(), diagnosis: { category: 'timeout', severity: 'error', cause: 'Slow upstream.' } })} onCompareWithRecording={vi.fn()} />)
    expect(container.querySelector('.replay-diagnosis-chip')?.textContent).toBe('Timeout')
    expect(button('Compare with recording')).toBeUndefined()
  })

  it('summarizes a passed event in one line and keeps its diagnosis', () => {
    render(<Harness event={event()} onCompareWithRecording={vi.fn()} />)
    const verdict = container.querySelector('[aria-label="Verdict"]')!
    expect(verdict.classList.contains('is-passed')).toBe(true)
    expect(verdict.querySelector('.rr-inspector-verdict-line')?.textContent).toBe('Passed · HTTP 200')
    // Plain content: a live region here would announce every selected event.
    expect(verdict.querySelector('[role="status"]')).toBeNull()
    expect(container.querySelector('[role="alert"]')).toBeNull()
    expect(container.querySelector('.rr-inspector-status')?.textContent).toBe('HTTP 200')
    expect(container.querySelector('.rr-inspector-status')?.classList.contains('is-success')).toBe(true)
    expect(verdict.querySelector('.replay-diagnosis-panel')).toBeNull()

    render(<Harness event={event({ status_code: undefined, diagnosis: { category: 'correlation', severity: 'info', cause: 'Self-healed a locator.' } })} onCompareWithRecording={vi.fn()} />)
    expect(container.querySelector('[aria-label="Verdict"] .rr-inspector-verdict-line')?.textContent).toBe('Passed')
    expect(container.querySelector('[aria-label="Verdict"] [role="status"]')).toBeNull()
    expect(container.querySelector('.rr-inspector-status')?.textContent).toBe('HTTP OK')
    expect(container.querySelector('[aria-label="Verdict"]')?.textContent).toContain('Self-healed a locator.')
    // The comparison link is for failures only.
    expect(button('Compare with recording')).toBeUndefined()
  })

  it('hides Edit this action while replaying or when the run is for another draft', () => {
    render(<Harness event={event()} replaying />)
    expect(button('Edit this action')).toBeUndefined()
    render(<Harness event={event()} replayMatchesCurrentScript={false} />)
    expect(button('Edit this action')).toBeUndefined()
  })

  it('links each tab to its panel, defaults to Response, and switches on click', () => {
    const onTabChange = vi.fn()
    render(<Harness event={event({ snapshot: wireSnapshot })} onTabChange={onTabChange} />)
    expect(tabNames()).toEqual(['Response', 'Request', 'Correlation', 'Timing & transport'])
    expect(selectedTab()?.textContent).toBe('Response')
    expect(tabs().map(node => node.tabIndex)).toEqual([0, -1, -1, -1])
    expect(container.querySelector('.rr-inspector-tabs')?.classList.contains('ux-underline-tabs')).toBe(true)
    // Every tab's aria-controls names a panel in the document.
    for (const node of tabs()) {
      const controlled = document.getElementById(node.getAttribute('aria-controls')!)
      expect(controlled?.getAttribute('role')).toBe('tabpanel')
      expect(controlled?.getAttribute('aria-labelledby')).toBe(node.id)
    }
    expect(panels()).toHaveLength(tabs().length)
    expect(panel().id).toBe(selectedTab()!.getAttribute('aria-controls'))
    expect(panel().getAttribute('aria-labelledby')).toBe(selectedTab()!.id)
    expect(panel().classList.contains('replay-event-body')).toBe(true)
    expect(button('Copy full response', panel())).toBeDefined()

    act(() => tab('Request')!.click())
    expect(onTabChange).toHaveBeenLastCalledWith('request')
    expect(selectedTab()?.textContent).toBe('Request')
    expect(tabs().map(node => node.tabIndex)).toEqual([-1, 0, -1, -1])
    expect(panel().id).toBe(tab('Request')!.getAttribute('aria-controls'))
    expect(panel().getAttribute('aria-labelledby')).toBe(tab('Request')!.id)
    expect(button('Copy full request', panel())).toBeDefined()
    // Only the active view is shown; the others stay mounted but hidden.
    expect(button('Copy full response', panel())).toBeUndefined()
    expect(panels().filter(node => !node.hidden)).toHaveLength(1)
    const responsePanel = document.getElementById(tab('Response')!.getAttribute('aria-controls')!)!
    expect(responsePanel.hidden).toBe(true)
    expect(button('Copy full response', responsePanel)).toBeDefined()
  })

  it('moves between tabs with the arrow, Home and End keys', () => {
    render(<Harness event={event({ snapshot: wireSnapshot, protocol_evidence: { protocol: 'http' } })} />)
    expect(tabNames()).toEqual(['Response', 'Request', 'Correlation', 'Timing & transport', 'Raw'])
    tab('Response')!.focus()
    press(tab('Response')!, 'ArrowRight')
    expect(selectedTab()?.textContent).toBe('Request')
    expect(document.activeElement).toBe(tab('Request'))
    press(tab('Request')!, 'ArrowRight')
    expect(selectedTab()?.textContent).toBe('Correlation')
    press(tab('Correlation')!, 'ArrowLeft')
    expect(selectedTab()?.textContent).toBe('Request')
    press(tab('Request')!, 'End')
    expect(selectedTab()?.textContent).toBe('Raw')
    expect(document.activeElement).toBe(tab('Raw'))
    press(tab('Raw')!, 'ArrowRight')
    expect(selectedTab()?.textContent).toBe('Response')
    press(tab('Response')!, 'ArrowLeft')
    expect(selectedTab()?.textContent).toBe('Raw')
    press(tab('Raw')!, 'Home')
    expect(selectedTab()?.textContent).toBe('Response')
    expect(document.activeElement).toBe(tab('Response'))
    expect(panel().getAttribute('aria-labelledby')).toBe(tab('Response')!.id)
  })

  it('shows the request as sent on the wire by default and as scripted on request', () => {
    const onCopySection = vi.fn(async () => {})
    render(<Harness event={event({ snapshot: wireSnapshot, headers: { 'X-Scripted': 'resolved' }, body: 'scripted-body' })} initialTab="request" onCopySection={onCopySection} />)
    const group = container.querySelector('[role="group"][aria-label="Request view"]')!
    const wire = button('Sent on the wire', group)!
    const scripted = button('As scripted', group)!
    expect(wire.getAttribute('aria-pressed')).toBe('true')
    expect(scripted.getAttribute('aria-pressed')).toBe('false')
    expect(panel().textContent).toContain('POST https://api.example.test/checkout')
    expect(rowNamed(namedTable('Request headers sent on the wire'), 'X-Wire')?.slice(0, 2)).toEqual(['X-Wire', 'sent'])
    expect(panel().textContent).toContain('wire-body')
    expect(namedTable('Request headers as scripted')).toBeNull()
    act(() => button('Copy full request', panel())!.click())
    expect(onCopySection).toHaveBeenCalledWith('0:request', expect.stringContaining('wire-body'))

    act(() => scripted.click())
    expect(wire.getAttribute('aria-pressed')).toBe('false')
    expect(scripted.getAttribute('aria-pressed')).toBe('true')
    expect(rowNamed(namedTable('Request headers as scripted'), 'X-Scripted')?.slice(0, 2)).toEqual(['X-Scripted', 'resolved'])
    expect(namedTable('Request headers sent on the wire')).toBeNull()
    expect(Array.from(panel().querySelectorAll('.replay-detail-label')).map(node => node.textContent)).toContain('Body')
    expect(panel().textContent).toContain('scripted-body')
    expect(panel().textContent).not.toContain('wire-body')
    expect(button('Copy full request')).toBeUndefined()

    act(() => wire.click())
    expect(panel().textContent).toContain('wire-body')
  })

  it("opens each event's request on the wire, whatever was chosen for another event", () => {
    const first = event({ snapshot: wireSnapshot, headers: { 'X-Scripted': 'resolved' }, body: 'scripted-body' })
    const second = event({ action_name: 'Pay', started_at: '2026-10-25T12:00:02Z', snapshot: { ...wireSnapshot, request_body: 'second-wire-body' }, headers: { 'X-Scripted': 'resolved' }, body: 'second-scripted-body' })
    const requestView = (label: string) => button(label, container.querySelector('[role="group"][aria-label="Request view"]')!)!
    render(<Harness event={first} initialTab="request" />)
    act(() => requestView('As scripted').click())
    expect(requestView('As scripted').getAttribute('aria-pressed')).toBe('true')

    render(<Harness event={second} index={1} initialTab="request" />)
    expect(selectedTab()?.textContent).toBe('Request')
    expect(requestView('Sent on the wire').getAttribute('aria-pressed')).toBe('true')
    expect(panel().textContent).toContain('second-wire-body')
    expect(namedTable('Request headers as scripted')).toBeNull()

    // A new run of the same action starts on the wire too.
    act(() => requestView('As scripted').click())
    expect(requestView('As scripted').getAttribute('aria-pressed')).toBe('true')
    render(<Harness event={{ ...second, started_at: '2026-10-25T12:05:00Z' }} index={1} initialTab="request" />)
    expect(requestView('Sent on the wire').getAttribute('aria-pressed')).toBe('true')
  })

  it('shows the only request view there is, without the toggle', () => {
    render(<Harness event={event({ headers: { 'X-Scripted': 'resolved' }, body: 'scripted-body' })} initialTab="request" />)
    expect(container.querySelector('[role="group"][aria-label="Request view"]')).toBeNull()
    expect(rowNamed(namedTable('Request headers as scripted'), 'X-Scripted')).toBeDefined()
    expect(panel().textContent).toContain('scripted-body')

    render(<Harness event={event({ snapshot: wireSnapshot })} initialTab="request" />)
    expect(container.querySelector('[role="group"][aria-label="Request view"]')).toBeNull()
    expect(panel().textContent).toContain('wire-body')
    expect(namedTable('Request headers sent on the wire')).not.toBeNull()
    expect(namedTable('Request headers as scripted')).toBeNull()

    // With nothing captured, the scripted view says there are no headers.
    render(<Harness event={event()} initialTab="request" />)
    expect(container.querySelector('[role="group"][aria-label="Request view"]')).toBeNull()
    expect(panel().textContent).toContain('No request headers captured.')
  })

  it('hides Network and Raw when the event has nothing for them', () => {
    render(<Harness event={event({ snapshot: wireSnapshot })} />)
    expect(tab('Network')).toBeUndefined()
    expect(tab('Raw')).toBeUndefined()

    render(<Harness event={event({
      snapshot: { ...wireSnapshot, resources: [{ request_url: '/a.css', response_status: 200, response_body_state: 'captured' }, { request_url: '/b.js', response_status: 200, response_body_state: 'captured' }] },
      protocol_evidence: { protocol: 'http', operation: 'POST' },
      protocol_evidence_omitted_count: 2,
    })} />)
    expect(tabNames()).toEqual(['Response', 'Request', 'Correlation', 'Timing & transport', 'Network 2', 'Raw'])
    expect(tab('Network')?.querySelector('.rr-inspector-tab-badge')?.textContent).toBe('2')
    act(() => tab('Network')!.click())
    expect(panel().querySelector('[aria-label="Network requests"] table')?.textContent).toContain('/a.css')
    act(() => tab('Raw')!.click())
    expect(panel().textContent).toContain('2 protocol evidence items were omitted or shortened by retention limits.')
    expect(panel().querySelector('.replay-code-block .replay-detail-label')?.textContent).toMatch(/^Protocol Evidence/)
    expect(panel().querySelector('details')).toBeNull()

    // Browser artifacts alone also give a Network view; network entries set its badge.
    render(<Harness event={event({ network_entries: [{ method: 'GET', url: '/api/one', status_code: 200 }], console_entries: [{ level: 'info', text: 'ready' }] } as Partial<BrowserReplayEvent>)} />)
    expect(tab('Network')?.textContent).toBe('Network 1')
    act(() => tab('Network')!.click())
    expect(panel().querySelector('[aria-label="Network requests"] table')?.textContent).toContain('/api/one')
    expect(panel().querySelector('[aria-label="Page artifacts"] h4')?.textContent).toBe('Page artifacts')
    expect(panel().textContent).toContain('ready')
  })

  it("hides Network when its only entry is the event's own request", () => {
    const aura = 'https://org.example.test/aura?r=12&aura.ApexAction.execute=1'
    const auraPost = (overrides: Partial<BrowserReplayEvent> = {}) => event({
      action_name: 'Aura POST', method: 'POST', target: '/aura?r=12&aura.ApexAction.execute=1',
      snapshot: { ...wireSnapshot, request_url: aura },
      network_entries: [{ method: 'POST', url: aura, status_code: 200, duration_ms: 42 }],
      ...overrides,
    } as Partial<BrowserReplayEvent>)
    render(<Harness event={auraPost()} />)
    expect(tab('Network')).toBeUndefined()
    // The relative target names the same request.
    render(<Harness event={auraPost({ snapshot: { ...wireSnapshot, request_url: undefined } } as Partial<BrowserReplayEvent>)} />)
    expect(tab('Network')).toBeUndefined()

    // Anything more keeps the view: another entry, a different request, resources or page artifacts.
    render(<Harness event={auraPost({ network_entries: [{ method: 'POST', url: aura }, { method: 'GET', url: 'https://org.example.test/s/sfsites/l/app.js' }] } as Partial<BrowserReplayEvent>)} />)
    expect(tab('Network')?.textContent).toBe('Network 2')
    render(<Harness event={auraPost({ network_entries: [{ method: 'GET', url: aura }] } as Partial<BrowserReplayEvent>)} />)
    expect(tab('Network')?.textContent).toBe('Network 1')
    render(<Harness event={auraPost({ network_entries: [{ method: 'POST', url: 'https://other.example.test/aura?r=12&aura.ApexAction.execute=1' }] } as Partial<BrowserReplayEvent>)} />)
    expect(tab('Network')?.textContent).toBe('Network 1')
    render(<Harness event={auraPost({ snapshot: { ...wireSnapshot, request_url: aura, resources: [{ request_url: '/a.css', response_status: 200, response_body_state: 'captured' }] } } as Partial<BrowserReplayEvent>)} />)
    expect(tab('Network')).toBeDefined()
    for (const artifacts of [{ console_entries: [{ level: 'info', text: 'ready' }] }, { dom_summary: { title: 'Home' } }, { screenshot_base64: 'iVBORw0KGgo=' }, { html: '<p>hi</p>' }]) {
      render(<Harness event={auraPost(artifacts as Partial<BrowserReplayEvent>)} />)
      expect(tab('Network'), JSON.stringify(artifacts)).toBeDefined()
    }
  })

  it('keeps the chosen tab across events and shows Response where it is hidden', () => {
    const withNetwork = event({ action_name: 'Catalog', snapshot: { ...wireSnapshot, resources: [{ request_url: '/a.css', response_status: 200, response_body_state: 'captured' }] } })
    const withoutNetwork = event({ action_name: 'Checkout', snapshot: wireSnapshot })
    const onTabChange = vi.fn()
    render(<Harness event={withNetwork} onTabChange={onTabChange} />)
    act(() => tab('Network')!.click())
    expect(selectedTab()?.firstElementChild?.textContent).toBe('Network')

    render(<Harness event={withoutNetwork} index={1} onTabChange={onTabChange} />)
    expect(tab('Network')).toBeUndefined()
    expect(selectedTab()?.textContent).toBe('Response')
    expect(panel().getAttribute('aria-labelledby')).toBe(tab('Response')!.id)
    expect(button('Copy full response', panel())).toBeDefined()
    // The fallback does not overwrite the choice.
    expect(onTabChange).toHaveBeenCalledTimes(1)

    render(<Harness event={withNetwork} index={0} onTabChange={onTabChange} />)
    expect(selectedTab()?.firstElementChild?.textContent).toBe('Network')
    expect(panel().querySelector('[aria-label="Network requests"]')).not.toBeNull()

    act(() => tab('Correlation')!.click())
    render(<Harness event={withoutNetwork} index={1} onTabChange={onTabChange} />)
    expect(selectedTab()?.firstElementChild?.textContent).toBe('Correlation')
  })

  it('keeps focus on the selected tab when the shown tab changes under it, and only then', () => {
    const withNetwork = event({ action_name: 'Catalog', snapshot: { ...wireSnapshot, resources: [{ request_url: '/a.css', response_status: 200, response_body_state: 'captured' }] } })
    const withoutNetwork = event({ action_name: 'Checkout', started_at: '2026-10-25T12:00:02Z', snapshot: wireSnapshot })
    const onTabChange = vi.fn()
    render(<Harness event={withNetwork} initialTab="network" onTabChange={onTabChange} />)
    tab('Network')!.focus()
    expect(document.activeElement).toBe(tab('Network'))
    render(<Harness event={withoutNetwork} index={1} initialTab="network" onTabChange={onTabChange} />)
    expect(tab('Network')).toBeUndefined()
    expect(selectedTab()?.textContent).toBe('Response')
    expect(document.activeElement).toBe(tab('Response'))

    // The remembered tab is offered again: focus follows the selection back to it.
    render(<Harness event={withNetwork} index={0} initialTab="network" onTabChange={onTabChange} />)
    expect(selectedTab()?.firstElementChild?.textContent).toBe('Network')
    expect(document.activeElement).toBe(selectedTab())
    expect(selectedTab()?.tabIndex).toBe(0)
    // The fallback and the return never overwrite the choice.
    expect(onTabChange).not.toHaveBeenCalled()
    // Arrow keys move on from the shown tab.
    press(selectedTab()!, 'ArrowLeft')
    expect(selectedTab()?.firstElementChild?.textContent).toBe('Timing & transport')
    expect(document.activeElement).toBe(selectedTab())
    act(() => tab('Network')!.click())
    tab('Network')!.focus()

    // Focus elsewhere stays where it is.
    const outside = document.createElement('button')
    document.body.appendChild(outside)
    outside.focus()
    render(<Harness event={withoutNetwork} index={1} initialTab="network" onTabChange={onTabChange} />)
    expect(selectedTab()?.textContent).toBe('Response')
    expect(document.activeElement).toBe(outside)
    render(<Harness event={withNetwork} index={0} initialTab="network" onTabChange={onTabChange} />)
    expect(selectedTab()?.firstElementChild?.textContent).toBe('Network')
    expect(document.activeElement).toBe(outside)
    outside.focus()
    render(<Harness event={withoutNetwork} index={1} initialTab="network" />)
    expect(selectedTab()?.textContent).toBe('Response')
    expect(document.activeElement).toBe(outside)
    outside.remove()
  })

  it('shows the extractor tester in Correlation without a disclosure and counts extractions and variable changes', () => {
    const variableDiff: ReplayVariableDiff = { session: { previous: 'old', next: 'new' } }
    render(<Harness event={event({ snapshot: wireSnapshot, extractions: { token: 'abc', order: '42' } })} variableDiff={variableDiff} initialTab="correlation" />)
    expect(tab('Correlation')?.textContent).toBe('Correlation 3')
    expect(tab('Correlation')?.querySelector('.rr-inspector-tab-badge')?.textContent).toBe('3')
    const heading = Array.from(panel().querySelectorAll('h4')).find(node => node.textContent === 'Test an extractor against this response')
    expect(heading).toBeDefined()
    const section = heading!.closest('section')!
    expect(section.getAttribute('aria-labelledby')).toBe(heading!.id)
    const tester = section.querySelector('[aria-label="Extractor tester"]')
    expect(tester).not.toBeNull()
    expect(tester!.closest('details')).toBeNull()
    expect(section.querySelector('[aria-label="Extractor expression"]')).not.toBeNull()
    const correlation = panel().querySelector('.rr-correlation-table')
    expect(rowNamed(correlation, 'token')?.slice(0, 2)).toEqual(['Extracts', 'token'])
    expect(rowNamed(correlation, 'session')).toEqual(['Sets', 'session', 'Unknown', 'old→changed tonew', 'ChangedNot used by later steps', ''])
    expect(panel().textContent).not.toContain('No extractions or variable changes were recorded for this action.')
  })

  it('keeps the extractor being written while the user checks the response', () => {
    render(<Harness event={event({ snapshot: wireSnapshot })} initialTab="correlation" />)
    const expression = () => panel().querySelector<HTMLInputElement>('[aria-label="Extractor expression"]')!
    typeInto(expression(), '"token":"([^"]+)"')
    expect(expression().value).toBe('"token":"([^"]+)"')
    act(() => tab('Response')!.click())
    expect(panel().textContent).toContain('"token": "abc"')
    expect(panel().querySelector('[aria-label="Extractor expression"]')).toBeNull()
    act(() => tab('Correlation')!.click())
    expect(expression().value).toBe('"token":"([^"]+)"')
  })

  it('says when Correlation has nothing to show', () => {
    render(<Harness event={event()} initialTab="correlation" />)
    expect(tab('Correlation')?.textContent).toBe('Correlation')
    expect(panel().textContent).toContain('No extractions or variable changes were recorded for this action.')
    expect(panel().querySelector('[aria-label="Extractor tester"]')).toBeNull()
  })

  it('shows timing, protocol evidence and the transport trace expanded in Timing & transport', () => {
    const timed = event({
      page_url: 'https://shop.example.test/cart',
      protocol_evidence: {
        protocol: 'http',
        http_version_policy: '2', http_version_negotiated: '2', http_version_source: 'wire',
        response_content_state: 'complete', response_content_basis: 'identity', response_content_source: 'wire',
        network_trace: { http_protocol: 'HTTP/2.0', remote_addr: '203.0.113.10:443', connection_reused: true },
      },
    })
    render(<Harness event={timed} initialTab="timing" />)
    for (const label of ['Started', 'Completed', 'Page URL']) expect(panel().textContent).toContain(label)
    expect(panel().textContent).toContain('https://shop.example.test/cart')
    const protocol = panel().querySelector('[aria-label="Protocol evidence"]')
    expect(protocol).not.toBeNull()
    expect(protocol!.querySelector('h4')?.textContent).toBe('Protocol evidence · HTTP/2 · Complete response')
    expect(protocol!.textContent).toContain('Identity response content')
    const trace = panel().querySelector('[aria-label="Network and transport trace"]')
    expect(trace?.querySelector('h4')?.textContent).toMatch(/^Network & transport trace · HTTP\/2.0/)
    // Everything is shown inline: no disclosure in the view.
    expect(panel().querySelector('details')).toBeNull()

    // The same evidence is withheld while runtime evidence is unavailable.
    render(<Harness event={timed} initialTab="timing" runtimeVariablesUnavailable />)
    for (const label of ['Started', 'Completed', 'Page URL']) expect(panel().textContent).toContain(label)
    expect(panel().querySelector('[aria-label="Protocol evidence"]')).toBeNull()
    expect(panel().querySelector('[aria-label="Network and transport trace"]')).toBeNull()
    expect(panel().textContent).not.toContain('Complete response')
  })

  it('explains a missing response body and links to Debug options when it can be captured', () => {
    const onOpenDebugOptions = vi.fn()
    render(<Harness event={event({ response_body_state: 'not_captured' })} onOpenDebugOptions={onOpenDebugOptions} />)
    expect(panel().textContent).toContain('Body not captured.')
    act(() => button('Open Debug options', panel())!.click())
    expect(onOpenDebugOptions).toHaveBeenCalledTimes(1)

    render(<Harness event={event({ response_body_state: 'redacted' })} />)
    expect(panel().textContent).toContain('Body removed by the credential-redaction policy.')
    expect(button('Open Debug options')).toBeUndefined()
  })
})

/** Holds the selection and the sub-tab like ScriptReplayDebuggerView: onSelectEvent moves the selection. */
function RunHarness({ events, initialIndex = 0, initialTab = 'response', onSelect, ...rest }: Omit<Overrides, 'event'> & {
  events: BrowserReplayEvent[]; initialIndex?: number; initialTab?: RequestResponseInspectorTab; onSelect?: (index: number) => void
}) {
  const [index, setIndex] = useState(initialIndex)
  const [tab, setTab] = useState<RequestResponseInspectorTab>(initialTab)
  return <RequestResponseInspector
    {...props({ event: events[index], ...rest })}
    index={index}
    events={events}
    onSelectEvent={next => { onSelect?.(next); setIndex(next) }}
    tab={tab}
    onTabChange={next => { rest.onTabChange?.(next); setTab(next) }}
  />
}

const loginEvent = (overrides: Partial<BrowserReplayEvent> = {}) => event({
  event_id: 'login', phase: 'init', action_index: 0, action_name: 'T02_LoginSFApp', method: 'POST', target: '/', success: false, status_code: 200,
  error_message: 'extractor "c_cshc" found no match: extraction found no match: no match for regex "cshc=(\\w+)"',
  diagnosis: { category: 'correlation', severity: 'error', cause: 'extractor "c_cshc" found no match' },
  snapshot: {
    request_method: 'POST', request_url: 'https://org.example.test/', request_headers: { 'Content-Type': 'application/x-www-form-urlencoded' }, request_body: 'un=user&pw=[REDACTED]',
    response_status: 200, response_headers: { 'Content-Type': 'text/html; charset=UTF-8' }, response_body_state: 'captured',
    response_body: '<!DOCTYPE html><html><head><title>Login | Salesforce</title></head><body><form><input type="password" name="pw"></form><p>cshc missing; CSHC not here</p></body></html>',
  },
  ...overrides,
})

const auraFollower = (overrides: Partial<BrowserReplayEvent> = {}) => event({
  event_id: 'aura-1', action_name: 'ActionBarController.getActions #1', method: 'POST', target: '/aura?r=920', success: false, status_code: 401,
  error_message: 'expected status 200, got 401',
  diagnosis: {
    category: 'assertion', severity: 'error', cause: 'expected status 200, got 401', evidence: 'Protocol status: 401', action: 'Review the assertion.',
    follows_failure_event_id: 'login', follows_failure_action: 'T02_LoginSFApp', follows_reason: 'aura_invalid_session', unresolved_variables: ['c_interactionid'],
  },
  repair_suggestions: [{ id: 'aura-1:fix-first-failure', title: 'Fix the first failure first', description: 'T02_LoginSFApp failed first.', draft_patch_type: 'follows_failure' }],
  snapshot: {
    request_method: 'POST', request_url: 'https://org.example.test/aura?r=920', request_headers: { 'Content-Type': 'application/json' }, request_body: '{"recordId":"{{var.c_interactionid}}"}',
    response_status: 401, response_headers: { 'Content-Type': 'application/json' }, response_body: '{"event":{"descriptor":"markup://aura:invalidSession"}}',
  },
  ...overrides,
})

const loginScript: ScriptDefinition = {
  ...script,
  init: [{ name: 'T02_LoginSFApp', type: 'POST', target: '/', extractors: [{ name: 'c_cshc', source: 'body', type: 'regex', expression: 'cshc=(\\w+)' }] }],
}

describe('RequestResponseInspector follow-on failures and navigation', () => {
  it('leads a follower with the earlier failure, shows its error once, and goes to the first failure', () => {
    const onSelect = vi.fn()
    render(<RunHarness events={[loginEvent(), event(), auraFollower()]} initialIndex={2} onSelect={onSelect} />)
    const verdict = container.querySelector('[aria-label="Verdict"]')!
    expect(verdict.classList.contains('is-follower')).toBe(true)
    expect(verdict.querySelector('.rr-inspector-follows-chip')?.textContent).toBe('Follows an earlier failure')
    expect(verdict.querySelector('.rr-inspector-follows-text')?.textContent).toBe(
      "This request failed after the run's first failure, T02_LoginSFApp. Salesforce rejected the session (aura:invalidSession). The request still contains {{var.c_interactionid}} because that value was never set.",
    )
    expect(occurrences('expected status 200, got 401')).toBe(1)
    expect(container.querySelectorAll('[role="alert"]')).toHaveLength(1)
    // The rule-based diagnosis is not the verdict any more.
    expect(verdict.querySelector('.replay-diagnosis-panel')).toBeNull()
    expect(verdict.textContent).not.toContain('Review the assertion.')
    // A follower opens on its rejected response.
    expect(selectedTab()?.textContent).toBe('Response')

    act(() => button('Go to first failure', verdict)!.click())
    expect(onSelect).toHaveBeenCalledWith(0)
    expect(container.querySelector('.rr-inspector-name')?.textContent).toBe('T02_LoginSFApp')
    expect(container.querySelector('[aria-label="Verdict"]')?.classList.contains('is-follower')).toBe(false)
    // Focus stays in the header's failure navigation instead of dropping to the page.
    expect(document.activeElement?.textContent).toBe('Next failure')
  })

  it('moves the rule-based diagnosis of a follower to Raw', () => {
    render(<RunHarness events={[loginEvent(), auraFollower()]} initialIndex={1} />)
    act(() => tab('Raw')!.click())
    const section = panel().querySelector('.rr-inspector-rule-diagnosis')!
    expect(section.querySelector('h4')?.textContent).toBe('Rule-based diagnosis')
    expect(section.getAttribute('aria-labelledby')).toBe(section.querySelector('h4')!.id)
    expect(section.querySelector('.replay-diagnosis-chip')?.textContent).toBe('Assertion')
    expect(section.textContent).toContain('Protocol status: 401')
    expect(section.textContent).toContain('Review the assertion.')
    // The engine's fix-first-failure suggestion is the verdict's button, not repeated here.
    expect(section.textContent).not.toContain('Fix the first failure first')
    expect(occurrences('expected status 200, got 401')).toBe(1)
  })

  it('disables Go to first failure when that event is no longer retained', () => {
    const onSelect = vi.fn()
    render(<RunHarness events={[event(), auraFollower()]} initialIndex={1} onSelect={onSelect} />)
    const go = button('Go to first failure')!
    expect(go.disabled).toBe(true)
    const note = container.querySelector('.rr-inspector-verdict-note')!
    expect(note.textContent).toBe('First failure is no longer retained')
    expect(go.getAttribute('aria-describedby')).toBe(note.id)
    act(() => go.click())
    expect(onSelect).not.toHaveBeenCalled()
    // Without selection the button is not offered at all.
    render(<Harness event={auraFollower()} />)
    expect(button('Go to first failure')).toBeUndefined()
    expect(container.querySelector('.rr-inspector-follows-chip')).not.toBeNull()
  })

  it('steps through failures with Previous and Next failure, skipping async callbacks', () => {
    const onSelect = vi.fn()
    const events = [loginEvent(), event({ event_id: 'ok' }), event({ event_id: 'async', phase: 'async', success: false, error_message: 'callback failed' }), auraFollower()]
    render(<RunHarness events={events} onSelect={onSelect} />)
    const header = () => container.querySelector('.rr-inspector-header')!
    const group = () => header().querySelector('[role="group"][aria-label="Failures"]')!
    const previous = () => button('Previous failure', group())!
    const next = () => button('Next failure', group())!
    expect(previous().disabled).toBe(true)
    expect(next().disabled).toBe(false)
    next().focus()
    act(() => next().click())
    expect(onSelect).toHaveBeenLastCalledWith(3)
    expect(header().querySelector('.rr-inspector-name')?.textContent).toBe('ActionBarController.getActions #1')
    expect(next().disabled).toBe(true)
    // The pressed button is now disabled: focus moves to the other one.
    expect(document.activeElement).toBe(previous())
    act(() => previous().click())
    expect(onSelect).toHaveBeenLastCalledWith(0)

    // A passed event between failures has both directions; a run with no failures has none.
    render(<RunHarness events={events} initialIndex={1} key="passed" />)
    expect(previous().disabled).toBe(false)
    expect(next().disabled).toBe(false)
    render(<RunHarness events={[event(), event()]} key="none" />)
    expect(previous().disabled).toBe(true)
    expect(next().disabled).toBe(true)
    // Not offered without a way to select.
    render(<Harness event={event()} />)
    expect(container.querySelector('[role="group"][aria-label="Failures"]')).toBeNull()
  })

  it("keeps focus in the inspector when the run's only failure leaves both buttons disabled", () => {
    const only = event({ event_id: 'only', started_at: '2026-10-25T12:00:02Z', success: false, status_code: 500, error_message: 'boom', snapshot: wireSnapshot })
    const events = [event({ event_id: 'a' }), event({ event_id: 'b', started_at: '2026-10-25T12:00:01Z' }), only]
    render(<RunHarness events={events} />)
    const next = () => button('Next failure')!
    const previous = () => button('Previous failure')!
    next().focus()
    act(() => next().click())
    expect(container.querySelector('.rr-inspector-name')?.textContent).toBe('Checkout')
    expect(next().disabled).toBe(true)
    expect(previous().disabled).toBe(true)
    // Neither button can take focus: the selected view's tab does.
    expect(document.activeElement).not.toBe(document.body)
    expect(document.activeElement).toBe(selectedTab())

    // Previous failure from a pass after the only failure lands the same way.
    render(<RunHarness events={[only, event({ event_id: 'after', started_at: '2026-10-25T12:00:03Z' })]} initialIndex={1} key="after" />)
    previous().focus()
    act(() => previous().click())
    expect(previous().disabled).toBe(true)
    expect(next().disabled).toBe(true)
    expect(document.activeElement).toBe(selectedTab())
  })

  it('copies the wire request as cURL', () => {
    const onCopySection = vi.fn(async () => {})
    render(<Harness event={loginEvent()} index={4} onCopySection={onCopySection} />)
    const copy = button('Copy as cURL', container.querySelector('.rr-inspector-header')!)!
    act(() => copy.click())
    expect(onCopySection).toHaveBeenCalledWith('4:curl', [
      'curl', '--globoff', '--request POST', "--url 'https://org.example.test/'",
      "--header 'Content-Type: application/x-www-form-urlencoded'", "--data-raw 'un=user&pw=[REDACTED]'",
    ].join(' \\\n  '))
    render(<Harness event={loginEvent()} index={4} copiedSection="4:curl" />)
    expect(container.querySelector('.rr-inspector-curl')?.textContent).toBe('Copied')
    // A complete copy has nothing to explain.
    expect(container.querySelector('.rr-inspector-curl-gaps')).toBeNull()
    expect(container.querySelector('.rr-inspector-curl')?.hasAttribute('aria-describedby')).toBe(false)
    // Nothing captured on the wire, nothing to copy.
    render(<Harness event={event()} />)
    expect(button('Copy as cURL')).toBeUndefined()
  })

  it('labels a cURL copy of a request retention cut beside the button, and copies the command alone', () => {
    const onCopySection = vi.fn(async () => {})
    const cut = loginEvent()
    cut.snapshot = { ...cut.snapshot!, request_body: 'un=user&pw=...[truncated]', request_headers_omitted: true }
    render(<Harness event={cut} index={4} onCopySection={onCopySection} />)
    const header = container.querySelector('.rr-inspector-header')!
    expect(button('Copy as cURL', header)).toBeUndefined()
    const copy = button('Copy as cURL (incomplete)', header)!
    // Said in visible text (not only a title tooltip, which touch and keyboard users never see), and describing the button.
    expect(copy.title).toBe('')
    const gaps = container.querySelector('.rr-inspector-curl-gaps')!
    expect(gaps.textContent).toBe('cURL copy is incomplete: body truncated by retention limits; some headers omitted or shortened by retention limits. The copy is the captured request only.')
    expect(copy.getAttribute('aria-describedby')).toBe(gaps.id)
    expect(gaps.id).not.toBe('')
    act(() => copy.click())
    const copied = (onCopySection.mock.calls[0] as unknown as [string, string])[1]
    // No "# …" lines, which an interactive zsh would run as commands: the copy pastes as one curl command.
    expect(copied.split('\n')[0]).toBe('curl \\')
    expect(copied.split('\n').some(line => line.trimStart().startsWith('#'))).toBe(false)
    expect(copied.endsWith("--data-raw 'un=user&pw=...[truncated]'")).toBe(true)
    render(<Harness event={cut} index={4} copiedSection="4:curl" />)
    expect(container.querySelector('.rr-inspector-curl')?.textContent).toBe('Copied')
  })

  it('names an HTML page and flags an unexpected login page in Response', () => {
    render(<Harness event={loginEvent()} initialTab="response" />)
    act(() => tab('Response')!.click())
    const summary = panel().querySelector('.rr-inspector-response-summary')!
    expect(summary.querySelector('.rr-inspector-page-title')?.textContent).toBe('HTML page “Login | Salesforce”')
    expect(summary.querySelector('.rr-inspector-login-chip')?.textContent).toBe('Looks like a login page')

    // Expected (a GET to the login page) or not HTML: no flag.
    const get = loginEvent({ success: true, error_message: undefined, diagnosis: undefined })
    get.snapshot = { ...get.snapshot!, request_method: 'GET', request_url: 'https://org.example.test/login' }
    render(<Harness event={get} />)
    expect(panel().querySelector('.rr-inspector-page-title')?.textContent).toBe('HTML page “Login | Salesforce”')
    expect(panel().querySelector('.rr-inspector-login-chip')).toBeNull()
    // A GET to a login host's root (how Salesforce scripts start) expects it too.
    const host = loginEvent({ success: true, error_message: undefined, diagnosis: undefined, event_id: 'host', started_at: '2026-10-25T12:09:00Z' })
    host.snapshot = { ...host.snapshot!, request_method: 'GET', request_url: 'https://login.salesforce.com/' }
    render(<Harness event={host} />)
    expect(panel().querySelector('.rr-inspector-login-chip')).toBeNull()
    // A body cut by retention is read in part, and the summary says so.
    const cut = loginEvent({ success: true, error_message: undefined, diagnosis: undefined, event_id: 'cut', started_at: '2026-10-25T12:10:00Z' })
    cut.snapshot = { ...cut.snapshot!, response_body: '<!DOCTYPE html><title>Login | Salesforce</title><p>first part...[truncated]' }
    render(<Harness event={cut} />)
    expect(panel().querySelector('.rr-inspector-page-title')?.textContent).toBe('HTML page “Login | Salesforce” (partial body)')
    render(<Harness event={event({ snapshot: wireSnapshot })} />)
    expect(panel().querySelector('.rr-inspector-page-title')).toBeNull()
    expect(panel().querySelector('.rr-inspector-login-chip')).toBeNull()
  })

  it('finds text in the response body, counts it, and clears on Escape', () => {
    render(<Harness event={event({ snapshot: { ...wireSnapshot, response_body: '{"token":"abc","Token2":"TOKEN"}' } })} />)
    const find = panel().querySelector<HTMLInputElement>('input[aria-label="Find in response body"]')!
    const count = () => panel().querySelector('.rr-inspector-find-count')!
    expect(count().getAttribute('role')).toBe('status')
    expect(count().textContent).toBe('')
    typeInto(find, 'token')
    expect(count().textContent).toBe('3 matches')
    typeInto(find, 'nothing')
    expect(count().textContent).toBe('No matches')
    typeInto(find, '"abc"')
    expect(count().textContent).toBe('1 match')
    press(find, 'Escape')
    expect(find.value).toBe('')
    expect(count().textContent).toBe('')

    render(<Harness event={event({ snapshot: { ...wireSnapshot, response_body: 'data: one\ndata: two', response_body_state: 'partial' } })} index={1} />)
    typeInto(panel().querySelector<HTMLInputElement>('input[aria-label="Find in response body"]')!, 'data')
    expect(count().textContent).toBe('2 matches (partial body)')
    // Retention cut the body (its state stays "captured"): "No matches" is not
    // proof of absence, and its marker is not a match.
    render(<Harness event={event({ snapshot: { ...wireSnapshot, response_body: '{"token":"abc","rest":"...[truncated]', response_body_state: 'captured' } })} index={3} />)
    const findCut = panel().querySelector<HTMLInputElement>('input[aria-label="Find in response body"]')!
    typeInto(findCut, 'sid=')
    expect(count().textContent).toBe('No matches (partial body)')
    typeInto(findCut, 'truncated')
    expect(count().textContent).toBe('No matches (partial body)')
    typeInto(findCut, 'token')
    expect(count().textContent).toBe('1 match (partial body)')
    // No body, no field.
    render(<Harness event={event({ snapshot: { ...wireSnapshot, response_body: '' } })} index={2} />)
    expect(panel().querySelector('input[aria-label="Find in response body"]')).toBeNull()
  })

  it('draws the timing bar: the main request phases and the rest of the action, or the total', () => {
    render(<Harness event={event({
      duration_ms: 693, snapshot: wireSnapshot,
      network_entries: [{ method: 'POST', url: 'https://api.example.test/checkout', har: { phases: [{ name: 'send', start_ms: 0, end_ms: 0.1 }, { name: 'request_wait', start_ms: 0.1, end_ms: 156.7 }, { name: 'response', start_ms: 156.7, end_ms: 156.9 }] } }],
    } as Partial<BrowserReplayEvent>)} initialTab="timing" />)
    const timing = panel().querySelector('[aria-label="Timing"]')!
    expect(timing).toBe(panel().firstElementChild)
    expect(timing.textContent).toContain('693 ms total')
    const bar = timing.querySelector('[role="img"]')!
    expect(bar.getAttribute('aria-label')).toBe('Total 693 ms: send 0.1 ms, waiting 157 ms, receive 0.2 ms, sub-resources and processing 536 ms')
    expect(Array.from(bar.children).map(node => node.className)).toEqual([
      'rr-inspector-timing-segment is-send', 'rr-inspector-timing-segment is-wait', 'rr-inspector-timing-segment is-receive', 'rr-inspector-timing-segment is-rest',
    ])
    expect(timing.querySelector('.rr-inspector-timing-legend')?.textContent).toContain('Sub-resources and processing')

    render(<Harness event={event()} initialTab="timing" />)
    expect(panel().querySelector('[aria-label="Timing"] [role="img"]')?.getAttribute('aria-label')).toBe('Total 1.25 s')
    expect(panel().querySelector('.rr-inspector-timing-legend')).toBeNull()
  })

  it('withholds the hop phases with the rest of the runtime evidence, leaving the total', () => {
    const phased = event({
      duration_ms: 693, snapshot: wireSnapshot,
      network_entries: [{ method: 'POST', url: 'https://api.example.test/checkout', har: { phases: [{ name: 'send', start_ms: 0, end_ms: 0.1 }, { name: 'request_wait', start_ms: 0.1, end_ms: 156.7 }, { name: 'response', start_ms: 156.7, end_ms: 156.9 }] } }],
    } as Partial<BrowserReplayEvent>)
    render(<Harness event={phased} initialTab="timing" />)
    expect(panel().querySelector('.rr-inspector-timing-legend')).not.toBeNull()
    render(<Harness event={phased} initialTab="timing" runtimeVariablesUnavailable />)
    const timing = panel().querySelector('[aria-label="Timing"]')!
    expect(timing.querySelector('[role="img"]')?.getAttribute('aria-label')).toBe('Total 693 ms')
    expect(Array.from(timing.querySelectorAll('.rr-inspector-timing-segment')).map(node => node.className)).toEqual(['rr-inspector-timing-segment is-total'])
    expect(timing.querySelector('.rr-inspector-timing-legend')).toBeNull()
    expect(timing.textContent).toContain('693 ms total')
    expect(timing.textContent).not.toContain('Waiting')
  })

  it('opens a failed extractor on Correlation with the tester seeded from the definition', () => {
    const onTabChange = vi.fn()
    render(<RunHarness events={[loginEvent(), event({ started_at: '2026-10-25T12:00:05Z', snapshot: wireSnapshot })]} script={loginScript} onTabChange={onTabChange} />)
    expect(selectedTab()?.firstElementChild?.textContent).toBe('Correlation')
    expect(onTabChange).not.toHaveBeenCalled()
    const heading = Array.from(panel().querySelectorAll('h4')).find(node => node.textContent === 'Testing the failing extractor c_cshc')
    expect(heading).toBeDefined()
    const expression = () => panel().querySelector<HTMLInputElement>('[aria-label="Extractor expression"]')!
    expect(expression().value).toBe('cshc=(\\w+)')
    expect(panel().querySelector<HTMLInputElement>('.extractor-tester-name input')!.value).toBe('c_cshc')
    expect(panel().querySelector('.extractor-tester-result')?.textContent).toBe('No matches in this response.')

    // An edit on the same event is kept across a look at another view.
    typeInto(expression(), 'cshc ([a-z]+)')
    const field = expression()
    act(() => tab('Response')!.click())
    act(() => tab('Correlation')!.click())
    expect(expression().value).toBe('cshc ([a-z]+)')
    expect(panel().querySelector('.extractor-tester-count')?.textContent).toBe('1 match')
    // And when the session delivers the same event again as new objects (a poll):
    // the tester is not re-keyed or re-seeded, so the very same field keeps the edit.
    render(<RunHarness events={[loginEvent(), event({ started_at: '2026-10-25T12:00:05Z', snapshot: wireSnapshot })]} script={{ ...loginScript }} onTabChange={onTabChange} />)
    expect(expression()).toBe(field)
    expect(expression().value).toBe('cshc ([a-z]+)')
    expect(Array.from(panel().querySelectorAll('h4')).some(node => node.textContent === 'Testing the failing extractor c_cshc')).toBe(true)
  })

  it('updates the failing extractor in place from the tester, and keeps naming it after the edit', () => {
    const replaceExtractorDefinition = vi.fn((): true | string => true)
    const addExtractorDefinition = vi.fn()
    render(<Harness event={loginEvent()} script={loginScript} replaceExtractorDefinition={replaceExtractorDefinition} addExtractorDefinition={addExtractorDefinition as never} />)
    expect(button('Add as extractor', panel())).toBeUndefined()
    typeInto(panel().querySelector<HTMLInputElement>('[aria-label="Extractor expression"]')!, 'cshc ([a-z]+)')
    act(() => button('Update extractor c_cshc', panel())!.click())
    expect(replaceExtractorDefinition).toHaveBeenCalledWith(expect.objectContaining({ event_id: 'login' }), 'c_cshc', { source: 'body', type: 'regex', expression: 'cshc ([a-z]+)' })
    expect(addExtractorDefinition).not.toHaveBeenCalled()
    // The edited script no longer matches the replay; the tester still names what it is testing.
    render(<Harness event={loginEvent()} script={{ ...loginScript, init: [{ ...loginScript.init![0], extractors: [{ name: 'c_cshc', source: 'body', type: 'regex', expression: 'cshc ([a-z]+)' }] }] }}
      replayMatchesCurrentScript={false} replaceExtractorDefinition={replaceExtractorDefinition} />)
    expect(panel().querySelector('h4')?.textContent).toBe('Testing the failing extractor c_cshc')
    expect(panel().querySelector('.extractor-tester-added')?.textContent).toBe('Updated “c_cshc” on this step. Replay to check it.')
    // Without the replace path (a read-only view) the seeded tester offers no Update.
    render(<Harness event={loginEvent({ started_at: '2026-10-25T12:07:00Z' })} script={loginScript} />)
    expect(button('Update extractor c_cshc', panel())).toBeUndefined()
    expect(button('Add as extractor', panel())).toBeDefined()
  })

  it('seeds the tester only from a replay that matches the script, and names a surface it cannot show', () => {
    render(<Harness event={loginEvent()} script={loginScript} replayMatchesCurrentScript={false} />)
    expect(panel().querySelector('h4')?.textContent).toBe('Test an extractor against this response')
    expect(panel().querySelector<HTMLInputElement>('[aria-label="Extractor expression"]')!.value).toBe('')

    const urlScript: ScriptDefinition = { ...script, init: [{ ...loginScript.init![0], extractors: [{ name: 'c_cshc', source: 'url', type: 'regex', expression: 'cshc=(\\w+)' }] }] }
    render(<Harness event={loginEvent({ started_at: '2026-10-25T12:08:00Z' })} script={urlScript} replaceExtractorDefinition={vi.fn((): true | string => true)} />)
    expect(panel().querySelector('h4')?.textContent).toBe('Test an extractor against this response')
    expect(panel().querySelector('.rr-inspector-tester-note')?.textContent).toBe('The failing extractor c_cshc reads the URL, which the tester cannot show; edit it in the action instead.')
    // Not tried against the body as if it were a body extractor.
    expect(panel().querySelector<HTMLInputElement>('[aria-label="Extractor expression"]')!.value).toBe('')
    expect(button('Update extractor c_cshc', panel())).toBeUndefined()
  })

  it('says when the failing extractor applies transforms or a response scope the tester cannot', () => {
    const scoped: ScriptDefinition = { ...script, init: [{ ...loginScript.init![0], extractors: [{
      name: 'c_cshc', source: 'body', type: 'regex', expression: 'cshc=(\\w+)',
      dfes: ['JsonXml'], scope: { redirects: 'include', content_type: 'text/html*' },
    }] }] }
    render(<Harness event={loginEvent({ started_at: '2026-10-25T12:09:00Z' })} script={scoped} replaceExtractorDefinition={vi.fn((): true | string => true)} />)
    // Still seeded (Update keeps the transforms and scope), but it does not claim to test the extractor.
    expect(panel().querySelector('h4')?.textContent).toBe('Trying the expression of the failing extractor c_cshc')
    expect(panel().querySelector('.rr-inspector-tester-note')?.textContent).toBe(
      'The tester tries the expression on this step’s final response only. It cannot apply its data-format transforms (JsonXml), redirect hops and its Content-Type filter, so its matches can differ from the replay’s. Update keeps those settings.')
    expect(panel().querySelector<HTMLInputElement>('[aria-label="Extractor expression"]')!.value).toBe('cshc=(\\w+)')
    expect(button('Update extractor c_cshc', panel())).toBeDefined()
    // An empty scope and no DFEs are the final response the tester reads.
    const plain: ScriptDefinition = { ...script, init: [{ ...loginScript.init![0], extractors: [{ name: 'c_cshc', source: 'body', type: 'regex', expression: 'cshc=(\\w+)', dfes: [], scope: { redirects: 'exclude' } }] }] }
    render(<Harness event={loginEvent({ started_at: '2026-10-25T12:10:00Z' })} script={plain} />)
    expect(panel().querySelector('h4')?.textContent).toBe('Testing the failing extractor c_cshc')
    expect(panel().querySelector('.rr-inspector-tester-note')).toBeNull()
  })

  it('seeds the tester when the replay matches the script again on the same event, unless the plain tester was edited', () => {
    const replaceExtractorDefinition = vi.fn((): true | string => true)
    const heading = () => panel().querySelector('h4')?.textContent
    const expression = () => panel().querySelector<HTMLInputElement>('[aria-label="Extractor expression"]')!
    // Unapplied JSON edits: the replay does not match, so the tester starts plain.
    render(<Harness event={loginEvent()} script={loginScript} replayMatchesCurrentScript={false} replaceExtractorDefinition={replaceExtractorDefinition} />)
    expect(heading()).toBe('Test an extractor against this response')
    expect(expression().value).toBe('')
    // Applied or discarded: the same event now seeds it, with Update.
    render(<Harness event={loginEvent()} script={loginScript} replayMatchesCurrentScript replaceExtractorDefinition={replaceExtractorDefinition} />)
    expect(heading()).toBe('Testing the failing extractor c_cshc')
    expect(expression().value).toBe('cshc=(\\w+)')
    expect(button('Update extractor c_cshc', panel())).toBeDefined()
    // Once seeded it stays: an edit survives the replay going out of step and back.
    typeInto(expression(), 'cshc ([a-z]+)')
    render(<Harness event={loginEvent()} script={loginScript} replayMatchesCurrentScript={false} replaceExtractorDefinition={replaceExtractorDefinition} />)
    render(<Harness event={loginEvent()} script={loginScript} replayMatchesCurrentScript replaceExtractorDefinition={replaceExtractorDefinition} />)
    expect(heading()).toBe('Testing the failing extractor c_cshc')
    expect(expression().value).toBe('cshc ([a-z]+)')

    // A plain tester the user already typed into is not replaced by a late seed.
    const later = loginEvent({ started_at: '2026-10-25T12:09:00Z' })
    render(<Harness event={later} script={loginScript} replayMatchesCurrentScript={false} replaceExtractorDefinition={replaceExtractorDefinition} />)
    expect(heading()).toBe('Test an extractor against this response')
    typeInto(expression(), 'my own (\\w+)')
    render(<Harness event={later} script={loginScript} replayMatchesCurrentScript replaceExtractorDefinition={replaceExtractorDefinition} />)
    expect(heading()).toBe('Test an extractor against this response')
    expect(expression().value).toBe('my own (\\w+)')
    expect(button('Update extractor c_cshc', panel())).toBeUndefined()
  })

  it('says why Update could not change the step', () => {
    const replaceExtractorDefinition = vi.fn((): true | string => 'apply or discard the JSON edits first.')
    render(<Harness event={loginEvent()} script={loginScript} replaceExtractorDefinition={replaceExtractorDefinition} />)
    act(() => button('Update extractor c_cshc', panel())!.click())
    expect(panel().querySelector('.extractor-tester-actions .extractor-tester-error')?.textContent).toBe('Could not update “c_cshc”: apply or discard the JSON edits first.')
  })

  it('re-seeds the tester per event and leaves other events with the plain tester', () => {
    const other = event({ started_at: '2026-10-25T12:00:05Z', snapshot: wireSnapshot })
    render(<Harness event={loginEvent()} script={loginScript} initialTab="correlation" />)
    const expression = () => panel().querySelector<HTMLInputElement>('[aria-label="Extractor expression"]')!
    typeInto(expression(), 'edited')
    render(<Harness event={other} index={1} script={loginScript} initialTab="correlation" />)
    expect(panel().querySelector('h4')?.textContent).toBe('Test an extractor against this response')
    expect(expression().value).toBe('')
    render(<Harness event={loginEvent()} index={0} script={loginScript} initialTab="correlation" />)
    expect(expression().value).toBe('cshc=(\\w+)')
    // Without a definition by that name the tester is not seeded.
    render(<Harness event={loginEvent({ started_at: '2026-10-25T12:09:00Z' })} script={script} initialTab="correlation" />)
    expect(panel().querySelector('h4')?.textContent).toBe('Test an extractor against this response')
    expect(expression().value).toBe('')
  })

  it('lists the values never set in this run in Correlation', () => {
    render(<Harness event={event({ success: false, status_code: 500, error_message: 'boom', diagnosis: { category: 'runtime', severity: 'error', cause: 'boom', unresolved_variables: ['c_interactionid', 'c_token'] } })} />)
    expect(selectedTab()?.firstElementChild?.textContent).toBe('Correlation')
    const correlation = panel().querySelector('.rr-correlation-table')
    for (const name of ['c_interactionid', 'c_token']) {
      const cells = rowNamed(correlation, name)
      expect(cells?.[0]).toBe('Reads')
      expect(cells?.[3]).toBe(`Left as {{var.${name}}}`)
      expect(cells?.[4]).toContain('Never set this run')
    }
    expect(panel().textContent).not.toContain('No extractions or variable changes were recorded for this action.')
  })

  it('picks the default view per failed event, honours a click until the event changes, and keeps the pick for passes', () => {
    const onTabChange = vi.fn()
    const noResponse = event({ event_id: 'down', started_at: '2026-10-25T12:00:02Z', success: false, status_code: undefined, error_message: 'connection refused' })
    const passed = event({ event_id: 'ok', started_at: '2026-10-25T12:00:03Z', snapshot: wireSnapshot })
    const events = [loginEvent(), noResponse, auraFollower({ started_at: '2026-10-25T12:00:04Z' }), passed]
    render(<RunHarness events={events} script={loginScript} onTabChange={onTabChange} />)
    expect(selectedTab()?.firstElementChild?.textContent).toBe('Correlation')
    act(() => button('Next failure')!.click())
    expect(selectedTab()?.firstElementChild?.textContent).toBe('Timing & transport')
    // A click on this event is honoured.
    act(() => tab('Request')!.click())
    expect(selectedTab()?.textContent).toBe('Request')
    expect(onTabChange).toHaveBeenCalledTimes(1)
    act(() => button('Next failure')!.click())
    expect(selectedTab()?.textContent).toBe('Response')
    // The click lasted until the event changed: coming back opens the default again.
    act(() => button('Previous failure')!.click())
    expect(container.querySelector('.rr-inspector-name')?.textContent).toBe('Checkout')
    expect(selectedTab()?.firstElementChild?.textContent).toBe('Timing & transport')
    act(() => button('Previous failure')!.click())
    expect(selectedTab()?.firstElementChild?.textContent).toBe('Correlation')
    // Defaults never overwrite the pick: a passed event shows it.
    expect(onTabChange).toHaveBeenCalledTimes(1)
    render(<RunHarness events={events} script={loginScript} initialIndex={3} initialTab="request" key="passed" />)
    expect(selectedTab()?.textContent).toBe('Request')
  })

  it('moves focus with the default view only when focus was on a tab', () => {
    const passed = event({ snapshot: wireSnapshot })
    render(<Harness event={passed} initialTab="request" />)
    tab('Request')!.focus()
    render(<Harness event={loginEvent()} index={1} script={loginScript} initialTab="request" />)
    expect(selectedTab()?.firstElementChild?.textContent).toBe('Correlation')
    expect(document.activeElement).toBe(selectedTab())
    const outside = document.createElement('button')
    document.body.appendChild(outside)
    outside.focus()
    render(<Harness event={passed} index={0} initialTab="request" />)
    expect(selectedTab()?.textContent).toBe('Request')
    expect(document.activeElement).toBe(outside)
    outside.remove()
  })
})

describe('RequestResponseInspector tables', () => {
  it('lists key response headers first, cuts long values to one line and shows all on request', () => {
    const csp = `default-src 'self' ${'https://cdn.example.test '.repeat(80)}`.trim()
    const onCopySection = vi.fn(async () => {})
    render(<Harness
      event={event({ snapshot: { ...wireSnapshot, response_headers: { 'Content-Type': 'text/html', 'Content-Security-Policy': csp, 'X-Request-Id': 'r-1' } } })}
      onCopySection={onCopySection}
    />)
    const headers = namedTable('Response headers')!
    expect(headers.querySelector('caption, [scope="col"]')).not.toBeNull()
    expect(Array.from(headers.querySelectorAll('thead th')).map(cell => cell.textContent)).toEqual(['Header', 'Value', 'Copy'])
    // Only the key header until Show all; the toggle says how many there are.
    expect(rowCells(headers).map(cells => cells[0])).toEqual(['Content-Type'])
    const toggle = button('Show all 3', panel())!
    expect(toggle.getAttribute('aria-expanded')).toBe('false')
    act(() => toggle.click())
    expect(toggle.getAttribute('aria-expanded')).toBe('true')
    expect(toggle.textContent).toBe('Show key headers')
    expect(rowCells(namedTable('Response headers')).map(cells => cells[0])).toEqual(['Content-Type', 'Content-Security-Policy', 'X-Request-Id'])
    // The long value keeps its whole text in the tooltip and behind Copy.
    const cspCell = Array.from(namedTable('Response headers')!.querySelectorAll('tbody tr'))
      .find(row => row.querySelector('th')?.textContent === 'Content-Security-Policy')!
    expect(cspCell.querySelector('.inspector-table-value')?.getAttribute('title')).toBe(csp)
    act(() => cspCell.querySelector<HTMLButtonElement>('button')!.click())
    expect(onCopySection).toHaveBeenCalledWith('0:response-headers:1', csp)
  })

  it('says where a scripted header comes from: a variable, or Unknown once the replay no longer matches the script', () => {
    const tokenScript: ScriptDefinition = {
      ...script,
      actions: [{ name: 'Checkout', type: 'POST', target: '/checkout', headers: { 'X-Token': '{{var.token}}', 'X-Fixed': 'yes' } }],
    }
    const scripted = event({ headers: { 'X-Token': 'abc123', 'X-Fixed': 'yes' } })
    render(<Harness event={scripted} script={tokenScript} initialTab="request" />)
    const headers = namedTable('Request headers as scripted')!
    expect(Array.from(headers.querySelectorAll('thead th')).map(cell => cell.textContent)).toContain('Comes from')
    expect(rowNamed(headers, 'X-Token')?.slice(0, 3)).toEqual(['X-Token', 'abc123', 'Variable token'])
    expect(rowNamed(headers, 'X-Fixed')?.slice(0, 3)).toEqual(['X-Fixed', 'yes', 'Literal'])

    // Once the replay no longer matches the script: no Comes from column, one quiet note.
    render(<Harness event={scripted} script={tokenScript} replayMatchesCurrentScript={false} initialTab="request" />)
    const stale = namedTable('Request headers as scripted')!
    expect(Array.from(stale.querySelectorAll('thead th')).map(cell => cell.textContent)).toEqual(['Header', 'Value', 'Copy'])
    expect(rowNamed(stale, 'X-Token')?.slice(0, 2)).toEqual(['X-Token', 'abc123'])
    expect(panel().textContent).toContain('Sources are unavailable because the script changed since this run.')
    expect(panel().textContent).not.toContain('Unknown')
    // Said once for the view, not by each table.
    expect(panel().querySelectorAll('.rr-inspector-sources-note')).toHaveLength(1)
    expect(panel().querySelectorAll('.inspector-table-note')).toHaveLength(0)

    // The replay matches the script but its step does not resolve: a different reason.
    render(<Harness event={event({ action_index: 7, headers: { 'X-Token': 'abc123' } })} script={tokenScript} initialTab="request" />)
    expect(panel().querySelector('.rr-inspector-sources-note')?.textContent).toBe('Sources are unknown for this step.')
  })

  it('masks a password in a form body and never shows the raw body beside the table', () => {
    const onCopySection = vi.fn(async () => {})
    const form = event({
      method: 'POST',
      snapshot: {
        ...wireSnapshot,
        request_headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        request_body: 'un=alice%40example.test&pw=hunter2&remember=on',
      },
    })
    render(<Harness event={form} initialTab="request" onCopySection={onCopySection} />)
    const fields = namedTable('Form fields sent on the wire')!
    expect(rowNamed(fields, 'un')?.slice(0, 2)).toEqual(['un', 'alice@example.test'])
    const pwRow = Array.from(fields.querySelectorAll('tbody tr')).find(row => row.querySelector('th')?.textContent === 'pw')!
    expect(pwRow.querySelector('.inspector-table-value [aria-hidden="true"]')?.textContent).toBe('••••••••')
    expect(pwRow.querySelector('.inspector-table-value .sr-only')?.textContent).toBe('Masked')
    // The note does not imply protection: the full-request copies keep the field.
    expect(panel().textContent).toContain('1 field is hidden here. Copy full request and Copy as cURL include it.')
    expect(panel().textContent).not.toContain('hunter2')
    expect(panel().innerHTML).not.toContain('hunter2')
    // A masked field offers no Copy at all.
    expect(pwRow.querySelector('button')).toBeNull()
    expect(onCopySection).not.toHaveBeenCalled()
  })

  it('masks As scripted too when only a script default header makes the body a form', () => {
    // The resolved step headers never carry a default_headers Content-Type.
    const defaultForm: ScriptDefinition = { ...script, config: { ...script.config, http: { default_headers: { 'Content-Type': 'application/x-www-form-urlencoded' } } } }
    const form = event({
      method: 'POST',
      headers: { 'X-Step': '1' },
      body: 'un=jane&pw=hunter2',
      snapshot: { ...wireSnapshot, request_headers: { 'Content-Type': 'application/x-www-form-urlencoded' }, request_body: 'un=jane&pw=hunter2' },
    })
    render(<Harness event={form} script={defaultForm} initialTab="request" />)
    act(() => button('As scripted', panel())!.click())
    const fields = namedTable('Form fields as scripted')!
    expect(rowNamed(fields, 'un')?.slice(0, 2)).toEqual(['un', 'jane'])
    expect(panel().innerHTML).not.toContain('hunter2')
    // Even with neither the wire nor the step headers naming the form.
    render(<Harness event={event({ headers: { 'X-Step': '1' }, body: 'un=jane&pw=hunter2' })} script={defaultForm} initialTab="request" />)
    expect(namedTable('Form fields as scripted')).not.toBeNull()
    expect(panel().innerHTML).not.toContain('hunter2')
  })

  it('names in the masked-field note only the copies the view offers', () => {
    const form = event({
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: 'un=jane&pw=hunter2',
      snapshot: { ...wireSnapshot, request_headers: { 'Content-Type': 'application/x-www-form-urlencoded' }, request_body: 'un=jane&pw=hunter2' },
    })
    render(<Harness event={form} initialTab="request" />)
    expect(button('Copy as cURL')).toBeDefined()
    expect(panel().textContent).toContain('1 field is hidden here. Copy full request and Copy as cURL include it.')
    // As scripted has no Copy full request: only the header's Copy as cURL is named.
    act(() => button('As scripted', panel())!.click())
    expect(button('Copy full request', panel())).toBeUndefined()
    expect(panel().textContent).toContain('1 field is hidden here. Copy as cURL includes it.')
    // Without a captured request there is no copy to name.
    render(<Harness event={event({ headers: { 'Content-Type': 'application/x-www-form-urlencoded' }, body: 'pw=hunter2' })} initialTab="request" />)
    expect(panel().querySelector('.inspector-table-note')?.textContent).toBe('1 field is hidden here.')
  })

  it('keeps a JSON body whose step overrides a form script default', () => {
    const defaultForm: ScriptDefinition = {
      ...script,
      config: { ...script.config, http: { default_headers: { 'Content-Type': 'application/x-www-form-urlencoded' } } },
      actions: [{ name: 'Checkout', type: 'POST', target: '/checkout', headers: { 'Content-Type': 'application/json' } }],
    }
    const json = event({ method: 'POST', headers: { 'X-Step': '1' }, body: '{"user":"jane","token":"abc"}' })
    render(<Harness event={json} script={defaultForm} initialTab="request" />)
    expect(namedTable('Form fields as scripted')).toBeNull()
    expect(panel().textContent).toContain('"user"')
  })

  it('masks As scripted when only the wire headers name the form', () => {
    const form = event({
      method: 'POST',
      headers: { 'X-Step': '1' },
      body: 'un=jane&pw=hunter2',
      snapshot: { ...wireSnapshot, request_headers: { 'Content-Type': 'application/x-www-form-urlencoded' }, request_body: 'un=jane&pw=hunter2' },
    })
    render(<Harness event={form} replayMatchesCurrentScript={false} initialTab="request" />)
    act(() => button('As scripted', panel())!.click())
    expect(namedTable('Form fields as scripted')).not.toBeNull()
    expect(panel().innerHTML).not.toContain('hunter2')
  })

  it('names failures on the tab badges in words, in the failure style', () => {
    render(<Harness
      event={event({
        success: false, status_code: 500, error_message: 'extractor "token" found no match',
        diagnosis: { category: 'correlation', severity: 'error', cause: 'x', unresolved_variables: ['csrf', 'sid'] },
      })}
      initialTab="response"
    />)
    expect(tab('Request')?.textContent).toBe('Request 2 unresolved')
    expect(tab('Request')?.querySelector('.rr-inspector-tab-badge')?.classList.contains('is-failing')).toBe(true)
    // One failed extractor and two reads never set.
    expect(tab('Correlation')?.textContent).toBe('Correlation 1 failed · 2 unset')
    expect(tab('Response')?.querySelector('.rr-inspector-tab-badge')).toBeNull()
  })

  it('lists an applied extraction with its extractor and later uses, and an unresolved read', () => {
    const correlationScript: ScriptDefinition = {
      ...script,
      actions: [
        { name: 'Checkout', type: 'POST', target: '/checkout', body: '{"order":"{{var.c_order}}"}', extractors: [{ name: 'token', source: 'body', type: 'regex', expression: '"token":"([^"]+)"' }] },
        { name: 'Pay', type: 'POST', target: '/pay', headers: { 'X-Token': '{{var.token}}' } },
      ],
    }
    render(<Harness
      event={event({
        success: false, status_code: 500, error_message: 'boom',
        diagnosis: { category: 'runtime', severity: 'error', cause: 'boom', unresolved_variables: ['c_order'] },
        extractions: { token: 'abc' },
      })}
      script={correlationScript}
      initialTab="correlation"
    />)
    const correlation = panel().querySelector('.rr-correlation-table')!
    expect(correlation.querySelector('caption')?.textContent).toBe('Values this action extracts, reads and changes')
    const extracted = rowNamed(correlation, 'token')!
    expect(extracted.slice(0, 4)).toEqual(['Extracts', 'token', 'body · regex', 'abc'])
    expect(extracted[4]).toContain('Extracted')
    expect(extracted[4]).toContain('Used by 1 later step')
    const unset = rowNamed(correlation, 'c_order')!
    expect(unset.slice(0, 4)).toEqual(['Reads', 'c_order', 'Body', 'Left as {{var.c_order}}'])
    expect(unset[4]).toContain('Never set this run')
  })

  it('merges a captured resource with its network entry, filters failed requests and opens a row', () => {
    render(<Harness
      event={event({
        network_entries: [{ method: 'GET', url: '/a.css', status_code: 200, duration_ms: 12, resource_type: 'stylesheet' }],
        snapshot: {
          ...wireSnapshot,
          resources: [
            { request_url: '/a.css', response_status: 200, response_body_state: 'captured', response_body: 'body{}', response_headers: { 'Content-Type': 'text/css' } },
            { request_url: '/b.js', response_status: 404, response_body_state: 'not_captured', response_headers: { 'Content-Type': 'application/javascript' } },
          ],
        },
      } as Partial<BrowserReplayEvent>)}
      initialTab="network"
    />)
    // Two requests, not three: the stylesheet's entry and resource are one row; the 404 is named as failed.
    expect(tab('Network')?.textContent).toBe('Network 2 · 1 failed')
    expect(tab('Network')?.querySelector('.rr-inspector-tab-badge')?.classList.contains('is-failing')).toBe(true)
    const section = panel().querySelector('[aria-label="Network requests"]')!
    const table = () => section.querySelector('table')!
    expect(table().querySelector('caption')?.textContent).toBe('2 requests')
    expect(rowCells(table()).map(cells => cells.slice(0, 6))).toEqual([
      ['1', 'GET /a.css', 'style', '200', '12 ms', 'Kept'],
      ['—', 'GET /b.js', 'script', '404', 'Unknown', 'Not kept'],
    ])
    expect(section.textContent).toContain('1 body kept · 1 not kept')

    const filters = section.querySelector('[role="group"][aria-label="Network filter"]')!
    const failedFilter = Array.from(filters.querySelectorAll('button')).find(node => node.textContent?.startsWith('Failed'))!
    expect(failedFilter.getAttribute('aria-pressed')).toBe('false')
    act(() => failedFilter.click())
    expect(failedFilter.getAttribute('aria-pressed')).toBe('true')
    expect(table().querySelector('caption')?.textContent).toBe('1 of 2 requests shown')
    expect(rowCells(table()).map(cells => cells[1])).toEqual(['GET /b.js'])

    act(() => Array.from(filters.querySelectorAll('button')).find(node => node.textContent?.startsWith('All'))!.click())
    const rowButton = table().querySelector<HTMLButtonElement>('tbody tr button')!
    expect(rowButton.getAttribute('aria-expanded')).toBe('false')
    act(() => rowButton.click())
    expect(rowButton.getAttribute('aria-expanded')).toBe('true')
    const detail = section.querySelector<HTMLElement>(`[id="${rowButton.getAttribute('aria-controls')}"]`)!
    expect(detail.getAttribute('aria-labelledby')).toBe(rowButton.id)
    // The detail opens right under its row, inside the table.
    expect(detail.closest('tr')?.previousElementSibling).toBe(rowButton.closest('tr'))
    expect(detail.textContent).toContain('/a.css')
    expect(detail.textContent).toContain('Response body · Kept')
    expect(detail.textContent).toContain('body{}')
  })
})

describe('RequestResponseInspector header layout', () => {
  // jsdom has no layout, so the rules are read. The header's items do not
  // shrink (`.rr-inspector-header > *` is flex 0 0 auto); the tools group must,
  // or it stays one max-content line (about 530px of buttons) and overflows a
  // narrow pane instead of wrapping its buttons.
  const rule = (css: string, selector: string) => {
    const escaped = selector.replace(/[.*+?^${}()|[\]\\>]/g, '\\$&')
    return css.match(new RegExp(`(?:^|\\n)${escaped} \\{([^}]*)\\}`))?.[1] ?? ''
  }

  it('lets the header tools shrink to the pane and wrap their buttons', async () => {
    // @ts-expect-error node:fs is available in the Vitest runtime.
    const { readFileSync } = await import('node:fs')
    const css = readFileSync('src/components/RequestResponseInspector.css', 'utf8') as string
    expect(rule(css, '.rr-inspector-header > *')).toContain('flex: 0 0 auto;')
    // A more specific rule than `> *`, so it wins for the tools group.
    const tools = rule(css, '.rr-inspector-header .rr-inspector-header-tools')
    expect(tools).toMatch(/\bflex: 0 1 auto;/)
    expect(tools).toMatch(/\bflex-wrap: wrap;/)
    expect(tools).toMatch(/\bmin-width: 0;/)
    expect(tools).toMatch(/\bmax-width: 100%;/)
    expect(rule(css, '.rr-inspector-failure-nav')).toMatch(/\bflex-wrap: wrap;/)
  })
})
