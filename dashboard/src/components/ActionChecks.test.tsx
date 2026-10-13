// @vitest-environment jsdom
import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import type { BrowserReplayEvent } from '../types/replay'
import type { ScriptActionDefinition as Action } from '../types/scripts'
import ActionChecks from './ActionChecks'
import { replayResolver, type ChecksReplay } from './actionChecksModel'
import { ReferenceFieldContext } from './referenceFieldContext'
import { buildReferenceScope } from './referenceScopeModel'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

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

const replay: ChecksReplay = {
  event: { phase: 'action', action_name: 'Login', action_type: 'POST', target: '/login', success: true, started_at: '', completed_at: '', duration_ms: 120, status_code: 302, snapshot: { response_status: 302, response_body: '{"token":"abc123"}', response_headers: { Location: '/home' } } } as BrowserReplayEvent,
  stale: false,
  resolve: replayResolver({ expected: '302' }),
}

let current: Action
function Harness({ initial, protocol = 'http', browser = false, withReplay = true, unavailable }: { initial: Action; protocol?: string; browser?: boolean; withReplay?: boolean; unavailable?: string }) {
  const [action, setAction] = useState(initial)
  current = action
  return <ActionChecks action={action} index={0} protocol={protocol} isBrowserScript={browser} replay={withReplay ? replay : null} unavailable={unavailable}
    onAddExtractor={() => setAction(prev => ({ ...prev, extractors: [...(prev.extractors || []), { name: 'value', source: 'body', type: 'boundary', expression: '' }] }))}
    onUpdateExtractor={(_action, at, field, value) => setAction(prev => ({ ...prev, extractors: prev.extractors!.map((extractor, index) => index === at ? { ...extractor, [field]: value } : extractor) }))}
    onRemoveExtractor={(_action, at) => setAction(prev => ({ ...prev, extractors: prev.extractors!.filter((_extractor, index) => index !== at) }))}
    onAddAssertion={(_action, assertion) => setAction(prev => ({ ...prev, assertions: [...(prev.assertions || []), assertion || { type: 'status_code', expected: 200 }] }))}
    onUpdateAssertion={(_action, at, field, value) => setAction(prev => ({ ...prev, assertions: prev.assertions!.map((assertion, index) => index === at ? { ...assertion, [field]: value } : assertion) }))}
    onRemoveAssertion={(_action, at) => setAction(prev => ({ ...prev, assertions: prev.assertions!.filter((_assertion, index) => index !== at) }))} />
}

const render = (props: Parameters<typeof Harness>[0]) => act(async () => root.render(<Harness {...props} />))
const byLabel = <T extends HTMLElement>(label: string) => container.querySelector<T>(`[aria-label="${label}"]`)!
const group = (label: string) => byLabel<HTMLElement>(label)
async function type(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!
  await act(async () => { setter.call(input, value); input.dispatchEvent(new Event('input', { bubbles: true })) })
}
async function choose(select: HTMLSelectElement, value: string) {
  await act(async () => { select.value = value; select.dispatchEvent(new Event('change', { bubbles: true })) })
}
const addMenu = () => [...container.querySelectorAll('label')].find(label => label.textContent?.startsWith('Add a check'))!.querySelector('select')!
const fieldInput = (label: string) => [...container.querySelectorAll('label')].find(node => node.firstChild?.textContent === label)!.querySelector('input')!

