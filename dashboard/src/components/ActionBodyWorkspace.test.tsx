// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ActionBodyWorkspace } from './ActionBodyWorkspace'
import { AuraEnvelopeDraftContext, type AuraEnvelopeDraftOwner } from './AuraEnvelopeEditorDrafts'
import type { ScriptActionDefinition } from '../types/scripts'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('ActionBodyWorkspace', () => {
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

  it('edits a multipart body as fields', () => {
    const action: ScriptActionDefinition = {
      name: 'upload', type: 'request', method: 'POST', target: '/upload',
      body_multipart: { parts: [{ name: 'title', value: 'Report' }, { name: 'doc', filename: 'a.bin', value: '{{file.upload_doc}}' }] },
    }
    const onUpdate = vi.fn()
    act(() => root.render(<ActionBodyWorkspace action={action} index={0} path="script.actions[0]" parameters={[]}
      script={{ name: 's', protocol: 'http', actions: [action], data_sources: [{ name: 'upload_doc', type: 'file' }] } as never}
      onUpdate={onUpdate} onParameterize={vi.fn()} />))
    expect(container.textContent).toContain('Request body (multipart form)')
    expect(container.querySelector('[aria-label="Request body"]')).toBeNull()
    expect((container.querySelector('select[aria-label="Field 2 content"]') as HTMLSelectElement).value).toBe('upload_doc')
    act(() => (container.querySelector('button[aria-label="Remove Field 1"]') as HTMLButtonElement).click())
    expect(onUpdate).toHaveBeenLastCalledWith('body_multipart', { parts: [action.body_multipart!.parts[1]] })
  })

  it('goes back to a plain body when the last field is removed or on request', () => {
    const action: ScriptActionDefinition = { name: 'upload', type: 'request', method: 'POST', target: '/upload', body_multipart: { parts: [{ name: 'only', value: 'x' }] } }
    const onUpdate = vi.fn()
    act(() => root.render(<ActionBodyWorkspace action={action} index={0} path="script.actions[0]" parameters={[]} onUpdate={onUpdate} onParameterize={vi.fn()} />))
    act(() => (container.querySelector('button[aria-label="Remove Field 1"]') as HTMLButtonElement).click())
    expect(onUpdate).toHaveBeenLastCalledWith('body_multipart', undefined)
    onUpdate.mockClear()
    const plain = Array.from(container.querySelectorAll('button')).find(candidate => candidate.textContent === 'Use a plain body instead')!
    act(() => plain.click())
    expect(onUpdate).toHaveBeenLastCalledWith('body_multipart', undefined)
  })

  it('lets Tab move focus out of the body editor instead of indenting, when turned on', () => {
    window.localStorage.removeItem('apexload.script-editor.tab-moves-focus')
    const action: ScriptActionDefinition = { name: 'post', type: 'request', method: 'POST', target: '/x', body: 'a=1' }
    const onUpdate = vi.fn()
    act(() => root.render(<ActionBodyWorkspace action={action} index={0} path="script.actions[0]" parameters={[]} onUpdate={onUpdate} onParameterize={vi.fn()} />))
    const toggle = () => Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(candidate => candidate.textContent?.startsWith('Tab moves focus'))!
    const hint = () => container.querySelector('#request-body-keyboard-hint')!.textContent
    const pressTab = () => {
      const content = container.querySelector<HTMLElement>('.cm-content')!
      const event = new KeyboardEvent('keydown', { key: 'Tab', keyCode: 9, bubbles: true, cancelable: true })
      act(() => { content.dispatchEvent(event) })
      return event.defaultPrevented
    }
    expect(toggle().getAttribute('aria-pressed')).toBe('false')
    expect(hint()).toContain('Press Esc, then Tab')
    // By default the editor keeps Tab for indenting.
    expect(pressTab()).toBe(true)

    act(() => toggle().click())
    expect(toggle().getAttribute('aria-pressed')).toBe('true')
    expect(toggle().textContent).toBe('Tab moves focus: On')
    expect(hint()).toContain('Tab moves focus out of the editor')
    // The browser's own Tab handling (focus moves on) is left alone.
    expect(pressTab()).toBe(false)
    expect(window.localStorage.getItem('apexload.script-editor.tab-moves-focus')).toBe('on')
    window.localStorage.removeItem('apexload.script-editor.tab-moves-focus')
  })

  it('switches an empty body to multipart fields', () => {
    const action: ScriptActionDefinition = { name: 'upload', type: 'request', method: 'POST', target: '/upload' }
    const onUpdate = vi.fn()
    act(() => root.render(<ActionBodyWorkspace action={action} index={0} path="script.actions[0]" parameters={[]} onUpdate={onUpdate} onParameterize={vi.fn()} />))
    const button = Array.from(container.querySelectorAll('button')).find(candidate => candidate.textContent === 'Use multipart form fields')!
    act(() => button.click())
    expect(onUpdate).toHaveBeenCalledWith('body_multipart', { parts: [{ name: 'field_1', value: '' }] })
  })

  it('defaults /aura form requests to the decoded Aura envelope view and toggles cleanly to raw form body', () => {
    const action: ScriptActionDefinition = {
      name: 'Search Customers',
      type: 'POST',
      method: 'POST',
      target: '/aura?r=7&aura.ApexAction.execute=1',
      body: 'message=%7B%22actions%22%3A%5B%7B%22id%22%3A%221%3Ba%22%2C%22descriptor%22%3A%22aura%3A%2F%2FApexActionController%2FACTION%24execute%22%2C%22params%22%3A%7B%22classname%22%3A%22SearchCtrl%22%2C%22method%22%3A%22find%22%7D%7D%5D%7D&aura.pageURI=%2Flightning%2Fpage%2Fhome&aura.token={{var.aura_token}}',
    }
    const onUpdate = vi.fn()
    act(() => root.render(<ActionBodyWorkspace action={action} index={0} path="script.actions[0]" parameters={[]} onUpdate={onUpdate} onParameterize={vi.fn()} />))
    expect(container.querySelector('[aria-label="Aura envelope"]')).not.toBeNull()
    expect(container.textContent).toContain('SearchCtrl.find')

    const rawBtn = Array.from(container.querySelectorAll('button')).find(btn => btn.textContent === 'Raw form body')!
    act(() => rawBtn.click())
    expect(container.querySelector('[aria-label="Aura envelope"]')).toBeNull()
    expect(container.querySelector('[aria-label="Request body"]')).not.toBeNull()
  })

  it('warns in raw form body that a raw edit leaves the step\'s unapplied Aura edits no longer applying', () => {
    const action: ScriptActionDefinition = {
      name: 'Aura_Record',
      type: 'POST',
      method: 'POST',
      target: '/aura',
      body: `message=${encodeURIComponent(JSON.stringify({ actions: [{ id: '1;a', descriptor: 'aura://RecordUiController/ACTION$getRecord', params: { recordId: '001OLD' } }] }))}&aura.token={{var.aura_token}}`,
    }
    const owner = (params: Record<number, string>): AuraEnvelopeDraftOwner => ({ drafts: { params, context: null }, stale: false, staleMessage: '', change: vi.fn() })
    const show = (draftOwner: AuraEnvelopeDraftOwner) => act(() => root.render(<AuraEnvelopeDraftContext.Provider value={draftOwner}><ActionBodyWorkspace action={action} index={0} path="script.actions[0]" parameters={[]} onUpdate={vi.fn()} onParameterize={vi.fn()} /></AuraEnvelopeDraftContext.Provider>))
    show(owner({ 0: '{"recordId":"001NEW"}' }))
    act(() => Array.from(container.querySelectorAll('button')).find(btn => btn.textContent === 'Raw form body')!.click())
    expect(container.textContent).toContain('This step has Aura envelope edits that are not applied')
    show(owner({}))
    expect(container.textContent).not.toContain('This step has Aura envelope edits that are not applied')
  })
})
