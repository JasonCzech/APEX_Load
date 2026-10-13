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

it('creates the script’s tables beside the seeds it already has', async () => {
  const change = vi.fn()
  const usage = [{ alias: 'jobs', columns: ['job_id'], operations: ['lease'], nativeOperations: [], mutates: true, kinds: ['native_queue' as const], readsEachIteration: false, suppliedByScript: false, steps: [] }]
  const seeds = JSON.stringify([{ alias: 'work', columns: ['id'], rows: [] }])
  await act(async () => root.render(<LiveTableSandboxEditor value={seeds} usage={usage} onChange={change} />))
  await act(async () => [...container.querySelectorAll('button')].find(button => button.textContent === 'Create tables from this script')!.click())
  expect(JSON.parse(change.mock.lastCall![0]).map((seed: { alias: string }) => seed.alias)).toEqual(['work', 'jobs'])
  expect(container.querySelector('[role="status"]')!.textContent).toContain('Added jobs, with the columns the script uses.')
  await act(async () => root.render(<LiveTableSandboxEditor value={change.mock.lastCall![0]} usage={usage} onChange={change} />))
  expect([...container.querySelectorAll('button')].find(button => button.textContent === 'Create tables from this script')!.hasAttribute('disabled')).toBe(true)
})
