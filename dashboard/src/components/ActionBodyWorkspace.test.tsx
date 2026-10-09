// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ActionBodyWorkspace } from './ActionBodyWorkspace'
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

  it('switches an empty body to multipart fields', () => {
    const action: ScriptActionDefinition = { name: 'upload', type: 'request', method: 'POST', target: '/upload' }
    const onUpdate = vi.fn()
    act(() => root.render(<ActionBodyWorkspace action={action} index={0} path="script.actions[0]" parameters={[]} onUpdate={onUpdate} onParameterize={vi.fn()} />))
    const button = Array.from(container.querySelectorAll('button')).find(candidate => candidate.textContent === 'Use multipart form fields')!
    act(() => button.click())
    expect(onUpdate).toHaveBeenCalledWith('body_multipart', { parts: [{ name: 'field_1', value: '' }] })
  })
})
