// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { MultipartPartsEditor } from './MultipartPartsEditor'
import type { ScriptMultipartPart } from '../types/scripts'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

function setValue(element: HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement, value: string) {
  const prototype = element instanceof HTMLSelectElement ? HTMLSelectElement.prototype : element instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype
  Object.getOwnPropertyDescriptor(prototype, 'value')!.set!.call(element, value)
  element.dispatchEvent(new Event(element instanceof HTMLSelectElement ? 'change' : 'input', { bubbles: true }))
}

describe('MultipartPartsEditor', () => {
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

  const parts: ScriptMultipartPart[] = [
    { name: 'title', value: 'Report {{var.quarter}}' },
    { name: 'doc', filename: 'report.txt', content_type: 'text/plain', value: '{{file.upload_doc}}' },
    { name: 'logo', filename: 'logo.bin', value: 'AAEC', value_encoding: 'base64' },
  ]

  it('shows text and file fields and edits them', () => {
    const onChange = vi.fn()
    act(() => root.render(<MultipartPartsEditor parts={parts} fileSources={['upload_doc', 'upload_other']} onChange={onChange} />))
    expect((container.querySelector('textarea[aria-label="Field 1 value"]') as HTMLTextAreaElement).value).toBe('Report {{var.quarter}}')
    const content = container.querySelector('select[aria-label="Field 2 content"]') as HTMLSelectElement
    expect(content.value).toBe('upload_doc')
    const inline = container.querySelector('select[aria-label="Field 3 content"]') as HTMLSelectElement
    expect(inline.value).toBe('#inline')
    expect(inline.textContent).toContain('Inline bytes (3 bytes)')

    act(() => setValue(container.querySelector('textarea[aria-label="Field 1 value"]') as HTMLTextAreaElement, 'Q4 report'))
    expect(onChange).toHaveBeenLastCalledWith([{ ...parts[0], value: 'Q4 report' }, parts[1], parts[2]])

    act(() => setValue(content, 'upload_other'))
    expect(onChange).toHaveBeenLastCalledWith([parts[0], { ...parts[1], value: '{{file.upload_other}}', value_encoding: undefined }, parts[2]])
  })

  it('adds and removes fields', () => {
    const onChange = vi.fn()
    act(() => root.render(<MultipartPartsEditor parts={parts.slice(0, 1)} fileSources={['upload_doc']} onChange={onChange} />))
    const button = (text: string) => Array.from(container.querySelectorAll('button')).find(candidate => candidate.textContent === text)!
    act(() => button('Add file field').click())
    expect(onChange).toHaveBeenLastCalledWith([parts[0], { name: 'file_2', filename: '', value: '{{file.upload_doc}}' }])
    act(() => (container.querySelector('button[aria-label="Remove Field 1"]') as HTMLButtonElement).click())
    expect(onChange).toHaveBeenLastCalledWith([])
  })
})
