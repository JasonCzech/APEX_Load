// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ProjectModal from '../components/ProjectModal'

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
})
