// @vitest-environment jsdom
import { act } from 'react'
import { afterEach, expect, it, vi } from 'vitest'
import { confirmAction } from './confirmAction'
vi.unmock('./DialogLayer')
vi.unmock('./confirmAction')

afterEach(async () => { await act(async () => { window.dispatchEvent(new HashChangeEvent('hashchange')) }) })
it('defaults focus to Cancel, rejects concurrent requests, and restores the opener', async () => {
  const opener = document.createElement('button'); document.body.appendChild(opener); opener.focus()
  let result!: Promise<boolean>
  await act(async () => { result = confirmAction('Delete this script?') })
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 0)) })
  expect(document.activeElement?.textContent).toBe('Cancel')
  expect(await confirmAction('Delete twice?')).toBe(false)
  await act(async () => { document.querySelector<HTMLButtonElement>('.ux-confirmation .btn-danger')!.click() })
  expect(await result).toBe(true)
  expect(document.querySelector('[role="alertdialog"]')).toBeNull()
  expect(document.activeElement).toBe(opener)
  opener.remove()
})
it('cancels on Escape or navigation so stale actions cannot run', async () => {
  for (const event of [new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }), new HashChangeEvent('hashchange')]) {
    let result!: Promise<boolean>
    await act(async () => { result = confirmAction('Apply these changes?') })
    await act(async () => { if (event.type === 'keydown') document.dispatchEvent(event); else window.dispatchEvent(event) })
    expect(await result).toBe(false)
  }
})
