// @vitest-environment jsdom
import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import type { ScriptActionDefinition, ScriptDefinition } from '../types/scripts'
import { ActionEditor } from './ScriptEditorActionEditor'
import { formatScriptJson, parseScriptJsonDraft } from './scriptEditorJsonModel'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
const initial: ScriptActionDefinition = { name: 'Shared session group', type: 'http_sequence', target: '', params: { requests: [{ method: 'GET', target: '/one', body: '{{var.value}}', vendor: { keep: 1 }, extractors: [{ name: 'next', type: 'json', expression: '$.next' }] }, { method: 'POST', target: '/two', header_list: [{ name: 'X-Trace', value: 'exact' }] }, { method: 'GET', target: '/three' }], unknown: { keep: true } }, metadata: { source_transaction: 'Shared session group', source_path: 'Action.c:10' } }
const noop = () => {}

let container: HTMLDivElement, root: Root, current: ScriptActionDefinition
function Controlled({ action = initial, protocol = 'http' }: { action?: ScriptActionDefinition; protocol?: string }) {
  const [value, setValue] = useState(action)
  current = value
  return <ActionEditor action={value} index={0} parameters={[]} onUpdate={(_index, field, next) => setValue(previous => ({ ...previous, [field]: next }))} onAddExtractor={noop} onUpdateExtractor={noop} onRemoveExtractor={noop} onAddAssertion={noop} onUpdateAssertion={noop} onRemoveAssertion={noop} onAddHeader={noop} onUpdateHeader={noop} onRemoveHeader={noop} onAddMetadata={noop} onUpdateMetadata={noop} onRemoveMetadata={noop} onBeginParameterEdit={noop} methodColor={() => '#000'} protocol={protocol} isBrowserScript={false} isJmsScript={false} isSoapScript={false} issues={[]} />
}
const mode = () => container.querySelector<HTMLSelectElement>('[aria-label="Sequence execution mode"]')!
const limit = () => container.querySelector<HTMLInputElement>('[aria-label="Maximum concurrent requests"]')!
function choose(value: string) { act(() => { mode().value = value; mode().dispatchEvent(new Event('change', { bubbles: true })) }) }
function changeLimit(value: string) { act(() => { Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!.call(limit(), value); limit().dispatchEvent(new Event('input', { bubbles: true })) }) }

beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container) })
afterEach(() => { act(() => root.unmount()); container.remove() })

