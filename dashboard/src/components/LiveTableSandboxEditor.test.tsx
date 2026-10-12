// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import LiveTableSandboxEditor from './LiveTableSandboxEditor'

let container: HTMLDivElement; let root: Root
beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container) })
afterEach(async () => { await act(async () => root.unmount()); container.remove() })

it('offers explicit empty seeds and explains isolation and disclosure', async () => {
  const change = vi.fn()
  await act(async () => root.render(<LiveTableSandboxEditor onChange={change} />))
  expect(container.textContent).toContain('Every replay starts fresh')
  await act(async () => container.querySelector('button')!.click())
  expect(JSON.parse(change.mock.calls[0][0])).toEqual([{ alias: 'work', profile: 'native_queue', columns: ['id'], rows: [] }])
})

it('keeps invalid seed text editable with an accessible validation message', async () => {
  const change = vi.fn()
  await act(async () => root.render(<LiveTableSandboxEditor value="{" onChange={change} />))
  const textarea = container.querySelector('textarea')!
  expect(textarea.getAttribute('aria-invalid')).toBe('true')
  expect(container.querySelector('[role="alert"]')?.textContent).toContain('JSON array')
  await act(async () => { Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(textarea, '[]'); textarea.dispatchEvent(new Event('input', { bubbles: true })) })
  expect(change).toHaveBeenCalledWith('[]')
})
