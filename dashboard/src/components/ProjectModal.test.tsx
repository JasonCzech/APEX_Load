// @vitest-environment jsdom

import { act, StrictMode } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ProjectModal from './ProjectModal'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('ProjectModal', () => {
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
    vi.clearAllMocks()
  })

  const typeName = async (value: string) => {
    const input = container.querySelector<HTMLInputElement>('.project-modal-field input')
    expect(input).toBeTruthy()
    if (!input) return
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
      setter?.call(input, value)
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
  }

  const submitForm = async () => {
    await act(async () => {
      container.querySelector<HTMLFormElement>('form.project-modal')
        ?.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    })
  }

  it('keeps the dialog open and shows the reason when the create fails', async () => {
    const onClose = vi.fn()
    const onSubmit = vi.fn().mockRejectedValue(new Error('An application with this name already exists.'))
    await act(async () => {
      root.render(<ProjectModal title="New Application" onClose={onClose} onSubmit={onSubmit} />)
    })

    await typeName('Wallet')
    await submitForm()

    expect(onClose).not.toHaveBeenCalled()
    expect(container.querySelector('.project-modal-error')?.textContent)
      .toBe('An application with this name already exists.')
  })

  it('re-enables dismissal when a save hangs past the watchdog, and ignores the zombie resolution', async () => {
    vi.useFakeTimers()
    try {
      const onClose = vi.fn()
      let resolveHung: () => void = () => {}
      const onSubmit = vi.fn().mockImplementation(
        () => new Promise<void>(resolve => { resolveHung = resolve }),
      )
      await act(async () => {
        root.render(<ProjectModal onClose={onClose} onSubmit={onSubmit} />)
      })

      await typeName('Hung Create')
      await submitForm()
      const cancel = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Cancel')
      expect(cancel?.disabled).toBe(true)

      await act(async () => {
        vi.advanceTimersByTime(15_000)
      })

      expect(container.textContent).toContain('the create may still complete')
      const cancelAfter = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Cancel')
      expect(cancelAfter?.disabled).toBe(false)
      await act(async () => cancelAfter?.click())
      expect(onClose).toHaveBeenCalledTimes(1)

      // The abandoned save settling later must not fire onClose again — the
      // operator may have reopened the dialog by then, and a second onClose
      // would slam the new one shut.
      await act(async () => resolveHung())
      expect(onClose).toHaveBeenCalledTimes(1)
    } finally {
      vi.useRealTimers()
    }
  })

  it('blocks dismissal while the create is in flight', async () => {
    const onClose = vi.fn()
    let resolveSubmit: () => void = () => {}
    const onSubmit = vi.fn().mockImplementation(
      () => new Promise<void>(resolve => { resolveSubmit = resolve }),
    )
    await act(async () => {
      root.render(<ProjectModal onClose={onClose} onSubmit={onSubmit} />)
    })

    await typeName('Foo')
    await submitForm()

    const cancel = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Cancel')
    const close = container.querySelector<HTMLButtonElement>('.project-modal-close')
    expect(cancel?.disabled).toBe(true)
    expect(close?.disabled).toBe(true)
    await act(async () => cancel?.click())
    await act(async () => close?.click())
    expect(onClose).not.toHaveBeenCalled()

    await act(async () => resolveSubmit())
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('closes after a successful submit under Strict Mode effect replay', async () => {
    const onClose = vi.fn()
    const onSubmit = vi.fn().mockResolvedValue(undefined)
    await act(async () => {
      root.render(
        <StrictMode>
          <ProjectModal title="New Release" onClose={onClose} onSubmit={onSubmit} />
        </StrictMode>,
      )
    })

    await typeName('2609')
    await submitForm()

    expect(onSubmit).toHaveBeenCalledWith('2609')
    expect(onClose).toHaveBeenCalledTimes(1)
  })
})
