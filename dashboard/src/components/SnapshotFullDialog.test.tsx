// @vitest-environment jsdom

import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import SnapshotFullDialog from './SnapshotFullDialog'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

function Harness() {
  const [open, setOpen] = useState(false)
  return (
    <>
      <button type="button" onClick={() => setOpen(true)}>Open snapshot</button>
      <SnapshotFullDialog
        title="Request body"
        open={open}
        copied={false}
        copyDisabled={false}
        onCopy={() => {}}
        onClose={() => setOpen(false)}
      >
        <pre>snapshot payload</pre>
      </SnapshotFullDialog>
    </>
  )
}

describe('SnapshotFullDialog accessibility', () => {
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

  it('labels the portal, traps Tab, closes on Escape, and restores focus', async () => {
    await act(async () => {
      root.render(<Harness />)
    })
    const trigger = container.querySelector<HTMLButtonElement>('button')
    trigger?.focus()
    await act(async () => {
      trigger?.click()
    })
    await flushFocus()

    const dialog = document.querySelector<HTMLElement>('.snapshot-full-dialog[role="dialog"]')
    const copy = document.querySelector<HTMLButtonElement>('.snapshot-full-dialog-actions .btn-secondary')
    const close = document.querySelector<HTMLButtonElement>('.snapshot-full-dialog-actions .btn-ghost')
    expect(dialog?.getAttribute('aria-modal')).toBe('true')
    expect(document.getElementById(dialog?.getAttribute('aria-labelledby') || '')?.textContent).toBe('Request body')
    expect(document.activeElement).toBe(close)

    await act(async () => {
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', bubbles: true }))
    })
    expect(document.activeElement).toBe(copy)

    await act(async () => {
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    })
    expect(document.querySelector('.snapshot-full-dialog')).toBeNull()
    expect(document.activeElement).toBe(trigger)
  })
})

async function flushFocus() {
  await act(async () => {
    await new Promise(resolve => window.setTimeout(resolve, 0))
  })
}
