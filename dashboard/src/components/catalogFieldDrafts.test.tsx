// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ScriptActionDefinition, ScriptDefinition } from '../types/scripts'
import { useCatalogFieldDrafts } from './catalogFieldDrafts'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const action = (): ScriptActionDefinition => ({ name: 'Step', type: 'live_table', target: '', params: { delta: '1' } })
const document = (): ScriptDefinition => ({ name: 'Script', version: '1', protocol: 'http', config: { base_url: 'http://example.test' }, actions: [action(), action()] })
let registry: ReturnType<typeof useCatalogFieldDrafts>
const edited = vi.fn()
function Harness({ script }: { script: ScriptDefinition }) {
  registry = useCatalogFieldDrafts(script, edited)
  return null
}

describe('catalog field draft ownership', () => {
  let container: HTMLDivElement
  let root: Root
  beforeEach(() => {
    container = globalThis.document.createElement('div')
    root = createRoot(container)
    edited.mockClear()
  })
  afterEach(async () => { await act(async () => root.unmount()) })

  it('resolves pending JSON into its field and removes a deliberately cleared field', async () => {
    const script = document()
    await act(async () => root.render(<Harness script={script} />))
    await act(async () => registry.forAction('action:0', script.actions[0]).change('headers', { text: '{"a":"b"}', error: '', value: { a: 'b' } }))
    expect(registry.count).toBe(1)
    expect(registry.hasPending()).toBe(true)
    expect(registry.resolve(script).actions[0].headers).toEqual({ a: 'b' })
    expect(script.actions[0].headers).toBeUndefined()
    await act(async () => registry.forAction('action:0', script.actions[0]).change('params.delta', { text: '', error: '', value: undefined }))
    expect(registry.resolve(script).actions[0].params).toEqual({})
    expect(edited).toHaveBeenCalledTimes(2)
    await act(async () => registry.clear())
    expect(registry.resolve(script)).toBe(script)
    expect(registry.hasPending()).toBe(false)
  })

  it.each(['rename', 'reorder', 'delete', 'replace'] as const)('retains and blocks a pending field across action %s until explicit discard', async kind => {
    const script = document()
    await act(async () => root.render(<Harness script={script} />))
    await act(async () => registry.forAction('action:0', script.actions[0]).change('params.delta', { text: '1.5', error: 'Enter a whole number.' }))
    const changed = { ...script, actions: kind === 'rename' ? [{ ...script.actions[0], name: 'Renamed' }, script.actions[1]] : kind === 'reorder' ? [...script.actions].reverse() : kind === 'delete' ? [script.actions[1]] : [action(), script.actions[1]] }
    await act(async () => root.render(<Harness script={changed} />))
    expect(registry.count).toBe(1)
    expect(registry.problem).toContain('The action changed')
    expect(() => registry.resolve(changed)).toThrow('Discard field edits')
    expect(registry.forAction('action:0', changed.actions[0]).drafts).toEqual({})
    // An edit to the replacement at the same path cannot erase the old draft.
    await act(async () => registry.forAction('action:0', changed.actions[0]).change('params.delta', null))
    expect(registry.hasPending()).toBe(true)
    await act(async () => registry.clear())
    expect(registry.resolve(changed)).toBe(changed)
  })

  it('keeps an invalid field after selection changes, and accepts its correction', async () => {
    const script = document()
    await act(async () => root.render(<Harness script={script} />))
    await act(async () => registry.forAction('action:0', script.actions[0]).change('params.delta', { text: '1.5', error: 'Enter a whole number.' }))
    expect(registry.forAction('action:1', script.actions[1]).drafts).toEqual({})
    expect(registry.forAction('action:0', script.actions[0]).drafts['params.delta'].text).toBe('1.5')
    expect(() => registry.resolve(script)).toThrow('Enter a whole number.')
    await act(async () => registry.forAction('action:0', script.actions[0]).change('params.delta', null))
    expect(registry.problem).toBe('')
    expect(registry.resolve(script)).toBe(script)
  })
})
