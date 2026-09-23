// @vitest-environment jsdom
import { act, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'
import ImportCredentialPlacement from './ImportCredentialPlacement'

it('moves the same editor between issue and credentials without losing unsaved values', async () => {
 const container = document.createElement('div'); document.body.append(container)
 const issue = document.createElement('div'); document.body.append(issue)
 const root = createRoot(container)
 function Editor() { const [value, setValue] = useState(''); return <input aria-label="Binding" value={value} onChange={event => setValue(event.target.value)} /> }
 const render = (target: HTMLElement | null) => root.render(<ImportCredentialPlacement target={target} visible><Editor /></ImportCredentialPlacement>)
 try {
  await act(async () => render(null))
  const input = container.querySelector('input')!
  await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, 'draft-secret-reference'); input.dispatchEvent(new Event('input', { bubbles: true })) })
  await act(async () => render(issue))
  expect(issue.querySelector('input')).toBe(input)
  expect(input.value).toBe('draft-secret-reference')
  await act(async () => render(null))
  expect(container.querySelector('input')).toBe(input)
  expect(input.value).toBe('draft-secret-reference')
  expect(issue.children).toHaveLength(0)
 } finally { await act(async () => root.unmount()); container.remove(); issue.remove() }
})
