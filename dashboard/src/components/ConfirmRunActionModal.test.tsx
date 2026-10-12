// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ConfirmRunActionModal from './ConfirmRunActionModal'
import { runActionConfirmationCopy } from './runActionConfirmation'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('ConfirmRunActionModal', () => {
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

  async function render(props: Partial<Parameters<typeof ConfirmRunActionModal>[0]> = {}) {
    const onConfirm = vi.fn()
    const onCancel = vi.fn()
    await act(async () => {
      root.render(
        <ConfirmRunActionModal
          action="stop"
          runName="Checkout Peak"
          runRef="RUN-20260817-A"
          onConfirm={onConfirm}
          onCancel={onCancel}
          {...props}
        />,
      )
    })
    return { onConfirm, onCancel }
  }

  function button(label: string) {
    return Array.from(container.querySelectorAll('button'))
      .find(candidate => candidate.textContent?.trim() === label) as HTMLButtonElement | undefined
  }

  async function click(element: HTMLElement) {
    await act(async () => {
      element.click()
    })
  }

  it('names the run and the action being confirmed', async () => {
    await render()

    const dialog = container.querySelector('.confirm-run-action-modal')
    expect(dialog?.getAttribute('role')).toBe('dialog')
    expect(dialog?.getAttribute('aria-modal')).toBe('true')
    expect(container.textContent).toContain('Stop this run?')
    expect(container.textContent).toContain('cannot be resumed')
    expect(container.textContent).toContain('Checkout Peak')
    expect(container.textContent).toContain('RUN-20260817-A')
  })

  it('confirms and cancels through the expected buttons', async () => {
    const { onConfirm, onCancel } = await render()

    await click(button('Stop run') as HTMLButtonElement)
    expect(onConfirm).toHaveBeenCalledTimes(1)
    expect(onCancel).not.toHaveBeenCalled()

    await click(button('Cancel') as HTMLButtonElement)
    expect(onCancel).toHaveBeenCalledTimes(1)
  })

  it('cancels on Escape so a mis-click never sends the action', async () => {
    const { onConfirm, onCancel } = await render({ action: 'abort' })

    await act(async () => {
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    })

    expect(onCancel).toHaveBeenCalledTimes(1)
    expect(onConfirm).not.toHaveBeenCalled()
  })

  it('opens with focus on Cancel rather than the destructive button', async () => {
    await render({ action: 'abort' })
    // useModalDialog applies initial focus on a macrotask.
    await act(async () => {
      await new Promise(resolve => setTimeout(resolve, 0))
    })

    expect(document.activeElement).toBe(button('Cancel'))
  })

  it('renders the copy registered for each confirmable action', async () => {
    for (const action of ['start', 'pause', 'stop', 'abort'] as const) {
      await render({ action })
      const copy = runActionConfirmationCopy(action)
      expect(container.textContent).toContain(copy.title)
      expect(button(copy.confirmLabel)).toBeDefined()
    }
  })
})
