// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import NativeScriptActionFields from './NativeScriptActionFields'
import NativeServicesEditor from './NativeServicesEditor'

let container: HTMLDivElement; let root: Root
beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container) })
afterEach(async () => { await act(async () => root.unmount()); container.remove() })
function input(element: HTMLInputElement | HTMLTextAreaElement, value: string) { Object.getOwnPropertyDescriptor(element instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype, 'value')!.set!.call(element, value); element.dispatchEvent(new Event('input', { bubbles: true })) }

it('keeps JavaScript source literal and retains malformed structured input drafts', async () => {
  const change = vi.fn()
  await act(async () => root.render(<NativeScriptActionFields action={{ name: 'Host', type: 'native_javascript', target: '', params: { source: 'return {id: "{{literal}}"};', inputs: {} } }} onChange={change} />))
  const [source, inputs] = container.querySelectorAll('textarea')
  expect(source.value).toContain('{{literal}}')
  await act(async () => input(inputs, '{'))
  expect(change).toHaveBeenLastCalledWith(expect.objectContaining({ source: 'return {id: "{{literal}}"};', inputs: '{' }))
})
it('makes distributed rendezvous an explicit opt-in with timeout semantics', async () => {
  const change = vi.fn()
  await act(async () => root.render(<NativeScriptActionFields action={{ name: 'checkout barrier', type: 'rendezvous', target: '', params: {} }} onChange={change} />))
  expect(container.textContent).not.toContain('On timeout')
  const select = container.querySelector('select')!
  await act(async () => { select.value = 'distributed_group'; select.dispatchEvent(new Event('change', { bubbles: true })) })
  expect(change).toHaveBeenLastCalledWith({ scope: 'distributed_group', key: 'checkout_barrier', timeout_ms: 30000, on_timeout: 'fail' })
  await act(async () => root.render(<NativeScriptActionFields action={{ name: 'checkout barrier', type: 'rendezvous', target: '', params: change.mock.lastCall![0] }} onChange={change} />))
  expect(container.querySelector('option[value="fail"]')?.textContent).toBe('Fail the action')
})
it('shows a null inputs draft as invalid instead of replacing it with an empty object', async () => {
  await act(async () => root.render(<NativeScriptActionFields action={{ name: 'Host', type: 'native_javascript', target: '', params: { source: 'return {};', inputs: null } }} onChange={vi.fn()} />))
  const inputs = container.querySelectorAll('textarea')[1]
  expect(inputs.value).toBe('null')
  expect(inputs.getAttribute('aria-invalid')).toBe('true')
  expect(container.querySelector('[role="alert"]')?.textContent).toContain('Inputs must be a JSON object')
})
it('does not silently add guest permissions for runtime cleanup', async () => {
  const change = vi.fn()
  await act(async () => root.render(<NativeServicesEditor declarations={[{ alias: 'work', operations: ['lease'] }]} onChange={change} />))
  expect([...container.querySelectorAll<HTMLInputElement>('input[type=checkbox]')].filter(item => item.checked)).toHaveLength(1)
  expect(container.textContent).toContain('runtime manages renewal and cleanup separately')
})
