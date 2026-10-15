// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import Header from './Header'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('Header', () => {
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

  it('exposes brand navigation as a keyboard-operable button', async () => {
    const onNavigate = vi.fn()
    await act(async () => root.render(<Header connected onNavigate={onNavigate} />))

    const button = container.querySelector<HTMLButtonElement>('button[aria-label="Go to Dashboard"]')
    expect(button).not.toBeNull()
    await act(async () => button?.click())
    expect(onNavigate).toHaveBeenCalledTimes(1)
  })
})
