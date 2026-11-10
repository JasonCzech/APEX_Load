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
    expect(panel().textContent).toContain('X-Wire: sent')
    expect(panel().textContent).toContain('wire-body')
    expect(panel().textContent).not.toContain('Resolved Headers')
    act(() => button('Copy full request', panel())!.click())
    expect(onCopySection).toHaveBeenCalledWith('0:request', expect.stringContaining('wire-body'))

    act(() => scripted.click())
    expect(wire.getAttribute('aria-pressed')).toBe('false')
    expect(scripted.getAttribute('aria-pressed')).toBe('true')
    expect(panel().textContent).toContain('Resolved Headers')
    expect(panel().textContent).toContain('X-Scripted: resolved')
    expect(panel().textContent).toContain('Resolved Body')
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
    expect(panel().textContent).not.toContain('Resolved Headers')

    // A new run of the same action starts on the wire too.
    act(() => requestView('As scripted').click())
    expect(requestView('As scripted').getAttribute('aria-pressed')).toBe('true')
    render(<Harness event={{ ...second, started_at: '2026-10-25T12:05:00Z' }} index={1} initialTab="request" />)
    expect(requestView('Sent on the wire').getAttribute('aria-pressed')).toBe('true')
  })

  it('shows the only request view there is, without the toggle', () => {
    render(<Harness event={event({ headers: { 'X-Scripted': 'resolved' }, body: 'scripted-body' })} initialTab="request" />)
    expect(container.querySelector('[role="group"][aria-label="Request view"]')).toBeNull()
    expect(panel().textContent).toContain('Resolved Headers')
    expect(panel().textContent).toContain('scripted-body')

    render(<Harness event={event({ snapshot: wireSnapshot })} initialTab="request" />)
    expect(container.querySelector('[role="group"][aria-label="Request view"]')).toBeNull()
    expect(panel().textContent).toContain('wire-body')
    expect(panel().textContent).not.toContain('Resolved Headers')

    // With nothing captured, the resolved headers block still renders.
    render(<Harness event={event()} initialTab="request" />)
    expect(container.querySelector('[role="group"][aria-label="Request view"]')).toBeNull()
    expect(panel().textContent).toContain('Resolved Headers')
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
    expect(panel().querySelector('[aria-label="Captured resources"]')?.textContent).toContain('/a.css')
    act(() => tab('Raw')!.click())
    expect(panel().textContent).toContain('2 protocol evidence items were omitted or shortened by retention limits.')
    expect(panel().querySelector('.replay-code-block .replay-detail-label')?.textContent).toMatch(/^Protocol Evidence/)
    expect(panel().querySelector('details')).toBeNull()

    // Browser artifacts alone also give a Network view; network entries set its badge.
    render(<Harness event={event({ network_entries: [{ method: 'GET', url: '/api/one', status_code: 200 }], console_entries: [{ level: 'info', text: 'ready' }] } as Partial<BrowserReplayEvent>)} />)
    expect(tab('Network')?.textContent).toBe('Network 1')
    act(() => tab('Network')!.click())
    expect(panel().textContent).toContain('Network entries')
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
    expect(panel().querySelector('[aria-label="Captured resources"]')).not.toBeNull()

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
    expect(panel().textContent).toContain('Variable Changes')
    expect(panel().textContent).toContain('old→new')
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
