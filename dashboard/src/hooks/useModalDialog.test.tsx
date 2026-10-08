// @vitest-environment jsdom

import { act, useRef } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ProjectModal from '../components/ProjectModal'
import { useModalDialog } from './useModalDialog'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

function keyboardEvent(key: string, options: Partial<KeyboardEventInit> = {}) {
  return new KeyboardEvent('keydown', {
    key,
    bubbles: true,
    cancelable: true,
    ...options,
  })
}

describe('useModalDialog integration', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
  })

  it('focuses the first field and restores focus after Escape closes the dialog', async () => {
    const onClose = vi.fn()
    const opener = document.createElement('button')
    opener.textContent = 'Open'
    document.body.appendChild(opener)
    opener.focus()

    await act(async () => {
      root.render(<ProjectModal title="New Project" onClose={onClose} onSubmit={vi.fn()} />)
    })
    await act(async () => {
      await new Promise(resolve => window.setTimeout(resolve, 0))
    })

    const input = container.querySelector<HTMLInputElement>('input')!
    expect(document.activeElement).toBe(input)

    await act(async () => {
      document.dispatchEvent(keyboardEvent('Escape'))
    })

    expect(onClose).toHaveBeenCalledTimes(1)

    await act(async () => {
      root.unmount()
    })

    expect(document.activeElement).toBe(opener)
    opener.remove()
  })

  it.each([
    ['removed', (opener: HTMLButtonElement) => opener.remove()],
    ['disabled', (opener: HTMLButtonElement) => { opener.disabled = true }],
  ])('returns focus to the fallback target when the opener was %s', async (_label, breakOpener) => {
    // Context-menu items unmount and shelf buttons can be disabled while the
    // dialog is open; either way focus must not be stranded on <body>.
    const opener = document.createElement('button')
    opener.textContent = 'Runtime Settings'
    const fallback = document.createElement('button')
    fallback.textContent = 'Checkout group actions'
    fallback.setAttribute('data-return-target', 'group')
    document.body.append(opener, fallback)
    opener.focus()

    function SelectorDialog() {
      const dialogRef = useRef<HTMLDivElement>(null)
      useModalDialog({ dialogRef, onClose: vi.fn(), returnFocusSelector: '[data-return-target="group"]' })
      return <div ref={dialogRef} role="dialog" aria-label="Runtime Settings"><button type="button">Close</button></div>
    }

    await act(async () => {
      root.render(<SelectorDialog />)
    })
    breakOpener(opener)
    await act(async () => {
      root.unmount()
    })

    expect(document.activeElement).toBe(fallback)
    opener.remove()
    fallback.remove()
  })

  it('keeps Tab focus inside the dialog', async () => {
    await act(async () => {
      root.render(<ProjectModal title="New Project" onClose={vi.fn()} onSubmit={vi.fn()} />)
    })
    await act(async () => {
      await new Promise(resolve => window.setTimeout(resolve, 0))
    })

    const buttons = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
    const enabledButtons = buttons.filter(button => !button.disabled)
    const lastEnabledButton = enabledButtons[enabledButtons.length - 1]
    lastEnabledButton.focus()

    await act(async () => {
      document.dispatchEvent(keyboardEvent('Tab'))
    })

    expect(document.activeElement).toBe(enabledButtons[0])

    enabledButtons[0].focus()
    await act(async () => {
      document.dispatchEvent(keyboardEvent('Tab', { shiftKey: true }))
    })

    expect(document.activeElement).toBe(lastEnabledButton)
  })

  it('does not reset focus when a parent rerender changes the close callback identity', async () => {
    await act(async () => {
      root.render(<ProjectModal title="New Project" onClose={() => undefined} onSubmit={vi.fn()} />)
    })
    await act(async () => {
      await new Promise(resolve => window.setTimeout(resolve, 0))
    })

    const cancel = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Cancel')!
    cancel.focus()

    await act(async () => {
      root.render(<ProjectModal title="New Project" onClose={() => undefined} onSubmit={vi.fn()} />)
      await Promise.resolve()
    })

    expect(document.activeElement).toBe(cancel)
  })

  it('consumes Escape for the top dialog even when closing is temporarily disabled', async () => {
    function LockedDialog() {
      const dialogRef = useRef<HTMLDivElement | null>(null)
      useModalDialog({ dialogRef, onClose: undefined })
      return <div ref={dialogRef} role="dialog" aria-modal="true"><button type="button">Working</button></div>
    }

    await act(async () => {
      root.render(<LockedDialog />)
      await new Promise(resolve => window.setTimeout(resolve, 0))
    })
    const globalShortcut = vi.fn()
    document.addEventListener('keydown', globalShortcut)
    const escape = keyboardEvent('Escape')
    await act(async () => {
      document.dispatchEvent(escape)
    })
    document.removeEventListener('keydown', globalShortcut)

    expect(escape.defaultPrevented).toBe(true)
    expect(globalShortcut).not.toHaveBeenCalled()
  })

  it('recovers Tab focus if another script moves focus outside the open dialog', async () => {
    await act(async () => {
      root.render(<ProjectModal title="New Project" onClose={vi.fn()} onSubmit={vi.fn()} />)
    })
    await act(async () => {
      await new Promise(resolve => window.setTimeout(resolve, 0))
    })

    const outside = document.createElement('button')
    document.body.appendChild(outside)
    const enabled = Array.from(container.querySelectorAll<HTMLElement>('input, button:not([disabled])'))

    outside.focus()
    await act(async () => {
      document.dispatchEvent(keyboardEvent('Tab'))
    })
    expect(document.activeElement).toBe(enabled[0])

    outside.focus()
    await act(async () => {
      document.dispatchEvent(keyboardEvent('Tab', { shiftKey: true }))
    })
    expect(document.activeElement).toBe(enabled[enabled.length - 1])
    outside.remove()
  })
})