describe('HTTP sequence execution fields', () => {
  it('authors concurrent mode and bounded fanout and retains them through real JSON save/reopen projection', () => {
    act(() => root.render(<Controlled />))
    expect(mode().value).toBe('default')
    expect(container.textContent).toContain('Sequential execution (default)')
    choose('concurrent')
    expect(container.textContent).toContain('up to 3 requests in flight')
    expect(container.textContent).toContain('Requests share the parent HTTP session')
    expect(container.textContent).toContain('prepared in source order before dispatch')
    expect(container.textContent).toContain('extracted values become available after the group joins')
    expect(container.textContent).toContain('Sibling requests cannot consume values extracted within this group')
    expect(container.textContent).toContain('pausing and stepping occur at the group boundary')
    changeLimit('2')
    expect(container.textContent).toContain('up to 2 requests in flight')
    expect(current.params?.requests).toBe(initial.params?.requests)
    expect(current.params?.unknown).toBe(initial.params?.unknown)
    expect(current.metadata).toBe(initial.metadata)
    const saved: ScriptDefinition = { name: 'Saved group', version: '1', protocol: 'http', config: { base_url: 'https://example.test' }, actions: [current] }
    const parsed = parseScriptJsonDraft(formatScriptJson(saved))
    expect(parsed.ok).toBe(true)
    if (!parsed.ok) throw new Error('saved JSON could not reopen')
    expect(parsed.script.actions[0]).toEqual({ ...initial, params: { ...initial.params, execution_mode: 'concurrent', max_concurrency: 2 } })
    act(() => root.render(<Controlled key="reopened" action={parsed.script.actions[0]} />))
    expect(mode().value).toBe('concurrent')
    expect(limit().value).toBe('2')
    expect(container.textContent).toContain('up to 2 requests in flight')
    choose('sequential')
    expect(current.params?.max_concurrency).toBe(2)
    expect(container.textContent).toContain('Each request can use values extracted by earlier requests')
    choose('default')
    expect(current.params).not.toHaveProperty('execution_mode')
    expect(current.params?.max_concurrency).toBe(2)
  })

  it('does not silently clamp invalid entered fanout and restores automatic concurrency only on an explicit clear', () => {
    act(() => root.render(<Controlled action={{ ...initial, params: { ...initial.params, execution_mode: 'concurrent' } }} />))
    for (const text of ['99', '0', '1.5']) {
      changeLimit(text)
      expect(current.params?.max_concurrency).toBe(Number(text))
      expect(limit().value).toBe(text)
      expect(container.textContent).toContain('Authored concurrency limit is invalid')
      expect(container.textContent).toContain('Execution settings need review')
      expect(container.textContent).not.toContain('Concurrent group · up to')
    }
    changeLimit('')
    expect(current.params).not.toHaveProperty('max_concurrency')
    expect(container.textContent).toContain('up to 3 requests in flight')
  })

  it('shows malformed raw settings without displaying/coercing source values or rewriting retained params', () => {
    const malformed = { ...initial, params: { ...initial.params, execution_mode: { toString: null }, max_concurrency: 'private-fanout' } }
    act(() => root.render(<Controlled action={malformed} />))
    expect(mode().value).toBe('unsupported')
    expect(limit().placeholder).toBe('Invalid authored limit')
    expect(container.textContent).toContain('Execution settings need review')
    expect(container.innerHTML).not.toContain('private-fanout')
    expect(current.params).toBe(malformed.params)
    choose('concurrent')
    expect(current.params?.execution_mode).toBe('concurrent')
    expect(current.params?.max_concurrency).toBe('private-fanout')
    expect(container.textContent).toContain('Execution settings need review')
    changeLimit('2')
    expect(container.textContent).toContain('up to 2 requests in flight')
  })

  it.each([null, [], 'private-params'])('disables policy editing for a malformed whole parameter envelope %j', params => {
    act(() => root.render(<Controlled action={{ ...initial, params } as unknown as ScriptActionDefinition} />))
    expect(mode().disabled).toBe(true)
    expect(limit().disabled).toBe(true)
    expect(container.textContent).toContain('Authored execution settings are malformed')
    expect(container.innerHTML).not.toContain('private-params')
    expect(current.params).toBe(params)
  })

  it('exposes concurrent child cap failures without silently changing the request list', () => {
    const requests = new Array(257).fill({ method: 'GET', target: '/one' })
    act(() => root.render(<Controlled action={{ ...initial, params: { requests, execution_mode: 'concurrent' } }} />))
    expect(container.textContent).toContain('Concurrent groups require 1–256 child request objects')
    expect(current.params?.requests).toBe(requests)
    choose('sequential')
    expect(container.textContent).toContain('Sequential execution.')
    expect(container.textContent).not.toContain('Execution settings need review')
  })

  it.each(['http/1.1', 'http/2'])('shows alias %s authored limits and preserves manual policy edits', protocol => {
    act(() => root.render(<Controlled protocol={protocol} action={{ ...initial, params: { ...initial.params, execution_mode: 'concurrent', max_concurrency: 99 } }} />))
    expect(mode()?.value).toBe('concurrent')
    expect(limit()?.value).toBe('99')
    expect(container.textContent).toContain('Authored concurrency limit is invalid')
    changeLimit('2')
    expect(current.params?.max_concurrency).toBe(2)
    expect(container.textContent).toContain('up to 2 requests in flight')
    choose('default')
    expect(current.params).not.toHaveProperty('execution_mode')
    expect(current.params?.max_concurrency).toBe(2)
    choose('concurrent')
    expect(current.params?.execution_mode).toBe('concurrent')
    expect(current.params?.requests).toBe(initial.params?.requests)
    expect(current.params?.unknown).toBe(initial.params?.unknown)
  })

  it.each(['browser', 'soap', 'grpc', 'java_http'])('does not expose concurrent HTTP claims or controls on %s', protocol => {
    act(() => root.render(<Controlled protocol={protocol} action={{ ...initial, params: { ...initial.params, execution_mode: 'concurrent' } }} />))
    expect(mode()).toBeNull()
    expect(limit()).toBeNull()
    expect(container.textContent).not.toContain('Requests share the parent HTTP session')
    expect(container.textContent).toContain('available for HTTP scripts')
  })
})
