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
  await act(async () => [...container.querySelectorAll('button')].find(button => button.textContent === 'Add empty sandbox example')!.click())
  expect(JSON.parse(change.mock.calls[0][0])).toEqual([{ alias: 'work', profile: 'native_queue', columns: ['id'], rows: [] }])
})

it('edits seeds as a table per alias and keeps absent cells absent', async () => {
  const change = vi.fn()
  const seeds = [{ alias: 'work', profile: 'native_queue', columns: ['id', 'note'], rows: [{ values: { id: 'a', note: '' } }] }]
  await act(async () => root.render(<LiveTableSandboxEditor value={JSON.stringify(seeds)} onChange={change} />))
  expect(container.querySelector('textarea')).toBeNull()
  expect(container.querySelector('table[aria-label="Starting rows for work"]')).not.toBeNull()
  const newId = container.querySelector<HTMLInputElement>('input[aria-label="id, new row"]')!
  await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(newId, 'b'); newId.dispatchEvent(new Event('input', { bubbles: true })) })
  await act(async () => { newId.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })) })
  expect(JSON.parse(change.mock.calls[0][0])[0].rows).toEqual([{ values: { id: 'a', note: '' } }, { values: { id: 'b' } }])
  await act(async () => [...container.querySelectorAll('button')].find(button => button.textContent === 'Edit as JSON')!.click())
  expect(container.querySelector('textarea')).not.toBeNull()
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
