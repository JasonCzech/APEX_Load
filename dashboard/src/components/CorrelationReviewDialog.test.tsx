// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import CorrelationReviewDialog from './CorrelationReviewDialog'
import type { ValueUse } from './responseSelectionModel'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

let container: HTMLDivElement
let root: Root
beforeEach(() => {
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
})
afterEach(async () => {
  await act(async () => root.unmount())
  container.remove()
})

const uses: ValueUse[] = [
  { position: { phase: 'action', actionIndex: 1 }, stepName: 'Order', field: 'request_url', fieldLabel: 'URL', start: 14, end: 20, before: '/orders?token=', after: '' },
  { position: { phase: 'action', actionIndex: 2 }, stepName: 'Pay', field: 'request_headers.x-token', fieldLabel: 'Header X-Token', start: 0, end: 6, before: '', after: '' },
]

async function render(onApply = vi.fn(), onClose = vi.fn()) {
  await act(async () => root.render(<CorrelationReviewDialog value="abc123" sourceName="Login" suggestedName="token" taken={new Set(['user'])} uses={uses} onApply={onApply} onClose={onClose} />))
  return { onApply, onClose }
}
const dialog = () => document.body.querySelector<HTMLElement>('[role="dialog"]')!
const apply = () => [...dialog().querySelectorAll('button')].find(button => button.textContent?.startsWith('Replace'))!
async function type(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!
  await act(async () => { setter.call(input, value); input.dispatchEvent(new Event('input', { bubbles: true })) })
}

describe('CorrelationReviewDialog', () => {
  it('lists each later use with the value in place, all chosen', async () => {
    await render()
    expect(dialog().getAttribute('aria-modal')).toBe('true')
    expect(dialog().textContent).toContain('Save “abc123” from Login each time it runs')
    const rows = [...dialog().querySelectorAll('.correlation-review-use')]
    expect(rows.map(row => row.textContent)).toEqual(['Order · URL: /orders?token=abc123', 'Pay · Header X-Token: abc123'])
    expect(rows.every(row => row.querySelector<HTMLInputElement>('input')!.checked)).toBe(true)
    expect(apply().textContent).toBe('Replace 2 uses')
    // The dialog focuses the name once it has opened.
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 0)) })
    expect(document.activeElement).toBe(dialog().querySelector('input'))
  })

  it('replaces only the chosen uses under the chosen name', async () => {
    const { onApply } = await render()
    await act(async () => dialog().querySelectorAll<HTMLInputElement>('.correlation-review-use input')[1].click())
    expect(dialog().querySelector('[role="status"]')!.textContent).toBe('1 of 2 uses will read the saved value. Undo puts the recorded value back.')
    await type(dialog().querySelector('input')!, ' authToken ')
    expect(dialog().querySelector('legend')!.textContent).toBe('Replace with {{var.authToken}}')
    await act(async () => apply().click())
    expect(onApply).toHaveBeenCalledWith('authToken', [uses[0]])
  })

  it('will not replace under a name that is invalid or taken, or with nothing chosen', async () => {
    await render()
    const name = dialog().querySelector('input')!
    await type(name, '9lives')
    expect(dialog().querySelector('[role="alert"]')!.textContent).toBe('Use a letter or underscore, then letters, numbers or underscores.')
    expect(name.getAttribute('aria-invalid')).toBe('true')
    expect(apply().disabled).toBe(true)
    await type(name, 'user')
    expect(dialog().querySelector('[role="alert"]')!.textContent).toBe('The script already uses user.')
    await type(name, 'token')
    expect(apply().disabled).toBe(false)
    for (const box of dialog().querySelectorAll<HTMLInputElement>('.correlation-review-use input')) await act(async () => box.click())
    expect(apply().textContent).toBe('Replace 0 uses')
    expect(apply().disabled).toBe(true)
  })

  it('closes on Cancel and Escape', async () => {
    const { onClose } = await render()
    await act(async () => [...dialog().querySelectorAll('button')].find(button => button.textContent === 'Cancel')!.click())
    expect(onClose).toHaveBeenCalledTimes(1)
    await act(async () => dialog().dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })))
    expect(onClose).toHaveBeenCalledTimes(2)
  })
})
