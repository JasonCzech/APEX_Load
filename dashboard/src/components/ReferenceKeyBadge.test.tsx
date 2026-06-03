// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ReferenceKeyBadge from './ReferenceKeyBadge'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('ReferenceKeyBadge', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    Object.defineProperty(document, 'execCommand', {
      configurable: true,
      value: vi.fn(() => true),
    })
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.restoreAllMocks()
  })

  it('renders and copies a reference key', async () => {
    await act(async () => {
      root.render(<ReferenceKeyBadge value="LOC-RUN-20260603-001" />)
    })

    expect(container.textContent).toContain('LOC-RUN-20260603-001')
    const button = container.querySelector('button') as HTMLButtonElement | null
    expect(button?.getAttribute('aria-label')).toBe('Copy LOC-RUN-20260603-001')

    await act(async () => {
      button?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(container.textContent).toContain('Copied')
    expect(document.execCommand).toHaveBeenCalledWith('copy')
  })
})