describe('ActionChecks', () => {
  it('adds a status check from the replay and shows its result without a new replay', async () => {
    await render({ initial: { name: 'Login', type: 'POST', target: '/login' } })
    expect(container.textContent).toContain('Results are from the last replay of this step.')
    const menu = addMenu()
    expect([...menu.querySelectorAll('option')].map(option => option.textContent).slice(0, 6)).toEqual(['Choose what to check…', 'Status is …', 'Response contains …', 'Response does not contain …', 'Header contains …', 'Finishes within … ms'])
    expect(menu.querySelector('optgroup')!.label).toBe('More checks')
    await choose(menu, 'status_code')
    expect(current.assertions).toEqual([{ type: 'status_code', expected: 302 }])
    expect(menu.value).toBe('')
    const row = group('Check 1: Status is …')
    expect(row.querySelector('.action-check-result')!.textContent).toBe('Passed')
    await type(byLabel<HTMLInputElement>('Check 1 status'), '200')
    expect(current.assertions![0].expected).toBe(200)
    expect(row.querySelector('.action-check-result.failed')!.textContent).toBe('Failed: got 302')
    // A reference stays as written and resolves from the replay's values.
    await type(byLabel<HTMLInputElement>('Check 1 status'), '{{var.expected}}')
    expect(current.assertions![0].expected).toBe('{{var.expected}}')
    expect(row.querySelector('.action-check-result')!.textContent).toBe('Passed')
  })

  it('checks response text as it is typed', async () => {
    await render({ initial: { name: 'Login', type: 'POST', target: '/login' } })
    await choose(addMenu(), 'text_contains')
    const row = group('Check 1: Response contains …')
    expect(row.querySelector('.action-check-result')!.textContent).toBe('Enter the text to look for')
    await type(byLabel<HTMLInputElement>('Check 1 text'), 'abc123')
    expect(current.assertions).toEqual([{ type: 'text_contains', value: 'abc123' }])
    expect(row.querySelector('.action-check-result.passed')).not.toBeNull()
    await choose(row.querySelector<HTMLSelectElement>('select')!, 'headers')
    expect(current.assertions![0].source).toBe('headers')
    expect(row.querySelector('.action-check-result.failed')).not.toBeNull()
    await act(async () => byLabel<HTMLButtonElement>('Remove check 1').click())
    expect(current.assertions).toEqual([])
  })

  it('edits a saved value’s boundaries as two fields and shows what it saves', async () => {
    await render({ initial: { name: 'Login', type: 'POST', target: '/login' } })
    await act(async () => [...container.querySelectorAll('button')].find(button => button.textContent === 'Save a value for later steps')!.click())
    const row = group('Saved value value')
    await type(fieldInput('Left boundary'), '"token":"')
    await type(fieldInput('Right boundary'), '"')
    expect(current.extractors![0].expression).toBe('"token":"|||"')
    expect(row.querySelector('.action-check-result')!.textContent).toBe('Saves “abc123”')
    await type(fieldInput('Save as'), 'token')
    expect(group('Saved value token')).not.toBeNull()
    await act(async () => byLabel<HTMLButtonElement>('Remove saved value token').click())
    expect(current.extractors).toEqual([])
  })

  it('keeps a configured boundary as one expression', async () => {
    await render({ initial: { name: 'Login', type: 'POST', target: '/login', extractors: [{ name: 't', source: 'body', type: 'boundary', expression: '__apexload_boundary_config__:{"left":"a","right":"b"}' }] } })
    expect(fieldInput('Expression').value).toContain('__apexload_boundary_config__')
  })

  it('offers no saved values in a browser script and words its checks for pages', async () => {
    await render({ initial: { name: 'Home', type: 'browser_wait', target: '#x' }, protocol: 'browser', browser: true })
    expect(container.textContent).not.toContain('Saved values')
    expect([...addMenu().querySelectorAll('option')].map(option => option.textContent)).toContain('Page title is …')
  })

  it('shows a page title kept in expected, and moves it to value when edited', async () => {
    await render({ initial: { name: 'Home', type: 'browser_wait', target: '#x', assertions: [{ type: 'page_title', expected: 'Welcome' }] }, protocol: 'browser', browser: true })
    expect(byLabel<HTMLInputElement>('Check 1 title').value).toBe('Welcome')
    await type(byLabel<HTMLInputElement>('Check 1 title'), 'Hello')
    expect(current.assertions).toEqual([{ type: 'page_title', value: 'Hello', expected: undefined }])
  })

  it('marks the checks and each field so the debugger can focus a check’s answer', async () => {
    await render({ initial: { name: 'Login', type: 'POST', target: '/login', assertions: [{ type: 'text_contains', value: 'a' }, { type: 'header_contains', value: 'Location', expected: '/home' }] } })
    const section = container.querySelector('[data-action-section="assertions"]')!
    expect(section.querySelector('[data-check-index="0:0"] [data-check-field="value"] input')).not.toBeNull()
    expect(section.querySelector('[data-check-index="0:1"] [data-check-field="expected"] input')).not.toBeNull()
  })

  it('offers to make a text a parameter, but not a status the script holds as a number', async () => {
    const scope = buildReferenceScope({ name: 's', version: '1', protocol: 'http', config: { base_url: '' }, actions: [{ name: 'Login', type: 'POST', target: '/login' }] }, { phase: 'action', actionIndex: 0 })
    await act(async () => root.render(<ReferenceFieldContext.Provider value={{ scope, onMakeParameter: () => {} }}>
      <Harness initial={{ name: 'Login', type: 'POST', target: '/login', assertions: [{ type: 'status_code', expected: 200 }, { type: 'text_contains', value: 'Welcome' }] }} />
    </ReferenceFieldContext.Provider>))
    const openPicker = async (label: string) => act(async () => byLabel<HTMLButtonElement>(`Insert a reference into ${label}`).click())
    await openPicker('Check 1 status')
    expect(container.querySelector('.reference-make-parameter')).toBeNull()
    await openPicker('Check 1 status')
    await openPicker('Check 2 text')
    expect(container.querySelector('.reference-make-parameter')!.textContent).toBe('Make this a parameter')
  })

  it('says why a replayed script has no result for the step', async () => {
    await render({ initial: { name: 'Login', type: 'POST', target: '/login', assertions: [{ type: 'status_code', expected: 200 }] }, withReplay: false, unavailable: 'The steps changed since the last replay: replay again to see each result here.' })
    expect(container.querySelector('.action-checks-replay')!.textContent).toBe('The steps changed since the last replay: replay again to see each result here.')
    expect(group('Check 1: Status is …').querySelector('.action-check-result')!.textContent).toBe('No result')
  })

  it('says when there is no replay to check against', async () => {
    await render({ initial: { name: 'Login', type: 'POST', target: '/login', assertions: [{ type: 'status_code', expected: 200 }] }, withReplay: false })
    expect(container.textContent).toContain('Not replayed yet: debug the script to see each result here.')
    expect(group('Check 1: Status is …').querySelector('.action-check-result.unchecked')!.textContent).toBe('Not replayed yet')
  })
})
