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
  await act(async () => { result = confirmAction('Delete this script?', { title: 'Delete script', confirmLabel: 'Delete', destructive: true }) })
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
it('uses explicit wording instead of guessing it from user-chosen names in the message', async () => {
  let result!: Promise<boolean>
  await act(async () => { result = confirmAction('Start “Delete account flow”?', { title: 'Start execution plan', confirmLabel: 'Start runs', destructive: false }) })
  const dialog = document.querySelector('[role="alertdialog"]')!
  expect(dialog.querySelector('h3')?.textContent).toBe('Start execution plan')
  expect(dialog.querySelector('.btn-danger')).toBeNull()
  const confirm = dialog.querySelector<HTMLButtonElement>('.btn-primary')!
  expect(confirm.textContent).toBe('Start runs')
  await act(async () => { confirm.click() })
  expect(await result).toBe(true)
})
it('stays neutral without explicit wording, even when the message mentions deleting', async () => {
  let result!: Promise<boolean>
  await act(async () => { result = confirmAction('Run the “Delete account” journey now?') })
  const dialog = document.querySelector('[role="alertdialog"]')!
  expect(dialog.querySelector('h3')?.textContent).toBe('Confirm action')
  expect(dialog.querySelector('.btn-danger')).toBeNull()
  expect(dialog.querySelector('.btn-primary')?.textContent).toBe('Continue')
  await act(async () => { dialog.querySelector<HTMLButtonElement>('.btn-secondary')!.click() })
  expect(await result).toBe(false)
})
